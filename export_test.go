package main

import (
	"archive/zip"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportUserData(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_export.db")
	exec.Command("sqlite3", dbPath, "SELECT 1").Run()

	// Create the ops tables.
	if err := initOpsDB(dbPath); err != nil {
		t.Fatalf("initOpsDB failed: %v", err)
	}

	// Create some test tables that export expects.
	ddl := `
CREATE TABLE IF NOT EXISTS agent_memory (
    key TEXT, value TEXT, agent TEXT, updated_at TEXT
);
INSERT INTO agent_memory (key, value, agent, updated_at) VALUES ('name', 'test-user', 'default', '2026-01-01T00:00:00Z');
INSERT INTO agent_memory (key, value, agent, updated_at) VALUES ('pref', 'dark-mode', 'default', '2026-01-02T00:00:00Z');

CREATE TABLE IF NOT EXISTS history (
    job_id TEXT, name TEXT, status TEXT, started_at TEXT, finished_at TEXT
);
INSERT INTO history (job_id, name, status, started_at, finished_at) VALUES ('abc123', 'test-job', 'success', '2026-01-01T00:00:00Z', '2026-01-01T00:01:00Z');
`
	exec.Command("sqlite3", dbPath, ddl).Run()

	cfg := &Config{
		HistoryDB: dbPath,
		baseDir:   dir,
		Ops: OpsConfig{
			ExportEnabled: true,
		},
	}

	result, err := exportUserData(cfg, "")
	if err != nil {
		t.Fatalf("exportUserData failed: %v", err)
	}

	// Check result fields.
	if result.Tables == 0 {
		t.Error("expected at least 1 exported table")
	}
	if result.SizeBytes <= 0 {
		t.Error("expected positive file size")
	}
	if result.Filename == "" {
		t.Error("expected filename")
	}
	if result.CreatedAt == "" {
		t.Error("expected createdAt")
	}

	// Verify the zip file exists.
	if _, err := os.Stat(result.Filename); err != nil {
		t.Fatalf("zip file does not exist: %v", err)
	}

	// Open and inspect the zip.
	r, err := zip.OpenReader(result.Filename)
	if err != nil {
		t.Fatalf("open zip failed: %v", err)
	}
	defer r.Close()

	fileNames := make(map[string]bool)
	for _, f := range r.File {
		fileNames[f.Name] = true
	}

	// Should contain manifest.
	if !fileNames["manifest.json"] {
		t.Error("expected manifest.json in zip")
	}

	// Should contain at least agent_memory.json.
	if !fileNames["agent_memory.json"] {
		t.Error("expected agent_memory.json in zip")
	}

	// Verify agent_memory.json content.
	for _, f := range r.File {
		if f.Name == "agent_memory.json" {
			rc, err := f.Open()
			if err != nil {
				t.Fatalf("open agent_memory.json: %v", err)
			}
			var rows []map[string]any
			if err := json.NewDecoder(rc).Decode(&rows); err != nil {
				rc.Close()
				t.Fatalf("decode agent_memory.json: %v", err)
			}
			rc.Close()
			if len(rows) != 2 {
				t.Errorf("expected 2 rows in agent_memory, got %d", len(rows))
			}
		}
	}
}

func TestExportUserData_WithUserIDFilter(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_export_filter.db")
	exec.Command("sqlite3", dbPath, "SELECT 1").Run()

	if err := initOpsDB(dbPath); err != nil {
		t.Fatalf("initOpsDB failed: %v", err)
	}

	// Create reminders table with user_id column.
	ddl := `
CREATE TABLE IF NOT EXISTS reminders (
    id TEXT, user_id TEXT, text TEXT, fire_at TEXT, status TEXT
);
INSERT INTO reminders VALUES ('r1', 'alice', 'buy milk', '2026-01-01T00:00:00Z', 'pending');
INSERT INTO reminders VALUES ('r2', 'bob', 'call mom', '2026-01-01T00:00:00Z', 'pending');
INSERT INTO reminders VALUES ('r3', 'alice', 'meeting', '2026-01-02T00:00:00Z', 'pending');
`
	exec.Command("sqlite3", dbPath, ddl).Run()

	cfg := &Config{
		HistoryDB: dbPath,
		baseDir:   dir,
		Ops:       OpsConfig{ExportEnabled: true},
	}

	result, err := exportUserData(cfg, "alice")
	if err != nil {
		t.Fatalf("exportUserData failed: %v", err)
	}

	// Open zip and check reminders.
	r, err := zip.OpenReader(result.Filename)
	if err != nil {
		t.Fatalf("open zip failed: %v", err)
	}
	defer r.Close()

	for _, f := range r.File {
		if f.Name == "reminders.json" {
			rc, err := f.Open()
			if err != nil {
				t.Fatalf("open reminders.json: %v", err)
			}
			var rows []map[string]any
			json.NewDecoder(rc).Decode(&rows)
			rc.Close()
			if len(rows) != 2 {
				t.Errorf("expected 2 reminders for alice, got %d", len(rows))
			}
		}
	}
}

func TestExportUserData_NoHistoryDB(t *testing.T) {
	cfg := &Config{HistoryDB: ""}
	_, err := exportUserData(cfg, "")
	if err == nil {
		t.Error("expected error for empty historyDB")
	}
	if !strings.Contains(err.Error(), "historyDB not configured") {
		t.Errorf("expected historyDB error, got: %v", err)
	}
}

func TestExportUserData_MissingTables(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_export_empty.db")
	exec.Command("sqlite3", dbPath, "SELECT 1").Run()

	cfg := &Config{
		HistoryDB: dbPath,
		baseDir:   dir,
		Ops:       OpsConfig{ExportEnabled: true},
	}

	result, err := exportUserData(cfg, "")
	if err != nil {
		t.Fatalf("exportUserData failed even with missing tables: %v", err)
	}

	// Should still produce a zip with manifest.
	if result.Tables != 0 {
		t.Errorf("expected 0 tables exported from empty db, got %d", result.Tables)
	}

	r, err := zip.OpenReader(result.Filename)
	if err != nil {
		t.Fatalf("open zip failed: %v", err)
	}
	defer r.Close()

	found := false
	for _, f := range r.File {
		if f.Name == "manifest.json" {
			found = true
		}
	}
	if !found {
		t.Error("expected manifest.json even with empty export")
	}
}

func TestCreateZipFromDir(t *testing.T) {
	srcDir := t.TempDir()
	destDir := t.TempDir()

	// Write some test files.
	os.WriteFile(filepath.Join(srcDir, "file1.txt"), []byte("hello"), 0o644)
	os.WriteFile(filepath.Join(srcDir, "file2.txt"), []byte("world"), 0o644)

	zipPath := filepath.Join(destDir, "test.zip")
	err := createZipFromDir(srcDir, zipPath)
	if err != nil {
		t.Fatalf("createZipFromDir failed: %v", err)
	}

	r, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("open zip failed: %v", err)
	}
	defer r.Close()

	if len(r.File) != 2 {
		t.Errorf("expected 2 files in zip, got %d", len(r.File))
	}
}

func TestExportManifestContent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_manifest.db")
	exec.Command("sqlite3", dbPath, "SELECT 1").Run()

	if err := initOpsDB(dbPath); err != nil {
		t.Fatalf("initOpsDB failed: %v", err)
	}

	cfg := &Config{
		HistoryDB: dbPath,
		baseDir:   dir,
		Ops:       OpsConfig{ExportEnabled: true},
	}

	result, err := exportUserData(cfg, "test-user")
	if err != nil {
		t.Fatalf("exportUserData failed: %v", err)
	}

	r, err := zip.OpenReader(result.Filename)
	if err != nil {
		t.Fatalf("open zip failed: %v", err)
	}
	defer r.Close()

	for _, f := range r.File {
		if f.Name == "manifest.json" {
			rc, _ := f.Open()
			var manifest map[string]any
			json.NewDecoder(rc).Decode(&manifest)
			rc.Close()

			if manifest["userID"] != "test-user" {
				t.Errorf("expected userID=test-user, got %v", manifest["userID"])
			}
			if manifest["exportTimestamp"] == nil {
				t.Error("expected exportTimestamp in manifest")
			}
		}
	}
}
