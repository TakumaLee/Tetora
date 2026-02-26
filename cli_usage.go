package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
)

// --- P18.1: CLI Usage Command ---

// cmdUsage implements `tetora usage [today|week|month] [--model] [--agent] [--days N]`
// Also supports `tetora usage tokens [--days N]` for token telemetry breakdown.
func cmdUsage(args []string) {
	// Handle `tetora usage tokens` subcommand.
	if len(args) > 0 && args[0] == "tokens" {
		cmdUsageTokens(args[1:])
		return
	}

	period := "today"
	showModel := false
	showRole := false
	days := 30

	// Parse args.
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

	cfg := loadConfig("")
	defaultLogger = initLogger(cfg.Logging, cfg.baseDir)

	// Try daemon API first.
	api := newAPIClient(cfg)
	if tryUsageFromAPI(api, period, showModel, showRole, days) {
		return
	}

	// Fallback: direct DB query.
	usageFromDB(cfg, period, showModel, showRole, days)
}

// tryUsageFromAPI attempts to get usage data from the daemon API.
// Returns true if successful.
func tryUsageFromAPI(api *apiClient, period string, showModel, showRole bool, days int) bool {
	resp, err := api.get("/api/usage/summary?period=" + period)
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
		resp2, err := api.get(fmt.Sprintf("/api/usage/breakdown?by=model&days=%d", days))
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
		resp3, err := api.get(fmt.Sprintf("/api/usage/breakdown?by=role&days=%d", days))
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
func usageFromDB(cfg *Config, period string, showModel, showRole bool, days int) {
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

	// Always show a quick hint if no breakdown flags specified.
	if !showModel && !showRole {
		fmt.Println("Tip: use --model or --agent for detailed breakdown")
	}
}

// cmdUsageTokens implements `tetora usage tokens [--days N]`.
// Shows token telemetry breakdown by complexity and agent.
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

	cfg := loadConfig("")
	defaultLogger = initLogger(cfg.Logging, cfg.baseDir)

	if cfg.HistoryDB == "" {
		fmt.Fprintln(os.Stderr, "Error: historyDB not configured")
		os.Exit(1)
	}

	// Try daemon API first.
	api := newAPIClient(cfg)
	resp, err := api.get(fmt.Sprintf("/api/tokens/summary?days=%d", days))
	if err == nil && resp.StatusCode == 200 {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		var data struct {
			Summary []TokenSummaryRow `json:"summary"`
			ByRole  []TokenAgentRow    `json:"byRole"`
			Days    int               `json:"days"`
		}
		if json.Unmarshal(body, &data) == nil {
			fmt.Printf("Token Telemetry (last %d days):\n\n", data.Days)
			fmt.Println("By Complexity:")
			fmt.Println(formatTokenSummary(data.Summary))
			fmt.Println()
			fmt.Println("By Agent:")
			fmt.Println(formatTokenByRole(data.ByRole))
			return
		}
	}

	// Fallback: direct DB query.
	fmt.Printf("Token Telemetry (last %d days):\n\n", days)

	summaryRows, err := queryTokenUsageSummary(cfg.HistoryDB, days)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error querying token summary: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("By Complexity:")
	fmt.Println(formatTokenSummary(parseTokenSummaryRows(summaryRows)))
	fmt.Println()

	roleRows, err := queryTokenUsageByRole(cfg.HistoryDB, days)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error querying token by agent: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("By Agent:")
	fmt.Println(formatTokenByRole(parseTokenAgentRows(roleRows)))
}
