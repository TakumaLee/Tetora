package main

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

// --- P23.0: Unified Memory Consolidation Engine ---

// UMConsolidationResult holds stats from a consolidation run.
type UMConsolidationResult struct {
	TombstonesCleared int    `json:"tombstonesCleared"`
	DuplicatesMerged  int    `json:"duplicatesMerged"`
	VersionsPruned    int    `json:"versionsPruned"`
	TTLExpired        int    `json:"ttlExpired"`
	RunAt             string `json:"runAt"`
	DurationMs        int64  `json:"durationMs"`
}

// umConsolidate runs a single consolidation pass:
//  1. Clear old tombstones (tombstoned_at > maxAgeDays ago) -- hard delete
//  2. Content hash dedup -- find active entries with same content_hash, keep newest, tombstone rest
//  3. Version pruning -- for each memory_id, keep only newest 50 versions, delete the rest
//  4. TTL expiry -- tombstone entries where ttl_days > 0 AND created_at + ttl_days < now
func umConsolidate(dbPath string, maxAgeDays int) (*UMConsolidationResult, error) {
	start := time.Now()
	result := &UMConsolidationResult{
		RunAt: start.UTC().Format(time.RFC3339),
	}

	// Step 1: Clear old tombstones.
	n, err := umClearTombstones(dbPath, maxAgeDays)
	if err != nil {
		logWarn("um_consolidate: clear tombstones failed", "error", err)
	} else {
		result.TombstonesCleared = n
	}

	// Step 2: Content hash dedup.
	n, err = umDedupByHash(dbPath)
	if err != nil {
		logWarn("um_consolidate: dedup failed", "error", err)
	} else {
		result.DuplicatesMerged = n
	}

	// Step 3: Version pruning.
	n, err = umPruneVersions(dbPath, 50)
	if err != nil {
		logWarn("um_consolidate: prune versions failed", "error", err)
	} else {
		result.VersionsPruned = n
	}

	// Step 4: TTL expiry.
	n, err = umExpireTTL(dbPath)
	if err != nil {
		logWarn("um_consolidate: ttl expiry failed", "error", err)
	} else {
		result.TTLExpired = n
	}

	result.DurationMs = time.Since(start).Milliseconds()
	logInfo("um_consolidate: pass complete",
		"tombstonesCleared", result.TombstonesCleared,
		"duplicatesMerged", result.DuplicatesMerged,
		"versionsPruned", result.VersionsPruned,
		"ttlExpired", result.TTLExpired,
		"durationMs", result.DurationMs,
	)
	return result, nil
}

// umClearTombstones hard-deletes entries that were tombstoned more than maxAgeDays ago.
// Also removes associated rows from memory_versions and memory_links.
func umClearTombstones(dbPath string, maxAgeDays int) (int, error) {
	cutoff := time.Now().UTC().AddDate(0, 0, -maxAgeDays).Format(time.RFC3339)
	sql := fmt.Sprintf(
		`SELECT id FROM unified_memory WHERE status='tombstoned' AND tombstoned_at < '%s'`,
		escapeSQLite(cutoff),
	)
	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return 0, fmt.Errorf("query tombstones: %w", err)
	}

	cleared := 0
	for _, row := range rows {
		id := jsonStr(row["id"])
		if id == "" {
			continue
		}
		eid := escapeSQLite(id)

		// Delete from memory_versions, memory_links, then unified_memory.
		delSQL := fmt.Sprintf(
			`DELETE FROM memory_versions WHERE memory_id='%s';`+
				`DELETE FROM memory_links WHERE source_id='%s' OR target_id='%s';`+
				`DELETE FROM unified_memory WHERE id='%s';`,
			eid, eid, eid, eid,
		)
		cmd := exec.Command("sqlite3", dbPath, delSQL)
		if out, err := cmd.CombinedOutput(); err != nil {
			logWarn("um_consolidate: delete tombstone failed", "id", id, "error", fmt.Sprintf("%s: %v", string(out), err))
			continue
		}
		cleared++
	}
	return cleared, nil
}

// umDedupByHash finds active entries that share the same content_hash,
// keeps the newest, tombstones the rest, and creates 'supersedes' links.
func umDedupByHash(dbPath string) (int, error) {
	sql := `SELECT content_hash, COUNT(*) as cnt FROM unified_memory WHERE status='active' AND content_hash != '' GROUP BY content_hash HAVING cnt > 1`
	groups, err := queryDB(dbPath, sql)
	if err != nil {
		return 0, fmt.Errorf("query dedup groups: %w", err)
	}

	merged := 0
	now := time.Now().UTC().Format(time.RFC3339)
	for _, g := range groups {
		hash := jsonStr(g["content_hash"])
		if hash == "" {
			continue
		}

		// Get all entries with this hash, newest first.
		dupSQL := fmt.Sprintf(
			`SELECT id, namespace, scope, key, updated_at FROM unified_memory WHERE content_hash='%s' AND status='active' ORDER BY updated_at DESC`,
			escapeSQLite(hash),
		)
		dups, err := queryDB(dbPath, dupSQL)
		if err != nil || len(dups) < 2 {
			continue
		}

		// Keep the first (newest), tombstone the rest.
		keepID := jsonStr(dups[0]["id"])
		for _, dup := range dups[1:] {
			dupID := jsonStr(dup["id"])
			if dupID == "" {
				continue
			}

			// Tombstone the duplicate.
			tombSQL := fmt.Sprintf(
				`UPDATE unified_memory SET status='tombstoned', tombstoned_at='%s' WHERE id='%s'`,
				now, escapeSQLite(dupID),
			)
			cmd := exec.Command("sqlite3", dbPath, tombSQL)
			if out, err := cmd.CombinedOutput(); err != nil {
				logWarn("um_consolidate: tombstone dup failed", "id", dupID, "error", fmt.Sprintf("%s: %v", string(out), err))
				continue
			}

			// Create 'supersedes' link from keeper to duplicate.
			linkSQL := fmt.Sprintf(
				`INSERT OR IGNORE INTO memory_links (source_id, target_id, link_type, created_at) VALUES ('%s','%s','supersedes','%s')`,
				escapeSQLite(keepID), escapeSQLite(dupID), now,
			)
			cmd = exec.Command("sqlite3", dbPath, linkSQL)
			if out, err := cmd.CombinedOutput(); err != nil {
				logWarn("um_consolidate: create link failed", "source", keepID, "target", dupID, "error", fmt.Sprintf("%s: %v", string(out), err))
			}
			merged++
		}
	}
	return merged, nil
}

// umPruneVersions removes old versions beyond the maxVersions limit per memory_id.
func umPruneVersions(dbPath string, maxVersions int) (int, error) {
	sql := fmt.Sprintf(
		`SELECT memory_id, COUNT(*) as cnt FROM memory_versions GROUP BY memory_id HAVING cnt > %d`,
		maxVersions,
	)
	groups, err := queryDB(dbPath, sql)
	if err != nil {
		return 0, fmt.Errorf("query version counts: %w", err)
	}

	pruned := 0
	for _, g := range groups {
		memID := jsonStr(g["memory_id"])
		if memID == "" {
			continue
		}

		// Delete all but the newest maxVersions.
		delSQL := fmt.Sprintf(
			`DELETE FROM memory_versions WHERE memory_id='%s' AND id NOT IN (SELECT id FROM memory_versions WHERE memory_id='%s' ORDER BY created_at DESC LIMIT %d)`,
			escapeSQLite(memID), escapeSQLite(memID), maxVersions,
		)
		cmd := exec.Command("sqlite3", dbPath, delSQL)
		if out, err := cmd.CombinedOutput(); err != nil {
			logWarn("um_consolidate: prune versions failed", "memoryId", memID, "error", fmt.Sprintf("%s: %v", string(out), err))
			continue
		}

		cnt := jsonInt(g["cnt"])
		pruned += cnt - maxVersions
	}
	return pruned, nil
}

// umExpireTTL tombstones entries where ttl_days > 0 and the entry has expired.
func umExpireTTL(dbPath string) (int, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	sql := fmt.Sprintf(
		`SELECT id FROM unified_memory WHERE status='active' AND ttl_days > 0 AND datetime(created_at, '+' || ttl_days || ' days') < '%s'`,
		escapeSQLite(now),
	)
	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return 0, fmt.Errorf("query ttl expired: %w", err)
	}

	expired := 0
	for _, row := range rows {
		id := jsonStr(row["id"])
		if id == "" {
			continue
		}
		tombSQL := fmt.Sprintf(
			`UPDATE unified_memory SET status='tombstoned', tombstoned_at='%s' WHERE id='%s'`,
			now, escapeSQLite(id),
		)
		cmd := exec.Command("sqlite3", dbPath, tombSQL)
		if out, err := cmd.CombinedOutput(); err != nil {
			logWarn("um_consolidate: expire ttl failed", "id", id, "error", fmt.Sprintf("%s: %v", string(out), err))
			continue
		}
		expired++
	}
	return expired, nil
}

// startConsolidationLoop starts a background goroutine that runs consolidation periodically.
func startConsolidationLoop(ctx context.Context, dbPath string, intervalHours int, maxAgeDays int) {
	if intervalHours <= 0 {
		intervalHours = 24
	}
	if maxAgeDays <= 0 {
		maxAgeDays = 90
	}

	ticker := time.NewTicker(time.Duration(intervalHours) * time.Hour)
	go func() {
		defer ticker.Stop()
		logInfo("um_consolidate: loop started", "intervalHours", intervalHours, "maxAgeDays", maxAgeDays)
		for {
			select {
			case <-ctx.Done():
				logInfo("um_consolidate: loop stopped")
				return
			case <-ticker.C:
				result, err := umConsolidate(dbPath, maxAgeDays)
				if err != nil {
					logError("um_consolidate: pass error", "error", err)
				} else {
					logInfo("um_consolidate: scheduled pass done",
						"tombstonesCleared", result.TombstonesCleared,
						"duplicatesMerged", result.DuplicatesMerged,
						"versionsPruned", result.VersionsPruned,
						"ttlExpired", result.TTLExpired,
						"durationMs", result.DurationMs,
					)
				}
			}
		}
	}()
}
