package main

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// TaskBoardDispatcher auto-dispatches tasks with status=todo and a non-empty assignee.
type TaskBoardDispatcher struct {
	engine *TaskBoardEngine
	cfg    *Config
	sem    chan struct{}
	state  *dispatchState

	mu      sync.Mutex
	running bool
	stopCh  chan struct{}
}

func newTaskBoardDispatcher(engine *TaskBoardEngine, cfg *Config, sem chan struct{}, state *dispatchState) *TaskBoardDispatcher {
	return &TaskBoardDispatcher{
		engine: engine,
		cfg:    cfg,
		sem:    sem,
		state:  state,
		stopCh: make(chan struct{}),
	}
}

// Start begins the auto-dispatch loop.
func (d *TaskBoardDispatcher) Start() {
	d.mu.Lock()
	if d.running {
		d.mu.Unlock()
		return
	}
	d.running = true
	d.mu.Unlock()

	interval := d.parseInterval()
	logInfo("taskboard auto-dispatch started", "interval", interval.String())

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-d.stopCh:
				logInfo("taskboard auto-dispatch stopped")
				return
			case <-ticker.C:
				d.scan()
			}
		}
	}()
}

// Stop halts the dispatcher.
func (d *TaskBoardDispatcher) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.running {
		close(d.stopCh)
		d.running = false
	}
}

func (d *TaskBoardDispatcher) parseInterval() time.Duration {
	raw := d.engine.config.AutoDispatch.Interval
	if raw == "" {
		raw = "5m"
	}
	dur, err := time.ParseDuration(raw)
	if err != nil {
		logWarn("invalid dispatch interval, using 5m", "raw", raw, "error", err)
		return 5 * time.Minute
	}
	return dur
}

// scan finds todo tasks with an assignee and dispatches them.
func (d *TaskBoardDispatcher) scan() {
	tasks, err := d.engine.ListTasks("todo", "", "")
	if err != nil {
		logWarn("taskboard dispatch scan error", "error", err)
		return
	}

	for _, t := range tasks {
		if t.Assignee == "" {
			continue // skip unassigned tasks
		}

		logInfo("taskboard dispatch: picking up task", "id", t.ID, "title", t.Title, "assignee", t.Assignee)

		// Move to "doing".
		if _, err := d.engine.MoveTask(t.ID, "doing"); err != nil {
			logWarn("taskboard dispatch: failed to move task to doing", "id", t.ID, "error", err)
			continue
		}

		// Dispatch in a goroutine.
		go d.dispatchTask(t)
	}
}

func (d *TaskBoardDispatcher) dispatchTask(t TaskBoard) {
	ctx := context.Background()

	// Build the dispatch task.
	prompt := t.Title
	if t.Description != "" {
		prompt = t.Title + "\n\n" + t.Description
	}

	task := Task{
		Name:   "board:" + t.ID,
		Prompt: prompt,
		Agent:  t.Assignee,
		Source: "taskboard",
	}
	fillDefaults(d.cfg, &task)

	// Apply taskboard-specific cost controls.
	// Priority: per-task model > dispatch defaultModel > agent model > global defaultModel.
	dispatchCfg := d.engine.config.AutoDispatch
	if t.Model != "" {
		task.Model = t.Model
	} else if dispatchCfg.DefaultModel != "" {
		task.Model = dispatchCfg.DefaultModel
	}
	if dispatchCfg.MaxBudget > 0 && (task.Budget == 0 || task.Budget > dispatchCfg.MaxBudget) {
		task.Budget = dispatchCfg.MaxBudget
	}

	// Look up project workdir.
	if t.Project != "" && t.Project != "default" {
		p, err := getProject(d.cfg.HistoryDB, t.Project)
		if err == nil && p != nil && p.Workdir != "" {
			task.Workdir = p.Workdir
		}
	}

	start := time.Now()
	result := runSingleTask(ctx, d.cfg, task, d.sem, t.Assignee)
	duration := time.Since(start)

	// Record cost/duration on the board task.
	costSQL := fmt.Sprintf(`
		UPDATE tasks SET cost_usd = %f, duration_ms = %d, session_id = '%s', updated_at = '%s'
		WHERE id = '%s'
	`,
		result.CostUSD,
		result.DurationMs,
		escapeSQLite(result.SessionID),
		time.Now().UTC().Format(time.RFC3339),
		escapeSQLite(t.ID),
	)
	pragmaDB(d.engine.dbPath)
	queryDB(d.engine.dbPath, costSQL)

	if result.Status == "success" || result.ExitCode == 0 {
		// Move to "done".
		d.engine.MoveTask(t.ID, "done")

		// Add result as comment.
		output := result.Output
		if len(output) > 2000 {
			output = output[:2000] + "\n... (truncated)"
		}
		comment := fmt.Sprintf("Task completed in %s (cost: $%.4f)\n\n%s", duration.Round(time.Second), result.CostUSD, output)
		d.engine.AddComment(t.ID, t.Assignee, comment)

		logInfo("taskboard dispatch: task completed", "id", t.ID, "cost", result.CostUSD, "duration", duration.Round(time.Second))
	} else {
		// Move to "failed".
		d.engine.MoveTask(t.ID, "failed")

		errMsg := result.Error
		if errMsg == "" {
			errMsg = result.Output
		}
		if len(errMsg) > 2000 {
			errMsg = errMsg[:2000] + "\n... (truncated)"
		}
		comment := fmt.Sprintf("Task failed (exit code: %d, duration: %s)\n\n%s", result.ExitCode, duration.Round(time.Second), errMsg)
		d.engine.AddComment(t.ID, t.Assignee, comment)

		logWarn("taskboard dispatch: task failed", "id", t.ID, "error", result.Error)

		// Auto-retry if enabled.
		d.engine.AutoRetryFailed()
	}
}
