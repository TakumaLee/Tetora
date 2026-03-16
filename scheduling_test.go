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

// --- Test helpers ---
// contains() is defined in memory_test.go and shared across the package.

func schedTruncateForTest(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
