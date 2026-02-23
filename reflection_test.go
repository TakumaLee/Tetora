package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// tempReflectionDB creates a temp DB with the reflections table initialized.
func tempReflectionDB(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not found, skipping")
	}
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_reflection.db")
	if err := initReflectionDB(dbPath); err != nil {
		t.Fatalf("initReflectionDB: %v", err)
	}
	return dbPath
}

// --- shouldReflect tests ---

func TestShouldReflect_Enabled(t *testing.T) {
	cfg := &Config{
		Reflection: ReflectionConfig{Enabled: true},
	}
	task := Task{Role: "翡翠"}
	result := TaskResult{Status: "success", CostUSD: 0.10}

	if !shouldReflect(cfg, task, result) {
		t.Error("shouldReflect should return true when enabled with successful task")
	}
}

func TestShouldReflect_Disabled(t *testing.T) {
	cfg := &Config{
		Reflection: ReflectionConfig{Enabled: false},
	}
	task := Task{Role: "翡翠"}
	result := TaskResult{Status: "success"}

	if shouldReflect(cfg, task, result) {
		t.Error("shouldReflect should return false when disabled")
	}
}

func TestShouldReflect_NilConfig(t *testing.T) {
	task := Task{Role: "翡翠"}
	result := TaskResult{Status: "success"}

	if shouldReflect(nil, task, result) {
		t.Error("shouldReflect should return false with nil config")
	}
}

func TestShouldReflect_MinCost(t *testing.T) {
	cfg := &Config{
		Reflection: ReflectionConfig{Enabled: true, MinCost: 0.50},
	}
	task := Task{Role: "翡翠"}

	// Below threshold.
	resultLow := TaskResult{Status: "success", CostUSD: 0.10}
	if shouldReflect(cfg, task, resultLow) {
		t.Error("shouldReflect should return false when cost below MinCost")
	}

	// At threshold.
	resultAt := TaskResult{Status: "success", CostUSD: 0.50}
	if !shouldReflect(cfg, task, resultAt) {
		t.Error("shouldReflect should return true when cost equals MinCost")
	}

	// Above threshold.
	resultHigh := TaskResult{Status: "success", CostUSD: 1.00}
	if !shouldReflect(cfg, task, resultHigh) {
		t.Error("shouldReflect should return true when cost above MinCost")
	}
}

func TestShouldReflect_TriggerOnFail(t *testing.T) {
	// Without TriggerOnFail: skip errors.
	cfg := &Config{
		Reflection: ReflectionConfig{Enabled: true, TriggerOnFail: false},
	}
	task := Task{Role: "黒曜"}

	resultErr := TaskResult{Status: "error"}
	if shouldReflect(cfg, task, resultErr) {
		t.Error("shouldReflect should return false for error status when TriggerOnFail is false")
	}

	resultTimeout := TaskResult{Status: "timeout"}
	if shouldReflect(cfg, task, resultTimeout) {
		t.Error("shouldReflect should return false for timeout status when TriggerOnFail is false")
	}

	// With TriggerOnFail: include errors.
	cfg.Reflection.TriggerOnFail = true
	if !shouldReflect(cfg, task, resultErr) {
		t.Error("shouldReflect should return true for error status when TriggerOnFail is true")
	}
	if !shouldReflect(cfg, task, resultTimeout) {
		t.Error("shouldReflect should return true for timeout status when TriggerOnFail is true")
	}
}

func TestShouldReflect_NoRole(t *testing.T) {
	cfg := &Config{
		Reflection: ReflectionConfig{Enabled: true},
	}
	task := Task{Role: ""}
	result := TaskResult{Status: "success"}

	if shouldReflect(cfg, task, result) {
		t.Error("shouldReflect should return false when role is empty")
	}
}

// --- parseReflectionOutput tests ---

func TestParseReflectionOutput_ValidJSON(t *testing.T) {
	output := `{"score":4,"feedback":"Good analysis","improvement":"Add more examples"}`

	ref, err := parseReflectionOutput(output)
	if err != nil {
		t.Fatalf("parseReflectionOutput: %v", err)
	}
	if ref.Score != 4 {
		t.Errorf("score = %d, want 4", ref.Score)
	}
	if ref.Feedback != "Good analysis" {
		t.Errorf("feedback = %q, want %q", ref.Feedback, "Good analysis")
	}
	if ref.Improvement != "Add more examples" {
		t.Errorf("improvement = %q, want %q", ref.Improvement, "Add more examples")
	}
}

func TestParseReflectionOutput_WrappedJSON(t *testing.T) {
	output := "```json\n{\"score\":5,\"feedback\":\"Excellent work\",\"improvement\":\"None needed\"}\n```"

	ref, err := parseReflectionOutput(output)
	if err != nil {
		t.Fatalf("parseReflectionOutput: %v", err)
	}
	if ref.Score != 5 {
		t.Errorf("score = %d, want 5", ref.Score)
	}
	if ref.Feedback != "Excellent work" {
		t.Errorf("feedback = %q, want %q", ref.Feedback, "Excellent work")
	}
}

func TestParseReflectionOutput_WithSurroundingText(t *testing.T) {
	output := "Here is my evaluation:\n{\"score\":3,\"feedback\":\"Adequate\",\"improvement\":\"Be more concise\"}\nThat's my assessment."

	ref, err := parseReflectionOutput(output)
	if err != nil {
		t.Fatalf("parseReflectionOutput: %v", err)
	}
	if ref.Score != 3 {
		t.Errorf("score = %d, want 3", ref.Score)
	}
}

func TestParseReflectionOutput_InvalidJSON(t *testing.T) {
	output := "This is not JSON at all"

	_, err := parseReflectionOutput(output)
	if err == nil {
		t.Error("expected error for non-JSON output")
	}
}

func TestParseReflectionOutput_MalformedJSON(t *testing.T) {
	output := `{"score":3, "feedback": incomplete`

	_, err := parseReflectionOutput(output)
	if err == nil {
		t.Error("expected error for malformed JSON")
	}
}

func TestParseReflectionOutput_InvalidScore(t *testing.T) {
	tests := []struct {
		name   string
		output string
	}{
		{"score 0", `{"score":0,"feedback":"bad","improvement":"fix"}`},
		{"score 6", `{"score":6,"feedback":"too high","improvement":"fix"}`},
		{"score -1", `{"score":-1,"feedback":"negative","improvement":"fix"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseReflectionOutput(tt.output)
			if err == nil {
				t.Errorf("expected error for invalid score in %q", tt.output)
			}
		})
	}
}

// --- DB tests ---

func TestInitReflectionDB(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not found, skipping")
	}
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	if err := initReflectionDB(dbPath); err != nil {
		t.Fatalf("initReflectionDB: %v", err)
	}
	// Verify file was created.
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("db file not created: %v", err)
	}
	// Idempotent: calling again should not error.
	if err := initReflectionDB(dbPath); err != nil {
		t.Fatalf("initReflectionDB second call: %v", err)
	}
}

func TestStoreAndQueryReflections(t *testing.T) {
	dbPath := tempReflectionDB(t)

	now := time.Now().UTC().Format(time.RFC3339)

	ref1 := &ReflectionResult{
		TaskID:      "task-001",
		Role:        "翡翠",
		Score:       4,
		Feedback:    "Good research quality",
		Improvement: "Include more sources",
		CostUSD:     0.02,
		CreatedAt:   now,
	}
	if err := storeReflection(dbPath, ref1); err != nil {
		t.Fatalf("storeReflection ref1: %v", err)
	}

	ref2 := &ReflectionResult{
		TaskID:      "task-002",
		Role:        "翡翠",
		Score:       2,
		Feedback:    "Incomplete analysis",
		Improvement: "Cover all edge cases",
		CostUSD:     0.03,
		CreatedAt:   now,
	}
	if err := storeReflection(dbPath, ref2); err != nil {
		t.Fatalf("storeReflection ref2: %v", err)
	}

	ref3 := &ReflectionResult{
		TaskID:      "task-003",
		Role:        "黒曜",
		Score:       5,
		Feedback:    "Excellent implementation",
		Improvement: "None needed",
		CostUSD:     0.01,
		CreatedAt:   now,
	}
	if err := storeReflection(dbPath, ref3); err != nil {
		t.Fatalf("storeReflection ref3: %v", err)
	}

	// Query all.
	all, err := queryReflections(dbPath, "", 10)
	if err != nil {
		t.Fatalf("queryReflections all: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 reflections, got %d", len(all))
	}

	// Query by role.
	jade, err := queryReflections(dbPath, "翡翠", 10)
	if err != nil {
		t.Fatalf("queryReflections jade: %v", err)
	}
	if len(jade) != 2 {
		t.Fatalf("expected 2 reflections for 翡翠, got %d", len(jade))
	}
	// Verify ordering (most recent first — both have same timestamp, so check both exist).
	for _, ref := range jade {
		if ref.Role != "翡翠" {
			t.Errorf("expected role 翡翠, got %q", ref.Role)
		}
	}

	// Query with limit.
	limited, err := queryReflections(dbPath, "", 1)
	if err != nil {
		t.Fatalf("queryReflections limited: %v", err)
	}
	if len(limited) != 1 {
		t.Fatalf("expected 1 reflection with limit=1, got %d", len(limited))
	}

	// Verify field values round-trip.
	obsidian, err := queryReflections(dbPath, "黒曜", 10)
	if err != nil {
		t.Fatalf("queryReflections obsidian: %v", err)
	}
	if len(obsidian) != 1 {
		t.Fatalf("expected 1 reflection for 黒曜, got %d", len(obsidian))
	}
	if obsidian[0].Score != 5 {
		t.Errorf("score = %d, want 5", obsidian[0].Score)
	}
	if obsidian[0].Feedback != "Excellent implementation" {
		t.Errorf("feedback = %q, want %q", obsidian[0].Feedback, "Excellent implementation")
	}
	if obsidian[0].Improvement != "None needed" {
		t.Errorf("improvement = %q, want %q", obsidian[0].Improvement, "None needed")
	}
	if obsidian[0].TaskID != "task-003" {
		t.Errorf("taskId = %q, want %q", obsidian[0].TaskID, "task-003")
	}
}

func TestStoreReflectionSpecialChars(t *testing.T) {
	dbPath := tempReflectionDB(t)

	ref := &ReflectionResult{
		TaskID:      "task-special",
		Role:        "琥珀",
		Score:       3,
		Feedback:    `She said "it's fine" and that's that`,
		Improvement: "Use apostrophes carefully",
		CostUSD:     0.01,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	if err := storeReflection(dbPath, ref); err != nil {
		t.Fatalf("storeReflection with special chars: %v", err)
	}

	results, err := queryReflections(dbPath, "琥珀", 10)
	if err != nil {
		t.Fatalf("queryReflections: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Feedback != ref.Feedback {
		t.Errorf("feedback = %q, want %q", results[0].Feedback, ref.Feedback)
	}
}

// --- buildReflectionContext tests ---

func TestBuildReflectionContext(t *testing.T) {
	dbPath := tempReflectionDB(t)

	now := time.Now().UTC().Format(time.RFC3339)

	storeReflection(dbPath, &ReflectionResult{
		TaskID: "t1", Role: "翡翠", Score: 3,
		Feedback: "OK", Improvement: "Be more thorough",
		CostUSD: 0.01, CreatedAt: now,
	})
	storeReflection(dbPath, &ReflectionResult{
		TaskID: "t2", Role: "翡翠", Score: 2,
		Feedback: "Needs work", Improvement: "Check all sources",
		CostUSD: 0.01, CreatedAt: now,
	})

	ctx := buildReflectionContext(dbPath, "翡翠", 5)
	if ctx == "" {
		t.Fatal("buildReflectionContext returned empty string")
	}
	if !stringContains(ctx, "Recent self-assessments for role 翡翠") {
		t.Errorf("missing header in context: %q", ctx)
	}
	if !stringContains(ctx, "Score: 3/5") {
		t.Errorf("missing score 3/5 in context: %q", ctx)
	}
	if !stringContains(ctx, "Score: 2/5") {
		t.Errorf("missing score 2/5 in context: %q", ctx)
	}
	if !stringContains(ctx, "Be more thorough") {
		t.Errorf("missing improvement text in context: %q", ctx)
	}
}

func TestBuildReflectionContext_Empty(t *testing.T) {
	dbPath := tempReflectionDB(t)

	// No reflections stored — should return empty.
	ctx := buildReflectionContext(dbPath, "翡翠", 5)
	if ctx != "" {
		t.Errorf("expected empty context, got %q", ctx)
	}
}

func TestBuildReflectionContext_EmptyRole(t *testing.T) {
	ctx := buildReflectionContext("/tmp/nonexistent.db", "", 5)
	if ctx != "" {
		t.Errorf("expected empty context for empty role, got %q", ctx)
	}
}

func TestBuildReflectionContext_EmptyDBPath(t *testing.T) {
	ctx := buildReflectionContext("", "翡翠", 5)
	if ctx != "" {
		t.Errorf("expected empty context for empty dbPath, got %q", ctx)
	}
}

// --- Budget helper ---

func TestReflectionBudgetOrDefault(t *testing.T) {
	// Nil config.
	if got := reflectionBudgetOrDefault(nil); got != 0.05 {
		t.Errorf("nil config: budget = %f, want 0.05", got)
	}

	// Zero budget (use default).
	cfg := &Config{}
	if got := reflectionBudgetOrDefault(cfg); got != 0.05 {
		t.Errorf("zero budget: budget = %f, want 0.05", got)
	}

	// Custom budget.
	cfg.Reflection.Budget = 0.10
	if got := reflectionBudgetOrDefault(cfg); got != 0.10 {
		t.Errorf("custom budget: budget = %f, want 0.10", got)
	}
}

// --- extractJSON tests ---

func TestExtractJSON_Simple(t *testing.T) {
	input := `{"key":"value"}`
	got := extractJSON(input)
	if got != `{"key":"value"}` {
		t.Errorf("extractJSON = %q, want %q", got, input)
	}
}

func TestExtractJSON_Nested(t *testing.T) {
	input := `{"outer":{"inner":"value"}}`
	got := extractJSON(input)
	if got != input {
		t.Errorf("extractJSON = %q, want %q", got, input)
	}
}

func TestExtractJSON_NoJSON(t *testing.T) {
	got := extractJSON("no json here")
	if got != "" {
		t.Errorf("extractJSON = %q, want empty", got)
	}
}

func TestExtractJSON_MarkdownWrapped(t *testing.T) {
	input := "```json\n{\"score\":5}\n```"
	got := extractJSON(input)
	if got != `{"score":5}` {
		t.Errorf("extractJSON = %q, want %q", got, `{"score":5}`)
	}
}

// --- Helper ---

func stringContains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
