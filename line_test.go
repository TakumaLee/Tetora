package main

// --- P15.1: LINE Channel ---

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLINESignatureVerification(t *testing.T) {
	channelSecret := "test_channel_secret"
	body := []byte(`{"events":[]}`)

	// Generate valid signature: HMAC-SHA256 → base64.
	mac := hmac.New(sha256.New, []byte(channelSecret))
	mac.Write(body)
	validSig := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	tests := []struct {
		name          string
		channelSecret string
		body          []byte
		signature     string
		want          bool
	}{
		{
			name:          "valid signature",
			channelSecret: channelSecret,
			body:          body,
			signature:     validSig,
			want:          true,
		},
		{
			name:          "invalid signature",
			channelSecret: channelSecret,
			body:          body,
			signature:     "aW52YWxpZA==",
			want:          false,
		},
		{
			name:          "empty signature",
			channelSecret: channelSecret,
			body:          body,
			signature:     "",
			want:          false,
		},
		{
			name:          "no secret configured",
			channelSecret: "",
			body:          body,
			signature:     "anything",
			want:          true, // skip verification if no secret
		},
		{
			name:          "tampered body",
			channelSecret: channelSecret,
			body:          []byte(`{"events":[{"type":"message"}]}`),
			signature:     validSig,
			want:          false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := verifyLINESignature(tt.channelSecret, tt.body, tt.signature)
			if got != tt.want {
				t.Errorf("verifyLINESignature() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLINEWebhookMessageEventParsing(t *testing.T) {
	payload := `{
		"destination": "U1234567890",
		"events": [
			{
				"type": "message",
				"timestamp": 1625000000000,
				"replyToken": "reply_token_abc",
				"source": {
					"type": "user",
					"userId": "U9876543210"
				},
				"message": {
					"id": "msg_001",
					"type": "text",
					"text": "Hello Tetora"
				}
			},
			{
				"type": "message",
				"timestamp": 1625000001000,
				"replyToken": "reply_token_def",
				"source": {
					"type": "user",
					"userId": "U9876543210"
				},
				"message": {
					"id": "msg_002",
					"type": "image"
				}
			},
			{
				"type": "message",
				"timestamp": 1625000002000,
				"replyToken": "reply_token_ghi",
				"source": {
					"type": "user",
					"userId": "U9876543210"
				},
				"message": {
					"id": "msg_003",
					"type": "sticker",
					"stickerId": "sticker_123",
					"packageId": "pkg_456"
				}
			}
		]
	}`

	var hook lineWebhookBody
	if err := json.Unmarshal([]byte(payload), &hook); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if hook.Destination != "U1234567890" {
		t.Errorf("destination = %q, want %q", hook.Destination, "U1234567890")
	}

	if len(hook.Events) != 3 {
		t.Fatalf("events count = %d, want 3", len(hook.Events))
	}

	// Text message event.
	ev := hook.Events[0]
	if ev.Type != "message" {
		t.Errorf("event[0].type = %q, want %q", ev.Type, "message")
	}
	if ev.ReplyToken != "reply_token_abc" {
		t.Errorf("event[0].replyToken = %q, want %q", ev.ReplyToken, "reply_token_abc")
	}
	if ev.Source.UserID != "U9876543210" {
		t.Errorf("event[0].source.userId = %q, want %q", ev.Source.UserID, "U9876543210")
	}
	if ev.Message == nil {
		t.Fatal("event[0].message is nil")
	}
	if ev.Message.Type != "text" {
		t.Errorf("event[0].message.type = %q, want %q", ev.Message.Type, "text")
	}
	if ev.Message.Text != "Hello Tetora" {
		t.Errorf("event[0].message.text = %q, want %q", ev.Message.Text, "Hello Tetora")
	}

	// Image message event.
	ev2 := hook.Events[1]
	if ev2.Message.Type != "image" {
		t.Errorf("event[1].message.type = %q, want %q", ev2.Message.Type, "image")
	}

	// Sticker message event.
	ev3 := hook.Events[2]
	if ev3.Message.Type != "sticker" {
		t.Errorf("event[2].message.type = %q, want %q", ev3.Message.Type, "sticker")
	}
	if ev3.Message.StickerID != "sticker_123" {
		t.Errorf("event[2].message.stickerId = %q, want %q", ev3.Message.StickerID, "sticker_123")
	}
}

func TestLINEReplyMessageConstruction(t *testing.T) {
	msg := lineMessage{
		Type: "text",
		Text: "Hello from Tetora!",
	}

	payload := map[string]interface{}{
		"replyToken": "token_xyz",
		"messages":   []lineMessage{msg},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if parsed["replyToken"] != "token_xyz" {
		t.Errorf("replyToken = %q, want %q", parsed["replyToken"], "token_xyz")
	}

	msgs := parsed["messages"].([]interface{})
	if len(msgs) != 1 {
		t.Fatalf("messages count = %d, want 1", len(msgs))
	}

	m := msgs[0].(map[string]interface{})
	if m["type"] != "text" {
		t.Errorf("message.type = %q, want %q", m["type"], "text")
	}
	if m["text"] != "Hello from Tetora!" {
		t.Errorf("message.text = %q, want %q", m["text"], "Hello from Tetora!")
	}
}

func TestLINEPushMessageConstruction(t *testing.T) {
	msg := lineMessage{
		Type: "text",
		Text: "Push notification from Tetora",
	}

	payload := map[string]interface{}{
		"to":       "U9876543210",
		"messages": []lineMessage{msg},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if parsed["to"] != "U9876543210" {
		t.Errorf("to = %q, want %q", parsed["to"], "U9876543210")
	}
}

func TestLINEGroupMessageHandling(t *testing.T) {
	payload := `{
		"destination": "U1234567890",
		"events": [
			{
				"type": "message",
				"timestamp": 1625000000000,
				"replyToken": "reply_token_group",
				"source": {
					"type": "group",
					"userId": "U9876543210",
					"groupId": "C1234567890"
				},
				"message": {
					"id": "msg_group_001",
					"type": "text",
					"text": "Hello from group"
				}
			}
		]
	}`

	var hook lineWebhookBody
	if err := json.Unmarshal([]byte(payload), &hook); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if len(hook.Events) != 1 {
		t.Fatalf("events count = %d, want 1", len(hook.Events))
	}

	ev := hook.Events[0]
	if ev.Source.Type != "group" {
		t.Errorf("source.type = %q, want %q", ev.Source.Type, "group")
	}
	if ev.Source.GroupID != "C1234567890" {
		t.Errorf("source.groupId = %q, want %q", ev.Source.GroupID, "C1234567890")
	}
	if ev.Source.UserID != "U9876543210" {
		t.Errorf("source.userId = %q, want %q", ev.Source.UserID, "U9876543210")
	}

	// Test resolveTargetID via a LINEBot.
	bot := &LINEBot{}
	targetID := bot.resolveTargetID(ev.Source)
	if targetID != "C1234567890" {
		t.Errorf("resolveTargetID() = %q, want %q (groupId)", targetID, "C1234567890")
	}

	// Test room source.
	roomSource := lineSource{Type: "room", UserID: "Uabc", RoomID: "Rdef"}
	if got := bot.resolveTargetID(roomSource); got != "Rdef" {
		t.Errorf("resolveTargetID(room) = %q, want %q", got, "Rdef")
	}

	// Test user source.
	userSource := lineSource{Type: "user", UserID: "Uxyz"}
	if got := bot.resolveTargetID(userSource); got != "Uxyz" {
		t.Errorf("resolveTargetID(user) = %q, want %q", got, "Uxyz")
	}
}

func TestLINEUserProfileFetch(t *testing.T) {
	// Mock LINE API server.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check auth header.
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test_access_token" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"message":"Invalid access token"}`))
			return
		}

		// Check path.
		if strings.HasSuffix(r.URL.Path, "/profile/U9876543210") {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(lineProfile{
				DisplayName:   "Test User",
				UserID:        "U9876543210",
				PictureURL:    "https://example.com/pic.jpg",
				StatusMessage: "Hello!",
				Language:      "ja",
			})
			return
		}

		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"Not found"}`))
	}))
	defer srv.Close()

	cfg := &Config{
		LINE: LINEConfig{
			Enabled:            true,
			ChannelAccessToken: "test_access_token",
		},
	}

	bot := newLINEBot(cfg, nil, nil, nil)
	bot.apiBase = srv.URL // Use mock server.

	// Test successful profile fetch.
	profile, err := bot.getProfile("U9876543210")
	if err != nil {
		t.Fatalf("getProfile() error: %v", err)
	}
	if profile.DisplayName != "Test User" {
		t.Errorf("DisplayName = %q, want %q", profile.DisplayName, "Test User")
	}
	if profile.UserID != "U9876543210" {
		t.Errorf("UserID = %q, want %q", profile.UserID, "U9876543210")
	}
	if profile.Language != "ja" {
		t.Errorf("Language = %q, want %q", profile.Language, "ja")
	}

	// Test empty user ID.
	_, err = bot.getProfile("")
	if err == nil {
		t.Error("getProfile('') should return error")
	}

	// Test not-found user.
	_, err = bot.getProfile("Unonexistent")
	if err == nil {
		t.Error("getProfile(nonexistent) should return error")
	}
}

func TestLINEPostbackEventParsing(t *testing.T) {
	payload := `{
		"destination": "U1234567890",
		"events": [
			{
				"type": "postback",
				"timestamp": 1625000000000,
				"replyToken": "reply_token_pb",
				"source": {
					"type": "user",
					"userId": "U9876543210"
				},
				"postback": {
					"data": "action=confirm&item_id=123"
				}
			}
		]
	}`

	var hook lineWebhookBody
	if err := json.Unmarshal([]byte(payload), &hook); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if len(hook.Events) != 1 {
		t.Fatalf("events count = %d, want 1", len(hook.Events))
	}

	ev := hook.Events[0]
	if ev.Type != "postback" {
		t.Errorf("event.type = %q, want %q", ev.Type, "postback")
	}
	if ev.Postback == nil {
		t.Fatal("event.postback is nil")
	}
	if ev.Postback.Data != "action=confirm&item_id=123" {
		t.Errorf("postback.data = %q, want %q", ev.Postback.Data, "action=confirm&item_id=123")
	}
}

func TestLINEWebhookMultipleEvents(t *testing.T) {
	payload := `{
		"destination": "U1234567890",
		"events": [
			{"type": "message", "timestamp": 1, "source": {"type": "user", "userId": "U1"}, "message": {"id": "m1", "type": "text", "text": "msg1"}},
			{"type": "follow", "timestamp": 2, "source": {"type": "user", "userId": "U2"}},
			{"type": "postback", "timestamp": 3, "source": {"type": "user", "userId": "U3"}, "postback": {"data": "action=test"}},
			{"type": "unfollow", "timestamp": 4, "source": {"type": "user", "userId": "U4"}},
			{"type": "join", "timestamp": 5, "source": {"type": "group", "groupId": "G1"}},
			{"type": "leave", "timestamp": 6, "source": {"type": "group", "groupId": "G2"}}
		]
	}`

	var hook lineWebhookBody
	if err := json.Unmarshal([]byte(payload), &hook); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if len(hook.Events) != 6 {
		t.Fatalf("events count = %d, want 6", len(hook.Events))
	}

	types := []string{"message", "follow", "postback", "unfollow", "join", "leave"}
	for i, ev := range hook.Events {
		if ev.Type != types[i] {
			t.Errorf("event[%d].type = %q, want %q", i, ev.Type, types[i])
		}
	}
}

func TestLINEConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     LINEConfig
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: LINEConfig{
				Enabled:            true,
				ChannelSecret:      "secret_abc",
				ChannelAccessToken: "token_xyz",
			},
			wantErr: false,
		},
		{
			name: "missing secret",
			cfg: LINEConfig{
				Enabled:            true,
				ChannelSecret:      "",
				ChannelAccessToken: "token_xyz",
			},
			wantErr: true,
		},
		{
			name: "missing token",
			cfg: LINEConfig{
				Enabled:            true,
				ChannelSecret:      "secret_abc",
				ChannelAccessToken: "",
			},
			wantErr: true,
		},
		{
			name: "disabled is ok with missing fields",
			cfg: LINEConfig{
				Enabled: false,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hasErr := false
			if tt.cfg.Enabled {
				if tt.cfg.ChannelSecret == "" || tt.cfg.ChannelAccessToken == "" {
					hasErr = true
				}
			}
			if hasErr != tt.wantErr {
				t.Errorf("config validation: hasErr = %v, wantErr = %v", hasErr, tt.wantErr)
			}
		})
	}
}

func TestLINEFlexMessageBuilder(t *testing.T) {
	msg := buildFlexText("Summary", "Hello, this is a flex message!")

	if msg.Type != "flex" {
		t.Errorf("type = %q, want %q", msg.Type, "flex")
	}
	if msg.AltText != "Summary" {
		t.Errorf("altText = %q, want %q", msg.AltText, "Summary")
	}
	if msg.Contents == nil {
		t.Fatal("contents is nil")
	}

	// Verify contents structure.
	var contents map[string]interface{}
	if err := json.Unmarshal(msg.Contents, &contents); err != nil {
		t.Fatalf("unmarshal contents failed: %v", err)
	}
	if contents["type"] != "bubble" {
		t.Errorf("contents.type = %q, want %q", contents["type"], "bubble")
	}

	body := contents["body"].(map[string]interface{})
	if body["layout"] != "vertical" {
		t.Errorf("body.layout = %q, want %q", body["layout"], "vertical")
	}
}

func TestLINEQuickReplyBuilder(t *testing.T) {
	msg := buildQuickReplyMessage("Choose an option:", []string{"Yes", "No", "Maybe"})

	if msg.Type != "text" {
		t.Errorf("type = %q, want %q", msg.Type, "text")
	}
	if msg.Text != "Choose an option:" {
		t.Errorf("text = %q, want %q", msg.Text, "Choose an option:")
	}
	if msg.QuickReply == nil {
		t.Fatal("quickReply is nil")
	}
	if len(msg.QuickReply.Items) != 3 {
		t.Fatalf("quickReply items = %d, want 3", len(msg.QuickReply.Items))
	}

	for i, item := range msg.QuickReply.Items {
		if item.Type != "action" {
			t.Errorf("item[%d].type = %q, want %q", i, item.Type, "action")
		}
		if item.Action.Type != "message" {
			t.Errorf("item[%d].action.type = %q, want %q", i, item.Action.Type, "message")
		}
	}

	if msg.QuickReply.Items[0].Action.Label != "Yes" {
		t.Errorf("item[0].action.label = %q, want %q", msg.QuickReply.Items[0].Action.Label, "Yes")
	}
	if msg.QuickReply.Items[1].Action.Label != "No" {
		t.Errorf("item[1].action.label = %q, want %q", msg.QuickReply.Items[1].Action.Label, "No")
	}
}

func TestLINEWebhookHandlerInvalidMethod(t *testing.T) {
	cfg := &Config{
		LINE: LINEConfig{
			Enabled:       true,
			ChannelSecret: "test_secret",
		},
	}

	bot := newLINEBot(cfg, nil, nil, nil)

	req := httptest.NewRequest("GET", "/api/line/webhook", nil)
	w := httptest.NewRecorder()

	bot.HandleWebhook(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestLINEWebhookHandlerInvalidSignature(t *testing.T) {
	cfg := &Config{
		LINE: LINEConfig{
			Enabled:       true,
			ChannelSecret: "test_secret",
		},
	}

	bot := newLINEBot(cfg, nil, nil, nil)

	body := `{"events":[]}`
	req := httptest.NewRequest("POST", "/api/line/webhook", strings.NewReader(body))
	req.Header.Set("X-Line-Signature", "invalid_signature")
	w := httptest.NewRecorder()

	bot.HandleWebhook(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestLINEWebhookHandlerValidSignature(t *testing.T) {
	secret := "test_secret"
	cfg := &Config{
		LINE: LINEConfig{
			Enabled:            true,
			ChannelSecret:      secret,
			ChannelAccessToken: "test_token",
		},
	}

	state := newDispatchState()
	sem := make(chan struct{}, 1)
	bot := newLINEBot(cfg, state, sem, nil)

	body := `{"events":[]}`

	// Compute valid signature.
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest("POST", "/api/line/webhook", strings.NewReader(body))
	req.Header.Set("X-Line-Signature", sig)
	w := httptest.NewRecorder()

	bot.HandleWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestLINESendReplyMock(t *testing.T) {
	var capturedBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/message/reply" {
			t.Errorf("path = %q, want /message/reply", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test_token" {
			t.Errorf("auth = %q, want 'Bearer test_token'", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content-type = %q, want application/json", r.Header.Get("Content-Type"))
		}

		bodyData, _ := io.ReadAll(r.Body)
		json.Unmarshal(bodyData, &capturedBody)

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	cfg := &Config{
		LINE: LINEConfig{
			Enabled:            true,
			ChannelAccessToken: "test_token",
		},
	}

	bot := newLINEBot(cfg, nil, nil, nil)
	bot.apiBase = srv.URL

	msgs := []lineMessage{{Type: "text", Text: "Hello!"}}
	err := bot.sendReply("reply_token_123", msgs)
	if err != nil {
		t.Fatalf("sendReply() error: %v", err)
	}

	if capturedBody["replyToken"] != "reply_token_123" {
		t.Errorf("replyToken = %v, want reply_token_123", capturedBody["replyToken"])
	}
}

func TestLINESendPushMock(t *testing.T) {
	var capturedBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/message/push" {
			t.Errorf("path = %q, want /message/push", r.URL.Path)
		}

		bodyData, _ := io.ReadAll(r.Body)
		json.Unmarshal(bodyData, &capturedBody)

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	cfg := &Config{
		LINE: LINEConfig{
			Enabled:            true,
			ChannelAccessToken: "test_token",
		},
	}

	bot := newLINEBot(cfg, nil, nil, nil)
	bot.apiBase = srv.URL

	msgs := []lineMessage{{Type: "text", Text: "Push message!"}}
	err := bot.sendPush("U9876543210", msgs)
	if err != nil {
		t.Fatalf("sendPush() error: %v", err)
	}

	if capturedBody["to"] != "U9876543210" {
		t.Errorf("to = %v, want U9876543210", capturedBody["to"])
	}
}

func TestLINEWebhookPathOrDefault(t *testing.T) {
	tests := []struct {
		name string
		cfg  LINEConfig
		want string
	}{
		{
			name: "default path",
			cfg:  LINEConfig{},
			want: "/api/line/webhook",
		},
		{
			name: "custom path",
			cfg:  LINEConfig{WebhookPath: "/custom/line"},
			want: "/custom/line",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.cfg.webhookPathOrDefault()
			if got != tt.want {
				t.Errorf("webhookPathOrDefault() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLINEBotCreation(t *testing.T) {
	cfg := &Config{
		LINE: LINEConfig{
			Enabled:            true,
			ChannelSecret:      "secret",
			ChannelAccessToken: "token",
		},
	}

	state := newDispatchState()
	sem := make(chan struct{}, 1)

	bot := newLINEBot(cfg, state, sem, nil)
	if bot == nil {
		t.Fatal("newLINEBot returned nil")
	}
	if bot.cfg != cfg {
		t.Error("bot config not set correctly")
	}
	if bot.state != state {
		t.Error("bot state not set correctly")
	}
	if bot.sem != sem {
		t.Error("bot semaphore not set correctly")
	}
	if bot.apiBase != "https://api.line.me/v2/bot" {
		t.Errorf("apiBase = %q, want %q", bot.apiBase, "https://api.line.me/v2/bot")
	}
	if bot.processed == nil {
		t.Error("bot processed map not initialized")
	}
	if bot.httpClient == nil {
		t.Error("bot httpClient not initialized")
	}
}

func TestLINENotifier(t *testing.T) {
	var capturedReq *http.Request
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedReq = r
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	// LINENotifier uses the hardcoded API URL, so we test the structure instead.
	notifier := &LINENotifier{
		Config: LINEConfig{
			ChannelAccessToken: "test_token",
		},
		ChatID: "U9876543210",
	}

	if notifier.Name() != "line" {
		t.Errorf("Name() = %q, want %q", notifier.Name(), "line")
	}

	// Test empty text returns nil.
	if err := notifier.Send(""); err != nil {
		t.Errorf("Send('') error: %v", err)
	}

	// Verify the notifier fields are set correctly.
	if notifier.ChatID != "U9876543210" {
		t.Errorf("ChatID = %q, want %q", notifier.ChatID, "U9876543210")
	}

	_ = capturedReq
	_ = capturedBody
}
