package main

import (
	"testing"
)

// --- resolveProviderName tests ---

func TestResolveProviderName_TaskOverride(t *testing.T) {
	cfg := &Config{DefaultProvider: "claude"}
	task := Task{Provider: "openai"}
	got := resolveProviderName(cfg, task, "")
	if got != "openai" {
		t.Errorf("expected openai, got %s", got)
	}
}

func TestResolveProviderName_RoleFallback(t *testing.T) {
	cfg := &Config{
		DefaultProvider: "claude",
		Roles: map[string]RoleConfig{
			"helper": {Provider: "ollama"},
		},
	}
	task := Task{}
	got := resolveProviderName(cfg, task, "helper")
	if got != "ollama" {
		t.Errorf("expected ollama, got %s", got)
	}
}

func TestResolveProviderName_ConfigDefault(t *testing.T) {
	cfg := &Config{DefaultProvider: "openai"}
	task := Task{}
	got := resolveProviderName(cfg, task, "")
	if got != "openai" {
		t.Errorf("expected openai, got %s", got)
	}
}

func TestResolveProviderName_FallbackClaude(t *testing.T) {
	cfg := &Config{}
	task := Task{}
	got := resolveProviderName(cfg, task, "")
	if got != "claude" {
		t.Errorf("expected claude, got %s", got)
	}
}

func TestResolveProviderName_PriorityChain(t *testing.T) {
	// Task > role > config default
	cfg := &Config{
		DefaultProvider: "default-provider",
		Roles: map[string]RoleConfig{
			"r": {Provider: "role-provider"},
		},
	}
	task := Task{Provider: "task-provider"}
	got := resolveProviderName(cfg, task, "r")
	if got != "task-provider" {
		t.Errorf("expected task-provider, got %s", got)
	}

	// Role > config default (no task override)
	task2 := Task{}
	got2 := resolveProviderName(cfg, task2, "r")
	if got2 != "role-provider" {
		t.Errorf("expected role-provider, got %s", got2)
	}
}

func TestResolveProviderName_RoleWithoutProvider(t *testing.T) {
	cfg := &Config{
		DefaultProvider: "mydefault",
		Roles: map[string]RoleConfig{
			"norole": {Model: "some-model"},
		},
	}
	task := Task{}
	got := resolveProviderName(cfg, task, "norole")
	if got != "mydefault" {
		t.Errorf("expected mydefault, got %s", got)
	}
}

// --- Provider Registry tests ---

func TestProviderRegistry_RegisterAndGet(t *testing.T) {
	reg := newProviderRegistry()
	p := &OpenAIProvider{name: "test", baseURL: "http://localhost", defaultModel: "m"}
	reg.register("test", p)

	got, err := reg.get("test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name() != "test" {
		t.Errorf("expected test, got %s", got.Name())
	}
}

func TestProviderRegistry_GetNotFound(t *testing.T) {
	reg := newProviderRegistry()
	_, err := reg.get("missing")
	if err == nil {
		t.Fatal("expected error for missing provider")
	}
}

// --- OpenAI response parsing tests ---

func TestParseOpenAIResponse_Success(t *testing.T) {
	data := []byte(`{
		"id": "chatcmpl-123",
		"choices": [{"message": {"content": "Hello!"}, "finish_reason": "stop"}],
		"usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
	}`)
	result := parseOpenAIResponse(data, 500)
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.Output != "Hello!" {
		t.Errorf("expected Hello!, got %s", result.Output)
	}
	if result.SessionID != "chatcmpl-123" {
		t.Errorf("expected chatcmpl-123, got %s", result.SessionID)
	}
	if result.DurationMs != 500 {
		t.Errorf("expected 500ms, got %d", result.DurationMs)
	}
	if result.CostUSD <= 0 {
		t.Error("expected positive cost estimate")
	}
	if result.StopReason != "end_turn" {
		t.Errorf("expected StopReason=end_turn, got %s", result.StopReason)
	}
}

func TestParseOpenAIResponse_ToolCalls(t *testing.T) {
	data := []byte(`{
		"id": "chatcmpl-tc",
		"choices": [{
			"message": {
				"content": "Checking.",
				"tool_calls": [
					{"id": "call_1", "type": "function", "function": {"name": "read_file", "arguments": "{\"path\":\"/tmp\"}"}}
				]
			},
			"finish_reason": "tool_calls"
		}],
		"usage": {"prompt_tokens": 50, "completion_tokens": 30, "total_tokens": 80}
	}`)
	result := parseOpenAIResponse(data, 200)
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.StopReason != "tool_use" {
		t.Errorf("StopReason = %q, want tool_use", result.StopReason)
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("len(ToolCalls) = %d, want 1", len(result.ToolCalls))
	}
	if result.ToolCalls[0].ID != "call_1" {
		t.Errorf("ToolCalls[0].ID = %q, want call_1", result.ToolCalls[0].ID)
	}
	if result.ToolCalls[0].Name != "read_file" {
		t.Errorf("ToolCalls[0].Name = %q, want read_file", result.ToolCalls[0].Name)
	}
}

func TestParseOpenAIResponse_APIError(t *testing.T) {
	data := []byte(`{
		"error": {"message": "rate limit exceeded", "type": "rate_limit"}
	}`)
	result := parseOpenAIResponse(data, 100)
	if !result.IsError {
		t.Fatal("expected error")
	}
	if result.Error != "rate limit exceeded" {
		t.Errorf("expected rate limit exceeded, got %s", result.Error)
	}
}

func TestParseOpenAIResponse_InvalidJSON(t *testing.T) {
	data := []byte(`not json`)
	result := parseOpenAIResponse(data, 100)
	if !result.IsError {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestParseOpenAIResponse_NoChoices(t *testing.T) {
	data := []byte(`{"id": "x", "choices": []}`)
	result := parseOpenAIResponse(data, 100)
	if result.IsError {
		t.Fatal("should not error with empty choices")
	}
	if result.Output != "" {
		t.Errorf("expected empty output, got %s", result.Output)
	}
}

func TestParseOpenAIResponse_NoUsage(t *testing.T) {
	data := []byte(`{
		"id": "x",
		"choices": [{"message": {"content": "hi"}, "finish_reason": "stop"}]
	}`)
	result := parseOpenAIResponse(data, 200)
	if result.CostUSD != 0 {
		t.Errorf("expected 0 cost without usage, got %f", result.CostUSD)
	}
}

// --- estimateOpenAICost tests ---

func TestEstimateOpenAICost(t *testing.T) {
	// 1000 input + 1000 output tokens
	cost := estimateOpenAICost(1000, 1000)
	expected := 1000*2.50/1_000_000 + 1000*10.00/1_000_000
	if cost != expected {
		t.Errorf("expected %f, got %f", expected, cost)
	}
}

func TestEstimateOpenAICost_Zero(t *testing.T) {
	cost := estimateOpenAICost(0, 0)
	if cost != 0 {
		t.Errorf("expected 0, got %f", cost)
	}
}

// --- truncateBytes tests ---

func TestTruncateBytes_Short(t *testing.T) {
	got := truncateBytes([]byte("hello"), 10)
	if got != "hello" {
		t.Errorf("expected hello, got %s", got)
	}
}

func TestTruncateBytes_Long(t *testing.T) {
	got := truncateBytes([]byte("hello world"), 5)
	if got != "hello" {
		t.Errorf("expected hello, got %s", got)
	}
}

// --- initProviders tests ---

func TestInitProviders_BackwardCompat(t *testing.T) {
	cfg := &Config{
		ClaudePath: "/usr/bin/claude",
		Providers:  map[string]ProviderConfig{},
	}
	// Even with empty providers map, should auto-create "claude".
	reg := initProviders(cfg)
	p, err := reg.get("claude")
	if err != nil {
		t.Fatalf("expected claude provider: %v", err)
	}
	if p.Name() != "claude" {
		t.Errorf("expected claude, got %s", p.Name())
	}
}

func TestInitProviders_OpenAIProvider(t *testing.T) {
	cfg := &Config{
		Providers: map[string]ProviderConfig{
			"myopenai": {
				Type:    "openai-compatible",
				BaseURL: "http://localhost:8080/v1",
				APIKey:  "test-key",
				Model:   "gpt-4o",
			},
		},
	}
	reg := initProviders(cfg)
	p, err := reg.get("myopenai")
	if err != nil {
		t.Fatalf("expected myopenai provider: %v", err)
	}
	if p.Name() != "myopenai" {
		t.Errorf("expected myopenai, got %s", p.Name())
	}
}

func TestInitProviders_ClaudeCLIProvider(t *testing.T) {
	cfg := &Config{
		Providers: map[string]ProviderConfig{
			"claude": {
				Type: "claude-cli",
				Path: "claude",
			},
		},
	}
	reg := initProviders(cfg)
	p, err := reg.get("claude")
	if err != nil {
		t.Fatalf("expected claude provider: %v", err)
	}
	if p.Name() != "claude" {
		t.Errorf("expected claude, got %s", p.Name())
	}
}

// --- parseClaudeOutput tests ---

func TestParseClaudeOutput_Success(t *testing.T) {
	stdout := []byte(`{"type":"result","subtype":"","result":"Done!","is_error":false,"duration_ms":1234,"total_cost_usd":0.05,"session_id":"s123","num_turns":3}`)
	result := parseClaudeOutput(stdout, nil, 0)
	if result.Status != "success" {
		t.Errorf("expected success, got %s", result.Status)
	}
	if result.Output != "Done!" {
		t.Errorf("expected Done!, got %s", result.Output)
	}
	if result.CostUSD != 0.05 {
		t.Errorf("expected 0.05, got %f", result.CostUSD)
	}
	if result.SessionID != "s123" {
		t.Errorf("expected s123, got %s", result.SessionID)
	}
}

func TestParseClaudeOutput_Error(t *testing.T) {
	stdout := []byte(`{"type":"result","subtype":"api_error","result":"","is_error":true,"duration_ms":100,"total_cost_usd":0,"session_id":"","num_turns":0}`)
	result := parseClaudeOutput(stdout, nil, 1)
	if result.Status != "error" {
		t.Errorf("expected error, got %s", result.Status)
	}
	if result.Error != "api_error" {
		t.Errorf("expected api_error, got %s", result.Error)
	}
}

func TestParseClaudeOutput_NonJSON(t *testing.T) {
	stdout := []byte("plain text output")
	result := parseClaudeOutput(stdout, []byte("some error"), 0)
	if result.Status != "success" {
		t.Errorf("expected success for exit 0, got %s", result.Status)
	}
	if result.Output != "plain text output" {
		t.Errorf("expected plain text output, got %s", result.Output)
	}
}

func TestParseClaudeOutput_NonJSONWithError(t *testing.T) {
	stdout := []byte("")
	stderr := []byte("command not found")
	result := parseClaudeOutput(stdout, stderr, 127)
	if result.Status != "error" {
		t.Errorf("expected error, got %s", result.Status)
	}
	if result.Error != "command not found" {
		t.Errorf("expected 'command not found', got %s", result.Error)
	}
}

// --- Token extraction tests ---

func TestParseClaudeOutput_WithUsage(t *testing.T) {
	stdout := []byte(`{
		"type":"result","subtype":"","result":"Done!","is_error":false,
		"duration_ms":2500,"total_cost_usd":0.08,"session_id":"s456","num_turns":2,
		"usage":{"input_tokens":1500,"output_tokens":800}
	}`)
	result := parseClaudeOutput(stdout, nil, 0)
	if result.Status != "success" {
		t.Errorf("expected success, got %s", result.Status)
	}
	if result.TokensIn != 1500 {
		t.Errorf("expected TokensIn=1500, got %d", result.TokensIn)
	}
	if result.TokensOut != 800 {
		t.Errorf("expected TokensOut=800, got %d", result.TokensOut)
	}
	if result.ProviderMs != 2500 {
		t.Errorf("expected ProviderMs=2500, got %d", result.ProviderMs)
	}
}

func TestParseClaudeOutput_WithoutUsage(t *testing.T) {
	stdout := []byte(`{
		"type":"result","result":"OK","is_error":false,
		"duration_ms":1000,"total_cost_usd":0.01,"session_id":"s789","num_turns":1
	}`)
	result := parseClaudeOutput(stdout, nil, 0)
	if result.TokensIn != 0 {
		t.Errorf("expected TokensIn=0 when no usage, got %d", result.TokensIn)
	}
	if result.TokensOut != 0 {
		t.Errorf("expected TokensOut=0 when no usage, got %d", result.TokensOut)
	}
	if result.ProviderMs != 1000 {
		t.Errorf("expected ProviderMs=1000, got %d", result.ProviderMs)
	}
}

func TestParseOpenAIResponse_TokensExtracted(t *testing.T) {
	data := []byte(`{
		"id": "chatcmpl-tok",
		"choices": [{"message": {"content": "Hello!"}, "finish_reason": "stop"}],
		"usage": {"prompt_tokens": 200, "completion_tokens": 50, "total_tokens": 250}
	}`)
	result := parseOpenAIResponse(data, 300)
	if result.TokensIn != 200 {
		t.Errorf("expected TokensIn=200, got %d", result.TokensIn)
	}
	if result.TokensOut != 50 {
		t.Errorf("expected TokensOut=50, got %d", result.TokensOut)
	}
	if result.ProviderMs != 300 {
		t.Errorf("expected ProviderMs=300, got %d", result.ProviderMs)
	}
}

func TestParseOpenAIResponse_NoUsageNoTokens(t *testing.T) {
	data := []byte(`{
		"id": "chatcmpl-x",
		"choices": [{"message": {"content": "hi"}, "finish_reason": "stop"}]
	}`)
	result := parseOpenAIResponse(data, 100)
	if result.TokensIn != 0 {
		t.Errorf("expected TokensIn=0, got %d", result.TokensIn)
	}
	if result.TokensOut != 0 {
		t.Errorf("expected TokensOut=0, got %d", result.TokensOut)
	}
	// ProviderMs is still set from wall-clock for OpenAI
	if result.ProviderMs != 100 {
		t.Errorf("expected ProviderMs=100, got %d", result.ProviderMs)
	}
}
