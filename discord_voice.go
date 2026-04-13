package main

import (
	"fmt"

	"tetora/internal/discord"
)

// Type aliases.
type discordVoiceManager = discord.VoiceManager
type voiceStateUpdatePayload = discord.VoiceStateUpdatePayload
type voiceServerUpdateData = discord.VoiceServerUpdateData
type voiceStateUpdateData = discord.VoiceStateUpdateData

// Constant aliases.
const (
	opVoiceStateUpdate     = discord.OpVoiceStateUpdate
	opVoiceServerUpdate    = discord.OpVoiceServerUpdate
	intentGuildVoiceStates = discord.IntentGuildVoiceStates
)

// newDiscordVoiceManager creates a VoiceManager wired to the bot's deps.
func newDiscordVoiceManager(bot *DiscordBot) *discordVoiceManager {
	deps := discord.VoiceDeps{
		SendGateway: func(payload discord.GatewayPayload) error {
			return bot.sendToGateway(discord.GatewayPayload(payload))
		},
	}

	if bot != nil {
		deps.BotUserID = bot.botUserID
		if bot.cfg != nil {
			deps.VoiceEnabled = bot.cfg.Discord.Voice.Enabled
			deps.AutoJoin = bot.cfg.Discord.Voice.AutoJoin
			deps.TTS = bot.cfg.Discord.Voice.TTS
		}
	}

	return discord.NewVoiceManager(deps)
}

// handleVoiceCommand processes /vc commands.
func (db *DiscordBot) handleVoiceCommand(msg discord.Message, args []string) {
	if !db.cfg.Discord.Voice.Enabled {
		db.sendMessage(msg.ChannelID, "Voice channel support is not enabled.")
		return
	}

	if len(args) == 0 {
		db.sendMessage(msg.ChannelID, "Usage: `/vc <join|leave|status> [channel_id]`")
		return
	}

	subCmd := args[0]

	switch subCmd {
	case "join":
		if len(args) < 2 {
			db.sendMessage(msg.ChannelID, "Usage: `/vc join <channel_id>`")
			return
		}
		channelID := args[1]
		guildID := msg.GuildID

		if guildID == "" {
			db.sendMessage(msg.ChannelID, "Voice channels are only available in guilds.")
			return
		}

		if err := db.voice.JoinVoiceChannel(guildID, channelID); err != nil {
			db.sendMessage(msg.ChannelID, fmt.Sprintf("Failed to join voice channel: %v", err))
		} else {
			db.sendMessage(msg.ChannelID, fmt.Sprintf("Joining voice channel <#%s>...", channelID))
		}

	case "leave":
		if err := db.voice.LeaveVoiceChannel(); err != nil {
			db.sendMessage(msg.ChannelID, fmt.Sprintf("Failed to leave voice channel: %v", err))
		} else {
			db.sendMessage(msg.ChannelID, "Leaving voice channel...")
		}

	case "status":
		status := db.voice.GetStatus()
		connected := status["connected"].(bool)
		if connected {
			db.sendMessage(msg.ChannelID,
				fmt.Sprintf("Connected to voice channel <#%s> in guild %s",
					status["channelId"], status["guildId"]))
		} else {
			db.sendMessage(msg.ChannelID, "Not connected to any voice channel.")
		}

	default:
		db.sendMessage(msg.ChannelID, "Unknown subcommand. Use: `join`, `leave`, or `status`")
	}
}

// sendToGateway sends a payload to the active gateway websocket.
func (db *DiscordBot) sendToGateway(payload discord.GatewayPayload) error {
	if db.gatewayConn == nil {
		return fmt.Errorf("no active gateway connection")
	}
	return db.gatewayConn.WriteJSON(payload)
}
