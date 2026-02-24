package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// --- Todoist Sync Tests ---

func TestNewTodoistSync(t *testing.T) {
	cfg := &Config{HistoryDB: "/tmp/test.db"}
	ts := newTodoistSync(cfg)
	if ts == nil {
		t.Fatal("expected non-nil TodoistSync")
	}
	if ts.dbPath != "/tmp/test.db" {
		t.Errorf("expected dbPath '/tmp/test.db', got %q", ts.dbPath)
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
		got := todoistPriorityToLocal(tt.todoist)
		if got != tt.local {
			t.Errorf("todoistPriorityToLocal(%d) = %d, want %d", tt.todoist, got, tt.local)
		}
		back := localPriorityToTodoist(tt.local)
		if back != tt.todoist {
			t.Errorf("localPriorityToTodoist(%d) = %d, want %d", tt.local, back, tt.todoist)
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

	origBase := todoistAPIBase
	todoistAPIBase = srv.URL
	defer func() { todoistAPIBase = origBase }()

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
	tasks, _ := globalTaskManager.ListTasks("user1", TaskFilter{})
	if len(tasks) != 2 {
		t.Errorf("expected 2 tasks in DB, got %d", len(tasks))
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
	if ns.dbPath != "/tmp/test.db" {
		t.Errorf("expected dbPath '/tmp/test.db', got %q", ns.dbPath)
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

	origBase := notionAPIBase
	notionAPIBase = srv.URL
	defer func() { notionAPIBase = origBase }()

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
		notion string
		local  string
	}{
		{"Done", "done"},
		{"Complete", "done"},
		{"In Progress", "in_progress"},
		{"Doing", "in_progress"},
		{"Cancelled", "cancelled"},
		{"To Do", "todo"},
		{"Unknown", "todo"},
	}
	for _, tt := range tests {
		page := notionPage{}
		page.Properties.Status.Select = &struct {
			Name string `json:"name"`
		}{Name: tt.notion}
		got := notionStatusToLocal(page)
		if got != tt.local {
			t.Errorf("notionStatusToLocal(%q) = %q, want %q", tt.notion, got, tt.local)
		}
	}
}

func TestNotionPriorityMapping(t *testing.T) {
	tests := []struct {
		notion string
		local  int
	}{
		{"Urgent", 1},
		{"High", 2},
		{"Medium", 3},
		{"Low", 4},
		{"P1", 1},
		{"P4", 4},
	}
	for _, tt := range tests {
		page := notionPage{}
		page.Properties.Priority.Select = &struct {
			Name string `json:"name"`
		}{Name: tt.notion}
		got := notionPriorityToLocal(page)
		if got != tt.local {
			t.Errorf("notionPriorityToLocal(%q) = %d, want %d", tt.notion, got, tt.local)
		}
	}
}

func TestLocalStatusToNotion(t *testing.T) {
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
		got := localStatusToNotion(tt.local)
		if got != tt.notion {
			t.Errorf("localStatusToNotion(%q) = %q, want %q", tt.local, got, tt.notion)
		}
	}
}

func TestLocalPriorityToNotion(t *testing.T) {
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
		got := localPriorityToNotion(tt.local)
		if got != tt.notion {
			t.Errorf("localPriorityToNotion(%d) = %q, want %q", tt.local, got, tt.notion)
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
	svc := newTaskManagerService(cfg)

	// Create a task with external ID.
	svc.CreateTask(UserTask{
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
