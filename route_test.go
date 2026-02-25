package main

import (
	"context"
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

// --- checkBindings ---

func TestCheckBindings_UserIDMatch(t *testing.T) {
	cfg := &Config{
		SmartDispatch: SmartDispatchConfig{
			Bindings: []RoutingBinding{
				{Channel: "telegram", UserID: "12345", Role: "黒曜"},
				{Channel: "slack", UserID: "U999", Role: "翡翠"},
			},
		},
	}

	tests := []struct {
		req      RouteRequest
		wantRole string
		wantNil  bool
	}{
		{RouteRequest{Source: "telegram", UserID: "12345"}, "黒曜", false},
		{RouteRequest{Source: "slack", UserID: "U999"}, "翡翠", false},
		{RouteRequest{Source: "telegram", UserID: "99999"}, "", true}, // no match
		{RouteRequest{Source: "discord", UserID: "12345"}, "", true},  // wrong channel
	}

	for _, tt := range tests {
		result := checkBindings(cfg, tt.req)
		if tt.wantNil {
			if result != nil {
				t.Errorf("checkBindings(%+v) = %+v, want nil", tt.req, result)
			}
			continue
		}
		if result == nil {
			t.Errorf("checkBindings(%+v) = nil, want role=%q", tt.req, tt.wantRole)
			continue
		}
		if result.Role != tt.wantRole {
			t.Errorf("checkBindings(%+v).Role = %q, want %q", tt.req, result.Role, tt.wantRole)
		}
		if result.Method != "binding" {
			t.Errorf("checkBindings(%+v).Method = %q, want binding", tt.req, result.Method)
		}
		if result.Confidence != "high" {
			t.Errorf("checkBindings(%+v).Confidence = %q, want high", tt.req, result.Confidence)
		}
	}
}

func TestCheckBindings_ChannelIDMatch(t *testing.T) {
	cfg := &Config{
		SmartDispatch: SmartDispatchConfig{
			Bindings: []RoutingBinding{
				{Channel: "slack", ChannelID: "C123", Role: "翡翠"},
				{Channel: "telegram", ChannelID: "-1001234567890", Role: "琥珀"},
			},
		},
	}

	tests := []struct {
		req      RouteRequest
		wantRole string
		wantNil  bool
	}{
		{RouteRequest{Source: "slack", ChannelID: "C123"}, "翡翠", false},
		{RouteRequest{Source: "telegram", ChannelID: "-1001234567890"}, "琥珀", false},
		{RouteRequest{Source: "slack", ChannelID: "C999"}, "", true},
	}

	for _, tt := range tests {
		result := checkBindings(cfg, tt.req)
		if tt.wantNil {
			if result != nil {
				t.Errorf("checkBindings(%+v) = %+v, want nil", tt.req, result)
			}
			continue
		}
		if result == nil {
			t.Errorf("checkBindings(%+v) = nil, want role=%q", tt.req, tt.wantRole)
			continue
		}
		if result.Role != tt.wantRole {
			t.Errorf("checkBindings(%+v).Role = %q, want %q", tt.req, result.Role, tt.wantRole)
		}
	}
}

func TestCheckBindings_GuildIDMatch(t *testing.T) {
	cfg := &Config{
		SmartDispatch: SmartDispatchConfig{
			Bindings: []RoutingBinding{
				{Channel: "discord", GuildID: "G456", Role: "琥珀"},
			},
		},
	}

	result := checkBindings(cfg, RouteRequest{Source: "discord", GuildID: "G456"})
	if result == nil {
		t.Fatal("checkBindings returned nil, want role=琥珀")
	}
	if result.Role != "琥珀" {
		t.Errorf("Role = %q, want 琥珀", result.Role)
	}
}

func TestCheckBindings_MultipleIDFields(t *testing.T) {
	cfg := &Config{
		SmartDispatch: SmartDispatchConfig{
			Bindings: []RoutingBinding{
				{Channel: "telegram", UserID: "12345", ChannelID: "-100999", Role: "黒曜"},
			},
		},
	}

	// Should match if ANY of the ID fields match.
	result1 := checkBindings(cfg, RouteRequest{Source: "telegram", UserID: "12345"})
	if result1 == nil || result1.Role != "黒曜" {
		t.Errorf("checkBindings with UserID match failed")
	}

	result2 := checkBindings(cfg, RouteRequest{Source: "telegram", ChannelID: "-100999"})
	if result2 == nil || result2.Role != "黒曜" {
		t.Errorf("checkBindings with ChannelID match failed")
	}

	result3 := checkBindings(cfg, RouteRequest{Source: "telegram", UserID: "12345", ChannelID: "-100999"})
	if result3 == nil || result3.Role != "黒曜" {
		t.Errorf("checkBindings with both IDs match failed")
	}
}

func TestCheckBindings_NoMatch(t *testing.T) {
	cfg := &Config{
		SmartDispatch: SmartDispatchConfig{
			Bindings: []RoutingBinding{
				{Channel: "telegram", UserID: "12345", Role: "黒曜"},
			},
		},
	}

	result := checkBindings(cfg, RouteRequest{Source: "slack", UserID: "12345"})
	if result != nil {
		t.Errorf("checkBindings returned %+v, want nil (channel mismatch)", result)
	}
}

// --- routeTask with bindings ---

func TestRouteTask_BindingPriority(t *testing.T) {
	cfg := &Config{
		SmartDispatch: SmartDispatchConfig{
			DefaultRole: "琉璃",
			Coordinator: "琉璃",
			Bindings: []RoutingBinding{
				{Channel: "telegram", UserID: "12345", Role: "黒曜"},
			},
			Rules: []RoutingRule{
				{Role: "翡翠", Keywords: []string{"research"}}, // this keyword will be in prompt
			},
		},
		Roles: map[string]RoleConfig{
			"琉璃": {Model: "sonnet"},
			"黒曜": {Model: "opus"},
			"翡翠": {Model: "sonnet"},
		},
	}

	// Even though prompt contains "research" keyword, binding should take priority.
	ctx := context.Background()
	req := RouteRequest{
		Prompt: "do some research please",
		Source: "telegram",
		UserID: "12345",
	}

	result := routeTask(ctx, cfg, req)
	if result.Role != "黒曜" {
		t.Errorf("Role = %q, want 黒曜 (binding should override keyword)", result.Role)
	}
	if result.Method != "binding" {
		t.Errorf("Method = %q, want binding", result.Method)
	}
}

func TestRouteTask_FallbackCoordinator(t *testing.T) {
	cfg := &Config{
		SmartDispatch: SmartDispatchConfig{
			DefaultRole: "琉璃",
			Coordinator: "琉璃",
			Fallback:    "coordinator", // bypass LLM routing
		},
		Roles: map[string]RoleConfig{
			"琉璃": {Model: "sonnet"},
		},
	}

	ctx := context.Background()
	req := RouteRequest{
		Prompt: "random task with no keywords or bindings",
		Source: "http",
	}

	result := routeTask(ctx, cfg, req)
	if result.Role != "琉璃" {
		t.Errorf("Role = %q, want 琉璃 (fallback coordinator)", result.Role)
	}
	if result.Method != "coordinator" {
		t.Errorf("Method = %q, want coordinator", result.Method)
	}
	if result.Confidence != "high" {
		t.Errorf("Confidence = %q, want high (coordinator fallback)", result.Confidence)
	}
}

func TestRouteTask_FallbackSmartWithKeyword(t *testing.T) {
	cfg := &Config{
		SmartDispatch: SmartDispatchConfig{
			DefaultRole: "琉璃",
			Coordinator: "琉璃",
			Fallback:    "smart",
			Rules: []RoutingRule{
				{Role: "翡翠", Keywords: []string{"research"}},
			},
		},
		Roles: map[string]RoleConfig{
			"琉璃": {Model: "sonnet"},
			"翡翠": {Model: "sonnet"},
		},
	}

	ctx := context.Background()
	req := RouteRequest{
		Prompt: "do research please",
		Source: "http",
	}

	// With fallback="smart", keyword matching should still work.
	result := routeTask(ctx, cfg, req)
	if result.Role != "翡翠" {
		t.Errorf("Role = %q, want 翡翠 (keyword should work with smart fallback)", result.Role)
	}
	if result.Method != "keyword" {
		t.Errorf("Method = %q, want keyword", result.Method)
	}
}

func TestRouteTask_NoBindingsUsesKeywords(t *testing.T) {
	cfg := &Config{
		SmartDispatch: SmartDispatchConfig{
			DefaultRole: "琉璃",
			Rules: []RoutingRule{
				{Role: "黒曜", Keywords: []string{"security"}},
			},
		},
		Roles: map[string]RoleConfig{
			"琉璃": {Model: "sonnet"},
			"黒曜": {Model: "opus"},
		},
	}

	ctx := context.Background()
	req := RouteRequest{
		Prompt: "run a security check",
		Source: "telegram",
		// No bindings defined, so should fall through to keyword matching.
	}

	result := routeTask(ctx, cfg, req)
	if result.Role != "黒曜" {
		t.Errorf("Role = %q, want 黒曜 (keyword match)", result.Role)
	}
	if result.Method != "keyword" {
		t.Errorf("Method = %q, want keyword", result.Method)
	}
}
