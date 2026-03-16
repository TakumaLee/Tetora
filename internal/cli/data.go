package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"tetora/internal/db"
)

// CmdData implements `tetora data`.
func CmdData(args []string) {
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

// dataRetentionConfig holds parsed retention settings from CLIConfig.Retention.
type dataRetentionConfig struct {
	History     int      `json:"history"`
	Sessions    int      `json:"sessions"`
	AuditLog    int      `json:"auditLog"`
	Logs        int      `json:"logs"`
	Workflows   int      `json:"workflows"`
	Reflections int      `json:"reflections"`
	SLA         int      `json:"sla"`
	TrustEvents int      `json:"trustEvents"`
	Handoffs    int      `json:"handoffs"`
	Queue       int      `json:"queue"`
	Versions    int      `json:"versions"`
	Outputs     int      `json:"outputs"`
	Uploads     int      `json:"uploads"`
	PIIPatterns []string `json:"piiPatterns"`
}

func parseDataRetention(cfg *CLIConfig) dataRetentionConfig {
	var r dataRetentionConfig
	if cfg.Retention != nil {
		json.Unmarshal(cfg.Retention, &r) //nolint:errcheck
	}
	return r
}

func dataRetentionDays(configured, fallback int) int {
	if configured > 0 {
		return configured
	}
	return fallback
}

func cmdDataStatus() {
	cfg := LoadCLIConfig(FindConfigPath())
	dbPath := cfg.HistoryDB
	if dbPath == "" {
		fmt.Println("No database configured.")
		return
	}

	r := parseDataRetention(cfg)
	fmt.Println("Retention Policy:")
	fmt.Printf("  history:      %d days\n", dataRetentionDays(r.History, 90))
	fmt.Printf("  sessions:     %d days\n", dataRetentionDays(r.Sessions, 30))
	fmt.Printf("  auditLog:     %d days\n", dataRetentionDays(r.AuditLog, 365))
	fmt.Printf("  logs:         %d days\n", dataRetentionDays(r.Logs, 14))
	fmt.Printf("  workflows:    %d days\n", dataRetentionDays(r.Workflows, 90))
	fmt.Printf("  reflections:  %d days\n", dataRetentionDays(r.Reflections, 60))
	fmt.Printf("  sla:          %d days\n", dataRetentionDays(r.SLA, 90))
	fmt.Printf("  trustEvents:  %d days\n", dataRetentionDays(r.TrustEvents, 90))
	fmt.Printf("  handoffs:     %d days\n", dataRetentionDays(r.Handoffs, 60))
	fmt.Printf("  queue:        %d days\n", dataRetentionDays(r.Queue, 7))
	fmt.Printf("  versions:     %d days\n", dataRetentionDays(r.Versions, 180))
	fmt.Printf("  outputs:      %d days\n", dataRetentionDays(r.Outputs, 30))
	fmt.Printf("  uploads:      %d days\n", dataRetentionDays(r.Uploads, 7))

	if len(r.PIIPatterns) > 0 {
		fmt.Printf("  piiPatterns:  %d patterns\n", len(r.PIIPatterns))
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
	fs.Parse(args) //nolint:errcheck

	if *dryRun {
		fmt.Println("Dry-run: showing current retention policy and counts")
		fmt.Println()
		cmdDataStatus()
		fmt.Println()
		fmt.Println("Run without --dry-run to execute cleanup.")
		return
	}

	cfg := LoadCLIConfig(FindConfigPath())
	fmt.Println("Running retention cleanup...")
	results := runRetentionCLI(cfg)
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
	fs.Parse(args) //nolint:errcheck

	if *format != "json" {
		fmt.Fprintf(os.Stderr, "Unsupported format: %s (only json is supported)\n", *format)
		os.Exit(1)
	}

	cfg := LoadCLIConfig(FindConfigPath())
	data, err := exportDataCLI(cfg)
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
	fs.Parse(args) //nolint:errcheck

	if *before == "" {
		fmt.Fprintf(os.Stderr, "Error: --before is required (e.g., --before 2025-01-01)\n")
		os.Exit(1)
	}

	if _, err := time.Parse("2006-01-02", *before); err != nil {
		fmt.Fprintf(os.Stderr, "Invalid date format: %s (expected YYYY-MM-DD)\n", *before)
		os.Exit(1)
	}

	if !*confirm {
		fmt.Fprintf(os.Stderr, "WARNING: This will permanently delete all data before %s.\n", *before)
		fmt.Fprintf(os.Stderr, "Add --confirm to proceed.\n")
		os.Exit(1)
	}

	cfg := LoadCLIConfig(FindConfigPath())
	results, err := purgeDataBeforeCLI(cfg.HistoryDB, *before)
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
}

// --- DB helpers ---

// dataRetentionResult holds the result of a retention cleanup for a single table.
type dataRetentionResult struct {
	Table   string `json:"table"`
	Deleted int    `json:"deleted"`
	Error   string `json:"error,omitempty"`
}

// queryRetentionStats returns row counts for the main tables.
func queryRetentionStats(dbPath string) map[string]int {
	tables := []string{
		"job_runs", "audit_log", "sessions", "session_messages",
		"workflow_runs", "handoffs", "agent_messages",
		"reflections", "sla_checks", "trust_events",
		"config_versions", "agent_memory", "offline_queue",
	}
	stats := make(map[string]int, len(tables))
	for _, table := range tables {
		sql := fmt.Sprintf("SELECT COUNT(*) as cnt FROM %s", table)
		rows, err := db.Query(dbPath, sql)
		if err != nil || len(rows) == 0 {
			stats[table] = 0
			continue
		}
		stats[table] = db.Int(rows[0]["cnt"])
	}
	return stats
}

// runRetentionCLI runs cleanup against each major table using the configured retention days.
// TODO: requires root function runRetention — this is a best-effort reimplementation.
func runRetentionCLI(cfg *CLIConfig) []dataRetentionResult {
	r := parseDataRetention(cfg)
	dbPath := cfg.HistoryDB
	var results []dataRetentionResult

	type tableSpec struct {
		table    string
		col      string
		days     int
		fallback int
	}
	specs := []tableSpec{
		{"job_runs", "created_at", r.History, 90},
		{"audit_log", "created_at", r.AuditLog, 365},
		{"sessions", "created_at", r.Sessions, 30},
		{"session_messages", "created_at", r.Sessions, 30},
		{"workflow_runs", "started_at", r.Workflows, 90},
		{"reflections", "created_at", r.Reflections, 60},
		{"sla_checks", "checked_at", r.SLA, 90},
		{"trust_events", "created_at", r.TrustEvents, 90},
		{"handoffs", "created_at", r.Handoffs, 60},
		{"agent_messages", "created_at", r.Handoffs, 60},
		{"offline_queue", "created_at", r.Queue, 7},
		{"config_versions", "created_at", r.Versions, 180},
	}

	for _, spec := range specs {
		days := dataRetentionDays(spec.days, spec.fallback)
		sql := fmt.Sprintf(
			"DELETE FROM %s WHERE datetime(%s) < datetime('now','-%d days')",
			spec.table, spec.col, days)
		err := db.Exec(dbPath, sql)
		res := dataRetentionResult{Table: spec.table}
		if err != nil {
			res.Error = err.Error()
		} else {
			res.Deleted = -1 // cleaned
		}
		results = append(results, res)
	}
	return results
}

// exportDataCLI exports all user data as JSON.
// TODO: requires root function exportData — this is a best-effort reimplementation.
func exportDataCLI(cfg *CLIConfig) ([]byte, error) {
	dbPath := cfg.HistoryDB
	if dbPath == "" {
		return nil, fmt.Errorf("no database configured")
	}

	export := map[string]any{
		"exportedAt": time.Now().Format(time.RFC3339),
		"version":    TetoraVersion,
	}

	tables := []string{
		"sessions", "session_messages", "job_runs", "audit_log",
		"workflow_runs", "reflections",
	}
	for _, table := range tables {
		rows, err := db.Query(dbPath, "SELECT * FROM "+table)
		if err != nil {
			// Table may not exist; skip.
			continue
		}
		export[table] = rows
	}

	return json.MarshalIndent(export, "", "  ")
}

// purgeDataBeforeCLI deletes all data older than the given date (YYYY-MM-DD).
// TODO: requires root function purgeDataBefore — this is a best-effort reimplementation.
func purgeDataBeforeCLI(dbPath, before string) ([]dataRetentionResult, error) {
	if dbPath == "" {
		return nil, fmt.Errorf("no database configured")
	}

	type tableSpec struct {
		table string
		col   string
	}
	specs := []tableSpec{
		{"job_runs", "created_at"},
		{"audit_log", "created_at"},
		{"sessions", "created_at"},
		{"session_messages", "created_at"},
		{"workflow_runs", "started_at"},
		{"reflections", "created_at"},
		{"sla_checks", "checked_at"},
		{"trust_events", "created_at"},
		{"handoffs", "created_at"},
		{"agent_messages", "created_at"},
		{"offline_queue", "created_at"},
		{"config_versions", "created_at"},
	}

	var results []dataRetentionResult
	for _, spec := range specs {
		// Count before delete.
		countSQL := fmt.Sprintf(
			"SELECT COUNT(*) as cnt FROM %s WHERE date(%s) < date('%s')",
			spec.table, spec.col, before)
		countRows, err := db.Query(dbPath, countSQL)
		count := 0
		if err == nil && len(countRows) > 0 {
			count = db.Int(countRows[0]["cnt"])
		}

		sql := fmt.Sprintf(
			"DELETE FROM %s WHERE date(%s) < date('%s')",
			spec.table, spec.col, before)
		err = db.Exec(dbPath, sql)
		res := dataRetentionResult{Table: spec.table, Deleted: count}
		if err != nil {
			res.Error = err.Error()
		}
		results = append(results, res)
	}
	return results, nil
}
