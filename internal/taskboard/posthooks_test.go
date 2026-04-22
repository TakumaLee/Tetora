package taskboard

import (
	"testing"

	"tetora/internal/config"
)

func TestResolveBaseBranch_UsesConfiguredOverride(t *testing.T) {
	cfg := config.GitWorkflowConfig{BaseBranch: "develop"}
	if got := ResolveBaseBranch(cfg, t.TempDir()); got != "develop" {
		t.Fatalf("ResolveBaseBranch() = %q, want %q", got, "develop")
	}
}

func TestResolveBaseBranch_TrimmedOverride(t *testing.T) {
	cfg := config.GitWorkflowConfig{BaseBranch: "  release/1.x  "}
	if got := ResolveBaseBranch(cfg, t.TempDir()); got != "release/1.x" {
		t.Fatalf("ResolveBaseBranch() = %q, want %q", got, "release/1.x")
	}
}

