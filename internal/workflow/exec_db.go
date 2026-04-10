package workflow

import (
	"encoding/json"
	"fmt"
	"strings"

	"tetora/internal/db"
	"tetora/internal/log"
)

const workflowRunsTableSQL = `CREATE TABLE IF NOT EXISTS workflow_runs (
  id TEXT PRIMARY KEY,
  workflow_name TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'running',
  started_at TEXT NOT NULL,
  finished_at TEXT DEFAULT '',
  duration_ms INTEGER DEFAULT 0,
  total_cost REAL DEFAULT 0,
  variables TEXT DEFAULT '{}',
  step_results TEXT DEFAULT '{}',
  error TEXT DEFAULT '',
  created_at TEXT NOT NULL
)`

// InitWorkflowRunsTable creates the workflow_runs table and applies migrations.
func InitWorkflowRunsTable(dbPath string) {
	if dbPath == "" {
		return
	}
	if err := db.Exec(dbPath, workflowRunsTableSQL); err != nil {
		log.Warn("init workflow_runs table failed", "error", err)
	}
	// Migration: add resumed_from column (no-op if column already exists).
	if err := db.Exec(dbPath, "ALTER TABLE workflow_runs ADD COLUMN resumed_from TEXT DEFAULT ''"); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") {
			log.Warn("migration: add resumed_from column failed", "error", err)
		}
	}
}

// RecordWorkflowRun upserts a workflow run record into the DB.
func RecordWorkflowRun(dbPath string, run *WorkflowRun) {
	if dbPath == "" {
		return
	}
	InitWorkflowRunsTable(dbPath)

	varsJSON, _ := json.Marshal(run.Variables)
	stepsJSON, _ := json.Marshal(run.StepResults)

	sql := fmt.Sprintf(
		`INSERT OR REPLACE INTO workflow_runs (id, workflow_name, status, started_at, finished_at, duration_ms, total_cost, variables, step_results, error, created_at, resumed_from)
		 VALUES ('%s','%s','%s','%s','%s',%d,%f,'%s','%s','%s','%s','%s')`,
		db.Escape(run.ID),
		db.Escape(run.WorkflowName),
		db.Escape(run.Status),
		db.Escape(run.StartedAt),
		db.Escape(run.FinishedAt),
		run.DurationMs,
		run.TotalCost,
		db.Escape(string(varsJSON)),
		db.Escape(string(stepsJSON)),
		db.Escape(run.Error),
		db.Escape(run.StartedAt),
		db.Escape(run.ResumedFrom),
	)

	if err := db.Exec(dbPath, sql); err != nil {
		log.Warn("record workflow run failed", "error", err)
	}
}

// QueryWorkflowRuns returns recent workflow runs, optionally filtered by workflow name.
func QueryWorkflowRuns(dbPath string, limit int, workflowName string) ([]WorkflowRun, error) {
	if limit <= 0 {
		limit = 20
	}

	where := ""
	if workflowName != "" {
		where = fmt.Sprintf("WHERE workflow_name='%s'", db.Escape(workflowName))
	}

	sql := fmt.Sprintf(
		`SELECT id, workflow_name, status, started_at, finished_at, duration_ms, total_cost, variables, step_results, error, COALESCE(resumed_from,'') as resumed_from
		 FROM workflow_runs %s ORDER BY created_at DESC LIMIT %d`,
		where, limit,
	)

	rows, err := db.Query(dbPath, sql)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil, nil
		}
		return nil, err
	}

	var runs []WorkflowRun
	for _, row := range rows {
		run := WorkflowRun{
			ID:           jsonStr(row["id"]),
			WorkflowName: jsonStr(row["workflow_name"]),
			Status:       jsonStr(row["status"]),
			StartedAt:    jsonStr(row["started_at"]),
			FinishedAt:   jsonStr(row["finished_at"]),
			DurationMs:   int64(jsonFloat(row["duration_ms"])),
			TotalCost:    jsonFloat(row["total_cost"]),
			Error:        jsonStr(row["error"]),
			StepResults:  make(map[string]*StepRunResult),
			ResumedFrom:  jsonStr(row["resumed_from"]),
		}
		if v := jsonStr(row["variables"]); v != "" {
			json.Unmarshal([]byte(v), &run.Variables)
		}
		if v := jsonStr(row["step_results"]); v != "" {
			json.Unmarshal([]byte(v), &run.StepResults)
		}
		runs = append(runs, run)
	}
	return runs, nil
}

// QueryWorkflowRunByID returns a single workflow run by ID.
func QueryWorkflowRunByID(dbPath, id string) (*WorkflowRun, error) {
	sql := fmt.Sprintf(
		`SELECT id, workflow_name, status, started_at, finished_at, duration_ms, total_cost, variables, step_results, error, COALESCE(resumed_from,'') as resumed_from
		 FROM workflow_runs WHERE id='%s'`,
		db.Escape(id),
	)

	rows, err := db.Query(dbPath, sql)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil, fmt.Errorf("workflow run %q not found", id)
		}
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("workflow run %q not found", id)
	}

	row := rows[0]
	run := &WorkflowRun{
		ID:           jsonStr(row["id"]),
		WorkflowName: jsonStr(row["workflow_name"]),
		Status:       jsonStr(row["status"]),
		StartedAt:    jsonStr(row["started_at"]),
		FinishedAt:   jsonStr(row["finished_at"]),
		DurationMs:   int64(jsonFloat(row["duration_ms"])),
		TotalCost:    jsonFloat(row["total_cost"]),
		Error:        jsonStr(row["error"]),
		StepResults:  make(map[string]*StepRunResult),
		ResumedFrom:  jsonStr(row["resumed_from"]),
	}
	if v := jsonStr(row["variables"]); v != "" {
		json.Unmarshal([]byte(v), &run.Variables)
	}
	if v := jsonStr(row["step_results"]); v != "" {
		json.Unmarshal([]byte(v), &run.StepResults)
	}
	return run, nil
}

// markRunResumed updates the original run's status to "resumed" in the DB.
func markRunResumed(dbPath, originalRunID, newRunID string) {
	if dbPath == "" {
		return
	}
	sql := fmt.Sprintf(
		`UPDATE workflow_runs SET status='resumed', error='resumed as %s' WHERE id='%s'`,
		db.Escape(newRunID), db.Escape(originalRunID),
	)
	if err := db.Exec(dbPath, sql); err != nil {
		log.Warn("markRunResumed: failed to mark original as resumed", "error", err)
	}
}

// --- DB value helpers ---

func jsonStr(v any) string {
	if v == nil {
		return ""
	}
	s := fmt.Sprintf("%v", v)
	if s == "<nil>" {
		return ""
	}
	return s
}

func jsonFloat(v any) float64 {
	if v == nil {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	case string:
		var f float64
		fmt.Sscanf(n, "%f", &f)
		return f
	}
	var f float64
	fmt.Sscanf(fmt.Sprintf("%v", v), "%f", &f)
	return f
}
