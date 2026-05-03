package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"tetora/internal/audit"
)

// ProjectsDeps holds dependencies for projects HTTP handlers.
type ProjectsDeps struct {
	ListProjects     func(status string) (any, error)
	GetProject       func(id string) (any, error)
	CreateProject    func(raw json.RawMessage) (any, error) // returns created project
	UpdateProject    func(id string, raw json.RawMessage) (any, error)
	DeleteProject    func(id string) error
	ScanWorkspace    func() (any, string, error) // returns (entries, sourceFile, error)
	GetProjectStats  func(id string) (any, error)
	TaskBoardEnabled bool
	HistoryDB        func() string
}

// RegisterProjectRoutes registers project CRUD and directory browser API routes.
func RegisterProjectRoutes(mux *http.ServeMux, d ProjectsDeps) {
	mux.HandleFunc("/api/projects", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			status := r.URL.Query().Get("status")
			projects, err := d.ListProjects(status)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(projects)

		case http.MethodPost:
			var raw json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"invalid json: %v"}`, err), http.StatusBadRequest)
				return
			}
			project, err := d.CreateProject(raw)
			if err != nil {
				code := http.StatusInternalServerError
				if strings.Contains(err.Error(), "UNIQUE constraint") {
					code = http.StatusConflict
				}
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), code)
				return
			}
			audit.Log(d.HistoryDB(), "project.create", "http", "created", clientIP(r))
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(project)

		default:
			http.Error(w, `{"error":"GET or POST only"}`, http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/projects/scan-workspace", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		entries, source, err := d.ScanWorkspace()
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"entries": entries, "source": source})
	})

	mux.HandleFunc("/api/projects/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		subPath := strings.TrimPrefix(r.URL.Path, "/api/projects/")
		subPath = strings.TrimSuffix(subPath, "/")
		if subPath == "" {
			http.Error(w, `{"error":"project id required"}`, http.StatusBadRequest)
			return
		}

		if parts := strings.SplitN(subPath, "/", 2); len(parts) == 2 && parts[1] == "stats" {
			if r.Method != http.MethodGet {
				http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
				return
			}
			if !d.TaskBoardEnabled {
				http.Error(w, `{"error":"task board not enabled"}`, http.StatusServiceUnavailable)
				return
			}
			stats, err := d.GetProjectStats(parts[0])
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(stats)
			return
		}

		id := subPath

		switch r.Method {
		case http.MethodGet:
			p, err := d.GetProject(id)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			if p == nil {
				http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
				return
			}
			json.NewEncoder(w).Encode(p)

		case http.MethodPut:
			var raw json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"invalid json: %v"}`, err), http.StatusBadRequest)
				return
			}
			updated, err := d.UpdateProject(id, raw)
			if err != nil {
				code := http.StatusInternalServerError
				if strings.Contains(err.Error(), "not found") {
					code = http.StatusNotFound
				}
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), code)
				return
			}
			audit.Log(d.HistoryDB(), "project.update", "http",
				fmt.Sprintf("id=%s", id), clientIP(r))
			json.NewEncoder(w).Encode(updated)

		case http.MethodDelete:
			if err := d.DeleteProject(id); err != nil {
				code := http.StatusInternalServerError
				if strings.Contains(err.Error(), "not found") {
					code = http.StatusNotFound
				}
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), code)
				return
			}
			audit.Log(d.HistoryDB(), "project.delete", "http",
				fmt.Sprintf("id=%s", id), clientIP(r))
			w.Write([]byte(`{"status":"deleted"}`))

		default:
			http.Error(w, `{"error":"GET, PUT or DELETE only"}`, http.StatusMethodNotAllowed)
		}
	})

	// GET /api/dirs — list subdirectories for folder browser.
	mux.HandleFunc("/api/dirs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}

		dirPath := r.URL.Query().Get("path")
		if dirPath == "" {
			home, _ := os.UserHomeDir()
			dirPath = home
		}
		if strings.HasPrefix(dirPath, "~/") {
			home, _ := os.UserHomeDir()
			dirPath = filepath.Join(home, dirPath[2:])
		} else if dirPath == "~" {
			home, _ := os.UserHomeDir()
			dirPath = home
		}

		entries, err := os.ReadDir(dirPath)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
			return
		}

		type dirEntry struct {
			Name string `json:"name"`
			Path string `json:"path"`
		}
		dirs := make([]dirEntry, 0)
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			dirs = append(dirs, dirEntry{
				Name: name,
				Path: filepath.Join(dirPath, name),
			})
		}

		parent := filepath.Dir(dirPath)
		json.NewEncoder(w).Encode(map[string]any{
			"path":   dirPath,
			"parent": parent,
			"dirs":   dirs,
		})
	})
}
