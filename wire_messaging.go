package main

// wire_messaging.go wires the internal/messaging packages to the root package.
// It provides a concrete implementation of the messaging.BotRuntime interface
// that delegates to root package functions, allowing bot implementations to
// remain in the root package while declaring their dependency via an interface.

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"time"

	"tetora/internal/log"
	"tetora/internal/messaging"
	"tetora/internal/trace"
	"tetora/internal/upload"
)

// messagingRuntime implements messaging.BotRuntime using root package functions.
type messagingRuntime struct {
	cfg      *Config
	state    *dispatchState
	sem      chan struct{}
	childSem chan struct{}
	cron     *CronEngine
}

// newMessagingRuntime creates a new messagingRuntime.
func newMessagingRuntime(cfg *Config, state *dispatchState, sem, childSem chan struct{}) *messagingRuntime {
	return &messagingRuntime{
		cfg:      cfg,
		state:    state,
		sem:      sem,
		childSem: childSem,
	}
}

// Ensure messagingRuntime implements BotRuntime at compile time.
var _ messaging.BotRuntime = (*messagingRuntime)(nil)

func (r *messagingRuntime) Submit(ctx context.Context, req messaging.TaskRequest) (messaging.TaskResult, error) {
	task := Task{
		Prompt:         req.Content,
		Agent:          req.AgentRole,
		Source:         req.Meta["source"],
		SessionID:      req.SessionID,
		SystemPrompt:   req.SystemPrompt,
		Model:          req.Model,
		PermissionMode: req.PermissionMode,
	}
	fillDefaults(r.cfg, &task)
	taskStart := time.Now()
	result := runSingleTask(ctx, r.cfg, task, r.sem, r.childSem, req.AgentRole)
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
		DurationMs: time.Since(taskStart).Milliseconds(),
	}, nil
}

func (r *messagingRuntime) Route(ctx context.Context, prompt, source string) (string, error) {
	route := routeTask(ctx, r.cfg, RouteRequest{Prompt: prompt, Source: source})
	if route == nil {
		return "", fmt.Errorf("routing returned nil result")
	}
	return route.Agent, nil
}

func (r *messagingRuntime) GetOrCreateSession(platform, key, agent, title string) (string, error) {
	sess, err := getOrCreateChannelSession(r.cfg.HistoryDB, platform, key, agent, title)
	if err != nil || sess == nil {
		return "", err
	}
	return sess.ID, nil
}

func (r *messagingRuntime) BuildSessionContext(sessionID string, limit int) string {
	return buildSessionContext(r.cfg.HistoryDB, sessionID, limit)
}

func (r *messagingRuntime) AddSessionMessage(sessionID, role, content string) {
	addSessionMessage(r.cfg.HistoryDB, SessionMessage{ //nolint:errcheck
		SessionID: sessionID,
		Role:      role,
		Content:   content,
	})
}

func (r *messagingRuntime) UpdateSessionStats(sessionID string, cost, tokensIn, tokensOut, msgCount float64) {
	updateSessionStats(r.cfg.HistoryDB, sessionID, cost, int(tokensIn), int(tokensOut), int(msgCount)) //nolint:errcheck
}

func (r *messagingRuntime) RecordHistory(taskID, name, source, agent, outputFile string, task, result interface{}) {
	if r.cfg.HistoryDB == "" {
		return
	}
	// Convert interfaces to concrete types if possible; otherwise record with zero values.
	t, _ := task.(Task)
	res, _ := result.(TaskResult)
	startedAt := time.Now().Format(time.RFC3339)
	finishedAt := startedAt
	recordHistory(r.cfg.HistoryDB, taskID, name, source, agent, t, res, startedAt, finishedAt, outputFile)
}

func (r *messagingRuntime) PublishEvent(eventType string, data map[string]interface{}) {
	if r.state != nil && r.state.broker != nil {
		r.state.broker.Publish(eventType, SSEEvent{
			Type: eventType,
			Data: data,
		})
	}
}

func (r *messagingRuntime) IsActive() bool {
	if r.state == nil {
		return false
	}
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	return r.state.active
}

func (r *messagingRuntime) ExpandPrompt(prompt, agent string) string {
	return expandPrompt(prompt, "", r.cfg.HistoryDB, agent, r.cfg.KnowledgeDir, r.cfg)
}

func (r *messagingRuntime) LoadAgentPrompt(agent string) (string, error) {
	return loadAgentPrompt(r.cfg, agent)
}

func (r *messagingRuntime) FillTaskDefaults(agent *string, name *string, source string) string {
	task := Task{Source: source}
	if agent != nil {
		task.Agent = *agent
	}
	if name != nil {
		task.Name = *name
	}
	fillDefaults(r.cfg, &task)
	if agent != nil {
		*agent = task.Agent
	}
	if name != nil {
		*name = task.Name
	}
	return task.ID
}

func (r *messagingRuntime) HistoryDB() string {
	return r.cfg.HistoryDB
}

func (r *messagingRuntime) WorkspaceDir() string {
	return r.cfg.WorkspaceDir
}

func (r *messagingRuntime) SaveUpload(filename string, data []byte) (string, error) {
	uploadDir := upload.InitDir(r.cfg.BaseDir)
	f, err := upload.Save(uploadDir, filename, bytes.NewReader(data), int64(len(data)), "messaging")
	if err != nil {
		return "", err
	}
	return f.Path, nil
}

func (r *messagingRuntime) Truncate(s string, maxLen int) string {
	return truncate(s, maxLen)
}

func (r *messagingRuntime) NewTraceID(source string) string {
	return trace.NewID(source)
}

func (r *messagingRuntime) WithTraceID(ctx context.Context, traceID string) context.Context {
	return trace.WithID(ctx, traceID)
}

func (r *messagingRuntime) LogInfo(msg string, args ...interface{}) {
	log.Info(msg, args...)
}

func (r *messagingRuntime) LogWarn(msg string, args ...interface{}) {
	log.Warn(msg, args...)
}

func (r *messagingRuntime) LogError(msg string, err error, args ...interface{}) {
	combined := append([]interface{}{"error", err}, args...)
	log.Error(msg, combined...)
}

func (r *messagingRuntime) LogInfoCtx(ctx context.Context, msg string, args ...interface{}) {
	log.InfoCtx(ctx, msg, args...)
}

func (r *messagingRuntime) LogErrorCtx(ctx context.Context, msg string, err error, args ...interface{}) {
	combined := append([]interface{}{"error", err}, args...)
	log.ErrorCtx(ctx, msg, combined...)
}

func (r *messagingRuntime) LogDebugCtx(ctx context.Context, msg string, args ...interface{}) {
	log.DebugCtx(ctx, msg, args...)
}

func (r *messagingRuntime) ClientIP(req *http.Request) string {
	return clientIP(req)
}

func (r *messagingRuntime) AuditLog(action, source, target, ip string) {
	auditLog(r.cfg.HistoryDB, action, source, target, ip)
}

func (r *messagingRuntime) QueryCostStats() (today, week, month float64) {
	stats, err := queryCostStats(r.cfg.HistoryDB)
	if err != nil {
		return 0, 0, 0
	}
	return stats.Today, stats.Week, stats.Month
}

func (r *messagingRuntime) UpdateAgentModel(agent, model string) error {
	_, err := updateAgentModel(r.cfg, agent, model)
	return err
}

func (r *messagingRuntime) MaybeCompactSession(sessionID string, msgCount int, tokenCount float64) {
	maybeCompactSession(r.cfg, r.cfg.HistoryDB, sessionID, msgCount, int(tokenCount), r.sem, r.childSem)
}

func (r *messagingRuntime) UpdateSessionTitle(sessionID, title string) {
	updateSessionTitle(r.cfg.HistoryDB, sessionID, title) //nolint:errcheck
}

func (r *messagingRuntime) SessionContextLimit() int {
	return r.cfg.Session.ContextMessagesOrDefault()
}

func (r *messagingRuntime) AgentConfig(agent string) (model, permMode string, found bool) {
	rc, ok := r.cfg.Agents[agent]
	if !ok {
		return "", "", false
	}
	return rc.Model, rc.PermissionMode, true
}

func (r *messagingRuntime) ArchiveSession(channelKey string) error {
	return archiveChannelSession(r.cfg.HistoryDB, channelKey)
}

func (r *messagingRuntime) SetMemory(agent, key, value string) {
	setMemory(r.cfg, agent, key, value)
}

func (r *messagingRuntime) SendWebhooks(status string, payload map[string]interface{}) {
	wp := WebhookPayload{}
	if v, ok := payload["job_id"].(string); ok {
		wp.JobID = v
	}
	if v, ok := payload["name"].(string); ok {
		wp.Name = v
	}
	if v, ok := payload["source"].(string); ok {
		wp.Source = v
	}
	if v, ok := payload["status"].(string); ok {
		wp.Status = v
	}
	if v, ok := payload["cost"].(float64); ok {
		wp.Cost = v
	}
	if v, ok := payload["duration"].(int64); ok {
		wp.Duration = v
	}
	if v, ok := payload["model"].(string); ok {
		wp.Model = v
	}
	if v, ok := payload["output"].(string); ok {
		wp.Output = v
	}
	if v, ok := payload["error"].(string); ok {
		wp.Error = v
	}
	sendWebhooks(r.cfg, status, wp)
}

func (r *messagingRuntime) StatusJSON() []byte {
	if r.state == nil {
		return []byte("{}")
	}
	return r.state.statusJSON()
}

func (r *messagingRuntime) ListCronJobs() []messaging.CronJobInfo {
	if r.cron == nil {
		return nil
	}
	jobs := r.cron.ListJobs()
	result := make([]messaging.CronJobInfo, len(jobs))
	for i, j := range jobs {
		nextRun := ""
		if !j.NextRun.IsZero() {
			nextRun = j.NextRun.Format(time.RFC3339)
		}
		result[i] = messaging.CronJobInfo{
			Name:     j.Name,
			Schedule: j.Schedule,
			Enabled:  j.Enabled,
			Running:  j.Running,
			NextRun:  nextRun,
			AvgCost:  j.AvgCost,
		}
	}
	return result
}

func (r *messagingRuntime) SmartDispatchEnabled() bool {
	return r.cfg.SmartDispatch.Enabled
}

func (r *messagingRuntime) DefaultAgent() string {
	return r.cfg.SmartDispatch.DefaultAgent
}

func (r *messagingRuntime) DefaultModel() string {
	return r.cfg.DefaultModel
}

func (r *messagingRuntime) CostAlertDailyLimit() float64 {
	return r.cfg.CostAlert.DailyLimit
}

func (r *messagingRuntime) ApprovalGatesEnabled() bool {
	return r.cfg.ApprovalGates.Enabled
}

func (r *messagingRuntime) ApprovalGatesAutoApproveTools() []string {
	return r.cfg.ApprovalGates.AutoApproveTools
}

func (r *messagingRuntime) ProviderHasNativeSession(agent string) bool {
	providerName := resolveProviderName(r.cfg, Task{Agent: agent}, agent)
	return providerHasNativeSession(providerName)
}

func (r *messagingRuntime) DownloadFile(url, filename, authHeader string) (string, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("HTTP %d downloading %s", resp.StatusCode, filename)
	}
	uploadDir := upload.InitDir(r.cfg.BaseDir)
	f, err := upload.Save(uploadDir, filename, resp.Body, resp.ContentLength, "messaging")
	if err != nil {
		return "", err
	}
	return f.Path, nil
}

func (r *messagingRuntime) BuildFilePromptPrefix(filePaths []string) string {
	var files []*upload.File
	for _, p := range filePaths {
		files = append(files, &upload.File{Path: p})
	}
	return upload.BuildPromptPrefix(files)
}

func (r *messagingRuntime) AgentModels() map[string]string {
	result := make(map[string]string)
	for name, rc := range r.cfg.Agents {
		m := rc.Model
		if m == "" {
			m = r.cfg.DefaultModel
		}
		result[name] = m
	}
	return result
}
