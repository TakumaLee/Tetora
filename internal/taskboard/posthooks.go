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

	"tetora/internal/db"
	"tetora/internal/dispatch"
	"tetora/internal/log"
)

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
