package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// --- Tool Profiles ---

// ToolProfile defines a preset collection of allowed tools.
type ToolProfile struct {
	Name  string   `json:"name"`
	Allow []string `json:"allow"`          // tool names, "*" = all
	Deny  []string `json:"deny,omitempty"` // denied tools
}

// Built-in profiles for common use cases.
var builtinProfiles = map[string]ToolProfile{
	"minimal": {
		Name: "minimal",
		Allow: []string{
			"memory_search",
			"memory_get",
			"knowledge_search",
		},
	},
	"standard": {
		Name: "standard",
		Allow: []string{
			"read",
			"write",
			"edit",
			"exec",
			"memory_search",
			"memory_get",
			"knowledge_search",
			"web_fetch",
			"session_list",
		},
	},
	"full": {
		Name:  "full",
		Allow: []string{"*"},
	},
}

// --- Per-Role Tool Policy ---

// RoleToolPolicy configures tool access for a specific role.
type RoleToolPolicy struct {
	Profile      string   `json:"profile,omitempty"`      // profile name (minimal, standard, full, or custom)
	Allow        []string `json:"allow,omitempty"`        // additional allowed tools
	Deny         []string `json:"deny,omitempty"`         // explicitly denied tools
	Sandbox      string   `json:"sandbox,omitempty"`      // "required" | "optional" | "never" (default: "never") --- P13.2: Sandbox Plugin ---
	SandboxImage string   `json:"sandboxImage,omitempty"` // custom Docker image per role --- P13.2: Sandbox Plugin ---
}

// --- Tool Trust Override ---

// ToolTrustOverride allows per-tool trust level overrides.
type ToolTrustOverride struct {
	TrustOverride map[string]string `json:"trustOverride,omitempty"` // tool name → trust level
}

// --- Tool Policy Resolution ---

// getProfile returns the tool profile by name (built-in or custom).
func getProfile(cfg *Config, profileName string) ToolProfile {
	// Default to "standard" if not specified.
	if profileName == "" {
		profileName = "standard"
	}

	// Check built-in profiles.
	if profile, ok := builtinProfiles[profileName]; ok {
		return profile
	}

	// Check custom profiles in config.
	if cfg.Tools.Profiles != nil {
		if profile, ok := cfg.Tools.Profiles[profileName]; ok {
			return profile
		}
	}

	// Fallback to standard.
	return builtinProfiles["standard"]
}

// getRoleToolPolicy returns the tool policy for a role.
func getRoleToolPolicy(cfg *Config, roleName string) RoleToolPolicy {
	if roleName == "" {
		return RoleToolPolicy{}
	}

	if rc, ok := cfg.Roles[roleName]; ok {
		return rc.ToolPolicy
	}

	return RoleToolPolicy{}
}

// resolveAllowedTools returns the set of tool names allowed for a role.
// Resolution order: profile → +allow → -deny
func resolveAllowedTools(cfg *Config, roleName string) map[string]bool {
	policy := getRoleToolPolicy(cfg, roleName)
	profile := getProfile(cfg, policy.Profile)

	allowed := make(map[string]bool)

	// Start with profile.
	for _, toolName := range profile.Allow {
		if toolName == "*" {
			// All registered tools.
			for _, td := range cfg.toolRegistry.List() {
				allowed[td.Name] = true
			}
			break
		}
		allowed[toolName] = true
	}

	// Remove profile denies.
	for _, toolName := range profile.Deny {
		delete(allowed, toolName)
	}

	// Add extra allows from role policy.
	for _, toolName := range policy.Allow {
		allowed[toolName] = true
	}

	// Remove role-level denies.
	for _, toolName := range policy.Deny {
		delete(allowed, toolName)
	}

	return allowed
}

// isToolAllowed checks if a tool is allowed for a role.
func isToolAllowed(cfg *Config, roleName, toolName string) bool {
	allowed := resolveAllowedTools(cfg, roleName)
	return allowed[toolName]
}

// --- Trust-Level Tool Filtering ---

// getToolTrustLevel returns the effective trust level for a tool call.
// Priority: tool-specific override → role trust level → RequireAuth check → default "auto"
func getToolTrustLevel(cfg *Config, roleName, toolName string) string {
	// Check tool-specific trust override in config.
	if cfg.Tools.TrustOverride != nil {
		if level, ok := cfg.Tools.TrustOverride[toolName]; ok && isValidTrustLevel(level) {
			return level
		}
	}

	// Check role trust level.
	if rc, ok := cfg.Roles[roleName]; ok {
		if rc.TrustLevel != "" && isValidTrustLevel(rc.TrustLevel) {
			return rc.TrustLevel
		}
	}

	// If tool has RequireAuth flag and no explicit trust config, default to "suggest" instead of "auto".
	if cfg.toolRegistry != nil {
		if td, ok := cfg.toolRegistry.Get(toolName); ok && td.RequireAuth {
			return TrustSuggest
		}
	}

	// Default to auto.
	return TrustAuto
}

// filterToolCall applies trust-level filtering before tool execution.
// Returns (result, shouldExecute).
// - observe: return mock result, don't execute
// - suggest: return approval-needed result, don't execute
// - auto: return nil, execute normally
func filterToolCall(cfg *Config, roleName string, call ToolCall) (*ToolResult, bool) {
	trustLevel := getToolTrustLevel(cfg, roleName, call.Name)

	switch trustLevel {
	case TrustObserve:
		// Log but don't execute.
		logInfo("tool call observed (not executed)", "tool", call.Name, "role", roleName)
		return &ToolResult{
			ToolUseID: call.ID,
			Content:   fmt.Sprintf("[OBSERVE MODE: tool %s would execute with input: %s]", call.Name, truncateJSON(call.Input, 100)),
			IsError:   false,
		}, false

	case TrustSuggest:
		// Log and return approval-needed message.
		logInfo("tool call requires approval", "tool", call.Name, "role", roleName)
		return &ToolResult{
			ToolUseID: call.ID,
			Content:   fmt.Sprintf("[APPROVAL REQUIRED: tool %s with input: %s]", call.Name, truncateJSON(call.Input, 200)),
			IsError:   false,
		}, false

	case TrustAuto:
		// Execute normally.
		return nil, true

	default:
		// Invalid trust level, default to auto.
		return nil, true
	}
}

// truncateJSON truncates a JSON string for display.
func truncateJSON(data json.RawMessage, maxLen int) string {
	s := string(data)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// --- Enhanced Loop Detection ---

// LoopDetector tracks tool call history to detect loops.
type LoopDetector struct {
	history    []loopEntry
	maxHistory int // default 20
	maxRepeat  int // default 3
}

type loopEntry struct {
	Name      string
	InputHash string
	Timestamp time.Time
}

// NewLoopDetector creates a loop detector with default settings.
func NewLoopDetector() *LoopDetector {
	return &LoopDetector{
		history:    make([]loopEntry, 0, 20),
		maxHistory: 20,
		maxRepeat:  3,
	}
}

// Check returns (isLoop, message) if a loop is detected.
// A loop is when the same tool+input is called > maxRepeat times.
func (d *LoopDetector) Check(name string, input json.RawMessage) (bool, string) {
	h := sha256.Sum256(input)
	inputHash := hex.EncodeToString(h[:8])

	// Count occurrences of this tool+input signature.
	count := 0
	for _, entry := range d.history {
		if entry.Name == name && entry.InputHash == inputHash {
			count++
		}
	}

	if count >= d.maxRepeat {
		return true, fmt.Sprintf("Tool call loop detected (%s called %d times with same input). Please try a different approach.", name, count)
	}

	return false, ""
}

// Record records a tool call in the history.
func (d *LoopDetector) Record(name string, input json.RawMessage) {
	h := sha256.Sum256(input)
	inputHash := hex.EncodeToString(h[:8])

	entry := loopEntry{
		Name:      name,
		InputHash: inputHash,
		Timestamp: time.Now(),
	}

	d.history = append(d.history, entry)

	// Trim history to maxHistory.
	if len(d.history) > d.maxHistory {
		d.history = d.history[len(d.history)-d.maxHistory:]
	}
}

// Reset clears the loop detector history.
func (d *LoopDetector) Reset() {
	d.history = d.history[:0]
}

// --- Pattern Detection ---

// detectToolLoopPattern detects if there's a repeating pattern of different tools.
// Returns true if a multi-tool loop is detected (e.g., A→B→A→B→A→B).
func (d *LoopDetector) detectToolLoopPattern() (bool, string) {
	if len(d.history) < 6 {
		return false, ""
	}

	// Check for simple 2-tool alternating pattern.
	recent := d.history
	if len(recent) > 10 {
		recent = recent[len(recent)-10:]
	}

	// Look for A→B→A→B pattern (at least 3 cycles).
	for patternLen := 2; patternLen <= 4; patternLen++ {
		if d.hasRepeatingPattern(recent, patternLen) {
			toolNames := make([]string, patternLen)
			for i := 0; i < patternLen; i++ {
				toolNames[i] = recent[i].Name
			}
			pattern := strings.Join(toolNames, "→")
			return true, fmt.Sprintf("Repeating tool pattern detected (%s). Consider a different strategy.", pattern)
		}
	}

	return false, ""
}

// hasRepeatingPattern checks if the last N entries repeat a pattern of given length.
func (d *LoopDetector) hasRepeatingPattern(entries []loopEntry, patternLen int) bool {
	if len(entries) < patternLen*3 {
		return false
	}

	// Extract pattern from first N entries.
	pattern := make([]string, patternLen)
	for i := 0; i < patternLen; i++ {
		pattern[i] = entries[i].Name
	}

	// Check if pattern repeats at least 3 times.
	matches := 1
	for i := patternLen; i < len(entries); i++ {
		idx := i % patternLen
		if entries[i].Name != pattern[idx] {
			return false
		}
		if idx == patternLen-1 {
			matches++
		}
	}

	return matches >= 3
}

// --- Tool Policy Validation ---

// validateToolPolicy checks if a tool policy is valid.
func validateToolPolicy(cfg *Config, policy RoleToolPolicy) error {
	// Check if profile exists.
	if policy.Profile != "" {
		profile := getProfile(cfg, policy.Profile)
		if profile.Name == "" {
			return fmt.Errorf("unknown tool profile: %s", policy.Profile)
		}
	}

	// Check if tools in allow/deny lists exist.
	allTools := make(map[string]bool)
	for _, td := range cfg.toolRegistry.List() {
		allTools[td.Name] = true
	}

	for _, toolName := range policy.Allow {
		if !allTools[toolName] {
			logWarn("tool policy references unknown tool", "tool", toolName)
		}
	}

	for _, toolName := range policy.Deny {
		if !allTools[toolName] {
			logWarn("tool policy references unknown tool", "tool", toolName)
		}
	}

	return nil
}

// --- Tool Policy Helpers ---

// getDefaultProfile returns the default tool profile name from config.
func getDefaultProfile(cfg *Config) string {
	if cfg.Tools.DefaultProfile != "" {
		return cfg.Tools.DefaultProfile
	}
	return "standard"
}

// listAvailableProfiles returns all available profile names (built-in + custom).
func listAvailableProfiles(cfg *Config) []string {
	profiles := []string{"minimal", "standard", "full"}

	if cfg.Tools.Profiles != nil {
		for name := range cfg.Tools.Profiles {
			profiles = append(profiles, name)
		}
	}

	return profiles
}

// --- P28.0: Approval Gates ---

// ApprovalGate requests user confirmation before executing a tool.
type ApprovalGate interface {
	// RequestApproval blocks until user approves/rejects or context expires.
	// Returns true if approved, false if rejected or timed out.
	RequestApproval(ctx context.Context, req ApprovalRequest) (bool, error)
	// AutoApprove adds a tool to the runtime auto-approved list.
	AutoApprove(toolName string)
	// IsAutoApproved checks if a tool has been auto-approved.
	IsAutoApproved(toolName string) bool
}

// ApprovalRequest describes a tool call pending user approval.
type ApprovalRequest struct {
	ID      string          `json:"id"`
	Tool    string          `json:"tool"`
	Input   json.RawMessage `json:"input"`
	Summary string          `json:"summary"` // human-readable description
	TaskID  string          `json:"taskId"`
	Role    string          `json:"role"`
}

// needsApproval checks if a tool requires approval gate confirmation.
func needsApproval(cfg *Config, toolName string) bool {
	if !cfg.ApprovalGates.Enabled {
		return false
	}
	for _, t := range cfg.ApprovalGates.Tools {
		if t == toolName {
			return true
		}
	}
	return false
}

// requestToolApproval sends approval request and blocks until response.
func requestToolApproval(ctx context.Context, cfg *Config, task Task, tc ToolCall) (bool, error) {
	timeout := cfg.ApprovalGates.Timeout
	if timeout <= 0 {
		timeout = 120
	}
	gateCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	req := ApprovalRequest{
		ID:      newTraceID("gate"),
		Tool:    tc.Name,
		Input:   tc.Input,
		Summary: summarizeToolCall(tc),
		TaskID:  task.ID,
		Role:    task.Role,
	}

	return task.approvalGate.RequestApproval(gateCtx, req)
}

// summarizeToolCall creates a human-readable summary of what the tool will do.
func summarizeToolCall(tc ToolCall) string {
	var args map[string]any
	json.Unmarshal(tc.Input, &args)
	switch tc.Name {
	case "exec":
		return fmt.Sprintf("Run command: %s", jsonStr(args["command"]))
	case "write":
		return fmt.Sprintf("Write file: %s", jsonStr(args["path"]))
	case "email_send":
		return fmt.Sprintf("Send email to: %s", jsonStr(args["to"]))
	case "tweet_post":
		return fmt.Sprintf("Post tweet: %s", truncateJSON(tc.Input, 80))
	case "delete":
		return fmt.Sprintf("Delete: %s", jsonStr(args["path"]))
	default:
		return fmt.Sprintf("Execute %s with %s", tc.Name, truncateJSON(tc.Input, 100))
	}
}

// gateReason returns a human-readable reason for gate rejection.
func gateReason(err error, approved bool) string {
	if err != nil {
		return err.Error()
	}
	if !approved {
		return "rejected by user"
	}
	return "approved"
}

// getToolPolicySummary returns a human-readable summary of a role's tool policy.
func getToolPolicySummary(cfg *Config, roleName string) string {
	policy := getRoleToolPolicy(cfg, roleName)
	allowed := resolveAllowedTools(cfg, roleName)

	var parts []string

	// Profile.
	profileName := policy.Profile
	if profileName == "" {
		profileName = getDefaultProfile(cfg)
	}
	parts = append(parts, fmt.Sprintf("Profile: %s", profileName))

	// Allowed count.
	parts = append(parts, fmt.Sprintf("Allowed: %d tools", len(allowed)))

	// Extra allows.
	if len(policy.Allow) > 0 {
		parts = append(parts, fmt.Sprintf("Additional: %s", strings.Join(policy.Allow, ", ")))
	}

	// Denies.
	if len(policy.Deny) > 0 {
		parts = append(parts, fmt.Sprintf("Denied: %s", strings.Join(policy.Deny, ", ")))
	}

	return strings.Join(parts, " | ")
}
