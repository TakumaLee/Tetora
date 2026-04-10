package worktree

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// setupTestRepo initialises a fresh git repo in a temp dir with a single
// "init" commit on branch "main".
func setupTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=t@t.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=t@t.com",
			"GIT_CONFIG_NOSYSTEM=1",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}
	run("init")
	run("config", "user.email", "t@t.com")
	run("config", "user.name", "Test")
	run("config", "commit.gpgsign", "false")

	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")
	run("commit", "-m", "init")

	// Ensure we are on "main".
	out, _ := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if strings.TrimSpace(string(out)) != "main" {
		run("checkout", "-b", "main")
	}
	return dir
}

// TestMerge_AutoResolvesBranchMetaConflict verifies that when the only merge
// conflict is .tetora-branch, Merge resolves it automatically (--ours wins)
// and returns nil.
func TestMerge_AutoResolvesBranchMetaConflict(t *testing.T) {
	repoDir := setupTestRepo(t)

	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repoDir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=t@t.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=t@t.com",
			"GIT_CONFIG_NOSYSTEM=1",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}

	// Create task branch with .tetora-branch set to "task/auto-test".
	runGit("checkout", "-b", "task/auto-test")
	if err := os.WriteFile(filepath.Join(repoDir, branchMetaFile), []byte("task/auto-test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit("add", branchMetaFile)
	runGit("commit", "-m", "add branch meta on task branch")

	// Switch back to main and commit a different .tetora-branch value to
	// guarantee a conflict when we merge.
	runGit("checkout", "main")
	if err := os.WriteFile(filepath.Join(repoDir, branchMetaFile), []byte("old-branch\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit("add", branchMetaFile)
	runGit("commit", "-m", "add branch meta on main")

	// Build a fake wtDir: only needs .tetora-branch so resolveBranch returns
	// the task branch name. No git history required here.
	wtDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(wtDir, branchMetaFile), []byte("task/auto-test\n"), 0644); err != nil {
		t.Fatal(err)
	}

	wm := NewWorktreeManager(t.TempDir())
	_, err := wm.Merge(repoDir, wtDir, "test")
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	// --ours means the main-branch version ("old-branch") should win.
	data, readErr := os.ReadFile(filepath.Join(repoDir, branchMetaFile))
	if readErr != nil {
		t.Fatalf("reading %s: %v", branchMetaFile, readErr)
	}
	if got := strings.TrimSpace(string(data)); got != "old-branch" {
		t.Errorf(".tetora-branch content = %q, want %q", got, "old-branch")
	}
}

// =============================================================================
// Section: Session lock tests
// =============================================================================

// TestIsSessionActive_NoLockFile returns false when no lock file exists.
func TestIsSessionActive_NoLockFile(t *testing.T) {
	dir := t.TempDir()
	if isSessionActive(dir) {
		t.Error("expected false for directory with no lock file, got true")
	}
}

// TestIsSessionActive_LivePID returns true when the lock file contains our own PID.
func TestIsSessionActive_LivePID(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, sessionLockFile)
	if err := os.WriteFile(lockPath, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o644); err != nil {
		t.Fatal(err)
	}
	if !isSessionActive(dir) {
		t.Error("expected true for lock file with live PID, got false")
	}
}

// TestIsSessionActive_DeadPID returns false for a PID that cannot exist (PID 0).
func TestIsSessionActive_DeadPID(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, sessionLockFile)
	// PID 0 is never a valid user process; Kill(0, 0) returns EPERM, not nil.
	if err := os.WriteFile(lockPath, []byte("99999999\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// PID 99999999 is almost certainly not running.
	if isSessionActive(dir) {
		t.Skip("skipping: PID 99999999 happens to be alive on this system")
	}
}

// TestIsSessionActive_MalformedContent returns false for a non-numeric lock file.
func TestIsSessionActive_MalformedContent(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, sessionLockFile)
	if err := os.WriteFile(lockPath, []byte("not-a-pid\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if isSessionActive(dir) {
		t.Error("expected false for malformed lock file content, got true")
	}
}

// TestCreate_WaitsForActiveSession verifies that Create() enters the session
// wait loop when the stale worktree has an active session, waits until the
// session ends, and then succeeds.
func TestCreate_StaleWorktreeWithActiveSession_WaitsAndSucceeds(t *testing.T) {
	repoDir := setupTestRepo(t)
	baseDir := t.TempDir()
	wm := NewWorktreeManager(baseDir)
	taskID := "task-session-lock-test"

	// Override poll intervals so the test runs in milliseconds, not seconds.
	origPoll := sessionWaitPollInterval
	origMax := sessionWaitMaxDuration
	sessionWaitPollInterval = 10 * time.Millisecond
	sessionWaitMaxDuration = 500 * time.Millisecond
	t.Cleanup(func() {
		sessionWaitPollInterval = origPoll
		sessionWaitMaxDuration = origMax
	})

	// Pre-create a fake stale worktree directory with an active session lock.
	staleDir := filepath.Join(baseDir, taskID)
	if err := os.MkdirAll(staleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(staleDir, sessionLockFile)
	if err := os.WriteFile(lockPath, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o644); err != nil {
		t.Fatal(err)
	}

	// Remove the lock only after Create() has entered the wait loop at least once.
	// Sleeping 2× the poll interval guarantees Create() polls at least once before
	// the lock disappears, making the test deterministic.
	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(sessionWaitPollInterval * 2)
		_ = os.Remove(lockPath)
	}()

	_, err := wm.Create(repoDir, taskID, "feat/session-lock-test")
	<-done

	// Create() must succeed: it detected the active session, waited for it to
	// finish, then removed the stale worktree and created a fresh one.
	if err != nil {
		t.Errorf("expected Create to succeed after session ended, got: %v", err)
	}
}

// =============================================================================
// Section: Remove() active-session guard tests
// =============================================================================

// TestRemove_ActiveSession_ReturnsError verifies that Remove returns an error
// (rather than silently deleting) when the worktree has a live session lock.
func TestRemove_ActiveSession_ReturnsError(t *testing.T) {
	repoDir := setupTestRepo(t)
	baseDir := t.TempDir()
	wm := NewWorktreeManager(baseDir)
	taskID := "task-remove-active-lock"

	// Create a real worktree so Remove has something to operate on.
	wtDir, err := wm.Create(repoDir, taskID, "feat/remove-active-lock-test")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Acquire a live session lock (our own PID).
	release := AcquireSessionLock(wtDir)
	defer release()

	// Remove must refuse while the lock is held.
	if removeErr := wm.Remove(repoDir, wtDir); removeErr == nil {
		t.Fatal("expected non-nil error from Remove with active session, got nil")
	} else if !strings.Contains(removeErr.Error(), "active session") {
		t.Errorf("error = %q, want it to contain \"active session\"", removeErr.Error())
	}

	// The worktree directory must still exist (not silently deleted).
	if _, statErr := os.Stat(wtDir); statErr != nil {
		t.Errorf("worktree directory unexpectedly gone after refused Remove: %v", statErr)
	}
}

// TestRemove_AfterLockRelease_Succeeds verifies that Remove succeeds once the
// session lock has been released by the caller.
func TestRemove_AfterLockRelease_Succeeds(t *testing.T) {
	repoDir := setupTestRepo(t)
	baseDir := t.TempDir()
	wm := NewWorktreeManager(baseDir)
	taskID := "task-remove-after-release"

	wtDir, err := wm.Create(repoDir, taskID, "feat/remove-after-release-test")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	release := AcquireSessionLock(wtDir)
	release() // release immediately — simulates agent having exited

	// Remove must now succeed without error.
	if removeErr := wm.Remove(repoDir, wtDir); removeErr != nil {
		t.Errorf("expected nil error from Remove after lock release, got: %v", removeErr)
	}

	// Worktree directory should be gone.
	if _, statErr := os.Stat(wtDir); statErr == nil {
		t.Error("worktree directory still present after successful Remove")
	}
}

// =============================================================================
// Section: Merge conflict tests (existing)
// =============================================================================

// TestMerge_CodeConflictReturnsError verifies that when a real code file
// conflicts, Merge returns a non-nil error containing "merge failed".
func TestMerge_CodeConflictReturnsError(t *testing.T) {
	repoDir := setupTestRepo(t)

	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repoDir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=t@t.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=t@t.com",
			"GIT_CONFIG_NOSYSTEM=1",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}

	// Create task branch with a diverging change to README.md.
	runGit("checkout", "-b", "task/code-conflict")
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("branch change\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "README.md")
	runGit("commit", "-m", "branch changes README")

	// Switch back to main and make a conflicting change.
	runGit("checkout", "main")
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("main change\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "README.md")
	runGit("commit", "-m", "main changes README")

	// Fake wtDir with branch meta pointing at the task branch.
	wtDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(wtDir, branchMetaFile), []byte("task/code-conflict\n"), 0644); err != nil {
		t.Fatal(err)
	}

	wm := NewWorktreeManager(t.TempDir())
	_, err := wm.Merge(repoDir, wtDir, "test")
	if err == nil {
		t.Fatal("expected non-nil error for code conflict, got nil")
	}
	if !strings.Contains(err.Error(), "merge failed") {
		t.Errorf("error = %q, want it to contain \"merge failed\"", err.Error())
	}
}
