package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func cmdBackup(args []string) {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	output := fs.String("output", "", "output path for backup file (default: ~/.tetora/backups/tetora-backup-TIMESTAMP.tar.gz)")
	fs.Parse(args)

	baseDir := findBaseDir()
	outputPath := *output
	if outputPath == "" {
		backupDir := filepath.Join(baseDir, "backups")
		os.MkdirAll(backupDir, 0o755)
		ts := time.Now().Format("20060102-150405")
		outputPath = filepath.Join(backupDir, fmt.Sprintf("tetora-backup-%s.tar.gz", ts))
	}

	fmt.Printf("Creating backup of %s ...\n", baseDir)

	if err := createBackup(baseDir, outputPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Print file size.
	info, err := os.Stat(outputPath)
	if err == nil {
		fmt.Printf("Backup created: %s (%s)\n", outputPath, formatSize(info.Size()))
	} else {
		fmt.Printf("Backup created: %s\n", outputPath)
	}

	// List contents.
	entries, err := listBackupContents(outputPath)
	if err == nil {
		fmt.Printf("Files: %d\n", len(entries))
	}
}

func cmdRestore(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: tetora restore <backup-file>")
		fmt.Println()
		fmt.Println("Restores a tetora backup. A pre-restore backup is created automatically.")
		return
	}

	backupPath := args[0]

	// Validate backup exists.
	if _, err := os.Stat(backupPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: backup file not found: %s\n", backupPath)
		os.Exit(1)
	}

	// List contents first.
	entries, err := listBackupContents(backupPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: invalid backup: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Backup contains %d files:\n", len(entries))
	for _, e := range entries {
		fmt.Printf("  %s\n", e)
	}
	fmt.Println()

	targetDir := findBaseDir()
	fmt.Printf("Restoring to %s ...\n", targetDir)

	if err := restoreBackup(backupPath, targetDir); err != nil {
		fmt.Fprintf(os.Stderr, "Error: restore failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Restore complete.")
	fmt.Println("Note: Restart the tetora daemon to pick up restored config.")
}

func cmdBackupList(args []string) {
	baseDir := findBaseDir()
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
			formatSize(info.Size()),
			filepath.Join(backupDir, name))
		found++
	}

	if found == 0 {
		fmt.Println("No backups found.")
	} else {
		fmt.Printf("\n%d backup(s) in %s\n", found, backupDir)
	}
}

// findBaseDir returns the tetora base directory (~/.tetora).
func findBaseDir() string {
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

// formatSize returns a human-readable file size string.
func formatSize(size int64) string {
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
