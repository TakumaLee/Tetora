package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"tetora/internal/cost"
)

func CmdBudget(args []string) {
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
	cfg := LoadCLIConfig(FindConfigPath())

	// Try daemon API first.
	api := cfg.NewAPIClient()
	resp, err := api.Get("/budget")
	if err == nil && resp.StatusCode == 200 {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		var status cost.BudgetStatus
		if json.Unmarshal(body, &status) == nil {
			printBudgetStatus(&status)
			return
		}
	}

	// Fallback: query DB directly.
	budgets := cost.BudgetConfig{
		Global: cost.GlobalBudget{
			Daily:   cfg.Budgets.Global.Daily,
			Weekly:  cfg.Budgets.Global.Weekly,
			Monthly: cfg.Budgets.Global.Monthly,
		},
		Paused: cfg.Budgets.Paused,
	}
	if cfg.Budgets.Agents != nil {
		budgets.Agents = make(map[string]cost.AgentBudget, len(cfg.Budgets.Agents))
		for k, v := range cfg.Budgets.Agents {
			budgets.Agents[k] = cost.AgentBudget{Daily: v.Daily}
		}
	}
	status := cost.QueryBudgetStatus(budgets, cfg.HistoryDB)
	printBudgetStatus(status)
}

func printBudgetStatus(status *cost.BudgetStatus) {
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
	cfg := LoadCLIConfig(FindConfigPath())

	// Try daemon API first.
	api := cfg.NewAPIClient()
	resp, err := api.Post("/budget/pause", "")
	if err == nil && resp.StatusCode == 200 {
		resp.Body.Close()
		fmt.Println("Budget paused: all paid execution suspended.")
		return
	}

	// Fallback: update config directly.
	configPath := FindConfigPath()
	if err := setBudgetPaused(configPath, true); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Budget paused: all paid execution suspended.")
	fmt.Println("Note: restart the daemon for changes to take effect.")
}

func cmdBudgetResume() {
	cfg := LoadCLIConfig(FindConfigPath())

	// Try daemon API first.
	api := cfg.NewAPIClient()
	resp, err := api.Post("/budget/resume", "")
	if err == nil && resp.StatusCode == 200 {
		resp.Body.Close()
		fmt.Println("Budget resumed: paid execution re-enabled.")
		return
	}

	// Fallback: update config directly.
	configPath := FindConfigPath()
	if err := setBudgetPaused(configPath, false); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Budget resumed: paid execution re-enabled.")
	fmt.Println("Note: restart the daemon for changes to take effect.")
}

// setBudgetPaused updates the budgets.paused field in config.json using raw
// JSON manipulation to preserve all other config fields.
func setBudgetPaused(configPath string, paused bool) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	// Parse existing budgets object.
	var budgets map[string]json.RawMessage
	if budgetsRaw, ok := raw["budgets"]; ok {
		json.Unmarshal(budgetsRaw, &budgets) //nolint:errcheck
	}
	if budgets == nil {
		budgets = make(map[string]json.RawMessage)
	}

	pausedJSON, _ := json.Marshal(paused)
	budgets["paused"] = pausedJSON

	budgetsJSON, err := json.Marshal(budgets)
	if err != nil {
		return err
	}
	raw["budgets"] = budgetsJSON

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, append(out, '\n'), 0o600)
}
