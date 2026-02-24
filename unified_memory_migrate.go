package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// --- P23.0: Unified Memory Migration Engine ---

// UMMigrationStats tracks results from a full migration run.
type UMMigrationStats struct {
	AgentMemory int `json:"agentMemory"`
	Embeddings  int `json:"embeddings"`
	Reflections int `json:"reflections"`
	Notes       int `json:"notes"`
	Skipped     int `json:"skipped"`
	Errors      int `json:"errors"`
}

// umMigrateAll runs migration from all legacy stores to unified_memory.
// Only migrates records that haven't been migrated yet (checks um_data_migration_log).
func umMigrateAll(dbPath string, vaultPath string) (*UMMigrationStats, error) {
	stats := &UMMigrationStats{}

	// Migrate agent_memory.
	n, err := umMigrateAgentMemory(dbPath)
	if err != nil {
		logWarn("um_migrate: agent_memory failed", "error", err)
		stats.Errors++
	} else {
		stats.AgentMemory = n
	}

	// Migrate embeddings.
	n, err = umMigrateEmbeddings(dbPath)
	if err != nil {
		logWarn("um_migrate: embeddings failed", "error", err)
		stats.Errors++
	} else {
		stats.Embeddings = n
	}

	// Migrate reflections.
	n, err = umMigrateReflections(dbPath)
	if err != nil {
		logWarn("um_migrate: reflections failed", "error", err)
		stats.Errors++
	} else {
		stats.Reflections = n
	}

	// Migrate notes from vault.
	if vaultPath != "" {
		n, err = umMigrateNotes(dbPath, vaultPath)
		if err != nil {
			logWarn("um_migrate: notes failed", "error", err)
			stats.Errors++
		} else {
			stats.Notes = n
		}
	}

	logInfo("um_migrate: migration complete",
		"agentMemory", stats.AgentMemory,
		"embeddings", stats.Embeddings,
		"reflections", stats.Reflections,
		"notes", stats.Notes,
		"skipped", stats.Skipped,
		"errors", stats.Errors,
	)
	return stats, nil
}

// umMigrateAgentMemory migrates from the agent_memory table.
// Maps: role -> scope, key -> key, value -> value.
// Namespace is inferred: keys containing "prefer", "setting", or "config" become
// UMNSPreference; everything else becomes UMNSFact.
func umMigrateAgentMemory(dbPath string) (int, error) {
	rows, err := queryDB(dbPath, `SELECT id, role, key, value FROM agent_memory`)
	if err != nil {
		return 0, fmt.Errorf("query agent_memory: %w", err)
	}

	migrated := 0
	for _, row := range rows {
		srcID := fmt.Sprintf("%v", row["id"])
		if umIsMigrated(dbPath, "agent_memory", srcID) {
			continue
		}

		key := jsonStr(row["key"])
		ns := UMNSFact
		lk := strings.ToLower(key)
		if strings.Contains(lk, "prefer") || strings.Contains(lk, "setting") || strings.Contains(lk, "config") {
			ns = UMNSPreference
		}

		entry := UnifiedMemoryEntry{
			Namespace: ns,
			Scope:     jsonStr(row["role"]),
			Key:       key,
			Value:     jsonStr(row["value"]),
			Source:    "migration:agent_memory",
			SourceRef: srcID,
		}

		memID, _, err := umStore(dbPath, entry)
		if err != nil {
			logWarn("um_migrate: store agent_memory failed", "srcID", srcID, "error", err)
			continue
		}
		if err := umLogMigration(dbPath, "agent_memory", srcID, memID); err != nil {
			logWarn("um_migrate: log agent_memory failed", "srcID", srcID, "error", err)
			continue
		}
		migrated++
	}
	return migrated, nil
}

// umMigrateEmbeddings migrates from the embeddings table.
// Maps: source -> source field, source_id -> key prefix ("emb:source:source_id"),
// content -> value, namespace = UMNSFact.
func umMigrateEmbeddings(dbPath string) (int, error) {
	rows, err := queryDB(dbPath, `SELECT id, source, source_id, content FROM embeddings`)
	if err != nil {
		return 0, fmt.Errorf("query embeddings: %w", err)
	}

	migrated := 0
	for _, row := range rows {
		srcID := fmt.Sprintf("%v", row["id"])
		if umIsMigrated(dbPath, "embeddings", srcID) {
			continue
		}

		source := jsonStr(row["source"])
		sourceID := jsonStr(row["source_id"])
		content := jsonStr(row["content"])

		entry := UnifiedMemoryEntry{
			Namespace: UMNSFact,
			Key:       "emb:" + source + ":" + sourceID,
			Value:     content,
			Source:    "migration:embeddings",
			SourceRef: srcID,
		}

		memID, _, err := umStore(dbPath, entry)
		if err != nil {
			logWarn("um_migrate: store embedding failed", "srcID", srcID, "error", err)
			continue
		}
		if err := umLogMigration(dbPath, "embeddings", srcID, memID); err != nil {
			logWarn("um_migrate: log embedding failed", "srcID", srcID, "error", err)
			continue
		}
		migrated++
	}
	return migrated, nil
}

// umMigrateReflections migrates from the reflections table.
// Maps: task_id+role -> key, feedback+improvement -> value, namespace = UMNSReflection.
func umMigrateReflections(dbPath string) (int, error) {
	rows, err := queryDB(dbPath, `SELECT id, task_id, role, feedback, improvement FROM reflections`)
	if err != nil {
		return 0, fmt.Errorf("query reflections: %w", err)
	}

	migrated := 0
	for _, row := range rows {
		srcID := fmt.Sprintf("%v", row["id"])
		if umIsMigrated(dbPath, "reflections", srcID) {
			continue
		}

		taskID := jsonStr(row["task_id"])
		role := jsonStr(row["role"])
		feedback := jsonStr(row["feedback"])
		improvement := jsonStr(row["improvement"])

		entry := UnifiedMemoryEntry{
			Namespace: UMNSReflection,
			Scope:     role,
			Key:       "reflect:" + taskID,
			Value:     feedback + "\n---\n" + improvement,
			Source:    "migration:reflections",
			SourceRef: srcID,
		}

		memID, _, err := umStore(dbPath, entry)
		if err != nil {
			logWarn("um_migrate: store reflection failed", "srcID", srcID, "error", err)
			continue
		}
		if err := umLogMigration(dbPath, "reflections", srcID, memID); err != nil {
			logWarn("um_migrate: log reflection failed", "srcID", srcID, "error", err)
			continue
		}
		migrated++
	}
	return migrated, nil
}

// umMigrateNotes migrates from notes files in the vault directory.
// Maps: relative path -> key, SHA-256 of content (first 32 hex chars) -> value,
// namespace = UMNSFile. Only processes .md files.
func umMigrateNotes(dbPath string, vaultPath string) (int, error) {
	info, err := os.Stat(vaultPath)
	if err != nil || !info.IsDir() {
		return 0, fmt.Errorf("vault path not accessible: %s", vaultPath)
	}

	migrated := 0
	walkErr := filepath.Walk(vaultPath, func(path string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
			return nil
		}

		rel, err := filepath.Rel(vaultPath, path)
		if err != nil {
			return nil
		}

		// Use relative path as source ID for idempotency.
		if umIsMigrated(dbPath, "notes", rel) {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			logWarn("um_migrate: read note failed", "path", rel, "error", err)
			return nil
		}

		h := sha256.Sum256(data)
		hash := hex.EncodeToString(h[:16]) // first 32 hex chars

		entry := UnifiedMemoryEntry{
			Namespace: UMNSFile,
			Key:       rel,
			Value:     hash,
			Source:    "migration:notes",
			SourceRef: rel,
		}

		memID, _, err := umStore(dbPath, entry)
		if err != nil {
			logWarn("um_migrate: store note failed", "path", rel, "error", err)
			return nil
		}
		if err := umLogMigration(dbPath, "notes", rel, memID); err != nil {
			logWarn("um_migrate: log note failed", "path", rel, "error", err)
			return nil
		}
		migrated++
		return nil
	})
	if walkErr != nil {
		return migrated, fmt.Errorf("walk vault: %w", walkErr)
	}
	return migrated, nil
}

// umIsMigrated checks if a source record has already been migrated
// by looking up the um_data_migration_log table.
func umIsMigrated(dbPath, sourceTable, sourceID string) bool {
	sql := fmt.Sprintf(
		`SELECT COUNT(*) as cnt FROM um_data_migration_log WHERE source_table='%s' AND source_id='%s'`,
		escapeSQLite(sourceTable), escapeSQLite(sourceID),
	)
	rows, err := queryDB(dbPath, sql)
	if err != nil || len(rows) == 0 {
		return false
	}
	return jsonInt(rows[0]["cnt"]) > 0
}

// umLogMigration records a completed migration in the um_data_migration_log table.
func umLogMigration(dbPath, sourceTable, sourceID, memoryID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	sql := fmt.Sprintf(
		`INSERT INTO um_data_migration_log (source_table, source_id, memory_id, migrated_at) VALUES ('%s','%s','%s','%s')`,
		escapeSQLite(sourceTable),
		escapeSQLite(sourceID),
		escapeSQLite(memoryID),
		now,
	)
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("log migration: %s: %w", string(out), err)
	}
	return nil
}

// initMigrationLogDB creates the um_data_migration_log table if it doesn't exist.
// This is separate from memory_migration_log (used for schema migrations).
func initMigrationLogDB(dbPath string) error {
	schema := `
CREATE TABLE IF NOT EXISTS um_data_migration_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_table TEXT NOT NULL,
    source_id TEXT NOT NULL,
    memory_id TEXT NOT NULL,
    migrated_at TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_um_dmlog_src ON um_data_migration_log(source_table, source_id);
`
	cmd := exec.Command("sqlite3", dbPath, schema)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("init um_data_migration_log: %s: %w", string(out), err)
	}
	return nil
}
