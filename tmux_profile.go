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
	// Use a larger window — Claude Code has status bars below the prompt.
	lastLines := lastNonEmptyLines(capture, 12)
	if len(lastLines) == 0 {
		return tmuxStateUnknown
	}

	bottom := strings.Join(lastLines, "\n")
	bottomLower := strings.ToLower(bottom)

	// Approval detection: Claude Code asks for permission.
	// Note: "permit" removed — matches "permissions" in status bar ("bypass permissions on").
	approvalPatterns := []string{
		"(y/n)", "do you want to", "approve",
		"yes/no", "allow once", "allow all",
	}
	for _, pat := range approvalPatterns {
		if strings.Contains(bottomLower, pat) {
			return tmuxStateApproval
		}
	}
	// "allow" needs word-boundary check to avoid matching "allowed" in normal text.
	if strings.Contains(bottomLower, " allow ") || strings.HasSuffix(bottomLower, " allow") {
		return tmuxStateApproval
	}

	// Done detection: back at shell prompt.
	lastLine := lastLines[len(lastLines)-1]
	if isShellPrompt(lastLine) {
		return tmuxStateDone
	}

	// Question detection: Claude Code's AskUserQuestion shows a contiguous block:
	//   ? Which approach should we use?
	//   ❯ Option 1 (Recommended)
	//     Option 2
	//     Other
	// Scan the raw capture lines (not just lastLines) from bottom up for this exact pattern.
	if detected := detectQuestionBlock(capture); detected {
		return tmuxStateQuestion
	}

	// Working indicator detection: Claude Code v2 shows "✽ Working…" while thinking,
	// even while the empty "❯" prompt is visible below. The ❯ prompt is always present
	// as a UI element — it does NOT mean Claude is waiting for input when ✽ is visible.
	// Note: only ✽ is checked (not ⏺) because ⏺ blocks persist in scrollback after completion.
	for _, line := range lastLines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "✽") {
			return tmuxStateWorking
		}
	}

	// Waiting detection: Claude Code input prompt.
	// Claude Code v2+ shows "❯" prompt with status bars BELOW it,
	// so we must scan all bottom lines, not just the last one.
	// Note: if ✽ was present, we already returned tmuxStateWorking above.
	for _, line := range lastLines {
		trimmed := strings.TrimSpace(line)
		// Empty prompt = truly waiting for input.
		if trimmed == "❯" {
			return tmuxStateWaiting
		}
		// ❯ with text: only waiting if text is very short (cursor artifacts).
		// Submitted prompts are always longer.
		if strings.HasPrefix(trimmed, "❯ ") || strings.HasPrefix(trimmed, "❯\u00a0") || strings.HasPrefix(trimmed, "❯\t") {
			afterPrompt := strings.TrimSpace(trimmed[len("❯ "):])
			if len(afterPrompt) <= 2 {
				// Very short text — likely empty prompt with cursor position artifact.
				return tmuxStateWaiting
			}
			// Substantial text = submitted prompt, Claude is working.
			// Don't return waiting.
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

// detectQuestionBlock scans the capture from bottom up for a contiguous
// AskUserQuestion block: a "? " question line immediately followed by option
// lines (❯ selected + indented non-selected), with at least 2 options total.
// This is stricter than checking scattered patterns to avoid false positives.
func detectQuestionBlock(capture string) bool {
	lines := strings.Split(capture, "\n")

	// Scan from bottom, skip empty lines and status bars.
	i := len(lines) - 1
	for i >= 0 && strings.TrimSpace(lines[i]) == "" {
		i--
	}
	// Skip Claude Code status bars and hint/chip lines at the very bottom.
	for i >= 0 {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.Contains(trimmed, "───") || strings.Contains(trimmed, "⏵") ||
			strings.Contains(trimmed, "🤖") || strings.Contains(trimmed, "📝") ||
			strings.Contains(trimmed, "🆔") || strings.Contains(trimmed, "💻") ||
			strings.Contains(trimmed, "📁") || strings.Contains(trimmed, "⏰") ||
			isHintOrChipLine(trimmed) {
			i--
			continue
		}
		break
	}

	// Now scan upward collecting option lines (must be contiguous).
	optionCount := 0
	hasCursor := false
	for i >= 0 {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			break // gap = end of option block
		}
		// Skip hint/chip lines within the question block.
		if isHintOrChipLine(trimmed) {
			i--
			continue
		}
		// Selected option: ❯ followed by text.
		if (strings.HasPrefix(trimmed, "❯ ") || strings.HasPrefix(trimmed, "❯\u00a0")) && len(trimmed) > 3 {
			hasCursor = true
			optionCount++
			i--
			continue
		}
		// Non-selected option: indented short text line.
		if len(lines[i]) > 0 && (lines[i][0] == ' ' || lines[i][0] == '\t') &&
			len(trimmed) < 100 && !strings.Contains(trimmed, "───") &&
			!strings.HasPrefix(trimmed, "❯") && !strings.HasPrefix(trimmed, "?") {
			optionCount++
			i--
			continue
		}
		break // not an option line
	}

	if !hasCursor || optionCount < 2 {
		return false
	}

	// The line just above options must be the question: "? ..."
	if i >= 0 {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "? ") && len(trimmed) > 3 {
			return true
		}
	}
	return false
}

// --- Question & Subagent Parsing ---

// parsedQuestion holds parsed information from an AskUserQuestion capture.
type parsedQuestion struct {
	Question      string
	Options       []string // display labels (checkbox prefix stripped)
	IsMultiSelect bool     // true if [ ] or [x] patterns detected
	HasTypeOption bool     // true if "Type something"/"Other" found
	SubmitIndex   int      // index of "Submit" in raw list (-1 if absent)
	TypeIndex     int      // index of "Type something" (-1 if absent)
}

// parseQuestionFromCapture extracts the question text and option list from an
// AskUserQuestion capture. Returns nil if not detected.
func parseQuestionFromCapture(capture string) *parsedQuestion {
	lines := strings.Split(capture, "\n")

	// Scan from bottom to find the question block.
	var question string
	var rawOptions []string
	isMulti := false

	// Find option lines and question line (scan upward from bottom).
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			if len(rawOptions) > 0 {
				break // hit blank line above the option block
			}
			continue
		}

		// Skip hint/chip lines that appear in multi-select UI.
		if isHintOrChipLine(trimmed) {
			continue
		}

		// Question line: starts with "? "
		if strings.HasPrefix(trimmed, "? ") {
			question = trimmed[2:]
			break
		}

		// Selected option: ❯ followed by text
		if strings.HasPrefix(trimmed, "❯ ") || strings.HasPrefix(trimmed, "❯\u00a0") {
			optText := strings.TrimSpace(trimmed[len("❯"):])
			if optText != "" {
				if hasCheckbox(optText) {
					isMulti = true
				}
				rawOptions = append([]string{optText}, rawOptions...)
			}
			continue
		}

		// Non-selected option: indented text
		if len(lines[i]) > 0 && (lines[i][0] == ' ' || lines[i][0] == '\t') && len(trimmed) < 100 {
			if !strings.Contains(trimmed, "───") {
				if hasCheckbox(trimmed) {
					isMulti = true
				}
				rawOptions = append([]string{trimmed}, rawOptions...)
			}
			continue
		}

		// Something else — stop scanning.
		if len(rawOptions) > 0 {
			break
		}
	}

	if question == "" || len(rawOptions) == 0 {
		return nil
	}

	// Build parsed result: strip checkbox prefixes, find Submit/Type indices.
	parsed := &parsedQuestion{
		Question:      question,
		IsMultiSelect: isMulti,
		SubmitIndex:   -1,
		TypeIndex:     -1,
	}
	for i, raw := range rawOptions {
		label := raw
		if isMulti {
			label = stripCheckboxPrefix(label)
		}
		label = strings.TrimSpace(label)

		lower := strings.ToLower(label)
		if lower == "submit" {
			parsed.SubmitIndex = i
		}
		if lower == "other" || strings.Contains(lower, "type something") || strings.Contains(lower, "type custom") {
			parsed.HasTypeOption = true
			parsed.TypeIndex = i
		}

		parsed.Options = append(parsed.Options, label)
	}

	return parsed
}

// subagentInfo describes a detected subagent running inside Claude Code.
type subagentInfo struct {
	Type   string `json:"type"`
	Status string `json:"status"`
}

// parseSubagentsFromCapture scans the capture for Claude Code subagent markers.
// Claude Code shows: ⎿  Agent (Explore) ...
func parseSubagentsFromCapture(capture string) []subagentInfo {
	lines := strings.Split(capture, "\n")
	var agents []subagentInfo
	seen := map[string]bool{}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Look for "⎿" followed by "Agent (" pattern.
		idx := strings.Index(trimmed, "Agent (")
		if idx < 0 {
			continue
		}
		// Must start with ⎿ (the Claude Code subagent marker).
		prefix := strings.TrimSpace(trimmed[:idx])
		if prefix != "⎿" && prefix != "" {
			continue
		}
		rest := trimmed[idx+len("Agent ("):]
		endIdx := strings.Index(rest, ")")
		if endIdx < 0 {
			continue
		}
		agentType := rest[:endIdx]
		if seen[agentType] {
			continue
		}
		seen[agentType] = true
		agents = append(agents, subagentInfo{Type: agentType, Status: "running"})
	}
	return agents
}

// --- Shared Utilities ---

// isStatusBarLine checks if a trimmed line is a Claude Code status bar element
// (not substantive content). These appear below the ❯ prompt.
func isStatusBarLine(trimmed string) bool {
	return strings.Contains(trimmed, "───") ||
		strings.Contains(trimmed, "⏵") ||
		strings.Contains(trimmed, "⏰") ||
		strings.Contains(trimmed, "🤖") ||
		strings.Contains(trimmed, "📝") ||
		strings.Contains(trimmed, "💻") ||
		strings.Contains(trimmed, "📁") ||
		strings.Contains(trimmed, "🆔")
}

// isPromptStuck checks if a capture shows text in the ❯ prompt that hasn't been
// submitted (Enter wasn't received). This is indicated by ❯ <text> being the last
// non-status-bar line — meaning there's no Claude response content below it.
func isPromptStuck(capture string) bool {
	lines := strings.Split(capture, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || isStatusBarLine(trimmed) {
			continue
		}
		// If the last substantive line is ❯ with text, the prompt is stuck.
		if strings.HasPrefix(trimmed, "❯ ") || strings.HasPrefix(trimmed, "❯\u00a0") {
			afterPrompt := strings.TrimSpace(trimmed[len("❯")+1:])
			return len(afterPrompt) > 2
		}
		return false
	}
	return false
}

// stripStatusBars removes status bar lines and separators from capture text,
// returning only substantive content. Used for stability comparison so that
// status bar updates (timestamps, CPU, etc.) don't prevent completion detection.
func stripStatusBars(capture string) string {
	lines := strings.Split(capture, "\n")
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			result = append(result, line)
			continue
		}
		if !isStatusBarLine(trimmed) {
			result = append(result, line)
		}
	}
	return strings.Join(result, "\n")
}

// isHintOrChipLine checks if a line is a Claude Code hint or chip bar from multi-select UI.
// Hint lines: "↑/↓ navigate", "Space toggles", "Enter to select", "Esc to cancel"
// Chip bars: lines containing both "←" and "→" (tag navigation indicators).
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

// hasCheckbox checks if a string contains a checkbox pattern from multi-select UI.
func hasCheckbox(s string) bool {
	return strings.Contains(s, "[ ]") || strings.Contains(s, "[x]") ||
		strings.Contains(s, "[X]") || strings.Contains(s, "[✔]")
}

// stripCheckboxPrefix removes leading "N. [ ] " or "[ ] " patterns from an option label.
func stripCheckboxPrefix(s string) string {
	t := s
	// Strip leading number+dot: "1. "
	if idx := strings.Index(t, ". "); idx >= 0 && idx <= 3 {
		allDigits := true
		for _, c := range t[:idx] {
			if c < '0' || c > '9' {
				allDigits = false
				break
			}
		}
		if allDigits {
			t = t[idx+2:]
		}
	}
	// Strip checkbox: "[ ] ", "[x] ", "[X] ", "[✔] "
	for _, prefix := range []string{"[ ] ", "[x] ", "[X] ", "[✔] "} {
		if strings.HasPrefix(t, prefix) {
			return t[len(prefix):]
		}
	}
	return s
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
