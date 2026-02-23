package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitSkillUsageTable(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	if err := initSkillUsageTable(dbPath); err != nil {
		t.Fatalf("initSkillUsageTable() error: %v", err)
	}

	// Verify table exists by querying it.
	rows, err := queryDB(dbPath, "SELECT COUNT(*) as cnt FROM skill_usage")
	if err != nil {
		t.Fatalf("query skill_usage failed: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("expected at least one row from COUNT(*)")
	}
}

func TestRecordSkillEvent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	if err := initSkillUsageTable(dbPath); err != nil {
		t.Fatalf("initSkillUsageTable() error: %v", err)
	}

	recordSkillEvent(dbPath, "my-skill", "created", "build a greeting tool", "琉璃")
	recordSkillEvent(dbPath, "my-skill", "used", "", "黒曜")

	rows, err := queryDB(dbPath, "SELECT * FROM skill_usage ORDER BY id")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}

	if rows[0]["skill_name"] != "my-skill" {
		t.Errorf("skill_name = %v, want 'my-skill'", rows[0]["skill_name"])
	}
	if rows[0]["event_type"] != "created" {
		t.Errorf("event_type = %v, want 'created'", rows[0]["event_type"])
	}
	if rows[1]["event_type"] != "used" {
		t.Errorf("event_type = %v, want 'used'", rows[1]["event_type"])
	}
}

func TestSuggestSkillsForPrompt(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	initSkillUsageTable(dbPath)

	// Record creation events with prompts.
	recordSkillEvent(dbPath, "deploy-app", "created", "deploy the application to production server", "黒曜")
	recordSkillEvent(dbPath, "check-logs", "created", "check and analyze server error logs", "翡翠")
	recordSkillEvent(dbPath, "greet-user", "created", "greet the user with a friendly hello message", "琥珀")

	// Query with related prompt.
	suggestions := suggestSkillsForPrompt(dbPath, "deploy the application to staging server", 5)
	if len(suggestions) == 0 {
		t.Fatal("expected suggestions for deploy-related prompt")
	}
	if suggestions[0] != "deploy-app" {
		t.Errorf("top suggestion = %q, want 'deploy-app'", suggestions[0])
	}

	// Query with unrelated prompt should return nothing or fewer results.
	unrelated := suggestSkillsForPrompt(dbPath, "play some music and dance", 5)
	// This should have no meaningful overlap.
	if len(unrelated) > 0 {
		t.Logf("unrelated suggestions: %v (may be noise)", unrelated)
	}
}

func TestAutoInjectLearnedSkills(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	cfg := &Config{
		baseDir:   dir,
		HistoryDB: dbPath,
		SkillStore: SkillStoreConfig{
			AutoApprove: true,
			MaxSkills:   50,
		},
	}

	initSkillUsageTable(dbPath)

	// Create an approved skill with keywords.
	meta := SkillMetadata{
		Name:     "deploy-tool",
		Command:  "./run.sh",
		Approved: true,
		Matcher:  &SkillMatcher{Keywords: []string{"deploy"}},
	}
	createSkill(cfg, meta, "echo deploying")

	// Record creation event.
	recordSkillEvent(dbPath, "deploy-tool", "created", "deploy application to production", "黒曜")

	// Task with matching keyword.
	task := Task{
		Prompt: "please deploy the app",
		Role:   "黒曜",
	}

	skills := autoInjectLearnedSkills(cfg, task)
	if len(skills) == 0 {
		t.Fatal("expected at least one auto-injected skill")
	}

	found := false
	for _, s := range skills {
		if s.Name == "deploy-tool" {
			found = true
		}
	}
	if !found {
		t.Error("deploy-tool not found in auto-injected skills")
	}
}

func TestSkillTokenize(t *testing.T) {
	words := skillTokenize("Hello, World! This is a test.")
	// "is" and "a" are < 3 chars, filtered out
	expected := []string{"hello", "world", "this", "test"}
	if len(words) != len(expected) {
		t.Fatalf("tokenize returned %d words, want %d: %v", len(words), len(expected), words)
	}
	for i, w := range words {
		if w != expected[i] {
			t.Errorf("word[%d] = %q, want %q", i, w, expected[i])
		}
	}
}

func TestWordOverlap(t *testing.T) {
	a := []string{"deploy", "the", "application"}
	b := []string{"deploy", "application", "production"}

	overlap := wordOverlap(a, b)
	if overlap != 2 {
		t.Errorf("wordOverlap = %d, want 2", overlap)
	}
}

// Ensure sqlite3 is available for tests.
func TestSQLiteAvailable(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "check.db")
	_, err := queryDB(dbPath, "SELECT 1")
	if err != nil {
		// Try to detect if sqlite3 is just not installed.
		if _, statErr := os.Stat("/usr/bin/sqlite3"); statErr != nil {
			t.Skip("sqlite3 not available, skipping DB tests")
		}
		t.Fatalf("queryDB failed: %v", err)
	}
}
