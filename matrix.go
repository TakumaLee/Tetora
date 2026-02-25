package main

// --- P15.2: Matrix Channel ---

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// --- Matrix Config ---

// MatrixConfig holds configuration for Matrix (Element/Synapse) integration.
type MatrixConfig struct {
	Enabled     bool   `json:"enabled,omitempty"`
	Homeserver  string `json:"homeserver,omitempty"`   // e.g. "https://matrix.example.com"
	UserID      string `json:"userId,omitempty"`       // e.g. "@tetora:example.com"
	AccessToken string `json:"accessToken,omitempty"`  // $ENV_VAR supported
	AutoJoin    bool   `json:"autoJoin,omitempty"`     // auto-join invited rooms
	DefaultRole string `json:"defaultRole,omitempty"`  // agent role for Matrix messages
}

// --- Matrix Sync Response Types ---

// matrixSyncResponse is the top-level response from /_matrix/client/v3/sync.
type matrixSyncResponse struct {
	NextBatch string          `json:"next_batch"`
	Rooms     matrixSyncRooms `json:"rooms"`
}

// matrixSyncRooms contains joined and invited room data.
type matrixSyncRooms struct {
	Join   map[string]matrixJoinedRoom  `json:"join"`
	Invite map[string]matrixInvitedRoom `json:"invite"`
}

// matrixJoinedRoom represents a room the bot has joined.
type matrixJoinedRoom struct {
	Timeline matrixTimeline `json:"timeline"`
}

// matrixTimeline contains timeline events for a room.
type matrixTimeline struct {
	Events []matrixEvent `json:"events"`
}

// matrixEvent represents a single Matrix event.
type matrixEvent struct {
	Type     string          `json:"type"`
	Sender   string          `json:"sender"`
	EventID  string          `json:"event_id"`
	Content  json.RawMessage `json:"content"`
	OriginTS int64           `json:"origin_server_ts"`
}

// matrixInvitedRoom represents a room the bot has been invited to.
type matrixInvitedRoom struct {
	InviteState matrixInviteState `json:"invite_state"`
}

// matrixInviteState contains state events for an invited room.
type matrixInviteState struct {
	Events []matrixEvent `json:"events"`
}

// matrixMessageContent is the content of an m.room.message event.
type matrixMessageContent struct {
	MsgType string `json:"msgtype"`
	Body    string `json:"body"`
	URL     string `json:"url,omitempty"` // for m.image, m.file, etc.
}

// matrixErrorResponse is a Matrix API error response.
type matrixErrorResponse struct {
	ErrCode string `json:"errcode"`
	Error   string `json:"error"`
}

// --- Matrix Bot ---

// MatrixBot manages the Matrix sync loop and message handling.
type MatrixBot struct {
	cfg        *Config
	state      *dispatchState
	sem        chan struct{}
	apiBase    string        // homeserver URL + /_matrix/client/v3
	sinceToken string        // for incremental sync
	txnID      int64         // atomic counter for transaction IDs
	stopCh     chan struct{}
	httpClient *http.Client
}

// newMatrixBot creates a new MatrixBot instance.
func newMatrixBot(cfg *Config, state *dispatchState, sem chan struct{}) *MatrixBot {
	apiBase := strings.TrimRight(cfg.Matrix.Homeserver, "/") + "/_matrix/client/v3"
	return &MatrixBot{
		cfg:        cfg,
		state:      state,
		sem:        sem,
		apiBase:    apiBase,
		stopCh:     make(chan struct{}),
		httpClient: &http.Client{Timeout: 60 * time.Second}, // long-poll needs longer timeout
	}
}

// Run starts the sync loop in a goroutine. Blocks until Stop is called.
func (mb *MatrixBot) Run(ctx context.Context) {
	logInfo("matrix bot starting sync loop", "homeserver", mb.cfg.Matrix.Homeserver, "userId", mb.cfg.Matrix.UserID)

	for {
		select {
		case <-ctx.Done():
			return
		case <-mb.stopCh:
			return
		default:
		}

		if err := mb.sync(); err != nil {
			logWarn("matrix sync error", "error", err)
			// Backoff on error.
			select {
			case <-ctx.Done():
				return
			case <-mb.stopCh:
				return
			case <-time.After(5 * time.Second):
			}
		}
	}
}

// Stop signals the bot to stop the sync loop.
func (mb *MatrixBot) Stop() {
	select {
	case <-mb.stopCh:
	default:
		close(mb.stopCh)
	}
}

// sync performs a single sync iteration.
func (mb *MatrixBot) sync() error {
	url := mb.apiBase + "/sync?timeout=30000"
	if mb.sinceToken != "" {
		url += "&since=" + mb.sinceToken
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("matrix: create sync request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+mb.cfg.Matrix.AccessToken)

	resp, err := mb.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("matrix: sync request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("matrix: unauthorized (401), check access token")
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("matrix: sync HTTP %d: %s", resp.StatusCode, string(body))
	}

	var syncResp matrixSyncResponse
	if err := json.NewDecoder(resp.Body).Decode(&syncResp); err != nil {
		return fmt.Errorf("matrix: decode sync response: %w", err)
	}

	// Update since token for next sync.
	if syncResp.NextBatch != "" {
		mb.sinceToken = syncResp.NextBatch
	}

	// Process invited rooms (auto-join).
	if mb.cfg.Matrix.AutoJoin {
		for roomID := range syncResp.Rooms.Invite {
			logInfo("matrix: auto-joining invited room", "roomID", roomID)
			if err := mb.joinRoom(roomID); err != nil {
				logWarn("matrix: auto-join failed", "roomID", roomID, "error", err)
			}
		}
	}

	// Process joined room events.
	for roomID, room := range syncResp.Rooms.Join {
		for _, event := range room.Timeline.Events {
			mb.handleRoomEvent(roomID, event)
		}
	}

	return nil
}

// handleRoomEvent processes a single event from a room timeline.
func (mb *MatrixBot) handleRoomEvent(roomID string, event matrixEvent) {
	// Only process m.room.message events.
	if event.Type != "m.room.message" {
		return
	}

	// Ignore own messages.
	if event.Sender == mb.cfg.Matrix.UserID {
		return
	}

	// Parse message content.
	var content matrixMessageContent
	if err := json.Unmarshal(event.Content, &content); err != nil {
		logDebug("matrix: failed to parse message content", "eventID", event.EventID, "error", err)
		return
	}

	// Only process text messages.
	if content.MsgType != "m.text" {
		logDebug("matrix: ignoring non-text message", "msgtype", content.MsgType, "eventID", event.EventID)
		return
	}

	text := strings.TrimSpace(content.Body)
	if text == "" {
		return
	}

	logInfo("matrix: received message", "from", event.Sender, "room", roomID, "text", truncate(text, 100))

	// Dispatch to agent asynchronously.
	go mb.dispatchToAgent(text, event.Sender, roomID)
}

// dispatchToAgent dispatches a message to the agent system and sends a reply.
func (mb *MatrixBot) dispatchToAgent(text, sender, roomID string) {
	ctx := withTraceID(context.Background(), newTraceID("matrix"))
	dbPath := mb.cfg.HistoryDB

	// Route to determine role.
	role := mb.cfg.Matrix.DefaultRole
	if role == "" {
		route := routeTask(ctx, mb.cfg, RouteRequest{Prompt: text, Source: "matrix"})
		role = route.Role
		logInfoCtx(ctx, "matrix route result", "role", role, "method", route.Method)
	}

	// Find or create session.
	chKey := channelSessionKey("matrix", sender, roomID)
	sess, err := getOrCreateChannelSession(dbPath, "matrix", chKey, role, "")
	if err != nil {
		logErrorCtx(ctx, "matrix session error", "error", err)
	}

	// Build context-aware prompt.
	contextPrompt := text
	if sess != nil {
		sessionCtx := buildSessionContext(dbPath, sess.ID, mb.cfg.Session.contextMessagesOrDefault())
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
		Source: "matrix",
	}
	fillDefaults(mb.cfg, &task)
	if sess != nil {
		task.SessionID = sess.ID
	}

	// Apply role-specific config.
	if role != "" {
		if soulPrompt, err := loadRolePrompt(mb.cfg, role); err == nil && soulPrompt != "" {
			task.SystemPrompt = soulPrompt
		}
		if rc, ok := mb.cfg.Roles[role]; ok {
			if rc.Model != "" {
				task.Model = rc.Model
			}
			if rc.PermissionMode != "" {
				task.PermissionMode = rc.PermissionMode
			}
		}
	}

	task.Prompt = expandPrompt(task.Prompt, "", mb.cfg.HistoryDB, role, mb.cfg.KnowledgeDir, mb.cfg)

	// Run task.
	taskStart := time.Now()
	result := runSingleTask(ctx, mb.cfg, task, mb.sem, role)

	// Record to history.
	recordHistory(mb.cfg.HistoryDB, task.ID, task.Name, task.Source, role, task, result,
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

	// Matrix has no strict message length limit, but truncate very long messages.
	if len(response) > 32000 {
		response = response[:31997] + "..."
	}

	// Send response.
	if err := mb.sendMessage(roomID, response); err != nil {
		logErrorCtx(ctx, "matrix: send reply failed", "roomID", roomID, "error", err)
	}

	logInfoCtx(ctx, "matrix task complete", "taskID", task.ID, "status", result.Status, "cost", result.CostUSD)

	// Emit SSE event.
	if mb.state.broker != nil {
		mb.state.broker.Publish("matrix", SSEEvent{
			Type: "matrix",
			Data: map[string]interface{}{
				"from":   sender,
				"room":   roomID,
				"taskID": task.ID,
				"status": result.Status,
				"cost":   result.CostUSD,
			},
		})
	}
}

// --- Matrix REST API ---

// sendMessage sends a text message to a Matrix room.
func (mb *MatrixBot) sendMessage(roomID, text string) error {
	if roomID == "" {
		return fmt.Errorf("matrix: empty room ID")
	}
	if text == "" {
		return nil
	}

	txnID := atomic.AddInt64(&mb.txnID, 1)
	url := fmt.Sprintf("%s/rooms/%s/send/m.room.message/%d",
		mb.apiBase, roomID, txnID)

	payload := map[string]string{
		"msgtype": "m.text",
		"body":    text,
	}

	return mb.matrixPUT(url, payload)
}

// joinRoom joins a Matrix room by room ID or alias.
func (mb *MatrixBot) joinRoom(roomIDOrAlias string) error {
	if roomIDOrAlias == "" {
		return fmt.Errorf("matrix: empty room ID/alias")
	}

	url := fmt.Sprintf("%s/join/%s", mb.apiBase, roomIDOrAlias)
	return mb.matrixPOST(url, map[string]string{})
}

// leaveRoom leaves a Matrix room.
func (mb *MatrixBot) leaveRoom(roomID string) error {
	if roomID == "" {
		return fmt.Errorf("matrix: empty room ID")
	}

	url := fmt.Sprintf("%s/rooms/%s/leave", mb.apiBase, roomID)
	return mb.matrixPOST(url, map[string]string{})
}

// --- HTTP Helpers ---

// matrixPUT sends a PUT request to the Matrix API.
func (mb *MatrixBot) matrixPUT(url string, payload interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("matrix: marshal payload: %w", err)
	}

	req, err := http.NewRequest("PUT", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("matrix: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+mb.cfg.Matrix.AccessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := mb.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("matrix: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("matrix: HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// matrixPOST sends a POST request to the Matrix API.
func (mb *MatrixBot) matrixPOST(url string, payload interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("matrix: marshal payload: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("matrix: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+mb.cfg.Matrix.AccessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := mb.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("matrix: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("matrix: HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// --- Matrix Notification Integration ---

// MatrixNotifier sends notifications via Matrix room messages.
type MatrixNotifier struct {
	Config MatrixConfig
	RoomID string // room ID to send notifications to
}

// Send sends a notification message to the configured Matrix room.
func (n *MatrixNotifier) Send(text string) error {
	if text == "" {
		return nil
	}
	if n.RoomID == "" {
		return fmt.Errorf("matrix: no room ID configured for notifications")
	}

	// Truncate very long messages.
	if len(text) > 32000 {
		text = text[:31997] + "..."
	}

	apiBase := strings.TrimRight(n.Config.Homeserver, "/") + "/_matrix/client/v3"
	txnID := time.Now().UnixNano()
	url := fmt.Sprintf("%s/rooms/%s/send/m.room.message/%d", apiBase, n.RoomID, txnID)

	payload := map[string]string{
		"msgtype": "m.text",
		"body":    text,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("matrix: marshal notification: %w", err)
	}

	req, err := http.NewRequest("PUT", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("matrix: create notification request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+n.Config.AccessToken)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("matrix: notification request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("matrix: notification HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// Name returns the notifier name.
func (n *MatrixNotifier) Name() string { return "matrix" }
