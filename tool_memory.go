package main

import (
	"context"
	"encoding/json"
	"fmt"
)

// registerMemoryTools registers memory and knowledge tools.
func registerMemoryTools(r *ToolRegistry, cfg *Config, enabled func(string) bool) {
	if enabled("memory_search") {
		r.Register(&ToolDef{
			Name:        "memory_search",
			Description: "Search agent memory by query",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Search query"},
					"role": {"type": "string", "description": "Filter by role name (optional)"}
				},
				"required": ["query"]
			}`),
			Handler: toolMemorySearch,
			Builtin: true,
		})
	}

	if enabled("memory_get") {
		r.Register(&ToolDef{
			Name:        "memory_get",
			Description: "Get a specific memory value by key",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"key": {"type": "string", "description": "Memory key"},
					"role": {"type": "string", "description": "Role name (optional)"}
				},
				"required": ["key"]
			}`),
			Handler: toolMemoryGet,
			Builtin: true,
		})
	}

	// --- P23.0: Unified Memory Tools ---
	if enabled("memory_store") {
		r.Register(&ToolDef{
			Name:        "memory_store",
			Description: "Store a memory entry with automatic deduplication and versioning. Uses the unified memory layer as single source of truth.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"namespace": {"type": "string", "description": "Category: fact, preference, episode, emotion, file, reflection"},
					"key": {"type": "string", "description": "Canonical key for this memory"},
					"value": {"type": "string", "description": "Memory content"},
					"scope": {"type": "string", "description": "Role name or empty for global (optional)"},
					"source": {"type": "string", "description": "Origin identifier (optional)"},
					"ttlDays": {"type": "number", "description": "Auto-expire after N days, 0=never (optional)"}
				},
				"required": ["namespace", "key", "value"]
			}`),
			Handler: toolUMStore,
			Builtin: true,
		})
	}

	if enabled("memory_recall") {
		r.Register(&ToolDef{
			Name:        "memory_recall",
			Description: "Recall a specific memory by namespace, scope, and key from unified memory",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"namespace": {"type": "string", "description": "Category: fact, preference, episode, emotion, file, reflection"},
					"key": {"type": "string", "description": "Memory key"},
					"scope": {"type": "string", "description": "Role name or empty for global (optional)"}
				},
				"required": ["namespace", "key"]
			}`),
			Handler: toolUMRecall,
			Builtin: true,
		})
	}

	if enabled("memory_um_search") {
		r.Register(&ToolDef{
			Name:        "memory_um_search",
			Description: "Search unified memory entries by query with optional namespace/scope filters",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Search query (matches key and value)"},
					"namespace": {"type": "string", "description": "Filter by namespace (optional)"},
					"scope": {"type": "string", "description": "Filter by scope/role (optional)"},
					"limit": {"type": "number", "description": "Max results (default 10)"}
				},
				"required": ["query"]
			}`),
			Handler: toolUMSearch,
			Builtin: true,
		})
	}

	if enabled("memory_history") {
		r.Register(&ToolDef{
			Name:        "memory_history",
			Description: "View version history of a unified memory entry",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"id": {"type": "string", "description": "Memory entry ID"},
					"limit": {"type": "number", "description": "Max versions to return (default 10)"}
				},
				"required": ["id"]
			}`),
			Handler: toolUMHistory,
			Builtin: true,
		})
	}

	if enabled("memory_link") {
		r.Register(&ToolDef{
			Name:        "memory_link",
			Description: "Create a cross-reference link between two memory entries",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"sourceId": {"type": "string", "description": "Source memory ID"},
					"targetId": {"type": "string", "description": "Target memory ID"},
					"linkType": {"type": "string", "description": "Link type: related, supersedes, derived_from, contradicts"}
				},
				"required": ["sourceId", "targetId", "linkType"]
			}`),
			Handler: toolUMLink,
			Builtin: true,
		})
	}

	if enabled("memory_forget") {
		r.Register(&ToolDef{
			Name:        "memory_forget",
			Description: "Soft-delete (tombstone) a unified memory entry. Can be recovered within retention period.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"id": {"type": "string", "description": "Memory entry ID to forget"},
					"namespace": {"type": "string", "description": "Alternative: forget by namespace+scope+key"},
					"key": {"type": "string", "description": "Alternative: forget by namespace+scope+key"},
					"scope": {"type": "string", "description": "Scope for key-based forget (optional)"}
				}
			}`),
			Handler: toolUMForget,
			Builtin: true,
		})
	}

	if enabled("knowledge_search") {
		r.Register(&ToolDef{
			Name:        "knowledge_search",
			Description: "Search knowledge base using TF-IDF",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Search query"},
					"limit": {"type": "number", "description": "Max results (default 5)"}
				},
				"required": ["query"]
			}`),
			Handler: toolKnowledgeSearch,
			Builtin: true,
		})
	}
}

// --- Memory Tool Handlers ---

// toolMemorySearch searches agent memory.
func toolMemorySearch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Query string `json:"query"`
		Role  string `json:"role"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Query == "" {
		return "", fmt.Errorf("query is required")
	}

	// Build SQL query.
	query := fmt.Sprintf(`SELECT key, value, role FROM agent_memory WHERE value LIKE '%%%s%%'`, escapeSQLite(args.Query))
	if args.Role != "" {
		query += fmt.Sprintf(` AND role = '%s'`, escapeSQLite(args.Role))
	}
	query += ` ORDER BY updated_at DESC LIMIT 10`

	rows, err := queryDB(cfg.HistoryDB, query)
	if err != nil {
		return "", fmt.Errorf("query failed: %w", err)
	}

	var results []map[string]string
	for _, row := range rows {
		results = append(results, map[string]string{
			"key":   fmt.Sprintf("%v", row["key"]),
			"value": fmt.Sprintf("%v", row["value"]),
			"role":  fmt.Sprintf("%v", row["role"]),
		})
	}

	b, _ := json.Marshal(results)
	return string(b), nil
}

// toolMemoryGet retrieves a specific memory value.
func toolMemoryGet(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Key  string `json:"key"`
		Role string `json:"role"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Key == "" {
		return "", fmt.Errorf("key is required")
	}

	query := fmt.Sprintf(`SELECT value FROM agent_memory WHERE key = '%s'`, escapeSQLite(args.Key))
	if args.Role != "" {
		query += fmt.Sprintf(` AND role = '%s'`, escapeSQLite(args.Role))
	}
	query += ` LIMIT 1`

	rows, err := queryDB(cfg.HistoryDB, query)
	if err != nil {
		return "", fmt.Errorf("query failed: %w", err)
	}
	if len(rows) == 0 {
		return "", fmt.Errorf("key not found")
	}

	return fmt.Sprintf("%v", rows[0]["value"]), nil
}

// toolKnowledgeSearch searches the knowledge base using existing TF-IDF search.
func toolKnowledgeSearch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Query == "" {
		return "", fmt.Errorf("query is required")
	}
	if args.Limit <= 0 {
		args.Limit = 5
	}

	// Use existing knowledge search function if available, otherwise fallback to simple DB query.
	query := fmt.Sprintf(`SELECT filename, snippet FROM knowledge WHERE content LIKE '%%%s%%' ORDER BY indexed_at DESC LIMIT %d`,
		escapeSQLite(args.Query), args.Limit)
	rows, err := queryDB(cfg.HistoryDB, query)
	if err != nil {
		return "", fmt.Errorf("query failed: %w", err)
	}

	var results []map[string]any
	for _, row := range rows {
		results = append(results, map[string]any{
			"filename": fmt.Sprintf("%v", row["filename"]),
			"snippet":  fmt.Sprintf("%v", row["snippet"]),
		})
	}

	b, _ := json.Marshal(results)
	return string(b), nil
}

// --- P23.0: Unified Memory Tool Handlers ---

func toolUMStore(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Namespace string `json:"namespace"`
		Key       string `json:"key"`
		Value     string `json:"value"`
		Scope     string `json:"scope"`
		Source    string `json:"source"`
		TTLDays   int    `json:"ttlDays"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Namespace == "" || args.Key == "" || args.Value == "" {
		return "", fmt.Errorf("namespace, key, and value are required")
	}
	if args.Source == "" {
		args.Source = "tool"
	}

	entry := UnifiedMemoryEntry{
		Namespace: args.Namespace,
		Scope:     args.Scope,
		Key:       args.Key,
		Value:     args.Value,
		Source:    args.Source,
		TTLDays:   args.TTLDays,
	}

	id, created, err := umStore(cfg.HistoryDB, entry)
	if err != nil {
		return "", fmt.Errorf("store failed: %w", err)
	}

	action := "updated"
	if created {
		action = "created"
	}
	result := map[string]any{"id": id, "action": action}
	b, _ := json.Marshal(result)
	return string(b), nil
}

func toolUMRecall(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Namespace string `json:"namespace"`
		Key       string `json:"key"`
		Scope     string `json:"scope"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Namespace == "" || args.Key == "" {
		return "", fmt.Errorf("namespace and key are required")
	}

	entry, err := umGet(cfg.HistoryDB, args.Namespace, args.Scope, args.Key)
	if err != nil {
		return "", fmt.Errorf("recall failed: %w", err)
	}
	if entry == nil {
		return "", fmt.Errorf("memory not found")
	}

	b, _ := json.Marshal(entry)
	return string(b), nil
}

func toolUMSearch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Query     string `json:"query"`
		Namespace string `json:"namespace"`
		Scope     string `json:"scope"`
		Limit     int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Query == "" {
		return "", fmt.Errorf("query is required")
	}
	if args.Limit <= 0 {
		args.Limit = 10
	}

	entries, err := umSearch(cfg.HistoryDB, args.Query, args.Namespace, args.Scope, args.Limit)
	if err != nil {
		return "", fmt.Errorf("search failed: %w", err)
	}

	b, _ := json.Marshal(entries)
	return string(b), nil
}

func toolUMHistory(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		ID    string `json:"id"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.ID == "" {
		return "", fmt.Errorf("id is required")
	}
	if args.Limit <= 0 {
		args.Limit = 10
	}

	versions, err := umHistory(cfg.HistoryDB, args.ID, args.Limit)
	if err != nil {
		return "", fmt.Errorf("history failed: %w", err)
	}

	b, _ := json.Marshal(versions)
	return string(b), nil
}

func toolUMLink(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		SourceID string `json:"sourceId"`
		TargetID string `json:"targetId"`
		LinkType string `json:"linkType"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.SourceID == "" || args.TargetID == "" || args.LinkType == "" {
		return "", fmt.Errorf("sourceId, targetId, and linkType are required")
	}

	if err := umLink(cfg.HistoryDB, args.SourceID, args.TargetID, args.LinkType); err != nil {
		return "", fmt.Errorf("link failed: %w", err)
	}

	return fmt.Sprintf("linked %s -> %s (%s)", args.SourceID, args.TargetID, args.LinkType), nil
}

func toolUMForget(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		ID        string `json:"id"`
		Namespace string `json:"namespace"`
		Key       string `json:"key"`
		Scope     string `json:"scope"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	// By ID
	if args.ID != "" {
		if err := umDelete(cfg.HistoryDB, args.ID); err != nil {
			return "", fmt.Errorf("forget failed: %w", err)
		}
		return fmt.Sprintf("memory %s tombstoned", args.ID), nil
	}

	// By namespace+key
	if args.Namespace != "" && args.Key != "" {
		entry, err := umGet(cfg.HistoryDB, args.Namespace, args.Scope, args.Key)
		if err != nil {
			return "", fmt.Errorf("lookup failed: %w", err)
		}
		if entry == nil {
			return "", fmt.Errorf("memory not found")
		}
		if err := umDelete(cfg.HistoryDB, entry.ID); err != nil {
			return "", fmt.Errorf("forget failed: %w", err)
		}
		return fmt.Sprintf("memory %s tombstoned", entry.ID), nil
	}

	return "", fmt.Errorf("either id or namespace+key is required")
}
