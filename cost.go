package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// --- Budget Config Types ---

// BudgetConfig configures cost governance budgets and auto-downgrade.
type BudgetConfig struct {
	Global        GlobalBudget              `json:"global,omitempty"`
	Roles         map[string]RoleBudget     `json:"roles,omitempty"`
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

// RoleBudget defines per-role daily budget cap.
type RoleBudget struct {
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
	Paused bool             `json:"paused"`
	Global *BudgetMeter     `json:"global,omitempty"`
	Roles  []RoleBudgetMeter `json:"roles,omitempty"`
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

// RoleBudgetMeter shows per-role spend vs. limit.
type RoleBudgetMeter struct {
	Role       string  `json:"role"`
	DailySpend float64 `json:"dailySpend"`
	DailyLimit float64 `json:"dailyLimit,omitempty"`
	DailyPct   float64 `json:"dailyPct"`
}

// --- Spend Queries ---

// querySpend returns cost sums for today, this week, and this month.
// If role is non-empty, filters by role.
func querySpend(dbPath, role string) (daily, weekly, monthly float64) {
	if dbPath == "" {
		return
	}

	roleFilter := ""
	if role != "" {
		roleFilter = fmt.Sprintf(" AND role = '%s'", escapeSQLite(role))
	}

	sql := fmt.Sprintf(
		`SELECT
			COALESCE(SUM(CASE WHEN date(started_at,'localtime') = date('now','localtime') THEN cost_usd ELSE 0 END), 0) as today,
			COALESCE(SUM(CASE WHEN date(started_at,'localtime') >= date('now','localtime','-7 days') THEN cost_usd ELSE 0 END), 0) as week,
			COALESCE(SUM(CASE WHEN date(started_at,'localtime') >= date('now','localtime','-30 days') THEN cost_usd ELSE 0 END), 0) as month
		 FROM job_runs WHERE 1=1%s`, roleFilter)

	rows, err := queryDB(dbPath, sql)
	if err != nil || len(rows) == 0 {
		return
	}
	daily = jsonFloat(rows[0]["today"])
	weekly = jsonFloat(rows[0]["week"])
	monthly = jsonFloat(rows[0]["month"])
	return
}

// queryWorkflowRunSpend returns the total cost of an active workflow run.
func queryWorkflowRunSpend(dbPath string, runID int) float64 {
	if dbPath == "" || runID <= 0 {
		return 0
	}
	sql := fmt.Sprintf(
		`SELECT COALESCE(cost_usd, 0) as cost FROM workflow_runs WHERE id = %d`, runID)
	rows, err := queryDB(dbPath, sql)
	if err != nil || len(rows) == 0 {
		return 0
	}
	return jsonFloat(rows[0]["cost"])
}

// --- Budget Checking ---

// checkBudget performs a pre-execution budget check.
// Returns a BudgetCheckResult indicating whether execution is allowed.
func checkBudget(cfg *Config, roleName, workflowName string, workflowRunID int) *BudgetCheckResult {
	budgets := cfg.Budgets

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
		len(budgets.Roles) == 0 && len(budgets.Workflows) == 0 {
		return &BudgetCheckResult{Allowed: true, AlertLevel: "ok"}
	}

	dbPath := cfg.HistoryDB
	result := &BudgetCheckResult{Allowed: true, AlertLevel: "ok"}
	var maxUtilization float64

	// Global budget check.
	if budgets.Global.Daily > 0 || budgets.Global.Weekly > 0 || budgets.Global.Monthly > 0 {
		daily, weekly, monthly := querySpend(dbPath, "")

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

	// Per-role budget check.
	if roleName != "" {
		if rb, ok := budgets.Roles[roleName]; ok && rb.Daily > 0 {
			daily, _, _ := querySpend(dbPath, roleName)
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
					Message:     fmt.Sprintf("role %q daily budget exceeded: $%.2f / $%.2f", roleName, daily, rb.Daily),
				}
			}
		}
	}

	// Per-workflow budget check.
	if workflowName != "" && workflowRunID > 0 {
		if wb, ok := budgets.Workflows[workflowName]; ok && wb.PerRun > 0 {
			runCost := queryWorkflowRunSpend(dbPath, workflowRunID)
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
		result.DowngradeModel = resolveDowngradeModel(budgets.AutoDowngrade, maxUtilization)
	}

	return result
}

// resolveDowngradeModel finds the appropriate downgrade model for the current utilization.
// Thresholds are checked in reverse order (highest first) to find the most restrictive match.
func resolveDowngradeModel(ad AutoDowngradeConfig, utilization float64) string {
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

// queryBudgetStatus returns the current budget status for display.
func queryBudgetStatus(cfg *Config) *BudgetStatus {
	status := &BudgetStatus{
		Paused: cfg.Budgets.Paused,
	}

	dbPath := cfg.HistoryDB

	// Global meter.
	if cfg.Budgets.Global.Daily > 0 || cfg.Budgets.Global.Weekly > 0 || cfg.Budgets.Global.Monthly > 0 {
		daily, weekly, monthly := querySpend(dbPath, "")
		meter := &BudgetMeter{
			DailySpend:   daily,
			DailyLimit:   cfg.Budgets.Global.Daily,
			WeeklySpend:  weekly,
			WeeklyLimit:  cfg.Budgets.Global.Weekly,
			MonthlySpend: monthly,
			MonthlyLimit: cfg.Budgets.Global.Monthly,
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
		daily, weekly, monthly := querySpend(dbPath, "")
		status.Global = &BudgetMeter{
			DailySpend:  daily,
			WeeklySpend: weekly,
			MonthlySpend: monthly,
		}
	}

	// Per-role meters.
	for roleName, rb := range cfg.Budgets.Roles {
		daily, _, _ := querySpend(dbPath, roleName)
		meter := RoleBudgetMeter{
			Role:       roleName,
			DailySpend: daily,
			DailyLimit: rb.Daily,
		}
		if rb.Daily > 0 {
			meter.DailyPct = daily / rb.Daily * 100
		}
		status.Roles = append(status.Roles, meter)
	}

	return status
}

// --- Budget Alert Notifications ---

// budgetAlertTracker tracks which alerts have been sent to avoid spam.
type budgetAlertTracker struct {
	mu       sync.Mutex
	sent     map[string]time.Time // key: "scope:period:level" → last sent
	cooldown time.Duration
}

func newBudgetAlertTracker() *budgetAlertTracker {
	return &budgetAlertTracker{
		sent:     make(map[string]time.Time),
		cooldown: 1 * time.Hour, // don't re-alert within 1h for same scope+level
	}
}

// shouldAlert returns true if this alert hasn't been sent recently.
func (t *budgetAlertTracker) shouldAlert(key string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if last, ok := t.sent[key]; ok {
		if time.Since(last) < t.cooldown {
			return false
		}
	}
	t.sent[key] = time.Now()
	return true
}

// checkAndNotifyBudgetAlerts checks budget utilization and sends notifications.
func checkAndNotifyBudgetAlerts(cfg *Config, notifyFn func(string), tracker *budgetAlertTracker) {
	if notifyFn == nil || cfg.HistoryDB == "" {
		return
	}
	budgets := cfg.Budgets

	// Global alerts.
	if budgets.Global.Daily > 0 || budgets.Global.Weekly > 0 || budgets.Global.Monthly > 0 {
		daily, weekly, monthly := querySpend(cfg.HistoryDB, "")
		checkPeriodAlert(notifyFn, tracker, "global", "daily", daily, budgets.Global.Daily)
		checkPeriodAlert(notifyFn, tracker, "global", "weekly", weekly, budgets.Global.Weekly)
		checkPeriodAlert(notifyFn, tracker, "global", "monthly", monthly, budgets.Global.Monthly)
	}

	// Per-role alerts.
	for roleName, rb := range budgets.Roles {
		if rb.Daily > 0 {
			daily, _, _ := querySpend(cfg.HistoryDB, roleName)
			checkPeriodAlert(notifyFn, tracker, "role:"+roleName, "daily", daily, rb.Daily)
		}
	}
}

// checkPeriodAlert sends a notification if spend crosses 70% or 90% thresholds.
func checkPeriodAlert(notifyFn func(string), tracker *budgetAlertTracker, scope, period string, spend, limit float64) {
	if limit <= 0 {
		return
	}
	pct := spend / limit
	if pct >= 0.9 {
		key := fmt.Sprintf("%s:%s:critical", scope, period)
		if tracker.shouldAlert(key) {
			notifyFn(fmt.Sprintf("Budget CRITICAL [%s] %s: $%.2f / $%.2f (%.0f%%)",
				scope, period, spend, limit, pct*100))
		}
	} else if pct >= 0.7 {
		key := fmt.Sprintf("%s:%s:warning", scope, period)
		if tracker.shouldAlert(key) {
			notifyFn(fmt.Sprintf("Budget Warning [%s] %s: $%.2f / $%.2f (%.0f%%)",
				scope, period, spend, limit, pct*100))
		}
	}
}

// --- Kill Switch ---

// setBudgetPaused updates the budgets.paused field in config.json.
func setBudgetPaused(configPath string, paused bool) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	// Parse existing budgets.
	var budgets map[string]json.RawMessage
	if budgetsRaw, ok := raw["budgets"]; ok {
		json.Unmarshal(budgetsRaw, &budgets)
	}
	if budgets == nil {
		budgets = make(map[string]json.RawMessage)
	}

	pausedJSON, _ := json.Marshal(paused)
	budgets["paused"] = pausedJSON

	budgetsJSON, err := json.Marshal(budgets)
	if err != nil {
		return err
	}
	raw["budgets"] = budgetsJSON

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, append(out, '\n'), 0o644)
}

// --- Budget Summary (for daily digest / Telegram) ---

// formatBudgetSummary formats a short budget summary.
func formatBudgetSummary(cfg *Config) string {
	status := queryBudgetStatus(cfg)
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

	for _, r := range status.Roles {
		line := fmt.Sprintf("  %s: $%.2f", r.Role, r.DailySpend)
		if r.DailyLimit > 0 {
			line = fmt.Sprintf("  %s: $%.2f/$%.2f (%.0f%%)", r.Role, r.DailySpend, r.DailyLimit, r.DailyPct)
		}
		lines = append(lines, line)
	}

	if len(lines) == 0 {
		return "No budget configured"
	}
	return strings.Join(lines, "\n")
}
