package session

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"tetora/internal/db"
	"tetora/internal/log"
)

// Correction patterns — phrases that signal the user is correcting the agent.
// Grouped by language: Japanese, Chinese, English.
var correctionPatterns = []*regexp.Regexp{
	// Chinese corrections
	regexp.MustCompile(`(?i)^不[對是要]`),
	regexp.MustCompile(`(?i)不是這樣`),
	regexp.MustCompile(`(?i)應該是`),
	regexp.MustCompile(`(?i)不要這樣`),
	regexp.MustCompile(`(?i)你搞錯了`),
	regexp.MustCompile(`(?i)錯了`),
	regexp.MustCompile(`(?i)我(的意思|說的)是`),
	regexp.MustCompile(`(?i)你(理解|搞)錯`),
	regexp.MustCompile(`(?i)不是這個意思`),
	regexp.MustCompile(`(?i)別這樣做`),

	// Japanese corrections
	regexp.MustCompile(`(?i)違う`),
	regexp.MustCompile(`(?i)そうじゃなく`),
	regexp.MustCompile(`(?i)ではなく`),
	regexp.MustCompile(`(?i)じゃなくて`),

	// English corrections — patterns require negation/correction context to reduce false positives.
	// "No problem" / "No worries" are NOT corrections, so we require follow-up correction words.
	regexp.MustCompile(`(?i)^no[,.]?\s+(that'?s|don'?t|wrong|not|it'?s|you|I|stop|wait)`),
	regexp.MustCompile(`(?i)^wrong`),
	regexp.MustCompile(`(?i)^actually[,.]?\s+(I|that|it|the|you|don'?t|no|this)`),
	regexp.MustCompile(`(?i)that'?s not (right|correct|what)`),
	regexp.MustCompile(`(?i)^not like that`),
	regexp.MustCompile(`(?i)^I (meant|said)\s`),
	regexp.MustCompile(`(?i)don'?t do (that|this|it)`),
	regexp.MustCompile(`(?i)^instead[,.]?\s`),
	regexp.MustCompile(`(?i)you misunderstood`),
}

// IsCorrection checks if a user message matches correction patterns.
func IsCorrection(msg string) bool {
	msg = strings.TrimSpace(msg)
	if len(msg) < 2 || len(msg) > 2000 {
		return false
	}
	for _, re := range correctionPatterns {
		if re.MatchString(msg) {
			return true
		}
	}
	return false
}

// QueryLastAssistantMessage returns the most recent assistant message for a session.
func QueryLastAssistantMessage(dbPath, sessionID string) string {
	if dbPath == "" || sessionID == "" {
		return ""
	}
	sql := fmt.Sprintf(
		`SELECT content FROM session_messages
		 WHERE session_id = '%s' AND role = 'assistant'
		 ORDER BY id DESC LIMIT 1`,
		db.Escape(sessionID))
	rows, err := db.Query(dbPath, sql)
	if err != nil || len(rows) == 0 {
		return ""
	}
	content, _ := rows[0]["content"].(string)
	// Decrypt if needed.
	if k := getEncryptionKey(); k != "" && DecryptFn != nil {
		if dec, err := DecryptFn(content, k); err == nil {
			content = dec
		}
	}
	return content
}

// RecordCorrection writes a correction-based lesson to workspace/rules/conversation-corrections.md.
// This file is auto-injected into all agent prompts via InjectContent.
func RecordCorrection(workspaceDir, agent, userMsg, lastAssistantMsg string) error {
	if workspaceDir == "" {
		return nil
	}

	rulesDir := filepath.Join(workspaceDir, "rules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		return fmt.Errorf("create rules dir: %w", err)
	}

	correctionsPath := filepath.Join(rulesDir, "conversation-corrections.md")

	// Read existing for dedup + cap.
	existing, _ := os.ReadFile(correctionsPath)
	content := string(existing)

	// Dedup: check if user's correction (first 80 chars) already recorded.
	needle := userMsg
	if len(needle) > 80 {
		needle = needle[:80]
	}
	if strings.Contains(content, needle) {
		return nil
	}

	// Cap at 50 entries.
	count := strings.Count(content, "\n- **")
	if count >= 50 {
		// Trim oldest entries (keep header + last 40).
		lines := strings.Split(content, "\n")
		var header []string
		var entries []string
		inHeader := true
		for _, l := range lines {
			if inHeader && !strings.HasPrefix(l, "- **") {
				header = append(header, l)
			} else {
				inHeader = false
				entries = append(entries, l)
			}
		}
		if len(entries) > 40 {
			entries = entries[len(entries)-40:]
		}
		content = strings.Join(header, "\n") + "\n" + strings.Join(entries, "\n") + "\n"
	}

	// Build entry.
	date := time.Now().Format("2006-01-02 15:04")

	// Truncate for readability.
	correctionSnippet := userMsg
	if len(correctionSnippet) > 200 {
		correctionSnippet = correctionSnippet[:200] + "..."
	}
	assistantSnippet := lastAssistantMsg
	if len(assistantSnippet) > 150 {
		assistantSnippet = assistantSnippet[:150] + "..."
	}

	entry := fmt.Sprintf("- **%s** (%s) 主人說：「%s」", date, agent, correctionSnippet)
	if assistantSnippet != "" {
		entry += fmt.Sprintf("\n  - 之前回覆：%s", assistantSnippet)
	}
	entry += "\n"

	// Create with header if new.
	if content == "" {
		content = "# 對話修正記錄\n\n主人在對話中修正 agent 的記錄。這些是最重要的學習信號。\n琉璃的自我強化 cron 會定期審閱此檔案，將重複模式提升為永久規則。\n\n"
	}

	content += entry

	log.Info("correction detected", "agent", agent, "snippet", needle[:min(len(needle), 40)])
	return os.WriteFile(correctionsPath, []byte(content), 0o644)
}
