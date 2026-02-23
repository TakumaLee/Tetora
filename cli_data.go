package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"
)

func cmdData(args []string) {
	if len(args) == 0 {
		printDataUsage()
		return
	}

	switch args[0] {
	case "status":
		cmdDataStatus()
	case "cleanup":
		cmdDataCleanup(args[1:])
	case "export":
		cmdDataExport(args[1:])
	case "purge":
		cmdDataPurge(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown data subcommand: %s\n", args[0])
		printDataUsage()
		os.Exit(1)
	}
}

func printDataUsage() {
	fmt.Fprintf(os.Stderr, `tetora data — Data retention & privacy management

Usage:
  tetora data <command> [options]

Commands:
  status             Show retention config and database row counts
  cleanup [--dry-run] Run retention cleanup (delete expired data)
  export [--output F] Export all user data as JSON (GDPR)
  purge --before DATE Permanently delete all data before date

Examples:
  tetora data status
  tetora data cleanup --dry-run
  tetora data export --output my-data.json
  tetora data purge --before 2025-01-01 --confirm
`)
}

func cmdDataStatus() {
	cfg := loadConfig("")
	dbPath := cfg.HistoryDB
	if dbPath == "" {
		fmt.Println("No database configured.")
		return
	}

	fmt.Println("Retention Policy:")
	fmt.Printf("  history:      %d days\n", retentionDays(cfg.Retention.History, 90))
	fmt.Printf("  sessions:     %d days\n", retentionDays(cfg.Retention.Sessions, 30))
	fmt.Printf("  auditLog:     %d days\n", retentionDays(cfg.Retention.AuditLog, 365))
	fmt.Printf("  logs:         %d days\n", retentionDays(cfg.Retention.Logs, 14))
	fmt.Printf("  workflows:    %d days\n", retentionDays(cfg.Retention.Workflows, 90))
	fmt.Printf("  reflections:  %d days\n", retentionDays(cfg.Retention.Reflections, 60))
	fmt.Printf("  sla:          %d days\n", retentionDays(cfg.Retention.SLA, 90))
	fmt.Printf("  trustEvents:  %d days\n", retentionDays(cfg.Retention.TrustEvents, 90))
	fmt.Printf("  handoffs:     %d days\n", retentionDays(cfg.Retention.Handoffs, 60))
	fmt.Printf("  queue:        %d days\n", retentionDays(cfg.Retention.Queue, 7))
	fmt.Printf("  versions:     %d days\n", retentionDays(cfg.Retention.Versions, 180))
	fmt.Printf("  outputs:      %d days\n", retentionDays(cfg.Retention.Outputs, 30))
	fmt.Printf("  uploads:      %d days\n", retentionDays(cfg.Retention.Uploads, 7))

	if len(cfg.Retention.PIIPatterns) > 0 {
		fmt.Printf("  piiPatterns:  %d patterns\n", len(cfg.Retention.PIIPatterns))
	}

	fmt.Println()
	fmt.Println("Database Row Counts:")
	stats := queryRetentionStats(dbPath)
	total := 0
	for _, table := range []string{
		"job_runs", "audit_log", "sessions", "session_messages",
		"workflow_runs", "handoffs", "agent_messages",
		"reflections", "sla_checks", "trust_events",
		"config_versions", "agent_memory", "offline_queue",
	} {
		count := stats[table]
		total += count
		fmt.Printf("  %-20s %d\n", table, count)
	}
	fmt.Printf("  %-20s %d\n", "TOTAL", total)
}

func cmdDataCleanup(args []string) {
	fs := flag.NewFlagSet("data cleanup", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "show what would be deleted without deleting")
	fs.Parse(args)

	cfg := loadConfig("")

	if *dryRun {
		fmt.Println("Dry-run: showing current retention policy and counts")
		fmt.Println()
		cmdDataStatus()
		fmt.Println()
		fmt.Println("Run without --dry-run to execute cleanup.")
		return
	}

	fmt.Println("Running retention cleanup...")
	results := runRetention(cfg)
	for _, r := range results {
		if r.Error != "" {
			fmt.Printf("  %-20s ERROR: %s\n", r.Table, r.Error)
		} else if r.Deleted < 0 {
			fmt.Printf("  %-20s cleaned\n", r.Table)
		} else {
			fmt.Printf("  %-20s %d deleted\n", r.Table, r.Deleted)
		}
	}
	fmt.Println("Done.")
}

func cmdDataExport(args []string) {
	fs := flag.NewFlagSet("data export", flag.ExitOnError)
	output := fs.String("output", "", "output file path (default: stdout)")
	format := fs.String("format", "json", "export format (json)")
	fs.Parse(args)

	if *format != "json" {
		fmt.Fprintf(os.Stderr, "Unsupported format: %s (only json is supported)\n", *format)
		os.Exit(1)
	}

	cfg := loadConfig("")
	data, err := exportData(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Export failed: %v\n", err)
		os.Exit(1)
	}

	if *output != "" {
		if err := os.WriteFile(*output, data, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "Write file failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Exported to %s (%d bytes)\n", *output, len(data))
	} else {
		os.Stdout.Write(data)
		os.Stdout.Write([]byte("\n"))
	}
}

func cmdDataPurge(args []string) {
	fs := flag.NewFlagSet("data purge", flag.ExitOnError)
	before := fs.String("before", "", "delete all data before this date (YYYY-MM-DD)")
	confirm := fs.Bool("confirm", false, "confirm destructive operation")
	fs.Parse(args)

	if *before == "" {
		fmt.Fprintf(os.Stderr, "Error: --before is required (e.g., --before 2025-01-01)\n")
		os.Exit(1)
	}

	// Validate date format.
	if _, err := time.Parse("2006-01-02", *before); err != nil {
		fmt.Fprintf(os.Stderr, "Invalid date format: %s (expected YYYY-MM-DD)\n", *before)
		os.Exit(1)
	}

	if !*confirm {
		fmt.Fprintf(os.Stderr, "WARNING: This will permanently delete all data before %s.\n", *before)
		fmt.Fprintf(os.Stderr, "Add --confirm to proceed.\n")
		os.Exit(1)
	}

	cfg := loadConfig("")
	results, err := purgeDataBefore(cfg, *before)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Purge failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Purged all data before %s:\n", *before)
	totalDeleted := 0
	for _, r := range results {
		if r.Error != "" {
			fmt.Printf("  %-20s ERROR: %s\n", r.Table, r.Error)
		} else {
			fmt.Printf("  %-20s %d deleted\n", r.Table, r.Deleted)
			totalDeleted += r.Deleted
		}
	}
	fmt.Printf("Total: %d records deleted\n", totalDeleted)

	// Also output as JSON for programmatic use.
	if j, err := json.Marshal(results); err == nil {
		_ = j // available for HTTP API
	}
}
