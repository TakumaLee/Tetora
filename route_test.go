package main

import (
	"testing"
)

// --- classifyByKeywords ---

func TestClassifyByKeywords_RuleMatch(t *testing.T) {
	cfg := &Config{
		SmartDispatch: SmartDispatchConfig{
			Rules: []RoutingRule{
				{Role: "黒曜", Keywords: []string{"security", "code review"}},
				{Role: "琥珀", Keywords: []string{"寫文", "content"}},
			},
		},
		Roles: map[string]RoleConfig{
			"黒曜": {Model: "sonnet"},
			"琥珀": {Model: "opus"},
		},
	}

	tests := []struct {
		prompt   string
		wantRole string
		wantNil  bool
	}{
		{"Please do a security audit", "黒曜", false},
		{"Can you do a code review?", "黒曜", false},
		{"寫文章給 Medium", "琥珀", false},
		{"Create content for the blog", "琥珀", false},
		{"SECURITY check please", "黒曜", false}, // case insensitive
		{"random unmatched prompt", "", true},
	}

	for _, tt := range tests {
		result := classifyByKeywords(cfg, tt.prompt)
		if tt.wantNil {
			if result != nil {
				t.Errorf("classifyByKeywords(%q) = %+v, want nil", tt.prompt, result)
			}
			continue
		}
		if result == nil {
			t.Errorf("classifyByKeywords(%q) = nil, want role=%q", tt.prompt, tt.wantRole)
			continue
		}
		if result.Role != tt.wantRole {
			t.Errorf("classifyByKeywords(%q).Role = %q, want %q", tt.prompt, result.Role, tt.wantRole)
		}
		if result.Method != "keyword" {
			t.Errorf("classifyByKeywords(%q).Method = %q, want keyword", tt.prompt, result.Method)
		}
	}
}

func TestClassifyByKeywords_PatternMatch(t *testing.T) {
	cfg := &Config{
		SmartDispatch: SmartDispatchConfig{
			Rules: []RoutingRule{
				{Role: "翡翠", Patterns: []string{`market\s+research`, `competitor\s+analysis`}},
			},
		},
		Roles: map[string]RoleConfig{
			"翡翠": {Model: "sonnet"},
		},
	}

	tests := []struct {
		prompt   string
		wantRole string
		wantNil  bool
	}{
		{"Do market research on AI tools", "翡翠", false},
		{"Run competitor analysis", "翡翠", false},
		{"Market Research please", "翡翠", false}, // case insensitive
		{"marketresearch", "", true},               // no space = no match
	}

	for _, tt := range tests {
		result := classifyByKeywords(cfg, tt.prompt)
		if tt.wantNil {
			if result != nil {
				t.Errorf("classifyByKeywords(%q) = %+v, want nil", tt.prompt, result)
			}
			continue
		}
		if result == nil {
			t.Errorf("classifyByKeywords(%q) = nil, want role=%q", tt.prompt, tt.wantRole)
			continue
		}
		if result.Role != tt.wantRole {
			t.Errorf("classifyByKeywords(%q).Role = %q, want %q", tt.prompt, result.Role, tt.wantRole)
		}
	}
}

func TestClassifyByKeywords_RoleKeywords(t *testing.T) {
	cfg := &Config{
		SmartDispatch: SmartDispatchConfig{
			Rules: []RoutingRule{}, // no explicit rules
		},
		Roles: map[string]RoleConfig{
			"琉璃": {Model: "sonnet", Keywords: []string{"管理", "status"}},
			"翡翠": {Model: "sonnet", Keywords: []string{"情報", "research"}},
		},
	}

	result := classifyByKeywords(cfg, "show me the system status")
	if result == nil {
		t.Fatal("classifyByKeywords returned nil, want role=琉璃")
	}
	if result.Role != "琉璃" {
		t.Errorf("Role = %q, want 琉璃", result.Role)
	}
	if result.Confidence != "medium" {
		t.Errorf("Confidence = %q, want medium (role keyword match)", result.Confidence)
	}
}

func TestClassifyByKeywords_RulePriorityOverRole(t *testing.T) {
	cfg := &Config{
		SmartDispatch: SmartDispatchConfig{
			Rules: []RoutingRule{
				{Role: "黒曜", Keywords: []string{"security"}},
			},
		},
		Roles: map[string]RoleConfig{
			"琉璃": {Model: "sonnet", Keywords: []string{"security"}}, // same keyword on role
			"黒曜": {Model: "sonnet"},
		},
	}

	result := classifyByKeywords(cfg, "run a security audit")
	if result == nil {
		t.Fatal("classifyByKeywords returned nil")
	}
	// Rule should take priority over role keyword.
	if result.Role != "黒曜" {
		t.Errorf("Role = %q, want 黒曜 (rule priority)", result.Role)
	}
	if result.Confidence != "high" {
		t.Errorf("Confidence = %q, want high (rule match)", result.Confidence)
	}
}

// --- parseLLMRouteResult ---

func TestParseLLMRouteResult_ValidJSON(t *testing.T) {
	output := `{"role":"翡翠","confidence":"high","reason":"market analysis task"}`
	result, err := parseLLMRouteResult(output, "琉璃")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Role != "翡翠" {
		t.Errorf("Role = %q, want 翡翠", result.Role)
	}
	if result.Confidence != "high" {
		t.Errorf("Confidence = %q, want high", result.Confidence)
	}
	if result.Method != "llm" {
		t.Errorf("Method = %q, want llm", result.Method)
	}
}

func TestParseLLMRouteResult_WrappedJSON(t *testing.T) {
	output := `Here's my analysis:
{"role":"黒曜","confidence":"medium","reason":"looks like code"}
That's my recommendation.`

	result, err := parseLLMRouteResult(output, "琉璃")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Role != "黒曜" {
		t.Errorf("Role = %q, want 黒曜", result.Role)
	}
}

func TestParseLLMRouteResult_Garbage(t *testing.T) {
	output := "I think you should use the engineering team"
	result, err := parseLLMRouteResult(output, "琉璃")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should fall back to default.
	if result.Role != "琉璃" {
		t.Errorf("Role = %q, want 琉璃 (default)", result.Role)
	}
	if result.Confidence != "low" {
		t.Errorf("Confidence = %q, want low", result.Confidence)
	}
}

func TestParseLLMRouteResult_EmptyRole(t *testing.T) {
	output := `{"role":"","confidence":"medium","reason":"unclear"}`
	result, err := parseLLMRouteResult(output, "琉璃")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Role != "琉璃" {
		t.Errorf("Role = %q, want 琉璃 (default when empty)", result.Role)
	}
}

func TestParseLLMRouteResult_InvalidJSON(t *testing.T) {
	output := `{role: broken json}`
	result, err := parseLLMRouteResult(output, "琉璃")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Role != "琉璃" {
		t.Errorf("Role = %q, want 琉璃 (default on parse error)", result.Role)
	}
	if result.Confidence != "low" {
		t.Errorf("Confidence = %q, want low", result.Confidence)
	}
}

func TestParseLLMRouteResult_MissingConfidence(t *testing.T) {
	output := `{"role":"翡翠","reason":"research task"}`
	result, err := parseLLMRouteResult(output, "琉璃")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Confidence != "medium" {
		t.Errorf("Confidence = %q, want medium (default)", result.Confidence)
	}
}
