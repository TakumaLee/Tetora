package main

// export.go exposes the internal/export package types to the main package.

import "tetora/internal/export"

// ExportResult is an alias for export.Result so callers in package main
// can reference the type without a package-qualified name.
type ExportResult = export.Result
