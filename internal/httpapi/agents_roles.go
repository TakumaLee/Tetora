package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"tetora/internal/audit"
	"tetora/internal/httputil"
)

// ArchetypeInfo describes a builtin agent archetype.
type ArchetypeInfo struct {
	Name           string `json:"name"`
	Description    string `json:"description"`
	Model          string `json:"model"`
	PermissionMode string `json:"permissionMode"`
	SoulTemplate   string `json:"soulTemplate"`
}

// AgentInfo describes a configured agent (list view).
type AgentInfo struct {
	Name           string `json:"name"`
	Model          string `json:"model"`
	Provider       string `json:"provider,omitempty"`
	PermissionMode string `json:"permissionMode,omitempty"`
	SoulFile       string `json:"soulFile"`
	Description    string `json:"description"`
	SoulPreview    string `json:"soulPreview,omitempty"`
	PortraitURL    string `json:"portraitURL,omitempty"`
}

// AgentRoleDeps holds dependencies for agent role HTTP handlers.
type AgentRoleDeps struct {
	// ListArchetypes returns the builtin archetypes.
	ListArchetypes func() []ArchetypeInfo

	// ListAgents returns all configured agents (list view with soul preview).
	ListAgents func() []AgentInfo

	// AgentExists checks if an agent name exists.
	AgentExists func(name string) bool

	// GetAgent returns agent detail with full soul content, or (nil, false).
	GetAgent func(name string) (map[string]any, bool)

	// CreateAgent creates a new agent. Returns error on failure.
	CreateAgent func(name, model, permMode, desc, soulFile, soulContent string) error

	// UpdateAgent updates an existing agent. Returns error on failure.
	UpdateAgent func(name, model, permMode, desc, soulFile, soulContent, providerName string) error

	// DeleteAgent deletes an agent. Returns (error, conflict) — conflict=true means
	// the agent is in use by a cron job (HTTP 409); other errors yield HTTP 500.
	DeleteAgent func(name string) (err error, conflict bool)

	// HistoryDB returns the history DB path for audit logging.
	HistoryDB func() string
}

// RegisterAgentRoleRoutes registers HTTP routes for agent role management.
func RegisterAgentRoleRoutes(mux *http.ServeMux, d AgentRoleDeps) {
	h := &agentRoleHandler{d: d}
	mux.HandleFunc("/roles/archetypes", h.handleArchetypes)
	mux.HandleFunc("/roles", h.handleRoles)
	mux.HandleFunc("/roles/", h.handleRole)
}

type agentRoleHandler struct {
	d AgentRoleDeps
}

func (h *agentRoleHandler) handleArchetypes(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	archs := h.d.ListArchetypes()
	if archs == nil {
		archs = []ArchetypeInfo{}
	}
	json.NewEncoder(w).Encode(archs)
}

func (h *agentRoleHandler) handleRoles(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		roles := h.d.ListAgents()
		if roles == nil {
			roles = []AgentInfo{}
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
		if h.d.AgentExists(body.Name) {
			http.Error(w, `{"error":"agent already exists"}`, http.StatusConflict)
			return
		}

		// Default soul file name if not specified.
		if body.SoulFile == "" {
			body.SoulFile = fmt.Sprintf("SOUL-%s.md", body.Name)
		}

		if err := h.d.CreateAgent(body.Name, body.Model, body.PermissionMode, body.Description, body.SoulFile, body.SoulContent); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}

		audit.Log(h.d.HistoryDB(), "agent.create", "http",
			fmt.Sprintf("name=%s", body.Name), httputil.ClientIP(r))
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"status":"created"}`))

	default:
		http.Error(w, "GET or POST only", http.StatusMethodNotAllowed)
	}
}

func (h *agentRoleHandler) handleRole(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Parse /roles/<name> — skip the archetypes sub-path (handled by other handler).
	path := strings.TrimPrefix(r.URL.Path, "/roles/")
	if path == "" || path == "archetypes" {
		return
	}
	name := path

	switch r.Method {
	case http.MethodGet:
		result, ok := h.d.GetAgent(name)
		if !ok {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(result)

	case http.MethodPut:
		if !h.d.AgentExists(name) {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}
		var body struct {
			Model          string `json:"model"`
			PermissionMode string `json:"permissionMode"`
			Description    string `json:"description"`
			SoulFile       string `json:"soulFile"`
			SoulContent    string `json:"soulContent"`
			Provider       string `json:"provider"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
			return
		}

		if err := h.d.UpdateAgent(name, body.Model, body.PermissionMode, body.Description, body.SoulFile, body.SoulContent, body.Provider); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}

		audit.Log(h.d.HistoryDB(), "agent.update", "http",
			fmt.Sprintf("name=%s", name), httputil.ClientIP(r))
		w.Write([]byte(`{"status":"updated"}`))

	case http.MethodDelete:
		if !h.d.AgentExists(name) {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}

		err, conflict := h.d.DeleteAgent(name)
		if err != nil {
			if conflict {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusConflict)
			} else {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			}
			return
		}

		audit.Log(h.d.HistoryDB(), "agent.delete", "http",
			fmt.Sprintf("name=%s", name), httputil.ClientIP(r))
		w.Write([]byte(`{"status":"deleted"}`))

	default:
		http.Error(w, "GET, PUT or DELETE only", http.StatusMethodNotAllowed)
	}
}
