package main

import (
	"encoding/json"
	"testing"
)

func TestClaudeCodeProvider_Name(t *testing.T) {
	p := &ClaudeCodeProvider{binaryPath: "/usr/local/bin/claude"}
	if p.Name() != "claude-code" {
		t.Errorf("Name() = %q, want claude-code", p.Name())
	}
}

func TestClaudeCodeProvider_ImplementsProvider(t *testing.T) {
	// Verify ClaudeCodeProvider implements Provider (not ToolCapableProvider).
	var _ Provider = (*ClaudeCodeProvider)(nil)
}

func TestClaudeCodeProvider_Struct(t *testing.T) {
	cfg := &Config{}
	p := &ClaudeCodeProvider{
		binaryPath: "/opt/bin/claude",
		cfg:        cfg,
	}
	if p.binaryPath != "/opt/bin/claude" {
		t.Errorf("binaryPath = %q, want /opt/bin/claude", p.binaryPath)
	}
	if p.cfg != cfg {
		t.Error("cfg mismatch")
	}
}

func TestBuildClaudeCodeArgs_Basic(t *testing.T) {
	req := ProviderRequest{
		Model: "claude-sonnet-4-5-20250929",
	}
	args := buildClaudeCodeArgs(req)

	expected := []string{"--print", "--verbose", "--output-format", "stream-json", "--model", "claude-sonnet-4-5-20250929", "--permission-mode", "acceptEdits"}
	if len(args) != len(expected) {
		t.Fatalf("len(args) = %d, want %d; args = %v", len(args), len(expected), args)
	}
	for i, a := range args {
		if a != expected[i] {
			t.Errorf("args[%d] = %q, want %q", i, a, expected[i])
		}
	}
}

func TestBuildClaudeCodeArgs_AllOptions(t *testing.T) {
	req := ProviderRequest{
		Model:          "claude-opus-4-20250514",
		SessionID:      "test-session-123",
		SystemPrompt:   "You are a helpful assistant.",
		Budget:         1.50,
		PermissionMode: "bypassPermissions",
	}
	args := buildClaudeCodeArgs(req)

	// Verify each expected flag is present.
	assertContainsFlag(t, args, "--model", "claude-opus-4-20250514")
	assertContainsFlag(t, args, "--session-id", "test-session-123")
	assertContainsFlag(t, args, "--append-system-prompt", "You are a helpful assistant.")
	assertContainsFlag(t, args, "--permission-mode", "bypassPermissions")

	// --max-budget-usd is intentionally NOT passed (Tetora uses soft-limit approach).
	for _, a := range args {
		if a == "--max-budget-usd" {
			t.Error("--max-budget-usd should not be present (Tetora uses soft-limit, not hard budget)")
		}
	}
	// Verify --add-dir is NOT present.
	for _, a := range args {
		if a == "--add-dir" {
			t.Error("--add-dir should not be present in claude-code args")
		}
	}
}

func TestBuildClaudeCodeArgs_NoModel(t *testing.T) {
	req := ProviderRequest{}
	args := buildClaudeCodeArgs(req)

	// Should have --print, --output-format, stream-json, but no --model.
	for _, a := range args {
		if a == "--model" {
			t.Error("--model should not be present when Model is empty")
		}
	}
}

func TestBuildClaudeCodeArgs_NoBudget(t *testing.T) {
	req := ProviderRequest{
		Model: "claude-sonnet-4-5-20250929",
	}
	args := buildClaudeCodeArgs(req)

	for _, a := range args {
		if a == "--max-budget-usd" {
			t.Error("--max-budget-usd should not be present when Budget is 0")
		}
	}
}

func TestParseStreamJSON_ResultLine(t *testing.T) {
	// Simulate a stream-json result line from Claude Code CLI.
	line := `{"type":"result","subtype":"success","is_error":false,"duration_ms":1234,"duration_api_ms":1000,"num_turns":1,"result":"Hello!","session_id":"abc-123","cost_usd":0.001,"total_cost_usd":0.005,"usage":{"input_tokens":123,"output_tokens":45}}`

	var msg claudeStreamMsg
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if msg.Type != "result" {
		t.Errorf("Type = %q, want result", msg.Type)
	}
	if msg.Result != "Hello!" {
		t.Errorf("Result = %q, want Hello!", msg.Result)
	}
	if msg.SessionID != "abc-123" {
		t.Errorf("SessionID = %q, want abc-123", msg.SessionID)
	}
	if msg.CostUSD != 0.005 {
		t.Errorf("CostUSD = %f, want 0.005", msg.CostUSD)
	}
	if msg.DurationMs != 1234 {
		t.Errorf("DurationMs = %d, want 1234", msg.DurationMs)
	}
	if msg.Usage == nil {
		t.Fatal("Usage is nil")
	}
	if msg.Usage.InputTokens != 123 {
		t.Errorf("InputTokens = %d, want 123", msg.Usage.InputTokens)
	}
	if msg.Usage.OutputTokens != 45 {
		t.Errorf("OutputTokens = %d, want 45", msg.Usage.OutputTokens)
	}
	if msg.IsError {
		t.Error("IsError should be false")
	}
}

func TestParseStreamJSON_AssistantLine(t *testing.T) {
	line := `{"type":"assistant","message":{"id":"msg_01","type":"message","role":"assistant","content":[{"type":"text","text":"Hello world!"}],"model":"claude-sonnet-4-5-20250929","stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5}}}`

	var msg claudeStreamMsg
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if msg.Type != "assistant" {
		t.Errorf("Type = %q, want assistant", msg.Type)
	}
	if msg.Message == nil {
		t.Fatal("Message is nil")
	}
	if len(msg.Message.Content) != 1 {
		t.Fatalf("len(Content) = %d, want 1", len(msg.Message.Content))
	}
	if msg.Message.Content[0].Text != "Hello world!" {
		t.Errorf("Content[0].Text = %q, want Hello world!", msg.Message.Content[0].Text)
	}
}

func TestParseStreamJSON_ErrorResult(t *testing.T) {
	line := `{"type":"result","subtype":"error","is_error":true,"result":"","session_id":"","total_cost_usd":0,"usage":{"input_tokens":0,"output_tokens":0}}`

	var msg claudeStreamMsg
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if msg.Type != "result" {
		t.Errorf("Type = %q, want result", msg.Type)
	}
	if !msg.IsError {
		t.Error("IsError should be true")
	}
	if msg.Subtype != "error" {
		t.Errorf("Subtype = %q, want error", msg.Subtype)
	}
}

func TestBuildResultFromStream_ClaudeCode(t *testing.T) {
	msg := &claudeStreamMsg{
		Type:       "result",
		Subtype:    "success",
		Result:     "Task completed.",
		CostUSD:    0.01,
		SessionID:  "session-xyz",
		DurationMs: 2500,
		Usage:      &claudeUsage{InputTokens: 500, OutputTokens: 200},
	}

	pr := buildResultFromStream(msg, nil, 0)
	if pr.Output != "Task completed." {
		t.Errorf("Output = %q, want 'Task completed.'", pr.Output)
	}
	if pr.CostUSD != 0.01 {
		t.Errorf("CostUSD = %f, want 0.01", pr.CostUSD)
	}
	if pr.SessionID != "session-xyz" {
		t.Errorf("SessionID = %q, want session-xyz", pr.SessionID)
	}
	if pr.TokensIn != 500 {
		t.Errorf("TokensIn = %d, want 500", pr.TokensIn)
	}
	if pr.TokensOut != 200 {
		t.Errorf("TokensOut = %d, want 200", pr.TokensOut)
	}
	if pr.ProviderMs != 2500 {
		t.Errorf("ProviderMs = %d, want 2500", pr.ProviderMs)
	}
	if pr.IsError {
		t.Error("IsError should be false")
	}
}

// assertContainsFlag checks that args contains --flag value in order.
func assertContainsFlag(t *testing.T, args []string, flag, value string) {
	t.Helper()
	for i, a := range args {
		if a == flag {
			if i+1 < len(args) && args[i+1] == value {
				return
			}
			t.Errorf("flag %s found but value = %q, want %q", flag, args[i+1], value)
			return
		}
	}
	t.Errorf("flag %s not found in args: %v", flag, args)
}
