package httpapi

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"
)

// RegisterLocaleRoutes registers the locale API endpoints using the provided
// embedded filesystem. The FS must contain a "locales/" directory with JSON files
// named "{locale-code}.json".
func RegisterLocaleRoutes(mux *http.ServeMux, localesFS fs.FS) {
	mux.HandleFunc("/api/locales", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(availableLocalesFrom(localesFS))
	})
	mux.HandleFunc("/api/locales/", func(w http.ResponseWriter, r *http.Request) {
		lang := strings.TrimPrefix(r.URL.Path, "/api/locales/")
		lang = strings.TrimRight(lang, "/")
		if lang == "" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(availableLocalesFrom(localesFS))
			return
		}
		// Sanitize: only allow alphanumeric and hyphens.
		for _, c := range lang {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-') {
				http.Error(w, "invalid locale", http.StatusBadRequest)
				return
			}
		}
		data, err := fs.ReadFile(localesFS, filepath.Join("locales", lang+".json"))
		if err != nil {
			http.Error(w, "locale not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Write(data)
	})
}

// availableLocalesFrom returns locale codes from JSON files in the "locales/" directory.
func availableLocalesFrom(localesFS fs.FS) []string {
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
