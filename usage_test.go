package main

import (
	"path/filepath"
	"testing"
	"time"
)

func TestQueryUsageSummary(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test.db")
	if err := initHistoryDB(dbPath); err != nil {
		t.Fatal(err)
	}

	// Insert test data for today.
	now := time.Now()
	insertJobRun(dbPath, JobRun{
		JobID:     "u1",
		Name:      "test1",
		Source:    "test",
		StartedAt: now.Format(time.RFC3339),
		FinishedAt: now.Format(time.RFC3339),
		Status:    "success",
		CostUSD:   0.05,
		Model:     "sonnet",
		TokensIn:  1000,
		TokensOut: 500,
		Role:      "ruri",
	})
	insertJobRun(dbPath, JobRun{
		JobID:     "u2",
		Name:      "test2",
		Source:    "test",
		StartedAt: now.Format(time.RFC3339),
		FinishedAt: now.Format(time.RFC3339),
		Status:    "success",
		CostUSD:   0.10,
		Model:     "opus",
		TokensIn:  2000,
		TokensOut: 800,
		Role:      "kohaku",
	})

	summary, err := queryUsageSummary(dbPath, "today")
	if err != nil {
		t.Fatal(err)
	}

	if summary.Period != "today" {
		t.Errorf("expected period=today, got %s", summary.Period)
	}
	if summary.TotalTasks != 2 {
		t.Errorf("expected 2 tasks, got %d", summary.TotalTasks)
	}
	if summary.TotalCost < 0.14 || summary.TotalCost > 0.16 {
		t.Errorf("expected ~0.15 total cost, got %.4f", summary.TotalCost)
	}
	if summary.TokensIn != 3000 {
		t.Errorf("expected 3000 tokens in, got %d", summary.TokensIn)
	}
	if summary.TokensOut != 1300 {
		t.Errorf("expected 1300 tokens out, got %d", summary.TokensOut)
	}
}

func TestQueryUsageSummaryEmptyDB(t *testing.T) {
	summary, err := queryUsageSummary("", "today")
	if err != nil {
		t.Fatal(err)
	}
	if summary.TotalCost != 0 {
		t.Errorf("expected 0 cost for empty db, got %.4f", summary.TotalCost)
	}
}

func TestQueryUsageByModel(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test.db")
	if err := initHistoryDB(dbPath); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	insertJobRun(dbPath, JobRun{
		JobID: "m1", Name: "test1", Source: "test",
		StartedAt: now.Format(time.RFC3339), FinishedAt: now.Format(time.RFC3339),
		Status: "success", CostUSD: 0.30, Model: "opus",
		TokensIn: 1000, TokensOut: 500,
	})
	insertJobRun(dbPath, JobRun{
		JobID: "m2", Name: "test2", Source: "test",
		StartedAt: now.Format(time.RFC3339), FinishedAt: now.Format(time.RFC3339),
		Status: "success", CostUSD: 0.10, Model: "sonnet",
		TokensIn: 2000, TokensOut: 800,
	})
	insertJobRun(dbPath, JobRun{
		JobID: "m3", Name: "test3", Source: "test",
		StartedAt: now.Format(time.RFC3339), FinishedAt: now.Format(time.RFC3339),
		Status: "success", CostUSD: 0.10, Model: "opus",
		TokensIn: 500, TokensOut: 200,
	})

	models, err := queryUsageByModel(dbPath, 30)
	if err != nil {
		t.Fatal(err)
	}

	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}

	// Should be ordered by cost DESC, so opus first.
	if models[0].Model != "opus" {
		t.Errorf("expected first model=opus, got %s", models[0].Model)
	}
	if models[0].Tasks != 2 {
		t.Errorf("expected opus tasks=2, got %d", models[0].Tasks)
	}
	if models[0].Cost < 0.39 || models[0].Cost > 0.41 {
		t.Errorf("expected opus cost ~0.40, got %.4f", models[0].Cost)
	}
	if models[0].Pct < 79 || models[0].Pct > 81 {
		t.Errorf("expected opus pct ~80%%, got %.1f%%", models[0].Pct)
	}
}

func TestQueryUsageByRole(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test.db")
	if err := initHistoryDB(dbPath); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	insertJobRun(dbPath, JobRun{
		JobID: "r1", Name: "test1", Source: "test",
		StartedAt: now.Format(time.RFC3339), FinishedAt: now.Format(time.RFC3339),
		Status: "success", CostUSD: 0.20, Model: "sonnet",
		TokensIn: 1000, TokensOut: 500, Role: "ruri",
	})
	insertJobRun(dbPath, JobRun{
		JobID: "r2", Name: "test2", Source: "test",
		StartedAt: now.Format(time.RFC3339), FinishedAt: now.Format(time.RFC3339),
		Status: "success", CostUSD: 0.05, Model: "sonnet",
		TokensIn: 500, TokensOut: 200, Role: "kohaku",
	})

	roles, err := queryUsageByRole(dbPath, 30)
	if err != nil {
		t.Fatal(err)
	}

	if len(roles) != 2 {
		t.Fatalf("expected 2 roles, got %d", len(roles))
	}

	// Ordered by cost DESC.
	if roles[0].Role != "ruri" {
		t.Errorf("expected first role=ruri, got %s", roles[0].Role)
	}
}

func TestQueryExpensiveSessions(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test.db")
	if err := initHistoryDB(dbPath); err != nil {
		t.Fatal(err)
	}
	if err := initSessionDB(dbPath); err != nil {
		t.Fatal(err)
	}

	// Insert test sessions directly.
	now := time.Now().Format(time.RFC3339)
	queries := []string{
		"INSERT INTO sessions (id, role, title, total_cost, message_count, total_tokens_in, total_tokens_out, created_at, updated_at) VALUES ('s1', 'ruri', 'Expensive session', 1.50, 10, 5000, 3000, '" + now + "', '" + now + "')",
		"INSERT INTO sessions (id, role, title, total_cost, message_count, total_tokens_in, total_tokens_out, created_at, updated_at) VALUES ('s2', 'kohaku', 'Cheap session', 0.10, 3, 500, 200, '" + now + "', '" + now + "')",
		"INSERT INTO sessions (id, role, title, total_cost, message_count, total_tokens_in, total_tokens_out, created_at, updated_at) VALUES ('s3', 'hisui', 'Medium session', 0.50, 5, 2000, 1000, '" + now + "', '" + now + "')",
	}
	for _, sql := range queries {
		queryDB(dbPath, sql)
	}

	sessions, err := queryExpensiveSessions(dbPath, 5, 30)
	if err != nil {
		t.Fatal(err)
	}

	if len(sessions) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(sessions))
	}

	// Ordered by total_cost DESC.
	if sessions[0].SessionID != "s1" {
		t.Errorf("expected first session=s1, got %s", sessions[0].SessionID)
	}
	if sessions[0].TotalCost < 1.49 || sessions[0].TotalCost > 1.51 {
		t.Errorf("expected s1 cost ~1.50, got %.4f", sessions[0].TotalCost)
	}
	if sessions[1].SessionID != "s3" {
		t.Errorf("expected second session=s3, got %s", sessions[1].SessionID)
	}
}

func TestQueryCostTrend(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test.db")
	if err := initHistoryDB(dbPath); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	yesterday := now.AddDate(0, 0, -1)

	insertJobRun(dbPath, JobRun{
		JobID: "t1", Name: "test1", Source: "test",
		StartedAt: now.Format(time.RFC3339), FinishedAt: now.Format(time.RFC3339),
		Status: "success", CostUSD: 0.05, Model: "sonnet",
		TokensIn: 1000, TokensOut: 500,
	})
	insertJobRun(dbPath, JobRun{
		JobID: "t2", Name: "test2", Source: "test",
		StartedAt: yesterday.Format(time.RFC3339), FinishedAt: yesterday.Format(time.RFC3339),
		Status: "success", CostUSD: 0.10, Model: "opus",
		TokensIn: 2000, TokensOut: 800,
	})

	trend, err := queryCostTrend(dbPath, 7)
	if err != nil {
		t.Fatal(err)
	}

	if len(trend) < 1 {
		t.Fatal("expected at least 1 day in trend")
	}

	// Verify total across all days.
	var totalCost float64
	var totalTasks int
	for _, d := range trend {
		totalCost += d.Cost
		totalTasks += d.Tasks
	}
	if totalTasks != 2 {
		t.Errorf("expected 2 total tasks in trend, got %d", totalTasks)
	}
	if totalCost < 0.14 || totalCost > 0.16 {
		t.Errorf("expected ~0.15 total cost, got %.4f", totalCost)
	}
}

func TestFormatResponseCostFooter(t *testing.T) {
	// Disabled.
	cfg := &Config{}
	result := &ProviderResult{TokensIn: 1000, TokensOut: 500, CostUSD: 0.05}
	footer := formatResponseCostFooter(cfg, result)
	if footer != "" {
		t.Errorf("expected empty footer when disabled, got %q", footer)
	}

	// Enabled with default template.
	cfg.Usage.ShowFooter = true
	footer = formatResponseCostFooter(cfg, result)
	if footer != "1000in/500out ~$0.0500" {
		t.Errorf("unexpected footer: %q", footer)
	}

	// Custom template.
	cfg.Usage.FooterTemplate = "Cost: ${{.cost}} ({{.tokensIn}}+{{.tokensOut}})"
	footer = formatResponseCostFooter(cfg, result)
	if footer != "Cost: $0.0500 (1000+500)" {
		t.Errorf("unexpected custom footer: %q", footer)
	}

	// Nil result.
	footer = formatResponseCostFooter(cfg, nil)
	if footer != "" {
		t.Errorf("expected empty footer for nil result, got %q", footer)
	}

	// Nil config.
	footer = formatResponseCostFooter(nil, result)
	if footer != "" {
		t.Errorf("expected empty footer for nil config, got %q", footer)
	}
}

func TestFormatResultCostFooter(t *testing.T) {
	cfg := &Config{Usage: UsageConfig{ShowFooter: true}}
	result := &TaskResult{TokensIn: 500, TokensOut: 200, CostUSD: 0.02}
	footer := formatResultCostFooter(cfg, result)
	if footer != "500in/200out ~$0.0200" {
		t.Errorf("unexpected footer: %q", footer)
	}
}
