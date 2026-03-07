package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func (s *Server) registerWorkersRoutes(mux *http.ServeMux) {
	// GET /api/workers — list all active tmux workers.
	mux.HandleFunc("/api/workers", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		sup := s.state.tmuxSupervisor
		if sup == nil {
			json.NewEncoder(w).Encode(map[string]any{"workers": []any{}, "count": 0})
			return
		}

		workers := sup.listWorkers()
		type workerInfo struct {
			Name    string `json:"name"`
			Agent   string `json:"agent"`
			State   string `json:"state"`
			TaskID  string `json:"taskId"`
			Prompt  string `json:"prompt"`
			Workdir string `json:"workdir"`
			Uptime  string `json:"uptime"`
		}
		out := make([]workerInfo, 0, len(workers))
		for _, w := range workers {
			out = append(out, workerInfo{
				Name:    w.TmuxName,
				Agent:   w.Agent,
				State:   w.State.String(),
				TaskID:  w.TaskID,
				Prompt:  w.Prompt,
				Workdir: w.Workdir,
				Uptime:  time.Since(w.CreatedAt).Round(time.Second).String(),
			})
		}
		json.NewEncoder(w).Encode(map[string]any{"workers": out, "count": len(out)})
	})

	// GET /api/workers/capture?name=<tmuxName> — capture terminal screen.
	mux.HandleFunc("/api/workers/capture", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		name := r.URL.Query().Get("name")
		if name == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "missing name parameter"})
			return
		}

		sup := s.state.tmuxSupervisor
		if sup == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"error": "supervisor not available"})
			return
		}

		worker := sup.getWorker(name)
		if worker == nil {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("worker %q not found", name)})
			return
		}

		capture, err := tmuxCapture(name)
		if err != nil {
			// Tmux session is gone — unregister the stale worker.
			if !tmuxHasSession(name) {
				sup.unregister(name)
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("worker %q session ended", name)})
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("capture failed: %v", err)})
			return
		}

		// Update worker state from capture (covers recovered workers with no polling goroutine).
		profile := &claudeTmuxProfile{}
		if newState := profile.DetectState(capture); newState != worker.State {
			worker.State = newState
			worker.LastChanged = time.Now()
		}

		// Strip ANSI escape sequences.
		cleaned := ansiEscapeRe.ReplaceAllString(capture, "")

		resp := map[string]any{
			"name":    name,
			"state":   worker.State.String(),
			"agent":   worker.Agent,
			"capture": cleaned,
		}

		// Parse question info if in question state.
		if worker.State == tmuxStateQuestion {
			if q, opts := parseQuestionFromCapture(cleaned); q != "" {
				resp["question"] = q
				resp["options"] = opts
			}
		}

		// Parse subagent info from capture.
		if subs := parseSubagentsFromCapture(cleaned); len(subs) > 0 {
			resp["subagents"] = subs
		}

		json.NewEncoder(w).Encode(resp)
	})

	// POST /api/workers/terminal — enable/disable terminal mode for an agent.
	// Body: {"agent": "ruri", "enabled": true}
	mux.HandleFunc("/api/workers/terminal", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		var req struct {
			Agent   string `json:"agent"`
			Enabled bool   `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
			return
		}
		if req.Agent == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "agent name required"})
			return
		}

		cfg := s.cfg
		rc, ok := cfg.Agents[req.Agent]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("agent %q not found", req.Agent)})
			return
		}

		configPath := findConfigPath()

		if req.Enabled {
			// Ensure tmux is installed.
			if err := ensureTmux(); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("tmux install failed: %v", err)})
				return
			}

			// Add claude-tmux provider if not present.
			if _, exists := cfg.Providers["claude-tmux"]; !exists {
				claudePath := "/usr/local/bin/claude"
				if cfg.ClaudePath != "" {
					claudePath = cfg.ClaudePath
				}
				updateConfigField(configPath, func(raw map[string]any) {
					providers, _ := raw["providers"].(map[string]any)
					if providers == nil {
						providers = make(map[string]any)
						raw["providers"] = providers
					}
					providers["claude-tmux"] = map[string]any{
						"type":             "claude-tmux",
						"path":             claudePath,
						"tmuxKeepSessions": true,
					}
				})
				if cfg.Providers == nil {
					cfg.Providers = make(map[string]ProviderConfig)
				}
				provCfg := ProviderConfig{Type: "claude-tmux", Path: claudePath, TmuxKeepSessions: true}
				cfg.Providers["claude-tmux"] = provCfg
				if cfg.registry != nil {
					cfg.registry.register("claude-tmux", &TmuxProvider{
						binaryPath: claudePath,
						cfg:        cfg,
						provCfg:    provCfg,
						supervisor: cfg.tmuxSupervisor,
						profile:    &claudeTmuxProfile{},
					})
				}
			}

			// Switch agent provider.
			oldProvider := rc.Provider
			rc.Provider = "claude-tmux"
			cfg.Agents[req.Agent] = rc
			updateConfigAgents(configPath, req.Agent, &rc)
			json.NewEncoder(w).Encode(map[string]any{
				"ok": true, "agent": req.Agent, "provider": "claude-tmux", "previous": oldProvider,
			})
		} else {
			// Switch back to default.
			oldProvider := rc.Provider
			fallback := cfg.DefaultProvider
			if fallback == "" || strings.HasSuffix(fallback, "-tmux") {
				fallback = "claude"
			}
			rc.Provider = fallback
			cfg.Agents[req.Agent] = rc
			updateConfigAgents(configPath, req.Agent, &rc)
			json.NewEncoder(w).Encode(map[string]any{
				"ok": true, "agent": req.Agent, "provider": fallback, "previous": oldProvider,
			})
		}
	})

	// POST /api/workers/input — send input to a tmux worker.
	// Body: {"name": "tetora-worker-xxx", "type": "keys"|"text", "value": "Enter"}
	mux.HandleFunc("/api/workers/input", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		var req struct {
			Name  string `json:"name"`
			Type  string `json:"type"`  // "keys" or "text"
			Value string `json:"value"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
			return
		}
		if req.Name == "" || req.Value == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "name and value required"})
			return
		}

		sup := s.state.tmuxSupervisor
		if sup == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"error": "supervisor not available"})
			return
		}
		if sup.getWorker(req.Name) == nil {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("worker %q not found", req.Name)})
			return
		}

		switch req.Type {
		case "keys":
			// Whitelist allowed key names.
			allowed := map[string]bool{
				"Enter": true, "Up": true, "Down": true, "Left": true, "Right": true,
				"Tab": true, "Escape": true, "Space": true, "BSpace": true,
				"y": true, "Y": true, "n": true, "N": true,
				"C-c": true, "C-d": true, "C-z": true, "C-l": true,
				"1": true, "2": true, "3": true, "4": true,
			}
			if !allowed[req.Value] {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("key %q not allowed", req.Value)})
				return
			}
			if err := tmuxSendKeys(req.Name, req.Value); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("send keys: %v", err)})
				return
			}
		case "text":
			if len(req.Value) > 10000 {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": "text too long (max 10000)"})
				return
			}
			if err := tmuxSendText(req.Name, req.Value); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("send text: %v", err)})
				return
			}
			if err := tmuxSendKeys(req.Name, "Enter"); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("send enter: %v", err)})
				return
			}
		default:
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "type must be \"keys\" or \"text\""})
			return
		}

		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})

	// GET /api/workers/agents — list agents with their terminal (tmux) status.
	mux.HandleFunc("/api/workers/agents", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		type agentTerminalInfo struct {
			Name     string `json:"name"`
			Provider string `json:"provider"`
			Model    string `json:"model"`
			Terminal bool   `json:"terminal"`
		}
		cfg := s.cfg
		agents := make([]agentTerminalInfo, 0, len(cfg.Agents))
		for name, rc := range cfg.Agents {
			p := rc.Provider
			if p == "" {
				p = cfg.DefaultProvider
			}
			if p == "" {
				p = "claude"
			}
			agents = append(agents, agentTerminalInfo{
				Name:     name,
				Provider: p,
				Model:    rc.Model,
				Terminal: strings.HasSuffix(p, "-tmux"),
			})
		}
		json.NewEncoder(w).Encode(map[string]any{"agents": agents})
	})

	// GET/PATCH /api/settings/discord — Discord display settings.
	mux.HandleFunc("/api/settings/discord", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case http.MethodGet:
			showProgress := s.cfg.Discord.ShowProgress == nil || *s.cfg.Discord.ShowProgress
			json.NewEncoder(w).Encode(map[string]any{
				"showProgress": showProgress,
			})

		case http.MethodPatch:
			var body struct {
				ShowProgress *bool `json:"showProgress"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": "invalid body"})
				return
			}
			if body.ShowProgress != nil {
				s.cfg.Discord.ShowProgress = body.ShowProgress
				configPath := findConfigPath()
				if configPath != "" {
					updateConfigField(configPath, func(raw map[string]any) {
						disc, _ := raw["discord"].(map[string]any)
						if disc == nil {
							disc = map[string]any{}
							raw["discord"] = disc
						}
						disc["showProgress"] = *body.ShowProgress
					})
				}
			}
			showProgress := s.cfg.Discord.ShowProgress == nil || *s.cfg.Discord.ShowProgress
			json.NewEncoder(w).Encode(map[string]any{"showProgress": showProgress})

		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
}
