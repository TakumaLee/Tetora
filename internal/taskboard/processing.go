package taskboard

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
	"time"

	"tetora/internal/config"
	"tetora/internal/db"
	"tetora/internal/dispatch"
	"tetora/internal/handoff"
	"tetora/internal/log"
)

// =============================================================================
// Section: Review types and verdicts
// =============================================================================

// reviewVerdict represents a three-way review decision.
type reviewVerdict string

const (
	reviewApprove  reviewVerdict = "approve"  // quality OK → done
	reviewFix      reviewVerdict = "fix"      // issues found but agent can fix → retry
	reviewEscalate reviewVerdict = "escalate" // needs human judgment → assign to user
)

type reviewResult struct {
	Verdict reviewVerdict
	Comment string
	CostUSD float64
}

// triageResult holds the commander's delegation decision.
type triageResult struct {
	TargetAgent string  `json:"targetAgent"`
	Workflow    string  `json:"workflow,omitempty"`
	Instruction string  `json:"instruction"`
	CostUSD     float64 `json:"-"`
}

// devQALoopResult holds the outcome of the Dev↔QA retry loop.
type devQALoopResult struct {
	Result     dispatch.TaskResult
	QAApproved bool    // true if QA review passed
	Attempts   int     // total execution attempts
	TotalCost  float64 // accumulated cost across all attempts (dev + QA)
}

// =============================================================================
// Section: Workflow routing
// =============================================================================

// routeWorkflow selects a workflow name based on the configured routing rules.
// Returns "" if routing is disabled or no rule matches.
func (d *Dispatcher) routeWorkflow(t TaskBoard) string {
	routing := d.engine.config.AutoDispatch.WorkflowRouting
	if !routing.Enabled || len(routing.Rules) == 0 {
		return routing.Fallback
	}

	// Look up project info for isPublic matching.
	var proj *ProjectInfo
	if t.Project != "" && t.Project != "default" && d.deps.GetProject != nil {
		proj = d.deps.GetProject(d.cfg.HistoryDB, t.Project)
	}

	for i, rule := range routing.Rules {
		if !matchRoutingRule(rule, t, proj) {
			continue
		}
		log.Info("workflow routing: matched rule",
			"task", t.ID, "rule", i, "workflow", rule.Workflow,
			"type", t.Type, "priority", t.Priority)
		return rule.Workflow
	}

	if routing.Fallback != "" {
		log.Info("workflow routing: using fallback", "task", t.ID, "workflow", routing.Fallback)
		return routing.Fallback
	}
	return ""
}

// matchRoutingRule checks whether a single routing rule matches the given task.
func matchRoutingRule(rule config.WorkflowRoutingRule, t TaskBoard, proj *ProjectInfo) bool {
	// Types filter.
	if len(rule.Types) > 0 && !containsStr(rule.Types, t.Type) {
		return false
	}
	// Priority filter.
	if len(rule.Priority) > 0 && !containsStr(rule.Priority, t.Priority) {
		return false
	}
	// Projects filter.
	if len(rule.Projects) > 0 && !containsStr(rule.Projects, t.Project) {
		return false
	}
	// IsPublic filter (heuristic: github.com or gitlab.com in RepoURL → public).
	if rule.IsPublic != nil && proj != nil {
		isPublic := strings.Contains(proj.RepoURL, "github.com") ||
			strings.Contains(proj.RepoURL, "gitlab.com")
		if *rule.IsPublic != isPublic {
			return false
		}
	}
	return true
}

func containsStr(ss []string, s string) bool {
	for _, v := range ss {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}

func truncStr(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// =============================================================================
// Section: dispatchTask — core task execution
// =============================================================================

func (d *Dispatcher) dispatchTask(t TaskBoard) {
	ctx := d.ctx

	prompt := t.Title
	if t.Description != "" {
		prompt = t.Title + "\n\n" + t.Description
	}

	// Inject spec/context comments.
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

	// Execution rules injection.
	prompt += "\n\n## Execution Rules\n"
	prompt += "- You are running autonomously. Do NOT use plan mode or ask for confirmation.\n"
	prompt += "- FIRST: Write your execution plan as a checklist to your todo.md file.\n"
	prompt += "- THEN: Execute each item, checking them off as you go.\n"
	prompt += "- Log major milestones by calling taskboard_comment.\n"
	prompt += "- Your todo.md persists across retries — if items exist, continue from where you left off.\n"
	prompt += "- Before starting: identify what is OUT OF SCOPE and note it in your todo.md. Prevent scope creep.\n"
	prompt += "- Use precise language in all outputs. Forbidden: 'should work', 'probably', 'might need', 'I think', 'seems to'. State facts or unknowns explicitly.\n"
	prompt += "- Before marking task complete: verify that a reviewer can understand your changes without asking clarifying questions. If not, add missing context.\n"

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

	taskID := t.ID
	task := dispatch.Task{
		Name:   "board:" + t.ID,
		Prompt: prompt,
		Agent:  t.Assignee,
		Source: "taskboard",
		OnStart: func() {
			if _, err := d.engine.MoveTask(taskID, "doing"); err != nil {
				log.Warn("taskboard dispatch: failed to move task to doing on start", "id", taskID, "error", err)
			}
		},
	}
	if d.deps.FillDefaults != nil {
		d.deps.FillDefaults(d.cfg, &task)
	}

	task.PermissionMode = "bypassPermissions"

	// LLM-based timeout estimation.
	if d.deps.Executor != nil {
		if llmTimeout := d.estimateTimeoutLLM(ctx, prompt); llmTimeout != "" {
			log.Info("taskboard dispatch: LLM timeout estimate", "id", t.ID, "llm", llmTimeout)
			task.Timeout = llmTimeout
		}
	}

	// Apply taskboard-specific cost controls.
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
	var projectWorkdir string
	if t.Project != "" && t.Project != "default" && d.deps.GetProject != nil {
		if p := d.deps.GetProject(d.cfg.HistoryDB, t.Project); p != nil && p.Workdir != "" {
			task.Workdir = p.Workdir
			projectWorkdir = p.Workdir
		}
	}

	// Worktree isolation.
	var worktreeDir string
	if d.engine.config.GitWorktree && projectWorkdir != "" && d.deps.Worktrees != nil {
		d.cleanStaleLock(projectWorkdir, t.ID)

		if exec.Command("git", "-C", projectWorkdir, "rev-parse", "--git-dir").Run() == nil {
			branch := ""
			if d.deps.BuildBranch != nil {
				branch = d.deps.BuildBranch(d.engine.config.GitWorkflow, t)
			}
			wtDir, err := d.deps.Worktrees.Create(projectWorkdir, t.ID, branch)
			if err != nil {
				log.Warn("worktree: creation failed, falling back to shared workdir",
					"task", t.ID, "error", err)
				d.engine.AddComment(t.ID, "system",
					fmt.Sprintf("[worktree] Failed to create isolated worktree: %v. Using shared workdir.", err))
			} else {
				worktreeDir = wtDir
				task.Workdir = wtDir
				log.Info("worktree: task running in isolation", "task", t.ID, "path", wtDir)
				d.engine.AddComment(t.ID, "system",
					fmt.Sprintf("[worktree] Running in isolated worktree: %s", wtDir))
			}
		}
	}

	// Determine workflow.
	workflowName := t.Workflow // per-task override wins
	if workflowName == "" {
		workflowName = d.routeWorkflow(t) // smart routing
	}
	if workflowName == "" {
		workflowName = d.engine.config.DefaultWorkflow // final fallback
	}
	usedWorkflow := workflowName != "" && workflowName != "none"

	// Triage: commander decides delegation before execution.
	var handoffID string
	triageCost := 0.0
	triageAgent := d.engine.config.AutoDispatch.DefaultAgent
	if triageAgent == "" {
		triageAgent = "ruri"
	}

	shouldTriage := d.engine.config.AutoDispatch.TriageEnabled &&
		d.deps.Executor != nil &&
		t.Assignee == triageAgent &&
		t.RetryCount == 0

	if shouldTriage {
		tr := d.triageTask(ctx, t, prompt)
		if tr != nil {
			triageCost = tr.CostUSD
			handoffID = d.recordTriageHandoff(t, tr, triageAgent, prompt)

			task.Agent = tr.TargetAgent
			t.Assignee = tr.TargetAgent

			if tr.Workflow != "" {
				workflowName = tr.Workflow
				usedWorkflow = workflowName != "" && workflowName != "none"
			}
			if tr.Instruction != "" {
				task.Prompt = "[Commander Briefing]\n" + tr.Instruction +
					"\n\n[Original Task]\n" + prompt
				// Also update t.Description so workflow vars pick up the briefing.
				t.Description = "[Commander Briefing]\n" + tr.Instruction + "\n\n" + t.Description
			}

			// Re-apply defaults for new agent.
			if d.deps.FillDefaults != nil {
				d.deps.FillDefaults(d.cfg, &task)
			}
			task.PermissionMode = "bypassPermissions"

			// Re-apply per-task model/budget overrides (FillDefaults may have overwritten them).
			if t.Model != "" {
				task.Model = t.Model
			} else if dispatchCfg.DefaultModel != "" {
				task.Model = dispatchCfg.DefaultModel
			}
			if dispatchCfg.MaxBudget > 0 && (task.Budget == 0 || task.Budget > dispatchCfg.MaxBudget) {
				task.Budget = dispatchCfg.MaxBudget
			}

			d.engine.AddComment(t.ID, triageAgent,
				fmt.Sprintf("[triage] → %s: %s", tr.TargetAgent, truncStr(tr.Instruction, 200)))

			log.Info("triage: delegating task", "task", t.ID, "from", triageAgent,
				"to", tr.TargetAgent, "workflow", tr.Workflow, "cost", triageCost)
		}
	}

	start := time.Now()
	var result dispatch.TaskResult
	qaApproved := false

	if d.engine.config.AutoDispatch.ReviewLoop {
		loopResult := d.devQALoop(ctx, t, task, usedWorkflow, workflowName)
		result = loopResult.Result
		qaApproved = loopResult.QAApproved
		result.CostUSD = loopResult.TotalCost
		if loopResult.Attempts > 1 {
			log.Info("devQA loop summary", "task", t.ID, "attempts", loopResult.Attempts,
				"approved", qaApproved, "totalCost", loopResult.TotalCost)
		}
	} else {
		if usedWorkflow {
			result = d.runTaskWithWorkflow(ctx, t, task, workflowName)
		} else if d.deps.Executor != nil {
			result = d.deps.Executor.RunTask(ctx, task, t.Assignee)
		}
	}
	duration := time.Since(start)

	// Complete handoff record if triage was used.
	if handoffID != "" {
		status := "completed"
		if result.Status != "success" {
			status = "error"
		}
		if err := handoff.UpdateStatus(d.cfg.HistoryDB, handoffID, status); err != nil {
			log.Warn("triage: failed to update handoff status", "handoffId", handoffID, "error", err)
		}
		if err := handoff.SendAgentMessage(d.cfg.HistoryDB, handoff.AgentMessage{
			FromAgent: t.Assignee,
			ToAgent:   triageAgent,
			Type:      "response",
			Content:   truncStr(result.Output, 2000),
			RefID:     handoffID,
		}, d.deps.NewID); err != nil {
			log.Warn("triage: failed to send response message", "handoffId", handoffID, "error", err)
		}
	}
	result.CostUSD += triageCost

	// Persist completion evidence immediately.
	if result.CostUSD > 0 || result.DurationMs > 0 || result.SessionID != "" {
		evidenceSQL := fmt.Sprintf(
			`UPDATE tasks SET cost_usd = %.6f, duration_ms = %d, session_id = '%s' WHERE id = '%s'`,
			result.CostUSD, result.DurationMs,
			db.Escape(result.SessionID), db.Escape(t.ID),
		)
		if err := db.Exec(d.engine.dbPath, evidenceSQL); err != nil {
			log.Warn("taskboard dispatch: failed to persist completion evidence", "id", t.ID, "error", err)
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

	if newStatus == "done" && strings.TrimSpace(result.Output) == "" {
		newStatus = "failed"
		d.engine.AddComment(t.ID, "system",
			"[auto-flag] Task completed with empty output. Marked failed for investigation.")
	}

	// Review gate.
	if newStatus == "done" && !qaApproved && t.Status != "review" {
		reviewer := d.engine.config.AutoDispatch.ReviewAgent
		if reviewer == "" {
			reviewer = "ruri"
		}
		log.Info("taskboard dispatch: auto-review starting", "id", t.ID, "reviewer", reviewer)
		d.engine.AddComment(t.ID, "system", fmt.Sprintf("[auto-review] %s reviewing output...", reviewer))

		rv := d.thoroughReview(ctx, prompt, result.Output, t.Assignee, reviewer)
		result.CostUSD += rv.CostUSD

		switch rv.Verdict {
		case reviewApprove:
			log.Info("taskboard dispatch: auto-review approved", "id", t.ID, "comment", rv.Comment)
			d.engine.AddComment(t.ID, reviewer, fmt.Sprintf("[review] Approved: %s", rv.Comment))

		case reviewFix:
			log.Info("taskboard dispatch: auto-review requests fix, retrying", "id", t.ID, "comment", rv.Comment)
			d.engine.AddComment(t.ID, reviewer, fmt.Sprintf("[review] Fix required: %s", rv.Comment))

			maxRetries := d.engine.config.MaxRetriesOrDefault()
			if t.RetryCount < maxRetries && d.deps.Executor != nil {
				t.RetryCount++
				d.engine.UpdateTask(t.ID, map[string]any{"retryCount": t.RetryCount})
				d.engine.AddComment(t.ID, "system",
					fmt.Sprintf("[auto-review] Sending back to %s for fix (attempt %d/%d)", t.Assignee, t.RetryCount, maxRetries))

				// Intentionally uses direct Executor (not workflow) for review-fix retries.
				// The retry is a targeted fix — re-running the full workflow would be wasteful.
				retryPrompt := prompt + "\n\n## Review Feedback (MUST FIX)\n\n" + rv.Comment +
					"\n\nFix ALL issues listed above. Do not skip any."
				task.Prompt = retryPrompt
				retryResult := d.deps.Executor.RunTask(ctx, task, t.Assignee)
				result.CostUSD += retryResult.CostUSD
				result.Output = retryResult.Output
				result.DurationMs += retryResult.DurationMs

				if retryResult.Status == "success" && strings.TrimSpace(retryResult.Output) != "" {
					rv2 := d.thoroughReview(ctx, prompt, retryResult.Output, t.Assignee, reviewer)
					result.CostUSD += rv2.CostUSD
					if rv2.Verdict == reviewApprove {
						d.engine.AddComment(t.ID, reviewer, fmt.Sprintf("[review] Approved after fix: %s", rv2.Comment))
					} else if rv2.Verdict == reviewEscalate {
						d.engine.AddComment(t.ID, reviewer, fmt.Sprintf("[review] Escalating: %s", rv2.Comment))
						newStatus = "review"
						t.Assignee = d.resolveEscalateAssignee()
					} else {
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
			log.Info("taskboard dispatch: auto-review escalating to user", "id", t.ID, "comment", rv.Comment)
			d.engine.AddComment(t.ID, reviewer, fmt.Sprintf("[review] Needs human judgment: %s", rv.Comment))
			newStatus = "review"
			t.Assignee = d.resolveEscalateAssignee()
		}
	}

	// Atomic status + cost update.
	nowISO := time.Now().UTC().Format(time.RFC3339)
	completedAtClause := "completed_at" // preserve existing value
	if newStatus == "done" {
		completedAtClause = fmt.Sprintf("'%s'", db.Escape(nowISO))
	}
	combinedSQL := fmt.Sprintf(`
		UPDATE tasks SET status = '%s', assignee = '%s', cost_usd = %.6f, duration_ms = %d,
		session_id = '%s', updated_at = '%s', completed_at = %s
		WHERE id = '%s'
	`,
		db.Escape(newStatus),
		db.Escape(t.Assignee),
		result.CostUSD,
		result.DurationMs,
		db.Escape(result.SessionID),
		db.Escape(nowISO),
		completedAtClause,
		db.Escape(t.ID),
	)
	if err := db.Exec(d.engine.dbPath, combinedSQL); err != nil {
		log.Error("taskboard dispatch: failed to update task status+cost", "id", t.ID, "error", err)
		time.Sleep(100 * time.Millisecond)
		if err2 := db.Exec(d.engine.dbPath, combinedSQL); err2 != nil {
			log.Error("taskboard dispatch: SQL retry also failed", "id", t.ID, "error", err2)
			if _, ferr := d.engine.MoveTask(t.ID, "todo"); ferr != nil {
				log.Error("taskboard dispatch: fallback MoveTask to todo failed", "id", t.ID, "error", ferr)
			} else {
				log.Warn("taskboard dispatch: task moved back to todo after persistent SQL failure", "id", t.ID)
			}
		} else {
			updatedTask := t
			updatedTask.Status = newStatus
			updatedTask.UpdatedAt = nowISO
			if newStatus == "done" {
				updatedTask.CompletedAt = nowISO
			}
			go d.engine.FireWebhook("task.moved", updatedTask)
		}
	} else {
		updatedTask := t
		updatedTask.Status = newStatus
		updatedTask.UpdatedAt = nowISO
		if newStatus == "done" {
			updatedTask.CompletedAt = nowISO
		}
		go d.engine.FireWebhook("task.moved", updatedTask)
	}

	// Record to job_runs.
	if d.deps.RecordHistory != nil {
		d.deps.RecordHistory(d.cfg.HistoryDB, task.ID, task.Name, task.Source, t.Assignee, task, result,
			start.Format(time.RFC3339), time.Now().Format(time.RFC3339), result.OutputFile)
	}

	// Post-task workspace git.
	d.postTaskWorkspaceGit(t)

	// Post-task worktree merge/cleanup.
	d.postTaskWorktree(t, projectWorkdir, worktreeDir, newStatus)

	// Post-task problem scan.
	d.postTaskProblemScan(t, result.Output, newStatus)

	if newStatus == "done" || newStatus == "review" {
		d.checkParentRollup(t.ID)
		d.promoteUnblockedTasks(t.ID)

		if !usedWorkflow && worktreeDir == "" {
			d.postTaskGit(t)
		}

		output := result.Output
		if len(output) > 2000 {
			output = output[:2000] + "\n... (truncated)"
		}
		comment := fmt.Sprintf("Task completed in %s (cost: $%.4f)\n\n%s", duration.Round(time.Second), result.CostUSD, output)
		if _, err := d.engine.AddComment(t.ID, t.Assignee, comment); err != nil {
			log.Warn("taskboard dispatch: failed to add completion comment", "id", t.ID, "error", err)
		}

		// Check for auto-delegations.
		if d.deps.Delegations != nil {
			delegations := d.deps.Delegations.Parse(result.Output)
			if len(delegations) > 0 {
				d.deps.Delegations.Process(ctx, delegations, result.Output, t.Assignee)
			}
		}

		log.Info("taskboard dispatch: task completed", "id", t.ID, "cost", result.CostUSD, "duration", duration.Round(time.Second))
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
			log.Warn("taskboard dispatch: failed to add failure comment", "id", t.ID, "error", err)
		}

		log.Warn("taskboard dispatch: task failed", "id", t.ID, "error", result.Error)
		d.postTaskSkillFailures(t, task, result.Error)
		d.engine.AutoRetryFailed()
	}
}

// =============================================================================
// Section: runTaskWithWorkflow
// =============================================================================

func (d *Dispatcher) runTaskWithWorkflow(ctx context.Context, t TaskBoard, task dispatch.Task, workflowName string) dispatch.TaskResult {
	if d.deps.Workflows == nil {
		log.Warn("runTaskWithWorkflow: no workflow runner, falling back to single dispatch",
			"task", t.ID, "workflow", workflowName)
		if d.deps.Executor != nil {
			return d.deps.Executor.RunTask(ctx, task, t.Assignee)
		}
		return dispatch.TaskResult{Status: "error", Error: "no executor configured"}
	}

	vars := map[string]string{
		"taskId":          t.ID,
		"taskTitle":       t.Title,
		"taskDescription": t.Description,
		"agent":           t.Assignee,
		"_taskId":         t.ID,
	}

	log.Info("runTaskWithWorkflow: starting workflow pipeline",
		"task", t.ID, "workflow", workflowName)

	if task.OnStart != nil {
		task.OnStart()
	}

	var run *WorkflowRunResult
	if t.WorkflowRunID != "" {
		prevRun, prevErr := d.deps.Workflows.QueryRun(d.cfg.HistoryDB, t.WorkflowRunID)
		if prevErr == nil && prevRun.IsResumable() && prevRun.WorkflowName == workflowName {
			log.Info("runTaskWithWorkflow: resuming previous run",
				"task", t.ID, "prevRunID", t.WorkflowRunID[:8])
			resumedRun, resumeErr := d.deps.Workflows.Resume(ctx, t.WorkflowRunID)
			if resumeErr == nil {
				run = &resumedRun
			} else {
				log.Warn("runTaskWithWorkflow: resume failed, starting fresh", "error", resumeErr)
			}
		} else if prevErr == nil && prevRun.WorkflowName != workflowName {
			log.Info("runTaskWithWorkflow: workflow changed, starting fresh",
				"task", t.ID, "prevWorkflow", prevRun.WorkflowName, "newWorkflow", workflowName)
		}
	}

	if run == nil {
		r, err := d.deps.Workflows.Execute(ctx, workflowName, vars)
		if err != nil {
			log.Warn("runTaskWithWorkflow: execute failed, falling back to single dispatch",
				"task", t.ID, "workflow", workflowName, "error", err)
			if d.deps.Executor != nil {
				return d.deps.Executor.RunTask(ctx, task, t.Assignee)
			}
			return dispatch.TaskResult{Status: "error", Error: err.Error()}
		}
		run = &r
	}

	// Persist the workflow run ID.
	d.engine.UpdateTask(t.ID, map[string]any{"workflowRunId": run.ID})

	result := dispatch.TaskResult{
		CostUSD:    run.TotalCost,
		DurationMs: run.DurationMs,
	}

	// Extract SessionID from the last step that has one.
	for i := len(run.StepOrder) - 1; i >= 0; i-- {
		if sid := run.StepSessions[run.StepOrder[i]]; sid != "" {
			result.SessionID = sid
			break
		}
	}

	if run.Status == "success" {
		result.Status = "success"
		var parts []string
		var lastOutput string
		for _, stepID := range run.StepOrder {
			if out := run.StepOutputs[stepID]; out != "" {
				parts = append(parts, fmt.Sprintf("## Step: %s\n%s", stepID, out))
				lastOutput = out
			}
		}
		if len(parts) > 1 {
			result.Output = strings.Join(parts, "\n\n---\n\n")
		} else {
			result.Output = lastOutput
		}
	} else {
		switch run.Status {
		case "timeout":
			result.Status = "timeout"
		case "cancelled":
			result.Status = "cancelled"
		default:
			result.Status = "error"
		}
		result.Error = run.Error
		for _, stepID := range run.StepOrder {
			if errMsg := run.StepErrors[stepID]; errMsg != "" {
				result.Output = fmt.Sprintf("[step:%s] %s", stepID, errMsg)
				break
			}
		}
	}

	log.Info("runTaskWithWorkflow: completed",
		"task", t.ID, "workflow", workflowName, "status", run.Status, "cost", run.TotalCost)
	return result
}

// =============================================================================
// Section: Dev↔QA Review Loop
// =============================================================================

func (d *Dispatcher) devQALoop(ctx context.Context, t TaskBoard, task dispatch.Task, usedWorkflow bool, workflowName string) devQALoopResult {
	maxRetries := d.engine.config.MaxRetriesOrDefault()

	reviewer := d.engine.config.AutoDispatch.ReviewAgent
	if reviewer == "" {
		reviewer = "ruri"
	}

	originalPrompt := task.Prompt
	var accumulated float64

	for attempt := 0; attempt <= maxRetries; attempt++ {
		var result dispatch.TaskResult
		if usedWorkflow {
			result = d.runTaskWithWorkflow(ctx, t, task, workflowName)
		} else if d.deps.Executor != nil {
			result = d.deps.Executor.RunTask(ctx, task, t.Assignee)
		}
		accumulated += result.CostUSD

		if result.Status != "success" {
			return devQALoopResult{Result: result, Attempts: attempt + 1, TotalCost: accumulated}
		}
		if strings.TrimSpace(result.Output) == "" {
			return devQALoopResult{Result: result, Attempts: attempt + 1, TotalCost: accumulated}
		}

		reviewOK, reviewComment, reviewCost := d.reviewTaskOutput(ctx, originalPrompt, result.Output, t.Assignee, reviewer)
		accumulated += reviewCost

		if reviewOK {
			log.Info("devQA: review passed", "task", t.ID, "attempt", attempt+1)
			d.engine.AddComment(t.ID, reviewer,
				fmt.Sprintf("[QA PASS] (attempt %d/%d) %s", attempt+1, maxRetries+1, reviewComment))
			return devQALoopResult{Result: result, QAApproved: true, Attempts: attempt + 1, TotalCost: accumulated}
		}

		log.Info("devQA: review failed, injecting feedback",
			"task", t.ID, "attempt", attempt+1, "maxAttempts", maxRetries+1)

		d.engine.AddComment(t.ID, reviewer,
			fmt.Sprintf("[QA FAIL] (attempt %d/%d) %s", attempt+1, maxRetries+1, reviewComment))

		qaFailMsg := fmt.Sprintf("[QA rejection attempt %d] %s", attempt+1, reviewComment)
		d.postTaskSkillFailures(t, task, qaFailMsg)

		if attempt == maxRetries {
			d.engine.AddComment(t.ID, "system",
				fmt.Sprintf("[ESCALATE] Dev↔QA loop exhausted (%d attempts). Escalating to human review.", maxRetries+1))
			log.Warn("devQA: max retries exhausted, escalating", "task", t.ID, "attempts", maxRetries+1)
			return devQALoopResult{Result: result, Attempts: attempt + 1, TotalCost: accumulated}
		}

		task.Prompt = originalPrompt

		if failureCtx := d.loadSkillFailureContext(task); failureCtx != "" {
			task.Prompt += "\n\n## Previous Failure Context\n"
			task.Prompt += failureCtx
		}

		task.Prompt += fmt.Sprintf("\n\n## QA Review Feedback (Attempt %d)\n", attempt+1)
		task.Prompt += "The QA reviewer rejected the output. Issues found:\n"
		task.Prompt += reviewComment
		task.Prompt += fmt.Sprintf("\n\nAddress ALL issues above. This is retry %d of %d.\n", attempt+2, maxRetries+1)

		if d.deps.NewID != nil {
			task.ID = d.deps.NewID()
			task.SessionID = d.deps.NewID()
		}
	}

	return devQALoopResult{}
}

func (d *Dispatcher) loadSkillFailureContext(task dispatch.Task) string {
	if d.deps.Skills == nil {
		return ""
	}
	skills := d.deps.Skills.SelectSkills(task)
	if len(skills) == 0 {
		return ""
	}

	var parts []string
	for _, s := range skills {
		failures := d.deps.Skills.LoadFailuresByName(s.Name)
		if failures == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("### Skill: %s\n%s", s.Name, failures))
	}
	if len(parts) == 0 {
		return ""
	}

	maxInject := 4096
	if d.deps.Skills != nil {
		maxInject = d.deps.Skills.MaxInject()
	}

	combined := strings.Join(parts, "\n\n")
	if len(combined) > maxInject {
		combined = combined[:maxInject] + "\n... (truncated)"
	}
	return combined
}

func (d *Dispatcher) reviewTaskOutput(ctx context.Context, originalPrompt, output, agentRole, reviewer string) (bool, string, float64) {
	r := d.thoroughReview(ctx, originalPrompt, output, agentRole, reviewer)
	return r.Verdict == reviewApprove, r.Comment, r.CostUSD
}

// =============================================================================
// Section: Triage (commander delegation)
// =============================================================================

// triageTask runs a lightweight LLM call to decide which agent should execute the task.
func (d *Dispatcher) triageTask(ctx context.Context, t TaskBoard, prompt string) *triageResult {
	truncate := func(s string, n int) string {
		if d.deps.Truncate != nil {
			return d.deps.Truncate(s, n)
		}
		if len(s) > n {
			return s[:n]
		}
		return s
	}

	// Build agent list.
	var agentList strings.Builder
	for name, ac := range d.cfg.Agents {
		fmt.Fprintf(&agentList, "- %s: %s\n", name, ac.Description)
	}

	triagePrompt := fmt.Sprintf(
		`You are the commander agent. Analyze this task and decide how to delegate it.

## Task
Title: %s
Description: %s
Type: %s
Priority: %s
Project: %s

## Available Agents
%s
Reply with ONLY a JSON object:
{"targetAgent":"agent_name","workflow":"","instruction":"Refined instructions for the executor..."}

- targetAgent: which agent should execute (must be from the list above)
- workflow: optional workflow override (empty = use default routing)
- instruction: briefing for the executor — clarify approach, priorities, gotchas`,
		t.Title,
		truncate(t.Description, 2000),
		t.Type,
		t.Priority,
		t.Project,
		agentList.String(),
	)

	budget := d.engine.config.AutoDispatch.TriageBudgetOrDefault()
	task := dispatch.Task{
		Prompt:  triagePrompt,
		Timeout: "30s",
		Budget:  budget,
		Source:  "triage",
	}
	if d.deps.FillDefaults != nil {
		d.deps.FillDefaults(d.cfg, &task)
	}
	task.Model = "sonnet"
	task.PermissionMode = "plan"

	// Load commander SOUL.
	triageAgent := d.engine.config.AutoDispatch.DefaultAgent
	if triageAgent == "" {
		triageAgent = "ruri"
	}
	if d.deps.LoadAgentPrompt != nil {
		if soulPrompt, err := d.deps.LoadAgentPrompt(d.cfg, triageAgent); err == nil && soulPrompt != "" {
			task.SystemPrompt = soulPrompt
		}
	}

	result := d.deps.Executor.RunTask(ctx, task, triageAgent)
	if result.Status != "success" {
		log.Warn("triage: execution failed, falling back to direct dispatch", "task", t.ID, "error", result.Output)
		return nil
	}

	// Parse JSON from output.
	start := strings.Index(result.Output, "{")
	end := strings.LastIndex(result.Output, "}")
	if start < 0 || end <= start {
		log.Warn("triage: no JSON in output, falling back", "task", t.ID)
		return nil
	}

	var tr triageResult
	if err := json.Unmarshal([]byte(result.Output[start:end+1]), &tr); err != nil {
		log.Warn("triage: JSON parse failed, falling back", "task", t.ID, "error", err)
		return nil
	}

	// Validate target agent exists.
	if _, ok := d.cfg.Agents[tr.TargetAgent]; !ok {
		log.Warn("triage: unknown target agent, falling back", "task", t.ID, "agent", tr.TargetAgent)
		return nil
	}

	tr.CostUSD = result.CostUSD
	return &tr
}

// recordTriageHandoff writes the handoff record and agent message for a triage delegation.
func (d *Dispatcher) recordTriageHandoff(t TaskBoard, tr *triageResult, triageAgent, prompt string) string {
	id := ""
	if d.deps.NewID != nil {
		id = d.deps.NewID()
	}
	if id == "" {
		id = fmt.Sprintf("ho-%s-%d", t.ID, time.Now().UnixMilli())
	}

	_ = handoff.RecordHandoff(d.cfg.HistoryDB, handoff.Handoff{
		ID:          id,
		FromAgent:   triageAgent,
		ToAgent:     tr.TargetAgent,
		Context:     prompt,
		Instruction: tr.Instruction,
		Status:      "pending",
	})

	_ = handoff.SendAgentMessage(d.cfg.HistoryDB, handoff.AgentMessage{
		FromAgent: triageAgent,
		ToAgent:   tr.TargetAgent,
		Type:      "handoff",
		Content:   tr.Instruction,
		RefID:     id,
	}, d.deps.NewID)

	return id
}

func (d *Dispatcher) thoroughReview(ctx context.Context, originalPrompt, output, agentRole, reviewer string) reviewResult {
	truncate := func(s string, n int) string {
		if d.deps.Truncate != nil {
			return d.deps.Truncate(s, n)
		}
		if len(s) > n {
			return s[:n]
		}
		return s
	}

	reviewPrompt := fmt.Sprintf(
		`You are a senior staff engineer conducting a thorough code review.

## Original Task
%s

## Agent (%s) Output
%s

## Review Checklist
Evaluate ALL of the following:

1. **Correctness**: Does the output fully address the original request? Any logical errors?
2. **Completeness**: Any TODO, placeholder, stub, or unfinished work left behind?
3. **Code Quality**: Redundant code? Copy-paste duplication? Poor naming? Over-engineering?
4. **Efficiency**: Unnecessary allocations, O(n²) where O(n) is possible, repeated work?
5. **Security**: SQL injection, XSS, command injection, hardcoded secrets, path traversal?
6. **Error Handling**: Missing error checks? Silent failures? Panics on edge cases?
7. **Breaking Changes**: Will this break existing functionality? Missing backwards compatibility?
8. **File Size**: Any single file growing beyond reasonable review size (>1500 lines)?

## Verdict Rules
- **approve**: Code is production-ready. Minor style nits are OK — don't block for cosmetics.
- **fix**: Issues found that the original agent CAN fix autonomously (bugs, missing error handling, code quality). Give specific, actionable feedback.
- **escalate**: ONLY use this when you genuinely cannot determine correctness — e.g., the spec is ambiguous, critical domain knowledge is missing, or the change could break production in ways you can't verify. Do NOT escalate for fixable code issues.

Reply with ONLY a JSON object:
{"verdict":"approve","comment":"brief summary"}
{"verdict":"fix","comment":"specific issues the agent must fix (be actionable)"}
{"verdict":"escalate","comment":"why human judgment is needed (be specific)"}`,
		truncate(originalPrompt, 2000),
		agentRole,
		truncate(output, 6000),
	)

	task := dispatch.Task{
		Prompt:  reviewPrompt,
		Timeout: "120s",
		Budget:  d.cfg.SmartDispatch.ReviewBudget,
		Source:  "auto-review",
	}
	if d.deps.FillDefaults != nil {
		d.deps.FillDefaults(d.cfg, &task)
	}
	task.Model = "sonnet"

	if d.deps.LoadAgentPrompt != nil {
		if soulPrompt, err := d.deps.LoadAgentPrompt(d.cfg, reviewer); err == nil && soulPrompt != "" {
			task.SystemPrompt = soulPrompt
		}
	}
	if rc, ok := d.cfg.Agents[reviewer]; ok {
		if rc.PermissionMode != "" {
			task.PermissionMode = rc.PermissionMode
		}
		if rc.Model == "opus" {
			task.Model = "opus"
		}
	}

	if d.deps.Executor == nil {
		return reviewResult{Verdict: reviewEscalate, Comment: "review skipped (no executor)"}
	}

	result := d.deps.Executor.RunTask(ctx, task, reviewer)
	if result.Status != "success" {
		return reviewResult{Verdict: reviewEscalate, Comment: "review skipped (execution error) — needs manual check", CostUSD: result.CostUSD}
	}

	// Parse review JSON.
	start := strings.Index(result.Output, "{")
	end := strings.LastIndex(result.Output, "}")
	if start >= 0 && end > start {
		var parsed struct {
			Verdict string `json:"verdict"`
			Comment string `json:"comment"`
			OK      bool   `json:"ok"`
		}
		if json.Unmarshal([]byte(result.Output[start:end+1]), &parsed) == nil {
			switch parsed.Verdict {
			case "approve":
				return reviewResult{Verdict: reviewApprove, Comment: parsed.Comment, CostUSD: result.CostUSD}
			case "fix":
				return reviewResult{Verdict: reviewFix, Comment: parsed.Comment, CostUSD: result.CostUSD}
			case "escalate":
				return reviewResult{Verdict: reviewEscalate, Comment: parsed.Comment, CostUSD: result.CostUSD}
			default:
				if parsed.OK {
					return reviewResult{Verdict: reviewApprove, Comment: parsed.Comment, CostUSD: result.CostUSD}
				}
				return reviewResult{Verdict: reviewFix, Comment: parsed.Comment, CostUSD: result.CostUSD}
			}
		}
	}

	return reviewResult{Verdict: reviewApprove, Comment: "review parse error", CostUSD: result.CostUSD}
}

// =============================================================================
// Section: Post-task Git Operations
// =============================================================================

func (d *Dispatcher) postTaskWorkspaceGit(t TaskBoard) {
	wsDir := d.cfg.WorkspaceDir
	if wsDir == "" {
		return
	}

	if err := exec.Command("git", "-C", wsDir, "rev-parse", "--git-dir").Run(); err != nil {
		return
	}

	d.cleanStaleLock(wsDir, t.ID)

	statusOut, err := exec.Command("git", "-C", wsDir, "status", "--porcelain").Output()
	if err != nil {
		log.Warn("postTaskWorkspaceGit: git status failed", "task", t.ID, "error", err)
		d.engine.AddComment(t.ID, "system", "[WARNING] workspace git status failed: "+err.Error())
		return
	}
	if len(bytes.TrimSpace(statusOut)) == 0 {
		return
	}

	if out, err := exec.Command("git", "-C", wsDir, "add", "-A").CombinedOutput(); err != nil {
		msg := fmt.Sprintf("[WARNING] workspace git add failed: %s", strings.TrimSpace(string(out)))
		log.Warn("postTaskWorkspaceGit: git add failed", "task", t.ID, "error", string(out))
		d.engine.AddComment(t.ID, "system", msg)
		if _, moveErr := d.engine.MoveTask(t.ID, "partial-done"); moveErr != nil {
			log.Warn("postTaskWorkspaceGit: failed to move to partial-done", "task", t.ID, "error", moveErr)
		}
		return
	}

	commitMsg := fmt.Sprintf("[%s] %s", t.ID, t.Title)
	if out, err := exec.Command("git", "-C", wsDir, "commit", "-m", commitMsg).CombinedOutput(); err != nil {
		msg := fmt.Sprintf("[WARNING] workspace git commit failed: %s", strings.TrimSpace(string(out)))
		log.Warn("postTaskWorkspaceGit: git commit failed", "task", t.ID, "error", string(out))
		d.engine.AddComment(t.ID, "system", msg)
		if _, moveErr := d.engine.MoveTask(t.ID, "partial-done"); moveErr != nil {
			log.Warn("postTaskWorkspaceGit: failed to move to partial-done", "task", t.ID, "error", moveErr)
		}
		return
	}

	log.Info("postTaskWorkspaceGit: committed workspace changes", "task", t.ID)
}

func (d *Dispatcher) postTaskGit(t TaskBoard) {
	if !d.engine.config.GitCommit {
		return
	}
	if t.Project == "" || t.Project == "default" {
		return
	}
	if t.Assignee == "" {
		return
	}
	if d.deps.GetProject == nil {
		return
	}

	p := d.deps.GetProject(d.cfg.HistoryDB, t.Project)
	if p == nil || p.Workdir == "" {
		return
	}
	workdir := p.Workdir

	if err := exec.Command("git", "-C", workdir, "rev-parse", "--git-dir").Run(); err != nil {
		return
	}

	statusOut, err := exec.Command("git", "-C", workdir, "status", "--porcelain").Output()
	if err != nil {
		log.Warn("postTaskGit: git status failed", "task", t.ID, "error", err)
		return
	}
	if len(bytes.TrimSpace(statusOut)) == 0 {
		log.Info("postTaskGit: no changes to commit", "task", t.ID, "project", t.Project)
		return
	}

	branch := ""
	if d.deps.BuildBranch != nil {
		branch = d.deps.BuildBranch(d.engine.config.GitWorkflow, t)
	}

	if out, err := exec.Command("git", "-C", workdir, "checkout", "-B", branch).CombinedOutput(); err != nil {
		msg := fmt.Sprintf("[post-task-git] checkout -B %s failed: %s", branch, strings.TrimSpace(string(out)))
		log.Warn("postTaskGit: checkout failed", "task", t.ID, "error", msg)
		d.engine.AddComment(t.ID, "system", msg)
		return
	}

	if out, err := exec.Command("git", "-C", workdir, "add", "-A").CombinedOutput(); err != nil {
		msg := fmt.Sprintf("[post-task-git] add -A failed: %s", strings.TrimSpace(string(out)))
		log.Warn("postTaskGit: add failed", "task", t.ID, "error", msg)
		d.engine.AddComment(t.ID, "system", msg)
		return
	}

	commitMsg := fmt.Sprintf("[%s] %s", t.ID, t.Title)
	if out, err := exec.Command("git", "-C", workdir, "commit", "-m", commitMsg).CombinedOutput(); err != nil {
		msg := fmt.Sprintf("[post-task-git] commit failed: %s", strings.TrimSpace(string(out)))
		log.Warn("postTaskGit: commit failed", "task", t.ID, "error", msg)
		d.engine.AddComment(t.ID, "system", msg)
		return
	}

	log.Info("postTaskGit: committed", "task", t.ID, "branch", branch)
	d.engine.AddComment(t.ID, "system", fmt.Sprintf("[post-task-git] Committed to branch %s", branch))

	baseBranch := DetectDefaultBranch(workdir)
	diffOut, _ := exec.Command("git", "-C", workdir, "diff", baseBranch+"..."+branch).Output()
	if diff := string(diffOut); diff != "" {
		if len(diff) > 100000 {
			diff = diff[:100000] + "\n... (truncated)"
		}
		d.engine.AddComment(t.ID, "system", diff, "diff")
	}

	if d.engine.config.GitPush {
		if out, err := exec.Command("git", "-C", workdir, "push", "-u", "origin", branch).CombinedOutput(); err != nil {
			msg := fmt.Sprintf("[post-task-git] push failed: %s", strings.TrimSpace(string(out)))
			log.Warn("postTaskGit: push failed", "task", t.ID, "error", msg)
			d.engine.AddComment(t.ID, "system", msg)
			return
		}
		log.Info("postTaskGit: pushed", "task", t.ID, "branch", branch)
		d.engine.AddComment(t.ID, "system", fmt.Sprintf("[post-task-git] Pushed to origin/%s", branch))

		if d.engine.config.GitPR {
			d.postTaskGitPR(t, workdir, branch)
		}
	}
}

func (d *Dispatcher) postTaskWorktree(t TaskBoard, projectWorkdir, worktreeDir, newStatus string) {
	if worktreeDir == "" || projectWorkdir == "" || d.deps.Worktrees == nil {
		return
	}

	mergeOK := false
	defer func() {
		if mergeOK {
			if err := d.deps.Worktrees.Remove(projectWorkdir, worktreeDir); err != nil {
				log.Warn("worktree: cleanup failed", "task", t.ID, "path", worktreeDir, "error", err)
				d.engine.AddComment(t.ID, "system",
					fmt.Sprintf("[worktree] Cleanup failed: %v", err))
			} else {
				log.Info("worktree: cleaned up", "task", t.ID, "path", worktreeDir)
			}
		} else if newStatus == "done" || newStatus == "review" {
			log.Warn("worktree: preserved for recovery", "task", t.ID, "path", worktreeDir)
		}
	}()

	switch newStatus {
	case "done", "review":
		commitCount := d.deps.Worktrees.CommitCount(worktreeDir)
		hasChanges := d.deps.Worktrees.HasChanges(worktreeDir)

		if commitCount == 0 && !hasChanges {
			mergeOK = true
			d.engine.AddComment(t.ID, "system", "[worktree] No changes committed. Worktree discarded.")
			return
		}

		d.captureTaskDiff(t, projectWorkdir, worktreeDir)

		commitMsg := fmt.Sprintf("[%s] %s", t.ID, t.Title)
		diffSummary, err := d.deps.Worktrees.Merge(projectWorkdir, worktreeDir, commitMsg)
		if err != nil {
			log.Warn("worktree: merge failed", "task", t.ID, "error", err)
			d.engine.AddComment(t.ID, "system",
				fmt.Sprintf("[worktree] ⚠️ Merge failed: %v\nBranch preserved: task/%s\nWorktree preserved: %s\nRecover manually: git -C %s merge task/%s",
					err, t.ID, worktreeDir, projectWorkdir, t.ID))
			if _, moveErr := d.engine.MoveTask(t.ID, "partial-done"); moveErr != nil {
				log.Warn("worktree: failed to move to partial-done", "task", t.ID, "error", moveErr)
			}
			return
		}

		mergeOK = true
		comment := "[worktree] Changes merged into main."
		if diffSummary != "" {
			comment += "\n```\n" + diffSummary + "\n```"
		}
		d.engine.AddComment(t.ID, "system", comment)
		log.Info("worktree: merge complete", "task", t.ID)

	default: // failed, cancelled
		mergeOK = true
		d.engine.AddComment(t.ID, "system", "[worktree] Task failed — worktree discarded without merge.")
	}
}

func (d *Dispatcher) captureTaskDiff(t TaskBoard, repoDir, wtDir string) string {
	if wtDir == "" {
		return ""
	}
	taskID := filepath.Base(wtDir)
	branch := "task/" + taskID
	baseBranch := DetectDefaultBranch(repoDir)

	mergeBase, err := exec.Command("git", "-C", wtDir, "merge-base", baseBranch, branch).Output()
	if err != nil {
		return ""
	}
	base := strings.TrimSpace(string(mergeBase))

	diffOut, err := exec.Command("git", "-C", wtDir, "diff", base+"..."+branch).Output()
	if err != nil {
		return ""
	}

	diff := string(diffOut)
	if len(diff) > 100000 {
		diff = diff[:100000] + "\n... (truncated, diff too large)"
	}

	if diff != "" {
		d.engine.AddComment(t.ID, "system", diff, "diff")
	}
	return diff
}

// =============================================================================
// Section: PR/MR creation
// =============================================================================

// prDescSem limits concurrent PR/MR description generation LLM calls.
var prDescSem = make(chan struct{}, 2)

func detectRemoteHost(workdir string) string {
	out, err := exec.Command("git", "-C", workdir, "remote", "get-url", "origin").Output()
	if err != nil {
		return "unknown"
	}
	url := strings.ToLower(strings.TrimSpace(string(out)))
	switch {
	case strings.Contains(url, "github.com"):
		return "github"
	case strings.Contains(url, "gitlab"):
		return "gitlab"
	default:
		return "unknown"
	}
}

func (d *Dispatcher) postTaskGitPR(t TaskBoard, workdir, branch string) {
	host := detectRemoteHost(workdir)
	switch host {
	case "github":
		d.postTaskGitHubPR(t, workdir, branch)
	case "gitlab":
		d.postTaskGitLabMR(t, workdir, branch)
	default:
		log.Warn("postTaskGitPR: remote host not recognized, skipping PR/MR creation", "task", t.ID)
		d.engine.AddComment(t.ID, "system", "[post-task-git] Remote host not recognized (not GitHub or GitLab). Skipping PR/MR creation.")
	}
}

func (d *Dispatcher) postTaskGitHubPR(t TaskBoard, workdir, branch string) {
	baseBranch := DetectDefaultBranch(workdir)

	prViewCmd := exec.Command("gh", "pr", "view", branch, "--json", "url", "-q", ".url")
	prViewCmd.Dir = workdir
	existingPR, _ := prViewCmd.Output()
	if url := strings.TrimSpace(string(existingPR)); url != "" {
		log.Info("postTaskGitHubPR: PR already exists", "task", t.ID, "url", url)
		d.engine.AddComment(t.ID, "system", fmt.Sprintf("[post-task-git] PR already exists: %s", url))
		return
	}

	diffOut, err := exec.Command("git", "-C", workdir, "diff", baseBranch+"..."+branch, "--stat").Output()
	if err != nil {
		log.Warn("postTaskGitHubPR: diff stat failed", "task", t.ID, "error", err)
	}
	diffDetail, _ := exec.Command("git", "-C", workdir, "diff", baseBranch+"..."+branch).Output()
	logOut, _ := exec.Command("git", "-C", workdir, "log", baseBranch+".."+branch, "--oneline").Output()

	title, body := d.generatePRDescription(t, string(diffOut), string(diffDetail), string(logOut))

	args := []string{"pr", "create",
		"--head", branch,
		"--base", baseBranch,
		"--title", title,
		"--body", body,
	}
	cmd := exec.Command("gh", args...)
	cmd.Dir = workdir
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := fmt.Sprintf("[post-task-git] PR creation failed: %s", strings.TrimSpace(string(out)))
		log.Warn("postTaskGitHubPR: gh pr create failed", "task", t.ID, "error", msg)
		d.engine.AddComment(t.ID, "system", msg)
		return
	}

	prURL := strings.TrimSpace(string(out))
	log.Info("postTaskGitHubPR: PR created", "task", t.ID, "url", prURL)
	d.engine.AddComment(t.ID, "system", fmt.Sprintf("[post-task-git] PR created: %s", prURL))
}

func (d *Dispatcher) postTaskGitLabMR(t TaskBoard, workdir, branch string) {
	baseBranch := DetectDefaultBranch(workdir)

	mrViewCmd := exec.Command("glab", "mr", "view", branch)
	mrViewCmd.Dir = workdir
	mrViewOut, mrViewErr := mrViewCmd.Output()
	if mrViewErr == nil && len(strings.TrimSpace(string(mrViewOut))) > 0 {
		url := ""
		for _, line := range strings.Split(string(mrViewOut), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "https://") {
				url = strings.TrimSpace(line)
				break
			}
		}
		msg := "[post-task-git] MR already exists"
		if url != "" {
			msg += ": " + url
		}
		log.Info("postTaskGitLabMR: MR already exists", "task", t.ID, "url", url)
		d.engine.AddComment(t.ID, "system", msg)
		return
	}

	diffOut, err := exec.Command("git", "-C", workdir, "diff", baseBranch+"..."+branch, "--stat").Output()
	if err != nil {
		log.Warn("postTaskGitLabMR: diff stat failed", "task", t.ID, "error", err)
	}
	diffDetail, _ := exec.Command("git", "-C", workdir, "diff", baseBranch+"..."+branch).Output()
	logOut, _ := exec.Command("git", "-C", workdir, "log", baseBranch+".."+branch, "--oneline").Output()

	title, body := d.generatePRDescription(t, string(diffOut), string(diffDetail), string(logOut))

	args := []string{"mr", "create",
		"--head", branch,
		"--base", baseBranch,
		"--title", title,
		"--description", body,
		"--yes",
	}
	cmd := exec.Command("glab", args...)
	cmd.Dir = workdir
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := fmt.Sprintf("[post-task-git] MR creation failed: %s", strings.TrimSpace(string(out)))
		log.Warn("postTaskGitLabMR: glab mr create failed", "task", t.ID, "error", msg)
		d.engine.AddComment(t.ID, "system", msg)
		return
	}

	mrURL := strings.TrimSpace(string(out))
	log.Info("postTaskGitLabMR: MR created", "task", t.ID, "url", mrURL)
	d.engine.AddComment(t.ID, "system", fmt.Sprintf("[post-task-git] MR created: %s", mrURL))
}

func (d *Dispatcher) generatePRDescription(t TaskBoard, diffStat, diffDetail, commitLog string) (title, body string) {
	truncate := func(s string, n int) string {
		if d.deps.Truncate != nil {
			return d.deps.Truncate(s, n)
		}
		if len(s) > n {
			return s[:n]
		}
		return s
	}

	if len(diffDetail) > 6000 {
		diffDetail = diffDetail[:6000] + "\n... (truncated)"
	}

	prompt := fmt.Sprintf(`Generate a Pull Request / Merge Request title and description for the following changes.

Task: %s
Description: %s

Commits:
%s

Diff summary:
%s

Diff detail:
%s

Respond with a JSON object:
{"title": "short PR title (under 70 chars)", "body": "markdown PR description with ## Summary section (2-4 bullet points) and ## Changes section"}

Rules:
- Title should be concise and describe the change (not the task ID)
- Body should explain what changed and why
- Use markdown formatting in body
- Keep it professional and clear`,
		truncate(t.Title, 200),
		truncate(t.Description, 500),
		truncate(commitLog, 500),
		truncate(diffStat, 1000),
		diffDetail)

	task := dispatch.Task{
		Prompt:         prompt,
		Model:          "haiku",
		Budget:         0.05,
		Timeout:        "30s",
		PermissionMode: "plan",
		Source:         "pr-description",
	}
	if d.deps.FillDefaults != nil {
		d.deps.FillDefaults(d.cfg, &task)
	}
	if d.deps.NewID != nil {
		task.ID = d.deps.NewID()
		task.Name = "pr-desc-" + t.ID
	}
	task.Model = "haiku"
	task.Budget = 0.05

	if d.deps.Executor == nil {
		return fmt.Sprintf("[%s] %s", t.ID, t.Title),
			fmt.Sprintf("## Summary\n- %s\n\nAuto-generated by Tetora task %s", t.Title, t.ID)
	}

	result := d.deps.Executor.RunTask(d.ctx, task, "")
	if result.Status != "success" || strings.TrimSpace(result.Output) == "" {
		return fmt.Sprintf("[%s] %s", t.ID, t.Title),
			fmt.Sprintf("## Summary\n- %s\n\nAuto-generated by Tetora task %s", t.Title, t.ID)
	}

	raw := result.Output
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end <= start {
		return fmt.Sprintf("[%s] %s", t.ID, t.Title),
			fmt.Sprintf("## Summary\n- %s\n\nAuto-generated by Tetora task %s", t.Title, t.ID)
	}

	var pr struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := json.Unmarshal([]byte(raw[start:end+1]), &pr); err != nil || pr.Title == "" {
		return fmt.Sprintf("[%s] %s", t.ID, t.Title),
			fmt.Sprintf("## Summary\n- %s\n\nAuto-generated by Tetora task %s", t.Title, t.ID)
	}

	pr.Body += fmt.Sprintf("\n\n---\nTask: `%s` — %s", t.ID, t.Title)
	return pr.Title, pr.Body
}

// =============================================================================
// Section: Utility functions
// =============================================================================

// DetectDefaultBranch returns the default branch name (main or master) for a repo.
func DetectDefaultBranch(workdir string) string {
	out, err := exec.Command("git", "-C", workdir, "symbolic-ref", "refs/remotes/origin/HEAD").Output()
	if err == nil {
		ref := strings.TrimSpace(string(out))
		if parts := strings.Split(ref, "/"); len(parts) > 0 {
			return parts[len(parts)-1]
		}
	}
	if exec.Command("git", "-C", workdir, "rev-parse", "--verify", "main").Run() == nil {
		return "main"
	}
	return "master"
}

// cleanStaleLock removes stale .git/index.lock files that are older than 1 hour.
func (d *Dispatcher) cleanStaleLock(repoDir, taskID string) {
	lockPath := filepath.Join(repoDir, ".git", "index.lock")
	info, err := os.Stat(lockPath)
	if err != nil {
		return
	}

	age := time.Since(info.ModTime())
	if age < time.Hour {
		log.Warn("cleanStaleLock: index.lock exists but is recent, skipping",
			"task", taskID, "path", lockPath, "age", age.Round(time.Second))
		d.engine.AddComment(taskID, "system",
			fmt.Sprintf("[WARNING] git index.lock exists (age: %s). Waiting for other git operation to finish.", age.Round(time.Second)))
		return
	}

	if err := os.Remove(lockPath); err != nil {
		log.Warn("cleanStaleLock: failed to remove stale lock", "task", taskID, "path", lockPath, "error", err)
		return
	}

	log.Info("cleanStaleLock: removed stale index.lock", "task", taskID, "path", lockPath, "age", age.Round(time.Second))
	d.engine.AddComment(taskID, "system",
		fmt.Sprintf("[auto-fix] Removed stale git index.lock (age: %s)", age.Round(time.Second)))
}

// =============================================================================
// Section: LLM helpers (timeout estimation, idle analysis, problem scan)
// =============================================================================

// estimateTimeoutSem is a dedicated semaphore for timeout estimation LLM calls.
var estimateTimeoutSem = make(chan struct{}, 3)

func (d *Dispatcher) estimateTimeoutLLM(ctx context.Context, prompt string) string {
	if d.deps.Executor == nil {
		return ""
	}

	truncate := func(s string, n int) string {
		if d.deps.Truncate != nil {
			return d.deps.Truncate(s, n)
		}
		if len(s) > n {
			return s[:n]
		}
		return s
	}

	estPrompt := fmt.Sprintf(`Estimate how long an AI coding agent will need to complete this task. Consider the complexity, number of files likely involved, and whether it requires research/analysis.

Task:
%s

Reply with ONLY a single integer: the estimated minutes needed. Examples:
- Simple bug fix or config change: 15
- Moderate feature or multi-file fix: 45
- Large feature, refactor, or codebase analysis: 120
- Major rewrite or multi-project task: 180

Minutes:`, truncate(prompt, 2000))

	task := dispatch.Task{
		Prompt:         estPrompt,
		Model:          "haiku",
		Budget:         0.02,
		Timeout:        "15s",
		PermissionMode: "plan",
		Source:         "timeout-estimate",
	}
	if d.deps.FillDefaults != nil {
		d.deps.FillDefaults(d.cfg, &task)
	}
	if d.deps.NewID != nil {
		task.ID = d.deps.NewID()
		task.Name = "timeout-estimate"
	}
	task.Model = "haiku"
	task.Budget = 0.02

	result := d.deps.Executor.RunTask(ctx, task, "")
	if result.Status != "success" || result.Output == "" {
		return ""
	}

	cleaned := strings.TrimSpace(result.Output)
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

// idleAnalysisSem limits concurrent idle-analysis LLM calls.
var idleAnalysisSem = make(chan struct{}, 1)

func (d *Dispatcher) idleAnalysis() {
	if !d.engine.config.IdleAnalyze {
		return
	}

	for _, status := range []string{"doing", "review"} {
		tasks, err := d.engine.ListTasks(status, "", "")
		if err != nil || len(tasks) > 0 {
			return
		}
	}

	cutoff7d := time.Now().Add(-7 * 24 * time.Hour).UTC().Format(time.RFC3339)
	projectSQL := fmt.Sprintf(`
		SELECT DISTINCT project FROM tasks
		WHERE status IN ('done','failed')
		AND completed_at > '%s'
		AND project != '' AND project != 'default'
		LIMIT 3
	`, db.Escape(cutoff7d))
	projectRows, err := db.Query(d.engine.dbPath, projectSQL)
	if err != nil || len(projectRows) == 0 {
		return
	}

	cutoff24h := time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339)

	for _, row := range projectRows {
		projectID := fmt.Sprintf("%v", row["project"])

		cooldownSQL := fmt.Sprintf(`
			SELECT COUNT(*) as cnt FROM task_comments
			WHERE content LIKE '%%[idle-analysis]%%'
			AND created_at > '%s'
			AND task_id IN (SELECT id FROM tasks WHERE project = '%s')
		`, db.Escape(cutoff24h), db.Escape(projectID))
		cooldownRows, err := db.Query(d.engine.dbPath, cooldownSQL)
		if err == nil && len(cooldownRows) > 0 && getFloat64(cooldownRows[0], "cnt") > 0 {
			log.Debug("idleAnalysis: 24h cooldown active", "project", projectID)
			continue
		}

		d.runIdleAnalysisForProject(projectID)
	}
}

func (d *Dispatcher) runIdleAnalysisForProject(projectID string) {
	if d.deps.Executor == nil {
		return
	}

	recentSQL := fmt.Sprintf(`
		SELECT id, title, status FROM tasks
		WHERE project = '%s' AND status IN ('done','failed')
		ORDER BY completed_at DESC LIMIT 10
	`, db.Escape(projectID))
	recentRows, err := db.Query(d.engine.dbPath, recentSQL)
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

	projectName := projectID
	if d.deps.GetProject != nil {
		if p := d.deps.GetProject(d.cfg.HistoryDB, projectID); p != nil {
			if p.Name != "" {
				projectName = p.Name
			}
			if p.Workdir != "" {
				if gitOut, err := exec.Command("git", "-C", p.Workdir, "log", "--oneline", "-20").Output(); err == nil {
					sb.WriteString("\nRecent git activity:\n")
					sb.WriteString(string(gitOut))
				}
			}
		}
	}

	prompt := fmt.Sprintf(`Based on the completed tasks and recent git activity for project "%s", identify 1-3 logical next tasks.

%s

Output ONLY a JSON array of objects with keys: title, description, priority (low/normal/high).
Example: [{"title":"...","description":"...","priority":"normal"}]`, projectName, sb.String())

	task := dispatch.Task{
		Prompt:         prompt,
		Model:          "haiku",
		Budget:         0.10,
		Timeout:        "30s",
		PermissionMode: "plan",
		Source:         "idle-analysis",
	}
	if d.deps.FillDefaults != nil {
		d.deps.FillDefaults(d.cfg, &task)
	}
	if d.deps.NewID != nil {
		task.ID = d.deps.NewID()
		task.Name = "idle-analysis-" + projectID
	}
	task.Model = "haiku"
	task.Budget = 0.10

	log.Info("idleAnalysis: analyzing project", "project", projectID)
	result := d.deps.Executor.RunTask(d.ctx, task, "")
	if result.Status != "success" || strings.TrimSpace(result.Output) == "" {
		log.Warn("idleAnalysis: LLM call failed", "project", projectID, "error", result.Error)
		return
	}

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
		log.Warn("idleAnalysis: no JSON array in output", "project", projectID)
		return
	}
	if err := json.Unmarshal([]byte(output[start:end+1]), &suggestions); err != nil {
		log.Warn("idleAnalysis: JSON parse failed", "project", projectID, "error", err)
		return
	}

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
			log.Warn("idleAnalysis: failed to create task", "project", projectID, "title", s.Title, "error", err)
			continue
		}
		d.engine.AddComment(newTask.ID, "system", "[idle-analysis] Auto-generated from project analysis")
		created++
	}

	log.Info("idleAnalysis: created backlog tasks", "project", projectID, "count", created)
}

// problemScanSem limits concurrent problem-scan LLM calls.
var problemScanSem = make(chan struct{}, 2)

func (d *Dispatcher) postTaskProblemScan(t TaskBoard, output string, newStatus string) {
	if !d.engine.config.ProblemScan {
		return
	}
	if strings.TrimSpace(output) == "" {
		return
	}
	if d.deps.Executor == nil {
		return
	}

	truncate := func(s string, n int) string {
		if d.deps.Truncate != nil {
			return d.deps.Truncate(s, n)
		}
		if len(s) > n {
			return s[:n]
		}
		return s
	}

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

If no problems found, respond with exactly: {"problems": [], "followup": []}`, truncate(t.Title, 200), newStatus, scanInput)

	task := dispatch.Task{
		Prompt:         prompt,
		Model:          "haiku",
		Budget:         0.05,
		Timeout:        "30s",
		PermissionMode: "plan",
		Source:         "problem-scan",
	}
	if d.deps.FillDefaults != nil {
		d.deps.FillDefaults(d.cfg, &task)
	}
	if d.deps.NewID != nil {
		task.ID = d.deps.NewID()
		task.Name = "problem-scan-" + t.ID
	}
	task.Model = "haiku"
	task.Budget = 0.05

	result := d.deps.Executor.RunTask(d.ctx, task, "")
	if result.Status != "success" || strings.TrimSpace(result.Output) == "" {
		log.Debug("postTaskProblemScan: LLM call failed or empty", "task", t.ID, "error", result.Error)
		return
	}

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
		log.Debug("postTaskProblemScan: no JSON in output", "task", t.ID)
		return
	}

	var sr scanResult
	if err := json.Unmarshal([]byte(raw[start:end+1]), &sr); err != nil {
		log.Debug("postTaskProblemScan: JSON parse failed", "task", t.ID, "error", err)
		return
	}

	if len(sr.Problems) == 0 && len(sr.Followup) == 0 {
		log.Debug("postTaskProblemScan: no issues found", "task", t.ID)
		return
	}

	var commentSb strings.Builder
	commentSb.WriteString("[problem-scan] Potential issues detected:\n")
	for _, p := range sr.Problems {
		commentSb.WriteString(fmt.Sprintf("- [%s] %s: %s\n", p.Severity, p.Summary, p.Detail))
	}

	if _, err := d.engine.AddComment(t.ID, "system", commentSb.String()); err != nil {
		log.Warn("postTaskProblemScan: failed to add comment", "task", t.ID, "error", err)
	}

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
			log.Warn("postTaskProblemScan: failed to create follow-up", "task", t.ID, "title", f.Title, "error", err)
			continue
		}
		d.engine.AddComment(newTask.ID, "system",
			fmt.Sprintf("[problem-scan] Auto-created from scan of task %s (%s)", t.ID, t.Title))
		created++
	}

	log.Info("postTaskProblemScan: scan complete", "task", t.ID, "problems", len(sr.Problems), "followups", created)
}

func (d *Dispatcher) postTaskSkillFailures(t TaskBoard, task dispatch.Task, errMsg string) {
	if errMsg == "" || d.deps.Skills == nil {
		return
	}

	skills := d.deps.Skills.SelectSkills(task)
	if len(skills) == 0 {
		return
	}

	for _, s := range skills {
		d.deps.Skills.AppendFailure(s.Name, t.Title, t.Assignee, errMsg)
		log.Debug("skill failure recorded", "skill", s.Name, "task", t.ID)
	}
}

