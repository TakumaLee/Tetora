package main

import (
	"encoding/json"
	"testing"
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
		Roles: map[string]RoleConfig{
			"test1": {
				ToolPolicy: RoleToolPolicy{
					Profile: "minimal",
					Allow:   []string{"read", "write"},
					Deny:    []string{"memory_search"},
				},
			},
			"test2": {
				ToolPolicy: RoleToolPolicy{
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
		Roles: map[string]RoleConfig{
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
		Roles: map[string]RoleConfig{
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
		Roles: map[string]RoleConfig{
			"admin": {
				ToolPolicy: RoleToolPolicy{
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
		Roles: map[string]RoleConfig{
			"test": {
				ToolPolicy: RoleToolPolicy{
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
