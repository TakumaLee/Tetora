// Package telegram provides the Telegram bot integration.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"tetora/internal/messaging"
	"tetora/internal/trace"
)

// --- Telegram Types ---

type tgUpdate struct {
	UpdateID      int              `json:"update_id"`
	Message       *tgMessage       `json:"message"`
	CallbackQuery *tgCallbackQuery `json:"callback_query"`
}

type tgMessage struct {
	MessageID int           `json:"message_id"`
	Chat      tgChat        `json:"chat"`
	Text      string        `json:"text"`
	Caption   string        `json:"caption,omitempty"`
	Document  *tgDocument   `json:"document,omitempty"`
	Photo     []tgPhotoSize `json:"photo,omitempty"`
}

type tgDocument struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	MimeType string `json:"mime_type"`
	FileSize int64  `json:"file_size"`
}

type tgPhotoSize struct {
	FileID   string `json:"file_id"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	FileSize int64  `json:"file_size"`
}

type tgChat struct {
	ID int64 `json:"id"`
}

type tgCallbackQuery struct {
	ID      string     `json:"id"`
	From    tgUser     `json:"from"`
	Message *tgMessage `json:"message"`
	Data    string     `json:"data"`
}

type tgUser struct {
	ID int64 `json:"id"`
}

// Inline keyboard types for Telegram Bot API.
type tgInlineKeyboard struct {
	InlineKeyboard [][]InlineButton `json:"inline_keyboard"`
}

// InlineButton is a Telegram inline keyboard button.
// It is exported so that the root package (cron, etc.) can use it.
type InlineButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
	URL          string `json:"url,omitempty"`
}

// --- Bot ---

// pendingEstimate stores a task pending user cost confirmation.
type pendingEstimate struct {
	prompt    string
	isRoute   bool // true = /route flow, false = /ask flow
	estimate  *CostEstimate
	chatID    int64
	createdAt time.Time
}

const pendingEstimateTTL = 10 * time.Minute

// pendingSuggest stores a suggest-mode task result pending human approval.
type pendingSuggest struct {
	result    messaging.TaskResult
	role      string
	prompt    string
	chatID    int64
	createdAt time.Time
}

const pendingSuggestTTL = 30 * time.Minute

// Bot is the Telegram bot implementation.
type Bot struct {
	cfg              Config
	rt               TelegramRuntime
	token            string
	chatID           int64
	pollTimeout      int
	client           *http.Client
	pendingEstimates map[string]*pendingEstimate
	pendingSuggests  map[string]*pendingSuggest
	pendingMu        sync.Mutex
	approvalGate     *tgApprovalGate // P28.0: approval gate for this bot
}

// NewBot creates a new Telegram Bot instance.
func NewBot(cfg Config, rt TelegramRuntime) *Bot {
	pollTimeout := cfg.PollTimeoutOrDefault()
	b := &Bot{
		cfg:              cfg,
		rt:               rt,
		token:            cfg.BotToken,
		chatID:           cfg.ChatID,
		pollTimeout:      pollTimeout,
		client:           &http.Client{Timeout: time.Duration(pollTimeout+10) * time.Second},
		pendingEstimates: make(map[string]*pendingEstimate),
		pendingSuggests:  make(map[string]*pendingSuggest),
	}
	// P28.0: Create approval gate if enabled.
	if rt.ApprovalGatesEnabled() {
		b.approvalGate = newTGApprovalGate(b, cfg.ChatID, rt.ApprovalGateAutoApproveTools())
	}
	return b
}

// ReplyWithKeyboard sends a message with an inline keyboard (exported for cron callbacks).
func (b *Bot) ReplyWithKeyboard(chatID int64, text string, keyboard [][]InlineButton) {
	b.replyWithKeyboard(chatID, text, keyboard)
}

// ChatID returns the configured chat ID.
func (b *Bot) ChatID() int64 { return b.chatID }

// SendNotify sends a standalone Telegram message (for cron notifications etc).
func (b *Bot) SendNotify(text string) {
	b.reply(b.chatID, text)
}

// SetTyping sends a "typing" chat action to the given channel ref (implements PresenceSetter).
func (b *Bot) SetTyping(_ context.Context, channelRef string) error {
	var chatID int64
	fmt.Sscanf(channelRef, "%d", &chatID)
	if chatID == 0 {
		chatID = b.chatID
	}
	if chatID == 0 {
		return fmt.Errorf("telegram: no chat ID")
	}
	b.sendTypingAction(chatID)
	return nil
}

// PresenceName returns the channel name for presence tracking.
func (b *Bot) PresenceName() string { return "telegram" }

// ApprovalGate returns the approval gate for this bot (may be nil).
func (b *Bot) ApprovalGate() ApprovalGate {
	if b.approvalGate == nil {
		return nil
	}
	return b.approvalGate
}

// maybeCostConfirm checks if the estimated cost exceeds the threshold.
// If so, sends a confirmation keyboard and returns true (do NOT execute yet).
// If cost is below threshold, returns false (proceed immediately).
func (b *Bot) maybeCostConfirm(chatID int64, prompt string, isRoute bool) bool {
	est := b.rt.EstimateCost(prompt)
	if est == nil {
		return false
	}

	threshold := b.rt.EstimateThreshold()
	if est.TotalEstimatedCost < threshold {
		return false
	}

	id := b.rt.NewUUID()
	if len(id) > 8 {
		id = id[:8]
	}
	b.pendingMu.Lock()
	b.pendingEstimates[id] = &pendingEstimate{
		prompt:    prompt,
		isRoute:   isRoute,
		estimate:  est,
		chatID:    chatID,
		createdAt: time.Now(),
	}
	b.pendingMu.Unlock()

	var lines []string
	lines = append(lines, "Cost Estimate")
	for _, t := range est.Tasks {
		lines = append(lines, fmt.Sprintf("  %s (%s): ~$%.2f", t.Model, t.Provider, t.EstimatedCostUSD))
		lines = append(lines, fmt.Sprintf("    %s", t.Breakdown))
	}
	if est.ClassifyCost > 0 {
		lines = append(lines, fmt.Sprintf("  Classification: ~$%.4f", est.ClassifyCost))
	}
	lines = append(lines, fmt.Sprintf("\nTotal: ~$%.2f", est.TotalEstimatedCost))

	keyboard := [][]InlineButton{
		{
			{Text: fmt.Sprintf("Execute ($%.2f)", est.TotalEstimatedCost),
				CallbackData: "confirm_dispatch:" + id},
			{Text: "Cancel", CallbackData: "cancel_dispatch:" + id},
		},
	}
	b.replyWithKeyboard(chatID, strings.Join(lines, "\n"), keyboard)
	return true
}

// cleanupPendingEstimates removes expired entries.
func (b *Bot) cleanupPendingEstimates() {
	b.pendingMu.Lock()
	defer b.pendingMu.Unlock()
	now := time.Now()
	for id, pe := range b.pendingEstimates {
		if now.Sub(pe.createdAt) > pendingEstimateTTL {
			delete(b.pendingEstimates, id)
		}
	}
	for id, ps := range b.pendingSuggests {
		if now.Sub(ps.createdAt) > pendingSuggestTTL {
			delete(b.pendingSuggests, id)
		}
	}
}

// sendSuggestConfirm sends a suggest-mode output with approve/reject keyboard.
func (b *Bot) sendSuggestConfirm(chatID int64, result messaging.TaskResult, role string, prompt string) {
	id := b.rt.NewUUID()
	if len(id) > 8 {
		id = id[:8]
	}
	b.pendingMu.Lock()
	b.pendingSuggests[id] = &pendingSuggest{
		result:    result,
		role:      role,
		prompt:    prompt,
		chatID:    chatID,
		createdAt: time.Now(),
	}
	b.pendingMu.Unlock()

	var lines []string
	lines = append(lines, fmt.Sprintf("Suggest Mode [%s]", role))
	lines = append(lines, fmt.Sprintf("Trust level: suggest"))
	lines = append(lines, "")
	if result.Status == "success" {
		lines = append(lines, truncate(result.Output, 2500))
	} else {
		lines = append(lines, fmt.Sprintf("[%s] %s", result.Status, truncate(result.Error, 500)))
	}
	dur := time.Duration(result.DurationMs) * time.Millisecond
	lines = append(lines, fmt.Sprintf("\n$%.2f | %s", result.CostUSD, formatDuration(dur)))

	keyboard := [][]InlineButton{
		{
			{Text: "Approve", CallbackData: "trust_approve:" + id},
			{Text: "Reject", CallbackData: "trust_reject:" + id},
		},
	}
	b.replyWithKeyboard(chatID, strings.Join(lines, "\n"), keyboard)
}

// PollLoop runs the Telegram polling loop until ctx is cancelled.
func (b *Bot) PollLoop(ctx context.Context) {
	offset := 0
	b.rt.LogInfo("telegram bot polling started", "chatID", b.chatID)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		url := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?offset=%d&timeout=%d",
			b.token, offset, b.pollTimeout)

		req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
		resp, err := b.client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			b.rt.LogError("telegram poll error", err)
			time.Sleep(5 * time.Second)
			continue
		}

		var body struct {
			OK     bool       `json:"ok"`
			Result []tgUpdate `json:"result"`
		}
		json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()

		for _, u := range body.Result {
			offset = u.UpdateID + 1

			// Handle callback queries (inline keyboard button presses).
			if u.CallbackQuery != nil {
				if u.CallbackQuery.Message != nil && u.CallbackQuery.Message.Chat.ID == b.chatID {
					b.handleCallback(ctx, u.CallbackQuery)
				}
				continue
			}

			if u.Message == nil {
				continue
			}
			if u.Message.Chat.ID != b.chatID {
				continue
			}
			b.handleMessage(ctx, u.Message)
		}
	}
}

func (b *Bot) handleMessage(ctx context.Context, msg *tgMessage) {
	// Lazy cleanup of expired pending estimates.
	b.cleanupPendingEstimates()

	text := strings.TrimSpace(msg.Text)

	// Handle file/photo attachments.
	var attachedFiles []uploadedFileInfo
	if msg.Document != nil {
		file, err := b.handleFileUpload(msg.Document.FileID, msg.Document.FileName)
		if err != nil {
			b.rt.LogError("telegram file upload error", err)
		} else {
			attachedFiles = append(attachedFiles, file)
		}
	}
	if len(msg.Photo) > 0 {
		// Use the largest photo (last element has highest resolution).
		largest := msg.Photo[len(msg.Photo)-1]
		file, err := b.handleFileUpload(largest.FileID, "photo.jpg")
		if err != nil {
			b.rt.LogError("telegram photo upload error", err)
		} else {
			attachedFiles = append(attachedFiles, file)
		}
	}

	// If files attached, prepend file info to text/caption and route.
	if len(attachedFiles) > 0 {
		prefix := buildFilePromptPrefix(attachedFiles)
		text = prefix + coalesce(msg.Caption, text, "Analyze the attached file(s)")

		// Log file upload.
		for _, f := range attachedFiles {
			b.rt.LogInfo("telegram file received", "name", f.name, "mime", f.mimeType, "bytes", f.size)
		}

		// If no command, route as a prompt with file context.
		if !strings.HasPrefix(strings.TrimSpace(coalesce(msg.Caption, msg.Text)), "/") {
			if b.rt.SmartDispatchEnabled() {
				b.cmdRoute(ctx, msg, text)
			} else {
				b.cmdAsk(ctx, msg, text)
			}
			return
		}
	}

	cmd := strings.SplitN(text, " ", 2)
	command := cmd[0]
	args := ""
	if len(cmd) > 1 {
		args = strings.TrimSpace(cmd[1])
	}

	switch {
	case command == "/dispatch":
		b.cmdDispatch(ctx, msg, args)
	case command == "/status":
		b.cmdStatus(msg)
	case command == "/cancel":
		b.cmdCancel(msg)
	case command == "/cron" || command == "/jobs":
		b.cmdCron(ctx, msg, args)
	case command == "/cost":
		b.cmdCost(msg)
	case command == "/approve":
		b.cmdApprove(msg, args)
	case command == "/reject":
		b.cmdReject(msg, args)
	case command == "/tasks":
		b.cmdTasks(msg)
	case command == "/health":
		b.cmdHealth(ctx, msg)
	case command == "/memory":
		b.cmdMemory(msg, args)
	case command == "/ask":
		b.cmdAsk(ctx, msg, args)
	case command == "/route":
		b.cmdRoute(ctx, msg, args)
	case command == "/new":
		b.cmdNew(msg, args)
	case command == "/trust":
		b.cmdTrust(msg)
	case command == "/model":
		b.cmdModel(msg, args)
	case command == "/help":
		b.cmdHelp(msg)
	default:
		// Smart dispatch: route non-command messages if enabled.
		if b.rt.SmartDispatchEnabled() && !strings.HasPrefix(text, "/") && text != "" {
			// Strip @botname mentions before routing.
			cleaned := stripBotMention(text)
			if cleaned != "" {
				b.cmdRoute(ctx, msg, cleaned)
			}
		}
	}
}

// stripBotMention removes @username mentions from message text.
// Handles both @somethingbot style mentions and any leading @mention.
func stripBotMention(text string) string {
	// Remove @username mentions where username ends with "bot" (case-insensitive).
	re := regexp.MustCompile(`(?i)@\w+bot\b`)
	text = re.ReplaceAllString(text, "")
	// Also strip any leading @mention even without "bot" suffix.
	text = regexp.MustCompile(`^@\w+\s*`).ReplaceAllString(text, "")
	return strings.TrimSpace(text)
}

// --- /dispatch ---

// DispatchTaskJSON is the JSON payload for /dispatch.
type DispatchTaskJSON struct {
	Name   string `json:"name"`
	Prompt string `json:"prompt"`
	Model  string `json:"model"`
	Agent  string `json:"agent"`
	MCP    string `json:"mcp"`
}

func (b *Bot) cmdDispatch(ctx context.Context, msg *tgMessage, payload string) {
	if payload == "" {
		b.reply(msg.Chat.ID, "Usage: /dispatch [{\"name\":\"...\",\"prompt\":\"...\"}]")
		return
	}
	var rawTasks []DispatchTaskJSON
	if err := json.Unmarshal([]byte(payload), &rawTasks); err != nil {
		b.reply(msg.Chat.ID, fmt.Sprintf("JSON parse error: %v", err))
		return
	}
	if len(rawTasks) == 0 {
		b.reply(msg.Chat.ID, "No tasks provided.")
		return
	}

	if b.rt.DispatchActive() {
		b.reply(msg.Chat.ID, "Already dispatching. Use /status or /cancel first.")
		return
	}

	tasks := make([]DispatchTask, len(rawTasks))
	for i, rt := range rawTasks {
		tasks[i] = DispatchTask{
			Name:   rt.Name,
			Prompt: rt.Prompt,
			Model:  rt.Model,
			Agent:  rt.Agent,
			MCP:    rt.MCP,
			Source: "dispatch",
		}
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("Dispatch %d tasks (max concurrent: %d)\n", len(tasks), b.rt.MaxConcurrent()))
	for i, t := range tasks {
		extra := ""
		if t.MCP != "" {
			extra = " + " + t.MCP
		}
		lines = append(lines, fmt.Sprintf("%d. %s (%s%s)", i+1, t.Name, t.Model, extra))
	}
	b.reply(msg.Chat.ID, strings.Join(lines, "\n"))

	go func() {
		dispatchCtx := b.rt.WithTraceID(context.Background(), trace.NewID("tg"))
		result := b.rt.Dispatch(dispatchCtx, tasks)
		if result != nil {
			b.reply(msg.Chat.ID, formatDispatchResult(result))
		}
	}()
}

// --- /status ---

func (b *Bot) cmdStatus(msg *tgMessage) {
	if !b.rt.DispatchActive() {
		// Show cron status instead if no dispatch is active.
		if b.rt.CronAvailable() {
			jobs := b.rt.CronListJobs()
			running := 0
			for _, j := range jobs {
				if j.Running {
					running++
				}
			}
			b.reply(msg.Chat.ID, fmt.Sprintf("No active dispatch.\nCron: %d jobs (%d running)", len(jobs), running))
		} else {
			b.reply(msg.Chat.ID, "No active dispatch.")
		}
		return
	}
	b.reply(msg.Chat.ID, b.rt.DispatchStatus())
}

// --- /cancel ---

func (b *Bot) cmdCancel(msg *tgMessage) {
	if !b.rt.DispatchActive() {
		b.reply(msg.Chat.ID, "Nothing to cancel.")
		return
	}
	b.rt.CancelDispatch()
	b.reply(msg.Chat.ID, "Cancelling all running tasks...")
}

// --- /cron (alias: /jobs) ---

func (b *Bot) cmdCron(ctx context.Context, msg *tgMessage, args string) {
	if !b.rt.CronAvailable() {
		b.reply(msg.Chat.ID, "Cron engine not available.")
		return
	}

	parts := strings.Fields(args)

	// /cron enable/disable <id>
	if len(parts) >= 2 && (parts[0] == "enable" || parts[0] == "disable") {
		enabled := parts[0] == "enable"
		id := parts[1]
		if err := b.rt.CronToggleJob(id, enabled); err != nil {
			b.reply(msg.Chat.ID, fmt.Sprintf("Error: %v", err))
		} else {
			b.reply(msg.Chat.ID, fmt.Sprintf("Job %q %sd.", id, parts[0]))
		}
		return
	}

	// /cron run <id>
	if len(parts) >= 2 && parts[0] == "run" {
		id := parts[1]
		if err := b.rt.CronRunJob(ctx, id); err != nil {
			b.reply(msg.Chat.ID, fmt.Sprintf("Error: %v", err))
		} else {
			b.reply(msg.Chat.ID, fmt.Sprintf("Job %q triggered.", id))
		}
		return
	}

	// /cron (list) — with inline keyboard
	jobs := b.rt.CronListJobs()
	if len(jobs) == 0 {
		b.reply(msg.Chat.ID, "No cron jobs configured.")
		return
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("Cron Jobs (%d total)\n", len(jobs)))
	for _, j := range jobs {
		icon := "[ ]"
		if j.Running {
			icon = "[>]"
		} else if j.Enabled {
			icon = "[*]"
		}

		nextStr := ""
		if j.NextRun != nil {
			if t, ok := j.NextRun.(time.Time); ok && !t.IsZero() && j.Enabled {
				nextStr = fmt.Sprintf(" next: %s", t.Format("15:04"))
			}
		}

		errStr := ""
		if j.Errors > 0 {
			errStr = fmt.Sprintf(" (err x%d)", j.Errors)
		}

		avgStr := ""
		if j.AvgCost > 0 {
			avgStr = fmt.Sprintf(" avg:$%.2f", j.AvgCost)
		}

		lines = append(lines, fmt.Sprintf("%s %s [%s]%s%s%s",
			icon, j.Name, j.Schedule, nextStr, avgStr, errStr))
	}

	// Build inline keyboard: each job gets a row with Run / Toggle buttons.
	var keyboard [][]InlineButton
	for _, j := range jobs {
		var row []InlineButton
		if !j.Running {
			row = append(row, InlineButton{Text: "Run " + j.Name, CallbackData: "run:" + j.ID})
		}
		if j.Enabled {
			row = append(row, InlineButton{Text: "Disable", CallbackData: "disable:" + j.ID})
		} else {
			row = append(row, InlineButton{Text: "Enable", CallbackData: "enable:" + j.ID})
		}
		if len(row) > 0 {
			keyboard = append(keyboard, row)
		}
	}

	b.replyWithKeyboard(msg.Chat.ID, strings.Join(lines, "\n"), keyboard)
}

// --- /cost ---

func (b *Bot) cmdCost(msg *tgMessage) {
	if b.rt.HistoryDB() == "" {
		b.reply(msg.Chat.ID, "History DB not configured.")
		return
	}

	today, week, month := b.rt.GetCostStats()

	text := fmt.Sprintf("Cost Summary\n\nToday: $%.2f\nThis Week: $%.2f\nThis Month: $%.2f",
		today, week, month)

	// Budget status is queried via rt — we don't need to know limit values here,
	// the runtime reports them through QueryCostStats which already incorporates limits.
	// For the full budget breakdown we just show raw numbers.

	// Per-job cost breakdown.
	costByJob := b.rt.GetCostByJob()
	if len(costByJob) > 0 {
		text += "\n\nPer Job (30d):"
		for id, cost := range costByJob {
			text += fmt.Sprintf("\n  %s: $%.2f", id, cost)
		}
	}

	b.reply(msg.Chat.ID, text)
}

// --- /tasks ---

func (b *Bot) cmdTasks(msg *tgMessage) {
	if b.rt.HistoryDB() == "" {
		b.reply(msg.Chat.ID, "History DB not configured.")
		return
	}

	stats, err := b.rt.GetTaskStats()
	if err != nil {
		b.reply(msg.Chat.ID, fmt.Sprintf("DB error: %v", err))
		return
	}

	text := fmt.Sprintf("Dashboard Tasks\n\n"+
		"Todo: %d\nRunning: %d\nReview: %d\nDone: %d\nFailed: %d\nTotal: %d",
		stats.Todo, stats.Running, stats.Review, stats.Done, stats.Failed, stats.Total)

	// Show stuck tasks if any.
	stuck := b.rt.GetStuckTasks(30)
	if len(stuck) > 0 {
		text += fmt.Sprintf("\n\nStuck (>30min): %d", len(stuck))
		for _, t := range stuck {
			text += fmt.Sprintf("\n  - %s (%s)", t.Title, t.CreatedAt)
		}
	}

	b.reply(msg.Chat.ID, text)
}

// --- /health ---

func (b *Bot) cmdHealth(ctx context.Context, msg *tgMessage) {
	if !b.rt.CronAvailable() {
		b.reply(msg.Chat.ID, "Cron engine not available.")
		return
	}

	err := b.rt.CronRunJob(ctx, "heartbeat")
	if err != nil {
		b.reply(msg.Chat.ID, fmt.Sprintf("Error triggering heartbeat: %v\nYou can also check: curl http://127.0.0.1:8991/healthz", err))
		return
	}
	b.reply(msg.Chat.ID, "Heartbeat triggered. Results will be sent when complete.")
}

// --- /memory ---

func (b *Bot) cmdMemory(msg *tgMessage, keyword string) {
	if keyword == "" {
		b.reply(msg.Chat.ID, "Usage: /memory <keyword>")
		return
	}

	results := b.rt.SearchMemory(keyword)
	if results == "" {
		b.reply(msg.Chat.ID, fmt.Sprintf("No results for %q in memory.", keyword))
		return
	}
	b.reply(msg.Chat.ID, truncate(results, 2000))
}

// --- /ask (with channel session sync) ---

func (b *Bot) cmdAsk(ctx context.Context, msg *tgMessage, prompt string) {
	if prompt == "" {
		b.reply(msg.Chat.ID, "Usage: /ask <prompt>")
		return
	}

	// Cost confirmation check.
	if b.maybeCostConfirm(msg.Chat.ID, prompt, false) {
		return
	}

	b.execAsk(ctx, msg, prompt)
}

// execAsk runs the /ask flow without cost confirmation (used after confirmation or below threshold).
func (b *Bot) execAsk(ctx context.Context, msg *tgMessage, prompt string) {
	b.sendTypingAction(msg.Chat.ID)

	go func() {
		chKey := b.rt.ChannelSessionKey("tg", "ask")

		// Find or create channel session.
		sess, err := b.rt.GetOrCreateChannelSession("telegram", chKey, "", "Quick Ask")
		if err != nil {
			b.rt.LogError("telegram ask session error", err)
		}

		// Build context from previous messages.
		var sessionID string
		var sessionMsgCount int
		var sessionTotalTokensIn float64
		contextPrompt := prompt
		if sess != nil {
			sessionID = sess.ID
			sessionMsgCount = sess.MessageCount
			sessionTotalTokensIn = sess.TotalTokensIn

			if !b.rt.ProviderHasNativeSession("") {
				sessionCtx := b.rt.BuildSessionContext(sessionID, b.rt.SessionContextLimit())
				contextPrompt = b.rt.WrapWithContext(sessionCtx, prompt)
			}

			// Record user message.
			b.rt.AddSessionMessage(sessionID, "user", messaging.TruncateStr(prompt, 5000))
			b.rt.UpdateSessionStats(sessionID, 0, 0, 0, 1)
		}

		result := b.rt.RunAsk(ctx, contextPrompt, sessionID, "")

		// Record assistant response to session.
		if sessionID != "" {
			b.rt.RecordAndCompact(sessionID, sessionMsgCount+2, sessionTotalTokensIn+result.TokensIn,
				"", "", &result)
		}

		if result.Status == "success" {
			output := truncate(result.Output, 3000)
			// --- P18.1: Cost footer ---
			if footer := b.rt.FormatResultCostFooter(&result); footer != "" {
				output = output + "\n\n" + footer
			}
			b.reply(msg.Chat.ID, output)
		} else {
			b.reply(msg.Chat.ID, fmt.Sprintf("Error: %s", truncate(result.Error, 500)))
		}
	}()
}

// --- /route (smart dispatch with channel session sync) ---

func (b *Bot) cmdRoute(ctx context.Context, msg *tgMessage, prompt string) {
	if prompt == "" {
		b.reply(msg.Chat.ID, "Usage: /route <task description>")
		return
	}

	if !b.rt.SmartDispatchEnabled() {
		b.reply(msg.Chat.ID, "Smart dispatch is not enabled.\nSet smartDispatch.enabled=true in config.")
		return
	}

	// Cost confirmation check.
	if b.maybeCostConfirm(msg.Chat.ID, prompt, true) {
		return
	}

	b.execRoute(ctx, msg, prompt)
}

// execRoute runs the /route flow without cost confirmation (used after confirmation or below threshold).
func (b *Bot) execRoute(ctx context.Context, msg *tgMessage, prompt string) {
	b.sendTypingAction(msg.Chat.ID)

	go func() {
		// Step 1: Route + get session + run task via runtime.
		// The runtime handles routing, session, context, and task execution.
		chKeyFn := func(agent string) string {
			return b.rt.ChannelSessionKey("tg", agent)
		}

		// Route first to get agent name for session key.
		traceID := b.rt.NewTraceID("tg")
		routeCtx := b.rt.WithTraceID(ctx, traceID)

		// Step 1: Route to determine agent.
		routedAgent, err := b.rt.Route(routeCtx, prompt, "telegram")
		if err != nil {
			b.rt.LogErrorCtx(routeCtx, "telegram: route error", err)
		}

		// Step 2: Find or create channel session for this agent.
		chKey := chKeyFn(routedAgent)
		sess, err := b.rt.GetOrCreateChannelSession("telegram", chKey, routedAgent, "")
		if err != nil {
			b.rt.LogError("telegram route session error", err)
		}

		// Step 3: Build context-aware prompt.
		var sessionID string
		var sessionMsgCount int
		var sessionTotalTokensIn float64
		contextPrompt := prompt
		if sess != nil {
			sessionID = sess.ID
			sessionMsgCount = sess.MessageCount
			sessionTotalTokensIn = sess.TotalTokensIn

			if !b.rt.ProviderHasNativeSession(routedAgent) {
				sessionCtx := b.rt.BuildSessionContext(sessionID, b.rt.SessionContextLimit())
				contextPrompt = b.rt.WrapWithContext(sessionCtx, prompt)
			}

			// Record user message to session.
			b.rt.AddSessionMessage(sessionID, "user", messaging.TruncateStr(prompt, 5000))
			b.rt.UpdateSessionStats(sessionID, 0, 0, 0, 1)

			// Update title from first real message.
			title := prompt
			if len(title) > 100 {
				title = title[:100]
			}
			b.rt.UpdateSessionTitle(sessionID, title)
		}

		// P27.3: Progress message with streaming output.
		var progressMsgID int
		var progressStopCh chan struct{}
		var outputAlreadySent bool
		if b.rt.StreamToChannels() && b.rt.SSEBrokerAvailable() {
			msgID, err := b.replyReturningID(msg.Chat.ID, "Working...")
			if err == nil && msgID != 0 {
				progressMsgID = msgID
				progressStopCh = make(chan struct{})
				tgBuilder := newTGProgressBuilder()
				// We need a task ID to subscribe — get it from the runtime via a dedicated field.
				// For now we'll start the progress updater after we have the task ID.
				// We'll use a deferred start via a channel.
				_ = tgBuilder // will be used below after task ID is known
			}
		}

		// Step 4: Run task via RouteAndRun (runtime handles routing details, agent config, expansion).
		sdr := b.rt.RouteAndRun(routeCtx, prompt, "route:telegram", sessionID, contextPrompt)

		if sdr == nil {
			b.reply(msg.Chat.ID, "Internal error: no result from route.")
			return
		}

		// Stop progress updater.
		if progressStopCh != nil {
			close(progressStopCh)
		}

		// Step 5: Record assistant response to session.
		if sessionID != "" {
			b.rt.RecordAndCompact(sessionID, sessionMsgCount+2, sessionTotalTokensIn+sdr.Task.TokensIn,
				"", "", &sdr.Task)
		}

		// Store output summary in agent memory.
		if sdr.Task.Status == "success" && sdr.Route.Agent != "" {
			b.rt.SetMemory(sdr.Route.Agent, "last_route_output", truncate(sdr.Task.Output, 500))
			b.rt.SetMemory(sdr.Route.Agent, "last_route_prompt", truncate(prompt, 200))
			b.rt.SetMemory(sdr.Route.Agent, "last_route_time", time.Now().Format(time.RFC3339))
		}

		// Webhook notifications.
		b.rt.SendWebhooks(sdr.Task.Status, map[string]interface{}{
			"task_id":     sdr.Task.TaskID,
			"source":      "route:telegram",
			"cost_usd":    sdr.Task.CostUSD,
			"duration_ms": sdr.Task.DurationMs,
			"model":       sdr.Task.Model,
			"output":      truncate(sdr.Task.Output, 500),
			"error":       truncate(sdr.Task.Error, 300),
		})

		// Handle progress message.
		if progressMsgID != 0 {
			if sdr.Task.Status != "success" {
				errMsg := sdr.Task.Error
				if errMsg == "" {
					errMsg = sdr.Task.Status
				}
				b.editMessageText(msg.Chat.ID, progressMsgID, fmt.Sprintf("Error: %s", errMsg))
			} else {
				output := sdr.Task.Output
				if strings.TrimSpace(output) == "" {
					output = "Task completed successfully."
				}
				if len(output) <= 3800 {
					b.editMessageText(msg.Chat.ID, progressMsgID, output)
					outputAlreadySent = true
				} else {
					b.tgDeleteMessage(msg.Chat.ID, progressMsgID)
				}
			}
		}

		// Suggest mode: hold output for human approval.
		if TrustLevel(b.rt.GetTrustLevel(sdr.Route.Agent)) == TrustSuggest && sdr.Task.Status == "success" {
			b.sendSuggestConfirm(msg.Chat.ID, sdr.Task, sdr.Route.Agent, prompt)
			return
		}

		// Send slot pressure warning before response if present.
		// (SlotWarning not in messaging.TaskResult, skip)

		// Format and send response.
		b.sendRouteResponse(msg.Chat.ID, sdr, outputAlreadySent)
	}()
}

// sendRouteResponse formats and sends a smart dispatch result to Telegram.
// When skipOutput is true, the main output text is omitted (already sent via progress edit).
func (b *Bot) sendRouteResponse(chatID int64, result *SmartDispatchResult, skipOutput bool) {
	var lines []string

	lines = append(lines, fmt.Sprintf("\xf0\x9f\x8e\xaf Route \xe2\x86\x92 %s (%s, %s confidence)",
		result.Route.Agent, result.Route.Method, result.Route.Confidence))

	if !skipOutput {
		if result.Task.Status == "success" {
			lines = append(lines, "")
			lines = append(lines, truncate(result.Task.Output, 3000))
		} else {
			lines = append(lines, fmt.Sprintf("\n\xe2\x9d\x8c [%s] %s",
				result.Task.Status, truncate(result.Task.Error, 500)))
		}
	}

	if result.ReviewOK != nil {
		if *result.ReviewOK {
			lines = append(lines, "\n\xe2\x9c\x85 Review: PASS")
		} else {
			lines = append(lines, "\n\xe2\x9a\xa0\xef\xb8\x8f Review: NEEDS REVIEW")
		}
		if result.Review != "" {
			lines = append(lines, result.Review)
		}
	}

	dur := time.Duration(result.Task.DurationMs) * time.Millisecond
	durStr := formatDuration(dur)
	lines = append(lines, fmt.Sprintf("\n\xf0\x9f\x93\x8a $%.2f | %s", result.Task.CostUSD, durStr))

	responseText := strings.Join(lines, "\n")

	if result.Task.Status != "success" {
		keyboard := [][]InlineButton{
			{
				{Text: "\xf0\x9f\x94\x84 Retry", CallbackData: "retry:" + result.Task.TaskID},
				{Text: "\xf0\x9f\x94\x80 Reroute", CallbackData: "reroute:" + result.Task.TaskID},
			},
		}
		b.replyWithKeyboard(chatID, responseText, keyboard)
	} else {
		b.reply(chatID, responseText)
	}
}

// --- /help ---

func (b *Bot) cmdModel(msg *tgMessage, args string) {
	parts := strings.Fields(args)

	// /model → show current models
	if len(parts) == 0 {
		models := b.rt.AgentModels()
		var lines []string
		for name, m := range models {
			lines = append(lines, fmt.Sprintf("  %s: `%s`", name, m))
		}
		b.reply(msg.Chat.ID, "Current models:\n"+strings.Join(lines, "\n"))
		return
	}

	// /model <model> [agent]
	model := parts[0]
	agentName := b.rt.DefaultSmartDispatchAgent()
	if agentName == "" {
		agentName = "default"
	}
	if len(parts) > 1 {
		agentName = parts[1]
	}

	old, err := b.rt.UpdateAgentModelByName(agentName, model)
	if err != nil {
		b.reply(msg.Chat.ID, fmt.Sprintf("Error: %v", err))
		return
	}
	b.reply(msg.Chat.ID, fmt.Sprintf("*%s* model: `%s` → `%s`", agentName, old, model))
}

func (b *Bot) cmdHelp(msg *tgMessage) {
	b.reply(msg.Chat.ID, "Tetora - AI Agent Orchestrator\n\n"+
		"/dispatch [tasks JSON] - parallel task dispatch\n"+
		"/route <task> - smart dispatch (auto-route to best agent)\n"+
		"/ask <prompt> - quick question (no agent)\n"+
		"/new [agent] - start fresh session (archives current)\n"+
		"/status - check running tasks\n"+
		"/cancel - cancel all running tasks\n"+
		"/jobs - list cron jobs (with buttons)\n"+
		"/cron enable/disable <id> - toggle job\n"+
		"/cron run <id> - trigger job now\n"+
		"/cost - cost summary\n"+
		"/approve <id> - approve pending job\n"+
		"/reject <id> - reject pending job\n"+
		"/tasks - dashboard task stats\n"+
		"/health - trigger heartbeat\n"+
		"/trust - show trust levels for all agents\n"+
		"/model [model] [agent] - show/switch model\n"+
		"/memory <keyword> - search memory files\n"+
		"/help - this message\n\n"+
		"Messages are linked to persistent sessions per agent.\n"+
		"Conversation history is automatically maintained.\n"+
		"Use /new to start a fresh conversation.\n\n"+
		fmt.Sprintf("Cost confirmation: tasks estimated above $%.2f will ask for confirmation before executing.\n\n",
			b.rt.EstimateThreshold())+
		"You can also send files/photos directly - they will be saved and analyzed by the agent.")
}

// --- /trust ---

func (b *Bot) cmdTrust(msg *tgMessage) {
	statuses := b.rt.GetAllTrustStatuses()
	if len(statuses) == 0 {
		b.reply(msg.Chat.ID, "No agents configured.")
		return
	}

	var lines []string
	lines = append(lines, "Trust Levels\n")
	for _, s := range statuses {
		icon := ""
		switch s.Level {
		case TrustObserve:
			icon = "[O]"
		case TrustSuggest:
			icon = "[S]"
		case TrustAuto:
			icon = "[A]"
		}
		line := fmt.Sprintf("%s %s: %s", icon, s.Agent, s.Level)
		if s.ConsecutiveSuccess > 0 {
			line += fmt.Sprintf(" (streak: %d)", s.ConsecutiveSuccess)
		}
		if s.PromoteReady {
			line += fmt.Sprintf(" -> %s ready", s.NextLevel)
		}
		lines = append(lines, line)
	}
	b.reply(msg.Chat.ID, strings.Join(lines, "\n"))
}

// --- Callback Query Handler ---

func (b *Bot) handleCallback(ctx context.Context, cq *tgCallbackQuery) {
	parts := strings.SplitN(cq.Data, ":", 2)
	if len(parts) != 2 {
		b.answerCallback(cq.ID, "Unknown action")
		return
	}
	action, id := parts[0], parts[1]

	// Handle retry/reroute actions.
	switch action {
	case "retry":
		b.answerCallback(cq.ID, "Retrying...")
		go func() {
			result, err := b.rt.RetryTask(ctx, id)
			if err != nil {
				b.reply(cq.Message.Chat.ID, fmt.Sprintf("\xe2\x9d\x8c Retry failed: %s", err.Error()))
				return
			}
			if result.Status == "success" {
				b.reply(cq.Message.Chat.ID, fmt.Sprintf("\xe2\x9c\x85 Retry succeeded [%s]\n\n%s",
					result.Name, truncate(result.Output, 3000)))
			} else {
				text := fmt.Sprintf("\xe2\x9d\x8c Retry failed [%s]\n%s",
					result.Name, truncate(result.Error, 500))
				keyboard := [][]InlineButton{
					{
						{Text: "\xf0\x9f\x94\x84 Retry", CallbackData: "retry:" + result.TaskID},
						{Text: "\xf0\x9f\x94\x80 Reroute", CallbackData: "reroute:" + result.TaskID},
					},
				}
				b.replyWithKeyboard(cq.Message.Chat.ID, text, keyboard)
			}
		}()
		return
	case "reroute":
		b.answerCallback(cq.ID, "Rerouting...")
		go func() {
			result, err := b.rt.RerouteTask(ctx, id)
			if err != nil {
				b.reply(cq.Message.Chat.ID, fmt.Sprintf("\xe2\x9d\x8c Reroute failed: %s", err.Error()))
				return
			}
			var lines []string
			lines = append(lines, fmt.Sprintf("\xf0\x9f\x94\x80 Rerouted \xe2\x86\x92 %s (%s)",
				result.Route.Agent, result.Route.Method))
			if result.Task.Status == "success" {
				lines = append(lines, "")
				lines = append(lines, truncate(result.Task.Output, 3000))
			} else {
				lines = append(lines, fmt.Sprintf("\n\xe2\x9d\x8c [%s] %s",
					result.Task.Status, truncate(result.Task.Error, 500)))
			}
			dur := time.Duration(result.Task.DurationMs) * time.Millisecond
			lines = append(lines, fmt.Sprintf("\n\xf0\x9f\x93\x8a $%.2f | %s", result.Task.CostUSD, formatDuration(dur)))

			responseText := strings.Join(lines, "\n")
			if result.Task.Status != "success" {
				keyboard := [][]InlineButton{
					{
						{Text: "\xf0\x9f\x94\x84 Retry", CallbackData: "retry:" + result.Task.TaskID},
						{Text: "\xf0\x9f\x94\x80 Reroute", CallbackData: "reroute:" + result.Task.TaskID},
					},
				}
				b.replyWithKeyboard(cq.Message.Chat.ID, responseText, keyboard)
			} else {
				b.reply(cq.Message.Chat.ID, responseText)
			}
		}()
		return

	case "confirm_dispatch":
		b.pendingMu.Lock()
		pe, ok := b.pendingEstimates[id]
		if ok {
			delete(b.pendingEstimates, id)
		}
		b.pendingMu.Unlock()

		if !ok || time.Since(pe.createdAt) > pendingEstimateTTL {
			b.answerCallback(cq.ID, "Expired or not found")
			return
		}

		b.answerCallback(cq.ID, "Executing...")
		msg := &tgMessage{Chat: tgChat{ID: pe.chatID}}
		if pe.isRoute {
			b.execRoute(ctx, msg, pe.prompt)
		} else {
			b.execAsk(ctx, msg, pe.prompt)
		}
		return

	case "cancel_dispatch":
		b.pendingMu.Lock()
		delete(b.pendingEstimates, id)
		b.pendingMu.Unlock()
		b.answerCallback(cq.ID, "Cancelled")
		b.reply(cq.Message.Chat.ID, "Dispatch cancelled.")
		return

	case "trust_approve":
		b.pendingMu.Lock()
		ps, ok := b.pendingSuggests[id]
		if ok {
			delete(b.pendingSuggests, id)
		}
		b.pendingMu.Unlock()

		if !ok || time.Since(ps.createdAt) > pendingSuggestTTL {
			b.answerCallback(cq.ID, "Expired or not found")
			return
		}

		b.answerCallback(cq.ID, "Approved")
		b.reply(cq.Message.Chat.ID, fmt.Sprintf("Approved [%s]\n\n%s",
			ps.role, truncate(ps.result.Output, 3000)))
		return

	case "trust_reject":
		b.pendingMu.Lock()
		ps, ok := b.pendingSuggests[id]
		if ok {
			delete(b.pendingSuggests, id)
		}
		b.pendingMu.Unlock()

		if !ok || time.Since(ps.createdAt) > pendingSuggestTTL {
			b.answerCallback(cq.ID, "Expired or not found")
			return
		}

		b.answerCallback(cq.ID, "Rejected")
		b.reply(cq.Message.Chat.ID, fmt.Sprintf("Rejected [%s] output. No action taken.", ps.role))
		return

	// P28.0: Approval gate callbacks.
	case "gate_approve":
		b.answerCallback(cq.ID, "Approved")
		if b.approvalGate != nil {
			b.approvalGate.handleGateCallback(id, true)
		}
		return
	case "gate_always":
		// id contains "reqID:toolName"
		alwaysParts := strings.SplitN(id, ":", 2)
		if len(alwaysParts) == 2 && b.approvalGate != nil {
			b.approvalGate.AutoApprove(alwaysParts[1])
			b.approvalGate.handleGateCallback(alwaysParts[0], true)
			b.answerCallback(cq.ID, "Always approved: "+alwaysParts[1])
		} else {
			b.answerCallback(cq.ID, "Approved")
		}
		return
	case "gate_reject":
		b.answerCallback(cq.ID, "Rejected")
		if b.approvalGate != nil {
			b.approvalGate.handleGateCallback(id, false)
		}
		return
	}

	// Cron-related actions require cron engine.
	if !b.rt.CronAvailable() {
		b.answerCallback(cq.ID, "Cron not available")
		return
	}

	switch action {
	case "run":
		if err := b.rt.CronRunJob(ctx, id); err != nil {
			b.answerCallback(cq.ID, "Error: "+err.Error())
		} else {
			b.answerCallback(cq.ID, id+" triggered")
			b.reply(cq.Message.Chat.ID, fmt.Sprintf("Job %q triggered.", id))
		}
	case "enable":
		if err := b.rt.CronToggleJob(id, true); err != nil {
			b.answerCallback(cq.ID, "Error: "+err.Error())
		} else {
			b.answerCallback(cq.ID, id+" enabled")
			b.reply(cq.Message.Chat.ID, fmt.Sprintf("Job %q enabled.", id))
		}
	case "disable":
		if err := b.rt.CronToggleJob(id, false); err != nil {
			b.answerCallback(cq.ID, "Error: "+err.Error())
		} else {
			b.answerCallback(cq.ID, id+" disabled")
			b.reply(cq.Message.Chat.ID, fmt.Sprintf("Job %q disabled.", id))
		}
	case "approve":
		if err := b.rt.CronApproveJob(id); err != nil {
			b.answerCallback(cq.ID, "Error: "+err.Error())
		} else {
			b.answerCallback(cq.ID, id+" approved")
			b.reply(cq.Message.Chat.ID, fmt.Sprintf("Job %q approved. Running now.", id))
		}
	case "reject":
		if err := b.rt.CronRejectJob(id); err != nil {
			b.answerCallback(cq.ID, "Error: "+err.Error())
		} else {
			b.answerCallback(cq.ID, id+" rejected")
			b.reply(cq.Message.Chat.ID, fmt.Sprintf("Job %q rejected.", id))
		}
	default:
		b.answerCallback(cq.ID, "Unknown action: "+action)
	}
}

// --- /approve and /reject commands ---

func (b *Bot) cmdApprove(msg *tgMessage, args string) {
	if !b.rt.CronAvailable() {
		b.reply(msg.Chat.ID, "Cron engine not available.")
		return
	}
	id := strings.TrimSpace(args)
	if id == "" {
		b.reply(msg.Chat.ID, "Usage: /approve <job-id>")
		return
	}
	if err := b.rt.CronApproveJob(id); err != nil {
		b.reply(msg.Chat.ID, fmt.Sprintf("Error: %v", err))
	} else {
		b.reply(msg.Chat.ID, fmt.Sprintf("Job %q approved. Running now.", id))
	}
}

func (b *Bot) cmdReject(msg *tgMessage, args string) {
	if !b.rt.CronAvailable() {
		b.reply(msg.Chat.ID, "Cron engine not available.")
		return
	}
	id := strings.TrimSpace(args)
	if id == "" {
		b.reply(msg.Chat.ID, "Usage: /reject <job-id>")
		return
	}
	if err := b.rt.CronRejectJob(id); err != nil {
		b.reply(msg.Chat.ID, fmt.Sprintf("Error: %v", err))
	} else {
		b.reply(msg.Chat.ID, fmt.Sprintf("Job %q rejected.", id))
	}
}

// --- File Download ---

// uploadedFileInfo holds info about an uploaded file.
type uploadedFileInfo struct {
	name     string
	path     string
	mimeType string
	size     int64
}

// handleFileUpload downloads a Telegram file and saves it to the uploads directory.
func (b *Bot) handleFileUpload(fileID, fileName string) (uploadedFileInfo, error) {
	// Step 1: Get file path from Telegram.
	getURL := fmt.Sprintf("https://api.telegram.org/bot%s/getFile?file_id=%s", b.token, fileID)
	resp, err := http.Get(getURL)
	if err != nil {
		return uploadedFileInfo{}, fmt.Errorf("getFile request: %w", err)
	}
	defer resp.Body.Close()

	var fileResult struct {
		OK     bool `json:"ok"`
		Result struct {
			FilePath string `json:"file_path"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&fileResult); err != nil {
		return uploadedFileInfo{}, fmt.Errorf("decode getFile response: %w", err)
	}
	if !fileResult.OK || fileResult.Result.FilePath == "" {
		return uploadedFileInfo{}, fmt.Errorf("telegram getFile failed for file_id=%s", fileID)
	}

	// Step 2: Download the file.
	downloadURL := fmt.Sprintf("https://api.telegram.org/file/bot%s/%s", b.token, fileResult.Result.FilePath)
	fileResp, err := http.Get(downloadURL)
	if err != nil {
		return uploadedFileInfo{}, fmt.Errorf("download file: %w", err)
	}
	defer fileResp.Body.Close()
	if fileResp.StatusCode != 200 {
		return uploadedFileInfo{}, fmt.Errorf("download file: status %d", fileResp.StatusCode)
	}

	resolvedName := fileName
	if resolvedName == "" {
		resolvedName = filepath.Base(fileResult.Result.FilePath)
	}

	data, err := io.ReadAll(fileResp.Body)
	if err != nil {
		return uploadedFileInfo{}, fmt.Errorf("read file body: %w", err)
	}

	path, err := b.rt.SaveUploadedFile(resolvedName, data, "telegram")
	if err != nil {
		return uploadedFileInfo{}, err
	}

	return uploadedFileInfo{
		name:     resolvedName,
		path:     path,
		mimeType: fileResp.Header.Get("Content-Type"),
		size:     int64(len(data)),
	}, nil
}

// buildFilePromptPrefix builds a text prefix describing uploaded files.
func buildFilePromptPrefix(files []uploadedFileInfo) string {
	var sb strings.Builder
	for _, f := range files {
		sb.WriteString(fmt.Sprintf("[File: %s | Path: %s | Size: %d bytes]\n", f.name, f.path, f.size))
	}
	return sb.String()
}

// --- /new (start fresh session) ---

func (b *Bot) cmdNew(msg *tgMessage, args string) {
	if b.rt.HistoryDB() == "" {
		b.reply(msg.Chat.ID, "History DB not configured.")
		return
	}

	role := strings.TrimSpace(args)
	if role == "" {
		// Archive all active Telegram channel sessions.
		agents := b.rt.AgentModels()
		archived := 0
		for agentName := range agents {
			chKey := b.rt.ChannelSessionKey("tg", agentName)
			if err := b.rt.ArchiveChannelSession(chKey); err == nil {
				archived++
			}
		}
		// Also archive the "ask" session.
		b.rt.ArchiveChannelSession(b.rt.ChannelSessionKey("tg", "ask"))
		b.reply(msg.Chat.ID, fmt.Sprintf("Archived %d channel sessions. Fresh start!", archived))
	} else {
		// Archive session for specific agent.
		agents := b.rt.AgentModels()
		if _, ok := agents[role]; !ok {
			b.reply(msg.Chat.ID, fmt.Sprintf("Unknown agent: %s", role))
			return
		}
		chKey := b.rt.ChannelSessionKey("tg", role)
		if err := b.rt.ArchiveChannelSession(chKey); err != nil {
			b.reply(msg.Chat.ID, fmt.Sprintf("Error: %v", err))
			return
		}
		b.reply(msg.Chat.ID, fmt.Sprintf("Archived session for %s. Next message starts a fresh conversation.", role))
	}
}

// --- Typing Indicator ---

// sendTypingAction sends a "typing" chat action to indicate the bot is processing.
func (b *Bot) sendTypingAction(chatID int64) {
	payload := map[string]any{
		"chat_id": chatID,
		"action":  "typing",
	}
	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendChatAction", b.token)
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return // best effort
	}
	resp.Body.Close()
}

// --- P27.3: Telegram Channel Notifier ---

// TgChannelNotifier implements ChannelNotifier for Telegram.
type TgChannelNotifier struct {
	Bot    *Bot
	ChatID int64
}

func (n *TgChannelNotifier) SendTyping(ctx context.Context) error {
	n.Bot.sendTypingAction(n.ChatID)
	return nil
}

func (n *TgChannelNotifier) SendStatus(ctx context.Context, msg string) error {
	// Just send typing — avoid spamming the channel with status text.
	n.Bot.sendTypingAction(n.ChatID)
	return nil
}

// --- P28.0: Telegram Approval Gate ---

// tgApprovalGate implements ApprovalGate via Telegram inline keyboards.
type tgApprovalGate struct {
	bot          *Bot
	chatID       int64
	mu           sync.Mutex
	pending      map[string]chan bool // requestID → response channel
	autoApproved map[string]bool     // tool name → always approved
}

func newTGApprovalGate(bot *Bot, chatID int64, autoApproveTools []string) *tgApprovalGate {
	g := &tgApprovalGate{
		bot:          bot,
		chatID:       chatID,
		pending:      make(map[string]chan bool),
		autoApproved: make(map[string]bool),
	}
	// Copy config-level auto-approve tools.
	for _, tool := range autoApproveTools {
		g.autoApproved[tool] = true
	}
	return g
}

func (g *tgApprovalGate) AutoApprove(toolName string) {
	g.mu.Lock()
	g.autoApproved[toolName] = true
	g.mu.Unlock()
}

func (g *tgApprovalGate) IsAutoApproved(toolName string) bool {
	g.mu.Lock()
	ok := g.autoApproved[toolName]
	g.mu.Unlock()
	return ok
}

func (g *tgApprovalGate) RequestApproval(ctx context.Context, req ApprovalRequest) (bool, error) {
	ch := make(chan bool, 1)
	g.mu.Lock()
	g.pending[req.ID] = ch
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		delete(g.pending, req.ID)
		g.mu.Unlock()
	}()

	// Send message with approve/always/reject buttons.
	text := fmt.Sprintf("Approval needed\n\nTool: %s\n%s", req.Tool, req.Summary)
	keyboard := [][]InlineButton{{
		{Text: "Approve", CallbackData: "gate_approve:" + req.ID},
		{Text: "Always", CallbackData: "gate_always:" + req.ID + ":" + req.Tool},
		{Text: "Reject", CallbackData: "gate_reject:" + req.ID},
	}}
	g.bot.replyWithKeyboard(g.chatID, text, keyboard)

	select {
	case approved := <-ch:
		return approved, nil
	case <-ctx.Done():
		return false, fmt.Errorf("approval timed out: %v", ctx.Err())
	}
}

// handleGateCallback processes gate_approve/gate_reject callbacks.
func (g *tgApprovalGate) handleGateCallback(reqID string, approved bool) {
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

// --- Telegram HTTP ---

// replyWithKeyboard sends a message with an inline keyboard.
func (b *Bot) replyWithKeyboard(chatID int64, text string, keyboard [][]InlineButton) {
	payload := map[string]any{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "Markdown",
	}
	if len(keyboard) > 0 {
		payload["reply_markup"] = tgInlineKeyboard{InlineKeyboard: keyboard}
	}
	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", b.token)
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		b.rt.LogError("telegram send error", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		// Retry without Markdown if parse_mode caused the error.
		if strings.Contains(string(respBody), "parse") {
			payload["parse_mode"] = ""
			body2, _ := json.Marshal(payload)
			http.Post(url, "application/json", bytes.NewReader(body2))
		} else {
			b.rt.LogWarn("telegram send non-200", "status", resp.StatusCode, "body", string(respBody))
		}
	}
}

// answerCallback acknowledges a callback query with a short toast message.
func (b *Bot) answerCallback(callbackQueryID, text string) {
	payload := map[string]any{
		"callback_query_id": callbackQueryID,
		"text":              text,
	}
	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("https://api.telegram.org/bot%s/answerCallbackQuery", b.token)
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		b.rt.LogError("telegram answerCallback error", err)
		return
	}
	resp.Body.Close()
}

func (b *Bot) reply(chatID int64, text string) {
	payload := map[string]any{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "Markdown",
	}
	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", b.token)
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		b.rt.LogError("telegram send error", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		// Retry without Markdown if parse_mode caused the error.
		if strings.Contains(string(respBody), "parse") {
			payload["parse_mode"] = ""
			body2, _ := json.Marshal(payload)
			http.Post(url, "application/json", bytes.NewReader(body2))
		} else {
			b.rt.LogWarn("telegram send non-200", "status", resp.StatusCode, "body", string(respBody))
		}
	}
}

// replyReturningID sends a message and returns the message ID.
func (b *Bot) replyReturningID(chatID int64, text string) (int, error) {
	if len(text) > 4096 {
		text = text[:4093] + "..."
	}
	payload := map[string]any{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "Markdown",
	}
	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", b.token)
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		// Retry without Markdown if parse_mode caused the error.
		if strings.Contains(string(respBody), "parse") {
			payload["parse_mode"] = ""
			body2, _ := json.Marshal(payload)
			resp2, err := http.Post(url, "application/json", bytes.NewReader(body2))
			if err != nil {
				return 0, err
			}
			defer resp2.Body.Close()
			respBody, _ = io.ReadAll(resp2.Body)
		} else {
			return 0, fmt.Errorf("telegram send non-200: %d %s", resp.StatusCode, string(respBody))
		}
	}
	var result struct {
		OK     bool `json:"ok"`
		Result struct {
			MessageID int `json:"message_id"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return 0, err
	}
	return result.Result.MessageID, nil
}

// editMessageText edits an existing Telegram message.
func (b *Bot) editMessageText(chatID int64, messageID int, text string) error {
	if len(text) > 4096 {
		text = text[:4093] + "..."
	}
	payload := map[string]any{
		"chat_id":    chatID,
		"message_id": messageID,
		"text":       text,
		"parse_mode": "Markdown",
	}
	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("https://api.telegram.org/bot%s/editMessageText", b.token)
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		// Retry without Markdown if parse_mode caused the error.
		if strings.Contains(string(respBody), "parse") {
			payload["parse_mode"] = ""
			body2, _ := json.Marshal(payload)
			resp2, err := http.Post(url, "application/json", bytes.NewReader(body2))
			if err != nil {
				return err
			}
			resp2.Body.Close()
			return nil
		}
		return fmt.Errorf("telegram edit non-200: %d %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// tgDeleteMessage deletes a Telegram message (best effort).
func (b *Bot) tgDeleteMessage(chatID int64, messageID int) {
	payload := map[string]any{
		"chat_id":    chatID,
		"message_id": messageID,
	}
	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("https://api.telegram.org/bot%s/deleteMessage", b.token)
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		b.rt.LogWarn("telegram delete message failed", "error", err)
		return
	}
	resp.Body.Close()
}

// --- P34: Telegram Progress Builder ---

// ansiEscapeRe matches ANSI escape sequences.
var ansiEscapeRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// tgProgressBuilder accumulates task progress for Telegram message updates.
type tgProgressBuilder struct {
	mu      sync.Mutex
	startAt time.Time
	tools   []string
	text    strings.Builder
	dirty   bool
}

func newTGProgressBuilder() *tgProgressBuilder {
	return &tgProgressBuilder{
		startAt: time.Now(),
	}
}

func (b *tgProgressBuilder) addToolCall(name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tools = append(b.tools, name)
	b.dirty = true
}

func (b *tgProgressBuilder) addText(text string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	text = ansiEscapeRe.ReplaceAllString(text, "")
	if text == "" {
		return
	}
	b.text.WriteString(text)
	b.dirty = true
}

func (b *tgProgressBuilder) render() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.dirty = false

	elapsed := time.Since(b.startAt).Round(time.Second)
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Working... (%s)\n", elapsed))

	start := 0
	if len(b.tools) > 5 {
		start = len(b.tools) - 5
		sb.WriteString(fmt.Sprintf("... and %d earlier steps\n", start))
	}
	for _, t := range b.tools[start:] {
		sb.WriteString(fmt.Sprintf("> %s\n", t))
	}

	accumulated := b.text.String()
	if accumulated != "" {
		sb.WriteString("\n")
		header := sb.String()
		maxText := 4000 - len(header) - 10 // Telegram 4096 limit with margin
		if maxText < 100 {
			maxText = 100
		}
		if len(accumulated) > maxText {
			trimmed := accumulated[len(accumulated)-maxText:]
			if idx := strings.Index(trimmed, "\n"); idx >= 0 && idx < len(trimmed)/2 {
				trimmed = trimmed[idx+1:]
			}
			sb.WriteString("..." + trimmed)
		} else {
			sb.WriteString(accumulated)
		}
	}

	return sb.String()
}

func (b *tgProgressBuilder) isDirty() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.dirty
}

// SSE event type constants (mirrored from root package).
const (
	sseToolCall    = "tool_call"
	sseOutputChunk = "output_chunk"
	sseCompleted   = "completed"
	sseError       = "error"
)

// runTelegramProgressUpdater subscribes to task SSE events and updates a Telegram progress message.
func (b *Bot) runTelegramProgressUpdater(
	chatID int64, progressMsgID int, taskID string,
	stopCh <-chan struct{},
	builder *tgProgressBuilder,
) {
	eventCh, unsub := b.rt.SubscribeTaskEvents(taskID)
	defer unsub()

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-stopCh:
			return
		case ev, ok := <-eventCh:
			if !ok {
				return
			}
			switch ev.Type {
			case sseToolCall:
				if data, ok := ev.Data.(map[string]any); ok {
					name, _ := data["name"].(string)
					if name != "" {
						builder.addToolCall(name)
					}
				}
			case sseOutputChunk:
				if data, ok := ev.Data.(map[string]any); ok {
					chunk, _ := data["chunk"].(string)
					if chunk != "" {
						builder.addText(chunk)
					}
				}
			case sseCompleted, sseError:
				return
			}
		case <-ticker.C:
			if builder.isDirty() {
				content := builder.render()
				if err := b.editMessageText(chatID, progressMsgID, content); err != nil {
					b.rt.LogWarn("telegram progress edit failed", "error", err)
				}
				b.sendTypingAction(chatID)
			}
		}
	}
}

// --- Telegram Formatters ---

func formatDispatchResult(dr *DispatchResult) string {
	ok := 0
	for _, t := range dr.Tasks {
		if t.Status == "success" {
			ok++
		}
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("Tetora: %d/%d tasks done\n", ok, len(dr.Tasks)))

	for _, t := range dr.Tasks {
		dur := time.Duration(t.DurationMs) * time.Millisecond
		switch t.Status {
		case "success":
			lines = append(lines, fmt.Sprintf("[OK] %s (%s, $%.2f)", t.Name, dur.Round(time.Second), t.CostUSD))
		case "timeout":
			lines = append(lines, fmt.Sprintf("[TIMEOUT] %s: %s", t.Name, t.Error))
		case "cancelled":
			lines = append(lines, fmt.Sprintf("[CANCEL] %s", t.Name))
		default:
			errMsg := t.Error
			if len(errMsg) > 100 {
				errMsg = errMsg[:100] + "..."
			}
			lines = append(lines, fmt.Sprintf("[FAIL] %s: %s", t.Name, errMsg))
		}
	}

	dur := time.Duration(dr.DurationMs) * time.Millisecond
	lines = append(lines, fmt.Sprintf("\nTotal: $%.2f | %s", dr.TotalCost, dur.Round(time.Second)))
	return strings.Join(lines, "\n")
}

// SendTelegramNotify sends a standalone Telegram message (for CLI --notify mode).
func SendTelegramNotify(cfg *Config, text string) error {
	if !cfg.Enabled || cfg.BotToken == "" {
		return nil
	}
	payload := map[string]any{
		"chat_id":    cfg.ChatID,
		"text":       text,
		"parse_mode": "Markdown",
	}
	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", cfg.BotToken)
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("telegram %d: %s", resp.StatusCode, respBody)
	}
	return nil
}

// --- Helpers ---

func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen < 4 {
		return string(runes[:maxLen])
	}
	return string(runes[:maxLen-3]) + "..."
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		m := int(d.Minutes())
		s := int(d.Seconds()) % 60
		if s == 0 {
			return fmt.Sprintf("%dm", m)
		}
		return fmt.Sprintf("%dm%ds", m, s)
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	return fmt.Sprintf("%dh%dm", h, m)
}

func coalesce(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// searchMemoryDir searches markdown files in dir for keyword matches.
func searchMemoryDir(dir, keyword string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}

	keyword = strings.ToLower(keyword)
	var matches []string

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			if strings.Contains(strings.ToLower(line), keyword) {
				matches = append(matches, fmt.Sprintf("%s:%d: %s", e.Name(), i+1, strings.TrimSpace(line)))
			}
		}
	}

	if len(matches) == 0 {
		return ""
	}
	return strings.Join(matches, "\n")
}
