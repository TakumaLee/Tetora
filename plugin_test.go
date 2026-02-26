package main

// --- P13.1: Plugin System Tests ---

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// createMockPluginScript creates a temporary shell script that acts as a mock plugin.
// The script reads JSON-RPC requests from stdin and writes responses to stdout.
func createMockPluginScript(t *testing.T, dir, name, behavior string) string {
	t.Helper()
	path := filepath.Join(dir, name)

	var script string
	switch behavior {
	case "echo":
		// Reads JSON-RPC requests, echoes back the params as result.
		script = `#!/bin/sh
while IFS= read -r line; do
  id=$(echo "$line" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
  if [ -n "$id" ] && [ "$id" != "0" ]; then
    echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"output\":\"echo response\",\"isError\":false}}"
  fi
done
`
	case "slow":
		// Takes 10 seconds to respond (for timeout tests).
		script = `#!/bin/sh
while IFS= read -r line; do
  sleep 10
  id=$(echo "$line" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
  if [ -n "$id" ] && [ "$id" != "0" ]; then
    echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"output\":\"slow response\"}}"
  fi
done
`
	case "crash":
		// Immediately exits.
		script = `#!/bin/sh
exit 1
`
	case "notify":
		// Sends a notification, then echoes requests.
		script = `#!/bin/sh
echo '{"jsonrpc":"2.0","method":"channel/message","params":{"channel":"test","from":"U1","text":"hello"}}'
while IFS= read -r line; do
  id=$(echo "$line" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
  if [ -n "$id" ] && [ "$id" != "0" ]; then
    echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"output\":\"ack\"}}"
  fi
done
`
	case "error":
		// Returns JSON-RPC error responses.
		script = `#!/bin/sh
while IFS= read -r line; do
  id=$(echo "$line" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
  if [ -n "$id" ] && [ "$id" != "0" ]; then
    echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"error\":{\"code\":-32000,\"message\":\"plugin error\"}}"
  fi
done
`
	case "ping":
		// Responds to ping and tool/execute.
		script = `#!/bin/sh
while IFS= read -r line; do
  id=$(echo "$line" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
  method=$(echo "$line" | sed -n 's/.*"method":"\([^"]*\)".*/\1/p')
  if [ -n "$id" ] && [ "$id" != "0" ]; then
    if [ "$method" = "ping" ]; then
      echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"status\":\"ok\"}}"
    elif [ "$method" = "tool/execute" ]; then
      echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"output\":\"tool executed\",\"isError\":false}}"
    else
      echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"output\":\"unknown method\"}}"
    fi
  fi
done
`
	default:
		t.Fatalf("unknown mock behavior: %s", behavior)
	}

	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("create mock script: %v", err)
	}
	return path
}

func TestPluginProcessLifecycle(t *testing.T) {
	dir := t.TempDir()
	scriptPath := createMockPluginScript(t, dir, "mock-plugin", "echo")

	cfg := &Config{
		Plugins: map[string]PluginConfig{
			"test-echo": {
				Type:    "tool",
				Command: scriptPath,
			},
		},
	}

	host := NewPluginHost(cfg)

	// Start plugin.
	if err := host.Start("test-echo"); err != nil {
		t.Fatalf("start plugin: %v", err)
	}

	// Check it's running.
	host.mu.RLock()
	proc, ok := host.plugins["test-echo"]
	host.mu.RUnlock()
	if !ok {
		t.Fatal("plugin not found in host")
	}
	if !proc.isRunning() {
		t.Error("plugin should be running")
	}

	// Stop plugin.
	if err := host.Stop("test-echo"); err != nil {
		t.Fatalf("stop plugin: %v", err)
	}

	// Check it's gone.
	host.mu.RLock()
	_, ok = host.plugins["test-echo"]
	host.mu.RUnlock()
	if ok {
		t.Error("plugin should be removed from host after stop")
	}
}

func TestPluginProcessRestart(t *testing.T) {
	dir := t.TempDir()
	scriptPath := createMockPluginScript(t, dir, "mock-plugin", "echo")

	cfg := &Config{
		Plugins: map[string]PluginConfig{
			"test-restart": {
				Type:    "tool",
				Command: scriptPath,
			},
		},
	}

	host := NewPluginHost(cfg)

	// Start.
	if err := host.Start("test-restart"); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Stop.
	if err := host.Stop("test-restart"); err != nil {
		t.Fatalf("stop: %v", err)
	}

	// Start again.
	if err := host.Start("test-restart"); err != nil {
		t.Fatalf("restart: %v", err)
	}

	// Should be running.
	host.mu.RLock()
	proc, ok := host.plugins["test-restart"]
	host.mu.RUnlock()
	if !ok || !proc.isRunning() {
		t.Error("plugin should be running after restart")
	}

	host.StopAll()
}

func TestPluginJSONRPCRoundTrip(t *testing.T) {
	dir := t.TempDir()
	scriptPath := createMockPluginScript(t, dir, "mock-plugin", "echo")

	cfg := &Config{
		Plugins: map[string]PluginConfig{
			"test-rpc": {
				Type:    "tool",
				Command: scriptPath,
			},
		},
	}

	host := NewPluginHost(cfg)
	if err := host.Start("test-rpc"); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer host.StopAll()

	// Make a call.
	result, err := host.Call("test-rpc", "tool/execute", map[string]any{
		"name":  "test_tool",
		"input": map[string]string{"key": "value"},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}

	var resp map[string]any
	if err := json.Unmarshal(result, &resp); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if resp["output"] != "echo response" {
		t.Errorf("output = %v, want 'echo response'", resp["output"])
	}
}

func TestPluginJSONRPCNotification(t *testing.T) {
	dir := t.TempDir()
	scriptPath := createMockPluginScript(t, dir, "mock-plugin", "notify")

	notified := make(chan string, 1)

	cfg := &Config{
		Plugins: map[string]PluginConfig{
			"test-notif": {
				Type:    "channel",
				Command: scriptPath,
			},
		},
	}

	host := NewPluginHost(cfg)
	if err := host.Start("test-notif"); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer host.StopAll()

	// Wire notification handler.
	host.mu.RLock()
	proc := host.plugins["test-notif"]
	host.mu.RUnlock()
	proc.mu.Lock()
	proc.onNotify = func(method string, params json.RawMessage) {
		notified <- method
	}
	proc.mu.Unlock()

	// Wait for notification from the mock plugin.
	select {
	case method := <-notified:
		if method != "channel/message" {
			t.Errorf("notification method = %q, want channel/message", method)
		}
	case <-time.After(3 * time.Second):
		t.Error("timeout waiting for notification")
	}
}

func TestPluginTimeoutHandling(t *testing.T) {
	dir := t.TempDir()
	scriptPath := createMockPluginScript(t, dir, "mock-plugin", "slow")

	cfg := &Config{
		Plugins: map[string]PluginConfig{
			"test-slow": {
				Type:    "tool",
				Command: scriptPath,
			},
		},
		Tools: ToolConfig{
			Timeout: 1, // 1 second timeout
		},
	}

	host := NewPluginHost(cfg)
	if err := host.Start("test-slow"); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer host.StopAll()

	// Call should timeout.
	_, err := host.Call("test-slow", "tool/execute", nil)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("error = %v, want timeout error", err)
	}
}

func TestPluginCrashRecovery(t *testing.T) {
	dir := t.TempDir()
	scriptPath := createMockPluginScript(t, dir, "mock-plugin", "crash")

	cfg := &Config{
		Plugins: map[string]PluginConfig{
			"test-crash": {
				Type:    "tool",
				Command: scriptPath,
			},
		},
	}

	host := NewPluginHost(cfg)
	if err := host.Start("test-crash"); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Wait for the process to crash (longer under -race).
	time.Sleep(2 * time.Second)

	// isRunning should return false.
	host.mu.RLock()
	proc, ok := host.plugins["test-crash"]
	host.mu.RUnlock()
	if !ok {
		t.Fatal("plugin should still be in host map")
	}
	if proc.isRunning() {
		t.Error("crashed plugin should not be running")
	}

	// Call should fail gracefully.
	_, err := host.Call("test-crash", "tool/execute", nil)
	if err == nil {
		t.Fatal("expected error calling crashed plugin")
	}

	host.StopAll()
}

func TestPluginToolRegistration(t *testing.T) {
	dir := t.TempDir()
	scriptPath := createMockPluginScript(t, dir, "mock-plugin", "ping")

	cfg := &Config{
		Plugins: map[string]PluginConfig{
			"test-tools": {
				Type:    "tool",
				Command: scriptPath,
				Tools:   []string{"browser_navigate", "browser_click"},
			},
		},
		Tools: ToolConfig{},
	}
	cfg.toolRegistry = NewToolRegistry(cfg)

	host := NewPluginHost(cfg)
	if err := host.Start("test-tools"); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer host.StopAll()

	// Check tools are registered.
	tool, ok := cfg.toolRegistry.Get("browser_navigate")
	if !ok {
		t.Fatal("browser_navigate should be registered")
	}
	if tool.Builtin {
		t.Error("plugin tool should not be marked as builtin")
	}

	tool2, ok := cfg.toolRegistry.Get("browser_click")
	if !ok {
		t.Fatal("browser_click should be registered")
	}
	if tool2.Name != "browser_click" {
		t.Errorf("tool name = %q, want browser_click", tool2.Name)
	}
}

func TestPluginChannelMessageRouting(t *testing.T) {
	dir := t.TempDir()
	scriptPath := createMockPluginScript(t, dir, "mock-plugin", "notify")

	received := make(chan json.RawMessage, 1)

	cfg := &Config{
		Plugins: map[string]PluginConfig{
			"test-channel": {
				Type:    "channel",
				Command: scriptPath,
			},
		},
	}

	host := NewPluginHost(cfg)
	if err := host.Start("test-channel"); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer host.StopAll()

	// Override notification handler to capture the message.
	host.mu.RLock()
	proc := host.plugins["test-channel"]
	host.mu.RUnlock()
	proc.mu.Lock()
	proc.onNotify = func(method string, params json.RawMessage) {
		if method == "channel/message" {
			received <- params
		}
	}
	proc.mu.Unlock()

	// Wait for the initial notification from the mock.
	select {
	case params := <-received:
		var msg struct {
			Channel string `json:"channel"`
			From    string `json:"from"`
			Text    string `json:"text"`
		}
		if err := json.Unmarshal(params, &msg); err != nil {
			t.Fatalf("unmarshal params: %v", err)
		}
		if msg.Channel != "test" || msg.From != "U1" || msg.Text != "hello" {
			t.Errorf("unexpected message: %+v", msg)
		}
	case <-time.After(3 * time.Second):
		t.Error("timeout waiting for channel message")
	}
}

func TestPluginConfigValidation(t *testing.T) {
	// Test missing command.
	cfg := &Config{
		Plugins: map[string]PluginConfig{
			"bad-cmd": {
				Type:    "tool",
				Command: "",
			},
		},
	}
	host := NewPluginHost(cfg)
	err := host.Start("bad-cmd")
	if err == nil {
		t.Fatal("expected error for empty command")
	}
	if !strings.Contains(err.Error(), "no command") {
		t.Errorf("error = %v, want 'no command'", err)
	}

	// Test invalid type.
	cfg2 := &Config{
		Plugins: map[string]PluginConfig{
			"bad-type": {
				Type:    "invalid",
				Command: "/bin/echo",
			},
		},
	}
	host2 := NewPluginHost(cfg2)
	err2 := host2.Start("bad-type")
	if err2 == nil {
		t.Fatal("expected error for invalid type")
	}
	if !strings.Contains(err2.Error(), "invalid type") {
		t.Errorf("error = %v, want 'invalid type'", err2)
	}

	// Test plugin not found.
	host3 := NewPluginHost(&Config{Plugins: map[string]PluginConfig{}})
	err3 := host3.Start("nonexistent")
	if err3 == nil {
		t.Fatal("expected error for nonexistent plugin")
	}
	if !strings.Contains(err3.Error(), "not found") {
		t.Errorf("error = %v, want 'not found'", err3)
	}
}

func TestPluginSearchTools(t *testing.T) {
	cfg := &Config{Tools: ToolConfig{}}
	cfg.toolRegistry = NewToolRegistry(cfg)

	// Register some extra tools to search.
	cfg.toolRegistry.Register(&ToolDef{
		Name:        "browser_navigate",
		Description: "Navigate browser to a URL",
		Handler:     func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) { return "", nil },
	})
	cfg.toolRegistry.Register(&ToolDef{
		Name:        "browser_screenshot",
		Description: "Take a screenshot of the browser",
		Handler:     func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) { return "", nil },
	})
	cfg.toolRegistry.Register(&ToolDef{
		Name:        "docker_exec",
		Description: "Execute a command in Docker container",
		Handler:     func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) { return "", nil },
	})

	ctx := context.Background()

	// Search for browser tools.
	input, _ := json.Marshal(map[string]any{"query": "browser"})
	result, err := toolSearchTools(ctx, cfg, input)
	if err != nil {
		t.Fatalf("search_tools: %v", err)
	}

	var tools []map[string]string
	if err := json.Unmarshal([]byte(result), &tools); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(tools) < 2 {
		t.Errorf("expected at least 2 browser tools, got %d", len(tools))
	}

	// Search for docker.
	input2, _ := json.Marshal(map[string]any{"query": "docker"})
	result2, err := toolSearchTools(ctx, cfg, input2)
	if err != nil {
		t.Fatalf("search_tools: %v", err)
	}

	var tools2 []map[string]string
	json.Unmarshal([]byte(result2), &tools2)

	if len(tools2) != 1 {
		t.Errorf("expected 1 docker tool, got %d", len(tools2))
	}
}

func TestPluginExecuteTool(t *testing.T) {
	cfg := &Config{Tools: ToolConfig{}}
	cfg.toolRegistry = NewToolRegistry(cfg)

	// Register a test tool.
	cfg.toolRegistry.Register(&ToolDef{
		Name:        "test_echo",
		Description: "Echo input back",
		Handler: func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
			return fmt.Sprintf("echoed: %s", string(input)), nil
		},
	})

	ctx := context.Background()

	// Execute the tool.
	input, _ := json.Marshal(map[string]any{
		"name":  "test_echo",
		"input": map[string]string{"msg": "hello"},
	})
	result, err := toolExecuteTool(ctx, cfg, input)
	if err != nil {
		t.Fatalf("execute_tool: %v", err)
	}

	if !strings.Contains(result, "hello") {
		t.Errorf("result = %q, want to contain 'hello'", result)
	}

	// Try nonexistent tool.
	input2, _ := json.Marshal(map[string]any{"name": "nonexistent"})
	_, err2 := toolExecuteTool(ctx, cfg, input2)
	if err2 == nil {
		t.Fatal("expected error for nonexistent tool")
	}
}

func TestPluginCodeModeThreshold(t *testing.T) {
	cfg := &Config{Tools: ToolConfig{}}
	cfg.toolRegistry = NewToolRegistry(cfg)

	// Initially we have built-in tools (< threshold likely).
	initialCount := len(cfg.toolRegistry.List())

	// Add tools until we exceed the threshold.
	for i := 0; i <= codeModeTotalThreshold-initialCount+1; i++ {
		cfg.toolRegistry.Register(&ToolDef{
			Name:        fmt.Sprintf("extra_tool_%d", i),
			Description: fmt.Sprintf("Extra tool %d", i),
			Handler:     func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) { return "", nil },
		})
	}

	if !shouldUseCodeMode(cfg.toolRegistry) {
		t.Error("should use code mode when tools > threshold")
	}

	// With nil registry, should not use code mode.
	if shouldUseCodeMode(nil) {
		t.Error("should not use code mode with nil registry")
	}
}

func TestPluginHostList(t *testing.T) {
	dir := t.TempDir()
	scriptPath := createMockPluginScript(t, dir, "mock-plugin", "echo")

	cfg := &Config{
		Plugins: map[string]PluginConfig{
			"plugin-a": {
				Type:      "tool",
				Command:   scriptPath,
				AutoStart: true,
				Tools:     []string{"tool1", "tool2"},
			},
			"plugin-b": {
				Type:    "channel",
				Command: scriptPath,
			},
		},
	}

	host := NewPluginHost(cfg)

	// Start only plugin-a.
	if err := host.Start("plugin-a"); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer host.StopAll()

	list := host.List()
	if len(list) != 2 {
		t.Fatalf("list length = %d, want 2", len(list))
	}

	// Find entries by name.
	found := map[string]map[string]any{}
	for _, entry := range list {
		name := entry["name"].(string)
		found[name] = entry
	}

	if found["plugin-a"]["status"] != "running" {
		t.Errorf("plugin-a status = %v, want running", found["plugin-a"]["status"])
	}
	if found["plugin-b"]["status"] != "stopped" {
		t.Errorf("plugin-b status = %v, want stopped", found["plugin-b"]["status"])
	}
}

func TestPluginJSONRPCError(t *testing.T) {
	dir := t.TempDir()
	scriptPath := createMockPluginScript(t, dir, "mock-plugin", "error")

	cfg := &Config{
		Plugins: map[string]PluginConfig{
			"test-error": {
				Type:    "tool",
				Command: scriptPath,
			},
		},
	}

	host := NewPluginHost(cfg)
	if err := host.Start("test-error"); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer host.StopAll()

	result, err := host.Call("test-error", "tool/execute", nil)
	if err != nil {
		t.Fatalf("call should succeed (error is in result): %v", err)
	}

	var resp map[string]any
	if err := json.Unmarshal(result, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if resp["isError"] != true {
		t.Errorf("expected isError=true, got %v", resp["isError"])
	}
	if resp["error"] != "plugin error" {
		t.Errorf("error = %v, want 'plugin error'", resp["error"])
	}
}

func TestPluginHealth(t *testing.T) {
	dir := t.TempDir()
	scriptPath := createMockPluginScript(t, dir, "mock-plugin", "ping")

	cfg := &Config{
		Plugins: map[string]PluginConfig{
			"test-health": {
				Type:    "tool",
				Command: scriptPath,
			},
		},
	}

	host := NewPluginHost(cfg)

	// Health check before starting.
	health := host.Health("test-health")
	if health["status"] != "not_running" {
		t.Errorf("status = %v, want not_running", health["status"])
	}

	// Start and check health.
	if err := host.Start("test-health"); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer host.StopAll()

	health2 := host.Health("test-health")
	if health2["status"] != "running" {
		t.Errorf("status = %v, want running", health2["status"])
	}
	if health2["healthy"] != true {
		t.Errorf("healthy = %v, want true", health2["healthy"])
	}
}

func TestPluginAutoStart(t *testing.T) {
	dir := t.TempDir()
	scriptPath := createMockPluginScript(t, dir, "mock-plugin", "echo")

	cfg := &Config{
		Plugins: map[string]PluginConfig{
			"auto-yes": {
				Type:      "tool",
				Command:   scriptPath,
				AutoStart: true,
			},
			"auto-no": {
				Type:    "tool",
				Command: scriptPath,
			},
		},
	}

	host := NewPluginHost(cfg)
	host.AutoStart()
	defer host.StopAll()

	host.mu.RLock()
	_, hasYes := host.plugins["auto-yes"]
	_, hasNo := host.plugins["auto-no"]
	host.mu.RUnlock()

	if !hasYes {
		t.Error("auto-yes should be started")
	}
	if hasNo {
		t.Error("auto-no should not be started")
	}
}

func TestPluginNotify(t *testing.T) {
	dir := t.TempDir()
	scriptPath := createMockPluginScript(t, dir, "mock-plugin", "echo")

	cfg := &Config{
		Plugins: map[string]PluginConfig{
			"test-notify": {
				Type:    "tool",
				Command: scriptPath,
			},
		},
	}

	host := NewPluginHost(cfg)
	if err := host.Start("test-notify"); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer host.StopAll()

	// Notify should not return error for running plugin.
	err := host.Notify("test-notify", "channel/typing", map[string]string{"channel": "test"})
	if err != nil {
		t.Errorf("notify: %v", err)
	}

	// Notify to non-running plugin should fail.
	err2 := host.Notify("nonexistent", "test", nil)
	if err2 == nil {
		t.Error("expected error for nonexistent plugin")
	}
}

func TestPluginDuplicateStart(t *testing.T) {
	dir := t.TempDir()
	scriptPath := createMockPluginScript(t, dir, "mock-plugin", "echo")

	cfg := &Config{
		Plugins: map[string]PluginConfig{
			"test-dup": {
				Type:    "tool",
				Command: scriptPath,
			},
		},
	}

	host := NewPluginHost(cfg)
	if err := host.Start("test-dup"); err != nil {
		t.Fatalf("first start: %v", err)
	}
	defer host.StopAll()

	// Second start should fail.
	err := host.Start("test-dup")
	if err == nil {
		t.Fatal("expected error for duplicate start")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Errorf("error = %v, want 'already running'", err)
	}
}

// TestPluginResolveEnv verifies that plugin env vars with $ENV_VAR are resolved.
func TestPluginResolveEnv(t *testing.T) {
	// This tests the config resolution path.
	cfg := &Config{
		Plugins: map[string]PluginConfig{
			"test-env": {
				Type:    "sandbox",
				Command: "some-plugin",
				Env: map[string]string{
					"NORMAL":  "plain_value",
					"FROM_ENV": "$TEST_PLUGIN_SECRET",
				},
			},
		},
	}

	// Set the env var.
	os.Setenv("TEST_PLUGIN_SECRET", "secret123")
	defer os.Unsetenv("TEST_PLUGIN_SECRET")

	// Resolve secrets (same as config loading does).
	resolvePluginSecretsForTest(cfg)

	pcfg := cfg.Plugins["test-env"]
	if pcfg.Env["NORMAL"] != "plain_value" {
		t.Errorf("NORMAL = %q, want plain_value", pcfg.Env["NORMAL"])
	}
	if pcfg.Env["FROM_ENV"] != "secret123" {
		t.Errorf("FROM_ENV = %q, want secret123", pcfg.Env["FROM_ENV"])
	}
}

// resolvePluginSecretsForTest resolves $ENV_VAR in plugin env maps (test helper).
// In production, this is done inline in Config.resolveSecrets().
func resolvePluginSecretsForTest(cfg *Config) {
	for name, pcfg := range cfg.Plugins {
		if len(pcfg.Env) > 0 {
			for k, v := range pcfg.Env {
				pcfg.Env[k] = resolveEnvRef(v, fmt.Sprintf("plugins.%s.env.%s", name, k))
			}
			cfg.Plugins[name] = pcfg
		}
	}
}

// TestPluginNonexistentBinary tests starting a plugin with a binary that doesn't exist.
func TestPluginNonexistentBinary(t *testing.T) {
	cfg := &Config{
		Plugins: map[string]PluginConfig{
			"bad-binary": {
				Type:    "tool",
				Command: "/nonexistent/path/to/plugin",
			},
		},
	}

	host := NewPluginHost(cfg)
	err := host.Start("bad-binary")
	if err == nil {
		host.StopAll()
		t.Fatal("expected error for nonexistent binary")
	}
}

// TestPluginStopNotRunning tests stopping a plugin that's not running.
func TestPluginStopNotRunning(t *testing.T) {
	host := NewPluginHost(&Config{Plugins: map[string]PluginConfig{}})
	err := host.Stop("nonexistent")
	if err == nil {
		t.Fatal("expected error for stopping non-running plugin")
	}
	if !strings.Contains(err.Error(), "not running") {
		t.Errorf("error = %v, want 'not running'", err)
	}
}

// Verify shell is available for mock scripts.
func init() {
	if _, err := exec.LookPath("sh"); err != nil {
		panic("sh not found, plugin tests require a POSIX shell")
	}
}
