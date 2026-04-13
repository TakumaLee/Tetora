package main

import (
	"embed"
	"net/http"

	"tetora/internal/httpapi"
)

//go:embed locales/*.json
var localesFS embed.FS

// registerLocaleRoutes wires locale API endpoints into mux.
// Handler logic lives in internal/httpapi; this file owns the embedded FS
// because the locales/ directory is co-located with the root package.
func registerLocaleRoutes(mux *http.ServeMux) {
	httpapi.RegisterLocaleRoutes(mux, localesFS)
}
