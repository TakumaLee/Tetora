package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// ToolsDeps holds dependencies for tools, MCP, embedding, proactive, and groupchat HTTP handlers.
type ToolsDeps struct {
	ListTools      func() any
	ExecuteTool    func(ctx context.Context, name string, input json.RawMessage) (string, error)
	MCPStatus      func() any
	MCPRestart     func(name string) error
	HybridSearch   func(ctx context.Context, query, source string, topK int) (any, error)
	ReindexAll     func(ctx context.Context) error
	EmbeddingStatus func() (any, error)
	ProactiveEnabled bool
	ListProactiveRules func() any
	TriggerProactiveRule func(name string) error
	GroupChatEnabled bool
	GroupChatStatus  func() any
	HandleAPIDocs    http.HandlerFunc
	HandleAPISpec    http.HandlerFunc
}

// RegisterToolRoutes registers tool, MCP, embedding, proactive, and groupchat API routes.
func RegisterToolRoutes(mux *http.ServeMux, d ToolsDeps) {
	mux.HandleFunc("/api/tools", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		tools := d.ListTools()
		if tools == nil {
			tools = []any{}
		}
		json.NewEncoder(w).Encode(tools)
	})

	// POST /api/tools/execute — Execute a registered tool by name.
	mux.HandleFunc("/api/tools/execute", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		if d.ExecuteTool == nil {
			http.Error(w, `{"error":"tool execution not available"}`, http.StatusServiceUnavailable)
			return
		}
		var req struct {
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
			return
		}
		if req.Name == "" {
			http.Error(w, `{"error":"name is required"}`, http.StatusBadRequest)
			return
		}
		output, err := d.ExecuteTool(r.Context(), req.Name, req.Input)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(output))
	})

	mux.HandleFunc("/api/mcp/servers", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		statuses := d.MCPStatus()
		if statuses == nil {
			statuses = []any{}
		}
		json.NewEncoder(w).Encode(statuses)
	})

	mux.HandleFunc("/api/mcp/servers/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/api/mcp/servers/")
		parts := strings.Split(path, "/")
		if len(parts) != 2 || parts[1] != "restart" {
			http.Error(w, `{"error":"invalid path, use /api/mcp/servers/{name}/restart"}`, http.StatusBadRequest)
			return
		}
		serverName := parts[0]
		if err := d.MCPRestart(serverName); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status": "restarted",
			"server": serverName,
		})
	})

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
		results, err := d.HybridSearch(r.Context(), req.Query, req.Source, req.TopK)
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
		if err := d.ReindexAll(r.Context()); err != nil {
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
		stats, err := d.EmbeddingStatus()
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(stats)
	})

	mux.HandleFunc("/api/proactive/rules", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		if !d.ProactiveEnabled {
			http.Error(w, `{"error":"proactive engine not enabled"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(d.ListProactiveRules())
	})

	mux.HandleFunc("/api/proactive/rules/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		if !d.ProactiveEnabled {
			http.Error(w, `{"error":"proactive engine not enabled"}`, http.StatusServiceUnavailable)
			return
		}

		path := strings.TrimPrefix(r.URL.Path, "/api/proactive/rules/")
		parts := strings.Split(path, "/")
		if len(parts) != 2 || parts[1] != "trigger" {
			http.Error(w, `{"error":"invalid path, use /api/proactive/rules/{name}/trigger"}`, http.StatusBadRequest)
			return
		}

		ruleName := parts[0]
		if err := d.TriggerProactiveRule(ruleName); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(fmt.Sprintf(`{"status":"triggered","rule":"%s"}`, ruleName)))
	})

	mux.HandleFunc("/api/groupchat/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		if !d.GroupChatEnabled {
			http.Error(w, `{"error":"group chat engine not enabled"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(d.GroupChatStatus())
	})

	mux.HandleFunc("/api/openapi", d.HandleAPIDocs)
	mux.HandleFunc("/api/spec", d.HandleAPISpec)
}
