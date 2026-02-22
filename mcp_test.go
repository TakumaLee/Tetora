package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestListMCPConfigsEmpty(t *testing.T) {
	cfg := &Config{}
	configs := listMCPConfigs(cfg)
	if len(configs) != 0 {
		t.Errorf("expected 0 configs, got %d", len(configs))
	}
}

func TestListMCPConfigs(t *testing.T) {
	cfg := &Config{
		MCPConfigs: map[string]json.RawMessage{
			"playwright": json.RawMessage(`{"mcpServers":{"playwright":{"command":"npx","args":["-y","@playwright/mcp"]}}}`),
			"filesystem": json.RawMessage(`{"mcpServers":{"fs":{"command":"node","args":["server.js"]}}}`),
		},
	}
	configs := listMCPConfigs(cfg)
	if len(configs) != 2 {
		t.Fatalf("expected 2 configs, got %d", len(configs))
	}
	// Should be sorted.
	if configs[0].Name != "filesystem" {
		t.Errorf("first config name = %q, want filesystem", configs[0].Name)
	}
}

func TestGetMCPConfigNotFound(t *testing.T) {
	cfg := &Config{MCPConfigs: make(map[string]json.RawMessage)}
	_, err := getMCPConfig(cfg, "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent config")
	}
}

func TestSetAndGetMCPConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	os.WriteFile(configPath, []byte(`{}`), 0o644)

	cfg := &Config{
		baseDir:    dir,
		MCPConfigs: make(map[string]json.RawMessage),
		mcpPaths:   make(map[string]string),
	}

	raw := json.RawMessage(`{"mcpServers":{"test":{"command":"echo","args":["hello"]}}}`)
	if err := setMCPConfig(cfg, configPath, "test-server", raw); err != nil {
		t.Fatalf("setMCPConfig: %v", err)
	}

	got, err := getMCPConfig(cfg, "test-server")
	if err != nil {
		t.Fatalf("getMCPConfig: %v", err)
	}

	var parsed map[string]any
	json.Unmarshal(got, &parsed)
	if parsed["mcpServers"] == nil {
		t.Error("expected mcpServers in config")
	}
}

func TestDeleteMCPConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	os.WriteFile(configPath, []byte(`{"mcpConfigs":{"to-delete":{}}}`), 0o644)
	os.MkdirAll(filepath.Join(dir, "mcp"), 0o755)
	os.WriteFile(filepath.Join(dir, "mcp", "to-delete.json"), []byte(`{}`), 0o644)

	cfg := &Config{
		baseDir:    dir,
		MCPConfigs: map[string]json.RawMessage{"to-delete": json.RawMessage(`{}`)},
		mcpPaths:   map[string]string{"to-delete": filepath.Join(dir, "mcp", "to-delete.json")},
	}

	if err := deleteMCPConfig(cfg, configPath, "to-delete"); err != nil {
		t.Fatalf("deleteMCPConfig: %v", err)
	}

	if _, err := getMCPConfig(cfg, "to-delete"); err == nil {
		t.Error("expected error after delete")
	}

	// File should be removed.
	if _, err := os.Stat(filepath.Join(dir, "mcp", "to-delete.json")); !os.IsNotExist(err) {
		t.Error("expected mcp file to be deleted")
	}
}

func TestSetMCPConfigInvalidName(t *testing.T) {
	cfg := &Config{MCPConfigs: make(map[string]json.RawMessage)}
	raw := json.RawMessage(`{}`)

	tests := []string{"bad/name", "bad name", ""}
	for _, name := range tests {
		if err := setMCPConfig(cfg, "/tmp/test.json", name, raw); err == nil {
			t.Errorf("expected error for name %q", name)
		}
	}
}

func TestSetMCPConfigInvalidJSON(t *testing.T) {
	cfg := &Config{MCPConfigs: make(map[string]json.RawMessage)}
	if err := setMCPConfig(cfg, "/tmp/test.json", "test", json.RawMessage(`{invalid`)); err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestExtractMCPSummary(t *testing.T) {
	tests := []struct {
		name    string
		raw     json.RawMessage
		wantCmd string
		wantArgs string
	}{
		{
			"mcpServers wrapper",
			json.RawMessage(`{"mcpServers":{"test":{"command":"npx","args":["-y","@playwright/mcp"]}}}`),
			"npx", "-y @playwright/mcp",
		},
		{
			"flat format",
			json.RawMessage(`{"command":"node","args":["server.js"]}`),
			"node", "server.js",
		},
		{
			"empty",
			json.RawMessage(`{}`),
			"", "",
		},
	}

	for _, tc := range tests {
		cmd, args := extractMCPSummary(tc.raw)
		if cmd != tc.wantCmd {
			t.Errorf("%s: command = %q, want %q", tc.name, cmd, tc.wantCmd)
		}
		if args != tc.wantArgs {
			t.Errorf("%s: args = %q, want %q", tc.name, args, tc.wantArgs)
		}
	}
}

func TestUpdateConfigMCPs(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	os.WriteFile(configPath, []byte(`{"claudePath":"/usr/bin/claude","mcpConfigs":{}}`), 0o644)

	// Add.
	raw := json.RawMessage(`{"mcpServers":{"test":{"command":"echo"}}}`)
	if err := updateConfigMCPs(configPath, "new-server", raw); err != nil {
		t.Fatalf("updateConfigMCPs add: %v", err)
	}

	// Verify file contents.
	data, _ := os.ReadFile(configPath)
	var parsed map[string]json.RawMessage
	json.Unmarshal(data, &parsed)
	if _, ok := parsed["claudePath"]; !ok {
		t.Error("claudePath should be preserved")
	}

	// Delete.
	if err := updateConfigMCPs(configPath, "new-server", nil); err != nil {
		t.Fatalf("updateConfigMCPs delete: %v", err)
	}
	data, _ = os.ReadFile(configPath)
	json.Unmarshal(data, &parsed)
	var mcps map[string]json.RawMessage
	json.Unmarshal(parsed["mcpConfigs"], &mcps)
	if len(mcps) != 0 {
		t.Errorf("expected empty mcpConfigs after delete, got %d", len(mcps))
	}
}

func TestTestMCPConfigValidCommand(t *testing.T) {
	// echo should exist on all systems.
	raw := json.RawMessage(`{"mcpServers":{"test":{"command":"echo","args":["hello"]}}}`)
	ok, _ := testMCPConfig(raw)
	if !ok {
		t.Error("expected ok=true for echo command")
	}
}

func TestTestMCPConfigInvalidCommand(t *testing.T) {
	raw := json.RawMessage(`{"mcpServers":{"test":{"command":"nonexistent-cmd-xyz-999","args":[]}}}`)
	ok, _ := testMCPConfig(raw)
	if ok {
		t.Error("expected ok=false for nonexistent command")
	}
}

func TestTestMCPConfigNoParse(t *testing.T) {
	raw := json.RawMessage(`{}`)
	ok, output := testMCPConfig(raw)
	if ok {
		t.Error("expected ok=false for empty config")
	}
	if output == "" {
		t.Error("expected non-empty output")
	}
}
