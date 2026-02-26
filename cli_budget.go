package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

func cmdBudget(args []string) {
	if len(args) == 0 {
		args = []string{"show"}
	}

	switch args[0] {
	case "show":
		cmdBudgetShow()
	case "pause":
		cmdBudgetPause()
	case "resume":
		cmdBudgetResume()
	default:
		fmt.Fprintf(os.Stderr, "Usage: tetora budget <show|pause|resume>\n")
		os.Exit(1)
	}
}

func cmdBudgetShow() {
	cfg := loadConfig("")
	defaultLogger = initLogger(cfg.Logging, cfg.baseDir)

	// Try daemon API first.
	api := newAPIClient(cfg)
	resp, err := api.get("/budget")
	if err == nil && resp.StatusCode == 200 {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		var status BudgetStatus
		if json.Unmarshal(body, &status) == nil {
			printBudgetStatus(&status)
			return
		}
	}

	// Fallback: query DB directly.
	status := queryBudgetStatus(cfg)
	printBudgetStatus(status)
}

func printBudgetStatus(status *BudgetStatus) {
	if status.Paused {
		fmt.Println("Status: PAUSED (all paid execution suspended)")
		fmt.Println()
	}

	if status.Global != nil {
		g := status.Global
		fmt.Println("Global Budget:")
		printMeterLine("  Daily ", g.DailySpend, g.DailyLimit, g.DailyPct)
		printMeterLine("  Weekly", g.WeeklySpend, g.WeeklyLimit, g.WeeklyPct)
		printMeterLine("  Month ", g.MonthlySpend, g.MonthlyLimit, g.MonthlyPct)
		fmt.Println()
	}

	if len(status.Agents) > 0 {
		fmt.Println("Per-Agent Budget:")
		for _, r := range status.Agents {
			printMeterLine(fmt.Sprintf("  %-6s", r.Agent), r.DailySpend, r.DailyLimit, r.DailyPct)
		}
		fmt.Println()
	}
}

func printMeterLine(label string, spend, limit, pct float64) {
	if limit > 0 {
		bar := renderBar(pct)
		fmt.Printf("%s  $%7.2f / $%7.2f  %s %5.1f%%\n", label, spend, limit, bar, pct)
	} else {
		fmt.Printf("%s  $%7.2f\n", label, spend)
	}
}

func renderBar(pct float64) string {
	width := 20
	filled := int(pct / 100 * float64(width))
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}
	bar := strings.Repeat("#", filled) + strings.Repeat("-", width-filled)
	return "[" + bar + "]"
}

func cmdBudgetPause() {
	cfg := loadConfig("")
	defaultLogger = initLogger(cfg.Logging, cfg.baseDir)

	// Try daemon API first.
	api := newAPIClient(cfg)
	resp, err := api.post("/budget/pause", "")
	if err == nil && resp.StatusCode == 200 {
		resp.Body.Close()
		fmt.Println("Budget paused: all paid execution suspended.")
		return
	}

	// Fallback: update config directly.
	configPath := findConfigPath()
	if err := setBudgetPaused(configPath, true); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Budget paused: all paid execution suspended.")
	fmt.Println("Note: restart the daemon for changes to take effect.")
}

func cmdBudgetResume() {
	cfg := loadConfig("")
	defaultLogger = initLogger(cfg.Logging, cfg.baseDir)

	// Try daemon API first.
	api := newAPIClient(cfg)
	resp, err := api.post("/budget/resume", "")
	if err == nil && resp.StatusCode == 200 {
		resp.Body.Close()
		fmt.Println("Budget resumed: paid execution re-enabled.")
		return
	}

	// Fallback: update config directly.
	configPath := findConfigPath()
	if err := setBudgetPaused(configPath, false); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Budget resumed: paid execution re-enabled.")
	fmt.Println("Note: restart the daemon for changes to take effect.")
}
