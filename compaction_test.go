package main

import (
	"fmt"
	"path/filepath"
	"testing"
)

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
			if got := tt.config.maxMessages(); got != tt.want["maxMessages"] {
				t.Errorf("maxMessages() = %v, want %v", got, tt.want["maxMessages"])
			}
			if got := tt.config.compactTo(); got != tt.want["compactTo"] {
				t.Errorf("compactTo() = %v, want %v", got, tt.want["compactTo"])
			}
			if got := tt.config.model(); got != tt.want["model"] {
				t.Errorf("model() = %v, want %v", got, tt.want["model"])
			}
			if got := tt.config.maxCost(); got != tt.want["maxCost"] {
				t.Errorf("maxCost() = %v, want %v", got, tt.want["maxCost"])
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
				{ID: 1, Role: "user", Content: "Hello", Timestamp: "2026-01-01 10:00:00"},
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
				{ID: 1, Role: "user", Content: "What's the weather?", Timestamp: "2026-01-01 10:00:00"},
				{ID: 2, Role: "assistant", Content: "It's sunny.", Timestamp: "2026-01-01 10:01:00"},
				{ID: 3, Role: "user", Content: "Great!", Timestamp: "2026-01-01 10:02:00"},
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
				{ID: 1, Role: "system", Content: "Init", Timestamp: ""},
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
	sql := fmt.Sprintf("INSERT INTO sessions (id, role, source, status, title, created_at, updated_at) VALUES ('%s', 'test', 'test', 'active', 'Test', datetime('now'), datetime('now'))", sessionID)
	queryDB(dbPath, sql)

	// Insert messages.
	for i := 1; i <= 5; i++ {
		sql := fmt.Sprintf("INSERT INTO session_messages (session_id, role, content, created_at) VALUES ('%s', 'user', 'Message %d', datetime('now'))",
			sessionID, i)
		queryDB(dbPath, sql)
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
	sql := fmt.Sprintf("INSERT INTO sessions (id, role, source, status, title, created_at, updated_at) VALUES ('%s', 'test', 'test', 'active', 'Test', datetime('now'), datetime('now'))", sessionID)
	queryDB(dbPath, sql)

	// Insert 10 messages.
	for i := 1; i <= 10; i++ {
		sql := fmt.Sprintf("INSERT INTO session_messages (session_id, role, content, created_at) VALUES ('%s', 'user', 'Message %d', datetime('now', '+%d seconds'))",
			sessionID, i, i)
		queryDB(dbPath, sql)
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
	sql := fmt.Sprintf("INSERT INTO sessions (id, role, source, status, title, created_at, updated_at) VALUES ('%s', 'test', 'test', 'active', 'Test', datetime('now'), datetime('now'))", sessionID)
	queryDB(dbPath, sql)

	// Insert 5 messages.
	for i := 1; i <= 5; i++ {
		sql := fmt.Sprintf("INSERT INTO session_messages (session_id, role, content, created_at) VALUES ('%s', 'user', 'Message %d', datetime('now'))",
			sessionID, i)
		queryDB(dbPath, sql)
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
		escapeSQLite(sessionID))
	rows, err := queryDB(dbPath, sql)
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
	sql := fmt.Sprintf("INSERT INTO sessions (id, role, source, status, title, created_at, updated_at) VALUES ('%s', 'test', 'test', 'active', 'Test', datetime('now'), datetime('now'))", sessionID)
	queryDB(dbPath, sql)

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
