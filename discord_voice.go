package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// --- P14.5: Discord Voice Channel ---

// Gateway opcodes for voice (in addition to existing opcodes in discord.go)
const (
	opVoiceStateUpdate = 4  // client -> gateway: request to join/leave voice channel
	opVoiceServerUpdate = 0 // gateway -> client: voice server details (via VOICE_SERVER_UPDATE dispatch)
)

// Gateway intents for voice
const (
	intentGuildVoiceStates = 1 << 7 // required for VOICE_STATE_UPDATE events
)

// DiscordVoiceConfig holds configuration for Discord voice channel integration.
type DiscordVoiceConfig struct {
	Enabled  bool                    `json:"enabled"`
	AutoJoin []DiscordVoiceAutoJoin  `json:"autoJoin,omitempty"` // channels to auto-join on connect
	TTS      DiscordVoiceTTSConfig   `json:"tts,omitempty"`      // TTS settings for voice playback
}

// DiscordVoiceAutoJoin specifies a voice channel to auto-join.
type DiscordVoiceAutoJoin struct {
	GuildID   string `json:"guildId"`
	ChannelID string `json:"channelId"`
}

// DiscordVoiceTTSConfig holds TTS settings for voice playback.
type DiscordVoiceTTSConfig struct {
	Provider string `json:"provider,omitempty"` // "elevenlabs", "openai", etc.
	Voice    string `json:"voice,omitempty"`    // voice ID/name
}

// voiceStateUpdatePayload is sent to the gateway to join/leave voice channels.
type voiceStateUpdatePayload struct {
	GuildID   string  `json:"guild_id"`
	ChannelID *string `json:"channel_id"` // null to disconnect
	SelfMute  bool    `json:"self_mute"`
	SelfDeaf  bool    `json:"self_deaf"`
}

// voiceServerUpdateData is received from gateway when joining a voice channel.
type voiceServerUpdateData struct {
	Token    string `json:"token"`
	GuildID  string `json:"guild_id"`
	Endpoint string `json:"endpoint"`
}

// voiceStateUpdateData is received from gateway for voice state changes.
type voiceStateUpdateData struct {
	GuildID   string `json:"guild_id,omitempty"`
	ChannelID string `json:"channel_id,omitempty"`
	UserID    string `json:"user_id"`
	SessionID string `json:"session_id,omitempty"`
	SelfMute  bool   `json:"self_mute"`
	SelfDeaf  bool   `json:"self_deaf"`
}

// discordVoiceManager manages voice channel connections and state.
type discordVoiceManager struct {
	bot *DiscordBot
	mu  sync.RWMutex

	// Current voice state
	currentGuildID   string
	currentChannelID string
	voiceToken       string
	voiceEndpoint    string
	voiceSessionID   string

	// Voice connection state
	connected bool
	ws        *wsConn // placeholder for future voice websocket connection
}

func newDiscordVoiceManager(bot *DiscordBot) *discordVoiceManager {
	return &discordVoiceManager{bot: bot}
}

// joinVoiceChannel sends a voice state update to join the specified voice channel.
func (vm *discordVoiceManager) joinVoiceChannel(guildID, channelID string) error {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	// Build voice state update payload
	payload := voiceStateUpdatePayload{
		GuildID:   guildID,
		ChannelID: &channelID,
		SelfMute:  false,
		SelfDeaf:  false,
	}

	d, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal voice state update: %w", err)
	}

	// Send via gateway (requires access to websocket connection)
	// For now, we'll send via the bot's active gateway connection
	if err := vm.sendGatewayPayload(gatewayPayload{Op: opVoiceStateUpdate, D: d}); err != nil {
		return fmt.Errorf("send voice state update: %w", err)
	}

	vm.currentGuildID = guildID
	vm.currentChannelID = channelID

	logInfo("discord voice: requested join", "guild", guildID, "channel", channelID)
	return nil
}

// leaveVoiceChannel disconnects from the current voice channel.
func (vm *discordVoiceManager) leaveVoiceChannel() error {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	if vm.currentGuildID == "" {
		return fmt.Errorf("not connected to any voice channel")
	}

	// Send voice state update with null channel_id to disconnect
	payload := voiceStateUpdatePayload{
		GuildID:   vm.currentGuildID,
		ChannelID: nil,
		SelfMute:  false,
		SelfDeaf:  false,
	}

	d, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal voice state update: %w", err)
	}

	if err := vm.sendGatewayPayload(gatewayPayload{Op: opVoiceStateUpdate, D: d}); err != nil {
		return fmt.Errorf("send voice state update: %w", err)
	}

	guildID := vm.currentGuildID
	channelID := vm.currentChannelID

	vm.currentGuildID = ""
	vm.currentChannelID = ""
	vm.connected = false

	logInfo("discord voice: requested leave", "guild", guildID, "channel", channelID)
	return nil
}

// getStatus returns current voice connection status.
func (vm *discordVoiceManager) getStatus() map[string]interface{} {
	vm.mu.RLock()
	defer vm.mu.RUnlock()

	return map[string]interface{}{
		"connected": vm.connected,
		"guildId":   vm.currentGuildID,
		"channelId": vm.currentChannelID,
	}
}

// handleVoiceServerUpdate processes VOICE_SERVER_UPDATE events from the gateway.
func (vm *discordVoiceManager) handleVoiceServerUpdate(data voiceServerUpdateData) {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	vm.voiceToken = data.Token
	vm.voiceEndpoint = data.Endpoint
	vm.currentGuildID = data.GuildID

	logInfo("discord voice: server update received",
		"guild", data.GuildID,
		"endpoint", data.Endpoint)

	// TODO: In full implementation, this would initiate UDP connection + voice websocket
	// For now, we just mark as connected (placeholder)
	vm.connected = true
}

// handleVoiceStateUpdate processes VOICE_STATE_UPDATE events from the gateway.
func (vm *discordVoiceManager) handleVoiceStateUpdate(data voiceStateUpdateData) {
	// Only track our own bot's voice state
	if data.UserID != vm.bot.botUserID {
		return
	}

	vm.mu.Lock()
	defer vm.mu.Unlock()

	vm.voiceSessionID = data.SessionID

	// If channel is empty, we've disconnected
	if data.ChannelID == "" {
		vm.connected = false
		vm.currentChannelID = ""
		logInfo("discord voice: disconnected from voice channel")
		return
	}

	vm.currentChannelID = data.ChannelID
	logInfo("discord voice: state updated", "channel", data.ChannelID, "session", data.SessionID)
}

// sendGatewayPayload sends a payload to the Discord gateway.
// This requires access to the active websocket connection from the main gateway loop.
// We'll store a reference to it in the voice manager.
func (vm *discordVoiceManager) sendGatewayPayload(payload gatewayPayload) error {
	// This is a placeholder - in the actual implementation, we need to send this
	// via the bot's active gateway websocket connection.
	// For now, we'll use the bot's sendToGateway helper (to be added).
	return vm.bot.sendToGateway(payload)
}

// autoJoinChannels joins configured auto-join voice channels.
func (vm *discordVoiceManager) autoJoinChannels() {
	if !vm.bot.cfg.Discord.Voice.Enabled {
		return
	}

	for _, aj := range vm.bot.cfg.Discord.Voice.AutoJoin {
		if aj.GuildID == "" || aj.ChannelID == "" {
			continue
		}

		// Delay slightly to ensure gateway is ready
		time.Sleep(2 * time.Second)

		if err := vm.joinVoiceChannel(aj.GuildID, aj.ChannelID); err != nil {
			logWarn("discord voice: auto-join failed", "guild", aj.GuildID,
				"channel", aj.ChannelID, "error", err)
		} else {
			logInfo("discord voice: auto-joined", "guild", aj.GuildID, "channel", aj.ChannelID)
		}
	}
}

// playTTS generates TTS audio and plays it in the voice channel (placeholder).
func (vm *discordVoiceManager) playTTS(ctx context.Context, text string) error {
	vm.mu.RLock()
	connected := vm.connected
	vm.mu.RUnlock()

	if !connected {
		return fmt.Errorf("not connected to voice channel")
	}

	// TODO: Full implementation would:
	// 1. Call TTS provider (ElevenLabs, OpenAI, etc.) to generate audio
	// 2. Encode audio to Opus format
	// 3. Send RTP packets via UDP to Discord voice server
	// 4. Handle voice websocket protocol (opcode 0-13)

	// For now, just log placeholder
	logInfo("discord voice: TTS playback requested (placeholder)",
		"text_length", len(text),
		"provider", vm.bot.cfg.Discord.Voice.TTS.Provider,
		"voice", vm.bot.cfg.Discord.Voice.TTS.Voice)

	return nil
}

// --- Command Handling ---

// handleVoiceCommand processes /vc commands.
func (db *DiscordBot) handleVoiceCommand(msg discordMessage, args []string) {
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

		if err := db.voice.joinVoiceChannel(guildID, channelID); err != nil {
			db.sendMessage(msg.ChannelID, fmt.Sprintf("Failed to join voice channel: %v", err))
		} else {
			db.sendMessage(msg.ChannelID, fmt.Sprintf("Joining voice channel <#%s>...", channelID))
		}

	case "leave":
		if err := db.voice.leaveVoiceChannel(); err != nil {
			db.sendMessage(msg.ChannelID, fmt.Sprintf("Failed to leave voice channel: %v", err))
		} else {
			db.sendMessage(msg.ChannelID, "Leaving voice channel...")
		}

	case "status":
		status := db.voice.getStatus()
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
// This needs to be added to DiscordBot to support voice state updates.
func (db *DiscordBot) sendToGateway(payload gatewayPayload) error {
	// Store active websocket connection in DiscordBot for this to work.
	// For now, return placeholder error.
	// This will be wired up in the main gateway loop.
	if db.gatewayConn == nil {
		return fmt.Errorf("no active gateway connection")
	}
	return db.gatewayConn.WriteJSON(payload)
}
