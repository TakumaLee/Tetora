// Package session manages conversation sessions and messages.
package session

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"tetora/internal/db"
)

// --- Encryption hooks (set by root wire file) ---

// EncryptFn encrypts content before DB storage. Nil = no encryption.
var EncryptFn func(plaintext, key string) (string, error)

// DecryptFn decrypts content after DB retrieval. Nil = no decryption.
var DecryptFn func(ciphertext, key string) (string, error)

// EncryptionKeyFn returns the current encryption key. Nil or "" = no encryption.
var EncryptionKeyFn func() string

// sessionAgentCol is the actual column name for the agent field in the sessions table.
// Old schemas use "role", new schemas use "agent". Detected once at init time.
var sessionAgentCol = "agent"

// sessionSelectCols returns the SELECT column list for session queries.
func sessionSelectCols() string {
	col := sessionAgentCol
	alias := col
	if col != "agent" {
		alias = col + " AS agent"
	}
	return fmt.Sprintf(
		"id, %s, source, status, title, channel_key, total_cost, total_tokens_in, total_tokens_out, message_count, created_at, updated_at,"+
			" COALESCE((SELECT tokens_in FROM session_messages WHERE session_id = id ORDER BY id DESC LIMIT 1), 0) AS context_size",
		alias)
}

// SystemLogSessionID is a fixed session ID that aggregates all non-chat dispatch task outputs.
const SystemLogSessionID = "system:logs"

// --- Session Types ---

type Session struct {
	ID             string  `json:"id"`
	Agent          string  `json:"agent"`
	Source         string  `json:"source"`
	Status         string  `json:"status"`
	Title          string  `json:"title"`
	ChannelKey     string  `json:"channelKey,omitempty"`
	TotalCost      float64 `json:"totalCost"`
	TotalTokensIn  int     `json:"totalTokensIn"`
	TotalTokensOut int     `json:"totalTokensOut"`
	MessageCount   int     `json:"messageCount"`
	ContextSize    int     `json:"contextSize"`
	CreatedAt      string  `json:"createdAt"`
	UpdatedAt      string  `json:"updatedAt"`
}

type SessionMessage struct {
	ID        int     `json:"id"`
	SessionID string  `json:"sessionId"`
	Role      string  `json:"role"`
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

// ErrAmbiguousSession is returned when a prefix matches multiple sessions.
type ErrAmbiguousSession struct {
	Prefix  string
	Matches []Session
}

func (e *ErrAmbiguousSession) Error() string {
	return fmt.Sprintf("ambiguous session ID %q: %d matches", e.Prefix, len(e.Matches))
}

// CleanupSessionStats holds the results of a sessions cleanup operation.
type CleanupSessionStats struct {
	SessionsDeleted int
	MessagesDeleted int
	OrphansFixed    int
	DryRun          bool
	Sessions        []Session
}

// --- DB Init ---

func InitSessionDB(dbPath string) error {
	db.Pragma(dbPath)
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
	if err := db.Exec(dbPath, sql); err != nil {
		return fmt.Errorf("init session db: %w", err)
	}

	migrateRoleToAgent(dbPath)

	cols := tableColumns(dbPath, "sessions")
	if cols["agent"] {
		sessionAgentCol = "agent"
	} else if cols["role"] {
		sessionAgentCol = "role"
		slog.Warn("session table still uses 'role' column — migration may have failed")
	}
	db.Exec(dbPath, fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_sessions_agent ON sessions(%s);`, sessionAgentCol))

	if err := db.Exec(dbPath, `ALTER TABLE sessions ADD COLUMN channel_key TEXT DEFAULT '';`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") {
			slog.Warn("session migration failed", "column", "channel_key", "error", err)
		}
	}
	db.Exec(dbPath, `CREATE INDEX IF NOT EXISTS idx_sessions_channel_key ON sessions(channel_key);`)

	ensureSystemLogSession(dbPath)

	return nil
}

// CleanupZombieSessions marks stale active sessions as completed.
func CleanupZombieSessions(dbPath string) {
	if dbPath == "" {
		return
	}
	sql := fmt.Sprintf(
		`UPDATE sessions SET status = 'completed', updated_at = '%s' WHERE status = 'active' AND id != '%s' AND source != 'discord'`,
		time.Now().Format(time.RFC3339), SystemLogSessionID,
	)
	if err := db.Exec(dbPath, sql); err != nil {
		slog.Warn("zombie session cleanup failed", "error", err)
	} else {
		slog.Info("startup: cleaned up stale active sessions")
	}
}

func migrateRoleToAgent(dbPath string) {
	cols := tableColumns(dbPath, "sessions")
	hasAgent := cols["agent"]
	hasRole := cols["role"]

	if hasAgent {
		return
	}
	if !hasRole {
		return
	}

	if err := db.Exec(dbPath, `ALTER TABLE sessions ADD COLUMN agent TEXT DEFAULT '';`); err != nil {
		slog.Warn("migration: add agent column failed", "error", err)
		return
	}
	if err := db.Exec(dbPath, `UPDATE sessions SET agent = role WHERE agent = '' OR agent IS NULL;`); err != nil {
		slog.Warn("migration: copy role→agent failed", "error", err)
	}
	db.Exec(dbPath, `CREATE INDEX IF NOT EXISTS idx_sessions_agent ON sessions(agent);`)
	slog.Info("migration: role→agent column added and data copied")
}

func tableColumns(dbPath, table string) map[string]bool {
	rows, err := db.Query(dbPath, fmt.Sprintf("PRAGMA table_info(%s);", table))
	if err != nil {
		return nil
	}
	cols := make(map[string]bool, len(rows))
	for _, row := range rows {
		if name, ok := row["name"].(string); ok {
			cols[name] = true
		}
	}
	return cols
}

func ensureSystemLogSession(dbPath string) {
	now := time.Now().Format(time.RFC3339)
	_ = CreateSession(dbPath, Session{
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

func CreateSession(dbPath string, s Session) error {
	sql := fmt.Sprintf(
		`INSERT OR IGNORE INTO sessions (id, %s, source, status, title, channel_key, total_cost, total_tokens_in, total_tokens_out, message_count, created_at, updated_at)
		 VALUES ('%s','%s','%s','%s','%s','%s',0,0,0,0,'%s','%s')`,
		sessionAgentCol,
		db.Escape(s.ID),
		db.Escape(s.Agent),
		db.Escape(s.Source),
		db.Escape(s.Status),
		db.Escape(s.Title),
		db.Escape(s.ChannelKey),
		db.Escape(s.CreatedAt),
		db.Escape(s.UpdatedAt),
	)
	return db.Exec(dbPath, sql)
}

func AddSessionMessage(dbPath string, msg SessionMessage) error {
	content := msg.Content
	if k := getEncryptionKey(); k != "" {
		if EncryptFn != nil {
			if enc, err := EncryptFn(content, k); err == nil {
				content = enc
			}
		}
	}
	sql := fmt.Sprintf(
		`INSERT INTO session_messages (session_id, role, content, cost_usd, tokens_in, tokens_out, model, task_id, created_at)
		 VALUES ('%s','%s','%s',%f,%d,%d,'%s','%s','%s')`,
		db.Escape(msg.SessionID),
		db.Escape(msg.Role),
		db.Escape(content),
		msg.CostUSD,
		msg.TokensIn,
		msg.TokensOut,
		db.Escape(msg.Model),
		db.Escape(msg.TaskID),
		db.Escape(msg.CreatedAt),
	)
	return db.Exec(dbPath, sql)
}

// --- Update ---

func UpdateSessionStats(dbPath, sessionID string, costDelta float64, tokensInDelta, tokensOutDelta, msgCountDelta int) error {
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
		db.Escape(now), db.Escape(sessionID),
	)
	return db.Exec(dbPath, sql)
}

func UpdateSessionStatus(dbPath, sessionID, status string) error {
	now := time.Now().Format(time.RFC3339)
	sql := fmt.Sprintf(
		`UPDATE sessions SET status = '%s', updated_at = '%s' WHERE id = '%s'`,
		db.Escape(status), db.Escape(now), db.Escape(sessionID),
	)
	return db.Exec(dbPath, sql)
}

// CreateSessionCtx is like CreateSession but respects context cancellation.
func CreateSessionCtx(ctx context.Context, dbPath string, s Session) error {
	sql := fmt.Sprintf(
		`INSERT OR IGNORE INTO sessions (id, %s, source, status, title, channel_key, total_cost, total_tokens_in, total_tokens_out, message_count, created_at, updated_at)
		 VALUES ('%s','%s','%s','%s','%s','%s',0,0,0,0,'%s','%s')`,
		sessionAgentCol,
		db.Escape(s.ID),
		db.Escape(s.Agent),
		db.Escape(s.Source),
		db.Escape(s.Status),
		db.Escape(s.Title),
		db.Escape(s.ChannelKey),
		db.Escape(s.CreatedAt),
		db.Escape(s.UpdatedAt),
	)
	return db.ExecContext(ctx, dbPath, sql)
}

// AddSessionMessageCtx is like AddSessionMessage but respects context cancellation.
func AddSessionMessageCtx(ctx context.Context, dbPath string, msg SessionMessage) error {
	content := msg.Content
	if k := getEncryptionKey(); k != "" {
		if EncryptFn != nil {
			if enc, err := EncryptFn(content, k); err == nil {
				content = enc
			}
		}
	}
	sql := fmt.Sprintf(
		`INSERT INTO session_messages (session_id, role, content, cost_usd, tokens_in, tokens_out, model, task_id, created_at)
		 VALUES ('%s','%s','%s',%f,%d,%d,'%s','%s','%s')`,
		db.Escape(msg.SessionID),
		db.Escape(msg.Role),
		db.Escape(content),
		msg.CostUSD,
		msg.TokensIn,
		msg.TokensOut,
		db.Escape(msg.Model),
		db.Escape(msg.TaskID),
		db.Escape(msg.CreatedAt),
	)
	return db.ExecContext(ctx, dbPath, sql)
}

// UpdateSessionStatsCtx is like UpdateSessionStats but respects context cancellation.
func UpdateSessionStatsCtx(ctx context.Context, dbPath, sessionID string, costDelta float64, tokensInDelta, tokensOutDelta, msgCountDelta int) error {
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
		db.Escape(now), db.Escape(sessionID),
	)
	return db.ExecContext(ctx, dbPath, sql)
}

// UpdateSessionStatusCtx is like UpdateSessionStatus but respects context cancellation.
func UpdateSessionStatusCtx(ctx context.Context, dbPath, sessionID, status string) error {
	now := time.Now().Format(time.RFC3339)
	sql := fmt.Sprintf(
		`UPDATE sessions SET status = '%s', updated_at = '%s' WHERE id = '%s'`,
		db.Escape(status), db.Escape(now), db.Escape(sessionID),
	)
	return db.ExecContext(ctx, dbPath, sql)
}

func UpdateSessionTitle(dbPath, sessionID, title string) error {
	now := time.Now().Format(time.RFC3339)
	sql := fmt.Sprintf(
		`UPDATE sessions SET title = '%s', updated_at = '%s' WHERE id = '%s' AND title LIKE 'New chat with%%'`,
		db.Escape(title), db.Escape(now), db.Escape(sessionID),
	)
	return db.Exec(dbPath, sql)
}

// --- Query ---

func QuerySessions(dbPath string, q SessionQuery) ([]Session, int, error) {
	if q.Limit <= 0 {
		q.Limit = 20
	}

	var conditions []string
	if q.Agent != "" {
		conditions = append(conditions, fmt.Sprintf("%s = '%s'", sessionAgentCol, db.Escape(q.Agent)))
	}
	if q.Status != "" {
		conditions = append(conditions, fmt.Sprintf("status = '%s'", db.Escape(q.Status)))
	}
	if q.Source != "" {
		conditions = append(conditions, fmt.Sprintf("source = '%s'", db.Escape(q.Source)))
	}

	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	countSQL := fmt.Sprintf("SELECT COUNT(*) as cnt FROM sessions %s", where)
	countRows, err := db.Query(dbPath, countSQL)
	if err != nil {
		return nil, 0, err
	}
	total := 0
	if len(countRows) > 0 {
		total = db.Int(countRows[0]["cnt"])
	}

	dataSQL := fmt.Sprintf(
		`SELECT `+sessionSelectCols()+`
		 FROM sessions %s ORDER BY updated_at DESC LIMIT %d OFFSET %d`,
		where, q.Limit, q.Offset)

	rows, err := db.Query(dbPath, dataSQL)
	if err != nil {
		return nil, 0, err
	}

	var sessions []Session
	for _, row := range rows {
		sessions = append(sessions, sessionFromRow(row))
	}
	return sessions, total, nil
}

func QuerySessionByID(dbPath, id string) (*Session, error) {
	sql := fmt.Sprintf(
		`SELECT `+sessionSelectCols()+`
		 FROM sessions WHERE id = '%s'`, db.Escape(id))
	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	s := sessionFromRow(rows[0])
	return &s, nil
}

func QuerySessionByIDCtx(ctx context.Context, dbPath, id string) (*Session, error) {
	sql := fmt.Sprintf(
		`SELECT `+sessionSelectCols()+`
		 FROM sessions WHERE id = '%s'`, db.Escape(id))
	rows, err := db.QueryContext(ctx, dbPath, sql)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	s := sessionFromRow(rows[0])
	return &s, nil
}

func QuerySessionsByPrefix(dbPath, prefix string) ([]Session, error) {
	sql := fmt.Sprintf(
		`SELECT `+sessionSelectCols()+`
		 FROM sessions WHERE id LIKE '%s%%' ORDER BY updated_at DESC LIMIT 10`,
		db.Escape(prefix))
	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil, err
	}
	var sessions []Session
	for _, row := range rows {
		sessions = append(sessions, sessionFromRow(row))
	}
	return sessions, nil
}

func QuerySessionMessages(dbPath, sessionID string) ([]SessionMessage, error) {
	sql := fmt.Sprintf(
		`SELECT id, session_id, role, content, cost_usd, tokens_in, tokens_out, model, task_id, created_at
		 FROM session_messages WHERE session_id = '%s' ORDER BY id ASC`,
		db.Escape(sessionID))
	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil, err
	}

	var msgs []SessionMessage
	for _, row := range rows {
		msgs = append(msgs, SessionMessageFromRow(row))
	}
	return msgs, nil
}

func QuerySessionDetail(dbPath, sessionID string) (*SessionDetail, error) {
	sess, err := QuerySessionByID(dbPath, sessionID)
	if err != nil {
		return nil, err
	}

	if sess == nil && len(sessionID) < 36 {
		matches, err := QuerySessionsByPrefix(dbPath, sessionID)
		if err != nil {
			return nil, err
		}
		switch len(matches) {
		case 0:
			return nil, nil
		case 1:
			sess = &matches[0]
		default:
			return nil, &ErrAmbiguousSession{Prefix: sessionID, Matches: matches}
		}
	}

	if sess == nil {
		return nil, nil
	}

	msgs, err := QuerySessionMessages(dbPath, sess.ID)
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

func CountActiveSessions(dbPath string) int {
	if dbPath == "" {
		return 0
	}
	sql := "SELECT COUNT(*) as cnt FROM sessions WHERE status = 'active'"
	rows, err := db.Query(dbPath, sql)
	if err != nil || len(rows) == 0 {
		return 0
	}
	return db.Int(rows[0]["cnt"])
}

func CountUserSessions(dbPath string) int {
	if dbPath == "" {
		return 0
	}
	sql := fmt.Sprintf("SELECT COUNT(*) as cnt FROM sessions WHERE status = 'active' AND id != '%s'", SystemLogSessionID)
	rows, err := db.Query(dbPath, sql)
	if err != nil || len(rows) == 0 {
		return 0
	}
	return db.Int(rows[0]["cnt"])
}

// --- Cleanup ---

func CleanupSessions(dbPath string, days int) error {
	if dbPath == "" {
		return nil
	}
	msgSQL := fmt.Sprintf(
		`DELETE FROM session_messages WHERE session_id IN (
		  SELECT id FROM sessions WHERE status IN ('completed','archived')
		  AND datetime(created_at) < datetime('now','-%d days')
		)`, days)
	if err := db.Exec(dbPath, msgSQL); err != nil {
		slog.Warn("cleanup session messages failed", "error", err)
	}

	sessSQL := fmt.Sprintf(
		`DELETE FROM sessions WHERE status IN ('completed','archived')
		 AND datetime(created_at) < datetime('now','-%d days')`, days)
	return db.Exec(dbPath, sessSQL)
}

func CleanupSessionsWithStats(dbPath string, days int, dryRun bool) (CleanupSessionStats, error) {
	var stats CleanupSessionStats
	stats.DryRun = dryRun

	if dbPath == "" {
		return stats, nil
	}

	countSQL := fmt.Sprintf(
		`SELECT COUNT(*) as cnt FROM sessions WHERE status IN ('completed','archived')
		 AND datetime(created_at) < datetime('now','-%d days')`, days)
	rows, err := db.Query(dbPath, countSQL)
	if err != nil {
		return stats, fmt.Errorf("count sessions: %w", err)
	}
	if len(rows) > 0 {
		stats.SessionsDeleted = db.Int(rows[0]["cnt"])
	}

	msgCountSQL := fmt.Sprintf(
		`SELECT COUNT(*) as cnt FROM session_messages WHERE session_id IN (
		  SELECT id FROM sessions WHERE status IN ('completed','archived')
		  AND datetime(created_at) < datetime('now','-%d days')
		)`, days)
	mrows, err := db.Query(dbPath, msgCountSQL)
	if err != nil {
		return stats, fmt.Errorf("count messages: %w", err)
	}
	if len(mrows) > 0 {
		stats.MessagesDeleted = db.Int(mrows[0]["cnt"])
	}

	if dryRun {
		listSQL := fmt.Sprintf(
			`SELECT `+sessionSelectCols()+`
			 FROM sessions WHERE status IN ('completed','archived')
			 AND datetime(created_at) < datetime('now','-%d days')
			 ORDER BY created_at ASC`, days)
		srows, err := db.Query(dbPath, listSQL)
		if err != nil {
			return stats, fmt.Errorf("list sessions: %w", err)
		}
		for _, r := range srows {
			stats.Sessions = append(stats.Sessions, sessionFromRow(r))
		}
		return stats, nil
	}

	msgDelSQL := fmt.Sprintf(
		`DELETE FROM session_messages WHERE session_id IN (
		  SELECT id FROM sessions WHERE status IN ('completed','archived')
		  AND datetime(created_at) < datetime('now','-%d days')
		)`, days)
	if err := db.Exec(dbPath, msgDelSQL); err != nil {
		slog.Warn("cleanup session messages failed", "error", err)
	}

	sessDelSQL := fmt.Sprintf(
		`DELETE FROM sessions WHERE status IN ('completed','archived')
		 AND datetime(created_at) < datetime('now','-%d days')`, days)
	if err := db.Exec(dbPath, sessDelSQL); err != nil {
		return stats, fmt.Errorf("delete sessions: %w", err)
	}

	return stats, nil
}

func FixMissingSessions(dbPath string, days int, dryRun bool) (int, error) {
	if dbPath == "" {
		return 0, nil
	}

	countSQL := fmt.Sprintf(
		`SELECT COUNT(*) as cnt FROM sessions
		 WHERE status = 'active'
		 AND id != '%s'
		 AND datetime(updated_at) < datetime('now','-%d days')`,
		SystemLogSessionID, days)
	rows, err := db.Query(dbPath, countSQL)
	if err != nil {
		return 0, fmt.Errorf("count orphan sessions: %w", err)
	}
	count := 0
	if len(rows) > 0 {
		count = db.Int(rows[0]["cnt"])
	}
	if dryRun || count == 0 {
		return count, nil
	}

	now := time.Now().Format(time.RFC3339)
	fixSQL := fmt.Sprintf(
		`UPDATE sessions SET status = 'completed', updated_at = '%s'
		 WHERE status = 'active'
		 AND id != '%s'
		 AND datetime(updated_at) < datetime('now','-%d days')`,
		now, SystemLogSessionID, days)
	if err := db.Exec(dbPath, fixSQL); err != nil {
		return 0, fmt.Errorf("fix orphan sessions: %w", err)
	}
	return count, nil
}

// --- Row Parsers ---

func sessionFromRow(row map[string]any) Session {
	return Session{
		ID:             db.Str(row["id"]),
		Agent:          db.Str(row["agent"]),
		Source:         db.Str(row["source"]),
		Status:         db.Str(row["status"]),
		Title:          db.Str(row["title"]),
		ChannelKey:     db.Str(row["channel_key"]),
		TotalCost:      db.Float(row["total_cost"]),
		TotalTokensIn:  db.Int(row["total_tokens_in"]),
		TotalTokensOut: db.Int(row["total_tokens_out"]),
		MessageCount:   db.Int(row["message_count"]),
		ContextSize:    db.Int(row["context_size"]),
		CreatedAt:      db.Str(row["created_at"]),
		UpdatedAt:      db.Str(row["updated_at"]),
	}
}

func SessionMessageFromRow(row map[string]any) SessionMessage {
	content := db.Str(row["content"])
	if k := getEncryptionKey(); k != "" {
		if DecryptFn != nil {
			if dec, err := DecryptFn(content, k); err == nil {
				content = dec
			}
		}
	}
	return SessionMessage{
		ID:        db.Int(row["id"]),
		SessionID: db.Str(row["session_id"]),
		Role:      db.Str(row["role"]),
		Content:   content,
		CostUSD:   db.Float(row["cost_usd"]),
		TokensIn:  db.Int(row["tokens_in"]),
		TokensOut: db.Int(row["tokens_out"]),
		Model:     db.Str(row["model"]),
		TaskID:    db.Str(row["task_id"]),
		CreatedAt: db.Str(row["created_at"]),
	}
}

// --- Channel Session Sync ---

func ChannelSessionKey(source string, parts ...string) string {
	all := append([]string{source}, parts...)
	return strings.Join(all, ":")
}

func FindChannelSession(dbPath, chKey string) (*Session, error) {
	sql := fmt.Sprintf(
		`SELECT `+sessionSelectCols()+`
		 FROM sessions WHERE channel_key = '%s' AND status = 'active' ORDER BY updated_at DESC LIMIT 1`,
		db.Escape(chKey))
	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	s := sessionFromRow(rows[0])
	return &s, nil
}

// FindLastArchivedChannelSession returns the most recently archived session
// for the given channel key, or nil if none exists.
func FindLastArchivedChannelSession(dbPath, chKey string) (*Session, error) {
	sql := fmt.Sprintf(
		`SELECT `+sessionSelectCols()+`
		 FROM sessions WHERE channel_key = '%s' AND status = 'archived' ORDER BY updated_at DESC LIMIT 1`,
		db.Escape(chKey))
	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	s := sessionFromRow(rows[0])
	return &s, nil
}

func GetOrCreateChannelSession(dbPath, source, chKey, role, title string) (*Session, error) {
	sess, err := FindChannelSession(dbPath, chKey)
	if err != nil {
		return nil, err
	}
	if sess != nil {
		if role != "" && sess.Agent != role {
			slog.Info("channel session agent changed, archiving old session",
				"channelKey", chKey, "oldAgent", sess.Agent, "newAgent", role, "sessionId", sess.ID)
			_ = UpdateSessionStatus(dbPath, sess.ID, "archived")
		} else {
			return sess, nil
		}
	}

	now := time.Now().Format(time.RFC3339)
	if title == "" {
		title = fmt.Sprintf("Channel session: %s", role)
	}
	s := Session{
		ID:         NewUUID(),
		Agent:      role,
		Source:     source,
		Status:     "active",
		Title:      title,
		ChannelKey: chKey,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := CreateSession(dbPath, s); err != nil {
		return nil, err
	}
	return &s, nil
}

func ArchiveChannelSession(dbPath, chKey string) error {
	sess, err := FindChannelSession(dbPath, chKey)
	if err != nil {
		return err
	}
	if sess == nil {
		return nil
	}
	return UpdateSessionStatus(dbPath, sess.ID, "archived")
}

// --- Context Building ---

func BuildSessionContext(dbPath, sessionID string, maxMessages int) string {
	if dbPath == "" || sessionID == "" {
		return ""
	}
	msgs, err := QuerySessionMessages(dbPath, sessionID)
	if err != nil || len(msgs) == 0 {
		return ""
	}

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

func BuildSessionContextWithLimit(dbPath, sessionID string, maxMessages int, maxChars int) string {
	ctx := BuildSessionContext(dbPath, sessionID, maxMessages)
	if maxChars > 0 && len(ctx) > maxChars {
		ctx = ctx[:maxChars]
		if idx := strings.LastIndex(ctx, "\n\n"); idx > len(ctx)*3/4 {
			ctx = ctx[:idx]
		}
		ctx += "\n\n[... earlier context truncated ...]"
	}
	return ctx
}

func WrapWithContext(sessionContext, prompt string) string {
	if sessionContext == "" {
		return prompt
	}
	return fmt.Sprintf(
		"<conversation_history>\nYou are in a continuous conversation. Continue naturally.\n\n%s\n</conversation_history>\n\n%s",
		sessionContext, prompt)
}

// --- Helpers ---

func getEncryptionKey() string {
	if EncryptionKeyFn == nil {
		return ""
	}
	return EncryptionKeyFn()
}

// NewUUID generates a random UUID v4.
func NewUUID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
