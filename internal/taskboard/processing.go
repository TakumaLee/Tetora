package taskboard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"tetora/internal/config"
	"tetora/internal/coord"
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
// Tasks with no type set are treated as non-dev and skip workflow routing entirely.
func (d *Dispatcher) routeWorkflow(t TaskBoard) string {
	routing := d.engine.config.AutoDispatch.WorkflowRouting
	if !routing.Enabled || len(routing.Rules) == 0 {
		return routing.Fallback
	}

	// Tasks without a type are non-dev — don't route them into dev workflows.
	if t.Type == "" {
		log.Info("workflow routing: no task type, skipping workflow", "task", t.ID)
		return "none"
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

// resolveCoordDir returns the shared coordination directory path, or "" if
// the workspace dir is not configured or the coord directory does not exist.
func resolveCoordDir(cfg *config.Config) string {
	if cfg.WorkspaceDir == "" {
		return ""
	}
	dir := filepath.Join(cfg.WorkspaceDir, "shared", "coord")
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return ""
	}
	return dir
}

// resolveRegions returns the file regions a task will operate in.
func resolveRegions(projectWorkdir string, cfg *config.Config) []string {
	if projectWorkdir != "" {
		return []string{projectWorkdir}
	}
	if cfg.DefaultWorkdir != "" {
		return []string{cfg.DefaultWorkdir}
	}
	return []string{cfg.WorkspaceDir}
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

	// Pre-dispatch coordination: claim regions & detect conflicts.
	coordDir := resolveCoordDir(d.cfg)
	finalAgent := t.Assignee
	if coordDir != "" && coord.KnownAgents[finalAgent] {
		regions := resolveRegions(projectWorkdir, d.cfg)
		activeClaims, err := coord.ReadActiveClaims(coordDir)
		if err != nil {
			log.Warn("coord: failed to read active claims", "err", err)
		} else if conflict := coord.CheckConflict(activeClaims, regions); conflict != nil {
			if err := coord.WriteBlocker(coordDir, coord.Blocker{
				Version:       "1",
				Type:          "blocker",
				TaskID:        t.ID,
				Agent:         finalAgent,
				BlockedAt:     time.Now().UTC(),
				Severity:      "high",
				Description:   fmt.Sprintf("Region conflict with %s on task %s", conflict.Agent, conflict.TaskID),
				DependsOnTask: conflict.TaskID,
			}); err != nil {
				log.Warn("coord: failed to write blocker", "task", t.ID, "err", err)
			}
			d.engine.AddComment(t.ID, "system",
				fmt.Sprintf("[coord] Region overlap with active claim from %s on task %s", conflict.Agent, conflict.TaskID))
		}
		now := time.Now().UTC()
		if err := coord.WriteClaim(coordDir, coord.Claim{
			Version:   "1",
			Type:      "claim",
			TaskID:    t.ID,
			Agent:     finalAgent,
			ClaimedAt: now,
			ExpiresAt: now.Add(2 * time.Hour),
			Regions:   regions,
			Status:    "active",
		}); err != nil {
			log.Warn("coord: failed to write claim", "task", t.ID, "err", err)
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

	// Post-dispatch coordination: finding + release claim + resolve blockers.
	if coordDir != "" && coord.KnownAgents[finalAgent] {
		summary := truncStr(result.Output, 500)
		var artifacts []string
		if result.OutputFile != "" {
			artifacts = []string{result.OutputFile}
		}
		if err := coord.WriteFinding(coordDir, coord.Finding{
			Version:    "1",
			Type:       "finding",
			TaskID:     t.ID,
			Agent:      finalAgent,
			RecordedAt: time.Now().UTC(),
			Summary:    summary,
			Artifacts:  artifacts,
		}); err != nil {
			log.Warn("coord: failed to write finding", "task", t.ID, "err", err)
		}
		if err := coord.ReleaseClaim(coordDir, t.ID, finalAgent); err != nil {
			log.Warn("coord: failed to release claim", "task", t.ID, "err", err)
		}
		resolution := "Task completed"
		if len(summary) > 100 {
			resolution = summary[:100]
		} else if summary != "" {
			resolution = summary
		}
		if err := coord.ResolveBlockersFor(coordDir, t.ID, finalAgent, resolution); err != nil {
			log.Warn("coord: failed to resolve blockers", "task", t.ID, "err", err)
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

	// Completion status gate — BLOCKED and NEEDS_CONTEXT skip review entirely.
	completionStatus := result.CompletionStat
	if newStatus == "done" && (completionStatus == dispatch.StatusBlocked || completionStatus == dispatch.StatusNeedsContext) {
		reason := result.BlockedReason
		if reason == "" {
			reason = "(no reason provided)"
		}
		tag := "BLOCKED"
		if completionStatus == dispatch.StatusNeedsContext {
			tag = "NEEDS_CONTEXT"
		}
		log.Info("taskboard dispatch: agent reported "+tag, "id", t.ID, "reason", reason)
		d.engine.AddComment(t.ID, "system",
			fmt.Sprintf("[completion-status] Agent reported %s: %s", tag, reason))
		newStatus = "review"
		t.Assignee = d.resolveEscalateAssignee()
	}

	// Review gate.
	if newStatus == "done" && !qaApproved && t.Status != "review" {
		reviewer := d.engine.config.AutoDispatch.ReviewAgent
		if reviewer == "" {
			reviewer = "ruri"
		}
		log.Info("taskboard dispatch: auto-review starting", "id", t.ID, "reviewer", reviewer)
		d.engine.AddComment(t.ID, "system", fmt.Sprintf("[auto-review] %s reviewing output...", reviewer))

		// Build completion context for the reviewer when agent has concerns.
		var completionCtxArg *string
		if completionStatus == dispatch.StatusDoneWithConcerns && result.Concerns != "" {
			s := fmt.Sprintf("Agent status: DONE_WITH_CONCERNS\nConcerns: %s", result.Concerns)
			completionCtxArg = &s
		}

		rv := d.thoroughReview(ctx, prompt, result.Output, t.Assignee, reviewer, completionCtxArg)
		result.CostUSD += rv.CostUSD

		// For DONE_WITH_CONCERNS: if review approves, mark needs-thought instead of done.
		needsThought := completionStatus == dispatch.StatusDoneWithConcerns

		switch rv.Verdict {
		case reviewApprove:
			log.Info("taskboard dispatch: auto-review approved", "id", t.ID, "comment", rv.Comment)
			d.engine.AddComment(t.ID, reviewer, fmt.Sprintf("[review] Approved: %s", rv.Comment))
			if needsThought {
				d.engine.AddComment(t.ID, "system",
					fmt.Sprintf("[needs-thought] Approved but agent had concerns: %s", result.Concerns))
				newStatus = "review"
				t.Assignee = d.resolveEscalateAssignee()
			}

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
					rv2 := d.thoroughReview(ctx, prompt, retryResult.Output, t.Assignee, reviewer, nil)
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
	r := d.thoroughReview(ctx, originalPrompt, output, agentRole, reviewer, nil)
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

func (d *Dispatcher) thoroughReview(ctx context.Context, originalPrompt, output, agentRole, reviewer string, completionCtx *string) reviewResult {
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

	// Inject agent's completion self-assessment into review prompt if provided.
	if completionCtx != nil && *completionCtx != "" {
		reviewPrompt += "\n\n## Agent Self-Assessment\n" + *completionCtx +
			"\n\nPay special attention to the agent's stated concerns when reviewing."
	}

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
