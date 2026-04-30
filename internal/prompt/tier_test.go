package prompt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tetora/internal/classify"
	"tetora/internal/config"
	"tetora/internal/dispatch"
)

// minimalDeps returns a Deps with no-op stubs suitable for unit tests.
// Override individual fields as needed.
func minimalDeps(providerName string) Deps {
	return Deps{
		ResolveProviderName:    func(_ *config.Config, _ dispatch.Task, _ string) string { return providerName },
		LoadSoulFile:           func(_ *config.Config, _ string) string { return "" },
		LoadAgentPrompt:        func(_ *config.Config, _ string) (string, error) { return "", nil },
		ResolveWorkspace:       func(_ *config.Config, _ string) config.WorkspaceConfig { return config.WorkspaceConfig{} },
		BuildReflectionContext: func(_, _ string, _ int) string { return "" },
		LoadWritingStyle:       func(_ *config.Config) string { return "" },
		BuildSkillsPrompt:      func(_ *config.Config, _ dispatch.Task, _ classify.Complexity) string { return "" },
		CollectSkillAllowedTools: func(_ *config.Config, _ dispatch.Task) []string { return nil },
		InjectWorkspaceContent: func(_ *config.Config, _ *dispatch.Task, _ string) {},
		EstimateDirSize:        func(_ string) int { return 0 },
	}
}

func minimalCfg() *config.Config {
	return &config.Config{}
}

// TestSkillExtractionInSystemPrompt_Standard checks that standard-complexity dispatch
// injects the Post-Task Skill Extraction section into the system prompt.
func TestSkillExtractionInSystemPrompt_Standard(t *testing.T) {
	cfg := minimalCfg()
	task := &dispatch.Task{Prompt: "do the thing"}
	BuildTieredPrompt(cfg, task, "", classify.Standard, minimalDeps("openai"))

	if !strings.Contains(task.SystemPrompt, "<!-- Post-Task Skill Extraction") {
		t.Error("standard dispatch: system prompt missing Post-Task Skill Extraction section")
	}
	if !strings.Contains(task.SystemPrompt, "5+ tool calls") {
		t.Error("standard dispatch: system prompt missing trigger conditions")
	}
	if !strings.Contains(task.SystemPrompt, "SKILL.md + metadata.json") {
		t.Error("standard dispatch: system prompt missing format instruction")
	}
}

// TestSkillExtractionInSystemPrompt_Complex checks that complex-complexity dispatch
// also injects the skill extraction section.
func TestSkillExtractionInSystemPrompt_Complex(t *testing.T) {
	cfg := minimalCfg()
	task := &dispatch.Task{Prompt: "do the complex thing"}
	BuildTieredPrompt(cfg, task, "", classify.Complex, minimalDeps("openai"))

	if !strings.Contains(task.SystemPrompt, "<!-- Post-Task Skill Extraction") {
		t.Error("complex dispatch: system prompt missing Post-Task Skill Extraction section")
	}
}

// TestSkillExtractionAbsent_Simple verifies simple tasks do NOT get the extraction section.
func TestSkillExtractionAbsent_Simple(t *testing.T) {
	cfg := minimalCfg()
	task := &dispatch.Task{Prompt: "quick lookup"}
	BuildTieredPrompt(cfg, task, "", classify.Simple, minimalDeps("openai"))

	if strings.Contains(task.SystemPrompt, "Post-Task Skill Extraction") {
		t.Error("simple dispatch: system prompt must not contain Post-Task Skill Extraction")
	}
}

// TestSkillExtractionInUserPrompt_ClaudeCode verifies that claude-code provider
// gets the skill extraction hint appended to task.Prompt (not SystemPrompt).
func TestSkillExtractionInUserPrompt_ClaudeCode(t *testing.T) {
	cfg := minimalCfg()
	original := "implement feature X"
	task := &dispatch.Task{Prompt: original}
	BuildTieredPrompt(cfg, task, "", classify.Standard, minimalDeps("claude-code"))

	if !strings.Contains(task.Prompt, "<!-- Post-Task Skill Extraction") {
		t.Error("claude-code dispatch: task.Prompt missing Post-Task Skill Extraction section")
	}
	if !strings.HasPrefix(task.Prompt, original) {
		t.Error("claude-code dispatch: original prompt must be preserved at start")
	}
	if strings.Contains(task.SystemPrompt, "Post-Task Skill Extraction") {
		t.Error("claude-code dispatch: skill extraction must not appear in SystemPrompt")
	}
}

// TestSkillExtractionInUserPrompt_CodexCLI verifies codex-cli provider behaves like claude-code:
// the skill-extraction hint appends to task.Prompt (not SystemPrompt), and the original prompt
// is preserved as a prefix. Provider name "codex" is mapped to providerType "codex-cli" in tier.go.
func TestSkillExtractionInUserPrompt_CodexCLI(t *testing.T) {
	cfg := minimalCfg()
	original := "generate code"
	task := &dispatch.Task{Prompt: original}
	BuildTieredPrompt(cfg, task, "", classify.Standard, minimalDeps("codex"))

	if !strings.Contains(task.Prompt, "<!-- Post-Task Skill Extraction") {
		t.Error("codex-cli dispatch: task.Prompt missing Post-Task Skill Extraction section")
	}
	if !strings.HasPrefix(task.Prompt, original) {
		t.Error("codex-cli dispatch: original prompt must be preserved at start")
	}
	if strings.Contains(task.SystemPrompt, "Post-Task Skill Extraction") {
		t.Error("codex-cli dispatch: skill extraction must not appear in SystemPrompt")
	}
}

// TestPreflightHeaderInjection_PrependedToPrompt verifies that when an agent has a
// preflight-header.md, its content is prepended to task.Prompt before the original prompt.
func TestPreflightHeaderInjection_PrependedToPrompt(t *testing.T) {
	dir := t.TempDir()
	agentName := "tekkou"
	if err := os.MkdirAll(filepath.Join(dir, "agents", agentName), 0o755); err != nil {
		t.Fatal(err)
	}
	preflightContent := "⛔ PRE-FLIGHT CHECK（強制執行）\n\nStep 1: Model 驗證"
	if err := os.WriteFile(filepath.Join(dir, "agents", agentName, "preflight-header.md"), []byte(preflightContent), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{BaseDir: dir}
	original := "deploy staging"
	task := &dispatch.Task{Prompt: original}
	BuildTieredPrompt(cfg, task, agentName, classify.Standard, minimalDeps("openai"))

	if !strings.HasPrefix(task.Prompt, preflightContent) {
		t.Errorf("preflight content must be prepended; got prefix: %q", task.Prompt[:min(len(task.Prompt), 60)])
	}
	if !strings.Contains(task.Prompt, original) {
		t.Error("original prompt must be preserved in task.Prompt")
	}
}

// TestPreflightHeaderInjection_AbsentForNoFile verifies no injection when preflight-header.md does not exist.
func TestPreflightHeaderInjection_AbsentForNoFile(t *testing.T) {
	cfg := minimalCfg()
	original := "deploy staging"
	task := &dispatch.Task{Prompt: original}
	BuildTieredPrompt(cfg, task, "tekkou", classify.Standard, minimalDeps("openai"))

	if task.Prompt != original+skillExtractionSection {
		// task.Prompt gets skillExtractionSection appended (non-claude-code path); without preflight it's just that
		if strings.Contains(task.Prompt, "PRE-FLIGHT") {
			t.Error("preflight content must not appear when file is absent")
		}
	}
}

// TestPreflightHeaderInjection_ClaudeCodeProvider verifies hard injection works for claude-code too.
func TestPreflightHeaderInjection_ClaudeCodeProvider(t *testing.T) {
	dir := t.TempDir()
	agentName := "tekkou"
	if err := os.MkdirAll(filepath.Join(dir, "agents", agentName), 0o755); err != nil {
		t.Fatal(err)
	}
	preflightContent := "⛔ PRE-FLIGHT CHECK"
	if err := os.WriteFile(filepath.Join(dir, "agents", agentName, "preflight-header.md"), []byte(preflightContent), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{BaseDir: dir}
	original := "run health check"
	task := &dispatch.Task{Prompt: original}
	BuildTieredPrompt(cfg, task, agentName, classify.Standard, minimalDeps("claude-code"))

	if !strings.HasPrefix(task.Prompt, preflightContent) {
		t.Errorf("claude-code: preflight must be prepended; got prefix: %q", task.Prompt[:min(len(task.Prompt), 60)])
	}
}

// TestScopeBoundary_DiagnosticOnly verifies the SCOPE HEADER is prepended to
// task.Prompt with the ⛔ marker for diagnostic_only tasks.
func TestScopeBoundary_DiagnosticOnly(t *testing.T) {
	cfg := minimalCfg()
	task := &dispatch.Task{Prompt: "audit the db", ScopeBoundary: "diagnostic_only"}
	BuildTieredPrompt(cfg, task, "", classify.Standard, minimalDeps("openai"))

	if !strings.HasPrefix(task.Prompt, "⛔ SCOPE: diagnostic_only") {
		t.Errorf("expected scope header at top of task.Prompt; got prefix: %q", task.Prompt[:min(len(task.Prompt), 80)])
	}
	if !strings.Contains(task.Prompt, "禁止：Edit、Write、git commit") {
		t.Error("diagnostic_only scope must list forbidden tools")
	}
}

// TestScopeBoundary_ClaudeCodeProvider verifies scope injection works for
// claude-code (which returns early from BuildTieredPrompt).
func TestScopeBoundary_ClaudeCodeProvider(t *testing.T) {
	cfg := minimalCfg()
	task := &dispatch.Task{Prompt: "verify config", ScopeBoundary: "review_only"}
	BuildTieredPrompt(cfg, task, "", classify.Standard, minimalDeps("claude-code"))

	if !strings.HasPrefix(task.Prompt, "🔍 SCOPE: review_only") {
		t.Errorf("claude-code: expected scope header; got prefix: %q", task.Prompt[:min(len(task.Prompt), 80)])
	}
}

// TestScopeBoundary_ImplementAllowed verifies the ⚠️ SCOPE header for implement_allowed.
func TestScopeBoundary_ImplementAllowed(t *testing.T) {
	cfg := minimalCfg()
	task := &dispatch.Task{Prompt: "fix the bug", ScopeBoundary: "implement_allowed"}
	BuildTieredPrompt(cfg, task, "", classify.Standard, minimalDeps("openai"))

	if !strings.HasPrefix(task.Prompt, "⚠️ SCOPE: implement_allowed") {
		t.Errorf("expected implement_allowed scope header at top; got prefix: %q", task.Prompt[:min(len(task.Prompt), 80)])
	}
	if !strings.Contains(task.Prompt, "critical_files") {
		t.Error("implement_allowed scope must mention critical_files constraint")
	}
	if task.ScopeBoundary != "implement_allowed" {
		t.Errorf("BuildTieredPrompt must not mutate task.ScopeBoundary; got %q", task.ScopeBoundary)
	}
}

// TestScopeBoundary_TestOnly verifies the ⚠️ SCOPE header for test_only.
func TestScopeBoundary_TestOnly(t *testing.T) {
	cfg := minimalCfg()
	task := &dispatch.Task{Prompt: "write tests", ScopeBoundary: "test_only"}
	BuildTieredPrompt(cfg, task, "", classify.Standard, minimalDeps("openai"))

	if !strings.HasPrefix(task.Prompt, "⚠️ SCOPE: test_only") {
		t.Errorf("expected test_only scope header at top; got prefix: %q", task.Prompt[:min(len(task.Prompt), 80)])
	}
	if !strings.Contains(task.Prompt, "*.test.*") {
		t.Error("test_only scope must mention allowed test file patterns")
	}
	if task.ScopeBoundary != "test_only" {
		t.Errorf("BuildTieredPrompt must not mutate task.ScopeBoundary; got %q", task.ScopeBoundary)
	}
}

// TestScopeBoundary_Empty verifies that an empty ScopeBoundary is a no-op.
func TestScopeBoundary_Empty(t *testing.T) {
	cfg := minimalCfg()
	original := "do the thing"
	task := &dispatch.Task{Prompt: original, ScopeBoundary: ""}
	BuildTieredPrompt(cfg, task, "", classify.Standard, minimalDeps("openai"))

	if strings.Contains(task.Prompt, "SCOPE:") {
		t.Errorf("empty ScopeBoundary must not inject SCOPE header; got: %q", task.Prompt[:min(len(task.Prompt), 80)])
	}
}

// TestScopeBoundary_Unknown verifies unknown values are downgraded to empty (no-op + warning).
func TestScopeBoundary_Unknown(t *testing.T) {
	cfg := minimalCfg()
	task := &dispatch.Task{Prompt: "do the thing", ScopeBoundary: "wildly_unsafe"}
	BuildTieredPrompt(cfg, task, "", classify.Standard, minimalDeps("openai"))

	if strings.Contains(task.Prompt, "SCOPE:") {
		t.Error("unknown ScopeBoundary must not inject any SCOPE header")
	}
	if task.ScopeBoundary != "wildly_unsafe" {
		t.Errorf("BuildTieredPrompt must not mutate task.ScopeBoundary; got %q", task.ScopeBoundary)
	}
}

// TestScopeBoundary_PrependedAfterPreflight verifies the SCOPE HEADER ends up
// at the very top of task.Prompt, overriding the preflight-header injection.
func TestScopeBoundary_PrependedAfterPreflight(t *testing.T) {
	dir := t.TempDir()
	agentName := "tekkou"
	if err := os.MkdirAll(filepath.Join(dir, "agents", agentName), 0o755); err != nil {
		t.Fatal(err)
	}
	preflight := "⛔ PRE-FLIGHT CHECK"
	if err := os.WriteFile(filepath.Join(dir, "agents", agentName, "preflight-header.md"), []byte(preflight), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{BaseDir: dir}
	task := &dispatch.Task{Prompt: "run health check", ScopeBoundary: "diagnostic_only"}
	BuildTieredPrompt(cfg, task, agentName, classify.Standard, minimalDeps("claude-code"))

	if !strings.HasPrefix(task.Prompt, "⛔ SCOPE: diagnostic_only") {
		t.Errorf("SCOPE header must be at top; got prefix: %q", task.Prompt[:min(len(task.Prompt), 80)])
	}
	if !strings.Contains(task.Prompt, preflight) {
		t.Error("preflight content must still be present in prompt")
	}
}

// TestSkillExtractionSection_UsesHTMLComment guards the HTML-comment wrapping of
// skillExtractionSection. The wrapper is the filter mechanism: agents read and
// obey the instructions but must not echo "## Post-Task Skill Extraction" as a
// visible markdown header in their output (see workspace CLAUDE.md HTML Comment
// Rule + LRN-20260419-001). Regression: this test fails if the constant reverts
// to a markdown-header format.
func TestSkillExtractionSection_UsesHTMLComment(t *testing.T) {
	if strings.Contains(skillExtractionSection, "## Post-Task Skill Extraction") {
		t.Error("skillExtractionSection must not use markdown-header format '## Post-Task Skill Extraction'; use HTML comment to prevent agents echoing the section header")
	}
	if !strings.Contains(skillExtractionSection, "<!-- Post-Task Skill Extraction") {
		t.Error("skillExtractionSection must use HTML comment format '<!-- Post-Task Skill Extraction ... -->' so the instruction is hidden from rendered agent output")
	}
	if !strings.Contains(skillExtractionSection, "-->") {
		t.Error("skillExtractionSection must terminate the HTML comment with '-->' to avoid unbounded comment leaking into downstream prompt content")
	}
}

// TestBuildTieredPrompt_NoMarkdownSectionHeader exercises the dispatch paths
// that inject skillExtractionSection and asserts none of them produce a visible
// '## Post-Task Skill Extraction' markdown header in the final prompt surfaces.
// Covers: openai provider Standard (system prompt), claude-code provider
// Standard (user prompt), openai provider Complex (system prompt).
func TestBuildTieredPrompt_NoMarkdownSectionHeader(t *testing.T) {
	cases := []struct {
		name       string
		provider   string
		complexity classify.Complexity
		field      func(*dispatch.Task) string
	}{
		{
			name:       "openai+Standard injects into SystemPrompt without markdown header",
			provider:   "openai",
			complexity: classify.Standard,
			field:      func(task *dispatch.Task) string { return task.SystemPrompt },
		},
		{
			name:       "claude-code+Standard injects into Prompt without markdown header",
			provider:   "claude-code",
			complexity: classify.Standard,
			field:      func(task *dispatch.Task) string { return task.Prompt },
		},
		{
			name:       "openai+Complex injects into SystemPrompt without markdown header",
			provider:   "openai",
			complexity: classify.Complex,
			field:      func(task *dispatch.Task) string { return task.SystemPrompt },
		},
		{
			name:       "codex+Standard injects into Prompt without markdown header",
			provider:   "codex",
			complexity: classify.Standard,
			field:      func(task *dispatch.Task) string { return task.Prompt },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := minimalCfg()
			task := &dispatch.Task{Prompt: "do the thing"}
			BuildTieredPrompt(cfg, task, "", tc.complexity, minimalDeps(tc.provider))

			target := tc.field(task)
			// Sanity: the extraction section should actually be present in the expected
			// target surface, otherwise the negative assertion below is vacuous.
			if !strings.Contains(target, "<!-- Post-Task Skill Extraction") {
				t.Fatalf("precondition: target surface missing skill-extraction block; got: %q", target)
			}
			if strings.Contains(target, "## Post-Task Skill Extraction") {
				t.Errorf("target surface must not contain markdown-header form '## Post-Task Skill Extraction' (regression: skillExtractionSection reverted to markdown-header format)")
			}
		})
	}
}

func TestNeedsWorkspaceRuleInjection(t *testing.T) {
	cases := []struct {
		providerType string
		want         bool
	}{
		// Terminal/CLI providers — must NOT inject
		{"claude-code", false},
		{"codex-cli", false},
		{"qwen-cli", false},
		{"terminal-bash", false},
		{"terminal-zsh", false},
		{"terminal-", false},
		// API providers — must inject
		{"anthropic", true},
		{"openai-compatible", true},
		{"google", true},
		{"groq", true},
		{"", true},
		{"unknown-provider", true},
	}

	for _, tc := range cases {
		t.Run(tc.providerType, func(t *testing.T) {
			got := needsWorkspaceRuleInjection(tc.providerType)
			if got != tc.want {
				t.Errorf("needsWorkspaceRuleInjection(%q) = %v, want %v", tc.providerType, got, tc.want)
			}
		})
	}
}
