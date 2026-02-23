package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

func (s *Server) registerToolRoutes(mux *http.ServeMux) {
	cfg := s.cfg

	// --- Tool Engine ---
	mux.HandleFunc("/api/tools", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if cfg.toolRegistry == nil {
			json.NewEncoder(w).Encode([]any{})
			return
		}
		tools := cfg.toolRegistry.List()
		result := make([]map[string]any, 0, len(tools))
		for _, t := range tools {
			var schema map[string]any
			if len(t.InputSchema) > 0 {
				json.Unmarshal(t.InputSchema, &schema)
			}
			result = append(result, map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"inputSchema": schema,
				"builtin":     t.Builtin,
				"requireAuth": t.RequireAuth,
			})
		}
		json.NewEncoder(w).Encode(result)
	})

	// --- MCP Host ---
	mux.HandleFunc("/api/mcp/servers", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if s.mcpHost == nil {
			json.NewEncoder(w).Encode([]any{})
			return
		}
		statuses := s.mcpHost.ServerStatus()
		json.NewEncoder(w).Encode(statuses)
	})

	mux.HandleFunc("/api/mcp/servers/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		if s.mcpHost == nil {
			http.Error(w, `{"error":"MCP host not enabled"}`, http.StatusBadRequest)
			return
		}
		// Extract server name from path
		path := strings.TrimPrefix(r.URL.Path, "/api/mcp/servers/")
		parts := strings.Split(path, "/")
		if len(parts) != 2 || parts[1] != "restart" {
			http.Error(w, `{"error":"invalid path, use /api/mcp/servers/{name}/restart"}`, http.StatusBadRequest)
			return
		}
		serverName := parts[0]
		if err := s.mcpHost.RestartServer(serverName); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status": "restarted",
			"server": serverName,
		})
	})

	// --- Embedding / Semantic Search ---
	mux.HandleFunc("/api/embedding/search", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Query  string `json:"query"`
			Source string `json:"source"`
			TopK   int    `json:"topK"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
			return
		}
		if req.TopK <= 0 {
			req.TopK = 10
		}
		results, err := hybridSearch(r.Context(), cfg, req.Query, req.Source, req.TopK)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(results)
	})

	mux.HandleFunc("/api/embedding/reindex", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		if err := reindexAll(r.Context(), cfg); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"reindexing complete"}`))
	})

	mux.HandleFunc("/api/embedding/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		stats, err := embeddingStatus(cfg.HistoryDB)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(stats)
	})

	// --- Proactive Agent API ---
	mux.HandleFunc("/api/proactive/rules", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		if s.proactiveEngine == nil {
			http.Error(w, `{"error":"proactive engine not enabled"}`, http.StatusServiceUnavailable)
			return
		}
		rules := s.proactiveEngine.ListRules()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(rules)
	})

	mux.HandleFunc("/api/proactive/rules/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		if s.proactiveEngine == nil {
			http.Error(w, `{"error":"proactive engine not enabled"}`, http.StatusServiceUnavailable)
			return
		}

		// Extract rule name from path: /api/proactive/rules/{name}/trigger
		path := strings.TrimPrefix(r.URL.Path, "/api/proactive/rules/")
		parts := strings.Split(path, "/")
		if len(parts) != 2 || parts[1] != "trigger" {
			http.Error(w, `{"error":"invalid path, use /api/proactive/rules/{name}/trigger"}`, http.StatusBadRequest)
			return
		}

		ruleName := parts[0]
		if err := s.proactiveEngine.TriggerRule(ruleName); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(fmt.Sprintf(`{"status":"triggered","rule":"%s"}`, ruleName)))
	})

	// --- Group Chat ---
	mux.HandleFunc("/api/groupchat/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		if s.groupChatEngine == nil {
			http.Error(w, `{"error":"group chat engine not enabled"}`, http.StatusServiceUnavailable)
			return
		}
		status := s.groupChatEngine.Status()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(status)
	})

	// --- API Documentation ---
	mux.HandleFunc("/api/docs", handleAPIDocs)
	mux.HandleFunc("/api/spec", handleAPISpec(cfg))
}
