package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"tetora/internal/team"
)

var teamNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9\-_]*$`)

// isValidTeamName returns true if name is a safe slug (no path traversal).
func isValidTeamName(name string) bool {
	return teamNameRe.MatchString(name)
}

// TeamDeps holds dependencies for team HTTP handlers.
type TeamDeps struct {
	Store         *team.Storage
	ConfigPath    string
	AgentsDir     string
	SignalReload  func()
	GenerateTeam  func(ctx context.Context, req team.GenerateRequest) (*team.TeamDef, error)
}

// RegisterTeamRoutes registers /api/teams/* endpoints on the given mux.
// It materializes builtin templates once at registration time.
func RegisterTeamRoutes(mux *http.ServeMux, d TeamDeps) {
	if err := d.Store.MaterializeBuiltins(); err != nil {
		fmt.Printf("warning: failed to materialize builtin teams: %v\n", err)
	}

	h := &teamHandler{d: d}

	mux.HandleFunc("/api/teams", h.handleTeams)
	mux.HandleFunc("/api/teams/generate", h.handleGenerate)
	mux.HandleFunc("/api/teams/", h.handleTeamByName)
}

type teamHandler struct {
	d TeamDeps
}

// handleTeams dispatches GET (list) and POST (save) on /api/teams.
func (h *teamHandler) handleTeams(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		h.listTeams(w, r)
	case http.MethodPost:
		h.saveTeam(w, r)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (h *teamHandler) listTeams(w http.ResponseWriter, r *http.Request) {
	teams, err := h.d.Store.List()
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
		return
	}

	type teamSummary struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Builtin     bool   `json:"builtin"`
		AgentCount  int    `json:"agentCount"`
	}

	out := make([]teamSummary, 0, len(teams))
	for _, t := range teams {
		out = append(out, teamSummary{
			Name:        t.Name,
			Description: t.Description,
			Builtin:     t.Builtin,
			AgentCount:  len(t.Agents),
		})
	}
	json.NewEncoder(w).Encode(out)
}

func (h *teamHandler) saveTeam(w http.ResponseWriter, r *http.Request) {
	var t team.TeamDef
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if t.Name == "" {
		http.Error(w, `{"error":"name required"}`, http.StatusBadRequest)
		return
	}
	if len(t.Agents) == 0 {
		http.Error(w, `{"error":"at least one agent required"}`, http.StatusBadRequest)
		return
	}

	t.Builtin = false
	if err := h.d.Store.Save(t); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{
		"name":       t.Name,
		"agentCount": len(t.Agents),
	})
}

// handleGenerate handles POST /api/teams/generate — AI generation without saving.
func (h *teamHandler) handleGenerate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
		return
	}

	if h.d.GenerateTeam == nil {
		http.Error(w, `{"error":"team generation not available (no provider configured)"}`, http.StatusServiceUnavailable)
		return
	}

	var req struct {
		Description string `json:"description"`
		Size        int    `json:"size"`
		Template    string `json:"template"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if req.Description == "" {
		http.Error(w, `{"error":"description required"}`, http.StatusBadRequest)
		return
	}

	td, err := h.d.GenerateTeam(r.Context(), team.GenerateRequest{
		Description: req.Description,
		Size:        req.Size,
		Template:    req.Template,
	})
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(td)
}

// handleTeamByName routes /api/teams/{name}[/apply].
func (h *teamHandler) handleTeamByName(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Extract name from path: /api/teams/{name}[/apply]
	path := strings.TrimPrefix(r.URL.Path, "/api/teams/")
	if path == "" {
		http.Error(w, `{"error":"team name required"}`, http.StatusBadRequest)
		return
	}

	// Check for /apply suffix.
	if strings.HasSuffix(path, "/apply") {
		name := strings.TrimSuffix(path, "/apply")
		if !isValidTeamName(name) {
			http.Error(w, `{"error":"invalid team name"}`, http.StatusBadRequest)
			return
		}
		h.applyTeam(w, r, name)
		return
	}

	name := path
	if !isValidTeamName(name) {
		http.Error(w, `{"error":"invalid team name"}`, http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.showTeam(w, r, name)
	case http.MethodPut:
		h.updateTeam(w, r, name)
	case http.MethodDelete:
		h.deleteTeam(w, r, name)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (h *teamHandler) showTeam(w http.ResponseWriter, _ *http.Request, name string) {
	t, err := h.d.Store.Load(name)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusNotFound)
		return
	}
	json.NewEncoder(w).Encode(t)
}

func (h *teamHandler) updateTeam(w http.ResponseWriter, r *http.Request, name string) {
	existing, err := h.d.Store.Load(name)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusNotFound)
		return
	}

	var updated team.TeamDef
	if err := json.NewDecoder(r.Body).Decode(&updated); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	// Preserve immutable fields.
	updated.Name = name
	updated.Builtin = existing.Builtin
	updated.CreatedAt = existing.CreatedAt

	if err := h.d.Store.Save(updated); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}

func (h *teamHandler) deleteTeam(w http.ResponseWriter, _ *http.Request, name string) {
	if err := h.d.Store.Delete(name); err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "builtin") {
			status = http.StatusConflict
		}
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), status)
		return
	}
	json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
}

func (h *teamHandler) applyTeam(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
		return
	}

	var opts struct {
		Force bool `json:"force"`
	}
	json.NewDecoder(r.Body).Decode(&opts)

	err := team.Apply(name, h.d.Store, h.d.ConfigPath, h.d.AgentsDir, team.ApplyOptions{
		Force: opts.Force,
	}, h.d.SignalReload)
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "already exists") {
			status = http.StatusConflict
		}
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), status)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"status": "applied"})
}
