package main

import (
	"context"
	"encoding/json"
	"fmt"

	"tetora/internal/tool"
)

// --- P29.2: Time Tracking ---

// TimeTrackingConfig holds configuration for time tracking.
type TimeTrackingConfig struct {
	Enabled bool `json:"enabled"`
}

// globalTimeTracking is the singleton time tracking service.
var globalTimeTracking *TimeTrackingService

// --- Tool Handlers (adapter closures) ---

func toolTimeStart(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.TimeTracking == nil {
		return "", fmt.Errorf("time tracking not initialized")
	}
	return tool.TimeStart(app.TimeTracking, newUUID, input)
}

func toolTimeStop(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.TimeTracking == nil {
		return "", fmt.Errorf("time tracking not initialized")
	}
	return tool.TimeStop(app.TimeTracking, input)
}

func toolTimeLog(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.TimeTracking == nil {
		return "", fmt.Errorf("time tracking not initialized")
	}
	return tool.TimeLog(app.TimeTracking, newUUID, input)
}

func toolTimeReport(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.TimeTracking == nil {
		return "", fmt.Errorf("time tracking not initialized")
	}
	return tool.TimeReport(app.TimeTracking, input)
}
