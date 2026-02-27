package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// --- WhatsApp Config ---

// WhatsAppConfig holds configuration for WhatsApp Cloud API integration.
type WhatsAppConfig struct {
	Enabled       bool   `json:"enabled"`
	PhoneNumberID string `json:"phoneNumberId"` // WhatsApp Business phone number ID
	AccessToken   string `json:"accessToken"`   // Meta access token, supports $ENV_VAR
	VerifyToken   string `json:"verifyToken"`   // Webhook verification token
	AppSecret     string `json:"appSecret,omitempty"` // For payload signature verification
	APIVersion    string `json:"apiVersion,omitempty"` // default "v21.0"
}

// apiVersion returns the configured API version or default "v21.0".
func (w WhatsAppConfig) apiVersion() string {
	if w.APIVersion != "" {
		return w.APIVersion
	}
	return "v21.0"
}

// --- WhatsApp Webhook Types ---

type whatsAppWebhook struct {
	Object string `json:"object"`
	Entry  []struct {
		ID      string `json:"id"`
		Changes []struct {
			Value struct {
				MessagingProduct string `json:"messaging_product"`
				Metadata struct {
					DisplayPhoneNumber string `json:"display_phone_number"`
					PhoneNumberID      string `json:"phone_number_id"`
				} `json:"metadata"`
				Messages []struct {
					ID        string                `json:"id"`
					From      string                `json:"from"` // sender phone number
					Timestamp string                `json:"timestamp"`
					Type      string                `json:"type"` // "text", "image", "audio", etc.
					Text      *whatsAppMessageText `json:"text,omitempty"`
				} `json:"messages,omitempty"`
				Statuses []struct {
					ID        string `json:"id"`
					Status    string `json:"status"` // "sent", "delivered", "read"
					Timestamp string `json:"timestamp"`
				} `json:"statuses,omitempty"`
			} `json:"value"`
			Field string `json:"field"`
		} `json:"changes"`
	} `json:"entry"`
}

// --- WhatsApp Bot ---

// WhatsAppBot handles incoming WhatsApp Cloud API webhook events.
type WhatsAppBot struct {
	cfg   *Config
	state *dispatchState
	sem      chan struct{}
	childSem chan struct{}
	cron     *CronEngine

	// Dedup: track recently processed message IDs to handle retries.
	processed     map[string]time.Time
	processedSize int
	mu            sync.Mutex
}

func newWhatsAppBot(cfg *Config, state *dispatchState, sem, childSem chan struct{}, cron *CronEngine) *WhatsAppBot {
	return &WhatsAppBot{
		cfg:       cfg,
		state:     state,
		sem:       sem,
		childSem:  childSem,
		cron:      cron,
		processed: make(map[string]time.Time),
	}
}

// whatsAppWebhookHandler handles incoming WhatsApp webhook events.
// GET = verification challenge, POST = incoming messages
func (wb *WhatsAppBot) whatsAppWebhookHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		wb.handleVerification(w, r)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read body for signature verification.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		logError("whatsapp: read body failed", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	r.Body.Close()

	// Verify signature if AppSecret is configured.
	if wb.cfg.WhatsApp.AppSecret != "" {
		sig := r.Header.Get("X-Hub-Signature-256")
		if !verifyWhatsAppSignature(wb.cfg.WhatsApp.AppSecret, body, sig) {
			logWarn("whatsapp: signature verification failed", "signature", sig)
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
	}

	// Parse webhook payload.
	var hook whatsAppWebhook
	if err := json.Unmarshal(body, &hook); err != nil {
		logError("whatsapp: parse webhook failed", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// WhatsApp expects 200 OK immediately to prevent retries.
	w.WriteHeader(http.StatusOK)

	// Process messages asynchronously.
	go wb.processWebhook(&hook)
}

// handleVerification handles the webhook verification challenge.
// GET /api/whatsapp/webhook?hub.mode=subscribe&hub.verify_token=xxx&hub.challenge=xxx
func (wb *WhatsAppBot) handleVerification(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("hub.mode")
	token := r.URL.Query().Get("hub.verify_token")
	challenge := r.URL.Query().Get("hub.challenge")

	if mode == "subscribe" && token == wb.cfg.WhatsApp.VerifyToken {
		logInfo("whatsapp: webhook verified", "challenge", challenge)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(challenge))
		return
	}

	logWarn("whatsapp: verification failed", "mode", mode, "token", token)
	http.Error(w, "forbidden", http.StatusForbidden)
}

// processWebhook processes incoming webhook messages.
func (wb *WhatsAppBot) processWebhook(hook *whatsAppWebhook) {
	for _, entry := range hook.Entry {
		for _, change := range entry.Changes {
			// Process incoming messages.
			for _, msg := range change.Value.Messages {
				wb.handleMessage(msg.From, msg.ID, msg.Text, msg.Type)
			}

			// Silently ignore status updates (sent, delivered, read).
			// These are logged but not processed.
			if len(change.Value.Statuses) > 0 {
				logDebug("whatsapp: ignoring status updates", "count", len(change.Value.Statuses))
			}
		}
	}
}

// whatsAppMessageText represents the text field in a WhatsApp message.
type whatsAppMessageText struct {
	Body string `json:"body"`
}

// handleMessage processes a single WhatsApp message.
func (wb *WhatsAppBot) handleMessage(from, msgID string, textPtr *whatsAppMessageText, msgType string) {
	// Dedup: check if we've already processed this message.
	wb.mu.Lock()
	if _, seen := wb.processed[msgID]; seen {
		wb.mu.Unlock()
		logDebug("whatsapp: duplicate message ignored", "msgID", msgID)
		return
	}
	wb.processed[msgID] = time.Now()
	wb.processedSize++

	// Cleanup old entries every 1000 messages.
	if wb.processedSize > 1000 {
		cutoff := time.Now().Add(-1 * time.Hour)
		for id, t := range wb.processed {
			if t.Before(cutoff) {
				delete(wb.processed, id)
				wb.processedSize--
			}
		}
	}
	wb.mu.Unlock()

	// Only process text messages for now.
	if msgType != "text" || textPtr == nil {
		logDebug("whatsapp: non-text message ignored", "msgID", msgID, "type", msgType)
		return
	}

	text := strings.TrimSpace(textPtr.Body)
	if text == "" {
		return
	}

	logInfo("whatsapp: received message", "from", from, "text", truncate(text, 100))

	// Determine agent via smart dispatch.
	ctx := withTraceID(context.Background(), newTraceID("whatsapp"))
	dbPath := wb.cfg.HistoryDB

	// Route to determine agent.
	route := routeTask(ctx, wb.cfg, RouteRequest{Prompt: text, Source: "whatsapp"})
	logInfoCtx(ctx, "whatsapp route result", "from", from, "agent", route.Agent, "method", route.Method)

	// Find or create session for this phone number.
	chKey := channelSessionKey("whatsapp", from, "")
	sess, err := getOrCreateChannelSession(dbPath, "whatsapp", chKey, route.Agent, "")
	if err != nil {
		logErrorCtx(ctx, "whatsapp session error", "error", err)
	}

	// Build context-aware prompt.
	contextPrompt := text
	if sess != nil {
		sessionCtx := buildSessionContext(dbPath, sess.ID, wb.cfg.Session.contextMessagesOrDefault())
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
		Agent:  route.Agent,
		Source: "whatsapp",
	}
	fillDefaults(wb.cfg, &task)
	if sess != nil {
		task.SessionID = sess.ID
	}

	// Apply agent-specific config.
	if route.Agent != "" {
		if soulPrompt, err := loadAgentPrompt(wb.cfg, route.Agent); err == nil && soulPrompt != "" {
			task.SystemPrompt = soulPrompt
		}
		if rc, ok := wb.cfg.Agents[route.Agent]; ok {
			if rc.Model != "" {
				task.Model = rc.Model
			}
			if rc.PermissionMode != "" {
				task.PermissionMode = rc.PermissionMode
			}
		}
	}

	task.Prompt = expandPrompt(task.Prompt, "", wb.cfg.HistoryDB, route.Agent, wb.cfg.KnowledgeDir, wb.cfg)

	// Run task asynchronously.
	go func() {
		taskStart := time.Now()
		result := runSingleTask(ctx, wb.cfg, task, wb.sem, wb.childSem, route.Agent)

		// Record to history.
		recordHistory(wb.cfg.HistoryDB, task.ID, task.Name, task.Source, route.Agent, task, result,
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

		// Send response back to WhatsApp.
		response := result.Output
		if result.Error != "" {
			response = fmt.Sprintf("❌ Error: %s", result.Error)
		}

		// Truncate response if too long.
		if len(response) > 4000 {
			response = response[:3997] + "..."
		}

		if err := wb.sendMessage(from, response); err != nil {
			logError("whatsapp: send response failed", "error", err, "taskID", task.ID)
		}

		logInfoCtx(ctx, "whatsapp task complete", "taskID", task.ID, "status", result.Status, "cost", result.CostUSD)

		// Emit SSE event.
		if wb.state.broker != nil {
			wb.state.broker.Publish("whatsapp", SSEEvent{
				Type: "whatsapp",
				Data: map[string]interface{}{
					"from":   from,
					"taskID": task.ID,
					"status": result.Status,
					"cost":   result.CostUSD,
				},
			})
		}
	}()
}

// sendMessage sends a text message via WhatsApp Cloud API.
func (wb *WhatsAppBot) sendMessage(to, text string) error {
	return sendWhatsAppMessage(wb.cfg.WhatsApp, to, text)
}

// sendWhatsAppNotify sends a notification to a specific WhatsApp number.
// This is used for notification chain integration.
func (wb *WhatsAppBot) sendWhatsAppNotify(to, text string) {
	if err := sendWhatsAppMessage(wb.cfg.WhatsApp, to, text); err != nil {
		logError("whatsapp: notification send failed", "error", err, "to", to)
	}
}

// --- WhatsApp Cloud API Functions ---

// sendWhatsAppMessage sends a text message via WhatsApp Cloud API.
func sendWhatsAppMessage(cfg WhatsAppConfig, to string, text string) error {
	if text == "" {
		return nil
	}

	// WhatsApp has a message length limit; truncate if needed.
	if len(text) > 4096 {
		text = text[:4093] + "..."
	}

	payload := map[string]interface{}{
		"messaging_product": "whatsapp",
		"recipient_type":    "individual",
		"to":                to,
		"type":              "text",
		"text": map[string]string{
			"body": text,
		},
	}

	return sendWhatsAppAPIRequest(cfg, payload)
}

// sendWhatsAppReply sends a reply to a specific message.
func sendWhatsAppReply(cfg WhatsAppConfig, to string, text string, messageID string) error {
	if text == "" {
		return nil
	}

	if len(text) > 4096 {
		text = text[:4093] + "..."
	}

	payload := map[string]interface{}{
		"messaging_product": "whatsapp",
		"recipient_type":    "individual",
		"to":                to,
		"type":              "text",
		"context": map[string]string{
			"message_id": messageID,
		},
		"text": map[string]string{
			"body": text,
		},
	}

	return sendWhatsAppAPIRequest(cfg, payload)
}

// sendWhatsAppAPIRequest sends a request to WhatsApp Cloud API.
func sendWhatsAppAPIRequest(cfg WhatsAppConfig, payload interface{}) error {
	url := fmt.Sprintf("https://graph.facebook.com/%s/%s/messages",
		cfg.apiVersion(), cfg.PhoneNumberID)

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("whatsapp: marshal payload: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("whatsapp: create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+cfg.AccessToken)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("whatsapp: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("whatsapp: HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	logDebug("whatsapp: message sent", "status", resp.StatusCode)
	return nil
}

// verifyWhatsAppSignature verifies the X-Hub-Signature-256 header.
func verifyWhatsAppSignature(appSecret string, body []byte, signature string) bool {
	if appSecret == "" {
		return true // skip if no secret configured
	}

	if signature == "" {
		return false
	}

	// Signature format: "sha256=<hex>"
	if !strings.HasPrefix(signature, "sha256=") {
		return false
	}
	providedSig := signature[7:]

	mac := hmac.New(sha256.New, []byte(appSecret))
	mac.Write(body)
	expectedSig := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(expectedSig), []byte(providedSig))
}

