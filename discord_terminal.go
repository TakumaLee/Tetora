package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"tetora/internal/tmux"
)

// --- Discord Terminal Bridge ---
// Bridges interactive CLI tool sessions (via tmux) to Discord,
// allowing remote control from a phone via buttons and text input.
// Coexists with the headless CLI dispatch mode — Terminal is for interactive,
// CLI is for automated dispatch. Both can run simultaneously.

// DiscordTerminalConfig holds configuration for the terminal bridge feature.
type DiscordTerminalConfig struct {
	Enabled      bool     `json:"enabled"`
	AllowedUsers []string `json:"allowedUsers,omitempty"` // Discord user IDs (empty = channel allowlist only)
	MaxSessions  int      `json:"maxSessions,omitempty"`  // default 3
	CaptureRows  int      `json:"captureRows,omitempty"`  // tmux pane height, default 40
	CaptureCols  int      `json:"captureCols,omitempty"`  // tmux pane width, default 120
	IdleTimeout  string   `json:"idleTimeout,omitempty"`  // default "30m"
	ClaudePath   string   `json:"claudePath,omitempty"`   // falls back to cfg.ClaudePath
	CodexPath    string   `json:"codexPath,omitempty"`    // path to codex binary
	DefaultTool  string   `json:"defaultTool,omitempty"`  // "claude" (default) or "codex"
	Workdir      string   `json:"workdir,omitempty"`      // default working directory
}

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
	if err := tmuxCreate(tmuxName, tb.cfg.CaptureCols, tb.cfg.CaptureRows, command, workdir); err != nil {
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
		tmuxKill(tmuxName)
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
		tmuxKill(tmuxName)
		return fmt.Errorf("send control panel: %w", err)
	}
	session.controlMsgID = controlMsgID

	// Register session.
	tb.mu.Lock()
	tb.sessions[channelID] = session
	tb.mu.Unlock()

	// Start capture loop.
	go tb.runCaptureLoop(session)

	logInfo("terminal session started",
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

	if tmuxHasSession(session.TmuxName) {
		tmuxKill(session.TmuxName)
	}

	tb.unregisterControlButtons(session.ID)

	tb.bot.editMessage(session.ChannelID, session.displayMsgID,
		"```\n[Session ended]\n```")
	tb.bot.deleteMessage(session.ChannelID, session.controlMsgID)

	logInfo("terminal session stopped", "session", session.ID, "channel", channelID)
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

		if !tmuxHasSession(session.TmuxName) {
			logInfo("terminal tmux session gone, stopping", "session", session.ID)
			tb.stopSession(session.ChannelID)
			return
		}

		// Check idle timeout.
		session.mu.Lock()
		lastActivity := session.LastActivity
		session.mu.Unlock()
		if time.Since(lastActivity) > idleTimeout {
			logInfo("terminal session idle timeout", "session", session.ID)
			tb.bot.sendMessage(session.ChannelID, "Terminal session timed out due to inactivity.")
			tb.stopSession(session.ChannelID)
			return
		}

		raw, err := tmuxCapture(session.TmuxName)
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
			logWarn("terminal display update failed", "session", session.ID, "error", err)
		}
		lastEdit = time.Now()
	}
}

// --- Discord UI ---

func (tb *terminalBridge) sendControlPanel(channelID, content, sessionID string, allowedIDs []string) (string, error) {
	prefix := "term_" + sessionID + "_"

	row1 := discordActionRow(
		discordButton(prefix+"up", "\u2b06 Up", buttonStyleSecondary),
		discordButton(prefix+"down", "\u2b07 Down", buttonStyleSecondary),
		discordButton(prefix+"enter", "\u23ce Enter", buttonStylePrimary),
		discordButton(prefix+"tab", "Tab", buttonStyleSecondary),
		discordButton(prefix+"esc", "Esc", buttonStyleSecondary),
	)
	row2 := discordActionRow(
		discordButton(prefix+"type", "\u2328 Type", buttonStylePrimary),
		discordButton(prefix+"y", "Y", buttonStyleSuccess),
		discordButton(prefix+"n", "N", buttonStyleDanger),
		discordButton(prefix+"ctrlc", "Ctrl+C", buttonStyleDanger),
		discordButton(prefix+"stop", "Stop", buttonStyleDanger),
	)

	components := []discordComponent{row1, row2}

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
			Callback: func(data discordInteractionData) {
				session := tb.getSessionByID(sessionID)
				if session == nil {
					return
				}
				session.mu.Lock()
				session.LastActivity = time.Now()
				session.mu.Unlock()

				tmuxSendKeys(session.TmuxName, keys...)
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
		ModalResponse: func() *discordInteractionResponse {
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
		Callback: func(data discordInteractionData) {
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

			tmuxSendText(session.TmuxName, text)
			tmuxSendKeys(session.TmuxName, "Enter")
			tb.signalCapture(session)
		},
	})

	// "Stop" button.
	tb.bot.interactions.register(&pendingInteraction{
		CustomID:   prefix + "stop",
		CreatedAt:  time.Now(),
		AllowedIDs: allowedIDs,
		Reusable:   false,
		Callback: func(data discordInteractionData) {
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
func (tb *terminalBridge) handleTermCommand(msg discordMessage, args string) {
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

	tmuxSendText(session.TmuxName, text)
	tmuxSendKeys(session.TmuxName, "Enter")
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
