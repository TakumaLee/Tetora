// Package db provides SQLite database access via the system sqlite3 CLI.
// No cgo or external Go modules required.
package db

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

// Task represents a row from the tasks table.
type Task struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	Priority  string `json:"priority"`
	CreatedAt string `json:"created_at"`
	Error     string `json:"error"`
}

// TaskStats holds aggregate task counts by status.
type TaskStats struct {
	Todo    int `json:"todo"`
	Running int `json:"running"`
	Review  int `json:"review"`
	Done    int `json:"done"`
	Failed  int `json:"failed"`
	Total   int `json:"total"`
}

// writeMu serializes all SQLite write operations to prevent "database is locked"
// errors from concurrent sqlite3 CLI processes competing for the same DB file.
var writeMu sync.Mutex

// Exec runs a write SQL statement against the SQLite database.
// Writes are serialized via writeMu to prevent concurrent sqlite3 processes
// from causing "database is locked" errors.
func Exec(dbPath, sql string) error {
	writeMu.Lock()
	defer writeMu.Unlock()
	cmd := exec.Command("sqlite3", dbPath, ".timeout 30000", sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sqlite3: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// Query runs a SQL query against the SQLite database and returns JSON rows.
// Uses .timeout dot-command (no output) instead of PRAGMA busy_timeout (produces JSON).
func Query(dbPath, sql string) ([]map[string]any, error) {
	cmd := exec.Command("sqlite3", "-json", dbPath, ".timeout 30000", sql)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("sqlite3: %s", string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("sqlite3: %w", err)
	}

	outStr := strings.TrimSpace(string(out))
	if outStr == "" || outStr == "[]" {
		return nil, nil
	}

	var rows []map[string]any
	if err := json.Unmarshal([]byte(outStr), &rows); err != nil {
		return nil, fmt.Errorf("parse sqlite3 output: %w", err)
	}
	return rows, nil
}

// Pragma sets recommended SQLite pragmas for reliability.
// WAL mode enables concurrent reads during writes.
// busy_timeout prevents "database is locked" under contention.
func Pragma(dbPath string) error {
	pragmas := []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA busy_timeout=30000;",
		"PRAGMA synchronous=NORMAL;",
	}
	for _, p := range pragmas {
		cmd := exec.Command("sqlite3", dbPath, p)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("pragma %q: %s: %w", p, string(out), err)
		}
	}
	return nil
}

// Escape sanitizes a string for safe SQLite interpolation.
// Handles single quotes, null bytes, and control characters.
func Escape(s string) string {
	// Remove null bytes — these can truncate SQL strings.
	s = strings.ReplaceAll(s, "\x00", "")
	// Escape single quotes for SQL.
	s = strings.ReplaceAll(s, "'", "''")
	return s
}
