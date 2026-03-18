package main

import (
	"context"
	"testing"
)

// --- from provider_test.go ---

// mockProvider is a minimal Provider for registry tests.
type mockProvider struct{ name string }

func (m *mockProvider) Name() string { return m.name }
func (m *mockProvider) Execute(_ context.Context, _ ProviderRequest) (*ProviderResult, error) {
	return &ProviderResult{}, nil
}

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
		Agents: map[string]AgentConfig{
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
		Agents: map[string]AgentConfig{
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
		Agents: map[string]AgentConfig{
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
	p := &mockProvider{name: "test"}
	reg.Register("test", p)

	got, err := reg.Get("test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name() != "test" {
		t.Errorf("expected test, got %s", got.Name())
	}
}

func TestProviderRegistry_GetNotFound(t *testing.T) {
	reg := newProviderRegistry()
	_, err := reg.Get("missing")
	if err == nil {
		t.Fatal("expected error for missing provider")
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
	p, err := reg.Get("claude")
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
	p, err := reg.Get("myopenai")
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
	p, err := reg.Get("claude")
	if err != nil {
		t.Fatalf("expected claude provider: %v", err)
	}
	if p.Name() != "claude" {
		t.Errorf("expected claude, got %s", p.Name())
	}
}
