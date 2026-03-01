package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// --- Proactive Engine Types ---

// ProactiveEngine manages proactive rules (scheduled, event-driven, threshold-based).
type ProactiveEngine struct {
	rules     []ProactiveRule
	cfg       *Config
	broker    *sseBroker
	mu        sync.RWMutex
	ctx       context.Context
	cancel    context.CancelFunc
	cooldowns map[string]time.Time // rule name → last triggered
	stopCh    chan struct{}
	wg        sync.WaitGroup
	sem       chan struct{} // shared semaphore for top-level tasks
	childSem  chan struct{} // shared semaphore for child tasks
}

// ProactiveRule defines a trigger → action → delivery pipeline.
type ProactiveRule struct {
	Name     string            `json:"name"`
	Trigger  ProactiveTrigger  `json:"trigger"`
	Action   ProactiveAction   `json:"action"`
	Delivery ProactiveDelivery `json:"delivery"`
	Cooldown string            `json:"cooldown,omitempty"` // e.g. "1h", "30m"
	Enabled  *bool             `json:"enabled,omitempty"`  // default true
}

// isEnabled returns true if the rule is enabled (default true).
func (r ProactiveRule) isEnabled() bool {
	return r.Enabled == nil || *r.Enabled
}

// ProactiveTrigger defines when a rule fires.
type ProactiveTrigger struct {
	Type     string  `json:"type"` // "schedule", "event", "threshold", "heartbeat"
	Cron     string  `json:"cron,omitempty"`     // for schedule type
	TZ       string  `json:"tz,omitempty"`       // timezone
	Event    string  `json:"event,omitempty"`    // for event type (SSE event type match)
	Metric   string  `json:"metric,omitempty"`   // for threshold type
	Op       string  `json:"op,omitempty"`       // ">", "<", ">=", "<=", "=="
	Value    float64 `json:"value,omitempty"`    // threshold value
	Interval string  `json:"interval,omitempty"` // for heartbeat type, e.g. "30m"
}

// ProactiveAction defines what happens when a rule triggers.
type ProactiveAction struct {
	Type           string                 `json:"type"` // "dispatch", "notify"
	Agent          string                 `json:"agent,omitempty"`
	Prompt         string                 `json:"prompt,omitempty"`
	PromptTemplate string                 `json:"promptTemplate,omitempty"`
	Params         map[string]interface{} `json:"params,omitempty"`
	Message        string                 `json:"message,omitempty"` // for notify type, supports {{.Var}} templates
	Autonomous     bool                   `json:"autonomous,omitempty"` // if true, agent decides what to do based on context
}

// ProactiveDelivery defines where to send the result.
type ProactiveDelivery struct {
	Channel string `json:"channel"` // "telegram", "slack", "discord", "dashboard"
	ChatID  int64  `json:"chatId,omitempty"` // for telegram
}

// ProactiveRuleInfo is the public view of a rule (for API).
type ProactiveRuleInfo struct {
	Name          string    `json:"name"`
	Enabled       bool      `json:"enabled"`
	TriggerType   string    `json:"triggerType"`
	LastTriggered time.Time `json:"lastTriggered,omitempty"`
	NextRun       time.Time `json:"nextRun,omitempty"`
	Cooldown      string    `json:"cooldown,omitempty"`
}

// --- Engine Lifecycle ---

// newProactiveEngine creates a new proactive engine instance.
func newProactiveEngine(cfg *Config, broker *sseBroker, sem, childSem chan struct{}) *ProactiveEngine {
	return &ProactiveEngine{
		rules:     cfg.Proactive.Rules,
		cfg:       cfg,
		broker:    broker,
		cooldowns: make(map[string]time.Time),
		stopCh:    make(chan struct{}),
		sem:       sem,
		childSem:  childSem,
	}
}

// Start begins all rule evaluators in background goroutines.
func (e *ProactiveEngine) Start(ctx context.Context) {
	e.ctx, e.cancel = context.WithCancel(ctx)

	logInfo("proactive engine starting", "rules", len(e.rules))

	// Start schedule loop (checks cron rules every 30s).
	e.wg.Add(1)
	go e.runScheduleLoop(e.ctx)

	// Start heartbeat loop (checks interval rules every 10s).
	e.wg.Add(1)
	go e.runHeartbeatLoop(e.ctx)

	// Threshold checking is polled by schedule loop.
	logInfo("proactive engine started")
}

// Stop gracefully shuts down the engine.
func (e *ProactiveEngine) Stop() {
	if e.cancel != nil {
		e.cancel()
	}
	close(e.stopCh)
	e.wg.Wait()
	logInfo("proactive engine stopped")
}

// --- Trigger Evaluators ---

// runScheduleLoop checks cron-based rules every 30s.
func (e *ProactiveEngine) runScheduleLoop(ctx context.Context) {
	defer e.wg.Done()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	logDebug("proactive schedule loop started")

	for {
		select {
		case <-ctx.Done():
			return
		case <-e.stopCh:
			return
		case <-ticker.C:
			e.checkScheduleRules(ctx)
			e.checkThresholdRules(ctx)
		}
	}
}

// runHeartbeatLoop checks interval-based rules every 10s.
func (e *ProactiveEngine) runHeartbeatLoop(ctx context.Context) {
	defer e.wg.Done()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	logDebug("proactive heartbeat loop started")

	for {
		select {
		case <-ctx.Done():
			return
		case <-e.stopCh:
			return
		case <-ticker.C:
			e.checkHeartbeatRules(ctx)
		}
	}
}

// checkScheduleRules evaluates all schedule-type rules.
func (e *ProactiveEngine) checkScheduleRules(ctx context.Context) {
	e.mu.RLock()
	rules := e.rules
	e.mu.RUnlock()

	for _, rule := range rules {
		if !rule.isEnabled() || rule.Trigger.Type != "schedule" {
			continue
		}

		if e.checkCooldown(rule.Name) {
			continue // still in cooldown
		}

		if e.matchesSchedule(rule) {
			logInfo("proactive schedule triggered", "rule", rule.Name)
			if err := e.executeAction(ctx, rule); err != nil {
				logError("proactive action failed", "rule", rule.Name, "error", err)
			}
		}
	}
}

// checkHeartbeatRules evaluates all heartbeat-type rules.
func (e *ProactiveEngine) checkHeartbeatRules(ctx context.Context) {
	e.mu.RLock()
	rules := e.rules
	e.mu.RUnlock()

	now := time.Now()
	for _, rule := range rules {
		if !rule.isEnabled() || rule.Trigger.Type != "heartbeat" {
			continue
		}

		interval, err := time.ParseDuration(rule.Trigger.Interval)
		if err != nil {
			continue
		}

		e.mu.Lock()
		lastTriggered, ok := e.cooldowns[rule.Name]
		e.mu.Unlock()

		if !ok || now.Sub(lastTriggered) >= interval {
			logInfo("proactive heartbeat triggered", "rule", rule.Name, "interval", interval)
			if err := e.executeAction(ctx, rule); err != nil {
				logError("proactive action failed", "rule", rule.Name, "error", err)
			}
		}
	}
}

// checkThresholdRules evaluates all threshold-type rules.
func (e *ProactiveEngine) checkThresholdRules(ctx context.Context) {
	e.mu.RLock()
	rules := e.rules
	e.mu.RUnlock()

	for _, rule := range rules {
		if !rule.isEnabled() || rule.Trigger.Type != "threshold" {
			continue
		}

		if e.checkCooldown(rule.Name) {
			continue
		}

		value, err := e.getMetricValue(rule.Trigger.Metric)
		if err != nil {
			logDebug("proactive metric error", "rule", rule.Name, "metric", rule.Trigger.Metric, "error", err)
			continue
		}

		if e.compareThreshold(value, rule.Trigger.Op, rule.Trigger.Value) {
			logInfo("proactive threshold triggered", "rule", rule.Name, "metric", rule.Trigger.Metric, "value", value, "threshold", rule.Trigger.Value)
			if err := e.executeAction(ctx, rule); err != nil {
				logError("proactive action failed", "rule", rule.Name, "error", err)
			}
		}
	}
}

// handleEvent processes incoming SSE events and triggers matching rules.
func (e *ProactiveEngine) handleEvent(event SSEEvent) {
	e.mu.RLock()
	rules := e.rules
	e.mu.RUnlock()

	for _, rule := range rules {
		if !rule.isEnabled() || rule.Trigger.Type != "event" {
			continue
		}

		if rule.Trigger.Event == event.Type {
			if e.checkCooldown(rule.Name) {
				continue
			}

			logInfo("proactive event triggered", "rule", rule.Name, "event", event.Type)
			ctx := context.Background()
			if err := e.executeAction(ctx, rule); err != nil {
				logError("proactive action failed", "rule", rule.Name, "error", err)
			}
		}
	}
}

// --- Schedule Matching ---

// matchesSchedule checks if a schedule rule should fire now.
func (e *ProactiveEngine) matchesSchedule(rule ProactiveRule) bool {
	expr, err := parseCronExpr(rule.Trigger.Cron)
	if err != nil {
		return false
	}

	loc := time.Local
	if rule.Trigger.TZ != "" {
		if l, err := time.LoadLocation(rule.Trigger.TZ); err == nil {
			loc = l
		}
	}

	now := time.Now().In(loc)

	// Check if cron expression matches current minute.
	if !expr.matches(now) {
		return false
	}

	// Avoid double-firing in the same minute.
	e.mu.Lock()
	lastTriggered, ok := e.cooldowns[rule.Name]
	e.mu.Unlock()

	if ok && lastTriggered.In(loc).Truncate(time.Minute).Equal(now.Truncate(time.Minute)) {
		return false
	}

	return true
}

// --- Threshold Comparison ---

// compareThreshold compares a metric value against a threshold using the given operator.
func (e *ProactiveEngine) compareThreshold(value float64, op string, threshold float64) bool {
	switch op {
	case ">":
		return value > threshold
	case "<":
		return value < threshold
	case ">=":
		return value >= threshold
	case "<=":
		return value <= threshold
	case "==":
		return value == threshold
	default:
		return false
	}
}

// getMetricValue retrieves the current value of a named metric.
func (e *ProactiveEngine) getMetricValue(metric string) (float64, error) {
	switch metric {
	case "daily_cost_usd":
		return e.getDailyCost()
	case "queue_depth":
		return e.getQueueDepth()
	case "active_sessions":
		return e.getActiveSessions()
	case "failed_tasks_today":
		return e.getFailedTasksToday()
	default:
		return 0, fmt.Errorf("unknown metric: %s", metric)
	}
}

// getDailyCost returns today's total cost from history DB.
func (e *ProactiveEngine) getDailyCost() (float64, error) {
	if e.cfg.HistoryDB == "" {
		return 0, fmt.Errorf("historyDB not configured")
	}

	today := time.Now().Format("2006-01-02")
	sql := fmt.Sprintf("SELECT COALESCE(SUM(cost_usd), 0) FROM runs WHERE started_at LIKE '%s%%'", escapeSQLite(today))

	rows, err := queryDB(e.cfg.HistoryDB, sql)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}

	// Extract value from first row.
	for _, v := range rows[0] {
		switch val := v.(type) {
		case float64:
			return val, nil
		case int64:
			return float64(val), nil
		}
	}
	return 0, nil
}

// getQueueDepth returns the number of items in the offline queue.
func (e *ProactiveEngine) getQueueDepth() (float64, error) {
	if e.cfg.HistoryDB == "" {
		return 0, fmt.Errorf("historyDB not configured")
	}

	sql := "SELECT COUNT(*) FROM offline_queue"
	rows, err := queryDB(e.cfg.HistoryDB, sql)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}

	for _, v := range rows[0] {
		switch val := v.(type) {
		case int64:
			return float64(val), nil
		case float64:
			return val, nil
		}
	}
	return 0, nil
}

// getActiveSessions returns the count of active sessions.
func (e *ProactiveEngine) getActiveSessions() (float64, error) {
	if e.cfg.HistoryDB == "" {
		return 0, fmt.Errorf("historyDB not configured")
	}

	sql := "SELECT COUNT(DISTINCT session_id) FROM sessions WHERE last_activity > datetime('now', '-1 hour')"
	rows, err := queryDB(e.cfg.HistoryDB, sql)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}

	for _, v := range rows[0] {
		switch val := v.(type) {
		case int64:
			return float64(val), nil
		case float64:
			return val, nil
		}
	}
	return 0, nil
}

// getFailedTasksToday returns the number of failed tasks today.
func (e *ProactiveEngine) getFailedTasksToday() (float64, error) {
	if e.cfg.HistoryDB == "" {
		return 0, fmt.Errorf("historyDB not configured")
	}

	today := time.Now().Format("2006-01-02")
	sql := fmt.Sprintf("SELECT COUNT(*) FROM runs WHERE started_at LIKE '%s%%' AND status != 'success'", escapeSQLite(today))

	rows, err := queryDB(e.cfg.HistoryDB, sql)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}

	for _, v := range rows[0] {
		switch val := v.(type) {
		case int64:
			return float64(val), nil
		case float64:
			return val, nil
		}
	}
	return 0, nil
}

// --- Action Execution ---

// executeAction performs the action defined in a rule.
func (e *ProactiveEngine) executeAction(ctx context.Context, rule ProactiveRule) error {
	// Set cooldown.
	if rule.Cooldown != "" {
		duration, err := time.ParseDuration(rule.Cooldown)
		if err == nil {
			e.setCooldown(rule.Name, duration)
		}
	} else {
		// Default cooldown: 1 minute for schedule/threshold, none for heartbeat.
		if rule.Trigger.Type == "schedule" || rule.Trigger.Type == "threshold" {
			e.setCooldown(rule.Name, time.Minute)
		}
	}

	switch rule.Action.Type {
	case "dispatch":
		return e.actionDispatch(ctx, rule)
	case "notify":
		return e.actionNotify(ctx, rule)
	default:
		return fmt.Errorf("unknown action type: %s", rule.Action.Type)
	}
}

// actionDispatch creates a new task dispatch.
func (e *ProactiveEngine) actionDispatch(ctx context.Context, rule ProactiveRule) error {
	if e.sem == nil {
		logWarn("proactive dispatch skipped: sem not available", "rule", rule.Name)
		return nil
	}

	prompt := rule.Action.Prompt
	if rule.Action.PromptTemplate != "" {
		prompt = e.resolveTemplate(rule.Action.PromptTemplate, rule)
	}

	// Autonomous mode: override prompt with context-rich self-initiative prompt.
	if rule.Action.Autonomous || prompt == "" {
		prompt = e.buildAutonomousPrompt(rule)
	}

	agentName := rule.Action.Agent
	if agentName == "" {
		agentName = "ruri"
	}

	task := Task{
		ID:     generateID("proactive"),
		Name:   fmt.Sprintf("proactive:%s", rule.Name),
		Prompt: prompt,
		Source: "proactive",
		Agent:  agentName,
	}

	logInfo("proactive dispatch action", "rule", rule.Name, "agent", agentName, "taskId", truncate(task.ID, 16), "prompt", truncate(prompt, 100))

	// Run in background goroutine so the trigger loop is not blocked.
	go func() {
		result := runSingleTask(ctx, e.cfg, task, e.sem, e.childSem, agentName)
		logInfo("proactive dispatch done", "rule", rule.Name, "taskId", truncate(task.ID, 16), "status", result.Status, "durationMs", result.DurationMs)

		// Deliver the result output via configured channel.
		out := result.Output
		if out == "" {
			out = result.Error
		}
		if out == "" {
			out = fmt.Sprintf("Proactive rule %q completed with status %s", rule.Name, result.Status)
		}
		if err := e.deliver(rule, truncate(out, 1000)); err != nil {
			logWarn("proactive deliver failed", "rule", rule.Name, "error", err)
		}
	}()

	return nil
}

// buildAutonomousPrompt builds a context-rich prompt for an autonomous agent heartbeat.
// The agent receives system state (recent tasks, available roles, pending items) and
// decides on its own what — if anything — to do proactively.
func (e *ProactiveEngine) buildAutonomousPrompt(rule ProactiveRule) string {
	var b strings.Builder

	b.WriteString("You are an autonomous AI agent doing a periodic self-check (heartbeat).\n")
	b.WriteString("Review the current system state below and decide if there is anything useful to do proactively.\n")
	b.WriteString("If nothing needs attention, respond with a brief status summary and stop.\n\n")

	// Time context.
	b.WriteString(fmt.Sprintf("Current time: %s\n\n", time.Now().Format("2006-01-02 15:04:05 MST")))

	// Recent task stats from DB.
	if e.cfg.HistoryDB != "" {
		if dailyCost, err := e.getDailyCost(); err == nil {
			b.WriteString(fmt.Sprintf("Today's API cost: $%.4f\n", dailyCost))
		}
		if failedTasks, err := e.getFailedTasksToday(); err == nil {
			b.WriteString(fmt.Sprintf("Failed tasks today: %.0f\n", failedTasks))
		}

		// Recent 5 completed tasks.
		rows, err := queryDB(e.cfg.HistoryDB, "SELECT name, status, started_at FROM runs ORDER BY started_at DESC LIMIT 5")
		if err == nil && len(rows) > 0 {
			b.WriteString("\nRecent tasks:\n")
			for _, row := range rows {
				name, _ := row["name"].(string)
				status, _ := row["status"].(string)
				startedAt, _ := row["started_at"].(string)
				b.WriteString(fmt.Sprintf("  - %s [%s] at %s\n", name, status, startedAt))
			}
		}
	}

	// Recent reflections (self-assessments from past tasks).
	if e.cfg.HistoryDB != "" {
		refRows, err := queryDB(e.cfg.HistoryDB, "SELECT agent, score, feedback, improvement, created_at FROM reflections ORDER BY created_at DESC LIMIT 5")
		if err == nil && len(refRows) > 0 {
			b.WriteString("\nRecent reflections:\n")
			for _, row := range refRows {
				agent, _ := row["agent"].(string)
				score, _ := row["score"].(int64)
				feedback, _ := row["feedback"].(string)
				improvement, _ := row["improvement"].(string)
				createdAt, _ := row["created_at"].(string)
				b.WriteString(fmt.Sprintf("  - [%s] score:%d at %s\n    feedback: %s\n    improvement: %s\n", agent, score, createdAt, feedback, improvement))
			}
		}
	}

	// Available agents.
	if len(e.cfg.Agents) > 0 {
		b.WriteString("\nAvailable agents:\n")
		for name, ag := range e.cfg.Agents {
			b.WriteString(fmt.Sprintf("  - %s: %s\n", name, ag.Description))
		}
	}

	// Rule-specific hint.
	if rule.Action.Prompt != "" {
		b.WriteString(fmt.Sprintf("\nHint from rule %q: %s\n", rule.Name, rule.Action.Prompt))
	}

	b.WriteString("\nDecide what — if anything — to do. Be concise and take action if warranted.")

	return b.String()
}

// actionNotify sends a notification message.
func (e *ProactiveEngine) actionNotify(ctx context.Context, rule ProactiveRule) error {
	message := rule.Action.Message
	if message == "" {
		message = fmt.Sprintf("Proactive rule %q triggered", rule.Name)
	}

	// Resolve template variables.
	message = e.resolveTemplate(message, rule)

	return e.deliver(rule, message)
}

// --- Delivery ---

// deliver sends content to the configured delivery channel.
func (e *ProactiveEngine) deliver(rule ProactiveRule, content string) error {
	switch rule.Delivery.Channel {
	case "telegram":
		return e.deliverTelegram(rule, content)
	case "slack":
		return e.deliverSlack(rule, content)
	case "discord":
		return e.deliverDiscord(rule, content)
	case "dashboard":
		return e.deliverDashboard(rule, content)
	default:
		return fmt.Errorf("unknown delivery channel: %s", rule.Delivery.Channel)
	}
}

// deliverTelegram sends a message via Telegram.
func (e *ProactiveEngine) deliverTelegram(rule ProactiveRule, content string) error {
	if !e.cfg.Telegram.Enabled {
		return fmt.Errorf("telegram not enabled")
	}

	chatID := rule.Delivery.ChatID
	if chatID == 0 {
		chatID = e.cfg.Telegram.ChatID
	}

	return sendTelegramNotify(&e.cfg.Telegram, content)
}

// deliverSlack sends a message via Slack.
func (e *ProactiveEngine) deliverSlack(rule ProactiveRule, content string) error {
	if !e.cfg.Slack.Enabled {
		return fmt.Errorf("slack not enabled")
	}

	// Use existing Slack notification mechanism.
	logInfo("proactive slack delivery", "rule", rule.Name, "message", truncate(content, 100))
	// TODO: integrate with Slack send when available.
	return nil
}

// deliverDiscord sends a message via Discord.
func (e *ProactiveEngine) deliverDiscord(rule ProactiveRule, content string) error {
	if !e.cfg.Discord.Enabled {
		return fmt.Errorf("discord not enabled")
	}

	// Use existing Discord notification mechanism.
	logInfo("proactive discord delivery", "rule", rule.Name, "message", truncate(content, 100))
	// TODO: integrate with Discord send when available.
	return nil
}

// deliverDashboard publishes an SSE event to the dashboard.
func (e *ProactiveEngine) deliverDashboard(rule ProactiveRule, content string) error {
	if e.broker == nil {
		return fmt.Errorf("sse broker not available")
	}

	event := SSEEvent{
		Type: "proactive_notification",
		Data: map[string]string{
			"rule":    rule.Name,
			"message": content,
		},
		Timestamp: time.Now().Format(time.RFC3339),
	}

	e.broker.Publish("dashboard", event)
	return nil
}

// --- Template Resolution ---

// resolveTemplate replaces {{.Var}} placeholders in a template string.
func (e *ProactiveEngine) resolveTemplate(tmpl string, rule ProactiveRule) string {
	// Get current metrics for template variables.
	vars := map[string]string{
		"RuleName": rule.Name,
		"Time":     time.Now().Format(time.RFC3339),
	}

	// Add metric values if available.
	if dailyCost, err := e.getDailyCost(); err == nil {
		vars["CostToday"] = fmt.Sprintf("%.2f", dailyCost)
	}
	if queueDepth, err := e.getQueueDepth(); err == nil {
		vars["QueueDepth"] = fmt.Sprintf("%.0f", queueDepth)
	}
	if activeSessions, err := e.getActiveSessions(); err == nil {
		vars["ActiveSessions"] = fmt.Sprintf("%.0f", activeSessions)
	}
	if failedTasks, err := e.getFailedTasksToday(); err == nil {
		vars["FailedTasksToday"] = fmt.Sprintf("%.0f", failedTasks)
	}

	// Add trigger-specific variables.
	if rule.Trigger.Type == "threshold" {
		if value, err := e.getMetricValue(rule.Trigger.Metric); err == nil {
			vars["Value"] = fmt.Sprintf("%.2f", value)
			vars["Threshold"] = fmt.Sprintf("%.2f", rule.Trigger.Value)
			vars["Metric"] = rule.Trigger.Metric
		}
	}

	result := tmpl
	for k, v := range vars {
		placeholder := fmt.Sprintf("{{.%s}}", k)
		result = strings.ReplaceAll(result, placeholder, v)
	}

	return result
}

// --- Cooldown Management ---

// checkCooldown returns true if the rule is still in cooldown.
func (e *ProactiveEngine) checkCooldown(ruleName string) bool {
	e.mu.RLock()
	lastTriggered, ok := e.cooldowns[ruleName]
	e.mu.RUnlock()

	if !ok {
		return false
	}

	// Check if cooldown has expired (we'll get duration from rule, but for simplicity check 1 min default).
	return time.Since(lastTriggered) < time.Minute
}

// setCooldown records when a rule was triggered to enforce cooldown.
func (e *ProactiveEngine) setCooldown(ruleName string, duration time.Duration) {
	e.mu.Lock()
	e.cooldowns[ruleName] = time.Now()
	e.mu.Unlock()
}

// --- Public API ---

// ListRules returns info about all proactive rules.
func (e *ProactiveEngine) ListRules() []ProactiveRuleInfo {
	e.mu.RLock()
	defer e.mu.RUnlock()

	var infos []ProactiveRuleInfo
	for _, rule := range e.rules {
		info := ProactiveRuleInfo{
			Name:        rule.Name,
			Enabled:     rule.isEnabled(),
			TriggerType: rule.Trigger.Type,
			Cooldown:    rule.Cooldown,
		}

		if lastTriggered, ok := e.cooldowns[rule.Name]; ok {
			info.LastTriggered = lastTriggered
		}

		// Calculate next run for schedule rules.
		if rule.Trigger.Type == "schedule" {
			if expr, err := parseCronExpr(rule.Trigger.Cron); err == nil {
				loc := time.Local
				if rule.Trigger.TZ != "" {
					if l, err := time.LoadLocation(rule.Trigger.TZ); err == nil {
						loc = l
					}
				}
				info.NextRun = nextRunAfter(expr, loc, time.Now().In(loc))
			}
		}

		infos = append(infos, info)
	}

	return infos
}

// TriggerRule manually triggers a rule by name.
func (e *ProactiveEngine) TriggerRule(name string) error {
	e.mu.RLock()
	var target *ProactiveRule
	for i := range e.rules {
		if e.rules[i].Name == name {
			target = &e.rules[i]
			break
		}
	}
	e.mu.RUnlock()

	if target == nil {
		return fmt.Errorf("rule %q not found", name)
	}

	if !target.isEnabled() {
		return fmt.Errorf("rule %q is disabled", name)
	}

	logInfo("proactive manual trigger", "rule", name)
	ctx := context.Background()
	return e.executeAction(ctx, *target)
}

// --- CLI Handler ---

// runProactive handles the `tetora proactive` CLI command.
func runProactive(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: tetora proactive <subcommand>")
		fmt.Println()
		fmt.Println("Subcommands:")
		fmt.Println("  list          List all proactive rules")
		fmt.Println("  trigger <name> Manually trigger a rule")
		fmt.Println("  status        Show engine status")
		return
	}

	cfg := loadConfig("")

	switch args[0] {
	case "list":
		cmdProactiveList(cfg)
	case "trigger":
		if len(args) < 2 {
			fmt.Println("Usage: tetora proactive trigger <rule-name>")
			return
		}
		cmdProactiveTrigger(cfg, args[1])
	case "status":
		cmdProactiveStatus(cfg)
	default:
		fmt.Printf("Unknown subcommand: %s\n", args[0])
	}
}

// cmdProactiveList lists all proactive rules with their status.
func cmdProactiveList(cfg *Config) {
	if !cfg.Proactive.Enabled {
		fmt.Println("Proactive engine is disabled in config.")
		return
	}

	if len(cfg.Proactive.Rules) == 0 {
		fmt.Println("No proactive rules configured.")
		return
	}

	fmt.Printf("Proactive Rules (%d total):\n\n", len(cfg.Proactive.Rules))

	for _, rule := range cfg.Proactive.Rules {
		enabled := "✓"
		if !rule.isEnabled() {
			enabled = "✗"
		}

		fmt.Printf("[%s] %s\n", enabled, rule.Name)
		fmt.Printf("    Trigger: %s", rule.Trigger.Type)

		switch rule.Trigger.Type {
		case "schedule":
			fmt.Printf(" (%s", rule.Trigger.Cron)
			if rule.Trigger.TZ != "" {
				fmt.Printf(" %s", rule.Trigger.TZ)
			}
			fmt.Printf(")")
		case "event":
			fmt.Printf(" (event=%s)", rule.Trigger.Event)
		case "threshold":
			fmt.Printf(" (metric=%s %s %.2f)", rule.Trigger.Metric, rule.Trigger.Op, rule.Trigger.Value)
		case "heartbeat":
			fmt.Printf(" (interval=%s)", rule.Trigger.Interval)
		}

		fmt.Printf("\n    Action: %s", rule.Action.Type)
		if rule.Action.Agent != "" {
			fmt.Printf(" (role=%s)", rule.Action.Agent)
		}

		fmt.Printf("\n    Delivery: %s", rule.Delivery.Channel)
		if rule.Cooldown != "" {
			fmt.Printf("\n    Cooldown: %s", rule.Cooldown)
		}
		fmt.Println()
	}
}

// cmdProactiveTrigger manually triggers a rule via API.
func cmdProactiveTrigger(cfg *Config, ruleName string) {
	// In CLI mode, we need to call the daemon's API endpoint.
	apiURL := fmt.Sprintf("http://%s/api/proactive/rules/%s/trigger", cfg.ListenAddr, ruleName)

	req, err := http.NewRequest("POST", apiURL, nil)
	if err != nil {
		fmt.Printf("Error creating request: %v\n", err)
		return
	}

	if cfg.APIToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIToken)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		fmt.Printf("Rule %q triggered successfully.\n", ruleName)
	} else {
		var errResp map[string]string
		json.NewDecoder(resp.Body).Decode(&errResp)
		fmt.Printf("Error: %v\n", errResp)
	}
}

// cmdProactiveStatus shows the proactive engine status.
func cmdProactiveStatus(cfg *Config) {
	if !cfg.Proactive.Enabled {
		fmt.Println("Proactive engine is disabled in config.")
		return
	}

	enabled := 0
	for _, rule := range cfg.Proactive.Rules {
		if rule.isEnabled() {
			enabled++
		}
	}

	fmt.Printf("Proactive Engine Status:\n")
	fmt.Printf("  Total rules: %d\n", len(cfg.Proactive.Rules))
	fmt.Printf("  Enabled: %d\n", enabled)
	fmt.Printf("  Disabled: %d\n", len(cfg.Proactive.Rules)-enabled)
}
