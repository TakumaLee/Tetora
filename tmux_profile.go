package main

import (
	"cmp"
	"strings"
)

// tmuxCLIProfile abstracts the tool-specific behavior of interactive CLI tools
// running inside tmux sessions. Each CLI tool (Claude Code, Codex, etc.) implements
// this interface so TmuxProvider can remain tool-agnostic.
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
	}

	// Unset Claude Code session env vars to prevent nested-session detection.
	// tmux server may have inherited these from the parent process, so cmd.Env
	// filtering on tmux itself is not enough — we must clear them in the shell.
	return "env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_TEAM_MODE " + binaryPath + " " + strings.Join(args, " ")
}

func (p *claudeTmuxProfile) DetectState(capture string) tmuxScreenState {
	lastLines := lastNonEmptyLines(capture, 5)
	if len(lastLines) == 0 {
		return tmuxStateUnknown
	}

	bottom := strings.Join(lastLines, "\n")
	bottomLower := strings.ToLower(bottom)

	// Approval detection: Claude Code asks for permission.
	approvalPatterns := []string{
		"allow", "(y/n)", "do you want to", "approve",
		"yes/no", "permit", "allow once",
	}
	for _, pat := range approvalPatterns {
		if strings.Contains(bottomLower, pat) {
			return tmuxStateApproval
		}
	}

	// Done detection: back at shell prompt.
	lastLine := lastLines[len(lastLines)-1]
	if isShellPrompt(lastLine) {
		return tmuxStateDone
	}

	// Waiting detection: Claude Code input prompt.
	// Claude Code uses "❯" (U+276F) as the prompt character, or "> " in older versions.
	trimmedLast := strings.TrimSpace(lastLine)
	waitingPatterns := []string{
		"> ", "what would you like", "how can i help",
	}
	if trimmedLast == "❯" || strings.HasPrefix(trimmedLast, "❯ ") {
		return tmuxStateWaiting
	}
	for _, pat := range waitingPatterns {
		if strings.Contains(strings.ToLower(lastLine), pat) {
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
	approvalPatterns := []string{
		"(y/n)", "approve", "allow",
	}
	for _, pat := range approvalPatterns {
		if strings.Contains(bottomLower, pat) {
			return tmuxStateApproval
		}
	}

	// Done detection: back at shell prompt.
	lastLine := lastLines[len(lastLines)-1]
	if isShellPrompt(lastLine) {
		return tmuxStateDone
	}

	// Waiting detection: Codex input prompt.
	lastLineLower := strings.ToLower(lastLine)
	if strings.HasPrefix(strings.TrimSpace(lastLineLower), ">") ||
		strings.Contains(lastLineLower, "what would you like") {
		return tmuxStateWaiting
	}

	return tmuxStateWorking
}

func (p *codexTmuxProfile) ApproveKeys() []string { return []string{"y", "Enter"} }
func (p *codexTmuxProfile) RejectKeys() []string  { return []string{"n", "Enter"} }

// --- Shared Utilities ---

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
