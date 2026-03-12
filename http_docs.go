package main

import (
	"embed"
	"encoding/json"
	"net/http"
	"strings"
)

//go:embed README.md INSTALL.md CHANGELOG.md ROADMAP.md CONTRIBUTING.md docs/*.md
var docsFS embed.FS

type docsPageEntry struct {
	Name        string `json:"name"`
	File        string `json:"file"`
	Description string `json:"description"`
}

var docsList = []docsPageEntry{
	{Name: "README", File: "README.md", Description: "Project Overview"},
	{Name: "Configuration", File: "docs/configuration.md", Description: "Config Reference"},
	{Name: "Workflows", File: "docs/workflow.md", Description: "Workflow Engine"},
	{Name: "Taskboard", File: "docs/taskboard.md", Description: "Kanban & Auto-Dispatch"},
	{Name: "Hooks", File: "docs/hooks.md", Description: "Claude Code Hooks"},
	{Name: "Troubleshooting", File: "docs/troubleshooting.md", Description: "Common Issues"},
	{Name: "Changelog", File: "CHANGELOG.md", Description: "Release History"},
	{Name: "Roadmap", File: "ROADMAP.md", Description: "Future Plans"},
	{Name: "Contributing", File: "CONTRIBUTING.md", Description: "Contributor Guide"},
	{Name: "Installation", File: "INSTALL.md", Description: "Setup Guide"},
}

// buildDocFileSet returns a set of allowed file paths from docsList for fast lookup.
func buildDocFileSet() map[string]struct{} {
	set := make(map[string]struct{}, len(docsList))
	for _, d := range docsList {
		set[d.File] = struct{}{}
	}
	return set
}

var allowedDocFiles = buildDocFileSet()

func (s *Server) registerDocsRoutes(mux *http.ServeMux) {
	// GET /api/docs — list available documentation files
	mux.HandleFunc("/api/docs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(docsList)
	})

	// GET /api/docs/{file} — return raw markdown content of a doc file
	// The {file} portion may include a subdirectory, e.g. /api/docs/docs/workflow.md
	mux.HandleFunc("/api/docs/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}

		filePath := strings.TrimPrefix(r.URL.Path, "/api/docs/")
		if filePath == "" {
			http.Error(w, `{"error":"file path required"}`, http.StatusBadRequest)
			return
		}

		// Security: only serve files that are in the explicit allowlist.
		// This also guards against path traversal since we never serve arbitrary paths.
		if _, ok := allowedDocFiles[filePath]; !ok {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}

		data, err := docsFS.ReadFile(filePath)
		if err != nil {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write(data)
	})
}
