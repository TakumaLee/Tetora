package reflection

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newRef(taskID, agent, improvement string, score int) *Result {
	return &Result{
		TaskID:      taskID,
		Agent:       agent,
		Score:       score,
		Improvement: improvement,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}
}

func TestExtractAutoLesson_RecordsLessonEvent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "history.db")
	if err := InitDB(dbPath); err != nil {
		t.Fatalf("InitDB: %v", err)
	}

	ref := newRef("task-evt-1", "hisui", "Always verify sources before responding", 1)
	if err := ExtractAutoLesson(dir, dbPath, ref); err != nil {
		t.Fatalf("ExtractAutoLesson: %v", err)
	}

	events, err := QueryLessonHistory(dbPath, "", 10)
	if err != nil {
		t.Fatalf("QueryLessonHistory: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].TaskID != "task-evt-1" {
		t.Errorf("task_id mismatch: %q", events[0].TaskID)
	}
	if events[0].Agent != "hisui" {
		t.Errorf("agent mismatch: %q", events[0].Agent)
	}
	if !strings.HasPrefix(events[0].LessonKey, "Always verify") {
		t.Errorf("lesson_key mismatch: %q", events[0].LessonKey)
	}
}

func TestExtractAutoLesson_EventRecordedEvenOnMarkdownDedup(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "history.db")
	if err := InitDB(dbPath); err != nil {
		t.Fatalf("InitDB: %v", err)
	}

	// Two different tasks trigger the exact same improvement text.
	if err := ExtractAutoLesson(dir, dbPath, newRef("task-a", "hisui", "Check all edge cases before finalising output", 1)); err != nil {
		t.Fatal(err)
	}
	if err := ExtractAutoLesson(dir, dbPath, newRef("task-b", "kokuyou", "Check all edge cases before finalising output", 2)); err != nil {
		t.Fatal(err)
	}

	// Markdown dedup should keep only one entry…
	md, _ := os.ReadFile(filepath.Join(dir, "memory", "auto-lessons.md"))
	if strings.Count(string(md), "[pending]") != 1 {
		t.Errorf("expected exactly 1 pending entry in markdown, got %d", strings.Count(string(md), "[pending]"))
	}

	// …but both events must be recorded for the promotion count.
	events, err := QueryLessonHistory(dbPath, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
}

func TestScanPromotionCandidates_MeetsThreshold(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "history.db")
	if err := InitDB(dbPath); err != nil {
		t.Fatalf("InitDB: %v", err)
	}

	improvement := "Never commit without verifying staged files"
	for i := 0; i < 4; i++ {
		ref := newRef(
			"task-scan-"+string(rune('a'+i)),
			"kokuyou",
			improvement,
			1)
		if err := ExtractAutoLesson(dir, dbPath, ref); err != nil {
			t.Fatal(err)
		}
	}

	got, err := ScanPromotionCandidates(dbPath, 3)
	if err != nil {
		t.Fatalf("ScanPromotionCandidates: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(got))
	}
	if got[0].Occurrences != 4 {
		t.Errorf("expected 4 occurrences, got %d", got[0].Occurrences)
	}
	if len(got[0].TaskIDs) != 4 {
		t.Errorf("expected 4 distinct task IDs, got %d", len(got[0].TaskIDs))
	}

	// Below threshold → no candidate.
	got, err = ScanPromotionCandidates(dbPath, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 candidates at threshold=5, got %d", len(got))
	}
}

func TestPromoteLessons_AppliesReportAndFlipsMarkers(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "history.db")
	if err := InitDB(dbPath); err != nil {
		t.Fatalf("InitDB: %v", err)
	}

	improvement := "Record commit hash immediately after code commit"
	for i := 0; i < 3; i++ {
		ref := newRef(
			"task-prom-"+string(rune('0'+i)),
			"kokuyou",
			improvement,
			2)
		if err := ExtractAutoLesson(dir, dbPath, ref); err != nil {
			t.Fatal(err)
		}
	}

	// Dry-run should not touch files.
	result, err := PromoteLessons(dir, dbPath, 3, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Candidates) != 1 {
		t.Fatalf("dry-run: expected 1 candidate, got %d", len(result.Candidates))
	}
	if result.ReportPath != "" {
		t.Errorf("dry-run should not set ReportPath, got %q", result.ReportPath)
	}
	if _, err := os.Stat(filepath.Join(dir, "rules")); err == nil {
		// rules/ may exist but must not contain an auto-promoted-*.md
		entries, _ := os.ReadDir(filepath.Join(dir, "rules"))
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "auto-promoted-") {
				t.Errorf("dry-run created %s", e.Name())
			}
		}
	}

	// Apply mode should write the report and flip the marker.
	result, err = PromoteLessons(dir, dbPath, 3, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.ReportPath == "" {
		t.Fatal("apply: expected ReportPath to be set")
	}
	if _, err := os.Stat(result.ReportPath); err != nil {
		t.Errorf("apply: report file not written: %v", err)
	}
	md, err := os.ReadFile(filepath.Join(dir, "memory", "auto-lessons.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(md), "[pending]") {
		t.Errorf("expected [pending] to be flipped, markdown still contains it:\n%s", md)
	}
	if !strings.Contains(string(md), "[promoted-") {
		t.Errorf("expected markdown to contain [promoted-…], got:\n%s", md)
	}
}

func TestAuditStaleRules(t *testing.T) {
	dir := t.TempDir()
	rulesDir := filepath.Join(dir, "rules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Stale rule: backdated mtime.
	stale := filepath.Join(rulesDir, "old-rule.md")
	if err := os.WriteFile(stale, []byte("# old"), 0o644); err != nil {
		t.Fatal(err)
	}
	backdate := time.Now().Add(-120 * 24 * time.Hour)
	if err := os.Chtimes(stale, backdate, backdate); err != nil {
		t.Fatal(err)
	}
	// Fresh rule: now.
	fresh := filepath.Join(rulesDir, "new-rule.md")
	if err := os.WriteFile(fresh, []byte("# new"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Ignored files.
	_ = os.WriteFile(filepath.Join(rulesDir, "INDEX.md"), []byte("idx"), 0o644)
	_ = os.WriteFile(filepath.Join(rulesDir, "auto-promoted-20200101.md"), []byte("auto"), 0o644)

	got, err := AuditStaleRules(dir, 90)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 stale rule, got %d: %+v", len(got), got)
	}
	if !strings.HasSuffix(got[0].Path, "old-rule.md") {
		t.Errorf("unexpected path: %s", got[0].Path)
	}
	if got[0].AgeDays < 119 {
		t.Errorf("age should be ~120, got %d", got[0].AgeDays)
	}
}

func TestQueryLessonHistory_PrefixFilter(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "history.db")
	if err := InitDB(dbPath); err != nil {
		t.Fatal(err)
	}
	refA := newRef("task-x", "hisui", "Always run tests locally before pushing", 1)
	refB := newRef("task-y", "hisui", "Never bypass pre-commit hooks", 2)
	if err := ExtractAutoLesson(dir, dbPath, refA); err != nil {
		t.Fatal(err)
	}
	if err := ExtractAutoLesson(dir, dbPath, refB); err != nil {
		t.Fatal(err)
	}

	got, err := QueryLessonHistory(dbPath, "Always", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 event with prefix 'Always', got %d", len(got))
	}
	if got[0].TaskID != "task-x" {
		t.Errorf("unexpected task_id: %s", got[0].TaskID)
	}
}
