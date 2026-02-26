package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// --- Retention Config ---

type RetentionConfig struct {
	History     int      `json:"history,omitempty"`     // days to keep job_runs (default 90)
	Sessions    int      `json:"sessions,omitempty"`    // days to keep sessions (default 30)
	AuditLog    int      `json:"auditLog,omitempty"`    // days to keep audit_log (default 365)
	Logs        int      `json:"logs,omitempty"`        // days to keep log files (default 14)
	Workflows   int      `json:"workflows,omitempty"`   // days to keep workflow_runs (default 90)
	Reflections int      `json:"reflections,omitempty"` // days to keep reflections (default 60)
	SLA         int      `json:"sla,omitempty"`         // days to keep sla_checks (default 90)
	TrustEvents int      `json:"trustEvents,omitempty"` // days to keep trust_events (default 90)
	Handoffs    int      `json:"handoffs,omitempty"`    // days to keep handoffs/agent_messages (default 60)
	Queue       int      `json:"queue,omitempty"`       // days to keep offline_queue items (default 7)
	Versions    int      `json:"versions,omitempty"`    // days to keep config_versions (default 180)
	Outputs     int      `json:"outputs,omitempty"`     // days to keep output files (default 30)
	Uploads     int      `json:"uploads,omitempty"`     // days to keep upload files (default 7)
	Memory      int      `json:"memory,omitempty"`      // days before stale memory archival (default 30)
	PIIPatterns []string `json:"piiPatterns,omitempty"` // regex patterns for PII redaction
}

// retentionDays returns the configured value, or the fallback if not set.
func retentionDays(configured, fallback int) int {
	if configured > 0 {
		return configured
	}
	return fallback
}

// --- Retention Results ---

type RetentionResult struct {
	Table   string `json:"table"`
	Deleted int    `json:"deleted"`
	Error   string `json:"error,omitempty"`
}

// --- Missing Cleanup Functions ---

// cleanupWorkflowRuns removes workflow_runs older than N days.
func cleanupWorkflowRuns(dbPath string, days int) (int, error) {
	if dbPath == "" || days <= 0 {
		return 0, nil
	}
	countSQL := fmt.Sprintf(
		`SELECT COUNT(*) as cnt FROM workflow_runs WHERE datetime(started_at) < datetime('now','-%d days')`, days)
	rows, err := queryDB(dbPath, countSQL)
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

// cleanupHandoffs removes handoffs and agent_messages older than N days.
func cleanupHandoffs(dbPath string, days int) (int, error) {
	if dbPath == "" || days <= 0 {
		return 0, nil
	}
	countSQL := fmt.Sprintf(
		`SELECT COUNT(*) as cnt FROM handoffs WHERE datetime(created_at) < datetime('now','-%d days')`, days)
	rows, err := queryDB(dbPath, countSQL)
	count := 0
	if err == nil && len(rows) > 0 {
		count = jsonInt(rows[0]["cnt"])
	}

	// Delete agent_messages first (references handoffs).
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

// cleanupReflections removes reflections older than N days.
func cleanupReflections(dbPath string, days int) (int, error) {
	if dbPath == "" || days <= 0 {
		return 0, nil
	}
	countSQL := fmt.Sprintf(
		`SELECT COUNT(*) as cnt FROM reflections WHERE datetime(created_at) < datetime('now','-%d days')`, days)
	rows, err := queryDB(dbPath, countSQL)
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

// cleanupSLAChecks removes sla_checks older than N days.
func cleanupSLAChecks(dbPath string, days int) (int, error) {
	if dbPath == "" || days <= 0 {
		return 0, nil
	}
	countSQL := fmt.Sprintf(
		`SELECT COUNT(*) as cnt FROM sla_checks WHERE datetime(checked_at) < datetime('now','-%d days')`, days)
	rows, err := queryDB(dbPath, countSQL)
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

// cleanupTrustEvents removes trust_events older than N days.
func cleanupTrustEvents(dbPath string, days int) (int, error) {
	if dbPath == "" || days <= 0 {
		return 0, nil
	}
	countSQL := fmt.Sprintf(
		`SELECT COUNT(*) as cnt FROM trust_events WHERE datetime(created_at) < datetime('now','-%d days')`, days)
	rows, err := queryDB(dbPath, countSQL)
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

// cleanupLogFiles removes rotated log files older than N days.
func cleanupLogFiles(logDir string, days int) int {
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
		// Only remove rotated log files (e.g., tetora.log.1, tetora.log.2).
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

// --- Stale Memory Cleanup ---

// cleanupStaleMemory archives memory files that haven't been accessed in N days
// and are not P0 (permanent). Returns the count of archived entries.
func cleanupStaleMemory(cfg *Config, days int) (int, error) {
	if cfg == nil || cfg.WorkspaceDir == "" || days <= 0 {
		return 0, nil
	}

	memDir := filepath.Join(cfg.WorkspaceDir, "memory")
	archiveDir := filepath.Join(memDir, "archive")

	accessLog := loadMemoryAccessLog(cfg)
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

		// Read file and check priority.
		data, err := os.ReadFile(filepath.Join(memDir, e.Name()))
		if err != nil {
			continue
		}
		priority, body := parseMemoryFrontmatter(data)

		// Never archive P0 (permanent) entries.
		if priority == "P0" {
			continue
		}

		// Check last access time.
		isStale := false
		if ts, ok := accessLog[key]; ok {
			if t, err := time.Parse(time.RFC3339, ts); err == nil {
				isStale = t.Before(cutoff)
			}
		} else {
			// No access record — use file mod time.
			if info, err := e.Info(); err == nil {
				isStale = info.ModTime().Before(cutoff)
			}
		}

		if !isStale {
			continue
		}

		// Archive: create archive dir, write with P2 priority, remove original.
		os.MkdirAll(archiveDir, 0o755)
		archiveContent := buildMemoryFrontmatter("P2", body)
		if err := os.WriteFile(filepath.Join(archiveDir, e.Name()), []byte(archiveContent), 0o644); err != nil {
			continue
		}
		os.Remove(filepath.Join(memDir, e.Name()))

		// Remove from access log.
		delete(accessLog, key)
		archived++
	}

	if archived > 0 {
		saveMemoryAccessLog(cfg, accessLog)
	}
	return archived, nil
}

// --- Master Retention Orchestrator ---

// runRetention executes all retention cleanups and returns results.
func runRetention(cfg *Config) []RetentionResult {
	var results []RetentionResult
	dbPath := cfg.HistoryDB

	if dbPath != "" {
		// job_runs
		days := retentionDays(cfg.Retention.History, 90)
		if err := cleanupHistory(dbPath, days); err != nil {
			results = append(results, RetentionResult{Table: "job_runs", Error: err.Error()})
		} else {
			results = append(results, RetentionResult{Table: "job_runs", Deleted: -1}) // existing func doesn't return count
		}

		// audit_log
		days = retentionDays(cfg.Retention.AuditLog, 365)
		if err := cleanupAuditLog(dbPath, days); err != nil {
			results = append(results, RetentionResult{Table: "audit_log", Error: err.Error()})
		} else {
			results = append(results, RetentionResult{Table: "audit_log", Deleted: -1})
		}

		// sessions + session_messages
		days = retentionDays(cfg.Retention.Sessions, 30)
		if err := cleanupSessions(dbPath, days); err != nil {
			results = append(results, RetentionResult{Table: "sessions", Error: err.Error()})
		} else {
			results = append(results, RetentionResult{Table: "sessions", Deleted: -1})
		}

		// offline_queue
		days = retentionDays(cfg.Retention.Queue, 7)
		cleanupOldQueueItems(dbPath, days)
		results = append(results, RetentionResult{Table: "offline_queue", Deleted: -1})

		// workflow_runs
		days = retentionDays(cfg.Retention.Workflows, 90)
		if n, err := cleanupWorkflowRuns(dbPath, days); err != nil {
			results = append(results, RetentionResult{Table: "workflow_runs", Error: err.Error()})
		} else {
			results = append(results, RetentionResult{Table: "workflow_runs", Deleted: n})
		}

		// handoffs + agent_messages
		days = retentionDays(cfg.Retention.Handoffs, 60)
		if n, err := cleanupHandoffs(dbPath, days); err != nil {
			results = append(results, RetentionResult{Table: "handoffs", Error: err.Error()})
		} else {
			results = append(results, RetentionResult{Table: "handoffs", Deleted: n})
		}

		// reflections
		days = retentionDays(cfg.Retention.Reflections, 60)
		if n, err := cleanupReflections(dbPath, days); err != nil {
			results = append(results, RetentionResult{Table: "reflections", Error: err.Error()})
		} else {
			results = append(results, RetentionResult{Table: "reflections", Deleted: n})
		}

		// sla_checks
		days = retentionDays(cfg.Retention.SLA, 90)
		if n, err := cleanupSLAChecks(dbPath, days); err != nil {
			results = append(results, RetentionResult{Table: "sla_checks", Error: err.Error()})
		} else {
			results = append(results, RetentionResult{Table: "sla_checks", Deleted: n})
		}

		// trust_events
		days = retentionDays(cfg.Retention.TrustEvents, 90)
		if n, err := cleanupTrustEvents(dbPath, days); err != nil {
			results = append(results, RetentionResult{Table: "trust_events", Error: err.Error()})
		} else {
			results = append(results, RetentionResult{Table: "trust_events", Deleted: n})
		}

		// config_versions
		days = retentionDays(cfg.Retention.Versions, 180)
		cleanupVersions(dbPath, days)
		results = append(results, RetentionResult{Table: "config_versions", Deleted: -1})
	}

	// Output files
	days := retentionDays(cfg.Retention.Outputs, 30)
	cleanupOutputs(cfg.baseDir, days)
	results = append(results, RetentionResult{Table: "outputs", Deleted: -1})

	// Upload files
	days = retentionDays(cfg.Retention.Uploads, 7)
	cleanupUploads(filepath.Join(cfg.baseDir, "uploads"), days)
	results = append(results, RetentionResult{Table: "uploads", Deleted: -1})

	// Log files
	days = retentionDays(cfg.Retention.Logs, 14)
	logDir := filepath.Join(cfg.baseDir, "logs")
	n := cleanupLogFiles(logDir, days)
	results = append(results, RetentionResult{Table: "log_files", Deleted: n})

	// Stale memory archival
	days = retentionDays(cfg.Retention.Memory, 30)
	if memArchived, err := cleanupStaleMemory(cfg, days); err != nil {
		results = append(results, RetentionResult{Table: "memory", Error: err.Error()})
	} else {
		results = append(results, RetentionResult{Table: "memory", Deleted: memArchived})
	}

	logInfo("retention cleanup completed", "tables", len(results))
	return results
}

// --- PII Redaction ---

// compilePIIPatterns compiles regex patterns for PII detection.
func compilePIIPatterns(patterns []string) []*regexp.Regexp {
	var compiled []*regexp.Regexp
	for _, p := range patterns {
		if re, err := regexp.Compile(p); err == nil {
			compiled = append(compiled, re)
		} else {
			logWarn("invalid PII pattern, skipping", "pattern", p, "error", err)
		}
	}
	return compiled
}

// redactPII replaces all PII pattern matches with [REDACTED].
func redactPII(text string, patterns []*regexp.Regexp) string {
	if len(patterns) == 0 || text == "" {
		return text
	}
	for _, re := range patterns {
		text = re.ReplaceAllString(text, "[REDACTED]")
	}
	return text
}

// --- Retention Stats ---

// queryRetentionStats returns row counts per table.
func queryRetentionStats(dbPath string) map[string]int {
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
		rows, err := queryDB(dbPath, sql)
		if err == nil && len(rows) > 0 {
			stats[t] = jsonInt(rows[0]["cnt"])
		}
	}
	return stats
}

// --- Data Export ---

type DataExport struct {
	ExportedAt string           `json:"exportedAt"`
	History    []JobRun         `json:"history"`
	Sessions   []Session        `json:"sessions"`
	Memory     []MemoryEntry    `json:"memory"`
	AuditLog   []AuditEntry     `json:"auditLog"`
	Reflections []ReflectionRow `json:"reflections,omitempty"`
}

// ReflectionRow is a simplified reflection entry for export.
type ReflectionRow struct {
	TaskID      string  `json:"taskId"`
	Agent        string  `json:"agent"`
	Score       int     `json:"score"`
	Feedback    string  `json:"feedback"`
	Improvement string  `json:"improvement"`
	CostUSD     float64 `json:"costUsd"`
	CreatedAt   string  `json:"createdAt"`
}

// exportData exports all user data as JSON (GDPR right of access).
func exportData(cfg *Config) ([]byte, error) {
	dbPath := cfg.HistoryDB
	if dbPath == "" {
		return json.Marshal(DataExport{ExportedAt: time.Now().UTC().Format(time.RFC3339)})
	}

	export := DataExport{
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
	}

	// History
	if runs, err := queryHistory(dbPath, "", 10000); err == nil {
		export.History = runs
	}

	// Sessions
	if sessions, _, err := querySessions(dbPath, SessionQuery{Limit: 10000}); err == nil {
		export.Sessions = sessions
	}

	// Memory
	if entries, err := listMemory(cfg, ""); err == nil {
		export.Memory = entries
	}

	// Audit log
	if entries, _, err := queryAuditLog(dbPath, 10000, 0); err == nil {
		export.AuditLog = entries
	}

	// Reflections
	export.Reflections = queryReflectionsForExport(dbPath)

	return json.MarshalIndent(export, "", "  ")
}

// queryReflectionsForExport queries all reflections for data export.
func queryReflectionsForExport(dbPath string) []ReflectionRow {
	if dbPath == "" {
		return nil
	}
	sql := `SELECT task_id, agent, score, feedback, improvement, cost_usd, created_at
	        FROM reflections ORDER BY created_at DESC LIMIT 10000`
	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return nil
	}
	var refs []ReflectionRow
	for _, row := range rows {
		refs = append(refs, ReflectionRow{
			TaskID:      jsonStr(row["task_id"]),
			Agent:        jsonStr(row["agent"]),
			Score:       jsonInt(row["score"]),
			Feedback:    jsonStr(row["feedback"]),
			Improvement: jsonStr(row["improvement"]),
			CostUSD:     jsonFloat(row["cost_usd"]),
			CreatedAt:   jsonStr(row["created_at"]),
		})
	}
	return refs
}

// --- Data Purge ---

// purgeDataBefore deletes all data before the given date across all tables.
func purgeDataBefore(cfg *Config, before string) ([]RetentionResult, error) {
	dbPath := cfg.HistoryDB
	if dbPath == "" {
		return nil, fmt.Errorf("no database configured")
	}

	type purgeTarget struct {
		table     string
		timeCol   string
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

	var results []RetentionResult
	for _, t := range targets {
		// Count first.
		countSQL := fmt.Sprintf(
			`SELECT COUNT(*) as cnt FROM %s WHERE datetime(%s) < datetime('%s')`,
			t.table, t.timeCol, escapeSQLite(before))
		rows, err := queryDB(dbPath, countSQL)
		count := 0
		if err == nil && len(rows) > 0 {
			count = jsonInt(rows[0]["cnt"])
		}

		// Delete.
		delSQL := fmt.Sprintf(
			`DELETE FROM %s WHERE datetime(%s) < datetime('%s')`,
			t.table, t.timeCol, escapeSQLite(before))
		cmd := exec.Command("sqlite3", dbPath, delSQL)
		if out, err := cmd.CombinedOutput(); err != nil {
			results = append(results, RetentionResult{
				Table: t.table, Error: fmt.Sprintf("%s: %s", err, string(out)),
			})
		} else {
			results = append(results, RetentionResult{Table: t.table, Deleted: count})
		}
	}

	// VACUUM to reclaim space.
	cmd := exec.Command("sqlite3", dbPath, "VACUUM;")
	cmd.CombinedOutput() // best effort

	return results, nil
}
