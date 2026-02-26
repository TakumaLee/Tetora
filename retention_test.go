package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

// --- retentionDays ---

func TestRetentionDays(t *testing.T) {
	if retentionDays(0, 90) != 90 {
		t.Error("expected fallback 90")
	}
	if retentionDays(30, 90) != 30 {
		t.Error("expected configured 30")
	}
	if retentionDays(-1, 14) != 14 {
		t.Error("expected fallback for negative")
	}
	if retentionDays(365, 90) != 365 {
		t.Error("expected configured 365")
	}
}

// --- PII Redaction ---

func TestCompilePIIPatterns(t *testing.T) {
	patterns := compilePIIPatterns([]string{
		`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b`, // email
		`\b\d{3}-\d{2}-\d{4}\b`,                                 // SSN
		`invalid[`,                                                // invalid regex
	})
	if len(patterns) != 2 {
		t.Errorf("expected 2 compiled patterns, got %d", len(patterns))
	}
}

func TestCompilePIIPatternsEmpty(t *testing.T) {
	patterns := compilePIIPatterns(nil)
	if patterns != nil {
		t.Error("expected nil for empty input")
	}
}

func TestRedactPII(t *testing.T) {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b`),
		regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`),
	}

	tests := []struct {
		input, expected string
	}{
		{"contact user@example.com for details", "contact [REDACTED] for details"},
		{"SSN: 123-45-6789", "SSN: [REDACTED]"},
		{"no PII here", "no PII here"},
		{"", ""},
		{"email test@test.org and 999-88-7777", "email [REDACTED] and [REDACTED]"},
	}

	for _, tt := range tests {
		result := redactPII(tt.input, patterns)
		if result != tt.expected {
			t.Errorf("redactPII(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestRedactPIINoPatterns(t *testing.T) {
	result := redactPII("user@example.com", nil)
	if result != "user@example.com" {
		t.Error("expected no change with nil patterns")
	}
}

// --- Helper: create test DB ---

func createRetentionTestDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// Create all tables.
	sql := `
CREATE TABLE IF NOT EXISTS job_runs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  job_id TEXT NOT NULL, name TEXT NOT NULL, source TEXT NOT NULL DEFAULT '',
  started_at TEXT NOT NULL, finished_at TEXT NOT NULL,
  status TEXT NOT NULL, exit_code INTEGER DEFAULT 0,
  cost_usd REAL DEFAULT 0, output_summary TEXT DEFAULT '',
  error TEXT DEFAULT '', model TEXT DEFAULT '',
  session_id TEXT DEFAULT '', output_file TEXT DEFAULT '',
  tokens_in INTEGER DEFAULT 0, tokens_out INTEGER DEFAULT 0,
  agent TEXT DEFAULT '', parent_id TEXT DEFAULT ''
);
CREATE TABLE IF NOT EXISTS audit_log (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  timestamp TEXT NOT NULL, action TEXT NOT NULL,
  source TEXT NOT NULL DEFAULT '', detail TEXT DEFAULT '', ip TEXT DEFAULT ''
);
CREATE TABLE IF NOT EXISTS sessions (
  id TEXT PRIMARY KEY, agent TEXT NOT NULL DEFAULT '',
  source TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'active',
  title TEXT NOT NULL DEFAULT '', total_cost REAL DEFAULT 0,
  total_tokens_in INTEGER DEFAULT 0, total_tokens_out INTEGER DEFAULT 0,
  message_count INTEGER DEFAULT 0, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS session_messages (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id TEXT NOT NULL, role TEXT NOT NULL DEFAULT 'system',
  content TEXT NOT NULL DEFAULT '', cost_usd REAL DEFAULT 0,
  tokens_in INTEGER DEFAULT 0, tokens_out INTEGER DEFAULT 0,
  model TEXT DEFAULT '', task_id TEXT DEFAULT '', created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS workflow_runs (
  id TEXT PRIMARY KEY, workflow_name TEXT NOT NULL,
  status TEXT NOT NULL, started_at TEXT NOT NULL,
  finished_at TEXT DEFAULT '', duration_ms INTEGER DEFAULT 0,
  total_cost REAL DEFAULT 0, variables TEXT DEFAULT '{}',
  step_results TEXT DEFAULT '{}', error TEXT DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS handoffs (
  id TEXT PRIMARY KEY, workflow_run_id TEXT DEFAULT '',
  from_agent TEXT NOT NULL, to_agent TEXT NOT NULL,
  from_step_id TEXT DEFAULT '', to_step_id TEXT DEFAULT '',
  from_session_id TEXT DEFAULT '', to_session_id TEXT DEFAULT '',
  context TEXT DEFAULT '', instruction TEXT DEFAULT '',
  status TEXT NOT NULL DEFAULT 'pending', created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS agent_messages (
  id TEXT PRIMARY KEY, workflow_run_id TEXT DEFAULT '',
  from_agent TEXT NOT NULL, to_agent TEXT NOT NULL,
  type TEXT NOT NULL, content TEXT NOT NULL DEFAULT '',
  ref_id TEXT DEFAULT '', created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS reflections (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id TEXT NOT NULL, agent TEXT NOT NULL DEFAULT '',
  score INTEGER DEFAULT 0, feedback TEXT DEFAULT '',
  improvement TEXT DEFAULT '', cost_usd REAL DEFAULT 0,
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS sla_checks (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  agent TEXT NOT NULL, checked_at TEXT NOT NULL,
  success_rate REAL DEFAULT 0, p95_latency_ms INTEGER DEFAULT 0,
  violation INTEGER DEFAULT 0, detail TEXT DEFAULT ''
);
CREATE TABLE IF NOT EXISTS trust_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  agent TEXT NOT NULL, event_type TEXT NOT NULL,
  from_level TEXT DEFAULT '', to_level TEXT DEFAULT '',
  consecutive_success INTEGER DEFAULT 0,
  created_at TEXT NOT NULL, note TEXT DEFAULT ''
);
CREATE TABLE IF NOT EXISTS config_versions (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  entity_type TEXT NOT NULL, entity_name TEXT NOT NULL DEFAULT '',
  version INTEGER NOT NULL, content TEXT NOT NULL DEFAULT '{}',
  changed_by TEXT DEFAULT '', diff_summary TEXT DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS agent_memory (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  agent TEXT NOT NULL, key TEXT NOT NULL, value TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL, created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS offline_queue (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task_json TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'pending',
  retry_count INTEGER DEFAULT 0, error TEXT DEFAULT '',
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
`
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create test db: %s: %v", string(out), err)
	}
	return dbPath
}

func insertTestRow(t *testing.T, dbPath, sql string) {
	t.Helper()
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("insert: %s: %v", string(out), err)
	}
}

func countRows(t *testing.T, dbPath, table string) int {
	t.Helper()
	rows, err := queryDB(dbPath, fmt.Sprintf("SELECT COUNT(*) as cnt FROM %s", table))
	if err != nil || len(rows) == 0 {
		return 0
	}
	return jsonInt(rows[0]["cnt"])
}

// --- Cleanup Functions ---

func TestCleanupWorkflowRuns(t *testing.T) {
	dbPath := createRetentionTestDB(t)
	old := time.Now().AddDate(0, 0, -100).Format(time.RFC3339)
	recent := time.Now().Format(time.RFC3339)

	insertTestRow(t, dbPath, fmt.Sprintf(
		`INSERT INTO workflow_runs (id, workflow_name, status, started_at, created_at) VALUES ('old1','wf','done','%s','%s')`, old, old))
	insertTestRow(t, dbPath, fmt.Sprintf(
		`INSERT INTO workflow_runs (id, workflow_name, status, started_at, created_at) VALUES ('new1','wf','done','%s','%s')`, recent, recent))

	n, err := cleanupWorkflowRuns(dbPath, 30)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("expected 1 deleted, got %d", n)
	}
	if countRows(t, dbPath, "workflow_runs") != 1 {
		t.Error("expected 1 remaining")
	}
}

func TestCleanupHandoffs(t *testing.T) {
	dbPath := createRetentionTestDB(t)
	old := time.Now().AddDate(0, 0, -100).Format(time.RFC3339)
	recent := time.Now().Format(time.RFC3339)

	insertTestRow(t, dbPath, fmt.Sprintf(
		`INSERT INTO handoffs (id, from_agent, to_agent, status, created_at) VALUES ('h1','a','b','done','%s')`, old))
	insertTestRow(t, dbPath, fmt.Sprintf(
		`INSERT INTO agent_messages (id, from_agent, to_agent, type, content, created_at) VALUES ('m1','a','b','note','hi','%s')`, old))
	insertTestRow(t, dbPath, fmt.Sprintf(
		`INSERT INTO handoffs (id, from_agent, to_agent, status, created_at) VALUES ('h2','a','b','done','%s')`, recent))

	n, err := cleanupHandoffs(dbPath, 30)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("expected 1 handoff deleted, got %d", n)
	}
	if countRows(t, dbPath, "handoffs") != 1 {
		t.Error("expected 1 handoff remaining")
	}
	if countRows(t, dbPath, "agent_messages") != 0 {
		t.Error("expected 0 agent_messages remaining")
	}
}

func TestCleanupReflections(t *testing.T) {
	dbPath := createRetentionTestDB(t)
	old := time.Now().AddDate(0, 0, -100).Format(time.RFC3339)
	recent := time.Now().Format(time.RFC3339)

	insertTestRow(t, dbPath, fmt.Sprintf(
		`INSERT INTO reflections (task_id, agent, score, created_at) VALUES ('t1','r1',4,'%s')`, old))
	insertTestRow(t, dbPath, fmt.Sprintf(
		`INSERT INTO reflections (task_id, agent, score, created_at) VALUES ('t2','r1',5,'%s')`, recent))

	n, err := cleanupReflections(dbPath, 30)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("expected 1 deleted, got %d", n)
	}
	if countRows(t, dbPath, "reflections") != 1 {
		t.Error("expected 1 remaining")
	}
}

func TestCleanupSLAChecks(t *testing.T) {
	dbPath := createRetentionTestDB(t)
	old := time.Now().AddDate(0, 0, -100).Format(time.RFC3339)
	recent := time.Now().Format(time.RFC3339)

	insertTestRow(t, dbPath, fmt.Sprintf(
		`INSERT INTO sla_checks (agent, checked_at) VALUES ('r1','%s')`, old))
	insertTestRow(t, dbPath, fmt.Sprintf(
		`INSERT INTO sla_checks (agent, checked_at) VALUES ('r1','%s')`, recent))

	n, err := cleanupSLAChecks(dbPath, 30)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("expected 1 deleted, got %d", n)
	}
}

func TestCleanupTrustEvents(t *testing.T) {
	dbPath := createRetentionTestDB(t)
	old := time.Now().AddDate(0, 0, -100).Format(time.RFC3339)
	recent := time.Now().Format(time.RFC3339)

	insertTestRow(t, dbPath, fmt.Sprintf(
		`INSERT INTO trust_events (agent, event_type, created_at) VALUES ('r1','promote','%s')`, old))
	insertTestRow(t, dbPath, fmt.Sprintf(
		`INSERT INTO trust_events (agent, event_type, created_at) VALUES ('r1','promote','%s')`, recent))

	n, err := cleanupTrustEvents(dbPath, 30)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("expected 1 deleted, got %d", n)
	}
}

func TestCleanupEmptyDB(t *testing.T) {
	// All cleanup functions should handle empty/missing DB gracefully.
	n, err := cleanupWorkflowRuns("", 30)
	if err != nil || n != 0 {
		t.Error("expected no-op for empty path")
	}
	n, err = cleanupHandoffs("", 30)
	if err != nil || n != 0 {
		t.Error("expected no-op for empty path")
	}
	n, err = cleanupReflections("", 30)
	if err != nil || n != 0 {
		t.Error("expected no-op for empty path")
	}
	n, err = cleanupSLAChecks("", 30)
	if err != nil || n != 0 {
		t.Error("expected no-op for empty path")
	}
	n, err = cleanupTrustEvents("", 30)
	if err != nil || n != 0 {
		t.Error("expected no-op for empty path")
	}
}

func TestCleanupZeroDays(t *testing.T) {
	n, _ := cleanupWorkflowRuns("/tmp/test.db", 0)
	if n != 0 {
		t.Error("expected 0 for zero days")
	}
	n, _ = cleanupWorkflowRuns("/tmp/test.db", -1)
	if n != 0 {
		t.Error("expected 0 for negative days")
	}
}

// --- Log File Cleanup ---

func TestCleanupLogFiles(t *testing.T) {
	dir := t.TempDir()

	// Create some log files.
	os.WriteFile(filepath.Join(dir, "tetora.log"), []byte("current"), 0o644)
	os.WriteFile(filepath.Join(dir, "tetora.log.1"), []byte("recent"), 0o644)
	os.WriteFile(filepath.Join(dir, "tetora.log.2"), []byte("old"), 0o644)

	// Make .2 old.
	oldTime := time.Now().AddDate(0, 0, -30)
	os.Chtimes(filepath.Join(dir, "tetora.log.2"), oldTime, oldTime)

	removed := cleanupLogFiles(dir, 14)
	if removed != 1 {
		t.Errorf("expected 1 removed, got %d", removed)
	}

	// Current log should not be touched.
	if _, err := os.Stat(filepath.Join(dir, "tetora.log")); err != nil {
		t.Error("current log should still exist")
	}
	// Recent rotated should still exist.
	if _, err := os.Stat(filepath.Join(dir, "tetora.log.1")); err != nil {
		t.Error("recent rotated log should still exist")
	}
}

func TestCleanupLogFilesEmptyDir(t *testing.T) {
	n := cleanupLogFiles("", 14)
	if n != 0 {
		t.Error("expected 0 for empty dir")
	}
	n = cleanupLogFiles("/nonexistent", 14)
	if n != 0 {
		t.Error("expected 0 for nonexistent dir")
	}
}

// --- Retention Stats ---

func TestQueryRetentionStats(t *testing.T) {
	dbPath := createRetentionTestDB(t)

	now := time.Now().Format(time.RFC3339)
	insertTestRow(t, dbPath, fmt.Sprintf(
		`INSERT INTO job_runs (job_id, name, source, started_at, finished_at, status) VALUES ('j1','test','cli','%s','%s','success')`, now, now))
	insertTestRow(t, dbPath, fmt.Sprintf(
		`INSERT INTO audit_log (timestamp, action) VALUES ('%s','test')`, now))
	insertTestRow(t, dbPath, fmt.Sprintf(
		`INSERT INTO reflections (task_id, agent, created_at) VALUES ('t1','r1','%s')`, now))

	stats := queryRetentionStats(dbPath)
	if stats["job_runs"] != 1 {
		t.Errorf("expected 1 job_run, got %d", stats["job_runs"])
	}
	if stats["audit_log"] != 1 {
		t.Errorf("expected 1 audit_log, got %d", stats["audit_log"])
	}
	if stats["reflections"] != 1 {
		t.Errorf("expected 1 reflection, got %d", stats["reflections"])
	}
}

func TestQueryRetentionStatsEmptyDB(t *testing.T) {
	stats := queryRetentionStats("")
	if len(stats) != 0 {
		t.Error("expected empty stats for empty path")
	}
}

// --- Data Export ---

func TestExportData(t *testing.T) {
	dbPath := createRetentionTestDB(t)
	now := time.Now().Format(time.RFC3339)

	insertTestRow(t, dbPath, fmt.Sprintf(
		`INSERT INTO job_runs (job_id, name, source, started_at, finished_at, status) VALUES ('j1','test','cli','%s','%s','success')`, now, now))
	insertTestRow(t, dbPath, fmt.Sprintf(
		`INSERT INTO audit_log (timestamp, action) VALUES ('%s','test')`, now))

	cfg := &Config{HistoryDB: dbPath}
	data, err := exportData(cfg)
	if err != nil {
		t.Fatal(err)
	}

	var export DataExport
	if err := json.Unmarshal(data, &export); err != nil {
		t.Fatal(err)
	}
	if export.ExportedAt == "" {
		t.Error("expected exportedAt")
	}
	if len(export.History) != 1 {
		t.Errorf("expected 1 history record, got %d", len(export.History))
	}
	if len(export.AuditLog) != 1 {
		t.Errorf("expected 1 audit record, got %d", len(export.AuditLog))
	}
}

func TestExportDataNoDBPath(t *testing.T) {
	cfg := &Config{}
	data, err := exportData(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var export DataExport
	if err := json.Unmarshal(data, &export); err != nil {
		t.Fatal(err)
	}
	if export.ExportedAt == "" {
		t.Error("expected exportedAt even with no DB")
	}
}

// --- Data Purge ---

func TestPurgeDataBefore(t *testing.T) {
	dbPath := createRetentionTestDB(t)
	old := "2024-01-01T00:00:00Z"
	recent := time.Now().Format(time.RFC3339)

	insertTestRow(t, dbPath, fmt.Sprintf(
		`INSERT INTO job_runs (job_id, name, source, started_at, finished_at, status) VALUES ('j1','old','cli','%s','%s','success')`, old, old))
	insertTestRow(t, dbPath, fmt.Sprintf(
		`INSERT INTO job_runs (job_id, name, source, started_at, finished_at, status) VALUES ('j2','new','cli','%s','%s','success')`, recent, recent))
	insertTestRow(t, dbPath, fmt.Sprintf(
		`INSERT INTO audit_log (timestamp, action) VALUES ('%s','old')`, old))
	insertTestRow(t, dbPath, fmt.Sprintf(
		`INSERT INTO reflections (task_id, agent, created_at) VALUES ('t1','r1','%s')`, old))

	results, err := purgeDataBefore(&Config{HistoryDB: dbPath}, "2025-01-01")
	if err != nil {
		t.Fatal(err)
	}

	// Check results.
	for _, r := range results {
		if r.Error != "" {
			t.Errorf("table %s error: %s", r.Table, r.Error)
		}
	}

	// Old job should be deleted, new should remain.
	if countRows(t, dbPath, "job_runs") != 1 {
		t.Errorf("expected 1 job_run remaining, got %d", countRows(t, dbPath, "job_runs"))
	}
	if countRows(t, dbPath, "audit_log") != 0 {
		t.Errorf("expected 0 audit_log remaining, got %d", countRows(t, dbPath, "audit_log"))
	}
	if countRows(t, dbPath, "reflections") != 0 {
		t.Errorf("expected 0 reflections remaining, got %d", countRows(t, dbPath, "reflections"))
	}
}

func TestPurgeDataBeforeNoDBPath(t *testing.T) {
	_, err := purgeDataBefore(&Config{}, "2025-01-01")
	if err == nil {
		t.Error("expected error for empty DB path")
	}
}

// --- runRetention ---

func TestRunRetention(t *testing.T) {
	dbPath := createRetentionTestDB(t)
	dir := t.TempDir()

	cfg := &Config{
		HistoryDB: dbPath,
		baseDir:   dir,
		Retention: RetentionConfig{
			History:     30,
			Sessions:    15,
			AuditLog:    90,
			Workflows:   30,
			Reflections: 30,
			SLA:         30,
			TrustEvents: 30,
			Handoffs:    30,
			Queue:       3,
			Versions:    60,
			Outputs:     14,
			Uploads:     3,
			Logs:        7,
		},
	}

	// Create dirs for outputs/uploads/logs.
	os.MkdirAll(filepath.Join(dir, "outputs"), 0o755)
	os.MkdirAll(filepath.Join(dir, "uploads"), 0o755)
	os.MkdirAll(filepath.Join(dir, "logs"), 0o755)

	results := runRetention(cfg)
	if len(results) == 0 {
		t.Error("expected results from runRetention")
	}

	// Check all tables are covered.
	tables := make(map[string]bool)
	for _, r := range results {
		tables[r.Table] = true
	}

	expected := []string{"job_runs", "audit_log", "sessions", "offline_queue",
		"workflow_runs", "handoffs", "reflections", "sla_checks",
		"trust_events", "config_versions", "outputs", "uploads", "log_files"}
	for _, e := range expected {
		if !tables[e] {
			t.Errorf("missing result for table: %s", e)
		}
	}
}

func TestRunRetentionDefaults(t *testing.T) {
	dbPath := createRetentionTestDB(t)
	dir := t.TempDir()

	// Empty retention config → all defaults.
	cfg := &Config{
		HistoryDB: dbPath,
		baseDir:   dir,
	}
	os.MkdirAll(filepath.Join(dir, "outputs"), 0o755)
	os.MkdirAll(filepath.Join(dir, "uploads"), 0o755)
	os.MkdirAll(filepath.Join(dir, "logs"), 0o755)

	results := runRetention(cfg)
	if len(results) == 0 {
		t.Error("expected results even with default config")
	}
}
