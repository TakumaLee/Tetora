package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBackupScheduler_RunBackup(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	backupDir := filepath.Join(dir, "backups")

	// Create a test database with some data.
	exec.Command("sqlite3", dbPath, "CREATE TABLE test(id INTEGER); INSERT INTO test VALUES(1);").Run()
	if err := initOpsDB(dbPath); err != nil {
		t.Fatalf("initOpsDB failed: %v", err)
	}

	cfg := &Config{
		HistoryDB: dbPath,
		baseDir:   dir,
		Ops: OpsConfig{
			BackupDir:    backupDir,
			BackupRetain: 7,
		},
	}

	bs := newBackupScheduler(cfg)

	result, err := bs.RunBackup()
	if err != nil {
		t.Fatalf("RunBackup failed: %v", err)
	}

	// Verify result fields.
	if result.Filename == "" {
		t.Error("expected filename")
	}
	if result.SizeBytes <= 0 {
		t.Error("expected positive size")
	}
	if result.DurationMs < 0 {
		t.Error("expected non-negative duration")
	}
	if result.CreatedAt == "" {
		t.Error("expected createdAt")
	}

	// Verify backup file exists.
	if _, err := os.Stat(result.Filename); err != nil {
		t.Fatalf("backup file does not exist: %v", err)
	}

	// Verify backup file has content.
	info, _ := os.Stat(result.Filename)
	if info.Size() == 0 {
		t.Error("backup file is empty")
	}

	// Verify backup was logged.
	rows, err := queryDB(dbPath, "SELECT filename, status FROM backup_log ORDER BY id DESC LIMIT 1")
	if err != nil {
		t.Fatalf("query backup_log failed: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("expected backup_log entry")
	}
	if rows[0]["status"] != "success" {
		t.Errorf("expected status=success, got %v", rows[0]["status"])
	}
}

func TestBackupScheduler_RunBackupNoHistoryDB(t *testing.T) {
	cfg := &Config{HistoryDB: ""}
	bs := newBackupScheduler(cfg)

	_, err := bs.RunBackup()
	if err == nil {
		t.Error("expected error for empty historyDB")
	}
	if !strings.Contains(err.Error(), "historyDB not configured") {
		t.Errorf("expected historyDB error, got: %v", err)
	}
}

func TestBackupScheduler_CleanOldBackups(t *testing.T) {
	dir := t.TempDir()
	backupDir := filepath.Join(dir, "backups")
	os.MkdirAll(backupDir, 0o755)

	// Create some old backup files.
	oldFile := filepath.Join(backupDir, "20250101-000000_tetora.db.bak")
	os.WriteFile(oldFile, []byte("old backup"), 0o644)
	// Set modification time to 30 days ago.
	oldTime := time.Now().AddDate(0, 0, -30)
	os.Chtimes(oldFile, oldTime, oldTime)

	// Create a recent backup file.
	newFile := filepath.Join(backupDir, "20260223-000000_tetora.db.bak")
	os.WriteFile(newFile, []byte("new backup"), 0o644)

	// Create a non-backup file (should not be deleted).
	otherFile := filepath.Join(backupDir, "notes.txt")
	os.WriteFile(otherFile, []byte("notes"), 0o644)
	os.Chtimes(otherFile, oldTime, oldTime)

	cfg := &Config{
		baseDir: dir,
		Ops: OpsConfig{
			BackupDir:    backupDir,
			BackupRetain: 7,
		},
	}

	bs := newBackupScheduler(cfg)
	removed := bs.CleanOldBackups()

	if removed != 1 {
		t.Errorf("expected 1 removed, got %d", removed)
	}

	// Old backup should be gone.
	if _, err := os.Stat(oldFile); err == nil {
		t.Error("old backup should have been removed")
	}

	// New backup should remain.
	if _, err := os.Stat(newFile); err != nil {
		t.Error("new backup should remain")
	}

	// Non-backup file should remain.
	if _, err := os.Stat(otherFile); err != nil {
		t.Error("non-backup file should remain")
	}
}

func TestBackupScheduler_ListBackups(t *testing.T) {
	dir := t.TempDir()
	backupDir := filepath.Join(dir, "backups")
	os.MkdirAll(backupDir, 0o755)

	// Create backup files.
	os.WriteFile(filepath.Join(backupDir, "20260101-000000_tetora.db.bak"), []byte("backup1"), 0o644)
	os.WriteFile(filepath.Join(backupDir, "20260201-000000_tetora.db.bak"), []byte("backup22"), 0o644)
	// Create a non-backup file.
	os.WriteFile(filepath.Join(backupDir, "random.txt"), []byte("not a backup"), 0o644)

	cfg := &Config{
		baseDir: dir,
		Ops: OpsConfig{
			BackupDir: backupDir,
		},
	}

	bs := newBackupScheduler(cfg)
	backups, err := bs.ListBackups()
	if err != nil {
		t.Fatalf("ListBackups failed: %v", err)
	}

	if len(backups) != 2 {
		t.Fatalf("expected 2 backups, got %d", len(backups))
	}

	// Should be sorted newest first.
	if !strings.Contains(backups[0].Filename, "20260201") {
		t.Errorf("expected newest first, got %s", backups[0].Filename)
	}
}

func TestBackupScheduler_ListBackupsEmptyDir(t *testing.T) {
	dir := t.TempDir()

	cfg := &Config{
		baseDir: dir,
		Ops: OpsConfig{
			BackupDir: filepath.Join(dir, "nonexistent"),
		},
	}

	bs := newBackupScheduler(cfg)
	backups, err := bs.ListBackups()
	if err != nil {
		t.Fatalf("ListBackups failed: %v", err)
	}
	if len(backups) != 0 {
		t.Errorf("expected 0 backups, got %d", len(backups))
	}
}

func TestBackupScheduler_DefaultBackupDir(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	exec.Command("sqlite3", dbPath, "CREATE TABLE test(id INTEGER)").Run()
	if err := initOpsDB(dbPath); err != nil {
		t.Fatalf("initOpsDB failed: %v", err)
	}

	cfg := &Config{
		HistoryDB: dbPath,
		baseDir:   dir,
		Ops:       OpsConfig{}, // No backupDir set — should use default.
	}

	bs := newBackupScheduler(cfg)
	result, err := bs.RunBackup()
	if err != nil {
		t.Fatalf("RunBackup with default dir failed: %v", err)
	}

	// Should be in baseDir/backups.
	expectedDir := filepath.Join(dir, "backups")
	if !strings.HasPrefix(result.Filename, expectedDir) {
		t.Errorf("expected backup in %s, got %s", expectedDir, result.Filename)
	}
}

func TestCopyFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")

	content := "hello world"
	os.WriteFile(src, []byte(content), 0o644)

	err := copyFile(src, dst)
	if err != nil {
		t.Fatalf("copyFile failed: %v", err)
	}

	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst failed: %v", err)
	}
	if string(data) != content {
		t.Errorf("expected %q, got %q", content, string(data))
	}
}

func TestCopyFile_SourceNotExists(t *testing.T) {
	dir := t.TempDir()
	err := copyFile(filepath.Join(dir, "nonexistent"), filepath.Join(dir, "dst"))
	if err == nil {
		t.Error("expected error for missing source")
	}
}
