package cli

import (
	"fmt"
	"os"
	"path/filepath"
)

// CmdBackup — backup feature archived.
func CmdBackup(_ []string) {
	fmt.Fprintln(os.Stderr, "backup: not available (feature archived)")
	os.Exit(1)
}

// CmdRestore — restore feature archived.
func CmdRestore(_ []string) {
	fmt.Fprintln(os.Stderr, "restore: not available (feature archived)")
	os.Exit(1)
}

// CmdBackupList — backup list feature archived.
func CmdBackupList(_ []string) {
	fmt.Fprintln(os.Stderr, "backup list: not available (feature archived)")
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
