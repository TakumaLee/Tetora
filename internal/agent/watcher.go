package agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"tetora/internal/db"
)

// WatchConfig holds the parameters for the task watcher.
type WatchConfig struct {
	HistoryDB     string
	AgentsDir     string
	ClaudePath    string
	Interval      time.Duration
	MaxConcurrent int // upper bound of in-flight claude spawns; 0 → defaultMaxConcurrent
}

const defaultMaxConcurrent = 4

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
	maxConc := cfg.MaxConcurrent
	if maxConc <= 0 {
		maxConc = defaultMaxConcurrent
	}
	sem := make(chan struct{}, maxConc)
	fmt.Printf("Watcher started (interval: %s, db: %s, maxConcurrent: %d)\n", interval, cfg.HistoryDB, maxConc)

	for {
		if err := poll(ctx, cfg, sem); err != nil {
			fmt.Fprintf(os.Stderr, "poll error: %v\n", err)
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(interval):
		}
	}
}

// poll fetches assigned tasks and spawns one agent process per task, capped by
// the semaphore.
func poll(ctx context.Context, cfg WatchConfig, sem chan struct{}) error {
	tasks, err := fetchAssignedTasks(cfg.HistoryDB)
	if err != nil {
		return err
	}
	for _, t := range tasks {
		select {
		case <-ctx.Done():
			return nil
		case sem <- struct{}{}:
		}
		if err := spawnAgent(cfg, t, sem); err != nil {
			<-sem // release slot on error
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

// resolveAgentDir validates the assignee resolves to a path strictly inside
// agentsDir. This blocks path-traversal payloads (e.g. assignee="../../etc")
// that would otherwise direct claude at an arbitrary directory.
func resolveAgentDir(agentsDir, assignee string) (string, error) {
	if assignee == "" || strings.ContainsAny(assignee, "/\\") || assignee == "." || assignee == ".." {
		return "", fmt.Errorf("invalid assignee %q", assignee)
	}
	root, err := filepath.Abs(filepath.Clean(agentsDir))
	if err != nil {
		return "", err
	}
	candidate, err := filepath.Abs(filepath.Join(root, assignee))
	if err != nil {
		return "", err
	}
	rootWithSep := root + string(os.PathSeparator)
	if candidate != root && !strings.HasPrefix(candidate, rootWithSep) {
		return "", fmt.Errorf("assignee %q escapes agents dir", assignee)
	}
	return candidate, nil
}

// spawnAgent marks the task "doing" (optimistic lock) then spawns claude in the
// agent's directory. The claude process runs asynchronously and releases the
// concurrency slot on exit.
func spawnAgent(cfg WatchConfig, t watchedTask, sem chan struct{}) error {
	agentDir, err := resolveAgentDir(cfg.AgentsDir, t.Assignee)
	if err != nil {
		return err
	}
	if _, err := os.Stat(agentDir); os.IsNotExist(err) {
		return fmt.Errorf("agent dir not found: %s (run: tetora agent configure %s)", agentDir, t.Assignee)
	}

	// Optimistic update: only proceed if we win the status transition.
	affected, err := markTaskDoing(cfg.HistoryDB, t.ID)
	if err != nil {
		return fmt.Errorf("mark doing: %w", err)
	}
	if !affected {
		// Another process already claimed this task; release the slot we acquired.
		<-sem
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

	// Reap the child process and release the concurrency slot when done.
	go func() {
		_ = cmd.Wait()
		<-sem
	}()

	return nil
}

// markTaskDoing atomically moves a task from "todo" to "doing".
// Returns true if the update actually changed a row (i.e., we won the race).
//
// Implemented as a single sqlite3 invocation that runs the UPDATE then queries
// changes() in the same connection — this keeps the read of the row count
// linearizable with the write, unlike a separate verification SELECT.
var markTaskDoingMu sync.Mutex

func markTaskDoing(dbPath, taskID string) (bool, error) {
	// Serialise local callers to avoid two goroutines racing on the same row;
	// other processes are still guarded by the WHERE status='todo' clause.
	markTaskDoingMu.Lock()
	defer markTaskDoingMu.Unlock()

	rows, err := db.Query(dbPath, fmt.Sprintf(
		`UPDATE tasks SET status = 'doing', updated_at = datetime('now') WHERE id = '%s' AND status = 'todo'; SELECT changes() AS changed;`,
		db.Escape(taskID),
	))
	if err != nil {
		return false, err
	}
	if len(rows) == 0 {
		return false, nil
	}
	return db.Int(rows[0]["changed"]) > 0, nil
}

func buildTaskPrompt(t watchedTask) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Task ID: %s\n", t.ID))
	sb.WriteString(fmt.Sprintf("Title: %s\n", t.Title))
	if t.Description != "" {
		// Wrap in XML delimiters to raise the bar for prompt injection via
		// crafted DB values. The model is instructed to treat this block as data.
		sb.WriteString("\n<task-description>\n")
		sb.WriteString(t.Description)
		sb.WriteString("\n</task-description>\n")
	}
	return sb.String()
}
