package main

import (
	"os"
	"path/filepath"
	"testing"
)

// helper: create a temp history DB and populate with test data.
func setupMetricsTestDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_metrics.db")

	if err := initHistoryDB(dbPath); err != nil {
		t.Fatalf("initHistoryDB: %v", err)
	}

	// Insert test records spanning multiple days and statuses.
	runs := []JobRun{
		{JobID: "j1", Name: "task-a", Source: "cron", StartedAt: "2026-02-20T10:00:00Z", FinishedAt: "2026-02-20T10:01:00Z", Status: "success", CostUSD: 0.10, Model: "opus", TokensIn: 1000, TokensOut: 500},
		{JobID: "j2", Name: "task-b", Source: "cron", StartedAt: "2026-02-20T11:00:00Z", FinishedAt: "2026-02-20T11:02:00Z", Status: "error", CostUSD: 0.05, Model: "opus", Error: "fail", TokensIn: 800, TokensOut: 200},
		{JobID: "j3", Name: "task-c", Source: "http", StartedAt: "2026-02-21T09:00:00Z", FinishedAt: "2026-02-21T09:00:30Z", Status: "success", CostUSD: 0.08, Model: "sonnet", TokensIn: 500, TokensOut: 300},
		{JobID: "j4", Name: "task-d", Source: "http", StartedAt: "2026-02-21T14:00:00Z", FinishedAt: "2026-02-21T14:05:00Z", Status: "timeout", CostUSD: 0.20, Model: "sonnet", TokensIn: 2000, TokensOut: 1000},
		{JobID: "j5", Name: "task-e", Source: "cron", StartedAt: "2026-02-22T08:00:00Z", FinishedAt: "2026-02-22T08:00:15Z", Status: "success", CostUSD: 0.03, Model: "opus", TokensIn: 300, TokensOut: 150},
	}
	for _, run := range runs {
		if err := insertJobRun(dbPath, run); err != nil {
			t.Fatalf("insertJobRun: %v", err)
		}
	}
	return dbPath
}

func TestQueryMetrics_EmptyDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "empty.db")
	if err := initHistoryDB(dbPath); err != nil {
		t.Fatalf("initHistoryDB: %v", err)
	}
	m, err := queryMetrics(dbPath, 30)
	if err != nil {
		t.Fatalf("queryMetrics: %v", err)
	}
	if m.TotalTasks != 0 {
		t.Errorf("expected 0 tasks, got %d", m.TotalTasks)
	}
	if m.SuccessRate != 0 {
		t.Errorf("expected 0 success rate, got %f", m.SuccessRate)
	}
}

func TestQueryMetrics_WithData(t *testing.T) {
	dbPath := setupMetricsTestDB(t)
	m, err := queryMetrics(dbPath, 30)
	if err != nil {
		t.Fatalf("queryMetrics: %v", err)
	}
	if m.TotalTasks != 5 {
		t.Errorf("expected 5 tasks, got %d", m.TotalTasks)
	}
	// 3 success out of 5
	expectedRate := 3.0 / 5.0
	if m.SuccessRate < expectedRate-0.01 || m.SuccessRate > expectedRate+0.01 {
		t.Errorf("expected success rate ~%f, got %f", expectedRate, m.SuccessRate)
	}
	expectedTokensIn := 1000 + 800 + 500 + 2000 + 300
	if m.TotalTokensIn != expectedTokensIn {
		t.Errorf("expected TotalTokensIn=%d, got %d", expectedTokensIn, m.TotalTokensIn)
	}
	expectedTokensOut := 500 + 200 + 300 + 1000 + 150
	if m.TotalTokensOut != expectedTokensOut {
		t.Errorf("expected TotalTokensOut=%d, got %d", expectedTokensOut, m.TotalTokensOut)
	}
	expectedCost := 0.10 + 0.05 + 0.08 + 0.20 + 0.03
	if m.TotalCostUSD < expectedCost-0.01 || m.TotalCostUSD > expectedCost+0.01 {
		t.Errorf("expected TotalCostUSD ~%f, got %f", expectedCost, m.TotalCostUSD)
	}
}

func TestQueryMetrics_EmptyPath(t *testing.T) {
	m, err := queryMetrics("", 30)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.TotalTasks != 0 {
		t.Errorf("expected 0 tasks for empty path, got %d", m.TotalTasks)
	}
}

func TestQueryDailyMetrics_WithData(t *testing.T) {
	dbPath := setupMetricsTestDB(t)
	daily, err := queryDailyMetrics(dbPath, 30)
	if err != nil {
		t.Fatalf("queryDailyMetrics: %v", err)
	}
	if len(daily) < 2 {
		t.Fatalf("expected at least 2 daily entries, got %d", len(daily))
	}

	// Check that we have data for multiple dates.
	dates := make(map[string]bool)
	totalTasks := 0
	for _, d := range daily {
		dates[d.Date] = true
		totalTasks += d.Tasks
		// Token fields should be populated.
		if d.TokensIn < 0 {
			t.Errorf("negative TokensIn for date %s", d.Date)
		}
	}
	if totalTasks != 5 {
		t.Errorf("expected 5 total tasks across daily, got %d", totalTasks)
	}
}

func TestQueryDailyMetrics_EmptyDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "empty.db")
	if err := initHistoryDB(dbPath); err != nil {
		t.Fatalf("initHistoryDB: %v", err)
	}
	daily, err := queryDailyMetrics(dbPath, 7)
	if err != nil {
		t.Fatalf("queryDailyMetrics: %v", err)
	}
	if len(daily) != 0 {
		t.Errorf("expected 0 daily entries for empty DB, got %d", len(daily))
	}
}

func TestQueryDailyMetrics_EmptyPath(t *testing.T) {
	daily, err := queryDailyMetrics("", 7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if daily != nil {
		t.Errorf("expected nil for empty path, got %v", daily)
	}
}

func TestQueryProviderMetrics_WithData(t *testing.T) {
	dbPath := setupMetricsTestDB(t)
	pm, err := queryProviderMetrics(dbPath, 30)
	if err != nil {
		t.Fatalf("queryProviderMetrics: %v", err)
	}
	if len(pm) < 2 {
		t.Fatalf("expected at least 2 model entries, got %d", len(pm))
	}

	// Verify we have both opus and sonnet.
	models := make(map[string]ProviderMetrics)
	for _, m := range pm {
		models[m.Model] = m
	}

	opus, ok := models["opus"]
	if !ok {
		t.Fatal("expected opus model in results")
	}
	if opus.Tasks != 3 {
		t.Errorf("expected 3 opus tasks, got %d", opus.Tasks)
	}
	// opus: 1 error out of 3 => error rate ~0.33
	if opus.ErrorRate < 0.30 || opus.ErrorRate > 0.35 {
		t.Errorf("expected opus error rate ~0.33, got %f", opus.ErrorRate)
	}

	sonnet, ok := models["sonnet"]
	if !ok {
		t.Fatal("expected sonnet model in results")
	}
	if sonnet.Tasks != 2 {
		t.Errorf("expected 2 sonnet tasks, got %d", sonnet.Tasks)
	}
	// sonnet: 1 timeout out of 2 => error rate 0.5
	if sonnet.ErrorRate < 0.45 || sonnet.ErrorRate > 0.55 {
		t.Errorf("expected sonnet error rate ~0.5, got %f", sonnet.ErrorRate)
	}
}

func TestQueryProviderMetrics_EmptyDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "empty.db")
	if err := initHistoryDB(dbPath); err != nil {
		t.Fatalf("initHistoryDB: %v", err)
	}
	pm, err := queryProviderMetrics(dbPath, 30)
	if err != nil {
		t.Fatalf("queryProviderMetrics: %v", err)
	}
	if len(pm) != 0 {
		t.Errorf("expected 0 provider entries for empty DB, got %d", len(pm))
	}
}

func TestQueryProviderMetrics_EmptyPath(t *testing.T) {
	pm, err := queryProviderMetrics("", 30)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pm != nil {
		t.Errorf("expected nil for empty path, got %v", pm)
	}
}

func TestInitHistoryDB_TokenMigrations(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "migrate.db")

	// First init creates base table.
	if err := initHistoryDB(dbPath); err != nil {
		t.Fatalf("first initHistoryDB: %v", err)
	}

	// Second init should succeed (idempotent migrations).
	if err := initHistoryDB(dbPath); err != nil {
		t.Fatalf("second initHistoryDB: %v", err)
	}

	// Verify we can insert a row with token data.
	run := JobRun{
		JobID:      "test-migrate",
		Name:       "migration-test",
		Source:     "test",
		StartedAt:  "2026-02-22T00:00:00Z",
		FinishedAt: "2026-02-22T00:01:00Z",
		Status:     "success",
		TokensIn:   999,
		TokensOut:  444,
	}
	if err := insertJobRun(dbPath, run); err != nil {
		t.Fatalf("insertJobRun after migration: %v", err)
	}

	// Query it back.
	runs, err := queryHistory(dbPath, "test-migrate", 1)
	if err != nil {
		t.Fatalf("queryHistory: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if runs[0].TokensIn != 999 {
		t.Errorf("expected TokensIn=999, got %d", runs[0].TokensIn)
	}
	if runs[0].TokensOut != 444 {
		t.Errorf("expected TokensOut=444, got %d", runs[0].TokensOut)
	}
}

func TestRecordHistory_IncludesTokens(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "record.db")
	if err := initHistoryDB(dbPath); err != nil {
		t.Fatalf("initHistoryDB: %v", err)
	}

	task := Task{ID: "rec-tok", Name: "token-task"}
	result := TaskResult{
		Status:    "success",
		CostUSD:   0.05,
		Model:     "opus",
		SessionID: "s1",
		TokensIn:  1234,
		TokensOut: 567,
	}

	recordHistory(dbPath, task.ID, task.Name, "test", "", task, result,
		"2026-02-22T00:00:00Z", "2026-02-22T00:01:00Z", "")

	runs, err := queryHistory(dbPath, "rec-tok", 1)
	if err != nil {
		t.Fatalf("queryHistory: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if runs[0].TokensIn != 1234 {
		t.Errorf("expected TokensIn=1234, got %d", runs[0].TokensIn)
	}
	if runs[0].TokensOut != 567 {
		t.Errorf("expected TokensOut=567, got %d", runs[0].TokensOut)
	}
}

// TestMetricsResult_ZeroDays verifies default behavior with zero days.
func TestQueryMetrics_ZeroDays(t *testing.T) {
	dbPath := setupMetricsTestDB(t)
	m, err := queryMetrics(dbPath, 0)
	if err != nil {
		t.Fatalf("queryMetrics: %v", err)
	}
	// 0 days should default to 30
	if m.TotalTasks != 5 {
		t.Errorf("expected 5 tasks with 0 days (default 30), got %d", m.TotalTasks)
	}
}

// Verify temp dir cleanup.
func TestSetupMetricsTestDB_Cleanup(t *testing.T) {
	dbPath := setupMetricsTestDB(t)
	if _, err := os.Stat(dbPath); err != nil {
		t.Errorf("DB should exist: %v", err)
	}
}
