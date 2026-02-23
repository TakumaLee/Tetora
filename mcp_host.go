package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

// --- MCP Host Types ---

// MCPHost manages the lifecycle of MCP server processes.
type MCPHost struct {
	servers map[string]*MCPServer
	mu      sync.RWMutex
	cfg     *Config
	toolReg *ToolRegistry
}

// MCPServer represents a single MCP server process.
type MCPServer struct {
	Name      string
	Command   string
	Args      []string
	Env       map[string]string
	Cmd       *exec.Cmd
	Stdin     io.WriteCloser
	Stdout    *bufio.Reader
	Tools     []ToolDef
	mu        sync.Mutex
	nextID    int
	status    string // "starting", "running", "stopped", "error"
	lastError string
	restarts  int
	ctx       context.Context
	cancel    context.CancelFunc
	toolReg   *ToolRegistry
}

// MCPServerStatus provides status information for API endpoints.
type MCPServerStatus struct {
	Name      string   `json:"name"`
	Status    string   `json:"status"` // "running", "stopped", "error"
	Tools     []string `json:"tools"`
	Restarts  int      `json:"restarts"`
	LastError string   `json:"lastError,omitempty"`
}

// --- JSON-RPC 2.0 Types ---

type jsonRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id,omitempty"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type initializeParams struct {
	ProtocolVersion string                 `json:"protocolVersion"`
	Capabilities    map[string]interface{} `json:"capabilities"`
	ClientInfo      struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"clientInfo"`
}

type initializeResult struct {
	ProtocolVersion string                 `json:"protocolVersion"`
	Capabilities    map[string]interface{} `json:"capabilities"`
	ServerInfo      struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"serverInfo"`
}

type toolsListResult struct {
	Tools []struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		InputSchema json.RawMessage `json:"inputSchema"`
	} `json:"tools"`
}

type toolsCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type toolsCallResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text,omitempty"`
	} `json:"content"`
}

// --- MCP Host Methods ---

// newMCPHost creates a new MCP host.
func newMCPHost(cfg *Config, toolReg *ToolRegistry) *MCPHost {
	return &MCPHost{
		servers: make(map[string]*MCPServer),
		cfg:     cfg,
		toolReg: toolReg,
	}
}

// Start initializes and starts all configured MCP servers.
func (h *MCPHost) Start(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	for name, serverCfg := range h.cfg.MCPServers {
		// Check if explicitly disabled.
		if serverCfg.Enabled != nil && !*serverCfg.Enabled {
			logInfo("MCP server %s disabled, skipping", name)
			continue
		}

		server := &MCPServer{
			Name:    name,
			Command: serverCfg.Command,
			Args:    serverCfg.Args,
			Env:     serverCfg.Env,
			status:  "starting",
			toolReg: h.toolReg,
		}
		server.ctx, server.cancel = context.WithCancel(ctx)

		h.servers[name] = server

		// Start in background.
		go func(s *MCPServer) {
			if err := s.start(s.ctx); err != nil {
				s.mu.Lock()
				s.status = "error"
				s.lastError = err.Error()
				s.mu.Unlock()
				logError("MCP server %s failed to start: %v", s.Name, err)
				return
			}

			// Monitor health.
			go s.monitorHealth()
		}(server)
	}

	return nil
}

// Stop shuts down all MCP servers.
func (h *MCPHost) Stop() {
	h.mu.Lock()
	defer h.mu.Unlock()

	for _, server := range h.servers {
		server.stop()
	}
}

// RestartServer restarts a specific MCP server.
func (h *MCPHost) RestartServer(name string) error {
	h.mu.Lock()
	server, ok := h.servers[name]
	h.mu.Unlock()

	if !ok {
		return fmt.Errorf("server %s not found", name)
	}

	// Stop existing.
	server.stop()

	// Restart.
	server.mu.Lock()
	server.status = "starting"
	server.restarts++
	server.ctx, server.cancel = context.WithCancel(context.Background())
	server.mu.Unlock()

	go func() {
		if err := server.start(server.ctx); err != nil {
			server.mu.Lock()
			server.status = "error"
			server.lastError = err.Error()
			server.mu.Unlock()
			logError("MCP server %s restart failed: %v", server.Name, err)
			return
		}
		go server.monitorHealth()
	}()

	return nil
}

// ServerStatus returns the status of all MCP servers.
func (h *MCPHost) ServerStatus() []MCPServerStatus {
	h.mu.RLock()
	defer h.mu.RUnlock()

	result := make([]MCPServerStatus, 0, len(h.servers))
	for _, server := range h.servers {
		server.mu.Lock()
		toolNames := make([]string, len(server.Tools))
		for i, t := range server.Tools {
			toolNames[i] = t.Name
		}
		result = append(result, MCPServerStatus{
			Name:      server.Name,
			Status:    server.status,
			Tools:     toolNames,
			Restarts:  server.restarts,
			LastError: server.lastError,
		})
		server.mu.Unlock()
	}

	return result
}

// getServer retrieves a server by name.
func (h *MCPHost) getServer(name string) *MCPServer {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.servers[name]
}

// --- MCP Server Methods ---

// start spawns the MCP server process and initializes the connection.
func (s *MCPServer) start(ctx context.Context) error {
	// Build command.
	cmd := exec.CommandContext(ctx, s.Command, s.Args...)

	// Set environment.
	cmd.Env = os.Environ()
	for k, v := range s.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	// Setup pipes.
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	s.Stdin = stdin

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	s.Stdout = bufio.NewReader(stdout)

	// Redirect stderr to our logs.
	cmd.Stderr = &mcpStderrWriter{serverName: s.Name}

	// Start process.
	s.Cmd = cmd
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start process: %w", err)
	}

	logInfo("MCP server %s started (PID %d)", s.Name, cmd.Process.Pid)

	// Initialize handshake.
	if err := s.initialize(); err != nil {
		s.Cmd.Process.Kill()
		return fmt.Errorf("initialize: %w", err)
	}

	// Discover tools.
	tools, err := s.discoverTools()
	if err != nil {
		s.Cmd.Process.Kill()
		return fmt.Errorf("discover tools: %w", err)
	}

	s.mu.Lock()
	s.Tools = tools
	s.status = "running"
	s.mu.Unlock()

	logInfo("MCP server %s running with %d tools", s.Name, len(tools))

	return nil
}

// stop gracefully stops the MCP server.
func (s *MCPServer) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.status == "stopped" {
		return
	}

	s.status = "stopped"

	// Cancel context.
	if s.cancel != nil {
		s.cancel()
	}

	// Send close notification (best effort).
	if s.Stdin != nil {
		closeReq := jsonRPCRequest{
			JSONRPC: "2.0",
			Method:  "notifications/close",
		}
		data, _ := json.Marshal(closeReq)
		s.Stdin.Write(append(data, '\n'))
		s.Stdin.Close()
	}

	// Kill process.
	if s.Cmd != nil && s.Cmd.Process != nil {
		s.Cmd.Process.Kill()
		s.Cmd.Wait()
	}

	logInfo("MCP server %s stopped", s.Name)
}

// initialize performs the MCP initialization handshake.
func (s *MCPServer) initialize() error {
	// Send initialize request.
	params := initializeParams{
		ProtocolVersion: "2025-03-26",
		Capabilities: map[string]interface{}{
			"roots": map[string]interface{}{
				"listChanged": true,
			},
		},
	}
	params.ClientInfo.Name = "tetora"
	params.ClientInfo.Version = "2.0"

	resp, err := s.sendRequest("initialize", params)
	if err != nil {
		return fmt.Errorf("initialize request: %w", err)
	}

	if resp.Error != nil {
		return fmt.Errorf("initialize error: %s", resp.Error.Message)
	}

	// Parse result.
	var result initializeResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return fmt.Errorf("parse initialize result: %w", err)
	}

	logDebug("MCP server %s initialized: %s %s", s.Name, result.ServerInfo.Name, result.ServerInfo.Version)

	// Send initialized notification.
	initNotif := jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	}
	data, _ := json.Marshal(initNotif)
	if _, err := s.Stdin.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("send initialized notification: %w", err)
	}

	return nil
}

// discoverTools discovers available tools from the server and registers them.
func (s *MCPServer) discoverTools() ([]ToolDef, error) {
	resp, err := s.sendRequest("tools/list", nil)
	if err != nil {
		return nil, fmt.Errorf("tools/list request: %w", err)
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("tools/list error: %s", resp.Error.Message)
	}

	var result toolsListResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("parse tools/list result: %w", err)
	}

	// Register tools with MCP prefix.
	tools := make([]ToolDef, 0, len(result.Tools))
	for _, t := range result.Tools {
		toolName := fmt.Sprintf("mcp:%s:%s", s.Name, t.Name)
		toolDef := ToolDef{
			Name:        toolName,
			Description: t.Description,
			InputSchema: t.InputSchema,
			Handler:     s.makeToolHandler(t.Name),
			Builtin:     false,
		}
		tools = append(tools, toolDef)

		// Register in global registry.
		if s.toolReg != nil {
			s.toolReg.Register(&toolDef)
			logDebug("registered MCP tool: %s", toolName)
		}
	}

	return tools, nil
}

// makeToolHandler creates a tool handler that forwards to the MCP server.
func (s *MCPServer) makeToolHandler(toolName string) ToolHandler {
	return func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
		return s.callTool(ctx, toolName, input)
	}
}

// callTool calls a tool on the MCP server.
func (s *MCPServer) callTool(ctx context.Context, name string, args json.RawMessage) (string, error) {
	s.mu.Lock()
	if s.status != "running" {
		s.mu.Unlock()
		return "", fmt.Errorf("server not running: %s", s.status)
	}
	s.mu.Unlock()

	params := toolsCallParams{
		Name:      name,
		Arguments: args,
	}

	resp, err := s.sendRequest("tools/call", params)
	if err != nil {
		return "", fmt.Errorf("tools/call request: %w", err)
	}

	if resp.Error != nil {
		return "", fmt.Errorf("tools/call error: %s", resp.Error.Message)
	}

	var result toolsCallResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return "", fmt.Errorf("parse tools/call result: %w", err)
	}

	// Concatenate all text content.
	var output string
	for _, c := range result.Content {
		if c.Type == "text" {
			output += c.Text
		}
	}

	return output, nil
}

// sendRequest sends a JSON-RPC request and waits for response.
func (s *MCPServer) sendRequest(method string, params interface{}) (*jsonRPCResponse, error) {
	s.mu.Lock()
	s.nextID++
	id := s.nextID
	s.mu.Unlock()

	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	s.mu.Lock()
	if s.Stdin == nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("stdin closed")
	}
	_, err = s.Stdin.Write(append(data, '\n'))
	s.mu.Unlock()

	if err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}

	// Read response.
	return s.readResponse()
}

// readResponse reads a JSON-RPC response from stdout.
func (s *MCPServer) readResponse() (*jsonRPCResponse, error) {
	s.mu.Lock()
	if s.Stdout == nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("stdout closed")
	}
	reader := s.Stdout
	s.mu.Unlock()

	// Read one line (JSON-RPC over stdio uses newline-delimited JSON).
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var resp jsonRPCResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return &resp, nil
}

// monitorHealth monitors the server process and restarts on failure.
func (s *MCPServer) monitorHealth() {
	if s.Cmd == nil {
		return
	}

	err := s.Cmd.Wait()

	s.mu.Lock()
	defer s.mu.Unlock()

	// If stopped normally, don't restart.
	if s.status == "stopped" {
		return
	}

	// Process crashed.
	s.status = "error"
	s.lastError = fmt.Sprintf("process exited: %v", err)

	// Auto-restart with backoff (max 3 retries).
	if s.restarts < 3 {
		s.restarts++
		logWarn("MCP server %s crashed (restart %d/3), restarting...", s.Name, s.restarts)

		// Exponential backoff.
		backoff := time.Duration(s.restarts) * 2 * time.Second
		time.Sleep(backoff)

		s.status = "starting"
		s.ctx, s.cancel = context.WithCancel(context.Background())

		go func() {
			if err := s.start(s.ctx); err != nil {
				s.mu.Lock()
				s.status = "error"
				s.lastError = err.Error()
				s.mu.Unlock()
				logError("MCP server %s restart failed: %v", s.Name, err)
				return
			}
			go s.monitorHealth()
		}()
	} else {
		logError("MCP server %s crashed, max restarts exceeded", s.Name)
	}
}

// --- Helper Types ---

// mcpStderrWriter forwards MCP server stderr to our logs.
type mcpStderrWriter struct {
	serverName string
}

func (w *mcpStderrWriter) Write(p []byte) (n int, err error) {
	logWarn("MCP server %s stderr: %s", w.serverName, string(p))
	return len(p), nil
}
