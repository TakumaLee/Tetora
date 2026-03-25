package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"tetora/internal/config"
)

// BridgeTool defines an MCP tool that maps to a Tetora HTTP API endpoint.
type BridgeTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	Method      string          `json:"-"`
	Path        string          `json:"-"`
	PathParams  []string        `json:"-"`
}

// BridgeServer implements the MCP server protocol over stdio.
type BridgeServer struct {
	baseURL string
	token   string
	tools   []BridgeTool
	mu      sync.Mutex
	nextID  int
	version string
}

// NewBridgeServer creates a new MCP bridge server.
// version is the tetora binary version string (e.g. "2.1.0").
func NewBridgeServer(listenAddr, token, version string) *BridgeServer {
	scheme := "http"
	if !strings.HasPrefix(listenAddr, ":") && !strings.Contains(listenAddr, "://") {
		listenAddr = "localhost" + listenAddr
	} else if strings.HasPrefix(listenAddr, ":") {
		listenAddr = "localhost" + listenAddr
	}

	return &BridgeServer{
		baseURL: scheme + "://" + listenAddr,
		token:   token,
		tools:   bridgeTools(),
		version: version,
	}
}

// bridgeTools returns the list of MCP tools exposed by the bridge.
func bridgeTools() []BridgeTool {
	tools := []BridgeTool{
		{
			Name:        "tetora_taskboard_list",
			Description: "List kanban board tickets. Optional filters: project, assignee, priority.",
			Method:      "GET",
			Path:        "/api/tasks/board",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"project":  {"type": "string", "description": "Filter by project name"},
					"assignee": {"type": "string", "description": "Filter by assignee"},
					"priority": {"type": "string", "description": "Filter by priority (P0-P4)"}
				}
			}`),
		},
		{
			Name:        "tetora_taskboard_update",
			Description: "Update a task on the kanban board (status, assignee, priority, etc).",
			Method:      "PATCH",
			Path:        "/api/tasks/{id}",
			PathParams:  []string{"id"},
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"id":       {"type": "string", "description": "Task ID"},
					"status":   {"type": "string", "description": "New status (todo/in_progress/review/done)"},
					"assignee": {"type": "string", "description": "New assignee"},
					"priority": {"type": "string", "description": "New priority (P0-P4)"},
					"title":    {"type": "string", "description": "New title"}
				},
				"required": ["id"]
			}`),
		},
		{
			Name:        "tetora_taskboard_comment",
			Description: "Add a comment to a kanban board task.",
			Method:      "POST",
			Path:        "/api/tasks/{id}/comments",
			PathParams:  []string{"id"},
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"id":      {"type": "string", "description": "Task ID"},
					"comment": {"type": "string", "description": "Comment text"},
					"author":  {"type": "string", "description": "Comment author (agent name)"}
				},
				"required": ["id", "comment"]
			}`),
		},
		{
			Name:        "tetora_memory_get",
			Description: "Read a memory entry for an agent. Returns the stored value.",
			Method:      "GET",
			Path:        "/memory/{agent}/{key}",
			PathParams:  []string{"agent", "key"},
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"agent": {"type": "string", "description": "Agent/role name"},
					"key":   {"type": "string", "description": "Memory key"}
				},
				"required": ["agent", "key"]
			}`),
		},
		{
			Name:        "tetora_memory_set",
			Description: "Write a memory entry for an agent.",
			Method:      "POST",
			Path:        "/memory",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"agent": {"type": "string", "description": "Agent/role name"},
					"key":   {"type": "string", "description": "Memory key"},
					"value": {"type": "string", "description": "Value to store"}
				},
				"required": ["agent", "key", "value"]
			}`),
		},
		{
			Name:        "tetora_memory_search",
			Description: "List all memory entries, optionally filtered by role.",
			Method:      "GET",
			Path:        "/memory",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"role": {"type": "string", "description": "Filter by role/agent name"}
				}
			}`),
		},
		{
			Name:        "tetora_dispatch",
			Description: "Dispatch a task to another agent via Tetora. Creates a new Claude Code session.",
			Method:      "POST",
			Path:        "/dispatch",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"prompt":  {"type": "string", "description": "Task prompt/instructions"},
					"agent":   {"type": "string", "description": "Target agent name"},
					"workdir": {"type": "string", "description": "Working directory for the task"},
					"model":   {"type": "string", "description": "Model to use (optional)"}
				},
				"required": ["prompt"]
			}`),
		},
		{
			Name:        "tetora_knowledge_search",
			Description: "Search the shared knowledge base for relevant information.",
			Method:      "GET",
			Path:        "/knowledge/search",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"q":     {"type": "string", "description": "Search query"},
					"limit": {"type": "integer", "description": "Max results (default 10)"}
				},
				"required": ["q"]
			}`),
		},
		{
			Name:        "tetora_notify",
			Description: "Send a notification to the user via Discord/Telegram.",
			Method:      "POST",
			Path:        "/api/hooks/notify",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"message": {"type": "string", "description": "Notification message"},
					"level":   {"type": "string", "description": "Notification level: info, warn, error (default: info)"}
				},
				"required": ["message"]
			}`),
		},
		{
			Name:        "tetora_ask_user",
			Description: "Ask the user a question via Discord. Use when you need user input. The user will see buttons for options and can also type a custom answer. This blocks until the user responds (up to 6 minutes).",
			Method:      "POST",
			Path:        "/api/hooks/ask-user",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"question": {"type": "string", "description": "The question to ask the user"},
					"options":  {"type": "array", "items": {"type": "string"}, "description": "Optional quick-reply buttons (max 4)"}
				},
				"required": ["question"]
			}`),
		},
	}

	return tools
}

// Run starts the MCP bridge server, reading JSON-RPC from stdin and writing to stdout.
func (s *BridgeServer) Run() error {
	reader := bufio.NewReader(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("read stdin: %w", err)
		}

		line = []byte(strings.TrimSpace(string(line)))
		if len(line) == 0 {
			continue
		}

		var req JSONRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			resp := JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      0,
				Error:   &JSONRPCError{Code: -32700, Message: "parse error"},
			}
			if err := encoder.Encode(resp); err != nil {
				fmt.Fprintf(os.Stderr, "mcp: encode response: %v\n", err)
			}
			continue
		}

		if req.Method == "initialized" || strings.HasPrefix(req.Method, "notifications/") {
			continue
		}

		resp := s.handleRequest(&req)
		if err := encoder.Encode(resp); err != nil {
			fmt.Fprintf(os.Stderr, "mcp: encode response: %v\n", err)
		}
	}
}

func (s *BridgeServer) handleRequest(req *JSONRPCRequest) JSONRPCResponse {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		return s.handleToolsCall(req)
	case "ping":
		return JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{}`)}
	default:
		return JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &JSONRPCError{Code: -32601, Message: "method not found: " + req.Method},
		}
	}
}

func (s *BridgeServer) handleInitialize(req *JSONRPCRequest) JSONRPCResponse {
	result := map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"serverInfo": map[string]any{
			"name":    "tetora",
			"version": s.version,
		},
	}
	data, err := json.Marshal(result)
	if err != nil {
		return s.errorResponse(req.ID, -32603, "internal: marshal failed")
	}
	return JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: data}
}

func (s *BridgeServer) handleToolsList(req *JSONRPCRequest) JSONRPCResponse {
	type toolDef struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		InputSchema json.RawMessage `json:"inputSchema"`
	}

	toolList := make([]toolDef, len(s.tools))
	for i, t := range s.tools {
		toolList[i] = toolDef{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		}
	}

	result := map[string]any{"tools": toolList}
	data, err := json.Marshal(result)
	if err != nil {
		return s.errorResponse(req.ID, -32603, "internal: marshal failed")
	}
	return JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: data}
}

func (s *BridgeServer) handleToolsCall(req *JSONRPCRequest) JSONRPCResponse {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}

	paramData, err := json.Marshal(req.Params)
	if err != nil {
		return s.errorResponse(req.ID, -32602, "invalid params")
	}
	if err := json.Unmarshal(paramData, &params); err != nil {
		return s.errorResponse(req.ID, -32602, "invalid params: "+err.Error())
	}

	var tool *BridgeTool
	for i := range s.tools {
		if s.tools[i].Name == params.Name {
			tool = &s.tools[i]
			break
		}
	}
	if tool == nil {
		return s.errorResponse(req.ID, -32602, "unknown tool: "+params.Name)
	}

	var args map[string]any
	if len(params.Arguments) > 0 {
		if err := json.Unmarshal(params.Arguments, &args); err != nil {
			return s.errorResponse(req.ID, -32602, "invalid arguments: "+err.Error())
		}
	}
	if args == nil {
		args = make(map[string]any)
	}

	path := tool.Path
	for _, p := range tool.PathParams {
		val, ok := args[p]
		if !ok {
			return s.errorResponse(req.ID, -32602, "missing required param: "+p)
		}
		valStr := fmt.Sprint(val)
		if strings.Contains(valStr, "/") {
			return s.errorResponse(req.ID, -32602, fmt.Sprintf("param %q must not contain '/'", p))
		}
		path = strings.Replace(path, "{"+p+"}", url.PathEscape(valStr), 1)
		delete(args, p)
	}

	result, err := s.doHTTP(tool.Method, path, args)
	if err != nil {
		return s.errorResponse(req.ID, -32603, err.Error())
	}

	content := []map[string]any{
		{
			"type": "text",
			"text": string(result),
		},
	}
	respData, err := json.Marshal(map[string]any{"content": content})
	if err != nil {
		return s.errorResponse(req.ID, -32603, "internal: marshal failed")
	}
	return JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: respData}
}

// doHTTP executes an HTTP request against the Tetora API.
func (s *BridgeServer) doHTTP(method, path string, args map[string]any) ([]byte, error) {
	reqURL := s.baseURL + path

	var body io.Reader
	if method == "GET" {
		if len(args) > 0 {
			q := url.Values{}
			for k, v := range args {
				q.Set(k, fmt.Sprint(v))
			}
			reqURL += "?" + q.Encode()
		}
	} else {
		data, err := json.Marshal(args)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		body = strings.NewReader(string(data))
	}

	req, err := http.NewRequest(method, reqURL, body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	req.Header.Set("X-Tetora-Source", "mcp-bridge")

	timeout := 30 * time.Second
	if strings.Contains(path, "/api/hooks/ask-user") || strings.Contains(path, "/api/hooks/plan-gate") {
		timeout = 7 * time.Minute
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

func (s *BridgeServer) errorResponse(id int, code int, msg string) JSONRPCResponse {
	return JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &JSONRPCError{Code: code, Message: msg},
	}
}

// GenerateBridgeConfig creates the ~/.tetora/mcp/bridge.json config file
// that Claude Code uses to connect to the Tetora MCP server.
func GenerateBridgeConfig(cfg *config.Config) error {
	baseDir := cfg.BaseDir
	if baseDir == "" {
		homeDir, _ := os.UserHomeDir()
		baseDir = filepath.Join(homeDir, ".tetora")
	}

	mcpDir := filepath.Join(baseDir, "mcp")
	if err := os.MkdirAll(mcpDir, 0o755); err != nil {
		return fmt.Errorf("create mcp dir: %w", err)
	}

	tetoraPath, err := os.Executable()
	if err != nil {
		tetoraPath = "tetora"
	}

	bridgeConfig := map[string]any{
		"mcpServers": map[string]any{
			"tetora": map[string]any{
				"command": tetoraPath,
				"args":    []string{"mcp-server"},
			},
		},
	}

	data, err := json.MarshalIndent(bridgeConfig, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	configPath := filepath.Join(mcpDir, "bridge.json")
	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	return nil
}
