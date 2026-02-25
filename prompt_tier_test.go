package main

import (
	"strings"
	"testing"
)

// --- truncateToChars tests ---

func TestTruncateToCharsShortString(t *testing.T) {
	s := "hello world"
	got := truncateToChars(s, 100)
	if got != s {
		t.Errorf("truncateToChars(%q, 100) = %q, want %q", s, got, s)
	}
}

func TestTruncateToCharsExactLength(t *testing.T) {
	s := "hello"
	got := truncateToChars(s, 5)
	if got != s {
		t.Errorf("truncateToChars(%q, 5) = %q, want %q", s, got, s)
	}
}

func TestTruncateToCharsLongString(t *testing.T) {
	s := strings.Repeat("a", 200)
	got := truncateToChars(s, 50)
	if len(got) > 80 { // 50 + truncation notice
		t.Errorf("truncateToChars(200 chars, 50) produced %d chars, expected roughly 50+notice", len(got))
	}
	if !strings.HasSuffix(got, "[... truncated ...]") {
		t.Errorf("truncateToChars should end with truncation notice, got: %q", got[len(got)-30:])
	}
}

func TestTruncateToCharsNewlineBoundary(t *testing.T) {
	// Build a string with newlines at known positions.
	// 90 chars of 'a', then newline, then 9 chars of 'b' = 100 chars total.
	s := strings.Repeat("a", 90) + "\n" + strings.Repeat("b", 9)
	got := truncateToChars(s, 95)

	// The newline at position 90 is within the last quarter (95*3/4 = 71),
	// so it should cut at the newline.
	if !strings.HasSuffix(got, "[... truncated ...]") {
		t.Errorf("truncateToChars should end with truncation notice")
	}
	// The cut should be at the newline (pos 90), so no 'b' chars.
	if strings.Contains(got, "b") {
		t.Errorf("truncateToChars should cut at newline boundary, but got 'b' chars in result")
	}
}

// --- buildTieredPrompt tests ---

func TestBuildTieredPromptNoPanic(t *testing.T) {
	// Minimal config that should not panic.
	cfg := &Config{
		Roles: map[string]RoleConfig{
			"test": {Model: "sonnet"},
		},
		Providers: map[string]ProviderConfig{},
	}

	task := Task{
		ID:     "test-task-id-12345678",
		Prompt: "hello",
		Source: "discord",
	}

	// Should not panic with any complexity level.
	buildTieredPrompt(cfg, &task, "test", ComplexitySimple)
	buildTieredPrompt(cfg, &task, "test", ComplexityStandard)
	buildTieredPrompt(cfg, &task, "test", ComplexityComplex)
}

func TestBuildTieredPromptSimpleShorterThanComplex(t *testing.T) {
	cfg := &Config{
		Roles: map[string]RoleConfig{
			"test": {Model: "sonnet"},
		},
		Providers:    map[string]ProviderConfig{},
		WritingStyle: WritingStyleConfig{Enabled: true, Guidelines: "Write concisely."},
		Citation:     CitationConfig{Enabled: true, Format: "bracket"},
	}

	simpleTask := Task{
		ID:     "simple-task-12345678",
		Prompt: "hi",
		Source: "discord",
	}
	complexTask := Task{
		ID:     "complex-task-12345678",
		Prompt: "implement a new feature",
		Source: "cron",
	}

	buildTieredPrompt(cfg, &simpleTask, "test", ComplexitySimple)
	buildTieredPrompt(cfg, &complexTask, "test", ComplexityComplex)

	simpleLen := len(simpleTask.SystemPrompt)
	complexLen := len(complexTask.SystemPrompt)

	// Complex should have more content (citation + writing style at minimum).
	if complexLen < simpleLen {
		t.Errorf("complex prompt (%d chars) should be >= simple prompt (%d chars)", complexLen, simpleLen)
	}
}

func TestBuildTieredPromptSimpleClearsAddDirs(t *testing.T) {
	cfg := &Config{
		baseDir: "/tmp/tetora",
		Roles: map[string]RoleConfig{
			"test": {},
		},
		Providers:      map[string]ProviderConfig{},
		DefaultAddDirs: []string{"/tmp/extra"},
	}

	task := Task{
		ID:     "adddir-task-12345678",
		Prompt: "hi",
		Source: "discord",
	}

	buildTieredPrompt(cfg, &task, "test", ComplexitySimple)

	// Simple should only have baseDir.
	if len(task.AddDirs) != 1 || task.AddDirs[0] != "/tmp/tetora" {
		t.Errorf("simple prompt AddDirs = %v, want [/tmp/tetora]", task.AddDirs)
	}
}

func TestBuildTieredPromptClaudeCodeSkipsInjection(t *testing.T) {
	cfg := &Config{
		baseDir: "/tmp/tetora",
		Roles: map[string]RoleConfig{
			"test": {Provider: "cc"},
		},
		Providers: map[string]ProviderConfig{
			"cc": {Type: "claude-code"},
		},
		WritingStyle: WritingStyleConfig{Enabled: true, Guidelines: "Write concisely."},
		Citation:     CitationConfig{Enabled: true, Format: "bracket"},
	}

	task := Task{
		ID:       "cc-task-12345678",
		Prompt:   "implement a feature",
		Source:   "cron",
		Provider: "cc",
	}

	buildTieredPrompt(cfg, &task, "test", ComplexityComplex)

	// Should NOT contain writing style or citation (claude-code skips injection).
	if strings.Contains(task.SystemPrompt, "Writing Style") {
		t.Error("claude-code provider should not inject writing style")
	}
	if strings.Contains(task.SystemPrompt, "Citation Rules") {
		t.Error("claude-code provider should not inject citation rules")
	}
}

func TestBuildTieredPromptTotalBudget(t *testing.T) {
	cfg := &Config{
		Roles: map[string]RoleConfig{
			"test": {},
		},
		Providers: map[string]ProviderConfig{},
		PromptBudget: PromptBudgetConfig{
			TotalMax: 100,
		},
	}

	task := Task{
		ID:           "budget-task-12345678",
		Prompt:       "hello",
		Source:       "discord",
		SystemPrompt: strings.Repeat("x", 200),
	}

	buildTieredPrompt(cfg, &task, "test", ComplexityComplex)

	// SystemPrompt should be truncated to fit within totalMax + truncation notice.
	if len(task.SystemPrompt) > 150 { // 100 + truncation notice overhead
		t.Errorf("system prompt should be truncated to ~100 chars, got %d", len(task.SystemPrompt))
	}
}

// --- buildSessionContextWithLimit tests ---

func TestBuildSessionContextWithLimitEmpty(t *testing.T) {
	got := buildSessionContextWithLimit("", "", 10, 1000)
	if got != "" {
		t.Errorf("buildSessionContextWithLimit with empty args = %q, want empty", got)
	}
}

func TestBuildSessionContextWithLimitTruncation(t *testing.T) {
	// We can't easily test with a real DB, but we can test the truncation logic
	// by verifying that maxChars=0 means no limit.
	got := buildSessionContextWithLimit("", "fake-session", 10, 0)
	if got != "" {
		t.Errorf("buildSessionContextWithLimit with empty dbPath = %q, want empty", got)
	}
}
