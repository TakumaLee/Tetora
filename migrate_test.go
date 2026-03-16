package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"tetora/internal/migrate"
)

// ---------------------------------------------------------------------------
// GetConfigVersion
// ---------------------------------------------------------------------------

func TestGetConfigVersion_Missing(t *testing.T) {
	raw := map[string]json.RawMessage{
		"claudePath": json.RawMessage(`"claude"`),
	}
	if v := migrate.GetConfigVersion(raw); v != 1 {
		t.Errorf("GetConfigVersion() = %d, want 1", v)
	}
}

func TestGetConfigVersion_Present(t *testing.T) {
	raw := map[string]json.RawMessage{
		"configVersion": json.RawMessage(`2`),
	}
	if v := migrate.GetConfigVersion(raw); v != 2 {
		t.Errorf("GetConfigVersion() = %d, want 2", v)
	}
}

func TestGetConfigVersion_Invalid(t *testing.T) {
	raw := map[string]json.RawMessage{
		"configVersion": json.RawMessage(`"notanumber"`),
	}
	if v := migrate.GetConfigVersion(raw); v != 1 {
		t.Errorf("GetConfigVersion() = %d, want 1", v)
	}
}

func TestGetConfigVersion_Zero(t *testing.T) {
	raw := map[string]json.RawMessage{
		"configVersion": json.RawMessage(`0`),
	}
	if v := migrate.GetConfigVersion(raw); v != 1 {
		t.Errorf("GetConfigVersion() = %d, want 1 for zero value", v)
	}
}

// ---------------------------------------------------------------------------
// MigrateConfig
// ---------------------------------------------------------------------------

func TestMigrateConfig_DryRun(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	cfg := map[string]any{
		"claudePath":    "claude",
		"maxConcurrent": 3,
		"listenAddr":    "127.0.0.1:8991",
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(configPath, data, 0o644)

	applied, err := migrate.MigrateConfig(configPath, true)
	if err != nil {
		t.Fatalf("MigrateConfig(dryRun=true) error: %v", err)
	}
	if len(applied) == 0 {
		t.Fatal("expected at least one migration in dry run")
	}

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

	applied, err := migrate.MigrateConfig(configPath, false)
	if err != nil {
		t.Fatalf("MigrateConfig() error: %v", err)
	}
	if len(applied) == 0 {
		t.Fatal("expected at least one migration applied")
	}

	after, _ := os.ReadFile(configPath)
	var raw map[string]json.RawMessage
	json.Unmarshal(after, &raw)

	var ver int
	if err := json.Unmarshal(raw["configVersion"], &ver); err != nil {
		t.Fatalf("parse configVersion: %v", err)
	}
	if ver != migrate.CurrentConfigVersion {
		t.Errorf("configVersion = %d, want %d", ver, migrate.CurrentConfigVersion)
	}

	if _, ok := raw["smartDispatch"]; !ok {
		t.Error("expected smartDispatch to be added by migration")
	}
	if _, ok := raw["knowledgeDir"]; !ok {
		t.Error("expected knowledgeDir to be added by migration")
	}

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
		"configVersion": migrate.CurrentConfigVersion,
		"claudePath":    "claude",
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(configPath, data, 0o644)

	applied, err := migrate.MigrateConfig(configPath, false)
	if err != nil {
		t.Fatalf("MigrateConfig() error: %v", err)
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

	migrate.MigrateConfig(configPath, false)

	after, _ := os.ReadFile(configPath)
	var raw map[string]json.RawMessage
	json.Unmarshal(after, &raw)

	var sd map[string]any
	json.Unmarshal(raw["smartDispatch"], &sd)
	if sd["coordinator"] != "custom" {
		t.Errorf("smartDispatch.coordinator = %v, want 'custom'", sd["coordinator"])
	}
}

func TestMigrateConfig_NonExistentFile(t *testing.T) {
	_, err := migrate.MigrateConfig("/nonexistent/path/config.json", false)
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestMigrateConfig_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	os.WriteFile(configPath, []byte("not json"), 0o644)

	_, err := migrate.MigrateConfig(configPath, false)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}
