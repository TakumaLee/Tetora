package main

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
	InlineKeyboard [][]tgInlineButton `json:"inline_keyboard"`
}

type tgInlineButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
	URL          string `json:"url,omitempty"`
}

// --- Bot ---

// pendingEstimate stores a task pending user cost confirmation.
type pendingEstimate struct {
	prompt    string
	isRoute   bool // true = /route flow, false = /ask flow
	estimate  *EstimateResult
	chatID    int64
	createdAt time.Time
}

const pendingEstimateTTL = 10 * time.Minute

// pendingSuggest stores a suggest-mode task result pending human approval.
type pendingSuggest struct {
	result    TaskResult
	role      string
	prompt    string
	chatID    int64
	createdAt time.Time
}

const pendingSuggestTTL = 30 * time.Minute

type Bot struct {
	token            string
	chatID           int64
	pollTimeout      int
	cfg              *Config
	state            *dispatchState
	sem              chan struct{}
	cron             *CronEngine
	client           *http.Client
	pendingEstimates map[string]*pendingEstimate
	pendingSuggests  map[string]*pendingSuggest
	pendingMu        sync.Mutex
}

func newBot(cfg *Config, state *dispatchState, sem chan struct{}, cron *CronEngine) *Bot {
	return &Bot{
		token:            cfg.Telegram.BotToken,
		chatID:           cfg.Telegram.ChatID,
		pollTimeout:      cfg.Telegram.PollTimeout,
		cfg:              cfg,
		state:            state,
		sem:              sem,
		cron:             cron,
		client:           &http.Client{Timeout: time.Duration(cfg.Telegram.PollTimeout+10) * time.Second},
		pendingEstimates: make(map[string]*pendingEstimate),
		pendingSuggests:  make(map[string]*pendingSuggest),
	}
}

// maybeCostConfirm checks if the estimated cost exceeds the threshold.
// If so, sends a confirmation keyboard and returns true (do NOT execute yet).
// If cost is below threshold, returns false (proceed immediately).
func (b *Bot) maybeCostConfirm(chatID int64, prompt string, isRoute bool) bool {
	task := Task{Prompt: prompt, Source: "telegram"}
	fillDefaults(b.cfg, &task)

	est := estimateTasks(b.cfg, []Task{task})

	threshold := b.cfg.Estimate.confirmThresholdOrDefault()
	if est.TotalEstimatedCost < threshold {
		return false
	}

	id := newUUID()[:8]
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

	keyboard := [][]tgInlineButton{
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
func (b *Bot) sendSuggestConfirm(chatID int64, result TaskResult, role string, prompt string) {
	id := newUUID()[:8]
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

	keyboard := [][]tgInlineButton{
		{
			{Text: "Approve", CallbackData: "trust_approve:" + id},
			{Text: "Reject", CallbackData: "trust_reject:" + id},
		},
	}
	b.replyWithKeyboard(chatID, strings.Join(lines, "\n"), keyboard)
}

func (b *Bot) pollLoop(ctx context.Context) {
	offset := 0
	logInfo("telegram bot polling started", "chatID", b.chatID)

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
			logError("telegram poll error", "error", err)
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
	var attachedFiles []*UploadedFile
	if msg.Document != nil {
		file, err := b.handleFileUpload(msg.Document.FileID, msg.Document.FileName)
		if err != nil {
			logError("telegram file upload error", "error", err)
		} else {
			attachedFiles = append(attachedFiles, file)
		}
	}
	if len(msg.Photo) > 0 {
		// Use the largest photo (last element has highest resolution).
		largest := msg.Photo[len(msg.Photo)-1]
		file, err := b.handleFileUpload(largest.FileID, "photo.jpg")
		if err != nil {
			logError("telegram photo upload error", "error", err)
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
			logInfo("telegram file received", "name", f.Name, "mime", f.MimeType, "bytes", f.Size)
		}

		// If no command, route as a prompt with file context.
		if !strings.HasPrefix(strings.TrimSpace(coalesce(msg.Caption, msg.Text)), "/") {
			if b.cfg.SmartDispatch.Enabled {
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
	case command == "/help":
		b.cmdHelp(msg)
	default:
		// Smart dispatch: route non-command messages if enabled.
		if b.cfg.SmartDispatch.Enabled && !strings.HasPrefix(text, "/") && text != "" {
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

func (b *Bot) cmdDispatch(ctx context.Context, msg *tgMessage, payload string) {
	if payload == "" {
		b.reply(msg.Chat.ID, "Usage: /dispatch [{\"name\":\"...\",\"prompt\":\"...\"}]")
		return
	}
	var tasks []Task
	if err := json.Unmarshal([]byte(payload), &tasks); err != nil {
		b.reply(msg.Chat.ID, fmt.Sprintf("JSON parse error: %v", err))
		return
	}
	if len(tasks) == 0 {
		b.reply(msg.Chat.ID, "No tasks provided.")
		return
	}

	for i := range tasks {
		fillDefaults(b.cfg, &tasks[i])
		tasks[i].Source = "dispatch"
	}

	b.state.mu.Lock()
	busy := b.state.active
	b.state.mu.Unlock()
	if busy {
		b.reply(msg.Chat.ID, "Already dispatching. Use /status or /cancel first.")
		return
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("Dispatch %d tasks (max concurrent: %d)\n", len(tasks), b.cfg.MaxConcurrent))
	for i, t := range tasks {
		extra := ""
		if t.MCP != "" {
			extra = " + " + t.MCP
		}
		lines = append(lines, fmt.Sprintf("%d. %s (%s%s)", i+1, t.Name, t.Model, extra))
	}
	b.reply(msg.Chat.ID, strings.Join(lines, "\n"))

	go func() {
		dispatchCtx := withTraceID(context.Background(), newTraceID("tg"))
		result := dispatch(dispatchCtx, b.cfg, tasks, b.state, b.sem)
		b.reply(msg.Chat.ID, formatTelegramResult(result))
	}()
}

// --- /status ---

func (b *Bot) cmdStatus(msg *tgMessage) {
	b.state.mu.Lock()
	active := b.state.active
	b.state.mu.Unlock()

	if !active {
		// Show cron status instead if no dispatch is active.
		if b.cron != nil {
			jobs := b.cron.ListJobs()
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
	b.reply(msg.Chat.ID, formatTelegramStatus(b.state))
}

// --- /cancel ---

func (b *Bot) cmdCancel(msg *tgMessage) {
	b.state.mu.Lock()
	cancelFn := b.state.cancel
	b.state.mu.Unlock()
	if cancelFn == nil {
		b.reply(msg.Chat.ID, "Nothing to cancel.")
		return
	}
	cancelFn()
	b.reply(msg.Chat.ID, "Cancelling all running tasks...")
}

// --- /cron (alias: /jobs) ---

func (b *Bot) cmdCron(ctx context.Context, msg *tgMessage, args string) {
	if b.cron == nil {
		b.reply(msg.Chat.ID, "Cron engine not available.")
		return
	}

	parts := strings.Fields(args)

	// /cron enable/disable <id>
	if len(parts) >= 2 && (parts[0] == "enable" || parts[0] == "disable") {
		enabled := parts[0] == "enable"
		id := parts[1]
		if err := b.cron.ToggleJob(id, enabled); err != nil {
			b.reply(msg.Chat.ID, fmt.Sprintf("Error: %v", err))
		} else {
			b.reply(msg.Chat.ID, fmt.Sprintf("Job %q %sd.", id, parts[0]))
		}
		return
	}

	// /cron run <id>
	if len(parts) >= 2 && parts[0] == "run" {
		id := parts[1]
		if err := b.cron.RunJobByID(ctx, id); err != nil {
			b.reply(msg.Chat.ID, fmt.Sprintf("Error: %v", err))
		} else {
			b.reply(msg.Chat.ID, fmt.Sprintf("Job %q triggered.", id))
		}
		return
	}

	// /cron (list) — with inline keyboard
	jobs := b.cron.ListJobs()
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
		if !j.NextRun.IsZero() && j.Enabled {
			nextStr = fmt.Sprintf(" next: %s", j.NextRun.Format("15:04"))
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
	var keyboard [][]tgInlineButton
	for _, j := range jobs {
		var row []tgInlineButton
		if !j.Running {
			row = append(row, tgInlineButton{Text: "Run " + j.Name, CallbackData: "run:" + j.ID})
		}
		if j.Enabled {
			row = append(row, tgInlineButton{Text: "Disable", CallbackData: "disable:" + j.ID})
		} else {
			row = append(row, tgInlineButton{Text: "Enable", CallbackData: "enable:" + j.ID})
		}
		if len(row) > 0 {
			keyboard = append(keyboard, row)
		}
	}

	b.replyWithKeyboard(msg.Chat.ID, strings.Join(lines, "\n"), keyboard)
}

// --- /cost ---

func (b *Bot) cmdCost(msg *tgMessage) {
	if b.cfg.HistoryDB == "" {
		b.reply(msg.Chat.ID, "History DB not configured.")
		return
	}

	stats, err := queryCostStats(b.cfg.HistoryDB)
	if err != nil {
		b.reply(msg.Chat.ID, fmt.Sprintf("Error: %v", err))
		return
	}

	text := fmt.Sprintf("Cost Summary\n\nToday: $%.2f\nThis Week: $%.2f\nThis Month: $%.2f",
		stats.Today, stats.Week, stats.Month)

	if b.cfg.CostAlert.DailyLimit > 0 {
		pct := (stats.Today / b.cfg.CostAlert.DailyLimit) * 100
		text += fmt.Sprintf("\n\nDaily limit: $%.2f (%.0f%% used)", b.cfg.CostAlert.DailyLimit, pct)
	}
	if b.cfg.CostAlert.WeeklyLimit > 0 {
		pct := (stats.Week / b.cfg.CostAlert.WeeklyLimit) * 100
		text += fmt.Sprintf("\nWeekly limit: $%.2f (%.0f%% used)", b.cfg.CostAlert.WeeklyLimit, pct)
	}

	// Budget governance status.
	budgets := b.cfg.Budgets
	if budgets.Paused {
		text += "\n\nBudget: PAUSED"
	} else if budgets.Global.Daily > 0 || budgets.Global.Weekly > 0 || budgets.Global.Monthly > 0 {
		text += "\n\nBudget:"
		if budgets.Global.Daily > 0 {
			pct := (stats.Today / budgets.Global.Daily) * 100
			text += fmt.Sprintf("\n  Daily: $%.2f/$%.2f (%.0f%%)", stats.Today, budgets.Global.Daily, pct)
		}
		if budgets.Global.Weekly > 0 {
			pct := (stats.Week / budgets.Global.Weekly) * 100
			text += fmt.Sprintf("\n  Weekly: $%.2f/$%.2f (%.0f%%)", stats.Week, budgets.Global.Weekly, pct)
		}
		if budgets.Global.Monthly > 0 {
			pct := (stats.Month / budgets.Global.Monthly) * 100
			text += fmt.Sprintf("\n  Monthly: $%.2f/$%.2f (%.0f%%)", stats.Month, budgets.Global.Monthly, pct)
		}
		if budgets.AutoDowngrade.Enabled {
			text += "\n  Auto-downgrade: ON"
		}
	}

	// Per-job cost breakdown.
	costByJob, err := queryCostByJobID(b.cfg.HistoryDB)
	if err == nil && len(costByJob) > 0 {
		text += "\n\nPer Job (30d):"
		for id, cost := range costByJob {
			text += fmt.Sprintf("\n  %s: $%.2f", id, cost)
		}
	}

	b.reply(msg.Chat.ID, text)
}

// --- /tasks ---

func (b *Bot) cmdTasks(msg *tgMessage) {
	if b.cfg.DashboardDB == "" {
		b.reply(msg.Chat.ID, "Dashboard DB not configured.")
		return
	}

	stats, err := getTaskStats(b.cfg.DashboardDB)
	if err != nil {
		b.reply(msg.Chat.ID, fmt.Sprintf("DB error: %v", err))
		return
	}

	text := fmt.Sprintf("Dashboard Tasks\n\n"+
		"Todo: %d\nRunning: %d\nReview: %d\nDone: %d\nFailed: %d\nTotal: %d",
		stats.Todo, stats.Running, stats.Review, stats.Done, stats.Failed, stats.Total)

	// Show stuck tasks if any.
	stuck, _ := getStuckTasks(b.cfg.DashboardDB, 30)
	if len(stuck) > 0 {
		text += fmt.Sprintf("\n\nStuck (>30min): %d", len(stuck))
		for _, t := range stuck {
			text += fmt.Sprintf("\n  - %s (%s)", t.Name, t.CreatedAt)
		}
	}

	b.reply(msg.Chat.ID, text)
}

// --- /health ---

func (b *Bot) cmdHealth(ctx context.Context, msg *tgMessage) {
	if b.cron == nil {
		b.reply(msg.Chat.ID, "Cron engine not available.")
		return
	}

	err := b.cron.RunJobByID(ctx, "heartbeat")
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

	memDir := filepath.Join(b.cfg.DefaultWorkdir, "memory")
	if _, err := os.Stat(memDir); os.IsNotExist(err) {
		b.reply(msg.Chat.ID, "Memory directory not found.")
		return
	}

	// Search memory files using grep.
	results := searchMemory(memDir, keyword)
	if results == "" {
		b.reply(msg.Chat.ID, fmt.Sprintf("No results for %q in memory.", keyword))
		return
	}
	b.reply(msg.Chat.ID, truncate(results, 2000))
}

func searchMemory(dir, keyword string) string {
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
		dbPath := b.cfg.HistoryDB
		chKey := channelSessionKey("tg", "ask")

		// Find or create channel session.
		sess, err := getOrCreateChannelSession(dbPath, "telegram", chKey, "", "Quick Ask")
		if err != nil {
			logError("telegram ask session error", "error", err)
		}

		// Build context from previous messages.
		contextPrompt := prompt
		if sess != nil {
			ctx := buildSessionContext(dbPath, sess.ID, b.cfg.Session.contextMessagesOrDefault())
			contextPrompt = wrapWithContext(ctx, prompt)

			// Record user message.
			now := time.Now().Format(time.RFC3339)
			addSessionMessage(dbPath, SessionMessage{
				SessionID: sess.ID,
				Role:      "user",
				Content:   truncateStr(prompt, 5000),
				CreatedAt: now,
			})
			updateSessionStats(dbPath, sess.ID, 0, 0, 0, 1)
		}

		task := Task{
			Prompt:  contextPrompt,
			Timeout: "3m",
			Budget:  0.5,
			Source:  "ask",
		}
		fillDefaults(b.cfg, &task)
		if sess != nil {
			task.SessionID = sess.ID
		}

		result := runSingleTask(ctx, b.cfg, task, b.sem, "")

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
			updateSessionStats(dbPath, sess.ID, result.CostUSD, result.TokensIn, result.TokensOut, 1)

			// Trigger compaction if needed.
			maybeCompactSession(b.cfg, dbPath, sess.ID, sess.MessageCount+2, b.sem)
		}

		if result.Status == "success" {
			b.reply(msg.Chat.ID, truncate(result.Output, 3000))
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

	if !b.cfg.SmartDispatch.Enabled {
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
		dbPath := b.cfg.HistoryDB

		// Step 1: Route (classify which role handles this).
		route := routeTask(ctx, b.cfg, RouteRequest{Prompt: prompt, Source: "telegram"}, b.sem)
		logInfoCtx(ctx, "route result", "prompt", truncate(prompt, 60), "role", route.Role, "method", route.Method, "confidence", route.Confidence)

		// Step 2: Find or create channel session for this role.
		chKey := channelSessionKey("tg", route.Role)
		sess, err := getOrCreateChannelSession(dbPath, "telegram", chKey, route.Role, "")
		if err != nil {
			logError("telegram route session error", "error", err)
		}

		// Step 3: Build context-aware prompt.
		contextPrompt := prompt
		if sess != nil {
			sessionCtx := buildSessionContext(dbPath, sess.ID, b.cfg.Session.contextMessagesOrDefault())
			contextPrompt = wrapWithContext(sessionCtx, prompt)

			// Record user message to session.
			now := time.Now().Format(time.RFC3339)
			addSessionMessage(dbPath, SessionMessage{
				SessionID: sess.ID,
				Role:      "user",
				Content:   truncateStr(prompt, 5000),
				CreatedAt: now,
			})
			updateSessionStats(dbPath, sess.ID, 0, 0, 0, 1)

			// Update title from first real message.
			title := prompt
			if len(title) > 100 {
				title = title[:100]
			}
			updateSessionTitle(dbPath, sess.ID, title)
		}

		// Step 4: Build and run task with the selected role.
		task := Task{
			Prompt: contextPrompt,
			Role:   route.Role,
			Source: "route:telegram",
		}
		fillDefaults(b.cfg, &task)
		if sess != nil {
			task.SessionID = sess.ID
		}

		// Inject role soul prompt + model + permission mode.
		if route.Role != "" {
			if soulPrompt, err := loadRolePrompt(b.cfg, route.Role); err == nil && soulPrompt != "" {
				task.SystemPrompt = soulPrompt
			}
			if rc, ok := b.cfg.Roles[route.Role]; ok {
				if rc.Model != "" {
					task.Model = rc.Model
				}
				if rc.PermissionMode != "" {
					task.PermissionMode = rc.PermissionMode
				}
			}
		}

		// Expand template variables.
		task.Prompt = expandPrompt(task.Prompt, "", b.cfg.HistoryDB, route.Role, b.cfg.KnowledgeDir, b.cfg)

		taskStart := time.Now()
		result := runSingleTask(ctx, b.cfg, task, b.sem, route.Role)

		// Record to history.
		recordHistory(b.cfg.HistoryDB, task.ID, task.Name, task.Source, route.Role, task, result,
			taskStart.Format(time.RFC3339), time.Now().Format(time.RFC3339), result.OutputFile)

		// Step 5: Record assistant response to channel session.
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

			// Trigger compaction if needed.
			maybeCompactSession(b.cfg, dbPath, sess.ID, sess.MessageCount+2, b.sem)
		}

		// Store output summary in agent memory.
		if result.Status == "success" && dbPath != "" {
			setMemory(dbPath, route.Role, "last_route_output", truncate(result.Output, 500))
			setMemory(dbPath, route.Role, "last_route_prompt", truncate(prompt, 200))
			setMemory(dbPath, route.Role, "last_route_time", time.Now().Format(time.RFC3339))
		}

		// Optional coordinator review.
		sdr := &SmartDispatchResult{Route: *route, Task: result}
		if b.cfg.SmartDispatch.Review && result.Status == "success" {
			reviewOK, reviewComment := reviewOutput(ctx, b.cfg, prompt, result.Output, route.Role, b.sem)
			sdr.ReviewOK = &reviewOK
			sdr.Review = reviewComment
		}

		// Audit log.
		auditLog(dbPath, "route.dispatch", "telegram",
			fmt.Sprintf("role=%s method=%s session=%s prompt=%s",
				route.Role, route.Method, task.SessionID, truncate(prompt, 100)), "")

		// Webhook notifications.
		sendWebhooks(b.cfg, result.Status, WebhookPayload{
			JobID:    task.ID,
			Name:     task.Name,
			Source:   task.Source,
			Status:   result.Status,
			Cost:     result.CostUSD,
			Duration: result.DurationMs,
			Model:    result.Model,
			Output:   truncate(result.Output, 500),
			Error:    truncate(result.Error, 300),
		})

		// Suggest mode: hold output for human approval.
		trustLevel := resolveTrustLevel(b.cfg, route.Role)
		if trustLevel == TrustSuggest && result.Status == "success" {
			b.sendSuggestConfirm(msg.Chat.ID, result, route.Role, prompt)
			return
		}

		// Format and send response.
		b.sendRouteResponse(msg.Chat.ID, sdr)
	}()
}

// sendRouteResponse formats and sends a smart dispatch result to Telegram.
func (b *Bot) sendRouteResponse(chatID int64, result *SmartDispatchResult) {
	var lines []string

	lines = append(lines, fmt.Sprintf("\xf0\x9f\x8e\xaf Route \xe2\x86\x92 %s (%s, %s confidence)",
		result.Route.Role, result.Route.Method, result.Route.Confidence))

	if result.Task.Status == "success" {
		lines = append(lines, "")
		lines = append(lines, truncate(result.Task.Output, 3000))
	} else {
		lines = append(lines, fmt.Sprintf("\n\xe2\x9d\x8c [%s] %s",
			result.Task.Status, truncate(result.Task.Error, 500)))
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
		keyboard := [][]tgInlineButton{
			{
				{Text: "\xf0\x9f\x94\x84 Retry", CallbackData: "retry:" + result.Task.ID},
				{Text: "\xf0\x9f\x94\x80 Reroute", CallbackData: "reroute:" + result.Task.ID},
			},
		}
		b.replyWithKeyboard(chatID, responseText, keyboard)
	} else {
		b.reply(chatID, responseText)
	}
}

// --- /help ---

func (b *Bot) cmdHelp(msg *tgMessage) {
	b.reply(msg.Chat.ID, "Tetora - AI Agent Orchestrator\n\n"+
		"/dispatch [tasks JSON] - parallel task dispatch\n"+
		"/route <task> - smart dispatch (auto-route to best role)\n"+
		"/ask <prompt> - quick question (no role)\n"+
		"/new [role] - start fresh session (archives current)\n"+
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
		"/memory <keyword> - search memory files\n"+
		"/help - this message\n\n"+
		"Messages are linked to persistent sessions per role.\n"+
		"Conversation history is automatically maintained.\n"+
		"Use /new to start a fresh conversation.\n\n"+
		"Cost confirmation: tasks estimated above $"+
		fmt.Sprintf("%.2f", b.cfg.Estimate.confirmThresholdOrDefault())+
		" will ask for confirmation before executing.\n\n"+
		"You can also send files/photos directly - they will be saved and analyzed by the agent.")
}

// --- /trust ---

func (b *Bot) cmdTrust(msg *tgMessage) {
	statuses := getAllTrustStatuses(b.cfg)
	if len(statuses) == 0 {
		b.reply(msg.Chat.ID, "No roles configured.")
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
		line := fmt.Sprintf("%s %s: %s", icon, s.Role, s.Level)
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

	// Handle retry/reroute actions (do not require cron).
	switch action {
	case "retry":
		b.answerCallback(cq.ID, "Retrying...")
		go func() {
			result, err := retryTask(ctx, b.cfg, id, b.state, b.sem)
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
				keyboard := [][]tgInlineButton{
					{
						{Text: "\xf0\x9f\x94\x84 Retry", CallbackData: "retry:" + result.ID},
						{Text: "\xf0\x9f\x94\x80 Reroute", CallbackData: "reroute:" + result.ID},
					},
				}
				b.replyWithKeyboard(cq.Message.Chat.ID, text, keyboard)
			}
		}()
		return
	case "reroute":
		b.answerCallback(cq.ID, "Rerouting...")
		go func() {
			result, err := rerouteTask(ctx, b.cfg, id, b.state, b.sem)
			if err != nil {
				b.reply(cq.Message.Chat.ID, fmt.Sprintf("\xe2\x9d\x8c Reroute failed: %s", err.Error()))
				return
			}
			var lines []string
			lines = append(lines, fmt.Sprintf("\xf0\x9f\x94\x80 Rerouted \xe2\x86\x92 %s (%s)",
				result.Route.Role, result.Route.Method))
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
				keyboard := [][]tgInlineButton{
					{
						{Text: "\xf0\x9f\x94\x84 Retry", CallbackData: "retry:" + result.Task.ID},
						{Text: "\xf0\x9f\x94\x80 Reroute", CallbackData: "reroute:" + result.Task.ID},
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
	}

	// Cron-related actions require cron engine.
	if b.cron == nil {
		b.answerCallback(cq.ID, "Cron not available")
		return
	}

	switch action {
	case "run":
		if err := b.cron.RunJobByID(ctx, id); err != nil {
			b.answerCallback(cq.ID, "Error: "+err.Error())
		} else {
			b.answerCallback(cq.ID, id+" triggered")
			b.reply(cq.Message.Chat.ID, fmt.Sprintf("Job %q triggered.", id))
		}
	case "enable":
		if err := b.cron.ToggleJob(id, true); err != nil {
			b.answerCallback(cq.ID, "Error: "+err.Error())
		} else {
			b.answerCallback(cq.ID, id+" enabled")
			b.reply(cq.Message.Chat.ID, fmt.Sprintf("Job %q enabled.", id))
		}
	case "disable":
		if err := b.cron.ToggleJob(id, false); err != nil {
			b.answerCallback(cq.ID, "Error: "+err.Error())
		} else {
			b.answerCallback(cq.ID, id+" disabled")
			b.reply(cq.Message.Chat.ID, fmt.Sprintf("Job %q disabled.", id))
		}
	case "approve":
		if err := b.cron.ApproveJob(id); err != nil {
			b.answerCallback(cq.ID, "Error: "+err.Error())
		} else {
			b.answerCallback(cq.ID, id+" approved")
			b.reply(cq.Message.Chat.ID, fmt.Sprintf("Job %q approved. Running now.", id))
		}
	case "reject":
		if err := b.cron.RejectJob(id); err != nil {
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
	if b.cron == nil {
		b.reply(msg.Chat.ID, "Cron engine not available.")
		return
	}
	id := strings.TrimSpace(args)
	if id == "" {
		b.reply(msg.Chat.ID, "Usage: /approve <job-id>")
		return
	}
	if err := b.cron.ApproveJob(id); err != nil {
		b.reply(msg.Chat.ID, fmt.Sprintf("Error: %v", err))
	} else {
		b.reply(msg.Chat.ID, fmt.Sprintf("Job %q approved. Running now.", id))
	}
}

func (b *Bot) cmdReject(msg *tgMessage, args string) {
	if b.cron == nil {
		b.reply(msg.Chat.ID, "Cron engine not available.")
		return
	}
	id := strings.TrimSpace(args)
	if id == "" {
		b.reply(msg.Chat.ID, "Usage: /reject <job-id>")
		return
	}
	if err := b.cron.RejectJob(id); err != nil {
		b.reply(msg.Chat.ID, fmt.Sprintf("Error: %v", err))
	} else {
		b.reply(msg.Chat.ID, fmt.Sprintf("Job %q rejected.", id))
	}
}

// --- File Download ---

// downloadTelegramFile downloads a file from Telegram by its file_id.
// Returns the filename, a reader for the file content, and any error.
func (b *Bot) downloadTelegramFile(fileID string) (string, io.ReadCloser, error) {
	// Step 1: Get file path from Telegram.
	getURL := fmt.Sprintf("https://api.telegram.org/bot%s/getFile?file_id=%s", b.token, fileID)
	resp, err := http.Get(getURL)
	if err != nil {
		return "", nil, fmt.Errorf("getFile request: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		OK     bool `json:"ok"`
		Result struct {
			FilePath string `json:"file_path"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", nil, fmt.Errorf("decode getFile response: %w", err)
	}
	if !result.OK || result.Result.FilePath == "" {
		return "", nil, fmt.Errorf("telegram getFile failed for file_id=%s", fileID)
	}

	// Step 2: Download the file.
	downloadURL := fmt.Sprintf("https://api.telegram.org/file/bot%s/%s", b.token, result.Result.FilePath)
	fileResp, err := http.Get(downloadURL)
	if err != nil {
		return "", nil, fmt.Errorf("download file: %w", err)
	}
	if fileResp.StatusCode != 200 {
		fileResp.Body.Close()
		return "", nil, fmt.Errorf("download file: status %d", fileResp.StatusCode)
	}

	fileName := filepath.Base(result.Result.FilePath)
	return fileName, fileResp.Body, nil
}

// handleFileUpload downloads a Telegram file and saves it to the uploads directory.
func (b *Bot) handleFileUpload(fileID, fileName string) (*UploadedFile, error) {
	name, reader, err := b.downloadTelegramFile(fileID)
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	if fileName != "" {
		name = fileName
	}
	uploadDir := initUploadDir(b.cfg.baseDir)
	return saveUpload(uploadDir, name, reader, 0, "telegram")
}

// --- /new (start fresh session) ---

func (b *Bot) cmdNew(msg *tgMessage, args string) {
	dbPath := b.cfg.HistoryDB
	if dbPath == "" {
		b.reply(msg.Chat.ID, "History DB not configured.")
		return
	}

	role := strings.TrimSpace(args)
	if role == "" {
		// Archive all active Telegram channel sessions.
		archived := 0
		if b.cfg.Roles != nil {
			for roleName := range b.cfg.Roles {
				chKey := channelSessionKey("tg", roleName)
				if err := archiveChannelSession(dbPath, chKey); err == nil {
					archived++
				}
			}
		}
		// Also archive the "ask" session.
		archiveChannelSession(dbPath, channelSessionKey("tg", "ask"))
		b.reply(msg.Chat.ID, fmt.Sprintf("Archived %d channel sessions. Fresh start!", archived))
	} else {
		// Archive session for specific role.
		if _, ok := b.cfg.Roles[role]; !ok {
			b.reply(msg.Chat.ID, fmt.Sprintf("Unknown role: %s", role))
			return
		}
		chKey := channelSessionKey("tg", role)
		if err := archiveChannelSession(dbPath, chKey); err != nil {
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

// --- Telegram HTTP ---

// replyWithKeyboard sends a message with an inline keyboard.
func (b *Bot) replyWithKeyboard(chatID int64, text string, keyboard [][]tgInlineButton) {
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
		logError("telegram send error", "error", err)
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
			logWarn("telegram send non-200", "status", resp.StatusCode, "body", string(respBody))
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
		logError("telegram answerCallback error", "error", err)
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
		logError("telegram send error", "error", err)
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
			logWarn("telegram send non-200", "status", resp.StatusCode, "body", string(respBody))
		}
	}
}

// sendNotify sends a standalone Telegram message (for cron notifications etc).
func (b *Bot) sendNotify(text string) {
	b.reply(b.chatID, text)
}

// --- Telegram Formatters ---

func formatTelegramResult(dr *DispatchResult) string {
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

func formatTelegramStatus(state *dispatchState) string {
	state.mu.Lock()
	defer state.mu.Unlock()

	elapsed := time.Since(state.startAt).Round(time.Second)
	var lines []string
	lines = append(lines, fmt.Sprintf("Dispatch in progress (%s)\n", elapsed))

	for _, ts := range state.running {
		e := time.Since(ts.startAt).Round(time.Second)
		lines = append(lines, fmt.Sprintf("[>] %s (%s)", ts.task.Name, e))
	}
	for _, r := range state.finished {
		dur := time.Duration(r.DurationMs) * time.Millisecond
		if r.Status == "success" {
			lines = append(lines, fmt.Sprintf("[OK] %s (%s, $%.2f)", r.Name, dur.Round(time.Second), r.CostUSD))
		} else {
			lines = append(lines, fmt.Sprintf("[FAIL] %s (%s)", r.Name, r.Status))
		}
	}
	return strings.Join(lines, "\n")
}

// sendTelegramNotify sends a standalone notification (for CLI --notify mode).
func sendTelegramNotify(cfg *TelegramConfig, text string) error {
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
