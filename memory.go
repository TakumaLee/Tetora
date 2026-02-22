package main

import (
	"fmt"
	"os/exec"
	"time"
)

// --- Agent Memory Types ---

// MemoryEntry represents a key-value memory entry scoped to a role.
type MemoryEntry struct {
	ID        int    `json:"id"`
	Role      string `json:"role"`
	Key       string `json:"key"`
	Value     string `json:"value"`
	UpdatedAt string `json:"updatedAt"`
	CreatedAt string `json:"createdAt"`
}

// --- Init ---

func initMemoryDB(dbPath string) error {
	sql := `
CREATE TABLE IF NOT EXISTS agent_memory (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  role TEXT NOT NULL,
  key TEXT NOT NULL,
  value TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_memory_role_key ON agent_memory(role, key);
CREATE INDEX IF NOT EXISTS idx_agent_memory_role ON agent_memory(role);
`
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("init agent_memory: %s: %w", string(out), err)
	}
	return nil
}

// --- Set (Upsert) ---

func setMemory(dbPath, role, key, value string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	sql := fmt.Sprintf(
		`INSERT INTO agent_memory (role, key, value, updated_at, created_at)
		 VALUES ('%s','%s','%s','%s','%s')
		 ON CONFLICT(role, key) DO UPDATE SET
		   value = excluded.value,
		   updated_at = excluded.updated_at`,
		escapeSQLite(role),
		escapeSQLite(key),
		escapeSQLite(value),
		now, now,
	)
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("set memory: %s: %w", string(out), err)
	}
	return nil
}

// --- Get ---

func getMemory(dbPath, role, key string) (string, error) {
	sql := fmt.Sprintf(
		`SELECT value FROM agent_memory WHERE role = '%s' AND key = '%s'`,
		escapeSQLite(role), escapeSQLite(key))

	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", nil
	}
	return jsonStr(rows[0]["value"]), nil
}

// --- List ---

func listMemory(dbPath, role string) ([]MemoryEntry, error) {
	where := ""
	if role != "" {
		where = fmt.Sprintf("WHERE role = '%s'", escapeSQLite(role))
	}

	sql := fmt.Sprintf(
		`SELECT id, role, key, value, updated_at, created_at
		 FROM agent_memory %s ORDER BY role, key ASC`, where)

	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return nil, err
	}

	var entries []MemoryEntry
	for _, row := range rows {
		entries = append(entries, memoryEntryFromRow(row))
	}
	return entries, nil
}

func memoryEntryFromRow(row map[string]any) MemoryEntry {
	return MemoryEntry{
		ID:        jsonInt(row["id"]),
		Role:      jsonStr(row["role"]),
		Key:       jsonStr(row["key"]),
		Value:     jsonStr(row["value"]),
		UpdatedAt: jsonStr(row["updated_at"]),
		CreatedAt: jsonStr(row["created_at"]),
	}
}

// --- Delete ---

func deleteMemory(dbPath, role, key string) error {
	sql := fmt.Sprintf(
		`DELETE FROM agent_memory WHERE role = '%s' AND key = '%s'`,
		escapeSQLite(role), escapeSQLite(key))
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("delete memory: %s: %w", string(out), err)
	}
	return nil
}
