package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"tetora/internal/backup"
)

// CmdBackup implements `tetora backup [--output PATH]`.
func CmdBackup(args []string) {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	output := fs.String("output", "", "output path for backup file (default: ~/.tetora/backups/tetora-backup-TIMESTAMP.tar.gz)")
	fs.Parse(args) //nolint:errcheck

	baseDir := FindBaseDir()
	outputPath := *output
	if outputPath == "" {
		backupDir := filepath.Join(baseDir, "backups")
		os.MkdirAll(backupDir, 0o755) //nolint:errcheck
		ts := time.Now().Format("20060102-150405")
		outputPath = filepath.Join(backupDir, fmt.Sprintf("tetora-backup-%s.tar.gz", ts))
	}

	fmt.Printf("Creating backup of %s ...\n", baseDir)

	if err := backup.Create(baseDir, outputPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	info, err := os.Stat(outputPath)
	if err == nil {
		fmt.Printf("Backup created: %s (%s)\n", outputPath, FormatSize(info.Size()))
	} else {
		fmt.Printf("Backup created: %s\n", outputPath)
	}

	entries, err := backup.ListContents(outputPath)
	if err == nil {
		fmt.Printf("Files: %d\n", len(entries))
	}
}

// CmdRestore implements `tetora restore <backup-file>`.
func CmdRestore(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: tetora restore <backup-file>")
		fmt.Println()
		fmt.Println("Restores a tetora backup. A pre-restore backup is created automatically.")
		return
	}

	backupPath := args[0]

	if _, err := os.Stat(backupPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: backup file not found: %s\n", backupPath)
		os.Exit(1)
	}

	entries, err := backup.ListContents(backupPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: invalid backup: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Backup contains %d files:\n", len(entries))
	for _, e := range entries {
		fmt.Printf("  %s\n", e)
	}
	fmt.Println()

	targetDir := FindBaseDir()
	fmt.Printf("Restoring to %s ...\n", targetDir)

	if err := backup.Restore(backupPath, targetDir); err != nil {
		fmt.Fprintf(os.Stderr, "Error: restore failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Restore complete.")
	fmt.Println("Note: Restart the tetora daemon to pick up restored config.")
}

// CmdBackupList implements `tetora backup list`.
func CmdBackupList(args []string) {
	baseDir := FindBaseDir()
	backupDir := filepath.Join(baseDir, "backups")

	entries, err := os.ReadDir(backupDir)
	if err != nil {
		fmt.Println("No backups found.")
		return
	}

	found := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if len(name) < 7 || name[len(name)-7:] != ".tar.gz" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		fmt.Printf("  %s  %s  %s\n",
			info.ModTime().Format("2006-01-02 15:04:05"),
			FormatSize(info.Size()),
			filepath.Join(backupDir, name))
		found++
	}

	if found == 0 {
		fmt.Println("No backups found.")
	} else {
		fmt.Printf("\n%d backup(s) in %s\n", found, backupDir)
	}
}

// FindBaseDir returns the tetora base directory (~/.tetora).
func FindBaseDir() string {
	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "..")
		if abs, err := filepath.Abs(candidate); err == nil {
			if _, err := os.Stat(filepath.Join(abs, "config.json")); err == nil {
				return abs
			}
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".tetora")
}

// FormatSize returns a human-readable file size string.
func FormatSize(size int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)
	switch {
	case size >= GB:
		return fmt.Sprintf("%.1f GB", float64(size)/float64(GB))
	case size >= MB:
		return fmt.Sprintf("%.1f MB", float64(size)/float64(MB))
	case size >= KB:
		return fmt.Sprintf("%.1f KB", float64(size)/float64(KB))
	default:
		return fmt.Sprintf("%d B", size)
	}
}
