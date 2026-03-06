package main

import (
	"encoding/json"
	"fmt"
	"net/http"
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
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("capture failed: %v", err)})
			return
		}

		// Strip ANSI escape sequences.
		cleaned := ansiEscapeRe.ReplaceAllString(capture, "")

		json.NewEncoder(w).Encode(map[string]any{
			"name":    name,
			"state":   worker.State.String(),
			"agent":   worker.Agent,
			"capture": cleaned,
		})
	})
}
