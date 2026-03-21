package coord

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func setupCoordDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"claims", "findings", "blockers"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestWriteAndReadClaims(t *testing.T) {
	dir := setupCoordDir(t)
	now := time.Now().UTC()

	c := Claim{
		Version:   "1",
		Type:      "claim",
		TaskID:    "task-100",
		Agent:     "ruri",
		ClaimedAt: now,
		ExpiresAt: now.Add(2 * time.Hour),
		Regions:   []string{"/projects/foo"},
		Status:    "active",
	}
	if err := WriteClaim(dir, c); err != nil {
		t.Fatal(err)
	}

	// Verify file exists.
	path := filepath.Join(dir, "claims", "task-100__ruri.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatal("claim file not created:", err)
	}

	// Read back.
	active, err := ReadActiveClaims(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 {
		t.Fatalf("expected 1 active claim, got %d", len(active))
	}
	if active[0].TaskID != "task-100" || active[0].Agent != "ruri" {
		t.Fatalf("unexpected claim: %+v", active[0])
	}
}

func TestReadActiveClaims_SkipsExpired(t *testing.T) {
	dir := setupCoordDir(t)
	now := time.Now().UTC()

	expired := Claim{
		Version:   "1",
		Type:      "claim",
		TaskID:    "task-200",
		Agent:     "hisui",
		ClaimedAt: now.Add(-3 * time.Hour),
		ExpiresAt: now.Add(-1 * time.Hour),
		Regions:   []string{"/projects/bar"},
		Status:    "active",
	}
	if err := WriteClaim(dir, expired); err != nil {
		t.Fatal(err)
	}

	active, err := ReadActiveClaims(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 0 {
		t.Fatalf("expected 0 active claims, got %d", len(active))
	}
}

func TestReadActiveClaims_SkipsReleased(t *testing.T) {
	dir := setupCoordDir(t)
	now := time.Now().UTC()

	released := Claim{
		Version:   "1",
		Type:      "claim",
		TaskID:    "task-300",
		Agent:     "kokuyou",
		ClaimedAt: now,
		ExpiresAt: now.Add(2 * time.Hour),
		Regions:   []string{"/projects/baz"},
		Status:    "released",
	}
	if err := WriteClaim(dir, released); err != nil {
		t.Fatal(err)
	}

	active, err := ReadActiveClaims(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 0 {
		t.Fatalf("expected 0 active claims, got %d", len(active))
	}
}

func TestReleaseClaim(t *testing.T) {
	dir := setupCoordDir(t)
	now := time.Now().UTC()

	c := Claim{
		Version:   "1",
		Type:      "claim",
		TaskID:    "task-400",
		Agent:     "kohaku",
		ClaimedAt: now,
		ExpiresAt: now.Add(2 * time.Hour),
		Regions:   []string{"/projects/qux"},
		Status:    "active",
	}
	if err := WriteClaim(dir, c); err != nil {
		t.Fatal(err)
	}

	if err := ReleaseClaim(dir, "task-400", "kohaku"); err != nil {
		t.Fatal(err)
	}

	// Verify status changed.
	data, _ := os.ReadFile(filepath.Join(dir, "claims", "task-400__kohaku.json"))
	var updated Claim
	json.Unmarshal(data, &updated)
	if updated.Status != "released" {
		t.Fatalf("expected released, got %s", updated.Status)
	}

	// Should not appear in active claims.
	active, _ := ReadActiveClaims(dir)
	if len(active) != 0 {
		t.Fatalf("expected 0 active after release, got %d", len(active))
	}
}

func TestCheckConflict(t *testing.T) {
	now := time.Now().UTC()
	claims := []Claim{
		{TaskID: "task-500", Agent: "ruri", Regions: []string{"/projects/alpha"}, ClaimedAt: now, ExpiresAt: now.Add(2 * time.Hour), Status: "active"},
		{TaskID: "task-501", Agent: "hisui", Regions: []string{"/projects/beta"}, ClaimedAt: now, ExpiresAt: now.Add(2 * time.Hour), Status: "active"},
	}

	// Exact match.
	if c := CheckConflict(claims, []string{"/projects/alpha"}); c == nil || c.TaskID != "task-500" {
		t.Fatal("expected conflict with task-500")
	}

	// Subdirectory overlap.
	if c := CheckConflict(claims, []string{"/projects/beta/src"}); c == nil || c.TaskID != "task-501" {
		t.Fatal("expected conflict with task-501 (subdirectory)")
	}

	// No conflict.
	if c := CheckConflict(claims, []string{"/projects/gamma"}); c != nil {
		t.Fatalf("expected no conflict, got %+v", c)
	}
}

func TestWriteFinding(t *testing.T) {
	dir := setupCoordDir(t)
	now := time.Now().UTC()

	f := Finding{
		Version:    "1",
		Type:       "finding",
		TaskID:     "task-600",
		Agent:      "spinel",
		RecordedAt: now,
		Summary:    "Implemented feature X",
		Artifacts:  []string{"src/x.go"},
	}
	if err := WriteFinding(dir, f); err != nil {
		t.Fatal(err)
	}

	files, _ := filepath.Glob(filepath.Join(dir, "findings", "task-600__spinel__*.json"))
	if len(files) != 1 {
		t.Fatalf("expected 1 finding file, got %d", len(files))
	}
}

func TestWriteBlocker(t *testing.T) {
	dir := setupCoordDir(t)
	now := time.Now().UTC()

	b := Blocker{
		Version:       "1",
		Type:          "blocker",
		TaskID:        "task-700",
		Agent:         "ruri",
		BlockedAt:     now,
		Severity:      "high",
		Description:   "Waiting for task-600",
		DependsOnTask: "task-600",
	}
	if err := WriteBlocker(dir, b); err != nil {
		t.Fatal(err)
	}

	files, _ := filepath.Glob(filepath.Join(dir, "blockers", "task-700__ruri__*.json"))
	if len(files) != 1 {
		t.Fatalf("expected 1 blocker file, got %d", len(files))
	}
}

func TestResolveBlockersFor(t *testing.T) {
	dir := setupCoordDir(t)
	now := time.Now().UTC()

	// Write a blocker that depends on task-800.
	b := Blocker{
		Version:       "1",
		Type:          "blocker",
		TaskID:        "task-900",
		Agent:         "hisui",
		BlockedAt:     now,
		Severity:      "high",
		Description:   "Waiting for task-800",
		DependsOnTask: "task-800",
	}
	if err := WriteBlocker(dir, b); err != nil {
		t.Fatal(err)
	}

	// Write an unrelated blocker.
	b2 := Blocker{
		Version:       "1",
		Type:          "blocker",
		TaskID:        "task-901",
		Agent:         "kokuyou",
		BlockedAt:     now,
		Severity:      "medium",
		Description:   "Waiting for task-999",
		DependsOnTask: "task-999",
	}
	if err := WriteBlocker(dir, b2); err != nil {
		t.Fatal(err)
	}

	// Resolve blockers for task-800.
	if err := ResolveBlockersFor(dir, "task-800", "ruri", "Task completed"); err != nil {
		t.Fatal(err)
	}

	// Check resolved blocker.
	files, _ := filepath.Glob(filepath.Join(dir, "blockers", "task-900__hisui__*.json"))
	if len(files) != 1 {
		t.Fatal("expected 1 blocker file for task-900")
	}
	data, _ := os.ReadFile(files[0])
	var resolved Blocker
	json.Unmarshal(data, &resolved)
	if resolved.Resolution != "Task completed" {
		t.Fatalf("expected resolution, got %q", resolved.Resolution)
	}
	if resolved.ResolvedBy != "ruri" {
		t.Fatalf("expected resolved_by=ruri, got %q", resolved.ResolvedBy)
	}
	if resolved.ResolvedAt == nil {
		t.Fatal("expected resolved_at to be set")
	}

	// Check unrelated blocker is untouched.
	files2, _ := filepath.Glob(filepath.Join(dir, "blockers", "task-901__kokuyou__*.json"))
	data2, _ := os.ReadFile(files2[0])
	var untouched Blocker
	json.Unmarshal(data2, &untouched)
	if untouched.Resolution != "" {
		t.Fatalf("unrelated blocker should not be resolved, got %q", untouched.Resolution)
	}
}

func TestRegionsOverlap(t *testing.T) {
	tests := []struct {
		a, b []string
		want bool
	}{
		{[]string{"/a/b"}, []string{"/a/b"}, true},
		{[]string{"/a/b"}, []string{"/a/b/c"}, true},
		{[]string{"/a/b/c"}, []string{"/a/b"}, true},
		{[]string{"/a/b"}, []string{"/a/bc"}, false},
		{[]string{"/x"}, []string{"/y"}, false},
	}
	for _, tt := range tests {
		got := regionsOverlap(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("regionsOverlap(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}
