package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"

	"tetora/internal/life/habits"
	"tetora/internal/log"
	"tetora/internal/trace"
)

// RegisterHabitsRoutes registers HTTP routes for the habits & wellness API.
func RegisterHabitsRoutes(mux *http.ServeMux, svc *habits.Service) {
	if svc == nil {
		return
	}
	h := &habitsHandler{svc: svc}
	mux.HandleFunc("/api/habits", h.handleHabits)
	mux.HandleFunc("/api/habits/log", h.handleHabitsLog)
	mux.HandleFunc("/api/habits/report", h.handleHabitsReport)
	mux.HandleFunc("/api/wellness", h.handleHealth)
	mux.HandleFunc("/api/wellness/summary", h.handleHealthSummary)
}

type habitsHandler struct {
	svc *habits.Service
}

func (h *habitsHandler) handleHabits(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		scope := r.URL.Query().Get("scope")
		habits, err := h.svc.HabitStatus(scope, log.Warn)
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
		id := trace.NewUUID()
		if err := h.svc.CreateHabit(
			id, body.Name, body.Description, body.Frequency, body.Category, body.Scope, body.TargetCount); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"status": "created", "habit_id": id, "name": body.Name})

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (h *habitsHandler) handleHabitsLog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

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

	if err := h.svc.LogHabit(trace.NewUUID(), body.HabitID, body.Note, body.Scope, body.Value); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusBadRequest)
		return
	}

	current, longest, _ := h.svc.GetStreak(body.HabitID, body.Scope)
	json.NewEncoder(w).Encode(map[string]any{
		"status":         "logged",
		"habit_id":       body.HabitID,
		"current_streak": current,
		"longest_streak": longest,
	})
}

func (h *habitsHandler) handleHabitsReport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	habitID := r.URL.Query().Get("habit_id")
	period := r.URL.Query().Get("period")
	scope := r.URL.Query().Get("scope")

	report, err := h.svc.HabitReport(habitID, period, scope)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(report)
}

func (h *habitsHandler) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

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

	if err := h.svc.LogHealth(trace.NewUUID(), body.Metric, body.Value, body.Unit, body.Source, body.Scope); err != nil {
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

func (h *habitsHandler) handleHealthSummary(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

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

	summary, err := h.svc.GetHealthSummary(metric, period, scope)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(summary)
}
