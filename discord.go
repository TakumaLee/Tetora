package main

import (
	"bufio"
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
	PublicKey      string                      `json:"publicKey,omitempty"` // Ed25519 public key for interaction verification
	Components     DiscordComponentsConfig     `json:"components,omitempty"`
	ThreadBindings DiscordThreadBindingsConfig `json:"threadBindings,omitempty"` // P14.2: per-thread agent isolation
	Reactions      DiscordReactionsConfig      `json:"reactions,omitempty"`      // P14.3: lifecycle reactions
	ForumBoard     DiscordForumBoardConfig     `json:"forumBoard,omitempty"`     // P14.4: forum task board
	Voice          DiscordVoiceConfig          `json:"voice,omitempty"`          // P14.5: voice channel integration
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
}

func newDiscordBot(cfg *Config, state *dispatchState, sem chan struct{}, cron *CronEngine) *DiscordBot {
	db := &DiscordBot{
		cfg:          cfg,
		state:        state,
		sem:          sem,
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

	if db.cfg.SmartDispatch.Enabled {
		db.handleRoute(msg, text)
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
		roleName := db.cfg.SmartDispatch.DefaultRole
		if roleName == "" {
			roleName = "default"
		}
		rc, ok := db.cfg.Roles[roleName]
		if !ok {
			db.sendMessage(msg.ChannelID, fmt.Sprintf("Role `%s` not found.", roleName))
			return
		}
		model := rc.Model
		if model == "" {
			model = db.cfg.DefaultModel
		}
		var fields []discordEmbedField
		for name, r := range db.cfg.Roles {
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

	// !model <model> [role] → set model
	model := parts[0]
	roleName := db.cfg.SmartDispatch.DefaultRole
	if roleName == "" {
		roleName = "default"
	}
	if len(parts) > 1 {
		roleName = parts[1]
	}

	old, err := updateRoleModel(db.cfg, roleName, model)
	if err != nil {
		db.sendMessage(msg.ChannelID, fmt.Sprintf("Error: %v", err))
		return
	}
	db.sendMessage(msg.ChannelID, fmt.Sprintf("**%s** model: `%s` → `%s`", roleName, old, model))
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
			{Name: "!model [model] [role]", Value: "Show/switch model"},
			{Name: "!help", Value: "Show this help"},
			{Name: "Free text", Value: "Mention me + your prompt for smart dispatch"},
		},
	})
}

// --- Smart Dispatch ---

func (db *DiscordBot) handleRoute(msg discordMessage, prompt string) {
	db.sendTyping(msg.ChannelID)

	// P14.3: Add queued reaction.
	if db.reactions != nil {
		db.reactions.reactQueued(msg.ChannelID, msg.ID)
	}

	ctx := withTraceID(context.Background(), newTraceID("discord"))
	dbPath := db.cfg.HistoryDB

	// Route.
	route := routeTask(ctx, db.cfg, RouteRequest{Prompt: prompt, Source: "discord"}, db.sem)
	logInfoCtx(ctx, "discord route result", "prompt", truncate(prompt, 60), "role", route.Role, "method", route.Method)

	// Channel session.
	chKey := channelSessionKey("discord", msg.ChannelID)
	sess, err := getOrCreateChannelSession(dbPath, "discord", chKey, route.Role, "")
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

	// Build and run task.
	task := Task{Prompt: contextPrompt, Role: route.Role, Source: "route:discord"}
	fillDefaults(db.cfg, &task)
	if sess != nil {
		task.SessionID = sess.ID
	}
	if route.Role != "" {
		if soulPrompt, err := loadRolePrompt(db.cfg, route.Role); err == nil && soulPrompt != "" {
			task.SystemPrompt = soulPrompt
		}
		if rc, ok := db.cfg.Roles[route.Role]; ok {
			if rc.Model != "" {
				task.Model = rc.Model
			}
			if rc.PermissionMode != "" {
				task.PermissionMode = rc.PermissionMode
			}
		}
	}
	task.Prompt = expandPrompt(task.Prompt, "", db.cfg.HistoryDB, route.Role, db.cfg.KnowledgeDir, db.cfg)

	// P28.0: Attach approval gate.
	if db.approvalGate != nil {
		task.approvalGate = db.approvalGate
	}

	// P14.3: Transition to thinking phase before task execution.
	if db.reactions != nil {
		db.reactions.reactThinking(msg.ChannelID, msg.ID)
	}

	taskStart := time.Now()
	result := runSingleTask(ctx, db.cfg, task, db.sem, route.Role)

	// P14.3: Set done/error reaction based on result.
	if db.reactions != nil {
		if result.Status == "success" {
			db.reactions.reactDone(msg.ChannelID, msg.ID)
		} else {
			db.reactions.reactError(msg.ChannelID, msg.ID)
		}
	}

	recordHistory(db.cfg.HistoryDB, task.ID, task.Name, task.Source, route.Role, task, result,
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
		maybeCompactSession(db.cfg, dbPath, sess.ID, sess.MessageCount+2, db.sem)
	}

	if result.Status == "success" && dbPath != "" {
		setMemory(dbPath, route.Role, "last_route_output", truncate(result.Output, 500))
		setMemory(dbPath, route.Role, "last_route_prompt", truncate(prompt, 200))
		setMemory(dbPath, route.Role, "last_route_time", time.Now().Format(time.RFC3339))
	}

	auditLog(dbPath, "route.dispatch", "discord",
		fmt.Sprintf("role=%s method=%s session=%s", route.Role, route.Method, task.SessionID), "")

	sendWebhooks(db.cfg, result.Status, WebhookPayload{
		JobID: task.ID, Name: task.Name, Source: task.Source,
		Status: result.Status, Cost: result.CostUSD, Duration: result.DurationMs,
		Model: result.Model, Output: truncate(result.Output, 500), Error: truncate(result.Error, 300),
	})

	// Send response embed.
	db.sendRouteResponse(msg.ChannelID, route, result, task)
}

func (db *DiscordBot) sendRouteResponse(channelID string, route *RouteResult, result TaskResult, task Task) {
	color := 0x57F287
	if result.Status != "success" {
		color = 0xED4245
	}
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
	db.sendEmbed(channelID, discordEmbed{
		Title:       fmt.Sprintf("%s (%s)", route.Role, route.Method),
		Description: output,
		Color:       color,
		Fields: []discordEmbedField{
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

// --- P28.0: Discord Approval Gate ---

// discordApprovalGate implements ApprovalGate via Discord button components.
type discordApprovalGate struct {
	bot       *DiscordBot
	channelID string
	mu        sync.Mutex
	pending   map[string]chan bool
}

func newDiscordApprovalGate(bot *DiscordBot, channelID string) *discordApprovalGate {
	return &discordApprovalGate{
		bot:       bot,
		channelID: channelID,
		pending:   make(map[string]chan bool),
	}
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
