package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
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

	mu            sync.Mutex
	wg            sync.WaitGroup // tracks in-flight dispatchTask goroutines
	activeCount   atomic.Int32   // number of currently running dispatchTask goroutines
	running       bool
	stopCh        chan struct{}
	doneCh        chan struct{} // signals when activeCount drops to 0 → immediate re-scan
	lastTriageAt  time.Time    // last backlog triage time (cooldown tracking)
	ctx           context.Context
	cancel        context.CancelFunc
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

	// On startup, reset ALL "doing" tasks to "todo". The daemon just started so
	// no legitimate in-flight tasks can exist — these are orphans from a previous
	// crash or forced shutdown where the completion handler never ran.
	d.resetOrphanedDoing()

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

// resetOrphanedDoing handles tasks stuck in "doing" at daemon startup.
// Instead of unconditionally resetting all doing tasks, it checks for evidence
// of completion (cost/duration/session data) and applies a grace period to avoid
// resetting tasks that were killed mid-completion during a restart.
func (d *TaskBoardDispatcher) resetOrphanedDoing() {
	sql := `SELECT id, title, completed_at, cost_usd, duration_ms, session_id, updated_at FROM tasks WHERE status = 'doing'`
	rows, err := queryDB(d.engine.dbPath, sql)
	if err != nil {
		logWarn("taskboard dispatch: resetOrphanedDoing query failed", "error", err)
		return
	}
	if len(rows) == 0 {
		return
	}

	now := time.Now().UTC()
	nowISO := escapeSQLite(now.Format(time.RFC3339))
	gracePeriod := 2 * time.Minute

	for _, row := range rows {
		id := fmt.Sprintf("%v", row["id"])
		title := fmt.Sprintf("%v", row["title"])
		completedAt := fmt.Sprintf("%v", row["completed_at"])
		costUSD := getFloat64(row, "cost_usd")
		durationMs := getFloat64(row, "duration_ms")
		sessionID := fmt.Sprintf("%v", row["session_id"])
		updatedAt := fmt.Sprintf("%v", row["updated_at"])

		// Evidence of completion: task has cost/duration/session data or completed_at.
		// This means the subprocess finished but the daemon was killed before the
		// final status update. Restore to "done" instead of wasting work.
		hasCompletionEvidence := (completedAt != "" && completedAt != "<nil>") ||
			costUSD > 0 || durationMs > 0 ||
			(sessionID != "" && sessionID != "<nil>")

		if hasCompletionEvidence {
			updateSQL := fmt.Sprintf(
				`UPDATE tasks SET status = 'done', updated_at = '%s', completed_at = CASE WHEN completed_at = '' THEN '%s' ELSE completed_at END WHERE id = '%s' AND status = 'doing'`,
				nowISO, nowISO, escapeSQLite(id),
			)
			if err := execDB(d.engine.dbPath, updateSQL); err != nil {
				logWarn("taskboard dispatch: failed to restore completed task", "id", id, "error", err)
				continue
			}
			comment := fmt.Sprintf("[auto-restore] Task had completion evidence (cost=$%.4f, duration=%dms, session=%s) but was in 'doing' at startup. Restored to 'done'.",
				costUSD, int64(durationMs), sessionID)
			if _, err := d.engine.AddComment(id, "system", comment); err != nil {
				logWarn("taskboard dispatch: failed to add restore comment", "id", id, "error", err)
			}
			logInfo("taskboard dispatch: restored completed task from doing", "id", id, "title", title)
			continue
		}

		// Grace period: skip tasks updated very recently. They might be in a
		// transient state from the previous daemon instance's shutdown sequence.
		// resetStuckDoing() will catch them later if they're truly stuck.
		if t, err := time.Parse(time.RFC3339, updatedAt); err == nil {
			if now.Sub(t) < gracePeriod {
				logInfo("taskboard dispatch: skipping recently-updated doing task (grace period)",
					"id", id, "title", title, "updatedAt", updatedAt, "age", now.Sub(t).Round(time.Second))
				continue
			}
		}

		// Truly orphaned: no completion evidence and not recent. Reset to todo.
		updateSQL := fmt.Sprintf(
			`UPDATE tasks SET status = 'todo', updated_at = '%s' WHERE id = '%s' AND status = 'doing'`,
			nowISO, escapeSQLite(id),
		)
		if err := execDB(d.engine.dbPath, updateSQL); err != nil {
			logWarn("taskboard dispatch: failed to reset orphaned task", "id", id, "error", err)
			continue
		}
		comment := "[auto-reset] Orphaned in 'doing' at daemon startup (no completion evidence, past grace period). Reset to 'todo' for re-dispatch."
		if _, err := d.engine.AddComment(id, "system", comment); err != nil {
			logWarn("taskboard dispatch: failed to add orphan reset comment", "id", id, "error", err)
		}
		logInfo("taskboard dispatch: reset orphaned doing task", "id", id, "title", title)
	}
}

// resetStuckDoing resets tasks that have been stuck in "doing" longer than StuckThreshold
// back to "todo" so they can be re-dispatched. This handles daemon crash/restart scenarios
// where in-flight tasks never received their completion callback.
func (d *TaskBoardDispatcher) resetStuckDoing() {
	threshold := d.parseStuckThreshold()
	cutoff := time.Now().Add(-threshold).UTC().Format(time.RFC3339)

	sql := fmt.Sprintf(`SELECT id, title FROM tasks WHERE status = 'doing' AND updated_at < '%s'`, escapeSQLite(cutoff))
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
			escapeSQLite(time.Now().UTC().Format(time.RFC3339)),
			escapeSQLite(id),
		)
		if err := execDB(d.engine.dbPath, updateSQL); err != nil {
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
	if len(tasks) == 0 {
		logDebug("taskboard dispatch: scan found no todo tasks")
		d.triageBacklog()
		return
	}

	maxTasks := d.engine.config.AutoDispatch.MaxConcurrentTasks
	if maxTasks <= 0 {
		maxTasks = 3
	}
	dispatched := 0

	for _, t := range tasks {
		if t.Assignee == "" {
			logInfo("taskboard dispatch: skipping unassigned task", "id", t.ID, "title", t.Title)
			continue
		}
		if dispatched >= maxTasks {
			logInfo("taskboard dispatch: maxConcurrentTasks reached, deferring remaining tasks", "limit", maxTasks)
			break
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

	// Inject dependency context from completed upstream tasks.
	if len(t.DependsOn) > 0 {
		depContext := d.buildDependencyContext(t.DependsOn)
		if depContext != "" {
			prompt += "\n\n## Previous Task Results\n" + depContext
		}
	}

	taskID := t.ID // capture for closure
	task := Task{
		Name:   "board:" + t.ID,
		Prompt: prompt,
		Agent:  t.Assignee,
		Source: "taskboard",
		onStart: func() {
			if _, err := d.engine.MoveTask(taskID, "doing"); err != nil {
				logWarn("taskboard dispatch: failed to move task to doing on start", "id", taskID, "error", err)
			}
		},
	}
	fillDefaults(d.cfg, &task)

	// LLM-based timeout estimation: replaces keyword heuristic with a quick
	// haiku call that reads the actual task content to judge complexity.
	if llmTimeout := estimateTimeoutLLM(ctx, d.cfg, prompt); llmTimeout != "" {
		logInfo("taskboard dispatch: LLM timeout estimate", "id", t.ID, "keyword", task.Timeout, "llm", llmTimeout)
		task.Timeout = llmTimeout
	}

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

	// Immediately persist cost/duration/session as completion evidence.
	// If the daemon is killed before the final status update below, resetOrphanedDoing
	// can use this evidence to restore the task to "done" instead of resetting to "todo".
	if result.CostUSD > 0 || result.DurationMs > 0 || result.SessionID != "" {
		evidenceSQL := fmt.Sprintf(
			`UPDATE tasks SET cost_usd = %.6f, duration_ms = %d, session_id = '%s' WHERE id = '%s'`,
			result.CostUSD, result.DurationMs,
			escapeSQLite(result.SessionID), escapeSQLite(t.ID),
		)
		if err := execDB(d.engine.dbPath, evidenceSQL); err != nil {
			logWarn("taskboard dispatch: failed to persist completion evidence", "id", t.ID, "error", err)
		}
	}

	// Determine target status.
	newStatus := "done"
	if result.Status != "success" {
		newStatus = "failed"
	}

	// Review gate: if requireReview is enabled, route to "review" instead of "done".
	if d.engine.config.RequireReview && newStatus == "done" && t.Status != "review" {
		newStatus = "review"
	}

	// Atomic status + cost update in a single SQL statement.
	nowISO := time.Now().UTC().Format(time.RFC3339)
	completedAt := ""
	if newStatus == "done" {
		completedAt = nowISO
	}
	combinedSQL := fmt.Sprintf(`
		UPDATE tasks SET status = '%s', cost_usd = %.6f, duration_ms = %d,
		session_id = '%s', updated_at = '%s', completed_at = '%s'
		WHERE id = '%s'
	`,
		escapeSQLite(newStatus),
		result.CostUSD,
		result.DurationMs,
		escapeSQLite(result.SessionID),
		escapeSQLite(nowISO),
		escapeSQLite(completedAt),
		escapeSQLite(t.ID),
	)
	if err := execDB(d.engine.dbPath, combinedSQL); err != nil {
		logError("taskboard dispatch: failed to update task status+cost", "id", t.ID, "error", err)
	} else {
		// Fire webhook only if DB update succeeded.
		updatedTask := t
		updatedTask.Status = newStatus
		updatedTask.UpdatedAt = nowISO
		updatedTask.CompletedAt = completedAt
		go d.engine.fireWebhook("task.moved", updatedTask)
	}

	if newStatus == "done" || newStatus == "review" {

		// Add result as comment.
		output := result.Output
		if len(output) > 2000 {
			output = output[:2000] + "\n... (truncated)"
		}
		comment := fmt.Sprintf("Task completed in %s (cost: $%.4f)\n\n%s", duration.Round(time.Second), result.CostUSD, output)
		if _, err := d.engine.AddComment(t.ID, t.Assignee, comment); err != nil {
			logWarn("taskboard dispatch: failed to add completion comment", "id", t.ID, "error", err)
		}

		// Check for auto-delegations in the output.
		delegations := parseAutoDelegate(result.Output)
		if len(delegations) > 0 {
			processAutoDelegations(ctx, d.cfg, delegations, result.Output,
				"", t.Assignee, "", d.state, d.sem, d.childSem, nil)
		}

		logInfo("taskboard dispatch: task completed", "id", t.ID, "cost", result.CostUSD, "duration", duration.Round(time.Second))
	} else {
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

// estimateTimeoutSem is a dedicated semaphore for timeout estimation LLM calls.
var estimateTimeoutSem = make(chan struct{}, 3)

// estimateTimeoutLLM uses a lightweight LLM call to estimate appropriate timeout
// for a taskboard task. Returns a duration string (e.g. "45m", "2h") or empty
// string on failure (caller should fall back to keyword-based estimation).
func estimateTimeoutLLM(ctx context.Context, cfg *Config, prompt string) string {
	estPrompt := fmt.Sprintf(`Estimate how long an AI coding agent will need to complete this task. Consider the complexity, number of files likely involved, and whether it requires research/analysis.

Task:
%s

Reply with ONLY a single integer: the estimated minutes needed. Examples:
- Simple bug fix or config change: 15
- Moderate feature or multi-file fix: 45
- Large feature, refactor, or codebase analysis: 120
- Major rewrite or multi-project task: 180

Minutes:`, truncateStr(prompt, 2000))

	task := Task{
		ID:             newUUID(),
		Name:           "timeout-estimate",
		Prompt:         estPrompt,
		Model:          "haiku",
		Budget:         0.02,
		Timeout:        "15s",
		PermissionMode: "plan",
		Source:         "timeout-estimate",
	}
	fillDefaults(cfg, &task)
	task.Model = "haiku"
	task.Budget = 0.02

	result := runSingleTask(ctx, cfg, task, estimateTimeoutSem, nil, "")
	if result.Status != "success" || result.Output == "" {
		return ""
	}

	// Parse the integer from output.
	cleaned := strings.TrimSpace(result.Output)
	// Extract first number found.
	var numStr string
	for _, ch := range cleaned {
		if ch >= '0' && ch <= '9' {
			numStr += string(ch)
		} else if numStr != "" {
			break
		}
	}
	minutes, err := strconv.Atoi(numStr)
	if err != nil || minutes < 5 || minutes > 480 {
		return ""
	}

	// Apply 1.5x buffer to avoid premature timeout.
	buffered := int(float64(minutes) * 1.5)
	if buffered < 15 {
		buffered = 15
	}

	if buffered >= 60 {
		hours := buffered / 60
		rem := buffered % 60
		if rem == 0 {
			return fmt.Sprintf("%dh", hours)
		}
		return fmt.Sprintf("%dh%dm", hours, rem)
	}
	return fmt.Sprintf("%dm", buffered)
}

// triageBacklog evaluates backlog tasks and promotes ready ones to todo.
// Runs at most once per BacklogTriageInterval (default 1h) to avoid excessive LLM calls.
func (d *TaskBoardDispatcher) triageBacklog() {
	triageInterval := d.parseTriageInterval()
	if time.Since(d.lastTriageAt) < triageInterval {
		return
	}

	backlog, err := d.engine.ListTasks("backlog", "", "")
	if err != nil {
		logWarn("taskboard dispatch: backlog triage query failed", "error", err)
		return
	}
	if len(backlog) == 0 {
		logDebug("taskboard dispatch: no backlog tasks to triage")
		d.lastTriageAt = time.Now()
		return
	}

	d.lastTriageAt = time.Now()

	// Build triage prompt with all backlog tasks.
	var sb strings.Builder
	sb.WriteString("你是 backlog triage agent。以下是目前所有 backlog 狀態的任務，請逐一評估是否可以推進到 todo：\n\n")
	for _, t := range backlog {
		sb.WriteString(fmt.Sprintf("- **%s** (ID: %s, assignee: %s, priority: %s)", t.Title, t.ID, t.Assignee, t.Priority))
		if len(t.DependsOn) > 0 {
			sb.WriteString(fmt.Sprintf(", depends_on: %s", strings.Join(t.DependsOn, ", ")))
		}
		sb.WriteString("\n")
		if t.Description != "" {
			sb.WriteString(fmt.Sprintf("  %s\n", t.Description))
		}
		sb.WriteString("\n")
	}
	sb.WriteString("## 評估標準\n")
	sb.WriteString("1. 目標明確，不需要主人額外決策即可開始執行\n")
	sb.WriteString("2. 有 assignee（沒有的話判斷誰適合並 assign）\n")
	sb.WriteString("3. 依賴的上游任務都已完成（或無依賴）\n")
	sb.WriteString("4. 專案存在且可存取\n\n")
	sb.WriteString("符合條件的任務請用 taskboard_move 推進到 todo。\n")
	sb.WriteString("不符合的說明原因即可，不要動它。\n")
	sb.WriteString("最後回報：推進了幾張票、哪些、以及跳過的原因。")

	agent := d.engine.config.AutoDispatch.BacklogAgent
	if agent == "" {
		agent = "ruri"
	}

	task := Task{
		Name:   "backlog-triage",
		Prompt: sb.String(),
		Agent:  agent,
		Source: "taskboard",
	}
	fillDefaults(d.cfg, &task)

	dispatchCfg := d.engine.config.AutoDispatch
	if dispatchCfg.DefaultModel != "" {
		task.Model = dispatchCfg.DefaultModel
	}
	if dispatchCfg.MaxBudget > 0 && (task.Budget == 0 || task.Budget > dispatchCfg.MaxBudget) {
		task.Budget = dispatchCfg.MaxBudget
	}

	logInfo("taskboard dispatch: starting backlog triage", "backlogCount", len(backlog), "agent", agent)

	d.wg.Add(1)
	d.activeCount.Add(1)
	go func() {
		defer d.wg.Done()
		defer func() {
			if remaining := d.activeCount.Add(-1); remaining == 0 {
				select {
				case d.doneCh <- struct{}{}:
				default:
				}
			}
		}()
		defer func() {
			if r := recover(); r != nil {
				logError("taskboard dispatch: panic in backlog triage", "recover", r)
			}
		}()

		result := runSingleTask(d.ctx, d.cfg, task, d.sem, d.childSem, agent)
		if result.Status == "success" {
			logInfo("taskboard dispatch: backlog triage completed", "cost", result.CostUSD)
		} else {
			logWarn("taskboard dispatch: backlog triage failed", "error", result.Error)
		}
	}()
}

func (d *TaskBoardDispatcher) parseTriageInterval() time.Duration {
	raw := d.engine.config.AutoDispatch.BacklogTriageInterval
	if raw == "" {
		raw = "1h"
	}
	dur, err := time.ParseDuration(raw)
	if err != nil {
		logWarn("invalid backlog triage interval, using 1h", "raw", raw, "error", err)
		return time.Hour
	}
	return dur
}

// buildDependencyContext fetches the latest completion comment from each dependency task
// and concatenates them into context for the downstream task.
func (d *TaskBoardDispatcher) buildDependencyContext(depIDs []string) string {
	maxCtx := d.cfg.PromptBudget.contextMaxOrDefault()
	var parts []string
	totalLen := 0

	for _, depID := range depIDs {
		depTask, err := d.engine.GetTask(depID)
		if err != nil {
			continue
		}

		comments, err := d.engine.GetThread(depID)
		if err != nil || len(comments) == 0 {
			continue
		}

		// Use the last comment (most likely the completion output).
		lastComment := comments[len(comments)-1].Content
		entry := fmt.Sprintf("### %s (task %s)\n%s", depTask.Title, depID, lastComment)

		if totalLen+len(entry) > maxCtx {
			remaining := maxCtx - totalLen
			if remaining > 200 {
				parts = append(parts, truncateToChars(entry, remaining))
			}
			break
		}
		parts = append(parts, entry)
		totalLen += len(entry)
	}

	return strings.Join(parts, "\n\n")
}
