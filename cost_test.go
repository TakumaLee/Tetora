package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestResolveDowngradeModel(t *testing.T) {
	ad := AutoDowngradeConfig{
		Enabled: true,
		Thresholds: []DowngradeThreshold{
			{At: 0.7, Model: "sonnet"},
			{At: 0.9, Model: "haiku"},
		},
	}

	tests := []struct {
		utilization float64
		want        string
	}{
		{0.5, ""},       // below all thresholds
		{0.7, "sonnet"}, // exactly at 70%
		{0.8, "sonnet"}, // between 70-90%
		{0.9, "haiku"},  // exactly at 90%
		{0.95, "haiku"}, // above 90%
		{1.0, "haiku"},  // at 100%
		{0.0, ""},       // zero
	}

	for _, tt := range tests {
		got := resolveDowngradeModel(ad, tt.utilization)
		if got != tt.want {
			t.Errorf("resolveDowngradeModel(%.2f) = %q, want %q", tt.utilization, got, tt.want)
		}
	}
}

func TestCheckBudgetPaused(t *testing.T) {
	cfg := &Config{
		Budgets: BudgetConfig{Paused: true},
	}
	result := checkBudget(cfg, "", "", 0)
	if result.Allowed {
		t.Error("expected not allowed when paused")
	}
	if !result.Paused {
		t.Error("expected paused flag")
	}
	if result.AlertLevel != "paused" {
		t.Errorf("expected alertLevel=paused, got %s", result.AlertLevel)
	}
}

func TestCheckBudgetNoBudgets(t *testing.T) {
	cfg := &Config{}
	result := checkBudget(cfg, "", "", 0)
	if !result.Allowed {
		t.Error("expected allowed when no budgets configured")
	}
	if result.AlertLevel != "ok" {
		t.Errorf("expected alertLevel=ok, got %s", result.AlertLevel)
	}
}

func TestCheckBudgetWithDB(t *testing.T) {
	// Create temp DB.
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test.db")
	if err := initHistoryDB(dbPath); err != nil {
		t.Fatal(err)
	}

	// Insert some cost data for today.
	now := time.Now()
	insertJobRun(dbPath, JobRun{
		JobID:     "test1",
		Name:      "test",
		Source:    "test",
		StartedAt: now.Format(time.RFC3339),
		FinishedAt: now.Add(time.Minute).Format(time.RFC3339),
		Status:    "success",
		CostUSD:   5.0,
		Role:      "翡翠",
	})
	insertJobRun(dbPath, JobRun{
		JobID:     "test2",
		Name:      "test2",
		Source:    "test",
		StartedAt: now.Format(time.RFC3339),
		FinishedAt: now.Add(time.Minute).Format(time.RFC3339),
		Status:    "success",
		CostUSD:   3.0,
		Role:      "黒曜",
	})

	// Test global daily budget exceeded.
	cfg := &Config{
		HistoryDB: dbPath,
		Budgets: BudgetConfig{
			Global: GlobalBudget{Daily: 5.0}, // $5 limit, $8 spent
		},
	}
	result := checkBudget(cfg, "", "", 0)
	if result.Allowed {
		t.Error("expected not allowed when budget exceeded")
	}
	if !result.Exceeded {
		t.Error("expected exceeded flag")
	}
	if result.AlertLevel != "exceeded" {
		t.Errorf("expected alertLevel=exceeded, got %s", result.AlertLevel)
	}

	// Test global budget within limits.
	cfg.Budgets.Global.Daily = 20.0
	result = checkBudget(cfg, "", "", 0)
	if !result.Allowed {
		t.Error("expected allowed when within budget")
	}
	if result.AlertLevel != "ok" {
		t.Errorf("expected alertLevel=ok, got %s", result.AlertLevel)
	}

	// Test global budget at warning level (70%).
	cfg.Budgets.Global.Daily = 10.0 // $8/$10 = 80% → warning
	result = checkBudget(cfg, "", "", 0)
	if !result.Allowed {
		t.Error("expected allowed at warning level")
	}
	if result.AlertLevel != "warning" {
		t.Errorf("expected alertLevel=warning, got %s", result.AlertLevel)
	}

	// Test global budget at critical level (90%).
	cfg.Budgets.Global.Daily = 8.5 // $8/$8.5 = 94% → critical
	result = checkBudget(cfg, "", "", 0)
	if !result.Allowed {
		t.Error("expected allowed at critical level")
	}
	if result.AlertLevel != "critical" {
		t.Errorf("expected alertLevel=critical, got %s", result.AlertLevel)
	}

	// Test per-role budget exceeded.
	cfg.Budgets.Global.Daily = 100.0 // global OK
	cfg.Budgets.Roles = map[string]RoleBudget{
		"翡翠": {Daily: 3.0}, // $5 spent by 翡翠, limit $3
	}
	result = checkBudget(cfg, "翡翠", "", 0)
	if result.Allowed {
		t.Error("expected not allowed when role budget exceeded")
	}
	if !result.Exceeded {
		t.Error("expected exceeded flag for role")
	}

	// Test per-role budget OK for different role.
	result = checkBudget(cfg, "黒曜", "", 0)
	if !result.Allowed {
		t.Error("expected allowed for role without budget config")
	}
}

func TestCheckBudgetAutoDowngrade(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test.db")
	if err := initHistoryDB(dbPath); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	insertJobRun(dbPath, JobRun{
		JobID:     "test1",
		Name:      "test",
		Source:    "test",
		StartedAt: now.Format(time.RFC3339),
		FinishedAt: now.Add(time.Minute).Format(time.RFC3339),
		Status:    "success",
		CostUSD:   7.5,
	})

	cfg := &Config{
		HistoryDB: dbPath,
		Budgets: BudgetConfig{
			Global: GlobalBudget{Daily: 10.0}, // 75% utilized
			AutoDowngrade: AutoDowngradeConfig{
				Enabled: true,
				Thresholds: []DowngradeThreshold{
					{At: 0.7, Model: "sonnet"},
					{At: 0.9, Model: "haiku"},
				},
			},
		},
	}

	result := checkBudget(cfg, "", "", 0)
	if !result.Allowed {
		t.Error("expected allowed with auto-downgrade")
	}
	if result.DowngradeModel != "sonnet" {
		t.Errorf("expected downgradeModel=sonnet, got %q", result.DowngradeModel)
	}
}

func TestQuerySpend(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test.db")
	if err := initHistoryDB(dbPath); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	insertJobRun(dbPath, JobRun{
		JobID:     "t1",
		Name:      "test",
		Source:    "test",
		StartedAt: now.Format(time.RFC3339),
		FinishedAt: now.Add(time.Minute).Format(time.RFC3339),
		Status:    "success",
		CostUSD:   2.5,
		Role:      "翡翠",
	})
	insertJobRun(dbPath, JobRun{
		JobID:     "t2",
		Name:      "test2",
		Source:    "test",
		StartedAt: now.Format(time.RFC3339),
		FinishedAt: now.Add(time.Minute).Format(time.RFC3339),
		Status:    "success",
		CostUSD:   1.5,
		Role:      "黒曜",
	})

	// Total spend.
	daily, weekly, monthly := querySpend(dbPath, "")
	if daily < 3.9 || daily > 4.1 {
		t.Errorf("expected daily ~4.0, got %.2f", daily)
	}
	if weekly < 3.9 || weekly > 4.1 {
		t.Errorf("expected weekly ~4.0, got %.2f", weekly)
	}
	if monthly < 3.9 || monthly > 4.1 {
		t.Errorf("expected monthly ~4.0, got %.2f", monthly)
	}

	// Per-role spend.
	daily, _, _ = querySpend(dbPath, "翡翠")
	if daily < 2.4 || daily > 2.6 {
		t.Errorf("expected role daily ~2.5, got %.2f", daily)
	}
}

func TestBudgetAlertTracker(t *testing.T) {
	tracker := newBudgetAlertTracker()
	tracker.cooldown = 100 * time.Millisecond

	// First alert should fire.
	if !tracker.shouldAlert("test:daily:warning") {
		t.Error("expected first alert to fire")
	}

	// Immediate second alert should be suppressed.
	if tracker.shouldAlert("test:daily:warning") {
		t.Error("expected second alert to be suppressed")
	}

	// Different key should fire.
	if !tracker.shouldAlert("test:daily:critical") {
		t.Error("expected different key to fire")
	}

	// After cooldown, same key should fire again.
	time.Sleep(150 * time.Millisecond)
	if !tracker.shouldAlert("test:daily:warning") {
		t.Error("expected alert to fire after cooldown")
	}
}

func TestSetBudgetPaused(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.json")
	os.WriteFile(configPath, []byte(`{"maxConcurrent": 3}`), 0644)

	// Pause.
	if err := setBudgetPaused(configPath, true); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(configPath)
	if !budgetContainsStr(string(data), `"paused": true`) {
		t.Error("expected paused=true in config")
	}

	// Resume.
	if err := setBudgetPaused(configPath, false); err != nil {
		t.Fatal(err)
	}

	data, _ = os.ReadFile(configPath)
	if !budgetContainsStr(string(data), `"paused": false`) {
		t.Error("expected paused=false in config")
	}
}

func TestQueryBudgetStatus(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test.db")
	if err := initHistoryDB(dbPath); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{
		HistoryDB: dbPath,
		Budgets: BudgetConfig{
			Global: GlobalBudget{Daily: 10.0, Weekly: 50.0},
			Roles: map[string]RoleBudget{
				"翡翠": {Daily: 3.0},
			},
		},
	}

	status := queryBudgetStatus(cfg)
	if status.Global == nil {
		t.Fatal("expected global meter")
	}
	if status.Global.DailyLimit != 10.0 {
		t.Errorf("expected daily limit 10.0, got %.2f", status.Global.DailyLimit)
	}
	if status.Global.WeeklyLimit != 50.0 {
		t.Errorf("expected weekly limit 50.0, got %.2f", status.Global.WeeklyLimit)
	}
	if len(status.Roles) != 1 {
		t.Errorf("expected 1 role meter, got %d", len(status.Roles))
	}
}

func TestFormatBudgetSummary(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test.db")
	if err := initHistoryDB(dbPath); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{
		HistoryDB: dbPath,
		Budgets: BudgetConfig{
			Global: GlobalBudget{Daily: 10.0},
		},
	}

	summary := formatBudgetSummary(cfg)
	if summary == "" {
		t.Error("expected non-empty summary")
	}
	if !budgetContainsStr(summary, "Today:") {
		t.Errorf("expected 'Today:' in summary, got: %s", summary)
	}
}

func budgetContainsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && budgetFindStr(s, substr))
}

func budgetFindStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
