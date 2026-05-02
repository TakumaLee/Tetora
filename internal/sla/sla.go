// Package sla implements SLA monitoring: metrics computation, violation checking,
// and periodic check recording against a SQLite history database.
package sla

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"tetora/internal/config"
	"tetora/internal/db"
	tlog "tetora/internal/log"
)

// SLAConfig and AgentSLACfg are defined in config to avoid circular imports.
type SLAConfig = config.SLAConfig
type AgentSLACfg = config.AgentSLACfg

// --- Metric and status types ---

// SLAMetrics holds computed SLA metrics for a single agent.
type SLAMetrics struct {
	Agent        string  `json:"agent"`
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
	Agent       string  `json:"agent"`
	Timestamp   string  `json:"timestamp"`
	SuccessRate float64 `json:"successRate"`
	P95Latency  int64   `json:"p95LatencyMs"`
	Violation   bool    `json:"violation"`
	Detail      string  `json:"detail"`
}

// --- DB init ---

// InitSLADB ensures the sla_checks table exists and applies schema migrations.
func InitSLADB(dbPath string) {
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
		tlog.Warn("init sla_checks table failed", "error", fmt.Sprintf("%s: %s", err, out))
	}
}

// --- Query helpers ---

// QuerySLAMetrics computes SLA metrics for a single agent over a time window.
func QuerySLAMetrics(dbPath, agent string, windowHours int) (*SLAMetrics, error) {
	if dbPath == "" {
		return &SLAMetrics{Agent: agent}, nil
	}
	if windowHours <= 0 {
		windowHours = 24
	}

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
		db.Escape(agent), windowHours)

	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil, err
	}

	m := &SLAMetrics{Agent: agent}
	if len(rows) > 0 {
		r := rows[0]
		m.Total = db.Int(r["total"])
		m.Success = db.Int(r["success"])
		m.Fail = db.Int(r["fail"])
		m.AvgLatencyMs = int64(db.Float(r["avg_latency_ms"]))
		m.TotalCost = db.Float(r["total_cost"])
		m.AvgCost = db.Float(r["avg_cost"])
		if m.Total > 0 {
			m.SuccessRate = float64(m.Success) / float64(m.Total)
		}
	}

	// Compute P95 latency.
	m.P95LatencyMs = QueryP95Latency(dbPath, agent, windowHours)

	return m, nil
}

// QueryP95Latency computes the 95th percentile latency for an agent.
func QueryP95Latency(dbPath, agent string, windowHours int) int64 {
	sql := fmt.Sprintf(
		`SELECT CAST((julianday(finished_at) - julianday(started_at)) * 86400000 AS INTEGER) as latency_ms
		 FROM job_runs
		 WHERE agent = '%s'
		   AND datetime(started_at) >= datetime('now', '-%d hours')
		   AND status = 'success'
		 ORDER BY latency_ms`,
		db.Escape(agent), windowHours)

	rows, err := db.Query(dbPath, sql)
	if err != nil || len(rows) == 0 {
		return 0
	}

	// Collect latencies.
	latencies := make([]int64, 0, len(rows))
	for _, r := range rows {
		latencies = append(latencies, int64(db.Float(r["latency_ms"])))
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

	// P95 index.
	idx := int(float64(len(latencies)) * 0.95)
	if idx >= len(latencies) {
		idx = len(latencies) - 1
	}
	return latencies[idx]
}

// QuerySLAAll computes SLA metrics for all listed agents.
func QuerySLAAll(dbPath string, agents []string, windowHours int) ([]SLAMetrics, error) {
	var metrics []SLAMetrics
	for _, agent := range agents {
		m, err := QuerySLAMetrics(dbPath, agent, windowHours)
		if err != nil {
			return nil, err
		}
		metrics = append(metrics, *m)
	}
	return metrics, nil
}

// QuerySLAStatusAll returns SLA status (with violation checks) for all agents.
// agents maps agent name to its SLA config; allAgentNames is the list of names to report on.
func QuerySLAStatusAll(dbPath string, agents map[string]AgentSLACfg, allAgentNames []string, windowHours int) ([]SLAStatus, error) {
	if windowHours <= 0 {
		windowHours = 24
	}

	names := make([]string, len(allAgentNames))
	copy(names, allAgentNames)
	sort.Strings(names)

	var statuses []SLAStatus
	for _, name := range names {
		m, err := QuerySLAMetrics(dbPath, name, windowHours)
		if err != nil {
			return nil, err
		}

		s := SLAStatus{SLAMetrics: *m, Status: "ok"}

		// Check thresholds.
		if agentCfg, ok := agents[name]; ok {
			var violations []string
			if agentCfg.MinSuccessRate > 0 && m.Total > 0 && m.SuccessRate < agentCfg.MinSuccessRate {
				violations = append(violations,
					fmt.Sprintf("success rate %.1f%% < %.1f%%", m.SuccessRate*100, agentCfg.MinSuccessRate*100))
			}
			if agentCfg.MaxP95LatencyMs > 0 && m.P95LatencyMs > agentCfg.MaxP95LatencyMs {
				violations = append(violations,
					fmt.Sprintf("p95 latency %dms > %dms", m.P95LatencyMs, agentCfg.MaxP95LatencyMs))
			}
			if len(violations) > 0 {
				s.Status = "violation"
				s.Violation = strings.Join(violations, "; ")
			} else if m.Total > 0 && agentCfg.MinSuccessRate > 0 && m.SuccessRate < agentCfg.MinSuccessRate+0.05 {
				// Warning: within 5% of threshold.
				s.Status = "warning"
				s.Violation = fmt.Sprintf("success rate %.1f%% approaching threshold %.1f%%",
					m.SuccessRate*100, agentCfg.MinSuccessRate*100)
			}
		}

		statuses = append(statuses, s)
	}
	return statuses, nil
}

// CheckSLAViolations runs a SLA check for all agents and calls notifyFn for each violation.
func CheckSLAViolations(dbPath string, agents map[string]AgentSLACfg, windowHours int, notifyFn func(string)) {
	if dbPath == "" {
		return
	}
	if windowHours <= 0 {
		windowHours = 24
	}

	for agent, agentCfg := range agents {
		m, err := QuerySLAMetrics(dbPath, agent, windowHours)
		if err != nil {
			tlog.Warn("SLA check query failed", "agent", agent, "error", err)
			continue
		}

		if m.Total == 0 {
			continue // no data, skip
		}

		var violations []string
		if agentCfg.MinSuccessRate > 0 && m.SuccessRate < agentCfg.MinSuccessRate {
			violations = append(violations,
				fmt.Sprintf("success rate %.1f%% < %.1f%%", m.SuccessRate*100, agentCfg.MinSuccessRate*100))
		}
		if agentCfg.MaxP95LatencyMs > 0 && m.P95LatencyMs > agentCfg.MaxP95LatencyMs {
			violations = append(violations,
				fmt.Sprintf("p95 latency %dms > %dms", m.P95LatencyMs, agentCfg.MaxP95LatencyMs))
		}

		isViolation := len(violations) > 0
		detail := ""
		if isViolation {
			detail = strings.Join(violations, "; ")
		}

		// Record check result.
		RecordSLACheck(dbPath, SLACheckResult{
			Agent:       agent,
			Timestamp:   time.Now().Format(time.RFC3339),
			SuccessRate: m.SuccessRate,
			P95Latency:  m.P95LatencyMs,
			Violation:   isViolation,
			Detail:      detail,
		})

		// Notify on violation.
		if isViolation && notifyFn != nil {
			msg := fmt.Sprintf("SLA Violation [%s]\n%s\n(%d tasks in %dh window, success: %d/%d, p95: %dms, cost: $%.2f)",
				agent, detail, m.Total, windowHours, m.Success, m.Total, m.P95LatencyMs, m.TotalCost)
			notifyFn(msg)
		}
	}
}

// RecordSLACheck stores a SLA check result in the database.
func RecordSLACheck(dbPath string, r SLACheckResult) {
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
		db.Escape(r.Agent),
		db.Escape(r.Timestamp),
		r.SuccessRate,
		r.P95Latency,
		violationInt,
		db.Escape(r.Detail))

	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		tlog.Warn("record SLA check failed", "error", fmt.Sprintf("%s: %s", err, out))
	}
}

// QuerySLAHistory returns recent SLA check results for an agent (empty string = all agents).
func QuerySLAHistory(dbPath, agent string, limit int) ([]SLACheckResult, error) {
	if dbPath == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 24
	}

	where := ""
	if agent != "" {
		where = fmt.Sprintf("WHERE agent = '%s'", db.Escape(agent))
	}

	sql := fmt.Sprintf(
		`SELECT agent, checked_at, success_rate, p95_latency_ms, violation, detail
		 FROM sla_checks %s ORDER BY id DESC LIMIT %d`, where, limit)

	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil, err
	}

	var results []SLACheckResult
	for _, r := range rows {
		results = append(results, SLACheckResult{
			Agent:       db.Str(r["agent"]),
			Timestamp:   db.Str(r["checked_at"]),
			SuccessRate: db.Float(r["success_rate"]),
			P95Latency:  int64(db.Float(r["p95_latency_ms"])),
			Violation:   db.Int(r["violation"]) != 0,
			Detail:      db.Str(r["detail"]),
		})
	}
	return results, nil
}

// --- Checker (periodic ticker) ---

// Checker runs periodic SLA checks.
type Checker struct {
	dbPath   string
	agents   map[string]AgentSLACfg
	interval time.Duration
	window   time.Duration
	notifyFn func(string)
	lastRun  time.Time
}

// NewChecker creates a Checker from an SLAConfig and db path.
func NewChecker(dbPath string, cfg SLAConfig, notifyFn func(string)) *Checker {
	return &Checker{
		dbPath:   dbPath,
		agents:   cfg.Agents,
		interval: cfg.CheckIntervalOrDefault(),
		window:   cfg.WindowOrDefault(),
		notifyFn: notifyFn,
	}
}

// LastRun returns the time of the most recent SLA check run.
func (c *Checker) LastRun() time.Time {
	return c.lastRun
}

// Tick is called periodically (e.g. from cron). Runs SLA check if enough time has passed.
func (c *Checker) Tick(ctx context.Context) {
	if time.Since(c.lastRun) < c.interval {
		return
	}
	c.lastRun = time.Now()
	tlog.DebugCtx(ctx, "running SLA check")
	windowHours := int(c.window.Hours())
	CheckSLAViolations(c.dbPath, c.agents, windowHours, c.notifyFn)
}
