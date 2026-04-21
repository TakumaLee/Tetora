// Package dedupguard suppresses repeated alerts that share the same root cause.
//
// On each Check call the guard queries diagnostics_cache by root_cause_key and
// applies the following rules:
//
//  1. count >= threshold AND now < next_allowed_at  → suppressed (return true)
//  2. no row, or window expired (now >= next_allowed_at) → allow, reset count to 1
//  3. count < threshold                               → allow, increment count
//
// next_allowed_at is always recalculated as now + window_hours on upsert so the
// window slides on each allowed diagnosis.
package dedupguard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"tetora/internal/db"
	"tetora/internal/log"
)

const (
	defaultThreshold   = 3
	defaultWindowHours = 24.0
	configCacheTTL     = 30 * time.Second
	schemaSQL          = `CREATE TABLE IF NOT EXISTS diagnostics_cache (
		root_cause_key TEXT PRIMARY KEY,
		diagnosis_count INTEGER NOT NULL DEFAULT 1,
		last_diagnosed_at TEXT NOT NULL,
		next_allowed_at TEXT NOT NULL
	)`
)

// Config holds the dedup guard configuration loaded from dedup-guard.json.
type Config struct {
	Threshold   int     `json:"threshold"`
	WindowHours float64 `json:"window_hours"`
	Enabled     bool    `json:"enabled"`
	// DBPath overrides the default DB location when non-empty.
	DBPath string `json:"db_path,omitempty"`
}

func (c Config) window() time.Duration {
	if c.WindowHours <= 0 {
		return time.Duration(defaultWindowHours * float64(time.Hour))
	}
	return time.Duration(c.WindowHours * float64(time.Hour))
}

// Guard checks whether an alert should be suppressed based on diagnosis count.
type Guard struct {
	configPath string
	defaultDB  string // resolved DB path used when config.DBPath is empty

	mu          sync.Mutex
	cachedCfg   Config
	cacheLoaded time.Time
	schemaOnce  sync.Once
	schemaErr   error
}

// New creates a Guard.
//   - configPath: path to dedup-guard.json (may contain ~)
//   - defaultDB:  path to dedup_guard.db (may contain ~); used when config omits db_path
func New(configPath, defaultDB string) *Guard {
	return &Guard{
		configPath: expandHome(configPath),
		defaultDB:  expandHome(defaultDB),
	}
}

// Check returns true if the alert for rootCauseKey should be suppressed.
// On error the guard fails open (returns false, logs the error).
func (g *Guard) Check(rootCauseKey string) (suppressed bool, err error) {
	cfg := g.config()
	if !cfg.Enabled {
		return false, nil
	}

	if initErr := g.ensureSchema(cfg); initErr != nil {
		log.Warn("dedupguard: schema init failed, failing open", "error", initErr)
		return false, nil
	}

	dbPath := g.resolveDB(cfg)
	now := time.Now().UTC()

	row, queryErr := g.queryRow(dbPath, rootCauseKey)
	if queryErr != nil {
		log.Warn("dedupguard: query failed, failing open", "key", rootCauseKey, "error", queryErr)
		return false, nil
	}

	threshold := cfg.Threshold
	if threshold <= 0 {
		threshold = defaultThreshold
	}

	if row != nil {
		// Within active window and over threshold → suppress.
		if row.DiagnosisCount >= threshold && now.Before(row.NextAllowedAt) {
			log.Info("dedupguard: suppressing repeated diagnosis",
				"key", rootCauseKey,
				"count", row.DiagnosisCount,
				"next_allowed_at", row.NextAllowedAt.Format(time.RFC3339Nano))
			return true, nil
		}

		// Window expired → reset count.
		if !now.Before(row.NextAllowedAt) {
			return false, g.upsert(dbPath, rootCauseKey, 1, now, now.Add(cfg.window()))
		}

		// Under threshold → increment.
		return false, g.upsert(dbPath, rootCauseKey, row.DiagnosisCount+1, now, now.Add(cfg.window()))
	}

	// No existing row → first diagnosis.
	return false, g.upsert(dbPath, rootCauseKey, 1, now, now.Add(cfg.window()))
}

// cacheEntry is a parsed row from diagnostics_cache.
type cacheEntry struct {
	DiagnosisCount int
	NextAllowedAt  time.Time
}

// queryRow returns nil when no row exists for the key.
func (g *Guard) queryRow(dbPath, key string) (*cacheEntry, error) {
	rows, err := db.QueryArgs(dbPath,
		`SELECT diagnosis_count, next_allowed_at FROM diagnostics_cache WHERE root_cause_key = ?`,
		key)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}

	row := rows[0]
	count, _ := parseIntField(row["diagnosis_count"])
	nextAt, _ := parseTimeField(row["next_allowed_at"])
	return &cacheEntry{DiagnosisCount: count, NextAllowedAt: nextAt}, nil
}

// upsert inserts or replaces a diagnostics_cache row.
func (g *Guard) upsert(dbPath, key string, count int, lastAt, nextAt time.Time) error {
	return db.ExecArgs(dbPath,
		`INSERT INTO diagnostics_cache (root_cause_key, diagnosis_count, last_diagnosed_at, next_allowed_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(root_cause_key) DO UPDATE SET
		   diagnosis_count   = excluded.diagnosis_count,
		   last_diagnosed_at = excluded.last_diagnosed_at,
		   next_allowed_at   = excluded.next_allowed_at`,
		key,
		count,
		lastAt.Format(time.RFC3339Nano),
		nextAt.Format(time.RFC3339Nano),
	)
}

// ensureSchema creates the diagnostics_cache table if it does not exist.
// The result is cached after the first call (idempotent for the process lifetime).
func (g *Guard) ensureSchema(cfg Config) error {
	g.schemaOnce.Do(func() {
		g.schemaErr = db.Exec(g.resolveDB(cfg), schemaSQL)
	})
	return g.schemaErr
}

// config returns a cached Config, refreshing from disk when the TTL has elapsed.
func (g *Guard) config() Config {
	g.mu.Lock()
	defer g.mu.Unlock()

	if time.Since(g.cacheLoaded) < configCacheTTL {
		return g.cachedCfg
	}

	cfg, err := loadConfigFile(g.configPath)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Warn("dedupguard: config load failed, using defaults", "path", g.configPath, "error", err)
		}
		cfg = Config{Threshold: defaultThreshold, WindowHours: defaultWindowHours, Enabled: true}
	}
	g.cachedCfg = cfg
	g.cacheLoaded = time.Now()
	return cfg
}

// resolveDB returns the active DB path (config override or defaultDB).
func (g *Guard) resolveDB(cfg Config) string {
	if cfg.DBPath != "" {
		return expandHome(cfg.DBPath)
	}
	return g.defaultDB
}

// loadConfigFile reads and parses the JSON config file at path.
func loadConfigFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse dedup-guard config: %w", err)
	}
	return cfg, nil
}

// expandHome replaces a leading ~ with the user's home directory.
func expandHome(path string) string {
	if len(path) == 0 || path[0] != '~' {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[1:])
}

func parseIntField(v any) (int, error) {
	switch x := v.(type) {
	case float64:
		return int(x), nil
	case string:
		n, err := strconv.Atoi(x)
		return n, err
	}
	return 0, fmt.Errorf("unexpected type %T", v)
}

func parseTimeField(v any) (time.Time, error) {
	s, ok := v.(string)
	if !ok {
		return time.Time{}, fmt.Errorf("unexpected type %T for time", v)
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		// Fallback: rows stored before RFC3339Nano migration use second precision.
		t, err = time.Parse(time.RFC3339, s)
	}
	return t, err
}
