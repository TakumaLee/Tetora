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

	if !strings.Contains(task.SystemPrompt, "## Post-Task Skill Extraction") {
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

	if !strings.Contains(task.SystemPrompt, "## Post-Task Skill Extraction") {
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

	if !strings.Contains(task.Prompt, "## Post-Task Skill Extraction") {
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

	if !strings.Contains(task.Prompt, "## Post-Task Skill Extraction") {
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

