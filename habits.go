package main

import (
	"context"
	"encoding/json"
	"fmt"

	"tetora/internal/tool"
)

// --- P24.5: Habit & Wellness Tracking ---
// Service struct and method implementations are in internal/life/habits/.
// Tool handler logic is in internal/tool/life_habits.go.
// This file keeps adapter closures and the global singleton.

var globalHabitsService *HabitsService

// --- Tool Handlers (adapter closures) ---

func toolHabitCreate(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Habits == nil {
		return "", fmt.Errorf("habits service not initialized")
	}
	return tool.HabitCreate(app.Habits, newUUID, input)
}

func toolHabitLog(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Habits == nil {
		return "", fmt.Errorf("habits service not initialized")
	}
	return tool.HabitLog(app.Habits, newUUID, input)
}

func toolHabitStatus(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Habits == nil {
		return "", fmt.Errorf("habits service not initialized")
	}
	return tool.HabitStatus(app.Habits, logWarn, input)
}

func toolHabitReport(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Habits == nil {
		return "", fmt.Errorf("habits service not initialized")
	}
	return tool.HabitReport(app.Habits, input)
}

func toolHealthLog(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Habits == nil {
		return "", fmt.Errorf("habits service not initialized")
	}
	return tool.HealthLog(app.Habits, newUUID, input)
}

func toolHealthSummary(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Habits == nil {
		return "", fmt.Errorf("habits service not initialized")
	}
	return tool.HealthSummary(app.Habits, input)
}
