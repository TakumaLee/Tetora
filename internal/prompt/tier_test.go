package prompt

import (
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
