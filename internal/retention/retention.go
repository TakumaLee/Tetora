package retention

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"tetora/internal/audit"
	"tetora/internal/config"
	"tetora/internal/db"
	"tetora/internal/history"
	"tetora/internal/log"
	"tetora/internal/session"
	"tetora/internal/upload"
	"tetora/internal/version"

	"math"
)

// MemoryEntry represents a key-value memory entry for export.
type MemoryEntry struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	Priority  string `json:"priority,omitempty"`
	UpdatedAt string `json:"updatedAt"`
}

// Result holds the outcome of a single retention cleanup operation.
type Result struct {
	Table   string `json:"table"`
	Deleted int    `json:"deleted"`
	Error   string `json:"error,omitempty"`
}

// ReflectionRow is a simplified reflection entry for export.
type ReflectionRow struct {
	TaskID      string  `json:"taskId"`
	Agent       string  `json:"agent"`
	Score       int     `json:"score"`
	Feedback    string  `json:"feedback"`
	Improvement string  `json:"improvement"`
	CostUSD     float64 `json:"costUsd"`
	CreatedAt   string  `json:"createdAt"`
}

// DataExport holds all user data for GDPR right-of-access export.
type DataExport struct {
	ExportedAt  string           `json:"exportedAt"`
	History     []history.JobRun `json:"history"`
	Sessions    []session.Session `json:"sessions"`
	Memory      []MemoryEntry    `json:"memory"`
	AuditLog    []audit.Entry    `json:"auditLog"`
	Reflections []ReflectionRow  `json:"reflections,omitempty"`
}

// Hooks wires in root-package functions that retention depends on.
type Hooks struct {
	// CleanupSessions removes sessions and session_messages older than N days.
	CleanupSessions func(dbPath string, days int) error
	// CleanupOldQueueItems removes completed/expired queue items older than N days.
	CleanupOldQueueItems func(dbPath string, days int)
	// CleanupOutputs removes output files older than N days.
	CleanupOutputs func(baseDir string, days int)
	// ListMemory returns all memory entries for the workspace.
	ListMemory func(workspaceDir string) ([]MemoryEntry, error)
	// QuerySessions returns sessions matching the given limit.
	QuerySessions func(dbPath string, limit int) ([]session.Session, error)
	// LoadMemoryAccessLog loads the .access.json file from workspace/memory/.
	LoadMemoryAccessLog func(workspaceDir string) map[string]string
	// SaveMemoryAccessLog writes the .access.json file to workspace/memory/.
	SaveMemoryAccessLog func(workspaceDir string, log map[string]string)
	// ParseMemoryFrontmatter extracts priority and body from a memory file.
	ParseMemoryFrontmatter func(data []byte) (priority string, body string)
	// BuildMemoryFrontmatter creates frontmatter + body content.
	BuildMemoryFrontmatter func(priority, body string) string
	// ParseMemoryMeta extracts priority, created_at, and body from a memory file.
	// Optional — if nil, falls back to ParseMemoryFrontmatter.
	ParseMemoryMeta func(data []byte) (priority, createdAt, body string)
}

// Days returns the configured value, or the fallback if not set.
func Days(configured, fallback int) int {
	if configured > 0 {
		return configured
	}
	return fallback
}

// CleanupWorkflowRuns removes workflow_runs older than N days.
func CleanupWorkflowRuns(dbPath string, days int) (int, error) {
	if dbPath == "" || days <= 0 {
		return 0, nil
	}
	countSQL := fmt.Sprintf(
		`SELECT COUNT(*) as cnt FROM workflow_runs WHERE datetime(started_at) < datetime('now','-%d days')`, days)
	rows, err := db.Query(dbPath, countSQL)
	count := 0
	if err == nil && len(rows) > 0 {
		count = jsonInt(rows[0]["cnt"])
	}

	sql := fmt.Sprintf(
		`DELETE FROM workflow_runs WHERE datetime(started_at) < datetime('now','-%d days')`, days)
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return 0, fmt.Errorf("cleanup workflow_runs: %s: %w", string(out), err)
	}
	return count, nil
}

// CleanupHandoffs removes handoffs and agent_messages older than N days.
func CleanupHandoffs(dbPath string, days int) (int, error) {
	if dbPath == "" || days <= 0 {
		return 0, nil
	}
	countSQL := fmt.Sprintf(
		`SELECT COUNT(*) as cnt FROM handoffs WHERE datetime(created_at) < datetime('now','-%d days')`, days)
	rows, err := db.Query(dbPath, countSQL)
	count := 0
	if err == nil && len(rows) > 0 {
		count = jsonInt(rows[0]["cnt"])
	}

	msgSQL := fmt.Sprintf(
		`DELETE FROM agent_messages WHERE datetime(created_at) < datetime('now','-%d days')`, days)
	cmd1 := exec.Command("sqlite3", dbPath, msgSQL)
	cmd1.CombinedOutput() // best effort

	sql := fmt.Sprintf(
		`DELETE FROM handoffs WHERE datetime(created_at) < datetime('now','-%d days')`, days)
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return 0, fmt.Errorf("cleanup handoffs: %s: %w", string(out), err)
	}
	return count, nil
}

// CleanupReflections removes reflections older than N days.
func CleanupReflections(dbPath string, days int) (int, error) {
	if dbPath == "" || days <= 0 {
		return 0, nil
	}
	countSQL := fmt.Sprintf(
		`SELECT COUNT(*) as cnt FROM reflections WHERE datetime(created_at) < datetime('now','-%d days')`, days)
	rows, err := db.Query(dbPath, countSQL)
	count := 0
	if err == nil && len(rows) > 0 {
		count = jsonInt(rows[0]["cnt"])
	}

	sql := fmt.Sprintf(
		`DELETE FROM reflections WHERE datetime(created_at) < datetime('now','-%d days')`, days)
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return 0, fmt.Errorf("cleanup reflections: %s: %w", string(out), err)
	}
	return count, nil
}

// CleanupSLAChecks removes sla_checks older than N days.
func CleanupSLAChecks(dbPath string, days int) (int, error) {
	if dbPath == "" || days <= 0 {
		return 0, nil
	}
	countSQL := fmt.Sprintf(
		`SELECT COUNT(*) as cnt FROM sla_checks WHERE datetime(checked_at) < datetime('now','-%d days')`, days)
	rows, err := db.Query(dbPath, countSQL)
	count := 0
	if err == nil && len(rows) > 0 {
		count = jsonInt(rows[0]["cnt"])
	}

	sql := fmt.Sprintf(
		`DELETE FROM sla_checks WHERE datetime(checked_at) < datetime('now','-%d days')`, days)
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return 0, fmt.Errorf("cleanup sla_checks: %s: %w", string(out), err)
	}
	return count, nil
}

// CleanupTrustEvents removes trust_events older than N days.
func CleanupTrustEvents(dbPath string, days int) (int, error) {
	if dbPath == "" || days <= 0 {
		return 0, nil
	}
	countSQL := fmt.Sprintf(
		`SELECT COUNT(*) as cnt FROM trust_events WHERE datetime(created_at) < datetime('now','-%d days')`, days)
	rows, err := db.Query(dbPath, countSQL)
	count := 0
	if err == nil && len(rows) > 0 {
		count = jsonInt(rows[0]["cnt"])
	}

	sql := fmt.Sprintf(
		`DELETE FROM trust_events WHERE datetime(created_at) < datetime('now','-%d days')`, days)
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return 0, fmt.Errorf("cleanup trust_events: %s: %w", string(out), err)
	}
	return count, nil
}

// CleanupLogFiles removes rotated log files older than N days.
func CleanupLogFiles(logDir string, days int) int {
	if logDir == "" || days <= 0 {
		return 0
	}
	entries, err := os.ReadDir(logDir)
	if err != nil {
		return 0
	}
	cutoff := time.Now().AddDate(0, 0, -days)
	removed := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.Contains(name, ".log.") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			if os.Remove(filepath.Join(logDir, name)) == nil {
				removed++
			}
		}
	}
	return removed
}

// CleanupClaudeSessions removes old Claude Code CLI session artifacts from
// ~/.claude/projects/. When many session dirs/JSONL files accumulate, concurrent
// `claude --print` instances can hang during startup scanning.
func CleanupClaudeSessions(days int) (removed int) {
	if days <= 0 {
		return 0
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return 0
	}
	projectsDir := filepath.Join(home, ".claude", "projects")
	projEntries, err := os.ReadDir(projectsDir)
	if err != nil {
		return 0
	}

	cutoff := time.Now().AddDate(0, 0, -days)

	for _, proj := range projEntries {
		if !proj.IsDir() {
			continue
		}
		projPath := filepath.Join(projectsDir, proj.Name())
		entries, err := os.ReadDir(projPath)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			base := strings.TrimSuffix(name, ".jsonl")
			if len(base) != 36 || strings.Count(base, "-") != 4 {
				continue
			}
			info, err := e.Info()
			if err != nil || info.ModTime().After(cutoff) {
				continue
			}
			target := filepath.Join(projPath, name)
			if e.IsDir() {
				os.RemoveAll(target)
			} else {
				os.Remove(target)
			}
			removed++
		}
	}
	return removed
}

// CleanupStaleMemory archives memory files that haven't been accessed in N days
// and are not P0 (permanent). Returns the count of archived entries.
func CleanupStaleMemory(workspaceDir string, days int, h Hooks) (int, error) {
	if workspaceDir == "" || days <= 0 {
		return 0, nil
	}

	memDir := filepath.Join(workspaceDir, "memory")
	archiveDir := filepath.Join(memDir, "archive")

	accessLog := h.LoadMemoryAccessLog(workspaceDir)
	cutoff := time.Now().AddDate(0, 0, -days)
	archived := 0

	entries, err := os.ReadDir(memDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		key := strings.TrimSuffix(e.Name(), ".md")

		data, err := os.ReadFile(filepath.Join(memDir, e.Name()))
		if err != nil {
			continue
		}
		priority, body := h.ParseMemoryFrontmatter(data)

		if priority == "P0" {
			continue
		}

		isStale := false
		if ts, ok := accessLog[key]; ok {
			if t, err := time.Parse(time.RFC3339, ts); err == nil {
				isStale = t.Before(cutoff)
			}
		} else {
			if info, err := e.Info(); err == nil {
				isStale = info.ModTime().Before(cutoff)
			}
		}

		if !isStale {
			continue
		}

		os.MkdirAll(archiveDir, 0o755)
		archiveContent := h.BuildMemoryFrontmatter("P2", body)
		if err := os.WriteFile(filepath.Join(archiveDir, e.Name()), []byte(archiveContent), 0o644); err != nil {
			continue
		}
		os.Remove(filepath.Join(memDir, e.Name()))

		delete(accessLog, key)
		archived++
	}

	if archived > 0 {
		h.SaveMemoryAccessLog(workspaceDir, accessLog)
	}
	return archived, nil
}

// PruneByScore removes memory entries whose temporal decay score falls below minScore.
// P0 (permanent) entries are always skipped. Returns count of pruned entries.
func PruneByScore(workspaceDir string, halfLifeDays, minScore float64, h Hooks) (int, error) {
	if workspaceDir == "" || minScore <= 0 {
		return 0, nil
	}

	memDir := filepath.Join(workspaceDir, "memory")
	accessLog := h.LoadMemoryAccessLog(workspaceDir)
	pruned := 0

	entries, err := os.ReadDir(memDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		key := strings.TrimSuffix(e.Name(), ".md")

		data, err := os.ReadFile(filepath.Join(memDir, e.Name()))
		if err != nil {
			continue
		}

		// Parse priority and created_at.
		var priority, createdAt string
		if h.ParseMemoryMeta != nil {
			priority, createdAt, _ = h.ParseMemoryMeta(data)
		} else {
			priority, _ = h.ParseMemoryFrontmatter(data)
		}

		if priority == "P0" {
			continue
		}

		// Determine reference time: lastAccessed > createdAt > file modtime.
		var refTime time.Time
		if ts, ok := accessLog[key]; ok {
			if t, err := time.Parse(time.RFC3339, ts); err == nil {
				refTime = t
			}
		}
		if refTime.IsZero() && createdAt != "" {
			if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
				refTime = t
			}
		}
		if refTime.IsZero() {
			if info, err := e.Info(); err == nil {
				refTime = info.ModTime()
			} else {
				continue
			}
		}

		// Compute decay: score=1.0 decayed over time.
		ageDays := time.Since(refTime).Hours() / 24.0
		score := math.Pow(0.5, ageDays/halfLifeDays)

		if score >= minScore {
			continue
		}

		os.Remove(filepath.Join(memDir, e.Name()))
		delete(accessLog, key)
		pruned++
	}

	if pruned > 0 {
		h.SaveMemoryAccessLog(workspaceDir, accessLog)
	}
	return pruned, nil
}

// Run executes all retention cleanups and returns results.
func Run(cfg *config.Config, h Hooks) []Result {
	var results []Result
	dbPath := cfg.HistoryDB

	if dbPath != "" {
		// job_runs
		days := Days(cfg.Retention.History, 90)
		if err := history.Cleanup(dbPath, days); err != nil {
			results = append(results, Result{Table: "job_runs", Error: err.Error()})
		} else {
			results = append(results, Result{Table: "job_runs", Deleted: -1})
		}

		// audit_log
		days = Days(cfg.Retention.AuditLog, 365)
		if err := audit.Cleanup(dbPath, days); err != nil {
			results = append(results, Result{Table: "audit_log", Error: err.Error()})
		} else {
			results = append(results, Result{Table: "audit_log", Deleted: -1})
		}

		// sessions + session_messages
		days = Days(cfg.Retention.Sessions, 30)
		if err := h.CleanupSessions(dbPath, days); err != nil {
			results = append(results, Result{Table: "sessions", Error: err.Error()})
		} else {
			results = append(results, Result{Table: "sessions", Deleted: -1})
		}

		// offline_queue
		days = Days(cfg.Retention.Queue, 7)
		h.CleanupOldQueueItems(dbPath, days)
		results = append(results, Result{Table: "offline_queue", Deleted: -1})

		// workflow_runs
		days = Days(cfg.Retention.Workflows, 90)
		if n, err := CleanupWorkflowRuns(dbPath, days); err != nil {
			results = append(results, Result{Table: "workflow_runs", Error: err.Error()})
		} else {
			results = append(results, Result{Table: "workflow_runs", Deleted: n})
		}

		// handoffs + agent_messages
		days = Days(cfg.Retention.Handoffs, 60)
		if n, err := CleanupHandoffs(dbPath, days); err != nil {
			results = append(results, Result{Table: "handoffs", Error: err.Error()})
		} else {
			results = append(results, Result{Table: "handoffs", Deleted: n})
		}

		// reflections
		days = Days(cfg.Retention.Reflections, 60)
		if n, err := CleanupReflections(dbPath, days); err != nil {
			results = append(results, Result{Table: "reflections", Error: err.Error()})
		} else {
			results = append(results, Result{Table: "reflections", Deleted: n})
		}

		// sla_checks
		days = Days(cfg.Retention.SLA, 90)
		if n, err := CleanupSLAChecks(dbPath, days); err != nil {
			results = append(results, Result{Table: "sla_checks", Error: err.Error()})
		} else {
			results = append(results, Result{Table: "sla_checks", Deleted: n})
		}

		// trust_events
		days = Days(cfg.Retention.TrustEvents, 90)
		if n, err := CleanupTrustEvents(dbPath, days); err != nil {
			results = append(results, Result{Table: "trust_events", Error: err.Error()})
		} else {
			results = append(results, Result{Table: "trust_events", Deleted: n})
		}

		// config_versions
		days = Days(cfg.Retention.Versions, 180)
		version.Cleanup(dbPath, days)
		results = append(results, Result{Table: "config_versions", Deleted: -1})
	}

	// Output files
	days := Days(cfg.Retention.Outputs, 30)
	h.CleanupOutputs(cfg.BaseDir, days)
	results = append(results, Result{Table: "outputs", Deleted: -1})

	// Upload files
	days = Days(cfg.Retention.Uploads, 7)
	upload.Cleanup(filepath.Join(cfg.BaseDir, "uploads"), days)
	results = append(results, Result{Table: "uploads", Deleted: -1})

	// Log files
	days = Days(cfg.Retention.Logs, 14)
	logDir := filepath.Join(cfg.BaseDir, "logs")
	n := CleanupLogFiles(logDir, days)
	results = append(results, Result{Table: "log_files", Deleted: n})

	// Stale memory archival
	days = Days(cfg.Retention.Memory, 30)
	if memArchived, err := CleanupStaleMemory(cfg.WorkspaceDir, days, h); err != nil {
		results = append(results, Result{Table: "memory", Error: err.Error()})
	} else {
		results = append(results, Result{Table: "memory", Deleted: memArchived})
	}

	// Score-based memory pruning
	td := cfg.Embedding.TemporalDecay
	if td.Enabled && td.MinScore > 0 {
		halfLife := td.HalfLifeDays
		if halfLife <= 0 {
			halfLife = 30.0
		}
		if pruned, err := PruneByScore(cfg.WorkspaceDir, halfLife, td.MinScore, h); err != nil {
			results = append(results, Result{Table: "memory_prune", Error: err.Error()})
		} else if pruned > 0 {
			results = append(results, Result{Table: "memory_prune", Deleted: pruned})
		}
	}

	// Claude CLI session artifacts
	days = Days(cfg.Retention.ClaudeSessions, 3)
	csRemoved := CleanupClaudeSessions(days)
	results = append(results, Result{Table: "claude_sessions", Deleted: csRemoved})

	log.Info("retention cleanup completed", "tables", len(results))
	return results
}

// CompilePIIPatterns compiles regex patterns for PII detection.
func CompilePIIPatterns(patterns []string) []*regexp.Regexp {
	var compiled []*regexp.Regexp
	for _, p := range patterns {
		if re, err := regexp.Compile(p); err == nil {
			compiled = append(compiled, re)
		} else {
			log.Warn("invalid PII pattern, skipping", "pattern", p, "error", err)
		}
	}
	return compiled
}

// RedactPII replaces all PII pattern matches with [REDACTED].
func RedactPII(text string, patterns []*regexp.Regexp) string {
	if len(patterns) == 0 || text == "" {
		return text
	}
	for _, re := range patterns {
		text = re.ReplaceAllString(text, "[REDACTED]")
	}
	return text
}

// QueryStats returns row counts per table.
func QueryStats(dbPath string) map[string]int {
	stats := make(map[string]int)
	if dbPath == "" {
		return stats
	}

	tables := []string{
		"job_runs", "audit_log", "sessions", "session_messages",
		"workflow_runs", "handoffs", "agent_messages",
		"reflections", "sla_checks", "trust_events",
		"config_versions", "agent_memory", "offline_queue",
	}
	for _, t := range tables {
		sql := fmt.Sprintf("SELECT COUNT(*) as cnt FROM %s", t)
		rows, err := db.Query(dbPath, sql)
		if err == nil && len(rows) > 0 {
			stats[t] = jsonInt(rows[0]["cnt"])
		}
	}
	return stats
}

// Export exports all user data as JSON (GDPR right of access).
func Export(cfg *config.Config, h Hooks) ([]byte, error) {
	dbPath := cfg.HistoryDB
	if dbPath == "" {
		return json.Marshal(DataExport{ExportedAt: time.Now().UTC().Format(time.RFC3339)})
	}

	export := DataExport{
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
	}

	// History
	if runs, err := history.Query(dbPath, "", 10000); err == nil {
		export.History = runs
	}

	// Sessions
	if sessions, err := h.QuerySessions(dbPath, 10000); err == nil {
		export.Sessions = sessions
	}

	// Memory
	if entries, err := h.ListMemory(cfg.WorkspaceDir); err == nil {
		export.Memory = entries
	}

	// Audit log
	if entries, _, err := audit.Query(dbPath, 10000, 0); err == nil {
		export.AuditLog = entries
	}

	// Reflections
	export.Reflections = QueryReflectionsForExport(dbPath)

	return json.MarshalIndent(export, "", "  ")
}

// QueryReflectionsForExport queries all reflections for data export.
func QueryReflectionsForExport(dbPath string) []ReflectionRow {
	if dbPath == "" {
		return nil
	}
	sql := `SELECT task_id, agent, score, feedback, improvement, cost_usd, created_at
	        FROM reflections ORDER BY created_at DESC LIMIT 10000`
	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil
	}
	var refs []ReflectionRow
	for _, row := range rows {
		refs = append(refs, ReflectionRow{
			TaskID:      jsonStr(row["task_id"]),
			Agent:       jsonStr(row["agent"]),
			Score:       jsonInt(row["score"]),
			Feedback:    jsonStr(row["feedback"]),
			Improvement: jsonStr(row["improvement"]),
			CostUSD:     jsonFloat(row["cost_usd"]),
			CreatedAt:   jsonStr(row["created_at"]),
		})
	}
	return refs
}

// PurgeBefore deletes all data before the given date across all tables.
func PurgeBefore(dbPath, before string) ([]Result, error) {
	if dbPath == "" {
		return nil, fmt.Errorf("no database configured")
	}

	type purgeTarget struct {
		table   string
		timeCol string
	}
	targets := []purgeTarget{
		{"job_runs", "started_at"},
		{"audit_log", "timestamp"},
		{"session_messages", "created_at"},
		{"sessions", "created_at"},
		{"workflow_runs", "started_at"},
		{"agent_messages", "created_at"},
		{"handoffs", "created_at"},
		{"reflections", "created_at"},
		{"sla_checks", "checked_at"},
		{"trust_events", "created_at"},
		{"config_versions", "created_at"},
		{"offline_queue", "created_at"},
	}

	var results []Result
	for _, t := range targets {
		countSQL := fmt.Sprintf(
			`SELECT COUNT(*) as cnt FROM %s WHERE datetime(%s) < datetime('%s')`,
			t.table, t.timeCol, db.Escape(before))
		rows, err := db.Query(dbPath, countSQL)
		count := 0
		if err == nil && len(rows) > 0 {
			count = jsonInt(rows[0]["cnt"])
		}

		delSQL := fmt.Sprintf(
			`DELETE FROM %s WHERE datetime(%s) < datetime('%s')`,
			t.table, t.timeCol, db.Escape(before))
		cmd := exec.Command("sqlite3", dbPath, delSQL)
		if out, err := cmd.CombinedOutput(); err != nil {
			results = append(results, Result{
				Table: t.table, Error: fmt.Sprintf("%s: %s", err, string(out)),
			})
		} else {
			results = append(results, Result{Table: t.table, Deleted: count})
		}
	}

	cmd := exec.Command("sqlite3", dbPath, "VACUUM;")
	cmd.CombinedOutput() // best effort

	return results, nil
}

func jsonStr(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func jsonFloat(v any) float64 {
	if v == nil {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case string:
		var f float64
		fmt.Sscanf(n, "%f", &f)
		return f
	}
	return 0
}

func jsonInt(v any) int {
	if v == nil {
		return 0
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case string:
		var i int
		fmt.Sscanf(n, "%d", &i)
		return i
	}
	return 0
}
