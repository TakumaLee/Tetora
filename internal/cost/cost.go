package cost

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"tetora/internal/db"
)

// --- Budget Config Types ---

// BudgetConfig configures cost governance budgets and auto-downgrade.
type BudgetConfig struct {
	Global        GlobalBudget              `json:"global,omitempty"`
	Agents        map[string]AgentBudget    `json:"agents,omitempty"`
	Workflows     map[string]WorkflowBudget `json:"workflows,omitempty"`
	AutoDowngrade AutoDowngradeConfig       `json:"autoDowngrade,omitempty"`
	Paused        bool                      `json:"paused,omitempty"` // kill switch: pause all paid execution
}

// GlobalBudget defines daily/weekly/monthly budget caps.
type GlobalBudget struct {
	Daily   float64 `json:"daily,omitempty"`
	Weekly  float64 `json:"weekly,omitempty"`
	Monthly float64 `json:"monthly,omitempty"`
}

// AgentBudget defines per-agent daily budget cap.
type AgentBudget struct {
	Daily float64 `json:"daily,omitempty"`
}

// WorkflowBudget defines per-workflow per-run budget cap.
type WorkflowBudget struct {
	PerRun float64 `json:"perRun,omitempty"`
}

// AutoDowngradeConfig configures automatic model downgrade near budget limits.
type AutoDowngradeConfig struct {
	Enabled    bool                 `json:"enabled,omitempty"`
	Thresholds []DowngradeThreshold `json:"thresholds,omitempty"` // sorted ascending by At
}

// DowngradeThreshold defines a budget utilization threshold that triggers model downgrade.
type DowngradeThreshold struct {
	At    float64 `json:"at"`    // utilization ratio (0.0-1.0), e.g. 0.7
	Model string  `json:"model"` // model to downgrade to, e.g. "sonnet", "local/llama3"
}

// --- Budget Check Result ---

// BudgetCheckResult is the result of a pre-execution budget check.
type BudgetCheckResult struct {
	Allowed        bool    `json:"allowed"`
	Paused         bool    `json:"paused,omitempty"`         // kill switch active
	Exceeded       bool    `json:"exceeded,omitempty"`       // hard cap hit
	Message        string  `json:"message,omitempty"`        // human-readable reason
	DowngradeModel string  `json:"downgradeModel,omitempty"` // auto-downgrade model (empty = no change)
	Utilization    float64 `json:"utilization,omitempty"`    // highest utilization ratio (0.0-1.0)
	AlertLevel     string  `json:"alertLevel"`               // "ok", "warning", "critical", "exceeded", "paused"
}

// --- Budget Status (for API/CLI) ---

// BudgetStatus shows current spend vs. limits.
type BudgetStatus struct {
	Paused bool               `json:"paused"`
	Global *BudgetMeter       `json:"global,omitempty"`
	Agents []AgentBudgetMeter `json:"agents,omitempty"`
}

// BudgetMeter shows spend vs. limit for a time period.
type BudgetMeter struct {
	DailySpend   float64 `json:"dailySpend"`
	DailyLimit   float64 `json:"dailyLimit,omitempty"`
	DailyPct     float64 `json:"dailyPct"`
	WeeklySpend  float64 `json:"weeklySpend"`
	WeeklyLimit  float64 `json:"weeklyLimit,omitempty"`
	WeeklyPct    float64 `json:"weeklyPct"`
	MonthlySpend float64 `json:"monthlySpend"`
	MonthlyLimit float64 `json:"monthlyLimit,omitempty"`
	MonthlyPct   float64 `json:"monthlyPct"`
}

// AgentBudgetMeter shows per-agent spend vs. limit.
type AgentBudgetMeter struct {
	Agent      string  `json:"agent"`
	DailySpend float64 `json:"dailySpend"`
	DailyLimit float64 `json:"dailyLimit,omitempty"`
	DailyPct   float64 `json:"dailyPct"`
}

// --- Spend Queries ---

// QuerySpend returns cost sums for today, this week, and this month.
// If role is non-empty, filters by agent.
func QuerySpend(dbPath, role string) (daily, weekly, monthly float64) {
	if dbPath == "" {
		return
	}

	roleFilter := ""
	if role != "" {
		roleFilter = fmt.Sprintf(" AND agent = '%s'", db.Escape(role))
	}

	sql := fmt.Sprintf(
		`SELECT
			COALESCE(SUM(CASE WHEN date(started_at,'localtime') = date('now','localtime') THEN cost_usd ELSE 0 END), 0) as today,
			COALESCE(SUM(CASE WHEN date(started_at,'localtime') >= date('now','localtime','-7 days') THEN cost_usd ELSE 0 END), 0) as week,
			COALESCE(SUM(CASE WHEN date(started_at,'localtime') >= date('now','localtime','-30 days') THEN cost_usd ELSE 0 END), 0) as month
		 FROM job_runs WHERE 1=1%s`, roleFilter)

	rows, err := db.Query(dbPath, sql)
	if err != nil || len(rows) == 0 {
		return
	}
	daily = db.Float(rows[0]["today"])
	weekly = db.Float(rows[0]["week"])
	monthly = db.Float(rows[0]["month"])
	return
}

// QueryWorkflowRunSpend returns the total cost of an active workflow run.
func QueryWorkflowRunSpend(dbPath string, runID int) float64 {
	if dbPath == "" || runID <= 0 {
		return 0
	}
	sql := fmt.Sprintf(
		`SELECT COALESCE(cost_usd, 0) as cost FROM workflow_runs WHERE id = %d`, runID)
	rows, err := db.Query(dbPath, sql)
	if err != nil || len(rows) == 0 {
		return 0
	}
	return db.Float(rows[0]["cost"])
}

// --- Budget Checking ---

// CheckBudget performs a pre-execution budget check.
// Returns a BudgetCheckResult indicating whether execution is allowed.
func CheckBudget(budgets BudgetConfig, dbPath, agentName, workflowName string, workflowRunID int) *BudgetCheckResult {
	// Kill switch check.
	if budgets.Paused {
		return &BudgetCheckResult{
			Allowed:    false,
			Paused:     true,
			AlertLevel: "paused",
			Message:    "budget paused: all paid execution suspended",
		}
	}

	// No budgets configured = always allowed.
	if budgets.Global.Daily == 0 && budgets.Global.Weekly == 0 && budgets.Global.Monthly == 0 &&
		len(budgets.Agents) == 0 && len(budgets.Workflows) == 0 {
		return &BudgetCheckResult{Allowed: true, AlertLevel: "ok"}
	}

	result := &BudgetCheckResult{Allowed: true, AlertLevel: "ok"}
	var maxUtilization float64

	// Global budget check.
	if budgets.Global.Daily > 0 || budgets.Global.Weekly > 0 || budgets.Global.Monthly > 0 {
		daily, weekly, monthly := QuerySpend(dbPath, "")

		if budgets.Global.Daily > 0 {
			u := daily / budgets.Global.Daily
			if u > maxUtilization {
				maxUtilization = u
			}
			if u >= 1.0 {
				return &BudgetCheckResult{
					Allowed:     false,
					Exceeded:    true,
					Utilization: u,
					AlertLevel:  "exceeded",
					Message:     fmt.Sprintf("daily budget exceeded: $%.2f / $%.2f", daily, budgets.Global.Daily),
				}
			}
		}
		if budgets.Global.Weekly > 0 {
			u := weekly / budgets.Global.Weekly
			if u > maxUtilization {
				maxUtilization = u
			}
			if u >= 1.0 {
				return &BudgetCheckResult{
					Allowed:     false,
					Exceeded:    true,
					Utilization: u,
					AlertLevel:  "exceeded",
					Message:     fmt.Sprintf("weekly budget exceeded: $%.2f / $%.2f", weekly, budgets.Global.Weekly),
				}
			}
		}
		if budgets.Global.Monthly > 0 {
			u := monthly / budgets.Global.Monthly
			if u > maxUtilization {
				maxUtilization = u
			}
			if u >= 1.0 {
				return &BudgetCheckResult{
					Allowed:     false,
					Exceeded:    true,
					Utilization: u,
					AlertLevel:  "exceeded",
					Message:     fmt.Sprintf("monthly budget exceeded: $%.2f / $%.2f", monthly, budgets.Global.Monthly),
				}
			}
		}
	}

	// Per-agent budget check.
	if agentName != "" {
		if rb, ok := budgets.Agents[agentName]; ok && rb.Daily > 0 {
			daily, _, _ := QuerySpend(dbPath, agentName)
			u := daily / rb.Daily
			if u > maxUtilization {
				maxUtilization = u
			}
			if u >= 1.0 {
				return &BudgetCheckResult{
					Allowed:     false,
					Exceeded:    true,
					Utilization: u,
					AlertLevel:  "exceeded",
					Message:     fmt.Sprintf("agent %q daily budget exceeded: $%.2f / $%.2f", agentName, daily, rb.Daily),
				}
			}
		}
	}

	// Per-workflow budget check.
	if workflowName != "" && workflowRunID > 0 {
		if wb, ok := budgets.Workflows[workflowName]; ok && wb.PerRun > 0 {
			runCost := QueryWorkflowRunSpend(dbPath, workflowRunID)
			u := runCost / wb.PerRun
			if u > maxUtilization {
				maxUtilization = u
			}
			if u >= 1.0 {
				return &BudgetCheckResult{
					Allowed:     false,
					Exceeded:    true,
					Utilization: u,
					AlertLevel:  "exceeded",
					Message:     fmt.Sprintf("workflow %q per-run budget exceeded: $%.2f / $%.2f", workflowName, runCost, wb.PerRun),
				}
			}
		}
	}

	result.Utilization = maxUtilization

	// Determine alert level.
	if maxUtilization >= 0.9 {
		result.AlertLevel = "critical"
	} else if maxUtilization >= 0.7 {
		result.AlertLevel = "warning"
	}

	// Auto-downgrade resolution.
	if budgets.AutoDowngrade.Enabled && len(budgets.AutoDowngrade.Thresholds) > 0 {
		result.DowngradeModel = ResolveDowngradeModel(budgets.AutoDowngrade, maxUtilization)
	}

	return result
}

// ResolveDowngradeModel finds the appropriate downgrade model for the current utilization.
// Thresholds are checked in order to find the most restrictive (highest) match.
func ResolveDowngradeModel(ad AutoDowngradeConfig, utilization float64) string {
	var bestModel string
	var bestAt float64
	for _, t := range ad.Thresholds {
		if utilization >= t.At && t.At >= bestAt {
			bestModel = t.Model
			bestAt = t.At
		}
	}
	return bestModel
}

// --- Budget Status ---

// QueryBudgetStatus returns the current budget status for display.
func QueryBudgetStatus(budgets BudgetConfig, dbPath string) *BudgetStatus {
	status := &BudgetStatus{
		Paused: budgets.Paused,
	}

	// Global meter.
	if budgets.Global.Daily > 0 || budgets.Global.Weekly > 0 || budgets.Global.Monthly > 0 {
		daily, weekly, monthly := QuerySpend(dbPath, "")
		meter := &BudgetMeter{
			DailySpend:   daily,
			DailyLimit:   budgets.Global.Daily,
			WeeklySpend:  weekly,
			WeeklyLimit:  budgets.Global.Weekly,
			MonthlySpend: monthly,
			MonthlyLimit: budgets.Global.Monthly,
		}
		if meter.DailyLimit > 0 {
			meter.DailyPct = daily / meter.DailyLimit * 100
		}
		if meter.WeeklyLimit > 0 {
			meter.WeeklyPct = weekly / meter.WeeklyLimit * 100
		}
		if meter.MonthlyLimit > 0 {
			meter.MonthlyPct = monthly / meter.MonthlyLimit * 100
		}
		status.Global = meter
	} else {
		// No budget configured, still show spend.
		daily, weekly, monthly := QuerySpend(dbPath, "")
		status.Global = &BudgetMeter{
			DailySpend:   daily,
			WeeklySpend:  weekly,
			MonthlySpend: monthly,
		}
	}

	// Per-role meters.
	for agentName, rb := range budgets.Agents {
		daily, _, _ := QuerySpend(dbPath, agentName)
		meter := AgentBudgetMeter{
			Agent:      agentName,
			DailySpend: daily,
			DailyLimit: rb.Daily,
		}
		if rb.Daily > 0 {
			meter.DailyPct = daily / rb.Daily * 100
		}
		status.Agents = append(status.Agents, meter)
	}

	return status
}

// --- Budget Alert Notifications ---

// BudgetAlertTracker tracks which alerts have been sent to avoid spam.
type BudgetAlertTracker struct {
	mu       sync.Mutex
	sent     map[string]time.Time // key: "scope:period:level" → last sent
	Cooldown time.Duration
}

// NewBudgetAlertTracker creates a new tracker with a 1-hour cooldown.
func NewBudgetAlertTracker() *BudgetAlertTracker {
	return &BudgetAlertTracker{
		sent:     make(map[string]time.Time),
		Cooldown: 1 * time.Hour, // don't re-alert within 1h for same scope+level
	}
}

// ShouldAlert returns true if this alert hasn't been sent recently.
func (t *BudgetAlertTracker) ShouldAlert(key string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if last, ok := t.sent[key]; ok {
		if time.Since(last) < t.Cooldown {
			return false
		}
	}
	t.sent[key] = time.Now()
	return true
}

// CheckAndNotifyBudgetAlerts checks budget utilization and sends notifications.
func CheckAndNotifyBudgetAlerts(budgets BudgetConfig, dbPath string, notifyFn func(string), tracker *BudgetAlertTracker) {
	if notifyFn == nil || dbPath == "" {
		return
	}

	// Global alerts.
	if budgets.Global.Daily > 0 || budgets.Global.Weekly > 0 || budgets.Global.Monthly > 0 {
		daily, weekly, monthly := QuerySpend(dbPath, "")
		CheckPeriodAlert(notifyFn, tracker, "global", "daily", daily, budgets.Global.Daily)
		CheckPeriodAlert(notifyFn, tracker, "global", "weekly", weekly, budgets.Global.Weekly)
		CheckPeriodAlert(notifyFn, tracker, "global", "monthly", monthly, budgets.Global.Monthly)
	}

	// Per-role alerts.
	for agentName, rb := range budgets.Agents {
		if rb.Daily > 0 {
			daily, _, _ := QuerySpend(dbPath, agentName)
			CheckPeriodAlert(notifyFn, tracker, "role:"+agentName, "daily", daily, rb.Daily)
		}
	}
}

// CheckPeriodAlert sends a notification if spend crosses 70% or 90% thresholds.
func CheckPeriodAlert(notifyFn func(string), tracker *BudgetAlertTracker, scope, period string, spend, limit float64) {
	if limit <= 0 {
		return
	}
	pct := spend / limit
	if pct >= 0.9 {
		key := fmt.Sprintf("%s:%s:critical", scope, period)
		if tracker.ShouldAlert(key) {
			notifyFn(fmt.Sprintf("Budget CRITICAL [%s] %s: $%.2f / $%.2f (%.0f%%)",
				scope, period, spend, limit, pct*100))
		}
	} else if pct >= 0.7 {
		key := fmt.Sprintf("%s:%s:warning", scope, period)
		if tracker.ShouldAlert(key) {
			notifyFn(fmt.Sprintf("Budget Warning [%s] %s: $%.2f / $%.2f (%.0f%%)",
				scope, period, spend, limit, pct*100))
		}
	}
}

// --- Budget Summary (for daily digest / Telegram) ---

// FormatBudgetSummary formats a short budget summary from a pre-built status.
func FormatBudgetSummary(status *BudgetStatus) string {
	var lines []string

	if status.Paused {
		lines = append(lines, "Budget: PAUSED (all paid execution suspended)")
	}

	if status.Global != nil {
		g := status.Global
		parts := []string{fmt.Sprintf("Today: $%.2f", g.DailySpend)}
		if g.DailyLimit > 0 {
			parts[0] = fmt.Sprintf("Today: $%.2f/$%.2f (%.0f%%)", g.DailySpend, g.DailyLimit, g.DailyPct)
		}
		parts = append(parts, fmt.Sprintf("Week: $%.2f", g.WeeklySpend))
		if g.WeeklyLimit > 0 {
			parts[len(parts)-1] = fmt.Sprintf("Week: $%.2f/$%.2f (%.0f%%)", g.WeeklySpend, g.WeeklyLimit, g.WeeklyPct)
		}
		parts = append(parts, fmt.Sprintf("Month: $%.2f", g.MonthlySpend))
		if g.MonthlyLimit > 0 {
			parts[len(parts)-1] = fmt.Sprintf("Month: $%.2f/$%.2f (%.0f%%)", g.MonthlySpend, g.MonthlyLimit, g.MonthlyPct)
		}
		lines = append(lines, strings.Join(parts, " | "))
	}

	for _, r := range status.Agents {
		line := fmt.Sprintf("  %s: $%.2f", r.Agent, r.DailySpend)
		if r.DailyLimit > 0 {
			line = fmt.Sprintf("  %s: $%.2f/$%.2f (%.0f%%)", r.Agent, r.DailySpend, r.DailyLimit, r.DailyPct)
		}
		lines = append(lines, line)
	}

	if len(lines) == 0 {
		return "No budget configured"
	}
	return strings.Join(lines, "\n")
}

// SetBudgetPaused updates the budgets.paused field in a config.json file.
// It uses raw JSON manipulation to preserve all other config fields.
func SetBudgetPaused(configPath string, paused bool) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	var budgets map[string]json.RawMessage
	if budgetsRaw, ok := raw["budgets"]; ok {
		json.Unmarshal(budgetsRaw, &budgets) //nolint:errcheck
	}
	if budgets == nil {
		budgets = make(map[string]json.RawMessage)
	}

	pausedJSON, _ := json.Marshal(paused)
	budgets["paused"] = pausedJSON

	budgetsJSON, err := json.Marshal(budgets)
	if err != nil {
		return fmt.Errorf("marshal budgets: %w", err)
	}
	raw["budgets"] = budgetsJSON

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return os.WriteFile(configPath, out, 0o600)
}
