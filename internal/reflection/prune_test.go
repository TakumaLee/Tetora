package reflection

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeAutoLessons(t *testing.T, dir, content string) string {
	t.Helper()
	memDir := filepath.Join(dir, "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(memDir, "auto-lessons.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func seedReflection(t *testing.T, dbPath, taskID string, createdAt time.Time) {
	t.Helper()
	if err := Store(dbPath, &Result{
		TaskID:    taskID,
		Agent:     "kokuyou",
		Score:     2,
		Feedback:  "",
		Improvement: "seeded",
		CreatedAt: createdAt.UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPruneAutoLessons_PendingFlipsToStaleAfter30d(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "history.db")
	if err := InitDB(dbPath); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	seedReflection(t, dbPath, "task-old", now.Add(-45*24*time.Hour))
	seedReflection(t, dbPath, "task-new", now.Add(-3*24*time.Hour))

	writeAutoLessons(t, dir, strings.Join([]string{
		"# Auto-Lessons",
		"",
		"- [pending] (score=2, task=task-old, agent=kokuyou) always verify X",
		"- [pending] (score=1, task=task-new, agent=hisui) always verify Y",
		"",
	}, "\n"))

	rep, err := PruneAutoLessons(dir, dbPath, now, DefaultPruneThresholds(), false)
	if err != nil {
		t.Fatalf("PruneAutoLessons: %v", err)
	}
	if rep.PendingToStale != 1 {
		t.Errorf("PendingToStale=%d, want 1", rep.PendingToStale)
	}
	if rep.StaleRemoved != 0 || rep.RejectedRemoved != 0 {
		t.Errorf("unexpected removals: stale=%d rejected=%d", rep.StaleRemoved, rep.RejectedRemoved)
	}

	got, _ := os.ReadFile(rep.Path)
	if !strings.Contains(string(got), "[stale] (score=2, task=task-old") {
		t.Errorf("expected task-old flipped to [stale], got:\n%s", got)
	}
	if !strings.Contains(string(got), "[pending] (score=1, task=task-new") {
		t.Errorf("expected task-new to stay [pending], got:\n%s", got)
	}
}

func TestPruneAutoLessons_StaleRemovedAfter60d(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "history.db")
	if err := InitDB(dbPath); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	seedReflection(t, dbPath, "task-ancient", now.Add(-75*24*time.Hour))
	seedReflection(t, dbPath, "task-recent-stale", now.Add(-45*24*time.Hour))

	writeAutoLessons(t, dir, strings.Join([]string{
		"# Auto-Lessons",
		"",
		"- [stale] (score=2, task=task-ancient, agent=kokuyou) very old",
		"- [stale] (score=1, task=task-recent-stale, agent=ruri) recently stale",
		"",
	}, "\n"))

	rep, err := PruneAutoLessons(dir, dbPath, now, DefaultPruneThresholds(), false)
	if err != nil {
		t.Fatal(err)
	}
	if rep.StaleRemoved != 1 {
		t.Errorf("StaleRemoved=%d, want 1", rep.StaleRemoved)
	}

	got, _ := os.ReadFile(rep.Path)
	if strings.Contains(string(got), "task-ancient") {
		t.Errorf("expected task-ancient removed, still present:\n%s", got)
	}
	if !strings.Contains(string(got), "task-recent-stale") {
		t.Errorf("expected task-recent-stale kept, got:\n%s", got)
	}
}

func TestPruneAutoLessons_RejectedRemovedAfter90d(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "history.db")
	if err := InitDB(dbPath); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	seedReflection(t, dbPath, "task-rejected-old", now.Add(-100*24*time.Hour))
	seedReflection(t, dbPath, "task-rejected-new", now.Add(-45*24*time.Hour))

	writeAutoLessons(t, dir, strings.Join([]string{
		"- [rejected] (score=2, task=task-rejected-old, agent=kokuyou) bad idea",
		"- [rejected] (score=1, task=task-rejected-new, agent=hisui) recent reject",
		"",
	}, "\n"))

	rep, err := PruneAutoLessons(dir, dbPath, now, DefaultPruneThresholds(), false)
	if err != nil {
		t.Fatal(err)
	}
	if rep.RejectedRemoved != 1 {
		t.Errorf("RejectedRemoved=%d, want 1", rep.RejectedRemoved)
	}
	got, _ := os.ReadFile(rep.Path)
	if strings.Contains(string(got), "task-rejected-old") {
		t.Errorf("old reject should be gone, got:\n%s", got)
	}
	if !strings.Contains(string(got), "task-rejected-new") {
		t.Errorf("recent reject should stay, got:\n%s", got)
	}
}

func TestPruneAutoLessons_PromotedAndClusteredAreKept(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "history.db")
	if err := InitDB(dbPath); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	seedReflection(t, dbPath, "task-very-old", now.Add(-365*24*time.Hour))

	original := strings.Join([]string{
		"# Auto-Lessons",
		"",
		"- [promoted-20260101] (score=2, task=task-very-old, agent=kokuyou) promoted long ago",
		"- [clustered: git-hygiene] (score=2, task=task-very-old, agent=ruri) waiting human",
		"",
	}, "\n")
	writeAutoLessons(t, dir, original)

	rep, err := PruneAutoLessons(dir, dbPath, now, DefaultPruneThresholds(), false)
	if err != nil {
		t.Fatal(err)
	}
	if rep.PendingToStale+rep.StaleRemoved+rep.RejectedRemoved != 0 {
		t.Errorf("expected zero transitions, got report=%+v", rep)
	}
	got, _ := os.ReadFile(rep.Path)
	if string(got) != original {
		t.Errorf("file changed unexpectedly:\nwant:\n%s\ngot:\n%s", original, got)
	}
}

func TestPruneAutoLessons_DryRunDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "history.db")
	if err := InitDB(dbPath); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	seedReflection(t, dbPath, "task-old", now.Add(-60*24*time.Hour))

	original := "- [pending] (score=2, task=task-old, agent=kokuyou) test\n"
	writeAutoLessons(t, dir, original)

	rep, err := PruneAutoLessons(dir, dbPath, now, DefaultPruneThresholds(), true)
	if err != nil {
		t.Fatal(err)
	}
	if rep.PendingToStale != 1 {
		t.Errorf("dry-run report should show transition: %+v", rep)
	}
	got, _ := os.ReadFile(rep.Path)
	if string(got) != original {
		t.Errorf("dry-run modified file:\nwant:\n%s\ngot:\n%s", original, got)
	}
}

func TestPruneAutoLessons_UnknownTaskIDUsesDefault(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "history.db")
	if err := InitDB(dbPath); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

	writeAutoLessons(t, dir,
		"- [pending] (score=2, task=task-missing, agent=kokuyou) orphan\n")

	// Default age = 30d, threshold = 30d → should flip.
	rep, err := PruneAutoLessons(dir, dbPath, now, DefaultPruneThresholds(), false)
	if err != nil {
		t.Fatal(err)
	}
	if rep.PendingToStale != 1 {
		t.Errorf("orphan should flip to stale under default age, got %+v", rep)
	}
}

func TestPruneAutoLessons_NoFileNoError(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "history.db")
	if err := InitDB(dbPath); err != nil {
		t.Fatal(err)
	}
	rep, err := PruneAutoLessons(dir, dbPath, time.Now(), DefaultPruneThresholds(), false)
	if err != nil {
		t.Fatalf("missing file should be no-op, got: %v", err)
	}
	if rep.PendingToStale+rep.StaleRemoved+rep.RejectedRemoved+rep.Kept != 0 {
		t.Errorf("expected zero-value report, got %+v", rep)
	}
}
