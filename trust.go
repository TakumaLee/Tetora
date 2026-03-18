package main

import (
	"context"

	"tetora/internal/config"
	"tetora/internal/trust"
)

// --- Trust Level Constants ---

const (
	TrustObserve = trust.Observe
	TrustSuggest = trust.Suggest
	TrustAuto    = trust.Auto
)

// validTrustLevels is the ordered set of trust levels (low → high).
var validTrustLevels = trust.ValidLevels

// TrustStatus is the trust state for a single agent.
type TrustStatus = trust.Status

// isValidTrustLevel checks if a string is a valid trust level.
func isValidTrustLevel(level string) bool { return trust.IsValidLevel(level) }

// trustLevelIndex returns the ordinal index (0=observe, 1=suggest, 2=auto).
func trustLevelIndex(level string) int { return trust.LevelIndex(level) }

// nextTrustLevel returns the next higher trust level, or "" if already at max.
func nextTrustLevel(current string) string { return trust.NextLevel(current) }

// initTrustDB creates the trust_events table.
func initTrustDB(dbPath string) { trust.InitDB(dbPath) }

// resolveTrustLevel returns the effective trust level for an agent.
func resolveTrustLevel(cfg *config.Config, agentName string) string {
	return trust.ResolveLevel(cfg, agentName)
}

// queryConsecutiveSuccess counts consecutive successful tasks for an agent.
func queryConsecutiveSuccess(dbPath, role string) int {
	return trust.QueryConsecutiveSuccess(dbPath, role)
}

// recordTrustEvent stores a trust event in the database.
func recordTrustEvent(dbPath, role, eventType, fromLevel, toLevel string, consecutiveSuccess int, note string) {
	trust.RecordEvent(dbPath, role, eventType, fromLevel, toLevel, consecutiveSuccess, note)
}

// queryTrustEvents returns recent trust events for a role.
func queryTrustEvents(dbPath, role string, limit int) ([]map[string]any, error) {
	return trust.QueryEvents(dbPath, role, limit)
}

// getTrustStatus returns the trust status for a single role.
func getTrustStatus(cfg *Config, role string) TrustStatus { return trust.GetStatus(cfg, role) }

// getAllTrustStatuses returns trust statuses for all configured roles.
func getAllTrustStatuses(cfg *Config) []TrustStatus { return trust.GetAllStatuses(cfg) }

// applyTrustToTask modifies a task based on the trust level of its agent.
// Returns the trust level applied and whether the task needs human confirmation.
func applyTrustToTask(cfg *Config, task *Task, agentName string) (level string, needsConfirm bool) {
	return trust.ApplyToTask(cfg, &task.PermissionMode, agentName)
}

// checkTrustPromotion checks if an agent should be promoted after a successful task.
// Returns a notification message if promotion is suggested, or "" if not.
func checkTrustPromotion(ctx context.Context, cfg *Config, agentName string) string {
	return trust.CheckPromotion(ctx, cfg, agentName)
}

// updateAgentTrustLevel updates the trust level for an agent in the live config.
func updateAgentTrustLevel(cfg *Config, agentName, newLevel string) error {
	return trust.UpdateAgentLevel(cfg, agentName, newLevel)
}

// saveAgentTrustLevel persists a trust level change to config.json.
func saveAgentTrustLevel(configPath, agentName, newLevel string) error {
	return trust.SaveAgentLevel(configPath, agentName, newLevel)
}

// updateConfigField reads config.json, applies a mutation, and writes it back.
// Kept here for use by non-trust callers in http.go.
func updateConfigField(configPath string, mutate func(raw map[string]any)) error {
	return trust.UpdateConfigField(configPath, mutate)
}
