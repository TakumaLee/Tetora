package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// --- Offline Queue Config ---

// OfflineQueueConfig configures the offline task queue.
type OfflineQueueConfig struct {
	Enabled  bool   `json:"enabled,omitempty"`  // default: true when circuitBreaker is configured
	TTL      string `json:"ttl,omitempty"`      // max time in queue before expiry (default "1h")
	MaxItems int    `json:"maxItems,omitempty"` // max queued items (default 100)
}

func (c OfflineQueueConfig) ttlOrDefault() time.Duration {
	if c.TTL != "" {
		if d, err := time.ParseDuration(c.TTL); err == nil && d > 0 {
			return d
		}
	}
	return 1 * time.Hour
}

func (c OfflineQueueConfig) maxItemsOrDefault() int {
	if c.MaxItems > 0 {
		return c.MaxItems
	}
	return 100
}

// --- Queue Item ---

// QueueItem represents a task buffered in the offline queue.
type QueueItem struct {
	ID         int    `json:"id"`
	TaskJSON   string `json:"taskJson"`
	AgentName   string `json:"agent"`
	Source     string `json:"source"`
	Priority   int    `json:"priority"`   // higher = processed sooner (0 = normal)
	Status     string `json:"status"`     // pending, processing, completed, expired, failed
	RetryCount int    `json:"retryCount"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
	Error      string `json:"error,omitempty"`
}

// --- DB Init ---

func initQueueDB(dbPath string) error {
	sql := `
CREATE TABLE IF NOT EXISTS offline_queue (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task_json TEXT NOT NULL,
    agent TEXT DEFAULT '',
    source TEXT DEFAULT '',
    priority INTEGER DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'pending',
    retry_count INTEGER DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    error TEXT DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_queue_status ON offline_queue(status);
`
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("init offline_queue: %s: %w", string(out), err)
	}
	return nil
}

// --- Enqueue ---

// enqueueTask adds a task to the offline queue for later retry.
func enqueueTask(dbPath string, task Task, agentName string, priority int) error {
	if dbPath == "" {
		return fmt.Errorf("no db path")
	}

	taskBytes, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("marshal task: %w", err)
	}

	now := time.Now().Format(time.RFC3339)
	sql := fmt.Sprintf(
		`INSERT INTO offline_queue (task_json, agent, source, priority, status, retry_count, created_at, updated_at)
		 VALUES ('%s','%s','%s',%d,'pending',0,'%s','%s')`,
		escapeSQLite(string(taskBytes)),
		escapeSQLite(agentName),
		escapeSQLite(task.Source),
		priority,
		escapeSQLite(now),
		escapeSQLite(now),
	)
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("enqueue: %s: %w", string(out), err)
	}
	return nil
}

// --- Dequeue ---

// dequeueNext retrieves the oldest pending item (respecting priority) and marks it "processing".
// Returns nil if no pending items.
func dequeueNext(dbPath string) *QueueItem {
	if dbPath == "" {
		return nil
	}

	// Select highest-priority, oldest pending item.
	selectSQL := `SELECT id, task_json, agent, source, priority, status, retry_count, created_at, updated_at, error
		FROM offline_queue
		WHERE status = 'pending'
		ORDER BY priority DESC, id ASC
		LIMIT 1`

	rows, err := queryDB(dbPath, selectSQL)
	if err != nil || len(rows) == 0 {
		return nil
	}

	item := queueItemFromRow(rows[0])

	// Mark as processing.
	now := time.Now().Format(time.RFC3339)
	updateSQL := fmt.Sprintf(
		`UPDATE offline_queue SET status = 'processing', updated_at = '%s' WHERE id = %d AND status = 'pending'`,
		escapeSQLite(now), item.ID)
	cmd := exec.Command("sqlite3", dbPath, updateSQL)
	if _, err := cmd.CombinedOutput(); err != nil {
		return nil
	}

	item.Status = "processing"
	return &item
}

// --- Query ---

// queryQueue returns queue items, optionally filtered by status.
func queryQueue(dbPath, status string) []QueueItem {
	if dbPath == "" {
		return nil
	}

	where := ""
	if status != "" {
		where = fmt.Sprintf("WHERE status = '%s'", escapeSQLite(status))
	}

	sql := fmt.Sprintf(
		`SELECT id, task_json, agent, source, priority, status, retry_count, created_at, updated_at, error
		 FROM offline_queue %s
		 ORDER BY priority DESC, id ASC`, where)

	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return nil
	}

	var items []QueueItem
	for _, row := range rows {
		items = append(items, queueItemFromRow(row))
	}
	return items
}

// queryQueueItem returns a single queue item by ID.
func queryQueueItem(dbPath string, id int) *QueueItem {
	if dbPath == "" {
		return nil
	}
	sql := fmt.Sprintf(
		`SELECT id, task_json, agent, source, priority, status, retry_count, created_at, updated_at, error
		 FROM offline_queue WHERE id = %d`, id)
	rows, err := queryDB(dbPath, sql)
	if err != nil || len(rows) == 0 {
		return nil
	}
	item := queueItemFromRow(rows[0])
	return &item
}

// --- Update ---

// updateQueueStatus updates the status and error message of a queue item.
func updateQueueStatus(dbPath string, id int, status, errMsg string) {
	if dbPath == "" {
		return
	}
	now := time.Now().Format(time.RFC3339)
	sql := fmt.Sprintf(
		`UPDATE offline_queue SET status = '%s', error = '%s', updated_at = '%s' WHERE id = %d`,
		escapeSQLite(status), escapeSQLite(errMsg), escapeSQLite(now), id)
	cmd := exec.Command("sqlite3", dbPath, sql)
	cmd.CombinedOutput()
}

// incrementQueueRetry increments retry_count and updates status.
func incrementQueueRetry(dbPath string, id int, status, errMsg string) {
	if dbPath == "" {
		return
	}
	now := time.Now().Format(time.RFC3339)
	sql := fmt.Sprintf(
		`UPDATE offline_queue SET status = '%s', error = '%s', retry_count = retry_count + 1, updated_at = '%s' WHERE id = %d`,
		escapeSQLite(status), escapeSQLite(errMsg), escapeSQLite(now), id)
	cmd := exec.Command("sqlite3", dbPath, sql)
	cmd.CombinedOutput()
}

// --- Delete ---

// deleteQueueItem removes a queue item.
func deleteQueueItem(dbPath string, id int) error {
	if dbPath == "" {
		return fmt.Errorf("no db path")
	}
	sql := fmt.Sprintf(`DELETE FROM offline_queue WHERE id = %d`, id)
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("delete queue item: %s: %w", string(out), err)
	}
	return nil
}

// --- Cleanup ---

// cleanupExpiredQueue marks pending items older than TTL as "expired".
// Returns the number of expired items.
func cleanupExpiredQueue(dbPath string, ttl time.Duration) int {
	if dbPath == "" {
		return 0
	}

	cutoff := time.Now().Add(-ttl).Format(time.RFC3339)
	now := time.Now().Format(time.RFC3339)

	// Count before updating.
	countSQL := fmt.Sprintf(
		`SELECT COUNT(*) as cnt FROM offline_queue WHERE status = 'pending' AND created_at < '%s'`,
		escapeSQLite(cutoff))
	rows, err := queryDB(dbPath, countSQL)
	if err != nil || len(rows) == 0 {
		return 0
	}
	count := jsonInt(rows[0]["cnt"])
	if count == 0 {
		return 0
	}

	// Update expired items.
	updateSQL := fmt.Sprintf(
		`UPDATE offline_queue SET status = 'expired', error = 'TTL exceeded', updated_at = '%s'
		 WHERE status = 'pending' AND created_at < '%s'`,
		escapeSQLite(now), escapeSQLite(cutoff))
	cmd := exec.Command("sqlite3", dbPath, updateSQL)
	cmd.CombinedOutput()

	return count
}

// cleanupOldQueueItems removes completed/expired/failed items older than N days.
func cleanupOldQueueItems(dbPath string, days int) {
	if dbPath == "" {
		return
	}
	sql := fmt.Sprintf(
		`DELETE FROM offline_queue WHERE status IN ('completed','expired','failed') AND datetime(updated_at) < datetime('now','-%d days')`,
		days)
	cmd := exec.Command("sqlite3", dbPath, sql)
	cmd.CombinedOutput()
}

// countPendingQueue returns the number of pending items in the queue.
func countPendingQueue(dbPath string) int {
	if dbPath == "" {
		return 0
	}
	rows, err := queryDB(dbPath, `SELECT COUNT(*) as cnt FROM offline_queue WHERE status IN ('pending','processing')`)
	if err != nil || len(rows) == 0 {
		return 0
	}
	return jsonInt(rows[0]["cnt"])
}

// --- Max Items Check ---

// isQueueFull checks if the queue has reached its max capacity.
func isQueueFull(dbPath string, maxItems int) bool {
	return countPendingQueue(dbPath) >= maxItems
}

// --- Helper ---

func queueItemFromRow(row map[string]any) QueueItem {
	return QueueItem{
		ID:         jsonInt(row["id"]),
		TaskJSON:   jsonStr(row["task_json"]),
		AgentName:   jsonStr(row["agent"]),
		Source:     jsonStr(row["source"]),
		Priority:   jsonInt(row["priority"]),
		Status:     jsonStr(row["status"]),
		RetryCount: jsonInt(row["retry_count"]),
		CreatedAt:  jsonStr(row["created_at"]),
		UpdatedAt:  jsonStr(row["updated_at"]),
		Error:      jsonStr(row["error"]),
	}
}

// isAllProvidersUnavailable checks whether an error indicates total provider failure.
func isAllProvidersUnavailable(errMsg string) bool {
	return strings.Contains(strings.ToLower(errMsg), "all providers unavailable")
}

// --- Queue Drainer ---

// queueDrainer processes offline queue items when providers recover.
type queueDrainer struct {
	cfg      *Config
	sem      chan struct{}
	state    *dispatchState
	notifyFn func(string)
	ttl      time.Duration
}

const maxQueueRetries = 3

// anyProviderAvailable checks if at least one provider's circuit allows requests.
func (d *queueDrainer) anyProviderAvailable() bool {
	if d.cfg.circuits == nil {
		return true // no circuit breaker = always available
	}
	for name := range d.cfg.Providers {
		cb := d.cfg.circuits.get(name)
		if cb.State() != CircuitOpen {
			return true
		}
	}
	return false
}

// run starts the queue drainer loop.
func (d *queueDrainer) run(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	logInfo("queue drainer started", "ttl", d.ttl.String())

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.tick(ctx)
		}
	}
}

func (d *queueDrainer) tick(ctx context.Context) {
	dbPath := d.cfg.HistoryDB
	if dbPath == "" {
		return
	}

	// 1. Expire old items.
	expired := cleanupExpiredQueue(dbPath, d.ttl)
	if expired > 0 {
		logWarn("queue items expired", "count", expired)
		if d.notifyFn != nil {
			d.notifyFn(fmt.Sprintf("Offline queue: %d item(s) expired (TTL %s)", expired, d.ttl.String()))
		}
	}

	// 2. Check if any provider is available.
	if !d.anyProviderAvailable() {
		return
	}

	// 3. Drain pending items one at a time.
	for {
		if ctx.Err() != nil {
			return
		}

		item := dequeueNext(dbPath)
		if item == nil {
			return // queue empty
		}

		d.processItem(ctx, item)
	}
}

func (d *queueDrainer) processItem(ctx context.Context, item *QueueItem) {
	// Deserialize task.
	var task Task
	if err := json.Unmarshal([]byte(item.TaskJSON), &task); err != nil {
		logError("queue: bad task JSON", "id", item.ID, "error", err)
		updateQueueStatus(d.cfg.HistoryDB, item.ID, "failed", "invalid task JSON: "+err.Error())
		return
	}

	// Generate new IDs for the retry.
	task.ID = newUUID()
	task.SessionID = newUUID()
	task.Source = "queue:" + task.Source

	logInfoCtx(ctx, "queue: retrying task", "queueId", item.ID, "taskId", task.ID[:8], "name", task.Name, "retry", item.RetryCount+1)

	// Run the task.
	result := runSingleTask(ctx, d.cfg, task, d.sem, item.AgentName)

	if result.Status == "success" {
		updateQueueStatus(d.cfg.HistoryDB, item.ID, "completed", "")
		logInfoCtx(ctx, "queue: task succeeded", "queueId", item.ID, "taskId", task.ID[:8])

		// Record to history.
		start := time.Now().Add(-time.Duration(result.DurationMs) * time.Millisecond)
		recordHistory(d.cfg.HistoryDB, task.ID, task.Name, task.Source, item.AgentName, task, result,
			start.Format(time.RFC3339), time.Now().Format(time.RFC3339), result.OutputFile)
		recordSessionActivity(d.cfg.HistoryDB, task, result, item.AgentName)

		if d.notifyFn != nil {
			d.notifyFn(fmt.Sprintf("Offline queue: task %q completed successfully (retry #%d)", task.Name, item.RetryCount+1))
		}
	} else if isAllProvidersUnavailable(result.Error) {
		// Still unavailable — put back in queue.
		if item.RetryCount+1 >= maxQueueRetries {
			incrementQueueRetry(d.cfg.HistoryDB, item.ID, "failed", result.Error)
			logWarnCtx(ctx, "queue: task failed after max retries", "queueId", item.ID, "retries", maxQueueRetries)
			if d.notifyFn != nil {
				d.notifyFn(fmt.Sprintf("Offline queue: task %q failed after %d retries: %s",
					task.Name, maxQueueRetries, truncate(result.Error, 200)))
			}
		} else {
			incrementQueueRetry(d.cfg.HistoryDB, item.ID, "pending", result.Error)
			logInfoCtx(ctx, "queue: task still unavailable, re-queued", "queueId", item.ID, "retry", item.RetryCount+1)
		}
	} else {
		// Non-provider error — mark as failed.
		incrementQueueRetry(d.cfg.HistoryDB, item.ID, "failed", result.Error)
		logWarnCtx(ctx, "queue: task failed with non-provider error", "queueId", item.ID, "error", result.Error)

		// Record to history even on failure.
		start := time.Now().Add(-time.Duration(result.DurationMs) * time.Millisecond)
		recordHistory(d.cfg.HistoryDB, task.ID, task.Name, task.Source, item.AgentName, task, result,
			start.Format(time.RFC3339), time.Now().Format(time.RFC3339), result.OutputFile)
	}
}
