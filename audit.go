package main

import (
	"fmt"
	"strings"
	"time"
)

// --- Audit Log ---

// auditEntry holds a single audit log entry for the batched writer.
type auditEntry struct {
	dbPath string
	ts     string
	action string
	source string
	detail string
	ip     string
}

// auditChan is a buffered channel for non-blocking audit log writes.
// The single auditWriter goroutine drains this channel and batches
// inserts into one sqlite3 call, eliminating "database is locked" errors
// from concurrent fire-and-forget goroutines.
var auditChan = make(chan auditEntry, 256)

// startAuditWriter starts the background goroutine that drains auditChan
// and writes entries in batches. Call once at startup.
func startAuditWriter() {
	go auditWriter()
}

func auditWriter() {
	// Batch window: collect entries for up to 500ms or 50 entries, whichever comes first.
	const maxBatch = 50
	const flushInterval = 500 * time.Millisecond

	buf := make([]auditEntry, 0, maxBatch)
	timer := time.NewTimer(flushInterval)
	defer timer.Stop()

	for {
		select {
		case entry, ok := <-auditChan:
			if !ok {
				// Channel closed — flush remaining and exit.
				if len(buf) > 0 {
					auditFlush(buf)
				}
				return
			}
			buf = append(buf, entry)
			if len(buf) >= maxBatch {
				auditFlush(buf)
				buf = buf[:0]
				timer.Reset(flushInterval)
			}
		case <-timer.C:
			if len(buf) > 0 {
				auditFlush(buf)
				buf = buf[:0]
			}
			timer.Reset(flushInterval)
		}
	}
}

// auditFlush writes a batch of audit entries in a single sqlite3 call.
func auditFlush(entries []auditEntry) {
	if len(entries) == 0 {
		return
	}
	// Group by dbPath (almost always the same, but be safe).
	byDB := make(map[string][]auditEntry)
	for _, e := range entries {
		byDB[e.dbPath] = append(byDB[e.dbPath], e)
	}
	for dbPath, batch := range byDB {
		var stmts []string
		for _, e := range batch {
			stmts = append(stmts, fmt.Sprintf(
				`INSERT INTO audit_log (timestamp, action, source, detail, ip) VALUES ('%s','%s','%s','%s','%s')`,
				e.ts, e.action, e.source, e.detail, e.ip,
			))
		}
		sql := strings.Join(stmts, ";\n")
		if err := execDB(dbPath, sql); err != nil {
			logError("audit log batch insert failed", "count", len(batch), "error", err)
		}
	}
}

// initAuditLog creates the audit_log table if it doesn't exist.
func initAuditLog(dbPath string) error {
	sql := `CREATE TABLE IF NOT EXISTS audit_log (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  timestamp TEXT NOT NULL,
  action TEXT NOT NULL,
  source TEXT NOT NULL DEFAULT '',
  detail TEXT DEFAULT '',
  ip TEXT DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_audit_log_ts ON audit_log(timestamp);
CREATE INDEX IF NOT EXISTS idx_audit_log_action ON audit_log(action);`

	return execDB(dbPath, sql)
}

// auditLog records an action to the audit_log table.
// Non-blocking: entries are queued to the batched audit writer.
func auditLog(dbPath, action, source, detail, ip string) {
	if dbPath == "" {
		return
	}
	select {
	case auditChan <- auditEntry{
		dbPath: dbPath,
		ts:     escapeSQLite(time.Now().UTC().Format(time.RFC3339)),
		action: escapeSQLite(action),
		source: escapeSQLite(source),
		detail: escapeSQLite(truncateStr(detail, 500)),
		ip:     escapeSQLite(ip),
	}:
	default:
		// Channel full — drop entry rather than block the caller.
		logWarn("audit log queue full, dropping entry", "action", action)
	}
}

// AuditEntry represents a row in the audit_log table.
type AuditEntry struct {
	ID        int    `json:"id"`
	Timestamp string `json:"timestamp"`
	Action    string `json:"action"`
	Source    string `json:"source"`
	Detail    string `json:"detail"`
	IP        string `json:"ip"`
}

// queryAuditLog returns recent audit log entries.
func queryAuditLog(dbPath string, limit, offset int) ([]AuditEntry, int, error) {
	if limit <= 0 {
		limit = 50
	}

	// Count total.
	countSQL := "SELECT COUNT(*) as cnt FROM audit_log"
	countRows, err := queryDB(dbPath, countSQL)
	if err != nil {
		return nil, 0, err
	}
	total := 0
	if len(countRows) > 0 {
		total = jsonInt(countRows[0]["cnt"])
	}

	// Query entries.
	sql := fmt.Sprintf(
		`SELECT id, timestamp, action, source, detail, ip
		 FROM audit_log ORDER BY id DESC LIMIT %d OFFSET %d`,
		limit, offset)
	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return nil, 0, err
	}

	var entries []AuditEntry
	for _, row := range rows {
		entries = append(entries, AuditEntry{
			ID:        jsonInt(row["id"]),
			Timestamp: jsonStr(row["timestamp"]),
			Action:    jsonStr(row["action"]),
			Source:    jsonStr(row["source"]),
			Detail:    jsonStr(row["detail"]),
			IP:        jsonStr(row["ip"]),
		})
	}
	return entries, total, nil
}

// --- Routing Stats ---

// RoutingHistoryEntry represents a parsed route.dispatch audit log entry.
type RoutingHistoryEntry struct {
	ID         int    `json:"id"`
	Timestamp  string `json:"timestamp"`
	Source     string `json:"source"`
	Role       string `json:"role"`
	Method     string `json:"method"`
	Confidence string `json:"confidence"`
	Prompt     string `json:"prompt"`
}

// RoleRoutingStats aggregates routing stats for a single role.
type RoleRoutingStats struct {
	Total int `json:"total"`
}

// parseRouteDetail extracts role, method, confidence, and prompt from the detail field.
// Format: "role=X method=Y confidence=Z prompt=..."
func parseRouteDetail(detail string) (role, method, confidence, prompt string) {
	// Parse key=value pairs from the detail string.
	// The prompt field may contain spaces, so we handle it specially.
	parts := strings.SplitN(detail, " prompt=", 2)
	if len(parts) == 2 {
		prompt = parts[1]
	}

	kvPart := parts[0]
	for _, token := range strings.Fields(kvPart) {
		if strings.HasPrefix(token, "role=") {
			role = strings.TrimPrefix(token, "role=")
		} else if strings.HasPrefix(token, "method=") {
			method = strings.TrimPrefix(token, "method=")
		} else if strings.HasPrefix(token, "confidence=") {
			confidence = strings.TrimPrefix(token, "confidence=")
		}
	}
	return
}

// queryRoutingStats queries audit_log for route.dispatch events and returns
// a list of routing history entries and per-role stats.
func queryRoutingStats(dbPath string, limit int) ([]RoutingHistoryEntry, map[string]*RoleRoutingStats, error) {
	if limit <= 0 {
		limit = 50
	}

	sql := fmt.Sprintf(
		`SELECT id, timestamp, source, detail
		 FROM audit_log WHERE action='route.dispatch'
		 ORDER BY id DESC LIMIT %d`,
		limit)
	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return nil, nil, err
	}

	var history []RoutingHistoryEntry
	byRole := make(map[string]*RoleRoutingStats)

	for _, row := range rows {
		detail := jsonStr(row["detail"])
		role, method, confidence, prompt := parseRouteDetail(detail)

		history = append(history, RoutingHistoryEntry{
			ID:         jsonInt(row["id"]),
			Timestamp:  jsonStr(row["timestamp"]),
			Source:     jsonStr(row["source"]),
			Role:       role,
			Method:     method,
			Confidence: confidence,
			Prompt:     prompt,
		})

		if role != "" {
			stats, ok := byRole[role]
			if !ok {
				stats = &RoleRoutingStats{}
				byRole[role] = stats
			}
			stats.Total++
		}
	}

	return history, byRole, nil
}

// cleanupAuditLog removes entries older than the given number of days.
func cleanupAuditLog(dbPath string, days int) error {
	sql := fmt.Sprintf(
		`DELETE FROM audit_log WHERE datetime(timestamp) < datetime('now','-%d days')`, days)
	return execDB(dbPath, sql)
}
