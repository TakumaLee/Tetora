package worktree

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"tetora/internal/config"
	"tetora/internal/log"
	"tetora/internal/taskboard"
)

// --- Git Worktree Manager ---
// Provides isolated git worktrees for agent tasks, preventing file conflicts
// when multiple agents work on the same repository concurrently.

// WorktreeInfo describes an active worktree.
type WorktreeInfo struct {
	Path      string    `json:"path"`
	Branch    string    `json:"branch"`
	TaskID    string    `json:"taskId,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	RepoDir   string    `json:"repoDir"`
}

// WorktreeManager handles lifecycle of git worktrees for task isolation.
type WorktreeManager struct {
	// baseDir is the root directory for storing worktrees (e.g., ~/.tetora/runtime/worktrees/).
	baseDir string
	// mu serializes concurrent operations per worktree path.
	pathMu sync.Map // map[string]*sync.Mutex
}

// NewWorktreeManager creates a worktree manager with the given base directory.
func NewWorktreeManager(baseDir string) *WorktreeManager {
	return &WorktreeManager{baseDir: baseDir}
}

// IsGitRepo checks if a directory is a git repository.
func IsGitRepo(dir string) bool {
	return exec.Command("git", "-C", dir, "rev-parse", "--git-dir").Run() == nil
}

// DetectDefaultBranch returns the default branch name (main or master) for a repo.
// Delegates to taskboard.DetectDefaultBranch to avoid duplication.
func DetectDefaultBranch(workdir string) string {
	return taskboard.DetectDefaultBranch(workdir)
}

// pathLock returns or creates a mutex for the given worktree path.
func (wm *WorktreeManager) pathLock(path string) *sync.Mutex {
	v, _ := wm.pathMu.LoadOrStore(path, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// branchMetaFile is the filename written inside each worktree to record the branch name.
const branchMetaFile = ".tetora-branch"

// BuildBranchName generates a branch name from the configured convention.
// Template vars: {type}, {agent}, {description}, {taskId}
// Default convention: "{type}/{agent}-{description}"
func BuildBranchName(cfg config.GitWorkflowConfig, t taskboard.TaskBoard) string {
	convention := cfg.BranchConvention
	if convention == "" {
		convention = "{type}/{agent}-{description}"
	}

	// Resolve {type}.
	taskType := t.Type
	if taskType == "" {
		taskType = cfg.DefaultType
	}
	if taskType == "" {
		taskType = "feat"
	}

	// Resolve {agent}.
	agent := t.Assignee
	if agent == "" {
		agent = "anon"
	}

	// Resolve {description} from title (slugify + truncate).
	description := SlugifyBranch(t.Title)
	if description == "" {
		description = t.ID
	}

	result := convention
	result = strings.ReplaceAll(result, "{type}", taskType)
	result = strings.ReplaceAll(result, "{agent}", agent)
	result = strings.ReplaceAll(result, "{description}", description)
	result = strings.ReplaceAll(result, "{taskId}", t.ID)

	return result
}

// slugifyRe is pre-compiled for Slugify() to avoid recompiling on every call.
var slugifyRe = regexp.MustCompile(`[^a-z0-9]+`)

// Slugify converts a string to a URL-friendly slug.
func Slugify(s string) string {
	s = strings.ToLower(s)
	s = slugifyRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}

// SlugifyBranch converts a title to a branch-safe slug: lowercase, max 40 chars.
func SlugifyBranch(s string) string {
	s = Slugify(s)

	// Truncate to 40 chars, but don't cut mid-word.
	if len(s) > 40 {
		s = s[:40]
		if idx := strings.LastIndex(s, "-"); idx > 20 {
			s = s[:idx]
		}
	}
	return s
}

// readBranchMeta reads the branch name from the .tetora-branch metadata file in a worktree.
func readBranchMeta(wtDir string) string {
	data, err := os.ReadFile(filepath.Join(wtDir, branchMetaFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// writeBranchMeta writes the branch name to the .tetora-branch metadata file.
func writeBranchMeta(wtDir, branch string) {
	if err := os.WriteFile(filepath.Join(wtDir, branchMetaFile), []byte(branch+"\n"), 0o644); err != nil {
		log.Debug("worktree: failed to write branch metadata", "path", wtDir, "error", err)
	}
}

// resolveBranch determines the branch name for a worktree directory.
// Reads from .tetora-branch metadata first, falls back to legacy "task/{taskID}" convention.
func resolveBranch(wtDir string) string {
	if b := readBranchMeta(wtDir); b != "" {
		return b
	}
	// Legacy fallback.
	return "task/" + filepath.Base(wtDir)
}

// Create creates a new git worktree for a task. Returns the worktree directory path.
// The branch parameter specifies the branch name to create (from BuildBranchName).
func (wm *WorktreeManager) Create(repoDir, taskID, branch string) (string, error) {
	wtDir := filepath.Join(wm.baseDir, taskID)

	mu := wm.pathLock(wtDir)
	mu.Lock()
	defer mu.Unlock()

	// Ensure base directory exists.
	if err := os.MkdirAll(wm.baseDir, 0o755); err != nil {
		return "", fmt.Errorf("worktree: mkdir %s: %w", wm.baseDir, err)
	}

	// Remove stale worktree if directory already exists.
	if _, err := os.Stat(wtDir); err == nil {
		log.Warn("worktree: removing stale worktree", "path", wtDir)
		oldBranch := resolveBranch(wtDir)
		wm.forceRemove(repoDir, wtDir, oldBranch)
	}

	// Detect base branch to branch from.
	baseBranch := DetectDefaultBranch(repoDir)

	// Create worktree: git worktree add -b {branch} {path} {base}
	out, err := exec.Command("git", "-C", repoDir,
		"worktree", "add", "-b", branch, wtDir, baseBranch).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("worktree: git worktree add failed: %s: %w",
			strings.TrimSpace(string(out)), err)
	}

	// Write branch metadata so Remove/Merge can find the branch name.
	writeBranchMeta(wtDir, branch)

	log.Info("worktree: created", "task", taskID, "path", wtDir, "branch", branch, "base", baseBranch)
	return wtDir, nil
}

// Remove cleans up a worktree with the 4-step sequence from Vibe Kanban:
// 1. git worktree remove --force
// 2. force cleanup .git/worktrees metadata
// 3. rm -rf worktree directory
// 4. git worktree prune
func (wm *WorktreeManager) Remove(repoDir, wtDir string) error {
	mu := wm.pathLock(wtDir)
	mu.Lock()
	defer mu.Unlock()

	branch := resolveBranch(wtDir)
	wm.forceRemove(repoDir, wtDir, branch)
	return nil
}

// forceRemove performs the 4-step cleanup (caller must hold pathLock).
func (wm *WorktreeManager) forceRemove(repoDir, wtDir, branch string) {
	// Step 1: git worktree remove --force
	if out, err := exec.Command("git", "-C", repoDir,
		"worktree", "remove", "--force", wtDir).CombinedOutput(); err != nil {
		log.Debug("worktree: git worktree remove failed (non-fatal)",
			"path", wtDir, "error", strings.TrimSpace(string(out)))
	}

	// Step 2: force cleanup .git/worktrees metadata
	wtName := filepath.Base(wtDir)
	metaDir := filepath.Join(repoDir, ".git", "worktrees", wtName)
	if err := os.RemoveAll(metaDir); err != nil {
		log.Debug("worktree: metadata cleanup failed (non-fatal)", "path", metaDir, "error", err)
	}

	// Step 3: rm -rf worktree directory (critical path)
	if err := os.RemoveAll(wtDir); err != nil {
		log.Warn("worktree: failed to remove directory", "path", wtDir, "error", err)
	}

	// Step 4: git worktree prune
	if out, err := exec.Command("git", "-C", repoDir,
		"worktree", "prune").CombinedOutput(); err != nil {
		log.Debug("worktree: prune failed (non-fatal)",
			"error", strings.TrimSpace(string(out)))
	}

	// Delete the task branch (best effort).
	if branch != "" {
		exec.Command("git", "-C", repoDir, "branch", "-D", branch).Run() //nolint:errcheck
	}

	log.Info("worktree: removed", "path", wtDir)
}

// DiffSummary returns git diff --stat between the worktree branch and its merge base.
func (wm *WorktreeManager) DiffSummary(repoDir, wtDir string) (string, error) {
	baseBranch := DetectDefaultBranch(repoDir)
	branch := resolveBranch(wtDir)

	// Get merge base.
	mergeBase, err := exec.Command("git", "-C", wtDir,
		"merge-base", baseBranch, branch).Output()
	if err != nil {
		return "", fmt.Errorf("worktree: merge-base failed: %w", err)
	}
	base := strings.TrimSpace(string(mergeBase))

	// Get diff stat.
	diffOut, err := exec.Command("git", "-C", wtDir,
		"diff", "--stat", base+"..."+branch).Output()
	if err != nil {
		return "", fmt.Errorf("worktree: diff stat failed: %w", err)
	}
	return strings.TrimSpace(string(diffOut)), nil
}

// Merge merges the worktree branch back to the target branch (typically main).
// Commits in the worktree first if there are uncommitted changes.
// Returns the diff summary for review logging.
func (wm *WorktreeManager) Merge(repoDir, wtDir, commitMsg string) (diffSummary string, err error) {
	mu := wm.pathLock(wtDir)
	mu.Lock()
	defer mu.Unlock()

	taskID := filepath.Base(wtDir)
	branch := resolveBranch(wtDir)
	targetBranch := DetectDefaultBranch(repoDir)

	// Stage and commit any uncommitted changes in the worktree.
	statusOut, _ := exec.Command("git", "-C", wtDir, "status", "--porcelain").Output()
	if len(strings.TrimSpace(string(statusOut))) > 0 {
		if out, err := exec.Command("git", "-C", wtDir, "add", "-A").CombinedOutput(); err != nil {
			return "", fmt.Errorf("worktree: git add failed: %s: %w",
				strings.TrimSpace(string(out)), err)
		}
		if commitMsg == "" {
			commitMsg = fmt.Sprintf("[%s] task changes", taskID)
		}
		if out, err := exec.Command("git", "-C", wtDir, "commit", "-m", commitMsg).CombinedOutput(); err != nil {
			return "", fmt.Errorf("worktree: git commit failed: %s: %w",
				strings.TrimSpace(string(out)), err)
		}
	}

	// Get diff summary before merge.
	diffSummary, _ = wm.diffStatUnlocked(wtDir, targetBranch, branch)

	// Merge branch into target on the main repo (not the worktree).
	if out, err := exec.Command("git", "-C", repoDir,
		"merge", "--no-ff", branch, "-m",
		fmt.Sprintf("Merge %s into %s", branch, targetBranch)).CombinedOutput(); err != nil {
		return diffSummary, fmt.Errorf("worktree: merge failed: %s: %w",
			strings.TrimSpace(string(out)), err)
	}

	log.Info("worktree: merged", "branch", branch, "into", targetBranch, "task", taskID)
	return diffSummary, nil
}

// diffStatUnlocked returns diff stat (caller must hold lock or not need one).
func (wm *WorktreeManager) diffStatUnlocked(wtDir, base, branch string) (string, error) {
	mergeBase, err := exec.Command("git", "-C", wtDir,
		"merge-base", base, branch).Output()
	if err != nil {
		return "", err
	}
	diffOut, err := exec.Command("git", "-C", wtDir,
		"diff", "--stat", strings.TrimSpace(string(mergeBase))+"..."+branch).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(diffOut)), nil
}

// List returns all active worktrees managed under the base directory.
func (wm *WorktreeManager) List() ([]WorktreeInfo, error) {
	entries, err := os.ReadDir(wm.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var infos []WorktreeInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		wtDir := filepath.Join(wm.baseDir, e.Name())

		// Verify it's actually a git worktree (has .git file, not directory).
		gitPath := filepath.Join(wtDir, ".git")
		fi, err := os.Stat(gitPath)
		if err != nil || fi.IsDir() {
			continue // not a worktree
		}

		info := WorktreeInfo{
			Path:   wtDir,
			TaskID: e.Name(),
			Branch: resolveBranch(wtDir),
		}

		// Get creation time from directory.
		if dirInfo, err := e.Info(); err == nil {
			info.CreatedAt = dirInfo.ModTime()
		}

		infos = append(infos, info)
	}
	return infos, nil
}

// Prune removes worktrees older than maxAge.
func (wm *WorktreeManager) Prune(repoDir string, maxAge time.Duration) (int, error) {
	infos, err := wm.List()
	if err != nil {
		return 0, err
	}

	cutoff := time.Now().Add(-maxAge)
	removed := 0
	for _, info := range infos {
		if info.CreatedAt.Before(cutoff) {
			log.Info("worktree: pruning expired", "path", info.Path,
				"age", time.Since(info.CreatedAt).Round(time.Minute))
			if err := wm.Remove(repoDir, info.Path); err != nil {
				log.Warn("worktree: prune remove failed", "path", info.Path, "error", err)
				continue
			}
			removed++
		}
	}
	return removed, nil
}

// HasChanges checks if a worktree has uncommitted changes.
func (wm *WorktreeManager) HasChanges(wtDir string) bool {
	out, err := exec.Command("git", "-C", wtDir, "status", "--porcelain").Output()
	if err != nil {
		return false
	}
	return len(strings.TrimSpace(string(out))) > 0
}

// CommitCount returns the number of commits in the worktree branch ahead of base.
func (wm *WorktreeManager) CommitCount(wtDir string) int {
	baseBranch := DetectDefaultBranch(wtDir)
	out, err := exec.Command("git", "-C", wtDir,
		"rev-list", "--count", baseBranch+"..HEAD").Output()
	if err != nil {
		return 0
	}
	n := 0
	fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &n)
	return n
}

// MergeBranchOnly merges the worktree branch into the target branch without
// committing uncommitted changes first. Used when the agent has already committed
// everything via its own git calls.
func (wm *WorktreeManager) MergeBranchOnly(repoDir, wtDir string) (diffSummary string, err error) {
	mu := wm.pathLock(wtDir)
	mu.Lock()
	defer mu.Unlock()

	taskID := filepath.Base(wtDir)
	branch := resolveBranch(wtDir)
	targetBranch := DetectDefaultBranch(repoDir)

	// Get diff summary before merge.
	diffSummary, _ = wm.diffStatUnlocked(wtDir, targetBranch, branch)

	// Merge branch into target on the main repo.
	if out, mergeErr := exec.Command("git", "-C", repoDir,
		"merge", "--no-ff", branch, "-m",
		fmt.Sprintf("Merge %s into %s", branch, targetBranch)).CombinedOutput(); mergeErr != nil {
		return diffSummary, fmt.Errorf("worktree: merge failed: %s: %w",
			strings.TrimSpace(string(out)), mergeErr)
	}

	log.Info("worktree: merged (branch-only)", "branch", branch, "into", targetBranch, "task", taskID)
	return diffSummary, nil
}
