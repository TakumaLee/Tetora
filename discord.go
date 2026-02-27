package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1"
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

// --- Discord Config ---

// DiscordBotConfig holds configuration for the Discord bot integration.
type DiscordBotConfig struct {
	Enabled        bool                        `json:"enabled"`
	BotToken       string                      `json:"botToken"`            // $ENV_VAR supported
	GuildID        string                      `json:"guildID,omitempty"`   // restrict to specific guild
	ChannelID         string                      `json:"channelID,omitempty"`          // restrict to specific channel (legacy, mention-only)
	ChannelIDs        []string                    `json:"channelIDs,omitempty"`         // direct-reply channels (no @ needed)
	MentionChannelIDs []string                    `json:"mentionChannelIDs,omitempty"`  // @mention-only channels
	Webhooks       map[string]string           `json:"webhooks,omitempty"`  // named webhook channels, e.g. {"stock": "https://discord.com/api/webhooks/..."}
	PublicKey      string                      `json:"publicKey,omitempty"` // Ed25519 public key for interaction verification
	Components     DiscordComponentsConfig     `json:"components,omitempty"`
	ThreadBindings DiscordThreadBindingsConfig `json:"threadBindings,omitempty"` // P14.2: per-thread agent isolation
	Reactions      DiscordReactionsConfig      `json:"reactions,omitempty"`      // P14.3: lifecycle reactions
	ForumBoard     DiscordForumBoardConfig     `json:"forumBoard,omitempty"`     // P14.4: forum task board
	Voice            DiscordVoiceConfig          `json:"voice,omitempty"`            // P14.5: voice channel integration
	NotifyChannelID  string                      `json:"notifyChannelID,omitempty"`  // task notification channel (thread-per-task)
	Routes           map[string]DiscordRouteConfig `json:"routes,omitempty"`           // per-channel agent routing
}

// DiscordRouteConfig binds a Discord channel to a specific agent.
type DiscordRouteConfig struct {
	Agent string `json:"agent"`
}

// UnmarshalJSON implements backward compat: accepts both "role" and "agent".
func (d *DiscordRouteConfig) UnmarshalJSON(data []byte) error {
	type Alias DiscordRouteConfig
	var alias Alias
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}
	if alias.Agent == "" {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err == nil {
			if roleRaw, ok := raw["role"]; ok {
				var role string
				if err := json.Unmarshal(roleRaw, &role); err == nil {
					alias.Agent = role
				}
			}
		}
	}
	*d = DiscordRouteConfig(alias)
	return nil
}

// --- P14.1: Discord Components v2 ---

// DiscordComponentsConfig holds configuration for Discord interactive components.
type DiscordComponentsConfig struct {
	Enabled         bool   `json:"enabled,omitempty"`
	ReusableDefault bool   `json:"reusableDefault,omitempty"` // default for button reusability
	AccentColor     string `json:"accentColor,omitempty"`     // hex color, default "#5865F2"
}

// --- Constants ---

const (
	discordGatewayURL = "wss://gateway.discord.gg/?v=10&encoding=json"
	discordAPIBase    = "https://discord.com/api/v10"

	// Gateway opcodes.
	opDispatch       = 0
	opHeartbeat      = 1
	opIdentify       = 2
	opResume         = 6
	opReconnect      = 7
	opInvalidSession = 9
	opHello          = 10
	opHeartbeatAck   = 11

	// Gateway intents.
	intentGuildMessages  = 1 << 9
	intentDirectMessages = 1 << 12
	intentMessageContent = 1 << 15
)

// --- Gateway Types ---

type gatewayPayload struct {
	Op int              `json:"op"`
	D  json.RawMessage  `json:"d,omitempty"`
	S  *int             `json:"s,omitempty"`
	T  string           `json:"t,omitempty"`
}

type helloData struct {
	HeartbeatInterval int `json:"heartbeat_interval"`
}

type identifyData struct {
	Token      string            `json:"token"`
	Intents    int               `json:"intents"`
	Properties map[string]string `json:"properties"`
}

type resumePayload struct {
	Token     string `json:"token"`
	SessionID string `json:"session_id"`
	Seq       int    `json:"seq"`
}

type readyData struct {
	SessionID string      `json:"session_id"`
	User      discordUser `json:"user"`
}

// --- API Types ---

type discordUser struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Bot      bool   `json:"bot"`
}

type discordMessage struct {
	ID        string        `json:"id"`
	ChannelID string        `json:"channel_id"`
	GuildID   string        `json:"guild_id,omitempty"`
	Author    discordUser   `json:"author"`
	Content   string        `json:"content"`
	Mentions  []discordUser `json:"mentions,omitempty"`
}

type discordEmbed struct {
	Title       string              `json:"title,omitempty"`
	Description string              `json:"description,omitempty"`
	Color       int                 `json:"color,omitempty"`
	Fields      []discordEmbedField `json:"fields,omitempty"`
	Footer      *discordEmbedFooter `json:"footer,omitempty"`
	Timestamp   string              `json:"timestamp,omitempty"`
}

type discordEmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

type discordEmbedFooter struct {
	Text string `json:"text"`
}

// --- Minimal WebSocket Client (RFC 6455, no external deps) ---

type wsConn struct {
	conn   net.Conn
	reader *bufio.Reader
	mu     sync.Mutex // protects writes
}

// wsConnect performs the WebSocket handshake over TLS.
func wsConnect(rawURL string) (*wsConn, error) {
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

	// Send HTTP upgrade request.
	path := u.RequestURI()
	reqStr := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n",
		path, u.Host, key)
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
			val := strings.TrimSpace(line[len("sec-websocket-accept:"):])
			if val == expectedAccept {
				gotAccept = true
			}
		}
	}
	if !gotAccept {
		conn.Close()
		return nil, fmt.Errorf("invalid Sec-WebSocket-Accept")
	}

	return &wsConn{conn: conn, reader: reader}, nil
}

// wsAcceptKey computes the expected Sec-WebSocket-Accept value.
func wsAcceptKey(key string) string {
	h := sha1.New()
	h.Write([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// ReadJSON reads a WebSocket text frame and decodes JSON.
func (ws *wsConn) ReadJSON(v any) error {
	data, err := ws.readFrame()
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// WriteJSON encodes JSON and sends as a WebSocket text frame.
func (ws *wsConn) WriteJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return ws.writeFrame(1, data) // opcode 1 = text
}

// Close sends a close frame and closes the connection.
func (ws *wsConn) Close() error {
	ws.writeFrame(8, nil) // opcode 8 = close
	return ws.conn.Close()
}

// readFrame reads a single WebSocket frame (handles continuation).
func (ws *wsConn) readFrame() ([]byte, error) {
	ws.conn.SetReadDeadline(time.Now().Add(90 * time.Second))

	var result []byte
	for {
		// Read first 2 bytes.
		header := make([]byte, 2)
		if _, err := io.ReadFull(ws.reader, header); err != nil {
			return nil, err
		}

		fin := header[0]&0x80 != 0
		opcode := header[0] & 0x0F
		masked := header[1]&0x80 != 0
		payloadLen := int64(header[1] & 0x7F)

		// Close frame.
		if opcode == 8 {
			return nil, io.EOF
		}

		// Ping frame — respond with pong.
		if opcode == 9 {
			pongData := make([]byte, payloadLen)
			io.ReadFull(ws.reader, pongData)
			ws.writeFrame(10, pongData) // opcode 10 = pong
			continue
		}

		// Extended payload length.
		if payloadLen == 126 {
			ext := make([]byte, 2)
			if _, err := io.ReadFull(ws.reader, ext); err != nil {
				return nil, err
			}
			payloadLen = int64(binary.BigEndian.Uint16(ext))
		} else if payloadLen == 127 {
			ext := make([]byte, 8)
			if _, err := io.ReadFull(ws.reader, ext); err != nil {
				return nil, err
			}
			payloadLen = int64(binary.BigEndian.Uint64(ext))
		}

		// Masking key (server frames typically aren't masked, but handle it).
		var maskKey [4]byte
		if masked {
			if _, err := io.ReadFull(ws.reader, maskKey[:]); err != nil {
				return nil, err
			}
		}

		// Read payload.
		payload := make([]byte, payloadLen)
		if _, err := io.ReadFull(ws.reader, payload); err != nil {
			return nil, err
		}

		if masked {
			for i := range payload {
				payload[i] ^= maskKey[i%4]
			}
		}

		result = append(result, payload...)

		if fin {
			break
		}
	}
	return result, nil
}

// writeFrame writes a WebSocket frame (client frames are masked per RFC 6455).
func (ws *wsConn) writeFrame(opcode byte, data []byte) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	var frame []byte
	frame = append(frame, 0x80|opcode) // FIN + opcode

	length := len(data)
	if length < 126 {
		frame = append(frame, byte(length)|0x80) // mask bit set
	} else if length < 65536 {
		frame = append(frame, 126|0x80)
		frame = append(frame, byte(length>>8), byte(length))
	} else {
		frame = append(frame, 127|0x80)
		b := make([]byte, 8)
		binary.BigEndian.PutUint64(b, uint64(length))
		frame = append(frame, b...)
	}

	// Masking key.
	maskKey := make([]byte, 4)
	rand.Read(maskKey)
	frame = append(frame, maskKey...)

	// Masked payload.
	masked := make([]byte, length)
	for i := range data {
		masked[i] = data[i] ^ maskKey[i%4]
	}
	frame = append(frame, masked...)

	ws.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err := ws.conn.Write(frame)
	return err
}

// --- Discord Bot ---

// DiscordBot manages the Discord Gateway connection and message handling.
type DiscordBot struct {
	cfg       *Config
	state     *dispatchState
	sem       chan struct{}
	childSem  chan struct{}
	cron      *CronEngine

	botUserID string
	sessionID string
	seq       int
	seqMu     sync.Mutex

	client       *http.Client
	stopCh       chan struct{}
	interactions *discordInteractionState // P14.1: tracks pending component interactions
	threads      *threadBindingStore      // P14.2: per-thread agent bindings
	reactions    *discordReactionManager  // P14.3: lifecycle reactions
	approvalGate *discordApprovalGate     // P28.0: approval gate
	forumBoard   *discordForumBoard       // P14.4: forum task board
	voice        *discordVoiceManager     // P14.5: voice channel manager
	gatewayConn  *wsConn                  // P14.5: active gateway connection for voice state updates
	notifier     *discordTaskNotifier     // task notification (thread-per-task)
}

func newDiscordBot(cfg *Config, state *dispatchState, sem, childSem chan struct{}, cron *CronEngine) *DiscordBot {
	db := &DiscordBot{
		cfg:          cfg,
		state:        state,
		sem:          sem,
		childSem:     childSem,
		cron:         cron,
		client:       &http.Client{Timeout: 10 * time.Second},
		stopCh:       make(chan struct{}),
		interactions: newDiscordInteractionState(), // P14.1
		threads:      newThreadBindingStore(),      // P14.2
	}

	// P14.3: Initialize reaction manager.
	if cfg.Discord.Reactions.Enabled {
		db.reactions = newDiscordReactionManager(db, cfg.Discord.Reactions.Emojis)
		logInfo("discord lifecycle reactions enabled")
	}

	// P14.4: Initialize forum board.
	if cfg.Discord.ForumBoard.Enabled {
		db.forumBoard = newDiscordForumBoard(db, cfg.Discord.ForumBoard)
		logInfo("discord forum board enabled", "channel", cfg.Discord.ForumBoard.ForumChannelID)
	}

	// P14.5: Initialize voice manager.
	db.voice = newDiscordVoiceManager(db)
	if cfg.Discord.Voice.Enabled {
		logInfo("discord voice enabled", "auto_join_count", len(cfg.Discord.Voice.AutoJoin))
	}

	// P28.0: Initialize approval gate.
	if cfg.ApprovalGates.Enabled {
		if ch := db.notifyChannelID(); ch != "" {
			db.approvalGate = newDiscordApprovalGate(db, ch)
		}
	}

	// Task notification (thread-per-task).
	if ch := cfg.Discord.NotifyChannelID; ch != "" {
		db.notifier = newDiscordTaskNotifier(db, ch)
		logInfo("discord task notifier enabled", "channel", ch)
	}

	return db
}

// Run connects to the Discord Gateway and processes events. Blocks until stopped.
func (db *DiscordBot) Run(ctx context.Context) {
	// P14.2: Start thread binding cleanup goroutine.
	if db.threads != nil && db.cfg.Discord.ThreadBindings.Enabled {
		go startThreadCleanup(ctx, db.threads)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-db.stopCh:
			return
		default:
		}

		if err := db.connectAndRun(ctx); err != nil {
			logError("discord gateway error", "error", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-db.stopCh:
			return
		case <-time.After(5 * time.Second):
			logInfo("discord reconnecting...")
		}
	}
}

// Stop signals the bot to disconnect.
func (db *DiscordBot) Stop() {
	select {
	case <-db.stopCh:
	default:
		close(db.stopCh)
	}
}

func (db *DiscordBot) connectAndRun(ctx context.Context) error {
	ws, err := wsConnect(discordGatewayURL)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer ws.Close()

	// P14.5: Store gateway connection for voice state updates
	db.gatewayConn = ws
	defer func() { db.gatewayConn = nil }()

	// Read Hello (op 10).
	var hello gatewayPayload
	if err := ws.ReadJSON(&hello); err != nil {
		return fmt.Errorf("read hello: %w", err)
	}
	if hello.Op != opHello {
		return fmt.Errorf("expected op 10, got %d", hello.Op)
	}

	var hd helloData
	json.Unmarshal(hello.D, &hd)

	// Start heartbeat.
	hbCtx, hbCancel := context.WithCancel(ctx)
	defer hbCancel()
	go db.heartbeatLoop(hbCtx, ws, time.Duration(hd.HeartbeatInterval)*time.Millisecond)

	// Identify or Resume.
	if db.sessionID != "" {
		db.seqMu.Lock()
		seq := db.seq
		db.seqMu.Unlock()
		err = db.sendResume(ws, seq)
	} else {
		err = db.sendIdentify(ws)
	}
	if err != nil {
		return err
	}

	// Event loop.
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-db.stopCh:
			return nil
		default:
		}

		var payload gatewayPayload
		if err := ws.ReadJSON(&payload); err != nil {
			return fmt.Errorf("read: %w", err)
		}

		if payload.S != nil {
			db.seqMu.Lock()
			db.seq = *payload.S
			db.seqMu.Unlock()
		}

		switch payload.Op {
		case opDispatch:
			db.handleEvent(payload)
		case opHeartbeat:
			db.sendHeartbeatWS(ws)
		case opReconnect:
			logInfo("discord gateway reconnect requested")
			return nil
		case opInvalidSession:
			logWarn("discord invalid session")
			db.sessionID = ""
			return nil
		case opHeartbeatAck:
			// OK
		}
	}
}

func (db *DiscordBot) sendIdentify(ws *wsConn) error {
	intents := intentGuildMessages | intentDirectMessages | intentMessageContent

	// P14.5: Add voice intents if voice is enabled
	if db.cfg.Discord.Voice.Enabled {
		intents |= intentGuildVoiceStates
	}

	id := identifyData{
		Token:   db.cfg.Discord.BotToken,
		Intents: intents,
		Properties: map[string]string{
			"os": "linux", "browser": "tetora", "device": "tetora",
		},
	}
	d, _ := json.Marshal(id)
	return ws.WriteJSON(gatewayPayload{Op: opIdentify, D: d})
}

func (db *DiscordBot) sendResume(ws *wsConn, seq int) error {
	r := resumePayload{
		Token: db.cfg.Discord.BotToken, SessionID: db.sessionID, Seq: seq,
	}
	d, _ := json.Marshal(r)
	return ws.WriteJSON(gatewayPayload{Op: opResume, D: d})
}

func (db *DiscordBot) heartbeatLoop(ctx context.Context, ws *wsConn, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := db.sendHeartbeatWS(ws); err != nil {
				return
			}
		}
	}
}

func (db *DiscordBot) sendHeartbeatWS(ws *wsConn) error {
	db.seqMu.Lock()
	seq := db.seq
	db.seqMu.Unlock()
	d, _ := json.Marshal(seq)
	return ws.WriteJSON(gatewayPayload{Op: opHeartbeat, D: d})
}

// --- Event Handling ---

func (db *DiscordBot) handleEvent(payload gatewayPayload) {
	switch payload.T {
	case "READY":
		var ready readyData
		if json.Unmarshal(payload.D, &ready) == nil {
			db.botUserID = ready.User.ID
			db.sessionID = ready.SessionID
			logInfo("discord bot connected", "user", ready.User.Username, "id", ready.User.ID)

			// P14.5: Auto-join voice channels if configured
			if db.cfg.Discord.Voice.Enabled && len(db.cfg.Discord.Voice.AutoJoin) > 0 {
				go db.voice.autoJoinChannels()
			}
		}
	case "MESSAGE_CREATE":
		// P14.2: Parse with channel_type for thread detection.
		var msgT discordMessageWithType
		if json.Unmarshal(payload.D, &msgT) == nil {
			go db.handleMessageWithType(msgT.discordMessage, msgT.ChannelType)
		}
	case "VOICE_STATE_UPDATE":
		// P14.5: Handle voice state updates
		var vsu voiceStateUpdateData
		if json.Unmarshal(payload.D, &vsu) == nil {
			db.voice.handleVoiceStateUpdate(vsu)
		}
	case "VOICE_SERVER_UPDATE":
		// P14.5: Handle voice server updates
		var vsuData voiceServerUpdateData
		if json.Unmarshal(payload.D, &vsuData) == nil {
			db.voice.handleVoiceServerUpdate(vsuData)
		}
	}
}

// handleMessageWithType is the top-level message handler that checks for thread bindings
// before falling through to normal message handling. (P14.2)
func (db *DiscordBot) handleMessageWithType(msg discordMessage, channelType int) {
	logDebug("discord message received",
		"author", msg.Author.Username, "channel", msg.ChannelID,
		"content_len", len(msg.Content), "bot", msg.Author.Bot,
		"guild", msg.GuildID, "mentions", len(msg.Mentions))

	// Ignore bots.
	if msg.Author.Bot || msg.Author.ID == db.botUserID {
		return
	}

	// P14.2: Check thread bindings first.
	if db.handleThreadMessage(msg, channelType) {
		return
	}

	// Fall through to normal handling.
	db.handleMessage(msg)
}

func (db *DiscordBot) handleMessage(msg discordMessage) {
	// Ignore bots.
	if msg.Author.Bot || msg.Author.ID == db.botUserID {
		return
	}

	// Channel/guild restriction.
	if !db.isAllowedChannel(msg.ChannelID) {
		return
	}
	if db.cfg.Discord.GuildID != "" && msg.GuildID != db.cfg.Discord.GuildID {
		return
	}

	// Direct channels respond to all messages; mention channels require @; DMs always accepted.
	mentioned := discordIsMentioned(msg.Mentions, db.botUserID)
	isDM := msg.GuildID == ""
	isDirect := db.isDirectChannel(msg.ChannelID)
	logDebug("discord message filter",
		"mentioned", mentioned, "isDM", isDM, "isDirect", isDirect,
		"channel", msg.ChannelID, "author", msg.Author.Username)
	if !mentioned && !isDM && !isDirect {
		return
	}

	text := discordStripMention(msg.Content, db.botUserID)
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}

	// P14.4: Forum board commands (/assign, /status) — available in any context.
	if db.forumBoard != nil && db.forumBoard.isConfigured() {
		if strings.HasPrefix(text, "/assign") {
			args := strings.TrimPrefix(text, "/assign")
			reply := db.forumBoard.handleAssignCommand(msg.ChannelID, msg.GuildID, args)
			db.sendMessage(msg.ChannelID, reply)
			return
		}
		if strings.HasPrefix(text, "/status") {
			args := strings.TrimPrefix(text, "/status")
			reply := db.forumBoard.handleStatusCommand(msg.ChannelID, args)
			db.sendMessage(msg.ChannelID, reply)
			return
		}
	}

	// P14.5: Voice channel commands (/vc join|leave|status)
	if strings.HasPrefix(text, "/vc") {
		argsStr := strings.TrimPrefix(text, "/vc")
		args := strings.Fields(strings.TrimSpace(argsStr))
		db.handleVoiceCommand(msg, args)
		return
	}

	// Command handling.
	if strings.HasPrefix(text, "!") {
		db.handleCommand(msg, text[1:])
		return
	}

	// Per-channel route binding (highest priority).
	if route, ok := db.cfg.Discord.Routes[msg.ChannelID]; ok && route.Agent != "" {
		db.handleDirectRoute(msg, text, route.Agent)
		return
	}

	if db.cfg.SmartDispatch.Enabled {
		db.handleRoute(msg, text)
	} else if db.cfg.DefaultAgent != "" {
		// No smart dispatch — route directly to the system default agent.
		db.handleDirectRoute(msg, text, db.cfg.DefaultAgent)
	} else {
		db.sendMessage(msg.ChannelID, "Smart dispatch is not enabled. Use `!help` for commands.")
	}
}

// discordIsMentioned checks if the bot user ID appears in the mentions list.
func discordIsMentioned(mentions []discordUser, botID string) bool {
	for _, m := range mentions {
		if m.ID == botID {
			return true
		}
	}
	return false
}

// discordStripMention removes bot mentions from content.
func discordStripMention(content, botID string) string {
	if botID == "" {
		return content
	}
	content = strings.ReplaceAll(content, "<@"+botID+">", "")
	content = strings.ReplaceAll(content, "<@!"+botID+">", "")
	return strings.TrimSpace(content)
}

// --- Commands ---

func (db *DiscordBot) handleCommand(msg discordMessage, cmdText string) {
	parts := strings.SplitN(cmdText, " ", 2)
	command := strings.ToLower(parts[0])
	args := ""
	if len(parts) > 1 {
		args = parts[1]
	}

	switch command {
	case "status":
		db.cmdStatus(msg)
	case "jobs", "cron":
		db.cmdJobs(msg)
	case "cost":
		db.cmdCost(msg)
	case "model":
		db.cmdModel(msg, args)
	case "new":
		db.cmdNewSession(msg)
	case "cancel":
		db.cmdCancel(msg)
	case "ask":
		if args == "" {
			db.sendMessage(msg.ChannelID, "Usage: `!ask <prompt>`")
		} else {
			db.cmdAsk(msg, args)
		}
	case "approve":
		db.cmdApprove(msg, args)
	case "help":
		db.cmdHelp(msg)
	default:
		if args != "" {
			db.handleRoute(msg, cmdText)
		} else {
			db.sendMessage(msg.ChannelID, "Unknown command `!"+command+"`. Use `!help` for available commands.")
		}
	}
}

func (db *DiscordBot) cmdStatus(msg discordMessage) {
	running := 0
	if db.state != nil {
		db.state.mu.Lock()
		running = len(db.state.running)
		db.state.mu.Unlock()
	}
	jobs := 0
	if db.cron != nil {
		jobs = len(db.cron.ListJobs())
	}
	db.sendEmbed(msg.ChannelID, discordEmbed{
		Title: "Tetora Status",
		Color: 0x5865F2,
		Fields: []discordEmbedField{
			{Name: "Running", Value: fmt.Sprintf("%d", running), Inline: true},
			{Name: "Cron Jobs", Value: fmt.Sprintf("%d", jobs), Inline: true},
		},
		Timestamp: time.Now().Format(time.RFC3339),
	})
}

func (db *DiscordBot) cmdJobs(msg discordMessage) {
	if db.cron == nil {
		db.sendMessage(msg.ChannelID, "Cron engine not available.")
		return
	}
	jobs := db.cron.ListJobs()
	if len(jobs) == 0 {
		db.sendMessage(msg.ChannelID, "No cron jobs configured.")
		return
	}
	var fields []discordEmbedField
	for _, j := range jobs {
		status := "enabled"
		if !j.Enabled {
			status = "disabled"
		}
		fields = append(fields, discordEmbedField{
			Name: j.Name, Value: fmt.Sprintf("`%s` [%s]", j.Schedule, status), Inline: true,
		})
	}
	db.sendEmbed(msg.ChannelID, discordEmbed{
		Title: fmt.Sprintf("Cron Jobs (%d)", len(jobs)), Color: 0x57F287, Fields: fields,
	})
}

func (db *DiscordBot) cmdCost(msg discordMessage) {
	dbPath := db.cfg.HistoryDB
	if dbPath == "" {
		db.sendMessage(msg.ChannelID, "History DB not configured.")
		return
	}
	stats, err := queryCostStats(dbPath)
	if err != nil {
		db.sendMessage(msg.ChannelID, fmt.Sprintf("Error: %v", err))
		return
	}
	db.sendEmbed(msg.ChannelID, discordEmbed{
		Title: "Cost Summary",
		Color: 0xFEE75C,
		Fields: []discordEmbedField{
			{Name: "Today", Value: fmt.Sprintf("$%.4f", stats.Today), Inline: true},
			{Name: "This Week", Value: fmt.Sprintf("$%.4f", stats.Week), Inline: true},
			{Name: "This Month", Value: fmt.Sprintf("$%.4f", stats.Month), Inline: true},
		},
	})
}

func (db *DiscordBot) cmdModel(msg discordMessage, args string) {
	parts := strings.Fields(args)

	// !model → show current model for default role
	if len(parts) == 0 {
		agentName := db.cfg.SmartDispatch.DefaultAgent
		if agentName == "" {
			agentName = "default"
		}
		rc, ok := db.cfg.Agents[agentName]
		if !ok {
			db.sendMessage(msg.ChannelID, fmt.Sprintf("Agent `%s` not found.", agentName))
			return
		}
		model := rc.Model
		if model == "" {
			model = db.cfg.DefaultModel
		}
		var fields []discordEmbedField
		for name, r := range db.cfg.Agents {
			m := r.Model
			if m == "" {
				m = db.cfg.DefaultModel
			}
			fields = append(fields, discordEmbedField{
				Name: name, Value: "`" + m + "`", Inline: true,
			})
		}
		db.sendEmbed(msg.ChannelID, discordEmbed{
			Title: "Current Models", Color: 0x5865F2, Fields: fields,
		})
		return
	}

	// !model <model> [agent] → set model
	model := parts[0]
	agentName := db.cfg.SmartDispatch.DefaultAgent
	if agentName == "" {
		agentName = "default"
	}
	if len(parts) > 1 {
		agentName = parts[1]
	}

	old, err := updateAgentModel(db.cfg, agentName, model)
	if err != nil {
		db.sendMessage(msg.ChannelID, fmt.Sprintf("Error: %v", err))
		return
	}
	db.sendMessage(msg.ChannelID, fmt.Sprintf("**%s** model: `%s` → `%s`", agentName, old, model))
}

func (db *DiscordBot) cmdCancel(msg discordMessage) {
	if db.state == nil {
		db.sendMessage(msg.ChannelID, "No dispatch state.")
		return
	}
	db.state.mu.Lock()
	count := 0
	for _, ts := range db.state.running {
		if ts.cancelFn != nil {
			ts.cancelFn()
			count++
		}
	}
	cancelFn := db.state.cancel
	db.state.mu.Unlock()
	if cancelFn != nil {
		cancelFn()
		count++
	}
	if count == 0 {
		db.sendMessage(msg.ChannelID, "Nothing running to cancel.")
	} else {
		db.sendMessage(msg.ChannelID, fmt.Sprintf("Cancelling %d task(s).", count))
	}
}

func (db *DiscordBot) cmdAsk(msg discordMessage, prompt string) {
	db.sendTyping(msg.ChannelID)

	ctx := withTraceID(context.Background(), newTraceID("discord"))

	task := Task{Prompt: prompt, Source: "ask:discord"}
	fillDefaults(db.cfg, &task)

	// Use default agent but no routing overhead.
	agentName := db.cfg.SmartDispatch.DefaultAgent
	if agentName != "" {
		if soulPrompt, err := loadAgentPrompt(db.cfg, agentName); err == nil && soulPrompt != "" {
			task.SystemPrompt = soulPrompt
		}
		if rc, ok := db.cfg.Agents[agentName]; ok {
			if rc.Model != "" {
				task.Model = rc.Model
			}
		}
	}

	result := runSingleTask(ctx, db.cfg, task, db.sem, db.childSem, agentName)

	output := result.Output
	if result.Status != "success" {
		output = result.Error
		if output == "" {
			output = result.Status
		}
	}
	if len(output) > 3800 {
		output = output[:3797] + "..."
	}

	color := 0x57F287
	if result.Status != "success" {
		color = 0xED4245
	}
	db.sendEmbed(msg.ChannelID, discordEmbed{
		Description: output,
		Color:       color,
		Fields: []discordEmbedField{
			{Name: "Cost", Value: fmt.Sprintf("$%.4f", result.CostUSD), Inline: true},
			{Name: "Duration", Value: fmt.Sprintf("%dms", result.DurationMs), Inline: true},
		},
		Footer:    &discordEmbedFooter{Text: fmt.Sprintf("ask | %s", task.ID[:8])},
		Timestamp: time.Now().Format(time.RFC3339),
	})
}

func (db *DiscordBot) cmdNewSession(msg discordMessage) {
	dbPath := db.cfg.HistoryDB
	if dbPath == "" {
		db.sendMessage(msg.ChannelID, "History DB not configured.")
		return
	}
	chKey := channelSessionKey("discord", msg.ChannelID)
	if err := archiveChannelSession(dbPath, chKey); err != nil {
		db.sendMessage(msg.ChannelID, fmt.Sprintf("Error: %v", err))
		return
	}
	db.sendMessage(msg.ChannelID, "New session started.")
}

func (db *DiscordBot) cmdHelp(msg discordMessage) {
	db.sendEmbed(msg.ChannelID, discordEmbed{
		Title:       "Tetora Help",
		Description: "Mention me with a message to route it to the best agent, or use commands:",
		Color:       0x5865F2,
		Fields: []discordEmbedField{
			{Name: "!status", Value: "Show daemon status"},
			{Name: "!jobs", Value: "List cron jobs"},
			{Name: "!cost", Value: "Show cost summary"},
			{Name: "!model [model] [agent]", Value: "Show/switch model"},
			{Name: "!new", Value: "Start a new session (clear context)"},
			{Name: "!cancel", Value: "Cancel all running tasks"},
			{Name: "!ask <prompt>", Value: "Quick question (no routing, no session)"},
			{Name: "!approve [tool|reset]", Value: "Manage auto-approved tools"},
			{Name: "!help", Value: "Show this help"},
			{Name: "Free text", Value: "Mention me + your prompt for smart dispatch"},
		},
	})
}

func (db *DiscordBot) cmdApprove(msg discordMessage, args string) {
	if db.approvalGate == nil {
		db.sendMessage(msg.ChannelID, "Approval gates are not enabled.")
		return
	}

	args = strings.TrimSpace(args)
	if args == "" {
		// List auto-approved tools.
		db.approvalGate.mu.Lock()
		var tools []string
		for t := range db.approvalGate.autoApproved {
			tools = append(tools, "`"+t+"`")
		}
		db.approvalGate.mu.Unlock()
		if len(tools) == 0 {
			db.sendMessage(msg.ChannelID, "No auto-approved tools.")
		} else {
			db.sendMessage(msg.ChannelID, "Auto-approved tools: "+strings.Join(tools, ", "))
		}
		return
	}

	if args == "reset" {
		db.approvalGate.mu.Lock()
		db.approvalGate.autoApproved = make(map[string]bool)
		db.approvalGate.mu.Unlock()
		db.sendMessage(msg.ChannelID, "Cleared all auto-approved tools.")
		return
	}

	// Add tool to auto-approved list.
	db.approvalGate.AutoApprove(args)
	db.sendMessage(msg.ChannelID, fmt.Sprintf("Auto-approved `%s` for this runtime.", args))
}

// --- Direct Route (no SmartDispatch) ---

// handleDirectRoute dispatches a message directly to a known agent without smart routing.
func (db *DiscordBot) handleDirectRoute(msg discordMessage, prompt string, agent string) {
	route := RouteResult{Agent: agent, Method: "default", Confidence: "high"}
	db.executeRoute(msg, prompt, route)
}

// --- Smart Dispatch ---

func (db *DiscordBot) handleRoute(msg discordMessage, prompt string) {
	ctx := withTraceID(context.Background(), newTraceID("discord"))
	route := routeTask(ctx, db.cfg, RouteRequest{Prompt: prompt, Source: "discord"})
	logInfoCtx(ctx, "discord route result", "prompt", truncate(prompt, 60), "agent", route.Agent, "method", route.Method)
	db.executeRoute(msg, prompt, *route)
}

// executeRoute runs a routed task through the full Discord execution pipeline
// (session, SSE events, progress messages, reply).
func (db *DiscordBot) executeRoute(msg discordMessage, prompt string, route RouteResult) {
	db.sendTyping(msg.ChannelID)

	// P14.3: Add queued reaction.
	if db.reactions != nil {
		db.reactions.reactQueued(msg.ChannelID, msg.ID)
	}

	ctx := withTraceID(context.Background(), newTraceID("discord"))
	dbPath := db.cfg.HistoryDB

	// Generate a task ID early for Discord activity tracking.
	activityID := newUUID()

	// Register Discord activity for dashboard visibility.
	if db.state != nil {
		db.state.setDiscordActivity(activityID, &discordActivity{
			TaskID:    activityID,
			Agent:     route.Agent,
			Phase:     "routing",
			Author:    msg.Author.Username,
			ChannelID: msg.ChannelID,
			StartAt:   time.Now(),
			Prompt:    truncate(prompt, 200),
		})
		defer db.state.removeDiscordActivity(activityID)
	}

	// Update Discord activity with resolved agent.
	if db.state != nil {
		db.state.mu.Lock()
		if da, ok := db.state.discordActivities[activityID]; ok {
			da.Agent = route.Agent
		}
		db.state.mu.Unlock()
	}

	// Channel session.
	chKey := channelSessionKey("discord", msg.ChannelID)
	sess, err := getOrCreateChannelSession(dbPath, "discord", chKey, route.Agent, "")
	if err != nil {
		logErrorCtx(ctx, "discord session error", "error", err)
	}

	// Context-aware prompt.
	contextPrompt := prompt
	if sess != nil {
		sessionCtx := buildSessionContext(dbPath, sess.ID, db.cfg.Session.contextMessagesOrDefault())
		contextPrompt = wrapWithContext(sessionCtx, prompt)
		now := time.Now().Format(time.RFC3339)
		addSessionMessage(dbPath, SessionMessage{
			SessionID: sess.ID, Role: "user", Content: truncateStr(prompt, 5000), CreatedAt: now,
		})
		updateSessionStats(dbPath, sess.ID, 0, 0, 0, 1)
		title := prompt
		if len(title) > 100 {
			title = title[:100]
		}
		updateSessionTitle(dbPath, sess.ID, title)
	}

	// Publish task_received + task_routing (after session resolved so watchers see them).
	if db.state != nil && db.state.broker != nil {
		sessID := ""
		if sess != nil {
			sessID = sess.ID
		}
		publishToSSEBroker(db.state.broker, SSEEvent{
			Type: SSETaskReceived, TaskID: activityID, SessionID: sessID,
			Data: map[string]any{
				"source":  "discord",
				"author":  msg.Author.Username,
				"prompt":  prompt,
				"channel": msg.ChannelID,
			},
		})
		publishToSSEBroker(db.state.broker, SSEEvent{
			Type: SSETaskRouting, TaskID: activityID, SessionID: sessID,
			Data: map[string]any{
				"source":     "discord",
				"role":       route.Agent,
				"method":     route.Method,
				"confidence": route.Confidence,
			},
		})
	}

	// Build and run task. Pre-set ID so it matches the activityID used for dashboard tracking.
	task := Task{ID: activityID, Prompt: contextPrompt, Agent: route.Agent, Source: "route:discord"}
	fillDefaults(db.cfg, &task)
	if sess != nil {
		task.SessionID = sess.ID
	}
	if task.Agent != "" {
		if soulPrompt, err := loadAgentPrompt(db.cfg, task.Agent); err == nil && soulPrompt != "" {
			task.SystemPrompt = soulPrompt
		}
	}
	// Discord tasks run unattended — default to bypassPermissions if not set by agent.
	if task.PermissionMode == "" {
		task.PermissionMode = "bypassPermissions"
	}

	task.Prompt = expandPrompt(task.Prompt, "", db.cfg.HistoryDB, route.Agent, db.cfg.KnowledgeDir, db.cfg)

	// P28.0: Attach approval gate.
	if db.approvalGate != nil {
		task.approvalGate = db.approvalGate
	}

	// P14.3: Transition to thinking phase before task execution.
	if db.reactions != nil {
		db.reactions.reactThinking(msg.ChannelID, msg.ID)
	}

	// Update Discord activity: routing → processing.
	if db.state != nil {
		db.state.updateDiscordPhase(activityID, "processing")
		if db.state.broker != nil {
			publishToSSEBroker(db.state.broker, SSEEvent{
				Type: SSEDiscordProcessing, TaskID: activityID, SessionID: task.SessionID,
				Data: map[string]any{
					"taskId":  activityID,
					"role":    route.Agent,
					"author":  msg.Author.Username,
					"channel": msg.ChannelID,
				},
			})
		}
	}

	// Wire up SSE streaming so dashboard can show live output.
	if db.state != nil && db.state.broker != nil {
		task.sseBroker = db.state.broker
	}

	// Start progress message for live Discord updates.
	var progressMsgID string
	var progressStopCh chan struct{}
	var progressBuilder *discordProgressBuilder
	if db.state != nil && db.state.broker != nil {
		msgID, err := db.sendMessageReturningID(msg.ChannelID, "Working...")
		if err == nil && msgID != "" {
			progressMsgID = msgID
			progressStopCh = make(chan struct{})
			progressBuilder = newDiscordProgressBuilder()
			go db.runDiscordProgressUpdater(msg.ChannelID, progressMsgID, task.ID, db.state.broker, progressStopCh, progressBuilder)
		}
	}

	taskStart := time.Now()
	result := runSingleTask(ctx, db.cfg, task, db.sem, db.childSem, route.Agent)

	// Stop progress updater and clean up progress message.
	if progressStopCh != nil {
		close(progressStopCh)
	}
	if progressMsgID != "" {
		if result.Status != "success" {
			// On error, edit progress to show error instead of deleting.
			errMsg := result.Error
			if errMsg == "" {
				errMsg = result.Status
			}
			elapsed := time.Since(taskStart).Round(time.Second)
			db.editMessage(msg.ChannelID, progressMsgID, fmt.Sprintf("Error (%s): %s", elapsed, errMsg))
		} else {
			// On success: if output fits in one message, edit progress in-place (no flicker).
			// Otherwise delete and re-send as chunks.
			output := result.Output
			if strings.TrimSpace(output) == "" {
				output = "Task completed successfully."
			}
			if len(output) <= 1900 {
				db.editMessage(msg.ChannelID, progressMsgID, output)
				progressMsgID = "" // signal sendRouteResponse to skip output (already shown)
			} else {
				db.deleteMessage(msg.ChannelID, progressMsgID)
			}
		}
	}

	// Track whether output was already sent via progress message edit.
	outputAlreadySent := progressMsgID == "" && progressBuilder != nil && result.Status == "success"

	// Update Discord activity: processing → replying.
	if db.state != nil {
		db.state.updateDiscordPhase(activityID, "replying")
		if db.state.broker != nil {
			publishToSSEBroker(db.state.broker, SSEEvent{
				Type: SSEDiscordReplying, TaskID: activityID, SessionID: task.SessionID,
				Data: map[string]any{
					"taskId":  activityID,
					"role":    route.Agent,
					"author":  msg.Author.Username,
					"status":  result.Status,
				},
			})
		}
	}

	// P14.3: Set done/error reaction based on result.
	if db.reactions != nil {
		if result.Status == "success" {
			db.reactions.reactDone(msg.ChannelID, msg.ID)
		} else {
			db.reactions.reactError(msg.ChannelID, msg.ID)
		}
	}

	recordHistory(db.cfg.HistoryDB, task.ID, task.Name, task.Source, route.Agent, task, result,
		taskStart.Format(time.RFC3339), time.Now().Format(time.RFC3339), result.OutputFile)

	// Record to session.
	if sess != nil {
		now := time.Now().Format(time.RFC3339)
		msgRole := "assistant"
		content := truncateStr(result.Output, 5000)
		if result.Status != "success" {
			msgRole = "system"
			errMsg := result.Error
			if errMsg == "" {
				errMsg = result.Status
			}
			content = fmt.Sprintf("[%s] %s", result.Status, truncateStr(errMsg, 2000))
		}
		addSessionMessage(dbPath, SessionMessage{
			SessionID: sess.ID, Role: msgRole, Content: content,
			CostUSD: result.CostUSD, TokensIn: result.TokensIn, TokensOut: result.TokensOut,
			Model: result.Model, TaskID: task.ID, CreatedAt: now,
		})
		updateSessionStats(dbPath, sess.ID, result.CostUSD, result.TokensIn, result.TokensOut, 1)

		// Publish session_message so watchers get the full output without polling.
		if db.state != nil && db.state.broker != nil {
			publishToSSEBroker(db.state.broker, SSEEvent{
				Type: SSESessionMessage, TaskID: task.ID, SessionID: sess.ID,
				Data: map[string]any{"role": msgRole, "content": content},
			})
		}

		maybeCompactSession(db.cfg, dbPath, sess.ID, sess.MessageCount+2, db.sem, db.childSem)
	}

	if result.Status == "success" {
		setMemory(db.cfg, route.Agent, "last_route_output", truncate(result.Output, 500))
		setMemory(db.cfg, route.Agent, "last_route_prompt", truncate(prompt, 200))
		setMemory(db.cfg, route.Agent, "last_route_time", time.Now().Format(time.RFC3339))
	}

	auditLog(dbPath, "route.dispatch", "discord",
		fmt.Sprintf("agent=%s method=%s session=%s", route.Agent, route.Method, task.SessionID), "")

	sendWebhooks(db.cfg, result.Status, WebhookPayload{
		JobID: task.ID, Name: task.Name, Source: task.Source,
		Status: result.Status, Cost: result.CostUSD, Duration: result.DurationMs,
		Model: result.Model, Output: truncate(result.Output, 500), Error: truncate(result.Error, 300),
	})

	// Send response embed.
	db.sendRouteResponse(msg.ChannelID, &route, result, task, outputAlreadySent)
}

func (db *DiscordBot) sendRouteResponse(channelID string, route *RouteResult, result TaskResult, task Task, skipOutput bool) {
	color := 0x57F287
	if result.Status != "success" {
		color = 0xED4245
	}

	if !skipOutput {
		output := result.Output
		if result.Status != "success" {
			output = result.Error
			if output == "" {
				output = result.Status
			}
		}
		// Fallback for empty/whitespace output on success (e.g. tool-only responses).
		if strings.TrimSpace(output) == "" && result.Status == "success" {
			parts := []string{"Task completed successfully."}
			if result.TokensIn > 0 || result.TokensOut > 0 {
				parts = append(parts, fmt.Sprintf("Tokens: %d in / %d out", result.TokensIn, result.TokensOut))
			}
			if result.OutputFile != "" {
				parts = append(parts, fmt.Sprintf("Output saved: `%s`", result.OutputFile))
			}
			output = strings.Join(parts, "\n")
		}

		// Send output as plain text messages (split into 2000-char chunks).
		// This avoids embed description truncation and is more readable.
		const maxChunk = 1900 // leave room for markdown formatting
		for len(output) > 0 {
			chunk := output
			if len(chunk) > maxChunk {
				// Try to split at a newline boundary.
				cut := maxChunk
				if idx := strings.LastIndex(chunk[:maxChunk], "\n"); idx > maxChunk/2 {
					cut = idx + 1
				}
				chunk = output[:cut]
				output = output[cut:]
			} else {
				output = ""
			}
			db.sendMessage(channelID, chunk)
		}
	}

	// Send metadata as a small embed at the end.
	db.sendEmbed(channelID, discordEmbed{
		Color: color,
		Fields: []discordEmbedField{
			{Name: "Agent", Value: fmt.Sprintf("%s (%s)", route.Agent, route.Method), Inline: true},
			{Name: "Status", Value: result.Status, Inline: true},
			{Name: "Cost", Value: fmt.Sprintf("$%.4f", result.CostUSD), Inline: true},
			{Name: "Duration", Value: fmt.Sprintf("%dms", result.DurationMs), Inline: true},
		},
		Footer:    &discordEmbedFooter{Text: fmt.Sprintf("Task: %s", task.ID[:8])},
		Timestamp: time.Now().Format(time.RFC3339),
	})
}

// --- REST API Helpers ---

func (db *DiscordBot) sendMessage(channelID, content string) {
	if len(content) > 2000 {
		content = content[:1997] + "..."
	}
	db.discordPost(fmt.Sprintf("/channels/%s/messages", channelID), map[string]string{"content": content})
}

func (db *DiscordBot) sendEmbed(channelID string, embed discordEmbed) {
	db.discordPost(fmt.Sprintf("/channels/%s/messages", channelID), map[string]any{"embeds": []discordEmbed{embed}})
}

func (db *DiscordBot) sendTyping(channelID string) {
	url := discordAPIBase + fmt.Sprintf("/channels/%s/typing", channelID)
	req, _ := http.NewRequest("POST", url, nil)
	if req != nil {
		req.Header.Set("Authorization", "Bot "+db.cfg.Discord.BotToken)
		db.client.Do(req)
	}
}

// --- P27.3: Discord Channel Notifier ---

type discordChannelNotifier struct {
	bot       *DiscordBot
	channelID string
}

func (n *discordChannelNotifier) SendTyping(ctx context.Context) error {
	n.bot.sendTyping(n.channelID)
	return nil
}

func (n *discordChannelNotifier) SendStatus(ctx context.Context, msg string) error {
	n.bot.sendTyping(n.channelID)
	return nil
}

// isAllowedChannel checks if a channel ID is in any allowed list.
// If no channel restrictions are set, all channels are allowed.
func (db *DiscordBot) isAllowedChannel(chID string) bool {
	hasRestrictions := len(db.cfg.Discord.ChannelIDs) > 0 ||
		len(db.cfg.Discord.MentionChannelIDs) > 0 ||
		db.cfg.Discord.ChannelID != ""
	if !hasRestrictions {
		return true
	}
	return db.isDirectChannel(chID) || db.isMentionChannel(chID)
}

// isDirectChannel returns true if the channel is in channelIDs (no @ needed).
func (db *DiscordBot) isDirectChannel(chID string) bool {
	for _, id := range db.cfg.Discord.ChannelIDs {
		if id == chID {
			return true
		}
	}
	return false
}

// isMentionChannel returns true if the channel requires @mention.
func (db *DiscordBot) isMentionChannel(chID string) bool {
	if db.cfg.Discord.ChannelID != "" && db.cfg.Discord.ChannelID == chID {
		return true
	}
	for _, id := range db.cfg.Discord.MentionChannelIDs {
		if id == chID {
			return true
		}
	}
	return false
}

func (db *DiscordBot) notifyChannelID() string {
	if len(db.cfg.Discord.ChannelIDs) > 0 {
		return db.cfg.Discord.ChannelIDs[0]
	}
	return db.cfg.Discord.ChannelID
}

func (db *DiscordBot) sendNotify(text string) {
	ch := db.notifyChannelID()
	if ch == "" {
		return
	}
	db.sendMessage(ch, text)
}

func (db *DiscordBot) discordPost(path string, payload any) {
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", discordAPIBase+path, strings.NewReader(string(body)))
	if err != nil {
		logError("discord api request error", "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bot "+db.cfg.Discord.BotToken)
	resp, err := db.client.Do(req)
	if err != nil {
		logError("discord api send failed", "error", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		logWarn("discord api error", "status", resp.StatusCode, "body", string(b))
	}
}

// discordRequestWithResponse sends a Discord API request and returns the response body.
// Supports any HTTP method (POST, PATCH, DELETE).
func (db *DiscordBot) discordRequestWithResponse(method, path string, payload any) ([]byte, error) {
	var bodyReader io.Reader
	if payload != nil {
		body, _ := json.Marshal(payload)
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, discordAPIBase+path, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bot "+db.cfg.Discord.BotToken)
	resp, err := db.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode >= 400 {
		return respBody, fmt.Errorf("discord api %s %s: %d %s", method, path, resp.StatusCode, string(respBody))
	}
	return respBody, nil
}

// sendMessageReturningID sends a message and returns the message ID.
func (db *DiscordBot) sendMessageReturningID(channelID, content string) (string, error) {
	if len(content) > 2000 {
		content = content[:1997] + "..."
	}
	body, err := db.discordRequestWithResponse("POST",
		fmt.Sprintf("/channels/%s/messages", channelID),
		map[string]string{"content": content})
	if err != nil {
		return "", err
	}
	var msg struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &msg); err != nil {
		return "", err
	}
	return msg.ID, nil
}

// editMessage edits an existing Discord message.
func (db *DiscordBot) editMessage(channelID, messageID, content string) error {
	if len(content) > 2000 {
		content = content[:1997] + "..."
	}
	_, err := db.discordRequestWithResponse("PATCH",
		fmt.Sprintf("/channels/%s/messages/%s", channelID, messageID),
		map[string]string{"content": content})
	return err
}

// deleteMessage deletes a Discord message.
func (db *DiscordBot) deleteMessage(channelID, messageID string) {
	_, err := db.discordRequestWithResponse("DELETE",
		fmt.Sprintf("/channels/%s/messages/%s", channelID, messageID), nil)
	if err != nil {
		logWarn("discord delete message failed", "error", err)
	}
}

// --- P14.1: Discord Components v2 ---

// sendMessageWithComponents sends a message with interactive components (buttons, selects, etc.).
func (db *DiscordBot) sendMessageWithComponents(channelID, content string, components []discordComponent) {
	if len(content) > 2000 {
		content = content[:1997] + "..."
	}
	db.discordPost(fmt.Sprintf("/channels/%s/messages", channelID), map[string]any{
		"content":    content,
		"components": components,
	})
}

// sendEmbedWithComponents sends an embed message with interactive components.
func (db *DiscordBot) sendEmbedWithComponents(channelID string, embed discordEmbed, components []discordComponent) {
	db.discordPost(fmt.Sprintf("/channels/%s/messages", channelID), map[string]any{
		"embeds":     []discordEmbed{embed},
		"components": components,
	})
}

// --- Discord Progress Updater ---

// discordProgressBuilder accumulates SSE events and renders a progress display for Discord.
type discordProgressBuilder struct {
	mu      sync.Mutex
	startAt time.Time
	tools   []string         // tool names in order
	text    strings.Builder  // accumulated text content
	dirty   bool             // whether content changed since last render
}

func newDiscordProgressBuilder() *discordProgressBuilder {
	return &discordProgressBuilder{
		startAt: time.Now(),
	}
}

func (b *discordProgressBuilder) addToolCall(name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tools = append(b.tools, name)
	b.dirty = true
}

func (b *discordProgressBuilder) addText(text string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	text = ansiEscapeRe.ReplaceAllString(text, "")
	if text == "" {
		return
	}
	b.text.WriteString(text)
	b.dirty = true
}

func (b *discordProgressBuilder) render() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.dirty = false

	elapsed := time.Since(b.startAt).Round(time.Second)
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Working... (%s)\n", elapsed))

	// Show last 5 tool calls.
	start := 0
	if len(b.tools) > 5 {
		start = len(b.tools) - 5
		sb.WriteString(fmt.Sprintf("... and %d earlier steps\n", start))
	}
	for _, t := range b.tools[start:] {
		sb.WriteString(fmt.Sprintf("> %s\n", t))
	}

	// Append accumulated text content (rolling window to fit Discord's 2000 char limit).
	accumulated := b.text.String()
	if accumulated != "" {
		sb.WriteString("\n")
		header := sb.String()
		maxText := 2000 - len(header) - 10 // leave margin
		if maxText < 100 {
			maxText = 100
		}
		if len(accumulated) > maxText {
			// Trim from front to nearest newline.
			trimmed := accumulated[len(accumulated)-maxText:]
			if idx := strings.Index(trimmed, "\n"); idx >= 0 && idx < len(trimmed)/2 {
				trimmed = trimmed[idx+1:]
			}
			sb.WriteString("..." + trimmed)
		} else {
			sb.WriteString(accumulated)
		}
	}

	return sb.String()
}

func (b *discordProgressBuilder) getText() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.text.String()
}

func (b *discordProgressBuilder) isDirty() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.dirty
}

// runDiscordProgressUpdater subscribes to task SSE events and updates a Discord progress message.
// It stops when stopCh is closed or the event channel closes.
func (db *DiscordBot) runDiscordProgressUpdater(
	channelID, progressMsgID, taskID string,
	broker *sseBroker,
	stopCh <-chan struct{},
	builder *discordProgressBuilder,
) {
	eventCh, unsub := broker.Subscribe(taskID)
	defer unsub()

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-stopCh:
			return
		case ev, ok := <-eventCh:
			if !ok {
				return
			}
			switch ev.Type {
			case SSEToolCall:
				if data, ok := ev.Data.(map[string]any); ok {
					name, _ := data["name"].(string)
					if name != "" {
						builder.addToolCall(name)
					}
				}
			case SSEOutputChunk:
				if data, ok := ev.Data.(map[string]any); ok {
					chunk, _ := data["chunk"].(string)
					if chunk != "" {
						builder.addText(chunk)
					}
				}
			case SSECompleted, SSEError:
				return
			}
		case <-ticker.C:
			if builder.isDirty() {
				content := builder.render()
				if err := db.editMessage(channelID, progressMsgID, content); err != nil {
					logWarn("discord progress edit failed", "error", err)
				}
				db.sendTyping(channelID)
			}
		}
	}
}

// --- P28.0: Discord Approval Gate ---

// discordApprovalGate implements ApprovalGate via Discord button components.
type discordApprovalGate struct {
	bot          *DiscordBot
	channelID    string
	mu           sync.Mutex
	pending      map[string]chan bool
	autoApproved map[string]bool // tool name → always approved
}

func newDiscordApprovalGate(bot *DiscordBot, channelID string) *discordApprovalGate {
	g := &discordApprovalGate{
		bot:          bot,
		channelID:    channelID,
		pending:      make(map[string]chan bool),
		autoApproved: make(map[string]bool),
	}
	// Copy config-level auto-approve tools.
	for _, tool := range bot.cfg.ApprovalGates.AutoApproveTools {
		g.autoApproved[tool] = true
	}
	return g
}

func (g *discordApprovalGate) AutoApprove(toolName string) {
	g.mu.Lock()
	g.autoApproved[toolName] = true
	g.mu.Unlock()
}

func (g *discordApprovalGate) IsAutoApproved(toolName string) bool {
	g.mu.Lock()
	ok := g.autoApproved[toolName]
	g.mu.Unlock()
	return ok
}

func (g *discordApprovalGate) RequestApproval(ctx context.Context, req ApprovalRequest) (bool, error) {
	ch := make(chan bool, 1)
	g.mu.Lock()
	g.pending[req.ID] = ch
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		delete(g.pending, req.ID)
		g.mu.Unlock()
	}()

	text := fmt.Sprintf("**Approval needed**\n\nTool: `%s`\n%s", req.Tool, req.Summary)
	components := []discordComponent{{
		Type: componentTypeActionRow,
		Components: []discordComponent{
			{Type: componentTypeButton, Style: buttonStyleSuccess, Label: "Approve", CustomID: "gate_approve:" + req.ID},
			{Type: componentTypeButton, Style: buttonStylePrimary, Label: "Always", CustomID: "gate_always:" + req.ID + ":" + req.Tool},
			{Type: componentTypeButton, Style: buttonStyleDanger, Label: "Reject", CustomID: "gate_reject:" + req.ID},
		},
	}}
	g.bot.sendMessageWithComponents(g.channelID, text, components)

	select {
	case approved := <-ch:
		return approved, nil
	case <-ctx.Done():
		return false, fmt.Errorf("approval timed out: %v", ctx.Err())
	}
}

func (g *discordApprovalGate) handleGateCallback(reqID string, approved bool) {
	g.mu.Lock()
	ch, ok := g.pending[reqID]
	g.mu.Unlock()
	if ok {
		select {
		case ch <- approved:
		default:
		}
	}
}
