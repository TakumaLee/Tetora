package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tetora/internal/db"
)

// --- from compaction_test.go ---

// --- CompactionConfig Defaults Tests ---

func TestCompactionConfig_Defaults(t *testing.T) {
	tests := []struct {
		name   string
		config CompactionConfig
		want   map[string]interface{}
	}{
		{
			name:   "all defaults",
			config: CompactionConfig{},
			want: map[string]interface{}{
				"maxMessages": 50,
				"compactTo":   10,
				"model":       "haiku",
				"maxCost":     0.02,
			},
		},
		{
			name: "custom values",
			config: CompactionConfig{
				MaxMessages: 100,
				CompactTo:   20,
				Model:       "opus",
				MaxCost:     0.05,
			},
			want: map[string]interface{}{
				"maxMessages": 100,
				"compactTo":   20,
				"model":       "opus",
				"maxCost":     0.05,
			},
		},
		{
			name: "partial custom",
			config: CompactionConfig{
				MaxMessages: 75,
				Model:       "sonnet",
			},
			want: map[string]interface{}{
				"maxMessages": 75,
				"compactTo":   10,
				"model":       "sonnet",
				"maxCost":     0.02,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := compactionMaxMessages(tt.config); got != tt.want["maxMessages"] {
				t.Errorf("compactionMaxMessages() = %v, want %v", got, tt.want["maxMessages"])
			}
			if got := compactionCompactTo(tt.config); got != tt.want["compactTo"] {
				t.Errorf("compactionCompactTo() = %v, want %v", got, tt.want["compactTo"])
			}
			if got := compactionModel(tt.config); got != tt.want["model"] {
				t.Errorf("compactionModel() = %v, want %v", got, tt.want["model"])
			}
			if got := compactionMaxCost(tt.config); got != tt.want["maxCost"] {
				t.Errorf("compactionMaxCost() = %v, want %v", got, tt.want["maxCost"])
			}
		})
	}
}

// --- buildCompactionPrompt Tests ---

func TestBuildCompactionPrompt(t *testing.T) {
	tests := []struct {
		name     string
		messages []sessionMessage
		contains []string
	}{
		{
			name: "single message",
			messages: []sessionMessage{
				{ID: 1, Agent: "user", Content: "Hello", Timestamp: "2026-01-01 10:00:00"},
			},
			contains: []string{
				"Summarize this conversation",
				"[2026-01-01 10:00:00] user: Hello",
				"Key decisions",
			},
		},
		{
			name: "multiple messages",
			messages: []sessionMessage{
				{ID: 1, Agent: "user", Content: "What's the weather?", Timestamp: "2026-01-01 10:00:00"},
				{ID: 2, Agent: "assistant", Content: "It's sunny.", Timestamp: "2026-01-01 10:01:00"},
				{ID: 3, Agent: "user", Content: "Great!", Timestamp: "2026-01-01 10:02:00"},
			},
			contains: []string{
				"user: What's the weather?",
				"assistant: It's sunny.",
				"user: Great!",
			},
		},
		{
			name: "missing timestamp",
			messages: []sessionMessage{
				{ID: 1, Agent: "system", Content: "Init", Timestamp: ""},
			},
			contains: []string{
				"[unknown] system: Init",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prompt := buildCompactionPrompt(tt.messages)
			for _, expected := range tt.contains {
				if !strContains(prompt, expected) {
					t.Errorf("prompt missing expected substring: %q", expected)
				}
			}
		})
	}
}

// --- Database Integration Tests ---

func TestCountSessionMessages(t *testing.T) {
	// Create temp test DB.
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Init DB.
	if err := initSessionDB(dbPath); err != nil {
		t.Fatalf("initSessionDB failed: %v", err)
	}

	// Insert test session.
	sessionID := "test-session-1"
	sql := fmt.Sprintf("INSERT INTO sessions (id, agent, source, status, title, created_at, updated_at) VALUES ('%s', 'test', 'test', 'active', 'Test', datetime('now'), datetime('now'))", sessionID)
	db.Query(dbPath, sql)

	// Insert messages.
	for i := 1; i <= 5; i++ {
		sql := fmt.Sprintf("INSERT INTO session_messages (session_id, role, content, created_at) VALUES ('%s', 'user', 'Message %d', datetime('now'))",
			sessionID, i)
		db.Query(dbPath, sql)
	}

	cfg := &Config{HistoryDB: dbPath}

	count := countSessionMessages(cfg, sessionID)
	if count != 5 {
		t.Errorf("countSessionMessages() = %d, want 5", count)
	}

	// Non-existent session.
	count = countSessionMessages(cfg, "nonexistent")
	if count != 0 {
		t.Errorf("countSessionMessages(nonexistent) = %d, want 0", count)
	}
}

func TestGetOldestMessages(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	if err := initSessionDB(dbPath); err != nil {
		t.Fatalf("initSessionDB failed: %v", err)
	}

	sessionID := "test-session-2"
	sql := fmt.Sprintf("INSERT INTO sessions (id, agent, source, status, title, created_at, updated_at) VALUES ('%s', 'test', 'test', 'active', 'Test', datetime('now'), datetime('now'))", sessionID)
	db.Query(dbPath, sql)

	// Insert 10 messages.
	for i := 1; i <= 10; i++ {
		sql := fmt.Sprintf("INSERT INTO session_messages (session_id, role, content, created_at) VALUES ('%s', 'user', 'Message %d', datetime('now', '+%d seconds'))",
			sessionID, i, i)
		db.Query(dbPath, sql)
	}

	cfg := &Config{HistoryDB: dbPath}

	// Get oldest 3 messages.
	messages := getOldestMessages(cfg, sessionID, 3)
	if len(messages) != 3 {
		t.Errorf("getOldestMessages() returned %d messages, want 3", len(messages))
	}

	// Check content order.
	for i, msg := range messages {
		expected := fmt.Sprintf("Message %d", i+1)
		if msg.Content != expected {
			t.Errorf("message[%d].Content = %q, want %q", i, msg.Content, expected)
		}
	}

	// Get all messages.
	messages = getOldestMessages(cfg, sessionID, 20)
	if len(messages) != 10 {
		t.Errorf("getOldestMessages(limit=20) returned %d messages, want 10", len(messages))
	}
}

func TestReplaceWithSummary(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	if err := initSessionDB(dbPath); err != nil {
		t.Fatalf("initSessionDB failed: %v", err)
	}

	sessionID := "test-session-3"
	sql := fmt.Sprintf("INSERT INTO sessions (id, agent, source, status, title, created_at, updated_at) VALUES ('%s', 'test', 'test', 'active', 'Test', datetime('now'), datetime('now'))", sessionID)
	db.Query(dbPath, sql)

	// Insert 5 messages.
	for i := 1; i <= 5; i++ {
		sql := fmt.Sprintf("INSERT INTO session_messages (session_id, role, content, created_at) VALUES ('%s', 'user', 'Message %d', datetime('now'))",
			sessionID, i)
		db.Query(dbPath, sql)
	}

	cfg := &Config{HistoryDB: dbPath}

	// Get oldest 3 to replace.
	messages := getOldestMessages(cfg, sessionID, 3)
	if len(messages) != 3 {
		t.Fatalf("setup: expected 3 messages, got %d", len(messages))
	}

	summary := "This is a summary of the first 3 messages."

	// Replace with summary.
	if err := replaceWithSummary(cfg, sessionID, messages, summary); err != nil {
		t.Fatalf("replaceWithSummary failed: %v", err)
	}

	// Count remaining messages.
	count := countSessionMessages(cfg, sessionID)
	// Should be: 5 original - 3 deleted + 1 summary = 3
	if count != 3 {
		t.Errorf("after replacement, count = %d, want 3", count)
	}

	// Check that summary exists.
	sql = fmt.Sprintf("SELECT content FROM session_messages WHERE session_id = '%s' AND role = 'system' ORDER BY id ASC LIMIT 1",
		db.Escape(sessionID))
	rows, err := db.Query(dbPath, sql)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("summary message not found")
	}

	content := rows[0]["content"].(string)
	if !strContains(content, "[COMPACTED]") || !strContains(content, summary) {
		t.Errorf("summary content = %q, want to contain '[COMPACTED]' and summary", content)
	}
}

func TestSessionExists(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	if err := initSessionDB(dbPath); err != nil {
		t.Fatalf("initSessionDB failed: %v", err)
	}

	cfg := &Config{HistoryDB: dbPath}

	// Non-existent session.
	if sessionExists(cfg, "nonexistent") {
		t.Error("sessionExists(nonexistent) = true, want false")
	}

	// Create session.
	sessionID := "test-session-exists"
	sql := fmt.Sprintf("INSERT INTO sessions (id, agent, source, status, title, created_at, updated_at) VALUES ('%s', 'test', 'test', 'active', 'Test', datetime('now'), datetime('now'))", sessionID)
	db.Query(dbPath, sql)

	// Should exist now.
	if !sessionExists(cfg, sessionID) {
		t.Error("sessionExists(test-session-exists) = false, want true")
	}
}

// --- Helper Functions ---

// strContains checks if a string contains a substring (case-sensitive).
func strContains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || strIndexOf(s, substr) >= 0)
}

// strIndexOf returns the index of substr in s, or -1 if not found.
func strIndexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// --- from session_test.go ---

func TestInitSessionDB(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	if err := initSessionDB(dbPath); err != nil {
		t.Fatalf("initSessionDB: %v", err)
	}
	// Idempotent.
	if err := initSessionDB(dbPath); err != nil {
		t.Fatalf("initSessionDB (second call): %v", err)
	}
}

func TestCreateAndQuerySession(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	initSessionDB(dbPath)

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
	if err := createSession(dbPath, s); err != nil {
		t.Fatalf("createSession: %v", err)
	}

	got, err := querySessionByID(dbPath, "sess-001")
	if err != nil {
		t.Fatalf("querySessionByID: %v", err)
	}
	if got == nil {
		t.Fatal("session not found")
	}
	if got.Agent != "翡翠" {
		t.Errorf("role = %q, want %q", got.Agent, "翡翠")
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
	initSessionDB(dbPath)

	now := time.Now().Format(time.RFC3339)
	s := Session{
		ID: "sess-dup", Agent: "黒曜", Source: "http", Status: "active",
		Title: "Original title", CreatedAt: now, UpdatedAt: now,
	}
	createSession(dbPath, s)

	// Second call with same ID should not error (INSERT OR IGNORE).
	s.Title = "Different title"
	if err := createSession(dbPath, s); err != nil {
		t.Fatalf("createSession (duplicate): %v", err)
	}

	// Title should remain the original.
	got, _ := querySessionByID(dbPath, "sess-dup")
	if got.Title != "Original title" {
		t.Errorf("title = %q, want %q (INSERT OR IGNORE should keep original)", got.Title, "Original title")
	}
}

func TestAddAndQuerySessionMessages(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	initSessionDB(dbPath)

	now := time.Now().Format(time.RFC3339)
	createSession(dbPath, Session{
		ID: "sess-msg", Agent: "琥珀", Source: "cli", Status: "active",
		CreatedAt: now, UpdatedAt: now,
	})

	// Add user message.
	addSessionMessage(dbPath, SessionMessage{
		SessionID: "sess-msg", Role: "user",
		Content: "Write a haiku about Go", TaskID: "task-001", CreatedAt: now,
	})

	// Add assistant message.
	addSessionMessage(dbPath, SessionMessage{
		SessionID: "sess-msg", Role: "assistant",
		Content: "Goroutines dance\nChannels carry data swift\nConcurrency blooms",
		CostUSD: 0.05, TokensIn: 100, TokensOut: 50, Model: "claude-3",
		TaskID: "task-001", CreatedAt: now,
	})

	msgs, err := querySessionMessages(dbPath, "sess-msg")
	if err != nil {
		t.Fatalf("querySessionMessages: %v", err)
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
	initSessionDB(dbPath)

	now := time.Now().Format(time.RFC3339)
	createSession(dbPath, Session{
		ID: "sess-stats", Agent: "翡翠", Source: "http", Status: "active",
		CreatedAt: now, UpdatedAt: now,
	})

	// Update stats incrementally.
	updateSessionStats(dbPath, "sess-stats", 0.10, 200, 100, 2)
	updateSessionStats(dbPath, "sess-stats", 0.05, 150, 80, 2)

	got, _ := querySessionByID(dbPath, "sess-stats")
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
	initSessionDB(dbPath)

	now := time.Now().Format(time.RFC3339)
	createSession(dbPath, Session{
		ID: "sess-status", Agent: "琉璃", Source: "http", Status: "active",
		CreatedAt: now, UpdatedAt: now,
	})

	updateSessionStatus(dbPath, "sess-status", "completed")

	got, _ := querySessionByID(dbPath, "sess-status")
	if got.Status != "completed" {
		t.Errorf("status = %q, want %q", got.Status, "completed")
	}
}

func TestQuerySessionsFiltered(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	initSessionDB(dbPath)

	now := time.Now().Format(time.RFC3339)
	createSession(dbPath, Session{
		ID: "s1", Agent: "翡翠", Source: "http", Status: "active",
		Title: "Research task", CreatedAt: now, UpdatedAt: now,
	})
	createSession(dbPath, Session{
		ID: "s2", Agent: "黒曜", Source: "telegram", Status: "completed",
		Title: "Dev task", CreatedAt: now, UpdatedAt: now,
	})
	createSession(dbPath, Session{
		ID: "s3", Agent: "翡翠", Source: "cron", Status: "active",
		Title: "Auto research", CreatedAt: now, UpdatedAt: now,
	})

	// Filter by role.
	sessions, total, err := querySessions(dbPath, SessionQuery{Agent: "翡翠"})
	if err != nil {
		t.Fatalf("querySessions: %v", err)
	}
	if total != 2 {
		t.Errorf("total for 翡翠 = %d, want 2", total)
	}
	if len(sessions) != 2 {
		t.Errorf("len sessions for 翡翠 = %d, want 2", len(sessions))
	}

	// Filter by status.
	// initSessionDB creates a system log session (status=active), so expect +1.
	sessions2, total2, _ := querySessions(dbPath, SessionQuery{Status: "active"})
	if total2 != 3 {
		t.Errorf("total active = %d, want 3 (2 test + 1 system log)", total2)
	}
	if len(sessions2) != 3 {
		t.Errorf("len active = %d, want 3 (2 test + 1 system log)", len(sessions2))
	}

	// Pagination.
	sessions3, _, _ := querySessions(dbPath, SessionQuery{Limit: 1})
	if len(sessions3) != 1 {
		t.Errorf("limit 1: got %d sessions", len(sessions3))
	}
}

func TestQuerySessionDetail(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	initSessionDB(dbPath)

	now := time.Now().Format(time.RFC3339)
	createSession(dbPath, Session{
		ID: "sess-detail", Agent: "琥珀", Source: "http", Status: "active",
		Title: "Creative session", CreatedAt: now, UpdatedAt: now,
	})
	addSessionMessage(dbPath, SessionMessage{
		SessionID: "sess-detail", Role: "user", Content: "Hello", CreatedAt: now,
	})
	addSessionMessage(dbPath, SessionMessage{
		SessionID: "sess-detail", Role: "assistant", Content: "Hi there!", CreatedAt: now,
	})

	detail, err := querySessionDetail(dbPath, "sess-detail")
	if err != nil {
		t.Fatalf("querySessionDetail: %v", err)
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
	initSessionDB(dbPath)

	detail, err := querySessionDetail(dbPath, "nonexistent")
	if err != nil {
		t.Fatalf("querySessionDetail: %v", err)
	}
	if detail != nil {
		t.Error("expected nil for nonexistent session")
	}
}

func TestCountActiveSessions(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	initSessionDB(dbPath)

	now := time.Now().Format(time.RFC3339)
	createSession(dbPath, Session{
		ID: "a1", Agent: "翡翠", Status: "active", CreatedAt: now, UpdatedAt: now,
	})
	createSession(dbPath, Session{
		ID: "a2", Agent: "黒曜", Status: "completed", CreatedAt: now, UpdatedAt: now,
	})
	createSession(dbPath, Session{
		ID: "a3", Agent: "琥珀", Status: "active", CreatedAt: now, UpdatedAt: now,
	})

	// initSessionDB creates a system log session (status=active), so expect +1.
	count := countActiveSessions(dbPath)
	if count != 3 {
		t.Errorf("active count = %d, want 3 (2 test + 1 system log)", count)
	}
}

func TestSessionSpecialChars(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	initSessionDB(dbPath)

	now := time.Now().Format(time.RFC3339)
	createSession(dbPath, Session{
		ID: "sess-special", Agent: "琥珀", Source: "http", Status: "active",
		Title: `He said "it's fine" & <ok>`, CreatedAt: now, UpdatedAt: now,
	})

	addSessionMessage(dbPath, SessionMessage{
		SessionID: "sess-special", Role: "user",
		Content: `Prompt with 'quotes' and "double quotes"`, CreatedAt: now,
	})

	got, _ := querySessionByID(dbPath, "sess-special")
	if got.Title != `He said "it's fine" & <ok>` {
		t.Errorf("title = %q", got.Title)
	}

	msgs, _ := querySessionMessages(dbPath, "sess-special")
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
		got := channelSessionKey(tc.source, tc.parts...)
		if got != tc.want {
			t.Errorf("channelSessionKey(%q, %v) = %q, want %q", tc.source, tc.parts, got, tc.want)
		}
	}
}

func TestFindChannelSession(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	initSessionDB(dbPath)

	now := time.Now().Format(time.RFC3339)

	// No session yet.
	sess, err := findChannelSession(dbPath, "tg:翡翠")
	if err != nil {
		t.Fatalf("findChannelSession: %v", err)
	}
	if sess != nil {
		t.Error("expected nil for nonexistent channel session")
	}

	// Create a channel session.
	createSession(dbPath, Session{
		ID: "ch-001", Agent: "翡翠", Source: "telegram", Status: "active",
		ChannelKey: "tg:翡翠", Title: "Research", CreatedAt: now, UpdatedAt: now,
	})

	// Should find it now.
	sess, err = findChannelSession(dbPath, "tg:翡翠")
	if err != nil {
		t.Fatalf("findChannelSession: %v", err)
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

	// Should NOT find a different channel key.
	sess2, _ := findChannelSession(dbPath, "tg:黒曜")
	if sess2 != nil {
		t.Error("expected nil for different channel key")
	}

	// Archived sessions should not be found.
	updateSessionStatus(dbPath, "ch-001", "archived")
	sess3, _ := findChannelSession(dbPath, "tg:翡翠")
	if sess3 != nil {
		t.Error("expected nil for archived channel session")
	}
}

func TestGetOrCreateChannelSession(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	initSessionDB(dbPath)

	// First call creates.
	sess, err := getOrCreateChannelSession(dbPath, "telegram", "tg:琥珀", "琥珀", "")
	if err != nil {
		t.Fatalf("getOrCreateChannelSession: %v", err)
	}
	if sess == nil {
		t.Fatal("expected session, got nil")
	}
	firstID := sess.ID
	if sess.Agent != "琥珀" {
		t.Errorf("role = %q, want %q", sess.Agent, "琥珀")
	}

	// Second call finds the existing one.
	sess2, err := getOrCreateChannelSession(dbPath, "telegram", "tg:琥珀", "琥珀", "")
	if err != nil {
		t.Fatalf("getOrCreateChannelSession (2nd): %v", err)
	}
	if sess2.ID != firstID {
		t.Errorf("expected same session ID %q, got %q", firstID, sess2.ID)
	}
}

func TestBuildSessionContext(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	initSessionDB(dbPath)

	now := time.Now().Format(time.RFC3339)
	createSession(dbPath, Session{
		ID: "ctx-001", Agent: "翡翠", Source: "telegram", Status: "active",
		CreatedAt: now, UpdatedAt: now,
	})

	// Empty session should return empty context.
	ctx := buildSessionContext(dbPath, "ctx-001", 20)
	if ctx != "" {
		t.Errorf("expected empty context, got %q", ctx)
	}

	// Add messages.
	addSessionMessage(dbPath, SessionMessage{
		SessionID: "ctx-001", Role: "user", Content: "How do goroutines work?", CreatedAt: now,
	})
	addSessionMessage(dbPath, SessionMessage{
		SessionID: "ctx-001", Role: "assistant", Content: "Goroutines are lightweight threads.", CreatedAt: now,
	})
	addSessionMessage(dbPath, SessionMessage{
		SessionID: "ctx-001", Role: "user", Content: "What about channels?", CreatedAt: now,
	})

	ctx = buildSessionContext(dbPath, "ctx-001", 20)
	if ctx == "" {
		t.Fatal("expected non-empty context")
	}
	if !contains(ctx, "[user] How do goroutines work?") {
		t.Error("context missing user message")
	}
	if !contains(ctx, "[assistant] Goroutines are lightweight threads.") {
		t.Error("context missing assistant message")
	}

	// Limit to 2 messages.
	ctx2 := buildSessionContext(dbPath, "ctx-001", 2)
	if contains(ctx2, "goroutines work") {
		t.Error("limited context should not contain first message")
	}
	if !contains(ctx2, "[user] What about channels?") {
		t.Error("limited context should contain last user message")
	}
}

func TestWrapWithContext(t *testing.T) {
	// No context — return prompt unchanged.
	got := wrapWithContext("", "Hello world")
	if got != "Hello world" {
		t.Errorf("expected unchanged prompt, got %q", got)
	}

	// With context.
	got2 := wrapWithContext("[user] Previous msg", "New message")
	if !contains(got2, "<conversation_history>") {
		t.Error("missing conversation_history opening tag")
	}
	if !contains(got2, "</conversation_history>") {
		t.Error("missing conversation_history closing tag")
	}
	if !contains(got2, "Previous msg") {
		t.Error("missing context content")
	}
	if !contains(got2, "New message") {
		t.Error("missing new prompt")
	}
}

func TestArchiveChannelSession(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	initSessionDB(dbPath)

	now := time.Now().Format(time.RFC3339)
	createSession(dbPath, Session{
		ID: "arch-001", Agent: "翡翠", Source: "telegram", Status: "active",
		ChannelKey: "tg:翡翠", CreatedAt: now, UpdatedAt: now,
	})

	if err := archiveChannelSession(dbPath, "tg:翡翠"); err != nil {
		t.Fatalf("archiveChannelSession: %v", err)
	}

	sess, _ := querySessionByID(dbPath, "arch-001")
	if sess.Status != "archived" {
		t.Errorf("status = %q, want %q", sess.Status, "archived")
	}

	// Archiving nonexistent should not error.
	if err := archiveChannelSession(dbPath, "tg:nonexistent"); err != nil {
		t.Fatalf("archiveChannelSession (nonexistent): %v", err)
	}
}

func TestSessionConfigDefaults(t *testing.T) {
	c := SessionConfig{}
	if c.ContextMessagesOrDefault() != 20 {
		t.Errorf("contextMessages default = %d, want 20", c.ContextMessagesOrDefault())
	}
	if c.CompactAfterOrDefault() != 30 {
		t.Errorf("compactAfter default = %d, want 30", c.CompactAfterOrDefault())
	}
	if c.CompactKeepOrDefault() != 10 {
		t.Errorf("compactKeep default = %d, want 10", c.CompactKeepOrDefault())
	}

	c2 := SessionConfig{ContextMessages: 5, CompactAfter: 15, CompactKeep: 3}
	if c2.ContextMessagesOrDefault() != 5 {
		t.Errorf("contextMessages = %d, want 5", c2.ContextMessagesOrDefault())
	}
	if c2.CompactAfterOrDefault() != 15 {
		t.Errorf("compactAfter = %d, want 15", c2.CompactAfterOrDefault())
	}
	if c2.CompactKeepOrDefault() != 3 {
		t.Errorf("compactKeep = %d, want 3", c2.CompactKeepOrDefault())
	}
}

func TestChannelKeyInQuerySessions(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	initSessionDB(dbPath)

	now := time.Now().Format(time.RFC3339)
	createSession(dbPath, Session{
		ID: "chq-001", Agent: "翡翠", Source: "telegram", Status: "active",
		ChannelKey: "tg:翡翠", Title: "Research", CreatedAt: now, UpdatedAt: now,
	})

	// querySessions should include channel_key.
	sessions, _, _ := querySessions(dbPath, SessionQuery{Agent: "翡翠"})
	if len(sessions) == 0 {
		t.Fatal("expected sessions")
	}
	if sessions[0].ChannelKey != "tg:翡翠" {
		t.Errorf("channel_key = %q, want %q", sessions[0].ChannelKey, "tg:翡翠")
	}
}

func TestQuerySessionDetailPrefixMatch(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	initSessionDB(dbPath)

	now := time.Now().Format(time.RFC3339)
	// Use realistic UUID-like IDs to exercise the prefix path (len < 36 check).
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
	createSession(dbPath, s1)
	createSession(dbPath, s2)
	createSession(dbPath, s3)

	// Prefix that matches exactly one session.
	detail, err := querySessionDetail(dbPath, "deadbeef")
	if err != nil {
		t.Fatalf("querySessionDetail (unique prefix): %v", err)
	}
	if detail == nil {
		t.Fatal("expected detail, got nil")
	}
	if detail.Session.ID != s3.ID {
		t.Errorf("got session ID %q, want %q", detail.Session.ID, s3.ID)
	}

	// Prefix that matches two sessions → ErrAmbiguousSession.
	_, err = querySessionDetail(dbPath, "9c1bbafa-6cc")
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

	// Prefix with no matches → nil, no error.
	detail2, err2 := querySessionDetail(dbPath, "ffffffff")
	if err2 != nil {
		t.Fatalf("querySessionDetail (no match): %v", err2)
	}
	if detail2 != nil {
		t.Error("expected nil for no-match prefix")
	}

	// Exact full ID match always works (bypasses prefix path).
	detail3, err3 := querySessionDetail(dbPath, s1.ID)
	if err3 != nil {
		t.Fatalf("querySessionDetail (exact): %v", err3)
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
	initSessionDB(dbPath)

	now := time.Now().Format(time.RFC3339)
	createSession(dbPath, Session{
		ID: "aaaa-0001", Agent: "翡翠", Source: "http", Status: "active",
		Title: "First", CreatedAt: now, UpdatedAt: now,
	})
	createSession(dbPath, Session{
		ID: "aaaa-0002", Agent: "黒曜", Source: "cli", Status: "active",
		Title: "Second", CreatedAt: now, UpdatedAt: now,
	})
	createSession(dbPath, Session{
		ID: "bbbb-0001", Agent: "琥珀", Source: "http", Status: "active",
		Title: "Third", CreatedAt: now, UpdatedAt: now,
	})

	// Prefix "aaaa" matches two.
	matches, err := querySessionsByPrefix(dbPath, "aaaa")
	if err != nil {
		t.Fatalf("querySessionsByPrefix: %v", err)
	}
	if len(matches) != 2 {
		t.Errorf("expected 2 matches for prefix 'aaaa', got %d", len(matches))
	}

	// Prefix "bbbb" matches one.
	matches2, err := querySessionsByPrefix(dbPath, "bbbb")
	if err != nil {
		t.Fatalf("querySessionsByPrefix: %v", err)
	}
	if len(matches2) != 1 {
		t.Errorf("expected 1 match for prefix 'bbbb', got %d", len(matches2))
	}
	if matches2[0].ID != "bbbb-0001" {
		t.Errorf("got ID %q, want %q", matches2[0].ID, "bbbb-0001")
	}

	// Prefix "cccc" matches none.
	matches3, err := querySessionsByPrefix(dbPath, "cccc")
	if err != nil {
		t.Fatalf("querySessionsByPrefix: %v", err)
	}
	if len(matches3) != 0 {
		t.Errorf("expected 0 matches for prefix 'cccc', got %d", len(matches3))
	}
}

func TestJoinStrings(t *testing.T) {
	tests := []struct {
		input []string
		sep   string
		want  string
	}{
		{nil, " AND ", ""},
		{[]string{"a"}, " AND ", "a"},
		{[]string{"a", "b"}, " AND ", "a AND b"},
		{[]string{"x", "y", "z"}, ", ", "x, y, z"},
	}
	for _, tc := range tests {
		got := strings.Join(tc.input, tc.sep)
		if got != tc.want {
			t.Errorf("strings.Join(%v, %q) = %q, want %q", tc.input, tc.sep, got, tc.want)
		}
	}
}
