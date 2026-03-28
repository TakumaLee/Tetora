package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
