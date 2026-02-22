package main

// --- P15.3: Teams Channel ---

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTeamsJWTValidation(t *testing.T) {
	appID := "test-app-id-12345"
	cfg := &Config{
		Teams: TeamsConfig{
			Enabled: true,
			AppID:   appID,
		},
	}
	bot := newTeamsBot(cfg, nil, nil)

	// Helper to build a JWT token from claims.
	buildJWT := func(claims map[string]interface{}) string {
		header := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
		claimsJSON, _ := json.Marshal(claims)
		payload := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(claimsJSON)
		sig := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString([]byte("fake-signature"))
		return header + "." + payload + "." + sig
	}

	now := time.Now().Unix()

	tests := []struct {
		name    string
		auth    string
		wantErr bool
	}{
		{
			name:    "valid token",
			auth:    "Bearer " + buildJWT(map[string]interface{}{"iss": "https://api.botframework.com", "aud": appID, "exp": now + 3600, "nbf": now - 60}),
			wantErr: false,
		},
		{
			name:    "missing authorization header",
			auth:    "",
			wantErr: true,
		},
		{
			name:    "invalid format (no Bearer prefix)",
			auth:    "Basic abc123",
			wantErr: true,
		},
		{
			name:    "invalid JWT structure (not 3 parts)",
			auth:    "Bearer abc.def",
			wantErr: true,
		},
		{
			name:    "expired token",
			auth:    "Bearer " + buildJWT(map[string]interface{}{"iss": "https://api.botframework.com", "aud": appID, "exp": now - 3600}),
			wantErr: true,
		},
		{
			name:    "wrong appId (audience mismatch)",
			auth:    "Bearer " + buildJWT(map[string]interface{}{"iss": "https://api.botframework.com", "aud": "wrong-app-id", "exp": now + 3600}),
			wantErr: true,
		},
		{
			name:    "invalid issuer",
			auth:    "Bearer " + buildJWT(map[string]interface{}{"iss": "https://evil.example.com", "aud": appID, "exp": now + 3600}),
			wantErr: true,
		},
		{
			name:    "valid STS issuer",
			auth:    "Bearer " + buildJWT(map[string]interface{}{"iss": "https://sts.windows.net/tenant-id/", "aud": appID, "exp": now + 3600}),
			wantErr: false,
		},
		{
			name:    "valid login.microsoftonline issuer",
			auth:    "Bearer " + buildJWT(map[string]interface{}{"iss": "https://login.microsoftonline.com/tenant-id/v2.0", "aud": appID, "exp": now + 3600}),
			wantErr: false,
		},
		{
			name:    "not yet valid (nbf in future)",
			auth:    "Bearer " + buildJWT(map[string]interface{}{"iss": "https://api.botframework.com", "aud": appID, "exp": now + 7200, "nbf": now + 3600}),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/teams/webhook", nil)
			if tt.auth != "" {
				req.Header.Set("Authorization", tt.auth)
			}
			err := bot.validateAuth(req)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateAuth() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestTeamsActivityParsing(t *testing.T) {
	tests := []struct {
		name     string
		payload  string
		wantType string
		wantText string
		wantFrom string
	}{
		{
			name: "message activity",
			payload: `{
				"type": "message",
				"id": "act-001",
				"timestamp": "2024-01-15T10:30:00.000Z",
				"text": "Hello Teams Bot",
				"channelId": "msteams",
				"serviceUrl": "https://smba.trafficmanager.net/teams/",
				"from": {"id": "user-123", "name": "Test User"},
				"conversation": {"id": "conv-456", "isGroup": false},
				"recipient": {"id": "bot-789", "name": "Tetora Bot"}
			}`,
			wantType: "message",
			wantText: "Hello Teams Bot",
			wantFrom: "Test User",
		},
		{
			name: "conversationUpdate activity",
			payload: `{
				"type": "conversationUpdate",
				"id": "act-002",
				"channelId": "msteams",
				"serviceUrl": "https://smba.trafficmanager.net/teams/",
				"from": {"id": "user-123", "name": "System"},
				"conversation": {"id": "conv-456"}
			}`,
			wantType: "conversationUpdate",
			wantText: "",
			wantFrom: "System",
		},
		{
			name: "invoke activity",
			payload: `{
				"type": "invoke",
				"id": "act-003",
				"channelId": "msteams",
				"serviceUrl": "https://smba.trafficmanager.net/teams/",
				"from": {"id": "user-123", "name": "Test User"},
				"conversation": {"id": "conv-456"},
				"value": {"action": "submit"}
			}`,
			wantType: "invoke",
			wantText: "",
			wantFrom: "Test User",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var activity teamsActivity
			if err := json.Unmarshal([]byte(tt.payload), &activity); err != nil {
				t.Fatalf("unmarshal failed: %v", err)
			}
			if activity.Type != tt.wantType {
				t.Errorf("type = %q, want %q", activity.Type, tt.wantType)
			}
			if activity.Text != tt.wantText {
				t.Errorf("text = %q, want %q", activity.Text, tt.wantText)
			}
			if activity.From.Name != tt.wantFrom {
				t.Errorf("from.name = %q, want %q", activity.From.Name, tt.wantFrom)
			}
		})
	}
}

func TestTeamsReplyMessageConstruction(t *testing.T) {
	var capturedURL string
	var capturedBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.Path

		// Check auth header.
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			t.Errorf("missing Bearer auth, got %q", auth)
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
		Teams: TeamsConfig{
			Enabled:     true,
			AppID:       "test-app",
			AppPassword: "test-secret",
		},
	}
	bot := newTeamsBot(cfg, nil, nil)
	// Pre-populate token cache to avoid real token request.
	bot.tokenCache.token = "test-bearer-token"
	bot.tokenCache.expiresAt = time.Now().Add(1 * time.Hour)

	err := bot.sendReply(srv.URL, "conv-123", "act-456", "Hello from Tetora!")
	if err != nil {
		t.Fatalf("sendReply() error: %v", err)
	}

	expectedPath := "/v3/conversations/conv-123/activities/act-456"
	if capturedURL != expectedPath {
		t.Errorf("URL path = %q, want %q", capturedURL, expectedPath)
	}

	if capturedBody["type"] != "message" {
		t.Errorf("body.type = %v, want 'message'", capturedBody["type"])
	}
	if capturedBody["text"] != "Hello from Tetora!" {
		t.Errorf("body.text = %v, want 'Hello from Tetora!'", capturedBody["text"])
	}
}

func TestTeamsProactiveMessage(t *testing.T) {
	var capturedURL string
	var capturedBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.Path
		bodyData, _ := io.ReadAll(r.Body)
		json.Unmarshal(bodyData, &capturedBody)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	cfg := &Config{
		Teams: TeamsConfig{
			Enabled:     true,
			AppID:       "test-app",
			AppPassword: "test-secret",
		},
	}
	bot := newTeamsBot(cfg, nil, nil)
	bot.tokenCache.token = "test-bearer-token"
	bot.tokenCache.expiresAt = time.Now().Add(1 * time.Hour)

	err := bot.sendProactive(srv.URL, "conv-789", "Proactive message from Tetora")
	if err != nil {
		t.Fatalf("sendProactive() error: %v", err)
	}

	expectedPath := "/v3/conversations/conv-789/activities"
	if capturedURL != expectedPath {
		t.Errorf("URL path = %q, want %q", capturedURL, expectedPath)
	}

	if capturedBody["type"] != "message" {
		t.Errorf("body.type = %v, want 'message'", capturedBody["type"])
	}
	if capturedBody["text"] != "Proactive message from Tetora" {
		t.Errorf("body.text = %v, want 'Proactive message from Tetora'", capturedBody["text"])
	}
}

func TestTeamsAdaptiveCardGeneration(t *testing.T) {
	card := buildSimpleAdaptiveCard("Task Result", "The task completed successfully.")

	if card["type"] != "AdaptiveCard" {
		t.Errorf("card.type = %v, want 'AdaptiveCard'", card["type"])
	}
	if card["version"] != "1.4" {
		t.Errorf("card.version = %v, want '1.4'", card["version"])
	}

	body, ok := card["body"].([]map[string]interface{})
	if !ok {
		t.Fatal("card.body is not []map[string]interface{}")
	}
	if len(body) != 2 {
		t.Fatalf("card.body length = %d, want 2", len(body))
	}

	// Title block.
	if body[0]["text"] != "Task Result" {
		t.Errorf("body[0].text = %v, want 'Task Result'", body[0]["text"])
	}
	if body[0]["weight"] != "bolder" {
		t.Errorf("body[0].weight = %v, want 'bolder'", body[0]["weight"])
	}

	// Body text.
	if body[1]["text"] != "The task completed successfully." {
		t.Errorf("body[1].text = %v, want 'The task completed successfully.'", body[1]["text"])
	}
	if body[1]["wrap"] != true {
		t.Errorf("body[1].wrap = %v, want true", body[1]["wrap"])
	}
}

func TestTeamsAdaptiveCardWithActions(t *testing.T) {
	actions := []map[string]interface{}{
		buildSubmitAction("Approve", map[string]interface{}{"action": "approve", "taskId": "t-123"}),
		buildSubmitAction("Reject", map[string]interface{}{"action": "reject", "taskId": "t-123"}),
	}

	card := buildAdaptiveCardWithActions("Approval Required", "Please review the task.", actions)

	cardActions, ok := card["actions"].([]map[string]interface{})
	if !ok {
		t.Fatal("card.actions is not []map[string]interface{}")
	}
	if len(cardActions) != 2 {
		t.Fatalf("card.actions length = %d, want 2", len(cardActions))
	}

	if cardActions[0]["type"] != "Action.Submit" {
		t.Errorf("actions[0].type = %v, want 'Action.Submit'", cardActions[0]["type"])
	}
	if cardActions[0]["title"] != "Approve" {
		t.Errorf("actions[0].title = %v, want 'Approve'", cardActions[0]["title"])
	}
	if cardActions[1]["title"] != "Reject" {
		t.Errorf("actions[1].title = %v, want 'Reject'", cardActions[1]["title"])
	}
}

func TestTeamsAdaptiveCardSending(t *testing.T) {
	var capturedBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyData, _ := io.ReadAll(r.Body)
		json.Unmarshal(bodyData, &capturedBody)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	cfg := &Config{
		Teams: TeamsConfig{
			Enabled:     true,
			AppID:       "test-app",
			AppPassword: "test-secret",
		},
	}
	bot := newTeamsBot(cfg, nil, nil)
	bot.tokenCache.token = "test-bearer-token"
	bot.tokenCache.expiresAt = time.Now().Add(1 * time.Hour)

	card := buildSimpleAdaptiveCard("Test", "Card content")
	err := bot.sendAdaptiveCard(srv.URL, "conv-123", card)
	if err != nil {
		t.Fatalf("sendAdaptiveCard() error: %v", err)
	}

	attachments, ok := capturedBody["attachments"].([]interface{})
	if !ok {
		t.Fatal("body.attachments is not []interface{}")
	}
	if len(attachments) != 1 {
		t.Fatalf("attachments length = %d, want 1", len(attachments))
	}

	att := attachments[0].(map[string]interface{})
	if att["contentType"] != "application/vnd.microsoft.card.adaptive" {
		t.Errorf("attachment contentType = %v, want 'application/vnd.microsoft.card.adaptive'", att["contentType"])
	}
}

func TestTeamsTokenRefresh(t *testing.T) {
	tokenCallCount := 0
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenCallCount++

		if r.Method != "POST" {
			t.Errorf("token request method = %q, want POST", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Errorf("content-type = %q, want application/x-www-form-urlencoded", r.Header.Get("Content-Type"))
		}

		bodyData, _ := io.ReadAll(r.Body)
		body := string(bodyData)
		if !strings.Contains(body, "grant_type=client_credentials") {
			t.Errorf("body missing grant_type, got: %s", body)
		}
		if !strings.Contains(body, "client_id=test-app") {
			t.Errorf("body missing client_id, got: %s", body)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": fmt.Sprintf("token-%d", tokenCallCount),
			"expires_in":   3600,
			"token_type":   "Bearer",
		})
	}))
	defer tokenSrv.Close()

	cfg := &Config{
		Teams: TeamsConfig{
			Enabled:     true,
			AppID:       "test-app",
			AppPassword: "test-secret",
		},
	}
	bot := newTeamsBot(cfg, nil, nil)
	bot.tokenURL = tokenSrv.URL

	// First call: should fetch a new token.
	tok1, err := bot.getToken()
	if err != nil {
		t.Fatalf("getToken() error: %v", err)
	}
	if tok1 != "token-1" {
		t.Errorf("token = %q, want 'token-1'", tok1)
	}
	if tokenCallCount != 1 {
		t.Errorf("tokenCallCount = %d, want 1", tokenCallCount)
	}

	// Second call: should return cached token.
	tok2, err := bot.getToken()
	if err != nil {
		t.Fatalf("getToken() error: %v", err)
	}
	if tok2 != "token-1" {
		t.Errorf("token = %q, want 'token-1' (cached)", tok2)
	}
	if tokenCallCount != 1 {
		t.Errorf("tokenCallCount = %d, want 1 (should use cache)", tokenCallCount)
	}

	// Expire the cache and fetch again.
	bot.tokenCache.mu.Lock()
	bot.tokenCache.expiresAt = time.Now().Add(-1 * time.Minute)
	bot.tokenCache.mu.Unlock()

	tok3, err := bot.getToken()
	if err != nil {
		t.Fatalf("getToken() error: %v", err)
	}
	if tok3 != "token-2" {
		t.Errorf("token = %q, want 'token-2' (refreshed)", tok3)
	}
	if tokenCallCount != 2 {
		t.Errorf("tokenCallCount = %d, want 2", tokenCallCount)
	}
}

func TestTeamsWebhookHandlerValidRequest(t *testing.T) {
	now := time.Now().Unix()
	cfg := &Config{
		Teams: TeamsConfig{
			Enabled: true,
			AppID:   "test-app",
		},
	}
	state := newDispatchState()
	sem := make(chan struct{}, 1)
	bot := newTeamsBot(cfg, state, sem)

	// Build a valid JWT.
	claimsJSON, _ := json.Marshal(map[string]interface{}{
		"iss": "https://api.botframework.com",
		"aud": "test-app",
		"exp": now + 3600,
	})
	header := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	payload := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(claimsJSON)
	sig := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString([]byte("fake-sig"))
	token := header + "." + payload + "." + sig

	body := `{
		"type": "conversationUpdate",
		"id": "act-001",
		"channelId": "msteams",
		"serviceUrl": "https://smba.trafficmanager.net/teams/",
		"from": {"id": "user-123", "name": "Test User"},
		"conversation": {"id": "conv-456"}
	}`

	req := httptest.NewRequest("POST", "/api/teams/webhook", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	bot.HandleWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestTeamsWebhookHandlerInvalidAuth(t *testing.T) {
	cfg := &Config{
		Teams: TeamsConfig{
			Enabled: true,
			AppID:   "test-app",
		},
	}
	bot := newTeamsBot(cfg, nil, nil)

	body := `{"type": "message", "text": "hello"}`
	req := httptest.NewRequest("POST", "/api/teams/webhook", strings.NewReader(body))
	// No Authorization header.
	w := httptest.NewRecorder()

	bot.HandleWebhook(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestTeamsWebhookHandlerInvalidBody(t *testing.T) {
	now := time.Now().Unix()
	cfg := &Config{
		Teams: TeamsConfig{
			Enabled: true,
			AppID:   "test-app",
		},
	}
	bot := newTeamsBot(cfg, nil, nil)

	// Build valid JWT.
	claimsJSON, _ := json.Marshal(map[string]interface{}{
		"iss": "https://api.botframework.com",
		"aud": "test-app",
		"exp": now + 3600,
	})
	header := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	payload := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(claimsJSON)
	sig := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString([]byte("fake-sig"))
	token := header + "." + payload + "." + sig

	// Invalid JSON body.
	req := httptest.NewRequest("POST", "/api/teams/webhook", strings.NewReader("{invalid json"))
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	bot.HandleWebhook(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestTeamsWebhookHandlerInvalidMethod(t *testing.T) {
	cfg := &Config{
		Teams: TeamsConfig{
			Enabled: true,
			AppID:   "test-app",
		},
	}
	bot := newTeamsBot(cfg, nil, nil)

	req := httptest.NewRequest("GET", "/api/teams/webhook", nil)
	w := httptest.NewRecorder()

	bot.HandleWebhook(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestTeamsConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     TeamsConfig
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: TeamsConfig{
				Enabled:     true,
				AppID:       "app-id-123",
				AppPassword: "secret-456",
				TenantID:    "tenant-789",
			},
			wantErr: false,
		},
		{
			name: "missing appId",
			cfg: TeamsConfig{
				Enabled:     true,
				AppID:       "",
				AppPassword: "secret-456",
			},
			wantErr: true,
		},
		{
			name: "missing appPassword",
			cfg: TeamsConfig{
				Enabled:     true,
				AppID:       "app-id-123",
				AppPassword: "",
			},
			wantErr: true,
		},
		{
			name: "disabled is ok with missing fields",
			cfg: TeamsConfig{
				Enabled: false,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hasErr := false
			if tt.cfg.Enabled {
				if tt.cfg.AppID == "" || tt.cfg.AppPassword == "" {
					hasErr = true
				}
			}
			if hasErr != tt.wantErr {
				t.Errorf("config validation: hasErr = %v, wantErr = %v", hasErr, tt.wantErr)
			}
		})
	}
}

func TestTeamsConversationReference(t *testing.T) {
	// Test that service URL and conversation ID are properly extracted from activity.
	payload := `{
		"type": "message",
		"id": "act-001",
		"text": "Hello",
		"channelId": "msteams",
		"serviceUrl": "https://smba.trafficmanager.net/amer/",
		"from": {"id": "user-123", "name": "Test User"},
		"conversation": {"id": "19:abc@thread.tacv2", "isGroup": true},
		"recipient": {"id": "bot-456", "name": "Tetora Bot"}
	}`

	var activity teamsActivity
	if err := json.Unmarshal([]byte(payload), &activity); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if activity.ServiceURL != "https://smba.trafficmanager.net/amer/" {
		t.Errorf("serviceUrl = %q, want 'https://smba.trafficmanager.net/amer/'", activity.ServiceURL)
	}
	if activity.Conversation.ID != "19:abc@thread.tacv2" {
		t.Errorf("conversation.id = %q, want '19:abc@thread.tacv2'", activity.Conversation.ID)
	}
	if !activity.Conversation.IsGroup {
		t.Error("conversation.isGroup = false, want true")
	}
}

func TestTeamsNotifier(t *testing.T) {
	var capturedBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyData, _ := io.ReadAll(r.Body)
		json.Unmarshal(bodyData, &capturedBody)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	cfg := &Config{
		Teams: TeamsConfig{
			Enabled:     true,
			AppID:       "test-app",
			AppPassword: "test-secret",
		},
	}
	bot := newTeamsBot(cfg, nil, nil)
	bot.tokenCache.token = "test-bearer-token"
	bot.tokenCache.expiresAt = time.Now().Add(1 * time.Hour)

	notifier := &TeamsNotifier{
		Bot:            bot,
		ServiceURL:     srv.URL,
		ConversationID: "conv-notify-123",
	}

	if notifier.Name() != "teams" {
		t.Errorf("Name() = %q, want %q", notifier.Name(), "teams")
	}

	// Test empty text returns nil.
	if err := notifier.Send(""); err != nil {
		t.Errorf("Send('') error: %v", err)
	}

	// Test sending a notification.
	if err := notifier.Send("Test notification"); err != nil {
		t.Fatalf("Send() error: %v", err)
	}

	if capturedBody["type"] != "message" {
		t.Errorf("body.type = %v, want 'message'", capturedBody["type"])
	}
	if capturedBody["text"] != "Test notification" {
		t.Errorf("body.text = %v, want 'Test notification'", capturedBody["text"])
	}
}

func TestTeamsBotCreation(t *testing.T) {
	cfg := &Config{
		Teams: TeamsConfig{
			Enabled:     true,
			AppID:       "app-id",
			AppPassword: "app-secret",
			TenantID:    "tenant-id",
		},
	}

	state := newDispatchState()
	sem := make(chan struct{}, 1)

	bot := newTeamsBot(cfg, state, sem)
	if bot == nil {
		t.Fatal("newTeamsBot returned nil")
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
	if bot.processed == nil {
		t.Error("bot processed map not initialized")
	}
	if bot.httpClient == nil {
		t.Error("bot httpClient not initialized")
	}

	expectedTokenURL := "https://login.microsoftonline.com/tenant-id/oauth2/v2.0/token"
	if bot.tokenURL != expectedTokenURL {
		t.Errorf("tokenURL = %q, want %q", bot.tokenURL, expectedTokenURL)
	}
}

func TestTeamsRemoveBotMention(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{
			input: "<at>Tetora Bot</at> hello world",
			want:  "hello world",
		},
		{
			input: "hello world",
			want:  "hello world",
		},
		{
			input: "<at>Bot</at> <at>Another</at> command",
			want:  "command",
		},
		{
			input: "<at>Bot</at>",
			want:  "",
		},
		{
			input: "",
			want:  "",
		},
	}

	for _, tt := range tests {
		got := removeBotMention(tt.input)
		if got != tt.want {
			t.Errorf("removeBotMention(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestTeamsBase64URLDecode(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "standard padding",
			input: base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString([]byte("hello")),
			want:  "hello",
		},
		{
			name:  "with padding needed",
			input: base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString([]byte("test data")),
			want:  "test data",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := base64URLDecode(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("base64URLDecode() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if string(got) != tt.want {
				t.Errorf("base64URLDecode() = %q, want %q", string(got), tt.want)
			}
		})
	}
}

func TestTeamsEnsureTrailingSlash(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"https://example.com", "https://example.com/"},
		{"https://example.com/", "https://example.com/"},
		{"", "/"},
	}

	for _, tt := range tests {
		got := ensureTrailingSlash(tt.input)
		if got != tt.want {
			t.Errorf("ensureTrailingSlash(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestTeamsSendReplyMissingParams(t *testing.T) {
	cfg := &Config{
		Teams: TeamsConfig{
			Enabled: true,
			AppID:   "test-app",
		},
	}
	bot := newTeamsBot(cfg, nil, nil)

	// Missing serviceURL.
	err := bot.sendReply("", "conv-123", "act-456", "text")
	if err == nil {
		t.Error("sendReply with empty serviceURL should return error")
	}

	// Missing conversationID.
	err = bot.sendReply("https://example.com", "", "act-456", "text")
	if err == nil {
		t.Error("sendReply with empty conversationID should return error")
	}
}

func TestTeamsSendProactiveMissingParams(t *testing.T) {
	cfg := &Config{
		Teams: TeamsConfig{
			Enabled: true,
			AppID:   "test-app",
		},
	}
	bot := newTeamsBot(cfg, nil, nil)

	// Missing serviceURL.
	err := bot.sendProactive("", "conv-123", "text")
	if err == nil {
		t.Error("sendProactive with empty serviceURL should return error")
	}

	// Missing conversationID.
	err = bot.sendProactive("https://example.com", "", "text")
	if err == nil {
		t.Error("sendProactive with empty conversationID should return error")
	}
}

func TestTeamsGroupConversation(t *testing.T) {
	payload := `{
		"type": "message",
		"id": "act-group-001",
		"text": "<at>Tetora Bot</at> what is the status?",
		"channelId": "msteams",
		"serviceUrl": "https://smba.trafficmanager.net/teams/",
		"from": {"id": "user-group-123", "name": "Group User"},
		"conversation": {"id": "19:abc@thread.tacv2", "isGroup": true},
		"recipient": {"id": "bot-789", "name": "Tetora Bot"}
	}`

	var activity teamsActivity
	if err := json.Unmarshal([]byte(payload), &activity); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if !activity.Conversation.IsGroup {
		t.Error("conversation.isGroup should be true")
	}

	// Test mention removal.
	cleaned := removeBotMention(activity.Text)
	if cleaned != "what is the status?" {
		t.Errorf("cleaned text = %q, want 'what is the status?'", cleaned)
	}
}

func TestTeamsAttachmentParsing(t *testing.T) {
	payload := `{
		"type": "message",
		"id": "act-att-001",
		"channelId": "msteams",
		"serviceUrl": "https://smba.trafficmanager.net/teams/",
		"from": {"id": "user-123", "name": "User"},
		"conversation": {"id": "conv-456"},
		"attachments": [
			{
				"contentType": "application/vnd.microsoft.card.adaptive",
				"content": {"type": "AdaptiveCard", "version": "1.4"}
			},
			{
				"contentType": "image/png",
				"contentUrl": "https://example.com/image.png",
				"name": "screenshot.png"
			}
		]
	}`

	var activity teamsActivity
	if err := json.Unmarshal([]byte(payload), &activity); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if len(activity.Attachments) != 2 {
		t.Fatalf("attachments count = %d, want 2", len(activity.Attachments))
	}

	if activity.Attachments[0].ContentType != "application/vnd.microsoft.card.adaptive" {
		t.Errorf("attachment[0].contentType = %q", activity.Attachments[0].ContentType)
	}
	if activity.Attachments[1].Name != "screenshot.png" {
		t.Errorf("attachment[1].name = %q", activity.Attachments[1].Name)
	}
}
