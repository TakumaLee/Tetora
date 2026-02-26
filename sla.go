package main

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// --- SLA Config ---

// SLAConfig configures per-agent SLA monitoring.
type SLAConfig struct {
	Enabled       bool                  `json:"enabled,omitempty"`
	Agents         map[string]AgentSLACfg `json:"agents,omitempty"`
	CheckInterval string                `json:"checkInterval,omitempty"` // duration between checks (default "1h")
	Window        string                `json:"window,omitempty"`        // sliding window for metrics (default "24h")
}

// AgentSLACfg defines SLA thresholds for a single agent.
type AgentSLACfg struct {
	MinSuccessRate  float64 `json:"minSuccessRate,omitempty"`  // e.g. 0.95
	MaxP95LatencyMs int64   `json:"maxP95LatencyMs,omitempty"` // e.g. 60000
}

func (c SLAConfig) checkIntervalOrDefault() time.Duration {
	if c.CheckInterval != "" {
		if d, err := time.ParseDuration(c.CheckInterval); err == nil {
			return d
		}
	}
	return 1 * time.Hour
}

func (c SLAConfig) windowOrDefault() time.Duration {
	if c.Window != "" {
		if d, err := time.ParseDuration(c.Window); err == nil {
			return d
		}
	}
	return 24 * time.Hour
}

// --- SLA Metrics ---

// SLAMetrics holds computed SLA metrics for a single agent.
type SLAMetrics struct {
	Agent         string  `json:"agent"`
	Total        int     `json:"total"`
	Success      int     `json:"success"`
	Fail         int     `json:"fail"`
	SuccessRate  float64 `json:"successRate"`
	AvgLatencyMs int64   `json:"avgLatencyMs"`
	P95LatencyMs int64   `json:"p95LatencyMs"`
	TotalCost    float64 `json:"totalCost"`
	AvgCost      float64 `json:"avgCost"`
}

// SLAStatus holds SLA metrics plus violation status for an agent.
type SLAStatus struct {
	SLAMetrics
	Status    string `json:"status"`    // "ok", "warning", "violation"
	Violation string `json:"violation"` // description of violation, empty if ok
}

// SLACheckResult holds the result of a periodic SLA check.
type SLACheckResult struct {
	Agent        string  `json:"agent"`
	Timestamp   string  `json:"timestamp"`
	SuccessRate float64 `json:"successRate"`
	P95Latency  int64   `json:"p95LatencyMs"`
	Violation   bool    `json:"violation"`
	Detail      string  `json:"detail"`
}

// --- DB Init ---

func initSLADB(dbPath string) {
	// Add agent column to job_runs if not exists (legacy: was "role").
	migrate := `ALTER TABLE job_runs ADD COLUMN agent TEXT DEFAULT '';`
	cmd := exec.Command("sqlite3", dbPath, migrate)
	cmd.CombinedOutput() // ignore error if column already exists

	// Migration: rename role -> agent in job_runs and sla_checks.
	for _, stmt := range []string{
		`ALTER TABLE job_runs RENAME COLUMN role TO agent;`,
		`ALTER TABLE sla_checks RENAME COLUMN role TO agent;`,
	} {
		cmd := exec.Command("sqlite3", dbPath, stmt)
		cmd.CombinedOutput() // ignore errors (column may already be renamed or table may not exist)
	}

	// Create sla_checks table for check history.
	sql := `CREATE TABLE IF NOT EXISTS sla_checks (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  agent TEXT NOT NULL,
  checked_at TEXT NOT NULL,
  success_rate REAL DEFAULT 0,
  p95_latency_ms INTEGER DEFAULT 0,
  violation INTEGER DEFAULT 0,
  detail TEXT DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_sla_checks_agent ON sla_checks(agent);
CREATE INDEX IF NOT EXISTS idx_sla_checks_time ON sla_checks(checked_at);`
	cmd2 := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd2.CombinedOutput(); err != nil {
		logWarn("init sla_checks table failed", "error", fmt.Sprintf("%s: %s", err, out))
	}
}

// --- Query SLA Metrics ---

// querySLAMetrics computes SLA metrics for a single agent over a time window.
func querySLAMetrics(dbPath, role string, windowHours int) (*SLAMetrics, error) {
	if dbPath == "" {
		return &SLAMetrics{Agent: role}, nil
	}
	if windowHours <= 0 {
		windowHours = 24
	}

	// Aggregate query.
	sql := fmt.Sprintf(
		`SELECT
			COUNT(*) as total,
			COALESCE(SUM(CASE WHEN status = 'success' THEN 1 ELSE 0 END), 0) as success,
			COALESCE(SUM(CASE WHEN status != 'success' THEN 1 ELSE 0 END), 0) as fail,
			COALESCE(AVG(CAST(
				(julianday(finished_at) - julianday(started_at)) * 86400000 AS INTEGER
			)), 0) as avg_latency_ms,
			COALESCE(SUM(cost_usd), 0) as total_cost,
			COALESCE(AVG(cost_usd), 0) as avg_cost
		 FROM job_runs
		 WHERE agent = '%s'
		   AND datetime(started_at) >= datetime('now', '-%d hours')`,
		escapeSQLite(role), windowHours)

	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return nil, err
	}

	m := &SLAMetrics{Agent: role}
	if len(rows) > 0 {
		r := rows[0]
		m.Total = jsonInt(r["total"])
		m.Success = jsonInt(r["success"])
		m.Fail = jsonInt(r["fail"])
		m.AvgLatencyMs = int64(jsonFloat(r["avg_latency_ms"]))
		m.TotalCost = jsonFloat(r["total_cost"])
		m.AvgCost = jsonFloat(r["avg_cost"])
		if m.Total > 0 {
			m.SuccessRate = float64(m.Success) / float64(m.Total)
		}
	}

	// Compute P95 latency.
	m.P95LatencyMs = queryP95Latency(dbPath, role, windowHours)

	return m, nil
}

// queryP95Latency computes the 95th percentile latency for a role.
func queryP95Latency(dbPath, role string, windowHours int) int64 {
	sql := fmt.Sprintf(
		`SELECT CAST((julianday(finished_at) - julianday(started_at)) * 86400000 AS INTEGER) as latency_ms
		 FROM job_runs
		 WHERE agent = '%s'
		   AND datetime(started_at) >= datetime('now', '-%d hours')
		   AND status = 'success'
		 ORDER BY latency_ms`,
		escapeSQLite(role), windowHours)

	rows, err := queryDB(dbPath, sql)
	if err != nil || len(rows) == 0 {
		return 0
	}

	// Collect latencies.
	latencies := make([]int64, 0, len(rows))
	for _, r := range rows {
		latencies = append(latencies, int64(jsonFloat(r["latency_ms"])))
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

	// P95 index.
	idx := int(float64(len(latencies)) * 0.95)
	if idx >= len(latencies) {
		idx = len(latencies) - 1
	}
	return latencies[idx]
}

// querySLAAll computes SLA metrics for all configured roles.
func querySLAAll(dbPath string, roles []string, windowHours int) ([]SLAMetrics, error) {
	var metrics []SLAMetrics
	for _, role := range roles {
		m, err := querySLAMetrics(dbPath, role, windowHours)
		if err != nil {
			return nil, err
		}
		metrics = append(metrics, *m)
	}
	return metrics, nil
}

// querySLAStatusAll returns SLA status (with violation checks) for all roles.
func querySLAStatusAll(cfg *Config) ([]SLAStatus, error) {
	window := cfg.SLA.windowOrDefault()
	windowHours := int(window.Hours())
	if windowHours <= 0 {
		windowHours = 24
	}

	// Collect all role names.
	roles := make([]string, 0, len(cfg.Agents))
	for name := range cfg.Agents {
		roles = append(roles, name)
	}
	sort.Strings(roles)

	var statuses []SLAStatus
	for _, role := range roles {
		m, err := querySLAMetrics(cfg.HistoryDB, role, windowHours)
		if err != nil {
			return nil, err
		}

		s := SLAStatus{SLAMetrics: *m, Status: "ok"}

		// Check thresholds.
		if roleCfg, ok := cfg.SLA.Agents[role]; ok {
			var violations []string
			if roleCfg.MinSuccessRate > 0 && m.Total > 0 && m.SuccessRate < roleCfg.MinSuccessRate {
				violations = append(violations,
					fmt.Sprintf("success rate %.1f%% < %.1f%%", m.SuccessRate*100, roleCfg.MinSuccessRate*100))
			}
			if roleCfg.MaxP95LatencyMs > 0 && m.P95LatencyMs > roleCfg.MaxP95LatencyMs {
				violations = append(violations,
					fmt.Sprintf("p95 latency %dms > %dms", m.P95LatencyMs, roleCfg.MaxP95LatencyMs))
			}
			if len(violations) > 0 {
				s.Status = "violation"
				s.Violation = strings.Join(violations, "; ")
			} else if m.Total > 0 && roleCfg.MinSuccessRate > 0 && m.SuccessRate < roleCfg.MinSuccessRate+0.05 {
				// Warning: within 5% of threshold.
				s.Status = "warning"
				s.Violation = fmt.Sprintf("success rate %.1f%% approaching threshold %.1f%%",
					m.SuccessRate*100, roleCfg.MinSuccessRate*100)
			}
		}

		statuses = append(statuses, s)
	}
	return statuses, nil
}

// --- SLA Check (Periodic) ---

// checkSLAViolations runs a periodic SLA check and notifies on violations.
func checkSLAViolations(cfg *Config, notifyFn func(string)) {
	if !cfg.SLA.Enabled || cfg.HistoryDB == "" {
		return
	}

	window := cfg.SLA.windowOrDefault()
	windowHours := int(window.Hours())
	if windowHours <= 0 {
		windowHours = 24
	}

	for role, roleCfg := range cfg.SLA.Agents {
		m, err := querySLAMetrics(cfg.HistoryDB, role, windowHours)
		if err != nil {
			logWarn("SLA check query failed", "agent", role, "error", err)
			continue
		}

		if m.Total == 0 {
			continue // no data, skip
		}

		var violations []string
		if roleCfg.MinSuccessRate > 0 && m.SuccessRate < roleCfg.MinSuccessRate {
			violations = append(violations,
				fmt.Sprintf("success rate %.1f%% < %.1f%%", m.SuccessRate*100, roleCfg.MinSuccessRate*100))
		}
		if roleCfg.MaxP95LatencyMs > 0 && m.P95LatencyMs > roleCfg.MaxP95LatencyMs {
			violations = append(violations,
				fmt.Sprintf("p95 latency %dms > %dms", m.P95LatencyMs, roleCfg.MaxP95LatencyMs))
		}

		isViolation := len(violations) > 0
		detail := ""
		if isViolation {
			detail = strings.Join(violations, "; ")
		}

		// Record check result.
		recordSLACheck(cfg.HistoryDB, SLACheckResult{
			Agent:       role,
			Timestamp:   time.Now().Format(time.RFC3339),
			SuccessRate: m.SuccessRate,
			P95Latency:  m.P95LatencyMs,
			Violation:   isViolation,
			Detail:      detail,
		})

		// Notify on violation.
		if isViolation && notifyFn != nil {
			msg := fmt.Sprintf("SLA Violation [%s]\n%s\n(%d tasks in %dh window, success: %d/%d, p95: %dms, cost: $%.2f)",
				role, detail, m.Total, windowHours, m.Success, m.Total, m.P95LatencyMs, m.TotalCost)
			notifyFn(msg)
		}
	}
}

// recordSLACheck stores a SLA check result in the database.
func recordSLACheck(dbPath string, r SLACheckResult) {
	if dbPath == "" {
		return
	}
	violationInt := 0
	if r.Violation {
		violationInt = 1
	}
	sql := fmt.Sprintf(
		`INSERT INTO sla_checks (agent, checked_at, success_rate, p95_latency_ms, violation, detail)
		 VALUES ('%s', '%s', %f, %d, %d, '%s')`,
		escapeSQLite(r.Agent),
		escapeSQLite(r.Timestamp),
		r.SuccessRate,
		r.P95Latency,
		violationInt,
		escapeSQLite(r.Detail))

	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		logWarn("record SLA check failed", "error", fmt.Sprintf("%s: %s", err, out))
	}
}

// querySLAHistory returns recent SLA check results for an agent.
func querySLAHistory(dbPath, role string, limit int) ([]SLACheckResult, error) {
	if dbPath == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 24
	}

	where := ""
	if role != "" {
		where = fmt.Sprintf("WHERE agent = '%s'", escapeSQLite(role))
	}

	sql := fmt.Sprintf(
		`SELECT agent, checked_at, success_rate, p95_latency_ms, violation, detail
		 FROM sla_checks %s ORDER BY id DESC LIMIT %d`, where, limit)

	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return nil, err
	}

	var results []SLACheckResult
	for _, r := range rows {
		results = append(results, SLACheckResult{
			Agent:       jsonStr(r["agent"]),
			Timestamp:   jsonStr(r["checked_at"]),
			SuccessRate: jsonFloat(r["success_rate"]),
			P95Latency:  int64(jsonFloat(r["p95_latency_ms"])),
			Violation:   jsonInt(r["violation"]) != 0,
			Detail:      jsonStr(r["detail"]),
		})
	}
	return results, nil
}

// --- SLA Ticker (for cron integration) ---

// slaChecker runs periodic SLA checks.
type slaChecker struct {
	cfg      *Config
	notifyFn func(string)
	lastRun  time.Time
}

func newSLAChecker(cfg *Config, notifyFn func(string)) *slaChecker {
	return &slaChecker{
		cfg:      cfg,
		notifyFn: notifyFn,
	}
}

// tick is called periodically (e.g. from cron tick). Runs SLA check if enough time has passed.
func (s *slaChecker) tick(ctx context.Context) {
	if !s.cfg.SLA.Enabled {
		return
	}
	interval := s.cfg.SLA.checkIntervalOrDefault()
	if time.Since(s.lastRun) < interval {
		return
	}
	s.lastRun = time.Now()
	logDebugCtx(ctx, "running SLA check")
	checkSLAViolations(s.cfg, s.notifyFn)
}
