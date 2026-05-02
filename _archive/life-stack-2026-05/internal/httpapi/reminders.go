package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"tetora/internal/life/reminder"
)

// ReminderDeps holds dependencies for reminder HTTP handlers.
type ReminderDeps struct {
	Engine         *reminder.Engine
	ParseTime      func(string) (time.Time, error)
	ParseCronExpr  func(string) (any, error)
}

// RegisterReminderRoutes registers HTTP routes for the reminders API.
func RegisterReminderRoutes(mux *http.ServeMux, deps ReminderDeps) {
	if deps.Engine == nil {
		return
	}
	h := &reminderHandler{deps: deps}
	mux.HandleFunc("/api/reminders", h.handleReminders)
	mux.HandleFunc("/api/reminders/", h.handleReminderByID)
}

type reminderHandler struct {
	deps ReminderDeps
}

func (h *reminderHandler) handleReminders(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		userID := r.URL.Query().Get("user_id")
		reminders, err := h.deps.Engine.List(userID)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		if reminders == nil {
			reminders = []reminder.Reminder{}
		}
		json.NewEncoder(w).Encode(map[string]any{
			"reminders": reminders,
			"count":     len(reminders),
		})

	case http.MethodPost:
		var req struct {
			Text      string `json:"text"`
			Time      string `json:"time"`
			Recurring string `json:"recurring"`
			Channel   string `json:"channel"`
			UserID    string `json:"userId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
			return
		}
		if req.Text == "" || req.Time == "" {
			http.Error(w, `{"error":"text and time are required"}`, http.StatusBadRequest)
			return
		}
		dueAt, err := h.deps.ParseTime(req.Time)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"cannot parse time: %v"}`, err), http.StatusBadRequest)
			return
		}
		if req.Recurring != "" {
			if _, err := h.deps.ParseCronExpr(req.Recurring); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"invalid cron expression: %v"}`, err), http.StatusBadRequest)
				return
			}
		}
		rem, err := h.deps.Engine.Add(req.Text, dueAt, req.Recurring, req.Channel, req.UserID)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(rem)

	default:
		http.Error(w, `{"error":"GET or POST only"}`, http.StatusMethodNotAllowed)
	}
}

func (h *reminderHandler) handleReminderByID(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	path := strings.TrimPrefix(r.URL.Path, "/api/reminders/")
	parts := strings.SplitN(path, "/", 2)
	id := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	if id == "" {
		http.Error(w, `{"error":"reminder id required"}`, http.StatusBadRequest)
		return
	}

	switch {
	case r.Method == http.MethodDelete && action == "":
		userID := r.URL.Query().Get("user_id")
		if err := h.deps.Engine.Cancel(id, userID); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": id, "status": "cancelled"})

	case r.Method == http.MethodPost && action == "snooze":
		durStr := r.URL.Query().Get("duration")
		if durStr == "" {
			durStr = "10m"
		}
		dur, err := time.ParseDuration(durStr)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"invalid duration: %v"}`, err), http.StatusBadRequest)
			return
		}
		if err := h.deps.Engine.Snooze(id, dur); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": id, "snoozed": durStr})

	default:
		http.Error(w, `{"error":"DELETE /api/reminders/{id} or POST /api/reminders/{id}/snooze"}`, http.StatusMethodNotAllowed)
	}
}
