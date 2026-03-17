package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"tetora/internal/audit"
	"tetora/internal/discord"
	"tetora/internal/log"
	"tetora/internal/trace"
	"tetora/internal/upload"
	"tetora/internal/webhook"
)

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

	api          *discord.Client
	client       *http.Client
	stopCh       chan struct{}
	interactions *discordInteractionState // P14.1: tracks pending component interactions
	threads       *threadBindingStore      // P14.2: per-thread agent bindings
	threadParents *threadParentCache      // thread→parent channel cache
	reactions     *discordReactionManager // P14.3: lifecycle reactions
	approvalGate *discordApprovalGate     // P28.0: approval gate
	forumBoard   *discordForumBoard       // P14.4: forum task board
	voice        *discordVoiceManager     // P14.5: voice channel manager
	gatewayConn  *wsConn                  // P14.5: active gateway connection for voice state updates
	notifier     *discordTaskNotifier     // task notification (thread-per-task)
	terminal     *terminalBridge         // terminal bridge (tmux sessions)
	msgSem       chan struct{}            // limits concurrent message handlers
	// Message dedup: ring buffer of recently processed message IDs to prevent
	// duplicate handling on gateway reconnect/resume event replay.
	dedupMu    sync.Mutex
	dedupRing  [128]string
	dedupIdx   int
}

func newDiscordBot(cfg *Config, state *dispatchState, sem, childSem chan struct{}, cron *CronEngine) *DiscordBot {
	apiClient := discord.NewClient(cfg.Discord.BotToken)
	db := &DiscordBot{
		cfg:          cfg,
		state:        state,
		sem:          sem,
		childSem:     childSem,
		cron:         cron,
		api:          apiClient,
		client:       apiClient.HTTPClient,
		stopCh:       make(chan struct{}),
		interactions:  newDiscordInteractionState(), // P14.1
		threads:       newThreadBindingStore(),      // P14.2
		threadParents: newThreadParentCache(),
		msgSem:        make(chan struct{}, 32),
	}

	// P14.3: Initialize reaction manager.
	if cfg.Discord.Reactions.Enabled {
		db.reactions = newDiscordReactionManager(db, cfg.Discord.Reactions.Emojis)
		log.Info("discord lifecycle reactions enabled")
	}

	// P14.4: Initialize forum board.
	if cfg.Discord.ForumBoard.Enabled {
		db.forumBoard = newDiscordForumBoard(db, cfg.Discord.ForumBoard)
		log.Info("discord forum board enabled", "channel", cfg.Discord.ForumBoard.ForumChannelID)
	}

	// P14.5: Initialize voice manager.
	db.voice = newDiscordVoiceManager(db)
	if cfg.Discord.Voice.Enabled {
		log.Info("discord voice enabled", "auto_join_count", len(cfg.Discord.Voice.AutoJoin))
	}

	// Terminal bridge: interactive tmux sessions via Discord.
	if cfg.Discord.Terminal.Enabled {
		db.terminal = newTerminalBridge(db, cfg.Discord.Terminal)
		log.Info("discord terminal bridge enabled",
			"maxSessions", cfg.Discord.Terminal.MaxSessions,
			"defaultTool", cfg.Discord.Terminal.DefaultTool)
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
		log.Info("discord task notifier enabled", "channel", ch)
	}

	return db
}

// Run connects to the Discord Gateway and processes events. Blocks until stopped.
func (db *DiscordBot) Run(ctx context.Context) {
	// P14.2: Start thread binding cleanup goroutine.
	if db.threads != nil && db.cfg.Discord.ThreadBindings.Enabled {
		go startThreadCleanup(ctx, db.threads, db.threadParents)
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
			log.Error("discord gateway error", "error", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-db.stopCh:
			return
		case <-time.After(5 * time.Second):
			log.Info("discord reconnecting...")
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
			log.Info("discord gateway reconnect requested")
			return nil
		case opInvalidSession:
			log.Warn("discord invalid session")
			db.sessionID = ""
			return nil
		case opHeartbeatAck:
			// OK
		}
	}
}

// --- Event Handling ---

func (db *DiscordBot) handleEvent(payload gatewayPayload) {
	switch payload.T {
	case "READY":
		var ready readyData
		if json.Unmarshal(payload.D, &ready) == nil {
			db.botUserID = ready.User.ID
			db.sessionID = ready.SessionID
			log.Info("discord bot connected", "user", ready.User.Username, "id", ready.User.ID)

			// P14.5: Auto-join voice channels if configured
			if db.cfg.Discord.Voice.Enabled && len(db.cfg.Discord.Voice.AutoJoin) > 0 {
				go db.voice.autoJoinChannels()
			}
		}
	case "MESSAGE_CREATE":
		// P14.2: Parse with channel_type for thread detection.
		var msgT discordMessageWithType
		if json.Unmarshal(payload.D, &msgT) == nil {
			select {
			case db.msgSem <- struct{}{}:
				go func() {
					defer func() { <-db.msgSem }()
					db.handleMessageWithType(msgT.discordMessage, msgT.ChannelType)
				}()
			default:
				log.Warn("discord message handler limit reached, dropping message",
					"author", msgT.Author.Username, "channel", msgT.ChannelID)
			}
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
	case "INTERACTION_CREATE":
		// Handle button clicks and component interactions via Gateway.
		var interaction discordInteraction
		if json.Unmarshal(payload.D, &interaction) == nil {
			go db.handleGatewayInteraction(&interaction)
		}
	}
}

// isDuplicateMessage checks if a message ID was already processed recently.
// Returns true if duplicate (should skip), false if new (recorded for future checks).
func (db *DiscordBot) isDuplicateMessage(msgID string) bool {
	db.dedupMu.Lock()
	defer db.dedupMu.Unlock()
	for _, id := range db.dedupRing {
		if id == msgID {
			return true
		}
	}
	db.dedupRing[db.dedupIdx%len(db.dedupRing)] = msgID
	db.dedupIdx++
	return false
}

// handleMessageWithType is the top-level message handler that checks for thread bindings
// before falling through to normal message handling. (P14.2)
func (db *DiscordBot) handleMessageWithType(msg discordMessage, channelType int) {
	log.Debug("discord message received",
		"author", msg.Author.Username, "channel", msg.ChannelID,
		"content_len", len(msg.Content), "bot", msg.Author.Bot,
		"guild", msg.GuildID, "mentions", len(msg.Mentions))

	// Ignore bots.
	if msg.Author.Bot || msg.Author.ID == db.botUserID {
		return
	}

	// Dedup: skip if this message was already processed (gateway resume replays events).
	if db.isDuplicateMessage(msg.ID) {
		log.Debug("discord message dedup: skipping replayed message", "msgId", msg.ID, "author", msg.Author.Username)
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

	// Channel/guild restriction — also resolves thread→parent for allowlist check.
	if !db.isAllowedChannelOrThread(msg.ChannelID, msg.GuildID) {
		return
	}
	if db.cfg.Discord.GuildID != "" && msg.GuildID != db.cfg.Discord.GuildID {
		return
	}

	// Direct channels respond to all messages; mention channels require @; DMs always accepted.
	// For threads, inherit the parent channel's direct-channel status.
	mentioned := discord.IsMentioned(msg.Mentions, db.botUserID)
	isDM := msg.GuildID == ""
	isDirect := db.isDirectChannel(msg.ChannelID)
	if !isDirect && msg.GuildID != "" {
		if parentID := db.resolveThreadParent(msg.ChannelID); parentID != "" {
			isDirect = db.isDirectChannel(parentID)
		}
	}
	log.Debug("discord message filter",
		"mentioned", mentioned, "isDM", isDM, "isDirect", isDirect,
		"channel", msg.ChannelID, "author", msg.Author.Username)
	if !mentioned && !isDM && !isDirect {
		return
	}

	text := discord.StripMention(msg.Content, db.botUserID)
	text = strings.TrimSpace(text)

	// Download attachments and inject into prompt.
	var attachedFiles []*upload.File
	for _, att := range msg.Attachments {
		if f, err := downloadDiscordAttachment(db.cfg.BaseDir, att); err != nil {
			log.Warn("discord: attachment download failed", "url", att.URL, "err", err)
		} else {
			attachedFiles = append(attachedFiles, f)
		}
	}
	if prefix := upload.BuildPromptPrefix(attachedFiles); prefix != "" {
		text = prefix + text
	}

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

	// Terminal bridge: route text to active terminal session (before command handling).
	if db.terminal != nil && db.terminal.handleTerminalInput(msg.ChannelID, text) {
		return
	}

	// Command handling.
	if strings.HasPrefix(text, "!") {
		db.handleCommand(msg, text[1:])
		return
	}

	// Per-channel route binding (highest priority).
	// For threads, also check parent channel's route binding.
	if route, ok := db.cfg.Discord.Routes[msg.ChannelID]; ok && route.Agent != "" {
		db.handleDirectRoute(msg, text, route.Agent)
		return
	}
	if msg.GuildID != "" {
		if parentID := db.resolveThreadParent(msg.ChannelID); parentID != "" {
			if route, ok := db.cfg.Discord.Routes[parentID]; ok && route.Agent != "" {
				db.handleDirectRoute(msg, text, route.Agent)
				return
			}
		}
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

// downloadDiscordAttachment fetches an attachment from Discord CDN and saves it locally.
func downloadDiscordAttachment(baseDir string, att discordAttachment) (*upload.File, error) {
	resp, err := http.Get(att.URL) //nolint:noctx
	if err != nil {
		return nil, fmt.Errorf("discord attachment: http get: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("discord attachment: HTTP %d for %s", resp.StatusCode, att.Filename)
	}
	uploadDir := upload.InitDir(baseDir)
	return upload.Save(uploadDir, att.Filename, resp.Body, att.Size, "discord")
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
	case "term", "terminal":
		if db.terminal != nil {
			db.terminal.handleTermCommand(msg, args)
		} else {
			db.sendMessage(msg.ChannelID, "Terminal bridge is not enabled.")
		}
	case "version", "ver":
		db.sendMessage(msg.ChannelID, fmt.Sprintf("Tetora v%s", tetoraVersion))
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
			{Name: "Version", Value: "v" + tetoraVersion, Inline: true},
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

	ctx := trace.WithID(context.Background(), trace.NewID("discord"))

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
			{Name: "Duration", Value: formatDurationMs(result.DurationMs), Inline: true},
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
	ctx := trace.WithID(context.Background(), trace.NewID("discord"))
	route := routeTask(ctx, db.cfg, RouteRequest{Prompt: prompt, Source: "discord"})
	log.InfoCtx(ctx, "discord route result", "prompt", truncate(prompt, 60), "agent", route.Agent, "method", route.Method)
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

	baseCtx, baseCancel := context.WithCancel(context.Background())
	defer baseCancel()
	ctx := trace.WithID(baseCtx, trace.NewID("discord"))
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
		log.ErrorCtx(ctx, "discord session error", "error", err)
	}

	// Context-aware prompt.
	// Skip text injection when:
	//  - Provider has native session support (e.g. claude-code), OR
	//  - Session has messages → CLI will --continue with native conversation history.
	// Both cases already have full context; injecting again would double it.
	contextPrompt := prompt
	canResume := sess != nil && sess.MessageCount > 0
	if sess != nil {
		providerName := resolveProviderName(db.cfg, Task{Agent: route.Agent}, route.Agent)
		if !providerHasNativeSession(providerName) && !canResume {
			sessionCtx := buildSessionContext(dbPath, sess.ID, db.cfg.Session.ContextMessagesOrDefault())
			contextPrompt = wrapWithContext(sessionCtx, prompt)
		}
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
		task.PersistSession = true // channel sessions persist for --continue on next message
		task.Resume = canResume    // resume if session already has conversation history
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
		task.ApprovalGate = db.approvalGate
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
		task.SSEBroker = db.state.broker
	}

	// Start progress message for live Discord updates.
	// Controlled by showProgress config (default: true).
	showProgress := db.cfg.Discord.ShowProgress == nil || *db.cfg.Discord.ShowProgress
	var progressMsgID string
	var progressStopCh chan struct{}
	var progressBuilder *discordProgressBuilder
	var progressEscapeID string // interaction custom ID for escape button cleanup
	if showProgress && db.state != nil && db.state.broker != nil {
		// Build escape button for the progress message.
		escapeID := fmt.Sprintf("progress_escape:%s", task.ID)
		escapeComponents := []discordComponent{
			discordActionRow(
				discordButton(escapeID, "Escape", buttonStyleDanger),
			),
		}

		msgID, err := db.sendMessageWithComponentsReturningID(msg.ChannelID, "Working...", escapeComponents)
		if err == nil && msgID != "" {
			progressMsgID = msgID
			progressEscapeID = escapeID
			progressStopCh = make(chan struct{})
			progressBuilder = newDiscordProgressBuilder()

			// Register escape button interaction.
			db.interactions.register(&pendingInteraction{
				CustomID:  escapeID,
				CreatedAt: time.Now(),
				Response: &discordInteractionResponse{
					Type: interactionResponseUpdateMessage,
					Data: &discordInteractionResponseData{
						Content: "Interrupted.",
					},
				},
				Callback: func(data discordInteractionData) {
					log.Info("progress escape: cancelling task", "taskId", task.ID)
					// Cancel the base context directly — works for both
					// Discord chat mode (no state.running entry) and
					// dispatch mode.
					baseCancel()
				},
			})

			go db.runDiscordProgressUpdater(msg.ChannelID, progressMsgID, task.ID, task.SessionID, db.state.broker, progressStopCh, progressBuilder, escapeComponents)
		}
	}

	taskStart := time.Now()
	result := runSingleTask(ctx, db.cfg, task, db.sem, db.childSem, route.Agent)

	// Stop progress updater and clean up progress message.
	if progressStopCh != nil {
		close(progressStopCh)
	}
	// Clean up escape button interaction.
	if progressEscapeID != "" {
		db.interactions.remove(progressEscapeID)
	}
	if progressMsgID != "" {
		if result.Status != "success" {
			// On error, edit progress to show error instead of deleting.
			// Clear components (remove escape button).
			errMsg := result.Error
			if errMsg == "" {
				errMsg = result.Status
			}
			elapsed := time.Since(taskStart).Round(time.Second)
			db.editMessageWithComponents(msg.ChannelID, progressMsgID, fmt.Sprintf("Error (%s): %s", elapsed, errMsg), nil)
		} else {
			// On success: if output fits in one message, edit progress in-place (no flicker).
			// Otherwise delete and re-send as chunks.
			output := result.Output
			if strings.TrimSpace(output) == "" {
				// Session mode: result.Output is empty but progressBuilder may have accumulated content
				if progressBuilder != nil {
					if text := progressBuilder.getText(); strings.TrimSpace(text) != "" {
						output = strings.TrimSpace(text)
					}
				}
				if strings.TrimSpace(output) == "" {
					output = "Task completed successfully."
				}
			}
			if len(output) <= 1900 {
				db.editMessageWithComponents(msg.ChannelID, progressMsgID, output, nil)
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

		maybeCompactSession(db.cfg, dbPath, sess.ID, sess.MessageCount+2, sess.TotalTokensIn+result.TokensIn, db.sem, db.childSem)
	}

	if result.Status == "success" {
		setMemory(db.cfg, route.Agent, "last_route_output", truncate(result.Output, 500))
		setMemory(db.cfg, route.Agent, "last_route_prompt", truncate(prompt, 200))
		setMemory(db.cfg, route.Agent, "last_route_time", time.Now().Format(time.RFC3339))
	}

	audit.Log(dbPath, "route.dispatch", "discord",
		fmt.Sprintf("agent=%s method=%s session=%s", route.Agent, route.Method, task.SessionID), "")

	sendWebhooks(db.cfg, result.Status, webhook.Payload{
		JobID: task.ID, Name: task.Name, Source: task.Source,
		Status: result.Status, Cost: result.CostUSD, Duration: result.DurationMs,
		Model: result.Model, Output: truncate(result.Output, 500), Error: truncate(result.Error, 300),
	})

	// Send slot pressure warning before response if present.
	if result.SlotWarning != "" {
		db.sendMessage(msg.ChannelID, result.SlotWarning)
	}

	// Send response embed.
	db.sendRouteResponse(msg.ChannelID, &route, result, task, outputAlreadySent, msg.ID)
}

func (db *DiscordBot) sendRouteResponse(channelID string, route *RouteResult, result TaskResult, task Task, skipOutput bool, replyMsgID string) {
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
		// For very long outputs, truncate the middle to preserve the conclusion.
		const maxChunk = 1900 // leave room for markdown formatting
		const maxTotal = 5700 // 3 messages max — Discord rate-limits beyond this
		if len(output) > maxTotal {
			// Keep beginning (context) + end (conclusion), separated by "...".
			headSize := maxTotal * 2 / 5
			tailSize := maxTotal * 3 / 5
			// Find clean break points at newlines.
			if idx := strings.LastIndex(output[:headSize], "\n"); idx > headSize/2 {
				headSize = idx
			}
			tailStart := len(output) - tailSize
			if idx := strings.Index(output[tailStart:], "\n"); idx >= 0 && idx < tailSize/3 {
				tailStart += idx + 1
			}
			output = output[:headSize] + "\n\n... (truncated) ...\n\n" + output[tailStart:]
		}
		chunkCount := 0
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
			if chunkCount > 0 {
				time.Sleep(300 * time.Millisecond) // avoid Discord rate limiting
			}
			db.sendMessage(channelID, chunk)
			chunkCount++
		}
	}

	// Query today's cumulative token usage (this task already recorded before this call).
	todayIn, todayOut := todayTotalTokens(db.cfg.HistoryDB)

	// Send metadata as a small embed at the end, as a reply to the original message.
	db.sendEmbedReply(channelID, replyMsgID, discordEmbed{
		Color: color,
		Fields: []discordEmbedField{
			{Name: "Agent", Value: fmt.Sprintf("%s (%s)", route.Agent, route.Method), Inline: true},
			{Name: "Status", Value: result.Status, Inline: true},
			{Name: "Cost", Value: fmt.Sprintf("$%.4f", result.CostUSD), Inline: true},
			{Name: "Duration", Value: formatDurationMs(result.DurationMs), Inline: true},
			{Name: "今日 Token", Value: formatTokenField(todayIn, todayOut, db.cfg.CostAlert.DailyTokenLimit), Inline: true},
		},
		Footer:    &discordEmbedFooter{Text: fmt.Sprintf("Task: %s", task.ID[:8])},
		Timestamp: time.Now().Format(time.RFC3339),
	})
}

