package main

import (
	"encoding/json"
	"fmt"

	"tetora/internal/log"
)

// isAllowedChannel checks if a channel ID is in any allowed list.
// If no channel restrictions are set, all channels are allowed.
func (db *DiscordBot) isAllowedChannel(chID string) bool {
	hasRestrictions := len(db.cfg.Discord.ChannelIDs) > 0 ||
		len(db.cfg.Discord.MentionChannelIDs) > 0 ||
		db.cfg.Discord.ChannelID != ""
	if !hasRestrictions {
		return true
	}
	return db.isDirectChannel(chID) || db.isMentionChannel(chID)
}

// isDirectChannel returns true if the channel is in channelIDs (no @ needed).
func (db *DiscordBot) isDirectChannel(chID string) bool {
	for _, id := range db.cfg.Discord.ChannelIDs {
		if id == chID {
			return true
		}
	}
	return false
}

// isMentionChannel returns true if the channel requires @mention.
func (db *DiscordBot) isMentionChannel(chID string) bool {
	if db.cfg.Discord.ChannelID != "" && db.cfg.Discord.ChannelID == chID {
		return true
	}
	for _, id := range db.cfg.Discord.MentionChannelIDs {
		if id == chID {
			return true
		}
	}
	return false
}

// resolveThreadParent returns the parent channel ID for a thread.
// Checks the cache first, then falls back to the Discord API.
func (db *DiscordBot) resolveThreadParent(threadID string) string {
	if db.threadParents == nil {
		return ""
	}
	// Check cache (includes negative entries).
	if parentID, cached := db.threadParents.get(threadID); cached {
		return parentID
	}
	// Fallback: GET /channels/{threadID} and parse parent_id.
	body, err := db.api.Request("GET", fmt.Sprintf("/channels/%s", threadID), nil)
	if err != nil {
		log.Debug("resolveThreadParent API failed", "thread", threadID, "error", err)
		// Cache negative result to avoid repeated API calls on failure.
		db.threadParents.set(threadID, "")
		return ""
	}
	var ch struct {
		ParentID string `json:"parent_id"`
	}
	if err := json.Unmarshal(body, &ch); err != nil || ch.ParentID == "" {
		db.threadParents.set(threadID, "")
		return ""
	}
	db.threadParents.set(threadID, ch.ParentID)
	log.Debug("resolved thread parent", "thread", threadID, "parent", ch.ParentID)
	return ch.ParentID
}

// isAllowedChannelOrThread checks if a channel is allowed, including thread→parent resolution.
// If the channel itself isn't allowed but is a guild thread, resolves its parent and checks that.
func (db *DiscordBot) isAllowedChannelOrThread(chID, guildID string) bool {
	if db.isAllowedChannel(chID) {
		return true
	}
	// Only attempt thread parent resolution for guild messages.
	if guildID == "" {
		return false
	}
	parentID := db.resolveThreadParent(chID)
	if parentID != "" {
		return db.isAllowedChannel(parentID)
	}
	return false
}
