package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"


	"tetora/internal/db"
)

// --- Dev↔QA Loop ---

// devQALoopResult holds the outcome of the Dev↔QA retry loop.
type devQALoopResult struct {
	Result     TaskResult
	QAApproved bool    // true if QA review passed
	Attempts   int     // total execution attempts
	TotalCost  float64 // accumulated cost across all attempts (dev + QA)
}

// devQALoop executes a task and runs automated QA review in a loop.
// If QA fails, the reviewer's feedback is injected into the prompt and the task is retried.
// After maxRetries QA failures, the task is escalated to human review.
//
// Failure injection integration:
//   - QA rejections are recorded to skill failures.md so future executions learn from them.
//   - On retry, existing skill failures are loaded and injected into the prompt.
//
// Flow: Dev execute → QA review → (pass → done) | (fail → record failure → inject feedback + failures → retry)
func (d *TaskBoardDispatcher) devQALoop(ctx context.Context, t TaskBoard, task Task, usedWorkflow bool, workflowName string) devQALoopResult {
	maxRetries := d.engine.config.MaxRetriesOrDefault() // default 3

	reviewer := d.engine.config.AutoDispatch.ReviewAgent
	if reviewer == "" {
		reviewer = "ruri"
	}

	// Preserve the original prompt so QA feedback doesn't compound across retries.
	originalPrompt := task.Prompt

	var accumulated float64

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Step 1: Dev execution.
		var result TaskResult
		if usedWorkflow {
			result = d.runTaskWithWorkflow(ctx, t, task, workflowName)
		} else {
			result = runSingleTask(ctx, d.cfg, task, d.sem, d.childSem, t.Assignee)
		}
		accumulated += result.CostUSD

		// If execution itself failed (crash/timeout/empty output), exit loop.
		// The caller's existing AutoRetryFailed path handles execution failures.
		if result.Status != "success" {
			return devQALoopResult{Result: result, Attempts: attempt + 1, TotalCost: accumulated}
		}
		if strings.TrimSpace(result.Output) == "" {
			return devQALoopResult{Result: result, Attempts: attempt + 1, TotalCost: accumulated}
		}

		// Step 2: QA review.
		reviewOK, reviewComment, reviewCost := d.reviewTaskOutput(ctx, originalPrompt, result.Output, t.Assignee, reviewer)
		accumulated += reviewCost

		if reviewOK {
			logInfo("devQA: review passed", "task", t.ID, "attempt", attempt+1)
			d.engine.AddComment(t.ID, reviewer,
				fmt.Sprintf("[QA PASS] (attempt %d/%d) %s", attempt+1, maxRetries+1, reviewComment))
			return devQALoopResult{Result: result, QAApproved: true, Attempts: attempt + 1, TotalCost: accumulated}
		}

		// QA failed.
		logInfo("devQA: review failed, injecting feedback",
			"task", t.ID, "attempt", attempt+1, "maxAttempts", maxRetries+1, "comment", truncate(reviewComment, 200))

		d.engine.AddComment(t.ID, reviewer,
			fmt.Sprintf("[QA FAIL] (attempt %d/%d) %s", attempt+1, maxRetries+1, reviewComment))

		// Record QA rejection as skill failure for future context injection.
		qaFailMsg := fmt.Sprintf("[QA rejection attempt %d] %s", attempt+1, reviewComment)
		d.postTaskSkillFailures(t, task, qaFailMsg)

		if attempt == maxRetries {
			// All retries exhausted — escalate.
			d.engine.AddComment(t.ID, "system",
				fmt.Sprintf("[ESCALATE] Dev↔QA loop exhausted (%d attempts). Escalating to human review.", maxRetries+1))
			logWarn("devQA: max retries exhausted, escalating", "task", t.ID, "attempts", maxRetries+1)
			return devQALoopResult{Result: result, Attempts: attempt + 1, TotalCost: accumulated}
		}

		// Step 3: Rebuild prompt with QA feedback + skill failure context for retry.
		task.Prompt = originalPrompt

		// Inject accumulated skill failures (includes QA rejections just recorded).
		if failureCtx := d.loadSkillFailureContext(task); failureCtx != "" {
			task.Prompt += "\n\n## Previous Failure Context\n"
			task.Prompt += failureCtx
		}

		// Inject QA reviewer's specific feedback for this attempt.
		task.Prompt += fmt.Sprintf("\n\n## QA Review Feedback (Attempt %d)\n", attempt+1)
		task.Prompt += "The QA reviewer rejected the output. Issues found:\n"
		task.Prompt += reviewComment
		task.Prompt += fmt.Sprintf("\n\nAddress ALL issues above. This is retry %d of %d.\n", attempt+2, maxRetries+1)

		// New IDs for the retry execution (fresh session, no session bleed).
		task.ID = newUUID()
		task.SessionID = newUUID()
	}

	// Unreachable, but satisfy the compiler.
	return devQALoopResult{}
}

// loadSkillFailureContext loads failure context for all skills matching the task.
// Returns the combined failure context string, or empty if none.
func (d *TaskBoardDispatcher) loadSkillFailureContext(task Task) string {
	skills := selectSkills(d.cfg, task)
	if len(skills) == 0 {
		return ""
	}

	var parts []string
	for _, s := range skills {
		failures := loadSkillFailuresByName(d.cfg, s.Name)
		if failures == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("### Skill: %s\n%s", s.Name, failures))
	}
	if len(parts) == 0 {
		return ""
	}

	combined := strings.Join(parts, "\n\n")
	if len(combined) > skillFailuresMaxInject {
		combined = combined[:skillFailuresMaxInject] + "\n... (truncated)"
	}
	return combined
}

// reviewTaskOutput asks the configured review agent to evaluate task output quality.
// Uses the taskboard's ReviewAgent config with fallback to SmartDispatch.ReviewAgent.
// Returns (approved, comment, costUSD).
// reviewVerdict represents a three-way review decision.
type reviewVerdict string

const (
	reviewApprove  reviewVerdict = "approve"  // quality OK → done
	reviewFix      reviewVerdict = "fix"      // issues found but agent can fix → retry
	reviewEscalate reviewVerdict = "escalate" // needs human judgment → assign to user
)

type reviewResult struct {
	Verdict  reviewVerdict
	Comment  string
	CostUSD  float64
}

func (d *TaskBoardDispatcher) reviewTaskOutput(ctx context.Context, originalPrompt, output, agentRole, reviewer string) (bool, string, float64) {
	r := d.thoroughReview(ctx, originalPrompt, output, agentRole, reviewer)
	return r.Verdict == reviewApprove, r.Comment, r.CostUSD
}

// thoroughReview runs a comprehensive code quality review using sonnet/opus.
// Returns a three-way verdict: approve, fix (send back to agent), or escalate (needs human).
func (d *TaskBoardDispatcher) thoroughReview(ctx context.Context, originalPrompt, output, agentRole, reviewer string) reviewResult {
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

	task := Task{
		Prompt:  reviewPrompt,
		Timeout: "120s",
		Budget:  d.cfg.SmartDispatch.ReviewBudget,
		Source:  "auto-review",
	}
	fillDefaults(d.cfg, &task)

	// Use sonnet for review — thorough but cost-effective.
	task.Model = "sonnet"

	// Apply reviewer's soul prompt (but keep sonnet model).
	if soulPrompt, err := loadAgentPrompt(d.cfg, reviewer); err == nil && soulPrompt != "" {
		task.SystemPrompt = soulPrompt
	}
	if rc, ok := d.cfg.Agents[reviewer]; ok {
		if rc.PermissionMode != "" {
			task.PermissionMode = rc.PermissionMode
		}
		// Use reviewer's model if it's opus (upgrade from sonnet is OK).
		if rc.Model == "opus" {
			task.Model = "opus"
		}
	}

	result := runSingleTask(ctx, d.cfg, task, d.sem, d.childSem, reviewer)
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
			// Legacy format support.
			OK bool `json:"ok"`
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
				// Legacy bool format fallback.
				if parsed.OK {
					return reviewResult{Verdict: reviewApprove, Comment: parsed.Comment, CostUSD: result.CostUSD}
				}
				return reviewResult{Verdict: reviewFix, Comment: parsed.Comment, CostUSD: result.CostUSD}
			}
		}
	}

	return reviewResult{Verdict: reviewApprove, Comment: "review parse error", CostUSD: result.CostUSD}
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
	`, db.Escape(cutoff7d))
	projectRows, err := db.Query(d.engine.dbPath, projectSQL)
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
		`, db.Escape(cutoff24h), db.Escape(projectID))
		cooldownRows, err := db.Query(d.engine.dbPath, cooldownSQL)
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
	var commentSb strings.Builder
	commentSb.WriteString("[problem-scan] Potential issues detected:\n")
	for _, p := range sr.Problems {
		commentSb.WriteString(fmt.Sprintf("- [%s] %s: %s\n", p.Severity, p.Summary, p.Detail))
	}

	if _, err := d.engine.AddComment(t.ID, "system", commentSb.String()); err != nil {
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

// postTaskSkillFailures records the failure to each skill's failures.md
// so that subsequent executions of the same skill get injected failure context.
func (d *TaskBoardDispatcher) postTaskSkillFailures(t TaskBoard, task Task, errMsg string) {
	if errMsg == "" {
		return
	}

	skills := selectSkills(d.cfg, task)
	if len(skills) == 0 {
		return
	}

	for _, s := range skills {
		appendSkillFailure(d.cfg, s.Name, t.Title, t.Assignee, errMsg)
		logDebug("skill failure recorded", "skill", s.Name, "task", t.ID)
	}
}
