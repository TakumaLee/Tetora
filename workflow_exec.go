package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// --- Workflow Run Types ---

// WorkflowRun tracks a single execution of a workflow.
type WorkflowRun struct {
	ID           string                       `json:"id"`
	WorkflowName string                       `json:"workflowName"`
	Status       string                       `json:"status"` // "running", "success", "error", "cancelled", "timeout"
	StartedAt    string                       `json:"startedAt"`
	FinishedAt   string                       `json:"finishedAt,omitempty"`
	DurationMs   int64                        `json:"durationMs,omitempty"`
	TotalCost    float64                      `json:"totalCostUsd,omitempty"`
	Variables    map[string]string            `json:"variables,omitempty"`
	StepResults  map[string]*StepRunResult    `json:"stepResults"`
	Error        string                       `json:"error,omitempty"`
}

// StepRunResult tracks the execution of one step.
type StepRunResult struct {
	StepID     string  `json:"stepId"`
	Status     string  `json:"status"` // "pending", "running", "success", "error", "skipped", "timeout"
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
	broker   *sseBroker
	mode     WorkflowRunMode
	mu       sync.Mutex
}

// executeWorkflow runs a full workflow and returns the completed run.
// An optional mode parameter controls execution behavior (default: WorkflowModeLive).
func executeWorkflow(ctx context.Context, cfg *Config, w *Workflow, vars map[string]string,
	state *dispatchState, sem chan struct{}, mode ...WorkflowRunMode) *WorkflowRun {

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
		broker:   broker,
		mode:     runMode,
	}

	// Apply workflow-level timeout.
	execCtx := ctx
	var execCancel context.CancelFunc
	if w.Timeout != "" {
		if d, err := time.ParseDuration(w.Timeout); err == nil {
			execCtx, execCancel = context.WithTimeout(ctx, d)
			defer execCancel()
		}
	}

	// Publish workflow started event.
	exec.publishEvent("workflow_started", map[string]any{
		"runId":    runID,
		"workflow": w.Name,
		"steps":    len(w.Steps),
		"stepDefs": buildStepSummaries(w.Steps),
	})

	// Execute DAG.
	err := exec.executeDAG(execCtx)

	// Finalize run.
	run.FinishedAt = time.Now().Format(time.RFC3339)
	run.DurationMs = time.Since(now).Milliseconds()

	// Calculate total cost.
	for _, sr := range run.StepResults {
		run.TotalCost += sr.CostUSD
	}

	if err != nil {
		run.Status = "error"
		run.Error = err.Error()
	} else if execCtx.Err() == context.DeadlineExceeded {
		run.Status = "timeout"
		run.Error = "workflow timeout exceeded"
	} else if ctx.Err() != nil {
		run.Status = "cancelled"
		run.Error = "workflow cancelled"
	} else {
		// Check if any step failed.
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
	exec.publishEvent("workflow_completed", map[string]any{
		"runId":      runID,
		"workflow":   w.Name,
		"status":     run.Status,
		"durationMs": run.DurationMs,
		"totalCost":  run.TotalCost,
	})

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
			logInfoCtx(ctx, "workflow completed", "workflow", w.Name, "runID", runID[:8], "status", run.Status, "durationMs", run.DurationMs, "cost", run.TotalCost)
		} else {
			logWarnCtx(ctx, "workflow completed with error", "workflow", w.Name, "runID", runID[:8], "status", run.Status, "durationMs", run.DurationMs, "cost", run.TotalCost)
		}
	} else {
		logInfoCtx(ctx, "workflow completed", "workflow", w.Name, "runID", runID[:8], "status", run.Status, "mode", string(runMode), "durationMs", run.DurationMs, "cost", run.TotalCost)
	}

	return run
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

	// Seed steps with no dependencies.
	for id, cnt := range remaining {
		if cnt == 0 {
			readyCh <- id
		}
	}

	completed := 0
	total := len(steps)
	inFlight := 0
	aborted := false

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

			inFlight++
			go func(id string) {
				step := stepMap[id]
				result := e.executeStep(ctx, step)
				doneCh <- stepDoneMsg{id: id, result: result}
			}(stepID)

		case msg := <-doneCh:
			inFlight--
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
				e.handleConditionResult(step, msg.result, remaining, dependents, readyCh)
				continue
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
func (e *workflowExecutor) handleConditionResult(step *WorkflowStep, result *StepRunResult,
	remaining map[string]int, dependents map[string][]string, readyCh chan string) {

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

	if skipTarget != "" {
		e.mu.Lock()
		if sr, ok := e.run.StepResults[skipTarget]; ok && sr.Status == "pending" {
			sr.Status = "skipped"
		}
		e.mu.Unlock()
	}
}

// executeStep runs a single step with retry logic.
func (e *workflowExecutor) executeStep(ctx context.Context, step *WorkflowStep) *StepRunResult {
	start := time.Now()

	result := &StepRunResult{
		StepID:    step.ID,
		StartedAt: start.Format(time.RFC3339),
	}

	// Mark as running.
	e.mu.Lock()
	e.run.StepResults[step.ID].Status = "running"
	e.mu.Unlock()

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
			logDebugCtx(ctx, "step retry", "workflow", e.workflow.Name, "step", step.ID, "attempt", attempt+1, "maxRetries", maxRetries+1)
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

	result.TaskID = task.ID
	result.SessionID = task.SessionID

	// Create a session for this step.
	createSession(e.cfg.HistoryDB, Session{
		ID:     task.SessionID,
		Agent:   step.Agent,
		Source: "workflow:" + e.workflow.Name,
		Status: "active",
		Title:  fmt.Sprintf("%s / %s", e.workflow.Name, step.ID),
	})

	// Execute using runSingleTask (respects semaphore).
	taskResult := runSingleTask(ctx, e.cfg, task, e.sem, step.Agent)

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
				result.Output, e.run.ID, step.Agent, step.ID,
				e.state, e.sem, e.broker)
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

	wg.Wait()

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
		Context:       truncateStr(sourceOutput, 5000),
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

	task := Task{
		ID:        newUUID(),
		Name:      fmt.Sprintf("%s/%s (handoff:%s→%s)", e.workflow.Name, step.ID, fromAgent, step.Agent),
		Prompt:    prompt,
		Agent:      step.Agent,
		Model:     step.Model,
		Provider:  step.Provider,
		Timeout:   step.Timeout,
		Budget:    step.Budget,
		Source:    fmt.Sprintf("workflow:%s:handoff", e.workflow.Name),
		SessionID: toSessionID,
	}
	if task.PermissionMode == "" {
		task.PermissionMode = step.PermissionMode
	}
	fillDefaults(e.cfg, &task)

	result.TaskID = task.ID
	result.SessionID = toSessionID

	// Create session.
	createSession(e.cfg.HistoryDB, Session{
		ID:        toSessionID,
		Agent:      step.Agent,
		Source:    fmt.Sprintf("workflow:%s:handoff", e.workflow.Name),
		Status:    "active",
		Title:     fmt.Sprintf("Handoff: %s → %s / %s", fromAgent, step.Agent, step.ID),
		CreatedAt: now,
		UpdatedAt: now,
	})

	// Update handoff to active.
	updateHandoffStatus(e.cfg.HistoryDB, handoffID, "active")

	// Execute.
	taskResult := runSingleTask(ctx, e.cfg, task, e.sem, step.Agent)

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

	logDebugCtx(ctx, "handoff completed", "from", fromAgent, "to", step.Agent, "workflow", e.workflow.Name, "step", step.ID, "status", result.Status)
}

// --- P18.3: New Step Type Implementations ---

// runToolCallStep executes a registered tool.
func (e *workflowExecutor) runToolCallStep(ctx context.Context, step *WorkflowStep,
	result *StepRunResult, wCtx *WorkflowContext) {

	if e.cfg.toolRegistry == nil {
		result.Status = "error"
		result.Error = "tool registry not initialized"
		return
	}

	tool, ok := e.cfg.toolRegistry.Get(step.ToolName)
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
	logInfo("workflow notify", "workflow", e.workflow.Name, "step", step.ID,
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

	est := estimateTaskCost(e.cfg, task, step.Agent)
	result.CostUSD = est.EstimatedCostUSD
	result.Status = "success"
	result.Output = fmt.Sprintf("[DRY-RUN] step=%s role=%s model=%s estimated_cost=$%.4f",
		step.ID, step.Agent, est.Model, est.EstimatedCostUSD)
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

	task := Task{
		ID:        newUUID(),
		Name:      fmt.Sprintf("%s/%s (handoff)", e.workflow.Name, step.ID),
		Prompt:    prompt,
		Agent:      step.Agent,
		Model:     step.Model,
		Provider:  step.Provider,
		Timeout:   step.Timeout,
		Budget:    step.Budget,
		Source:    fmt.Sprintf("workflow:%s:handoff", e.workflow.Name),
		SessionID: newUUID(),
	}
	fillDefaults(e.cfg, &task)

	est := estimateTaskCost(e.cfg, task, step.Agent)
	result.TaskID = task.ID
	result.SessionID = task.SessionID
	result.CostUSD = est.EstimatedCostUSD
	result.Status = "success"
	result.Output = fmt.Sprintf("[DRY-RUN] step=%s role=%s model=%s estimated_cost=$%.4f (handoff)",
		step.ID, step.Agent, est.Model, est.EstimatedCostUSD)
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
	taskResult := runSingleTaskNoRecord(ctx, e.cfg, task, e.sem, step.Agent)

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

	task := Task{
		ID:        newUUID(),
		Name:      fmt.Sprintf("%s/%s (handoff:%s)", e.workflow.Name, step.ID, step.Agent),
		Prompt:    prompt,
		Agent:      step.Agent,
		Model:     step.Model,
		Provider:  step.Provider,
		Timeout:   step.Timeout,
		Budget:    step.Budget,
		Source:    fmt.Sprintf("workflow:%s:handoff", e.workflow.Name),
		SessionID: newUUID(),
	}
	fillDefaults(e.cfg, &task)

	result.TaskID = task.ID
	result.SessionID = task.SessionID

	// Execute without recording history/session/handoff metadata.
	taskResult := runSingleTaskNoRecord(ctx, e.cfg, task, e.sem, step.Agent)

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
func runSingleTaskNoRecord(ctx context.Context, cfg *Config, task Task, sem chan struct{}, agentName string) TaskResult {
	// Validate directories before running.
	if err := validateDirs(cfg, task, agentName); err != nil {
		return TaskResult{
			ID: task.ID, Name: task.Name, Status: "error",
			Error: err.Error(), Model: task.Model, SessionID: task.SessionID,
		}
	}

	sem <- struct{}{}
	defer func() { <-sem }()

	providerName := resolveProviderName(cfg, task, agentName)

	logDebugCtx(ctx, "shadow task start",
		"taskId", task.ID[:8], "name", task.Name,
		"model", task.Model, "provider", providerName)

	timeout, err := time.ParseDuration(task.Timeout)
	if err != nil {
		timeout = 15 * time.Minute
	}
	taskCtx, taskCancel := context.WithTimeout(ctx, timeout)
	defer taskCancel()

	start := time.Now()
	pr := executeWithProvider(taskCtx, cfg, task, agentName, cfg.registry, nil)
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

	logDebugCtx(ctx, "shadow task done",
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
	e.broker.PublishMulti([]string{
		"workflow:" + e.run.ID,
		"workflow:" + e.workflow.Name,
	}, SSEEvent{
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
	if _, err := queryDB(dbPath, workflowRunsTableSQL); err != nil {
		logWarn("init workflow_runs table failed", "error", err)
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
		`INSERT OR REPLACE INTO workflow_runs (id, workflow_name, status, started_at, finished_at, duration_ms, total_cost, variables, step_results, error, created_at)
		 VALUES ('%s','%s','%s','%s','%s',%d,%f,'%s','%s','%s','%s')`,
		escapeSQLite(run.ID),
		escapeSQLite(run.WorkflowName),
		escapeSQLite(run.Status),
		escapeSQLite(run.StartedAt),
		escapeSQLite(run.FinishedAt),
		run.DurationMs,
		run.TotalCost,
		escapeSQLite(string(varsJSON)),
		escapeSQLite(string(stepsJSON)),
		escapeSQLite(run.Error),
		escapeSQLite(run.StartedAt),
	)

	if _, err := queryDB(dbPath, sql); err != nil {
		logWarn("record workflow run failed", "error", err)
	}
}

// queryWorkflowRuns returns recent workflow runs.
func queryWorkflowRuns(dbPath string, limit int, workflowName string) ([]WorkflowRun, error) {
	if limit <= 0 {
		limit = 20
	}

	where := ""
	if workflowName != "" {
		where = fmt.Sprintf("WHERE workflow_name='%s'", escapeSQLite(workflowName))
	}

	sql := fmt.Sprintf(
		`SELECT id, workflow_name, status, started_at, finished_at, duration_ms, total_cost, variables, step_results, error
		 FROM workflow_runs %s ORDER BY created_at DESC LIMIT %d`,
		where, limit,
	)

	rows, err := queryDB(dbPath, sql)
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
		`SELECT id, workflow_name, status, started_at, finished_at, duration_ms, total_cost, variables, step_results, error
		 FROM workflow_runs WHERE id='%s'`,
		escapeSQLite(id),
	)

	rows, err := queryDB(dbPath, sql)
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
	}
	if v := jsonStr(row["variables"]); v != "" {
		json.Unmarshal([]byte(v), &run.Variables)
	}
	if v := jsonStr(row["step_results"]); v != "" {
		json.Unmarshal([]byte(v), &run.StepResults)
	}
	return run, nil
}
