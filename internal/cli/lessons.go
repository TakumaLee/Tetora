package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"

	"tetora/internal/reflection"
)

// CmdLessons implements `tetora lessons` — inspect, promote, audit auto-lesson
// data surfaced by the reflection pipeline.
func CmdLessons(args []string) {
	if len(args) == 0 {
		printLessonsUsage()
		return
	}
	switch args[0] {
	case "scan":
		cmdLessonsScan(args[1:])
	case "promote":
		cmdLessonsPromote(args[1:])
	case "history":
		cmdLessonsHistory(args[1:])
	case "audit":
		cmdLessonsAudit(args[1:])
	case "-h", "--help", "help":
		printLessonsUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown lessons subcommand: %s\n\n", args[0])
		printLessonsUsage()
		os.Exit(1)
	}
}

func printLessonsUsage() {
	fmt.Fprintf(os.Stderr, `tetora lessons — Auto-lesson promotion & audit

Usage:
  tetora lessons <command> [options]

Commands:
  scan     [--threshold N] [--json]              List promotion candidates from lesson_events
  promote  [--threshold N] [--apply] [--json]    Materialise a promotion report (dry-run by default)
  history  <key-prefix> [--limit N] [--json]     Show events for a lesson_key prefix
  audit    [--stale-days N] [--json]             Flag stale rules/*.md files

Options:
  --threshold N   Minimum distinct task_ids per lesson_key (default 3)
  --apply         Write rules/auto-promoted-YYYYMMDD.md and flip [pending] → [promoted-DATE]
  --stale-days N  Mark rules files older than N days as stale (default 90)
  --json          Emit machine-readable JSON instead of human output
`)
}

func cmdLessonsScan(args []string) {
	fs := flag.NewFlagSet("lessons scan", flag.ExitOnError)
	threshold := fs.Int("threshold", 3, "minimum distinct task_ids per lesson_key")
	asJSON := fs.Bool("json", false, "emit JSON output")
	_ = fs.Parse(args)

	cfg := LoadCLIConfig(FindConfigPath())
	if cfg.HistoryDB == "" {
		fmt.Fprintln(os.Stderr, "error: no history DB configured")
		os.Exit(1)
	}

	candidates, err := reflection.ScanPromotionCandidates(cfg.HistoryDB, *threshold)
	if err != nil {
		fmt.Fprintf(os.Stderr, "scan failed: %v\n", err)
		os.Exit(1)
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(candidates)
		return
	}

	if len(candidates) == 0 {
		fmt.Printf("No lesson_keys reached threshold %d.\n", *threshold)
		return
	}
	fmt.Printf("Promotion candidates (threshold=%d):\n", *threshold)
	for _, c := range candidates {
		fmt.Printf("  [%d×] %s\n", c.Occurrences, c.LessonKey)
		if len(c.Agents) > 0 {
			fmt.Printf("        agents: %v\n", c.Agents)
		}
		if len(c.TaskIDs) > 0 {
			fmt.Printf("        tasks:  %v\n", c.TaskIDs)
		}
	}
}

func cmdLessonsPromote(args []string) {
	fs := flag.NewFlagSet("lessons promote", flag.ExitOnError)
	threshold := fs.Int("threshold", 3, "minimum distinct task_ids per lesson_key")
	apply := fs.Bool("apply", false, "write report file and flip auto-lessons markers")
	asJSON := fs.Bool("json", false, "emit JSON output")
	_ = fs.Parse(args)

	cfg := LoadCLIConfig(FindConfigPath())
	if cfg.HistoryDB == "" || cfg.WorkspaceDir == "" {
		fmt.Fprintln(os.Stderr, "error: HistoryDB and WorkspaceDir must be configured")
		os.Exit(1)
	}

	result, err := reflection.PromoteLessons(cfg.WorkspaceDir, cfg.HistoryDB, *threshold, *apply)
	if err != nil {
		fmt.Fprintf(os.Stderr, "promote failed: %v\n", err)
		os.Exit(1)
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(result)
		return
	}

	mode := "dry-run"
	if *apply {
		mode = "applied"
	}
	fmt.Printf("Promote (%s, threshold=%d): %d candidate(s)\n", mode, *threshold, len(result.Candidates))
	for _, c := range result.Candidates {
		fmt.Printf("  [%d×] %s\n", c.Occurrences, c.LessonKey)
	}
	if result.ReportPath != "" {
		fmt.Printf("Report: %s\n", result.ReportPath)
	}
}

func cmdLessonsHistory(args []string) {
	fs := flag.NewFlagSet("lessons history", flag.ExitOnError)
	limit := fs.Int("limit", 50, "max events to return")
	asJSON := fs.Bool("json", false, "emit JSON output")
	_ = fs.Parse(args)

	positional := fs.Args()
	prefix := ""
	if len(positional) > 0 {
		prefix = positional[0]
	}

	cfg := LoadCLIConfig(FindConfigPath())
	if cfg.HistoryDB == "" {
		fmt.Fprintln(os.Stderr, "error: no history DB configured")
		os.Exit(1)
	}

	events, err := reflection.QueryLessonHistory(cfg.HistoryDB, prefix, *limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "history failed: %v\n", err)
		os.Exit(1)
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(events)
		return
	}

	if len(events) == 0 {
		fmt.Println("(no events)")
		return
	}
	for _, e := range events {
		fmt.Printf("%s  task=%s  agent=%s  score=%d\n", e.CreatedAt, e.TaskID, e.Agent, e.Score)
		fmt.Printf("  key: %s\n", e.LessonKey)
		if e.Improvement != "" {
			fmt.Printf("  improvement: %s\n", e.Improvement)
		}
	}
}

func cmdLessonsAudit(args []string) {
	fs := flag.NewFlagSet("lessons audit", flag.ExitOnError)
	staleDaysStr := fs.String("stale-days", "90", "flag rules/*.md older than N days")
	asJSON := fs.Bool("json", false, "emit JSON output")
	_ = fs.Parse(args)

	days, err := strconv.Atoi(*staleDaysStr)
	if err != nil || days <= 0 {
		fmt.Fprintf(os.Stderr, "invalid --stale-days: %s\n", *staleDaysStr)
		os.Exit(1)
	}

	cfg := LoadCLIConfig(FindConfigPath())
	if cfg.WorkspaceDir == "" {
		fmt.Fprintln(os.Stderr, "error: WorkspaceDir must be configured")
		os.Exit(1)
	}

	results, err := reflection.AuditStaleRules(cfg.WorkspaceDir, days)
	if err != nil {
		fmt.Fprintf(os.Stderr, "audit failed: %v\n", err)
		os.Exit(1)
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(results)
		return
	}

	if len(results) == 0 {
		fmt.Printf("No stale rules (cutoff %d days).\n", days)
		return
	}
	fmt.Printf("Stale rules (cutoff %d days):\n", days)
	for _, r := range results {
		fmt.Printf("  %s  (last modified %s, ~%d days ago)\n", r.RelativePath, r.LastModified, r.AgeDays)
	}
}
