package main

// --- P14.3: Lifecycle Reactions ---

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// --- Config ---

// DiscordReactionsConfig configures emoji lifecycle reactions for agent processing.
type DiscordReactionsConfig struct {
	Enabled bool              `json:"enabled,omitempty"`
	Emojis  map[string]string `json:"emojis,omitempty"` // phase -> emoji override
}

// --- Default Phase Emojis ---

// Lifecycle phases and their default emoji representations.
const (
	reactionPhaseQueued   = "queued"
	reactionPhaseThinking = "thinking"
	reactionPhaseTool     = "tool"
	reactionPhaseDone     = "done"
	reactionPhaseError    = "error"
)

// defaultReactionEmojis returns the default phase-to-emoji mapping.
func defaultReactionEmojis() map[string]string {
	return map[string]string{
		reactionPhaseQueued:   "\u23F3", // hourglass
		reactionPhaseThinking: "\U0001F914", // thinking face
		reactionPhaseTool:     "\U0001F527", // wrench
		reactionPhaseDone:     "\u2705", // white check mark
		reactionPhaseError:    "\u274C", // cross mark
	}
}

// --- Reaction Manager ---

// discordReactionManager manages lifecycle emoji reactions on Discord messages.
// It tracks the current phase per message and handles add/remove of reactions
// via the Discord REST API.
type discordReactionManager struct {
	bot          *DiscordBot
	defaultEmoji map[string]string // phase -> unicode emoji
	overrides    map[string]string // config overrides
	mu           sync.Mutex
	current      map[string]string // "channelID:messageID" -> current phase
}

// newDiscordReactionManager creates a new reaction manager with optional emoji overrides.
func newDiscordReactionManager(bot *DiscordBot, overrides map[string]string) *discordReactionManager {
	return &discordReactionManager{
		bot:          bot,
		defaultEmoji: defaultReactionEmojis(),
		overrides:    overrides,
		current:      make(map[string]string),
	}
}

// reactionKey generates a map key for tracking per-message phase state.
func reactionKey(channelID, messageID string) string {
	return channelID + ":" + messageID
}

// emojiForPhase returns the emoji string for a given phase, checking overrides first.
func (rm *discordReactionManager) emojiForPhase(phase string) string {
	if rm.overrides != nil {
		if emoji, ok := rm.overrides[phase]; ok && emoji != "" {
			return emoji
		}
	}
	if emoji, ok := rm.defaultEmoji[phase]; ok {
		return emoji
	}
	return ""
}

// setPhase transitions a message to a new lifecycle phase.
// It removes the previous phase's emoji and adds the new one.
func (rm *discordReactionManager) setPhase(channelID, messageID, phase string) {
	if channelID == "" || messageID == "" || phase == "" {
		return
	}

	newEmoji := rm.emojiForPhase(phase)
	if newEmoji == "" {
		logDebug("discord reactions: unknown phase", "phase", phase)
		return
	}

	key := reactionKey(channelID, messageID)

	rm.mu.Lock()
	prevPhase := rm.current[key]
	rm.current[key] = phase
	rm.mu.Unlock()

	// Remove previous phase emoji if different.
	if prevPhase != "" && prevPhase != phase {
		prevEmoji := rm.emojiForPhase(prevPhase)
		if prevEmoji != "" {
			rm.removeReaction(channelID, messageID, prevEmoji)
		}
	}

	// Add new phase emoji.
	rm.addReaction(channelID, messageID, newEmoji)

	logDebug("discord reaction phase set",
		"channel", channelID, "message", messageID,
		"phase", phase, "emoji", newEmoji,
		"prevPhase", prevPhase)
}

// clearPhase removes tracking for a message (called when processing is complete).
func (rm *discordReactionManager) clearPhase(channelID, messageID string) {
	key := reactionKey(channelID, messageID)
	rm.mu.Lock()
	delete(rm.current, key)
	rm.mu.Unlock()
}

// getCurrentPhase returns the current phase for a message, or empty string if not tracked.
func (rm *discordReactionManager) getCurrentPhase(channelID, messageID string) string {
	key := reactionKey(channelID, messageID)
	rm.mu.Lock()
	defer rm.mu.Unlock()
	return rm.current[key]
}

// --- Discord API Calls ---

// addReaction adds an emoji reaction to a message via Discord REST API.
// PUT /channels/{channelID}/messages/{messageID}/reactions/{emoji}/@me
func (rm *discordReactionManager) addReaction(channelID, messageID, emoji string) {
	if rm.bot == nil {
		return
	}
	encoded := url.PathEscape(emoji)
	path := fmt.Sprintf("/channels/%s/messages/%s/reactions/%s/@me", channelID, messageID, encoded)
	rm.bot.discordRequest("PUT", path, nil)
}

// removeReaction removes the bot's own emoji reaction from a message via Discord REST API.
// DELETE /channels/{channelID}/messages/{messageID}/reactions/{emoji}/@me
func (rm *discordReactionManager) removeReaction(channelID, messageID, emoji string) {
	if rm.bot == nil {
		return
	}
	encoded := url.PathEscape(emoji)
	path := fmt.Sprintf("/channels/%s/messages/%s/reactions/%s/@me", channelID, messageID, encoded)
	rm.bot.discordRequest("DELETE", path, nil)
}

// --- Generic Discord API Request Helper ---

// discordRequest performs a generic HTTP request to the Discord API.
// Supports PUT, DELETE, PATCH, GET, POST methods.
func (db *DiscordBot) discordRequest(method, path string, payload any) (int, []byte) {
	if db == nil || db.client == nil {
		return 0, nil
	}
	var bodyStr string
	if payload != nil {
		body, _ := json.Marshal(payload)
		bodyStr = string(body)
	}

	var reqBody *strings.Reader
	if bodyStr != "" {
		reqBody = strings.NewReader(bodyStr)
	} else {
		reqBody = strings.NewReader("")
	}

	req, err := http.NewRequest(method, discordAPIBase+path, reqBody)
	if err != nil {
		logError("discord api request error", "method", method, "path", path, "error", err)
		return 0, nil
	}
	if bodyStr != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bot "+db.cfg.Discord.BotToken)

	resp, err := db.client.Do(req)
	if err != nil {
		logError("discord api send failed", "method", method, "path", path, "error", err)
		return 0, nil
	}
	defer resp.Body.Close()

	var respBody []byte
	if resp.Body != nil {
		respBody = make([]byte, 0, 1024)
		buf := make([]byte, 1024)
		for {
			n, readErr := resp.Body.Read(buf)
			if n > 0 {
				respBody = append(respBody, buf[:n]...)
			}
			if readErr != nil {
				break
			}
			if len(respBody) > 8192 {
				break
			}
		}
	}

	if resp.StatusCode >= 400 {
		logWarn("discord api error", "method", method, "path", path,
			"status", resp.StatusCode, "body", string(respBody))
	}

	return resp.StatusCode, respBody
}

// --- Lifecycle Integration Helpers ---

// reactQueued adds the queued reaction to a message.
func (rm *discordReactionManager) reactQueued(channelID, messageID string) {
	rm.setPhase(channelID, messageID, reactionPhaseQueued)
}

// reactThinking transitions to the thinking phase.
func (rm *discordReactionManager) reactThinking(channelID, messageID string) {
	rm.setPhase(channelID, messageID, reactionPhaseThinking)
}

// reactTool transitions to the tool execution phase.
func (rm *discordReactionManager) reactTool(channelID, messageID string) {
	rm.setPhase(channelID, messageID, reactionPhaseTool)
}

// reactDone transitions to the done phase (success).
func (rm *discordReactionManager) reactDone(channelID, messageID string) {
	rm.setPhase(channelID, messageID, reactionPhaseDone)
	rm.clearPhase(channelID, messageID)
}

// reactError transitions to the error phase (failure).
func (rm *discordReactionManager) reactError(channelID, messageID string) {
	rm.setPhase(channelID, messageID, reactionPhaseError)
	rm.clearPhase(channelID, messageID)
}

// validPhases returns all valid lifecycle phase names.
func validReactionPhases() []string {
	return []string{
		reactionPhaseQueued,
		reactionPhaseThinking,
		reactionPhaseTool,
		reactionPhaseDone,
		reactionPhaseError,
	}
}
