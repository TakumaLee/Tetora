package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"tetora/internal/audit"
)

// CronDeps holds dependencies for cron HTTP handlers.
type CronDeps struct {
	Available    bool
	ListJobs     func() any
	AddJob       func(raw json.RawMessage) error // decode + add
	GetJobConfig func(id string) any
	UpdateJob    func(id string, raw json.RawMessage) error
	RemoveJob    func(id string) error
	ToggleJob    func(id string, enabled bool) error
	ApproveJob   func(id string) error
	RejectJob    func(id string) error
	RunJob       func(ctx context.Context, id string) error
	HistoryDB    func() string
}

// RegisterCronRoutes registers cron job CRUD API routes.
func RegisterCronRoutes(mux *http.ServeMux, d CronDeps) {
	mux.HandleFunc("/cron", func(w http.ResponseWriter, r *http.Request) {
		if !d.Available {
			http.Error(w, `{"error":"cron not available"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case http.MethodGet:
			json.NewEncoder(w).Encode(d.ListJobs())

		case http.MethodPost:
			body, err := readBody(r)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
				return
			}
			if err := d.AddJob(body); err != nil {
				code := http.StatusBadRequest
				if strings.Contains(err.Error(), "already exists") {
					code = http.StatusConflict
				}
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), code)
				return
			}
			// Extract id+schedule for audit.
			var info struct {
				ID       string `json:"id"`
				Schedule string `json:"schedule"`
			}
			json.Unmarshal(body, &info)
			audit.Log(d.HistoryDB(), "job.create", "http",
				fmt.Sprintf("id=%s schedule=%s", info.ID, info.Schedule), clientIP(r))
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"status":"created"}`))

		default:
			http.Error(w, "GET or POST only", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/cron/", func(w http.ResponseWriter, r *http.Request) {
		if !d.Available {
			http.Error(w, `{"error":"cron not available"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		path := strings.TrimPrefix(r.URL.Path, "/cron/")
		parts := strings.SplitN(path, "/", 2)
		id := parts[0]
		action := ""
		if len(parts) > 1 {
			action = parts[1]
		}

		switch {
		case action == "" && r.Method == http.MethodGet:
			jc := d.GetJobConfig(id)
			if jc == nil {
				http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
				return
			}
			json.NewEncoder(w).Encode(jc)

		case action == "" && r.Method == http.MethodPut:
			body, err := readBody(r)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
				return
			}
			if err := d.UpdateJob(id, body); err != nil {
				code := http.StatusBadRequest
				if strings.Contains(err.Error(), "not found") {
					code = http.StatusNotFound
				}
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), code)
				return
			}
			audit.Log(d.HistoryDB(), "job.update", "http",
				fmt.Sprintf("id=%s", id), clientIP(r))
			w.Write([]byte(`{"status":"updated"}`))

		case action == "" && r.Method == http.MethodDelete:
			if err := d.RemoveJob(id); err != nil {
				code := http.StatusBadRequest
				if strings.Contains(err.Error(), "not found") {
					code = http.StatusNotFound
				}
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), code)
				return
			}
			audit.Log(d.HistoryDB(), "job.delete", "http",
				fmt.Sprintf("id=%s", id), clientIP(r))
			w.Write([]byte(`{"status":"removed"}`))

		case action == "toggle" && r.Method == http.MethodPost:
			var body struct {
				Enabled bool `json:"enabled"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			if err := d.ToggleJob(id, body.Enabled); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusNotFound)
				return
			}
			audit.Log(d.HistoryDB(), "job.toggle", "http",
				fmt.Sprintf("id=%s enabled=%v", id, body.Enabled), clientIP(r))
			w.Write([]byte(fmt.Sprintf(`{"status":"ok","enabled":%v}`, body.Enabled)))

		case action == "approve" && r.Method == http.MethodPost:
			if err := d.ApproveJob(id); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
				return
			}
			audit.Log(d.HistoryDB(), "job.approve", "http",
				fmt.Sprintf("id=%s", id), clientIP(r))
			w.Write([]byte(`{"status":"approved"}`))

		case action == "reject" && r.Method == http.MethodPost:
			if err := d.RejectJob(id); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
				return
			}
			audit.Log(d.HistoryDB(), "job.reject", "http",
				fmt.Sprintf("id=%s", id), clientIP(r))
			w.Write([]byte(`{"status":"rejected"}`))

		case action == "run" && r.Method == http.MethodPost:
			if err := d.RunJob(r.Context(), id); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
				return
			}
			audit.Log(d.HistoryDB(), "job.trigger", "http",
				fmt.Sprintf("id=%s", id), clientIP(r))
			w.Write([]byte(`{"status":"triggered"}`))

		default:
			http.Error(w, `{"error":"unknown action"}`, http.StatusBadRequest)
		}
	})
}

func readBody(r *http.Request) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		return nil, err
	}
	return raw, nil
}
