package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// SystemLogSessionID is a fixed session ID that aggregates all non-chat dispatch task outputs.
const SystemLogSessionID = "system:logs"

// --- Session Types ---

type Session struct {
	ID             string  `json:"id"`
	Agent          string  `json:"agent"`
	Source         string  `json:"source"`
	Status         string  `json:"status"`
	Title          string  `json:"title"`
	ChannelKey     string  `json:"channelKey,omitempty"` // channel session key (e.g. "tg:翡翠", "slack:#ch:ts")
	TotalCost      float64 `json:"totalCost"`
	TotalTokensIn  int     `json:"totalTokensIn"`
	TotalTokensOut int     `json:"totalTokensOut"`
	MessageCount   int     `json:"messageCount"`
	CreatedAt      string  `json:"createdAt"`
	UpdatedAt      string  `json:"updatedAt"`
}

type SessionMessage struct {
	ID        int     `json:"id"`
	SessionID string  `json:"sessionId"`
	Role      string  `json:"role"` // "user", "assistant", "system"
	Content   string  `json:"content"`
	CostUSD   float64 `json:"costUsd"`
	TokensIn  int     `json:"tokensIn"`
	TokensOut int     `json:"tokensOut"`
	Model     string  `json:"model"`
	TaskID    string  `json:"taskId"`
	CreatedAt string  `json:"createdAt"`
}

type SessionQuery struct {
	Agent  string
	Status string
	Source string
	Limit  int
	Offset int
}

type SessionDetail struct {
	Session  Session          `json:"session"`
	Messages []SessionMessage `json:"messages"`
}

// --- DB Init ---

func initSessionDB(dbPath string) error {
	pragmaDB(dbPath)
	sql := `
CREATE TABLE IF NOT EXISTS sessions (
  id TEXT PRIMARY KEY,
  agent TEXT NOT NULL DEFAULT '',
  source TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'active',
  title TEXT NOT NULL DEFAULT '',
  total_cost REAL DEFAULT 0,
  total_tokens_in INTEGER DEFAULT 0,
  total_tokens_out INTEGER DEFAULT 0,
  message_count INTEGER DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sessions_agent ON sessions(agent);
CREATE INDEX IF NOT EXISTS idx_sessions_status ON sessions(status);
CREATE INDEX IF NOT EXISTS idx_sessions_created ON sessions(created_at);
CREATE INDEX IF NOT EXISTS idx_sessions_updated ON sessions(updated_at);

CREATE TABLE IF NOT EXISTS session_messages (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id TEXT NOT NULL,
  role TEXT NOT NULL DEFAULT 'system',
  content TEXT NOT NULL DEFAULT '',
  cost_usd REAL DEFAULT 0,
  tokens_in INTEGER DEFAULT 0,
  tokens_out INTEGER DEFAULT 0,
  model TEXT DEFAULT '',
  task_id TEXT DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_session_messages_session ON session_messages(session_id);
CREATE INDEX IF NOT EXISTS idx_session_messages_created ON session_messages(created_at);
`
	if err := execDB(dbPath, sql); err != nil {
		return fmt.Errorf("init session db: %w", err)
	}

	// Migration: rename role -> agent in sessions table.
	if err := execDB(dbPath, `ALTER TABLE sessions RENAME COLUMN role TO agent;`); err != nil {
		if !strings.Contains(err.Error(), "no such column") && !strings.Contains(err.Error(), "duplicate column") && !strings.Contains(err.Error(), "no such table") {
			logWarn("session migration failed", "column", "role->agent", "error", err)
		}
	}

	// Migration: add channel_key column if it doesn't exist.
	if err := execDB(dbPath, `ALTER TABLE sessions ADD COLUMN channel_key TEXT DEFAULT '';`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") {
			logWarn("session migration failed", "column", "channel_key", "error", err)
		}
	}
	execDB(dbPath, `CREATE INDEX IF NOT EXISTS idx_sessions_channel_key ON sessions(channel_key);`)

	// Ensure system log session exists.
	ensureSystemLogSession(dbPath)

	// Cleanup zombie sessions: mark stale active sessions as completed on startup.
	cleanupSQL := fmt.Sprintf(
		`UPDATE sessions SET status = 'completed', updated_at = '%s' WHERE status = 'active' AND id != '%s'`,
		time.Now().Format(time.RFC3339), SystemLogSessionID,
	)
	if err := execDB(dbPath, cleanupSQL); err != nil {
		logWarn("zombie session cleanup failed", "error", err)
	} else {
		logInfo("startup: cleaned up stale active sessions")
	}

	return nil
}

// ensureSystemLogSession creates the system log session if it doesn't exist.
func ensureSystemLogSession(dbPath string) {
	now := time.Now().Format(time.RFC3339)
	_ = createSession(dbPath, Session{
		ID:        SystemLogSessionID,
		Agent:     "system",
		Source:    "system",
		Status:    "active",
		Title:     "System Dispatch Log",
		CreatedAt: now,
		UpdatedAt: now,
	})
}

// --- Insert ---

func createSession(dbPath string, s Session) error {
	sql := fmt.Sprintf(
		`INSERT OR IGNORE INTO sessions (id, agent, source, status, title, channel_key, total_cost, total_tokens_in, total_tokens_out, message_count, created_at, updated_at)
		 VALUES ('%s','%s','%s','%s','%s','%s',0,0,0,0,'%s','%s')`,
		escapeSQLite(s.ID),
		escapeSQLite(s.Agent),
		escapeSQLite(s.Source),
		escapeSQLite(s.Status),
		escapeSQLite(s.Title),
		escapeSQLite(s.ChannelKey),
		escapeSQLite(s.CreatedAt),
		escapeSQLite(s.UpdatedAt),
	)
	return execDB(dbPath, sql)
}

func addSessionMessage(dbPath string, msg SessionMessage) error {
	// P27.2: Encrypt message content if encryption key is configured.
	content := msg.Content
	if k := globalEncryptionKey(); k != "" {
		if enc, err := encrypt(content, k); err == nil {
			content = enc
		}
	}
	sql := fmt.Sprintf(
		`INSERT INTO session_messages (session_id, role, content, cost_usd, tokens_in, tokens_out, model, task_id, created_at)
		 VALUES ('%s','%s','%s',%f,%d,%d,'%s','%s','%s')`,
		escapeSQLite(msg.SessionID),
		escapeSQLite(msg.Role),
		escapeSQLite(content),
		msg.CostUSD,
		msg.TokensIn,
		msg.TokensOut,
		escapeSQLite(msg.Model),
		escapeSQLite(msg.TaskID),
		escapeSQLite(msg.CreatedAt),
	)
	return execDB(dbPath, sql)
}

// --- Update ---

func updateSessionStats(dbPath, sessionID string, costDelta float64, tokensInDelta, tokensOutDelta, msgCountDelta int) error {
	now := time.Now().Format(time.RFC3339)
	sql := fmt.Sprintf(
		`UPDATE sessions SET
		  total_cost = total_cost + %f,
		  total_tokens_in = total_tokens_in + %d,
		  total_tokens_out = total_tokens_out + %d,
		  message_count = message_count + %d,
		  updated_at = '%s'
		 WHERE id = '%s'`,
		costDelta, tokensInDelta, tokensOutDelta, msgCountDelta,
		escapeSQLite(now), escapeSQLite(sessionID),
	)
	return execDB(dbPath, sql)
}

func updateSessionStatus(dbPath, sessionID, status string) error {
	now := time.Now().Format(time.RFC3339)
	sql := fmt.Sprintf(
		`UPDATE sessions SET status = '%s', updated_at = '%s' WHERE id = '%s'`,
		escapeSQLite(status), escapeSQLite(now), escapeSQLite(sessionID),
	)
	return execDB(dbPath, sql)
}

// updateSessionTitle updates the session title, but only if the current title
// is auto-generated (starts with "New chat with").
func updateSessionTitle(dbPath, sessionID, title string) error {
	now := time.Now().Format(time.RFC3339)
	sql := fmt.Sprintf(
		`UPDATE sessions SET title = '%s', updated_at = '%s' WHERE id = '%s' AND title LIKE 'New chat with%%'`,
		escapeSQLite(title), escapeSQLite(now), escapeSQLite(sessionID),
	)
	if err := execDB(dbPath, sql); err != nil {
		return err
	}
	return nil
}

// --- Query ---

func querySessions(dbPath string, q SessionQuery) ([]Session, int, error) {
	if q.Limit <= 0 {
		q.Limit = 20
	}

	var conditions []string
	if q.Agent != "" {
		conditions = append(conditions, fmt.Sprintf("agent = '%s'", escapeSQLite(q.Agent)))
	}
	if q.Status != "" {
		conditions = append(conditions, fmt.Sprintf("status = '%s'", escapeSQLite(q.Status)))
	}
	if q.Source != "" {
		conditions = append(conditions, fmt.Sprintf("source = '%s'", escapeSQLite(q.Source)))
	}

	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + joinStrings(conditions, " AND ")
	}

	// Count total.
	countSQL := fmt.Sprintf("SELECT COUNT(*) as cnt FROM sessions %s", where)
	countRows, err := queryDB(dbPath, countSQL)
	if err != nil {
		return nil, 0, err
	}
	total := 0
	if len(countRows) > 0 {
		total = jsonInt(countRows[0]["cnt"])
	}

	// Query page.
	dataSQL := fmt.Sprintf(
		`SELECT id, agent, source, status, title, channel_key, total_cost, total_tokens_in, total_tokens_out, message_count, created_at, updated_at
		 FROM sessions %s ORDER BY updated_at DESC LIMIT %d OFFSET %d`,
		where, q.Limit, q.Offset)

	rows, err := queryDB(dbPath, dataSQL)
	if err != nil {
		return nil, 0, err
	}

	var sessions []Session
	for _, row := range rows {
		sessions = append(sessions, sessionFromRow(row))
	}
	return sessions, total, nil
}

func querySessionByID(dbPath, id string) (*Session, error) {
	sql := fmt.Sprintf(
		`SELECT id, agent, source, status, title, channel_key, total_cost, total_tokens_in, total_tokens_out, message_count, created_at, updated_at
		 FROM sessions WHERE id = '%s'`, escapeSQLite(id))
	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	s := sessionFromRow(rows[0])
	return &s, nil
}

// querySessionsByPrefix searches sessions whose id starts with the given prefix.
// Returns all matching sessions ordered by updated_at DESC.
func querySessionsByPrefix(dbPath, prefix string) ([]Session, error) {
	sql := fmt.Sprintf(
		`SELECT id, agent, source, status, title, channel_key, total_cost, total_tokens_in, total_tokens_out, message_count, created_at, updated_at
		 FROM sessions WHERE id LIKE '%s%%' ORDER BY updated_at DESC LIMIT 10`,
		escapeSQLite(prefix))
	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return nil, err
	}
	var sessions []Session
	for _, row := range rows {
		sessions = append(sessions, sessionFromRow(row))
	}
	return sessions, nil
}

func querySessionMessages(dbPath, sessionID string) ([]SessionMessage, error) {
	sql := fmt.Sprintf(
		`SELECT id, session_id, role, content, cost_usd, tokens_in, tokens_out, model, task_id, created_at
		 FROM session_messages WHERE session_id = '%s' ORDER BY id ASC`,
		escapeSQLite(sessionID))
	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return nil, err
	}

	var msgs []SessionMessage
	for _, row := range rows {
		msgs = append(msgs, sessionMessageFromRow(row))
	}
	return msgs, nil
}

// ErrAmbiguousSession is returned by querySessionDetail when a prefix matches
// multiple sessions. The Matches field lists the candidates.
type ErrAmbiguousSession struct {
	Prefix  string
	Matches []Session
}

func (e *ErrAmbiguousSession) Error() string {
	return fmt.Sprintf("ambiguous session ID %q: %d matches", e.Prefix, len(e.Matches))
}

func querySessionDetail(dbPath, sessionID string) (*SessionDetail, error) {
	// Try exact match first.
	sess, err := querySessionByID(dbPath, sessionID)
	if err != nil {
		return nil, err
	}

	// If not found and not a full UUID (36 chars), attempt prefix search.
	if sess == nil && len(sessionID) < 36 {
		matches, err := querySessionsByPrefix(dbPath, sessionID)
		if err != nil {
			return nil, err
		}
		switch len(matches) {
		case 0:
			return nil, nil // not found
		case 1:
			sess = &matches[0]
		default:
			return nil, &ErrAmbiguousSession{Prefix: sessionID, Matches: matches}
		}
	}

	if sess == nil {
		return nil, nil
	}

	msgs, err := querySessionMessages(dbPath, sess.ID)
	if err != nil {
		return nil, err
	}
	if msgs == nil {
		msgs = []SessionMessage{}
	}

	return &SessionDetail{
		Session:  *sess,
		Messages: msgs,
	}, nil
}

func countActiveSessions(dbPath string) int {
	if dbPath == "" {
		return 0
	}
	sql := "SELECT COUNT(*) as cnt FROM sessions WHERE status = 'active'"
	rows, err := queryDB(dbPath, sql)
	if err != nil || len(rows) == 0 {
		return 0
	}
	return jsonInt(rows[0]["cnt"])
}

// countUserSessions returns the number of active sessions excluding system log.
// Used for idle detection — system:logs is always active so we exclude it.
func countUserSessions(dbPath string) int {
	if dbPath == "" {
		return 0
	}
	sql := fmt.Sprintf("SELECT COUNT(*) as cnt FROM sessions WHERE status = 'active' AND id != '%s'", SystemLogSessionID)
	rows, err := queryDB(dbPath, sql)
	if err != nil || len(rows) == 0 {
		return 0
	}
	return jsonInt(rows[0]["cnt"])
}

// --- Cleanup ---

func cleanupSessions(dbPath string, days int) error {
	if dbPath == "" {
		return nil
	}
	// Delete old completed/archived sessions and their messages.
	msgSQL := fmt.Sprintf(
		`DELETE FROM session_messages WHERE session_id IN (
		  SELECT id FROM sessions WHERE status IN ('completed','archived')
		  AND datetime(created_at) < datetime('now','-%d days')
		)`, days)
	if err := execDB(dbPath, msgSQL); err != nil {
		logWarn("cleanup session messages failed", "error", err)
	}

	sessSQL := fmt.Sprintf(
		`DELETE FROM sessions WHERE status IN ('completed','archived')
		 AND datetime(created_at) < datetime('now','-%d days')`, days)
	return execDB(dbPath, sessSQL)
}

// --- Row Parsers ---

func sessionFromRow(row map[string]any) Session {
	return Session{
		ID:             jsonStr(row["id"]),
		Agent:          jsonStr(row["agent"]),
		Source:         jsonStr(row["source"]),
		Status:         jsonStr(row["status"]),
		Title:          jsonStr(row["title"]),
		ChannelKey:     jsonStr(row["channel_key"]),
		TotalCost:      jsonFloat(row["total_cost"]),
		TotalTokensIn:  jsonInt(row["total_tokens_in"]),
		TotalTokensOut: jsonInt(row["total_tokens_out"]),
		MessageCount:   jsonInt(row["message_count"]),
		CreatedAt:      jsonStr(row["created_at"]),
		UpdatedAt:      jsonStr(row["updated_at"]),
	}
}

func sessionMessageFromRow(row map[string]any) SessionMessage {
	content := jsonStr(row["content"])
	// P27.2: Decrypt message content if encryption key is configured.
	if k := globalEncryptionKey(); k != "" {
		if dec, err := decrypt(content, k); err == nil {
			content = dec
		}
	}
	return SessionMessage{
		ID:        jsonInt(row["id"]),
		SessionID: jsonStr(row["session_id"]),
		Role:      jsonStr(row["role"]),
		Content:   content,
		CostUSD:   jsonFloat(row["cost_usd"]),
		TokensIn:  jsonInt(row["tokens_in"]),
		TokensOut: jsonInt(row["tokens_out"]),
		Model:     jsonStr(row["model"]),
		TaskID:    jsonStr(row["task_id"]),
		CreatedAt: jsonStr(row["created_at"]),
	}
}

// --- Channel Session Sync ---

// channelSessionKey builds a channel key for session lookup.
// Examples: channelSessionKey("tg", "翡翠") → "tg:翡翠"
//
//	channelSessionKey("slack", "#general", "1234567890.123456") → "slack:#general:1234567890.123456"
func channelSessionKey(source string, parts ...string) string {
	all := append([]string{source}, parts...)
	return strings.Join(all, ":")
}

// findChannelSession finds the most recent active session with the given channel_key.
// Returns nil if no active session exists for this channel key.
func findChannelSession(dbPath, chKey string) (*Session, error) {
	sql := fmt.Sprintf(
		`SELECT id, agent, source, status, title, channel_key, total_cost, total_tokens_in, total_tokens_out, message_count, created_at, updated_at
		 FROM sessions WHERE channel_key = '%s' AND status = 'active' ORDER BY updated_at DESC LIMIT 1`,
		escapeSQLite(chKey))
	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	s := sessionFromRow(rows[0])
	return &s, nil
}

// getOrCreateChannelSession finds an active session for the channel key,
// or creates a new one if none exists.
func getOrCreateChannelSession(dbPath, source, chKey, role, title string) (*Session, error) {
	sess, err := findChannelSession(dbPath, chKey)
	if err != nil {
		return nil, err
	}
	if sess != nil {
		return sess, nil
	}

	// Create new session.
	now := time.Now().Format(time.RFC3339)
	if title == "" {
		title = fmt.Sprintf("Channel session: %s", role)
	}
	s := Session{
		ID:         newUUID(),
		Agent:      role,
		Source:     source,
		Status:     "active",
		Title:      title,
		ChannelKey: chKey,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := createSession(dbPath, s); err != nil {
		return nil, err
	}
	return &s, nil
}

// archiveChannelSession archives the current active session for a channel key.
func archiveChannelSession(dbPath, chKey string) error {
	sess, err := findChannelSession(dbPath, chKey)
	if err != nil {
		return err
	}
	if sess == nil {
		return nil
	}
	return updateSessionStatus(dbPath, sess.ID, "archived")
}

// --- Context Building ---

// buildSessionContext fetches recent messages from a session and formats them
// as conversation history for prompt injection. Returns empty string if no messages.
func buildSessionContext(dbPath, sessionID string, maxMessages int) string {
	if dbPath == "" || sessionID == "" {
		return ""
	}
	msgs, err := querySessionMessages(dbPath, sessionID)
	if err != nil || len(msgs) == 0 {
		return ""
	}

	// Take last N messages.
	if maxMessages > 0 && len(msgs) > maxMessages {
		msgs = msgs[len(msgs)-maxMessages:]
	}

	var lines []string
	for _, m := range msgs {
		content := m.Content
		if len(content) > 2000 {
			content = content[:2000] + "..."
		}
		lines = append(lines, fmt.Sprintf("[%s] %s", m.Role, content))
	}
	return strings.Join(lines, "\n\n")
}

// buildSessionContextWithLimit fetches session context like buildSessionContext
// but also enforces a character limit on the result. If the context exceeds
// maxChars, it is truncated at a paragraph boundary with a note.
func buildSessionContextWithLimit(dbPath, sessionID string, maxMessages int, maxChars int) string {
	ctx := buildSessionContext(dbPath, sessionID, maxMessages)
	if maxChars > 0 && len(ctx) > maxChars {
		ctx = ctx[:maxChars]
		if idx := strings.LastIndex(ctx, "\n\n"); idx > len(ctx)*3/4 {
			ctx = ctx[:idx]
		}
		ctx += "\n\n[... earlier context truncated ...]"
	}
	return ctx
}

// wrapWithContext prepends conversation history to a new user prompt.
// Returns the original prompt unchanged if there's no context.
func wrapWithContext(sessionContext, prompt string) string {
	if sessionContext == "" {
		return prompt
	}
	return fmt.Sprintf("[Conversation history]\n%s\n\n[Current message]\n%s", sessionContext, prompt)
}

// --- Context Compaction ---

// compactSession summarizes old messages when the session grows too large.
// Keeps the last `keep` messages and replaces older ones with a summary.
// Uses the coordinator role to generate the summary via LLM.
func compactSession(ctx context.Context, cfg *Config, dbPath, sessionID string, sem chan struct{}) error {
	if dbPath == "" {
		return nil
	}

	sess, err := querySessionByID(dbPath, sessionID)
	if err != nil || sess == nil {
		return err
	}

	keep := cfg.Session.compactKeepOrDefault()
	if sess.MessageCount <= keep {
		return nil // not enough messages to compact
	}

	msgs, err := querySessionMessages(dbPath, sessionID)
	if err != nil || len(msgs) <= keep {
		return nil
	}

	// Split: old messages to summarize, recent to keep.
	oldMsgs := msgs[:len(msgs)-keep]

	// Build text to summarize.
	var summaryInput []string
	for _, m := range oldMsgs {
		content := m.Content
		if len(content) > 1000 {
			content = content[:1000] + "..."
		}
		summaryInput = append(summaryInput, fmt.Sprintf("[%s] %s", m.Role, content))
	}

	summaryPrompt := fmt.Sprintf(
		`Summarize this conversation history into a concise context summary (max 500 words).
Focus on key topics discussed, decisions made, and important information.
Output ONLY the summary text, no headers or formatting.

Conversation (%d messages):
%s`,
		len(oldMsgs), strings.Join(summaryInput, "\n"))

	// Run summary via coordinator.
	coordinator := cfg.SmartDispatch.Coordinator
	task := Task{
		Prompt:  summaryPrompt,
		Timeout: "60s",
		Budget:  0.2,
		Source:  "compact",
	}
	fillDefaults(cfg, &task)
	if rc, ok := cfg.Agents[coordinator]; ok && rc.Model != "" {
		task.Model = rc.Model
	}

	result := runSingleTask(ctx, cfg, task, sem, coordinator)
	if result.Status != "success" {
		return fmt.Errorf("compaction summary failed: %s", result.Error)
	}

	summaryText := fmt.Sprintf("[Context Summary] %s", strings.TrimSpace(result.Output))

	// Delete old messages.
	lastOldID := oldMsgs[len(oldMsgs)-1].ID
	delSQL := fmt.Sprintf(
		`DELETE FROM session_messages WHERE session_id = '%s' AND id <= %d`,
		escapeSQLite(sessionID), lastOldID)
	if err := execDB(dbPath, delSQL); err != nil {
		return fmt.Errorf("delete old messages: %w", err)
	}

	// Insert summary as first message.
	now := time.Now().Format(time.RFC3339)
	if err := addSessionMessage(dbPath, SessionMessage{
		SessionID: sessionID,
		Role:      "system",
		Content:   truncateStr(summaryText, 5000),
		CostUSD:   result.CostUSD,
		Model:     result.Model,
		CreatedAt: now,
	}); err != nil {
		return fmt.Errorf("insert summary: %w", err)
	}

	// Update message count: kept messages + 1 summary.
	newCount := keep + 1
	updateSQL := fmt.Sprintf(
		`UPDATE sessions SET message_count = %d, updated_at = '%s' WHERE id = '%s'`,
		newCount, escapeSQLite(now), escapeSQLite(sessionID))
	if err := execDB(dbPath, updateSQL); err != nil {
		logWarn("session count update failed", "session", sessionID, "error", err)
	}

	logInfo("session compacted", "session", sessionID[:8], "before", len(msgs), "after", newCount, "kept", keep)
	return nil
}

// maybeCompactSession triggers compaction if the session exceeds the threshold.
// Non-blocking: runs in a goroutine.
func maybeCompactSession(cfg *Config, dbPath, sessionID string, msgCount int, sem chan struct{}) {
	threshold := cfg.Session.compactAfterOrDefault()
	if msgCount <= threshold {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := compactSession(ctx, cfg, dbPath, sessionID, sem); err != nil {
			logWarn("session compaction failed", "session", sessionID, "error", err)
		}
	}()
}

// --- Recording Helper ---

// recordSessionActivity records user message (prompt) and assistant/system response
// for a completed task execution. Creates the session if it doesn't exist.
// Non-blocking: runs in a goroutine to avoid adding latency to task execution.
func recordSessionActivity(dbPath string, task Task, result TaskResult, role string) {
	if dbPath == "" {
		return
	}
	go func() {
		sessionID := result.SessionID
		if sessionID == "" {
			sessionID = task.SessionID
		}
		if sessionID == "" {
			return
		}
		now := time.Now().Format(time.RFC3339)

		// Auto-generate title from prompt.
		title := task.Prompt
		if len(title) > 100 {
			title = title[:100]
		}

		// Create session (INSERT OR IGNORE — idempotent).
		if err := createSession(dbPath, Session{
			ID:        sessionID,
			Agent:     role,
			Source:     task.Source,
			Status:    "active",
			Title:     title,
			CreatedAt: now,
			UpdatedAt: now,
		}); err != nil {
			logWarn("create session failed", "session", sessionID, "error", err)
		}

		// Add user message.
		if err := addSessionMessage(dbPath, SessionMessage{
			SessionID: sessionID,
			Role:      "user",
			Content:   truncateStr(task.Prompt, 5000),
			TaskID:    task.ID,
			CreatedAt: now,
		}); err != nil {
			logWarn("add user message failed", "session", sessionID, "error", err)
		}

		// Add assistant or system message.
		msgRole := "assistant"
		content := truncateStr(result.Output, 5000)
		if result.Status != "success" {
			msgRole = "system"
			errMsg := result.Error
			if errMsg == "" {
				errMsg = result.Status
			}
			content = fmt.Sprintf("[%s] %s", result.Status, truncateStr(errMsg, 2000))
		}
		if err := addSessionMessage(dbPath, SessionMessage{
			SessionID: sessionID,
			Role:      msgRole,
			Content:   content,
			CostUSD:   result.CostUSD,
			TokensIn:  result.TokensIn,
			TokensOut: result.TokensOut,
			Model:     result.Model,
			TaskID:    task.ID,
			CreatedAt: now,
		}); err != nil {
			logWarn("add assistant message failed", "session", sessionID, "error", err)
		}

		// Update session aggregates (2 messages added: user + assistant).
		if err := updateSessionStats(dbPath, sessionID, result.CostUSD, result.TokensIn, result.TokensOut, 2); err != nil {
			logWarn("update session stats failed", "session", sessionID, "error", err)
		}

		// Mark session completed once the task reaches any terminal state.
		// Multi-turn sessions via /sessions/{id}/message won't hit this path.
		// Channel-bound sessions (Discord, etc.) stay active for conversation continuity.
		existing, _ := querySessionByID(dbPath, sessionID)
		if existing == nil || existing.ChannelKey == "" {
			updateSessionStatus(dbPath, sessionID, "completed")
		}
	}()
}

// logSystemDispatch appends a summary of a dispatch task to the system log session.
// This allows dashboard users to see all non-chat dispatch outputs in one place.
func logSystemDispatch(dbPath string, task Task, result TaskResult, role string) {
	if dbPath == "" || task.ID == "" {
		return
	}
	go func() {
		now := time.Now().Format(time.RFC3339)
		taskShort := task.ID
		if len(taskShort) > 8 {
			taskShort = taskShort[:8]
		}
		statusLabel := "✓"
		if result.Status != "success" {
			statusLabel = "✗"
		}
		output := truncateStr(result.Output, 1000)
		if result.Status != "success" {
			errMsg := result.Error
			if errMsg == "" {
				errMsg = result.Status
			}
			output = truncateStr(errMsg, 500)
		}
		content := fmt.Sprintf("[%s] %s · %s · %s · $%.4f\n\n**Prompt:** %s\n\n**Output:**\n%s",
			statusLabel, taskShort, role, task.Source, result.CostUSD,
			truncateStr(task.Prompt, 300),
			output,
		)
		if err := addSessionMessage(dbPath, SessionMessage{
			SessionID: SystemLogSessionID,
			Role:      "system",
			Content:   content,
			CostUSD:   result.CostUSD,
			TokensIn:  result.TokensIn,
			TokensOut: result.TokensOut,
			Model:     result.Model,
			TaskID:    task.ID,
			CreatedAt: now,
		}); err != nil {
			logWarn("logSystemDispatch: add message failed", "task", task.ID, "error", err)
			return
		}
		_ = updateSessionStats(dbPath, SystemLogSessionID, result.CostUSD, result.TokensIn, result.TokensOut, 1)
	}()
}
