package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"tetora/internal/log"
)

// formatDurationMs converts milliseconds to a human-readable string (e.g. "11.9s", "320ms").
func formatDurationMs(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	s := ms / 1000
	if s < 60 {
		return fmt.Sprintf("%.1fs", float64(ms)/1000)
	}
	h := s / 3600
	m := (s % 3600) / 60
	sec := s % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm %ds", h, m, sec)
	}
	return fmt.Sprintf("%dm %ds", m, sec)
}

// formatTokenField renders the "今日 Token" embed value.
// When limit > 0 it shows a progress bar; otherwise plain numbers.
func formatTokenField(in, out, limit int) string {
	counts := fmt.Sprintf("%s in / %s out", formatTokenCount(in), formatTokenCount(out))
	if limit <= 0 {
		return counts
	}
	total := in + out
	pct := float64(total) / float64(limit) * 100
	if pct > 100 {
		pct = 100
	}
	const barWidth = 10
	filled := int(pct / 100 * barWidth)
	bar := ""
	for i := 0; i < barWidth; i++ {
		if i < filled {
			bar += "▓"
		} else {
			bar += "░"
		}
	}
	return fmt.Sprintf("%s %.0f%%\n%s", bar, pct, counts)
}

// formatTokenCount formats a token count with K/M suffix for readability.
func formatTokenCount(n int) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

// --- REST API Helpers ---

func (db *DiscordBot) sendMessage(channelID, content string) {
	db.api.SendMessage(channelID, content)
}

func (db *DiscordBot) sendEmbed(channelID string, embed discordEmbed) {
	db.api.SendEmbed(channelID, embed)
}

func (db *DiscordBot) sendEmbedReply(channelID, replyToID string, embed discordEmbed) {
	db.api.SendEmbedReply(channelID, replyToID, embed)
}

func (db *DiscordBot) sendTyping(channelID string) {
	db.api.SendTyping(channelID)
}

// --- P27.3: Discord Channel Notifier ---

type discordChannelNotifier struct {
	bot       *DiscordBot
	channelID string
}

func (n *discordChannelNotifier) SendTyping(ctx context.Context) error {
	n.bot.sendTyping(n.channelID)
	return nil
}

func (n *discordChannelNotifier) SendStatus(ctx context.Context, msg string) error {
	n.bot.sendTyping(n.channelID)
	return nil
}

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

func (db *DiscordBot) notifyChannelID() string {
	if len(db.cfg.Discord.ChannelIDs) > 0 {
		return db.cfg.Discord.ChannelIDs[0]
	}
	return db.cfg.Discord.ChannelID
}

func (db *DiscordBot) sendNotify(text string) {
	ch := db.notifyChannelID()
	if ch == "" {
		return
	}
	db.sendMessage(ch, text)
}

// discordPost delegates to the api client (kept for callers in other files).
func (db *DiscordBot) discordPost(path string, payload any) {
	db.api.Post(path, payload)
}

// discordRequestWithResponse delegates to the api client (kept for callers in other files).
func (db *DiscordBot) discordRequestWithResponse(method, path string, payload any) ([]byte, error) {
	return db.api.Request(method, path, payload)
}

// sendMessageReturningID sends a message and returns the message ID.
func (db *DiscordBot) sendMessageReturningID(channelID, content string) (string, error) {
	return db.api.SendMessageReturningID(channelID, content)
}

// editMessage edits an existing Discord message.
func (db *DiscordBot) editMessage(channelID, messageID, content string) error {
	return db.api.EditMessage(channelID, messageID, content)
}

// editMessageWithComponents edits an existing Discord message, replacing content and components.
func (db *DiscordBot) editMessageWithComponents(channelID, messageID, content string, components []discordComponent) error {
	return db.api.EditMessageWithComponents(channelID, messageID, content, components)
}

// deleteMessage deletes a Discord message.
func (db *DiscordBot) deleteMessage(channelID, messageID string) {
	db.api.DeleteMessage(channelID, messageID)
}

// --- P14.1: Discord Components v2 ---

// sendMessageWithComponents sends a message with interactive components (buttons, selects, etc.).
func (db *DiscordBot) sendMessageWithComponents(channelID, content string, components []discordComponent) {
	db.api.SendMessageWithComponents(channelID, content, components)
}

// sendMessageWithComponentsReturningID sends a message with components and returns the message ID.
func (db *DiscordBot) sendMessageWithComponentsReturningID(channelID, content string, components []discordComponent) (string, error) {
	return db.api.SendMessageWithComponentsReturningID(channelID, content, components)
}

// sendEmbedWithComponents sends an embed message with interactive components.
func (db *DiscordBot) sendEmbedWithComponents(channelID string, embed discordEmbed, components []discordComponent) {
	db.api.SendEmbedWithComponents(channelID, embed, components)
}

// --- P28.0: Discord Approval Gate ---

// discordApprovalGate implements ApprovalGate via Discord button components.
type discordApprovalGate struct {
	bot          *DiscordBot
	channelID    string
	mu           sync.Mutex
	pending      map[string]chan bool
	autoApproved map[string]bool // tool name → always approved
}

func newDiscordApprovalGate(bot *DiscordBot, channelID string) *discordApprovalGate {
	g := &discordApprovalGate{
		bot:          bot,
		channelID:    channelID,
		pending:      make(map[string]chan bool),
		autoApproved: make(map[string]bool),
	}
	// Copy config-level auto-approve tools.
	for _, tool := range bot.cfg.ApprovalGates.AutoApproveTools {
		g.autoApproved[tool] = true
	}
	return g
}

func (g *discordApprovalGate) AutoApprove(toolName string) {
	g.mu.Lock()
	g.autoApproved[toolName] = true
	g.mu.Unlock()
}

func (g *discordApprovalGate) IsAutoApproved(toolName string) bool {
	g.mu.Lock()
	ok := g.autoApproved[toolName]
	g.mu.Unlock()
	return ok
}

func (g *discordApprovalGate) RequestApproval(ctx context.Context, req ApprovalRequest) (bool, error) {
	ch := make(chan bool, 1)
	g.mu.Lock()
	g.pending[req.ID] = ch
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		delete(g.pending, req.ID)
		g.mu.Unlock()
	}()

	text := fmt.Sprintf("**Approval needed**\n\nTool: `%s`\n%s", req.Tool, req.Summary)
	components := []discordComponent{{
		Type: componentTypeActionRow,
		Components: []discordComponent{
			{Type: componentTypeButton, Style: buttonStyleSuccess, Label: "Approve", CustomID: "gate_approve:" + req.ID},
			{Type: componentTypeButton, Style: buttonStylePrimary, Label: "Always", CustomID: "gate_always:" + req.ID + ":" + req.Tool},
			{Type: componentTypeButton, Style: buttonStyleDanger, Label: "Reject", CustomID: "gate_reject:" + req.ID},
		},
	}}
	g.bot.sendMessageWithComponents(g.channelID, text, components)

	select {
	case approved := <-ch:
		return approved, nil
	case <-ctx.Done():
		return false, fmt.Errorf("approval timed out: %v", ctx.Err())
	}
}

func (g *discordApprovalGate) handleGateCallback(reqID string, approved bool) {
	g.mu.Lock()
	ch, ok := g.pending[reqID]
	g.mu.Unlock()
	if ok {
		select {
		case ch <- approved:
		default:
		}
	}
}
