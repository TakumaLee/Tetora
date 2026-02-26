package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

// --- Compaction Types ---

type CompactionConfig struct {
	Enabled     bool    `json:"enabled,omitempty"`
	MaxMessages int     `json:"maxMessages,omitempty"` // trigger threshold, default 50
	CompactTo   int     `json:"compactTo,omitempty"`   // keep recent N messages, default 10
	Model       string  `json:"model,omitempty"`       // LLM model for summary, default "haiku"
	MaxCost     float64 `json:"maxCost,omitempty"`     // max cost per compaction, default 0.02
	Provider    string  `json:"provider,omitempty"`    // provider name, default from config
}

// Helper methods with defaults.
func (c CompactionConfig) maxMessages() int {
	if c.MaxMessages <= 0 {
		return 50
	}
	return c.MaxMessages
}

func (c CompactionConfig) compactTo() int {
	if c.CompactTo <= 0 {
		return 10
	}
	return c.CompactTo
}

func (c CompactionConfig) model() string {
	if c.Model == "" {
		return "haiku"
	}
	return c.Model
}

func (c CompactionConfig) maxCost() float64 {
	if c.MaxCost <= 0 {
		return 0.02
	}
	return c.MaxCost
}

// sessionMessage represents a message in a session (read from DB).
type sessionMessage struct {
	ID        int
	SessionID string
	Agent      string
	Content   string
	Timestamp string
}

// --- Token-Based Compaction Check ---

// shouldCompactByTokens estimates whether the session context exceeds 75% of the model's context window.
func shouldCompactByTokens(cfg *Config, messages []sessionMessage, systemPromptLen, toolDefsLen int) bool {
	var totalChars int
	totalChars += systemPromptLen
	totalChars += toolDefsLen
	for _, m := range messages {
		totalChars += len(m.Content)
	}
	estimatedTokens := totalChars / 4 // rough estimate: 4 chars per token
	contextLimit := 200000            // model context window
	return estimatedTokens > contextLimit*75/100
}

// --- Core Compaction Logic ---

// checkCompaction checks if a session needs compaction and runs it if so.
// This function is designed to be called asynchronously after task completion.
func checkCompaction(cfg *Config, sessionID string) error {
	if !cfg.Session.Compaction.Enabled {
		return nil
	}

	// 1. Count session messages.
	count := countSessionMessages(cfg, sessionID)
	if count <= cfg.Session.Compaction.maxMessages() {
		return nil
	}

	logInfo("compaction triggered for session %s (%d messages, threshold %d)", sessionID, count, cfg.Session.Compaction.maxMessages())

	// 2. Get oldest messages to compact.
	toCompact := count - cfg.Session.Compaction.compactTo()
	if toCompact <= 0 {
		return nil
	}

	messages := getOldestMessages(cfg, sessionID, toCompact)
	if len(messages) == 0 {
		logWarn("no messages to compact", "sessionID", sessionID)
		return nil
	}

	// 3. Generate summary via LLM.
	summary, err := compactMessages(cfg, messages)
	if err != nil {
		logError("compaction failed", "sessionID", sessionID, "error", err)
		return err
	}

	// 4. Delete old messages, insert compacted summary.
	if err := replaceWithSummary(cfg, sessionID, messages, summary); err != nil {
		logError("replace with summary failed", "sessionID", sessionID, "error", err)
		return err
	}

	logInfo("compacted %d messages for session %s", len(messages), sessionID)
	return nil
}

// countSessionMessages counts messages for a session.
func countSessionMessages(cfg *Config, sessionID string) int {
	dbPath := cfg.HistoryDB
	if dbPath == "" {
		return 0
	}

	sql := fmt.Sprintf("SELECT COUNT(*) as count FROM session_messages WHERE session_id = '%s'",
		escapeSQLite(sessionID))
	rows, err := queryDB(dbPath, sql)
	if err != nil || len(rows) == 0 {
		return 0
	}

	// Parse count from result (SQLite JSON returns numbers as float64).
	if countVal, ok := rows[0]["count"]; ok {
		if countFloat, ok := countVal.(float64); ok {
			return int(countFloat)
		}
	}
	return 0
}

// getOldestMessages retrieves the oldest N messages for a session.
func getOldestMessages(cfg *Config, sessionID string, limit int) []sessionMessage {
	dbPath := cfg.HistoryDB
	if dbPath == "" {
		return nil
	}

	sql := fmt.Sprintf("SELECT id, session_id, role, content, created_at FROM session_messages WHERE session_id = '%s' ORDER BY id ASC LIMIT %d",
		escapeSQLite(sessionID), limit)
	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return nil
	}

	messages := make([]sessionMessage, 0, len(rows))
	for _, row := range rows {
		msg := sessionMessage{
			SessionID: sessionID,
		}

		if idVal, ok := row["id"]; ok {
			if idFloat, ok := idVal.(float64); ok {
				msg.ID = int(idFloat)
			}
		}
		if roleVal, ok := row["role"]; ok {
			if roleStr, ok := roleVal.(string); ok {
				msg.Agent = roleStr
			}
		}
		if contentVal, ok := row["content"]; ok {
			if contentStr, ok := contentVal.(string); ok {
				msg.Content = contentStr
			}
		}
		if tsVal, ok := row["created_at"]; ok {
			if tsStr, ok := tsVal.(string); ok {
				msg.Timestamp = tsStr
			}
		}

		messages = append(messages, msg)
	}

	return messages
}

// compactMessages sends messages to LLM for summarization.
func compactMessages(cfg *Config, messages []sessionMessage) (string, error) {
	if len(messages) == 0 {
		return "", fmt.Errorf("no messages to compact")
	}

	prompt := buildCompactionPrompt(messages)

	// Build a minimal task for summarization.
	task := Task{
		ID:           fmt.Sprintf("compact-%d", time.Now().Unix()),
		Name:         "session-compaction",
		Prompt:       prompt,
		Model:        cfg.Session.Compaction.model(),
		Provider:     cfg.Session.Compaction.Provider,
		Timeout:      "60s",
		Budget:       cfg.Session.Compaction.maxCost(),
		SystemPrompt: "You are a conversation summarizer. Summarize the following conversation, preserving key facts, decisions, action items, and important context. Be concise but thorough. Output only the summary, no preamble.",
		Source:       "compaction",
	}

	// Use existing dispatch mechanism.
	// Create a minimal dispatch state for task execution.
	timeout := 60 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	state := newDispatchState()
	result := runTask(ctx, cfg, task, state)

	if result.Status != "success" {
		return "", fmt.Errorf("compaction task failed: %s", result.Error)
	}

	return result.Output, nil
}

// buildCompactionPrompt formats messages into a prompt for summarization.
func buildCompactionPrompt(messages []sessionMessage) string {
	var sb strings.Builder
	sb.WriteString("Summarize this conversation segment, preserving key information:\n\n")

	for _, m := range messages {
		// Format: [timestamp] role: content
		ts := m.Timestamp
		if ts == "" {
			ts = "unknown"
		}
		sb.WriteString(fmt.Sprintf("[%s] %s: %s\n\n", ts, m.Agent, m.Content))
	}

	sb.WriteString("\nProvide a concise summary that captures:\n")
	sb.WriteString("- Key decisions and action items\n")
	sb.WriteString("- Important context and facts\n")
	sb.WriteString("- Main topics discussed\n")
	sb.WriteString("- Any critical information that should not be lost\n")

	return sb.String()
}

// replaceWithSummary deletes old messages and inserts a compacted summary.
func replaceWithSummary(cfg *Config, sessionID string, oldMessages []sessionMessage, summary string) error {
	dbPath := cfg.HistoryDB
	if dbPath == "" {
		return fmt.Errorf("historyDB not configured")
	}

	// Delete old messages (by ID range).
	if len(oldMessages) > 0 {
		firstID := oldMessages[0].ID
		lastID := oldMessages[len(oldMessages)-1].ID

		deleteSQL := fmt.Sprintf("DELETE FROM session_messages WHERE session_id = '%s' AND id >= %d AND id <= %d",
			escapeSQLite(sessionID), firstID, lastID)
		queryDB(dbPath, deleteSQL)

		logDebug("deleted old messages for session %s (id range %d-%d, count %d)", sessionID, firstID, lastID, len(oldMessages))
	}

	// Insert compacted message as 'system' role.
	insertSQL := fmt.Sprintf("INSERT INTO session_messages (session_id, role, content, created_at) VALUES ('%s', 'system', '[COMPACTED] %s', datetime('now'))",
		escapeSQLite(sessionID), escapeSQLite(summary))
	queryDB(dbPath, insertSQL)

	logDebug("inserted compacted summary for session %s (length %d)", sessionID, len(summary))

	return nil
}

// --- CLI Command ---

// runCompaction handles the CLI: tetora compact <sessionID> or tetora compact --all
func runCompaction(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: tetora compact <sessionID>")
		fmt.Println("       tetora compact --all")
		fmt.Println()
		fmt.Println("Manually compact session messages to reduce context length.")
		fmt.Println()
		fmt.Println("Examples:")
		fmt.Println("  tetora compact abc123       # Compact specific session")
		fmt.Println("  tetora compact --all        # Compact all sessions exceeding threshold")
		return
	}

	cfg := loadConfig("")

	if args[0] == "--all" {
		compactAllSessions(cfg)
		return
	}

	sessionID := args[0]

	// Check if session exists.
	if !sessionExists(cfg, sessionID) {
		fmt.Printf("Error: session %s not found\n", sessionID)
		os.Exit(1)
	}

	// Force compaction regardless of threshold.
	count := countSessionMessages(cfg, sessionID)
	fmt.Printf("Session %s has %d messages\n", sessionID, count)

	if count <= cfg.Session.Compaction.compactTo() {
		fmt.Printf("Session has too few messages to compact (minimum: %d)\n", cfg.Session.Compaction.compactTo()+1)
		return
	}

	fmt.Printf("Compacting to %d most recent messages...\n", cfg.Session.Compaction.compactTo())

	if err := checkCompaction(cfg, sessionID); err != nil {
		fmt.Printf("Compaction failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Compaction completed successfully")
}

// compactAllSessions compacts all sessions exceeding the threshold.
func compactAllSessions(cfg *Config) {
	if !cfg.Session.Compaction.Enabled {
		fmt.Println("Compaction is disabled in config")
		return
	}

	dbPath := cfg.HistoryDB
	if dbPath == "" {
		fmt.Println("Error: historyDB not configured")
		os.Exit(1)
	}

	// Get all sessions with message count > threshold.
	sql := fmt.Sprintf(`
		SELECT session_id, COUNT(*) as count
		FROM session_messages
		GROUP BY session_id
		HAVING count > %d
	`, cfg.Session.Compaction.maxMessages())

	rows, err := queryDB(dbPath, sql)
	if err != nil {
		fmt.Printf("Query error: %v\n", err)
		os.Exit(1)
	}
	if len(rows) == 0 {
		fmt.Println("No sessions require compaction")
		return
	}

	fmt.Printf("Found %d sessions to compact\n", len(rows))

	successCount := 0
	for _, row := range rows {
		sessionID := ""
		if sidVal, ok := row["session_id"]; ok {
			if sidStr, ok := sidVal.(string); ok {
				sessionID = sidStr
			}
		}

		if sessionID == "" {
			continue
		}

		countVal := 0
		if cVal, ok := row["count"]; ok {
			if cFloat, ok := cVal.(float64); ok {
				countVal = int(cFloat)
			}
		}

		fmt.Printf("Compacting session %s (%d messages)...\n", sessionID, countVal)

		if err := checkCompaction(cfg, sessionID); err != nil {
			fmt.Printf("  Failed: %v\n", err)
		} else {
			fmt.Printf("  Success\n")
			successCount++
		}
	}

	fmt.Printf("\nCompacted %d/%d sessions\n", successCount, len(rows))
}

// sessionExists checks if a session exists in the database.
func sessionExists(cfg *Config, sessionID string) bool {
	dbPath := cfg.HistoryDB
	if dbPath == "" {
		return false
	}

	sql := fmt.Sprintf("SELECT id FROM sessions WHERE id = '%s' LIMIT 1",
		escapeSQLite(sessionID))
	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return false
	}
	return len(rows) > 0
}
