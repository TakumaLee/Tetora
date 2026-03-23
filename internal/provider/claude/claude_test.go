package claude

import (
	"testing"

	"tetora/internal/provider"
)

// --- ParseOutput tests ---

func TestParseOutput_Success(t *testing.T) {
	stdout := []byte(`{"type":"result","subtype":"","result":"Done!","is_error":false,"duration_ms":1234,"total_cost_usd":0.05,"session_id":"s123","num_turns":3}`)
	result := ParseOutput(stdout, nil, 0)
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

func TestParseOutput_Error(t *testing.T) {
	stdout := []byte(`{"type":"result","subtype":"api_error","result":"","is_error":true,"duration_ms":100,"total_cost_usd":0,"session_id":"","num_turns":0}`)
	result := ParseOutput(stdout, nil, 1)
	if result.Status != "error" {
		t.Errorf("expected error, got %s", result.Status)
	}
	if result.Error != "api_error" {
		t.Errorf("expected api_error, got %s", result.Error)
	}
}

func TestParseOutput_NonJSON(t *testing.T) {
	stdout := []byte("plain text output")
	result := ParseOutput(stdout, []byte("some error"), 0)
	if result.Status != "success" {
		t.Errorf("expected success for exit 0, got %s", result.Status)
	}
	if result.Output != "plain text output" {
		t.Errorf("expected plain text output, got %s", result.Output)
	}
}

func TestParseOutput_NonJSONWithError(t *testing.T) {
	stdout := []byte("")
	stderr := []byte("command not found")
	result := ParseOutput(stdout, stderr, 127)
	if result.Status != "error" {
		t.Errorf("expected error, got %s", result.Status)
	}
	if result.Error != "command not found" {
		t.Errorf("expected 'command not found', got %s", result.Error)
	}
}

func TestParseOutput_WithUsage(t *testing.T) {
	stdout := []byte(`{
		"type":"result","subtype":"","result":"Done!","is_error":false,
		"duration_ms":2500,"total_cost_usd":0.08,"session_id":"s456","num_turns":2,
		"usage":{"input_tokens":1500,"output_tokens":800}
	}`)
	result := ParseOutput(stdout, nil, 0)
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

func TestParseOutput_WithoutUsage(t *testing.T) {
	stdout := []byte(`{
		"type":"result","result":"OK","is_error":false,
		"duration_ms":1000,"total_cost_usd":0.01,"session_id":"s789","num_turns":1
	}`)
	result := ParseOutput(stdout, nil, 0)
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

func TestParseOutput_ArrayFormatWithStringContent(t *testing.T) {
	stdout := []byte(`[
		{"type":"system","subtype":"init","message":{"role":"system","content":"init"},"session_id":"s1"},
		{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Hello"}]}},
		{"type":"rate_limit_event","message":{"role":"system","content":"throttled"}},
		{"type":"result","result":"Done","is_error":false,"duration_ms":5000,"total_cost_usd":0.28,"session_id":"s1","num_turns":3,"usage":{"input_tokens":100,"output_tokens":50,"cache_creation_input_tokens":200,"cache_read_input_tokens":300}}
	]`)
	result := ParseOutput(stdout, nil, 0)
	if result.Status != "success" {
		t.Errorf("expected success, got %s (error: %s)", result.Status, result.Error)
	}
	if result.CostUSD != 0.28 {
		t.Errorf("expected CostUSD=0.28, got %f", result.CostUSD)
	}
	// TotalInputTokens = input_tokens + cache_creation + cache_read = 100+200+300 = 600
	if result.TokensIn != 600 {
		t.Errorf("expected TokensIn=600, got %d", result.TokensIn)
	}
	if result.TokensOut != 50 {
		t.Errorf("expected TokensOut=50, got %d", result.TokensOut)
	}
	if result.SessionID != "s1" {
		t.Errorf("expected SessionID=s1, got %s", result.SessionID)
	}
}

// --- BuildArgs tests ---

func TestBuildArgs_Basic(t *testing.T) {
	req := provider.Request{
		Model:          "opus",
		SessionID:      "s123",
		PermissionMode: "acceptEdits",
		Prompt:         "hello world",
	}
	args := BuildArgs(req, false)
	assertContainsSequence(t, args, "--model", "opus")
	assertContainsSequence(t, args, "--session-id", "s123")
	assertContainsSequence(t, args, "--permission-mode", "acceptEdits")
	assertContains(t, args, "--print")
	assertContains(t, args, "--no-session-persistence")
	// Prompt should NOT be in args (piped via stdin instead).
	for _, a := range args {
		if a == "hello world" {
			t.Error("prompt should not be in args; it is piped via stdin")
		}
	}
}

func TestBuildArgs_WithBudget(t *testing.T) {
	// --max-budget-usd is intentionally NOT passed to Claude CLI.
	req := provider.Request{
		Model:          "opus",
		SessionID:      "s",
		PermissionMode: "plan",
		Budget:         5.50,
		Prompt:         "hi",
	}
	args := BuildArgs(req, false)
	for _, a := range args {
		if a == "--max-budget-usd" {
			t.Error("--max-budget-usd should NOT be passed (soft-limit approach)")
		}
	}
}

func TestBuildArgs_WithAddDirs(t *testing.T) {
	req := provider.Request{
		Model:          "opus",
		SessionID:      "s",
		PermissionMode: "plan",
		AddDirs:        []string{"/dir1", "/dir2"},
		Prompt:         "hi",
	}
	args := BuildArgs(req, false)
	assertContainsSequence(t, args, "--add-dir", "/dir1")
	assertContainsSequence(t, args, "--add-dir", "/dir2")
}

func TestBuildArgs_WithMCP(t *testing.T) {
	req := provider.Request{
		Model:          "opus",
		SessionID:      "s",
		PermissionMode: "plan",
		MCPPath:        "/tmp/mcp.json",
		Prompt:         "hi",
	}
	args := BuildArgs(req, false)
	assertContainsSequence(t, args, "--mcp-config", "/tmp/mcp.json")
}

func TestBuildArgs_WithSystemPrompt(t *testing.T) {
	req := provider.Request{
		Model:          "opus",
		SessionID:      "s",
		PermissionMode: "plan",
		SystemPrompt:   "You are a helper",
		Prompt:         "hi",
	}
	args := BuildArgs(req, false)
	assertContainsSequence(t, args, "--append-system-prompt", "You are a helper")
}

func TestBuildArgs_Streaming(t *testing.T) {
	req := provider.Request{
		Model:     "opus",
		SessionID: "s",
	}
	argsStream := BuildArgs(req, true)
	assertContainsSequence(t, argsStream, "--output-format", "stream-json")

	argsNonStream := BuildArgs(req, false)
	assertContainsSequence(t, argsNonStream, "--output-format", "json")
}

func TestBuildArgs_WithAllowedTools(t *testing.T) {
	req := provider.Request{
		Model:        "opus",
		SessionID:    "s",
		AllowedTools: []string{"Bash", "Read", "Grep"},
	}
	args := BuildArgs(req, false)
	assertContainsSequence(t, args, "--allowedTools", "Bash,Read,Grep")
}

func TestBuildArgs_WithoutAllowedTools(t *testing.T) {
	req := provider.Request{
		Model:     "opus",
		SessionID: "s",
	}
	args := BuildArgs(req, false)
	for _, a := range args {
		if a == "--allowedTools" {
			t.Error("--allowedTools should not be present when AllowedTools is empty")
		}
	}
}

// --- shouldUseDocker tests ---

func TestShouldUseDocker_TaskOverrideTrue(t *testing.T) {
	p := &Provider{dockerEnabled: false}
	v := true
	req := provider.Request{Docker: &v}
	if !p.shouldUseDocker(req) {
		t.Error("expected true when task Docker=true")
	}
}

func TestShouldUseDocker_TaskOverrideFalse(t *testing.T) {
	p := &Provider{dockerEnabled: true}
	v := false
	req := provider.Request{Docker: &v}
	if p.shouldUseDocker(req) {
		t.Error("expected false when task Docker=false overrides config")
	}
}

func TestShouldUseDocker_ConfigEnabled(t *testing.T) {
	p := &Provider{dockerEnabled: true}
	req := provider.Request{}
	if !p.shouldUseDocker(req) {
		t.Error("expected true when dockerEnabled=true and no override")
	}
}

func TestShouldUseDocker_ConfigDisabled(t *testing.T) {
	p := &Provider{dockerEnabled: false}
	req := provider.Request{}
	if p.shouldUseDocker(req) {
		t.Error("expected false when Docker not configured")
	}
}

// --- isStaleSessionError tests ---

func TestIsStaleSessionError_Match(t *testing.T) {
	pr := &provider.Result{
		IsError:   true,
		Error:     "error_during_execution",
		TokensIn:  0,
		TokensOut: 0,
	}
	if !isStaleSessionError(pr) {
		t.Error("expected true for error_during_execution with 0 tokens")
	}
}

func TestIsStaleSessionError_NotAnError(t *testing.T) {
	pr := &provider.Result{
		IsError:   false,
		TokensIn:  0,
		TokensOut: 0,
	}
	if isStaleSessionError(pr) {
		t.Error("expected false when IsError=false")
	}
}

func TestIsStaleSessionError_DifferentSubtype(t *testing.T) {
	pr := &provider.Result{
		IsError:   true,
		Error:     "api_error",
		TokensIn:  0,
		TokensOut: 0,
	}
	if isStaleSessionError(pr) {
		t.Error("expected false for api_error (not error_during_execution)")
	}
}

func TestIsStaleSessionError_NonZeroTokens(t *testing.T) {
	pr := &provider.Result{
		IsError:   true,
		Error:     "error_during_execution",
		TokensIn:  100,
		TokensOut: 50,
	}
	if isStaleSessionError(pr) {
		t.Error("expected false when tokens were consumed (error happened mid-execution)")
	}
}

func TestIsStaleSessionError_Nil(t *testing.T) {
	if isStaleSessionError(nil) {
		t.Error("expected false for nil result")
	}
}

// --- Test helpers ---

func assertContains(t *testing.T, args []string, val string) {
	t.Helper()
	for _, a := range args {
		if a == val {
			return
		}
	}
	t.Errorf("expected args to contain %q, got %v", val, args)
}

func assertContainsSequence(t *testing.T, args []string, key, val string) {
	t.Helper()
	for i := 0; i < len(args)-1; i++ {
		if args[i] == key && args[i+1] == val {
			return
		}
	}
	t.Errorf("expected args to contain %q %q sequence, got %v", key, val, args)
}
