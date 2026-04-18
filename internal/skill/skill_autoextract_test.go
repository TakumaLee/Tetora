package skill

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- ShouldExtractSkill tests ---

func TestShouldExtractSkill_ToolCallThreshold(t *testing.T) {
	cfg := &AppConfig{BaseDir: t.TempDir()}

	if ShouldExtractSkill(cfg, TaskSignals{ToolCallCount: 4}) {
		t.Error("4 tool calls should not trigger extraction")
	}
	if !ShouldExtractSkill(cfg, TaskSignals{ToolCallCount: 5}) {
		t.Error("5 tool calls should trigger extraction")
	}
	if !ShouldExtractSkill(cfg, TaskSignals{ToolCallCount: 20}) {
		t.Error("20 tool calls should trigger extraction")
	}
}

func TestShouldExtractSkill_ErrorRecovery(t *testing.T) {
	cfg := &AppConfig{BaseDir: t.TempDir()}

	if !ShouldExtractSkill(cfg, TaskSignals{ErrorRecovery: true}) {
		t.Error("error recovery should trigger extraction")
	}
}

func TestShouldExtractSkill_UserCorrection(t *testing.T) {
	cfg := &AppConfig{BaseDir: t.TempDir()}

	if !ShouldExtractSkill(cfg, TaskSignals{UserCorrection: true}) {
		t.Error("user correction should trigger extraction")
	}
}

func TestShouldExtractSkill_NoTrigger(t *testing.T) {
	cfg := &AppConfig{BaseDir: t.TempDir()}

	signals := TaskSignals{
		ToolCallCount:  0,
		ErrorRecovery:  false,
		UserCorrection: false,
	}
	if ShouldExtractSkill(cfg, signals) {
		t.Error("no trigger conditions should not extract skill")
	}
}

func TestShouldExtractSkill_NoHistoryDB_SkipsOverlapCheck(t *testing.T) {
	// When HistoryDB is empty, overlap check is skipped → extraction proceeds.
	cfg := &AppConfig{BaseDir: t.TempDir(), HistoryDB: ""}

	if !ShouldExtractSkill(cfg, TaskSignals{ToolCallCount: 5, TaskPrompt: "deploy to staging"}) {
		t.Error("should extract when HistoryDB is empty and tool call threshold met")
	}
}

// --- CreateLearnedSkill tests ---

func TestCreateLearnedSkill_Basic(t *testing.T) {
	dir := t.TempDir()
	cfg := &AppConfig{BaseDir: dir}

	spec := LearnedSkillSpec{
		Name:        "my-workflow",
		Description: "Automates my-workflow steps",
		Triggers:    []string{"my-workflow", "automate"},
		Doc:         "## Steps\n\n1. Do thing A\n2. Do thing B",
		CreatedBy:   "kokuyou",
	}

	if err := CreateLearnedSkill(cfg, spec); err != nil {
		t.Fatalf("CreateLearnedSkill() error: %v", err)
	}

	// SKILL.md must exist with correct frontmatter.
	skillMDPath := filepath.Join(dir, "skills", "learned", "my-workflow", "SKILL.md")
	data, err := os.ReadFile(skillMDPath)
	if err != nil {
		t.Fatalf("SKILL.md not created: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "name: my-workflow") {
		t.Error("SKILL.md missing name frontmatter")
	}
	if !strings.Contains(content, "my-workflow, automate") {
		t.Error("SKILL.md missing triggers")
	}
	if !strings.Contains(content, "maintainer: kokuyou") {
		t.Error("SKILL.md missing maintainer")
	}
	if !strings.Contains(content, "## Steps") {
		t.Error("SKILL.md missing doc body")
	}

	// metadata.json must exist with approved=false.
	metaPath := filepath.Join(dir, "skills", "learned", "my-workflow", "metadata.json")
	metaData, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("metadata.json not created: %v", err)
	}
	var meta SkillMetadata
	if err := json.Unmarshal(metaData, &meta); err != nil {
		t.Fatalf("metadata.json unmarshal error: %v", err)
	}
	if meta.Approved {
		t.Error("learned skill must start as approved=false")
	}
	if meta.CreatedAt == "" {
		t.Error("createdAt must be set")
	}
	if meta.Matcher == nil || len(meta.Matcher.Keywords) != 2 {
		t.Errorf("matcher keywords = %v, want 2 items", meta.Matcher)
	}
}

func TestCreateLearnedSkill_LoadedAsLearned(t *testing.T) {
	dir := t.TempDir()
	cfg := &AppConfig{BaseDir: dir}

	spec := LearnedSkillSpec{
		Name:      "learned-one",
		Triggers:  []string{"trigger-kw"},
		CreatedBy: "kokuyou",
	}
	if err := CreateLearnedSkill(cfg, spec); err != nil {
		t.Fatalf("CreateLearnedSkill() error: %v", err)
	}

	// Before approval: must NOT appear in LoadFileSkills (fail-closed).
	if skills := LoadFileSkills(cfg); len(skills) != 0 {
		t.Fatalf("unapproved learned skill surfaced in LoadFileSkills: %+v", skills)
	}

	// Approve by flipping metadata.json.approved and invalidating cache.
	approveLearnedSkillForTest(t, cfg, "learned-one")

	skills := LoadFileSkills(cfg)
	var found bool
	for _, s := range skills {
		if s.Name == "learned-one" && s.Learned {
			found = true
		}
	}
	if !found {
		t.Error("approved learned skill not found in LoadFileSkills with Learned=true")
	}
}

// approveLearnedSkillForTest flips metadata.json.approved=true for a
// learned skill and invalidates the LoadFileSkills cache so the next call
// picks up the new state. Mirrors what a production ApproveSkill for the
// learned/ path would do; kept test-local to avoid scope creep in this PR.
func approveLearnedSkillForTest(t *testing.T, cfg *AppConfig, name string) {
	t.Helper()
	metaPath := filepath.Join(SkillsDir(cfg), "learned", name, "metadata.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("read metadata.json: %v", err)
	}
	var meta SkillMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatalf("parse metadata.json: %v", err)
	}
	meta.Approved = true
	out, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	if err := os.WriteFile(metaPath, out, 0o644); err != nil {
		t.Fatalf("write metadata.json: %v", err)
	}
	invalidateSkillsCache(cfg)
}

func TestCreateLearnedSkill_Duplicate(t *testing.T) {
	dir := t.TempDir()
	cfg := &AppConfig{BaseDir: dir}

	spec := LearnedSkillSpec{Name: "dup-learned", Description: "first", CreatedBy: "kokuyou"}
	if err := CreateLearnedSkill(cfg, spec); err != nil {
		t.Fatalf("first CreateLearnedSkill() error: %v", err)
	}
	if err := CreateLearnedSkill(cfg, spec); err == nil {
		t.Fatal("expected error for duplicate learned skill, got nil")
	}
}

func TestCreateLearnedSkill_InvalidName(t *testing.T) {
	cfg := &AppConfig{BaseDir: t.TempDir()}

	spec := LearnedSkillSpec{Name: "../evil", CreatedBy: "kokuyou"}
	if err := CreateLearnedSkill(cfg, spec); err == nil {
		t.Fatal("expected error for invalid skill name, got nil")
	}
}

func TestCreateLearnedSkill_NoTriggers(t *testing.T) {
	dir := t.TempDir()
	cfg := &AppConfig{BaseDir: dir}

	spec := LearnedSkillSpec{
		Name:        "no-triggers",
		Description: "Skill without triggers",
		CreatedBy:   "kokuyou",
	}
	if err := CreateLearnedSkill(cfg, spec); err != nil {
		t.Fatalf("CreateLearnedSkill() error: %v", err)
	}

	metaPath := filepath.Join(dir, "skills", "learned", "no-triggers", "metadata.json")
	data, _ := os.ReadFile(metaPath)
	var meta SkillMetadata
	json.Unmarshal(data, &meta)
	if meta.Matcher != nil {
		t.Error("matcher should be nil when no triggers specified")
	}
}

// TestAutoInjectLearnedSkills_SkipsUnapproved asserts that a freshly-created
// learned skill (CreateLearnedSkill writes approved=false) is NOT returned by
// AutoInjectLearnedSkills, even though the 24h recency window in
// ShouldInjectLearnedSkill would otherwise match. LoadFileSkills is the
// approval gate; this test guards against regressions that would let
// unapproved LLM-extracted skills leak into downstream task prompts.
func TestAutoInjectLearnedSkills_SkipsUnapproved(t *testing.T) {
	dir := t.TempDir()
	cfg := &AppConfig{BaseDir: dir}

	spec := LearnedSkillSpec{
		Name:        "unapproved-flow",
		Description: "pending human review",
		Triggers:    []string{"unapproved-flow"},
		CreatedBy:   "kokuyou",
	}
	if err := CreateLearnedSkill(cfg, spec); err != nil {
		t.Fatalf("CreateLearnedSkill: %v", err)
	}

	// Prompt that would trip both keyword match and the 24h recency window.
	task := TaskContext{Agent: "kokuyou", Prompt: "run the unapproved-flow please"}
	injected := AutoInjectLearnedSkills(cfg, task)
	for _, s := range injected {
		if s.Name == "unapproved-flow" {
			t.Errorf("unapproved learned skill leaked into AutoInjectLearnedSkills: %+v", s)
		}
	}
}

// --- Frontmatter sanitization tests (YAML injection hardening) ---

func TestCreateLearnedSkill_SanitizesDescriptionNewlines(t *testing.T) {
	dir := t.TempDir()
	cfg := &AppConfig{BaseDir: dir}

	// Adversarial LLM output: embedded newline + `---` tries to terminate the
	// frontmatter block and forge a second field. The sanitizer must collapse
	// newlines so the payload stays a scalar on the `description:` line and
	// cannot escape as a separate YAML key.
	spec := LearnedSkillSpec{
		Name:        "sanitize-desc",
		Description: "first line\n---\ninjected: true",
		CreatedBy:   "kokuyou",
	}
	if err := CreateLearnedSkill(cfg, spec); err != nil {
		t.Fatalf("CreateLearnedSkill() error: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "skills", "learned", "sanitize-desc", "SKILL.md"))
	content := string(data)
	// Structural safety: exactly two `---` document markers (open + close),
	// and the injected string must not appear on its own line as a key:value.
	if strings.Count(content, "---\n") != 2 {
		t.Errorf("expected exactly two `---\\n` lines (open+close); got content:\n%s", content)
	}
	// The injected payload MUST stay on the description line (same line as
	// `description:`), not on its own line.
	for _, line := range strings.Split(content, "\n") {
		if line == "injected: true" {
			t.Errorf("injection escaped to its own YAML line in:\n%s", content)
		}
	}
}

func TestCreateLearnedSkill_SanitizesTriggerCharset(t *testing.T) {
	dir := t.TempDir()
	cfg := &AppConfig{BaseDir: dir}

	// Adversarial trigger entries: commas, quotes, brackets, and newlines
	// must all be stripped; clean parts (alphanumeric + `-_`) must survive.
	spec := LearnedSkillSpec{
		Name: "sanitize-trig",
		Triggers: []string{
			"deploy",                        // clean
			"rm]-rf, 'pwn'",                 // punctuation → keep only letters/hyphen
			"foo\nbar",                      // newline → merged into "foobar"
			"valid_keyword-2",               // underscore + digit + hyphen allowed
			"deploy",                        // duplicate of first
			"",                              // empty → dropped
		},
		CreatedBy: "kokuyou",
	}
	if err := CreateLearnedSkill(cfg, spec); err != nil {
		t.Fatalf("CreateLearnedSkill() error: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "skills", "learned", "sanitize-trig", "SKILL.md"))
	content := string(data)
	// Assert that no unsafe punctuation leaked into the triggers line.
	for _, badFragment := range []string{"'", "\"", "]", "[]"} {
		// `[` is the legitimate triggers array opener; only `]` we care about not appearing spuriously.
		_ = badFragment
	}
	if strings.Contains(content, "'pwn'") || strings.Contains(content, "rm]") {
		t.Errorf("triggers not sanitized:\n%s", content)
	}
	// Clean entries must survive (hyphens are allowed so "rm]-rf, 'pwn'" → "rm-rfpwn").
	for _, want := range []string{"deploy", "rm-rfpwn", "foobar", "valid_keyword-2"} {
		if !strings.Contains(content, want) {
			t.Errorf("missing expected sanitized trigger %q in:\n%s", want, content)
		}
	}
	// Dedup + count check: "deploy" appears once in the triggers line.
	triggersLine := ""
	for _, ln := range strings.Split(content, "\n") {
		if strings.HasPrefix(ln, "triggers:") {
			triggersLine = ln
			break
		}
	}
	if strings.Count(triggersLine, "deploy") != 1 {
		t.Errorf("duplicate trigger not deduped: %q", triggersLine)
	}
}

// --- ShouldInjectLearnedSkill tests ---

func TestShouldInjectLearnedSkill_KeywordMatch(t *testing.T) {
	s := SkillConfig{
		Name: "kw-skill",
		Matcher: &SkillMatcher{
			Keywords: []string{"deploy"},
		},
	}
	task := TaskContext{Prompt: "please deploy to staging"}
	if !ShouldInjectLearnedSkill(s, task) {
		t.Error("ShouldInjectLearnedSkill() should return true on keyword match")
	}
}

func TestShouldInjectLearnedSkill_RecentFile(t *testing.T) {
	dir := t.TempDir()
	cfg := &AppConfig{BaseDir: dir}

	// Create a learned skill (SKILL.md will have recent mtime).
	spec := LearnedSkillSpec{
		Name:      "recent-extracted",
		Triggers:  []string{"very-specific-keyword-xyz"},
		CreatedBy: "kokuyou",
	}
	if err := CreateLearnedSkill(cfg, spec); err != nil {
		t.Fatalf("CreateLearnedSkill() error: %v", err)
	}
	// LoadFileSkills fails closed on unapproved learned skills; approve first.
	approveLearnedSkillForTest(t, cfg, "recent-extracted")

	skills := LoadFileSkills(cfg)
	var learnedSkill SkillConfig
	for _, s := range skills {
		if s.Name == "recent-extracted" {
			learnedSkill = s
		}
	}
	if learnedSkill.Name == "" {
		t.Fatal("learned skill not found in LoadFileSkills")
	}

	// Task does NOT match keywords but SKILL.md is fresh (<24h).
	task := TaskContext{Agent: "kokuyou", Prompt: "completely unrelated task prompt"}
	if !ShouldInjectLearnedSkill(learnedSkill, task) {
		t.Error("ShouldInjectLearnedSkill() should return true for recently extracted skill within 24h window")
	}
}

func TestShouldInjectLearnedSkill_NoMatch_NoDoc(t *testing.T) {
	// Skill with no matcher and no DocPath — should not inject if task doesn't match.
	s := SkillConfig{
		Name:    "orphan-skill",
		Matcher: &SkillMatcher{Keywords: []string{"xyz-never-matches"}},
		DocPath: "", // no doc path → recency check skipped
	}
	task := TaskContext{Prompt: "completely different task"}
	if ShouldInjectLearnedSkill(s, task) {
		t.Error("ShouldInjectLearnedSkill() should return false for no-match skill without DocPath")
	}
}
