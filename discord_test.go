package main

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"tetora/internal/db"
	"tetora/internal/discord"
)


// --- WebSocket Accept Key ---

func TestWsAcceptKey(t *testing.T) {
	// RFC 6455 example key.
	key := "dGhlIHNhbXBsZSBub25jZQ=="
	expected := "s3pPLMBiTxaQ9kYGzzhZRbK+xOo="
	got := wsAcceptKey(key)
	if got != expected {
		t.Errorf("wsAcceptKey(%q) = %q, want %q", key, got, expected)
	}
}

// --- Mention Detection ---

func TestDiscordIsMentioned(t *testing.T) {
	botID := "123456"
	tests := []struct {
		mentions []discord.User
		expected bool
	}{
		{nil, false},
		{[]discord.User{}, false},
		{[]discord.User{{ID: "999"}}, false},
		{[]discord.User{{ID: "123456"}}, true},
		{[]discord.User{{ID: "999"}, {ID: "123456"}}, true},
	}
	for _, tt := range tests {
		got := discord.IsMentioned(tt.mentions, botID)
		if got != tt.expected {
			t.Errorf("discord.IsMentioned(%v, %q) = %v, want %v", tt.mentions, botID, got, tt.expected)
		}
	}
}

// --- Strip Mention ---

func TestDiscordStripMention(t *testing.T) {
	botID := "123456"
	tests := []struct {
		content  string
		expected string
	}{
		{"<@123456> hello", "hello"},
		{"<@!123456> hello", "hello"},
		{"hello <@123456>", "hello"},
		{"hello", "hello"},
		{"<@123456>", ""},
		{"<@999> hello", "<@999> hello"},
	}
	for _, tt := range tests {
		got := discord.StripMention(tt.content, botID)
		if got != tt.expected {
			t.Errorf("discord.StripMention(%q, %q) = %q, want %q", tt.content, botID, got, tt.expected)
		}
	}
}

func TestDiscordStripMention_EmptyBotID(t *testing.T) {
	got := discord.StripMention("<@123> hello", "")
	if got != "<@123> hello" {
		t.Errorf("expected no change with empty botID, got %q", got)
	}
}

// --- Gateway Payload JSON ---

func TestGatewayPayloadMarshal(t *testing.T) {
	seq := 42
	p := discord.GatewayPayload{Op: discord.OpHeartbeat, S: &seq}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var decoded discord.GatewayPayload
	json.Unmarshal(data, &decoded)
	if decoded.Op != discord.OpHeartbeat {
		t.Errorf("expected op %d, got %d", discord.OpHeartbeat, decoded.Op)
	}
	if decoded.S == nil || *decoded.S != 42 {
		t.Errorf("expected seq 42, got %v", decoded.S)
	}
}

func TestGatewayPayloadUnmarshal(t *testing.T) {
	raw := `{"op":10,"d":{"heartbeat_interval":41250}}`
	var p discord.GatewayPayload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatal(err)
	}
	if p.Op != discord.OpHello {
		t.Errorf("expected op %d, got %d", discord.OpHello, p.Op)
	}
	var hd discord.HelloData
	json.Unmarshal(p.D, &hd)
	if hd.HeartbeatInterval != 41250 {
		t.Errorf("expected interval 41250, got %d", hd.HeartbeatInterval)
	}
}

// --- Discord Message Parse ---

func TestDiscordMessageParse(t *testing.T) {
	raw := `{
		"id": "123",
		"channel_id": "456",
		"guild_id": "789",
		"author": {"id": "111", "username": "user1", "bot": false},
		"content": "<@bot123> hello world",
		"mentions": [{"id": "bot123", "username": "tetora", "bot": true}]
	}`
	var msg discord.Message
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		t.Fatal(err)
	}
	if msg.ID != "123" {
		t.Errorf("expected id 123, got %q", msg.ID)
	}
	if msg.Author.Bot {
		t.Error("expected non-bot author")
	}
	if len(msg.Mentions) != 1 {
		t.Fatalf("expected 1 mention, got %d", len(msg.Mentions))
	}
	if msg.Mentions[0].ID != "bot123" {
		t.Errorf("expected mention id bot123, got %q", msg.Mentions[0].ID)
	}
}

// --- Embed Marshal ---

func TestDiscordEmbedMarshal(t *testing.T) {
	embed := discord.Embed{
		Title:       "Test",
		Description: "A test embed",
		Color:       0x5865F2,
		Fields: []discord.EmbedField{
			{Name: "Field1", Value: "Value1", Inline: true},
		},
		Footer:    &discord.EmbedFooter{Text: "footer"},
		Timestamp: "2024-01-01T00:00:00Z",
	}
	data, err := json.Marshal(embed)
	if err != nil {
		t.Fatal(err)
	}
	// Verify it's valid JSON and contains expected fields.
	var decoded map[string]any
	json.Unmarshal(data, &decoded)
	if decoded["title"] != "Test" {
		t.Errorf("expected title 'Test', got %v", decoded["title"])
	}
	if decoded["color"].(float64) != float64(0x5865F2) {
		t.Errorf("unexpected color value")
	}
	fields := decoded["fields"].([]any)
	if len(fields) != 1 {
		t.Errorf("expected 1 field, got %d", len(fields))
	}
}

// --- Ready Event Parse ---

func TestReadyDataParse(t *testing.T) {
	raw := `{"session_id":"abc123","user":{"id":"999","username":"tetora","bot":true}}`
	var ready discord.ReadyData
	if err := json.Unmarshal([]byte(raw), &ready); err != nil {
		t.Fatal(err)
	}
	if ready.SessionID != "abc123" {
		t.Errorf("expected session abc123, got %q", ready.SessionID)
	}
	if ready.User.ID != "999" {
		t.Errorf("expected user id 999, got %q", ready.User.ID)
	}
	if !ready.User.Bot {
		t.Error("expected bot flag true")
	}
}

// --- Config ---

func TestDiscordBotConfig(t *testing.T) {
	raw := `{"enabled":true,"botToken":"$DISCORD_TOKEN","guildID":"123","channelID":"456"}`
	var cfg DiscordBotConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled {
		t.Error("expected enabled")
	}
	if cfg.BotToken != "$DISCORD_TOKEN" {
		t.Errorf("expected $DISCORD_TOKEN, got %q", cfg.BotToken)
	}
	if cfg.GuildID != "123" {
		t.Errorf("expected guildID 123, got %q", cfg.GuildID)
	}
}

// --- Identify Data ---

func TestIdentifyDataMarshal(t *testing.T) {
	id := discord.IdentifyData{
		Token:   "test-token",
		Intents: discord.IntentGuildMessages | discord.IntentDirectMessages | discord.IntentMessageContent,
		Properties: map[string]string{
			"os": "linux", "browser": "tetora", "device": "tetora",
		},
	}
	data, err := json.Marshal(id)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	json.Unmarshal(data, &decoded)
	if decoded["token"] != "test-token" {
		t.Errorf("expected token, got %v", decoded["token"])
	}
	intents := int(decoded["intents"].(float64))
	if intents&discord.IntentMessageContent == 0 {
		t.Error("expected message content intent")
	}
}

// --- Message Truncation (matches Slack/TG pattern) ---

func TestDiscordMessageTruncation(t *testing.T) {
	long := make([]byte, 2500)
	for i := range long {
		long[i] = 'x'
	}
	content := string(long)
	// Simulate the truncation logic used in sendMessage.
	if len(content) > 2000 {
		content = content[:1997] + "..."
	}
	if len(content) != 2000 {
		t.Errorf("expected 2000 chars after truncation, got %d", len(content))
	}
}

// --- Embed Description Truncation ---

func TestDiscordEmbedDescTruncation(t *testing.T) {
	long := make([]byte, 4000)
	for i := range long {
		long[i] = 'y'
	}
	output := string(long)
	if len(output) > 3800 {
		output = output[:3797] + "..."
	}
	if len(output) != 3800 {
		t.Errorf("expected 3800 chars after truncation, got %d", len(output))
	}
}

// --- Hello Data Parse ---

func TestHelloDataParse(t *testing.T) {
	raw := `{"heartbeat_interval":41250}`
	var hd discord.HelloData
	json.Unmarshal([]byte(raw), &hd)
	if hd.HeartbeatInterval != 41250 {
		t.Errorf("expected 41250, got %d", hd.HeartbeatInterval)
	}
}

// --- Resume Payload ---

func TestResumePayloadMarshal(t *testing.T) {
	r := discord.ResumePayload{Token: "tok", SessionID: "sid", Seq: 10}
	data, _ := json.Marshal(r)
	var decoded map[string]any
	json.Unmarshal(data, &decoded)
	if decoded["token"] != "tok" {
		t.Errorf("expected token 'tok', got %v", decoded["token"])
	}
	if decoded["session_id"] != "sid" {
		t.Errorf("expected session_id 'sid', got %v", decoded["session_id"])
	}
	if int(decoded["seq"].(float64)) != 10 {
		t.Errorf("expected seq 10, got %v", decoded["seq"])
	}
}

// --- from discord_voice_test.go ---

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

			if tt.wantNull {
				if !strings.Contains(string(data), `"channel_id":null`) {
					t.Errorf("expected channel_id to be null, got: %s", data)
				}
			} else {
				if strings.Contains(string(data), `"channel_id":null`) {
					t.Errorf("expected channel_id to be set, got: %s", data)
				}
			}

			if !strings.Contains(string(data), tt.guildID) {
				t.Errorf("expected guild_id %s in payload, got: %s", tt.guildID, data)
			}
		})
	}
}

func TestVoiceManagerInitialization(t *testing.T) {
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
	status := bot.voice.GetStatus()
	if status["connected"].(bool) {
		t.Error("expected not connected initially")
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

// --- from discord_forum_test.go ---

// --- P14.4: Discord Forum Task Board Tests ---

// --- Valid Forum Statuses ---

func TestValidForumStatuses(t *testing.T) {
	statuses := discord.ValidForumStatuses()
	if len(statuses) != 5 {
		t.Errorf("expected 5 statuses, got %d", len(statuses))
	}

	expected := []string{"backlog", "todo", "doing", "review", "done"}
	for i, s := range expected {
		if statuses[i] != s {
			t.Errorf("status[%d] = %q, want %q", i, statuses[i], s)
		}
	}
}

func TestIsValidForumStatus(t *testing.T) {
	tests := []struct {
		status   string
		expected bool
	}{
		{"backlog", true},
		{"todo", true},
		{"doing", true},
		{"review", true},
		{"done", true},
		{"BACKLOG", false}, // case-sensitive
		{"unknown", false},
		{"", false},
		{"doing ", false}, // trailing space
	}
	for _, tt := range tests {
		got := discord.IsValidForumStatus(tt.status)
		if got != tt.expected {
			t.Errorf("discord.IsValidForumStatus(%q) = %v, want %v", tt.status, got, tt.expected)
		}
	}
}

// --- Status Constants ---

func TestForumStatusConstants(t *testing.T) {
	if discord.ForumStatusBacklog != "backlog" {
		t.Errorf("expected 'backlog', got %q", discord.ForumStatusBacklog)
	}
	if discord.ForumStatusTodo != "todo" {
		t.Errorf("expected 'todo', got %q", discord.ForumStatusTodo)
	}
	if discord.ForumStatusDoing != "doing" {
		t.Errorf("expected 'doing', got %q", discord.ForumStatusDoing)
	}
	if discord.ForumStatusReview != "review" {
		t.Errorf("expected 'review', got %q", discord.ForumStatusReview)
	}
	if discord.ForumStatusDone != "done" {
		t.Errorf("expected 'done', got %q", discord.ForumStatusDone)
	}
}

// --- Forum Board Creation ---

func TestNewDiscordForumBoard(t *testing.T) {
	cfg := DiscordForumBoardConfig{
		Enabled:        true,
		ForumChannelID: "F123",
		Tags: map[string]string{
			"backlog": "TAG1",
			"doing":   "TAG2",
			"done":    "TAG3",
		},
	}
	fb := newDiscordForumBoard(nil, cfg)
	if fb == nil {
		t.Fatal("expected non-nil forum board")
	}
}

// --- IsConfigured ---

func TestForumBoard_IsConfigured(t *testing.T) {
	tests := []struct {
		enabled   bool
		channelID string
		expected  bool
	}{
		{true, "F123", true},
		{true, "", false},
		{false, "F123", false},
		{false, "", false},
	}
	for _, tt := range tests {
		fb := newDiscordForumBoard(nil, DiscordForumBoardConfig{
			Enabled:        tt.enabled,
			ForumChannelID: tt.channelID,
		})
		got := fb.IsConfigured()
		if got != tt.expected {
			t.Errorf("IsConfigured(enabled=%v, channelID=%q) = %v, want %v",
				tt.enabled, tt.channelID, got, tt.expected)
		}
	}
}

// --- Config Validation ---

func TestValidateForumBoardConfig(t *testing.T) {
	// Valid config — no warnings.
	cfg := DiscordForumBoardConfig{
		Enabled:        true,
		ForumChannelID: "F123",
		Tags: map[string]string{
			"backlog": "TAG1",
			"done":    "TAG2",
		},
	}
	warnings := discord.ValidateForumBoardConfig(cfg)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}
}

func TestValidateForumBoardConfig_MissingChannelID(t *testing.T) {
	cfg := DiscordForumBoardConfig{
		Enabled: true,
	}
	warnings := discord.ValidateForumBoardConfig(cfg)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "forumChannelId is empty") {
		t.Errorf("unexpected warning: %s", warnings[0])
	}
}

func TestValidateForumBoardConfig_UnknownStatus(t *testing.T) {
	cfg := DiscordForumBoardConfig{
		Tags: map[string]string{
			"invalid_status": "TAG1",
		},
	}
	warnings := discord.ValidateForumBoardConfig(cfg)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "unknown status") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected unknown status warning, got %v", warnings)
	}
}

func TestValidateForumBoardConfig_EmptyTagID(t *testing.T) {
	cfg := DiscordForumBoardConfig{
		Tags: map[string]string{
			"doing": "",
		},
	}
	warnings := discord.ValidateForumBoardConfig(cfg)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "empty tag ID") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected empty tag ID warning, got %v", warnings)
	}
}

func TestValidateForumBoardConfig_Disabled(t *testing.T) {
	// Disabled config should not warn about missing channel ID.
	cfg := DiscordForumBoardConfig{
		Enabled: false,
	}
	warnings := discord.ValidateForumBoardConfig(cfg)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for disabled config, got %v", warnings)
	}
}

// --- Config Parsing ---

func TestDiscordForumBoardConfigParse(t *testing.T) {
	raw := `{"enabled":true,"forumChannelId":"F999","tags":{"backlog":"T1","done":"T2"}}`
	var cfg DiscordForumBoardConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled {
		t.Error("expected enabled")
	}
	if cfg.ForumChannelID != "F999" {
		t.Errorf("expected F999, got %q", cfg.ForumChannelID)
	}
	if cfg.Tags == nil {
		t.Fatal("expected tags map")
	}
	if cfg.Tags["backlog"] != "T1" {
		t.Errorf("expected T1 for backlog, got %q", cfg.Tags["backlog"])
	}
}

// --- Assign Command ---

func TestHandleAssignCommand_EmptyRole(t *testing.T) {
	fb := newDiscordForumBoard(nil, DiscordForumBoardConfig{Enabled: true})
	msg := fb.HandleAssignCommand("T1", "G1", "")
	if !strings.Contains(msg, "Usage:") {
		t.Errorf("expected usage message for empty role, got %q", msg)
	}
}

// --- Status Command ---

func TestHandleStatusCommand_EmptyStatus(t *testing.T) {
	fb := newDiscordForumBoard(nil, DiscordForumBoardConfig{Enabled: true})
	msg := fb.HandleStatusCommand("T1", "")
	if !strings.Contains(msg, "Usage:") {
		t.Errorf("expected usage message for empty status, got %q", msg)
	}
}

func TestHandleStatusCommand_InvalidStatus(t *testing.T) {
	fb := newDiscordForumBoard(nil, DiscordForumBoardConfig{Enabled: true})
	msg := fb.HandleStatusCommand("T1", "invalid")
	if !strings.Contains(msg, "Invalid status") {
		t.Errorf("expected invalid status message, got %q", msg)
	}
}

// --- CreateThread Validation ---

func TestCreateThread_NoChannelID(t *testing.T) {
	fb := newDiscordForumBoard(nil, DiscordForumBoardConfig{Enabled: true})
	_, err := fb.CreateThread("Title", "Body", "backlog")
	if err == nil {
		t.Error("expected error for missing forum channel ID")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Errorf("expected 'not configured' error, got %v", err)
	}
}

func TestCreateThread_EmptyTitle(t *testing.T) {
	fb := newDiscordForumBoard(nil, DiscordForumBoardConfig{
		Enabled:        true,
		ForumChannelID: "F123",
	})
	_, err := fb.CreateThread("", "Body", "backlog")
	if err == nil {
		t.Error("expected error for empty title")
	}
	if !strings.Contains(err.Error(), "title is required") {
		t.Errorf("expected 'title is required' error, got %v", err)
	}
}

func TestCreateThread_InvalidStatus(t *testing.T) {
	fb := newDiscordForumBoard(nil, DiscordForumBoardConfig{
		Enabled:        true,
		ForumChannelID: "F123",
	})
	_, err := fb.CreateThread("Title", "Body", "invalid_status")
	if err == nil {
		t.Error("expected error for invalid status")
	}
	if !strings.Contains(err.Error(), "invalid status") {
		t.Errorf("expected 'invalid status' error, got %v", err)
	}
}

func TestCreateThread_DefaultStatus(t *testing.T) {
	fb := newDiscordForumBoard(nil, DiscordForumBoardConfig{
		Enabled:        true,
		ForumChannelID: "F123",
	})
	// Will fail at API call (nil client), but should pass validation.
	_, err := fb.CreateThread("Title", "Body", "")
	// Will fail because client is nil, but error should be about API, not validation.
	if err != nil && strings.Contains(err.Error(), "invalid status") {
		t.Error("empty status should default to backlog, not be rejected")
	}
}

// --- SetStatus Validation ---

func TestSetStatus_EmptyThreadID(t *testing.T) {
	fb := newDiscordForumBoard(nil, DiscordForumBoardConfig{})
	err := fb.SetStatus("", "done")
	if err == nil {
		t.Error("expected error for empty thread ID")
	}
}

func TestSetStatus_InvalidStatus(t *testing.T) {
	fb := newDiscordForumBoard(nil, DiscordForumBoardConfig{})
	err := fb.SetStatus("T123", "invalid")
	if err == nil {
		t.Error("expected error for invalid status")
	}
}

// --- HandleAssign Validation ---

func TestHandleAssign_EmptyThreadID(t *testing.T) {
	fb := newDiscordForumBoard(nil, DiscordForumBoardConfig{})
	err := fb.HandleAssign("", "G1", "ruri")
	if err == nil {
		t.Error("expected error for empty thread ID")
	}
}

func TestHandleAssign_EmptyRole(t *testing.T) {
	fb := newDiscordForumBoard(nil, DiscordForumBoardConfig{})
	err := fb.HandleAssign("T123", "G1", "")
	if err == nil {
		t.Error("expected error for empty role")
	}
}

// --- from discord_reactions_test.go ---

// --- P14.3: Lifecycle Reactions Tests ---

// --- Default Emoji Map ---

func TestDefaultReactionEmojis(t *testing.T) {
	emojis := discord.DefaultReactionEmojis()

	// Must have all 5 phases.
	phases := discord.ValidReactionPhases()
	for _, phase := range phases {
		if emoji, ok := emojis[phase]; !ok || emoji == "" {
			t.Errorf("missing default emoji for phase %q", phase)
		}
	}

	// Verify specific defaults.
	if emojis[discord.ReactionPhaseQueued] != "\u23F3" {
		t.Errorf("expected hourglass for queued, got %q", emojis[discord.ReactionPhaseQueued])
	}
	if emojis[discord.ReactionPhaseDone] != "\u2705" {
		t.Errorf("expected check mark for done, got %q", emojis[discord.ReactionPhaseDone])
	}
	if emojis[discord.ReactionPhaseError] != "\u274C" {
		t.Errorf("expected cross mark for error, got %q", emojis[discord.ReactionPhaseError])
	}
}

// --- Reaction Manager Creation ---

func TestNewDiscordReactionManager(t *testing.T) {
	rm := discord.NewReactionManager(nil, nil)
	if rm == nil {
		t.Fatal("expected non-nil reaction manager")
	}
}

func TestNewDiscordReactionManager_WithOverrides(t *testing.T) {
	overrides := map[string]string{
		"queued": "\U0001F4E5", // inbox tray
	}
	rm := discord.NewReactionManager(nil, overrides)
	if rm.EmojiForPhase("queued") != "\U0001F4E5" {
		t.Errorf("expected override emoji, got %q", rm.EmojiForPhase("queued"))
	}
}

// --- Emoji For Phase ---

func TestEmojiForPhase_Default(t *testing.T) {
	rm := discord.NewReactionManager(nil, nil)

	tests := []struct {
		phase    string
		expected string
	}{
		{discord.ReactionPhaseQueued, "\u23F3"},
		{discord.ReactionPhaseThinking, "\U0001F914"},
		{discord.ReactionPhaseTool, "\U0001F527"},
		{discord.ReactionPhaseDone, "\u2705"},
		{discord.ReactionPhaseError, "\u274C"},
	}
	for _, tt := range tests {
		got := rm.EmojiForPhase(tt.phase)
		if got != tt.expected {
			t.Errorf("EmojiForPhase(%q) = %q, want %q", tt.phase, got, tt.expected)
		}
	}
}

func TestEmojiForPhase_Override(t *testing.T) {
	overrides := map[string]string{
		"queued": "\U0001F4E5", // inbox tray
		"done":   "\U0001F389", // party popper
	}
	rm := discord.NewReactionManager(nil, overrides)

	if got := rm.EmojiForPhase("queued"); got != "\U0001F4E5" {
		t.Errorf("expected override for queued, got %q", got)
	}
	if got := rm.EmojiForPhase("done"); got != "\U0001F389" {
		t.Errorf("expected override for done, got %q", got)
	}

	// Non-overridden phases fall back to default.
	if got := rm.EmojiForPhase("thinking"); got != "\U0001F914" {
		t.Errorf("expected default for thinking, got %q", got)
	}
}

func TestEmojiForPhase_UnknownPhase(t *testing.T) {
	rm := discord.NewReactionManager(nil, nil)
	got := rm.EmojiForPhase("unknown_phase")
	if got != "" {
		t.Errorf("expected empty for unknown phase, got %q", got)
	}
}

func TestEmojiForPhase_EmptyOverride(t *testing.T) {
	overrides := map[string]string{
		"queued": "",
	}
	rm := discord.NewReactionManager(nil, overrides)
	got := rm.EmojiForPhase("queued")
	if got != "\u23F3" {
		t.Errorf("expected default for empty override, got %q", got)
	}
}

// --- Phase Tracking ---

func TestSetPhase_TracksCurrentPhase(t *testing.T) {
	rm := discord.NewReactionManager(nil, nil)

	rm.SetPhase("C1", "M1", discord.ReactionPhaseQueued)

	got := rm.GetCurrentPhase("C1", "M1")
	if got != discord.ReactionPhaseQueued {
		t.Errorf("expected phase %q, got %q", discord.ReactionPhaseQueued, got)
	}
}

func TestSetPhase_TransitionUpdatesPhase(t *testing.T) {
	rm := discord.NewReactionManager(nil, nil)

	rm.SetPhase("C1", "M1", discord.ReactionPhaseQueued)
	rm.SetPhase("C1", "M1", discord.ReactionPhaseThinking)

	got := rm.GetCurrentPhase("C1", "M1")
	if got != discord.ReactionPhaseThinking {
		t.Errorf("expected phase %q after transition, got %q", discord.ReactionPhaseThinking, got)
	}
}

func TestSetPhase_IgnoresEmptyArgs(t *testing.T) {
	rm := discord.NewReactionManager(nil, nil)

	rm.SetPhase("", "M1", discord.ReactionPhaseQueued)
	rm.SetPhase("C1", "", discord.ReactionPhaseQueued)
	rm.SetPhase("C1", "M1", "")

	if got := rm.GetCurrentPhase("", "M1"); got != "" {
		t.Errorf("expected empty for empty channelID, got %q", got)
	}
	if got := rm.GetCurrentPhase("C1", ""); got != "" {
		t.Errorf("expected empty for empty messageID, got %q", got)
	}
}

func TestSetPhase_UnknownPhaseIgnored(t *testing.T) {
	rm := discord.NewReactionManager(nil, nil)

	rm.SetPhase("C1", "M1", "nonexistent_phase")
	got := rm.GetCurrentPhase("C1", "M1")
	if got != "" {
		t.Errorf("expected empty for unknown phase, got %q", got)
	}
}

// --- Clear Phase ---

func TestClearPhase(t *testing.T) {
	rm := discord.NewReactionManager(nil, nil)

	rm.SetPhase("C1", "M1", discord.ReactionPhaseQueued)
	rm.ClearPhase("C1", "M1")

	got := rm.GetCurrentPhase("C1", "M1")
	if got != "" {
		t.Errorf("expected empty after ClearPhase, got %q", got)
	}
}

func TestClearPhase_NonExistent(t *testing.T) {
	rm := discord.NewReactionManager(nil, nil)
	rm.ClearPhase("C999", "M999")
}

// --- Convenience Methods ---

func TestReactQueued(t *testing.T) {
	rm := discord.NewReactionManager(nil, nil)
	rm.ReactQueued("C1", "M1")
	if got := rm.GetCurrentPhase("C1", "M1"); got != discord.ReactionPhaseQueued {
		t.Errorf("expected queued, got %q", got)
	}
}

func TestReactDone_ClearsTracking(t *testing.T) {
	rm := discord.NewReactionManager(nil, nil)
	rm.SetPhase("C1", "M1", discord.ReactionPhaseThinking)
	rm.ReactDone("C1", "M1")
	if got := rm.GetCurrentPhase("C1", "M1"); got != "" {
		t.Errorf("expected empty after ReactDone, got %q", got)
	}
}

func TestReactError_ClearsTracking(t *testing.T) {
	rm := discord.NewReactionManager(nil, nil)
	rm.SetPhase("C1", "M1", discord.ReactionPhaseThinking)
	rm.ReactError("C1", "M1")
	if got := rm.GetCurrentPhase("C1", "M1"); got != "" {
		t.Errorf("expected empty after ReactError, got %q", got)
	}
}

// --- Full Lifecycle ---

func TestReactionLifecycle_FullTransition(t *testing.T) {
	rm := discord.NewReactionManager(nil, nil)

	rm.SetPhase("C1", "M1", discord.ReactionPhaseQueued)
	if got := rm.GetCurrentPhase("C1", "M1"); got != discord.ReactionPhaseQueued {
		t.Fatalf("step 1: expected queued, got %q", got)
	}

	rm.SetPhase("C1", "M1", discord.ReactionPhaseThinking)
	if got := rm.GetCurrentPhase("C1", "M1"); got != discord.ReactionPhaseThinking {
		t.Fatalf("step 2: expected thinking, got %q", got)
	}

	rm.SetPhase("C1", "M1", discord.ReactionPhaseTool)
	if got := rm.GetCurrentPhase("C1", "M1"); got != discord.ReactionPhaseTool {
		t.Fatalf("step 3: expected tool, got %q", got)
	}

	rm.ReactDone("C1", "M1")
	if got := rm.GetCurrentPhase("C1", "M1"); got != "" {
		t.Fatalf("step 4: expected empty after done, got %q", got)
	}
}

func TestReactionLifecycle_ErrorPath(t *testing.T) {
	rm := discord.NewReactionManager(nil, nil)

	rm.SetPhase("C1", "M1", discord.ReactionPhaseQueued)
	rm.SetPhase("C1", "M1", discord.ReactionPhaseThinking)
	rm.ReactError("C1", "M1")

	if got := rm.GetCurrentPhase("C1", "M1"); got != "" {
		t.Errorf("expected empty after error, got %q", got)
	}
}

// --- Multiple Messages ---

func TestReactionManager_MultipleMessages(t *testing.T) {
	rm := discord.NewReactionManager(nil, nil)

	rm.SetPhase("C1", "M1", discord.ReactionPhaseQueued)
	rm.SetPhase("C1", "M2", discord.ReactionPhaseThinking)
	rm.SetPhase("C2", "M3", discord.ReactionPhaseTool)

	if got := rm.GetCurrentPhase("C1", "M1"); got != discord.ReactionPhaseQueued {
		t.Errorf("M1: expected queued, got %q", got)
	}
	if got := rm.GetCurrentPhase("C1", "M2"); got != discord.ReactionPhaseThinking {
		t.Errorf("M2: expected thinking, got %q", got)
	}
	if got := rm.GetCurrentPhase("C2", "M3"); got != discord.ReactionPhaseTool {
		t.Errorf("M3: expected tool, got %q", got)
	}
}

// --- Valid Phases ---

func TestValidReactionPhases(t *testing.T) {
	phases := discord.ValidReactionPhases()
	if len(phases) != 5 {
		t.Errorf("expected 5 phases, got %d", len(phases))
	}

	expected := []string{"queued", "thinking", "tool", "done", "error"}
	for i, p := range expected {
		if phases[i] != p {
			t.Errorf("phase[%d] = %q, want %q", i, phases[i], p)
		}
	}
}

// --- Config Parsing ---

func TestDiscordReactionsConfigParse(t *testing.T) {
	raw := `{"enabled":true,"emojis":{"queued":"\u2b50","done":"\ud83c\udf89"}}`
	var cfg DiscordReactionsConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled {
		t.Error("expected enabled")
	}
	if cfg.Emojis == nil {
		t.Fatal("expected emojis map")
	}
	if cfg.Emojis["queued"] == "" {
		t.Error("expected queued emoji override")
	}
}

func TestDiscordReactionsConfigParse_Disabled(t *testing.T) {
	raw := `{}`
	var cfg DiscordReactionsConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Enabled {
		t.Error("expected disabled by default")
	}
	if cfg.Emojis != nil {
		t.Error("expected nil emojis by default")
	}
}

// --- Phase Constants ---

func TestReactionPhaseConstants(t *testing.T) {
	if discord.ReactionPhaseQueued != "queued" {
		t.Errorf("expected 'queued', got %q", discord.ReactionPhaseQueued)
	}
	if discord.ReactionPhaseThinking != "thinking" {
		t.Errorf("expected 'thinking', got %q", discord.ReactionPhaseThinking)
	}
	if discord.ReactionPhaseTool != "tool" {
		t.Errorf("expected 'tool', got %q", discord.ReactionPhaseTool)
	}
	if discord.ReactionPhaseDone != "done" {
		t.Errorf("expected 'done', got %q", discord.ReactionPhaseDone)
	}
	if discord.ReactionPhaseError != "error" {
		t.Errorf("expected 'error', got %q", discord.ReactionPhaseError)
	}
}

// --- Same Phase No-Op ---

func TestSetPhase_SamePhaseNoRemove(t *testing.T) {
	rm := discord.NewReactionManager(nil, nil)

	rm.SetPhase("C1", "M1", discord.ReactionPhaseQueued)
	rm.SetPhase("C1", "M1", discord.ReactionPhaseQueued)

	got := rm.GetCurrentPhase("C1", "M1")
	if got != discord.ReactionPhaseQueued {
		t.Errorf("expected queued after re-set, got %q", got)
	}
}

// --- Helper: use strings.Contains for substring checks ---

func TestReactionKeyContainsSeparator(t *testing.T) {
	// reactionKey is unexported in internal/discord, test via SetPhase+GetCurrentPhase
	rm := discord.NewReactionManager(nil, nil)
	rm.SetPhase("C123", "M456", discord.ReactionPhaseQueued)
	if got := rm.GetCurrentPhase("C123", "M456"); got != discord.ReactionPhaseQueued {
		t.Error("expected phase tracking to work with specific channel/message IDs")
	}
	_ = strings.Contains("C123:M456", ":")
}

// --- P14.2: Thread-Bound Sessions Tests ---


// --- Session Key Derivation ---

func TestThreadSessionKey(t *testing.T) {
	tests := []struct {
		role, guildID, threadID string
		expected                string
	}{
		{"ruri", "G123", "T456", "agent:ruri:discord:thread:G123:T456"},
		{"hisui", "G123", "T789", "agent:hisui:discord:thread:G123:T789"},
		{"kokuyou", "G999", "T012", "agent:kokuyou:discord:thread:G999:T012"},
		{"kohaku", "", "T111", "agent:kohaku:discord:thread::T111"},
	}
	for _, tt := range tests {
		got := threadSessionKey(tt.role, tt.guildID, tt.threadID)
		if got != tt.expected {
			t.Errorf("threadSessionKey(%q, %q, %q) = %q, want %q",
				tt.role, tt.guildID, tt.threadID, got, tt.expected)
		}
	}
}

// --- Thread Binding: Bind, Get, Unbind ---

func TestThreadBindingStore_BindAndGet(t *testing.T) {
	store := newThreadBindingStore()

	sessionID := store.bind("G123", "T456", "ruri", 24*time.Hour)
	if sessionID != "agent:ruri:discord:thread:G123:T456" {
		t.Errorf("unexpected session ID: %s", sessionID)
	}

	b := store.get("G123", "T456")
	if b == nil {
		t.Fatal("expected binding, got nil")
	}
	if b.Agent != "ruri" {
		t.Errorf("expected role ruri, got %q", b.Agent)
	}
	if b.GuildID != "G123" {
		t.Errorf("expected guildID G123, got %q", b.GuildID)
	}
	if b.ThreadID != "T456" {
		t.Errorf("expected threadID T456, got %q", b.ThreadID)
	}
	if b.SessionID != sessionID {
		t.Errorf("expected sessionID %q, got %q", sessionID, b.SessionID)
	}
}

func TestThreadBindingStore_GetNotFound(t *testing.T) {
	store := newThreadBindingStore()

	b := store.get("G999", "T999")
	if b != nil {
		t.Errorf("expected nil for unbound thread, got %+v", b)
	}
}

func TestThreadBindingStore_Unbind(t *testing.T) {
	store := newThreadBindingStore()

	store.bind("G123", "T456", "hisui", 24*time.Hour)
	if store.get("G123", "T456") == nil {
		t.Fatal("expected binding after bind")
	}

	store.unbind("G123", "T456")
	if store.get("G123", "T456") != nil {
		t.Error("expected nil after unbind")
	}
}

func TestThreadBindingStore_UnbindNonExistent(t *testing.T) {
	store := newThreadBindingStore()
	// Should not panic.
	store.unbind("G999", "T999")
}

// --- TTL Expiration ---

func TestThreadBindingStore_TTLExpiration(t *testing.T) {
	store := newThreadBindingStore()

	// Bind with a very short TTL.
	store.bind("G123", "T456", "kokuyou", 1*time.Millisecond)

	// Wait for expiration.
	time.Sleep(5 * time.Millisecond)

	b := store.get("G123", "T456")
	if b != nil {
		t.Errorf("expected nil for expired binding, got %+v", b)
	}
}

func TestThreadBindingStore_TTLNotYetExpired(t *testing.T) {
	store := newThreadBindingStore()

	store.bind("G123", "T456", "ruri", 1*time.Hour)

	b := store.get("G123", "T456")
	if b == nil {
		t.Fatal("expected binding before TTL expires")
	}
	if b.Agent != "ruri" {
		t.Errorf("expected role ruri, got %q", b.Agent)
	}
}

// --- Cleanup ---

func TestThreadBindingStore_Cleanup(t *testing.T) {
	store := newThreadBindingStore()

	// Bind two: one expired, one active.
	store.bind("G1", "T1", "ruri", 1*time.Millisecond)
	store.bind("G2", "T2", "hisui", 1*time.Hour)

	time.Sleep(5 * time.Millisecond)

	store.cleanup()

	if store.get("G1", "T1") != nil {
		t.Error("expected expired binding to be cleaned up")
	}
	if store.get("G2", "T2") == nil {
		t.Error("expected active binding to survive cleanup")
	}
	if store.count() != 1 {
		t.Errorf("expected 1 active binding, got %d", store.count())
	}
}

func TestThreadBindingStore_CleanupEmpty(t *testing.T) {
	store := newThreadBindingStore()
	// Should not panic on empty store.
	store.cleanup()
	if store.count() != 0 {
		t.Errorf("expected 0, got %d", store.count())
	}
}

// --- Concurrent Access ---

func TestThreadBindingStore_Concurrent(t *testing.T) {
	store := newThreadBindingStore()
	var wg sync.WaitGroup

	// Concurrent binds.
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			gid := "G1"
			tid := fmt.Sprintf("T%d", n)
			store.bind(gid, tid, "ruri", 1*time.Hour)
		}(i)
	}

	// Concurrent reads.
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			store.get("G1", fmt.Sprintf("T%d", n))
		}(i)
	}

	// Concurrent cleanup.
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			store.cleanup()
		}()
	}

	wg.Wait()

	// All 50 should exist (none expired).
	if store.count() != 50 {
		t.Errorf("expected 50 bindings, got %d", store.count())
	}
}

// --- Channel Type Detection ---

func TestIsThreadChannel(t *testing.T) {
	tests := []struct {
		channelType int
		expected    bool
	}{
		{discordChannelTypePublicThread, true},
		{discordChannelTypePrivateThread, true},
		{discordChannelTypeForum, true},
		{0, false},  // guild text
		{1, false},  // DM
		{2, false},  // guild voice
		{5, false},  // guild announcement
		{13, false}, // guild stage voice
	}
	for _, tt := range tests {
		got := isThreadChannel(tt.channelType)
		if got != tt.expected {
			t.Errorf("isThreadChannel(%d) = %v, want %v", tt.channelType, got, tt.expected)
		}
	}
}

// --- Forum Auto-Thread Detection ---

func TestForumChannelDetection(t *testing.T) {
	// Forum channels (type 15) should be treated as threads.
	if !isThreadChannel(discordChannelTypeForum) {
		t.Error("expected forum channel type 15 to be detected as thread")
	}
}

// --- Binding Key ---

func TestThreadBindingKey(t *testing.T) {
	tests := []struct {
		guildID, threadID, expected string
	}{
		{"G123", "T456", "G123:T456"},
		{"", "T456", ":T456"},
		{"G123", "", "G123:"},
	}
	for _, tt := range tests {
		got := threadBindingKey(tt.guildID, tt.threadID)
		if got != tt.expected {
			t.Errorf("threadBindingKey(%q, %q) = %q, want %q",
				tt.guildID, tt.threadID, got, tt.expected)
		}
	}
}

// --- Override Binding ---

func TestThreadBindingStore_OverrideBind(t *testing.T) {
	store := newThreadBindingStore()

	store.bind("G1", "T1", "ruri", 1*time.Hour)
	b := store.get("G1", "T1")
	if b == nil || b.Agent != "ruri" {
		t.Fatal("expected ruri binding")
	}

	// Override with a different role.
	store.bind("G1", "T1", "hisui", 2*time.Hour)
	b = store.get("G1", "T1")
	if b == nil || b.Agent != "hisui" {
		t.Fatal("expected hisui binding after override")
	}
	if b.SessionID != "agent:hisui:discord:thread:G1:T1" {
		t.Errorf("expected updated session ID, got %q", b.SessionID)
	}
}

// --- TTL Config Default ---

func TestThreadBindingsConfigTTL(t *testing.T) {
	// Default (zero value).
	cfg := DiscordThreadBindingsConfig{}
	if cfg.ThreadBindingsTTL() != 24*time.Hour {
		t.Errorf("expected 24h default, got %v", cfg.ThreadBindingsTTL())
	}

	// Custom value.
	cfg = DiscordThreadBindingsConfig{TTLHours: 48}
	if cfg.ThreadBindingsTTL() != 48*time.Hour {
		t.Errorf("expected 48h, got %v", cfg.ThreadBindingsTTL())
	}

	// Negative value defaults to 24h.
	cfg = DiscordThreadBindingsConfig{TTLHours: -1}
	if cfg.ThreadBindingsTTL() != 24*time.Hour {
		t.Errorf("expected 24h for negative, got %v", cfg.ThreadBindingsTTL())
	}
}

// --- Binding Expired Method ---

func TestThreadBinding_Expired(t *testing.T) {
	b := &threadBinding{
		ExpiresAt: time.Now().Add(-1 * time.Hour),
	}
	if !b.expired() {
		t.Error("expected expired for past expiration time")
	}

	b = &threadBinding{
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}
	if b.expired() {
		t.Error("expected not expired for future expiration time")
	}
}

// --- Count ---

func TestThreadBindingStore_Count(t *testing.T) {
	store := newThreadBindingStore()

	if store.count() != 0 {
		t.Errorf("expected 0, got %d", store.count())
	}

	store.bind("G1", "T1", "ruri", 1*time.Hour)
	store.bind("G2", "T2", "hisui", 1*time.Hour)
	store.bind("G3", "T3", "kokuyou", 1*time.Millisecond)

	time.Sleep(5 * time.Millisecond)

	// T3 is expired, so count should be 2.
	if store.count() != 2 {
		t.Errorf("expected 2 active bindings, got %d", store.count())
	}
}

// --- P14.1: Discord Components v2 — Tests ---


// --- Ed25519 Signature Verification ---

func TestDiscordComponentVerifySignature_Valid(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	pubHex := hex.EncodeToString(pub)
	timestamp := "1234567890"
	body := []byte(`{"type":1}`)
	msg := []byte(timestamp + string(body))
	sig := ed25519.Sign(priv, msg)
	sigHex := hex.EncodeToString(sig)

	if !verifyDiscordSignature(pubHex, sigHex, timestamp, body) {
		t.Error("expected valid signature to verify")
	}
}

func TestDiscordComponentVerifySignature_Invalid(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	pubHex := hex.EncodeToString(pub)

	// Use a different key to sign — signature will be invalid.
	_, otherPriv, _ := ed25519.GenerateKey(nil)
	timestamp := "1234567890"
	body := []byte(`{"type":1}`)
	msg := []byte(timestamp + string(body))
	sig := ed25519.Sign(otherPriv, msg)
	sigHex := hex.EncodeToString(sig)

	if verifyDiscordSignature(pubHex, sigHex, timestamp, body) {
		t.Error("expected invalid signature to fail verification")
	}
}

func TestDiscordComponentVerifySignature_BadHex(t *testing.T) {
	if verifyDiscordSignature("not-hex", "also-not-hex", "ts", []byte("body")) {
		t.Error("expected bad hex to fail")
	}
}

func TestDiscordComponentVerifySignature_WrongKeySize(t *testing.T) {
	if verifyDiscordSignature("aabb", "ccdd", "ts", []byte("body")) {
		t.Error("expected wrong key size to fail")
	}
}

// --- PING Interaction → PONG ---

func TestDiscordComponentPingPong(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	pubHex := hex.EncodeToString(pub)

	cfg := &Config{}
	cfg.Discord.PublicKey = pubHex
	db := &DiscordBot{cfg: cfg, api: discord.NewClient("")}

	body := []byte(`{"type":1}`)
	timestamp := "1234567890"
	msg := []byte(timestamp + string(body))
	sig := ed25519.Sign(priv, msg)
	sigHex := hex.EncodeToString(sig)

	req := httptest.NewRequest(http.MethodPost, "/api/discord/interactions", strings.NewReader(string(body)))
	req.Header.Set("X-Signature-Ed25519", sigHex)
	req.Header.Set("X-Signature-Timestamp", timestamp)
	w := httptest.NewRecorder()

	handleDiscordInteraction(db, w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var resp discord.InteractionResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Type != discord.InteractionResponsePong {
		t.Errorf("expected PONG (type 1), got %d", resp.Type)
	}
}

// --- Invalid Signature → 401 ---

func TestDiscordComponentInvalidSignature401(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(nil)
	pubHex := hex.EncodeToString(pub)

	cfg := &Config{}
	cfg.Discord.PublicKey = pubHex
	db := &DiscordBot{cfg: cfg, api: discord.NewClient("")}

	body := []byte(`{"type":1}`)
	req := httptest.NewRequest(http.MethodPost, "/api/discord/interactions", strings.NewReader(string(body)))
	req.Header.Set("X-Signature-Ed25519", "deadbeef")
	req.Header.Set("X-Signature-Timestamp", "1234567890")
	w := httptest.NewRecorder()

	handleDiscordInteraction(db, w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

// --- Button Interaction Routing ---

func TestDiscordComponentButtonRouting(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	pubHex := hex.EncodeToString(pub)

	cfg := &Config{}
	cfg.Discord.PublicKey = pubHex
	interactions := newDiscordInteractionState()

	interactions.register(&pendingInteraction{
		CustomID:  "test_btn",
		CreatedAt: time.Now(),
		Callback:  func(data discord.InteractionData) {},
	})

	db := &DiscordBot{cfg: cfg, api: discord.NewClient(""), interactions: interactions}

	payload := map[string]any{
		"type":    discord.InteractionTypeMessageComponent,
		"id":      "int_1",
		"token":   "tok",
		"version": 1,
		"data":    map[string]any{"custom_id": "test_btn", "component_type": 2},
	}
	body, _ := json.Marshal(payload)
	timestamp := "1234567890"
	msg := []byte(timestamp + string(body))
	sig := ed25519.Sign(priv, msg)
	sigHex := hex.EncodeToString(sig)

	req := httptest.NewRequest(http.MethodPost, "/api/discord/interactions", strings.NewReader(string(body)))
	req.Header.Set("X-Signature-Ed25519", sigHex)
	req.Header.Set("X-Signature-Timestamp", timestamp)
	w := httptest.NewRecorder()

	handleDiscordInteraction(db, w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var resp discord.InteractionResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Type != discord.InteractionResponseDeferredUpdate {
		t.Errorf("expected DEFERRED_UPDATE (type 6), got %d", resp.Type)
	}
}

// --- Select Menu Value Extraction ---

func TestDiscordComponentSelectMenuValues(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	pubHex := hex.EncodeToString(pub)

	cfg := &Config{}
	cfg.Discord.PublicKey = pubHex
	db := &DiscordBot{cfg: cfg, api: discord.NewClient(""), interactions: newDiscordInteractionState()}

	payload := map[string]any{
		"type":    discord.InteractionTypeMessageComponent,
		"id":      "int_2",
		"token":   "tok",
		"version": 1,
		"data":    map[string]any{"custom_id": "agent_select", "values": []string{"ruri"}},
	}
	body, _ := json.Marshal(payload)
	timestamp := "1234567890"
	msg := []byte(timestamp + string(body))
	sig := ed25519.Sign(priv, msg)
	sigHex := hex.EncodeToString(sig)

	req := httptest.NewRequest(http.MethodPost, "/api/discord/interactions", strings.NewReader(string(body)))
	req.Header.Set("X-Signature-Ed25519", sigHex)
	req.Header.Set("X-Signature-Timestamp", timestamp)
	w := httptest.NewRecorder()

	handleDiscordInteraction(db, w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var resp discord.InteractionResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Type != discord.InteractionResponseMessage {
		t.Errorf("expected MESSAGE (type 4), got %d", resp.Type)
	}
	if resp.Data == nil || !strings.Contains(resp.Data.Content, "ruri") {
		t.Errorf("expected response to contain selected agent 'ruri', got %v", resp.Data)
	}
}

// --- Modal Submission Parsing ---

func TestDiscordComponentModalSubmitParsing(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	pubHex := hex.EncodeToString(pub)

	cfg := &Config{}
	cfg.Discord.PublicKey = pubHex
	db := &DiscordBot{cfg: cfg, api: discord.NewClient(""), interactions: newDiscordInteractionState()}

	payload := map[string]any{
		"type":    discord.InteractionTypeModalSubmit,
		"id":      "int_3",
		"token":   "tok",
		"version": 1,
		"data": map[string]any{
			"custom_id": "task_form",
			"components": []any{
				map[string]any{
					"type": discord.ComponentTypeActionRow,
					"components": []any{
						map[string]any{"type": discord.ComponentTypeTextInput, "custom_id": "title", "value": "My Task"},
					},
				},
				map[string]any{
					"type": discord.ComponentTypeActionRow,
					"components": []any{
						map[string]any{"type": discord.ComponentTypeTextInput, "custom_id": "desc", "value": "Do something"},
					},
				},
			},
		},
	}
	body, _ := json.Marshal(payload)
	timestamp := "1234567890"
	msg := []byte(timestamp + string(body))
	sig := ed25519.Sign(priv, msg)
	sigHex := hex.EncodeToString(sig)

	req := httptest.NewRequest(http.MethodPost, "/api/discord/interactions", strings.NewReader(string(body)))
	req.Header.Set("X-Signature-Ed25519", sigHex)
	req.Header.Set("X-Signature-Timestamp", timestamp)
	w := httptest.NewRecorder()

	handleDiscordInteraction(db, w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var resp discord.InteractionResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Data == nil || !strings.Contains(resp.Data.Content, "2 fields") {
		t.Errorf("expected response mentioning 2 fields, got %v", resp.Data)
	}
}

// --- Allowed Users Enforcement ---

func TestDiscordComponentAllowedUsersEnforcement(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	pubHex := hex.EncodeToString(pub)

	cfg := &Config{}
	cfg.Discord.PublicKey = pubHex
	interactions := newDiscordInteractionState()
	interactions.register(&pendingInteraction{
		CustomID:   "restricted_btn",
		CreatedAt:  time.Now(),
		AllowedIDs: []string{"user_allowed"},
		Callback:   func(data discord.InteractionData) {},
	})

	db := &DiscordBot{cfg: cfg, api: discord.NewClient(""), interactions: interactions}

	// Interaction from a disallowed user (no member/user = empty userID).
	payload := map[string]any{
		"type":    discord.InteractionTypeMessageComponent,
		"id":      "int_4",
		"token":   "tok",
		"version": 1,
		"member":  map[string]any{"user": map[string]any{"id": "user_blocked"}},
		"data":    map[string]any{"custom_id": "restricted_btn"},
	}
	body, _ := json.Marshal(payload)
	timestamp := "1234567890"
	msg := []byte(timestamp + string(body))
	sig := ed25519.Sign(priv, msg)
	sigHex := hex.EncodeToString(sig)

	req := httptest.NewRequest(http.MethodPost, "/api/discord/interactions", strings.NewReader(string(body)))
	req.Header.Set("X-Signature-Ed25519", sigHex)
	req.Header.Set("X-Signature-Timestamp", timestamp)
	w := httptest.NewRecorder()

	handleDiscordInteraction(db, w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var resp discord.InteractionResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Data == nil || !strings.Contains(resp.Data.Content, "not allowed") {
		t.Errorf("expected 'not allowed' message, got %v", resp.Data)
	}
}

// --- Component Message Builder JSON ---

func TestDiscordComponentBuilderJSON(t *testing.T) {
	components := []discord.Component{
		discordActionRow(
			discordButton("btn_1", "Click Me", discord.ButtonStylePrimary),
			discordButton("btn_2", "Cancel", discord.ButtonStyleDanger),
		),
		discordActionRow(
			discordSelectMenu("sel_1", "Choose...", []discord.SelectOption{
				{Label: "Option A", Value: "a"},
				{Label: "Option B", Value: "b", Description: "Second option"},
			}),
		),
	}

	data, err := json.Marshal(components)
	if err != nil {
		t.Fatal(err)
	}

	var decoded []map[string]any
	json.Unmarshal(data, &decoded)

	if len(decoded) != 2 {
		t.Fatalf("expected 2 action rows, got %d", len(decoded))
	}

	// First row: buttons.
	row1 := decoded[0]
	if int(row1["type"].(float64)) != discord.ComponentTypeActionRow {
		t.Errorf("expected action row type %d, got %v", discord.ComponentTypeActionRow, row1["type"])
	}
	buttons := row1["components"].([]any)
	if len(buttons) != 2 {
		t.Fatalf("expected 2 buttons, got %d", len(buttons))
	}
	btn1 := buttons[0].(map[string]any)
	if btn1["custom_id"] != "btn_1" {
		t.Errorf("expected custom_id 'btn_1', got %v", btn1["custom_id"])
	}
	if int(btn1["style"].(float64)) != discord.ButtonStylePrimary {
		t.Errorf("expected primary style, got %v", btn1["style"])
	}

	// Second row: select.
	row2 := decoded[1]
	selects := row2["components"].([]any)
	if len(selects) != 1 {
		t.Fatalf("expected 1 select menu, got %d", len(selects))
	}
	sel := selects[0].(map[string]any)
	if sel["custom_id"] != "sel_1" {
		t.Errorf("expected custom_id 'sel_1', got %v", sel["custom_id"])
	}
	opts := sel["options"].([]any)
	if len(opts) != 2 {
		t.Fatalf("expected 2 options, got %d", len(opts))
	}
}

// --- Modal Builder ---

func TestDiscordComponentModalBuilder(t *testing.T) {
	modal := discordBuildModal("form_1", "Enter Details",
		discordTextInput("name", "Your Name", true),
		discordParagraphInput("bio", "Your Bio", false),
	)

	if modal.Type != discord.InteractionResponseModal {
		t.Errorf("expected modal response type %d, got %d", discord.InteractionResponseModal, modal.Type)
	}
	if modal.Data == nil {
		t.Fatal("expected modal data")
	}
	if modal.Data.CustomID != "form_1" {
		t.Errorf("expected custom_id 'form_1', got %q", modal.Data.CustomID)
	}
	if modal.Data.Title != "Enter Details" {
		t.Errorf("expected title 'Enter Details', got %q", modal.Data.Title)
	}
	// Components should be wrapped in action rows.
	if len(modal.Data.Components) != 2 {
		t.Fatalf("expected 2 action rows, got %d", len(modal.Data.Components))
	}
	for i, row := range modal.Data.Components {
		if row.Type != discord.ComponentTypeActionRow {
			t.Errorf("component %d: expected action row, got type %d", i, row.Type)
		}
		if len(row.Components) != 1 {
			t.Errorf("component %d: expected 1 inner component, got %d", i, len(row.Components))
		}
	}
}

// --- Approval Buttons Helper ---

func TestDiscordComponentApprovalButtons(t *testing.T) {
	components := discordApprovalButtons("task123")
	if len(components) != 1 {
		t.Fatalf("expected 1 action row, got %d", len(components))
	}
	row := components[0]
	if row.Type != discord.ComponentTypeActionRow {
		t.Errorf("expected action row type")
	}
	if len(row.Components) != 2 {
		t.Fatalf("expected 2 buttons, got %d", len(row.Components))
	}
	if row.Components[0].CustomID != "approve:task123" {
		t.Errorf("expected approve custom_id, got %q", row.Components[0].CustomID)
	}
	if row.Components[0].Style != discord.ButtonStyleSuccess {
		t.Errorf("expected success style for approve button")
	}
	if row.Components[1].CustomID != "reject:task123" {
		t.Errorf("expected reject custom_id, got %q", row.Components[1].CustomID)
	}
	if row.Components[1].Style != discord.ButtonStyleDanger {
		t.Errorf("expected danger style for reject button")
	}
}

// --- Extract Modal Values ---

func TestDiscordComponentExtractModalValues(t *testing.T) {
	components := []discord.Component{
		{Type: discord.ComponentTypeActionRow, Components: []discord.Component{
			{Type: discord.ComponentTypeTextInput, CustomID: "field1", Value: "hello"},
		}},
		{Type: discord.ComponentTypeActionRow, Components: []discord.Component{
			{Type: discord.ComponentTypeTextInput, CustomID: "field2", Value: "world"},
		}},
	}

	values := extractModalValues(components)
	if len(values) != 2 {
		t.Fatalf("expected 2 values, got %d", len(values))
	}
	if values["field1"] != "hello" {
		t.Errorf("expected field1='hello', got %q", values["field1"])
	}
	if values["field2"] != "world" {
		t.Errorf("expected field2='world', got %q", values["field2"])
	}
}

// --- Missing Signature Headers → 401 ---

func TestDiscordComponentMissingSignatureHeaders(t *testing.T) {
	cfg := &Config{}
	cfg.Discord.PublicKey = "aabbccdd" // doesn't matter, headers missing
	db := &DiscordBot{cfg: cfg, api: discord.NewClient("")}

	body := `{"type":1}`
	req := httptest.NewRequest(http.MethodPost, "/api/discord/interactions", strings.NewReader(body))
	// No signature headers.
	w := httptest.NewRecorder()

	handleDiscordInteraction(db, w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

// --- Link Button Builder ---

func TestDiscordComponentLinkButton(t *testing.T) {
	btn := discordLinkButton("https://example.com", "Visit")
	if btn.Type != discord.ComponentTypeButton {
		t.Errorf("expected button type")
	}
	if btn.Style != discord.ButtonStyleLink {
		t.Errorf("expected link style")
	}
	if btn.URL != "https://example.com" {
		t.Errorf("expected URL, got %q", btn.URL)
	}
	if btn.CustomID != "" {
		t.Errorf("expected empty custom_id for link button, got %q", btn.CustomID)
	}
}

// --- Agent Select Menu Helper ---

func TestDiscordComponentAgentSelectMenu(t *testing.T) {
	components := discordAgentSelectMenu([]string{"ruri", "hisui", "kokuyou", "kohaku"})
	if len(components) != 1 {
		t.Fatalf("expected 1 action row, got %d", len(components))
	}
	row := components[0]
	if len(row.Components) != 1 {
		t.Fatalf("expected 1 select menu, got %d", len(row.Components))
	}
	sel := row.Components[0]
	if sel.Type != discord.ComponentTypeStringSelect {
		t.Errorf("expected string select type")
	}
	if len(sel.Options) != 4 {
		t.Errorf("expected 4 options, got %d", len(sel.Options))
	}
}

// --- No Public Key → 503 ---

func TestDiscordComponentNoPublicKey(t *testing.T) {
	cfg := &Config{}
	// No public key set.
	db := &DiscordBot{cfg: cfg, api: discord.NewClient("")}

	body := `{"type":1}`
	req := httptest.NewRequest(http.MethodPost, "/api/discord/interactions", strings.NewReader(body))
	req.Header.Set("X-Signature-Ed25519", "aabb")
	req.Header.Set("X-Signature-Timestamp", "123")
	w := httptest.NewRecorder()

	handleDiscordInteraction(db, w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

// --- sliceContainsStr helper ---

func TestDiscordComponentContainsStr(t *testing.T) {
	if !sliceContainsStr([]string{"a", "b", "c"}, "b") {
		t.Error("expected true for 'b' in [a,b,c]")
	}
	if sliceContainsStr([]string{"a", "b"}, "c") {
		t.Error("expected false for 'c' in [a,b]")
	}
	if sliceContainsStr(nil, "a") {
		t.Error("expected false for nil slice")
	}
}

// --- interactionUserID ---

func TestDiscordComponentInteractionUserID(t *testing.T) {
	// Guild interaction (member).
	i := &discord.Interaction{
		Member: &struct {
			User discord.User `json:"user"`
		}{User: discord.User{ID: "guild_user"}},
	}
	if got := interactionUserID(i); got != "guild_user" {
		t.Errorf("expected 'guild_user', got %q", got)
	}

	// DM interaction (user).
	i2 := &discord.Interaction{
		User: &discord.User{ID: "dm_user"},
	}
	if got := interactionUserID(i2); got != "dm_user" {
		t.Errorf("expected 'dm_user', got %q", got)
	}

	// Neither.
	i3 := &discord.Interaction{}
	if got := interactionUserID(i3); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}



// --- from presence_test.go ---

// mockPresenceSetter is a test double for PresenceSetter.
type mockPresenceSetter struct {
	name    string
	calls   atomic.Int64
	lastRef string
	mu      sync.Mutex
}

func (m *mockPresenceSetter) SetTyping(ctx context.Context, channelRef string) error {
	m.calls.Add(1)
	m.mu.Lock()
	m.lastRef = channelRef
	m.mu.Unlock()
	return nil
}

func (m *mockPresenceSetter) PresenceName() string { return m.name }

func (m *mockPresenceSetter) callCount() int64 { return m.calls.Load() }

func (m *mockPresenceSetter) getLastRef() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastRef
}

// --- parseSourceChannel Tests ---

func TestParseSourceChannel(t *testing.T) {
	tests := []struct {
		source  string
		wantCh  string
		wantRef string
	}{
		{"", "", ""},
		{"telegram", "telegram", ""},
		{"telegram:12345", "telegram", "12345"},
		{"slack:C123", "slack", "C123"},
		{"discord:456789", "discord", "456789"},
		{"whatsapp:123", "whatsapp", "123"},
		{"chat:telegram:789", "telegram", "789"},
		{"route:slack:C456", "slack", "C456"},
		{"chat:discord:chan:extra", "discord", "chan:extra"},
	}

	for _, tt := range tests {
		ch, ref := parseSourceChannel(tt.source)
		if ch != tt.wantCh || ref != tt.wantRef {
			t.Errorf("parseSourceChannel(%q) = (%q, %q), want (%q, %q)",
				tt.source, ch, ref, tt.wantCh, tt.wantRef)
		}
	}
}

// --- presenceManager Lifecycle Tests ---

func TestPresenceManagerStartStop(t *testing.T) {
	pm := newPresenceManager()
	mock := &mockPresenceSetter{name: "telegram"}
	pm.RegisterSetter("telegram", mock)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pm.StartTyping(ctx, "telegram:12345")

	// Wait a bit for the first typing call.
	time.Sleep(100 * time.Millisecond)

	if mock.callCount() < 1 {
		t.Fatalf("expected at least 1 typing call, got %d", mock.callCount())
	}
	if mock.getLastRef() != "12345" {
		t.Errorf("expected lastRef=12345, got %q", mock.getLastRef())
	}

	pm.StopTyping("telegram:12345")

	// Verify the loop stopped by checking call count doesn't increase.
	countAfterStop := mock.callCount()
	time.Sleep(150 * time.Millisecond)
	if mock.callCount() > countAfterStop+1 {
		t.Errorf("typing loop did not stop: calls went from %d to %d",
			countAfterStop, mock.callCount())
	}
}

func TestPresenceManagerUnknownChannel(t *testing.T) {
	pm := newPresenceManager()

	ctx := context.Background()

	// Should not panic for unknown channels.
	pm.StartTyping(ctx, "unknown:123")
	pm.StopTyping("unknown:123")
}

func TestPresenceManagerEmptySource(t *testing.T) {
	pm := newPresenceManager()

	ctx := context.Background()

	// Should not panic for empty source.
	pm.StartTyping(ctx, "")
	pm.StopTyping("")
}

func TestPresenceManagerNoRef(t *testing.T) {
	pm := newPresenceManager()
	mock := &mockPresenceSetter{name: "telegram"}
	pm.RegisterSetter("telegram", mock)

	ctx := context.Background()

	// Source without ref should not start typing.
	pm.StartTyping(ctx, "telegram")
	time.Sleep(50 * time.Millisecond)

	if mock.callCount() != 0 {
		t.Errorf("expected 0 typing calls for source without ref, got %d", mock.callCount())
	}
}

func TestPresenceManagerChatPrefix(t *testing.T) {
	pm := newPresenceManager()
	mock := &mockPresenceSetter{name: "discord"}
	pm.RegisterSetter("discord", mock)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pm.StartTyping(ctx, "chat:discord:789")

	time.Sleep(100 * time.Millisecond)

	if mock.callCount() < 1 {
		t.Fatalf("expected at least 1 typing call for chat:discord:789, got %d", mock.callCount())
	}
	if mock.getLastRef() != "789" {
		t.Errorf("expected lastRef=789, got %q", mock.getLastRef())
	}

	pm.StopTyping("chat:discord:789")
}

func TestPresenceManagerConcurrentSessions(t *testing.T) {
	pm := newPresenceManager()
	mockTG := &mockPresenceSetter{name: "telegram"}
	mockDC := &mockPresenceSetter{name: "discord"}
	pm.RegisterSetter("telegram", mockTG)
	pm.RegisterSetter("discord", mockDC)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start typing in both channels simultaneously.
	pm.StartTyping(ctx, "telegram:111")
	pm.StartTyping(ctx, "discord:222")

	time.Sleep(100 * time.Millisecond)

	if mockTG.callCount() < 1 {
		t.Errorf("expected telegram typing calls, got %d", mockTG.callCount())
	}
	if mockDC.callCount() < 1 {
		t.Errorf("expected discord typing calls, got %d", mockDC.callCount())
	}

	// Stop both.
	pm.StopTyping("telegram:111")
	pm.StopTyping("discord:222")

	time.Sleep(100 * time.Millisecond)

	// Verify both active maps are clean.
	pm.mu.RLock()
	activeCount := len(pm.active)
	pm.mu.RUnlock()

	if activeCount != 0 {
		t.Errorf("expected 0 active entries after stop, got %d", activeCount)
	}
}

func TestPresenceManagerDoubleStart(t *testing.T) {
	pm := newPresenceManager()
	mock := &mockPresenceSetter{name: "slack"}
	pm.RegisterSetter("slack", mock)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Starting typing twice for the same source should cancel the first loop.
	pm.StartTyping(ctx, "slack:C123")
	time.Sleep(50 * time.Millisecond)
	pm.StartTyping(ctx, "slack:C123")
	time.Sleep(50 * time.Millisecond)

	pm.StopTyping("slack:C123")

	// Should have exactly one active entry removed.
	pm.mu.RLock()
	activeCount := len(pm.active)
	pm.mu.RUnlock()

	if activeCount != 0 {
		t.Errorf("expected 0 active entries, got %d", activeCount)
	}
}

func TestPresenceManagerContextCancellation(t *testing.T) {
	pm := newPresenceManager()
	mock := &mockPresenceSetter{name: "telegram"}
	pm.RegisterSetter("telegram", mock)

	ctx, cancel := context.WithCancel(context.Background())

	pm.StartTyping(ctx, "telegram:999")
	time.Sleep(50 * time.Millisecond)

	// Cancel context should stop the loop.
	cancel()
	time.Sleep(100 * time.Millisecond)

	countAfterCancel := mock.callCount()
	time.Sleep(150 * time.Millisecond)

	if mock.callCount() > countAfterCancel+1 {
		t.Errorf("typing loop did not stop after context cancel")
	}
}

// --- archiveStaleSession ---

func TestArchiveStaleSession(t *testing.T) {
	skipIfNoSQLite(t)

	ctx := context.Background()

	setupDB := func(t *testing.T) (dbPath string, sessID string) {
		t.Helper()
		dbPath = filepath.Join(t.TempDir(), "test.db")
		if err := initSessionDB(dbPath); err != nil {
			t.Fatalf("initSessionDB: %v", err)
		}
		sessID = "sess-stale-test"
		sql := fmt.Sprintf(
			"INSERT INTO sessions (id, agent, source, status, title, created_at, updated_at) VALUES ('%s', 'test', 'discord', 'active', 'T', datetime('now'), datetime('now'))",
			escapeSQLite(sessID),
		)
		if _, err := db.Query(dbPath, sql); err != nil {
			t.Fatalf("insert session: %v", err)
		}
		return
	}

	tests := []struct {
		name       string
		resultErr  string
		sess       func(dbPath, sessID string) *Session
		wantResult bool
	}{
		{
			name:      "stale error with valid session — archived, returns true",
			resultErr: "No saved session found",
			sess: func(dbPath, sessID string) *Session {
				return &Session{ID: sessID}
			},
			wantResult: true,
		},
		{
			name:      "stale error with nil session — no archive attempt, returns true",
			resultErr: "No saved session found",
			sess:      func(_, _ string) *Session { return nil },
			wantResult: true,
		},
		{
			name:      "unrelated error — returns false",
			resultErr: "provider timeout",
			sess: func(dbPath, sessID string) *Session {
				return &Session{ID: sessID}
			},
			wantResult: false,
		},
		{
			name:      "empty error — returns false",
			resultErr: "",
			sess:      func(_, _ string) *Session { return nil },
			wantResult: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dbPath, sessID := setupDB(t)
			sess := tc.sess(dbPath, sessID)

			got := archiveStaleSession(ctx, dbPath, sess, tc.resultErr)
			if got != tc.wantResult {
				t.Errorf("archiveStaleSession() = %v, want %v", got, tc.wantResult)
			}

			// If stale + valid sess, verify status was updated to archived.
			if tc.wantResult && sess != nil {
				rows, err := db.Query(dbPath, fmt.Sprintf("SELECT status FROM sessions WHERE id='%s'", escapeSQLite(sess.ID)))
				if err != nil {
					t.Fatalf("query status: %v", err)
				}
				if len(rows) == 0 {
					t.Fatal("session row not found")
				}
				got, _ := rows[0]["status"].(string)
				if got != "archived" {
					t.Errorf("session status = %q, want %q", got, "archived")
				}
			}
		})
	}
}

func TestArchiveStaleSession_DBFailure(t *testing.T) {
	skipIfNoSQLite(t)

	ctx := context.Background()
	// Point at a non-existent DB — updateSessionStatus will fail.
	// The function must still return true (stale detected) and not panic.
	dbPath := filepath.Join(t.TempDir(), "nonexistent", "test.db")
	sess := &Session{ID: "ghost-session"}

	got := archiveStaleSession(ctx, dbPath, sess, "No saved session found")
	if !got {
		t.Error("archiveStaleSession() = false, want true (stale still detected even if DB write fails)")
	}
}
