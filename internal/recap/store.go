package recap

import (
	"fmt"

	"tetora/internal/db"
)

const schemaSQL = `
CREATE TABLE IF NOT EXISTS recap_sent (
  uuid        TEXT PRIMARY KEY,
  session_id  TEXT NOT NULL,
  thread_id   TEXT NOT NULL,
  sent_at     TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_recap_sent_session ON recap_sent(session_id);

CREATE TABLE IF NOT EXISTS recap_session_routing (
  session_id        TEXT PRIMARY KEY,
  parent_channel_id TEXT NOT NULL,
  thread_id         TEXT NOT NULL,
  cwd               TEXT NOT NULL,
  created_at        TEXT NOT NULL,
  last_recap_at     TEXT NOT NULL
);
`

// InitSchema creates the recap tables if they do not exist.
func InitSchema(dbPath string) error {
	if dbPath == "" {
		return nil
	}
	return db.Exec(dbPath, schemaSQL)
}

// IsSent reports whether a recap with this uuid has already been delivered.
func IsSent(dbPath, uuid string) bool {
	rows, err := db.Query(dbPath,
		fmt.Sprintf(`SELECT 1 FROM recap_sent WHERE uuid = '%s' LIMIT 1`, db.Escape(uuid)))
	if err != nil {
		return false
	}
	return len(rows) > 0
}

// MarkSent records that a recap uuid has been delivered to a thread.
func MarkSent(dbPath, uuid, sessionID, threadID, sentAt string) error {
	return db.Exec(dbPath, fmt.Sprintf(
		`INSERT OR REPLACE INTO recap_sent (uuid, session_id, thread_id, sent_at) VALUES ('%s', '%s', '%s', '%s')`,
		db.Escape(uuid), db.Escape(sessionID), db.Escape(threadID), db.Escape(sentAt)))
}

// Routing is the stored Discord destination for a Claude Code session.
type Routing struct {
	SessionID       string
	ParentChannelID string
	ThreadID        string
	CWD             string
}

// GetRouting returns the routing for a session if one exists.
func GetRouting(dbPath, sessionID string) (*Routing, error) {
	rows, err := db.Query(dbPath, fmt.Sprintf(
		`SELECT session_id, parent_channel_id, thread_id, cwd FROM recap_session_routing WHERE session_id = '%s' LIMIT 1`,
		db.Escape(sessionID)))
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	r := rows[0]
	return &Routing{
		SessionID:       db.Str(r["session_id"]),
		ParentChannelID: db.Str(r["parent_channel_id"]),
		ThreadID:        db.Str(r["thread_id"]),
		CWD:             db.Str(r["cwd"]),
	}, nil
}

// SetRouting records the Discord destination for a session. Subsequent calls
// update last_recap_at.
func SetRouting(dbPath string, r Routing, nowISO string) error {
	return db.Exec(dbPath, fmt.Sprintf(
		`INSERT INTO recap_session_routing
		   (session_id, parent_channel_id, thread_id, cwd, created_at, last_recap_at)
		 VALUES ('%s', '%s', '%s', '%s', '%s', '%s')
		 ON CONFLICT(session_id) DO UPDATE SET
		   parent_channel_id = excluded.parent_channel_id,
		   thread_id         = excluded.thread_id,
		   cwd               = excluded.cwd,
		   last_recap_at     = excluded.last_recap_at`,
		db.Escape(r.SessionID), db.Escape(r.ParentChannelID), db.Escape(r.ThreadID),
		db.Escape(r.CWD), db.Escape(nowISO), db.Escape(nowISO)))
}

// TouchRouting updates last_recap_at for an existing session routing.
func TouchRouting(dbPath, sessionID, nowISO string) error {
	return db.Exec(dbPath, fmt.Sprintf(
		`UPDATE recap_session_routing SET last_recap_at = '%s' WHERE session_id = '%s'`,
		db.Escape(nowISO), db.Escape(sessionID)))
}
