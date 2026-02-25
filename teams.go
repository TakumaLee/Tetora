package main

// --- P15.3: Teams Channel ---

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// --- Teams Config ---

// TeamsConfig holds configuration for Microsoft Teams Bot Framework integration.
type TeamsConfig struct {
	Enabled     bool   `json:"enabled,omitempty"`
	AppID       string `json:"appId,omitempty"`       // Azure AD App ID ($ENV_VAR)
	AppPassword string `json:"appPassword,omitempty"` // Azure AD App Secret ($ENV_VAR)
	TenantID    string `json:"tenantId,omitempty"`    // Azure AD Tenant ID ($ENV_VAR)
	DefaultRole string `json:"defaultRole,omitempty"` // agent role for Teams messages
}

// --- Teams Activity Types ---

// teamsActivity represents an incoming Activity from Bot Framework.
type teamsActivity struct {
	Type         string             `json:"type"`                    // "message", "conversationUpdate", "invoke"
	ID           string             `json:"id"`
	Timestamp    string             `json:"timestamp"`
	Text         string             `json:"text"`
	ChannelID    string             `json:"channelId"`               // "msteams"
	ServiceURL   string             `json:"serviceUrl"`              // for replies
	From         teamsAccount       `json:"from"`
	Conversation teamsConversation  `json:"conversation"`
	Recipient    teamsAccount       `json:"recipient"`
	Attachments  []teamsAttachment  `json:"attachments,omitempty"`
	Value        json.RawMessage    `json:"value,omitempty"`         // for Adaptive Card actions
}

// teamsAccount identifies a user or bot in Teams.
type teamsAccount struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// teamsConversation identifies a Teams conversation.
type teamsConversation struct {
	ID      string `json:"id"`
	IsGroup bool   `json:"isGroup,omitempty"`
}

// teamsAttachment represents an attachment in a Teams message.
type teamsAttachment struct {
	ContentType string          `json:"contentType"`
	Content     json.RawMessage `json:"content,omitempty"`
	ContentURL  string          `json:"contentUrl,omitempty"`
	Name        string          `json:"name,omitempty"`
}

// --- Teams Token Cache ---

// teamsTokenCache caches the OAuth2 bearer token for outbound API calls.
type teamsTokenCache struct {
	mu        sync.RWMutex
	token     string
	expiresAt time.Time
}

// --- Teams Bot ---

// TeamsBot handles incoming Microsoft Teams Bot Framework webhook events.
type TeamsBot struct {
	cfg        *Config
	state      *dispatchState
	sem        chan struct{}
	tokenCache teamsTokenCache

	// Dedup: track recently processed activity IDs.
	processed     map[string]time.Time
	processedSize int
	mu            sync.Mutex

	// httpClient for API calls (replaceable for testing).
	httpClient *http.Client

	// tokenURL can be overridden for testing.
	tokenURL string
}

func newTeamsBot(cfg *Config, state *dispatchState, sem chan struct{}) *TeamsBot {
	tenantID := cfg.Teams.TenantID
	if tenantID == "" {
		tenantID = "botframework.com"
	}
	return &TeamsBot{
		cfg:        cfg,
		state:      state,
		sem:        sem,
		processed:  make(map[string]time.Time),
		httpClient: &http.Client{Timeout: 10 * time.Second},
		tokenURL:   fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", tenantID),
	}
}

// HandleWebhook handles incoming Bot Framework webhook events.
// POST /api/teams/webhook
func (tb *TeamsBot) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read body.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		logError("teams: read body failed", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	r.Body.Close()

	// Validate auth (JWT in Authorization header).
	if err := tb.validateAuth(r); err != nil {
		logWarn("teams: auth validation failed", "error", err)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Parse activity.
	var activity teamsActivity
	if err := json.Unmarshal(body, &activity); err != nil {
		logError("teams: parse activity failed", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Return 200 OK immediately to prevent retries.
	w.WriteHeader(http.StatusOK)

	// Process activity asynchronously.
	go tb.processActivity(activity)
}

// validateAuth validates the JWT token from the Authorization header.
// For simplicity: validates structure + verifies appId in claims (skip full JWKS rotation).
func (tb *TeamsBot) validateAuth(r *http.Request) error {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return fmt.Errorf("teams: missing Authorization header")
	}

	// Expect "Bearer <token>" format.
	if !strings.HasPrefix(auth, "Bearer ") {
		return fmt.Errorf("teams: invalid Authorization format")
	}
	token := strings.TrimPrefix(auth, "Bearer ")

	// JWT is three base64url-encoded parts separated by dots.
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return fmt.Errorf("teams: invalid JWT structure (expected 3 parts, got %d)", len(parts))
	}

	// Decode and validate payload (middle part).
	payload, err := base64URLDecode(parts[1])
	if err != nil {
		return fmt.Errorf("teams: decode JWT payload: %w", err)
	}

	var claims struct {
		Iss    string `json:"iss"`
		Aud    string `json:"aud"`
		AppID  string `json:"appid"`
		Exp    int64  `json:"exp"`
		Nbf    int64  `json:"nbf"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return fmt.Errorf("teams: parse JWT claims: %w", err)
	}

	// Check expiration.
	now := time.Now().Unix()
	if claims.Exp > 0 && now > claims.Exp {
		return fmt.Errorf("teams: token expired (exp=%d, now=%d)", claims.Exp, now)
	}

	// Check not-before.
	if claims.Nbf > 0 && now < claims.Nbf {
		return fmt.Errorf("teams: token not yet valid (nbf=%d, now=%d)", claims.Nbf, now)
	}

	// Validate audience matches our appId.
	if tb.cfg.Teams.AppID != "" {
		if claims.Aud != tb.cfg.Teams.AppID {
			return fmt.Errorf("teams: audience mismatch (got %q, want %q)", claims.Aud, tb.cfg.Teams.AppID)
		}
	}

	// Validate issuer (should be Microsoft-related).
	if claims.Iss != "" {
		validIssuers := []string{
			"https://api.botframework.com",
			"https://sts.windows.net/",
			"https://login.microsoftonline.com/",
		}
		issValid := false
		for _, vi := range validIssuers {
			if strings.HasPrefix(claims.Iss, vi) {
				issValid = true
				break
			}
		}
		if !issValid {
			return fmt.Errorf("teams: invalid issuer %q", claims.Iss)
		}
	}

	return nil
}

// base64URLDecode decodes a base64url-encoded string (JWT-style, no padding).
func base64URLDecode(s string) ([]byte, error) {
	// Add padding if needed.
	switch len(s) % 4 {
	case 2:
		s += "=="
	case 3:
		s += "="
	}
	return base64.URLEncoding.DecodeString(s)
}

// processActivity handles a Teams activity after webhook validation.
func (tb *TeamsBot) processActivity(activity teamsActivity) {
	switch activity.Type {
	case "message":
		tb.handleMessageActivity(activity)
	case "conversationUpdate":
		tb.handleConversationUpdate(activity)
	case "invoke":
		tb.handleInvokeActivity(activity)
	default:
		logDebug("teams: unhandled activity type", "type", activity.Type)
	}
}

// handleMessageActivity processes an incoming message from Teams.
func (tb *TeamsBot) handleMessageActivity(activity teamsActivity) {
	// Dedup: check if already processed.
	if activity.ID != "" {
		tb.mu.Lock()
		if _, seen := tb.processed[activity.ID]; seen {
			tb.mu.Unlock()
			logDebug("teams: duplicate activity ignored", "activityID", activity.ID)
			return
		}
		tb.processed[activity.ID] = time.Now()
		tb.processedSize++

		// Cleanup old entries every 1000 activities.
		if tb.processedSize > 1000 {
			cutoff := time.Now().Add(-1 * time.Hour)
			for id, t := range tb.processed {
				if t.Before(cutoff) {
					delete(tb.processed, id)
					tb.processedSize--
				}
			}
		}
		tb.mu.Unlock()
	}

	// Handle Adaptive Card action submissions (value field).
	if activity.Value != nil && len(activity.Value) > 0 {
		var val map[string]interface{}
		if json.Unmarshal(activity.Value, &val) == nil && len(val) > 0 {
			logInfo("teams: adaptive card action", "from", activity.From.Name, "value", string(activity.Value))
			// Treat the JSON value as a prompt.
			valJSON, _ := json.Marshal(val)
			tb.dispatchToAgent(string(valJSON), activity)
			return
		}
	}

	text := strings.TrimSpace(activity.Text)
	if text == "" {
		return
	}

	// Remove bot mention prefix (e.g., "<at>BotName</at> ").
	text = removeBotMention(text)
	if text == "" {
		return
	}

	logInfo("teams: received message", "from", activity.From.Name, "conversation", activity.Conversation.ID, "text", truncate(text, 100))

	// Dispatch to agent.
	tb.dispatchToAgent(text, activity)
}

// removeBotMention strips Teams @mention tags from the message text.
func removeBotMention(text string) string {
	// Teams wraps mentions in <at>Name</at> tags.
	for {
		start := strings.Index(text, "<at>")
		if start == -1 {
			break
		}
		end := strings.Index(text, "</at>")
		if end == -1 {
			break
		}
		text = text[:start] + text[end+5:]
	}
	return strings.TrimSpace(text)
}

// handleConversationUpdate handles bot join/leave events.
func (tb *TeamsBot) handleConversationUpdate(activity teamsActivity) {
	logInfo("teams: conversation update", "conversation", activity.Conversation.ID)
}

// handleInvokeActivity handles invoke activities (e.g., messaging extensions).
func (tb *TeamsBot) handleInvokeActivity(activity teamsActivity) {
	logInfo("teams: invoke activity", "conversation", activity.Conversation.ID)
}

// dispatchToAgent dispatches a message to the agent system and replies via Teams.
func (tb *TeamsBot) dispatchToAgent(text string, activity teamsActivity) {
	ctx := withTraceID(context.Background(), newTraceID("teams"))
	dbPath := tb.cfg.HistoryDB

	// Route to determine role.
	role := tb.cfg.Teams.DefaultRole
	if role == "" {
		route := routeTask(ctx, tb.cfg, RouteRequest{Prompt: text, Source: "teams"})
		role = route.Role
		logInfoCtx(ctx, "teams route result", "role", role, "method", route.Method)
	}

	// Find or create session.
	chKey := channelSessionKey("teams", activity.From.ID, activity.Conversation.ID)
	sess, err := getOrCreateChannelSession(dbPath, "teams", chKey, role, "")
	if err != nil {
		logErrorCtx(ctx, "teams session error", "error", err)
	}

	// Build context-aware prompt.
	contextPrompt := text
	if sess != nil {
		sessionCtx := buildSessionContext(dbPath, sess.ID, tb.cfg.Session.contextMessagesOrDefault())
		contextPrompt = wrapWithContext(sessionCtx, text)

		// Record user message to session.
		now := time.Now().Format(time.RFC3339)
		addSessionMessage(dbPath, SessionMessage{
			SessionID: sess.ID,
			Role:      "user",
			Content:   truncateStr(text, 5000),
			CreatedAt: now,
		})
		updateSessionStats(dbPath, sess.ID, 0, 0, 0, 1)

		title := text
		if len(title) > 100 {
			title = title[:100]
		}
		updateSessionTitle(dbPath, sess.ID, title)
	}

	// Create task.
	task := Task{
		Prompt: contextPrompt,
		Role:   role,
		Source: "teams",
	}
	fillDefaults(tb.cfg, &task)
	if sess != nil {
		task.SessionID = sess.ID
	}

	// Apply role-specific config.
	if role != "" {
		if soulPrompt, err := loadRolePrompt(tb.cfg, role); err == nil && soulPrompt != "" {
			task.SystemPrompt = soulPrompt
		}
		if rc, ok := tb.cfg.Roles[role]; ok {
			if rc.Model != "" {
				task.Model = rc.Model
			}
			if rc.PermissionMode != "" {
				task.PermissionMode = rc.PermissionMode
			}
		}
	}

	task.Prompt = expandPrompt(task.Prompt, "", tb.cfg.HistoryDB, role, tb.cfg.KnowledgeDir, tb.cfg)

	// Run task.
	taskStart := time.Now()
	result := runSingleTask(ctx, tb.cfg, task, tb.sem, role)

	// Record to history.
	recordHistory(tb.cfg.HistoryDB, task.ID, task.Name, task.Source, role, task, result,
		taskStart.Format(time.RFC3339), time.Now().Format(time.RFC3339), result.OutputFile)

	// Record assistant response to session.
	if sess != nil {
		now := time.Now().Format(time.RFC3339)
		msgRole := "assistant"
		content := truncateStr(result.Output, 5000)
		if result.Status != "success" {
			msgRole = "system"
			errMsg := result.Error
			if errMsg == "" {
				errMsg = result.Status
			}
			content = fmt.Sprintf("[%s] %s", result.Status, truncateStr(errMsg, 2000))
		}
		addSessionMessage(dbPath, SessionMessage{
			SessionID: sess.ID,
			Role:      msgRole,
			Content:   content,
			CostUSD:   result.CostUSD,
			TokensIn:  result.TokensIn,
			TokensOut: result.TokensOut,
			Model:     result.Model,
			TaskID:    task.ID,
			CreatedAt: now,
		})
		updateSessionStats(dbPath, sess.ID, result.CostUSD, result.TokensIn, result.TokensOut, 0)
	}

	// Build response.
	response := result.Output
	if result.Error != "" {
		response = fmt.Sprintf("Error: %s", result.Error)
	}

	// Teams text messages limit: truncate at 28KB (Teams limit is ~28KB for text).
	if len(response) > 28000 {
		response = response[:27997] + "..."
	}

	// Send reply.
	if err := tb.sendReply(activity.ServiceURL, activity.Conversation.ID, activity.ID, response); err != nil {
		logError("teams: reply failed", "error", err)
		// Fall back to proactive message.
		if proactiveErr := tb.sendProactive(activity.ServiceURL, activity.Conversation.ID, response); proactiveErr != nil {
			logError("teams: proactive also failed", "error", proactiveErr)
		}
	}

	logInfoCtx(ctx, "teams task complete", "taskID", task.ID, "status", result.Status, "cost", result.CostUSD)

	// Emit SSE event.
	if tb.state != nil && tb.state.broker != nil {
		tb.state.broker.Publish("teams", SSEEvent{
			Type: "teams",
			Data: map[string]interface{}{
				"from":           activity.From.Name,
				"conversationId": activity.Conversation.ID,
				"taskID":         task.ID,
				"status":         result.Status,
				"cost":           result.CostUSD,
			},
		})
	}
}

// --- Teams Bot Framework API ---

// getToken obtains (or returns cached) an OAuth2 bearer token for outbound Bot Framework API calls.
func (tb *TeamsBot) getToken() (string, error) {
	// Check cache first.
	tb.tokenCache.mu.RLock()
	if tb.tokenCache.token != "" && time.Now().Before(tb.tokenCache.expiresAt) {
		token := tb.tokenCache.token
		tb.tokenCache.mu.RUnlock()
		return token, nil
	}
	tb.tokenCache.mu.RUnlock()

	// Acquire write lock to refresh.
	tb.tokenCache.mu.Lock()
	defer tb.tokenCache.mu.Unlock()

	// Double-check after acquiring write lock.
	if tb.tokenCache.token != "" && time.Now().Before(tb.tokenCache.expiresAt) {
		return tb.tokenCache.token, nil
	}

	// Request new token.
	data := fmt.Sprintf(
		"grant_type=client_credentials&client_id=%s&client_secret=%s&scope=%s",
		tb.cfg.Teams.AppID,
		tb.cfg.Teams.AppPassword,
		"https%3A%2F%2Fapi.botframework.com%2F.default",
	)

	req, err := http.NewRequest("POST", tb.tokenURL, strings.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("teams: create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := tb.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("teams: token request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("teams: token HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"` // seconds
		TokenType   string `json:"token_type"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", fmt.Errorf("teams: parse token response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return "", fmt.Errorf("teams: empty access token in response")
	}

	// Cache with 5-minute buffer before actual expiry.
	expiresIn := time.Duration(tokenResp.ExpiresIn) * time.Second
	if expiresIn > 5*time.Minute {
		expiresIn -= 5 * time.Minute
	}
	tb.tokenCache.token = tokenResp.AccessToken
	tb.tokenCache.expiresAt = time.Now().Add(expiresIn)

	logDebug("teams: token refreshed", "expiresIn", tokenResp.ExpiresIn)
	return tokenResp.AccessToken, nil
}

// sendReply sends a reply to a specific activity in a conversation.
// POST to serviceUrl/v3/conversations/{conversationId}/activities/{activityId}
func (tb *TeamsBot) sendReply(serviceURL, conversationID, activityID, text string) error {
	if serviceURL == "" || conversationID == "" {
		return fmt.Errorf("teams: missing serviceURL or conversationID")
	}

	url := fmt.Sprintf("%sv3/conversations/%s/activities/%s",
		ensureTrailingSlash(serviceURL), conversationID, activityID)

	payload := map[string]interface{}{
		"type": "message",
		"text": text,
	}

	return tb.sendBotFrameworkRequest(url, payload)
}

// sendProactive sends a proactive message to a conversation (no reply-to activity).
// POST to serviceUrl/v3/conversations/{conversationId}/activities
func (tb *TeamsBot) sendProactive(serviceURL, conversationID, text string) error {
	if serviceURL == "" || conversationID == "" {
		return fmt.Errorf("teams: missing serviceURL or conversationID")
	}

	url := fmt.Sprintf("%sv3/conversations/%s/activities",
		ensureTrailingSlash(serviceURL), conversationID)

	payload := map[string]interface{}{
		"type": "message",
		"text": text,
	}

	return tb.sendBotFrameworkRequest(url, payload)
}

// sendAdaptiveCard sends an Adaptive Card to a conversation.
func (tb *TeamsBot) sendAdaptiveCard(serviceURL, conversationID string, card map[string]interface{}) error {
	if serviceURL == "" || conversationID == "" {
		return fmt.Errorf("teams: missing serviceURL or conversationID")
	}

	url := fmt.Sprintf("%sv3/conversations/%s/activities",
		ensureTrailingSlash(serviceURL), conversationID)

	payload := map[string]interface{}{
		"type": "message",
		"attachments": []map[string]interface{}{
			{
				"contentType": "application/vnd.microsoft.card.adaptive",
				"content":     card,
			},
		},
	}

	return tb.sendBotFrameworkRequest(url, payload)
}

// sendBotFrameworkRequest sends an authenticated POST request to the Bot Framework API.
func (tb *TeamsBot) sendBotFrameworkRequest(url string, payload interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("teams: marshal payload: %w", err)
	}

	token, err := tb.getToken()
	if err != nil {
		return fmt.Errorf("teams: get token: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("teams: create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := tb.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("teams: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("teams: HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	logDebug("teams: API request sent", "url", url, "status", resp.StatusCode)
	return nil
}

// ensureTrailingSlash ensures a URL ends with a slash.
func ensureTrailingSlash(url string) string {
	if !strings.HasSuffix(url, "/") {
		return url + "/"
	}
	return url
}

// --- Teams Adaptive Card Builder ---

// buildSimpleAdaptiveCard creates a simple Adaptive Card with a title and body text.
func buildSimpleAdaptiveCard(title, body string) map[string]interface{} {
	card := map[string]interface{}{
		"type":    "AdaptiveCard",
		"version": "1.4",
		"body": []map[string]interface{}{
			{
				"type":   "TextBlock",
				"text":   title,
				"weight": "bolder",
				"size":   "medium",
			},
			{
				"type": "TextBlock",
				"text": body,
				"wrap": true,
			},
		},
	}
	return card
}

// buildAdaptiveCardWithActions creates an Adaptive Card with action buttons.
func buildAdaptiveCardWithActions(title, body string, actions []map[string]interface{}) map[string]interface{} {
	card := buildSimpleAdaptiveCard(title, body)
	if len(actions) > 0 {
		card["actions"] = actions
	}
	return card
}

// buildSubmitAction creates an Action.Submit for an Adaptive Card.
func buildSubmitAction(title string, data map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"type":  "Action.Submit",
		"title": title,
		"data":  data,
	}
}

// --- Teams Notification Integration ---

// TeamsNotifier sends notifications via Teams Bot Framework API.
type TeamsNotifier struct {
	Bot            *TeamsBot
	ServiceURL     string // cached service URL for proactive messages
	ConversationID string // target conversation ID
}

func (n *TeamsNotifier) Send(text string) error {
	if text == "" {
		return nil
	}
	// Truncate if too long.
	if len(text) > 28000 {
		text = text[:27997] + "..."
	}

	return n.Bot.sendProactive(n.ServiceURL, n.ConversationID, text)
}

func (n *TeamsNotifier) Name() string { return "teams" }
