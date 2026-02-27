package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInitOpsDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_ops.db")

	// Create the DB file first.
	exec.Command("sqlite3", dbPath, "SELECT 1").Run()

	err := initOpsDB(dbPath)
	if err != nil {
		t.Fatalf("initOpsDB failed: %v", err)
	}

	// Verify tables exist.
	for _, table := range []string{"message_queue", "backup_log", "channel_status"} {
		rows, err := queryDB(dbPath, fmt.Sprintf("SELECT name FROM sqlite_master WHERE type='table' AND name='%s'", table))
		if err != nil {
			t.Fatalf("query table %s failed: %v", table, err)
		}
		if len(rows) == 0 {
			t.Errorf("table %s not created", table)
		}
	}

	// Verify index exists.
	rows, err := queryDB(dbPath, "SELECT name FROM sqlite_master WHERE type='index' AND name='idx_mq_status'")
	if err != nil {
		t.Fatalf("query index failed: %v", err)
	}
	if len(rows) == 0 {
		t.Error("index idx_mq_status not created")
	}

	// Idempotent — should not fail on second call.
	err = initOpsDB(dbPath)
	if err != nil {
		t.Fatalf("second initOpsDB failed: %v", err)
	}
}

func TestMessageQueue_Enqueue(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_mq.db")
	exec.Command("sqlite3", dbPath, "SELECT 1").Run()

	if err := initOpsDB(dbPath); err != nil {
		t.Fatalf("initOpsDB failed: %v", err)
	}

	cfg := &Config{
		HistoryDB: dbPath,
		Ops: OpsConfig{
			MessageQueue: MessageQueueConfig{
				Enabled:       true,
				RetryAttempts: 3,
				MaxQueueSize:  100,
			},
		},
	}

	mq := newMessageQueueEngine(cfg)

	// Enqueue a message.
	err := mq.Enqueue("telegram", "12345", "Hello World", 0)
	if err != nil {
		t.Fatalf("Enqueue failed: %v", err)
	}

	// Verify it's in the DB.
	rows, err := queryDB(dbPath, "SELECT channel, channel_target, message_text, status, priority FROM message_queue")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if fmt.Sprintf("%v", rows[0]["channel"]) != "telegram" {
		t.Errorf("expected channel=telegram, got %v", rows[0]["channel"])
	}
	if fmt.Sprintf("%v", rows[0]["status"]) != "pending" {
		t.Errorf("expected status=pending, got %v", rows[0]["status"])
	}

	// Test empty fields validation.
	err = mq.Enqueue("", "target", "text", 0)
	if err == nil {
		t.Error("expected error for empty channel")
	}
	err = mq.Enqueue("telegram", "", "text", 0)
	if err == nil {
		t.Error("expected error for empty target")
	}
	err = mq.Enqueue("telegram", "target", "", 0)
	if err == nil {
		t.Error("expected error for empty text")
	}
}

func TestMessageQueue_EnqueuePriority(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_mq_prio.db")
	exec.Command("sqlite3", dbPath, "SELECT 1").Run()

	if err := initOpsDB(dbPath); err != nil {
		t.Fatalf("initOpsDB failed: %v", err)
	}

	cfg := &Config{
		HistoryDB: dbPath,
		Ops: OpsConfig{
			MessageQueue: MessageQueueConfig{
				Enabled:      true,
				MaxQueueSize: 100,
			},
		},
	}

	mq := newMessageQueueEngine(cfg)

	// Enqueue with different priorities.
	mq.Enqueue("telegram", "user1", "Low priority", 0)
	mq.Enqueue("telegram", "user2", "High priority", 10)
	mq.Enqueue("telegram", "user3", "Medium priority", 5)

	// Verify order by priority DESC.
	rows, err := queryDB(dbPath, "SELECT channel_target, priority FROM message_queue WHERE status='pending' ORDER BY priority DESC, id ASC")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	if fmt.Sprintf("%v", rows[0]["channel_target"]) != "user2" {
		t.Errorf("expected high priority first, got %v", rows[0]["channel_target"])
	}
}

func TestMessageQueue_QueueSizeLimit(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_mq_limit.db")
	exec.Command("sqlite3", dbPath, "SELECT 1").Run()

	if err := initOpsDB(dbPath); err != nil {
		t.Fatalf("initOpsDB failed: %v", err)
	}

	cfg := &Config{
		HistoryDB: dbPath,
		Ops: OpsConfig{
			MessageQueue: MessageQueueConfig{
				Enabled:      true,
				MaxQueueSize: 3,
			},
		},
	}

	mq := newMessageQueueEngine(cfg)

	// Fill up the queue.
	for i := 0; i < 3; i++ {
		err := mq.Enqueue("telegram", fmt.Sprintf("user%d", i), "msg", 0)
		if err != nil {
			t.Fatalf("Enqueue %d failed: %v", i, err)
		}
	}

	// Next one should fail.
	err := mq.Enqueue("telegram", "overflow", "msg", 0)
	if err == nil {
		t.Error("expected queue full error")
	}
	if err != nil && !strings.Contains(err.Error(), "queue full") {
		t.Errorf("expected 'queue full' error, got: %v", err)
	}
}

func TestMessageQueue_ProcessQueue(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_mq_process.db")
	exec.Command("sqlite3", dbPath, "SELECT 1").Run()

	if err := initOpsDB(dbPath); err != nil {
		t.Fatalf("initOpsDB failed: %v", err)
	}

	cfg := &Config{
		HistoryDB: dbPath,
		Ops: OpsConfig{
			MessageQueue: MessageQueueConfig{
				Enabled:       true,
				RetryAttempts: 3,
				MaxQueueSize:  100,
			},
		},
	}

	mq := newMessageQueueEngine(cfg)

	// Enqueue messages.
	mq.Enqueue("telegram", "user1", "Hello", 0)
	mq.Enqueue("slack", "channel1", "World", 5)

	// Process the queue.
	ctx := context.Background()
	mq.ProcessQueue(ctx)

	// All should be sent (attemptDelivery succeeds by default).
	rows, err := queryDB(dbPath, "SELECT status FROM message_queue ORDER BY id")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	for _, row := range rows {
		status := fmt.Sprintf("%v", row["status"])
		if status != "sent" {
			t.Errorf("expected status=sent, got %s", status)
		}
	}
}

func TestMessageQueue_ProcessQueueWithFutureRetry(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_mq_future.db")
	exec.Command("sqlite3", dbPath, "SELECT 1").Run()

	if err := initOpsDB(dbPath); err != nil {
		t.Fatalf("initOpsDB failed: %v", err)
	}

	cfg := &Config{
		HistoryDB: dbPath,
		Ops: OpsConfig{
			MessageQueue: MessageQueueConfig{
				Enabled:       true,
				RetryAttempts: 3,
				MaxQueueSize:  100,
			},
		},
	}

	mq := newMessageQueueEngine(cfg)

	// Insert a message with future next_retry_at.
	now := time.Now().UTC().Format(time.RFC3339)
	future := time.Now().UTC().Add(1 * time.Hour).Format(time.RFC3339)
	sql := fmt.Sprintf(
		`INSERT INTO message_queue (channel, channel_target, message_text, priority, status, next_retry_at, created_at, updated_at) VALUES ('telegram', 'user1', 'test', 0, 'pending', '%s', '%s', '%s')`,
		future, now, now,
	)
	exec.Command("sqlite3", dbPath, sql).Run()

	// Process should not pick it up.
	ctx := context.Background()
	mq.ProcessQueue(ctx)

	rows, err := queryDB(dbPath, "SELECT status FROM message_queue")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if fmt.Sprintf("%v", rows[0]["status"]) != "pending" {
		t.Errorf("expected status=pending (future retry), got %v", rows[0]["status"])
	}
}

func TestChannelHealth_Record(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_ch.db")
	exec.Command("sqlite3", dbPath, "SELECT 1").Run()

	if err := initOpsDB(dbPath); err != nil {
		t.Fatalf("initOpsDB failed: %v", err)
	}

	// Record healthy.
	err := recordChannelHealth(dbPath, "telegram", "healthy", "")
	if err != nil {
		t.Fatalf("recordChannelHealth (healthy) failed: %v", err)
	}

	rows, err := queryDB(dbPath, "SELECT channel, status, failure_count FROM channel_status WHERE channel='telegram'")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if fmt.Sprintf("%v", rows[0]["status"]) != "healthy" {
		t.Errorf("expected status=healthy, got %v", rows[0]["status"])
	}
	if jsonInt(rows[0]["failure_count"]) != 0 {
		t.Errorf("expected failure_count=0, got %v", rows[0]["failure_count"])
	}

	// Record degraded.
	err = recordChannelHealth(dbPath, "telegram", "degraded", "connection timeout")
	if err != nil {
		t.Fatalf("recordChannelHealth (degraded) failed: %v", err)
	}

	rows, err = queryDB(dbPath, "SELECT status, failure_count, last_error FROM channel_status WHERE channel='telegram'")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if fmt.Sprintf("%v", rows[0]["status"]) != "degraded" {
		t.Errorf("expected status=degraded, got %v", rows[0]["status"])
	}
	if jsonInt(rows[0]["failure_count"]) != 1 {
		t.Errorf("expected failure_count=1, got %v", rows[0]["failure_count"])
	}

	// Record another failure.
	recordChannelHealth(dbPath, "telegram", "degraded", "timeout again")
	rows, _ = queryDB(dbPath, "SELECT failure_count FROM channel_status WHERE channel='telegram'")
	if jsonInt(rows[0]["failure_count"]) != 2 {
		t.Errorf("expected failure_count=2, got %v", rows[0]["failure_count"])
	}

	// Record healthy resets failure count.
	recordChannelHealth(dbPath, "telegram", "healthy", "")
	rows, _ = queryDB(dbPath, "SELECT failure_count FROM channel_status WHERE channel='telegram'")
	if jsonInt(rows[0]["failure_count"]) != 0 {
		t.Errorf("expected failure_count=0 after healthy, got %v", rows[0]["failure_count"])
	}
}

func TestChannelHealth_Get(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_ch_get.db")
	exec.Command("sqlite3", dbPath, "SELECT 1").Run()

	if err := initOpsDB(dbPath); err != nil {
		t.Fatalf("initOpsDB failed: %v", err)
	}

	// Record some channels.
	recordChannelHealth(dbPath, "telegram", "healthy", "")
	recordChannelHealth(dbPath, "slack", "degraded", "rate limited")
	recordChannelHealth(dbPath, "discord", "offline", "bot disconnected")

	channels, err := getChannelHealth(dbPath)
	if err != nil {
		t.Fatalf("getChannelHealth failed: %v", err)
	}
	if len(channels) != 3 {
		t.Fatalf("expected 3 channels, got %d", len(channels))
	}

	// Should be sorted by channel name.
	if channels[0].Channel != "discord" {
		t.Errorf("expected first channel=discord, got %s", channels[0].Channel)
	}
	if channels[1].Channel != "slack" {
		t.Errorf("expected second channel=slack, got %s", channels[1].Channel)
	}
	if channels[2].Channel != "telegram" {
		t.Errorf("expected third channel=telegram, got %s", channels[2].Channel)
	}
}

func TestQueueStats(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_stats.db")
	exec.Command("sqlite3", dbPath, "SELECT 1").Run()

	if err := initOpsDB(dbPath); err != nil {
		t.Fatalf("initOpsDB failed: %v", err)
	}

	cfg := &Config{
		HistoryDB: dbPath,
		Ops: OpsConfig{
			MessageQueue: MessageQueueConfig{
				Enabled:      true,
				MaxQueueSize: 100,
			},
		},
	}

	mq := newMessageQueueEngine(cfg)

	// Enqueue some messages.
	mq.Enqueue("telegram", "user1", "msg1", 0)
	mq.Enqueue("telegram", "user2", "msg2", 0)

	stats := mq.QueueStats()
	if stats["pending"] != 2 {
		t.Errorf("expected pending=2, got %d", stats["pending"])
	}
	if stats["sent"] != 0 {
		t.Errorf("expected sent=0, got %d", stats["sent"])
	}

	// Process queue.
	mq.ProcessQueue(context.Background())

	stats = mq.QueueStats()
	if stats["sent"] != 2 {
		t.Errorf("expected sent=2, got %d", stats["sent"])
	}
	if stats["pending"] != 0 {
		t.Errorf("expected pending=0, got %d", stats["pending"])
	}
}

func TestQueueStats_Empty(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_stats_empty.db")
	exec.Command("sqlite3", dbPath, "SELECT 1").Run()

	if err := initOpsDB(dbPath); err != nil {
		t.Fatalf("initOpsDB failed: %v", err)
	}

	cfg := &Config{HistoryDB: dbPath}
	mq := newMessageQueueEngine(cfg)

	stats := mq.QueueStats()
	if stats["pending"] != 0 {
		t.Errorf("expected pending=0 for empty queue, got %d", stats["pending"])
	}
}

func TestSystemHealth(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_health.db")
	exec.Command("sqlite3", dbPath, "SELECT 1").Run()

	if err := initOpsDB(dbPath); err != nil {
		t.Fatalf("initOpsDB failed: %v", err)
	}

	cfg := &Config{
		HistoryDB:     dbPath,
		MaxConcurrent: 3,
		DefaultModel:  "sonnet",
		Providers:     map[string]ProviderConfig{"claude": {Type: "claude-cli"}},
		Agents:         map[string]AgentConfig{"test": {}},
	}

	health := getSystemHealth(cfg)

	// Check top-level status.
	if health["status"] != "healthy" {
		t.Errorf("expected status=healthy, got %v", health["status"])
	}

	// Check database status.
	dbHealth, ok := health["database"].(map[string]any)
	if !ok {
		t.Fatal("expected database map")
	}
	if dbHealth["status"] != "healthy" {
		t.Errorf("expected db status=healthy, got %v", dbHealth["status"])
	}

	// Check config summary.
	cfgSummary, ok := health["config"].(map[string]any)
	if !ok {
		t.Fatal("expected config map")
	}
	if cfgSummary["maxConcurrent"] != 3 {
		t.Errorf("expected maxConcurrent=3, got %v", cfgSummary["maxConcurrent"])
	}
	if cfgSummary["providers"] != 1 {
		t.Errorf("expected providers=1, got %v", cfgSummary["providers"])
	}
	if cfgSummary["agents"] != 1 {
		t.Errorf("expected agents=1, got %v", cfgSummary["agents"])
	}
}

func TestSystemHealth_DegradedWithUnhealthyChannel(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_health_degraded.db")
	exec.Command("sqlite3", dbPath, "SELECT 1").Run()

	if err := initOpsDB(dbPath); err != nil {
		t.Fatalf("initOpsDB failed: %v", err)
	}

	// Record an unhealthy channel.
	recordChannelHealth(dbPath, "telegram", "offline", "bot disconnected")

	cfg := &Config{HistoryDB: dbPath}
	health := getSystemHealth(cfg)

	if health["status"] != "degraded" {
		t.Errorf("expected status=degraded with offline channel, got %v", health["status"])
	}
}

func TestSystemHealth_NoDatabase(t *testing.T) {
	cfg := &Config{HistoryDB: "/nonexistent/path.db"}
	health := getSystemHealth(cfg)

	if health["status"] != "degraded" {
		t.Errorf("expected status=degraded with no db, got %v", health["status"])
	}
}

func TestCleanupExpiredMessages(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_cleanup.db")
	exec.Command("sqlite3", dbPath, "SELECT 1").Run()

	if err := initOpsDB(dbPath); err != nil {
		t.Fatalf("initOpsDB failed: %v", err)
	}

	// Insert an old sent message.
	oldTime := time.Now().UTC().AddDate(0, 0, -10).Format(time.RFC3339)
	sql := fmt.Sprintf(
		`INSERT INTO message_queue (channel, channel_target, message_text, status, created_at, updated_at) VALUES ('telegram', 'user1', 'old', 'sent', '%s', '%s')`,
		oldTime, oldTime,
	)
	exec.Command("sqlite3", dbPath, sql).Run()

	// Insert a recent sent message.
	now := time.Now().UTC().Format(time.RFC3339)
	sql = fmt.Sprintf(
		`INSERT INTO message_queue (channel, channel_target, message_text, status, created_at, updated_at) VALUES ('telegram', 'user2', 'new', 'sent', '%s', '%s')`,
		now, now,
	)
	exec.Command("sqlite3", dbPath, sql).Run()

	// Cleanup with 7-day retention.
	err := cleanupExpiredMessages(dbPath, 7)
	if err != nil {
		t.Fatalf("cleanup failed: %v", err)
	}

	rows, err := queryDB(dbPath, "SELECT channel_target FROM message_queue")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row after cleanup, got %d", len(rows))
	}
	if fmt.Sprintf("%v", rows[0]["channel_target"]) != "user2" {
		t.Errorf("expected user2 to survive cleanup, got %v", rows[0]["channel_target"])
	}
}

func TestBoolToHealthy(t *testing.T) {
	if boolToHealthy(true) != "healthy" {
		t.Error("expected healthy for true")
	}
	if boolToHealthy(false) != "offline" {
		t.Error("expected offline for false")
	}
}

func TestQueueStatusSummary(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_summary.db")
	exec.Command("sqlite3", dbPath, "SELECT 1").Run()

	if err := initOpsDB(dbPath); err != nil {
		t.Fatalf("initOpsDB failed: %v", err)
	}

	// Empty queue.
	summary := queueStatusSummary(dbPath)
	if summary != "message queue: empty" {
		t.Errorf("expected empty summary, got: %s", summary)
	}

	// Add some messages.
	now := time.Now().UTC().Format(time.RFC3339)
	for i := 0; i < 3; i++ {
		sql := fmt.Sprintf(
			`INSERT INTO message_queue (channel, channel_target, message_text, status, created_at, updated_at) VALUES ('telegram', 'user', 'msg', 'pending', '%s', '%s')`,
			now, now,
		)
		exec.Command("sqlite3", dbPath, sql).Run()
	}

	summary = queueStatusSummary(dbPath)
	if !strings.Contains(summary, "pending=3") {
		t.Errorf("expected pending=3 in summary, got: %s", summary)
	}
}

func TestSQLInjectionSafety(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_injection.db")
	exec.Command("sqlite3", dbPath, "SELECT 1").Run()

	if err := initOpsDB(dbPath); err != nil {
		t.Fatalf("initOpsDB failed: %v", err)
	}

	cfg := &Config{
		HistoryDB: dbPath,
		Ops: OpsConfig{
			MessageQueue: MessageQueueConfig{
				Enabled:      true,
				MaxQueueSize: 100,
			},
		},
	}

	mq := newMessageQueueEngine(cfg)

	// Try to inject SQL via message text.
	err := mq.Enqueue("telegram", "user1", "'; DROP TABLE message_queue; --", 0)
	if err != nil {
		t.Fatalf("Enqueue with special chars failed: %v", err)
	}

	// Table should still exist.
	rows, err := queryDB(dbPath, "SELECT COUNT(*) as cnt FROM message_queue")
	if err != nil {
		t.Fatalf("table was dropped by SQL injection! query failed: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("expected at least 1 row")
	}
}

func TestInitOpsDB_FileCreation(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "brand_new.db")

	// File should not exist yet.
	if _, err := os.Stat(dbPath); err == nil {
		t.Fatal("db file should not exist yet")
	}

	err := initOpsDB(dbPath)
	if err != nil {
		t.Fatalf("initOpsDB on new file failed: %v", err)
	}

	// File should now exist.
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("db file should exist after initOpsDB: %v", err)
	}
}
