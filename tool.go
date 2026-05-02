package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"tetora/internal/db"
	"tetora/internal/log"
	"tetora/internal/provider"
	"tetora/internal/tools"
	"tetora/internal/trace"
	"time"
	dtypes "tetora/internal/dispatch"
)

// tool.go wires internal/tools to the root package via type aliases.
// All tool types and registry logic live in internal/tools/registry.go.

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
	r.ApplyDeferredPolicy()
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
	tools.RegisterDailyTools(r, cfg, enabled, buildDailyDeps(cfg))
	registerAdminTools(r, cfg, enabled)
	tools.RegisterTaskboardTools(r, cfg, enabled, buildTaskboardDeps(cfg))
	tools.RegisterReflectionTools(r, cfg, enabled, buildReflectionDeps(cfg))
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
	if enabled("skill_load") {
		r.Register(&ToolDef{
			Name:        "skill_load",
			Description: "Load a skill's full SKILL.md content by name. Use when the skill catalog surfaces a relevant skill and you want the complete procedure before deciding whether to invoke it.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"name": {"type": "string", "description": "Skill name (e.g. \"tdd\")"}
				},
				"required": ["name"]
			}`),
			Handler: toolSkillLoad,
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

// --- Tool Handlers ---

// toolExec executes a shell command.
func toolExec(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Command string  `json:"command"`
		Workdir string  `json:"workdir"`
		Timeout float64 `json:"timeout"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Command == "" {
		return "", fmt.Errorf("command is required")
	}
	if args.Timeout <= 0 {
		args.Timeout = 60
	}

	// Validate workdir is within allowedDirs.
	if args.Workdir != "" {
		if err := validateDirs(cfg, Task{Workdir: args.Workdir}, ""); err != nil {
			return "", fmt.Errorf("workdir not allowed: %w", err)
		}
	}

	timeout := time.Duration(args.Timeout) * time.Second
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "sh", "-c", args.Command)
	if args.Workdir != "" {
		cmd.Dir = args.Workdir
	}

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return "", fmt.Errorf("command failed: %w", err)
		}
	}

	// Limit output size.
	const maxOutput = 100 * 1024 // 100KB
	out := stdout.String()
	errOut := stderr.String()
	if len(out) > maxOutput {
		out = out[:maxOutput] + "\n[truncated]"
	}
	if len(errOut) > maxOutput {
		errOut = errOut[:maxOutput] + "\n[truncated]"
	}

	result := map[string]any{
		"stdout":   out,
		"stderr":   errOut,
		"exitCode": exitCode,
	}
	b, _ := json.Marshal(result)
	return string(b), nil
}

// toolRead reads file contents.
func toolRead(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Path == "" {
		return "", fmt.Errorf("path is required")
	}

	// Check file size.
	info, err := os.Stat(args.Path)
	if err != nil {
		return "", fmt.Errorf("stat file: %w", err)
	}
	const maxSize = 1024 * 1024 // 1MB
	if info.Size() > maxSize {
		return "", fmt.Errorf("file too large (max 1MB)")
	}

	data, err := os.ReadFile(args.Path)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	if args.Offset > 0 {
		if args.Offset >= len(lines) {
			return "", nil
		}
		lines = lines[args.Offset:]
	}
	if args.Limit > 0 && args.Limit < len(lines) {
		lines = lines[:args.Limit]
	}

	return strings.Join(lines, "\n"), nil
}

// toolWrite writes content to a file.
func toolWrite(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Path == "" {
		return "", fmt.Errorf("path is required")
	}

	// Validate path is within allowedDirs.
	if err := validateDirs(cfg, Task{Workdir: filepath.Dir(args.Path)}, ""); err != nil {
		return "", fmt.Errorf("path not allowed: %w", err)
	}

	// Ensure parent directory exists.
	dir := filepath.Dir(args.Path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create directory: %w", err)
	}

	if err := os.WriteFile(args.Path, []byte(args.Content), 0o644); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}

	return fmt.Sprintf("wrote %d bytes to %s", len(args.Content), args.Path), nil
}

// toolEdit performs string replacement in a file.
func toolEdit(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Path      string `json:"path"`
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Path == "" || args.OldString == "" {
		return "", fmt.Errorf("path and old_string are required")
	}

	// Validate path is within allowedDirs.
	if err := validateDirs(cfg, Task{Workdir: filepath.Dir(args.Path)}, ""); err != nil {
		return "", fmt.Errorf("path not allowed: %w", err)
	}

	data, err := os.ReadFile(args.Path)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	content := string(data)
	if !strings.Contains(content, args.OldString) {
		return "", fmt.Errorf("old_string not found in file")
	}

	// Check for unique match.
	count := strings.Count(content, args.OldString)
	if count > 1 {
		return "", fmt.Errorf("old_string appears %d times (not unique)", count)
	}

	newContent := strings.Replace(content, args.OldString, args.NewString, 1)
	if err := os.WriteFile(args.Path, []byte(newContent), 0o644); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}

	return fmt.Sprintf("replaced 1 occurrence in %s", args.Path), nil
}

// toolWebFetch fetches a URL and returns plain text.
func toolWebFetch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		URL       string `json:"url"`
		MaxLength int    `json:"maxLength"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.URL == "" {
		return "", fmt.Errorf("url is required")
	}
	if args.MaxLength <= 0 {
		args.MaxLength = 50000 // default 50KB
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", args.URL, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", "Tetora/2.0")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch url: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("http %d: %s", resp.StatusCode, resp.Status)
	}

	// Limit response size.
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(args.MaxLength)))
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}

	// Simple HTML tag stripping.
	text := stripHTMLTags(string(body))

	// Truncate to maxLength after stripping tags.
	if len(text) > args.MaxLength {
		text = text[:args.MaxLength]
	}

	return text, nil
}

// stripHTMLTags removes HTML tags from text (naive implementation).
func stripHTMLTags(html string) string {
	var result strings.Builder
	inTag := false
	for _, c := range html {
		if c == '<' {
			inTag = true
		} else if c == '>' {
			inTag = false
		} else if !inTag {
			result.WriteRune(c)
		}
	}
	// Collapse multiple whitespace.
	text := result.String()
	text = strings.Join(strings.Fields(text), " ")
	return text
}

// toolSessionList lists active sessions.
func toolSessionList(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Channel string `json:"channel"`
	}
	json.Unmarshal(input, &args)

	query := `SELECT session_id, channel_type, channel_id, message_count, created_at, updated_at FROM sessions WHERE 1=1`
	if args.Channel != "" {
		query += fmt.Sprintf(` AND channel_type = '%s'`, db.Escape(args.Channel))
	}
	query += ` ORDER BY updated_at DESC LIMIT 20`

	rows, err := db.Query(cfg.HistoryDB, query)
	if err != nil {
		return "", fmt.Errorf("query failed: %w", err)
	}

	var results []map[string]string
	for _, row := range rows {
		results = append(results, map[string]string{
			"session_id":    fmt.Sprintf("%v", row["session_id"]),
			"channel_type":  fmt.Sprintf("%v", row["channel_type"]),
			"channel_id":    fmt.Sprintf("%v", row["channel_id"]),
			"message_count": fmt.Sprintf("%v", row["message_count"]),
			"created_at":    fmt.Sprintf("%v", row["created_at"]),
			"updated_at":    fmt.Sprintf("%v", row["updated_at"]),
		})
	}

	b, _ := json.Marshal(results)
	return string(b), nil
}

// toolMessage sends a message to a channel.
func toolMessage(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Channel string `json:"channel"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Channel == "" || args.Message == "" {
		return "", fmt.Errorf("channel and message are required")
	}

	switch args.Channel {
	case "telegram":
		if cfg.Telegram.Enabled {
			err := sendTelegramNotify(&cfg.Telegram, args.Message)
			if err != nil {
				return "", fmt.Errorf("send telegram: %w", err)
			}
			return "message sent to telegram", nil
		}
		return "", fmt.Errorf("telegram not enabled")
	case "slack":
		if cfg.Slack.Enabled {
			// Use notification system.
			notifiers := buildNotifiers(cfg)
			for _, n := range notifiers {
				if n.Name() == "slack" {
					n.Send(args.Message)
				}
			}
			return "message sent to slack", nil
		}
		return "", fmt.Errorf("slack not enabled")
	case "discord":
		if cfg.Discord.Enabled {
			// Use notification system.
			notifiers := buildNotifiers(cfg)
			for _, n := range notifiers {
				if n.Name() == "discord" {
					n.Send(args.Message)
				}
			}
			return "message sent to discord", nil
		}
		return "", fmt.Errorf("discord not enabled")
	default:
		// Support discord-id:CHANNEL_ID for direct bot-token based sending.
		if strings.HasPrefix(args.Channel, "discord-id:") {
			channelID := strings.TrimPrefix(args.Channel, "discord-id:")
			if !cfg.Discord.Enabled {
				return "", fmt.Errorf("discord not enabled")
			}
			if cfg.Discord.BotToken == "" {
				return "", fmt.Errorf("discord bot token not configured")
			}
			if err := cronDiscordSendBotChannel(cfg.Discord.BotToken, channelID, args.Message); err != nil {
				return "", fmt.Errorf("send discord-id:%s: %w", channelID, err)
			}
			return "message sent to discord channel " + channelID, nil
		}
		// Support discord-<name> for named webhook channels, e.g. "discord-stock".
		if strings.HasPrefix(args.Channel, "discord-") {
			name := strings.TrimPrefix(args.Channel, "discord-")
			if !cfg.Discord.Enabled {
				return "", fmt.Errorf("discord not enabled")
			}
			webhookURL, ok := cfg.Discord.Webhooks[name]
			if !ok || webhookURL == "" {
				return "", fmt.Errorf("discord channel %q not configured (add to discord.webhooks in config.json)", name)
			}
			n := newDiscordNotifier(webhookURL, 10*time.Second)
			if err := n.Send(args.Message); err != nil {
				return "", fmt.Errorf("send discord-%s: %w", name, err)
			}
			return "message sent to discord-" + name, nil
		}
		return "", fmt.Errorf("unknown channel: %s", args.Channel)
	}
}

// toolCronList lists cron jobs.
func toolCronList(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	// Read cron jobs from JobsFile.
	jobs, err := loadCronJobs(cfg.JobsFile)
	if err != nil {
		return "", fmt.Errorf("load jobs: %w", err)
	}

	var results []map[string]any
	for _, j := range jobs {
		results = append(results, map[string]any{
			"id":       j.ID,
			"name":     j.Name,
			"schedule": j.Schedule,
			"enabled":  j.Enabled,
			"agent":    j.Agent,
		})
	}

	b, _ := json.Marshal(results)
	return string(b), nil
}

// toolCronCreate creates or updates a cron job.
func toolCronCreate(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Name     string `json:"name"`
		Schedule string `json:"schedule"`
		Prompt   string `json:"prompt"`
		Agent    string `json:"agent"`
		Role     string `json:"role"` // backward compat
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Agent == "" {
		args.Agent = args.Role
	}
	if args.Name == "" || args.Schedule == "" || args.Prompt == "" {
		return "", fmt.Errorf("name, schedule, and prompt are required")
	}

	jobs, err := loadCronJobs(cfg.JobsFile)
	if err != nil {
		jobs = []CronJobConfig{}
	}

	// Check if job exists.
	found := false
	for i := range jobs {
		if jobs[i].Name == args.Name {
			jobs[i].Schedule = args.Schedule
			jobs[i].Task.Prompt = args.Prompt
			jobs[i].Agent = args.Agent
			jobs[i].Enabled = true
			found = true
			break
		}
	}

	if !found {
		newJob := CronJobConfig{
			ID:       newUUID(),
			Name:     args.Name,
			Schedule: args.Schedule,
			Enabled:  true,
			Agent:    args.Agent,
			Task: CronTaskConfig{
				Prompt: args.Prompt,
			},
		}
		jobs = append(jobs, newJob)
	}

	if err := saveCronJobs(cfg.JobsFile, jobs); err != nil {
		return "", fmt.Errorf("save jobs: %w", err)
	}

	msg := "created"
	if found {
		msg = "updated"
	}
	return fmt.Sprintf("cron job %q %s", args.Name, msg), nil
}

// toolCronDelete deletes a cron job.
func toolCronDelete(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Name == "" {
		return "", fmt.Errorf("name is required")
	}

	jobs, err := loadCronJobs(cfg.JobsFile)
	if err != nil {
		return "", fmt.Errorf("load jobs: %w", err)
	}

	found := false
	newJobs := make([]CronJobConfig, 0, len(jobs))
	for _, j := range jobs {
		if j.Name != args.Name {
			newJobs = append(newJobs, j)
		} else {
			found = true
		}
	}

	if !found {
		return "", fmt.Errorf("job %q not found", args.Name)
	}

	if err := saveCronJobs(cfg.JobsFile, newJobs); err != nil {
		return "", fmt.Errorf("save jobs: %w", err)
	}

	return fmt.Sprintf("cron job %q deleted", args.Name), nil
}

// --- Helper functions for cron job management ---

func loadCronJobs(path string) ([]CronJobConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var jobs []CronJobConfig
	if err := json.Unmarshal(data, &jobs); err != nil {
		return nil, err
	}
	return jobs, nil
}

func saveCronJobs(path string, jobs []CronJobConfig) error {
	data, err := json.MarshalIndent(jobs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// Registration moved to internal/tools/taskboard.go.
// Handler factories below are passed via TaskboardDeps in wire_tools.go.

// --- Handler Factories ---

func toolTaskboardList(cfg *Config) ToolHandler {
	return func(ctx context.Context, _ *Config, input json.RawMessage) (string, error) {
		var args struct {
			Status   string `json:"status"`
			Assignee string `json:"assignee"`
			Project  string `json:"project"`
			ParentID string `json:"parentId"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}

		tb := newTaskBoardEngine(cfg.HistoryDB, cfg.TaskBoard, cfg.Webhooks)

		// If parentId is specified, use ListChildren.
		if args.ParentID != "" {
			children, err := tb.ListChildren(args.ParentID)
			if err != nil {
				return "", err
			}
			out, _ := json.MarshalIndent(children, "", "  ")
			return string(out), nil
		}

		tasks, err := tb.ListTasks(args.Status, args.Assignee, args.Project)
		if err != nil {
			return "", err
		}
		out, _ := json.MarshalIndent(tasks, "", "  ")
		return string(out), nil
	}
}

func toolTaskboardGet(cfg *Config) ToolHandler {
	return func(ctx context.Context, _ *Config, input json.RawMessage) (string, error) {
		var args struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}
		if args.ID == "" {
			return "", fmt.Errorf("id is required")
		}

		tb := newTaskBoardEngine(cfg.HistoryDB, cfg.TaskBoard, cfg.Webhooks)

		task, err := tb.GetTask(args.ID)
		if err != nil {
			// Suggest similar tasks on not-found.
			normalizedID := normalizeTaskID(args.ID)
			if candidates := tb.SuggestTasks(normalizedID); len(candidates) > 0 {
				lines := []string{err.Error(), "Did you mean:"}
				for _, c := range candidates {
					lines = append(lines, fmt.Sprintf("  %s  %s  (%s)", c.ID, c.Title, c.Status))
				}
				return "", fmt.Errorf("%s", strings.Join(lines, "\n"))
			}
			return "", err
		}
		// Use normalized ID (from task) for thread lookup.
		comments, err := tb.GetThread(task.ID)
		if err != nil {
			return "", err
		}

		result := map[string]any{
			"task":     task,
			"comments": comments,
		}
		out, _ := json.MarshalIndent(result, "", "  ")
		return string(out), nil
	}
}

func toolTaskboardCreate(cfg *Config) ToolHandler {
	return func(ctx context.Context, _ *Config, input json.RawMessage) (string, error) {
		var args struct {
			Title       string   `json:"title"`
			Description string   `json:"description"`
			Assignee    string   `json:"assignee"`
			Priority    string   `json:"priority"`
			Project     string   `json:"project"`
			ParentID    string   `json:"parentId"`
			Model       string   `json:"model"`
			DependsOn   []string `json:"dependsOn"`
			Workflow    string   `json:"workflow"`
			Type        string   `json:"type"`
			RetryPolicy string   `json:"retryPolicy"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}
		if args.Title == "" {
			return "", fmt.Errorf("title is required")
		}

		tb := newTaskBoardEngine(cfg.HistoryDB, cfg.TaskBoard, cfg.Webhooks)

		task, err := tb.CreateTask(TaskBoard{
			Title:       args.Title,
			Description: args.Description,
			Assignee:    args.Assignee,
			Priority:    args.Priority,
			Project:     args.Project,
			ParentID:    args.ParentID,
			Model:       args.Model,
			DependsOn:   args.DependsOn,
			Workflow:    args.Workflow,
			Type:        args.Type,
			RetryPolicy: args.RetryPolicy,
		})
		if err != nil {
			return "", err
		}

		out, _ := json.MarshalIndent(task, "", "  ")
		return string(out), nil
	}
}

func toolTaskboardMove(cfg *Config) ToolHandler {
	return func(ctx context.Context, _ *Config, input json.RawMessage) (string, error) {
		var args struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}
		if args.ID == "" || args.Status == "" {
			return "", fmt.Errorf("id and status are required")
		}

		tb := newTaskBoardEngine(cfg.HistoryDB, cfg.TaskBoard, cfg.Webhooks)

		task, err := tb.MoveTask(args.ID, args.Status)
		if err != nil {
			return "", err
		}

		out, _ := json.MarshalIndent(task, "", "  ")
		return string(out), nil
	}
}

func toolTaskboardComment(cfg *Config) ToolHandler {
	return func(ctx context.Context, _ *Config, input json.RawMessage) (string, error) {
		var args struct {
			TaskID  string `json:"taskId"`
			Content string `json:"content"`
			Author  string `json:"author"`
			Type    string `json:"type"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}
		if args.TaskID == "" || args.Content == "" {
			return "", fmt.Errorf("taskId and content are required")
		}
		if args.Author == "" {
			args.Author = "agent"
		}

		tb := newTaskBoardEngine(cfg.HistoryDB, cfg.TaskBoard, cfg.Webhooks)

		comment, err := tb.AddComment(args.TaskID, args.Author, args.Content, args.Type)
		if err != nil {
			return "", err
		}

		out, _ := json.MarshalIndent(comment, "", "  ")
		return string(out), nil
	}
}

func toolTaskboardDecompose(cfg *Config) ToolHandler {
	return func(ctx context.Context, _ *Config, input json.RawMessage) (string, error) {
		var args struct {
			ParentID string `json:"parentId"`
			Subtasks []struct {
				Title       string   `json:"title"`
				Description string   `json:"description"`
				Assignee    string   `json:"assignee"`
				Priority    string   `json:"priority"`
				Model       string   `json:"model"`
				Type        string   `json:"type"`
				DependsOn   []string `json:"dependsOn"`
			} `json:"subtasks"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}
		if args.ParentID == "" {
			return "", fmt.Errorf("parentId is required")
		}
		if len(args.Subtasks) == 0 {
			return "", fmt.Errorf("subtasks array is required and must not be empty")
		}

		tb := newTaskBoardEngine(cfg.HistoryDB, cfg.TaskBoard, cfg.Webhooks)

		// Verify parent exists.
		parent, err := tb.GetTask(args.ParentID)
		if err != nil {
			return "", fmt.Errorf("parent task not found: %w", err)
		}

		// Fetch existing children for idempotency check.
		existing, err := tb.ListChildren(args.ParentID)
		if err != nil {
			return "", fmt.Errorf("failed to list existing children: %w", err)
		}
		existingTitles := make(map[string]bool, len(existing))
		for _, e := range existing {
			existingTitles[e.Title] = true
		}

		var created, skipped int
		var subtaskIDs []string

		for _, sub := range args.Subtasks {
			if sub.Title == "" {
				continue
			}

			// Idempotency: skip if same title already exists under this parent.
			if existingTitles[sub.Title] {
				skipped++
				continue
			}

			priority := sub.Priority
			if priority == "" {
				priority = parent.Priority
			}

			subType := sub.Type
			if subType == "" {
				subType = parent.Type
			}

			task, err := tb.CreateTask(TaskBoard{
				Title:       sub.Title,
				Description: sub.Description,
				Assignee:    sub.Assignee,
				Priority:    priority,
				Project:     parent.Project,
				ParentID:    args.ParentID,
				Model:       sub.Model,
				Type:        subType,
				DependsOn:   sub.DependsOn,
			})
			if err != nil {
				log.Warn("taskboard_decompose: create subtask failed", "parent", args.ParentID, "title", sub.Title, "error", err)
				continue
			}

			created++
			subtaskIDs = append(subtaskIDs, task.ID)
			existingTitles[sub.Title] = true
		}

		// Move parent to "todo" (ready, waiting for children) if it was in backlog.
		if created > 0 && parent.Status == "backlog" {
			if _, err := tb.MoveTask(args.ParentID, "todo"); err != nil {
				log.Warn("taskboard_decompose: failed to move parent to todo", "parentId", args.ParentID, "error", err)
			}
		}

		// Add decomposition comment to parent.
		if created > 0 {
			comment := fmt.Sprintf("[decompose] Created %d subtasks (skipped %d existing): %s",
				created, skipped, strings.Join(subtaskIDs, ", "))
			if _, err := tb.AddComment(args.ParentID, "system", comment); err != nil {
				log.Warn("taskboard_decompose: add comment failed", "parentId", args.ParentID, "error", err)
			}
		}

		result := map[string]any{
			"created":    created,
			"skipped":    skipped,
			"subtaskIds": subtaskIDs,
		}
		out, _ := json.MarshalIndent(result, "", "  ")
		return string(out), nil
	}
}

// --- Canvas Engine ---

// CanvasEngine provides 3 layers: MCP Apps Host, Built-in Canvas Tools, Interactive Canvas.
type CanvasEngine struct {
	sessions map[string]*CanvasSession
	mu       sync.RWMutex
	cfg      *Config
	mcpHost  *MCPHost
}

// CanvasSession represents a single canvas instance.
type CanvasSession struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Content   string    `json:"content"` // HTML
	Width     string    `json:"width"`
	Height    string    `json:"height"`
	Source    string    `json:"source"`  // "builtin", "mcp"
	MCPServer string    `json:"mcpServer,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// CanvasMessage represents a message between canvas iframe and agent.
type CanvasMessage struct {
	SessionID string          `json:"sessionId"`
	Message   json.RawMessage `json:"message"`
}

// newCanvasEngine creates a new canvas engine.
func newCanvasEngine(cfg *Config, mcpHost *MCPHost) *CanvasEngine {
	return &CanvasEngine{
		sessions: make(map[string]*CanvasSession),
		cfg:      cfg,
		mcpHost:  mcpHost,
	}
}

// --- L1: MCP Apps Host ---

// discoverMCPCanvas checks if an MCP server provides ui:// resources.
// When a tool response contains _meta.ui/resourceUri, fetch the HTML from MCP.
func (ce *CanvasEngine) discoverMCPCanvas(mcpServerName, resourceURI string) (*CanvasSession, error) {
	ce.mu.Lock()
	defer ce.mu.Unlock()

	if ce.mcpHost == nil {
		return nil, fmt.Errorf("mcp host not available")
	}

	// Check if MCP server exists.
	server := ce.mcpHost.GetServer(mcpServerName)
	if server == nil {
		return nil, fmt.Errorf("mcp server %q not found", mcpServerName)
	}

	// Fetch resource from MCP server.
	// This is a simplified implementation. In a real scenario, you'd send a
	// JSON-RPC request to the MCP server to fetch the resource content.
	// For now, we'll return a placeholder.
	content := fmt.Sprintf("<p>Canvas from MCP server: %s</p><p>Resource: %s</p>", mcpServerName, resourceURI)

	session := &CanvasSession{
		ID:        newUUID(),
		Title:     fmt.Sprintf("MCP: %s", mcpServerName),
		Content:   content,
		Width:     "100%",
		Height:    "400px",
		Source:    "mcp",
		MCPServer: mcpServerName,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	ce.sessions[session.ID] = session
	log.Info("canvas.mcp.created", "id", session.ID, "server", mcpServerName, "uri", resourceURI)

	return session, nil
}

// --- L2: Built-in Canvas Tools ---

// renderCanvas creates a new canvas session from agent-generated HTML.
func (ce *CanvasEngine) renderCanvas(title, content, width, height string) (*CanvasSession, error) {
	ce.mu.Lock()
	defer ce.mu.Unlock()

	// Validate inputs.
	if title == "" {
		title = "Canvas"
	}
	if width == "" {
		width = "100%"
	}
	if height == "" {
		height = "400px"
	}

	// Apply CSP and sanitization if configured.
	if !ce.cfg.Canvas.AllowScripts {
		// Simple script tag removal (naive implementation).
		// In production, use a proper HTML sanitizer.
		content = stripScriptTags(content)
	}

	session := &CanvasSession{
		ID:        newUUID(),
		Title:     title,
		Content:   content,
		Width:     width,
		Height:    height,
		Source:    "builtin",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	ce.sessions[session.ID] = session
	log.Info("canvas.render", "id", session.ID, "title", title)

	return session, nil
}

// updateCanvas updates an existing canvas session's content.
func (ce *CanvasEngine) updateCanvas(id, content string) error {
	ce.mu.Lock()
	defer ce.mu.Unlock()

	session, ok := ce.sessions[id]
	if !ok {
		return fmt.Errorf("canvas session %q not found", id)
	}

	// Apply sanitization.
	if !ce.cfg.Canvas.AllowScripts {
		content = stripScriptTags(content)
	}

	session.Content = content
	session.UpdatedAt = time.Now()

	log.Info("canvas.update", "id", id)
	return nil
}

// closeCanvas closes a canvas session.
func (ce *CanvasEngine) closeCanvas(id string) error {
	ce.mu.Lock()
	defer ce.mu.Unlock()

	if _, ok := ce.sessions[id]; !ok {
		return fmt.Errorf("canvas session %q not found", id)
	}

	delete(ce.sessions, id)
	log.Info("canvas.close", "id", id)
	return nil
}

// getCanvas retrieves a canvas session by ID.
func (ce *CanvasEngine) getCanvas(id string) (*CanvasSession, error) {
	ce.mu.RLock()
	defer ce.mu.RUnlock()

	session, ok := ce.sessions[id]
	if !ok {
		return nil, fmt.Errorf("canvas session %q not found", id)
	}

	return session, nil
}

// listCanvasSessions returns all active canvas sessions.
func (ce *CanvasEngine) listCanvasSessions() []*CanvasSession {
	ce.mu.RLock()
	defer ce.mu.RUnlock()

	sessions := make([]*CanvasSession, 0, len(ce.sessions))
	for _, s := range ce.sessions {
		sessions = append(sessions, s)
	}

	return sessions
}

// --- L3: Interactive Canvas ---

// handleCanvasMessage handles messages from canvas iframe to agent.
// This allows interactive canvas to send user input back to the agent session.
func (ce *CanvasEngine) handleCanvasMessage(sessionID string, message json.RawMessage) error {
	ce.mu.RLock()
	session, ok := ce.sessions[sessionID]
	ce.mu.RUnlock()

	if !ok {
		return fmt.Errorf("canvas session %q not found", sessionID)
	}

	// Log the message for now. In a full implementation, this would:
	// 1. Look up the agent session associated with this canvas
	// 2. Inject the message into that session's context
	// 3. Trigger the agent to process the message
	log.Info("canvas.message", "sessionId", sessionID, "source", session.Source, "message", string(message))

	return nil
}

// --- Helper Functions ---

// stripScriptTags removes <script> tags from HTML (naive implementation).
// In production, use a proper HTML sanitizer like bluemonday.
func stripScriptTags(html string) string {
	// Simple implementation: remove everything between <script> and </script>
	result := html
	for {
		start := findIgnoreCase(result, "<script")
		if start == -1 {
			break
		}
		end := findIgnoreCase(result[start:], "</script>")
		if end == -1 {
			// Unclosed script tag, remove to end.
			result = result[:start]
			break
		}
		end += start + len("</script>")
		result = result[:start] + result[end:]
	}
	return result
}

// findIgnoreCase finds the first occurrence of substr in s (case-insensitive).
// Returns -1 if not found.
func findIgnoreCase(s, substr string) int {
	sLower := toLower(s)
	substrLower := toLower(substr)
	for i := 0; i <= len(sLower)-len(substrLower); i++ {
		if sLower[i:i+len(substrLower)] == substrLower {
			return i
		}
	}
	return -1
}

// toLower converts a string to lowercase (ASCII only, simplified).
func toLower(s string) string {
	result := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c = c + ('a' - 'A')
		}
		result[i] = c
	}
	return string(result)
}

// --- Tool Handlers (registered in tool.go or http.go) ---

// toolCanvasRender is the handler for canvas_render tool.
func toolCanvasRender(ctx context.Context, ce *CanvasEngine) ToolHandler {
	return func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
		var args struct {
			Title   string `json:"title"`
			Content string `json:"content"`
			Width   string `json:"width"`
			Height  string `json:"height"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}
		if args.Content == "" {
			return "", fmt.Errorf("content is required")
		}

		session, err := ce.renderCanvas(args.Title, args.Content, args.Width, args.Height)
		if err != nil {
			return "", err
		}

		result := map[string]any{
			"id":      session.ID,
			"title":   session.Title,
			"message": "Canvas rendered successfully",
		}
		b, _ := json.Marshal(result)
		return string(b), nil
	}
}

// toolCanvasUpdate is the handler for canvas_update tool.
func toolCanvasUpdate(ctx context.Context, ce *CanvasEngine) ToolHandler {
	return func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
		var args struct {
			ID      string `json:"id"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}
		if args.ID == "" || args.Content == "" {
			return "", fmt.Errorf("id and content are required")
		}

		if err := ce.updateCanvas(args.ID, args.Content); err != nil {
			return "", err
		}

		result := map[string]any{
			"id":      args.ID,
			"message": "Canvas updated successfully",
		}
		b, _ := json.Marshal(result)
		return string(b), nil
	}
}

// toolCanvasClose is the handler for canvas_close tool.
func toolCanvasClose(ctx context.Context, ce *CanvasEngine) ToolHandler {
	return func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
		var args struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}
		if args.ID == "" {
			return "", fmt.Errorf("id is required")
		}

		if err := ce.closeCanvas(args.ID); err != nil {
			return "", err
		}

		result := map[string]any{
			"id":      args.ID,
			"message": "Canvas closed successfully",
		}
		b, _ := json.Marshal(result)
		return string(b), nil
	}
}

// registerCanvasTools registers canvas tools with the tool registry.
// This is called from http.go after creating the canvasEngine.
func registerCanvasTools(registry *ToolRegistry, ce *CanvasEngine, cfg *Config) {
	// Check which canvas tools are enabled.
	enabled := func(name string) bool {
		if cfg.Tools.Builtin == nil {
			return true // default: all enabled
		}
		e, ok := cfg.Tools.Builtin[name]
		return !ok || e
	}

	if enabled("canvas_render") {
		registry.Register(&ToolDef{
			Name:        "canvas_render",
			Description: "Render HTML/SVG content in dashboard canvas panel",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"title": {"type": "string", "description": "Canvas title"},
					"content": {"type": "string", "description": "HTML/SVG content to render"},
					"width": {"type": "string", "description": "Canvas width (e.g., '100%', '800px'). Default: 100%"},
					"height": {"type": "string", "description": "Canvas height (e.g., '400px', '600px'). Default: 400px"}
				},
				"required": ["content"]
			}`),
			Handler: toolCanvasRender(context.Background(), ce),
			Builtin: true,
		})
	}

	if enabled("canvas_update") {
		registry.Register(&ToolDef{
			Name:        "canvas_update",
			Description: "Update existing canvas content",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"id": {"type": "string", "description": "Canvas session ID"},
					"content": {"type": "string", "description": "New HTML/SVG content"}
				},
				"required": ["id", "content"]
			}`),
			Handler: toolCanvasUpdate(context.Background(), ce),
			Builtin: true,
		})
	}

	if enabled("canvas_close") {
		registry.Register(&ToolDef{
			Name:        "canvas_close",
			Description: "Close a canvas panel",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"id": {"type": "string", "description": "Canvas session ID to close"}
				},
				"required": ["id"]
			}`),
			Handler: toolCanvasClose(context.Background(), ce),
			Builtin: true,
		})
	}
}
