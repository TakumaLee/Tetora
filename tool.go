package main

// tool.go wires internal/tools to the root package via type aliases.
// All tool types and registry logic live in internal/tools/registry.go.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	dtypes "tetora/internal/dispatch"
	"tetora/internal/log"
	"tetora/internal/provider"
	"tetora/internal/tools"
	"tetora/internal/trace"
)

// --- Type Aliases (canonical definitions in internal/tools) ---

type ToolDef = tools.ToolDef
type ToolCall = provider.ToolCall
type ToolHandler = tools.Handler
type ToolResult = tools.Result
type ToolRegistry = tools.Registry

// --- Forwarding Functions ---

func NewToolRegistry(cfg *Config) *ToolRegistry {
	r := tools.NewRegistry()
	registerBuiltins(r, cfg)
	return r
}

// newEmptyRegistry creates an empty registry (no builtins). Used in tests.
func newEmptyRegistry() *ToolRegistry { return tools.NewRegistry() }

var ToolsForProfile = tools.ForProfile
var ToolsForComplexity = tools.ForComplexity

// --- Built-in Tools ---

func registerBuiltins(r *ToolRegistry, cfg *Config) {
	enabled := func(name string) bool {
		if cfg.Tools.Builtin == nil {
			return true
		}
		e, ok := cfg.Tools.Builtin[name]
		return !ok || e
	}
	tools.RegisterCoreTools(r, cfg, enabled, buildCoreDeps())
	tools.RegisterMemoryTools(r, cfg, enabled, buildMemoryDeps())
	tools.RegisterLifeTools(r, cfg, enabled, buildLifeDeps())
	tools.RegisterIntegrationTools(r, cfg, enabled, buildIntegrationDeps(cfg))
	tools.RegisterDailyTools(r, cfg, enabled, buildDailyDeps(cfg))
	registerAdminTools(r, cfg, enabled)
	tools.RegisterTaskboardTools(r, cfg, enabled, buildTaskboardDeps(cfg))
	tools.RegisterImageGenTools(r, cfg, enabled, buildImageGenDeps())
}

// registerAdminTools registers admin/ops tools (backup, export, health,
// skills, sentori, create_skill).
func registerAdminTools(r *ToolRegistry, cfg *Config, enabled func(string) bool) {
	if enabled("create_skill") {
		r.Register(&ToolDef{
			Name:        "create_skill",
			Description: "Create a new reusable skill (shell script or Python script) that can be used in future tasks. The skill will need approval before it can execute unless autoApprove is enabled.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"name": {"type": "string", "description": "Skill name (alphanumeric and hyphens only, max 64 chars)"},
					"description": {"type": "string", "description": "What the skill does"},
					"script": {"type": "string", "description": "The script content (bash or python)"},
					"language": {"type": "string", "enum": ["bash", "python"], "description": "Script language (default: bash)"},
					"matcher": {"type": "object", "properties": {"agents": {"type": "array", "items": {"type": "string"}}, "keywords": {"type": "array", "items": {"type": "string"}}}, "description": "Conditions for auto-injecting this skill"}
				},
				"required": ["name", "description", "script"]
			}`),
			Handler:     createSkillToolHandler,
			Builtin:     true,
			RequireAuth: true,
		})
	}

	if enabled("backup_now") {
		r.Register(&ToolDef{
			Name:        "backup_now",
			Description: "Trigger an immediate backup of the database",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {}
			}`),
			Handler:     toolBackupNow,
			Builtin:     true,
			RequireAuth: true,
		})
	}
	if enabled("export_data") {
		r.Register(&ToolDef{
			Name:        "export_data",
			Description: "Export user data as a ZIP archive (GDPR compliance)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"userId": {"type": "string", "description": "User ID to filter export (optional)"}
				}
			}`),
			Handler:     toolExportData,
			Builtin:     true,
			RequireAuth: true,
		})
	}
	if enabled("system_health") {
		r.Register(&ToolDef{
			Name:        "system_health",
			Description: "Get the overall system health status including database, channels, and integrations",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {}
			}`),
			Handler: toolSystemHealth,
			Builtin: true,
		})
	}

	if enabled("sentori_scan") {
		r.Register(&ToolDef{
			Name:        "sentori_scan",
			Description: "Security scan a skill script for dangerous patterns (exec, path access, exfiltration, env reads, listeners)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"name": {"type": "string", "description": "Skill name to scan (from store)"},
					"content": {"type": "string", "description": "Raw script content to scan (alternative to name)"}
				}
			}`),
			Handler: toolSentoriScan,
			Builtin: true,
		})
	}
	if enabled("skill_install") {
		r.Register(&ToolDef{
			Name:        "skill_install",
			Description: "Download and install a skill from a URL. Runs Sentori security scan before installation.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"url": {"type": "string", "description": "URL to download skill from"},
					"auto_approve": {"type": "boolean", "description": "Auto-approve safe skills (default false)"}
				},
				"required": ["url"]
			}`),
			Handler:     toolSkillInstall,
			Builtin:     true,
			RequireAuth: true,
		})
	}
	if enabled("skill_search") {
		r.Register(&ToolDef{
			Name:        "skill_search",
			Description: "Search the skill registry for installable skills by keyword",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Search query"}
				},
				"required": ["query"]
			}`),
			Handler: toolSkillSearch,
			Builtin: true,
		})
	}
}

// --- Memory Tool Compatibility Wrappers (merged from tool_memory.go) ---
// Registration moved to internal/tools/memory.go.
// Wrappers below are used by tests that call handlers directly.

var memoryDepsForTest = tools.MemoryDeps{
	GetMemory: getMemory,
	SetMemory: func(cfg *Config, role, key, value string) error {
		return setMemory(cfg, role, key, value)
	},
	DeleteMemory: deleteMemory,
	SearchMemory: func(cfg *Config, role, query string) ([]tools.MemoryEntry, error) {
		entries, err := searchMemoryFS(cfg, role, query)
		if err != nil {
			return nil, err
		}
		result := make([]tools.MemoryEntry, len(entries))
		for i, e := range entries {
			result[i] = tools.MemoryEntry{Key: e.Key, Value: e.Value}
		}
		return result, nil
	},
}

func toolMemorySearch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	h := tools.MakeMemorySearchHandler(memoryDepsForTest)
	return h(ctx, cfg, input)
}

func toolMemoryGet(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	h := tools.MakeMemoryGetHandler(memoryDepsForTest)
	return h(ctx, cfg, input)
}

func toolKnowledgeSearch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	h := tools.MakeKnowledgeSearchHandler()
	return h(ctx, cfg, input)
}

// --- ImageGen Compatibility Aliases (merged from tool_imagegen.go) ---
// Handler implementations are in internal/tools/imagegen.go.

// Type alias for backwards compat (used by App struct, tests).
type imageGenLimiter = tools.ImageGenLimiter

// globalImageGenLimiter is the default limiter instance.
var globalImageGenLimiter = &tools.ImageGenLimiter{}

// estimateImageCost forwards to tools.EstimateImageCost for test compat.
var estimateImageCost = tools.EstimateImageCost

// imageGenBaseURL forwards to tools.ImageGenBaseURL for test overrides.
var imageGenBaseURL = tools.ImageGenBaseURL

// toolImageGenerate wraps internal/tools image handler for test compat.
func toolImageGenerate(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	// Set the shared base URL before calling.
	tools.ImageGenBaseURL = imageGenBaseURL
	deps := tools.ImageGenDeps{
		GetLimiter: func(ctx context.Context) *tools.ImageGenLimiter {
			return globalImageGenLimiter
		},
	}
	handler := tools.MakeImageGenerateHandler(deps)
	return handler(ctx, cfg, input)
}

// toolImageGenerateStatus wraps internal/tools status handler for test compat.
func toolImageGenerateStatus(ctx context.Context, cfg *Config, _ json.RawMessage) (string, error) {
	tools.ImageGenBaseURL = imageGenBaseURL
	deps := tools.ImageGenDeps{
		GetLimiter: func(ctx context.Context) *tools.ImageGenLimiter {
			return globalImageGenLimiter
		},
	}
	handler := tools.MakeImageGenerateStatusHandler(deps)
	return handler(ctx, cfg, nil)
}

// --- Type Aliases (canonical definitions in internal/dispatch) ---

type ApprovalGate = dtypes.ApprovalGate
type ApprovalRequest = dtypes.ApprovalRequest

// --- Tool Profiles ---


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

// --- Per-Agent Tool Policy ---


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

// getAgentToolPolicy returns the tool policy for an agent.
func getAgentToolPolicy(cfg *Config, agentName string) AgentToolPolicy {
	if agentName == "" {
		return AgentToolPolicy{}
	}

	if rc, ok := cfg.Agents[agentName]; ok {
		return rc.ToolPolicy
	}

	return AgentToolPolicy{}
}

// resolveAllowedTools returns the set of tool names allowed for an agent.
// Resolution order: profile → +allow → -deny
func resolveAllowedTools(cfg *Config, agentName string) map[string]bool {
	policy := getAgentToolPolicy(cfg, agentName)
	profile := getProfile(cfg, policy.Profile)

	allowed := make(map[string]bool)

	// Start with profile.
	for _, toolName := range profile.Allow {
		if toolName == "*" {
			// All registered tools.
			for _, td := range cfg.Runtime.ToolRegistry.(*ToolRegistry).List() {
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

	// Add extra allows from agent policy.
	for _, toolName := range policy.Allow {
		allowed[toolName] = true
	}

	// Remove agent-level denies.
	for _, toolName := range policy.Deny {
		delete(allowed, toolName)
	}

	return allowed
}

// isToolAllowed checks if a tool is allowed for an agent.
func isToolAllowed(cfg *Config, agentName, toolName string) bool {
	allowed := resolveAllowedTools(cfg, agentName)
	return allowed[toolName]
}

// --- Trust-Level Tool Filtering ---

// getToolTrustLevel returns the effective trust level for a tool call.
// Priority: tool-specific override → agent trust level → RequireAuth check → default "auto"
func getToolTrustLevel(cfg *Config, agentName, toolName string) string {
	// Check tool-specific trust override in config.
	if cfg.Tools.TrustOverride != nil {
		if level, ok := cfg.Tools.TrustOverride[toolName]; ok && isValidTrustLevel(level) {
			return level
		}
	}

	// Check agent trust level.
	if rc, ok := cfg.Agents[agentName]; ok {
		if rc.TrustLevel != "" && isValidTrustLevel(rc.TrustLevel) {
			return rc.TrustLevel
		}
	}

	// If tool has RequireAuth flag and no explicit trust config, default to "suggest" instead of "auto".
	if cfg.Runtime.ToolRegistry != nil {
		if td, ok := cfg.Runtime.ToolRegistry.(*ToolRegistry).Get(toolName); ok && td.RequireAuth {
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
func filterToolCall(cfg *Config, agentName string, call ToolCall) (*ToolResult, bool) {
	trustLevel := getToolTrustLevel(cfg, agentName, call.Name)

	switch trustLevel {
	case TrustObserve:
		// Log but don't execute.
		log.Info("tool call observed (not executed)", "tool", call.Name, "agent", agentName)
		return &ToolResult{
			ToolUseID: call.ID,
			Content:   fmt.Sprintf("[OBSERVE MODE: tool %s would execute with input: %s]", call.Name, truncateJSON(call.Input, 100)),
			IsError:   false,
		}, false

	case TrustSuggest:
		// Log and return approval-needed message.
		log.Info("tool call requires approval", "tool", call.Name, "agent", agentName)
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
func validateToolPolicy(cfg *Config, policy AgentToolPolicy) error {
	// Check if profile exists.
	if policy.Profile != "" {
		profile := getProfile(cfg, policy.Profile)
		if profile.Name == "" {
			return fmt.Errorf("unknown tool profile: %s", policy.Profile)
		}
	}

	// Check if tools in allow/deny lists exist.
	allTools := make(map[string]bool)
	for _, td := range cfg.Runtime.ToolRegistry.(*ToolRegistry).List() {
		allTools[td.Name] = true
	}

	for _, toolName := range policy.Allow {
		if !allTools[toolName] {
			log.Warn("tool policy references unknown tool", "tool", toolName)
		}
	}

	for _, toolName := range policy.Deny {
		if !allTools[toolName] {
			log.Warn("tool policy references unknown tool", "tool", toolName)
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
		ID:      trace.NewID("gate"),
		Tool:    tc.Name,
		Input:   tc.Input,
		Summary: summarizeToolCall(tc),
		TaskID:  task.ID,
		Role:    task.Agent,
	}

	return task.ApprovalGate.RequestApproval(gateCtx, req)
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

// getToolPolicySummary returns a human-readable summary of an agent's tool policy.
func getToolPolicySummary(cfg *Config, agentName string) string {
	policy := getAgentToolPolicy(cfg, agentName)
	allowed := resolveAllowedTools(cfg, agentName)

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
