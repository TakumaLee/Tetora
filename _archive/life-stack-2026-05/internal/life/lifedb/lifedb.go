// Package lifedb provides shared database helpers for life service packages.
// It decouples the life services from the root-package helper implementations
// by accepting them as injected function types.
package lifedb

// QueryFn executes a SELECT query and returns rows as a slice of string maps.
type QueryFn func(dbPath, sql string) ([]map[string]any, error)

// ExecFn executes a non-query SQL statement.
type ExecFn func(dbPath, sql string) error

// EscapeFn escapes a string for safe SQLite embedding.
type EscapeFn func(s string) string

// LogFn is a structured log function (Info/Warn level).
type LogFn func(msg string, keyvals ...any)

// DB bundles the minimal database helpers needed by life services.
type DB struct {
	Query  QueryFn
	Exec   ExecFn
	Escape EscapeFn
	LogInfo  LogFn
	LogWarn  LogFn
}
