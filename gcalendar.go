package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// --- P19.2: Google Calendar Integration ---
// Service struct and method implementations are in internal/life/calendar/.
// This file keeps tool handlers, config types, and the global singleton.

// CalendarConfig holds Calendar integration settings.
type CalendarConfig struct {
	Enabled    bool   `json:"enabled"`
	CalendarID string `json:"calendarId,omitempty"` // default "primary"
	TimeZone   string `json:"timeZone,omitempty"`   // default local timezone
	MaxResults int    `json:"maxResults,omitempty"`  // default 10
}

var globalCalendarService *CalendarService

// --- Tool Handlers ---

// toolCalendarList handles the calendar_list tool.
func toolCalendarList(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if !cfg.Calendar.Enabled {
		return "", fmt.Errorf("calendar integration is not enabled")
	}
	app := appFromCtx(ctx)
	if app == nil || app.Calendar == nil {
		return "", fmt.Errorf("calendar service not initialized")
	}

	var args struct {
		TimeMin    string `json:"timeMin"`
		TimeMax    string `json:"timeMax"`
		MaxResults int    `json:"maxResults"`
		Days       int    `json:"days"` // convenience: list events for next N days
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	// Default: next 7 days if no time range specified.
	if args.TimeMin == "" && args.TimeMax == "" {
		now := time.Now()
		args.TimeMin = now.Format(time.RFC3339)
		days := 7
		if args.Days > 0 {
			days = args.Days
		}
		args.TimeMax = now.AddDate(0, 0, days).Format(time.RFC3339)
	}

	events, err := app.Calendar.ListEvents(ctx, args.TimeMin, args.TimeMax, args.MaxResults)
	if err != nil {
		return "", err
	}

	if len(events) == 0 {
		return "No upcoming events found.", nil
	}

	out, err := json.MarshalIndent(events, "", "  ")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Found %d events:\n%s", len(events), string(out)), nil
}

// toolCalendarCreate handles the calendar_create tool.
func toolCalendarCreate(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if !cfg.Calendar.Enabled {
		return "", fmt.Errorf("calendar integration is not enabled")
	}
	app := appFromCtx(ctx)
	if app == nil || app.Calendar == nil {
		return "", fmt.Errorf("calendar service not initialized")
	}

	var args struct {
		// Structured input.
		Summary     string   `json:"summary"`
		Description string   `json:"description"`
		Location    string   `json:"location"`
		Start       string   `json:"start"`
		End         string   `json:"end"`
		TimeZone    string   `json:"timeZone"`
		Attendees   []string `json:"attendees"`
		AllDay      bool     `json:"allDay"`
		// Natural language input.
		Text string `json:"text"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	var eventInput CalendarEventInput

	if args.Text != "" {
		// Try natural language parsing.
		parsed, err := parseNaturalSchedule(args.Text)
		if err != nil {
			return "", fmt.Errorf("cannot parse schedule: %w", err)
		}
		eventInput = *parsed
	} else {
		if args.Summary == "" {
			return "", fmt.Errorf("summary is required")
		}
		if args.Start == "" {
			return "", fmt.Errorf("start time is required")
		}
		eventInput = CalendarEventInput{
			Summary:     args.Summary,
			Description: args.Description,
			Location:    args.Location,
			Start:       args.Start,
			End:         args.End,
			TimeZone:    args.TimeZone,
			Attendees:   args.Attendees,
			AllDay:      args.AllDay,
		}
	}

	if eventInput.TimeZone == "" {
		eventInput.TimeZone = app.Calendar.TimeZone()
	}

	ev, err := app.Calendar.CreateEvent(ctx, eventInput)
	if err != nil {
		return "", err
	}

	out, _ := json.MarshalIndent(ev, "", "  ")
	return fmt.Sprintf("Event created:\n%s", string(out)), nil
}

// toolCalendarUpdate handles the calendar_update tool.
func toolCalendarUpdate(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if !cfg.Calendar.Enabled {
		return "", fmt.Errorf("calendar integration is not enabled")
	}
	app := appFromCtx(ctx)
	if app == nil || app.Calendar == nil {
		return "", fmt.Errorf("calendar service not initialized")
	}

	var args struct {
		EventID     string   `json:"eventId"`
		Summary     string   `json:"summary"`
		Description string   `json:"description"`
		Location    string   `json:"location"`
		Start       string   `json:"start"`
		End         string   `json:"end"`
		TimeZone    string   `json:"timeZone"`
		Attendees   []string `json:"attendees"`
		AllDay      bool     `json:"allDay"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if args.EventID == "" {
		return "", fmt.Errorf("eventId is required")
	}

	eventInput := CalendarEventInput{
		Summary:     args.Summary,
		Description: args.Description,
		Location:    args.Location,
		Start:       args.Start,
		End:         args.End,
		TimeZone:    args.TimeZone,
		Attendees:   args.Attendees,
		AllDay:      args.AllDay,
	}

	if eventInput.TimeZone == "" {
		eventInput.TimeZone = app.Calendar.TimeZone()
	}

	ev, err := app.Calendar.UpdateEvent(ctx, args.EventID, eventInput)
	if err != nil {
		return "", err
	}

	out, _ := json.MarshalIndent(ev, "", "  ")
	return fmt.Sprintf("Event updated:\n%s", string(out)), nil
}

// toolCalendarDelete handles the calendar_delete tool.
func toolCalendarDelete(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if !cfg.Calendar.Enabled {
		return "", fmt.Errorf("calendar integration is not enabled")
	}
	app := appFromCtx(ctx)
	if app == nil || app.Calendar == nil {
		return "", fmt.Errorf("calendar service not initialized")
	}

	var args struct {
		EventID string `json:"eventId"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if args.EventID == "" {
		return "", fmt.Errorf("eventId is required")
	}

	if err := app.Calendar.DeleteEvent(ctx, args.EventID); err != nil {
		return "", err
	}

	return fmt.Sprintf("Event %s deleted successfully.", args.EventID), nil
}

// toolCalendarSearch handles the calendar_search tool.
func toolCalendarSearch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if !cfg.Calendar.Enabled {
		return "", fmt.Errorf("calendar integration is not enabled")
	}
	app := appFromCtx(ctx)
	if app == nil || app.Calendar == nil {
		return "", fmt.Errorf("calendar service not initialized")
	}

	var args struct {
		Query   string `json:"query"`
		TimeMin string `json:"timeMin"`
		TimeMax string `json:"timeMax"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if args.Query == "" {
		return "", fmt.Errorf("query is required")
	}

	// Default time range: past 30 days to next 90 days.
	if args.TimeMin == "" {
		args.TimeMin = time.Now().AddDate(0, 0, -30).Format(time.RFC3339)
	}
	if args.TimeMax == "" {
		args.TimeMax = time.Now().AddDate(0, 0, 90).Format(time.RFC3339)
	}

	events, err := app.Calendar.SearchEvents(ctx, args.Query, args.TimeMin, args.TimeMax)
	if err != nil {
		return "", err
	}

	if len(events) == 0 {
		return fmt.Sprintf("No events found matching %q.", args.Query), nil
	}

	out, err := json.MarshalIndent(events, "", "  ")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Found %d events matching %q:\n%s", len(events), args.Query, string(out)), nil
}
