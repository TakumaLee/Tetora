package main

import (
	"embed"
	"encoding/json"
	"net/http"
	"strings"
)

//go:embed README.md README.*.md INSTALL.md CHANGELOG.md ROADMAP.md CONTRIBUTING.md docs/*.md
var docsFS embed.FS

type docsPageEntry struct {
	Name        string   `json:"name"`
	File        string   `json:"file"`
	Description string   `json:"description"`
	Langs       []string `json:"langs"`
}

var supportedDocsLangs = []string{"zh-TW", "ja", "ko", "id", "th", "fil", "es", "fr", "de"}

var docsList = []docsPageEntry{
	{Name: "README", File: "README.md", Description: "Project Overview"},
	{Name: "Configuration", File: "docs/configuration.md", Description: "Config Reference"},
	{Name: "Workflows", File: "docs/workflow.md", Description: "Workflow Engine"},
	{Name: "Taskboard", File: "docs/taskboard.md", Description: "Kanban & Auto-Dispatch"},
	{Name: "Hooks", File: "docs/hooks.md", Description: "Claude Code Hooks"},
	{Name: "MCP", File: "docs/mcp.md", Description: "Model Context Protocol"},
	{Name: "Discord Multitasking", File: "docs/discord-multitasking.md", Description: "Thread & Focus"},
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

// initDocsLangs scans docsFS for translated variants of each doc and populates
// the Langs field and allowedDocFiles map.
func initDocsLangs() {
	for i := range docsList {
		entry := &docsList[i]
		base := strings.TrimSuffix(entry.File, ".md")
		var langs []string
		for _, lang := range supportedDocsLangs {
			candidate := base + "." + lang + ".md"
			if _, err := docsFS.ReadFile(candidate); err == nil {
				langs = append(langs, lang)
				allowedDocFiles[candidate] = struct{}{}
			}
		}
		entry.Langs = langs
	}
}

func (s *Server) registerDocsRoutes(mux *http.ServeMux) {
	initDocsLangs()

	// GET /api/docs — list available documentation files
	mux.HandleFunc("/api/docs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(docsList)
	})

	// GET /api/docs/{file}?lang=xx — return raw markdown content of a doc file
	// The {file} portion may include a subdirectory, e.g. /api/docs/docs/workflow.md
	// Optional ?lang= parameter resolves to a translated variant, falling back to base.
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
		if _, ok := allowedDocFiles[filePath]; !ok {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}

		// Resolve translated variant if ?lang= is provided
		resolvedPath := filePath
		if lang := r.URL.Query().Get("lang"); lang != "" && lang != "en" {
			base := strings.TrimSuffix(filePath, ".md")
			candidate := base + "." + lang + ".md"
			if _, ok := allowedDocFiles[candidate]; ok {
				resolvedPath = candidate
			}
		}

		data, err := docsFS.ReadFile(resolvedPath)
		if err != nil {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write(data)
	})
}
