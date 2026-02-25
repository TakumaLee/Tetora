package main

import (
	"fmt"
	"strings"
	"time"
)

// --- Token Telemetry ---
// Tracks detailed token usage breakdown per task for cost optimization analysis.
// Records system prompt, context, tool definition, input, and output token counts
// alongside cost and duration data.

// TokenTelemetryEntry holds the token breakdown for a single task execution.
type TokenTelemetryEntry struct {
	TaskID             string
	Role               string
	Complexity         string
	Provider           string
	Model              string
	SystemPromptTokens int
	ContextTokens      int
	ToolDefsTokens     int
	InputTokens        int
	OutputTokens       int
	CostUSD            float64
	DurationMs         int64
	Source             string
	CreatedAt          string
}

// initTokenTelemetry creates the token_telemetry table if it doesn't exist.
func initTokenTelemetry(dbPath string) error {
	if dbPath == "" {
		return nil
	}
	sql := `CREATE TABLE IF NOT EXISTS token_telemetry (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		task_id TEXT,
		role TEXT,
		complexity TEXT,
		provider TEXT,
		model TEXT,
		system_prompt_tokens INTEGER DEFAULT 0,
		context_tokens INTEGER DEFAULT 0,
		tool_defs_tokens INTEGER DEFAULT 0,
		input_tokens INTEGER DEFAULT 0,
		output_tokens INTEGER DEFAULT 0,
		cost_usd REAL DEFAULT 0,
		duration_ms INTEGER DEFAULT 0,
		source TEXT,
		created_at TEXT
	);`
	return execDB(dbPath, sql)
}

// recordTokenTelemetry stores token usage data for a completed task.
// Called asynchronously (goroutine) to avoid blocking task execution.
func recordTokenTelemetry(dbPath string, entry TokenTelemetryEntry) {
	if dbPath == "" {
		return
	}
	sql := fmt.Sprintf(
		`INSERT INTO token_telemetry
			(task_id, role, complexity, provider, model, system_prompt_tokens, context_tokens,
			 tool_defs_tokens, input_tokens, output_tokens, cost_usd, duration_ms, source, created_at)
		 VALUES ('%s', '%s', '%s', '%s', '%s', %d, %d, %d, %d, %d, %.6f, %d, '%s', '%s');`,
		escapeSQLite(entry.TaskID),
		escapeSQLite(entry.Role),
		escapeSQLite(entry.Complexity),
		escapeSQLite(entry.Provider),
		escapeSQLite(entry.Model),
		entry.SystemPromptTokens,
		entry.ContextTokens,
		entry.ToolDefsTokens,
		entry.InputTokens,
		entry.OutputTokens,
		entry.CostUSD,
		entry.DurationMs,
		escapeSQLite(entry.Source),
		escapeSQLite(entry.CreatedAt),
	)
	if err := execDB(dbPath, sql); err != nil {
		logWarn("record token telemetry failed", "error", err, "taskId", entry.TaskID)
	}
}

// queryTokenUsageSummary returns a summary of token usage grouped by complexity.
func queryTokenUsageSummary(dbPath string, days int) ([]map[string]any, error) {
	if dbPath == "" {
		return nil, nil
	}
	if days <= 0 {
		days = 7
	}
	since := time.Now().AddDate(0, 0, -days).Format("2006-01-02")
	sql := fmt.Sprintf(
		`SELECT
			complexity,
			COUNT(*) as request_count,
			COALESCE(SUM(system_prompt_tokens), 0) as total_system_prompt,
			COALESCE(SUM(context_tokens), 0) as total_context,
			COALESCE(SUM(tool_defs_tokens), 0) as total_tool_defs,
			COALESCE(SUM(input_tokens), 0) as total_input,
			COALESCE(SUM(output_tokens), 0) as total_output,
			COALESCE(SUM(cost_usd), 0) as total_cost,
			COALESCE(AVG(input_tokens), 0) as avg_input,
			COALESCE(AVG(output_tokens), 0) as avg_output
		 FROM token_telemetry
		 WHERE date(created_at) >= '%s'
		 GROUP BY complexity
		 ORDER BY total_cost DESC;`, since)
	return queryDB(dbPath, sql)
}

// queryTokenUsageByRole returns token usage grouped by role and complexity.
func queryTokenUsageByRole(dbPath string, days int) ([]map[string]any, error) {
	if dbPath == "" {
		return nil, nil
	}
	if days <= 0 {
		days = 7
	}
	since := time.Now().AddDate(0, 0, -days).Format("2006-01-02")
	sql := fmt.Sprintf(
		`SELECT
			CASE WHEN role = '' THEN '(unassigned)' ELSE role END as role,
			complexity,
			COUNT(*) as request_count,
			COALESCE(SUM(input_tokens), 0) as total_input,
			COALESCE(SUM(output_tokens), 0) as total_output,
			COALESCE(SUM(cost_usd), 0) as total_cost
		 FROM token_telemetry
		 WHERE date(created_at) >= '%s'
		 GROUP BY role, complexity
		 ORDER BY total_cost DESC;`, since)
	return queryDB(dbPath, sql)
}

// --- Token Telemetry Summary Types (for CLI + API) ---

// TokenSummaryRow is a parsed row from queryTokenUsageSummary.
type TokenSummaryRow struct {
	Complexity        string  `json:"complexity"`
	RequestCount      int     `json:"requestCount"`
	TotalSystemPrompt int     `json:"totalSystemPrompt"`
	TotalContext      int     `json:"totalContext"`
	TotalToolDefs     int     `json:"totalToolDefs"`
	TotalInput        int     `json:"totalInput"`
	TotalOutput       int     `json:"totalOutput"`
	TotalCost         float64 `json:"totalCost"`
	AvgInput          int     `json:"avgInput"`
	AvgOutput         int     `json:"avgOutput"`
}

// TokenRoleRow is a parsed row from queryTokenUsageByRole.
type TokenRoleRow struct {
	Role         string  `json:"role"`
	Complexity   string  `json:"complexity"`
	RequestCount int     `json:"requestCount"`
	TotalInput   int     `json:"totalInput"`
	TotalOutput  int     `json:"totalOutput"`
	TotalCost    float64 `json:"totalCost"`
}

// parseTokenSummaryRows converts raw DB rows to typed structs.
func parseTokenSummaryRows(rows []map[string]any) []TokenSummaryRow {
	var result []TokenSummaryRow
	for _, row := range rows {
		result = append(result, TokenSummaryRow{
			Complexity:        jsonStr(row["complexity"]),
			RequestCount:      jsonInt(row["request_count"]),
			TotalSystemPrompt: jsonInt(row["total_system_prompt"]),
			TotalContext:      jsonInt(row["total_context"]),
			TotalToolDefs:     jsonInt(row["total_tool_defs"]),
			TotalInput:        jsonInt(row["total_input"]),
			TotalOutput:       jsonInt(row["total_output"]),
			TotalCost:         jsonFloat(row["total_cost"]),
			AvgInput:          jsonInt(row["avg_input"]),
			AvgOutput:         jsonInt(row["avg_output"]),
		})
	}
	return result
}

// parseTokenRoleRows converts raw DB rows to typed structs.
func parseTokenRoleRows(rows []map[string]any) []TokenRoleRow {
	var result []TokenRoleRow
	for _, row := range rows {
		result = append(result, TokenRoleRow{
			Role:         jsonStr(row["role"]),
			Complexity:   jsonStr(row["complexity"]),
			RequestCount: jsonInt(row["request_count"]),
			TotalInput:   jsonInt(row["total_input"]),
			TotalOutput:  jsonInt(row["total_output"]),
			TotalCost:    jsonFloat(row["total_cost"]),
		})
	}
	return result
}

// formatTokenSummary formats token telemetry summary for CLI display.
func formatTokenSummary(rows []TokenSummaryRow) string {
	if len(rows) == 0 {
		return "  (no data)"
	}

	lines := []string{
		fmt.Sprintf("  %-12s %6s %10s %10s %10s %10s",
			"Complexity", "Reqs", "Avg In", "Avg Out", "Total Cost", "Sys Prompt"),
		fmt.Sprintf("  %s", "--------------------------------------------------------------------"),
	}
	for _, r := range rows {
		lines = append(lines, fmt.Sprintf("  %-12s %6d %10d %10d $%9.4f %10d",
			r.Complexity, r.RequestCount, r.AvgInput, r.AvgOutput, r.TotalCost, r.TotalSystemPrompt))
	}
	return strings.Join(lines, "\n")
}

// formatTokenByRole formats token usage by role for CLI display.
func formatTokenByRole(rows []TokenRoleRow) string {
	if len(rows) == 0 {
		return "  (no data)"
	}

	lines := []string{
		fmt.Sprintf("  %-15s %-12s %6s %10s %10s %10s",
			"Role", "Complexity", "Reqs", "Total In", "Total Out", "Cost"),
		fmt.Sprintf("  %s", "-------------------------------------------------------------------"),
	}
	for _, r := range rows {
		lines = append(lines, fmt.Sprintf("  %-15s %-12s %6d %10d %10d $%9.4f",
			truncate(r.Role, 15), r.Complexity, r.RequestCount, r.TotalInput, r.TotalOutput, r.TotalCost))
	}
	return strings.Join(lines, "\n")
}

