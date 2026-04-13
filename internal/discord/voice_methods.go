package discord

import (
	"fmt"

)

// Type aliases.
type discordVoiceManager = VoiceManager
type voiceStateUpdatePayload = VoiceStateUpdatePayload
type voiceServerUpdateData = VoiceServerUpdateData
type voiceStateUpdateData = VoiceStateUpdateData

// Constant aliases.
const (
	opVoiceStateUpdate     = OpVoiceStateUpdate
	opVoiceServerUpdate    = OpVoiceServerUpdate
	intentGuildVoiceStates = IntentGuildVoiceStates
)

// newDiscordVoiceManager creates a VoiceManager wired to the bot's deps.
func newDiscordVoiceManager(bot *Bot) *discordVoiceManager {
	deps := VoiceDeps{
		SendGateway: func(payload GatewayPayload) error {
			return bot.sendToGateway(GatewayPayload(payload))
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

	return NewVoiceManager(deps)
}

// handleVoiceCommand processes /vc commands.
func (b *Bot) handleVoiceCommand(msg Message, args []string) {
	if !b.cfg.Discord.Voice.Enabled {
		b.sendMessage(msg.ChannelID, "Voice channel support is not enabled.")
		return
	}

	if len(args) == 0 {
		b.sendMessage(msg.ChannelID, "Usage: `/vc <join|leave|status> [channel_id]`")
		return
	}

	subCmd := args[0]

	switch subCmd {
	case "join":
		if len(args) < 2 {
			b.sendMessage(msg.ChannelID, "Usage: `/vc join <channel_id>`")
			return
		}
		channelID := args[1]
		guildID := msg.GuildID

		if guildID == "" {
			b.sendMessage(msg.ChannelID, "Voice channels are only available in guilds.")
			return
		}

		if err := b.voice.JoinVoiceChannel(guildID, channelID); err != nil {
			b.sendMessage(msg.ChannelID, fmt.Sprintf("Failed to join voice channel: %v", err))
		} else {
			b.sendMessage(msg.ChannelID, fmt.Sprintf("Joining voice channel <#%s>...", channelID))
		}

	case "leave":
		if err := b.voice.LeaveVoiceChannel(); err != nil {
			b.sendMessage(msg.ChannelID, fmt.Sprintf("Failed to leave voice channel: %v", err))
		} else {
			b.sendMessage(msg.ChannelID, "Leaving voice channel...")
		}

	case "status":
		status := b.voice.GetStatus()
		connected := status["connected"].(bool)
		if connected {
			b.sendMessage(msg.ChannelID,
				fmt.Sprintf("Connected to voice channel <#%s> in guild %s",
					status["channelId"], status["guildId"]))
		} else {
			b.sendMessage(msg.ChannelID, "Not connected to any voice channel.")
		}

	default:
		b.sendMessage(msg.ChannelID, "Unknown subcommand. Use: `join`, `leave`, or `status`")
	}
}

// sendToGateway sends a payload to the active gateway websocket.
func (b *Bot) sendToGateway(payload GatewayPayload) error {
	if b.gatewayConn == nil {
		return fmt.Errorf("no active gateway connection")
	}
	return b.gatewayConn.WriteJSON(payload)
}
