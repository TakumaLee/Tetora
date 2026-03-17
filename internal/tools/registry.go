// Package tools provides the tool registry and handler types for Tetora agents.
package tools

import (
	"context"
	"encoding/json"
	"sync"

	"tetora/internal/classify"
	"tetora/internal/config"
	"tetora/internal/provider"
)

// ToolDef defines a tool that can be called by agents.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
	Handler     Handler         `json:"-"`
	Builtin     bool            `json:"-"`
	RequireAuth bool            `json:"requireAuth,omitempty"`
}

// ToolCall is an alias for provider.ToolCall.
type ToolCall = provider.ToolCall

// Result represents the result of a tool execution.
type Result struct {
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error,omitempty"`
}

// Handler is a function that executes a tool.
type Handler func(ctx context.Context, cfg *config.Config, input json.RawMessage) (string, error)

// Registry manages available tools.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]*ToolDef
}

// NewRegistry creates a new empty tool registry.
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]*ToolDef),
	}
}

// Register adds a tool to the registry.
func (r *Registry) Register(tool *ToolDef) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[tool.Name] = tool
}

// Get retrieves a tool by name.
func (r *Registry) Get(name string) (*ToolDef, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// List returns all registered tools.
func (r *Registry) List() []*ToolDef {
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
func (r *Registry) ListFiltered(allowed map[string]bool) []*ToolDef {
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

// Range calls fn for each tool in the registry. If fn returns false, iteration stops.
func (r *Registry) Range(fn func(*ToolDef) bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, t := range r.tools {
		if !fn(t) {
			return
		}
	}
}

// ListForProvider serializes tools for API calls (no Handler field).
func (r *Registry) ListForProvider() []map[string]any {
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

// ProfileSets defines which tools are included in each profile.
var ProfileSets = map[string][]string{
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
		"taskboard_list", "taskboard_get", "taskboard_create",
		"taskboard_move", "taskboard_comment", "taskboard_decompose",
	},
	// "full" = all tools (no filtering)
}

// ForProfile returns the allowed tool set for a given profile name.
// Returns nil for "full" or unknown profiles (which means all tools).
func ForProfile(profile string) map[string]bool {
	tools, ok := ProfileSets[profile]
	if !ok || profile == "full" {
		return nil // nil = all tools
	}
	allowed := make(map[string]bool, len(tools))
	for _, t := range tools {
		allowed[t] = true
	}
	return allowed
}

// ForComplexity returns the tool profile name appropriate for the given request complexity.
func ForComplexity(c classify.Complexity) string {
	switch c {
	case classify.Simple:
		return "none"
	case classify.Standard:
		return "standard"
	default:
		return "full"
	}
}
