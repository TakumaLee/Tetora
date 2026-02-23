package main

// --- P14.4: Discord Forum Task Board ---

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// --- Config ---

// DiscordForumBoardConfig configures a Discord Forum channel as a Kanban task board.
type DiscordForumBoardConfig struct {
	Enabled        bool              `json:"enabled,omitempty"`
	ForumChannelID string            `json:"forumChannelId,omitempty"` // Discord Forum channel ID
	Tags           map[string]string `json:"tags,omitempty"`           // status -> tag_id mapping
}

// --- Status Constants ---

const (
	forumStatusBacklog = "backlog"
	forumStatusTodo    = "todo"
	forumStatusDoing   = "doing"
	forumStatusReview  = "review"
	forumStatusDone    = "done"
)

// validForumStatuses returns all recognized forum board statuses.
func validForumStatuses() []string {
	return []string{
		forumStatusBacklog,
		forumStatusTodo,
		forumStatusDoing,
		forumStatusReview,
		forumStatusDone,
	}
}

// isValidForumStatus checks if a status string is a recognized forum board status.
func isValidForumStatus(status string) bool {
	for _, s := range validForumStatuses() {
		if s == status {
			return true
		}
	}
	return false
}

// --- Forum Board ---

// discordForumBoard manages a Discord Forum channel as a task board,
// providing thread creation with status tags, status transitions, and agent assignment.
type discordForumBoard struct {
	bot *DiscordBot
	cfg DiscordForumBoardConfig
}

// newDiscordForumBoard creates a new forum board manager.
func newDiscordForumBoard(bot *DiscordBot, cfg DiscordForumBoardConfig) *discordForumBoard {
	return &discordForumBoard{
		bot: bot,
		cfg: cfg,
	}
}

// --- Thread Creation ---

// createThread creates a new forum thread with an initial status tag and body message.
// Returns the created thread ID and any error.
func (fb *discordForumBoard) createThread(title, body, status string) (string, error) {
	if fb.cfg.ForumChannelID == "" {
		return "", fmt.Errorf("forum channel ID not configured")
	}
	if title == "" {
		return "", fmt.Errorf("thread title is required")
	}
	if status == "" {
		status = forumStatusBacklog
	}
	if !isValidForumStatus(status) {
		return "", fmt.Errorf("invalid status %q, valid: %s", status, strings.Join(validForumStatuses(), ", "))
	}

	// Truncate title to Discord's limit (100 chars for thread names).
	if len(title) > 100 {
		title = title[:97] + "..."
	}

	// Build applied_tags array.
	appliedTags := fb.tagsForStatus(status)

	// Build the request payload for forum thread creation.
	// POST /channels/{forumChannelId}/threads
	payload := map[string]any{
		"name": title,
		"message": map[string]any{
			"content": fb.formatThreadBody(body, status),
		},
	}
	if len(appliedTags) > 0 {
		payload["applied_tags"] = appliedTags
	}

	path := fmt.Sprintf("/channels/%s/threads", fb.cfg.ForumChannelID)
	statusCode, respBody := fb.bot.discordRequest("POST", path, payload)

	if statusCode == 0 && respBody == nil {
		return "", fmt.Errorf("discord API unavailable (bot not connected)")
	}
	if statusCode < 200 || statusCode >= 300 {
		return "", fmt.Errorf("create forum thread failed: status %d, body: %s", statusCode, string(respBody))
	}

	// Parse response to get thread ID.
	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parse thread response: %w", err)
	}
	if result.ID == "" {
		return "", fmt.Errorf("created thread has no ID")
	}

	logInfo("discord forum thread created",
		"thread", result.ID, "title", title, "status", status)

	return result.ID, nil
}

// formatThreadBody formats the initial message body for a forum thread.
func (fb *discordForumBoard) formatThreadBody(body, status string) string {
	if body == "" {
		body = "(No description)"
	}
	return fmt.Sprintf("**Status:** `%s`\n\n%s\n\n_Created via Tetora at %s_",
		status, body, time.Now().Format(time.RFC3339))
}

// --- Status Management ---

// setStatus updates the status tag on a forum thread.
// It replaces any existing status tags with the new status tag.
func (fb *discordForumBoard) setStatus(threadID, status string) error {
	if threadID == "" {
		return fmt.Errorf("thread ID is required")
	}
	if !isValidForumStatus(status) {
		return fmt.Errorf("invalid status %q, valid: %s", status, strings.Join(validForumStatuses(), ", "))
	}

	appliedTags := fb.tagsForStatus(status)

	// PATCH /channels/{threadId} with applied_tags.
	payload := map[string]any{
		"applied_tags": appliedTags,
	}

	path := fmt.Sprintf("/channels/%s", threadID)
	statusCode, respBody := fb.bot.discordRequest("PATCH", path, payload)

	if statusCode == 0 && respBody == nil {
		return fmt.Errorf("discord API unavailable (bot not connected)")
	}
	if statusCode < 200 || statusCode >= 300 {
		return fmt.Errorf("set forum status failed: status %d, body: %s", statusCode, string(respBody))
	}

	logInfo("discord forum status updated",
		"thread", threadID, "status", status)

	return nil
}

// tagsForStatus returns the applied_tags array for a given status.
// Returns an array with the tag ID if configured, empty array otherwise.
func (fb *discordForumBoard) tagsForStatus(status string) []string {
	if fb.cfg.Tags == nil {
		return nil
	}
	tagID, ok := fb.cfg.Tags[status]
	if !ok || tagID == "" {
		return nil
	}
	return []string{tagID}
}

// --- Agent Assignment ---

// handleAssign assigns an agent role to a forum thread.
// It binds the thread to the role (using P14.2 threadBindingStore) and sets status to "doing".
func (fb *discordForumBoard) handleAssign(threadID, guildID, role string) error {
	if threadID == "" {
		return fmt.Errorf("thread ID is required")
	}
	if role == "" {
		return fmt.Errorf("role is required")
	}

	// Validate role exists in config.
	if fb.bot == nil || fb.bot.cfg == nil || fb.bot.cfg.Roles == nil {
		return fmt.Errorf("unknown role %q (no roles configured)", role)
	}
	if _, ok := fb.bot.cfg.Roles[role]; !ok {
		return fmt.Errorf("unknown role %q", role)
	}

	// Bind thread to agent via P14.2 thread binding store.
	if fb.bot.threads != nil && fb.bot.cfg.Discord.ThreadBindings.Enabled {
		ttl := fb.bot.cfg.Discord.ThreadBindings.threadBindingsTTL()
		sessionID := fb.bot.threads.bind(guildID, threadID, role, ttl)
		logInfo("discord forum thread assigned",
			"thread", threadID, "role", role, "session", sessionID)
	}

	// Set status to "doing".
	if err := fb.setStatus(threadID, forumStatusDoing); err != nil {
		logWarn("discord forum auto-status failed", "thread", threadID, "error", err)
		// Non-fatal: assignment succeeded even if tag update fails.
	}

	return nil
}

// --- Completion Hook ---

// handleCompletion updates a forum thread status when a task completes.
// On success: sets status to "done". On error: keeps current status.
func (fb *discordForumBoard) handleCompletion(threadID string, success bool) {
	if threadID == "" {
		return
	}

	newStatus := forumStatusDone
	if !success {
		// On error, move to review instead of done so it gets attention.
		newStatus = forumStatusReview
	}

	if err := fb.setStatus(threadID, newStatus); err != nil {
		logWarn("discord forum completion status failed",
			"thread", threadID, "success", success, "error", err)
	}
}

// --- Command Handlers ---

// handleAssignCommand processes the /assign <role> command in a forum thread.
// Returns a user-facing message string.
func (fb *discordForumBoard) handleAssignCommand(channelID, guildID, args string) string {
	role := strings.TrimSpace(strings.ToLower(args))
	if role == "" {
		available := fb.bot.availableRoleNames()
		return fmt.Sprintf("Usage: `/assign <role>` -- Available roles: %s", strings.Join(available, ", "))
	}

	if err := fb.handleAssign(channelID, guildID, role); err != nil {
		return fmt.Sprintf("Failed to assign: %v", err)
	}

	return fmt.Sprintf("Thread assigned to agent **%s** and status set to `doing`.", role)
}

// handleStatusCommand processes the /status <status> command in a forum thread.
// Returns a user-facing message string.
func (fb *discordForumBoard) handleStatusCommand(channelID, args string) string {
	status := strings.TrimSpace(strings.ToLower(args))
	if status == "" {
		return fmt.Sprintf("Usage: `/status <status>` -- Valid statuses: %s", strings.Join(validForumStatuses(), ", "))
	}

	if !isValidForumStatus(status) {
		return fmt.Sprintf("Invalid status `%s`. Valid: %s", status, strings.Join(validForumStatuses(), ", "))
	}

	if err := fb.setStatus(channelID, status); err != nil {
		return fmt.Sprintf("Failed to update status: %v", err)
	}

	return fmt.Sprintf("Thread status updated to `%s`.", status)
}

// --- Forum Channel Detection ---

// isForumThread checks if a thread/channel is within the configured forum channel.
// This is a heuristic: we check if the thread has been created via the forum board
// or if commands are used within forum threads. In practice, Discord forum threads
// have channel_type=11 (public thread) with a parent_id pointing to the forum channel.
func (fb *discordForumBoard) isConfigured() bool {
	return fb.cfg.Enabled && fb.cfg.ForumChannelID != ""
}

// --- Config Validation ---

// validateForumBoardConfig checks the forum board configuration for common issues.
func validateForumBoardConfig(cfg DiscordForumBoardConfig) []string {
	var warnings []string

	if cfg.Enabled && cfg.ForumChannelID == "" {
		warnings = append(warnings, "discord.forumBoard.enabled=true but forumChannelId is empty")
	}

	if cfg.Tags != nil {
		for status, tagID := range cfg.Tags {
			if !isValidForumStatus(status) {
				warnings = append(warnings, fmt.Sprintf("discord.forumBoard.tags: unknown status %q", status))
			}
			if tagID == "" {
				warnings = append(warnings, fmt.Sprintf("discord.forumBoard.tags.%s: empty tag ID", status))
			}
		}
	}

	return warnings
}
