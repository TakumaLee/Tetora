package main

import (
	"fmt"

	"tetora/internal/db"
)

// --- SQLite Task Management ---
// Uses the system `sqlite3` CLI (macOS built-in) to query the dashboard DB.
// No cgo or external Go modules required.

// DBTask is an alias for db.Task for backward compatibility.
type DBTask = db.Task

// TaskStats is an alias for db.TaskStats for backward compatibility.
type TaskStats = db.TaskStats

// execDB delegates to db.Exec.
func execDB(dbPath, sql string) error {
	return db.Exec(dbPath, sql)
}

// queryDB delegates to db.Query.
func queryDB(dbPath, sql string) ([]map[string]any, error) {
	return db.Query(dbPath, sql)
}

// pragmaDB delegates to db.Pragma.
func pragmaDB(dbPath string) error {
	return db.Pragma(dbPath)
}

// escapeSQLite delegates to db.Escape.
func escapeSQLite(s string) string {
	return db.Escape(s)
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
		case "doing":
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
		`SELECT id, title, status, priority, created_at, COALESCE(error,'') as error
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
			Title:     fmt.Sprintf("%v", row["title"]),
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
		`SELECT id, title, status, priority, created_at, COALESCE(error,'') as error
		 FROM tasks
		 WHERE status = 'doing'
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
			Title:     fmt.Sprintf("%v", row["title"]),
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
	return execDB(dbPath, sql)
}
