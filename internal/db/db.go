// Package db provides SQLite database access via the system sqlite3 CLI.
// No cgo or external Go modules required.
package db

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
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

// ExecContext runs a write SQL statement with context cancellation support.
func ExecContext(ctx context.Context, dbPath, sql string) error {
	writeMu.Lock()
	defer writeMu.Unlock()
	cmd := exec.CommandContext(ctx, "sqlite3", dbPath, ".timeout 30000", sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sqlite3: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// QueryContext runs a SQL query with context cancellation support.
func QueryContext(ctx context.Context, dbPath, sql string) ([]map[string]any, error) {
	cmd := exec.CommandContext(ctx, "sqlite3", "-json", dbPath, ".timeout 30000", sql)
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

// Escape sanitizes a string for safe interpolation inside a single-quoted
// SQLite string literal. It removes NULL bytes (which truncate SQL strings)
// and doubles single quotes ('' is the SQL-standard escape).
//
// Backslash (\) is intentionally NOT escaped: SQLite uses SQL-standard string
// literals, not C-style. Inside '...', a backslash is a literal character and
// is not an escape introducer, so the injection vectors that exist in MySQL
// ("\\'") or PostgreSQL E-strings do not apply here.
//
// Multi-byte UTF-8 is safe by construction: UTF-8 continuation bytes use the
// high-bit pattern 10xxxxxx (0x80-0xBF) and can never contain 0x27 (') or
// 0x00. So a valid UTF-8 sequence cannot smuggle an unescaped quote through
// this function.
func Escape(s string) string {
	s = strings.ReplaceAll(s, "\x00", "")
	s = strings.ReplaceAll(s, "'", "''")
	return s
}

// bindArgs replaces ? placeholders in query with safely-quoted argument values,
// providing a parameterized-query interface over the sqlite3 CLI backend.
//
// Because this package drives SQLite via the sqlite3 CLI subprocess rather than
// a native Go database/sql driver, there is no db.Prepare() or driver-level
// binding available. bindArgs fills that gap: callers write standard ? placeholders
// and pass Go values as separate arguments; bindArgs escapes each value via Escape
// and interpolates it as a single-quoted SQL literal before the SQL string is
// handed to the subprocess.
//
// Each ? is consumed left-to-right; extra args beyond the placeholder count are
// silently ignored. Only string-like interpolation is performed — callers must
// not rely on type-specific SQL casts. Use ExecArgs / QueryArgs instead of
// calling bindArgs directly.
//
// WARNING: bindArgs does not parse SQL; a literal `?` inside a string literal
// (e.g. `LIKE '%?%'`) is treated as a placeholder and silently consumed by the
// next arg. None of the current callers hit this case, but future writers
// should either rewrite such queries to use concatenation-free patterns
// (e.g. `LIKE ?` with an arg of `"%pattern%"`) or avoid ExecArgs/QueryArgs
// for those statements.
func bindArgs(query string, args []any) string {
	if len(args) == 0 {
		return query
	}
	var b strings.Builder
	b.Grow(len(query) + len(args)*32)
	argIdx := 0
	for i := 0; i < len(query); i++ {
		if query[i] == '?' && argIdx < len(args) {
			b.WriteByte('\'')
			b.WriteString(Escape(fmt.Sprintf("%v", args[argIdx])))
			b.WriteByte('\'')
			argIdx++
		} else {
			b.WriteByte(query[i])
		}
	}
	return b.String()
}

// ExecArgs runs a write SQL statement with ? parameterized arguments.
// It is the parameterized equivalent of Exec: instead of building the SQL string
// manually (which risks injection if values come from external input), callers
// pass raw Go values and let bindArgs handle quoting.
//
// Example:
//
//	err := db.ExecArgs(dbPath,
//	    "UPDATE workflows SET status=?, finished_at=? WHERE id=?",
//	    "error", time.Now().UTC().Format(time.RFC3339), workflowID)
//
// Writes are serialized via writeMu, so ExecArgs is safe for concurrent use.
func ExecArgs(dbPath, query string, args ...any) error {
	return Exec(dbPath, bindArgs(query, args))
}

// QueryArgs runs a SQL query with ? parameterized arguments.
// It is the parameterized equivalent of Query: instead of building the SQL string
// manually (which risks injection if values come from external input), callers
// pass raw Go values and let bindArgs handle quoting.
//
// Example:
//
//	rows, err := db.QueryArgs(dbPath,
//	    "SELECT id, status FROM workflow_runs WHERE workflow_id=? AND status=?",
//	    workflowID, "running")
//
// Returns nil (not an error) when the query produces zero rows.
func QueryArgs(dbPath, query string, args ...any) ([]map[string]any, error) {
	return Query(dbPath, bindArgs(query, args))
}

// --- JSON row helpers ---
// These parse values returned from sqlite3 -json output.

// Str extracts a string from a JSON row value.
func Str(v any) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case json.Number:
		return val.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}

// Int extracts an int from a JSON row value.
func Int(v any) int {
	if v == nil {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return int(val)
	case json.Number:
		i, _ := val.Int64()
		return int(i)
	case string:
		i, _ := strconv.Atoi(val)
		return i
	default:
		return 0
	}
}

// Float extracts a float64 from a JSON row value.
func Float(v any) float64 {
	if v == nil {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return val
	case json.Number:
		f, _ := val.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(val, 64)
		return f
	default:
		return 0
	}
}

// Truncate truncates a string to maxLen characters, appending "..." if truncated.
func Truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
