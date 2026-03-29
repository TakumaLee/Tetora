package workflow

import (
	"path/filepath"
	"testing"

	"tetora/internal/db"
)

func setupTestDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	InitHumanGateTable(dbPath)
	return dbPath
}

func TestQueryAllPendingHumanGates(t *testing.T) {
	dbPath := setupTestDB(t)

	// Insert two waiting gates for different workflows.
	RecordHumanGate(dbPath, "key1", "run1", "step1", "wf-alpha", "approval", "Please approve", "alice", "2099-01-01 00:00:00", "", "")
	RecordHumanGate(dbPath, "key2", "run2", "step1", "wf-beta", "input", "Enter value", "bob", "2099-01-01 00:00:00", "", "")

	t.Run("Given status=waiting, When two gates exist, Then both are returned", func(t *testing.T) {
		records := QueryAllPendingHumanGates(dbPath, "waiting")
		if len(records) != 2 {
			t.Fatalf("expected 2 records, got %d", len(records))
		}
	})

	t.Run("Given empty status, When two gates exist, Then both are returned", func(t *testing.T) {
		records := QueryAllPendingHumanGates(dbPath, "")
		if len(records) != 2 {
			t.Fatalf("expected 2 records, got %d", len(records))
		}
	})

	t.Run("Given status=completed, When no completed gates, Then empty slice returned", func(t *testing.T) {
		records := QueryAllPendingHumanGates(dbPath, "completed")
		if len(records) != 0 {
			t.Fatalf("expected 0 records, got %d", len(records))
		}
	})

	t.Run("Given waiting gate, When queried, Then workflow_name is populated", func(t *testing.T) {
		records := QueryAllPendingHumanGates(dbPath, "waiting")
		found := false
		for _, r := range records {
			if r.Key == "key1" {
				found = true
				if r.WorkflowName != "wf-alpha" {
					t.Errorf("expected WorkflowName 'wf-alpha', got '%s'", r.WorkflowName)
				}
			}
		}
		if !found {
			t.Error("key1 not found in results")
		}
	})

	t.Run("Given empty dbPath, Then nil returned", func(t *testing.T) {
		records := QueryAllPendingHumanGates("", "waiting")
		if records != nil {
			t.Errorf("expected nil, got %v", records)
		}
	})
}

func TestCountPendingHumanGates(t *testing.T) {
	dbPath := setupTestDB(t)

	t.Run("Given no gates, Then count is 0", func(t *testing.T) {
		n := CountPendingHumanGates(dbPath)
		if n != 0 {
			t.Errorf("expected 0, got %d", n)
		}
	})

	RecordHumanGate(dbPath, "key1", "run1", "step1", "wf-alpha", "approval", "Approve?", "alice", "2099-01-01 00:00:00", "", "")
	RecordHumanGate(dbPath, "key2", "run2", "step1", "wf-beta", "input", "Enter", "bob", "2099-01-01 00:00:00", "", "")

	t.Run("Given two waiting gates, Then count is 2", func(t *testing.T) {
		n := CountPendingHumanGates(dbPath)
		if n != 2 {
			t.Errorf("expected 2, got %d", n)
		}
	})

	CompleteHumanGate(dbPath, "key1", "approved", "", "charlie")

	t.Run("Given one completed and one waiting, Then count is 1", func(t *testing.T) {
		n := CountPendingHumanGates(dbPath)
		if n != 1 {
			t.Errorf("expected 1, got %d", n)
		}
	})

	t.Run("Given empty dbPath, Then count is 0", func(t *testing.T) {
		n := CountPendingHumanGates("")
		if n != 0 {
			t.Errorf("expected 0, got %d", n)
		}
	})
}

func TestRecordHumanGateWorkflowName(t *testing.T) {
	dbPath := setupTestDB(t)

	RecordHumanGate(dbPath, "key1", "run1", "step1", "my-workflow", "approval", "Review this", "alice", "2099-01-01 00:00:00", "", "")

	t.Run("Given recorded gate with workflow_name, When queried by key, Then workflow_name matches", func(t *testing.T) {
		r := QueryHumanGate(dbPath, "key1")
		if r == nil {
			t.Fatal("expected record, got nil")
		}
		if r.WorkflowName != "my-workflow" {
			t.Errorf("expected WorkflowName 'my-workflow', got '%s'", r.WorkflowName)
		}
	})
}

func TestRecordHumanGateOptionsContext(t *testing.T) {
	dbPath := setupTestDB(t)

	opts := `["Approve","Reject","Delegate"]`
	ctx := `{"pr_url":"https://github.com/example/pr/1","requester":"alice"}`
	RecordHumanGate(dbPath, "key-oc", "run1", "step1", "wf-options", "approval", "Review PR", "bob", "2099-01-01 00:00:00", opts, ctx)

	t.Run("Given gate with options and context, When queried, Then options and context are parsed", func(t *testing.T) {
		r := QueryHumanGate(dbPath, "key-oc")
		if r == nil {
			t.Fatal("expected record, got nil")
		}
		if len(r.Options) != 3 {
			t.Errorf("expected 3 options, got %d: %v", len(r.Options), r.Options)
		}
		if r.Options[0] != "Approve" {
			t.Errorf("expected first option 'Approve', got '%s'", r.Options[0])
		}
		if r.Context["pr_url"] != "https://github.com/example/pr/1" {
			t.Errorf("expected pr_url in context, got %v", r.Context)
		}
	})

	t.Run("Given gate with empty options, When queried, Then options is nil", func(t *testing.T) {
		RecordHumanGate(dbPath, "key-empty", "run2", "step1", "wf-empty", "approval", "p", "a", "2099-01-01 00:00:00", "", "")
		r := QueryHumanGate(dbPath, "key-empty")
		if r == nil {
			t.Fatal("expected record")
		}
		if r.Options != nil {
			t.Errorf("expected nil options, got %v", r.Options)
		}
		if r.Context != nil {
			t.Errorf("expected nil context, got %v", r.Context)
		}
	})
}

func TestMigrationWorkflowName(t *testing.T) {
	// Verify that InitHumanGateTable handles existing DB without workflow_name column.
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "migrate.db")

	// Manually create table without workflow_name to simulate old schema.
	oldSQL := `CREATE TABLE IF NOT EXISTS workflow_human_gates (
		key TEXT PRIMARY KEY,
		run_id TEXT NOT NULL,
		step_id TEXT NOT NULL,
		subtype TEXT NOT NULL,
		prompt TEXT,
		assignee TEXT,
		status TEXT NOT NULL DEFAULT 'waiting',
		decision TEXT,
		response TEXT,
		responded_by TEXT,
		timeout_at TEXT,
		created_at TEXT DEFAULT (datetime('now')),
		completed_at TEXT
	)`
	if err := db.Exec(dbPath, oldSQL); err != nil {
		t.Fatalf("failed to create old schema: %v", err)
	}
	// Run InitHumanGateTable which should apply migration (ADD COLUMN workflow_name).
	InitHumanGateTable(dbPath)

	// Should be able to insert with workflow_name now.
	RecordHumanGate(dbPath, "k1", "r1", "s1", "migrated-wf", "approval", "p", "a", "2099-01-01 00:00:00", "", "")
	r := QueryHumanGate(dbPath, "k1")
	if r == nil {
		t.Fatal("expected record after migration")
	}
	if r.WorkflowName != "migrated-wf" {
		t.Errorf("expected 'migrated-wf', got '%s'", r.WorkflowName)
	}

}
