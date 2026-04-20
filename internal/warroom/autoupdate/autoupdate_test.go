package autoupdate

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"tetora/internal/config"
	"tetora/internal/warroom"
)

// --- helpers ---

func writeStatus(t *testing.T, path string, fronts []map[string]any) {
	t.Helper()
	rawFronts := make([]json.RawMessage, len(fronts))
	for i, f := range fronts {
		b, err := json.Marshal(f)
		if err != nil {
			t.Fatalf("marshal front %d: %v", i, err)
		}
		rawFronts[i] = b
	}
	s := warroom.Status{
		SchemaVersion: 3,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		Fronts:        rawFronts,
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := warroom.SaveStatus(path, &s); err != nil {
		t.Fatalf("SaveStatus: %v", err)
	}
}

func jsonLogicalEqual(t *testing.T, a, b json.RawMessage) bool {
	t.Helper()
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		t.Fatalf("unmarshal a: %v", err)
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		t.Fatalf("unmarshal b: %v", err)
	}
	return reflect.DeepEqual(av, bv)
}

// --- Polymarket updater ---

func TestUpdatePolymarket_PicksLatestHealthFile(t *testing.T) {
	baseDir := t.TempDir()
	dailyDir := filepath.Join(baseDir, "workspace/memory/daily")
	if err := os.MkdirAll(dailyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	today := time.Now().Format("2006-01-02")
	for _, d := range []string{yesterday, today, "2026-01-01"} {
		p := filepath.Join(dailyDir, "polymarket-health-"+d+".md")
		if err := os.WriteFile(p, []byte("# Polymarket Health "+d+"\n\n## Summary\n- 今天的最新摘要 "+d+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := &config.Config{BaseDir: baseDir}
	updates, err := updatePolymarket(context.Background(), cfg, json.RawMessage(`{"id":"polymarket"}`))
	if err != nil {
		t.Fatalf("updatePolymarket: %v", err)
	}
	if updates == nil {
		t.Fatal("expected updates, got nil")
	}
	summary, _ := updates["summary"].(string)
	if !strings.Contains(summary, today) {
		t.Errorf("expected summary to reference latest date %q, got %q", today, summary)
	}
}

func TestUpdatePolymarket_StaleMarksRed(t *testing.T) {
	baseDir := t.TempDir()
	dailyDir := filepath.Join(baseDir, "workspace/memory/daily")
	if err := os.MkdirAll(dailyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().AddDate(0, 0, -10).Format("2006-01-02")
	p := filepath.Join(dailyDir, "polymarket-health-"+old+".md")
	if err := os.WriteFile(p, []byte("# Old\n\n## Summary\n- stale data\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{BaseDir: baseDir}
	updates, err := updatePolymarket(context.Background(), cfg, json.RawMessage(`{"id":"polymarket"}`))
	if err != nil {
		t.Fatalf("updatePolymarket: %v", err)
	}
	if status, _ := updates["status"].(string); status != "red" {
		t.Errorf("expected status=red for stale data, got %q", status)
	}
}

func TestUpdatePolymarket_NoFiles(t *testing.T) {
	baseDir := t.TempDir()
	cfg := &config.Config{BaseDir: baseDir}
	updates, err := updatePolymarket(context.Background(), cfg, json.RawMessage(`{"id":"polymarket"}`))
	if err != nil {
		t.Fatalf("updatePolymarket: %v", err)
	}
	if updates != nil {
		t.Errorf("expected nil updates when no health files, got %v", updates)
	}
}

// --- Taiwan Stock updater ---

func TestUpdateTaiwanStockAuto_NoDB(t *testing.T) {
	baseDir := t.TempDir()
	cfg := &config.Config{BaseDir: baseDir}
	missingDB := filepath.Join(baseDir, "nonexistent-trading.db")
	updates, err := updateTaiwanStockAutoAt(context.Background(), cfg, json.RawMessage(`{"id":"taiwan-stock-auto"}`), missingDB)
	if err != nil {
		t.Fatalf("updateTaiwanStockAutoAt: %v", err)
	}
	if updates != nil {
		t.Errorf("expected nil updates when DB missing, got %v", updates)
	}
}

func TestResolveTradingDBPath_EnvOverride(t *testing.T) {
	t.Setenv("STOCK_TRADING_DB_PATH", "/tmp/custom-trading.db")
	if got := resolveTradingDBPath(); got != "/tmp/custom-trading.db" {
		t.Errorf("env override not respected: got %q", got)
	}
}

// --- Tetora updater ---

func TestUpdateTetoraAt_GitInfo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=t@test",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=t@test")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	run("init", "-b", "feat/testbranch")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "initial: subject-test")

	// Create tasks dir with 2 .md files.
	tasksDir := filepath.Join(dir, "tasks")
	os.MkdirAll(tasksDir, 0o755)
	os.WriteFile(filepath.Join(tasksDir, "a.md"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(tasksDir, "b.md"), []byte("x"), 0o644)

	updates, err := updateTetoraAt(context.Background(), dir)
	if err != nil {
		t.Fatalf("updateTetoraAt: %v", err)
	}
	summary, _ := updates["summary"].(string)
	if !strings.Contains(summary, "feat/testbranch") {
		t.Errorf("summary missing branch: %q", summary)
	}
	if !strings.Contains(summary, "subject-test") {
		t.Errorf("summary missing commit subject: %q", summary)
	}
	if !strings.Contains(summary, "2") {
		t.Errorf("summary missing task count: %q", summary)
	}
}

func TestUpdateTetoraAt_MissingDir(t *testing.T) {
	updates, err := updateTetoraAt(context.Background(), "/nonexistent/path/for/tetora/test")
	if err != nil {
		t.Fatalf("updateTetoraAt: %v", err)
	}
	if updates != nil {
		t.Errorf("expected nil updates when project dir missing, got %v", updates)
	}
}

// --- Run() integration ---

func TestRun_SkipsNonAutoFronts(t *testing.T) {
	baseDir := t.TempDir()
	statusPath := filepath.Join(baseDir, "workspace/memory/war-room/status.json")

	autoFront := map[string]any{
		"id":      "polymarket",
		"auto":    true,
		"summary": "original",
	}
	nonAutoFront := map[string]any{
		"id":      "marketing",
		"auto":    false,
		"summary": "untouched",
		"extra":   "keep-me",
	}
	writeStatus(t, statusPath, []map[string]any{autoFront, nonAutoFront})

	// Capture bytes of non-auto front before run.
	before, err := warroom.LoadStatus(statusPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeNonAuto := before.Fronts[1]

	cfg := &config.Config{BaseDir: baseDir}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	after, err := warroom.LoadStatus(statusPath)
	if err != nil {
		t.Fatal(err)
	}
	if !jsonLogicalEqual(t, beforeNonAuto, after.Fronts[1]) {
		t.Errorf("non-auto front changed:\nbefore: %s\nafter:  %s", beforeNonAuto, after.Fronts[1])
	}
}

func TestRun_SkipsManualOverrideActive(t *testing.T) {
	baseDir := t.TempDir()
	statusPath := filepath.Join(baseDir, "workspace/memory/war-room/status.json")

	future := time.Now().Add(1 * time.Hour).Format(time.RFC3339)
	front := map[string]any{
		"id":      "polymarket",
		"auto":    true,
		"summary": "original-override",
		"manual_override": map[string]any{
			"active":     true,
			"expires_at": future,
		},
	}
	writeStatus(t, statusPath, []map[string]any{front})

	cfg := &config.Config{BaseDir: baseDir}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	after, err := warroom.LoadStatus(statusPath)
	if err != nil {
		t.Fatal(err)
	}
	var summary string
	if err := warroom.FrontField(after.Fronts[0], "summary", &summary); err != nil {
		t.Fatal(err)
	}
	if summary != "original-override" {
		t.Errorf("manual override ignored: summary changed to %q", summary)
	}
}

func TestRun_SkipsExpiredOverride(t *testing.T) {
	baseDir := t.TempDir()
	statusPath := filepath.Join(baseDir, "workspace/memory/war-room/status.json")

	past := time.Now().Add(-1 * time.Hour).Format(time.RFC3339)
	front := map[string]any{
		"id":      "polymarket",
		"auto":    true,
		"summary": "original-expired",
		"manual_override": map[string]any{
			"active":     true,
			"expires_at": past,
		},
	}
	writeStatus(t, statusPath, []map[string]any{front})

	// No polymarket health files exist → updater returns nil → no change expected.
	cfg := &config.Config{BaseDir: baseDir}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	after, err := warroom.LoadStatus(statusPath)
	if err != nil {
		t.Fatal(err)
	}
	// Front should have been eligible (expired override) — but with no data the
	// updater returns nil, so summary stays.
	var summary string
	warroom.FrontField(after.Fronts[0], "summary", &summary)
	if summary != "original-expired" {
		t.Errorf("unexpected summary change: %q", summary)
	}
}

func TestRun_PreservesUnknownFields(t *testing.T) {
	baseDir := t.TempDir()
	statusPath := filepath.Join(baseDir, "workspace/memory/war-room/status.json")

	// Create a polymarket health file so updater produces updates.
	dailyDir := filepath.Join(baseDir, "workspace/memory/daily")
	os.MkdirAll(dailyDir, 0o755)
	today := time.Now().Format("2006-01-02")
	os.WriteFile(filepath.Join(dailyDir, "polymarket-health-"+today+".md"),
		[]byte("# Polymarket\n\n## Status\n- healthy\n"), 0o644)

	front := map[string]any{
		"id":         "polymarket",
		"auto":       true,
		"summary":    "old",
		"depends_on": []string{"tetora"},
		"metrics": map[string]any{
			"paper_days":        0,
			"connection_status": "unknown",
		},
		"extra_field": "preserve_me",
	}
	writeStatus(t, statusPath, []map[string]any{front})

	cfg := &config.Config{BaseDir: baseDir}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	after, err := warroom.LoadStatus(statusPath)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(after.Fronts[0], &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["extra_field"]; !ok {
		t.Error("extra_field was dropped")
	}
	if _, ok := m["depends_on"]; !ok {
		t.Error("depends_on was dropped")
	}
	if _, ok := m["metrics"]; !ok {
		t.Error("metrics was dropped")
	}
	var summary string
	json.Unmarshal(m["summary"], &summary)
	if !strings.HasPrefix(summary, "[auto] ") {
		t.Errorf("expected summary to be updated with [auto] prefix, got %q", summary)
	}
}
