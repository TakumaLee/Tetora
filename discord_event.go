package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"tetora/internal/discord"
	"tetora/internal/log"
	"tetora/internal/upload"
)

// --- Event Handling ---

func (db *DiscordBot) handleEvent(payload discord.GatewayPayload) {
	switch payload.T {
	case "READY":
		var ready discord.ReadyData
		if json.Unmarshal(payload.D, &ready) == nil {
			db.botUserID = ready.User.ID
			db.sessionID = ready.SessionID
			log.Info("discord bot connected", "user", ready.User.Username, "id", ready.User.ID)

			// P14.5: Auto-join voice channels if configured
			if db.cfg.Discord.Voice.Enabled && len(db.cfg.Discord.Voice.AutoJoin) > 0 {
				go db.voice.AutoJoinChannels()
			}
		}
	case "MESSAGE_CREATE":
		// P14.2: Parse with channel_type for thread detection.
		var msgT discordMessageWithType
		if json.Unmarshal(payload.D, &msgT) == nil {
			select {
			case db.msgSem <- struct{}{}:
				go func() {
					defer func() { <-db.msgSem }()
					db.handleMessageWithType(msgT.Message, msgT.ChannelType)
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
			db.voice.HandleVoiceStateUpdate(vsu)
		}
	case "VOICE_SERVER_UPDATE":
		// P14.5: Handle voice server updates
		var vsuData voiceServerUpdateData
		if json.Unmarshal(payload.D, &vsuData) == nil {
			db.voice.HandleVoiceServerUpdate(vsuData)
		}
	case "INTERACTION_CREATE":
		// Handle button clicks and component interactions via Gateway.
		var interaction discord.Interaction
		if json.Unmarshal(payload.D, &interaction) == nil {
			go db.handleGatewayInteraction(&interaction)
		}
	}
}

// isDuplicateMessage checks if a message ID was already processed recently.
// Returns true if duplicate (should skip), false if new (recorded for future checks).
func (db *DiscordBot) isDuplicateMessage(msgID string) bool {
	db.dedupMu.Lock()
	defer db.dedupMu.Unlock()
	for _, id := range db.dedupRing {
		if id == msgID {
			return true
		}
	}
	db.dedupRing[db.dedupIdx%len(db.dedupRing)] = msgID
	db.dedupIdx++
	return false
}

// handleMessageWithType is the top-level message handler that checks for thread bindings
// before falling through to normal message handling. (P14.2)
func (db *DiscordBot) handleMessageWithType(msg discord.Message, channelType int) {
	log.Debug("discord message received",
		"author", msg.Author.Username, "channel", msg.ChannelID,
		"content_len", len(msg.Content), "bot", msg.Author.Bot,
		"guild", msg.GuildID, "mentions", len(msg.Mentions))

	// Ignore bots.
	if msg.Author.Bot || msg.Author.ID == db.botUserID {
		return
	}

	// Dedup: skip if this message was already processed (gateway resume replays events).
	if db.isDuplicateMessage(msg.ID) {
		log.Debug("discord message dedup: skipping replayed message", "msgId", msg.ID, "author", msg.Author.Username)
		return
	}

	// P14.2: Check thread bindings first.
	if db.handleThreadMessage(msg, channelType) {
		return
	}

	// Fall through to normal handling.
	db.handleMessage(msg)
}

func (db *DiscordBot) handleMessage(msg discord.Message) {
	// Ignore bots.
	if msg.Author.Bot || msg.Author.ID == db.botUserID {
		return
	}

	// Channel/guild restriction — also resolves thread→parent for allowlist check.
	if !db.isAllowedChannelOrThread(msg.ChannelID, msg.GuildID) {
		return
	}
	if db.cfg.Discord.GuildID != "" && msg.GuildID != db.cfg.Discord.GuildID {
		return
	}

	// Direct channels respond to all messages; mention channels require @; DMs always accepted.
	// For threads, inherit the parent channel's direct-channel status.
	mentioned := discord.IsMentioned(msg.Mentions, db.botUserID)
	isDM := msg.GuildID == ""
	isDirect := db.isDirectChannel(msg.ChannelID)
	if !isDirect && msg.GuildID != "" {
		if parentID := db.resolveThreadParent(msg.ChannelID); parentID != "" {
			isDirect = db.isDirectChannel(parentID)
		}
	}
	log.Debug("discord message filter",
		"mentioned", mentioned, "isDM", isDM, "isDirect", isDirect,
		"channel", msg.ChannelID, "author", msg.Author.Username)
	if !mentioned && !isDM && !isDirect {
		return
	}

	text := discord.StripMention(msg.Content, db.botUserID)
	text = strings.TrimSpace(text)

	// Download attachments and inject into prompt.
	var attachedFiles []*upload.File
	for _, att := range msg.Attachments {
		if f, err := downloadDiscordAttachment(db.cfg.BaseDir, att); err != nil {
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
	if db.forumBoard != nil && db.forumBoard.IsConfigured() {
		if strings.HasPrefix(text, "/assign") {
			args := strings.TrimPrefix(text, "/assign")
			reply := db.forumBoard.HandleAssignCommand(msg.ChannelID, msg.GuildID, args)
			db.sendMessage(msg.ChannelID, reply)
			return
		}
		if strings.HasPrefix(text, "/status") {
			args := strings.TrimPrefix(text, "/status")
			reply := db.forumBoard.HandleStatusCommand(msg.ChannelID, args)
			db.sendMessage(msg.ChannelID, reply)
			return
		}
	}

	// P14.5: Voice channel commands (/vc join|leave|status)
	if strings.HasPrefix(text, "/vc") {
		argsStr := strings.TrimPrefix(text, "/vc")
		args := strings.Fields(strings.TrimSpace(argsStr))
		db.handleVoiceCommand(msg, args)
		return
	}

	// Terminal bridge: route text to active terminal session (before command handling).
	if db.terminal != nil && db.terminal.handleTerminalInput(msg.ChannelID, text) {
		return
	}

	// Command handling.
	if strings.HasPrefix(text, "!") {
		db.handleCommand(msg, text[1:])
		return
	}

	// Per-channel route binding (highest priority).
	// For threads, also check parent channel's route binding.
	if route, ok := db.cfg.Discord.Routes[msg.ChannelID]; ok && route.Agent != "" {
		db.handleDirectRoute(msg, text, route.Agent)
		return
	}
	if msg.GuildID != "" {
		if parentID := db.resolveThreadParent(msg.ChannelID); parentID != "" {
			if route, ok := db.cfg.Discord.Routes[parentID]; ok && route.Agent != "" {
				db.handleDirectRoute(msg, text, route.Agent)
				return
			}
		}
	}

	// !chat lock: skip smart dispatch, route directly to locked agent.
	if agent := db.getChatLock(msg.ChannelID); agent != "" {
		db.handleDirectRoute(msg, text, agent)
		return
	}

	if db.cfg.SmartDispatch.Enabled {
		db.handleRoute(msg, text)
	} else if db.cfg.DefaultAgent != "" {
		// No smart dispatch — route directly to the system default agent.
		db.handleDirectRoute(msg, text, db.cfg.DefaultAgent)
	} else {
		db.sendMessage(msg.ChannelID, "Smart dispatch is not enabled. Use `!help` for commands.")
	}
}

// downloadDiscordAttachment fetches an attachment from Discord CDN and saves it locally.
func downloadDiscordAttachment(baseDir string, att discord.Attachment) (*upload.File, error) {
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
