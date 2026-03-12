package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

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

	// Clean stale lock before git operations.
	cleanStaleLock(wsDir, t.ID, d.engine)

	// Check for uncommitted changes.
	statusOut, err := exec.Command("git", "-C", wsDir, "status", "--porcelain").Output()
	if err != nil {
		logWarn("postTaskWorkspaceGit: git status failed", "task", t.ID, "error", err)
		d.engine.AddComment(t.ID, "system", "[WARNING] workspace git status failed: "+err.Error())
		return
	}
	if len(bytes.TrimSpace(statusOut)) == 0 {
		return
	}

	if out, err := exec.Command("git", "-C", wsDir, "add", "-A").CombinedOutput(); err != nil {
		msg := fmt.Sprintf("[WARNING] workspace git add failed: %s", strings.TrimSpace(string(out)))
		logWarn("postTaskWorkspaceGit: git add failed", "task", t.ID, "error", string(out))
		d.engine.AddComment(t.ID, "system", msg)
		if _, moveErr := d.engine.MoveTask(t.ID, "partial-done"); moveErr != nil {
			logWarn("postTaskWorkspaceGit: failed to move to partial-done", "task", t.ID, "error", moveErr)
		}
		return
	}

	commitMsg := fmt.Sprintf("[%s] %s", t.ID, t.Title)
	if out, err := exec.Command("git", "-C", wsDir, "commit", "-m", commitMsg).CombinedOutput(); err != nil {
		msg := fmt.Sprintf("[WARNING] workspace git commit failed: %s", strings.TrimSpace(string(out)))
		logWarn("postTaskWorkspaceGit: git commit failed", "task", t.ID, "error", string(out))
		d.engine.AddComment(t.ID, "system", msg)
		if _, moveErr := d.engine.MoveTask(t.ID, "partial-done"); moveErr != nil {
			logWarn("postTaskWorkspaceGit: failed to move to partial-done", "task", t.ID, "error", moveErr)
		}
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

	// Branch name from configured convention.
	branch := buildBranchName(d.engine.config.GitWorkflow, t)

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

	// Capture full diff for review panel.
	baseBranch := detectDefaultBranch(workdir)
	diffOut, _ := exec.Command("git", "-C", workdir, "diff", baseBranch+"..."+branch).Output()
	if diff := string(diffOut); diff != "" {
		if len(diff) > 100000 {
			diff = diff[:100000] + "\n... (truncated)"
		}
		d.engine.AddComment(t.ID, "system", diff, "diff")
	}

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

		// Auto-create PR if enabled.
		if d.engine.config.GitPR {
			d.postTaskGitPR(t, workdir, branch)
		}
	}
}

// postTaskWorktree handles the worktree lifecycle after a task completes:
//   - done/review: commits any uncommitted agent changes, merges into main, logs diff
//   - failed/cancelled: discards the worktree without merging
//   - always: removes the worktree directory
//
// This is always called even on failure so the worktree doesn't accumulate on disk.
func (d *TaskBoardDispatcher) postTaskWorktree(t TaskBoard, projectWorkdir, worktreeDir, newStatus string) {
	if worktreeDir == "" || projectWorkdir == "" {
		return
	}

	// Track whether merge succeeded — only cleanup on success.
	mergeOK := false
	defer func() {
		if mergeOK {
			if err := d.worktreeMgr.Remove(projectWorkdir, worktreeDir); err != nil {
				logWarn("worktree: cleanup failed", "task", t.ID, "path", worktreeDir, "error", err)
				d.engine.AddComment(t.ID, "system",
					fmt.Sprintf("[worktree] Cleanup failed: %v", err))
			} else {
				logInfo("worktree: cleaned up", "task", t.ID, "path", worktreeDir)
			}
		} else if newStatus == "done" || newStatus == "review" {
			// Merge failed — preserve worktree for manual recovery.
			logWarn("worktree: preserved for recovery", "task", t.ID, "path", worktreeDir)
		}
	}()

	switch newStatus {
	case "done", "review":
		commitCount := d.worktreeMgr.CommitCount(worktreeDir)
		hasChanges := d.worktreeMgr.HasChanges(worktreeDir)

		if commitCount == 0 && !hasChanges {
			mergeOK = true // nothing to preserve
			d.engine.AddComment(t.ID, "system",
				"[worktree] No changes committed. Worktree discarded.")
			return
		}

		// Get full diff for review panel (before merge destroys the branch diff).
		d.captureTaskDiff(t, projectWorkdir, worktreeDir)

		// Commit any uncommitted changes, then merge.
		commitMsg := fmt.Sprintf("[%s] %s", t.ID, t.Title)
		diffSummary, err := d.worktreeMgr.Merge(projectWorkdir, worktreeDir, commitMsg)
		if err != nil {
			logWarn("worktree: merge failed", "task", t.ID, "error", err)
			d.engine.AddComment(t.ID, "system",
				fmt.Sprintf("[worktree] ⚠️ Merge failed: %v\nBranch preserved: task/%s\nWorktree preserved: %s\nRecover manually: git -C %s merge task/%s",
					err, t.ID, worktreeDir, projectWorkdir, t.ID))
			// Move task to partial-done so it's visible in triage.
			if _, moveErr := d.engine.MoveTask(t.ID, "partial-done"); moveErr != nil {
				logWarn("worktree: failed to move to partial-done", "task", t.ID, "error", moveErr)
			}
			return // mergeOK stays false → worktree preserved
		}

		mergeOK = true
		comment := "[worktree] Changes merged into main."
		if diffSummary != "" {
			comment += "\n```\n" + diffSummary + "\n```"
		}
		d.engine.AddComment(t.ID, "system", comment)
		logInfo("worktree: merge complete", "task", t.ID)

	default: // failed, cancelled
		mergeOK = true // discard worktree on failure (no data to preserve)
		d.engine.AddComment(t.ID, "system",
			"[worktree] Task failed — worktree discarded without merge.")
	}
}

// captureTaskDiff captures the full unified diff for the review panel.
// Stored as a type="diff" comment so it survives worktree removal.
func (d *TaskBoardDispatcher) captureTaskDiff(t TaskBoard, repoDir, wtDir string) string {
	if wtDir == "" {
		return ""
	}
	taskID := filepath.Base(wtDir)
	branch := "task/" + taskID
	baseBranch := detectDefaultBranch(repoDir)

	// Get merge base.
	mergeBase, err := exec.Command("git", "-C", wtDir, "merge-base", baseBranch, branch).Output()
	if err != nil {
		return ""
	}
	base := strings.TrimSpace(string(mergeBase))

	// Get full unified diff.
	diffOut, err := exec.Command("git", "-C", wtDir, "diff", base+"..."+branch).Output()
	if err != nil {
		return ""
	}

	diff := string(diffOut)
	if len(diff) > 100000 { // 100KB cap
		diff = diff[:100000] + "\n... (truncated, diff too large)"
	}

	if diff != "" {
		d.engine.AddComment(t.ID, "system", diff, "diff")
	}
	return diff
}

// prDescSem limits concurrent PR/MR description generation LLM calls.
var prDescSem = make(chan struct{}, 2)

// detectRemoteHost inspects the origin remote URL and returns "github", "gitlab", or "unknown".
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

// postTaskGitPR is the entry point for auto-creating a PR or MR after a task push.
// It detects whether the remote is GitHub or GitLab and delegates accordingly.
func (d *TaskBoardDispatcher) postTaskGitPR(t TaskBoard, workdir, branch string) {
	host := detectRemoteHost(workdir)
	switch host {
	case "github":
		d.postTaskGitHubPR(t, workdir, branch)
	case "gitlab":
		d.postTaskGitLabMR(t, workdir, branch)
	default:
		logWarn("postTaskGitPR: remote host not recognized, skipping PR/MR creation", "task", t.ID)
		d.engine.AddComment(t.ID, "system", "[post-task-git] Remote host not recognized (not GitHub or GitLab). Skipping PR/MR creation.")
	}
}

// postTaskGitHubPR creates a GitHub PR with an AI-generated title and description.
func (d *TaskBoardDispatcher) postTaskGitHubPR(t TaskBoard, workdir, branch string) {
	// Detect the default branch (main or master).
	baseBranch := detectDefaultBranch(workdir)

	// Check if a PR already exists for this branch.
	prViewCmd := exec.Command("gh", "pr", "view", branch, "--json", "url", "-q", ".url")
	prViewCmd.Dir = workdir
	existingPR, _ := prViewCmd.Output()
	if url := strings.TrimSpace(string(existingPR)); url != "" {
		logInfo("postTaskGitHubPR: PR already exists", "task", t.ID, "url", url)
		d.engine.AddComment(t.ID, "system", fmt.Sprintf("[post-task-git] PR already exists: %s", url))
		return
	}

	// Gather diff for LLM context.
	diffOut, err := exec.Command("git", "-C", workdir, "diff", baseBranch+"..."+branch, "--stat").Output()
	if err != nil {
		logWarn("postTaskGitHubPR: diff stat failed", "task", t.ID, "error", err)
	}
	diffDetail, _ := exec.Command("git", "-C", workdir, "diff", baseBranch+"..."+branch).Output()

	// Gather commit log.
	logOut, _ := exec.Command("git", "-C", workdir, "log", baseBranch+".."+branch, "--oneline").Output()

	// Generate PR title and body via LLM.
	title, body := d.generatePRDescription(t, string(diffOut), string(diffDetail), string(logOut))

	// Create PR via gh CLI.
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
		logWarn("postTaskGitHubPR: gh pr create failed", "task", t.ID, "error", msg)
		d.engine.AddComment(t.ID, "system", msg)
		return
	}

	prURL := strings.TrimSpace(string(out))
	logInfo("postTaskGitHubPR: PR created", "task", t.ID, "url", prURL)
	d.engine.AddComment(t.ID, "system", fmt.Sprintf("[post-task-git] PR created: %s", prURL))
}

// postTaskGitLabMR creates a GitLab MR with an AI-generated title and description.
func (d *TaskBoardDispatcher) postTaskGitLabMR(t TaskBoard, workdir, branch string) {
	// Detect the default branch (main or master).
	baseBranch := detectDefaultBranch(workdir)

	// Check if an MR already exists for this branch.
	mrViewCmd := exec.Command("glab", "mr", "view", branch)
	mrViewCmd.Dir = workdir
	mrViewOut, mrViewErr := mrViewCmd.Output()
	if mrViewErr == nil && len(strings.TrimSpace(string(mrViewOut))) > 0 {
		// glab mr view exits 0 and prints details when the MR exists.
		// Extract the web URL from the output if present, otherwise just note it exists.
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
		logInfo("postTaskGitLabMR: MR already exists", "task", t.ID, "url", url)
		d.engine.AddComment(t.ID, "system", msg)
		return
	}

	// Gather diff for LLM context.
	diffOut, err := exec.Command("git", "-C", workdir, "diff", baseBranch+"..."+branch, "--stat").Output()
	if err != nil {
		logWarn("postTaskGitLabMR: diff stat failed", "task", t.ID, "error", err)
	}
	diffDetail, _ := exec.Command("git", "-C", workdir, "diff", baseBranch+"..."+branch).Output()

	// Gather commit log.
	logOut, _ := exec.Command("git", "-C", workdir, "log", baseBranch+".."+branch, "--oneline").Output()

	// Generate MR title and body via LLM (reuses the same generator as GitHub).
	title, body := d.generatePRDescription(t, string(diffOut), string(diffDetail), string(logOut))

	// Create MR via glab CLI.
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
		logWarn("postTaskGitLabMR: glab mr create failed", "task", t.ID, "error", msg)
		d.engine.AddComment(t.ID, "system", msg)
		return
	}

	mrURL := strings.TrimSpace(string(out))
	logInfo("postTaskGitLabMR: MR created", "task", t.ID, "url", mrURL)
	d.engine.AddComment(t.ID, "system", fmt.Sprintf("[post-task-git] MR created: %s", mrURL))
}

// generatePRDescription uses a lightweight LLM call to generate a PR/MR title and body.
func (d *TaskBoardDispatcher) generatePRDescription(t TaskBoard, diffStat, diffDetail, commitLog string) (title, body string) {
	// Truncate diff detail to keep cost low.
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
		truncateStr(t.Title, 200),
		truncateStr(t.Description, 500),
		truncateStr(commitLog, 500),
		truncateStr(diffStat, 1000),
		diffDetail)

	task := Task{
		ID:             newUUID(),
		Name:           "pr-desc-" + t.ID,
		Prompt:         prompt,
		Model:          "haiku",
		Budget:         0.05,
		Timeout:        "30s",
		PermissionMode: "plan",
		Source:         "pr-description",
	}
	fillDefaults(d.cfg, &task)
	task.Model = "haiku"
	task.Budget = 0.05

	result := runSingleTask(d.ctx, d.cfg, task, prDescSem, nil, "")
	if result.Status != "success" || strings.TrimSpace(result.Output) == "" {
		// Fallback: use task title and simple description.
		return fmt.Sprintf("[%s] %s", t.ID, t.Title), fmt.Sprintf("## Summary\n- %s\n\nAuto-generated by Tetora task %s", t.Title, t.ID)
	}

	// Parse JSON from output.
	raw := result.Output
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end <= start {
		return fmt.Sprintf("[%s] %s", t.ID, t.Title), fmt.Sprintf("## Summary\n- %s\n\nAuto-generated by Tetora task %s", t.Title, t.ID)
	}

	var pr struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := json.Unmarshal([]byte(raw[start:end+1]), &pr); err != nil || pr.Title == "" {
		return fmt.Sprintf("[%s] %s", t.ID, t.Title), fmt.Sprintf("## Summary\n- %s\n\nAuto-generated by Tetora task %s", t.Title, t.ID)
	}

	// Append task reference to body.
	pr.Body += fmt.Sprintf("\n\n---\nTask: `%s` — %s", t.ID, t.Title)

	return pr.Title, pr.Body
}

// detectDefaultBranch returns the default branch name (main or master) for a repo.
func detectDefaultBranch(workdir string) string {
	// Try git symbolic-ref for the remote HEAD.
	out, err := exec.Command("git", "-C", workdir, "symbolic-ref", "refs/remotes/origin/HEAD").Output()
	if err == nil {
		ref := strings.TrimSpace(string(out))
		if parts := strings.Split(ref, "/"); len(parts) > 0 {
			return parts[len(parts)-1]
		}
	}
	// Fallback: check if main exists.
	if exec.Command("git", "-C", workdir, "rev-parse", "--verify", "main").Run() == nil {
		return "main"
	}
	return "master"
}

// cleanStaleLock removes stale .git/index.lock files that are older than 1 hour.
// A stale lock blocks all git operations and causes silent dispatch failures.
func cleanStaleLock(repoDir, taskID string, engine *TaskBoardEngine) {
	lockPath := filepath.Join(repoDir, ".git", "index.lock")
	info, err := os.Stat(lockPath)
	if err != nil {
		return // no lock file
	}

	age := time.Since(info.ModTime())
	if age < time.Hour {
		logWarn("cleanStaleLock: index.lock exists but is recent, skipping",
			"task", taskID, "path", lockPath, "age", age.Round(time.Second))
		if engine != nil {
			engine.AddComment(taskID, "system",
				fmt.Sprintf("[WARNING] git index.lock exists (age: %s). Waiting for other git operation to finish.", age.Round(time.Second)))
		}
		return
	}

	if err := os.Remove(lockPath); err != nil {
		logWarn("cleanStaleLock: failed to remove stale lock", "task", taskID, "path", lockPath, "error", err)
		return
	}

	logInfo("cleanStaleLock: removed stale index.lock", "task", taskID, "path", lockPath, "age", age.Round(time.Second))
	if engine != nil {
		engine.AddComment(taskID, "system",
			fmt.Sprintf("[auto-fix] Removed stale git index.lock (age: %s)", age.Round(time.Second)))
	}
}
