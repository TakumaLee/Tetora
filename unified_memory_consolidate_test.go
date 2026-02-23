package main

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"
)

// tempConsolidateDB creates a temporary SQLite DB with unified_memory tables.
func tempConsolidateDB(t *testing.T) string {
	t.Helper()
	f, err := os.CreateTemp("", "um-consolidate-*.db")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	path := f.Name()
	t.Cleanup(func() { os.Remove(path) })

	if err := initUnifiedMemoryDB(path); err != nil {
		t.Fatalf("init unified memory db: %v", err)
	}
	return path
}

// consolidateExecSQL is a test helper to run raw SQL against a DB.
func consolidateExecSQL(t *testing.T, dbPath, sql string) {
	t.Helper()
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("exec sql: %s: %v", string(out), err)
	}
}

// ---------------------------------------------------------------------------
// TestUmConsolidate_ClearTombstones
// ---------------------------------------------------------------------------

func TestUmConsolidate_ClearTombstones(t *testing.T) {
	dbPath := tempConsolidateDB(t)

	// Insert a tombstoned entry with old tombstoned_at (100 days ago).
	oldDate := time.Now().UTC().AddDate(0, 0, -100).Format(time.RFC3339)
	now := time.Now().UTC().Format(time.RFC3339)
	consolidateExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO unified_memory (id, namespace, scope, key, value, content_hash, version, status, tombstoned_at, ttl_days, source, created_at, updated_at) VALUES ('tomb-old', 'fact', '', 'old-key', 'old-val', 'hash1', 1, 'tombstoned', '%s', 0, 'test', '%s', '%s')`,
		oldDate, now, now,
	))

	// Insert a tombstoned entry with recent tombstoned_at (1 day ago).
	recentDate := time.Now().UTC().AddDate(0, 0, -1).Format(time.RFC3339)
	consolidateExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO unified_memory (id, namespace, scope, key, value, content_hash, version, status, tombstoned_at, ttl_days, source, created_at, updated_at) VALUES ('tomb-recent', 'fact', '', 'recent-key', 'recent-val', 'hash2', 1, 'tombstoned', '%s', 0, 'test', '%s', '%s')`,
		recentDate, now, now,
	))

	// Insert a version and link for the old tombstone.
	consolidateExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO memory_versions (memory_id, version, value, content_hash, created_at) VALUES ('tomb-old', 1, 'old-val', 'hash1', '%s')`, now,
	))
	consolidateExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO memory_links (source_id, target_id, link_type, created_at) VALUES ('tomb-old', 'tomb-recent', 'related', '%s')`, now,
	))

	// Run consolidation with maxAgeDays=30.
	n, err := umClearTombstones(dbPath, 30)
	if err != nil {
		t.Fatalf("umClearTombstones: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 tombstone cleared, got %d", n)
	}

	// Verify old tombstone is hard-deleted.
	rows, _ := queryDB(dbPath, `SELECT id FROM unified_memory WHERE id='tomb-old'`)
	if len(rows) != 0 {
		t.Error("expected tomb-old to be hard-deleted")
	}

	// Verify recent tombstone still exists.
	rows, _ = queryDB(dbPath, `SELECT id FROM unified_memory WHERE id='tomb-recent'`)
	if len(rows) != 1 {
		t.Error("expected tomb-recent to still exist")
	}

	// Verify associated version and link are deleted.
	rows, _ = queryDB(dbPath, `SELECT id FROM memory_versions WHERE memory_id='tomb-old'`)
	if len(rows) != 0 {
		t.Error("expected versions for tomb-old to be deleted")
	}
	rows, _ = queryDB(dbPath, `SELECT id FROM memory_links WHERE source_id='tomb-old' OR target_id='tomb-old'`)
	if len(rows) != 0 {
		t.Error("expected links for tomb-old to be deleted")
	}
}

// ---------------------------------------------------------------------------
// TestUmConsolidate_DedupHash
// ---------------------------------------------------------------------------

func TestUmConsolidate_DedupHash(t *testing.T) {
	dbPath := tempConsolidateDB(t)

	now := time.Now().UTC().Format(time.RFC3339)
	older := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
	oldest := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)

	// Insert 3 active entries with the same content_hash.
	// Use different namespace+scope+key to avoid UNIQUE constraint on active entries.
	consolidateExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO unified_memory (id, namespace, scope, key, value, content_hash, version, status, ttl_days, source, created_at, updated_at) VALUES ('dup-1', 'fact', '', 'key-a', 'same content', 'deadbeef', 1, 'active', 0, 'test', '%s', '%s')`,
		now, now,
	))
	consolidateExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO unified_memory (id, namespace, scope, key, value, content_hash, version, status, ttl_days, source, created_at, updated_at) VALUES ('dup-2', 'fact', '', 'key-b', 'same content', 'deadbeef', 1, 'active', 0, 'test', '%s', '%s')`,
		older, older,
	))
	consolidateExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO unified_memory (id, namespace, scope, key, value, content_hash, version, status, ttl_days, source, created_at, updated_at) VALUES ('dup-3', 'fact', '', 'key-c', 'same content', 'deadbeef', 1, 'active', 0, 'test', '%s', '%s')`,
		oldest, oldest,
	))

	n, err := umDedupByHash(dbPath)
	if err != nil {
		t.Fatalf("umDedupByHash: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 duplicates merged, got %d", n)
	}

	// Verify: dup-1 (newest) should remain active.
	rows, _ := queryDB(dbPath, `SELECT status FROM unified_memory WHERE id='dup-1'`)
	if len(rows) == 0 || jsonStr(rows[0]["status"]) != "active" {
		t.Error("expected dup-1 to remain active")
	}

	// Verify: dup-2 and dup-3 should be tombstoned.
	for _, id := range []string{"dup-2", "dup-3"} {
		rows, _ := queryDB(dbPath, fmt.Sprintf(`SELECT status FROM unified_memory WHERE id='%s'`, id))
		if len(rows) == 0 || jsonStr(rows[0]["status"]) != "tombstoned" {
			t.Errorf("expected %s to be tombstoned", id)
		}
	}

	// Verify: supersedes links exist from dup-1 to dup-2 and dup-3.
	rows, _ = queryDB(dbPath, `SELECT target_id FROM memory_links WHERE source_id='dup-1' AND link_type='supersedes'`)
	if len(rows) != 2 {
		t.Errorf("expected 2 supersedes links, got %d", len(rows))
	}
}

// ---------------------------------------------------------------------------
// TestUmConsolidate_PruneVersions
// ---------------------------------------------------------------------------

func TestUmConsolidate_PruneVersions(t *testing.T) {
	dbPath := tempConsolidateDB(t)

	now := time.Now().UTC().Format(time.RFC3339)

	// Insert a memory entry.
	consolidateExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO unified_memory (id, namespace, scope, key, value, content_hash, version, status, ttl_days, source, created_at, updated_at) VALUES ('ver-entry', 'fact', '', 'versioned', 'current', 'hashcur', 55, 'active', 0, 'test', '%s', '%s')`,
		now, now,
	))

	// Insert 55 versions (should prune 5, keeping 50).
	for i := 1; i <= 55; i++ {
		ts := time.Now().UTC().Add(time.Duration(-55+i) * time.Minute).Format(time.RFC3339)
		consolidateExecSQL(t, dbPath, fmt.Sprintf(
			`INSERT INTO memory_versions (memory_id, version, value, content_hash, created_at) VALUES ('ver-entry', %d, 'val-%d', 'hash-%d', '%s')`,
			i, i, i, ts,
		))
	}

	n, err := umPruneVersions(dbPath, 50)
	if err != nil {
		t.Fatalf("umPruneVersions: %v", err)
	}
	if n != 5 {
		t.Errorf("expected 5 versions pruned, got %d", n)
	}

	// Verify remaining count.
	rows, _ := queryDB(dbPath, `SELECT COUNT(*) as cnt FROM memory_versions WHERE memory_id='ver-entry'`)
	if len(rows) == 0 {
		t.Fatal("expected version count row")
	}
	cnt := jsonInt(rows[0]["cnt"])
	if cnt != 50 {
		t.Errorf("expected 50 remaining versions, got %d", cnt)
	}
}

// ---------------------------------------------------------------------------
// TestUmConsolidate_TTLExpiry
// ---------------------------------------------------------------------------

func TestUmConsolidate_TTLExpiry(t *testing.T) {
	dbPath := tempConsolidateDB(t)

	// Insert an entry with ttl_days=1, created 3 days ago (should expire).
	oldDate := time.Now().UTC().AddDate(0, 0, -3).Format(time.RFC3339)
	consolidateExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO unified_memory (id, namespace, scope, key, value, content_hash, version, status, ttl_days, source, created_at, updated_at) VALUES ('ttl-expired', 'fact', '', 'temp-data', 'value', 'hashttl1', 1, 'active', 1, 'test', '%s', '%s')`,
		oldDate, oldDate,
	))

	// Insert an entry with ttl_days=30, created 1 day ago (should NOT expire).
	recentDate := time.Now().UTC().AddDate(0, 0, -1).Format(time.RFC3339)
	consolidateExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO unified_memory (id, namespace, scope, key, value, content_hash, version, status, ttl_days, source, created_at, updated_at) VALUES ('ttl-ok', 'fact', '', 'long-data', 'value', 'hashttl2', 1, 'active', 30, 'test', '%s', '%s')`,
		recentDate, recentDate,
	))

	// Insert an entry with ttl_days=0 (no TTL, should NOT expire).
	consolidateExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO unified_memory (id, namespace, scope, key, value, content_hash, version, status, ttl_days, source, created_at, updated_at) VALUES ('ttl-none', 'fact', '', 'perm-data', 'value', 'hashttl3', 1, 'active', 0, 'test', '%s', '%s')`,
		oldDate, oldDate,
	))

	n, err := umExpireTTL(dbPath)
	if err != nil {
		t.Fatalf("umExpireTTL: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 TTL expired, got %d", n)
	}

	// Verify ttl-expired is tombstoned.
	rows, _ := queryDB(dbPath, `SELECT status FROM unified_memory WHERE id='ttl-expired'`)
	if len(rows) == 0 || jsonStr(rows[0]["status"]) != "tombstoned" {
		t.Error("expected ttl-expired to be tombstoned")
	}

	// Verify ttl-ok and ttl-none are still active.
	for _, id := range []string{"ttl-ok", "ttl-none"} {
		rows, _ := queryDB(dbPath, fmt.Sprintf(`SELECT status FROM unified_memory WHERE id='%s'`, id))
		if len(rows) == 0 || jsonStr(rows[0]["status"]) != "active" {
			t.Errorf("expected %s to remain active", id)
		}
	}
}

// ---------------------------------------------------------------------------
// TestUmConsolidate_Full
// ---------------------------------------------------------------------------

func TestUmConsolidate_Full(t *testing.T) {
	dbPath := tempConsolidateDB(t)

	now := time.Now().UTC().Format(time.RFC3339)
	oldDate := time.Now().UTC().AddDate(0, 0, -100).Format(time.RFC3339)
	expiredDate := time.Now().UTC().AddDate(0, 0, -10).Format(time.RFC3339)

	// 1. Old tombstone (should be cleared).
	consolidateExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO unified_memory (id, namespace, scope, key, value, content_hash, version, status, tombstoned_at, ttl_days, source, created_at, updated_at) VALUES ('full-tomb', 'fact', '', 'tomb-key', 'v', 'h1', 1, 'tombstoned', '%s', 0, 'test', '%s', '%s')`,
		oldDate, oldDate, oldDate,
	))

	// 2. Two duplicates by hash (one should be tombstoned).
	consolidateExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO unified_memory (id, namespace, scope, key, value, content_hash, version, status, ttl_days, source, created_at, updated_at) VALUES ('full-dup1', 'fact', '', 'dup-a', 'same', 'samehash', 1, 'active', 0, 'test', '%s', '%s')`,
		now, now,
	))
	older := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
	consolidateExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO unified_memory (id, namespace, scope, key, value, content_hash, version, status, ttl_days, source, created_at, updated_at) VALUES ('full-dup2', 'fact', '', 'dup-b', 'same', 'samehash', 1, 'active', 0, 'test', '%s', '%s')`,
		older, older,
	))

	// 3. TTL-expired entry.
	consolidateExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO unified_memory (id, namespace, scope, key, value, content_hash, version, status, ttl_days, source, created_at, updated_at) VALUES ('full-ttl', 'fact', '', 'ttl-key', 'v', 'httl', 1, 'active', 5, 'test', '%s', '%s')`,
		expiredDate, expiredDate,
	))

	// Run full consolidation.
	result, err := umConsolidate(dbPath, 30)
	if err != nil {
		t.Fatalf("umConsolidate: %v", err)
	}

	if result.TombstonesCleared != 1 {
		t.Errorf("TombstonesCleared: want 1, got %d", result.TombstonesCleared)
	}
	if result.DuplicatesMerged != 1 {
		t.Errorf("DuplicatesMerged: want 1, got %d", result.DuplicatesMerged)
	}
	if result.TTLExpired != 1 {
		t.Errorf("TTLExpired: want 1, got %d", result.TTLExpired)
	}
	if result.DurationMs < 0 {
		t.Error("DurationMs should be non-negative")
	}
	if result.RunAt == "" {
		t.Error("RunAt should not be empty")
	}
}
