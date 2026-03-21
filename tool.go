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
	"regexp"
	"strings"
	"sync"
	"tetora/internal/automation/briefing"
	"tetora/internal/db"
	"tetora/internal/life/reminder"
	"tetora/internal/log"
	"tetora/internal/nlp"
	"tetora/internal/provider"
	"tetora/internal/tool"
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

// Global singletons for life services.
var (
	globalContactsService *ContactsService
	globalFinanceService  *FinanceService
	globalGoalsService    *GoalsService
	globalHabitsService   *HabitsService
	globalTimeTracking    *TimeTrackingService
	globalFamilyService      *FamilyService
	globalUserProfileService *UserProfileService
)

// --- Contacts Tool Handlers ---

func toolContactAdd(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Contacts == nil {
		return "", fmt.Errorf("contacts service not initialized")
	}
	return tool.ContactAdd(app.Contacts, newUUID, input)
}

func toolContactSearch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Contacts == nil {
		return "", fmt.Errorf("contacts service not initialized")
	}
	return tool.ContactSearch(app.Contacts, input)
}

func toolContactList(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Contacts == nil {
		return "", fmt.Errorf("contacts service not initialized")
	}
	return tool.ContactList(app.Contacts, input)
}

func toolContactUpcoming(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Contacts == nil {
		return "", fmt.Errorf("contacts service not initialized")
	}
	return tool.ContactUpcoming(app.Contacts, input)
}

func toolContactLog(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Contacts == nil {
		return "", fmt.Errorf("contacts service not initialized")
	}
	return tool.ContactLog(app.Contacts, newUUID, input)
}

// --- Finance Tool Handlers ---

func toolExpenseAdd(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Finance == nil {
		return "", fmt.Errorf("finance service not initialized (enable finance in config)")
	}
	return tool.ExpenseAdd(app.Finance, parseExpenseNL, cfg.Finance.DefaultCurrencyOrTWD(), input)
}

func toolExpenseReport(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Finance == nil {
		return "", fmt.Errorf("finance service not initialized (enable finance in config)")
	}
	return tool.ExpenseReport(app.Finance, input)
}

func toolExpenseBudget(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Finance == nil {
		return "", fmt.Errorf("finance service not initialized (enable finance in config)")
	}
	return tool.ExpenseBudget(app.Finance, cfg.Finance.DefaultCurrencyOrTWD(), input)
}

// --- Goals Tool Handlers ---

func toolGoalCreate(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Goals == nil {
		return "", fmt.Errorf("goals service not initialized")
	}
	return tool.GoalCreate(app.Goals, newUUID, app.Lifecycle, cfg.Lifecycle.AutoHabitSuggest, input)
}

func toolGoalList(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Goals == nil {
		return "", fmt.Errorf("goals service not initialized")
	}
	return tool.GoalList(app.Goals, input)
}

func toolGoalUpdate(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Goals == nil {
		return "", fmt.Errorf("goals service not initialized")
	}
	return tool.GoalUpdate(app.Goals, newUUID, app.Lifecycle, log.Warn, input)
}

func toolGoalReview(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Goals == nil {
		return "", fmt.Errorf("goals service not initialized")
	}
	return tool.GoalReview(app.Goals, input)
}

// --- Habits Tool Handlers ---

func toolHabitCreate(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Habits == nil {
		return "", fmt.Errorf("habits service not initialized")
	}
	return tool.HabitCreate(app.Habits, newUUID, input)
}

func toolHabitLog(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Habits == nil {
		return "", fmt.Errorf("habits service not initialized")
	}
	return tool.HabitLog(app.Habits, newUUID, input)
}

func toolHabitStatus(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Habits == nil {
		return "", fmt.Errorf("habits service not initialized")
	}
	return tool.HabitStatus(app.Habits, log.Warn, input)
}

func toolHabitReport(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Habits == nil {
		return "", fmt.Errorf("habits service not initialized")
	}
	return tool.HabitReport(app.Habits, input)
}

func toolHealthLog(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Habits == nil {
		return "", fmt.Errorf("habits service not initialized")
	}
	return tool.HealthLog(app.Habits, newUUID, input)
}

func toolHealthSummary(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Habits == nil {
		return "", fmt.Errorf("habits service not initialized")
	}
	return tool.HealthSummary(app.Habits, input)
}

// --- Time Tracking Tool Handlers ---

func toolTimeStart(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.TimeTracking == nil {
		return "", fmt.Errorf("time tracking not initialized")
	}
	return tool.TimeStart(app.TimeTracking, newUUID, input)
}

func toolTimeStop(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.TimeTracking == nil {
		return "", fmt.Errorf("time tracking not initialized")
	}
	return tool.TimeStop(app.TimeTracking, input)
}

func toolTimeLog(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.TimeTracking == nil {
		return "", fmt.Errorf("time tracking not initialized")
	}
	return tool.TimeLog(app.TimeTracking, newUUID, input)
}

func toolTimeReport(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.TimeTracking == nil {
		return "", fmt.Errorf("time tracking not initialized")
	}
	return tool.TimeReport(app.TimeTracking, input)
}

// --- Family Tool Handlers ---

func toolFamilyListAdd(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	svc := globalFamilyService
	if svc == nil {
		return "", fmt.Errorf("family mode not enabled")
	}

	var args struct {
		ListID   string `json:"listId"`
		Text     string `json:"text"`
		Quantity string `json:"quantity"`
		AddedBy  string `json:"addedBy"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Text == "" {
		return "", fmt.Errorf("text is required")
	}
	if args.AddedBy == "" {
		args.AddedBy = "default"
	}

	// If listId not provided, use the first shopping list or create one.
	if args.ListID == "" {
		lists, err := svc.ListLists()
		if err != nil {
			return "", err
		}
		for _, l := range lists {
			if l.ListType == "shopping" {
				args.ListID = l.ID
				break
			}
		}
		if args.ListID == "" {
			list, err := svc.CreateList("Shopping", "shopping", args.AddedBy, newUUID)
			if err != nil {
				return "", fmt.Errorf("create default shopping list: %w", err)
			}
			args.ListID = list.ID
		}
	}

	item, err := svc.AddListItem(args.ListID, args.Text, args.Quantity, args.AddedBy)
	if err != nil {
		return "", err
	}

	b, _ := json.Marshal(map[string]any{
		"status": "added",
		"item":   item,
	})
	return string(b), nil
}

func toolFamilyListView(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	svc := globalFamilyService
	if svc == nil {
		return "", fmt.Errorf("family mode not enabled")
	}

	var args struct {
		ListID   string `json:"listId"`
		ListType string `json:"listType"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if args.ListID != "" {
		items, err := svc.GetListItems(args.ListID)
		if err != nil {
			return "", err
		}
		list, _ := svc.GetList(args.ListID)
		result := map[string]any{
			"items": items,
		}
		if list != nil {
			result["list"] = list
		}
		b, _ := json.Marshal(result)
		return string(b), nil
	}

	lists, err := svc.ListLists()
	if err != nil {
		return "", err
	}
	if args.ListType != "" {
		var filtered []SharedList
		for _, l := range lists {
			if l.ListType == args.ListType {
				filtered = append(filtered, l)
			}
		}
		lists = filtered
	}

	b, _ := json.Marshal(map[string]any{"lists": lists})
	return string(b), nil
}

func toolUserSwitch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	svc := globalFamilyService
	if svc == nil {
		return "", fmt.Errorf("family mode not enabled")
	}

	var args struct {
		UserID string `json:"userId"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.UserID == "" {
		return "", fmt.Errorf("userId is required")
	}

	user, err := svc.GetUser(args.UserID)
	if err != nil {
		return "", fmt.Errorf("user not found or inactive: %w", err)
	}

	allowed, remaining, _ := svc.CheckRateLimit(args.UserID)
	perms, _ := svc.GetPermissions(args.UserID)

	b, _ := json.Marshal(map[string]any{
		"status":      "switched",
		"user":        user,
		"permissions": perms,
		"rateLimit": map[string]any{
			"allowed":   allowed,
			"remaining": remaining,
		},
	})
	return string(b), nil
}

func toolFamilyManage(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	svc := globalFamilyService
	if svc == nil {
		return "", fmt.Errorf("family mode not enabled")
	}

	var args struct {
		Action      string  `json:"action"`
		UserID      string  `json:"userId"`
		DisplayName string  `json:"displayName"`
		Role        string  `json:"role"`
		Permission  string  `json:"permission"`
		Grant       bool    `json:"grant"`
		RateLimit   int     `json:"rateLimit"`
		Budget      float64 `json:"budget"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	switch args.Action {
	case "add":
		if args.Role == "" {
			args.Role = "member"
		}
		if err := svc.AddUser(args.UserID, args.DisplayName, args.Role); err != nil {
			return "", err
		}
		user, _ := svc.GetUser(args.UserID)
		b, _ := json.Marshal(map[string]any{"status": "added", "user": user})
		return string(b), nil

	case "remove":
		if err := svc.RemoveUser(args.UserID); err != nil {
			return "", err
		}
		b, _ := json.Marshal(map[string]any{"status": "removed", "userId": args.UserID})
		return string(b), nil

	case "list":
		users, err := svc.ListUsers()
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(map[string]any{"users": users})
		return string(b), nil

	case "update":
		updates := make(map[string]any)
		if args.DisplayName != "" {
			updates["displayName"] = args.DisplayName
		}
		if args.Role != "" {
			updates["role"] = args.Role
		}
		if args.RateLimit > 0 {
			updates["rateLimitDaily"] = float64(args.RateLimit)
		}
		if args.Budget > 0 {
			updates["budgetMonthly"] = args.Budget
		}
		if err := svc.UpdateUser(args.UserID, updates); err != nil {
			return "", err
		}
		user, _ := svc.GetUser(args.UserID)
		b, _ := json.Marshal(map[string]any{"status": "updated", "user": user})
		return string(b), nil

	case "permissions":
		if args.Permission != "" {
			if args.Grant {
				if err := svc.GrantPermission(args.UserID, args.Permission); err != nil {
					return "", err
				}
			} else {
				if err := svc.RevokePermission(args.UserID, args.Permission); err != nil {
					return "", err
				}
			}
		}
		perms, err := svc.GetPermissions(args.UserID)
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(map[string]any{"userId": args.UserID, "permissions": perms})
		return string(b), nil

	default:
		return "", fmt.Errorf("unknown action: %s (use add, remove, list, update, or permissions)", args.Action)
	}
}

// --- Price Watch Tool Handler ---

func toolPriceWatch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	fs := globalFinanceService
	if app != nil && app.Finance != nil {
		fs = app.Finance
	}
	if fs == nil {
		return "", fmt.Errorf("finance service not initialized (enable finance in config)")
	}

	engineCfg := cfg
	if engineCfg.HistoryDB == "" {
		engineCfg = &Config{HistoryDB: fs.DBPath()}
	}
	engine := newPriceWatchEngine(engineCfg)

	return tool.PriceWatch(engine, input)
}

// --- User Profile Tool Handlers ---

func toolUserProfileGet(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		UserID     string `json:"userId"`
		ChannelKey string `json:"channelKey"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	app := appFromCtx(ctx)
	if app == nil || app.UserProfile == nil {
		return "", fmt.Errorf("user profile service not initialized")
	}

	if args.UserID == "" && args.ChannelKey != "" {
		uid, err := app.UserProfile.ResolveUser(args.ChannelKey)
		if err != nil {
			return "", fmt.Errorf("resolve user: %w", err)
		}
		args.UserID = uid
	}
	if args.UserID == "" {
		return "", fmt.Errorf("userId or channelKey is required")
	}

	userCtx, err := app.UserProfile.GetUserContext(args.ChannelKey)
	if err != nil {
		profile, err2 := app.UserProfile.GetProfile(args.UserID)
		if err2 != nil {
			return "", fmt.Errorf("get profile: %w", err2)
		}
		if profile == nil {
			return "", fmt.Errorf("user not found")
		}
		b, _ := json.Marshal(profile)
		return string(b), nil
	}

	b, _ := json.Marshal(userCtx)
	return string(b), nil
}

func toolUserProfileSet(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		UserID      string `json:"userId"`
		DisplayName string `json:"displayName"`
		Language    string `json:"language"`
		Timezone    string `json:"timezone"`
		ChannelKey  string `json:"channelKey"`
		ChannelName string `json:"channelName"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.UserID == "" {
		return "", fmt.Errorf("userId is required")
	}

	app := appFromCtx(ctx)
	if app == nil || app.UserProfile == nil {
		return "", fmt.Errorf("user profile service not initialized")
	}

	p, _ := app.UserProfile.GetProfile(args.UserID)
	if p == nil {
		err := app.UserProfile.CreateProfile(UserProfile{ID: args.UserID})
		if err != nil {
			return "", fmt.Errorf("create profile: %w", err)
		}
	}

	updates := make(map[string]string)
	if args.DisplayName != "" {
		updates["displayName"] = args.DisplayName
	}
	if args.Language != "" {
		updates["preferredLanguage"] = args.Language
	}
	if args.Timezone != "" {
		updates["timezone"] = args.Timezone
	}
	if len(updates) > 0 {
		if err := app.UserProfile.UpdateProfile(args.UserID, updates); err != nil {
			return "", fmt.Errorf("update profile: %w", err)
		}
	}

	if args.ChannelKey != "" {
		if err := app.UserProfile.LinkChannel(args.UserID, args.ChannelKey, args.ChannelName); err != nil {
			return "", fmt.Errorf("link channel: %w", err)
		}
	}

	return fmt.Sprintf(`{"status":"ok","userId":"%s"}`, args.UserID), nil
}

func toolMoodCheck(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		UserID     string `json:"userId"`
		ChannelKey string `json:"channelKey"`
		Days       int    `json:"days"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	app := appFromCtx(ctx)
	if app == nil || app.UserProfile == nil {
		return "", fmt.Errorf("user profile service not initialized")
	}

	if args.UserID == "" && args.ChannelKey != "" {
		uid, err := app.UserProfile.ResolveUser(args.ChannelKey)
		if err != nil {
			return "", fmt.Errorf("resolve user: %w", err)
		}
		args.UserID = uid
	}
	if args.UserID == "" {
		return "", fmt.Errorf("userId or channelKey is required")
	}

	if args.Days <= 0 {
		args.Days = 7
	}

	mood, err := app.UserProfile.GetMoodTrend(args.UserID, args.Days)
	if err != nil {
		return "", fmt.Errorf("get mood: %w", err)
	}

	var totalScore float64
	for _, m := range mood {
		if s, ok := m["sentimentScore"].(float64); ok {
			totalScore += s
		}
	}
	avg := 0.0
	if len(mood) > 0 {
		avg = totalScore / float64(len(mood))
	}

	result := map[string]any{
		"userId":       args.UserID,
		"days":         args.Days,
		"entries":      len(mood),
		"averageScore": avg,
		"label":        nlp.Label(avg),
		"trend":        mood,
	}

	b, _ := json.Marshal(result)
	return string(b), nil
}

// --- Task Sync: Todoist ---

func toolTodoistSync(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if !cfg.TaskManager.Todoist.Enabled {
		return "", fmt.Errorf("todoist sync not enabled")
	}
	var args struct {
		Action string `json:"action"`
		UserID string `json:"userId"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.UserID == "" {
		args.UserID = "default"
	}

	ts := newTodoistSync(cfg)

	switch args.Action {
	case "pull":
		n, err := ts.PullTasks(args.UserID)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Pulled %d tasks from Todoist.", n), nil
	case "push":
		if app == nil || app.TaskManager == nil {
			return "", fmt.Errorf("task manager not initialized")
		}
		localTasks, _ := app.TaskManager.ListTasks(args.UserID, TaskFilter{})
		pushed := 0
		for _, task := range localTasks {
			if task.ExternalSource == "todoist" || task.ExternalID != "" {
				continue
			}
			if err := ts.PushTask(task); err != nil {
				continue
			}
			pushed++
		}
		return fmt.Sprintf("Pushed %d tasks to Todoist.", pushed), nil
	case "sync", "":
		pulled, pushed, err := ts.SyncAll(args.UserID)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Todoist sync complete: pulled %d, pushed %d.", pulled, pushed), nil
	default:
		return "", fmt.Errorf("unknown action %q (use pull, push, or sync)", args.Action)
	}
}

// --- Task Sync: Notion ---

func toolNotionSync(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if !cfg.TaskManager.Notion.Enabled {
		return "", fmt.Errorf("notion sync not enabled")
	}
	var args struct {
		Action string `json:"action"`
		UserID string `json:"userId"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.UserID == "" {
		args.UserID = "default"
	}

	ns := newNotionSync(cfg)

	switch args.Action {
	case "pull":
		n, err := ns.PullTasks(args.UserID)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Pulled %d tasks from Notion.", n), nil
	case "push":
		if app == nil || app.TaskManager == nil {
			return "", fmt.Errorf("task manager not initialized")
		}
		localTasks, _ := app.TaskManager.ListTasks(args.UserID, TaskFilter{})
		pushed := 0
		for _, task := range localTasks {
			if task.ExternalSource == "notion" || task.ExternalID != "" {
				continue
			}
			if err := ns.PushTask(task); err != nil {
				continue
			}
			pushed++
		}
		return fmt.Sprintf("Pushed %d tasks to Notion.", pushed), nil
	case "sync", "":
		pulled, pushed, err := ns.SyncAll(args.UserID)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Notion sync complete: pulled %d, pushed %d.", pulled, pushed), nil
	default:
		return "", fmt.Errorf("unknown action %q (use pull, push, or sync)", args.Action)
	}
}

// --- Quick Capture Tool Handler ---

func classifyCapture(input string) string {
	lower := strings.ToLower(input)
	if strings.Contains(lower, "$") || strings.Contains(lower, "spent") ||
		strings.Contains(lower, "paid") || strings.Contains(lower, "bought") ||
		strings.Contains(lower, "cost") || strings.Contains(lower, "元") ||
		strings.Contains(lower, "円") {
		return "expense"
	}
	if strings.Contains(lower, "remind") || strings.Contains(lower, "deadline") ||
		strings.Contains(lower, "don't forget") || strings.Contains(lower, "dont forget") {
		return "reminder"
	}
	if strings.Contains(lower, "phone") || strings.Contains(lower, "email") ||
		strings.Contains(lower, "birthday") || strings.Contains(input, "@") {
		return "contact"
	}
	if strings.Contains(lower, "todo") || strings.Contains(lower, "need to") ||
		strings.Contains(lower, "must") || strings.Contains(lower, "should") ||
		strings.Contains(lower, "fix") {
		return "task"
	}
	if strings.HasPrefix(lower, "idea:") || strings.Contains(lower, "what if") {
		return "idea"
	}
	return "note"
}

func executeCapture(ctx context.Context, cfg *Config, category, text string) (string, error) {
	app := appFromCtx(ctx)
	switch category {
	case "task":
		tm := globalTaskManager
		if app != nil && app.TaskManager != nil {
			tm = app.TaskManager
		}
		if tm == nil {
			return "", fmt.Errorf("task manager not initialized")
		}
		task, err := tm.CreateTask(UserTask{
			Title:  text,
			Status: "todo",
		})
		if err != nil {
			return "", fmt.Errorf("create task: %w", err)
		}
		return fmt.Sprintf("Task created: %s (id=%s)", task.Title, task.ID), nil

	case "expense":
		input, _ := json.Marshal(map[string]string{"text": text})
		return toolExpenseAdd(ctx, cfg, input)

	case "reminder":
		re := globalReminderEngine
		if app != nil && app.Reminder != nil {
			re = app.Reminder
		}
		if re == nil {
			return "", fmt.Errorf("reminder engine not initialized")
		}
		due := time.Now().Add(24 * time.Hour)
		r, err := re.Add(text, due, "", "", "default")
		if err != nil {
			return "", fmt.Errorf("add reminder: %w", err)
		}
		return fmt.Sprintf("Reminder set: %s (due=%s)", r.Text, r.DueAt), nil

	case "contact":
		cs := globalContactsService
		if app != nil && app.Contacts != nil {
			cs = app.Contacts
		}
		if cs == nil {
			return "", fmt.Errorf("contacts service not initialized")
		}
		now := time.Now().UTC().Format(time.RFC3339)
		c := &Contact{ID: newUUID(), Name: text, CreatedAt: now, UpdatedAt: now}
		if err := cs.AddContact(c); err != nil {
			return "", fmt.Errorf("add contact: %w", err)
		}
		return fmt.Sprintf("Contact added: %s (id=%s)", c.Name, c.ID), nil

	case "note", "idea":
		if !cfg.Notes.Enabled {
			return "", fmt.Errorf("notes not enabled in config")
		}
		vaultPath := cfg.Notes.VaultPathResolved(cfg.BaseDir)
		prefix := "note"
		if category == "idea" {
			prefix = "idea"
		}
		filename := fmt.Sprintf("%s-%s.md", prefix, time.Now().Format("20060102-150405"))
		notePath := filepath.Join(vaultPath, filename)
		os.MkdirAll(vaultPath, 0o755)
		if err := os.WriteFile(notePath, []byte(text+"\n"), 0o644); err != nil {
			return "", fmt.Errorf("write note: %w", err)
		}
		return fmt.Sprintf("Note saved: %s", notePath), nil

	default:
		return "", fmt.Errorf("unknown capture category: %s", category)
	}
}

func toolQuickCapture(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Text     string `json:"text"`
		Category string `json:"category"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Text == "" {
		return "", fmt.Errorf("text is required")
	}

	category := args.Category
	if category == "" {
		category = classifyCapture(args.Text)
	}

	result, err := executeCapture(ctx, cfg, category, args.Text)
	if err != nil {
		return "", err
	}

	out := map[string]string{
		"category": category,
		"result":   result,
	}
	b, _ := json.Marshal(out)
	return string(b), nil
}

// --- P24.7: Morning Briefing & Evening Wrap ---

var globalBriefingService *briefing.Service

// newBriefingService constructs a briefing.Service from Config + globals.
func newBriefingService(cfg *Config) *briefing.Service {
	deps := briefing.Deps{
		Query:  db.Query,
		Escape: db.Escape,
	}
	if globalSchedulingService != nil {
		svc := globalSchedulingService
		deps.ViewSchedule = func(dateStr string, days int) ([]briefing.ScheduleDay, error) {
			schedules, err := svc.ViewSchedule(dateStr, days)
			if err != nil {
				return nil, err
			}
			result := make([]briefing.ScheduleDay, len(schedules))
			for i, s := range schedules {
				events := make([]briefing.ScheduleEvent, len(s.Events))
				for j, ev := range s.Events {
					events[j] = briefing.ScheduleEvent{
						Start: ev.Start,
						Title: ev.Title,
					}
				}
				result[i] = briefing.ScheduleDay{Events: events}
			}
			return result, nil
		}
	}
	if globalContactsService != nil {
		svc := globalContactsService
		deps.GetUpcomingEvents = func(days int) ([]map[string]any, error) {
			return svc.GetUpcomingEvents(days)
		}
	}
	deps.TasksAvailable = globalTaskManager != nil
	deps.HabitsAvailable = globalHabitsService != nil
	deps.GoalsAvailable = globalGoalsService != nil
	deps.FinanceAvailable = globalFinanceService != nil
	return briefing.New(cfg.HistoryDB, deps)
}

// --- Briefing Tool Handlers ---

func toolBriefingMorning(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Briefing == nil {
		return "", fmt.Errorf("briefing service not initialized")
	}
	var args struct {
		Date string `json:"date"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	date := time.Now()
	if args.Date != "" {
		parsed, err := time.Parse("2006-01-02", args.Date)
		if err != nil {
			return "", fmt.Errorf("invalid date format (expected YYYY-MM-DD): %w", err)
		}
		date = parsed
	}
	br, err := app.Briefing.GenerateMorning(date)
	if err != nil {
		return "", err
	}
	return briefing.FormatBriefing(br), nil
}

func toolBriefingEvening(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Briefing == nil {
		return "", fmt.Errorf("briefing service not initialized")
	}
	var args struct {
		Date string `json:"date"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	date := time.Now()
	if args.Date != "" {
		parsed, err := time.Parse("2006-01-02", args.Date)
		if err != nil {
			return "", fmt.Errorf("invalid date format (expected YYYY-MM-DD): %w", err)
		}
		date = parsed
	}
	br, err := app.Briefing.GenerateEvening(date)
	if err != nil {
		return "", err
	}
	return briefing.FormatBriefing(br), nil
}

// --- P19.3: Smart Reminders ---

// nextCronTime computes the next occurrence of a cron expression after the given time.
// Reuses parseCronExpr and nextRunAfter from cron.go.
func nextCronTime(expr string, after time.Time) time.Time {
	parsed, err := parseCronExpr(expr)
	if err != nil {
		log.Warn("reminder bad cron expr", "expr", expr, "error", err)
		return time.Time{}
	}
	return nextRunAfter(parsed, time.UTC, after)
}

// parseNaturalTime delegates to internal reminder package.
func parseNaturalTime(input string) (time.Time, error) {
	return reminder.ParseNaturalTime(input)
}

// --- Tool Handlers for Reminders ---

// Global reminder engine reference (set in main.go).
var globalReminderEngine *ReminderEngine

func toolReminderSet(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	var args struct {
		Text      string `json:"text"`
		Time      string `json:"time"`
		Recurring string `json:"recurring"`
		Channel   string `json:"channel"`
		UserID    string `json:"user_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("parse input: %w", err)
	}
	if args.Text == "" {
		return "", fmt.Errorf("text is required")
	}
	if args.Time == "" {
		return "", fmt.Errorf("time is required")
	}

	if app == nil || app.Reminder == nil {
		return "", fmt.Errorf("reminder engine not initialized (enable reminders in config)")
	}

	dueAt, err := parseNaturalTime(args.Time)
	if err != nil {
		return "", fmt.Errorf("parse time %q: %w", args.Time, err)
	}

	// Validate recurring expression if provided.
	if args.Recurring != "" {
		if _, err := parseCronExpr(args.Recurring); err != nil {
			return "", fmt.Errorf("invalid recurring cron expression %q: %w", args.Recurring, err)
		}
	}

	rem, err := app.Reminder.Add(args.Text, dueAt, args.Recurring, args.Channel, args.UserID)
	if err != nil {
		return "", err
	}

	out, _ := json.Marshal(rem)
	return string(out), nil
}

func toolReminderList(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	var args struct {
		UserID string `json:"user_id"`
	}
	json.Unmarshal(input, &args)

	if app == nil || app.Reminder == nil {
		return "", fmt.Errorf("reminder engine not initialized")
	}

	reminders, err := app.Reminder.List(args.UserID)
	if err != nil {
		return "", err
	}
	if reminders == nil {
		reminders = []Reminder{}
	}

	out, _ := json.Marshal(map[string]any{
		"reminders": reminders,
		"count":     len(reminders),
	})
	return string(out), nil
}

func toolReminderCancel(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	var args struct {
		ID     string `json:"id"`
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("parse input: %w", err)
	}
	if args.ID == "" {
		return "", fmt.Errorf("id is required")
	}

	if app == nil || app.Reminder == nil {
		return "", fmt.Errorf("reminder engine not initialized")
	}

	if err := app.Reminder.Cancel(args.ID, args.UserID); err != nil {
		return "", err
	}

	return fmt.Sprintf(`{"ok":true,"id":"%s","status":"cancelled"}`, args.ID), nil
}

// --- Lesson Tool Handler ---

func toolStoreLesson(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Category string   `json:"category"`
		Lesson   string   `json:"lesson"`
		Source   string   `json:"source"`
		Tags     []string `json:"tags"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Category == "" {
		return "", fmt.Errorf("category is required")
	}
	if args.Lesson == "" {
		return "", fmt.Errorf("lesson is required")
	}

	category := sanitizeLessonCategory(args.Category)
	noteName := "lessons/" + category

	svc := getGlobalNotesService()
	if svc == nil {
		return "", fmt.Errorf("notes service is not enabled")
	}

	now := time.Now().Format("2006-01-02 15:04")
	var entry strings.Builder
	entry.WriteString(fmt.Sprintf("\n## %s\n", now))
	entry.WriteString(fmt.Sprintf("- %s\n", args.Lesson))
	if args.Source != "" {
		entry.WriteString(fmt.Sprintf("- Source: %s\n", args.Source))
	}
	if len(args.Tags) > 0 {
		entry.WriteString(fmt.Sprintf("- Tags: %s\n", strings.Join(args.Tags, ", ")))
	}

	if err := svc.AppendNote(noteName, entry.String()); err != nil {
		return "", fmt.Errorf("append to vault: %w", err)
	}

	lessonsFile := "tasks/lessons.md"
	if _, err := os.Stat(lessonsFile); err == nil {
		sectionHeader := "## " + args.Category
		line := fmt.Sprintf("- %s", args.Lesson)
		if err := appendToLessonSection(lessonsFile, sectionHeader, line); err != nil {
			log.Warn("append to lessons.md failed", "error", err)
		}
	}

	if cfg.HistoryDB != "" {
		recordSkillEvent(cfg.HistoryDB, category, "lesson", args.Lesson, args.Source)
	}

	log.InfoCtx(ctx, "lesson stored", "category", category, "tags", args.Tags)

	result := map[string]any{
		"status":   "stored",
		"category": category,
		"vault":    noteName,
	}
	b, _ := json.Marshal(result)
	return string(b), nil
}

func sanitizeLessonCategory(cat string) string {
	cat = strings.ToLower(strings.TrimSpace(cat))
	re := regexp.MustCompile(`[^a-z0-9-]+`)
	cat = re.ReplaceAllString(cat, "-")
	cat = strings.Trim(cat, "-")
	if cat == "" {
		cat = "general"
	}
	return cat
}

func appendToLessonSection(filePath, sectionHeader, content string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	lines := strings.Split(string(data), "\n")
	var result []string
	inserted := false

	for i, line := range lines {
		result = append(result, line)
		if strings.TrimSpace(line) == sectionHeader {
			j := i + 1
			for j < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[j]), "## ") {
				j++
			}
			insertIdx := j
			for insertIdx > i+1 && strings.TrimSpace(lines[insertIdx-1]) == "" {
				insertIdx--
			}
			for k := i + 1; k < insertIdx; k++ {
				result = append(result, lines[k])
			}
			result = append(result, content)
			for k := insertIdx; k < len(lines); k++ {
				result = append(result, lines[k])
			}
			inserted = true
			break
		}
	}

	if !inserted {
		result = append(result, "", sectionHeader, content)
	}

	return os.WriteFile(filePath, []byte(strings.Join(result, "\n")), 0o644)
}

// --- Note Dedup Tool Handler ---

func toolNoteDedup(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		AutoDelete bool   `json:"auto_delete"`
		Prefix     string `json:"prefix"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	svc := getGlobalNotesService()
	if svc == nil {
		return "", fmt.Errorf("notes service is not enabled")
	}

	vaultPath := svc.VaultPath()

	type fileHash struct {
		Path string
		Hash string
		Size int64
	}
	var files []fileHash
	hashMap := make(map[string][]string)

	filepath.Walk(vaultPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
			return nil
		}
		if args.Prefix != "" {
			rel, _ := filepath.Rel(vaultPath, path)
			if !strings.HasPrefix(rel, args.Prefix) {
				return nil
			}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		h := sha256.Sum256(data)
		hash := hex.EncodeToString(h[:16])
		rel, _ := filepath.Rel(vaultPath, path)
		files = append(files, fileHash{Path: rel, Hash: hash, Size: info.Size()})
		hashMap[hash] = append(hashMap[hash], rel)
		return nil
	})

	var duplicates []map[string]any
	deleted := 0
	for hash, paths := range hashMap {
		if len(paths) <= 1 {
			continue
		}
		if args.AutoDelete {
			for _, p := range paths[1:] {
				fullPath := filepath.Join(vaultPath, p)
				if err := os.Remove(fullPath); err == nil {
					deleted++
				}
			}
		}
		duplicates = append(duplicates, map[string]any{
			"hash":  hash,
			"files": paths,
			"count": len(paths),
		})
	}

	result := map[string]any{
		"total_files":      len(files),
		"duplicate_groups": len(duplicates),
		"duplicates":       duplicates,
	}
	if args.AutoDelete {
		result["deleted"] = deleted
	}

	b, _ := json.Marshal(result)
	log.InfoCtx(ctx, "note dedup scan complete", "total_files", len(files), "duplicate_groups", len(duplicates))
	return string(b), nil
}

// --- Source Audit Tool Handler ---

func toolSourceAudit(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Expected []string `json:"expected"`
		Prefix   string   `json:"prefix"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	svc := getGlobalNotesService()
	if svc == nil {
		return "", fmt.Errorf("notes service is not enabled")
	}

	vaultPath := svc.VaultPath()
	prefix := args.Prefix
	if prefix == "" {
		prefix = "."
	}

	actualSet := make(map[string]bool)
	scanDir := filepath.Join(vaultPath, prefix)
	filepath.Walk(scanDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		rel, _ := filepath.Rel(vaultPath, path)
		actualSet[rel] = true
		return nil
	})

	expectedSet := make(map[string]bool)
	for _, e := range args.Expected {
		expectedSet[e] = true
	}

	var missing, extra []string
	for e := range expectedSet {
		if !actualSet[e] {
			missing = append(missing, e)
		}
	}
	for a := range actualSet {
		if !expectedSet[a] {
			extra = append(extra, a)
		}
	}

	result := map[string]any{
		"expected_count": len(args.Expected),
		"actual_count":   len(actualSet),
		"missing_count":  len(missing),
		"extra_count":    len(extra),
		"missing":        missing,
		"extra":          extra,
	}
	b, _ := json.Marshal(result)
	log.InfoCtx(ctx, "source audit complete", "expected", len(args.Expected), "actual", len(actualSet))
	return string(b), nil
}

// --- P21.5: Sitemap Ingest Pipeline ---

// toolWebCrawl fetches a sitemap and imports pages into the notes vault.
func toolWebCrawl(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		URL         string   `json:"url"`
		Mode        string   `json:"mode"`        // "sitemap" (default), "single"
		Include     []string `json:"include"`      // glob patterns to include
		Exclude     []string `json:"exclude"`      // glob patterns to exclude
		Target      string   `json:"target"`       // "notes" (default)
		Prefix      string   `json:"prefix"`       // note path prefix
		Dedup       bool     `json:"dedup"`         // skip if same content hash exists
		MaxPages    int      `json:"max_pages"`
		Concurrency int      `json:"concurrency"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.URL == "" {
		return "", fmt.Errorf("url is required")
	}
	if args.Mode == "" {
		args.Mode = "sitemap"
	}
	if args.MaxPages <= 0 {
		args.MaxPages = 500
	}
	if args.Concurrency <= 0 {
		args.Concurrency = 3
	}
	if args.Target == "" {
		args.Target = "notes"
	}

	svc := getGlobalNotesService()
	if svc == nil {
		return "", fmt.Errorf("notes service not enabled")
	}

	var urls []string
	switch args.Mode {
	case "sitemap":
		var err error
		urls, err = fetchSitemapURLs(ctx, args.URL)
		if err != nil {
			return "", fmt.Errorf("fetch sitemap: %w", err)
		}
	case "single":
		urls = []string{args.URL}
	default:
		return "", fmt.Errorf("unknown mode: %s", args.Mode)
	}

	// Apply filters.
	urls = filterURLs(urls, args.Include, args.Exclude)

	// Cap at max pages.
	if len(urls) > args.MaxPages {
		urls = urls[:args.MaxPages]
	}

	log.InfoCtx(ctx, "web_crawl starting", "urls", len(urls), "prefix", args.Prefix)

	// Fetch pages concurrently.
	type pageResult struct {
		URL    string
		Status string // "imported", "skipped", "failed"
		Error  string
	}

	results := make([]pageResult, len(urls))
	sem := make(chan struct{}, args.Concurrency)
	var wg sync.WaitGroup

	for i, u := range urls {
		wg.Add(1)
		go func(idx int, pageURL string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			status, err := ingestPage(ctx, svc, pageURL, args.Prefix, args.Dedup)
			results[idx] = pageResult{URL: pageURL, Status: status}
			if err != nil {
				results[idx].Error = err.Error()
			}
		}(i, u)
	}
	wg.Wait()

	// Summarize.
	imported, skipped, failed := 0, 0, 0
	var errors []string
	for _, r := range results {
		switch r.Status {
		case "imported":
			imported++
		case "skipped":
			skipped++
		default:
			failed++
			if r.Error != "" {
				errors = append(errors, fmt.Sprintf("%s: %s", r.URL, r.Error))
			}
		}
	}

	summary := map[string]any{
		"total":    len(urls),
		"imported": imported,
		"skipped":  skipped,
		"failed":   failed,
	}
	if len(errors) > 0 {
		// Cap errors to avoid huge output.
		if len(errors) > 10 {
			errors = errors[:10]
		}
		summary["errors"] = errors
	}

	b, _ := json.Marshal(summary)
	return string(b), nil
}

// fetchSitemapURLs fetches and parses a sitemap (or sitemap index).
func fetchSitemapURLs(ctx context.Context, sitemapURL string) ([]string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", sitemapURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Tetora/2.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024)) // 10MB limit
	if err != nil {
		return nil, err
	}

	content := string(body)

	// Check if this is a sitemap index.
	if strings.Contains(content, "<sitemapindex") {
		return parseSitemapIndex(ctx, content, client)
	}

	return parseSitemapURLs(content), nil
}

// parseSitemapIndex parses a <sitemapindex> and fetches child sitemaps.
func parseSitemapIndex(ctx context.Context, content string, client *http.Client) ([]string, error) {
	// Extract <loc> from <sitemap> entries.
	re := regexp.MustCompile(`<sitemap>[^<]*<loc>([^<]+)</loc>`)
	matches := re.FindAllStringSubmatch(content, -1)

	var allURLs []string
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		childURL := strings.TrimSpace(m[1])
		req, err := http.NewRequestWithContext(ctx, "GET", childURL, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", "Tetora/2.0")
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
		resp.Body.Close()
		if err != nil {
			continue
		}
		urls := parseSitemapURLs(string(body))
		allURLs = append(allURLs, urls...)
	}
	return allURLs, nil
}

// parseSitemapURLs extracts <loc> URLs from a <urlset> sitemap.
func parseSitemapURLs(content string) []string {
	re := regexp.MustCompile(`<url>[^<]*<loc>([^<]+)</loc>`)
	matches := re.FindAllStringSubmatch(content, -1)
	var urls []string
	for _, m := range matches {
		if len(m) >= 2 {
			urls = append(urls, strings.TrimSpace(m[1]))
		}
	}
	return urls
}

// filterURLs applies include/exclude glob patterns to a URL list.
func filterURLs(urls, include, exclude []string) []string {
	if len(include) == 0 && len(exclude) == 0 {
		return urls
	}
	var result []string
	for _, u := range urls {
		// Check exclude first.
		excluded := false
		for _, pat := range exclude {
			if matched, _ := filepath.Match(pat, u); matched {
				excluded = true
				break
			}
			// Also try matching just the path portion.
			if idx := strings.Index(u, "://"); idx >= 0 {
				pathPart := u[idx+3:]
				if matched, _ := filepath.Match(pat, pathPart); matched {
					excluded = true
					break
				}
			}
		}
		if excluded {
			continue
		}

		// Check include (if any patterns specified, URL must match at least one).
		if len(include) > 0 {
			included := false
			for _, pat := range include {
				if matched, _ := filepath.Match(pat, u); matched {
					included = true
					break
				}
				if idx := strings.Index(u, "://"); idx >= 0 {
					pathPart := u[idx+3:]
					if matched, _ := filepath.Match(pat, pathPart); matched {
						included = true
						break
					}
				}
			}
			if !included {
				continue
			}
		}
		result = append(result, u)
	}
	return result
}

// ingestPage fetches a URL, strips HTML, and writes to notes vault.
func ingestPage(ctx context.Context, svc *NotesService, pageURL, prefix string, dedup bool) (string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", pageURL, nil)
	if err != nil {
		return "failed", err
	}
	req.Header.Set("User-Agent", "Tetora/2.0")

	resp, err := client.Do(req)
	if err != nil {
		return "failed", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "failed", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024)) // 5MB limit
	if err != nil {
		return "failed", err
	}

	text := stripHTMLTags(string(body))
	if strings.TrimSpace(text) == "" {
		return "skipped", nil
	}

	// Generate note name from URL.
	slug := urlToSlug(pageURL)
	noteName := slug
	if prefix != "" {
		noteName = prefix + "/" + slug
	}

	// Dedup check.
	if dedup {
		h := sha256.Sum256([]byte(text))
		hash := hex.EncodeToString(h[:16])
		// Check if note already exists with same hash.
		existing, err := svc.ReadNote(noteName)
		if err == nil && existing != "" {
			// Strip frontmatter before hashing to compare body only.
			body := stripFrontmatter(existing)
			existingH := sha256.Sum256([]byte(body))
			existingHash := hex.EncodeToString(existingH[:16])
			if existingHash == hash {
				return "skipped", nil
			}
		}
	}

	// Write as markdown with URL source header.
	content := fmt.Sprintf("---\nsource: %s\nimported: %s\n---\n\n%s", pageURL, time.Now().Format("2006-01-02"), text)
	if err := svc.CreateNote(noteName, content); err != nil {
		return "failed", err
	}

	return "imported", nil
}

// urlToSlug converts a URL to a filesystem-safe slug.
// The slug intentionally avoids dots so ensureExt appends .md reliably.
func urlToSlug(u string) string {
	// Remove scheme.
	slug := u
	if idx := strings.Index(slug, "://"); idx >= 0 {
		slug = slug[idx+3:]
	}
	// Remove query/fragment.
	if idx := strings.IndexAny(slug, "?#"); idx >= 0 {
		slug = slug[:idx]
	}
	// Replace path separators and special chars.
	slug = strings.TrimRight(slug, "/")
	slug = strings.ReplaceAll(slug, "/", "_")
	// Remove non-alphanumeric chars except - _
	// (dots excluded so filepath.Ext returns "" and ensureExt adds .md)
	re := regexp.MustCompile(`[^a-zA-Z0-9_-]+`)
	slug = re.ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "page"
	}
	// Cap length.
	if len(slug) > 100 {
		slug = slug[:100]
	}
	return slug
}

// toolSourceAuditURL compares a sitemap's URLs against imported notes.
func toolSourceAuditURL(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		SitemapURL string `json:"sitemap_url"`
		Prefix     string `json:"prefix"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.SitemapURL == "" {
		return "", fmt.Errorf("sitemap_url is required")
	}

	svc := getGlobalNotesService()
	if svc == nil {
		return "", fmt.Errorf("notes service not enabled")
	}

	// Fetch sitemap URLs.
	urls, err := fetchSitemapURLs(ctx, args.SitemapURL)
	if err != nil {
		return "", fmt.Errorf("fetch sitemap: %w", err)
	}

	// Build expected note names.
	expectedNotes := make(map[string]string) // noteName -> URL
	for _, u := range urls {
		slug := urlToSlug(u)
		noteName := slug
		if args.Prefix != "" {
			noteName = args.Prefix + "/" + slug
		}
		expectedNotes[noteName] = u
	}

	// Check which exist.
	var missing []map[string]string
	existing := 0
	for name, url := range expectedNotes {
		content, err := svc.ReadNote(name)
		if err != nil || content == "" {
			missing = append(missing, map[string]string{"note": name, "url": url})
		} else {
			existing++
		}
	}

	result := map[string]any{
		"total":         len(urls),
		"existing":      existing,
		"missing_count": len(missing),
	}
	// Cap missing list.
	if len(missing) > 50 {
		result["missing"] = missing[:50]
		result["missing_truncated"] = true
	} else {
		result["missing"] = missing
	}

	b, _ := json.Marshal(result)
	return string(b), nil
}

// stripFrontmatter removes YAML frontmatter (--- delimited) from content.
func stripFrontmatter(content string) string {
	if !strings.HasPrefix(content, "---\n") {
		return content
	}
	// Find closing ---.
	end := strings.Index(content[4:], "\n---\n")
	if end < 0 {
		return content
	}
	// Skip frontmatter + trailing newlines.
	body := content[4+end+5:]
	return strings.TrimLeft(body, "\n")
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
