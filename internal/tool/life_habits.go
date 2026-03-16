package tool

import (
	"encoding/json"
	"fmt"

	"tetora/internal/life/habits"
)

// HabitCreate handles the habit_create tool.
func HabitCreate(svc *habits.Service, uuidFn func() string, input json.RawMessage) (string, error) {
	var args struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Frequency   string `json:"frequency"`
		Category    string `json:"category"`
		TargetCount int    `json:"targetCount"`
		Scope       string `json:"scope"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	id := uuidFn()
	if err := svc.CreateHabit(id, args.Name, args.Description, args.Frequency, args.Category, args.Scope, args.TargetCount); err != nil {
		return "", err
	}

	out, _ := json.MarshalIndent(map[string]any{
		"status":   "created",
		"habit_id": id,
		"name":     args.Name,
	}, "", "  ")
	return string(out), nil
}

// HabitLog handles the habit_log tool.
func HabitLog(svc *habits.Service, uuidFn func() string, input json.RawMessage) (string, error) {
	var args struct {
		HabitID string  `json:"habitId"`
		Note    string  `json:"note"`
		Value   float64 `json:"value"`
		Scope   string  `json:"scope"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	logID := uuidFn()
	if err := svc.LogHabit(logID, args.HabitID, args.Note, args.Scope, args.Value); err != nil {
		return "", err
	}

	// Return current streak after logging.
	current, longest, _ := svc.GetStreak(args.HabitID, args.Scope)

	out, _ := json.MarshalIndent(map[string]any{
		"status":         "logged",
		"habit_id":       args.HabitID,
		"current_streak": current,
		"longest_streak": longest,
	}, "", "  ")
	return string(out), nil
}

// HabitStatus handles the habit_status tool.
func HabitStatus(svc *habits.Service, logFn func(string, ...any), input json.RawMessage) (string, error) {
	var args struct {
		Scope string `json:"scope"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	habits, err := svc.HabitStatus(args.Scope, logFn)
	if err != nil {
		return "", err
	}

	out, _ := json.MarshalIndent(map[string]any{
		"habits": habits,
		"count":  len(habits),
	}, "", "  ")
	return string(out), nil
}

// HabitReport handles the habit_report tool.
func HabitReport(svc *habits.Service, input json.RawMessage) (string, error) {
	var args struct {
		HabitID string `json:"habitId"`
		Period  string `json:"period"`
		Scope   string `json:"scope"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	report, err := svc.HabitReport(args.HabitID, args.Period, args.Scope)
	if err != nil {
		return "", err
	}

	out, _ := json.MarshalIndent(report, "", "  ")
	return string(out), nil
}

// HealthLog handles the health_log tool.
func HealthLog(svc *habits.Service, uuidFn func() string, input json.RawMessage) (string, error) {
	var args struct {
		Metric string  `json:"metric"`
		Value  float64 `json:"value"`
		Unit   string  `json:"unit"`
		Source string  `json:"source"`
		Scope  string  `json:"scope"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	id := uuidFn()
	if err := svc.LogHealth(id, args.Metric, args.Value, args.Unit, args.Source, args.Scope); err != nil {
		return "", err
	}

	out, _ := json.MarshalIndent(map[string]any{
		"status": "logged",
		"metric": args.Metric,
		"value":  args.Value,
		"unit":   args.Unit,
	}, "", "  ")
	return string(out), nil
}

// HealthSummary handles the health_summary tool.
func HealthSummary(svc *habits.Service, input json.RawMessage) (string, error) {
	var args struct {
		Metric string `json:"metric"`
		Period string `json:"period"`
		Scope  string `json:"scope"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	summary, err := svc.GetHealthSummary(args.Metric, args.Period, args.Scope)
	if err != nil {
		return "", err
	}

	out, _ := json.MarshalIndent(summary, "", "  ")
	return string(out), nil
}
