package main

// --- P13.1: Plugin System ---

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// --- Plugin Config Types ---

// PluginConfig defines a plugin in the config file.
type PluginConfig struct {
	Type      string            `json:"type"`                // "channel", "tool", "sandbox", "provider", "memory"
	Command   string            `json:"command"`             // binary path or name
	Args      []string          `json:"args,omitempty"`      // command arguments
	Env       map[string]string `json:"env,omitempty"`       // environment variables ($ENV_VAR supported)
	AutoStart bool              `json:"autoStart,omitempty"` // start when daemon starts
	Tools     []string          `json:"tools,omitempty"`     // tool names (for type=tool)
}

// validPluginTypes enumerates valid plugin type strings.
var validPluginTypes = map[string]bool{
	"channel":  true,
	"tool":     true,
	"sandbox":  true,
	"provider": true,
	"memory":   true,
}

// --- JSON-RPC Protocol ---
// jsonRPCRequest, jsonRPCResponse, jsonRPCError are defined in mcp_host.go (shared JSON-RPC types).

// jsonRPCNotification is a JSON-RPC 2.0 notification (Plugin -> Tetora, no ID).
type jsonRPCNotification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// --- Plugin Process ---

// PluginProcess represents a running plugin process.
type PluginProcess struct {
	Name    string
	Type    string // "channel", "tool", "sandbox", "provider", "memory"
	Config  PluginConfig
	Cmd     *exec.Cmd
	Stdin   io.WriteCloser
	Stdout  *bufio.Scanner
	mu      sync.Mutex
	pending map[int]chan json.RawMessage // request ID -> response channel
	nextID  int32                        // atomic request ID counter
	done    chan struct{}                 // closed when readLoop exits
	onNotify func(method string, params json.RawMessage) // notification callback
}

// newPluginProcess creates a new PluginProcess (not started yet).
func newPluginProcess(name string, pcfg PluginConfig) *PluginProcess {
	return &PluginProcess{
		Name:    name,
		Type:    pcfg.Type,
		Config:  pcfg,
		pending: make(map[int]chan json.RawMessage),
		done:    make(chan struct{}),
	}
}

// start launches the plugin binary and begins the read loop.
func (p *PluginProcess) start() error {
	cmd := exec.Command(p.Config.Command, p.Config.Args...)

	// Set environment variables.
	if len(p.Config.Env) > 0 {
		env := cmd.Environ()
		for k, v := range p.Config.Env {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		stdin.Close()
		return fmt.Errorf("start plugin %s: %w", p.Name, err)
	}

	p.Cmd = cmd
	p.Stdin = stdin
	p.Stdout = bufio.NewScanner(stdout)
	// Increase scanner buffer for large responses (1MB).
	p.Stdout.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	p.done = make(chan struct{})

	// Start the read loop in a goroutine.
	go p.readLoop()

	return nil
}

// stop terminates the plugin process gracefully.
func (p *PluginProcess) stop() error {
	if p.Stdin != nil {
		p.Stdin.Close()
	}
	if p.Cmd != nil && p.Cmd.Process != nil {
		// Wait for the read loop to finish (with timeout).
		select {
		case <-p.done:
		case <-time.After(3 * time.Second):
		}
		p.Cmd.Process.Kill()
		p.Cmd.Wait()
	}

	// Fail all pending requests.
	p.mu.Lock()
	for id, ch := range p.pending {
		close(ch)
		delete(p.pending, id)
	}
	p.mu.Unlock()

	return nil
}

// call sends a JSON-RPC request and waits for the response.
func (p *PluginProcess) call(method string, params any, timeout time.Duration) (json.RawMessage, error) {
	id := int(atomic.AddInt32(&p.nextID, 1))

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

	// Register pending response channel.
	ch := make(chan json.RawMessage, 1)
	p.mu.Lock()
	p.pending[id] = ch
	p.mu.Unlock()

	// Clean up on exit.
	defer func() {
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
	}()

	// Write request to stdin (one line).
	p.mu.Lock()
	if p.Stdin == nil {
		p.mu.Unlock()
		return nil, fmt.Errorf("plugin %s not started", p.Name)
	}
	_, err = p.Stdin.Write(append(data, '\n'))
	p.mu.Unlock()

	if err != nil {
		return nil, fmt.Errorf("write to plugin %s: %w", p.Name, err)
	}

	// Wait for response with timeout.
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case result, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("plugin %s: response channel closed (process crashed?)", p.Name)
		}
		return result, nil
	case <-timer.C:
		return nil, fmt.Errorf("plugin %s: timeout waiting for response (method=%s, id=%d)", p.Name, method, id)
	}
}

// notify sends a JSON-RPC notification to the plugin (no response expected).
func (p *PluginProcess) notify(method string, params any) error {
	req := jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal notification: %w", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.Stdin == nil {
		return fmt.Errorf("plugin %s not started", p.Name)
	}

	_, err = p.Stdin.Write(append(data, '\n'))
	return err
}

// readLoop reads JSON-RPC responses/notifications from the plugin stdout.
func (p *PluginProcess) readLoop() {
	defer close(p.done)

	for p.Stdout.Scan() {
		line := p.Stdout.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}

		// Try to parse as a response (has "id" field).
		var resp jsonRPCResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			logWarn("plugin read invalid json", "plugin", p.Name, "error", err)
			continue
		}

		if resp.ID > 0 {
			// This is a response to a pending request.
			p.mu.Lock()
			ch, ok := p.pending[resp.ID]
			p.mu.Unlock()

			if ok {
				if resp.Error != nil {
					// Encode error as a JSON result with error info.
					errJSON, _ := json.Marshal(map[string]any{
						"error":   resp.Error.Message,
						"code":    resp.Error.Code,
						"isError": true,
					})
					ch <- errJSON
				} else {
					ch <- resp.Result
				}
			} else {
				logDebug("plugin response for unknown id", "plugin", p.Name, "id", resp.ID)
			}
		} else {
			// No ID — this is a notification from the plugin.
			var notif jsonRPCNotification
			if err := json.Unmarshal([]byte(line), &notif); err == nil && notif.Method != "" {
				if p.onNotify != nil {
					p.onNotify(notif.Method, notif.Params)
				}
			}
		}
	}
}

// isRunning checks if the plugin process is still alive.
func (p *PluginProcess) isRunning() bool {
	if p.Cmd == nil || p.Cmd.Process == nil {
		return false
	}
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

// --- Plugin Host ---

// PluginHost manages all plugin processes.
type PluginHost struct {
	mu      sync.RWMutex
	plugins map[string]*PluginProcess // name -> running process
	cfg     *Config
}

// NewPluginHost creates a new plugin host.
func NewPluginHost(cfg *Config) *PluginHost {
	return &PluginHost{
		plugins: make(map[string]*PluginProcess),
		cfg:     cfg,
	}
}

// Start starts a named plugin from config.
func (h *PluginHost) Start(name string) error {
	pcfg, ok := h.cfg.Plugins[name]
	if !ok {
		return fmt.Errorf("plugin %q not found in config", name)
	}

	if pcfg.Command == "" {
		return fmt.Errorf("plugin %q has no command", name)
	}

	if !validPluginTypes[pcfg.Type] {
		return fmt.Errorf("plugin %q has invalid type %q", name, pcfg.Type)
	}

	h.mu.Lock()
	// Check if already running.
	if existing, ok := h.plugins[name]; ok && existing.isRunning() {
		h.mu.Unlock()
		return fmt.Errorf("plugin %q is already running", name)
	}
	h.mu.Unlock()

	proc := newPluginProcess(name, pcfg)

	// Wire notification handler for channel plugins.
	proc.onNotify = func(method string, params json.RawMessage) {
		logDebug("plugin notification", "plugin", name, "method", method)
		// Channel notifications can be handled by the dispatch system.
		// For now, just log them.
	}

	if err := proc.start(); err != nil {
		return err
	}

	h.mu.Lock()
	h.plugins[name] = proc
	h.mu.Unlock()

	logInfo("plugin started", "name", name, "type", pcfg.Type, "command", pcfg.Command)

	// Register plugin tools in the tool registry.
	if pcfg.Type == "tool" && len(pcfg.Tools) > 0 && h.cfg.toolRegistry != nil {
		for _, toolName := range pcfg.Tools {
			h.registerPluginTool(name, toolName)
		}
		logInfo("plugin tools registered", "plugin", name, "tools", len(pcfg.Tools))
	}

	return nil
}

// registerPluginTool registers a plugin-provided tool in the tool registry.
func (h *PluginHost) registerPluginTool(pluginName, toolName string) {
	pluginRef := pluginName // capture for closure
	toolRef := toolName     // capture for closure

	h.cfg.toolRegistry.Register(&ToolDef{
		Name:        toolRef,
		Description: fmt.Sprintf("Plugin tool (%s) provided by plugin %q", toolRef, pluginRef),
		InputSchema: json.RawMessage(`{"type": "object", "properties": {"input": {"type": "object", "description": "Tool input"}}, "required": []}`),
		Handler: func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
			result, err := h.Call(pluginRef, "tool/execute", map[string]any{
				"name":  toolRef,
				"input": json.RawMessage(input),
			})
			if err != nil {
				return "", err
			}
			return string(result), nil
		},
		Builtin: false,
	})
}

// Stop stops a named plugin.
func (h *PluginHost) Stop(name string) error {
	h.mu.Lock()
	proc, ok := h.plugins[name]
	if !ok {
		h.mu.Unlock()
		return fmt.Errorf("plugin %q is not running", name)
	}
	delete(h.plugins, name)
	h.mu.Unlock()

	logInfo("plugin stopping", "name", name)
	return proc.stop()
}

// StopAll stops all running plugins.
func (h *PluginHost) StopAll() {
	h.mu.Lock()
	names := make([]string, 0, len(h.plugins))
	for name := range h.plugins {
		names = append(names, name)
	}
	h.mu.Unlock()

	for _, name := range names {
		if err := h.Stop(name); err != nil {
			logWarn("stop plugin failed", "name", name, "error", err)
		}
	}
}

// Call sends a synchronous JSON-RPC call to a plugin.
func (h *PluginHost) Call(name, method string, params any) (json.RawMessage, error) {
	h.mu.RLock()
	proc, ok := h.plugins[name]
	h.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("plugin %q is not running", name)
	}

	if !proc.isRunning() {
		return nil, fmt.Errorf("plugin %q process has exited", name)
	}

	timeout := 30 * time.Second
	if h.cfg.Tools.Timeout > 0 {
		timeout = time.Duration(h.cfg.Tools.Timeout) * time.Second
	}

	return proc.call(method, params, timeout)
}

// Notify sends an async JSON-RPC notification to a plugin (no response).
func (h *PluginHost) Notify(name, method string, params any) error {
	h.mu.RLock()
	proc, ok := h.plugins[name]
	h.mu.RUnlock()

	if !ok {
		return fmt.Errorf("plugin %q is not running", name)
	}

	return proc.notify(method, params)
}

// List returns information about all configured plugins and their status.
func (h *PluginHost) List() []map[string]any {
	h.mu.RLock()
	defer h.mu.RUnlock()

	var result []map[string]any
	for name, pcfg := range h.cfg.Plugins {
		status := "stopped"
		if proc, ok := h.plugins[name]; ok && proc.isRunning() {
			status = "running"
		}
		entry := map[string]any{
			"name":      name,
			"type":      pcfg.Type,
			"command":   pcfg.Command,
			"autoStart": pcfg.AutoStart,
			"status":    status,
		}
		if len(pcfg.Tools) > 0 {
			entry["tools"] = pcfg.Tools
		}
		result = append(result, entry)
	}
	return result
}

// Health checks if a plugin is running and responsive.
func (h *PluginHost) Health(name string) map[string]any {
	h.mu.RLock()
	proc, ok := h.plugins[name]
	h.mu.RUnlock()

	if !ok {
		return map[string]any{"name": name, "status": "not_running", "healthy": false}
	}

	if !proc.isRunning() {
		return map[string]any{"name": name, "status": "exited", "healthy": false}
	}

	// Try a ping call with short timeout.
	_, err := proc.call("ping", nil, 5*time.Second)
	if err != nil {
		return map[string]any{"name": name, "status": "running", "healthy": false, "error": err.Error()}
	}

	return map[string]any{"name": name, "status": "running", "healthy": true}
}

// AutoStart starts all plugins with autoStart=true.
func (h *PluginHost) AutoStart() {
	for name, pcfg := range h.cfg.Plugins {
		if pcfg.AutoStart {
			if err := h.Start(name); err != nil {
				logWarn("auto-start plugin failed", "name", name, "error", err)
			}
		}
	}
}

// --- Code Mode Meta-Tools ---

// codeModeCoreTools lists the names of tools always exposed directly to the agent.
var codeModeCoreTools = map[string]bool{
	"exec":           true,
	"read":           true,
	"write":          true,
	"web_search":     true,
	"web_fetch":      true,
	"memory_search":  true,
	"agent_dispatch": true,
	"search_tools":   true,
	"execute_tool":   true,
}

// codeModeTotalThreshold is the tool count above which Code Mode activates.
const codeModeTotalThreshold = 10

// shouldUseCodeMode returns true if Code Mode should be used (too many tools).
func shouldUseCodeMode(registry *ToolRegistry) bool {
	if registry == nil {
		return false
	}
	return len(registry.List()) > codeModeTotalThreshold
}

// toolSearchTools is the handler for the search_tools meta-tool.
func toolSearchTools(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
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
		args.Limit = 10
	}

	if cfg.toolRegistry == nil {
		return "[]", nil
	}

	query := strings.ToLower(args.Query)
	var results []map[string]string

	for _, tool := range cfg.toolRegistry.List() {
		// Match by name or description.
		nameMatch := strings.Contains(strings.ToLower(tool.Name), query)
		descMatch := strings.Contains(strings.ToLower(tool.Description), query)
		if nameMatch || descMatch {
			results = append(results, map[string]string{
				"name":        tool.Name,
				"description": tool.Description,
			})
			if len(results) >= args.Limit {
				break
			}
		}
	}

	b, _ := json.Marshal(results)
	return string(b), nil
}

// toolExecuteTool is the handler for the execute_tool meta-tool.
func toolExecuteTool(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Name == "" {
		return "", fmt.Errorf("name is required")
	}

	if cfg.toolRegistry == nil {
		return "", fmt.Errorf("tool registry not initialized")
	}

	tool, ok := cfg.toolRegistry.Get(args.Name)
	if !ok {
		return "", fmt.Errorf("tool %q not found", args.Name)
	}

	if tool.Handler == nil {
		return "", fmt.Errorf("tool %q has no handler", args.Name)
	}

	return tool.Handler(ctx, cfg, args.Input)
}

// --- Plugin CLI ---

// cmdPlugin handles the `tetora plugin` subcommand.
func cmdPlugin(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: tetora plugin <list|start|stop> [name]")
		fmt.Println()
		fmt.Println("Manage external plugins.")
		fmt.Println()
		fmt.Println("Commands:")
		fmt.Println("  list          List configured plugins and their status")
		fmt.Println("  start <name>  Start a plugin")
		fmt.Println("  stop <name>   Stop a running plugin")
		return
	}

	cfg := loadConfig("")

	switch args[0] {
	case "list":
		if len(cfg.Plugins) == 0 {
			fmt.Println("No plugins configured.")
			return
		}
		fmt.Printf("%-20s %-10s %-10s %-30s %s\n", "NAME", "TYPE", "AUTOSTART", "COMMAND", "TOOLS")
		for name, pcfg := range cfg.Plugins {
			tools := "-"
			if len(pcfg.Tools) > 0 {
				tools = strings.Join(pcfg.Tools, ", ")
			}
			autoStart := "no"
			if pcfg.AutoStart {
				autoStart = "yes"
			}
			fmt.Printf("%-20s %-10s %-10s %-30s %s\n", name, pcfg.Type, autoStart, pcfg.Command, tools)
		}

	case "start":
		if len(args) < 2 {
			fmt.Println("Usage: tetora plugin start <name>")
			return
		}
		name := args[1]
		pcfg, ok := cfg.Plugins[name]
		if !ok {
			fmt.Printf("Plugin %q not found in config.\n", name)
			return
		}
		fmt.Printf("Starting plugin %q (type=%s, command=%s)...\n", name, pcfg.Type, pcfg.Command)
		fmt.Println("Note: plugins are managed by the daemon. Use the HTTP API to start plugins at runtime.")

	case "stop":
		if len(args) < 2 {
			fmt.Println("Usage: tetora plugin stop <name>")
			return
		}
		name := args[1]
		if _, ok := cfg.Plugins[name]; !ok {
			fmt.Printf("Plugin %q not found in config.\n", name)
			return
		}
		fmt.Printf("Note: plugins are managed by the daemon. Use the HTTP API to stop plugins at runtime.\n")

	default:
		fmt.Printf("Unknown plugin command: %s\n", args[0])
		fmt.Println("Use: tetora plugin list|start|stop")
	}
}
