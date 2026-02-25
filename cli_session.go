package main

import (
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"
)

func cmdSession(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: tetora session <list|show> [options]")
		fmt.Println()
		fmt.Println("Commands:")
		fmt.Println("  list    List sessions [--role ROLE] [--status STATUS] [--limit N]")
		fmt.Println("  show    Show session conversation [session-id]")
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
		case "--role", "-r":
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
		Role: role, Status: status, Limit: limit,
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
			shortID, s.Role, s.Status, s.MessageCount, cost, title, formatTime(s.UpdatedAt))
	}
	w.Flush()
	fmt.Printf("\n%d sessions (of %d total)\n", len(sessions), total)
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
				fmt.Fprintf(os.Stderr, "  %s  %s  %s\n", s.ID, s.Role, s.Title)
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
	fmt.Printf("  Role:     %s\n", s.Role)
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
