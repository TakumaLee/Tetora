package proactive

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"tetora/internal/config"
	"tetora/internal/db"
	"tetora/internal/dispatch"
)

func newEngineWithRules(rules []config.ProactiveRule, deps Deps) *Engine {
	cfg := &config.Config{
		Proactive: config.ProactiveConfig{
			Enabled: true,
			Rules:   rules,
		},
	}
	return New(cfg, nil, nil, nil, deps)
}

// TestThresholdExplicitCooldown verifies that a threshold rule with an explicit
// cooldown fires once and then blocks via CheckCooldown.
func TestThresholdExplicitCooldown(t *testing.T) {
	metricValue := 10.0 // above threshold of 5
	actionCalled := 0

	rule := config.ProactiveRule{
		Name:     "cost-alert",
		Cooldown: "10m",
		Trigger: config.ProactiveTrigger{
			Type:   "threshold",
			Metric: "daily_cost_usd",
			Op:     ">",
			Value:  5.0,
		},
		Action: config.ProactiveAction{
			Type:    "dispatch",
			Prompt:  "check cost",
			Agent:   "ruri",
		},
		Delivery: config.ProactiveDelivery{
			Channel: "dashboard",
		},
	}

	deps := Deps{
		RunTask: func(ctx context.Context, task dispatch.Task, sem, childSem chan struct{}, agentName string) dispatch.TaskResult {
			actionCalled++
			return dispatch.TaskResult{Status: "success", Output: "ok"}
		},
		RecordHistory: func(dbPath string, task dispatch.Task, result dispatch.TaskResult, agentName, startedAt, finishedAt, outputFile string) {
		},
		FillDefaults: func(cfg *config.Config, t *dispatch.Task) {},
	}

	e := newEngineWithRules([]config.ProactiveRule{rule}, deps)

	// Patch getMetricValue by directly calling executeAction (we test cooldown mechanics, not metric resolution).
	// Verify not in cooldown before first fire.
	if e.CheckCooldown(rule.Name) {
		t.Fatal("expected no cooldown before first trigger")
	}

	// Manually call executeAction (same path as checkThresholdRules would take).
	_ = e.executeAction(context.Background(), rule)

	// Verify cooldown is now active.
	if !e.CheckCooldown(rule.Name) {
		t.Error("expected cooldown to be active after executeAction")
	}

	// Verify the stored duration matches the explicit cooldown.
	e.mu.RLock()
	entry, ok := e.cooldowns[rule.Name]
	e.mu.RUnlock()

	if !ok {
		t.Fatal("expected cooldown entry to exist")
	}

	want := 10 * time.Minute
	if entry.duration != want {
		t.Errorf("expected cooldown duration %v, got %v", want, entry.duration)
	}

	// Verify the metric check respects the cooldown (simulate second check pass).
	if e.CompareThreshold(metricValue, rule.Trigger.Op, rule.Trigger.Value) {
		// Would fire — but CheckCooldown should block it.
		if e.CheckCooldown(rule.Name) {
			// Correct: rule is blocked.
		} else {
			t.Error("rule should be in cooldown but CheckCooldown returned false")
		}
	}
}

// TestSaveReportTimestamp verifies that saveReport embeds a "triggered:" timestamp
// in both the dated file and the -latest.md file, so agents reading the file can
// determine when the alert fired without relying on filesystem mtime.
func TestSaveReportTimestamp(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{}
	cfg.WorkspaceDir = dir
	e := New(cfg, nil, nil, nil, Deps{})

	before := time.Now().Truncate(time.Second)
	e.saveReport("cost-alert", "⚠️ cost exceeded $15")
	after := time.Now().Add(time.Second)

	// latest file must exist
	latestPath := filepath.Join(dir, "reports", "cost-alert-latest.md")
	data, err := os.ReadFile(latestPath)
	if err != nil {
		t.Fatalf("latest file missing: %v", err)
	}
	content := string(data)

	// must start with "triggered: " line
	if !strings.HasPrefix(content, "triggered: ") {
		t.Fatalf("expected content to start with 'triggered: ', got: %q", content[:min(50, len(content))])
	}

	// parse and validate the embedded timestamp
	firstLine := strings.SplitN(content, "\n", 2)[0]
	tsStr := strings.TrimPrefix(firstLine, "triggered: ")
	ts, err := time.Parse(time.RFC3339, tsStr)
	if err != nil {
		t.Fatalf("timestamp parse failed: %v (got %q)", err, tsStr)
	}
	if ts.Before(before) || ts.After(after) {
		t.Errorf("timestamp %v out of expected range [%v, %v]", ts, before, after)
	}

	// original alert message must still be present
	if !strings.Contains(content, "cost exceeded $15") {
		t.Errorf("original message missing from report content")
	}

	// dated file must also contain the timestamp
	dateDir := filepath.Join(dir, "reports", time.Now().Format("2006-01-02"))
	entries, err := os.ReadDir(dateDir)
	if err != nil {
		t.Fatalf("date dir missing: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one dated file")
	}
	datedData, err := os.ReadFile(filepath.Join(dateDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("dated file read failed: %v", err)
	}
	if !strings.HasPrefix(string(datedData), "triggered: ") {
		t.Errorf("dated file missing timestamp header")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestHeartbeatCooldownSetToInterval verifies that a heartbeat rule without an
// explicit cooldown gets a cooldown equal to its Trigger.Interval after firing.
func TestHeartbeatCooldownSetToInterval(t *testing.T) {
	rule := config.ProactiveRule{
		Name: "heartbeat-check",
		// No Cooldown field — engine should derive it from Trigger.Interval.
		Trigger: config.ProactiveTrigger{
			Type:     "heartbeat",
			Interval: "5m",
		},
		Action: config.ProactiveAction{
			Type:   "notify",
			Message: "heartbeat ping",
		},
		Delivery: config.ProactiveDelivery{
			Channel: "dashboard",
		},
	}

	e := newEngineWithRules([]config.ProactiveRule{rule}, Deps{})

	if e.CheckCooldown(rule.Name) {
		t.Fatal("expected no cooldown before first trigger")
	}

	_ = e.executeAction(context.Background(), rule)

	// Cooldown should now be set.
	if !e.CheckCooldown(rule.Name) {
		t.Error("expected cooldown to be active after heartbeat executeAction")
	}

	e.mu.RLock()
	entry, ok := e.cooldowns[rule.Name]
	e.mu.RUnlock()

	if !ok {
		t.Fatal("expected cooldown entry to exist after heartbeat trigger")
	}

	want := 5 * time.Minute
	if entry.duration != want {
		t.Errorf("expected heartbeat cooldown duration %v (from Interval), got %v", want, entry.duration)
	}
}

func TestComputeMedian(t *testing.T) {
	cases := []struct {
		name string
		vals []float64
		want float64
	}{
		{"empty", []float64{}, 0},
		{"single", []float64{3.0}, 3.0},
		{"odd", []float64{1, 5, 3}, 3.0},
		{"even", []float64{1, 2, 3, 4}, 2.5},
		{"unsorted input", []float64{9, 1, 5}, 5.0},
	}
	for _, tc := range cases {
		got := computeMedian(tc.vals)
		if got != tc.want {
			t.Errorf("computeMedian(%v) = %v, want %v", tc.vals, got, tc.want)
		}
	}
}

func TestGetDynamicThreshold_UnknownFormula(t *testing.T) {
	e := newEngineWithRules(nil, Deps{})
	_, err := e.getDynamicThreshold("unknown_formula")
	if err == nil {
		t.Error("expected error for unknown formula, got nil")
	}
}

func TestGetDynamicThreshold_NoHistoryDB(t *testing.T) {
	// Engine with no historyDB: median_30d_x1.5 should return error, not panic.
	e := newEngineWithRules(nil, Deps{})
	_, err := e.getDynamicThreshold("median_30d_x1.5")
	if err == nil {
		t.Error("expected error when historyDB not configured, got nil")
	}
}

// TestGet30DayDailyCosts_ColumnByName guards against reintroducing the
// positional `vals[1]` access bug: Go map iteration order is non-deterministic,
// so reading the second column by position flipped between day (a string like
// "2026-04-18") and cost (a float) across runs. The fix uses named column
// access (AS total_cost) and asserts the returned floats match the inserted
// cost_usd values exactly.
func TestGet30DayDailyCosts_ColumnByName(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test.db")
	schema := `CREATE TABLE IF NOT EXISTS job_runs (
	  id INTEGER PRIMARY KEY AUTOINCREMENT,
	  job_id TEXT NOT NULL DEFAULT '',
	  name TEXT NOT NULL DEFAULT '',
	  source TEXT NOT NULL DEFAULT '',
	  started_at TEXT NOT NULL,
	  finished_at TEXT NOT NULL DEFAULT '',
	  status TEXT NOT NULL DEFAULT '',
	  cost_usd REAL DEFAULT 0
	);`
	if err := db.Exec(dbPath, schema); err != nil {
		t.Fatalf("schema: %v", err)
	}

	// Insert three days of costs. Use distinct non-integer values so the
	// positional-access bug (returning a date-string via int64 coercion, or
	// zero via failed type-switch) would produce visibly wrong output.
	insert := func(daysAgo int, cost float64) {
		d := time.Now().AddDate(0, 0, -daysAgo).UTC().Format(time.RFC3339)
		sql := fmt.Sprintf(
			`INSERT INTO job_runs (job_id, name, source, started_at, cost_usd) VALUES ('j','n','s','%s',%f)`,
			d, cost,
		)
		if err := db.Exec(dbPath, sql); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	// Two rows same day → SUM; plus two other days.
	insert(2, 1.25)
	insert(2, 0.75) // same day as above → sum = 2.00
	insert(5, 3.50)
	insert(10, 0.10)

	e := newEngineWithRules(nil, Deps{})
	e.cfg.HistoryDB = dbPath

	costs, err := e.get30DayDailyCosts()
	if err != nil {
		t.Fatalf("get30DayDailyCosts: %v", err)
	}

	// Expect 3 distinct days; order is SQL-ordered by day so we sort a copy
	// before comparing to keep the assertion insertion-order-independent.
	if len(costs) != 3 {
		t.Fatalf("expected 3 daily cost rows, got %d (costs=%v)", len(costs), costs)
	}
	sort.Float64s(costs)
	want := []float64{0.10, 2.00, 3.50}
	for i, w := range want {
		if costs[i] < w-1e-9 || costs[i] > w+1e-9 {
			t.Errorf("costs[%d] = %v, want %v (all=%v)", i, costs[i], w, costs)
		}
	}
}
