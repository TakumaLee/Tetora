package recap

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"tetora/internal/config"
	"tetora/internal/discord"
	"tetora/internal/log"
)

// DiscordAPI is the subset of the discord client we call. Declared here so
// router.go can be unit-tested with a fake.
type DiscordAPI interface {
	Request(method, path string, payload any) ([]byte, error)
	SendLongMessage(channelID, content string) error
}

// Router decides which Discord thread a recap belongs to and delivers it.
type Router struct {
	Cfg    config.DiscordRecapConfig
	API    DiscordAPI
	DBPath string
	Now    func() time.Time // injectable clock for tests
}

// Deliver processes one recap record end-to-end:
//  1. skip if already sent (dedup by uuid)
//  2. find or create the session→thread routing
//  3. post the recap content into the thread
//  4. mark as sent
func (r *Router) Deliver(rec Record) error {
	if rec.UUID == "" || rec.Content == "" {
		return nil
	}
	if IsSent(r.DBPath, rec.UUID) {
		return nil
	}

	routing, err := r.resolveRouting(rec)
	if err != nil {
		return err
	}
	if routing == nil {
		log.Debug("recap: no parent channel for cwd, skipping",
			"sessionId", rec.SessionID, "cwd", rec.CWD, "uuid", rec.UUID)
		return nil
	}

	if err := r.API.SendLongMessage(routing.ThreadID, rec.Content); err != nil {
		// Don't MarkSent on failure — next poll will retry this uuid.
		log.Warn("recap: send to discord failed, will retry",
			"error", err, "uuid", rec.UUID, "thread", routing.ThreadID)
		return err
	}

	now := r.nowISO()
	if err := MarkSent(r.DBPath, rec.UUID, rec.SessionID, routing.ThreadID, now); err != nil {
		log.Warn("recap: mark sent failed", "error", err, "uuid", rec.UUID)
	}
	if err := TouchRouting(r.DBPath, rec.SessionID, now); err != nil {
		log.Warn("recap: touch routing failed", "error", err, "sessionId", rec.SessionID)
	}
	log.Info("recap forwarded to discord",
		"sessionId", shortID(rec.SessionID),
		"thread", routing.ThreadID,
		"uuid", shortID(rec.UUID),
		"bytes", len(rec.Content))
	return nil
}

// resolveRouting returns the existing routing for the session, or creates a
// new thread and persists the mapping. Returns nil,nil when no parent channel
// can be determined (caller treats as skip).
func (r *Router) resolveRouting(rec Record) (*Routing, error) {
	existing, err := GetRouting(r.DBPath, rec.SessionID)
	if err != nil {
		return nil, err
	}
	if existing != nil && existing.ThreadID != "" {
		return existing, nil
	}

	parent := r.pickParentChannel(rec.CWD)
	if parent == "" {
		return nil, nil
	}

	threadID, err := r.createThread(parent, r.threadName(rec))
	if err != nil {
		return nil, fmt.Errorf("create thread: %w", err)
	}

	routing := Routing{
		SessionID:       rec.SessionID,
		ParentChannelID: parent,
		ThreadID:        threadID,
		CWD:             rec.CWD,
	}
	if err := SetRouting(r.DBPath, routing, r.nowISO()); err != nil {
		log.Warn("recap: set routing failed", "error", err, "sessionId", rec.SessionID)
	}
	log.Info("recap: created discord thread",
		"sessionId", shortID(rec.SessionID),
		"parent", parent,
		"thread", threadID,
		"cwd", rec.CWD)
	return &routing, nil
}

func (r *Router) pickParentChannel(cwd string) string {
	if cwd != "" {
		if id, ok := r.Cfg.ProjectChannels[cwd]; ok && id != "" {
			return id
		}
	}
	return r.Cfg.DefaultParentChannel
}

// createThread opens a new public thread in parentChannelID without a starter
// message (Discord "Start Thread without Message" endpoint).
func (r *Router) createThread(parentChannelID, name string) (string, error) {
	archive := r.Cfg.ThreadAutoArchiveMin
	if archive <= 0 {
		archive = 10080 // 7 days
	}
	body, err := r.API.Request("POST",
		fmt.Sprintf("/channels/%s/threads", parentChannelID),
		map[string]any{
			"name":                  name,
			"type":                  11, // public thread
			"auto_archive_duration": archive,
		})
	if err != nil {
		return "", err
	}
	var ch struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &ch); err != nil {
		return "", fmt.Errorf("parse thread response: %w", err)
	}
	if ch.ID == "" {
		return "", fmt.Errorf("empty thread id in response")
	}
	return ch.ID, nil
}

// threadName renders a human-readable thread title under Discord's 100-char cap.
// Example: "[tetora/feat-xx] db49ea04 · 04-17 14:30".
func (r *Router) threadName(rec Record) string {
	repo := filepath.Base(rec.CWD)
	if repo == "." || repo == "/" {
		repo = "session"
	}
	branch := strings.TrimPrefix(rec.GitBranch, "refs/heads/")
	branch = strings.ReplaceAll(branch, "/", "-")
	head := repo
	if branch != "" {
		head = repo + "/" + branch
	}
	short := shortID(rec.SessionID)
	stamp := r.nowTime().Format("01-02 15:04")
	name := fmt.Sprintf("[%s] %s · %s", head, short, stamp)
	// Discord's thread-name limit is 100 characters (runes), not bytes.
	// Truncate on rune boundary so CJK characters never split mid-sequence.
	if runes := []rune(name); len(runes) > 100 {
		name = string(runes[:100])
	}
	return name
}

func (r *Router) nowTime() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Router) nowISO() string {
	return r.nowTime().UTC().Format(time.RFC3339)
}

func shortID(s string) string {
	if len(s) >= 8 {
		return s[:8]
	}
	return s
}

// Ensure the concrete *discord.Client satisfies DiscordAPI at compile time.
var _ DiscordAPI = (*discord.Client)(nil)
