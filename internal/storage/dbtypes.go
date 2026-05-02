package storage

// QueryFn executes a SELECT query and returns rows as a slice of string maps.
type QueryFn func(dbPath, sql string) ([]map[string]any, error)

// ExecFn executes a non-query SQL statement.
type ExecFn func(dbPath, sql string) error

// EscapeFn escapes a string for safe SQLite embedding.
type EscapeFn func(s string) string

// LogFn is a structured log function (Info/Warn level).
type LogFn func(msg string, keyvals ...any)

// DBHelpers bundles the minimal database helpers needed by storage services.
type DBHelpers struct {
	Query   QueryFn
	Exec    ExecFn
	Escape  EscapeFn
	LogInfo LogFn
	LogWarn LogFn
}
