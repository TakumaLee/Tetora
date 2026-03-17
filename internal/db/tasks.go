package db

import "fmt"

// GetTaskStats returns aggregate task counts by status.
func GetTaskStats(dbPath string) (TaskStats, error) {
	rows, err := Query(dbPath,
		`SELECT status, COUNT(*) as cnt FROM tasks GROUP BY status`)
	if err != nil {
		return TaskStats{}, err
	}

	var stats TaskStats
	for _, row := range rows {
		status, _ := row["status"].(string)
		cntVal, _ := row["cnt"].(float64)
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

// GetTasksByStatus returns tasks matching the given status.
func GetTasksByStatus(dbPath, status string) ([]Task, error) {
	sql := fmt.Sprintf(
		`SELECT id, title, status, priority, created_at, COALESCE(error,'') as error
		 FROM tasks WHERE status = '%s' ORDER BY priority DESC, created_at DESC LIMIT 20`,
		Escape(status))
	rows, err := Query(dbPath, sql)
	if err != nil {
		return nil, err
	}

	var tasks []Task
	for _, row := range rows {
		tasks = append(tasks, Task{
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

// GetStuckTasks returns tasks that have been "running" for more than N minutes.
func GetStuckTasks(dbPath string, minutes int) ([]Task, error) {
	sql := fmt.Sprintf(
		`SELECT id, title, status, priority, created_at, COALESCE(error,'') as error
		 FROM tasks
		 WHERE status = 'doing'
		   AND datetime(created_at) < datetime('now', '-%d minutes')
		 ORDER BY created_at ASC`,
		minutes)
	rows, err := Query(dbPath, sql)
	if err != nil {
		return nil, err
	}

	var tasks []Task
	for _, row := range rows {
		tasks = append(tasks, Task{
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

// UpdateTaskStatus changes a task's status in the DB.
func UpdateTaskStatus(dbPath string, id, status, errMsg string) error {
	sql := fmt.Sprintf(
		`UPDATE tasks SET status = '%s', error = '%s', updated_at = datetime('now')
		 WHERE id = %s`,
		Escape(status), Escape(errMsg), Escape(id))
	return Exec(dbPath, sql)
}
