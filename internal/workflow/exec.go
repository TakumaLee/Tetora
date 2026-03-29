package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	dtypes "tetora/internal/dispatch"
	"tetora/internal/config"
	"tetora/internal/log"
)

// --- Deps: root-package callbacks injected at construction time ---

// WorktreeOps abstracts git worktree lifecycle from the root package.
type WorktreeOps struct {
	// Create creates an isolated worktree for the run. Returns (worktreeDir, error).
	// A nil Create disables worktree isolation.
	Create func(repoDir, runID, branch string) (wtDir string, err error)
	// MergeBranchOnly merges the worktree branch and returns a diff summary.
	MergeBranchOnly func(repoDir, wtDir string) (diffSummary string, err error)
	// Remove cleans up a worktree after a run.
	Remove func(repoDir, wtDir string)
}

// ToolCallFunc handles a tool_call step: resolves the tool and invokes it.
// toolInput is already template-expanded. Returns (output, error).
type ToolCallFunc func(ctx context.Context, toolName string, toolInput map[string]string) (string, error)

// SkillFunc executes a named skill with the given vars.
// Returns (output, status, errorString). status is "success", "error", or "timeout".
type SkillFunc func(ctx context.Context, skillName string, vars map[string]string) (output, status, errStr string)

// CostEstimateResult is a minimal cost estimate used for dry-run output.
type CostEstimateResult struct {
	Model            string
	EstimatedCostUSD float64
}

// HandoffParams carries parameters for creating an agent handoff record.
type HandoffParams struct {
	ID            string
	WorkflowRunID string
	FromAgent     string
	ToAgent       string
	FromStepID    string
	ToStepID      string
	FromSessionID string
	ToSessionID   string
	Context       string
	Instruction   string
	Status        string
	CreatedAt     string
}

// AgentMessageParams carries parameters for recording an agent message.
type AgentMessageParams struct {
	WorkflowRunID string
	FromAgent     string
	ToAgent       string
	Type          string
	Content       string
	RefID         string
	CreatedAt     string
}

// AutoDelegation is a parsed delegation marker from agent output.
type AutoDelegation struct {
	Agent  string
	Task   string
	Reason string
}

// SessionParams holds fields for creating a workflow step session.
type SessionParams struct {
	ID        string
	Agent     string
	Source    string
	Status    string
	Title     string
	CreatedAt string
	UpdatedAt string
}

// TaskParams mirrors the dispatch.Task fields that the executor needs to build.
type TaskParams struct {
	ID             string
	Name           string
	Prompt         string
	Agent          string
	Model          string
	Provider       string
	Timeout        string
	Budget         float64
	PermissionMode string
	Source         string
	SessionID      string
	Workdir        string
}

// TaskResultSummary carries the fields the executor needs from a completed task.
type TaskResultSummary struct {
	Output    string
	CostUSD   float64
	SessionID string
	Status    string // "success", "error", "timeout", "cancelled"
	Error     string
}

// Deps holds all root-package callbacks injected into the Executor.
// The internal/workflow package must not import the root package; all integration
// points are provided here at construction time.
type Deps struct {
	// Cfg is used for loading workflows by name (e.g. on resume).
	Cfg *config.Config

	// NewUUID generates a new unique ID.
	NewUUID func() string

	// RunTask executes a task via the provider, records history, and returns the result.
	RunTask func(ctx context.Context, params TaskParams, sem, childSem chan struct{}) TaskResultSummary

	// RunTaskNoRecord executes a task without recording history (shadow mode).
	RunTaskNoRecord func(ctx context.Context, params TaskParams, sem, childSem chan struct{}) TaskResultSummary

	// EstimateTaskCost estimates the cost of a task without executing it.
	EstimateTaskCost func(params TaskParams) CostEstimateResult

	// BuildStepTask builds a TaskParams from a workflow step and context.
	BuildStepTask func(step *WorkflowStep, wCtx *WorkflowContext, workflowName string) TaskParams

	// FillDefaults applies agent/model defaults to a TaskParams.
	FillDefaults func(params *TaskParams)

	// CreateSession creates a new session record.
	CreateSession func(params SessionParams) error

	// RecordSessionActivity records session activity after a task completes.
	RecordSessionActivity func(params TaskParams, result TaskResultSummary, agentName string)

	// RecordHandoff persists a handoff record.
	RecordHandoff func(params HandoffParams) error

	// UpdateHandoffStatus updates the status of a handoff record.
	UpdateHandoffStatus func(id, status string)

	// SendAgentMessage records an inter-agent communication message.
	SendAgentMessage func(params AgentMessageParams) error

	// BuildHandoffPrompt builds the full prompt for a handoff step.
	BuildHandoffPrompt func(contextOutput, instruction string) string

	// ParseAutoDelegate extracts delegation markers from agent output.
	ParseAutoDelegate func(output string) []AutoDelegation

	// ProcessAutoDelegations handles auto-delegations found in a step's output.
	// Returns the updated output after processing.
	ProcessAutoDelegations func(ctx context.Context, delegations []AutoDelegation,
		output, runID, agentName, stepID string,
		broker dtypes.SSEBrokerPublisher, sem, childSem chan struct{}) string

	// TruncateStr truncates a string to a max byte length.
	TruncateStr func(s string, maxLen int) string

	// Truncate truncates a string (for short display, e.g. log messages).
	Truncate func(s string, maxLen int) string

	// ExecuteSkill runs a named skill step.
	ExecuteSkill SkillFunc

	// ExecuteToolCall handles tool_call steps.
	ExecuteToolCall ToolCallFunc

	// RegisterRunCanceller registers a context.CancelFunc for the workflow run ID.
	RegisterRunCanceller func(runID string, cancel context.CancelFunc)

	// UnregisterRunCanceller removes a canceller when the run completes.
	UnregisterRunCanceller func(runID string)

	// Checkpoint saves the current run state to DB immediately.
	// Called after step start and step completion. If nil, checkpointing is skipped.
	Checkpoint func(run *WorkflowRun)

	// Worktree provides git worktree isolation operations.
	// If Worktree is nil or Worktree.Create is nil, worktree isolation is disabled.
	Worktree *WorktreeOps

	// IsGitRepo reports whether a directory is a git repository.
	IsGitRepo func(dir string) bool

	// SlugifyBranch converts a workflow name to a valid git branch name segment.
	SlugifyBranch func(s string) string

	// RunExternalStep handles external HTTP callback steps.
	// If nil, external steps return an error.
	RunExternalStep func(ctx context.Context, extCtx ExternalStepContext, step *WorkflowStep, result *StepRunResult)

	// RunHumanStep handles human gate steps (approval/action/input).
	// If nil, human steps return an error.
	RunHumanStep func(ctx context.Context, hCtx HumanStepContext, step *WorkflowStep, result *StepRunResult)

	// UpdateTaskWorkflowRunID updates a task's workflowRunId field (used on workflow resume).
	UpdateTaskWorkflowRunID func(taskID, runID string)
}

// ExternalStepContext provides executor state to the external step handler.
type ExternalStepContext struct {
	Run  *WorkflowRun
	WCtx *WorkflowContext
	// ResolveTemplate resolves {{...}} templates with extended field access.
	ResolveTemplate    func(tmpl string) string
	ResolveTemplateMap func(m map[string]string) map[string]string
	PublishEvent       func(eventType string, data map[string]any)
}

// HumanStepContext provides executor state to the human gate step handler.
type HumanStepContext struct {
	Run             *WorkflowRun
	WCtx            *WorkflowContext
	ResolveTemplate func(tmpl string) string
	PublishEvent    func(eventType string, data map[string]any)
	Checkpoint      func()
}

// --- Executor ---

// Executor holds the state for one workflow execution.
type Executor struct {
	deps     *Deps
	workflow *Workflow
	run      *WorkflowRun
	wCtx     *WorkflowContext
	sem      chan struct{}
	childSem chan struct{}
	broker   dtypes.SSEBrokerPublisher
	mode     WorkflowRunMode
	mu       sync.Mutex

	// Git worktree isolation (populated when workflow.GitWorktree is true).
	worktreeDir string
	repoDir     string

	// resumeState is non-nil when resuming a previous run.
	resumeState map[string]*StepRunResult
}

// Execute runs a full workflow and returns the completed run.
// An optional mode parameter controls execution behavior (default: WorkflowModeLive).
func Execute(ctx context.Context, deps *Deps, w *Workflow, vars map[string]string,
	broker dtypes.SSEBrokerPublisher, sem, childSem chan struct{}, mode ...WorkflowRunMode) *WorkflowRun {

	runMode := WorkflowModeLive
	if len(mode) > 0 && mode[0] != "" {
		runMode = mode[0]
	}

	runID := deps.NewUUID()
	now := time.Now()

	run := &WorkflowRun{
		ID:           runID,
		WorkflowName: w.Name,
		Status:       "running",
		StartedAt:    now.Format(time.RFC3339),
		Variables:    vars,
		StepResults:  make(map[string]*StepRunResult),
	}

	for _, s := range w.Steps {
		run.StepResults[s.ID] = &StepRunResult{StepID: s.ID, Status: "pending"}
	}

	wCtx := NewWorkflowContext(w, vars)

	exec := &Executor{
		deps:     deps,
		workflow: w,
		run:      run,
		wCtx:     wCtx,
		sem:      sem,
		childSem: childSem,
		broker:   broker,
		mode:     runMode,
	}

	execCtx, execCancel := exec.setupExecContext(ctx)
	defer execCancel()

	exec.publishEvent("workflow_started", map[string]any{
		"runId":    runID,
		"workflow": w.Name,
		"steps":    len(w.Steps),
		"stepDefs": BuildStepSummaries(w.Steps),
	})

	if runMode == WorkflowModeLive {
		exec.setupWorktree()
	}

	RecordWorkflowRun(deps.Cfg.HistoryDB, run)

	dagErr := exec.executeDAG(execCtx)
	exec.finalizeRun(dagErr, now, ctx, execCtx)

	switch runMode {
	case WorkflowModeDryRun:
		run.Status = "dry-run:" + run.Status
	case WorkflowModeShadow:
		run.Status = "shadow:" + run.Status
	}

	RecordWorkflowRun(deps.Cfg.HistoryDB, run)

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

// Resume creates a new run that skips already-completed steps from a previous run.
func Resume(ctx context.Context, deps *Deps, originalRunID string,
	broker dtypes.SSEBrokerPublisher, sem, childSem chan struct{}) (*WorkflowRun, error) {

	originalRun, err := QueryWorkflowRunByID(deps.Cfg.HistoryDB, originalRunID)
	if err != nil {
		return nil, fmt.Errorf("original run not found: %w", err)
	}

	if !IsResumableStatus(originalRun.Status) {
		return nil, fmt.Errorf("run %s has status %q which is not resumable (must be error/cancelled/timeout)",
			originalRunID[:8], originalRun.Status)
	}

	w, err := LoadWorkflowByName(deps.Cfg, originalRun.WorkflowName)
	if err != nil {
		return nil, fmt.Errorf("workflow %q not found: %w", originalRun.WorkflowName, err)
	}

	runID := deps.NewUUID()
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

	resumeState := make(map[string]*StepRunResult)
	skippedCount, pendingCount := 0, 0

	for _, s := range w.Steps {
		if prevSR, ok := originalRun.StepResults[s.ID]; ok && (prevSR.Status == "success" || prevSR.Status == "skipped") {
			copied := *prevSR
			resumeState[s.ID] = &copied
			run.StepResults[s.ID] = &copied
			skippedCount++
		} else {
			run.StepResults[s.ID] = &StepRunResult{StepID: s.ID, Status: "pending"}
			pendingCount++
		}
	}

	// Log orphaned steps removed from current definition.
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
	wCtx := NewWorkflowContext(w, originalRun.Variables)
	for id, sr := range resumeState {
		if sr.Status == "success" {
			wCtx.Steps[id] = &WorkflowStepResult{Output: sr.Output, Status: sr.Status, Error: sr.Error}
		}
	}

	exec := &Executor{
		deps:        deps,
		workflow:    w,
		run:         run,
		wCtx:        wCtx,
		sem:         sem,
		childSem:    childSem,
		broker:      broker,
		mode:        WorkflowModeLive,
		resumeState: resumeState,
	}

	// Mark original run as "resumed" in DB.
	markRunResumed(deps.Cfg.HistoryDB, originalRunID, runID)


	log.Info("workflow resumed", "workflow", w.Name, "originalRunID", originalRunID[:8],
		"newRunID", runID[:8], "skippedSteps", skippedCount, "pendingSteps", pendingCount)

	execCtx, execCancel := exec.setupExecContext(ctx)
	defer execCancel()

	exec.publishEvent("workflow_resumed", map[string]any{
		"runId":        runID,
		"workflow":     w.Name,
		"resumedFrom":  originalRunID,
		"skippedSteps": skippedCount,
		"pendingSteps": pendingCount,
	})

	exec.setupWorktree()

	RecordWorkflowRun(deps.Cfg.HistoryDB, run)

	// Update task's workflowRunId to point to the new run immediately.
	if taskID := originalRun.Variables["_taskId"]; taskID != "" && deps.UpdateTaskWorkflowRunID != nil {
		deps.UpdateTaskWorkflowRunID(taskID, runID)
	}

	dagErr := exec.executeDAG(execCtx)
	exec.finalizeRun(dagErr, now, ctx, execCtx)

	RecordWorkflowRun(deps.Cfg.HistoryDB, run)

	if run.Status == "success" {
		log.Info("workflow resume completed", "workflow", w.Name, "runID", runID[:8],
			"status", run.Status, "durationMs", run.DurationMs, "cost", run.TotalCost)
	} else {
		log.Warn("workflow resume completed with error", "workflow", w.Name, "runID", runID[:8],
			"status", run.Status, "durationMs", run.DurationMs, "cost", run.TotalCost)
	}

	return run, nil
}

// IsResumableStatus reports whether a workflow run status can be resumed.
func IsResumableStatus(status string) bool {
	switch status {
	case "error", "cancelled", "timeout":
		return true
	}
	return false
}

// --- Setup helpers ---

func (e *Executor) setupExecContext(ctx context.Context) (context.Context, context.CancelFunc) {
	execCtx := ctx
	var timeoutCancel context.CancelFunc
	if e.workflow.Timeout != "" {
		if d, err := time.ParseDuration(e.workflow.Timeout); err == nil {
			execCtx, timeoutCancel = context.WithTimeout(ctx, d)
		}
	}
	execCtx, cancelRun := context.WithCancel(execCtx)
	if e.deps.RegisterRunCanceller != nil {
		e.deps.RegisterRunCanceller(e.run.ID, cancelRun)
	}

	return execCtx, func() {
		cancelRun()
		if e.deps.UnregisterRunCanceller != nil {
			e.deps.UnregisterRunCanceller(e.run.ID)
		}
		if timeoutCancel != nil {
			timeoutCancel()
		}
	}
}

func (e *Executor) setupWorktree() {
	w := e.workflow
	if !w.GitWorktree {
		return
	}
	if e.deps.Worktree == nil || e.deps.Worktree.Create == nil {
		return
	}
	repoDir := w.Workdir
	if repoDir == "" {
		repoDir = e.deps.Cfg.DefaultWorkdir
	}
	if repoDir == "" {
		return
	}
	if e.deps.IsGitRepo != nil && !e.deps.IsGitRepo(repoDir) {
		return
	}
	branch := w.Branch
	if branch == "" {
		branch = "wf/" + e.deps.SlugifyBranch(w.Name)
	}
	branch = ResolveTemplate(branch, e.wCtx)

	wtDir, wtErr := e.deps.Worktree.Create(repoDir, e.run.ID, branch)
	if wtErr != nil {
		log.Warn("workflow worktree: creation failed, continuing without isolation",
			"workflow", w.Name, "error", wtErr)
		return
	}
	e.worktreeDir = wtDir
	e.repoDir = repoDir
	log.Info("workflow worktree: created",
		"workflow", w.Name, "runID", e.run.ID[:8], "path", wtDir, "branch", branch)
}

func (e *Executor) finalizeRun(dagErr error, startTime time.Time, outerCtx, execCtx context.Context) {
	run := e.run
	run.FinishedAt = time.Now().Format(time.RFC3339)
	run.DurationMs = time.Since(startTime).Milliseconds()

	for _, sr := range run.StepResults {
		run.TotalCost += sr.CostUSD
	}

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
		var failedSteps []string
		for id, sr := range run.StepResults {
			if sr.Status == "error" || sr.Status == "timeout" {
				failedSteps = append(failedSteps, fmt.Sprintf("%s(%s)", id, sr.Status))
			}
		}
		if len(failedSteps) > 0 {
			run.Status = "error"
			run.Error = fmt.Sprintf("step failures: %s", strings.Join(failedSteps, ", "))
		} else {
			run.Status = "success"
		}
	}

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
	if e.worktreeDir != "" && e.deps.Worktree != nil && e.deps.Worktree.MergeBranchOnly != nil {
		if run.Status == "success" {
			diffSummary, mergeErr := e.deps.Worktree.MergeBranchOnly(e.repoDir, e.worktreeDir)
			if mergeErr != nil {
				log.Warn("workflow worktree: merge failed, keeping for inspection",
					"workflow", e.workflow.Name, "path", e.worktreeDir, "error", mergeErr)
			} else {
				if diffSummary != "" {
					log.Info("workflow worktree: merged", "workflow", e.workflow.Name, "diff", diffSummary)
				}
				if e.deps.Worktree.Remove != nil {
					e.deps.Worktree.Remove(e.repoDir, e.worktreeDir)
				}
			}
		} else {
			log.Info("workflow worktree: keeping for inspection (workflow failed)",
				"workflow", e.workflow.Name, "path", e.worktreeDir, "status", run.Status)
		}
	}
}

// --- DAG execution ---

func (e *Executor) executeDAG(ctx context.Context) error {
	steps := e.workflow.Steps

	stepMap := make(map[string]*WorkflowStep)
	remaining := make(map[string]int)
	dependents := make(map[string][]string)

	for i := range steps {
		s := &steps[i]
		stepMap[s.ID] = s
		remaining[s.ID] = len(s.DependsOn)
		for _, dep := range s.DependsOn {
			dependents[dep] = append(dependents[dep], s.ID)
		}
	}

	readyCh := make(chan string, len(steps))
	doneCh := make(chan stepDoneMsg, len(steps))

	completed := 0
	total := len(steps)
	aborted := false

	if e.resumeState != nil {
		for id, sr := range e.resumeState {
			if _, exists := stepMap[id]; !exists {
				continue
			}
			if sr.Status == "success" || sr.Status == "skipped" {
				completed++
				for _, dep := range dependents[id] {
					remaining[dep]--
				}
			}
		}
		for i := range steps {
			s := &steps[i]
			if StepType(s) == "condition" {
				if sr, ok := e.resumeState[s.ID]; ok && sr.Status == "success" {
					skipped := e.replayConditionSkips(s, sr, remaining, dependents)
					completed += len(skipped)
				}
			}
		}
		for id, cnt := range remaining {
			if cnt == 0 {
				if sr, ok := e.resumeState[id]; ok && (sr.Status == "success" || sr.Status == "skipped") {
					continue
				}
				readyCh <- id
			}
		}
	} else {
		for id, cnt := range remaining {
			if cnt == 0 {
				readyCh <- id
			}
		}
	}

	for completed < total {
		select {
		case <-ctx.Done():
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
				e.mu.Lock()
				e.run.StepResults[stepID].Status = "skipped"
				e.mu.Unlock()
				completed++
				for _, dep := range dependents[stepID] {
					remaining[dep]--
					if remaining[dep] == 0 {
						readyCh <- dep
					}
				}
				continue
			}

			e.mu.Lock()
			skipSr := e.run.StepResults[stepID]
			isSkipped := skipSr != nil && skipSr.Status == "skipped"
			e.mu.Unlock()
			if isSkipped {
				continue
			}

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
			completed++

			e.mu.Lock()
			sr := e.run.StepResults[msg.id]
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

			e.wCtx.Steps[msg.id] = &WorkflowStepResult{
				Output: msg.result.Output,
				Status: msg.result.Status,
				Error:  msg.result.Error,
			}
			e.mu.Unlock()

			if msg.result.Status != "success" && msg.result.Status != "skipped" {
				step := stepMap[msg.id]
				onErr := step.OnError
				if onErr == "" {
					onErr = "stop"
				}
				if onErr == "stop" {
					aborted = true
				}
			}

			step := stepMap[msg.id]
			if StepType(step) == "condition" {
				skippedSteps := e.handleConditionResult(step, msg.result, remaining, dependents, readyCh)
				visited := make(map[string]bool)
				for i := 0; i < len(skippedSteps); i++ {
					sid := skippedSteps[i]
					if visited[sid] {
						continue
					}
					visited[sid] = true
					completed++

					if ss, ok := stepMap[sid]; ok && StepType(ss) == "condition" {
						for _, target := range []string{ss.Then, ss.Else} {
							if target == "" {
								continue
							}
							e.mu.Lock()
							if sr2, ok2 := e.run.StepResults[target]; ok2 && sr2.Status == "pending" {
								sr2.Status = "skipped"
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
			}

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

func (e *Executor) handleConditionResult(step *WorkflowStep, result *StepRunResult,
	remaining map[string]int, dependents map[string][]string, readyCh chan string) []string {

	chosenTarget := strings.TrimSpace(result.Output)

	for _, dep := range dependents[step.ID] {
		remaining[dep]--
		if remaining[dep] == 0 {
			readyCh <- dep
		}
	}

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

func (e *Executor) replayConditionSkips(step *WorkflowStep, sr *StepRunResult,
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
		for _, dep := range dependents[skipTarget] {
			remaining[dep]--
		}
	}
	return skipped
}

// --- Step execution ---

func (e *Executor) executeStep(ctx context.Context, step *WorkflowStep) *StepRunResult {
	start := time.Now()

	result := &StepRunResult{
		StepID:    step.ID,
		StartedAt: start.Format(time.RFC3339),
	}

	e.mu.Lock()
	e.run.StepResults[step.ID].Status = "running"
	e.run.StepResults[step.ID].StartedAt = start.Format(time.RFC3339)
	e.mu.Unlock()
	e.checkpoint()

	e.publishEvent("step_started", map[string]any{
		"runId":  e.run.ID,
		"stepId": step.ID,
		"type":   StepType(step),
		"role":   step.Agent,
	})

	maxRetries := step.RetryMax
	if step.OnError != "retry" {
		maxRetries = 0
	}

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
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
			log.DebugCtx(ctx, "step retry", "workflow", e.workflow.Name, "step", step.ID,
				"attempt", attempt+1, "maxRetries", maxRetries+1)
		}

		e.runStepOnce(ctx, step, result)

		if result.Status == "success" || result.Status == "skipped" {
			break
		}
	}

	result.FinishedAt = time.Now().Format(time.RFC3339)
	result.DurationMs = time.Since(start).Milliseconds()

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

	e.checkpoint()

	return result
}

func (e *Executor) runStepOnce(ctx context.Context, step *WorkflowStep, result *StepRunResult) {
	e.mu.Lock()
	wCtx := e.wCtx
	e.mu.Unlock()

	st := StepType(step)

	if e.mode == WorkflowModeDryRun {
		switch st {
		case "dispatch":
			e.runDispatchStepDryRun(step, result, wCtx)
		case "skill":
			e.runSkillStepDryRun(step, result)
		case "handoff":
			e.runHandoffStepDryRun(step, result, wCtx)
		case "condition":
			e.runConditionStep(step, result, wCtx)
		case "parallel":
			e.runParallelStep(ctx, step, result, wCtx)
		case "tool_call":
			result.Status = "success"
			result.Output = fmt.Sprintf("[DRY-RUN] Would call tool: %s", step.ToolName)
		case "delay":
			result.Status = "success"
			result.Output = fmt.Sprintf("[DRY-RUN] Would delay: %s", step.Delay)
		case "external":
			result.Status = "success"
			result.Output = fmt.Sprintf("[DRY-RUN] Would call external URL: %s (callback mode: %s)", step.ExternalURL, step.CallbackMode)
		case "human":
			subtype := step.HumanSubtype
			if subtype == "" {
				subtype = "approval"
			}
			result.Status = "success"
			result.Output = fmt.Sprintf("[DRY-RUN] Would wait for human %s (assignee: %s)", subtype, step.HumanAssignee)
		case "notify":
			msg := ResolveTemplate(step.NotifyMsg, wCtx)
			result.Status = "success"
			result.Output = fmt.Sprintf("[DRY-RUN] Would notify (%s): %s", step.NotifyTo, msg)
		default:
			result.Status = "error"
			result.Error = fmt.Sprintf("unknown step type: %s", step.Type)
		}
		return
	}

	if e.mode == WorkflowModeShadow {
		switch st {
		case "dispatch":
			e.runDispatchStepShadow(ctx, step, result, wCtx)
			return
		case "handoff":
			e.runHandoffStepShadow(ctx, step, result, wCtx)
			return
		}
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

// --- Step type implementations ---

func (e *Executor) runDispatchStep(ctx context.Context, step *WorkflowStep,
	result *StepRunResult, wCtx *WorkflowContext) {

	task := e.deps.BuildStepTask(step, wCtx, e.workflow.Name)
	e.deps.FillDefaults(&task)

	if e.worktreeDir != "" {
		task.Workdir = e.worktreeDir
	}

	result.TaskID = task.ID
	result.SessionID = task.SessionID

	now := time.Now().Format(time.RFC3339)
	e.deps.CreateSession(SessionParams{
		ID:        task.SessionID,
		Agent:     task.Agent,
		Source:    "workflow:" + e.workflow.Name,
		Status:    "active",
		Title:     fmt.Sprintf("%s / %s", e.workflow.Name, step.ID),
		CreatedAt: now,
		UpdatedAt: now,
	})

	taskResult := e.deps.RunTask(ctx, task, e.sem, e.childSem)
	e.deps.RecordSessionActivity(task, taskResult, task.Agent)

	result.Output = taskResult.Output
	result.CostUSD = taskResult.CostUSD
	result.SessionID = taskResult.SessionID

	switch taskResult.Status {
	case "success":
		result.Status = "success"
		if e.deps.ParseAutoDelegate != nil {
			delegations := e.deps.ParseAutoDelegate(result.Output)
			if len(delegations) > 0 && e.deps.ProcessAutoDelegations != nil {
				result.Output = e.deps.ProcessAutoDelegations(ctx, delegations,
					result.Output, e.run.ID, task.Agent, step.ID,
					e.broker, e.sem, e.childSem)
			}
		}
	case "timeout":
		result.Status = "timeout"
		result.Error = taskResult.Error
	default:
		result.Status = "error"
		result.Error = taskResult.Error
	}
}

func (e *Executor) runSkillStep(ctx context.Context, step *WorkflowStep,
	result *StepRunResult, wCtx *WorkflowContext) {

	if e.deps.ExecuteSkill == nil {
		result.Status = "error"
		result.Error = "skill execution not configured"
		return
	}

	vars := make(map[string]string)
	for k, v := range wCtx.Input {
		vars[k] = v
	}
	for i, arg := range step.SkillArgs {
		vars[fmt.Sprintf("arg%d", i)] = ResolveTemplate(arg, wCtx)
	}

	output, status, errStr := e.deps.ExecuteSkill(ctx, step.Skill, vars)
	result.Output = output

	switch status {
	case "success":
		result.Status = "success"
	case "timeout":
		result.Status = "timeout"
		result.Error = errStr
	default:
		result.Status = "error"
		result.Error = errStr
	}
}

func (e *Executor) runConditionStep(step *WorkflowStep, result *StepRunResult, wCtx *WorkflowContext) {
	if EvalCondition(step.If, wCtx) {
		result.Output = step.Then
	} else {
		result.Output = step.Else
	}
	result.Status = "success"
}

func (e *Executor) runParallelStep(ctx context.Context, step *WorkflowStep,
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

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		gracePeriod := time.NewTimer(10 * time.Second)
		select {
		case <-done:
			gracePeriod.Stop()
		case <-gracePeriod.C:
			log.Warn("workflow parallel step: sub-steps did not finish within grace period", "step", step.ID)
		}
	}

	var outputs []string
	hasError := false
	for _, sr := range subResults {
		if sr == nil {
			continue
		}
		e.mu.Lock()
		e.wCtx.Steps[sr.StepID] = &WorkflowStepResult{Output: sr.Output, Status: sr.Status, Error: sr.Error}
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

func (e *Executor) runHandoffStep(ctx context.Context, step *WorkflowStep,
	result *StepRunResult, wCtx *WorkflowContext) {

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

	instruction := ResolveTemplate(step.Prompt, wCtx)

	fromAgent := ""
	for _, s := range e.workflow.Steps {
		if s.ID == step.HandoffFrom {
			fromAgent = s.Agent
			break
		}
	}

	now := time.Now().Format(time.RFC3339)
	handoffID := e.deps.NewUUID()
	toSessionID := e.deps.NewUUID()

	fromSessionID := ""
	if sr, exists := e.run.StepResults[step.HandoffFrom]; exists {
		fromSessionID = sr.SessionID
	}

	contextStr := sourceResult.Output
	if e.deps.TruncateStr != nil {
		contextStr = e.deps.TruncateStr(sourceResult.Output, e.deps.Cfg.PromptBudget.ContextMaxOrDefault())
	}

	e.deps.RecordHandoff(HandoffParams{
		ID:            handoffID,
		WorkflowRunID: e.run.ID,
		FromAgent:     fromAgent,
		ToAgent:       step.Agent,
		FromStepID:    step.HandoffFrom,
		ToStepID:      step.ID,
		FromSessionID: fromSessionID,
		ToSessionID:   toSessionID,
		Context:       contextStr,
		Instruction:   instruction,
		Status:        "pending",
		CreatedAt:     now,
	})

	truncInstruction := instruction
	if e.deps.Truncate != nil {
		truncInstruction = e.deps.Truncate(instruction, 200)
	}
	e.deps.SendAgentMessage(AgentMessageParams{
		WorkflowRunID: e.run.ID,
		FromAgent:     fromAgent,
		ToAgent:       step.Agent,
		Type:          "handoff",
		Content:       fmt.Sprintf("Handoff from %s: %s", fromAgent, truncInstruction),
		RefID:         handoffID,
		CreatedAt:     now,
	})

	e.publishEvent("handoff", map[string]any{
		"runId":      e.run.ID,
		"handoffId":  handoffID,
		"fromAgent":  fromAgent,
		"toAgent":    step.Agent,
		"fromStepId": step.HandoffFrom,
		"toStepId":   step.ID,
	})

	prompt := e.deps.BuildHandoffPrompt(sourceResult.Output, instruction)
	resolvedAgent := ResolveTemplate(step.Agent, wCtx)

	task := TaskParams{
		ID:             e.deps.NewUUID(),
		Name:           fmt.Sprintf("%s/%s (handoff:%s→%s)", e.workflow.Name, step.ID, fromAgent, resolvedAgent),
		Prompt:         prompt,
		Agent:          resolvedAgent,
		Model:          ResolveTemplate(step.Model, wCtx),
		Provider:       ResolveTemplate(step.Provider, wCtx),
		Timeout:        ResolveTemplate(step.Timeout, wCtx),
		Budget:         step.Budget,
		PermissionMode: ResolveTemplate(step.PermissionMode, wCtx),
		Source:         fmt.Sprintf("workflow:%s:handoff", e.workflow.Name),
		SessionID:      toSessionID,
	}
	e.deps.FillDefaults(&task)

	if e.worktreeDir != "" {
		task.Workdir = e.worktreeDir
	}

	result.TaskID = task.ID
	result.SessionID = toSessionID

	e.deps.CreateSession(SessionParams{
		ID:        toSessionID,
		Agent:     resolvedAgent,
		Source:    fmt.Sprintf("workflow:%s:handoff", e.workflow.Name),
		Status:    "active",
		Title:     fmt.Sprintf("Handoff: %s → %s / %s", fromAgent, resolvedAgent, step.ID),
		CreatedAt: now,
		UpdatedAt: now,
	})

	e.deps.UpdateHandoffStatus(handoffID, "active")

	taskResult := e.deps.RunTask(ctx, task, e.sem, e.childSem)
	e.deps.RecordSessionActivity(task, taskResult, resolvedAgent)

	result.Output = taskResult.Output
	result.CostUSD = taskResult.CostUSD

	switch taskResult.Status {
	case "success":
		result.Status = "success"
		e.deps.UpdateHandoffStatus(handoffID, "completed")
	case "timeout":
		result.Status = "timeout"
		result.Error = taskResult.Error
		e.deps.UpdateHandoffStatus(handoffID, "error")
	default:
		result.Status = "error"
		result.Error = taskResult.Error
		e.deps.UpdateHandoffStatus(handoffID, "error")
	}

	truncOutput := taskResult.Output
	if e.deps.TruncateStr != nil {
		truncOutput = e.deps.TruncateStr(taskResult.Output, 2000)
	}
	e.deps.SendAgentMessage(AgentMessageParams{
		WorkflowRunID: e.run.ID,
		FromAgent:     step.Agent,
		ToAgent:       fromAgent,
		Type:          "response",
		Content:       truncOutput,
		RefID:         handoffID,
		CreatedAt:     time.Now().Format(time.RFC3339),
	})

	log.DebugCtx(ctx, "handoff completed", "from", fromAgent, "to", step.Agent,
		"workflow", e.workflow.Name, "step", step.ID, "status", result.Status)
}

func (e *Executor) runToolCallStep(ctx context.Context, step *WorkflowStep,
	result *StepRunResult, wCtx *WorkflowContext) {

	if e.deps.ExecuteToolCall == nil {
		result.Status = "error"
		result.Error = "tool call execution not configured"
		return
	}

	expandedInput := ExpandToolInput(step.ToolInput, wCtx.Input)

	output, err := e.deps.ExecuteToolCall(ctx, step.ToolName, expandedInput)
	if err != nil {
		result.Status = "error"
		result.Error = fmt.Sprintf("tool %q error: %v", step.ToolName, err)
		result.Output = output
		return
	}

	result.Status = "success"
	result.Output = output
}

func (e *Executor) runDelayStep(ctx context.Context, step *WorkflowStep, result *StepRunResult) {
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

func (e *Executor) runNotifyStep(step *WorkflowStep, result *StepRunResult, wCtx *WorkflowContext) {
	msg := ResolveTemplate(step.NotifyMsg, wCtx)

	truncMsg := msg
	if len(truncMsg) > 200 {
		truncMsg = truncMsg[:200]
	}
	log.Info("workflow notify", "workflow", e.workflow.Name, "step", step.ID,
		"to", step.NotifyTo, "message", truncMsg)

	if e.broker != nil {
		e.broker.Publish("_triggers", dtypes.SSEEvent{
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

func (e *Executor) runExternalStep(ctx context.Context, step *WorkflowStep, result *StepRunResult) {
	if e.deps.RunExternalStep == nil {
		result.Status = "error"
		result.Error = "external step execution not configured"
		return
	}

	extCtx := ExternalStepContext{
		Run:  e.run,
		WCtx: e.wCtx,
		ResolveTemplate:    e.resolveTemplateWithFields,
		ResolveTemplateMap: e.resolveTemplateMapWithFields,
		PublishEvent:       e.publishEvent,
	}
	e.deps.RunExternalStep(ctx, extCtx, step, result)
}

func (e *Executor) runHumanStep(ctx context.Context, step *WorkflowStep, result *StepRunResult) {
	if e.deps.RunHumanStep == nil {
		result.Status = "error"
		result.Error = "human step execution not configured"
		return
	}

	hCtx := HumanStepContext{
		Run:             e.run,
		WCtx:            e.wCtx,
		ResolveTemplate: e.resolveTemplateWithFields,
		PublishEvent:    e.publishEvent,
		Checkpoint:      e.checkpoint,
	}
	e.deps.RunHumanStep(ctx, hCtx, step, result)
}

// --- Dry-run implementations ---

func (e *Executor) runDispatchStepDryRun(step *WorkflowStep, result *StepRunResult, wCtx *WorkflowContext) {
	task := e.deps.BuildStepTask(step, wCtx, e.workflow.Name)
	e.deps.FillDefaults(&task)

	result.TaskID = task.ID
	result.SessionID = task.SessionID

	est := e.deps.EstimateTaskCost(task)
	result.CostUSD = est.EstimatedCostUSD
	result.Status = "success"
	result.Output = fmt.Sprintf("[DRY-RUN] step=%s role=%s model=%s estimated_cost=$%.4f",
		step.ID, task.Agent, est.Model, est.EstimatedCostUSD)
}

func (e *Executor) runSkillStepDryRun(step *WorkflowStep, result *StepRunResult) {
	result.Status = "success"
	result.Output = fmt.Sprintf("[DRY-RUN] Would execute skill: %s", step.Skill)
}

func (e *Executor) runHandoffStepDryRun(step *WorkflowStep, result *StepRunResult, wCtx *WorkflowContext) {
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

	instruction := ResolveTemplate(step.Prompt, wCtx)
	prompt := e.deps.BuildHandoffPrompt(sourceResult.Output, instruction)
	resolvedAgent := ResolveTemplate(step.Agent, wCtx)

	task := TaskParams{
		ID:             e.deps.NewUUID(),
		Name:           fmt.Sprintf("%s/%s (handoff)", e.workflow.Name, step.ID),
		Prompt:         prompt,
		Agent:          resolvedAgent,
		Model:          ResolveTemplate(step.Model, wCtx),
		Provider:       ResolveTemplate(step.Provider, wCtx),
		Timeout:        ResolveTemplate(step.Timeout, wCtx),
		Budget:         step.Budget,
		PermissionMode: ResolveTemplate(step.PermissionMode, wCtx),
		Source:         fmt.Sprintf("workflow:%s:handoff", e.workflow.Name),
		SessionID:      e.deps.NewUUID(),
	}
	e.deps.FillDefaults(&task)

	est := e.deps.EstimateTaskCost(task)
	result.TaskID = task.ID
	result.SessionID = task.SessionID
	result.CostUSD = est.EstimatedCostUSD
	result.Status = "success"
	result.Output = fmt.Sprintf("[DRY-RUN] step=%s role=%s model=%s estimated_cost=$%.4f (handoff)",
		step.ID, resolvedAgent, est.Model, est.EstimatedCostUSD)
}

// --- Shadow implementations ---

func (e *Executor) runDispatchStepShadow(ctx context.Context, step *WorkflowStep,
	result *StepRunResult, wCtx *WorkflowContext) {

	task := e.deps.BuildStepTask(step, wCtx, e.workflow.Name)
	e.deps.FillDefaults(&task)

	result.TaskID = task.ID
	result.SessionID = task.SessionID

	taskResult := e.deps.RunTaskNoRecord(ctx, task, e.sem, e.childSem)

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

func (e *Executor) runHandoffStepShadow(ctx context.Context, step *WorkflowStep,
	result *StepRunResult, wCtx *WorkflowContext) {

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

	instruction := ResolveTemplate(step.Prompt, wCtx)
	prompt := e.deps.BuildHandoffPrompt(sourceResult.Output, instruction)
	resolvedAgent := ResolveTemplate(step.Agent, wCtx)

	task := TaskParams{
		ID:             e.deps.NewUUID(),
		Name:           fmt.Sprintf("%s/%s (handoff:%s)", e.workflow.Name, step.ID, resolvedAgent),
		Prompt:         prompt,
		Agent:          resolvedAgent,
		Model:          ResolveTemplate(step.Model, wCtx),
		Provider:       ResolveTemplate(step.Provider, wCtx),
		Timeout:        ResolveTemplate(step.Timeout, wCtx),
		Budget:         step.Budget,
		PermissionMode: ResolveTemplate(step.PermissionMode, wCtx),
		Source:         fmt.Sprintf("workflow:%s:handoff", e.workflow.Name),
		SessionID:      e.deps.NewUUID(),
	}
	e.deps.FillDefaults(&task)

	result.TaskID = task.ID
	result.SessionID = task.SessionID

	taskResult := e.deps.RunTaskNoRecord(ctx, task, e.sem, e.childSem)

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

// --- Template resolution with field access ---

// resolveTemplateWithFields resolves {{...}} and {{steps.id.output.field}} templates.
func (e *Executor) resolveTemplateWithFields(tmpl string) string {
	return TemplateVarRe.ReplaceAllStringFunc(tmpl, func(match string) string {
		expr := strings.TrimSpace(match[2 : len(match)-2])
		parts := strings.SplitN(expr, ".", 4)

		// Handle {{steps.id.output.fieldPath}}
		if len(parts) >= 4 && parts[0] == "steps" && parts[2] == "output" {
			stepID := parts[1]
			fieldPath := strings.Join(parts[3:], ".")
			e.mu.Lock()
			stepResult, ok := e.wCtx.Steps[stepID]
			e.mu.Unlock()
			if !ok {
				return ""
			}
			return extractJSONPath(stepResult.Output, fieldPath)
		}

		e.mu.Lock()
		wCtx := e.wCtx
		e.mu.Unlock()
		return ResolveExpr(expr, wCtx)
	})
}

func (e *Executor) resolveTemplateMapWithFields(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	result := make(map[string]string, len(m))
	for k, v := range m {
		result[k] = e.resolveTemplateWithFields(v)
	}
	return result
}

// --- SSE event publishing ---

func (e *Executor) publishEvent(eventType string, data map[string]any) {
	if e.broker == nil {
		return
	}
	e.broker.PublishMulti([]string{
		"workflow:" + e.run.ID,
		"workflow:" + e.workflow.Name,
	}, dtypes.SSEEvent{
		Type:   eventType,
		TaskID: e.run.ID,
		Data:   data,
	})
}

// --- Checkpointing ---

func (e *Executor) checkpoint() {
	if e.deps.Checkpoint != nil {
		e.deps.Checkpoint(e.run)
	}
}

// --- JSON path extraction ---

func extractJSONPath(jsonStr, path string) string {
	if jsonStr == "" || path == "" {
		return ""
	}

	var data any
	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		return ""
	}

	parts := strings.Split(path, ".")
	current := data
	for _, part := range parts {
		m, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		val, ok := m[part]
		if !ok {
			return ""
		}
		current = val
	}

	switch v := current.(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(b)
	}
}
