package plugin

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"tetora/internal/config"
	"tetora/internal/log"
)

// ValidPluginTypes enumerates valid plugin type strings.
var ValidPluginTypes = map[string]bool{
	"channel":  true,
	"tool":     true,
	"sandbox":  true,
	"provider": true,
	"memory":   true,
}

// --- JSON-RPC Protocol (local copies, not shared with mcp_host.go) ---

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

type jsonRPCNotification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// ToolRegistrar is implemented by callers that can register plugin-provided tools.
type ToolRegistrar interface {
	RegisterPluginTool(toolName, pluginName string, call func(method string, params any) (json.RawMessage, error))
}

// --- Plugin Process ---

// Process represents a running plugin process.
type Process struct {
	Name     string
	Type     string
	Config   config.PluginConfig
	Cmd      *exec.Cmd
	Stdin    io.WriteCloser
	Stdout   *bufio.Scanner
	Mu       sync.Mutex
	pending  map[int]chan json.RawMessage
	nextID   int32
	done     chan struct{}
	OnNotify func(method string, params json.RawMessage)
}

func newProcess(name string, pcfg config.PluginConfig) *Process {
	return &Process{
		Name:    name,
		Type:    pcfg.Type,
		Config:  pcfg,
		pending: make(map[int]chan json.RawMessage),
		done:    make(chan struct{}),
	}
}

func (p *Process) start() error {
	cmd := exec.Command(p.Config.Command, p.Config.Args...)

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
	p.Stdout.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	p.done = make(chan struct{})

	go p.readLoop()

	return nil
}

func (p *Process) stop() error {
	if p.Stdin != nil {
		p.Stdin.Close()
	}
	if p.Cmd != nil && p.Cmd.Process != nil {
		select {
		case <-p.done:
		case <-time.After(3 * time.Second):
		}
		p.Cmd.Process.Kill()
		p.Cmd.Wait()
	}

	p.Mu.Lock()
	for id, ch := range p.pending {
		close(ch)
		delete(p.pending, id)
	}
	p.Mu.Unlock()

	return nil
}

func (p *Process) call(method string, params any, timeout time.Duration) (json.RawMessage, error) {
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

	ch := make(chan json.RawMessage, 1)
	p.Mu.Lock()
	p.pending[id] = ch
	p.Mu.Unlock()

	defer func() {
		p.Mu.Lock()
		delete(p.pending, id)
		p.Mu.Unlock()
	}()

	p.Mu.Lock()
	if p.Stdin == nil {
		p.Mu.Unlock()
		return nil, fmt.Errorf("plugin %s not started", p.Name)
	}
	_, err = p.Stdin.Write(append(data, '\n'))
	p.Mu.Unlock()

	if err != nil {
		return nil, fmt.Errorf("write to plugin %s: %w", p.Name, err)
	}

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

func (p *Process) notify(method string, params any) error {
	req := jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal notification: %w", err)
	}

	p.Mu.Lock()
	defer p.Mu.Unlock()

	if p.Stdin == nil {
		return fmt.Errorf("plugin %s not started", p.Name)
	}

	_, err = p.Stdin.Write(append(data, '\n'))
	return err
}

func (p *Process) readLoop() {
	defer close(p.done)

	for p.Stdout.Scan() {
		line := p.Stdout.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}

		var resp jsonRPCResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			log.Warn("plugin read invalid json", "plugin", p.Name, "error", err)
			continue
		}

		if resp.ID > 0 {
			p.Mu.Lock()
			ch, ok := p.pending[resp.ID]
			p.Mu.Unlock()

			if ok {
				if resp.Error != nil {
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
				log.Debug("plugin response for unknown id", "plugin", p.Name, "id", resp.ID)
			}
		} else {
			var notif jsonRPCNotification
			if err := json.Unmarshal([]byte(line), &notif); err == nil && notif.Method != "" {
				p.Mu.Lock()
				fn := p.OnNotify
				p.Mu.Unlock()
				if fn != nil {
					fn(notif.Method, notif.Params)
				}
			}
		}
	}
}

func (p *Process) IsRunning() bool {
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

// Host manages all plugin processes.
type Host struct {
	Mu       sync.RWMutex
	Plugins  map[string]*Process
	cfg      *config.Config
	registrar ToolRegistrar
}

// NewHost creates a new plugin host. registrar may be nil if no tool plugins are used.
func NewHost(cfg *config.Config, registrar ToolRegistrar) *Host {
	return &Host{
		Plugins:   make(map[string]*Process),
		cfg:       cfg,
		registrar: registrar,
	}
}

// Start starts a named plugin from config.
func (h *Host) Start(name string) error {
	pcfg, ok := h.cfg.Plugins[name]
	if !ok {
		return fmt.Errorf("plugin %q not found in config", name)
	}

	if pcfg.Command == "" {
		return fmt.Errorf("plugin %q has no command", name)
	}

	if !ValidPluginTypes[pcfg.Type] {
		return fmt.Errorf("plugin %q has invalid type %q", name, pcfg.Type)
	}

	h.Mu.Lock()
	if existing, ok := h.Plugins[name]; ok && existing.IsRunning() {
		h.Mu.Unlock()
		return fmt.Errorf("plugin %q is already running", name)
	}
	h.Mu.Unlock()

	proc := newProcess(name, pcfg)

	proc.OnNotify = func(method string, params json.RawMessage) {
		log.Debug("plugin notification", "plugin", name, "method", method)
	}

	if err := proc.start(); err != nil {
		return err
	}

	h.Mu.Lock()
	h.Plugins[name] = proc
	h.Mu.Unlock()

	log.Info("plugin started", "name", name, "type", pcfg.Type, "command", pcfg.Command)

	if pcfg.Type == "tool" && len(pcfg.Tools) > 0 && h.registrar != nil {
		for _, toolName := range pcfg.Tools {
			pluginName := name
			tName := toolName
			h.registrar.RegisterPluginTool(tName, pluginName, func(method string, params any) (json.RawMessage, error) {
				return h.Call(pluginName, method, params)
			})
		}
		log.Info("plugin tools registered", "plugin", name, "tools", len(pcfg.Tools))
	}

	return nil
}

// Stop stops a named plugin.
func (h *Host) Stop(name string) error {
	h.Mu.Lock()
	proc, ok := h.Plugins[name]
	if !ok {
		h.Mu.Unlock()
		return fmt.Errorf("plugin %q is not running", name)
	}
	delete(h.Plugins, name)
	h.Mu.Unlock()

	log.Info("plugin stopping", "name", name)
	return proc.stop()
}

// StopAll stops all running plugins.
func (h *Host) StopAll() {
	h.Mu.Lock()
	names := make([]string, 0, len(h.Plugins))
	for name := range h.Plugins {
		names = append(names, name)
	}
	h.Mu.Unlock()

	for _, name := range names {
		if err := h.Stop(name); err != nil {
			log.Warn("stop plugin failed", "name", name, "error", err)
		}
	}
}

// Call sends a synchronous JSON-RPC call to a plugin.
func (h *Host) Call(name, method string, params any) (json.RawMessage, error) {
	h.Mu.RLock()
	proc, ok := h.Plugins[name]
	h.Mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("plugin %q is not running", name)
	}

	if !proc.IsRunning() {
		return nil, fmt.Errorf("plugin %q process has exited", name)
	}

	timeout := 30 * time.Second
	if h.cfg.Tools.Timeout > 0 {
		timeout = time.Duration(h.cfg.Tools.Timeout) * time.Second
	}

	return proc.call(method, params, timeout)
}

// Notify sends an async JSON-RPC notification to a plugin.
func (h *Host) Notify(name, method string, params any) error {
	h.Mu.RLock()
	proc, ok := h.Plugins[name]
	h.Mu.RUnlock()

	if !ok {
		return fmt.Errorf("plugin %q is not running", name)
	}

	return proc.notify(method, params)
}

// List returns information about all configured plugins and their status.
func (h *Host) List() []map[string]any {
	h.Mu.RLock()
	defer h.Mu.RUnlock()

	var result []map[string]any
	for name, pcfg := range h.cfg.Plugins {
		status := "stopped"
		if proc, ok := h.Plugins[name]; ok && proc.IsRunning() {
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
func (h *Host) Health(name string) map[string]any {
	h.Mu.RLock()
	proc, ok := h.Plugins[name]
	h.Mu.RUnlock()

	if !ok {
		return map[string]any{"name": name, "status": "not_running", "healthy": false}
	}

	if !proc.IsRunning() {
		return map[string]any{"name": name, "status": "exited", "healthy": false}
	}

	_, err := proc.call("ping", nil, 5*time.Second)
	if err != nil {
		return map[string]any{"name": name, "status": "running", "healthy": false, "error": err.Error()}
	}

	return map[string]any{"name": name, "status": "running", "healthy": true}
}

// AutoStart starts all plugins with autoStart=true.
func (h *Host) AutoStart() {
	for name, pcfg := range h.cfg.Plugins {
		if pcfg.AutoStart {
			if err := h.Start(name); err != nil {
				log.Warn("auto-start plugin failed", "name", name, "error", err)
			}
		}
	}
}
