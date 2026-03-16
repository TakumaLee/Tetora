package main

// scheduling.go is a thin facade wrapping internal/scheduling.
// Business logic lives in internal/scheduling/; this file bridges globals.

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tetora/internal/scheduling"
)

// --- Type aliases ---

type TimeSlot = scheduling.TimeSlot
type DaySchedule = scheduling.DaySchedule
type ScheduleEvent = scheduling.ScheduleEvent
type ScheduleSuggestion = scheduling.ScheduleSuggestion

// --- Service wrapper ---

// SchedulingService wraps internal/scheduling.Service, bridging globals.
type SchedulingService struct {
	svc *scheduling.Service
}

var globalSchedulingService *SchedulingService

// newSchedulingService creates a new SchedulingService.
func newSchedulingService(cfg *Config) *SchedulingService {
	var calProv scheduling.CalendarProvider
	var taskProv scheduling.TaskProvider

	calProv = &schedulingCalendarAdapter{}
	taskProv = &schedulingTaskAdapter{}

	svc := scheduling.New(calProv, taskProv, logWarn)
	return &SchedulingService{svc: svc}
}

// --- Delegated methods ---

func (s *SchedulingService) ViewSchedule(date string, days int) ([]DaySchedule, error) {
	return s.svc.ViewSchedule(date, days)
}

func (s *SchedulingService) SuggestSlots(duration int, preferMorning bool, days int) ([]ScheduleSuggestion, error) {
	return s.svc.SuggestSlots(duration, preferMorning, days)
}

func (s *SchedulingService) PlanWeek(userID string) (map[string]any, error) {
	return s.svc.PlanWeek(userID)
}

func (s *SchedulingService) FindFreeSlots(start, end time.Time, minMinutes int) ([]TimeSlot, error) {
	return s.svc.FindFreeSlots(start, end, minMinutes)
}

// mergeEvents delegates to the internal package.
func mergeEvents(events []ScheduleEvent) []ScheduleEvent {
	return scheduling.MergeEvents(events)
}

// --- Adapter types ---

// schedulingCalendarAdapter implements scheduling.CalendarProvider using globalCalendarService.
type schedulingCalendarAdapter struct{}

func (a *schedulingCalendarAdapter) ListEvents(ctx context.Context, timeMin, timeMax string, maxResults int) ([]scheduling.CalendarEvent, error) {
	if globalCalendarService == nil {
		return nil, nil
	}
	events, err := globalCalendarService.ListEvents(ctx, timeMin, timeMax, maxResults)
	if err != nil {
		return nil, err
	}
	var result []scheduling.CalendarEvent
	for _, ev := range events {
		result = append(result, scheduling.CalendarEvent{
			Summary: ev.Summary,
			Start:   ev.Start,
			End:     ev.End,
			AllDay:  ev.AllDay,
		})
	}
	return result, nil
}

// schedulingTaskAdapter implements scheduling.TaskProvider using globalTaskManager.
type schedulingTaskAdapter struct{}

func (a *schedulingTaskAdapter) ListTasks(userID string, filter scheduling.TaskFilter) ([]scheduling.Task, error) {
	if globalTaskManager == nil {
		return nil, nil
	}
	tasks, err := globalTaskManager.ListTasks(userID, TaskFilter{
		DueDate: filter.DueDate,
		Status:  filter.Status,
		Limit:   filter.Limit,
	})
	if err != nil {
		return nil, err
	}
	var result []scheduling.Task
	for _, t := range tasks {
		result = append(result, scheduling.Task{
			Title:    t.Title,
			Priority: t.Priority,
			DueAt:    t.DueAt,
			Project:  t.Project,
		})
	}
	return result, nil
}

// --- Tool Handlers ---

// toolScheduleView handles the schedule_view tool.
func toolScheduleView(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalSchedulingService == nil {
		return "", fmt.Errorf("scheduling service not initialized")
	}

	var args struct {
		Date string `json:"date"`
		Days int    `json:"days"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Days <= 0 {
		args.Days = 1
	}
	if args.Days > 30 {
		args.Days = 30
	}

	schedules, err := globalSchedulingService.ViewSchedule(args.Date, args.Days)
	if err != nil {
		return "", err
	}

	out, err := json.MarshalIndent(schedules, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal result: %w", err)
	}
	return string(out), nil
}

// toolScheduleSuggest handles the schedule_suggest tool.
func toolScheduleSuggest(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalSchedulingService == nil {
		return "", fmt.Errorf("scheduling service not initialized")
	}

	var args struct {
		DurationMinutes int  `json:"duration_minutes"`
		PreferMorning   bool `json:"prefer_morning"`
		Days            int  `json:"days"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.DurationMinutes <= 0 {
		args.DurationMinutes = 60
	}
	if args.Days <= 0 {
		args.Days = 5
	}
	if args.Days > 14 {
		args.Days = 14
	}

	suggestions, err := globalSchedulingService.SuggestSlots(args.DurationMinutes, args.PreferMorning, args.Days)
	if err != nil {
		return "", err
	}

	if len(suggestions) == 0 {
		return "No available time slots found for the requested duration.", nil
	}

	out, err := json.MarshalIndent(suggestions, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal result: %w", err)
	}
	return fmt.Sprintf("Found %d suggested slots:\n%s", len(suggestions), string(out)), nil
}

// toolSchedulePlan handles the schedule_plan tool.
func toolSchedulePlan(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalSchedulingService == nil {
		return "", fmt.Errorf("scheduling service not initialized")
	}

	var args struct {
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.UserID == "" {
		args.UserID = "default"
	}

	plan, err := globalSchedulingService.PlanWeek(args.UserID)
	if err != nil {
		return "", err
	}

	out, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal result: %w", err)
	}
	return string(out), nil
}
