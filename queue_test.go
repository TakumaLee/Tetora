package main

import (
	"encoding/json"
	"os"
	osexec "os/exec"
	"path/filepath"
	"testing"
	"time"
)

func tempQueueDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_queue.db")
	if err := initQueueDB(dbPath); err != nil {
		t.Fatalf("initQueueDB: %v", err)
	}
	return dbPath
}

func TestInitQueueDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	if err := initQueueDB(dbPath); err != nil {
		t.Fatalf("initQueueDB: %v", err)
	}
	// Verify file was created.
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("db file not created: %v", err)
	}
	// Idempotent: calling again should not error.
	if err := initQueueDB(dbPath); err != nil {
		t.Fatalf("initQueueDB second call: %v", err)
	}
}

func TestEnqueueDequeue(t *testing.T) {
	dbPath := tempQueueDB(t)

	task := Task{
		ID:     "test-id-1",
		Name:   "test-task",
		Prompt: "hello world",
		Source: "test",
	}

	// Enqueue.
	if err := enqueueTask(dbPath, task, "翡翠", 0); err != nil {
		t.Fatalf("enqueueTask: %v", err)
	}

	// Verify it's in the queue.
	items := queryQueue(dbPath, "pending")
	if len(items) != 1 {
		t.Fatalf("expected 1 pending item, got %d", len(items))
	}
	if items[0].RoleName != "翡翠" {
		t.Errorf("role = %q, want %q", items[0].RoleName, "翡翠")
	}
	if items[0].Source != "test" {
		t.Errorf("source = %q, want %q", items[0].Source, "test")
	}

	// Dequeue.
	item := dequeueNext(dbPath)
	if item == nil {
		t.Fatal("dequeueNext returned nil")
	}
	if item.Status != "processing" {
		t.Errorf("status = %q, want %q", item.Status, "processing")
	}

	// Deserialize task.
	var got Task
	if err := json.Unmarshal([]byte(item.TaskJSON), &got); err != nil {
		t.Fatalf("unmarshal task: %v", err)
	}
	if got.Name != "test-task" {
		t.Errorf("task name = %q, want %q", got.Name, "test-task")
	}
	if got.Prompt != "hello world" {
		t.Errorf("task prompt = %q, want %q", got.Prompt, "hello world")
	}

	// Queue should now be empty for pending.
	if next := dequeueNext(dbPath); next != nil {
		t.Error("expected nil after dequeue, got item")
	}
}

func TestDequeueOrder(t *testing.T) {
	dbPath := tempQueueDB(t)

	// Enqueue 3 items: low priority, high priority, normal priority.
	enqueueTask(dbPath, Task{Name: "low", Source: "test"}, "", 0)
	enqueueTask(dbPath, Task{Name: "high", Source: "test"}, "", 10)
	enqueueTask(dbPath, Task{Name: "normal", Source: "test"}, "", 5)

	// Should dequeue in priority order: high → normal → low.
	item1 := dequeueNext(dbPath)
	if item1 == nil || !taskNameFromJSON(item1.TaskJSON, "high") {
		t.Errorf("first dequeue should be 'high', got %v", taskNameFromQueueItem(item1))
	}

	item2 := dequeueNext(dbPath)
	if item2 == nil || !taskNameFromJSON(item2.TaskJSON, "normal") {
		t.Errorf("second dequeue should be 'normal', got %v", taskNameFromQueueItem(item2))
	}

	item3 := dequeueNext(dbPath)
	if item3 == nil || !taskNameFromJSON(item3.TaskJSON, "low") {
		t.Errorf("third dequeue should be 'low', got %v", taskNameFromQueueItem(item3))
	}
}

func TestCleanupExpired(t *testing.T) {
	dbPath := tempQueueDB(t)

	// Enqueue an item with a fake old timestamp.
	task := Task{Name: "old-task", Source: "test"}
	taskBytes, _ := json.Marshal(task)
	oldTime := time.Now().Add(-2 * time.Hour).Format(time.RFC3339)

	sql := "INSERT INTO offline_queue (task_json, role, source, priority, status, retry_count, created_at, updated_at) " +
		"VALUES ('" + escapeSQLite(string(taskBytes)) + "','','test',0,'pending',0,'" + oldTime + "','" + oldTime + "')"
	execSQL(dbPath, sql)

	// Enqueue a recent item.
	enqueueTask(dbPath, Task{Name: "new-task", Source: "test"}, "", 0)

	// Cleanup with 1h TTL — should expire the old one.
	expired := cleanupExpiredQueue(dbPath, 1*time.Hour)
	if expired != 1 {
		t.Errorf("expired = %d, want 1", expired)
	}

	// Only new item should be pending.
	pending := queryQueue(dbPath, "pending")
	if len(pending) != 1 {
		t.Fatalf("pending = %d, want 1", len(pending))
	}

	// Old item should be marked expired.
	expiredItems := queryQueue(dbPath, "expired")
	if len(expiredItems) != 1 {
		t.Fatalf("expired items = %d, want 1", len(expiredItems))
	}
}

func TestQueueMaxItems(t *testing.T) {
	dbPath := tempQueueDB(t)

	// Fill queue to max (use small max for test).
	maxItems := 3
	for i := 0; i < maxItems; i++ {
		enqueueTask(dbPath, Task{Name: "task", Source: "test"}, "", 0)
	}

	if !isQueueFull(dbPath, maxItems) {
		t.Error("expected queue to be full")
	}
	if isQueueFull(dbPath, maxItems+1) {
		t.Error("expected queue to not be full at maxItems+1")
	}
}

func TestIsAllProvidersUnavailable(t *testing.T) {
	tests := []struct {
		err  string
		want bool
	}{
		{"all providers unavailable", true},
		{"All Providers Unavailable", true},
		{"provider claude: connection refused", false},
		{"timeout", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isAllProvidersUnavailable(tt.err); got != tt.want {
			t.Errorf("isAllProvidersUnavailable(%q) = %v, want %v", tt.err, got, tt.want)
		}
	}
}

func TestQueueItemQueryAndDelete(t *testing.T) {
	dbPath := tempQueueDB(t)

	enqueueTask(dbPath, Task{Name: "delete-me", Source: "test"}, "", 0)
	items := queryQueue(dbPath, "")
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	// Query by ID.
	item := queryQueueItem(dbPath, items[0].ID)
	if item == nil {
		t.Fatal("queryQueueItem returned nil")
	}

	// Delete.
	if err := deleteQueueItem(dbPath, item.ID); err != nil {
		t.Fatalf("deleteQueueItem: %v", err)
	}

	// Should be gone.
	if queryQueueItem(dbPath, item.ID) != nil {
		t.Error("item should be deleted")
	}
}

func TestUpdateQueueStatus(t *testing.T) {
	dbPath := tempQueueDB(t)

	enqueueTask(dbPath, Task{Name: "status-test", Source: "test"}, "", 0)
	items := queryQueue(dbPath, "pending")
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	updateQueueStatus(dbPath, items[0].ID, "failed", "some error")

	item := queryQueueItem(dbPath, items[0].ID)
	if item.Status != "failed" {
		t.Errorf("status = %q, want %q", item.Status, "failed")
	}
	if item.Error != "some error" {
		t.Errorf("error = %q, want %q", item.Error, "some error")
	}
}

func TestIncrementQueueRetry(t *testing.T) {
	dbPath := tempQueueDB(t)

	enqueueTask(dbPath, Task{Name: "retry-test", Source: "test"}, "", 0)
	items := queryQueue(dbPath, "pending")
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	incrementQueueRetry(dbPath, items[0].ID, "pending", "retry error")
	item := queryQueueItem(dbPath, items[0].ID)
	if item.RetryCount != 1 {
		t.Errorf("retryCount = %d, want 1", item.RetryCount)
	}

	incrementQueueRetry(dbPath, items[0].ID, "pending", "retry error 2")
	item = queryQueueItem(dbPath, items[0].ID)
	if item.RetryCount != 2 {
		t.Errorf("retryCount = %d, want 2", item.RetryCount)
	}
}

func TestCountPendingQueue(t *testing.T) {
	dbPath := tempQueueDB(t)

	if n := countPendingQueue(dbPath); n != 0 {
		t.Errorf("empty queue count = %d, want 0", n)
	}

	enqueueTask(dbPath, Task{Name: "t1", Source: "test"}, "", 0)
	enqueueTask(dbPath, Task{Name: "t2", Source: "test"}, "", 0)

	if n := countPendingQueue(dbPath); n != 2 {
		t.Errorf("count = %d, want 2", n)
	}

	// Dequeue one (status → processing, still counted).
	dequeueNext(dbPath)
	if n := countPendingQueue(dbPath); n != 2 {
		t.Errorf("count after dequeue = %d, want 2 (pending+processing)", n)
	}
}

func TestOfflineQueueConfigDefaults(t *testing.T) {
	// Zero value.
	var c OfflineQueueConfig
	if c.ttlOrDefault() != 1*time.Hour {
		t.Errorf("default TTL = %v, want 1h", c.ttlOrDefault())
	}
	if c.maxItemsOrDefault() != 100 {
		t.Errorf("default maxItems = %d, want 100", c.maxItemsOrDefault())
	}

	// Custom values.
	c = OfflineQueueConfig{TTL: "30m", MaxItems: 50}
	if c.ttlOrDefault() != 30*time.Minute {
		t.Errorf("custom TTL = %v, want 30m", c.ttlOrDefault())
	}
	if c.maxItemsOrDefault() != 50 {
		t.Errorf("custom maxItems = %d, want 50", c.maxItemsOrDefault())
	}
}

// --- Helpers ---

func execSQL(dbPath, sql string) {
	cmd := osexec.Command("sqlite3", dbPath, sql)
	cmd.CombinedOutput()
}

func taskNameFromJSON(taskJSON, expected string) bool {
	var t Task
	json.Unmarshal([]byte(taskJSON), &t)
	return t.Name == expected
}

func taskNameFromQueueItem(item *QueueItem) string {
	if item == nil {
		return "<nil>"
	}
	var t Task
	json.Unmarshal([]byte(item.TaskJSON), &t)
	return t.Name
}
