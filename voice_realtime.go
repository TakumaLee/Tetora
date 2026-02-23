package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// --- Voice Realtime Config ---

type VoiceWakeConfig struct {
	Enabled   bool     `json:"enabled,omitempty"`
	WakeWords []string `json:"wakeWords,omitempty"` // ["テトラ", "tetora", "hey tetora"]
	Threshold float64  `json:"threshold,omitempty"` // VAD sensitivity (0.0-1.0), default 0.6
}

type VoiceRealtimeConfig struct {
	Enabled  bool   `json:"enabled,omitempty"`
	Provider string `json:"provider,omitempty"` // "openai"
	Model    string `json:"model,omitempty"`    // "gpt-4o-realtime-preview"
	APIKey   string `json:"apiKey,omitempty"`   // $ENV_VAR supported
	Voice    string `json:"voice,omitempty"`    // "alloy", "shimmer", etc.
}

// --- Voice Realtime Engine ---

// VoiceRealtimeEngine manages wake word detection and OpenAI Realtime API relay.
type VoiceRealtimeEngine struct {
	cfg         *Config
	voiceEngine *VoiceEngine
	sessions    sync.Map // sessionID -> *realtimeSession
}

// newVoiceRealtimeEngine initializes the voice realtime engine.
func newVoiceRealtimeEngine(cfg *Config, voiceEngine *VoiceEngine) *VoiceRealtimeEngine {
	vre := &VoiceRealtimeEngine{
		cfg:         cfg,
		voiceEngine: voiceEngine,
	}
	logInfo("voice realtime engine initialized")
	return vre
}

// --- Wake Word Detection ---

// wakeSession represents a wake word detection session over WebSocket.
type wakeSession struct {
	id          string
	conn        *wsConn
	cfg         *Config
	voiceEngine *VoiceEngine
	mu          sync.Mutex
	closed      bool
	audioBuffer *bytes.Buffer // accumulate audio until silence or wake word
	lastAudio   time.Time
}

// handleWakeWebSocket handles the /ws/voice/wake WebSocket endpoint.
func (vre *VoiceRealtimeEngine) handleWakeWebSocket(w http.ResponseWriter, r *http.Request) {
	// Upgrade to WebSocket.
	conn, err := wsUpgrade(w, r)
	if err != nil {
		logWarn("wake websocket upgrade failed", "error", err)
		http.Error(w, "upgrade failed", http.StatusBadRequest)
		return
	}

	sessionID := generateSessionID()
	sess := &wakeSession{
		id:          sessionID,
		conn:        conn,
		cfg:         vre.cfg,
		voiceEngine: vre.voiceEngine,
		audioBuffer: &bytes.Buffer{},
		lastAudio:   time.Now(),
	}

	logInfo("wake session started", "sessionID", sessionID)

	// Run session in goroutine.
	go sess.run()
}

func (ws *wakeSession) run() {
	defer func() {
		ws.mu.Lock()
		ws.closed = true
		ws.mu.Unlock()
		ws.conn.Close()
		logInfo("wake session closed", "sessionID", ws.id)
	}()

	// Start silence detector (detects end of utterance).
	go ws.silenceDetector()

	for {
		// Read audio chunk from client.
		opcode, payload, err := ws.conn.ReadMessage()
		if err != nil {
			if !ws.closed {
				logDebug("wake websocket read error", "sessionID", ws.id, "error", err)
			}
			return
		}

		if opcode == wsBinary {
			// Accumulate audio.
			ws.mu.Lock()
			ws.audioBuffer.Write(payload)
			ws.lastAudio = time.Now()
			ws.mu.Unlock()
		}
	}
}

func (ws *wakeSession) silenceDetector() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		<-ticker.C
		ws.mu.Lock()
		if ws.closed {
			ws.mu.Unlock()
			return
		}

		// Check if silence threshold exceeded (no audio for 1 second).
		silenceDuration := time.Since(ws.lastAudio)
		if silenceDuration > 1*time.Second && ws.audioBuffer.Len() > 0 {
			// Process accumulated audio.
			audioData := ws.audioBuffer.Bytes()
			ws.audioBuffer.Reset()
			ws.mu.Unlock()

			ws.processAudio(audioData)
		} else {
			ws.mu.Unlock()
		}
	}
}

func (ws *wakeSession) processAudio(audioData []byte) {
	// Run STT on audio chunk.
	if ws.voiceEngine == nil || ws.voiceEngine.stt == nil {
		logDebug("wake stt not enabled", "sessionID", ws.id)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := ws.voiceEngine.Transcribe(ctx, bytes.NewReader(audioData), STTOptions{
		Format: "webm", // assume WebM Opus from browser
	})
	if err != nil {
		logDebug("wake stt error", "sessionID", ws.id, "error", err)
		return
	}

	logDebug("wake stt result", "sessionID", ws.id, "text", result.Text)

	// Check for wake word.
	wakeWords := ws.cfg.Voice.Wake.WakeWords
	if len(wakeWords) == 0 {
		wakeWords = []string{"テトラ", "tetora"} // default
	}

	lowerText := strings.ToLower(result.Text)
	wakeDetected := false
	for _, ww := range wakeWords {
		if strings.Contains(lowerText, strings.ToLower(ww)) {
			wakeDetected = true
			break
		}
	}

	if wakeDetected {
		// Send wake detection event to client.
		ws.sendEvent("wake_detected", map[string]any{
			"text": result.Text,
		})
		logInfo("wake word detected", "sessionID", ws.id, "text", result.Text)
	}
}

func (ws *wakeSession) sendEvent(eventType string, data any) {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	if ws.closed {
		return
	}

	msg := map[string]any{
		"type": eventType,
		"data": data,
	}
	jsonData, _ := json.Marshal(msg)
	ws.conn.WriteMessage(wsText, jsonData)
}

// --- OpenAI Realtime API Relay ---

// realtimeSession represents an OpenAI Realtime API relay session.
type realtimeSession struct {
	id           string
	clientConn   *wsConn
	openaiConn   *wsConn
	cfg          *Config
	ctx          context.Context
	cancel       context.CancelFunc
	mu           sync.Mutex
	closed       bool
	voiceEngine  *VoiceEngine
	toolRegistry *ToolRegistry
}

// handleRealtimeWebSocket handles the /ws/voice/realtime WebSocket endpoint.
func (vre *VoiceRealtimeEngine) handleRealtimeWebSocket(w http.ResponseWriter, r *http.Request) {
	// Upgrade to WebSocket.
	conn, err := wsUpgrade(w, r)
	if err != nil {
		logWarn("realtime websocket upgrade failed", "error", err)
		http.Error(w, "upgrade failed", http.StatusBadRequest)
		return
	}

	sessionID := generateSessionID()
	ctx, cancel := context.WithCancel(context.Background())

	sess := &realtimeSession{
		id:           sessionID,
		clientConn:   conn,
		cfg:          vre.cfg,
		ctx:          ctx,
		cancel:       cancel,
		voiceEngine:  vre.voiceEngine,
		toolRegistry: vre.cfg.toolRegistry,
	}

	vre.sessions.Store(sessionID, sess)
	logInfo("realtime session started", "sessionID", sessionID)

	// Run session in goroutine.
	go sess.run()
}

func (rs *realtimeSession) run() {
	defer func() {
		rs.mu.Lock()
		rs.closed = true
		rs.mu.Unlock()

		if rs.openaiConn != nil {
			rs.openaiConn.Close()
		}
		rs.clientConn.Close()
		rs.cancel()

		logInfo("realtime session closed", "sessionID", rs.id)
	}()

	// Connect to OpenAI Realtime API.
	if err := rs.connectOpenAI(); err != nil {
		logError("realtime openai connect failed", "sessionID", rs.id, "error", err)
		rs.sendError(err.Error())
		return
	}

	// Start bidirectional relay.
	var wg sync.WaitGroup
	wg.Add(2)

	// Client -> OpenAI
	go func() {
		defer wg.Done()
		rs.relayClientToOpenAI()
	}()

	// OpenAI -> Client
	go func() {
		defer wg.Done()
		rs.relayOpenAIToClient()
	}()

	wg.Wait()
}

func (rs *realtimeSession) connectOpenAI() error {
	apiKey := rs.cfg.Voice.Realtime.APIKey
	if apiKey == "" {
		return fmt.Errorf("openai api key not configured")
	}

	model := rs.cfg.Voice.Realtime.Model
	if model == "" {
		model = "gpt-4o-realtime-preview-2024-12-17"
	}

	// OpenAI Realtime API endpoint.
	realtimeURL := fmt.Sprintf("wss://api.openai.com/v1/realtime?model=%s", url.QueryEscape(model))

	// Create WebSocket connection with Authorization header.
	conn, err := rs.wsConnectWithAuth(realtimeURL, apiKey)
	if err != nil {
		return fmt.Errorf("connect openai realtime: %w", err)
	}

	rs.openaiConn = conn
	logInfo("realtime connected to openai", "sessionID", rs.id, "model", model)

	// Send session.update with system prompt and tools.
	if err := rs.configureSession(); err != nil {
		return fmt.Errorf("configure session: %w", err)
	}

	return nil
}

func (rs *realtimeSession) configureSession() error {
	// Build system prompt with agent context.
	systemPrompt := "You are Tetora, an AI agent orchestrator. You are helpful, concise, and proactive."

	// Build tool definitions.
	tools := rs.buildToolDefinitions()

	// Send session.update event.
	sessionUpdate := map[string]any{
		"type": "session.update",
		"session": map[string]any{
			"modalities":   []string{"text", "audio"},
			"instructions": systemPrompt,
			"voice":        rs.getVoice(),
			"tools":        tools,
			"tool_choice":  "auto",
		},
	}

	jsonData, err := json.Marshal(sessionUpdate)
	if err != nil {
		return fmt.Errorf("marshal session update: %w", err)
	}

	rs.mu.Lock()
	defer rs.mu.Unlock()

	if rs.closed {
		return fmt.Errorf("session closed")
	}

	return rs.openaiConn.WriteMessage(wsText, jsonData)
}

func (rs *realtimeSession) getVoice() string {
	voice := rs.cfg.Voice.Realtime.Voice
	if voice == "" {
		voice = "alloy"
	}
	return voice
}

func (rs *realtimeSession) buildToolDefinitions() []map[string]any {
	if rs.toolRegistry == nil {
		return nil
	}

	var tools []map[string]any

	// Add built-in tools.
	for _, tool := range rs.toolRegistry.tools {
		// Parse InputSchema to extract parameters.
		var schema map[string]any
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			logWarn("failed to parse tool input schema", "tool", tool.Name, "error", err)
			continue
		}

		tools = append(tools, map[string]any{
			"type":        "function",
			"name":        tool.Name,
			"description": tool.Description,
			"parameters":  schema,
		})
	}

	return tools
}

func (rs *realtimeSession) relayClientToOpenAI() {
	for {
		opcode, payload, err := rs.clientConn.ReadMessage()
		if err != nil {
			if !rs.closed {
				logDebug("realtime client read error", "sessionID", rs.id, "error", err)
			}
			return
		}

		rs.mu.Lock()
		if rs.closed || rs.openaiConn == nil {
			rs.mu.Unlock()
			return
		}

		// Forward to OpenAI.
		if err := rs.openaiConn.WriteMessage(opcode, payload); err != nil {
			logDebug("realtime openai write error", "sessionID", rs.id, "error", err)
			rs.mu.Unlock()
			return
		}
		rs.mu.Unlock()
	}
}

func (rs *realtimeSession) relayOpenAIToClient() {
	for {
		opcode, payload, err := rs.openaiConn.ReadMessage()
		if err != nil {
			if !rs.closed {
				logDebug("realtime openai read error", "sessionID", rs.id, "error", err)
			}
			return
		}

		// Check for tool calls (function_call events).
		if opcode == wsText {
			rs.handleOpenAIEvent(payload)
		}

		rs.mu.Lock()
		if rs.closed {
			rs.mu.Unlock()
			return
		}

		// Forward to client.
		if err := rs.clientConn.WriteMessage(opcode, payload); err != nil {
			logDebug("realtime client write error", "sessionID", rs.id, "error", err)
			rs.mu.Unlock()
			return
		}
		rs.mu.Unlock()
	}
}

func (rs *realtimeSession) handleOpenAIEvent(payload []byte) {
	var event map[string]any
	if err := json.Unmarshal(payload, &event); err != nil {
		return
	}

	eventType, _ := event["type"].(string)
	if eventType != "response.function_call_arguments.done" {
		return
	}

	// Extract function call details.
	callID, _ := event["call_id"].(string)
	name, _ := event["name"].(string)
	argsStr, _ := event["arguments"].(string)

	logInfo("realtime function call", "sessionID", rs.id, "callID", callID, "name", name)

	// Execute tool.
	go rs.executeToolCall(callID, name, argsStr)
}

func (rs *realtimeSession) executeToolCall(callID, name, argsJSON string) {
	if rs.toolRegistry == nil {
		rs.sendToolResult(callID, "", fmt.Errorf("tool registry not available"))
		return
	}

	tool := rs.toolRegistry.tools[name]
	if tool == nil {
		rs.sendToolResult(callID, "", fmt.Errorf("tool not found: %s", name))
		return
	}

	// Execute tool with proper handler signature.
	ctx, cancel := context.WithTimeout(rs.ctx, 30*time.Second)
	defer cancel()

	result, err := tool.Handler(ctx, rs.cfg, json.RawMessage(argsJSON))
	if err != nil {
		rs.sendToolResult(callID, "", err)
		return
	}

	rs.sendToolResult(callID, result, nil)
}

func (rs *realtimeSession) sendToolResult(callID, result string, err error) {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	if rs.closed || rs.openaiConn == nil {
		return
	}

	output := result
	if err != nil {
		output = fmt.Sprintf("Error: %v", err)
	}

	// Send conversation.item.create with function_call_output.
	itemCreate := map[string]any{
		"type": "conversation.item.create",
		"item": map[string]any{
			"type":    "function_call_output",
			"call_id": callID,
			"output":  output,
		},
	}

	jsonData, _ := json.Marshal(itemCreate)
	rs.openaiConn.WriteMessage(wsText, jsonData)

	// Send response.create to trigger model response.
	responseCreate := map[string]any{
		"type": "response.create",
	}
	jsonData, _ = json.Marshal(responseCreate)
	rs.openaiConn.WriteMessage(wsText, jsonData)
}

func (rs *realtimeSession) sendError(msg string) {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	if rs.closed {
		return
	}

	errMsg := map[string]any{
		"type":  "error",
		"error": msg,
	}
	jsonData, _ := json.Marshal(errMsg)
	rs.clientConn.WriteMessage(wsText, jsonData)
}

func (rs *realtimeSession) wsConnectWithAuth(rawURL, apiKey string) (*wsConn, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}

	host := u.Host
	if !strings.Contains(host, ":") {
		host += ":443"
	}

	// TLS dial.
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", host, &tls.Config{})
	if err != nil {
		return nil, fmt.Errorf("tls dial: %w", err)
	}

	// Generate WebSocket key.
	keyBytes := make([]byte, 16)
	rand.Read(keyBytes)
	key := base64.StdEncoding.EncodeToString(keyBytes)

	// Send HTTP upgrade request with Authorization header.
	path := u.RequestURI()
	reqStr := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\nAuthorization: Bearer %s\r\nOpenAI-Beta: realtime=v1\r\n\r\n",
		path, u.Host, key, apiKey)
	if _, err := conn.Write([]byte(reqStr)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("write upgrade: %w", err)
	}

	// Read response.
	reader := bufio.NewReader(conn)
	statusLine, err := reader.ReadString('\n')
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("read status: %w", err)
	}
	if !strings.Contains(statusLine, "101") {
		conn.Close()
		return nil, fmt.Errorf("upgrade failed: %s", strings.TrimSpace(statusLine))
	}

	// Read headers until empty line.
	expectedAccept := wsAcceptKey(key)
	gotAccept := false
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("read headers: %w", err)
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), "sec-websocket-accept:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 && strings.TrimSpace(parts[1]) == expectedAccept {
				gotAccept = true
			}
		}
	}

	if !gotAccept {
		conn.Close()
		return nil, fmt.Errorf("handshake failed: accept key mismatch")
	}

	return &wsConn{conn: conn, reader: reader}, nil
}

// --- WebSocket Upgrade (HTTP -> WebSocket) ---

// wsUpgrade upgrades an HTTP connection to WebSocket (server-side).
func wsUpgrade(w http.ResponseWriter, r *http.Request) (*wsConn, error) {
	// Validate upgrade headers.
	if r.Method != "GET" {
		return nil, fmt.Errorf("method not allowed")
	}
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return nil, fmt.Errorf("missing upgrade header")
	}
	if !strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade") {
		return nil, fmt.Errorf("missing connection upgrade header")
	}
	wsKey := r.Header.Get("Sec-WebSocket-Key")
	if wsKey == "" {
		return nil, fmt.Errorf("missing sec-websocket-key")
	}

	// Hijack connection.
	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, fmt.Errorf("server does not support hijacking")
	}
	conn, bufrw, err := hj.Hijack()
	if err != nil {
		return nil, fmt.Errorf("hijack failed: %w", err)
	}

	// Send upgrade response.
	accept := wsAcceptKey(wsKey)
	resp := fmt.Sprintf("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", accept)
	if _, err := bufrw.Write([]byte(resp)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("write upgrade response: %w", err)
	}
	if err := bufrw.Flush(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("flush upgrade response: %w", err)
	}

	return &wsConn{conn: conn, reader: bufrw.Reader}, nil
}

// Note: wsAcceptKey is defined in discord.go

// --- WebSocket Frame Reading/Writing ---

const (
	wsText   = 1
	wsBinary = 2
	wsClose  = 8
	wsPing   = 9
	wsPong   = 10
)

func (ws *wsConn) ReadMessage() (opcode int, payload []byte, err error) {
	// Read frame header (2 bytes minimum).
	header := make([]byte, 2)
	if _, err := io.ReadFull(ws.reader, header); err != nil {
		return 0, nil, err
	}

	fin := (header[0] & 0x80) != 0
	opcode = int(header[0] & 0x0F)
	masked := (header[1] & 0x80) != 0
	payloadLen := int64(header[1] & 0x7F)

	// Read extended payload length.
	if payloadLen == 126 {
		lenBuf := make([]byte, 2)
		if _, err := io.ReadFull(ws.reader, lenBuf); err != nil {
			return 0, nil, err
		}
		payloadLen = int64(binary.BigEndian.Uint16(lenBuf))
	} else if payloadLen == 127 {
		lenBuf := make([]byte, 8)
		if _, err := io.ReadFull(ws.reader, lenBuf); err != nil {
			return 0, nil, err
		}
		payloadLen = int64(binary.BigEndian.Uint64(lenBuf))
	}

	// Read masking key if present.
	var maskKey []byte
	if masked {
		maskKey = make([]byte, 4)
		if _, err := io.ReadFull(ws.reader, maskKey); err != nil {
			return 0, nil, err
		}
	}

	// Read payload.
	payload = make([]byte, payloadLen)
	if _, err := io.ReadFull(ws.reader, payload); err != nil {
		return 0, nil, err
	}

	// Unmask payload.
	if masked {
		for i := range payload {
			payload[i] ^= maskKey[i%4]
		}
	}

	_ = fin // ignore for now (assume single-frame messages)

	// Handle control frames.
	if opcode == wsClose {
		return opcode, payload, io.EOF
	}
	if opcode == wsPing {
		// Respond with pong.
		ws.WriteMessage(wsPong, payload)
		return ws.ReadMessage() // read next message
	}

	return opcode, payload, nil
}

func (ws *wsConn) WriteMessage(opcode int, payload []byte) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	// Build frame header.
	header := []byte{0x80 | byte(opcode)} // FIN=1, opcode
	payloadLen := len(payload)

	if payloadLen < 126 {
		header = append(header, byte(payloadLen))
	} else if payloadLen <= 0xFFFF {
		header = append(header, 126)
		lenBuf := make([]byte, 2)
		binary.BigEndian.PutUint16(lenBuf, uint16(payloadLen))
		header = append(header, lenBuf...)
	} else {
		header = append(header, 127)
		lenBuf := make([]byte, 8)
		binary.BigEndian.PutUint64(lenBuf, uint64(payloadLen))
		header = append(header, lenBuf...)
	}

	// Write header and payload.
	if _, err := ws.conn.Write(header); err != nil {
		return err
	}
	if _, err := ws.conn.Write(payload); err != nil {
		return err
	}

	return nil
}

// Note: wsConn.Close is defined in discord.go

// --- Utilities ---

func generateSessionID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}
