package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// --- P19.2: Google Calendar Integration ---

// CalendarConfig holds Calendar integration settings.
type CalendarConfig struct {
	Enabled    bool   `json:"enabled"`
	CalendarID string `json:"calendarId,omitempty"` // default "primary"
	TimeZone   string `json:"timeZone,omitempty"`   // default local timezone
	MaxResults int    `json:"maxResults,omitempty"`  // default 10
}

// CalendarEvent represents a parsed calendar event.
type CalendarEvent struct {
	ID          string   `json:"id"`
	Summary     string   `json:"summary"`
	Description string   `json:"description,omitempty"`
	Location    string   `json:"location,omitempty"`
	Start       string   `json:"start"`             // formatted datetime
	End         string   `json:"end"`               // formatted datetime
	Status      string   `json:"status"`            // confirmed, tentative, cancelled
	HtmlLink    string   `json:"htmlLink,omitempty"`
	Attendees   []string `json:"attendees,omitempty"`
	AllDay      bool     `json:"allDay,omitempty"`
}

// CalendarEventInput represents input for creating/updating an event.
type CalendarEventInput struct {
	Summary     string   `json:"summary"`
	Description string   `json:"description,omitempty"`
	Location    string   `json:"location,omitempty"`
	Start       string   `json:"start"`                    // ISO 8601 datetime or date
	End         string   `json:"end"`                      // ISO 8601 datetime or date
	TimeZone    string   `json:"timeZone,omitempty"`
	Attendees   []string `json:"attendees,omitempty"` // email addresses
	AllDay      bool     `json:"allDay,omitempty"`
}

// CalendarService wraps the Google Calendar API.
type CalendarService struct {
	cfg *Config
}

var globalCalendarService *CalendarService

const calendarBaseURL = "https://www.googleapis.com/calendar/v3/calendars"

// --- Helper Functions ---

// calendarID returns the configured calendar ID or "primary".
func calendarID(cfg *Config) string {
	if cfg.Calendar.CalendarID != "" {
		return cfg.Calendar.CalendarID
	}
	return "primary"
}

// calendarTimeZone returns the configured timezone or local timezone.
func calendarTimeZone(cfg *Config) string {
	if cfg.Calendar.TimeZone != "" {
		return cfg.Calendar.TimeZone
	}
	return time.Now().Location().String()
}

// calendarMaxResults returns the configured max results or 10.
func calendarMaxResults(cfg *Config) int {
	if cfg.Calendar.MaxResults > 0 {
		return cfg.Calendar.MaxResults
	}
	return 10
}

// parseGCalEvent parses a Calendar API response item into a CalendarEvent.
func parseGCalEvent(item map[string]any) CalendarEvent {
	ev := CalendarEvent{}

	if id, ok := item["id"].(string); ok {
		ev.ID = id
	}
	if summary, ok := item["summary"].(string); ok {
		ev.Summary = summary
	}
	if desc, ok := item["description"].(string); ok {
		ev.Description = desc
	}
	if loc, ok := item["location"].(string); ok {
		ev.Location = loc
	}
	if status, ok := item["status"].(string); ok {
		ev.Status = status
	}
	if link, ok := item["htmlLink"].(string); ok {
		ev.HtmlLink = link
	}

	// Parse start time.
	if startObj, ok := item["start"].(map[string]any); ok {
		if dt, ok := startObj["dateTime"].(string); ok {
			ev.Start = dt
		} else if d, ok := startObj["date"].(string); ok {
			ev.Start = d
			ev.AllDay = true
		}
	}

	// Parse end time.
	if endObj, ok := item["end"].(map[string]any); ok {
		if dt, ok := endObj["dateTime"].(string); ok {
			ev.End = dt
		} else if d, ok := endObj["date"].(string); ok {
			ev.End = d
		}
	}

	// Parse attendees.
	if attendees, ok := item["attendees"].([]any); ok {
		for _, a := range attendees {
			if aMap, ok := a.(map[string]any); ok {
				if email, ok := aMap["email"].(string); ok {
					ev.Attendees = append(ev.Attendees, email)
				}
			}
		}
	}

	return ev
}

// buildGCalBody builds a Google Calendar API request body from input.
func buildGCalBody(input CalendarEventInput) map[string]any {
	body := map[string]any{}

	if input.Summary != "" {
		body["summary"] = input.Summary
	}
	if input.Description != "" {
		body["description"] = input.Description
	}
	if input.Location != "" {
		body["location"] = input.Location
	}

	tz := input.TimeZone
	if tz == "" {
		tz = time.Now().Location().String()
	}

	if input.AllDay {
		// All-day events use "date" field (YYYY-MM-DD).
		startDate := input.Start
		endDate := input.End
		// Ensure date-only format.
		if len(startDate) > 10 {
			startDate = startDate[:10]
		}
		if endDate == "" {
			// Default: next day for all-day events.
			if t, err := time.Parse("2006-01-02", startDate); err == nil {
				endDate = t.AddDate(0, 0, 1).Format("2006-01-02")
			} else {
				endDate = startDate
			}
		}
		if len(endDate) > 10 {
			endDate = endDate[:10]
		}
		body["start"] = map[string]any{"date": startDate}
		body["end"] = map[string]any{"date": endDate}
	} else {
		// Timed events use "dateTime" field.
		body["start"] = map[string]any{
			"dateTime": input.Start,
			"timeZone": tz,
		}
		endTime := input.End
		if endTime == "" {
			// Default: 1 hour after start.
			if t, err := time.Parse(time.RFC3339, input.Start); err == nil {
				endTime = t.Add(1 * time.Hour).Format(time.RFC3339)
			} else {
				endTime = input.Start
			}
		}
		body["end"] = map[string]any{
			"dateTime": endTime,
			"timeZone": tz,
		}
	}

	// Attendees.
	if len(input.Attendees) > 0 {
		attendees := make([]map[string]any, len(input.Attendees))
		for i, email := range input.Attendees {
			attendees[i] = map[string]any{"email": email}
		}
		body["attendees"] = attendees
	}

	return body
}

// --- Natural Language Schedule Parser ---

// parseNaturalSchedule parses natural language scheduling input into a CalendarEventInput.
// Supports Japanese ("明日2時の会議"), English ("meeting tomorrow at 2pm"),
// and Chinese ("明天下午2點開會").
func parseNaturalSchedule(text string) (*CalendarEventInput, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("empty schedule input")
	}

	now := time.Now()
	loc := now.Location()

	// Try Japanese: "明日2時の会議", "今日15時のミーティング"
	if ev, ok := parseJaSchedule(text, now, loc); ok {
		return ev, nil
	}

	// Try Chinese: "明天下午2點開會", "明天3點的會議"
	if ev, ok := parseZhSchedule(text, now, loc); ok {
		return ev, nil
	}

	// Try English: "meeting tomorrow at 2pm", "lunch at noon"
	if ev, ok := parseEnSchedule(text, now, loc); ok {
		return ev, nil
	}

	return nil, fmt.Errorf("cannot parse schedule: %q", text)
}

// parseJaSchedule parses Japanese schedule expressions.
func parseJaSchedule(text string, now time.Time, loc *time.Location) (*CalendarEventInput, bool) {
	// Pattern: {date}{time}の{summary} or {date}{time}{summary}
	// "明日14時の会議" → tomorrow 14:00, summary=会議
	// "今日15時ミーティング" → today 15:00, summary=ミーティング

	var baseDate time.Time
	rest := text

	if strings.HasPrefix(text, "明日") {
		baseDate = now.AddDate(0, 0, 1)
		rest = strings.TrimPrefix(text, "明日")
	} else if strings.HasPrefix(text, "今日") {
		baseDate = now
		rest = strings.TrimPrefix(text, "今日")
	} else if strings.HasPrefix(text, "明後日") {
		baseDate = now.AddDate(0, 0, 2)
		rest = strings.TrimPrefix(text, "明後日")
	} else {
		return nil, false
	}

	// Extract time: "14時", "2時", "14時30分"
	reTime := regexp.MustCompile(`^(\d{1,2})時(?:(\d{1,2})分)?`)
	m := reTime.FindStringSubmatch(rest)
	h, min := 9, 0 // default 9:00
	if m != nil {
		h, _ = strconv.Atoi(m[1])
		if m[2] != "" {
			min, _ = strconv.Atoi(m[2])
		}
		rest = rest[len(m[0]):]
	}

	// Extract summary: after "の" or remaining text.
	summary := strings.TrimPrefix(rest, "の")
	summary = strings.TrimSpace(summary)
	if summary == "" {
		summary = "予定"
	}

	startTime := time.Date(baseDate.Year(), baseDate.Month(), baseDate.Day(), h, min, 0, 0, loc)
	endTime := startTime.Add(1 * time.Hour)

	return &CalendarEventInput{
		Summary:  summary,
		Start:    startTime.Format(time.RFC3339),
		End:      endTime.Format(time.RFC3339),
		TimeZone: loc.String(),
	}, true
}

// parseZhSchedule parses Chinese schedule expressions.
func parseZhSchedule(text string, now time.Time, loc *time.Location) (*CalendarEventInput, bool) {
	// "明天下午2點開會", "明天3點的會議"
	var baseDate time.Time
	rest := text

	if strings.HasPrefix(text, "明天") {
		baseDate = now.AddDate(0, 0, 1)
		rest = strings.TrimPrefix(text, "明天")
	} else if strings.HasPrefix(text, "今天") {
		baseDate = now
		rest = strings.TrimPrefix(text, "今天")
	} else if strings.HasPrefix(text, "後天") {
		baseDate = now.AddDate(0, 0, 2)
		rest = strings.TrimPrefix(text, "後天")
	} else {
		return nil, false
	}

	h, min := 9, 0
	offset := 0

	// Check for AM/PM prefix.
	if strings.HasPrefix(rest, "下午") {
		offset = 12
		rest = strings.TrimPrefix(rest, "下午")
	} else if strings.HasPrefix(rest, "上午") {
		rest = strings.TrimPrefix(rest, "上午")
	}

	// Extract time: "2點", "2點30分"
	reTime := regexp.MustCompile(`^(\d{1,2})點(?:(\d{1,2})分)?`)
	m := reTime.FindStringSubmatch(rest)
	if m != nil {
		h, _ = strconv.Atoi(m[1])
		h += offset
		if h == 24 {
			h = 12 // 下午12點 = noon
		}
		if m[2] != "" {
			min, _ = strconv.Atoi(m[2])
		}
		rest = rest[len(m[0]):]
	}

	// Extract summary.
	summary := strings.TrimPrefix(rest, "的")
	summary = strings.TrimSpace(summary)
	if summary == "" {
		summary = "活動"
	}

	startTime := time.Date(baseDate.Year(), baseDate.Month(), baseDate.Day(), h, min, 0, 0, loc)
	endTime := startTime.Add(1 * time.Hour)

	return &CalendarEventInput{
		Summary:  summary,
		Start:    startTime.Format(time.RFC3339),
		End:      endTime.Format(time.RFC3339),
		TimeZone: loc.String(),
	}, true
}

// parseEnSchedule parses English schedule expressions.
func parseEnSchedule(text string, now time.Time, loc *time.Location) (*CalendarEventInput, bool) {
	lower := strings.ToLower(text)

	// Extract date part.
	var baseDate time.Time
	dateFound := false

	if strings.Contains(lower, "tomorrow") {
		baseDate = now.AddDate(0, 0, 1)
		dateFound = true
	} else if strings.Contains(lower, "today") {
		baseDate = now
		dateFound = true
	}

	if !dateFound {
		return nil, false
	}

	// Extract time: "at 2pm", "at 14:00", "at 2:30pm"
	h, min := 9, 0 // default 9:00
	reAt := regexp.MustCompile(`at\s+(\d{1,2})(?::(\d{2}))?\s*(am|pm)?`)
	if m := reAt.FindStringSubmatch(lower); m != nil {
		h, _ = strconv.Atoi(m[1])
		if m[2] != "" {
			min, _ = strconv.Atoi(m[2])
		}
		if m[3] == "pm" && h != 12 {
			h += 12
		} else if m[3] == "am" && h == 12 {
			h = 0
		}
	}

	// Extract summary: remove date and time parts.
	summary := text
	// Remove common date words.
	for _, w := range []string{"tomorrow", "today", "Tomorrow", "Today"} {
		summary = strings.ReplaceAll(summary, w, "")
	}
	// Remove "at TIME" part.
	reAtFull := regexp.MustCompile(`(?i)at\s+\d{1,2}(?::\d{2})?\s*(?:am|pm)?`)
	summary = reAtFull.ReplaceAllString(summary, "")
	summary = strings.TrimSpace(summary)
	if summary == "" {
		summary = "Event"
	}

	startTime := time.Date(baseDate.Year(), baseDate.Month(), baseDate.Day(), h, min, 0, 0, loc)
	endTime := startTime.Add(1 * time.Hour)

	return &CalendarEventInput{
		Summary:  summary,
		Start:    startTime.Format(time.RFC3339),
		End:      endTime.Format(time.RFC3339),
		TimeZone: loc.String(),
	}, true
}

// --- Calendar API Methods ---

// ListEvents lists events from the calendar within a time range.
func (s *CalendarService) ListEvents(ctx context.Context, timeMin, timeMax string, maxResults int) ([]CalendarEvent, error) {
	if globalOAuthManager == nil {
		return nil, fmt.Errorf("OAuth not configured")
	}

	calID := calendarID(s.cfg)
	if maxResults <= 0 {
		maxResults = calendarMaxResults(s.cfg)
	}

	params := url.Values{}
	if timeMin != "" {
		params.Set("timeMin", timeMin)
	}
	if timeMax != "" {
		params.Set("timeMax", timeMax)
	}
	params.Set("maxResults", strconv.Itoa(maxResults))
	params.Set("singleEvents", "true")
	params.Set("orderBy", "startTime")

	reqURL := fmt.Sprintf("%s/%s/events?%s", calendarBaseURL, url.PathEscape(calID), params.Encode())

	resp, err := globalOAuthManager.Request(ctx, "google", "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("calendar API error %d: %s", resp.StatusCode, truncateStr(string(body), 500))
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	items, _ := result["items"].([]any)
	events := make([]CalendarEvent, 0, len(items))
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			events = append(events, parseGCalEvent(m))
		}
	}

	return events, nil
}

// CreateEvent creates a new calendar event.
func (s *CalendarService) CreateEvent(ctx context.Context, event CalendarEventInput) (*CalendarEvent, error) {
	if globalOAuthManager == nil {
		return nil, fmt.Errorf("OAuth not configured")
	}

	calID := calendarID(s.cfg)
	reqBody := buildGCalBody(event)

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal event: %w", err)
	}

	reqURL := fmt.Sprintf("%s/%s/events", calendarBaseURL, url.PathEscape(calID))

	resp, err := globalOAuthManager.Request(ctx, "google", "POST", reqURL, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return nil, fmt.Errorf("create event: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("calendar API error %d: %s", resp.StatusCode, truncateStr(string(respBody), 500))
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	ev := parseGCalEvent(result)
	return &ev, nil
}

// UpdateEvent updates an existing calendar event.
func (s *CalendarService) UpdateEvent(ctx context.Context, eventID string, event CalendarEventInput) (*CalendarEvent, error) {
	if globalOAuthManager == nil {
		return nil, fmt.Errorf("OAuth not configured")
	}

	calID := calendarID(s.cfg)
	reqBody := buildGCalBody(event)

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal event: %w", err)
	}

	reqURL := fmt.Sprintf("%s/%s/events/%s", calendarBaseURL, url.PathEscape(calID), url.PathEscape(eventID))

	resp, err := globalOAuthManager.Request(ctx, "google", "PATCH", reqURL, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return nil, fmt.Errorf("update event: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("calendar API error %d: %s", resp.StatusCode, truncateStr(string(respBody), 500))
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	ev := parseGCalEvent(result)
	return &ev, nil
}

// DeleteEvent deletes a calendar event.
func (s *CalendarService) DeleteEvent(ctx context.Context, eventID string) error {
	if globalOAuthManager == nil {
		return fmt.Errorf("OAuth not configured")
	}

	calID := calendarID(s.cfg)
	reqURL := fmt.Sprintf("%s/%s/events/%s", calendarBaseURL, url.PathEscape(calID), url.PathEscape(eventID))

	resp, err := globalOAuthManager.Request(ctx, "google", "DELETE", reqURL, nil)
	if err != nil {
		return fmt.Errorf("delete event: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 204 && resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("calendar API error %d: %s", resp.StatusCode, truncateStr(string(body), 500))
	}

	return nil
}

// SearchEvents searches for events matching a query.
func (s *CalendarService) SearchEvents(ctx context.Context, query string, timeMin, timeMax string) ([]CalendarEvent, error) {
	if globalOAuthManager == nil {
		return nil, fmt.Errorf("OAuth not configured")
	}

	calID := calendarID(s.cfg)
	maxResults := calendarMaxResults(s.cfg)

	params := url.Values{}
	params.Set("q", query)
	if timeMin != "" {
		params.Set("timeMin", timeMin)
	}
	if timeMax != "" {
		params.Set("timeMax", timeMax)
	}
	params.Set("maxResults", strconv.Itoa(maxResults))
	params.Set("singleEvents", "true")
	params.Set("orderBy", "startTime")

	reqURL := fmt.Sprintf("%s/%s/events?%s", calendarBaseURL, url.PathEscape(calID), params.Encode())

	resp, err := globalOAuthManager.Request(ctx, "google", "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("search events: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("calendar API error %d: %s", resp.StatusCode, truncateStr(string(body), 500))
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	items, _ := result["items"].([]any)
	events := make([]CalendarEvent, 0, len(items))
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			events = append(events, parseGCalEvent(m))
		}
	}

	return events, nil
}

// --- Tool Handlers ---

// toolCalendarList handles the calendar_list tool.
func toolCalendarList(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if !cfg.Calendar.Enabled {
		return "", fmt.Errorf("calendar integration is not enabled")
	}
	if globalCalendarService == nil {
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

	events, err := globalCalendarService.ListEvents(ctx, args.TimeMin, args.TimeMax, args.MaxResults)
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
	if globalCalendarService == nil {
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
		eventInput.TimeZone = calendarTimeZone(cfg)
	}

	ev, err := globalCalendarService.CreateEvent(ctx, eventInput)
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
	if globalCalendarService == nil {
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
		eventInput.TimeZone = calendarTimeZone(cfg)
	}

	ev, err := globalCalendarService.UpdateEvent(ctx, args.EventID, eventInput)
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
	if globalCalendarService == nil {
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

	if err := globalCalendarService.DeleteEvent(ctx, args.EventID); err != nil {
		return "", err
	}

	return fmt.Sprintf("Event %s deleted successfully.", args.EventID), nil
}

// toolCalendarSearch handles the calendar_search tool.
func toolCalendarSearch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if !cfg.Calendar.Enabled {
		return "", fmt.Errorf("calendar integration is not enabled")
	}
	if globalCalendarService == nil {
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

	events, err := globalCalendarService.SearchEvents(ctx, args.Query, args.TimeMin, args.TimeMax)
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

