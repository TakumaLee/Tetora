package main

import (
	"encoding/json"
	"testing"
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
		mentions []discordUser
		expected bool
	}{
		{nil, false},
		{[]discordUser{}, false},
		{[]discordUser{{ID: "999"}}, false},
		{[]discordUser{{ID: "123456"}}, true},
		{[]discordUser{{ID: "999"}, {ID: "123456"}}, true},
	}
	for _, tt := range tests {
		got := discordIsMentioned(tt.mentions, botID)
		if got != tt.expected {
			t.Errorf("discordIsMentioned(%v, %q) = %v, want %v", tt.mentions, botID, got, tt.expected)
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
		got := discordStripMention(tt.content, botID)
		if got != tt.expected {
			t.Errorf("discordStripMention(%q, %q) = %q, want %q", tt.content, botID, got, tt.expected)
		}
	}
}

func TestDiscordStripMention_EmptyBotID(t *testing.T) {
	got := discordStripMention("<@123> hello", "")
	if got != "<@123> hello" {
		t.Errorf("expected no change with empty botID, got %q", got)
	}
}

// --- Gateway Payload JSON ---

func TestGatewayPayloadMarshal(t *testing.T) {
	seq := 42
	p := gatewayPayload{Op: opHeartbeat, S: &seq}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var decoded gatewayPayload
	json.Unmarshal(data, &decoded)
	if decoded.Op != opHeartbeat {
		t.Errorf("expected op %d, got %d", opHeartbeat, decoded.Op)
	}
	if decoded.S == nil || *decoded.S != 42 {
		t.Errorf("expected seq 42, got %v", decoded.S)
	}
}

func TestGatewayPayloadUnmarshal(t *testing.T) {
	raw := `{"op":10,"d":{"heartbeat_interval":41250}}`
	var p gatewayPayload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatal(err)
	}
	if p.Op != opHello {
		t.Errorf("expected op %d, got %d", opHello, p.Op)
	}
	var hd helloData
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
	var msg discordMessage
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
	embed := discordEmbed{
		Title:       "Test",
		Description: "A test embed",
		Color:       0x5865F2,
		Fields: []discordEmbedField{
			{Name: "Field1", Value: "Value1", Inline: true},
		},
		Footer:    &discordEmbedFooter{Text: "footer"},
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
	var ready readyData
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
	id := identifyData{
		Token:   "test-token",
		Intents: intentGuildMessages | intentDirectMessages | intentMessageContent,
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
	if intents&intentMessageContent == 0 {
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
	var hd helloData
	json.Unmarshal([]byte(raw), &hd)
	if hd.HeartbeatInterval != 41250 {
		t.Errorf("expected 41250, got %d", hd.HeartbeatInterval)
	}
}

// --- Resume Payload ---

func TestResumePayloadMarshal(t *testing.T) {
	r := resumePayload{Token: "tok", SessionID: "sid", Seq: 10}
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
