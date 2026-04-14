// Package tools provides the tool registry and handler types for Tetora agents.
package tools

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"tetora/internal/bm25"
	"tetora/internal/classify"
	"tetora/internal/config"
	"tetora/internal/provider"
)

// ToolDef defines a tool that can be called by agents.
type ToolDef struct {
	Name              string          `json:"name"`
	Description       string          `json:"description"`
	ContextualSummary string          `json:"-"` // AI-generated contextual summary for better retrieval
	InputSchema       json.RawMessage `json:"input_schema"`
	Keywords          []string        `json:"-"` // Extra searchable keywords for BM25
	DeferLoading      bool            `json:"-"` // When true, tool is deferred (loaded on-demand via search_tools)
	Handler           Handler         `json:"-"`
	Builtin           bool            `json:"-"`
	RequireAuth       bool            `json:"requireAuth,omitempty"`
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
	mu         sync.RWMutex
	tools      map[string]*ToolDef
	bm25Index  *bm25.BM25
	reranker   bm25.Reranker
	usageCount map[string]int // tool name -> call count (for reranking boost)
}

// NewRegistry creates a new empty tool registry.
func NewRegistry() *Registry {
	return &Registry{
		tools:      make(map[string]*ToolDef),
		reranker:   bm25.NewHeuristicReranker(bm25.DefaultRerankConfig()),
		usageCount: make(map[string]int),
	}
}

// SetReranker replaces the default reranker (for using external/neural rerankers).
func (r *Registry) SetReranker(rk bm25.Reranker) {
	r.mu.Lock()
	r.reranker = rk
	r.mu.Unlock()
}

// Register adds a tool to the registry and marks the BM25 index as dirty.
// The index is rebuilt lazily on the next SearchBM25 call.
func (r *Registry) Register(tool *ToolDef) {
	r.mu.Lock()
	r.tools[tool.Name] = tool
	r.bm25Index = nil // mark dirty; rebuilt lazily in SearchBM25
	r.mu.Unlock()
}

// rebuildBM25IndexLocked rebuilds the BM25 index from all registered tools.
// Must be called with r.mu held (write lock).
func (r *Registry) rebuildBM25IndexLocked() {
	docs := make([]bm25.Document, 0, len(r.tools))
	for _, t := range r.tools {
		// Build searchable text: name + description + contextual summary + keywords
		var parts []string
		// Split underscored names into separate terms for better matching
		nameTerms := strings.ReplaceAll(t.Name, "_", " ")
		parts = append(parts, nameTerms)
		parts = append(parts, t.Description)
		if t.ContextualSummary != "" {
			parts = append(parts, t.ContextualSummary)
		}
		parts = append(parts, strings.Join(t.Keywords, " "))
		docs = append(docs, bm25.Document{
			ID:    t.Name,
			Terms: bm25.Tokenize(strings.Join(parts, " ")),
		})
	}
	r.bm25Index = bm25.New(docs, bm25.DefaultK1, bm25.DefaultB)
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

// RecordUsage increments the call count for a tool (used in reranking bonus).
func (r *Registry) RecordUsage(name string) {
	r.mu.Lock()
	r.usageCount[name]++
	r.mu.Unlock()
}

// GetUsage returns the call count for a tool.
func (r *Registry) GetUsage(name string) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.usageCount[name]
}

// SearchResult holds a tool search result with its BM25 score.
type SearchResult struct {
	Tool        *ToolDef
	BM25Score   float64
	FinalScore  float64
}

// SearchBM25 searches tools using two-stage reranking (BM25 recall → rerank).
// Returns results sorted by final reranked score. If topN <= 0, returns all matching.
// ctx is forwarded to the reranker (e.g. for cancelling external HTTP calls).
func (r *Registry) SearchBM25(ctx context.Context, query string, topN int) []SearchResult {
	// Lazy index build: check under read lock, then upgrade to write lock if needed.
	r.mu.RLock()
	needsBuild := r.bm25Index == nil
	r.mu.RUnlock()

	if needsBuild {
		r.mu.Lock()
		if r.bm25Index == nil { // double-check after acquiring write lock
			r.rebuildBM25IndexLocked()
		}
		r.mu.Unlock()
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	terms := bm25.Tokenize(query)

	// Stage 1: BM25 recall with a larger candidate set.
	recallN := topN * 4
	if recallN < 20 {
		recallN = 20
	}
	bm25Results := r.bm25Index.Search(terms, recallN)
	if len(bm25Results) == 0 {
		return nil
	}

	// Stage 2: Rerank with pluggable reranker.
	getMeta := func(docID string) bm25.DocMeta {
		t, ok := r.tools[docID]
		if !ok {
			return bm25.DocMeta{}
		}
		return bm25.DocMeta{
			Name:              t.Name,
			Description:       t.Description,
			ContextualSummary: t.ContextualSummary,
			Keywords:          t.Keywords,
			DocLen:            len(bm25.Tokenize(t.Description)),
			UsageCount:        r.usageCount[t.Name],
		}
	}

	reranked := r.reranker.Rerank(ctx, query, terms, bm25Results, getMeta)

	out := make([]SearchResult, 0, len(reranked))
	for _, res := range reranked {
		if t, ok := r.tools[res.ID]; ok {
			out = append(out, SearchResult{
				Tool:       t,
				BM25Score:  res.BM25Score,
				FinalScore: res.FinalScore,
			})
		}
	}

	if topN > 0 && topN < len(out) {
		out = out[:topN]
	}
	return out
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

// --- Deferred Tool Loading ---

// alwaysLoadedTools are tools that must always be available in the provider
// request. These are the core discovery/execution tools that enable the agent
// to find and use any other deferred tool. All other tools are marked with
// defer_loading=true so the provider loads them on-demand via search.
var alwaysLoadedTools = map[string]bool{
	"search_tools":     true, // BM25 tool discovery
	"execute_tool":     true, // Execute any tool by name
	"memory_search":    true, // Memory lookup (frequently used)
	"web_search":       true, // Web lookup (frequently used)
	"knowledge_search": true, // Knowledge base lookup
}

// IsAlwaysLoaded reports whether the named tool is in the always-loaded set
// (i.e. it will never be marked deferred by ApplyDeferredPolicy).
func IsAlwaysLoaded(name string) bool {
	return alwaysLoadedTools[name]
}

// AlwaysLoadedCount returns the number of tools in the always-loaded set.
// Useful for test assertions without exposing the underlying map.
func AlwaysLoadedCount() int {
	return len(alwaysLoadedTools)
}

// ApplyDeferredPolicy marks all tools NOT in alwaysLoadedTools with DeferLoading=true.
// This should be called after all tools are registered, before starting the agent loop.
func (r *Registry) ApplyDeferredPolicy() {
	r.mu.Lock()
	defer r.mu.Unlock()

	for name, t := range r.tools {
		if !alwaysLoadedTools[name] {
			t.DeferLoading = true
		}
	}
}
