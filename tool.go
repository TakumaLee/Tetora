package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
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

// --- Built-in Tools ---

func (r *ToolRegistry) registerBuiltins(cfg *Config) {
	// Check which built-in tools are enabled.
	enabled := func(name string) bool {
		if cfg.Tools.Builtin == nil {
			return true // default: all enabled
		}
		e, ok := cfg.Tools.Builtin[name]
		return !ok || e
	}

	if enabled("exec") {
		r.Register(&ToolDef{
			Name:        "exec",
			Description: "Execute a shell command and return stdout, stderr, and exit code",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"command": {"type": "string", "description": "Shell command to execute"},
					"workdir": {"type": "string", "description": "Working directory (optional)"},
					"timeout": {"type": "number", "description": "Timeout in seconds (default 60)"}
				},
				"required": ["command"]
			}`),
			Handler:     toolExec,
			Builtin:     true,
			RequireAuth: true,
		})
	}

	if enabled("read") {
		r.Register(&ToolDef{
			Name:        "read",
			Description: "Read file contents with optional line range",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"path": {"type": "string", "description": "File path to read"},
					"offset": {"type": "number", "description": "Start line (0-indexed, optional)"},
					"limit": {"type": "number", "description": "Number of lines to read (optional)"}
				},
				"required": ["path"]
			}`),
			Handler: toolRead,
			Builtin: true,
		})
	}

	if enabled("write") {
		r.Register(&ToolDef{
			Name:        "write",
			Description: "Write content to a file (creates or overwrites)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"path": {"type": "string", "description": "File path to write"},
					"content": {"type": "string", "description": "Content to write"}
				},
				"required": ["path", "content"]
			}`),
			Handler:     toolWrite,
			Builtin:     true,
			RequireAuth: true,
		})
	}

	if enabled("edit") {
		r.Register(&ToolDef{
			Name:        "edit",
			Description: "Replace text in a file using string substitution",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"path": {"type": "string", "description": "File path to edit"},
					"old_string": {"type": "string", "description": "Text to find"},
					"new_string": {"type": "string", "description": "Replacement text"}
				},
				"required": ["path", "old_string", "new_string"]
			}`),
			Handler:     toolEdit,
			Builtin:     true,
			RequireAuth: true,
		})
	}

	if enabled("web_search") && cfg.Tools.WebSearch.Provider != "" {
		r.Register(&ToolDef{
			Name:        "web_search",
			Description: "Search the web using configured search provider (Brave, Tavily, or SearXNG)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Search query"},
					"maxResults": {"type": "number", "description": "Maximum number of results (default 5)"}
				},
				"required": ["query"]
			}`),
			Handler: toolWebSearch,
			Builtin: true,
		})
	}

	if enabled("web_fetch") {
		r.Register(&ToolDef{
			Name:        "web_fetch",
			Description: "Fetch a URL and return plain text content (HTML tags stripped)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"url": {"type": "string", "description": "URL to fetch"},
					"maxLength": {"type": "number", "description": "Maximum length in characters (default 50000)"}
				},
				"required": ["url"]
			}`),
			Handler: toolWebFetch,
			Builtin: true,
		})
	}

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

	if enabled("session_list") {
		r.Register(&ToolDef{
			Name:        "session_list",
			Description: "List active sessions",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"channel": {"type": "string", "description": "Filter by channel (optional)"}
				}
			}`),
			Handler: toolSessionList,
			Builtin: true,
		})
	}

	if enabled("message") {
		r.Register(&ToolDef{
			Name:        "message",
			Description: "Send a message to a channel (Telegram, Slack, Discord)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"channel": {"type": "string", "description": "Channel type: telegram, slack, discord"},
					"message": {"type": "string", "description": "Message content"}
				},
				"required": ["channel", "message"]
			}`),
			Handler: toolMessage,
			Builtin: true,
		})
	}

	if enabled("cron_list") {
		r.Register(&ToolDef{
			Name:        "cron_list",
			Description: "List scheduled cron jobs",
			InputSchema: json.RawMessage(`{"type": "object"}`),
			Handler:     toolCronList,
			Builtin:     true,
		})
	}

	if enabled("cron_create") {
		r.Register(&ToolDef{
			Name:        "cron_create",
			Description: "Create or update a cron job",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"name": {"type": "string", "description": "Job name"},
					"schedule": {"type": "string", "description": "Cron schedule or interval (e.g., '@hourly', '*/5m')"},
					"prompt": {"type": "string", "description": "Task prompt"},
					"role": {"type": "string", "description": "Agent role (optional)"}
				},
				"required": ["name", "schedule", "prompt"]
			}`),
			Handler:     toolCronCreate,
			Builtin:     true,
			RequireAuth: true,
		})
	}

	if enabled("cron_delete") {
		r.Register(&ToolDef{
			Name:        "cron_delete",
			Description: "Delete a cron job by name",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"name": {"type": "string", "description": "Job name to delete"}
				},
				"required": ["name"]
			}`),
			Handler:     toolCronDelete,
			Builtin:     true,
			RequireAuth: true,
		})
	}

	if enabled("agent_list") {
		r.Register(&ToolDef{
			Name:        "agent_list",
			Description: "List available agents/roles with their capabilities",
			InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`),
			Handler:     toolAgentList,
			Builtin:     true,
		})
	}

	if enabled("agent_dispatch") {
		r.Register(&ToolDef{
			Name:        "agent_dispatch",
			Description: "Dispatch a sub-task to another agent and wait for the result",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"role": {"type": "string", "description": "Target agent role name"},
					"prompt": {"type": "string", "description": "Task prompt to send"},
					"timeout": {"type": "number", "description": "Timeout in seconds (default 300)"}
				},
				"required": ["role", "prompt"]
			}`),
			Handler:     toolAgentDispatch,
			Builtin:     true,
		})
	}

	if enabled("agent_message") {
		r.Register(&ToolDef{
			Name:        "agent_message",
			Description: "Send an async message to another agent's session",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"role": {"type": "string", "description": "Target agent role name"},
					"message": {"type": "string", "description": "Message content"},
					"sessionId": {"type": "string", "description": "Target session ID (optional)"}
				},
				"required": ["role", "message"]
			}`),
			Handler:     toolAgentMessage,
			Builtin:     true,
		})
	}

	// --- P13.1: Plugin System --- Code Mode meta-tools.
	if enabled("search_tools") {
		r.Register(&ToolDef{
			Name:        "search_tools",
			Description: "Search available tools by keyword (name or description). Use when there are many tools and you need to find the right one.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Keyword to search for in tool names and descriptions"},
					"limit": {"type": "number", "description": "Maximum results to return (default 10)"}
				},
				"required": ["query"]
			}`),
			Handler: toolSearchTools,
			Builtin: true,
		})
	}

	if enabled("execute_tool") {
		r.Register(&ToolDef{
			Name:        "execute_tool",
			Description: "Execute any registered tool by name with given input. Use with search_tools to discover and run tools dynamically.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"name": {"type": "string", "description": "Tool name to execute"},
					"input": {"type": "object", "description": "Input parameters for the tool"}
				},
				"required": ["name"]
			}`),
			Handler:     toolExecuteTool,
			Builtin:     true,
			RequireAuth: true,
		})
	}
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

// toolSessionList lists active sessions.
func toolSessionList(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Channel string `json:"channel"`
	}
	json.Unmarshal(input, &args)

	query := `SELECT session_id, channel_type, channel_id, message_count, created_at, updated_at FROM sessions WHERE 1=1`
	if args.Channel != "" {
		query += fmt.Sprintf(` AND channel_type = '%s'`, escapeSQLite(args.Channel))
	}
	query += ` ORDER BY updated_at DESC LIMIT 20`

	rows, err := queryDB(cfg.HistoryDB, query)
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
			"role":     j.Role,
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
		Role     string `json:"role"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
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
			jobs[i].Role = args.Role
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
			Role:     args.Role,
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
