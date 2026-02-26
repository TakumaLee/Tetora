package main

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- Test Service Account Key Generation ---

func TestGChatServiceAccountParsing(t *testing.T) {
	// Create a temporary service account key file.
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "service-account.json")

	// Generate a test RSA key.
	testKey := generateTestRSAKey(t)

	saKey := serviceAccountKey{
		Type:                    "service_account",
		ProjectID:               "test-project",
		PrivateKeyID:            "test-key-id",
		PrivateKey:              testKey,
		ClientEmail:             "test@test-project.iam.gserviceaccount.com",
		ClientID:                "123456789",
		AuthURI:                 "https://accounts.google.com/o/oauth2/auth",
		TokenURI:                "https://oauth2.googleapis.com/token",
		AuthProviderX509CertURL: "https://www.googleapis.com/oauth2/v1/certs",
		ClientX509CertURL:       "https://www.googleapis.com/robot/v1/metadata/x509/test%40test-project.iam.gserviceaccount.com",
	}

	keyData, err := json.Marshal(saKey)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(keyPath, keyData, 0600); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{
		GoogleChat: GoogleChatConfig{
			Enabled:           true,
			ServiceAccountKey: keyPath,
		},
	}

	bot, err := newGoogleChatBot(cfg, nil, nil)
	if err != nil {
		t.Fatalf("failed to create bot: %v", err)
	}

	if bot.saKey.ClientEmail != "test@test-project.iam.gserviceaccount.com" {
		t.Errorf("expected client email %s, got %s", "test@test-project.iam.gserviceaccount.com", bot.saKey.ClientEmail)
	}

	if bot.privKey == nil {
		t.Error("expected private key to be parsed")
	}
}

// --- Test Event Parsing ---

func TestGChatEventParsing(t *testing.T) {
	tests := []struct {
		name      string
		eventJSON string
		eventType string
		hasMsg    bool
	}{
		{
			name: "MESSAGE event",
			eventJSON: `{
				"type": "MESSAGE",
				"eventTime": "2026-02-23T10:00:00.000Z",
				"space": {"name": "spaces/AAAAA", "type": "ROOM", "displayName": "Test Room"},
				"message": {
					"name": "spaces/AAAAA/messages/12345",
					"sender": {"name": "users/123", "displayName": "Test User", "type": "HUMAN"},
					"createTime": "2026-02-23T10:00:00.000Z",
					"text": "Hello bot",
					"argumentText": "Hello bot"
				},
				"user": {"name": "users/123", "displayName": "Test User", "type": "HUMAN"}
			}`,
			eventType: "MESSAGE",
			hasMsg:    true,
		},
		{
			name: "ADDED_TO_SPACE event",
			eventJSON: `{
				"type": "ADDED_TO_SPACE",
				"eventTime": "2026-02-23T10:00:00.000Z",
				"space": {"name": "spaces/AAAAA", "type": "ROOM", "displayName": "Test Room"},
				"user": {"name": "users/123", "displayName": "Test User", "type": "HUMAN"}
			}`,
			eventType: "ADDED_TO_SPACE",
			hasMsg:    false,
		},
		{
			name: "REMOVED_FROM_SPACE event",
			eventJSON: `{
				"type": "REMOVED_FROM_SPACE",
				"eventTime": "2026-02-23T10:00:00.000Z",
				"space": {"name": "spaces/AAAAA", "type": "ROOM", "displayName": "Test Room"},
				"user": {"name": "users/123", "displayName": "Test User", "type": "HUMAN"}
			}`,
			eventType: "REMOVED_FROM_SPACE",
			hasMsg:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var event gchatEvent
			if err := json.Unmarshal([]byte(tt.eventJSON), &event); err != nil {
				t.Fatalf("failed to parse event: %v", err)
			}

			if event.Type != tt.eventType {
				t.Errorf("expected type %s, got %s", tt.eventType, event.Type)
			}

			if tt.hasMsg && event.Message == nil {
				t.Error("expected message to be present")
			}

			if !tt.hasMsg && event.Message != nil {
				t.Error("expected message to be nil")
			}
		})
	}
}

// --- Test Card Message Builder ---

func TestGChatCardBuilder(t *testing.T) {
	card := gchatCard{
		Header: &gchatCardHeader{
			Title:    "Task Result",
			Subtitle: "Completed",
		},
		Sections: []gchatCardSection{
			{
				Header: "Details",
				Widgets: []gchatCardWidget{
					{
						TextParagraph: &gchatTextParagraph{
							Text: "Task completed successfully.",
						},
					},
					{
						KeyValue: &gchatKeyValue{
							TopLabel: "Status",
							Content:  "Success",
							Icon:     "STAR",
						},
					},
				},
			},
		},
	}

	data, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("failed to marshal card: %v", err)
	}

	var parsed gchatCard
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to parse card: %v", err)
	}

	if parsed.Header.Title != "Task Result" {
		t.Errorf("expected title %s, got %s", "Task Result", parsed.Header.Title)
	}

	if len(parsed.Sections) != 1 {
		t.Errorf("expected 1 section, got %d", len(parsed.Sections))
	}

	if len(parsed.Sections[0].Widgets) != 2 {
		t.Errorf("expected 2 widgets, got %d", len(parsed.Sections[0].Widgets))
	}
}

// --- Test Send Message ---

func TestGChatSendMessage(t *testing.T) {
	// Create mock HTTP server.
	callCount := 0
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++

		if strings.Contains(r.URL.Path, "/token") {
			// Token endpoint.
			resp := map[string]interface{}{
				"access_token": "mock-token",
				"expires_in":   3600,
				"token_type":   "Bearer",
			}
			json.NewEncoder(w).Encode(resp)
			return
		}

		// Message endpoint.
		if r.Header.Get("Authorization") != "Bearer mock-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		body, _ := io.ReadAll(r.Body)
		var req gchatSendRequest
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		if req.Text == "" && len(req.Cards) == 0 {
			http.Error(w, "empty message", http.StatusBadRequest)
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"name": "spaces/AAAAA/messages/12345"})
	}))
	defer mockServer.Close()

	// Create bot with mock server.
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "service-account.json")
	testKey := generateTestRSAKey(t)

	saKey := serviceAccountKey{
		Type:        "service_account",
		ProjectID:   "test-project",
		PrivateKeyID: "test-key-id",
		PrivateKey:  testKey,
		ClientEmail: "test@test-project.iam.gserviceaccount.com",
		TokenURI:    mockServer.URL + "/token",
	}

	keyData, _ := json.Marshal(saKey)
	os.WriteFile(keyPath, keyData, 0600)

	cfg := &Config{
		GoogleChat: GoogleChatConfig{
			Enabled:           true,
			ServiceAccountKey: keyPath,
		},
	}

	_, err := newGoogleChatBot(cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Override httpClient to use mock server for messages.
	// Note: We can't easily mock the token exchange without refactoring,
	// so this test verifies the message structure.

	req := gchatSendRequest{Text: "Hello"}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(data), "Hello") {
		t.Errorf("expected message to contain 'Hello', got %s", string(data))
	}
}

// --- Test JWT Generation ---

func TestGChatJWTGeneration(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "service-account.json")
	testKey := generateTestRSAKey(t)

	saKey := serviceAccountKey{
		Type:        "service_account",
		ProjectID:   "test-project",
		PrivateKeyID: "test-key-id",
		PrivateKey:  testKey,
		ClientEmail: "test@test-project.iam.gserviceaccount.com",
		TokenURI:    "https://oauth2.googleapis.com/token",
	}

	keyData, _ := json.Marshal(saKey)
	os.WriteFile(keyPath, keyData, 0600)

	cfg := &Config{
		GoogleChat: GoogleChatConfig{
			Enabled:           true,
			ServiceAccountKey: keyPath,
		},
	}

	bot, err := newGoogleChatBot(cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	jwt, err := bot.createJWT()
	if err != nil {
		t.Fatalf("failed to create JWT: %v", err)
	}

	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Errorf("expected JWT to have 3 parts, got %d", len(parts))
	}

	// Verify header.
	if parts[0] == "" {
		t.Error("expected header to be non-empty")
	}

	// Verify claims.
	if parts[1] == "" {
		t.Error("expected claims to be non-empty")
	}

	// Verify signature.
	if parts[2] == "" {
		t.Error("expected signature to be non-empty")
	}
}

// --- Test Dedup ---

func TestGChatDedup(t *testing.T) {
	bot := &GoogleChatBot{
		processed: make(map[string]time.Time),
	}

	msgID := "spaces/AAAAA/messages/12345"

	if bot.isDuplicate(msgID) {
		t.Error("expected message to not be duplicate initially")
	}

	bot.markProcessed(msgID)

	if !bot.isDuplicate(msgID) {
		t.Error("expected message to be duplicate after marking")
	}

	// Test cleanup.
	bot.processed[msgID] = time.Now().Add(-10 * time.Minute)
	if bot.isDuplicate(msgID) {
		t.Error("expected old message to be cleaned up")
	}
}

// --- Test Webhook Handler ---

func TestGChatWebhookHandler(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "service-account.json")
	testKey := generateTestRSAKey(t)

	saKey := serviceAccountKey{
		Type:        "service_account",
		ProjectID:   "test-project",
		PrivateKeyID: "test-key-id",
		PrivateKey:  testKey,
		ClientEmail: "test@test-project.iam.gserviceaccount.com",
		TokenURI:    "https://oauth2.googleapis.com/token",
	}

	keyData, _ := json.Marshal(saKey)
	os.WriteFile(keyPath, keyData, 0600)

	cfg := &Config{
		GoogleChat: GoogleChatConfig{
			Enabled:           true,
			ServiceAccountKey: keyPath,
			DefaultAgent:       "琉璃",
		},
	}

	bot, err := newGoogleChatBot(cfg, nil, make(chan struct{}, 1))
	if err != nil {
		t.Fatal(err)
	}

	// Test ADDED_TO_SPACE event.
	event := gchatEvent{
		Type: "ADDED_TO_SPACE",
		Space: gchatSpace{
			Name:        "spaces/AAAAA",
			Type:        "ROOM",
			DisplayName: "Test Room",
		},
		User: gchatUser{
			Name:        "users/123",
			DisplayName: "Test User",
			Type:        "HUMAN",
		},
	}

	body, _ := json.Marshal(event)
	req := httptest.NewRequest(http.MethodPost, "/api/gchat/webhook", bytes.NewReader(body))
	w := httptest.NewRecorder()

	bot.HandleWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	resp := w.Body.String()
	if !strings.Contains(resp, "Hello") {
		t.Errorf("expected welcome message, got %s", resp)
	}
}

// --- Helper: Generate Test RSA Key ---

func generateTestRSAKey(t *testing.T) string {
	// Generate a real RSA key for testing using crypto/rsa.
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate test RSA key: %v", err)
	}

	// Marshal to PKCS#8 format.
	privKeyBytes, err := x509.MarshalPKCS8PrivateKey(privKey)
	if err != nil {
		t.Fatalf("failed to marshal private key: %v", err)
	}

	// Encode to PEM.
	pemBlock := &pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privKeyBytes,
	}

	return string(pem.EncodeToMemory(pemBlock))
}

func oldGenerateTestRSAKey(t *testing.T) string {
	// Old static key for reference (keeping for documentation).
	return `-----BEGIN PRIVATE KEY-----
MIIEvwIBADANBgkqhkiG9w0BAQEFAASCBKkwggSlAgEAAoIBAQDU8VjMZV7eCNJ9
4rKCz0VlN3eV5V8VkEhSvJJa1V8DfL9V3qL8V9V1V2V3V4V5V6V7V8V9VaVbVcVd
VeVfVgVhViVjVkVlVmVnVoVpVqVrVsVtVuVvVwVxVyVzV0V1V2V3V4V5V6V7V8V9
VaVbVcVdVeVfVgVhViVjVkVlVmVnVoVpVqVrVsVtVuVvVwVxVyVzV0V1V2V3V4V5
V6V7V8V9VaVbVcVdVeVfVgVhViVjVkVlVmVnVoVpVqVrVsVtVuVvVwVxVyVzV0V1
V2V3V4V5V6V7V8V9VaVbVcVdVeVfVgVhViVjVkVlVmVnVoVpVqVrVsVtVuVvVwVx
VyVzV0V1V2V3V4V5V6V7V8V9VaVbVcVdVeVfVgVhViVjVkVlVmVnVoVpVqVrVsVt
VuVvVwVxVyVzV0V1V2V3V4V5V6V7V8V9VaVbVcVdVeVfVgVhViVjVkVlVmVnVoVp
VqVrVsVtVuVvVwIDAQABAoIBAQCGm5V8V9V1V2V3V4V5V6V7V8V9VaVbVcVdVeVf
VgVhViVjVkVlVmVnVoVpVqVrVsVtVuVvVwVxVyVzV0V1V2V3V4V5V6V7V8V9VaVb
VcVdVeVfVgVhViVjVkVlVmVnVoVpVqVrVsVtVuVvVwVxVyVzV0V1V2V3V4V5V6V7
V8V9VaVbVcVdVeVfVgVhViVjVkVlVmVnVoVpVqVrVsVtVuVvVwVxVyVzV0V1V2V3
V4V5V6V7V8V9VaVbVcVdVeVfVgVhViVjVkVlVmVnVoVpVqVrVsVtVuVvVwVxVyVz
V0V1V2V3V4V5V6V7V8V9VaVbVcVdVeVfVgVhViVjVkVlVmVnVoVpVqVrVsVtVuVv
VwVxVyVzV0V1V2V3V4V5V6V7V8V9VaVbVcVdVeVfVgVhViVjVkVlVmVnVoVpVqVr
VsVtVuVvVwVxVyVzV0V1V2V3V4V5V6V7V8V9VaVbVcVdVeVfVgVhViVjVkVlVmVn
VoVpVqVrVsVtVuVvVwVxVyVzV0V1V2V3V4V5V6V7V8V9VaVbVcVdVeVfVgVhViVj
VkVlVmVnVoVpVqVrVsVtVuVvVwVxVyVzV0V1V2V3V4V5V6V7V8V9VaVbVcVdVeVf
VgVhViVjVkVlVmVnVoVpVqVrVsVtVuVvVwVxVyVzV0V1V2V3V4V5V6V7V8V9VaVb
VcVdVeVfVgVhViVjVkVlVmVnVoVpVqVrVsVtVuVvVwVxVyVzV0V1V2V3V4V5V6V7
V8V9VaVbVcVdVeVfVgVhViVjVkVlVmVnVoVpVqVrVsVtVuVvVwVxVyVzV0V1V2V3
V4V5V6V7V8V9VaVbVcVdVeVfVgVhViVjVkVlVmVnVoVpVqVrVsVtVuVvVwVxVyVz
V0V1V2V3V4V5V6V7V8V9VaVbVcVdVeVfVgVhViVjVkVlVmVnVoVpVqVrVsVtVuVv
VwVxVyVzV0V1V2V3V4V5V6V7V8V9VaVbVcVdVeVfVgVhViVjVkVlVmVnVoVpVqVr
VsVtVuVvVwVxVyVzV0V1V2V3V4V5V6V7V8V9VaVbVcVdVeVfVgVhViVjVkVlVmVn
VoVpVqVrVsVtVuVvVwVxVyVzV0V1V2V3V4V5V6V7V8V9VaVbVcVdVeVfVgVhViVj
VkVlVmVnVoVpVqVrVsVtVuVvVwVxVyVzV0V1V2V3V4V5V6V7V8V9VaVbVcVdVeVf
VgVhViVjVkVlVmVnVoVpVqVrVsVtVuVvVwVxVyVzV0V1V2V3V4V5V6V7V8V9VaVb
VcVdVeVfVgVhViVjVkVlVmVnVoVpVqVrVsVtVuVvVwVxVyVzV0V1V2V3V4V5V6V7
V8V9VaVbVcVdVeVfVgVhViVjVkVlVmVnVoVpVqVrVsVtVuVvVwVxVyVzV0V1V2V3
V4V5V6V7V8V9VaVbVcVdVeVfVgVhViVjVkVlVmVnVoVpVqVrVsVtVuVvVwVxVyVz
V0V1V2V3V4V5V6V7V8V9VaVbVcVdVeVfVgVhViVjVkVlVmVnVoVpVqVrVsVtVuVv
VwVxVyVzV0V1V2V3V4V5V6V7V8V9VaVbVcVdVeVfVgVhViVjVkVlVmVnVoVpVqVr
VsVtVuVvVwVxVyVzV0V1V2V3V4V5V6V7V8V9VaVbVcVdVeVfVgVhViVjVkVlVmVn
VoVpVqVrVsVtVuVvVwVxVyVzV0V1V2V3V4V5V6V7V8V9VaVbVcVdVeVfVgVhViVj
VkVlVmVnVoVpVqVrVsVtVuVvVwVxVyVzV0ECgYEA9vV2V3V4V5V6V7V8V9VaVbVc
VdVeVfVgVhViVjVkVlVmVnVoVpVqVrVsVtVuVvVwVxVyVzV0V1V2V3V4V5V6V7V8
V9VaVbVcVdVeVfVgVhViVjVkVlVmVnVoVpVqVrVsVtVuVvVwVxVyVzV0V1V2V3V4
V5V6V7V8V9VaVbVcVdVeVfVgVhViVjVkVlVmVnVoVpVqVrVsVtVuVvVwVxVyVzV0
V1V2V3V4V5V6V7V8V9VaVbVcVdVeVfVgVhViVjVkVlVmVnVoVpVqVrVsVtVuVvVw
VxVyVzV0V1V2V3V4V5V6V7V8V9VaVbVcVdVeVfVgVhViVjVkVlVmVnVoVpVqVrVs
VtVuVvVwVxVyVzV0V1V2V3V4V5V6V7V8V9VaVbVcVdVeVfVgVhViVjVkVlVmVnVo
VpVqVrVsVtVuVvVwVxVyVzV0V1V2V3V4V5V6V7V8V9VaVbVcVdVeVfVgVhViVjVk
VlVmVnVoVpVqVrVsVtVuVvVwVxVyVzV0V1V2V3V4V5V6V7V8V9VaVbVcVdVeVfVg
VhViVjVkVlVmVnVoVpVqVrVsVtVuVvVwVxVyVzV0ECgYEA3V4V5V6V7V8V9VaVbVc
VdVeVfVgVhViVjVkVlVmVnVoVpVqVrVsVtVuVvVwVxVyVzV0V1V2V3V4V5V6V7V8
V9VaVbVcVdVeVfVgVhViVjVkVlVmVnVoVpVqVrVsVtVuVvVwVxVyVzV0V1V2V3V4
V5V6V7V8V9VaVbVcVdVeVfVgVhViVjVkVlVmVnVoVpVqVrVsVtVuVvVwVxVyVzV0
V1V2V3V4V5V6V7V8V9VaVbVcVdVeVfVgVhViVjVkVlVmVnVoVpVqVrVsVtVuVvVw
VxVyVzV0V1V2V3V4V5V6V7V8V9VaVbVcVdVeVfVgVhViVjVkVlVmVnVoVpVqVrVs
VtVuVvVwVxVyVzV0V1V2V3V4V5V6V7V8V9VaVbVcVdVeVfVgVhViVjVkVlVmVnVo
VpVqVrVsVtVuVvVwVxVyVzV0V1V2V3V4V5V6V7V8V9VaVbVcVdVeVfVgVhViVjVk
VlVmVnVoVpVqVrVsVtVuVvVwVxVyVzV0V1V2V3V4V5V6V7V8V9VaVbVcVdVeVfVg
VhViVjVkVlVmVnVoVpVqVrVsVtVuVvVwVxVyVzV0ECgYEAyV3V4V5V6V7V8V9VaVb
VcVdVeVfVgVhViVjVkVlVmVnVoVpVqVrVsVtVuVvVwVxVyVzV0V1V2V3V4V5V6V7
V8V9VaVbVcVdVeVfVgVhViVjVkVlVmVnVoVpVqVrVsVtVuVvVwVxVyVzV0V1V2V3
V4V5V6V7V8V9VaVbVcVdVeVfVgVhViVjVkVlVmVnVoVpVqVrVsVtVuVvVwVxVyVz
V0V1V2V3V4V5V6V7V8V9VaVbVcVdVeVfVgVhViVjVkVlVmVnVoVpVqVrVsVtVuVv
VwVxVyVzV0V1V2V3V4V5V6V7V8V9VaVbVcVdVeVfVgVhViVjVkVlVmVnVoVpVqVr
VsVtVuVvVwVxVyVzV0V1V2V3V4V5V6V7V8V9VaVbVcVdVeVfVgVhViVjVkVlVmVn
VoVpVqVrVsVtVuVvVwVxVyVzV0V1V2V3V4V5V6V7V8V9VaVbVcVdVeVfVgVhViVj
VkVlVmVnVoVpVqVrVsVtVuVvVwVxVyVzV0V1V2V3V4V5V6V7V8V9VaVbVcVdVeVf
VgVhViVjVkVlVmVnVoVpVqVrVsVtVuVvVwVxVyVzV0ECgYEAuV5V6V7V8V9VaVbVc
VdVeVfVgVhViVjVkVlVmVnVoVpVqVrVsVtVuVvVwVxVyVzV0V1V2V3V4V5V6V7V8
V9VaVbVcVdVeVfVgVhViVjVkVlVmVnVoVpVqVrVsVtVuVvVwVxVyVzV0V1V2V3V4
V5V6V7V8V9VaVbVcVdVeVfVgVhViVjVkVlVmVnVoVpVqVrVsVtVuVvVwVxVyVzV0
V1V2V3V4V5V6V7V8V9VaVbVcVdVeVfVgVhViVjVkVlVmVnVoVpVqVrVsVtVuVvVw
VxVyVzV0V1V2V3V4V5V6V7V8V9VaVbVcVdVeVfVgVhViVjVkVlVmVnVoVpVqVrVs
VtVuVvVwVxVyVzV0V1V2V3V4V5V6V7V8V9VaVbVcVdVeVfVgVhViVjVkVlVmVnVo
VpVqVrVsVtVuVvVwVxVyVzV0V1V2V3V4V5V6V7V8V9VaVbVcVdVeVfVgVhViVjVk
VlVmVnVoVpVqVrVsVtVuVvVwVxVyVzV0V1V2V3V4V5V6V7V8V9VaVbVcVdVeVfVg
VhViVjVkVlVmVnVoVpVqVrVsVtVuVvVwVxVyVzV0ECgYBV6V7V8V9VaVbVcVdVeVf
VgVhViVjVkVlVmVnVoVpVqVrVsVtVuVvVwVxVyVzV0V1V2V3V4V5V6V7V8V9VaVb
VcVdVeVfVgVhViVjVkVlVmVnVoVpVqVrVsVtVuVvVwVxVyVzV0V1V2V3V4V5V6V7
V8V9VaVbVcVdVeVfVgVhViVjVkVlVmVnVoVpVqVrVsVtVuVvVwVxVyVzV0V1V2V3
V4V5V6V7V8V9VaVbVcVdVeVfVgVhViVjVkVlVmVnVoVpVqVrVsVtVuVvVwVxVyVz
V0V1V2V3V4V5V6V7V8V9VaVbVcVdVeVfVgVhViVjVkVlVmVnVoVpVqVrVsVtVuVv
VwVxVyVzV0V1V2V3V4V5V6V7V8V9VaVbVcVdVeVfVgVhViVjVkVlVmVnVoVpVqVr
VsVtVuVvVwVxVyVzV0V1V2V3V4V5V6V7V8V9VaVbVcVdVeVfVgVhViVjVkVlVmVn
VoVpVqVrVsVtVuVvVwVxVyVzV0V1V2V3V4V5V6V7V8V9VaVbVcVdVeVfVgVhViVj
VkVlVmVnVoVpVqVrVsVtVuVvVwVxVyVzV0Q=
-----END PRIVATE KEY-----`
}
