package main

// --- P15.1: LINE Channel ---

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// --- LINE Config ---

// LINEConfig holds configuration for LINE Messaging API integration.
type LINEConfig struct {
	Enabled            bool   `json:"enabled,omitempty"`
	ChannelSecret      string `json:"channelSecret,omitempty"`      // $ENV_VAR supported, for webhook signature
	ChannelAccessToken string `json:"channelAccessToken,omitempty"` // $ENV_VAR supported, for API calls
	WebhookPath        string `json:"webhookPath,omitempty"`        // default "/api/line/webhook"
	DefaultRole        string `json:"defaultRole,omitempty"`        // agent role for LINE messages
}

// webhookPathOrDefault returns the configured webhook path or default "/api/line/webhook".
func (c LINEConfig) webhookPathOrDefault() string {
	if c.WebhookPath != "" {
		return c.WebhookPath
	}
	return "/api/line/webhook"
}

// --- LINE Webhook Event Types ---

// lineWebhookBody is the top-level webhook payload from LINE Platform.
type lineWebhookBody struct {
	Destination string      `json:"destination"`
	Events      []lineEvent `json:"events"`
}

// lineEvent represents a single webhook event from LINE.
type lineEvent struct {
	Type       string      `json:"type"` // "message", "follow", "unfollow", "join", "leave", "postback"
	Timestamp  int64       `json:"timestamp"`
	ReplyToken string      `json:"replyToken,omitempty"`
	Source     lineSource  `json:"source"`
	Message    *lineMsg    `json:"message,omitempty"`
	Postback   *linePostback `json:"postback,omitempty"`
}

// lineSource identifies the source of an event.
type lineSource struct {
	Type    string `json:"type"`              // "user", "group", "room"
	UserID  string `json:"userId,omitempty"`
	GroupID string `json:"groupId,omitempty"`
	RoomID  string `json:"roomId,omitempty"`
}

// lineMsg represents an incoming message.
type lineMsg struct {
	ID        string `json:"id"`
	Type      string `json:"type"` // "text", "image", "video", "audio", "sticker"
	Text      string `json:"text,omitempty"`
	StickerID string `json:"stickerId,omitempty"`
	PackageID string `json:"packageId,omitempty"`
}

// linePostback represents a postback event.
type linePostback struct {
	Data string `json:"data"`
}

// --- LINE Message Types ---

// lineMessage is a message to send via LINE API.
type lineMessage struct {
	Type               string          `json:"type"`                         // "text", "image", "flex"
	Text               string          `json:"text,omitempty"`               // for text messages
	AltText            string          `json:"altText,omitempty"`            // for flex messages
	Contents           json.RawMessage `json:"contents,omitempty"`           // for flex messages
	OriginalContentURL string          `json:"originalContentUrl,omitempty"` // for image/video/audio
	PreviewImageURL    string          `json:"previewImageUrl,omitempty"`    // for image/video
	QuickReply         *lineQuickReply `json:"quickReply,omitempty"`         // quick reply buttons
}

// lineQuickReply holds quick reply items.
type lineQuickReply struct {
	Items []lineQuickReplyItem `json:"items"`
}

// lineQuickReplyItem is a single quick reply button.
type lineQuickReplyItem struct {
	Type   string          `json:"type"` // "action"
	Action lineQuickAction `json:"action"`
}

// lineQuickAction is the action of a quick reply item.
type lineQuickAction struct {
	Type  string `json:"type"`            // "message", "postback", "uri"
	Label string `json:"label"`
	Text  string `json:"text,omitempty"`  // for "message" type
	Data  string `json:"data,omitempty"`  // for "postback" type
	URI   string `json:"uri,omitempty"`   // for "uri" type
}

// lineProfile represents a user profile from LINE API.
type lineProfile struct {
	DisplayName   string `json:"displayName"`
	UserID        string `json:"userId"`
	PictureURL    string `json:"pictureUrl,omitempty"`
	StatusMessage string `json:"statusMessage,omitempty"`
	Language      string `json:"language,omitempty"`
}

// --- LINE Bot ---

// LINEBot handles incoming LINE Messaging API webhook events.
type LINEBot struct {
	cfg     *Config
	state   *dispatchState
	sem     chan struct{}
	apiBase string // "https://api.line.me/v2/bot"

	// Dedup: track recently processed message IDs.
	processed     map[string]time.Time
	processedSize int
	mu            sync.Mutex

	// httpClient for API calls (replaceable for testing).
	httpClient *http.Client
}

func newLINEBot(cfg *Config, state *dispatchState, sem chan struct{}) *LINEBot {
	return &LINEBot{
		cfg:        cfg,
		state:      state,
		sem:        sem,
		apiBase:    "https://api.line.me/v2/bot",
		processed:  make(map[string]time.Time),
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// HandleWebhook handles incoming LINE webhook events.
// POST = incoming events from LINE Platform
func (lb *LINEBot) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read body for signature verification.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		logError("line: read body failed", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	r.Body.Close()

	// Verify HMAC-SHA256 signature.
	if lb.cfg.LINE.ChannelSecret != "" {
		sig := r.Header.Get("X-Line-Signature")
		if !verifyLINESignature(lb.cfg.LINE.ChannelSecret, body, sig) {
			logWarn("line: signature verification failed")
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
	}

	// Parse webhook payload.
	var hook lineWebhookBody
	if err := json.Unmarshal(body, &hook); err != nil {
		logError("line: parse webhook failed", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// LINE expects 200 OK immediately to prevent retries.
	w.WriteHeader(http.StatusOK)

	// Process events asynchronously.
	go lb.processEvents(hook.Events)
}

// processEvents processes incoming LINE webhook events.
func (lb *LINEBot) processEvents(events []lineEvent) {
	for _, event := range events {
		switch event.Type {
		case "message":
			lb.handleMessageEvent(event)
		case "postback":
			lb.handlePostbackEvent(event)
		case "follow":
			lb.handleFollowEvent(event)
		case "join":
			lb.handleJoinEvent(event)
		case "unfollow", "leave":
			logInfo("line: user/group event", "type", event.Type, "source", event.Source.UserID)
		default:
			logDebug("line: unhandled event type", "type", event.Type)
		}
	}
}

// handleMessageEvent processes a message event.
func (lb *LINEBot) handleMessageEvent(event lineEvent) {
	if event.Message == nil {
		return
	}

	// Dedup: check if already processed.
	lb.mu.Lock()
	if _, seen := lb.processed[event.Message.ID]; seen {
		lb.mu.Unlock()
		logDebug("line: duplicate message ignored", "msgID", event.Message.ID)
		return
	}
	lb.processed[event.Message.ID] = time.Now()
	lb.processedSize++

	// Cleanup old entries every 1000 messages.
	if lb.processedSize > 1000 {
		cutoff := time.Now().Add(-1 * time.Hour)
		for id, t := range lb.processed {
			if t.Before(cutoff) {
				delete(lb.processed, id)
				lb.processedSize--
			}
		}
	}
	lb.mu.Unlock()

	// Only process text messages for dispatch.
	if event.Message.Type != "text" || event.Message.Text == "" {
		logDebug("line: non-text message ignored", "msgID", event.Message.ID, "type", event.Message.Type)
		return
	}

	text := strings.TrimSpace(event.Message.Text)
	if text == "" {
		return
	}

	// Determine conversation target (user or group).
	targetID := lb.resolveTargetID(event.Source)
	logInfo("line: received message", "from", event.Source.UserID, "target", targetID, "text", truncate(text, 100))

	// Dispatch to agent.
	lb.dispatchToAgent(text, event.Source.UserID, targetID, event.ReplyToken)
}

// handlePostbackEvent processes a postback event.
func (lb *LINEBot) handlePostbackEvent(event lineEvent) {
	if event.Postback == nil {
		return
	}

	logInfo("line: postback received", "data", event.Postback.Data, "from", event.Source.UserID)

	// Treat postback data as a prompt.
	targetID := lb.resolveTargetID(event.Source)
	lb.dispatchToAgent(event.Postback.Data, event.Source.UserID, targetID, event.ReplyToken)
}

// handleFollowEvent sends a welcome message when a user adds the bot.
func (lb *LINEBot) handleFollowEvent(event lineEvent) {
	logInfo("line: new follower", "userID", event.Source.UserID)

	if event.ReplyToken != "" {
		msgs := []lineMessage{{
			Type: "text",
			Text: "Welcome to Tetora! Send me a message and I'll help you.",
		}}
		if err := lb.sendReply(event.ReplyToken, msgs); err != nil {
			logError("line: welcome message failed", "error", err)
		}
	}
}

// handleJoinEvent logs when the bot joins a group/room.
func (lb *LINEBot) handleJoinEvent(event lineEvent) {
	logInfo("line: joined group/room", "groupID", event.Source.GroupID, "roomID", event.Source.RoomID)
}

// dispatchToAgent dispatches a message to the agent system and replies.
func (lb *LINEBot) dispatchToAgent(text, userID, targetID, replyToken string) {
	ctx := withTraceID(context.Background(), newTraceID("line"))
	dbPath := lb.cfg.HistoryDB

	// Route to determine role.
	role := lb.cfg.LINE.DefaultRole
	if role == "" {
		route := routeTask(ctx, lb.cfg, RouteRequest{Prompt: text, Source: "line"})
		role = route.Role
		logInfoCtx(ctx, "line route result", "role", role, "method", route.Method)
	}

	// Find or create session.
	chKey := channelSessionKey("line", userID, targetID)
	sess, err := getOrCreateChannelSession(dbPath, "line", chKey, role, "")
	if err != nil {
		logErrorCtx(ctx, "line session error", "error", err)
	}

	// Build context-aware prompt.
	contextPrompt := text
	if sess != nil {
		sessionCtx := buildSessionContext(dbPath, sess.ID, lb.cfg.Session.contextMessagesOrDefault())
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
		Source: "line",
	}
	fillDefaults(lb.cfg, &task)
	if sess != nil {
		task.SessionID = sess.ID
	}

	// Apply role-specific config.
	if role != "" {
		if soulPrompt, err := loadRolePrompt(lb.cfg, role); err == nil && soulPrompt != "" {
			task.SystemPrompt = soulPrompt
		}
		if rc, ok := lb.cfg.Roles[role]; ok {
			if rc.Model != "" {
				task.Model = rc.Model
			}
			if rc.PermissionMode != "" {
				task.PermissionMode = rc.PermissionMode
			}
		}
	}

	task.Prompt = expandPrompt(task.Prompt, "", lb.cfg.HistoryDB, role, lb.cfg.KnowledgeDir, lb.cfg)

	// Run task.
	taskStart := time.Now()
	result := runSingleTask(ctx, lb.cfg, task, lb.sem, role)

	// Record to history.
	recordHistory(lb.cfg.HistoryDB, task.ID, task.Name, task.Source, role, task, result,
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

	// LINE text messages have a 5000 character limit.
	if len(response) > 5000 {
		response = response[:4997] + "..."
	}

	// Send response: try reply first (free), fall back to push (paid).
	msgs := []lineMessage{{Type: "text", Text: response}}
	if replyToken != "" {
		if err := lb.sendReply(replyToken, msgs); err != nil {
			logWarn("line: reply failed, trying push", "error", err)
			if pushErr := lb.sendPush(targetID, msgs); pushErr != nil {
				logError("line: push also failed", "error", pushErr)
			}
		}
	} else {
		if err := lb.sendPush(targetID, msgs); err != nil {
			logError("line: push failed", "error", err)
		}
	}

	logInfoCtx(ctx, "line task complete", "taskID", task.ID, "status", result.Status, "cost", result.CostUSD)

	// Emit SSE event.
	if lb.state.broker != nil {
		lb.state.broker.Publish("line", SSEEvent{
			Type: "line",
			Data: map[string]interface{}{
				"from":   userID,
				"target": targetID,
				"taskID": task.ID,
				"status": result.Status,
				"cost":   result.CostUSD,
			},
		})
	}
}

// resolveTargetID determines the reply target (group/room/user).
func (lb *LINEBot) resolveTargetID(src lineSource) string {
	if src.GroupID != "" {
		return src.GroupID
	}
	if src.RoomID != "" {
		return src.RoomID
	}
	return src.UserID
}

// --- LINE Messaging API ---

// sendReply sends reply messages using a reply token (free, within 3-minute window).
func (lb *LINEBot) sendReply(replyToken string, messages []lineMessage) error {
	if replyToken == "" {
		return fmt.Errorf("line: empty reply token")
	}

	payload := map[string]interface{}{
		"replyToken": replyToken,
		"messages":   messages,
	}

	return lb.sendLINEAPIRequest(lb.apiBase+"/message/reply", payload)
}

// sendPush sends push messages to a user/group/room (costs money per message).
func (lb *LINEBot) sendPush(to string, messages []lineMessage) error {
	if to == "" {
		return fmt.Errorf("line: empty push target")
	}

	payload := map[string]interface{}{
		"to":       to,
		"messages": messages,
	}

	return lb.sendLINEAPIRequest(lb.apiBase+"/message/push", payload)
}

// getProfile fetches a user's LINE profile.
func (lb *LINEBot) getProfile(userID string) (*lineProfile, error) {
	if userID == "" {
		return nil, fmt.Errorf("line: empty user ID")
	}

	url := fmt.Sprintf("%s/profile/%s", lb.apiBase, userID)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("line: create profile request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+lb.cfg.LINE.ChannelAccessToken)

	resp, err := lb.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("line: profile request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("line: profile HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var profile lineProfile
	if err := json.NewDecoder(resp.Body).Decode(&profile); err != nil {
		return nil, fmt.Errorf("line: parse profile: %w", err)
	}

	return &profile, nil
}

// sendLINEAPIRequest sends a POST request to LINE Messaging API.
func (lb *LINEBot) sendLINEAPIRequest(url string, payload interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("line: marshal payload: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("line: create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+lb.cfg.LINE.ChannelAccessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := lb.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("line: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("line: HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	logDebug("line: API request sent", "url", url, "status", resp.StatusCode)
	return nil
}

// --- LINE Signature Verification ---

// verifyLINESignature verifies the X-Line-Signature header using HMAC-SHA256.
// The signature is base64-encoded HMAC-SHA256 of the request body with the channel secret.
func verifyLINESignature(channelSecret string, body []byte, signature string) bool {
	if channelSecret == "" {
		return true // skip if no secret configured
	}

	if signature == "" {
		return false
	}

	mac := hmac.New(sha256.New, []byte(channelSecret))
	mac.Write(body)
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(expected), []byte(signature))
}

// --- LINE Flex Message Builder ---

// buildFlexText creates a simple flex message with text content.
func buildFlexText(altText, text string) lineMessage {
	contents, _ := json.Marshal(map[string]interface{}{
		"type": "bubble",
		"body": map[string]interface{}{
			"type":   "box",
			"layout": "vertical",
			"contents": []map[string]interface{}{
				{
					"type": "text",
					"text": text,
					"wrap": true,
				},
			},
		},
	})

	return lineMessage{
		Type:     "flex",
		AltText:  altText,
		Contents: contents,
	}
}

// buildQuickReplyMessage attaches quick reply buttons to a text message.
func buildQuickReplyMessage(text string, options []string) lineMessage {
	items := make([]lineQuickReplyItem, 0, len(options))
	for _, opt := range options {
		items = append(items, lineQuickReplyItem{
			Type: "action",
			Action: lineQuickAction{
				Type:  "message",
				Label: opt,
				Text:  opt,
			},
		})
	}

	return lineMessage{
		Type: "text",
		Text: text,
		QuickReply: &lineQuickReply{
			Items: items,
		},
	}
}

// --- LINE Notification Integration ---

// LINENotifier sends notifications via LINE Push API.
type LINENotifier struct {
	Config LINEConfig
	ChatID string // user/group ID to send to
}

func (n *LINENotifier) Send(text string) error {
	if text == "" {
		return nil
	}
	// Truncate if too long.
	if len(text) > 5000 {
		text = text[:4997] + "..."
	}

	payload := map[string]interface{}{
		"to": n.ChatID,
		"messages": []lineMessage{
			{Type: "text", Text: text},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("line: marshal notification: %w", err)
	}

	req, err := http.NewRequest("POST", "https://api.line.me/v2/bot/message/push", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("line: create notification request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+n.Config.ChannelAccessToken)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("line: notification request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("line: notification HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

func (n *LINENotifier) Name() string { return "line" }
