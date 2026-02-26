package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDailyNotesConfig(t *testing.T) {
	cfg := DailyNotesConfig{}
	if cfg.scheduleOrDefault() != "0 0 * * *" {
		t.Errorf("default schedule wrong: got %s", cfg.scheduleOrDefault())
	}

	cfg.Schedule = "0 12 * * *"
	if cfg.scheduleOrDefault() != "0 12 * * *" {
		t.Errorf("custom schedule wrong: got %s", cfg.scheduleOrDefault())
	}

	baseDir := "/tmp/tetora-test"
	if cfg.dirOrDefault(baseDir) != "/tmp/tetora-test/notes" {
		t.Errorf("default dir wrong: got %s", cfg.dirOrDefault(baseDir))
	}

	cfg.Dir = "custom_notes"
	if cfg.dirOrDefault(baseDir) != "/tmp/tetora-test/custom_notes" {
		t.Errorf("relative dir wrong: got %s", cfg.dirOrDefault(baseDir))
	}

	cfg.Dir = "/absolute/path"
	if cfg.dirOrDefault(baseDir) != "/absolute/path" {
		t.Errorf("absolute dir wrong: got %s", cfg.dirOrDefault(baseDir))
	}
}

func TestGenerateDailyNote(t *testing.T) {
	// Create temp DB with test data.
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create DB schema.
	schema := `CREATE TABLE IF NOT EXISTS history (
		id TEXT PRIMARY KEY,
		name TEXT,
		source TEXT,
		agent TEXT,
		status TEXT,
		duration_ms INTEGER,
		cost_usd REAL,
		tokens_in INTEGER,
		tokens_out INTEGER,
		started_at TEXT,
		finished_at TEXT
	);`
	if _, err := queryDB(dbPath, schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	cfg := &Config{
		baseDir:   tmpDir,
		HistoryDB: dbPath,
	}

	// Insert test tasks.
	yesterday := time.Now().AddDate(0, 0, -1)
	startedAt := yesterday.Format("2006-01-02 10:00:00")
	sql := `INSERT INTO history (id, name, source, agent, status, duration_ms, cost_usd, tokens_in, tokens_out, started_at, finished_at)
	        VALUES ('test1', 'Test Task 1', 'cron', '琉璃', 'success', 1000, 0.05, 100, 200, '` + escapeSQLite(startedAt) + `', '` + escapeSQLite(startedAt) + `')`
	if _, err := queryDB(dbPath, sql); err != nil {
		t.Fatalf("insert test data: %v", err)
	}

	// Generate note.
	content, err := generateDailyNote(cfg, yesterday)
	if err != nil {
		t.Fatalf("generate note: %v", err)
	}

	if content == "" {
		t.Fatal("note content is empty")
	}

	if !dailyNoteContains(content, "# Daily Summary") {
		t.Error("note missing header")
	}
	if !dailyNoteContains(content, "Total Tasks") {
		t.Error("note missing summary")
	}
	if !dailyNoteContains(content, "Test Task 1") {
		t.Error("note missing task details")
	}
}

func TestWriteDailyNote(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &Config{
		baseDir: tmpDir,
		DailyNotes: DailyNotesConfig{
			Enabled: true,
			Dir:     "notes",
		},
	}

	date := time.Now()
	content := "# Daily Summary\n\nTest content."

	if err := writeDailyNote(cfg, date, content); err != nil {
		t.Fatalf("write note: %v", err)
	}

	notesDir := cfg.DailyNotes.dirOrDefault(tmpDir)
	filename := date.Format("2006-01-02") + ".md"
	filePath := filepath.Join(notesDir, filename)

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read note file: %v", err)
	}

	if string(data) != content {
		t.Errorf("note content mismatch: got %q", string(data))
	}
}

func dailyNoteContains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && len(s) >= len(substr) && dailyNoteFindSubstring(s, substr)
}

func dailyNoteFindSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
