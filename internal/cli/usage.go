package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"tetora/internal/db"
	"tetora/internal/telemetry"
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

// TokenSummaryRow and TokenAgentRow are aliases for the telemetry package types.
type TokenSummaryRow = telemetry.SummaryRow
type TokenAgentRow = telemetry.AgentRow

// CmdUsage implements `tetora usage [today|week|month] [--model] [--agent] [--days N]`
// and `tetora usage tokens [--days N]`.
func CmdUsage(args []string) {
	if len(args) > 0 && args[0] == "tokens" {
		cmdUsageTokens(args[1:])
		return
	}

	period := "today"
	showModel := false
	showRole := false
	days := 30

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "today", "week", "month":
			period = args[i]
		case "--model", "-m":
			showModel = true
		case "--role", "-r":
			showRole = true
		case "--days", "-d":
			if i+1 < len(args) {
				i++
				if n, err := strconv.Atoi(args[i]); err == nil && n > 0 {
					days = n
				}
			}
		case "--help", "-h":
			fmt.Println("Usage: tetora usage [today|week|month] [--model] [--agent] [--days N]")
			fmt.Println("       tetora usage tokens [--days N]")
			fmt.Println()
			fmt.Println("Options:")
			fmt.Println("  today|week|month  Period for summary (default: today)")
			fmt.Println("  --model, -m       Show breakdown by model")
			fmt.Println("  --agent, -r       Show breakdown by agent")
			fmt.Println("  --days, -d N      Number of days for breakdown (default: 30)")
			fmt.Println()
			fmt.Println("Subcommands:")
			fmt.Println("  tokens            Show token telemetry breakdown by complexity and agent")
			return
		}
	}

	cfg := LoadCLIConfig(FindConfigPath())

	// Try daemon API first.
	api := cfg.NewAPIClient()
	if tryUsageFromAPI(api, period, showModel, showRole, days) {
		return
	}

	// Fallback: direct DB query.
	usageFromDB(cfg, period, showModel, showRole, days)
}

// tryUsageFromAPI attempts to get usage data from the daemon API.
// Returns true if successful.
func tryUsageFromAPI(api *APIClient, period string, showModel, showRole bool, days int) bool {
	resp, err := api.Get("/api/usage/summary?period=" + period)
	if err != nil || resp.StatusCode != 200 {
		return false
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var summary UsageSummary
	if json.Unmarshal(body, &summary) != nil {
		return false
	}

	fmt.Println(formatUsageSummary(&summary))
	fmt.Println()

	if showModel {
		resp2, err := api.Get(fmt.Sprintf("/api/usage/breakdown?by=model&days=%d", days))
		if err == nil && resp2.StatusCode == 200 {
			defer resp2.Body.Close()
			body2, _ := io.ReadAll(resp2.Body)
			var models []ModelUsage
			if json.Unmarshal(body2, &models) == nil {
				fmt.Println("By Model:")
				fmt.Println(formatModelBreakdown(models))
				fmt.Println()
			}
		}
	}

	if showRole {
		resp3, err := api.Get(fmt.Sprintf("/api/usage/breakdown?by=role&days=%d", days))
		if err == nil && resp3.StatusCode == 200 {
			defer resp3.Body.Close()
			body3, _ := io.ReadAll(resp3.Body)
			var roles []AgentUsage
			if json.Unmarshal(body3, &roles) == nil {
				fmt.Println("By Agent:")
				fmt.Println(formatAgentBreakdown(roles))
				fmt.Println()
			}
		}
	}

	return true
}

// usageFromDB queries usage data directly from the history DB.
func usageFromDB(cfg *CLIConfig, period string, showModel, showRole bool, days int) {
	if cfg.HistoryDB == "" {
		fmt.Fprintln(os.Stderr, "Error: historyDB not configured")
		os.Exit(1)
	}

	summary, err := queryUsageSummary(cfg.HistoryDB, period)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Overlay budget info.
	switch period {
	case "today":
		if cfg.Budgets.Global.Daily > 0 {
			summary.BudgetLimit = cfg.Budgets.Global.Daily
			if summary.BudgetLimit > 0 {
				summary.BudgetPct = summary.TotalCost / summary.BudgetLimit * 100
			}
		}
	case "week":
		if cfg.Budgets.Global.Weekly > 0 {
			summary.BudgetLimit = cfg.Budgets.Global.Weekly
			if summary.BudgetLimit > 0 {
				summary.BudgetPct = summary.TotalCost / summary.BudgetLimit * 100
			}
		}
	case "month":
		if cfg.Budgets.Global.Monthly > 0 {
			summary.BudgetLimit = cfg.Budgets.Global.Monthly
			if summary.BudgetLimit > 0 {
				summary.BudgetPct = summary.TotalCost / summary.BudgetLimit * 100
			}
		}
	}

	fmt.Println(formatUsageSummary(summary))
	fmt.Println()

	if showModel {
		models, err := queryUsageByModel(cfg.HistoryDB, days)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error querying model breakdown: %v\n", err)
		} else {
			fmt.Println("By Model:")
			fmt.Println(formatModelBreakdown(models))
			fmt.Println()
		}
	}

	if showRole {
		roles, err := queryUsageByAgent(cfg.HistoryDB, days)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error querying agent breakdown: %v\n", err)
		} else {
			fmt.Println("By Agent:")
			fmt.Println(formatAgentBreakdown(roles))
			fmt.Println()
		}
	}

	if !showModel && !showRole {
		fmt.Println("Tip: use --model or --agent for detailed breakdown")
	}
}

// cmdUsageTokens implements `tetora usage tokens [--days N]`.
func cmdUsageTokens(args []string) {
	days := 7

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--days", "-d":
			if i+1 < len(args) {
				i++
				if n, err := strconv.Atoi(args[i]); err == nil && n > 0 {
					days = n
				}
			}
		case "--help", "-h":
			fmt.Println("Usage: tetora usage tokens [--days N]")
			fmt.Println()
			fmt.Println("Options:")
			fmt.Println("  --days, -d N  Number of days to include (default: 7)")
			return
		}
	}

	cfg := LoadCLIConfig(FindConfigPath())

	if cfg.HistoryDB == "" {
		fmt.Fprintln(os.Stderr, "Error: historyDB not configured")
		os.Exit(1)
	}

	// Try daemon API first.
	api := cfg.NewAPIClient()
	resp, err := api.Get(fmt.Sprintf("/api/tokens/summary?days=%d", days))
	if err == nil && resp.StatusCode == 200 {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		var data struct {
			Summary []TokenSummaryRow `json:"summary"`
			ByRole  []TokenAgentRow   `json:"byRole"`
			Days    int               `json:"days"`
		}
		if json.Unmarshal(body, &data) == nil {
			fmt.Printf("Token Telemetry (last %d days):\n\n", data.Days)
			fmt.Println("By Complexity:")
			fmt.Println(telemetry.FormatSummary(data.Summary))
			fmt.Println()
			fmt.Println("By Agent:")
			fmt.Println(telemetry.FormatByRole(data.ByRole))
			return
		}
	}

	// Fallback: direct DB query.
	fmt.Printf("Token Telemetry (last %d days):\n\n", days)

	summaryRows, err := telemetry.QueryUsageSummary(cfg.HistoryDB, days)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error querying token summary: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("By Complexity:")
	fmt.Println(telemetry.FormatSummary(telemetry.ParseSummaryRows(summaryRows)))
	fmt.Println()

	roleRows, err := telemetry.QueryUsageByRole(cfg.HistoryDB, days)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error querying token by agent: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("By Agent:")
	fmt.Println(telemetry.FormatByRole(telemetry.ParseAgentRows(roleRows)))
}

// --- DB query helpers (replicated from root usage.go) ---

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

func queryUsageByAgent(dbPath string, days int) ([]AgentUsage, error) {
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

// --- Formatting helpers ---

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
			truncateStr(m.Model, 20), m.Tasks, m.Cost, m.TokensIn, m.TokensOut, m.Pct))
	}
	return strings.Join(lines, "\n")
}

func formatAgentBreakdown(roles []AgentUsage) string {
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

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
