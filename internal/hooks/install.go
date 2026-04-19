// Package hooks manages Tetora hook entries in ~/.claude/settings.json.
// See: https://code.claude.com/docs/en/hooks
package hooks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Settings represents the structure of ~/.claude/settings.json.
type Settings struct {
	Raw map[string]json.RawMessage
}

// LoadSettings reads ~/.claude/settings.json and returns the parsed settings
// along with the file path. If the file does not exist, empty settings are returned.
func LoadSettings() (*Settings, string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, "", fmt.Errorf("get home dir: %w", err)
	}

	path := filepath.Join(homeDir, ".claude", "settings.json")

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Settings{Raw: make(map[string]json.RawMessage)}, path, nil
		}
		return nil, "", fmt.Errorf("read settings: %w", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, "", fmt.Errorf("parse settings: %w", err)
	}

	return &Settings{Raw: raw}, path, nil
}

// Save writes the settings to the given path.
func (s *Settings) Save(path string) error {
	data, err := json.MarshalIndent(s.Raw, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}

	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// HookCommand returns the curl command that Claude Code hooks will execute.
func HookCommand(listenAddr string) string {
	if listenAddr == "" {
		listenAddr = ":8991"
	}
	addr := listenAddr
	if strings.HasPrefix(addr, ":") {
		addr = "localhost" + addr
	}

	return fmt.Sprintf(
		`curl -sf -X POST http://%s/api/hooks/event -H 'Content-Type:application/json' -d @/dev/stdin 2>/dev/null || true`,
		addr,
	)
}

// tetoraHookMarker is the substring identifying a Tetora event hook command.
const tetoraHookMarker = "/api/hooks/event"
const tetoraGateMarker = "/api/hooks/plan-gate"
const tetoraAskUserDenyMarker = "/api/hooks/deny-ask-user"
const tetoraPreBashGuardMarker = "/api/hooks/pre-bash"

// IsTetoraHook returns true if the command belongs to any Tetora hook type.
func IsTetoraHook(cmd string) bool {
	return strings.Contains(cmd, tetoraHookMarker) ||
		IsTetoraGateHook(cmd) ||
		IsTetoraAskUserDenyHook(cmd) ||
		IsTetoraPreBashGuardHook(cmd)
}

// IsTetoraGateHook returns true if the command is the plan-gate hook.
func IsTetoraGateHook(cmd string) bool {
	return strings.Contains(cmd, tetoraGateMarker)
}

// IsTetoraAskUserDenyHook returns true if the command is the ask-user deny hook.
func IsTetoraAskUserDenyHook(cmd string) bool {
	return strings.Contains(cmd, tetoraAskUserDenyMarker)
}

// IsTetoraPreBashGuardHook returns true if the command is the pre-bash guard hook.
func IsTetoraPreBashGuardHook(cmd string) bool {
	return strings.Contains(cmd, tetoraPreBashGuardMarker)
}

// --- New hook format (Claude Code 2025+) ---
// Each event has an array of HookRule objects. Each HookRule has:
//   - Matcher: regex string filtering when hooks fire (optional)
//   - Hooks: array of HookHandler objects that run when matched

// HookHandler represents a single hook command within a HookRule.
type HookHandler struct {
	Type    string `json:"type"`              // "command", "http", "prompt", "agent"
	Command string `json:"command,omitempty"` // for type="command"
	Timeout int    `json:"timeout,omitempty"` // max seconds to wait
}

// HookRule represents a matcher group with one or more hook handlers.
type HookRule struct {
	Matcher string        `json:"matcher,omitempty"` // regex filter (tool name, etc.)
	Hooks   []HookHandler `json:"hooks"`
}

// HooksConfig represents the hooks section of Claude Code settings.
type HooksConfig struct {
	PreToolUse   []HookRule `json:"PreToolUse,omitempty"`
	PostToolUse  []HookRule `json:"PostToolUse,omitempty"`
	Stop         []HookRule `json:"Stop,omitempty"`
	Notification []HookRule `json:"Notification,omitempty"`
}

// PlanGateHookCommand returns the curl command for the PreToolUse:ExitPlanMode gate.
// Note: no `|| true` — response must be read by Claude Code.
func PlanGateHookCommand(listenAddr string) string {
	addr := listenAddr
	if strings.HasPrefix(addr, ":") {
		addr = "localhost" + addr
	}
	return fmt.Sprintf(
		`curl -sf --max-time 300 -X POST http://%s/api/hooks/plan-gate -H 'Content-Type:application/json' -d @/dev/stdin 2>/dev/null`,
		addr,
	)
}

// AskUserDenyCommand returns a short script that denies AskUserQuestion and tells Claude
// to use the tetora_ask_user MCP tool instead.
// The trailing comment contains the marker for IsTetoraAskUserDenyHook() detection.
func AskUserDenyCommand() string {
	return `echo '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","reason":"Use tetora_ask_user MCP tool to ask the user remotely via Discord."}}' # /api/hooks/deny-ask-user`
}

// PreBashGuardHookCommand returns the curl command for the PreToolUse:Bash
// self-preservation guard. On curl failure (tetora down, network issue) the
// command prints the "allow" JSON so the user's shell workflow is not broken
// when the daemon is offline. Timeout kept short so a wedged daemon cannot
// stall every Bash call indefinitely.
func PreBashGuardHookCommand(listenAddr string) string {
	addr := listenAddr
	if strings.HasPrefix(addr, ":") {
		addr = "localhost" + addr
	}
	const allowJSON = `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}}`
	return fmt.Sprintf(
		`curl -sf --max-time 3 -X POST http://%s/api/hooks/pre-bash -H 'Content-Type:application/json' -d @/dev/stdin 2>/dev/null || echo '%s'`,
		addr, allowJSON,
	)
}

// IsTetoraRule checks if a HookRule contains any Tetora hook commands.
func IsTetoraRule(rule HookRule) bool {
	for _, h := range rule.Hooks {
		if IsTetoraHook(h.Command) {
			return true
		}
	}
	return false
}

// IsTetoraGateRule checks if a HookRule contains a plan-gate command.
func IsTetoraGateRule(rule HookRule) bool {
	for _, h := range rule.Hooks {
		if IsTetoraGateHook(h.Command) {
			return true
		}
	}
	return false
}

// IsTetoraAskUserDenyRule checks if a HookRule contains an ask-user deny command.
func IsTetoraAskUserDenyRule(rule HookRule) bool {
	for _, h := range rule.Hooks {
		if IsTetoraAskUserDenyHook(h.Command) {
			return true
		}
	}
	return false
}

// IsTetoraPreBashGuardRule checks if a HookRule contains a pre-bash guard command.
func IsTetoraPreBashGuardRule(rule HookRule) bool {
	for _, h := range rule.Hooks {
		if IsTetoraPreBashGuardHook(h.Command) {
			return true
		}
	}
	return false
}

// Install adds Tetora hook entries to Claude Code settings.
func Install(listenAddr string) error {
	settings, path, err := LoadSettings()
	if err != nil {
		return err
	}

	cmd := HookCommand(listenAddr)

	var hooks HooksConfig
	if raw, ok := settings.Raw["hooks"]; ok {
		json.Unmarshal(raw, &hooks)
	}

	hooks.PostToolUse = addTetoraRule(hooks.PostToolUse, cmd, "")
	hooks.Stop = addTetoraRule(hooks.Stop, cmd, "")
	hooks.Notification = addTetoraRule(hooks.Notification, cmd, "")

	gateCmd := PlanGateHookCommand(listenAddr)
	hooks.PreToolUse = addTetoraGateRule(hooks.PreToolUse, gateCmd, "ExitPlanMode", 600)

	denyCmd := AskUserDenyCommand()
	hooks.PreToolUse = addTetoraAskUserDenyRule(hooks.PreToolUse, denyCmd, "AskUserQuestion")

	bashGuardCmd := PreBashGuardHookCommand(listenAddr)
	hooks.PreToolUse = addTetoraPreBashGuardRule(hooks.PreToolUse, bashGuardCmd, "Bash", 5)

	hooksData, err := json.Marshal(hooks)
	if err != nil {
		return fmt.Errorf("marshal hooks: %w", err)
	}
	settings.Raw["hooks"] = hooksData

	if err := settings.Save(path); err != nil {
		return err
	}

	fmt.Printf("Hooks installed in %s\n", path)
	fmt.Printf("Hook commands:\n")
	fmt.Printf("  event:      %s\n", cmd)
	fmt.Printf("  plan-gate:  %s\n", gateCmd)
	fmt.Printf("  deny-ask:   %s\n", denyCmd)
	fmt.Printf("  bash-guard: %s\n", bashGuardCmd)
	return nil
}

// addTetoraRule adds a Tetora hook rule, replacing any existing Tetora rule.
func addTetoraRule(rules []HookRule, cmd, matcher string) []HookRule {
	filtered := make([]HookRule, 0, len(rules))
	for _, r := range rules {
		if len(r.Hooks) == 0 || IsTetoraRule(r) {
			continue
		}
		filtered = append(filtered, r)
	}

	rule := HookRule{
		Matcher: matcher,
		Hooks: []HookHandler{
			{Type: "command", Command: cmd},
		},
	}
	return append(filtered, rule)
}

// addTetoraGateRule adds a plan-gate PreToolUse rule, replacing any existing one.
func addTetoraGateRule(rules []HookRule, cmd, matcher string, timeout int) []HookRule {
	filtered := make([]HookRule, 0, len(rules))
	for _, r := range rules {
		if len(r.Hooks) == 0 || IsTetoraGateRule(r) {
			continue
		}
		filtered = append(filtered, r)
	}
	rule := HookRule{
		Matcher: matcher,
		Hooks: []HookHandler{
			{Type: "command", Command: cmd, Timeout: timeout},
		},
	}
	return append(filtered, rule)
}

// addTetoraAskUserDenyRule adds a deny rule for AskUserQuestion, replacing any existing one.
func addTetoraAskUserDenyRule(rules []HookRule, cmd, matcher string) []HookRule {
	filtered := make([]HookRule, 0, len(rules))
	for _, r := range rules {
		if len(r.Hooks) == 0 || IsTetoraAskUserDenyRule(r) {
			continue
		}
		filtered = append(filtered, r)
	}
	rule := HookRule{
		Matcher: matcher,
		Hooks: []HookHandler{
			{Type: "command", Command: cmd, Timeout: 10},
		},
	}
	return append(filtered, rule)
}

// addTetoraPreBashGuardRule adds a PreToolUse:Bash self-preservation rule,
// replacing any existing one.
func addTetoraPreBashGuardRule(rules []HookRule, cmd, matcher string, timeout int) []HookRule {
	filtered := make([]HookRule, 0, len(rules))
	for _, r := range rules {
		if len(r.Hooks) == 0 || IsTetoraPreBashGuardRule(r) {
			continue
		}
		filtered = append(filtered, r)
	}
	rule := HookRule{
		Matcher: matcher,
		Hooks: []HookHandler{
			{Type: "command", Command: cmd, Timeout: timeout},
		},
	}
	return append(filtered, rule)
}

// Remove removes all Tetora hook entries from Claude Code settings.
func Remove() error {
	settings, path, err := LoadSettings()
	if err != nil {
		return err
	}

	raw, ok := settings.Raw["hooks"]
	if !ok {
		fmt.Println("No hooks configured.")
		return nil
	}

	var hooks HooksConfig
	if err := json.Unmarshal(raw, &hooks); err != nil {
		return fmt.Errorf("parse hooks: %w", err)
	}

	removed := 0
	hooks.PreToolUse, removed = removeTetoraRules(hooks.PreToolUse)
	hooks.PostToolUse, removed = removeTetoraRulesCount(hooks.PostToolUse, removed)
	hooks.Stop, removed = removeTetoraRulesCount(hooks.Stop, removed)
	hooks.Notification, removed = removeTetoraRulesCount(hooks.Notification, removed)

	if removed == 0 {
		fmt.Println("No Tetora hooks found.")
		return nil
	}

	hooksData, _ := json.Marshal(hooks)
	settings.Raw["hooks"] = hooksData

	if err := settings.Save(path); err != nil {
		return err
	}

	fmt.Printf("Removed %d Tetora hook(s) from %s\n", removed, path)
	return nil
}

func removeTetoraRules(rules []HookRule) ([]HookRule, int) {
	filtered := make([]HookRule, 0, len(rules))
	removed := 0
	for _, r := range rules {
		if IsTetoraRule(r) {
			removed++
		} else {
			filtered = append(filtered, r)
		}
	}
	return filtered, removed
}

func removeTetoraRulesCount(rules []HookRule, prevRemoved int) ([]HookRule, int) {
	result, r := removeTetoraRules(rules)
	return result, prevRemoved + r
}

// ShowStatus displays the current state of Tetora hooks.
func ShowStatus() error {
	settings, path, err := LoadSettings()
	if err != nil {
		return err
	}

	fmt.Printf("Settings file: %s\n\n", path)

	raw, ok := settings.Raw["hooks"]
	if !ok {
		fmt.Println("No hooks configured.")
		return nil
	}

	var hooks HooksConfig
	if err := json.Unmarshal(raw, &hooks); err != nil {
		return fmt.Errorf("parse hooks: %w", err)
	}

	found := false
	checkRuleType := func(name string, rules []HookRule) {
		for _, r := range rules {
			if IsTetoraRule(r) {
				for _, h := range r.Hooks {
					fmt.Printf("  %s: %s\n", name, h.Command)
				}
				if r.Matcher != "" {
					fmt.Printf("    matcher: %s\n", r.Matcher)
				}
				found = true
			}
		}
	}

	fmt.Println("Tetora hooks:")
	checkRuleType("PreToolUse", hooks.PreToolUse)
	checkRuleType("PostToolUse", hooks.PostToolUse)
	checkRuleType("Stop", hooks.Stop)
	checkRuleType("Notification", hooks.Notification)

	if !found {
		fmt.Println("  (none installed)")
	}

	return nil
}
