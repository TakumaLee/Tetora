package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func (s *Server) registerReminderRoutes(mux *http.ServeMux) {
	// --- P19.3: Smart Reminders ---
	mux.HandleFunc("/api/reminders", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if globalReminderEngine == nil {
			json.NewEncoder(w).Encode(map[string]any{"reminders": []any{}, "count": 0, "note": "reminder engine not enabled"})
			return
		}

		switch r.Method {
		case http.MethodGet:
			// GET /api/reminders?user_id=...
			userID := r.URL.Query().Get("user_id")
			reminders, err := globalReminderEngine.listReminders(userID)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			if reminders == nil {
				reminders = []Reminder{}
			}
			json.NewEncoder(w).Encode(map[string]any{
				"reminders": reminders,
				"count":     len(reminders),
			})

		case http.MethodPost:
			// POST /api/reminders — create a reminder.
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
			dueAt, err := parseNaturalTime(req.Time)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"cannot parse time: %v"}`, err), http.StatusBadRequest)
				return
			}
			if req.Recurring != "" {
				if _, err := parseCronExpr(req.Recurring); err != nil {
					http.Error(w, fmt.Sprintf(`{"error":"invalid cron expression: %v"}`, err), http.StatusBadRequest)
					return
				}
			}
			rem, err := globalReminderEngine.addReminder(req.Text, dueAt, req.Recurring, req.Channel, req.UserID)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(rem)

		default:
			http.Error(w, `{"error":"GET or POST only"}`, http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/reminders/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if globalReminderEngine == nil {
			http.Error(w, `{"error":"reminder engine not enabled"}`, http.StatusServiceUnavailable)
			return
		}

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
			// DELETE /api/reminders/{id}
			userID := r.URL.Query().Get("user_id")
			if err := globalReminderEngine.cancelReminder(id, userID); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": id, "status": "cancelled"})

		case r.Method == http.MethodPost && action == "snooze":
			// POST /api/reminders/{id}/snooze?duration=10m
			durStr := r.URL.Query().Get("duration")
			if durStr == "" {
				durStr = "10m"
			}
			dur, err := time.ParseDuration(durStr)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"invalid duration: %v"}`, err), http.StatusBadRequest)
				return
			}
			if err := globalReminderEngine.snoozeReminder(id, dur); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": id, "snoozed": durStr})

		default:
			http.Error(w, `{"error":"DELETE /api/reminders/{id} or POST /api/reminders/{id}/snooze"}`, http.StatusMethodNotAllowed)
		}
	})
}
