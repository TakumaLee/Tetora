package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tetora/internal/log"
	"tetora/internal/life/reminder"
)

// --- P19.3: Smart Reminders ---

// nextCronTime computes the next occurrence of a cron expression after the given time.
// Reuses parseCronExpr and nextRunAfter from cron.go.
func nextCronTime(expr string, after time.Time) time.Time {
	parsed, err := parseCronExpr(expr)
	if err != nil {
		log.Warn("reminder bad cron expr", "expr", expr, "error", err)
		return time.Time{}
	}
	return nextRunAfter(parsed, time.UTC, after)
}

// parseNaturalTime delegates to internal reminder package.
func parseNaturalTime(input string) (time.Time, error) {
	return reminder.ParseNaturalTime(input)
}

// --- Tool Handlers for Reminders ---

// Global reminder engine reference (set in main.go).
var globalReminderEngine *ReminderEngine

func toolReminderSet(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	var args struct {
		Text      string `json:"text"`
		Time      string `json:"time"`
		Recurring string `json:"recurring"`
		Channel   string `json:"channel"`
		UserID    string `json:"user_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("parse input: %w", err)
	}
	if args.Text == "" {
		return "", fmt.Errorf("text is required")
	}
	if args.Time == "" {
		return "", fmt.Errorf("time is required")
	}

	if app == nil || app.Reminder == nil {
		return "", fmt.Errorf("reminder engine not initialized (enable reminders in config)")
	}

	dueAt, err := parseNaturalTime(args.Time)
	if err != nil {
		return "", fmt.Errorf("parse time %q: %w", args.Time, err)
	}

	// Validate recurring expression if provided.
	if args.Recurring != "" {
		if _, err := parseCronExpr(args.Recurring); err != nil {
			return "", fmt.Errorf("invalid recurring cron expression %q: %w", args.Recurring, err)
		}
	}

	rem, err := app.Reminder.Add(args.Text, dueAt, args.Recurring, args.Channel, args.UserID)
	if err != nil {
		return "", err
	}

	out, _ := json.Marshal(rem)
	return string(out), nil
}

func toolReminderList(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	var args struct {
		UserID string `json:"user_id"`
	}
	json.Unmarshal(input, &args)

	if app == nil || app.Reminder == nil {
		return "", fmt.Errorf("reminder engine not initialized")
	}

	reminders, err := app.Reminder.List(args.UserID)
	if err != nil {
		return "", err
	}
	if reminders == nil {
		reminders = []Reminder{}
	}

	out, _ := json.Marshal(map[string]any{
		"reminders": reminders,
		"count":     len(reminders),
	})
	return string(out), nil
}

func toolReminderCancel(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	var args struct {
		ID     string `json:"id"`
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("parse input: %w", err)
	}
	if args.ID == "" {
		return "", fmt.Errorf("id is required")
	}

	if app == nil || app.Reminder == nil {
		return "", fmt.Errorf("reminder engine not initialized")
	}

	if err := app.Reminder.Cancel(args.ID, args.UserID); err != nil {
		return "", err
	}

	return fmt.Sprintf(`{"ok":true,"id":"%s","status":"cancelled"}`, args.ID), nil
}
