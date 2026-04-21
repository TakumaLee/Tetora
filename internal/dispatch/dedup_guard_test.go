package dispatch

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// writeTestConfig writes a dedup-guard.json to dir and returns baseDir.
func writeTestConfig(t *testing.T, cfg DedupConfig) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "workspace", "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(filepath.Join(dir, "workspace", "config", "dedup-guard.json"), data, 0o644); err != nil {
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
	os.MkdirAll(filepath.Join(dir, "workspace", "config"), 0o755)
	os.WriteFile(filepath.Join(dir, "workspace", "config", "dedup-guard.json"), []byte("{invalid"), 0o644)
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

// --- getOrCreateGuard ---

func TestGetOrCreateGuard_SamePointer(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "workspace", "config", "dedup-guard.json")
	dbPath := filepath.Join(dir, "runtime", "dedup_guard.db")

	g1 := getOrCreateGuard(configPath, dbPath)
	g2 := getOrCreateGuard(configPath, dbPath)

	if g1 != g2 {
		t.Errorf("cache miss: got different *Guard pointers %p vs %p", g1, g2)
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

	// Initialize DB using guard.go's schema (4 columns, no created_at).
	dbDir := filepath.Join(baseDir, "runtime")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dbDir, "dedup_guard.db")
	sql := `CREATE TABLE diagnostics_cache (
		root_cause_key TEXT PRIMARY KEY,
		diagnosis_count INTEGER NOT NULL DEFAULT 1,
		last_diagnosed_at TEXT NOT NULL,
		next_allowed_at TEXT NOT NULL
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
