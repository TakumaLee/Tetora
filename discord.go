package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"tetora/internal/audit"
	tetoraConfig "tetora/internal/config"
	"tetora/internal/discord"
	"tetora/internal/provider"
	"tetora/internal/history"
	"tetora/internal/log"
	"tetora/internal/messaging"
	"tetora/internal/tmux"
	"tetora/internal/trace"
	"tetora/internal/upload"
	"tetora/internal/webhook"
)

// errNoSavedSession is the error string emitted when a provider cannot find the
// session referenced by the stored session ID (e.g. after provider switch or
// cross-machine config sync).
const errNoSavedSession = "No saved session found"

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
	stopCh       chan struct{}
	interactions *discordInteractionState // P14.1: tracks pending component interactions
	threads       *threadBindingStore      // P14.2: per-thread agent bindings
	threadParents *threadParentCache      // thread→parent channel cache
	reactions     *discord.ReactionManager // P14.3: lifecycle reactions
	approvalGate *discordApprovalGate     // P28.0: approval gate
	forumBoard   *discord.ForumBoard       // P14.4: forum task board
	voice        *discordVoiceManager     // P14.5: voice channel manager
	gatewayConn  *wsConn                  // P14.5: active gateway connection for voice state updates
	notifier     *discord.TaskNotifier     // task notification (thread-per-task)
	terminal     *terminalBridge         // terminal bridge (tmux sessions)
	msgSem       chan struct{}            // limits concurrent message handlers
	// Message dedup: ring buffer of recently processed message IDs to prevent
	// duplicate handling on gateway reconnect/resume event replay.
	dedupMu    sync.Mutex
	dedupRing  [128]string
	dedupIdx   int

	// !chat lock: channel → locked agent name.
	chatLock   map[string]string
	chatLockMu sync.RWMutex
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
		stopCh:       make(chan struct{}),
		interactions:  newDiscordInteractionState(), // P14.1
		threads:       newThreadBindingStore(),      // P14.2
		threadParents: newThreadParentCache(),
		msgSem:        make(chan struct{}, 32),
	}

	// P14.3: Initialize reaction manager.
	if cfg.Discord.Reactions.Enabled {
		db.reactions = discord.NewReactionManager(db.api, cfg.Discord.Reactions.Emojis)
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
		db.notifier = discord.NewTaskNotifier(db.api, ch)
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
	ws, err := wsConnect(discord.GatewayURL)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer ws.Close()

	// P14.5: Store gateway connection for voice state updates
	db.gatewayConn = ws
	defer func() { db.gatewayConn = nil }()

	// Read Hello (op 10).
	var hello discord.GatewayPayload
	if err := ws.ReadJSON(&hello); err != nil {
		return fmt.Errorf("read hello: %w", err)
	}
	if hello.Op != discord.OpHello {
		return fmt.Errorf("expected op 10, got %d", hello.Op)
	}

	var hd discord.HelloData
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

		var payload discord.GatewayPayload
		if err := ws.ReadJSON(&payload); err != nil {
			return fmt.Errorf("read: %w", err)
		}

		if payload.S != nil {
			db.seqMu.Lock()
			db.seq = *payload.S
			db.seqMu.Unlock()
		}

		switch payload.Op {
		case discord.OpDispatch:
			db.handleEvent(payload)
		case discord.OpHeartbeat:
			db.sendHeartbeatWS(ws)
		case discord.OpReconnect:
			log.Info("discord gateway reconnect requested")
			return nil
		case discord.OpInvalidSession:
			log.Warn("discord invalid session")
			db.sessionID = ""
			return nil
		case discord.OpHeartbeatAck:
			// OK
		}
	}
}

// --- Event Handling ---

func (db *DiscordBot) handleEvent(payload discord.GatewayPayload) {
	switch payload.T {
	case "READY":
		var ready discord.ReadyData
		if json.Unmarshal(payload.D, &ready) == nil {
			db.botUserID = ready.User.ID
			db.sessionID = ready.SessionID
			log.Info("discord bot connected", "user", ready.User.Username, "id", ready.User.ID)

			// P14.5: Auto-join voice channels if configured
			if db.cfg.Discord.Voice.Enabled && len(db.cfg.Discord.Voice.AutoJoin) > 0 {
				go db.voice.AutoJoinChannels()
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
					db.handleMessageWithType(msgT.Message, msgT.ChannelType)
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
			db.voice.HandleVoiceStateUpdate(vsu)
		}
	case "VOICE_SERVER_UPDATE":
		// P14.5: Handle voice server updates
		var vsuData voiceServerUpdateData
		if json.Unmarshal(payload.D, &vsuData) == nil {
			db.voice.HandleVoiceServerUpdate(vsuData)
		}
	case "INTERACTION_CREATE":
		// Handle button clicks and component interactions via Gateway.
		var interaction discord.Interaction
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
func (db *DiscordBot) handleMessageWithType(msg discord.Message, channelType int) {
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

func (db *DiscordBot) handleMessage(msg discord.Message) {
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
	if db.forumBoard != nil && db.forumBoard.IsConfigured() {
		if strings.HasPrefix(text, "/assign") {
			args := strings.TrimPrefix(text, "/assign")
			reply := db.forumBoard.HandleAssignCommand(msg.ChannelID, msg.GuildID, args)
			db.sendMessage(msg.ChannelID, reply)
			return
		}
		if strings.HasPrefix(text, "/status") {
			args := strings.TrimPrefix(text, "/status")
			reply := db.forumBoard.HandleStatusCommand(msg.ChannelID, args)
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

	// !chat lock: skip smart dispatch, route directly to locked agent.
	if agent := db.getChatLock(msg.ChannelID); agent != "" {
		db.handleDirectRoute(msg, text, agent)
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

// downloadDiscordAttachment fetches an attachment from Discord CDN and saves it locally.
func downloadDiscordAttachment(baseDir string, att discord.Attachment) (*upload.File, error) {
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

func (db *DiscordBot) handleCommand(msg discord.Message, cmdText string) {
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
	case "local":
		db.cmdLocal(msg, args)
	case "cloud":
		db.cmdCloud(msg, args)
	case "mode":
		db.cmdMode(msg)
	case "new":
		db.cmdNewSession(msg)
	case "cancel":
		db.cmdCancel(msg)
	case "chat":
		if args == "" {
			db.sendMessage(msg.ChannelID, "Usage: `!chat <agent-name>`")
		} else {
			db.cmdChat(msg, strings.Fields(args)[0])
		}
	case "end":
		db.cmdEnd(msg)
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

func (db *DiscordBot) cmdStatus(msg discord.Message) {
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
	db.sendEmbed(msg.ChannelID, discord.Embed{
		Title: "Tetora Status",
		Color: 0x5865F2,
		Fields: []discord.EmbedField{
			{Name: "Version", Value: "v" + tetoraVersion, Inline: true},
			{Name: "Running", Value: fmt.Sprintf("%d", running), Inline: true},
			{Name: "Cron Jobs", Value: fmt.Sprintf("%d", jobs), Inline: true},
		},
		Timestamp: time.Now().Format(time.RFC3339),
	})
}

func (db *DiscordBot) cmdJobs(msg discord.Message) {
	if db.cron == nil {
		db.sendMessage(msg.ChannelID, "Cron engine not available.")
		return
	}
	jobs := db.cron.ListJobs()
	if len(jobs) == 0 {
		db.sendMessage(msg.ChannelID, "No cron jobs configured.")
		return
	}
	var fields []discord.EmbedField
	for _, j := range jobs {
		status := "enabled"
		if !j.Enabled {
			status = "disabled"
		}
		fields = append(fields, discord.EmbedField{
			Name: j.Name, Value: fmt.Sprintf("`%s` [%s]", j.Schedule, status), Inline: true,
		})
	}
	db.sendEmbed(msg.ChannelID, discord.Embed{
		Title: fmt.Sprintf("Cron Jobs (%d)", len(jobs)), Color: 0x57F287, Fields: fields,
	})
}

func (db *DiscordBot) cmdCost(msg discord.Message) {
	dbPath := db.cfg.HistoryDB
	if dbPath == "" {
		db.sendMessage(msg.ChannelID, "History DB not configured.")
		return
	}
	stats, err := history.QueryCostStats(dbPath)
	if err != nil {
		db.sendMessage(msg.ChannelID, fmt.Sprintf("Error: %v", err))
		return
	}
	db.sendEmbed(msg.ChannelID, discord.Embed{
		Title: "Cost Summary",
		Color: 0xFEE75C,
		Fields: []discord.EmbedField{
			{Name: "Today", Value: fmt.Sprintf("$%.4f", stats.Today), Inline: true},
			{Name: "This Week", Value: fmt.Sprintf("$%.4f", stats.Week), Inline: true},
			{Name: "This Month", Value: fmt.Sprintf("$%.4f", stats.Month), Inline: true},
		},
	})
}

func (db *DiscordBot) cmdModel(msg discord.Message, args string) {
	parts := strings.Fields(args)

	// !model → grouped status display
	if len(parts) == 0 {
		db.cmdModelStatus(msg)
		return
	}

	// !model pick [agent] → interactive picker
	if parts[0] == "pick" {
		agentName := ""
		if len(parts) > 1 {
			agentName = parts[1]
		}
		db.cmdModelPick(msg, agentName)
		return
	}

	// !model <model> [agent] → set model (auto-switches provider)
	model := parts[0]
	agentName := db.cfg.SmartDispatch.DefaultAgent
	if agentName == "" {
		agentName = "default"
	}
	if len(parts) > 1 {
		agentName = parts[1]
	}

	// Infer provider from model name and auto-create if needed.
	inferredProvider := ""
	if presetName, ok := provider.InferProviderFromModelWithPref(model, db.cfg.ClaudeProvider); ok {
		if err := ensureProvider(db.cfg, presetName); err != nil {
			db.sendMessage(msg.ChannelID, fmt.Sprintf("Warning: could not auto-create provider `%s`: %v", presetName, err))
		} else {
			inferredProvider = presetName
		}
	}
	// If prefix inference failed, check dynamic providers (Ollama, LM Studio).
	if inferredProvider == "" {
		for _, p := range provider.Presets {
			if !p.Dynamic {
				continue
			}
			models, err := provider.FetchPresetModels(p)
			if err != nil {
				continue
			}
			for _, m := range models {
				// Match exact name or name without tag (e.g. "dolphin-mistral" matches "dolphin-mistral:latest").
				if m == model || strings.TrimSuffix(m, ":latest") == model {
					if err := ensureProvider(db.cfg, p.Name); err == nil {
						inferredProvider = p.Name
					}
					break
				}
			}
			if inferredProvider != "" {
				break
			}
		}
	}

	res, err := updateAgentModel(db.cfg, agentName, model, inferredProvider)
	if err != nil {
		db.sendMessage(msg.ChannelID, fmt.Sprintf("Error: %v", err))
		return
	}

	reply := fmt.Sprintf("**%s** model: `%s` → `%s`", agentName, res.OldModel, model)
	if res.NewProvider != "" {
		reply += fmt.Sprintf(" (provider: `%s` → `%s`)", res.OldProvider, res.NewProvider)
		// Auto-start new session when provider changes — old session IDs are invalid.
		db.autoNewSession(msg.ChannelID, res.OldProvider, res.NewProvider)
	}
	db.sendMessage(msg.ChannelID, reply)
}

// cmdModelStatus shows all agents grouped by Cloud / Local.
func (db *DiscordBot) cmdModelStatus(msg discord.Message) {
	type agentEntry struct {
		name     string
		model    string
		provider string
	}

	var cloudAgents, localAgents []agentEntry
	for name, ac := range db.cfg.Agents {
		m := ac.Model
		if m == "" {
			m = db.cfg.DefaultModel
		}
		p := ac.Provider
		if p == "" {
			p = db.cfg.DefaultProvider
		}
		if p == "" {
			p = "claude"
		}
		entry := agentEntry{name: name, model: m, provider: p}
		if provider.IsLocalProvider(p) {
			localAgents = append(localAgents, entry)
		} else {
			cloudAgents = append(cloudAgents, entry)
		}
	}

	sort.Slice(cloudAgents, func(i, j int) bool { return cloudAgents[i].name < cloudAgents[j].name })
	sort.Slice(localAgents, func(i, j int) bool { return localAgents[i].name < localAgents[j].name })

	var fields []discord.EmbedField

	if len(cloudAgents) > 0 {
		var lines []string
		for _, a := range cloudAgents {
			lines = append(lines, fmt.Sprintf("`%s` — %s (%s)", a.name, a.model, a.provider))
		}
		fields = append(fields, discord.EmbedField{
			Name:  fmt.Sprintf("☁ Cloud (%d)", len(cloudAgents)),
			Value: strings.Join(lines, "\n"),
		})
	}

	if len(localAgents) > 0 {
		var lines []string
		for _, a := range localAgents {
			lines = append(lines, fmt.Sprintf("`%s` — %s (%s)", a.name, a.model, a.provider))
		}
		fields = append(fields, discord.EmbedField{
			Name:  fmt.Sprintf("🏠 Local (%d)", len(localAgents)),
			Value: strings.Join(lines, "\n"),
		})
	}

	mode := db.cfg.InferenceMode
	if mode == "" {
		mode = "mixed"
	}

	suffix := fmt.Sprintf("_%s_%d", msg.ChannelID, time.Now().UnixMilli())
	db.sendEmbedWithComponents(msg.ChannelID, discord.Embed{
		Title:  "Agent Models",
		Color:  0x5865F2,
		Fields: fields,
		Footer: &discord.EmbedFooter{Text: fmt.Sprintf("Mode: %s | !model pick [agent] | !local | !cloud", mode)},
	}, []discord.Component{
		discordActionRow(
			discordButton("model_pick_start"+suffix, "Pick Model", discord.ButtonStylePrimary),
			discordButton("mode_local"+suffix, "Switch All Local", discord.ButtonStyleSuccess),
			discordButton("mode_cloud"+suffix, "Switch All Cloud", discord.ButtonStyleSecondary),
		),
	})

	// Register button callbacks.
	db.interactions.register(&pendingInteraction{
		CustomID:  "model_pick_start" + suffix,
		ChannelID: msg.ChannelID,
		UserID:    msg.Author.ID,
		CreatedAt: time.Now(),
		AllowedIDs: []string{msg.Author.ID},
		Reusable:  true,
		Callback: func(data discord.InteractionData) {
			db.cmdModelPick(msg, "")
		},
	})
	db.interactions.register(&pendingInteraction{
		CustomID:  "mode_local" + suffix,
		ChannelID: msg.ChannelID,
		UserID:    msg.Author.ID,
		CreatedAt: time.Now(),
		AllowedIDs: []string{msg.Author.ID},
		Callback: func(data discord.InteractionData) {
			db.cmdLocal(msg, "")
		},
	})
	db.interactions.register(&pendingInteraction{
		CustomID:  "mode_cloud" + suffix,
		ChannelID: msg.ChannelID,
		UserID:    msg.Author.ID,
		CreatedAt: time.Now(),
		AllowedIDs: []string{msg.Author.ID},
		Callback: func(data discord.InteractionData) {
			db.cmdCloud(msg, "")
		},
	})
}

// cmdModelPick starts an interactive model picker flow.
func (db *DiscordBot) cmdModelPick(msg discord.Message, agentName string) {
	// Step 1: If no agent specified, show agent select menu.
	if agentName == "" {
		var options []discord.SelectOption
		// Sort agent names for consistent display.
		names := make([]string, 0, len(db.cfg.Agents))
		for name := range db.cfg.Agents {
			names = append(names, name)
		}
		sort.Strings(names)

		for _, name := range names {
			ac := db.cfg.Agents[name]
			m := ac.Model
			if m == "" {
				m = db.cfg.DefaultModel
			}
			p := ac.Provider
			if p == "" {
				p = db.cfg.DefaultProvider
			}
			desc := fmt.Sprintf("%s (%s)", m, p)
			if len(desc) > 100 {
				desc = desc[:100]
			}
			options = append(options, discord.SelectOption{
				Label:       name,
				Value:       name,
				Description: desc,
			})
		}

		// Discord limits to 25 options.
		if len(options) > 25 {
			options = options[:25]
		}

		customID := fmt.Sprintf("model_pick_agent_%d", time.Now().UnixMilli())
		db.sendEmbedWithComponents(msg.ChannelID, discord.Embed{
			Title: "Pick Model — Select Agent",
			Color: 0x5865F2,
		}, []discord.Component{
			discordActionRow(discordSelectMenu(customID, "Select an agent...", options)),
		})

		db.interactions.register(&pendingInteraction{
			CustomID:   customID,
			ChannelID:  msg.ChannelID,
			UserID:     msg.Author.ID,
			CreatedAt:  time.Now(),
			AllowedIDs: []string{msg.Author.ID},
			Callback: func(data discord.InteractionData) {
				if len(data.Values) > 0 {
					db.cmdModelPickProvider(msg, data.Values[0])
				}
			},
		})
		return
	}

	// Agent specified — go directly to provider selection.
	if _, ok := db.cfg.Agents[agentName]; !ok {
		db.sendMessage(msg.ChannelID, fmt.Sprintf("Agent `%s` not found.", agentName))
		return
	}
	db.cmdModelPickProvider(msg, agentName)
}

// cmdModelPickProvider shows provider selection buttons for an agent.
func (db *DiscordBot) cmdModelPickProvider(msg discord.Message, agentName string) {
	ts := fmt.Sprintf("%d", time.Now().UnixMilli())

	var buttons []discord.Component
	for _, preset := range provider.Presets {
		if preset.Name == "custom" {
			continue
		}
		customID := fmt.Sprintf("model_pick_prov_%s_%s_%s", agentName, preset.Name, ts)
		style := discord.ButtonStyleSecondary
		if provider.IsLocalProvider(preset.Name) {
			style = discord.ButtonStyleSuccess // green for local
		}
		buttons = append(buttons, discordButton(customID, preset.DisplayName, style))

		presetName := preset.Name // capture
		db.interactions.register(&pendingInteraction{
			CustomID:   customID,
			ChannelID:  msg.ChannelID,
			UserID:     msg.Author.ID,
			CreatedAt:  time.Now(),
			AllowedIDs: []string{msg.Author.ID},
			Callback: func(data discord.InteractionData) {
				db.cmdModelPickModel(msg, agentName, presetName)
			},
		})
	}

	// Discord allows max 5 buttons per action row.
	var rows []discord.Component
	for i := 0; i < len(buttons); i += 5 {
		end := i + 5
		if end > len(buttons) {
			end = len(buttons)
		}
		rows = append(rows, discordActionRow(buttons[i:end]...))
	}

	ac := db.cfg.Agents[agentName]
	currentModel := ac.Model
	if currentModel == "" {
		currentModel = db.cfg.DefaultModel
	}

	db.sendEmbedWithComponents(msg.ChannelID, discord.Embed{
		Title:       fmt.Sprintf("Pick Model — %s", agentName),
		Description: fmt.Sprintf("Current: `%s`\nSelect a provider:", currentModel),
		Color:       0x5865F2,
	}, rows)
}

// cmdModelPickModel shows model selection for a specific agent + provider.
func (db *DiscordBot) cmdModelPickModel(msg discord.Message, agentName, presetName string) {
	preset, ok := provider.GetPreset(presetName)
	if !ok {
		db.sendMessage(msg.ChannelID, fmt.Sprintf("Preset `%s` not found.", presetName))
		return
	}

	// Fetch available models.
	models, err := provider.FetchPresetModels(preset)
	if err != nil {
		db.sendMessage(msg.ChannelID, fmt.Sprintf("Cannot fetch models from `%s`: %v", presetName, err))
		return
	}
	if len(models) == 0 {
		models = preset.Models
	}
	if len(models) == 0 {
		db.sendMessage(msg.ChannelID, fmt.Sprintf("No models available for `%s`.", preset.DisplayName))
		return
	}

	// Build select menu options.
	var options []discord.SelectOption
	for _, m := range models {
		if len(options) >= 25 {
			break
		}
		options = append(options, discord.SelectOption{
			Label: m,
			Value: m,
		})
	}

	customID := fmt.Sprintf("model_pick_model_%s_%s_%d", agentName, presetName, time.Now().UnixMilli())
	db.sendEmbedWithComponents(msg.ChannelID, discord.Embed{
		Title:       fmt.Sprintf("Pick Model — %s — %s", agentName, preset.DisplayName),
		Description: "Select a model:",
		Color:       0x5865F2,
	}, []discord.Component{
		discordActionRow(discordSelectMenu(customID, "Select model...", options)),
	})

	db.interactions.register(&pendingInteraction{
		CustomID:   customID,
		ChannelID:  msg.ChannelID,
		UserID:     msg.Author.ID,
		CreatedAt:  time.Now(),
		AllowedIDs: []string{msg.Author.ID},
		Callback: func(data discord.InteractionData) {
			if len(data.Values) == 0 {
				return
			}
			selectedModel := data.Values[0]

			// Auto-create provider if needed.
			if err := ensureProvider(db.cfg, presetName); err != nil {
				db.sendMessage(msg.ChannelID, fmt.Sprintf("Warning: could not auto-create provider `%s`: %v", presetName, err))
			}

			res, err := updateAgentModel(db.cfg, agentName, selectedModel, presetName)
			if err != nil {
				db.sendMessage(msg.ChannelID, fmt.Sprintf("Error: %v", err))
				return
			}

			// Auto-start new session when provider changes.
			if res.NewProvider != "" {
				db.autoNewSession(msg.ChannelID, res.OldProvider, res.NewProvider)
			}

			db.sendEmbed(msg.ChannelID, discord.Embed{
				Title: "Model Updated",
				Color: 0x57F287, // green
				Fields: []discord.EmbedField{
					{Name: "Agent", Value: agentName, Inline: true},
					{Name: "Model", Value: fmt.Sprintf("`%s` → `%s`", res.OldModel, selectedModel), Inline: true},
					{Name: "Provider", Value: fmt.Sprintf("`%s` → `%s`", res.OldProvider, presetName), Inline: true},
				},
			})
		},
	})
}

// cmdLocal switches agents to local models (Ollama).
func (db *DiscordBot) cmdLocal(msg discord.Message, args string) {
	// Check Ollama is reachable.
	ollamaPreset, _ := provider.GetPreset("ollama")
	ollamaModels, err := provider.FetchPresetModels(ollamaPreset)
	if err != nil || len(ollamaModels) == 0 {
		db.sendMessage(msg.ChannelID, "Ollama is not reachable or has no models.\nStart it with: `ollama serve`")
		return
	}

	// Ensure ollama provider exists.
	if err := ensureProvider(db.cfg, "ollama"); err != nil {
		db.sendMessage(msg.ChannelID, fmt.Sprintf("Error creating ollama provider: %v", err))
		return
	}

	target := strings.TrimSpace(args)
	switched := 0
	pinned := 0
	updatedAgents := make(map[string]tetoraConfig.AgentConfig)

	for name, ac := range db.cfg.Agents {
		if target != "" && name != target {
			continue
		}
		if ac.PinMode != "" {
			pinned++
			continue
		}
		if provider.IsLocalProvider(ac.Provider) {
			continue // already local
		}
		ac.CloudModel = ac.Model
		if ac.LocalModel != "" {
			ac.Model = ac.LocalModel
		} else {
			ac.Model = ollamaModels[0]
		}
		ac.Provider = "ollama"
		db.cfg.Agents[name] = ac
		updatedAgents[name] = ac
		switched++
	}

	if switched == 0 && target != "" {
		db.sendMessage(msg.ChannelID, fmt.Sprintf("Agent `%s` not found or already on local.", target))
		return
	}

	// Persist.
	configPath := findConfigPath()
	if err := tetoraConfig.SaveInferenceMode(configPath, "local", updatedAgents); err != nil {
		db.sendMessage(msg.ChannelID, fmt.Sprintf("Error saving config: %v", err))
		return
	}
	db.cfg.InferenceMode = "local"

	desc := fmt.Sprintf("Switched **%d** agents to local (Ollama)\nUsing model: `%s`", switched, ollamaModels[0])
	if pinned > 0 {
		desc += fmt.Sprintf("\n%d agents pinned (unchanged)", pinned)
	}

	db.sendEmbed(msg.ChannelID, discord.Embed{
		Title:       "🏠 Local Mode",
		Description: desc,
		Color:       0x57F287,
	})
}

// cmdCloud switches agents back to cloud models.
func (db *DiscordBot) cmdCloud(msg discord.Message, args string) {
	target := strings.TrimSpace(args)
	switched := 0
	pinned := 0
	updatedAgents := make(map[string]tetoraConfig.AgentConfig)

	for name, ac := range db.cfg.Agents {
		if target != "" && name != target {
			continue
		}
		if ac.PinMode != "" {
			pinned++
			continue
		}
		if !provider.IsLocalProvider(ac.Provider) {
			continue // already on cloud
		}
		if ac.CloudModel != "" {
			ac.Model = ac.CloudModel
			if preset, ok := provider.InferProviderFromModelWithPref(ac.CloudModel, db.cfg.ClaudeProvider); ok {
				ac.Provider = preset
			} else {
				ac.Provider = db.cfg.DefaultProvider
			}
		} else {
			ac.Model = db.cfg.DefaultModel
			ac.Provider = db.cfg.DefaultProvider
		}
		db.cfg.Agents[name] = ac
		updatedAgents[name] = ac
		switched++
	}

	if switched == 0 && target != "" {
		db.sendMessage(msg.ChannelID, fmt.Sprintf("Agent `%s` not found or already on cloud.", target))
		return
	}

	configPath := findConfigPath()
	if err := tetoraConfig.SaveInferenceMode(configPath, "", updatedAgents); err != nil {
		db.sendMessage(msg.ChannelID, fmt.Sprintf("Error saving config: %v", err))
		return
	}
	db.cfg.InferenceMode = ""

	desc := fmt.Sprintf("Restored **%d** agents to cloud models", switched)
	if pinned > 0 {
		desc += fmt.Sprintf("\n%d agents pinned (unchanged)", pinned)
	}

	db.sendEmbed(msg.ChannelID, discord.Embed{
		Title:       "☁ Cloud Mode",
		Description: desc,
		Color:       0x5865F2,
	})
}

// cmdMode shows current inference mode summary.
func (db *DiscordBot) cmdMode(msg discord.Message) {
	cloud, local := 0, 0
	for _, ac := range db.cfg.Agents {
		p := ac.Provider
		if p == "" {
			p = db.cfg.DefaultProvider
		}
		if provider.IsLocalProvider(p) {
			local++
		} else {
			cloud++
		}
	}

	mode := db.cfg.InferenceMode
	if mode == "" {
		mode = "mixed"
	}

	modeSuffix := fmt.Sprintf("_%s_%d", msg.ChannelID, time.Now().UnixMilli())
	db.sendEmbedWithComponents(msg.ChannelID, discord.Embed{
		Title: "Inference Mode",
		Color: 0x5865F2,
		Fields: []discord.EmbedField{
			{Name: "Mode", Value: mode, Inline: true},
			{Name: "Cloud", Value: fmt.Sprintf("%d agents", cloud), Inline: true},
			{Name: "Local", Value: fmt.Sprintf("%d agents", local), Inline: true},
		},
	}, []discord.Component{
		discordActionRow(
			discordButton("mode_local_cmd"+modeSuffix, "Switch All Local", discord.ButtonStyleSuccess),
			discordButton("mode_cloud_cmd"+modeSuffix, "Switch All Cloud", discord.ButtonStyleSecondary),
		),
	})

	db.interactions.register(&pendingInteraction{
		CustomID:   "mode_local_cmd" + modeSuffix,
		ChannelID:  msg.ChannelID,
		UserID:     msg.Author.ID,
		CreatedAt:  time.Now(),
		AllowedIDs: []string{msg.Author.ID},
		Callback: func(data discord.InteractionData) {
			db.cmdLocal(msg, "")
		},
	})
	db.interactions.register(&pendingInteraction{
		CustomID:   "mode_cloud_cmd" + modeSuffix,
		ChannelID:  msg.ChannelID,
		UserID:     msg.Author.ID,
		CreatedAt:  time.Now(),
		AllowedIDs: []string{msg.Author.ID},
		Callback: func(data discord.InteractionData) {
			db.cmdCloud(msg, "")
		},
	})
}

func (db *DiscordBot) cmdCancel(msg discord.Message) {
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

func (db *DiscordBot) cmdAsk(msg discord.Message, prompt string) {
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
	db.sendEmbed(msg.ChannelID, discord.Embed{
		Description: output,
		Color:       color,
		Fields: []discord.EmbedField{
			{Name: "Cost", Value: fmt.Sprintf("$%.4f", result.CostUSD), Inline: true},
			{Name: "Duration", Value: formatDurationMs(result.DurationMs), Inline: true},
		},
		Footer:    &discord.EmbedFooter{Text: fmt.Sprintf("ask | %s", task.ID[:8])},
		Timestamp: time.Now().Format(time.RFC3339),
	})
}

func (db *DiscordBot) getChatLock(channelID string) string {
	db.chatLockMu.RLock()
	defer db.chatLockMu.RUnlock()
	return db.chatLock[channelID]
}

func (db *DiscordBot) cmdChat(msg discord.Message, agentName string) {
	if _, ok := db.cfg.Agents[agentName]; !ok {
		db.sendMessage(msg.ChannelID, fmt.Sprintf("Agent `%s` not found.", agentName))
		return
	}
	db.chatLockMu.Lock()
	if db.chatLock == nil {
		db.chatLock = make(map[string]string)
	}
	db.chatLock[msg.ChannelID] = agentName
	db.chatLockMu.Unlock()
	db.sendMessage(msg.ChannelID, fmt.Sprintf("Locked to **%s**. All messages route directly to this agent. Use `!end` to unlock.", agentName))
}

func (db *DiscordBot) cmdEnd(msg discord.Message) {
	db.chatLockMu.Lock()
	agent := db.chatLock[msg.ChannelID]
	delete(db.chatLock, msg.ChannelID)
	db.chatLockMu.Unlock()
	if agent == "" {
		db.sendMessage(msg.ChannelID, "No active chat lock.")
		return
	}
	db.sendMessage(msg.ChannelID, fmt.Sprintf("Unlocked from **%s**. Smart dispatch resumed.", agent))
}

// autoNewSession archives the current channel session when provider changes,
// since session/thread IDs from one provider are invalid in another.
func (db *DiscordBot) autoNewSession(channelID, oldProvider, newProvider string) {
	if oldProvider == newProvider || oldProvider == "" || newProvider == "" {
		return
	}
	dbPath := db.cfg.HistoryDB
	if dbPath == "" {
		return
	}
	chKey := channelSessionKey("discord", channelID)
	_ = archiveChannelSession(dbPath, chKey) // best-effort
}

func (db *DiscordBot) cmdNewSession(msg discord.Message) {
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

func (db *DiscordBot) cmdHelp(msg discord.Message) {
	db.sendEmbed(msg.ChannelID, discord.Embed{
		Title:       "Tetora Help",
		Description: "Mention me with a message to route it to the best agent, or use commands:",
		Color:       0x5865F2,
		Fields: []discord.EmbedField{
			{Name: "!status", Value: "Show daemon status"},
			{Name: "!jobs", Value: "List cron jobs"},
			{Name: "!cost", Value: "Show cost summary"},
			{Name: "!model [model] [agent]", Value: "Show/switch model"},
			{Name: "!model pick [agent]", Value: "Interactive model picker"},
			{Name: "!local [agent]", Value: "Switch to local models (Ollama)"},
			{Name: "!cloud [agent]", Value: "Switch back to cloud models"},
			{Name: "!mode", Value: "Show inference mode summary"},
			{Name: "!new", Value: "Start a new session (clear context)"},
			{Name: "!cancel", Value: "Cancel all running tasks"},
			{Name: "!chat <agent>", Value: "Lock this channel to an agent (skip dispatch)"},
			{Name: "!end", Value: "Unlock channel, resume smart dispatch"},
			{Name: "!ask <prompt>", Value: "Quick question (no routing, no session)"},
			{Name: "!approve [tool|reset]", Value: "Manage auto-approved tools"},
			{Name: "!help", Value: "Show this help"},
			{Name: "Free text", Value: "Mention me + your prompt for smart dispatch"},
		},
	})
}

func (db *DiscordBot) cmdApprove(msg discord.Message, args string) {
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
func (db *DiscordBot) handleDirectRoute(msg discord.Message, prompt string, agent string) {
	route := RouteResult{Agent: agent, Method: "explicit", Confidence: "high"}
	db.executeRoute(msg, prompt, route)
}

// --- Smart Dispatch ---

func (db *DiscordBot) handleRoute(msg discord.Message, prompt string) {
	ctx := trace.WithID(context.Background(), trace.NewID("discord"))
	route := routeTask(ctx, db.cfg, RouteRequest{
		Prompt:    prompt,
		Source:    "discord",
		ChannelID: msg.ChannelID,
		GuildID:   msg.GuildID,
		UserID:    msg.Author.ID,
	})
	log.InfoCtx(ctx, "discord route result", "prompt", truncate(prompt, 60), "agent", route.Agent, "method", route.Method)
	db.executeRoute(msg, prompt, *route)
}

// archiveStaleSession detects a "No saved session found" error and, when found,
// archives the session in the DB so the next request gets a fresh start.
// Returns true if a stale session was detected (caller should send reset message).
func archiveStaleSession(ctx context.Context, dbPath string, sess *Session, resultErr string) bool {
	if !strings.Contains(resultErr, errNoSavedSession) {
		return false
	}
	log.WarnCtx(ctx, "Auto-cleared stale session ID", "error", resultErr)
	if sess != nil {
		if err := updateSessionStatus(dbPath, sess.ID, "archived"); err != nil {
			log.WarnCtx(ctx, "Failed to archive stale session", "sessionID", sess.ID, "error", err)
		}
	}
	return true
}

// executeRoute runs a routed task through the full Discord execution pipeline
// (session, SSE events, progress messages, reply).
func (db *DiscordBot) executeRoute(msg discord.Message, prompt string, route RouteResult) {
	db.sendTyping(msg.ChannelID)

	// P14.3: Add queued reaction.
	if db.reactions != nil {
		db.reactions.ReactQueued(msg.ChannelID, msg.ID)
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

	// Channel session.
	// Look up existing session once; reuse it directly when the agent matches
	// to avoid a redundant DB read inside getOrCreateChannelSession.
	chKey := channelSessionKey("discord", msg.ChannelID)
	agent := route.Agent

	existing, findErr := findChannelSession(dbPath, chKey)
	if findErr != nil {
		log.WarnCtx(ctx, "discord findChannelSession error", "error", findErr)
	}

	// For non-deterministic routes (keyword/LLM), keep the existing session's
	// agent to avoid constant session churn.
	if route.Method != "binding" && route.Method != "explicit" && existing != nil {
		agent = existing.Agent
	}

	var sess *Session
	if existing != nil && existing.Agent == agent {
		sess = existing
	} else {
		var err error
		sess, err = getOrCreateChannelSession(dbPath, "discord", chKey, agent, "")
		if err != nil {
			log.ErrorCtx(ctx, "discord session error", "error", err)
		}
	}

	// Update Discord activity with resolved agent (after session-stickiness override).
	if db.state != nil {
		db.state.mu.Lock()
		if da, ok := db.state.discordActivities[activityID]; ok {
			da.Agent = agent
		}
		db.state.mu.Unlock()
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
			// New session with no history — carry forward context from the archived predecessor.
			if sessionCtx == "" {
				if prev, err := findLastArchivedChannelSession(dbPath, chKey); err == nil && prev != nil {
					sessionCtx = buildSessionContext(dbPath, prev.ID, db.cfg.Session.ContextMessagesOrDefault())
					log.InfoCtx(ctx, "auto-continuing from archived session",
						"prevSession", prev.ID[:8], "channel", chKey)
				}
			}
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
		db.reactions.ReactThinking(msg.ChannelID, msg.ID)
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

	// Create cancellable context so Escape button and !cancel can interrupt.
	taskCtx, taskCancel := context.WithCancel(ctx)
	defer taskCancel()

	// Register task in dispatch state so !cancel can find it.
	if db.state != nil {
		db.state.mu.Lock()
		db.state.running[task.ID] = &taskState{
			task:         task,
			startAt:      time.Now(),
			lastActivity: time.Now(),
			cancelFn:     taskCancel,
		}
		db.state.mu.Unlock()
		defer func() {
			db.state.mu.Lock()
			delete(db.state.running, task.ID)
			db.state.mu.Unlock()
		}()
	}

	// Start progress message for live Discord updates.
	// Controlled by showProgress config (default: true).
	showProgress := db.cfg.Discord.ShowProgress == nil || *db.cfg.Discord.ShowProgress
	var progressMsgID string
	var progressStopCh chan struct{}
	var progressBuilder *discord.ProgressBuilder
	var progressEscapeID string // interaction custom ID for escape button cleanup
	if showProgress && db.state != nil && db.state.broker != nil {
		// Build escape button for the progress message.
		escapeID := fmt.Sprintf("progress_escape:%s", task.ID)
		escapeComponents := []discord.Component{
			discordActionRow(
				discordButton(escapeID, "Escape", discord.ButtonStyleDanger),
			),
		}

		msgID, err := db.sendMessageWithComponentsReturningID(msg.ChannelID, "Working...", escapeComponents)
		if err == nil && msgID != "" {
			progressMsgID = msgID
			progressEscapeID = escapeID
			progressStopCh = make(chan struct{})
			progressBuilder = discord.NewProgressBuilder()

			// Register escape button interaction.
			db.interactions.register(&pendingInteraction{
				CustomID:  escapeID,
				CreatedAt: time.Now(),
				Response: &discord.InteractionResponse{
					Type: discord.InteractionResponseUpdateMessage,
					Data: &discord.InteractionResponseData{
						Content: "Interrupted.",
					},
				},
				Callback: func(data discord.InteractionData) {
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
	result := runSingleTask(taskCtx, db.cfg, task, db.sem, db.childSem, route.Agent)

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
					if text := progressBuilder.GetText(); strings.TrimSpace(text) != "" {
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
			db.reactions.ReactDone(msg.ChannelID, msg.ID)
		} else {
			db.reactions.ReactError(msg.ChannelID, msg.ID)
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

	// Auto-recover from stale session errors (provider switch or machine migration).
	if result.Status != "success" && archiveStaleSession(ctx, dbPath, sess, result.Error) {
		db.sendMessage(msg.ChannelID, "♻️ **System Reset**: Detected environment change (Provider/Migration). Starting new session...")
		return
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

		// Persist full output to disk before sending, so it can be retrieved
		// even if Discord drops or truncates the message.
		if result.Status == "success" && strings.TrimSpace(output) != "" && db.cfg.BaseDir != "" {
			outDir := filepath.Join(db.cfg.BaseDir, "outputs")
			if err := os.MkdirAll(outDir, 0o755); err == nil {
				outPath := filepath.Join(outDir,
					fmt.Sprintf("%s_%s.txt", task.ID, time.Now().Format("20060102-150405")))
				if err := os.WriteFile(outPath, []byte(output), 0o644); err != nil {
					log.Warn("discord: failed to save output file", "task", task.ID, "err", err)
				}
			}
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
	todayIn, todayOut := history.TodayTotalTokens(db.cfg.HistoryDB)

	// Send metadata as a small embed at the end, as a reply to the original message.
	db.sendEmbedReply(channelID, replyMsgID, discord.Embed{
		Color: color,
		Fields: []discord.EmbedField{
			{Name: "Agent", Value: fmt.Sprintf("%s (%s)", route.Agent, route.Method), Inline: true},
			{Name: "Status", Value: result.Status, Inline: true},
			{Name: "Cost", Value: fmt.Sprintf("$%.4f", result.CostUSD), Inline: true},
			{Name: "Duration", Value: formatDurationMs(result.DurationMs), Inline: true},
			{Name: "今日 Token", Value: formatTokenField(todayIn, todayOut, db.cfg.CostAlert.DailyTokenLimit), Inline: true},
		},
		Footer:    &discord.EmbedFooter{Text: fmt.Sprintf("Task: %s", task.ID[:8])},
		Timestamp: time.Now().Format(time.RFC3339),
	})
}

// --- Voice (from discord_voice.go) ---

// Type aliases.
type discordVoiceManager = discord.VoiceManager
type voiceStateUpdatePayload = discord.VoiceStateUpdatePayload
type voiceServerUpdateData = discord.VoiceServerUpdateData
type voiceStateUpdateData = discord.VoiceStateUpdateData

// Constant aliases.
const (
	opVoiceStateUpdate     = discord.OpVoiceStateUpdate
	opVoiceServerUpdate    = discord.OpVoiceServerUpdate
	intentGuildVoiceStates = discord.IntentGuildVoiceStates
)

// newDiscordVoiceManager creates a VoiceManager wired to the bot's deps.
func newDiscordVoiceManager(bot *DiscordBot) *discordVoiceManager {
	deps := discord.VoiceDeps{
		SendGateway: func(payload discord.GatewayPayload) error {
			return bot.sendToGateway(discord.GatewayPayload(payload))
		},
	}

	if bot != nil {
		deps.BotUserID = bot.botUserID
		if bot.cfg != nil {
			deps.VoiceEnabled = bot.cfg.Discord.Voice.Enabled
			deps.AutoJoin = bot.cfg.Discord.Voice.AutoJoin
			deps.TTS = bot.cfg.Discord.Voice.TTS
		}
	}

	return discord.NewVoiceManager(deps)
}

// handleVoiceCommand processes /vc commands.
func (db *DiscordBot) handleVoiceCommand(msg discord.Message, args []string) {
	if !db.cfg.Discord.Voice.Enabled {
		db.sendMessage(msg.ChannelID, "Voice channel support is not enabled.")
		return
	}

	if len(args) == 0 {
		db.sendMessage(msg.ChannelID, "Usage: `/vc <join|leave|status> [channel_id]`")
		return
	}

	subCmd := args[0]

	switch subCmd {
	case "join":
		if len(args) < 2 {
			db.sendMessage(msg.ChannelID, "Usage: `/vc join <channel_id>`")
			return
		}
		channelID := args[1]
		guildID := msg.GuildID

		if guildID == "" {
			db.sendMessage(msg.ChannelID, "Voice channels are only available in guilds.")
			return
		}

		if err := db.voice.JoinVoiceChannel(guildID, channelID); err != nil {
			db.sendMessage(msg.ChannelID, fmt.Sprintf("Failed to join voice channel: %v", err))
		} else {
			db.sendMessage(msg.ChannelID, fmt.Sprintf("Joining voice channel <#%s>...", channelID))
		}

	case "leave":
		if err := db.voice.LeaveVoiceChannel(); err != nil {
			db.sendMessage(msg.ChannelID, fmt.Sprintf("Failed to leave voice channel: %v", err))
		} else {
			db.sendMessage(msg.ChannelID, "Leaving voice channel...")
		}

	case "status":
		status := db.voice.GetStatus()
		connected := status["connected"].(bool)
		if connected {
			db.sendMessage(msg.ChannelID,
				fmt.Sprintf("Connected to voice channel <#%s> in guild %s",
					status["channelId"], status["guildId"]))
		} else {
			db.sendMessage(msg.ChannelID, "Not connected to any voice channel.")
		}

	default:
		db.sendMessage(msg.ChannelID, "Unknown subcommand. Use: `join`, `leave`, or `status`")
	}
}

// sendToGateway sends a payload to the active gateway websocket.
func (db *DiscordBot) sendToGateway(payload discord.GatewayPayload) error {
	if db.gatewayConn == nil {
		return fmt.Errorf("no active gateway connection")
	}
	return db.gatewayConn.WriteJSON(payload)
}

// newDiscordForumBoard constructs a ForumBoard wired to DiscordBot dependencies.
func newDiscordForumBoard(bot *DiscordBot, cfg DiscordForumBoardConfig) *discord.ForumBoard {
	var deps discord.ForumBoardDeps
	var client *discord.Client

	if bot != nil {
		client = bot.api

		if bot.cfg != nil {
			deps.ThreadBindingsEnabled = bot.cfg.Discord.ThreadBindings.Enabled

			deps.ValidateAgent = func(name string) bool {
				if bot.cfg.Agents == nil {
					return false
				}
				_, ok := bot.cfg.Agents[name]
				return ok
			}
		}

		deps.AvailableRoles = func() []string {
			return bot.availableRoleNames()
		}

		if bot.threads != nil && bot.cfg != nil {
			deps.BindThread = func(guildID, threadID, role string) string {
				ttl := bot.cfg.Discord.ThreadBindings.ThreadBindingsTTL()
				return bot.threads.bind(guildID, threadID, role, ttl)
			}
		}
	}

	return discord.NewForumBoard(client, cfg, deps)
}


// --- P14.2: Thread-Bound Sessions ---


// --- Thread Parent Cache ---

// threadParentCache caches the mapping from thread channel IDs to parent channel IDs.
// Discord threads have their own channel IDs that don't appear in config allowlists.
// This cache avoids repeated API calls to resolve thread→parent relationships.
// Bounded to threadParentCacheMaxSize entries with LRU-style eviction.
type threadParentCache struct {
	mu    sync.RWMutex
	items map[string]threadParentEntry
}

type threadParentEntry struct {
	ParentID  string    // empty string = negative cache (thread has no parent / API failed)
	ExpiresAt time.Time
}

const (
	threadParentCacheTTL     = 24 * time.Hour   // thread→parent is immutable; long TTL, bounded by max size
	threadParentNegativeTTL  = 5 * time.Minute  // shorter TTL for failed lookups (transient errors)
	threadParentCacheMaxSize = 1000
)

func newThreadParentCache() *threadParentCache {
	return &threadParentCache{
		items: make(map[string]threadParentEntry),
	}
}

// get returns the cached parent channel ID for a thread.
// Returns ("", false) if not cached or expired.
// Returns ("", true) if negative-cached (known non-thread or API failure).
// Returns (parentID, true) on cache hit.
func (c *threadParentCache) get(threadID string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.items[threadID]
	if !ok || time.Now().After(entry.ExpiresAt) {
		return "", false
	}
	return entry.ParentID, true
}

// set caches a thread→parent mapping with TTL.
// parentID == "" caches a negative result (shorter TTL).
func (c *threadParentCache) set(threadID, parentID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Evict expired entries if at capacity.
	if len(c.items) >= threadParentCacheMaxSize {
		c.evictExpiredLocked()
	}
	// If still at capacity after eviction, drop oldest entry.
	if len(c.items) >= threadParentCacheMaxSize {
		c.evictOldestLocked()
	}
	ttl := threadParentCacheTTL
	if parentID == "" {
		ttl = threadParentNegativeTTL
	}
	c.items[threadID] = threadParentEntry{
		ParentID:  parentID,
		ExpiresAt: time.Now().Add(ttl),
	}
}

// cleanup removes all expired entries.
func (c *threadParentCache) cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.evictExpiredLocked()
}

// evictExpiredLocked removes expired entries. Caller must hold write lock.
func (c *threadParentCache) evictExpiredLocked() {
	now := time.Now()
	for k, v := range c.items {
		if now.After(v.ExpiresAt) {
			delete(c.items, k)
		}
	}
}

// evictOldestLocked removes the entry with the earliest expiration. Caller must hold write lock.
func (c *threadParentCache) evictOldestLocked() {
	var oldestKey string
	var oldestTime time.Time
	for k, v := range c.items {
		if oldestKey == "" || v.ExpiresAt.Before(oldestTime) {
			oldestKey = k
			oldestTime = v.ExpiresAt
		}
	}
	if oldestKey != "" {
		delete(c.items, oldestKey)
	}
}


// --- Discord Channel Types ---

const (
	discordChannelTypePublicThread  = 11
	discordChannelTypePrivateThread = 12
	discordChannelTypeForum         = 15
)

// --- Thread Binding ---

// threadBinding represents a Discord thread bound to a specific agent session.
type threadBinding struct {
	Agent      string
	GuildID   string
	ThreadID  string
	SessionID string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// expired returns true if the binding has passed its expiration time.
func (b *threadBinding) expired() bool {
	return time.Now().After(b.ExpiresAt)
}

// --- Thread Binding Store ---

// threadBindingStore manages thread-to-agent bindings with TTL expiration.
type threadBindingStore struct {
	mu       sync.RWMutex
	bindings map[string]*threadBinding // key: "guildId:threadId"
}

// newThreadBindingStore creates a new empty thread binding store.
func newThreadBindingStore() *threadBindingStore {
	return &threadBindingStore{
		bindings: make(map[string]*threadBinding),
	}
}

// threadBindingKey generates the map key for a guild/thread pair.
func threadBindingKey(guildID, threadID string) string {
	return guildID + ":" + threadID
}

// bind creates or updates a thread binding. Returns the generated session ID.
func (s *threadBindingStore) bind(guildID, threadID, agent string, ttl time.Duration) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := threadBindingKey(guildID, threadID)
	now := time.Now()
	sessionID := threadSessionKey(agent, guildID, threadID)

	s.bindings[key] = &threadBinding{
		Agent:     agent,
		GuildID:   guildID,
		ThreadID:  threadID,
		SessionID: sessionID,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
	return sessionID
}

// unbind removes a thread binding.
func (s *threadBindingStore) unbind(guildID, threadID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.bindings, threadBindingKey(guildID, threadID))
}

// get retrieves a thread binding, returning nil if not found or expired.
func (s *threadBindingStore) get(guildID, threadID string) *threadBinding {
	s.mu.RLock()
	defer s.mu.RUnlock()

	b, ok := s.bindings[threadBindingKey(guildID, threadID)]
	if !ok {
		return nil
	}
	if b.expired() {
		return nil
	}
	return b
}

// cleanup removes all expired bindings.
func (s *threadBindingStore) cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for key, b := range s.bindings {
		if b.expired() {
			delete(s.bindings, key)
		}
	}
}

// count returns the number of active (non-expired) bindings. Used for status/testing.
func (s *threadBindingStore) count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	n := 0
	for _, b := range s.bindings {
		if !b.expired() {
			n++
		}
	}
	return n
}

// --- Session Key ---

// threadSessionKey generates a deterministic session key for a thread binding.
// Format: agent:{agent}:discord:thread:{guildId}:{threadId}
func threadSessionKey(agent, guildID, threadID string) string {
	return fmt.Sprintf("agent:%s:discord:thread:%s:%s", agent, guildID, threadID)
}

// --- Cleanup Goroutine ---

// startThreadCleanup runs periodic cleanup of expired thread bindings and parent cache entries.
func startThreadCleanup(ctx context.Context, store *threadBindingStore, parentCache *threadParentCache) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			store.cleanup()
			if parentCache != nil {
				parentCache.cleanup()
			}
			log.Debug("discord thread cleanup complete", "bindings", store.count())
		}
	}
}

// --- Channel Type Detection ---

// discordMessageWithType extends discord.Message with channel type info
// used for thread detection during MESSAGE_CREATE dispatch.
type discordMessageWithType struct {
	discord.Message
	ChannelType int `json:"channel_type,omitempty"`
}

// isThreadChannel returns true if the channel type represents a thread or forum.
func isThreadChannel(channelType int) bool {
	return channelType == discordChannelTypePublicThread ||
		channelType == discordChannelTypePrivateThread ||
		channelType == discordChannelTypeForum
}

// --- /focus and /unfocus Command Handlers ---

// availableRoleNames returns sorted agent names from config.
func (db *DiscordBot) availableRoleNames() []string {
	if db == nil || db.cfg == nil || db.cfg.Agents == nil {
		return nil
	}
	names := make([]string, 0, len(db.cfg.Agents))
	for r := range db.cfg.Agents {
		names = append(names, r)
	}
	sort.Strings(names)
	return names
}

// handleFocusCommand processes the /focus <agent> command to bind a thread to an agent.
func (db *DiscordBot) handleFocusCommand(msg discord.Message, args string, channelType int) bool {
	if !isThreadChannel(channelType) {
		db.sendMessage(msg.ChannelID, "The `/focus` command can only be used inside a thread.")
		return true
	}

	role := strings.TrimSpace(strings.ToLower(args))
	if role == "" {
		available := db.availableRoleNames()
		db.sendMessage(msg.ChannelID, fmt.Sprintf("Usage: `/focus <agent>` — Available agents: %s", strings.Join(available, ", ")))
		return true
	}

	// Validate agent exists in config.
	_, roleExists := db.cfg.Agents[role]
	if db.cfg.Agents == nil || !roleExists {
		available := db.availableRoleNames()
		db.sendMessage(msg.ChannelID, fmt.Sprintf("Unknown agent `%s`. Available: %s", role, strings.Join(available, ", ")))
		return true
	}

	guildID := msg.GuildID
	threadID := msg.ChannelID // in a thread, channel_id IS the thread ID
	ttl := db.cfg.Discord.ThreadBindings.ThreadBindingsTTL()

	sessionID := db.threads.bind(guildID, threadID, role, ttl)
	log.Info("discord thread bound", "guild", guildID, "thread", threadID, "agent", role, "session", sessionID)

	db.sendEmbed(msg.ChannelID, discord.Embed{
		Title:       fmt.Sprintf("Thread focused on %s", role),
		Description: fmt.Sprintf("This thread is now bound to agent **%s**.\nSession: `%s`\nExpires in %d hours.", role, sessionID, int(ttl.Hours())),
		Color:       0x57F287,
		Timestamp:   time.Now().Format(time.RFC3339),
	})
	return true
}

// handleUnfocusCommand processes the /unfocus command to unbind a thread.
func (db *DiscordBot) handleUnfocusCommand(msg discord.Message, channelType int) bool {
	if !isThreadChannel(channelType) {
		db.sendMessage(msg.ChannelID, "The `/unfocus` command can only be used inside a thread.")
		return true
	}

	guildID := msg.GuildID
	threadID := msg.ChannelID

	existing := db.threads.get(guildID, threadID)
	if existing == nil {
		db.sendMessage(msg.ChannelID, "This thread is not currently focused on any agent.")
		return true
	}

	db.threads.unbind(guildID, threadID)
	log.Info("discord thread unbound", "guild", guildID, "thread", threadID, "wasRole", existing.Agent)

	db.sendEmbed(msg.ChannelID, discord.Embed{
		Title:       "Thread unfocused",
		Description: fmt.Sprintf("Agent **%s** has been unbound from this thread.", existing.Agent),
		Color:       0xFEE75C,
		Timestamp:   time.Now().Format(time.RFC3339),
	})
	return true
}

// --- Thread-Aware Message Routing ---

// handleThreadMessage checks if a message is in a bound thread and routes accordingly.
// Returns true if the message was handled (bound thread routing), false for normal routing.
func (db *DiscordBot) handleThreadMessage(msg discord.Message, channelType int) bool {
	if db.threads == nil || !db.cfg.Discord.ThreadBindings.Enabled {
		return false
	}

	// channelType may be 0 when Discord omits it from the payload.
	// If it's explicitly a non-thread type (1-10), skip. If 0 or thread type, check binding.
	if channelType > 0 && !isThreadChannel(channelType) {
		return false
	}

	// For channelType == 0 (unknown), check if we have a binding as a fallback signal.
	// This handles cases where Discord doesn't include channel_type in MESSAGE_CREATE.
	binding := db.threads.get(msg.GuildID, msg.ChannelID)
	isThread := isThreadChannel(channelType)

	// Check for /focus and /unfocus commands (only in confirmed threads).
	text := discord.StripMention(msg.Content, db.botUserID)
	text = strings.TrimSpace(text)

	if isThread {
		if strings.HasPrefix(text, "/focus") {
			args := strings.TrimPrefix(text, "/focus")
			return db.handleFocusCommand(msg, args, channelType)
		}
		if text == "/unfocus" {
			return db.handleUnfocusCommand(msg, channelType)
		}
	}

	if binding == nil {
		// Auto-bind unbound threads to the default agent (parent route → system default).
		// This ensures threads created from bot messages inherit session context without
		// requiring an explicit /focus command.
		if !isThread {
			return false // channelType unknown and no binding, let normal routing handle
		}
		agent := db.resolveThreadDefaultAgent(msg.ChannelID, msg.GuildID)
		if agent == "" {
			return false // no default agent configured, fall through
		}
		ttl := db.cfg.Discord.ThreadBindings.ThreadBindingsTTL()
		sessionID := db.threads.bind(msg.GuildID, msg.ChannelID, agent, ttl)
		log.Info("discord thread auto-bound", "thread", msg.ChannelID, "agent", agent, "session", sessionID)
		binding = db.threads.get(msg.GuildID, msg.ChannelID)
		if binding == nil {
			return false
		}
	}

	// Thread is bound — route to the bound agent.
	db.handleThreadRoute(msg, text, binding)
	return true
}

// resolveThreadDefaultAgent returns the agent to use for auto-binding an unbound thread.
// Priority: parent channel route → system-wide default agent.
func (db *DiscordBot) resolveThreadDefaultAgent(threadID, guildID string) string {
	if guildID != "" {
		if parentID := db.resolveThreadParent(threadID); parentID != "" {
			if route, ok := db.cfg.Discord.Routes[parentID]; ok && route.Agent != "" {
				return route.Agent
			}
		}
	}
	return db.cfg.DefaultAgent
}

// handleThreadRoute dispatches a message in a bound thread to the bound agent.
func (db *DiscordBot) handleThreadRoute(msg discord.Message, prompt string, binding *threadBinding) {
	if prompt == "" {
		return
	}

	db.sendTyping(msg.ChannelID)

	ctx := trace.WithID(context.Background(), trace.NewID("discord-thread"))
	dbPath := db.cfg.HistoryDB
	role := binding.Agent
	sessionID := binding.SessionID

	log.InfoCtx(ctx, "discord thread dispatch",
		"thread", msg.ChannelID, "agent", role, "session", sessionID, "prompt", truncate(prompt, 60))

	// Get or create session using the thread binding's session ID as channel key.
	sess, err := getOrCreateChannelSession(dbPath, "discord", sessionID, role, "")
	if err != nil {
		log.ErrorCtx(ctx, "discord thread session error", "error", err)
	}

	// Context-aware prompt.
	// Skip text injection for providers with native session support (e.g. claude-code).
	contextPrompt := prompt
	if sess != nil {
		providerName := resolveProviderName(db.cfg, Task{Agent: role}, role)
		if !providerHasNativeSession(providerName) {
			sessionCtx := buildSessionContext(dbPath, sess.ID, db.cfg.Session.ContextMessagesOrDefault())
			// New session with no history — carry forward context from the archived predecessor.
			if sessionCtx == "" && sess.MessageCount == 0 {
				if prev, err := findLastArchivedChannelSession(dbPath, sessionID); err == nil && prev != nil {
					sessionCtx = buildSessionContext(dbPath, prev.ID, db.cfg.Session.ContextMessagesOrDefault())
					log.InfoCtx(ctx, "auto-continuing from archived session",
						"prevSession", prev.ID[:8], "channel", sessionID)
				}
			}
			contextPrompt = wrapWithContext(sessionCtx, prompt)
		}
		now := time.Now().Format(time.RFC3339)
		addSessionMessage(dbPath, SessionMessage{
			SessionID: sess.ID, Role: "user", Content: truncateStr(prompt, 5000), CreatedAt: now,
		})
		updateSessionStats(dbPath, sess.ID, 0, 0, 0, 1)
		title := fmt.Sprintf("[thread:%s] %s", role, prompt)
		if len(title) > 100 {
			title = title[:100]
		}
		updateSessionTitle(dbPath, sess.ID, title)
	}

	// Build and run task.
	task := Task{Prompt: contextPrompt, Agent: role, Source: "route:discord:thread"}
	fillDefaults(db.cfg, &task)
	if sess != nil {
		task.SessionID = sess.ID
	}
	if role != "" {
		if soulPrompt, err := loadAgentPrompt(db.cfg, role); err == nil && soulPrompt != "" {
			task.SystemPrompt = soulPrompt
		}
		if rc, ok := db.cfg.Agents[role]; ok {
			if rc.Model != "" {
				task.Model = rc.Model
			}
			if rc.PermissionMode != "" {
				task.PermissionMode = rc.PermissionMode
			}
		}
	}
	task.Prompt = expandPrompt(task.Prompt, "", db.cfg.HistoryDB, role, db.cfg.KnowledgeDir, db.cfg)

	taskStart := time.Now()
	result := runSingleTask(ctx, db.cfg, task, db.sem, db.childSem, role)

	recordHistory(db.cfg.HistoryDB, task.ID, task.Name, task.Source, role, task, result,
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
		maybeCompactSession(db.cfg, dbPath, sess.ID, sess.MessageCount+2, sess.TotalTokensIn+result.TokensIn, db.sem, db.childSem)
	}

	if result.Status == "success" {
		setMemory(db.cfg, role, "last_thread_output", truncate(result.Output, 500))
		setMemory(db.cfg, role, "last_thread_prompt", truncate(prompt, 200))
		setMemory(db.cfg, role, "last_thread_time", time.Now().Format(time.RFC3339))
	}

	audit.Log(dbPath, "thread.dispatch", "discord",
		fmt.Sprintf("agent=%s thread=%s session=%s", role, msg.ChannelID, task.SessionID), "")

	// Send response embed.
	route := &RouteResult{Agent: role, Method: "thread-binding"}
	db.sendRouteResponse(msg.ChannelID, route, result, task, false, msg.ID)
}

// runDiscordProgressUpdater subscribes to task SSE events and updates a Discord progress message.
func (db *DiscordBot) runDiscordProgressUpdater(
	channelID, progressMsgID, taskID, sessionID string,
	broker *sseBroker,
	stopCh <-chan struct{},
	builder *discord.ProgressBuilder,
	components []discord.Component,
) {
	eventCh, unsub := broker.Subscribe(taskID)
	defer unsub()

	log.Debug("discord progress updater started", "taskID", taskID, "sessionID", sessionID)

	var sessionEventCh chan SSEEvent
	if sessionID != "" && sessionID != taskID {
		ch, u := broker.Subscribe(sessionID)
		sessionEventCh = ch
		defer u()
		log.Debug("discord progress updater subscribed to session", "sessionID", sessionID)
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var lastEdit time.Time

	tryEdit := func() {
		if builder.IsDirty() && time.Since(lastEdit) >= 1500*time.Millisecond {
			content := builder.Render()
			log.Debug("discord progress edit", "contentLen", len(content), "taskID", taskID)
			if err := db.editMessageWithComponents(channelID, progressMsgID, content, components); err != nil {
				log.Warn("discord progress edit failed", "error", err)
			}
			db.sendTyping(channelID)
			lastEdit = time.Now()
		}
	}

	handleEvent := func(ev SSEEvent) (done bool) {
		switch ev.Type {
		case SSEToolCall:
			if data, ok := ev.Data.(map[string]any); ok {
				if name, _ := data["name"].(string); name != "" {
					builder.AddToolCall(name)
					tryEdit()
				}
			}
		case SSEOutputChunk:
			if data, ok := ev.Data.(map[string]any); ok {
				if chunk, _ := data["chunk"].(string); chunk != "" {
					log.Debug("discord progress got chunk", "len", len(chunk), "taskID", taskID)
					if replace, _ := data["replace"].(bool); replace {
						builder.ReplaceText(chunk)
					} else {
						builder.AddText(chunk)
					}
					tryEdit()
				}
			}
		case SSECompleted, SSEError:
			log.Debug("discord progress completed/error event", "type", ev.Type, "taskID", taskID)
			return true
		}
		return false
	}

	for {
		select {
		case <-stopCh:
			return
		case ev, ok := <-eventCh:
			if !ok {
				return
			}
			if handleEvent(ev) {
				return
			}
		case ev, ok := <-sessionEventCh:
			if !ok {
				sessionEventCh = nil
			} else {
				handleEvent(ev)
			}
		case <-ticker.C:
			tryEdit()
		}
	}
}


// --- Discord Terminal Bridge ---
// Bridges interactive CLI tool sessions (via tmux) to Discord,
// allowing remote control from a phone via buttons and text input.
// Coexists with the headless CLI dispatch mode — Terminal is for interactive,
// CLI is for automated dispatch. Both can run simultaneously.

// terminalSession represents a single interactive tmux session.
type terminalSession struct {
	ID           string
	TmuxName     string
	ChannelID    string
	OwnerID      string
	Tool         string // "claude" or "codex"
	CreatedAt    time.Time
	LastActivity time.Time

	displayMsgID string // Discord message showing terminal screen
	controlMsgID string // Discord message with control buttons

	mu         sync.Mutex
	lastScreen string
	stopCh     chan struct{}
	captureCh  chan struct{} // signal immediate re-capture after input
}

// terminalBridge manages all terminal sessions for a Discord bot.
type terminalBridge struct {
	bot *DiscordBot
	cfg DiscordTerminalConfig

	mu       sync.RWMutex
	sessions map[string]*terminalSession // channelID → session
}

// newTerminalBridge creates a new terminal bridge.
func newTerminalBridge(bot *DiscordBot, cfg DiscordTerminalConfig) *terminalBridge {
	if cfg.MaxSessions <= 0 {
		cfg.MaxSessions = 3
	}
	if cfg.CaptureRows <= 0 {
		cfg.CaptureRows = 40
	}
	if cfg.CaptureCols <= 0 {
		cfg.CaptureCols = 120
	}
	if cfg.IdleTimeout == "" {
		cfg.IdleTimeout = "30m"
	}
	if cfg.DefaultTool == "" {
		cfg.DefaultTool = "claude"
	}
	return &terminalBridge{
		bot:      bot,
		cfg:      cfg,
		sessions: make(map[string]*terminalSession),
	}
}

// --- Session Lifecycle ---

// startSession creates a new terminal session in the given channel.
func (tb *terminalBridge) startSession(channelID, userID, workdir, tool string) error {
	tb.mu.Lock()
	if _, exists := tb.sessions[channelID]; exists {
		tb.mu.Unlock()
		return fmt.Errorf("session already active in this channel")
	}
	if len(tb.sessions) >= tb.cfg.MaxSessions {
		tb.mu.Unlock()
		return fmt.Errorf("max sessions reached (%d)", tb.cfg.MaxSessions)
	}
	tb.mu.Unlock()

	// Resolve tool and binary path.
	if tool == "" {
		tool = tb.cfg.DefaultTool
	}
	binaryPath := tb.resolveBinaryPath(tool)
	profile := tb.resolveProfile(tool)

	// Resolve workdir.
	if workdir == "" {
		workdir = tb.cfg.Workdir
	}

	// Build the command.
	tmuxReq := tmux.ProfileRequest{
		Model:          "sonnet",
		PermissionMode: "acceptEdits",
	}
	command := profile.BuildCommand(binaryPath, tmuxReq)

	// Generate session ID.
	sessionID := fmt.Sprintf("%d", time.Now().UnixNano()%1000000)
	tmuxName := "tetora-term-" + sessionID

	// Create tmux session.
	if err := tmux.Create(tmuxName, tb.cfg.CaptureCols, tb.cfg.CaptureRows, command, workdir); err != nil {
		return fmt.Errorf("create tmux session: %w", err)
	}

	session := &terminalSession{
		ID:           sessionID,
		TmuxName:     tmuxName,
		ChannelID:    channelID,
		OwnerID:      userID,
		Tool:         tool,
		CreatedAt:    time.Now(),
		LastActivity: time.Now(),
		stopCh:       make(chan struct{}),
		captureCh:    make(chan struct{}, 1),
	}

	// Send display message.
	toolLabel := "Claude Code"
	if tool == "codex" {
		toolLabel = "Codex"
	}
	displayContent := fmt.Sprintf("```\nStarting %s session...\n```", toolLabel)
	displayMsgID, err := tb.bot.sendMessageReturningID(channelID, displayContent)
	if err != nil {
		tmux.Kill(tmuxName)
		return fmt.Errorf("send display message: %w", err)
	}
	session.displayMsgID = displayMsgID

	// Send control panel.
	allowedIDs := tb.cfg.AllowedUsers
	if len(allowedIDs) == 0 {
		allowedIDs = []string{userID}
	}
	controlMsgID, err := tb.sendControlPanel(channelID, "Terminal Controls:", sessionID, allowedIDs)
	if err != nil {
		tmux.Kill(tmuxName)
		return fmt.Errorf("send control panel: %w", err)
	}
	session.controlMsgID = controlMsgID

	// Register session.
	tb.mu.Lock()
	tb.sessions[channelID] = session
	tb.mu.Unlock()

	// Start capture loop.
	go tb.runCaptureLoop(session)

	log.Info("terminal session started",
		"session", sessionID, "channel", channelID, "user", userID,
		"tool", tool, "tmux", tmuxName)
	return nil
}

// stopSession stops the terminal session in a channel.
func (tb *terminalBridge) stopSession(channelID string) error {
	tb.mu.Lock()
	session, exists := tb.sessions[channelID]
	if !exists {
		tb.mu.Unlock()
		return fmt.Errorf("no active session in this channel")
	}
	delete(tb.sessions, channelID)
	tb.mu.Unlock()

	close(session.stopCh)

	if tmux.HasSession(session.TmuxName) {
		tmux.Kill(session.TmuxName)
	}

	tb.unregisterControlButtons(session.ID)

	tb.bot.editMessage(session.ChannelID, session.displayMsgID,
		"```\n[Session ended]\n```")
	tb.bot.deleteMessage(session.ChannelID, session.controlMsgID)

	log.Info("terminal session stopped", "session", session.ID, "channel", channelID)
	return nil
}

// stopAllSessions stops all active terminal sessions.
func (tb *terminalBridge) stopAllSessions() {
	tb.mu.RLock()
	channels := make([]string, 0, len(tb.sessions))
	for ch := range tb.sessions {
		channels = append(channels, ch)
	}
	tb.mu.RUnlock()

	for _, ch := range channels {
		tb.stopSession(ch)
	}
}

// getSession returns the terminal session for a channel, or nil.
func (tb *terminalBridge) getSession(channelID string) *terminalSession {
	tb.mu.RLock()
	defer tb.mu.RUnlock()
	return tb.sessions[channelID]
}

// --- Screen Rendering ---

// renderTerminalScreen cleans and truncates terminal output for Discord code blocks.
func renderTerminalScreen(raw string, maxChars int) string {
	cleaned := ansiEscapeRe.ReplaceAllString(raw, "")
	cleaned = strings.ReplaceAll(cleaned, "```", "` ` `")

	// Trim trailing empty lines.
	lines := strings.Split(cleaned, "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}

	result := strings.Join(lines, "\n")
	if len(result) <= maxChars {
		return result
	}

	// Truncate from top, keeping the bottom visible.
	truncated := make([]string, 0)
	totalLen := 0
	for i := len(lines) - 1; i >= 0; i-- {
		lineLen := len(lines[i]) + 1
		if totalLen+lineLen > maxChars-30 {
			break
		}
		truncated = append([]string{lines[i]}, truncated...)
		totalLen += lineLen
	}

	skipped := len(lines) - len(truncated)
	header := fmt.Sprintf("... (%d lines above) ...\n", skipped)
	return header + strings.Join(truncated, "\n")
}

// --- Capture Loop ---

func (tb *terminalBridge) runCaptureLoop(session *terminalSession) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	idleTimeout, err := time.ParseDuration(tb.cfg.IdleTimeout)
	if err != nil {
		idleTimeout = 30 * time.Minute
	}

	minInterval := 1500 * time.Millisecond
	lastEdit := time.Time{}

	for {
		select {
		case <-session.stopCh:
			return
		case <-ticker.C:
		case <-session.captureCh:
			time.Sleep(500 * time.Millisecond)
		}

		if !tmux.HasSession(session.TmuxName) {
			log.Info("terminal tmux session gone, stopping", "session", session.ID)
			tb.stopSession(session.ChannelID)
			return
		}

		// Check idle timeout.
		session.mu.Lock()
		lastActivity := session.LastActivity
		session.mu.Unlock()
		if time.Since(lastActivity) > idleTimeout {
			log.Info("terminal session idle timeout", "session", session.ID)
			tb.bot.sendMessage(session.ChannelID, "Terminal session timed out due to inactivity.")
			tb.stopSession(session.ChannelID)
			return
		}

		raw, err := tmux.Capture(session.TmuxName)
		if err != nil {
			continue
		}

		screen := renderTerminalScreen(raw, 1988) // 2000 - "```\n" - "\n```"
		session.mu.Lock()
		changed := screen != session.lastScreen
		if changed {
			session.lastScreen = screen
		}
		session.mu.Unlock()

		if !changed {
			continue
		}

		if time.Since(lastEdit) < minInterval {
			remaining := minInterval - time.Since(lastEdit)
			time.Sleep(remaining)
		}

		content := "```\n" + screen + "\n```"
		if err := tb.bot.editMessage(session.ChannelID, session.displayMsgID, content); err != nil {
			log.Warn("terminal display update failed", "session", session.ID, "error", err)
		}
		lastEdit = time.Now()
	}
}

// --- Discord UI ---

func (tb *terminalBridge) sendControlPanel(channelID, content, sessionID string, allowedIDs []string) (string, error) {
	prefix := "term_" + sessionID + "_"

	row1 := discordActionRow(
		discordButton(prefix+"up", "\u2b06 Up", discord.ButtonStyleSecondary),
		discordButton(prefix+"down", "\u2b07 Down", discord.ButtonStyleSecondary),
		discordButton(prefix+"enter", "\u23ce Enter", discord.ButtonStylePrimary),
		discordButton(prefix+"tab", "Tab", discord.ButtonStyleSecondary),
		discordButton(prefix+"esc", "Esc", discord.ButtonStyleSecondary),
	)
	row2 := discordActionRow(
		discordButton(prefix+"type", "\u2328 Type", discord.ButtonStylePrimary),
		discordButton(prefix+"y", "Y", discord.ButtonStyleSuccess),
		discordButton(prefix+"n", "N", discord.ButtonStyleDanger),
		discordButton(prefix+"ctrlc", "Ctrl+C", discord.ButtonStyleDanger),
		discordButton(prefix+"stop", "Stop", discord.ButtonStyleDanger),
	)

	components := []discord.Component{row1, row2}

	body, err := tb.bot.discordRequestWithResponse("POST",
		fmt.Sprintf("/channels/%s/messages", channelID),
		map[string]any{
			"content":    content,
			"components": components,
		})
	if err != nil {
		return "", err
	}
	var msg struct {
		ID string `json:"id"`
	}
	if err := jsonUnmarshalBytes(body, &msg); err != nil {
		return "", err
	}

	tb.registerControlButtons(sessionID, allowedIDs)
	return msg.ID, nil
}

func (tb *terminalBridge) registerControlButtons(sessionID string, allowedIDs []string) {
	prefix := "term_" + sessionID + "_"

	keyMap := map[string][]string{
		"up":    {"Up"},
		"down":  {"Down"},
		"enter": {"Enter"},
		"tab":   {"Tab"},
		"esc":   {"Escape"},
		"y":     {"y"},
		"n":     {"n"},
		"ctrlc": {"C-c"},
	}

	for action, keys := range keyMap {
		keys := keys
		customID := prefix + action
		tb.bot.interactions.register(&pendingInteraction{
			CustomID:   customID,
			CreatedAt:  time.Now(),
			AllowedIDs: allowedIDs,
			Reusable:   true,
			Callback: func(data discord.InteractionData) {
				session := tb.getSessionByID(sessionID)
				if session == nil {
					return
				}
				session.mu.Lock()
				session.LastActivity = time.Now()
				session.mu.Unlock()

				tmux.SendKeys(session.TmuxName, keys...)
				tb.signalCapture(session)
			},
		})
	}

	// "Type" button → modal.
	typeCustomID := prefix + "type"
	modalCustomID := "term_modal_" + sessionID
	tb.bot.interactions.register(&pendingInteraction{
		CustomID:   typeCustomID,
		CreatedAt:  time.Now(),
		AllowedIDs: allowedIDs,
		Reusable:   true,
		ModalResponse: func() *discord.InteractionResponse {
			resp := discordBuildModal(modalCustomID, "Terminal Input",
				discordParagraphInput("term_input", "Text to send", true),
			)
			return &resp
		}(),
	})

	// Modal submit handler.
	tb.bot.interactions.register(&pendingInteraction{
		CustomID:   modalCustomID,
		CreatedAt:  time.Now(),
		AllowedIDs: allowedIDs,
		Reusable:   true,
		Callback: func(data discord.InteractionData) {
			session := tb.getSessionByID(sessionID)
			if session == nil {
				return
			}
			values := extractModalValues(data.Components)
			text := values["term_input"]
			if text == "" {
				return
			}
			session.mu.Lock()
			session.LastActivity = time.Now()
			session.mu.Unlock()

			tmux.SendText(session.TmuxName, text)
			tmux.SendKeys(session.TmuxName, "Enter")
			tb.signalCapture(session)
		},
	})

	// "Stop" button.
	tb.bot.interactions.register(&pendingInteraction{
		CustomID:   prefix + "stop",
		CreatedAt:  time.Now(),
		AllowedIDs: allowedIDs,
		Reusable:   false,
		Callback: func(data discord.InteractionData) {
			session := tb.getSessionByID(sessionID)
			if session == nil {
				return
			}
			tb.stopSession(session.ChannelID)
			tb.bot.sendMessage(session.ChannelID, "Terminal session stopped.")
		},
	})
}

func (tb *terminalBridge) unregisterControlButtons(sessionID string) {
	prefix := "term_" + sessionID + "_"
	for _, action := range []string{"up", "down", "enter", "tab", "esc", "y", "n", "ctrlc", "type", "stop"} {
		tb.bot.interactions.remove(prefix + action)
	}
	tb.bot.interactions.remove("term_modal_" + sessionID)
}

func (tb *terminalBridge) getSessionByID(sessionID string) *terminalSession {
	tb.mu.RLock()
	defer tb.mu.RUnlock()
	for _, s := range tb.sessions {
		if s.ID == sessionID {
			return s
		}
	}
	return nil
}

func (tb *terminalBridge) signalCapture(session *terminalSession) {
	select {
	case session.captureCh <- struct{}{}:
	default:
	}
}

// --- /term Command Handling ---

// handleTermCommand processes !term start|stop|status commands.
func (tb *terminalBridge) handleTermCommand(msg discord.Message, args string) {
	parts := strings.Fields(strings.TrimSpace(args))
	cmd := "start"
	if len(parts) > 0 {
		cmd = strings.ToLower(parts[0])
	}

	switch cmd {
	case "start":
		if !tb.isAllowedUser(msg.Author.ID) {
			tb.bot.sendMessage(msg.ChannelID, "You are not allowed to use terminal bridge.")
			return
		}
		// Parse optional flags: !term start [claude|codex] [workdir]
		tool := ""
		workdir := ""
		for _, part := range parts[1:] {
			lower := strings.ToLower(part)
			if lower == "claude" || lower == "codex" {
				tool = lower
			} else {
				workdir = part
			}
		}
		if err := tb.startSession(msg.ChannelID, msg.Author.ID, workdir, tool); err != nil {
			tb.bot.sendMessage(msg.ChannelID, fmt.Sprintf("Failed to start terminal: %s", err))
		}

	case "stop":
		if err := tb.stopSession(msg.ChannelID); err != nil {
			tb.bot.sendMessage(msg.ChannelID, fmt.Sprintf("Failed to stop terminal: %s", err))
		} else {
			tb.bot.sendMessage(msg.ChannelID, "Terminal session stopped.")
		}

	case "status":
		tb.mu.RLock()
		count := len(tb.sessions)
		lines := make([]string, 0, count)
		for ch, s := range tb.sessions {
			age := time.Since(s.CreatedAt).Round(time.Second)
			idle := time.Since(s.LastActivity).Round(time.Second)
			lines = append(lines, fmt.Sprintf("• <#%s> — `%s` %s (up %s, idle %s)",
				ch, s.ID, s.Tool, age, idle))
		}
		tb.mu.RUnlock()
		if count == 0 {
			tb.bot.sendMessage(msg.ChannelID, "No active terminal sessions.")
		} else {
			tb.bot.sendMessage(msg.ChannelID, fmt.Sprintf("**Active sessions (%d/%d):**\n%s",
				count, tb.cfg.MaxSessions, strings.Join(lines, "\n")))
		}

	default:
		tb.bot.sendMessage(msg.ChannelID,
			"Usage: `!term start [claude|codex] [workdir]` | `!term stop` | `!term status`")
	}
}

// handleTerminalInput checks if a message should be routed to the terminal session.
func (tb *terminalBridge) handleTerminalInput(channelID, text string) bool {
	session := tb.getSession(channelID)
	if session == nil {
		return false
	}
	if strings.HasPrefix(text, "/") || strings.HasPrefix(text, "!") {
		return false
	}

	session.mu.Lock()
	session.LastActivity = time.Now()
	session.mu.Unlock()

	tmux.SendText(session.TmuxName, text)
	tmux.SendKeys(session.TmuxName, "Enter")
	tb.signalCapture(session)
	return true
}

// isAllowedUser checks if a user is allowed to use the terminal bridge.
func (tb *terminalBridge) isAllowedUser(userID string) bool {
	if len(tb.cfg.AllowedUsers) == 0 {
		return true
	}
	return sliceContainsStr(tb.cfg.AllowedUsers, userID)
}

// --- Helpers ---

func (tb *terminalBridge) resolveBinaryPath(tool string) string {
	switch tool {
	case "codex":
		if tb.cfg.CodexPath != "" {
			return tb.cfg.CodexPath
		}
		return "codex"
	default:
		if tb.cfg.ClaudePath != "" {
			return tb.cfg.ClaudePath
		}
		if tb.bot.cfg.ClaudePath != "" {
			return tb.bot.cfg.ClaudePath
		}
		return "claude"
	}
}

func (tb *terminalBridge) resolveProfile(tool string) tmux.CLIProfile {
	switch tool {
	case "codex":
		return tmux.NewCodexProfile()
	default:
		return tmux.NewClaudeProfile()
	}
}

// jsonUnmarshalBytes is a small helper to unmarshal JSON from bytes.
func jsonUnmarshalBytes(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

// --- P14.1: Discord Components v2 ---


// --- Interaction State ---

// discordInteractionState tracks pending interactions for follow-up.
type discordInteractionState struct {
	mu           sync.Mutex
	pending      map[string]*pendingInteraction
	cleanupEvery time.Duration
}

type pendingInteraction struct {
	CustomID      string
	ChannelID     string
	UserID        string
	CreatedAt     time.Time
	Callback      func(data discord.InteractionData)
	AllowedIDs    []string                    // restrict to specific user IDs (empty = allow all)
	Reusable      bool                        // if true, don't remove after first use
	ModalResponse *discord.InteractionResponse // if set, respond with this modal instead of deferred update
	Response      *discord.InteractionResponse // if set, use this instead of deferred update (e.g. type 7 message update)
}

func newDiscordInteractionState() *discordInteractionState {
	s := &discordInteractionState{
		pending:      make(map[string]*pendingInteraction),
		cleanupEvery: 30 * time.Minute,
	}
	go s.cleanupLoop()
	return s
}

func (s *discordInteractionState) register(pi *pendingInteraction) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending[pi.CustomID] = pi
}

func (s *discordInteractionState) lookup(customID string) *pendingInteraction {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pending[customID]
}

func (s *discordInteractionState) remove(customID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pending, customID)
}

func (s *discordInteractionState) cleanupLoop() {
	ticker := time.NewTicker(s.cleanupEvery)
	defer ticker.Stop()
	for range ticker.C {
		s.mu.Lock()
		cutoff := time.Now().Add(-1 * time.Hour)
		for k, v := range s.pending {
			if v.CreatedAt.Before(cutoff) {
				delete(s.pending, k)
			}
		}
		s.mu.Unlock()
	}
}

// --- Component Builder Aliases (canonical implementations in internal/discord) ---

var (
	discordActionRow      = discord.ActionRow
	discordButton         = discord.Button
	discordLinkButton     = discord.LinkButton
	discordSelectMenu     = discord.SelectMenu
	discordMultiSelectMenu = discord.MultiSelectMenu
	discordUserSelect     = discord.UserSelect
	discordRoleSelect     = discord.RoleSelect
	discordChannelSelect  = discord.ChannelSelect
	discordTextInput      = discord.TextInput
	discordParagraphInput = discord.ParagraphInput
	discordBuildModal     = discord.BuildModal
	discordApprovalButtons  = discord.ApprovalButtons
	discordAgentSelectMenu  = discord.AgentSelectMenu
)

var (
	verifyDiscordSignature = discord.VerifySignature
	interactionUserID      = discord.InteractionUserID
	extractModalValues     = discord.ExtractModalValues
	runCallbackWithTimeout = discord.RunCallbackWithTimeout
)

// --- Interaction Handler ---

// handleDiscordInteraction processes incoming Discord interaction webhooks.
func handleDiscordInteraction(db *DiscordBot, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
		return
	}

	// Read body.
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB limit
	if err != nil {
		http.Error(w, `{"error":"read body failed"}`, http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Verify Ed25519 signature.
	publicKey := db.cfg.Discord.PublicKey
	if publicKey == "" {
		log.Warn("discord interactions: no public key configured")
		http.Error(w, `{"error":"interactions not configured"}`, http.StatusServiceUnavailable)
		return
	}

	sig := r.Header.Get("X-Signature-Ed25519")
	ts := r.Header.Get("X-Signature-Timestamp")
	if sig == "" || ts == "" {
		http.Error(w, `{"error":"missing signature headers"}`, http.StatusUnauthorized)
		return
	}

	if !verifyDiscordSignature(publicKey, sig, ts, body) {
		log.Warn("discord interactions: invalid signature", "ip", clientIP(r))
		http.Error(w, `{"error":"invalid signature"}`, http.StatusUnauthorized)
		return
	}

	// Parse interaction.
	var interaction discord.Interaction
	if err := json.Unmarshal(body, &interaction); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	ctx := trace.WithID(context.Background(), trace.NewID("discord-interaction"))

	// Route by interaction type.
	switch interaction.Type {
	case discord.InteractionTypePing:
		// Respond with PONG.
		log.InfoCtx(ctx, "discord interaction PING received")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(discord.InteractionResponse{Type: discord.InteractionResponsePong})
		return

	case discord.InteractionTypeMessageComponent:
		handleComponentInteraction(ctx, db, w, &interaction)
		return

	case discord.InteractionTypeModalSubmit:
		handleModalSubmit(ctx, db, w, &interaction)
		return

	case discord.InteractionTypeApplicationCmd:
		// Application commands — respond with a basic message for now.
		log.InfoCtx(ctx, "discord application command received")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(discord.InteractionResponse{
			Type: discord.InteractionResponseMessage,
			Data: &discord.InteractionResponseData{
				Content: "Command received. Use the Tetora dashboard for full functionality.",
			},
		})
		return

	default:
		log.Warn("discord interactions: unknown type", "type", interaction.Type)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(discord.InteractionResponse{
			Type: discord.InteractionResponseMessage,
			Data: &discord.InteractionResponseData{
				Content: "Unknown interaction type.",
				Flags:   64, // ephemeral
			},
		})
	}
}

// handleComponentInteraction routes button clicks and select menu selections.
func handleComponentInteraction(ctx context.Context, db *DiscordBot, w http.ResponseWriter, interaction *discord.Interaction) {
	var data discord.InteractionData
	if err := json.Unmarshal(interaction.Data, &data); err != nil {
		log.WarnCtx(ctx, "discord component: invalid data", "error", err)
		http.Error(w, `{"error":"invalid component data"}`, http.StatusBadRequest)
		return
	}

	userID := interactionUserID(interaction)
	log.InfoCtx(ctx, "discord component interaction",
		"customID", data.CustomID,
		"userID", userID,
		"values", fmt.Sprintf("%v", data.Values))

	// Check registered interaction callbacks.
	if db.interactions != nil {
		if pi := db.interactions.lookup(data.CustomID); pi != nil {
			// Check allowed users.
			if len(pi.AllowedIDs) > 0 && !sliceContainsStr(pi.AllowedIDs, userID) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(discord.InteractionResponse{
					Type: discord.InteractionResponseMessage,
					Data: &discord.InteractionResponseData{
						Content: "You are not allowed to use this component.",
						Flags:   64, // ephemeral
					},
				})
				return
			}

			// Fire callback in background.
			if pi.Callback != nil {
				runCallbackWithTimeout(pi.Callback, data)
			}

			// Remove if not reusable.
			if !pi.Reusable {
				db.interactions.remove(data.CustomID)
			}

			// Respond: custom Response → modal → deferred update.
			w.Header().Set("Content-Type", "application/json")
			if pi.Response != nil {
				json.NewEncoder(w).Encode(*pi.Response)
			} else if pi.ModalResponse != nil {
				json.NewEncoder(w).Encode(*pi.ModalResponse)
			} else {
				json.NewEncoder(w).Encode(discord.InteractionResponse{
					Type: discord.InteractionResponseDeferredUpdate,
				})
			}
			return
		}
	}

	// Default: handle common built-in custom_id patterns.
	response := handleBuiltinComponent(ctx, db, data, userID)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleModalSubmit processes modal form submissions.
func handleModalSubmit(ctx context.Context, db *DiscordBot, w http.ResponseWriter, interaction *discord.Interaction) {
	var data discord.InteractionData
	if err := json.Unmarshal(interaction.Data, &data); err != nil {
		log.WarnCtx(ctx, "discord modal: invalid data", "error", err)
		http.Error(w, `{"error":"invalid modal data"}`, http.StatusBadRequest)
		return
	}

	userID := interactionUserID(interaction)
	log.InfoCtx(ctx, "discord modal submit",
		"customID", data.CustomID,
		"userID", userID)

	// Extract modal field values.
	values := extractModalValues(data.Components)

	// Check registered interaction callbacks.
	if db.interactions != nil {
		if pi := db.interactions.lookup(data.CustomID); pi != nil {
			if len(pi.AllowedIDs) > 0 && !sliceContainsStr(pi.AllowedIDs, userID) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(discord.InteractionResponse{
					Type: discord.InteractionResponseMessage,
					Data: &discord.InteractionResponseData{
						Content: "You are not allowed to submit this form.",
						Flags:   64,
					},
				})
				return
			}

			if pi.Callback != nil {
				runCallbackWithTimeout(pi.Callback, data)
			}
			db.interactions.remove(data.CustomID)

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(discord.InteractionResponse{
				Type: discord.InteractionResponseMessage,
				Data: &discord.InteractionResponseData{
					Content: "Form submitted successfully.",
					Flags:   64,
				},
			})
			return
		}
	}

	// Default response for unhandled modals.
	log.InfoCtx(ctx, "discord modal unhandled", "customID", data.CustomID, "values", fmt.Sprintf("%v", values))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(discord.InteractionResponse{
		Type: discord.InteractionResponseMessage,
		Data: &discord.InteractionResponseData{
			Content: fmt.Sprintf("Form received (%d fields).", len(values)),
			Flags:   64,
		},
	})
}

// --- Built-in Component Handlers ---

// handleBuiltinComponent handles common built-in component custom_id patterns.
func handleBuiltinComponent(ctx context.Context, db *DiscordBot, data discord.InteractionData, userID string) discord.InteractionResponse {
	customID := data.CustomID

	// P28.0: Approval gate callbacks.
	if strings.HasPrefix(customID, "gate_approve:") {
		reqID := strings.TrimPrefix(customID, "gate_approve:")
		if db.approvalGate != nil {
			db.approvalGate.handleGateCallback(reqID, true)
		}
		return discord.InteractionResponse{
			Type: discord.InteractionResponseUpdateMessage,
			Data: &discord.InteractionResponseData{
				Content: fmt.Sprintf("Approved by <@%s>.", userID),
			},
		}
	}
	if strings.HasPrefix(customID, "gate_always:") {
		rest := strings.TrimPrefix(customID, "gate_always:")
		parts := strings.SplitN(rest, ":", 2)
		if len(parts) == 2 {
			reqID, toolName := parts[0], parts[1]
			if db.approvalGate != nil {
				db.approvalGate.AutoApprove(toolName)
				db.approvalGate.handleGateCallback(reqID, true)
			}
			return discord.InteractionResponse{
				Type: discord.InteractionResponseUpdateMessage,
				Data: &discord.InteractionResponseData{
					Content: fmt.Sprintf("Always approved `%s` by <@%s>.", toolName, userID),
				},
			}
		}
	}
	if strings.HasPrefix(customID, "gate_reject:") {
		reqID := strings.TrimPrefix(customID, "gate_reject:")
		if db.approvalGate != nil {
			db.approvalGate.handleGateCallback(reqID, false)
		}
		return discord.InteractionResponse{
			Type: discord.InteractionResponseUpdateMessage,
			Data: &discord.InteractionResponseData{
				Content: fmt.Sprintf("Rejected by <@%s>.", userID),
			},
		}
	}

	// Pattern: "approve:{taskID}" / "reject:{taskID}"
	if strings.HasPrefix(customID, "approve:") {
		taskID := strings.TrimPrefix(customID, "approve:")
		log.InfoCtx(ctx, "discord component: task approved", "taskID", taskID, "userID", userID)
		audit.Log(db.cfg.HistoryDB, "discord.component.approve", "discord",
			fmt.Sprintf("task=%s user=%s", taskID, userID), "")
		return discord.InteractionResponse{
			Type: discord.InteractionResponseUpdateMessage,
			Data: &discord.InteractionResponseData{
				Content: fmt.Sprintf("Task `%s` approved by <@%s>.", truncate(taskID, 8), userID),
			},
		}
	}

	if strings.HasPrefix(customID, "reject:") {
		taskID := strings.TrimPrefix(customID, "reject:")
		log.InfoCtx(ctx, "discord component: task rejected", "taskID", taskID, "userID", userID)
		audit.Log(db.cfg.HistoryDB, "discord.component.reject", "discord",
			fmt.Sprintf("task=%s user=%s", taskID, userID), "")
		return discord.InteractionResponse{
			Type: discord.InteractionResponseUpdateMessage,
			Data: &discord.InteractionResponseData{
				Content: fmt.Sprintf("Task `%s` rejected by <@%s>.", truncate(taskID, 8), userID),
			},
		}
	}

	// Pattern: "agent_select" — route to selected agent.
	if customID == "agent_select" && len(data.Values) > 0 {
		agent := data.Values[0]
		log.InfoCtx(ctx, "discord component: agent selected", "agent", agent, "userID", userID)
		return discord.InteractionResponse{
			Type: discord.InteractionResponseMessage,
			Data: &discord.InteractionResponseData{
				Content: fmt.Sprintf("Routing to agent **%s**...", agent),
			},
		}
	}

	// Unknown component.
	log.InfoCtx(ctx, "discord component: unhandled", "customID", customID)
	return discord.InteractionResponse{
		Type: discord.InteractionResponseDeferredUpdate,
	}
}

// --- Helpers ---

// sliceContainsStr checks if a string slice contains a value.
// Also exported as discord.ContainsStr in internal/discord.
func sliceContainsStr(slice []string, val string) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
}

// --- WebSocket aliases and gateway interactions (from discord_interactions.go) ---

// wsConn is a type alias so all root-package files share the same type.
type wsConn = discord.WsConn

// wsConnect dials a WebSocket URL (TLS) and completes the upgrade handshake.
var wsConnect = discord.WsConnect

// wsAcceptKey computes the expected Sec-WebSocket-Accept header value.
var wsAcceptKey = discord.WsAcceptKey

func (db *DiscordBot) sendIdentify(ws *wsConn) error {
	intents := discord.IntentGuildMessages | discord.IntentDirectMessages | discord.IntentMessageContent

	// P14.5: Add voice intents if voice is enabled
	if db.cfg.Discord.Voice.Enabled {
		intents |= intentGuildVoiceStates
	}

	id := discord.IdentifyData{
		Token:   db.cfg.Discord.BotToken,
		Intents: intents,
		Properties: map[string]string{
			"os": "linux", "browser": "tetora", "device": "tetora",
		},
	}
	d, _ := json.Marshal(id)
	return ws.WriteJSON(discord.GatewayPayload{Op: discord.OpIdentify, D: d})
}

func (db *DiscordBot) sendResume(ws *wsConn, seq int) error {
	r := discord.ResumePayload{
		Token: db.cfg.Discord.BotToken, SessionID: db.sessionID, Seq: seq,
	}
	d, _ := json.Marshal(r)
	return ws.WriteJSON(discord.GatewayPayload{Op: discord.OpResume, D: d})
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
	return ws.WriteJSON(discord.GatewayPayload{Op: discord.OpHeartbeat, D: d})
}

// handleGatewayInteraction processes Discord interactions received via the Gateway
// (as opposed to the HTTP webhook endpoint). Responds via REST API callback.
func (db *DiscordBot) handleGatewayInteraction(interaction *discord.Interaction) {
	ctx := trace.WithID(context.Background(), trace.NewID("discord-interaction"))

	switch interaction.Type {
	case discord.InteractionTypePing:
		db.respondToInteraction(interaction, discord.InteractionResponse{Type: discord.InteractionResponsePong})

	case discord.InteractionTypeMessageComponent:
		resp := db.handleGatewayComponent(ctx, interaction)
		db.respondToInteraction(interaction, resp)

	case discord.InteractionTypeModalSubmit:
		resp := db.handleGatewayModal(ctx, interaction)
		db.respondToInteraction(interaction, resp)
	}
}

// handleGatewayComponent routes button clicks received via Gateway.
func (db *DiscordBot) handleGatewayComponent(ctx context.Context, interaction *discord.Interaction) discord.InteractionResponse {
	var data discord.InteractionData
	if err := json.Unmarshal(interaction.Data, &data); err != nil {
		log.WarnCtx(ctx, "discord gateway component: invalid data", "error", err)
		return discord.InteractionResponse{Type: discord.InteractionResponseDeferredUpdate}
	}

	userID := interactionUserID(interaction)
	log.InfoCtx(ctx, "discord gateway component interaction",
		"customID", data.CustomID, "userID", userID)

	// Check registered interaction callbacks.
	if db.interactions != nil {
		if pi := db.interactions.lookup(data.CustomID); pi != nil {
			if len(pi.AllowedIDs) > 0 && !sliceContainsStr(pi.AllowedIDs, userID) {
				return discord.InteractionResponse{
					Type: discord.InteractionResponseMessage,
					Data: &discord.InteractionResponseData{
						Content: "You are not allowed to use this component.",
						Flags:   64,
					},
				}
			}
			if pi.Callback != nil {
				runCallbackWithTimeout(pi.Callback, data)
			}
			if !pi.Reusable {
				db.interactions.remove(data.CustomID)
			}
			if pi.Response != nil {
				return *pi.Response
			}
			if pi.ModalResponse != nil {
				return *pi.ModalResponse
			}
			return discord.InteractionResponse{Type: discord.InteractionResponseDeferredUpdate}
		}
	}

	// Fall through to built-in handlers.
	return handleBuiltinComponent(ctx, db, data, userID)
}

// handleGatewayModal routes modal submissions received via Gateway.
func (db *DiscordBot) handleGatewayModal(ctx context.Context, interaction *discord.Interaction) discord.InteractionResponse {
	var data discord.InteractionData
	if err := json.Unmarshal(interaction.Data, &data); err != nil {
		log.WarnCtx(ctx, "discord gateway modal: invalid data", "error", err)
		return discord.InteractionResponse{Type: discord.InteractionResponseDeferredUpdate}
	}

	userID := interactionUserID(interaction)
	log.InfoCtx(ctx, "discord gateway modal submit", "customID", data.CustomID, "userID", userID)

	if db.interactions != nil {
		if pi := db.interactions.lookup(data.CustomID); pi != nil {
			if len(pi.AllowedIDs) > 0 && !sliceContainsStr(pi.AllowedIDs, userID) {
				return discord.InteractionResponse{
					Type: discord.InteractionResponseMessage,
					Data: &discord.InteractionResponseData{
						Content: "You are not allowed to submit this form.",
						Flags:   64,
					},
				}
			}
			if pi.Callback != nil {
				runCallbackWithTimeout(pi.Callback, data)
			}
			db.interactions.remove(data.CustomID)
			return discord.InteractionResponse{
				Type: discord.InteractionResponseDeferredUpdate,
			}
		}
	}

	return discord.InteractionResponse{Type: discord.InteractionResponseDeferredUpdate}
}

// respondToInteraction sends an interaction response via REST API (for Gateway-received interactions).
func (db *DiscordBot) respondToInteraction(interaction *discord.Interaction, resp discord.InteractionResponse) {
	path := fmt.Sprintf("/interactions/%s/%s/callback", interaction.ID, interaction.Token)
	db.discordPost(path, resp)
}


// formatDurationMs converts milliseconds to a human-readable string (e.g. "11.9s", "320ms").
func formatDurationMs(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	s := ms / 1000
	if s < 60 {
		return fmt.Sprintf("%.1fs", float64(ms)/1000)
	}
	h := s / 3600
	m := (s % 3600) / 60
	sec := s % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm %ds", h, m, sec)
	}
	return fmt.Sprintf("%dm %ds", m, sec)
}

// formatTokenField renders the "今日 Token" embed value.
// When limit > 0 it shows a progress bar; otherwise plain numbers.
func formatTokenField(in, out, limit int) string {
	counts := fmt.Sprintf("%s in / %s out", formatTokenCount(in), formatTokenCount(out))
	if limit <= 0 {
		return counts
	}
	total := in + out
	pct := float64(total) / float64(limit) * 100
	if pct > 100 {
		pct = 100
	}
	const barWidth = 10
	filled := int(pct / 100 * barWidth)
	bar := ""
	for i := 0; i < barWidth; i++ {
		if i < filled {
			bar += "▓"
		} else {
			bar += "░"
		}
	}
	return fmt.Sprintf("%s %.0f%%\n%s", bar, pct, counts)
}

// formatTokenCount formats a token count with K/M suffix for readability.
func formatTokenCount(n int) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

// --- REST API Helpers ---

func (db *DiscordBot) sendMessage(channelID, content string) {
	db.api.SendMessage(channelID, content)
}

func (db *DiscordBot) sendEmbed(channelID string, embed discord.Embed) {
	db.api.SendEmbed(channelID, embed)
}

func (db *DiscordBot) sendEmbedReply(channelID, replyToID string, embed discord.Embed) {
	db.api.SendEmbedReply(channelID, replyToID, embed)
}

func (db *DiscordBot) sendTyping(channelID string) {
	db.api.SendTyping(channelID)
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

// resolveThreadParent returns the parent channel ID for a thread.
// Checks the cache first, then falls back to the Discord API.
func (db *DiscordBot) resolveThreadParent(threadID string) string {
	if db.threadParents == nil {
		return ""
	}
	// Check cache (includes negative entries).
	if parentID, cached := db.threadParents.get(threadID); cached {
		return parentID
	}
	// Fallback: GET /channels/{threadID} and parse parent_id.
	body, err := db.api.Request("GET", fmt.Sprintf("/channels/%s", threadID), nil)
	if err != nil {
		log.Debug("resolveThreadParent API failed", "thread", threadID, "error", err)
		// Cache negative result to avoid repeated API calls on failure.
		db.threadParents.set(threadID, "")
		return ""
	}
	var ch struct {
		ParentID string `json:"parent_id"`
	}
	if err := json.Unmarshal(body, &ch); err != nil || ch.ParentID == "" {
		db.threadParents.set(threadID, "")
		return ""
	}
	db.threadParents.set(threadID, ch.ParentID)
	log.Debug("resolved thread parent", "thread", threadID, "parent", ch.ParentID)
	return ch.ParentID
}

// isAllowedChannelOrThread checks if a channel is allowed, including thread→parent resolution.
// If the channel itself isn't allowed but is a guild thread, resolves its parent and checks that.
func (db *DiscordBot) isAllowedChannelOrThread(chID, guildID string) bool {
	if db.isAllowedChannel(chID) {
		return true
	}
	// Only attempt thread parent resolution for guild messages.
	if guildID == "" {
		return false
	}
	parentID := db.resolveThreadParent(chID)
	if parentID != "" {
		return db.isAllowedChannel(parentID)
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

// discordPost delegates to the api client (kept for callers in other files).
func (db *DiscordBot) discordPost(path string, payload any) {
	db.api.Post(path, payload)
}

// discordRequestWithResponse delegates to the api client (kept for callers in other files).
func (db *DiscordBot) discordRequestWithResponse(method, path string, payload any) ([]byte, error) {
	return db.api.Request(method, path, payload)
}

// sendMessageReturningID sends a message and returns the message ID.
func (db *DiscordBot) sendMessageReturningID(channelID, content string) (string, error) {
	return db.api.SendMessageReturningID(channelID, content)
}

// editMessage edits an existing Discord message.
func (db *DiscordBot) editMessage(channelID, messageID, content string) error {
	return db.api.EditMessage(channelID, messageID, content)
}

// editMessageWithComponents edits an existing Discord message, replacing content and components.
func (db *DiscordBot) editMessageWithComponents(channelID, messageID, content string, components []discord.Component) error {
	return db.api.EditMessageWithComponents(channelID, messageID, content, components)
}

// deleteMessage deletes a Discord message.
func (db *DiscordBot) deleteMessage(channelID, messageID string) {
	db.api.DeleteMessage(channelID, messageID)
}

// --- P14.1: Discord Components v2 ---

// sendMessageWithComponents sends a message with interactive components (buttons, selects, etc.).
func (db *DiscordBot) sendMessageWithComponents(channelID, content string, components []discord.Component) {
	db.api.SendMessageWithComponents(channelID, content, components)
}

// sendMessageWithComponentsReturningID sends a message with components and returns the message ID.
func (db *DiscordBot) sendMessageWithComponentsReturningID(channelID, content string, components []discord.Component) (string, error) {
	return db.api.SendMessageWithComponentsReturningID(channelID, content, components)
}

// sendEmbedWithComponents sends an embed message with interactive components.
func (db *DiscordBot) sendEmbedWithComponents(channelID string, embed discord.Embed, components []discord.Component) {
	db.api.SendEmbedWithComponents(channelID, embed, components)
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
	components := []discord.Component{{
		Type: discord.ComponentTypeActionRow,
		Components: []discord.Component{
			{Type: discord.ComponentTypeButton, Style: discord.ButtonStyleSuccess, Label: "Approve", CustomID: "gate_approve:" + req.ID},
			{Type: discord.ComponentTypeButton, Style: discord.ButtonStylePrimary, Label: "Always", CustomID: "gate_always:" + req.ID + ":" + req.Tool},
			{Type: discord.ComponentTypeButton, Style: discord.ButtonStyleDanger, Label: "Reject", CustomID: "gate_reject:" + req.ID},
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

// ============================================================
// Merged from presence.go
// ============================================================

// --- P19.5: Unified Presence/Typing Indicators ---

// Ensure root PresenceSetter is compatible with messaging.PresenceSetter.
var _ messaging.PresenceSetter = (PresenceSetter)(nil)

// PresenceState represents the current activity state of the bot in a channel.
type PresenceState int

const (
	PresenceIdle       PresenceState = iota
	PresenceThinking                         // processing user request
	PresenceToolUse                          // executing a tool call
	PresenceResponding                       // generating response
)

// presenceTickInterval is how often the typing indicator is refreshed.
// Most chat APIs expire typing after 5 seconds, so we refresh every 4s.
const presenceTickInterval = 4 * time.Second

// PresenceSetter is implemented by channel bots that support typing indicators.
type PresenceSetter interface {
	// SetTyping sends a typing indicator to the specified channel reference.
	// channelRef is the channel-specific identifier (chat ID, channel ID, etc.).
	SetTyping(ctx context.Context, channelRef string) error
	// PresenceName returns the channel name (e.g., "telegram", "slack").
	PresenceName() string
}

// presenceManager coordinates typing indicators across all channel bots.
type presenceManager struct {
	mu      sync.RWMutex
	setters map[string]PresenceSetter        // keyed by channel name
	active  map[string]context.CancelFunc    // active typing loops keyed by "channel:ref"
}

// globalPresence is the package-level presence manager, initialized in daemon mode.
var globalPresence *presenceManager

// newPresenceManager creates a new presenceManager.
func newPresenceManager() *presenceManager {
	return &presenceManager{
		setters: make(map[string]PresenceSetter),
		active:  make(map[string]context.CancelFunc),
	}
}

// RegisterSetter registers a channel bot as a presence setter.
func (pm *presenceManager) RegisterSetter(name string, setter PresenceSetter) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.setters[name] = setter
	log.Debug("presence: registered setter", "channel", name)
}

// StartTyping starts a typing indicator loop for the given task source.
// The loop repeats every presenceTickInterval until StopTyping is called
// or the context is cancelled.
func (pm *presenceManager) StartTyping(ctx context.Context, source string) {
	if source == "" {
		return
	}

	channel, ref := parseSourceChannel(source)
	if channel == "" || ref == "" {
		return
	}

	pm.mu.RLock()
	setter, ok := pm.setters[channel]
	pm.mu.RUnlock()
	if !ok {
		return // no setter registered for this channel
	}

	key := channel + ":" + ref

	// Cancel any existing typing loop for this key.
	pm.mu.Lock()
	if cancel, exists := pm.active[key]; exists {
		cancel()
	}
	loopCtx, loopCancel := context.WithCancel(ctx)
	pm.active[key] = loopCancel
	pm.mu.Unlock()

	// Start typing loop in background.
	go pm.typingLoop(loopCtx, setter, ref, key)
}

// StopTyping cancels the typing indicator loop for the given task source.
func (pm *presenceManager) StopTyping(source string) {
	if source == "" {
		return
	}

	channel, ref := parseSourceChannel(source)
	if channel == "" || ref == "" {
		return
	}

	key := channel + ":" + ref

	pm.mu.Lock()
	if cancel, exists := pm.active[key]; exists {
		cancel()
		delete(pm.active, key)
	}
	pm.mu.Unlock()
}

// typingLoop repeatedly sends typing indicators until the context is cancelled.
func (pm *presenceManager) typingLoop(ctx context.Context, setter PresenceSetter, ref, key string) {
	// Send the first typing indicator immediately.
	if err := setter.SetTyping(ctx, ref); err != nil {
		log.Debug("presence: typing error", "channel", setter.PresenceName(), "ref", ref, "error", err)
	}

	ticker := time.NewTicker(presenceTickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Clean up the active entry if it still references us.
			pm.mu.Lock()
			if _, exists := pm.active[key]; exists {
				delete(pm.active, key)
			}
			pm.mu.Unlock()
			return
		case <-ticker.C:
			if err := setter.SetTyping(ctx, ref); err != nil {
				log.Debug("presence: typing error", "channel", setter.PresenceName(), "ref", ref, "error", err)
			}
		}
	}
}

// parseSourceChannel extracts the channel name and channel reference from a task source.
//
// Source formats:
//   - "telegram"          -> ("telegram", "") — no ref, won't start typing
//   - "telegram:12345"    -> ("telegram", "12345")
//   - "slack:C123"        -> ("slack", "C123")
//   - "discord:456"       -> ("discord", "456")
//   - "chat:telegram:789" -> ("telegram", "789")
//   - "route:slack:C123"  -> ("slack", "C123")
//   - "whatsapp:123"      -> ("whatsapp", "123")
func parseSourceChannel(source string) (channel, ref string) {
	if source == "" {
		return "", ""
	}

	parts := strings.Split(source, ":")

	switch len(parts) {
	case 1:
		// "telegram" — channel only, no ref
		return parts[0], ""
	case 2:
		// "telegram:12345" or "slack:C123"
		return parts[0], parts[1]
	default:
		// "chat:telegram:789" or "route:slack:C123" — skip prefix
		// The channel name is parts[1], ref is everything after
		return parts[1], strings.Join(parts[2:], ":")
	}
}

// --- PresenceSetter Implementations ---

// Telegram and Slack PresenceSetter implementations are in their
// respective internal/messaging/ packages.

// Discord Bot — POST /channels/{channelRef}/typing
func (db *DiscordBot) SetTyping(ctx context.Context, channelRef string) error {
	if channelRef == "" {
		return nil
	}
	url := discord.APIBase + fmt.Sprintf("/channels/%s/typing", channelRef)
	req, err := http.NewRequestWithContext(ctx, "POST", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bot "+db.cfg.Discord.BotToken)
	resp, err := db.api.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (db *DiscordBot) PresenceName() string { return "discord" }
