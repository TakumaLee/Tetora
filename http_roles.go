package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

func (s *Server) registerRoleRoutes(mux *http.ServeMux) {
	cfg := s.cfg
	cron := s.cron

	// --- Roles: archetypes ---
	mux.HandleFunc("/roles/archetypes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		type archInfo struct {
			Name           string `json:"name"`
			Description    string `json:"description"`
			Model          string `json:"model"`
			PermissionMode string `json:"permissionMode"`
			SoulTemplate   string `json:"soulTemplate"`
		}
		var archs []archInfo
		for _, a := range builtinArchetypes {
			archs = append(archs, archInfo{
				Name:           a.Name,
				Description:    a.Description,
				Model:          a.Model,
				PermissionMode: a.PermissionMode,
				SoulTemplate:   a.SoulTemplate,
			})
		}
		json.NewEncoder(w).Encode(archs)
	})

	// --- Roles: list + create ---
	mux.HandleFunc("/roles", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case http.MethodGet:
			type roleInfo struct {
				Name           string `json:"name"`
				Model          string `json:"model"`
				PermissionMode string `json:"permissionMode,omitempty"`
				SoulFile       string `json:"soulFile"`
				Description    string `json:"description"`
				SoulPreview    string `json:"soulPreview,omitempty"`
			}

			var roles []roleInfo
			for name, rc := range cfg.Roles {
				ri := roleInfo{
					Name:           name,
					Model:          rc.Model,
					PermissionMode: rc.PermissionMode,
					SoulFile:       rc.SoulFile,
					Description:    rc.Description,
				}
				// Load soul file preview.
				if content, err := loadRolePrompt(cfg, name); err == nil && content != "" {
					if len(content) > 500 {
						ri.SoulPreview = content[:500] + "..."
					} else {
						ri.SoulPreview = content
					}
				}
				roles = append(roles, ri)
			}
			if roles == nil {
				roles = []roleInfo{}
			}
			json.NewEncoder(w).Encode(roles)

		case http.MethodPost:
			var body struct {
				Name           string `json:"name"`
				Model          string `json:"model"`
				PermissionMode string `json:"permissionMode"`
				Description    string `json:"description"`
				SoulFile       string `json:"soulFile"`
				SoulContent    string `json:"soulContent"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
				return
			}
			if body.Name == "" {
				http.Error(w, `{"error":"name is required"}`, http.StatusBadRequest)
				return
			}
			if _, exists := cfg.Roles[body.Name]; exists {
				http.Error(w, `{"error":"role already exists"}`, http.StatusConflict)
				return
			}

			// Default soul file name if not specified.
			if body.SoulFile == "" {
				body.SoulFile = fmt.Sprintf("SOUL-%s.md", body.Name)
			}

			// Write soul content to file.
			if body.SoulContent != "" {
				if err := writeSoulFile(cfg, body.SoulFile, body.SoulContent); err != nil {
					http.Error(w, fmt.Sprintf(`{"error":"write soul file: %v"}`, err), http.StatusInternalServerError)
					return
				}
			}

			rc := RoleConfig{
				SoulFile:       body.SoulFile,
				Model:          body.Model,
				Description:    body.Description,
				PermissionMode: body.PermissionMode,
			}

			configPath := findConfigPath()
			if err := updateConfigRoles(configPath, body.Name, &rc); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"save config: %v"}`, err), http.StatusInternalServerError)
				return
			}

			// Hot-reload into memory.
			if cfg.Roles == nil {
				cfg.Roles = make(map[string]RoleConfig)
			}
			cfg.Roles[body.Name] = rc

			auditLog(cfg.HistoryDB, "role.create", "http",
				fmt.Sprintf("name=%s", body.Name), clientIP(r))
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"status":"created"}`))

		default:
			http.Error(w, "GET or POST only", http.StatusMethodNotAllowed)
		}
	})

	// --- Roles: per-role actions ---
	mux.HandleFunc("/roles/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Parse /roles/<name> - skip the archetypes path.
		path := strings.TrimPrefix(r.URL.Path, "/roles/")
		if path == "" || path == "archetypes" {
			return // handled by other handlers
		}
		name := path

		switch r.Method {
		case http.MethodGet:
			rc, ok := cfg.Roles[name]
			if !ok {
				http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
				return
			}
			result := map[string]any{
				"name":           name,
				"model":          rc.Model,
				"permissionMode": rc.PermissionMode,
				"soulFile":       rc.SoulFile,
				"description":    rc.Description,
			}
			// Load full soul content (not just preview).
			if content, err := loadRolePrompt(cfg, name); err == nil {
				result["soulContent"] = content
			}
			json.NewEncoder(w).Encode(result)

		case http.MethodPut:
			rc, ok := cfg.Roles[name]
			if !ok {
				http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
				return
			}
			var body struct {
				Model          string `json:"model"`
				PermissionMode string `json:"permissionMode"`
				Description    string `json:"description"`
				SoulFile       string `json:"soulFile"`
				SoulContent    string `json:"soulContent"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
				return
			}

			// Update fields.
			if body.Model != "" {
				rc.Model = body.Model
			}
			if body.PermissionMode != "" {
				rc.PermissionMode = body.PermissionMode
			}
			if body.Description != "" {
				rc.Description = body.Description
			}
			if body.SoulFile != "" {
				rc.SoulFile = body.SoulFile
			}
			if body.SoulContent != "" {
				soulFile := rc.SoulFile
				if soulFile == "" {
					soulFile = fmt.Sprintf("SOUL-%s.md", name)
					rc.SoulFile = soulFile
				}
				if err := writeSoulFile(cfg, soulFile, body.SoulContent); err != nil {
					http.Error(w, fmt.Sprintf(`{"error":"write soul: %v"}`, err), http.StatusInternalServerError)
					return
				}
			}

			configPath := findConfigPath()
			if err := updateConfigRoles(configPath, name, &rc); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"save: %v"}`, err), http.StatusInternalServerError)
				return
			}
			cfg.Roles[name] = rc
			auditLog(cfg.HistoryDB, "role.update", "http",
				fmt.Sprintf("name=%s", name), clientIP(r))
			w.Write([]byte(`{"status":"updated"}`))

		case http.MethodDelete:
			if _, ok := cfg.Roles[name]; !ok {
				http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
				return
			}
			// Check if any job uses this role.
			if cron != nil {
				for _, j := range cron.ListJobs() {
					if j.Role == name {
						http.Error(w, fmt.Sprintf(`{"error":"role in use by job %q"}`, j.ID), http.StatusConflict)
						return
					}
				}
			}
			configPath := findConfigPath()
			if err := updateConfigRoles(configPath, name, nil); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"save: %v"}`, err), http.StatusInternalServerError)
				return
			}
			delete(cfg.Roles, name)
			auditLog(cfg.HistoryDB, "role.delete", "http",
				fmt.Sprintf("name=%s", name), clientIP(r))
			w.Write([]byte(`{"status":"deleted"}`))

		default:
			http.Error(w, "GET, PUT or DELETE only", http.StatusMethodNotAllowed)
		}
	})
}
