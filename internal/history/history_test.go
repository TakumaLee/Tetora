package history

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func skipIfNoSQLite(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not found, skipping test")
	}
}

func mustInitDB(t *testing.T) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	if err := InitDB(dbPath); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	return dbPath
}

func insertRun(t *testing.T, dbPath string, run JobRun) {
	t.Helper()
	if err := InsertRun(dbPath, run); err != nil {
		t.Fatalf("InsertRun: %v", err)
	}
}

func baseRun(jobID, name, status string, minutesAgo int) JobRun {
	ts := time.Now().Add(-time.Duration(minutesAgo) * time.Minute).Format(time.RFC3339)
	return JobRun{
		JobID:      jobID,
		Name:       name,
		Source:     "cron",
		StartedAt:  ts,
		FinishedAt: ts,
		Status:     status,
	}
}

// --- QueryRecentFails ---

func TestQueryRecentFails_ReturnsFailStatuses(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := mustInitDB(t)

	insertRun(t, dbPath, baseRun("job-a", "Job A", "error", 60))
	insertRun(t, dbPath, baseRun("job-b", "Job B", "timeout", 90))
	insertRun(t, dbPath, baseRun("job-c", "Job C", "skipped_concurrent_limit", 120))
	insertRun(t, dbPath, baseRun("job-d", "Job D", "success", 30))

	runs, err := QueryRecentFails(dbPath, FailQuery{Days: 7, Limit: 100})
	if err != nil {
		t.Fatalf("QueryRecentFails: %v", err)
	}
	// skipped_concurrent_limit and success are both excluded; only error + timeout remain.
	if len(runs) != 2 {
		t.Errorf("got %d runs, want 2", len(runs))
	}
	for _, r := range runs {
		if r.Status == "success" || r.Status == "skipped_concurrent_limit" {
			t.Errorf("unexpected status %q in results: %+v", r.Status, r)
		}
	}
}

func TestQueryRecentFails_FilterByJobID(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := mustInitDB(t)

	insertRun(t, dbPath, baseRun("job-x", "Job X", "error", 60))
	insertRun(t, dbPath, baseRun("job-y", "Job Y", "error", 90))

	runs, err := QueryRecentFails(dbPath, FailQuery{JobID: "job-x", Days: 7, Limit: 100})
	if err != nil {
		t.Fatalf("QueryRecentFails: %v", err)
	}
	if len(runs) != 1 {
		t.Errorf("got %d runs, want 1", len(runs))
	}
	if runs[0].JobID != "job-x" {
		t.Errorf("got job_id=%q, want job-x", runs[0].JobID)
	}
}

func TestQueryRecentFails_DefaultDaysAndLimit(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := mustInitDB(t)

	// Insert one recent fail and one old one (40 days ago — beyond default 3-day window).
	insertRun(t, dbPath, baseRun("job-new", "New", "error", 60))
	old := baseRun("job-old", "Old", "error", 0)
	old.StartedAt = time.Now().AddDate(0, 0, -40).Format(time.RFC3339)
	old.FinishedAt = old.StartedAt
	insertRun(t, dbPath, old)

	runs, err := QueryRecentFails(dbPath, FailQuery{}) // zero value → defaults
	if err != nil {
		t.Fatalf("QueryRecentFails: %v", err)
	}
	if len(runs) != 1 {
		t.Errorf("got %d runs, want 1 (old run should be filtered out)", len(runs))
	}
}

func TestQueryRecentFails_LimitRespected(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := mustInitDB(t)

	for i := 0; i < 5; i++ {
		insertRun(t, dbPath, baseRun(fmt.Sprintf("job-%d", i), fmt.Sprintf("Job %d", i), "error", i+1))
	}

	runs, err := QueryRecentFails(dbPath, FailQuery{Days: 7, Limit: 3})
	if err != nil {
		t.Fatalf("QueryRecentFails: %v", err)
	}
	if len(runs) != 3 {
		t.Errorf("got %d runs, want 3", len(runs))
	}
}

// --- QueryConsecutiveFails ---

func TestQueryConsecutiveFails_DetectsStreak(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := mustInitDB(t)

	// job-streak: 4 consecutive errors (newest first in time)
	for i := 0; i < 4; i++ {
		insertRun(t, dbPath, baseRun("job-streak", "Streaky", "error", i+1))
	}
	// job-ok: success in most recent run, shouldn't appear
	insertRun(t, dbPath, baseRun("job-ok", "OK", "success", 1))
	insertRun(t, dbPath, baseRun("job-ok", "OK", "error", 2))

	results, err := QueryConsecutiveFails(dbPath, 3)
	if err != nil {
		t.Fatalf("QueryConsecutiveFails: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("got %d results, want 1", len(results))
	}
	if results[0].JobID != "job-streak" {
		t.Errorf("got job_id=%q, want job-streak", results[0].JobID)
	}
	if results[0].Streak < 3 {
		t.Errorf("streak=%d, want >= 3", results[0].Streak)
	}
}

func TestQueryConsecutiveFails_BelowThreshold(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := mustInitDB(t)

	// Only 2 consecutive errors, threshold is 3
	insertRun(t, dbPath, baseRun("job-short", "Short", "error", 1))
	insertRun(t, dbPath, baseRun("job-short", "Short", "error", 2))
	insertRun(t, dbPath, baseRun("job-short", "Short", "success", 3))

	results, err := QueryConsecutiveFails(dbPath, 3)
	if err != nil {
		t.Fatalf("QueryConsecutiveFails: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results, want 0 (streak below threshold)", len(results))
	}
}

func TestQueryConsecutiveFails_DefaultThreshold(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := mustInitDB(t)

	// 3 consecutive errors, threshold 0 → defaults to 3
	for i := 0; i < 3; i++ {
		insertRun(t, dbPath, baseRun("job-def", "Default", "error", i+1))
	}

	results, err := QueryConsecutiveFails(dbPath, 0)
	if err != nil {
		t.Fatalf("QueryConsecutiveFails: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("got %d results, want 1", len(results))
	}
}

func TestQueryConsecutiveFails_StreakBrokenBySuccess(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := mustInitDB(t)

	// Pattern (newest → oldest): error, success, error, error, error
	// Streak = 1 (broken at second run), so should NOT appear with threshold=3.
	insertRun(t, dbPath, baseRun("job-broken", "Broken", "error", 1))
	insertRun(t, dbPath, baseRun("job-broken", "Broken", "success", 2))
	insertRun(t, dbPath, baseRun("job-broken", "Broken", "error", 3))
	insertRun(t, dbPath, baseRun("job-broken", "Broken", "error", 4))
	insertRun(t, dbPath, baseRun("job-broken", "Broken", "error", 5))

	results, err := QueryConsecutiveFails(dbPath, 3)
	if err != nil {
		t.Fatalf("QueryConsecutiveFails: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results, want 0 (streak broken by success)", len(results))
	}
}

func TestQueryConsecutiveFails_SkippedNotCounted(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := mustInitDB(t)

	// 3 consecutive skipped_concurrent_limit — must NOT trigger consecutive-fail alert.
	// This was the root cause of the original bug.
	for i := 0; i < 3; i++ {
		insertRun(t, dbPath, baseRun("job-skipped", "Skipped", "skipped_concurrent_limit", i+1))
	}

	results, err := QueryConsecutiveFails(dbPath, 3)
	if err != nil {
		t.Fatalf("QueryConsecutiveFails: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results, want 0: skipped_concurrent_limit must not count as failure", len(results))
	}
}

func TestQueryConsecutiveFails_RealErrorsStillDetected(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := mustInitDB(t)

	// Contrast: 3 consecutive real errors MUST trigger.
	for i := 0; i < 3; i++ {
		insertRun(t, dbPath, baseRun("job-real-err", "RealErr", "error", i+1))
	}
	// Same job with skipped_concurrent_limit in parallel — skipped must not affect streak.
	for i := 0; i < 3; i++ {
		insertRun(t, dbPath, baseRun("job-real-err", "RealErr", "skipped_concurrent_limit", i+4))
	}

	results, err := QueryConsecutiveFails(dbPath, 3)
	if err != nil {
		t.Fatalf("QueryConsecutiveFails: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("got %d results, want 1: 3 real errors must be detected", len(results))
	}
	if results[0].JobID != "job-real-err" {
		t.Errorf("got job_id=%q, want job-real-err", results[0].JobID)
	}
	if results[0].Streak < 3 {
		t.Errorf("streak=%d, want >= 3", results[0].Streak)
	}
}

// --- QueryJobTrace ---

func TestQueryJobTrace_ReturnsRunsForJob(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := mustInitDB(t)

	for i := 0; i < 5; i++ {
		insertRun(t, dbPath, baseRun("job-trace", "Trace", "success", i+1))
	}
	insertRun(t, dbPath, baseRun("other-job", "Other", "success", 1))

	runs, err := QueryJobTrace(dbPath, "job-trace", 10)
	if err != nil {
		t.Fatalf("QueryJobTrace: %v", err)
	}
	if len(runs) != 5 {
		t.Errorf("got %d runs, want 5", len(runs))
	}
	for _, r := range runs {
		if r.JobID != "job-trace" {
			t.Errorf("unexpected job_id=%q in trace", r.JobID)
		}
	}
}

func TestQueryJobTrace_LimitRespected(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := mustInitDB(t)

	for i := 0; i < 8; i++ {
		insertRun(t, dbPath, baseRun("job-lim", "Lim", "success", i+1))
	}

	runs, err := QueryJobTrace(dbPath, "job-lim", 3)
	if err != nil {
		t.Fatalf("QueryJobTrace: %v", err)
	}
	if len(runs) != 3 {
		t.Errorf("got %d runs, want 3", len(runs))
	}
}

func TestQueryJobTrace_DefaultLimit(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := mustInitDB(t)

	for i := 0; i < 15; i++ {
		insertRun(t, dbPath, baseRun("job-dl", "DL", "success", i+1))
	}

	runs, err := QueryJobTrace(dbPath, "job-dl", 0) // 0 → defaults to 10
	if err != nil {
		t.Fatalf("QueryJobTrace: %v", err)
	}
	if len(runs) != 10 {
		t.Errorf("got %d runs, want 10 (default limit)", len(runs))
	}
}

func TestQueryJobTrace_OrderedMostRecentFirst(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := mustInitDB(t)

	insertRun(t, dbPath, baseRun("job-ord", "Ord", "error", 60))   // older
	insertRun(t, dbPath, baseRun("job-ord", "Ord", "success", 10)) // newer

	runs, err := QueryJobTrace(dbPath, "job-ord", 10)
	if err != nil {
		t.Fatalf("QueryJobTrace: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("got %d runs, want 2", len(runs))
	}
	if runs[0].Status != "success" {
		t.Errorf("first run should be newest (success), got %q", runs[0].Status)
	}
}

func TestQueryJobTrace_EmptyForUnknownJob(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := mustInitDB(t)

	runs, err := QueryJobTrace(dbPath, "no-such-job", 10)
	if err != nil {
		t.Fatalf("QueryJobTrace: %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("got %d runs, want 0", len(runs))
	}
}

// --- QueryDailyMetrics ---

func TestQueryDailyMetrics_SkippedNotCounted(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := mustInitDB(t)

	insertRun(t, dbPath, baseRun("job-a", "Job A", "success", 60))
	insertRun(t, dbPath, baseRun("job-b", "Job B", "error", 90))
	insertRun(t, dbPath, baseRun("job-c", "Job C", "skipped_concurrent_limit", 120))

	days, err := QueryDailyMetrics(dbPath, 7)
	if err != nil {
		t.Fatalf("QueryDailyMetrics: %v", err)
	}
	if len(days) == 0 {
		t.Fatal("expected at least one day bucket")
	}
	today := days[len(days)-1]
	// skipped_concurrent_limit excluded: total = success + error = 2
	if today.Tasks != 2 {
		t.Errorf("Tasks = %d, want 2 (skipped must not be counted)", today.Tasks)
	}
	if today.Success != 1 {
		t.Errorf("Success = %d, want 1", today.Success)
	}
	if today.Errors != 1 {
		t.Errorf("Errors = %d, want 1", today.Errors)
	}
}

// --- QueryProviderMetrics ---

func TestQueryProviderMetrics_SkippedNotCounted(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := mustInitDB(t)

	successRun := baseRun("job-a", "Job A", "success", 60)
	successRun.Model = "claude-sonnet"
	insertRun(t, dbPath, successRun)

	errorRun := baseRun("job-b", "Job B", "error", 90)
	errorRun.Model = "claude-sonnet"
	insertRun(t, dbPath, errorRun)

	skippedRun := baseRun("job-c", "Job C", "skipped_concurrent_limit", 120)
	skippedRun.Model = "claude-sonnet"
	insertRun(t, dbPath, skippedRun)

	metrics, err := QueryProviderMetrics(dbPath, 7)
	if err != nil {
		t.Fatalf("QueryProviderMetrics: %v", err)
	}
	if len(metrics) == 0 {
		t.Fatal("expected at least one provider bucket")
	}
	m := metrics[0]
	// skipped_concurrent_limit excluded: total = 2, errors = 1
	if m.Tasks != 2 {
		t.Errorf("Tasks = %d, want 2 (skipped must not be counted)", m.Tasks)
	}
	// error rate = 1/2 = 0.5
	if m.ErrorRate != 0.5 {
		t.Errorf("ErrorRate = %f, want 0.5", m.ErrorRate)
	}
}
