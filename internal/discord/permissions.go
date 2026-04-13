package discord

import (
	"encoding/json"
	"fmt"

	"tetora/internal/log"
)

// isAllowedChannel checks if a channel ID is in any allowed list.
// If no channel restrictions are set, all channels are allowed.
func (b *Bot) isAllowedChannel(chID string) bool {
	hasRestrictions := len(b.cfg.Discord.ChannelIDs) > 0 ||
		len(b.cfg.Discord.MentionChannelIDs) > 0 ||
		b.cfg.Discord.ChannelID != ""
	if !hasRestrictions {
		return true
	}
	return b.isDirectChannel(chID) || b.isMentionChannel(chID)
}

// isDirectChannel returns true if the channel is in channelIDs (no @ needed).
func (b *Bot) isDirectChannel(chID string) bool {
	for _, id := range b.cfg.Discord.ChannelIDs {
		if id == chID {
			return true
		}
	}
	return false
}

// isMentionChannel returns true if the channel requires @mention.
func (b *Bot) isMentionChannel(chID string) bool {
	if b.cfg.Discord.ChannelID != "" && b.cfg.Discord.ChannelID == chID {
		return true
	}
	for _, id := range b.cfg.Discord.MentionChannelIDs {
		if id == chID {
			return true
		}
	}
	return false
}

// resolveThreadParent returns the parent channel ID for a thread.
// Checks the cache first, then falls back to the Discord API.
func (b *Bot) resolveThreadParent(threadID string) string {
	if b.threadParents == nil {
		return ""
	}
	// Check cache (includes negative entries).
	if parentID, cached := b.threadParents.get(threadID); cached {
		return parentID
	}
	// Fallback: GET /channels/{threadID} and parse parent_id.
	body, err := b.api.Request("GET", fmt.Sprintf("/channels/%s", threadID), nil)
	if err != nil {
		log.Debug("resolveThreadParent API failed", "thread", threadID, "error", err)
		// Cache negative result to avoid repeated API calls on failure.
		b.threadParents.set(threadID, "")
		return ""
	}
	var ch struct {
		ParentID string `json:"parent_id"`
	}
	if err := json.Unmarshal(body, &ch); err != nil || ch.ParentID == "" {
		b.threadParents.set(threadID, "")
		return ""
	}
	b.threadParents.set(threadID, ch.ParentID)
	log.Debug("resolved thread parent", "thread", threadID, "parent", ch.ParentID)
	return ch.ParentID
}

// isAllowedChannelOrThread checks if a channel is allowed, including thread→parent resolution.
// If the channel itself isn't allowed but is a guild thread, resolves its parent and checks that.
func (b *Bot) isAllowedChannelOrThread(chID, guildID string) bool {
	if b.isAllowedChannel(chID) {
		return true
	}
	// Only attempt thread parent resolution for guild messages.
	if guildID == "" {
		return false
	}
	parentID := b.resolveThreadParent(chID)
	if parentID != "" {
		return b.isAllowedChannel(parentID)
	}
	return false
}
