package main

import (
	"context"
	"encoding/json"
	"fmt"
)

// --- P24.5: Habit & Wellness Tracking ---
// Service struct and method implementations are in internal/life/habits/.
// This file keeps tool handlers and the global singleton.

var globalHabitsService *HabitsService

// --- Tool Handlers ---

// toolHabitCreate handles the habit_create tool.
func toolHabitCreate(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Habits == nil {
		return "", fmt.Errorf("habits service not initialized")
	}

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

	id := newUUID()
	if err := app.Habits.CreateHabit(id, args.Name, args.Description, args.Frequency, args.Category, args.Scope, args.TargetCount); err != nil {
		return "", err
	}

	out, _ := json.MarshalIndent(map[string]any{
		"status":   "created",
		"habit_id": id,
		"name":     args.Name,
	}, "", "  ")
	return string(out), nil
}

// toolHabitLog handles the habit_log tool.
func toolHabitLog(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Habits == nil {
		return "", fmt.Errorf("habits service not initialized")
	}

	var args struct {
		HabitID string  `json:"habitId"`
		Note    string  `json:"note"`
		Value   float64 `json:"value"`
		Scope   string  `json:"scope"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	logID := newUUID()
	if err := app.Habits.LogHabit(logID, args.HabitID, args.Note, args.Scope, args.Value); err != nil {
		return "", err
	}

	// Return current streak after logging.
	current, longest, _ := app.Habits.GetStreak(args.HabitID, args.Scope)

	out, _ := json.MarshalIndent(map[string]any{
		"status":         "logged",
		"habit_id":       args.HabitID,
		"current_streak": current,
		"longest_streak": longest,
	}, "", "  ")
	return string(out), nil
}

// toolHabitStatus handles the habit_status tool.
func toolHabitStatus(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Habits == nil {
		return "", fmt.Errorf("habits service not initialized")
	}

	var args struct {
		Scope string `json:"scope"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	habits, err := app.Habits.HabitStatus(args.Scope, logWarn)
	if err != nil {
		return "", err
	}

	out, _ := json.MarshalIndent(map[string]any{
		"habits": habits,
		"count":  len(habits),
	}, "", "  ")
	return string(out), nil
}

// toolHabitReport handles the habit_report tool.
func toolHabitReport(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Habits == nil {
		return "", fmt.Errorf("habits service not initialized")
	}

	var args struct {
		HabitID string `json:"habitId"`
		Period  string `json:"period"`
		Scope   string `json:"scope"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	report, err := app.Habits.HabitReport(args.HabitID, args.Period, args.Scope)
	if err != nil {
		return "", err
	}

	out, _ := json.MarshalIndent(report, "", "  ")
	return string(out), nil
}

// toolHealthLog handles the health_log tool.
func toolHealthLog(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Habits == nil {
		return "", fmt.Errorf("habits service not initialized")
	}

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

	id := newUUID()
	if err := app.Habits.LogHealth(id, args.Metric, args.Value, args.Unit, args.Source, args.Scope); err != nil {
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

// toolHealthSummary handles the health_summary tool.
func toolHealthSummary(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Habits == nil {
		return "", fmt.Errorf("habits service not initialized")
	}

	var args struct {
		Metric string `json:"metric"`
		Period string `json:"period"`
		Scope  string `json:"scope"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	summary, err := app.Habits.GetHealthSummary(args.Metric, args.Period, args.Scope)
	if err != nil {
		return "", err
	}

	out, _ := json.MarshalIndent(summary, "", "  ")
	return string(out), nil
}
