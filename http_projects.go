package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (s *Server) registerProjectRoutes(mux *http.ServeMux) {
	cfg := s.cfg

	// Initialize projects table.
	if err := initProjectsDB(cfg.HistoryDB); err != nil {
		logWarn("init projects db failed", "error", err)
	}

	// GET /api/projects        → list projects
	// POST /api/projects       → create project
	mux.HandleFunc("/api/projects", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			status := r.URL.Query().Get("status")
			projects, err := listProjects(cfg.HistoryDB, status)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			if projects == nil {
				projects = []Project{}
			}
			json.NewEncoder(w).Encode(map[string]any{"projects": projects, "count": len(projects)})

		case http.MethodPost:
			var p Project
			if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"invalid json: %v"}`, err), http.StatusBadRequest)
				return
			}
			if p.Name == "" {
				http.Error(w, `{"error":"name is required"}`, http.StatusBadRequest)
				return
			}
			if p.ID == "" {
				p.ID = generateProjectID()
			}
			now := time.Now().UTC().Format(time.RFC3339)
			p.CreatedAt = now
			p.UpdatedAt = now
			if p.Status == "" {
				p.Status = "active"
			}
			if err := createProject(cfg.HistoryDB, p); err != nil {
				code := http.StatusInternalServerError
				if strings.Contains(err.Error(), "UNIQUE constraint") {
					code = http.StatusConflict
				}
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), code)
				return
			}
			auditLog(cfg.HistoryDB, "project.create", "http",
				fmt.Sprintf("id=%s name=%s", p.ID, p.Name), clientIP(r))
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(p)

		default:
			http.Error(w, `{"error":"GET or POST only"}`, http.StatusMethodNotAllowed)
		}
	})

	// GET /api/projects/scan-workspace — parse PROJECTS.md from workspace for import suggestions.
	mux.HandleFunc("/api/projects/scan-workspace", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		projectsFile := filepath.Join(cfg.WorkspaceDir, "projects", "PROJECTS.md")
		entries, err := parseProjectsMD(projectsFile)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"entries": entries, "source": projectsFile})
	})

	// GET    /api/projects/:id  → get project
	// PUT    /api/projects/:id  → update project
	// DELETE /api/projects/:id  → delete project
	mux.HandleFunc("/api/projects/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		subPath := strings.TrimPrefix(r.URL.Path, "/api/projects/")
		subPath = strings.TrimSuffix(subPath, "/")
		if subPath == "" {
			http.Error(w, `{"error":"project id required"}`, http.StatusBadRequest)
			return
		}

		// Handle /api/projects/{id}/stats sub-route.
		if parts := strings.SplitN(subPath, "/", 2); len(parts) == 2 && parts[1] == "stats" {
			if r.Method != http.MethodGet {
				http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
				return
			}
			if cfg.TaskBoard.Enabled {
				tb := newTaskBoardEngine(cfg.HistoryDB, cfg.TaskBoard, cfg.Webhooks)
				stats, err := tb.GetProjectStats(parts[0])
				if err != nil {
					http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
					return
				}
				json.NewEncoder(w).Encode(stats)
			} else {
				http.Error(w, `{"error":"task board not enabled"}`, http.StatusServiceUnavailable)
			}
			return
		}

		id := subPath

		switch r.Method {
		case http.MethodGet:
			p, err := getProject(cfg.HistoryDB, id)
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
			var p Project
			if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"invalid json: %v"}`, err), http.StatusBadRequest)
				return
			}
			// Ensure ID matches path.
			p.ID = id
			existing, err := getProject(cfg.HistoryDB, id)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			if existing == nil {
				http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
				return
			}
			// Preserve created_at from existing.
			p.CreatedAt = existing.CreatedAt
			if err := updateProject(cfg.HistoryDB, p); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			auditLog(cfg.HistoryDB, "project.update", "http",
				fmt.Sprintf("id=%s", id), clientIP(r))
			updated, _ := getProject(cfg.HistoryDB, id)
			if updated == nil {
				json.NewEncoder(w).Encode(p)
			} else {
				json.NewEncoder(w).Encode(updated)
			}

		case http.MethodDelete:
			existing, err := getProject(cfg.HistoryDB, id)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			if existing == nil {
				http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
				return
			}
			if err := deleteProject(cfg.HistoryDB, id); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			auditLog(cfg.HistoryDB, "project.delete", "http",
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
		// Expand ~ prefix.
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
			// Skip hidden directories.
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

// generateProjectID creates a random short ID for a new project.
func generateProjectID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return fmt.Sprintf("proj_%x", b)
}
