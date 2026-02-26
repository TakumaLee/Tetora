package main

import (
	"testing"
)

func TestQuickAction_List(t *testing.T) {
	cfg := &Config{
		QuickActions: []QuickAction{
			{Name: "deploy", Label: "Deploy to production"},
			{Name: "review", Label: "Code review"},
		},
	}
	engine := newQuickActionEngine(cfg)
	actions := engine.List()
	if len(actions) != 2 {
		t.Errorf("expected 2 actions, got %d", len(actions))
	}
}

func TestQuickAction_Get(t *testing.T) {
	cfg := &Config{
		QuickActions: []QuickAction{
			{Name: "deploy", Label: "Deploy to production"},
			{Name: "review", Label: "Code review"},
		},
	}
	engine := newQuickActionEngine(cfg)

	action, err := engine.Get("deploy")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if action.Name != "deploy" {
		t.Errorf("expected name 'deploy', got %s", action.Name)
	}
}

func TestQuickAction_Get_NotFound(t *testing.T) {
	cfg := &Config{
		QuickActions: []QuickAction{
			{Name: "deploy", Label: "Deploy to production"},
		},
	}
	engine := newQuickActionEngine(cfg)

	_, err := engine.Get("unknown")
	if err == nil {
		t.Error("expected error for missing action, got nil")
	}
}

func TestQuickAction_BuildPrompt_Static(t *testing.T) {
	cfg := &Config{
		QuickActions: []QuickAction{
			{Name: "hello", Prompt: "Say hello", Agent: "琉璃"},
		},
		SmartDispatch: SmartDispatchConfig{DefaultAgent: "琉璃"},
	}
	engine := newQuickActionEngine(cfg)

	prompt, role, err := engine.BuildPrompt("hello", nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if prompt != "Say hello" {
		t.Errorf("expected prompt 'Say hello', got %s", prompt)
	}
	if role != "琉璃" {
		t.Errorf("expected role '琉璃', got %s", role)
	}
}

func TestQuickAction_BuildPrompt_Template(t *testing.T) {
	cfg := &Config{
		QuickActions: []QuickAction{
			{
				Name:           "greet",
				PromptTemplate: "Hello {{.name}}!",
				Agent:           "琉璃",
			},
		},
		SmartDispatch: SmartDispatchConfig{DefaultAgent: "琉璃"},
	}
	engine := newQuickActionEngine(cfg)

	params := map[string]any{"name": "Alice"}
	prompt, role, err := engine.BuildPrompt("greet", params)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if prompt != "Hello Alice!" {
		t.Errorf("expected prompt 'Hello Alice!', got %s", prompt)
	}
	if role != "琉璃" {
		t.Errorf("expected role '琉璃', got %s", role)
	}
}

func TestQuickAction_BuildPrompt_Defaults(t *testing.T) {
	cfg := &Config{
		QuickActions: []QuickAction{
			{
				Name:           "greet",
				PromptTemplate: "Hello {{.name}}, you are {{.age}} years old!",
				Params: map[string]QuickActionParam{
					"name": {Type: "string", Default: "Guest"},
					"age":  {Type: "number", Default: 18},
				},
				Agent: "琉璃",
			},
		},
		SmartDispatch: SmartDispatchConfig{DefaultAgent: "琉璃"},
	}
	engine := newQuickActionEngine(cfg)

	// Only override name, age should use default.
	params := map[string]any{"name": "Bob"}
	prompt, _, err := engine.BuildPrompt("greet", params)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if prompt != "Hello Bob, you are 18 years old!" {
		t.Errorf("expected 'Hello Bob, you are 18 years old!', got %s", prompt)
	}

	// No params, should use all defaults.
	prompt2, _, err := engine.BuildPrompt("greet", nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if prompt2 != "Hello Guest, you are 18 years old!" {
		t.Errorf("expected 'Hello Guest, you are 18 years old!', got %s", prompt2)
	}
}

func TestQuickAction_Search(t *testing.T) {
	cfg := &Config{
		QuickActions: []QuickAction{
			{Name: "deploy", Label: "Deploy to production", Shortcut: "d"},
			{Name: "review", Label: "Code review", Shortcut: "r"},
			{Name: "test", Label: "Run tests", Shortcut: "t"},
		},
	}
	engine := newQuickActionEngine(cfg)

	// Search by name.
	results := engine.Search("deploy")
	if len(results) != 1 || results[0].Name != "deploy" {
		t.Errorf("expected 1 result 'deploy', got %d results", len(results))
	}

	// Search by label substring.
	results = engine.Search("code")
	if len(results) != 1 || results[0].Name != "review" {
		t.Errorf("expected 1 result 'review', got %d results", len(results))
	}

	// Search by label substring (case insensitive).
	results = engine.Search("PRODUCTION")
	if len(results) != 1 || results[0].Name != "deploy" {
		t.Errorf("expected 1 result 'deploy', got %d results", len(results))
	}
}

func TestQuickAction_Search_NoMatch(t *testing.T) {
	cfg := &Config{
		QuickActions: []QuickAction{
			{Name: "deploy", Label: "Deploy to production"},
		},
	}
	engine := newQuickActionEngine(cfg)

	results := engine.Search("unknown")
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestQuickAction_Search_Shortcut(t *testing.T) {
	cfg := &Config{
		QuickActions: []QuickAction{
			{Name: "build", Label: "Build project", Shortcut: "b"},
			{Name: "test", Label: "Run tests", Shortcut: "t"},
		},
	}
	engine := newQuickActionEngine(cfg)

	results := engine.Search("b")
	if len(results) != 1 || results[0].Name != "build" {
		t.Errorf("expected 1 result 'build', got %d results", len(results))
	}
}

func TestQuickAction_EmptyConfig(t *testing.T) {
	cfg := &Config{
		QuickActions: []QuickAction{},
	}
	engine := newQuickActionEngine(cfg)

	actions := engine.List()
	if len(actions) != 0 {
		t.Errorf("expected 0 actions, got %d", len(actions))
	}

	_, err := engine.Get("any")
	if err == nil {
		t.Error("expected error for missing action, got nil")
	}
}
