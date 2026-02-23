package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// shouldInclude
// ---------------------------------------------------------------------------

func TestShouldInclude_ConfigJSON(t *testing.T) {
	if !shouldInclude("config.json") {
		t.Error("config.json should be included")
	}
}

func TestShouldInclude_JobsJSON(t *testing.T) {
	if !shouldInclude("jobs.json") {
		t.Error("jobs.json should be included")
	}
}

func TestShouldInclude_HistoryDB(t *testing.T) {
	if !shouldInclude("history.db") {
		t.Error("history.db should be included")
	}
}

func TestShouldInclude_PromptsDir(t *testing.T) {
	if !shouldInclude("prompts/daily-report.md") {
		t.Error("prompts/*.md should be included")
	}
}

func TestShouldInclude_KnowledgeDir(t *testing.T) {
	if !shouldInclude("knowledge/readme.txt") {
		t.Error("knowledge/* should be included")
	}
}

func TestShouldInclude_SoulsDir(t *testing.T) {
	if !shouldInclude("souls/engineer/SOUL.md") {
		t.Error("souls/**/* should be included")
	}
}

func TestShouldInclude_TopLevelSoulFile(t *testing.T) {
	if !shouldInclude("SOUL.md") {
		t.Error("SOUL.md should be included")
	}
	if !shouldInclude("SOUL-ruri.md") {
		t.Error("SOUL-ruri.md should be included")
	}
}

func TestShouldInclude_ExcludeBin(t *testing.T) {
	if shouldInclude("bin/tetora") {
		t.Error("bin/* should be excluded")
	}
}

func TestShouldInclude_ExcludeOutputs(t *testing.T) {
	if shouldInclude("outputs/task-123.json") {
		t.Error("outputs/* should be excluded")
	}
}

func TestShouldInclude_ExcludeLogs(t *testing.T) {
	if shouldInclude("logs/tetora.log") {
		t.Error("logs/* should be excluded")
	}
}

func TestShouldInclude_ExcludeBackups(t *testing.T) {
	if shouldInclude("backups/tetora-backup-20260101.tar.gz") {
		t.Error("backups/* should be excluded")
	}
}

func TestShouldInclude_ExcludeTarGz(t *testing.T) {
	if shouldInclude("something.tar.gz") {
		t.Error("*.tar.gz should be excluded")
	}
}

func TestShouldInclude_ExcludeConfigBackups(t *testing.T) {
	if shouldInclude("config.json.backup.20260101") {
		t.Error("config.json.backup.* should be excluded")
	}
}

func TestShouldInclude_ExcludeMCPDir(t *testing.T) {
	if shouldInclude("mcp/playwright.json") {
		t.Error("mcp/* should be excluded")
	}
}

func TestShouldInclude_ExcludeRandomFiles(t *testing.T) {
	if shouldInclude("random.txt") {
		t.Error("random files should be excluded")
	}
}

// ---------------------------------------------------------------------------
// createBackup + listBackupContents
// ---------------------------------------------------------------------------

func TestCreateBackup_Basic(t *testing.T) {
	// Create a temporary tetora-like directory.
	baseDir := t.TempDir()
	os.WriteFile(filepath.Join(baseDir, "config.json"), []byte(`{"claudePath":"claude"}`), 0o644)
	os.WriteFile(filepath.Join(baseDir, "jobs.json"), []byte(`{"jobs":[]}`), 0o644)

	// Create prompts dir.
	promptsDir := filepath.Join(baseDir, "prompts")
	os.MkdirAll(promptsDir, 0o755)
	os.WriteFile(filepath.Join(promptsDir, "test.md"), []byte("# Test"), 0o644)

	// Create a soul file.
	os.WriteFile(filepath.Join(baseDir, "SOUL.md"), []byte("# Soul"), 0o644)

	// Create excluded dirs.
	os.MkdirAll(filepath.Join(baseDir, "bin"), 0o755)
	os.WriteFile(filepath.Join(baseDir, "bin", "tetora"), []byte("binary"), 0o755)
	os.MkdirAll(filepath.Join(baseDir, "outputs"), 0o755)
	os.WriteFile(filepath.Join(baseDir, "outputs", "task.json"), []byte("{}"), 0o644)

	// Create backup.
	outputPath := filepath.Join(t.TempDir(), "test-backup.tar.gz")
	if err := createBackup(baseDir, outputPath); err != nil {
		t.Fatalf("createBackup() error: %v", err)
	}

	// Verify backup exists.
	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatalf("backup file not created: %v", err)
	}
	if info.Size() == 0 {
		t.Error("backup file is empty")
	}

	// List contents.
	entries, err := listBackupContents(outputPath)
	if err != nil {
		t.Fatalf("listBackupContents() error: %v", err)
	}

	// Check expected files are present.
	entrySet := make(map[string]bool)
	for _, e := range entries {
		entrySet[e] = true
	}

	expected := []string{"config.json", "jobs.json", "SOUL.md", "prompts/test.md"}
	for _, f := range expected {
		if !entrySet[f] {
			t.Errorf("expected %s in backup, got entries: %v", f, entries)
		}
	}

	// Check excluded files are absent.
	excluded := []string{"bin/tetora", "outputs/task.json"}
	for _, f := range excluded {
		if entrySet[f] {
			t.Errorf("expected %s to be excluded from backup", f)
		}
	}
}

func TestCreateBackup_EmptyDir(t *testing.T) {
	baseDir := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "empty-backup.tar.gz")

	err := createBackup(baseDir, outputPath)
	if err == nil {
		t.Error("expected error for empty directory")
	}
	if !strings.Contains(err.Error(), "no files to backup") {
		t.Errorf("expected 'no files to backup' error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// restoreBackup
// ---------------------------------------------------------------------------

func TestRestoreBackup_Basic(t *testing.T) {
	// Create source dir.
	sourceDir := t.TempDir()
	os.WriteFile(filepath.Join(sourceDir, "config.json"), []byte(`{"configVersion":2}`), 0o644)
	os.WriteFile(filepath.Join(sourceDir, "jobs.json"), []byte(`{"jobs":[]}`), 0o644)
	promptsDir := filepath.Join(sourceDir, "prompts")
	os.MkdirAll(promptsDir, 0o755)
	os.WriteFile(filepath.Join(promptsDir, "daily.md"), []byte("# Daily"), 0o644)

	// Create backup.
	backupPath := filepath.Join(t.TempDir(), "backup.tar.gz")
	if err := createBackup(sourceDir, backupPath); err != nil {
		t.Fatalf("createBackup() error: %v", err)
	}

	// Restore to new dir.
	targetDir := t.TempDir()
	// Create a config.json so pre-restore backup works.
	os.WriteFile(filepath.Join(targetDir, "config.json"), []byte(`{}`), 0o644)

	if err := restoreBackup(backupPath, targetDir); err != nil {
		t.Fatalf("restoreBackup() error: %v", err)
	}

	// Verify files were restored.
	data, err := os.ReadFile(filepath.Join(targetDir, "config.json"))
	if err != nil {
		t.Fatalf("config.json not restored: %v", err)
	}
	if !strings.Contains(string(data), "configVersion") {
		t.Error("config.json content mismatch")
	}

	data, err = os.ReadFile(filepath.Join(targetDir, "prompts", "daily.md"))
	if err != nil {
		t.Fatalf("prompts/daily.md not restored: %v", err)
	}
	if string(data) != "# Daily" {
		t.Errorf("prompts/daily.md content = %q, want '# Daily'", string(data))
	}
}

func TestRestoreBackup_NonExistentFile(t *testing.T) {
	err := restoreBackup("/nonexistent/backup.tar.gz", t.TempDir())
	if err == nil {
		t.Error("expected error for nonexistent backup")
	}
}

// ---------------------------------------------------------------------------
// listBackupContents
// ---------------------------------------------------------------------------

func TestListBackupContents_InvalidFile(t *testing.T) {
	// Create a non-gzip file.
	path := filepath.Join(t.TempDir(), "not-a-backup.tar.gz")
	os.WriteFile(path, []byte("not gzip data"), 0o644)

	_, err := listBackupContents(path)
	if err == nil {
		t.Error("expected error for invalid gzip file")
	}
}

// ---------------------------------------------------------------------------
// formatSize
// ---------------------------------------------------------------------------

// TestFormatSize is in knowledge_test.go (shared utility).

// ---------------------------------------------------------------------------
// Round-trip: create + list + restore
// ---------------------------------------------------------------------------

func TestBackupRoundTrip(t *testing.T) {
	// Create a full tetora-like setup.
	baseDir := t.TempDir()

	os.WriteFile(filepath.Join(baseDir, "config.json"),
		[]byte(`{"configVersion":2,"claudePath":"claude","listenAddr":"127.0.0.1:8991"}`), 0o644)
	os.WriteFile(filepath.Join(baseDir, "jobs.json"),
		[]byte(`{"jobs":[{"id":"test","schedule":"0 * * * *"}]}`), 0o644)

	// Create souls directory tree.
	soulsDir := filepath.Join(baseDir, "souls", "engineer")
	os.MkdirAll(soulsDir, 0o755)
	os.WriteFile(filepath.Join(soulsDir, "SOUL.md"), []byte("# Engineer Soul"), 0o644)

	// Create knowledge dir.
	knowledgeDir := filepath.Join(baseDir, "knowledge")
	os.MkdirAll(knowledgeDir, 0o755)
	os.WriteFile(filepath.Join(knowledgeDir, "notes.txt"), []byte("Some notes"), 0o644)

	// Backup.
	backupPath := filepath.Join(t.TempDir(), "roundtrip.tar.gz")
	if err := createBackup(baseDir, backupPath); err != nil {
		t.Fatalf("createBackup: %v", err)
	}

	// List.
	entries, err := listBackupContents(backupPath)
	if err != nil {
		t.Fatalf("listBackupContents: %v", err)
	}

	expectedFiles := map[string]bool{
		"config.json":              false,
		"jobs.json":                false,
		"souls/engineer/SOUL.md":   false,
		"knowledge/notes.txt":      false,
	}
	for _, e := range entries {
		if _, ok := expectedFiles[e]; ok {
			expectedFiles[e] = true
		}
	}
	for f, found := range expectedFiles {
		if !found {
			t.Errorf("expected %s in backup entries: %v", f, entries)
		}
	}

	// Restore to new location.
	restoreDir := t.TempDir()
	os.WriteFile(filepath.Join(restoreDir, "config.json"), []byte(`{}`), 0o644)
	if err := restoreBackup(backupPath, restoreDir); err != nil {
		t.Fatalf("restoreBackup: %v", err)
	}

	// Verify restored files.
	for f := range expectedFiles {
		if _, err := os.Stat(filepath.Join(restoreDir, f)); err != nil {
			t.Errorf("restored file %s not found: %v", f, err)
		}
	}

	// Verify content integrity.
	data, _ := os.ReadFile(filepath.Join(restoreDir, "souls", "engineer", "SOUL.md"))
	if string(data) != "# Engineer Soul" {
		t.Errorf("souls/engineer/SOUL.md content = %q, want '# Engineer Soul'", string(data))
	}
}
