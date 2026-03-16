package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"tetora/internal/life/tasks"
)

// --- Todoist Sync Tests ---

func TestNewTodoistSync(t *testing.T) {
	cfg := &Config{HistoryDB: "/tmp/test.db"}
	ts := newTodoistSync(cfg)
	if ts == nil {
		t.Fatal("expected non-nil TodoistSync")
	}
}

func TestTodoistPriorityConversion(t *testing.T) {
	tests := []struct {
		todoist int
		local   int
	}{
		{4, 1}, // urgent
		{3, 2}, // high
		{2, 3}, // medium
		{1, 4}, // normal
	}
	for _, tt := range tests {
		got := tasks.TodoistPriorityToLocal(tt.todoist)
		if got != tt.local {
			t.Errorf("TodoistPriorityToLocal(%d) = %d, want %d", tt.todoist, got, tt.local)
		}
		back := tasks.LocalPriorityToTodoist(tt.local)
		if back != tt.todoist {
			t.Errorf("LocalPriorityToTodoist(%d) = %d, want %d", tt.local, back, tt.todoist)
		}
	}
}

func TestTodoistPullTasks_NoAPIKey(t *testing.T) {
	cfg := &Config{HistoryDB: "/tmp/test.db"}
	ts := newTodoistSync(cfg)
	_, err := ts.PullTasks("user1")
	if err == nil {
		t.Fatal("expected error when API key is missing")
	}
}

func TestTodoistPullTasks_MockServer(t *testing.T) {
	// Set up test DB.
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	f, _ := os.Create(dbPath)
	f.Close()
	initTaskManagerDB(dbPath)

	// Mock Todoist API.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			w.WriteHeader(401)
			return
		}
		json.NewEncoder(w).Encode([]TodoistTask{
			{
				ID:      "td-1",
				Content: "Test Todoist Task",
				Priority: 4,
				Due: &struct {
					Date string `json:"date"`
				}{Date: "2026-03-01"},
			},
			{
				ID:          "td-2",
				Content:     "Completed task",
				IsCompleted: true,
			},
		})
	}))
	defer srv.Close()

	origBase := tasks.TodoistAPIBase
	tasks.TodoistAPIBase = srv.URL
	defer func() { tasks.TodoistAPIBase = origBase }()

	cfg := &Config{
		HistoryDB: dbPath,
		TaskManager: TaskManagerConfig{
			Enabled: true,
			Todoist: TodoistConfig{
				Enabled: true,
				APIKey:  "test-key",
			},
		},
	}

	oldMgr := globalTaskManager
	globalTaskManager = newTaskManagerService(cfg)
	defer func() { globalTaskManager = oldMgr }()

	ts := newTodoistSync(cfg)
	pulled, err := ts.PullTasks("user1")
	if err != nil {
		t.Fatalf("PullTasks: %v", err)
	}
	if pulled != 2 {
		t.Errorf("expected 2 pulled, got %d", pulled)
	}

	// Verify tasks were created.
	taskList, _ := globalTaskManager.ListTasks("user1", TaskFilter{})
	if len(taskList) != 2 {
		t.Errorf("expected 2 tasks in DB, got %d", len(taskList))
	}
}

func TestTodoistPushTask_NoAPIKey(t *testing.T) {
	cfg := &Config{HistoryDB: "/tmp/test.db"}
	ts := newTodoistSync(cfg)
	err := ts.PushTask(UserTask{Title: "test"})
	if err == nil {
		t.Fatal("expected error when API key is missing")
	}
}

func TestTodoistSyncAll_NoAPIKey(t *testing.T) {
	cfg := &Config{HistoryDB: "/tmp/test.db"}
	ts := newTodoistSync(cfg)
	_, _, err := ts.SyncAll("user1")
	if err == nil {
		t.Fatal("expected error when API key is missing")
	}
}

func TestToolTodoistSync_NotEnabled(t *testing.T) {
	cfg := &Config{}
	input, _ := json.Marshal(map[string]string{"action": "sync"})
	_, err := toolTodoistSync(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error when not enabled")
	}
}

func TestToolTodoistSync_UnknownAction(t *testing.T) {
	cfg := &Config{
		TaskManager: TaskManagerConfig{
			Todoist: TodoistConfig{Enabled: true, APIKey: "key"},
		},
	}
	input, _ := json.Marshal(map[string]string{"action": "invalid"})
	_, err := toolTodoistSync(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
}

// --- Notion Sync Tests ---

func TestNewNotionSync(t *testing.T) {
	cfg := &Config{HistoryDB: "/tmp/test.db"}
	ns := newNotionSync(cfg)
	if ns == nil {
		t.Fatal("expected non-nil NotionSync")
	}
}

func TestNotionPullTasks_NoAPIKey(t *testing.T) {
	cfg := &Config{HistoryDB: "/tmp/test.db"}
	ns := newNotionSync(cfg)
	_, err := ns.PullTasks("user1")
	if err == nil {
		t.Fatal("expected error when API key is missing")
	}
}

func TestNotionPullTasks_NoDatabaseID(t *testing.T) {
	cfg := &Config{
		HistoryDB: "/tmp/test.db",
		TaskManager: TaskManagerConfig{
			Notion: NotionConfig{APIKey: "key"},
		},
	}
	ns := newNotionSync(cfg)
	_, err := ns.PullTasks("user1")
	if err == nil {
		t.Fatal("expected error when database ID is missing")
	}
}

func TestNotionPullTasks_MockServer(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	f, _ := os.Create(dbPath)
	f.Close()
	initTaskManagerDB(dbPath)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer notion-key" {
			w.WriteHeader(401)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{
					"id": "notion-page-1",
					"properties": map[string]any{
						"Name": map[string]any{
							"title": []map[string]any{
								{"plain_text": "Notion Task 1"},
							},
						},
						"Status": map[string]any{
							"select": map[string]any{"name": "To Do"},
						},
						"Priority": map[string]any{
							"select": map[string]any{"name": "High"},
						},
						"Due Date": map[string]any{
							"date": map[string]any{"start": "2026-04-01"},
						},
					},
				},
			},
		})
	}))
	defer srv.Close()

	origBase := tasks.NotionAPIBase
	tasks.NotionAPIBase = srv.URL
	defer func() { tasks.NotionAPIBase = origBase }()

	cfg := &Config{
		HistoryDB: dbPath,
		TaskManager: TaskManagerConfig{
			Enabled: true,
			Notion: NotionConfig{
				Enabled:    true,
				APIKey:     "notion-key",
				DatabaseID: "db-123",
			},
		},
	}

	oldMgr := globalTaskManager
	globalTaskManager = newTaskManagerService(cfg)
	defer func() { globalTaskManager = oldMgr }()

	ns := newNotionSync(cfg)
	pulled, err := ns.PullTasks("user1")
	if err != nil {
		t.Fatalf("PullTasks: %v", err)
	}
	if pulled != 1 {
		t.Errorf("expected 1 pulled, got %d", pulled)
	}
}

func TestNotionPushTask_NoAPIKey(t *testing.T) {
	cfg := &Config{HistoryDB: "/tmp/test.db"}
	ns := newNotionSync(cfg)
	err := ns.PushTask(UserTask{Title: "test"})
	if err == nil {
		t.Fatal("expected error when API key is missing")
	}
}

func TestNotionSyncAll_NoAPIKey(t *testing.T) {
	cfg := &Config{HistoryDB: "/tmp/test.db"}
	ns := newNotionSync(cfg)
	_, _, err := ns.SyncAll("user1")
	if err == nil {
		t.Fatal("expected error when API key is missing")
	}
}

func TestToolNotionSync_NotEnabled(t *testing.T) {
	cfg := &Config{}
	input, _ := json.Marshal(map[string]string{"action": "sync"})
	_, err := toolNotionSync(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error when not enabled")
	}
}

func TestToolNotionSync_UnknownAction(t *testing.T) {
	cfg := &Config{
		TaskManager: TaskManagerConfig{
			Notion: NotionConfig{Enabled: true, APIKey: "key", DatabaseID: "db"},
		},
	}
	input, _ := json.Marshal(map[string]string{"action": "invalid"})
	_, err := toolNotionSync(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
}

// --- Notion Status/Priority Mapping Tests ---

func TestNotionStatusMapping(t *testing.T) {
	tests := []struct {
		local  string
		notion string
	}{
		{"todo", "To Do"},
		{"in_progress", "In Progress"},
		{"done", "Done"},
		{"cancelled", "Cancelled"},
		{"unknown", ""},
	}
	for _, tt := range tests {
		got := tasks.LocalStatusToNotion(tt.local)
		if got != tt.notion {
			t.Errorf("LocalStatusToNotion(%q) = %q, want %q", tt.local, got, tt.notion)
		}
	}
}

func TestNotionPriorityMapping(t *testing.T) {
	tests := []struct {
		local  int
		notion string
	}{
		{1, "Urgent"},
		{2, "High"},
		{3, "Medium"},
		{4, "Low"},
		{0, ""},
	}
	for _, tt := range tests {
		got := tasks.LocalPriorityToNotion(tt.local)
		if got != tt.notion {
			t.Errorf("LocalPriorityToNotion(%d) = %q, want %q", tt.local, got, tt.notion)
		}
	}
}

// --- findTaskByExternalID Tests ---

func TestFindTaskByExternalID(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	f, _ := os.Create(dbPath)
	f.Close()
	initTaskManagerDB(dbPath)

	cfg := &Config{HistoryDB: dbPath}
	oldMgr := globalTaskManager
	globalTaskManager = newTaskManagerService(cfg)
	defer func() { globalTaskManager = oldMgr }()

	// Create a task with external ID.
	globalTaskManager.CreateTask(UserTask{
		UserID:         "u1",
		Title:          "Synced task",
		ExternalID:     "ext-123",
		ExternalSource: "todoist",
	})

	// Find it.
	found, err := findTaskByExternalID(dbPath, "todoist", "ext-123")
	if err != nil {
		t.Fatalf("findTaskByExternalID: %v", err)
	}
	if found == nil {
		t.Fatal("expected to find task")
	}
	if found.Title != "Synced task" {
		t.Errorf("expected title 'Synced task', got %q", found.Title)
	}

	// Not found.
	notFound, _ := findTaskByExternalID(dbPath, "todoist", "nonexistent")
	if notFound != nil {
		t.Error("expected nil for nonexistent external ID")
	}
}
