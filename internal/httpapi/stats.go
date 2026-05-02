package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"

	"tetora/internal/audit"
	"tetora/internal/cost"
	"tetora/internal/db"
	"tetora/internal/history"
	"tetora/internal/log"
	"tetora/internal/sla"
	"tetora/internal/telemetry"
)

// --- Data types (mirrors root-package types; no external dependencies) ---

// UsageSummary is the aggregate cost/token summary for a time period.
type UsageSummary struct {
	Period      string  `json:"period"`
	TotalCost   float64 `json:"totalCostUsd"`
	TotalTasks  int     `json:"totalTasks"`
	TokensIn    int     `json:"totalTokensIn"`
	TokensOut   int     `json:"totalTokensOut"`
	BudgetLimit float64 `json:"budgetLimit,omitempty"`
	BudgetPct   float64 `json:"budgetPct,omitempty"`
}

// StatsDeps holds dependencies for stats, budget, and usage HTTP handlers.
// Callbacks wrap root-package functions that cannot be imported from internal/.
// Routes that only need internal packages (audit, sla, telemetry, db, cost, history)
// call those directly without callbacks.
type StatsDeps struct {
	HistoryDB string

	// QueryCostStats returns today/week/month cost totals.
	QueryCostStats func(dbPath string) (today, week, month float64, err error)

	// QueryDailyStats returns per-day cost/task stats for the last N days.
	QueryDailyStats func(dbPath string, days int) (any, error)

	// QueryMetricsSummary/QueryDailyMetrics/QueryProviderMetrics return observability data.
	QueryMetricsSummary  func(dbPath string, days int) (any, error)
	QueryDailyMetrics    func(dbPath string, days int) (any, error)
	QueryProviderMetrics func(dbPath string, days int) (any, error)

	// Cost alert config.
	CostAlertDailyLimit  func() float64
	CostAlertWeeklyLimit func() float64
	CostAlertAction      func() string

	// Budget access.
	Budgets         func() cost.BudgetConfig
	SetBudgetPaused func(configPath string, paused bool) error
	// ConfigPath returns cfg.BaseDir (the directory containing config.json).
	ConfigPath func() string

	// SLA config.
	SLAConfig  func() sla.SLAConfig
	AgentNames func() []string

	// Usage query callbacks (wrap root-package query functions).
	QueryUsageSummary      func(dbPath, period string) (*UsageSummary, error)
	QueryUsageByModel      func(dbPath string, days int) (any, error)
	QueryUsageByAgent      func(dbPath string, days int) (any, error)
	QueryExpensiveSessions func(dbPath string, limit, days int) (any, error)
	QueryCostTrend         func(dbPath string, days int) (any, error)
}

// jsonStr converts an any value from a db row to string.
func jsonStr(v any) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case json.Number:
		return val.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}

// jsonFloat converts an any value from a db row to float64.
func jsonFloat(v any) float64 {
	if v == nil {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return val
	case json.Number:
		f, _ := val.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(val, 64)
		return f
	default:
		return 0
	}
}

// jsonInt converts an any value from a db row to int.
func jsonInt(v any) int {
	if v == nil {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return int(val)
	case json.Number:
		i, _ := val.Int64()
		return int(i)
	case string:
		i, _ := strconv.Atoi(val)
		return i
	default:
		return 0
	}
}

// RegisterStatsRoutes registers all stats, budget, usage, and scorecard routes.
func RegisterStatsRoutes(mux *http.ServeMux, d StatsDeps) {
	historyDB := d.HistoryDB

	// --- Cost Stats ---
	mux.HandleFunc("/stats/cost", func(w http.ResponseWriter, r *http.Request) {
		if historyDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		today, week, month, err := d.QueryCostStats(historyDB)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}

		result := map[string]any{
			"today": today,
			"week":  week,
			"month": month,
		}

		// Include cost alert config if limits are set.
		dailyLimit := d.CostAlertDailyLimit()
		weeklyLimit := d.CostAlertWeeklyLimit()
		if dailyLimit > 0 || weeklyLimit > 0 {
			result["dailyLimit"] = dailyLimit
			result["weeklyLimit"] = weeklyLimit
			result["alertAction"] = d.CostAlertAction()
			result["dailyExceeded"] = dailyLimit > 0 && today >= dailyLimit
			result["weeklyExceeded"] = weeklyLimit > 0 && week >= weeklyLimit
		}

		json.NewEncoder(w).Encode(result)
	})

	// --- Trend Stats ---
	mux.HandleFunc("/stats/trend", func(w http.ResponseWriter, r *http.Request) {
		if historyDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		days := 7
		if dv := r.URL.Query().Get("days"); dv != "" {
			if n, err := strconv.Atoi(dv); err == nil && n > 0 && n <= 90 {
				days = n
			}
		}

		stats, err := d.QueryDailyStats(historyDB, days)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		if stats == nil {
			stats = []history.DayStat{}
		}
		json.NewEncoder(w).Encode(stats)
	})

	// --- Metrics Stats ---
	mux.HandleFunc("/stats/metrics", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		if historyDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		days := 30
		if dv := r.URL.Query().Get("days"); dv != "" {
			if n, err := strconv.Atoi(dv); err == nil && n > 0 && n <= 365 {
				days = n
			}
		}

		summary, err := d.QueryMetricsSummary(historyDB, days)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}

		daily, err := d.QueryDailyMetrics(historyDB, days)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		if daily == nil {
			daily = []history.DailyMetrics{}
		}

		byModel, err := d.QueryProviderMetrics(historyDB, days)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		if byModel == nil {
			byModel = []history.ProviderMetrics{}
		}

		json.NewEncoder(w).Encode(map[string]any{
			"days":    days,
			"summary": summary,
			"daily":   daily,
			"byModel": byModel,
		})
	})

	// --- Routing Stats ---
	mux.HandleFunc("/stats/routing", func(w http.ResponseWriter, r *http.Request) {
		if historyDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		limit := 50
		if l := r.URL.Query().Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 200 {
				limit = n
			}
		}

		routeHistory, byRole, err := audit.QueryRoutingStats(historyDB, limit)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		if routeHistory == nil {
			routeHistory = []audit.RoutingHistoryEntry{}
		}
		if byRole == nil {
			byRole = map[string]*audit.AgentRoutingStats{}
		}

		json.NewEncoder(w).Encode(map[string]any{
			"history": routeHistory,
			"byRole":  byRole,
			"total":   len(routeHistory),
		})
	})

	// --- SLA Stats ---
	mux.HandleFunc("/stats/sla", func(w http.ResponseWriter, r *http.Request) {
		if historyDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case http.MethodGet:
			slaCfg := d.SLAConfig()
			window := slaCfg.WindowOrDefault()
			windowHours := int(window.Hours())
			if windowHours <= 0 {
				windowHours = 24
			}
			names := d.AgentNames()
			statuses, err := sla.QuerySLAStatusAll(historyDB, slaCfg.Agents, names, windowHours)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			if statuses == nil {
				statuses = []sla.SLAStatus{}
			}

			// Also fetch recent check history.
			role := r.URL.Query().Get("role")
			limit := 24
			if l := r.URL.Query().Get("limit"); l != "" {
				if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 100 {
					limit = n
				}
			}
			slaHistory, _ := sla.QuerySLAHistory(historyDB, role, limit)
			if slaHistory == nil {
				slaHistory = []sla.SLACheckResult{}
			}

			json.NewEncoder(w).Encode(map[string]any{
				"statuses": statuses,
				"history":  slaHistory,
				"config":   slaCfg,
			})
		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
	})

	// --- Budget ---
	mux.HandleFunc("/budget", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		status := cost.QueryBudgetStatus(d.Budgets(), historyDB)
		json.NewEncoder(w).Encode(status)
	})

	mux.HandleFunc("/budget/pause", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		configPath := filepath.Join(d.ConfigPath(), "config.json")
		if err := d.SetBudgetPaused(configPath, true); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		audit.Log(historyDB, "budget.pause", "http", "all paid execution paused", clientIP(r))
		log.Warn("budget PAUSED by API request", "ip", clientIP(r))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"paused"}`))
	})

	mux.HandleFunc("/budget/resume", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		configPath := filepath.Join(d.ConfigPath(), "config.json")
		if err := d.SetBudgetPaused(configPath, false); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		audit.Log(historyDB, "budget.resume", "http", "paid execution resumed", clientIP(r))
		log.Info("budget RESUMED by API request", "ip", clientIP(r))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"active"}`))
	})

	// --- P18.1: Usage / Cost Dashboard API ---
	mux.HandleFunc("/api/usage/summary", func(w http.ResponseWriter, r *http.Request) {
		if historyDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		period := r.URL.Query().Get("period")
		if period == "" {
			period = "today"
		}

		summary, err := d.QueryUsageSummary(historyDB, period)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}

		// Overlay budget info.
		budgets := d.Budgets()
		switch period {
		case "today":
			if budgets.Global.Daily > 0 {
				summary.BudgetLimit = budgets.Global.Daily
				summary.BudgetPct = summary.TotalCost / summary.BudgetLimit * 100
			}
		case "week":
			if budgets.Global.Weekly > 0 {
				summary.BudgetLimit = budgets.Global.Weekly
				summary.BudgetPct = summary.TotalCost / summary.BudgetLimit * 100
			}
		case "month":
			if budgets.Global.Monthly > 0 {
				summary.BudgetLimit = budgets.Global.Monthly
				summary.BudgetPct = summary.TotalCost / summary.BudgetLimit * 100
			}
		}

		json.NewEncoder(w).Encode(summary)
	})

	mux.HandleFunc("/api/usage/breakdown", func(w http.ResponseWriter, r *http.Request) {
		if historyDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		by := r.URL.Query().Get("by")
		days := 30
		if dv := r.URL.Query().Get("days"); dv != "" {
			if n, err := strconv.Atoi(dv); err == nil && n > 0 && n <= 365 {
				days = n
			}
		}

		switch by {
		case "model":
			models, err := d.QueryUsageByModel(historyDB, days)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			if models == nil {
				models = []any{}
			}
			json.NewEncoder(w).Encode(models)
		case "role":
			roles, err := d.QueryUsageByAgent(historyDB, days)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			if roles == nil {
				roles = []any{}
			}
			json.NewEncoder(w).Encode(roles)
		default:
			http.Error(w, `{"error":"by parameter required: model or role"}`, http.StatusBadRequest)
		}
	})

	mux.HandleFunc("/api/usage/sessions", func(w http.ResponseWriter, r *http.Request) {
		if historyDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		limit := 10
		if l := r.URL.Query().Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 100 {
				limit = n
			}
		}
		days := 30
		if dv := r.URL.Query().Get("days"); dv != "" {
			if n, err := strconv.Atoi(dv); err == nil && n > 0 && n <= 365 {
				days = n
			}
		}

		sessions, err := d.QueryExpensiveSessions(historyDB, limit, days)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		if sessions == nil {
			sessions = []any{}
		}
		json.NewEncoder(w).Encode(sessions)
	})

	mux.HandleFunc("/api/usage/trend", func(w http.ResponseWriter, r *http.Request) {
		if historyDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		days := 30
		if dv := r.URL.Query().Get("days"); dv != "" {
			if n, err := strconv.Atoi(dv); err == nil && n > 0 && n <= 365 {
				days = n
			}
		}

		trend, err := d.QueryCostTrend(historyDB, days)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		if trend == nil {
			trend = []any{}
		}
		json.NewEncoder(w).Encode(trend)
	})

	// --- Task Trend (from history.db) ---
	mux.HandleFunc("/api/tasks/trend", func(w http.ResponseWriter, r *http.Request) {
		if historyDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		days := 14
		if dv := r.URL.Query().Get("days"); dv != "" {
			if n, err := strconv.Atoi(dv); err == nil && n > 0 && n <= 90 {
				days = n
			}
		}

		type TaskDayStat struct {
			Date    string `json:"date"`
			Created int    `json:"created"`
			Done    int    `json:"done"`
		}

		byDate := map[string]*TaskDayStat{}
		ensure := func(day string) *TaskDayStat {
			if _, ok := byDate[day]; !ok {
				byDate[day] = &TaskDayStat{Date: day}
			}
			return byDate[day]
		}

		createdRows, err := db.Query(historyDB, fmt.Sprintf(
			`SELECT date(created_at, 'localtime') as day, COUNT(*) as cnt
			 FROM tasks
			 WHERE date(created_at, 'localtime') >= date('now', 'localtime', '-%d days')
			 GROUP BY day`, days))
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		for _, row := range createdRows {
			ensure(jsonStr(row["day"])).Created = jsonInt(row["cnt"])
		}

		doneRows, err := db.Query(historyDB, fmt.Sprintf(
			`SELECT date(CASE WHEN completed_at != '' THEN completed_at ELSE updated_at END, 'localtime') as day, COUNT(*) as cnt
			 FROM tasks
			 WHERE status IN ('done', 'completed')
			   AND date(CASE WHEN completed_at != '' THEN completed_at ELSE updated_at END, 'localtime') >= date('now', 'localtime', '-%d days')
			 GROUP BY day`, days))
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		for _, row := range doneRows {
			ensure(jsonStr(row["day"])).Done = jsonInt(row["cnt"])
		}

		dates := make([]string, 0, len(byDate))
		for dkey := range byDate {
			dates = append(dates, dkey)
		}
		sort.Strings(dates)

		result := make([]TaskDayStat, 0, len(dates))
		for _, dkey := range dates {
			result = append(result, *byDate[dkey])
		}
		json.NewEncoder(w).Encode(result)
	})

	// --- Token Telemetry ---
	mux.HandleFunc("/api/tokens/summary", func(w http.ResponseWriter, r *http.Request) {
		if historyDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		days := 7
		if dv := r.URL.Query().Get("days"); dv != "" {
			if n, err := strconv.Atoi(dv); err == nil && n > 0 && n <= 365 {
				days = n
			}
		}

		summaryRows, err := telemetry.QueryUsageSummary(historyDB, days)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		roleRows, err := telemetry.QueryUsageByRole(historyDB, days)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}

		summary := telemetry.ParseSummaryRows(summaryRows)
		byRole := telemetry.ParseAgentRows(roleRows)
		if summary == nil {
			summary = []telemetry.SummaryRow{}
		}
		if byRole == nil {
			byRole = []telemetry.AgentRow{}
		}

		json.NewEncoder(w).Encode(map[string]any{
			"summary": summary,
			"byRole":  byRole,
			"days":    days,
		})
	})

	// --- Period Comparison ---
	mux.HandleFunc("/api/usage/compare", func(w http.ResponseWriter, r *http.Request) {
		if historyDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		period := r.URL.Query().Get("period")
		if period == "" {
			period = "week"
		}

		current, err := d.QueryUsageSummary(historyDB, period)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}

		// Query previous period for comparison.
		prevPeriod := "prev_" + period
		previous, prevErr := d.QueryUsageSummary(historyDB, prevPeriod)
		if prevErr != nil {
			// If prev query fails, return current with zero deltas.
			previous = &UsageSummary{}
		}

		// Calculate deltas.
		costDelta := 0.0
		taskDelta := 0.0
		if previous.TotalCost > 0 {
			costDelta = (current.TotalCost - previous.TotalCost) / previous.TotalCost * 100
		}
		if previous.TotalTasks > 0 {
			taskDelta = float64(current.TotalTasks-previous.TotalTasks) / float64(previous.TotalTasks) * 100
		}

		json.NewEncoder(w).Encode(map[string]any{
			"current":   current,
			"previous":  previous,
			"costDelta": costDelta,
			"taskDelta": taskDelta,
			"period":    period,
		})
	})

	// --- Agent Scorecard ---
	mux.HandleFunc("/api/agents/scorecard", func(w http.ResponseWriter, r *http.Request) {
		if historyDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		days := 7
		if dv := r.URL.Query().Get("days"); dv != "" {
			if n, err := strconv.Atoi(dv); err == nil && n > 0 && n <= 365 {
				days = n
			}
		}

		scorecardSQL := fmt.Sprintf(`
			SELECT assignee,
			       COUNT(*) as total_tasks,
			       SUM(CASE WHEN status IN ('done','completed') THEN 1 ELSE 0 END) as done_tasks,
			       SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END) as failed_tasks,
			       COALESCE(SUM(cost_usd), 0) as total_cost,
			       COALESCE(AVG(CASE WHEN status IN ('done','completed') THEN cost_usd END), 0) as avg_cost_per_task,
			       COALESCE(AVG(CASE WHEN duration_ms > 0 THEN duration_ms END), 0) as avg_duration_ms
			FROM tasks
			WHERE assignee != ''
			  AND created_at >= datetime('now', '-%d days')
			GROUP BY assignee
			ORDER BY done_tasks DESC
		`, days)

		rows, err := db.Query(historyDB, scorecardSQL)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}

		type AgentScore struct {
			Agent          string  `json:"agent"`
			TotalTasks     int     `json:"totalTasks"`
			DoneTasks      int     `json:"doneTasks"`
			FailedTasks    int     `json:"failedTasks"`
			SuccessRate    float64 `json:"successRate"`
			TotalCost      float64 `json:"totalCost"`
			AvgCostPerTask float64 `json:"avgCostPerTask"`
			AvgDurationMs  float64 `json:"avgDurationMs"`
		}

		var scores []AgentScore
		for _, row := range rows {
			total := jsonInt(row["total_tasks"])
			done := jsonInt(row["done_tasks"])
			failed := jsonInt(row["failed_tasks"])
			successRate := 0.0
			if total > 0 {
				successRate = float64(done) / float64(total) * 100
			}
			scores = append(scores, AgentScore{
				Agent:          jsonStr(row["assignee"]),
				TotalTasks:     total,
				DoneTasks:      done,
				FailedTasks:    failed,
				SuccessRate:    successRate,
				TotalCost:      jsonFloat(row["total_cost"]),
				AvgCostPerTask: jsonFloat(row["avg_cost_per_task"]),
				AvgDurationMs:  jsonFloat(row["avg_duration_ms"]),
			})
		}
		if scores == nil {
			scores = []AgentScore{}
		}

		json.NewEncoder(w).Encode(map[string]any{
			"agents": scores,
			"days":   days,
		})
	})
}
