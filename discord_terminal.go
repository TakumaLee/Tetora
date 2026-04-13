package main

import (
	"fmt"
	"sync"
	"time"

	"tetora/internal/log"
	"tetora/internal/tmux"
)

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
