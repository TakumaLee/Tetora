package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"tetora/internal/db"
)

// Session mirrors the root Session type for CLI use.
type Session struct {
	ID             string  `json:"id"`
	Agent          string  `json:"agent"`
	Source         string  `json:"source"`
	Status         string  `json:"status"`
	Title          string  `json:"title"`
	ChannelKey     string  `json:"channelKey,omitempty"`
	TotalCost      float64 `json:"totalCost"`
	TotalTokensIn  int     `json:"totalTokensIn"`
	TotalTokensOut int     `json:"totalTokensOut"`
	MessageCount   int     `json:"messageCount"`
	ContextSize    int     `json:"contextSize"`
	CreatedAt      string  `json:"createdAt"`
	UpdatedAt      string  `json:"updatedAt"`
}

// SessionMessage mirrors the root SessionMessage type for CLI use.
type SessionMessage struct {
	ID        int     `json:"id"`
	SessionID string  `json:"sessionId"`
	Role      string  `json:"role"`
	Content   string  `json:"content"`
	CostUSD   float64 `json:"costUsd"`
	TokensIn  int     `json:"tokensIn"`
	TokensOut int     `json:"tokensOut"`
	Model     string  `json:"model"`
	TaskID    string  `json:"taskId"`
	CreatedAt string  `json:"createdAt"`
}

// SessionQuery holds filters for session listing.
type SessionQuery struct {
	Agent  string
	Status string
	Source string
	Limit  int
	Offset int
}

// SessionDetail holds a session with its messages.
type SessionDetail struct {
	Session  Session          `json:"session"`
	Messages []SessionMessage `json:"messages"`
}

// CleanupSessionStats holds results of a sessions cleanup operation.
type CleanupSessionStats struct {
	SessionsDeleted int
	MessagesDeleted int
	OrphansFixed    int
	DryRun          bool
	Sessions        []Session
}

// ErrAmbiguousSession is returned when a prefix matches multiple sessions.
type ErrAmbiguousSession struct {
	Prefix  string
	Matches []Session
}

func (e *ErrAmbiguousSession) Error() string {
	return fmt.Sprintf("ambiguous session ID %q: %d matches", e.Prefix, len(e.Matches))
}

// systemLogSessionID is the fixed session ID for non-chat dispatch task outputs.
const systemLogSessionID = "system:logs"

// CmdSession implements `tetora session <list|show|cleanup> [options]`.
func CmdSession(args []string) {
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
	cfg := LoadCLIConfig(FindConfigPath())
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
		costStr := fmt.Sprintf("$%.2f", s.TotalCost)
		title := s.Title
		if len(title) > 50 {
			title = title[:50] + "..."
		}
		shortID := s.ID
		if len(shortID) > 12 {
			shortID = shortID[:12]
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\t%s\n",
			shortID, s.Agent, s.Status, s.MessageCount, costStr, title, formatTime(s.UpdatedAt))
	}
	w.Flush()
	fmt.Printf("\n%d sessions (of %d total)\n", len(sessions), total)
}

func sessionCleanup(args []string) {
	cfg := LoadCLIConfig(FindConfigPath())
	if cfg.HistoryDB == "" {
		fmt.Fprintln(os.Stderr, "History DB not configured.")
		os.Exit(1)
	}

	dryRun := false
	fixMissing := false
	days := retentionDays(cfg, 30)

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

	var sizeBefore int64
	if fi, err := os.Stat(cfg.HistoryDB); err == nil {
		sizeBefore = fi.Size()
	}

	if dryRun {
		fmt.Printf("DRY RUN — no changes will be made (threshold: %d days)\n\n", days)
	} else {
		fmt.Printf("Cleaning up sessions older than %d days...\n\n", days)
	}

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
		_ = db.Exec(cfg.HistoryDB, "VACUUM")

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
	cfg := LoadCLIConfig(FindConfigPath())
	if cfg.HistoryDB == "" {
		fmt.Fprintln(os.Stderr, "History DB not configured.")
		os.Exit(1)
	}

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

// --- DB query helpers ---

// sessionSelectCols returns the SELECT column list for session queries.
// Uses "agent" column name; falls back gracefully if only "role" exists.
func sessionSelectCols() string {
	return "id, agent, source, status, title, channel_key, total_cost, total_tokens_in, total_tokens_out, message_count, created_at, updated_at," +
		" COALESCE((SELECT tokens_in FROM session_messages WHERE session_id = id ORDER BY id DESC LIMIT 1), 0) AS context_size"
}

func sessionFromRow(row map[string]any) Session {
	return Session{
		ID:             db.Str(row["id"]),
		Agent:          db.Str(row["agent"]),
		Source:         db.Str(row["source"]),
		Status:         db.Str(row["status"]),
		Title:          db.Str(row["title"]),
		ChannelKey:     db.Str(row["channel_key"]),
		TotalCost:      db.Float(row["total_cost"]),
		TotalTokensIn:  db.Int(row["total_tokens_in"]),
		TotalTokensOut: db.Int(row["total_tokens_out"]),
		MessageCount:   db.Int(row["message_count"]),
		ContextSize:    db.Int(row["context_size"]),
		CreatedAt:      db.Str(row["created_at"]),
		UpdatedAt:      db.Str(row["updated_at"]),
	}
}

func sessionMessageFromRow(row map[string]any) SessionMessage {
	return SessionMessage{
		ID:        db.Int(row["id"]),
		SessionID: db.Str(row["session_id"]),
		Role:      db.Str(row["role"]),
		Content:   db.Str(row["content"]),
		CostUSD:   db.Float(row["cost_usd"]),
		TokensIn:  db.Int(row["tokens_in"]),
		TokensOut: db.Int(row["tokens_out"]),
		Model:     db.Str(row["model"]),
		TaskID:    db.Str(row["task_id"]),
		CreatedAt: db.Str(row["created_at"]),
	}
}

func querySessions(dbPath string, q SessionQuery) ([]Session, int, error) {
	if q.Limit <= 0 {
		q.Limit = 20
	}

	var conditions []string
	if q.Agent != "" {
		conditions = append(conditions, fmt.Sprintf("agent = '%s'", db.Escape(q.Agent)))
	}
	if q.Status != "" {
		conditions = append(conditions, fmt.Sprintf("status = '%s'", db.Escape(q.Status)))
	}
	if q.Source != "" {
		conditions = append(conditions, fmt.Sprintf("source = '%s'", db.Escape(q.Source)))
	}

	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	countSQL := fmt.Sprintf("SELECT COUNT(*) as cnt FROM sessions %s", where)
	countRows, err := db.Query(dbPath, countSQL)
	if err != nil {
		return nil, 0, err
	}
	total := 0
	if len(countRows) > 0 {
		total = db.Int(countRows[0]["cnt"])
	}

	dataSQL := fmt.Sprintf(
		`SELECT `+sessionSelectCols()+`
		 FROM sessions %s ORDER BY updated_at DESC LIMIT %d OFFSET %d`,
		where, q.Limit, q.Offset)

	rows, err := db.Query(dbPath, dataSQL)
	if err != nil {
		return nil, 0, err
	}

	var sessions []Session
	for _, row := range rows {
		sessions = append(sessions, sessionFromRow(row))
	}
	return sessions, total, nil
}

func querySessionByID(dbPath, id string) (*Session, error) {
	sql := fmt.Sprintf(
		`SELECT `+sessionSelectCols()+`
		 FROM sessions WHERE id = '%s'`, db.Escape(id))
	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	s := sessionFromRow(rows[0])
	return &s, nil
}

func querySessionsByPrefix(dbPath, prefix string) ([]Session, error) {
	sql := fmt.Sprintf(
		`SELECT `+sessionSelectCols()+`
		 FROM sessions WHERE id LIKE '%s%%' ORDER BY updated_at DESC LIMIT 10`,
		db.Escape(prefix))
	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil, err
	}
	var sessions []Session
	for _, row := range rows {
		sessions = append(sessions, sessionFromRow(row))
	}
	return sessions, nil
}

func querySessionMessages(dbPath, sessionID string) ([]SessionMessage, error) {
	sql := fmt.Sprintf(
		`SELECT id, session_id, role, content, cost_usd, tokens_in, tokens_out, model, task_id, created_at
		 FROM session_messages WHERE session_id = '%s' ORDER BY id ASC`,
		db.Escape(sessionID))
	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil, err
	}
	var msgs []SessionMessage
	for _, row := range rows {
		msgs = append(msgs, sessionMessageFromRow(row))
	}
	return msgs, nil
}

func querySessionDetail(dbPath, sessionID string) (*SessionDetail, error) {
	sess, err := querySessionByID(dbPath, sessionID)
	if err != nil {
		return nil, err
	}

	if sess == nil && len(sessionID) < 36 {
		matches, err := querySessionsByPrefix(dbPath, sessionID)
		if err != nil {
			return nil, err
		}
		switch len(matches) {
		case 0:
			return nil, nil
		case 1:
			sess = &matches[0]
		default:
			return nil, &ErrAmbiguousSession{Prefix: sessionID, Matches: matches}
		}
	}

	if sess == nil {
		return nil, nil
	}

	msgs, err := querySessionMessages(dbPath, sess.ID)
	if err != nil {
		return nil, err
	}
	if msgs == nil {
		msgs = []SessionMessage{}
	}

	return &SessionDetail{
		Session:  *sess,
		Messages: msgs,
	}, nil
}

func cleanupSessionsWithStats(dbPath string, days int, dryRun bool) (CleanupSessionStats, error) {
	var stats CleanupSessionStats
	stats.DryRun = dryRun

	if dbPath == "" {
		return stats, nil
	}

	countSQL := fmt.Sprintf(
		`SELECT COUNT(*) as cnt FROM sessions WHERE status IN ('completed','archived')
		 AND datetime(created_at) < datetime('now','-%d days')`, days)
	rows, err := db.Query(dbPath, countSQL)
	if err != nil {
		return stats, fmt.Errorf("count sessions: %w", err)
	}
	if len(rows) > 0 {
		stats.SessionsDeleted = db.Int(rows[0]["cnt"])
	}

	msgCountSQL := fmt.Sprintf(
		`SELECT COUNT(*) as cnt FROM session_messages WHERE session_id IN (
		  SELECT id FROM sessions WHERE status IN ('completed','archived')
		  AND datetime(created_at) < datetime('now','-%d days')
		)`, days)
	mrows, err := db.Query(dbPath, msgCountSQL)
	if err != nil {
		return stats, fmt.Errorf("count messages: %w", err)
	}
	if len(mrows) > 0 {
		stats.MessagesDeleted = db.Int(mrows[0]["cnt"])
	}

	if dryRun {
		listSQL := fmt.Sprintf(
			`SELECT `+sessionSelectCols()+`
			 FROM sessions WHERE status IN ('completed','archived')
			 AND datetime(created_at) < datetime('now','-%d days')
			 ORDER BY created_at ASC`, days)
		srows, err := db.Query(dbPath, listSQL)
		if err != nil {
			return stats, fmt.Errorf("list sessions: %w", err)
		}
		for _, r := range srows {
			stats.Sessions = append(stats.Sessions, sessionFromRow(r))
		}
		return stats, nil
	}

	msgDelSQL := fmt.Sprintf(
		`DELETE FROM session_messages WHERE session_id IN (
		  SELECT id FROM sessions WHERE status IN ('completed','archived')
		  AND datetime(created_at) < datetime('now','-%d days')
		)`, days)
	if err := db.Exec(dbPath, msgDelSQL); err != nil {
		// log but continue
		fmt.Fprintf(os.Stderr, "Warning: cleanup session messages: %v\n", err)
	}

	sessDelSQL := fmt.Sprintf(
		`DELETE FROM sessions WHERE status IN ('completed','archived')
		 AND datetime(created_at) < datetime('now','-%d days')`, days)
	if err := db.Exec(dbPath, sessDelSQL); err != nil {
		return stats, fmt.Errorf("delete sessions: %w", err)
	}

	return stats, nil
}

func fixMissingSessions(dbPath string, days int, dryRun bool) (int, error) {
	if dbPath == "" {
		return 0, nil
	}

	countSQL := fmt.Sprintf(
		`SELECT COUNT(*) as cnt FROM sessions
		 WHERE status = 'active'
		 AND id != '%s'
		 AND datetime(updated_at) < datetime('now','-%d days')`,
		systemLogSessionID, days)
	rows, err := db.Query(dbPath, countSQL)
	if err != nil {
		return 0, fmt.Errorf("count orphan sessions: %w", err)
	}
	count := 0
	if len(rows) > 0 {
		count = db.Int(rows[0]["cnt"])
	}
	if dryRun || count == 0 {
		return count, nil
	}

	now := time.Now().Format(time.RFC3339)
	fixSQL := fmt.Sprintf(
		`UPDATE sessions SET status = 'completed', updated_at = '%s'
		 WHERE status = 'active'
		 AND id != '%s'
		 AND datetime(updated_at) < datetime('now','-%d days')`,
		now, systemLogSessionID, days)
	if err := db.Exec(dbPath, fixSQL); err != nil {
		return 0, fmt.Errorf("fix orphan sessions: %w", err)
	}
	return count, nil
}

// retentionDays extracts the sessions retention setting from the raw Retention
// JSON field in CLIConfig, falling back to the provided default if not set.
func retentionDays(cfg *CLIConfig, fallback int) int {
	if cfg.Retention == nil {
		return fallback
	}
	var r struct {
		Sessions int `json:"sessions"`
	}
	if err := jsonUnmarshal(cfg.Retention, &r); err == nil && r.Sessions > 0 {
		return r.Sessions
	}
	return fallback
}

// --- Format helpers ---

func formatTime(iso string) string {
	if iso == "" {
		return "-"
	}
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		t, err = time.Parse("2006-01-02T15:04:05", strings.TrimSuffix(iso, "Z"))
		if err != nil {
			return iso
		}
	}
	now := time.Now()
	if t.Format("2006-01-02") == now.Format("2006-01-02") {
		return t.Format("15:04:05")
	}
	return t.Format("2006-01-02 15:04")
}

// jsonUnmarshal is a thin wrapper around json.Unmarshal for use with json.RawMessage.
func jsonUnmarshal(data json.RawMessage, v any) error {
	return json.Unmarshal(data, v)
}

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
