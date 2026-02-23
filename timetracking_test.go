package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTimeTracking_InitDB(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test.db")

	if err := initTimeTrackingDB(dbPath); err != nil {
		t.Fatalf("initTimeTrackingDB: %v", err)
	}

	// Verify table exists.
	rows, err := queryDB(dbPath, "SELECT name FROM sqlite_master WHERE type='table' AND name='time_entries';")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("time_entries table not created")
	}
}

func TestTimeTracking_StartStop(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test.db")
	if err := initTimeTrackingDB(dbPath); err != nil {
		t.Fatalf("initTimeTrackingDB: %v", err)
	}

	cfg := &Config{HistoryDB: dbPath}
	svc := newTimeTrackingService(cfg)

	// Start timer.
	entry, err := svc.StartTimer("testuser", "myproject", "coding", []string{"go"})
	if err != nil {
		t.Fatalf("StartTimer: %v", err)
	}
	if entry.Project != "myproject" {
		t.Errorf("project = %q, want 'myproject'", entry.Project)
	}
	if entry.Activity != "coding" {
		t.Errorf("activity = %q, want 'coding'", entry.Activity)
	}

	// Check running.
	running, err := svc.GetRunning("testuser")
	if err != nil {
		t.Fatalf("GetRunning: %v", err)
	}
	if running == nil {
		t.Fatal("expected running timer, got nil")
	}
	if running.ID != entry.ID {
		t.Errorf("running id = %q, want %q", running.ID, entry.ID)
	}

	// Stop timer.
	stopped, err := svc.StopTimer("testuser")
	if err != nil {
		t.Fatalf("StopTimer: %v", err)
	}
	if stopped.EndTime == "" {
		t.Error("expected end_time to be set")
	}
	if stopped.DurationMinutes < 1 {
		t.Errorf("expected duration >= 1, got %d", stopped.DurationMinutes)
	}

	// No running timer after stop.
	running, err = svc.GetRunning("testuser")
	if err != nil {
		t.Fatalf("GetRunning: %v", err)
	}
	if running != nil {
		t.Error("expected no running timer after stop")
	}
}

func TestTimeTracking_AutoStop(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test.db")
	if err := initTimeTrackingDB(dbPath); err != nil {
		t.Fatalf("initTimeTrackingDB: %v", err)
	}

	cfg := &Config{HistoryDB: dbPath}
	svc := newTimeTrackingService(cfg)

	// Start first timer.
	first, err := svc.StartTimer("testuser", "proj1", "task1", nil)
	if err != nil {
		t.Fatalf("StartTimer first: %v", err)
	}

	// Start second timer (should auto-stop first).
	second, err := svc.StartTimer("testuser", "proj2", "task2", nil)
	if err != nil {
		t.Fatalf("StartTimer second: %v", err)
	}

	// Verify first is stopped.
	rows, _ := queryDB(dbPath, "SELECT end_time FROM time_entries WHERE id = '"+first.ID+"';")
	if len(rows) == 0 {
		t.Fatal("first entry not found")
	}
	if jsonStr(rows[0]["end_time"]) == "" {
		t.Error("first timer should be auto-stopped")
	}

	// Verify second is running.
	running, err := svc.GetRunning("testuser")
	if err != nil {
		t.Fatalf("GetRunning: %v", err)
	}
	if running == nil || running.ID != second.ID {
		t.Error("second timer should be running")
	}
}

func TestTimeTracking_ManualLog(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test.db")
	if err := initTimeTrackingDB(dbPath); err != nil {
		t.Fatalf("initTimeTrackingDB: %v", err)
	}

	cfg := &Config{HistoryDB: dbPath}
	svc := newTimeTrackingService(cfg)

	entry, err := svc.LogEntry("testuser", "reading", "book", 60, "2025-01-15", "Read chapter 3", []string{"learn"})
	if err != nil {
		t.Fatalf("LogEntry: %v", err)
	}
	if entry.DurationMinutes != 60 {
		t.Errorf("duration = %d, want 60", entry.DurationMinutes)
	}
	if entry.Note != "Read chapter 3" {
		t.Errorf("note = %q, want 'Read chapter 3'", entry.Note)
	}
}

func TestTimeTracking_Report(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test.db")
	if err := initTimeTrackingDB(dbPath); err != nil {
		t.Fatalf("initTimeTrackingDB: %v", err)
	}

	cfg := &Config{HistoryDB: dbPath}
	svc := newTimeTrackingService(cfg)

	// Log some entries.
	svc.LogEntry("testuser", "proj1", "coding", 120, "", "session 1", nil)
	svc.LogEntry("testuser", "proj1", "review", 30, "", "session 2", nil)
	svc.LogEntry("testuser", "proj2", "design", 60, "", "session 3", nil)

	report, err := svc.Report("testuser", "month", "")
	if err != nil {
		t.Fatalf("Report: %v", err)
	}

	if report.EntryCount != 3 {
		t.Errorf("entry_count = %d, want 3", report.EntryCount)
	}
	if report.TotalHours < 3.4 || report.TotalHours > 3.6 {
		t.Errorf("total_hours = %.2f, want ~3.5", report.TotalHours)
	}
	if _, ok := report.ByProject["proj1"]; !ok {
		t.Error("expected proj1 in by_project")
	}
	if _, ok := report.ByProject["proj2"]; !ok {
		t.Error("expected proj2 in by_project")
	}
}

func TestTimeTracking_LogEntry_InvalidDuration(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test.db")
	_ = initTimeTrackingDB(dbPath)
	cfg := &Config{HistoryDB: dbPath}
	svc := newTimeTrackingService(cfg)

	_, err := svc.LogEntry("testuser", "proj", "act", 0, "", "", nil)
	if err == nil {
		t.Error("expected error for zero duration")
	}

	_, err = svc.LogEntry("testuser", "proj", "act", -5, "", "", nil)
	if err == nil {
		t.Error("expected error for negative duration")
	}
}

// Ensure unused import doesn't break.
var _ = os.DevNull
