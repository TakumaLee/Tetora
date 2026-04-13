package discord

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"tetora/internal/log"
	"tetora/internal/upload"
)

// --- Event Handling ---

func (b *Bot) handleEvent(payload GatewayPayload) {
	switch payload.T {
	case "READY":
		var ready ReadyData
		if json.Unmarshal(payload.D, &ready) == nil {
			b.botUserID = ready.User.ID
			b.sessionID = ready.SessionID
			log.Info("discord bot connected", "user", ready.User.Username, "id", ready.User.ID)

			// P14.5: Auto-join voice channels if configured
			if b.cfg.Discord.Voice.Enabled && len(b.cfg.Discord.Voice.AutoJoin) > 0 {
				go b.voice.AutoJoinChannels()
			}
		}
	case "MESSAGE_CREATE":
		// P14.2: Parse with channel_type for thread detection.
		var msgT discordMessageWithType
		if json.Unmarshal(payload.D, &msgT) == nil {
			select {
			case b.msgSem <- struct{}{}:
				go func() {
					defer func() { <-b.msgSem }()
					b.handleMessageWithType(msgT.Message, msgT.ChannelType)
				}()
			default:
				log.Warn("discord message handler limit reached, dropping message",
					"author", msgT.Author.Username, "channel", msgT.ChannelID)
			}
		}
	case "VOICE_STATE_UPDATE":
		// P14.5: Handle voice state updates
		var vsu voiceStateUpdateData
		if json.Unmarshal(payload.D, &vsu) == nil {
			b.voice.HandleVoiceStateUpdate(vsu)
		}
	case "VOICE_SERVER_UPDATE":
		// P14.5: Handle voice server updates
		var vsuData voiceServerUpdateData
		if json.Unmarshal(payload.D, &vsuData) == nil {
			b.voice.HandleVoiceServerUpdate(vsuData)
		}
	case "INTERACTION_CREATE":
		// Handle button clicks and component interactions via Gateway.
		var interaction Interaction
		if json.Unmarshal(payload.D, &interaction) == nil {
			go b.handleGatewayInteraction(&interaction)
		}
	}
}

// isDuplicateMessage checks if a message ID was already processed recently.
// Returns true if duplicate (should skip), false if new (recorded for future checks).
func (b *Bot) isDuplicateMessage(msgID string) bool {
	b.dedupMu.Lock()
	defer b.dedupMu.Unlock()
	for _, id := range b.dedupRing {
		if id == msgID {
			return true
		}
	}
	b.dedupRing[b.dedupIdx%len(b.dedupRing)] = msgID
	b.dedupIdx++
	return false
}

// handleMessageWithType is the top-level message handler that checks for thread bindings
// before falling through to normal message handling. (P14.2)
func (b *Bot) handleMessageWithType(msg Message, channelType int) {
	log.Debug("discord message received",
		"author", msg.Author.Username, "channel", msg.ChannelID,
		"content_len", len(msg.Content), "bot", msg.Author.Bot,
		"guild", msg.GuildID, "mentions", len(msg.Mentions))

	// Ignore bots.
	if msg.Author.Bot || msg.Author.ID == b.botUserID {
		return
	}

	// Dedup: skip if this message was already processed (gateway resume replays events).
	if b.isDuplicateMessage(msg.ID) {
		log.Debug("discord message dedup: skipping replayed message", "msgId", msg.ID, "author", msg.Author.Username)
		return
	}

	// P14.2: Check thread bindings first.
	if b.handleThreadMessage(msg, channelType) {
		return
	}

	// Fall through to normal handling.
	b.handleMessage(msg)
}

func (b *Bot) handleMessage(msg Message) {
	// Ignore bots.
	if msg.Author.Bot || msg.Author.ID == b.botUserID {
		return
	}

	// Channel/guild restriction — also resolves thread→parent for allowlist check.
	if !b.isAllowedChannelOrThread(msg.ChannelID, msg.GuildID) {
		return
	}
	if b.cfg.Discord.GuildID != "" && msg.GuildID != b.cfg.Discord.GuildID {
		return
	}

	// Direct channels respond to all messages; mention channels require @; DMs always accepted.
	// For threads, inherit the parent channel's direct-channel status.
	mentioned := IsMentioned(msg.Mentions, b.botUserID)
	isDM := msg.GuildID == ""
	isDirect := b.isDirectChannel(msg.ChannelID)
	if !isDirect && msg.GuildID != "" {
		if parentID := b.resolveThreadParent(msg.ChannelID); parentID != "" {
			isDirect = b.isDirectChannel(parentID)
		}
	}
	log.Debug("discord message filter",
		"mentioned", mentioned, "isDM", isDM, "isDirect", isDirect,
		"channel", msg.ChannelID, "author", msg.Author.Username)
	if !mentioned && !isDM && !isDirect {
		return
	}

	text := StripMention(msg.Content, b.botUserID)
	text = strings.TrimSpace(text)

	// Download attachments and inject into prompt.
	var attachedFiles []*upload.File
	for _, att := range msg.Attachments {
		if f, err := downloadDiscordAttachment(b.cfg.BaseDir, att); err != nil {
			log.Warn("discord: attachment download failed", "url", att.URL, "err", err)
		} else {
			attachedFiles = append(attachedFiles, f)
		}
	}
	if prefix := upload.BuildPromptPrefix(attachedFiles); prefix != "" {
		text = prefix + text
	}

	if text == "" {
		return
	}

	// P14.4: Forum board commands (/assign, /status) — available in any context.
	if b.forumBoard != nil && b.forumBoard.IsConfigured() {
		if strings.HasPrefix(text, "/assign") {
			args := strings.TrimPrefix(text, "/assign")
			reply := b.forumBoard.HandleAssignCommand(msg.ChannelID, msg.GuildID, args)
			b.sendMessage(msg.ChannelID, reply)
			return
		}
		if strings.HasPrefix(text, "/status") {
			args := strings.TrimPrefix(text, "/status")
			reply := b.forumBoard.HandleStatusCommand(msg.ChannelID, args)
			b.sendMessage(msg.ChannelID, reply)
			return
		}
	}

	// P14.5: Voice channel commands (/vc join|leave|status)
	if strings.HasPrefix(text, "/vc") {
		argsStr := strings.TrimPrefix(text, "/vc")
		args := strings.Fields(strings.TrimSpace(argsStr))
		b.handleVoiceCommand(msg, args)
		return
	}

	// Terminal bridge: route text to active terminal session (before command handling).
	if b.terminal != nil && b.terminal.handleTerminalInput(msg.ChannelID, text) {
		return
	}

	// Command handling.
	if strings.HasPrefix(text, "!") {
		b.handleCommand(msg, text[1:])
		return
	}

	// Per-channel route binding (highest priority).
	// For threads, also check parent channel's route binding.
	if route, ok := b.cfg.Discord.Routes[msg.ChannelID]; ok && route.Agent != "" {
		b.handleDirectRoute(msg, text, route.Agent)
		return
	}
	if msg.GuildID != "" {
		if parentID := b.resolveThreadParent(msg.ChannelID); parentID != "" {
			if route, ok := b.cfg.Discord.Routes[parentID]; ok && route.Agent != "" {
				b.handleDirectRoute(msg, text, route.Agent)
				return
			}
		}
	}

	// !chat lock: skip smart dispatch, route directly to locked agent.
	if agent := b.getChatLock(msg.ChannelID); agent != "" {
		b.handleDirectRoute(msg, text, agent)
		return
	}

	if b.cfg.SmartDispatch.Enabled {
		b.handleRoute(msg, text)
	} else if b.cfg.DefaultAgent != "" {
		// No smart dispatch — route directly to the system default agent.
		b.handleDirectRoute(msg, text, b.cfg.DefaultAgent)
	} else {
		b.sendMessage(msg.ChannelID, "Smart dispatch is not enabled. Use `!help` for commands.")
	}
}

// downloadDiscordAttachment fetches an attachment from Discord CDN and saves it locally.
func downloadDiscordAttachment(baseDir string, att Attachment) (*upload.File, error) {
	resp, err := http.Get(att.URL) //nolint:noctx
	if err != nil {
		return nil, fmt.Errorf("discord attachment: http get: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("discord attachment: HTTP %d for %s", resp.StatusCode, att.Filename)
	}
	uploadDir := upload.InitDir(baseDir)
	return upload.Save(uploadDir, att.Filename, resp.Body, att.Size, "discord")
}
