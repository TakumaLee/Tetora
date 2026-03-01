package main

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// TaskBoardDispatcher auto-dispatches tasks with status=todo and a non-empty assignee.
//
// Polling rules (resource contention prevention):
//   - Scans every Interval (default 5m) via ticker.
//   - If any tasks from the previous cycle are still running (activeCount > 0),
//     the scan is skipped — no new tasks are launched until all current ones finish.
//   - When activeCount drops to zero, doneCh fires an immediate extra scan so the
//     next batch starts without waiting for the full interval.
//   - MaxConcurrentTasks caps how many tasks are started per scan cycle (0 = unlimited).
//   - resetStuckDoing() always runs at scan time to recover from daemon crashes.
type TaskBoardDispatcher struct {
	engine *TaskBoardEngine
	cfg    *Config
	sem      chan struct{}
	childSem chan struct{}
	state    *dispatchState

	mu          sync.Mutex
	wg          sync.WaitGroup // tracks in-flight dispatchTask goroutines
	activeCount atomic.Int32   // number of currently running dispatchTask goroutines
	running     bool
	stopCh      chan struct{}
	doneCh      chan struct{} // signals when activeCount drops to 0 → immediate re-scan
	ctx         context.Context
	cancel      context.CancelFunc
}

func newTaskBoardDispatcher(engine *TaskBoardEngine, cfg *Config, sem, childSem chan struct{}, state *dispatchState) *TaskBoardDispatcher {
	ctx, cancel := context.WithCancel(context.Background())
	return &TaskBoardDispatcher{
		engine:   engine,
		cfg:      cfg,
		sem:      sem,
		childSem: childSem,
		state:    state,
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}, 1), // buffered: at most one pending re-scan signal
		ctx:    ctx,
		cancel: cancel,
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
			case <-d.doneCh:
				// All tasks from the previous cycle finished — re-scan immediately
				// instead of waiting for the next ticker tick.
				logInfo("taskboard auto-dispatch: all tasks done, re-scanning immediately")
				d.scan()
			}
		}
	}()
}

// Stop halts the dispatcher and waits for in-flight tasks to finish.
func (d *TaskBoardDispatcher) Stop() {
	d.mu.Lock()
	if !d.running {
		d.mu.Unlock()
		return
	}
	d.running = false
	close(d.stopCh)
	d.mu.Unlock()

	// Signal all in-flight tasks to cancel, then wait.
	d.cancel()
	d.wg.Wait()
	logInfo("taskboard dispatch: all in-flight tasks finished")
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

func (d *TaskBoardDispatcher) parseStuckThreshold() time.Duration {
	raw := d.engine.config.AutoDispatch.StuckThreshold
	if raw == "" {
		raw = "2h"
	}
	dur, err := time.ParseDuration(raw)
	if err != nil {
		logWarn("invalid stuck threshold, using 2h", "raw", raw, "error", err)
		return 2 * time.Hour
	}
	return dur
}

// resetStuckDoing resets tasks that have been stuck in "doing" longer than StuckThreshold
// back to "todo" so they can be re-dispatched. This handles daemon crash/restart scenarios
// where in-flight tasks never received their completion callback.
func (d *TaskBoardDispatcher) resetStuckDoing() {
	threshold := d.parseStuckThreshold()
	cutoff := time.Now().Add(-threshold).UTC().Format(time.RFC3339)

	sql := fmt.Sprintf(`SELECT id, title FROM tasks WHERE status = 'doing' AND updated_at < '%s'`, cutoff)
	rows, err := queryDB(d.engine.dbPath, sql)
	if err != nil {
		logWarn("taskboard dispatch: resetStuckDoing query failed", "error", err)
		return
	}

	for _, row := range rows {
		id := fmt.Sprintf("%v", row["id"])
		title := fmt.Sprintf("%v", row["title"])

		updateSQL := fmt.Sprintf(
			`UPDATE tasks SET status = 'todo', updated_at = '%s' WHERE id = '%s' AND status = 'doing'`,
			time.Now().UTC().Format(time.RFC3339),
			escapeSQLite(id),
		)
		if err := pragmaDB(d.engine.dbPath); err != nil {
			logWarn("taskboard dispatch: resetStuckDoing pragmaDB failed", "id", id, "error", err)
			continue
		}
		if _, err := queryDB(d.engine.dbPath, updateSQL); err != nil {
			logWarn("taskboard dispatch: failed to reset stuck task", "id", id, "error", err)
			continue
		}

		comment := fmt.Sprintf("[auto-reset] Stuck in 'doing' for >%s (likely daemon restart). Reset to 'todo' for re-dispatch.", threshold)
		if _, err := d.engine.AddComment(id, "system", comment); err != nil {
			logWarn("taskboard dispatch: failed to add reset comment", "id", id, "error", err)
		}

		logInfo("taskboard dispatch: reset stuck doing task", "id", id, "title", title, "threshold", threshold)
	}
}

// scan finds todo tasks with an assignee and dispatches them.
// If any tasks are still running from a previous cycle, the dispatch is skipped
// to avoid resource contention. resetStuckDoing always runs to handle crash recovery.
func (d *TaskBoardDispatcher) scan() {
	// Always reset stuck tasks first (handles crash/restart scenarios regardless of active count).
	d.resetStuckDoing()

	// Skip dispatch if tasks from the previous cycle are still running.
	if n := d.activeCount.Load(); n > 0 {
		logInfo("taskboard dispatch: scan skipped, waiting for running tasks", "active", n)
		return
	}

	tasks, err := d.engine.ListTasks("todo", "", "")
	if err != nil {
		logWarn("taskboard dispatch scan error", "error", err)
		return
	}

	maxTasks := d.engine.config.AutoDispatch.MaxConcurrentTasks
	dispatched := 0

	for _, t := range tasks {
		if t.Assignee == "" {
			continue // skip unassigned tasks
		}
		if maxTasks > 0 && dispatched >= maxTasks {
			logInfo("taskboard dispatch: maxConcurrentTasks reached, deferring remaining tasks", "limit", maxTasks)
			break
		}

		// Move to "doing" before logging success.
		if _, err := d.engine.MoveTask(t.ID, "doing"); err != nil {
			logWarn("taskboard dispatch: failed to move task to doing", "id", t.ID, "error", err)
			continue
		}

		logInfo("taskboard dispatch: picking up task", "id", t.ID, "title", t.Title, "assignee", t.Assignee)
		dispatched++

		// Dispatch in a goroutine with panic recovery.
		d.wg.Add(1)
		d.activeCount.Add(1)
		go func(task TaskBoard) {
			defer d.wg.Done()
			defer func() {
				// Decrement active count; signal for immediate re-scan if last task done.
				if remaining := d.activeCount.Add(-1); remaining == 0 {
					select {
					case d.doneCh <- struct{}{}:
					default: // already signaled, skip
					}
				}
			}()
			defer func() {
				if r := recover(); r != nil {
					logError("taskboard dispatch: panic in dispatchTask", "id", task.ID, "recover", r)
					if _, err := d.engine.MoveTask(task.ID, "failed"); err != nil {
						logWarn("taskboard dispatch: failed to move panicked task to failed", "id", task.ID, "error", err)
					}
				}
			}()
			d.dispatchTask(task)
		}(t)
	}
}

func (d *TaskBoardDispatcher) dispatchTask(t TaskBoard) {
	ctx := d.ctx // use dispatcher context (cancelled on Stop)

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
	result := runSingleTask(ctx, d.cfg, task, d.sem, d.childSem, t.Assignee)
	duration := time.Since(start)

	// Record cost/duration on the board task.
	costSQL := fmt.Sprintf(`
		UPDATE tasks SET cost_usd = %.6f, duration_ms = %d, session_id = '%s', updated_at = '%s'
		WHERE id = '%s'
	`,
		result.CostUSD,
		result.DurationMs,
		escapeSQLite(result.SessionID),
		time.Now().UTC().Format(time.RFC3339),
		escapeSQLite(t.ID),
	)
	if err := pragmaDB(d.engine.dbPath); err != nil {
		logWarn("taskboard dispatch: pragmaDB failed", "id", t.ID, "error", err)
	}
	if _, err := queryDB(d.engine.dbPath, costSQL); err != nil {
		logWarn("taskboard dispatch: failed to record cost/duration", "id", t.ID, "error", err)
	}

	if result.Status == "success" || result.ExitCode == 0 {
		// Move to "done".
		if _, err := d.engine.MoveTask(t.ID, "done"); err != nil {
			logWarn("taskboard dispatch: failed to move task to done", "id", t.ID, "error", err)
		}

		// Add result as comment.
		output := result.Output
		if len(output) > 2000 {
			output = output[:2000] + "\n... (truncated)"
		}
		comment := fmt.Sprintf("Task completed in %s (cost: $%.4f)\n\n%s", duration.Round(time.Second), result.CostUSD, output)
		if _, err := d.engine.AddComment(t.ID, t.Assignee, comment); err != nil {
			logWarn("taskboard dispatch: failed to add completion comment", "id", t.ID, "error", err)
		}

		logInfo("taskboard dispatch: task completed", "id", t.ID, "cost", result.CostUSD, "duration", duration.Round(time.Second))
	} else {
		// Move to "failed".
		if _, err := d.engine.MoveTask(t.ID, "failed"); err != nil {
			logWarn("taskboard dispatch: failed to move task to failed", "id", t.ID, "error", err)
		}

		errMsg := result.Error
		if errMsg == "" {
			errMsg = result.Output
		}
		if len(errMsg) > 2000 {
			errMsg = errMsg[:2000] + "\n... (truncated)"
		}
		comment := fmt.Sprintf("Task failed (exit code: %d, duration: %s)\n\n%s", result.ExitCode, duration.Round(time.Second), errMsg)
		if _, err := d.engine.AddComment(t.ID, t.Assignee, comment); err != nil {
			logWarn("taskboard dispatch: failed to add failure comment", "id", t.ID, "error", err)
		}

		logWarn("taskboard dispatch: task failed", "id", t.ID, "error", result.Error)

		// Auto-retry if enabled.
		d.engine.AutoRetryFailed()
	}
}
