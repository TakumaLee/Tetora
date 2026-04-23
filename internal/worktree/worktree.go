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

// unsafeMarkerFile is dropped into a worktree directory that a prior Create()
// attempt could not fully initialize (e.g., master ref was corrupt, git worktree
// add failed mid-way). On the next attempt, Create() sees the marker, wipes the
// stale dir, and retries from scratch. The file contents carry a human-readable
// reason for diagnostics.
const unsafeMarkerFile = ".tetora-unsafe"

// MarkUnsafe writes an unsafe marker into a worktree directory. Best-effort —
// if the directory doesn't exist the caller likely failed even earlier, and
// there is nothing useful to mark. Errors are logged, not returned.
func MarkUnsafe(wtDir, reason string) {
	if err := os.MkdirAll(wtDir, 0o755); err != nil {
		log.Debug("worktree: MarkUnsafe mkdir failed", "path", wtDir, "error", err)
		return
	}
	payload := fmt.Sprintf("%s\n%s\n", time.Now().UTC().Format(time.RFC3339), reason)
	if err := os.WriteFile(filepath.Join(wtDir, unsafeMarkerFile), []byte(payload), 0o644); err != nil {
		log.Debug("worktree: MarkUnsafe write failed", "path", wtDir, "error", err)
	}
}

// IsUnsafe reports whether a worktree directory carries the unsafe marker.
func IsUnsafe(wtDir string) bool {
	_, err := os.Stat(filepath.Join(wtDir, unsafeMarkerFile))
	return err == nil
}

// repairMasterRef attempts to fix a corrupted default-branch ref by re-fetching
// from origin and pruning stale worktree metadata. Safe to call even when the
// ref is healthy — the fetch/prune operations are idempotent and do not touch
// the working tree. All sub-command failures are non-fatal and logged at Debug.
func repairMasterRef(repoDir string) {
	if out, err := exec.Command("git", "-C", repoDir, "worktree", "prune").CombinedOutput(); err != nil {
		log.Debug("worktree: prune during master-ref repair failed (non-fatal)",
			"error", strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("git", "-C", repoDir, "fetch", "--prune", "origin").CombinedOutput(); err != nil {
		// No origin remote, offline, etc. — not fatal; the retry still gets a shot.
		log.Debug("worktree: fetch --prune during master-ref repair failed (non-fatal)",
			"error", strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("git", "-C", repoDir, "remote", "prune", "origin").CombinedOutput(); err != nil {
		log.Debug("worktree: remote prune during master-ref repair failed (non-fatal)",
			"error", strings.TrimSpace(string(out)))
	}
}

// isMasterRefError matches the error signatures git emits when the default
// branch ref is missing or unreadable — the symptom we're trying to repair.
func isMasterRefError(msg string) bool {
	lower := strings.ToLower(msg)
	return strings.Contains(lower, "invalid reference") ||
		strings.Contains(lower, "not a valid object name") ||
		strings.Contains(lower, "unknown revision") ||
		strings.Contains(lower, "bad revision") ||
		strings.Contains(lower, "cannot update ref") ||
		strings.Contains(lower, "could not read")
}

// sessionLockFile aliases taskboard.SessionLockFile for use within this package.
const sessionLockFile = taskboard.SessionLockFile

// sessionWaitPollInterval and sessionWaitMaxDuration control how long Create()
// waits for an active session to finish before proceeding with stale worktree
// removal. Declared as vars (not consts) so tests can override them.
var (
	sessionWaitPollInterval = 5 * time.Second
	sessionWaitMaxDuration  = 60 * time.Second
)

// AcquireSessionLock writes a session lock file inside wtDir containing the
// current process PID. Returns a release function that removes the file.
// The lock prevents Create() and Remove() from deleting the worktree while a
// Claude session is active inside it. The release function is idempotent and
// safe to call if the directory has already been removed by forceRemove.
func AcquireSessionLock(wtDir string) func() {
	lockPath := filepath.Join(wtDir, sessionLockFile)
	data := fmt.Sprintf("%d\n", os.Getpid())
	if err := os.WriteFile(lockPath, []byte(data), 0o644); err != nil {
		log.Debug("worktree: failed to write session lock", "path", lockPath, "error", err)
	}
	return func() { os.Remove(lockPath) } //nolint:errcheck
}

// AcquireSessionLock implements WorktreeManageable. It is a method wrapper
// around the package-level AcquireSessionLock so WorktreeManager satisfies
// the interface without duplicating the implementation.
func (wm *WorktreeManager) AcquireSessionLock(wtDir string) func() {
	return AcquireSessionLock(wtDir)
}

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
		// If a prior attempt marked this worktree unsafe, log the reason and
		// force removal without waiting for a session — the session concept
		// does not apply to a half-initialized worktree.
		if IsUnsafe(wtDir) {
			if data, readErr := os.ReadFile(filepath.Join(wtDir, unsafeMarkerFile)); readErr == nil {
				log.Warn("worktree: unsafe marker present, forcing cleanup",
					"path", wtDir, "marker", strings.TrimSpace(string(data)))
			} else {
				log.Warn("worktree: unsafe marker present, forcing cleanup", "path", wtDir)
			}
			wm.forceRemove(repoDir, wtDir, resolveBranch(wtDir))
			// Verify cleanup actually removed the path; if not, fail fast so we
			// don't feed a half-cleaned directory to `git worktree add` and
			// produce an opaque error.
			if _, err := os.Stat(wtDir); err == nil {
				return "", fmt.Errorf("worktree: unsafe cleanup failed — %s still exists, refusing to proceed", wtDir)
			}
		} else {
			// Guard: wait for any active session to finish before removing the stale
			// worktree. Deleting a worktree while a Claude session has its CWD inside
			// it causes permanent Bash tool failure for that session.
			if isSessionActive(wtDir) {
				log.Warn("worktree: stale worktree has active session — waiting before removal",
					"path", wtDir, "maxWait", sessionWaitMaxDuration)
				deadline := time.Now().Add(sessionWaitMaxDuration)
				for time.Now().Before(deadline) {
					time.Sleep(sessionWaitPollInterval)
					if !isSessionActive(wtDir) {
						break
					}
				}
				if isSessionActive(wtDir) {
					return "", fmt.Errorf("worktree: active session still running in %s after %v; refusing to remove", wtDir, sessionWaitMaxDuration)
				}
				log.Info("worktree: stale session finished, proceeding with worktree removal", "path", wtDir)
			}
			log.Warn("worktree: removing stale worktree", "path", wtDir)
			oldBranch := resolveBranch(wtDir)
			wm.forceRemove(repoDir, wtDir, oldBranch)
		}
	}

	// Detect base branch to branch from.
	baseBranch := DetectDefaultBranch(repoDir)

	// Create worktree: git worktree add -b {branch} {path} {base}
	// Retry up to 3 times with exponential backoff to handle transient git failures.
	const maxCreateAttempts = 3
	backoffs := []time.Duration{100 * time.Millisecond, 500 * time.Millisecond, time.Second}
	var lastCreateErr error
	for attempt := 1; attempt <= maxCreateAttempts; attempt++ {
		if attempt > 1 {
			time.Sleep(backoffs[attempt-2])
		}
		out, err := exec.Command("git", "-C", repoDir,
			"worktree", "add", "-b", branch, wtDir, baseBranch).CombinedOutput()
		if err == nil {
			lastCreateErr = nil
			break
		}
		errMsg := strings.TrimSpace(string(out))
		lastCreateErr = fmt.Errorf("worktree: git worktree add failed: %s: %w", errMsg, err)

		// Capture diagnostics on failure.
		gitVerOut, _ := exec.Command("git", "version").Output()
		repoStatusOut, _ := exec.Command("git", "-C", repoDir, "status", "--short").Output()
		log.Warn("worktree: git worktree add failed, capturing diagnostics",
			"attempt", attempt,
			"maxAttempts", maxCreateAttempts,
			"error", errMsg,
			"gitVersion", strings.TrimSpace(string(gitVerOut)),
			"repoStatus", strings.TrimSpace(string(repoStatusOut)),
			"repoDir", repoDir,
			"wtDir", wtDir,
			"branch", branch,
		)

		// If the failure looks like a corrupt/stale master ref (e.g. packed-refs
		// inconsistency after a crashed fetch), try a best-effort repair before
		// the next attempt.
		if isMasterRefError(errMsg) {
			log.Warn("worktree: master-ref error detected, attempting repair",
				"repoDir", repoDir, "error", errMsg)
			repairMasterRef(repoDir)
		}
	}
	if lastCreateErr != nil {
		// Mark the worktree dir as unsafe so the next Create() for this taskID
		// force-cleans instead of waiting on a phantom session or re-using a
		// half-initialized directory.
		MarkUnsafe(wtDir, lastCreateErr.Error())
		return "", lastCreateErr
	}

	// Write branch metadata so Remove/Merge can find the branch name.
	writeBranchMeta(wtDir, branch)

	// Write session lock immediately after creation so that a concurrent Create()
	// call for the same taskID cannot delete this worktree before the caller has
	// a chance to write its own lock. The caller is responsible for removing this
	// file when the session ends (typically via os.Remove in a defer).
	if err := os.WriteFile(filepath.Join(wtDir, sessionLockFile),
		[]byte(fmt.Sprintf("%d\n", os.Getpid())), 0o644); err != nil {
		log.Debug("worktree: failed to write session lock after create",
			"path", wtDir, "error", err)
	}

	log.Info("worktree: created", "task", taskID, "path", wtDir, "branch", branch, "base", baseBranch)
	return wtDir, nil
}

// Remove cleans up a worktree with the 4-step sequence from Vibe Kanban:
// 1. git worktree remove --force
// 2. force cleanup .git/worktrees metadata
// 3. rm -rf worktree directory
// 4. git worktree prune
//
// Remove refuses to delete a worktree that still has an active Claude session
// (i.e. the session lock file is present and its PID is alive). Callers must
// release the session lock (via the function returned by AcquireSessionLock)
// before calling Remove, or the call will return an error.
func (wm *WorktreeManager) Remove(repoDir, wtDir string) error {
	mu := wm.pathLock(wtDir)
	mu.Lock()
	defer mu.Unlock()

	// Guard: refuse to delete a worktree whose session lock is still held.
	// Removing the CWD of a running Claude session permanently breaks the
	// session's Bash tool. The caller must release its lock before Remove.
	if isSessionActive(wtDir) {
		return fmt.Errorf("worktree: active session in %s; release session lock before Remove", wtDir)
	}

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
	if out, mergeErr := exec.Command("git", "-C", repoDir,
		"merge", "--no-ff", branch, "-m",
		fmt.Sprintf("Merge %s into %s", branch, targetBranch)).CombinedOutput(); mergeErr != nil {
		origErr := fmt.Errorf("worktree: merge failed: %s: %w",
			strings.TrimSpace(string(out)), mergeErr)
		if resolved, resolveErr := tryAutoResolveMetaConflict(repoDir); resolved {
			log.Info("worktree: auto-resolved .tetora-branch conflict", "branch", branch, "task", taskID)
		} else {
			_ = resolveErr
			return diffSummary, origErr
		}
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
			// Skip worktrees with a live session — removing them would permanently
			// break the session's Bash tool. They will be cleaned up on the next
			// prune cycle once the session completes.
			if isSessionActive(info.Path) {
				log.Warn("worktree: skipping prune — active session detected",
					"path", info.Path,
					"age", time.Since(info.CreatedAt).Round(time.Minute))
				continue
			}
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
		origErr := fmt.Errorf("worktree: merge failed: %s: %w",
			strings.TrimSpace(string(out)), mergeErr)
		if resolved, resolveErr := tryAutoResolveMetaConflict(repoDir); resolved {
			log.Info("worktree: auto-resolved .tetora-branch conflict (branch-only)", "branch", branch, "task", taskID)
		} else {
			_ = resolveErr
			return diffSummary, origErr
		}
	}

	log.Info("worktree: merged (branch-only)", "branch", branch, "into", targetBranch, "task", taskID)
	return diffSummary, nil
}

// tryAutoResolveMetaConflict checks if the only merge conflict is .tetora-branch
// and resolves it by keeping ours (the target branch version).
// Returns (true, nil) if resolved, (false, err) if conflicts involve other files.
func tryAutoResolveMetaConflict(repoDir string) (resolved bool, err error) {
	out, err := exec.Command("git", "-C", repoDir,
		"diff", "--name-only", "--diff-filter=U").Output()
	if err != nil {
		return false, fmt.Errorf("worktree: failed to list conflicted files: %w", err)
	}

	conflicted := strings.Fields(strings.TrimSpace(string(out)))
	if len(conflicted) != 1 || conflicted[0] != branchMetaFile {
		// More than one conflict, or not the meta file — abort and report.
		abortOut, abortErr := exec.Command("git", "-C", repoDir, "merge", "--abort").CombinedOutput()
		if abortErr != nil {
			return false, fmt.Errorf("worktree: merge --abort failed: %s: %w",
				strings.TrimSpace(string(abortOut)), abortErr)
		}
		if len(conflicted) == 0 {
			return false, fmt.Errorf("worktree: merge conflict with no conflicted files listed")
		}
		return false, fmt.Errorf("worktree: unresolvable conflicts in: %s", strings.Join(conflicted, ", "))
	}

	// Only .tetora-branch is conflicted — resolve by keeping ours.
	if out, err := exec.Command("git", "-C", repoDir,
		"checkout", "--ours", branchMetaFile).CombinedOutput(); err != nil {
		return false, fmt.Errorf("worktree: checkout --ours %s failed: %s: %w",
			branchMetaFile, strings.TrimSpace(string(out)), err)
	}
	if out, err := exec.Command("git", "-C", repoDir,
		"add", branchMetaFile).CombinedOutput(); err != nil {
		return false, fmt.Errorf("worktree: git add %s failed: %s: %w",
			branchMetaFile, strings.TrimSpace(string(out)), err)
	}
	if out, err := exec.Command("git", "-C", repoDir,
		"commit", "--no-edit").CombinedOutput(); err != nil {
		return false, fmt.Errorf("worktree: commit after meta resolve failed: %s: %w",
			strings.TrimSpace(string(out)), err)
	}
	return true, nil
}
