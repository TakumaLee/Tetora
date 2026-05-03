package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"tetora/internal/audit"
)

// MemoryDeps holds dependencies for memory and MCP config HTTP handlers.
type MemoryDeps struct {
	// MCP config operations.
	ListMCPConfigs  func() any
	GetMCPConfig    func(name string) (json.RawMessage, error)
	SetMCPConfig    func(configPath, name string, raw json.RawMessage) error
	DeleteMCPConfig func(configPath, name string) error
	TestMCPConfig   func(raw json.RawMessage) (bool, string)

	// Memory operations.
	ListMemory   func(role string) (any, error)
	GetMemory    func(role, key string) (string, error)
	SetMemory    func(agent, key, value string) error
	DeleteMemory func(role, key string) error

	FindConfigPath func() string
	HistoryDB      func() string
}

// RegisterMemoryRoutes registers MCP config and agent memory API routes.
func RegisterMemoryRoutes(mux *http.ServeMux, d MemoryDeps) {
	// --- MCP Configs ---
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case "GET":
			configs := d.ListMCPConfigs()
			if configs == nil {
				configs = []any{}
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
			configPath := d.FindConfigPath()
			if err := d.SetMCPConfig(configPath, body.Name, body.Config); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
				return
			}
			audit.Log(d.HistoryDB(), "mcp.save", "http", body.Name, clientIP(r))
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
			raw, err := d.GetMCPConfig(name)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusNotFound)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"name": name, "config": json.RawMessage(raw)})

		case action == "" && r.Method == "DELETE":
			configPath := d.FindConfigPath()
			if err := d.DeleteMCPConfig(configPath, name); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusNotFound)
				return
			}
			audit.Log(d.HistoryDB(), "mcp.delete", "http", name, clientIP(r))
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

		case action == "test" && r.Method == "POST":
			raw, err := d.GetMCPConfig(name)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusNotFound)
				return
			}
			ok, output := d.TestMCPConfig(raw)
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
			entries, err := d.ListMemory(role)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
				return
			}
			if entries == nil {
				entries = []any{}
			}
			json.NewEncoder(w).Encode(entries)

		case "POST":
			var body struct {
				Agent string `json:"agent"`
				Key   string `json:"key"`
				Value string `json:"value"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
				return
			}
			if body.Agent == "" || body.Key == "" {
				http.Error(w, `{"error":"agent and key are required"}`, http.StatusBadRequest)
				return
			}
			if err := d.SetMemory(body.Agent, body.Key, body.Value); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
				return
			}
			audit.Log(d.HistoryDB(), "memory.set", "http",
				fmt.Sprintf("agent=%s key=%s", body.Agent, body.Key), clientIP(r))
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/memory/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		path := strings.TrimPrefix(r.URL.Path, "/memory/")
		parts := strings.SplitN(path, "/", 2)
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			http.Error(w, `{"error":"path must be /memory/{agent}/{key}"}`, http.StatusBadRequest)
			return
		}
		role := parts[0]
		key := parts[1]

		switch r.Method {
		case "GET":
			val, err := d.GetMemory(role, key)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{
				"role": role, "key": key, "value": val,
			})

		case "DELETE":
			if err := d.DeleteMemory(role, key); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
				return
			}
			audit.Log(d.HistoryDB(), "memory.delete", "http",
				fmt.Sprintf("role=%s key=%s", role, key), clientIP(r))
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
	})
}
