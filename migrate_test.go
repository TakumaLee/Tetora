package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// getConfigVersion
// ---------------------------------------------------------------------------

func TestGetConfigVersion_Missing(t *testing.T) {
	raw := map[string]json.RawMessage{
		"claudePath": json.RawMessage(`"claude"`),
	}
	if v := getConfigVersion(raw); v != 1 {
		t.Errorf("getConfigVersion() = %d, want 1", v)
	}
}

func TestGetConfigVersion_Present(t *testing.T) {
	raw := map[string]json.RawMessage{
		"configVersion": json.RawMessage(`2`),
	}
	if v := getConfigVersion(raw); v != 2 {
		t.Errorf("getConfigVersion() = %d, want 2", v)
	}
}

func TestGetConfigVersion_Invalid(t *testing.T) {
	raw := map[string]json.RawMessage{
		"configVersion": json.RawMessage(`"notanumber"`),
	}
	if v := getConfigVersion(raw); v != 1 {
		t.Errorf("getConfigVersion() = %d, want 1", v)
	}
}

func TestGetConfigVersion_Zero(t *testing.T) {
	raw := map[string]json.RawMessage{
		"configVersion": json.RawMessage(`0`),
	}
	if v := getConfigVersion(raw); v != 1 {
		t.Errorf("getConfigVersion() = %d, want 1 for zero value", v)
	}
}

// ---------------------------------------------------------------------------
// migrateConfig
// ---------------------------------------------------------------------------

func TestMigrateConfig_DryRun(t *testing.T) {
	// Create a v1 config (no configVersion field).
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	cfg := map[string]any{
		"claudePath":    "claude",
		"maxConcurrent": 3,
		"listenAddr":    "127.0.0.1:8991",
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(configPath, data, 0o644)

	// Dry run should return descriptions but not modify file.
	applied, err := migrateConfig(configPath, true)
	if err != nil {
		t.Fatalf("migrateConfig(dryRun=true) error: %v", err)
	}
	if len(applied) == 0 {
		t.Fatal("expected at least one migration in dry run")
	}

	// Verify file was NOT modified.
	after, _ := os.ReadFile(configPath)
	var raw map[string]json.RawMessage
	json.Unmarshal(after, &raw)
	if _, ok := raw["configVersion"]; ok {
		t.Error("dry run should not modify config file")
	}
}

func TestMigrateConfig_Apply(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	cfg := map[string]any{
		"claudePath":    "claude",
		"maxConcurrent": 3,
		"listenAddr":    "127.0.0.1:8991",
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(configPath, data, 0o644)

	applied, err := migrateConfig(configPath, false)
	if err != nil {
		t.Fatalf("migrateConfig() error: %v", err)
	}
	if len(applied) == 0 {
		t.Fatal("expected at least one migration applied")
	}

	// Verify configVersion was set.
	after, _ := os.ReadFile(configPath)
	var raw map[string]json.RawMessage
	json.Unmarshal(after, &raw)

	var ver int
	if err := json.Unmarshal(raw["configVersion"], &ver); err != nil {
		t.Fatalf("parse configVersion: %v", err)
	}
	if ver != currentConfigVersion {
		t.Errorf("configVersion = %d, want %d", ver, currentConfigVersion)
	}

	// Verify smartDispatch was added.
	if _, ok := raw["smartDispatch"]; !ok {
		t.Error("expected smartDispatch to be added by migration")
	}

	// Verify knowledgeDir was added.
	if _, ok := raw["knowledgeDir"]; !ok {
		t.Error("expected knowledgeDir to be added by migration")
	}

	// Verify backup was created.
	entries, _ := os.ReadDir(dir)
	hasBackup := false
	for _, e := range entries {
		if len(e.Name()) > 15 && e.Name()[:12] == "config.json." {
			hasBackup = true
		}
	}
	if !hasBackup {
		t.Error("expected backup file to be created")
	}
}

func TestMigrateConfig_AlreadyUpToDate(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	cfg := map[string]any{
		"configVersion": currentConfigVersion,
		"claudePath":    "claude",
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(configPath, data, 0o644)

	applied, err := migrateConfig(configPath, false)
	if err != nil {
		t.Fatalf("migrateConfig() error: %v", err)
	}
	if applied != nil {
		t.Errorf("expected nil applied for up-to-date config, got %v", applied)
	}
}

func TestMigrateConfig_PreservesExistingSmartDispatch(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	cfg := map[string]any{
		"claudePath": "claude",
		"smartDispatch": map[string]any{
			"enabled":     true,
			"coordinator": "custom",
		},
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(configPath, data, 0o644)

	migrateConfig(configPath, false)

	after, _ := os.ReadFile(configPath)
	var raw map[string]json.RawMessage
	json.Unmarshal(after, &raw)

	// smartDispatch should still have the existing values.
	var sd map[string]any
	json.Unmarshal(raw["smartDispatch"], &sd)
	if sd["coordinator"] != "custom" {
		t.Errorf("smartDispatch.coordinator = %v, want 'custom'", sd["coordinator"])
	}
}

func TestMigrateConfig_NonExistentFile(t *testing.T) {
	_, err := migrateConfig("/nonexistent/path/config.json", false)
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestMigrateConfig_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	os.WriteFile(configPath, []byte("not json"), 0o644)

	_, err := migrateConfig(configPath, false)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}
