package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// --- SQLite Task Management ---
// Uses the system `sqlite3` CLI (macOS built-in) to query the dashboard DB.
// No cgo or external Go modules required.

type DBTask struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Priority  string `json:"priority"`
	CreatedAt string `json:"created_at"`
	Error     string `json:"error"`
}

type TaskStats struct {
	Todo    int `json:"todo"`
	Running int `json:"running"`
	Review  int `json:"review"`
	Done    int `json:"done"`
	Failed  int `json:"failed"`
	Total   int `json:"total"`
}

// queryDB runs a SQL query against the SQLite database and returns JSON rows.
func queryDB(dbPath, sql string) ([]map[string]any, error) {
	cmd := exec.Command("sqlite3", "-json", dbPath, sql)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("sqlite3: %s", string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("sqlite3: %w", err)
	}

	outStr := strings.TrimSpace(string(out))
	if outStr == "" || outStr == "[]" {
		return nil, nil
	}

	var rows []map[string]any
	if err := json.Unmarshal([]byte(outStr), &rows); err != nil {
		return nil, fmt.Errorf("parse sqlite3 output: %w", err)
	}
	return rows, nil
}

// getTaskStats returns aggregate task counts by status.
func getTaskStats(dbPath string) (TaskStats, error) {
	rows, err := queryDB(dbPath,
		`SELECT status, COUNT(*) as cnt FROM tasks GROUP BY status`)
	if err != nil {
		return TaskStats{}, err
	}

	var stats TaskStats
	for _, row := range rows {
		status, _ := row["status"].(string)
		cntVal, _ := row["cnt"].(float64) // JSON numbers are float64
		cnt := int(cntVal)
		switch status {
		case "todo":
			stats.Todo = cnt
		case "running":
			stats.Running = cnt
		case "review":
			stats.Review = cnt
		case "done":
			stats.Done = cnt
		case "failed":
			stats.Failed = cnt
		}
		stats.Total += cnt
	}
	return stats, nil
}

// getTasksByStatus returns tasks matching the given status.
func getTasksByStatus(dbPath, status string) ([]DBTask, error) {
	sql := fmt.Sprintf(
		`SELECT id, name, status, priority, created_at, COALESCE(error,'') as error
		 FROM tasks WHERE status = '%s' ORDER BY priority DESC, created_at DESC LIMIT 20`,
		escapeSQLite(status))
	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return nil, err
	}

	var tasks []DBTask
	for _, row := range rows {
		tasks = append(tasks, DBTask{
			ID:        fmt.Sprintf("%v", row["id"]),
			Name:      fmt.Sprintf("%v", row["name"]),
			Status:    fmt.Sprintf("%v", row["status"]),
			Priority:  fmt.Sprintf("%v", row["priority"]),
			CreatedAt: fmt.Sprintf("%v", row["created_at"]),
			Error:     fmt.Sprintf("%v", row["error"]),
		})
	}
	return tasks, nil
}

// getStuckTasks returns tasks that have been "running" for more than N minutes.
func getStuckTasks(dbPath string, minutes int) ([]DBTask, error) {
	sql := fmt.Sprintf(
		`SELECT id, name, status, priority, created_at, COALESCE(error,'') as error
		 FROM tasks
		 WHERE status = 'running'
		   AND datetime(created_at) < datetime('now', '-%d minutes')
		 ORDER BY created_at ASC`,
		minutes)
	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return nil, err
	}

	var tasks []DBTask
	for _, row := range rows {
		tasks = append(tasks, DBTask{
			ID:        fmt.Sprintf("%v", row["id"]),
			Name:      fmt.Sprintf("%v", row["name"]),
			Status:    fmt.Sprintf("%v", row["status"]),
			Priority:  fmt.Sprintf("%v", row["priority"]),
			CreatedAt: fmt.Sprintf("%v", row["created_at"]),
			Error:     fmt.Sprintf("%v", row["error"]),
		})
	}
	return tasks, nil
}

// updateTaskStatus changes a task's status in the DB.
func updateTaskStatus(dbPath string, id, status, errMsg string) error {
	sql := fmt.Sprintf(
		`UPDATE tasks SET status = '%s', error = '%s', updated_at = datetime('now')
		 WHERE id = %s`,
		escapeSQLite(status), escapeSQLite(errMsg), escapeSQLite(id))
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sqlite3: %s: %w", string(out), err)
	}
	return nil
}

// escapeSQLite sanitizes a string for safe SQLite interpolation.
// Handles single quotes, null bytes, and control characters.
func escapeSQLite(s string) string {
	// Remove null bytes — these can truncate SQL strings.
	s = strings.ReplaceAll(s, "\x00", "")
	// Escape single quotes for SQL.
	s = strings.ReplaceAll(s, "'", "''")
	return s
}
