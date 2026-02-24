package main

import (
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

// BrowserRelay manages WebSocket connections from Chrome extensions.
type BrowserRelay struct {
	mu      sync.RWMutex
	cfg     *BrowserRelayConfig
	conn    net.Conn                      // current extension WebSocket connection
	pending map[string]chan relayResponse  // request ID -> response channel
	server  *http.Server
}

type relayRequest struct {
	ID     string          `json:"id"`
	Action string          `json:"action"` // navigate, content, click, type, screenshot, eval
	Params json.RawMessage `json:"params"`
}

type relayResponse struct {
	ID     string `json:"id"`
	Result string `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

// Global browser relay instance.
var globalBrowserRelay *BrowserRelay

func newBrowserRelay(cfg *BrowserRelayConfig) *BrowserRelay {
	return &BrowserRelay{
		cfg:     cfg,
		pending: make(map[string]chan relayResponse),
	}
}

// Start launches the relay HTTP server on the configured port.
func (br *BrowserRelay) Start(ctx context.Context) error {
	port := br.cfg.Port
	if port == 0 {
		port = 18792
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/relay/ws", br.handleWebSocket)
	mux.HandleFunc("/relay/health", br.handleHealth)
	mux.HandleFunc("/relay/status", br.handleStatus)

	// Tool endpoints (called by agent tools).
	mux.HandleFunc("/relay/navigate", br.handleToolRequest("navigate"))
	mux.HandleFunc("/relay/content", br.handleToolRequest("content"))
	mux.HandleFunc("/relay/click", br.handleToolRequest("click"))
	mux.HandleFunc("/relay/type", br.handleToolRequest("type"))
	mux.HandleFunc("/relay/screenshot", br.handleToolRequest("screenshot"))
	mux.HandleFunc("/relay/eval", br.handleToolRequest("eval"))

	br.server = &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", port),
		Handler: mux,
	}

	logInfo("browser relay starting", "port", port)
	go func() {
		<-ctx.Done()
		br.server.Close()
	}()
	return br.server.ListenAndServe()
}

func (br *BrowserRelay) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (br *BrowserRelay) handleStatus(w http.ResponseWriter, r *http.Request) {
	br.mu.RLock()
	connected := br.conn != nil
	pending := len(br.pending)
	br.mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"connected": connected,
		"pending":   pending,
	})
}

// handleWebSocket performs the WebSocket upgrade and manages the extension connection.
// Uses stdlib-only WebSocket (same pattern as homeassistant.go in this project).
func (br *BrowserRelay) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Validate token if configured.
	if br.cfg.Token != "" {
		token := r.URL.Query().Get("token")
		if token != br.cfg.Token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	// WebSocket upgrade (RFC 6455).
	if r.Header.Get("Upgrade") != "websocket" {
		http.Error(w, "expected websocket", http.StatusBadRequest)
		return
	}

	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		http.Error(w, "missing Sec-WebSocket-Key", http.StatusBadRequest)
		return
	}

	acceptKey := computeWebSocketAccept(key)

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack not supported", http.StatusInternalServerError)
		return
	}
	conn, bufrw, err := hj.Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Send upgrade response.
	bufrw.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
	bufrw.WriteString("Upgrade: websocket\r\n")
	bufrw.WriteString("Connection: Upgrade\r\n")
	bufrw.WriteString("Sec-WebSocket-Accept: " + acceptKey + "\r\n\r\n")
	bufrw.Flush()

	br.mu.Lock()
	if br.conn != nil {
		br.conn.Close() // Close old connection.
	}
	br.conn = conn
	br.mu.Unlock()

	logInfo("browser extension connected", "remote", conn.RemoteAddr().String())

	// Read loop: read responses from extension.
	br.readLoop(conn)

	br.mu.Lock()
	if br.conn == conn {
		br.conn = nil
	}
	br.mu.Unlock()
	logInfo("browser extension disconnected")
}

func computeWebSocketAccept(key string) string {
	const magic = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	h := sha1.New()
	h.Write([]byte(key + magic))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

func (br *BrowserRelay) readLoop(conn net.Conn) {
	for {
		data, err := relayWSReadMessage(conn)
		if err != nil {
			return
		}
		var resp relayResponse
		if err := json.Unmarshal(data, &resp); err != nil {
			continue
		}
		br.mu.RLock()
		ch, ok := br.pending[resp.ID]
		br.mu.RUnlock()
		if ok {
			ch <- resp
		}
	}
}

// SendCommand sends a command to the extension and waits for a response.
func (br *BrowserRelay) SendCommand(action string, params json.RawMessage, timeout time.Duration) (string, error) {
	br.mu.RLock()
	conn := br.conn
	br.mu.RUnlock()
	if conn == nil {
		return "", fmt.Errorf("no browser extension connected")
	}

	id := generateRelayID()
	req := relayRequest{ID: id, Action: action, Params: params}
	data, _ := json.Marshal(req)

	ch := make(chan relayResponse, 1)
	br.mu.Lock()
	br.pending[id] = ch
	br.mu.Unlock()
	defer func() {
		br.mu.Lock()
		delete(br.pending, id)
		br.mu.Unlock()
	}()

	if err := relayWSWriteMessage(conn, data); err != nil {
		return "", fmt.Errorf("send to extension: %w", err)
	}

	select {
	case resp := <-ch:
		if resp.Error != "" {
			return "", fmt.Errorf("extension error: %s", resp.Error)
		}
		return resp.Result, nil
	case <-time.After(timeout):
		return "", fmt.Errorf("extension timeout after %v", timeout)
	}
}

// Connected returns whether an extension is connected.
func (br *BrowserRelay) Connected() bool {
	br.mu.RLock()
	defer br.mu.RUnlock()
	return br.conn != nil
}

func generateRelayID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}

// handleToolRequest returns an HTTP handler for tool-initiated relay commands.
func (br *BrowserRelay) handleToolRequest(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
			return
		}

		result, err := br.SendCommand(action, json.RawMessage(body), 30*time.Second)
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"result": result})
	}
}

// toolBrowserRelay returns a tool handler that sends commands to the browser extension.
func toolBrowserRelay(action string) ToolHandler {
	return func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
		if globalBrowserRelay == nil || !globalBrowserRelay.Connected() {
			return "", fmt.Errorf("browser extension not connected. Install the Tetora Chrome extension and enable it.")
		}
		result, err := globalBrowserRelay.SendCommand(action, input, 30*time.Second)
		if err != nil {
			return "", err
		}
		return result, nil
	}
}

// relayWSReadMessage reads a single WebSocket frame (text/binary).
// Minimal implementation for relay use — same pattern as homeassistant.go.
func relayWSReadMessage(conn net.Conn) ([]byte, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(conn, header); err != nil {
		return nil, err
	}

	opcode := header[0] & 0x0F

	// Handle close frame.
	if opcode == 0x08 {
		return nil, fmt.Errorf("received close frame")
	}

	masked := header[1]&0x80 != 0
	payloadLen := int(header[1] & 0x7f)
	switch payloadLen {
	case 126:
		ext := make([]byte, 2)
		if _, err := io.ReadFull(conn, ext); err != nil {
			return nil, err
		}
		payloadLen = int(ext[0])<<8 | int(ext[1])
	case 127:
		ext := make([]byte, 8)
		if _, err := io.ReadFull(conn, ext); err != nil {
			return nil, err
		}
		payloadLen = int(ext[4])<<24 | int(ext[5])<<16 | int(ext[6])<<8 | int(ext[7])
	}

	// Safety: limit frame size to 16MB.
	if payloadLen > 16*1024*1024 {
		return nil, fmt.Errorf("frame too large: %d bytes", payloadLen)
	}

	var maskKey [4]byte
	if masked {
		if _, err := io.ReadFull(conn, maskKey[:]); err != nil {
			return nil, err
		}
	}

	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(conn, payload); err != nil {
		return nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= maskKey[i%4]
		}
	}
	return payload, nil
}

// relayWSWriteMessage writes a text WebSocket frame (unmasked, server->client).
func relayWSWriteMessage(conn net.Conn, data []byte) error {
	frame := []byte{0x81} // FIN + text opcode
	l := len(data)
	switch {
	case l <= 125:
		frame = append(frame, byte(l))
	case l <= 65535:
		frame = append(frame, 126, byte(l>>8), byte(l))
	default:
		frame = append(frame, 127, 0, 0, 0, 0, byte(l>>24), byte(l>>16), byte(l>>8), byte(l))
	}
	frame = append(frame, data...)
	_, err := conn.Write(frame)
	return err
}
