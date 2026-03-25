package reflection

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
