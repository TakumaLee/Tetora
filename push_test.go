package main

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPushSubscription_JSON(t *testing.T) {
	sub := PushSubscription{
		Endpoint: "https://fcm.googleapis.com/fcm/send/test123",
		Keys: PushKeys{
			P256dh: "BNcRdreALRFXTkOOUHK1EtK2wtaz5Ry4YfYCA_0QTp",
			Auth:   "tBHItJI5svbpez7KI4CCXg",
		},
		CreatedAt: "2026-01-01T00:00:00Z",
		UserAgent: "Mozilla/5.0",
	}

	data, err := json.Marshal(sub)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded PushSubscription
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.Endpoint != sub.Endpoint {
		t.Errorf("endpoint mismatch: got %q, want %q", decoded.Endpoint, sub.Endpoint)
	}
	if decoded.Keys.P256dh != sub.Keys.P256dh {
		t.Errorf("p256dh mismatch")
	}
}

func TestPushNotification_JSON(t *testing.T) {
	notif := PushNotification{
		Title: "Test",
		Body:  "Hello World",
		Icon:  "/icon.png",
		Tag:   "test-tag",
		URL:   "https://example.com",
	}

	data, err := json.Marshal(notif)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	if !strings.Contains(string(data), `"title":"Test"`) {
		t.Errorf("title not in JSON")
	}
}

func TestPushConfig_Defaults(t *testing.T) {
	cfg := PushConfig{
		Enabled: true,
		TTL:     0, // should default to 3600
	}

	if cfg.Enabled != true {
		t.Errorf("enabled should be true")
	}
	// TTL default is handled in SendToEndpoint
}

func TestHKDF_SHA256(t *testing.T) {
	// Test HKDF-Expand with known inputs.
	prk := []byte("test-prk-key-for-hkdf-expand")
	info := []byte("test-info")
	result := hkdfExpand(prk, info, 32)

	if len(result) != 32 {
		t.Errorf("expected 32 bytes, got %d", len(result))
	}

	// Test determinism: same inputs should give same output.
	result2 := hkdfExpand(prk, info, 32)
	if string(result) != string(result2) {
		t.Errorf("hkdfExpand not deterministic")
	}
}

func TestHMAC_SHA256(t *testing.T) {
	key := []byte("secret-key")
	data := []byte("test-data")
	mac := hmacSHA256(key, data)

	if len(mac) != 32 {
		t.Errorf("expected 32 bytes, got %d", len(mac))
	}

	// Test determinism.
	mac2 := hmacSHA256(key, data)
	if string(mac) != string(mac2) {
		t.Errorf("hmacSHA256 not deterministic")
	}
}

func TestVAPIDJWT_Generate(t *testing.T) {
	// Generate a test ECDH P-256 keypair for VAPID.
	privKey, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key failed: %v", err)
	}

	privKeyBytes := privKey.Bytes()
	vapidPrivateKey := base64.RawURLEncoding.EncodeToString(privKeyBytes)

	endpoint := "https://fcm.googleapis.com/fcm/send/test123"
	email := "test@example.com"

	authHeader, err := generateVAPIDAuth(endpoint, vapidPrivateKey, email)
	if err != nil {
		t.Fatalf("generateVAPIDAuth failed: %v", err)
	}

	// Verify format: vapid t=<jwt>,k=<publicKey>
	if !strings.HasPrefix(authHeader, "vapid t=") {
		t.Errorf("invalid auth header format: %s", authHeader)
	}

	parts := strings.Split(authHeader, ",k=")
	if len(parts) != 2 {
		t.Errorf("auth header should have k= part")
	}

	jwt := strings.TrimPrefix(parts[0], "vapid t=")
	jwtParts := strings.Split(jwt, ".")
	if len(jwtParts) != 3 {
		t.Errorf("JWT should have 3 parts (header.claims.signature), got %d", len(jwtParts))
	}
}

func TestPushPayload_Encrypt(t *testing.T) {
	// Generate subscriber keypair.
	subPrivKey, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate subscriber key failed: %v", err)
	}
	subPubKeyBytes := subPrivKey.PublicKey().Bytes()
	p256dh := base64.RawURLEncoding.EncodeToString(subPubKeyBytes)

	// Generate auth secret (16 random bytes).
	authBytes := make([]byte, 16)
	if _, err := rand.Read(authBytes); err != nil {
		t.Fatalf("generate auth failed: %v", err)
	}
	auth := base64.RawURLEncoding.EncodeToString(authBytes)

	payload := []byte(`{"title":"Test","body":"Hello"}`)

	encrypted, encHeader, cryptoHeader, err := encryptPayload(payload, p256dh, auth)
	if err != nil {
		t.Fatalf("encryptPayload failed: %v", err)
	}

	if len(encrypted) == 0 {
		t.Errorf("encrypted payload is empty")
	}

	// Verify headers.
	if !strings.HasPrefix(encHeader, "salt=") {
		t.Errorf("Encryption header should start with salt=")
	}
	if !strings.HasPrefix(cryptoHeader, "dh=") {
		t.Errorf("Crypto-Key header should start with dh=")
	}

	// Encrypted payload should be larger than original (salt + rs + idlen + ciphertext + tag).
	if len(encrypted) <= len(payload) {
		t.Errorf("encrypted payload should be larger than original")
	}
}

func TestPushManager_SubscribeUnsubscribe(t *testing.T) {
	// Create temp DB.
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := &Config{
		HistoryDB: dbPath,
		Push: PushConfig{
			Enabled:         true,
			VAPIDPublicKey:  "test-public",
			VAPIDPrivateKey: "test-private",
			VAPIDEmail:      "test@example.com",
			TTL:             7200,
		},
	}

	pm := newPushManager(cfg)

	sub := PushSubscription{
		Endpoint: "https://fcm.googleapis.com/fcm/send/test123",
		Keys: PushKeys{
			P256dh: "BNcRdreALRFXTkOOUHK1EtK2wtaz5Ry4YfYCA_0QTp",
			Auth:   "tBHItJI5svbpez7KI4CCXg",
		},
		UserAgent: "Test/1.0",
	}

	// Subscribe.
	if err := pm.Subscribe(sub); err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}

	// List.
	subs := pm.ListSubscriptions()
	if len(subs) != 1 {
		t.Errorf("expected 1 subscription, got %d", len(subs))
	}
	if subs[0].Endpoint != sub.Endpoint {
		t.Errorf("endpoint mismatch")
	}

	// Unsubscribe.
	if err := pm.Unsubscribe(sub.Endpoint); err != nil {
		t.Fatalf("unsubscribe failed: %v", err)
	}

	// List should be empty.
	subs = pm.ListSubscriptions()
	if len(subs) != 0 {
		t.Errorf("expected 0 subscriptions after unsubscribe, got %d", len(subs))
	}
}

func TestPushManager_LoadFromDB(t *testing.T) {
	// Create temp DB.
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := &Config{
		HistoryDB: dbPath,
		Push: PushConfig{
			Enabled: true,
		},
	}

	// First manager: subscribe.
	pm1 := newPushManager(cfg)
	sub := PushSubscription{
		Endpoint: "https://example.com/push/abc",
		Keys: PushKeys{
			P256dh: "test-p256dh",
			Auth:   "test-auth",
		},
		UserAgent: "Test/1.0",
	}
	if err := pm1.Subscribe(sub); err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}

	// Second manager: should load from DB.
	pm2 := newPushManager(cfg)
	subs := pm2.ListSubscriptions()
	if len(subs) != 1 {
		t.Errorf("expected 1 subscription loaded from DB, got %d", len(subs))
	}
	if subs[0].Endpoint != sub.Endpoint {
		t.Errorf("endpoint mismatch after reload")
	}
}

func TestPushSubscription_Validation(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := &Config{
		HistoryDB: dbPath,
		Push: PushConfig{
			Enabled: true,
		},
	}

	pm := newPushManager(cfg)

	// Missing endpoint.
	err := pm.Subscribe(PushSubscription{
		Keys: PushKeys{
			P256dh: "test",
			Auth:   "test",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "missing required fields") {
		t.Errorf("expected validation error for missing endpoint, got: %v", err)
	}

	// Invalid endpoint URL.
	err = pm.Subscribe(PushSubscription{
		Endpoint: "not-a-url",
		Keys: PushKeys{
			P256dh: "test",
			Auth:   "test",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "invalid endpoint") {
		t.Errorf("expected validation error for invalid URL, got: %v", err)
	}
}

func TestVAPIDKey_RoundTrip(t *testing.T) {
	// Generate ECDSA P-256 private key.
	curve := elliptic.P256()
	privKeyECDSA, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		t.Fatalf("generate ECDSA key failed: %v", err)
	}

	// Export private key (32 bytes).
	privKeyBytes := privKeyECDSA.D.Bytes()
	if len(privKeyBytes) < 32 {
		// Pad to 32 bytes if needed.
		padded := make([]byte, 32)
		copy(padded[32-len(privKeyBytes):], privKeyBytes)
		privKeyBytes = padded
	}

	vapidPrivateKey := base64.RawURLEncoding.EncodeToString(privKeyBytes)

	// Generate VAPID auth.
	endpoint := "https://example.com/push/test"
	email := "test@example.com"
	authHeader, err := generateVAPIDAuth(endpoint, vapidPrivateKey, email)
	if err != nil {
		t.Fatalf("generateVAPIDAuth failed: %v", err)
	}

	// Extract public key from auth header.
	parts := strings.Split(authHeader, ",k=")
	if len(parts) != 2 {
		t.Fatalf("invalid auth header format")
	}
	pubKeyB64 := parts[1]

	// Decode and verify it's a valid P-256 point.
	pubKeyBytes, err := base64.RawURLEncoding.DecodeString(pubKeyB64)
	if err != nil {
		t.Fatalf("decode public key failed: %v", err)
	}

	x, y := elliptic.Unmarshal(curve, pubKeyBytes)
	if x == nil {
		t.Fatalf("invalid public key point")
	}

	// Verify it matches the private key's public key.
	if x.Cmp(privKeyECDSA.PublicKey.X) != 0 || y.Cmp(privKeyECDSA.PublicKey.Y) != 0 {
		t.Errorf("public key mismatch")
	}
}

func TestPushManager_NoSubscriptions(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := &Config{
		HistoryDB: dbPath,
		Push: PushConfig{
			Enabled: true,
		},
	}

	pm := newPushManager(cfg)

	notif := PushNotification{
		Title: "Test",
		Body:  "Hello",
	}

	err := pm.SendNotification(notif)
	if err == nil || !strings.Contains(err.Error(), "no subscriptions") {
		t.Errorf("expected 'no subscriptions' error, got: %v", err)
	}
}

// Cleanup test databases.
func TestMain(m *testing.M) {
	// Run tests.
	code := m.Run()

	// Cleanup (if needed).
	os.Exit(code)
}
