package main

// wire_tgbot.go wires the internal/messaging/telegram package to the root package.
// It provides a concrete implementation of tgbot.TelegramRuntime that delegates
// to root package functions, allowing the tgbot.Bot to remain in the internal package
// while accessing root internals via this interface.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tetora/internal/messaging"
	"tetora/internal/upload"
	tgbot "tetora/internal/messaging/telegram"
)

// telegramRuntime implements tgbot.TelegramRuntime.
// It embeds *messagingRuntime to inherit all BotRuntime methods and only
// implements the Telegram-specific additions.
type telegramRuntime struct {
	*messagingRuntime
}

// newTelegramRuntime creates a new telegramRuntime.
func newTelegramRuntime(cfg *Config, state *dispatchState, sem, childSem chan struct{}, cron *CronEngine) *telegramRuntime {
	mr := newMessagingRuntime(cfg, state, sem, childSem)
	mr.cron = cron
	return &telegramRuntime{messagingRuntime: mr}
}

// Ensure telegramRuntime implements TelegramRuntime at compile time.
var _ tgbot.TelegramRuntime = (*telegramRuntime)(nil)

// --- Dispatch ---

func (r *telegramRuntime) Dispatch(ctx context.Context, tasks []tgbot.DispatchTask) *tgbot.DispatchResult {
	rootTasks := make([]Task, len(tasks))
	for i, t := range tasks {
		rootTasks[i] = Task{
			Name:   t.Name,
			Prompt: t.Prompt,
			Model:  t.Model,
			Agent:  t.Agent,
			MCP:    t.MCP,
			Source: t.Source,
		}
		fillDefaults(r.cfg, &rootTasks[i])
	}

	rootResult := dispatch(ctx, r.cfg, rootTasks, r.state, r.sem, r.childSem)

	result := &tgbot.DispatchResult{
		DurationMs: rootResult.DurationMs,
		TotalCost:  rootResult.TotalCost,
	}
	for _, t := range rootResult.Tasks {
		result.Tasks = append(result.Tasks, tgbot.DispatchTaskResult{
			ID:         t.ID,
			Name:       t.Name,
			Status:     t.Status,
			Output:     t.Output,
			Error:      t.Error,
			CostUSD:    t.CostUSD,
			DurationMs: t.DurationMs,
		})
	}
	return result
}

func (r *telegramRuntime) DispatchStatus() string {
	if r.state == nil {
		return ""
	}
	return string(r.state.statusJSON())
}

func (r *telegramRuntime) DispatchActive() bool {
	if r.state == nil {
		return false
	}
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	return r.state.active
}

func (r *telegramRuntime) CancelDispatch() {
	if r.state == nil {
		return
	}
	r.state.mu.Lock()
	cancelFn := r.state.cancel
	r.state.mu.Unlock()
	if cancelFn != nil {
		cancelFn()
	}
}

// --- Routing ---

func (r *telegramRuntime) RouteAndRun(ctx context.Context, prompt, source, sessionID, sessionCtx string) *tgbot.SmartDispatchResult {
	route := routeTask(ctx, r.cfg, RouteRequest{Prompt: prompt, Source: source})
	if route == nil {
		return &tgbot.SmartDispatchResult{}
	}

	contextPrompt := prompt
	if sessionCtx != "" {
		contextPrompt = wrapWithContext(sessionCtx, prompt)
	}

	task := Task{
		Prompt:    contextPrompt,
		Agent:     route.Agent,
		Source:    source,
		SessionID: sessionID,
	}
	fillDefaults(r.cfg, &task)

	if route.Agent != "" {
		if soulPrompt, err := loadAgentPrompt(r.cfg, route.Agent); err == nil && soulPrompt != "" {
			task.SystemPrompt = soulPrompt
		}
		if rc, ok := r.cfg.Agents[route.Agent]; ok {
			if rc.Model != "" {
				task.Model = rc.Model
			}
			if rc.PermissionMode != "" {
				task.PermissionMode = rc.PermissionMode
			}
		}
	}

	task.Prompt = expandPrompt(task.Prompt, "", r.cfg.HistoryDB, route.Agent, r.cfg.KnowledgeDir, r.cfg)

	if r.state != nil && r.state.broker != nil {
		task.sseBroker = r.state.broker
	}

	result := runSingleTask(ctx, r.cfg, task, r.sem, r.childSem, route.Agent)

	sdr := &tgbot.SmartDispatchResult{
		Route: tgbot.RouteResult{
			Agent:      route.Agent,
			Method:     route.Method,
			Confidence: route.Confidence,
		},
		Task: messaging.TaskResult{
			Output:     result.Output,
			Error:      result.Error,
			Status:     result.Status,
			CostUSD:    result.CostUSD,
			TokensIn:   float64(result.TokensIn),
			TokensOut:  float64(result.TokensOut),
			Model:      result.Model,
			OutputFile: result.OutputFile,
			TaskID:     task.ID,
			DurationMs: result.DurationMs,
		},
	}

	if r.cfg.SmartDispatch.Review && result.Status == "success" {
		reviewOK, reviewComment := reviewOutput(ctx, r.cfg, prompt, result.Output, route.Agent, r.sem, r.childSem)
		sdr.ReviewOK = &reviewOK
		sdr.Review = reviewComment
	}

	return sdr
}

func (r *telegramRuntime) RunAsk(ctx context.Context, prompt, sessionID, sessionCtx string) messaging.TaskResult {
	contextPrompt := prompt
	if sessionCtx != "" {
		contextPrompt = wrapWithContext(sessionCtx, prompt)
	}

	task := Task{
		Prompt:    contextPrompt,
		Timeout:   "3m",
		Budget:    0.5,
		Source:    "ask",
		SessionID: sessionID,
	}
	fillDefaults(r.cfg, &task)

	if r.state != nil && r.state.broker != nil {
		task.sseBroker = r.state.broker
	}

	result := runSingleTask(ctx, r.cfg, task, r.sem, r.childSem, "")
	return messaging.TaskResult{
		Output:     result.Output,
		Error:      result.Error,
		Status:     result.Status,
		CostUSD:    result.CostUSD,
		TokensIn:   float64(result.TokensIn),
		TokensOut:  float64(result.TokensOut),
		Model:      result.Model,
		OutputFile: result.OutputFile,
		TaskID:     task.ID,
		DurationMs: result.DurationMs,
	}
}

// --- Cost Estimation ---

func (r *telegramRuntime) EstimateCost(prompt string) *tgbot.CostEstimate {
	task := Task{Prompt: prompt, Source: "telegram"}
	fillDefaults(r.cfg, &task)
	est := estimateTasks(r.cfg, []Task{task})
	if est == nil {
		return &tgbot.CostEstimate{}
	}

	result := &tgbot.CostEstimate{
		TotalEstimatedCost: est.TotalEstimatedCost,
		ClassifyCost:       est.ClassifyCost,
	}
	for _, t := range est.Tasks {
		result.Tasks = append(result.Tasks, tgbot.CostEstimateTask{
			Model:            t.Model,
			Provider:         t.Provider,
			EstimatedCostUSD: t.EstimatedCostUSD,
			Breakdown:        t.Breakdown,
		})
	}
	return result
}

func (r *telegramRuntime) EstimateThreshold() float64 {
	return r.cfg.Estimate.confirmThresholdOrDefault()
}

// --- Trust ---

func (r *telegramRuntime) GetTrustLevel(agent string) tgbot.TrustLevel {
	level := resolveTrustLevel(r.cfg, agent)
	switch level {
	case TrustObserve:
		return tgbot.TrustObserve
	case TrustSuggest:
		return tgbot.TrustSuggest
	default:
		return tgbot.TrustAuto
	}
}

func (r *telegramRuntime) GetAllTrustStatuses() []tgbot.TrustStatus {
	statuses := getAllTrustStatuses(r.cfg)
	result := make([]tgbot.TrustStatus, len(statuses))
	for i, s := range statuses {
		var level tgbot.TrustLevel
		switch s.Level {
		case TrustObserve:
			level = tgbot.TrustObserve
		case TrustSuggest:
			level = tgbot.TrustSuggest
		default:
			level = tgbot.TrustAuto
		}
		var nextLevel tgbot.TrustLevel
		switch s.NextLevel {
		case TrustObserve:
			nextLevel = tgbot.TrustObserve
		case TrustSuggest:
			nextLevel = tgbot.TrustSuggest
		default:
			nextLevel = tgbot.TrustAuto
		}
		result[i] = tgbot.TrustStatus{
			Agent:              s.Agent,
			Level:              level,
			ConsecutiveSuccess: s.ConsecutiveSuccess,
			PromoteReady:       s.PromoteReady,
			NextLevel:          nextLevel,
		}
	}
	return result
}

// --- Review ---

func (r *telegramRuntime) ReviewOutput(ctx context.Context, prompt, output, agent string) (bool, string) {
	return reviewOutput(ctx, r.cfg, prompt, output, agent, r.sem, r.childSem)
}

// --- Memory ---

// SetMemory is already provided by messagingRuntime (inherited via embed).

func (r *telegramRuntime) SearchMemory(keyword string) string {
	memDir := filepath.Join(r.cfg.DefaultWorkdir, "memory")
	if _, err := os.Stat(memDir); os.IsNotExist(err) {
		return ""
	}
	return searchMemoryDir(memDir, keyword)
}

// searchMemoryDir searches .md files in a directory for keyword matches.
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

// --- Cost Stats ---

func (r *telegramRuntime) GetCostStats() (today, week, month float64) {
	stats, err := queryCostStats(r.cfg.HistoryDB)
	if err != nil {
		return 0, 0, 0
	}
	return stats.Today, stats.Week, stats.Month
}

func (r *telegramRuntime) GetCostByJob() map[string]float64 {
	result, err := queryCostByJobID(r.cfg.HistoryDB)
	if err != nil {
		return nil
	}
	return result
}

// --- Task Stats ---

func (r *telegramRuntime) GetTaskStats() (*tgbot.TaskStats, error) {
	stats, err := getTaskStats(r.cfg.HistoryDB)
	if err != nil {
		return nil, err
	}
	return &tgbot.TaskStats{
		Todo:    stats.Todo,
		Running: stats.Running,
		Review:  stats.Review,
		Done:    stats.Done,
		Failed:  stats.Failed,
		Total:   stats.Total,
	}, nil
}

func (r *telegramRuntime) GetStuckTasks(thresholdMin int) []tgbot.StuckTask {
	tasks, err := getStuckTasks(r.cfg.HistoryDB, thresholdMin)
	if err != nil {
		return nil
	}
	result := make([]tgbot.StuckTask, len(tasks))
	for i, t := range tasks {
		result[i] = tgbot.StuckTask{
			Title:     t.Title,
			CreatedAt: t.CreatedAt,
		}
	}
	return result
}

// --- Cron ---

func (r *telegramRuntime) CronListJobs() []tgbot.CronJobInfo {
	if r.cron == nil {
		return nil
	}
	jobs := r.cron.ListJobs()
	result := make([]tgbot.CronJobInfo, len(jobs))
	for i, j := range jobs {
		result[i] = tgbot.CronJobInfo{
			ID:       j.ID,
			Name:     j.Name,
			Schedule: j.Schedule,
			Enabled:  j.Enabled,
			Running:  j.Running,
			NextRun:  j.NextRun,
			Errors:   j.Errors,
			AvgCost:  j.AvgCost,
		}
	}
	return result
}

func (r *telegramRuntime) CronToggleJob(id string, enabled bool) error {
	if r.cron == nil {
		return fmt.Errorf("cron engine not available")
	}
	return r.cron.ToggleJob(id, enabled)
}

func (r *telegramRuntime) CronRunJob(ctx context.Context, id string) error {
	if r.cron == nil {
		return fmt.Errorf("cron engine not available")
	}
	return r.cron.RunJobByID(ctx, id)
}

func (r *telegramRuntime) CronApproveJob(id string) error {
	if r.cron == nil {
		return fmt.Errorf("cron engine not available")
	}
	return r.cron.ApproveJob(id)
}

func (r *telegramRuntime) CronRejectJob(id string) error {
	if r.cron == nil {
		return fmt.Errorf("cron engine not available")
	}
	return r.cron.RejectJob(id)
}

func (r *telegramRuntime) CronAvailable() bool {
	return r.cron != nil
}

// --- Config Accessors ---

func (r *telegramRuntime) MaxConcurrent() int {
	return r.cfg.MaxConcurrent
}

func (r *telegramRuntime) SmartDispatchEnabled() bool {
	return r.cfg.SmartDispatch.Enabled
}

func (r *telegramRuntime) SmartDispatchReview() bool {
	return r.cfg.SmartDispatch.Review
}

func (r *telegramRuntime) StreamToChannels() bool {
	return r.cfg.StreamToChannels
}

func (r *telegramRuntime) DefaultWorkdir() string {
	return r.cfg.DefaultWorkdir
}

func (r *telegramRuntime) ApprovalGatesEnabled() bool {
	return r.cfg.ApprovalGates.Enabled
}

func (r *telegramRuntime) ApprovalGateAutoApproveTools() []string {
	return r.cfg.ApprovalGates.AutoApproveTools
}

// --- SSE ---

func (r *telegramRuntime) SubscribeTaskEvents(taskID string) (<-chan tgbot.SSEEvent, func()) {
	if r.state == nil || r.state.broker == nil {
		ch := make(chan tgbot.SSEEvent)
		close(ch)
		return ch, func() {}
	}

	rawCh, unsub := r.state.broker.Subscribe(taskID)
	outCh := make(chan tgbot.SSEEvent, 64)

	go func() {
		defer close(outCh)
		for ev := range rawCh {
			select {
			case outCh <- tgbot.SSEEvent{Type: ev.Type, Data: ev.Data}:
			default:
			}
		}
	}()

	return outCh, unsub
}

func (r *telegramRuntime) SSEBrokerAvailable() bool {
	return r.state != nil && r.state.broker != nil
}

// --- Sessions ---

func (r *telegramRuntime) GetOrCreateChannelSession(platform, key, agent, title string) (*tgbot.ChannelSession, error) {
	sess, err := getOrCreateChannelSession(r.cfg.HistoryDB, platform, key, agent, title)
	if err != nil || sess == nil {
		return nil, err
	}
	return &tgbot.ChannelSession{
		ID:            sess.ID,
		MessageCount:  sess.MessageCount,
		TotalTokensIn: float64(sess.TotalTokensIn),
	}, nil
}

func (r *telegramRuntime) ArchiveChannelSession(key string) error {
	return archiveChannelSession(r.cfg.HistoryDB, key)
}

func (r *telegramRuntime) ChannelSessionKey(platform, agent string) string {
	return channelSessionKey(platform, agent)
}

func (r *telegramRuntime) WrapWithContext(sessionCtx, prompt string) string {
	return wrapWithContext(sessionCtx, prompt)
}

// --- Provider ---

func (r *telegramRuntime) ProviderHasNativeSession(agent string) bool {
	providerName := resolveProviderName(r.cfg, Task{Agent: agent}, agent)
	return providerHasNativeSession(providerName)
}

// --- File Uploads ---

func (r *telegramRuntime) SaveFileUpload(telegramToken, fileID, hint string) (filename string, data []byte, err error) {
	// Step 1: Get file path from Telegram.
	getURL := fmt.Sprintf("https://api.tgbot.org/bot%s/getFile?file_id=%s", telegramToken, fileID)
	resp, err := http.Get(getURL)
	if err != nil {
		return "", nil, fmt.Errorf("getFile request: %w", err)
	}
	defer resp.Body.Close()

	var getResult struct {
		OK     bool `json:"ok"`
		Result struct {
			FilePath string `json:"file_path"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&getResult); err != nil {
		return "", nil, fmt.Errorf("decode getFile response: %w", err)
	}
	if !getResult.OK || getResult.Result.FilePath == "" {
		return "", nil, fmt.Errorf("telegram getFile failed for file_id=%s", fileID)
	}

	// Step 2: Download the file content.
	downloadURL := fmt.Sprintf("https://api.tgbot.org/file/bot%s/%s", telegramToken, getResult.Result.FilePath)
	fileResp, err := http.Get(downloadURL)
	if err != nil {
		return "", nil, fmt.Errorf("download file: %w", err)
	}
	defer fileResp.Body.Close()
	if fileResp.StatusCode != 200 {
		return "", nil, fmt.Errorf("download file: status %d", fileResp.StatusCode)
	}

	content, err := io.ReadAll(fileResp.Body)
	if err != nil {
		return "", nil, fmt.Errorf("read file content: %w", err)
	}

	// Use hint as filename if provided, otherwise derive from path.
	name := hint
	if name == "" {
		name = filepath.Base(getResult.Result.FilePath)
	}

	// Save to uploads dir.
	uploadDir := upload.InitDir(r.cfg.baseDir)
	f, err := upload.Save(uploadDir, name, bytes.NewReader(content), int64(len(content)), "telegram")
	if err != nil {
		return "", nil, fmt.Errorf("save upload: %w", err)
	}

	return f.Name, content, nil
}

func (r *telegramRuntime) SaveUploadedFile(filename string, data []byte, source string) (path string, err error) {
	uploadDir := upload.InitDir(r.cfg.baseDir)
	f, err := upload.Save(uploadDir, filename, bytes.NewReader(data), int64(len(data)), source)
	if err != nil {
		return "", err
	}
	return f.Path, nil
}

// --- Formatting ---

func (r *telegramRuntime) FormatResultCostFooter(result *messaging.TaskResult) string {
	if result == nil {
		return ""
	}
	rootResult := &TaskResult{
		TokensIn:  int(result.TokensIn),
		TokensOut: int(result.TokensOut),
		CostUSD:   result.CostUSD,
		DurationMs: result.DurationMs,
	}
	return formatResultCostFooter(r.cfg, rootResult)
}

// --- Agent Models ---

func (r *telegramRuntime) AgentModels() map[string]string {
	return r.messagingRuntime.AgentModels()
}

func (r *telegramRuntime) UpdateAgentModelByName(agent, model string) (old string, err error) {
	return updateAgentModel(r.cfg, agent, model)
}

func (r *telegramRuntime) DefaultSmartDispatchAgent() string {
	return r.cfg.SmartDispatch.DefaultAgent
}

// --- Session Recording ---

func (r *telegramRuntime) RecordAndCompact(sessID string, msgCount int, tokensIn float64, userMsg, assistantMsg string, result *messaging.TaskResult) {
	dbPath := r.cfg.HistoryDB
	now := time.Now().Format(time.RFC3339)

	addSessionMessage(dbPath, SessionMessage{ //nolint:errcheck
		SessionID: sessID,
		Role:      "user",
		Content:   truncateStr(userMsg, 5000),
		CreatedAt: now,
	})

	if result != nil {
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
		addSessionMessage(dbPath, SessionMessage{ //nolint:errcheck
			SessionID: sessID,
			Role:      msgRole,
			Content:   content,
			CostUSD:   result.CostUSD,
			TokensIn:  int(result.TokensIn),
			TokensOut: int(result.TokensOut),
			Model:     result.Model,
			TaskID:    result.TaskID,
			CreatedAt: now,
		})
		updateSessionStats(dbPath, sessID, result.CostUSD, int(result.TokensIn), int(result.TokensOut), 1) //nolint:errcheck
	}

	maybeCompactSession(r.cfg, dbPath, sessID, msgCount+2, int(tokensIn), r.sem, r.childSem)
}

// --- UUID ---

func (r *telegramRuntime) NewUUID() string {
	return newUUID()
}

// --- Retry / Reroute ---

func (r *telegramRuntime) RetryTask(ctx context.Context, taskID string) (*tgbot.RetryResult, error) {
	result, err := retryTask(ctx, r.cfg, taskID, r.state, r.sem, r.childSem)
	if err != nil {
		return nil, err
	}
	return &tgbot.RetryResult{
		TaskID:     result.ID,
		Name:       result.Name,
		Status:     result.Status,
		Output:     result.Output,
		Error:      result.Error,
		CostUSD:    result.CostUSD,
		DurationMs: result.DurationMs,
	}, nil
}

func (r *telegramRuntime) RerouteTask(ctx context.Context, taskID string) (*tgbot.SmartDispatchResult, error) {
	result, err := rerouteTask(ctx, r.cfg, taskID, r.state, r.sem, r.childSem)
	if err != nil {
		return nil, err
	}
	sdr := &tgbot.SmartDispatchResult{
		Route: tgbot.RouteResult{
			Agent:      result.Route.Agent,
			Method:     result.Route.Method,
			Confidence: result.Route.Confidence,
		},
		Task: messaging.TaskResult{
			Output:     result.Task.Output,
			Error:      result.Task.Error,
			Status:     result.Task.Status,
			CostUSD:    result.Task.CostUSD,
			TokensIn:   float64(result.Task.TokensIn),
			TokensOut:  float64(result.Task.TokensOut),
			Model:      result.Task.Model,
			OutputFile: result.Task.OutputFile,
			TaskID:     result.Task.ID,
			DurationMs: result.Task.DurationMs,
		},
	}
	if result.ReviewOK != nil {
		sdr.ReviewOK = result.ReviewOK
		sdr.Review = result.Review
	}
	return sdr, nil
}

// --- Root compatibility: types and functions still referenced from root package ---

// tgInlineButton is a type alias for the internal tgbot.InlineButton.
type tgInlineButton = tgbot.InlineButton

// formatTelegramResult formats a root DispatchResult for Telegram notification.
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
	url := fmt.Sprintf("https://api.tgbot.org/bot%s/sendMessage", cfg.BotToken)
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
