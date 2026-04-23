package skill

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tetora/internal/classify"
)

func TestExtractChannelFromSource(t *testing.T) {
	tests := []struct {
		source   string
		expected string
	}{
		{"telegram", "telegram"},
		{"slack:C123", "slack"},
		{"discord:456", "discord"},
		{"chat:telegram:789", "telegram"},
		{"chat:slack:C123", "slack"},
		{"cron", "cron"},
		{"api", "api"},
		{"", ""},
	}

	for _, tt := range tests {
		got := ExtractChannelFromSource(tt.source)
		if got != tt.expected {
			t.Errorf("ExtractChannelFromSource(%q) = %q, want %q", tt.source, got, tt.expected)
		}
	}
}

func TestShouldInjectSkill(t *testing.T) {
	// No matcher — always inject.
	s := SkillConfig{Name: "test"}
	task := TaskContext{Agent: "琉璃", Prompt: "hello", Source: "telegram"}
	if !ShouldInjectSkill(s, task) {
		t.Error("skill without matcher should always inject")
	}

	// Role match.
	s.Matcher = &SkillMatcher{Agents: []string{"琉璃"}}
	if !ShouldInjectSkill(s, task) {
		t.Error("skill should match role 琉璃")
	}

	s.Matcher.Agents = []string{"翡翠"}
	if ShouldInjectSkill(s, task) {
		t.Error("skill should not match role 翡翠")
	}

	// Keyword match.
	s.Matcher = &SkillMatcher{Keywords: []string{"deploy", "build"}}
	task.Prompt = "Please deploy the app"
	if !ShouldInjectSkill(s, task) {
		t.Error("skill should match keyword 'deploy'")
	}

	task.Prompt = "Say hello"
	if ShouldInjectSkill(s, task) {
		t.Error("skill should not match without keyword")
	}

	// Channel match.
	s.Matcher = &SkillMatcher{Channels: []string{"slack", "discord"}}
	task.Source = "slack:C123"
	if !ShouldInjectSkill(s, task) {
		t.Error("skill should match channel slack")
	}

	task.Source = "telegram"
	if ShouldInjectSkill(s, task) {
		t.Error("skill should not match channel telegram")
	}

	// Multiple conditions (any match).
	s.Matcher = &SkillMatcher{
		Agents:   []string{"琥珀"},
		Keywords: []string{"image"},
		Channels: []string{"discord"},
	}
	task.Agent = "琥珀"
	task.Prompt = "hello"
	task.Source = "telegram"
	if !ShouldInjectSkill(s, task) {
		t.Error("skill should match role 琥珀")
	}

	task.Agent = "琉璃"
	task.Prompt = "generate an image"
	if !ShouldInjectSkill(s, task) {
		t.Error("skill should match keyword 'image'")
	}

	task.Prompt = "hello"
	task.Source = "discord:456"
	if !ShouldInjectSkill(s, task) {
		t.Error("skill should match channel discord")
	}
}

func TestSelectSkills(t *testing.T) {
	cfg := &AppConfig{
		Skills: []SkillConfig{
			{Name: "deploy", Matcher: &SkillMatcher{Keywords: []string{"deploy"}}},
			{Name: "creative", Matcher: &SkillMatcher{Agents: []string{"琥珀"}}},
			{Name: "slack-only", Matcher: &SkillMatcher{Channels: []string{"slack"}}},
			{Name: "always", Matcher: nil}, // no matcher = always inject
		},
	}

	task := TaskContext{Agent: "琉璃", Prompt: "deploy the app", Source: "telegram"}
	skills := SelectSkills(cfg, task)

	if len(skills) != 2 {
		t.Errorf("expected 2 skills, got %d", len(skills))
	}

	// Should have "deploy" (keyword match) and "always" (no matcher).
	hasDeploy := false
	hasAlways := false
	for _, s := range skills {
		if s.Name == "deploy" {
			hasDeploy = true
		}
		if s.Name == "always" {
			hasAlways = true
		}
	}
	if !hasDeploy || !hasAlways {
		t.Error("SelectSkills missing expected skills")
	}
}

func TestBuildSkillsPrompt(t *testing.T) {
	cfg := &AppConfig{
		Skills: []SkillConfig{
			{Name: "test", Description: "Test skill", Example: "test arg1 arg2"},
		},
	}

	task := TaskContext{Prompt: "hello"}
	prompt := BuildSkillsPrompt(cfg, task, classify.Standard)

	if prompt == "" {
		t.Fatal("BuildSkillsPrompt returned empty string")
	}

	if !skillStringContains(prompt, "Active Skills") {
		t.Error("prompt missing header")
	}
	if !skillStringContains(prompt, "test") {
		t.Error("prompt missing skill name")
	}
	if !skillStringContains(prompt, "Test skill") {
		t.Error("prompt missing description")
	}
	if !skillStringContains(prompt, "test arg1 arg2") {
		t.Error("prompt missing example")
	}
}

func TestBuildSkillsPromptTier2DocInjection(t *testing.T) {
	// Create a temp skill dir with SKILL.md.
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "skills", "test-skill")
	os.MkdirAll(skillDir, 0o755)

	// Write metadata.json.
	meta := SkillMetadata{
		Name:        "test-skill",
		Description: "A test skill",
		Command:     "./run.sh",
		Approved:    true,
	}
	metaData, _ := json.Marshal(meta)
	os.WriteFile(filepath.Join(skillDir, "metadata.json"), metaData, 0o644)

	// Write SKILL.md (under 4KB).
	docContent := "# Test Skill\n\nUsage: run with --flag\n\n## Examples\n\n```bash\n./run.sh --flag value\n```"
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(docContent), 0o644)

	cfg := &AppConfig{
		WorkspaceDir: dir,
	}

	task := TaskContext{Prompt: "test-skill related query"}

	// Standard complexity: should inject SKILL.md.
	prompt := BuildSkillsPrompt(cfg, task, classify.Standard)
	if !skillStringContains(prompt, "<skill-doc name=\"test-skill\">") {
		t.Error("Standard complexity should inject skill-doc tag")
	}
	if !skillStringContains(prompt, "Usage: run with --flag") {
		t.Error("Standard complexity should inject SKILL.md content")
	}

	// Simple complexity: should NOT inject SKILL.md.
	promptSimple := BuildSkillsPrompt(cfg, task, classify.Simple)
	if skillStringContains(promptSimple, "<skill-doc") {
		t.Error("Simple complexity should not inject skill-doc")
	}
	// But should still have the Tier 1 summary.
	if !skillStringContains(promptSimple, "test-skill") {
		t.Error("Simple complexity should still have skill name in Tier 1")
	}
}

func TestBuildSkillsPromptTier2LargeDoc(t *testing.T) {
	// Create a temp skill dir with large SKILL.md (>4KB).
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "skills", "big-skill")
	os.MkdirAll(skillDir, 0o755)

	meta := SkillMetadata{
		Name:        "big-skill",
		Description: "A big skill",
		Command:     "./run.sh",
		Approved:    true,
	}
	metaData, _ := json.Marshal(meta)
	os.WriteFile(filepath.Join(skillDir, "metadata.json"), metaData, 0o644)

	// Write large SKILL.md (>4096 bytes).
	largeDoc := strings.Repeat("x", 5000)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(largeDoc), 0o644)

	cfg := &AppConfig{
		WorkspaceDir: dir,
	}

	task := TaskContext{Prompt: "big-skill query"}
	prompt := BuildSkillsPrompt(cfg, task, classify.Standard)

	// Should not inline the doc, but provide path hint.
	if skillStringContains(prompt, "<skill-doc") {
		t.Error("Large doc (>4KB) should not be inlined")
	}
	if !skillStringContains(prompt, "read with file tool") {
		t.Error("Large doc should have path hint for agent")
	}
}

func TestBuildSkillsPromptTier2BudgetExceeded(t *testing.T) {
	// Create two skills, each with docs that exceed the budget together.
	dir := t.TempDir()

	for i, name := range []string{"skill-a", "skill-b"} {
		skillDir := filepath.Join(dir, "skills", name)
		os.MkdirAll(skillDir, 0o755)
		meta := SkillMetadata{
			Name:        name,
			Description: fmt.Sprintf("Skill %d", i),
			Command:     "./run.sh",
			Approved:    true,
		}
		metaData, _ := json.Marshal(meta)
		os.WriteFile(filepath.Join(skillDir, "metadata.json"), metaData, 0o644)

		// Each doc is 3000 bytes — both together exceed default 4000 budget.
		doc := strings.Repeat("a", 3000)
		os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(doc), 0o644)
	}

	cfg := &AppConfig{
		WorkspaceDir: dir,
	}

	task := TaskContext{Prompt: "skill query"}
	prompt := BuildSkillsPrompt(cfg, task, classify.Complex)

	// First skill should be inlined, second should get budget exceeded hint.
	if !skillStringContains(prompt, "<skill-doc name=\"skill-a\">") {
		t.Error("First skill should be inlined within budget")
	}
	if skillStringContains(prompt, "<skill-doc name=\"skill-b\">") {
		t.Error("Second skill should not be inlined (budget exceeded)")
	}
	if !skillStringContains(prompt, "budget exceeded") {
		t.Error("Second skill should have budget exceeded hint")
	}
}

func skillStringContains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && len(s) >= len(substr) && skillFindSubstr(s, substr)
}

func TestCollectSkillAllowedTools(t *testing.T) {
	cfg := &AppConfig{
		Skills: []SkillConfig{
			{Name: "s1", AllowedTools: []string{"Bash", "Read"}},
			{Name: "s2", AllowedTools: []string{"Read", "Grep"}},
			{Name: "s3"}, // no allowed tools
		},
	}
	task := TaskContext{Agent: "test", Prompt: "hello"}
	got := CollectSkillAllowedTools(cfg, task)
	// Should be deduped: Bash, Read, Grep
	if len(got) != 3 {
		t.Fatalf("CollectSkillAllowedTools() len = %d, want 3", len(got))
	}
	want := map[string]bool{"Bash": true, "Read": true, "Grep": true}
	for _, tool := range got {
		if !want[tool] {
			t.Errorf("unexpected tool %q", tool)
		}
	}
}

func TestCollectSkillAllowedTools_Empty(t *testing.T) {
	cfg := &AppConfig{
		Skills: []SkillConfig{
			{Name: "s1"},
			{Name: "s2"},
		},
	}
	task := TaskContext{Agent: "test", Prompt: "hello"}
	got := CollectSkillAllowedTools(cfg, task)
	if len(got) != 0 {
		t.Errorf("CollectSkillAllowedTools() = %v, want empty", got)
	}
}

func skillFindSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// --- A1: tier-aware skill injection (SkillsOnDemand) ---

// writeSkillWithDoc creates a skill dir with metadata.json + SKILL.md under cfg.WorkspaceDir.
func writeSkillWithDoc(t *testing.T, dir, name, desc, doc string, mandatory bool) {
	t.Helper()
	skillDir := filepath.Join(dir, "skills", name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	meta := SkillMetadata{
		Name:        name,
		Description: desc,
		Command:     "./run.sh",
		Approved:    true,
		Mandatory:   mandatory,
	}
	b, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(skillDir, "metadata.json"), b, 0o644); err != nil {
		t.Fatalf("write meta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(doc), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
}

func TestBuildSkillsPromptWithMeta_Simple_OnDemandOff(t *testing.T) {
	dir := t.TempDir()
	writeSkillWithDoc(t, dir, "legacy-skill", "Legacy skill", "# legacy body", false)
	InvalidateSkillsCache(&AppConfig{WorkspaceDir: dir})

	cfg := &AppConfig{
		WorkspaceDir:          dir,
		SkillsOnDemandEnabled: false,
	}
	task := TaskContext{Prompt: "legacy-skill usage"}

	prompt, matched := BuildSkillsPromptWithMeta(cfg, task, classify.Simple)
	if !skillStringContains(prompt, "legacy-skill") {
		t.Error("OnDemand off should keep Tier 1 summary on Simple tier")
	}
	if skillStringContains(prompt, "<skill-doc") {
		t.Error("Simple tier should never inline Tier 2 docs")
	}
	if len(matched) == 0 {
		t.Error("matched names should be non-empty when skill is injected")
	}
}

func TestBuildSkillsPromptWithMeta_Simple_OnDemandOn_NoMandatory_ReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	writeSkillWithDoc(t, dir, "some-skill", "Some skill", "# body", false)
	InvalidateSkillsCache(&AppConfig{WorkspaceDir: dir})

	cfg := &AppConfig{
		WorkspaceDir:          dir,
		SkillsOnDemandEnabled: true,
	}
	task := TaskContext{Prompt: "some-skill relevant"}

	prompt, matched := BuildSkillsPromptWithMeta(cfg, task, classify.Simple)
	if prompt != "" {
		t.Errorf("Simple+OnDemand with no mandatory skills should return empty; got %q", prompt)
	}
	if len(matched) != 0 {
		t.Errorf("matched names should be empty; got %v", matched)
	}
}

func TestBuildSkillsPromptWithMeta_Simple_OnDemandOn_Mandatory_AlwaysInjected(t *testing.T) {
	dir := t.TempDir()
	writeSkillWithDoc(t, dir, "guard-skill", "Identity guard", "# guard body\n\nfollow the rules.", true)
	writeSkillWithDoc(t, dir, "regular-skill", "Regular skill", "# regular body", false)
	InvalidateSkillsCache(&AppConfig{WorkspaceDir: dir})

	cfg := &AppConfig{
		WorkspaceDir:          dir,
		SkillsOnDemandEnabled: true,
	}
	// Prompt has no keyword match for either skill — mandatory must bypass matcher.
	task := TaskContext{Prompt: "unrelated chatter"}

	prompt, matched := BuildSkillsPromptWithMeta(cfg, task, classify.Simple)
	if !skillStringContains(prompt, "guard-skill") {
		t.Error("mandatory skill must be injected on Simple tier")
	}
	if skillStringContains(prompt, "regular-skill") {
		t.Error("non-mandatory skill must be dropped on Simple+OnDemand")
	}
	if !skillStringContains(prompt, "## Active Skills (mandatory)") {
		t.Error("Simple+OnDemand output should use the mandatory-only header")
	}
	// Mandatory skill doc is inlined (small + identity guard).
	if !skillStringContains(prompt, "follow the rules.") {
		t.Error("mandatory skill doc should be inlined when under budget")
	}
	if len(matched) != 1 || matched[0] != "guard-skill" {
		t.Errorf("matched names should be [guard-skill]; got %v", matched)
	}
}

func TestBuildSkillsPromptWithMeta_Standard_OnDemand_SkipsTier2(t *testing.T) {
	dir := t.TempDir()
	writeSkillWithDoc(t, dir, "doc-skill", "Doc skill", "# body\n\nprocedure details here.", false)
	InvalidateSkillsCache(&AppConfig{WorkspaceDir: dir})

	cfg := &AppConfig{
		WorkspaceDir:          dir,
		SkillsOnDemandEnabled: true,
	}
	task := TaskContext{Prompt: "doc-skill related"}

	prompt, _ := BuildSkillsPromptWithMeta(cfg, task, classify.Standard)
	if !skillStringContains(prompt, "doc-skill") {
		t.Error("Standard tier should still list matched skills")
	}
	if skillStringContains(prompt, "<skill-doc") {
		t.Error("Standard + OnDemand should NOT inline Tier 2 skill-doc")
	}
	if !skillStringContains(prompt, "skill_load") {
		t.Error("prompt should mention skill_load tool for on-demand loading")
	}
}

func TestBuildSkillsPromptWithMeta_Complex_OnDemand_KeepsTier2(t *testing.T) {
	dir := t.TempDir()
	writeSkillWithDoc(t, dir, "doc-skill", "Doc skill", "# body\n\nfull procedure.", false)
	InvalidateSkillsCache(&AppConfig{WorkspaceDir: dir})

	cfg := &AppConfig{
		WorkspaceDir:          dir,
		SkillsOnDemandEnabled: true,
	}
	task := TaskContext{Prompt: "doc-skill related"}

	prompt, _ := BuildSkillsPromptWithMeta(cfg, task, classify.Complex)
	if !skillStringContains(prompt, "<skill-doc name=\"doc-skill\">") {
		t.Error("Complex tier should still inline Tier 2 docs even with OnDemand on")
	}
	if !skillStringContains(prompt, "full procedure.") {
		t.Error("Complex tier should inline the SKILL.md body")
	}
}

func TestShouldInjectSkill_MandatoryBypassesMatcher(t *testing.T) {
	// Matcher says "only agent=hisui" — but Mandatory overrides.
	s := SkillConfig{
		Name:      "guard",
		Mandatory: true,
		Matcher:   &SkillMatcher{Agents: []string{"hisui"}},
	}
	task := TaskContext{Agent: "琉璃", Prompt: "unrelated"}
	if !ShouldInjectSkill(s, task) {
		t.Error("Mandatory=true must bypass matcher and inject unconditionally")
	}
}

func TestLoadSkillFromFrontmatter_ParsesMandatory(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "skill-a")
	os.MkdirAll(skillDir, 0o755)
	frontmatter := "---\nname: skill-a\ndescription: test\nmandatory: true\n---\n\nbody\n"
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(frontmatter), 0o644)

	sc := loadSkillFromFrontmatter(skillDir)
	if sc == nil {
		t.Fatal("loadSkillFromFrontmatter returned nil")
	}
	if !sc.Mandatory {
		t.Error("Mandatory should be true when frontmatter says mandatory: true")
	}
}

func TestParseMandatoryFromFrontmatter(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{"true", "---\nname: x\nmandatory: true\n---\nbody\n", true},
		{"false", "---\nname: x\nmandatory: false\n---\nbody\n", false},
		{"missing", "---\nname: x\n---\nbody\n", false},
		{"no-frontmatter", "just body\n", false},
		{"quoted-true", "---\nname: x\nmandatory: \"true\"\n---\nbody\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name+".md")
			os.WriteFile(path, []byte(tc.content), 0o644)
			got := parseMandatoryFromFrontmatter(path)
			if got != tc.want {
				t.Errorf("parseMandatoryFromFrontmatter() = %v, want %v", got, tc.want)
			}
		})
	}
}
