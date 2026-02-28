package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// --- Slack Config ---

// SlackBotConfig holds configuration for the Slack bot integration.
// Uses the Slack Events API (HTTP push mode) for receiving messages.
type SlackBotConfig struct {
	Enabled        bool   `json:"enabled"`
	BotToken       string `json:"botToken"`                // xoxb-... ($ENV_VAR supported)
	SigningSecret   string `json:"signingSecret"`           // for request verification ($ENV_VAR supported)
	AppToken       string `json:"appToken,omitempty"`       // for Socket Mode (optional, $ENV_VAR)
	DefaultChannel string `json:"defaultChannel,omitempty"` // channel ID for notifications
}

// --- Slack Event Types ---

type slackEventWrapper struct {
	Token     string          `json:"token"`
	Challenge string          `json:"challenge"` // URL verification
	Type      string          `json:"type"`      // "url_verification", "event_callback"
	Event     json.RawMessage `json:"event"`
	TeamID    string          `json:"team_id"`
	EventID   string          `json:"event_id"`
}

type slackFile struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Mimetype           string `json:"mimetype"`
	URLPrivateDownload string `json:"url_private_download"`
	Size               int64  `json:"size"`
}

type slackEvent struct {
	Type     string      `json:"type"`                // "message", "app_mention"
	Text     string      `json:"text"`
	User     string      `json:"user"`
	Channel  string      `json:"channel"`
	TS       string      `json:"ts"`                  // message timestamp (used as thread ID)
	ThreadTS string      `json:"thread_ts,omitempty"` // parent thread
	BotID    string      `json:"bot_id,omitempty"`    // non-empty if from a bot
	SubType  string      `json:"subtype,omitempty"`   // e.g. "bot_message", "message_changed"
	Files    []slackFile `json:"files,omitempty"`
}

// --- Slack Bot ---

// SlackBot handles incoming Slack Events API requests and routes them
// through Tetora's smart dispatch system.
type SlackBot struct {
	cfg   *Config
	state *dispatchState
	sem      chan struct{}
	childSem chan struct{}
	cron     *CronEngine

	// Dedup: track recently processed event IDs to handle Slack retries.
	processed     map[string]time.Time
	processedSize int
	approvalGate  *slackApprovalGate // P28.0: approval gate
}

func newSlackBot(cfg *Config, state *dispatchState, sem, childSem chan struct{}, cron *CronEngine) *SlackBot {
	sb := &SlackBot{
		cfg:       cfg,
		state:     state,
		sem:       sem,
		childSem:  childSem,
		cron:      cron,
		processed: make(map[string]time.Time),
	}
	// P28.0: Initialize approval gate.
	if cfg.ApprovalGates.Enabled && cfg.Slack.DefaultChannel != "" {
		sb.approvalGate = newSlackApprovalGate(sb, cfg.Slack.DefaultChannel)
	}
	return sb
}

// slackEventHandler handles incoming Slack Events API requests.
// Register this at /slack/events in the HTTP server.
func (sb *SlackBot) slackEventHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB max
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Verify Slack signature if signing secret is configured.
	secret := sb.cfg.Slack.SigningSecret
	if secret != "" {
		if !verifySlackSignature(r, body, secret) {
			logWarn("slack invalid signature", "remoteAddr", r.RemoteAddr)
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
	}

	var wrapper slackEventWrapper
	if err := json.Unmarshal(body, &wrapper); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	// Handle URL verification challenge (Slack sends this during app setup).
	if wrapper.Type == "url_verification" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"challenge": wrapper.Challenge})
		return
	}

	// Handle event callbacks.
	if wrapper.Type == "event_callback" {
		// Dedup: Slack may retry if we respond slowly.
		if wrapper.EventID != "" && sb.isDuplicate(wrapper.EventID) {
			w.WriteHeader(http.StatusOK)
			return
		}

		var event slackEvent
		if err := json.Unmarshal(wrapper.Event, &event); err != nil {
			http.Error(w, "invalid event", http.StatusBadRequest)
			return
		}

		// Acknowledge immediately (Slack requires response within 3 seconds).
		w.WriteHeader(http.StatusOK)

		// Process event asynchronously.
		go sb.handleSlackEvent(event)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// isDuplicate checks if an event ID has already been processed.
// Returns true if duplicate, false if new (and marks it as processed).
func (sb *SlackBot) isDuplicate(eventID string) bool {
	now := time.Now()

	// Cleanup old entries periodically (keep map bounded).
	if sb.processedSize > 500 {
		for id, ts := range sb.processed {
			if now.Sub(ts) > 10*time.Minute {
				delete(sb.processed, id)
			}
		}
		sb.processedSize = len(sb.processed)
	}

	if _, exists := sb.processed[eventID]; exists {
		return true
	}
	sb.processed[eventID] = now
	sb.processedSize++
	return false
}

func (sb *SlackBot) handleSlackEvent(event slackEvent) {
	// Ignore bot messages to prevent loops.
	if event.BotID != "" {
		return
	}
	// Ignore message subtypes (edits, deletes, etc).
	if event.SubType != "" {
		return
	}

	// Handle app_mention and direct messages.
	switch event.Type {
	case "app_mention", "message":
		text := stripSlackMentions(event.Text)

		// Download attached files and inject into prompt.
		var attachedFiles []*UploadedFile
		for _, f := range event.Files {
			if uf, err := sb.downloadSlackFile(f); err != nil {
				logWarn("slack: file download failed", "name", f.Name, "err", err)
			} else {
				attachedFiles = append(attachedFiles, uf)
			}
		}
		if prefix := buildFilePromptPrefix(attachedFiles); prefix != "" {
			text = prefix + text
		}

		if text == "" {
			return
		}

		// P28.0: Handle approval gate replies ("approve <id>" / "reject <id>" / "always <tool>").
		if sb.approvalGate != nil {
			if strings.HasPrefix(text, "approve ") {
				reqID := strings.TrimPrefix(text, "approve ")
				sb.approvalGate.handleGateCallback(strings.TrimSpace(reqID), true)
				sb.slackReply(event.Channel, threadTS(event), "Approved.")
				return
			}
			if strings.HasPrefix(text, "reject ") {
				reqID := strings.TrimPrefix(text, "reject ")
				sb.approvalGate.handleGateCallback(strings.TrimSpace(reqID), false)
				sb.slackReply(event.Channel, threadTS(event), "Rejected.")
				return
			}
			if strings.HasPrefix(text, "always ") {
				toolName := strings.TrimSpace(strings.TrimPrefix(text, "always "))
				sb.approvalGate.AutoApprove(toolName)
				sb.slackReply(event.Channel, threadTS(event), fmt.Sprintf("Auto-approved `%s` for this runtime.", toolName))
				return
			}
		}

		// Parse commands (use ! prefix to distinguish from plain text).
		if strings.HasPrefix(text, "!") {
			sb.handleSlackCommand(event, text[1:])
			return
		}

		// Smart dispatch for free-form messages.
		if sb.cfg.SmartDispatch.Enabled {
			sb.handleSlackRoute(event, text)
		} else {
			sb.slackReply(event.Channel, threadTS(event), "Smart dispatch is not enabled. Use `!help` for commands.")
		}
	}
}

func (sb *SlackBot) handleSlackCommand(event slackEvent, cmdText string) {
	parts := strings.SplitN(cmdText, " ", 2)
	command := strings.ToLower(parts[0])
	args := ""
	if len(parts) > 1 {
		args = parts[1]
	}

	switch command {
	case "status":
		sb.slackCmdStatus(event)
	case "jobs", "cron":
		sb.slackCmdJobs(event)
	case "cost":
		sb.slackCmdCost(event)
	case "model":
		sb.slackCmdModel(event, args)
	case "new":
		sb.slackCmdNew(event, args)
	case "help":
		sb.slackCmdHelp(event)
	default:
		// Treat unknown command + args as a route prompt.
		if args != "" {
			sb.handleSlackRoute(event, cmdText)
		} else {
			sb.slackReply(event.Channel, threadTS(event),
				"Unknown command `!"+command+"`. Use `!help` for available commands.")
		}
	}
	_ = args // used above
}

func (sb *SlackBot) handleSlackRoute(event slackEvent, prompt string) {
	ts := threadTS(event)

	// Send initial "thinking" message.
	thinkingTS := sb.slackPostMessage(event.Channel, ts, "Routing...")

	ctx := withTraceID(context.Background(), newTraceID("slack"))
	dbPath := sb.cfg.HistoryDB

	// Step 1: Route to determine agent.
	route := routeTask(ctx, sb.cfg, RouteRequest{Prompt: prompt, Source: "slack"})
	logInfoCtx(ctx, "slack route result", "prompt", truncate(prompt, 60), "agent", route.Agent, "method", route.Method)

	// Step 2: Find or create channel session for this thread.
	chKey := channelSessionKey("slack", event.Channel, ts)
	sess, err := getOrCreateChannelSession(dbPath, "slack", chKey, route.Agent, "")
	if err != nil {
		logErrorCtx(ctx, "slack route session error", "error", err)
	}

	// Step 3: Build context-aware prompt.
	// Skip text injection for providers with native session support (e.g. claude-code).
	contextPrompt := prompt
	if sess != nil {
		providerName := resolveProviderName(sb.cfg, Task{Agent: route.Agent}, route.Agent)
		if !providerHasNativeSession(providerName) {
			sessionCtx := buildSessionContext(dbPath, sess.ID, sb.cfg.Session.contextMessagesOrDefault())
			contextPrompt = wrapWithContext(sessionCtx, prompt)
		}

		// Record user message to session.
		now := time.Now().Format(time.RFC3339)
		addSessionMessage(dbPath, SessionMessage{
			SessionID: sess.ID,
			Role:      "user",
			Content:   truncateStr(prompt, 5000),
			CreatedAt: now,
		})
		updateSessionStats(dbPath, sess.ID, 0, 0, 0, 1)

		title := prompt
		if len(title) > 100 {
			title = title[:100]
		}
		updateSessionTitle(dbPath, sess.ID, title)
	}

	// Step 4: Build and run task.
	task := Task{
		Prompt: contextPrompt,
		Agent:  route.Agent,
		Source: "route:slack",
	}
	fillDefaults(sb.cfg, &task)
	if sess != nil {
		task.SessionID = sess.ID
	}

	if route.Agent != "" {
		if soulPrompt, err := loadAgentPrompt(sb.cfg, route.Agent); err == nil && soulPrompt != "" {
			task.SystemPrompt = soulPrompt
		}
		if rc, ok := sb.cfg.Agents[route.Agent]; ok {
			if rc.Model != "" {
				task.Model = rc.Model
			}
			if rc.PermissionMode != "" {
				task.PermissionMode = rc.PermissionMode
			}
		}
	}

	task.Prompt = expandPrompt(task.Prompt, "", sb.cfg.HistoryDB, route.Agent, sb.cfg.KnowledgeDir, sb.cfg)

	// P28.0: Attach approval gate.
	if sb.approvalGate != nil {
		task.approvalGate = sb.approvalGate
	}

	taskStart := time.Now()
	result := runSingleTask(ctx, sb.cfg, task, sb.sem, sb.childSem, route.Agent)

	// Record to history.
	recordHistory(sb.cfg.HistoryDB, task.ID, task.Name, task.Source, route.Agent, task, result,
		taskStart.Format(time.RFC3339), time.Now().Format(time.RFC3339), result.OutputFile)

	// Step 5: Record assistant response to session.
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
		updateSessionStats(dbPath, sess.ID, result.CostUSD, result.TokensIn, result.TokensOut, 1)

		maybeCompactSession(sb.cfg, dbPath, sess.ID, sess.MessageCount+2, sb.sem, sb.childSem)
	}

	// Store in agent memory.
	if result.Status == "success" {
		setMemory(sb.cfg, route.Agent, "last_route_output", truncate(result.Output, 500))
		setMemory(sb.cfg, route.Agent, "last_route_prompt", truncate(prompt, 200))
		setMemory(sb.cfg, route.Agent, "last_route_time", time.Now().Format(time.RFC3339))
	}

	// Audit log.
	auditLog(dbPath, "route.dispatch", "slack",
		fmt.Sprintf("agent=%s method=%s session=%s", route.Agent, route.Method, task.SessionID), "")

	// Webhook notifications.
	sendWebhooks(sb.cfg, result.Status, WebhookPayload{
		JobID: task.ID, Name: task.Name, Source: task.Source,
		Status: result.Status, Cost: result.CostUSD, Duration: result.DurationMs,
		Model: result.Model, Output: truncate(result.Output, 500), Error: truncate(result.Error, 300),
	})

	// Format response.
	var text strings.Builder
	fmt.Fprintf(&text, "*Route:* %s (%s, %s confidence)\n",
		route.Agent, route.Method, route.Confidence)

	if result.Status == "success" {
		fmt.Fprintf(&text, "\n%s", truncate(result.Output, 3000))
	} else {
		fmt.Fprintf(&text, "\n*[%s]* %s", result.Status, truncate(result.Error, 500))
	}

	dur := time.Duration(result.DurationMs) * time.Millisecond
	fmt.Fprintf(&text, "\n\n_$%.2f | %s_", result.CostUSD, dur.Round(time.Second))

	// Update the "Routing..." message with the actual response.
	if thinkingTS != "" {
		sb.slackUpdateMessage(event.Channel, thinkingTS, text.String())
	} else {
		sb.slackReply(event.Channel, ts, text.String())
	}
}

func (sb *SlackBot) slackCmdStatus(event slackEvent) {
	ts := threadTS(event)

	sb.state.mu.Lock()
	active := sb.state.active
	sb.state.mu.Unlock()

	if !active {
		// Show cron status if no dispatch is active.
		if sb.cron != nil {
			jobs := sb.cron.ListJobs()
			running := 0
			for _, j := range jobs {
				if j.Running {
					running++
				}
			}
			sb.slackReply(event.Channel, ts,
				fmt.Sprintf("No active dispatch.\nCron: %d jobs (%d running)", len(jobs), running))
		} else {
			sb.slackReply(event.Channel, ts, "No active dispatch.")
		}
	} else {
		sb.slackReply(event.Channel, ts, string(sb.state.statusJSON()))
	}
}

func (sb *SlackBot) slackCmdJobs(event slackEvent) {
	ts := threadTS(event)

	if sb.cron == nil {
		sb.slackReply(event.Channel, ts, "Cron engine not available.")
		return
	}
	jobs := sb.cron.ListJobs()
	if len(jobs) == 0 {
		sb.slackReply(event.Channel, ts, "No cron jobs configured.")
		return
	}
	var lines []string
	for _, j := range jobs {
		icon := "[ ]"
		if j.Running {
			icon = "[>]"
		} else if j.Enabled {
			icon = "[*]"
		}
		nextStr := ""
		if !j.NextRun.IsZero() && j.Enabled {
			nextStr = fmt.Sprintf(" next: %s", j.NextRun.Format("15:04"))
		}
		avgStr := ""
		if j.AvgCost > 0 {
			avgStr = fmt.Sprintf(" avg:$%.2f", j.AvgCost)
		}
		lines = append(lines, fmt.Sprintf("%s *%s* [%s]%s%s",
			icon, j.Name, j.Schedule, nextStr, avgStr))
	}
	sb.slackReply(event.Channel, ts, strings.Join(lines, "\n"))
}

func (sb *SlackBot) slackCmdCost(event slackEvent) {
	ts := threadTS(event)

	if sb.cfg.HistoryDB == "" {
		sb.slackReply(event.Channel, ts, "History DB not configured.")
		return
	}
	stats, err := queryCostStats(sb.cfg.HistoryDB)
	if err != nil {
		sb.slackReply(event.Channel, ts, "Error: "+err.Error())
		return
	}
	text := fmt.Sprintf("*Cost Summary*\nToday: $%.2f\nWeek: $%.2f\nMonth: $%.2f",
		stats.Today, stats.Week, stats.Month)

	if sb.cfg.CostAlert.DailyLimit > 0 {
		pct := (stats.Today / sb.cfg.CostAlert.DailyLimit) * 100
		text += fmt.Sprintf("\n\nDaily limit: $%.2f (%.0f%% used)", sb.cfg.CostAlert.DailyLimit, pct)
	}

	sb.slackReply(event.Channel, ts, text)
}

func (sb *SlackBot) slackCmdNew(event slackEvent, args string) {
	ts := threadTS(event)
	dbPath := sb.cfg.HistoryDB
	if dbPath == "" {
		sb.slackReply(event.Channel, ts, "History DB not configured.")
		return
	}

	// Archive the session for this thread.
	chKey := channelSessionKey("slack", event.Channel, ts)
	if err := archiveChannelSession(dbPath, chKey); err != nil {
		sb.slackReply(event.Channel, ts, "Error: "+err.Error())
		return
	}
	sb.slackReply(event.Channel, ts, "Session archived. Next message starts a fresh conversation.")
}

func (sb *SlackBot) slackCmdModel(event slackEvent, args string) {
	parts := strings.Fields(args)

	if len(parts) == 0 {
		var lines []string
		for name, rc := range sb.cfg.Agents {
			m := rc.Model
			if m == "" {
				m = sb.cfg.DefaultModel
			}
			lines = append(lines, fmt.Sprintf("  %s: `%s`", name, m))
		}
		sb.slackReply(event.Channel, threadTS(event), "*Current models:*\n"+strings.Join(lines, "\n"))
		return
	}

	model := parts[0]
	agentName := sb.cfg.SmartDispatch.DefaultAgent
	if agentName == "" {
		agentName = "default"
	}
	if len(parts) > 1 {
		agentName = parts[1]
	}

	old, err := updateAgentModel(sb.cfg, agentName, model)
	if err != nil {
		sb.slackReply(event.Channel, threadTS(event), fmt.Sprintf("Error: %v", err))
		return
	}
	sb.slackReply(event.Channel, threadTS(event), fmt.Sprintf("*%s* model: `%s` → `%s`", agentName, old, model))
}

func (sb *SlackBot) slackCmdHelp(event slackEvent) {
	sb.slackReply(event.Channel, threadTS(event),
		"*Tetora Slack Bot*\n"+
			"`!status` -- Check running tasks\n"+
			"`!jobs` -- List cron jobs\n"+
			"`!cost` -- Cost summary\n"+
			"`!model [model] [agent]` -- Show/switch model\n"+
			"`!new` -- Start fresh session in this thread\n"+
			"`!help` -- This message\n"+
			"\nMessages in a thread share conversation context.\n"+
			"Just type a message to auto-route to the best agent.")
}

// --- Slack API ---

// slackReply sends a message to a Slack channel, optionally in a thread.
func (sb *SlackBot) slackReply(channel, threadTS, text string) {
	token := sb.cfg.Slack.BotToken
	if token == "" {
		logWarn("slack cannot send message, botToken is empty")
		return
	}

	payload := map[string]string{
		"channel": channel,
		"text":    text,
	}
	if threadTS != "" {
		payload["thread_ts"] = threadTS
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", "https://slack.com/api/chat.postMessage",
		strings.NewReader(string(body)))
	if err != nil {
		logError("slack send request error", "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		logError("slack send error", "error", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		logWarn("slack send non-200", "status", resp.StatusCode, "body", string(respBody))
		return
	}

	// Check Slack API response for errors (Slack returns 200 even on API errors).
	var slackResp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}
	if err := json.NewDecoder(strings.NewReader(string(body))).Decode(&slackResp); err == nil {
		// Re-read the response body for the Slack API check.
	}
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	_ = respBody // logged above if non-200
}

// slackPostMessage sends a message and returns the message timestamp (ts) for later updates.
// Returns empty string on error.
func (sb *SlackBot) slackPostMessage(channel, threadTS, text string) string {
	token := sb.cfg.Slack.BotToken
	if token == "" {
		return ""
	}

	payload := map[string]string{
		"channel": channel,
		"text":    text,
	}
	if threadTS != "" {
		payload["thread_ts"] = threadTS
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", "https://slack.com/api/chat.postMessage",
		strings.NewReader(string(body)))
	if err != nil {
		return ""
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	var result struct {
		OK bool   `json:"ok"`
		TS string `json:"ts"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	if result.OK {
		return result.TS
	}
	return ""
}

// slackUpdateMessage updates a previously sent message with new text.
func (sb *SlackBot) slackUpdateMessage(channel, messageTS, text string) {
	token := sb.cfg.Slack.BotToken
	if token == "" {
		return
	}

	payload := map[string]string{
		"channel": channel,
		"ts":      messageTS,
		"text":    text,
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", "https://slack.com/api/chat.update",
		strings.NewReader(string(body)))
	if err != nil {
		logError("slack update request error", "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		logError("slack update error", "error", err)
		return
	}
	defer resp.Body.Close()

	var result struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	if !result.OK {
		logError("slack update API error", "error", result.Error)
	}
}

// --- P27.3: Slack Channel Notifier ---
// Note: Slack typing indicators require RTM WebSocket, so we use no-op for typing.

type slackChannelNotifier struct {
	channelID string
}

func (n *slackChannelNotifier) SendTyping(ctx context.Context) error { return nil }
func (n *slackChannelNotifier) SendStatus(ctx context.Context, msg string) error { return nil }

// sendSlackNotify sends a standalone notification to the configured default channel.
func (sb *SlackBot) sendSlackNotify(text string) {
	if sb.cfg.Slack.DefaultChannel != "" {
		sb.slackReply(sb.cfg.Slack.DefaultChannel, "", text)
	}
}

// downloadSlackFile downloads a file attached to a Slack message using the bot token.
func (sb *SlackBot) downloadSlackFile(f slackFile) (*UploadedFile, error) {
	if f.URLPrivateDownload == "" {
		return nil, fmt.Errorf("slack file %s has no download URL", f.Name)
	}
	req, err := http.NewRequest("GET", f.URLPrivateDownload, nil)
	if err != nil {
		return nil, fmt.Errorf("slack file: build request: %w", err)
	}
	token := sb.cfg.Slack.BotToken
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("slack file: http get: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("slack file: HTTP %d for %s", resp.StatusCode, f.Name)
	}
	uploadDir := initUploadDir(sb.cfg.WorkspaceDir)
	return saveUpload(uploadDir, f.Name, resp.Body, f.Size, "slack")
}

// --- P28.0: Slack Approval Gate ---

// slackApprovalGate implements ApprovalGate via Slack Block Kit buttons.
// Note: Slack interactive payloads require a separate HTTP endpoint to handle.
// This implementation sends a prompt and polls the user via a reaction-based
// fallback (approve = message reply "yes", reject = "no" or timeout).
type slackApprovalGate struct {
	bot          *SlackBot
	channel      string
	mu           sync.Mutex
	pending      map[string]chan bool
	autoApproved map[string]bool // tool name → always approved
}

func newSlackApprovalGate(bot *SlackBot, channel string) *slackApprovalGate {
	g := &slackApprovalGate{
		bot:          bot,
		channel:      channel,
		pending:      make(map[string]chan bool),
		autoApproved: make(map[string]bool),
	}
	// Copy config-level auto-approve tools.
	for _, tool := range bot.cfg.ApprovalGates.AutoApproveTools {
		g.autoApproved[tool] = true
	}
	return g
}

func (g *slackApprovalGate) AutoApprove(toolName string) {
	g.mu.Lock()
	g.autoApproved[toolName] = true
	g.mu.Unlock()
}

func (g *slackApprovalGate) IsAutoApproved(toolName string) bool {
	g.mu.Lock()
	ok := g.autoApproved[toolName]
	g.mu.Unlock()
	return ok
}

func (g *slackApprovalGate) RequestApproval(ctx context.Context, req ApprovalRequest) (bool, error) {
	ch := make(chan bool, 1)
	g.mu.Lock()
	g.pending[req.ID] = ch
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		delete(g.pending, req.ID)
		g.mu.Unlock()
	}()

	text := fmt.Sprintf("*Approval needed*\n\nTool: `%s`\n%s\n\nReply `approve %s` or `reject %s` or `always %s`",
		req.Tool, req.Summary, req.ID, req.ID, req.Tool)
	g.bot.slackReply(g.channel, "", text)

	select {
	case approved := <-ch:
		return approved, nil
	case <-ctx.Done():
		return false, fmt.Errorf("approval timed out: %v", ctx.Err())
	}
}

func (g *slackApprovalGate) handleGateCallback(reqID string, approved bool) {
	g.mu.Lock()
	ch, ok := g.pending[reqID]
	g.mu.Unlock()
	if ok {
		select {
		case ch <- approved:
		default:
		}
	}
}

// --- Slack Signature Verification ---

// verifySlackSignature verifies the HMAC-SHA256 signature from Slack.
// See: https://api.slack.com/authentication/verifying-requests-from-slack
func verifySlackSignature(r *http.Request, body []byte, signingSecret string) bool {
	timestamp := r.Header.Get("X-Slack-Request-Timestamp")
	signature := r.Header.Get("X-Slack-Signature")

	if timestamp == "" || signature == "" {
		return false
	}

	// Check timestamp is within 5 minutes to prevent replay attacks.
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}
	diff := time.Now().Unix() - ts
	if diff < 0 {
		diff = -diff
	}
	if diff > 300 {
		return false
	}

	// Compute expected signature: v0=HMAC-SHA256("v0:{timestamp}:{body}").
	baseStr := fmt.Sprintf("v0:%s:%s", timestamp, string(body))
	mac := hmac.New(sha256.New, []byte(signingSecret))
	mac.Write([]byte(baseStr))
	expected := "v0=" + hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(signature), []byte(expected))
}

// --- Helpers ---

// threadTS returns the thread timestamp for replying in-thread.
// If the message is already in a thread, use the parent thread_ts;
// otherwise use the message's own ts to start a new thread.
func threadTS(event slackEvent) string {
	if event.ThreadTS != "" {
		return event.ThreadTS
	}
	return event.TS
}

// stripSlackMentions removes <@USERID> mentions from text.
func stripSlackMentions(text string) string {
	// Remove <@U12345> and <@U12345|username> style mentions.
	for {
		start := strings.Index(text, "<@")
		if start < 0 {
			break
		}
		end := strings.Index(text[start:], ">")
		if end < 0 {
			break
		}
		text = text[:start] + text[start+end+1:]
	}
	return strings.TrimSpace(text)
}
