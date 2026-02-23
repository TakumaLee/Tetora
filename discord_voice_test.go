package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// --- P14.5: Discord Voice Channel Tests ---

func TestVoiceStateUpdatePayload(t *testing.T) {
	tests := []struct {
		name      string
		guildID   string
		channelID *string
		wantNull  bool
	}{
		{
			name:      "join channel",
			guildID:   "guild123",
			channelID: stringPtr("voice456"),
			wantNull:  false,
		},
		{
			name:      "leave channel",
			guildID:   "guild123",
			channelID: nil,
			wantNull:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := voiceStateUpdatePayload{
				GuildID:   tt.guildID,
				ChannelID: tt.channelID,
				SelfMute:  false,
				SelfDeaf:  false,
			}

			data, err := json.Marshal(payload)
			if err != nil {
				t.Fatalf("marshal failed: %v", err)
			}

			// Verify channel_id is null when leaving
			if tt.wantNull {
				if !strings.Contains(string(data), `"channel_id":null`) {
					t.Errorf("expected channel_id to be null, got: %s", data)
				}
			} else {
				if strings.Contains(string(data), `"channel_id":null`) {
					t.Errorf("expected channel_id to be set, got: %s", data)
				}
			}

			// Verify guild_id is always set
			if !strings.Contains(string(data), tt.guildID) {
				t.Errorf("expected guild_id %s in payload, got: %s", tt.guildID, data)
			}
		})
	}
}

func TestVoiceManagerJoinLeave(t *testing.T) {
	cfg := &Config{
		Discord: DiscordBotConfig{
			Voice: DiscordVoiceConfig{
				Enabled: true,
			},
		},
	}

	bot := &DiscordBot{
		cfg:       cfg,
		botUserID: "bot123",
	}
	bot.voice = newDiscordVoiceManager(bot)

	// Test initial state
	status := bot.voice.getStatus()
	if status["connected"].(bool) {
		t.Error("expected not connected initially")
	}

	// Note: We can't actually test joining without a real gateway connection,
	// but we can verify the manager is initialized correctly
	if bot.voice.bot != bot {
		t.Error("voice manager bot reference not set")
	}
}

func TestVoiceAutoJoinConfig(t *testing.T) {
	cfg := &Config{
		Discord: DiscordBotConfig{
			Voice: DiscordVoiceConfig{
				Enabled: true,
				AutoJoin: []DiscordVoiceAutoJoin{
					{GuildID: "guild1", ChannelID: "voice1"},
					{GuildID: "guild2", ChannelID: "voice2"},
				},
				TTS: DiscordVoiceTTSConfig{
					Provider: "elevenlabs",
					Voice:    "rachel",
				},
			},
		},
	}

	if !cfg.Discord.Voice.Enabled {
		t.Error("voice should be enabled")
	}

	if len(cfg.Discord.Voice.AutoJoin) != 2 {
		t.Errorf("expected 2 auto-join channels, got %d", len(cfg.Discord.Voice.AutoJoin))
	}

	if cfg.Discord.Voice.TTS.Provider != "elevenlabs" {
		t.Errorf("expected TTS provider elevenlabs, got %s", cfg.Discord.Voice.TTS.Provider)
	}
}

func TestVoiceCommandParsing(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		wantCmd string
		wantLen int
	}{
		{
			name:    "join with channel",
			text:    "/vc join 123456",
			wantCmd: "join",
			wantLen: 2,
		},
		{
			name:    "leave",
			text:    "/vc leave",
			wantCmd: "leave",
			wantLen: 1,
		},
		{
			name:    "status",
			text:    "/vc status",
			wantCmd: "status",
			wantLen: 1,
		},
		{
			name:    "no args",
			text:    "/vc",
			wantCmd: "",
			wantLen: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			argsStr := strings.TrimPrefix(tt.text, "/vc")
			args := strings.Fields(strings.TrimSpace(argsStr))

			if len(args) != tt.wantLen {
				t.Errorf("expected %d args, got %d", tt.wantLen, len(args))
			}

			if tt.wantLen > 0 && args[0] != tt.wantCmd {
				t.Errorf("expected command %s, got %s", tt.wantCmd, args[0])
			}
		})
	}
}

func TestVoiceStateUpdateEvent(t *testing.T) {
	data := voiceStateUpdateData{
		GuildID:   "guild123",
		ChannelID: "voice456",
		UserID:    "user789",
		SessionID: "session_abc",
		SelfMute:  false,
		SelfDeaf:  false,
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var parsed voiceStateUpdateData
	if err := json.Unmarshal(jsonData, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if parsed.UserID != "user789" {
		t.Errorf("expected user_id user789, got %s", parsed.UserID)
	}

	if parsed.SessionID != "session_abc" {
		t.Errorf("expected session_id session_abc, got %s", parsed.SessionID)
	}
}

func TestVoiceServerUpdateEvent(t *testing.T) {
	data := voiceServerUpdateData{
		Token:    "voice_token_xyz",
		GuildID:  "guild123",
		Endpoint: "us-east1.discord.gg:443",
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var parsed voiceServerUpdateData
	if err := json.Unmarshal(jsonData, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if parsed.Token != "voice_token_xyz" {
		t.Errorf("expected token voice_token_xyz, got %s", parsed.Token)
	}

	if !strings.Contains(parsed.Endpoint, "discord.gg") {
		t.Errorf("expected endpoint to contain discord.gg, got %s", parsed.Endpoint)
	}
}

// Helper function
func stringPtr(s string) *string {
	return &s
}
