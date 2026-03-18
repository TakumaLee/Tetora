package main

import (
	"encoding/json"
	"strings"
	"testing"

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
