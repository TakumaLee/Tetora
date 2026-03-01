package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// --- P18.3: Workflow Trigger Engine ---

// WorkflowTriggerConfig defines a trigger that automatically executes a workflow.
type WorkflowTriggerConfig struct {
	Name         string            `json:"name"`
	WorkflowName string            `json:"workflowName"`
	Enabled      *bool             `json:"enabled,omitempty"`
	Trigger      TriggerSpec       `json:"trigger"`
	Variables    map[string]string `json:"variables,omitempty"`
	Cooldown     string            `json:"cooldown,omitempty"` // e.g. "5m", "1h"
}

func (t WorkflowTriggerConfig) isEnabled() bool {
	return t.Enabled == nil || *t.Enabled
}

// TriggerSpec defines when and how a trigger fires.
type TriggerSpec struct {
	Type    string `json:"type"`              // "cron", "event", "webhook"
	Cron    string `json:"cron,omitempty"`     // cron expression (5-field)
	TZ      string `json:"tz,omitempty"`       // timezone for cron
	Event   string `json:"event,omitempty"`    // SSE event type to match
	Webhook string `json:"webhook,omitempty"`  // webhook path suffix
}

// TriggerInfo provides status information about a configured trigger.
type TriggerInfo struct {
	Name         string `json:"name"`
	WorkflowName string `json:"workflowName"`
	Type         string `json:"type"`
	Enabled      bool   `json:"enabled"`
	LastFired    string `json:"lastFired,omitempty"`
	NextCron     string `json:"nextCron,omitempty"`
	Cooldown     string `json:"cooldown,omitempty"`
	CooldownLeft string `json:"cooldownLeft,omitempty"`
}

// --- Trigger Engine ---

// WorkflowTriggerEngine manages workflow triggers: cron-based, event-based, and webhook-based.
type WorkflowTriggerEngine struct {
	cfg       *Config
	state     *dispatchState
	sem       chan struct{}
	childSem  chan struct{}
	broker    *sseBroker
	triggers  []WorkflowTriggerConfig
	cooldowns map[string]time.Time // trigger name -> cooldown expiry
	lastFired map[string]time.Time // trigger name -> last fire time
	mu        sync.RWMutex
	ctx       context.Context    // engine-scoped context, cancelled on Stop
	cancel    context.CancelFunc
	stopCh    chan struct{}
	wg        sync.WaitGroup
}

func newWorkflowTriggerEngine(cfg *Config, state *dispatchState, sem, childSem chan struct{}, broker *sseBroker) *WorkflowTriggerEngine {
	e := &WorkflowTriggerEngine{
		cfg:       cfg,
		state:     state,
		sem:       sem,
		childSem:  childSem,
		broker:    broker,
		triggers:  cfg.WorkflowTriggers,
		cooldowns: make(map[string]time.Time),
		lastFired: make(map[string]time.Time),
		stopCh:    make(chan struct{}),
	}
	return e
}

// Start launches the cron loop and event listener goroutines.
func (e *WorkflowTriggerEngine) Start(ctx context.Context) {
	e.ctx, e.cancel = context.WithCancel(ctx)

	if len(e.triggers) == 0 {
		logInfo("workflow trigger engine: no triggers configured")
		return
	}

	hasCron := false
	hasEvent := false
	for _, t := range e.triggers {
		if t.Trigger.Type == "cron" {
			hasCron = true
		}
		if t.Trigger.Type == "event" {
			hasEvent = true
		}
	}

	if hasCron {
		e.wg.Add(1)
		go func() {
			defer e.wg.Done()
			e.cronLoop(ctx)
		}()
	}

	if hasEvent && e.broker != nil {
		e.wg.Add(1)
		go func() {
			defer e.wg.Done()
			e.eventLoop(ctx)
		}()
	}

	// Init trigger runs table.
	initTriggerRunsTable(e.cfg.HistoryDB)

	enabled := 0
	for _, t := range e.triggers {
		if t.isEnabled() {
			enabled++
		}
	}
	logInfo("workflow trigger engine started", "total", len(e.triggers), "enabled", enabled, "cron", hasCron, "event", hasEvent)
}

// Stop gracefully shuts down the trigger engine.
func (e *WorkflowTriggerEngine) Stop() {
	close(e.stopCh)
	if e.cancel != nil {
		e.cancel()
	}
	e.wg.Wait()
	logInfo("workflow trigger engine stopped")
}

// cronLoop checks cron triggers every 30 seconds.
func (e *WorkflowTriggerEngine) cronLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	cleanupCounter := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-e.stopCh:
			return
		case <-ticker.C:
			e.checkCronTriggers(ctx)

			// Clean up expired cooldown entries every ~5 minutes (10 ticks × 30s).
			cleanupCounter++
			if cleanupCounter >= 10 {
				cleanupCounter = 0
				e.cleanupExpiredCooldowns()
			}
		}
	}
}

// cleanupExpiredCooldowns removes expired entries from the cooldowns map to prevent memory leaks.
func (e *WorkflowTriggerEngine) cleanupExpiredCooldowns() {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now()
	for k, v := range e.cooldowns {
		if now.After(v) {
			delete(e.cooldowns, k)
		}
	}
}

func (e *WorkflowTriggerEngine) checkCronTriggers(ctx context.Context) {
	now := time.Now()

	e.mu.RLock()
	triggers := e.triggers
	e.mu.RUnlock()

	for _, t := range triggers {
		if !t.isEnabled() || t.Trigger.Type != "cron" || t.Trigger.Cron == "" {
			continue
		}

		expr, err := parseCronExpr(t.Trigger.Cron)
		if err != nil {
			logWarn("workflow trigger bad cron", "trigger", t.Name, "cron", t.Trigger.Cron, "error", err)
			continue
		}

		// Resolve timezone.
		loc := time.Local
		if t.Trigger.TZ != "" {
			if l, err := time.LoadLocation(t.Trigger.TZ); err == nil {
				loc = l
			}
		}

		nowLocal := now.In(loc)
		if !expr.matches(nowLocal) {
			continue
		}

		// Avoid double-firing in the same minute.
		e.mu.RLock()
		lastFired := e.lastFired[t.Name]
		e.mu.RUnlock()

		if !lastFired.IsZero() &&
			lastFired.In(loc).Truncate(time.Minute).Equal(nowLocal.Truncate(time.Minute)) {
			continue
		}

		// Check cooldown.
		if !e.checkCooldown(t.Name) {
			logDebug("workflow trigger cooldown active", "trigger", t.Name)
			continue
		}

		logInfo("workflow trigger cron firing", "trigger", t.Name, "workflow", t.WorkflowName)
		go e.executeTrigger(ctx, t, nil)
	}
}

// eventLoop subscribes to all SSE events and matches event triggers.
func (e *WorkflowTriggerEngine) eventLoop(ctx context.Context) {
	// Subscribe to a global event channel.
	ch, unsub := e.broker.Subscribe("_triggers")
	defer unsub()

	for {
		select {
		case <-ctx.Done():
			return
		case <-e.stopCh:
			return
		case event, ok := <-ch:
			if !ok {
				return
			}
			e.matchEventTriggers(ctx, event)
		}
	}
}

func (e *WorkflowTriggerEngine) matchEventTriggers(ctx context.Context, event SSEEvent) {
	e.mu.RLock()
	triggers := e.triggers
	e.mu.RUnlock()

	for _, t := range triggers {
		if !t.isEnabled() || t.Trigger.Type != "event" || t.Trigger.Event == "" {
			continue
		}

		// Match event type (supports prefix matching with *)
		if !matchEventType(event.Type, t.Trigger.Event) {
			continue
		}

		if !e.checkCooldown(t.Name) {
			continue
		}

		// Build extra vars from event data.
		extraVars := map[string]string{
			"event_type": event.Type,
			"task_id":    event.TaskID,
			"session_id": event.SessionID,
		}
		if data, ok := event.Data.(map[string]any); ok {
			for k, v := range data {
				extraVars["event_"+k] = fmt.Sprintf("%v", v)
			}
		}

		logInfo("workflow trigger event firing", "trigger", t.Name, "eventType", event.Type)
		go e.executeTrigger(ctx, t, extraVars)
	}
}

// matchEventType checks if an event type matches a pattern.
// Supports exact match and wildcard prefix (e.g. "workflow_*").
func matchEventType(eventType, pattern string) bool {
	if pattern == eventType {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(eventType, prefix)
	}
	return false
}

// HandleWebhookTrigger fires a webhook trigger by name with the given payload.
func (e *WorkflowTriggerEngine) HandleWebhookTrigger(triggerName string, payload map[string]string) error {
	e.mu.Lock()
	var found *WorkflowTriggerConfig
	for i := range e.triggers {
		t := &e.triggers[i]
		if t.Name == triggerName && t.Trigger.Type == "webhook" {
			found = t
			break
		}
	}

	if found == nil {
		e.mu.Unlock()
		return fmt.Errorf("webhook trigger %q not found", triggerName)
	}
	if !found.isEnabled() {
		e.mu.Unlock()
		return fmt.Errorf("webhook trigger %q is disabled", triggerName)
	}

	// Check cooldown under write lock to prevent TOCTOU race.
	expiry, ok := e.cooldowns[triggerName]
	if ok && !time.Now().After(expiry) {
		e.mu.Unlock()
		return fmt.Errorf("webhook trigger %q is in cooldown", triggerName)
	}

	// Set cooldown immediately before releasing lock.
	if found.Cooldown != "" {
		if d, err := time.ParseDuration(found.Cooldown); err == nil {
			e.cooldowns[triggerName] = time.Now().Add(d)
		} else {
			logWarn("webhook trigger cooldown parse failed", "trigger", triggerName, "cooldown", found.Cooldown, "error", err)
		}
	}
	e.lastFired[triggerName] = time.Now()
	triggerCopy := *found
	e.mu.Unlock()

	logInfo("workflow trigger webhook firing", "trigger", triggerName, "workflow", triggerCopy.WorkflowName)
	go e.executeTrigger(e.ctx, triggerCopy, payload)
	return nil
}

// executeTrigger loads the workflow, merges variables, and executes it.
func (e *WorkflowTriggerEngine) executeTrigger(ctx context.Context, trigger WorkflowTriggerConfig, extraVars map[string]string) {
	startedAt := time.Now()

	// Update last fired and cooldown.
	e.mu.Lock()
	e.lastFired[trigger.Name] = startedAt
	if trigger.Cooldown != "" {
		if d, err := time.ParseDuration(trigger.Cooldown); err == nil {
			e.cooldowns[trigger.Name] = startedAt.Add(d)
		}
	}
	e.mu.Unlock()

	// Load workflow.
	wf, err := loadWorkflowByName(e.cfg, trigger.WorkflowName)
	if err != nil {
		errMsg := fmt.Sprintf("load workflow: %v", err)
		logError("workflow trigger exec failed", "trigger", trigger.Name, "error", errMsg)
		recordTriggerRun(e.cfg.HistoryDB, trigger.Name, trigger.WorkflowName, "", "error",
			startedAt.Format(time.RFC3339), time.Now().Format(time.RFC3339), errMsg)
		return
	}

	// Validate workflow.
	if errs := validateWorkflow(wf); len(errs) > 0 {
		errMsg := fmt.Sprintf("validation: %s", strings.Join(errs, "; "))
		logError("workflow trigger validation failed", "trigger", trigger.Name, "errors", errs)
		recordTriggerRun(e.cfg.HistoryDB, trigger.Name, trigger.WorkflowName, "", "error",
			startedAt.Format(time.RFC3339), time.Now().Format(time.RFC3339), errMsg)
		return
	}

	// Merge variables: workflow defaults < trigger config < extra vars.
	vars := make(map[string]string)
	for k, v := range wf.Variables {
		vars[k] = v
	}
	for k, v := range trigger.Variables {
		vars[k] = v
	}
	for k, v := range extraVars {
		vars[k] = v
	}

	// Add trigger metadata as variables.
	vars["_trigger_name"] = trigger.Name
	vars["_trigger_type"] = trigger.Trigger.Type
	vars["_trigger_time"] = startedAt.Format(time.RFC3339)

	// Execute workflow.
	run := executeWorkflow(ctx, e.cfg, wf, vars, e.state, e.sem, e.childSem)

	// Record trigger run.
	status := "success"
	errMsg := ""
	if run.Status != "success" {
		status = "error"
		errMsg = run.Error
	}
	recordTriggerRun(e.cfg.HistoryDB, trigger.Name, trigger.WorkflowName, run.ID, status,
		startedAt.Format(time.RFC3339), time.Now().Format(time.RFC3339), errMsg)

	// Publish trigger event.
	if e.broker != nil {
		e.broker.Publish("_triggers", SSEEvent{
			Type: "trigger_fired",
			Data: map[string]any{
				"trigger":      trigger.Name,
				"workflow":     trigger.WorkflowName,
				"runId":        run.ID,
				"status":       run.Status,
				"triggerType":  trigger.Trigger.Type,
				"durationMs":   run.DurationMs,
			},
		})
	}
}

// checkCooldown returns true if the trigger is past its cooldown period.
func (e *WorkflowTriggerEngine) checkCooldown(triggerName string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()

	expiry, ok := e.cooldowns[triggerName]
	if !ok {
		return true // no cooldown set
	}
	return time.Now().After(expiry)
}

// ListTriggers returns status info for all configured triggers.
func (e *WorkflowTriggerEngine) ListTriggers() []TriggerInfo {
	e.mu.RLock()
	defer e.mu.RUnlock()

	var infos []TriggerInfo
	now := time.Now()

	for _, t := range e.triggers {
		info := TriggerInfo{
			Name:         t.Name,
			WorkflowName: t.WorkflowName,
			Type:         t.Trigger.Type,
			Enabled:      t.isEnabled(),
			Cooldown:     t.Cooldown,
		}

		// Last fired.
		if lf, ok := e.lastFired[t.Name]; ok {
			info.LastFired = lf.Format(time.RFC3339)
		}

		// Cooldown remaining.
		if expiry, ok := e.cooldowns[t.Name]; ok && now.Before(expiry) {
			info.CooldownLeft = expiry.Sub(now).Round(time.Second).String()
		}

		// Next cron run.
		if t.Trigger.Type == "cron" && t.Trigger.Cron != "" {
			expr, err := parseCronExpr(t.Trigger.Cron)
			if err == nil {
				loc := time.Local
				if t.Trigger.TZ != "" {
					if l, err := time.LoadLocation(t.Trigger.TZ); err == nil {
						loc = l
					}
				}
				next := nextRunAfter(expr, loc, now.In(loc))
				if !next.IsZero() {
					info.NextCron = next.Format(time.RFC3339)
				}
			}
		}

		infos = append(infos, info)
	}

	return infos
}

// FireTrigger manually fires a trigger by name.
func (e *WorkflowTriggerEngine) FireTrigger(name string) error {
	e.mu.RLock()
	var found *WorkflowTriggerConfig
	for i := range e.triggers {
		if e.triggers[i].Name == name {
			found = &e.triggers[i]
			break
		}
	}
	e.mu.RUnlock()

	if found == nil {
		return fmt.Errorf("trigger %q not found", name)
	}
	if !found.isEnabled() {
		return fmt.Errorf("trigger %q is disabled", name)
	}

	logInfo("workflow trigger manual fire", "trigger", name, "workflow", found.WorkflowName)
	go e.executeTrigger(e.ctx, *found, map[string]string{
		"_manual": "true",
	})
	return nil
}

// --- Trigger Run Recording ---

const triggerRunsTableSQL = `CREATE TABLE IF NOT EXISTS workflow_trigger_runs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	trigger_name TEXT NOT NULL,
	workflow_name TEXT NOT NULL,
	workflow_run_id TEXT DEFAULT '',
	status TEXT NOT NULL DEFAULT 'started',
	started_at TEXT NOT NULL,
	finished_at TEXT DEFAULT '',
	error TEXT DEFAULT ''
)`

func initTriggerRunsTable(dbPath string) {
	if dbPath == "" {
		return
	}
	// Migration: add workflow_run_id column if missing (for DBs created before this column existed).
	if err := execDB(dbPath, `ALTER TABLE workflow_trigger_runs ADD COLUMN workflow_run_id TEXT DEFAULT '';`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") && !strings.Contains(err.Error(), "no such table") {
			logWarn("workflow_trigger_runs migration failed", "error", err)
		}
	}
	if _, err := queryDB(dbPath, triggerRunsTableSQL); err != nil {
		logWarn("init workflow_trigger_runs table failed", "error", err)
	}
}

func recordTriggerRun(dbPath, triggerName, workflowName, runID, status, startedAt, finishedAt, errMsg string) {
	if dbPath == "" {
		return
	}

	sql := fmt.Sprintf(
		`INSERT INTO workflow_trigger_runs (trigger_name, workflow_name, workflow_run_id, status, started_at, finished_at, error)
		 VALUES ('%s','%s','%s','%s','%s','%s','%s')`,
		escapeSQLite(triggerName),
		escapeSQLite(workflowName),
		escapeSQLite(runID),
		escapeSQLite(status),
		escapeSQLite(startedAt),
		escapeSQLite(finishedAt),
		escapeSQLite(errMsg),
	)

	if _, err := queryDB(dbPath, sql); err != nil {
		logWarn("record trigger run failed", "error", err)
	}
}

func queryTriggerRuns(dbPath, triggerName string, limit int) ([]map[string]any, error) {
	if limit <= 0 {
		limit = 20
	}

	where := ""
	if triggerName != "" {
		where = fmt.Sprintf("WHERE trigger_name='%s'", escapeSQLite(triggerName))
	}

	sql := fmt.Sprintf(
		`SELECT id, trigger_name, workflow_name, workflow_run_id, status, started_at, finished_at, error
		 FROM workflow_trigger_runs %s ORDER BY id DESC LIMIT %d`,
		where, limit,
	)

	rows, err := queryDB(dbPath, sql)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil, nil
		}
		return nil, err
	}

	return rows, nil
}

// --- Variable Expansion for Tool Inputs ---

// expandVars replaces {{key}} with values from the vars map.
// Same pattern as expandSkillVars but used for workflow step tool inputs.
func expandVars(s string, vars map[string]string) string {
	for k, v := range vars {
		s = strings.ReplaceAll(s, "{{"+k+"}}", v)
	}
	return s
}

// expandToolInput expands {{var}} in all tool input values.
func expandToolInput(input map[string]string, vars map[string]string) map[string]string {
	if len(input) == 0 {
		return input
	}
	result := make(map[string]string, len(input))
	for k, v := range input {
		result[k] = expandVars(v, vars)
	}
	return result
}

// toolInputToJSON converts a map[string]string to json.RawMessage.
func toolInputToJSON(input map[string]string) json.RawMessage {
	if len(input) == 0 {
		return json.RawMessage(`{}`)
	}
	// Convert to map[string]any for JSON marshaling.
	m := make(map[string]any, len(input))
	for k, v := range input {
		m[k] = v
	}
	data, err := json.Marshal(m)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return data
}
