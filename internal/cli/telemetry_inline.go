// telemetry_inline.go — inlined from archived internal/telemetry package.
// Provides token usage telemetry types, DB queries, and CLI formatters.
package cli

import (
	"fmt"
	"strings"
	"time"

	"tetora/internal/db"
)

// SummaryRow is a parsed row from telemetry QueryUsageSummary.
type SummaryRow struct {
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

// AgentRow is a parsed row from telemetry QueryUsageByRole.
type AgentRow struct {
	Agent        string  `json:"agent"`
	Complexity   string  `json:"complexity"`
	RequestCount int     `json:"requestCount"`
	TotalInput   int     `json:"totalInput"`
	TotalOutput  int     `json:"totalOutput"`
	TotalCost    float64 `json:"totalCost"`
}

// telemetryQueryUsageSummary returns a summary of token usage grouped by complexity.
func telemetryQueryUsageSummary(dbPath string, days int) ([]map[string]any, error) {
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
	return db.Query(dbPath, sql)
}

// telemetryQueryUsageByRole returns token usage grouped by agent and complexity.
func telemetryQueryUsageByRole(dbPath string, days int) ([]map[string]any, error) {
	if dbPath == "" {
		return nil, nil
	}
	if days <= 0 {
		days = 7
	}
	since := time.Now().AddDate(0, 0, -days).Format("2006-01-02")
	sql := fmt.Sprintf(
		`SELECT
			CASE WHEN agent = '' THEN '(unassigned)' ELSE agent END as agent,
			complexity,
			COUNT(*) as request_count,
			COALESCE(SUM(input_tokens), 0) as total_input,
			COALESCE(SUM(output_tokens), 0) as total_output,
			COALESCE(SUM(cost_usd), 0) as total_cost
		 FROM token_telemetry
		 WHERE date(created_at) >= '%s'
		 GROUP BY agent, complexity
		 ORDER BY total_cost DESC;`, since)
	return db.Query(dbPath, sql)
}

// telemetryParseSummaryRows converts raw DB rows to typed structs.
func telemetryParseSummaryRows(rows []map[string]any) []SummaryRow {
	var result []SummaryRow
	for _, row := range rows {
		result = append(result, SummaryRow{
			Complexity:        db.Str(row["complexity"]),
			RequestCount:      db.Int(row["request_count"]),
			TotalSystemPrompt: db.Int(row["total_system_prompt"]),
			TotalContext:      db.Int(row["total_context"]),
			TotalToolDefs:     db.Int(row["total_tool_defs"]),
			TotalInput:        db.Int(row["total_input"]),
			TotalOutput:       db.Int(row["total_output"]),
			TotalCost:         db.Float(row["total_cost"]),
			AvgInput:          db.Int(row["avg_input"]),
			AvgOutput:         db.Int(row["avg_output"]),
		})
	}
	return result
}

// telemetryParseAgentRows converts raw DB rows to typed structs.
func telemetryParseAgentRows(rows []map[string]any) []AgentRow {
	var result []AgentRow
	for _, row := range rows {
		result = append(result, AgentRow{
			Agent:        db.Str(row["agent"]),
			Complexity:   db.Str(row["complexity"]),
			RequestCount: db.Int(row["request_count"]),
			TotalInput:   db.Int(row["total_input"]),
			TotalOutput:  db.Int(row["total_output"]),
			TotalCost:    db.Float(row["total_cost"]),
		})
	}
	return result
}

// telemetryFormatSummary formats token telemetry summary for CLI display.
func telemetryFormatSummary(rows []SummaryRow) string {
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

// telemetryFormatByRole formats token usage by agent for CLI display.
func telemetryFormatByRole(rows []AgentRow) string {
	if len(rows) == 0 {
		return "  (no data)"
	}

	lines := []string{
		fmt.Sprintf("  %-15s %-12s %6s %10s %10s %10s",
			"Agent", "Complexity", "Reqs", "Total In", "Total Out", "Cost"),
		fmt.Sprintf("  %s", "-------------------------------------------------------------------"),
	}
	for _, r := range rows {
		lines = append(lines, fmt.Sprintf("  %-15s %-12s %6d %10d %10d $%9.4f",
			db.Truncate(r.Agent, 15), r.Complexity, r.RequestCount, r.TotalInput, r.TotalOutput, r.TotalCost))
	}
	return strings.Join(lines, "\n")
}
