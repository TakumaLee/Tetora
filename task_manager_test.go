package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// testTaskDB creates a temporary DB and initializes task manager tables.
func testTaskDB(t *testing.T) (string, func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_tasks.db")

	// Create the database file first.
	f, err := os.Create(dbPath)
	if err != nil {
		t.Fatalf("create db file: %v", err)
	}
	f.Close()

	if err := initTaskManagerDB(dbPath); err != nil {
		t.Fatalf("initTaskManagerDB: %v", err)
	}
	return dbPath, func() { os.RemoveAll(dir) }
}

func testTaskService(t *testing.T) (*TaskManagerService, func()) {
	t.Helper()
	dbPath, cleanup := testTaskDB(t)
	cfg := &Config{HistoryDB: dbPath}
	svc := newTaskManagerService(cfg)
	return svc, cleanup
}

func TestInitTaskManagerDB(t *testing.T) {
	_, cleanup := testTaskDB(t)
	defer cleanup()
}

func TestCreateTask(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	task := UserTask{
		UserID:   "user1",
		Title:    "Buy groceries",
		Priority: 2,
		Tags:     []string{"personal", "shopping"},
	}
	created, err := svc.CreateTask(task)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if created.ID == "" {
		t.Error("expected non-empty ID")
	}
	if created.Status != "todo" {
		t.Errorf("expected status 'todo', got %q", created.Status)
	}
	if created.Project != "inbox" {
		t.Errorf("expected project 'inbox', got %q", created.Project)
	}
	if created.CreatedAt == "" {
		t.Error("expected non-empty CreatedAt")
	}
	if len(created.Tags) != 2 {
		t.Errorf("expected 2 tags, got %d", len(created.Tags))
	}
}

func TestGetTask(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	task := UserTask{
		UserID:      "user1",
		Title:       "Test task",
		Description: "A test description",
		Priority:    1,
		Tags:        []string{"urgent"},
	}
	created, err := svc.CreateTask(task)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	got, err := svc.GetTask(created.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Title != "Test task" {
		t.Errorf("expected title 'Test task', got %q", got.Title)
	}
	if got.Description != "A test description" {
		t.Errorf("expected description, got %q", got.Description)
	}
	if got.Priority != 1 {
		t.Errorf("expected priority 1, got %d", got.Priority)
	}
	if len(got.Tags) != 1 || got.Tags[0] != "urgent" {
		t.Errorf("expected tags [urgent], got %v", got.Tags)
	}
}

func TestGetTask_NotFound(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	_, err := svc.GetTask("nonexistent-id")
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

func TestUpdateTask(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	created, err := svc.CreateTask(UserTask{
		UserID: "user1",
		Title:  "Original title",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	err = svc.UpdateTask(created.ID, map[string]any{
		"title":    "Updated title",
		"status":   "in_progress",
		"priority": 1,
	})
	if err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}

	got, err := svc.GetTask(created.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Title != "Updated title" {
		t.Errorf("expected updated title, got %q", got.Title)
	}
	if got.Status != "in_progress" {
		t.Errorf("expected status 'in_progress', got %q", got.Status)
	}
	if got.Priority != 1 {
		t.Errorf("expected priority 1, got %d", got.Priority)
	}
}

func TestUpdateTask_EmptyUpdates(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	created, _ := svc.CreateTask(UserTask{UserID: "u1", Title: "t"})
	err := svc.UpdateTask(created.ID, map[string]any{})
	if err != nil {
		t.Fatalf("expected no error for empty updates, got: %v", err)
	}
}

func TestDeleteTask(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	created, err := svc.CreateTask(UserTask{
		UserID: "user1",
		Title:  "To delete",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	err = svc.DeleteTask(created.ID)
	if err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	_, err = svc.GetTask(created.ID)
	if err == nil {
		t.Fatal("expected error after deletion")
	}
}

func TestCompleteTask(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	created, _ := svc.CreateTask(UserTask{
		UserID: "user1",
		Title:  "Complete me",
	})

	err := svc.CompleteTask(created.ID)
	if err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}

	got, _ := svc.GetTask(created.ID)
	if got.Status != "done" {
		t.Errorf("expected status 'done', got %q", got.Status)
	}
	if got.CompletedAt == "" {
		t.Error("expected non-empty CompletedAt")
	}
}

func TestCompleteTask_WithSubtasks(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	parent, _ := svc.CreateTask(UserTask{
		UserID: "user1",
		Title:  "Parent task",
	})

	// Create subtasks.
	sub1, _ := svc.CreateTask(UserTask{
		UserID:   "user1",
		Title:    "Subtask 1",
		ParentID: parent.ID,
	})
	sub2, _ := svc.CreateTask(UserTask{
		UserID:   "user1",
		Title:    "Subtask 2",
		ParentID: parent.ID,
	})

	// Create a nested subtask.
	nested, _ := svc.CreateTask(UserTask{
		UserID:   "user1",
		Title:    "Nested subtask",
		ParentID: sub1.ID,
	})

	// Complete parent should cascade.
	err := svc.CompleteTask(parent.ID)
	if err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}

	// Verify all are completed.
	for _, id := range []string{parent.ID, sub1.ID, sub2.ID, nested.ID} {
		got, _ := svc.GetTask(id)
		if got.Status != "done" {
			t.Errorf("task %s: expected 'done', got %q", id, got.Status)
		}
	}
}

func TestCompleteTask_SkipsCancelledSubtasks(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	parent, _ := svc.CreateTask(UserTask{
		UserID: "user1",
		Title:  "Parent",
	})
	sub, _ := svc.CreateTask(UserTask{
		UserID:   "user1",
		Title:    "Cancelled subtask",
		ParentID: parent.ID,
		Status:   "cancelled",
	})

	err := svc.CompleteTask(parent.ID)
	if err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}

	got, _ := svc.GetTask(sub.ID)
	if got.Status != "cancelled" {
		t.Errorf("expected cancelled subtask to stay 'cancelled', got %q", got.Status)
	}
}

func TestListTasks(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	for i := 0; i < 5; i++ {
		svc.CreateTask(UserTask{
			UserID:   "user1",
			Title:    "Task " + string(rune('A'+i)),
			Priority: (i % 4) + 1,
		})
	}
	// Task from different user.
	svc.CreateTask(UserTask{
		UserID: "user2",
		Title:  "Other user task",
	})

	tasks, err := svc.ListTasks("user1", TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 5 {
		t.Errorf("expected 5 tasks, got %d", len(tasks))
	}
}

func TestListTasks_WithFilters(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	svc.CreateTask(UserTask{UserID: "u1", Title: "Todo 1", Status: "todo", Project: "work", Priority: 1})
	svc.CreateTask(UserTask{UserID: "u1", Title: "Todo 2", Status: "todo", Project: "personal"})
	svc.CreateTask(UserTask{UserID: "u1", Title: "Done 1", Status: "done", Project: "work"})
	svc.CreateTask(UserTask{UserID: "u1", Title: "In Prog", Status: "in_progress", Project: "work"})

	// Filter by status.
	tasks, _ := svc.ListTasks("u1", TaskFilter{Status: "todo"})
	if len(tasks) != 2 {
		t.Errorf("status filter: expected 2, got %d", len(tasks))
	}

	// Filter by project.
	tasks, _ = svc.ListTasks("u1", TaskFilter{Project: "work"})
	if len(tasks) != 3 {
		t.Errorf("project filter: expected 3, got %d", len(tasks))
	}

	// Filter by priority.
	tasks, _ = svc.ListTasks("u1", TaskFilter{Priority: 1})
	if len(tasks) != 1 {
		t.Errorf("priority filter: expected 1, got %d", len(tasks))
	}

	// Filter by limit.
	tasks, _ = svc.ListTasks("u1", TaskFilter{Limit: 2})
	if len(tasks) != 2 {
		t.Errorf("limit filter: expected 2, got %d", len(tasks))
	}
}

func TestListTasks_DueDateFilter(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	tomorrow := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	nextWeek := time.Now().UTC().Add(7 * 24 * time.Hour).Format(time.RFC3339)

	svc.CreateTask(UserTask{UserID: "u1", Title: "Due tomorrow", DueAt: tomorrow})
	svc.CreateTask(UserTask{UserID: "u1", Title: "Due next week", DueAt: nextWeek})
	svc.CreateTask(UserTask{UserID: "u1", Title: "No due date"})

	// Filter by due date (before 3 days from now).
	cutoff := time.Now().UTC().Add(3 * 24 * time.Hour).Format(time.RFC3339)
	tasks, _ := svc.ListTasks("u1", TaskFilter{DueDate: cutoff})
	if len(tasks) != 1 {
		t.Errorf("due date filter: expected 1, got %d", len(tasks))
	}
}

func TestListTasks_TagFilter(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	svc.CreateTask(UserTask{UserID: "u1", Title: "Tagged", Tags: []string{"important", "work"}})
	svc.CreateTask(UserTask{UserID: "u1", Title: "Other tag", Tags: []string{"personal"}})

	tasks, _ := svc.ListTasks("u1", TaskFilter{Tag: "important"})
	if len(tasks) != 1 {
		t.Errorf("tag filter: expected 1, got %d", len(tasks))
	}
}

func TestGetSubtasks(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	parent, _ := svc.CreateTask(UserTask{UserID: "u1", Title: "Parent"})
	svc.CreateTask(UserTask{UserID: "u1", Title: "Sub1", ParentID: parent.ID, SortOrder: 1})
	svc.CreateTask(UserTask{UserID: "u1", Title: "Sub2", ParentID: parent.ID, SortOrder: 2})

	subs, err := svc.GetSubtasks(parent.ID)
	if err != nil {
		t.Fatalf("GetSubtasks: %v", err)
	}
	if len(subs) != 2 {
		t.Errorf("expected 2 subtasks, got %d", len(subs))
	}
	if subs[0].Title != "Sub1" {
		t.Errorf("expected first subtask 'Sub1', got %q", subs[0].Title)
	}
}

func TestGetSubtasks_Empty(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	parent, _ := svc.CreateTask(UserTask{UserID: "u1", Title: "No subs"})
	subs, err := svc.GetSubtasks(parent.ID)
	if err != nil {
		t.Fatalf("GetSubtasks: %v", err)
	}
	if len(subs) != 0 {
		t.Errorf("expected 0 subtasks, got %d", len(subs))
	}
}

func TestCreateProject(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	proj, err := svc.CreateProject("user1", "Work", "Work-related tasks")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if proj.ID == "" {
		t.Error("expected non-empty ID")
	}
	if proj.Name != "Work" {
		t.Errorf("expected name 'Work', got %q", proj.Name)
	}
}

func TestCreateProject_Duplicate(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	_, err := svc.CreateProject("user1", "Work", "")
	if err != nil {
		t.Fatalf("first CreateProject: %v", err)
	}

	_, err = svc.CreateProject("user1", "Work", "duplicate")
	if err == nil {
		t.Fatal("expected error for duplicate project name")
	}
}

func TestListProjects(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	svc.CreateProject("user1", "Alpha", "")
	svc.CreateProject("user1", "Beta", "")
	svc.CreateProject("user2", "Gamma", "")

	projs, err := svc.ListProjects("user1")
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projs) != 2 {
		t.Errorf("expected 2 projects, got %d", len(projs))
	}
	// Should be sorted by name.
	if projs[0].Name != "Alpha" {
		t.Errorf("expected first project 'Alpha', got %q", projs[0].Name)
	}
}

func TestDecomposeTask(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	parent, _ := svc.CreateTask(UserTask{
		UserID:   "user1",
		Title:    "Plan vacation",
		Project:  "personal",
		Priority: 2,
		Tags:     []string{"travel"},
	})

	subtitles := []string{"Book flights", "Reserve hotel", "Plan activities"}
	subs, err := svc.DecomposeTask(parent.ID, subtitles)
	if err != nil {
		t.Fatalf("DecomposeTask: %v", err)
	}
	if len(subs) != 3 {
		t.Errorf("expected 3 subtasks, got %d", len(subs))
	}

	// Verify subtask properties inherited from parent.
	for i, sub := range subs {
		if sub.ParentID != parent.ID {
			t.Errorf("subtask %d: parent_id mismatch", i)
		}
		if sub.Project != "personal" {
			t.Errorf("subtask %d: project should be 'personal', got %q", i, sub.Project)
		}
		if sub.Priority != 2 {
			t.Errorf("subtask %d: priority should be 2, got %d", i, sub.Priority)
		}
		if sub.SortOrder != i+1 {
			t.Errorf("subtask %d: sort_order should be %d, got %d", i, i+1, sub.SortOrder)
		}
	}

	// Parent should now be in_progress.
	updatedParent, _ := svc.GetTask(parent.ID)
	if updatedParent.Status != "in_progress" {
		t.Errorf("expected parent status 'in_progress', got %q", updatedParent.Status)
	}
}

func TestDecomposeTask_NonexistentParent(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	_, err := svc.DecomposeTask("nonexistent", []string{"sub1"})
	if err == nil {
		t.Fatal("expected error for nonexistent parent")
	}
}

func TestGenerateReview(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	now := time.Now().UTC().Format(time.RFC3339)
	past := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	yesterday := time.Now().UTC().Add(-25 * time.Hour).Format(time.RFC3339)

	// Create tasks with various states.
	svc.CreateTask(UserTask{UserID: "u1", Title: "Done recently", Status: "done", Project: "work"})
	// Manually set completed_at to recent time.
	tasks, _ := svc.ListTasks("u1", TaskFilter{Status: "done"})
	if len(tasks) > 0 {
		svc.UpdateTask(tasks[0].ID, map[string]any{"status": "done"})
		// Manually set completed_at via raw SQL.
		setCompleted := fmt.Sprintf(`UPDATE user_tasks SET completed_at = '%s' WHERE id = '%s';`,
			escapeSQLite(past), escapeSQLite(tasks[0].ID))
		exec.Command("sqlite3", svc.dbPath, setCompleted).Run()
	}

	svc.CreateTask(UserTask{UserID: "u1", Title: "In progress", Status: "in_progress", Project: "work"})
	svc.CreateTask(UserTask{UserID: "u1", Title: "Todo 1", Status: "todo", Project: "personal"})
	svc.CreateTask(UserTask{UserID: "u1", Title: "Overdue", Status: "todo", DueAt: yesterday, Project: "work"})

	_ = now // used via time checks in the review

	review, err := svc.GenerateReview("u1", "daily")
	if err != nil {
		t.Fatalf("GenerateReview: %v", err)
	}
	if review.Period != "daily" {
		t.Errorf("expected period 'daily', got %q", review.Period)
	}
	if review.InProgress != 1 {
		t.Errorf("expected 1 in_progress, got %d", review.InProgress)
	}
	if review.Pending < 2 {
		t.Errorf("expected at least 2 pending, got %d", review.Pending)
	}
	if review.Overdue < 1 {
		t.Errorf("expected at least 1 overdue, got %d", review.Overdue)
	}
	if len(review.TopProjects) == 0 {
		t.Error("expected at least 1 top project")
	}
}

func TestGenerateReview_Weekly(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	svc.CreateTask(UserTask{UserID: "u1", Title: "Weekly task", Status: "todo"})

	review, err := svc.GenerateReview("u1", "weekly")
	if err != nil {
		t.Fatalf("GenerateReview weekly: %v", err)
	}
	if review.Period != "weekly" {
		t.Errorf("expected period 'weekly', got %q", review.Period)
	}
}

func TestTaskFromRow(t *testing.T) {
	row := map[string]any{
		"id":              "test-id",
		"user_id":         "user1",
		"title":           "Test",
		"description":     "desc",
		"project":         "inbox",
		"status":          "todo",
		"priority":        float64(2),
		"due_at":          "",
		"parent_id":       "",
		"tags":            `["a","b"]`,
		"source_channel":  "telegram",
		"external_id":     "",
		"external_source": "",
		"sort_order":      float64(0),
		"created_at":      "2026-01-01T00:00:00Z",
		"updated_at":      "2026-01-01T00:00:00Z",
		"completed_at":    "",
	}

	task := taskFromRow(row)
	if task.ID != "test-id" {
		t.Errorf("expected ID 'test-id', got %q", task.ID)
	}
	if len(task.Tags) != 2 {
		t.Errorf("expected 2 tags, got %d", len(task.Tags))
	}
	if task.Tags[0] != "a" || task.Tags[1] != "b" {
		t.Errorf("expected tags [a,b], got %v", task.Tags)
	}
}

func TestTaskFieldToColumn(t *testing.T) {
	tests := []struct {
		field  string
		column string
	}{
		{"title", "title"},
		{"dueAt", "due_at"},
		{"parentId", "parent_id"},
		{"sortOrder", "sort_order"},
		{"unknown", ""},
	}
	for _, tt := range tests {
		got := taskFieldToColumn(tt.field)
		if got != tt.column {
			t.Errorf("taskFieldToColumn(%q) = %q, want %q", tt.field, got, tt.column)
		}
	}
}

func TestDefaultProjectOrInbox(t *testing.T) {
	cfg := TaskManagerConfig{}
	if cfg.defaultProjectOrInbox() != "inbox" {
		t.Errorf("expected 'inbox', got %q", cfg.defaultProjectOrInbox())
	}

	cfg.DefaultProject = "work"
	if cfg.defaultProjectOrInbox() != "work" {
		t.Errorf("expected 'work', got %q", cfg.defaultProjectOrInbox())
	}
}

func TestCreateTaskPriorityValidation(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	// Priority 0 should default to 2.
	created, _ := svc.CreateTask(UserTask{UserID: "u1", Title: "No priority"})
	got, _ := svc.GetTask(created.ID)
	if got.Priority != 2 {
		t.Errorf("expected default priority 2, got %d", got.Priority)
	}

	// Priority 5 (out of range) should default to 2.
	created, _ = svc.CreateTask(UserTask{UserID: "u1", Title: "Bad priority", Priority: 5})
	got, _ = svc.GetTask(created.ID)
	if got.Priority != 2 {
		t.Errorf("expected default priority 2 for out-of-range, got %d", got.Priority)
	}
}

func TestToolTaskCreate_JSONOutput(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	f, _ := os.Create(dbPath)
	f.Close()
	initTaskManagerDB(dbPath)

	cfg := &Config{HistoryDB: dbPath}
	old := globalTaskManager
	globalTaskManager = newTaskManagerService(cfg)
	defer func() { globalTaskManager = old }()

	input, _ := json.Marshal(map[string]any{
		"title":  "Test tool create",
		"userId": "tool-user",
	})
	result, err := toolTaskCreate(nil, cfg, input)
	if err != nil {
		t.Fatalf("toolTaskCreate: %v", err)
	}

	var task UserTask
	if err := json.Unmarshal([]byte(result), &task); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if task.Title != "Test tool create" {
		t.Errorf("expected title 'Test tool create', got %q", task.Title)
	}
}

func TestToolTaskCreate_WithDecompose(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	f, _ := os.Create(dbPath)
	f.Close()
	initTaskManagerDB(dbPath)

	cfg := &Config{HistoryDB: dbPath}
	old := globalTaskManager
	globalTaskManager = newTaskManagerService(cfg)
	defer func() { globalTaskManager = old }()

	input, _ := json.Marshal(map[string]any{
		"title":     "Big task",
		"userId":    "u1",
		"decompose": true,
		"subtasks":  []string{"Step 1", "Step 2"},
	})
	result, err := toolTaskCreate(nil, cfg, input)
	if err != nil {
		t.Fatalf("toolTaskCreate with decompose: %v", err)
	}

	var out struct {
		Task     UserTask   `json:"task"`
		Subtasks []UserTask `json:"subtasks"`
	}
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("unmarshal decompose result: %v", err)
	}
	if len(out.Subtasks) != 2 {
		t.Errorf("expected 2 subtasks, got %d", len(out.Subtasks))
	}
}

func TestToolTaskCreate_MissingTitle(t *testing.T) {
	old := globalTaskManager
	globalTaskManager = &TaskManagerService{}
	defer func() { globalTaskManager = old }()

	input, _ := json.Marshal(map[string]any{})
	_, err := toolTaskCreate(nil, &Config{}, input)
	if err == nil {
		t.Fatal("expected error for missing title")
	}
}

func TestToolTaskCreate_NotInitialized(t *testing.T) {
	old := globalTaskManager
	globalTaskManager = nil
	defer func() { globalTaskManager = old }()

	input, _ := json.Marshal(map[string]any{"title": "test"})
	_, err := toolTaskCreate(nil, &Config{}, input)
	if err == nil {
		t.Fatal("expected error when not initialized")
	}
}
