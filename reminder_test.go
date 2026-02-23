package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// --- P19.3: Smart Reminders Tests ---

// testReminderDB creates a temporary SQLite DB for testing.
func testReminderDB(t *testing.T) string {
	t.Helper()
	f, err := os.CreateTemp("", "reminder_test_*.db")
	if err != nil {
		t.Fatalf("create temp db: %v", err)
	}
	f.Close()
	path := f.Name()
	t.Cleanup(func() { os.Remove(path) })

	if err := initReminderDB(path); err != nil {
		t.Fatalf("init reminder db: %v", err)
	}
	return path
}

func testExecSQL(t *testing.T, dbPath, sql string) {
	t.Helper()
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("exec sql: %s: %v", string(out), err)
	}
}

// --- parseNaturalTime Tests ---

func TestParseNaturalTime_Japanese(t *testing.T) {
	// "5分後"
	t.Run("5min_later", func(t *testing.T) {
		before := time.Now().UTC()
		got, err := parseNaturalTime("5分後")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		expected := before.Add(5 * time.Minute)
		diff := got.Sub(expected)
		if diff < -2*time.Second || diff > 2*time.Second {
			t.Errorf("expected ~%v, got %v (diff %v)", expected, got, diff)
		}
	})

	// "1時間後"
	t.Run("1hour_later", func(t *testing.T) {
		before := time.Now().UTC()
		got, err := parseNaturalTime("1時間後")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		expected := before.Add(1 * time.Hour)
		diff := got.Sub(expected)
		if diff < -2*time.Second || diff > 2*time.Second {
			t.Errorf("expected ~%v, got %v", expected, got)
		}
	})

	// "30秒後"
	t.Run("30sec_later", func(t *testing.T) {
		before := time.Now().UTC()
		got, err := parseNaturalTime("30秒後")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		expected := before.Add(30 * time.Second)
		diff := got.Sub(expected)
		if diff < -2*time.Second || diff > 2*time.Second {
			t.Errorf("expected ~%v, got %v", expected, got)
		}
	})

	// "明日"
	t.Run("tomorrow", func(t *testing.T) {
		got, err := parseNaturalTime("明日")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		tomorrow := time.Now().AddDate(0, 0, 1)
		if got.Day() != tomorrow.Day() {
			t.Errorf("expected day %d, got day %d", tomorrow.Day(), got.Day())
		}
	})

	// "明日3時"
	t.Run("tomorrow_3am", func(t *testing.T) {
		got, err := parseNaturalTime("明日3時")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		tomorrow := time.Now().AddDate(0, 0, 1)
		// Compare in local timezone since the time was created in local tz.
		local := got.In(time.Now().Location())
		if local.Day() != tomorrow.Day() {
			t.Errorf("expected day %d, got day %d (local)", tomorrow.Day(), local.Day())
		}
		if local.Hour() != 3 {
			t.Errorf("expected hour 3, got %d", local.Hour())
		}
	})

	// "来週月曜"
	t.Run("next_monday", func(t *testing.T) {
		got, err := parseNaturalTime("来週月曜")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		local := got.In(time.Now().Location())
		if local.Weekday() != time.Monday {
			t.Errorf("expected Monday, got %v", local.Weekday())
		}
		if !got.After(time.Now().UTC()) {
			t.Errorf("expected future time, got %v", got)
		}
	})
}

func TestParseNaturalTime_English(t *testing.T) {
	// "in 5 min"
	t.Run("in_5_min", func(t *testing.T) {
		before := time.Now().UTC()
		got, err := parseNaturalTime("in 5 min")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		expected := before.Add(5 * time.Minute)
		diff := got.Sub(expected)
		if diff < -2*time.Second || diff > 2*time.Second {
			t.Errorf("expected ~%v, got %v", expected, got)
		}
	})

	// "in 1 hour"
	t.Run("in_1_hour", func(t *testing.T) {
		before := time.Now().UTC()
		got, err := parseNaturalTime("in 1 hour")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		expected := before.Add(1 * time.Hour)
		diff := got.Sub(expected)
		if diff < -2*time.Second || diff > 2*time.Second {
			t.Errorf("expected ~%v, got %v", expected, got)
		}
	})

	// "tomorrow"
	t.Run("tomorrow", func(t *testing.T) {
		got, err := parseNaturalTime("tomorrow")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		tomorrow := time.Now().AddDate(0, 0, 1)
		if got.Day() != tomorrow.Day() {
			t.Errorf("expected day %d, got day %d", tomorrow.Day(), got.Day())
		}
	})

	// "tomorrow 3pm"
	t.Run("tomorrow_3pm", func(t *testing.T) {
		got, err := parseNaturalTime("tomorrow 3pm")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		tomorrow := time.Now().AddDate(0, 0, 1)
		if got.Day() != tomorrow.Day() {
			t.Errorf("expected day %d, got day %d", tomorrow.Day(), got.Day())
		}
		local := got.In(time.Now().Location())
		if local.Hour() != 15 {
			t.Errorf("expected hour 15, got %d", local.Hour())
		}
	})

	// "next monday"
	t.Run("next_monday", func(t *testing.T) {
		got, err := parseNaturalTime("next monday")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		local := got.In(time.Now().Location())
		if local.Weekday() != time.Monday {
			t.Errorf("expected Monday, got %v", local.Weekday())
		}
	})

	// "in 30 seconds"
	t.Run("in_30_seconds", func(t *testing.T) {
		before := time.Now().UTC()
		got, err := parseNaturalTime("in 30 seconds")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		expected := before.Add(30 * time.Second)
		diff := got.Sub(expected)
		if diff < -2*time.Second || diff > 2*time.Second {
			t.Errorf("expected ~%v, got %v", expected, got)
		}
	})
}

func TestParseNaturalTime_Chinese(t *testing.T) {
	// "5分鐘後"
	t.Run("5min_later", func(t *testing.T) {
		before := time.Now().UTC()
		got, err := parseNaturalTime("5分鐘後")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		expected := before.Add(5 * time.Minute)
		diff := got.Sub(expected)
		if diff < -2*time.Second || diff > 2*time.Second {
			t.Errorf("expected ~%v, got %v", expected, got)
		}
	})

	// "明天下午3點"
	t.Run("tomorrow_3pm", func(t *testing.T) {
		got, err := parseNaturalTime("明天下午3點")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		tomorrow := time.Now().AddDate(0, 0, 1)
		if got.Day() != tomorrow.Day() {
			t.Errorf("expected day %d, got day %d", tomorrow.Day(), got.Day())
		}
		local := got.In(time.Now().Location())
		if local.Hour() != 15 {
			t.Errorf("expected hour 15, got %d", local.Hour())
		}
	})

	// "下週一"
	t.Run("next_monday", func(t *testing.T) {
		got, err := parseNaturalTime("下週一")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		local := got.In(time.Now().Location())
		if local.Weekday() != time.Monday {
			t.Errorf("expected Monday, got %v", local.Weekday())
		}
	})
}

func TestParseNaturalTime_Absolute(t *testing.T) {
	// ISO format
	t.Run("iso", func(t *testing.T) {
		got, err := parseNaturalTime("2025-06-15 14:00")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Hour() != 14 || got.Minute() != 0 {
			t.Errorf("expected 14:00, got %02d:%02d", got.Hour(), got.Minute())
		}
	})

	// Time only: "15:30"
	t.Run("time_only", func(t *testing.T) {
		got, err := parseNaturalTime("15:30")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		local := got.In(time.Now().Location())
		if local.Hour() != 15 || local.Minute() != 30 {
			t.Errorf("expected 15:30, got %02d:%02d", local.Hour(), local.Minute())
		}
	})
}

func TestParseNaturalTime_Error(t *testing.T) {
	_, err := parseNaturalTime("")
	if err == nil {
		t.Error("expected error for empty input")
	}

	_, err = parseNaturalTime("garbage text that is not a time")
	if err == nil {
		t.Error("expected error for garbage input")
	}
}

// --- DB Operation Tests ---

func TestReminderAddAndList(t *testing.T) {
	dbPath := testReminderDB(t)
	cfg := &Config{
		HistoryDB: dbPath,
		Reminders: ReminderConfig{Enabled: true},
	}

	re := newReminderEngine(cfg, nil)

	// Add a reminder.
	due := time.Now().Add(1 * time.Hour)
	rem, err := re.addReminder("Test reminder", due, "", "api", "user1")
	if err != nil {
		t.Fatalf("addReminder: %v", err)
	}
	if rem.ID == "" {
		t.Error("expected non-empty ID")
	}
	if rem.Text != "Test reminder" {
		t.Errorf("expected text 'Test reminder', got %q", rem.Text)
	}
	if rem.Status != "pending" {
		t.Errorf("expected status 'pending', got %q", rem.Status)
	}

	// List reminders.
	list, err := re.listReminders("user1")
	if err != nil {
		t.Fatalf("listReminders: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 reminder, got %d", len(list))
	}
	if list[0].Text != "Test reminder" {
		t.Errorf("expected text 'Test reminder', got %q", list[0].Text)
	}

	// List with different user should be empty.
	list2, err := re.listReminders("user2")
	if err != nil {
		t.Fatalf("listReminders: %v", err)
	}
	if len(list2) != 0 {
		t.Errorf("expected 0 reminders for user2, got %d", len(list2))
	}
}

func TestReminderCancel(t *testing.T) {
	dbPath := testReminderDB(t)
	cfg := &Config{
		HistoryDB: dbPath,
		Reminders: ReminderConfig{Enabled: true},
	}

	re := newReminderEngine(cfg, nil)

	due := time.Now().Add(1 * time.Hour)
	rem, err := re.addReminder("Cancel me", due, "", "api", "user1")
	if err != nil {
		t.Fatalf("addReminder: %v", err)
	}

	// Cancel it.
	if err := re.cancelReminder(rem.ID, "user1"); err != nil {
		t.Fatalf("cancelReminder: %v", err)
	}

	// Should no longer appear in list.
	list, err := re.listReminders("user1")
	if err != nil {
		t.Fatalf("listReminders: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected 0 reminders after cancel, got %d", len(list))
	}
}

func TestReminderSnooze(t *testing.T) {
	dbPath := testReminderDB(t)
	cfg := &Config{
		HistoryDB: dbPath,
		Reminders: ReminderConfig{Enabled: true},
	}

	re := newReminderEngine(cfg, nil)

	// Create a reminder due 5 minutes from now.
	due := time.Now().Add(5 * time.Minute)
	rem, err := re.addReminder("Snooze me", due, "", "api", "user1")
	if err != nil {
		t.Fatalf("addReminder: %v", err)
	}

	// Snooze by 1 hour.
	if err := re.snoozeReminder(rem.ID, 1*time.Hour); err != nil {
		t.Fatalf("snoozeReminder: %v", err)
	}

	// Verify the due_at moved forward.
	list, err := re.listReminders("user1")
	if err != nil {
		t.Fatalf("listReminders: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 reminder, got %d", len(list))
	}

	newDue, err := time.Parse(time.RFC3339, list[0].DueAt)
	if err != nil {
		t.Fatalf("parse due_at: %v", err)
	}
	expectedMin := due.Add(55 * time.Minute) // at least 55 minutes later
	if newDue.Before(expectedMin) {
		t.Errorf("expected due_at after %v, got %v", expectedMin, newDue)
	}
}

func TestReminderTick(t *testing.T) {
	dbPath := testReminderDB(t)
	cfg := &Config{
		HistoryDB: dbPath,
		Reminders: ReminderConfig{Enabled: true},
	}

	var notifications []string
	notifyFn := func(text string) {
		notifications = append(notifications, text)
	}

	re := newReminderEngine(cfg, notifyFn)

	// Insert a reminder that is already due (in the past).
	pastDue := time.Now().Add(-1 * time.Minute)
	_, err := re.addReminder("Past due reminder", pastDue, "", "api", "user1")
	if err != nil {
		t.Fatalf("addReminder: %v", err)
	}

	// Insert a reminder that is not yet due.
	futureDue := time.Now().Add(1 * time.Hour)
	_, err = re.addReminder("Future reminder", futureDue, "", "api", "user1")
	if err != nil {
		t.Fatalf("addReminder: %v", err)
	}

	// Run tick.
	re.tick()

	// Should have fired the past-due reminder.
	if len(notifications) != 1 {
		t.Fatalf("expected 1 notification, got %d: %v", len(notifications), notifications)
	}
	if !strings.Contains(notifications[0], "Past due reminder") {
		t.Errorf("expected notification about 'Past due reminder', got %q", notifications[0])
	}

	// The past-due reminder should now be 'fired'.
	list, err := re.listReminders("user1")
	if err != nil {
		t.Fatalf("listReminders: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 pending reminder (the future one), got %d", len(list))
	}
}

func TestReminderRecurring(t *testing.T) {
	dbPath := testReminderDB(t)
	cfg := &Config{
		HistoryDB: dbPath,
		Reminders: ReminderConfig{Enabled: true},
	}

	var notifications []string
	notifyFn := func(text string) {
		notifications = append(notifications, text)
	}

	re := newReminderEngine(cfg, notifyFn)

	// Insert a recurring reminder that is already due.
	pastDue := time.Now().Add(-1 * time.Minute)
	rem, err := re.addReminder("Daily standup", pastDue, "0 9 * * *", "api", "user1")
	if err != nil {
		t.Fatalf("addReminder: %v", err)
	}

	// Run tick — should fire and reschedule.
	re.tick()

	if len(notifications) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(notifications))
	}

	// The reminder should still be pending (rescheduled, not fired).
	list, err := re.listReminders("user1")
	if err != nil {
		t.Fatalf("listReminders: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 reminder (rescheduled), got %d", len(list))
	}

	// The due_at should be in the future.
	newDue, err := time.Parse(time.RFC3339, list[0].DueAt)
	if err != nil {
		t.Fatalf("parse due_at: %v", err)
	}
	if !newDue.After(time.Now().UTC()) {
		t.Errorf("expected rescheduled due_at in future, got %v", newDue)
	}

	_ = rem // suppress unused
}

func TestReminderMaxPerUser(t *testing.T) {
	dbPath := testReminderDB(t)
	cfg := &Config{
		HistoryDB: dbPath,
		Reminders: ReminderConfig{Enabled: true, MaxPerUser: 3},
	}

	re := newReminderEngine(cfg, nil)

	due := time.Now().Add(1 * time.Hour)
	for i := 0; i < 3; i++ {
		_, err := re.addReminder(fmt.Sprintf("Reminder %d", i), due, "", "api", "user1")
		if err != nil {
			t.Fatalf("addReminder %d: %v", i, err)
		}
	}

	// 4th should fail.
	_, err := re.addReminder("Too many", due, "", "api", "user1")
	if err == nil {
		t.Error("expected error when exceeding max per user")
	}
	if !strings.Contains(err.Error(), "maximum") {
		t.Errorf("expected max limit error, got: %v", err)
	}
}

func TestNextCronTime(t *testing.T) {
	// Every day at 9:00.
	now := time.Date(2025, 3, 15, 10, 0, 0, 0, time.UTC)
	next := nextCronTime("0 9 * * *", now)
	if next.IsZero() {
		t.Fatal("expected non-zero next time")
	}
	if next.Hour() != 9 || next.Minute() != 0 {
		t.Errorf("expected 09:00, got %02d:%02d", next.Hour(), next.Minute())
	}
	if !next.After(now) {
		t.Errorf("expected next > now, got %v", next)
	}
}

func TestInitReminderDB(t *testing.T) {
	dbPath := testReminderDB(t)

	// Table should exist. Try inserting.
	sql := fmt.Sprintf(
		`INSERT INTO reminders (id, text, due_at, status, created_at)
		 VALUES ('test1', 'hello', '2025-01-01T00:00:00Z', 'pending', '2025-01-01T00:00:00Z')`)
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("insert into reminders: %s: %v", string(out), err)
	}

	// Verify it exists.
	rows, err := queryDB(dbPath, "SELECT * FROM reminders WHERE id = 'test1'")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
}
