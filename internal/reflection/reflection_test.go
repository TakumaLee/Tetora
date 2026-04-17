package reflection

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- ParseOutput / scoring dimension tests ---

func TestParseOutput_NewFields(t *testing.T) {
	output := `{"score":2,"feedback":"output cut off","improvement":"fix truncation","truncation_detected":true,"output_quality":"good","execution_issue":false}`
	ref, err := ParseOutput(output)
	if err != nil {
		t.Fatalf("ParseOutput: %v", err)
	}
	if !ref.TruncationDetected {
		t.Error("expected TruncationDetected=true")
	}
	if ref.OutputQuality != "good" {
		t.Errorf("expected OutputQuality=good, got %q", ref.OutputQuality)
	}
	if ref.ExecutionIssue {
		t.Error("expected ExecutionIssue=false")
	}
	if ref.Score != 2 {
		t.Errorf("ParseOutput raw score: expected 2, got %d", ref.Score)
	}
}

func TestParseOutput_LegacyJSON_MissingNewFields(t *testing.T) {
	// Old-format JSON without new fields must still parse correctly.
	output := `{"score":4,"feedback":"great","improvement":"keep it up"}`
	ref, err := ParseOutput(output)
	if err != nil {
		t.Fatalf("ParseOutput legacy: %v", err)
	}
	if ref.TruncationDetected {
		t.Error("legacy JSON: expected TruncationDetected=false")
	}
	if ref.ExecutionIssue {
		t.Error("legacy JSON: expected ExecutionIssue=false")
	}
	if ref.Score != 4 {
		t.Errorf("legacy JSON score: expected 4, got %d", ref.Score)
	}
}

// applyScoreOverride mirrors the override logic in Perform() for unit testing.
func applyScoreOverride(ref *Result) {
	if ref.TruncationDetected && ref.OutputQuality == "good" && ref.Score < 3 {
		ref.Score = 3
	}
}

func TestScoreOverride_TruncationGoodQuality_ClampsToThree(t *testing.T) {
	ref := &Result{Score: 1, TruncationDetected: true, OutputQuality: "good"}
	applyScoreOverride(ref)
	if ref.Score != 3 {
		t.Errorf("expected score clamped to 3, got %d", ref.Score)
	}
}

func TestScoreOverride_TruncationGoodQuality_ScoreAlreadyThree(t *testing.T) {
	ref := &Result{Score: 3, TruncationDetected: true, OutputQuality: "good"}
	applyScoreOverride(ref)
	if ref.Score != 3 {
		t.Errorf("expected score unchanged at 3, got %d", ref.Score)
	}
}

func TestScoreOverride_TruncationGoodQuality_ScoreFour_NotChanged(t *testing.T) {
	ref := &Result{Score: 4, TruncationDetected: true, OutputQuality: "good"}
	applyScoreOverride(ref)
	if ref.Score != 4 {
		t.Errorf("expected score unchanged at 4, got %d", ref.Score)
	}
}

func TestScoreOverride_TruncationPoorQuality_NotOverridden(t *testing.T) {
	// Truncation + poor quality → score stays as LLM set it (not overridden).
	ref := &Result{Score: 1, TruncationDetected: true, OutputQuality: "poor"}
	applyScoreOverride(ref)
	if ref.Score != 1 {
		t.Errorf("truncation+poor: expected score unchanged at 1, got %d", ref.Score)
	}
}

func TestScoreOverride_ExecutionIssue_NotOverridden(t *testing.T) {
	// Execution issue (scope/language violation) → score stays at minimum.
	ref := &Result{Score: 1, TruncationDetected: false, OutputQuality: "poor", ExecutionIssue: true}
	applyScoreOverride(ref)
	if ref.Score != 1 {
		t.Errorf("execution issue: expected score unchanged at 1, got %d", ref.Score)
	}
}

func TestScoreOverride_NoTruncation_NoChange(t *testing.T) {
	ref := &Result{Score: 2, TruncationDetected: false, OutputQuality: "poor", ExecutionIssue: false}
	applyScoreOverride(ref)
	if ref.Score != 2 {
		t.Errorf("no truncation: expected score unchanged at 2, got %d", ref.Score)
	}
}

// --- ExtractAutoLesson tests ---

func TestExtractAutoLesson_CreatesFile(t *testing.T) {
	dir := t.TempDir()

	ref := &Result{
		TaskID:      "task-001",
		Agent:       "hisui",
		Score:       1,
		Feedback:    "Very poor output",
		Improvement: "Always verify sources before responding",
		CostUSD:     0.01,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}

	if err := ExtractAutoLesson(dir, ref); err != nil {
		t.Fatalf("ExtractAutoLesson: %v", err)
	}

	autoPath := filepath.Join(dir, "rules", "auto-lessons.md")
	data, err := os.ReadFile(autoPath)
	if err != nil {
		t.Fatalf("auto-lessons.md not created: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "# Auto-Lessons") {
		t.Errorf("missing header in auto-lessons.md: %q", content)
	}
	if !strings.Contains(content, ref.Improvement) {
		t.Errorf("missing improvement text in auto-lessons.md: %q", content)
	}
	if !strings.Contains(content, "score=1") {
		t.Errorf("missing score annotation in auto-lessons.md: %q", content)
	}
	if !strings.Contains(content, "[pending]") {
		t.Errorf("missing [pending] status in auto-lessons.md: %q", content)
	}
}

func TestExtractAutoLesson_Dedup(t *testing.T) {
	dir := t.TempDir()

	ref := &Result{
		TaskID:      "task-dedup",
		Agent:       "hisui",
		Score:       2,
		Feedback:    "Incomplete",
		Improvement: "Check all edge cases before finalising output",
		CostUSD:     0.01,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}

	// First call — should write.
	if err := ExtractAutoLesson(dir, ref); err != nil {
		t.Fatalf("first ExtractAutoLesson: %v", err)
	}

	autoPath := filepath.Join(dir, "rules", "auto-lessons.md")
	data1, _ := os.ReadFile(autoPath)

	// Second call with same improvement — should be a no-op.
	if err := ExtractAutoLesson(dir, ref); err != nil {
		t.Fatalf("second ExtractAutoLesson: %v", err)
	}

	data2, _ := os.ReadFile(autoPath)

	// File size must not grow.
	if len(data2) != len(data1) {
		t.Errorf("dedup failed: file grew from %d to %d bytes", len(data1), len(data2))
	}

	// Entry must appear exactly once.
	needle := ref.Improvement[:40]
	count := strings.Count(string(data2), needle)
	if count != 1 {
		t.Errorf("expected improvement to appear exactly once, got %d occurrences", count)
	}
}

func TestExtractAutoLesson_ScoreTooHigh(t *testing.T) {
	dir := t.TempDir()

	ref := &Result{
		TaskID:      "task-high",
		Agent:       "hisui",
		Score:       3,
		Feedback:    "Good enough",
		Improvement: "This should not be written to auto-lessons",
		CostUSD:     0.01,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}

	if err := ExtractAutoLesson(dir, ref); err != nil {
		t.Fatalf("ExtractAutoLesson with score=3: %v", err)
	}

	autoPath := filepath.Join(dir, "rules", "auto-lessons.md")
	if _, err := os.Stat(autoPath); !os.IsNotExist(err) {
		data, _ := os.ReadFile(autoPath)
		// File should not have been created at all, but if it exists it must not contain our text.
		if strings.Contains(string(data), ref.Improvement) {
			t.Errorf("score=3 should be a no-op but improvement was written: %q", string(data))
		}
	}
}

func TestExtractAutoLesson_NilRef(t *testing.T) {
	dir := t.TempDir()
	if err := ExtractAutoLesson(dir, nil); err != nil {
		t.Errorf("nil ref should be a no-op, got: %v", err)
	}
}

func TestExtractAutoLesson_EmptyImprovement(t *testing.T) {
	dir := t.TempDir()

	ref := &Result{
		TaskID:      "task-empty",
		Agent:       "hisui",
		Score:       1,
		Feedback:    "Very bad",
		Improvement: "", // empty — should be a no-op
		CostUSD:     0.01,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}

	if err := ExtractAutoLesson(dir, ref); err != nil {
		t.Errorf("empty improvement should be a no-op, got: %v", err)
	}

	autoPath := filepath.Join(dir, "rules", "auto-lessons.md")
	if _, err := os.Stat(autoPath); !os.IsNotExist(err) {
		t.Error("auto-lessons.md should not be created for empty improvement")
	}
}
