package main

// --- P15.5: Google Chat Channel ---

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// --- Google Chat Config ---

// GoogleChatConfig holds configuration for Google Chat integration.
type GoogleChatConfig struct {
	Enabled           bool   `json:"enabled,omitempty"`
	ServiceAccountKey string `json:"serviceAccountKey,omitempty"` // JSON key file path or $ENV_VAR
	WebhookPath       string `json:"webhookPath,omitempty"`       // default "/api/gchat/webhook"
	DefaultAgent       string `json:"defaultAgent,omitempty"`       // agent role for Google Chat messages
}

// webhookPathOrDefault returns the configured webhook path or default.
func (c GoogleChatConfig) webhookPathOrDefault() string {
	if c.WebhookPath != "" {
		return c.WebhookPath
	}
	return "/api/gchat/webhook"
}

// --- Google Chat Message Types ---

// gchatEvent represents an incoming event from Google Chat.
type gchatEvent struct {
	Type    string         `json:"type"`    // "MESSAGE", "ADDED_TO_SPACE", "REMOVED_FROM_SPACE", "CARD_CLICKED"
	EventTime string       `json:"eventTime"`
	Space   gchatSpace     `json:"space"`
	Message *gchatMessage  `json:"message,omitempty"`
	User    gchatUser      `json:"user"`
	Action  *gchatAction   `json:"action,omitempty"`
}

// gchatSpace represents a Google Chat space (room/DM).
type gchatSpace struct {
	Name        string `json:"name"`        // "spaces/{space_id}"
	Type        string `json:"type"`        // "ROOM", "DM"
	DisplayName string `json:"displayName"`
}

// gchatMessage represents a message in Google Chat.
type gchatMessage struct {
	Name         string              `json:"name"`         // "spaces/{space}/messages/{message}"
	Sender       gchatUser           `json:"sender"`
	CreateTime   string              `json:"createTime"`
	Text         string              `json:"text"`
	Thread       *gchatThread        `json:"thread,omitempty"`
	ArgumentText string              `json:"argumentText"` // text after @bot mention
	Annotations  []gchatAnnotation   `json:"annotations,omitempty"`
	Attachment   []gchatAttachment   `json:"attachment,omitempty"`
}

// gchatUser represents a Google Chat user.
type gchatUser struct {
	Name        string `json:"name"`        // "users/{user_id}"
	DisplayName string `json:"displayName"`
	AvatarUrl   string `json:"avatarUrl,omitempty"`
	Email       string `json:"email,omitempty"`
	Type        string `json:"type"` // "HUMAN", "BOT"
}

// gchatThread represents a message thread.
type gchatThread struct {
	Name string `json:"name"` // "spaces/{space}/threads/{thread}"
}

// gchatAnnotation represents mentions or other annotations.
type gchatAnnotation struct {
	Type         string         `json:"type"` // "USER_MENTION"
	StartIndex   int            `json:"startIndex"`
	Length       int            `json:"length"`
	UserMention  *gchatUserMention `json:"userMention,omitempty"`
}

// gchatUserMention represents a user mention.
type gchatUserMention struct {
	User gchatUser `json:"user"`
	Type string    `json:"type"` // "ADD", "MENTION"
}

// gchatAttachment represents a file attachment.
type gchatAttachment struct {
	Name        string `json:"name"`
	ContentName string `json:"contentName"`
	ContentType string `json:"contentType"`
	Source      string `json:"source"` // "UPLOADED_CONTENT", "DRIVE_FILE"
}

// gchatAction represents a card click action.
type gchatAction struct {
	ActionMethodName string `json:"actionMethodName"`
	Parameters       []gchatActionParameter `json:"parameters,omitempty"`
}

// gchatActionParameter represents a parameter for a card action.
type gchatActionParameter struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// --- Google Chat Card Types ---

// gchatCard represents a Google Chat card message.
type gchatCard struct {
	Header   *gchatCardHeader  `json:"header,omitempty"`
	Sections []gchatCardSection `json:"sections"`
}

// gchatCardHeader represents a card header.
type gchatCardHeader struct {
	Title    string `json:"title"`
	Subtitle string `json:"subtitle,omitempty"`
	ImageUrl string `json:"imageUrl,omitempty"`
}

// gchatCardSection represents a card section.
type gchatCardSection struct {
	Header  string            `json:"header,omitempty"`
	Widgets []gchatCardWidget `json:"widgets"`
}

// gchatCardWidget represents a card widget.
type gchatCardWidget struct {
	TextParagraph *gchatTextParagraph `json:"textParagraph,omitempty"`
	KeyValue      *gchatKeyValue      `json:"keyValue,omitempty"`
	Buttons       []gchatButton       `json:"buttons,omitempty"`
}

// gchatTextParagraph represents a text paragraph widget.
type gchatTextParagraph struct {
	Text string `json:"text"`
}

// gchatKeyValue represents a key-value widget.
type gchatKeyValue struct {
	TopLabel string `json:"topLabel,omitempty"`
	Content  string `json:"content"`
	BottomLabel string `json:"bottomLabel,omitempty"`
	Icon     string `json:"icon,omitempty"`
}

// gchatButton represents a button widget.
type gchatButton struct {
	TextButton *gchatTextButton `json:"textButton,omitempty"`
	ImageButton *gchatImageButton `json:"imageButton,omitempty"`
}

// gchatTextButton represents a text button.
type gchatTextButton struct {
	Text    string        `json:"text"`
	OnClick gchatOnClick  `json:"onClick"`
}

// gchatImageButton represents an image button.
type gchatImageButton struct {
	Icon    string        `json:"icon"` // "STAR", "BOOKMARK", etc.
	OnClick gchatOnClick  `json:"onClick"`
}

// gchatOnClick represents a button click action.
type gchatOnClick struct {
	Action   *gchatAction `json:"action,omitempty"`
	OpenLink *gchatOpenLink `json:"openLink,omitempty"`
}

// gchatOpenLink represents a link to open.
type gchatOpenLink struct {
	Url string `json:"url"`
}

// --- Google Chat Send Request ---

// gchatSendRequest represents a request to send a message to Google Chat.
type gchatSendRequest struct {
	Text   string      `json:"text,omitempty"`
	Cards  []gchatCard `json:"cards,omitempty"`
	Thread *gchatThread `json:"thread,omitempty"`
}

// --- Service Account Types ---

// serviceAccountKey represents a Google service account key JSON.
type serviceAccountKey struct {
	Type                    string `json:"type"`
	ProjectID               string `json:"project_id"`
	PrivateKeyID            string `json:"private_key_id"`
	PrivateKey              string `json:"private_key"`
	ClientEmail             string `json:"client_email"`
	ClientID                string `json:"client_id"`
	AuthURI                 string `json:"auth_uri"`
	TokenURI                string `json:"token_uri"`
	AuthProviderX509CertURL string `json:"auth_provider_x509_cert_url"`
	ClientX509CertURL       string `json:"client_x509_cert_url"`
}

// --- Google Chat Bot ---

// GoogleChatBot handles incoming Google Chat messages and sends responses.
type GoogleChatBot struct {
	cfg     *Config
	state   *dispatchState
	sem      chan struct{}
	childSem chan struct{}
	saKey    *serviceAccountKey // parsed service account key
	privKey *rsa.PrivateKey    // parsed RSA private key

	// Token cache.
	tokenCache    string
	tokenExpiry   time.Time
	tokenMu       sync.Mutex

	// Dedup: track recently processed message IDs.
	processed     map[string]time.Time
	processedSize int
	mu            sync.Mutex

	// httpClient for API calls (replaceable for testing).
	httpClient *http.Client
}

// newGoogleChatBot creates a new GoogleChatBot instance.
func newGoogleChatBot(cfg *Config, state *dispatchState, sem, childSem chan struct{}) (*GoogleChatBot, error) {
	bot := &GoogleChatBot{
		cfg:        cfg,
		state:      state,
		sem:        sem,
		childSem:   childSem,
		processed:  make(map[string]time.Time),
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}

	// Load service account key.
	keyPath := cfg.GoogleChat.ServiceAccountKey
	if keyPath == "" {
		return nil, fmt.Errorf("gchat: serviceAccountKey not configured")
	}

	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("gchat: failed to read service account key: %w", err)
	}

	var saKey serviceAccountKey
	if err := json.Unmarshal(keyData, &saKey); err != nil {
		return nil, fmt.Errorf("gchat: failed to parse service account key: %w", err)
	}
	bot.saKey = &saKey

	// Parse RSA private key.
	block, _ := pem.Decode([]byte(saKey.PrivateKey))
	if block == nil {
		return nil, fmt.Errorf("gchat: failed to decode PEM private key")
	}

	privKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		// Try PKCS1 format.
		privKey, err = x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("gchat: failed to parse private key: %w", err)
		}
	}

	rsaKey, ok := privKey.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("gchat: private key is not RSA")
	}
	bot.privKey = rsaKey

	logInfo("gchat: initialized", "clientEmail", saKey.ClientEmail)
	return bot, nil
}

// HandleWebhook handles incoming Google Chat events.
func (bot *GoogleChatBot) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		logError("gchat: failed to read webhook body", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	var event gchatEvent
	if err := json.Unmarshal(body, &event); err != nil {
		logError("gchat: failed to parse webhook event", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	logDebug("gchat: received event", "type", event.Type, "space", event.Space.Name)

	// Handle different event types.
	switch event.Type {
	case "MESSAGE":
		bot.handleMessage(w, &event)
	case "ADDED_TO_SPACE":
		bot.handleAddedToSpace(w, &event)
	case "REMOVED_FROM_SPACE":
		bot.handleRemovedFromSpace(w, &event)
	case "CARD_CLICKED":
		bot.handleCardClicked(w, &event)
	default:
		logWarn("gchat: unknown event type", "type", event.Type)
		w.WriteHeader(http.StatusOK)
	}
}

// handleMessage processes a MESSAGE event.
func (bot *GoogleChatBot) handleMessage(w http.ResponseWriter, event *gchatEvent) {
	if event.Message == nil {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Dedup check.
	msgID := event.Message.Name
	if bot.isDuplicate(msgID) {
		logDebug("gchat: duplicate message", "msgID", msgID)
		w.WriteHeader(http.StatusOK)
		return
	}
	bot.markProcessed(msgID)

	// Ignore bot messages.
	if event.Message.Sender.Type == "BOT" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Extract text (use argumentText if available, else text).
	text := strings.TrimSpace(event.Message.ArgumentText)
	if text == "" {
		text = strings.TrimSpace(event.Message.Text)
	}
	if text == "" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Determine agent.
	role := bot.cfg.GoogleChat.DefaultAgent
	if role == "" {
		role = bot.cfg.SmartDispatch.DefaultAgent
	}

	// Dispatch task.
	spaceName := event.Space.Name
	threadName := ""
	if event.Message.Thread != nil {
		threadName = event.Message.Thread.Name
	}

	go bot.dispatchTask(spaceName, threadName, role, text, event.User.DisplayName)

	// Send immediate acknowledgment.
	resp := gchatSendRequest{Text: "Processing..."}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleAddedToSpace processes an ADDED_TO_SPACE event.
func (bot *GoogleChatBot) handleAddedToSpace(w http.ResponseWriter, event *gchatEvent) {
	logInfo("gchat: added to space", "space", event.Space.Name, "type", event.Space.Type)

	welcomeText := fmt.Sprintf("Hello! I'm Tetora bot. Send me a message to get started.")
	resp := gchatSendRequest{Text: welcomeText}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleRemovedFromSpace processes a REMOVED_FROM_SPACE event.
func (bot *GoogleChatBot) handleRemovedFromSpace(w http.ResponseWriter, event *gchatEvent) {
	logInfo("gchat: removed from space", "space", event.Space.Name)
	w.WriteHeader(http.StatusOK)
}

// handleCardClicked processes a CARD_CLICKED event.
func (bot *GoogleChatBot) handleCardClicked(w http.ResponseWriter, event *gchatEvent) {
	logDebug("gchat: card clicked", "action", event.Action)

	// Could dispatch task based on action parameters.
	w.WriteHeader(http.StatusOK)
}

// dispatchTask dispatches a task to the agent.
func (bot *GoogleChatBot) dispatchTask(spaceName, threadName, role, text, userName string) {
	ctx := context.Background()

	// Acquire semaphore.
	select {
	case bot.sem <- struct{}{}:
		defer func() { <-bot.sem }()
	default:
		bot.sendTextMessage(spaceName, threadName, "System busy. Please try again later.")
		return
	}

	// Build task from agent config.
	roleConfig, exists := bot.cfg.Agents[role]
	if !exists {
		bot.sendTextMessage(spaceName, threadName, fmt.Sprintf("Unknown agent: %s", role))
		return
	}

	task := Task{
		ID:             fmt.Sprintf("gchat-%d", time.Now().UnixNano()),
		Name:           fmt.Sprintf("gchat-%s", userName),
		Prompt:         text,
		Model:          roleConfig.Model,
		Provider:       roleConfig.Provider,
		Workdir:        bot.cfg.DefaultWorkdir,
		Timeout:        bot.cfg.DefaultTimeout,
		Budget:         bot.cfg.DefaultBudget,
		PermissionMode: roleConfig.PermissionMode,
	}

	result := runTask(ctx, bot.cfg, task, bot.state)

	// Send result back to Google Chat.
	var responseText string
	if result.Error != "" {
		responseText = fmt.Sprintf("Error: %s", result.Error)
	} else {
		responseText = result.Output
	}

	if err := bot.sendTextMessage(spaceName, threadName, responseText); err != nil {
		logError("gchat: failed to send response", "space", spaceName, "error", err)
	}
}

// sendTextMessage sends a text message to a Google Chat space.
func (bot *GoogleChatBot) sendTextMessage(spaceName, threadName, text string) error {
	req := gchatSendRequest{Text: text}
	if threadName != "" {
		req.Thread = &gchatThread{Name: threadName}
	}
	return bot.sendMessage(spaceName, req)
}

// sendCardMessage sends a card message to a Google Chat space.
func (bot *GoogleChatBot) sendCardMessage(spaceName, threadName string, card gchatCard) error {
	req := gchatSendRequest{Cards: []gchatCard{card}}
	if threadName != "" {
		req.Thread = &gchatThread{Name: threadName}
	}
	return bot.sendMessage(spaceName, req)
}

// sendMessage sends a message to a Google Chat space.
func (bot *GoogleChatBot) sendMessage(spaceName string, req gchatSendRequest) error {
	token, err := bot.getAccessToken()
	if err != nil {
		return fmt.Errorf("failed to get access token: %w", err)
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("https://chat.googleapis.com/v1/%s/messages", spaceName)
	httpReq, err := http.NewRequest("POST", url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token)

	resp, err := bot.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API error: HTTP %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// getAccessToken returns a valid OAuth2 access token, generating a new one if needed.
func (bot *GoogleChatBot) getAccessToken() (string, error) {
	bot.tokenMu.Lock()
	defer bot.tokenMu.Unlock()

	// Return cached token if still valid.
	if bot.tokenCache != "" && time.Now().Before(bot.tokenExpiry) {
		return bot.tokenCache, nil
	}

	// Generate new JWT.
	jwt, err := bot.createJWT()
	if err != nil {
		return "", fmt.Errorf("failed to create JWT: %w", err)
	}

	// Exchange JWT for access token.
	token, expiresIn, err := bot.exchangeJWT(jwt)
	if err != nil {
		return "", fmt.Errorf("failed to exchange JWT: %w", err)
	}

	// Cache token.
	bot.tokenCache = token
	bot.tokenExpiry = time.Now().Add(time.Duration(expiresIn-60) * time.Second) // 60s buffer

	return token, nil
}

// createJWT creates a JWT for service account authentication.
func (bot *GoogleChatBot) createJWT() (string, error) {
	now := time.Now().Unix()
	claims := map[string]interface{}{
		"iss":   bot.saKey.ClientEmail,
		"scope": "https://www.googleapis.com/auth/chat.bot",
		"aud":   "https://oauth2.googleapis.com/token",
		"exp":   now + 3600,
		"iat":   now,
	}

	// Build header.
	header := map[string]string{
		"alg": "RS256",
		"typ": "JWT",
	}
	headerJSON, _ := json.Marshal(header)
	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)

	// Build claims.
	claimsJSON, _ := json.Marshal(claims)
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)

	// Sign.
	signInput := headerB64 + "." + claimsB64
	hash := sha256.Sum256([]byte(signInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, bot.privKey, crypto.SHA256, hash[:])
	if err != nil {
		return "", fmt.Errorf("failed to sign JWT: %w", err)
	}
	signatureB64 := base64.RawURLEncoding.EncodeToString(signature)

	jwt := signInput + "." + signatureB64
	return jwt, nil
}

// exchangeJWT exchanges a JWT for an OAuth2 access token.
func (bot *GoogleChatBot) exchangeJWT(jwt string) (token string, expiresIn int, err error) {
	payload := fmt.Sprintf("grant_type=urn:ietf:params:oauth:grant-type:jwt-bearer&assertion=%s", jwt)

	req, err := http.NewRequest("POST", "https://oauth2.googleapis.com/token", strings.NewReader(payload))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := bot.httpClient.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return "", 0, fmt.Errorf("token exchange failed: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		TokenType   string `json:"token_type"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", 0, fmt.Errorf("failed to parse token response: %w", err)
	}

	return result.AccessToken, result.ExpiresIn, nil
}

// isDuplicate checks if a message ID has been processed recently.
func (bot *GoogleChatBot) isDuplicate(msgID string) bool {
	bot.mu.Lock()
	defer bot.mu.Unlock()

	// Clean old entries (older than 5 minutes).
	cutoff := time.Now().Add(-5 * time.Minute)
	for id, t := range bot.processed {
		if t.Before(cutoff) {
			delete(bot.processed, id)
			bot.processedSize--
		}
	}

	// Limit map size.
	if bot.processedSize > 10000 {
		bot.processed = make(map[string]time.Time)
		bot.processedSize = 0
	}

	_, exists := bot.processed[msgID]
	return exists
}

// markProcessed marks a message ID as processed.
func (bot *GoogleChatBot) markProcessed(msgID string) {
	bot.mu.Lock()
	defer bot.mu.Unlock()
	bot.processed[msgID] = time.Now()
	bot.processedSize++
}

// --- GoogleChatNotifier implements Notifier interface ---

// GoogleChatNotifier sends notifications to Google Chat.
type GoogleChatNotifier struct {
	Bot       *GoogleChatBot
	SpaceName string // "spaces/{space_id}"
}

func (g *GoogleChatNotifier) Send(text string) error {
	return g.Bot.sendTextMessage(g.SpaceName, "", text)
}

func (g *GoogleChatNotifier) Name() string {
	return "gchat"
}
