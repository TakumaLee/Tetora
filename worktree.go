package main

// worktree.go — thin shim over internal/worktree.
//
// Types are aliases so callers (including package main tests) see no type change.
// Unexported helper shims (buildBranchName, slugifyBranch, isGitRepo, detectDefaultBranch)
// are kept here because worktree_test.go (package main) references them directly.

import (
	"tetora/internal/config"
	"tetora/internal/taskboard"
	"tetora/internal/worktree"
)

// --- Type aliases ---

type WorktreeManager = worktree.WorktreeManager
type WorktreeInfo = worktree.WorktreeInfo

// --- Constructor shim ---

func NewWorktreeManager(baseDir string) *WorktreeManager {
	return worktree.NewWorktreeManager(baseDir)
}

// --- Unexported shims for package main tests and internal callers ---

func buildBranchName(cfg config.GitWorkflowConfig, t taskboard.TaskBoard) string {
	return worktree.BuildBranchName(cfg, t)
}

func slugify(s string) string {
	return worktree.Slugify(s)
}

func slugifyBranch(s string) string {
	return worktree.SlugifyBranch(s)
}

func isGitRepo(dir string) bool {
	return worktree.IsGitRepo(dir)
}
