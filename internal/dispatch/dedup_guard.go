package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tetora/internal/db"
)

const dedupConfigSubpath = "config/dedup-guard.json"
const dedupDBSubpath = "runtime/dedup_guard.db"

// DedupConfig holds the runtime dedup guard configuration.
type DedupConfig struct {
	Enabled       bool            `json:"enabled"`
	Threshold     int             `json:"threshold"`
	WindowHours   int             `json:"window_hours"`
	RootCauses    map[string]bool `json:"root_causes"`
	AlertTemplate string          `json:"alert_template"`
}

// LoadDedupConfig reads config/dedup-guard.json from baseDir each call (supports runtime reload).
func LoadDedupConfig(baseDir string) (*DedupConfig, error) {
	path := filepath.Join(baseDir, dedupConfigSubpath)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("dedup config: %w", err)
	}
	var cfg DedupConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("dedup config: %w", err)
	}
	return &cfg, nil
}

// ExtractRootCauseKey returns the first enabled root_cause key found (case-insensitive) in taskName.
// Returns "" if no match.
func ExtractRootCauseKey(cfg *DedupConfig, taskName string) string {
	lower := strings.ToLower(taskName)
	for key, enabled := range cfg.RootCauses {
		if enabled && strings.Contains(lower, strings.ToLower(key)) {
			return key
		}
	}
	return ""
}

// DedupCheckResult is the outcome of a dedup guard check.
type DedupCheckResult struct {
	Suppressed bool
	Message    string
}

// CheckAndUpdate implements the 4-step dedup logic from docs/dedup-guard-setup.md:
//
//  1. Look up root_cause_key in diagnostics_cache.
//  2. If diagnosis_count >= threshold AND NOW() < next_allowed_at → suppress.
//  3. Otherwise → allow, upsert row (diagnosis_count++, next_allowed_at = NOW() + window).
//  4. On window expiry (NOW() >= next_allowed_at) → reset diagnosis_count = 1.
//
// Fails open on DB errors to avoid blocking legitimate alerts.
func CheckAndUpdate(ctx context.Context, dbPath, rootCauseKey string, threshold, windowHours int) DedupCheckResult {
	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)
	nextAllowed := now.Add(time.Duration(windowHours) * time.Hour)
	nextAllowedStr := nextAllowed.Format(time.RFC3339)

	// Step 1: look up existing row.
	rows, err := db.QueryContext(ctx, dbPath,
		fmt.Sprintf(`SELECT diagnosis_count, next_allowed_at FROM diagnostics_cache WHERE root_cause_key='%s' LIMIT 1`,
			db.Escape(rootCauseKey)))
	if err != nil {
		// Fail open: DB may not be initialized yet.
		return DedupCheckResult{}
	}

	if len(rows) == 0 {
		// First occurrence: insert row with count=1.
		_ = db.ExecContext(ctx, dbPath, fmt.Sprintf(
			`INSERT INTO diagnostics_cache (root_cause_key, last_diagnosed_at, diagnosis_count, next_allowed_at, created_at) VALUES ('%s','%s',1,'%s','%s')`,
			db.Escape(rootCauseKey), nowStr, nextAllowedStr, nowStr,
		))
		return DedupCheckResult{}
	}

	// Parse row.
	count := intFromRow(rows[0], "diagnosis_count", 1)
	nextAllowedAt := timeFromRow(rows[0], "next_allowed_at")

	// Step 4: window expired → reset, allow.
	if !nextAllowedAt.IsZero() && !now.Before(nextAllowedAt) {
		_ = db.ExecContext(ctx, dbPath, fmt.Sprintf(
			`UPDATE diagnostics_cache SET diagnosis_count=1, last_diagnosed_at='%s', next_allowed_at='%s' WHERE root_cause_key='%s'`,
			nowStr, nextAllowedStr, db.Escape(rootCauseKey),
		))
		return DedupCheckResult{}
	}

	// Step 2: suppress if over threshold and within window.
	if count >= threshold && now.Before(nextAllowedAt) {
		msg := fmt.Sprintf("dedup guard suppressed: root_cause=%s count=%d/%d next_allowed=%s",
			rootCauseKey, count, threshold, nextAllowedAt.Format(time.RFC3339))
		return DedupCheckResult{Suppressed: true, Message: msg}
	}

	// Step 3: allow, increment.
	_ = db.ExecContext(ctx, dbPath, fmt.Sprintf(
		`UPDATE diagnostics_cache SET diagnosis_count=%d, last_diagnosed_at='%s', next_allowed_at='%s' WHERE root_cause_key='%s'`,
		count+1, nowStr, nextAllowedStr, db.Escape(rootCauseKey),
	))
	return DedupCheckResult{}
}

// RunDedupGuard loads config, extracts root_cause_key, and runs CheckAndUpdate.
// Returns (suppressed, suppressionMessage). Fails open on config/DB errors.
func RunDedupGuard(ctx context.Context, baseDir, taskName string) DedupCheckResult {
	if baseDir == "" {
		return DedupCheckResult{}
	}
	cfg, err := LoadDedupConfig(baseDir)
	if err != nil || !cfg.Enabled {
		return DedupCheckResult{}
	}
	key := ExtractRootCauseKey(cfg, taskName)
	if key == "" {
		return DedupCheckResult{}
	}
	dbPath := filepath.Join(baseDir, dedupDBSubpath)
	return CheckAndUpdate(ctx, dbPath, key, cfg.Threshold, cfg.WindowHours)
}

func intFromRow(row map[string]any, col string, def int) int {
	v, ok := row[col]
	if !ok {
		return def
	}
	switch n := v.(type) {
	case int64:
		return int(n)
	case float64:
		return int(n)
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return int(i)
		}
	}
	return def
}

func timeFromRow(row map[string]any, col string) time.Time {
	v, ok := row[col]
	if !ok {
		return time.Time{}
	}
	s, ok := v.(string)
	if !ok {
		return time.Time{}
	}
	// Try RFC3339, then SQLite CURRENT_TIMESTAMP format.
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
		return t.UTC()
	}
	return time.Time{}
}
