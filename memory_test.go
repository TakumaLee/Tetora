package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func skipIfNoSQLite(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not found, skipping")
	}
}

func TestInitMemoryDB(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	if err := initMemoryDB(dbPath); err != nil {
		t.Fatalf("initMemoryDB: %v", err)
	}
	// Idempotent.
	if err := initMemoryDB(dbPath); err != nil {
		t.Fatalf("initMemoryDB (second call): %v", err)
	}
}

func TestSetAndGetMemory(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	initMemoryDB(dbPath)

	if err := setMemory(dbPath, "amber", "topic", "Go concurrency"); err != nil {
		t.Fatalf("setMemory: %v", err)
	}

	val, err := getMemory(dbPath, "amber", "topic")
	if err != nil {
		t.Fatalf("getMemory: %v", err)
	}
	if val != "Go concurrency" {
		t.Errorf("got %q, want %q", val, "Go concurrency")
	}
}

func TestSetMemoryUpsert(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	initMemoryDB(dbPath)

	setMemory(dbPath, "amber", "topic", "first value")
	setMemory(dbPath, "amber", "topic", "second value")

	val, _ := getMemory(dbPath, "amber", "topic")
	if val != "second value" {
		t.Errorf("upsert failed: got %q, want %q", val, "second value")
	}
}

func TestGetMemoryNotFound(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	initMemoryDB(dbPath)

	val, err := getMemory(dbPath, "amber", "nonexistent")
	if err != nil {
		t.Fatalf("getMemory: %v", err)
	}
	if val != "" {
		t.Errorf("expected empty, got %q", val)
	}
}

func TestListMemoryByRole(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	initMemoryDB(dbPath)

	setMemory(dbPath, "amber", "key1", "val1")
	setMemory(dbPath, "amber", "key2", "val2")
	setMemory(dbPath, "ruby", "key3", "val3")

	entries, err := listMemory(dbPath, "amber")
	if err != nil {
		t.Fatalf("listMemory: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries for amber, got %d", len(entries))
	}
}

func TestListMemoryAllRoles(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	initMemoryDB(dbPath)

	setMemory(dbPath, "amber", "key1", "val1")
	setMemory(dbPath, "ruby", "key2", "val2")

	entries, err := listMemory(dbPath, "")
	if err != nil {
		t.Fatalf("listMemory: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries total, got %d", len(entries))
	}
}

func TestDeleteMemory(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	initMemoryDB(dbPath)

	setMemory(dbPath, "amber", "key1", "val1")
	deleteMemory(dbPath, "amber", "key1")

	val, _ := getMemory(dbPath, "amber", "key1")
	if val != "" {
		t.Errorf("expected empty after delete, got %q", val)
	}
}

func TestExpandPromptMemory(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	initMemoryDB(dbPath)

	setMemory(dbPath, "amber", "context", "previous session notes")

	got := expandPrompt("Remember: {{memory.context}}", "", dbPath, "amber", "", nil)
	want := "Remember: previous session notes"
	if got != want {
		t.Errorf("expandPrompt with memory: got %q, want %q", got, want)
	}
}

func TestExpandPromptMemoryNoRole(t *testing.T) {
	input := "Remember: {{memory.context}}"
	got := expandPrompt(input, "", "", "", "", nil)
	if got != input {
		t.Errorf("expandPrompt with no role: got %q, want %q (unchanged)", got, input)
	}
}

func TestMemorySpecialChars(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	initMemoryDB(dbPath)

	// Test with quotes and special chars in value.
	val := `He said "hello" and it's fine`
	if err := setMemory(dbPath, "amber", "quote_test", val); err != nil {
		t.Fatalf("setMemory with quotes: %v", err)
	}

	got, _ := getMemory(dbPath, "amber", "quote_test")
	if got != val {
		t.Errorf("got %q, want %q", got, val)
	}
}

func TestParseRoleFlag(t *testing.T) {
	tests := []struct {
		args     []string
		wantRole string
		wantRest []string
	}{
		{[]string{"--role", "amber", "key1"}, "amber", []string{"key1"}},
		{[]string{"key1", "--role", "amber"}, "amber", []string{"key1"}},
		{[]string{"key1"}, "", []string{"key1"}},
		{[]string{}, "", nil},
	}

	for _, tc := range tests {
		role, rest := parseRoleFlag(tc.args)
		if role != tc.wantRole {
			t.Errorf("parseRoleFlag(%v) role = %q, want %q", tc.args, role, tc.wantRole)
		}
		if len(rest) != len(tc.wantRest) {
			t.Errorf("parseRoleFlag(%v) rest len = %d, want %d", tc.args, len(rest), len(tc.wantRest))
		}
	}
}

// Verify initMemoryDB works when called from CLI context.
func TestInitMemoryDBFromCLI(t *testing.T) {
	skipIfNoSQLite(t)
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "history.db")

	// Create history db first (as main.go would).
	if err := initHistoryDB(dbPath); err != nil {
		t.Fatalf("initHistoryDB: %v", err)
	}

	// Then init memory table in same DB.
	if err := initMemoryDB(dbPath); err != nil {
		t.Fatalf("initMemoryDB: %v", err)
	}

	// Verify both tables exist.
	out, err := exec.Command("sqlite3", dbPath, ".tables").CombinedOutput()
	if err != nil {
		t.Fatalf("sqlite3 .tables: %v", err)
	}
	tables := string(out)
	if !contains(tables, "agent_memory") {
		t.Errorf("agent_memory table not found in: %s", tables)
	}
	if !contains(tables, "job_runs") {
		t.Errorf("job_runs table not found in: %s", tables)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// Ensure outputs directory exists for tests that need it.
func init() {
	os.MkdirAll(filepath.Join(os.TempDir(), "tetora-test-outputs"), 0o755)
}
