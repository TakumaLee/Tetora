package main

import (
	"path/filepath"
	"testing"

	"tetora/internal/config"
	"tetora/internal/provider"
)

// TestResolveProviderName_ActiveProviderOverride tests that active provider
// override takes highest priority.
func TestResolveProviderName_ActiveProviderOverride(t *testing.T) {
	tmpDir := t.TempDir()
	activeProviderPath := filepath.Join(tmpDir, "runtime", "active-provider.json")

	// Create a minimal config.
	cfg := &config.Config{
		BaseDir:         tmpDir,
		DefaultProvider: "claude",
		Providers: map[string]config.ProviderConfig{
			"qwen":   {Type: "openai-compatible", Model: "qwen3.6-plus"},
			"google": {Type: "openai-compatible", Model: "gemini-2.5-pro"},
			"claude": {Type: "anthropic", Model: "claude-sonnet-4"},
		},
		Agents: map[string]config.AgentConfig{
			"coder": {Provider: "qwen", Model: "auto"},
		},
	}

	// Initialize active provider store.
	store := config.NewActiveProviderStore(activeProviderPath)
	cfg.ActiveProviderStore = store

	// Test 1: No active override - should use agent-level provider.
	result := resolveProviderName(cfg, Task{Agent: "coder"}, "coder")
	if result != "qwen" {
		t.Errorf("Test 1: expected 'qwen', got '%s'", result)
	}

	// Test 2: Set active provider - should override everything.
	store.Set("google", "auto", "test")
	result = resolveProviderName(cfg, Task{Agent: "coder"}, "coder")
	if result != "google" {
		t.Errorf("Test 2: expected 'google' (active override), got '%s'", result)
	}

	// Test 3: Task-level override should still work when active provider is set.
	result = resolveProviderName(cfg, Task{Agent: "coder", Provider: "claude"}, "coder")
	if result != "claude" {
		t.Errorf("Test 3: expected 'claude' (task-level), got '%s'", result)
	}
}

// TestResolveProviderName_AutoMode tests that "auto" provider falls through
// to global default.
func TestResolveProviderName_AutoMode(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &config.Config{
		BaseDir:         tmpDir,
		DefaultProvider: "qwen",
		Providers: map[string]config.ProviderConfig{
			"qwen":   {Type: "openai-compatible", Model: "qwen3.6-plus"},
			"google": {Type: "openai-compatible", Model: "gemini-2.5-pro"},
		},
		Agents: map[string]config.AgentConfig{
			"writer": {Provider: "auto", Model: "auto"},
		},
	}

	// Agent with "auto" should fall through to global default.
	result := resolveProviderName(cfg, Task{Agent: "writer"}, "writer")
	if result != "qwen" {
		t.Errorf("expected 'qwen' (global default), got '%s'", result)
	}
}

// TestBuildProviderRequest_ModelResolution tests model resolution logic.
func TestBuildProviderRequest_ModelResolution(t *testing.T) {
	tmpDir := t.TempDir()
	activeProviderPath := filepath.Join(tmpDir, "runtime", "active-provider.json")

	cfg := &config.Config{
		BaseDir:      tmpDir,
		DefaultModel: "auto",
		Providers: map[string]config.ProviderConfig{
			"qwen":   {Type: "openai-compatible", Model: "qwen3.6-plus"},
			"google": {Type: "openai-compatible", Model: "gemini-2.5-pro"},
		},
	}

	store := config.NewActiveProviderStore(activeProviderPath)
	cfg.ActiveProviderStore = store

	// Test 1: Active provider with explicit model.
	store.Set("qwen", "qwen-max", "test")
	req := buildProviderRequest(cfg, Task{}, "", "qwen", nil)
	if req.Model != "qwen-max" {
		t.Errorf("Test 1: expected model 'qwen-max', got '%s'", req.Model)
	}

	// Test 2: Active provider with "auto" model - should use provider default.
	store.Set("qwen", "auto", "test")
	req = buildProviderRequest(cfg, Task{Model: "auto"}, "", "qwen", nil)
	if req.Model != "qwen3.6-plus" {
		t.Errorf("Test 2: expected model 'qwen3.6-plus', got '%s'", req.Model)
	}

	// Test 3: Task model override takes precedence.
	req = buildProviderRequest(cfg, Task{Model: "custom-model"}, "", "qwen", nil)
	if req.Model != "custom-model" {
		t.Errorf("Test 3: expected model 'custom-model', got '%s'", req.Model)
	}
}

// TestBuildProviderCandidates_FallbacksWithActiveProvider tests that fallback
// providers still work when active provider is set.
func TestBuildProviderCandidates_FallbacksWithActiveProvider(t *testing.T) {
	tmpDir := t.TempDir()
	activeProviderPath := filepath.Join(tmpDir, "runtime", "active-provider.json")

	cfg := &config.Config{
		BaseDir:            tmpDir,
		DefaultProvider:    "qwen",
		FallbackProviders:  []string{"google", "claude"},
		Providers: map[string]config.ProviderConfig{
			"qwen":   {Type: "openai-compatible"},
			"google": {Type: "openai-compatible"},
			"claude": {Type: "anthropic"},
		},
	}

	store := config.NewActiveProviderStore(activeProviderPath)
	cfg.ActiveProviderStore = store

	// Without active provider.
	candidates := buildProviderCandidates(cfg, Task{Agent: "coder"}, "coder")
	if len(candidates) != 3 {
		t.Errorf("expected 3 candidates, got %d: %v", len(candidates), candidates)
	}
	if candidates[0] != "qwen" {
		t.Errorf("expected primary candidate 'qwen', got '%s'", candidates[0])
	}

	// With active provider.
	store.Set("google", "auto", "test")
	candidates = buildProviderCandidates(cfg, Task{Agent: "coder"}, "coder")
	// Should have: google (active), qwen (default), claude (fallback)
	// But since active provider is set, we skip agent fallbacks
	if len(candidates) < 2 {
		t.Errorf("expected at least 2 candidates with active provider, got %d: %v", len(candidates), candidates)
	}
	if candidates[0] != "google" {
		t.Errorf("expected primary candidate 'google' (active), got '%s'", candidates[0])
	}
}

// TestProviderProfiles_Availability tests that provider profiles are accessible.
func TestProviderProfiles_Availability(t *testing.T) {
	profiles := map[string]string{
		"qwen":        "qwen3.6-plus",
		"google":      "gemini-2.5-pro",
		"claude-code": "claude-sonnet-4-20250514",
		"groq":        "llama-3.3-70b-versatile",
	}

	for providerName, expectedModel := range profiles {
		profile := provider.GetProviderProfile(providerName)
		if profile == nil {
			t.Errorf("profile for '%s' not found", providerName)
			continue
		}
		if profile.DefaultModel != expectedModel {
			t.Errorf("profile '%s': expected model '%s', got '%s'",
				providerName, expectedModel, profile.DefaultModel)
		}
		if profile.MaxTokens <= 0 {
			t.Errorf("profile '%s': expected positive MaxTokens", providerName)
		}
		if len(profile.Strengths) == 0 {
			t.Errorf("profile '%s': expected non-empty Strengths", providerName)
		}
	}
}

// TestProviderProfiles_ApplyToConfig tests applying profiles to config.
func TestProviderProfiles_ApplyToConfig(t *testing.T) {
	profile := provider.GetProviderProfile("qwen")
	if profile == nil {
		t.Fatal("qwen profile not found")
	}

	cfg := &config.ProviderConfig{
		Type:    "custom",
		BaseURL: "http://custom.com",
		Model:   "custom-model",
	}

	// Apply profile (should not override existing non-empty values).
	provider.ApplyProfileToConfig(profile, cfg)
	if cfg.Model != "custom-model" {
		t.Errorf("expected Model to remain 'custom-model', got '%s'", cfg.Model)
	}

	// Now test with empty config.
	cfg2 := &config.ProviderConfig{}
	provider.ApplyProfileToConfig(profile, cfg2)
	if cfg2.Type != "openai-compatible" {
		t.Errorf("expected Type 'openai-compatible', got '%s'", cfg2.Type)
	}
	if cfg2.Model != "qwen3.6-plus" {
		t.Errorf("expected Model 'qwen3.6-plus', got '%s'", cfg2.Model)
	}
	if cfg2.MaxTokens <= 0 {
		t.Errorf("expected positive MaxTokens, got %d", cfg2.MaxTokens)
	}
}
