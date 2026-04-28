package taskboard

import (
	"strings"
	"testing"
	"time"

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

// =============================================================================
// SetRetryBackoff tests (Proposal C)
// =============================================================================

func TestSetRetryBackoff_FirstFailureNoDelay(t *testing.T) {
	// retryCount=0 → no backoff; next_retry_at should remain unset.
	engine := newTestEngine(t)
	task, _ := engine.CreateTask(TaskBoard{Title: "backoff test", Status: "todo"})
	engine.MoveTask(task.ID, "failed")

	engine.SetRetryBackoff(task.ID, 0)

	got, _ := engine.GetTask(task.ID)
	if got.NextRetryAt != "" {
		t.Errorf("expected no next_retry_at for retryCount=0, got %q", got.NextRetryAt)
	}
}

func TestSetRetryBackoff_SecondFailure5Minutes(t *testing.T) {
	// retryCount=1 → 5min backoff.
	engine := newTestEngine(t)
	task, _ := engine.CreateTask(TaskBoard{Title: "backoff test r1", Status: "todo"})
	engine.MoveTask(task.ID, "failed")

	before := time.Now().UTC()
	engine.SetRetryBackoff(task.ID, 1)
	after := time.Now().UTC()

	got, _ := engine.GetTask(task.ID)
	if got.NextRetryAt == "" {
		t.Fatal("expected next_retry_at to be set for retryCount=1")
	}
	parsed, err := time.Parse(time.RFC3339, got.NextRetryAt)
	if err != nil {
		t.Fatalf("next_retry_at not RFC3339: %v", err)
	}
	lo := before.Add(4 * time.Minute)
	hi := after.Add(6 * time.Minute)
	if parsed.Before(lo) || parsed.After(hi) {
		t.Errorf("next_retry_at %v not in [%v, %v]", parsed, lo, hi)
	}
}

// TestAutoRetryFailed_RequireHumanConfirm_NoRepeat verifies that a failed task
// with retry_policy.require_human_confirm=true produces exactly one
// "[retry-policy] Human confirmation required" comment no matter how many times
// AutoRetryFailed is invoked by the daemon. Previously the handler added the
// comment and `continue`d without updating next_retry_at, so the next scan
// matched the same row and repeated the comment (one per daemon tick).
func TestAutoRetryFailed_RequireHumanConfirm_NoRepeat(t *testing.T) {
	engine := newTestEngine(t)

	task, err := engine.CreateTask(TaskBoard{
		Title:       "human-confirm repeat guard",
		Status:      "todo",
		RetryPolicy: `{"require_human_confirm":true}`,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := engine.MoveTask(task.ID, "failed"); err != nil {
		t.Fatalf("MoveTask failed: %v", err)
	}

	for i := 0; i < 3; i++ {
		if err := engine.AutoRetryFailed(); err != nil {
			t.Fatalf("AutoRetryFailed iter=%d: %v", i, err)
		}
	}

	comments, err := engine.GetThread(task.ID)
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}
	count := 0
	for _, c := range comments {
		if strings.Contains(c.Content, "[retry-policy] Human confirmation required") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 [retry-policy] comment after 3 scans, got %d", count)
	}

	got, err := engine.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.NextRetryAt != "9999-12-31T23:59:59Z" {
		t.Errorf("expected sentinel next_retry_at, got %q", got.NextRetryAt)
	}
}

func TestSetRetryBackoff_CapsAt80Minutes(t *testing.T) {
	// retryCount=5 (shift=4) → cap at 5min × 16 = 80min.
	engine := newTestEngine(t)
	task, _ := engine.CreateTask(TaskBoard{Title: "backoff cap test", Status: "todo"})
	engine.MoveTask(task.ID, "failed")

	before := time.Now().UTC()
	engine.SetRetryBackoff(task.ID, 5)
	after := time.Now().UTC()

	got, _ := engine.GetTask(task.ID)
	if got.NextRetryAt == "" {
		t.Fatal("expected next_retry_at to be set")
	}
	parsed, _ := time.Parse(time.RFC3339, got.NextRetryAt)
	lo := before.Add(79 * time.Minute)
	hi := after.Add(81 * time.Minute)
	if parsed.Before(lo) || parsed.After(hi) {
		t.Errorf("next_retry_at %v not in [%v, %v] (expected ~80min cap)", parsed, lo, hi)
	}

	// retryCount=10 should give the same cap.
	engine2 := newTestEngine(t)
	task2, _ := engine2.CreateTask(TaskBoard{Title: "backoff cap test 2", Status: "todo"})
	engine2.MoveTask(task2.ID, "failed")
	engine2.SetRetryBackoff(task2.ID, 10)
	got2, _ := engine2.GetTask(task2.ID)
	parsed2, _ := time.Parse(time.RFC3339, got2.NextRetryAt)
	if parsed2.Before(lo) || parsed2.After(hi) {
		t.Errorf("retryCount=10 next_retry_at %v should also be ~80min, not higher", parsed2)
	}
}

func TestMatchSlotTimeoutSignature(t *testing.T) {
	cases := []struct {
		name     string
		content  string
		wantOK   bool
		wantName string
	}{
		{"deadline", "Task failed: context deadline exceeded while waiting", true, "deadline-exceeded"},
		{"slot-cancel", "slot acquisition cancelled: context canceled", true, "slot-acquisition-cancelled"},
		{"cron-timeout", "triggered REQUEST_TIMEOUT_CRON after 10m", true, "request-timeout-cron"},
		{"slot-pressure", "slot pressure detected, timeout exceeded", true, "slot-pressure-timeout"},
		{"unrelated", "task failed: exit code 2", false, ""},
		{"partial-deadline", "context deadline exceeded", false, ""},
		{"case-insensitive", "Slot Acquisition Cancelled due to shutdown", true, "slot-acquisition-cancelled"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, name := matchSlotTimeoutSignature(tc.content)
			if ok != tc.wantOK || name != tc.wantName {
				t.Errorf("matchSlotTimeoutSignature(%q) = (%v, %q), want (%v, %q)",
					tc.content, ok, name, tc.wantOK, tc.wantName)
			}
		})
	}
}
