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

	"tetora/internal/log"
)

// --- Claude Code Hooks Event Receiver ---
// Receives hook events from Claude Code (PostToolUse, Stop, Notification, etc.)
// and routes them to the supervisor + SSE broker for real-time monitoring.

// HookEvent represents a Claude Code hook event payload (flat format).
// See: https://code.claude.com/docs/en/hooks
type HookEvent struct {
	// New flat format (Claude Code 2025+).
	HookEventName string          `json:"hook_event_name"`        // "PreToolUse", "PostToolUse", "Stop", "Notification"
	SessionID     string          `json:"session_id"`             // session UUID
	Cwd           string          `json:"cwd,omitempty"`          // working directory
	ToolName      string          `json:"tool_name,omitempty"`    // tool name (PreToolUse/PostToolUse)
	ToolInput     json.RawMessage `json:"tool_input,omitempty"`   // tool input
	ToolResponse  json.RawMessage `json:"tool_response,omitempty"` // tool output (PostToolUse)
	ToolUseID     string          `json:"tool_use_id,omitempty"`  // tool use ID
	StopHookActive bool           `json:"stop_hook_active,omitempty"` // Stop event
	LastAssistant  string         `json:"last_assistant_message,omitempty"` // Stop event

	// Legacy nested format (backward compat).
	Type      string          `json:"type"`                // old: "PreToolUse", etc.
	Tool      *HookToolInfo   `json:"tool,omitempty"`      // old: nested tool info
	Session   *HookSession    `json:"session,omitempty"`   // old: nested session
	Stop      *HookStopInfo   `json:"stop_info,omitempty"` // old: nested stop info

	Timestamp string          `json:"timestamp,omitempty"`
	Raw       json.RawMessage `json:"-"`
}

// ResolvedType returns the event type, supporting both new and legacy format.
func (e *HookEvent) ResolvedType() string {
	if e.HookEventName != "" {
		return e.HookEventName
	}
	return e.Type
}

// ResolvedSessionID returns the session ID from either format.
func (e *HookEvent) ResolvedSessionID() string {
	if e.SessionID != "" {
		return e.SessionID
	}
	if e.Session != nil {
		return e.Session.ID
	}
	return ""
}

// ResolvedToolName returns the tool name from either format.
func (e *HookEvent) ResolvedToolName() string {
	if e.ToolName != "" {
		return e.ToolName
	}
	if e.Tool != nil {
		return e.Tool.Name
	}
	return ""
}

// ResolvedCwd returns the working directory from either format.
func (e *HookEvent) ResolvedCwd() string {
	if e.Cwd != "" {
		return e.Cwd
	}
	if e.Session != nil {
		return e.Session.Cwd
	}
	return ""
}

// HookToolInfo contains tool-related details (legacy format).
type HookToolInfo struct {
	Name  string          `json:"tool_name"`
	Input json.RawMessage `json:"tool_input,omitempty"`
}

// HookSession identifies the Claude Code session (legacy format).
type HookSession struct {
	ID  string `json:"session_id"`
	Cwd string `json:"cwd,omitempty"`
}

// HookStopInfo contains details about why Claude Code stopped (legacy format).
type HookStopInfo struct {
	Reason string `json:"reason,omitempty"`
}

// planGateDecision represents the result of a plan gate review.
type planGateDecision struct {
	Approved bool
	Reason   string
}

// hookWorkerEvent is a single safe event entry (no sensitive data).
type hookWorkerEvent struct {
	Timestamp string `json:"timestamp"`
	EventType string `json:"eventType"` // "PreToolUse", "PostToolUse", "Stop"
	ToolName  string `json:"toolName,omitempty"`
}

const hookWorkerEventsMax = 50

// workerOrigin tracks where a worker came from (cron, dispatch, ask, etc.).
type workerOrigin struct {
	TaskID   string `json:"taskId"`
	TaskName string `json:"taskName"`
	Source   string `json:"source"`   // "cron", "dispatch", "ask", "route:eng", etc.
	Agent    string `json:"agent"`
	JobID    string `json:"jobId"`    // cron job ID (empty = non-cron)
}

// hookWorkerInfo tracks a Claude Code session detected via hooks.
type hookWorkerInfo struct {
	SessionID string
	State     string // "working", "idle", "done"
	LastTool  string
	Cwd       string
	FirstSeen time.Time
	LastSeen  time.Time
	ToolCount int
	Events    []hookWorkerEvent // ring buffer, max hookWorkerEventsMax
	Origin    *workerOrigin     // nil = manual session

	// Claude Code usage data (updated via statusline bridge).
	CostUSD      float64 `json:"-"`
	InputTokens  int     `json:"-"`
	OutputTokens int     `json:"-"`
	ContextPct   int     `json:"-"`
	Model        string  `json:"-"`
}

// hookReceiver processes incoming hook events and routes them to the system.
type hookReceiver struct {
	mu     sync.RWMutex
	broker *sseBroker
	cfg    *Config

	// planCache stores recently seen plan file paths and content.
	planCache   map[string]*cachedPlan // sessionID → plan
	planCacheMu sync.RWMutex

	// planGates tracks pending plan gate long-poll channels.
	planGates   map[string]chan planGateDecision
	planGatesMu sync.Mutex

	// questionGates tracks pending ask-user long-poll channels.
	questionGates   map[string]chan string
	questionGatesMu sync.Mutex

	// hookWorkers tracks sessions detected via hooks.
	hookWorkers   map[string]*hookWorkerInfo
	hookWorkersMu sync.RWMutex

	// workerOrigins maps sessionID → origin (registered before CLI starts).
	workerOrigins   map[string]*workerOrigin
	workerOriginsMu sync.RWMutex

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

func newHookReceiver(broker *sseBroker, cfg *Config) *hookReceiver {
	return &hookReceiver{
		broker:        broker,
		cfg:           cfg,
		planCache:     make(map[string]*cachedPlan),
		planGates:     make(map[string]chan planGateDecision),
		questionGates: make(map[string]chan string),
		hookWorkers:   make(map[string]*hookWorkerInfo),
		workerOrigins: make(map[string]*workerOrigin),
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
	mux.HandleFunc("/api/hooks/install", s.handleHookInstall)
	mux.HandleFunc("/api/hooks/remove", s.handleHookRemove)
	mux.HandleFunc("/api/hooks/install-status", s.handleHookInstallStatus)
	mux.HandleFunc("/api/hooks/plan-gate", s.handlePlanGate)
	mux.HandleFunc("/api/hooks/ask-user", s.handleAskUser)
	mux.HandleFunc("/api/hooks/usage", s.hookReceiver.handleUsageUpdate)
}

// handleHookInstall installs Tetora hooks into Claude Code settings.
// POST /api/hooks/install
func (s *Server) handleHookInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
		return
	}

	cfg := s.Cfg()
	if err := installHooks(cfg.ListenAddr); err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	resp := map[string]any{"ok": true}

	// Also generate MCP bridge config.
	if err := generateMCPBridgeConfig(cfg); err != nil {
		resp["mcpBridgeError"] = err.Error()
	} else {
		homeDir, _ := os.UserHomeDir()
		resp["mcpBridge"] = filepath.Join(homeDir, ".tetora", "mcp", "bridge.json")
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleHookRemove removes Tetora hooks from Claude Code settings.
// POST /api/hooks/remove
func (s *Server) handleHookRemove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
		return
	}

	if err := removeHooks(); err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"ok": "true"})
}

// handleHookInstallStatus checks whether hooks are currently installed.
// GET /api/hooks/install-status
func (s *Server) handleHookInstallStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
		return
	}

	installed := false
	hookCount := 0

	// Check Claude Code settings for Tetora hooks.
	settings, _, err := loadClaudeSettings()
	if err == nil {
		raw, ok := settings.raw["hooks"]
		if ok {
			var hooks hooksConfig
			if json.Unmarshal(raw, &hooks) == nil {
				for _, r := range hooks.PreToolUse {
					if isTetoraRule(r) {
						installed = true
						hookCount++
					}
				}
				for _, r := range hooks.PostToolUse {
					if isTetoraRule(r) {
						installed = true
						hookCount++
					}
				}
				for _, r := range hooks.Stop {
					if isTetoraRule(r) {
						hookCount++
					}
				}
				for _, r := range hooks.Notification {
					if isTetoraRule(r) {
						hookCount++
					}
				}
			}
		}
	}

	// Check MCP bridge config.
	homeDir, _ := os.UserHomeDir()
	bridgePath := filepath.Join(homeDir, ".tetora", "mcp", "bridge.json")
	_, mcpErr := os.Stat(bridgePath)
	mcpBridge := mcpErr == nil

	// Get event count from hook receiver.
	var eventCount int64
	if s.hookReceiver != nil {
		s.hookReceiver.mu.RLock()
		eventCount = s.hookReceiver.eventCount
		s.hookReceiver.mu.RUnlock()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"installed":  installed,
		"hookCount":  hookCount,
		"mcpBridge":  mcpBridge,
		"eventCount": eventCount,
	})
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
	if cfg.Runtime.DiscordBot != nil {
		cfg.Runtime.DiscordBot.(*DiscordBot).sendNotify(msg)
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
	sessionID := event.ResolvedSessionID()
	if sessionID == "" {
		sessionID = hr.extractSessionID(&event, body)
	}
	eventType := event.ResolvedType()

	// Update stats.
	hr.mu.Lock()
	hr.eventCount++
	hr.lastEventTime = time.Now()
	hr.mu.Unlock()

	// Route by event type.
	switch eventType {
	case "PreToolUse":
		hr.handlePreToolUse(&event, sessionID)
	case "PostToolUse":
		hr.handlePostToolUse(&event, sessionID)
	case "Stop":
		hr.handleStop(&event, sessionID)
	case "Notification":
		hr.handleNotification(&event, sessionID)
	default:
		log.Debug("hooks: unknown event type", "type", eventType)
	}

	// Publish raw event to dashboard SSE.
	if hr.broker != nil {
		hr.broker.Publish(SSEDashboardKey, SSEEvent{
			Type:      SSEHookEvent,
			SessionID: sessionID,
			Data: map[string]any{
				"hookType":  eventType,
				"toolName":  event.ResolvedToolName(),
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

	resp := map[string]any{
		"eventCount":    count,
		"lastEventTime": lastEvent.Format(time.RFC3339),
		"planCache":     plans,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// --- Event Handlers ---

func (hr *hookReceiver) handlePreToolUse(event *HookEvent, sessionID string) {
	toolName := event.ResolvedToolName()
	log.Debug("hooks: PreToolUse", "tool", toolName, "session", sessionID)

	// Track hook worker.
	hr.trackHookWorker(event, sessionID, "working", toolName, "PreToolUse")
}

func (hr *hookReceiver) handlePostToolUse(event *HookEvent, sessionID string) {
	toolName := event.ResolvedToolName()
	log.Debug("hooks: PostToolUse", "tool", toolName, "session", sessionID)

	// Track hook worker.
	hr.trackHookWorker(event, sessionID, "working", toolName, "PostToolUse")

	// Check for plan-related tool calls.
	switch toolName {
	case "Write", "Edit":
		// Check if writing to a plan file.
		hr.checkPlanFileWrite(event, sessionID)
	case "ExitPlanMode":
		// Plan review triggered — cache and publish.
		hr.handlePlanReviewTrigger(sessionID)
	}
}

func (hr *hookReceiver) handleStop(event *HookEvent, sessionID string) {
	reason := ""
	if event.Stop != nil {
		reason = event.Stop.Reason
	}
	log.Info("hooks: Stop", "reason", reason, "session", sessionID)

	// Mark hook worker as done.
	hr.trackHookWorker(event, sessionID, "done", "", "Stop")

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
	log.Info("hooks: Notification", "session", sessionID)

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

// handleUsageUpdate receives usage data from the Claude Code statusline script.
// POST /api/hooks/usage
func (hr *hookReceiver) handleUsageUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		SessionID    string  `json:"sessionId"`
		CostUSD      float64 `json:"costUsd"`
		InputTokens  int     `json:"inputTokens"`
		OutputTokens int     `json:"outputTokens"`
		ContextPct   int     `json:"contextPct"`
		Model        string  `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if body.SessionID == "" {
		http.Error(w, `{"error":"sessionId required"}`, http.StatusBadRequest)
		return
	}

	hr.hookWorkersMu.Lock()
	// Find worker by prefix match (statusline may send full or short session ID).
	var target *hookWorkerInfo
	for sid, w := range hr.hookWorkers {
		if strings.HasPrefix(sid, body.SessionID) || strings.HasPrefix(body.SessionID, sid) {
			target = w
			break
		}
	}
	if target == nil {
		// Create a minimal worker entry so usage data is visible even before first hook event.
		target = &hookWorkerInfo{
			SessionID: body.SessionID,
			State:     "working",
			FirstSeen: time.Now(),
			LastSeen:  time.Now(),
		}
		hr.hookWorkers[body.SessionID] = target
	}
	target.CostUSD = body.CostUSD
	target.InputTokens = body.InputTokens
	target.OutputTokens = body.OutputTokens
	target.ContextPct = body.ContextPct
	if body.Model != "" {
		target.Model = body.Model
	}
	target.LastSeen = time.Now()
	hr.hookWorkersMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// --- Plan File Detection ---

// checkPlanFileWrite checks if a Write/Edit tool call is targeting a plan file.
func (hr *hookReceiver) checkPlanFileWrite(event *HookEvent, sessionID string) {
	// Get tool input from either format.
	toolInput := event.ToolInput
	if len(toolInput) == 0 && event.Tool != nil {
		toolInput = event.Tool.Input
	}
	if len(toolInput) == 0 {
		return
	}

	// Parse tool input to find file_path.
	var input struct {
		FilePath string `json:"file_path"`
	}
	if err := json.Unmarshal(toolInput, &input); err != nil || input.FilePath == "" {
		return
	}

	// Check if writing to a plan file location.
	homeDir, _ := os.UserHomeDir()
	planDir := filepath.Join(homeDir, ".claude", "plans")
	if !strings.HasPrefix(input.FilePath, planDir) {
		return
	}

	log.Info("hooks: plan file write detected", "path", input.FilePath, "session", sessionID)

	// Read the plan file content.
	content, err := os.ReadFile(input.FilePath)
	if err != nil {
		log.Warn("hooks: failed to read plan file", "path", input.FilePath, "error", err)
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
	log.Info("hooks: plan review triggered (ExitPlanMode)", "session", sessionID)

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

// --- Plan Gate (PreToolUse:ExitPlanMode long-poll) ---

// handlePlanGate handles POST /api/hooks/plan-gate.
// Called by the PreToolUse:ExitPlanMode hook script. Blocks until Discord approval.
func (s *Server) handlePlanGate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, `{"error":"read body failed"}`, http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Parse hook event from Claude Code.
	var event HookEvent
	json.Unmarshal(body, &event)
	sessionID := event.ResolvedSessionID()
	if sessionID == "" {
		sessionID = s.hookReceiver.extractSessionID(&event, body)
	}

	hr := s.hookReceiver
	cfg := s.Cfg()

	// Read cached plan content.
	planText := ""
	if sessionID != "" {
		if plan := hr.GetCachedPlan(sessionID); plan != nil {
			planText = plan.Content
		}
	}

	// --- Keyboard mode: allow immediately (no terminal UI in --print mode) ---
	if cfg.PlanGate.Mode == "keyboard" {
		log.Info("plan gate: keyboard mode, allowing immediately", "session", sessionID)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"hookSpecificOutput": map[string]any{
				"hookEventName":      "PreToolUse",
				"permissionDecision": "allow",
			},
		})
		return
	}

	// Generate gate ID.
	sessionShort := sessionID
	if len(sessionShort) > 8 {
		sessionShort = sessionShort[:8]
	}
	gateID := fmt.Sprintf("pg-%s-%d", sessionShort, time.Now().Unix())

	// Create decision channel.
	ch := make(chan planGateDecision, 1)
	hr.planGatesMu.Lock()
	hr.planGates[gateID] = ch
	hr.planGatesMu.Unlock()
	defer func() {
		hr.planGatesMu.Lock()
		delete(hr.planGates, gateID)
		hr.planGatesMu.Unlock()
	}()

	// Insert plan review DB record.
	review := &PlanReview{
		ID:        gateID,
		SessionID: sessionID,
		PlanText:  planText,
		Status:    "pending",
		CreatedAt: time.Now().Format(time.RFC3339),
	}
	if cfg.HistoryDB != "" {
		insertPlanReview(cfg.HistoryDB, review)
	}

	// Send to Discord if available.
	if bot, _ := cfg.Runtime.DiscordBot.(*DiscordBot); bot != nil {
		embed := buildPlanReviewEmbed(review)
		customApprove := "pgate_approve:" + gateID
		customReject := "pgate_reject:" + gateID
		components := []discordComponent{
			discordActionRow(
				discordButton(customApprove, "Approve Plan", buttonStyleSuccess),
				discordButton(customReject, "Reject Plan", buttonStyleDanger),
			),
		}

		bot.interactions.register(&pendingInteraction{
			CustomID:  customApprove,
			CreatedAt: time.Now(),
			Response: &discordInteractionResponse{
				Type: interactionResponseUpdateMessage,
				Data: &discordInteractionResponseData{
					Content: "✅ Plan approved.",
				},
			},
			Callback: func(data discordInteractionData) {
				if cfg.HistoryDB != "" {
					updatePlanReviewStatus(cfg.HistoryDB, gateID, "approved", "discord", "")
				}
				select {
				case ch <- planGateDecision{Approved: true}:
				default:
				}
			},
		})
		bot.interactions.register(&pendingInteraction{
			CustomID:  customReject,
			CreatedAt: time.Now(),
			Response: &discordInteractionResponse{
				Type: interactionResponseUpdateMessage,
				Data: &discordInteractionResponseData{
					Content: "❌ Plan rejected.",
				},
			},
			Callback: func(data discordInteractionData) {
				if cfg.HistoryDB != "" {
					updatePlanReviewStatus(cfg.HistoryDB, gateID, "rejected", "discord", "")
				}
				select {
				case ch <- planGateDecision{Approved: false, Reason: "Rejected via Discord"}:
				default:
				}
			},
		})
		defer func() {
			bot.interactions.remove(customApprove)
			bot.interactions.remove(customReject)
		}()

		notifyCh := bot.notifyChannelID()
		if notifyCh != "" {
			bot.sendEmbedWithComponents(notifyCh, embed, components)
		}

		log.Info("plan gate: waiting for Discord approval", "gateId", gateID, "session", sessionID)
	} else {
		// No Discord — auto-approve.
		log.Info("plan gate: no Discord bot, auto-approving", "gateId", gateID)
		ch <- planGateDecision{Approved: true}
	}

	// Publish to SSE for dashboard.
	if hr.broker != nil {
		hr.broker.Publish(SSEDashboardKey, SSEEvent{
			Type:      SSEPlanReview,
			SessionID: sessionID,
			Data: map[string]any{
				"gateId":    gateID,
				"sessionId": sessionID,
				"status":    "waiting",
			},
		})
	}

	// Long-poll: wait for decision or timeout (5 minutes).
	var decision planGateDecision
	select {
	case decision = <-ch:
		log.Info("plan gate: decision received", "gateId", gateID, "approved", decision.Approved)
	case <-time.After(5 * time.Minute):
		log.Warn("plan gate: timeout, auto-approving", "gateId", gateID)
		decision = planGateDecision{Approved: true, Reason: "timeout"}
	}

	// Clear cached plan.
	if sessionID != "" {
		hr.ClearPlanCache(sessionID)
	}

	// Return Claude Code hook response.
	w.Header().Set("Content-Type", "application/json")
	if decision.Approved {
		json.NewEncoder(w).Encode(map[string]any{
			"hookSpecificOutput": map[string]any{
				"hookEventName":     "PreToolUse",
				"permissionDecision": "allow",
			},
		})
	} else {
		reason := decision.Reason
		if reason == "" {
			reason = "Plan rejected by reviewer"
		}
		json.NewEncoder(w).Encode(map[string]any{
			"hookSpecificOutput": map[string]any{
				"hookEventName":     "PreToolUse",
				"permissionDecision": "deny",
				"reason":            reason,
			},
		})
	}
}

// --- Ask User (long-poll question gate) ---

// handleAskUser handles POST /api/hooks/ask-user.
// MCP tool tetora_ask_user routes questions here. Blocks until Discord response.
func (s *Server) handleAskUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		Question string   `json:"question"`
		Options  []string `json:"options,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if body.Question == "" {
		http.Error(w, `{"error":"question is required"}`, http.StatusBadRequest)
		return
	}

	hr := s.hookReceiver
	cfg := s.Cfg()

	qID := fmt.Sprintf("q-%d", time.Now().UnixNano())

	// Create answer channel.
	ch := make(chan string, 1)
	hr.questionGatesMu.Lock()
	hr.questionGates[qID] = ch
	hr.questionGatesMu.Unlock()
	defer func() {
		hr.questionGatesMu.Lock()
		delete(hr.questionGates, qID)
		hr.questionGatesMu.Unlock()
	}()

	// Send to Discord.
	var cleanupIDs []string
	var cleanupBot *DiscordBot
	if bot, _ := cfg.Runtime.DiscordBot.(*DiscordBot); bot != nil {
		notifyCh := bot.notifyChannelID()
		if notifyCh != "" {
			cleanupBot = bot

			// Build buttons for options.
			var buttons []discordComponent
			for i, opt := range body.Options {
				if i >= 4 {
					break // Discord max 5 buttons per row, keep room for "Type" button
				}
				customID := fmt.Sprintf("askuser_%s_%d", qID, i)
				answer := opt
				bot.interactions.register(&pendingInteraction{
					CustomID:  customID,
					CreatedAt: time.Now(),
					Callback: func(data discordInteractionData) {
						select {
						case ch <- answer:
						default:
						}
					},
				})
				cleanupIDs = append(cleanupIDs, customID)
				buttons = append(buttons, discordButton(customID, truncate(opt, 80), buttonStylePrimary))
			}

			// Add "Type" button for free-text input.
			typeButtonID := "askuser_type_" + qID
			typeModalID := "askuser_modal_" + qID
			modalResp := discordBuildModal(typeModalID, "Your Answer",
				discordTextInput("answer_text", "Answer", true))
			bot.interactions.register(&pendingInteraction{
				CustomID:      typeButtonID,
				CreatedAt:     time.Now(),
				ModalResponse: &modalResp,
			})
			cleanupIDs = append(cleanupIDs, typeButtonID)

			bot.interactions.register(&pendingInteraction{
				CustomID:  typeModalID,
				CreatedAt: time.Now(),
				Callback: func(data discordInteractionData) {
					values := extractModalValues(data.Components)
					text := values["answer_text"]
					if text == "" {
						text = "(empty)"
					}
					select {
					case ch <- text:
					default:
					}
				},
			})
			cleanupIDs = append(cleanupIDs, typeModalID)

			buttons = append(buttons, discordButton(typeButtonID, "Type...", buttonStyleSecondary))

			content := fmt.Sprintf("**Question from Claude Code:**\n%s", body.Question)
			components := []discordComponent{discordActionRow(buttons...)}
			bot.sendMessageWithComponents(notifyCh, content, components)

			log.Info("ask-user: waiting for Discord answer", "qId", qID)
		}
	} else {
		// No Discord — return empty answer.
		log.Info("ask-user: no Discord bot, returning empty", "qId", qID)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"answer": "(no Discord configured)"})
		return
	}

	// Long-poll: wait for answer or timeout (6 minutes).
	var answer string
	select {
	case answer = <-ch:
		log.Info("ask-user: answer received", "qId", qID)
	case <-time.After(6 * time.Minute):
		log.Warn("ask-user: timeout", "qId", qID)
		answer = "(timeout: no answer received)"
	}

	// Cleanup all registered interactions.
	if cleanupBot != nil {
		for _, id := range cleanupIDs {
			cleanupBot.interactions.remove(id)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"answer": answer})
}

// --- Hook-Based Worker Tracking ---

// RegisterOrigin registers a worker origin before the CLI session starts.
func (hr *hookReceiver) RegisterOrigin(sessionID string, o *workerOrigin) {
	hr.workerOriginsMu.Lock()
	hr.workerOrigins[sessionID] = o
	hr.workerOriginsMu.Unlock()
}

// RegisterOriginIfAbsent registers origin only if not already registered (e.g. by cron layer).
func (hr *hookReceiver) RegisterOriginIfAbsent(sessionID string, o *workerOrigin) {
	hr.workerOriginsMu.Lock()
	if _, exists := hr.workerOrigins[sessionID]; !exists {
		hr.workerOrigins[sessionID] = o
	}
	hr.workerOriginsMu.Unlock()
}

// trackHookWorker creates or updates a hook worker entry.
func (hr *hookReceiver) trackHookWorker(event *HookEvent, sessionID string, state, toolName, eventType string) {
	if sessionID == "" {
		return
	}

	cwd := event.ResolvedCwd()

	hr.hookWorkersMu.Lock()
	defer hr.hookWorkersMu.Unlock()

	w, ok := hr.hookWorkers[sessionID]
	if !ok {
		w = &hookWorkerInfo{
			SessionID: sessionID,
			FirstSeen: time.Now(),
		}
		// Bring in origin from registry.
		hr.workerOriginsMu.RLock()
		w.Origin = hr.workerOrigins[sessionID]
		hr.workerOriginsMu.RUnlock()
		hr.hookWorkers[sessionID] = w
	}
	w.State = state
	w.LastSeen = time.Now()
	if toolName != "" {
		w.LastTool = toolName
		w.ToolCount++
	}
	if cwd != "" {
		w.Cwd = cwd
	}

	// Record safe event (no sensitive data).
	if eventType != "" {
		entry := hookWorkerEvent{
			Timestamp: time.Now().Format(time.RFC3339),
			EventType: eventType,
			ToolName:  toolName,
		}
		w.Events = append(w.Events, entry)
		if len(w.Events) > hookWorkerEventsMax {
			w.Events = w.Events[len(w.Events)-hookWorkerEventsMax:]
		}
	}
}

// ListHookWorkers returns all hook-tracked workers.
func (hr *hookReceiver) ListHookWorkers() []*hookWorkerInfo {
	hr.hookWorkersMu.RLock()
	defer hr.hookWorkersMu.RUnlock()

	out := make([]*hookWorkerInfo, 0, len(hr.hookWorkers))
	for _, w := range hr.hookWorkers {
		out = append(out, w)
	}
	return out
}

// FindHookWorkerByPrefix returns a worker matching the session ID prefix, plus a snapshot of its events.
func (hr *hookReceiver) FindHookWorkerByPrefix(prefix string) (*hookWorkerInfo, []hookWorkerEvent) {
	hr.hookWorkersMu.RLock()
	defer hr.hookWorkersMu.RUnlock()

	for sid, w := range hr.hookWorkers {
		if strings.HasPrefix(sid, prefix) {
			events := make([]hookWorkerEvent, len(w.Events))
			copy(events, w.Events)
			return w, events
		}
	}
	return nil, nil
}

// HasActiveWorkers returns true if any hook worker is in "working" state.
func (hr *hookReceiver) HasActiveWorkers() bool {
	hr.hookWorkersMu.RLock()
	defer hr.hookWorkersMu.RUnlock()
	for _, w := range hr.hookWorkers {
		if w.State == "working" {
			return true
		}
	}
	return false
}

// cleanupStaleHookWorkers removes hook workers not seen in 10 minutes.
func (hr *hookReceiver) cleanupStaleHookWorkers() {
	hr.hookWorkersMu.Lock()
	defer hr.hookWorkersMu.Unlock()
	cutoff := time.Now().Add(-10 * time.Minute)
	for sid, w := range hr.hookWorkers {
		if w.LastSeen.Before(cutoff) {
			delete(hr.hookWorkers, sid)
		}
	}
	// Also clean up stale origins (same 10-minute window).
	hr.workerOriginsMu.Lock()
	for sid := range hr.workerOrigins {
		// Keep origin if worker still exists.
		if _, exists := hr.hookWorkers[sid]; !exists {
			delete(hr.workerOrigins, sid)
		}
	}
	hr.workerOriginsMu.Unlock()
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
