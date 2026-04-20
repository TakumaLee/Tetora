package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"tetora/internal/dedupguard"
	"tetora/internal/log"
)

// guardCache caches one *dedupguard.Guard per dbPath so that schemaOnce
// and the 30-second configCacheTTL survive across RunDedupGuard calls.
var guardCache sync.Map // key: dbPath string → *dedupguard.Guard

func getOrCreateGuard(configPath, dbPath string) *dedupguard.Guard {
	if v, ok := guardCache.Load(dbPath); ok {
		return v.(*dedupguard.Guard)
	}
	g := dedupguard.New(configPath, dbPath)
	actual, _ := guardCache.LoadOrStore(dbPath, g)
	return actual.(*dedupguard.Guard)
}

// dedupConfigSubpath must match internal/proactive/proactive.go so both layers
// read the same dedup-guard.json and share one set of thresholds / root_causes.
const dedupConfigSubpath = "workspace/config/dedup-guard.json"
const dedupDBSubpath = "runtime/dedup_guard.db"

// DedupConfig holds the runtime dedup guard configuration.
type DedupConfig struct {
	Enabled       bool            `json:"enabled"`
	Threshold     int             `json:"threshold"`
	WindowHours   float64         `json:"window_hours"`
	RootCauses    map[string]bool `json:"root_causes"`
	AlertTemplate string          `json:"alert_template"`
}

// LoadDedupConfig reads workspace/config/dedup-guard.json from baseDir each call (supports runtime reload).
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
// Keys are iterated in sorted order so that multi-match tasks map to a deterministic counter.
// Returns "" if no match or root_causes is not configured.
func ExtractRootCauseKey(cfg *DedupConfig, taskName string) string {
	if len(cfg.RootCauses) == 0 {
		log.Warn("dedupguard: no root_causes configured; dispatcher dedup guard inactive")
		return ""
	}
	lower := strings.ToLower(taskName)
	keys := make([]string, 0, len(cfg.RootCauses))
	for key := range cfg.RootCauses {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if cfg.RootCauses[key] && strings.Contains(lower, strings.ToLower(key)) {
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

// RunDedupGuard loads config, extracts root_cause_key, and delegates to dedupguard.Guard.
// Returns suppression result. Fails open on config/DB errors.
func RunDedupGuard(ctx context.Context, baseDir, taskName string) DedupCheckResult {
	// Context is not forwarded: dedupguard.Guard.Check uses synchronous SQLite I/O
	// that has no context-aware path. Running the guard to completion is preferable
	// to leaving dedup state stale on cancellation.
	_ = ctx
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
	configPath := filepath.Join(baseDir, dedupConfigSubpath)
	dbPath := filepath.Join(baseDir, dedupDBSubpath)
	guard := getOrCreateGuard(configPath, dbPath)
	suppressed, guardErr := guard.Check(key)
	if guardErr != nil {
		return DedupCheckResult{}
	}
	if suppressed {
		return DedupCheckResult{
			Suppressed: true,
			Message:    fmt.Sprintf("dedup guard suppressed: root_cause=%s", key),
		}
	}
	return DedupCheckResult{}
}

