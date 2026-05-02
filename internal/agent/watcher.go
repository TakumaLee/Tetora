package agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"tetora/internal/db"
)

// WatchConfig holds the parameters for the task watcher.
type WatchConfig struct {
	HistoryDB  string
	AgentsDir  string
	ClaudePath string
	Interval   time.Duration
}

// watchedTask is a minimal view of a taskboard row for the watcher.
type watchedTask struct {
	ID          string
	Title       string
	Description string
	Assignee    string
}

// Watch polls the taskboard and spawns claude in the agent directory for every
// task that has status "todo" and a non-empty assignee. It blocks until ctx is
// cancelled.
func Watch(ctx context.Context, cfg WatchConfig) error {
	interval := cfg.Interval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	fmt.Printf("Watcher started (interval: %s, db: %s)\n", interval, cfg.HistoryDB)

	for {
		if err := poll(ctx, cfg); err != nil {
			fmt.Fprintf(os.Stderr, "poll error: %v\n", err)
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(interval):
		}
	}
}

// poll fetches assigned tasks and spawns one agent process per task.
func poll(ctx context.Context, cfg WatchConfig) error {
	tasks, err := fetchAssignedTasks(cfg.HistoryDB)
	if err != nil {
		return err
	}
	for _, t := range tasks {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		if err := spawnAgent(cfg, t); err != nil {
			fmt.Fprintf(os.Stderr, "spawn %s for task %s: %v\n", t.Assignee, t.ID, err)
		}
	}
	return nil
}

// fetchAssignedTasks returns tasks with status "todo" that have a non-empty assignee.
func fetchAssignedTasks(dbPath string) ([]watchedTask, error) {
	rows, err := db.Query(dbPath, `
		SELECT id, title, description, assignee
		FROM tasks
		WHERE status = 'todo' AND assignee != ''
		ORDER BY
			CASE priority
				WHEN 'urgent' THEN 1
				WHEN 'high'   THEN 2
				WHEN 'normal' THEN 3
				WHEN 'low'    THEN 4
				ELSE 5
			END,
			created_at
	`)
	if err != nil {
		return nil, fmt.Errorf("query tasks: %w", err)
	}
	tasks := make([]watchedTask, 0, len(rows))
	for _, row := range rows {
		tasks = append(tasks, watchedTask{
			ID:          db.Str(row["id"]),
			Title:       db.Str(row["title"]),
			Description: db.Str(row["description"]),
			Assignee:    db.Str(row["assignee"]),
		})
	}
	return tasks, nil
}

// spawnAgent marks the task "doing" (optimistic lock) then spawns claude in the
// agent's directory. The claude process runs asynchronously.
func spawnAgent(cfg WatchConfig, t watchedTask) error {
	agentDir := filepath.Join(cfg.AgentsDir, t.Assignee)
	if _, err := os.Stat(agentDir); os.IsNotExist(err) {
		return fmt.Errorf("agent dir not found: %s (run: tetora agent configure %s)", agentDir, t.Assignee)
	}

	// Optimistic update: only proceed if we win the status transition.
	affected, err := markTaskDoing(cfg.HistoryDB, t.ID)
	if err != nil {
		return fmt.Errorf("mark doing: %w", err)
	}
	if !affected {
		// Another process already claimed this task.
		return nil
	}

	prompt := buildTaskPrompt(t)
	cmd := exec.Command(cfg.ClaudePath, "--cwd", agentDir, "-p", prompt)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	fmt.Printf("[watcher] spawning %s → task %s: %s\n", t.Assignee, t.ID, t.Title)
	if err := cmd.Start(); err != nil {
		// Roll back to "todo" so the task can be retried.
		_ = db.Exec(cfg.HistoryDB, fmt.Sprintf(
			`UPDATE tasks SET status = 'todo', updated_at = datetime('now') WHERE id = '%s'`,
			db.Escape(t.ID),
		))
		return fmt.Errorf("start claude: %w", err)
	}

	// Reap the child process to prevent zombies.
	go func() { _ = cmd.Wait() }()

	return nil
}

// markTaskDoing atomically moves a task from "todo" to "doing".
// Returns true if the update actually changed a row (i.e., we won the race).
func markTaskDoing(dbPath, taskID string) (bool, error) {
	// Read the current status first to detect the race.
	rows, err := db.Query(dbPath, fmt.Sprintf(
		`SELECT status FROM tasks WHERE id = '%s'`, db.Escape(taskID),
	))
	if err != nil {
		return false, err
	}
	if len(rows) == 0 {
		return false, nil
	}
	if db.Str(rows[0]["status"]) != "todo" {
		return false, nil
	}

	if err := db.Exec(dbPath, fmt.Sprintf(
		`UPDATE tasks SET status = 'doing', updated_at = datetime('now') WHERE id = '%s' AND status = 'todo'`,
		db.Escape(taskID),
	)); err != nil {
		return false, err
	}

	// Verify the update actually applied (the conditional WHERE guards against races).
	rows2, err := db.Query(dbPath, fmt.Sprintf(
		`SELECT status FROM tasks WHERE id = '%s'`, db.Escape(taskID),
	))
	if err != nil || len(rows2) == 0 {
		return false, err
	}
	return db.Str(rows2[0]["status"]) == "doing", nil
}

func buildTaskPrompt(t watchedTask) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Task ID: %s\n", t.ID))
	sb.WriteString(fmt.Sprintf("Title: %s\n", t.Title))
	if t.Description != "" {
		sb.WriteString("\n")
		sb.WriteString(t.Description)
		sb.WriteString("\n")
	}
	return sb.String()
}
