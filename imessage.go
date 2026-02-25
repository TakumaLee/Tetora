package main

// --- P20.2: iMessage via BlueBubbles ---

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// --- iMessage Config ---

// IMessageConfig holds configuration for BlueBubbles iMessage integration.
type IMessageConfig struct {
	Enabled      bool     `json:"enabled,omitempty"`
	ServerURL    string   `json:"serverUrl,omitempty"`           // BlueBubbles server URL, e.g. "http://localhost:1234"
	Password     string   `json:"password,omitempty"`            // BlueBubbles server password ($ENV_VAR)
	AllowedChats []string `json:"allowedChats,omitempty"`        // allowed chat GUIDs or phone numbers
	WebhookPath  string   `json:"webhookPath,omitempty"`         // default "/api/imessage/webhook"
	DefaultRole  string   `json:"defaultRole,omitempty"`         // agent role for iMessage messages
}

// webhookPathOrDefault returns the configured webhook path or default.
func (c IMessageConfig) webhookPathOrDefault() string {
	if c.WebhookPath != "" {
		return c.WebhookPath
	}
	return "/api/imessage/webhook"
}

// --- BlueBubbles Message Types ---

// BlueBubblesWebhookPayload represents an incoming webhook event from BlueBubbles.
type BlueBubblesWebhookPayload struct {
	Type string              `json:"type"`
	Data json.RawMessage     `json:"data"`
}

// BlueBubblesMessage represents a message from BlueBubbles.
type BlueBubblesMessage struct {
	GUID        string `json:"guid"`
	ChatGUID    string `json:"chatGuid"`
	Text        string `json:"text"`
	Handle      struct {
		Address string `json:"address"`
	} `json:"handle"`
	DateCreated int64  `json:"dateCreated"`
	IsFromMe    bool   `json:"isFromMe"`
}

// BBMessage is a simplified message for search/read results.
type BBMessage struct {
	GUID        string `json:"guid"`
	ChatGUID    string `json:"chatGuid"`
	Text        string `json:"text"`
	Handle      string `json:"handle"`
	DateCreated int64  `json:"dateCreated"`
	IsFromMe    bool   `json:"isFromMe"`
}

// --- IMessage Bot ---

// globalIMessageBot is the package-level iMessage bot instance.
var globalIMessageBot *IMessageBot

// IMessageBot handles incoming iMessage messages via BlueBubbles.
type IMessageBot struct {
	cfg       *Config
	state     *dispatchState
	sem       chan struct{}
	serverURL string
	password  string
	dedup     map[string]time.Time // message GUID -> timestamp for dedup
	mu        sync.Mutex
	client    *http.Client
}

// newIMessageBot creates a new IMessageBot instance.
func newIMessageBot(cfg *Config, state *dispatchState, sem chan struct{}) *IMessageBot {
	serverURL := strings.TrimRight(cfg.IMessage.ServerURL, "/")
	return &IMessageBot{
		cfg:       cfg,
		state:     state,
		sem:       sem,
		serverURL: serverURL,
		password:  cfg.IMessage.Password,
		dedup:     make(map[string]time.Time),
		client:    &http.Client{Timeout: 10 * time.Second},
	}
}

// --- Webhook Handler ---

// webhookHandler handles incoming BlueBubbles webhook POST events.
func (ib *IMessageBot) webhookHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		logError("imessage: read webhook body failed", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	r.Body.Close()

	var payload BlueBubblesWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		logError("imessage: parse webhook payload failed", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Return 200 OK immediately to prevent retries.
	w.WriteHeader(http.StatusOK)

	// Only handle new-message events.
	if payload.Type != "new-message" {
		logDebug("imessage: non-message event ignored", "type", payload.Type)
		return
	}

	var msg BlueBubblesMessage
	if err := json.Unmarshal(payload.Data, &msg); err != nil {
		logError("imessage: parse message data failed", "error", err)
		return
	}

	go ib.handleMessage(msg)
}

// handleMessage processes an incoming BlueBubbles message.
func (ib *IMessageBot) handleMessage(msg BlueBubblesMessage) {
	// Skip messages from self.
	if msg.IsFromMe {
		logDebug("imessage: skipping own message", "guid", msg.GUID)
		return
	}

	// Skip empty messages.
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		logDebug("imessage: empty message ignored", "guid", msg.GUID)
		return
	}

	// Dedup check.
	ib.mu.Lock()
	if _, seen := ib.dedup[msg.GUID]; seen {
		ib.mu.Unlock()
		logDebug("imessage: duplicate message ignored", "guid", msg.GUID)
		return
	}
	ib.dedup[msg.GUID] = time.Now()

	// Cleanup old dedup entries (older than 5 minutes).
	cutoff := time.Now().Add(-5 * time.Minute)
	for key, t := range ib.dedup {
		if t.Before(cutoff) {
			delete(ib.dedup, key)
		}
	}
	ib.mu.Unlock()

	// Check allowed chats.
	if len(ib.cfg.IMessage.AllowedChats) > 0 {
		allowed := false
		for _, chat := range ib.cfg.IMessage.AllowedChats {
			if chat == msg.ChatGUID || chat == msg.Handle.Address {
				allowed = true
				break
			}
		}
		if !allowed {
			logDebug("imessage: chat not in allowedChats", "chatGuid", msg.ChatGUID, "handle", msg.Handle.Address)
			return
		}
	}

	logInfo("imessage: received message", "from", msg.Handle.Address, "chatGuid", msg.ChatGUID, "text", truncate(text, 100))

	// Dispatch to agent.
	ib.dispatchToAgent(text, msg)
}

// dispatchToAgent dispatches a message to the agent system and replies via iMessage.
func (ib *IMessageBot) dispatchToAgent(text string, msg BlueBubblesMessage) {
	ctx := withTraceID(context.Background(), newTraceID("imessage"))
	dbPath := ib.cfg.HistoryDB

	// Route to determine role.
	role := ib.cfg.IMessage.DefaultRole
	if role == "" {
		route := routeTask(ctx, ib.cfg, RouteRequest{Prompt: text, Source: "imessage"})
		role = route.Role
		logInfoCtx(ctx, "imessage route result", "role", role, "method", route.Method)
	}

	// Find or create session.
	chKey := channelSessionKey("imessage", msg.Handle.Address, msg.ChatGUID)
	sess, err := getOrCreateChannelSession(dbPath, "imessage", chKey, role, "")
	if err != nil {
		logErrorCtx(ctx, "imessage session error", "error", err)
	}

	// Build context-aware prompt.
	contextPrompt := text
	if sess != nil {
		sessionCtx := buildSessionContext(dbPath, sess.ID, ib.cfg.Session.contextMessagesOrDefault())
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
		Source: "imessage",
	}
	fillDefaults(ib.cfg, &task)
	if sess != nil {
		task.SessionID = sess.ID
	}

	// Apply role-specific config.
	if role != "" {
		if soulPrompt, err := loadRolePrompt(ib.cfg, role); err == nil && soulPrompt != "" {
			task.SystemPrompt = soulPrompt
		}
		if rc, ok := ib.cfg.Roles[role]; ok {
			if rc.Model != "" {
				task.Model = rc.Model
			}
			if rc.PermissionMode != "" {
				task.PermissionMode = rc.PermissionMode
			}
		}
	}

	task.Prompt = expandPrompt(task.Prompt, "", ib.cfg.HistoryDB, role, ib.cfg.KnowledgeDir, ib.cfg)

	// Run task.
	taskStart := time.Now()
	result := runSingleTask(ctx, ib.cfg, task, ib.sem, role)

	// Record to history.
	recordHistory(ib.cfg.HistoryDB, task.ID, task.Name, task.Source, role, task, result,
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

	// iMessage has no strict length limit but keep reasonable.
	if len(response) > 4000 {
		response = response[:3997] + "..."
	}

	// Send response.
	if err := ib.sendMessage(msg.ChatGUID, response); err != nil {
		logError("imessage: send response failed", "error", err, "chatGuid", msg.ChatGUID)
	}

	logInfoCtx(ctx, "imessage task complete", "taskID", task.ID, "status", result.Status, "cost", result.CostUSD)

	// Emit SSE event.
	if ib.state != nil && ib.state.broker != nil {
		ib.state.broker.Publish("imessage", SSEEvent{
			Type: "imessage",
			Data: map[string]interface{}{
				"from":    msg.Handle.Address,
				"chatGuid": msg.ChatGUID,
				"taskID":  task.ID,
				"status":  result.Status,
				"cost":    result.CostUSD,
			},
		})
	}
}

// --- BlueBubbles API Methods ---

// sendMessage sends a text message to a chat via BlueBubbles API.
// POST /api/v1/message/text?password=...
func (ib *IMessageBot) sendMessage(chatGUID, text string) error {
	if chatGUID == "" || text == "" {
		return fmt.Errorf("imessage: empty chatGUID or message")
	}

	payload := map[string]string{
		"chatGuid": chatGUID,
		"message":  text,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("imessage: marshal send request: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/message/text?password=%s", ib.serverURL, ib.password)
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("imessage: create send request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := ib.client.Do(req)
	if err != nil {
		return fmt.Errorf("imessage: send request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("imessage: send HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	logDebug("imessage: message sent", "chatGuid", chatGUID, "status", resp.StatusCode)
	return nil
}

// searchMessages searches messages via BlueBubbles API.
// GET /api/v1/message/search?password=...&query=...&limit=...
func (ib *IMessageBot) searchMessages(query string, limit int) ([]BBMessage, error) {
	if query == "" {
		return nil, fmt.Errorf("imessage: empty search query")
	}
	if limit <= 0 {
		limit = 10
	}

	url := fmt.Sprintf("%s/api/v1/message/search?password=%s&query=%s&limit=%d",
		ib.serverURL, ib.password, query, limit)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("imessage: create search request: %w", err)
	}

	resp, err := ib.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("imessage: search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("imessage: search HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Data []BlueBubblesMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("imessage: parse search response: %w", err)
	}

	messages := make([]BBMessage, 0, len(result.Data))
	for _, m := range result.Data {
		messages = append(messages, BBMessage{
			GUID:        m.GUID,
			ChatGUID:    m.ChatGUID,
			Text:        m.Text,
			Handle:      m.Handle.Address,
			DateCreated: m.DateCreated,
			IsFromMe:    m.IsFromMe,
		})
	}
	return messages, nil
}

// readRecentMessages reads recent messages from a chat via BlueBubbles API.
// GET /api/v1/chat/{chatGUID}/message?password=...&limit=...
func (ib *IMessageBot) readRecentMessages(chatGUID string, limit int) ([]BBMessage, error) {
	if chatGUID == "" {
		return nil, fmt.Errorf("imessage: empty chatGUID")
	}
	if limit <= 0 {
		limit = 20
	}

	url := fmt.Sprintf("%s/api/v1/chat/%s/message?password=%s&limit=%d",
		ib.serverURL, chatGUID, ib.password, limit)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("imessage: create read request: %w", err)
	}

	resp, err := ib.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("imessage: read request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("imessage: read HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Data []BlueBubblesMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("imessage: parse read response: %w", err)
	}

	messages := make([]BBMessage, 0, len(result.Data))
	for _, m := range result.Data {
		messages = append(messages, BBMessage{
			GUID:        m.GUID,
			ChatGUID:    m.ChatGUID,
			Text:        m.Text,
			Handle:      m.Handle.Address,
			DateCreated: m.DateCreated,
			IsFromMe:    m.IsFromMe,
		})
	}
	return messages, nil
}

// sendTapback sends a tapback reaction on a message via BlueBubbles API.
// POST /api/v1/message/{guid}/tapback?password=...
func (ib *IMessageBot) sendTapback(chatGUID, messageGUID string, tapback int) error {
	if messageGUID == "" {
		return fmt.Errorf("imessage: empty message GUID for tapback")
	}

	payload := map[string]interface{}{
		"chatGuid": chatGUID,
		"tapback":  tapback,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("imessage: marshal tapback request: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/message/%s/tapback?password=%s", ib.serverURL, messageGUID, ib.password)
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("imessage: create tapback request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := ib.client.Do(req)
	if err != nil {
		return fmt.Errorf("imessage: tapback request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("imessage: tapback HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	logDebug("imessage: tapback sent", "messageGuid", messageGUID, "tapback", tapback)
	return nil
}

// --- Notifier Interface ---

// Send sends a notification to the first allowed chat (Notifier interface).
func (ib *IMessageBot) Send(text string) error {
	if text == "" {
		return nil
	}
	if len(text) > 4000 {
		text = text[:3997] + "..."
	}
	// Send to first allowed chat.
	if len(ib.cfg.IMessage.AllowedChats) > 0 {
		return ib.sendMessage(ib.cfg.IMessage.AllowedChats[0], text)
	}
	return fmt.Errorf("imessage: no allowed chats configured for notification")
}

// Name returns the notifier name (Notifier interface).
func (ib *IMessageBot) Name() string { return "imessage" }

// --- IMessageNotifier ---

// IMessageNotifier sends notifications via BlueBubbles iMessage API (standalone, for buildNotifiers).
type IMessageNotifier struct {
	Config   IMessageConfig
	ChatGUID string // target chat GUID
}

func (n *IMessageNotifier) Send(text string) error {
	if text == "" {
		return nil
	}
	if len(text) > 4000 {
		text = text[:3997] + "..."
	}

	serverURL := strings.TrimRight(n.Config.ServerURL, "/")
	payload := map[string]string{
		"chatGuid": n.ChatGUID,
		"message":  text,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("imessage: marshal notification: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/message/text?password=%s", serverURL, n.Config.Password)
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("imessage: create notification request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("imessage: notification request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("imessage: notification HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

func (n *IMessageNotifier) Name() string { return "imessage" }

// --- PresenceSetter Interface (in presence.go) ---
// SetTyping and PresenceName are implemented in presence.go

// --- Tool Handlers ---

// toolIMessageSend sends an iMessage to a specific chat.
func toolIMessageSend(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		ChatGUID string `json:"chat_guid"`
		Text     string `json:"text"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.ChatGUID == "" || args.Text == "" {
		return "", fmt.Errorf("chat_guid and text are required")
	}

	if globalIMessageBot == nil {
		return "", fmt.Errorf("iMessage bot not initialized")
	}

	if err := globalIMessageBot.sendMessage(args.ChatGUID, args.Text); err != nil {
		return "", err
	}
	return fmt.Sprintf("message sent to %s", args.ChatGUID), nil
}

// toolIMessageSearch searches iMessage messages.
func toolIMessageSearch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Query == "" {
		return "", fmt.Errorf("query is required")
	}
	if args.Limit <= 0 {
		args.Limit = 10
	}

	if globalIMessageBot == nil {
		return "", fmt.Errorf("iMessage bot not initialized")
	}

	messages, err := globalIMessageBot.searchMessages(args.Query, args.Limit)
	if err != nil {
		return "", err
	}

	b, _ := json.Marshal(messages)
	return string(b), nil
}

// toolIMessageRead reads recent messages from an iMessage chat.
func toolIMessageRead(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		ChatGUID string `json:"chat_guid"`
		Limit    int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.ChatGUID == "" {
		return "", fmt.Errorf("chat_guid is required")
	}
	if args.Limit <= 0 {
		args.Limit = 20
	}

	if globalIMessageBot == nil {
		return "", fmt.Errorf("iMessage bot not initialized")
	}

	messages, err := globalIMessageBot.readRecentMessages(args.ChatGUID, args.Limit)
	if err != nil {
		return "", err
	}

	b, _ := json.Marshal(messages)
	return string(b), nil
}
