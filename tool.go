package main

import (
	"context"
	"encoding/json"
	"sync"
)

// --- Tool Types ---

// ToolDef defines a tool that can be called by agents.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
	Handler     ToolHandler     `json:"-"`
	Builtin     bool            `json:"-"`
	RequireAuth bool            `json:"requireAuth,omitempty"`
}

// ToolCall represents a tool invocation request from the provider.
type ToolCall struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// ToolResult represents the result of a tool execution.
type ToolResult struct {
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error,omitempty"`
}

// ToolHandler is a function that executes a tool.
type ToolHandler func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error)

// --- Tool Registry ---

// ToolRegistry manages available tools.
type ToolRegistry struct {
	mu    sync.RWMutex
	tools map[string]*ToolDef
}

// NewToolRegistry creates a new tool registry with built-in tools.
func NewToolRegistry(cfg *Config) *ToolRegistry {
	r := &ToolRegistry{
		tools: make(map[string]*ToolDef),
	}
	r.registerBuiltins(cfg)
	return r
}

// Register adds a tool to the registry.
func (r *ToolRegistry) Register(tool *ToolDef) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[tool.Name] = tool
}

// Get retrieves a tool by name.
func (r *ToolRegistry) Get(name string) (*ToolDef, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// List returns all registered tools.
func (r *ToolRegistry) List() []*ToolDef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]*ToolDef, 0, len(r.tools))
	for _, t := range r.tools {
		result = append(result, t)
	}
	return result
}

// ListFiltered returns tools whose Name is in the allowed map.
// If allowed is nil or empty, returns all tools (backward compat).
func (r *ToolRegistry) ListFiltered(allowed map[string]bool) []*ToolDef {
	if len(allowed) == 0 {
		return r.List()
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]*ToolDef, 0, len(allowed))
	for _, t := range r.tools {
		if allowed[t.Name] {
			result = append(result, t)
		}
	}
	return result
}

// ListForProvider serializes tools for API calls (no Handler field).
func (r *ToolRegistry) ListForProvider() []map[string]any {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]map[string]any, 0, len(r.tools))
	for _, t := range r.tools {
		var schema map[string]any
		if len(t.InputSchema) > 0 {
			json.Unmarshal(t.InputSchema, &schema)
		}
		result = append(result, map[string]any{
			"name":         t.Name,
			"description":  t.Description,
			"input_schema": schema,
		})
	}
	return result
}

// --- Tool Profiles ---

// toolProfileSets defines which tools are included in each profile.
var toolProfileSets = map[string][]string{
	"minimal": {
		"memory_get", "memory_search", "knowledge_search",
		"web_search", "agent_dispatch",
	},
	"standard": {
		"memory_get", "memory_search", "memory_store", "memory_recall",
		"memory_um_search", "memory_forget",
		"knowledge_search", "web_search", "web_fetch",
		"agent_dispatch", "lesson_record",
		"task_create", "task_list", "task_update",
		"file_read", "file_write",
	},
	// "full" = all tools (no filtering)
}

// ToolsForProfile returns the allowed tool set for a given profile name.
// Returns nil for "full" or unknown profiles (which means all tools).
func ToolsForProfile(profile string) map[string]bool {
	tools, ok := toolProfileSets[profile]
	if !ok || profile == "full" {
		return nil // nil = all tools
	}
	allowed := make(map[string]bool, len(tools))
	for _, t := range tools {
		allowed[t] = true
	}
	return allowed
}

// ToolsForComplexity returns the tool profile name appropriate for the given request complexity.
func ToolsForComplexity(c RequestComplexity) string {
	switch c {
	case ComplexitySimple:
		return "none"
	case ComplexityStandard:
		return "standard"
	default:
		return "full"
	}
}

// --- Built-in Tools ---

func (r *ToolRegistry) registerBuiltins(cfg *Config) {
	enabled := func(name string) bool {
		if cfg.Tools.Builtin == nil {
			return true
		}
		e, ok := cfg.Tools.Builtin[name]
		return !ok || e
	}
	registerCoreTools(r, cfg, enabled)
	registerMemoryTools(r, cfg, enabled)
	registerLifeTools(r, cfg, enabled)
	registerIntegrationTools(r, cfg, enabled)
	registerDailyTools(r, cfg, enabled)
	registerAdminTools(r, cfg, enabled)
}
