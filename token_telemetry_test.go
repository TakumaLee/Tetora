package main

import (
	"path/filepath"
	"testing"
	"time"
)

func TestInitTokenTelemetry(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test.db")

	if err := initTokenTelemetry(dbPath); err != nil {
		t.Fatalf("initTokenTelemetry failed: %v", err)
	}

	// Calling it again should be idempotent (CREATE TABLE IF NOT EXISTS).
	if err := initTokenTelemetry(dbPath); err != nil {
		t.Fatalf("second initTokenTelemetry failed: %v", err)
	}

	// Verify table exists by querying it.
	rows, err := queryDB(dbPath, "SELECT COUNT(*) as cnt FROM token_telemetry;")
	if err != nil {
		t.Fatalf("query token_telemetry failed: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if jsonInt(rows[0]["cnt"]) != 0 {
		t.Errorf("expected 0 rows in empty table, got %d", jsonInt(rows[0]["cnt"]))
	}
}

func TestInitTokenTelemetryEmptyPath(t *testing.T) {
	// Empty dbPath should be a no-op.
	if err := initTokenTelemetry(""); err != nil {
		t.Fatalf("expected nil error for empty path, got: %v", err)
	}
}

func TestRecordAndQueryTokenTelemetry(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test.db")

	if err := initTokenTelemetry(dbPath); err != nil {
		t.Fatalf("initTokenTelemetry failed: %v", err)
	}

	now := time.Now().Format(time.RFC3339)

	// Record two entries with different complexity levels.
	recordTokenTelemetry(dbPath, TokenTelemetryEntry{
		TaskID:             "task-001",
		Role:               "ruri",
		Complexity:         "simple",
		Provider:           "anthropic",
		Model:              "haiku",
		SystemPromptTokens: 200,
		ContextTokens:      100,
		ToolDefsTokens:     0,
		InputTokens:        500,
		OutputTokens:       150,
		CostUSD:            0.001,
		DurationMs:         1200,
		Source:             "telegram",
		CreatedAt:          now,
	})

	recordTokenTelemetry(dbPath, TokenTelemetryEntry{
		TaskID:             "task-002",
		Role:               "kohaku",
		Complexity:         "complex",
		Provider:           "anthropic",
		Model:              "sonnet",
		SystemPromptTokens: 1500,
		ContextTokens:      800,
		ToolDefsTokens:     500,
		InputTokens:        3000,
		OutputTokens:       1200,
		CostUSD:            0.05,
		DurationMs:         8500,
		Source:             "discord",
		CreatedAt:          now,
	})

	recordTokenTelemetry(dbPath, TokenTelemetryEntry{
		TaskID:             "task-003",
		Role:               "ruri",
		Complexity:         "complex",
		Provider:           "anthropic",
		Model:              "sonnet",
		SystemPromptTokens: 1600,
		ContextTokens:      900,
		ToolDefsTokens:     500,
		InputTokens:        3500,
		OutputTokens:       1400,
		CostUSD:            0.06,
		DurationMs:         9000,
		Source:             "telegram",
		CreatedAt:          now,
	})

	// Query summary (by complexity).
	summaryRows, err := queryTokenUsageSummary(dbPath, 7)
	if err != nil {
		t.Fatalf("queryTokenUsageSummary failed: %v", err)
	}

	summary := parseTokenSummaryRows(summaryRows)

	if len(summary) != 2 {
		t.Fatalf("expected 2 complexity groups, got %d", len(summary))
	}

	// Ordered by total_cost DESC, so "complex" should be first.
	if summary[0].Complexity != "complex" {
		t.Errorf("expected first group=complex, got %s", summary[0].Complexity)
	}
	if summary[0].RequestCount != 2 {
		t.Errorf("expected 2 complex requests, got %d", summary[0].RequestCount)
	}
	if summary[0].TotalInput != 6500 {
		t.Errorf("expected complex total_input=6500, got %d", summary[0].TotalInput)
	}
	if summary[0].TotalOutput != 2600 {
		t.Errorf("expected complex total_output=2600, got %d", summary[0].TotalOutput)
	}
	if summary[0].TotalCost < 0.10 || summary[0].TotalCost > 0.12 {
		t.Errorf("expected complex total_cost ~0.11, got %.4f", summary[0].TotalCost)
	}

	if summary[1].Complexity != "simple" {
		t.Errorf("expected second group=simple, got %s", summary[1].Complexity)
	}
	if summary[1].RequestCount != 1 {
		t.Errorf("expected 1 simple request, got %d", summary[1].RequestCount)
	}

	// Query by role.
	roleRows, err := queryTokenUsageByRole(dbPath, 7)
	if err != nil {
		t.Fatalf("queryTokenUsageByRole failed: %v", err)
	}

	roles := parseTokenRoleRows(roleRows)

	if len(roles) != 3 {
		t.Fatalf("expected 3 role/complexity groups, got %d", len(roles))
	}

	// First entry should be the highest cost (ruri/complex: $0.06).
	if roles[0].Role != "ruri" || roles[0].Complexity != "complex" {
		t.Errorf("expected first entry ruri/complex, got %s/%s", roles[0].Role, roles[0].Complexity)
	}
}

func TestQueryTokenUsageSummaryEmptyDB(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test.db")

	if err := initTokenTelemetry(dbPath); err != nil {
		t.Fatalf("initTokenTelemetry failed: %v", err)
	}

	rows, err := queryTokenUsageSummary(dbPath, 7)
	if err != nil {
		t.Fatalf("queryTokenUsageSummary on empty DB failed: %v", err)
	}
	if rows != nil {
		t.Errorf("expected nil for empty DB, got %v", rows)
	}
}

func TestQueryTokenUsageSummaryNoDBPath(t *testing.T) {
	rows, err := queryTokenUsageSummary("", 7)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if rows != nil {
		t.Errorf("expected nil for empty dbPath, got %v", rows)
	}
}

func TestQueryTokenUsageByRoleEmptyDB(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test.db")

	if err := initTokenTelemetry(dbPath); err != nil {
		t.Fatalf("initTokenTelemetry failed: %v", err)
	}

	rows, err := queryTokenUsageByRole(dbPath, 7)
	if err != nil {
		t.Fatalf("queryTokenUsageByRole on empty DB failed: %v", err)
	}
	if rows != nil {
		t.Errorf("expected nil for empty DB, got %v", rows)
	}
}

func TestQueryTokenUsageByRoleNoDBPath(t *testing.T) {
	rows, err := queryTokenUsageByRole("", 7)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if rows != nil {
		t.Errorf("expected nil for empty dbPath, got %v", rows)
	}
}

func TestRecordTokenTelemetryEmptyPath(t *testing.T) {
	// Should be a no-op, not panic.
	recordTokenTelemetry("", TokenTelemetryEntry{
		TaskID: "test", Role: "ruri", Complexity: "simple",
	})
}

func TestFormatTokenSummaryEmpty(t *testing.T) {
	result := formatTokenSummary(nil)
	if result != "  (no data)" {
		t.Errorf("expected '  (no data)', got %q", result)
	}
}

func TestFormatTokenByRoleEmpty(t *testing.T) {
	result := formatTokenByRole(nil)
	if result != "  (no data)" {
		t.Errorf("expected '  (no data)', got %q", result)
	}
}

func TestFormatTokenSummaryWithData(t *testing.T) {
	rows := []TokenSummaryRow{
		{
			Complexity: "complex", RequestCount: 5,
			AvgInput: 3000, AvgOutput: 1200,
			TotalCost: 0.25, TotalSystemPrompt: 7500,
		},
		{
			Complexity: "simple", RequestCount: 10,
			AvgInput: 500, AvgOutput: 150,
			TotalCost: 0.01, TotalSystemPrompt: 2000,
		},
	}

	result := formatTokenSummary(rows)
	if result == "  (no data)" {
		t.Error("expected formatted output, got (no data)")
	}
	// Basic structure check: should contain header and both rows.
	if len(result) < 100 {
		t.Errorf("formatted output too short: %q", result)
	}
}

func TestFormatTokenByRoleWithData(t *testing.T) {
	rows := []TokenRoleRow{
		{
			Role: "ruri", Complexity: "complex", RequestCount: 3,
			TotalInput: 9000, TotalOutput: 3600, TotalCost: 0.18,
		},
	}

	result := formatTokenByRole(rows)
	if result == "  (no data)" {
		t.Error("expected formatted output, got (no data)")
	}
}

func TestParseTokenSummaryRows(t *testing.T) {
	// Test with nil input.
	result := parseTokenSummaryRows(nil)
	if result != nil {
		t.Errorf("expected nil for nil input, got %v", result)
	}

	// Test with actual data.
	rows := []map[string]any{
		{
			"complexity":          "simple",
			"request_count":       float64(5),
			"total_system_prompt": float64(1000),
			"total_context":       float64(500),
			"total_tool_defs":     float64(0),
			"total_input":         float64(2500),
			"total_output":        float64(750),
			"total_cost":          float64(0.005),
			"avg_input":           float64(500),
			"avg_output":          float64(150),
		},
	}

	parsed := parseTokenSummaryRows(rows)
	if len(parsed) != 1 {
		t.Fatalf("expected 1 row, got %d", len(parsed))
	}
	if parsed[0].Complexity != "simple" {
		t.Errorf("expected complexity=simple, got %s", parsed[0].Complexity)
	}
	if parsed[0].RequestCount != 5 {
		t.Errorf("expected requestCount=5, got %d", parsed[0].RequestCount)
	}
}

func TestParseTokenRoleRows(t *testing.T) {
	result := parseTokenRoleRows(nil)
	if result != nil {
		t.Errorf("expected nil for nil input, got %v", result)
	}

	rows := []map[string]any{
		{
			"role":          "kohaku",
			"complexity":    "complex",
			"request_count": float64(3),
			"total_input":   float64(9000),
			"total_output":  float64(3600),
			"total_cost":    float64(0.15),
		},
	}

	parsed := parseTokenRoleRows(rows)
	if len(parsed) != 1 {
		t.Fatalf("expected 1 row, got %d", len(parsed))
	}
	if parsed[0].Role != "kohaku" {
		t.Errorf("expected role=kohaku, got %s", parsed[0].Role)
	}
	if parsed[0].TotalCost < 0.14 || parsed[0].TotalCost > 0.16 {
		t.Errorf("expected totalCost ~0.15, got %.4f", parsed[0].TotalCost)
	}
}
