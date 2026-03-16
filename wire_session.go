package main

// wire_session.go bridges root callers to internal/session.
// Functions that depend on root-only types (Task, *Config) stay here.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"tetora/internal/crypto"
	"tetora/internal/session"
)

func init() {
	session.EncryptionKeyFn = globalEncryptionKey
	session.EncryptFn = crypto.Encrypt
	session.DecryptFn = crypto.Decrypt
}

// --- Type aliases ---

type Session = session.Session
type SessionMessage = session.SessionMessage
type SessionQuery = session.SessionQuery
type SessionDetail = session.SessionDetail
type ErrAmbiguousSession = session.ErrAmbiguousSession
type CleanupSessionStats = session.CleanupSessionStats

// --- Constants ---

const SystemLogSessionID = session.SystemLogSessionID

// --- DB Init ---

func initSessionDB(dbPath string) error          { return session.InitSessionDB(dbPath) }
func cleanupZombieSessions(dbPath string)         { session.CleanupZombieSessions(dbPath) }

// --- Insert ---

func createSession(dbPath string, s Session) error            { return session.CreateSession(dbPath, s) }
func addSessionMessage(dbPath string, msg SessionMessage) error { return session.AddSessionMessage(dbPath, msg) }

// --- Update ---

func updateSessionStats(dbPath, sessionID string, costDelta float64, tokensInDelta, tokensOutDelta, msgCountDelta int) error {
	return session.UpdateSessionStats(dbPath, sessionID, costDelta, tokensInDelta, tokensOutDelta, msgCountDelta)
}

func updateSessionStatus(dbPath, sessionID, status string) error {
	return session.UpdateSessionStatus(dbPath, sessionID, status)
}

func updateSessionTitle(dbPath, sessionID, title string) error {
	return session.UpdateSessionTitle(dbPath, sessionID, title)
}

// --- Query ---

func querySessions(dbPath string, q SessionQuery) ([]Session, int, error) {
	return session.QuerySessions(dbPath, q)
}

func querySessionByID(dbPath, id string) (*Session, error) {
	return session.QuerySessionByID(dbPath, id)
}

func querySessionsByPrefix(dbPath, prefix string) ([]Session, error) {
	return session.QuerySessionsByPrefix(dbPath, prefix)
}

func querySessionMessages(dbPath, sessionID string) ([]SessionMessage, error) {
	return session.QuerySessionMessages(dbPath, sessionID)
}

func querySessionDetail(dbPath, sessionID string) (*SessionDetail, error) {
	return session.QuerySessionDetail(dbPath, sessionID)
}

func countActiveSessions(dbPath string) int { return session.CountActiveSessions(dbPath) }
func countUserSessions(dbPath string) int   { return session.CountUserSessions(dbPath) }

// --- Cleanup ---

func cleanupSessions(dbPath string, days int) error { return session.CleanupSessions(dbPath, days) }

func cleanupSessionsWithStats(dbPath string, days int, dryRun bool) (CleanupSessionStats, error) {
	return session.CleanupSessionsWithStats(dbPath, days, dryRun)
}

func fixMissingSessions(dbPath string, days int, dryRun bool) (int, error) {
	return session.FixMissingSessions(dbPath, days, dryRun)
}

// --- Channel Session ---

func channelSessionKey(source string, parts ...string) string {
	return session.ChannelSessionKey(source, parts...)
}

func findChannelSession(dbPath, chKey string) (*Session, error) {
	return session.FindChannelSession(dbPath, chKey)
}

func getOrCreateChannelSession(dbPath, source, chKey, role, title string) (*Session, error) {
	return session.GetOrCreateChannelSession(dbPath, source, chKey, role, title)
}

func archiveChannelSession(dbPath, chKey string) error {
	return session.ArchiveChannelSession(dbPath, chKey)
}

// --- Row Parsers ---

func sessionMessageFromRow(row map[string]any) SessionMessage {
	return session.SessionMessageFromRow(row)
}

// --- Context Building ---

func buildSessionContext(dbPath, sessionID string, maxMessages int) string {
	return session.BuildSessionContext(dbPath, sessionID, maxMessages)
}

func buildSessionContextWithLimit(dbPath, sessionID string, maxMessages, maxChars int) string {
	return session.BuildSessionContextWithLimit(dbPath, sessionID, maxMessages, maxChars)
}

func wrapWithContext(sessionContext, prompt string) string {
	return session.WrapWithContext(sessionContext, prompt)
}

// --- Root-only functions (depend on Task, *Config, dispatch) ---

// compactSession summarizes old messages when a session grows too large.
func compactSession(ctx context.Context, cfg *Config, dbPath, sessionID string, tokenTriggered bool, sem, childSem chan struct{}) error {
	if dbPath == "" {
		return nil
	}

	sess, err := querySessionByID(dbPath, sessionID)
	if err != nil || sess == nil {
		return err
	}

	keep := cfg.Session.compactKeepOrDefault()
	if tokenTriggered {
		keep = keep * 2
		if keep < 15 {
			keep = 15
		}
	}
	if sess.MessageCount <= keep {
		return nil
	}

	msgs, err := querySessionMessages(dbPath, sessionID)
	if err != nil || len(msgs) <= keep {
		return nil
	}

	oldMsgs := msgs[:len(msgs)-keep]

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

	result := runSingleTask(ctx, cfg, task, sem, childSem, coordinator)
	if result.Status != "success" {
		return fmt.Errorf("compaction summary failed: %s", result.Error)
	}

	summaryText := fmt.Sprintf("[Context Summary] %s", strings.TrimSpace(result.Output))

	lastOldID := oldMsgs[len(oldMsgs)-1].ID
	delSQL := fmt.Sprintf(
		`DELETE FROM session_messages WHERE session_id = '%s' AND id <= %d`,
		escapeSQLite(sessionID), lastOldID)
	if err := execDB(dbPath, delSQL); err != nil {
		return fmt.Errorf("delete old messages: %w", err)
	}

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

// maybeCompactSession triggers compaction if the session exceeds thresholds.
func maybeCompactSession(cfg *Config, dbPath, sessionID string, msgCount, tokensIn int, sem, childSem chan struct{}) {
	msgThreshold := cfg.Session.compactAfterOrDefault()
	tokenThreshold := cfg.Session.compactTokensOrDefault()
	tokenTriggered := tokensIn > tokenThreshold
	if msgCount <= msgThreshold && !tokenTriggered {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := compactSession(ctx, cfg, dbPath, sessionID, tokenTriggered, sem, childSem); err != nil {
			logWarn("session compaction failed", "session", sessionID, "error", err)
		}
	}()
}

// recordSessionActivity records user message and assistant response for a completed task.
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

		title := task.Prompt
		if len(title) > 100 {
			title = title[:100]
		}

		if err := createSession(dbPath, Session{
			ID:        sessionID,
			Agent:     role,
			Source:    task.Source,
			Status:    "active",
			Title:     title,
			CreatedAt: now,
			UpdatedAt: now,
		}); err != nil {
			logWarn("create session failed", "session", sessionID, "error", err)
		}

		if err := addSessionMessage(dbPath, SessionMessage{
			SessionID: sessionID,
			Role:      "user",
			Content:   truncateStr(task.Prompt, 5000),
			TaskID:    task.ID,
			CreatedAt: now,
		}); err != nil {
			logWarn("add user message failed", "session", sessionID, "error", err)
		}

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

		if err := updateSessionStats(dbPath, sessionID, result.CostUSD, result.TokensIn, result.TokensOut, 2); err != nil {
			logWarn("update session stats failed", "session", sessionID, "error", err)
		}

		existing, _ := querySessionByID(dbPath, sessionID)
		if existing == nil || existing.ChannelKey == "" {
			updateSessionStatus(dbPath, sessionID, "completed")
		}
	}()
}

// logSystemDispatch appends a summary of a dispatch task to the system log session.
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
