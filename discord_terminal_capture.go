package main

import (
	"fmt"
	"strings"
	"time"

	"tetora/internal/discord"
	"tetora/internal/log"
	"tetora/internal/tmux"
)

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
