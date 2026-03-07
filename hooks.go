package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// --- Claude Code Hooks Event Receiver ---
// Receives hook events from Claude Code (PostToolUse, Stop, Notification, etc.)
// and routes them to the supervisor + SSE broker for real-time monitoring.

// HookEvent represents a Claude Code hook event payload.
// See: https://docs.anthropic.com/en/docs/claude-code/hooks
type HookEvent struct {
	Type      string          `json:"type"`                // "PreToolUse", "PostToolUse", "Stop", "Notification"
	Tool      *HookToolInfo   `json:"tool,omitempty"`      // tool details (PreToolUse/PostToolUse)
	Session   *HookSession    `json:"session,omitempty"`   // session info
	Stop      *HookStopInfo   `json:"stop_info,omitempty"` // stop details
	Timestamp string          `json:"timestamp,omitempty"` // ISO 8601
	Raw       json.RawMessage `json:"-"`                   // original payload for forwarding
}

// HookToolInfo contains tool-related details from a hook event.
type HookToolInfo struct {
	Name  string          `json:"tool_name"`
	Input json.RawMessage `json:"tool_input,omitempty"`
}

// HookSession identifies the Claude Code session that fired the hook.
type HookSession struct {
	ID  string `json:"session_id"`
	Cwd string `json:"cwd,omitempty"`
}

// HookStopInfo contains details about why Claude Code stopped.
type HookStopInfo struct {
	Reason string `json:"reason,omitempty"` // "end_turn", "max_turns", "error", etc.
}

// hookReceiver processes incoming hook events and routes them to the system.
type hookReceiver struct {
	mu         sync.RWMutex
	broker     *sseBroker
	supervisor *tmuxSupervisor
	cfg        *Config

	// planCache stores recently seen plan file paths and content.
	planCache   map[string]*cachedPlan // sessionID → plan
	planCacheMu sync.RWMutex

	// sessionWorker maps Claude Code session IDs to tmux worker names.
	sessionWorker   map[string]string
	sessionWorkerMu sync.RWMutex

	// stats
	eventCount    int64
	lastEventTime time.Time
}

// cachedPlan stores plan file info detected from hook events.
type cachedPlan struct {
	SessionID string `json:"sessionId"`
	FilePath  string `json:"filePath"`
	Content   string `json:"content,omitempty"`
	CachedAt  time.Time
	// ExitPlanMode detected — plan is ready for review.
	ReadyForReview bool `json:"readyForReview"`
}

func newHookReceiver(broker *sseBroker, supervisor *tmuxSupervisor, cfg *Config) *hookReceiver {
	return &hookReceiver{
		broker:        broker,
		supervisor:    supervisor,
		cfg:           cfg,
		planCache:     make(map[string]*cachedPlan),
		sessionWorker: make(map[string]string),
	}
}

// registerHookRoutes registers /api/hooks/* endpoints on the given mux.
func (s *Server) registerHookRoutes(mux *http.ServeMux) {
	if s.hookReceiver == nil {
		return
	}
	mux.HandleFunc("/api/hooks/event", s.hookReceiver.handleEvent)
	mux.HandleFunc("/api/hooks/status", s.hookReceiver.handleStatus)
	mux.HandleFunc("/api/hooks/notify", s.handleHookNotify)
}

// handleHookNotify receives notifications from Claude Code via MCP bridge
// and forwards them to Discord/Telegram.
// POST /api/hooks/notify
func (s *Server) handleHookNotify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		Message string `json:"message"`
		Level   string `json:"level"` // info, warn, error
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if body.Message == "" {
		http.Error(w, `{"error":"message is required"}`, http.StatusBadRequest)
		return
	}
	if body.Level == "" {
		body.Level = "info"
	}

	// Send notification via configured channels.
	cfg := s.Cfg()
	prefix := ""
	switch body.Level {
	case "warn":
		prefix = "[WARN] "
	case "error":
		prefix = "[ERROR] "
	}
	msg := prefix + body.Message

	// Try Discord notification channel.
	if cfg.discordBot != nil {
		cfg.discordBot.sendNotify(msg)
	}

	// Publish to SSE for dashboard.
	if s.hookReceiver != nil && s.hookReceiver.broker != nil {
		s.hookReceiver.broker.Publish(SSEDashboardKey, SSEEvent{
			Type: SSEHookEvent,
			Data: map[string]any{
				"hookType": "notification",
				"message":  body.Message,
				"level":    body.Level,
			},
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// handleEvent receives a hook event from Claude Code.
// POST /api/hooks/event
func (hr *hookReceiver) handleEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB limit
	if err != nil {
		http.Error(w, `{"error":"read body failed"}`, http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var event HookEvent
	if err := json.Unmarshal(body, &event); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	event.Raw = body

	if event.Timestamp == "" {
		event.Timestamp = time.Now().Format(time.RFC3339)
	}

	// Extract session ID from various locations in the payload.
	sessionID := hr.extractSessionID(&event, body)

	// Update stats.
	hr.mu.Lock()
	hr.eventCount++
	hr.lastEventTime = time.Now()
	hr.mu.Unlock()

	// Route by event type.
	switch event.Type {
	case "PreToolUse":
		hr.handlePreToolUse(&event, sessionID)
	case "PostToolUse":
		hr.handlePostToolUse(&event, sessionID)
	case "Stop":
		hr.handleStop(&event, sessionID)
	case "Notification":
		hr.handleNotification(&event, sessionID)
	default:
		logDebug("hooks: unknown event type", "type", event.Type)
	}

	// Publish raw event to dashboard SSE.
	if hr.broker != nil {
		hr.broker.Publish(SSEDashboardKey, SSEEvent{
			Type:      SSEHookEvent,
			SessionID: sessionID,
			Data: map[string]any{
				"hookType":  event.Type,
				"toolName":  hr.toolName(&event),
				"sessionId": sessionID,
				"timestamp": event.Timestamp,
			},
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// handleStatus returns hook receiver status.
// GET /api/hooks/status
func (hr *hookReceiver) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	hr.mu.RLock()
	count := hr.eventCount
	lastEvent := hr.lastEventTime
	hr.mu.RUnlock()

	hr.planCacheMu.RLock()
	plans := make([]map[string]any, 0, len(hr.planCache))
	for sid, p := range hr.planCache {
		plans = append(plans, map[string]any{
			"sessionId":      sid,
			"filePath":       p.FilePath,
			"readyForReview": p.ReadyForReview,
			"cachedAt":       p.CachedAt.Format(time.RFC3339),
		})
	}
	hr.planCacheMu.RUnlock()

	hr.sessionWorkerMu.RLock()
	workerMap := make(map[string]string, len(hr.sessionWorker))
	for k, v := range hr.sessionWorker {
		workerMap[k] = v
	}
	hr.sessionWorkerMu.RUnlock()

	resp := map[string]any{
		"eventCount":    count,
		"lastEventTime": lastEvent.Format(time.RFC3339),
		"planCache":     plans,
		"sessionWorker": workerMap,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// --- Event Handlers ---

func (hr *hookReceiver) handlePreToolUse(event *HookEvent, sessionID string) {
	toolName := hr.toolName(event)
	logDebug("hooks: PreToolUse", "tool", toolName, "session", sessionID)

	// Update worker state to "working" since a tool is being called.
	hr.updateWorkerState(sessionID, tmuxStateWorking, "tool:"+toolName)
}

func (hr *hookReceiver) handlePostToolUse(event *HookEvent, sessionID string) {
	toolName := hr.toolName(event)
	logDebug("hooks: PostToolUse", "tool", toolName, "session", sessionID)

	// Check for plan-related tool calls.
	switch toolName {
	case "Write", "Edit":
		// Check if writing to a plan file.
		hr.checkPlanFileWrite(event, sessionID)
	case "ExitPlanMode":
		// Plan review triggered — cache and publish.
		hr.handlePlanReviewTrigger(sessionID)
	}

	// Update worker state.
	hr.updateWorkerState(sessionID, tmuxStateWorking, "post:"+toolName)
}

func (hr *hookReceiver) handleStop(event *HookEvent, sessionID string) {
	reason := ""
	if event.Stop != nil {
		reason = event.Stop.Reason
	}
	logInfo("hooks: Stop", "reason", reason, "session", sessionID)

	// Update worker state to done.
	hr.updateWorkerState(sessionID, tmuxStateDone, "stop:"+reason)

	// Publish stop event.
	if hr.broker != nil {
		hr.broker.Publish(SSEDashboardKey, SSEEvent{
			Type:      SSECompleted,
			SessionID: sessionID,
			Data: map[string]any{
				"reason":    reason,
				"sessionId": sessionID,
			},
		})
	}
}

func (hr *hookReceiver) handleNotification(event *HookEvent, sessionID string) {
	logInfo("hooks: Notification", "session", sessionID)

	if hr.broker != nil {
		hr.broker.Publish(SSEDashboardKey, SSEEvent{
			Type:      SSEHookEvent,
			SessionID: sessionID,
			Data: map[string]any{
				"hookType":  "Notification",
				"sessionId": sessionID,
			},
		})
	}
}

// --- Plan File Detection ---

// checkPlanFileWrite checks if a Write/Edit tool call is targeting a plan file.
func (hr *hookReceiver) checkPlanFileWrite(event *HookEvent, sessionID string) {
	if event.Tool == nil || len(event.Tool.Input) == 0 {
		return
	}

	// Parse tool input to find file_path.
	var input struct {
		FilePath string `json:"file_path"`
	}
	if err := json.Unmarshal(event.Tool.Input, &input); err != nil || input.FilePath == "" {
		return
	}

	// Check if writing to a plan file location.
	homeDir, _ := os.UserHomeDir()
	planDir := filepath.Join(homeDir, ".claude", "plans")
	if !strings.HasPrefix(input.FilePath, planDir) {
		return
	}

	logInfo("hooks: plan file write detected", "path", input.FilePath, "session", sessionID)

	// Read the plan file content.
	content, err := os.ReadFile(input.FilePath)
	if err != nil {
		logWarn("hooks: failed to read plan file", "path", input.FilePath, "error", err)
		content = nil
	}

	hr.planCacheMu.Lock()
	hr.planCache[sessionID] = &cachedPlan{
		SessionID: sessionID,
		FilePath:  input.FilePath,
		Content:   string(content),
		CachedAt:  time.Now(),
	}
	hr.planCacheMu.Unlock()
}

// handlePlanReviewTrigger is called when ExitPlanMode is detected.
func (hr *hookReceiver) handlePlanReviewTrigger(sessionID string) {
	logInfo("hooks: plan review triggered (ExitPlanMode)", "session", sessionID)

	hr.planCacheMu.Lock()
	plan, ok := hr.planCache[sessionID]
	if ok {
		plan.ReadyForReview = true
	} else {
		// ExitPlanMode without a Write — try to find the plan file.
		plan = &cachedPlan{
			SessionID:      sessionID,
			ReadyForReview: true,
			CachedAt:       time.Now(),
		}
		hr.planCache[sessionID] = plan
	}
	hr.planCacheMu.Unlock()

	// Publish plan review event to dashboard.
	if hr.broker != nil {
		data := map[string]any{
			"sessionId":      sessionID,
			"readyForReview": true,
		}
		if plan != nil {
			data["filePath"] = plan.FilePath
			if len(plan.Content) > 0 {
				// Truncate for SSE (full content via API).
				preview := plan.Content
				if len(preview) > 2000 {
					preview = preview[:2000] + "\n... (truncated)"
				}
				data["preview"] = preview
			}
		}
		hr.broker.Publish(SSEDashboardKey, SSEEvent{
			Type:      SSEPlanReview,
			SessionID: sessionID,
			Data:      data,
		})
	}
}

// --- Worker State Updates ---

// updateWorkerState updates the tmux worker state from hook events.
func (hr *hookReceiver) updateWorkerState(sessionID string, state tmuxScreenState, detail string) {
	if sessionID == "" || hr.supervisor == nil {
		return
	}

	// Find the worker for this session.
	workerName := hr.getWorkerForSession(sessionID)
	if workerName == "" {
		return
	}

	worker := hr.supervisor.getWorker(workerName)
	if worker == nil {
		return
	}

	worker.State = state
	worker.LastChanged = time.Now()

	// Publish state update.
	if hr.broker != nil {
		hr.broker.Publish(SSEDashboardKey, SSEEvent{
			Type:      SSEWorkerUpdate,
			SessionID: sessionID,
			Data: map[string]string{
				"action":    "state_update",
				"name":      workerName,
				"state":     state.String(),
				"detail":    detail,
				"sessionId": sessionID,
			},
		})
	}
}

// MapSessionToWorker associates a Claude Code session ID with a tmux worker name.
func (hr *hookReceiver) MapSessionToWorker(sessionID, workerName string) {
	if sessionID == "" || workerName == "" {
		return
	}
	hr.sessionWorkerMu.Lock()
	hr.sessionWorker[sessionID] = workerName
	hr.sessionWorkerMu.Unlock()
}

// UnmapSession removes a session-to-worker mapping.
func (hr *hookReceiver) UnmapSession(sessionID string) {
	hr.sessionWorkerMu.Lock()
	delete(hr.sessionWorker, sessionID)
	hr.sessionWorkerMu.Unlock()
}

func (hr *hookReceiver) getWorkerForSession(sessionID string) string {
	hr.sessionWorkerMu.RLock()
	defer hr.sessionWorkerMu.RUnlock()
	return hr.sessionWorker[sessionID]
}

// GetCachedPlan returns the cached plan for a session, if any.
func (hr *hookReceiver) GetCachedPlan(sessionID string) *cachedPlan {
	hr.planCacheMu.RLock()
	defer hr.planCacheMu.RUnlock()
	return hr.planCache[sessionID]
}

// ClearPlanCache removes a session's cached plan after review is complete.
func (hr *hookReceiver) ClearPlanCache(sessionID string) {
	hr.planCacheMu.Lock()
	delete(hr.planCache, sessionID)
	hr.planCacheMu.Unlock()
}

// --- Helpers ---

func (hr *hookReceiver) toolName(event *HookEvent) string {
	if event.Tool != nil {
		return event.Tool.Name
	}
	return ""
}

// extractSessionID tries to extract the session ID from various locations in the event.
func (hr *hookReceiver) extractSessionID(event *HookEvent, body []byte) string {
	// Try session field first.
	if event.Session != nil && event.Session.ID != "" {
		return event.Session.ID
	}

	// Try to extract from raw JSON (Claude Code may place it at different levels).
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err == nil {
		for _, key := range []string{"session_id", "sessionId"} {
			if v, ok := raw[key]; ok {
				var s string
				if json.Unmarshal(v, &s) == nil && s != "" {
					return s
				}
			}
		}
	}

	return ""
}

// cleanupStalePlans removes plan cache entries older than 1 hour.
func (hr *hookReceiver) cleanupStalePlans() {
	hr.planCacheMu.Lock()
	defer hr.planCacheMu.Unlock()
	cutoff := time.Now().Add(-1 * time.Hour)
	for sid, p := range hr.planCache {
		if p.CachedAt.Before(cutoff) {
			delete(hr.planCache, sid)
		}
	}
}

// --- Config ---

// HooksConfig holds configuration for the hooks event receiver.
type HooksConfig struct {
	Enabled bool `json:"enabled,omitempty"` // default: true when hooks are installed
}

// --- Auth bypass for hooks endpoint ---

// isHooksPath returns true if the request path is a hooks endpoint.
// These are called from Claude Code hook scripts running locally,
// so they should bypass API token auth (they use curl from a shell script).
func isHooksPath(path string) bool {
	return strings.HasPrefix(path, "/api/hooks/")
}

// --- Debug ---

func (hr *hookReceiver) String() string {
	hr.mu.RLock()
	count := hr.eventCount
	last := hr.lastEventTime
	hr.mu.RUnlock()
	return fmt.Sprintf("hookReceiver{events=%d, last=%s}", count, last.Format(time.RFC3339))
}
