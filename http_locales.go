package main

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"
)

//go:embed locales/*.json
var localesFS embed.FS

// availableLocales returns locale codes from embedded JSON files.
func availableLocales() []string {
	entries, err := fs.ReadDir(localesFS, "locales")
	if err != nil {
		return nil
	}
	var langs []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			langs = append(langs, strings.TrimSuffix(e.Name(), ".json"))
		}
	}
	return langs
}

// handleLocalesList responds with the list of available locale codes.
func handleLocalesList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(availableLocales())
}

// handleLocaleGet serves a specific locale JSON file.
func handleLocaleGet(w http.ResponseWriter, r *http.Request) {
	lang := strings.TrimPrefix(r.URL.Path, "/api/locales/")
	lang = strings.TrimRight(lang, "/")
	if lang == "" {
		handleLocalesList(w, r)
		return
	}
	// Sanitize: only allow alphanumeric and hyphens.
	for _, c := range lang {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-') {
			http.Error(w, "invalid locale", http.StatusBadRequest)
			return
		}
	}
	data, err := localesFS.ReadFile(filepath.Join("locales", lang+".json"))
	if err != nil {
		http.Error(w, "locale not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write(data)
}
