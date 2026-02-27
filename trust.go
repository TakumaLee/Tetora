package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// --- Trust Level Constants ---

const (
	TrustObserve = "observe" // report only, no side effects (forces plan mode)
	TrustSuggest = "suggest" // execute + present for human confirmation
	TrustAuto    = "auto"    // fully autonomous execution
)

// validTrustLevels is the ordered set of trust levels (low → high).
var validTrustLevels = []string{TrustObserve, TrustSuggest, TrustAuto}

// isValidTrustLevel checks if a string is a valid trust level.
func isValidTrustLevel(level string) bool {
	for _, v := range validTrustLevels {
		if v == level {
			return true
		}
	}
	return false
}

// trustLevelIndex returns the ordinal index (0=observe, 1=suggest, 2=auto).
func trustLevelIndex(level string) int {
	for i, v := range validTrustLevels {
		if v == level {
			return i
		}
	}
	return -1
}

// nextTrustLevel returns the next higher trust level, or "" if already at max.
func nextTrustLevel(current string) string {
	idx := trustLevelIndex(current)
	if idx < 0 || idx >= len(validTrustLevels)-1 {
		return ""
	}
	return validTrustLevels[idx+1]
}

// --- Trust Config ---

// TrustConfig configures trust gradient behavior.
type TrustConfig struct {
	Enabled          bool `json:"enabled,omitempty"`
	PromoteThreshold int  `json:"promoteThreshold,omitempty"` // consecutive successes before suggesting promotion (default 10)
	AutoPromote      bool `json:"autoPromote,omitempty"`      // auto-promote without asking (default false, just suggest)
}

func (c TrustConfig) promoteThresholdOrDefault() int {
	if c.PromoteThreshold > 0 {
		return c.PromoteThreshold
	}
	return 10
}

// --- Trust Status ---

// TrustStatus holds the trust state for a single agent.
type TrustStatus struct {
	Agent               string `json:"agent"`
	Level              string `json:"level"`
	ConsecutiveSuccess int    `json:"consecutiveSuccess"`
	PromoteReady       bool   `json:"promoteReady"`       // true if enough consecutive successes for promotion
	NextLevel          string `json:"nextLevel,omitempty"` // next level to promote to
	TotalTasks         int    `json:"totalTasks"`
	LastUpdated        string `json:"lastUpdated,omitempty"`
}

// --- DB Init ---

func initTrustDB(dbPath string) {
	if dbPath == "" {
		return
	}
	// Migration: rename role -> agent in trust_events.
	migrateSQL := `ALTER TABLE trust_events RENAME COLUMN role TO agent;`
	migrateCmd := exec.Command("sqlite3", dbPath, migrateSQL)
	migrateCmd.CombinedOutput() // ignore errors (column may already be renamed or table may not exist)

	sql := `CREATE TABLE IF NOT EXISTS trust_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  agent TEXT NOT NULL,
  event_type TEXT NOT NULL,
  from_level TEXT DEFAULT '',
  to_level TEXT DEFAULT '',
  consecutive_success INTEGER DEFAULT 0,
  created_at TEXT NOT NULL,
  note TEXT DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_trust_events_agent ON trust_events(agent);
CREATE INDEX IF NOT EXISTS idx_trust_events_time ON trust_events(created_at);`

	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		logWarn("init trust_events table failed", "error", fmt.Sprintf("%s: %s", err, out))
	}
}

// --- Trust Level Resolution ---

// resolveTrustLevel returns the effective trust level for an agent.
// Priority: agent config → default "auto".
func resolveTrustLevel(cfg *Config, agentName string) string {
	if !cfg.Trust.Enabled {
		return TrustAuto
	}
	if agentName == "" {
		return TrustAuto
	}
	if rc, ok := cfg.Agents[agentName]; ok && rc.TrustLevel != "" {
		if isValidTrustLevel(rc.TrustLevel) {
			return rc.TrustLevel
		}
	}
	return TrustAuto
}

// --- Consecutive Success Tracking ---

// queryConsecutiveSuccess counts consecutive successful tasks for an agent
// (most recent first, stopping at the first non-success).
func queryConsecutiveSuccess(dbPath, role string) int {
	if dbPath == "" || role == "" {
		return 0
	}

	sql := fmt.Sprintf(
		`SELECT status FROM job_runs
		 WHERE agent = '%s'
		 ORDER BY id DESC LIMIT 50`,
		escapeSQLite(role))

	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return 0
	}

	count := 0
	for _, r := range rows {
		if jsonStr(r["status"]) == "success" {
			count++
		} else {
			break
		}
	}
	return count
}

// --- Trust Event Recording ---

// recordTrustEvent stores a trust event in the database.
func recordTrustEvent(dbPath, role, eventType, fromLevel, toLevel string, consecutiveSuccess int, note string) {
	if dbPath == "" {
		return
	}

	sql := fmt.Sprintf(
		`INSERT INTO trust_events (agent, event_type, from_level, to_level, consecutive_success, created_at, note)
		 VALUES ('%s', '%s', '%s', '%s', %d, '%s', '%s')`,
		escapeSQLite(role),
		escapeSQLite(eventType),
		escapeSQLite(fromLevel),
		escapeSQLite(toLevel),
		consecutiveSuccess,
		time.Now().Format(time.RFC3339),
		escapeSQLite(note))

	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		logWarn("record trust event failed", "error", fmt.Sprintf("%s: %s", err, out))
	}
}

// queryTrustEvents returns recent trust events for a role.
func queryTrustEvents(dbPath, role string, limit int) ([]map[string]any, error) {
	if dbPath == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}

	where := ""
	if role != "" {
		where = fmt.Sprintf("WHERE agent = '%s'", escapeSQLite(role))
	}

	sql := fmt.Sprintf(
		`SELECT agent, event_type, from_level, to_level, consecutive_success, created_at, note
		 FROM trust_events %s ORDER BY id DESC LIMIT %d`, where, limit)

	return queryDB(dbPath, sql)
}

// --- Trust Status Queries ---

// getTrustStatus returns the trust status for a single role.
func getTrustStatus(cfg *Config, role string) TrustStatus {
	level := resolveTrustLevel(cfg, role)
	consecutiveSuccess := queryConsecutiveSuccess(cfg.HistoryDB, role)
	threshold := cfg.Trust.promoteThresholdOrDefault()
	next := nextTrustLevel(level)
	promoteReady := next != "" && consecutiveSuccess >= threshold

	// Count total tasks.
	totalTasks := 0
	if cfg.HistoryDB != "" {
		sql := fmt.Sprintf(`SELECT COUNT(*) as cnt FROM job_runs WHERE agent = '%s'`, escapeSQLite(role))
		if rows, err := queryDB(cfg.HistoryDB, sql); err == nil && len(rows) > 0 {
			totalTasks = jsonInt(rows[0]["cnt"])
		}
	}

	// Last trust event.
	lastUpdated := ""
	if events, err := queryTrustEvents(cfg.HistoryDB, role, 1); err == nil && len(events) > 0 {
		lastUpdated = jsonStr(events[0]["created_at"])
	}

	return TrustStatus{
		Agent:               role,
		Level:              level,
		ConsecutiveSuccess: consecutiveSuccess,
		PromoteReady:       promoteReady,
		NextLevel:          next,
		TotalTasks:         totalTasks,
		LastUpdated:        lastUpdated,
	}
}

// getAllTrustStatuses returns trust statuses for all configured roles.
func getAllTrustStatuses(cfg *Config) []TrustStatus {
	roles := make([]string, 0, len(cfg.Agents))
	for name := range cfg.Agents {
		roles = append(roles, name)
	}
	sort.Strings(roles)

	statuses := make([]TrustStatus, 0, len(roles))
	for _, role := range roles {
		statuses = append(statuses, getTrustStatus(cfg, role))
	}
	return statuses
}

// --- Trust-Aware Task Modification ---

// applyTrustToTask modifies a task based on the trust level of its agent.
// Returns the trust level applied and whether the task needs human confirmation.
func applyTrustToTask(cfg *Config, task *Task, agentName string) (level string, needsConfirm bool) {
	level = resolveTrustLevel(cfg, agentName)

	switch level {
	case TrustObserve:
		// Force read-only mode — no side effects.
		task.PermissionMode = "plan"
		return level, false // no confirmation needed, just observing

	case TrustSuggest:
		// Execute normally but output needs human approval.
		return level, true

	case TrustAuto:
		// Full autonomy.
		return level, false
	}

	return TrustAuto, false
}

// --- Trust Promotion Check ---

// checkTrustPromotion checks if an agent should be promoted after a successful task.
// Returns a notification message if promotion is suggested, or "" if not.
func checkTrustPromotion(ctx context.Context, cfg *Config, agentName string) string {
	if !cfg.Trust.Enabled || agentName == "" {
		return ""
	}

	level := resolveTrustLevel(cfg, agentName)
	next := nextTrustLevel(level)
	if next == "" {
		return "" // already at max
	}

	consecutiveSuccess := queryConsecutiveSuccess(cfg.HistoryDB, agentName)
	threshold := cfg.Trust.promoteThresholdOrDefault()

	if consecutiveSuccess < threshold {
		return ""
	}

	// Check if we already suggested promotion recently (within 24h).
	if events, err := queryTrustEvents(cfg.HistoryDB, agentName, 5); err == nil {
		for _, e := range events {
			if jsonStr(e["event_type"]) == "promote_suggest" {
				if t, err := time.Parse(time.RFC3339, jsonStr(e["created_at"])); err == nil {
					if time.Since(t) < 24*time.Hour {
						return "" // already suggested recently
					}
				}
			}
		}
	}

	if cfg.Trust.AutoPromote {
		// Auto-promote: update config and record.
		if err := updateAgentTrustLevel(cfg, agentName, next); err != nil {
			logWarnCtx(ctx, "auto-promote failed", "agent", agentName, "error", err)
			return ""
		}
		recordTrustEvent(cfg.HistoryDB, agentName, "promote", level, next, consecutiveSuccess,
			fmt.Sprintf("auto-promoted after %d consecutive successes", consecutiveSuccess))
		logInfoCtx(ctx, "trust auto-promoted", "agent", agentName, "from", level, "to", next)
		return fmt.Sprintf("Trust Auto-Promoted [%s]\n%s → %s (%d consecutive successes)",
			agentName, level, next, consecutiveSuccess)
	}

	// Suggest promotion.
	recordTrustEvent(cfg.HistoryDB, agentName, "promote_suggest", level, next, consecutiveSuccess,
		fmt.Sprintf("suggested after %d consecutive successes", consecutiveSuccess))

	return fmt.Sprintf("Trust Promotion Ready [%s]\n%s → %s available (%d consecutive successes)\nUse: tetora trust set %s %s",
		agentName, level, next, consecutiveSuccess, agentName, next)
}

// --- Config Update ---

// updateAgentTrustLevel updates the trust level for an agent in the live config.
// Note: This modifies the in-memory config only. To persist, call saveAgentTrustLevel.
func updateAgentTrustLevel(cfg *Config, agentName, newLevel string) error {
	if !isValidTrustLevel(newLevel) {
		return fmt.Errorf("invalid trust level %q (valid: %s)", newLevel, strings.Join(validTrustLevels, ", "))
	}
	rc, ok := cfg.Agents[agentName]
	if !ok {
		return fmt.Errorf("agent %q not found", agentName)
	}
	rc.TrustLevel = newLevel
	cfg.Agents[agentName] = rc
	return nil
}

// saveAgentTrustLevel persists a trust level change to config.json.
func saveAgentTrustLevel(configPath, agentName, newLevel string) error {
	return updateConfigField(configPath, func(raw map[string]any) {
		agents, ok := raw["agents"].(map[string]any)
		if !ok {
			// Fallback to old "roles" key.
			agents, ok = raw["roles"].(map[string]any)
			if !ok {
				return
			}
		}
		rc, ok := agents[agentName].(map[string]any)
		if !ok {
			return
		}
		rc["trustLevel"] = newLevel
	})
}

// updateConfigField reads config.json, applies a mutation, and writes it back.
func updateConfigField(configPath string, mutate func(raw map[string]any)) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	mutate(raw)

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return os.WriteFile(configPath, append(out, '\n'), 0o644)
}
