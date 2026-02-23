package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// --- Web Push Types ---

type PushSubscription struct {
	Endpoint  string `json:"endpoint"`
	Keys      PushKeys `json:"keys"`
	CreatedAt string `json:"createdAt,omitempty"`
	UserAgent string `json:"userAgent,omitempty"`
}

type PushKeys struct {
	P256dh string `json:"p256dh"` // base64url
	Auth   string `json:"auth"`   // base64url
}

type PushNotification struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Icon  string `json:"icon,omitempty"`
	Tag   string `json:"tag,omitempty"`
	URL   string `json:"url,omitempty"`
}

type PushManager struct {
	cfg           *Config
	subscriptions map[string]PushSubscription // endpoint → subscription
	mu            sync.RWMutex
	dbPath        string
}

// --- Push Manager ---

func newPushManager(cfg *Config) *PushManager {
	pm := &PushManager{
		cfg:           cfg,
		subscriptions: make(map[string]PushSubscription),
		dbPath:        cfg.HistoryDB,
	}
	pm.initDB()
	pm.loadFromDB()
	return pm
}

func (pm *PushManager) initDB() {
	sql := `CREATE TABLE IF NOT EXISTS push_subscriptions (
		endpoint TEXT PRIMARY KEY,
		p256dh TEXT NOT NULL,
		auth TEXT NOT NULL,
		user_agent TEXT DEFAULT '',
		created_at TEXT NOT NULL DEFAULT (datetime('now'))
	);`
	if _, err := queryDB(pm.dbPath, sql); err != nil {
		logWarn("push: init db failed", "error", err)
	}
}

func (pm *PushManager) loadFromDB() {
	rows, err := queryDB(pm.dbPath, "SELECT endpoint, p256dh, auth, user_agent, created_at FROM push_subscriptions")
	if err != nil {
		logWarn("push: load from db failed", "error", err)
		return
	}
	pm.mu.Lock()
	defer pm.mu.Unlock()
	for _, row := range rows {
		sub := PushSubscription{
			Endpoint: row["endpoint"].(string),
			Keys: PushKeys{
				P256dh: row["p256dh"].(string),
				Auth:   row["auth"].(string),
			},
			UserAgent: row["user_agent"].(string),
			CreatedAt: row["created_at"].(string),
		}
		pm.subscriptions[sub.Endpoint] = sub
	}
	logInfo("push: loaded subscriptions", "count", len(pm.subscriptions))
}

func (pm *PushManager) Subscribe(sub PushSubscription) error {
	if sub.Endpoint == "" || sub.Keys.P256dh == "" || sub.Keys.Auth == "" {
		return errors.New("invalid subscription: missing required fields")
	}

	// Validate endpoint is a valid absolute URL.
	u, err := url.Parse(sub.Endpoint)
	if err != nil {
		return fmt.Errorf("invalid endpoint URL: %w", err)
	}
	if !u.IsAbs() || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("invalid endpoint: must be absolute http/https URL")
	}

	// Set timestamp if not provided.
	if sub.CreatedAt == "" {
		sub.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}

	pm.mu.Lock()
	pm.subscriptions[sub.Endpoint] = sub
	pm.mu.Unlock()

	// Save to DB.
	sql := fmt.Sprintf(
		`INSERT OR REPLACE INTO push_subscriptions (endpoint, p256dh, auth, user_agent, created_at) VALUES ('%s', '%s', '%s', '%s', '%s')`,
		escapeSQLite(sub.Endpoint),
		escapeSQLite(sub.Keys.P256dh),
		escapeSQLite(sub.Keys.Auth),
		escapeSQLite(sub.UserAgent),
		escapeSQLite(sub.CreatedAt),
	)
	if _, err := queryDB(pm.dbPath, sql); err != nil {
		logWarn("push: save subscription failed", "error", err)
		return err
	}

	logInfo("push: subscription saved", "endpoint", sub.Endpoint)
	return nil
}

func (pm *PushManager) Unsubscribe(endpoint string) error {
	pm.mu.Lock()
	delete(pm.subscriptions, endpoint)
	pm.mu.Unlock()

	sql := fmt.Sprintf(`DELETE FROM push_subscriptions WHERE endpoint = '%s'`, escapeSQLite(endpoint))
	if _, err := queryDB(pm.dbPath, sql); err != nil {
		logWarn("push: unsubscribe failed", "error", err)
		return err
	}

	logInfo("push: subscription removed", "endpoint", endpoint)
	return nil
}

func (pm *PushManager) SendNotification(notif PushNotification) error {
	pm.mu.RLock()
	subs := make([]PushSubscription, 0, len(pm.subscriptions))
	for _, sub := range pm.subscriptions {
		subs = append(subs, sub)
	}
	pm.mu.RUnlock()

	if len(subs) == 0 {
		return errors.New("no subscriptions")
	}

	var errs []string
	for _, sub := range subs {
		if err := pm.SendToEndpoint(sub.Endpoint, notif); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", sub.Endpoint, err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to send to %d/%d subscribers: %s", len(errs), len(subs), strings.Join(errs, "; "))
	}

	logInfo("push: notification sent to all subscribers", "count", len(subs))
	return nil
}

func (pm *PushManager) SendToEndpoint(endpoint string, notif PushNotification) error {
	pm.mu.RLock()
	sub, ok := pm.subscriptions[endpoint]
	pm.mu.RUnlock()

	if !ok {
		return errors.New("subscription not found")
	}

	// Serialize notification as JSON payload.
	payload, err := json.Marshal(notif)
	if err != nil {
		return fmt.Errorf("marshal notification: %w", err)
	}

	// Encrypt payload.
	encrypted, encHeader, cryptoHeader, err := encryptPayload(payload, sub.Keys.P256dh, sub.Keys.Auth)
	if err != nil {
		return fmt.Errorf("encrypt payload: %w", err)
	}

	// Generate VAPID Authorization.
	authHeader, err := generateVAPIDAuth(endpoint, pm.cfg.Push.VAPIDPrivateKey, pm.cfg.Push.VAPIDEmail)
	if err != nil {
		return fmt.Errorf("generate VAPID auth: %w", err)
	}

	// Send push request.
	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(string(encrypted)))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	ttl := pm.cfg.Push.TTL
	if ttl <= 0 {
		ttl = 3600
	}

	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Encryption", encHeader)
	req.Header.Set("Crypto-Key", cryptoHeader)
	req.Header.Set("Authorization", authHeader)
	req.Header.Set("TTL", fmt.Sprintf("%d", ttl))

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send push request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		// If subscription is gone (410), auto-remove it.
		if resp.StatusCode == 410 {
			logInfo("push: subscription expired (410), removing", "endpoint", endpoint)
			pm.Unsubscribe(endpoint)
		}
		return fmt.Errorf("push server returned %d: %s", resp.StatusCode, string(body))
	}

	logInfo("push: notification sent", "endpoint", endpoint, "status", resp.StatusCode)
	return nil
}

func (pm *PushManager) ListSubscriptions() []PushSubscription {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	subs := make([]PushSubscription, 0, len(pm.subscriptions))
	for _, sub := range pm.subscriptions {
		subs = append(subs, sub)
	}
	return subs
}

// --- VAPID + Web Push Crypto (pure Go stdlib) ---

// generateVAPIDAuth creates VAPID JWT + Authorization header for a push request.
func generateVAPIDAuth(endpoint, vapidPrivateKey, vapidEmail string) (string, error) {
	// Parse endpoint to get origin.
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("parse endpoint: %w", err)
	}
	origin := fmt.Sprintf("%s://%s", u.Scheme, u.Host)

	// Decode VAPID private key (base64url → 32 bytes).
	privKeyBytes, err := base64.RawURLEncoding.DecodeString(vapidPrivateKey)
	if err != nil {
		return "", fmt.Errorf("decode private key: %w", err)
	}
	if len(privKeyBytes) != 32 {
		return "", errors.New("invalid private key length (expected 32 bytes)")
	}

	// Build ECDSA private key (P-256).
	curve := elliptic.P256()
	d := new(big.Int).SetBytes(privKeyBytes)
	x, y := curve.ScalarBaseMult(privKeyBytes)
	privKey := &ecdsa.PrivateKey{
		PublicKey: ecdsa.PublicKey{
			Curve: curve,
			X:     x,
			Y:     y,
		},
		D: d,
	}

	// Compute VAPID public key (uncompressed point).
	pubKeyBytes := elliptic.Marshal(curve, privKey.PublicKey.X, privKey.PublicKey.Y)
	vapidPublicKey := base64.RawURLEncoding.EncodeToString(pubKeyBytes)

	// Create JWT claims.
	now := time.Now().Unix()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(
		`{"aud":%q,"exp":%d,"sub":"mailto:%s"}`,
		origin,
		now+43200, // 12 hours
		vapidEmail,
	)))

	// Sign with ES256 (ECDSA P-256 SHA-256).
	message := header + "." + claims
	hash := sha256.Sum256([]byte(message))
	r, s, err := ecdsa.Sign(rand.Reader, privKey, hash[:])
	if err != nil {
		return "", fmt.Errorf("sign JWT: %w", err)
	}

	// Encode signature (r || s, 64 bytes).
	sig := append(r.Bytes(), s.Bytes()...)
	if len(sig) < 64 {
		// Pad if needed.
		padded := make([]byte, 64)
		copy(padded[32-len(r.Bytes()):32], r.Bytes())
		copy(padded[64-len(s.Bytes()):], s.Bytes())
		sig = padded
	}
	signature := base64.RawURLEncoding.EncodeToString(sig)

	jwt := message + "." + signature

	// Return Authorization header: vapid t=<jwt>,k=<publicKey>
	return fmt.Sprintf("vapid t=%s,k=%s", jwt, vapidPublicKey), nil
}

// encryptPayload encrypts push payload per RFC 8188 (aes128gcm).
func encryptPayload(payload []byte, p256dh, auth string) ([]byte, string, string, error) {
	// Decode subscriber keys (base64url).
	subPubKey, err := base64.RawURLEncoding.DecodeString(p256dh)
	if err != nil {
		return nil, "", "", fmt.Errorf("decode p256dh: %w", err)
	}
	authSecret, err := base64.RawURLEncoding.DecodeString(auth)
	if err != nil {
		return nil, "", "", fmt.Errorf("decode auth: %w", err)
	}

	// Generate ephemeral ECDH P-256 keypair.
	curve := elliptic.P256()
	localPrivKey, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", "", fmt.Errorf("generate ephemeral key: %w", err)
	}
	localPubKeyBytes := localPrivKey.PublicKey().Bytes()

	// Parse subscriber public key.
	subX, _ := elliptic.Unmarshal(curve, subPubKey)
	if subX == nil {
		return nil, "", "", errors.New("invalid subscriber public key")
	}
	subPubKeyECDH, err := ecdh.P256().NewPublicKey(subPubKey)
	if err != nil {
		return nil, "", "", fmt.Errorf("parse subscriber public key: %w", err)
	}

	// ECDH key agreement.
	sharedSecret, err := localPrivKey.ECDH(subPubKeyECDH)
	if err != nil {
		return nil, "", "", fmt.Errorf("ECDH: %w", err)
	}

	// Derive encryption key and nonce using HKDF.
	// HKDF-SHA256 Extract: PRK = HMAC-SHA256(salt=auth, ikm=sharedSecret)
	prk := hmacSHA256(authSecret, sharedSecret)

	// HKDF-SHA256 Expand for IKM (Intermediate Key Material).
	// Info = "Content-Encoding: auth\x00" || subPubKey || localPubKey
	ikmInfo := append([]byte("Content-Encoding: auth\x00"), subPubKey...)
	ikmInfo = append(ikmInfo, localPubKeyBytes...)
	ikm := hkdfExpand(prk, ikmInfo, 32)

	// Derive CEK (Content Encryption Key) and Nonce.
	// Salt for CEK/Nonce derivation: we use a random 16-byte salt.
	salt := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, "", "", fmt.Errorf("generate salt: %w", err)
	}

	// PRK for CEK/Nonce: HKDF-Extract with salt.
	prkCEK := hmacSHA256(salt, ikm)

	// CEK: HKDF-Expand(prkCEK, "Content-Encoding: aes128gcm\x00", 16)
	cek := hkdfExpand(prkCEK, []byte("Content-Encoding: aes128gcm\x00"), 16)

	// Nonce: HKDF-Expand(prkCEK, "Content-Encoding: nonce\x00", 12)
	nonce := hkdfExpand(prkCEK, []byte("Content-Encoding: nonce\x00"), 12)

	// Encrypt with AES-128-GCM.
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, "", "", fmt.Errorf("create AES cipher: %w", err)
	}
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, "", "", fmt.Errorf("create GCM: %w", err)
	}

	// Construct record: payload + padding delimiter (0x02).
	record := append(payload, 0x02)

	// Encrypt.
	ciphertext := aesgcm.Seal(nil, nonce, record, nil)

	// Build encrypted payload: salt (16 bytes) + rs (4 bytes, big-endian) + idlen (1 byte, 0) + ciphertext.
	rs := uint32(4096) // record size
	encrypted := make([]byte, 0, 16+4+1+len(ciphertext))
	encrypted = append(encrypted, salt...)
	rsBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(rsBytes, rs)
	encrypted = append(encrypted, rsBytes...)
	encrypted = append(encrypted, 0x00) // idlen = 0
	encrypted = append(encrypted, ciphertext...)

	// Encryption header: salt=<base64url(salt)>
	encHeader := fmt.Sprintf("salt=%s", base64.RawURLEncoding.EncodeToString(salt))

	// Crypto-Key header: dh=<base64url(localPubKey)>
	cryptoHeader := fmt.Sprintf("dh=%s", base64.RawURLEncoding.EncodeToString(localPubKeyBytes))

	return encrypted, encHeader, cryptoHeader, nil
}

// --- HKDF Helpers (RFC 5869, HMAC-SHA256) ---

func hmacSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

func hkdfExpand(prk, info []byte, length int) []byte {
	// HKDF-Expand: T(0) = empty, T(i) = HMAC(PRK, T(i-1) | info | i)
	hashLen := sha256.Size
	n := (length + hashLen - 1) / hashLen
	okm := make([]byte, 0, n*hashLen)
	prev := []byte{}
	for i := 1; i <= n; i++ {
		mac := hmac.New(sha256.New, prk)
		mac.Write(prev)
		mac.Write(info)
		mac.Write([]byte{byte(i)})
		block := mac.Sum(nil)
		okm = append(okm, block...)
		prev = block
	}
	return okm[:length]
}
