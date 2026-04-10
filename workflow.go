package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	neturl "net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	dtypes "tetora/internal/dispatch"
	"tetora/internal/db"
	discord "tetora/internal/discord"
	"tetora/internal/log"
	"tetora/internal/version"
	iwf "tetora/internal/workflow"
)

// --- Workflow Run Types ---

// WorkflowRun tracks a single execution of a workflow.
type WorkflowRun struct {
	ID           string                       `json:"id"`
	WorkflowName string                       `json:"workflowName"`
	Status       string                       `json:"status"` // "running", "success", "error", "cancelled", "timeout", "resumed"
	StartedAt    string                       `json:"startedAt"`
	FinishedAt   string                       `json:"finishedAt,omitempty"`
	DurationMs   int64                        `json:"durationMs,omitempty"`
	TotalCost    float64                      `json:"totalCostUsd,omitempty"`
	Variables    map[string]string            `json:"variables,omitempty"`
	StepResults  map[string]*StepRunResult    `json:"stepResults"`
	Error        string                       `json:"error,omitempty"`
	ResumedFrom  string                       `json:"resumedFrom,omitempty"`
}

// StepRunResult tracks the execution of one step.
type StepRunResult struct {
	StepID     string  `json:"stepId"`
	Status     string  `json:"status"` // "pending", "running", "success", "error", "skipped", "timeout", "cancelled"
	Output     string  `json:"output,omitempty"`
	Error      string  `json:"error,omitempty"`
	StartedAt  string  `json:"startedAt,omitempty"`
	FinishedAt string  `json:"finishedAt,omitempty"`
	DurationMs int64   `json:"durationMs,omitempty"`
	CostUSD    float64 `json:"costUsd,omitempty"`
	TaskID     string  `json:"taskId,omitempty"`
	SessionID  string  `json:"sessionId,omitempty"`
	Retries    int     `json:"retries,omitempty"`
}

// --- Workflow Run Mode ---

// WorkflowRunMode controls how a workflow is executed.
type WorkflowRunMode string

const (
	// WorkflowModeLive is the default mode: full execution with provider calls and history recording.
	WorkflowModeLive WorkflowRunMode = "live"
	// WorkflowModeDryRun skips provider calls and estimates costs instead.
	WorkflowModeDryRun WorkflowRunMode = "dry-run"
	// WorkflowModeShadow executes normally but marks runs so they skip task-level history recording.
	WorkflowModeShadow WorkflowRunMode = "shadow"
)

// --- Workflow Executor ---

// workflowExecutor holds the state for one workflow execution.
type workflowExecutor struct {
	cfg      *Config
	workflow *Workflow
	run      *WorkflowRun
	wCtx     *WorkflowContext
	state    *dispatchState
	sem      chan struct{}
	childSem chan struct{}
	broker   *sseBroker
	mode     WorkflowRunMode
	mu       sync.Mutex

	// Git worktree isolation (populated when workflow.GitWorktree is true).
	worktreeDir         string           // active worktree path (empty = no isolation)
	repoDir             string           // original repo dir (for merge/cleanup)
	worktreeMgr         *WorktreeManager // worktree manager reference
	releaseWorktreeLock func()           // releases .tetora-active session lock; nil until lock acquired

	// Resume state: non-nil when resuming a previous run. Carries completed step results.
	resumeState map[string]*StepRunResult
}

// executeWorkflow runs a full workflow and returns the completed run.
// An optional mode parameter controls execution behavior (default: WorkflowModeLive).
func executeWorkflow(ctx context.Context, cfg *Config, w *Workflow, vars map[string]string,
	state *dispatchState, sem, childSem chan struct{}, mode ...WorkflowRunMode) *WorkflowRun {

	runMode := WorkflowModeLive
	if len(mode) > 0 && mode[0] != "" {
		runMode = mode[0]
	}

	runID := newUUID()
	now := time.Now()

	run := &WorkflowRun{
		ID:           runID,
		WorkflowName: w.Name,
		Status:       "running",
		StartedAt:    now.Format(time.RFC3339),
		Variables:    vars,
		StepResults:  make(map[string]*StepRunResult),
	}

	// Initialize all step results as pending.
	for _, s := range w.Steps {
		run.StepResults[s.ID] = &StepRunResult{
			StepID: s.ID,
			Status: "pending",
		}
	}

	wCtx := newWorkflowContext(w, vars)

	var broker *sseBroker
	if state != nil {
		broker = state.broker
	}

	exec := &workflowExecutor{
		cfg:      cfg,
		workflow: w,
		run:      run,
		wCtx:     wCtx,
		state:    state,
		sem:      sem,
		childSem: childSem,
		broker:   broker,
		mode:     runMode,
	}

	execCtx, execCancel := exec.setupExecContext(ctx)
	defer execCancel()

	// Publish workflow started event.
	exec.publishEvent("workflow_started", map[string]any{
		"runId":    runID,
		"workflow": w.Name,
		"steps":    len(w.Steps),
		"stepDefs": buildStepSummaries(w.Steps),
	})

	// --- Worktree isolation (opt-in) ---
	if runMode == WorkflowModeLive {
		exec.setupWorktree()
	}

	// Record running state to DB so dashboard can see it immediately.
	recordWorkflowRun(cfg.HistoryDB, run)

	// Execute DAG.
	err := exec.executeDAG(execCtx)

	// Finalize run.
	exec.finalizeRun(err, now, ctx, execCtx)

	// Prefix status for non-live modes.
	switch runMode {
	case WorkflowModeDryRun:
		run.Status = "dry-run:" + run.Status
	case WorkflowModeShadow:
		run.Status = "shadow:" + run.Status
	}

	// Record to DB.
	recordWorkflowRun(cfg.HistoryDB, run)

	if runMode == WorkflowModeLive {
		if run.Status == "success" {
			log.InfoCtx(ctx, "workflow completed", "workflow", w.Name, "runID", runID[:8], "status", run.Status, "durationMs", run.DurationMs, "cost", run.TotalCost)
		} else {
			log.WarnCtx(ctx, "workflow completed with error", "workflow", w.Name, "runID", runID[:8], "status", run.Status, "durationMs", run.DurationMs, "cost", run.TotalCost)
		}
	} else {
		log.InfoCtx(ctx, "workflow completed", "workflow", w.Name, "runID", runID[:8], "status", run.Status, "mode", string(runMode), "durationMs", run.DurationMs, "cost", run.TotalCost)
	}

	return run
}

// setupExecContext applies workflow-level timeout and registers the canceller.
// Returns the execution context and a cleanup function that must be deferred.
func (e *workflowExecutor) setupExecContext(ctx context.Context) (context.Context, context.CancelFunc) {
	execCtx := ctx
	var timeoutCancel context.CancelFunc
	if e.workflow.Timeout != "" {
		if d, err := time.ParseDuration(e.workflow.Timeout); err == nil {
			execCtx, timeoutCancel = context.WithTimeout(ctx, d)
		}
	}
	execCtx, cancelRun := context.WithCancel(execCtx)
	runCancellers.Store(e.run.ID, cancelRun)

	return execCtx, func() {
		cancelRun()
		runCancellers.Delete(e.run.ID)
		if timeoutCancel != nil {
			timeoutCancel()
		}
	}
}

// setupWorktree creates a git worktree for isolated execution if configured.
func (e *workflowExecutor) setupWorktree() {
	w := e.workflow
	if !w.GitWorktree {
		return
	}
	repoDir := w.Workdir
	if repoDir == "" {
		repoDir = e.cfg.DefaultWorkdir
	}
	if repoDir == "" || !isGitRepo(repoDir) {
		return
	}
	branch := w.Branch
	if branch == "" {
		branch = "wf/" + slugifyBranch(w.Name)
	}
	branch = resolveTemplate(branch, e.wCtx)

	wtBaseDir := filepath.Join(e.cfg.RuntimeDir, "worktrees")
	wm := NewWorktreeManager(wtBaseDir)
	wtDir, wtErr := wm.Create(repoDir, e.run.ID, branch)
	if wtErr != nil {
		log.Warn("workflow worktree: creation failed, continuing without isolation",
			"workflow", w.Name, "error", wtErr)
		return
	}
	e.worktreeDir = wtDir
	e.repoDir = repoDir
	e.worktreeMgr = wm
	// Acquire session lock so Prune and Remove see this worktree as active and
	// refuse to delete it while DAG steps are running inside it.
	e.releaseWorktreeLock = acquireSessionLock(wtDir)
	log.Info("workflow worktree: created",
		"workflow", w.Name, "runID", e.run.ID[:8], "path", wtDir, "branch", branch)
}

// finalizeRun determines final status, calculates cost, publishes event,
// and handles worktree merge/cleanup after DAG execution completes.
func (e *workflowExecutor) finalizeRun(dagErr error, startTime time.Time, outerCtx, execCtx context.Context) {
	run := e.run
	run.FinishedAt = time.Now().Format(time.RFC3339)
	run.DurationMs = time.Since(startTime).Milliseconds()

	// Calculate total cost.
	for _, sr := range run.StepResults {
		run.TotalCost += sr.CostUSD
	}

	// Determine final status.
	if dagErr != nil {
		run.Status = "error"
		run.Error = dagErr.Error()
	} else if execCtx.Err() == context.DeadlineExceeded {
		run.Status = "timeout"
		run.Error = "workflow timeout exceeded"
	} else if outerCtx.Err() != nil {
		run.Status = "cancelled"
		run.Error = "workflow cancelled"
	} else {
		hasError := false
		for _, sr := range run.StepResults {
			if sr.Status == "error" || sr.Status == "timeout" {
				hasError = true
				break
			}
		}
		if hasError {
			run.Status = "error"
		} else {
			run.Status = "success"
		}
	}

	// Publish workflow completed event.
	eventData := map[string]any{
		"runId":      run.ID,
		"workflow":   e.workflow.Name,
		"status":     run.Status,
		"durationMs": run.DurationMs,
		"totalCost":  run.TotalCost,
	}
	if run.ResumedFrom != "" {
		eventData["resumedFrom"] = run.ResumedFrom
	}
	e.publishEvent("workflow_completed", eventData)

	// Worktree finalization.
	if e.worktreeDir != "" && e.worktreeMgr != nil {
		// Release the session lock before any cleanup so that Remove() does not
		// see a live PID and refuse. The DAG has already completed at this point.
		if e.releaseWorktreeLock != nil {
			e.releaseWorktreeLock()
			e.releaseWorktreeLock = nil
		}
		if run.Status == "success" {
			diffSummary, mergeErr := e.worktreeMgr.MergeBranchOnly(e.repoDir, e.worktreeDir)
			if mergeErr != nil {
				log.Warn("workflow worktree: merge failed, keeping for inspection",
					"workflow", e.workflow.Name, "path", e.worktreeDir, "error", mergeErr)
			} else {
				if diffSummary != "" {
					log.Info("workflow worktree: merged", "workflow", e.workflow.Name, "diff", diffSummary)
				}
				e.worktreeMgr.Remove(e.repoDir, e.worktreeDir)
			}
		} else {
			log.Info("workflow worktree: keeping for inspection (workflow failed)",
				"workflow", e.workflow.Name, "path", e.worktreeDir, "status", run.Status)
		}
	}
}

// executeDAG processes the workflow steps respecting dependencies.
func (e *workflowExecutor) executeDAG(ctx context.Context) error {
	steps := e.workflow.Steps

	// Build step map and dependency tracking.
	stepMap := make(map[string]*WorkflowStep)
	remaining := make(map[string]int) // step ID → pending dependency count
	dependents := make(map[string][]string) // step ID → steps that depend on it

	for i := range steps {
		s := &steps[i]
		stepMap[s.ID] = s
		remaining[s.ID] = len(s.DependsOn)
		for _, dep := range s.DependsOn {
			dependents[dep] = append(dependents[dep], s.ID)
		}
	}

	// Channels for coordination.
	readyCh := make(chan string, len(steps))
	doneCh := make(chan stepDoneMsg, len(steps))

	completed := 0
	total := len(steps)
	inFlight := 0
	aborted := false

	if e.resumeState != nil {
		// Resume mode: adjust dependency counts for already-completed steps.
		for id, sr := range e.resumeState {
			if _, exists := stepMap[id]; !exists {
				continue // step removed from definition
			}
			if sr.Status == "success" || sr.Status == "skipped" {
				completed++
				for _, dep := range dependents[id] {
					remaining[dep]--
				}
			}
		}
		// Replay condition branches: mark unchosen branches as skipped.
		for i := range steps {
			s := &steps[i]
			if stepType(s) == "condition" {
				if sr, ok := e.resumeState[s.ID]; ok && sr.Status == "success" {
					skipped := e.replayConditionSkips(s, sr, remaining, dependents)
					completed += len(skipped)
				}
			}
		}
		// Seed steps that are ready and not already completed.
		for id, cnt := range remaining {
			if cnt == 0 {
				if sr, ok := e.resumeState[id]; ok && (sr.Status == "success" || sr.Status == "skipped") {
					continue // already completed
				}
				readyCh <- id
			}
		}
	} else {
		// Normal mode: seed steps with no dependencies.
		for id, cnt := range remaining {
			if cnt == 0 {
				readyCh <- id
			}
		}
	}

	for completed < total {
		select {
		case <-ctx.Done():
			// Mark all pending steps as cancelled.
			e.mu.Lock()
			for id, sr := range e.run.StepResults {
				if sr.Status == "pending" || sr.Status == "running" {
					e.run.StepResults[id].Status = "cancelled"
				}
			}
			e.mu.Unlock()
			return ctx.Err()

		case stepID := <-readyCh:
			if aborted {
				// Mark as skipped and count as done.
				e.mu.Lock()
				e.run.StepResults[stepID].Status = "skipped"
				e.mu.Unlock()
				completed++
				// Propagate to dependents.
				for _, dep := range dependents[stepID] {
					remaining[dep]--
					if remaining[dep] == 0 {
						readyCh <- dep
					}
				}
				continue
			}

			// Don't execute steps already marked as skipped by condition handling.
			e.mu.Lock()
			skipSr := e.run.StepResults[stepID]
			isSkipped := skipSr != nil && skipSr.Status == "skipped"
			e.mu.Unlock()
			if isSkipped {
				continue
			}

			inFlight++
			go func(id string) {
				defer func() {
					if r := recover(); r != nil {
						log.Error("workflow step panic", "step", id, "recover", r)
						doneCh <- stepDoneMsg{
							id: id,
							result: &StepRunResult{
								StepID:     id,
								Status:     "error",
								Error:      fmt.Sprintf("panic: %v", r),
								StartedAt:  time.Now().Format(time.RFC3339),
								FinishedAt: time.Now().Format(time.RFC3339),
							},
						}
					}
				}()
				step := stepMap[id]
				result := e.executeStep(ctx, step)
				doneCh <- stepDoneMsg{id: id, result: result}
			}(stepID)

		case msg := <-doneCh:
			inFlight--
			completed++

			e.mu.Lock()
			sr := e.run.StepResults[msg.id]
			if sr == nil {
				log.Error("workflow: step result not found in map", "stepId", msg.id)
				sr = &StepRunResult{StepID: msg.id}
				e.run.StepResults[msg.id] = sr
			}
			sr.Status = msg.result.Status
			sr.Output = msg.result.Output
			sr.Error = msg.result.Error
			sr.StartedAt = msg.result.StartedAt
			sr.FinishedAt = msg.result.FinishedAt
			sr.DurationMs = msg.result.DurationMs
			sr.CostUSD = msg.result.CostUSD
			sr.TaskID = msg.result.TaskID
			sr.SessionID = msg.result.SessionID
			sr.Retries = msg.result.Retries

			// Update workflow context with step output.
			e.wCtx.Steps[msg.id] = &WorkflowStepResult{
				Output: msg.result.Output,
				Status: msg.result.Status,
				Error:  msg.result.Error,
			}
			e.mu.Unlock()

			// Check failure strategy.
			if msg.result.Status != "success" && msg.result.Status != "skipped" {
				step := stepMap[msg.id]
				onErr := step.OnError
				if onErr == "" {
					onErr = "stop"
				}
				if onErr == "stop" {
					aborted = true
				}
				// "skip" and "retry" (already retried by executeStep) just continue.
			}

			// Handle condition step: may skip dependents.
			step := stepMap[msg.id]
			if stepType(step) == "condition" {
				skippedSteps := e.handleConditionResult(step, msg.result, remaining, dependents, readyCh)
				// Propagate skipped steps through the DAG so their dependents get unblocked.
				// Use index-based loop: nested condition branches append to skippedSteps.
				visited := make(map[string]bool)
				for i := 0; i < len(skippedSteps); i++ {
					sid := skippedSteps[i]
					if visited[sid] {
						continue
					}
					visited[sid] = true
					completed++

					// If the skipped step is itself a condition, skip BOTH its branches
					// (since the condition was never evaluated, neither branch should run).
					if ss, ok := stepMap[sid]; ok && stepType(ss) == "condition" {
						for _, target := range []string{ss.Then, ss.Else} {
							if target == "" {
								continue
							}
							e.mu.Lock()
							if sr, ok := e.run.StepResults[target]; ok && sr.Status == "pending" {
								sr.Status = "skipped"
								skippedSteps = append(skippedSteps, target)
							}
							e.mu.Unlock()
						}
					}

					for _, dep := range dependents[sid] {
						remaining[dep]--
						if remaining[dep] == 0 {
							readyCh <- dep
						}
					}
				}
				// Fall through to propagate the condition step's own dependents.
			}

			// Unblock dependents.
			for _, dep := range dependents[msg.id] {
				remaining[dep]--
				if remaining[dep] == 0 {
					readyCh <- dep
				}
			}
		}
	}

	return nil
}

type stepDoneMsg struct {
	id     string
	result *StepRunResult
}

// handleConditionResult processes condition branching after evaluation.
// Returns the list of step IDs that were marked as skipped (for DAG propagation by caller).
func (e *workflowExecutor) handleConditionResult(step *WorkflowStep, result *StepRunResult,
	remaining map[string]int, dependents map[string][]string, readyCh chan string) []string {

	// The condition output is "then" or "else" — the chosen branch target.
	chosenTarget := strings.TrimSpace(result.Output)

	// Unblock dependents normally.
	for _, dep := range dependents[step.ID] {
		remaining[dep]--
		if remaining[dep] == 0 {
			readyCh <- dep
		}
	}

	// Skip the unchosen branch target (mark as skipped if it's not already done).
	skipTarget := ""
	if chosenTarget == step.Then && step.Else != "" {
		skipTarget = step.Else
	} else if chosenTarget == step.Else && step.Then != "" {
		skipTarget = step.Then
	}

	var skipped []string
	if skipTarget != "" {
		e.mu.Lock()
		if sr, ok := e.run.StepResults[skipTarget]; ok && sr.Status == "pending" {
			sr.Status = "skipped"
			skipped = append(skipped, skipTarget)
		}
		e.mu.Unlock()
	}
	return skipped
}

// replayConditionSkips replays a completed condition step's branch choice without re-executing.
// It marks the unchosen branch as skipped and adjusts dependency counts.
func (e *workflowExecutor) replayConditionSkips(step *WorkflowStep, sr *StepRunResult,
	remaining map[string]int, dependents map[string][]string) []string {

	chosenTarget := strings.TrimSpace(sr.Output)
	skipTarget := ""
	if chosenTarget == step.Then && step.Else != "" {
		skipTarget = step.Else
	} else if chosenTarget == step.Else && step.Then != "" {
		skipTarget = step.Then
	}

	var skipped []string
	if skipTarget != "" {
		e.mu.Lock()
		if existing, ok := e.run.StepResults[skipTarget]; ok && existing.Status == "pending" {
			existing.Status = "skipped"
			skipped = append(skipped, skipTarget)
		}
		e.mu.Unlock()
		// Adjust dependency counts for skipped step's dependents.
		for _, dep := range dependents[skipTarget] {
			remaining[dep]--
		}
	}
	return skipped
}

// isResumableStatus returns true if a workflow run status can be resumed.
func isResumableStatus(status string) bool {
	switch status {
	case "error", "cancelled", "timeout":
		return true
	}
	return false
}

// resumeWorkflow creates a new run that skips already-completed steps from a previous run.
// extraVars (if non-nil) are merged into the new run's Variables, overriding originals.
func resumeWorkflow(ctx context.Context, cfg *Config, originalRunID string,
	state *dispatchState, sem, childSem chan struct{}, extraVars map[string]string) (*WorkflowRun, error) {

	// Load the original run.
	originalRun, err := queryWorkflowRunByID(cfg.HistoryDB, originalRunID)
	if err != nil {
		return nil, fmt.Errorf("original run not found: %w", err)
	}

	// Validate status is resumable.
	if !isResumableStatus(originalRun.Status) {
		return nil, fmt.Errorf("run %s has status %q which is not resumable (must be error/cancelled/timeout)",
			originalRunID[:8], originalRun.Status)
	}

	// Load current workflow definition.
	w, err := loadWorkflowByName(cfg, originalRun.WorkflowName)
	if err != nil {
		return nil, fmt.Errorf("workflow %q not found: %w", originalRun.WorkflowName, err)
	}

	// Create new run.
	runID := newUUID()
	now := time.Now()
	run := &WorkflowRun{
		ID:           runID,
		WorkflowName: w.Name,
		Status:       "running",
		StartedAt:    now.Format(time.RFC3339),
		Variables:    originalRun.Variables,
		StepResults:  make(map[string]*StepRunResult),
		ResumedFrom:  originalRunID,
	}

	// Merge extra variables (override_variables, recovery keys, etc.).
	for k, v := range extraVars {
		run.Variables[k] = v
	}

	// Build resume state and initialize step results.
	resumeState := make(map[string]*StepRunResult)
	skippedCount := 0
	pendingCount := 0

	for _, s := range w.Steps {
		if prevSR, ok := originalRun.StepResults[s.ID]; ok && (prevSR.Status == "success" || prevSR.Status == "skipped") {
			// Carry over completed/skipped results (shallow copy).
			copied := *prevSR
			resumeState[s.ID] = &copied
			run.StepResults[s.ID] = &copied
			skippedCount++
		} else {
			// Step needs to run (pending, error, running, cancelled, timeout, or new).
			run.StepResults[s.ID] = &StepRunResult{
				StepID: s.ID,
				Status: "pending",
			}
			pendingCount++
		}
	}

	// Log orphaned steps (in original run but removed from current definition).
	stepSet := make(map[string]bool, len(w.Steps))
	for _, s := range w.Steps {
		stepSet[s.ID] = true
	}
	for stepID := range originalRun.StepResults {
		if !stepSet[stepID] {
			log.Info("workflow resume: orphaned step (removed from definition)", "step", stepID, "workflow", w.Name)
		}
	}

	// Pre-populate WorkflowContext with completed step outputs.
	wCtx := newWorkflowContext(w, originalRun.Variables)
	for id, sr := range resumeState {
		if sr.Status == "success" {
			wCtx.Steps[id] = &WorkflowStepResult{
				Output: sr.Output,
				Status: sr.Status,
				Error:  sr.Error,
			}
		}
	}

	var broker *sseBroker
	if state != nil {
		broker = state.broker
	}

	exec := &workflowExecutor{
		cfg:         cfg,
		workflow:    w,
		run:         run,
		wCtx:        wCtx,
		state:       state,
		sem:         sem,
		childSem:    childSem,
		broker:      broker,
		mode:        WorkflowModeLive,
		resumeState: resumeState,
	}

	// Mark original run as "resumed".
	if _, err := db.Query(cfg.HistoryDB, fmt.Sprintf(
		`UPDATE workflow_runs SET status='resumed', error='resumed as %s' WHERE id='%s'`,
		db.Escape(runID), db.Escape(originalRunID),
	)); err != nil {
		log.Warn("resumeWorkflow: failed to mark original as resumed", "error", err)
	}

	log.Info("workflow resumed", "workflow", w.Name, "originalRunID", originalRunID[:8],
		"newRunID", runID[:8], "skippedSteps", skippedCount, "pendingSteps", pendingCount)

	execCtx, execCancel := exec.setupExecContext(ctx)
	defer execCancel()

	// Publish workflow resumed event.
	exec.publishEvent("workflow_resumed", map[string]any{
		"runId":        runID,
		"workflow":     w.Name,
		"resumedFrom":  originalRunID,
		"skippedSteps": skippedCount,
		"pendingSteps": pendingCount,
	})

	exec.setupWorktree()

	// Record running state.
	recordWorkflowRun(cfg.HistoryDB, run)

	// Update task's workflowRunId to point to the new run immediately,
	// so resetStuckDoing() sees the correct (running) run during execution.
	if taskID := originalRun.Variables["_taskId"]; taskID != "" {
		tb := newTaskBoardEngine(cfg.HistoryDB, cfg.TaskBoard, cfg.Webhooks)
		tb.UpdateTask(taskID, map[string]any{"workflowRunId": runID})
	}

	// Execute DAG.
	dagErr := exec.executeDAG(execCtx)

	// Finalize run.
	exec.finalizeRun(dagErr, now, ctx, execCtx)

	// Record final state.
	recordWorkflowRun(cfg.HistoryDB, run)

	if run.Status == "success" {
		log.Info("workflow resume completed", "workflow", w.Name, "runID", runID[:8],
			"status", run.Status, "durationMs", run.DurationMs, "cost", run.TotalCost)
	} else {
		log.Warn("workflow resume completed with error", "workflow", w.Name, "runID", runID[:8],
			"status", run.Status, "durationMs", run.DurationMs, "cost", run.TotalCost)
	}

	return run, nil
}

// retryFromHumanGate resets a rejected human gate and resumes the workflow.
// It returns the new run ID or an error.
func retryFromHumanGate(ctx context.Context, cfg *Config, gateKey string,
	overrideVars map[string]string, state *dispatchState, sem, childSem chan struct{}) (string, error) {

	// Look up the gate.
	gate := queryHumanGate(cfg.HistoryDB, gateKey)
	if gate == nil {
		return "", fmt.Errorf("gate not found: %s", gateKey)
	}
	if gate.Status != "rejected" {
		return "", fmt.Errorf("gate %s has status %q, only rejected gates can be retried", gateKey, gate.Status)
	}

	// Reset gate to "waiting" (clears decision, response, completed_at, timeout_at).
	resetHumanGate(cfg.HistoryDB, gateKey)

	// Build extra variables: override_variables + recovery key for gate reuse.
	extraVars := make(map[string]string)
	for k, v := range overrideVars {
		extraVars[k] = v
	}
	extraVars["__hg_key_"+gate.StepID] = gate.Key

	// Resume the workflow from the gate's run (which should be in "error" status).
	run, err := resumeWorkflow(ctx, cfg, gate.RunID, state, sem, childSem, extraVars)
	if err != nil {
		return "", fmt.Errorf("resume workflow for gate retry: %w", err)
	}

	log.Info("human gate retry started", "gateKey", gateKey, "originalRunID", gate.RunID[:8], "newRunID", run.ID[:8])
	return run.ID, nil
}

// executeStep runs a single step with retry logic.
func (e *workflowExecutor) executeStep(ctx context.Context, step *WorkflowStep) *StepRunResult {
	start := time.Now()

	result := &StepRunResult{
		StepID:    step.ID,
		StartedAt: start.Format(time.RFC3339),
	}

	// Mark as running and checkpoint to DB so dashboard sees it immediately.
	e.mu.Lock()
	e.run.StepResults[step.ID].Status = "running"
	e.run.StepResults[step.ID].StartedAt = start.Format(time.RFC3339)
	e.mu.Unlock()
	checkpointRun(e)

	e.publishEvent("step_started", map[string]any{
		"runId":  e.run.ID,
		"stepId": step.ID,
		"type":   stepType(step),
		"role":   step.Agent,
	})

	maxRetries := step.RetryMax
	if step.OnError != "retry" {
		maxRetries = 0
	}

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			// Retry delay.
			delay := 5 * time.Second
			if step.RetryDelay != "" {
				if d, err := time.ParseDuration(step.RetryDelay); err == nil {
					delay = d
				}
			}
			select {
			case <-ctx.Done():
				result.Status = "cancelled"
				result.Error = "cancelled during retry wait"
				result.FinishedAt = time.Now().Format(time.RFC3339)
				result.DurationMs = time.Since(start).Milliseconds()
				return result
			case <-time.After(delay):
			}
			result.Retries = attempt
			log.DebugCtx(ctx, "step retry", "workflow", e.workflow.Name, "step", step.ID, "attempt", attempt+1, "maxRetries", maxRetries+1)
		}

		e.runStepOnce(ctx, step, result)

		if result.Status == "success" || result.Status == "skipped" {
			break
		}
	}

	result.FinishedAt = time.Now().Format(time.RFC3339)
	result.DurationMs = time.Since(start).Milliseconds()

	// If still failed after retries and onError is "skip", mark as skipped.
	if result.Status != "success" && step.OnError == "skip" {
		result.Status = "skipped"
	}

	e.publishEvent("step_completed", map[string]any{
		"runId":      e.run.ID,
		"stepId":     step.ID,
		"status":     result.Status,
		"durationMs": result.DurationMs,
		"costUsd":    result.CostUSD,
	})

	// Checkpoint run state to DB after each step for dashboard visibility.
	checkpointRun(e)

	return result
}

// runStepOnce executes a step once (no retry logic).
func (e *workflowExecutor) runStepOnce(ctx context.Context, step *WorkflowStep, result *StepRunResult) {
	e.mu.Lock()
	wCtx := e.wCtx
	e.mu.Unlock()

	st := stepType(step)

	// Dry-run mode: skip actual execution for dispatch/skill/handoff steps.
	if e.mode == WorkflowModeDryRun {
		switch st {
		case "dispatch":
			e.runDispatchStepDryRun(step, result, wCtx)
			return
		case "skill":
			e.runSkillStepDryRun(step, result)
			return
		case "handoff":
			e.runHandoffStepDryRun(step, result, wCtx)
			return
		case "condition":
			// Conditions evaluate normally in dry-run.
			e.runConditionStep(step, result, wCtx)
			return
		case "parallel":
			// Parallel steps recurse into runStepOnce which will hit dry-run again.
			e.runParallelStep(ctx, step, result, wCtx)
			return
		// --- P18.3: New step types in dry-run ---
		case "tool_call":
			result.Status = "success"
			result.Output = fmt.Sprintf("[DRY-RUN] Would call tool: %s", step.ToolName)
			return
		case "delay":
			result.Status = "success"
			result.Output = fmt.Sprintf("[DRY-RUN] Would delay: %s", step.Delay)
			return
		case "external":
			result.Status = "success"
			result.Output = fmt.Sprintf("[DRY-RUN] Would call external URL: %s (callback mode: %s)", step.ExternalURL, step.CallbackMode)
			return
		case "human":
			subtype := step.HumanSubtype
			if subtype == "" {
				subtype = "approval"
			}
			result.Status = "success"
			result.Output = fmt.Sprintf("[DRY-RUN] Would wait for human %s (assignee: %s)", subtype, step.HumanAssignee)
			return
		case "notify":
			msg := resolveTemplate(step.NotifyMsg, wCtx)
			result.Status = "success"
			result.Output = fmt.Sprintf("[DRY-RUN] Would notify (%s): %s", step.NotifyTo, msg)
			return
		default:
			result.Status = "error"
			result.Error = fmt.Sprintf("unknown step type: %s", step.Type)
			return
		}
	}

	// Shadow mode: execute dispatch steps without history recording.
	if e.mode == WorkflowModeShadow && st == "dispatch" {
		e.runDispatchStepShadow(ctx, step, result, wCtx)
		return
	}
	if e.mode == WorkflowModeShadow && st == "handoff" {
		e.runHandoffStepShadow(ctx, step, result, wCtx)
		return
	}

	switch st {
	case "dispatch":
		e.runDispatchStep(ctx, step, result, wCtx)
	case "skill":
		e.runSkillStep(ctx, step, result, wCtx)
	case "handoff":
		e.runHandoffStep(ctx, step, result, wCtx)
	case "condition":
		e.runConditionStep(step, result, wCtx)
	case "parallel":
		e.runParallelStep(ctx, step, result, wCtx)
	// --- P18.3: Workflow Triggers --- New step types.
	case "tool_call":
		e.runToolCallStep(ctx, step, result, wCtx)
	case "delay":
		e.runDelayStep(ctx, step, result)
	case "notify":
		e.runNotifyStep(step, result, wCtx)
	case "external":
		e.runExternalStep(ctx, step, result)
	case "human":
		e.runHumanStep(ctx, step, result)
	default:
		result.Status = "error"
		result.Error = fmt.Sprintf("unknown step type: %s", step.Type)
	}
}

// runDispatchStep executes an LLM dispatch step.
func (e *workflowExecutor) runDispatchStep(ctx context.Context, step *WorkflowStep,
	result *StepRunResult, wCtx *WorkflowContext) {

	task := buildStepTask(step, wCtx, e.workflow.Name)
	fillDefaults(e.cfg, &task)

	if task.Model == "" {
		log.Error("workflow dispatch: model still empty after defaults",
			"workflow", e.workflow.Name, "step", step.ID, "agent", task.Agent)
		result.Status = "error"
		result.Error = "model is required but not configured"
		return
	}

	// Override workdir with worktree path when isolation is active.
	if e.worktreeDir != "" {
		task.Workdir = e.worktreeDir
	}

	result.TaskID = task.ID
	result.SessionID = task.SessionID

	// Create a session for this step.
	createSession(e.cfg.HistoryDB, Session{
		ID:     task.SessionID,
		Agent:   task.Agent,
		Source: "workflow:" + e.workflow.Name,
		Status: "active",
		Title:  fmt.Sprintf("%s / %s", e.workflow.Name, step.ID),
	})

	// Enable streaming when broker is available.
	// Only assign when non-nil to avoid typed-nil interface trap.
	if e.broker != nil {
		task.SSEBroker = e.broker
	}
	task.WorkflowRunID = e.run.ID

	// Execute using runSingleTask (respects semaphore).
	taskResult := runSingleTask(ctx, e.cfg, task, e.sem, e.childSem, task.Agent)
	recordSessionActivity(e.cfg.HistoryDB, task, taskResult, task.Agent)

	result.Output = taskResult.Output
	result.CostUSD = taskResult.CostUSD
	result.SessionID = taskResult.SessionID

	switch taskResult.Status {
	case "success":
		result.Status = "success"

		// Auto-delegation: check for delegation markers in output.
		delegations := parseAutoDelegate(result.Output)
		if len(delegations) > 0 {
			result.Output = processAutoDelegations(ctx, e.cfg, delegations,
				result.Output, e.run.ID, task.Agent, step.ID,
				e.state, e.sem, e.childSem, e.broker)
		}
	case "timeout":
		result.Status = "timeout"
		result.Error = taskResult.Error
	default:
		result.Status = "error"
		result.Error = taskResult.Error
	}
}

// runSkillStep executes an external skill command.
func (e *workflowExecutor) runSkillStep(ctx context.Context, step *WorkflowStep,
	result *StepRunResult, wCtx *WorkflowContext) {

	skill := getSkill(e.cfg, step.Skill)
	if skill == nil {
		result.Status = "error"
		result.Error = fmt.Sprintf("skill %q not found", step.Skill)
		return
	}

	// Build vars from workflow context.
	vars := make(map[string]string)
	for k, v := range wCtx.Input {
		vars[k] = v
	}
	// Resolve skill args as template vars.
	for i, arg := range step.SkillArgs {
		vars[fmt.Sprintf("arg%d", i)] = resolveTemplate(arg, wCtx)
	}

	skillResult, err := executeSkill(ctx, *skill, vars)
	if err != nil {
		result.Status = "error"
		result.Error = err.Error()
		return
	}

	result.Output = skillResult.Output
	switch skillResult.Status {
	case "success":
		result.Status = "success"
	case "timeout":
		result.Status = "timeout"
		result.Error = skillResult.Error
	default:
		result.Status = "error"
		result.Error = skillResult.Error
	}
}

// runConditionStep evaluates a condition and returns the chosen branch.
func (e *workflowExecutor) runConditionStep(step *WorkflowStep,
	result *StepRunResult, wCtx *WorkflowContext) {

	condResult := evalCondition(step.If, wCtx)

	if condResult {
		result.Output = step.Then
		result.Status = "success"
	} else {
		if step.Else != "" {
			result.Output = step.Else
		} else {
			result.Output = ""
		}
		result.Status = "success"
	}
}

// runParallelStep executes sub-steps in parallel and waits for all.
func (e *workflowExecutor) runParallelStep(ctx context.Context, step *WorkflowStep,
	result *StepRunResult, wCtx *WorkflowContext) {

	var wg sync.WaitGroup
	subResults := make([]*StepRunResult, len(step.Parallel))

	for i, sub := range step.Parallel {
		wg.Add(1)
		go func(idx int, s WorkflowStep) {
			defer wg.Done()
			sr := &StepRunResult{StepID: s.ID, StartedAt: time.Now().Format(time.RFC3339)}
			e.runStepOnce(ctx, &s, sr)
			sr.FinishedAt = time.Now().Format(time.RFC3339)
			subResults[idx] = sr
		}(i, sub)
	}

	// Wait with early cancellation support: if ctx is cancelled,
	// give sub-steps a grace period to finish (they already received ctx cancellation).
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		// All sub-steps completed normally.
	case <-ctx.Done():
		gracePeriod := time.NewTimer(10 * time.Second)
		select {
		case <-done:
			gracePeriod.Stop()
		case <-gracePeriod.C:
			log.Warn("workflow parallel step: sub-steps did not finish within grace period", "step", step.ID)
		}
	}

	// Aggregate results.
	var outputs []string
	hasError := false
	for _, sr := range subResults {
		if sr == nil {
			continue
		}
		// Store sub-step results in workflow context.
		e.mu.Lock()
		e.wCtx.Steps[sr.StepID] = &WorkflowStepResult{
			Output: sr.Output,
			Status: sr.Status,
			Error:  sr.Error,
		}
		// Also track in run results.
		e.run.StepResults[sr.StepID] = sr
		e.mu.Unlock()

		result.CostUSD += sr.CostUSD
		if sr.Output != "" {
			outputs = append(outputs, sr.Output)
		}
		if sr.Status != "success" && sr.Status != "skipped" {
			hasError = true
		}
	}

	result.Output = strings.Join(outputs, "\n---\n")
	if hasError {
		result.Status = "error"
		result.Error = "one or more parallel sub-steps failed"
	} else {
		result.Status = "success"
	}
}

// runHandoffStep executes a handoff from one agent to another.
func (e *workflowExecutor) runHandoffStep(ctx context.Context, step *WorkflowStep,
	result *StepRunResult, wCtx *WorkflowContext) {

	// Get source step output.
	sourceResult, ok := wCtx.Steps[step.HandoffFrom]
	if !ok {
		result.Status = "error"
		result.Error = fmt.Sprintf("handoff source step %q has no result", step.HandoffFrom)
		return
	}

	sourceOutput := sourceResult.Output
	if sourceResult.Status != "success" {
		result.Status = "error"
		result.Error = fmt.Sprintf("handoff source step %q failed: %s", step.HandoffFrom, sourceResult.Error)
		return
	}

	// Resolve the instruction prompt with templates.
	instruction := resolveTemplate(step.Prompt, wCtx)

	// Determine source role.
	fromAgent := ""
	for _, s := range e.workflow.Steps {
		if s.ID == step.HandoffFrom {
			fromAgent = s.Agent
			break
		}
	}

	now := time.Now().Format(time.RFC3339)
	handoffID := newUUID()
	toSessionID := newUUID()

	// Get source step's session ID.
	fromSessionID := ""
	if sr, exists := e.run.StepResults[step.HandoffFrom]; exists {
		fromSessionID = sr.SessionID
	}

	h := Handoff{
		ID:            handoffID,
		WorkflowRunID: e.run.ID,
		FromAgent:      fromAgent,
		ToAgent:        step.Agent,
		FromStepID:    step.HandoffFrom,
		ToStepID:      step.ID,
		FromSessionID: fromSessionID,
		ToSessionID:   toSessionID,
		Context:       truncateStr(sourceOutput, e.cfg.PromptBudget.ContextMaxOrDefault()),
		Instruction:   instruction,
		Status:        "pending",
		CreatedAt:     now,
	}

	// Record handoff.
	recordHandoff(e.cfg.HistoryDB, h)

	// Record agent message.
	sendAgentMessage(e.cfg.HistoryDB, AgentMessage{
		WorkflowRunID: e.run.ID,
		FromAgent:      fromAgent,
		ToAgent:        step.Agent,
		Type:          "handoff",
		Content:       fmt.Sprintf("Handoff from %s: %s", fromAgent, truncate(instruction, 200)),
		RefID:         handoffID,
		CreatedAt:     now,
	})

	// Publish handoff event.
	e.publishEvent("handoff", map[string]any{
		"runId":      e.run.ID,
		"handoffId":  handoffID,
		"fromAgent":   fromAgent,
		"toAgent":     step.Agent,
		"fromStepId": step.HandoffFrom,
		"toStepId":   step.ID,
	})

	// Build task with handoff context.
	prompt := buildHandoffPrompt(sourceOutput, instruction)

	resolvedAgent := resolveTemplate(step.Agent, wCtx)
	task := Task{
		ID:             newUUID(),
		Name:           fmt.Sprintf("%s/%s (handoff:%s→%s)", e.workflow.Name, step.ID, fromAgent, resolvedAgent),
		Prompt:         prompt,
		Agent:          resolvedAgent,
		Model:          resolveTemplate(step.Model, wCtx),
		Provider:       resolveTemplate(step.Provider, wCtx),
		Timeout:        resolveTemplate(step.Timeout, wCtx),
		Budget:         step.Budget,
		PermissionMode: resolveTemplate(step.PermissionMode, wCtx),
		Source:         fmt.Sprintf("workflow:%s:handoff", e.workflow.Name),
		SessionID:      toSessionID,
	}
	fillDefaults(e.cfg, &task)

	// Override workdir with worktree path when isolation is active.
	if e.worktreeDir != "" {
		task.Workdir = e.worktreeDir
	}

	result.TaskID = task.ID
	result.SessionID = toSessionID

	// Create session.
	createSession(e.cfg.HistoryDB, Session{
		ID:        toSessionID,
		Agent:      resolvedAgent,
		Source:    fmt.Sprintf("workflow:%s:handoff", e.workflow.Name),
		Status:    "active",
		Title:     fmt.Sprintf("Handoff: %s → %s / %s", fromAgent, resolvedAgent, step.ID),
		CreatedAt: now,
		UpdatedAt: now,
	})

	// Update handoff to active.
	updateHandoffStatus(e.cfg.HistoryDB, handoffID, "active")

	// Enable streaming when broker is available.
	// Only assign when non-nil to avoid typed-nil interface trap.
	if e.broker != nil {
		task.SSEBroker = e.broker
	}
	task.WorkflowRunID = e.run.ID

	// Execute.
	taskResult := runSingleTask(ctx, e.cfg, task, e.sem, e.childSem, resolvedAgent)
	recordSessionActivity(e.cfg.HistoryDB, task, taskResult, resolvedAgent)

	result.Output = taskResult.Output
	result.CostUSD = taskResult.CostUSD

	switch taskResult.Status {
	case "success":
		result.Status = "success"
		updateHandoffStatus(e.cfg.HistoryDB, handoffID, "completed")
	case "timeout":
		result.Status = "timeout"
		result.Error = taskResult.Error
		updateHandoffStatus(e.cfg.HistoryDB, handoffID, "error")
	default:
		result.Status = "error"
		result.Error = taskResult.Error
		updateHandoffStatus(e.cfg.HistoryDB, handoffID, "error")
	}

	// Record response message.
	sendAgentMessage(e.cfg.HistoryDB, AgentMessage{
		WorkflowRunID: e.run.ID,
		FromAgent:      step.Agent,
		ToAgent:        fromAgent,
		Type:          "response",
		Content:       truncateStr(taskResult.Output, 2000),
		RefID:         handoffID,
		CreatedAt:     time.Now().Format(time.RFC3339),
	})

	log.DebugCtx(ctx, "handoff completed", "from", fromAgent, "to", step.Agent, "workflow", e.workflow.Name, "step", step.ID, "status", result.Status)
}

// --- P18.3: New Step Type Implementations ---

// runToolCallStep executes a registered tool.
func (e *workflowExecutor) runToolCallStep(ctx context.Context, step *WorkflowStep,
	result *StepRunResult, wCtx *WorkflowContext) {

	if e.cfg.Runtime.ToolRegistry == nil {
		result.Status = "error"
		result.Error = "tool registry not initialized"
		return
	}

	tool, ok := e.cfg.Runtime.ToolRegistry.(*ToolRegistry).Get(step.ToolName)
	if !ok {
		result.Status = "error"
		result.Error = fmt.Sprintf("tool %q not found", step.ToolName)
		return
	}

	// Expand {{var}} in tool input values.
	expandedInput := expandToolInput(step.ToolInput, wCtx.Input)
	inputJSON := toolInputToJSON(expandedInput)

	output, err := tool.Handler(ctx, e.cfg, inputJSON)
	if err != nil {
		result.Status = "error"
		result.Error = fmt.Sprintf("tool %q error: %v", step.ToolName, err)
		result.Output = output
		return
	}

	result.Status = "success"
	result.Output = output
}

// runDelayStep waits for the specified duration.
func (e *workflowExecutor) runDelayStep(ctx context.Context, step *WorkflowStep,
	result *StepRunResult) {

	d, err := time.ParseDuration(step.Delay)
	if err != nil {
		result.Status = "error"
		result.Error = fmt.Sprintf("invalid delay %q: %v", step.Delay, err)
		return
	}

	select {
	case <-time.After(d):
		result.Status = "success"
		result.Output = fmt.Sprintf("delayed %s", d)
	case <-ctx.Done():
		result.Status = "cancelled"
		result.Error = "cancelled during delay"
	}
}

// runNotifyStep sends a notification message.
func (e *workflowExecutor) runNotifyStep(step *WorkflowStep,
	result *StepRunResult, wCtx *WorkflowContext) {

	// Expand {{var}} in the notification message.
	msg := resolveTemplate(step.NotifyMsg, wCtx)

	// Log the notification (fallback if no notification channel available).
	log.Info("workflow notify", "workflow", e.workflow.Name, "step", step.ID,
		"to", step.NotifyTo, "message", truncateStr(msg, 200))

	// Publish as SSE event so external consumers can act on it.
	if e.broker != nil {
		e.broker.Publish("_triggers", SSEEvent{
			Type:   "workflow_notify",
			TaskID: e.run.ID,
			Data: map[string]any{
				"workflow": e.workflow.Name,
				"step":     step.ID,
				"message":  msg,
				"channel":  step.NotifyTo,
			},
		})
	}

	result.Status = "success"
	result.Output = msg
}

// --- Dry-Run Step Implementations ---

// runDispatchStepDryRun estimates cost without calling any provider.
func (e *workflowExecutor) runDispatchStepDryRun(step *WorkflowStep,
	result *StepRunResult, wCtx *WorkflowContext) {

	task := buildStepTask(step, wCtx, e.workflow.Name)
	fillDefaults(e.cfg, &task)

	result.TaskID = task.ID
	result.SessionID = task.SessionID

	est := estimateTaskCost(e.cfg, task, task.Agent)
	result.CostUSD = est.EstimatedCostUSD
	result.Status = "success"
	result.Output = fmt.Sprintf("[DRY-RUN] step=%s role=%s model=%s estimated_cost=$%.4f",
		step.ID, task.Agent, est.Model, est.EstimatedCostUSD)
}

// runSkillStepDryRun returns mock output without running the skill.
func (e *workflowExecutor) runSkillStepDryRun(step *WorkflowStep,
	result *StepRunResult) {

	result.Status = "success"
	result.Output = fmt.Sprintf("[DRY-RUN] Would execute skill: %s", step.Skill)
}

// runHandoffStepDryRun estimates cost for the handoff provider call without executing.
func (e *workflowExecutor) runHandoffStepDryRun(step *WorkflowStep,
	result *StepRunResult, wCtx *WorkflowContext) {

	// Verify source step exists in context (same validation as live).
	sourceResult, ok := wCtx.Steps[step.HandoffFrom]
	if !ok {
		result.Status = "error"
		result.Error = fmt.Sprintf("handoff source step %q has no result", step.HandoffFrom)
		return
	}
	if sourceResult.Status != "success" {
		result.Status = "error"
		result.Error = fmt.Sprintf("handoff source step %q failed: %s", step.HandoffFrom, sourceResult.Error)
		return
	}

	// Build the handoff task to estimate cost.
	instruction := resolveTemplate(step.Prompt, wCtx)
	sourceOutput := sourceResult.Output
	prompt := buildHandoffPrompt(sourceOutput, instruction)

	resolvedAgent := resolveTemplate(step.Agent, wCtx)
	task := Task{
		ID:             newUUID(),
		Name:           fmt.Sprintf("%s/%s (handoff)", e.workflow.Name, step.ID),
		Prompt:         prompt,
		Agent:          resolvedAgent,
		Model:          resolveTemplate(step.Model, wCtx),
		Provider:       resolveTemplate(step.Provider, wCtx),
		Timeout:        resolveTemplate(step.Timeout, wCtx),
		Budget:         step.Budget,
		PermissionMode: resolveTemplate(step.PermissionMode, wCtx),
		Source:         fmt.Sprintf("workflow:%s:handoff", e.workflow.Name),
		SessionID:      newUUID(),
	}
	fillDefaults(e.cfg, &task)

	est := estimateTaskCost(e.cfg, task, resolvedAgent)
	result.TaskID = task.ID
	result.SessionID = task.SessionID
	result.CostUSD = est.EstimatedCostUSD
	result.Status = "success"
	result.Output = fmt.Sprintf("[DRY-RUN] step=%s role=%s model=%s estimated_cost=$%.4f (handoff)",
		step.ID, resolvedAgent, est.Model, est.EstimatedCostUSD)
}

// --- Shadow Step Implementations ---

// runDispatchStepShadow executes the dispatch step but skips history/session recording.
func (e *workflowExecutor) runDispatchStepShadow(ctx context.Context, step *WorkflowStep,
	result *StepRunResult, wCtx *WorkflowContext) {

	task := buildStepTask(step, wCtx, e.workflow.Name)
	fillDefaults(e.cfg, &task)

	result.TaskID = task.ID
	result.SessionID = task.SessionID

	// Execute using the provider directly (no history/session recording).
	taskResult := runSingleTaskNoRecord(ctx, e.cfg, task, e.sem, e.childSem, task.Agent)

	result.Output = taskResult.Output
	result.CostUSD = taskResult.CostUSD
	result.SessionID = taskResult.SessionID

	switch taskResult.Status {
	case "success":
		result.Status = "success"
	case "timeout":
		result.Status = "timeout"
		result.Error = taskResult.Error
	default:
		result.Status = "error"
		result.Error = taskResult.Error
	}
}

// runHandoffStepShadow executes the handoff step but skips history/session recording.
func (e *workflowExecutor) runHandoffStepShadow(ctx context.Context, step *WorkflowStep,
	result *StepRunResult, wCtx *WorkflowContext) {

	// Get source step output.
	sourceResult, ok := wCtx.Steps[step.HandoffFrom]
	if !ok {
		result.Status = "error"
		result.Error = fmt.Sprintf("handoff source step %q has no result", step.HandoffFrom)
		return
	}
	if sourceResult.Status != "success" {
		result.Status = "error"
		result.Error = fmt.Sprintf("handoff source step %q failed: %s", step.HandoffFrom, sourceResult.Error)
		return
	}

	sourceOutput := sourceResult.Output
	instruction := resolveTemplate(step.Prompt, wCtx)
	prompt := buildHandoffPrompt(sourceOutput, instruction)

	resolvedAgent := resolveTemplate(step.Agent, wCtx)
	task := Task{
		ID:             newUUID(),
		Name:           fmt.Sprintf("%s/%s (handoff:%s)", e.workflow.Name, step.ID, resolvedAgent),
		Prompt:         prompt,
		Agent:          resolvedAgent,
		Model:          resolveTemplate(step.Model, wCtx),
		Provider:       resolveTemplate(step.Provider, wCtx),
		Timeout:        resolveTemplate(step.Timeout, wCtx),
		Budget:         step.Budget,
		PermissionMode: resolveTemplate(step.PermissionMode, wCtx),
		Source:         fmt.Sprintf("workflow:%s:handoff", e.workflow.Name),
		SessionID:      newUUID(),
	}
	fillDefaults(e.cfg, &task)

	result.TaskID = task.ID
	result.SessionID = task.SessionID

	// Execute without recording history/session/handoff metadata.
	taskResult := runSingleTaskNoRecord(ctx, e.cfg, task, e.sem, e.childSem, resolvedAgent)

	result.Output = taskResult.Output
	result.CostUSD = taskResult.CostUSD

	switch taskResult.Status {
	case "success":
		result.Status = "success"
	case "timeout":
		result.Status = "timeout"
		result.Error = taskResult.Error
	default:
		result.Status = "error"
		result.Error = taskResult.Error
	}
}

// runSingleTaskNoRecord executes a task using the provider but skips
// history recording and session activity tracking. Used by shadow mode.
func runSingleTaskNoRecord(ctx context.Context, cfg *Config, task Task, sem, childSem chan struct{}, agentName string) TaskResult {
	// Validate directories before running.
	if err := validateDirs(cfg, task, agentName); err != nil {
		return TaskResult{
			ID: task.ID, Name: task.Name, Status: "error",
			Error: err.Error(), Model: task.Model, SessionID: task.SessionID,
		}
	}

	s := selectSem(sem, childSem, task.Depth)
	if task.Depth == 0 && cfg.Runtime.SlotPressureGuard != nil {
		_, err := cfg.Runtime.SlotPressureGuard.(*dtypes.SlotPressureGuard).AcquireSlot(ctx, s, task.Source)
		if err != nil {
			return TaskResult{
				ID: task.ID, Name: task.Name, Status: "cancelled",
				Error: "slot acquisition cancelled: " + err.Error(), Model: task.Model, SessionID: task.SessionID,
			}
		}
		defer cfg.Runtime.SlotPressureGuard.(*dtypes.SlotPressureGuard).ReleaseSlot()
		defer func() { <-s }()
	} else {
		s <- struct{}{}
		defer func() { <-s }()
	}

	// Signal that this task has acquired a slot and is about to execute.
	if task.OnStart != nil {
		task.OnStart()
	}

	providerName := resolveProviderName(cfg, task, agentName)

	log.DebugCtx(ctx, "shadow task start",
		"taskId", task.ID[:8], "name", task.Name,
		"model", task.Model, "provider", providerName)

	timeout, err := time.ParseDuration(task.Timeout)
	if err != nil {
		timeout = 15 * time.Minute
	}
	taskCtx, taskCancel := context.WithTimeout(ctx, timeout)
	defer taskCancel()

	start := time.Now()
	pr := executeWithProvider(taskCtx, cfg, task, agentName, cfg.Runtime.ProviderRegistry.(*providerRegistry), nil)
	elapsed := time.Since(start)

	result := TaskResult{
		ID:         task.ID,
		Name:       task.Name,
		Output:     pr.Output,
		CostUSD:    pr.CostUSD,
		DurationMs: elapsed.Milliseconds(),
		Model:      task.Model,
		SessionID:  pr.SessionID,
		TokensIn:   pr.TokensIn,
		TokensOut:  pr.TokensOut,
		ProviderMs: pr.ProviderMs,
		Provider:   pr.Provider,
	}
	if result.SessionID == "" {
		result.SessionID = task.SessionID
	}

	if taskCtx.Err() == context.DeadlineExceeded {
		result.Status = "timeout"
		result.Error = fmt.Sprintf("timed out after %v", timeout)
	} else if ctx.Err() != nil {
		result.Status = "cancelled"
		result.Error = "cancelled"
	} else if pr.IsError {
		result.Status = "error"
		result.Error = pr.Error
	} else {
		result.Status = "success"
	}

	log.DebugCtx(ctx, "shadow task done",
		"taskId", task.ID[:8], "name", task.Name,
		"elapsed", elapsed.Round(time.Millisecond),
		"cost", result.CostUSD,
		"status", result.Status)

	// Deliberately skip: recordHistory, recordSessionActivity, saveTaskOutput, webhooks.
	return result
}

// buildStepSummaries returns step metadata for DAG visualization.
func buildStepSummaries(steps []WorkflowStep) []map[string]any {
	var out []map[string]any
	for _, s := range steps {
		out = append(out, map[string]any{
			"id":        s.ID,
			"type":      stepType(&s),
			"role":      s.Agent,
			"dependsOn": s.DependsOn,
		})
	}
	return out
}

// publishEvent sends an SSE event for workflow progress.
func (e *workflowExecutor) publishEvent(eventType string, data map[string]any) {
	if e.broker == nil {
		return
	}
	topics := []string{
		"workflow:" + e.run.ID,
		"workflow:" + e.workflow.Name,
	}
	// Broadcast human gate events to the dashboard feed so the
	// "Waiting for You" panel can update in real time.
	if eventType == "human_gate_waiting" || eventType == "human_gate_responded" {
		topics = append(topics, SSEDashboardKey)
	}
	e.broker.PublishMulti(topics, SSEEvent{
		Type:   eventType,
		TaskID: e.run.ID,
		Data:   data,
	})
}

// --- Workflow Run DB ---

const workflowRunsTableSQL = `CREATE TABLE IF NOT EXISTS workflow_runs (
  id TEXT PRIMARY KEY,
  workflow_name TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'running',
  started_at TEXT NOT NULL,
  finished_at TEXT DEFAULT '',
  duration_ms INTEGER DEFAULT 0,
  total_cost REAL DEFAULT 0,
  variables TEXT DEFAULT '{}',
  step_results TEXT DEFAULT '{}',
  error TEXT DEFAULT '',
  created_at TEXT NOT NULL
)`

func initWorkflowRunsTable(dbPath string) {
	if dbPath == "" {
		return
	}
	if _, err := db.Query(dbPath, workflowRunsTableSQL); err != nil {
		log.Warn("init workflow_runs table failed", "error", err)
	}
	// Migration: add resumed_from column (no-op if column already exists).
	if _, err := db.Query(dbPath, "ALTER TABLE workflow_runs ADD COLUMN resumed_from TEXT DEFAULT ''"); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") {
			log.Warn("migration: add resumed_from column failed", "error", err)
		}
	}
}

func recordWorkflowRun(dbPath string, run *WorkflowRun) {
	if dbPath == "" {
		return
	}
	initWorkflowRunsTable(dbPath)

	varsJSON, _ := json.Marshal(run.Variables)
	stepsJSON, _ := json.Marshal(run.StepResults)

	sql := fmt.Sprintf(
		`INSERT OR REPLACE INTO workflow_runs (id, workflow_name, status, started_at, finished_at, duration_ms, total_cost, variables, step_results, error, created_at, resumed_from)
		 VALUES ('%s','%s','%s','%s','%s',%d,%f,'%s','%s','%s','%s','%s')`,
		db.Escape(run.ID),
		db.Escape(run.WorkflowName),
		db.Escape(run.Status),
		db.Escape(run.StartedAt),
		db.Escape(run.FinishedAt),
		run.DurationMs,
		run.TotalCost,
		db.Escape(string(varsJSON)),
		db.Escape(string(stepsJSON)),
		db.Escape(run.Error),
		db.Escape(run.StartedAt),
		db.Escape(run.ResumedFrom),
	)

	if _, err := db.Query(dbPath, sql); err != nil {
		log.Warn("record workflow run failed", "error", err)
	}
}

// queryWorkflowRuns returns recent workflow runs.
func queryWorkflowRuns(dbPath string, limit int, workflowName string) ([]WorkflowRun, error) {
	if limit <= 0 {
		limit = 20
	}

	where := ""
	if workflowName != "" {
		where = fmt.Sprintf("WHERE workflow_name='%s'", db.Escape(workflowName))
	}

	sql := fmt.Sprintf(
		`SELECT id, workflow_name, status, started_at, finished_at, duration_ms, total_cost, variables, step_results, error, COALESCE(resumed_from,'') as resumed_from
		 FROM workflow_runs %s ORDER BY created_at DESC LIMIT %d`,
		where, limit,
	)

	rows, err := db.Query(dbPath, sql)
	if err != nil {
		// Table might not exist yet.
		if strings.Contains(err.Error(), "no such table") {
			return nil, nil
		}
		return nil, err
	}

	var runs []WorkflowRun
	for _, row := range rows {
		run := WorkflowRun{
			ID:           jsonStr(row["id"]),
			WorkflowName: jsonStr(row["workflow_name"]),
			Status:       jsonStr(row["status"]),
			StartedAt:    jsonStr(row["started_at"]),
			FinishedAt:   jsonStr(row["finished_at"]),
			DurationMs:   int64(jsonFloat(row["duration_ms"])),
			TotalCost:    jsonFloat(row["total_cost"]),
			Error:        jsonStr(row["error"]),
			StepResults:  make(map[string]*StepRunResult),
			ResumedFrom:  jsonStr(row["resumed_from"]),
		}
		// Parse variables.
		if v := jsonStr(row["variables"]); v != "" {
			json.Unmarshal([]byte(v), &run.Variables)
		}
		// Parse step results.
		if v := jsonStr(row["step_results"]); v != "" {
			json.Unmarshal([]byte(v), &run.StepResults)
		}
		runs = append(runs, run)
	}
	return runs, nil
}

// queryWorkflowRunByID returns a single workflow run.
func queryWorkflowRunByID(dbPath, id string) (*WorkflowRun, error) {
	sql := fmt.Sprintf(
		`SELECT id, workflow_name, status, started_at, finished_at, duration_ms, total_cost, variables, step_results, error, COALESCE(resumed_from,'') as resumed_from
		 FROM workflow_runs WHERE id='%s'`,
		db.Escape(id),
	)

	rows, err := db.Query(dbPath, sql)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil, fmt.Errorf("workflow run %q not found", id)
		}
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("workflow run %q not found", id)
	}

	row := rows[0]
	run := &WorkflowRun{
		ID:           jsonStr(row["id"]),
		WorkflowName: jsonStr(row["workflow_name"]),
		Status:       jsonStr(row["status"]),
		StartedAt:    jsonStr(row["started_at"]),
		FinishedAt:   jsonStr(row["finished_at"]),
		DurationMs:   int64(jsonFloat(row["duration_ms"])),
		TotalCost:    jsonFloat(row["total_cost"]),
		Error:        jsonStr(row["error"]),
		StepResults:  make(map[string]*StepRunResult),
		ResumedFrom:  jsonStr(row["resumed_from"]),
	}
	if v := jsonStr(row["variables"]); v != "" {
		json.Unmarshal([]byte(v), &run.Variables)
	}
	if v := jsonStr(row["step_results"]); v != "" {
		json.Unmarshal([]byte(v), &run.StepResults)
	}
	return run, nil
}

// =============================================================================
// Type aliases — re-export from internal/workflow for root-package callers
// =============================================================================

type CallbackManager = iwf.CallbackManager
type CallbackResult = iwf.CallbackResult
type CallbackRecord = iwf.CallbackRecord
type DeliverResult = iwf.DeliverResult
type DeliverWithSeq = iwf.DeliverWithSeq

const (
	DeliverOK      = iwf.DeliverOK
	DeliverNoEntry = iwf.DeliverNoEntry
	DeliverDup     = iwf.DeliverDup
	DeliverFull    = iwf.DeliverFull
)

type TriggerInfo = iwf.TriggerInfo
type WorkflowTriggerEngine = iwf.WorkflowTriggerEngine

// =============================================================================
// Package-level singletons
// =============================================================================

// callbackMgr is the process-wide singleton CallbackManager.
var callbackMgr *CallbackManager

// runCancellers maps runID -> context.CancelFunc for the cancel API.
var runCancellers sync.Map

// =============================================================================
// Constructor wrappers
// =============================================================================

func newCallbackManager(dbPath string) *CallbackManager {
	return iwf.NewCallbackManager(dbPath)
}

func newWorkflowTriggerEngine(cfg *Config, state *dispatchState, sem, childSem chan struct{}, broker *sseBroker) *WorkflowTriggerEngine {
	deps := iwf.TriggerDeps{
		ExecuteWorkflow: func(ctx context.Context, c *Config, wf *Workflow, vars map[string]string) iwf.TriggerRunResult {
			run := executeWorkflow(ctx, c, wf, vars, state, sem, childSem)
			return iwf.TriggerRunResult{
				ID:         run.ID,
				Status:     run.Status,
				Error:      run.Error,
				DurationMs: run.DurationMs,
			}
		},
		LoadWorkflowByName: func(c *Config, name string) (*Workflow, error) {
			return loadWorkflowByName(c, name)
		},
	}
	return iwf.NewWorkflowTriggerEngine(cfg, deps, broker)
}

// =============================================================================
// DB helper wrappers (lowercase aliases used across the codebase)
// =============================================================================

func initCallbackTable(dbPath string)   { iwf.InitCallbackTable(dbPath) }
func initTriggerRunsTable(dbPath string) { iwf.InitTriggerRunsTable(dbPath) }

func recordPendingCallback(dbPath, key, runID, stepID, mode, authMode, url, body, timeoutAt string) {
	iwf.RecordPendingCallback(dbPath, key, runID, stepID, mode, authMode, url, body, timeoutAt)
}

func queryPendingCallbackByKey(dbPath, key string) *CallbackRecord {
	return iwf.QueryPendingCallbackByKey(dbPath, key)
}

func queryPendingCallback(dbPath, key string) *CallbackRecord {
	return iwf.QueryPendingCallback(dbPath, key)
}

func queryPendingCallbacksByRun(dbPath, runID string) []*CallbackRecord {
	return iwf.QueryPendingCallbacksByRun(dbPath, runID)
}

func markPostSent(dbPath, key string)          { iwf.MarkPostSent(dbPath, key) }
func resetCallbackRecord(dbPath, key string)   { iwf.ResetCallbackRecord(dbPath, key) }
func clearPendingCallback(dbPath, key string)  { iwf.ClearPendingCallback(dbPath, key) }
func updateCallbackRunID(dbPath, key, newRunID string) { iwf.UpdateCallbackRunID(dbPath, key, newRunID) }

func markCallbackDelivered(dbPath, key string, seq int, result CallbackResult) {
	iwf.MarkCallbackDelivered(dbPath, key, seq, result)
}

func isCallbackDelivered(dbPath, key string, seq int) bool {
	return iwf.IsCallbackDelivered(dbPath, key, seq)
}

func appendStreamingCallback(dbPath, key string, seq int, result CallbackResult) {
	iwf.AppendStreamingCallback(dbPath, key, seq, result)
}

func queryStreamingCallbacks(dbPath, key string) []CallbackResult {
	return iwf.QueryStreamingCallbacks(dbPath, key)
}

func cleanupExpiredCallbacks(dbPath string)     { iwf.CleanupExpiredCallbacks(dbPath) }
func cleanupExpiredHumanGates(dbPath string)    { iwf.CleanupExpiredHumanGates(dbPath) }

// --- Human gate DB wrappers ---

type HumanGateRecord = iwf.HumanGateRecord

func initHumanGateTable(dbPath string)   { iwf.InitHumanGateTable(dbPath) }
func recordHumanGate(dbPath, key, runID, stepID, workflowName, subtype, prompt, assignee, timeoutAt, options, context string) {
	iwf.RecordHumanGate(dbPath, key, runID, stepID, workflowName, subtype, prompt, assignee, timeoutAt, options, context)
}
func queryHumanGate(dbPath, key string) *HumanGateRecord       { return iwf.QueryHumanGate(dbPath, key) }
func queryPendingHumanGatesByRun(dbPath, runID string) []*HumanGateRecord {
	return iwf.QueryPendingHumanGatesByRun(dbPath, runID)
}
func queryAllPendingHumanGates(dbPath, status string) []*HumanGateRecord {
	return iwf.QueryAllPendingHumanGates(dbPath, status)
}
func countPendingHumanGates(dbPath string) int { return iwf.CountPendingHumanGates(dbPath) }
func completeHumanGate(dbPath, key, decision, response, respondedBy string) {
	iwf.CompleteHumanGate(dbPath, key, decision, response, respondedBy)
}
func rejectHumanGate(dbPath, key, reason, respondedBy string) {
	iwf.RejectHumanGate(dbPath, key, reason, respondedBy)
}
func timeoutHumanGate(dbPath, key string)                      { iwf.TimeoutHumanGate(dbPath, key) }
func cancelHumanGate(dbPath, key string)                       { iwf.CancelHumanGate(dbPath, key) }
func resetHumanGate(dbPath, key string)                        { iwf.ResetHumanGate(dbPath, key) }
func updateHumanGateRunID(dbPath, key, newRunID string)        { iwf.UpdateHumanGateRunID(dbPath, key, newRunID) }

func recordTriggerRun(dbPath, triggerName, workflowName, runID, status, startedAt, finishedAt, errMsg string) {
	iwf.RecordTriggerRun(dbPath, triggerName, workflowName, runID, status, startedAt, finishedAt, errMsg)
}

func queryTriggerRuns(dbPath, triggerName string, limit int) ([]map[string]any, error) {
	return iwf.QueryTriggerRuns(dbPath, triggerName, limit)
}

// =============================================================================
// HMAC helpers
// =============================================================================

func callbackSignatureSecret(serverSecret, callbackKey string) string {
	return iwf.CallbackSignatureSecret(serverSecret, callbackKey)
}

func verifyCallbackSignature(body []byte, secret, signatureHex string) bool {
	return iwf.VerifyCallbackSignature(body, secret, signatureHex)
}

// =============================================================================
// JSON helpers
// =============================================================================

func extractJSONPath(jsonStr, path string) string { return iwf.ExtractJSONPath(jsonStr, path) }

func applyResponseMapping(body string, mapping *ResponseMapping) string {
	return iwf.ApplyResponseMapping(body, mapping)
}

// =============================================================================
// Validation / utility helpers
// =============================================================================

func isValidCallbackKey(key string) bool { return iwf.IsValidCallbackKey(key) }

func parseDurationWithDays(s string) (time.Duration, error) { return iwf.ParseDurationWithDays(s) }

func matchEventType(eventType, pattern string) bool { return iwf.MatchEventType(eventType, pattern) }

func toolInputToJSON(input map[string]string) json.RawMessage { return iwf.ToolInputToJSON(input) }

// expandVars and expandToolInput are already wrapped in workflow.go via iwf.ExpandVars /
// iwf.ExpandToolInput. Keep thin wrappers here so workflow_exec.go callers compile.
func expandVars(s string, vars map[string]string) string { return iwf.ExpandVars(s, vars) }
func expandToolInput(input, vars map[string]string) map[string]string {
	return iwf.ExpandToolInput(input, vars)
}

// =============================================================================
// HTTP helper
// =============================================================================

func httpPostWithRetry(ctx context.Context, url, contentType string, headers map[string]string, body string, maxRetry int) (*http.Response, error) {
	return iwf.HTTPPostWithRetry(ctx, url, contentType, headers, body, maxRetry)
}

// =============================================================================
// Wait helpers
// =============================================================================

// waitSingleCallback waits for a single callback result or timeout.
func waitSingleCallback(ctx context.Context, ch chan CallbackResult, _ string, _ *WorkflowStep, timeout time.Duration) *CallbackResult {
	return iwf.WaitSingleCallback(ctx, ch, timeout)
}

// waitStreamingCallback waits for multiple callbacks until DonePath==DoneValue or timeout.
func waitStreamingCallback(ctx context.Context, ch chan CallbackResult, _ string, step *WorkflowStep, timeout time.Duration) (*CallbackResult, []CallbackResult) {
	return iwf.WaitStreamingCallback(ctx, ch, step.CallbackResponseMap, timeout)
}

// handleCallbackTimeout sets result fields for a timed-out callback.
func handleCallbackTimeout(step *WorkflowStep, result *StepRunResult, timeout time.Duration, ctx context.Context) {
	onTimeout := step.OnTimeout
	r := iwf.HandleCallbackTimeout(onTimeout, timeout, ctx.Err())
	result.Status = r.Status
	result.Error = r.Error
	if r.Output != "" {
		result.Output = r.Output
	}
}

// =============================================================================
// Template helpers (on workflowExecutor — root-only, accesses wCtx)
// =============================================================================

// resolveTemplateWithFields resolves {{...}} templates and also handles
// {{steps.id.output.field}} by extracting JSON fields from step outputs.
func (e *workflowExecutor) resolveTemplateWithFields(tmpl string) string {
	result := templateVarRe.ReplaceAllStringFunc(tmpl, func(match string) string {
		expr := strings.TrimSpace(match[2 : len(match)-2])
		parts := strings.SplitN(expr, ".", 4)

		// Handle {{steps.id.output.fieldPath}}
		if len(parts) >= 4 && parts[0] == "steps" && parts[2] == "output" {
			stepID := parts[1]
			fieldPath := strings.Join(parts[3:], ".")
			stepResult, ok := e.wCtx.Steps[stepID]
			if !ok {
				return ""
			}
			return extractJSONPath(stepResult.Output, fieldPath)
		}

		// Fallback to standard resolution.
		return resolveExpr(expr, e.wCtx)
	})
	return result
}

// resolveTemplateMapWithFields resolves all values in a map using resolveTemplateWithFields.
func (e *workflowExecutor) resolveTemplateMapWithFields(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	result := make(map[string]string, len(m))
	for k, v := range m {
		result[k] = e.resolveTemplateWithFields(v)
	}
	return result
}

// resolveTemplateXMLEscaped resolves templates and XML-escapes the result.
func (e *workflowExecutor) resolveTemplateXMLEscaped(tmpl string) string {
	result := e.resolveTemplateWithFields(tmpl)
	result = strings.ReplaceAll(result, "&", "&amp;")
	result = strings.ReplaceAll(result, "<", "&lt;")
	result = strings.ReplaceAll(result, ">", "&gt;")
	result = strings.ReplaceAll(result, "\"", "&quot;")
	result = strings.ReplaceAll(result, "'", "&apos;")
	return result
}

// =============================================================================
// runExternalStep — root-only: accesses workflowExecutor, callbackMgr singleton
// =============================================================================

// runExternalStep executes an external step: POST to URL, wait for callback.
func (e *workflowExecutor) runExternalStep(ctx context.Context, step *WorkflowStep, result *StepRunResult) {
	if callbackMgr == nil {
		result.Status = "error"
		result.Error = "callback manager not initialized"
		return
	}

	// Resolve templates in all fields.
	url := e.resolveTemplateWithFields(step.ExternalURL)
	headers := e.resolveTemplateMapWithFields(step.ExternalHeaders)

	// Resolve auth mode early — needed before callbackKey for header injection.
	authMode := step.CallbackAuth
	if authMode == "" {
		authMode = "bearer"
	}

	callbackKey := e.resolveTemplateWithFields(step.CallbackKey)
	if callbackKey == "" {
		// Check for recovery-injected key (from recoverPendingWorkflows).
		if recoveredKey, ok := e.wCtx.Input["__cb_key_"+step.ID]; ok && recoveredKey != "" {
			callbackKey = recoveredKey
		} else {
			callbackKey = fmt.Sprintf("%s-%s-%s", e.run.ID, step.ID, newUUID()[:8])
		}
	}

	// For signature auth, include callback secret in outgoing headers.
	if authMode == "signature" {
		if url != "" && !strings.HasPrefix(url, "https://") {
			log.Warn("HMAC callback secret sent over non-HTTPS connection", "step", step.ID, "url", url)
		}
		cbSecret := callbackSignatureSecret(e.cfg.APIToken, callbackKey)
		if headers == nil {
			headers = make(map[string]string)
		}
		headers["X-Callback-Secret"] = cbSecret
	}

	// Build request body.
	contentType := step.ExternalContentType
	if contentType == "" {
		contentType = "application/json"
	}
	var bodyStr string
	if step.ExternalRawBody != "" {
		bodyStr = e.resolveTemplateWithFields(step.ExternalRawBody)
	} else if step.ExternalBody != nil {
		resolvedBody := e.resolveTemplateMapWithFields(step.ExternalBody)
		if contentType == "application/x-www-form-urlencoded" {
			vals := neturl.Values{}
			for k, v := range resolvedBody {
				vals.Set(k, v)
			}
			bodyStr = vals.Encode()
		} else {
			bodyBytes, _ := json.Marshal(resolvedBody)
			bodyStr = string(bodyBytes)
		}
	}

	// Callback mode and timeout.
	mode := step.CallbackMode
	if mode == "" {
		mode = "single"
	}
	timeout := 1 * time.Hour // default
	if step.CallbackTimeout != "" {
		if d, err := parseDurationWithDays(step.CallbackTimeout); err == nil {
			timeout = d
		}
	}

	// Check DB state for resume/retry.
	isResume := false
	existingRecord := queryPendingCallbackByKey(callbackMgr.DBPath(), callbackKey)
	if existingRecord != nil {
		switch existingRecord.Status {
		case "delivered":
			// Already completed — skip re-execution.
			result.Status = "success"
			output := existingRecord.ResultBody
			if output == "" {
				output = existingRecord.Body // fallback for legacy records
			}
			result.Output = output
			log.Info("external step already delivered, skipping", "step", step.ID, "key", callbackKey)
			return
		case "completed", "timeout":
			// Previous attempt finished — reset for retry.
			resetCallbackRecord(callbackMgr.DBPath(), callbackKey)
			log.Info("external step retrying (reset old record)", "step", step.ID, "key", callbackKey, "oldStatus", existingRecord.Status)
		default:
			// "waiting" — check if POST was already sent (resume).
			if existingRecord.PostSent {
				isResume = true
				log.Info("external step resuming (POST already sent)", "step", step.ID, "key", callbackKey)
			}
		}
	}

	// If this is a recovered key, update the DB record to reference the new run ID.
	if _, ok := e.wCtx.Input["__cb_key_"+step.ID]; ok {
		updateCallbackRunID(callbackMgr.DBPath(), callbackKey, e.run.ID)
	}

	// Register channel BEFORE POST to prevent race condition.
	ch := callbackMgr.Register(callbackKey, ctx, mode)
	if ch == nil {
		result.Status = "error"
		result.Error = fmt.Sprintf("failed to register callback channel (key collision or at capacity): %s", callbackKey)
		return
	}
	defer callbackMgr.Unregister(callbackKey)

	// Calculate timeout time.
	timeoutAt := time.Now().Add(timeout)

	// Write DB record.
	if !isResume {
		recordPendingCallback(callbackMgr.DBPath(), callbackKey, e.run.ID, step.ID,
			mode, authMode, url, bodyStr, timeoutAt.UTC().Format("2006-01-02 15:04:05"))
	}

	// Replay accumulated streaming callbacks on resume.
	if isResume && mode == "streaming" {
		accumulated := queryStreamingCallbacks(callbackMgr.DBPath(), callbackKey)
		if len(accumulated) > 0 {
			callbackMgr.ReplayAccumulated(callbackKey, accumulated)
			callbackMgr.SetSeq(callbackKey, len(accumulated))
			log.Info("replayed accumulated streaming callbacks", "step", step.ID, "key", callbackKey, "count", len(accumulated))
		}
	}

	// HTTP POST (skip if resuming).
	if !isResume && url != "" {
		markPostSent(callbackMgr.DBPath(), callbackKey)

		retryMax := step.RetryMax
		if retryMax <= 0 {
			retryMax = 2
		}
		resp, err := httpPostWithRetry(ctx, url, contentType, headers, bodyStr, retryMax)
		if err != nil {
			result.Status = "error"
			result.Error = fmt.Sprintf("external POST failed: %v", err)
			resetCallbackRecord(callbackMgr.DBPath(), callbackKey)
			return
		}
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
	} else if !isResume {
		// No URL — callback-only mode (e.g. manual approval).
		markPostSent(callbackMgr.DBPath(), callbackKey)
	}

	// Publish waiting event.
	e.publishEvent("step_waiting", map[string]any{
		"runId":       e.run.ID,
		"stepId":      step.ID,
		"callbackKey": callbackKey,
		"timeout":     timeout.String(),
	})

	log.Info("external step waiting for callback", "step", step.ID, "key", callbackKey, "timeout", timeout.String())

	// Wait for callback(s).
	if mode == "streaming" {
		lastResult, accumulated := waitStreamingCallback(ctx, ch, callbackKey, step, timeout)

		if lastResult == nil {
			handleCallbackTimeout(step, result, timeout, ctx)
			return
		}

		// Build output based on accumulate setting.
		var output string
		if step.CallbackAccumulate && len(accumulated) > 0 {
			var parts []string
			for _, a := range accumulated {
				mapped := applyResponseMapping(a.Body, step.CallbackResponseMap)
				if !json.Valid([]byte(mapped)) {
					b, _ := json.Marshal(mapped)
					mapped = string(b)
				}
				parts = append(parts, mapped)
			}
			output = "[" + strings.Join(parts, ",") + "]"
		} else {
			output = applyResponseMapping(lastResult.Body, step.CallbackResponseMap)
		}

		// Check if done or timed out.
		isDone := false
		if step.CallbackResponseMap != nil && step.CallbackResponseMap.DonePath != "" {
			doneVal := extractJSONPath(lastResult.Body, step.CallbackResponseMap.DonePath)
			isDone = doneVal == step.CallbackResponseMap.DoneValue
		}

		if isDone {
			result.Status = "success"
			result.Output = output
		} else if ctx.Err() != nil {
			result.Status = "cancelled"
			result.Error = "workflow cancelled while waiting for callback"
			result.Output = output
		} else {
			// Timeout with partial results.
			onTimeout := step.OnTimeout
			if onTimeout == "" {
				onTimeout = "stop"
			}
			if onTimeout == "skip" {
				result.Status = "skipped"
				result.Error = "streaming timeout (partial)"
				result.Output = output
			} else {
				result.Status = "timeout"
				result.Error = "streaming timeout (partial)"
				result.Output = output
			}
		}
		clearPendingCallback(callbackMgr.DBPath(), callbackKey)
		log.Info("external step completed (streaming)", "step", step.ID, "key", callbackKey, "callbacks", len(accumulated))
	} else {
		// Single mode.
		cbResult := waitSingleCallback(ctx, ch, callbackKey, step, timeout)
		if cbResult == nil {
			handleCallbackTimeout(step, result, timeout, ctx)
			return
		}

		markCallbackDelivered(callbackMgr.DBPath(), callbackKey, 0, *cbResult)

		output := cbResult.Body
		if step.CallbackResponseMap != nil {
			output = applyResponseMapping(output, step.CallbackResponseMap)
		}

		result.Status = "success"
		result.Output = output
		clearPendingCallback(callbackMgr.DBPath(), callbackKey)
		log.Info("external step completed", "step", step.ID, "key", callbackKey)
	}
}

// =============================================================================
// runHumanStep — root-only: accesses callbackMgr singleton for channel wait
// =============================================================================

// runHumanStep executes a human gate step: write DB, wait for human response via callback.
func (e *workflowExecutor) runHumanStep(ctx context.Context, step *WorkflowStep, result *StepRunResult) {
	if callbackMgr == nil {
		result.Status = "error"
		result.Error = "callback manager not initialized"
		return
	}

	// Resolve templates.
	prompt := e.resolveTemplateWithFields(step.HumanPrompt)
	assignee := e.resolveTemplateWithFields(step.HumanAssignee)

	subtype := step.HumanSubtype
	if subtype == "" {
		subtype = "approval"
	}

	// Generate key.
	hgKey := fmt.Sprintf("hg-%s-%s", e.run.ID, step.ID)

	// Check for recovery-injected key.
	if recoveredKey, ok := e.wCtx.Input["__hg_key_"+step.ID]; ok && recoveredKey != "" {
		hgKey = recoveredKey
	}

	// Parse timeout (default 72h).
	timeout := 72 * time.Hour
	if step.HumanTimeout != "" {
		if d, err := parseDurationWithDays(step.HumanTimeout); err == nil {
			timeout = d
		}
	}

	// Check DB state for resume/completed.
	existing := queryHumanGate(callbackMgr.DBPath(), hgKey)
	if existing != nil {
		switch existing.Status {
		case "completed":
			// Already completed — apply result directly.
			applyHumanGateResult(subtype, existing.Decision, existing.Response, step, result)
			log.Info("human gate already completed, skipping", "step", step.ID, "key", hgKey)
			return
		case "rejected":
			result.Status = "error"
			result.Error = fmt.Sprintf("human gate rejected: %s", existing.Response)
			log.Info("human gate already rejected, skipping", "step", step.ID, "key", hgKey)
			return
		case "cancelled":
			result.Status = "cancelled"
			result.Error = "human gate cancelled by operator"
			log.Info("human gate already cancelled, skipping", "step", step.ID, "key", hgKey)
			return
		case "timeout":
			// Previous timeout — reset for retry.
			resetHumanGate(callbackMgr.DBPath(), hgKey)
			log.Info("human gate retrying (reset old timeout)", "step", step.ID, "key", hgKey)
		case "waiting":
			// Resume — skip DB write, go straight to wait.
			// Recalculate remaining timeout from DB so recovery doesn't restart the full timer.
			if existing.TimeoutAt != "" {
				if t, err := time.Parse("2006-01-02 15:04:05", existing.TimeoutAt); err == nil {
					if remaining := time.Until(t); remaining > 0 {
						timeout = remaining
					} else {
						timeout = time.Millisecond // already expired, fire immediately
					}
				}
			}
			log.Info("human gate resuming (already waiting)", "step", step.ID, "key", hgKey, "remaining", timeout.String())
		}
	}

	// If this is a recovered key, update the DB record to reference the new run ID.
	if _, ok := e.wCtx.Input["__hg_key_"+step.ID]; ok {
		updateHumanGateRunID(callbackMgr.DBPath(), hgKey, e.run.ID)
	}

	// Register channel BEFORE DB write to prevent race.
	ch := callbackMgr.Register(hgKey, ctx, "single")
	if ch == nil {
		result.Status = "error"
		result.Error = fmt.Sprintf("failed to register human gate channel (key collision): %s", hgKey)
		return
	}
	defer callbackMgr.Unregister(hgKey)

	timeoutAt := time.Now().Add(timeout)

	// Marshal options and context for DB storage.
	optionsJSON := ""
	if len(step.HumanOptions) > 0 {
		if b, err := json.Marshal(step.HumanOptions); err == nil {
			optionsJSON = string(b)
		}
	}
	contextJSON := ""
	if len(step.HumanContext) > 0 {
		if b, err := json.Marshal(step.HumanContext); err == nil {
			contextJSON = string(b)
		}
	}

	// Write DB record (skip if already waiting from resume).
	if existing == nil || existing.Status != "waiting" {
		recordHumanGate(callbackMgr.DBPath(), hgKey, e.run.ID, step.ID, e.run.WorkflowName, subtype, prompt, assignee,
			timeoutAt.UTC().Format("2006-01-02 15:04:05"), optionsJSON, contextJSON)
	}

	// Update run status to "waiting" and checkpoint.
	e.mu.Lock()
	e.run.Status = "waiting"
	result.Status = "waiting_human"
	e.mu.Unlock()
	checkpointRun(e)

	// Publish waiting event.
	e.publishEvent("human_gate_waiting", map[string]any{
		"runId":    e.run.ID,
		"stepId":   step.ID,
		"hgKey":    hgKey,
		"subtype":  subtype,
		"prompt":   prompt,
		"assignee": assignee,
		"timeout":  timeout.String(),
	})

	log.Info("human gate waiting", "step", step.ID, "key", hgKey, "subtype", subtype, "assignee", assignee, "timeout", timeout.String())

	// Notify Discord that a gate is waiting (only on first activation, not on resume).
	if existing == nil || existing.Status != "waiting" {
		notifyDiscordHumanGateWaiting(e.cfg, subtype, prompt, assignee, e.run.WorkflowName, step.ID, hgKey, timeout.String())
	}

	// Wait for human response via callback channel.
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case cbResult, ok := <-ch:
		if !ok {
			result.Status = "error"
			result.Error = "human gate channel closed unexpectedly"
			return
		}

		// Parse the callback body as JSON to extract action/response/respondedBy.
		// Accepts both "action" (canonical) and "decision" (legacy) field names.
		var body struct {
			Action      string `json:"action"`
			Decision    string `json:"decision"`
			Response    string `json:"response"`
			RespondedBy string `json:"respondedBy"`
		}
		if err := json.Unmarshal([]byte(cbResult.Body), &body); err != nil {
			// Treat raw body as response text.
			body.Response = cbResult.Body
			if subtype == "approval" {
				body.Action = cbResult.Body // e.g. "approve" or "reject"
			}
		}
		// Normalize: prefer "action", fall back to legacy "decision".
		if body.Action == "" && body.Decision != "" {
			body.Action = body.Decision
		}

		// Cancel — stop the step without completing.
		if body.Action == "cancelled" {
			cancelHumanGate(callbackMgr.DBPath(), hgKey)
			result.Status = "cancelled"
			result.Error = "human gate cancelled by operator"

			e.publishEvent("human_gate_responded", map[string]any{
				"runId":       e.run.ID,
				"stepId":      step.ID,
				"hgKey":       hgKey,
				"action":      "cancelled",
				"cancelledBy": body.RespondedBy,
			})

			e.mu.Lock()
			e.run.Status = "running"
			e.mu.Unlock()
			checkpointRun(e)

			log.Info("human gate cancelled", "step", step.ID, "key", hgKey, "cancelledBy", body.RespondedBy)
			return
		}

		// Record completion in DB — use rejectHumanGate for approval rejections so
		// DB status is "rejected" and the resume path (existing.Status == "rejected") matches.
		if subtype == "approval" && (body.Action == "rejected" || body.Action == "reject") {
			rejectHumanGate(callbackMgr.DBPath(), hgKey, body.Response, body.RespondedBy)
		} else {
			completeHumanGate(callbackMgr.DBPath(), hgKey, body.Action, body.Response, body.RespondedBy)
		}

		// Apply result based on subtype.
		applyHumanGateResult(subtype, body.Action, body.Response, step, result)

		// Publish responded event.
		e.publishEvent("human_gate_responded", map[string]any{
			"runId":       e.run.ID,
			"stepId":      step.ID,
			"hgKey":       hgKey,
			"action":      body.Action,
			"respondedBy": body.RespondedBy,
		})

		// Restore run status.
		e.mu.Lock()
		e.run.Status = "running"
		e.mu.Unlock()
		checkpointRun(e)

		log.Info("human gate completed", "step", step.ID, "key", hgKey, "action", body.Action)

	case <-timer.C:
		// Timeout.
		timeoutHumanGate(callbackMgr.DBPath(), hgKey)
		applyHumanGateTimeout(step, result, timeout)

		// Publish responded event (timeout).
		e.publishEvent("human_gate_responded", map[string]any{
			"runId":  e.run.ID,
			"stepId": step.ID,
			"hgKey":  hgKey,
			"action": "timeout",
		})

		// Restore run status.
		e.mu.Lock()
		e.run.Status = "running"
		e.mu.Unlock()
		checkpointRun(e)

		notifyDiscordHumanGateTimeout(e.cfg, subtype, e.run.WorkflowName, step.ID, step.HumanOnTimeout, timeout.String())
		log.Warn("human gate timeout", "step", step.ID, "key", hgKey, "timeout", timeout.String())

		// Discord notification already sent by notifyDiscordHumanGateTimeout above.

	case <-ctx.Done():
		// Mark gate as cancelled in DB so recovery skips it on restart.
		cancelHumanGate(callbackMgr.DBPath(), hgKey)
		result.Status = "cancelled"
		result.Error = "workflow cancelled while waiting for human response"
		e.mu.Lock()
		e.run.Status = "running"
		e.mu.Unlock()
		checkpointRun(e)
	}
}

// applyHumanGateResult maps human gate decision/response to step result.
func applyHumanGateResult(subtype, decision, response string, step *WorkflowStep, result *StepRunResult) {
	switch subtype {
	case "approval":
		if decision == "approved" || decision == "approve" {
			result.Status = "success"
			result.Output = fmt.Sprintf("approved: %s", response)
		} else {
			result.Status = "error"
			result.Error = fmt.Sprintf("human gate rejected: %s", response)
		}
	case "action":
		result.Status = "success"
		result.Output = response
		if result.Output == "" {
			result.Output = "action completed"
		}
	case "input":
		result.Status = "success"
		if step.HumanInputKey != "" {
			m := map[string]string{step.HumanInputKey: response}
			if b, err := json.Marshal(m); err == nil {
				result.Output = string(b)
			} else {
				result.Output = response
			}
		} else {
			result.Output = response
		}
	default:
		result.Status = "success"
		result.Output = response
	}
}

// applyHumanGateTimeout applies the timeout policy for a human gate step.
func applyHumanGateTimeout(step *WorkflowStep, result *StepRunResult, timeout time.Duration) {
	onTimeout := step.HumanOnTimeout
	if onTimeout == "" {
		onTimeout = "stop"
	}
	switch onTimeout {
	case "skip":
		result.Status = "skipped"
		result.Error = fmt.Sprintf("human gate timeout after %s (skipped)", timeout)
	case "approve":
		result.Status = "success"
		result.Output = fmt.Sprintf("auto-approved after timeout (%s)", timeout)
	default: // "stop"
		result.Status = "timeout"
		result.Error = fmt.Sprintf("human gate timeout after %s", timeout)
	}
}

// =============================================================================
// Human Gate — Discord notifications
// =============================================================================

// resolveHumanAssigneeChannel returns the Discord channel ID to notify for a given assignee.
// It first checks assigneeMap for an explicit mapping; if not found, returns fallback.
func resolveHumanAssigneeChannel(assigneeMap map[string]string, assignee, fallback string) string {
	if assignee != "" && len(assigneeMap) > 0 {
		if ch, ok := assigneeMap[assignee]; ok && ch != "" {
			return ch
		}
	}
	return fallback
}

// humanGateDashboardURL builds the URL to the human-gates dashboard panel.
// Uses cfg.Discord.DashboardBaseURL if set; otherwise falls back to http://<listenAddr>.
func humanGateDashboardURL(cfg *Config) string {
	base := cfg.Discord.DashboardBaseURL
	if base == "" {
		base = "http://" + cfg.ListenAddr
	}
	return strings.TrimRight(base, "/") + "/dashboard#human-gates-panel"
}

// notifyDiscordHumanGateWaiting sends a Discord embed when a human gate starts waiting.
func notifyDiscordHumanGateWaiting(cfg *Config, subtype, prompt, assignee, workflowName, stepID, hgKey, timeoutStr string) {
	bot, ok := cfg.Runtime.DiscordBot.(*DiscordBot)
	if !ok || bot == nil {
		return
	}
	ch := resolveHumanAssigneeChannel(cfg.Discord.HumanAssigneeMap, assignee, bot.notifyChannelID())
	if ch == "" {
		return
	}

	title := "⏳ Human Gate Waiting"
	switch subtype {
	case "approval":
		title = "⏳ Approval Required"
	case "input":
		title = "⏳ Human Input Required"
	case "action":
		title = "⏳ Human Action Required"
	}

	promptSnippet := prompt
	if runes := []rune(promptSnippet); len(runes) > 300 {
		promptSnippet = string(runes[:297]) + "..."
	}
	if promptSnippet == "" {
		promptSnippet = "(no prompt)"
	}

	dashboardURL := humanGateDashboardURL(cfg)
	embed := discord.Embed{
		Title:       title,
		URL:         dashboardURL,
		Description: promptSnippet,
		Color:       0xFAA61A, // yellow-orange
		Fields: []discord.EmbedField{
			{Name: "Workflow", Value: workflowName, Inline: true},
			{Name: "Step", Value: stepID, Inline: true},
			{Name: "Type", Value: subtype, Inline: true},
		},
	}
	if assignee != "" {
		embed.Fields = append(embed.Fields, discord.EmbedField{Name: "Assignee", Value: assignee, Inline: true})
	}
	if timeoutStr != "" {
		embed.Fields = append(embed.Fields, discord.EmbedField{Name: "Timeout", Value: timeoutStr, Inline: true})
	}
	if hgKey != "" {
		embed.Fields = append(embed.Fields, discord.EmbedField{Name: "Gate Key", Value: "`" + hgKey + "`", Inline: false})
	}
	embed.Fields = append(embed.Fields, discord.EmbedField{Name: "Dashboard", Value: dashboardURL, Inline: false})

	bot.sendEmbed(ch, embed)
}

// notifyDiscordHumanGateTimeout sends a Discord embed when a human gate times out.
func notifyDiscordHumanGateTimeout(cfg *Config, subtype, workflowName, stepID, onTimeout, timeoutStr string) {
	bot, ok := cfg.Runtime.DiscordBot.(*DiscordBot)
	if !ok || bot == nil {
		return
	}
	ch := bot.notifyChannelID()
	if ch == "" {
		return
	}

	behavior := onTimeout
	if behavior == "" {
		behavior = "stop"
	}

	embed := discord.Embed{
		Title:       "⏰ Human Gate Timeout",
		Description: fmt.Sprintf("Gate timed out after %s → **%s**", timeoutStr, behavior),
		Color:       0xED4245, // red
		Fields: []discord.EmbedField{
			{Name: "Workflow", Value: workflowName, Inline: true},
			{Name: "Step", Value: stepID, Inline: true},
			{Name: "Type", Value: subtype, Inline: true},
			{Name: "On Timeout", Value: behavior, Inline: true},
		},
	}

	bot.sendEmbed(ch, embed)
}

// =============================================================================
// Recovery helpers — root-only (use dispatchState + executeWorkflow)
// =============================================================================

// recoverPendingWorkflows scans for workflows with pending external/human steps and resumes them.
func recoverPendingWorkflows(cfg *Config, state *dispatchState, sem, childSem chan struct{}) {
	if cfg.HistoryDB == "" || callbackMgr == nil {
		return
	}

	// Collect all unique run IDs with waiting callbacks OR waiting human gates.
	pendingRunIDs := make(map[string]bool)

	cbRows, err := db.Query(cfg.HistoryDB, `SELECT DISTINCT run_id FROM workflow_callbacks WHERE status='waiting'`)
	if err == nil {
		for _, row := range cbRows {
			pendingRunIDs[fmt.Sprintf("%v", row["run_id"])] = true
		}
	}

	hgRows, err := db.Query(cfg.HistoryDB, `SELECT DISTINCT run_id FROM workflow_human_gates WHERE status='waiting'`)
	if err == nil {
		for _, row := range hgRows {
			pendingRunIDs[fmt.Sprintf("%v", row["run_id"])] = true
		}
	}

	if len(pendingRunIDs) == 0 {
		return
	}

	for runID := range pendingRunIDs {
		// Load the workflow run.
		run, err := queryWorkflowRunByID(cfg.HistoryDB, runID)
		if err != nil || run == nil {
			log.Warn("recovery: cannot load workflow run", "runID", runID, "error", err)
			continue
		}

		// Load the workflow definition.
		wf, err := loadWorkflowByName(cfg, run.WorkflowName)
		if err != nil {
			log.Warn("recovery: cannot load workflow", "workflow", run.WorkflowName, "error", err)
			continue
		}

		log.Info("recovering pending workflow", "workflow", run.WorkflowName, "runID", runID[:8])

		// Collect pending callback keys for this run so the new execution can reuse them.
		pendingCallbacks := queryPendingCallbacksByRun(cfg.HistoryDB, runID)
		recoveryVars := make(map[string]string)
		for k, v := range run.Variables {
			recoveryVars[k] = v
		}
		for _, cb := range pendingCallbacks {
			recoveryVars["__cb_key_"+cb.StepID] = cb.Key
		}

		// Collect pending human gate keys for this run.
		pendingHumanGates := queryPendingHumanGatesByRun(cfg.HistoryDB, runID)
		for _, hg := range pendingHumanGates {
			recoveryVars["__hg_key_"+hg.StepID] = hg.Key
		}

		// Mark old run as superseded so it's not left orphaned.
		markRunSuperseded := func(oldRunID string) {
			sql := fmt.Sprintf(
				`UPDATE workflow_runs SET status='recovered', finished_at=datetime('now') WHERE id='%s' AND status IN ('running','waiting')`,
				db.Escape(oldRunID),
			)
			db.Query(cfg.HistoryDB, sql)
		}
		markRunSuperseded(runID)

		// Re-execute the workflow in background.
		go executeWorkflow(context.Background(), cfg, wf, recoveryVars, state, sem, childSem)
	}
}

// checkpointRun saves current workflow run state to DB.
func checkpointRun(e *workflowExecutor) {
	e.mu.Lock()
	defer e.mu.Unlock()
	recordWorkflowRun(e.cfg.HistoryDB, e.run)
}

// hasWaitingExternalStep checks if any step result indicates a waiting external or human step.
func hasWaitingExternalStep(results map[string]*StepRunResult) bool {
	for _, r := range results {
		if r.Status == "waiting" || r.Status == "waiting_human" {
			return true
		}
	}
	return false
}

// validateTriggerConfig wraps the internal version (which also validates cron expressions).
func validateTriggerConfig(t WorkflowTriggerConfig, existingNames map[string]bool) []string {
	return iwf.ValidateTriggerConfig(t, existingNames)
}

// =============================================================================
// Merged from workflow.go
// =============================================================================

// ConfigVersion is an alias for version.Version so that existing callers in
// the root package continue to compile without modification.
type ConfigVersion = version.Version

//go:embed examples/templates/*.json
var templateFS embed.FS

// --- Type Aliases ---
// These aliases let root-package code continue to use the unqualified names
// while the canonical definitions live in internal/workflow.

type Workflow = iwf.Workflow
type WorkflowStep = iwf.WorkflowStep
type ResponseMapping = iwf.ResponseMapping
type WorkflowContext = iwf.WorkflowContext
type WorkflowStepResult = iwf.WorkflowStepResult
type TemplateSummary = iwf.TemplateSummary

// --- Directory helpers ---

func workflowDir(cfg *Config) string      { return iwf.WorkflowDir(cfg) }
func ensureWorkflowDir(cfg *Config) error { return iwf.EnsureWorkflowDir(cfg) }

// --- Load / Save ---

func loadWorkflow(path string) (*Workflow, error) { return iwf.LoadWorkflow(path) }

func loadWorkflowByName(cfg *Config, name string) (*Workflow, error) {
	return iwf.LoadWorkflowByName(cfg, name)
}

func saveWorkflow(cfg *Config, w *Workflow) error { return iwf.SaveWorkflow(cfg, w) }

func deleteWorkflow(cfg *Config, name string) error { return iwf.DeleteWorkflow(cfg, name) }

func listWorkflows(cfg *Config) ([]*Workflow, error) { return iwf.ListWorkflows(cfg) }

// --- Validation ---

func validateWorkflow(w *Workflow) []string { return iwf.ValidateWorkflow(w) }
func validateStep(s WorkflowStep, allIDs map[string]bool) []string {
	return iwf.ValidateStep(s, allIDs)
}

// --- DAG helpers ---

func detectCycle(steps []WorkflowStep) string       { return iwf.DetectCycle(steps) }
func topologicalSort(steps []WorkflowStep) []string { return iwf.TopologicalSort(steps) }

// --- Variable Template System ---

// templateVarRe matches {{...}} template expressions — re-exported from internal/workflow.
var templateVarRe = iwf.TemplateVarRe

func newWorkflowContext(w *Workflow, inputOverrides map[string]string) *WorkflowContext {
	return iwf.NewWorkflowContext(w, inputOverrides)
}

func resolveTemplate(tmpl string, wCtx *WorkflowContext) string {
	return iwf.ResolveTemplate(tmpl, wCtx)
}

func resolveExpr(expr string, wCtx *WorkflowContext) string {
	return iwf.ResolveExpr(expr, wCtx)
}

// --- Condition Evaluation ---

func evalCondition(expr string, wCtx *WorkflowContext) bool {
	return iwf.EvalCondition(expr, wCtx)
}

// --- Utility ---

func getStepByID(w *Workflow, id string) *WorkflowStep { return iwf.GetStepByID(w, id) }
func stepType(s *WorkflowStep) string                  { return iwf.StepType(s) }

// --- Template Gallery ---

// cachedTemplates holds the pre-computed template summaries (static from embed.FS).
var cachedTemplates []TemplateSummary

// listTemplates returns summaries of all embedded workflow templates (cached after first call).
func listTemplates() []TemplateSummary {
	if cachedTemplates != nil {
		return cachedTemplates
	}
	entries, err := templateFS.ReadDir("examples/templates")
	if err != nil {
		return nil
	}
	var templates []TemplateSummary
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := templateFS.ReadFile("examples/templates/" + e.Name())
		if err != nil {
			continue
		}
		var wf Workflow
		if err := json.Unmarshal(data, &wf); err != nil {
			continue
		}
		var varNames []string
		for k := range wf.Variables {
			varNames = append(varNames, k)
		}
		category := ""
		name := strings.TrimPrefix(wf.Name, "tpl-")
		if idx := strings.Index(name, "-"); idx > 0 {
			category = name[:idx]
		}
		templates = append(templates, TemplateSummary{
			Name:        wf.Name,
			Description: wf.Description,
			StepCount:   len(wf.Steps),
			Variables:   varNames,
			Category:    category,
		})
	}
	cachedTemplates = templates
	return templates
}

// loadTemplate loads a full workflow template by name.
func loadTemplate(name string) (*Workflow, error) {
	// Sanitize to prevent path traversal.
	if strings.Contains(name, "/") || strings.Contains(name, "\\") || strings.Contains(name, "..") {
		return nil, fmt.Errorf("invalid template name")
	}
	fileName := name + ".json"
	if !strings.HasPrefix(name, "tpl-") {
		fileName = "tpl-" + fileName
	}
	data, err := templateFS.ReadFile("examples/templates/" + fileName)
	if err != nil {
		return nil, fmt.Errorf("template %q not found", name)
	}
	var wf Workflow
	if err := json.Unmarshal(data, &wf); err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}
	return &wf, nil
}

// installTemplate copies a template to the user's workflows directory with an optional new name.
func installTemplate(cfg *Config, templateName, newName string) error {
	wf, err := loadTemplate(templateName)
	if err != nil {
		return err
	}
	if newName != "" {
		wf.Name = newName
	}
	return saveWorkflow(cfg, wf)
}

// buildStepTask converts a workflow dispatch step into a Task for execution.
func buildStepTask(s *WorkflowStep, wCtx *WorkflowContext, workflowName string) Task {
	return Task{
		ID:             newUUID(),
		Name:           fmt.Sprintf("%s/%s", workflowName, s.ID),
		Prompt:         resolveTemplate(s.Prompt, wCtx),
		Agent:          resolveTemplate(s.Agent, wCtx),
		Model:          resolveTemplate(s.Model, wCtx),
		Provider:       resolveTemplate(s.Provider, wCtx),
		Timeout:        resolveTemplate(s.Timeout, wCtx),
		Budget:         s.Budget,
		PermissionMode: resolveTemplate(s.PermissionMode, wCtx),
		AllowDangerous: s.AllowDangerous,
		Source:         "workflow:" + workflowName,
	}
}

// restoreWorkflowVersion restores a workflow to a saved version.
func restoreWorkflowVersion(dbPath string, cfg *Config, versionID string) error {
	return iwf.RestoreWorkflowVersion(dbPath, cfg, versionID)
}
