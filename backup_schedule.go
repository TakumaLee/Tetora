package main

// backup_schedule.go is a thin facade wrapping internal/scheduling backup.
// Business logic lives in internal/scheduling/backup.go.

import (
	"context"

	"tetora/internal/db"
	"tetora/internal/log"
	"tetora/internal/scheduling"
)

// --- Type aliases ---

type BackupScheduler = scheduling.BackupScheduler
type BackupResult = scheduling.BackupResult
type BackupInfo = scheduling.BackupInfo

// --- Constructor ---

// newBackupScheduler creates a new backup scheduler.
func newBackupScheduler(cfg *Config) *BackupScheduler {
	bcfg := scheduling.BackupConfig{
		DBPath:     cfg.HistoryDB,
		BackupDir:  cfg.Ops.BackupDirResolved(cfg.BaseDir),
		RetainDays: cfg.Ops.BackupRetainOrDefault(),
		EscapeSQL:  db.Escape,
		LogInfo:    log.Info,
		LogWarn:    log.Warn,
	}
	return scheduling.NewBackupScheduler(bcfg)
}

// --- Forwarding helpers ---

// verifyDBBackup runs sqlite3 integrity_check on a backup file.
func verifyDBBackup(path string) error {
	return scheduling.VerifyDBBackup(path)
}

// copyFile copies a file from src to dst using io.Copy.
func copyFile(src, dst string) error {
	return scheduling.CopyFile(src, dst)
}

// Ensure BackupScheduler.Start signature is compatible — it takes context.Context.
var _ = (*BackupScheduler).Start
var _ = func(ctx context.Context) {}
