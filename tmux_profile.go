package main

import (
	"cmp"
	"os"
	"path/filepath"
	"strings"
)

// tmuxCLIProfile abstracts tool-specific behavior of interactive CLI tools
// running inside tmux sessions. Each CLI tool (Claude Code, Codex, etc.) implements
// this interface so TerminalProvider can remain tool-agnostic.
type tmuxCLIProfile interface {
	// Name returns the profile identifier (e.g. "claude", "codex").
	Name() string
	// BuildCommand constructs the full CLI command string for the given request.
	BuildCommand(binaryPath string, req ProviderRequest) string
	// DetectState analyzes tmux capture output to determine the screen state.
	DetectState(capture string) tmuxScreenState
	// ApproveKeys returns the tmux key sequence to approve a permission prompt.
	ApproveKeys() []string
	// RejectKeys returns the tmux key sequence to reject a permission prompt.
	RejectKeys() []string
}

// --- Claude Code Profile ---

type claudeTmuxProfile struct{}

func (p *claudeTmuxProfile) Name() string { return "claude" }

func (p *claudeTmuxProfile) BuildCommand(binaryPath string, req ProviderRequest) string {
	args := []string{
		"--model", req.Model,
		"--permission-mode", cmp.Or(req.PermissionMode, "acceptEdits"),
	}

	if req.SystemPrompt != "" {
		args = append(args, "--append-system-prompt", shellQuote(req.SystemPrompt))
	}

	for _, dir := range req.AddDirs {
		args = append(args, "--add-dir", dir)
	}

	if req.MCPPath != "" {
		args = append(args, "--mcp-config", req.MCPPath)
	} else {
		// Auto-inject Tetora MCP bridge config if available.
		homeDir, _ := os.UserHomeDir()
		bridgePath := filepath.Join(homeDir, ".tetora", "mcp", "bridge.json")
		if _, err := os.Stat(bridgePath); err == nil {
			args = append(args, "--mcp-config", bridgePath)
		}
	}

	// Unset Claude Code session env vars to prevent nested-session detection.
	return "env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_TEAM_MODE " + binaryPath + " " + strings.Join(args, " ")
}

func (p *claudeTmuxProfile) DetectState(capture string) tmuxScreenState {
	lastLines := lastNonEmptyLines(capture, 12)
	if len(lastLines) == 0 {
		return tmuxStateUnknown
	}

	bottom := strings.Join(lastLines, "\n")
	bottomLower := strings.ToLower(bottom)

	// Approval detection.
	approvalPatterns := []string{
		"(y/n)", "do you want to", "approve",
		"yes/no", "allow once", "allow all",
	}
	for _, pat := range approvalPatterns {
		if strings.Contains(bottomLower, pat) {
			return tmuxStateApproval
		}
	}
	if strings.Contains(bottomLower, " allow ") || strings.HasSuffix(bottomLower, " allow") {
		return tmuxStateApproval
	}

	// Done detection: back at shell prompt.
	lastLine := lastLines[len(lastLines)-1]
	if isShellPrompt(lastLine) {
		return tmuxStateDone
	}

	// Question detection.
	if detectQuestionBlock(capture) {
		return tmuxStateQuestion
	}

	// Working indicator: ✽ Working…
	for _, line := range lastLines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "✽") {
			return tmuxStateWorking
		}
	}

	// Waiting detection: ❯ prompt.
	for _, line := range lastLines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "❯" {
			return tmuxStateWaiting
		}
		if strings.HasPrefix(trimmed, "❯ ") || strings.HasPrefix(trimmed, "❯\u00a0") || strings.HasPrefix(trimmed, "❯\t") {
			afterPrompt := strings.TrimSpace(trimmed[len("❯ "):])
			if len(afterPrompt) <= 2 {
				return tmuxStateWaiting
			}
			continue
		}
		lineLower := strings.ToLower(trimmed)
		if strings.HasPrefix(lineLower, "> ") ||
			strings.Contains(lineLower, "what would you like") ||
			strings.Contains(lineLower, "how can i help") {
			return tmuxStateWaiting
		}
	}

	return tmuxStateWorking
}

func (p *claudeTmuxProfile) ApproveKeys() []string { return []string{"y", "Enter"} }
func (p *claudeTmuxProfile) RejectKeys() []string  { return []string{"n", "Enter"} }

// --- Codex CLI Profile ---

type codexTmuxProfile struct{}

func (p *codexTmuxProfile) Name() string { return "codex" }

func (p *codexTmuxProfile) BuildCommand(binaryPath string, req ProviderRequest) string {
	args := []string{
		"--no-alt-screen", // required for tmux capture to work
	}

	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}

	// Map Tetora permission modes to Codex sandbox modes.
	switch req.PermissionMode {
	case "bypassPermissions":
		args = append(args, "--full-auto")
	case "acceptEdits":
		args = append(args, "--sandbox", "workspace-write")
	default:
		args = append(args, "--sandbox", "read-only")
	}

	for _, dir := range req.AddDirs {
		args = append(args, "--add-dir", dir)
	}

	return binaryPath + " " + strings.Join(args, " ")
}

func (p *codexTmuxProfile) DetectState(capture string) tmuxScreenState {
	lastLines := lastNonEmptyLines(capture, 5)
	if len(lastLines) == 0 {
		return tmuxStateUnknown
	}

	bottom := strings.Join(lastLines, "\n")
	bottomLower := strings.ToLower(bottom)

	// Approval detection.
	for _, pat := range []string{"(y/n)", "approve", "allow"} {
		if strings.Contains(bottomLower, pat) {
			return tmuxStateApproval
		}
	}

	// Done detection.
	lastLine := lastLines[len(lastLines)-1]
	if isShellPrompt(lastLine) {
		return tmuxStateDone
	}

	// Waiting detection.
	lastLineLower := strings.ToLower(lastLine)
	if strings.HasPrefix(strings.TrimSpace(lastLineLower), ">") ||
		strings.Contains(lastLineLower, "what would you like") {
		return tmuxStateWaiting
	}

	return tmuxStateWorking
}

func (p *codexTmuxProfile) ApproveKeys() []string { return []string{"y", "Enter"} }
func (p *codexTmuxProfile) RejectKeys() []string  { return []string{"n", "Enter"} }

// --- Question & Subagent Parsing ---

// detectQuestionBlock scans capture for an AskUserQuestion block.
func detectQuestionBlock(capture string) bool {
	lines := strings.Split(capture, "\n")

	i := len(lines) - 1
	for i >= 0 && strings.TrimSpace(lines[i]) == "" {
		i--
	}
	// Skip status bars.
	for i >= 0 {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.Contains(trimmed, "───") || strings.Contains(trimmed, "⏵") ||
			isStatusBarEmoji(trimmed) || isHintOrChipLine(trimmed) {
			i--
			continue
		}
		break
	}

	optionCount := 0
	hasCursor := false
	for i >= 0 {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			break
		}
		if isHintOrChipLine(trimmed) {
			i--
			continue
		}
		if (strings.HasPrefix(trimmed, "❯ ") || strings.HasPrefix(trimmed, "❯\u00a0")) && len(trimmed) > 3 {
			hasCursor = true
			optionCount++
			i--
			continue
		}
		if len(lines[i]) > 0 && (lines[i][0] == ' ' || lines[i][0] == '\t') &&
			len(trimmed) < 100 && !strings.Contains(trimmed, "───") &&
			!strings.HasPrefix(trimmed, "❯") && !strings.HasPrefix(trimmed, "?") {
			optionCount++
			i--
			continue
		}
		break
	}

	if !hasCursor || optionCount < 2 {
		return false
	}

	if i >= 0 {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "? ") && len(trimmed) > 3 {
			return true
		}
	}
	return false
}

// --- Shared Utilities ---

// shellQuote wraps a string in single quotes for shell command construction.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

// lastNonEmptyLines returns the last n non-empty trimmed lines from a capture string.
func lastNonEmptyLines(capture string, n int) []string {
	if capture == "" {
		return nil
	}
	lines := strings.Split(capture, "\n")
	var result []string
	for i := len(lines) - 1; i >= 0 && len(result) < n; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed != "" {
			result = append([]string{trimmed}, result...)
		}
	}
	return result
}

// isStatusBarEmoji checks if a trimmed line contains status bar emoji markers.
func isStatusBarEmoji(trimmed string) bool {
	return strings.Contains(trimmed, "🤖") || strings.Contains(trimmed, "📝") ||
		strings.Contains(trimmed, "🆔") || strings.Contains(trimmed, "💻") ||
		strings.Contains(trimmed, "📁") || strings.Contains(trimmed, "⏰")
}

// isHintOrChipLine checks if a line is a Claude Code hint or chip bar.
func isHintOrChipLine(s string) bool {
	if strings.Contains(s, "←") && strings.Contains(s, "→") {
		return true
	}
	lower := strings.ToLower(s)
	return strings.Contains(lower, "↑/↓") ||
		strings.Contains(lower, "enter to select") ||
		strings.Contains(lower, "esc to cancel") ||
		strings.Contains(lower, "space toggles")
}
