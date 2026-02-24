package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

func (s *Server) registerMemoryRoutes(mux *http.ServeMux) {
	cfg := s.cfg

	// --- MCP Configs ---
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case "GET":
			configs := listMCPConfigs(cfg)
			if configs == nil {
				configs = []MCPConfigInfo{}
			}
			json.NewEncoder(w).Encode(configs)

		case "POST":
			var body struct {
				Name   string          `json:"name"`
				Config json.RawMessage `json:"config"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
				return
			}
			if body.Name == "" || len(body.Config) == 0 {
				http.Error(w, `{"error":"name and config are required"}`, http.StatusBadRequest)
				return
			}
			configPath := findConfigPath()
			if err := setMCPConfig(cfg, configPath, body.Name, body.Config); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
				return
			}
			auditLog(cfg.HistoryDB, "mcp.save", "http", body.Name, clientIP(r))
			json.NewEncoder(w).Encode(map[string]string{"status": "ok", "name": body.Name})

		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/mcp/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		path := strings.TrimPrefix(r.URL.Path, "/mcp/")
		if path == "" {
			http.Error(w, `{"error":"name required"}`, http.StatusBadRequest)
			return
		}

		parts := strings.SplitN(path, "/", 2)
		name := parts[0]
		action := ""
		if len(parts) > 1 {
			action = parts[1]
		}

		switch {
		case action == "" && r.Method == "GET":
			raw, err := getMCPConfig(cfg, name)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusNotFound)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"name": name, "config": json.RawMessage(raw)})

		case action == "" && r.Method == "DELETE":
			configPath := findConfigPath()
			if err := deleteMCPConfig(cfg, configPath, name); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusNotFound)
				return
			}
			auditLog(cfg.HistoryDB, "mcp.delete", "http", name, clientIP(r))
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

		case action == "test" && r.Method == "POST":
			raw, err := getMCPConfig(cfg, name)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusNotFound)
				return
			}
			ok, output := testMCPConfig(raw)
			json.NewEncoder(w).Encode(map[string]any{"ok": ok, "output": output})

		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
	})

	// --- Agent Memory ---
	mux.HandleFunc("/memory", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case "GET":
			role := r.URL.Query().Get("role")
			entries, err := listMemory(cfg, role)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
				return
			}
			if entries == nil {
				entries = []MemoryEntry{}
			}
			json.NewEncoder(w).Encode(entries)

		case "POST":
			var body struct {
				Role  string `json:"role"`
				Key   string `json:"key"`
				Value string `json:"value"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
				return
			}
			if body.Role == "" || body.Key == "" {
				http.Error(w, `{"error":"role and key are required"}`, http.StatusBadRequest)
				return
			}
			if err := setMemory(cfg, body.Role, body.Key, body.Value); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
				return
			}
			auditLog(cfg.HistoryDB, "memory.set", "http",
				fmt.Sprintf("role=%s key=%s", body.Role, body.Key), clientIP(r))
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/memory/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Parse /memory/<role>/<key>
		path := strings.TrimPrefix(r.URL.Path, "/memory/")
		parts := strings.SplitN(path, "/", 2)
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			http.Error(w, `{"error":"path must be /memory/{role}/{key}"}`, http.StatusBadRequest)
			return
		}
		role := parts[0]
		key := parts[1]

		switch r.Method {
		case "GET":
			val, err := getMemory(cfg, role, key)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{
				"role": role, "key": key, "value": val,
			})

		case "DELETE":
			if err := deleteMemory(cfg, role, key); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
				return
			}
			auditLog(cfg.HistoryDB, "memory.delete", "http",
				fmt.Sprintf("role=%s key=%s", role, key), clientIP(r))
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
	})
}
