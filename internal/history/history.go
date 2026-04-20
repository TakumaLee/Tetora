package history

import (
	"context"
	"fmt"
	"strings"
	"time"

	"tetora/internal/db"
	tlog "tetora/internal/log"
)

// StatusSkippedConcurrentLimit is the job_runs.status value recorded when the
// 30s cron tick catches a long-running job still executing and records a
// "virtual" run. It must be excluded from every failure/error metric because
// it is not a real failure — routing it through a const is the tripwire that
// catches typos at compile time (SQL would silently filter nothing).
const StatusSkippedConcurrentLimit = "skipped_concurrent_limit"

// --- Types ---

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
	Provider      string  `json:"provider,omitempty"`
	SessionID     string  `json:"sessionId"`
	OutputFile    string  `json:"outputFile,omitempty"`
	TokensIn      int     `json:"tokensIn,omitempty"`
	TokensOut     int     `json:"tokensOut,omitempty"`
	Agent         string  `json:"agent,omitempty"`
	ParentID      string  `json:"parentId,omitempty"`
}

type CostStats struct {
	Today float64 `json:"today"`
	Week  float64 `json:"week"`
	Month float64 `json:"month"`
}

type HistoryQuery struct {
	JobID    string
	Status   string
	From     string // RFC3339 or date string
	To       string
	Limit    int
	Offset   int
	ParentID string // filter subtasks by parent job_id
}

type DayStat struct {
	Date    string  `json:"date"`
	Total   int     `json:"total"`
	Success int     `json:"success"`
	Fail    int     `json:"fail"`
	Cost    float64 `json:"cost"`
}

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

// ProviderMetrics holds per-model metrics breakdown.
type ProviderMetrics struct {
	Model     string  `json:"model"`
	Provider  string  `json:"provider"`
	Tasks     int     `json:"tasks"`
	TotalCost float64 `json:"totalCost"`
	AvgCost   float64 `json:"avgCost"`
	AvgDurMs  int64   `json:"avgDurMs"`
	ErrorRate float64 `json:"errorRate"`
	TokensIn  int     `json:"tokensIn"`
	TokensOut int     `json:"tokensOut"`
}

// SubtaskCount holds the total and completed counts for a decomposed parent task.
type SubtaskCount struct {
	Total     int `json:"total"`
	Completed int `json:"completed"`
}

// --- Init ---

func InitDB(dbPath string) error {
	db.Pragma(dbPath)
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
	if err := db.Exec(dbPath, sql); err != nil {
		return fmt.Errorf("init history db: %w", err)
	}

	// Migrations: add columns if missing.
	for _, col := range []string{
		`ALTER TABLE job_runs ADD COLUMN output_file TEXT DEFAULT '';`,
		`ALTER TABLE job_runs ADD COLUMN tokens_in INTEGER DEFAULT 0;`,
		`ALTER TABLE job_runs ADD COLUMN tokens_out INTEGER DEFAULT 0;`,
		`ALTER TABLE job_runs ADD COLUMN agent TEXT DEFAULT '';`,
		`ALTER TABLE job_runs ADD COLUMN parent_id TEXT DEFAULT '';`,
		`ALTER TABLE job_runs ADD COLUMN provider TEXT DEFAULT '';`,
	} {
		if err := db.Exec(dbPath, col); err != nil {
			if !strings.Contains(err.Error(), "duplicate column") {
				tlog.Warn("history migration failed", "sql", col, "error", err)
			}
		}
	}

	// Migration: rename role -> agent in job_runs (for existing DBs with old column name).
	if err := db.Exec(dbPath, `ALTER TABLE job_runs RENAME COLUMN role TO agent;`); err != nil {
		if !strings.Contains(err.Error(), "no such column") && !strings.Contains(err.Error(), "duplicate column") {
			// Ignore expected errors silently.
		}
	}

	// Cron execution log: records each job trigger for startup replay / zombie detection.
	if err := db.Exec(dbPath, `
CREATE TABLE IF NOT EXISTS cron_execution_log (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  job_id TEXT NOT NULL,
  scheduled_at TEXT NOT NULL,
  started_at TEXT NOT NULL DEFAULT '',
  replayed INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_cron_exec_log ON cron_execution_log(job_id, scheduled_at);
`); err != nil {
		tlog.Warn("cron_execution_log init failed", "error", err)
	}

	return nil
}

// --- Cron Execution Log ---

// InsertCronExecLog records a cron job trigger for startup replay tracking.
func InsertCronExecLog(dbPath, jobID, scheduledAt, startedAt string, replayed bool) {
	replayedInt := 0
	if replayed {
		replayedInt = 1
	}
	sql := fmt.Sprintf(
		`INSERT INTO cron_execution_log (job_id, scheduled_at, started_at, replayed) VALUES ('%s','%s','%s',%d)`,
		db.Escape(jobID), db.Escape(scheduledAt), db.Escape(startedAt), replayedInt,
	)
	if err := db.Exec(dbPath, sql); err != nil {
		tlog.Warn("cron exec log insert failed", "jobId", jobID, "error", err)
	}
}

// CronExecLogExists returns true if there is a cron_execution_log entry for jobID
// with scheduled_at within ±10 minutes of scheduledAt.
func CronExecLogExists(dbPath, jobID string, scheduledAt time.Time) bool {
	const tol = 10 * time.Minute
	from := scheduledAt.Add(-tol).UTC().Format(time.RFC3339)
	to := scheduledAt.Add(tol).UTC().Format(time.RFC3339)
	sql := fmt.Sprintf(
		`SELECT COUNT(*) as cnt FROM cron_execution_log WHERE job_id='%s' AND scheduled_at >= '%s' AND scheduled_at <= '%s'`,
		db.Escape(jobID), db.Escape(from), db.Escape(to),
	)
	rows, err := db.Query(dbPath, sql)
	if err != nil || len(rows) == 0 {
		return false
	}
	return db.Int(rows[0]["cnt"]) > 0
}

// JobRunExistsNear returns true if job_runs has an entry for jobID with started_at
// within ±10 minutes of near. Used as a backward-compat fallback before the
// cron_execution_log table existed.
func JobRunExistsNear(dbPath, jobID string, near time.Time) bool {
	const tol = 10 * time.Minute
	from := near.Add(-tol).UTC().Format(time.RFC3339)
	to := near.Add(tol).UTC().Format(time.RFC3339)
	sql := fmt.Sprintf(
		`SELECT COUNT(*) as cnt FROM job_runs WHERE job_id='%s' AND started_at >= '%s' AND started_at <= '%s'`,
		db.Escape(jobID), db.Escape(from), db.Escape(to),
	)
	rows, err := db.Query(dbPath, sql)
	if err != nil || len(rows) == 0 {
		return false
	}
	return db.Int(rows[0]["cnt"]) > 0
}

// --- Insert ---

func InsertRun(dbPath string, run JobRun) error {
	sql := fmt.Sprintf(
		`INSERT INTO job_runs (job_id, name, source, started_at, finished_at, status, exit_code, cost_usd, output_summary, error, model, provider, session_id, output_file, tokens_in, tokens_out, agent, parent_id)
		 VALUES ('%s','%s','%s','%s','%s','%s',%d,%f,'%s','%s','%s','%s','%s','%s',%d,%d,'%s','%s')`,
		db.Escape(run.JobID),
		db.Escape(run.Name),
		db.Escape(run.Source),
		db.Escape(run.StartedAt),
		db.Escape(run.FinishedAt),
		db.Escape(run.Status),
		run.ExitCode,
		run.CostUSD,
		db.Escape(run.OutputSummary),
		db.Escape(run.Error),
		db.Escape(run.Model),
		db.Escape(run.Provider),
		db.Escape(run.SessionID),
		db.Escape(run.OutputFile),
		run.TokensIn,
		run.TokensOut,
		db.Escape(run.Agent),
		db.Escape(run.ParentID),
	)
	return db.Exec(dbPath, sql)
}

// InsertRunCtx is like InsertRun but respects context cancellation.
func InsertRunCtx(ctx context.Context, dbPath string, run JobRun) error {
	sql := fmt.Sprintf(
		`INSERT INTO job_runs (job_id, name, source, started_at, finished_at, status, exit_code, cost_usd, output_summary, error, model, provider, session_id, output_file, tokens_in, tokens_out, agent, parent_id)
		 VALUES ('%s','%s','%s','%s','%s','%s',%d,%f,'%s','%s','%s','%s','%s','%s',%d,%d,'%s','%s')`,
		db.Escape(run.JobID),
		db.Escape(run.Name),
		db.Escape(run.Source),
		db.Escape(run.StartedAt),
		db.Escape(run.FinishedAt),
		db.Escape(run.Status),
		run.ExitCode,
		run.CostUSD,
		db.Escape(run.OutputSummary),
		db.Escape(run.Error),
		db.Escape(run.Model),
		db.Escape(run.Provider),
		db.Escape(run.SessionID),
		db.Escape(run.OutputFile),
		run.TokensIn,
		run.TokensOut,
		db.Escape(run.Agent),
		db.Escape(run.ParentID),
	)
	return db.ExecContext(ctx, dbPath, sql)
}

// --- Query ---

func Query(dbPath, jobID string, limit int) ([]JobRun, error) {
	if limit <= 0 {
		limit = 20
	}

	where := ""
	if jobID != "" {
		where = fmt.Sprintf("WHERE job_id = '%s'", db.Escape(jobID))
	}

	sql := fmt.Sprintf(
		`SELECT id, job_id, name, source, started_at, finished_at, status, exit_code, cost_usd, output_summary, error, model, COALESCE(provider,'') as provider, session_id, COALESCE(output_file,'') as output_file, COALESCE(tokens_in,0) as tokens_in, COALESCE(tokens_out,0) as tokens_out, COALESCE(agent,'') as agent, COALESCE(parent_id,'') as parent_id
		 FROM job_runs %s ORDER BY id DESC LIMIT %d`,
		where, limit)

	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil, err
	}

	var runs []JobRun
	for _, row := range rows {
		runs = append(runs, runFromRow(row))
	}
	return runs, nil
}

// QueryByID returns a single job run by its ID.
func QueryByID(dbPath string, id int) (*JobRun, error) {
	sql := fmt.Sprintf(
		`SELECT id, job_id, name, source, started_at, finished_at, status, exit_code, cost_usd, output_summary, error, model, COALESCE(provider,'') as provider, session_id, COALESCE(output_file,'') as output_file, COALESCE(tokens_in,0) as tokens_in, COALESCE(tokens_out,0) as tokens_out, COALESCE(agent,'') as agent, COALESCE(parent_id,'') as parent_id
		 FROM job_runs WHERE id = %d`, id)
	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	run := runFromRow(rows[0])
	return &run, nil
}

// RunFromRow converts a raw DB row map to a JobRun. Exported for use by callers
// that perform their own queries and need consistent field mapping.
func RunFromRow(row map[string]any) JobRun { return runFromRow(row) }

func runFromRow(row map[string]any) JobRun {
	return JobRun{
		ID:            db.Int(row["id"]),
		JobID:         db.Str(row["job_id"]),
		Name:          db.Str(row["name"]),
		Source:        db.Str(row["source"]),
		StartedAt:     db.Str(row["started_at"]),
		FinishedAt:    db.Str(row["finished_at"]),
		Status:        db.Str(row["status"]),
		ExitCode:      db.Int(row["exit_code"]),
		CostUSD:       db.Float(row["cost_usd"]),
		OutputSummary: db.Str(row["output_summary"]),
		Error:         db.Str(row["error"]),
		Model:         db.Str(row["model"]),
		Provider:      db.Str(row["provider"]),
		SessionID:     db.Str(row["session_id"]),
		OutputFile:    db.Str(row["output_file"]),
		TokensIn:      db.Int(row["tokens_in"]),
		TokensOut:     db.Int(row["tokens_out"]),
		Agent:         db.Str(row["agent"]),
		ParentID:      db.Str(row["parent_id"]),
	}
}

// --- Cost Stats ---

// TodayTotalTokens returns the total tokens_in and tokens_out recorded today.
func TodayTotalTokens(dbPath string) (int, int) {
	rows, err := db.Query(dbPath, `SELECT COALESCE(SUM(tokens_in),0) as total_in, COALESCE(SUM(tokens_out),0) as total_out FROM job_runs WHERE date(started_at) = date('now','localtime')`)
	if err != nil || len(rows) == 0 {
		return 0, 0
	}
	return db.Int(rows[0]["total_in"]), db.Int(rows[0]["total_out"])
}

func QueryCostStats(dbPath string) (CostStats, error) {
	sql := `SELECT
		COALESCE(SUM(CASE WHEN date(started_at) = date('now','localtime') THEN cost_usd ELSE 0 END), 0) as today,
		COALESCE(SUM(CASE WHEN date(started_at) >= date('now','localtime','-7 days') THEN cost_usd ELSE 0 END), 0) as week,
		COALESCE(SUM(CASE WHEN date(started_at) >= date('now','localtime','-30 days') THEN cost_usd ELSE 0 END), 0) as month
		FROM job_runs`

	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return CostStats{}, err
	}
	if len(rows) == 0 {
		return CostStats{}, nil
	}

	return CostStats{
		Today: db.Float(rows[0]["today"]),
		Week:  db.Float(rows[0]["week"]),
		Month: db.Float(rows[0]["month"]),
	}, nil
}

// --- Filtered Query ---

func QueryFiltered(dbPath string, q HistoryQuery) ([]JobRun, int, error) {
	if q.Limit <= 0 {
		q.Limit = 20
	}

	var conditions []string
	if q.JobID != "" {
		conditions = append(conditions, fmt.Sprintf("job_id = '%s'", db.Escape(q.JobID)))
	}
	if q.Status != "" {
		conditions = append(conditions, fmt.Sprintf("status = '%s'", db.Escape(q.Status)))
	}
	if q.From != "" {
		conditions = append(conditions, fmt.Sprintf("started_at >= '%s'", db.Escape(q.From)))
	}
	if q.To != "" {
		conditions = append(conditions, fmt.Sprintf("started_at <= '%s'", db.Escape(q.To)))
	}
	if q.ParentID != "" {
		conditions = append(conditions, fmt.Sprintf("parent_id = '%s'", db.Escape(q.ParentID)))
	}

	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	// Count total matching.
	countSQL := fmt.Sprintf("SELECT COUNT(*) as cnt FROM job_runs %s", where)
	countRows, err := db.Query(dbPath, countSQL)
	if err != nil {
		return nil, 0, err
	}
	total := 0
	if len(countRows) > 0 {
		total = db.Int(countRows[0]["cnt"])
	}

	// Query page.
	dataSQL := fmt.Sprintf(
		`SELECT id, job_id, name, source, started_at, finished_at, status, exit_code, cost_usd, output_summary, error, model, COALESCE(provider,'') as provider, session_id, COALESCE(output_file,'') as output_file, COALESCE(tokens_in,0) as tokens_in, COALESCE(tokens_out,0) as tokens_out, COALESCE(agent,'') as agent, COALESCE(parent_id,'') as parent_id
		 FROM job_runs %s ORDER BY id DESC LIMIT %d OFFSET %d`,
		where, q.Limit, q.Offset)

	rows, err := db.Query(dbPath, dataSQL)
	if err != nil {
		return nil, 0, err
	}

	var runs []JobRun
	for _, row := range rows {
		runs = append(runs, runFromRow(row))
	}
	return runs, total, nil
}

// QueryCostByJobID returns cost aggregated by job_id for the last 30 days.
func QueryCostByJobID(dbPath string) (map[string]float64, error) {
	sql := `SELECT job_id, COALESCE(SUM(cost_usd), 0) as total_cost
		FROM job_runs
		WHERE date(started_at) >= date('now','localtime','-30 days')
		GROUP BY job_id`

	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil, err
	}

	result := make(map[string]float64)
	for _, row := range rows {
		jobID := db.Str(row["job_id"])
		cost := db.Float(row["total_cost"])
		if jobID != "" {
			result[jobID] = cost
		}
	}
	return result, nil
}

// --- Query Last Finished ---

// QueryLastFinished returns the most recent finished_at timestamp from job_runs.
// Used for idle detection — if no job has finished recently, the system is idle.
func QueryLastFinished(dbPath string) time.Time {
	if dbPath == "" {
		return time.Time{}
	}
	sql := `SELECT MAX(finished_at) as last_finished FROM job_runs`
	rows, err := db.Query(dbPath, sql)
	if err != nil || len(rows) == 0 {
		return time.Time{}
	}
	ts := db.Str(rows[0]["last_finished"])
	if ts == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return time.Time{}
	}
	return t
}

// --- Query Last Job Run ---

// QueryLastRun returns the most recent job run for a given jobID.
// Used by template variable expansion for {{last_output}}, {{last_status}}, {{last_error}}.
func QueryLastRun(dbPath, jobID string) *JobRun {
	if dbPath == "" || jobID == "" {
		return nil
	}
	sql := fmt.Sprintf(
		`SELECT id, job_id, name, source, started_at, finished_at, status, exit_code, cost_usd, output_summary, error, model, COALESCE(provider,'') as provider, session_id, COALESCE(output_file,'') as output_file, COALESCE(tokens_in,0) as tokens_in, COALESCE(tokens_out,0) as tokens_out, COALESCE(agent,'') as agent, COALESCE(parent_id,'') as parent_id
		 FROM job_runs WHERE job_id = '%s' ORDER BY id DESC LIMIT 1`,
		db.Escape(jobID))

	rows, err := db.Query(dbPath, sql)
	if err != nil || len(rows) == 0 {
		return nil
	}
	run := runFromRow(rows[0])
	return &run
}

// --- Job Average Cost ---

// QueryJobAvgCost returns the average cost of the last 10 successful runs for a job.
func QueryJobAvgCost(dbPath, jobID string) float64 {
	if dbPath == "" || jobID == "" {
		return 0
	}
	sql := fmt.Sprintf(
		`SELECT COALESCE(AVG(cost_usd), 0) as avg_cost
		 FROM (SELECT cost_usd FROM job_runs
		       WHERE job_id = '%s' AND status = 'success' AND cost_usd > 0
		       ORDER BY id DESC LIMIT 10)`,
		db.Escape(jobID))

	rows, err := db.Query(dbPath, sql)
	if err != nil || len(rows) == 0 {
		return 0
	}
	return db.Float(rows[0]["avg_cost"])
}

// --- Daily Stats (for trend chart) ---

// QueryDailyStats returns per-day aggregated stats for the last N days.
func QueryDailyStats(dbPath string, days int) ([]DayStat, error) {
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
		   AND status != '%s'
		 GROUP BY day ORDER BY day`, days, StatusSkippedConcurrentLimit)

	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil, err
	}

	var stats []DayStat
	for _, row := range rows {
		stats = append(stats, DayStat{
			Date:    db.Str(row["day"]),
			Total:   db.Int(row["total"]),
			Success: db.Int(row["success"]),
			Fail:    db.Int(row["fail"]),
			Cost:    db.Float(row["cost"]),
		})
	}
	return stats, nil
}

// QueryDigestStats returns summary stats for a given date range (for daily digest).
func QueryDigestStats(dbPath, from, to string) (total, success, fail int, cost float64, failures []JobRun, err error) {
	if dbPath == "" {
		return
	}
	// Summary counts — skipped_concurrent_limit excluded from all metrics so total = success + fail.
	summarySQL := fmt.Sprintf(
		`SELECT COUNT(*) as total,
		        SUM(CASE WHEN status = 'success' THEN 1 ELSE 0 END) as success,
		        SUM(CASE WHEN status NOT IN ('success', '%s') THEN 1 ELSE 0 END) as fail,
		        COALESCE(SUM(cost_usd), 0) as cost
		 FROM job_runs
		 WHERE started_at >= '%s' AND started_at < '%s'
		   AND status != '%s'`,
		StatusSkippedConcurrentLimit, db.Escape(from), db.Escape(to), StatusSkippedConcurrentLimit)

	rows, qErr := db.Query(dbPath, summarySQL)
	if qErr != nil {
		err = qErr
		return
	}
	if len(rows) > 0 {
		total = db.Int(rows[0]["total"])
		success = db.Int(rows[0]["success"])
		fail = db.Int(rows[0]["fail"])
		cost = db.Float(rows[0]["cost"])
	}

	// Failed runs details.
	if fail > 0 {
		failSQL := fmt.Sprintf(
			`SELECT id, job_id, name, source, started_at, finished_at, status, exit_code, cost_usd, output_summary, error, model, COALESCE(provider,'') as provider, session_id, COALESCE(output_file,'') as output_file, COALESCE(tokens_in,0) as tokens_in, COALESCE(tokens_out,0) as tokens_out, COALESCE(agent,'') as agent, COALESCE(parent_id,'') as parent_id
			 FROM job_runs
			 WHERE started_at >= '%s' AND started_at < '%s' AND status NOT IN ('success', '%s')
			 ORDER BY id DESC LIMIT 10`,
			db.Escape(from), db.Escape(to), StatusSkippedConcurrentLimit)
		failRows, fErr := db.Query(dbPath, failSQL)
		if fErr == nil {
			for _, row := range failRows {
				failures = append(failures, runFromRow(row))
			}
		}
	}
	return
}

// --- Recent Fails ---

// FailQuery specifies parameters for QueryRecentFails.
type FailQuery struct {
	JobID string
	Days  int // default 3
	Limit int // default 20
}

// QueryRecentFails returns non-success runs within the last N days.
func QueryRecentFails(dbPath string, q FailQuery) ([]JobRun, error) {
	days := q.Days
	if days <= 0 {
		days = 3
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}

	var conditions []string
	conditions = append(conditions, fmt.Sprintf("status NOT IN ('success', '%s')", StatusSkippedConcurrentLimit))
	conditions = append(conditions, fmt.Sprintf("datetime(started_at) >= datetime('now','-%d days')", days))
	if q.JobID != "" {
		conditions = append(conditions, fmt.Sprintf("job_id = '%s'", db.Escape(q.JobID)))
	}

	where := "WHERE " + strings.Join(conditions, " AND ")
	sql := fmt.Sprintf(
		`SELECT id, job_id, name, source, started_at, finished_at, status, exit_code, cost_usd, output_summary, error, model, COALESCE(provider,'') as provider, session_id, COALESCE(output_file,'') as output_file, COALESCE(tokens_in,0) as tokens_in, COALESCE(tokens_out,0) as tokens_out, COALESCE(agent,'') as agent, COALESCE(parent_id,'') as parent_id
		 FROM job_runs %s ORDER BY id DESC LIMIT %d`,
		where, limit)

	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil, err
	}

	var runs []JobRun
	for _, row := range rows {
		runs = append(runs, runFromRow(row))
	}
	return runs, nil
}

// --- Consecutive Fails ---

// ConsecutiveFailResult holds a job and its current consecutive-fail streak.
type ConsecutiveFailResult struct {
	JobID  string
	Name   string
	Streak int
}

// QueryConsecutiveFails returns jobs whose most recent runs are all non-success,
// with a consecutive streak >= threshold (default 3 when threshold <= 0).
func QueryConsecutiveFails(dbPath string, threshold int) ([]ConsecutiveFailResult, error) {
	if threshold <= 0 {
		threshold = 3
	}

	sql := fmt.Sprintf(`SELECT job_id, name, status FROM job_runs
	         WHERE date(started_at, 'localtime') >= date('now', 'localtime', '-30 days')
	           AND status != '%s'
	         ORDER BY job_id, datetime(started_at) DESC, id DESC`, StatusSkippedConcurrentLimit)

	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil, err
	}

	type jobState struct {
		name   string
		streak int
		broken bool
	}
	jobs := map[string]*jobState{}
	order := []string{} // preserve insertion order for stable output

	for _, row := range rows {
		jobID := db.Str(row["job_id"])
		name := db.Str(row["name"])
		status := db.Str(row["status"])

		if _, ok := jobs[jobID]; !ok {
			jobs[jobID] = &jobState{name: name}
			order = append(order, jobID)
		}

		state := jobs[jobID]
		if state.broken {
			continue
		}
		if status == "success" {
			state.broken = true
		} else {
			state.streak++
		}
	}

	var results []ConsecutiveFailResult
	for _, jobID := range order {
		state := jobs[jobID]
		if state.streak >= threshold {
			results = append(results, ConsecutiveFailResult{
				JobID:  jobID,
				Name:   state.name,
				Streak: state.streak,
			})
		}
	}
	return results, nil
}

// --- Job Trace ---

// QueryJobTrace returns the most recent runs for a given jobID, ordered newest first.
// Default limit is 10 when limit <= 0.
func QueryJobTrace(dbPath, jobID string, limit int) ([]JobRun, error) {
	if limit <= 0 {
		limit = 10
	}
	sql := fmt.Sprintf(
		`SELECT id, job_id, name, source, started_at, finished_at, status, exit_code, cost_usd, output_summary, error, model, COALESCE(provider,'') as provider, session_id, COALESCE(output_file,'') as output_file, COALESCE(tokens_in,0) as tokens_in, COALESCE(tokens_out,0) as tokens_out, COALESCE(agent,'') as agent, COALESCE(parent_id,'') as parent_id
		 FROM job_runs WHERE job_id = '%s' ORDER BY id DESC LIMIT %d`,
		db.Escape(jobID), limit)

	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil, err
	}

	var runs []JobRun
	for _, row := range rows {
		runs = append(runs, runFromRow(row))
	}
	return runs, nil
}

// --- Cleanup ---

func Cleanup(dbPath string, days int) error {
	sql := fmt.Sprintf(
		`DELETE FROM job_runs WHERE datetime(started_at) < datetime('now','-%d days')`, days)
	return db.Exec(dbPath, sql)
}

// --- Observability Metrics ---

// QueryMetrics returns aggregate metrics for the last N days.
func QueryMetrics(dbPath string, days int) (*MetricsResult, error) {
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
		 WHERE date(started_at, 'localtime') >= date('now', 'localtime', '-%d days')
		   AND status != '%s'`, days, StatusSkippedConcurrentLimit)

	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return &MetricsResult{}, nil
	}

	r := rows[0]
	total := db.Int(r["total"])
	success := db.Int(r["success"])
	successRate := 0.0
	if total > 0 {
		successRate = float64(success) / float64(total)
	}

	return &MetricsResult{
		TotalTasks:     total,
		SuccessRate:    successRate,
		TotalTokensIn:  db.Int(r["total_tokens_in"]),
		TotalTokensOut: db.Int(r["total_tokens_out"]),
		AvgDurationMs:  int64(db.Float(r["avg_dur_ms"])),
		AvgCostUSD:     db.Float(r["avg_cost"]),
		TotalCostUSD:   db.Float(r["total_cost"]),
	}, nil
}

// QueryDailyMetrics returns per-day breakdowns for the last N days.
func QueryDailyMetrics(dbPath string, days int) ([]DailyMetrics, error) {
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
		   AND status != '%s'
		 GROUP BY day ORDER BY day`, days, StatusSkippedConcurrentLimit)

	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil, err
	}

	var metrics []DailyMetrics
	for _, row := range rows {
		metrics = append(metrics, DailyMetrics{
			Date:      db.Str(row["day"]),
			Tasks:     db.Int(row["total"]),
			Success:   db.Int(row["success"]),
			Errors:    db.Int(row["errors"]),
			Timeouts:  db.Int(row["timeouts"]),
			CostUSD:   db.Float(row["cost"]),
			TokensIn:  db.Int(row["tokens_in"]),
			TokensOut: db.Int(row["tokens_out"]),
			AvgDurMs:  int64(db.Float(row["avg_dur_ms"])),
		})
	}
	return metrics, nil
}

// QueryProviderMetrics returns per-model breakdowns for the last N days.
func QueryProviderMetrics(dbPath string, days int) ([]ProviderMetrics, error) {
	if dbPath == "" {
		return nil, nil
	}
	if days <= 0 {
		days = 30
	}

	sql := fmt.Sprintf(
		`SELECT
			model,
			COALESCE(provider, '') as provider,
			COUNT(*) as total,
			COALESCE(SUM(cost_usd), 0) as total_cost,
			COALESCE(AVG(cost_usd), 0) as avg_cost,
			COALESCE(AVG(CAST(
				(julianday(finished_at) - julianday(started_at)) * 86400000 AS INTEGER
			)), 0) as avg_dur_ms,
			COALESCE(SUM(CASE WHEN status NOT IN ('success', '%s') THEN 1 ELSE 0 END), 0) as errors,
			COALESCE(SUM(tokens_in), 0) as tokens_in,
			COALESCE(SUM(tokens_out), 0) as tokens_out
		 FROM job_runs
		 WHERE date(started_at, 'localtime') >= date('now', 'localtime', '-%d days')
		   AND model != ''
		   AND status != '%s'
		 GROUP BY model, provider ORDER BY total DESC`, StatusSkippedConcurrentLimit, days, StatusSkippedConcurrentLimit)

	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil, err
	}

	var metrics []ProviderMetrics
	for _, row := range rows {
		total := db.Int(row["total"])
		errors := db.Int(row["errors"])
		errorRate := 0.0
		if total > 0 {
			errorRate = float64(errors) / float64(total)
		}
		metrics = append(metrics, ProviderMetrics{
			Model:     db.Str(row["model"]),
			Provider:  db.Str(row["provider"]),
			Tasks:     total,
			TotalCost: db.Float(row["total_cost"]),
			AvgCost:   db.Float(row["avg_cost"]),
			AvgDurMs:  int64(db.Float(row["avg_dur_ms"])),
			ErrorRate: errorRate,
			TokensIn:  db.Int(row["tokens_in"]),
			TokensOut: db.Int(row["tokens_out"]),
		})
	}
	return metrics, nil
}

// QueryParentSubtaskCounts returns subtask counts grouped by parent_id for the given parent IDs.
func QueryParentSubtaskCounts(dbPath string, parentIDs []string) (map[string]SubtaskCount, error) {
	if dbPath == "" || len(parentIDs) == 0 {
		return nil, nil
	}

	inList := ""
	for i, id := range parentIDs {
		if i > 0 {
			inList += ","
		}
		inList += "'" + db.Escape(id) + "'"
	}

	sql := fmt.Sprintf(
		`SELECT parent_id,
		        COUNT(*) as total,
		        SUM(CASE WHEN status = 'success' THEN 1 ELSE 0 END) as completed
		 FROM job_runs
		 WHERE parent_id IN (%s)
		 GROUP BY parent_id`, inList)

	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil, err
	}

	result := make(map[string]SubtaskCount)
	for _, row := range rows {
		pid := db.Str(row["parent_id"])
		if pid != "" {
			result[pid] = SubtaskCount{
				Total:     db.Int(row["total"]),
				Completed: db.Int(row["completed"]),
			}
		}
	}
	return result, nil
}
