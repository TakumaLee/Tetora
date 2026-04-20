package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"tetora/internal/audit"
	"tetora/internal/httputil"
)

// SessionDeps holds all dependencies for session and skill HTTP handlers.
type SessionDeps struct {
	HistoryDB string

	// QuerySessions returns paginated sessions as JSON-serializable value plus total count.
	QuerySessions func(role, status, source string, limit, offset int) (sessions any, total int, err error)

	// CreateSession creates a new session. agent must exist; returns JSON-serializable session or error.
	// ip is the client IP for audit logging.
	CreateSession func(agent, title, ip string) (any, error)

	// GetSessionDetail returns session detail with messages, or nil if not found.
	GetSessionDetail func(id string) (any, error)

	// ArchiveSession sets a session's status to archived.
	ArchiveSession func(id string) error

	// SendMessage sends a message to a session. async=true returns immediately without waiting for the result.
	// Returns a JSON-serializable response and HTTP status code.
	SendMessage func(r *http.Request, sessionID, prompt string, async bool) (any, int, error)

	// MirrorMessage records an external message in a session (no task execution).
	// The body contains the raw JSON payload. Returns JSON-serializable response and HTTP status code.
	MirrorMessage func(r *http.Request, sessionID string, body json.RawMessage) (any, int, error)

	// CompactSession triggers context compaction for a session (fire-and-forget).
	CompactSession func(sessionID string)

	// SearchHistory returns archived + active messages for a session matching
	// an optional query substring. Used by tetora_search_history MCP tool.
	SearchHistory func(sessionID, query string, limit int) (any, error)

	// ServeSSE serves a one-shot SSE stream for a session.
	ServeSSE func(w http.ResponseWriter, r *http.Request, sessionID string)

	// ServeSSEPersistent serves a persistent SSE stream that stays open across tasks.
	ServeSSEPersistent func(w http.ResponseWriter, r *http.Request, sessionID string)

	// SSEAvailable reports whether the SSE broker is running.
	SSEAvailable func() bool

	// ListSkills returns all available skills as a JSON-serializable value.
	ListSkills func() any

	// RunSkill executes a skill with the given variables.
	RunSkill func(r *http.Request, name string, vars map[string]string) (any, error)

	// TestSkill runs a skill in test mode.
	TestSkill func(r *http.Request, name string) (any, error)

	// SkillExists returns true if a skill with the given name exists.
	SkillExists func(name string) bool

	// AgentExists returns true if an agent with the given name exists in the config.
	AgentExists func(name string) bool
}

func clientIPFromRequest(r *http.Request) string { return httputil.ClientIP(r) }

// RegisterSessionRoutes registers session and skill routes on the given mux.
func RegisterSessionRoutes(mux *http.ServeMux, d SessionDeps) {
	// --- Sessions ---
	mux.HandleFunc("/sessions", func(w http.ResponseWriter, r *http.Request) {
		if d.HistoryDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case http.MethodGet:
			role := r.URL.Query().Get("role")
			status := r.URL.Query().Get("status")
			source := r.URL.Query().Get("source")
			limit := 20
			offset := 0

			if l := r.URL.Query().Get("limit"); l != "" {
				if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 100 {
					limit = n
				}
			}
			if p := r.URL.Query().Get("page"); p != "" {
				if n, err := strconv.Atoi(p); err == nil && n > 1 {
					offset = (n - 1) * limit
				}
			}

			sessions, total, err := d.QuerySessions(role, status, source, limit, offset)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			if sessions == nil {
				sessions = []any{}
			}
			page := (offset/limit) + 1
			json.NewEncoder(w).Encode(map[string]any{
				"sessions": sessions,
				"total":    total,
				"page":     page,
				"limit":    limit,
			})

		case http.MethodPost:
			var body struct {
				Agent string `json:"agent"`
				Title string `json:"title"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
				return
			}
			if body.Agent == "" {
				http.Error(w, `{"error":"agent is required"}`, http.StatusBadRequest)
				return
			}
			if !d.AgentExists(body.Agent) {
				http.Error(w, `{"error":"agent not found"}`, http.StatusBadRequest)
				return
			}
			sess, err := d.CreateSession(body.Agent, body.Title, clientIPFromRequest(r))
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(sess)

		default:
			http.Error(w, `{"error":"GET or POST only"}`, http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/sessions/", func(w http.ResponseWriter, r *http.Request) {
		if d.HistoryDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		path := strings.TrimPrefix(r.URL.Path, "/sessions/")
		if path == "" {
			http.Error(w, `{"error":"session id required"}`, http.StatusBadRequest)
			return
		}

		parts := strings.SplitN(path, "/", 2)
		sessionID := parts[0]
		action := ""
		if len(parts) > 1 {
			action = parts[1]
		}

		switch {
		// GET /sessions/{id}/stream — SSE stream for session events.
		case action == "stream" && r.Method == http.MethodGet:
			if !d.SSEAvailable() {
				http.Error(w, `{"error":"streaming not available"}`, http.StatusServiceUnavailable)
				return
			}
			d.ServeSSE(w, r, sessionID)
			return

		// GET /sessions/{id}/watch — persistent SSE stream (stays open across tasks).
		case action == "watch" && r.Method == http.MethodGet:
			if !d.SSEAvailable() {
				http.Error(w, `{"error":"streaming not available"}`, http.StatusServiceUnavailable)
				return
			}
			d.ServeSSEPersistent(w, r, sessionID)
			return

		// GET /sessions/{id} — get session with messages.
		case action == "" && r.Method == http.MethodGet:
			detail, err := d.GetSessionDetail(sessionID)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			if detail == nil {
				http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
				return
			}
			json.NewEncoder(w).Encode(detail)

		// DELETE /sessions/{id} — archive session.
		case action == "" && r.Method == http.MethodDelete:
			if err := d.ArchiveSession(sessionID); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			audit.Log(d.HistoryDB, "session.archive", "http",
				fmt.Sprintf("session=%s", sessionID), clientIPFromRequest(r))
			w.Write([]byte(`{"status":"archived"}`))

		// POST /sessions/{id}/message — continue a session.
		case action == "message" && r.Method == http.MethodPost:
			var body struct {
				Prompt string `json:"prompt"`
				Async  bool   `json:"async"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Prompt == "" {
				http.Error(w, `{"error":"prompt is required"}`, http.StatusBadRequest)
				return
			}
			resp, code, err := d.SendMessage(r, sessionID, body.Prompt, body.Async)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), code)
				return
			}
			w.WriteHeader(code)
			json.NewEncoder(w).Encode(resp)

		// POST /sessions/{id}/mirror — record external message (no task execution).
		case action == "mirror" && r.Method == http.MethodPost:
			raw, err := readBody(r)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
				return
			}
			resp, code, err := d.MirrorMessage(r, sessionID, raw)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), code)
				return
			}
			w.WriteHeader(code)
			json.NewEncoder(w).Encode(resp)

		// POST /sessions/{id}/compact — trigger context compaction.
		case action == "compact" && r.Method == http.MethodPost:
			d.CompactSession(sessionID)
			audit.Log(d.HistoryDB, "session.compact", "http",
				fmt.Sprintf("session=%s", sessionID), clientIPFromRequest(r))
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]string{"status": "compacting"})

		// GET /sessions/{id}/history — search archived + active messages.
		case action == "history" && r.Method == http.MethodGet:
			if d.SearchHistory == nil {
				http.Error(w, `{"error":"history search not available"}`, http.StatusServiceUnavailable)
				return
			}
			q := r.URL.Query().Get("q")
			limit := 20
			if l := r.URL.Query().Get("limit"); l != "" {
				if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 100 {
					limit = n
				}
			}
			msgs, err := d.SearchHistory(sessionID, q, limit)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			if msgs == nil {
				msgs = []any{}
			}
			json.NewEncoder(w).Encode(map[string]any{
				"session_id": sessionID,
				"query":      q,
				"limit":      limit,
				"messages":   msgs,
			})

		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
	})

	// --- Skills ---
	mux.HandleFunc("/skills", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(d.ListSkills())
	})

	mux.HandleFunc("/skills/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Parse /skills/<name>/<action>
		path := strings.TrimPrefix(r.URL.Path, "/skills/")
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

		if !d.SkillExists(name) {
			http.Error(w, fmt.Sprintf(`{"error":"skill %q not found"}`, name), http.StatusNotFound)
			return
		}

		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}

		switch action {
		case "run":
			var body struct {
				Vars map[string]string `json:"vars"`
			}
			json.NewDecoder(r.Body).Decode(&body)

			audit.Log(d.HistoryDB, "skill.run", "http",
				fmt.Sprintf("name=%s", name), clientIPFromRequest(r))

			result, err := d.RunSkill(r, name, body.Vars)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(result)

		case "test":
			audit.Log(d.HistoryDB, "skill.test", "http",
				fmt.Sprintf("name=%s", name), clientIPFromRequest(r))

			result, err := d.TestSkill(r, name)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(result)

		default:
			http.Error(w, `{"error":"unknown action, use run or test"}`, http.StatusBadRequest)
		}
	})
}
