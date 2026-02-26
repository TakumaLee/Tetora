package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
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

	// GET    /api/projects/:id  → get project
	// PUT    /api/projects/:id  → update project
	// DELETE /api/projects/:id  → delete project
	mux.HandleFunc("/api/projects/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		id := strings.TrimPrefix(r.URL.Path, "/api/projects/")
		id = strings.TrimSuffix(id, "/")
		if id == "" {
			http.Error(w, `{"error":"project id required"}`, http.StatusBadRequest)
			return
		}

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
}

// generateProjectID creates a random short ID for a new project.
func generateProjectID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return fmt.Sprintf("proj_%x", b)
}
