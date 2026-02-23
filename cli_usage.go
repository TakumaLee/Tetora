package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
)

// --- P18.1: CLI Usage Command ---

// cmdUsage implements `tetora usage [today|week|month] [--model] [--role] [--days N]`
func cmdUsage(args []string) {
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
			fmt.Println("Usage: tetora usage [today|week|month] [--model] [--role] [--days N]")
			fmt.Println()
			fmt.Println("Options:")
			fmt.Println("  today|week|month  Period for summary (default: today)")
			fmt.Println("  --model, -m       Show breakdown by model")
			fmt.Println("  --role, -r        Show breakdown by role")
			fmt.Println("  --days, -d N      Number of days for breakdown (default: 30)")
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
			var roles []RoleUsage
			if json.Unmarshal(body3, &roles) == nil {
				fmt.Println("By Role:")
				fmt.Println(formatRoleBreakdown(roles))
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
		roles, err := queryUsageByRole(cfg.HistoryDB, days)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error querying role breakdown: %v\n", err)
		} else {
			fmt.Println("By Role:")
			fmt.Println(formatRoleBreakdown(roles))
			fmt.Println()
		}
	}

	// Always show a quick hint if no breakdown flags specified.
	if !showModel && !showRole {
		fmt.Println("Tip: use --model or --role for detailed breakdown")
	}
}
