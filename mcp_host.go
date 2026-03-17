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

	"tetora/internal/log"
)

// --- MCP Host Types ---

// MCPHost manages the lifecycle of MCP server processes.
type MCPHost struct {
	servers  map[string]*MCPServer
	mu       sync.RWMutex
	cfg      *Config
	toolReg  *ToolRegistry
	ctx      context.Context
	cancel   context.CancelFunc
	stopOnce sync.Once
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
	nextID    uint64
	status    string // "starting", "running", "stopped", "error"
	lastError string
	restarts  int
	ctx       context.Context
	cancel    context.CancelFunc
	parentCtx context.Context
	toolReg   *ToolRegistry

	// Demux: route responses to the correct caller by request ID.
	pending    map[int]chan *jsonRPCResponse
	pendingMu  sync.Mutex
	readerDone chan struct{}
	waitOnce   sync.Once
}

// MCPServerStatus provides status information for API endpoints.
type MCPServerStatus struct {
	Name      string   `json:"name"`
	Status    string   `json:"status"` // "running", "stopped", "error"
	Tools     []string `json:"tools"`
	Restarts  int      `json:"restarts"`
	LastError string   `json:"lastError,omitempty"`
}

// mcpProtocolVersion is the MCP protocol version used by both host and server.
const mcpProtocolVersion = "2025-03-26"

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

	h.ctx, h.cancel = context.WithCancel(ctx)

	// Watch for host context cancellation to ensure clean shutdown.
	go func() {
		<-h.ctx.Done()
		h.Stop()
	}()

	var wg sync.WaitGroup
	for name, serverCfg := range h.cfg.MCPServers {
		// Check if explicitly disabled.
		if serverCfg.Enabled != nil && !*serverCfg.Enabled {
			log.Info("MCP server %s disabled, skipping", name)
			continue
		}

		server := &MCPServer{
			Name:      name,
			Command:   serverCfg.Command,
			Args:      serverCfg.Args,
			Env:       serverCfg.Env,
			status:    "starting",
			parentCtx: h.ctx,
			toolReg:   h.toolReg,
		}
		server.ctx, server.cancel = context.WithCancel(h.ctx)

		h.servers[name] = server

		wg.Add(1)
		go func(s *MCPServer) {
			defer wg.Done()
			if err := s.start(s.ctx); err != nil {
				s.mu.Lock()
				s.status = "error"
				s.lastError = err.Error()
				s.mu.Unlock()
				log.Error("MCP server %s failed to start: %v", s.Name, err)
				return
			}

			// Monitor health.
			go s.monitorHealth()
		}(server)
	}

	wg.Wait()
	return nil
}

// Stop shuts down all MCP servers. Safe to call multiple times.
func (h *MCPHost) Stop() {
	h.stopOnce.Do(func() {
		// Cancel host context to signal all derived server contexts.
		if h.cancel != nil {
			h.cancel()
		}

		h.mu.Lock()
		defer h.mu.Unlock()

		for _, server := range h.servers {
			server.stop()
		}
	})
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

	// Restart with fresh context derived from host context.
	server.mu.Lock()
	server.status = "starting"
	server.restarts++
	server.parentCtx = h.ctx
	server.ctx, server.cancel = context.WithCancel(h.ctx)
	server.mu.Unlock()

	go func() {
		if err := server.start(server.ctx); err != nil {
			server.mu.Lock()
			server.status = "error"
			server.lastError = err.Error()
			server.mu.Unlock()
			log.Error("MCP server %s restart failed: %v", server.Name, err)
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

	log.Info("MCP server %s started (PID %d)", s.Name, cmd.Process.Pid)

	// Initialize demux state and start reader goroutine before any requests.
	s.pending = make(map[int]chan *jsonRPCResponse)
	s.readerDone = make(chan struct{})
	s.waitOnce = sync.Once{}
	go s.runReader()

	// Initialize handshake.
	if err := s.initialize(); err != nil {
		s.Stdin.Close()
		s.Cmd.Process.Kill()
		<-s.readerDone // reader goroutine calls Wait() via waitOnce; avoids double-Wait race
		return fmt.Errorf("initialize: %w", err)
	}

	// Discover tools.
	tools, err := s.discoverTools()
	if err != nil {
		s.Stdin.Close()
		s.Cmd.Process.Kill()
		<-s.readerDone // reader goroutine calls Wait() via waitOnce; avoids double-Wait race
		return fmt.Errorf("discover tools: %w", err)
	}

	s.mu.Lock()
	s.Tools = tools
	s.status = "running"
	s.mu.Unlock()

	log.Info("MCP server %s running with %d tools", s.Name, len(tools))

	return nil
}

// stop gracefully stops the MCP server.
func (s *MCPServer) stop() {
	s.mu.Lock()
	if s.status == "stopped" {
		s.mu.Unlock()
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

	// Kill process (triggers reader EOF).
	if s.Cmd != nil && s.Cmd.Process != nil {
		s.Cmd.Process.Kill()
	}
	s.mu.Unlock()

	// Wait for reader goroutine to finish (outside lock to avoid deadlock).
	if s.readerDone != nil {
		<-s.readerDone
	}

	// Safety net: ensure Wait() is called even if reader didn't run.
	s.waitOnce.Do(func() {
		if s.Cmd != nil {
			s.Cmd.Wait()
		}
	})

	log.Info("MCP server %s stopped", s.Name)
}

// initialize performs the MCP initialization handshake.
func (s *MCPServer) initialize() error {
	// Send initialize request.
	params := initializeParams{
		ProtocolVersion: mcpProtocolVersion,
		Capabilities: map[string]interface{}{
			"roots": map[string]interface{}{
				"listChanged": true,
			},
		},
	}
	params.ClientInfo.Name = "tetora"
	params.ClientInfo.Version = "2.0"

	resp, err := s.sendRequest(s.ctx, "initialize", params)
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

	log.Debug("MCP server %s initialized: %s %s", s.Name, result.ServerInfo.Name, result.ServerInfo.Version)

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
	resp, err := s.sendRequest(s.ctx, "tools/list", nil)
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
			log.Debug("registered MCP tool: %s", toolName)
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

	resp, err := s.sendRequest(ctx, "tools/call", params)
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

// sendRequest sends a JSON-RPC request and waits for the demuxed response.
// ctx controls the caller-level timeout; s.ctx controls server-level shutdown.
func (s *MCPServer) sendRequest(ctx context.Context, method string, params interface{}) (*jsonRPCResponse, error) {
	s.mu.Lock()
	s.nextID++
	id := s.nextID
	s.mu.Unlock()

	intID := int(id)
	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      intID,
		Method:  method,
		Params:  params,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// Register pending channel before writing to avoid race with reader.
	ch := make(chan *jsonRPCResponse, 1)
	s.pendingMu.Lock()
	s.pending[intID] = ch
	s.pendingMu.Unlock()

	// Write request to stdin.
	s.mu.Lock()
	if s.Stdin == nil {
		s.mu.Unlock()
		s.pendingMu.Lock()
		delete(s.pending, intID)
		s.pendingMu.Unlock()
		return nil, fmt.Errorf("stdin closed")
	}
	_, err = s.Stdin.Write(append(data, '\n'))
	s.mu.Unlock()

	if err != nil {
		s.pendingMu.Lock()
		delete(s.pending, intID)
		s.pendingMu.Unlock()
		return nil, fmt.Errorf("write request: %w", err)
	}

	// Wait for demuxed response or context cancellation.
	select {
	case resp, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("reader closed before response received")
		}
		return resp, nil
	case <-ctx.Done():
		s.pendingMu.Lock()
		delete(s.pending, intID)
		s.pendingMu.Unlock()
		return nil, fmt.Errorf("context cancelled: %w", ctx.Err())
	case <-s.ctx.Done():
		s.pendingMu.Lock()
		delete(s.pending, intID)
		s.pendingMu.Unlock()
		return nil, fmt.Errorf("context cancelled: %w", s.ctx.Err())
	}
}

// runReader is the single goroutine that reads stdout and demuxes responses by ID.
func (s *MCPServer) runReader() {
	defer func() {
		// Close all pending channels so blocked senders unblock.
		s.pendingMu.Lock()
		for id, ch := range s.pending {
			close(ch)
			delete(s.pending, id)
		}
		s.pendingMu.Unlock()

		s.waitOnce.Do(func() {
			if s.Cmd != nil {
				s.Cmd.Wait()
			}
		})
		close(s.readerDone)
	}()

	for {
		line, err := s.Stdout.ReadBytes('\n')
		if err != nil {
			if err != io.EOF {
				log.Debug("MCP server %s reader: %v", s.Name, err)
			}
			return
		}

		var resp jsonRPCResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			log.Warn("MCP server %s: invalid JSON from stdout: %v", s.Name, err)
			continue
		}

		// Notifications (ID == 0) are server-initiated; log and discard.
		if resp.ID == 0 {
			log.Debug("MCP server %s: notification received", s.Name)
			continue
		}

		s.pendingMu.Lock()
		ch, ok := s.pending[resp.ID]
		if ok {
			delete(s.pending, resp.ID)
		}
		s.pendingMu.Unlock()

		if ok {
			ch <- &resp
		} else {
			log.Warn("MCP server %s: unexpected response ID %d", s.Name, resp.ID)
		}
	}
}

// monitorHealth monitors the server process and restarts on failure.
func (s *MCPServer) monitorHealth() {
	if s.readerDone == nil {
		return
	}

	// Wait for reader goroutine to exit (which happens on process exit/EOF).
	<-s.readerDone

	s.mu.Lock()

	// If stopped normally, don't restart.
	if s.status == "stopped" {
		s.mu.Unlock()
		return
	}

	// Process crashed.
	s.status = "error"
	s.lastError = "process exited unexpectedly"

	// Auto-restart with backoff (max 3 retries).
	// Skip restart if parent (host) context is already cancelled.
	if s.restarts < 3 && s.parentCtx.Err() == nil {
		s.restarts++
		restarts := s.restarts
		log.Warn("MCP server %s crashed (restart %d/3), restarting...", s.Name, restarts)

		// Exponential backoff.
		backoff := time.Duration(restarts) * 2 * time.Second

		s.status = "starting"
		s.ctx, s.cancel = context.WithCancel(s.parentCtx)
		s.mu.Unlock()

		time.Sleep(backoff)

		if err := s.start(s.ctx); err != nil {
			s.mu.Lock()
			s.status = "error"
			s.lastError = err.Error()
			s.mu.Unlock()
			log.Error("MCP server %s restart failed: %v", s.Name, err)
			return
		}
		// start() already initialized new demux state; recurse to monitor.
		s.monitorHealth()
	} else {
		log.Error("MCP server %s crashed, max restarts exceeded", s.Name)
		s.mu.Unlock()
	}
}

// --- Helper Types ---

// mcpStderrWriter forwards MCP server stderr to our logs.
type mcpStderrWriter struct {
	serverName string
}

func (w *mcpStderrWriter) Write(p []byte) (n int, err error) {
	log.Warn("MCP server %s stderr: %s", w.serverName, string(p))
	return len(p), nil
}
