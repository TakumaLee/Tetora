package dedupguard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// testGuard creates a Guard backed by a temporary DB and config file.
// threshold and windowDuration control the guard's behaviour.
func testGuard(t *testing.T, threshold int, window time.Duration) *Guard {
	t.Helper()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "dedup_guard.db")
	cfgPath := filepath.Join(dir, "dedup-guard.json")

	windowHours := window.Hours()
	cfg := Config{
		Threshold:   threshold,
		WindowHours: windowHours,
		Enabled:     true,
		DBPath:      dbPath,
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(cfgPath, data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	g := New(cfgPath, dbPath)
	// Force immediate config load so tests use the written file.
	g.cacheLoaded = time.Time{}
	return g
}

// TestThresholdTrigger verifies that an alert is suppressed once diagnosis_count
// reaches the configured threshold within the window.
func TestThresholdTrigger(t *testing.T) {
	const threshold = 3
	g := testGuard(t, threshold, 24*time.Hour)

	key := "test-root-cause"

	// First (threshold-1) calls must be allowed.
	for i := 0; i < threshold; i++ {
		suppressed, err := g.Check(key)
		if err != nil {
			t.Fatalf("call %d: unexpected error: %v", i+1, err)
		}
		if suppressed {
			t.Fatalf("call %d: expected allowed, got suppressed", i+1)
		}
	}

	// threshold-th call: count is now == threshold, next call should suppress.
	// But the threshold-th call itself incremented to threshold, so the NEXT call suppresses.
	suppressed, err := g.Check(key)
	if err != nil {
		t.Fatalf("call %d: unexpected error: %v", threshold+1, err)
	}
	if !suppressed {
		t.Fatalf("call %d: expected suppressed, got allowed (threshold=%d)", threshold+1, threshold)
	}
}

// TestWindowExpiry verifies that once the window expires the guard resets the
// count to 1 and allows the alert again.
func TestWindowExpiry(t *testing.T) {
	const threshold = 2
	window := 200 * time.Millisecond
	g := testGuard(t, threshold, window)

	key := "expire-root-cause"

	// Exhaust threshold.
	for i := 0; i < threshold; i++ {
		suppressed, err := g.Check(key)
		if err != nil {
			t.Fatalf("setup call %d: %v", i+1, err)
		}
		if suppressed {
			t.Fatalf("setup call %d: unexpected suppression", i+1)
		}
	}

	// Now suppressed within window.
	suppressed, err := g.Check(key)
	if err != nil {
		t.Fatalf("suppressed check: %v", err)
	}
	if !suppressed {
		t.Fatal("expected suppressed before window expiry")
	}

	// Wait for window to expire.
	time.Sleep(window + 50*time.Millisecond)

	// After expiry the guard must allow and reset count to 1.
	suppressed, err = g.Check(key)
	if err != nil {
		t.Fatalf("post-expiry check: %v", err)
	}
	if suppressed {
		t.Fatal("expected allowed after window expiry")
	}

	// Row should now have count=1; one more call is allowed before suppression.
	suppressed, err = g.Check(key)
	if err != nil {
		t.Fatalf("post-reset second check: %v", err)
	}
	if suppressed {
		t.Fatal("expected second call after reset to be allowed (threshold=2)")
	}

	// Third call (count reaches threshold again) → suppressed.
	suppressed, err = g.Check(key)
	if err != nil {
		t.Fatalf("post-reset third check: %v", err)
	}
	if !suppressed {
		t.Fatal("expected third call after reset to be suppressed")
	}
}

// TestUpsertCountCorrectness verifies that diagnosis_count increments correctly
// up to threshold-1 and that the row is created on the first call.
func TestUpsertCountCorrectness(t *testing.T) {
	const threshold = 5
	g := testGuard(t, threshold, 24*time.Hour)

	key := "count-root-cause"
	cfg := g.config()
	dbPath := g.resolveDB(cfg)

	if err := g.ensureSchema(cfg); err != nil {
		t.Fatalf("ensureSchema: %v", err)
	}

	for i := 1; i <= threshold-1; i++ {
		suppressed, err := g.Check(key)
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if suppressed {
			t.Fatalf("call %d: unexpected suppression at count %d (threshold=%d)", i, i, threshold)
		}

		// Verify stored count.
		row, err := g.queryRow(dbPath, key)
		if err != nil {
			t.Fatalf("call %d queryRow: %v", i, err)
		}
		if row == nil {
			t.Fatalf("call %d: row not found after upsert", i)
		}
		if row.DiagnosisCount != i {
			t.Fatalf("call %d: expected count=%d, got %d", i, i, row.DiagnosisCount)
		}
	}
}

// TestDisabledGuard verifies that Check always returns false (allow) when
// the guard is disabled in config.
func TestDisabledGuard(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "dedup_guard.db")
	cfgPath := filepath.Join(dir, "dedup-guard.json")

	cfg := Config{Threshold: 1, WindowHours: 24, Enabled: false, DBPath: dbPath}
	data, _ := json.Marshal(cfg)
	_ = os.WriteFile(cfgPath, data, 0o600)

	g := New(cfgPath, dbPath)
	g.cacheLoaded = time.Time{}

	for i := 0; i < 5; i++ {
		suppressed, err := g.Check("disabled-key")
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if suppressed {
			t.Fatalf("call %d: guard disabled but returned suppressed", i)
		}
	}
}

// TestIntegration_SameRootCause3ThenSuppressed is the integration acceptance
// test: dispatching the same root_cause 3 times must result in the 4th being
// suppressed (threshold=3).
func TestIntegration_SameRootCause3ThenSuppressed(t *testing.T) {
	const threshold = 3
	g := testGuard(t, threshold, 24*time.Hour)

	key := "repeated-diagnosis"

	for dispatch := 1; dispatch <= threshold; dispatch++ {
		suppressed, err := g.Check(key)
		if err != nil {
			t.Fatalf("dispatch %d: %v", dispatch, err)
		}
		if suppressed {
			t.Fatalf("dispatch %d/%d: expected allowed, got suppressed", dispatch, threshold)
		}
	}

	// 4th dispatch must be suppressed.
	suppressed, err := g.Check(key)
	if err != nil {
		t.Fatalf("dispatch 4: %v", err)
	}
	if !suppressed {
		t.Fatal("dispatch 4: expected suppressed (same root cause dispatched 3 times)")
	}
}

// TestMissingConfigFallsBackToDefaults verifies that a non-existent config file
// causes the guard to use built-in defaults (enabled, threshold=3) rather than
// erroring out.
func TestMissingConfigFallsBackToDefaults(t *testing.T) {
	dir := t.TempDir()
	g := New(
		filepath.Join(dir, "nonexistent-dedup.json"),
		filepath.Join(dir, "dedup.db"),
	)
	g.cacheLoaded = time.Time{}

	cfg := g.config()
	if !cfg.Enabled {
		t.Fatal("expected enabled=true from defaults")
	}
	if cfg.Threshold != defaultThreshold {
		t.Fatalf("expected default threshold=%d, got %d", defaultThreshold, cfg.Threshold)
	}
}
