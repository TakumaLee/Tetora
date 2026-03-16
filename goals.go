package main

import (
	"context"
	"encoding/json"
	"fmt"

	"tetora/internal/tool"
)

// --- P24.6: Goal Planning & Autonomy ---
// Service struct, types, and method implementations are in internal/life/goals/.
// Tool handler logic is in internal/tool/life_goals.go.
// This file keeps adapter closures and the global singleton.

// globalGoalsService is the singleton goals service.
var globalGoalsService *GoalsService

// --- Tool Handlers (adapter closures) ---

func toolGoalCreate(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Goals == nil {
		return "", fmt.Errorf("goals service not initialized")
	}
	return tool.GoalCreate(app.Goals, newUUID, app.Lifecycle, cfg.Lifecycle.AutoHabitSuggest, input)
}

func toolGoalList(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Goals == nil {
		return "", fmt.Errorf("goals service not initialized")
	}
	return tool.GoalList(app.Goals, input)
}

func toolGoalUpdate(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Goals == nil {
		return "", fmt.Errorf("goals service not initialized")
	}
	return tool.GoalUpdate(app.Goals, newUUID, app.Lifecycle, logWarn, input)
}

func toolGoalReview(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Goals == nil {
		return "", fmt.Errorf("goals service not initialized")
	}
	return tool.GoalReview(app.Goals, input)
}
