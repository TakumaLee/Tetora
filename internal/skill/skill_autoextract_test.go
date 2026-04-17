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

	skills := LoadFileSkills(cfg)
	var found bool
	for _, s := range skills {
		if s.Name == "learned-one" && s.Learned {
			found = true
		}
	}
	if !found {
		t.Error("created learned skill not found in LoadFileSkills with Learned=true")
	}
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
