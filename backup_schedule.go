package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// --- P23.7: Backup Scheduler ---

// BackupScheduler manages periodic database backups.
type BackupScheduler struct {
	cfg    *Config
	dbPath string
}

// BackupResult describes the result of a backup operation.
type BackupResult struct {
	Filename   string `json:"filename"`
	SizeBytes  int64  `json:"sizeBytes"`
	DurationMs int64  `json:"durationMs"`
	CreatedAt  string `json:"createdAt"`
}

// BackupInfo describes a stored backup file.
type BackupInfo struct {
	Filename  string `json:"filename"`
	SizeBytes int64  `json:"sizeBytes"`
	CreatedAt string `json:"createdAt"`
}

// newBackupScheduler creates a new backup scheduler.
func newBackupScheduler(cfg *Config) *BackupScheduler {
	return &BackupScheduler{
		cfg:    cfg,
		dbPath: cfg.HistoryDB,
	}
}

// RunBackup copies the database file to the backup directory.
func (bs *BackupScheduler) RunBackup() (*BackupResult, error) {
	if bs.dbPath == "" {
		return nil, fmt.Errorf("historyDB not configured")
	}

	start := time.Now()

	// Ensure backup directory exists.
	backupDir := bs.cfg.Ops.backupDirResolved(bs.cfg.baseDir)
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return nil, fmt.Errorf("create backup dir: %w", err)
	}

	// Generate backup filename.
	date := time.Now().UTC().Format("20060102-150405")
	backupFilename := fmt.Sprintf("%s_tetora.db.bak", date)
	backupPath := filepath.Join(backupDir, backupFilename)

	// Copy database file.
	if err := copyFile(bs.dbPath, backupPath); err != nil {
		bs.logBackup(backupFilename, 0, "failed", time.Since(start).Milliseconds())
		return nil, fmt.Errorf("copy database: %w", err)
	}

	// Verify backup integrity.
	if err := verifyDBBackup(backupPath); err != nil {
		bs.logBackup(backupFilename, 0, "verify_failed", time.Since(start).Milliseconds())
		os.Remove(backupPath) // remove corrupt backup
		return nil, fmt.Errorf("backup verification failed: %w", err)
	}

	// Get backup file size.
	info, err := os.Stat(backupPath)
	if err != nil {
		return nil, fmt.Errorf("stat backup: %w", err)
	}

	duration := time.Since(start).Milliseconds()

	// Log the backup.
	bs.logBackup(backupFilename, info.Size(), "success", duration)

	result := &BackupResult{
		Filename:   backupPath,
		SizeBytes:  info.Size(),
		DurationMs: duration,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}

	logInfo("backup complete", "filename", backupFilename, "sizeBytes", info.Size(), "durationMs", duration)
	return result, nil
}

// logBackup records a backup operation in the backup_log table.
func (bs *BackupScheduler) logBackup(filename string, sizeBytes int64, status string, durationMs int64) {
	if bs.dbPath == "" {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	sql := fmt.Sprintf(
		`INSERT INTO backup_log (filename, size_bytes, status, duration_ms, created_at) VALUES ('%s', %d, '%s', %d, '%s')`,
		escapeSQLite(filename), sizeBytes, escapeSQLite(status), durationMs, now,
	)
	cmd := exec.Command("sqlite3", bs.dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		logWarn("backup log insert failed", "error", err, "output", string(out))
	}
}

// CleanOldBackups removes backup files older than the retention period.
// Returns the number of files removed.
func (bs *BackupScheduler) CleanOldBackups() int {
	backupDir := bs.cfg.Ops.backupDirResolved(bs.cfg.baseDir)
	retainDays := bs.cfg.Ops.backupRetainOrDefault()
	cutoff := time.Now().AddDate(0, 0, -retainDays)

	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return 0
	}

	removed := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".db.bak") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		if info.ModTime().Before(cutoff) {
			path := filepath.Join(backupDir, entry.Name())
			if err := os.Remove(path); err != nil {
				logWarn("remove old backup failed", "file", entry.Name(), "error", err)
			} else {
				removed++
			}
		}
	}

	if removed > 0 {
		logInfo("old backups cleaned", "removed", removed, "retainDays", retainDays)
	}
	return removed
}

// ListBackups returns all backup files sorted by creation time (newest first).
func (bs *BackupScheduler) ListBackups() ([]BackupInfo, error) {
	backupDir := bs.cfg.Ops.backupDirResolved(bs.cfg.baseDir)

	entries, err := os.ReadDir(backupDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []BackupInfo{}, nil
		}
		return nil, fmt.Errorf("read backup dir: %w", err)
	}

	var backups []BackupInfo
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".db.bak") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		backups = append(backups, BackupInfo{
			Filename:  entry.Name(),
			SizeBytes: info.Size(),
			CreatedAt: info.ModTime().UTC().Format(time.RFC3339),
		})
	}

	// Sort by filename descending (filenames include date, so this gives newest first).
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].Filename > backups[j].Filename
	})

	return backups, nil
}

// Start runs periodic backups based on a simplified schedule.
// The schedule uses a daily interval: runs once per 24h.
func (bs *BackupScheduler) Start(ctx context.Context) {
	go func() {
		// Run an initial backup shortly after startup.
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}

		if _, err := bs.RunBackup(); err != nil {
			logWarn("initial backup failed", "error", err)
		}
		bs.CleanOldBackups()

		// Then run daily.
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := bs.RunBackup(); err != nil {
					logWarn("periodic backup failed", "error", err)
				}
				bs.CleanOldBackups()
			}
		}
	}()
}

// verifyDBBackup runs sqlite3 integrity_check on a backup file.
func verifyDBBackup(path string) error {
	cmd := exec.Command("sqlite3", path, "PRAGMA integrity_check;")
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("integrity_check: %w", err)
	}
	result := strings.TrimSpace(string(out))
	if result != "ok" {
		return fmt.Errorf("integrity_check: %s", result)
	}
	return nil
}

// copyFile copies a file from src to dst using io.Copy.
func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return err
	}

	return dstFile.Sync()
}
