package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
)

func (s *Server) registerStatsRoutes(mux *http.ServeMux) {
	cfg := s.cfg

	// --- Cost Stats ---
	mux.HandleFunc("/stats/cost", func(w http.ResponseWriter, r *http.Request) {
		if cfg.HistoryDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		stats, err := queryCostStats(cfg.HistoryDB)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}

		result := map[string]any{
			"today": stats.Today,
			"week":  stats.Week,
			"month": stats.Month,
		}

		// Include cost alert config if limits are set.
		if cfg.CostAlert.DailyLimit > 0 || cfg.CostAlert.WeeklyLimit > 0 {
			result["dailyLimit"] = cfg.CostAlert.DailyLimit
			result["weeklyLimit"] = cfg.CostAlert.WeeklyLimit
			result["alertAction"] = cfg.CostAlert.Action
			result["dailyExceeded"] = cfg.CostAlert.DailyLimit > 0 && stats.Today >= cfg.CostAlert.DailyLimit
			result["weeklyExceeded"] = cfg.CostAlert.WeeklyLimit > 0 && stats.Week >= cfg.CostAlert.WeeklyLimit
		}

		json.NewEncoder(w).Encode(result)
	})

	// --- Trend Stats ---
	mux.HandleFunc("/stats/trend", func(w http.ResponseWriter, r *http.Request) {
		if cfg.HistoryDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		days := 7
		if d := r.URL.Query().Get("days"); d != "" {
			if n, err := strconv.Atoi(d); err == nil && n > 0 && n <= 90 {
				days = n
			}
		}

		stats, err := queryDailyStats(cfg.HistoryDB, days)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		if stats == nil {
			stats = []DayStat{}
		}
		json.NewEncoder(w).Encode(stats)
	})

	// --- Metrics Stats ---
	mux.HandleFunc("/stats/metrics", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		if cfg.HistoryDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		days := 30
		if d := r.URL.Query().Get("days"); d != "" {
			if n, err := strconv.Atoi(d); err == nil && n > 0 && n <= 365 {
				days = n
			}
		}

		summary, err := queryMetrics(cfg.HistoryDB, days)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}

		daily, err := queryDailyMetrics(cfg.HistoryDB, days)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		if daily == nil {
			daily = []DailyMetrics{}
		}

		byModel, err := queryProviderMetrics(cfg.HistoryDB, days)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		if byModel == nil {
			byModel = []ProviderMetrics{}
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
		if cfg.HistoryDB == "" {
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

		history, byRole, err := queryRoutingStats(cfg.HistoryDB, limit)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		if history == nil {
			history = []RoutingHistoryEntry{}
		}
		if byRole == nil {
			byRole = map[string]*AgentRoutingStats{}
		}

		json.NewEncoder(w).Encode(map[string]any{
			"history": history,
			"byRole":  byRole,
			"total":   len(history),
		})
	})

	// --- SLA Stats ---
	mux.HandleFunc("/stats/sla", func(w http.ResponseWriter, r *http.Request) {
		if cfg.HistoryDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case http.MethodGet:
			statuses, err := querySLAStatusAll(cfg)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			if statuses == nil {
				statuses = []SLAStatus{}
			}

			// Also fetch recent check history.
			role := r.URL.Query().Get("role")
			limit := 24
			if l := r.URL.Query().Get("limit"); l != "" {
				if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 100 {
					limit = n
				}
			}
			history, _ := querySLAHistory(cfg.HistoryDB, role, limit)
			if history == nil {
				history = []SLACheckResult{}
			}

			json.NewEncoder(w).Encode(map[string]any{
				"statuses": statuses,
				"history":  history,
				"config":   cfg.SLA,
			})
		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
	})

	// --- Budget ---
	mux.HandleFunc("/budget", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		status := queryBudgetStatus(cfg)
		json.NewEncoder(w).Encode(status)
	})

	mux.HandleFunc("/budget/pause", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		cfg.Budgets.Paused = true
		configPath := filepath.Join(cfg.baseDir, "config.json")
		if err := setBudgetPaused(configPath, true); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		auditLog(cfg.HistoryDB, "budget.pause", "http", "all paid execution paused", clientIP(r))
		logWarn("budget PAUSED by API request", "ip", clientIP(r))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"paused"}`))
	})

	mux.HandleFunc("/budget/resume", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		cfg.Budgets.Paused = false
		configPath := filepath.Join(cfg.baseDir, "config.json")
		if err := setBudgetPaused(configPath, false); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		auditLog(cfg.HistoryDB, "budget.resume", "http", "paid execution resumed", clientIP(r))
		logInfo("budget RESUMED by API request", "ip", clientIP(r))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"active"}`))
	})

	// --- P18.1: Usage / Cost Dashboard API ---
	mux.HandleFunc("/api/usage/summary", func(w http.ResponseWriter, r *http.Request) {
		if cfg.HistoryDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		period := r.URL.Query().Get("period")
		if period == "" {
			period = "today"
		}

		summary, err := queryUsageSummary(cfg.HistoryDB, period)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}

		// Overlay budget info.
		switch period {
		case "today":
			if cfg.Budgets.Global.Daily > 0 {
				summary.BudgetLimit = cfg.Budgets.Global.Daily
				summary.BudgetPct = summary.TotalCost / summary.BudgetLimit * 100
			}
		case "week":
			if cfg.Budgets.Global.Weekly > 0 {
				summary.BudgetLimit = cfg.Budgets.Global.Weekly
				summary.BudgetPct = summary.TotalCost / summary.BudgetLimit * 100
			}
		case "month":
			if cfg.Budgets.Global.Monthly > 0 {
				summary.BudgetLimit = cfg.Budgets.Global.Monthly
				summary.BudgetPct = summary.TotalCost / summary.BudgetLimit * 100
			}
		}

		json.NewEncoder(w).Encode(summary)
	})

	mux.HandleFunc("/api/usage/breakdown", func(w http.ResponseWriter, r *http.Request) {
		if cfg.HistoryDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		by := r.URL.Query().Get("by")
		days := 30
		if d := r.URL.Query().Get("days"); d != "" {
			if n, err := strconv.Atoi(d); err == nil && n > 0 && n <= 365 {
				days = n
			}
		}

		switch by {
		case "model":
			models, err := queryUsageByModel(cfg.HistoryDB, days)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			if models == nil {
				models = []ModelUsage{}
			}
			json.NewEncoder(w).Encode(models)
		case "role":
			roles, err := queryUsageByAgent(cfg.HistoryDB, days)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			if roles == nil {
				roles = []AgentUsage{}
			}
			json.NewEncoder(w).Encode(roles)
		default:
			http.Error(w, `{"error":"by parameter required: model or role"}`, http.StatusBadRequest)
		}
	})

	mux.HandleFunc("/api/usage/sessions", func(w http.ResponseWriter, r *http.Request) {
		if cfg.HistoryDB == "" {
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
		if d := r.URL.Query().Get("days"); d != "" {
			if n, err := strconv.Atoi(d); err == nil && n > 0 && n <= 365 {
				days = n
			}
		}

		sessions, err := queryExpensiveSessions(cfg.HistoryDB, limit, days)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		if sessions == nil {
			sessions = []ExpensiveSession{}
		}
		json.NewEncoder(w).Encode(sessions)
	})

	mux.HandleFunc("/api/usage/trend", func(w http.ResponseWriter, r *http.Request) {
		if cfg.HistoryDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		days := 30
		if d := r.URL.Query().Get("days"); d != "" {
			if n, err := strconv.Atoi(d); err == nil && n > 0 && n <= 365 {
				days = n
			}
		}

		trend, err := queryCostTrend(cfg.HistoryDB, days)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		if trend == nil {
			trend = []DayUsage{}
		}
		json.NewEncoder(w).Encode(trend)
	})

	// --- Token Telemetry API ---
	// --- Task Trend (from dashboard.db) ---
	mux.HandleFunc("/api/tasks/trend", func(w http.ResponseWriter, r *http.Request) {
		if cfg.DashboardDB == "" {
			http.Error(w, `{"error":"dashboard DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		days := 14
		if d := r.URL.Query().Get("days"); d != "" {
			if n, err := strconv.Atoi(d); err == nil && n > 0 && n <= 90 {
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

		createdRows, err := queryDB(cfg.DashboardDB, fmt.Sprintf(
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

		doneRows, err := queryDB(cfg.DashboardDB, fmt.Sprintf(
			`SELECT date(completed_at, 'localtime') as day, COUNT(*) as cnt
			 FROM tasks
			 WHERE completed_at IS NOT NULL AND completed_at != ''
			   AND date(completed_at, 'localtime') >= date('now', 'localtime', '-%d days')
			 GROUP BY day`, days))
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		for _, row := range doneRows {
			ensure(jsonStr(row["day"])).Done = jsonInt(row["cnt"])
		}

		dates := make([]string, 0, len(byDate))
		for d := range byDate {
			dates = append(dates, d)
		}
		sort.Strings(dates)

		result := make([]TaskDayStat, 0, len(dates))
		for _, d := range dates {
			result = append(result, *byDate[d])
		}
		json.NewEncoder(w).Encode(result)
	})

	mux.HandleFunc("/api/tokens/summary", func(w http.ResponseWriter, r *http.Request) {
		if cfg.HistoryDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		days := 7
		if d := r.URL.Query().Get("days"); d != "" {
			if n, err := strconv.Atoi(d); err == nil && n > 0 && n <= 365 {
				days = n
			}
		}

		summaryRows, err := queryTokenUsageSummary(cfg.HistoryDB, days)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		roleRows, err := queryTokenUsageByRole(cfg.HistoryDB, days)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}

		summary := parseTokenSummaryRows(summaryRows)
		byRole := parseTokenAgentRows(roleRows)
		if summary == nil {
			summary = []TokenSummaryRow{}
		}
		if byRole == nil {
			byRole = []TokenAgentRow{}
		}

		json.NewEncoder(w).Encode(map[string]any{
			"summary": summary,
			"byRole":  byRole,
			"days":    days,
		})
	})
}
