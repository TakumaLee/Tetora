package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// --- History DB Types ---

type JobRun struct {
	ID            int     `json:"id"`
	JobID         string  `json:"jobId"`
	Name          string  `json:"name"`
	Source        string  `json:"source"`
	StartedAt     string  `json:"startedAt"`
	FinishedAt    string  `json:"finishedAt"`
	Status        string  `json:"status"`
	ExitCode      int     `json:"exitCode"`
	CostUSD       float64 `json:"costUsd"`
	OutputSummary string  `json:"outputSummary"`
	Error         string  `json:"error"`
	Model         string  `json:"model"`
	SessionID     string  `json:"sessionId"`
	OutputFile    string  `json:"outputFile,omitempty"`
	TokensIn      int     `json:"tokensIn,omitempty"`
	TokensOut     int     `json:"tokensOut,omitempty"`
	Role          string  `json:"role,omitempty"`
}

type CostStats struct {
	Today float64 `json:"today"`
	Week  float64 `json:"week"`
	Month float64 `json:"month"`
}

// --- Init ---

func initHistoryDB(dbPath string) error {
	sql := `
CREATE TABLE IF NOT EXISTS job_runs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  job_id TEXT NOT NULL,
  name TEXT NOT NULL,
  source TEXT NOT NULL DEFAULT '',
  started_at TEXT NOT NULL,
  finished_at TEXT NOT NULL,
  status TEXT NOT NULL,
  exit_code INTEGER DEFAULT 0,
  cost_usd REAL DEFAULT 0,
  output_summary TEXT DEFAULT '',
  error TEXT DEFAULT '',
  model TEXT DEFAULT '',
  session_id TEXT DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_job_runs_job_id ON job_runs(job_id);
CREATE INDEX IF NOT EXISTS idx_job_runs_started ON job_runs(started_at);
`
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("init history db: %s: %w", string(out), err)
	}

	// Migration: add output_file column if missing.
	migrate := `ALTER TABLE job_runs ADD COLUMN output_file TEXT DEFAULT '';`
	cmd2 := exec.Command("sqlite3", dbPath, migrate)
	cmd2.CombinedOutput() // ignore error if column already exists

	// Migration: add tokens_in column if missing.
	migrate2 := `ALTER TABLE job_runs ADD COLUMN tokens_in INTEGER DEFAULT 0;`
	cmd3 := exec.Command("sqlite3", dbPath, migrate2)
	cmd3.CombinedOutput() // ignore error if column already exists

	// Migration: add tokens_out column if missing.
	migrate3 := `ALTER TABLE job_runs ADD COLUMN tokens_out INTEGER DEFAULT 0;`
	cmd4 := exec.Command("sqlite3", dbPath, migrate3)
	cmd4.CombinedOutput() // ignore error if column already exists

	// Migration: add role column if missing.
	migrate4 := `ALTER TABLE job_runs ADD COLUMN role TEXT DEFAULT '';`
	cmd5 := exec.Command("sqlite3", dbPath, migrate4)
	cmd5.CombinedOutput() // ignore error if column already exists

	return nil
}

// --- Insert ---

func insertJobRun(dbPath string, run JobRun) error {
	sql := fmt.Sprintf(
		`INSERT INTO job_runs (job_id, name, source, started_at, finished_at, status, exit_code, cost_usd, output_summary, error, model, session_id, output_file, tokens_in, tokens_out, role)
		 VALUES ('%s','%s','%s','%s','%s','%s',%d,%f,'%s','%s','%s','%s','%s',%d,%d,'%s')`,
		escapeSQLite(run.JobID),
		escapeSQLite(run.Name),
		escapeSQLite(run.Source),
		escapeSQLite(run.StartedAt),
		escapeSQLite(run.FinishedAt),
		escapeSQLite(run.Status),
		run.ExitCode,
		run.CostUSD,
		escapeSQLite(run.OutputSummary),
		escapeSQLite(run.Error),
		escapeSQLite(run.Model),
		escapeSQLite(run.SessionID),
		escapeSQLite(run.OutputFile),
		run.TokensIn,
		run.TokensOut,
		escapeSQLite(run.Role),
	)
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("insert job_run: %s: %w", string(out), err)
	}
	return nil
}

// --- Query History ---

func queryHistory(dbPath, jobID string, limit int) ([]JobRun, error) {
	if limit <= 0 {
		limit = 20
	}

	where := ""
	if jobID != "" {
		where = fmt.Sprintf("WHERE job_id = '%s'", escapeSQLite(jobID))
	}

	sql := fmt.Sprintf(
		`SELECT id, job_id, name, source, started_at, finished_at, status, exit_code, cost_usd, output_summary, error, model, session_id, COALESCE(output_file,'') as output_file, COALESCE(tokens_in,0) as tokens_in, COALESCE(tokens_out,0) as tokens_out, COALESCE(role,'') as role
		 FROM job_runs %s ORDER BY id DESC LIMIT %d`,
		where, limit)

	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return nil, err
	}

	var runs []JobRun
	for _, row := range rows {
		runs = append(runs, jobRunFromRow(row))
	}
	return runs, nil
}

// queryHistoryByID returns a single job run by its ID.
func queryHistoryByID(dbPath string, id int) (*JobRun, error) {
	sql := fmt.Sprintf(
		`SELECT id, job_id, name, source, started_at, finished_at, status, exit_code, cost_usd, output_summary, error, model, session_id, COALESCE(output_file,'') as output_file, COALESCE(tokens_in,0) as tokens_in, COALESCE(tokens_out,0) as tokens_out, COALESCE(role,'') as role
		 FROM job_runs WHERE id = %d`, id)
	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	run := jobRunFromRow(rows[0])
	return &run, nil
}

func jobRunFromRow(row map[string]any) JobRun {
	return JobRun{
		ID:            jsonInt(row["id"]),
		JobID:         jsonStr(row["job_id"]),
		Name:          jsonStr(row["name"]),
		Source:        jsonStr(row["source"]),
		StartedAt:     jsonStr(row["started_at"]),
		FinishedAt:    jsonStr(row["finished_at"]),
		Status:        jsonStr(row["status"]),
		ExitCode:      jsonInt(row["exit_code"]),
		CostUSD:       jsonFloat(row["cost_usd"]),
		OutputSummary: jsonStr(row["output_summary"]),
		Error:         jsonStr(row["error"]),
		Model:         jsonStr(row["model"]),
		SessionID:     jsonStr(row["session_id"]),
		OutputFile:    jsonStr(row["output_file"]),
		TokensIn:      jsonInt(row["tokens_in"]),
		TokensOut:     jsonInt(row["tokens_out"]),
		Role:          jsonStr(row["role"]),
	}
}

// --- Cost Stats ---

func queryCostStats(dbPath string) (CostStats, error) {
	sql := `SELECT
		COALESCE(SUM(CASE WHEN date(started_at) = date('now','localtime') THEN cost_usd ELSE 0 END), 0) as today,
		COALESCE(SUM(CASE WHEN date(started_at) >= date('now','localtime','-7 days') THEN cost_usd ELSE 0 END), 0) as week,
		COALESCE(SUM(CASE WHEN date(started_at) >= date('now','localtime','-30 days') THEN cost_usd ELSE 0 END), 0) as month
		FROM job_runs`

	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return CostStats{}, err
	}
	if len(rows) == 0 {
		return CostStats{}, nil
	}

	return CostStats{
		Today: jsonFloat(rows[0]["today"]),
		Week:  jsonFloat(rows[0]["week"]),
		Month: jsonFloat(rows[0]["month"]),
	}, nil
}

// --- Filtered Query ---

type HistoryQuery struct {
	JobID  string
	Status string
	From   string // RFC3339 or date string
	To     string
	Limit  int
	Offset int
}

func queryHistoryFiltered(dbPath string, q HistoryQuery) ([]JobRun, int, error) {
	if q.Limit <= 0 {
		q.Limit = 20
	}

	var conditions []string
	if q.JobID != "" {
		conditions = append(conditions, fmt.Sprintf("job_id = '%s'", escapeSQLite(q.JobID)))
	}
	if q.Status != "" {
		conditions = append(conditions, fmt.Sprintf("status = '%s'", escapeSQLite(q.Status)))
	}
	if q.From != "" {
		conditions = append(conditions, fmt.Sprintf("started_at >= '%s'", escapeSQLite(q.From)))
	}
	if q.To != "" {
		conditions = append(conditions, fmt.Sprintf("started_at <= '%s'", escapeSQLite(q.To)))
	}

	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	// Count total matching.
	countSQL := fmt.Sprintf("SELECT COUNT(*) as cnt FROM job_runs %s", where)
	countRows, err := queryDB(dbPath, countSQL)
	if err != nil {
		return nil, 0, err
	}
	total := 0
	if len(countRows) > 0 {
		total = jsonInt(countRows[0]["cnt"])
	}

	// Query page.
	dataSQL := fmt.Sprintf(
		`SELECT id, job_id, name, source, started_at, finished_at, status, exit_code, cost_usd, output_summary, error, model, session_id, COALESCE(output_file,'') as output_file, COALESCE(tokens_in,0) as tokens_in, COALESCE(tokens_out,0) as tokens_out, COALESCE(role,'') as role
		 FROM job_runs %s ORDER BY id DESC LIMIT %d OFFSET %d`,
		where, q.Limit, q.Offset)

	rows, err := queryDB(dbPath, dataSQL)
	if err != nil {
		return nil, 0, err
	}

	var runs []JobRun
	for _, row := range rows {
		runs = append(runs, jobRunFromRow(row))
	}
	return runs, total, nil
}

// queryCostByJobID returns cost aggregated by job_id for the last 30 days.
func queryCostByJobID(dbPath string) (map[string]float64, error) {
	sql := `SELECT job_id, COALESCE(SUM(cost_usd), 0) as total_cost
		FROM job_runs
		WHERE date(started_at) >= date('now','localtime','-30 days')
		GROUP BY job_id`

	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return nil, err
	}

	result := make(map[string]float64)
	for _, row := range rows {
		jobID := jsonStr(row["job_id"])
		cost := jsonFloat(row["total_cost"])
		if jobID != "" {
			result[jobID] = cost
		}
	}
	return result, nil
}

// --- Query Last Job Run ---

// queryLastJobRun returns the most recent job run for a given jobID.
// Used by template variable expansion for {{last_output}}, {{last_status}}, {{last_error}}.
func queryLastJobRun(dbPath, jobID string) *JobRun {
	if dbPath == "" || jobID == "" {
		return nil
	}
	sql := fmt.Sprintf(
		`SELECT id, job_id, name, source, started_at, finished_at, status, exit_code, cost_usd, output_summary, error, model, session_id, COALESCE(output_file,'') as output_file, COALESCE(tokens_in,0) as tokens_in, COALESCE(tokens_out,0) as tokens_out, COALESCE(role,'') as role
		 FROM job_runs WHERE job_id = '%s' ORDER BY id DESC LIMIT 1`,
		escapeSQLite(jobID))

	rows, err := queryDB(dbPath, sql)
	if err != nil || len(rows) == 0 {
		return nil
	}
	run := jobRunFromRow(rows[0])
	return &run
}

// --- Job Average Cost ---

// queryJobAvgCost returns the average cost of the last 10 successful runs for a job.
func queryJobAvgCost(dbPath, jobID string) float64 {
	if dbPath == "" || jobID == "" {
		return 0
	}
	sql := fmt.Sprintf(
		`SELECT COALESCE(AVG(cost_usd), 0) as avg_cost
		 FROM (SELECT cost_usd FROM job_runs
		       WHERE job_id = '%s' AND status = 'success' AND cost_usd > 0
		       ORDER BY id DESC LIMIT 10)`,
		escapeSQLite(jobID))

	rows, err := queryDB(dbPath, sql)
	if err != nil || len(rows) == 0 {
		return 0
	}
	return jsonFloat(rows[0]["avg_cost"])
}

// --- Daily Stats (for trend chart) ---

type DayStat struct {
	Date    string  `json:"date"`
	Total   int     `json:"total"`
	Success int     `json:"success"`
	Fail    int     `json:"fail"`
	Cost    float64 `json:"cost"`
}

// queryDailyStats returns per-day aggregated stats for the last N days.
func queryDailyStats(dbPath string, days int) ([]DayStat, error) {
	if dbPath == "" {
		return nil, nil
	}
	if days <= 0 {
		days = 7
	}
	sql := fmt.Sprintf(
		`SELECT date(started_at, 'localtime') as day,
		        COUNT(*) as total,
		        SUM(CASE WHEN status = 'success' THEN 1 ELSE 0 END) as success,
		        SUM(CASE WHEN status != 'success' THEN 1 ELSE 0 END) as fail,
		        COALESCE(SUM(cost_usd), 0) as cost
		 FROM job_runs
		 WHERE date(started_at, 'localtime') >= date('now', 'localtime', '-%d days')
		 GROUP BY day ORDER BY day`, days)

	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return nil, err
	}

	var stats []DayStat
	for _, row := range rows {
		stats = append(stats, DayStat{
			Date:    jsonStr(row["day"]),
			Total:   jsonInt(row["total"]),
			Success: jsonInt(row["success"]),
			Fail:    jsonInt(row["fail"]),
			Cost:    jsonFloat(row["cost"]),
		})
	}
	return stats, nil
}

// queryDigestStats returns summary stats for a given date range (for daily digest).
func queryDigestStats(dbPath, from, to string) (total, success, fail int, cost float64, failures []JobRun, err error) {
	if dbPath == "" {
		return
	}
	// Summary counts.
	summarySQL := fmt.Sprintf(
		`SELECT COUNT(*) as total,
		        SUM(CASE WHEN status = 'success' THEN 1 ELSE 0 END) as success,
		        SUM(CASE WHEN status != 'success' THEN 1 ELSE 0 END) as fail,
		        COALESCE(SUM(cost_usd), 0) as cost
		 FROM job_runs
		 WHERE started_at >= '%s' AND started_at < '%s'`,
		escapeSQLite(from), escapeSQLite(to))

	rows, qErr := queryDB(dbPath, summarySQL)
	if qErr != nil {
		err = qErr
		return
	}
	if len(rows) > 0 {
		total = jsonInt(rows[0]["total"])
		success = jsonInt(rows[0]["success"])
		fail = jsonInt(rows[0]["fail"])
		cost = jsonFloat(rows[0]["cost"])
	}

	// Failed runs details.
	if fail > 0 {
		failSQL := fmt.Sprintf(
			`SELECT id, job_id, name, source, started_at, finished_at, status, exit_code, cost_usd, output_summary, error, model, session_id, COALESCE(output_file,'') as output_file, COALESCE(tokens_in,0) as tokens_in, COALESCE(tokens_out,0) as tokens_out, COALESCE(role,'') as role
			 FROM job_runs
			 WHERE started_at >= '%s' AND started_at < '%s' AND status != 'success'
			 ORDER BY id DESC LIMIT 10`,
			escapeSQLite(from), escapeSQLite(to))
		failRows, fErr := queryDB(dbPath, failSQL)
		if fErr == nil {
			for _, row := range failRows {
				failures = append(failures, jobRunFromRow(row))
			}
		}
	}
	return
}

// --- Cleanup ---

func cleanupHistory(dbPath string, days int) error {
	sql := fmt.Sprintf(
		`DELETE FROM job_runs WHERE datetime(started_at) < datetime('now','-%d days')`, days)
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("cleanup history: %s: %w", string(out), err)
	}
	return nil
}

// --- JSON helpers ---

func jsonStr(v any) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case json.Number:
		return val.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}

func jsonFloat(v any) float64 {
	if v == nil {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return val
	case json.Number:
		f, _ := val.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(val, 64)
		return f
	default:
		return 0
	}
}

func jsonInt(v any) int {
	if v == nil {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return int(val)
	case json.Number:
		i, _ := val.Int64()
		return int(i)
	case string:
		i, _ := strconv.Atoi(val)
		return i
	default:
		return 0
	}
}

// --- Record History Helper ---
// Used by both cron.go and dispatch.go to record task execution.

func recordHistory(dbPath string, jobID, name, source, role string, task Task, result TaskResult, startedAt, finishedAt, outputFile string) {
	if dbPath == "" {
		return
	}
	run := JobRun{
		JobID:         jobID,
		Name:          name,
		Source:        source,
		StartedAt:     startedAt,
		FinishedAt:    finishedAt,
		Status:        result.Status,
		ExitCode:      result.ExitCode,
		CostUSD:       result.CostUSD,
		OutputSummary: truncateStr(result.Output, 1000),
		Error:         result.Error,
		Model:         result.Model,
		SessionID:     result.SessionID,
		OutputFile:    outputFile,
		TokensIn:      result.TokensIn,
		TokensOut:     result.TokensOut,
		Role:          role,
	}
	if err := insertJobRun(dbPath, run); err != nil {
		// Log but don't fail the task.
		logWarn("record history failed", "error", err)
	}
}

// --- Observability Metrics ---

// MetricsResult holds aggregate metrics for a time range.
type MetricsResult struct {
	TotalTasks     int     `json:"totalTasks"`
	SuccessRate    float64 `json:"successRate"`
	TotalTokensIn  int     `json:"totalTokensIn"`
	TotalTokensOut int     `json:"totalTokensOut"`
	AvgDurationMs  int64   `json:"avgDurationMs"`
	AvgCostUSD     float64 `json:"avgCostUsd"`
	TotalCostUSD   float64 `json:"totalCostUsd"`
}

// queryMetrics returns aggregate metrics for the last N days.
func queryMetrics(dbPath string, days int) (*MetricsResult, error) {
	if dbPath == "" {
		return &MetricsResult{}, nil
	}
	if days <= 0 {
		days = 30
	}

	sql := fmt.Sprintf(
		`SELECT
			COUNT(*) as total,
			COALESCE(SUM(CASE WHEN status = 'success' THEN 1 ELSE 0 END), 0) as success,
			COALESCE(SUM(tokens_in), 0) as total_tokens_in,
			COALESCE(SUM(tokens_out), 0) as total_tokens_out,
			COALESCE(AVG(CAST(
				(julianday(finished_at) - julianday(started_at)) * 86400000 AS INTEGER
			)), 0) as avg_dur_ms,
			COALESCE(AVG(cost_usd), 0) as avg_cost,
			COALESCE(SUM(cost_usd), 0) as total_cost
		 FROM job_runs
		 WHERE date(started_at, 'localtime') >= date('now', 'localtime', '-%d days')`, days)

	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return &MetricsResult{}, nil
	}

	r := rows[0]
	total := jsonInt(r["total"])
	success := jsonInt(r["success"])
	successRate := 0.0
	if total > 0 {
		successRate = float64(success) / float64(total)
	}

	return &MetricsResult{
		TotalTasks:     total,
		SuccessRate:    successRate,
		TotalTokensIn:  jsonInt(r["total_tokens_in"]),
		TotalTokensOut: jsonInt(r["total_tokens_out"]),
		AvgDurationMs:  int64(jsonFloat(r["avg_dur_ms"])),
		AvgCostUSD:     jsonFloat(r["avg_cost"]),
		TotalCostUSD:   jsonFloat(r["total_cost"]),
	}, nil
}

// DailyMetrics holds per-day metrics breakdown.
type DailyMetrics struct {
	Date      string  `json:"date"`
	Tasks     int     `json:"tasks"`
	Success   int     `json:"success"`
	Errors    int     `json:"errors"`
	Timeouts  int     `json:"timeouts"`
	CostUSD   float64 `json:"costUsd"`
	TokensIn  int     `json:"tokensIn"`
	TokensOut int     `json:"tokensOut"`
	AvgDurMs  int64   `json:"avgDurMs"`
}

// queryDailyMetrics returns per-day breakdowns for the last N days.
func queryDailyMetrics(dbPath string, days int) ([]DailyMetrics, error) {
	if dbPath == "" {
		return nil, nil
	}
	if days <= 0 {
		days = 30
	}

	sql := fmt.Sprintf(
		`SELECT
			date(started_at, 'localtime') as day,
			COUNT(*) as total,
			SUM(CASE WHEN status = 'success' THEN 1 ELSE 0 END) as success,
			SUM(CASE WHEN status = 'error' THEN 1 ELSE 0 END) as errors,
			SUM(CASE WHEN status = 'timeout' THEN 1 ELSE 0 END) as timeouts,
			COALESCE(SUM(cost_usd), 0) as cost,
			COALESCE(SUM(tokens_in), 0) as tokens_in,
			COALESCE(SUM(tokens_out), 0) as tokens_out,
			COALESCE(AVG(CAST(
				(julianday(finished_at) - julianday(started_at)) * 86400000 AS INTEGER
			)), 0) as avg_dur_ms
		 FROM job_runs
		 WHERE date(started_at, 'localtime') >= date('now', 'localtime', '-%d days')
		 GROUP BY day ORDER BY day`, days)

	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return nil, err
	}

	var metrics []DailyMetrics
	for _, row := range rows {
		metrics = append(metrics, DailyMetrics{
			Date:      jsonStr(row["day"]),
			Tasks:     jsonInt(row["total"]),
			Success:   jsonInt(row["success"]),
			Errors:    jsonInt(row["errors"]),
			Timeouts:  jsonInt(row["timeouts"]),
			CostUSD:   jsonFloat(row["cost"]),
			TokensIn:  jsonInt(row["tokens_in"]),
			TokensOut: jsonInt(row["tokens_out"]),
			AvgDurMs:  int64(jsonFloat(row["avg_dur_ms"])),
		})
	}
	return metrics, nil
}

// ProviderMetrics holds per-provider (model) metrics breakdown.
type ProviderMetrics struct {
	Model     string  `json:"model"`
	Tasks     int     `json:"tasks"`
	AvgCost   float64 `json:"avgCost"`
	AvgDurMs  int64   `json:"avgDurMs"`
	ErrorRate float64 `json:"errorRate"`
	TokensIn  int     `json:"tokensIn"`
	TokensOut int     `json:"tokensOut"`
}

// queryProviderMetrics returns per-model breakdowns for the last N days.
func queryProviderMetrics(dbPath string, days int) ([]ProviderMetrics, error) {
	if dbPath == "" {
		return nil, nil
	}
	if days <= 0 {
		days = 30
	}

	sql := fmt.Sprintf(
		`SELECT
			model,
			COUNT(*) as total,
			COALESCE(AVG(cost_usd), 0) as avg_cost,
			COALESCE(AVG(CAST(
				(julianday(finished_at) - julianday(started_at)) * 86400000 AS INTEGER
			)), 0) as avg_dur_ms,
			COALESCE(SUM(CASE WHEN status != 'success' THEN 1 ELSE 0 END), 0) as errors,
			COALESCE(SUM(tokens_in), 0) as tokens_in,
			COALESCE(SUM(tokens_out), 0) as tokens_out
		 FROM job_runs
		 WHERE date(started_at, 'localtime') >= date('now', 'localtime', '-%d days')
		   AND model != ''
		 GROUP BY model ORDER BY total DESC`, days)

	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return nil, err
	}

	var metrics []ProviderMetrics
	for _, row := range rows {
		total := jsonInt(row["total"])
		errors := jsonInt(row["errors"])
		errorRate := 0.0
		if total > 0 {
			errorRate = float64(errors) / float64(total)
		}
		metrics = append(metrics, ProviderMetrics{
			Model:     jsonStr(row["model"]),
			Tasks:     total,
			AvgCost:   jsonFloat(row["avg_cost"]),
			AvgDurMs:  int64(jsonFloat(row["avg_dur_ms"])),
			ErrorRate: errorRate,
			TokensIn:  jsonInt(row["tokens_in"]),
			TokensOut: jsonInt(row["tokens_out"]),
		})
	}
	return metrics, nil
}

// truncateStr is like truncate() but avoids name collision if truncate is in another file.
func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// stringSliceContains checks if a string slice contains a value.
func stringSliceContains(ss []string, s string) bool {
	for _, v := range ss {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}
