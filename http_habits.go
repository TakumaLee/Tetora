package main

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// registerHabitsRoutes registers HTTP routes for the habits & wellness API.
func (s *Server) registerHabitsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/habits", s.handleHabits)
	mux.HandleFunc("/api/habits/log", s.handleHabitsLog)
	mux.HandleFunc("/api/habits/report", s.handleHabitsReport)
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/health/summary", s.handleHealthSummary)
}

// handleHabits handles listing and creating habits.
func (s *Server) handleHabits(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if globalHabitsService == nil {
		http.Error(w, `{"error":"habits service not initialized"}`, http.StatusServiceUnavailable)
		return
	}

	switch r.Method {
	case http.MethodGet:
		scope := r.URL.Query().Get("scope")
		habits, err := globalHabitsService.HabitStatus(scope)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"habits": habits, "count": len(habits)})

	case http.MethodPost:
		var body struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Frequency   string `json:"frequency"`
			Category    string `json:"category"`
			TargetCount int    `json:"targetCount"`
			Scope       string `json:"scope"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
			return
		}
		if body.Name == "" {
			http.Error(w, `{"error":"name is required"}`, http.StatusBadRequest)
			return
		}
		id, err := globalHabitsService.CreateHabit(
			body.Name, body.Description, body.Frequency, body.Category, body.Scope, body.TargetCount)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"status": "created", "habit_id": id, "name": body.Name})

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// handleHabitsLog handles logging a habit completion.
func (s *Server) handleHabitsLog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if globalHabitsService == nil {
		http.Error(w, `{"error":"habits service not initialized"}`, http.StatusServiceUnavailable)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		HabitID string  `json:"habitId"`
		Note    string  `json:"note"`
		Value   float64 `json:"value"`
		Scope   string  `json:"scope"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if body.HabitID == "" {
		http.Error(w, `{"error":"habitId is required"}`, http.StatusBadRequest)
		return
	}

	if err := globalHabitsService.LogHabit(body.HabitID, body.Note, body.Scope, body.Value); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusBadRequest)
		return
	}

	current, longest, _ := globalHabitsService.GetStreak(body.HabitID, body.Scope)
	json.NewEncoder(w).Encode(map[string]any{
		"status":         "logged",
		"habit_id":       body.HabitID,
		"current_streak": current,
		"longest_streak": longest,
	})
}

// handleHabitsReport returns a detailed habit report.
func (s *Server) handleHabitsReport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if globalHabitsService == nil {
		http.Error(w, `{"error":"habits service not initialized"}`, http.StatusServiceUnavailable)
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	habitID := r.URL.Query().Get("habit_id")
	period := r.URL.Query().Get("period")
	scope := r.URL.Query().Get("scope")

	report, err := globalHabitsService.HabitReport(habitID, period, scope)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(report)
}

// handleHealth handles logging health data.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if globalHabitsService == nil {
		http.Error(w, `{"error":"habits service not initialized"}`, http.StatusServiceUnavailable)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		Metric string  `json:"metric"`
		Value  float64 `json:"value"`
		Unit   string  `json:"unit"`
		Source string  `json:"source"`
		Scope  string  `json:"scope"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if body.Metric == "" {
		http.Error(w, `{"error":"metric is required"}`, http.StatusBadRequest)
		return
	}

	if err := globalHabitsService.LogHealth(body.Metric, body.Value, body.Unit, body.Source, body.Scope); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]any{
		"status": "logged",
		"metric": body.Metric,
		"value":  body.Value,
		"unit":   body.Unit,
	})
}

// handleHealthSummary returns health metric summary.
func (s *Server) handleHealthSummary(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if globalHabitsService == nil {
		http.Error(w, `{"error":"habits service not initialized"}`, http.StatusServiceUnavailable)
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	metric := r.URL.Query().Get("metric")
	period := r.URL.Query().Get("period")
	scope := r.URL.Query().Get("scope")

	if metric == "" {
		http.Error(w, `{"error":"metric query parameter is required"}`, http.StatusBadRequest)
		return
	}

	summary, err := globalHabitsService.GetHealthSummary(metric, period, scope)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(summary)
}
