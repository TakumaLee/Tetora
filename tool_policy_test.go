package main

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// TestProfileResolution tests tool profile resolution.
func TestProfileResolution(t *testing.T) {
	cfg := &Config{
		Tools: ToolConfig{
			Profiles: map[string]ToolProfile{
				"custom": {
					Name:  "custom",
					Allow: []string{"read", "write"},
				},
			},
		},
	}

	tests := []struct {
		name         string
		profileName  string
		wantLen      int
		wantContains []string
	}{
		{
			name:         "minimal profile",
			profileName:  "minimal",
			wantLen:      3,
			wantContains: []string{"memory_search", "memory_get", "knowledge_search"},
		},
		{
			name:         "standard profile",
			profileName:  "standard",
			wantLen:      9,
			wantContains: []string{"read", "write", "exec", "memory_search"},
		},
		{
			name:         "custom profile",
			profileName:  "custom",
			wantLen:      2,
			wantContains: []string{"read", "write"},
		},
		{
			name:         "default to standard",
			profileName:  "",
			wantLen:      9,
			wantContains: []string{"read", "write", "exec"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profile := getProfile(cfg, tt.profileName)
			if len(profile.Allow) != tt.wantLen {
				t.Errorf("got %d tools, want %d", len(profile.Allow), tt.wantLen)
			}
			for _, tool := range tt.wantContains {
				found := false
				for _, allowed := range profile.Allow {
					if allowed == tool {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("profile missing expected tool: %s", tool)
				}
			}
		})
	}
}

// TestAllowDenyMerge tests allow/deny list merging.
func TestAllowDenyMerge(t *testing.T) {
	cfg := &Config{
		Tools: ToolConfig{},
		Agents: map[string]AgentConfig{
			"test1": {
				ToolPolicy: AgentToolPolicy{
					Profile: "minimal",
					Allow:   []string{"read", "write"},
					Deny:    []string{"memory_search"},
				},
			},
			"test2": {
				ToolPolicy: AgentToolPolicy{
					Profile: "standard",
					Deny:    []string{"exec", "edit"},
				},
			},
		},
	}
	cfg.toolRegistry = NewToolRegistry(cfg)

	// Test role test1: minimal + read,write - memory_search
	allowed := resolveAllowedTools(cfg, "test1")
	if allowed["memory_search"] {
		t.Error("memory_search should be denied")
	}
	if !allowed["read"] {
		t.Error("read should be allowed")
	}
	if !allowed["write"] {
		t.Error("write should be allowed")
	}
	if !allowed["memory_get"] {
		t.Error("memory_get from minimal should be allowed")
	}

	// Test role test2: standard - exec,edit
	allowed = resolveAllowedTools(cfg, "test2")
	if allowed["exec"] {
		t.Error("exec should be denied")
	}
	if allowed["edit"] {
		t.Error("edit should be denied")
	}
	if !allowed["read"] {
		t.Error("read from standard should be allowed")
	}
}

// TestTrustLevelFiltering tests trust-level filtering.
func TestTrustLevelFiltering(t *testing.T) {
	cfg := &Config{
		Tools: ToolConfig{},
		Agents: map[string]AgentConfig{
			"observer": {TrustLevel: TrustObserve},
			"suggester": {TrustLevel: TrustSuggest},
			"auto": {TrustLevel: TrustAuto},
		},
	}

	call := ToolCall{
		ID:    "test-1",
		Name:  "exec",
		Input: json.RawMessage(`{"command":"echo test"}`),
	}

	// Test observe mode.
	result, shouldExec := filterToolCall(cfg, "observer", call)
	if shouldExec {
		t.Error("observe mode should not execute")
	}
	if result == nil {
		t.Fatal("observe mode should return result")
	}
	if !containsString(result.Content, "OBSERVE MODE") {
		t.Errorf("observe result should contain 'OBSERVE MODE', got: %s", result.Content)
	}

	// Test suggest mode.
	result, shouldExec = filterToolCall(cfg, "suggester", call)
	if shouldExec {
		t.Error("suggest mode should not execute")
	}
	if result == nil {
		t.Fatal("suggest mode should return result")
	}
	if !containsString(result.Content, "APPROVAL REQUIRED") {
		t.Errorf("suggest result should contain 'APPROVAL REQUIRED', got: %s", result.Content)
	}

	// Test auto mode.
	result, shouldExec = filterToolCall(cfg, "auto", call)
	if !shouldExec {
		t.Error("auto mode should execute")
	}
	if result != nil {
		t.Error("auto mode should return nil result")
	}
}

// TestToolTrustOverride tests per-tool trust overrides.
func TestToolTrustOverride(t *testing.T) {
	cfg := &Config{
		Tools: ToolConfig{
			TrustOverride: map[string]string{
				"exec": TrustSuggest,
			},
		},
		Agents: map[string]AgentConfig{
			"test": {TrustLevel: TrustAuto},
		},
	}

	// exec should be suggest due to override, even though role is auto.
	level := getToolTrustLevel(cfg, "test", "exec")
	if level != TrustSuggest {
		t.Errorf("got trust level %s, want %s", level, TrustSuggest)
	}

	// read should be auto (no override).
	level = getToolTrustLevel(cfg, "test", "read")
	if level != TrustAuto {
		t.Errorf("got trust level %s, want %s", level, TrustAuto)
	}
}

// TestLoopDetection tests the enhanced loop detector.
func TestLoopDetection(t *testing.T) {
	detector := NewLoopDetector()

	input1 := json.RawMessage(`{"path":"/test"}`)
	input2 := json.RawMessage(`{"path":"/other"}`)

	// Same tool, same input - should detect loop after maxRepeat.
	detector.Record("read", input1)
	isLoop, _ := detector.Check("read", input1)
	if isLoop {
		t.Error("should not detect loop on first repeat")
	}

	detector.Record("read", input1)
	isLoop, _ = detector.Check("read", input1)
	if isLoop {
		t.Error("should not detect loop on second repeat")
	}

	detector.Record("read", input1)
	isLoop, msg := detector.Check("read", input1)
	if !isLoop {
		t.Error("should detect loop on third repeat")
	}
	if !containsString(msg, "loop detected") {
		t.Errorf("loop message should contain 'loop detected', got: %s", msg)
	}

	// Different input - no loop.
	detector.Reset()
	detector.Record("read", input1)
	detector.Record("read", input2)
	isLoop, _ = detector.Check("read", input1)
	if isLoop {
		t.Error("should not detect loop with different inputs")
	}
}

// TestLoopPatternDetection tests multi-tool pattern detection.
func TestLoopPatternDetection(t *testing.T) {
	detector := NewLoopDetector()

	input := json.RawMessage(`{"test":"value"}`)

	// Create A→B→A→B→A→B pattern.
	for i := 0; i < 6; i++ {
		if i%2 == 0 {
			detector.Record("toolA", input)
		} else {
			detector.Record("toolB", input)
		}
	}

	isLoop, msg := detector.detectToolLoopPattern()
	if !isLoop {
		t.Error("should detect repeating pattern")
	}
	if !containsString(msg, "pattern detected") {
		t.Errorf("pattern message should contain 'pattern detected', got: %s", msg)
	}
}

// TestLoopHistoryLimit tests that history is trimmed to maxHistory.
func TestLoopHistoryLimit(t *testing.T) {
	detector := NewLoopDetector()
	detector.maxHistory = 5

	input := json.RawMessage(`{"test":"value"}`)

	// Record 10 entries.
	for i := 0; i < 10; i++ {
		detector.Record("test", input)
	}

	// History should be trimmed to 5.
	if len(detector.history) != 5 {
		t.Errorf("got history length %d, want 5", len(detector.history))
	}
}

// TestFullProfileWildcard tests the "*" wildcard in full profile.
func TestFullProfileWildcard(t *testing.T) {
	cfg := &Config{
		Tools: ToolConfig{},
		Agents: map[string]AgentConfig{
			"admin": {
				ToolPolicy: AgentToolPolicy{
					Profile: "full",
				},
			},
		},
	}
	cfg.toolRegistry = NewToolRegistry(cfg)

	allowed := resolveAllowedTools(cfg, "admin")

	// Should have all registered tools.
	allTools := cfg.toolRegistry.List()
	if len(allowed) != len(allTools) {
		t.Errorf("full profile should allow all tools, got %d, want %d", len(allowed), len(allTools))
	}

	for _, tool := range allTools {
		if !allowed[tool.Name] {
			t.Errorf("full profile should allow %s", tool.Name)
		}
	}
}

// TestToolPolicySummary tests the summary generation.
func TestToolPolicySummary(t *testing.T) {
	cfg := &Config{
		Tools: ToolConfig{},
		Agents: map[string]AgentConfig{
			"test": {
				ToolPolicy: AgentToolPolicy{
					Profile: "standard",
					Allow:   []string{"extra_tool"},
					Deny:    []string{"exec"},
				},
			},
		},
	}
	cfg.toolRegistry = NewToolRegistry(cfg)

	summary := getToolPolicySummary(cfg, "test")

	if !containsString(summary, "standard") {
		t.Error("summary should contain profile name")
	}
	if !containsString(summary, "Allowed:") {
		t.Error("summary should contain allowed count")
	}
}

// containsString is defined in proactive_test.go

func containsStringHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// --- P28.0: Approval Gate Tests ---

func TestNeedsApproval(t *testing.T) {
	tests := []struct {
		name     string
		enabled  bool
		tools    []string
		toolName string
		want     bool
	}{
		{"disabled", false, []string{"exec"}, "exec", false},
		{"enabled, tool in list", true, []string{"exec", "write"}, "exec", true},
		{"enabled, tool not in list", true, []string{"exec", "write"}, "read", false},
		{"enabled, empty list", true, nil, "exec", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				ApprovalGates: ApprovalGateConfig{
					Enabled: tt.enabled,
					Tools:   tt.tools,
				},
			}
			got := needsApproval(cfg, tt.toolName)
			if got != tt.want {
				t.Errorf("needsApproval() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSummarizeToolCall(t *testing.T) {
	tests := []struct {
		name       string
		tc         ToolCall
		wantSubstr string
	}{
		{
			"exec",
			ToolCall{Name: "exec", Input: json.RawMessage(`{"command":"ls -la"}`)},
			"Run command: ls -la",
		},
		{
			"write",
			ToolCall{Name: "write", Input: json.RawMessage(`{"path":"/tmp/test.txt"}`)},
			"Write file: /tmp/test.txt",
		},
		{
			"email_send",
			ToolCall{Name: "email_send", Input: json.RawMessage(`{"to":"user@example.com"}`)},
			"Send email to: user@example.com",
		},
		{
			"generic",
			ToolCall{Name: "custom_tool", Input: json.RawMessage(`{"key":"value"}`)},
			"Execute custom_tool",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := summarizeToolCall(tt.tc)
			if !containsString(got, tt.wantSubstr) {
				t.Errorf("summarizeToolCall() = %q, want to contain %q", got, tt.wantSubstr)
			}
		})
	}
}

// mockApprovalGate is a test implementation of ApprovalGate.
type mockApprovalGate struct {
	respondWith  bool
	respondErr   error
	delay        time.Duration
	autoApproved map[string]bool
}

func (m *mockApprovalGate) RequestApproval(ctx context.Context, req ApprovalRequest) (bool, error) {
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	return m.respondWith, m.respondErr
}

func (m *mockApprovalGate) AutoApprove(toolName string) {
	if m.autoApproved == nil {
		m.autoApproved = make(map[string]bool)
	}
	m.autoApproved[toolName] = true
}

func (m *mockApprovalGate) IsAutoApproved(toolName string) bool {
	if m.autoApproved == nil {
		return false
	}
	return m.autoApproved[toolName]
}

func TestApprovalGateTimeout(t *testing.T) {
	gate := &mockApprovalGate{delay: 5 * time.Second}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	approved, err := gate.RequestApproval(ctx, ApprovalRequest{
		ID:   "test-1",
		Tool: "exec",
	})

	if approved {
		t.Error("should not be approved on timeout")
	}
	if err == nil {
		t.Error("should return error on timeout")
	}
}

func TestGateReason(t *testing.T) {
	if r := gateReason(nil, false); r != "rejected by user" {
		t.Errorf("got %q, want %q", r, "rejected by user")
	}
	if r := gateReason(fmt.Errorf("timeout"), false); r != "timeout" {
		t.Errorf("got %q, want %q", r, "timeout")
	}
	if r := gateReason(nil, true); r != "approved" {
		t.Errorf("got %q, want %q", r, "approved")
	}
}

func TestAutoApproveFlow(t *testing.T) {
	gate := &mockApprovalGate{respondWith: false}

	// Initially not auto-approved.
	if gate.IsAutoApproved("exec") {
		t.Error("exec should not be auto-approved initially")
	}

	// Auto-approve exec.
	gate.AutoApprove("exec")

	if !gate.IsAutoApproved("exec") {
		t.Error("exec should be auto-approved after AutoApprove")
	}

	// Other tools still not approved.
	if gate.IsAutoApproved("write") {
		t.Error("write should not be auto-approved")
	}
}

func TestConfigAutoApproveTools(t *testing.T) {
	cfg := &Config{
		ApprovalGates: ApprovalGateConfig{
			Enabled:          true,
			Tools:            []string{"exec", "write", "delete"},
			AutoApproveTools: []string{"exec"},
		},
	}

	// exec needs approval per config.
	if !needsApproval(cfg, "exec") {
		t.Error("exec should need approval")
	}

	// Simulate what dispatch.go does: check auto-approved before requesting.
	gate := &mockApprovalGate{}
	// Pre-load from config.
	for _, tool := range cfg.ApprovalGates.AutoApproveTools {
		gate.AutoApprove(tool)
	}

	// exec is auto-approved → skip gate.
	if !gate.IsAutoApproved("exec") {
		t.Error("exec should be auto-approved from config")
	}

	// write still needs full approval.
	if gate.IsAutoApproved("write") {
		t.Error("write should not be auto-approved")
	}

	// delete still needs full approval.
	if gate.IsAutoApproved("delete") {
		t.Error("delete should not be auto-approved")
	}
}
