package trust

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"tetora/internal/config"
	"tetora/internal/db"
	"tetora/internal/log"
)

// --- Trust Level Constants ---

const (
	Observe = "observe" // report only, no side effects (forces plan mode)
	Suggest = "suggest" // execute + present for human confirmation
	Auto    = "auto"    // fully autonomous execution
)

// ValidLevels is the ordered set of trust levels (low → high).
var ValidLevels = []string{Observe, Suggest, Auto}

// IsValidLevel checks if a string is a valid trust level.
func IsValidLevel(level string) bool {
	for _, v := range ValidLevels {
		if v == level {
			return true
		}
	}
	return false
}

// LevelIndex returns the ordinal index (0=observe, 1=suggest, 2=auto).
func LevelIndex(level string) int {
	for i, v := range ValidLevels {
		if v == level {
			return i
		}
	}
	return -1
}

// NextLevel returns the next higher trust level, or "" if already at max.
func NextLevel(current string) string {
	idx := LevelIndex(current)
	if idx < 0 || idx >= len(ValidLevels)-1 {
		return ""
	}
	return ValidLevels[idx+1]
}

// --- Trust Status ---

// Status holds the trust state for a single agent.
type Status struct {
	Agent              string `json:"agent"`
	Level              string `json:"level"`
	ConsecutiveSuccess int    `json:"consecutiveSuccess"`
	PromoteReady       bool   `json:"promoteReady"`        // true if enough consecutive successes for promotion
	NextLevel          string `json:"nextLevel,omitempty"` // next level to promote to
	TotalTasks         int    `json:"totalTasks"`
	LastUpdated        string `json:"lastUpdated,omitempty"`
}

// --- DB Init ---

// InitDB creates the trust_events table in the given SQLite database.
func InitDB(dbPath string) {
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
		log.Warn("init trust_events table failed", "error", fmt.Sprintf("%s: %s", err, out))
	}
}

// --- Trust Level Resolution ---

// ResolveLevel returns the effective trust level for an agent.
// Priority: agent config → default "auto".
func ResolveLevel(cfg *config.Config, agentName string) string {
	if !cfg.Trust.Enabled {
		return Auto
	}
	if agentName == "" {
		return Auto
	}
	if rc, ok := cfg.Agents[agentName]; ok && rc.TrustLevel != "" {
		if IsValidLevel(rc.TrustLevel) {
			return rc.TrustLevel
		}
	}
	return Auto
}

// --- Consecutive Success Tracking ---

// QueryConsecutiveSuccess counts consecutive successful tasks for an agent
// (most recent first, stopping at the first non-success).
func QueryConsecutiveSuccess(dbPath, role string) int {
	if dbPath == "" || role == "" {
		return 0
	}

	sql := fmt.Sprintf(
		`SELECT status FROM job_runs
		 WHERE agent = '%s'
		 ORDER BY id DESC LIMIT 50`,
		db.Escape(role))

	rows, err := db.Query(dbPath, sql)
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

// RecordEvent stores a trust event in the database.
func RecordEvent(dbPath, role, eventType, fromLevel, toLevel string, consecutiveSuccess int, note string) {
	if dbPath == "" {
		return
	}

	sql := fmt.Sprintf(
		`INSERT INTO trust_events (agent, event_type, from_level, to_level, consecutive_success, created_at, note)
		 VALUES ('%s', '%s', '%s', '%s', %d, '%s', '%s')`,
		db.Escape(role),
		db.Escape(eventType),
		db.Escape(fromLevel),
		db.Escape(toLevel),
		consecutiveSuccess,
		time.Now().Format(time.RFC3339),
		db.Escape(note))

	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Warn("record trust event failed", "error", fmt.Sprintf("%s: %s", err, out))
	}
}

// QueryEvents returns recent trust events for a role.
func QueryEvents(dbPath, role string, limit int) ([]map[string]any, error) {
	if dbPath == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}

	where := ""
	if role != "" {
		where = fmt.Sprintf("WHERE agent = '%s'", db.Escape(role))
	}

	sql := fmt.Sprintf(
		`SELECT agent, event_type, from_level, to_level, consecutive_success, created_at, note
		 FROM trust_events %s ORDER BY id DESC LIMIT %d`, where, limit)

	return db.Query(dbPath, sql)
}

// --- Trust Status Queries ---

// GetStatus returns the trust status for a single role.
func GetStatus(cfg *config.Config, role string) Status {
	level := ResolveLevel(cfg, role)
	consecutiveSuccess := QueryConsecutiveSuccess(cfg.HistoryDB, role)
	threshold := cfg.Trust.PromoteThresholdOrDefault()
	next := NextLevel(level)
	promoteReady := next != "" && consecutiveSuccess >= threshold

	// Count total tasks.
	totalTasks := 0
	if cfg.HistoryDB != "" {
		sql := fmt.Sprintf(`SELECT COUNT(*) as cnt FROM job_runs WHERE agent = '%s'`, db.Escape(role))
		if rows, err := db.Query(cfg.HistoryDB, sql); err == nil && len(rows) > 0 {
			totalTasks = jsonInt(rows[0]["cnt"])
		}
	}

	// Last trust event.
	lastUpdated := ""
	if events, err := QueryEvents(cfg.HistoryDB, role, 1); err == nil && len(events) > 0 {
		lastUpdated = jsonStr(events[0]["created_at"])
	}

	return Status{
		Agent:              role,
		Level:              level,
		ConsecutiveSuccess: consecutiveSuccess,
		PromoteReady:       promoteReady,
		NextLevel:          next,
		TotalTasks:         totalTasks,
		LastUpdated:        lastUpdated,
	}
}

// GetAllStatuses returns trust statuses for all configured roles.
func GetAllStatuses(cfg *config.Config) []Status {
	roles := make([]string, 0, len(cfg.Agents))
	for name := range cfg.Agents {
		roles = append(roles, name)
	}
	sort.Strings(roles)

	statuses := make([]Status, 0, len(roles))
	for _, role := range roles {
		statuses = append(statuses, GetStatus(cfg, role))
	}
	return statuses
}

// --- Trust-Aware Task Modification ---

// ApplyToTask modifies a task's PermissionMode based on the trust level of its agent.
// Returns the trust level applied and whether the task needs human confirmation.
// permissionMode is modified in place via the pointer.
func ApplyToTask(cfg *config.Config, permissionMode *string, agentName string) (level string, needsConfirm bool) {
	level = ResolveLevel(cfg, agentName)

	switch level {
	case Observe:
		// Force read-only mode — no side effects.
		*permissionMode = "plan"
		return level, false // no confirmation needed, just observing

	case Suggest:
		// Execute normally but output needs human approval.
		return level, true

	case Auto:
		// Full autonomy.
		return level, false
	}

	return Auto, false
}

// --- Trust Promotion Check ---

// CheckPromotion checks if an agent should be promoted after a successful task.
// Returns a notification message if promotion is suggested, or "" if not.
func CheckPromotion(ctx context.Context, cfg *config.Config, agentName string) string {
	if !cfg.Trust.Enabled || agentName == "" {
		return ""
	}

	level := ResolveLevel(cfg, agentName)
	next := NextLevel(level)
	if next == "" {
		return "" // already at max
	}

	consecutiveSuccess := QueryConsecutiveSuccess(cfg.HistoryDB, agentName)
	threshold := cfg.Trust.PromoteThresholdOrDefault()

	if consecutiveSuccess < threshold {
		return ""
	}

	// Check if we already suggested promotion recently (within 24h).
	if events, err := QueryEvents(cfg.HistoryDB, agentName, 5); err == nil {
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
		if err := UpdateAgentLevel(cfg, agentName, next); err != nil {
			log.WarnCtx(ctx, "auto-promote failed", "agent", agentName, "error", err)
			return ""
		}
		RecordEvent(cfg.HistoryDB, agentName, "promote", level, next, consecutiveSuccess,
			fmt.Sprintf("auto-promoted after %d consecutive successes", consecutiveSuccess))
		log.InfoCtx(ctx, "trust auto-promoted", "agent", agentName, "from", level, "to", next)
		return fmt.Sprintf("Trust Auto-Promoted [%s]\n%s → %s (%d consecutive successes)",
			agentName, level, next, consecutiveSuccess)
	}

	// Suggest promotion.
	RecordEvent(cfg.HistoryDB, agentName, "promote_suggest", level, next, consecutiveSuccess,
		fmt.Sprintf("suggested after %d consecutive successes", consecutiveSuccess))

	return fmt.Sprintf("Trust Promotion Ready [%s]\n%s → %s available (%d consecutive successes)\nUse: tetora trust set %s %s",
		agentName, level, next, consecutiveSuccess, agentName, next)
}

// --- Config Update ---

// UpdateAgentLevel updates the trust level for an agent in the live config.
// Note: This modifies the in-memory config only. To persist, call SaveAgentLevel.
func UpdateAgentLevel(cfg *config.Config, agentName, newLevel string) error {
	if !IsValidLevel(newLevel) {
		return fmt.Errorf("invalid trust level %q (valid: %s)", newLevel, strings.Join(ValidLevels, ", "))
	}
	rc, ok := cfg.Agents[agentName]
	if !ok {
		return fmt.Errorf("agent %q not found", agentName)
	}
	rc.TrustLevel = newLevel
	cfg.Agents[agentName] = rc
	return nil
}

// SaveAgentLevel persists a trust level change to config.json.
func SaveAgentLevel(configPath, agentName, newLevel string) error {
	return UpdateConfigField(configPath, func(raw map[string]any) {
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

// UpdateConfigField reads config.json, applies a mutation, and writes it back.
func UpdateConfigField(configPath string, mutate func(raw map[string]any)) error {
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
	return os.WriteFile(configPath, append(out, '\n'), 0o600)
}

// --- JSON helpers (private) ---

func jsonStr(v any) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case json.Number:
		return val.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}

func jsonInt(v any) int {
	if v == nil {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return int(val)
	case json.Number:
		i, _ := val.Int64()
		return int(i)
	case string:
		i, _ := strconv.Atoi(val)
		return i
	default:
		return 0
	}
}
