package main

import (
	"encoding/json"
	"testing"
	"time"
)

// --- parseCronExpr tests ---

func TestParseCronExpr_EveryMinute(t *testing.T) {
	expr, err := parseCronExpr("* * * * *")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i := 0; i < 60; i++ {
		if !expr.minutes[i] {
			t.Errorf("minute %d should be set", i)
		}
	}
	for i := 0; i < 24; i++ {
		if !expr.hours[i] {
			t.Errorf("hour %d should be set", i)
		}
	}
	for i := 1; i <= 31; i++ {
		if !expr.doms[i] {
			t.Errorf("dom %d should be set", i)
		}
	}
	for i := 1; i <= 12; i++ {
		if !expr.months[i] {
			t.Errorf("month %d should be set", i)
		}
	}
	for i := 0; i < 7; i++ {
		if !expr.dows[i] {
			t.Errorf("dow %d should be set", i)
		}
	}
}

func TestParseCronExpr_Every5Minutes(t *testing.T) {
	expr, err := parseCronExpr("*/5 * * * *")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := map[int]bool{0: true, 5: true, 10: true, 15: true, 20: true, 25: true, 30: true, 35: true, 40: true, 45: true, 50: true, 55: true}
	for i := 0; i < 60; i++ {
		if expr.minutes[i] != expected[i] {
			t.Errorf("minute %d: got %v, want %v", i, expr.minutes[i], expected[i])
		}
	}
}

func TestParseCronExpr_9AMWeekdays(t *testing.T) {
	expr, err := parseCronExpr("0 9 * * 1-5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Minute: only 0
	for i := 0; i < 60; i++ {
		want := (i == 0)
		if expr.minutes[i] != want {
			t.Errorf("minute %d: got %v, want %v", i, expr.minutes[i], want)
		}
	}
	// Hour: only 9
	for i := 0; i < 24; i++ {
		want := (i == 9)
		if expr.hours[i] != want {
			t.Errorf("hour %d: got %v, want %v", i, expr.hours[i], want)
		}
	}
	// DOW: Mon(1) through Fri(5)
	dowExpected := map[int]bool{0: false, 1: true, 2: true, 3: true, 4: true, 5: true, 6: false}
	for i := 0; i < 7; i++ {
		if expr.dows[i] != dowExpected[i] {
			t.Errorf("dow %d: got %v, want %v", i, expr.dows[i], dowExpected[i])
		}
	}
}

func TestParseCronExpr_Midnight1stOfMonth(t *testing.T) {
	expr, err := parseCronExpr("0 0 1 * *")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Minute: only 0
	if !expr.minutes[0] {
		t.Error("minute 0 should be set")
	}
	if expr.minutes[1] {
		t.Error("minute 1 should not be set")
	}
	// Hour: only 0
	if !expr.hours[0] {
		t.Error("hour 0 should be set")
	}
	if expr.hours[1] {
		t.Error("hour 1 should not be set")
	}
	// DOM: only 1
	if !expr.doms[1] {
		t.Error("dom 1 should be set")
	}
	if expr.doms[2] {
		t.Error("dom 2 should not be set")
	}
}

func TestParseCronExpr_CommaSeparated(t *testing.T) {
	// 4:30 on 1st and 15th
	expr, err := parseCronExpr("30 4 1,15 * *")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Minute: only 30
	if !expr.minutes[30] {
		t.Error("minute 30 should be set")
	}
	if expr.minutes[0] {
		t.Error("minute 0 should not be set")
	}
	// Hour: only 4
	if !expr.hours[4] {
		t.Error("hour 4 should be set")
	}
	if expr.hours[3] {
		t.Error("hour 3 should not be set")
	}
	// DOM: 1 and 15
	if !expr.doms[1] {
		t.Error("dom 1 should be set")
	}
	if !expr.doms[15] {
		t.Error("dom 15 should be set")
	}
	if expr.doms[2] {
		t.Error("dom 2 should not be set")
	}
	if expr.doms[14] {
		t.Error("dom 14 should not be set")
	}
}

func TestParseCronExpr_RangeWithStep(t *testing.T) {
	// 0-30/10 => 0, 10, 20, 30
	expr, err := parseCronExpr("0-30/10 * * * *")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := map[int]bool{0: true, 10: true, 20: true, 30: true}
	for i := 0; i < 60; i++ {
		if expr.minutes[i] != expected[i] {
			t.Errorf("minute %d: got %v, want %v", i, expr.minutes[i], expected[i])
		}
	}
}

func TestParseCronExpr_SingleValue(t *testing.T) {
	expr, err := parseCronExpr("15 12 * * *")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Only minute 15
	setCount := 0
	for i := 0; i < 60; i++ {
		if expr.minutes[i] {
			setCount++
		}
	}
	if setCount != 1 {
		t.Errorf("expected 1 minute set, got %d", setCount)
	}
	if !expr.minutes[15] {
		t.Error("minute 15 should be set")
	}
	// Only hour 12
	setCount = 0
	for i := 0; i < 24; i++ {
		if expr.hours[i] {
			setCount++
		}
	}
	if setCount != 1 {
		t.Errorf("expected 1 hour set, got %d", setCount)
	}
	if !expr.hours[12] {
		t.Error("hour 12 should be set")
	}
}

func TestParseCronExpr_MultipleCommas(t *testing.T) {
	expr, err := parseCronExpr("0,15,30,45 * * * *")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := map[int]bool{0: true, 15: true, 30: true, 45: true}
	setCount := 0
	for i := 0; i < 60; i++ {
		if expr.minutes[i] {
			setCount++
			if !expected[i] {
				t.Errorf("minute %d should not be set", i)
			}
		}
	}
	if setCount != 4 {
		t.Errorf("expected 4 minutes set, got %d", setCount)
	}
}

// --- parseCronExpr error cases ---

func TestParseCronExpr_TooFewFields(t *testing.T) {
	_, err := parseCronExpr("* * *")
	if err == nil {
		t.Fatal("expected error for too few fields")
	}
}

func TestParseCronExpr_TooManyFields(t *testing.T) {
	_, err := parseCronExpr("* * * * * *")
	if err == nil {
		t.Fatal("expected error for too many fields")
	}
}

func TestParseCronExpr_MinuteOutOfRange(t *testing.T) {
	_, err := parseCronExpr("60 * * * *")
	if err == nil {
		t.Fatal("expected error for minute 60 (out of range)")
	}
}

func TestParseCronExpr_HourOutOfRange(t *testing.T) {
	_, err := parseCronExpr("* 24 * * *")
	if err == nil {
		t.Fatal("expected error for hour 24 (out of range)")
	}
}

func TestParseCronExpr_DOMOutOfRange(t *testing.T) {
	_, err := parseCronExpr("* * 32 * *")
	if err == nil {
		t.Fatal("expected error for dom 32 (out of range)")
	}
}

func TestParseCronExpr_DOM0OutOfRange(t *testing.T) {
	_, err := parseCronExpr("* * 0 * *")
	if err == nil {
		t.Fatal("expected error for dom 0 (out of range)")
	}
}

func TestParseCronExpr_MonthOutOfRange(t *testing.T) {
	_, err := parseCronExpr("* * * 13 *")
	if err == nil {
		t.Fatal("expected error for month 13 (out of range)")
	}
}

func TestParseCronExpr_Month0OutOfRange(t *testing.T) {
	_, err := parseCronExpr("* * * 0 *")
	if err == nil {
		t.Fatal("expected error for month 0 (out of range)")
	}
}

func TestParseCronExpr_DOWOutOfRange(t *testing.T) {
	_, err := parseCronExpr("* * * * 7")
	if err == nil {
		t.Fatal("expected error for dow 7 (out of range)")
	}
}

func TestParseCronExpr_NonNumeric(t *testing.T) {
	_, err := parseCronExpr("a * * * *")
	if err == nil {
		t.Fatal("expected error for non-numeric value")
	}
}

func TestParseCronExpr_BadRangeNonNumericStart(t *testing.T) {
	_, err := parseCronExpr("a-5 * * * *")
	if err == nil {
		t.Fatal("expected error for non-numeric range start")
	}
}

func TestParseCronExpr_BadRangeNonNumericEnd(t *testing.T) {
	_, err := parseCronExpr("0-b * * * *")
	if err == nil {
		t.Fatal("expected error for non-numeric range end")
	}
}

func TestParseCronExpr_BadStep(t *testing.T) {
	_, err := parseCronExpr("*/0 * * * *")
	if err == nil {
		t.Fatal("expected error for step of 0")
	}
}

func TestParseCronExpr_BadStepNonNumeric(t *testing.T) {
	_, err := parseCronExpr("*/abc * * * *")
	if err == nil {
		t.Fatal("expected error for non-numeric step")
	}
}

func TestParseCronExpr_InvertedRange(t *testing.T) {
	_, err := parseCronExpr("30-10 * * * *")
	if err == nil {
		t.Fatal("expected error for inverted range (30-10)")
	}
}

func TestParseCronExpr_EmptyString(t *testing.T) {
	_, err := parseCronExpr("")
	if err == nil {
		t.Fatal("expected error for empty string")
	}
}

func TestParseCronExpr_NegativeStep(t *testing.T) {
	_, err := parseCronExpr("*/-1 * * * *")
	if err == nil {
		t.Fatal("expected error for negative step")
	}
}

// --- cronExpr.matches tests ---

func TestCronExprMatches_Every5Min_Match(t *testing.T) {
	expr, err := parseCronExpr("*/5 * * * *")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 2026-01-15 10:15:00 UTC is a Thursday; minute 15 is divisible by 5
	tm := time.Date(2026, 1, 15, 10, 15, 0, 0, time.UTC)
	if !expr.matches(tm) {
		t.Errorf("expected match for %v", tm)
	}
}

func TestCronExprMatches_Every5Min_NoMatch(t *testing.T) {
	expr, err := parseCronExpr("*/5 * * * *")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// minute 13 is not divisible by 5
	tm := time.Date(2026, 1, 15, 10, 13, 0, 0, time.UTC)
	if expr.matches(tm) {
		t.Errorf("expected no match for %v", tm)
	}
}

func TestCronExprMatches_9AMWeekdays_MatchMonday(t *testing.T) {
	expr, err := parseCronExpr("0 9 * * 1-5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 2026-02-16 is Monday
	tm := time.Date(2026, 2, 16, 9, 0, 0, 0, time.UTC)
	if tm.Weekday() != time.Monday {
		t.Fatalf("expected Monday, got %v", tm.Weekday())
	}
	if !expr.matches(tm) {
		t.Errorf("expected match for Monday 9:00")
	}
}

func TestCronExprMatches_9AMWeekdays_NoMatchSunday(t *testing.T) {
	expr, err := parseCronExpr("0 9 * * 1-5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 2026-02-15 is Sunday
	tm := time.Date(2026, 2, 15, 9, 0, 0, 0, time.UTC)
	if tm.Weekday() != time.Sunday {
		t.Fatalf("expected Sunday, got %v", tm.Weekday())
	}
	if expr.matches(tm) {
		t.Errorf("expected no match for Sunday 9:00 with weekdays-only cron")
	}
}

func TestCronExprMatches_9AMWeekdays_NoMatchSaturday(t *testing.T) {
	expr, err := parseCronExpr("0 9 * * 1-5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 2026-02-21 is Saturday
	tm := time.Date(2026, 2, 21, 9, 0, 0, 0, time.UTC)
	if tm.Weekday() != time.Saturday {
		t.Fatalf("expected Saturday, got %v", tm.Weekday())
	}
	if expr.matches(tm) {
		t.Errorf("expected no match for Saturday 9:00 with weekdays-only cron")
	}
}

func TestCronExprMatches_9AMWeekdays_WrongMinute(t *testing.T) {
	expr, err := parseCronExpr("0 9 * * 1-5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Correct day/hour but wrong minute
	tm := time.Date(2026, 2, 16, 9, 30, 0, 0, time.UTC) // Monday 9:30
	if expr.matches(tm) {
		t.Errorf("expected no match for Monday 9:30 (minute 0 only)")
	}
}

func TestCronExprMatches_Midnight1st(t *testing.T) {
	expr, err := parseCronExpr("0 0 1 * *")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 2026-03-01 00:00 UTC is a Sunday
	tm := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if !expr.matches(tm) {
		t.Errorf("expected match for midnight 1st of March")
	}
	// 2026-03-02 00:00 should not match
	tm2 := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)
	if expr.matches(tm2) {
		t.Errorf("expected no match for midnight 2nd of March")
	}
}

func TestCronExprMatches_SpecificMonthAndDay(t *testing.T) {
	expr, err := parseCronExpr("0 12 25 12 *")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Christmas noon
	tm := time.Date(2026, 12, 25, 12, 0, 0, 0, time.UTC)
	if !expr.matches(tm) {
		t.Error("expected match for Dec 25 at noon")
	}
	// Not Christmas
	tm2 := time.Date(2026, 12, 24, 12, 0, 0, 0, time.UTC)
	if expr.matches(tm2) {
		t.Error("expected no match for Dec 24 at noon")
	}
}

func TestCronExprMatches_EveryMinute_AllTimes(t *testing.T) {
	expr, err := parseCronExpr("* * * * *")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Every minute of every day should match
	times := []time.Time{
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 15, 12, 30, 0, 0, time.UTC),
		time.Date(2026, 12, 31, 23, 59, 0, 0, time.UTC),
	}
	for _, tm := range times {
		if !expr.matches(tm) {
			t.Errorf("expected match for %v with * * * * *", tm)
		}
	}
}

// --- nextRunAfter tests ---

func TestNextRunAfter_SameDayBefore(t *testing.T) {
	expr, err := parseCronExpr("0 9 * * *")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 08:59 -> next should be 09:00 same day
	after := time.Date(2026, 2, 20, 8, 59, 0, 0, time.UTC)
	next := nextRunAfter(expr, time.UTC, after)
	want := time.Date(2026, 2, 20, 9, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("got %v, want %v", next, want)
	}
}

func TestNextRunAfter_SameDayAfter(t *testing.T) {
	expr, err := parseCronExpr("0 9 * * *")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 09:01 -> next should be 09:00 next day
	after := time.Date(2026, 2, 20, 9, 1, 0, 0, time.UTC)
	next := nextRunAfter(expr, time.UTC, after)
	want := time.Date(2026, 2, 21, 9, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("got %v, want %v", next, want)
	}
}

func TestNextRunAfter_SameMinute(t *testing.T) {
	// If "after" is exactly at the matching time, nextRunAfter should return the NEXT occurrence.
	// Because it starts from after.Truncate(Minute).Add(Minute).
	expr, err := parseCronExpr("0 9 * * *")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	after := time.Date(2026, 2, 20, 9, 0, 0, 0, time.UTC)
	next := nextRunAfter(expr, time.UTC, after)
	want := time.Date(2026, 2, 21, 9, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("got %v, want %v", next, want)
	}
}

func TestNextRunAfter_Every5Min(t *testing.T) {
	expr, err := parseCronExpr("*/5 * * * *")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 10:03 -> next should be 10:05
	after := time.Date(2026, 2, 20, 10, 3, 0, 0, time.UTC)
	next := nextRunAfter(expr, time.UTC, after)
	want := time.Date(2026, 2, 20, 10, 5, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("got %v, want %v", next, want)
	}
}

func TestNextRunAfter_WeekdaysSkipsWeekend(t *testing.T) {
	expr, err := parseCronExpr("0 9 * * 1-5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 2026-02-20 is Friday, after 09:01 -> next should be Monday 2026-02-23 09:00
	after := time.Date(2026, 2, 20, 9, 1, 0, 0, time.UTC)
	next := nextRunAfter(expr, time.UTC, after)
	want := time.Date(2026, 2, 23, 9, 0, 0, 0, time.UTC)
	if next.Weekday() != time.Monday {
		t.Errorf("expected Monday, got %v", next.Weekday())
	}
	if !next.Equal(want) {
		t.Errorf("got %v, want %v", next, want)
	}
}

func TestNextRunAfter_MonthBoundary(t *testing.T) {
	expr, err := parseCronExpr("0 0 1 * *")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// After Jan 1st midnight -> Feb 1st midnight
	after := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	next := nextRunAfter(expr, time.UTC, after)
	want := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("got %v, want %v", next, want)
	}
}

func TestNextRunAfter_MonthSkip(t *testing.T) {
	// Only in March (month 3)
	expr, err := parseCronExpr("0 0 1 3 *")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// After Jan 15 -> March 1
	after := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	next := nextRunAfter(expr, time.UTC, after)
	want := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("got %v, want %v", next, want)
	}
}

func TestNextRunAfter_YearBoundary(t *testing.T) {
	expr, err := parseCronExpr("0 0 1 1 *")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// After Jan 1 2026 -> Jan 1 2027
	after := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	next := nextRunAfter(expr, time.UTC, after)
	want := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("got %v, want %v", next, want)
	}
}

func TestNextRunAfter_NotZero(t *testing.T) {
	// Ensure a basic expression always finds a next run.
	expr, err := parseCronExpr("* * * * *")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	after := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	next := nextRunAfter(expr, time.UTC, after)
	if next.IsZero() {
		t.Error("expected non-zero next run for * * * * *")
	}
	// Should be exactly one minute later
	want := time.Date(2026, 6, 15, 12, 1, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("got %v, want %v", next, want)
	}
}

func TestNextRunAfter_WithLocation(t *testing.T) {
	expr, err := parseCronExpr("0 9 * * *")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	loc, err := time.LoadLocation("Asia/Tokyo") // UTC+9
	if err != nil {
		t.Skip("Asia/Tokyo timezone not available")
	}
	// 2026-02-20 08:59 JST -> next should be 09:00 JST same day
	after := time.Date(2026, 2, 20, 8, 59, 0, 0, loc)
	next := nextRunAfter(expr, loc, after)
	want := time.Date(2026, 2, 20, 9, 0, 0, 0, loc)
	if !next.Equal(want) {
		t.Errorf("got %v, want %v", next, want)
	}
}

func TestNextRunAfter_CommaSeparatedDOMs(t *testing.T) {
	expr, err := parseCronExpr("30 4 1,15 * *")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// After Feb 1 04:31 -> Feb 15 04:30
	after := time.Date(2026, 2, 1, 4, 31, 0, 0, time.UTC)
	next := nextRunAfter(expr, time.UTC, after)
	want := time.Date(2026, 2, 15, 4, 30, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("got %v, want %v", next, want)
	}
}

func TestNextRunAfter_RangeWithStep(t *testing.T) {
	// 0-30/10 => minutes 0, 10, 20, 30
	expr, err := parseCronExpr("0-30/10 * * * *")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// After 10:05 -> 10:10
	after := time.Date(2026, 2, 20, 10, 5, 0, 0, time.UTC)
	next := nextRunAfter(expr, time.UTC, after)
	want := time.Date(2026, 2, 20, 10, 10, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("got %v, want %v", next, want)
	}

	// After 10:30 -> 11:00 (next hour, minute 0)
	after2 := time.Date(2026, 2, 20, 10, 30, 0, 0, time.UTC)
	next2 := nextRunAfter(expr, time.UTC, after2)
	want2 := time.Date(2026, 2, 20, 11, 0, 0, 0, time.UTC)
	if !next2.Equal(want2) {
		t.Errorf("got %v, want %v", next2, want2)
	}
}

// --- truncate tests ---

func TestTruncate_ShortString(t *testing.T) {
	s := "hello"
	result := truncate(s, 10)
	if result != "hello" {
		t.Errorf("got %q, want %q", result, "hello")
	}
}

func TestTruncate_LongString(t *testing.T) {
	s := "hello world, this is a long string"
	result := truncate(s, 10)
	want := "hello worl..."
	if result != want {
		t.Errorf("got %q, want %q", result, want)
	}
}

func TestTruncate_ExactLength(t *testing.T) {
	s := "hello"
	result := truncate(s, 5)
	if result != "hello" {
		t.Errorf("got %q, want %q", result, "hello")
	}
}

func TestTruncate_OneOver(t *testing.T) {
	s := "abcdef"
	result := truncate(s, 5)
	want := "abcde..."
	if result != want {
		t.Errorf("got %q, want %q", result, want)
	}
}

func TestTruncate_EmptyString(t *testing.T) {
	result := truncate("", 10)
	if result != "" {
		t.Errorf("got %q, want %q", result, "")
	}
}

func TestTruncate_ZeroMaxLen(t *testing.T) {
	result := truncate("hello", 0)
	want := "..."
	if result != want {
		t.Errorf("got %q, want %q", result, want)
	}
}

// --- maxChainDepth constant ---

func TestMaxChainDepth(t *testing.T) {
	if maxChainDepth != 5 {
		t.Errorf("maxChainDepth = %d, want 5", maxChainDepth)
	}
}

// --- Table-driven tests for comprehensive coverage ---

func TestParseCronExpr_Table(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"every minute", "* * * * *", false},
		{"every 5 min", "*/5 * * * *", false},
		{"9am weekdays", "0 9 * * 1-5", false},
		{"midnight 1st", "0 0 1 * *", false},
		{"1st and 15th", "30 4 1,15 * *", false},
		{"range step", "0-30/10 * * * *", false},
		{"every 2 hours", "0 */2 * * *", false},
		{"specific month", "0 0 1 6 *", false},
		{"sunday only", "0 0 * * 0", false},
		{"complex", "0,30 9-17 * * 1-5", false},
		{"too few fields", "* * *", true},
		{"too many fields", "* * * * * *", true},
		{"empty", "", true},
		{"minute 60", "60 * * * *", true},
		{"hour 24", "* 24 * * *", true},
		{"dom 0", "* * 0 * *", true},
		{"dom 32", "* * 32 * *", true},
		{"month 0", "* * * 0 *", true},
		{"month 13", "* * * 13 *", true},
		{"dow 7", "* * * * 7", true},
		{"non-numeric", "a * * * *", true},
		{"bad step 0", "*/0 * * * *", true},
		{"bad step abc", "*/abc * * * *", true},
		{"inverted range", "30-10 * * * *", true},
		{"negative value", "-1 * * * *", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseCronExpr(tt.input)
			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestCronExprMatches_Table(t *testing.T) {
	tests := []struct {
		name   string
		expr   string
		time   time.Time
		expect bool
	}{
		{
			"every minute matches any time",
			"* * * * *",
			time.Date(2026, 7, 4, 15, 33, 0, 0, time.UTC),
			true,
		},
		{
			"every 5 min matches 0",
			"*/5 * * * *",
			time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			true,
		},
		{
			"every 5 min matches 55",
			"*/5 * * * *",
			time.Date(2026, 1, 1, 0, 55, 0, 0, time.UTC),
			true,
		},
		{
			"every 5 min no match 1",
			"*/5 * * * *",
			time.Date(2026, 1, 1, 0, 1, 0, 0, time.UTC),
			false,
		},
		{
			"weekday match friday",
			"0 9 * * 1-5",
			time.Date(2026, 2, 20, 9, 0, 0, 0, time.UTC), // Friday
			true,
		},
		{
			"weekday no match saturday",
			"0 9 * * 1-5",
			time.Date(2026, 2, 21, 9, 0, 0, 0, time.UTC), // Saturday
			false,
		},
		{
			"1st and 15th match",
			"30 4 1,15 * *",
			time.Date(2026, 3, 15, 4, 30, 0, 0, time.UTC),
			true,
		},
		{
			"1st and 15th no match 2nd",
			"30 4 1,15 * *",
			time.Date(2026, 3, 2, 4, 30, 0, 0, time.UTC),
			false,
		},
		{
			"specific month match",
			"0 0 * 6 *",
			time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
			true,
		},
		{
			"specific month no match",
			"0 0 * 6 *",
			time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expr, err := parseCronExpr(tt.expr)
			if err != nil {
				t.Fatalf("parseCronExpr(%q) error: %v", tt.expr, err)
			}
			got := expr.matches(tt.time)
			if got != tt.expect {
				t.Errorf("matches(%v) = %v, want %v", tt.time, got, tt.expect)
			}
		})
	}
}

func TestNextRunAfter_Table(t *testing.T) {
	tests := []struct {
		name  string
		expr  string
		after time.Time
		want  time.Time
	}{
		{
			"daily 9am before target",
			"0 9 * * *",
			time.Date(2026, 2, 20, 8, 59, 0, 0, time.UTC),
			time.Date(2026, 2, 20, 9, 0, 0, 0, time.UTC),
		},
		{
			"daily 9am after target",
			"0 9 * * *",
			time.Date(2026, 2, 20, 9, 1, 0, 0, time.UTC),
			time.Date(2026, 2, 21, 9, 0, 0, 0, time.UTC),
		},
		{
			"every minute next",
			"* * * * *",
			time.Date(2026, 2, 20, 10, 30, 0, 0, time.UTC),
			time.Date(2026, 2, 20, 10, 31, 0, 0, time.UTC),
		},
		{
			"end of day rollover",
			"0 9 * * *",
			time.Date(2026, 2, 20, 23, 59, 0, 0, time.UTC),
			time.Date(2026, 2, 21, 9, 0, 0, 0, time.UTC),
		},
		{
			"every 5 min from :03",
			"*/5 * * * *",
			time.Date(2026, 2, 20, 10, 3, 0, 0, time.UTC),
			time.Date(2026, 2, 20, 10, 5, 0, 0, time.UTC),
		},
		{
			"1st of month after 1st",
			"0 0 1 * *",
			time.Date(2026, 3, 1, 0, 1, 0, 0, time.UTC),
			time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expr, err := parseCronExpr(tt.expr)
			if err != nil {
				t.Fatalf("parseCronExpr(%q) error: %v", tt.expr, err)
			}
			got := nextRunAfter(expr, time.UTC, tt.after)
			if !got.Equal(tt.want) {
				t.Errorf("nextRunAfter after %v: got %v, want %v", tt.after, got, tt.want)
			}
		})
	}
}

func TestTruncate_Table(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{"short", "hi", 10, "hi"},
		{"exact", "hello", 5, "hello"},
		{"over by one", "abcdef", 5, "abcde..."},
		{"way over", "the quick brown fox jumps over", 10, "the quick ..."},
		{"empty", "", 5, ""},
		{"zero len", "abc", 0, "..."},
		{"one char max", "abc", 1, "a..."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncate(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
			}
		})
	}
}

// --- Per-job concurrency tests ---

func TestEffectiveMaxConcurrentRuns_Default(t *testing.T) {
	// MaxConcurrentRuns == 0 (unset) should default to 1.
	j := &cronJob{CronJobConfig: CronJobConfig{ID: "test", MaxConcurrentRuns: 0}}
	if got := j.effectiveMaxConcurrentRuns(); got != 1 {
		t.Errorf("expected 1 for unset MaxConcurrentRuns, got %d", got)
	}
}

func TestEffectiveMaxConcurrentRuns_Explicit(t *testing.T) {
	// Explicitly set value should be returned as-is.
	for _, want := range []int{1, 2, 5, 10} {
		j := &cronJob{CronJobConfig: CronJobConfig{ID: "test", MaxConcurrentRuns: want}}
		if got := j.effectiveMaxConcurrentRuns(); got != want {
			t.Errorf("MaxConcurrentRuns=%d: expected %d, got %d", want, want, got)
		}
	}
}

func TestEffectiveMaxConcurrentRuns_Negative(t *testing.T) {
	// Negative value (invalid) falls back to default 1.
	j := &cronJob{CronJobConfig: CronJobConfig{ID: "test", MaxConcurrentRuns: -1}}
	if got := j.effectiveMaxConcurrentRuns(); got != 1 {
		t.Errorf("expected 1 for negative MaxConcurrentRuns, got %d", got)
	}
}

func TestCronJobConfig_MaxConcurrentRuns_JSONRoundtrip(t *testing.T) {
	// Field absent → deserialises to 0; effectiveMaxConcurrentRuns() → 1 (default).
	var cfgAbsent CronJobConfig
	if err := json.Unmarshal([]byte(`{"id":"j1","name":"Job","enabled":true,"schedule":"* * * * *","task":{"prompt":"hi"}}`), &cfgAbsent); err != nil {
		t.Fatalf("unmarshal without maxConcurrentRuns: %v", err)
	}
	if cfgAbsent.MaxConcurrentRuns != 0 {
		t.Errorf("expected MaxConcurrentRuns=0 when field absent, got %d", cfgAbsent.MaxConcurrentRuns)
	}
	jAbsent := &cronJob{CronJobConfig: cfgAbsent}
	if jAbsent.effectiveMaxConcurrentRuns() != 1 {
		t.Errorf("expected effectiveMaxConcurrentRuns()=1 for absent field, got %d", jAbsent.effectiveMaxConcurrentRuns())
	}

	// Field present with explicit value → returned as-is.
	var cfgPresent CronJobConfig
	if err := json.Unmarshal([]byte(`{"id":"j2","name":"Job","enabled":true,"schedule":"* * * * *","task":{"prompt":"hi"},"maxConcurrentRuns":3}`), &cfgPresent); err != nil {
		t.Fatalf("unmarshal with maxConcurrentRuns: %v", err)
	}
	if cfgPresent.MaxConcurrentRuns != 3 {
		t.Errorf("expected MaxConcurrentRuns=3, got %d", cfgPresent.MaxConcurrentRuns)
	}
	jPresent := &cronJob{CronJobConfig: cfgPresent}
	if jPresent.effectiveMaxConcurrentRuns() != 3 {
		t.Errorf("expected effectiveMaxConcurrentRuns()=3, got %d", jPresent.effectiveMaxConcurrentRuns())
	}
}
