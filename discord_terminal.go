package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// --- Discord Terminal Bridge ---
// Bridges interactive Claude Code sessions (via tmux) to Discord,
// allowing remote control from a phone via buttons and text input.

// DiscordTerminalConfig holds configuration for the terminal bridge feature.
type DiscordTerminalConfig struct {
	Enabled      bool     `json:"enabled"`
	AllowedUsers []string `json:"allowedUsers,omitempty"` // Discord user IDs (empty = channel allowlist only)
	MaxSessions  int      `json:"maxSessions,omitempty"`  // default 3
	CaptureRows  int      `json:"captureRows,omitempty"`  // tmux pane height, default 40
	CaptureCols  int      `json:"captureCols,omitempty"`  // tmux pane width, default 120
	IdleTimeout  string   `json:"idleTimeout,omitempty"`  // default "30m"
	ClaudePath   string   `json:"claudePath,omitempty"`   // falls back to cfg.ClaudePath
	Workdir      string   `json:"workdir,omitempty"`      // default working directory
}

// terminalSession represents a single interactive Claude Code tmux session.
type terminalSession struct {
	ID           string
	TmuxName     string
	ChannelID    string
	OwnerID      string
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
	// Apply defaults.
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
	return &terminalBridge{
		bot:      bot,
		cfg:      cfg,
		sessions: make(map[string]*terminalSession),
	}
}

// --- tmux Primitives ---

// tmuxBin returns the tmux binary path, resolving via findBinary for
// environments with minimal PATH (e.g. launchd on macOS).
func tmuxBin() string {
	if p := findBinary("tmux"); p != "" {
		return p
	}
	return "tmux" // fallback to PATH
}

// tmuxCreate creates a new tmux session with the given dimensions and command.
func tmuxCreate(name string, cols, rows int, command, workdir string) error {
	args := []string{
		"new-session", "-d",
		"-s", name,
		"-x", fmt.Sprintf("%d", cols),
		"-y", fmt.Sprintf("%d", rows),
	}
	if command != "" {
		args = append(args, command)
	}
	cmd := exec.Command(tmuxBin(), args...)
	if workdir != "" {
		cmd.Dir = workdir
	}
	// Filter out Claude Code session env vars to prevent nested-session detection.
	rawEnv := os.Environ()
	filteredEnv := make([]string, 0, len(rawEnv))
	for _, e := range rawEnv {
		if !strings.HasPrefix(e, "CLAUDECODE=") &&
			!strings.HasPrefix(e, "CLAUDE_CODE_ENTRYPOINT=") &&
			!strings.HasPrefix(e, "CLAUDE_CODE_TEAM_MODE=") {
			filteredEnv = append(filteredEnv, e)
		}
	}
	cmd.Env = filteredEnv
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux new-session: %w: %s", err, string(out))
	}
	return nil
}

// tmuxCapture captures the current visible content of a tmux pane (clean text, no ANSI).
func tmuxCapture(name string) (string, error) {
	cmd := exec.Command(tmuxBin(), "capture-pane", "-t", name, "-p")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("tmux capture-pane: %w", err)
	}
	return string(out), nil
}

// tmuxSendKeys sends key names (Up, Down, Enter, etc.) to a tmux session.
func tmuxSendKeys(name string, keys ...string) error {
	args := append([]string{"send-keys", "-t", name}, keys...)
	cmd := exec.Command(tmuxBin(), args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux send-keys: %w: %s", err, string(out))
	}
	return nil
}

// tmuxSendText sends literal text to a tmux session (uses -l flag to prevent key name interpretation).
func tmuxSendText(name string, text string) error {
	cmd := exec.Command(tmuxBin(), "send-keys", "-t", name, "-l", text)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux send-keys -l: %w: %s", err, string(out))
	}
	return nil
}

// tmuxKill kills a tmux session.
func tmuxKill(name string) error {
	cmd := exec.Command(tmuxBin(), "kill-session", "-t", name)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux kill-session: %w: %s", err, string(out))
	}
	return nil
}

// tmuxHasSession checks if a tmux session exists.
func tmuxHasSession(name string) bool {
	cmd := exec.Command(tmuxBin(), "has-session", "-t", name)
	return cmd.Run() == nil
}

// tmuxCaptureHistory captures the full scrollback history of a tmux pane (not just visible area).
func tmuxCaptureHistory(name string) (string, error) {
	cmd := exec.Command(tmuxBin(), "capture-pane", "-t", name, "-p", "-S", "-")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("tmux capture-pane history: %w", err)
	}
	return string(out), nil
}

// tmuxLoadAndPaste sends arbitrary-length text into a tmux pane via load-buffer + paste-buffer.
// This bypasses send-keys -l buffer limits.
func tmuxLoadAndPaste(name, text string) error {
	// Write text to a temp file.
	f, err := os.CreateTemp("", "tetora-prompt-*.txt")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := f.Name()
	defer os.Remove(tmpPath)
	if _, err := f.WriteString(text); err != nil {
		f.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	f.Close()

	// Load into tmux buffer.
	cmd := exec.Command(tmuxBin(), "load-buffer", tmpPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux load-buffer: %w: %s", err, string(out))
	}

	// Paste into the target pane.
	cmd = exec.Command(tmuxBin(), "paste-buffer", "-t", name)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux paste-buffer: %w: %s", err, string(out))
	}

	return nil
}

// --- Session Lifecycle ---

// startSession creates a new terminal session in the given channel.
func (tb *terminalBridge) startSession(channelID, userID, workdir string) error {
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

	// Resolve claude path.
	claudePath := tb.cfg.ClaudePath
	if claudePath == "" {
		claudePath = tb.bot.cfg.ClaudePath
	}
	if claudePath == "" {
		claudePath = "claude"
	}

	// Resolve workdir.
	if workdir == "" {
		workdir = tb.cfg.Workdir
	}

	// Generate session ID.
	sessionID := fmt.Sprintf("%d", time.Now().UnixNano()%1000000)
	tmuxName := "tetora-term-" + sessionID

	// Create tmux session.
	if err := tmuxCreate(tmuxName, tb.cfg.CaptureCols, tb.cfg.CaptureRows, claudePath, workdir); err != nil {
		return fmt.Errorf("create tmux session: %w", err)
	}

	session := &terminalSession{
		ID:           sessionID,
		TmuxName:     tmuxName,
		ChannelID:    channelID,
		OwnerID:      userID,
		CreatedAt:    time.Now(),
		LastActivity: time.Now(),
		stopCh:       make(chan struct{}),
		captureCh:    make(chan struct{}, 1),
	}

	// Send display message (empty initially, will be updated by capture loop).
	displayContent := "```\nStarting Claude Code session...\n```"
	displayMsgID, err := tb.bot.sendMessageReturningID(channelID, displayContent)
	if err != nil {
		tmuxKill(tmuxName)
		return fmt.Errorf("send display message: %w", err)
	}
	session.displayMsgID = displayMsgID

	// Send control panel with buttons.
	controlContent := "Terminal Controls:"
	allowedIDs := tb.cfg.AllowedUsers
	if len(allowedIDs) == 0 {
		allowedIDs = []string{userID}
	}
	controlMsgID, err := tb.sendControlPanel(channelID, controlContent, sessionID, allowedIDs)
	if err != nil {
		tmuxKill(tmuxName)
		return fmt.Errorf("send control panel: %w", err)
	}
	session.controlMsgID = controlMsgID

	// Register in sessions map.
	tb.mu.Lock()
	tb.sessions[channelID] = session
	tb.mu.Unlock()

	// Start capture loop.
	go tb.runCaptureLoop(session)

	logInfo("terminal session started",
		"session", sessionID, "channel", channelID, "user", userID, "tmux", tmuxName)
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

	// Signal capture loop to stop.
	close(session.stopCh)

	// Kill tmux session.
	if tmuxHasSession(session.TmuxName) {
		tmuxKill(session.TmuxName)
	}

	// Clean up control buttons.
	tb.unregisterControlButtons(session.ID)

	// Update display message.
	tb.bot.editMessage(session.ChannelID, session.displayMsgID,
		"```\n[Session ended]\n```")

	// Delete control panel.
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
	// Strip ANSI escape sequences as safety net (tmux capture-pane -p should be clean).
	cleaned := ansiEscapeRe.ReplaceAllString(raw, "")

	// Escape backticks to prevent breaking the code block.
	cleaned = strings.ReplaceAll(cleaned, "```", "` ` `")

	// Trim trailing empty lines.
	lines := strings.Split(cleaned, "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}

	// If within limit, return as-is.
	result := strings.Join(lines, "\n")
	if len(result) <= maxChars {
		return result
	}

	// Truncate from top, keeping the bottom (most recent) visible.
	truncated := make([]string, 0)
	totalLen := 0
	for i := len(lines) - 1; i >= 0; i-- {
		lineLen := len(lines[i]) + 1 // +1 for newline
		if totalLen+lineLen > maxChars-30 { // reserve space for truncation notice
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

// runCaptureLoop periodically captures the tmux screen and updates the Discord message.
func (tb *terminalBridge) runCaptureLoop(session *terminalSession) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	// Parse idle timeout.
	idleTimeout, err := time.ParseDuration(tb.cfg.IdleTimeout)
	if err != nil {
		idleTimeout = 30 * time.Minute
	}

	// Minimum interval between Discord edits (rate limit protection).
	minInterval := 1500 * time.Millisecond
	lastEdit := time.Time{}

	for {
		select {
		case <-session.stopCh:
			return
		case <-ticker.C:
			// Regular tick.
		case <-session.captureCh:
			// Triggered after input — small delay for screen to update.
			time.Sleep(500 * time.Millisecond)
		}

		// Check if tmux session still exists.
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

		// Capture screen.
		raw, err := tmuxCapture(session.TmuxName)
		if err != nil {
			continue
		}

		// Dirty check: only update if screen changed.
		screen := renderTerminalScreen(raw, 1988) // 2000 - len("```\n") - len("\n```")
		session.mu.Lock()
		changed := screen != session.lastScreen
		if changed {
			session.lastScreen = screen
		}
		session.mu.Unlock()

		if !changed {
			continue
		}

		// Rate limit.
		if time.Since(lastEdit) < minInterval {
			remaining := minInterval - time.Since(lastEdit)
			time.Sleep(remaining)
		}

		// Update Discord display message.
		content := "```\n" + screen + "\n```"
		if err := tb.bot.editMessage(session.ChannelID, session.displayMsgID, content); err != nil {
			logWarn("terminal display update failed", "session", session.ID, "error", err)
		}
		lastEdit = time.Now()
	}
}

// --- Discord UI ---

// sendControlPanel sends the control buttons and returns the message ID.
func (tb *terminalBridge) sendControlPanel(channelID, content, sessionID string, allowedIDs []string) (string, error) {
	prefix := "term_" + sessionID + "_"

	// Build buttons.
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

	// Send message with components.
	body, err := tb.bot.discordRequestWithResponse("POST",
		fmt.Sprintf("/channels/%s/messages", channelID),
		map[string]any{
			"content":    content,
			"components": components,
		})
	if err != nil {
		return "", err
	}
	var msg struct{ ID string `json:"id"` }
	if err := jsonUnmarshal(body, &msg); err != nil {
		return "", err
	}

	// Register button callbacks.
	tb.registerControlButtons(sessionID, allowedIDs)

	return msg.ID, nil
}

// registerControlButtons registers interaction callbacks for all terminal control buttons.
func (tb *terminalBridge) registerControlButtons(sessionID string, allowedIDs []string) {
	prefix := "term_" + sessionID + "_"

	// Key mapping: button action → tmux key(s).
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
		keys := keys // capture for closure
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

	// "Type" button → respond with modal.
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
		Callback: nil, // Modal response replaces the callback flow.
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
	stopCustomID := prefix + "stop"
	tb.bot.interactions.register(&pendingInteraction{
		CustomID:   stopCustomID,
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

// unregisterControlButtons removes all registered callbacks for a session.
func (tb *terminalBridge) unregisterControlButtons(sessionID string) {
	prefix := "term_" + sessionID + "_"
	actions := []string{"up", "down", "enter", "tab", "esc", "y", "n", "ctrlc", "type", "stop"}
	for _, action := range actions {
		tb.bot.interactions.remove(prefix + action)
	}
	tb.bot.interactions.remove("term_modal_" + sessionID)
}

// getSessionByID finds a session by its ID (not channel ID).
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

// signalCapture signals the capture loop to do an immediate re-capture.
func (tb *terminalBridge) signalCapture(session *terminalSession) {
	select {
	case session.captureCh <- struct{}{}:
	default:
	}
}

// --- /term Command Handling ---

// handleTermCommand processes /term start|stop|status commands.
func (tb *terminalBridge) handleTermCommand(msg discordMessage, args string) {
	parts := strings.Fields(strings.TrimSpace(args))
	cmd := "start"
	if len(parts) > 0 {
		cmd = strings.ToLower(parts[0])
	}

	switch cmd {
	case "start":
		// Check user permission.
		if !tb.isAllowedUser(msg.Author.ID) {
			tb.bot.sendMessage(msg.ChannelID, "You are not allowed to use terminal bridge.")
			return
		}
		workdir := ""
		if len(parts) > 1 {
			workdir = parts[1]
		}
		if err := tb.startSession(msg.ChannelID, msg.Author.ID, workdir); err != nil {
			tb.bot.sendMessage(msg.ChannelID, fmt.Sprintf("Failed to start terminal: %s", err))
			return
		}

	case "stop":
		if err := tb.stopSession(msg.ChannelID); err != nil {
			tb.bot.sendMessage(msg.ChannelID, fmt.Sprintf("Failed to stop terminal: %s", err))
			return
		}
		tb.bot.sendMessage(msg.ChannelID, "Terminal session stopped.")

	case "status":
		tb.mu.RLock()
		count := len(tb.sessions)
		lines := make([]string, 0, count)
		for ch, s := range tb.sessions {
			age := time.Since(s.CreatedAt).Round(time.Second)
			idle := time.Since(s.LastActivity).Round(time.Second)
			lines = append(lines, fmt.Sprintf("• <#%s> — session `%s` (up %s, idle %s)", ch, s.ID, age, idle))
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
			"Usage: `/term start [workdir]` | `/term stop` | `/term status`")
	}
}

// handleTerminalInput checks if a message should be routed to the terminal session
// as direct text input. Returns true if handled.
func (tb *terminalBridge) handleTerminalInput(channelID, text string) bool {
	session := tb.getSession(channelID)
	if session == nil {
		return false
	}

	// Don't intercept commands.
	if strings.HasPrefix(text, "/") || strings.HasPrefix(text, "!") {
		return false
	}

	// Send text to tmux.
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
		return true // No restrictions, relies on channel-level access
	}
	return sliceContainsStr(tb.cfg.AllowedUsers, userID)
}

// jsonUnmarshal is a small helper to unmarshal JSON from bytes.
func jsonUnmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
