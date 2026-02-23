package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

func (s *Server) registerCronRoutes(mux *http.ServeMux) {
	cfg := s.cfg
	cron := s.cron

	// --- Cron: list + create ---
	mux.HandleFunc("/cron", func(w http.ResponseWriter, r *http.Request) {
		if cron == nil {
			http.Error(w, `{"error":"cron not available"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case http.MethodGet:
			json.NewEncoder(w).Encode(cron.ListJobs())

		case http.MethodPost:
			var jc CronJobConfig
			if err := json.NewDecoder(r.Body).Decode(&jc); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
				return
			}
			if jc.ID == "" || jc.Schedule == "" {
				http.Error(w, `{"error":"id and schedule are required"}`, http.StatusBadRequest)
				return
			}
			if err := cron.AddJob(jc); err != nil {
				code := http.StatusBadRequest
				if strings.Contains(err.Error(), "already exists") {
					code = http.StatusConflict
				}
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), code)
				return
			}
			auditLog(cfg.HistoryDB, "job.create", "http",
				fmt.Sprintf("id=%s schedule=%s", jc.ID, jc.Schedule), clientIP(r))
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"status":"created"}`))

		default:
			http.Error(w, "GET or POST only", http.StatusMethodNotAllowed)
		}
	})

	// --- Cron: per-job actions ---
	mux.HandleFunc("/cron/", func(w http.ResponseWriter, r *http.Request) {
		if cron == nil {
			http.Error(w, `{"error":"cron not available"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		// Parse /cron/<id>/<action>
		path := strings.TrimPrefix(r.URL.Path, "/cron/")
		parts := strings.SplitN(path, "/", 2)
		id := parts[0]
		action := ""
		if len(parts) > 1 {
			action = parts[1]
		}

		switch {
		// GET /cron/<id> — get full job config
		case action == "" && r.Method == http.MethodGet:
			jc := cron.GetJobConfig(id)
			if jc == nil {
				http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
				return
			}
			json.NewEncoder(w).Encode(jc)

		// PUT /cron/<id> — update job
		case action == "" && r.Method == http.MethodPut:
			var jc CronJobConfig
			if err := json.NewDecoder(r.Body).Decode(&jc); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
				return
			}
			if jc.Schedule == "" {
				http.Error(w, `{"error":"schedule is required"}`, http.StatusBadRequest)
				return
			}
			if err := cron.UpdateJob(id, jc); err != nil {
				code := http.StatusBadRequest
				if strings.Contains(err.Error(), "not found") {
					code = http.StatusNotFound
				}
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), code)
				return
			}
			auditLog(cfg.HistoryDB, "job.update", "http",
				fmt.Sprintf("id=%s", id), clientIP(r))
			w.Write([]byte(`{"status":"updated"}`))

		// DELETE /cron/<id> — remove job
		case action == "" && r.Method == http.MethodDelete:
			if err := cron.RemoveJob(id); err != nil {
				code := http.StatusBadRequest
				if strings.Contains(err.Error(), "not found") {
					code = http.StatusNotFound
				}
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), code)
				return
			}
			auditLog(cfg.HistoryDB, "job.delete", "http",
				fmt.Sprintf("id=%s", id), clientIP(r))
			w.Write([]byte(`{"status":"removed"}`))

		// POST /cron/<id>/toggle
		case action == "toggle" && r.Method == http.MethodPost:
			var body struct {
				Enabled bool `json:"enabled"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			if err := cron.ToggleJob(id, body.Enabled); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusNotFound)
				return
			}
			auditLog(cfg.HistoryDB, "job.toggle", "http",
				fmt.Sprintf("id=%s enabled=%v", id, body.Enabled), clientIP(r))
			w.Write([]byte(fmt.Sprintf(`{"status":"ok","enabled":%v}`, body.Enabled)))

		// POST /cron/<id>/approve
		case action == "approve" && r.Method == http.MethodPost:
			if err := cron.ApproveJob(id); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
				return
			}
			auditLog(cfg.HistoryDB, "job.approve", "http",
				fmt.Sprintf("id=%s", id), clientIP(r))
			w.Write([]byte(`{"status":"approved"}`))

		// POST /cron/<id>/reject
		case action == "reject" && r.Method == http.MethodPost:
			if err := cron.RejectJob(id); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
				return
			}
			auditLog(cfg.HistoryDB, "job.reject", "http",
				fmt.Sprintf("id=%s", id), clientIP(r))
			w.Write([]byte(`{"status":"rejected"}`))

		// POST /cron/<id>/run
		case action == "run" && r.Method == http.MethodPost:
			if err := cron.RunJobByID(r.Context(), id); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
				return
			}
			auditLog(cfg.HistoryDB, "job.trigger", "http",
				fmt.Sprintf("id=%s", id), clientIP(r))
			w.Write([]byte(`{"status":"triggered"}`))

		default:
			http.Error(w, `{"error":"unknown action"}`, http.StatusBadRequest)
		}
	})
}
