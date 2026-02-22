package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

func cmdHistory(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: tetora history <list|show|cost> [options]")
		return
	}
	switch args[0] {
	case "list", "ls":
		historyList(args[1:])
	case "show", "view":
		if len(args) < 2 {
			fmt.Println("Usage: tetora history show <run-id>")
			return
		}
		historyShow(args[1])
	case "cost", "costs":
		historyCost()
	default:
		fmt.Fprintf(os.Stderr, "Unknown action: %s\n", args[0])
	}
}

func historyList(args []string) {
	cfg := loadConfig(findConfigPath())
	if cfg.HistoryDB == "" {
		fmt.Fprintln(os.Stderr, "History DB not configured.")
		os.Exit(1)
	}

	jobID := ""
	status := ""
	from := ""
	limit := 20
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--job", "-j":
			if i+1 < len(args) {
				i++
				jobID = args[i]
			}
		case "--status", "-s":
			if i+1 < len(args) {
				i++
				status = args[i]
			}
		case "--from":
			if i+1 < len(args) {
				i++
				from = args[i]
			}
		case "--limit", "-n":
			if i+1 < len(args) {
				i++
				if n, err := strconv.Atoi(args[i]); err == nil && n > 0 {
					limit = n
				}
			}
		}
	}

	q := HistoryQuery{
		JobID:  jobID,
		Status: status,
		From:   from,
		Limit:  limit,
	}
	runs, total, err := queryHistoryFiltered(cfg.HistoryDB, q)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(runs) == 0 {
		fmt.Println("No history records found.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintf(w, "ID\tNAME\tSOURCE\tSTATUS\tCOST\tMODEL\tTIME\n")
	for _, r := range runs {
		t := formatTime(r.StartedAt)
		cost := fmt.Sprintf("$%.2f", r.CostUSD)
		if r.CostUSD < 0.01 && r.CostUSD > 0 {
			cost = fmt.Sprintf("$%.4f", r.CostUSD)
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n",
			r.ID, r.Name, r.Source, r.Status, cost, r.Model, t)
	}
	w.Flush()
	fmt.Printf("\n%d records (of %d total)\n", len(runs), total)
}

func historyShow(idStr string) {
	cfg := loadConfig(findConfigPath())
	if cfg.HistoryDB == "" {
		fmt.Fprintln(os.Stderr, "History DB not configured.")
		os.Exit(1)
	}

	id, err := strconv.Atoi(idStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid run ID: %s\n", idStr)
		os.Exit(1)
	}

	run, err := queryHistoryByID(cfg.HistoryDB, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if run == nil {
		fmt.Fprintf(os.Stderr, "Run #%d not found.\n", id)
		os.Exit(1)
	}

	fmt.Printf("Run #%d — %s\n", run.ID, run.Name)
	fmt.Printf("  Job ID:    %s\n", run.JobID)
	fmt.Printf("  Source:    %s\n", run.Source)
	fmt.Printf("  Status:    %s (exit %d)\n", run.Status, run.ExitCode)
	fmt.Printf("  Model:     %s\n", run.Model)
	fmt.Printf("  Cost:      $%.4f\n", run.CostUSD)
	fmt.Printf("  Started:   %s\n", run.StartedAt)
	fmt.Printf("  Finished:  %s\n", run.FinishedAt)
	fmt.Printf("  Session:   %s\n", run.SessionID)

	if run.OutputSummary != "" {
		fmt.Printf("\n--- Output ---\n%s\n", run.OutputSummary)
	}
	if run.Error != "" {
		fmt.Printf("\n--- Error ---\n%s\n", run.Error)
	}
}

func historyCost() {
	cfg := loadConfig(findConfigPath())
	if cfg.HistoryDB == "" {
		fmt.Fprintln(os.Stderr, "History DB not configured.")
		os.Exit(1)
	}

	stats, err := queryCostStats(cfg.HistoryDB)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Cost Summary\n")
	fmt.Printf("  Today:      $%.2f\n", stats.Today)
	fmt.Printf("  This Week:  $%.2f\n", stats.Week)
	fmt.Printf("  This Month: $%.2f\n", stats.Month)
}

func formatTime(iso string) string {
	if iso == "" {
		return "-"
	}
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		// Try without timezone
		t, err = time.Parse("2006-01-02T15:04:05", strings.TrimSuffix(iso, "Z"))
		if err != nil {
			return iso
		}
	}
	now := time.Now()
	if t.Format("2006-01-02") == now.Format("2006-01-02") {
		return t.Format("15:04:05")
	}
	return t.Format("Jan 02 15:04")
}
