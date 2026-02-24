package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// tempMigrateDB creates a temporary SQLite DB with unified_memory tables,
// migration log, and legacy tables (agent_memory, embeddings, reflections).
func tempMigrateDB(t *testing.T) string {
	t.Helper()
	f, err := os.CreateTemp("", "um-migrate-*.db")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	path := f.Name()
	t.Cleanup(func() { os.Remove(path) })

	if err := initUnifiedMemoryDB(path); err != nil {
		t.Fatalf("init unified memory db: %v", err)
	}
	if err := initMigrationLogDB(path); err != nil {
		t.Fatalf("init migration log db: %v", err)
	}
	if err := initMemoryDB(path); err != nil {
		t.Fatalf("init memory db: %v", err)
	}
	if err := initEmbeddingDB(path); err != nil {
		t.Fatalf("init embedding db: %v", err)
	}
	if err := initReflectionDB(path); err != nil {
		t.Fatalf("init reflection db: %v", err)
	}
	return path
}

// migrateExecSQL is a test helper to run raw SQL against a temp DB.
func migrateExecSQL(t *testing.T, dbPath, sql string) {
	t.Helper()
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("exec sql: %s: %v", string(out), err)
	}
}

// ---------------------------------------------------------------------------
// TestUmMigrateAgentMemory
// ---------------------------------------------------------------------------

func TestUmMigrateAgentMemory(t *testing.T) {
	dbPath := tempMigrateDB(t)

	// Insert legacy agent_memory rows.
	migrateExecSQL(t, dbPath, `INSERT INTO agent_memory (role, key, value, updated_at, created_at) VALUES ('assistant', 'user_name', 'Alice', '2025-01-01T00:00:00Z', '2025-01-01T00:00:00Z')`)
	migrateExecSQL(t, dbPath, `INSERT INTO agent_memory (role, key, value, updated_at, created_at) VALUES ('assistant', 'preferred_language', 'Japanese', '2025-01-01T00:00:00Z', '2025-01-01T00:00:00Z')`)
	migrateExecSQL(t, dbPath, `INSERT INTO agent_memory (role, key, value, updated_at, created_at) VALUES ('coder', 'editor_setting', 'vim', '2025-01-01T00:00:00Z', '2025-01-01T00:00:00Z')`)

	n, err := umMigrateAgentMemory(dbPath)
	if err != nil {
		t.Fatalf("umMigrateAgentMemory: %v", err)
	}
	if n != 3 {
		t.Errorf("expected 3 migrated, got %d", n)
	}

	// Verify: "user_name" should be namespace=fact.
	entry, err := umGet(dbPath, UMNSFact, "assistant", "user_name")
	if err != nil {
		t.Fatalf("umGet: %v", err)
	}
	if entry == nil {
		t.Fatal("expected entry for user_name, got nil")
	}
	if entry.Value != "Alice" {
		t.Errorf("expected value Alice, got %s", entry.Value)
	}

	// Verify: "preferred_language" should be namespace=preference (contains "prefer").
	entry, err = umGet(dbPath, UMNSPreference, "assistant", "preferred_language")
	if err != nil {
		t.Fatalf("umGet: %v", err)
	}
	if entry == nil {
		t.Fatal("expected entry for preferred_language, got nil")
	}
	if entry.Value != "Japanese" {
		t.Errorf("expected value Japanese, got %s", entry.Value)
	}

	// Verify: "editor_setting" should be namespace=preference (contains "setting").
	entry, err = umGet(dbPath, UMNSPreference, "coder", "editor_setting")
	if err != nil {
		t.Fatalf("umGet: %v", err)
	}
	if entry == nil {
		t.Fatal("expected entry for editor_setting, got nil")
	}
	if entry.Value != "vim" {
		t.Errorf("expected value vim, got %s", entry.Value)
	}

	// Run again -- should be idempotent (0 new migrations).
	n2, err := umMigrateAgentMemory(dbPath)
	if err != nil {
		t.Fatalf("umMigrateAgentMemory (2nd): %v", err)
	}
	if n2 != 0 {
		t.Errorf("expected 0 on re-run, got %d", n2)
	}
}

// ---------------------------------------------------------------------------
// TestUmMigrateEmbeddings
// ---------------------------------------------------------------------------

func TestUmMigrateEmbeddings(t *testing.T) {
	dbPath := tempMigrateDB(t)

	// Insert legacy embedding rows (embedding BLOB can be empty for migration purposes).
	migrateExecSQL(t, dbPath, `INSERT INTO embeddings (source, source_id, content, embedding, created_at) VALUES ('discord', 'msg-123', 'Hello world', X'00', '2025-01-01T00:00:00Z')`)
	migrateExecSQL(t, dbPath, `INSERT INTO embeddings (source, source_id, content, embedding, created_at) VALUES ('telegram', 'msg-456', 'Goodbye', X'00', '2025-01-01T00:00:00Z')`)

	n, err := umMigrateEmbeddings(dbPath)
	if err != nil {
		t.Fatalf("umMigrateEmbeddings: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 migrated, got %d", n)
	}

	// Verify key format: emb:source:source_id.
	entry, err := umGet(dbPath, UMNSFact, "", "emb:discord:msg-123")
	if err != nil {
		t.Fatalf("umGet: %v", err)
	}
	if entry == nil {
		t.Fatal("expected entry for emb:discord:msg-123, got nil")
	}
	if entry.Value != "Hello world" {
		t.Errorf("expected value 'Hello world', got %s", entry.Value)
	}

	// Idempotency check.
	n2, err := umMigrateEmbeddings(dbPath)
	if err != nil {
		t.Fatalf("umMigrateEmbeddings (2nd): %v", err)
	}
	if n2 != 0 {
		t.Errorf("expected 0 on re-run, got %d", n2)
	}
}

// ---------------------------------------------------------------------------
// TestUmMigrateReflections
// ---------------------------------------------------------------------------

func TestUmMigrateReflections(t *testing.T) {
	dbPath := tempMigrateDB(t)

	// Insert legacy reflection rows.
	migrateExecSQL(t, dbPath, `INSERT INTO reflections (task_id, role, score, feedback, improvement, cost_usd, created_at) VALUES ('task-001', 'assistant', 4, 'Good response', 'Be more concise', 0.01, '2025-01-01T00:00:00Z')`)
	migrateExecSQL(t, dbPath, `INSERT INTO reflections (task_id, role, score, feedback, improvement, cost_usd, created_at) VALUES ('task-002', 'coder', 3, 'Code works', 'Add error handling', 0.02, '2025-01-01T00:00:00Z')`)

	n, err := umMigrateReflections(dbPath)
	if err != nil {
		t.Fatalf("umMigrateReflections: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 migrated, got %d", n)
	}

	// Verify key format: reflect:task_id.
	entry, err := umGet(dbPath, UMNSReflection, "assistant", "reflect:task-001")
	if err != nil {
		t.Fatalf("umGet: %v", err)
	}
	if entry == nil {
		t.Fatal("expected entry for reflect:task-001, got nil")
	}
	// Value should be "feedback\n---\nimprovement".
	expected := "Good response\n---\nBe more concise"
	if entry.Value != expected {
		t.Errorf("expected value %q, got %q", expected, entry.Value)
	}

	// Idempotency check.
	n2, err := umMigrateReflections(dbPath)
	if err != nil {
		t.Fatalf("umMigrateReflections (2nd): %v", err)
	}
	if n2 != 0 {
		t.Errorf("expected 0 on re-run, got %d", n2)
	}
}

// ---------------------------------------------------------------------------
// TestUmMigrateNotes
// ---------------------------------------------------------------------------

func TestUmMigrateNotes(t *testing.T) {
	dbPath := tempMigrateDB(t)
	vaultDir := t.TempDir()

	// Create test .md files.
	os.WriteFile(filepath.Join(vaultDir, "note1.md"), []byte("Hello notes"), 0o644)
	os.MkdirAll(filepath.Join(vaultDir, "sub"), 0o755)
	os.WriteFile(filepath.Join(vaultDir, "sub", "note2.md"), []byte("Nested note"), 0o644)
	// Non-md file should be skipped.
	os.WriteFile(filepath.Join(vaultDir, "readme.txt"), []byte("Not a note"), 0o644)

	n, err := umMigrateNotes(dbPath, vaultDir)
	if err != nil {
		t.Fatalf("umMigrateNotes: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 migrated, got %d", n)
	}

	// Verify: note1.md should exist with namespace=file.
	entry, err := umGet(dbPath, UMNSFile, "", "note1.md")
	if err != nil {
		t.Fatalf("umGet: %v", err)
	}
	if entry == nil {
		t.Fatal("expected entry for note1.md, got nil")
	}
	// Value should be a 32-char hex hash.
	if len(entry.Value) != 32 {
		t.Errorf("expected 32 hex chars, got %d: %s", len(entry.Value), entry.Value)
	}

	// Verify: sub/note2.md should use relative path as key.
	entry, err = umGet(dbPath, UMNSFile, "", filepath.Join("sub", "note2.md"))
	if err != nil {
		t.Fatalf("umGet: %v", err)
	}
	if entry == nil {
		t.Fatal("expected entry for sub/note2.md, got nil")
	}

	// Idempotency check.
	n2, err := umMigrateNotes(dbPath, vaultDir)
	if err != nil {
		t.Fatalf("umMigrateNotes (2nd): %v", err)
	}
	if n2 != 0 {
		t.Errorf("expected 0 on re-run, got %d", n2)
	}
}

// ---------------------------------------------------------------------------
// TestUmMigrateNotes_InvalidVault
// ---------------------------------------------------------------------------

func TestUmMigrateNotes_InvalidVault(t *testing.T) {
	dbPath := tempMigrateDB(t)

	_, err := umMigrateNotes(dbPath, "/nonexistent/vault/path")
	if err == nil {
		t.Fatal("expected error for invalid vault path")
	}
}

// ---------------------------------------------------------------------------
// TestUmIsMigrated
// ---------------------------------------------------------------------------

func TestUmIsMigrated(t *testing.T) {
	dbPath := tempMigrateDB(t)

	// Should not be migrated initially.
	if umIsMigrated(dbPath, "agent_memory", "1") {
		t.Error("expected not migrated for non-existent record")
	}

	// Log a migration and check again.
	if err := umLogMigration(dbPath, "agent_memory", "1", "um-test-id"); err != nil {
		t.Fatalf("umLogMigration: %v", err)
	}
	if !umIsMigrated(dbPath, "agent_memory", "1") {
		t.Error("expected migrated after logging")
	}

	// Different source table should not match.
	if umIsMigrated(dbPath, "embeddings", "1") {
		t.Error("expected not migrated for different table")
	}

	// Duplicate insert should fail silently (unique constraint).
	err := umLogMigration(dbPath, "agent_memory", "1", "um-test-id-2")
	if err == nil {
		t.Error("expected error for duplicate migration log")
	}
}

// ---------------------------------------------------------------------------
// TestUmMigrateAll
// ---------------------------------------------------------------------------

func TestUmMigrateAll(t *testing.T) {
	dbPath := tempMigrateDB(t)
	vaultDir := t.TempDir()

	// Seed legacy data.
	migrateExecSQL(t, dbPath, `INSERT INTO agent_memory (role, key, value, updated_at, created_at) VALUES ('bot', 'greeting', 'Hi!', '2025-01-01T00:00:00Z', '2025-01-01T00:00:00Z')`)
	migrateExecSQL(t, dbPath, `INSERT INTO embeddings (source, source_id, content, embedding, created_at) VALUES ('web', 'page-1', 'Page content', X'00', '2025-01-01T00:00:00Z')`)
	migrateExecSQL(t, dbPath, `INSERT INTO reflections (task_id, role, score, feedback, improvement, cost_usd, created_at) VALUES ('t1', 'bot', 5, 'Great', 'Nothing', 0.0, '2025-01-01T00:00:00Z')`)
	os.WriteFile(filepath.Join(vaultDir, "all_test.md"), []byte("Full migration test"), 0o644)

	stats, err := umMigrateAll(dbPath, vaultDir)
	if err != nil {
		t.Fatalf("umMigrateAll: %v", err)
	}

	if stats.AgentMemory != 1 {
		t.Errorf("AgentMemory: want 1, got %d", stats.AgentMemory)
	}
	if stats.Embeddings != 1 {
		t.Errorf("Embeddings: want 1, got %d", stats.Embeddings)
	}
	if stats.Reflections != 1 {
		t.Errorf("Reflections: want 1, got %d", stats.Reflections)
	}
	if stats.Notes != 1 {
		t.Errorf("Notes: want 1, got %d", stats.Notes)
	}
	if stats.Errors != 0 {
		t.Errorf("Errors: want 0, got %d", stats.Errors)
	}

	// Re-run should produce 0 for everything (idempotent).
	stats2, err := umMigrateAll(dbPath, vaultDir)
	if err != nil {
		t.Fatalf("umMigrateAll (2nd): %v", err)
	}
	if stats2.AgentMemory != 0 || stats2.Embeddings != 0 || stats2.Reflections != 0 || stats2.Notes != 0 {
		t.Errorf("expected all 0 on re-run, got agents=%d emb=%d ref=%d notes=%d",
			stats2.AgentMemory, stats2.Embeddings, stats2.Reflections, stats2.Notes)
	}
}

// ---------------------------------------------------------------------------
// TestUmMigrateAll_EmptyVault
// ---------------------------------------------------------------------------

func TestUmMigrateAll_EmptyVault(t *testing.T) {
	dbPath := tempMigrateDB(t)

	// No vault path -- notes migration should be skipped.
	stats, err := umMigrateAll(dbPath, "")
	if err != nil {
		t.Fatalf("umMigrateAll: %v", err)
	}
	if stats.Notes != 0 {
		t.Errorf("Notes: want 0 with empty vault, got %d", stats.Notes)
	}
}
