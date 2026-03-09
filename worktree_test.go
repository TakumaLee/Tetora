package main

import "testing"

func TestBuildBranchName_Default(t *testing.T) {
	cfg := GitWorkflowConfig{}
	task := TaskBoard{
		Type:     "feat",
		Assignee: "kokuyou",
		Title:    "Codex CLI Provider",
		ID:       "task-123",
	}
	got := buildBranchName(cfg, task)
	if got != "feat/kokuyou-codex-cli-provider" {
		t.Errorf("expected feat/kokuyou-codex-cli-provider, got %s", got)
	}
}

func TestBuildBranchName_CustomConvention(t *testing.T) {
	cfg := GitWorkflowConfig{
		BranchConvention: "{type}/{description}",
	}
	task := TaskBoard{
		Type:     "fix",
		Assignee: "kokuyou",
		Title:    "Cron timezone bug",
	}
	got := buildBranchName(cfg, task)
	if got != "fix/cron-timezone-bug" {
		t.Errorf("expected fix/cron-timezone-bug, got %s", got)
	}
}

func TestBuildBranchName_WithTaskId(t *testing.T) {
	cfg := GitWorkflowConfig{
		BranchConvention: "{type}/{taskId}-{description}",
	}
	task := TaskBoard{
		Type:  "refactor",
		Title: "Dispatch cleanup",
		ID:    "task-456",
	}
	got := buildBranchName(cfg, task)
	if got != "refactor/task-456-dispatch-cleanup" {
		t.Errorf("expected refactor/task-456-dispatch-cleanup, got %s", got)
	}
}

func TestBuildBranchName_DefaultType(t *testing.T) {
	cfg := GitWorkflowConfig{
		DefaultType: "chore",
	}
	task := TaskBoard{
		Assignee: "ruri",
		Title:    "Update dependencies",
	}
	got := buildBranchName(cfg, task)
	if got != "chore/ruri-update-dependencies" {
		t.Errorf("expected chore/ruri-update-dependencies, got %s", got)
	}
}

func TestBuildBranchName_FallbackType(t *testing.T) {
	cfg := GitWorkflowConfig{}
	task := TaskBoard{
		Assignee: "hisui",
		Title:    "Add feature",
	}
	got := buildBranchName(cfg, task)
	if got != "feat/hisui-add-feature" {
		t.Errorf("expected feat/hisui-add-feature, got %s", got)
	}
}

func TestBuildBranchName_NoAssignee(t *testing.T) {
	cfg := GitWorkflowConfig{}
	task := TaskBoard{
		Type:  "fix",
		Title: "Fix something",
	}
	got := buildBranchName(cfg, task)
	if got != "fix/anon-fix-something" {
		t.Errorf("expected fix/anon-fix-something, got %s", got)
	}
}

func TestSlugifyBranch_Basic(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Codex CLI Provider", "codex-cli-provider"},
		{"Fix cron timezone", "fix-cron-timezone"},
		{"Hello World!", "hello-world"},
		{"multiple   spaces", "multiple-spaces"},
		{"CamelCase Test", "camelcase-test"},
		{"", ""},
		{"feat: add new thing", "feat-add-new-thing"},
	}
	for _, tt := range tests {
		got := slugifyBranch(tt.input)
		if got != tt.want {
			t.Errorf("slugifyBranch(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSlugifyBranch_LongTitle(t *testing.T) {
	input := "This is a very long title that should be truncated to forty characters maximum"
	got := slugifyBranch(input)
	if len(got) > 40 {
		t.Errorf("slugifyBranch produced %d chars (>40): %s", len(got), got)
	}
	if got == "" {
		t.Error("slugifyBranch should produce non-empty result for long title")
	}
}
