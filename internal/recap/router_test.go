package recap

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"tetora/internal/config"
	"tetora/internal/db"
)

var errDiscordDown = errors.New("discord api unavailable")

// fakeAPI records all API interactions for assertions and controls the
// thread-id returned by Request.
type fakeAPI struct {
	nextThreadID string
	requests     []fakeRequest
	sends        []fakeSend
	forceErr     error
	forceSendErr error
}

type fakeRequest struct {
	Method  string
	Path    string
	Payload any
}

type fakeSend struct {
	ChannelID string
	Content   string
}

func (f *fakeAPI) Request(method, path string, payload any) ([]byte, error) {
	f.requests = append(f.requests, fakeRequest{method, path, payload})
	if f.forceErr != nil {
		return nil, f.forceErr
	}
	return []byte(`{"id":"` + f.nextThreadID + `"}`), nil
}

func (f *fakeAPI) SendLongMessage(channelID, content string) error {
	f.sends = append(f.sends, fakeSend{channelID, content})
	return f.forceSendErr
}

// newTestDB creates a temp sqlite file with the recap schema applied.
func newTestDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	if _, err := os.Create(dbPath); err != nil {
		t.Fatalf("touch db: %v", err)
	}
	if err := InitSchema(dbPath); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	return dbPath
}

func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

func TestRouter_Deliver_NewSession_CreatesThreadAndSends(t *testing.T) {
	dbPath := newTestDB(t)
	api := &fakeAPI{nextThreadID: "thread-abc"}
	r := &Router{
		Cfg: config.DiscordRecapConfig{
			DefaultParentChannel: "default-ch",
			ProjectChannels: map[string]string{
				"/repo/tetora": "ruri-ch",
			},
		},
		API:    api,
		DBPath: dbPath,
		Now:    fixedClock(time.Date(2026, 4, 17, 14, 30, 0, 0, time.UTC)),
	}
	rec := Record{
		UUID:      "u-1",
		SessionID: "session-12345678-abcd",
		CWD:       "/repo/tetora",
		GitBranch: "feat/x",
		Content:   "hello from recap",
	}
	if err := r.Deliver(rec); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if len(api.requests) != 1 {
		t.Fatalf("expected 1 thread-create request, got %d", len(api.requests))
	}
	if api.requests[0].Path != "/channels/ruri-ch/threads" {
		t.Errorf("wrong thread-create path: %s", api.requests[0].Path)
	}
	if len(api.sends) != 1 {
		t.Fatalf("expected 1 SendMessage, got %d", len(api.sends))
	}
	if api.sends[0].ChannelID != "thread-abc" {
		t.Errorf("message not sent to thread, got channel=%s", api.sends[0].ChannelID)
	}
	if api.sends[0].Content != "hello from recap" {
		t.Errorf("wrong content: %q", api.sends[0].Content)
	}
	if !IsSent(dbPath, "u-1") {
		t.Errorf("expected uuid marked sent")
	}
	got, err := GetRouting(dbPath, rec.SessionID)
	if err != nil || got == nil {
		t.Fatalf("expected routing stored: err=%v got=%v", err, got)
	}
	if got.ThreadID != "thread-abc" || got.ParentChannelID != "ruri-ch" {
		t.Errorf("routing mismatch: %+v", got)
	}
}

func TestRouter_Deliver_ReusesExistingThread(t *testing.T) {
	dbPath := newTestDB(t)
	// Pre-existing routing.
	if err := SetRouting(dbPath, Routing{
		SessionID:       "session-X",
		ParentChannelID: "ruri-ch",
		ThreadID:        "existing-thread",
		CWD:             "/repo/tetora",
	}, "2026-04-17T00:00:00Z"); err != nil {
		t.Fatalf("seed routing: %v", err)
	}

	api := &fakeAPI{nextThreadID: "should-not-be-used"}
	r := &Router{
		Cfg:    config.DiscordRecapConfig{DefaultParentChannel: "default-ch"},
		API:    api,
		DBPath: dbPath,
	}
	rec := Record{UUID: "u-2", SessionID: "session-X", CWD: "/repo/tetora", Content: "second recap"}
	if err := r.Deliver(rec); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if len(api.requests) != 0 {
		t.Errorf("should NOT create a new thread, got %d requests", len(api.requests))
	}
	if len(api.sends) != 1 || api.sends[0].ChannelID != "existing-thread" {
		t.Errorf("expected send to existing thread, got sends=%+v", api.sends)
	}
}

func TestRouter_Deliver_DedupsByUUID(t *testing.T) {
	dbPath := newTestDB(t)
	api := &fakeAPI{nextThreadID: "thr"}
	r := &Router{
		Cfg:    config.DiscordRecapConfig{DefaultParentChannel: "default-ch"},
		API:    api,
		DBPath: dbPath,
	}
	rec := Record{UUID: "u-dup", SessionID: "s", CWD: "/x", Content: "once"}

	if err := r.Deliver(rec); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := r.Deliver(rec); err != nil {
		t.Fatalf("second: %v", err)
	}
	if len(api.sends) != 1 {
		t.Errorf("expected exactly 1 send (dedup), got %d", len(api.sends))
	}
}

func TestRouter_Deliver_SendFails_DoesNotMarkSent(t *testing.T) {
	dbPath := newTestDB(t)
	// Pre-existing routing so thread creation isn't attempted.
	if err := SetRouting(dbPath, Routing{
		SessionID: "s-fail", ParentChannelID: "ruri-ch",
		ThreadID: "thread-x", CWD: "/x",
	}, "2026-04-17T00:00:00Z"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	api := &fakeAPI{forceSendErr: errDiscordDown}
	r := &Router{
		Cfg:    config.DiscordRecapConfig{DefaultParentChannel: "default-ch"},
		API:    api,
		DBPath: dbPath,
	}
	rec := Record{UUID: "u-fail", SessionID: "s-fail", CWD: "/x", Content: "will fail"}

	if err := r.Deliver(rec); err == nil {
		t.Fatalf("expected Deliver to return error when send fails")
	}
	if IsSent(dbPath, "u-fail") {
		t.Errorf("uuid must NOT be marked sent after send failure (would prevent retry)")
	}

	// Second Deliver on same uuid should attempt send again.
	api.forceSendErr = nil
	if err := r.Deliver(rec); err != nil {
		t.Fatalf("retry deliver: %v", err)
	}
	if !IsSent(dbPath, "u-fail") {
		t.Errorf("after successful retry, uuid should be marked sent")
	}
	if len(api.sends) != 2 {
		t.Errorf("expected 2 send attempts (failed + retried), got %d", len(api.sends))
	}
}

func TestRouter_Deliver_NoParentChannel_Skips(t *testing.T) {
	dbPath := newTestDB(t)
	api := &fakeAPI{}
	r := &Router{
		// No DefaultParentChannel, no ProjectChannels match.
		Cfg:    config.DiscordRecapConfig{},
		API:    api,
		DBPath: dbPath,
	}
	rec := Record{UUID: "u-nop", SessionID: "s", CWD: "/nowhere", Content: "?"}
	if err := r.Deliver(rec); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if len(api.requests) != 0 || len(api.sends) != 0 {
		t.Errorf("expected no discord activity, got requests=%d sends=%d",
			len(api.requests), len(api.sends))
	}
	if IsSent(dbPath, "u-nop") {
		t.Errorf("skipped recap should NOT be marked sent")
	}
}

func TestRouter_ThreadName_RespectsLimit(t *testing.T) {
	r := &Router{Now: fixedClock(time.Date(2026, 4, 17, 14, 30, 0, 0, time.UTC))}
	long := strings.Repeat("a", 200)
	name := r.threadName(Record{
		SessionID: "abcdefghij",
		CWD:       "/x/" + long,
		GitBranch: long,
	})
	if utf8.RuneCountInString(name) > 100 {
		t.Errorf("thread name exceeds Discord 100-rune limit: runes=%d",
			utf8.RuneCountInString(name))
	}
}

func TestRouter_ThreadName_CJKStaysRuneSafe(t *testing.T) {
	// A CJK-heavy name that, before the rune-safe fix, would get byte-sliced
	// through a 3-byte UTF-8 sequence and produce invalid UTF-8.
	r := &Router{Now: fixedClock(time.Date(2026, 4, 17, 14, 30, 0, 0, time.UTC))}
	longCJK := strings.Repeat("議", 150) // 450 bytes, 150 runes
	name := r.threadName(Record{
		SessionID: "cjksessiondb49ea04",
		CWD:       "/repo/" + longCJK,
		GitBranch: "feat/測試",
	})
	if !utf8.ValidString(name) {
		t.Errorf("thread name produced invalid UTF-8 after truncation")
	}
	if utf8.RuneCountInString(name) > 100 {
		t.Errorf("CJK thread name exceeds 100 runes: %d", utf8.RuneCountInString(name))
	}
}

// Sanity check: db helpers are used so import isn't accidentally dropped.
var _ = db.Escape
