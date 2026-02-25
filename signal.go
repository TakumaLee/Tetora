package main

// --- P15.4: Signal Channel ---

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

// --- Signal Config ---

// SignalConfig holds configuration for signal-cli-rest-api integration.
type SignalConfig struct {
	Enabled     bool   `json:"enabled,omitempty"`
	APIBaseURL  string `json:"apiBaseURL,omitempty"`  // default "http://localhost:8080"
	PhoneNumber string `json:"phoneNumber,omitempty"` // +1234567890 ($ENV_VAR)
	WebhookPath string `json:"webhookPath,omitempty"` // default "/api/signal/webhook"
	DefaultRole string `json:"defaultRole,omitempty"` // agent role for Signal messages
	PollingMode bool   `json:"pollingMode,omitempty"` // enable polling instead of webhook
	PollInterval int   `json:"pollInterval,omitempty"` // polling interval in seconds (default 5)
}

// webhookPathOrDefault returns the configured webhook path or default.
func (c SignalConfig) webhookPathOrDefault() string {
	if c.WebhookPath != "" {
		return c.WebhookPath
	}
	return "/api/signal/webhook"
}

// apiBaseURLOrDefault returns the configured API base URL or default.
func (c SignalConfig) apiBaseURLOrDefault() string {
	if c.APIBaseURL != "" {
		return c.APIBaseURL
	}
	return "http://localhost:8080"
}

// pollIntervalOrDefault returns the polling interval in seconds (default 5).
func (c SignalConfig) pollIntervalOrDefault() int {
	if c.PollInterval > 0 {
		return c.PollInterval
	}
	return 5
}

// --- Signal Message Types ---

// signalReceivePayload represents an incoming message from signal-cli-rest-api webhook.
type signalReceivePayload struct {
	Envelope signalEnvelope `json:"envelope"`
}

// signalEnvelope contains the message envelope.
type signalEnvelope struct {
	Source      string              `json:"source"`      // sender phone number
	SourceName  string              `json:"sourceName"`  // sender name
	SourceUUID  string              `json:"sourceUuid"`  // sender UUID
	Timestamp   int64               `json:"timestamp"`   // message timestamp (ms)
	DataMessage *signalDataMessage  `json:"dataMessage,omitempty"`
	SyncMessage *signalSyncMessage  `json:"syncMessage,omitempty"`
	CallMessage *signalCallMessage  `json:"callMessage,omitempty"`
	TypingMessage *signalTypingMessage `json:"typingMessage,omitempty"`
}

// signalDataMessage represents a text/attachment message.
type signalDataMessage struct {
	Timestamp   int64                  `json:"timestamp"`
	Message     string                 `json:"message"`
	ExpiresInSeconds int               `json:"expiresInSeconds"`
	GroupInfo   *signalGroupInfo       `json:"groupInfo,omitempty"`
	Attachments []signalAttachment     `json:"attachments,omitempty"`
	Mentions    []signalMention        `json:"mentions,omitempty"`
	Quote       *signalQuote           `json:"quote,omitempty"`
	Reaction    *signalReaction        `json:"reaction,omitempty"`
}

// signalGroupInfo contains group message context.
type signalGroupInfo struct {
	GroupID string `json:"groupId"`
	Type    string `json:"type"` // "DELIVER", "UPDATE", "QUIT"
}

// signalAttachment represents a file attachment.
type signalAttachment struct {
	ContentType string `json:"contentType"`
	Filename    string `json:"filename,omitempty"`
	ID          string `json:"id"`
	Size        int    `json:"size"`
}

// signalMention represents an @mention in a message.
type signalMention struct {
	Name   string `json:"name"`
	Number string `json:"number"`
	UUID   string `json:"uuid"`
	Start  int    `json:"start"`
	Length int    `json:"length"`
}

// signalQuote represents a quoted/replied message.
type signalQuote struct {
	ID     int64  `json:"id"`
	Author string `json:"author"`
	Text   string `json:"text"`
}

// signalReaction represents a reaction to a message.
type signalReaction struct {
	Emoji            string `json:"emoji"`
	TargetAuthor     string `json:"targetAuthor"`
	TargetTimestamp  int64  `json:"targetTimestamp"`
	IsRemove         bool   `json:"isRemove"`
}

// signalSyncMessage is for multi-device sync.
type signalSyncMessage struct {
	SentMessage *signalDataMessage `json:"sentMessage,omitempty"`
}

// signalCallMessage is for voice/video calls.
type signalCallMessage struct {
	OfferMessage  json.RawMessage `json:"offerMessage,omitempty"`
	AnswerMessage json.RawMessage `json:"answerMessage,omitempty"`
	BusyMessage   json.RawMessage `json:"busyMessage,omitempty"`
	HangupMessage json.RawMessage `json:"hangupMessage,omitempty"`
}

// signalTypingMessage is for typing indicators.
type signalTypingMessage struct {
	Action    string `json:"action"` // "STARTED", "STOPPED"
	Timestamp int64  `json:"timestamp"`
	GroupID   string `json:"groupId,omitempty"`
}

// signalSendRequest is the payload for sending a message via signal-cli-rest-api.
type signalSendRequest struct {
	Number      string   `json:"number,omitempty"`      // recipient phone number (for DM)
	Recipients  []string `json:"recipients,omitempty"`  // multiple recipients
	GroupID     string   `json:"groupId,omitempty"`     // group ID (for group message)
	Message     string   `json:"message"`
	Attachments []string `json:"attachments,omitempty"` // base64-encoded or file paths
}

// --- Signal Bot ---

// SignalBot handles incoming Signal messages via signal-cli-rest-api.
type SignalBot struct {
	cfg     *Config
	state   *dispatchState
	sem     chan struct{}
	apiBase string // signal-cli-rest-api base URL

	// Dedup: track recently processed message timestamps.
	processed     map[string]time.Time
	processedSize int
	mu            sync.Mutex

	// httpClient for API calls (replaceable for testing).
	httpClient *http.Client

	// Polling state.
	stopPolling chan struct{}
	pollingWg   sync.WaitGroup
}

// newSignalBot creates a new SignalBot instance.
func newSignalBot(cfg *Config, state *dispatchState, sem chan struct{}) *SignalBot {
	return &SignalBot{
		cfg:         cfg,
		state:       state,
		sem:         sem,
		apiBase:     cfg.Signal.apiBaseURLOrDefault(),
		processed:   make(map[string]time.Time),
		httpClient:  &http.Client{Timeout: 10 * time.Second},
		stopPolling: make(chan struct{}),
	}
}

// Start starts the polling loop if polling mode is enabled.
func (sb *SignalBot) Start() {
	if !sb.cfg.Signal.PollingMode {
		return
	}

	logInfo("signal: starting polling mode", "interval", sb.cfg.Signal.pollIntervalOrDefault())
	sb.pollingWg.Add(1)
	go sb.pollLoop()
}

// Stop stops the polling loop.
func (sb *SignalBot) Stop() {
	if !sb.cfg.Signal.PollingMode {
		return
	}

	logInfo("signal: stopping polling")
	close(sb.stopPolling)
	sb.pollingWg.Wait()
	logInfo("signal: polling stopped")
}

// pollLoop continuously polls signal-cli-rest-api for new messages.
func (sb *SignalBot) pollLoop() {
	defer sb.pollingWg.Done()

	interval := time.Duration(sb.cfg.Signal.pollIntervalOrDefault()) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-sb.stopPolling:
			return
		case <-ticker.C:
			if err := sb.fetchMessages(); err != nil {
				logDebug("signal: poll fetch failed", "error", err)
			}
		}
	}
}

// fetchMessages fetches new messages from signal-cli-rest-api polling endpoint.
// GET /v1/receive/{number}
func (sb *SignalBot) fetchMessages() error {
	url := fmt.Sprintf("%s/v1/receive/%s", sb.apiBase, sb.cfg.Signal.PhoneNumber)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("signal: create poll request: %w", err)
	}

	resp, err := sb.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("signal: poll request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		// No new messages.
		return nil
	}

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("signal: poll HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	// Parse response: array of envelopes.
	var envelopes []signalReceivePayload
	if err := json.NewDecoder(resp.Body).Decode(&envelopes); err != nil {
		return fmt.Errorf("signal: parse poll response: %w", err)
	}

	// Process each envelope.
	for _, payload := range envelopes {
		sb.processEnvelope(payload.Envelope)
	}

	return nil
}

// HandleWebhook handles incoming Signal webhook events.
// POST /api/signal/webhook
func (sb *SignalBot) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read body.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		logError("signal: read webhook body failed", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	r.Body.Close()

	// Parse payload.
	var payload signalReceivePayload
	if err := json.Unmarshal(body, &payload); err != nil {
		logError("signal: parse webhook payload failed", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Return 200 OK immediately to prevent retries.
	w.WriteHeader(http.StatusOK)

	// Process envelope asynchronously.
	go sb.processEnvelope(payload.Envelope)
}

// processEnvelope processes a signal envelope.
func (sb *SignalBot) processEnvelope(envelope signalEnvelope) {
	// Only handle data messages (text/attachments).
	if envelope.DataMessage == nil {
		logDebug("signal: non-data message ignored", "source", envelope.Source)
		return
	}

	msg := envelope.DataMessage
	if msg.Message == "" {
		logDebug("signal: empty message ignored", "source", envelope.Source)
		return
	}

	// Dedup: check if already processed using timestamp + source.
	dedupKey := fmt.Sprintf("%s:%d", envelope.Source, msg.Timestamp)
	sb.mu.Lock()
	if _, seen := sb.processed[dedupKey]; seen {
		sb.mu.Unlock()
		logDebug("signal: duplicate message ignored", "key", dedupKey)
		return
	}
	sb.processed[dedupKey] = time.Now()
	sb.processedSize++

	// Cleanup old entries every 1000 messages.
	if sb.processedSize > 1000 {
		cutoff := time.Now().Add(-1 * time.Hour)
		for key, t := range sb.processed {
			if t.Before(cutoff) {
				delete(sb.processed, key)
				sb.processedSize--
			}
		}
	}
	sb.mu.Unlock()

	text := strings.TrimSpace(msg.Message)
	if text == "" {
		return
	}

	// Determine if this is a group message or DM.
	isGroup := msg.GroupInfo != nil && msg.GroupInfo.GroupID != ""
	targetID := envelope.Source
	if isGroup {
		targetID = msg.GroupInfo.GroupID
	}

	logInfo("signal: received message", "from", envelope.SourceName, "source", envelope.Source, "group", isGroup, "text", truncate(text, 100))

	// Dispatch to agent.
	sb.dispatchToAgent(text, envelope, targetID, isGroup)
}

// dispatchToAgent dispatches a message to the agent system and replies via Signal.
func (sb *SignalBot) dispatchToAgent(text string, envelope signalEnvelope, targetID string, isGroup bool) {
	ctx := withTraceID(context.Background(), newTraceID("signal"))
	dbPath := sb.cfg.HistoryDB

	// Route to determine role.
	role := sb.cfg.Signal.DefaultRole
	if role == "" {
		route := routeTask(ctx, sb.cfg, RouteRequest{Prompt: text, Source: "signal"})
		role = route.Role
		logInfoCtx(ctx, "signal route result", "role", role, "method", route.Method)
	}

	// Find or create session.
	chKey := channelSessionKey("signal", envelope.Source, targetID)
	sess, err := getOrCreateChannelSession(dbPath, "signal", chKey, role, "")
	if err != nil {
		logErrorCtx(ctx, "signal session error", "error", err)
	}

	// Build context-aware prompt.
	contextPrompt := text
	if sess != nil {
		sessionCtx := buildSessionContext(dbPath, sess.ID, sb.cfg.Session.contextMessagesOrDefault())
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
		Source: "signal",
	}
	fillDefaults(sb.cfg, &task)
	if sess != nil {
		task.SessionID = sess.ID
	}

	// Apply role-specific config.
	if role != "" {
		if soulPrompt, err := loadRolePrompt(sb.cfg, role); err == nil && soulPrompt != "" {
			task.SystemPrompt = soulPrompt
		}
		if rc, ok := sb.cfg.Roles[role]; ok {
			if rc.Model != "" {
				task.Model = rc.Model
			}
			if rc.PermissionMode != "" {
				task.PermissionMode = rc.PermissionMode
			}
		}
	}

	task.Prompt = expandPrompt(task.Prompt, "", sb.cfg.HistoryDB, role, sb.cfg.KnowledgeDir, sb.cfg)

	// Run task.
	taskStart := time.Now()
	result := runSingleTask(ctx, sb.cfg, task, sb.sem, role)

	// Record to history.
	recordHistory(sb.cfg.HistoryDB, task.ID, task.Name, task.Source, role, task, result,
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

	// Signal has no strict message length limit, but keep reasonable (2000 chars for safety).
	if len(response) > 2000 {
		response = response[:1997] + "..."
	}

	// Send response.
	if isGroup {
		if err := sb.SendGroupMessage(targetID, response); err != nil {
			logError("signal: group message send failed", "error", err)
		}
	} else {
		if err := sb.SendMessage(envelope.Source, response); err != nil {
			logError("signal: DM send failed", "error", err)
		}
	}

	logInfoCtx(ctx, "signal task complete", "taskID", task.ID, "status", result.Status, "cost", result.CostUSD)

	// Emit SSE event.
	if sb.state != nil && sb.state.broker != nil {
		sb.state.broker.Publish("signal", SSEEvent{
			Type: "signal",
			Data: map[string]interface{}{
				"from":    envelope.SourceName,
				"source":  envelope.Source,
				"group":   isGroup,
				"taskID":  task.ID,
				"status":  result.Status,
				"cost":    result.CostUSD,
			},
		})
	}
}

// --- Signal API Methods ---

// SendMessage sends a text message to a recipient via signal-cli-rest-api.
// POST /v2/send
func (sb *SignalBot) SendMessage(to, text string) error {
	if to == "" || text == "" {
		return fmt.Errorf("signal: empty recipient or message")
	}

	payload := signalSendRequest{
		Number:  to,
		Message: text,
	}

	return sb.sendSignalAPIRequest("/v2/send", payload)
}

// SendGroupMessage sends a text message to a group via signal-cli-rest-api.
// POST /v2/send
func (sb *SignalBot) SendGroupMessage(groupID, text string) error {
	if groupID == "" || text == "" {
		return fmt.Errorf("signal: empty group ID or message")
	}

	payload := signalSendRequest{
		GroupID: groupID,
		Message: text,
	}

	return sb.sendSignalAPIRequest("/v2/send", payload)
}

// sendSignalAPIRequest sends a POST request to signal-cli-rest-api.
func (sb *SignalBot) sendSignalAPIRequest(path string, payload interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("signal: marshal payload: %w", err)
	}

	url := sb.apiBase + path
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("signal: create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := sb.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("signal: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("signal: HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	logDebug("signal: API request sent", "path", path, "status", resp.StatusCode)
	return nil
}

// --- Signal Notification Integration ---

// SignalNotifier sends notifications via signal-cli-rest-api.
type SignalNotifier struct {
	Config  SignalConfig
	Recipient string // phone number or group ID to send to
	IsGroup   bool   // true if Recipient is a group ID
}

func (n *SignalNotifier) Send(text string) error {
	if text == "" {
		return nil
	}
	// Truncate if too long.
	if len(text) > 2000 {
		text = text[:1997] + "..."
	}

	var payload signalSendRequest
	if n.IsGroup {
		payload = signalSendRequest{
			GroupID: n.Recipient,
			Message: text,
		}
	} else {
		payload = signalSendRequest{
			Number:  n.Recipient,
			Message: text,
		}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("signal: marshal notification: %w", err)
	}

	url := n.Config.apiBaseURLOrDefault() + "/v2/send"
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("signal: create notification request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("signal: notification request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("signal: notification HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

func (n *SignalNotifier) Name() string { return "signal" }
