package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// --- P18.2: OAuth 2.0 Generic Framework ---

// OAuthConfig holds top-level OAuth settings.
type OAuthConfig struct {
	Services      map[string]OAuthServiceConfig `json:"services,omitempty"`
	EncryptionKey string                        `json:"encryptionKey,omitempty"` // $ENV_VAR supported
	RedirectBase  string                        `json:"redirectBase,omitempty"` // e.g. "https://my.domain.com"
}

// OAuthServiceConfig configures a single OAuth 2.0 service.
type OAuthServiceConfig struct {
	Name         string            `json:"name"`
	ClientID     string            `json:"clientId"`               // supports $ENV_VAR
	ClientSecret string            `json:"clientSecret"`           // supports $ENV_VAR
	AuthURL      string            `json:"authUrl"`
	TokenURL     string            `json:"tokenUrl"`
	Scopes       []string          `json:"scopes"`
	RedirectURL  string            `json:"redirectUrl,omitempty"`  // default: {redirectBase}/api/oauth/{name}/callback
	ExtraParams  map[string]string `json:"extraParams,omitempty"`
}

// OAuthToken represents a stored token.
type OAuthToken struct {
	ServiceName  string `json:"serviceName"`
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken,omitempty"`
	TokenType    string `json:"tokenType,omitempty"`
	ExpiresAt    string `json:"expiresAt,omitempty"`
	Scopes       string `json:"scopes,omitempty"`
	CreatedAt    string `json:"createdAt"`
	UpdatedAt    string `json:"updatedAt"`
}

// OAuthTokenStatus is a public-safe view of token status (no secrets).
type OAuthTokenStatus struct {
	ServiceName string `json:"serviceName"`
	Connected   bool   `json:"connected"`
	Scopes      string `json:"scopes,omitempty"`
	ExpiresAt   string `json:"expiresAt,omitempty"`
	ExpiresSoon bool   `json:"expiresSoon,omitempty"`
	CreatedAt   string `json:"createdAt,omitempty"`
}

// OAuthManager coordinates OAuth flows and token lifecycle.
type OAuthManager struct {
	cfg           *Config
	dbPath        string
	encryptionKey string
	states        map[string]oauthState // CSRF state token -> service info
	mu            sync.Mutex
}

type oauthState struct {
	service   string
	createdAt time.Time
}

// globalOAuthManager is exposed for tool handlers (Gmail, Calendar, etc.) to make authenticated requests.
var globalOAuthManager *OAuthManager

// oauthTemplates provides built-in OAuth provider templates.
var oauthTemplates = map[string]OAuthServiceConfig{
	"google": {
		AuthURL:     "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL:    "https://oauth2.googleapis.com/token",
		ExtraParams: map[string]string{"access_type": "offline", "prompt": "consent"},
	},
	"github": {
		AuthURL:  "https://github.com/login/oauth/authorize",
		TokenURL: "https://github.com/login/oauth/access_token",
	},
	"twitter": {
		AuthURL:  "https://twitter.com/i/oauth2/authorize",
		TokenURL: "https://api.twitter.com/2/oauth2/token",
	},
}

// newOAuthManager creates a new OAuthManager from config.
func newOAuthManager(cfg *Config) *OAuthManager {
	m := &OAuthManager{
		cfg:           cfg,
		dbPath:        cfg.HistoryDB,
		encryptionKey: cfg.OAuth.EncryptionKey,
		states:        make(map[string]oauthState),
	}
	return m
}

// initOAuthTable creates the oauth_tokens table.
func initOAuthTable(dbPath string) error {
	sql := `CREATE TABLE IF NOT EXISTS oauth_tokens (
		service_name TEXT PRIMARY KEY,
		access_token TEXT NOT NULL,
		refresh_token TEXT DEFAULT '',
		token_type TEXT DEFAULT 'Bearer',
		expires_at TEXT DEFAULT '',
		scopes TEXT DEFAULT '',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	);`
	_, err := queryDB(dbPath, sql)
	return err
}

// --- Token Encryption (AES-256-GCM) ---

// encryptOAuthToken encrypts plaintext using AES-256-GCM.
// Key is derived from SHA-256 of the encryption key string.
// Nonce (12 bytes) is prepended to ciphertext. Output is hex-encoded.
func encryptOAuthToken(plaintext, key string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	if key == "" {
		return plaintext, nil // no encryption key — store as plaintext
	}

	// Derive 32-byte key via SHA-256.
	keyHash := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(keyHash[:])
	if err != nil {
		return "", fmt.Errorf("aes cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("gcm: %w", err)
	}

	// Generate random 12-byte nonce.
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("nonce: %w", err)
	}

	// Encrypt and prepend nonce.
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return hex.EncodeToString(ciphertext), nil
}

// decryptOAuthToken decrypts a hex-encoded AES-256-GCM ciphertext.
func decryptOAuthToken(ciphertextHex, key string) (string, error) {
	if ciphertextHex == "" {
		return "", nil
	}
	if key == "" {
		return ciphertextHex, nil // no key — stored as plaintext
	}

	data, err := hex.DecodeString(ciphertextHex)
	if err != nil {
		return "", fmt.Errorf("hex decode: %w", err)
	}

	keyHash := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(keyHash[:])
	if err != nil {
		return "", fmt.Errorf("aes cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("gcm: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}

	return string(plaintext), nil
}

// --- Token Storage ---

// storeOAuthToken stores (or updates) an OAuth token in the DB.
// Access and refresh tokens are encrypted if an encryption key is set.
func storeOAuthToken(dbPath string, token OAuthToken, encKey string) error {
	accessEnc, err := encryptOAuthToken(token.AccessToken, encKey)
	if err != nil {
		return fmt.Errorf("encrypt access_token: %w", err)
	}
	refreshEnc, err := encryptOAuthToken(token.RefreshToken, encKey)
	if err != nil {
		return fmt.Errorf("encrypt refresh_token: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if token.CreatedAt == "" {
		token.CreatedAt = now
	}
	token.UpdatedAt = now

	sql := fmt.Sprintf(
		`INSERT OR REPLACE INTO oauth_tokens (service_name, access_token, refresh_token, token_type, expires_at, scopes, created_at, updated_at) VALUES ('%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s')`,
		escapeSQLite(token.ServiceName),
		escapeSQLite(accessEnc),
		escapeSQLite(refreshEnc),
		escapeSQLite(token.TokenType),
		escapeSQLite(token.ExpiresAt),
		escapeSQLite(token.Scopes),
		escapeSQLite(token.CreatedAt),
		escapeSQLite(token.UpdatedAt),
	)
	_, err = queryDB(dbPath, sql)
	return err
}

// loadOAuthToken loads and decrypts a token from the DB.
func loadOAuthToken(dbPath, serviceName, encKey string) (*OAuthToken, error) {
	sql := fmt.Sprintf(
		`SELECT service_name, access_token, refresh_token, token_type, expires_at, scopes, created_at, updated_at FROM oauth_tokens WHERE service_name = '%s'`,
		escapeSQLite(serviceName),
	)
	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}

	row := rows[0]
	accessDec, err := decryptOAuthToken(fmt.Sprint(row["access_token"]), encKey)
	if err != nil {
		return nil, fmt.Errorf("decrypt access_token: %w", err)
	}
	refreshDec, err := decryptOAuthToken(fmt.Sprint(row["refresh_token"]), encKey)
	if err != nil {
		return nil, fmt.Errorf("decrypt refresh_token: %w", err)
	}

	return &OAuthToken{
		ServiceName:  fmt.Sprint(row["service_name"]),
		AccessToken:  accessDec,
		RefreshToken: refreshDec,
		TokenType:    fmt.Sprint(row["token_type"]),
		ExpiresAt:    fmt.Sprint(row["expires_at"]),
		Scopes:       fmt.Sprint(row["scopes"]),
		CreatedAt:    fmt.Sprint(row["created_at"]),
		UpdatedAt:    fmt.Sprint(row["updated_at"]),
	}, nil
}

// deleteOAuthToken removes a token from the DB.
func deleteOAuthToken(dbPath, serviceName string) error {
	sql := fmt.Sprintf(
		`DELETE FROM oauth_tokens WHERE service_name = '%s'`,
		escapeSQLite(serviceName),
	)
	_, err := queryDB(dbPath, sql)
	return err
}

// listOAuthTokenStatuses returns status info for all stored tokens (no secrets).
func listOAuthTokenStatuses(dbPath, encKey string) ([]OAuthTokenStatus, error) {
	rows, err := queryDB(dbPath, `SELECT service_name, expires_at, scopes, created_at FROM oauth_tokens ORDER BY service_name`)
	if err != nil {
		return nil, err
	}

	statuses := make([]OAuthTokenStatus, 0, len(rows))
	for _, row := range rows {
		expiresAt := fmt.Sprint(row["expires_at"])
		expiresSoon := false
		if expiresAt != "" {
			if t, err := time.Parse(time.RFC3339, expiresAt); err == nil {
				expiresSoon = time.Until(t) < 5*time.Minute
			}
		}
		statuses = append(statuses, OAuthTokenStatus{
			ServiceName: fmt.Sprint(row["service_name"]),
			Connected:   true,
			Scopes:      fmt.Sprint(row["scopes"]),
			ExpiresAt:   expiresAt,
			ExpiresSoon: expiresSoon,
			CreatedAt:   fmt.Sprint(row["created_at"]),
		})
	}
	return statuses, nil
}

// --- Token Refresh ---

// refreshTokenIfNeeded checks token expiry and refreshes if needed.
func (m *OAuthManager) refreshTokenIfNeeded(serviceName string) (*OAuthToken, error) {
	token, err := loadOAuthToken(m.dbPath, serviceName, m.encryptionKey)
	if err != nil {
		return nil, fmt.Errorf("load token: %w", err)
	}
	if token == nil {
		return nil, fmt.Errorf("no token stored for service %q", serviceName)
	}

	// Check if token is still valid.
	if token.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, token.ExpiresAt); err == nil {
			if time.Until(t) > 60*time.Second {
				return token, nil // still valid
			}
		}
	}

	// No refresh token — return current token as-is.
	if token.RefreshToken == "" {
		logDebug("oauth token expired but no refresh_token", "service", serviceName)
		return token, nil
	}

	// Resolve service config.
	svcCfg, err := m.resolveServiceConfig(serviceName)
	if err != nil {
		return nil, err
	}

	// Exchange refresh token for new access token.
	logInfo("oauth refreshing token", "service", serviceName)
	data := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {token.RefreshToken},
		"client_id":     {svcCfg.ClientID},
		"client_secret": {svcCfg.ClientSecret},
	}

	resp, err := http.PostForm(svcCfg.TokenURL, data)
	if err != nil {
		return nil, fmt.Errorf("refresh request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("refresh failed (HTTP %d): %s", resp.StatusCode, string(body))
	}

	var tokenResp oauthTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parse refresh response: %w", err)
	}

	// Update token.
	token.AccessToken = tokenResp.AccessToken
	if tokenResp.RefreshToken != "" {
		token.RefreshToken = tokenResp.RefreshToken // some providers rotate refresh tokens
	}
	if tokenResp.TokenType != "" {
		token.TokenType = tokenResp.TokenType
	}
	if tokenResp.ExpiresIn > 0 {
		token.ExpiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second).UTC().Format(time.RFC3339)
	}

	if err := storeOAuthToken(m.dbPath, *token, m.encryptionKey); err != nil {
		return nil, fmt.Errorf("store refreshed token: %w", err)
	}

	logInfo("oauth token refreshed", "service", serviceName, "expiresAt", token.ExpiresAt)
	return token, nil
}

// oauthTokenResponse is the JSON response from a token endpoint.
type oauthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	TokenType    string `json:"token_type,omitempty"`
	ExpiresIn    int    `json:"expires_in,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

// --- Authenticated HTTP Request ---

// Request makes an authenticated HTTP request using the stored token for the given service.
// It auto-refreshes the token if needed.
func (m *OAuthManager) Request(ctx context.Context, serviceName, method, reqURL string, body io.Reader) (*http.Response, error) {
	token, err := m.refreshTokenIfNeeded(serviceName)
	if err != nil {
		return nil, fmt.Errorf("oauth token: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL, body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	tokenType := token.TokenType
	if tokenType == "" {
		tokenType = "Bearer"
	}
	req.Header.Set("Authorization", tokenType+" "+token.AccessToken)

	return http.DefaultClient.Do(req)
}

// --- Authorization Flow ---

// cleanExpiredStates removes CSRF states older than 10 minutes.
func (m *OAuthManager) cleanExpiredStates() {
	cutoff := time.Now().Add(-10 * time.Minute)
	for k, v := range m.states {
		if v.createdAt.Before(cutoff) {
			delete(m.states, k)
		}
	}
}

// generateState creates a random CSRF state token.
func generateState() (string, error) {
	b := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// handleAuthorize starts an OAuth authorization flow — redirects the user to the provider.
func (m *OAuthManager) handleAuthorize(w http.ResponseWriter, r *http.Request, serviceName string) {
	svcCfg, err := m.resolveServiceConfig(serviceName)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
		return
	}

	state, err := generateState()
	if err != nil {
		http.Error(w, `{"error":"state generation failed"}`, http.StatusInternalServerError)
		return
	}

	m.mu.Lock()
	m.cleanExpiredStates()
	m.states[state] = oauthState{service: serviceName, createdAt: time.Now()}
	m.mu.Unlock()

	// Build redirect URL.
	redirectURL := svcCfg.RedirectURL
	if redirectURL == "" {
		base := m.cfg.OAuth.RedirectBase
		if base == "" {
			base = "http://localhost" + m.cfg.ListenAddr
		}
		redirectURL = base + "/api/oauth/" + serviceName + "/callback"
	}

	params := url.Values{
		"client_id":     {svcCfg.ClientID},
		"redirect_uri":  {redirectURL},
		"response_type": {"code"},
		"state":         {state},
	}
	if len(svcCfg.Scopes) > 0 {
		params.Set("scope", strings.Join(svcCfg.Scopes, " "))
	}
	for k, v := range svcCfg.ExtraParams {
		params.Set(k, v)
	}

	authURL := svcCfg.AuthURL + "?" + params.Encode()
	logInfo("oauth authorize redirect", "service", serviceName, "url", authURL)

	http.Redirect(w, r, authURL, http.StatusFound)
}

// handleCallback processes the OAuth callback from the provider.
func (m *OAuthManager) handleCallback(w http.ResponseWriter, r *http.Request, serviceName string) {
	// Validate CSRF state.
	state := r.URL.Query().Get("state")
	if state == "" {
		http.Error(w, `{"error":"missing state parameter"}`, http.StatusBadRequest)
		return
	}

	m.mu.Lock()
	st, ok := m.states[state]
	if ok {
		delete(m.states, state) // consume state
	}
	m.mu.Unlock()

	if !ok {
		http.Error(w, `{"error":"invalid or expired state"}`, http.StatusBadRequest)
		return
	}
	if st.service != serviceName {
		http.Error(w, `{"error":"state service mismatch"}`, http.StatusBadRequest)
		return
	}
	if time.Since(st.createdAt) > 10*time.Minute {
		http.Error(w, `{"error":"state expired"}`, http.StatusBadRequest)
		return
	}

	// Check for error from provider.
	if errParam := r.URL.Query().Get("error"); errParam != "" {
		errDesc := r.URL.Query().Get("error_description")
		http.Error(w, fmt.Sprintf(`{"error":"%s","description":"%s"}`, errParam, errDesc), http.StatusBadRequest)
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, `{"error":"missing authorization code"}`, http.StatusBadRequest)
		return
	}

	svcCfg, err := m.resolveServiceConfig(serviceName)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
		return
	}

	// Build redirect URL (must match the one used in authorize).
	redirectURL := svcCfg.RedirectURL
	if redirectURL == "" {
		base := m.cfg.OAuth.RedirectBase
		if base == "" {
			base = "http://localhost" + m.cfg.ListenAddr
		}
		redirectURL = base + "/api/oauth/" + serviceName + "/callback"
	}

	// Exchange code for token.
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURL},
		"client_id":     {svcCfg.ClientID},
		"client_secret": {svcCfg.ClientSecret},
	}

	req, err := http.NewRequest("POST", svcCfg.TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"request creation: %v"}`, err), http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"token exchange: %v"}`, err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		logWarn("oauth token exchange failed", "service", serviceName, "status", resp.StatusCode, "body", string(body))
		http.Error(w, fmt.Sprintf(`{"error":"token exchange failed (HTTP %d)"}`, resp.StatusCode), http.StatusBadGateway)
		return
	}

	var tokenResp oauthTokenResponse
	// Some providers (like GitHub) may return as form-encoded.
	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/x-www-form-urlencoded") || strings.Contains(contentType, "text/plain") {
		vals, err := url.ParseQuery(string(body))
		if err == nil {
			tokenResp.AccessToken = vals.Get("access_token")
			tokenResp.RefreshToken = vals.Get("refresh_token")
			tokenResp.TokenType = vals.Get("token_type")
			tokenResp.Scope = vals.Get("scope")
		}
	} else {
		if err := json.Unmarshal(body, &tokenResp); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"parse token response: %v"}`, err), http.StatusBadGateway)
			return
		}
	}

	if tokenResp.AccessToken == "" {
		http.Error(w, `{"error":"no access_token in response"}`, http.StatusBadGateway)
		return
	}

	// Build token.
	now := time.Now().UTC()
	token := OAuthToken{
		ServiceName:  serviceName,
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		TokenType:    tokenResp.TokenType,
		Scopes:       tokenResp.Scope,
		CreatedAt:    now.Format(time.RFC3339),
		UpdatedAt:    now.Format(time.RFC3339),
	}
	if token.TokenType == "" {
		token.TokenType = "Bearer"
	}
	if tokenResp.ExpiresIn > 0 {
		token.ExpiresAt = now.Add(time.Duration(tokenResp.ExpiresIn) * time.Second).Format(time.RFC3339)
	}
	if tokenResp.Scope == "" && len(svcCfg.Scopes) > 0 {
		token.Scopes = strings.Join(svcCfg.Scopes, " ")
	}

	// Store token.
	if m.encryptionKey == "" {
		logWarn("oauth storing token WITHOUT encryption — set oauth.encryptionKey for security", "service", serviceName)
	}
	if err := storeOAuthToken(m.dbPath, token, m.encryptionKey); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"store token: %v"}`, err), http.StatusInternalServerError)
		return
	}

	logInfo("oauth token stored", "service", serviceName, "expiresAt", token.ExpiresAt)

	// Return success HTML.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html><html><body style="font-family:system-ui;text-align:center;margin-top:80px">
<h2>OAuth Connected</h2>
<p>Service <strong>%s</strong> has been connected successfully.</p>
<p>You can close this window.</p>
<script>setTimeout(function(){window.close()},3000)</script>
</body></html>`, serviceName)
}

// --- Service Config Resolution ---

// resolveServiceConfig merges built-in templates with user-provided config.
func (m *OAuthManager) resolveServiceConfig(name string) (*OAuthServiceConfig, error) {
	userCfg, hasUser := m.cfg.OAuth.Services[name]
	tmpl, hasTmpl := oauthTemplates[name]

	if !hasUser && !hasTmpl {
		return nil, fmt.Errorf("unknown oauth service %q: not configured and no built-in template", name)
	}

	// Start from template if available.
	result := OAuthServiceConfig{Name: name}
	if hasTmpl {
		result.AuthURL = tmpl.AuthURL
		result.TokenURL = tmpl.TokenURL
		if tmpl.ExtraParams != nil {
			result.ExtraParams = make(map[string]string)
			for k, v := range tmpl.ExtraParams {
				result.ExtraParams[k] = v
			}
		}
	}

	// Override with user config.
	if hasUser {
		if userCfg.ClientID != "" {
			result.ClientID = userCfg.ClientID
		}
		if userCfg.ClientSecret != "" {
			result.ClientSecret = userCfg.ClientSecret
		}
		if userCfg.AuthURL != "" {
			result.AuthURL = userCfg.AuthURL
		}
		if userCfg.TokenURL != "" {
			result.TokenURL = userCfg.TokenURL
		}
		if len(userCfg.Scopes) > 0 {
			result.Scopes = userCfg.Scopes
		}
		if userCfg.RedirectURL != "" {
			result.RedirectURL = userCfg.RedirectURL
		}
		if userCfg.ExtraParams != nil {
			if result.ExtraParams == nil {
				result.ExtraParams = make(map[string]string)
			}
			for k, v := range userCfg.ExtraParams {
				result.ExtraParams[k] = v
			}
		}
	}

	// Validate required fields.
	if result.ClientID == "" {
		return nil, fmt.Errorf("oauth service %q: clientId is required", name)
	}
	if result.AuthURL == "" {
		return nil, fmt.Errorf("oauth service %q: authUrl is required", name)
	}
	if result.TokenURL == "" {
		return nil, fmt.Errorf("oauth service %q: tokenUrl is required", name)
	}

	return &result, nil
}

// --- HTTP Route Handlers ---

// handleOAuthServices returns configured service list with connection status.
func (m *OAuthManager) handleOAuthServices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	// List configured services.
	services := make([]map[string]any, 0)
	seen := make(map[string]bool)

	// User-configured services.
	for name, svc := range m.cfg.OAuth.Services {
		seen[name] = true
		entry := map[string]any{
			"name":     name,
			"authUrl":  svc.AuthURL,
			"scopes":   svc.Scopes,
			"template": false,
		}
		if _, ok := oauthTemplates[name]; ok {
			entry["template"] = true
		}
		services = append(services, entry)
	}

	// Template-only services (not user-configured but available).
	for name := range oauthTemplates {
		if !seen[name] {
			services = append(services, map[string]any{
				"name":     name,
				"authUrl":  oauthTemplates[name].AuthURL,
				"template": true,
			})
		}
	}

	// Get stored token statuses.
	statuses, err := listOAuthTokenStatuses(m.dbPath, m.encryptionKey)
	if err != nil {
		logWarn("list oauth token statuses", "error", err)
	}

	statusMap := make(map[string]OAuthTokenStatus)
	for _, s := range statuses {
		statusMap[s.ServiceName] = s
	}

	// Merge status into services.
	for i, svc := range services {
		name := fmt.Sprint(svc["name"])
		if st, ok := statusMap[name]; ok {
			services[i]["connected"] = st.Connected
			services[i]["expiresAt"] = st.ExpiresAt
			services[i]["expiresSoon"] = st.ExpiresSoon
			services[i]["scopes"] = st.Scopes
		} else {
			services[i]["connected"] = false
		}
	}

	json.NewEncoder(w).Encode(map[string]any{
		"services": services,
		"total":    len(services),
	})
}

// handleOAuthRoute routes /api/oauth/{service}/{action} requests.
func (m *OAuthManager) handleOAuthRoute(w http.ResponseWriter, r *http.Request) {
	// Parse: /api/oauth/{service}/{action}
	path := strings.TrimPrefix(r.URL.Path, "/api/oauth/")
	if path == "" || path == "/" {
		http.Error(w, `{"error":"service name required"}`, http.StatusBadRequest)
		return
	}

	parts := strings.SplitN(path, "/", 2)
	service := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	switch action {
	case "authorize":
		m.handleAuthorize(w, r, service)
	case "callback":
		m.handleCallback(w, r, service)
	case "revoke":
		if r.Method != http.MethodPost && r.Method != http.MethodDelete {
			http.Error(w, `{"error":"POST or DELETE only"}`, http.StatusMethodNotAllowed)
			return
		}
		if err := deleteOAuthToken(m.dbPath, service); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"revoke: %v"}`, err), http.StatusInternalServerError)
			return
		}
		logInfo("oauth token revoked", "service", service)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "revoked", "service": service})
	case "status":
		w.Header().Set("Content-Type", "application/json")
		token, err := loadOAuthToken(m.dbPath, service, m.encryptionKey)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		if token == nil {
			json.NewEncoder(w).Encode(OAuthTokenStatus{ServiceName: service, Connected: false})
			return
		}
		expiresSoon := false
		if token.ExpiresAt != "" {
			if t, err := time.Parse(time.RFC3339, token.ExpiresAt); err == nil {
				expiresSoon = time.Until(t) < 5*time.Minute
			}
		}
		json.NewEncoder(w).Encode(OAuthTokenStatus{
			ServiceName: service,
			Connected:   true,
			Scopes:      token.Scopes,
			ExpiresAt:   token.ExpiresAt,
			ExpiresSoon: expiresSoon,
			CreatedAt:   token.CreatedAt,
		})
	default:
		http.Error(w, fmt.Sprintf(`{"error":"unknown action %q, use: authorize, callback, revoke, status"}`, action), http.StatusBadRequest)
	}
}

// --- Tool Handlers ---

// toolOAuthStatus lists connected OAuth services.
func toolOAuthStatus(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	statuses, err := listOAuthTokenStatuses(cfg.HistoryDB, cfg.OAuth.EncryptionKey)
	if err != nil {
		return "", fmt.Errorf("list oauth statuses: %w", err)
	}

	if len(statuses) == 0 {
		return "No OAuth services connected. Configure services in config.json under \"oauth.services\" and use the authorize endpoint to connect.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Connected OAuth services (%d):\n", len(statuses)))
	for _, s := range statuses {
		status := "connected"
		if s.ExpiresSoon {
			status = "expires soon"
		}
		sb.WriteString(fmt.Sprintf("  - %s: %s", s.ServiceName, status))
		if s.Scopes != "" {
			sb.WriteString(fmt.Sprintf(" (scopes: %s)", s.Scopes))
		}
		if s.ExpiresAt != "" {
			sb.WriteString(fmt.Sprintf(" (expires: %s)", s.ExpiresAt))
		}
		sb.WriteString("\n")
	}
	return sb.String(), nil
}

// toolOAuthRequest makes an authenticated HTTP request.
func toolOAuthRequest(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Service string `json:"service"`
		Method  string `json:"method"`
		URL     string `json:"url"`
		Body    string `json:"body"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("parse input: %w", err)
	}
	if args.Service == "" || args.URL == "" {
		return "", fmt.Errorf("service and url are required")
	}
	if args.Method == "" {
		args.Method = "GET"
	}

	mgr := newOAuthManager(cfg)
	var body io.Reader
	if args.Body != "" {
		body = strings.NewReader(args.Body)
	}

	resp, err := mgr.Request(ctx, args.Service, args.Method, args.URL, body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 100*1024)) // 100KB limit
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	return fmt.Sprintf("HTTP %d\n%s", resp.StatusCode, string(respBody)), nil
}

// toolOAuthAuthorize returns the authorization URL for a service.
func toolOAuthAuthorize(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Service string `json:"service"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("parse input: %w", err)
	}
	if args.Service == "" {
		return "", fmt.Errorf("service is required")
	}

	mgr := newOAuthManager(cfg)
	svcCfg, err := mgr.resolveServiceConfig(args.Service)
	if err != nil {
		return "", err
	}

	redirectURL := svcCfg.RedirectURL
	if redirectURL == "" {
		base := cfg.OAuth.RedirectBase
		if base == "" {
			base = "http://localhost" + cfg.ListenAddr
		}
		redirectURL = base + "/api/oauth/" + args.Service + "/callback"
	}

	// Build authorization URL (without state — state is handled by the HTTP flow).
	params := url.Values{
		"client_id":     {svcCfg.ClientID},
		"redirect_uri":  {redirectURL},
		"response_type": {"code"},
	}
	if len(svcCfg.Scopes) > 0 {
		params.Set("scope", strings.Join(svcCfg.Scopes, " "))
	}
	for k, v := range svcCfg.ExtraParams {
		params.Set(k, v)
	}

	authorizeURL := fmt.Sprintf("%s/api/oauth/%s/authorize", strings.TrimRight(cfg.OAuth.RedirectBase, "/"), args.Service)
	if cfg.OAuth.RedirectBase == "" {
		authorizeURL = fmt.Sprintf("http://localhost%s/api/oauth/%s/authorize", cfg.ListenAddr, args.Service)
	}

	return fmt.Sprintf("To connect %s, visit this URL:\n%s\n\nThe authorization flow will handle CSRF protection and token exchange automatically.", args.Service, authorizeURL), nil
}
