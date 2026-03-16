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

	"tetora/internal/messaging"
	"tetora/internal/trace"
)

// messagingRuntime implements messaging.BotRuntime using root package functions.
type messagingRuntime struct {
	cfg      *Config
	state    *dispatchState
	sem      chan struct{}
	childSem chan struct{}
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
		Prompt: req.Content,
		Agent:  req.AgentRole,
		Source: req.Meta["source"],
	}
	fillDefaults(r.cfg, &task)
	result := runSingleTask(ctx, r.cfg, task, r.sem, r.childSem, req.AgentRole)
	return messaging.TaskResult{
		Output: result.Output,
		Error:  result.Error,
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
	// No-op stub: actual record calls happen inline in each bot handler.
	// Bots in root package call recordHistory() directly.
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
	uploadDir := initUploadDir(r.cfg.baseDir)
	f, err := saveUpload(uploadDir, filename, bytes.NewReader(data), int64(len(data)), "messaging")
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
	logInfo(msg, args...)
}

func (r *messagingRuntime) LogWarn(msg string, args ...interface{}) {
	logWarn(msg, args...)
}

func (r *messagingRuntime) LogError(msg string, err error, args ...interface{}) {
	combined := append([]interface{}{"error", err}, args...)
	logError(msg, combined...)
}

func (r *messagingRuntime) LogInfoCtx(ctx context.Context, msg string, args ...interface{}) {
	logInfoCtx(ctx, msg, args...)
}

func (r *messagingRuntime) LogErrorCtx(ctx context.Context, msg string, err error, args ...interface{}) {
	combined := append([]interface{}{"error", err}, args...)
	logErrorCtx(ctx, msg, combined...)
}

func (r *messagingRuntime) LogDebugCtx(ctx context.Context, msg string, args ...interface{}) {
	logDebugCtx(ctx, msg, args...)
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
