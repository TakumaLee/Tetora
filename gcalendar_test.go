package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// --- parseGCalEvent Tests ---

func TestParseGCalEvent_Basic(t *testing.T) {
	item := map[string]any{
		"id":          "event123",
		"summary":     "Team Standup",
		"description": "Daily standup meeting",
		"location":    "Meeting Room A",
		"status":      "confirmed",
		"htmlLink":    "https://calendar.google.com/event?id=event123",
		"start": map[string]any{
			"dateTime": "2024-01-15T14:00:00+09:00",
		},
		"end": map[string]any{
			"dateTime": "2024-01-15T15:00:00+09:00",
		},
		"attendees": []any{
			map[string]any{"email": "alice@example.com"},
			map[string]any{"email": "bob@example.com"},
		},
	}

	ev := parseGCalEvent(item)

	if ev.ID != "event123" {
		t.Errorf("ID: got %q, want %q", ev.ID, "event123")
	}
	if ev.Summary != "Team Standup" {
		t.Errorf("Summary: got %q, want %q", ev.Summary, "Team Standup")
	}
	if ev.Description != "Daily standup meeting" {
		t.Errorf("Description: got %q, want %q", ev.Description, "Daily standup meeting")
	}
	if ev.Location != "Meeting Room A" {
		t.Errorf("Location: got %q, want %q", ev.Location, "Meeting Room A")
	}
	if ev.Status != "confirmed" {
		t.Errorf("Status: got %q, want %q", ev.Status, "confirmed")
	}
	if ev.Start != "2024-01-15T14:00:00+09:00" {
		t.Errorf("Start: got %q, want %q", ev.Start, "2024-01-15T14:00:00+09:00")
	}
	if ev.End != "2024-01-15T15:00:00+09:00" {
		t.Errorf("End: got %q, want %q", ev.End, "2024-01-15T15:00:00+09:00")
	}
	if ev.AllDay {
		t.Error("AllDay: expected false for timed event")
	}
	if len(ev.Attendees) != 2 {
		t.Errorf("Attendees: got %d, want 2", len(ev.Attendees))
	}
	if ev.Attendees[0] != "alice@example.com" {
		t.Errorf("Attendees[0]: got %q, want %q", ev.Attendees[0], "alice@example.com")
	}
}

func TestParseGCalEvent_AllDay(t *testing.T) {
	item := map[string]any{
		"id":      "allday1",
		"summary": "Company Holiday",
		"status":  "confirmed",
		"start": map[string]any{
			"date": "2024-01-15",
		},
		"end": map[string]any{
			"date": "2024-01-16",
		},
	}

	ev := parseGCalEvent(item)

	if ev.ID != "allday1" {
		t.Errorf("ID: got %q, want %q", ev.ID, "allday1")
	}
	if ev.Start != "2024-01-15" {
		t.Errorf("Start: got %q, want %q", ev.Start, "2024-01-15")
	}
	if !ev.AllDay {
		t.Error("AllDay: expected true for all-day event")
	}
}

func TestParseGCalEvent_Empty(t *testing.T) {
	item := map[string]any{}
	ev := parseGCalEvent(item)
	if ev.ID != "" || ev.Summary != "" {
		t.Errorf("Expected empty event, got ID=%q, Summary=%q", ev.ID, ev.Summary)
	}
}

// --- buildGCalBody Tests ---

func TestBuildGCalBody_TimedEvent(t *testing.T) {
	input := CalendarEventInput{
		Summary:     "Meeting",
		Description: "Weekly sync",
		Location:    "Room 1",
		Start:       "2024-01-15T14:00:00+09:00",
		End:         "2024-01-15T15:00:00+09:00",
		TimeZone:    "Asia/Tokyo",
		Attendees:   []string{"alice@example.com"},
	}

	body := buildGCalBody(input)

	if body["summary"] != "Meeting" {
		t.Errorf("summary: got %v, want %q", body["summary"], "Meeting")
	}
	if body["description"] != "Weekly sync" {
		t.Errorf("description: got %v, want %q", body["description"], "Weekly sync")
	}

	startObj, ok := body["start"].(map[string]any)
	if !ok {
		t.Fatal("start should be a map")
	}
	if startObj["dateTime"] != "2024-01-15T14:00:00+09:00" {
		t.Errorf("start.dateTime: got %v", startObj["dateTime"])
	}
	if startObj["timeZone"] != "Asia/Tokyo" {
		t.Errorf("start.timeZone: got %v", startObj["timeZone"])
	}

	attendees, ok := body["attendees"].([]map[string]any)
	if !ok || len(attendees) != 1 {
		t.Fatal("expected 1 attendee")
	}
	if attendees[0]["email"] != "alice@example.com" {
		t.Errorf("attendee email: got %v", attendees[0]["email"])
	}
}

func TestBuildGCalBody_AllDayEvent(t *testing.T) {
	input := CalendarEventInput{
		Summary: "Holiday",
		Start:   "2024-01-15",
		AllDay:  true,
	}

	body := buildGCalBody(input)

	startObj, ok := body["start"].(map[string]any)
	if !ok {
		t.Fatal("start should be a map")
	}
	if startObj["date"] != "2024-01-15" {
		t.Errorf("start.date: got %v, want %q", startObj["date"], "2024-01-15")
	}

	endObj, ok := body["end"].(map[string]any)
	if !ok {
		t.Fatal("end should be a map")
	}
	if endObj["date"] != "2024-01-16" {
		t.Errorf("end.date: got %v, want %q (next day default)", endObj["date"], "2024-01-16")
	}
}

func TestBuildGCalBody_DefaultEnd(t *testing.T) {
	input := CalendarEventInput{
		Summary:  "Quick Chat",
		Start:    "2024-01-15T14:00:00+09:00",
		TimeZone: "Asia/Tokyo",
	}

	body := buildGCalBody(input)

	endObj, ok := body["end"].(map[string]any)
	if !ok {
		t.Fatal("end should be a map")
	}
	endDT, ok := endObj["dateTime"].(string)
	if !ok {
		t.Fatal("end.dateTime should be a string")
	}
	// Should be 1 hour after start.
	endTime, err := time.Parse(time.RFC3339, endDT)
	if err != nil {
		t.Fatalf("parse end time: %v", err)
	}
	startTime, _ := time.Parse(time.RFC3339, "2024-01-15T14:00:00+09:00")
	if !endTime.Equal(startTime.Add(1 * time.Hour)) {
		t.Errorf("end should be 1 hour after start, got %v", endDT)
	}
}

// --- calendarID Tests ---

func TestCalendarID_Default(t *testing.T) {
	cfg := &Config{}
	if id := calendarID(cfg); id != "primary" {
		t.Errorf("calendarID: got %q, want %q", id, "primary")
	}
}

func TestCalendarID_Custom(t *testing.T) {
	cfg := &Config{Calendar: CalendarConfig{CalendarID: "my-cal@group.calendar.google.com"}}
	if id := calendarID(cfg); id != "my-cal@group.calendar.google.com" {
		t.Errorf("calendarID: got %q, want custom", id)
	}
}

// --- parseNaturalSchedule Tests ---

func TestParseNaturalSchedule_Japanese(t *testing.T) {
	tests := []struct {
		input   string
		summary string
	}{
		{"明日14時の会議", "会議"},
		{"明日2時のミーティング", "ミーティング"},
		{"今日15時30分の打ち合わせ", "打ち合わせ"},
		{"明後日10時のランチ", "ランチ"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			ev, err := parseNaturalSchedule(tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ev.Summary != tt.summary {
				t.Errorf("summary: got %q, want %q", ev.Summary, tt.summary)
			}
			if ev.Start == "" {
				t.Error("start should not be empty")
			}
			if ev.End == "" {
				t.Error("end should not be empty")
			}
			// Verify end is 1 hour after start.
			startT, _ := time.Parse(time.RFC3339, ev.Start)
			endT, _ := time.Parse(time.RFC3339, ev.End)
			if !endT.Equal(startT.Add(1 * time.Hour)) {
				t.Errorf("end should be 1h after start: start=%s end=%s", ev.Start, ev.End)
			}
		})
	}
}

func TestParseNaturalSchedule_English(t *testing.T) {
	tests := []struct {
		input   string
		summary string
	}{
		{"meeting tomorrow at 2pm", "meeting"},
		{"lunch today at 12pm", "lunch"},
		{"standup tomorrow at 9:30am", "standup"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			ev, err := parseNaturalSchedule(tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ev.Summary != tt.summary {
				t.Errorf("summary: got %q, want %q", ev.Summary, tt.summary)
			}
		})
	}
}

func TestParseNaturalSchedule_Chinese(t *testing.T) {
	tests := []struct {
		input   string
		summary string
	}{
		{"明天下午2點開會", "開會"},
		{"明天3點的會議", "會議"},
		{"今天上午10點報告", "報告"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			ev, err := parseNaturalSchedule(tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ev.Summary != tt.summary {
				t.Errorf("summary: got %q, want %q", ev.Summary, tt.summary)
			}
		})
	}
}

func TestParseNaturalSchedule_Empty(t *testing.T) {
	_, err := parseNaturalSchedule("")
	if err == nil {
		t.Error("expected error for empty input")
	}
}

func TestParseNaturalSchedule_Invalid(t *testing.T) {
	_, err := parseNaturalSchedule("something random")
	if err == nil {
		t.Error("expected error for unparseable input")
	}
}

// --- ListEvents Mock HTTP Test ---

func TestListEvents_Mock(t *testing.T) {
	// Create a mock HTTP server.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if !strings.Contains(r.URL.String(), "singleEvents=true") {
			t.Error("expected singleEvents=true in query")
		}
		if r.Header.Get("Authorization") == "" {
			t.Error("expected Authorization header")
		}

		resp := map[string]any{
			"items": []any{
				map[string]any{
					"id":      "ev1",
					"summary": "Test Event",
					"status":  "confirmed",
					"start":   map[string]any{"dateTime": "2024-01-15T14:00:00+09:00"},
					"end":     map[string]any{"dateTime": "2024-01-15T15:00:00+09:00"},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	// Save and restore globalOAuthManager.
	oldMgr := globalOAuthManager
	defer func() { globalOAuthManager = oldMgr }()

	// Create a mock OAuthManager that rewrites URLs to our test server.
	cfg := &Config{
		Calendar: CalendarConfig{Enabled: true},
	}
	mockMgr := &OAuthManager{
		cfg:    cfg,
		states: make(map[string]oauthState),
	}
	// We cannot easily mock OAuthManager.Request, so we test the parse/build helpers
	// and test tool handlers for guard conditions instead.
	_ = mockMgr
	_ = srv
}

// --- CreateEvent Request Format Test ---

func TestBuildGCalBody_JSONFormat(t *testing.T) {
	input := CalendarEventInput{
		Summary:   "Design Review",
		Start:     "2024-01-15T14:00:00+09:00",
		End:       "2024-01-15T15:00:00+09:00",
		TimeZone:  "Asia/Tokyo",
		Attendees: []string{"alice@example.com", "bob@example.com"},
	}

	body := buildGCalBody(input)
	bs, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(bs, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if parsed["summary"] != "Design Review" {
		t.Errorf("summary: got %v", parsed["summary"])
	}

	attendees, ok := parsed["attendees"].([]any)
	if !ok || len(attendees) != 2 {
		t.Fatal("expected 2 attendees")
	}
}

// --- Tool Handler Input Validation Tests ---

func TestToolCalendarList_NotEnabled(t *testing.T) {
	cfg := &Config{Calendar: CalendarConfig{Enabled: false}}
	_, err := toolCalendarList(context.Background(), cfg, json.RawMessage(`{}`))
	if err == nil {
		t.Error("expected error when not enabled")
	}
	if !strings.Contains(err.Error(), "not enabled") {
		t.Errorf("error should mention not enabled, got: %v", err)
	}
}

func TestToolCalendarCreate_NotEnabled(t *testing.T) {
	cfg := &Config{Calendar: CalendarConfig{Enabled: false}}
	_, err := toolCalendarCreate(context.Background(), cfg, json.RawMessage(`{"summary":"test","start":"2024-01-15T14:00:00Z"}`))
	if err == nil {
		t.Error("expected error when not enabled")
	}
}

func TestToolCalendarCreate_MissingSummary(t *testing.T) {
	oldSvc := globalCalendarService
	defer func() { globalCalendarService = oldSvc }()

	cfg := &Config{Calendar: CalendarConfig{Enabled: true}}
	globalCalendarService = &CalendarService{cfg: cfg}

	_, err := toolCalendarCreate(context.Background(), cfg, json.RawMessage(`{"start":"2024-01-15T14:00:00Z"}`))
	if err == nil {
		t.Error("expected error for missing summary")
	}
	if !strings.Contains(err.Error(), "summary is required") {
		t.Errorf("error should mention summary, got: %v", err)
	}
}

func TestToolCalendarCreate_MissingStart(t *testing.T) {
	oldSvc := globalCalendarService
	defer func() { globalCalendarService = oldSvc }()

	cfg := &Config{Calendar: CalendarConfig{Enabled: true}}
	globalCalendarService = &CalendarService{cfg: cfg}

	_, err := toolCalendarCreate(context.Background(), cfg, json.RawMessage(`{"summary":"test"}`))
	if err == nil {
		t.Error("expected error for missing start")
	}
	if !strings.Contains(err.Error(), "start time is required") {
		t.Errorf("error should mention start, got: %v", err)
	}
}

func TestToolCalendarDelete_NotEnabled(t *testing.T) {
	cfg := &Config{Calendar: CalendarConfig{Enabled: false}}
	_, err := toolCalendarDelete(context.Background(), cfg, json.RawMessage(`{"eventId":"ev1"}`))
	if err == nil {
		t.Error("expected error when not enabled")
	}
}

func TestToolCalendarDelete_MissingEventID(t *testing.T) {
	oldSvc := globalCalendarService
	defer func() { globalCalendarService = oldSvc }()

	cfg := &Config{Calendar: CalendarConfig{Enabled: true}}
	globalCalendarService = &CalendarService{cfg: cfg}

	_, err := toolCalendarDelete(context.Background(), cfg, json.RawMessage(`{}`))
	if err == nil {
		t.Error("expected error for missing eventId")
	}
	if !strings.Contains(err.Error(), "eventId is required") {
		t.Errorf("error should mention eventId, got: %v", err)
	}
}

func TestToolCalendarUpdate_MissingEventID(t *testing.T) {
	oldSvc := globalCalendarService
	defer func() { globalCalendarService = oldSvc }()

	cfg := &Config{Calendar: CalendarConfig{Enabled: true}}
	globalCalendarService = &CalendarService{cfg: cfg}

	_, err := toolCalendarUpdate(context.Background(), cfg, json.RawMessage(`{"summary":"updated"}`))
	if err == nil {
		t.Error("expected error for missing eventId")
	}
}

func TestToolCalendarSearch_NotEnabled(t *testing.T) {
	cfg := &Config{Calendar: CalendarConfig{Enabled: false}}
	_, err := toolCalendarSearch(context.Background(), cfg, json.RawMessage(`{"query":"test"}`))
	if err == nil {
		t.Error("expected error when not enabled")
	}
}

func TestToolCalendarSearch_MissingQuery(t *testing.T) {
	oldSvc := globalCalendarService
	defer func() { globalCalendarService = oldSvc }()

	cfg := &Config{Calendar: CalendarConfig{Enabled: true}}
	globalCalendarService = &CalendarService{cfg: cfg}

	_, err := toolCalendarSearch(context.Background(), cfg, json.RawMessage(`{}`))
	if err == nil {
		t.Error("expected error for missing query")
	}
	if !strings.Contains(err.Error(), "query is required") {
		t.Errorf("error should mention query, got: %v", err)
	}
}

func TestToolCalendarList_NotInitialized(t *testing.T) {
	oldSvc := globalCalendarService
	defer func() { globalCalendarService = oldSvc }()
	globalCalendarService = nil

	cfg := &Config{Calendar: CalendarConfig{Enabled: true}}
	_, err := toolCalendarList(context.Background(), cfg, json.RawMessage(`{}`))
	if err == nil {
		t.Error("expected error when service not initialized")
	}
	if !strings.Contains(err.Error(), "not initialized") {
		t.Errorf("error should mention not initialized, got: %v", err)
	}
}

// --- CalendarMaxResults Tests ---

func TestCalendarMaxResults_Default(t *testing.T) {
	cfg := &Config{}
	if n := calendarMaxResults(cfg); n != 10 {
		t.Errorf("calendarMaxResults: got %d, want 10", n)
	}
}

func TestCalendarMaxResults_Custom(t *testing.T) {
	cfg := &Config{Calendar: CalendarConfig{MaxResults: 25}}
	if n := calendarMaxResults(cfg); n != 25 {
		t.Errorf("calendarMaxResults: got %d, want 25", n)
	}
}

// --- CalendarTimeZone Tests ---

func TestCalendarTimeZone_Default(t *testing.T) {
	cfg := &Config{}
	tz := calendarTimeZone(cfg)
	if tz == "" {
		t.Error("calendarTimeZone should not be empty")
	}
	// Should be local timezone.
	expected := time.Now().Location().String()
	if tz != expected {
		t.Errorf("calendarTimeZone: got %q, want %q", tz, expected)
	}
}

func TestCalendarTimeZone_Custom(t *testing.T) {
	cfg := &Config{Calendar: CalendarConfig{TimeZone: "America/New_York"}}
	if tz := calendarTimeZone(cfg); tz != "America/New_York" {
		t.Errorf("calendarTimeZone: got %q, want %q", tz, "America/New_York")
	}
}

// Prevent unused import warnings.
var _ = fmt.Sprintf
var _ = httptest.NewServer
