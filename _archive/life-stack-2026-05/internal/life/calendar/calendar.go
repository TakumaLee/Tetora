// Package calendar implements the Google Calendar integration service.
package calendar

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

const BaseURL = "https://www.googleapis.com/calendar/v3/calendars"

// Event represents a parsed calendar event.
type Event struct {
	ID          string   `json:"id"`
	Summary     string   `json:"summary"`
	Description string   `json:"description,omitempty"`
	Location    string   `json:"location,omitempty"`
	Start       string   `json:"start"`
	End         string   `json:"end"`
	Status      string   `json:"status"`
	HtmlLink    string   `json:"htmlLink,omitempty"`
	Attendees   []string `json:"attendees,omitempty"`
	AllDay      bool     `json:"allDay,omitempty"`
}

// EventInput represents input for creating/updating an event.
type EventInput struct {
	Summary     string   `json:"summary"`
	Description string   `json:"description,omitempty"`
	Location    string   `json:"location,omitempty"`
	Start       string   `json:"start"`
	End         string   `json:"end"`
	TimeZone    string   `json:"timeZone,omitempty"`
	Attendees   []string `json:"attendees,omitempty"`
	AllDay      bool     `json:"allDay,omitempty"`
}

// OAuthRequester abstracts the OAuth HTTP request mechanism.
type OAuthRequester interface {
	Request(ctx context.Context, provider, method, url string, body io.Reader) (*OAuthResponse, error)
}

// OAuthResponse is a minimal HTTP response wrapper.
type OAuthResponse struct {
	StatusCode int
	Body       io.ReadCloser
}

// Service wraps the Google Calendar API.
type Service struct {
	calendarID string
	timeZone   string
	maxResults int
	oauth      OAuthRequester
}

// New creates a new calendar Service.
// calendarID defaults to "primary" if empty.
// timeZone defaults to local timezone if empty.
// maxResults defaults to 10 if <= 0.
func New(calendarID, timeZone string, maxResults int, oauth OAuthRequester) *Service {
	if calendarID == "" {
		calendarID = "primary"
	}
	if timeZone == "" {
		timeZone = time.Now().Location().String()
	}
	if maxResults <= 0 {
		maxResults = 10
	}
	return &Service{
		calendarID: calendarID,
		timeZone:   timeZone,
		maxResults: maxResults,
		oauth:      oauth,
	}
}

// ListEvents lists events from the calendar within a time range.
func (s *Service) ListEvents(ctx context.Context, timeMin, timeMax string, maxResults int) ([]Event, error) {
	if s.oauth == nil {
		return nil, fmt.Errorf("OAuth not configured")
	}

	if maxResults <= 0 {
		maxResults = s.maxResults
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

	reqURL := fmt.Sprintf("%s/%s/events?%s", BaseURL, url.PathEscape(s.calendarID), params.Encode())

	resp, err := s.oauth.Request(ctx, "google", "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("calendar API error %d: %s", resp.StatusCode, truncate(string(body), 500))
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	items, _ := result["items"].([]any)
	events := make([]Event, 0, len(items))
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			events = append(events, parseEvent(m))
		}
	}

	return events, nil
}

// CreateEvent creates a new calendar event.
func (s *Service) CreateEvent(ctx context.Context, event EventInput) (*Event, error) {
	if s.oauth == nil {
		return nil, fmt.Errorf("OAuth not configured")
	}

	reqBody := buildBody(event, s.timeZone)
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal event: %w", err)
	}

	reqURL := fmt.Sprintf("%s/%s/events", BaseURL, url.PathEscape(s.calendarID))

	resp, err := s.oauth.Request(ctx, "google", "POST", reqURL, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return nil, fmt.Errorf("create event: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("calendar API error %d: %s", resp.StatusCode, truncate(string(respBody), 500))
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	ev := parseEvent(result)
	return &ev, nil
}

// UpdateEvent updates an existing calendar event.
func (s *Service) UpdateEvent(ctx context.Context, eventID string, event EventInput) (*Event, error) {
	if s.oauth == nil {
		return nil, fmt.Errorf("OAuth not configured")
	}

	reqBody := buildBody(event, s.timeZone)
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal event: %w", err)
	}

	reqURL := fmt.Sprintf("%s/%s/events/%s", BaseURL, url.PathEscape(s.calendarID), url.PathEscape(eventID))

	resp, err := s.oauth.Request(ctx, "google", "PATCH", reqURL, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return nil, fmt.Errorf("update event: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("calendar API error %d: %s", resp.StatusCode, truncate(string(respBody), 500))
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	ev := parseEvent(result)
	return &ev, nil
}

// DeleteEvent deletes a calendar event.
func (s *Service) DeleteEvent(ctx context.Context, eventID string) error {
	if s.oauth == nil {
		return fmt.Errorf("OAuth not configured")
	}

	reqURL := fmt.Sprintf("%s/%s/events/%s", BaseURL, url.PathEscape(s.calendarID), url.PathEscape(eventID))

	resp, err := s.oauth.Request(ctx, "google", "DELETE", reqURL, nil)
	if err != nil {
		return fmt.Errorf("delete event: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 204 && resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("calendar API error %d: %s", resp.StatusCode, truncate(string(body), 500))
	}

	return nil
}

// SearchEvents searches for events matching a query.
func (s *Service) SearchEvents(ctx context.Context, query string, timeMin, timeMax string) ([]Event, error) {
	if s.oauth == nil {
		return nil, fmt.Errorf("OAuth not configured")
	}

	params := url.Values{}
	params.Set("q", query)
	if timeMin != "" {
		params.Set("timeMin", timeMin)
	}
	if timeMax != "" {
		params.Set("timeMax", timeMax)
	}
	params.Set("maxResults", strconv.Itoa(s.maxResults))
	params.Set("singleEvents", "true")
	params.Set("orderBy", "startTime")

	reqURL := fmt.Sprintf("%s/%s/events?%s", BaseURL, url.PathEscape(s.calendarID), params.Encode())

	resp, err := s.oauth.Request(ctx, "google", "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("search events: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("calendar API error %d: %s", resp.StatusCode, truncate(string(body), 500))
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	items, _ := result["items"].([]any)
	events := make([]Event, 0, len(items))
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			events = append(events, parseEvent(m))
		}
	}

	return events, nil
}

// TimeZone returns the configured timezone string.
func (s *Service) TimeZone() string {
	return s.timeZone
}

// --- Natural Language Schedule Parser ---

// ParseNaturalSchedule parses natural language scheduling input into an EventInput.
func ParseNaturalSchedule(text string) (*EventInput, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("empty schedule input")
	}

	now := time.Now()
	loc := now.Location()

	if ev, ok := parseJaSchedule(text, now, loc); ok {
		return ev, nil
	}
	if ev, ok := parseZhSchedule(text, now, loc); ok {
		return ev, nil
	}
	if ev, ok := parseEnSchedule(text, now, loc); ok {
		return ev, nil
	}

	return nil, fmt.Errorf("cannot parse schedule: %q", text)
}

func parseJaSchedule(text string, now time.Time, loc *time.Location) (*EventInput, bool) {
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

	reTime := regexp.MustCompile(`^(\d{1,2})時(?:(\d{1,2})分)?`)
	m := reTime.FindStringSubmatch(rest)
	h, min := 9, 0
	if m != nil {
		h, _ = strconv.Atoi(m[1])
		if m[2] != "" {
			min, _ = strconv.Atoi(m[2])
		}
		rest = rest[len(m[0]):]
	}

	summary := strings.TrimPrefix(rest, "の")
	summary = strings.TrimSpace(summary)
	if summary == "" {
		summary = "予定"
	}

	startTime := time.Date(baseDate.Year(), baseDate.Month(), baseDate.Day(), h, min, 0, 0, loc)
	endTime := startTime.Add(1 * time.Hour)

	return &EventInput{
		Summary:  summary,
		Start:    startTime.Format(time.RFC3339),
		End:      endTime.Format(time.RFC3339),
		TimeZone: loc.String(),
	}, true
}

func parseZhSchedule(text string, now time.Time, loc *time.Location) (*EventInput, bool) {
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

	if strings.HasPrefix(rest, "下午") {
		offset = 12
		rest = strings.TrimPrefix(rest, "下午")
	} else if strings.HasPrefix(rest, "上午") {
		rest = strings.TrimPrefix(rest, "上午")
	}

	reTime := regexp.MustCompile(`^(\d{1,2})點(?:(\d{1,2})分)?`)
	m := reTime.FindStringSubmatch(rest)
	if m != nil {
		h, _ = strconv.Atoi(m[1])
		h += offset
		if h == 24 {
			h = 12
		}
		if m[2] != "" {
			min, _ = strconv.Atoi(m[2])
		}
		rest = rest[len(m[0]):]
	}

	summary := strings.TrimPrefix(rest, "的")
	summary = strings.TrimSpace(summary)
	if summary == "" {
		summary = "活動"
	}

	startTime := time.Date(baseDate.Year(), baseDate.Month(), baseDate.Day(), h, min, 0, 0, loc)
	endTime := startTime.Add(1 * time.Hour)

	return &EventInput{
		Summary:  summary,
		Start:    startTime.Format(time.RFC3339),
		End:      endTime.Format(time.RFC3339),
		TimeZone: loc.String(),
	}, true
}

func parseEnSchedule(text string, now time.Time, loc *time.Location) (*EventInput, bool) {
	lower := strings.ToLower(text)

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

	h, min := 9, 0
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

	summary := text
	for _, w := range []string{"tomorrow", "today", "Tomorrow", "Today"} {
		summary = strings.ReplaceAll(summary, w, "")
	}
	reAtFull := regexp.MustCompile(`(?i)at\s+\d{1,2}(?::\d{2})?\s*(?:am|pm)?`)
	summary = reAtFull.ReplaceAllString(summary, "")
	summary = strings.TrimSpace(summary)
	if summary == "" {
		summary = "Event"
	}

	startTime := time.Date(baseDate.Year(), baseDate.Month(), baseDate.Day(), h, min, 0, 0, loc)
	endTime := startTime.Add(1 * time.Hour)

	return &EventInput{
		Summary:  summary,
		Start:    startTime.Format(time.RFC3339),
		End:      endTime.Format(time.RFC3339),
		TimeZone: loc.String(),
	}, true
}

// --- Helpers ---

func parseEvent(item map[string]any) Event {
	ev := Event{}

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

	if startObj, ok := item["start"].(map[string]any); ok {
		if dt, ok := startObj["dateTime"].(string); ok {
			ev.Start = dt
		} else if d, ok := startObj["date"].(string); ok {
			ev.Start = d
			ev.AllDay = true
		}
	}

	if endObj, ok := item["end"].(map[string]any); ok {
		if dt, ok := endObj["dateTime"].(string); ok {
			ev.End = dt
		} else if d, ok := endObj["date"].(string); ok {
			ev.End = d
		}
	}

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

func buildBody(input EventInput, defaultTZ string) map[string]any {
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
		tz = defaultTZ
	}

	if input.AllDay {
		startDate := input.Start
		endDate := input.End
		if len(startDate) > 10 {
			startDate = startDate[:10]
		}
		if endDate == "" {
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
		body["start"] = map[string]any{
			"dateTime": input.Start,
			"timeZone": tz,
		}
		endTime := input.End
		if endTime == "" {
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

	if len(input.Attendees) > 0 {
		attendees := make([]map[string]any, len(input.Attendees))
		for i, email := range input.Attendees {
			attendees[i] = map[string]any{"email": email}
		}
		body["attendees"] = attendees
	}

	return body
}

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}
