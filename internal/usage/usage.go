package usage

import (
	"fmt"
	"strings"

	"tetora/internal/db"
	"tetora/internal/text"
)

// UsageSummary is the aggregate cost/token summary for a time period.
type UsageSummary struct {
	Period      string       `json:"period"`
	TotalCost   float64      `json:"totalCostUsd"`
	TotalTasks  int          `json:"totalTasks"`
	TokensIn    int          `json:"totalTokensIn"`
	TokensOut   int          `json:"totalTokensOut"`
	BudgetLimit float64      `json:"budgetLimit,omitempty"`
	BudgetPct   float64      `json:"budgetPct,omitempty"`
	ByModel     []ModelUsage `json:"byModel,omitempty"`
	ByRole      []AgentUsage `json:"byRole,omitempty"`
}

// ModelUsage is cost/token usage breakdown for a single model.
type ModelUsage struct {
	Model     string  `json:"model"`
	Tasks     int     `json:"tasks"`
	Cost      float64 `json:"costUsd"`
	TokensIn  int     `json:"tokensIn"`
	TokensOut int     `json:"tokensOut"`
	Pct       float64 `json:"pct"`
}

// AgentUsage is cost/token usage breakdown for a single agent.
type AgentUsage struct {
	Agent     string  `json:"agent"`
	Tasks     int     `json:"tasks"`
	Cost      float64 `json:"costUsd"`
	TokensIn  int     `json:"tokensIn"`
	TokensOut int     `json:"tokensOut"`
	Pct       float64 `json:"pct"`
}

// ExpensiveSession summarizes a high-cost session.
type ExpensiveSession struct {
	SessionID string  `json:"sessionId"`
	Agent     string  `json:"agent"`
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

// QuerySummary aggregates cost/token data from job_runs for the given period.
// period: "today", "week", "month"
func QuerySummary(dbPath, period string) (*UsageSummary, error) {
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
	case "prev_today":
		dateFilter = "date(started_at,'localtime') = date('now','localtime','-1 day')"
	case "prev_week":
		dateFilter = "date(started_at,'localtime') >= date('now','localtime','-14 days') AND date(started_at,'localtime') < date('now','localtime','-7 days')"
	case "prev_month":
		dateFilter = "date(started_at,'localtime') >= date('now','localtime','-60 days') AND date(started_at,'localtime') < date('now','localtime','-30 days')"
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

	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("query usage summary: %w", err)
	}

	summary := &UsageSummary{Period: period}
	if len(rows) > 0 {
		summary.TotalCost = db.Float(rows[0]["total_cost"])
		summary.TotalTasks = db.Int(rows[0]["total_tasks"])
		summary.TokensIn = db.Int(rows[0]["total_tokens_in"])
		summary.TokensOut = db.Int(rows[0]["total_tokens_out"])
	}

	return summary, nil
}

// QueryByModel returns cost/token breakdown grouped by model for the last N days.
func QueryByModel(dbPath string, days int) ([]ModelUsage, error) {
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

	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("query usage by model: %w", err)
	}

	var totalCost float64
	for _, row := range rows {
		totalCost += db.Float(row["cost"])
	}

	var result []ModelUsage
	for _, row := range rows {
		cost := db.Float(row["cost"])
		pct := 0.0
		if totalCost > 0 {
			pct = cost / totalCost * 100
		}
		result = append(result, ModelUsage{
			Model:     db.Str(row["model"]),
			Tasks:     db.Int(row["tasks"]),
			Cost:      cost,
			TokensIn:  db.Int(row["tokens_in"]),
			TokensOut: db.Int(row["tokens_out"]),
			Pct:       pct,
		})
	}

	return result, nil
}

// QueryByAgent returns cost/token breakdown grouped by agent for the last N days.
func QueryByAgent(dbPath string, days int) ([]AgentUsage, error) {
	if dbPath == "" {
		return nil, nil
	}
	if days <= 0 {
		days = 30
	}

	sql := fmt.Sprintf(
		`SELECT
			CASE WHEN agent = '' THEN '(unassigned)' ELSE agent END as agent,
			COUNT(*) as tasks,
			COALESCE(SUM(cost_usd), 0) as cost,
			COALESCE(SUM(tokens_in), 0) as tokens_in,
			COALESCE(SUM(tokens_out), 0) as tokens_out
		 FROM job_runs
		 WHERE date(started_at,'localtime') >= date('now','localtime','-%d days')
		 GROUP BY agent
		 ORDER BY cost DESC`, days)

	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("query usage by agent: %w", err)
	}

	var totalCost float64
	for _, row := range rows {
		totalCost += db.Float(row["cost"])
	}

	var result []AgentUsage
	for _, row := range rows {
		cost := db.Float(row["cost"])
		pct := 0.0
		if totalCost > 0 {
			pct = cost / totalCost * 100
		}
		result = append(result, AgentUsage{
			Agent:     db.Str(row["agent"]),
			Tasks:     db.Int(row["tasks"]),
			Cost:      cost,
			TokensIn:  db.Int(row["tokens_in"]),
			TokensOut: db.Int(row["tokens_out"]),
			Pct:       pct,
		})
	}

	return result, nil
}

// QueryExpensiveSessions returns the most expensive sessions from the sessions table.
func QueryExpensiveSessions(dbPath string, limit, days int) ([]ExpensiveSession, error) {
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
			id, agent, title, total_cost, message_count,
			total_tokens_in, total_tokens_out, created_at
		 FROM sessions
		 WHERE date(created_at,'localtime') >= date('now','localtime','-%d days')
		   AND total_cost > 0
		 ORDER BY total_cost DESC
		 LIMIT %d`, days, limit)

	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("query expensive sessions: %w", err)
	}

	var result []ExpensiveSession
	for _, row := range rows {
		result = append(result, ExpensiveSession{
			SessionID: db.Str(row["id"]),
			Agent:     db.Str(row["agent"]),
			Title:     db.Str(row["title"]),
			TotalCost: db.Float(row["total_cost"]),
			Messages:  db.Int(row["message_count"]),
			TokensIn:  db.Int(row["total_tokens_in"]),
			TokensOut: db.Int(row["total_tokens_out"]),
			CreatedAt: db.Str(row["created_at"]),
		})
	}

	return result, nil
}

// QueryCostTrend returns daily cost/token aggregation for the last N days.
func QueryCostTrend(dbPath string, days int) ([]DayUsage, error) {
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

	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("query cost trend: %w", err)
	}

	var result []DayUsage
	for _, row := range rows {
		result = append(result, DayUsage{
			Date:      db.Str(row["day"]),
			Cost:      db.Float(row["cost"]),
			Tasks:     db.Int(row["tasks"]),
			TokensIn:  db.Int(row["tokens_in"]),
			TokensOut: db.Int(row["tokens_out"]),
		})
	}

	return result, nil
}

// FormatSummary formats a UsageSummary for CLI display.
func FormatSummary(summary *UsageSummary) string {
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

// FormatModelBreakdown formats model usage breakdown for CLI display.
func FormatModelBreakdown(models []ModelUsage) string {
	if len(models) == 0 {
		return "  (no data)"
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("  %-20s %6s %10s %10s %10s %6s",
		"Model", "Tasks", "Cost", "Tokens In", "Tokens Out", "Pct"))
	lines = append(lines, fmt.Sprintf("  %s", strings.Repeat("-", 68)))

	for _, m := range models {
		lines = append(lines, fmt.Sprintf("  %-20s %6d $%9.4f %10d %10d %5.1f%%",
			truncateStr(m.Model, 20), m.Tasks, m.Cost, m.TokensIn, m.TokensOut, m.Pct))
	}

	return strings.Join(lines, "\n")
}

// FormatAgentBreakdown formats agent usage breakdown for CLI display.
func FormatAgentBreakdown(roles []AgentUsage) string {
	if len(roles) == 0 {
		return "  (no data)"
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("  %-20s %6s %10s %10s %10s %6s",
		"Agent", "Tasks", "Cost", "Tokens In", "Tokens Out", "Pct"))
	lines = append(lines, fmt.Sprintf("  %s", strings.Repeat("-", 68)))

	for _, r := range roles {
		lines = append(lines, fmt.Sprintf("  %-20s %6d $%9.4f %10d %10d %5.1f%%",
			truncateStr(r.Agent, 20), r.Tasks, r.Cost, r.TokensIn, r.TokensOut, r.Pct))
	}

	return strings.Join(lines, "\n")
}

// truncateStr delegates to text.TruncateStr.
var truncateStr = text.TruncateStr
