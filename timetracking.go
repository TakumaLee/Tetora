package main

import (
	"context"
	"encoding/json"
	"fmt"
)

// --- P29.2: Time Tracking ---

// TimeTrackingConfig holds configuration for time tracking.
type TimeTrackingConfig struct {
	Enabled bool `json:"enabled"`
}

// globalTimeTracking is the singleton time tracking service.
var globalTimeTracking *TimeTrackingService

// --- Tool Handlers ---

// toolTimeStart handles the time_start tool.
func toolTimeStart(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.TimeTracking == nil {
		return "", fmt.Errorf("time tracking not initialized")
	}
	var args struct {
		Project  string   `json:"project"`
		Activity string   `json:"activity"`
		Tags     []string `json:"tags"`
		UserID   string   `json:"user_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	entry, err := app.TimeTracking.StartTimer(args.UserID, args.Project, args.Activity, args.Tags, newUUID)
	if err != nil {
		return "", err
	}
	out, _ := json.MarshalIndent(entry, "", "  ")
	return string(out), nil
}

// toolTimeStop handles the time_stop tool.
func toolTimeStop(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.TimeTracking == nil {
		return "", fmt.Errorf("time tracking not initialized")
	}
	var args struct {
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	entry, err := app.TimeTracking.StopTimer(args.UserID)
	if err != nil {
		return "", err
	}
	out, _ := json.MarshalIndent(entry, "", "  ")
	return fmt.Sprintf("Timer stopped. Duration: %d minutes\n%s", entry.DurationMinutes, string(out)), nil
}

// toolTimeLog handles the time_log tool.
func toolTimeLog(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.TimeTracking == nil {
		return "", fmt.Errorf("time tracking not initialized")
	}
	var args struct {
		Project  string   `json:"project"`
		Activity string   `json:"activity"`
		Duration int      `json:"duration"`
		Date     string   `json:"date"`
		Note     string   `json:"note"`
		Tags     []string `json:"tags"`
		UserID   string   `json:"user_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Duration <= 0 {
		return "", fmt.Errorf("duration (minutes) is required and must be positive")
	}
	entry, err := app.TimeTracking.LogEntry(args.UserID, args.Project, args.Activity, args.Duration, args.Date, args.Note, args.Tags, newUUID)
	if err != nil {
		return "", err
	}
	out, _ := json.MarshalIndent(entry, "", "  ")
	return string(out), nil
}

// toolTimeReport handles the time_report tool.
func toolTimeReport(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.TimeTracking == nil {
		return "", fmt.Errorf("time tracking not initialized")
	}
	var args struct {
		Period  string `json:"period"`
		Project string `json:"project"`
		UserID  string `json:"user_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	report, err := app.TimeTracking.Report(args.UserID, args.Period, args.Project)
	if err != nil {
		return "", err
	}
	out, _ := json.MarshalIndent(report, "", "  ")
	return string(out), nil
}
