package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveInferenceMode(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	// Initial config with two agents.
	initial := map[string]any{
		"defaultModel": "claude-sonnet-4-6",
		"agents": map[string]any{
			"ruri": map[string]any{
				"model":    "claude-sonnet-4-6",
				"provider": "claude-code",
				"soulFile": "SOUL.md",
			},
			"kohaku": map[string]any{
				"model":    "claude-sonnet-4-6",
				"provider": "claude-code",
				"soulFile": "SOUL.md",
			},
		},
	}
	data, _ := json.MarshalIndent(initial, "", "  ")
	os.WriteFile(configPath, append(data, '\n'), 0o600)

	// Update only ruri, set mode to "local".
	updated := map[string]AgentConfig{
		"ruri": {
			Model:    "gemma4:e4b",
			Provider: "ollama",
			SoulFile: "SOUL.md",
		},
	}

	if err := SaveInferenceMode(configPath, "local", updated); err != nil {
		t.Fatalf("SaveInferenceMode: %v", err)
	}

	// Read back and verify.
	result, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	var got map[string]json.RawMessage
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatalf("parse config: %v", err)
	}

	// Check inferenceMode is set.
	var mode string
	json.Unmarshal(got["inferenceMode"], &mode)
	if mode != "local" {
		t.Errorf("inferenceMode: got %q, want %q", mode, "local")
	}

	// Check ruri was updated.
	var agents map[string]json.RawMessage
	json.Unmarshal(got["agents"], &agents)

	var ruri AgentConfig
	json.Unmarshal(agents["ruri"], &ruri)
	if ruri.Model != "gemma4:e4b" {
		t.Errorf("ruri.Model: got %q, want %q", ruri.Model, "gemma4:e4b")
	}
	if ruri.Provider != "ollama" {
		t.Errorf("ruri.Provider: got %q, want %q", ruri.Provider, "ollama")
	}

	// Check kohaku was NOT modified.
	var kohaku AgentConfig
	json.Unmarshal(agents["kohaku"], &kohaku)
	if kohaku.Model != "claude-sonnet-4-6" {
		t.Errorf("kohaku.Model should be unchanged, got %q", kohaku.Model)
	}
	if kohaku.Provider != "claude-code" {
		t.Errorf("kohaku.Provider should be unchanged, got %q", kohaku.Provider)
	}

	// Check defaultModel preserved.
	var defaultModel string
	json.Unmarshal(got["defaultModel"], &defaultModel)
	if defaultModel != "claude-sonnet-4-6" {
		t.Errorf("defaultModel should be preserved, got %q", defaultModel)
	}
}

func TestSaveInferenceMode_MissingFile(t *testing.T) {
	err := SaveInferenceMode("/nonexistent/config.json", "local", nil)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}
