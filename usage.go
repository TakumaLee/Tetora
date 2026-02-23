package main

import (
	"fmt"
	"strings"
)

// --- P18.1: Cost Dashboard + Usage Tracking ---

// UsageConfig configures the cost footer displayed on channel responses.
type UsageConfig struct {
	ShowFooter     bool   `json:"showFooter,omitempty"`     // append cost footer to channel responses
	FooterTemplate string `json:"footerTemplate,omitempty"` // default: "tokensIn in/tokensOut out ~$cost"
}

// --- Usage Summary Types ---

// UsageSummary is the aggregate cost/token summary for a time period.
type UsageSummary struct {
	Period      string       `json:"period"`              // "today", "week", "month"
	TotalCost   float64      `json:"totalCostUsd"`
	TotalTasks  int          `json:"totalTasks"`
	TokensIn    int          `json:"totalTokensIn"`
	TokensOut   int          `json:"totalTokensOut"`
	BudgetLimit float64      `json:"budgetLimit,omitempty"`
	BudgetPct   float64      `json:"budgetPct,omitempty"`
	ByModel     []ModelUsage `json:"byModel,omitempty"`
	ByRole      []RoleUsage  `json:"byRole,omitempty"`
}

// ModelUsage is cost/token usage breakdown for a single model.
type ModelUsage struct {
	Model     string  `json:"model"`
	Tasks     int     `json:"tasks"`
	Cost      float64 `json:"costUsd"`
	TokensIn  int     `json:"tokensIn"`
	TokensOut int     `json:"tokensOut"`
	Pct       float64 `json:"pct"` // percentage of total cost
}

// RoleUsage is cost/token usage breakdown for a single role.
type RoleUsage struct {
	Role      string  `json:"role"`
	Tasks     int     `json:"tasks"`
	Cost      float64 `json:"costUsd"`
	TokensIn  int     `json:"tokensIn"`
	TokensOut int     `json:"tokensOut"`
	Pct       float64 `json:"pct"`
}

// ExpensiveSession summarizes a high-cost session.
type ExpensiveSession struct {
	SessionID string  `json:"sessionId"`
	Role      string  `json:"role"`
	Title     string  `json:"title"`
	TotalCost float64 `json:"totalCostUsd"`
	Messages  int     `json:"messages"`
	TokensIn  int     `json:"tokensIn"`
	TokensOut int     `json:"tokensOut"`
	CreatedAt string  `json:"createdAt"`
}

// DayUsage is the cost/token usage for a single day (for trend chart).
type DayUsage struct {
	Date      string  `json:"date"`
	Cost      float64 `json:"costUsd"`
	Tasks     int     `json:"tasks"`
	TokensIn  int     `json:"tokensIn"`
	TokensOut int     `json:"tokensOut"`
}

// --- Query Functions ---

// queryUsageSummary aggregates cost/token data from job_runs for the given period.
// period: "today", "week", "month"
func queryUsageSummary(dbPath, period string) (*UsageSummary, error) {
	if dbPath == "" {
		return &UsageSummary{Period: period}, nil
	}

	var dateFilter string
	switch period {
	case "today":
		dateFilter = "date(started_at,'localtime') = date('now','localtime')"
	case "week":
		dateFilter = "date(started_at,'localtime') >= date('now','localtime','-7 days')"
	case "month":
		dateFilter = "date(started_at,'localtime') >= date('now','localtime','-30 days')"
	default:
		dateFilter = "date(started_at,'localtime') = date('now','localtime')"
		period = "today"
	}

	sql := fmt.Sprintf(
		`SELECT
			COALESCE(SUM(cost_usd), 0) as total_cost,
			COUNT(*) as total_tasks,
			COALESCE(SUM(tokens_in), 0) as total_tokens_in,
			COALESCE(SUM(tokens_out), 0) as total_tokens_out
		 FROM job_runs WHERE %s`, dateFilter)

	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("query usage summary: %w", err)
	}

	summary := &UsageSummary{Period: period}
	if len(rows) > 0 {
		summary.TotalCost = jsonFloat(rows[0]["total_cost"])
		summary.TotalTasks = jsonInt(rows[0]["total_tasks"])
		summary.TokensIn = jsonInt(rows[0]["total_tokens_in"])
		summary.TokensOut = jsonInt(rows[0]["total_tokens_out"])
	}

	return summary, nil
}

// queryUsageByModel returns cost/token breakdown grouped by model for the last N days.
func queryUsageByModel(dbPath string, days int) ([]ModelUsage, error) {
	if dbPath == "" {
		return nil, nil
	}
	if days <= 0 {
		days = 30
	}

	sql := fmt.Sprintf(
		`SELECT
			model,
			COUNT(*) as tasks,
			COALESCE(SUM(cost_usd), 0) as cost,
			COALESCE(SUM(tokens_in), 0) as tokens_in,
			COALESCE(SUM(tokens_out), 0) as tokens_out
		 FROM job_runs
		 WHERE date(started_at,'localtime') >= date('now','localtime','-%d days')
		   AND model != ''
		 GROUP BY model
		 ORDER BY cost DESC`, days)

	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("query usage by model: %w", err)
	}

	// Calculate total cost for percentages.
	var totalCost float64
	for _, row := range rows {
		totalCost += jsonFloat(row["cost"])
	}

	var result []ModelUsage
	for _, row := range rows {
		cost := jsonFloat(row["cost"])
		pct := 0.0
		if totalCost > 0 {
			pct = cost / totalCost * 100
		}
		result = append(result, ModelUsage{
			Model:     jsonStr(row["model"]),
			Tasks:     jsonInt(row["tasks"]),
			Cost:      cost,
			TokensIn:  jsonInt(row["tokens_in"]),
			TokensOut: jsonInt(row["tokens_out"]),
			Pct:       pct,
		})
	}

	return result, nil
}

// queryUsageByRole returns cost/token breakdown grouped by role for the last N days.
func queryUsageByRole(dbPath string, days int) ([]RoleUsage, error) {
	if dbPath == "" {
		return nil, nil
	}
	if days <= 0 {
		days = 30
	}

	sql := fmt.Sprintf(
		`SELECT
			CASE WHEN role = '' THEN '(unassigned)' ELSE role END as role,
			COUNT(*) as tasks,
			COALESCE(SUM(cost_usd), 0) as cost,
			COALESCE(SUM(tokens_in), 0) as tokens_in,
			COALESCE(SUM(tokens_out), 0) as tokens_out
		 FROM job_runs
		 WHERE date(started_at,'localtime') >= date('now','localtime','-%d days')
		 GROUP BY role
		 ORDER BY cost DESC`, days)

	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("query usage by role: %w", err)
	}

	// Calculate total cost for percentages.
	var totalCost float64
	for _, row := range rows {
		totalCost += jsonFloat(row["cost"])
	}

	var result []RoleUsage
	for _, row := range rows {
		cost := jsonFloat(row["cost"])
		pct := 0.0
		if totalCost > 0 {
			pct = cost / totalCost * 100
		}
		result = append(result, RoleUsage{
			Role:      jsonStr(row["role"]),
			Tasks:     jsonInt(row["tasks"]),
			Cost:      cost,
			TokensIn:  jsonInt(row["tokens_in"]),
			TokensOut: jsonInt(row["tokens_out"]),
			Pct:       pct,
		})
	}

	return result, nil
}

// queryExpensiveSessions returns the most expensive sessions from the sessions table.
func queryExpensiveSessions(dbPath string, limit, days int) ([]ExpensiveSession, error) {
	if dbPath == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 10
	}
	if days <= 0 {
		days = 30
	}

	sql := fmt.Sprintf(
		`SELECT
			id, role, title, total_cost, message_count,
			total_tokens_in, total_tokens_out, created_at
		 FROM sessions
		 WHERE date(created_at,'localtime') >= date('now','localtime','-%d days')
		   AND total_cost > 0
		 ORDER BY total_cost DESC
		 LIMIT %d`, days, limit)

	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("query expensive sessions: %w", err)
	}

	var result []ExpensiveSession
	for _, row := range rows {
		result = append(result, ExpensiveSession{
			SessionID: jsonStr(row["id"]),
			Role:      jsonStr(row["role"]),
			Title:     jsonStr(row["title"]),
			TotalCost: jsonFloat(row["total_cost"]),
			Messages:  jsonInt(row["message_count"]),
			TokensIn:  jsonInt(row["total_tokens_in"]),
			TokensOut: jsonInt(row["total_tokens_out"]),
			CreatedAt: jsonStr(row["created_at"]),
		})
	}

	return result, nil
}

// queryCostTrend returns daily cost/token aggregation for the last N days.
func queryCostTrend(dbPath string, days int) ([]DayUsage, error) {
	if dbPath == "" {
		return nil, nil
	}
	if days <= 0 {
		days = 30
	}

	sql := fmt.Sprintf(
		`SELECT
			date(started_at,'localtime') as day,
			COALESCE(SUM(cost_usd), 0) as cost,
			COUNT(*) as tasks,
			COALESCE(SUM(tokens_in), 0) as tokens_in,
			COALESCE(SUM(tokens_out), 0) as tokens_out
		 FROM job_runs
		 WHERE date(started_at,'localtime') >= date('now','localtime','-%d days')
		 GROUP BY day
		 ORDER BY day ASC`, days)

	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("query cost trend: %w", err)
	}

	var result []DayUsage
	for _, row := range rows {
		result = append(result, DayUsage{
			Date:      jsonStr(row["day"]),
			Cost:      jsonFloat(row["cost"]),
			Tasks:     jsonInt(row["tasks"]),
			TokensIn:  jsonInt(row["tokens_in"]),
			TokensOut: jsonInt(row["tokens_out"]),
		})
	}

	return result, nil
}

// --- Cost Footer ---

// formatResponseCostFooter returns a cost footer string for channel responses.
// Returns empty string if the footer is disabled in config.
func formatResponseCostFooter(cfg *Config, result *ProviderResult) string {
	if cfg == nil || !cfg.Usage.ShowFooter || result == nil {
		return ""
	}

	tmpl := cfg.Usage.FooterTemplate
	if tmpl == "" {
		tmpl = "{{.tokensIn}}in/{{.tokensOut}}out ~${{.cost}}"
	}

	// Simple template substitution (no text/template dependency).
	footer := tmpl
	footer = strings.ReplaceAll(footer, "{{.tokensIn}}", fmt.Sprintf("%d", result.TokensIn))
	footer = strings.ReplaceAll(footer, "{{.tokensOut}}", fmt.Sprintf("%d", result.TokensOut))
	footer = strings.ReplaceAll(footer, "{{.cost}}", fmt.Sprintf("%.4f", result.CostUSD))

	return footer
}

// formatResultCostFooter returns a cost footer string from a TaskResult.
// This is a convenience wrapper for use with TaskResult instead of ProviderResult.
func formatResultCostFooter(cfg *Config, result *TaskResult) string {
	if cfg == nil || !cfg.Usage.ShowFooter || result == nil {
		return ""
	}
	// Convert TaskResult fields to ProviderResult for footer formatting.
	pr := &ProviderResult{
		TokensIn:  result.TokensIn,
		TokensOut: result.TokensOut,
		CostUSD:   result.CostUSD,
	}
	return formatResponseCostFooter(cfg, pr)
}

// --- Usage Summary Formatting (for CLI) ---

// formatUsageSummary formats a UsageSummary for CLI display.
func formatUsageSummary(summary *UsageSummary) string {
	var lines []string

	lines = append(lines, fmt.Sprintf("Usage (%s):", summary.Period))
	lines = append(lines, fmt.Sprintf("  Cost:      $%.4f", summary.TotalCost))
	lines = append(lines, fmt.Sprintf("  Tasks:     %d", summary.TotalTasks))
	lines = append(lines, fmt.Sprintf("  Tokens In: %d", summary.TokensIn))
	lines = append(lines, fmt.Sprintf("  Tokens Out:%d", summary.TokensOut))

	if summary.BudgetLimit > 0 {
		lines = append(lines, fmt.Sprintf("  Budget:    $%.2f / $%.2f (%.1f%%)",
			summary.TotalCost, summary.BudgetLimit, summary.BudgetPct))
	}

	return strings.Join(lines, "\n")
}

// formatModelBreakdown formats model usage breakdown for CLI display.
func formatModelBreakdown(models []ModelUsage) string {
	if len(models) == 0 {
		return "  (no data)"
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("  %-20s %6s %10s %10s %10s %6s",
		"Model", "Tasks", "Cost", "Tokens In", "Tokens Out", "Pct"))
	lines = append(lines, fmt.Sprintf("  %s", strings.Repeat("-", 68)))

	for _, m := range models {
		lines = append(lines, fmt.Sprintf("  %-20s %6d $%9.4f %10d %10d %5.1f%%",
			truncate(m.Model, 20), m.Tasks, m.Cost, m.TokensIn, m.TokensOut, m.Pct))
	}

	return strings.Join(lines, "\n")
}

// formatRoleBreakdown formats role usage breakdown for CLI display.
func formatRoleBreakdown(roles []RoleUsage) string {
	if len(roles) == 0 {
		return "  (no data)"
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("  %-20s %6s %10s %10s %10s %6s",
		"Role", "Tasks", "Cost", "Tokens In", "Tokens Out", "Pct"))
	lines = append(lines, fmt.Sprintf("  %s", strings.Repeat("-", 68)))

	for _, r := range roles {
		lines = append(lines, fmt.Sprintf("  %-20s %6d $%9.4f %10d %10d %5.1f%%",
			truncate(r.Role, 20), r.Tasks, r.Cost, r.TokensIn, r.TokensOut, r.Pct))
	}

	return strings.Join(lines, "\n")
}

// Note: jsonStr, jsonFloat, jsonInt helpers are defined in history.go.
