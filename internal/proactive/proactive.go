package proactive

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"tetora/internal/config"
	"tetora/internal/cron"
	"tetora/internal/db"
	"tetora/internal/dispatch"
	"tetora/internal/log"
	"tetora/internal/messaging/telegram"
)

// Deps holds injected callbacks that the engine needs but cannot import directly
// without creating a circular dependency on the root package.
type Deps struct {
	// RunTask executes a single task and returns its result.
	RunTask func(ctx context.Context, task dispatch.Task, sem, childSem chan struct{}, agentName string) dispatch.TaskResult

	// RecordHistory persists a completed task run to the history DB.
	RecordHistory func(dbPath string, task dispatch.Task, result dispatch.TaskResult, agentName, startedAt, finishedAt, outputFile string)

	// FillDefaults populates empty Task fields from config.
	FillDefaults func(cfg *config.Config, t *dispatch.Task)

	// NotifyFn sends a notification string via the configured notification chain
	// (e.g. Discord). May be nil if no notifier is wired up.
	NotifyFn func(string)
}

// cooldownEntry tracks when a rule was last triggered and how long the cooldown lasts.
type cooldownEntry struct {
	lastTriggered time.Time
	duration      time.Duration
}

// Engine manages proactive rules (scheduled, event-driven, threshold-based).
type Engine struct {
	rules     []config.ProactiveRule
	cfg       *config.Config
	broker    *dispatch.Broker
	deps      Deps
	mu        sync.RWMutex
	ctx       context.Context
	cancel    context.CancelFunc
	cooldowns map[string]cooldownEntry // rule name → {lastTriggered, duration}
	stopCh    chan struct{}
	wg        sync.WaitGroup
	sem       chan struct{} // shared semaphore for top-level tasks
	childSem  chan struct{} // shared semaphore for child tasks
}

// RuleInfo is the public view of a rule (for API).
type RuleInfo struct {
	Name          string    `json:"name"`
	Enabled       bool      `json:"enabled"`
	TriggerType   string    `json:"triggerType"`
	LastTriggered time.Time `json:"lastTriggered,omitempty"`
	NextRun       time.Time `json:"nextRun,omitempty"`
	Cooldown      string    `json:"cooldown,omitempty"`
}

// --- Engine Lifecycle ---

// New creates a new proactive Engine instance.
func New(cfg *config.Config, broker *dispatch.Broker, sem, childSem chan struct{}, deps Deps) *Engine {
	return &Engine{
		rules:     cfg.Proactive.Rules,
		cfg:       cfg,
		broker:    broker,
		deps:      deps,
		cooldowns: make(map[string]cooldownEntry),
		stopCh:    make(chan struct{}),
		sem:       sem,
		childSem:  childSem,
	}
}

// Start begins all rule evaluators in background goroutines.
func (e *Engine) Start(ctx context.Context) {
	e.ctx, e.cancel = context.WithCancel(ctx)
	e.loadCooldownsFromDB()

	log.Info("proactive engine starting", "rules", len(e.rules))

	// Start schedule loop (checks cron rules every 30s).
	e.wg.Add(1)
	go e.runScheduleLoop(e.ctx)

	// Start heartbeat loop (checks interval rules every 10s).
	e.wg.Add(1)
	go e.runHeartbeatLoop(e.ctx)

	// Threshold checking is polled by schedule loop.
	log.Info("proactive engine started")
}

// Stop gracefully shuts down the engine.
func (e *Engine) Stop() {
	if e.cancel != nil {
		e.cancel()
	}
	close(e.stopCh)
	e.wg.Wait()
	log.Info("proactive engine stopped")
}

// --- Trigger Evaluators ---

// runScheduleLoop checks cron-based rules every 30s.
func (e *Engine) runScheduleLoop(ctx context.Context) {
	defer e.wg.Done()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	log.Debug("proactive schedule loop started")

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
func (e *Engine) runHeartbeatLoop(ctx context.Context) {
	defer e.wg.Done()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	log.Debug("proactive heartbeat loop started")

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
func (e *Engine) checkScheduleRules(ctx context.Context) {
	e.mu.RLock()
	rules := e.rules
	e.mu.RUnlock()

	for _, rule := range rules {
		if !rule.IsEnabled() || rule.Trigger.Type != "schedule" {
			continue
		}

		if e.CheckCooldown(rule.Name) {
			continue // still in cooldown
		}

		if e.matchesSchedule(rule) {
			log.Info("proactive schedule triggered", "rule", rule.Name)
			if err := e.executeAction(ctx, rule); err != nil {
				log.Error("proactive action failed", "rule", rule.Name, "error", err)
			}
		}
	}
}

// checkHeartbeatRules evaluates all heartbeat-type rules.
func (e *Engine) checkHeartbeatRules(ctx context.Context) {
	e.mu.RLock()
	rules := e.rules
	e.mu.RUnlock()

	now := time.Now()
	for _, rule := range rules {
		if !rule.IsEnabled() || rule.Trigger.Type != "heartbeat" {
			continue
		}

		interval, err := time.ParseDuration(rule.Trigger.Interval)
		if err != nil {
			continue
		}

		e.mu.Lock()
		entry, ok := e.cooldowns[rule.Name]
		e.mu.Unlock()

		if !ok || now.Sub(entry.lastTriggered) >= interval {
			log.Info("proactive heartbeat triggered", "rule", rule.Name, "interval", interval)
			if err := e.executeAction(ctx, rule); err != nil {
				log.Error("proactive action failed", "rule", rule.Name, "error", err)
			}
		}
	}
}

// checkThresholdRules evaluates all threshold-type rules.
func (e *Engine) checkThresholdRules(ctx context.Context) {
	e.mu.RLock()
	rules := e.rules
	e.mu.RUnlock()

	for _, rule := range rules {
		if !rule.IsEnabled() || rule.Trigger.Type != "threshold" {
			continue
		}

		if e.CheckCooldown(rule.Name) {
			continue
		}

		value, err := e.getMetricValue(rule.Trigger.Metric)
		if err != nil {
			log.Debug("proactive metric error", "rule", rule.Name, "metric", rule.Trigger.Metric, "error", err)
			continue
		}

		threshold := rule.Trigger.Value
		if rule.Trigger.DynamicFormula != "" {
			if dyn, err := e.getDynamicThreshold(rule.Trigger.DynamicFormula); err != nil {
				log.Debug("proactive dynamic threshold error", "rule", rule.Name, "formula", rule.Trigger.DynamicFormula, "error", err)
				continue
			} else {
				threshold = dyn
			}
		}

		if e.CompareThreshold(value, rule.Trigger.Op, threshold) {
			log.Info("proactive threshold triggered", "rule", rule.Name, "metric", rule.Trigger.Metric, "value", value, "threshold", threshold)
			if err := e.executeAction(ctx, rule); err != nil {
				log.Error("proactive action failed", "rule", rule.Name, "error", err)
			}
		}
	}
}

// HandleEvent processes incoming SSE events and triggers matching rules.
func (e *Engine) HandleEvent(event dispatch.SSEEvent) {
	e.mu.RLock()
	rules := e.rules
	e.mu.RUnlock()

	for _, rule := range rules {
		if !rule.IsEnabled() || rule.Trigger.Type != "event" {
			continue
		}

		if rule.Trigger.Event == event.Type {
			if e.CheckCooldown(rule.Name) {
				continue
			}

			log.Info("proactive event triggered", "rule", rule.Name, "event", event.Type)
			ctx := context.Background()
			if err := e.executeAction(ctx, rule); err != nil {
				log.Error("proactive action failed", "rule", rule.Name, "error", err)
			}
		}
	}
}

// --- Schedule Matching ---

// matchesSchedule checks if a schedule rule should fire now.
func (e *Engine) matchesSchedule(rule config.ProactiveRule) bool {
	expr, err := cron.Parse(rule.Trigger.Cron)
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
	if !expr.Matches(now) {
		return false
	}

	// Avoid double-firing in the same minute.
	e.mu.Lock()
	entry, ok := e.cooldowns[rule.Name]
	e.mu.Unlock()

	if ok && entry.lastTriggered.In(loc).Truncate(time.Minute).Equal(now.Truncate(time.Minute)) {
		return false
	}

	return true
}

// --- Threshold Comparison ---

// CompareThreshold compares a metric value against a threshold using the given operator.
func (e *Engine) CompareThreshold(value float64, op string, threshold float64) bool {
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
func (e *Engine) getMetricValue(metric string) (float64, error) {
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
func (e *Engine) getDailyCost() (float64, error) {
	if e.cfg.HistoryDB == "" {
		return 0, fmt.Errorf("historyDB not configured")
	}

	today := time.Now().Format("2006-01-02")
	sql := fmt.Sprintf("SELECT COALESCE(SUM(cost_usd), 0) FROM job_runs WHERE started_at LIKE '%s%%'", db.Escape(today))

	rows, err := db.Query(e.cfg.HistoryDB, sql)
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

// get30DayDailyCosts returns the total cost per day for the past 30 days.
// Columns are accessed by name (via AS aliases) — Go map iteration order is
// non-deterministic, so any positional `vals[i]` access on a row map would
// silently flip between day and cost depending on the iteration order.
func (e *Engine) get30DayDailyCosts() ([]float64, error) {
	if e.cfg.HistoryDB == "" {
		return nil, fmt.Errorf("historyDB not configured")
	}
	sql := `SELECT DATE(started_at) AS day, COALESCE(SUM(cost_usd), 0) AS total_cost FROM job_runs
	        WHERE datetime(started_at) >= datetime('now', '-30 days')
	        GROUP BY day ORDER BY day`
	rows, err := db.Query(e.cfg.HistoryDB, sql)
	if err != nil {
		return nil, err
	}
	costs := make([]float64, 0, len(rows))
	for _, row := range rows {
		switch v := row["total_cost"].(type) {
		case float64:
			costs = append(costs, v)
		case int64:
			costs = append(costs, float64(v))
		}
	}
	return costs, nil
}

// computeMedian returns the median of a sorted copy of vals. Returns 0 for empty input.
func computeMedian(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	cp := make([]float64, len(vals))
	copy(cp, vals)
	sort.Float64s(cp)
	n := len(cp)
	if n%2 == 0 {
		return (cp[n/2-1] + cp[n/2]) / 2
	}
	return cp[n/2]
}

// getDynamicThreshold computes the threshold from a formula string.
// Supported formula: "median_30d_x1.5" → 30-day daily cost median × 1.5.
func (e *Engine) getDynamicThreshold(formula string) (float64, error) {
	switch formula {
	case "median_30d_x1.5":
		costs, err := e.get30DayDailyCosts()
		if err != nil {
			return 0, err
		}
		return computeMedian(costs) * 1.5, nil
	default:
		return 0, fmt.Errorf("unknown dynamic_formula: %s", formula)
	}
}

// getQueueDepth returns the number of items in the offline queue.
func (e *Engine) getQueueDepth() (float64, error) {
	if e.cfg.HistoryDB == "" {
		return 0, fmt.Errorf("historyDB not configured")
	}

	sql := "SELECT COUNT(*) FROM offline_queue"
	rows, err := db.Query(e.cfg.HistoryDB, sql)
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
func (e *Engine) getActiveSessions() (float64, error) {
	if e.cfg.HistoryDB == "" {
		return 0, fmt.Errorf("historyDB not configured")
	}

	sql := "SELECT COUNT(DISTINCT session_id) FROM sessions WHERE last_activity > datetime('now', '-1 hour')"
	rows, err := db.Query(e.cfg.HistoryDB, sql)
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
func (e *Engine) getFailedTasksToday() (float64, error) {
	if e.cfg.HistoryDB == "" {
		return 0, fmt.Errorf("historyDB not configured")
	}

	today := time.Now().Format("2006-01-02")
	// Blacklist approach: exclude success and skip-variants so new failure status types are caught automatically.
	sql := fmt.Sprintf("SELECT COUNT(*) FROM job_runs WHERE started_at LIKE '%s%%' AND status NOT IN ('success', 'skipped_concurrent_limit')", db.Escape(today))

	rows, err := db.Query(e.cfg.HistoryDB, sql)
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
func (e *Engine) executeAction(ctx context.Context, rule config.ProactiveRule) error {
	// Set cooldown.
	if rule.Cooldown != "" {
		duration, err := time.ParseDuration(rule.Cooldown)
		if err == nil {
			e.SetCooldown(rule.Name, duration)
		}
	} else {
		// Default cooldown by trigger type.
		switch rule.Trigger.Type {
		case "schedule", "threshold":
			e.SetCooldown(rule.Name, time.Minute)
		case "heartbeat":
			if interval, err := time.ParseDuration(rule.Trigger.Interval); err == nil {
				e.SetCooldown(rule.Name, interval)
			}
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
func (e *Engine) actionDispatch(ctx context.Context, rule config.ProactiveRule) error {
	if e.sem == nil {
		log.Warn("proactive dispatch skipped: sem not available", "rule", rule.Name)
		return nil
	}

	prompt := rule.Action.Prompt
	if rule.Action.PromptTemplate != "" {
		prompt = e.ResolveTemplate(rule.Action.PromptTemplate, rule)
	}

	// Autonomous mode: override prompt with context-rich self-initiative prompt.
	if rule.Action.Autonomous || prompt == "" {
		prompt = e.buildAutonomousPrompt(rule)
	}

	agentName := rule.Action.Agent
	if agentName == "" {
		agentName = "ruri"
	}

	task := dispatch.Task{
		ID:     generateID("proactive"),
		Name:   fmt.Sprintf("proactive:%s", rule.Name),
		Prompt: prompt,
		Source: "proactive",
		Agent:  agentName,
	}

	if e.deps.FillDefaults != nil {
		e.deps.FillDefaults(e.cfg, &task)
	}

	log.Info("proactive dispatch action", "rule", rule.Name, "agent", agentName, "taskId", truncate(task.ID, 16), "prompt", truncate(prompt, 100))

	// Run in background goroutine so the trigger loop is not blocked.
	go func() {
		start := time.Now()
		result := e.deps.RunTask(ctx, task, e.sem, e.childSem, agentName)
		log.Info("proactive dispatch done", "rule", rule.Name, "taskId", truncate(task.ID, 16), "status", result.Status, "durationMs", result.DurationMs)

		// Record to history DB so cost/tokens appear in budget queries.
		if e.deps.RecordHistory != nil {
			e.deps.RecordHistory(e.cfg.HistoryDB, task, result, agentName,
				start.Format(time.RFC3339), time.Now().Format(time.RFC3339), result.OutputFile)
		}

		// Deliver the result output via configured channel.
		out := result.Output
		if out == "" {
			out = result.Error
		}
		if out == "" {
			out = fmt.Sprintf("Proactive rule %q completed with status %s", rule.Name, result.Status)
		}
		if err := e.deliver(rule, truncate(out, 1000)); err != nil {
			log.Warn("proactive deliver failed", "rule", rule.Name, "error", err)
		}
	}()

	return nil
}

// buildAutonomousPrompt builds a context-rich prompt for an autonomous agent heartbeat.
// The agent receives system state (recent tasks, available roles, pending items) and
// decides on its own what — if anything — to do proactively.
func (e *Engine) buildAutonomousPrompt(rule config.ProactiveRule) string {
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
		rows, err := db.Query(e.cfg.HistoryDB, "SELECT name, status, started_at FROM job_runs ORDER BY started_at DESC LIMIT 5")
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
		refRows, err := db.Query(e.cfg.HistoryDB, "SELECT agent, score, feedback, improvement, created_at FROM reflections ORDER BY created_at DESC LIMIT 5")
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

	// Taskboard tickets (backlog, todo, doing, failed).
	if e.cfg.HistoryDB != "" {
		tbRows, err := db.Query(e.cfg.HistoryDB, `
			SELECT id, title, status, assignee, priority, project, updated_at
			FROM tasks
			WHERE status IN ('backlog', 'todo', 'doing', 'failed')
			ORDER BY
				CASE priority
					WHEN 'urgent' THEN 1
					WHEN 'high' THEN 2
					WHEN 'normal' THEN 3
					WHEN 'low' THEN 4
					ELSE 5
				END,
				created_at DESC
			LIMIT 20
		`)
		if err == nil && len(tbRows) > 0 {
			b.WriteString("\nTaskboard tickets (active):\n")
			for _, row := range tbRows {
				id, _ := row["id"].(string)
				title, _ := row["title"].(string)
				status, _ := row["status"].(string)
				assignee, _ := row["assignee"].(string)
				priority, _ := row["priority"].(string)
				project, _ := row["project"].(string)
				if assignee == "" {
					assignee = "unassigned"
				}
				b.WriteString(fmt.Sprintf("  - [%s] %s: %s (assignee:%s, priority:%s, project:%s)\n",
					status, id, title, assignee, priority, project))
			}
		} else if err == nil {
			b.WriteString("\nTaskboard: no active tickets.\n")
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
func (e *Engine) actionNotify(ctx context.Context, rule config.ProactiveRule) error {
	message := rule.Action.Message
	if message == "" {
		message = fmt.Sprintf("Proactive rule %q triggered", rule.Name)
	}

	// Resolve template variables.
	message = e.ResolveTemplate(message, rule)

	return e.deliver(rule, message)
}

// --- Delivery ---

// deliver sends content to the configured delivery channel.
func (e *Engine) deliver(rule config.ProactiveRule, content string) error {
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
func (e *Engine) deliverTelegram(rule config.ProactiveRule, content string) error {
	if !e.cfg.Telegram.Enabled {
		return fmt.Errorf("telegram not enabled")
	}

	chatID := rule.Delivery.ChatID
	if chatID == 0 {
		chatID = e.cfg.Telegram.ChatID
	}
	_ = chatID // used implicitly via cfg passed to SendTelegramNotify

	return telegram.SendTelegramNotify(&e.cfg.Telegram, content)
}

// deliverSlack sends a message via Slack.
func (e *Engine) deliverSlack(rule config.ProactiveRule, content string) error {
	if !e.cfg.Slack.Enabled {
		return fmt.Errorf("slack not enabled")
	}

	// Use existing Slack notification mechanism.
	log.Info("proactive slack delivery", "rule", rule.Name, "message", truncate(content, 100))
	// TODO: integrate with Slack send when available.
	return nil
}

// deliverDiscord sends a message via Discord.
func (e *Engine) deliverDiscord(rule config.ProactiveRule, content string) error {
	if !e.cfg.Discord.Enabled {
		return fmt.Errorf("discord not enabled")
	}
	if e.deps.NotifyFn == nil {
		return fmt.Errorf("discord notifyFn not configured")
	}
	log.Info("proactive discord delivery", "rule", rule.Name, "message", truncate(content, 100))
	e.deps.NotifyFn(content)

	// Persist report to file for agent access.
	e.saveReport(rule.Name, content)
	return nil
}

// saveReport persists a proactive report to the workspace/reports/ directory.
// Files are saved as {date}/{rule-name}-{HHMM}.md with latest always at {rule-name}-latest.md.
// A "triggered:" header is prepended so readers can determine when the alert fired
// without relying on filesystem mtime (which becomes stale when files persist across days).
func (e *Engine) saveReport(ruleName, content string) {
	now := time.Now()
	reportsDir := filepath.Join(e.cfg.WorkspaceDir, "reports")
	dateDir := filepath.Join(reportsDir, now.Format("2006-01-02"))

	if err := os.MkdirAll(dateDir, 0o755); err != nil {
		log.Error("saveReport: mkdir failed", "err", err)
		return
	}

	timestampedContent := fmt.Sprintf("triggered: %s\n\n%s", now.Format(time.RFC3339), content)

	filename := fmt.Sprintf("%s-%s.md", ruleName, now.Format("1504"))

	// Save dated report.
	dated := filepath.Join(dateDir, filename)
	if err := os.WriteFile(dated, []byte(timestampedContent), 0o644); err != nil {
		log.Error("saveReport: write dated failed", "err", err)
		return
	}

	// Save latest file for easy agent access.
	latest := filepath.Join(reportsDir, ruleName+"-latest.md")
	_ = os.Remove(latest)
	if err := os.WriteFile(latest, []byte(timestampedContent), 0o644); err != nil {
		log.Error("saveReport: write latest failed", "err", err)
	}

	log.Info("saveReport: saved", "path", dated)
}

// deliverDashboard publishes an SSE event to the dashboard.
func (e *Engine) deliverDashboard(rule config.ProactiveRule, content string) error {
	if e.broker == nil {
		return fmt.Errorf("sse broker not available")
	}

	event := dispatch.SSEEvent{
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

// ResolveTemplate replaces {{.Var}} placeholders in a template string.
func (e *Engine) ResolveTemplate(tmpl string, rule config.ProactiveRule) string {
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
			vars["Metric"] = rule.Trigger.Metric
			threshold := rule.Trigger.Value
			if rule.Trigger.DynamicFormula != "" {
				if dyn, err := e.getDynamicThreshold(rule.Trigger.DynamicFormula); err == nil {
					threshold = dyn
				}
			}
			vars["Threshold"] = fmt.Sprintf("%.2f", threshold)
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

// CheckCooldown returns true if the rule is still in cooldown.
func (e *Engine) CheckCooldown(ruleName string) bool {
	e.mu.RLock()
	entry, ok := e.cooldowns[ruleName]
	e.mu.RUnlock()

	if !ok {
		return false
	}

	d := entry.duration
	if d <= 0 {
		d = time.Minute // fallback for entries without a stored duration
	}
	return time.Since(entry.lastTriggered) < d
}

// SetCooldown records when a rule was triggered and for how long to enforce cooldown.
func (e *Engine) SetCooldown(ruleName string, duration time.Duration) {
	now := time.Now()
	e.mu.Lock()
	e.cooldowns[ruleName] = cooldownEntry{lastTriggered: now, duration: duration}
	e.mu.Unlock()
	e.persistCooldownToDB(ruleName, now, duration)
}

// persistCooldownToDB writes a cooldown entry to SQLite (upsert).
func (e *Engine) persistCooldownToDB(ruleName string, triggered time.Time, duration time.Duration) {
	if e.cfg.HistoryDB == "" {
		return
	}
	sql := fmt.Sprintf(
		`INSERT INTO proactive_cooldowns (rule_name, last_triggered, duration_ns)
		 VALUES ('%s', '%s', %d)
		 ON CONFLICT(rule_name) DO UPDATE SET last_triggered=excluded.last_triggered, duration_ns=excluded.duration_ns`,
		db.Escape(ruleName), triggered.UTC().Format(time.RFC3339), duration.Nanoseconds())
	if err := db.Exec(e.cfg.HistoryDB, sql); err != nil {
		log.Warn("proactive cooldown persist failed", "rule", ruleName, "error", err)
	}
}

// loadCooldownsFromDB restores cooldown state from SQLite on startup.
func (e *Engine) loadCooldownsFromDB() {
	if e.cfg.HistoryDB == "" {
		return
	}
	// Ensure table exists (idempotent).
	if err := db.Exec(e.cfg.HistoryDB, `CREATE TABLE IF NOT EXISTS proactive_cooldowns (
		rule_name TEXT PRIMARY KEY,
		last_triggered TEXT NOT NULL,
		duration_ns INTEGER NOT NULL
	)`); err != nil {
		log.Warn("proactive cooldown table creation failed", "error", err)
		return
	}

	rows, err := db.Query(e.cfg.HistoryDB, "SELECT rule_name, last_triggered, duration_ns FROM proactive_cooldowns")
	if err != nil {
		log.Warn("proactive cooldown load failed", "error", err)
		return
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	loaded := 0
	for _, row := range rows {
		name := db.Str(row["rule_name"])
		triggeredStr := db.Str(row["last_triggered"])
		durationNs := db.Int(row["duration_ns"])
		if name == "" || triggeredStr == "" {
			continue
		}
		triggered, err := time.Parse(time.RFC3339, triggeredStr)
		if err != nil {
			continue
		}
		dur := time.Duration(durationNs)
		// Only restore if cooldown has not yet expired.
		if time.Since(triggered) < dur {
			e.cooldowns[name] = cooldownEntry{lastTriggered: triggered, duration: dur}
			loaded++
		}
	}
	if loaded > 0 {
		log.Info("proactive cooldowns restored from DB", "count", loaded)
	}
}

// CooldownTime returns the last-triggered time for a rule, if any.
// Exported for testing.
func (e *Engine) CooldownTime(ruleName string) (time.Time, bool) {
	e.mu.RLock()
	entry, ok := e.cooldowns[ruleName]
	e.mu.RUnlock()
	return entry.lastTriggered, ok
}

// --- Public API ---

// ListRules returns info about all proactive rules.
func (e *Engine) ListRules() []RuleInfo {
	e.mu.RLock()
	defer e.mu.RUnlock()

	var infos []RuleInfo
	for _, rule := range e.rules {
		info := RuleInfo{
			Name:        rule.Name,
			Enabled:     rule.IsEnabled(),
			TriggerType: rule.Trigger.Type,
			Cooldown:    rule.Cooldown,
		}

		if entry, ok := e.cooldowns[rule.Name]; ok {
			info.LastTriggered = entry.lastTriggered
		}

		// Calculate next run for schedule rules.
		if rule.Trigger.Type == "schedule" {
			if expr, err := cron.Parse(rule.Trigger.Cron); err == nil {
				loc := time.Local
				if rule.Trigger.TZ != "" {
					if l, err := time.LoadLocation(rule.Trigger.TZ); err == nil {
						loc = l
					}
				}
				info.NextRun = cron.NextRunAfter(expr, loc, time.Now().In(loc))
			}
		}

		infos = append(infos, info)
	}

	return infos
}

// TriggerRule manually triggers a rule by name.
func (e *Engine) TriggerRule(name string) error {
	e.mu.RLock()
	var target *config.ProactiveRule
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

	if !target.IsEnabled() {
		return fmt.Errorf("rule %q is disabled", name)
	}

	log.Info("proactive manual trigger", "rule", name)
	ctx := context.Background()
	return e.executeAction(ctx, *target)
}

// --- CLI Commands ---

// CmdList lists all proactive rules with their status.
func CmdList(cfg *config.Config) {
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
		if !rule.IsEnabled() {
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

// CmdTrigger manually triggers a rule via the daemon API.
func CmdTrigger(cfg *config.Config, ruleName string) {
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

// CmdStatus shows the proactive engine status.
func CmdStatus(cfg *config.Config) {
	if !cfg.Proactive.Enabled {
		fmt.Println("Proactive engine is disabled in config.")
		return
	}

	enabled := 0
	for _, rule := range cfg.Proactive.Rules {
		if rule.IsEnabled() {
			enabled++
		}
	}

	fmt.Printf("Proactive Engine Status:\n")
	fmt.Printf("  Total rules: %d\n", len(cfg.Proactive.Rules))
	fmt.Printf("  Enabled: %d\n", enabled)
	fmt.Printf("  Disabled: %d\n", len(cfg.Proactive.Rules)-enabled)
}

// --- Helpers ---

// generateID returns a unique ID with the given prefix.
func generateID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

// truncate shortens s to maxLen runes, appending "..." if truncated.
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
