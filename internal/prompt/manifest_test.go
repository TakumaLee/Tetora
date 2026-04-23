package prompt

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tetora/internal/classify"
	"tetora/internal/config"
	"tetora/internal/dispatch"
)

func TestManifest_Record_SkipsEmpty(t *testing.T) {
	m := NewManifest(&dispatch.Task{ID: "abc"}, "simple", "openai", "", "ruri")
	m.Record("empty", "system_prompt", 0)
	m.Record("also_empty", "user_prompt", -10)
	if len(m.Sections) != 0 {
		t.Fatalf("expected no sections for bytes <= 0, got %d", len(m.Sections))
	}
}

func TestManifest_Record_BasicFields(t *testing.T) {
	m := NewManifest(&dispatch.Task{ID: "abc", Name: "hello"}, "complex", "openai", "api", "ruri")
	m.Record("soul", "system_prompt", 1024,
		Path("/x/SOUL.md"),
		Truncated(true),
		HashOf("abc"),
	)
	m.Record("skills", "system_prompt", 512, Items([]string{"a", "b"}))
	if len(m.Sections) != 2 {
		t.Fatalf("expected 2 sections, got %d", len(m.Sections))
	}
	if m.Sections[0].Name != "soul" || m.Sections[0].Path != "/x/SOUL.md" || !m.Sections[0].Truncated {
		t.Errorf("soul section fields wrong: %+v", m.Sections[0])
	}
	if m.Sections[0].HashHex == "" || len(m.Sections[0].HashHex) != 64 {
		t.Errorf("expected 64-char sha256 hex, got %q", m.Sections[0].HashHex)
	}
	if got := m.Sections[1].Items; len(got) != 2 || got[0] != "a" {
		t.Errorf("items not recorded: %+v", got)
	}
}

func TestManifest_Finalize_ComputesTotals(t *testing.T) {
	task := &dispatch.Task{
		ID:           "abc",
		SystemPrompt: strings.Repeat("s", 100),
		Prompt:       strings.Repeat("u", 200),
		AllowedTools: []string{"Read", "Write"},
		AddDirs:      []string{"/a", "/b", "/c"},
	}
	m := NewManifest(task, "complex", "openai", "api", "ruri")
	m.Finalize(task)

	if m.Totals.SystemPromptBytes != 100 {
		t.Errorf("system bytes: got %d want 100", m.Totals.SystemPromptBytes)
	}
	if m.Totals.UserPromptBytes != 200 {
		t.Errorf("user bytes: got %d want 200", m.Totals.UserPromptBytes)
	}
	if m.Totals.AllowedToolsCount != 2 {
		t.Errorf("tools count: got %d want 2", m.Totals.AllowedToolsCount)
	}
	if m.Totals.AddDirsCount != 3 {
		t.Errorf("addDirs count: got %d want 3", m.Totals.AddDirsCount)
	}
}

func TestManifest_Save_WritesValidJSON(t *testing.T) {
	dir := t.TempDir()
	task := &dispatch.Task{ID: "12345678abcdef", Name: "board:xyz"}
	m := NewManifest(task, "standard", "openai", "api", "ruri")
	m.Record("soul", "system_prompt", 10)
	m.Finalize(task)

	filename, err := m.Save(dir)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if !strings.HasSuffix(filename, ".prompt-manifest.json") {
		t.Errorf("filename suffix wrong: %q", filename)
	}
	if !strings.HasPrefix(filename, "12345678_") {
		t.Errorf("filename should start with 8-char shortID: %q", filename)
	}

	fullPath := filepath.Join(dir, "outputs", filename)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	var parsed Manifest
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("roundtrip unmarshal: %v", err)
	}
	if parsed.SchemaVersion != ManifestSchemaVersion {
		t.Errorf("schema_version roundtrip wrong: got %d want %d", parsed.SchemaVersion, ManifestSchemaVersion)
	}
	if parsed.TaskID != task.ID {
		t.Errorf("task_id roundtrip wrong: got %q", parsed.TaskID)
	}
	if len(parsed.Sections) != 1 || parsed.Sections[0].Name != "soul" {
		t.Errorf("sections roundtrip wrong: %+v", parsed.Sections)
	}
}

func TestManifest_Save_NilReturnsEmpty(t *testing.T) {
	var m *Manifest
	filename, err := m.Save(t.TempDir())
	if err != nil {
		t.Errorf("nil manifest Save returned error: %v", err)
	}
	if filename != "" {
		t.Errorf("nil manifest Save returned filename: %q", filename)
	}
}

func TestManifest_Save_UnknownTaskIDFallback(t *testing.T) {
	dir := t.TempDir()
	task := &dispatch.Task{ID: ""}
	m := NewManifest(task, "simple", "openai", "", "")
	m.Record("soul", "system_prompt", 5)

	filename, err := m.Save(dir)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if !strings.HasPrefix(filename, "unknown_") {
		t.Errorf("empty task ID should fall back to 'unknown' prefix; got %q", filename)
	}
}

// TestBuildTieredPrompt_ReturnsManifest_ClaudeCode verifies that the claude-code
// early-return path still produces a manifest with preflight/skill_extraction
// recorded (and finalized).
func TestBuildTieredPrompt_ReturnsManifest_ClaudeCode(t *testing.T) {
	cfg := minimalCfg()
	task := &dispatch.Task{ID: "claude-cc-1", Prompt: "do it"}
	m := BuildTieredPrompt(cfg, task, "", classify.Standard, minimalDeps("claude-code"))
	if m == nil {
		t.Fatal("manifest must not be nil")
	}
	if m.Tier != "standard" {
		t.Errorf("tier: got %q want standard", m.Tier)
	}
	if m.ProviderType != "claude-code" {
		t.Errorf("provider_type: got %q want claude-code", m.ProviderType)
	}
	// skill_extraction should be recorded in user_prompt (claude-code path).
	found := false
	for _, s := range m.Sections {
		if s.Name == "skill_extraction" && s.Target == "user_prompt" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected skill_extraction section on user_prompt; got %+v", m.Sections)
	}
	if m.Totals.UserPromptBytes != len(task.Prompt) {
		t.Errorf("Finalize not run: totals user=%d, actual=%d", m.Totals.UserPromptBytes, len(task.Prompt))
	}
}

// TestBuildTieredPrompt_ReturnsManifest_APIProvider verifies the Standard/Complex path
// records multiple sections and finalizes totals correctly.
func TestBuildTieredPrompt_ReturnsManifest_APIProvider(t *testing.T) {
	cfg := minimalCfg()
	cfg.Citation.Enabled = true
	task := &dispatch.Task{ID: "abc-1", Prompt: "build a feature", ScopeBoundary: "implement_allowed"}
	m := BuildTieredPrompt(cfg, task, "", classify.Complex, minimalDeps("openai"))
	if m == nil {
		t.Fatal("manifest must not be nil")
	}
	have := map[string]bool{}
	for _, s := range m.Sections {
		have[s.Name] = true
	}
	if !have["citation"] {
		t.Errorf("expected citation section in complex API path; got sections: %v", sectionNames(m))
	}
	if !have["skill_extraction"] {
		t.Errorf("expected skill_extraction section in complex API path; got sections: %v", sectionNames(m))
	}
	if !have["scope_boundary"] {
		t.Errorf("expected scope_boundary section; got: %v", sectionNames(m))
	}
	if m.Totals.SystemPromptBytes != len(task.SystemPrompt) {
		t.Errorf("system totals mismatch: %d vs %d", m.Totals.SystemPromptBytes, len(task.SystemPrompt))
	}
}

// TestBuildTieredPrompt_SkillsWithMeta verifies that when BuildSkillsPromptWithMeta
// returns matched names, they are recorded in the manifest's skills section.
func TestBuildTieredPrompt_SkillsWithMeta(t *testing.T) {
	cfg := minimalCfg()
	task := &dispatch.Task{ID: "skills-1", Prompt: "please fix"}
	deps := minimalDeps("openai")
	deps.BuildSkillsPromptWithMeta = func(_ *config.Config, _ dispatch.Task, _ classify.Complexity) (string, []string) {
		return "\n\n## Active Skills\n- a\n- b\n", []string{"a", "b"}
	}
	m := BuildTieredPrompt(cfg, task, "", classify.Standard, deps)
	for _, s := range m.Sections {
		if s.Name == "skills" {
			if len(s.Items) != 2 || s.Items[0] != "a" || s.Items[1] != "b" {
				t.Errorf("skills items not recorded: %+v", s.Items)
			}
			return
		}
	}
	t.Errorf("no skills section found; got: %v", sectionNames(m))
}

func sectionNames(m *Manifest) []string {
	out := make([]string, 0, len(m.Sections))
	for _, s := range m.Sections {
		out = append(out, s.Name)
	}
	return out
}
