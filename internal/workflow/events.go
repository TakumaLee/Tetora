// Package workflow — events.go: external step callback management and trigger engine.
//
// Self-contained subsystems that do NOT import the root package:
//   - CallbackManager: in-memory channel registry for external step callbacks
//   - DB helpers: workflow_callbacks and workflow_callback_stream persistence
//   - HTTP helpers: httpPostWithRetry with exponential backoff
//   - JSON helpers: extractJSONPath, applyResponseMapping
//   - HMAC helpers: callbackSignatureSecret, verifyCallbackSignature
//   - WorkflowTriggerEngine: cron/event/webhook trigger dispatch
//
// Root-only concerns (kept in root shim workflow_events.go):
//   - callbackMgr singleton, runCancellers
//   - runExternalStep (method on root workflowExecutor)
//   - resolveTemplate* methods (on root workflowExecutor)
//   - recoverPendingWorkflows (uses root dispatchState + executeWorkflow)
//   - checkpointRun (uses root workflowExecutor)
package workflow

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"tetora/internal/config"
	"tetora/internal/cron"
	"tetora/internal/db"
	"tetora/internal/dispatch"
	"tetora/internal/log"
)

// =============================================================================
// Callback Types
// =============================================================================

// CallbackManager manages in-memory channels for pending external step callbacks.
type CallbackManager struct {
	mu       sync.RWMutex
	channels map[string]*callbackEntry
	dbPath   string
}

type callbackEntry struct {
	ch   chan CallbackResult
	mode string // "single" or "streaming"
	seq  int    // next sequence number for streaming persistence
}

// CallbackResult holds one callback delivery.
type CallbackResult struct {
	Status      int    `json:"status"`
	Body        string `json:"body"`
	ContentType string `json:"contentType"`
	RecvAt      string `json:"recvAt"`
}

// CallbackRecord is the DB representation (for recovery).
type CallbackRecord struct {
	Key        string
	RunID      string
	StepID     string
	Mode       string
	AuthMode   string
	URL        string
	Body       string
	Status     string
	TimeoutAt  string
	PostSent   bool
	Seq        int
	ResultBody string // populated when status=delivered (the callback response body)
}

// DeliverResult indicates the outcome of a Deliver call.
type DeliverResult int

const (
	DeliverOK      DeliverResult = iota // Successfully sent to channel
	DeliverNoEntry                      // No channel registered for key
	DeliverDup                          // Single mode: already has data (idempotent reject)
	DeliverFull                         // Streaming: channel buffer full
)

// DeliverWithSeq holds the result of a Deliver call along with the allocated sequence number.
type DeliverWithSeq struct {
	Result DeliverResult
	Seq    int    // sequence number for streaming persistence (-1 if not applicable)
	Mode   string // callback mode captured under lock (avoids TOCTOU with GetMode)
}

// =============================================================================
// CallbackManager methods
// =============================================================================

// NewCallbackManager creates a new CallbackManager backed by dbPath.
func NewCallbackManager(dbPath string) *CallbackManager {
	return &CallbackManager{
		channels: make(map[string]*callbackEntry),
		dbPath:   dbPath,
	}
}

// Register creates a channel for the given callback key.
// Returns nil if key already exists or capacity exceeded.
func (cm *CallbackManager) Register(key string, ctx context.Context, mode string) chan CallbackResult {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Collision check.
	if _, exists := cm.channels[key]; exists {
		return nil
	}

	// Capacity guard.
	if len(cm.channels) >= 1000 {
		log.Warn("callback manager at capacity", "count", len(cm.channels))
		return nil
	}

	bufSize := 1
	if mode == "streaming" {
		bufSize = 256
	}

	ch := make(chan CallbackResult, bufSize)
	cm.channels[key] = &callbackEntry{ch: ch, mode: mode}

	// Context cleanup goroutine: remove channel when context is cancelled.
	go func() {
		<-ctx.Done()
		cm.Unregister(key)
	}()

	return ch
}

// Deliver sends a callback result to the registered channel.
// Uses named return + recover to guard against send-on-closed-channel panic
// if concurrent Unregister closes the channel between RUnlock and send.
func (cm *CallbackManager) Deliver(key string, result CallbackResult) (dr DeliverResult) {
	out := cm.DeliverAndSeq(key, result)
	return out.Result
}

// DeliverAndSeq atomically delivers a result AND allocates a sequence number for streaming.
// This prevents the race where Unregister happens between Deliver and NextSeq.
func (cm *CallbackManager) DeliverAndSeq(key string, result CallbackResult) (out DeliverWithSeq) {
	out.Seq = -1

	cm.mu.Lock()
	entry, exists := cm.channels[key]
	if !exists {
		cm.mu.Unlock()
		out.Result = DeliverNoEntry
		return
	}
	// Capture mode and allocate seq under lock (avoids TOCTOU with GetMode).
	out.Mode = entry.mode
	isStreaming := entry.mode == "streaming"
	if isStreaming {
		out.Seq = entry.seq
		entry.seq++
	}
	cm.mu.Unlock()

	// Guard: if Unregister closes the channel concurrently, recover gracefully.
	defer func() {
		if r := recover(); r != nil {
			out.Result = DeliverNoEntry
		}
	}()

	// For single mode, check idempotency (don't send if channel already has data).
	if !isStreaming && len(entry.ch) > 0 {
		out.Result = DeliverDup
		return
	}

	select {
	case entry.ch <- result:
		out.Result = DeliverOK
	default:
		// Channel full (streaming overflow).
		out.Result = DeliverFull
	}
	return
}

// Unregister removes and closes the channel for the given key.
// Safe to call multiple times.
func (cm *CallbackManager) Unregister(key string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	entry, exists := cm.channels[key]
	if !exists {
		return
	}
	close(entry.ch)
	delete(cm.channels, key)
}

// HasChannel checks if a channel is registered for the key.
func (cm *CallbackManager) HasChannel(key string) bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	_, exists := cm.channels[key]
	return exists
}

// GetMode returns the callback mode for the key.
func (cm *CallbackManager) GetMode(key string) string {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	entry, exists := cm.channels[key]
	if !exists {
		return ""
	}
	return entry.mode
}

// SetSeq sets the sequence counter for a streaming callback key.
// Used after ReplayAccumulated to avoid seq collisions with existing DB records.
func (cm *CallbackManager) SetSeq(key string, seq int) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if entry, ok := cm.channels[key]; ok {
		entry.seq = seq
	}
}

// ReplayAccumulated sends previously accumulated streaming callbacks into the channel.
// Used after daemon restart to replay partial results.
func (cm *CallbackManager) ReplayAccumulated(key string, results []CallbackResult) {
	cm.mu.RLock()
	entry, exists := cm.channels[key]
	cm.mu.RUnlock()

	if !exists || entry.mode != "streaming" {
		return
	}
	for _, r := range results {
		select {
		case entry.ch <- r:
		default:
			log.Warn("replay: buffer full, skipping", "key", key)
		}
	}
}

// DBPath returns the database path used by this CallbackManager.
func (cm *CallbackManager) DBPath() string {
	return cm.dbPath
}

// =============================================================================
// JSON / XML helpers
// =============================================================================

// ExtractJSONPath extracts a value from a JSON string using dot-notation path.
// Supports nested objects, array indices (e.g. "items.0.name"), and type conversion.
func ExtractJSONPath(jsonStr, path string) string {
	if jsonStr == "" || path == "" {
		return ""
	}

	var root any
	if err := json.Unmarshal([]byte(jsonStr), &root); err != nil {
		return ""
	}

	parts := strings.Split(path, ".")
	current := root

	for _, part := range parts {
		switch v := current.(type) {
		case map[string]any:
			val, ok := v[part]
			if !ok {
				return ""
			}
			current = val
		case []any:
			idx, err := strconv.Atoi(part)
			if err != nil || idx < 0 || idx >= len(v) {
				return ""
			}
			current = v[idx]
		default:
			return ""
		}
	}

	// Convert to string.
	switch v := current.(type) {
	case string:
		return v
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10)
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	case nil:
		return ""
	default:
		// For objects/arrays, marshal back to JSON.
		b, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(b)
	}
}

// ExtractXMLText provides basic extraction from XML callback bodies.
// For XML callbacks without ResponseMapping, returns the raw body.
// For XML with a simple tag path like "response.status", extracts inner text
// using the last segment as the tag name.
func ExtractXMLText(xmlStr, tagName string) string {
	if tagName == "" {
		return xmlStr
	}
	// Simple tag extraction: find <tagName>...</tagName>
	openTag := "<" + tagName + ">"
	closeTag := "</" + tagName + ">"
	start := strings.Index(xmlStr, openTag)
	if start < 0 {
		return ""
	}
	start += len(openTag)
	end := strings.Index(xmlStr[start:], closeTag)
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(xmlStr[start : start+end])
}

// ApplyResponseMapping extracts data from callback body using ResponseMapping.
// Returns the extracted data path content, or the full body if no mapping.
// Tries JSON extraction first; falls back to XML tag extraction.
func ApplyResponseMapping(body string, mapping *ResponseMapping) string {
	if body == "" {
		return ""
	}
	if mapping == nil || mapping.DataPath == "" {
		return body
	}
	// Try JSON extraction first.
	extracted := ExtractJSONPath(body, mapping.DataPath)
	if extracted != "" {
		return extracted
	}
	// Fallback: try XML tag extraction (last segment of dot path as tag name).
	parts := strings.Split(mapping.DataPath, ".")
	tagName := parts[len(parts)-1]
	xmlExtracted := ExtractXMLText(body, tagName)
	if xmlExtracted != "" {
		return xmlExtracted
	}
	return body // fallback to full body
}

// =============================================================================
// HTTP helpers
// =============================================================================

// HTTPPostWithRetry sends an HTTP POST with exponential backoff retry.
// Respects context cancellation for both requests and retry delays.
func HTTPPostWithRetry(ctx context.Context, url, contentType string, headers map[string]string, body string, maxRetry int) (*http.Response, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	delays := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}

	var lastErr error
	for attempt := 0; attempt <= maxRetry; attempt++ {
		if attempt > 0 && attempt-1 < len(delays) {
			// Context-aware sleep between retries.
			select {
			case <-time.After(delays[attempt-1]):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", contentType)
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = err
			continue
		}

		// Success on 2xx, retry on 5xx.
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp, nil
		}
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode >= 500 && attempt < maxRetry {
			lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
			continue
		}

		// Non-retryable error.
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	return nil, fmt.Errorf("all %d retries failed: %w", maxRetry+1, lastErr)
}

// =============================================================================
// Wait helpers
// =============================================================================

// HandleCallbackTimeout sets the result status/error for a callback timeout/cancellation.
// result must be a pointer to a struct with Status, Error, and Output string fields.
// We accept a generic interface to avoid importing the root package's StepRunResult.
// Callers (in root) call this with their *StepRunResult and apply the returned values.
type CallbackTimeoutResult struct {
	Status string
	Error  string
	Output string
}

// HandleCallbackTimeout computes what status/error a timed-out external step should get.
func HandleCallbackTimeout(onTimeout string, timeout time.Duration, ctxErr error) CallbackTimeoutResult {
	if onTimeout == "" {
		onTimeout = "stop"
	}
	if ctxErr != nil {
		return CallbackTimeoutResult{
			Status: "cancelled",
			Error:  "workflow cancelled while waiting for callback",
		}
	}
	if onTimeout == "skip" {
		return CallbackTimeoutResult{
			Status: "skipped",
			Output: fmt.Sprintf("callback timeout after %s (skipped)", timeout.String()),
		}
	}
	return CallbackTimeoutResult{
		Status: "timeout",
		Error:  fmt.Sprintf("callback timeout after %s", timeout.String()),
	}
}

// WaitSingleCallback waits for a single callback result or timeout.
func WaitSingleCallback(ctx context.Context, ch chan CallbackResult, timeout time.Duration) *CallbackResult {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case result, ok := <-ch:
		if !ok {
			return nil // channel closed
		}
		return &result
	case <-timer.C:
		return nil // timeout
	case <-ctx.Done():
		return nil // cancelled
	}
}

// WaitStreamingCallback waits for multiple callbacks until DonePath==DoneValue or timeout.
func WaitStreamingCallback(ctx context.Context, ch chan CallbackResult, mapping *ResponseMapping, timeout time.Duration) (*CallbackResult, []CallbackResult) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	var accumulated []CallbackResult
	var lastResult *CallbackResult

	for {
		select {
		case result, ok := <-ch:
			if !ok {
				return lastResult, accumulated
			}
			accumulated = append(accumulated, result)
			lastResult = &result

			// Check if this is the final callback.
			if mapping != nil && mapping.DonePath != "" {
				doneVal := ExtractJSONPath(result.Body, mapping.DonePath)
				if doneVal == mapping.DoneValue {
					return lastResult, accumulated
				}
			}

		case <-timer.C:
			return lastResult, accumulated // partial results on timeout

		case <-ctx.Done():
			return lastResult, accumulated // cancelled
		}
	}
}

// =============================================================================
// HMAC / signature helpers
// =============================================================================

// CallbackSignatureSecret derives a per-callback HMAC secret.
// secret = hex(HMAC-SHA256(serverSecret, callbackKey))
func CallbackSignatureSecret(serverSecret, callbackKey string) string {
	mac := hmac.New(sha256.New, []byte(serverSecret))
	mac.Write([]byte(callbackKey))
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyCallbackSignature checks the X-Callback-Signature header.
func VerifyCallbackSignature(body []byte, secret, signatureHex string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signatureHex))
}

// =============================================================================
// DB helpers — workflow_callbacks table
// =============================================================================

const CallbackTableSQL = `CREATE TABLE IF NOT EXISTS workflow_callbacks (
	key TEXT PRIMARY KEY,
	run_id TEXT NOT NULL,
	step_id TEXT NOT NULL,
	mode TEXT DEFAULT 'single',
	auth_mode TEXT DEFAULT 'bearer',
	url TEXT,
	body TEXT,
	status TEXT DEFAULT 'waiting',
	timeout_at TEXT,
	post_sent INTEGER DEFAULT 0,
	seq INTEGER DEFAULT 0,
	result_body TEXT,
	result_status INTEGER DEFAULT 0,
	result_content_type TEXT,
	delivered_at TEXT,
	created_at TEXT DEFAULT (datetime('now'))
)`

const CallbackStreamTableSQL = `CREATE TABLE IF NOT EXISTS workflow_callback_stream (
	key TEXT NOT NULL,
	seq INTEGER NOT NULL,
	body TEXT,
	content_type TEXT,
	recv_at TEXT,
	PRIMARY KEY (key, seq)
)`

// InitCallbackTable creates the callback tables if they don't exist.
func InitCallbackTable(dbPath string) {
	if dbPath == "" {
		return
	}
	if err := db.Exec(dbPath, CallbackTableSQL); err != nil {
		log.Warn("init workflow_callbacks table failed", "error", err)
	}
	if err := db.Exec(dbPath, CallbackStreamTableSQL); err != nil {
		log.Warn("init workflow_callback_stream table failed", "error", err)
	}
}

// RecordPendingCallback inserts a new callback record.
func RecordPendingCallback(dbPath, key, runID, stepID, mode, authMode, url, body, timeoutAt string) {
	if dbPath == "" {
		return
	}
	sql := fmt.Sprintf(
		`INSERT OR REPLACE INTO workflow_callbacks (key, run_id, step_id, mode, auth_mode, url, body, status, timeout_at, created_at)
		 VALUES ('%s','%s','%s','%s','%s','%s','%s','waiting','%s',datetime('now'))`,
		db.Escape(key), db.Escape(runID), db.Escape(stepID),
		db.Escape(mode), db.Escape(authMode),
		db.Escape(url), db.Escape(body), db.Escape(timeoutAt),
	)
	if err := db.Exec(dbPath, sql); err != nil {
		log.Warn("record pending callback failed", "error", err, "key", key)
	}
}

// QueryPendingCallbackByKey returns a callback record by key (any status).
func QueryPendingCallbackByKey(dbPath, key string) *CallbackRecord {
	if dbPath == "" {
		return nil
	}
	sql := fmt.Sprintf(
		`SELECT key, run_id, step_id, mode, auth_mode, url, body, status, timeout_at, post_sent, seq, result_body
		 FROM workflow_callbacks WHERE key='%s' LIMIT 1`,
		db.Escape(key),
	)
	rows, err := db.Query(dbPath, sql)
	if err != nil || len(rows) == 0 {
		return nil
	}
	return parseCallbackRecord(rows[0])
}

// QueryPendingCallback returns a callback record only if status='waiting'.
func QueryPendingCallback(dbPath, key string) *CallbackRecord {
	if dbPath == "" {
		return nil
	}
	sql := fmt.Sprintf(
		`SELECT key, run_id, step_id, mode, auth_mode, url, body, status, timeout_at, post_sent, seq, result_body
		 FROM workflow_callbacks WHERE key='%s' AND status='waiting' LIMIT 1`,
		db.Escape(key),
	)
	rows, err := db.Query(dbPath, sql)
	if err != nil || len(rows) == 0 {
		return nil
	}
	return parseCallbackRecord(rows[0])
}

// QueryPendingCallbacksByRun returns all pending callbacks for a workflow run.
func QueryPendingCallbacksByRun(dbPath, runID string) []*CallbackRecord {
	if dbPath == "" {
		return nil
	}
	sql := fmt.Sprintf(
		`SELECT key, run_id, step_id, mode, auth_mode, url, body, status, timeout_at, post_sent, seq, result_body
		 FROM workflow_callbacks WHERE run_id='%s' AND status='waiting'`,
		db.Escape(runID),
	)
	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil
	}
	var records []*CallbackRecord
	for _, row := range rows {
		records = append(records, parseCallbackRecord(row))
	}
	return records
}

// MarkPostSent sets post_sent=1 for a callback record.
func MarkPostSent(dbPath, key string) {
	if dbPath == "" {
		return
	}
	sql := fmt.Sprintf(
		`UPDATE workflow_callbacks SET post_sent=1 WHERE key='%s'`,
		db.Escape(key),
	)
	if err := db.Exec(dbPath, sql); err != nil {
		log.Warn("mark post sent failed", "error", err, "key", key)
	}
}

// MarkCallbackDelivered records that a callback was delivered.
func MarkCallbackDelivered(dbPath, key string, seq int, result CallbackResult) {
	if dbPath == "" {
		return
	}
	sql := fmt.Sprintf(
		`UPDATE workflow_callbacks SET status='delivered', seq=%d, result_body='%s', result_status=%d, result_content_type='%s', delivered_at=datetime('now')
		 WHERE key='%s'`,
		seq, db.Escape(result.Body), result.Status, db.Escape(result.ContentType),
		db.Escape(key),
	)
	if err := db.Exec(dbPath, sql); err != nil {
		log.Warn("mark callback delivered failed", "error", err, "key", key)
	}
}

// UpdateCallbackRunID updates the run_id for a callback record (used during recovery).
func UpdateCallbackRunID(dbPath, key, newRunID string) {
	if dbPath == "" {
		return
	}
	sql := fmt.Sprintf(
		`UPDATE workflow_callbacks SET run_id='%s' WHERE key='%s'`,
		db.Escape(newRunID), db.Escape(key),
	)
	if err := db.Exec(dbPath, sql); err != nil {
		log.Warn("update callback run_id failed", "error", err, "key", key)
	}
}

// ResetCallbackRecord resets a callback to 'waiting' for retry.
func ResetCallbackRecord(dbPath, key string) {
	if dbPath == "" {
		return
	}
	sql := fmt.Sprintf(
		`UPDATE workflow_callbacks SET status='waiting', post_sent=0, seq=0, result_body=NULL, delivered_at=NULL WHERE key='%s'`,
		db.Escape(key),
	)
	if err := db.Exec(dbPath, sql); err != nil {
		log.Warn("reset callback record failed", "error", err, "key", key)
	}
}

// IsCallbackDelivered returns true if the callback was delivered with seq >= given seq.
func IsCallbackDelivered(dbPath, key string, seq int) bool {
	if dbPath == "" {
		return false
	}
	sql := fmt.Sprintf(
		`SELECT 1 FROM workflow_callbacks WHERE key='%s' AND status='delivered' AND seq>=%d LIMIT 1`,
		db.Escape(key), seq,
	)
	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return false
	}
	return len(rows) > 0
}

// ClearPendingCallback marks a callback as completed.
func ClearPendingCallback(dbPath, key string) {
	if dbPath == "" {
		return
	}
	sql := fmt.Sprintf(
		`UPDATE workflow_callbacks SET status='completed' WHERE key='%s'`,
		db.Escape(key),
	)
	if err := db.Exec(dbPath, sql); err != nil {
		log.Warn("clear pending callback failed", "error", err, "key", key)
	}
}

// AppendStreamingCallback records a streaming callback result to DB.
func AppendStreamingCallback(dbPath, key string, seq int, result CallbackResult) {
	if dbPath == "" {
		return
	}
	sql := fmt.Sprintf(
		`INSERT OR REPLACE INTO workflow_callback_stream (key, seq, body, content_type, recv_at)
		 VALUES ('%s', %d, '%s', '%s', '%s')`,
		db.Escape(key), seq, db.Escape(result.Body),
		db.Escape(result.ContentType), db.Escape(result.RecvAt),
	)
	if err := db.Exec(dbPath, sql); err != nil {
		log.Warn("append streaming callback failed", "error", err, "key", key, "seq", seq)
	}
}

// QueryStreamingCallbacks returns all streaming callback results for a key, ordered by seq.
func QueryStreamingCallbacks(dbPath, key string) []CallbackResult {
	if dbPath == "" {
		return nil
	}
	sql := fmt.Sprintf(
		`SELECT body, content_type, recv_at FROM workflow_callback_stream WHERE key='%s' ORDER BY seq`,
		db.Escape(key),
	)
	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil
	}
	var results []CallbackResult
	for _, row := range rows {
		results = append(results, CallbackResult{
			Body:        sqlStr(row["body"]),
			ContentType: sqlStr(row["content_type"]),
			RecvAt:      sqlStr(row["recv_at"]),
		})
	}
	return results
}

// CleanupExpiredCallbacks marks timed-out callbacks and cleans old streaming records.
func CleanupExpiredCallbacks(dbPath string) {
	if dbPath == "" {
		return
	}
	// Mark expired waiting callbacks as timeout.
	sql := `UPDATE workflow_callbacks SET status='timeout'
		WHERE status='waiting' AND timeout_at != '' AND timeout_at < datetime('now')`
	if err := db.Exec(dbPath, sql); err != nil {
		log.Warn("cleanup expired callbacks failed", "error", err)
	}

	// Clean streaming records older than 7 days for completed callbacks.
	sql2 := `DELETE FROM workflow_callback_stream WHERE key IN (
		SELECT key FROM workflow_callbacks WHERE status IN ('completed','delivered','timeout')
		AND created_at < datetime('now', '-7 days')
	)`
	if err := db.Exec(dbPath, sql2); err != nil {
		log.Warn("cleanup old streaming records failed", "error", err)
	}

	// Mark expired waiting human gates as timeout.
	sql3 := `UPDATE workflow_human_gates SET status='timeout', completed_at=datetime('now')
		WHERE status='waiting' AND timeout_at != '' AND timeout_at < datetime('now')`
	if err := db.Exec(dbPath, sql3); err != nil {
		log.Warn("cleanup expired human gates failed", "error", err)
	}

	// Delete old completed/rejected/timeout gate history.
	CleanupExpiredHumanGates(dbPath)
}

// CleanupExpiredHumanGates deletes completed, rejected, and timeout human gate
// records whose completed_at is older than 30 days. Waiting records are never deleted.
func CleanupExpiredHumanGates(dbPath string) {
	if dbPath == "" {
		return
	}
	sql := `DELETE FROM workflow_human_gates
		WHERE status IN ('completed', 'rejected', 'timeout')
		AND completed_at != '' AND completed_at < datetime('now', '-30 days')`
	if err := db.Exec(dbPath, sql); err != nil {
		log.Warn("cleanup expired human gates history failed", "error", err)
	}
}

// =============================================================================
// DB helpers — workflow_human_gates table
// =============================================================================

// HumanGateRecord represents a human gate entry in the DB.
type HumanGateRecord struct {
	Key          string
	RunID        string
	StepID       string
	WorkflowName string // name of the workflow that created this gate
	Subtype      string // "approval", "action", "input"
	Prompt       string
	Assignee     string
	Status       string // "waiting", "completed", "timeout", "rejected"
	Decision     string // approval: "approved" / "rejected"
	Response     string // input: human's text response
	RespondedBy  string            // identity of the human who responded (audit trail)
	Options      []string          // custom option labels (parsed from JSON)
	Context      map[string]string // gate card context (parsed from JSON)
	TimeoutAt    string
	CreatedAt    string
	CompletedAt  string
}

const HumanGateTableSQL = `CREATE TABLE IF NOT EXISTS workflow_human_gates (
	key TEXT PRIMARY KEY,
	run_id TEXT NOT NULL,
	step_id TEXT NOT NULL,
	workflow_name TEXT NOT NULL DEFAULT '',
	subtype TEXT NOT NULL,
	prompt TEXT,
	assignee TEXT,
	status TEXT NOT NULL DEFAULT 'waiting',
	decision TEXT,
	response TEXT,
	responded_by TEXT,
	timeout_at TEXT,
	created_at TEXT DEFAULT (datetime('now')),
	completed_at TEXT,
	options TEXT,
	context TEXT
)`

// InitHumanGateTable creates the human gates table if it doesn't exist.
func InitHumanGateTable(dbPath string) {
	if dbPath == "" {
		return
	}
	// Migration: add responded_by column if missing (existing installations).
	if err := db.Exec(dbPath, `ALTER TABLE workflow_human_gates ADD COLUMN responded_by TEXT DEFAULT '';`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") && !strings.Contains(err.Error(), "no such table") {
			log.Warn("workflow_human_gates migration failed", "error", err)
		}
	}
	// Migration: add workflow_name column if missing.
	if err := db.Exec(dbPath, `ALTER TABLE workflow_human_gates ADD COLUMN workflow_name TEXT NOT NULL DEFAULT '';`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") && !strings.Contains(err.Error(), "no such table") {
			log.Warn("workflow_human_gates migration (workflow_name) failed", "error", err)
		}
	}
	// Migration: add options column if missing.
	if err := db.Exec(dbPath, `ALTER TABLE workflow_human_gates ADD COLUMN options TEXT DEFAULT '';`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") && !strings.Contains(err.Error(), "no such table") {
			log.Warn("workflow_human_gates migration (options) failed", "error", err)
		}
	}
	// Migration: add context column if missing.
	if err := db.Exec(dbPath, `ALTER TABLE workflow_human_gates ADD COLUMN context TEXT DEFAULT '';`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") && !strings.Contains(err.Error(), "no such table") {
			log.Warn("workflow_human_gates migration (context) failed", "error", err)
		}
	}
	if err := db.Exec(dbPath, HumanGateTableSQL); err != nil {
		log.Warn("init workflow_human_gates table failed", "error", err)
	}
}

// RecordHumanGate inserts a new human gate record.
func RecordHumanGate(dbPath, key, runID, stepID, workflowName, subtype, prompt, assignee, timeoutAt, options, context string) {
	if dbPath == "" {
		return
	}
	sql := fmt.Sprintf(
		`INSERT OR REPLACE INTO workflow_human_gates (key, run_id, step_id, workflow_name, subtype, prompt, assignee, status, timeout_at, created_at, options, context)
		 VALUES ('%s','%s','%s','%s','%s','%s','%s','waiting','%s',datetime('now'),'%s','%s')`,
		db.Escape(key), db.Escape(runID), db.Escape(stepID), db.Escape(workflowName),
		db.Escape(subtype), db.Escape(prompt), db.Escape(assignee),
		db.Escape(timeoutAt), db.Escape(options), db.Escape(context),
	)
	if err := db.Exec(dbPath, sql); err != nil {
		log.Warn("record human gate failed", "error", err, "key", key)
	}
}

// humanGateSelectCols is the shared SELECT column list for workflow_human_gates queries.
// Keep in sync with parseHumanGateRecord field mappings.
const humanGateSelectCols = `key, run_id, step_id, COALESCE(workflow_name,'') as workflow_name, subtype, prompt, assignee, status, COALESCE(decision,'') as decision, COALESCE(response,'') as response, COALESCE(responded_by,'') as responded_by, COALESCE(timeout_at,'') as timeout_at, created_at, COALESCE(completed_at,'') as completed_at, COALESCE(options,'') as options, COALESCE(context,'') as context`

// QueryHumanGate returns a human gate record by key.
func QueryHumanGate(dbPath, key string) *HumanGateRecord {
	if dbPath == "" {
		return nil
	}
	sql := fmt.Sprintf(
		`SELECT `+humanGateSelectCols+` FROM workflow_human_gates WHERE key='%s' LIMIT 1`,
		db.Escape(key),
	)
	rows, err := db.Query(dbPath, sql)
	if err != nil || len(rows) == 0 {
		return nil
	}
	return parseHumanGateRecord(rows[0])
}

// QueryPendingHumanGatesByRun returns all waiting human gates for a run.
func QueryPendingHumanGatesByRun(dbPath, runID string) []*HumanGateRecord {
	if dbPath == "" {
		return nil
	}
	sql := fmt.Sprintf(
		`SELECT `+humanGateSelectCols+` FROM workflow_human_gates WHERE run_id='%s' AND status='waiting'`,
		db.Escape(runID),
	)
	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil
	}
	var records []*HumanGateRecord
	for _, row := range rows {
		records = append(records, parseHumanGateRecord(row))
	}
	return records
}

// QueryAllPendingHumanGates returns all human gates matching the given status (e.g. "waiting").
// Pass an empty status to return all records regardless of status.
func QueryAllPendingHumanGates(dbPath, status string) []*HumanGateRecord {
	if dbPath == "" {
		return nil
	}
	var where string
	if status != "" {
		where = fmt.Sprintf(" WHERE status='%s'", db.Escape(status))
	}
	sql := fmt.Sprintf(
		`SELECT `+humanGateSelectCols+` FROM workflow_human_gates%s ORDER BY created_at DESC`,
		where,
	)
	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil
	}
	var records []*HumanGateRecord
	for _, row := range rows {
		records = append(records, parseHumanGateRecord(row))
	}
	return records
}

// CountPendingHumanGates returns the number of human gates with status='waiting'.
func CountPendingHumanGates(dbPath string) int {
	if dbPath == "" {
		return 0
	}
	rows, err := db.Query(dbPath, `SELECT COUNT(*) as count FROM workflow_human_gates WHERE status='waiting'`)
	if err != nil || len(rows) == 0 {
		return 0
	}
	switch v := rows[0]["count"].(type) {
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		n, _ := strconv.Atoi(fmt.Sprintf("%v", rows[0]["count"]))
		return n
	}
}

// CompleteHumanGate marks a human gate as completed with decision and response.
func CompleteHumanGate(dbPath, key, decision, response, respondedBy string) {
	if dbPath == "" {
		return
	}
	sql := fmt.Sprintf(
		`UPDATE workflow_human_gates SET status='completed', decision='%s', response='%s', responded_by='%s', completed_at=datetime('now')
		 WHERE key='%s' AND status='waiting'`,
		db.Escape(decision), db.Escape(response), db.Escape(respondedBy), db.Escape(key),
	)
	if err := db.Exec(dbPath, sql); err != nil {
		log.Warn("complete human gate failed", "error", err, "key", key)
	}
}

// RejectHumanGate marks a human gate as rejected.
func RejectHumanGate(dbPath, key, reason, respondedBy string) {
	if dbPath == "" {
		return
	}
	sql := fmt.Sprintf(
		`UPDATE workflow_human_gates SET status='rejected', decision='rejected', response='%s', responded_by='%s', completed_at=datetime('now')
		 WHERE key='%s' AND status='waiting'`,
		db.Escape(reason), db.Escape(respondedBy), db.Escape(key),
	)
	if err := db.Exec(dbPath, sql); err != nil {
		log.Warn("reject human gate failed", "error", err, "key", key)
	}
}

// TimeoutHumanGate marks a human gate as timed out.
func TimeoutHumanGate(dbPath, key string) {
	if dbPath == "" {
		return
	}
	sql := fmt.Sprintf(
		`UPDATE workflow_human_gates SET status='timeout', completed_at=datetime('now')
		 WHERE key='%s' AND status='waiting'`,
		db.Escape(key),
	)
	if err := db.Exec(dbPath, sql); err != nil {
		log.Warn("timeout human gate failed", "error", err, "key", key)
	}
}

// CancelHumanGate marks a human gate as cancelled (workflow context cancelled).
func CancelHumanGate(dbPath, key string) {
	if dbPath == "" {
		return
	}
	sql := fmt.Sprintf(
		`UPDATE workflow_human_gates SET status='cancelled', completed_at=datetime('now')
		 WHERE key='%s' AND status='waiting'`,
		db.Escape(key),
	)
	if err := db.Exec(dbPath, sql); err != nil {
		log.Warn("cancel human gate failed", "error", err, "key", key)
	}
}

// ResetHumanGate resets a human gate for retry (back to waiting).
func ResetHumanGate(dbPath, key string) {
	if dbPath == "" {
		return
	}
	sql := fmt.Sprintf(
		`UPDATE workflow_human_gates SET status='waiting', decision='', response='', completed_at='', timeout_at=''
		 WHERE key='%s'`,
		db.Escape(key),
	)
	if err := db.Exec(dbPath, sql); err != nil {
		log.Warn("reset human gate failed", "error", err, "key", key)
	}
}

// UpdateHumanGateRunID updates the run_id for a human gate (used during recovery).
func UpdateHumanGateRunID(dbPath, key, newRunID string) {
	if dbPath == "" {
		return
	}
	sql := fmt.Sprintf(
		`UPDATE workflow_human_gates SET run_id='%s' WHERE key='%s'`,
		db.Escape(newRunID), db.Escape(key),
	)
	if err := db.Exec(dbPath, sql); err != nil {
		log.Warn("update human gate run_id failed", "error", err, "key", key)
	}
}

func parseHumanGateRecord(row map[string]any) *HumanGateRecord {
	r := &HumanGateRecord{
		Key:          fmt.Sprintf("%v", row["key"]),
		RunID:        fmt.Sprintf("%v", row["run_id"]),
		StepID:       fmt.Sprintf("%v", row["step_id"]),
		WorkflowName: fmt.Sprintf("%v", row["workflow_name"]),
		Subtype:      fmt.Sprintf("%v", row["subtype"]),
		Prompt:       fmt.Sprintf("%v", row["prompt"]),
		Assignee:     fmt.Sprintf("%v", row["assignee"]),
		Status:       fmt.Sprintf("%v", row["status"]),
		Decision:     fmt.Sprintf("%v", row["decision"]),
		Response:     fmt.Sprintf("%v", row["response"]),
		RespondedBy:  fmt.Sprintf("%v", row["responded_by"]),
		TimeoutAt:    fmt.Sprintf("%v", row["timeout_at"]),
		CreatedAt:    fmt.Sprintf("%v", row["created_at"]),
		CompletedAt:  fmt.Sprintf("%v", row["completed_at"]),
	}
	// parse options JSON array
	if opts, ok := row["options"]; ok {
		if s := fmt.Sprintf("%v", opts); s != "" && s != "<nil>" {
			_ = json.Unmarshal([]byte(s), &r.Options)
		}
	}
	// parse context JSON object
	if ctxVal, ok := row["context"]; ok {
		if s := fmt.Sprintf("%v", ctxVal); s != "" && s != "<nil>" {
			_ = json.Unmarshal([]byte(s), &r.Context)
		}
	}
	return r
}

// =============================================================================
// DB helpers — workflow_trigger_runs table
// =============================================================================

const TriggerRunsTableSQL = `CREATE TABLE IF NOT EXISTS workflow_trigger_runs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	trigger_name TEXT NOT NULL,
	workflow_name TEXT NOT NULL,
	workflow_run_id TEXT DEFAULT '',
	status TEXT NOT NULL DEFAULT 'started',
	started_at TEXT NOT NULL,
	finished_at TEXT DEFAULT '',
	error TEXT DEFAULT ''
)`

// InitTriggerRunsTable creates the trigger runs table.
func InitTriggerRunsTable(dbPath string) {
	if dbPath == "" {
		return
	}
	// Migration: add workflow_run_id column if missing.
	if err := db.Exec(dbPath, `ALTER TABLE workflow_trigger_runs ADD COLUMN workflow_run_id TEXT DEFAULT '';`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") && !strings.Contains(err.Error(), "no such table") {
			log.Warn("workflow_trigger_runs migration failed", "error", err)
		}
	}
	if err := db.Exec(dbPath, TriggerRunsTableSQL); err != nil {
		log.Warn("init workflow_trigger_runs table failed", "error", err)
	}
}

// RecordTriggerRun inserts a trigger run record.
func RecordTriggerRun(dbPath, triggerName, workflowName, runID, status, startedAt, finishedAt, errMsg string) {
	if dbPath == "" {
		return
	}
	sql := fmt.Sprintf(
		`INSERT INTO workflow_trigger_runs (trigger_name, workflow_name, workflow_run_id, status, started_at, finished_at, error)
		 VALUES ('%s','%s','%s','%s','%s','%s','%s')`,
		db.Escape(triggerName),
		db.Escape(workflowName),
		db.Escape(runID),
		db.Escape(status),
		db.Escape(startedAt),
		db.Escape(finishedAt),
		db.Escape(errMsg),
	)
	if err := db.Exec(dbPath, sql); err != nil {
		log.Warn("record trigger run failed", "error", err)
	}
}

// QueryTriggerRuns returns recent trigger run records.
func QueryTriggerRuns(dbPath, triggerName string, limit int) ([]map[string]any, error) {
	if limit <= 0 {
		limit = 20
	}

	where := ""
	if triggerName != "" {
		where = fmt.Sprintf("WHERE trigger_name='%s'", db.Escape(triggerName))
	}

	sql := fmt.Sprintf(
		`SELECT id, trigger_name, workflow_name, workflow_run_id, status, started_at, finished_at, error
		 FROM workflow_trigger_runs %s ORDER BY id DESC LIMIT %d`,
		where, limit,
	)

	rows, err := db.Query(dbPath, sql)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil, nil
		}
		return nil, err
	}
	return rows, nil
}

// =============================================================================
// Internal DB parsing helpers
// =============================================================================

// sqlStr safely converts a DB value to string, returning "" for nil/NULL.
func sqlStr(v any) string {
	if v == nil {
		return ""
	}
	s := fmt.Sprintf("%v", v)
	if s == "<nil>" {
		return ""
	}
	return s
}

func parseCallbackRecord(row map[string]any) *CallbackRecord {
	rec := &CallbackRecord{
		Key:        sqlStr(row["key"]),
		RunID:      sqlStr(row["run_id"]),
		StepID:     sqlStr(row["step_id"]),
		Mode:       sqlStr(row["mode"]),
		AuthMode:   sqlStr(row["auth_mode"]),
		URL:        sqlStr(row["url"]),
		Body:       sqlStr(row["body"]),
		Status:     sqlStr(row["status"]),
		TimeoutAt:  sqlStr(row["timeout_at"]),
		ResultBody: sqlStr(row["result_body"]),
	}
	if ps, ok := row["post_sent"]; ok {
		rec.PostSent = fmt.Sprintf("%v", ps) == "1"
	}
	if sq, ok := row["seq"]; ok {
		if n, err := strconv.Atoi(fmt.Sprintf("%v", sq)); err == nil {
			rec.Seq = n
		}
	}
	return rec
}

// =============================================================================
// Trigger Engine
// =============================================================================

// TriggerInfo provides status information about a configured trigger.
type TriggerInfo struct {
	Name         string `json:"name"`
	WorkflowName string `json:"workflowName"`
	Type         string `json:"type"`
	Enabled      bool   `json:"enabled"`
	LastFired    string `json:"lastFired,omitempty"`
	NextCron     string `json:"nextCron,omitempty"`
	Cooldown     string `json:"cooldown,omitempty"`
	CooldownLeft string `json:"cooldownLeft,omitempty"`
}

// TriggerRunResult holds the outcome of a workflow triggered by the engine.
// It mirrors only the fields that WorkflowTriggerEngine needs from WorkflowRun,
// so the engine does not import the root package.
type TriggerRunResult struct {
	ID         string
	Status     string
	Error      string
	DurationMs int64
}

// TriggerDeps holds root-package callbacks that WorkflowTriggerEngine needs.
// This breaks the import cycle: internal/workflow does not import root.
type TriggerDeps struct {
	// ExecuteWorkflow runs a workflow and returns a TriggerRunResult.
	ExecuteWorkflow func(ctx context.Context, cfg *config.Config, wf *Workflow, vars map[string]string) TriggerRunResult

	// LoadWorkflowByName loads a workflow by name. If nil, uses the package-level LoadWorkflowByName.
	LoadWorkflowByName func(cfg *config.Config, name string) (*Workflow, error)
}

// WorkflowTriggerEngine manages workflow triggers: cron-based, event-based, and webhook-based.
type WorkflowTriggerEngine struct {
	cfg       *config.Config
	deps      TriggerDeps
	broker    *dispatch.Broker
	triggers  []config.WorkflowTriggerConfig
	cooldowns map[string]time.Time // trigger name -> cooldown expiry
	lastFired map[string]time.Time // trigger name -> last fire time
	mu        sync.RWMutex
	parentCtx context.Context    // parent context from Start(), preserved for ReloadTriggers
	ctx       context.Context    // engine-scoped context, cancelled on Stop
	cancel    context.CancelFunc
	stopCh    chan struct{}
	wg        sync.WaitGroup
}

// NewWorkflowTriggerEngine creates a new trigger engine with the given dependencies.
func NewWorkflowTriggerEngine(cfg *config.Config, deps TriggerDeps, broker *dispatch.Broker) *WorkflowTriggerEngine {
	e := &WorkflowTriggerEngine{
		cfg:       cfg,
		deps:      deps,
		broker:    broker,
		triggers:  cfg.WorkflowTriggers,
		cooldowns: make(map[string]time.Time),
		lastFired: make(map[string]time.Time),
		ctx:       context.Background(), // safe default; overridden by Start()
		stopCh:    make(chan struct{}),
	}
	// Default loader.
	if e.deps.LoadWorkflowByName == nil {
		e.deps.LoadWorkflowByName = LoadWorkflowByName
	}
	return e
}

// Start launches the cron loop and event listener goroutines.
func (e *WorkflowTriggerEngine) Start(ctx context.Context) {
	e.parentCtx = ctx
	e.ctx, e.cancel = context.WithCancel(ctx)

	if len(e.triggers) == 0 {
		log.Info("workflow trigger engine: no triggers configured")
		return
	}

	hasCron := false
	hasEvent := false
	for _, t := range e.triggers {
		if t.Trigger.Type == "cron" {
			hasCron = true
		}
		if t.Trigger.Type == "event" {
			hasEvent = true
		}
	}

	if hasCron {
		e.wg.Add(1)
		go func() {
			defer e.wg.Done()
			e.cronLoop(ctx)
		}()
	}

	if hasEvent && e.broker != nil {
		e.wg.Add(1)
		go func() {
			defer e.wg.Done()
			e.eventLoop(ctx)
		}()
	}

	// Init trigger runs table.
	InitTriggerRunsTable(e.cfg.HistoryDB)

	enabled := 0
	for _, t := range e.triggers {
		if t.IsEnabled() {
			enabled++
		}
	}
	log.Info("workflow trigger engine started", "total", len(e.triggers), "enabled", enabled, "cron", hasCron, "event", hasEvent)
}

// Stop gracefully shuts down the trigger engine.
func (e *WorkflowTriggerEngine) Stop() {
	close(e.stopCh)
	if e.cancel != nil {
		e.cancel()
	}
	e.wg.Wait()
	log.Info("workflow trigger engine stopped")
}

// ReloadTriggers hot-swaps triggers: stops the current engine loops and restarts with new triggers.
func (e *WorkflowTriggerEngine) ReloadTriggers(triggers []config.WorkflowTriggerConfig) {
	// Stop current loops.
	if e.cancel != nil {
		e.cancel()
	}
	e.wg.Wait()

	// Swap triggers.
	e.mu.Lock()
	e.triggers = triggers
	e.stopCh = make(chan struct{})
	e.mu.Unlock()

	// Restart with stored parent context (preserves shutdown signal).
	parentCtx := e.parentCtx
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	if len(triggers) > 0 {
		e.Start(parentCtx)
	}
	log.Info("workflow triggers reloaded", "count", len(triggers))
}

// cronLoop checks cron triggers every 30 seconds.
func (e *WorkflowTriggerEngine) cronLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	cleanupCounter := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-e.stopCh:
			return
		case <-ticker.C:
			e.checkCronTriggers(ctx)

			// Clean up expired cooldown entries every ~5 minutes (10 ticks × 30s).
			cleanupCounter++
			if cleanupCounter >= 10 {
				cleanupCounter = 0
				e.cleanupExpiredCooldowns()
			}
		}
	}
}

// cleanupExpiredCooldowns removes expired entries from the cooldowns map.
func (e *WorkflowTriggerEngine) cleanupExpiredCooldowns() {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now()
	for k, v := range e.cooldowns {
		if now.After(v) {
			delete(e.cooldowns, k)
		}
	}
}

func (e *WorkflowTriggerEngine) checkCronTriggers(ctx context.Context) {
	now := time.Now()

	e.mu.RLock()
	triggers := e.triggers
	e.mu.RUnlock()

	for _, t := range triggers {
		if !t.IsEnabled() || t.Trigger.Type != "cron" || t.Trigger.Cron == "" {
			continue
		}

		expr, err := cron.Parse(t.Trigger.Cron)
		if err != nil {
			log.Warn("workflow trigger bad cron", "trigger", t.Name, "cron", t.Trigger.Cron, "error", err)
			continue
		}

		// Resolve timezone.
		loc := time.Local
		if t.Trigger.TZ != "" {
			if l, err := time.LoadLocation(t.Trigger.TZ); err == nil {
				loc = l
			}
		}

		nowLocal := now.In(loc)
		if !expr.Matches(nowLocal) {
			continue
		}

		// Avoid double-firing in the same minute.
		e.mu.RLock()
		lastFired := e.lastFired[t.Name]
		e.mu.RUnlock()

		if !lastFired.IsZero() &&
			lastFired.In(loc).Truncate(time.Minute).Equal(nowLocal.Truncate(time.Minute)) {
			continue
		}

		// Check cooldown.
		if !e.checkCooldown(t.Name) {
			log.Debug("workflow trigger cooldown active", "trigger", t.Name)
			continue
		}

		log.Info("workflow trigger cron firing", "trigger", t.Name, "workflow", t.WorkflowName)
		go e.executeTrigger(ctx, t, nil)
	}
}

// eventLoop subscribes to all SSE events and matches event triggers.
func (e *WorkflowTriggerEngine) eventLoop(ctx context.Context) {
	ch, unsub := e.broker.Subscribe("_triggers")
	defer unsub()

	for {
		select {
		case <-ctx.Done():
			return
		case <-e.stopCh:
			return
		case event, ok := <-ch:
			if !ok {
				return
			}
			e.matchEventTriggers(ctx, event)
		}
	}
}

func (e *WorkflowTriggerEngine) matchEventTriggers(ctx context.Context, event dispatch.SSEEvent) {
	e.mu.RLock()
	triggers := e.triggers
	e.mu.RUnlock()

	for _, t := range triggers {
		if !t.IsEnabled() || t.Trigger.Type != "event" || t.Trigger.Event == "" {
			continue
		}

		// Match event type (supports prefix matching with *)
		if !MatchEventType(event.Type, t.Trigger.Event) {
			continue
		}

		if !e.checkCooldown(t.Name) {
			continue
		}

		// Build extra vars from event data.
		extraVars := map[string]string{
			"event_type": event.Type,
			"task_id":    event.TaskID,
			"session_id": event.SessionID,
		}
		if data, ok := event.Data.(map[string]any); ok {
			for k, v := range data {
				extraVars["event_"+k] = fmt.Sprintf("%v", v)
			}
		}

		log.Info("workflow trigger event firing", "trigger", t.Name, "eventType", event.Type)
		go e.executeTrigger(ctx, t, extraVars)
	}
}

// MatchEventType checks if an event type matches a pattern.
// Supports exact match and wildcard prefix (e.g. "workflow_*").
func MatchEventType(eventType, pattern string) bool {
	if pattern == eventType {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(eventType, prefix)
	}
	return false
}

// HandleWebhookTrigger fires a webhook trigger by name with the given payload.
func (e *WorkflowTriggerEngine) HandleWebhookTrigger(triggerName string, payload map[string]string) error {
	e.mu.Lock()
	var found *config.WorkflowTriggerConfig
	for i := range e.triggers {
		t := &e.triggers[i]
		if t.Name == triggerName && t.Trigger.Type == "webhook" {
			found = t
			break
		}
	}

	if found == nil {
		e.mu.Unlock()
		return fmt.Errorf("webhook trigger %q not found", triggerName)
	}
	if !found.IsEnabled() {
		e.mu.Unlock()
		return fmt.Errorf("webhook trigger %q is disabled", triggerName)
	}

	// Check cooldown under write lock to prevent TOCTOU race.
	expiry, ok := e.cooldowns[triggerName]
	if ok && !time.Now().After(expiry) {
		e.mu.Unlock()
		return fmt.Errorf("webhook trigger %q is in cooldown", triggerName)
	}

	// Set cooldown immediately before releasing lock.
	if found.Cooldown != "" {
		if d, err := time.ParseDuration(found.Cooldown); err == nil {
			e.cooldowns[triggerName] = time.Now().Add(d)
		} else {
			log.Warn("webhook trigger cooldown parse failed", "trigger", triggerName, "cooldown", found.Cooldown, "error", err)
		}
	}
	e.lastFired[triggerName] = time.Now()
	triggerCopy := *found
	e.mu.Unlock()

	log.Info("workflow trigger webhook firing", "trigger", triggerName, "workflow", triggerCopy.WorkflowName)
	go e.executeTrigger(e.ctx, triggerCopy, payload)
	return nil
}

// executeTrigger loads the workflow, merges variables, and executes it via deps.ExecuteWorkflow.
func (e *WorkflowTriggerEngine) executeTrigger(ctx context.Context, trigger config.WorkflowTriggerConfig, extraVars map[string]string) {
	startedAt := time.Now()

	// Update last fired and cooldown.
	e.mu.Lock()
	e.lastFired[trigger.Name] = startedAt
	if trigger.Cooldown != "" {
		if d, err := time.ParseDuration(trigger.Cooldown); err == nil {
			e.cooldowns[trigger.Name] = startedAt.Add(d)
		}
	}
	e.mu.Unlock()

	// Load workflow.
	wf, err := e.deps.LoadWorkflowByName(e.cfg, trigger.WorkflowName)
	if err != nil {
		errMsg := fmt.Sprintf("load workflow: %v", err)
		log.Error("workflow trigger exec failed", "trigger", trigger.Name, "error", errMsg)
		RecordTriggerRun(e.cfg.HistoryDB, trigger.Name, trigger.WorkflowName, "", "error",
			startedAt.Format(time.RFC3339), time.Now().Format(time.RFC3339), errMsg)
		return
	}

	// Validate workflow.
	if errs := ValidateWorkflow(wf); len(errs) > 0 {
		errMsg := fmt.Sprintf("validation: %s", strings.Join(errs, "; "))
		log.Error("workflow trigger validation failed", "trigger", trigger.Name, "errors", errs)
		RecordTriggerRun(e.cfg.HistoryDB, trigger.Name, trigger.WorkflowName, "", "error",
			startedAt.Format(time.RFC3339), time.Now().Format(time.RFC3339), errMsg)
		return
	}

	// Merge variables: workflow defaults < trigger config < extra vars.
	vars := make(map[string]string)
	for k, v := range wf.Variables {
		vars[k] = v
	}
	for k, v := range trigger.Variables {
		vars[k] = v
	}
	for k, v := range extraVars {
		vars[k] = v
	}

	// Add trigger metadata as variables.
	vars["_trigger_name"] = trigger.Name
	vars["_trigger_type"] = trigger.Trigger.Type
	vars["_trigger_time"] = startedAt.Format(time.RFC3339)

	// Execute workflow via injected dep.
	run := e.deps.ExecuteWorkflow(ctx, e.cfg, wf, vars)

	// Record trigger run.
	status := "success"
	errMsg := ""
	if run.Status != "success" {
		status = "error"
		errMsg = run.Error
	}
	RecordTriggerRun(e.cfg.HistoryDB, trigger.Name, trigger.WorkflowName, run.ID, status,
		startedAt.Format(time.RFC3339), time.Now().Format(time.RFC3339), errMsg)

	// Publish trigger event.
	if e.broker != nil {
		e.broker.Publish("_triggers", dispatch.SSEEvent{
			Type: "trigger_fired",
			Data: map[string]any{
				"trigger":     trigger.Name,
				"workflow":    trigger.WorkflowName,
				"runId":       run.ID,
				"status":      run.Status,
				"triggerType": trigger.Trigger.Type,
				"durationMs":  run.DurationMs,
			},
		})
	}
}

// checkCooldown returns true if the trigger is past its cooldown period.
func (e *WorkflowTriggerEngine) checkCooldown(triggerName string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()

	expiry, ok := e.cooldowns[triggerName]
	if !ok {
		return true // no cooldown set
	}
	return time.Now().After(expiry)
}

// ListTriggers returns status info for all configured triggers.
func (e *WorkflowTriggerEngine) ListTriggers() []TriggerInfo {
	e.mu.RLock()
	defer e.mu.RUnlock()

	var infos []TriggerInfo
	now := time.Now()

	for _, t := range e.triggers {
		info := TriggerInfo{
			Name:         t.Name,
			WorkflowName: t.WorkflowName,
			Type:         t.Trigger.Type,
			Enabled:      t.IsEnabled(),
			Cooldown:     t.Cooldown,
		}

		// Last fired.
		if lf, ok := e.lastFired[t.Name]; ok {
			info.LastFired = lf.Format(time.RFC3339)
		}

		// Cooldown remaining.
		if expiry, ok := e.cooldowns[t.Name]; ok && now.Before(expiry) {
			info.CooldownLeft = expiry.Sub(now).Round(time.Second).String()
		}

		// Next cron run.
		if t.Trigger.Type == "cron" && t.Trigger.Cron != "" {
			expr, err := cron.Parse(t.Trigger.Cron)
			if err == nil {
				loc := time.Local
				if t.Trigger.TZ != "" {
					if l, err := time.LoadLocation(t.Trigger.TZ); err == nil {
						loc = l
					}
				}
				next := cron.NextRunAfter(expr, loc, now.In(loc))
				if !next.IsZero() {
					info.NextCron = next.Format(time.RFC3339)
				}
			}
		}

		infos = append(infos, info)
	}

	return infos
}

// FireTrigger manually fires a trigger by name.
func (e *WorkflowTriggerEngine) FireTrigger(name string) error {
	e.mu.RLock()
	var found *config.WorkflowTriggerConfig
	for i := range e.triggers {
		if e.triggers[i].Name == name {
			found = &e.triggers[i]
			break
		}
	}
	e.mu.RUnlock()

	if found == nil {
		return fmt.Errorf("trigger %q not found", name)
	}
	if !found.IsEnabled() {
		return fmt.Errorf("trigger %q is disabled", name)
	}

	log.Info("workflow trigger manual fire", "trigger", name, "workflow", found.WorkflowName)
	go e.executeTrigger(e.ctx, *found, map[string]string{
		"_manual": "true",
	})
	return nil
}

// =============================================================================
// toolInputToJSON
// =============================================================================

// ToolInputToJSON converts a map[string]string to json.RawMessage.
func ToolInputToJSON(input map[string]string) json.RawMessage {
	if len(input) == 0 {
		return json.RawMessage(`{}`)
	}
	m := make(map[string]any, len(input))
	for k, v := range input {
		m[k] = v
	}
	data, err := json.Marshal(m)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return data
}
