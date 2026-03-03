package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

		// Evidence of completion: task has completed_at or meaningful cost.
		// sessionID is NOT evidence — fillDefaults() pre-assigns it before execution.
		// durationMs alone is NOT evidence — it's set as soon as the task starts.
		// Only completed_at (set on status→done) and cost > threshold are reliable.
		hasCompletionEvidence := (completedAt != "" && completedAt != "<nil>") ||
			costUSD > 0.001

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
		d.idleAnalysis()
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

	// --- Phase A: Autonomous execution mode ---
	// Inject execution rules so the agent plans via todo.md instead of CLI plan mode.
	prompt += "\n\n## Execution Rules\n"
	prompt += "- You are running autonomously. Do NOT use plan mode or ask for confirmation.\n"
	prompt += "- FIRST: Write your execution plan as a checklist to your todo.md file.\n"
	prompt += "- THEN: Execute each item, checking them off as you go.\n"
	prompt += "- Log major milestones by calling taskboard_comment.\n"
	prompt += "- Your todo.md persists across retries — if items exist, continue from where you left off.\n"

	// --- Phase B: Per-agent todo progress tracking ---
	// Inject agent's existing todo.md for retry awareness.
	todoPath := filepath.Join(d.cfg.AgentsDir, t.Assignee, "todo.md")
	if todoContent, err := os.ReadFile(todoPath); err == nil && len(bytes.TrimSpace(todoContent)) > 0 {
		prompt += "\n\n## Your Previous Progress (todo.md)\n"
		prompt += string(todoContent)
		prompt += "\n\nContinue from where you left off. Update your todo.md as you complete items.\n"
	}

	// Inject previous execution comments for retry context.
	if t.RetryCount > 0 {
		comments, _ := d.engine.GetThread(t.ID)
		if len(comments) > 0 {
			prompt += "\n\n## Previous Execution Log\n"
			for _, c := range comments {
				prompt += fmt.Sprintf("[%s] %s: %s\n", c.CreatedAt, c.Author, c.Content)
			}
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

	// Taskboard tasks run unattended — force non-interactive mode.
	// Plan-before-execute is achieved via prompt instructions (write todo.md),
	// not via CLI plan mode which requires interactive approval.
	task.PermissionMode = "bypassPermissions"

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

	// Determine if this task should run through a workflow pipeline.
	workflowName := t.Workflow
	if workflowName == "" {
		workflowName = d.engine.config.DefaultWorkflow
	}

	usedWorkflow := workflowName != "" && workflowName != "none"

	start := time.Now()
	var result TaskResult
	if usedWorkflow {
		result = d.runTaskWithWorkflow(ctx, t, task, workflowName)
	} else {
		result = runSingleTask(ctx, d.cfg, task, d.sem, d.childSem, t.Assignee)
	}
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
	switch {
	case result.Status == "success":
		// keep "done"
	case result.Status == "cancelled":
		newStatus = "failed"
		d.engine.AddComment(t.ID, "system", "[auto-flag] Task was cancelled (not retryable).")
	default:
		newStatus = "failed"
	}

	// Treat empty output as failure — timeout/error often produces no output.
	if newStatus == "done" && strings.TrimSpace(result.Output) == "" {
		newStatus = "failed"
		d.engine.AddComment(t.ID, "system",
			"[auto-flag] Task completed with empty output. Marked failed for investigation.")
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

	// Post-task workspace git: commit workspace changes regardless of outcome.
	d.postTaskWorkspaceGit(t)

	// Post-task problem scan: lightweight LLM analysis of output for latent issues.
	d.postTaskProblemScan(t, result.Output, newStatus)

	if newStatus == "done" || newStatus == "review" {
		// Check if completing this task should roll up the parent.
		d.checkParentRollup(t.ID)

		// Auto-promote downstream tasks whose dependencies are now satisfied.
		d.promoteUnblockedTasks(t.ID)

		// Post-task git: commit & push changes if enabled.
		// Skip when a workflow was used — workflows have their own commit step.
		if !usedWorkflow {
			d.postTaskGit(t)
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

// checkParentRollup checks if completing a child task means all siblings are done,
// and if so, moves the parent task to review (or done if review not required).
func (d *TaskBoardDispatcher) checkParentRollup(taskID string) {
	task, err := d.engine.GetTask(taskID)
	if err != nil || task.ParentID == "" {
		return
	}

	children, err := d.engine.ListChildren(task.ParentID)
	if err != nil || len(children) == 0 {
		return
	}

	// Check if ALL children are done.
	allDone := true
	for _, c := range children {
		if c.Status != "done" {
			allDone = false
			break
		}
	}
	if !allDone {
		return
	}

	// All children done — roll up parent.
	parent, err := d.engine.GetTask(task.ParentID)
	if err != nil {
		logWarn("taskboard rollup: failed to get parent", "parentId", task.ParentID, "error", err)
		return
	}

	// Don't roll up if parent is already done/review.
	if parent.Status == "done" || parent.Status == "review" {
		return
	}

	targetStatus := "done"
	if d.engine.config.RequireReview {
		targetStatus = "review"
	}

	if _, err := d.engine.MoveTask(task.ParentID, targetStatus); err != nil {
		logWarn("taskboard rollup: failed to move parent", "parentId", task.ParentID, "target", targetStatus, "error", err)
		return
	}

	comment := fmt.Sprintf("[auto-rollup] All %d child tasks completed. Parent moved to %s.", len(children), targetStatus)
	if _, err := d.engine.AddComment(task.ParentID, "system", comment); err != nil {
		logWarn("taskboard rollup: failed to add comment", "parentId", task.ParentID, "error", err)
	}

	logInfo("taskboard rollup: parent rolled up", "parentId", task.ParentID, "children", len(children), "status", targetStatus)
}

// runTaskWithWorkflow executes a task through a workflow pipeline instead of a single dispatch.
// It loads the named workflow, injects task variables, runs executeWorkflow, and returns
// a merged ProviderResult for the caller's status/cost handling.
func (d *TaskBoardDispatcher) runTaskWithWorkflow(ctx context.Context, t TaskBoard, task Task, workflowName string) TaskResult {
	w, err := loadWorkflowByName(d.cfg, workflowName)
	if err != nil {
		logWarn("runTaskWithWorkflow: load failed, falling back to single dispatch",
			"task", t.ID, "workflow", workflowName, "error", err)
		return runSingleTask(ctx, d.cfg, task, d.sem, d.childSem, t.Assignee)
	}

	// Inject task variables into the workflow.
	vars := map[string]string{
		"taskId":          t.ID,
		"taskTitle":       t.Title,
		"taskDescription": t.Description,
		"agent":           t.Assignee,
	}

	logInfo("runTaskWithWorkflow: starting workflow pipeline",
		"task", t.ID, "workflow", workflowName, "steps", len(w.Steps))

	run := executeWorkflow(ctx, d.cfg, w, vars, d.state, d.sem, d.childSem)

	// Merge workflow run results into a single TaskResult.
	result := TaskResult{
		CostUSD:    run.TotalCost,
		DurationMs: run.DurationMs,
	}

	// Extract SessionID from the last step that has one.
	for i := len(w.Steps) - 1; i >= 0; i-- {
		if sr, ok := run.StepResults[w.Steps[i].ID]; ok && sr.SessionID != "" {
			result.SessionID = sr.SessionID
			break
		}
	}

	if run.Status == "success" {
		result.Status = "success"
		// Concatenate all step outputs with headers for full pipeline context.
		var parts []string
		var lastOutput string
		for _, s := range w.Steps {
			if sr, ok := run.StepResults[s.ID]; ok && sr.Output != "" {
				parts = append(parts, fmt.Sprintf("## Step: %s\n%s", s.ID, sr.Output))
				lastOutput = sr.Output
			}
		}
		if len(parts) > 1 {
			result.Output = strings.Join(parts, "\n\n---\n\n")
		} else {
			result.Output = lastOutput
		}
	} else {
		// Distinguish timeout/cancelled/error status.
		switch run.Status {
		case "timeout":
			result.Status = "timeout"
		case "cancelled":
			result.Status = "cancelled"
		default:
			result.Status = "error"
		}
		result.Error = run.Error
		// Collect failed step's output/error with step ID for context.
		for _, s := range w.Steps {
			if sr, ok := run.StepResults[s.ID]; ok && sr.Status == "error" {
				result.Output = fmt.Sprintf("[step:%s] %s", s.ID, sr.Error)
				break
			}
		}
	}

	logInfo("runTaskWithWorkflow: completed",
		"task", t.ID, "workflow", workflowName, "status", run.Status, "cost", run.TotalCost)
	return result
}

// promoteUnblockedTasks checks backlog tasks that depend on the just-completed task.
// If all dependencies of a candidate are now done (or review), it promotes to "todo".
func (d *TaskBoardDispatcher) promoteUnblockedTasks(completedID string) {
	// Find backlog tasks whose depends_on mentions the completed task.
	sql := fmt.Sprintf(
		`SELECT id, depends_on FROM tasks WHERE status = 'backlog' AND depends_on LIKE '%%%s%%'`,
		escapeSQLite(completedID),
	)
	rows, err := queryDB(d.engine.dbPath, sql)
	if err != nil {
		logWarn("promoteUnblockedTasks: query failed", "error", err)
		return
	}

	promoted := 0
	for _, row := range rows {
		id := fmt.Sprintf("%v", row["id"])
		depsJSON := fmt.Sprintf("%v", row["depends_on"])
		var depIDs []string
		if err := json.Unmarshal([]byte(depsJSON), &depIDs); err != nil || len(depIDs) == 0 {
			continue
		}

		allSatisfied := true
		for _, depID := range depIDs {
			dep, err := d.engine.GetTask(depID)
			if err != nil || (dep.Status != "done" && dep.Status != "review") {
				allSatisfied = false
				break
			}
		}
		if !allSatisfied {
			continue
		}

		if _, err := d.engine.MoveTask(id, "todo"); err != nil {
			logWarn("promoteUnblockedTasks: move failed", "id", id, "error", err)
			continue
		}
		d.engine.AddComment(id, "system",
			"[auto-promote] All dependencies resolved. Moved to todo.")
		promoted++
		logInfo("promoteUnblockedTasks: promoted", "id", id)
	}

	if promoted > 0 {
		logInfo("promoteUnblockedTasks: total promoted", "count", promoted, "trigger", completedID)
	}
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

// postTaskWorkspaceGit commits workspace changes after a task completes (done or failed).
// The workspace (memory, rules, knowledge, skills, agent todo.md) is modified during task
// execution. This ensures those changes are tracked in git with the task ID in the commit message.
func (d *TaskBoardDispatcher) postTaskWorkspaceGit(t TaskBoard) {
	wsDir := d.cfg.WorkspaceDir
	if wsDir == "" {
		return
	}

	// Verify workspace is a git repo.
	if err := exec.Command("git", "-C", wsDir, "rev-parse", "--git-dir").Run(); err != nil {
		return
	}

	// Check for uncommitted changes.
	statusOut, err := exec.Command("git", "-C", wsDir, "status", "--porcelain").Output()
	if err != nil {
		logWarn("postTaskWorkspaceGit: git status failed", "task", t.ID, "error", err)
		return
	}
	if len(bytes.TrimSpace(statusOut)) == 0 {
		return
	}

	if out, err := exec.Command("git", "-C", wsDir, "add", "-A").CombinedOutput(); err != nil {
		logWarn("postTaskWorkspaceGit: git add failed", "task", t.ID, "error", string(out))
		return
	}

	commitMsg := fmt.Sprintf("[%s] %s", t.ID, t.Title)
	if out, err := exec.Command("git", "-C", wsDir, "commit", "-m", commitMsg).CombinedOutput(); err != nil {
		logWarn("postTaskWorkspaceGit: git commit failed", "task", t.ID, "error", string(out))
		return
	}

	logInfo("postTaskWorkspaceGit: committed workspace changes", "task", t.ID)
}

// postTaskGit commits and optionally pushes changes after a task completes.
// Only runs when gitCommit is enabled, the task has a project with a workdir
// that is a git repo, and there are uncommitted changes.
func (d *TaskBoardDispatcher) postTaskGit(t TaskBoard) {
	if !d.engine.config.GitCommit {
		return
	}
	if t.Project == "" || t.Project == "default" {
		return
	}
	if t.Assignee == "" {
		return
	}

	p, err := getProject(d.cfg.HistoryDB, t.Project)
	if err != nil || p == nil || p.Workdir == "" {
		return
	}
	workdir := p.Workdir

	// Verify workdir is a git repo.
	if err := exec.Command("git", "-C", workdir, "rev-parse", "--git-dir").Run(); err != nil {
		return
	}

	// Check for uncommitted changes.
	statusOut, err := exec.Command("git", "-C", workdir, "status", "--porcelain").Output()
	if err != nil {
		logWarn("postTaskGit: git status failed", "task", t.ID, "error", err)
		return
	}
	if len(bytes.TrimSpace(statusOut)) == 0 {
		logInfo("postTaskGit: no changes to commit", "task", t.ID, "project", t.Project)
		return
	}

	// Branch: {assignee}/{project-name}
	branch := fmt.Sprintf("%s/%s", t.Assignee, p.Name)

	if out, err := exec.Command("git", "-C", workdir, "checkout", "-B", branch).CombinedOutput(); err != nil {
		msg := fmt.Sprintf("[post-task-git] checkout -B %s failed: %s", branch, strings.TrimSpace(string(out)))
		logWarn("postTaskGit: checkout failed", "task", t.ID, "error", msg)
		d.engine.AddComment(t.ID, "system", msg)
		return
	}

	if out, err := exec.Command("git", "-C", workdir, "add", "-A").CombinedOutput(); err != nil {
		msg := fmt.Sprintf("[post-task-git] add -A failed: %s", strings.TrimSpace(string(out)))
		logWarn("postTaskGit: add failed", "task", t.ID, "error", msg)
		d.engine.AddComment(t.ID, "system", msg)
		return
	}

	commitMsg := fmt.Sprintf("[%s] %s", t.ID, t.Title)
	if out, err := exec.Command("git", "-C", workdir, "commit", "-m", commitMsg).CombinedOutput(); err != nil {
		msg := fmt.Sprintf("[post-task-git] commit failed: %s", strings.TrimSpace(string(out)))
		logWarn("postTaskGit: commit failed", "task", t.ID, "error", msg)
		d.engine.AddComment(t.ID, "system", msg)
		return
	}

	logInfo("postTaskGit: committed", "task", t.ID, "branch", branch)
	d.engine.AddComment(t.ID, "system", fmt.Sprintf("[post-task-git] Committed to branch %s", branch))

	// Push if enabled.
	if d.engine.config.GitPush {
		if out, err := exec.Command("git", "-C", workdir, "push", "-u", "origin", branch).CombinedOutput(); err != nil {
			msg := fmt.Sprintf("[post-task-git] push failed: %s", strings.TrimSpace(string(out)))
			logWarn("postTaskGit: push failed", "task", t.ID, "error", msg)
			d.engine.AddComment(t.ID, "system", msg)
			return
		}
		logInfo("postTaskGit: pushed", "task", t.ID, "branch", branch)
		d.engine.AddComment(t.ID, "system", fmt.Sprintf("[post-task-git] Pushed to origin/%s", branch))
	}
}

// idleAnalysisSem limits concurrent idle-analysis LLM calls.
var idleAnalysisSem = make(chan struct{}, 1)

// idleAnalysis generates backlog tasks when the board is idle.
// Conditions: idleAnalyze enabled, no doing/review/todo tasks, 24h cooldown per project.
func (d *TaskBoardDispatcher) idleAnalysis() {
	if !d.engine.config.IdleAnalyze {
		return
	}

	// Verify no doing or review tasks exist.
	for _, status := range []string{"doing", "review"} {
		tasks, err := d.engine.ListTasks(status, "", "")
		if err != nil || len(tasks) > 0 {
			return
		}
	}

	// Find active projects: distinct projects with tasks completed in last 7 days.
	cutoff7d := time.Now().Add(-7 * 24 * time.Hour).UTC().Format(time.RFC3339)
	projectSQL := fmt.Sprintf(`
		SELECT DISTINCT project FROM tasks
		WHERE status IN ('done','failed')
		AND completed_at > '%s'
		AND project != '' AND project != 'default'
		LIMIT 3
	`, escapeSQLite(cutoff7d))
	projectRows, err := queryDB(d.engine.dbPath, projectSQL)
	if err != nil || len(projectRows) == 0 {
		return
	}

	cutoff24h := time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339)

	for _, row := range projectRows {
		projectID := fmt.Sprintf("%v", row["project"])

		// 24h cooldown: check for recent [idle-analysis] comments in this project.
		cooldownSQL := fmt.Sprintf(`
			SELECT COUNT(*) as cnt FROM task_comments
			WHERE content LIKE '%%[idle-analysis]%%'
			AND created_at > '%s'
			AND task_id IN (SELECT id FROM tasks WHERE project = '%s')
		`, escapeSQLite(cutoff24h), escapeSQLite(projectID))
		cooldownRows, err := queryDB(d.engine.dbPath, cooldownSQL)
		if err == nil && len(cooldownRows) > 0 && getFloat64(cooldownRows[0], "cnt") > 0 {
			logDebug("idleAnalysis: 24h cooldown active", "project", projectID)
			continue
		}

		d.runIdleAnalysisForProject(projectID)
	}
}

// runIdleAnalysisForProject gathers context and asks an LLM to suggest next tasks.
func (d *TaskBoardDispatcher) runIdleAnalysisForProject(projectID string) {
	// Gather recently completed tasks.
	recentSQL := fmt.Sprintf(`
		SELECT id, title, status FROM tasks
		WHERE project = '%s' AND status IN ('done','failed')
		ORDER BY completed_at DESC LIMIT 10
	`, escapeSQLite(projectID))
	recentRows, err := queryDB(d.engine.dbPath, recentSQL)
	if err != nil || len(recentRows) == 0 {
		return
	}

	var sb strings.Builder
	sb.WriteString("Recently completed tasks:\n")
	for _, r := range recentRows {
		sb.WriteString(fmt.Sprintf("- [%s] %s (%s)\n",
			fmt.Sprintf("%v", r["id"]),
			fmt.Sprintf("%v", r["title"]),
			fmt.Sprintf("%v", r["status"])))
	}

	// Gather git log if project has a workdir.
	p, _ := getProject(d.cfg.HistoryDB, projectID)
	if p != nil && p.Workdir != "" {
		if gitOut, err := exec.Command("git", "-C", p.Workdir, "log", "--oneline", "-20").Output(); err == nil {
			sb.WriteString("\nRecent git activity:\n")
			sb.WriteString(string(gitOut))
		}
	}

	projectName := projectID
	if p != nil && p.Name != "" {
		projectName = p.Name
	}

	prompt := fmt.Sprintf(`Based on the completed tasks and recent git activity for project "%s", identify 1-3 logical next tasks.

%s

Output ONLY a JSON array of objects with keys: title, description, priority (low/normal/high).
Example: [{"title":"...","description":"...","priority":"normal"}]`, projectName, sb.String())

	task := Task{
		ID:             newUUID(),
		Name:           "idle-analysis-" + projectID,
		Prompt:         prompt,
		Model:          "haiku",
		Budget:         0.10,
		Timeout:        "30s",
		PermissionMode: "plan",
		Source:         "idle-analysis",
	}
	fillDefaults(d.cfg, &task)
	task.Model = "haiku"
	task.Budget = 0.10

	logInfo("idleAnalysis: analyzing project", "project", projectID)
	result := runSingleTask(d.ctx, d.cfg, task, idleAnalysisSem, nil, "")
	if result.Status != "success" || strings.TrimSpace(result.Output) == "" {
		logWarn("idleAnalysis: LLM call failed", "project", projectID, "error", result.Error)
		return
	}

	// Parse JSON array from output.
	type suggestion struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Priority    string `json:"priority"`
	}
	var suggestions []suggestion

	output := result.Output
	start := strings.Index(output, "[")
	end := strings.LastIndex(output, "]")
	if start < 0 || end <= start {
		logWarn("idleAnalysis: no JSON array in output", "project", projectID)
		return
	}
	if err := json.Unmarshal([]byte(output[start:end+1]), &suggestions); err != nil {
		logWarn("idleAnalysis: JSON parse failed", "project", projectID, "error", err)
		return
	}

	// Cap at 3 suggestions.
	if len(suggestions) > 3 {
		suggestions = suggestions[:3]
	}

	created := 0
	for _, s := range suggestions {
		if s.Title == "" {
			continue
		}
		priority := s.Priority
		if priority == "" {
			priority = "normal"
		}
		newTask, err := d.engine.CreateTask(TaskBoard{
			Project:     projectID,
			Title:       s.Title,
			Description: s.Description,
			Priority:    priority,
			Status:      "backlog",
		})
		if err != nil {
			logWarn("idleAnalysis: failed to create task", "project", projectID, "title", s.Title, "error", err)
			continue
		}
		d.engine.AddComment(newTask.ID, "system", "[idle-analysis] Auto-generated from project analysis")
		created++
	}

	logInfo("idleAnalysis: created backlog tasks", "project", projectID, "count", created)
}

// problemScanSem limits concurrent problem-scan LLM calls.
var problemScanSem = make(chan struct{}, 2)

// postTaskProblemScan uses a lightweight LLM call to scan the task output for latent
// issues: error patterns, unresolved TODOs, test failures, warnings, partial implementations.
// If problems are found, adds a comment to the task and optionally creates follow-up tickets.
func (d *TaskBoardDispatcher) postTaskProblemScan(t TaskBoard, output string, newStatus string) {
	if !d.engine.config.ProblemScan {
		return
	}
	if strings.TrimSpace(output) == "" {
		return
	}

	// Truncate output to keep LLM cost low.
	scanInput := output
	if len(scanInput) > 4000 {
		scanInput = scanInput[:4000] + "\n... (truncated)"
	}

	prompt := fmt.Sprintf(`You are a post-task quality scanner. Analyze this task output and identify latent problems that may need follow-up.

Task: %s (status: %s)

Output:
%s

Look for:
1. Error messages or stack traces (even if the task "succeeded")
2. Unresolved TODOs or FIXMEs mentioned in the output
3. Test failures or skipped tests
4. Warnings that could become errors later
5. Partial implementations ("will do later", "not yet implemented", etc.)
6. Security concerns (hardcoded credentials, unsafe patterns)

If you find problems, respond with a JSON object:
{"problems": [{"severity": "high|medium|low", "summary": "one-line description", "detail": "brief explanation"}], "followup": [{"title": "follow-up task title", "description": "what needs to be done", "priority": "high|normal|low"}]}

If no problems found, respond with exactly: {"problems": [], "followup": []}`, truncateStr(t.Title, 200), newStatus, scanInput)

	task := Task{
		ID:             newUUID(),
		Name:           "problem-scan-" + t.ID,
		Prompt:         prompt,
		Model:          "haiku",
		Budget:         0.05,
		Timeout:        "30s",
		PermissionMode: "plan",
		Source:         "problem-scan",
	}
	fillDefaults(d.cfg, &task)
	task.Model = "haiku"
	task.Budget = 0.05

	result := runSingleTask(d.ctx, d.cfg, task, problemScanSem, nil, "")
	if result.Status != "success" || strings.TrimSpace(result.Output) == "" {
		logDebug("postTaskProblemScan: LLM call failed or empty", "task", t.ID, "error", result.Error)
		return
	}

	// Parse JSON from output.
	type problem struct {
		Severity string `json:"severity"`
		Summary  string `json:"summary"`
		Detail   string `json:"detail"`
	}
	type followup struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Priority    string `json:"priority"`
	}
	type scanResult struct {
		Problems []problem  `json:"problems"`
		Followup []followup `json:"followup"`
	}

	raw := result.Output
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end <= start {
		logDebug("postTaskProblemScan: no JSON in output", "task", t.ID)
		return
	}

	var sr scanResult
	if err := json.Unmarshal([]byte(raw[start:end+1]), &sr); err != nil {
		logDebug("postTaskProblemScan: JSON parse failed", "task", t.ID, "error", err)
		return
	}

	if len(sr.Problems) == 0 && len(sr.Followup) == 0 {
		logDebug("postTaskProblemScan: no issues found", "task", t.ID)
		return
	}

	// Build comment with findings.
	var sb strings.Builder
	sb.WriteString("[problem-scan] Potential issues detected:\n")
	for _, p := range sr.Problems {
		sb.WriteString(fmt.Sprintf("- [%s] %s: %s\n", p.Severity, p.Summary, p.Detail))
	}

	if _, err := d.engine.AddComment(t.ID, "system", sb.String()); err != nil {
		logWarn("postTaskProblemScan: failed to add comment", "task", t.ID, "error", err)
	}

	// Create follow-up tickets (cap at 3).
	created := 0
	for _, f := range sr.Followup {
		if created >= 3 || f.Title == "" {
			break
		}
		priority := f.Priority
		if priority == "" {
			priority = "normal"
		}
		newTask, err := d.engine.CreateTask(TaskBoard{
			Project:     t.Project,
			Title:       f.Title,
			Description: f.Description,
			Priority:    priority,
			Status:      "backlog",
			DependsOn:   []string{t.ID},
		})
		if err != nil {
			logWarn("postTaskProblemScan: failed to create follow-up", "task", t.ID, "title", f.Title, "error", err)
			continue
		}
		d.engine.AddComment(newTask.ID, "system",
			fmt.Sprintf("[problem-scan] Auto-created from scan of task %s (%s)", t.ID, t.Title))
		created++
	}

	logInfo("postTaskProblemScan: scan complete", "task", t.ID, "problems", len(sr.Problems), "followups", created)
}
