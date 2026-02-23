package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func setupSLATestDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sla_test.db")
	if err := initHistoryDB(dbPath); err != nil {
		t.Fatalf("initHistoryDB: %v", err)
	}
	initSLADB(dbPath)
	return dbPath
}

func insertTestRun(t *testing.T, dbPath, role, status, startedAt, finishedAt string, cost float64) {
	t.Helper()
	run := JobRun{
		JobID:      newUUID(),
		Name:       "test-task",
		Source:     "test",
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
		Status:     status,
		CostUSD:    cost,
		Model:      "sonnet",
		SessionID:  newUUID(),
		Role:       role,
	}
	if err := insertJobRun(dbPath, run); err != nil {
		t.Fatalf("insertJobRun: %v", err)
	}
}

func TestInitSLADB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "init_sla.db")
	if err := initHistoryDB(dbPath); err != nil {
		t.Fatalf("initHistoryDB: %v", err)
	}
	initSLADB(dbPath)

	// Verify sla_checks table exists.
	rows, err := queryDB(dbPath, "SELECT name FROM sqlite_master WHERE type='table' AND name='sla_checks'")
	if err != nil {
		t.Fatalf("queryDB: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected sla_checks table, got %d tables", len(rows))
	}

	// Verify role column exists in job_runs.
	_, err = queryDB(dbPath, "SELECT role FROM job_runs LIMIT 0")
	if err != nil {
		t.Fatalf("role column not added to job_runs: %v", err)
	}
}

func TestQuerySLAMetricsEmpty(t *testing.T) {
	dbPath := setupSLATestDB(t)

	m, err := querySLAMetrics(dbPath, "翡翠", 24)
	if err != nil {
		t.Fatalf("querySLAMetrics: %v", err)
	}
	if m.Role != "翡翠" {
		t.Errorf("role = %q, want %q", m.Role, "翡翠")
	}
	if m.Total != 0 {
		t.Errorf("total = %d, want 0", m.Total)
	}
}

func TestQuerySLAMetricsWithData(t *testing.T) {
	dbPath := setupSLATestDB(t)

	now := time.Now()
	for i := 0; i < 8; i++ {
		start := now.Add(-time.Duration(i)*time.Minute - 30*time.Second)
		end := start.Add(time.Duration(10+i*5) * time.Second)
		insertTestRun(t, dbPath, "翡翠", "success",
			start.Format(time.RFC3339), end.Format(time.RFC3339), 0.10)
	}
	// Add 2 failures.
	for i := 0; i < 2; i++ {
		start := now.Add(-time.Duration(10+i) * time.Minute)
		end := start.Add(5 * time.Second)
		insertTestRun(t, dbPath, "翡翠", "error",
			start.Format(time.RFC3339), end.Format(time.RFC3339), 0.05)
	}

	m, err := querySLAMetrics(dbPath, "翡翠", 24)
	if err != nil {
		t.Fatalf("querySLAMetrics: %v", err)
	}

	if m.Total != 10 {
		t.Errorf("total = %d, want 10", m.Total)
	}
	if m.Success != 8 {
		t.Errorf("success = %d, want 8", m.Success)
	}
	if m.Fail != 2 {
		t.Errorf("fail = %d, want 2", m.Fail)
	}
	expectedRate := 0.8
	if m.SuccessRate != expectedRate {
		t.Errorf("successRate = %f, want %f", m.SuccessRate, expectedRate)
	}
	if m.TotalCost <= 0 {
		t.Errorf("totalCost = %f, want > 0", m.TotalCost)
	}
	if m.AvgLatencyMs <= 0 {
		t.Errorf("avgLatencyMs = %d, want > 0", m.AvgLatencyMs)
	}
}

func TestQuerySLAMetricsMultipleRoles(t *testing.T) {
	dbPath := setupSLATestDB(t)

	now := time.Now()
	start := now.Add(-5 * time.Minute)
	end := start.Add(30 * time.Second)
	startStr := start.Format(time.RFC3339)
	endStr := end.Format(time.RFC3339)

	insertTestRun(t, dbPath, "翡翠", "success", startStr, endStr, 0.10)
	insertTestRun(t, dbPath, "翡翠", "success", startStr, endStr, 0.10)
	insertTestRun(t, dbPath, "黒曜", "success", startStr, endStr, 0.20)
	insertTestRun(t, dbPath, "黒曜", "error", startStr, endStr, 0.15)

	m1, err := querySLAMetrics(dbPath, "翡翠", 24)
	if err != nil {
		t.Fatalf("querySLAMetrics 翡翠: %v", err)
	}
	if m1.Total != 2 || m1.Success != 2 {
		t.Errorf("翡翠: total=%d success=%d, want 2/2", m1.Total, m1.Success)
	}

	m2, err := querySLAMetrics(dbPath, "黒曜", 24)
	if err != nil {
		t.Fatalf("querySLAMetrics 黒曜: %v", err)
	}
	if m2.Total != 2 || m2.Success != 1 {
		t.Errorf("黒曜: total=%d success=%d, want 2/1", m2.Total, m2.Success)
	}
}

func TestQueryP95Latency(t *testing.T) {
	dbPath := setupSLATestDB(t)

	now := time.Now()
	// Insert 20 tasks with varying latencies (1s to 20s).
	for i := 1; i <= 20; i++ {
		start := now.Add(-time.Duration(25-i) * time.Minute)
		end := start.Add(time.Duration(i) * time.Second)
		insertTestRun(t, dbPath, "翡翠", "success",
			start.Format(time.RFC3339), end.Format(time.RFC3339), 0.01)
	}

	p95 := queryP95Latency(dbPath, "翡翠", 24)
	if p95 <= 0 {
		t.Errorf("p95 = %d, want > 0", p95)
	}
	// P95 of 1-20s should be around 19s (19000ms).
	if p95 < 15000 || p95 > 25000 {
		t.Errorf("p95 = %d, expected roughly 19000ms", p95)
	}
}

func TestSLAStatusViolation(t *testing.T) {
	dbPath := setupSLATestDB(t)

	now := time.Now()
	// 7 success, 3 fail = 70% success rate.
	for i := 0; i < 7; i++ {
		start := now.Add(-time.Duration(i+1) * time.Minute)
		end := start.Add(10 * time.Second)
		insertTestRun(t, dbPath, "翡翠", "success",
			start.Format(time.RFC3339), end.Format(time.RFC3339), 0.10)
	}
	for i := 0; i < 3; i++ {
		start := now.Add(-time.Duration(i+8) * time.Minute)
		end := start.Add(5 * time.Second)
		insertTestRun(t, dbPath, "翡翠", "error",
			start.Format(time.RFC3339), end.Format(time.RFC3339), 0.05)
	}

	cfg := &Config{
		HistoryDB: dbPath,
		Roles: map[string]RoleConfig{
			"翡翠": {Description: "research"},
		},
		SLA: SLAConfig{
			Enabled: true,
			Roles: map[string]RoleSLACfg{
				"翡翠": {MinSuccessRate: 0.95},
			},
		},
	}

	statuses, err := querySLAStatusAll(cfg)
	if err != nil {
		t.Fatalf("querySLAStatusAll: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("expected 1 status, got %d", len(statuses))
	}
	s := statuses[0]
	if s.Status != "violation" {
		t.Errorf("status = %q, want %q", s.Status, "violation")
	}
	if s.Violation == "" {
		t.Error("violation should not be empty")
	}
}

func TestSLAStatusOK(t *testing.T) {
	dbPath := setupSLATestDB(t)

	now := time.Now()
	for i := 0; i < 10; i++ {
		start := now.Add(-time.Duration(i+1) * time.Minute)
		end := start.Add(10 * time.Second)
		insertTestRun(t, dbPath, "翡翠", "success",
			start.Format(time.RFC3339), end.Format(time.RFC3339), 0.10)
	}

	cfg := &Config{
		HistoryDB: dbPath,
		Roles: map[string]RoleConfig{
			"翡翠": {Description: "research"},
		},
		SLA: SLAConfig{
			Enabled: true,
			Roles: map[string]RoleSLACfg{
				"翡翠": {MinSuccessRate: 0.90},
			},
		},
	}

	statuses, err := querySLAStatusAll(cfg)
	if err != nil {
		t.Fatalf("querySLAStatusAll: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("expected 1 status, got %d", len(statuses))
	}
	if statuses[0].Status != "ok" {
		t.Errorf("status = %q, want %q", statuses[0].Status, "ok")
	}
}

func TestRecordSLACheck(t *testing.T) {
	dbPath := setupSLATestDB(t)

	recordSLACheck(dbPath, SLACheckResult{
		Role:        "翡翠",
		Timestamp:   time.Now().Format(time.RFC3339),
		SuccessRate: 0.85,
		P95Latency:  30000,
		Violation:   true,
		Detail:      "success rate 85% < 95%",
	})

	results, err := querySLAHistory(dbPath, "翡翠", 10)
	if err != nil {
		t.Fatalf("querySLAHistory: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Role != "翡翠" {
		t.Errorf("role = %q, want %q", r.Role, "翡翠")
	}
	if !r.Violation {
		t.Error("violation should be true")
	}
	if r.SuccessRate != 0.85 {
		t.Errorf("successRate = %f, want 0.85", r.SuccessRate)
	}
}

func TestCheckSLAViolationsNotifies(t *testing.T) {
	dbPath := setupSLATestDB(t)

	now := time.Now()
	// 5 success, 5 fail = 50% success rate.
	for i := 0; i < 5; i++ {
		start := now.Add(-time.Duration(i+1) * time.Minute)
		end := start.Add(10 * time.Second)
		insertTestRun(t, dbPath, "黒曜", "success",
			start.Format(time.RFC3339), end.Format(time.RFC3339), 0.10)
	}
	for i := 0; i < 5; i++ {
		start := now.Add(-time.Duration(i+6) * time.Minute)
		end := start.Add(5 * time.Second)
		insertTestRun(t, dbPath, "黒曜", "error",
			start.Format(time.RFC3339), end.Format(time.RFC3339), 0.05)
	}

	cfg := &Config{
		HistoryDB: dbPath,
		SLA: SLAConfig{
			Enabled: true,
			Window:  "24h",
			Roles: map[string]RoleSLACfg{
				"黒曜": {MinSuccessRate: 0.90},
			},
		},
	}

	var notifications []string
	notifyFn := func(msg string) {
		notifications = append(notifications, msg)
	}

	checkSLAViolations(cfg, notifyFn)

	if len(notifications) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(notifications))
	}
	if notifications[0] == "" {
		t.Error("notification should not be empty")
	}

	// Check that it was recorded.
	results, err := querySLAHistory(dbPath, "黒曜", 10)
	if err != nil {
		t.Fatalf("querySLAHistory: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 check result, got %d", len(results))
	}
	if !results[0].Violation {
		t.Error("check result should be violation")
	}
}

func TestCheckSLAViolationsNoData(t *testing.T) {
	dbPath := setupSLATestDB(t)

	cfg := &Config{
		HistoryDB: dbPath,
		SLA: SLAConfig{
			Enabled: true,
			Roles: map[string]RoleSLACfg{
				"翡翠": {MinSuccessRate: 0.90},
			},
		},
	}

	var notifications []string
	checkSLAViolations(cfg, func(msg string) {
		notifications = append(notifications, msg)
	})

	// No data = no notifications.
	if len(notifications) != 0 {
		t.Errorf("expected 0 notifications, got %d", len(notifications))
	}
}

func TestSLAConfigDefaults(t *testing.T) {
	cfg := SLAConfig{}
	if cfg.checkIntervalOrDefault() != 1*time.Hour {
		t.Errorf("default checkInterval = %v, want 1h", cfg.checkIntervalOrDefault())
	}
	if cfg.windowOrDefault() != 24*time.Hour {
		t.Errorf("default window = %v, want 24h", cfg.windowOrDefault())
	}

	cfg2 := SLAConfig{CheckInterval: "30m", Window: "12h"}
	if cfg2.checkIntervalOrDefault() != 30*time.Minute {
		t.Errorf("checkInterval = %v, want 30m", cfg2.checkIntervalOrDefault())
	}
	if cfg2.windowOrDefault() != 12*time.Hour {
		t.Errorf("window = %v, want 12h", cfg2.windowOrDefault())
	}
}

func TestSLACheckerTick(t *testing.T) {
	dbPath := setupSLATestDB(t)

	cfg := &Config{
		HistoryDB: dbPath,
		SLA: SLAConfig{
			Enabled:       true,
			CheckInterval: "1s",
			Roles: map[string]RoleSLACfg{
				"翡翠": {MinSuccessRate: 0.90},
			},
		},
	}

	var called int
	checker := newSLAChecker(cfg, func(msg string) { called++ })

	// First tick should run immediately.
	checker.tick(slaTestContext())
	if checker.lastRun.IsZero() {
		t.Error("lastRun should be set after first tick")
	}

	// Second tick within interval should be skipped.
	checker.tick(slaTestContext())
}

func TestJobRunRoleField(t *testing.T) {
	dbPath := setupSLATestDB(t)

	task := Task{ID: "role-test", Name: "role-task"}
	result := TaskResult{
		Status:    "success",
		CostUSD:   0.05,
		Model:     "sonnet",
		SessionID: "s1",
	}

	recordHistory(dbPath, task.ID, task.Name, "test", "翡翠", task, result,
		"2026-02-22T00:00:00Z", "2026-02-22T00:01:00Z", "")

	runs, err := queryHistory(dbPath, "role-test", 1)
	if err != nil {
		t.Fatalf("queryHistory: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if runs[0].Role != "翡翠" {
		t.Errorf("role = %q, want %q", runs[0].Role, "翡翠")
	}
}

func TestSLAMetricsEmptyDB(t *testing.T) {
	m, err := querySLAMetrics("", "test", 24)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Role != "test" {
		t.Errorf("role = %q, want %q", m.Role, "test")
	}
}

func TestSLALatencyThreshold(t *testing.T) {
	dbPath := setupSLATestDB(t)

	now := time.Now()
	// Insert tasks with 2 minute latency.
	for i := 0; i < 5; i++ {
		start := now.Add(-time.Duration(i+1) * time.Minute * 3)
		end := start.Add(2 * time.Minute) // 120s = 120000ms
		insertTestRun(t, dbPath, "黒曜", "success",
			start.Format(time.RFC3339), end.Format(time.RFC3339), 0.10)
	}

	cfg := &Config{
		HistoryDB: dbPath,
		Roles: map[string]RoleConfig{
			"黒曜": {Description: "dev"},
		},
		SLA: SLAConfig{
			Enabled: true,
			Roles: map[string]RoleSLACfg{
				"黒曜": {MinSuccessRate: 0.90, MaxP95LatencyMs: 60000}, // max 60s
			},
		},
	}

	statuses, err := querySLAStatusAll(cfg)
	if err != nil {
		t.Fatalf("querySLAStatusAll: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("expected 1 status, got %d", len(statuses))
	}
	if statuses[0].Status != "violation" {
		t.Errorf("status = %q, want %q (p95 should exceed threshold)", statuses[0].Status, "violation")
	}
}

func slaTestContext() context.Context {
	return context.Background()
}

func TestSLADisabledNoOp(t *testing.T) {
	cfg := &Config{
		SLA: SLAConfig{Enabled: false},
	}
	// Should not panic.
	checkSLAViolations(cfg, nil)
}

// TestSLACheckHistoryQuery verifies querySLAHistory with and without role filter.
func TestSLACheckHistoryQuery(t *testing.T) {
	dbPath := setupSLATestDB(t)

	recordSLACheck(dbPath, SLACheckResult{
		Role: "翡翠", Timestamp: time.Now().Format(time.RFC3339),
		SuccessRate: 0.95, P95Latency: 10000, Violation: false,
	})
	recordSLACheck(dbPath, SLACheckResult{
		Role: "黒曜", Timestamp: time.Now().Format(time.RFC3339),
		SuccessRate: 0.80, P95Latency: 50000, Violation: true, Detail: "low success rate",
	})

	// Query all.
	all, err := querySLAHistory(dbPath, "", 10)
	if err != nil {
		t.Fatalf("querySLAHistory all: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("expected 2 results, got %d", len(all))
	}

	// Query filtered.
	filtered, err := querySLAHistory(dbPath, "黒曜", 10)
	if err != nil {
		t.Fatalf("querySLAHistory filtered: %v", err)
	}
	if len(filtered) != 1 {
		t.Errorf("expected 1 result, got %d", len(filtered))
	}
	if filtered[0].Role != "黒曜" {
		t.Errorf("role = %q, want %q", filtered[0].Role, "黒曜")
	}
}

// Ensure unused import doesn't cause issues.
var _ = os.DevNull
