package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tetora/internal/automation/briefing"
	"tetora/internal/db"
)

// --- P24.7: Morning Briefing & Evening Wrap ---

var globalBriefingService *briefing.Service

// newBriefingService constructs a briefing.Service from Config + globals.
func newBriefingService(cfg *Config) *briefing.Service {
	deps := briefing.Deps{
		Query:  db.Query,
		Escape: db.Escape,
	}
	if globalSchedulingService != nil {
		svc := globalSchedulingService
		deps.ViewSchedule = func(dateStr string, days int) ([]briefing.ScheduleDay, error) {
			schedules, err := svc.ViewSchedule(dateStr, days)
			if err != nil {
				return nil, err
			}
			result := make([]briefing.ScheduleDay, len(schedules))
			for i, s := range schedules {
				events := make([]briefing.ScheduleEvent, len(s.Events))
				for j, ev := range s.Events {
					events[j] = briefing.ScheduleEvent{
						Start: ev.Start,
						Title: ev.Title,
					}
				}
				result[i] = briefing.ScheduleDay{Events: events}
			}
			return result, nil
		}
	}
	if globalContactsService != nil {
		svc := globalContactsService
		deps.GetUpcomingEvents = func(days int) ([]map[string]any, error) {
			return svc.GetUpcomingEvents(days)
		}
	}
	deps.TasksAvailable = globalTaskManager != nil
	deps.HabitsAvailable = globalHabitsService != nil
	deps.GoalsAvailable = globalGoalsService != nil
	deps.FinanceAvailable = globalFinanceService != nil
	return briefing.New(cfg.HistoryDB, deps)
}

// --- Tool Handlers ---

func toolBriefingMorning(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Briefing == nil {
		return "", fmt.Errorf("briefing service not initialized")
	}
	var args struct {
		Date string `json:"date"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	date := time.Now()
	if args.Date != "" {
		parsed, err := time.Parse("2006-01-02", args.Date)
		if err != nil {
			return "", fmt.Errorf("invalid date format (expected YYYY-MM-DD): %w", err)
		}
		date = parsed
	}
	br, err := app.Briefing.GenerateMorning(date)
	if err != nil {
		return "", err
	}
	return briefing.FormatBriefing(br), nil
}

func toolBriefingEvening(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Briefing == nil {
		return "", fmt.Errorf("briefing service not initialized")
	}
	var args struct {
		Date string `json:"date"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	date := time.Now()
	if args.Date != "" {
		parsed, err := time.Parse("2006-01-02", args.Date)
		if err != nil {
			return "", fmt.Errorf("invalid date format (expected YYYY-MM-DD): %w", err)
		}
		date = parsed
	}
	br, err := app.Briefing.GenerateEvening(date)
	if err != nil {
		return "", err
	}
	return briefing.FormatBriefing(br), nil
}
