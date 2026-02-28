package main

import (
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"
)

// formatBytes converts a byte count to a human-readable string (KB/MB/GB).
func formatBytes(n int64) string {
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
	)
	switch {
	case n >= gb:
		return fmt.Sprintf("%.2f GB", float64(n)/float64(gb))
	case n >= mb:
		return fmt.Sprintf("%.2f MB", float64(n)/float64(mb))
	case n >= kb:
		return fmt.Sprintf("%.2f KB", float64(n)/float64(kb))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func cmdSession(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: tetora session <list|show|cleanup> [options]")
		fmt.Println()
		fmt.Println("Commands:")
		fmt.Println("  list      List sessions [--agent AGENT] [--status STATUS] [--limit N]")
		fmt.Println("  show      Show session conversation [session-id]")
		fmt.Println("  cleanup   Remove old completed/archived sessions from DB")
		return
	}
	switch args[0] {
	case "list", "ls":
		sessionList(args[1:])
	case "show", "view":
		if len(args) < 2 {
			fmt.Println("Usage: tetora session show <session-id>")
			return
		}
		sessionShow(args[1])
	case "cleanup":
		sessionCleanup(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown action: %s\n", args[0])
	}
}

func sessionList(args []string) {
	cfg := loadConfig(findConfigPath())
	if cfg.HistoryDB == "" {
		fmt.Fprintln(os.Stderr, "History DB not configured.")
		os.Exit(1)
	}

	role := ""
	status := ""
	limit := 20
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--agent", "--role", "-r":
			if i+1 < len(args) {
				i++
				role = args[i]
			}
		case "--status", "-s":
			if i+1 < len(args) {
				i++
				status = args[i]
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

	sessions, total, err := querySessions(cfg.HistoryDB, SessionQuery{
		Agent: role, Status: status, Limit: limit,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if len(sessions) == 0 {
		fmt.Println("No sessions found.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintf(w, "ID\tROLE\tSTATUS\tMSGS\tCOST\tTITLE\tUPDATED\n")
	for _, s := range sessions {
		cost := fmt.Sprintf("$%.2f", s.TotalCost)
		title := s.Title
		if len(title) > 50 {
			title = title[:50] + "..."
		}
		shortID := s.ID
		if len(shortID) > 12 {
			shortID = shortID[:12]
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\t%s\n",
			shortID, s.Agent, s.Status, s.MessageCount, cost, title, formatTime(s.UpdatedAt))
	}
	w.Flush()
	fmt.Printf("\n%d sessions (of %d total)\n", len(sessions), total)
}

func sessionCleanup(args []string) {
	cfg := loadConfig(findConfigPath())
	if cfg.HistoryDB == "" {
		fmt.Fprintln(os.Stderr, "History DB not configured.")
		os.Exit(1)
	}

	dryRun := false
	fixMissing := false
	days := retentionDays(cfg.Retention.Sessions, 30)

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dry-run", "-n":
			dryRun = true
		case "--fix-missing":
			fixMissing = true
		case "--days", "-d", "--before":
			if i+1 < len(args) {
				i++
				if n, err := strconv.Atoi(args[i]); err == nil && n > 0 {
					days = n
				}
			}
		case "--help", "-h":
			fmt.Println("Usage: tetora session cleanup [options]")
			fmt.Println()
			fmt.Println("Options:")
			fmt.Println("  --before N, --days N  Delete sessions older than N days (default: from config, fallback 30)")
			fmt.Println("  --dry-run, -n         Show what would be deleted without making changes")
			fmt.Println("  --fix-missing         Mark stale active sessions as completed (orphan recovery)")
			return
		}
	}

	// Capture DB size before cleanup to estimate space freed.
	var sizeBefore int64
	if fi, err := os.Stat(cfg.HistoryDB); err == nil {
		sizeBefore = fi.Size()
	}

	if dryRun {
		fmt.Printf("DRY RUN — no changes will be made (threshold: %d days)\n\n", days)
	} else {
		fmt.Printf("Cleaning up sessions older than %d days...\n\n", days)
	}

	// --fix-missing: identify/fix stale active sessions.
	if fixMissing {
		orphans, err := fixMissingSessions(cfg.HistoryDB, days, dryRun)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error fixing orphan sessions: %v\n", err)
			os.Exit(1)
		}
		if dryRun {
			fmt.Printf("  Stale active sessions (would mark completed): %d\n", orphans)
		} else {
			fmt.Printf("  Stale active sessions marked completed: %d\n", orphans)
		}
	}

	// Main cleanup: delete old completed/archived sessions.
	stats, err := cleanupSessionsWithStats(cfg.HistoryDB, days, dryRun)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if dryRun && len(stats.Sessions) > 0 {
		fmt.Println("Sessions that would be deleted:")
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintf(w, "  ID\tROLE\tSTATUS\tMSGS\tCREATED\tTITLE\n")
		for _, s := range stats.Sessions {
			shortID := s.ID
			if len(shortID) > 12 {
				shortID = shortID[:12]
			}
			title := s.Title
			if len(title) > 40 {
				title = title[:40] + "..."
			}
			fmt.Fprintf(w, "  %s\t%s\t%s\t%d\t%s\t%s\n",
				shortID, s.Agent, s.Status, s.MessageCount, formatTime(s.CreatedAt), title)
		}
		w.Flush()
		fmt.Println()
	}

	if dryRun {
		fmt.Printf("Would delete: %d sessions, %d messages\n", stats.SessionsDeleted, stats.MessagesDeleted)
	} else {
		// Run VACUUM to reclaim disk space after bulk deletion.
		_ = execDB(cfg.HistoryDB, "VACUUM")

		var sizeAfter int64
		if fi, err := os.Stat(cfg.HistoryDB); err == nil {
			sizeAfter = fi.Size()
		}
		freed := sizeBefore - sizeAfter
		if freed < 0 {
			freed = 0
		}

		fmt.Printf("Deleted:  %d sessions, %d messages\n", stats.SessionsDeleted, stats.MessagesDeleted)
		if freed > 0 {
			fmt.Printf("Freed:    %s\n", formatBytes(freed))
		}
		fmt.Println("Done.")
	}
}

func sessionShow(id string) {
	cfg := loadConfig(findConfigPath())
	if cfg.HistoryDB == "" {
		fmt.Fprintln(os.Stderr, "History DB not configured.")
		os.Exit(1)
	}

	// Support prefix matching for short IDs.
	detail, err := querySessionDetail(cfg.HistoryDB, id)
	if err != nil {
		if ambig, ok := err.(*ErrAmbiguousSession); ok {
			fmt.Fprintf(os.Stderr, "Ambiguous session ID, multiple matches:\n")
			for _, s := range ambig.Matches {
				fmt.Fprintf(os.Stderr, "  %s  %s  %s\n", s.ID, s.Agent, s.Title)
			}
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if detail == nil {
		fmt.Fprintf(os.Stderr, "Session %s not found.\n", id)
		os.Exit(1)
	}

	s := detail.Session
	fmt.Printf("Session %s\n", s.ID)
	fmt.Printf("  Role:     %s\n", s.Agent)
	fmt.Printf("  Source:   %s\n", s.Source)
	fmt.Printf("  Status:   %s\n", s.Status)
	fmt.Printf("  Title:    %s\n", s.Title)
	fmt.Printf("  Messages: %d\n", s.MessageCount)
	fmt.Printf("  Cost:     $%.4f\n", s.TotalCost)
	fmt.Printf("  Tokens:   %d in / %d out\n", s.TotalTokensIn, s.TotalTokensOut)
	fmt.Printf("  Created:  %s\n", s.CreatedAt)
	fmt.Printf("  Updated:  %s\n", s.UpdatedAt)

	if len(detail.Messages) > 0 {
		fmt.Println("\n--- Conversation ---")
		for _, m := range detail.Messages {
			prefix := "  [SYS]"
			switch m.Role {
			case "user":
				prefix = "  [USER]"
			case "assistant":
				prefix = "  [AGENT]"
			}
			content := m.Content
			if len(content) > 500 {
				content = content[:500] + "..."
			}
			costStr := ""
			if m.CostUSD > 0 {
				costStr = fmt.Sprintf(" ($%.4f)", m.CostUSD)
			}
			fmt.Printf("\n%s%s %s\n%s\n", prefix, costStr, formatTime(m.CreatedAt), content)
		}
	}
}
