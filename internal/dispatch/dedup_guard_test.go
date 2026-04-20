package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// initTestDB creates a temporary SQLite DB with the diagnostics_cache table.
func initTestDB(t *testing.T) (dbPath string, cleanup func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath = filepath.Join(dir, "dedup_guard.db")
	sql := `CREATE TABLE diagnostics_cache (
		root_cause_key TEXT NOT NULL PRIMARY KEY,
		last_diagnosed_at TEXT NOT NULL,
		diagnosis_count INTEGER NOT NULL DEFAULT 1,
		next_allowed_at TEXT NOT NULL,
		created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
	);`
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init test DB: %s: %v", string(out), err)
	}
	return dbPath, func() { os.RemoveAll(dir) }
}

// writeTestConfig writes a dedup-guard.json to dir and returns baseDir.
func writeTestConfig(t *testing.T, cfg DedupConfig) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(filepath.Join(dir, "config", "dedup-guard.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// --- LoadDedupConfig ---

func TestLoadDedupConfig_OK(t *testing.T) {
	cfg := DedupConfig{Enabled: true, Threshold: 3, WindowHours: 24, RootCauses: map[string]bool{"news_edge_arb_failure": true}}
	baseDir := writeTestConfig(t, cfg)
	got, err := LoadDedupConfig(baseDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.Enabled || got.Threshold != 3 || got.WindowHours != 24 {
		t.Errorf("unexpected config: %+v", got)
	}
	if !got.RootCauses["news_edge_arb_failure"] {
		t.Errorf("root cause not loaded")
	}
}

func TestLoadDedupConfig_MissingFile(t *testing.T) {
	_, err := LoadDedupConfig("/nonexistent/path")
	if err == nil {
		t.Fatal("expected error for missing config")
	}
}

func TestLoadDedupConfig_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "config"), 0o755)
	os.WriteFile(filepath.Join(dir, "config", "dedup-guard.json"), []byte("{invalid"), 0o644)
	_, err := LoadDedupConfig(dir)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// --- ExtractRootCauseKey ---

func TestExtractRootCauseKey_Match(t *testing.T) {
	cfg := &DedupConfig{RootCauses: map[string]bool{"news_edge_arb_failure": true}}
	key := ExtractRootCauseKey(cfg, "hisui: news_edge_arb_failure detected")
	if key != "news_edge_arb_failure" {
		t.Errorf("expected 'news_edge_arb_failure', got '%s'", key)
	}
}

func TestExtractRootCauseKey_CaseInsensitive(t *testing.T) {
	cfg := &DedupConfig{RootCauses: map[string]bool{"budget_cap_exceeded": true}}
	key := ExtractRootCauseKey(cfg, "BUDGET_CAP_EXCEEDED alert")
	if key != "budget_cap_exceeded" {
		t.Errorf("expected 'budget_cap_exceeded', got '%s'", key)
	}
}

func TestExtractRootCauseKey_NoMatch(t *testing.T) {
	cfg := &DedupConfig{RootCauses: map[string]bool{"news_edge_arb_failure": true}}
	key := ExtractRootCauseKey(cfg, "unrelated task name")
	if key != "" {
		t.Errorf("expected empty, got '%s'", key)
	}
}

func TestExtractRootCauseKey_DisabledCause(t *testing.T) {
	cfg := &DedupConfig{RootCauses: map[string]bool{"news_edge_arb_failure": false}}
	key := ExtractRootCauseKey(cfg, "news_edge_arb_failure alert")
	if key != "" {
		t.Errorf("expected empty (disabled root cause), got '%s'", key)
	}
}

// --- CheckAndUpdate ---

func TestCheckAndUpdate_FirstOccurrence(t *testing.T) {
	dbPath, cleanup := initTestDB(t)
	defer cleanup()

	result := CheckAndUpdate(context.Background(), dbPath, "news_edge_arb_failure", 3, 24)
	if result.Suppressed {
		t.Errorf("first occurrence should not be suppressed")
	}
}

func TestCheckAndUpdate_ThresholdTrigger(t *testing.T) {
	dbPath, cleanup := initTestDB(t)
	defer cleanup()

	ctx := context.Background()
	const key = "news_edge_arb_failure"
	const threshold = 3

	// Run threshold times to fill up.
	for i := 0; i < threshold; i++ {
		result := CheckAndUpdate(ctx, dbPath, key, threshold, 24)
		if result.Suppressed {
			t.Errorf("iteration %d: should not be suppressed yet (count=%d)", i+1, i+1)
		}
	}

	// Next call should be suppressed.
	result := CheckAndUpdate(ctx, dbPath, key, threshold, 24)
	if !result.Suppressed {
		t.Errorf("expected suppression at count=%d (threshold=%d)", threshold+1, threshold)
	}
	if result.Message == "" {
		t.Errorf("expected non-empty suppression message")
	}
}

func TestCheckAndUpdate_WindowExpiry(t *testing.T) {
	dbPath, cleanup := initTestDB(t)
	defer cleanup()

	ctx := context.Background()
	const key = "data_fetch_timeout"

	// Insert a row with next_allowed_at in the past (expired window).
	past := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
	insertSQL := fmt.Sprintf(
		`INSERT INTO diagnostics_cache (root_cause_key, last_diagnosed_at, diagnosis_count, next_allowed_at, created_at) VALUES ('%s','%s',5,'%s','%s')`,
		key, past, past, past,
	)
	cmd := exec.Command("sqlite3", dbPath, insertSQL)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("seed DB: %s: %v", string(out), err)
	}

	// Check: window expired → should reset and allow.
	result := CheckAndUpdate(ctx, dbPath, key, 3, 24)
	if result.Suppressed {
		t.Errorf("window expired: should not be suppressed, got: %s", result.Message)
	}
}

func TestCheckAndUpdate_UpsertIncrement(t *testing.T) {
	dbPath, cleanup := initTestDB(t)
	defer cleanup()

	ctx := context.Background()
	const key = "position_price_collapse"

	// First two calls should allow and increment.
	for i := 1; i <= 2; i++ {
		result := CheckAndUpdate(ctx, dbPath, key, 5, 24)
		if result.Suppressed {
			t.Errorf("call %d: should not be suppressed (threshold=5)", i)
		}
	}

	// Verify diagnosis_count = 2 in DB.
	cmd := exec.Command("sqlite3", dbPath,
		fmt.Sprintf("SELECT diagnosis_count FROM diagnostics_cache WHERE root_cause_key='%s'", key))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("query DB: %v", err)
	}
	got := string(out[:len(out)-1]) // trim newline
	if got != "2" {
		t.Errorf("expected diagnosis_count=2, got %s", got)
	}
}

// --- RunDedupGuard ---

func TestRunDedupGuard_Disabled(t *testing.T) {
	cfg := DedupConfig{Enabled: false, Threshold: 3, WindowHours: 24, RootCauses: map[string]bool{"news_edge_arb_failure": true}}
	baseDir := writeTestConfig(t, cfg)
	result := RunDedupGuard(context.Background(), baseDir, "news_edge_arb_failure alert")
	if result.Suppressed {
		t.Errorf("disabled guard should never suppress")
	}
}

func TestRunDedupGuard_NoMatchingRootCause(t *testing.T) {
	cfg := DedupConfig{Enabled: true, Threshold: 3, WindowHours: 24, RootCauses: map[string]bool{"news_edge_arb_failure": true}}
	baseDir := writeTestConfig(t, cfg)
	result := RunDedupGuard(context.Background(), baseDir, "unrelated task")
	if result.Suppressed {
		t.Errorf("no matching root cause should not suppress")
	}
}

func TestRunDedupGuard_EmptyBaseDir(t *testing.T) {
	result := RunDedupGuard(context.Background(), "", "news_edge_arb_failure alert")
	if result.Suppressed {
		t.Errorf("empty baseDir should not suppress")
	}
}

// --- Integration: 3 allows then suppress ---

func TestRunDedupGuard_Integration_SuppressAfterThreshold(t *testing.T) {
	cfg := DedupConfig{Enabled: true, Threshold: 3, WindowHours: 24, RootCauses: map[string]bool{"news_edge_arb_failure": true}}
	baseDir := writeTestConfig(t, cfg)

	// Initialize DB.
	dbDir := filepath.Join(baseDir, "runtime")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dbDir, "dedup_guard.db")
	sql := `CREATE TABLE diagnostics_cache (
		root_cause_key TEXT NOT NULL PRIMARY KEY,
		last_diagnosed_at TEXT NOT NULL,
		diagnosis_count INTEGER NOT NULL DEFAULT 1,
		next_allowed_at TEXT NOT NULL,
		created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
	);`
	if out, err := exec.Command("sqlite3", dbPath, sql).CombinedOutput(); err != nil {
		t.Fatalf("init DB: %s: %v", string(out), err)
	}

	ctx := context.Background()
	taskName := "hisui: news_edge_arb_failure detected"

	// First 3 dispatches: allowed.
	for i := 1; i <= 3; i++ {
		result := RunDedupGuard(ctx, baseDir, taskName)
		if result.Suppressed {
			t.Errorf("dispatch %d: should not be suppressed", i)
		}
	}

	// 4th dispatch: suppressed.
	result := RunDedupGuard(ctx, baseDir, taskName)
	if !result.Suppressed {
		t.Errorf("4th dispatch should be suppressed (threshold=3)")
	}
}
