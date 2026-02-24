package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// --- P23.7: Reliability & Operations ---

// QueuedMessage represents a message in the offline retry queue.
type QueuedMessage struct {
	ID            int    `json:"id"`
	Channel       string `json:"channel"`
	ChannelTarget string `json:"channelTarget"`
	MessageText   string `json:"messageText"`
	Priority      int    `json:"priority"`
	Status        string `json:"status"` // pending,sending,sent,failed,expired
	RetryCount    int    `json:"retryCount"`
	MaxRetries    int    `json:"maxRetries"`
	NextRetryAt   string `json:"nextRetryAt"`
	Error         string `json:"error"`
	CreatedAt     string `json:"createdAt"`
	UpdatedAt     string `json:"updatedAt"`
}

// ChannelHealthStatus tracks the health of a communication channel.
type ChannelHealthStatus struct {
	Channel      string `json:"channel"`
	Status       string `json:"status"` // healthy,degraded,offline
	LastError    string `json:"lastError"`
	LastSuccess  string `json:"lastSuccess"`
	FailureCount int    `json:"failureCount"`
	UpdatedAt    string `json:"updatedAt"`
}

// MessageQueueEngine manages message queueing and retry logic.
type MessageQueueEngine struct {
	cfg    *Config
	dbPath string
	mu     sync.Mutex
}

// initOpsDB creates the message_queue, backup_log, and channel_status tables.
func initOpsDB(dbPath string) error {
	ddl := `
CREATE TABLE IF NOT EXISTS message_queue (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    channel TEXT NOT NULL,
    channel_target TEXT NOT NULL,
    message_text TEXT NOT NULL,
    priority INTEGER DEFAULT 0,
    status TEXT DEFAULT 'pending',
    retry_count INTEGER DEFAULT 0,
    max_retries INTEGER DEFAULT 3,
    next_retry_at TEXT DEFAULT '',
    error TEXT DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_mq_status ON message_queue(status, next_retry_at);

CREATE TABLE IF NOT EXISTS backup_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    filename TEXT NOT NULL,
    size_bytes INTEGER DEFAULT 0,
    status TEXT DEFAULT 'success',
    duration_ms INTEGER DEFAULT 0,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS channel_status (
    channel TEXT PRIMARY KEY,
    status TEXT DEFAULT 'healthy',
    last_error TEXT DEFAULT '',
    last_success TEXT DEFAULT '',
    failure_count INTEGER DEFAULT 0,
    updated_at TEXT NOT NULL
);
`
	cmd := exec.Command("sqlite3", dbPath, ddl)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("init ops tables: %w: %s", err, string(out))
	}
	return nil
}

// newMessageQueueEngine creates a new message queue engine.
func newMessageQueueEngine(cfg *Config) *MessageQueueEngine {
	return &MessageQueueEngine{
		cfg:    cfg,
		dbPath: cfg.HistoryDB,
	}
}

// Enqueue adds a message to the queue for delivery.
func (mq *MessageQueueEngine) Enqueue(channel, target, text string, priority int) error {
	mq.mu.Lock()
	defer mq.mu.Unlock()

	if channel == "" || target == "" || text == "" {
		return fmt.Errorf("channel, target, and text are required")
	}

	maxRetries := mq.cfg.Ops.MessageQueue.retryAttemptsOrDefault()
	maxSize := mq.cfg.Ops.MessageQueue.maxQueueSizeOrDefault()

	// Check queue size limit.
	rows, err := queryDB(mq.dbPath, "SELECT COUNT(*) as cnt FROM message_queue WHERE status IN ('pending','sending')")
	if err == nil && len(rows) > 0 {
		cnt := jsonInt(rows[0]["cnt"])
		if cnt >= maxSize {
			return fmt.Errorf("queue full (%d/%d)", cnt, maxSize)
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	sql := fmt.Sprintf(
		`INSERT INTO message_queue (channel, channel_target, message_text, priority, status, max_retries, created_at, updated_at) VALUES ('%s', '%s', '%s', %d, 'pending', %d, '%s', '%s')`,
		escapeSQLite(channel), escapeSQLite(target), escapeSQLite(text),
		priority, maxRetries, now, now,
	)

	cmd := exec.Command("sqlite3", mq.dbPath, sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("enqueue: %w: %s", err, string(out))
	}
	return nil
}

// ProcessQueue processes pending messages with retry logic.
func (mq *MessageQueueEngine) ProcessQueue(ctx context.Context) {
	mq.mu.Lock()
	defer mq.mu.Unlock()

	now := time.Now().UTC().Format(time.RFC3339)

	// Select pending messages ready for delivery.
	sql := fmt.Sprintf(
		`SELECT id, channel, channel_target, message_text, priority, retry_count, max_retries FROM message_queue WHERE status='pending' AND (next_retry_at='' OR next_retry_at <= '%s') ORDER BY priority DESC, id ASC LIMIT 10`,
		now,
	)

	rows, err := queryDB(mq.dbPath, sql)
	if err != nil {
		logWarn("message queue: query failed", "error", err)
		return
	}

	for _, row := range rows {
		select {
		case <-ctx.Done():
			return
		default:
		}

		id := jsonInt(row["id"])
		channel := fmt.Sprintf("%v", row["channel"])
		target := fmt.Sprintf("%v", row["channel_target"])
		text := fmt.Sprintf("%v", row["message_text"])
		retryCount := jsonInt(row["retry_count"])
		maxRetries := jsonInt(row["max_retries"])

		// Mark as sending.
		updateNow := time.Now().UTC().Format(time.RFC3339)
		updateSQL := fmt.Sprintf(
			`UPDATE message_queue SET status='sending', updated_at='%s' WHERE id=%d`,
			updateNow, id,
		)
		exec.Command("sqlite3", mq.dbPath, updateSQL).Run()

		// Attempt delivery (log for now - actual delivery integration is for later).
		deliveryErr := attemptDelivery(channel, target, text)

		updateNow = time.Now().UTC().Format(time.RFC3339)
		if deliveryErr == nil {
			// Success.
			successSQL := fmt.Sprintf(
				`UPDATE message_queue SET status='sent', updated_at='%s' WHERE id=%d`,
				updateNow, id,
			)
			exec.Command("sqlite3", mq.dbPath, successSQL).Run()

			// Update channel health.
			recordChannelHealth(mq.dbPath, channel, "healthy", "")

			logInfo("message queue: delivered", "id", id, "channel", channel, "target", target)
		} else {
			// Failure.
			retryCount++
			errMsg := escapeSQLite(deliveryErr.Error())

			if retryCount >= maxRetries {
				// Max retries exceeded.
				failSQL := fmt.Sprintf(
					`UPDATE message_queue SET status='failed', retry_count=%d, error='%s', updated_at='%s' WHERE id=%d`,
					retryCount, errMsg, updateNow, id,
				)
				exec.Command("sqlite3", mq.dbPath, failSQL).Run()

				logWarn("message queue: permanently failed", "id", id, "channel", channel, "retries", retryCount)
			} else {
				// Schedule retry with exponential backoff: 30s * 2^retry_count.
				backoff := time.Duration(30*(1<<uint(retryCount))) * time.Second
				nextRetry := time.Now().UTC().Add(backoff).Format(time.RFC3339)

				retrySQL := fmt.Sprintf(
					`UPDATE message_queue SET status='pending', retry_count=%d, error='%s', next_retry_at='%s', updated_at='%s' WHERE id=%d`,
					retryCount, errMsg, nextRetry, updateNow, id,
				)
				exec.Command("sqlite3", mq.dbPath, retrySQL).Run()

				logInfo("message queue: retry scheduled", "id", id, "channel", channel, "retryCount", retryCount, "nextRetry", nextRetry)
			}

			// Update channel health.
			recordChannelHealth(mq.dbPath, channel, "degraded", deliveryErr.Error())
		}
	}
}

// attemptDelivery tries to deliver a message. For now just logs it.
// Actual channel delivery will be integrated later.
func attemptDelivery(channel, target, text string) error {
	// Placeholder: real delivery integration will come later.
	// For now, all deliveries succeed (logged only).
	logDebug("message queue: delivery attempt", "channel", channel, "target", target, "textLen", len(text))
	return nil
}

// Start runs the message queue processor as a background goroutine.
func (mq *MessageQueueEngine) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				mq.ProcessQueue(ctx)
			}
		}
	}()
}

// QueueStats returns counts of messages by status.
func (mq *MessageQueueEngine) QueueStats() map[string]int {
	stats := map[string]int{
		"pending": 0,
		"sending": 0,
		"sent":    0,
		"failed":  0,
	}

	rows, err := queryDB(mq.dbPath, "SELECT status, COUNT(*) as cnt FROM message_queue GROUP BY status")
	if err != nil {
		return stats
	}
	for _, row := range rows {
		status := fmt.Sprintf("%v", row["status"])
		cnt := jsonInt(row["cnt"])
		stats[status] = cnt
	}
	return stats
}

// recordChannelHealth updates the health status of a channel.
func recordChannelHealth(dbPath, channel, status, lastError string) error {
	now := time.Now().UTC().Format(time.RFC3339)

	var sql string
	if status == "healthy" {
		sql = fmt.Sprintf(
			`INSERT INTO channel_status (channel, status, last_success, failure_count, updated_at) VALUES ('%s', '%s', '%s', 0, '%s') ON CONFLICT(channel) DO UPDATE SET status='%s', last_success='%s', failure_count=0, updated_at='%s'`,
			escapeSQLite(channel), status, now, now,
			status, now, now,
		)
	} else {
		sql = fmt.Sprintf(
			`INSERT INTO channel_status (channel, status, last_error, failure_count, updated_at) VALUES ('%s', '%s', '%s', 1, '%s') ON CONFLICT(channel) DO UPDATE SET status='%s', last_error='%s', failure_count=failure_count+1, updated_at='%s'`,
			escapeSQLite(channel), status, escapeSQLite(lastError), now,
			status, escapeSQLite(lastError), now,
		)
	}

	cmd := exec.Command("sqlite3", dbPath, sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("record channel health: %w: %s", err, string(out))
	}
	return nil
}

// getChannelHealth returns the health status of all channels.
func getChannelHealth(dbPath string) ([]ChannelHealthStatus, error) {
	rows, err := queryDB(dbPath, "SELECT channel, status, last_error, last_success, failure_count, updated_at FROM channel_status ORDER BY channel")
	if err != nil {
		return nil, err
	}

	var results []ChannelHealthStatus
	for _, row := range rows {
		results = append(results, ChannelHealthStatus{
			Channel:      fmt.Sprintf("%v", row["channel"]),
			Status:       fmt.Sprintf("%v", row["status"]),
			LastError:    fmt.Sprintf("%v", row["last_error"]),
			LastSuccess:  fmt.Sprintf("%v", row["last_success"]),
			FailureCount: jsonInt(row["failure_count"]),
			UpdatedAt:    fmt.Sprintf("%v", row["updated_at"]),
		})
	}
	return results, nil
}

// getSystemHealth returns an overall system health summary.
func getSystemHealth(cfg *Config) map[string]any {
	health := map[string]any{
		"status":    "healthy",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}

	// Check DB accessibility.
	dbOK := false
	if cfg.HistoryDB != "" {
		rows, err := queryDB(cfg.HistoryDB, "SELECT 1 as ok")
		if err == nil && len(rows) > 0 {
			dbOK = true
		}
	}
	health["database"] = map[string]any{
		"status": boolToHealthy(dbOK),
		"path":   cfg.HistoryDB,
	}

	// Channel health.
	channels := []ChannelHealthStatus{}
	if cfg.HistoryDB != "" {
		ch, err := getChannelHealth(cfg.HistoryDB)
		if err == nil {
			channels = ch
		}
	}
	health["channels"] = channels

	// Message queue stats.
	if cfg.Ops.MessageQueue.Enabled && cfg.HistoryDB != "" {
		mqe := newMessageQueueEngine(cfg)
		health["messageQueue"] = mqe.QueueStats()
	}

	// Active integrations.
	integrations := map[string]bool{
		"telegram":  cfg.Telegram.Enabled,
		"slack":     cfg.Slack.Enabled,
		"discord":   cfg.Discord.Enabled,
		"whatsapp":  cfg.WhatsApp.Enabled,
		"line":      cfg.LINE.Enabled,
		"matrix":    cfg.Matrix.Enabled,
		"teams":     cfg.Teams.Enabled,
		"signal":    cfg.Signal.Enabled,
		"gchat":     cfg.GoogleChat.Enabled,
		"gmail":     cfg.Gmail.Enabled,
		"calendar":  cfg.Calendar.Enabled,
		"twitter":   cfg.Twitter.Enabled,
		"imessage":  cfg.IMessage.Enabled,
		"homeassistant": cfg.HomeAssistant.Enabled,
	}
	health["integrations"] = integrations

	// Count unhealthy channels.
	unhealthyCount := 0
	for _, ch := range channels {
		if ch.Status != "healthy" {
			unhealthyCount++
		}
	}
	if !dbOK {
		health["status"] = "degraded"
	} else if unhealthyCount > 0 {
		health["status"] = "degraded"
	}

	// Config summary.
	health["config"] = map[string]any{
		"maxConcurrent":  cfg.MaxConcurrent,
		"defaultModel":   cfg.DefaultModel,
		"defaultTimeout": cfg.DefaultTimeout,
		"providers":      len(cfg.Providers),
		"roles":          len(cfg.Roles),
	}

	return health
}

// boolToHealthy returns "healthy" or "offline" based on a bool.
func boolToHealthy(ok bool) string {
	if ok {
		return "healthy"
	}
	return "offline"
}

// --- Tool Handlers for P23.7 ---

// toolBackupNow triggers an immediate backup.
func toolBackupNow(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	bs := newBackupScheduler(cfg)
	result, err := bs.RunBackup()
	if err != nil {
		return "", fmt.Errorf("backup failed: %w", err)
	}
	b, _ := json.Marshal(result)
	return string(b), nil
}

// toolExportData triggers a GDPR data export.
func toolExportData(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		UserID string `json:"userId"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if !cfg.Ops.ExportEnabled {
		return "", fmt.Errorf("data export is not enabled in config (ops.exportEnabled)")
	}

	result, err := exportUserData(cfg, args.UserID)
	if err != nil {
		return "", fmt.Errorf("export failed: %w", err)
	}
	b, _ := json.Marshal(result)
	return string(b), nil
}

// toolSystemHealth returns the system health summary.
func toolSystemHealth(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	health := getSystemHealth(cfg)
	b, _ := json.MarshalIndent(health, "", "  ")
	return string(b), nil
}

// --- Cleanup helper ---

// cleanupExpiredMessages removes old sent/failed messages from the queue.
func cleanupExpiredMessages(dbPath string, retainDays int) error {
	if retainDays <= 0 {
		retainDays = 7
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -retainDays).Format(time.RFC3339)
	sql := fmt.Sprintf(
		`DELETE FROM message_queue WHERE status IN ('sent','failed','expired') AND updated_at < '%s'`,
		cutoff,
	)

	cmd := exec.Command("sqlite3", dbPath, sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("cleanup expired messages: %w: %s", err, string(out))
	}

	// Also clean old backup logs.
	sql = fmt.Sprintf(
		`DELETE FROM backup_log WHERE created_at < '%s'`,
		time.Now().UTC().AddDate(0, 0, -90).Format(time.RFC3339),
	)
	exec.Command("sqlite3", dbPath, sql).Run()

	return nil
}

// --- Queue Status Summary ---

// queueStatusSummary returns a human-readable summary of the message queue.
func queueStatusSummary(dbPath string) string {
	rows, err := queryDB(dbPath, "SELECT status, COUNT(*) as cnt FROM message_queue GROUP BY status")
	if err != nil {
		return "message queue: unavailable"
	}
	if len(rows) == 0 {
		return "message queue: empty"
	}

	var parts []string
	for _, row := range rows {
		status := fmt.Sprintf("%v", row["status"])
		cnt := jsonInt(row["cnt"])
		parts = append(parts, fmt.Sprintf("%s=%d", status, cnt))
	}
	return "message queue: " + strings.Join(parts, ", ")
}
