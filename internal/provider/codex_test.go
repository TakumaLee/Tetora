package provider

import (
	"strings"
	"testing"
)

func TestBuildCodexArgs_Basic(t *testing.T) {
	req := Request{
		Model:  "o4-mini",
		Prompt: "hello",
	}
	args := BuildCodexArgs(req, true)

	if args[0] != "exec" {
		t.Errorf("expected first arg 'exec', got %s", args[0])
	}
	if !containsArg(args, "--json") {
		t.Error("expected --json for streaming")
	}
	if !containsArgPair(args, "--model", "o4-mini") {
		t.Error("expected --model o4-mini")
	}
	// Prompt should be the last positional arg
	if args[len(args)-1] != "hello" {
		t.Errorf("expected prompt as last arg, got %s", args[len(args)-1])
	}

	// Non-streaming should NOT have --json
	args2 := BuildCodexArgs(req, false)
	if containsArg(args2, "--json") {
		t.Error("should not have --json when not streaming")
	}
}

func TestBuildCodexArgs_PermissionModes(t *testing.T) {
	tests := []struct {
		mode     string
		expected string
	}{
		{"bypassPermissions", "--full-auto"},
		{"acceptEdits", "--full-auto"},
		{"", "--sandbox"},
	}
	for _, tc := range tests {
		req := Request{
			PermissionMode: tc.mode,
			Prompt:         "test",
		}
		args := BuildCodexArgs(req, false)
		if !containsArg(args, tc.expected) {
			t.Errorf("mode %q: expected %s in args %v", tc.mode, tc.expected, args)
		}
	}
}

func TestBuildCodexArgs_Resume(t *testing.T) {
	req := Request{
		Resume:    true,
		SessionID: "sess-123",
		Model:     "o4-mini",
	}
	args := BuildCodexArgs(req, false)
	// Should end with: resume sess-123
	if !containsArg(args, "resume") {
		t.Error("expected 'resume' in args")
	}
	if args[len(args)-1] != "sess-123" {
		t.Errorf("expected session ID as last arg, got %s", args[len(args)-1])
	}
}

func TestBuildCodexArgs_Ephemeral(t *testing.T) {
	// PersistSession=false → --ephemeral
	req := Request{
		PersistSession: false,
		Prompt:         "test",
	}
	args := BuildCodexArgs(req, false)
	if !containsArg(args, "--ephemeral") {
		t.Errorf("expected --ephemeral, args: %v", args)
	}

	// PersistSession=true → no --ephemeral
	req2 := Request{
		PersistSession: true,
		Prompt:         "test",
	}
	args2 := BuildCodexArgs(req2, false)
	if containsArg(args2, "--ephemeral") {
		t.Errorf("should not have --ephemeral when PersistSession, args: %v", args2)
	}
}

func TestParseCodexEvent_AgentMessage(t *testing.T) {
	jsonl := `{"type":"agent_message","content":"Hello world"}
{"type":"agent_message","content":" more text"}
{"type":"turn.completed","usage":{"input_tokens":100,"output_tokens":50}}`

	pr := ParseCodexOutput([]byte(jsonl), nil, 0)
	if pr.IsError {
		t.Fatalf("unexpected error: %s", pr.Error)
	}
	if pr.Output != "Hello world more text" {
		t.Errorf("expected 'Hello world more text', got %q", pr.Output)
	}
}

func TestParseCodexEvent_TurnCompleted(t *testing.T) {
	jsonl := `{"type":"agent_message","content":"Done"}
{"type":"turn.completed","usage":{"input_tokens":1500,"output_tokens":800}}`

	pr := ParseCodexOutput([]byte(jsonl), nil, 0)
	if pr.TokensIn != 1500 {
		t.Errorf("expected TokensIn=1500, got %d", pr.TokensIn)
	}
	if pr.TokensOut != 800 {
		t.Errorf("expected TokensOut=800, got %d", pr.TokensOut)
	}
}

func TestParseCodexEvent_TurnFailed(t *testing.T) {
	jsonl := `{"type":"agent_message","content":"Trying..."}
{"type":"turn.failed","error":"context cancelled"}`

	pr := ParseCodexOutput([]byte(jsonl), nil, 1)
	if !pr.IsError {
		t.Fatal("expected error")
	}
	if !strings.Contains(pr.Error, "context cancelled") {
		t.Errorf("expected 'context cancelled' in error, got %q", pr.Error)
	}
}

func TestParseCodexOutput_QuotaMessageBecomesError(t *testing.T) {
	jsonl := `{"type":"agent_message","content":"You're out of extra usage · resets 4am (Asia/Taipei)"}`

	pr := ParseCodexOutput([]byte(jsonl), nil, 0)
	if !pr.IsError {
		t.Fatal("expected quota message to be treated as error")
	}
	if pr.Output != "" {
		t.Errorf("expected output to be cleared, got %q", pr.Output)
	}
	if !strings.Contains(strings.ToLower(pr.Error), "out of extra usage") {
		t.Errorf("expected quota error, got %q", pr.Error)
	}
}

// helpers
func containsArg(args []string, target string) bool {
	for _, a := range args {
		if a == target {
			return true
		}
	}
	return false
}

func containsArgPair(args []string, key, value string) bool {
	for i, a := range args {
		if a == key && i+1 < len(args) && args[i+1] == value {
			return true
		}
	}
	return false
}
