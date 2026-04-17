package taskboard

import (
	"testing"

	"tetora/internal/config"
)

// newTestEngine creates an Engine with a temp SQLite DB for unit testing.
func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	dbPath := t.TempDir() + "/test.db"
	engine := NewEngine(dbPath, config.TaskBoardConfig{}, nil)
	if err := engine.InitSchema(); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	return engine
}

// TestNormalizeTaskID_BareNumericID verifies that all mutating Engine methods
// work correctly when called with a bare numeric ID (without "task-" prefix).
// This is the regression test for the silent-fail bug where MoveTask, AssignTask,
// UpdateTask, DeleteTask, AddComment, and GetThread matched 0 rows in SQLite
// because DB stores IDs with "task-" prefix.
func TestNormalizeTaskID_BareNumericID(t *testing.T) {
	engine := newTestEngine(t)

	// Create a task — ID will have "task-" prefix.
	task, err := engine.CreateTask(TaskBoard{
		Title:  "normalize test",
		Status: "todo",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// Extract bare numeric part (strip "task-" prefix).
	bareID := task.ID[len("task-"):]

	t.Run("MoveTask with bare ID", func(t *testing.T) {
		moved, err := engine.MoveTask(bareID, "doing")
		if err != nil {
			t.Fatalf("MoveTask: %v", err)
		}
		if moved.Status != "doing" {
			t.Errorf("expected status 'doing', got %q", moved.Status)
		}
		// Verify in DB.
		got, _ := engine.GetTask(task.ID)
		if got.Status != "doing" {
			t.Errorf("DB status mismatch: expected 'doing', got %q", got.Status)
		}
	})

	t.Run("AssignTask with bare ID", func(t *testing.T) {
		assigned, err := engine.AssignTask(bareID, "kokuyou")
		if err != nil {
			t.Fatalf("AssignTask: %v", err)
		}
		if assigned.Assignee != "kokuyou" {
			t.Errorf("expected assignee 'kokuyou', got %q", assigned.Assignee)
		}
	})

	t.Run("UpdateTask with bare ID", func(t *testing.T) {
		updated, err := engine.UpdateTask(bareID, map[string]any{"title": "updated title"})
		if err != nil {
			t.Fatalf("UpdateTask: %v", err)
		}
		if updated.Title != "updated title" {
			t.Errorf("expected title 'updated title', got %q", updated.Title)
		}
	})

	t.Run("AddComment with bare ID", func(t *testing.T) {
		comment, err := engine.AddComment(bareID, "test", "hello")
		if err != nil {
			t.Fatalf("AddComment: %v", err)
		}
		if comment.TaskID != task.ID {
			t.Errorf("expected TaskID %q, got %q", task.ID, comment.TaskID)
		}
	})

	t.Run("GetThread with bare ID", func(t *testing.T) {
		comments, err := engine.GetThread(bareID)
		if err != nil {
			t.Fatalf("GetThread: %v", err)
		}
		if len(comments) != 1 {
			t.Errorf("expected 1 comment, got %d", len(comments))
		}
	})

	t.Run("DeleteTask with bare ID", func(t *testing.T) {
		if err := engine.DeleteTask(bareID); err != nil {
			t.Fatalf("DeleteTask: %v", err)
		}
		_, err := engine.GetTask(task.ID)
		if err == nil {
			t.Error("expected task to be deleted, but GetTask succeeded")
		}
	})
}

// TestScopeBoundary_RoundTrip verifies that scope_boundary is persisted on create
// and retrievable via GetTask, UpdateTask, and ListTasks.
func TestScopeBoundary_RoundTrip(t *testing.T) {
	engine := newTestEngine(t)

	t.Run("CreateTask persists scope_boundary", func(t *testing.T) {
		task, err := engine.CreateTask(TaskBoard{
			Title:         "diagnose something",
			Status:        "todo",
			ScopeBoundary: "diagnostic_only",
		})
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
		if task.ScopeBoundary != "diagnostic_only" {
			t.Errorf("expected ScopeBoundary 'diagnostic_only' in returned task, got %q", task.ScopeBoundary)
		}

		got, err := engine.GetTask(task.ID)
		if err != nil {
			t.Fatalf("GetTask: %v", err)
		}
		if got.ScopeBoundary != "diagnostic_only" {
			t.Errorf("expected ScopeBoundary 'diagnostic_only' from DB, got %q", got.ScopeBoundary)
		}
	})

	t.Run("UpdateTask updates scope_boundary", func(t *testing.T) {
		task, err := engine.CreateTask(TaskBoard{
			Title:         "implement fix",
			Status:        "todo",
			ScopeBoundary: "diagnostic_only",
		})
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}

		updated, err := engine.UpdateTask(task.ID, map[string]any{"scopeBoundary": "implement_allowed"})
		if err != nil {
			t.Fatalf("UpdateTask: %v", err)
		}
		if updated.ScopeBoundary != "implement_allowed" {
			t.Errorf("expected ScopeBoundary 'implement_allowed' after update, got %q", updated.ScopeBoundary)
		}
	})

	t.Run("Empty scope_boundary is preserved as empty", func(t *testing.T) {
		task, err := engine.CreateTask(TaskBoard{
			Title:  "regular task",
			Status: "todo",
		})
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}

		got, err := engine.GetTask(task.ID)
		if err != nil {
			t.Fatalf("GetTask: %v", err)
		}
		// Empty string or "<nil>" from DB should not be exposed as "diagnostic_only" etc.
		if got.ScopeBoundary == "diagnostic_only" || got.ScopeBoundary == "implement_allowed" {
			t.Errorf("expected empty ScopeBoundary, got %q", got.ScopeBoundary)
		}
	})
}

// TestNormalizeTaskID_PrefixedID verifies that methods still work with
// already-prefixed IDs (idempotency check).
func TestNormalizeTaskID_PrefixedID(t *testing.T) {
	engine := newTestEngine(t)

	task, err := engine.CreateTask(TaskBoard{
		Title:  "prefixed test",
		Status: "todo",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// Use the full prefixed ID — should still work.
	moved, err := engine.MoveTask(task.ID, "doing")
	if err != nil {
		t.Fatalf("MoveTask with prefixed ID: %v", err)
	}
	if moved.Status != "doing" {
		t.Errorf("expected 'doing', got %q", moved.Status)
	}
}
