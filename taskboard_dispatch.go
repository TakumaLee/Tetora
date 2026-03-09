package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	worktreeMgr   *WorktreeManager // git worktree manager for task isolation
}

func newTaskBoardDispatcher(engine *TaskBoardEngine, cfg *Config, sem, childSem chan struct{}, state *dispatchState) *TaskBoardDispatcher {
	ctx, cancel := context.WithCancel(context.Background())

	// Initialize worktree manager under runtime/worktrees/.
	wtBaseDir := filepath.Join(cfg.RuntimeDir, "worktrees")
	wtMgr := NewWorktreeManager(wtBaseDir)

	return &TaskBoardDispatcher{
		engine:      engine,
		cfg:         cfg,
		sem:         sem,
		childSem:    childSem,
		state:       state,
		stopCh:      make(chan struct{}),
		doneCh:      make(chan struct{}, 1), // buffered: at most one pending re-scan signal
		ctx:         ctx,
		cancel:      cancel,
		worktreeMgr: wtMgr,
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

	// Auto-review: pick up tasks in "review" assigned to an agent (not a human).
	// Runs before dispatch so approved tasks can unblock dependents in this same cycle.
	d.scanReviews()

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
	d.triageBacklog() // always attempt (interval-gated internally)
	if len(tasks) == 0 {
		logDebug("taskboard dispatch: scan found no todo tasks")
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

	// Inject spec/context comments (always, not just on retry).
	allComments, _ := d.engine.GetThread(t.ID)
	var specParts []string
	for _, c := range allComments {
		if c.Type == "spec" || c.Type == "context" {
			specParts = append(specParts, c.Content)
		}
	}
	if len(specParts) > 0 {
		prompt += "\n\n## Task Specifications\n\n"
		prompt += strings.Join(specParts, "\n\n")
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

	// Inject previous execution log comments for retry context.
	if t.RetryCount > 0 {
		var logComments []TaskComment
		for _, c := range allComments {
			if c.Type == "log" || c.Type == "system" {
				logComments = append(logComments, c)
			}
		}
		if len(logComments) > 0 {
			prompt += "\n\n## Previous Execution Log\n"
			for _, c := range logComments {
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
	var projectWorkdir string // original repo dir (for worktree operations)
	if t.Project != "" && t.Project != "default" {
		p, err := getProject(d.cfg.HistoryDB, t.Project)
		if err == nil && p != nil && p.Workdir != "" {
			task.Workdir = p.Workdir
			projectWorkdir = p.Workdir
		}
	}

	// --- Worktree isolation ---
	// When gitWorktree is enabled and the task has a project workdir that is a git repo,
	// create an isolated worktree so the agent doesn't touch the main working tree.
	var worktreeDir string
	if d.engine.config.GitWorktree && projectWorkdir != "" {
		if exec.Command("git", "-C", projectWorkdir, "rev-parse", "--git-dir").Run() == nil {
			branch := buildBranchName(d.engine.config.GitWorkflow, t)
			wtDir, err := d.worktreeMgr.Create(projectWorkdir, t.ID, branch)
			if err != nil {
				logWarn("worktree: creation failed, falling back to shared workdir",
					"task", t.ID, "error", err)
				d.engine.AddComment(t.ID, "system",
					fmt.Sprintf("[worktree] Failed to create isolated worktree: %v. Using shared workdir.", err))
			} else {
				worktreeDir = wtDir
				task.Workdir = wtDir
				logInfo("worktree: task running in isolation", "task", t.ID, "path", wtDir)
				d.engine.AddComment(t.ID, "system",
					fmt.Sprintf("[worktree] Running in isolated worktree: %s", wtDir))
			}
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
	qaApproved := false

	if d.engine.config.AutoDispatch.ReviewLoop {
		// Dev↔QA loop: execute → review → retry with feedback (max N retries).
		loopResult := d.devQALoop(ctx, t, task, usedWorkflow, workflowName)
		result = loopResult.Result
		qaApproved = loopResult.QAApproved
		// Use accumulated cost from all attempts.
		result.CostUSD = loopResult.TotalCost
		if loopResult.Attempts > 1 {
			logInfo("devQA loop summary", "task", t.ID, "attempts", loopResult.Attempts,
				"approved", qaApproved, "totalCost", loopResult.TotalCost)
		}
	} else {
		if usedWorkflow {
			result = d.runTaskWithWorkflow(ctx, t, task, workflowName)
		} else {
			result = runSingleTask(ctx, d.cfg, task, d.sem, d.childSem, t.Assignee)
		}
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

	// Review gate:
	// If Dev↔QA loop approved the output → go directly to "done" (QA already passed).
	// Otherwise → thorough auto-review (sonnet/opus). Three outcomes:
	//   approve  → "done" (code is production-ready)
	//   fix      → retry with feedback (agent can fix autonomously)
	//   escalate → assign to user (needs human judgment)
	if newStatus == "done" && !qaApproved && t.Status != "review" {
		reviewer := d.engine.config.AutoDispatch.ReviewAgent
		if reviewer == "" {
			reviewer = "ruri"
		}
		logInfo("taskboard dispatch: auto-review starting", "id", t.ID, "reviewer", reviewer)
		d.engine.AddComment(t.ID, "system", fmt.Sprintf("[auto-review] %s reviewing output...", reviewer))

		rv := d.thoroughReview(ctx, prompt, result.Output, t.Assignee, reviewer)
		result.CostUSD += rv.CostUSD

		switch rv.Verdict {
		case reviewApprove:
			logInfo("taskboard dispatch: auto-review approved", "id", t.ID, "comment", rv.Comment)
			d.engine.AddComment(t.ID, reviewer, fmt.Sprintf("[review] Approved: %s", rv.Comment))
			// newStatus stays "done"

		case reviewFix:
			logInfo("taskboard dispatch: auto-review requests fix, retrying", "id", t.ID, "comment", rv.Comment)
			d.engine.AddComment(t.ID, reviewer, fmt.Sprintf("[review] Fix required: %s", rv.Comment))

			maxRetries := d.engine.config.maxRetriesOrDefault()
			if t.RetryCount < maxRetries {
				// Inject review feedback and retry.
				t.RetryCount++
				d.engine.UpdateTask(t.ID, map[string]any{"retryCount": t.RetryCount})
				d.engine.AddComment(t.ID, "system",
					fmt.Sprintf("[auto-review] Sending back to %s for fix (attempt %d/%d)", t.Assignee, t.RetryCount, maxRetries))

				retryPrompt := prompt + "\n\n## Review Feedback (MUST FIX)\n\n" + rv.Comment +
					"\n\nFix ALL issues listed above. Do not skip any."
				task.Prompt = retryPrompt
				retryResult := runSingleTask(ctx, d.cfg, task, d.sem, d.childSem, t.Assignee)
				result.CostUSD += retryResult.CostUSD
				result.Output = retryResult.Output
				result.DurationMs += retryResult.DurationMs

				if retryResult.Status == "success" && strings.TrimSpace(retryResult.Output) != "" {
					// Re-review after fix.
					rv2 := d.thoroughReview(ctx, prompt, retryResult.Output, t.Assignee, reviewer)
					result.CostUSD += rv2.CostUSD
					if rv2.Verdict == reviewApprove {
						d.engine.AddComment(t.ID, reviewer, fmt.Sprintf("[review] Approved after fix: %s", rv2.Comment))
						// newStatus stays "done"
					} else if rv2.Verdict == reviewEscalate {
						d.engine.AddComment(t.ID, reviewer, fmt.Sprintf("[review] Escalating: %s", rv2.Comment))
						newStatus = "review"
						t.Assignee = d.resolveEscalateAssignee()
					} else {
						// Still needs fix but out of inline retries — escalate.
						d.engine.AddComment(t.ID, reviewer,
							fmt.Sprintf("[review] Still has issues after fix: %s. Escalating.", rv2.Comment))
						newStatus = "review"
						t.Assignee = d.resolveEscalateAssignee()
					}
				} else {
					d.engine.AddComment(t.ID, "system", "[auto-review] Retry failed. Escalating.")
					newStatus = "review"
					t.Assignee = d.resolveEscalateAssignee()
				}
			} else {
				d.engine.AddComment(t.ID, "system",
					fmt.Sprintf("[auto-review] Max retries (%d) reached. Escalating.", maxRetries))
				newStatus = "review"
				t.Assignee = d.resolveEscalateAssignee()
			}

		case reviewEscalate:
			logInfo("taskboard dispatch: auto-review escalating to user", "id", t.ID, "comment", rv.Comment)
			d.engine.AddComment(t.ID, reviewer, fmt.Sprintf("[review] Needs human judgment: %s", rv.Comment))
			newStatus = "review"
			t.Assignee = d.resolveEscalateAssignee()
		}
	}

	// Atomic status + cost update in a single SQL statement.
	nowISO := time.Now().UTC().Format(time.RFC3339)
	completedAt := ""
	if newStatus == "done" {
		completedAt = nowISO
	}
	combinedSQL := fmt.Sprintf(`
		UPDATE tasks SET status = '%s', assignee = '%s', cost_usd = %.6f, duration_ms = %d,
		session_id = '%s', updated_at = '%s', completed_at = '%s'
		WHERE id = '%s'
	`,
		escapeSQLite(newStatus),
		escapeSQLite(t.Assignee),
		result.CostUSD,
		result.DurationMs,
		escapeSQLite(result.SessionID),
		escapeSQLite(nowISO),
		escapeSQLite(completedAt),
		escapeSQLite(t.ID),
	)
	if err := execDB(d.engine.dbPath, combinedSQL); err != nil {
		logError("taskboard dispatch: failed to update task status+cost", "id", t.ID, "error", err)
		// Retry once after a short delay — transient SQLite lock errors are common.
		time.Sleep(100 * time.Millisecond)
		if err2 := execDB(d.engine.dbPath, combinedSQL); err2 != nil {
			logError("taskboard dispatch: SQL retry also failed", "id", t.ID, "error", err2)
			// Fallback: move back to todo so the task is not stuck in doing.
			if _, ferr := d.engine.MoveTask(t.ID, "todo"); ferr != nil {
				logError("taskboard dispatch: fallback MoveTask to todo failed", "id", t.ID, "error", ferr)
			} else {
				logWarn("taskboard dispatch: task moved back to todo after persistent SQL failure", "id", t.ID)
			}
		} else {
			// Retry succeeded — fire webhook.
			updatedTask := t
			updatedTask.Status = newStatus
			updatedTask.UpdatedAt = nowISO
			updatedTask.CompletedAt = completedAt
			go d.engine.fireWebhook("task.moved", updatedTask)
		}
	} else {
		// Fire webhook only if DB update succeeded.
		updatedTask := t
		updatedTask.Status = newStatus
		updatedTask.UpdatedAt = nowISO
		updatedTask.CompletedAt = completedAt
		go d.engine.fireWebhook("task.moved", updatedTask)
	}

	// Record to job_runs so cost/tokens appear in budget and telemetry queries.
	recordHistory(d.cfg.HistoryDB, task.ID, task.Name, task.Source, t.Assignee, task, result,
		start.Format(time.RFC3339), time.Now().Format(time.RFC3339), result.OutputFile)

	// Post-task workspace git: commit workspace changes regardless of outcome.
	d.postTaskWorkspaceGit(t)

	// Post-task worktree: merge agent changes into main and clean up the isolated worktree.
	// Must run before postTaskGit so the merge commit is in place first.
	d.postTaskWorktree(t, projectWorkdir, worktreeDir, newStatus)

	// Post-task problem scan: lightweight LLM analysis of output for latent issues.
	d.postTaskProblemScan(t, result.Output, newStatus)

	if newStatus == "done" || newStatus == "review" {
		// Check if completing this task should roll up the parent.
		d.checkParentRollup(t.ID)

		// Auto-promote downstream tasks whose dependencies are now satisfied.
		d.promoteUnblockedTasks(t.ID)

		// Post-task git: commit & push changes if enabled.
		// Skip when a workflow was used — workflows have their own commit step.
		// Skip when worktree isolation was used — the worktree merge already committed.
		if !usedWorkflow && worktreeDir == "" {
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

		// Record failure to skill-specific failures.md for future context injection.
		d.postTaskSkillFailures(t, task, result.Error)

		// Auto-retry if enabled.
		d.engine.AutoRetryFailed()
	}
}


// triageBacklog evaluates backlog tasks and promotes ready ones to todo.
// Runs at most once per BacklogTriageInterval (default 1h) to avoid excessive LLM calls.
func (d *TaskBoardDispatcher) triageBacklog() {
	backlog, err := d.engine.ListTasks("backlog", "", "")
	if err != nil {
		logWarn("taskboard dispatch: backlog triage query failed", "error", err)
		return
	}
	if len(backlog) == 0 {
		logDebug("taskboard dispatch: no backlog tasks to triage")
		return
	}

	// Fast-path: promote assigned backlog tasks with no blocking deps directly.
	promoted := 0
	for _, t := range backlog {
		if t.Assignee != "" && !hasBlockingDeps(d.engine, t) {
			if _, err := d.engine.MoveTask(t.ID, "todo"); err == nil {
				logInfo("taskboard dispatch: fast-path promote from backlog", "taskId", t.ID, "priority", t.Priority)
				d.engine.AddComment(t.ID, "triage", "[triage] Fast-path: already assigned, no blocking deps → todo")
				promoted++
			}
		}
	}
	if promoted > 0 {
		// Re-fetch remaining backlog for LLM triage.
		backlog, err = d.engine.ListTasks("backlog", "", "")
		if err != nil || len(backlog) == 0 {
			return
		}
	}

	// Step 3: Urgent tickets get shorter triage interval (1/4, min 5min).
	triageInterval := d.parseTriageInterval()
	hasUrgent := false
	for _, t := range backlog {
		if t.Priority == "urgent" {
			hasUrgent = true
			break
		}
	}
	if hasUrgent {
		triageInterval = triageInterval / 4
		if triageInterval < 5*time.Minute {
			triageInterval = 5 * time.Minute
		}
	}

	if time.Since(d.lastTriageAt) < triageInterval {
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

// scanReviews picks up tasks in "review" status assigned to an agent and runs auto-review.
// Only reviews tasks assigned to known agents (not to humans like "takuma").
// Each task is reviewed at most once per scan — the verdict moves it to done or escalates.
func (d *TaskBoardDispatcher) scanReviews() {
	reviews, err := d.engine.ListTasks("review", "", "")
	if err != nil || len(reviews) == 0 {
		return
	}

	escalateUser := d.resolveEscalateAssignee()
	reviewer := d.engine.config.AutoDispatch.ReviewAgent
	if reviewer == "" {
		reviewer = "ruri"
	}

	for _, t := range reviews {
		// Skip tasks already assigned to a human (escalated).
		if t.Assignee == escalateUser || t.Assignee == "" {
			continue
		}

		// Skip if assignee is NOT a known agent — it's probably a human.
		if _, isAgent := d.cfg.Agents[t.Assignee]; !isAgent && t.Assignee != reviewer {
			continue
		}

		logInfo("scanReviews: auto-reviewing", "id", t.ID, "title", t.Title, "assignee", t.Assignee)

		// Extract original prompt (title + description) and output (last log comment).
		originalPrompt := t.Title
		if t.Description != "" {
			originalPrompt += "\n\n" + t.Description
		}
		output := ""
		if comments, err := d.engine.GetThread(t.ID); err == nil {
			for i := len(comments) - 1; i >= 0; i-- {
				c := comments[i]
				if (c.Type == "log" || c.Type == "") && c.Author != "system" && c.Author != "triage" {
					output = c.Content
					break
				}
			}
		}
		if output == "" {
			logDebug("scanReviews: no output found, skipping", "id", t.ID)
			continue
		}

		d.engine.AddComment(t.ID, "system", fmt.Sprintf("[auto-review] %s reviewing...", reviewer))

		rv := d.thoroughReview(d.ctx, originalPrompt, output, t.Assignee, reviewer)

		switch rv.Verdict {
		case reviewApprove:
			logInfo("scanReviews: approved", "id", t.ID, "comment", rv.Comment)
			d.engine.AddComment(t.ID, reviewer, fmt.Sprintf("[review] Approved: %s", rv.Comment))
			nowISO := time.Now().UTC().Format(time.RFC3339)
			sql := fmt.Sprintf(
				`UPDATE tasks SET status = 'done', completed_at = '%s', updated_at = '%s', cost_usd = cost_usd + %.6f WHERE id = '%s'`,
				escapeSQLite(nowISO), escapeSQLite(nowISO), rv.CostUSD, escapeSQLite(t.ID),
			)
			execDB(d.engine.dbPath, sql)
			d.checkParentRollup(t.ID)
			d.promoteUnblockedTasks(t.ID)

		case reviewFix:
			logInfo("scanReviews: fix required, sending back", "id", t.ID, "comment", rv.Comment)
			d.engine.AddComment(t.ID, reviewer, fmt.Sprintf("[review] Fix required: %s", rv.Comment))
			// Move back to todo with the original assignee — dispatcher will re-execute with feedback.
			d.engine.AddComment(t.ID, "system",
				fmt.Sprintf("[auto-review] Sending back to %s for fix.", t.Assignee))
			d.engine.UpdateTask(t.ID, map[string]any{
				"status":     "todo",
				"retryCount": t.RetryCount + 1,
			})

		case reviewEscalate:
			logInfo("scanReviews: escalating to user", "id", t.ID, "comment", rv.Comment)
			d.engine.AddComment(t.ID, reviewer, fmt.Sprintf("[review] Needs human judgment: %s", rv.Comment))
			d.engine.UpdateTask(t.ID, map[string]any{"assignee": escalateUser})
		}
	}
}

// resolveEscalateAssignee returns the user to assign review-escalated tasks to.
func (d *TaskBoardDispatcher) resolveEscalateAssignee() string {
	if ea := d.engine.config.AutoDispatch.EscalateAssignee; ea != "" {
		return ea
	}
	return "takuma"
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


