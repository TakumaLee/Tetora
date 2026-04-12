package session

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func skipIfNoSQLite(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not found, skipping test")
	}
}

func TestInitSessionDB(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	if err := InitSessionDB(dbPath); err != nil {
		t.Fatalf("InitSessionDB: %v", err)
	}
	// Idempotent.
	if err := InitSessionDB(dbPath); err != nil {
		t.Fatalf("InitSessionDB (second call): %v", err)
	}
}

func TestCreateAndQuerySession(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	InitSessionDB(dbPath)

	now := time.Now().Format(time.RFC3339)
	s := Session{
		ID:        "sess-001",
		Agent:     "翡翠",
		Source:    "telegram",
		Status:    "active",
		Title:     "Research Go concurrency",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := CreateSession(dbPath, s); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	got, err := QuerySessionByID(dbPath, "sess-001")
	if err != nil {
		t.Fatalf("QuerySessionByID: %v", err)
	}
	if got == nil {
		t.Fatal("session not found")
	}
	if got.Agent != "翡翠" {
		t.Errorf("agent = %q, want %q", got.Agent, "翡翠")
	}
	if got.Status != "active" {
		t.Errorf("status = %q, want %q", got.Status, "active")
	}
	if got.Title != "Research Go concurrency" {
		t.Errorf("title = %q, want %q", got.Title, "Research Go concurrency")
	}
}

func TestCreateSessionIdempotent(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	InitSessionDB(dbPath)

	now := time.Now().Format(time.RFC3339)
	s := Session{
		ID: "sess-dup", Agent: "黒曜", Source: "http", Status: "active",
		Title: "Original title", CreatedAt: now, UpdatedAt: now,
	}
	CreateSession(dbPath, s)

	s.Title = "Different title"
	if err := CreateSession(dbPath, s); err != nil {
		t.Fatalf("CreateSession (duplicate): %v", err)
	}

	got, _ := QuerySessionByID(dbPath, "sess-dup")
	if got.Title != "Original title" {
		t.Errorf("title = %q, want %q (INSERT OR IGNORE should keep original)", got.Title, "Original title")
	}
}

func TestAddAndQuerySessionMessages(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	InitSessionDB(dbPath)

	now := time.Now().Format(time.RFC3339)
	CreateSession(dbPath, Session{
		ID: "sess-msg", Agent: "琥珀", Source: "cli", Status: "active",
		CreatedAt: now, UpdatedAt: now,
	})

	AddSessionMessage(dbPath, SessionMessage{
		SessionID: "sess-msg", Role: "user",
		Content: "Write a haiku about Go", TaskID: "task-001", CreatedAt: now,
	})
	AddSessionMessage(dbPath, SessionMessage{
		SessionID: "sess-msg", Role: "assistant",
		Content: "Goroutines dance\nChannels carry data swift\nConcurrency blooms",
		CostUSD: 0.05, TokensIn: 100, TokensOut: 50, Model: "claude-3",
		TaskID: "task-001", CreatedAt: now,
	})

	msgs, err := QuerySessionMessages(dbPath, "sess-msg")
	if err != nil {
		t.Fatalf("QuerySessionMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "user" {
		t.Errorf("first message role = %q, want %q", msgs[0].Role, "user")
	}
	if msgs[1].Role != "assistant" {
		t.Errorf("second message role = %q, want %q", msgs[1].Role, "assistant")
	}
	if msgs[1].CostUSD != 0.05 {
		t.Errorf("cost = %f, want 0.05", msgs[1].CostUSD)
	}
}

func TestUpdateSessionStats(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	InitSessionDB(dbPath)

	now := time.Now().Format(time.RFC3339)
	CreateSession(dbPath, Session{
		ID: "sess-stats", Agent: "翡翠", Source: "http", Status: "active",
		CreatedAt: now, UpdatedAt: now,
	})

	UpdateSessionStats(dbPath, "sess-stats", 0.10, 200, 100, 2)
	UpdateSessionStats(dbPath, "sess-stats", 0.05, 150, 80, 2)

	got, _ := QuerySessionByID(dbPath, "sess-stats")
	if got.TotalCost < 0.14 || got.TotalCost > 0.16 {
		t.Errorf("total cost = %f, want ~0.15", got.TotalCost)
	}
	if got.TotalTokensIn != 350 {
		t.Errorf("tokens in = %d, want 350", got.TotalTokensIn)
	}
	if got.TotalTokensOut != 180 {
		t.Errorf("tokens out = %d, want 180", got.TotalTokensOut)
	}
	if got.MessageCount != 4 {
		t.Errorf("message count = %d, want 4", got.MessageCount)
	}
}

func TestUpdateSessionStatus(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	InitSessionDB(dbPath)

	now := time.Now().Format(time.RFC3339)
	CreateSession(dbPath, Session{
		ID: "sess-status", Agent: "琉璃", Source: "http", Status: "active",
		CreatedAt: now, UpdatedAt: now,
	})

	UpdateSessionStatus(dbPath, "sess-status", "completed")

	got, _ := QuerySessionByID(dbPath, "sess-status")
	if got.Status != "completed" {
		t.Errorf("status = %q, want %q", got.Status, "completed")
	}
}

func TestQuerySessionsFiltered(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	InitSessionDB(dbPath)

	now := time.Now().Format(time.RFC3339)
	CreateSession(dbPath, Session{
		ID: "s1", Agent: "翡翠", Source: "http", Status: "active",
		Title: "Research task", CreatedAt: now, UpdatedAt: now,
	})
	CreateSession(dbPath, Session{
		ID: "s2", Agent: "黒曜", Source: "telegram", Status: "completed",
		Title: "Dev task", CreatedAt: now, UpdatedAt: now,
	})
	CreateSession(dbPath, Session{
		ID: "s3", Agent: "翡翠", Source: "cron", Status: "active",
		Title: "Auto research", CreatedAt: now, UpdatedAt: now,
	})

	sessions, total, err := QuerySessions(dbPath, SessionQuery{Agent: "翡翠"})
	if err != nil {
		t.Fatalf("QuerySessions: %v", err)
	}
	if total != 2 {
		t.Errorf("total for 翡翠 = %d, want 2", total)
	}
	if len(sessions) != 2 {
		t.Errorf("len sessions for 翡翠 = %d, want 2", len(sessions))
	}

	// initSessionDB creates a system log session (status=active), so expect +1.
	sessions2, total2, _ := QuerySessions(dbPath, SessionQuery{Status: "active"})
	if total2 != 3 {
		t.Errorf("total active = %d, want 3 (2 test + 1 system log)", total2)
	}
	if len(sessions2) != 3 {
		t.Errorf("len active = %d, want 3 (2 test + 1 system log)", len(sessions2))
	}

	sessions3, _, _ := QuerySessions(dbPath, SessionQuery{Limit: 1})
	if len(sessions3) != 1 {
		t.Errorf("limit 1: got %d sessions", len(sessions3))
	}
}

func TestQuerySessionDetail(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	InitSessionDB(dbPath)

	now := time.Now().Format(time.RFC3339)
	CreateSession(dbPath, Session{
		ID: "sess-detail", Agent: "琥珀", Source: "http", Status: "active",
		Title: "Creative session", CreatedAt: now, UpdatedAt: now,
	})
	AddSessionMessage(dbPath, SessionMessage{
		SessionID: "sess-detail", Role: "user", Content: "Hello", CreatedAt: now,
	})
	AddSessionMessage(dbPath, SessionMessage{
		SessionID: "sess-detail", Role: "assistant", Content: "Hi there!", CreatedAt: now,
	})

	detail, err := QuerySessionDetail(dbPath, "sess-detail")
	if err != nil {
		t.Fatalf("QuerySessionDetail: %v", err)
	}
	if detail == nil {
		t.Fatal("detail is nil")
	}
	if detail.Session.Agent != "琥珀" {
		t.Errorf("session role = %q, want %q", detail.Session.Agent, "琥珀")
	}
	if len(detail.Messages) != 2 {
		t.Errorf("messages count = %d, want 2", len(detail.Messages))
	}
}

func TestQuerySessionDetailNotFound(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	InitSessionDB(dbPath)

	detail, err := QuerySessionDetail(dbPath, "nonexistent")
	if err != nil {
		t.Fatalf("QuerySessionDetail: %v", err)
	}
	if detail != nil {
		t.Error("expected nil for nonexistent session")
	}
}

func TestCountActiveSessions(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	InitSessionDB(dbPath)

	now := time.Now().Format(time.RFC3339)
	CreateSession(dbPath, Session{
		ID: "a1", Agent: "翡翠", Status: "active", CreatedAt: now, UpdatedAt: now,
	})
	CreateSession(dbPath, Session{
		ID: "a2", Agent: "黒曜", Status: "completed", CreatedAt: now, UpdatedAt: now,
	})
	CreateSession(dbPath, Session{
		ID: "a3", Agent: "琥珀", Status: "active", CreatedAt: now, UpdatedAt: now,
	})

	count := CountActiveSessions(dbPath)
	if count != 3 {
		t.Errorf("active count = %d, want 3 (2 test + 1 system log)", count)
	}
}

func TestSessionSpecialChars(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	InitSessionDB(dbPath)

	now := time.Now().Format(time.RFC3339)
	CreateSession(dbPath, Session{
		ID: "sess-special", Agent: "琥珀", Source: "http", Status: "active",
		Title: `He said "it's fine" & <ok>`, CreatedAt: now, UpdatedAt: now,
	})

	AddSessionMessage(dbPath, SessionMessage{
		SessionID: "sess-special", Role: "user",
		Content: `Prompt with 'quotes' and "double quotes"`, CreatedAt: now,
	})

	got, _ := QuerySessionByID(dbPath, "sess-special")
	if got.Title != `He said "it's fine" & <ok>` {
		t.Errorf("title = %q", got.Title)
	}

	msgs, _ := QuerySessionMessages(dbPath, "sess-special")
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Content != `Prompt with 'quotes' and "double quotes"` {
		t.Errorf("content = %q", msgs[0].Content)
	}
}

func TestChannelSessionKey(t *testing.T) {
	tests := []struct {
		source string
		parts  []string
		want   string
	}{
		{"tg", []string{"翡翠"}, "tg:翡翠"},
		{"tg", []string{"ask"}, "tg:ask"},
		{"slack", []string{"#general", "1234567890.123456"}, "slack:#general:1234567890.123456"},
		{"slack", []string{"C01234"}, "slack:C01234"},
	}
	for _, tc := range tests {
		got := ChannelSessionKey(tc.source, tc.parts...)
		if got != tc.want {
			t.Errorf("ChannelSessionKey(%q, %v) = %q, want %q", tc.source, tc.parts, got, tc.want)
		}
	}
}

func TestFindChannelSession(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	InitSessionDB(dbPath)

	now := time.Now().Format(time.RFC3339)

	sess, err := FindChannelSession(dbPath, "tg:翡翠")
	if err != nil {
		t.Fatalf("FindChannelSession: %v", err)
	}
	if sess != nil {
		t.Error("expected nil for nonexistent channel session")
	}

	CreateSession(dbPath, Session{
		ID: "ch-001", Agent: "翡翠", Source: "telegram", Status: "active",
		ChannelKey: "tg:翡翠", Title: "Research", CreatedAt: now, UpdatedAt: now,
	})

	sess, err = FindChannelSession(dbPath, "tg:翡翠")
	if err != nil {
		t.Fatalf("FindChannelSession: %v", err)
	}
	if sess == nil {
		t.Fatal("expected session, got nil")
	}
	if sess.ID != "ch-001" {
		t.Errorf("session ID = %q, want %q", sess.ID, "ch-001")
	}
	if sess.ChannelKey != "tg:翡翠" {
		t.Errorf("channel_key = %q, want %q", sess.ChannelKey, "tg:翡翠")
	}

	sess2, _ := FindChannelSession(dbPath, "tg:黒曜")
	if sess2 != nil {
		t.Error("expected nil for different channel key")
	}

	UpdateSessionStatus(dbPath, "ch-001", "archived")
	sess3, _ := FindChannelSession(dbPath, "tg:翡翠")
	if sess3 != nil {
		t.Error("expected nil for archived channel session")
	}
}

func TestGetOrCreateChannelSession(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	InitSessionDB(dbPath)

	sess, err := GetOrCreateChannelSession(dbPath, "telegram", "tg:琥珀", "琥珀", "")
	if err != nil {
		t.Fatalf("GetOrCreateChannelSession: %v", err)
	}
	if sess == nil {
		t.Fatal("expected session, got nil")
	}
	firstID := sess.ID
	if sess.Agent != "琥珀" {
		t.Errorf("role = %q, want %q", sess.Agent, "琥珀")
	}

	sess2, err := GetOrCreateChannelSession(dbPath, "telegram", "tg:琥珀", "琥珀", "")
	if err != nil {
		t.Fatalf("GetOrCreateChannelSession (2nd): %v", err)
	}
	if sess2.ID != firstID {
		t.Errorf("expected same session ID %q, got %q", firstID, sess2.ID)
	}
}

func TestBuildSessionContext(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	InitSessionDB(dbPath)

	now := time.Now().Format(time.RFC3339)
	CreateSession(dbPath, Session{
		ID: "ctx-001", Agent: "翡翠", Source: "telegram", Status: "active",
		CreatedAt: now, UpdatedAt: now,
	})

	ctx := BuildSessionContext(dbPath, "ctx-001", 20)
	if ctx != "" {
		t.Errorf("expected empty context, got %q", ctx)
	}

	AddSessionMessage(dbPath, SessionMessage{
		SessionID: "ctx-001", Role: "user", Content: "How do goroutines work?", CreatedAt: now,
	})
	AddSessionMessage(dbPath, SessionMessage{
		SessionID: "ctx-001", Role: "assistant", Content: "Goroutines are lightweight threads.", CreatedAt: now,
	})
	AddSessionMessage(dbPath, SessionMessage{
		SessionID: "ctx-001", Role: "user", Content: "What about channels?", CreatedAt: now,
	})

	ctx = BuildSessionContext(dbPath, "ctx-001", 20)
	if ctx == "" {
		t.Fatal("expected non-empty context")
	}
	if !strings.Contains(ctx, "[user] How do goroutines work?") {
		t.Error("context missing user message")
	}
	if !strings.Contains(ctx, "[assistant] Goroutines are lightweight threads.") {
		t.Error("context missing assistant message")
	}

	ctx2 := BuildSessionContext(dbPath, "ctx-001", 2)
	if strings.Contains(ctx2, "goroutines work") {
		t.Error("limited context should not contain first message")
	}
	if !strings.Contains(ctx2, "[user] What about channels?") {
		t.Error("limited context should contain last user message")
	}
}

func TestWrapWithContext(t *testing.T) {
	got := WrapWithContext("", "Hello world")
	if got != "Hello world" {
		t.Errorf("expected unchanged prompt, got %q", got)
	}

	got2 := WrapWithContext("[user] Previous msg", "New message")
	if !strings.Contains(got2, "<conversation_history>") {
		t.Error("missing conversation_history opening tag")
	}
	if !strings.Contains(got2, "</conversation_history>") {
		t.Error("missing conversation_history closing tag")
	}
	if !strings.Contains(got2, "Previous msg") {
		t.Error("missing context content")
	}
	if !strings.Contains(got2, "New message") {
		t.Error("missing new prompt")
	}
}

func TestFindLastArchivedChannelSession(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	InitSessionDB(dbPath)

	now := time.Now().Format(time.RFC3339)

	// Nothing yet — should return nil.
	sess, err := FindLastArchivedChannelSession(dbPath, "discord:test")
	if err != nil {
		t.Fatalf("FindLastArchivedChannelSession: %v", err)
	}
	if sess != nil {
		t.Error("expected nil for nonexistent archived session")
	}

	// Active session — should NOT be returned.
	CreateSession(dbPath, Session{
		ID: "active-001", Agent: "龍蝦", Source: "discord", Status: "active",
		ChannelKey: "discord:test", CreatedAt: now, UpdatedAt: now,
	})
	sess, _ = FindLastArchivedChannelSession(dbPath, "discord:test")
	if sess != nil {
		t.Error("expected nil: active session should not be returned")
	}

	// Archive it — now it should be returned.
	UpdateSessionStatus(dbPath, "active-001", "archived")
	sess, err = FindLastArchivedChannelSession(dbPath, "discord:test")
	if err != nil {
		t.Fatalf("FindLastArchivedChannelSession: %v", err)
	}
	if sess == nil {
		t.Fatal("expected archived session, got nil")
	}
	if sess.ID != "active-001" {
		t.Errorf("session ID = %q, want %q", sess.ID, "active-001")
	}

	// Different channel key — should not cross-match.
	sess2, _ := FindLastArchivedChannelSession(dbPath, "discord:other")
	if sess2 != nil {
		t.Error("expected nil for different channel key")
	}
}

func TestArchiveChannelSession(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	InitSessionDB(dbPath)

	now := time.Now().Format(time.RFC3339)
	CreateSession(dbPath, Session{
		ID: "arch-001", Agent: "翡翠", Source: "telegram", Status: "active",
		ChannelKey: "tg:翡翠", CreatedAt: now, UpdatedAt: now,
	})

	if err := ArchiveChannelSession(dbPath, "tg:翡翠"); err != nil {
		t.Fatalf("ArchiveChannelSession: %v", err)
	}

	sess, _ := QuerySessionByID(dbPath, "arch-001")
	if sess.Status != "archived" {
		t.Errorf("status = %q, want %q", sess.Status, "archived")
	}

	if err := ArchiveChannelSession(dbPath, "tg:nonexistent"); err != nil {
		t.Fatalf("ArchiveChannelSession (nonexistent): %v", err)
	}
}

func TestQuerySessionDetailPrefixMatch(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	InitSessionDB(dbPath)

	now := time.Now().Format(time.RFC3339)
	s1 := Session{
		ID: "9c1bbafa-6cc8-4b1a-9f5e-000000000001", Agent: "翡翠", Source: "http", Status: "active",
		Title: "Research session", CreatedAt: now, UpdatedAt: now,
	}
	s2 := Session{
		ID: "9c1bbafa-6cc8-4b1a-9f5e-000000000002", Agent: "黒曜", Source: "cli", Status: "active",
		Title: "Dev session", CreatedAt: now, UpdatedAt: now,
	}
	s3 := Session{
		ID: "deadbeef-1234-5678-abcd-000000000003", Agent: "琥珀", Source: "http", Status: "active",
		Title: "Creative session", CreatedAt: now, UpdatedAt: now,
	}
	CreateSession(dbPath, s1)
	CreateSession(dbPath, s2)
	CreateSession(dbPath, s3)

	detail, err := QuerySessionDetail(dbPath, "deadbeef")
	if err != nil {
		t.Fatalf("QuerySessionDetail (unique prefix): %v", err)
	}
	if detail == nil {
		t.Fatal("expected detail, got nil")
	}
	if detail.Session.ID != s3.ID {
		t.Errorf("got session ID %q, want %q", detail.Session.ID, s3.ID)
	}

	_, err = QuerySessionDetail(dbPath, "9c1bbafa-6cc")
	if err == nil {
		t.Fatal("expected ErrAmbiguousSession, got nil error")
	}
	ambig, ok := err.(*ErrAmbiguousSession)
	if !ok {
		t.Fatalf("expected *ErrAmbiguousSession, got %T: %v", err, err)
	}
	if len(ambig.Matches) != 2 {
		t.Errorf("ambiguous matches = %d, want 2", len(ambig.Matches))
	}

	detail2, err2 := QuerySessionDetail(dbPath, "ffffffff")
	if err2 != nil {
		t.Fatalf("QuerySessionDetail (no match): %v", err2)
	}
	if detail2 != nil {
		t.Error("expected nil for no-match prefix")
	}

	detail3, err3 := QuerySessionDetail(dbPath, s1.ID)
	if err3 != nil {
		t.Fatalf("QuerySessionDetail (exact): %v", err3)
	}
	if detail3 == nil {
		t.Fatal("expected detail for exact ID, got nil")
	}
	if detail3.Session.Agent != "翡翠" {
		t.Errorf("role = %q, want %q", detail3.Session.Agent, "翡翠")
	}
}

func TestQuerySessionsByPrefix(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	InitSessionDB(dbPath)

	now := time.Now().Format(time.RFC3339)
	CreateSession(dbPath, Session{
		ID: "aaaa-0001", Agent: "翡翠", Source: "http", Status: "active",
		Title: "First", CreatedAt: now, UpdatedAt: now,
	})
	CreateSession(dbPath, Session{
		ID: "aaaa-0002", Agent: "黒曜", Source: "cli", Status: "active",
		Title: "Second", CreatedAt: now, UpdatedAt: now,
	})
	CreateSession(dbPath, Session{
		ID: "bbbb-0001", Agent: "琥珀", Source: "http", Status: "active",
		Title: "Third", CreatedAt: now, UpdatedAt: now,
	})

	matches, err := QuerySessionsByPrefix(dbPath, "aaaa")
	if err != nil {
		t.Fatalf("QuerySessionsByPrefix: %v", err)
	}
	if len(matches) != 2 {
		t.Errorf("expected 2 matches for prefix 'aaaa', got %d", len(matches))
	}

	matches2, err := QuerySessionsByPrefix(dbPath, "bbbb")
	if err != nil {
		t.Fatalf("QuerySessionsByPrefix: %v", err)
	}
	if len(matches2) != 1 {
		t.Errorf("expected 1 match for prefix 'bbbb', got %d", len(matches2))
	}
	if matches2[0].ID != "bbbb-0001" {
		t.Errorf("got ID %q, want %q", matches2[0].ID, "bbbb-0001")
	}

	matches3, err := QuerySessionsByPrefix(dbPath, "cccc")
	if err != nil {
		t.Fatalf("QuerySessionsByPrefix: %v", err)
	}
	if len(matches3) != 0 {
		t.Errorf("expected 0 matches for prefix 'cccc', got %d", len(matches3))
	}
}

func TestQuerySessionByIDCtxCancellation(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	if err := InitSessionDB(dbPath); err != nil {
		t.Fatalf("InitSessionDB: %v", err)
	}

	now := time.Now().Format(time.RFC3339)
	CreateSession(dbPath, Session{
		ID: "ctx-cancel-001", Agent: "黒曜", Source: "test", Status: "active",
		Title: "Ctx cancellation test", CreatedAt: now, UpdatedAt: now,
	})

	// Cancel the context before calling QuerySessionByIDCtx.
	// exec.CommandContext sends SIGKILL on cancel, so the sqlite3 subprocess
	// should terminate immediately rather than blocking.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	_, err := QuerySessionByIDCtx(ctx, dbPath, "ctx-cancel-001")
	elapsed := time.Since(start)

	// Must return within 2s budget; a leaked process would block much longer.
	if elapsed > 2*time.Second {
		t.Fatalf("blocked for %v with cancelled ctx (budget: 2s)", elapsed)
	}
	// exec.CommandContext wraps the kill in *exec.ExitError, so context.Canceled
	// is not preserved in the error chain. Just assert err != nil.
	if err == nil {
		t.Error("expected error with cancelled context, got nil")
	}
}
