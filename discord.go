package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"tetora/internal/discord"
	dtypes "tetora/internal/dispatch"
	"tetora/internal/log"
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

// dispatchStateAdapter makes *dispatchState implement discord.StateAccessor.
// This allows the discord package to be decoupled from the main-package dispatchState.
type dispatchStateAdapter struct {
	s *dispatchState
}

func (a *dispatchStateAdapter) TrackTask(task dtypes.Task, cancelFn context.CancelFunc) func() {
	a.s.mu.Lock()
	a.s.running[task.ID] = &taskState{
		task:         task,
		startAt:      time.Now(),
		lastActivity: time.Now(),
		cancelFn:     cancelFn,
	}
	a.s.mu.Unlock()
	return func() {
		a.s.mu.Lock()
		delete(a.s.running, task.ID)
		a.s.mu.Unlock()
	}
}

func (a *dispatchStateAdapter) RunningCount() int {
	a.s.mu.Lock()
	defer a.s.mu.Unlock()
	return len(a.s.running)
}

func (a *dispatchStateAdapter) CancelAll() {
	a.s.mu.Lock()
	cancelFn := a.s.cancel
	a.s.mu.Unlock()
	if cancelFn != nil {
		cancelFn()
	}
}

func (a *dispatchStateAdapter) SetDiscordActivity(taskID, agent, phase, author, channelID, prompt string, startAt time.Time) {
	a.s.setDiscordActivity(taskID, &discordActivity{
		TaskID:    taskID,
		Agent:     agent,
		Phase:     phase,
		Author:    author,
		ChannelID: channelID,
		Prompt:    prompt,
		StartAt:   startAt,
	})
}

func (a *dispatchStateAdapter) UpdateDiscordPhase(taskID, phase string) {
	a.s.updateDiscordPhase(taskID, phase)
}

func (a *dispatchStateAdapter) RemoveDiscordActivity(taskID string) {
	a.s.removeDiscordActivity(taskID)
}

func (a *dispatchStateAdapter) DiscordActivity(id string) (*discord.DiscordActivity, bool) {
	a.s.mu.Lock()
	defer a.s.mu.Unlock()
	da, ok := a.s.discordActivities[id]
	if !ok {
		return nil, false
	}
	return &discord.DiscordActivity{
		TaskID:    da.TaskID,
		Agent:     da.Agent,
		Phase:     da.Phase,
		Author:    da.Author,
		ChannelID: da.ChannelID,
		StartAt:   da.StartAt,
		Prompt:    da.Prompt,
	}, true
}

func (a *dispatchStateAdapter) Broker() *dtypes.Broker {
	return a.s.broker
}

// Ensure dispatchStateAdapter implements discord.StateAccessor at compile time.
var _ discord.StateAccessor = (*dispatchStateAdapter)(nil)

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
