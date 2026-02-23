package main

import (
	"path/filepath"
	"testing"
)

func TestTaskBoardCRUD(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_tasks.db")

	tb := newTaskBoardEngine(dbPath, TaskBoardConfig{Enabled: true, MaxRetries: 3}, nil)
	if err := tb.initTaskBoardSchema(); err != nil {
		t.Fatal(err)
	}

	// Create task.
	task, err := tb.CreateTask(TaskBoard{
		Title:       "Test Task",
		Description: "Test description",
		Priority:    "high",
		Assignee:    "琉璃",
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.ID == "" {
		t.Fatal("task ID should be generated")
	}
	if task.Status != "backlog" {
		t.Fatalf("expected status backlog, got %s", task.Status)
	}

	// Update task.
	updated, err := tb.UpdateTask(task.ID, map[string]any{
		"title": "Updated Task",
		"priority": "urgent",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title != "Updated Task" {
		t.Fatalf("expected Updated Task, got %s", updated.Title)
	}
	if updated.Priority != "urgent" {
		t.Fatalf("expected urgent, got %s", updated.Priority)
	}

	// List tasks.
	tasks, err := tb.ListTasks("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}

	// Get task by ID.
	fetched, err := tb.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fetched.ID != task.ID {
		t.Fatalf("expected %s, got %s", task.ID, fetched.ID)
	}
}

func TestTaskBoardStatusTransitions(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_tasks.db")

	tb := newTaskBoardEngine(dbPath, TaskBoardConfig{Enabled: true}, nil)
	if err := tb.initTaskBoardSchema(); err != nil {
		t.Fatal(err)
	}

	task, err := tb.CreateTask(TaskBoard{
		Title: "Status Test",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Move backlog → todo.
	task, err = tb.MoveTask(task.ID, "todo")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != "todo" {
		t.Fatalf("expected todo, got %s", task.Status)
	}

	// Move todo → doing.
	task, err = tb.MoveTask(task.ID, "doing")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != "doing" {
		t.Fatalf("expected doing, got %s", task.Status)
	}

	// Move doing → done.
	task, err = tb.MoveTask(task.ID, "done")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != "done" {
		t.Fatalf("expected done, got %s", task.Status)
	}
	if task.CompletedAt == "" {
		t.Fatal("completedAt should be set when status is done")
	}
}

func TestTaskBoardDependencyEnforcement(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_tasks.db")

	tb := newTaskBoardEngine(dbPath, TaskBoardConfig{Enabled: true}, nil)
	if err := tb.initTaskBoardSchema(); err != nil {
		t.Fatal(err)
	}

	// Create task A (dependency).
	taskA, err := tb.CreateTask(TaskBoard{
		Title: "Task A",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Create task B that depends on A.
	taskB, err := tb.CreateTask(TaskBoard{
		Title:     "Task B",
		DependsOn: []string{taskA.ID},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Try to move task B to doing while A is still in backlog → should fail.
	taskB, _ = tb.MoveTask(taskB.ID, "todo")
	_, err = tb.MoveTask(taskB.ID, "doing")
	if err == nil {
		t.Fatal("expected error when moving task with incomplete dependencies")
	}

	// Complete task A.
	taskA, _ = tb.MoveTask(taskA.ID, "todo")
	taskA, _ = tb.MoveTask(taskA.ID, "doing")
	taskA, err = tb.MoveTask(taskA.ID, "done")
	if err != nil {
		t.Fatal(err)
	}

	// Now task B should be able to move to doing.
	taskB, err = tb.MoveTask(taskB.ID, "doing")
	if err != nil {
		t.Fatal(err)
	}
	if taskB.Status != "doing" {
		t.Fatalf("expected doing, got %s", taskB.Status)
	}
}

func TestTaskBoardAssignment(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_tasks.db")

	tb := newTaskBoardEngine(dbPath, TaskBoardConfig{Enabled: true}, nil)
	if err := tb.initTaskBoardSchema(); err != nil {
		t.Fatal(err)
	}

	task, err := tb.CreateTask(TaskBoard{
		Title: "Assign Test",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Assign to 翡翠.
	task, err = tb.AssignTask(task.ID, "翡翠")
	if err != nil {
		t.Fatal(err)
	}
	if task.Assignee != "翡翠" {
		t.Fatalf("expected 翡翠, got %s", task.Assignee)
	}

	// List tasks by assignee.
	tasks, err := tb.ListTasks("", "翡翠", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
}

func TestTaskBoardComments(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_tasks.db")

	tb := newTaskBoardEngine(dbPath, TaskBoardConfig{Enabled: true}, nil)
	if err := tb.initTaskBoardSchema(); err != nil {
		t.Fatal(err)
	}

	task, err := tb.CreateTask(TaskBoard{
		Title: "Comment Test",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Add comment.
	comment, err := tb.AddComment(task.ID, "琉璃", "This is a test comment")
	if err != nil {
		t.Fatal(err)
	}
	if comment.ID == "" {
		t.Fatal("comment ID should be generated")
	}

	// Get thread.
	comments, err := tb.GetThread(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(comments))
	}
	if comments[0].Content != "This is a test comment" {
		t.Fatalf("expected 'This is a test comment', got %s", comments[0].Content)
	}
}

func TestTaskBoardAutoRetry(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_tasks.db")

	tb := newTaskBoardEngine(dbPath, TaskBoardConfig{Enabled: true, MaxRetries: 3}, nil)
	if err := tb.initTaskBoardSchema(); err != nil {
		t.Fatal(err)
	}

	task, err := tb.CreateTask(TaskBoard{
		Title: "Retry Test",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Move to failed.
	task, _ = tb.MoveTask(task.ID, "todo")
	task, _ = tb.MoveTask(task.ID, "doing")
	task, err = tb.MoveTask(task.ID, "failed")
	if err != nil {
		t.Fatal(err)
	}

	// Auto retry should move it back to todo.
	if err := tb.AutoRetryFailed(); err != nil {
		t.Fatal(err)
	}

	task, err = tb.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != "todo" {
		t.Fatalf("expected todo after retry, got %s", task.Status)
	}
	if task.RetryCount != 1 {
		t.Fatalf("expected retryCount 1, got %d", task.RetryCount)
	}

	// Retry again.
	task, _ = tb.MoveTask(task.ID, "doing")
	task, _ = tb.MoveTask(task.ID, "failed")
	tb.AutoRetryFailed()

	task, _ = tb.GetTask(task.ID)
	if task.RetryCount != 2 {
		t.Fatalf("expected retryCount 2, got %d", task.RetryCount)
	}
}

func TestTaskBoardQualityGate(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_tasks.db")

	tb := newTaskBoardEngine(dbPath, TaskBoardConfig{Enabled: true, RequireReview: true}, nil)
	if err := tb.initTaskBoardSchema(); err != nil {
		t.Fatal(err)
	}

	task, err := tb.CreateTask(TaskBoard{
		Title: "Review Gate Test",
	})
	if err != nil {
		t.Fatal(err)
	}

	task, _ = tb.MoveTask(task.ID, "todo")
	task, _ = tb.MoveTask(task.ID, "doing")

	// Try to move directly to done → should fail.
	_, err = tb.MoveTask(task.ID, "done")
	if err == nil {
		t.Fatal("expected error when moving to done without review")
	}

	// Move to review first.
	task, err = tb.MoveTask(task.ID, "review")
	if err != nil {
		t.Fatal(err)
	}

	// Now move to done → should succeed.
	task, err = tb.MoveTask(task.ID, "done")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != "done" {
		t.Fatalf("expected done, got %s", task.Status)
	}
}

func TestTaskBoardInvalidStatus(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_tasks.db")

	tb := newTaskBoardEngine(dbPath, TaskBoardConfig{Enabled: true}, nil)
	if err := tb.initTaskBoardSchema(); err != nil {
		t.Fatal(err)
	}

	task, err := tb.CreateTask(TaskBoard{
		Title: "Invalid Status Test",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Try to move to invalid status.
	_, err = tb.MoveTask(task.ID, "invalid")
	if err == nil {
		t.Fatal("expected error when moving to invalid status")
	}
}

func TestTaskBoardPriorityOrdering(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_tasks.db")

	tb := newTaskBoardEngine(dbPath, TaskBoardConfig{Enabled: true}, nil)
	if err := tb.initTaskBoardSchema(); err != nil {
		t.Fatal(err)
	}

	// Create tasks with different priorities.
	tb.CreateTask(TaskBoard{Title: "Low Priority", Priority: "low"})
	tb.CreateTask(TaskBoard{Title: "Urgent Priority", Priority: "urgent"})
	tb.CreateTask(TaskBoard{Title: "Normal Priority", Priority: "normal"})
	tb.CreateTask(TaskBoard{Title: "High Priority", Priority: "high"})

	tasks, err := tb.ListTasks("", "", "")
	if err != nil {
		t.Fatal(err)
	}

	// Check priority ordering: urgent → high → normal → low.
	expectedOrder := []string{"urgent", "high", "normal", "low"}
	for i, expected := range expectedOrder {
		if tasks[i].Priority != expected {
			t.Fatalf("expected priority %s at index %d, got %s", expected, i, tasks[i].Priority)
		}
	}
}
