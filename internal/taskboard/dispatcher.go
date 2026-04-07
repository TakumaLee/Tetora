package taskboard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"tetora/internal/config"
	"tetora/internal/db"
	"tetora/internal/discord"
	"tetora/internal/dispatch"
	"tetora/internal/log"
)

// DiscordEmbedSender can send a Discord embed to a channel. Implemented by DiscordBot
// in the root package.
type DiscordEmbedSender interface {
	SendEmbed(channelID string, embed discord.Embed)
}

// WorktreeManageable abstracts git worktree lifecycle operations.
type WorktreeManageable interface {
	Create(repoDir, taskID, branch string) (string, error)
	Remove(repoDir, worktreeDir string) error
	CommitCount(worktreeDir string) int
	HasChanges(worktreeDir string) bool
	Merge(repoDir, worktreeDir, commitMsg string) (string, error)
}

// WorkflowRunner abstracts workflow execution and resumption. The root workflow
// engine satisfies this interface.
type WorkflowRunner interface {
	// Execute runs the named workflow with the given variables.
	Execute(ctx context.Context, workflowName string, vars map[string]string) (WorkflowRunResult, error)
	// Resume resumes a previously started workflow run.
	Resume(ctx context.Context, runID string) (WorkflowRunResult, error)
	// QueryRun looks up a workflow run by ID.
	QueryRun(dbPath, id string) (WorkflowRunInfo, error)
}

// WorkflowRunResult holds the result of a workflow execution.
type WorkflowRunResult struct {
	ID         string
	Status     string
	TotalCost  float64
	DurationMs int64
	Error      string
	// StepOutputs maps step ID → output text.
	StepOutputs map[string]string
	// StepErrors maps step ID → error (for failed steps).
	StepErrors map[string]string
	// StepSessions maps step ID → session ID.
	StepSessions map[string]string
	// StepOrder preserves the order of steps for output concatenation.
	StepOrder []string
}

// WorkflowRunInfo holds metadata about a workflow run (for resume/stuck-guard logic).
type WorkflowRunInfo struct {
	ID           string
	WorkflowName string
	Status       string
	StartedAt    string
}

// IsResumable returns true when the run status permits resumption.
func (r WorkflowRunInfo) IsResumable() bool {
	switch r.Status {
	case "paused", "partial", "error", "resumed":
		return true
	}
	return false
}

// SkillsProvider abstracts skill selection and failure context operations.
// Implemented by wire_skill.go in the root package.
type SkillsProvider interface {
	// SelectSkills returns skills matching the given task.
	SelectSkills(task dispatch.Task) []config.SkillConfig
	// LoadFailuresByName returns recorded failure context for a skill.
	LoadFailuresByName(skillName string) string
	// AppendFailure records a failure for a skill.
	AppendFailure(skillName, taskTitle, agentName, errMsg string)
	// MaxInject returns the maximum inject length for skill failures.
	MaxInject() int
}

// ProjectLookup looks up a project by ID. Returns nil if not found.
type ProjectLookup func(historyDB, id string) *ProjectInfo

// ProjectInfo holds the relevant fields from a project record.
type ProjectInfo struct {
	Name     string
	Workdir  string
	RepoURL  string
	Category string
}

// AutoDelegation is an agent-to-agent delegation directive parsed from output.
type AutoDelegation struct {
	Agent  string
	Task   string
	Reason string
}

// DelegationProcessor parses and executes auto-delegation directives from output.
type DelegationProcessor interface {
	// Parse extracts delegation directives from agent output.
	Parse(output string) []AutoDelegation
	// Process executes the delegations found in agent output.
	Process(ctx context.Context, delegations []AutoDelegation, output, fromAgent string)
}

// AgentLoader loads an agent's system prompt.
type AgentLoader func(cfg *config.Config, agentName string) (string, error)

// BranchNamer builds a branch name for a task.
type BranchNamer func(cfg config.GitWorkflowConfig, t TaskBoard) string

// HistoryRecorder records task execution history.
type HistoryRecorder func(dbPath, jobID, name, source, role string, task dispatch.Task, result dispatch.TaskResult, startedAt, finishedAt, outputFile string)

// FillDefaultsFn populates default values for a task.
type FillDefaultsFn func(cfg *config.Config, t *dispatch.Task)

// NewIDFn generates a new unique ID.
type NewIDFn func() string

// TruncateFn truncates a string to maxLen.
type TruncateFn func(s string, maxLen int) string

// TruncateToCharsFn truncates a string to maxChars.
type TruncateToCharsFn func(s string, maxChars int) string

// ExtractJSONFn extracts a JSON object from mixed text.
type ExtractJSONFn func(s string) string

// DispatcherDeps holds all root-package dependencies injected into the Dispatcher.
// This avoids import cycles: internal/taskboard does not need to import package main.
type DispatcherDeps struct {
	// Executor runs a single task (wraps root runSingleTask).
	Executor dispatch.TaskExecutor
	// ChildExecutor is the child semaphore variant of Executor (can be nil).
	ChildExecutor dispatch.TaskExecutor

	// FillDefaults sets default task fields.
	FillDefaults FillDefaultsFn
	// RecordHistory persists execution history.
	RecordHistory HistoryRecorder
	// LoadAgentPrompt loads an agent's system prompt.
	LoadAgentPrompt AgentLoader
	// Workflows handles workflow execution and resumption.
	Workflows WorkflowRunner
	// GetProject looks up project metadata.
	GetProject ProjectLookup
	// Skills handles skill selection and failure injection.
	Skills SkillsProvider
	// Worktrees manages git worktree lifecycle.
	Worktrees WorktreeManageable
	// Delegations parses and processes auto-delegation directives.
	Delegations DelegationProcessor
	// BuildBranch generates a branch name for a task.
	BuildBranch BranchNamer
	// NewID generates a new unique ID.
	NewID NewIDFn
	// Truncate truncates a string.
	Truncate TruncateFn
	// TruncateToChars truncates a string to a char count.
	TruncateToChars TruncateToCharsFn
	// ExtractJSON extracts a JSON object from text.
	ExtractJSON ExtractJSONFn

	// Discord is an optional Discord embed sender for stale-reset notifications.
	Discord DiscordEmbedSender
	// DiscordNotifyChannelID is the Discord channel for task board notifications.
	DiscordNotifyChannelID string
}

// Dispatcher auto-dispatches tasks with status=todo and a non-empty assignee.
type Dispatcher struct {
	engine *Engine
	cfg    *config.Config
	deps   DispatcherDeps

	mu           sync.Mutex
	wg           sync.WaitGroup
	activeCount  atomic.Int32
	running      bool
	stopCh       chan struct{}
	doneCh       chan struct{}
	lastTriageAt time.Time
	ctx          context.Context
	cancel       context.CancelFunc

	// reviewInFlight tracks task IDs currently being reviewed to prevent
	// double-reviewing on overlapping scan cycles.
	reviewInFlight sync.Map // map[string]bool

	scanCount      atomic.Int64
	lastHealthAt   time.Time

	// wsGitMu serializes workspace git operations to prevent index.lock races
	// when multiple agents complete tasks concurrently.
	wsGitMu sync.Mutex
}

// NewDispatcher creates a new Dispatcher.
func NewDispatcher(engine *Engine, cfg *config.Config, deps DispatcherDeps) *Dispatcher {
	ctx, cancel := context.WithCancel(context.Background())
	return &Dispatcher{
		engine: engine,
		cfg:    cfg,
		deps:   deps,
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}, 1),
		ctx:    ctx,
		cancel: cancel,
	}
}

// Start begins the auto-dispatch loop.
func (d *Dispatcher) Start() {
	d.mu.Lock()
	if d.running {
		d.mu.Unlock()
		return
	}
	d.running = true
	d.mu.Unlock()

	d.resetOrphanedWorkflowRuns()
	d.resetOrphanedDoing()

	interval := d.parseInterval()
	log.Info("taskboard auto-dispatch started", "interval", interval.String())

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-d.stopCh:
				log.Info("taskboard auto-dispatch stopped")
				return
			case <-ticker.C:
				d.scan()
			case <-d.doneCh:
				log.Info("taskboard auto-dispatch: all tasks done, re-scanning immediately")
				d.scan()
			}
		}
	}()
}

// Stop halts the dispatcher and waits for in-flight tasks to finish.
func (d *Dispatcher) Stop() {
	d.mu.Lock()
	if !d.running {
		d.mu.Unlock()
		return
	}
	d.running = false
	close(d.stopCh)
	d.mu.Unlock()

	d.cancel()
	d.wg.Wait()
	log.Info("taskboard dispatch: all in-flight tasks finished")
}

func (d *Dispatcher) parseInterval() time.Duration {
	raw := d.engine.config.AutoDispatch.Interval
	if raw == "" {
		raw = "5m"
	}
	dur, err := time.ParseDuration(raw)
	if err != nil {
		log.Warn("invalid dispatch interval, using 5m", "raw", raw, "error", err)
		return 5 * time.Minute
	}
	return dur
}

func (d *Dispatcher) parseStuckThreshold() time.Duration {
	raw := d.engine.config.AutoDispatch.StuckThreshold
	if raw == "" {
		raw = "2h"
	}
	dur, err := time.ParseDuration(raw)
	if err != nil {
		log.Warn("invalid stuck threshold, using 2h", "raw", raw, "error", err)
		return 2 * time.Hour
	}
	return dur
}

func (d *Dispatcher) parseTriageInterval() time.Duration {
	raw := d.engine.config.AutoDispatch.BacklogTriageInterval
	if raw == "" {
		raw = "1h"
	}
	dur, err := time.ParseDuration(raw)
	if err != nil {
		log.Warn("invalid backlog triage interval, using 1h", "raw", raw, "error", err)
		return time.Hour
	}
	return dur
}

// resetOrphanedWorkflowRuns marks any "running" workflow_runs as "error" on startup.
// After a daemon restart no workflow process can still be alive, so these are zombies.
func (d *Dispatcher) resetOrphanedWorkflowRuns() {
	if d.cfg.HistoryDB == "" {
		return
	}
	// Count before update to get accurate number.
	rows, _ := db.Query(d.cfg.HistoryDB,
		`SELECT COUNT(*) as cnt FROM workflow_runs WHERE status = 'running'`)
	cnt := "0"
	if len(rows) > 0 {
		cnt = fmt.Sprintf("%v", rows[0]["cnt"])
	}
	if cnt == "0" {
		return
	}

	sql := `UPDATE workflow_runs SET status = 'error', error = 'daemon restart: orphaned running workflow' WHERE status = 'running'`
	if err := db.Exec(d.cfg.HistoryDB, sql); err != nil {
		log.Warn("resetOrphanedWorkflowRuns: failed", "error", err)
		return
	}
	log.Info("resetOrphanedWorkflowRuns: marked zombie runs as error", "count", cnt)
}

func (d *Dispatcher) resetOrphanedDoing() {
	sql := `SELECT id, title, completed_at, cost_usd, duration_ms, session_id, updated_at FROM tasks WHERE status = 'doing'`
	rows, err := db.Query(d.engine.dbPath, sql)
	if err != nil {
		log.Warn("taskboard dispatch: resetOrphanedDoing query failed", "error", err)
		return
	}
	if len(rows) == 0 {
		return
	}

	now := time.Now().UTC()
	nowISO := db.Escape(now.Format(time.RFC3339))
	gracePeriod := 2 * time.Minute

	for _, row := range rows {
		id := fmt.Sprintf("%v", row["id"])
		title := fmt.Sprintf("%v", row["title"])
		completedAt := fmt.Sprintf("%v", row["completed_at"])
		costUSD := getFloat64(row, "cost_usd")
		durationMs := getFloat64(row, "duration_ms")
		sessionID := fmt.Sprintf("%v", row["session_id"])
		updatedAt := fmt.Sprintf("%v", row["updated_at"])

		// completedAt is only set by the final atomic update, which runs AFTER review.
		// If completedAt is set → task genuinely completed (including review) → restore to "done".
		// If only cost is set → task was mid-execution or mid-review → restore to "review" for re-review.
		hasCompletedAt := completedAt != "" && completedAt != "<nil>"
		hasCostOnly := !hasCompletedAt && costUSD > 0.001

		if hasCompletedAt {
			updateSQL := fmt.Sprintf(
				`UPDATE tasks SET status = 'done', updated_at = '%s' WHERE id = '%s' AND status = 'doing'`,
				nowISO, db.Escape(id),
			)
			if err := db.Exec(d.engine.dbPath, updateSQL); err != nil {
				log.Warn("taskboard dispatch: failed to restore completed task", "id", id, "error", err)
				continue
			}
			comment := fmt.Sprintf("[auto-restore] Task had completed_at set (cost=$%.4f, duration=%dms, session=%s). Restored to 'done'.",
				costUSD, int64(durationMs), sessionID)
			d.engine.AddComment(id, "system", comment)
			log.Info("taskboard dispatch: restored completed task from doing", "id", id, "title", title)
			continue
		}

		if hasCostOnly {
			updateSQL := fmt.Sprintf(
				`UPDATE tasks SET status = 'review', updated_at = '%s' WHERE id = '%s' AND status = 'doing'`,
				nowISO, db.Escape(id),
			)
			if err := db.Exec(d.engine.dbPath, updateSQL); err != nil {
				log.Warn("taskboard dispatch: failed to restore task to review", "id", id, "error", err)
				continue
			}
			comment := fmt.Sprintf("[auto-restore] Task had cost evidence ($%.4f) but no completed_at — may not have been reviewed. Restored to 'review'.",
				costUSD)
			d.engine.AddComment(id, "system", comment)
			log.Info("taskboard dispatch: restored task to review (cost-only evidence)", "id", id, "title", title)
			continue
		}

		if t, err := time.Parse(time.RFC3339, updatedAt); err == nil {
			if now.Sub(t) < gracePeriod {
				log.Info("taskboard dispatch: skipping recently-updated doing task (grace period)",
					"id", id, "title", title, "updatedAt", updatedAt, "age", now.Sub(t).Round(time.Second))
				continue
			}
		}

		updateSQL := fmt.Sprintf(
			`UPDATE tasks SET status = 'todo', updated_at = '%s' WHERE id = '%s' AND status = 'doing'`,
			nowISO, db.Escape(id),
		)
		if err := db.Exec(d.engine.dbPath, updateSQL); err != nil {
			log.Warn("taskboard dispatch: failed to reset orphaned task", "id", id, "error", err)
			continue
		}
		comment := "[auto-reset] Orphaned in 'doing' at daemon startup (no completion evidence, past grace period). Reset to 'todo' for re-dispatch."
		if _, err := d.engine.AddComment(id, "system", comment); err != nil {
			log.Warn("taskboard dispatch: failed to add orphan reset comment", "id", id, "error", err)
		}
		log.Info("taskboard dispatch: reset orphaned doing task", "id", id, "title", title)
	}
}

// findRunningWorkflowForTask finds a currently running workflow run for a given task ID.
func (d *Dispatcher) findRunningWorkflowForTask(taskID string) string {
	sql := fmt.Sprintf(
		`SELECT id FROM workflow_runs WHERE status = 'running' AND json_extract(variables, '$._taskId') = '%s' LIMIT 1`,
		db.Escape(taskID),
	)
	rows, err := db.Query(d.cfg.HistoryDB, sql)
	if err != nil || len(rows) == 0 {
		return ""
	}
	return fmt.Sprintf("%v", rows[0]["id"])
}

// DoingTaskCountForAgent returns the number of tasks in 'doing' state for the
// given agent. Used by the HTTP dispatch handler to enforce per-agent concurrency.
func (d *Dispatcher) DoingTaskCountForAgent(agent string) int {
	tasks, err := d.engine.ListTasks("doing", agent, "")
	if err != nil {
		log.Warn("DoingTaskCountForAgent: ListTasks error", "agent", agent, "error", err)
		return 0
	}
	return len(tasks)
}

func (d *Dispatcher) ResetStuckDoing() {
	threshold := d.parseStuckThreshold()
	cutoff := time.Now().Add(-threshold).UTC().Format(time.RFC3339)

	sql := fmt.Sprintf(`SELECT id, title, workflow_run_id, execution_count FROM tasks WHERE status = 'doing' AND updated_at < '%s'`, db.Escape(cutoff))
	rows, err := db.Query(d.engine.dbPath, sql)
	if err != nil {
		log.Warn("taskboard dispatch: resetStuckDoing query failed", "error", err)
		return
	}

	for _, row := range rows {
		id := fmt.Sprintf("%v", row["id"])
		title := fmt.Sprintf("%v", row["title"])
		wfRunID := fmt.Sprintf("%v", row["workflow_run_id"])

		if wfRunID != "" && d.deps.Workflows != nil {
			wfRun, wfErr := d.deps.Workflows.QueryRun(d.cfg.HistoryDB, wfRunID)
			isRunning := wfErr == nil && (wfRun.Status == "running" || wfRun.Status == "resumed")

			// Zombie detection: if workflow has been "running" for > 2x stuckThreshold,
			// it's likely dead (process crashed without finalizing). Mark as error.
			if isRunning {
				maxRunDuration := threshold * 2
				isZombie := false
				if wfRun.StartedAt != "" {
					if startedAt, parseErr := time.Parse(time.RFC3339, wfRun.StartedAt); parseErr == nil {
						if time.Since(startedAt) > maxRunDuration {
							isZombie = true
							log.Warn("taskboard dispatch: zombie workflow detected, marking as error",
								"id", id, "title", title, "workflowRunId", wfRunID[:8],
								"startedAt", wfRun.StartedAt, "maxRunDuration", maxRunDuration)
							nowISO := time.Now().UTC().Format(time.RFC3339)
							db.Exec(d.cfg.HistoryDB, fmt.Sprintf(
								`UPDATE workflow_runs SET status = 'error', error = 'zombie: no progress for %s, auto-terminated', finished_at = '%s' WHERE id = '%s' AND status IN ('running','resumed')`,
								db.Escape(maxRunDuration.String()), db.Escape(nowISO), db.Escape(wfRunID),
							))
						}
					}
				}

				if !isZombie {
					if wfRun.Status == "resumed" {
						newRunID := d.findRunningWorkflowForTask(id)
						if newRunID != "" {
							db.Exec(d.engine.dbPath, fmt.Sprintf(
								`UPDATE tasks SET workflow_run_id = '%s', updated_at = '%s' WHERE id = '%s'`,
								db.Escape(newRunID),
								db.Escape(time.Now().UTC().Format(time.RFC3339)),
								db.Escape(id),
							))
							log.Info("taskboard dispatch: task workflow_run_id updated to active run",
								"id", id, "title", title, "oldRunId", wfRunID[:8], "newRunId", newRunID[:8])
							continue
						}
					}
					touchSQL := fmt.Sprintf(
						`UPDATE tasks SET updated_at = '%s' WHERE id = '%s'`,
						db.Escape(time.Now().UTC().Format(time.RFC3339)),
						db.Escape(id),
					)
					db.Exec(d.engine.dbPath, touchSQL)
					log.Info("taskboard dispatch: task has running workflow, refreshing timestamp",
						"id", id, "title", title, "workflowRunId", wfRunID[:8])
					continue
				}
				// Zombie: fall through to task reset.
			} else {
				// Check for an active run even if pointed run is terminal.
				activeRunID := d.findRunningWorkflowForTask(id)
				if activeRunID != "" {
					db.Exec(d.engine.dbPath, fmt.Sprintf(
						`UPDATE tasks SET workflow_run_id = '%s', updated_at = '%s' WHERE id = '%s'`,
						db.Escape(activeRunID),
						db.Escape(time.Now().UTC().Format(time.RFC3339)),
						db.Escape(id),
					))
					log.Info("taskboard dispatch: found active workflow for task, updating link",
						"id", id, "title", title, "activeRunId", activeRunID[:8])
					continue
				}
			}
		}

		// Check execution_count: if already at limit, move to failed instead of todo.
		execCount := int(getFloat64(row, "execution_count"))
		maxExec := d.engine.config.MaxExecutionsOrDefault()

		if execCount >= maxExec {
			failSQL := fmt.Sprintf(
				`UPDATE tasks SET status = 'failed', updated_at = '%s' WHERE id = '%s' AND status = 'doing'`,
				db.Escape(time.Now().UTC().Format(time.RFC3339)),
				db.Escape(id),
			)
			if err := db.Exec(d.engine.dbPath, failSQL); err != nil {
				log.Warn("taskboard dispatch: failed to move over-limit stuck task to failed", "id", id, "error", err)
				continue
			}
			comment := fmt.Sprintf("[guard] Stuck in 'doing' for >%s and max executions (%d) reached. Moved to failed.", threshold, maxExec)
			d.engine.AddComment(id, "system", comment)
			log.Warn("taskboard dispatch: stuck task exceeded max executions, moved to failed",
				"id", id, "title", title, "executionCount", execCount, "max", maxExec)
			continue
		}

		updateSQL := fmt.Sprintf(
			`UPDATE tasks SET status = 'todo', updated_at = '%s' WHERE id = '%s' AND status = 'doing'`,
			db.Escape(time.Now().UTC().Format(time.RFC3339)),
			db.Escape(id),
		)
		if err := db.Exec(d.engine.dbPath, updateSQL); err != nil {
			log.Warn("taskboard dispatch: failed to reset stuck task", "id", id, "error", err)
			continue
		}

		comment := fmt.Sprintf("[auto-reset] Stuck in 'doing' for >%s (likely daemon restart). Reset to 'todo' for re-dispatch. (execution %d/%d)", threshold, execCount, maxExec)
		if _, err := d.engine.AddComment(id, "system", comment); err != nil {
			log.Warn("taskboard dispatch: failed to add reset comment", "id", id, "error", err)
		}

		log.Info("taskboard dispatch: reset stuck doing task", "id", id, "title", title, "threshold", threshold, "executionCount", execCount)
		d.notifyStaleReset(id, title, threshold)
	}
}

func (d *Dispatcher) notifyStaleReset(taskID, title string, threshold time.Duration) {
	if d.deps.Discord == nil {
		return
	}
	ch := d.deps.DiscordNotifyChannelID
	if ch == "" {
		return
	}

	shortID := taskID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}

	embed := discord.Embed{
		Title: "⚠️ Stale Task Auto-Reset",
		Description: fmt.Sprintf(
			"Task **%s** (`%s`) was stuck in `doing` for >%s.\nReset to `todo` for re-dispatch.",
			title, shortID, threshold,
		),
		Color:     0xFEE75C,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Footer:    &discord.EmbedFooter{Text: "tetora taskboard"},
	}
	d.deps.Discord.SendEmbed(ch, embed)
}

func (d *Dispatcher) scan() {
	d.ResetStuckDoing()
	d.scanReviews()
	d.healthCheck()

	maxTasks := d.engine.config.AutoDispatch.MaxConcurrentTasks
	if maxTasks <= 0 {
		maxTasks = 3
	}

	if n := int(d.activeCount.Load()); n >= maxTasks {
		log.Info("taskboard dispatch: at capacity, skipping scan", "active", n, "max", maxTasks)
		return
	}

	tasks, err := d.engine.ListTasks("todo", "", "")
	if err != nil {
		log.Warn("taskboard dispatch scan error", "error", err)
		return
	}
	d.triageBacklog()
	if len(tasks) == 0 {
		log.Debug("taskboard dispatch: scan found no todo tasks")
		d.idleAnalysis()
		return
	}

	active := int(d.activeCount.Load())
	available := maxTasks - active
	dispatched := 0

	for _, t := range tasks {
		if t.Assignee == "" {
			defaultAgent := d.engine.config.AutoDispatch.DefaultAgent
			if defaultAgent == "" {
				defaultAgent = "ruri"
			}
			d.engine.UpdateTask(t.ID, map[string]any{"assignee": defaultAgent})
			t.Assignee = defaultAgent
			log.Info("taskboard dispatch: assigned defaultAgent to unassigned task",
				"id", t.ID, "title", t.Title, "agent", defaultAgent)
		}

		if _, isAgent := d.cfg.Agents[t.Assignee]; !isAgent {
			log.Debug("taskboard dispatch: skipping non-agent assignee",
				"id", t.ID, "title", t.Title, "assignee", t.Assignee)
			continue
		}

		if HasBlockingDeps(d.engine, t) {
			log.Debug("taskboard dispatch: skipping task with blocking deps",
				"id", t.ID, "title", t.Title, "dependsOn", t.DependsOn)
			continue
		}

		maxPerAgent := d.engine.config.AutoDispatch.MaxTasksPerAgentOrDefault()
		if n := d.DoingTaskCountForAgent(t.Assignee); n >= maxPerAgent {
			log.Debug("taskboard dispatch: agent at per-agent limit, skipping",
				"id", t.ID, "assignee", t.Assignee, "doing", n, "max", maxPerAgent)
			continue
		}

		if dispatched >= available {
			log.Info("taskboard dispatch: maxConcurrentTasks reached, deferring remaining tasks",
				"active", active, "dispatched", dispatched, "max", maxTasks)
			break
		}

		log.Info("taskboard dispatch: picking up task", "id", t.ID, "title", t.Title, "assignee", t.Assignee)

		// CAS claim: atomically move todo→doing before spawning goroutine.
		// This prevents the next scan cycle from picking up the same task.
		claimSQL := fmt.Sprintf(
			`UPDATE tasks SET status = 'doing', updated_at = '%s' WHERE id = '%s' AND status = 'todo'`,
			db.Escape(time.Now().UTC().Format(time.RFC3339)),
			db.Escape(t.ID),
		)
		if err := db.Exec(d.engine.dbPath, claimSQL); err != nil {
			log.Warn("taskboard dispatch: failed to claim task", "id", t.ID, "error", err)
			continue
		}
		// Verify the CAS succeeded by re-reading status.
		// Note: db.Exec shells out to sqlite3 CLI so RowsAffected is unavailable;
		// re-SELECT is the best available verification in this architecture.
		verifyRows, _ := db.Query(d.engine.dbPath, fmt.Sprintf(
			`SELECT status FROM tasks WHERE id = '%s'`, db.Escape(t.ID)))
		if len(verifyRows) == 0 || fmt.Sprintf("%v", verifyRows[0]["status"]) != "doing" {
			log.Info("taskboard dispatch: task already claimed by another scan", "id", t.ID)
			continue
		}
		t.Status = "doing"

		dispatched++

		d.wg.Add(1)
		d.activeCount.Add(1)
		go func(task TaskBoard) {
			defer d.wg.Done()
			defer func() {
				d.activeCount.Add(-1)
				select {
				case d.doneCh <- struct{}{}:
				default:
				}
			}()
			defer func() {
				if r := recover(); r != nil {
					log.Error("taskboard dispatch: panic in dispatchTask", "id", task.ID, "recover", r)
					if _, err := d.engine.MoveTask(task.ID, "failed"); err != nil {
						log.Warn("taskboard dispatch: failed to move panicked task to failed", "id", task.ID, "error", err)
					}
				}
			}()
			d.dispatchTask(task)
		}(t)
	}
}

func (d *Dispatcher) resolveEscalateAssignee() string {
	if ea := d.engine.config.AutoDispatch.EscalateAssignee; ea != "" {
		return ea
	}
	return "takuma"
}

func (d *Dispatcher) buildDependencyContext(depIDs []string) string {
	maxCtx := d.cfg.PromptBudget.ContextMaxOrDefault()
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

		lastComment := comments[len(comments)-1].Content
		entry := fmt.Sprintf("### %s (task %s)\n%s", depTask.Title, depID, lastComment)

		if totalLen+len(entry) > maxCtx {
			remaining := maxCtx - totalLen
			if remaining > 200 && d.deps.TruncateToChars != nil {
				parts = append(parts, d.deps.TruncateToChars(entry, remaining))
			}
			break
		}
		parts = append(parts, entry)
		totalLen += len(entry)
	}

	return strings.Join(parts, "\n\n")
}

func (d *Dispatcher) checkParentRollup(taskID string) {
	task, err := d.engine.GetTask(taskID)
	if err != nil || task.ParentID == "" {
		return
	}

	children, err := d.engine.ListChildren(task.ParentID)
	if err != nil || len(children) == 0 {
		return
	}

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

	parent, err := d.engine.GetTask(task.ParentID)
	if err != nil {
		log.Warn("taskboard rollup: failed to get parent", "parentId", task.ParentID, "error", err)
		return
	}

	if parent.Status == "done" || parent.Status == "review" {
		return
	}

	targetStatus := "done"
	if d.engine.config.RequireReview {
		targetStatus = "review"
	}

	if _, err := d.engine.MoveTask(task.ParentID, targetStatus); err != nil {
		log.Warn("taskboard rollup: failed to move parent", "parentId", task.ParentID, "target", targetStatus, "error", err)
		return
	}

	comment := fmt.Sprintf("[auto-rollup] All %d child tasks completed. Parent moved to %s.", len(children), targetStatus)
	if _, err := d.engine.AddComment(task.ParentID, "system", comment); err != nil {
		log.Warn("taskboard rollup: failed to add comment", "parentId", task.ParentID, "error", err)
	}

	log.Info("taskboard rollup: parent rolled up", "parentId", task.ParentID, "children", len(children), "status", targetStatus)
}

func (d *Dispatcher) promoteUnblockedTasks(completedID string) {
	sql := fmt.Sprintf(
		`SELECT id, depends_on FROM tasks WHERE status = 'backlog' AND depends_on LIKE '%%%s%%'`,
		db.Escape(completedID),
	)
	rows, err := db.Query(d.engine.dbPath, sql)
	if err != nil {
		log.Warn("promoteUnblockedTasks: query failed", "error", err)
		return
	}

	promoted := 0
	for _, row := range rows {
		id := fmt.Sprintf("%v", row["id"])
		depsJSON := fmt.Sprintf("%v", row["depends_on"])
		depIDs := jsonUnmarshalStringSlice(depsJSON)
		if len(depIDs) == 0 {
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
			log.Warn("promoteUnblockedTasks: move failed", "id", id, "error", err)
			continue
		}
		d.engine.AddComment(id, "system", "[auto-promote] All dependencies resolved. Moved to todo.")
		promoted++
		log.Info("promoteUnblockedTasks: promoted", "id", id)
	}

	if promoted > 0 {
		log.Info("promoteUnblockedTasks: total promoted", "count", promoted, "trigger", completedID)
	}
}

func (d *Dispatcher) triageBacklog() {
	backlog, err := d.engine.ListTasks("backlog", "", "")
	if err != nil {
		log.Warn("taskboard dispatch: backlog triage query failed", "error", err)
		return
	}
	if len(backlog) == 0 {
		log.Debug("taskboard dispatch: no backlog tasks to triage")
		return
	}

	promoted := 0
	for _, t := range backlog {
		if t.Assignee != "" && !HasBlockingDeps(d.engine, t) {
			if _, err := d.engine.MoveTask(t.ID, "todo"); err == nil {
				log.Info("taskboard dispatch: fast-path promote from backlog", "taskId", t.ID, "priority", t.Priority)
				d.engine.AddComment(t.ID, "triage", "[triage] Fast-path: already assigned, no blocking deps → todo")
				promoted++
			}
		}
	}
	if promoted > 0 {
		backlog, err = d.engine.ListTasks("backlog", "", "")
		if err != nil || len(backlog) == 0 {
			return
		}
	}

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

	task := dispatch.Task{
		Name:   "backlog-triage",
		Prompt: sb.String(),
		Agent:  agent,
		Source: "taskboard",
	}
	if d.deps.FillDefaults != nil {
		d.deps.FillDefaults(d.cfg, &task)
	}

	dispatchCfg := d.engine.config.AutoDispatch
	if dispatchCfg.DefaultModel != "" {
		task.Model = dispatchCfg.DefaultModel
	}
	if dispatchCfg.MaxBudget > 0 && (task.Budget == 0 || task.Budget > dispatchCfg.MaxBudget) {
		task.Budget = dispatchCfg.MaxBudget
	}

	log.Info("taskboard dispatch: starting backlog triage", "backlogCount", len(backlog), "agent", agent)

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
				log.Error("taskboard dispatch: panic in backlog triage", "recover", r)
			}
		}()

		result := d.deps.Executor.RunTask(d.ctx, task, agent)
		if result.Status == "success" {
			log.Info("taskboard dispatch: backlog triage completed", "cost", result.CostUSD)
		} else {
			log.Warn("taskboard dispatch: backlog triage failed", "error", result.Error)
		}
	}()
}

// healthCheck runs periodically (every 30 min) and logs warnings for pipeline anomalies.
func (d *Dispatcher) healthCheck() {
	const healthInterval = 30 * time.Minute
	if time.Since(d.lastHealthAt) < healthInterval {
		return
	}
	d.lastHealthAt = time.Now()

	var warnings []string

	// 1. Stuck reviews: tasks in "review" for > 2 hours.
	if reviews, err := d.engine.ListTasks("review", "", ""); err == nil {
		stuckCount := 0
		for _, t := range reviews {
			if t.UpdatedAt != "" {
				if updated, err := time.Parse(time.RFC3339, t.UpdatedAt); err == nil {
					if time.Since(updated) > 2*time.Hour {
						stuckCount++
					}
				}
			}
		}
		if stuckCount > 0 {
			warnings = append(warnings, fmt.Sprintf("%d review tasks stuck >2h", stuckCount))
		}
	}

	// 2. Stuck doing: tasks in "doing" for > 4 hours.
	if doing, err := d.engine.ListTasks("doing", "", ""); err == nil {
		stuckCount := 0
		for _, t := range doing {
			if t.UpdatedAt != "" {
				if updated, err := time.Parse(time.RFC3339, t.UpdatedAt); err == nil {
					if time.Since(updated) > 4*time.Hour {
						stuckCount++
					}
				}
			}
		}
		if stuckCount > 0 {
			warnings = append(warnings, fmt.Sprintf("%d doing tasks stuck >4h", stuckCount))
		}
	}

	// 3. Backlog size.
	if todo, err := d.engine.ListTasks("todo", "", ""); err == nil && len(todo) > 20 {
		warnings = append(warnings, fmt.Sprintf("large backlog: %d todo tasks", len(todo)))
	}

	// 4. In-flight review count.
	inFlightCount := 0
	d.reviewInFlight.Range(func(_, _ any) bool {
		inFlightCount++
		return true
	})
	if inFlightCount > 0 {
		log.Info("pipeline health: reviews in-flight", "count", inFlightCount)
	}

	if len(warnings) > 0 {
		log.Warn("pipeline health check", "warnings", strings.Join(warnings, "; "))
		// Notify via Discord if configured.
		if d.deps.Discord != nil && d.deps.DiscordNotifyChannelID != "" {
			d.deps.Discord.SendEmbed(d.deps.DiscordNotifyChannelID, discord.Embed{
				Title:       "Pipeline Health Alert",
				Description: strings.Join(warnings, "\n- "),
				Color:       0xFFA500,
			})
		}
	} else {
		log.Info("pipeline health check: all clear")
	}
}

func (d *Dispatcher) scanReviews() {
	reviews, err := d.engine.ListTasks("review", "", "")
	if err != nil || len(reviews) == 0 {
		return
	}

	escalateUser := d.resolveEscalateAssignee()
	reviewer := d.engine.config.AutoDispatch.ReviewAgent
	if reviewer == "" {
		reviewer = "ruri"
	}

	log.Info("scanReviews: found review tasks", "count", len(reviews))

	// Limit concurrent reviews to avoid overwhelming the system.
	const maxConcurrentReviews = 3
	sem := make(chan struct{}, maxConcurrentReviews)

	for _, t := range reviews {
		// Handle escalated-to-user tasks: auto-approve if stuck too long.
		if t.Assignee == escalateUser || t.Assignee == "" {
			if t.Assignee == "" {
				continue
			}
			// Check how long this task has been in review.
			if t.UpdatedAt != "" {
				if updated, err := time.Parse(time.RFC3339, t.UpdatedAt); err == nil {
					const escalateStaleThreshold = 4 * time.Hour
					if time.Since(updated) > escalateStaleThreshold {
						log.Info("scanReviews: escalated review stale, auto-approving",
							"id", t.ID, "assignee", t.Assignee,
							"age", time.Since(updated).Round(time.Minute))
						d.engine.AddComment(t.ID, "system",
							fmt.Sprintf("[auto-review] Escalated review unhandled for >%s. Auto-approved to unblock pipeline.", escalateStaleThreshold))
						d.approveReviewTask(t, reviewer, 0)
					}
				}
			}
			continue
		}
		if _, isAgent := d.cfg.Agents[t.Assignee]; !isAgent && t.Assignee != reviewer {
			continue
		}

		// Extract thread data synchronously (cheap DB reads).
		originalPrompt := t.Title
		if t.Description != "" {
			originalPrompt += "\n\n" + t.Description
		}
		output := ""
		diff := ""
		hasNeedsThought := false
		var needsThoughtTime time.Time
		if comments, err := d.engine.GetThread(t.ID); err == nil {
			for i := len(comments) - 1; i >= 0; i-- {
				c := comments[i]
				if output == "" && c.Author == "system" && strings.Contains(c.Content, "[needs-thought]") {
					hasNeedsThought = true
					if ts, err := time.Parse(time.RFC3339, c.CreatedAt); err == nil {
						needsThoughtTime = ts
					}
				}
				if output == "" && (c.Type == "log" || c.Type == "") && c.Author != "system" && c.Author != "triage" {
					output = c.Content
				}
				if diff == "" && c.Type == "diff" {
					diff = c.Content
				}
			}
		}

		// Stale needs-thought auto-approval: if stuck for >24h, auto-approve (synchronous, fast).
		if hasNeedsThought {
			staleThreshold := 24 * time.Hour
			if !needsThoughtTime.IsZero() && time.Since(needsThoughtTime) > staleThreshold {
				log.Info("scanReviews: stale needs-thought, auto-approving", "id", t.ID,
					"age", time.Since(needsThoughtTime).Round(time.Hour))
				d.engine.AddComment(t.ID, "system",
					fmt.Sprintf("[auto-review] Stale needs-thought (>%s). Auto-approved.", staleThreshold))
				d.approveReviewTask(t, reviewer, 0)
				continue
			}
			log.Debug("scanReviews: skipping needs-thought task", "id", t.ID)
			continue
		}
		if output == "" {
			log.Debug("scanReviews: no output found, skipping", "id", t.ID)
			continue
		}

		// Skip if already being reviewed by a previous scan cycle.
		if _, loaded := d.reviewInFlight.LoadOrStore(t.ID, true); loaded {
			log.Debug("scanReviews: already in-flight", "id", t.ID)
			continue
		}

		// Launch review asynchronously.
		task := t // capture loop var
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			defer d.reviewInFlight.Delete(task.ID)

			sem <- struct{}{}        // acquire slot
			defer func() { <-sem }() // release slot

			log.Info("scanReviews: auto-reviewing", "id", task.ID, "title", task.Title, "assignee", task.Assignee)
			d.engine.AddComment(task.ID, "system", fmt.Sprintf("[auto-review] %s reviewing...", reviewer))

			var reviewCtx *string
			if diff != "" {
				s := "\n\n## Git Diff (actual code changes)\n```diff\n" + diff + "\n```"
				reviewCtx = &s
			}

			rv := d.thoroughReview(d.ctx, originalPrompt, output, task.Assignee, reviewer, reviewCtx)

			switch rv.Verdict {
			case reviewApprove:
				log.Info("scanReviews: approved", "id", task.ID, "comment", rv.Comment)
				d.engine.AddComment(task.ID, reviewer, fmt.Sprintf("[review] Approved: %s", rv.Comment))
				d.spawnReviewSubtasks(task, rv.ActionableItems, reviewer)
				d.approveReviewTask(task, reviewer, rv.CostUSD)

			case reviewFix:
				log.Info("scanReviews: fix required, sending back", "id", task.ID, "comment", rv.Comment)
				d.engine.AddComment(task.ID, reviewer, fmt.Sprintf("[review] Fix required: %s", rv.Comment))
				d.engine.AddComment(task.ID, "system",
					fmt.Sprintf("[auto-review] Sending back to %s for fix.", task.Assignee))
				d.engine.UpdateTask(task.ID, map[string]any{
					"status":     "todo",
					"retryCount": task.RetryCount + 1,
				})

			case reviewEscalate:
				log.Info("scanReviews: escalating to user", "id", task.ID, "comment", rv.Comment)
				d.engine.AddComment(task.ID, reviewer, fmt.Sprintf("[review] Needs human judgment: %s", rv.Comment))
				d.engine.AddComment(task.ID, "system", "[needs-thought] Escalated by reviewer — needs human judgment")
				d.engine.UpdateTask(task.ID, map[string]any{"assignee": escalateUser})
			}
		}()
	}
}

// approveReviewTask marks a review task as done and merges its worktree if preserved.
func (d *Dispatcher) approveReviewTask(t TaskBoard, reviewer string, costUSD float64) {
	nowISO := time.Now().UTC().Format(time.RFC3339)
	sql := fmt.Sprintf(
		`UPDATE tasks SET status = 'done', completed_at = '%s', updated_at = '%s', cost_usd = cost_usd + %.6f WHERE id = '%s'`,
		db.Escape(nowISO), db.Escape(nowISO), costUSD, db.Escape(t.ID),
	)
	db.Exec(d.engine.dbPath, sql)

	// Merge preserved worktree if it exists.
	d.mergePreservedWorktree(t)

	d.checkParentRollup(t.ID)
	d.promoteUnblockedTasks(t.ID)
}

// mergePreservedWorktree merges a worktree that was preserved during review.
func (d *Dispatcher) mergePreservedWorktree(t TaskBoard) {
	if d.deps.Worktrees == nil {
		return
	}

	// Derive worktree path from convention: runtime/worktrees/{taskID}
	wtDir := filepath.Join(d.cfg.RuntimeDir, "worktrees", t.ID)
	if _, err := os.Stat(wtDir); os.IsNotExist(err) {
		return // No preserved worktree
	}

	// Resolve project workdir.
	projectWorkdir := d.cfg.WorkspaceDir
	if t.Project != "" && t.Project != "default" && d.deps.GetProject != nil {
		if p := d.deps.GetProject(d.cfg.HistoryDB, t.Project); p != nil && p.Workdir != "" {
			projectWorkdir = p.Workdir
		}
	}
	if projectWorkdir == "" {
		log.Warn("scanReviews: cannot merge worktree, no project workdir", "task", t.ID)
		return
	}

	commitMsg := fmt.Sprintf("[%s] %s", t.ID, t.Title)
	diffSummary, err := d.deps.Worktrees.Merge(projectWorkdir, wtDir, commitMsg)
	if err != nil {
		log.Warn("scanReviews: worktree merge failed", "task", t.ID, "error", err)
		d.engine.AddComment(t.ID, "system",
			fmt.Sprintf("[worktree] ⚠️ Post-review merge failed: %v\nBranch preserved: task/%s", err, t.ID))
		return
	}

	comment := "[worktree] Post-review: changes merged into main."
	if diffSummary != "" {
		comment += "\n```\n" + diffSummary + "\n```"
	}
	d.engine.AddComment(t.ID, "system", comment)
	log.Info("scanReviews: worktree merged after approval", "task", t.ID)

	// Cleanup worktree.
	if err := d.deps.Worktrees.Remove(projectWorkdir, wtDir); err != nil {
		log.Warn("scanReviews: worktree cleanup failed", "task", t.ID, "error", err)
	}
}
