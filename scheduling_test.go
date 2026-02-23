package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// setupSchedulingTest creates a SchedulingService for testing and returns
// a cleanup function that restores the original global state.
func setupSchedulingTest(t *testing.T) (*SchedulingService, func()) {
	t.Helper()

	cfg := &Config{}
	svc := newSchedulingService(cfg)

	oldScheduling := globalSchedulingService
	oldCalendar := globalCalendarService
	oldTaskMgr := globalTaskManager

	globalSchedulingService = svc
	globalCalendarService = nil
	globalTaskManager = nil

	cleanup := func() {
		globalSchedulingService = oldScheduling
		globalCalendarService = oldCalendar
		globalTaskManager = oldTaskMgr
	}

	return svc, cleanup
}

func TestNewSchedulingService(t *testing.T) {
	cfg := &Config{}
	svc := newSchedulingService(cfg)
	if svc == nil {
		t.Fatal("expected non-nil service")
	}
	if svc.cfg != cfg {
		t.Fatal("expected cfg to be stored")
	}
}

func TestViewSchedule_NoServices(t *testing.T) {
	svc, cleanup := setupSchedulingTest(t)
	defer cleanup()

	// Both globalCalendarService and globalTaskManager are nil.
	schedules, err := svc.ViewSchedule("", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(schedules) != 1 {
		t.Fatalf("expected 1 day, got %d", len(schedules))
	}

	day := schedules[0]
	today := time.Now().Format("2006-01-02")
	if day.Date != today {
		t.Errorf("expected date %s, got %s", today, day.Date)
	}
	if len(day.Events) != 0 {
		t.Errorf("expected 0 events, got %d", len(day.Events))
	}
	if day.BusyHours != 0 {
		t.Errorf("expected 0 busy hours, got %f", day.BusyHours)
	}
	// Should have 1 free slot = full working hours.
	if len(day.FreeSlots) != 1 {
		t.Errorf("expected 1 free slot (full working day), got %d", len(day.FreeSlots))
	}
	if day.FreeHours != 9 {
		t.Errorf("expected 9 free hours, got %f", day.FreeHours)
	}
}

func TestViewSchedule_MultipleDays(t *testing.T) {
	svc, cleanup := setupSchedulingTest(t)
	defer cleanup()

	schedules, err := svc.ViewSchedule("2026-03-01", 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(schedules) != 3 {
		t.Fatalf("expected 3 days, got %d", len(schedules))
	}
	expected := []string{"2026-03-01", "2026-03-02", "2026-03-03"}
	for i, day := range schedules {
		if day.Date != expected[i] {
			t.Errorf("day %d: expected %s, got %s", i, expected[i], day.Date)
		}
	}
}

func TestViewSchedule_InvalidDate(t *testing.T) {
	svc, cleanup := setupSchedulingTest(t)
	defer cleanup()

	_, err := svc.ViewSchedule("not-a-date", 1)
	if err == nil {
		t.Fatal("expected error for invalid date")
	}
}

func TestViewSchedule_WithEvents(t *testing.T) {
	svc, cleanup := setupSchedulingTest(t)
	defer cleanup()

	// We test the internal method by directly constructing events and
	// using findFreeSlotsInDay (since we can't easily mock the calendar service).
	loc := time.Now().Location()
	day := time.Date(2026, 3, 15, 0, 0, 0, 0, loc)
	whStart := time.Date(2026, 3, 15, 9, 0, 0, 0, loc)
	whEnd := time.Date(2026, 3, 15, 18, 0, 0, 0, loc)

	events := []ScheduleEvent{
		{
			Title:  "Standup",
			Start:  time.Date(2026, 3, 15, 10, 0, 0, 0, loc),
			End:    time.Date(2026, 3, 15, 10, 30, 0, 0, loc),
			Source: "calendar",
		},
		{
			Title:  "Design Review",
			Start:  time.Date(2026, 3, 15, 14, 0, 0, 0, loc),
			End:    time.Date(2026, 3, 15, 15, 0, 0, 0, loc),
			Source: "calendar",
		},
	}

	_ = day
	freeSlots := svc.findFreeSlotsInDay(events, whStart, whEnd)

	// Expected free slots: 09:00-10:00, 10:30-14:00, 15:00-18:00
	if len(freeSlots) != 3 {
		t.Fatalf("expected 3 free slots, got %d", len(freeSlots))
	}

	// First slot: 09:00-10:00 = 60 min
	if freeSlots[0].Duration != 60 {
		t.Errorf("slot 0: expected 60 min, got %d", freeSlots[0].Duration)
	}
	// Second slot: 10:30-14:00 = 210 min
	if freeSlots[1].Duration != 210 {
		t.Errorf("slot 1: expected 210 min, got %d", freeSlots[1].Duration)
	}
	// Third slot: 15:00-18:00 = 180 min
	if freeSlots[2].Duration != 180 {
		t.Errorf("slot 2: expected 180 min, got %d", freeSlots[2].Duration)
	}
}

func TestFindFreeSlots_FullDay(t *testing.T) {
	svc, cleanup := setupSchedulingTest(t)
	defer cleanup()

	loc := time.Now().Location()
	start := time.Date(2026, 3, 15, 9, 0, 0, 0, loc)
	end := time.Date(2026, 3, 15, 18, 0, 0, 0, loc)

	slots, err := svc.FindFreeSlots(start, end, 30)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// No events, so the entire range should be one free slot.
	if len(slots) != 1 {
		t.Fatalf("expected 1 free slot, got %d", len(slots))
	}
	if slots[0].Duration != 540 { // 9 hours = 540 min
		t.Errorf("expected 540 min, got %d", slots[0].Duration)
	}
}

func TestFindFreeSlots_WithEvents(t *testing.T) {
	// Since FindFreeSlots calls fetchCalendarEvents and fetchTaskDeadlines
	// which return nil when globals are nil, this effectively tests with no events.
	svc, cleanup := setupSchedulingTest(t)
	defer cleanup()

	loc := time.Now().Location()
	start := time.Date(2026, 3, 15, 9, 0, 0, 0, loc)
	end := time.Date(2026, 3, 15, 18, 0, 0, 0, loc)

	slots, err := svc.FindFreeSlots(start, end, 60)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(slots) != 1 {
		t.Fatalf("expected 1 slot, got %d", len(slots))
	}
	if slots[0].Duration != 540 {
		t.Errorf("expected 540 min, got %d", slots[0].Duration)
	}
}

func TestFindFreeSlots_NoSpace(t *testing.T) {
	svc, cleanup := setupSchedulingTest(t)
	defer cleanup()

	loc := time.Now().Location()
	start := time.Date(2026, 3, 15, 9, 0, 0, 0, loc)
	end := time.Date(2026, 3, 15, 9, 10, 0, 0, loc)

	// Only 10 minutes available, but we need at least 30.
	slots, err := svc.FindFreeSlots(start, end, 30)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(slots) != 0 {
		t.Errorf("expected 0 slots, got %d", len(slots))
	}
}

func TestFindFreeSlots_InvalidRange(t *testing.T) {
	svc, cleanup := setupSchedulingTest(t)
	defer cleanup()

	loc := time.Now().Location()
	start := time.Date(2026, 3, 15, 18, 0, 0, 0, loc)
	end := time.Date(2026, 3, 15, 9, 0, 0, 0, loc)

	_, err := svc.FindFreeSlots(start, end, 30)
	if err == nil {
		t.Fatal("expected error for invalid range")
	}
}

func TestSuggestSlots_Basic(t *testing.T) {
	svc, cleanup := setupSchedulingTest(t)
	defer cleanup()

	suggestions, err := svc.SuggestSlots(60, false, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// With no events, there should be suggestions.
	if len(suggestions) == 0 {
		t.Fatal("expected at least 1 suggestion")
	}
	if len(suggestions) > 5 {
		t.Errorf("expected at most 5 suggestions, got %d", len(suggestions))
	}

	// All suggestions should have duration 60.
	for i, s := range suggestions {
		if s.Slot.Duration != 60 {
			t.Errorf("suggestion %d: expected 60 min, got %d", i, s.Slot.Duration)
		}
		if s.Score < 0 || s.Score > 1 {
			t.Errorf("suggestion %d: score %f out of [0,1] range", i, s.Score)
		}
		if s.Reason == "" {
			t.Errorf("suggestion %d: empty reason", i)
		}
	}

	// Verify sorted by score descending.
	for i := 1; i < len(suggestions); i++ {
		if suggestions[i].Score > suggestions[i-1].Score {
			t.Errorf("suggestions not sorted: [%d].Score=%f > [%d].Score=%f", i, suggestions[i].Score, i-1, suggestions[i-1].Score)
		}
	}
}

func TestSuggestSlots_PreferMorning(t *testing.T) {
	svc, cleanup := setupSchedulingTest(t)
	defer cleanup()

	suggestions, err := svc.SuggestSlots(60, true, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(suggestions) == 0 {
		t.Fatal("expected at least 1 suggestion")
	}

	// The top suggestion should be a morning slot (before noon).
	topHour := suggestions[0].Slot.Start.Hour()
	if topHour >= 12 {
		t.Errorf("expected morning slot as top suggestion, got hour %d", topHour)
	}
}

func TestSuggestSlots_NoFreeTime(t *testing.T) {
	svc, cleanup := setupSchedulingTest(t)
	defer cleanup()

	// Request a 600-minute slot (10 hours) — impossible in a 9-hour workday.
	suggestions, err := svc.SuggestSlots(600, false, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(suggestions) != 0 {
		t.Errorf("expected 0 suggestions for 600-min slot, got %d", len(suggestions))
	}
}

func TestSuggestSlots_InvalidDuration(t *testing.T) {
	svc, cleanup := setupSchedulingTest(t)
	defer cleanup()

	_, err := svc.SuggestSlots(0, false, 1)
	if err == nil {
		t.Fatal("expected error for zero duration")
	}
}

func TestPlanWeek_Basic(t *testing.T) {
	svc, cleanup := setupSchedulingTest(t)
	defer cleanup()

	plan, err := svc.PlanWeek("default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if plan == nil {
		t.Fatal("expected non-nil plan")
	}

	// Check required fields exist.
	requiredKeys := []string{"period", "total_meetings", "total_busy_hours", "total_free_hours", "daily_summaries", "focus_blocks", "urgent_tasks", "warnings"}
	for _, key := range requiredKeys {
		if _, ok := plan[key]; !ok {
			t.Errorf("missing key in plan: %s", key)
		}
	}

	// daily_summaries should have 7 entries.
	summaries, ok := plan["daily_summaries"].([]map[string]any)
	if !ok {
		t.Fatalf("daily_summaries wrong type: %T", plan["daily_summaries"])
	}
	if len(summaries) != 7 {
		t.Errorf("expected 7 daily summaries, got %d", len(summaries))
	}

	// With no events, total_meetings should be 0.
	totalMeetings, ok := plan["total_meetings"].(int)
	if !ok {
		t.Fatalf("total_meetings wrong type: %T", plan["total_meetings"])
	}
	if totalMeetings != 0 {
		t.Errorf("expected 0 total meetings, got %d", totalMeetings)
	}

	// With no events, total_free_hours should be 63 (9 * 7).
	totalFree, ok := plan["total_free_hours"].(float64)
	if !ok {
		t.Fatalf("total_free_hours wrong type: %T", plan["total_free_hours"])
	}
	if totalFree != 63 {
		t.Errorf("expected 63 total free hours, got %f", totalFree)
	}
}

func TestDetectOvercommitment(t *testing.T) {
	svc, cleanup := setupSchedulingTest(t)
	defer cleanup()

	schedules := []DaySchedule{
		{Date: "2026-03-15", BusyHours: 7.5, MeetingCount: 6, FreeHours: 0.5},
	}

	warnings := svc.detectOvercommitment(schedules)
	if len(warnings) < 2 {
		t.Errorf("expected at least 2 warnings (busy hours + meeting count + low free hours), got %d", len(warnings))
	}

	// Check content of warnings.
	foundBusy := false
	foundMeetings := false
	foundFree := false
	for _, w := range warnings {
		if contains(w, "overcommitted") {
			foundBusy = true
		}
		if contains(w, "context-switching") {
			foundMeetings = true
		}
		if contains(w, "no focus time") {
			foundFree = true
		}
	}
	if !foundBusy {
		t.Error("expected overcommitment warning for busy hours")
	}
	if !foundMeetings {
		t.Error("expected warning for high meeting count")
	}
	if !foundFree {
		t.Error("expected warning for low free time")
	}
}

func TestDetectOvercommitment_NormalDay(t *testing.T) {
	svc, cleanup := setupSchedulingTest(t)
	defer cleanup()

	schedules := []DaySchedule{
		{Date: "2026-03-15", BusyHours: 3, MeetingCount: 2, FreeHours: 6},
	}

	warnings := svc.detectOvercommitment(schedules)
	if len(warnings) != 0 {
		t.Errorf("expected 0 warnings for normal day, got %d: %v", len(warnings), warnings)
	}
}

func TestMergeEvents(t *testing.T) {
	loc := time.Now().Location()
	base := time.Date(2026, 3, 15, 9, 0, 0, 0, loc)

	events := []ScheduleEvent{
		{Title: "A", Start: base, End: base.Add(60 * time.Minute)},
		{Title: "B", Start: base.Add(30 * time.Minute), End: base.Add(90 * time.Minute)},
		{Title: "C", Start: base.Add(120 * time.Minute), End: base.Add(150 * time.Minute)},
	}

	merged := mergeEvents(events)
	if len(merged) != 2 {
		t.Fatalf("expected 2 merged events, got %d", len(merged))
	}
	// First merged: 09:00-10:30 (A and B overlap).
	if merged[0].End != base.Add(90*time.Minute) {
		t.Errorf("expected first merged end at 10:30, got %s", merged[0].End.Format("15:04"))
	}
	// Second: 11:00-11:30 (C standalone).
	if merged[1].Start != base.Add(120*time.Minute) {
		t.Errorf("expected second event at 11:00, got %s", merged[1].Start.Format("15:04"))
	}
}

func TestMergeEvents_Empty(t *testing.T) {
	merged := mergeEvents(nil)
	if merged != nil {
		t.Errorf("expected nil for empty input, got %v", merged)
	}
}

func TestMergeEvents_Adjacent(t *testing.T) {
	loc := time.Now().Location()
	base := time.Date(2026, 3, 15, 9, 0, 0, 0, loc)

	events := []ScheduleEvent{
		{Title: "A", Start: base, End: base.Add(60 * time.Minute)},
		{Title: "B", Start: base.Add(60 * time.Minute), End: base.Add(120 * time.Minute)},
	}

	merged := mergeEvents(events)
	// Adjacent events should be merged.
	if len(merged) != 1 {
		t.Fatalf("expected 1 merged event for adjacent, got %d", len(merged))
	}
	if merged[0].End != base.Add(120*time.Minute) {
		t.Errorf("expected end at 11:00, got %s", merged[0].End.Format("15:04"))
	}
}

func TestToolScheduleView(t *testing.T) {
	_, cleanup := setupSchedulingTest(t)
	defer cleanup()

	cfg := &Config{}
	input := json.RawMessage(`{"date": "2026-03-15", "days": 2}`)

	result, err := toolScheduleView(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}

	// Parse output as JSON.
	var schedules []DaySchedule
	if err := json.Unmarshal([]byte(result), &schedules); err != nil {
		t.Fatalf("invalid JSON output: %v", err)
	}
	if len(schedules) != 2 {
		t.Errorf("expected 2 days, got %d", len(schedules))
	}
}

func TestToolScheduleView_Defaults(t *testing.T) {
	_, cleanup := setupSchedulingTest(t)
	defer cleanup()

	cfg := &Config{}
	input := json.RawMessage(`{}`)

	result, err := toolScheduleView(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var schedules []DaySchedule
	if err := json.Unmarshal([]byte(result), &schedules); err != nil {
		t.Fatalf("invalid JSON output: %v", err)
	}
	if len(schedules) != 1 {
		t.Errorf("expected 1 day (default), got %d", len(schedules))
	}
}

func TestToolScheduleView_NotInitialized(t *testing.T) {
	old := globalSchedulingService
	globalSchedulingService = nil
	defer func() { globalSchedulingService = old }()

	cfg := &Config{}
	input := json.RawMessage(`{}`)

	_, err := toolScheduleView(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error when service not initialized")
	}
}

func TestToolScheduleSuggest(t *testing.T) {
	_, cleanup := setupSchedulingTest(t)
	defer cleanup()

	cfg := &Config{}
	input := json.RawMessage(`{"duration_minutes": 60, "prefer_morning": true, "days": 2}`)

	result, err := toolScheduleSuggest(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
	if !contains(result, "suggested slots") {
		t.Errorf("expected 'suggested slots' in result, got: %s", schedTruncateForTest(result, 200))
	}
}

func TestToolScheduleSuggest_Defaults(t *testing.T) {
	_, cleanup := setupSchedulingTest(t)
	defer cleanup()

	cfg := &Config{}
	input := json.RawMessage(`{}`)

	result, err := toolScheduleSuggest(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should use defaults (60 min, no preference, 5 days).
	if result == "" {
		t.Fatal("expected non-empty result")
	}
}

func TestToolSchedulePlan(t *testing.T) {
	_, cleanup := setupSchedulingTest(t)
	defer cleanup()

	cfg := &Config{}
	input := json.RawMessage(`{"user_id": "testuser"}`)

	result, err := toolSchedulePlan(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}

	// Parse as JSON.
	var plan map[string]any
	if err := json.Unmarshal([]byte(result), &plan); err != nil {
		t.Fatalf("invalid JSON output: %v", err)
	}
	if _, ok := plan["period"]; !ok {
		t.Error("expected 'period' in plan")
	}
	if _, ok := plan["warnings"]; !ok {
		t.Error("expected 'warnings' in plan")
	}
}

func TestScoreSlot(t *testing.T) {
	svc, cleanup := setupSchedulingTest(t)
	defer cleanup()

	loc := time.Now().Location()

	day := DaySchedule{
		Date:         "2026-03-15",
		Events:       []ScheduleEvent{},
		FreeSlots:    []TimeSlot{},
		BusyHours:    2,
		FreeHours:    7,
		MeetingCount: 0,
	}

	// Morning slot.
	morningStart := time.Date(2026, 3, 15, 9, 0, 0, 0, loc)
	morningEnd := time.Date(2026, 3, 15, 10, 0, 0, 0, loc)

	scoreMorningPref := svc.scoreSlot(morningStart, morningEnd, day, true)
	scoreMorningNoPref := svc.scoreSlot(morningStart, morningEnd, day, false)

	if scoreMorningPref <= scoreMorningNoPref {
		t.Errorf("morning slot should score higher with morning preference: pref=%f, nopref=%f", scoreMorningPref, scoreMorningNoPref)
	}

	// Afternoon slot.
	afternoonStart := time.Date(2026, 3, 15, 15, 0, 0, 0, loc)
	afternoonEnd := time.Date(2026, 3, 15, 16, 0, 0, 0, loc)

	scoreAfternoonPref := svc.scoreSlot(afternoonStart, afternoonEnd, day, false)
	scoreAfternoonNoPref := svc.scoreSlot(afternoonStart, afternoonEnd, day, true)

	if scoreAfternoonPref <= scoreAfternoonNoPref {
		t.Errorf("afternoon slot should score higher with afternoon preference: pref=%f, nopref=%f", scoreAfternoonPref, scoreAfternoonNoPref)
	}
}

func TestScoreSlot_BufferPenalty(t *testing.T) {
	svc, cleanup := setupSchedulingTest(t)
	defer cleanup()

	loc := time.Now().Location()

	// Day with a 90-minute meeting ending at 11:00.
	day := DaySchedule{
		Date: "2026-03-15",
		Events: []ScheduleEvent{
			{
				Title:  "Long meeting",
				Start:  time.Date(2026, 3, 15, 9, 30, 0, 0, loc),
				End:    time.Date(2026, 3, 15, 11, 0, 0, 0, loc),
				Source: "calendar",
			},
		},
		FreeHours:    5,
		MeetingCount: 1,
	}

	// Slot right after the long meeting (11:05).
	rightAfter := time.Date(2026, 3, 15, 11, 5, 0, 0, loc)
	rightAfterEnd := time.Date(2026, 3, 15, 12, 5, 0, 0, loc)
	scoreRightAfter := svc.scoreSlot(rightAfter, rightAfterEnd, day, false)

	// Slot with buffer (11:30).
	withBuffer := time.Date(2026, 3, 15, 11, 30, 0, 0, loc)
	withBufferEnd := time.Date(2026, 3, 15, 12, 30, 0, 0, loc)
	scoreWithBuffer := svc.scoreSlot(withBuffer, withBufferEnd, day, false)

	if scoreRightAfter >= scoreWithBuffer {
		t.Errorf("slot right after long meeting should score lower: rightAfter=%f, withBuffer=%f", scoreRightAfter, scoreWithBuffer)
	}
}

func TestWorkingHours(t *testing.T) {
	cfg := &Config{}
	svc := newSchedulingService(cfg)

	start, end := svc.workingHours()
	if start != 9 {
		t.Errorf("expected work start 9, got %d", start)
	}
	if end != 18 {
		t.Errorf("expected work end 18, got %d", end)
	}
}

func TestParseDate(t *testing.T) {
	cfg := &Config{}
	svc := newSchedulingService(cfg)
	loc := time.Now().Location()

	// Valid date.
	d, err := svc.parseDate("2026-06-15", loc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Year() != 2026 || d.Month() != 6 || d.Day() != 15 {
		t.Errorf("expected 2026-06-15, got %s", d.Format("2006-01-02"))
	}

	// Empty date = today.
	d, err = svc.parseDate("", loc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	today := time.Now().In(loc)
	if d.Format("2006-01-02") != today.Format("2006-01-02") {
		t.Errorf("expected today %s, got %s", today.Format("2006-01-02"), d.Format("2006-01-02"))
	}

	// Invalid date.
	_, err = svc.parseDate("xyz", loc)
	if err == nil {
		t.Fatal("expected error for invalid date")
	}
}

func TestFindFreeSlotsInDay_OverlappingEvents(t *testing.T) {
	svc, cleanup := setupSchedulingTest(t)
	defer cleanup()

	loc := time.Now().Location()
	whStart := time.Date(2026, 3, 15, 9, 0, 0, 0, loc)
	whEnd := time.Date(2026, 3, 15, 18, 0, 0, 0, loc)

	events := []ScheduleEvent{
		{
			Title:  "Meeting A",
			Start:  time.Date(2026, 3, 15, 10, 0, 0, 0, loc),
			End:    time.Date(2026, 3, 15, 11, 30, 0, 0, loc),
			Source: "calendar",
		},
		{
			Title:  "Meeting B (overlaps A)",
			Start:  time.Date(2026, 3, 15, 11, 0, 0, 0, loc),
			End:    time.Date(2026, 3, 15, 12, 0, 0, 0, loc),
			Source: "calendar",
		},
	}

	freeSlots := svc.findFreeSlotsInDay(events, whStart, whEnd)

	// Expected: 09:00-10:00 (60 min), 12:00-18:00 (360 min)
	if len(freeSlots) != 2 {
		t.Fatalf("expected 2 free slots, got %d", len(freeSlots))
	}
	if freeSlots[0].Duration != 60 {
		t.Errorf("slot 0: expected 60 min, got %d", freeSlots[0].Duration)
	}
	if freeSlots[1].Duration != 360 {
		t.Errorf("slot 1: expected 360 min, got %d", freeSlots[1].Duration)
	}
}

func TestFindFreeSlotsInDay_FullyBooked(t *testing.T) {
	svc, cleanup := setupSchedulingTest(t)
	defer cleanup()

	loc := time.Now().Location()
	whStart := time.Date(2026, 3, 15, 9, 0, 0, 0, loc)
	whEnd := time.Date(2026, 3, 15, 18, 0, 0, 0, loc)

	events := []ScheduleEvent{
		{
			Title:  "All day meeting",
			Start:  time.Date(2026, 3, 15, 9, 0, 0, 0, loc),
			End:    time.Date(2026, 3, 15, 18, 0, 0, 0, loc),
			Source: "calendar",
		},
	}

	freeSlots := svc.findFreeSlotsInDay(events, whStart, whEnd)
	if len(freeSlots) != 0 {
		t.Errorf("expected 0 free slots for fully booked day, got %d", len(freeSlots))
	}
}

func TestFindFreeSlotsInDay_SmallGap(t *testing.T) {
	svc, cleanup := setupSchedulingTest(t)
	defer cleanup()

	loc := time.Now().Location()
	whStart := time.Date(2026, 3, 15, 9, 0, 0, 0, loc)
	whEnd := time.Date(2026, 3, 15, 18, 0, 0, 0, loc)

	events := []ScheduleEvent{
		{
			Title:  "Meeting A",
			Start:  time.Date(2026, 3, 15, 9, 0, 0, 0, loc),
			End:    time.Date(2026, 3, 15, 12, 0, 0, 0, loc),
			Source: "calendar",
		},
		{
			Title:  "Meeting B",
			Start:  time.Date(2026, 3, 15, 12, 10, 0, 0, loc),
			End:    time.Date(2026, 3, 15, 18, 0, 0, 0, loc),
			Source: "calendar",
		},
	}

	freeSlots := svc.findFreeSlotsInDay(events, whStart, whEnd)
	// Only a 10-minute gap (12:00-12:10), which is below the 15-minute minimum.
	if len(freeSlots) != 0 {
		t.Errorf("expected 0 free slots (gap too small), got %d", len(freeSlots))
	}
}

// --- Test helpers ---
// contains() is defined in memory_test.go and shared across the package.

func schedTruncateForTest(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
