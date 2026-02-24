package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"time"
)

// --- P24.5: Habit & Wellness Tracking ---

// HabitsService manages habit tracking and health data.
type HabitsService struct {
	dbPath string
	cfg    *Config
}

var globalHabitsService *HabitsService

// initHabitsDB creates the habits, habit_logs, and health_data tables.
func initHabitsDB(dbPath string) error {
	ddl := `
CREATE TABLE IF NOT EXISTS habits (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT DEFAULT '',
    frequency TEXT NOT NULL DEFAULT 'daily',
    target_count INTEGER DEFAULT 1,
    category TEXT DEFAULT 'general',
    color TEXT DEFAULT '',
    created_at TEXT NOT NULL,
    archived_at TEXT DEFAULT '',
    scope TEXT DEFAULT ''
);

CREATE TABLE IF NOT EXISTS habit_logs (
    id TEXT PRIMARY KEY,
    habit_id TEXT NOT NULL,
    logged_at TEXT NOT NULL,
    value REAL DEFAULT 1.0,
    note TEXT DEFAULT '',
    scope TEXT DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_habit_logs_habit ON habit_logs(habit_id);
CREATE INDEX IF NOT EXISTS idx_habit_logs_date ON habit_logs(logged_at);

CREATE TABLE IF NOT EXISTS health_data (
    id TEXT PRIMARY KEY,
    metric TEXT NOT NULL,
    value REAL NOT NULL,
    unit TEXT DEFAULT '',
    recorded_at TEXT NOT NULL,
    source TEXT DEFAULT 'manual',
    scope TEXT DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_health_metric ON health_data(metric, recorded_at);
`
	cmd := exec.Command("sqlite3", dbPath, ddl)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("init habits tables: %w: %s", err, string(out))
	}
	return nil
}

// newHabitsService creates a new HabitsService.
func newHabitsService(cfg *Config) *HabitsService {
	return &HabitsService{
		cfg:    cfg,
		dbPath: cfg.HistoryDB,
	}
}

// CreateHabit creates a new habit and returns its ID.
func (h *HabitsService) CreateHabit(name, description, frequency, category, scope string, targetCount int) (string, error) {
	if name == "" {
		return "", fmt.Errorf("habit name is required")
	}
	if frequency == "" {
		frequency = "daily"
	}
	if category == "" {
		category = "general"
	}
	if targetCount < 1 {
		targetCount = 1
	}

	id := newUUID()
	now := time.Now().UTC().Format(time.RFC3339)

	sql := fmt.Sprintf(
		`INSERT INTO habits (id, name, description, frequency, target_count, category, created_at, scope)
		 VALUES ('%s', '%s', '%s', '%s', %d, '%s', '%s', '%s')`,
		escapeSQLite(id),
		escapeSQLite(name),
		escapeSQLite(description),
		escapeSQLite(frequency),
		targetCount,
		escapeSQLite(category),
		escapeSQLite(now),
		escapeSQLite(scope),
	)

	cmd := exec.Command("sqlite3", h.dbPath, sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("create habit: %w: %s", err, string(out))
	}
	return id, nil
}

// LogHabit records a habit completion.
func (h *HabitsService) LogHabit(habitID, note, scope string, value float64) error {
	if habitID == "" {
		return fmt.Errorf("habit_id is required")
	}

	// Verify habit exists and is not archived.
	rows, err := queryDB(h.dbPath, fmt.Sprintf(
		`SELECT id, archived_at FROM habits WHERE id = '%s'`, escapeSQLite(habitID)))
	if err != nil {
		return fmt.Errorf("check habit: %w", err)
	}
	if len(rows) == 0 {
		return fmt.Errorf("habit not found: %s", habitID)
	}
	if archived := jsonStr(rows[0]["archived_at"]); archived != "" {
		return fmt.Errorf("habit is archived since %s", archived)
	}

	if value <= 0 {
		value = 1.0
	}

	id := newUUID()
	now := time.Now().UTC().Format(time.RFC3339)

	// P27.2: Encrypt log note.
	encNote := encryptField(h.cfg, note)

	sql := fmt.Sprintf(
		`INSERT INTO habit_logs (id, habit_id, logged_at, value, note, scope)
		 VALUES ('%s', '%s', '%s', %f, '%s', '%s')`,
		escapeSQLite(id),
		escapeSQLite(habitID),
		escapeSQLite(now),
		value,
		escapeSQLite(encNote),
		escapeSQLite(scope),
	)

	cmd := exec.Command("sqlite3", h.dbPath, sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("log habit: %w: %s", err, string(out))
	}
	return nil
}

// GetStreak calculates the current and longest streak for a habit.
func (h *HabitsService) GetStreak(habitID, scope string) (current int, longest int, err error) {
	// Get the habit to determine frequency and target count.
	rows, err := queryDB(h.dbPath, fmt.Sprintf(
		`SELECT frequency, target_count FROM habits WHERE id = '%s'`, escapeSQLite(habitID)))
	if err != nil {
		return 0, 0, fmt.Errorf("get habit: %w", err)
	}
	if len(rows) == 0 {
		return 0, 0, fmt.Errorf("habit not found: %s", habitID)
	}

	frequency := jsonStr(rows[0]["frequency"])
	targetCount := int(jsonFloat(rows[0]["target_count"]))
	if targetCount < 1 {
		targetCount = 1
	}

	// Build scope filter.
	scopeFilter := ""
	if scope != "" {
		scopeFilter = fmt.Sprintf(" AND scope = '%s'", escapeSQLite(scope))
	}

	if frequency == "weekly" {
		return h.getWeeklyStreak(habitID, targetCount, scopeFilter)
	}
	return h.getDailyStreak(habitID, targetCount, scopeFilter)
}

// getDailyStreak calculates streak for daily habits.
func (h *HabitsService) getDailyStreak(habitID string, targetCount int, scopeFilter string) (current int, longest int, err error) {
	// Get logs grouped by date, ordered descending.
	sql := fmt.Sprintf(
		`SELECT date(logged_at) as log_date, SUM(value) as total
		 FROM habit_logs
		 WHERE habit_id = '%s'%s
		 GROUP BY log_date
		 HAVING total >= %d
		 ORDER BY log_date DESC`,
		escapeSQLite(habitID), scopeFilter, targetCount)

	rows, err := queryDB(h.dbPath, sql)
	if err != nil {
		return 0, 0, fmt.Errorf("query streak: %w", err)
	}

	if len(rows) == 0 {
		return 0, 0, nil
	}

	// Parse dates and calculate streaks.
	today := time.Now().UTC().Format("2006-01-02")
	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")

	var dates []string
	for _, row := range rows {
		d := jsonStr(row["log_date"])
		if d != "" {
			dates = append(dates, d)
		}
	}

	if len(dates) == 0 {
		return 0, 0, nil
	}

	// Current streak: must include today or yesterday.
	current = 0
	if dates[0] == today || dates[0] == yesterday {
		current = 1
		for i := 1; i < len(dates); i++ {
			prev, err1 := time.Parse("2006-01-02", dates[i-1])
			curr, err2 := time.Parse("2006-01-02", dates[i])
			if err1 != nil || err2 != nil {
				break
			}
			diff := prev.Sub(curr).Hours() / 24
			if diff == 1 {
				current++
			} else {
				break
			}
		}
	}

	// Longest streak: scan all dates.
	longest = 0
	streak := 1
	for i := 1; i < len(dates); i++ {
		prev, err1 := time.Parse("2006-01-02", dates[i-1])
		curr, err2 := time.Parse("2006-01-02", dates[i])
		if err1 != nil || err2 != nil {
			if streak > longest {
				longest = streak
			}
			streak = 1
			continue
		}
		diff := prev.Sub(curr).Hours() / 24
		if diff == 1 {
			streak++
		} else {
			if streak > longest {
				longest = streak
			}
			streak = 1
		}
	}
	if streak > longest {
		longest = streak
	}
	if current > longest {
		longest = current
	}

	return current, longest, nil
}

// getWeeklyStreak calculates streak for weekly habits.
func (h *HabitsService) getWeeklyStreak(habitID string, targetCount int, scopeFilter string) (current int, longest int, err error) {
	// Get logs grouped by ISO week, ordered descending.
	sql := fmt.Sprintf(
		`SELECT strftime('%%Y-W%%W', logged_at) as log_week, SUM(value) as total
		 FROM habit_logs
		 WHERE habit_id = '%s'%s
		 GROUP BY log_week
		 HAVING total >= %d
		 ORDER BY log_week DESC`,
		escapeSQLite(habitID), scopeFilter, targetCount)

	rows, err := queryDB(h.dbPath, sql)
	if err != nil {
		return 0, 0, fmt.Errorf("query weekly streak: %w", err)
	}

	if len(rows) == 0 {
		return 0, 0, nil
	}

	// Current week identifier.
	now := time.Now().UTC()
	currentWeek := now.Format("2006-W") + fmt.Sprintf("%02d", isoWeekNumber(now))
	lastWeek := now.AddDate(0, 0, -7)
	prevWeek := lastWeek.Format("2006-W") + fmt.Sprintf("%02d", isoWeekNumber(lastWeek))

	var weeks []string
	for _, row := range rows {
		w := jsonStr(row["log_week"])
		if w != "" {
			weeks = append(weeks, w)
		}
	}

	if len(weeks) == 0 {
		return 0, 0, nil
	}

	// Current streak: must include current or previous week.
	current = 0
	if weeks[0] == currentWeek || weeks[0] == prevWeek {
		current = 1
		for i := 1; i < len(weeks); i++ {
			if consecutiveWeeks(weeks[i], weeks[i-1]) {
				current++
			} else {
				break
			}
		}
	}

	// Longest streak.
	longest = 0
	streak := 1
	for i := 1; i < len(weeks); i++ {
		if consecutiveWeeks(weeks[i], weeks[i-1]) {
			streak++
		} else {
			if streak > longest {
				longest = streak
			}
			streak = 1
		}
	}
	if streak > longest {
		longest = streak
	}
	if current > longest {
		longest = current
	}

	return current, longest, nil
}

// isoWeekNumber returns the ISO week number for the given time.
func isoWeekNumber(t time.Time) int {
	_, week := t.ISOWeek()
	return week
}

// consecutiveWeeks checks if two "YYYY-WNN" strings represent consecutive weeks.
func consecutiveWeeks(earlier, later string) bool {
	// Parse year and week from format "2006-W01".
	var y1, w1, y2, w2 int
	fmt.Sscanf(earlier, "%d-W%d", &y1, &w1)
	fmt.Sscanf(later, "%d-W%d", &y2, &w2)

	if y1 == y2 {
		return w2-w1 == 1
	}
	// Year boundary: last week of y1 -> first week of y2.
	if y2 == y1+1 && w2 == 1 {
		// w1 should be the last week of y1 (52 or 53).
		return w1 >= 52
	}
	return false
}

// HabitStatus returns all active habits with current streak and today's completion.
func (h *HabitsService) HabitStatus(scope string) ([]map[string]any, error) {
	scopeFilter := ""
	if scope != "" {
		scopeFilter = fmt.Sprintf(" AND scope = '%s'", escapeSQLite(scope))
	}

	rows, err := queryDB(h.dbPath, fmt.Sprintf(
		`SELECT id, name, description, frequency, target_count, category, color, created_at
		 FROM habits
		 WHERE archived_at = '' OR archived_at IS NULL%s
		 ORDER BY created_at ASC`, scopeFilter))
	if err != nil {
		return nil, fmt.Errorf("list habits: %w", err)
	}

	today := time.Now().UTC().Format("2006-01-02")
	thirtyDaysAgo := time.Now().UTC().AddDate(0, 0, -30).Format("2006-01-02")

	var result []map[string]any
	for _, row := range rows {
		habitID := jsonStr(row["id"])
		targetCount := int(jsonFloat(row["target_count"]))
		if targetCount < 1 {
			targetCount = 1
		}

		// Today's completion count.
		todayRows, err := queryDB(h.dbPath, fmt.Sprintf(
			`SELECT COALESCE(SUM(value), 0) as today_total
			 FROM habit_logs
			 WHERE habit_id = '%s' AND date(logged_at) = '%s'`,
			escapeSQLite(habitID), today))
		if err != nil {
			logWarn("habits", "status query failed for %s: %v", habitID, err)
			continue
		}
		todayTotal := 0.0
		if len(todayRows) > 0 {
			todayTotal = jsonFloat(todayRows[0]["today_total"])
		}

		// Completion rate (last 30 days).
		rateRows, err := queryDB(h.dbPath, fmt.Sprintf(
			`SELECT COUNT(DISTINCT date(logged_at)) as completed_days
			 FROM habit_logs
			 WHERE habit_id = '%s' AND date(logged_at) >= '%s'`,
			escapeSQLite(habitID), thirtyDaysAgo))
		if err != nil {
			logWarn("habits", "rate query failed for %s: %v", habitID, err)
			continue
		}
		completedDays := 0.0
		if len(rateRows) > 0 {
			completedDays = jsonFloat(rateRows[0]["completed_days"])
		}
		completionRate := completedDays / 30.0

		// Get streak.
		currentStreak, longestStreak, _ := h.GetStreak(habitID, scope)

		status := map[string]any{
			"id":             habitID,
			"name":           jsonStr(row["name"]),
			"description":    jsonStr(row["description"]),
			"frequency":      jsonStr(row["frequency"]),
			"target_count":   targetCount,
			"category":       jsonStr(row["category"]),
			"color":          jsonStr(row["color"]),
			"today_count":    todayTotal,
			"today_complete": todayTotal >= float64(targetCount),
			"current_streak": currentStreak,
			"longest_streak": longestStreak,
			"completion_rate": math.Round(completionRate*1000) / 10, // percentage, 1 decimal
		}
		result = append(result, status)
	}

	if result == nil {
		result = []map[string]any{}
	}
	return result, nil
}

// HabitReport generates a detailed report for a habit or all habits.
func (h *HabitsService) HabitReport(habitID, period, scope string) (map[string]any, error) {
	if period == "" {
		period = "week"
	}

	// Calculate date range.
	now := time.Now().UTC()
	var startDate string
	switch period {
	case "week":
		startDate = now.AddDate(0, 0, -7).Format("2006-01-02")
	case "month":
		startDate = now.AddDate(0, -1, 0).Format("2006-01-02")
	case "year":
		startDate = now.AddDate(-1, 0, 0).Format("2006-01-02")
	default:
		startDate = now.AddDate(0, 0, -7).Format("2006-01-02")
	}

	endDate := now.Format("2006-01-02")

	scopeFilter := ""
	if scope != "" {
		scopeFilter = fmt.Sprintf(" AND scope = '%s'", escapeSQLite(scope))
	}

	habitFilter := ""
	if habitID != "" {
		habitFilter = fmt.Sprintf(" AND habit_id = '%s'", escapeSQLite(habitID))
	}

	// Total logs in period.
	sql := fmt.Sprintf(
		`SELECT COUNT(*) as total_logs, COALESCE(SUM(value), 0) as total_value
		 FROM habit_logs
		 WHERE date(logged_at) >= '%s' AND date(logged_at) <= '%s'%s%s`,
		startDate, endDate, habitFilter, scopeFilter)
	rows, err := queryDB(h.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("report query: %w", err)
	}

	totalLogs := 0.0
	totalValue := 0.0
	if len(rows) > 0 {
		totalLogs = jsonFloat(rows[0]["total_logs"])
		totalValue = jsonFloat(rows[0]["total_value"])
	}

	// Days with completions.
	sql = fmt.Sprintf(
		`SELECT date(logged_at) as log_date, SUM(value) as day_total
		 FROM habit_logs
		 WHERE date(logged_at) >= '%s' AND date(logged_at) <= '%s'%s%s
		 GROUP BY log_date
		 ORDER BY log_date ASC`,
		startDate, endDate, habitFilter, scopeFilter)
	dayRows, err := queryDB(h.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("day breakdown query: %w", err)
	}

	// Calculate total days in period.
	start, _ := time.Parse("2006-01-02", startDate)
	end, _ := time.Parse("2006-01-02", endDate)
	totalDays := int(end.Sub(start).Hours()/24) + 1
	completedDays := len(dayRows)
	completionRate := 0.0
	if totalDays > 0 {
		completionRate = float64(completedDays) / float64(totalDays) * 100
	}

	// Best and worst days (by day of week).
	dayOfWeekCounts := make(map[string]float64)
	dayOfWeekDays := make(map[string]int)
	for _, dr := range dayRows {
		dateStr := jsonStr(dr["log_date"])
		t, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			continue
		}
		dow := t.Weekday().String()
		dayOfWeekCounts[dow] += jsonFloat(dr["day_total"])
		dayOfWeekDays[dow]++
	}

	bestDay := ""
	worstDay := ""
	bestAvg := 0.0
	worstAvg := math.MaxFloat64
	for dow, total := range dayOfWeekCounts {
		avg := total / float64(dayOfWeekDays[dow])
		if avg > bestAvg {
			bestAvg = avg
			bestDay = dow
		}
		if avg < worstAvg {
			worstAvg = avg
			worstDay = dow
		}
	}
	if len(dayOfWeekCounts) == 0 {
		worstDay = ""
		worstAvg = 0
	}

	// Trend: compare first half vs second half of period.
	midpoint := len(dayRows) / 2
	firstHalfTotal := 0.0
	secondHalfTotal := 0.0
	for i, dr := range dayRows {
		val := jsonFloat(dr["day_total"])
		if i < midpoint {
			firstHalfTotal += val
		} else {
			secondHalfTotal += val
		}
	}
	trend := "stable"
	if len(dayRows) >= 4 {
		if secondHalfTotal > firstHalfTotal*1.1 {
			trend = "improving"
		} else if secondHalfTotal < firstHalfTotal*0.9 {
			trend = "declining"
		}
	}

	// Get streak info if specific habit.
	var streakInfo map[string]any
	if habitID != "" {
		current, longest, _ := h.GetStreak(habitID, scope)
		streakInfo = map[string]any{
			"current": current,
			"longest": longest,
		}
	}

	report := map[string]any{
		"period":          period,
		"start_date":      startDate,
		"end_date":        endDate,
		"total_logs":      int(totalLogs),
		"total_value":     totalValue,
		"total_days":      totalDays,
		"completed_days":  completedDays,
		"completion_rate": math.Round(completionRate*10) / 10,
		"best_day":        bestDay,
		"worst_day":       worstDay,
		"trend":           trend,
	}
	if streakInfo != nil {
		report["streak"] = streakInfo
	}
	if habitID != "" {
		report["habit_id"] = habitID
	}

	return report, nil
}

// LogHealth stores a health data point.
func (h *HabitsService) LogHealth(metric string, value float64, unit, source, scope string) error {
	if metric == "" {
		return fmt.Errorf("metric is required")
	}
	if source == "" {
		source = "manual"
	}

	id := newUUID()
	now := time.Now().UTC().Format(time.RFC3339)

	sql := fmt.Sprintf(
		`INSERT INTO health_data (id, metric, value, unit, recorded_at, source, scope)
		 VALUES ('%s', '%s', %f, '%s', '%s', '%s', '%s')`,
		escapeSQLite(id),
		escapeSQLite(metric),
		value,
		escapeSQLite(unit),
		escapeSQLite(now),
		escapeSQLite(source),
		escapeSQLite(scope),
	)

	cmd := exec.Command("sqlite3", h.dbPath, sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("log health: %w: %s", err, string(out))
	}
	return nil
}

// GetHealthSummary returns summary statistics for a health metric.
func (h *HabitsService) GetHealthSummary(metric, period, scope string) (map[string]any, error) {
	if metric == "" {
		return nil, fmt.Errorf("metric is required")
	}
	if period == "" {
		period = "week"
	}

	now := time.Now().UTC()
	var startDate string
	switch period {
	case "week":
		startDate = now.AddDate(0, 0, -7).Format("2006-01-02")
	case "month":
		startDate = now.AddDate(0, -1, 0).Format("2006-01-02")
	case "year":
		startDate = now.AddDate(-1, 0, 0).Format("2006-01-02")
	default:
		startDate = now.AddDate(0, 0, -7).Format("2006-01-02")
	}

	scopeFilter := ""
	if scope != "" {
		scopeFilter = fmt.Sprintf(" AND scope = '%s'", escapeSQLite(scope))
	}

	// Aggregate stats.
	sql := fmt.Sprintf(
		`SELECT COUNT(*) as cnt, AVG(value) as avg_val, MIN(value) as min_val, MAX(value) as max_val,
		        COALESCE(SUM(value), 0) as total
		 FROM health_data
		 WHERE metric = '%s' AND date(recorded_at) >= '%s'%s`,
		escapeSQLite(metric), startDate, scopeFilter)

	rows, err := queryDB(h.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("health summary query: %w", err)
	}

	result := map[string]any{
		"metric":     metric,
		"period":     period,
		"start_date": startDate,
		"count":      0,
		"avg":        0.0,
		"min":        0.0,
		"max":        0.0,
		"total":      0.0,
		"trend":      "no_data",
	}

	if len(rows) > 0 && jsonFloat(rows[0]["cnt"]) > 0 {
		result["count"] = int(jsonFloat(rows[0]["cnt"]))
		result["avg"] = math.Round(jsonFloat(rows[0]["avg_val"])*100) / 100
		result["min"] = jsonFloat(rows[0]["min_val"])
		result["max"] = jsonFloat(rows[0]["max_val"])
		result["total"] = jsonFloat(rows[0]["total"])
	}

	// Trend: compare first half vs second half.
	sql = fmt.Sprintf(
		`SELECT value, recorded_at FROM health_data
		 WHERE metric = '%s' AND date(recorded_at) >= '%s'%s
		 ORDER BY recorded_at ASC`,
		escapeSQLite(metric), startDate, scopeFilter)

	dataRows, err := queryDB(h.dbPath, sql)
	if err == nil && len(dataRows) >= 4 {
		mid := len(dataRows) / 2
		firstHalf := 0.0
		secondHalf := 0.0
		for i, dr := range dataRows {
			v := jsonFloat(dr["value"])
			if i < mid {
				firstHalf += v
			} else {
				secondHalf += v
			}
		}
		firstAvg := firstHalf / float64(mid)
		secondAvg := secondHalf / float64(len(dataRows)-mid)
		if secondAvg > firstAvg*1.05 {
			result["trend"] = "increasing"
		} else if secondAvg < firstAvg*0.95 {
			result["trend"] = "decreasing"
		} else {
			result["trend"] = "stable"
		}
	}

	// Get the unit from the most recent entry.
	unitRows, err := queryDB(h.dbPath, fmt.Sprintf(
		`SELECT unit FROM health_data WHERE metric = '%s' ORDER BY recorded_at DESC LIMIT 1`,
		escapeSQLite(metric)))
	if err == nil && len(unitRows) > 0 {
		result["unit"] = jsonStr(unitRows[0]["unit"])
	}

	return result, nil
}

// CheckStreakAlerts checks for habits about to break their streak.
func (h *HabitsService) CheckStreakAlerts(scope string) ([]string, error) {
	scopeFilter := ""
	if scope != "" {
		scopeFilter = fmt.Sprintf(" AND scope = '%s'", escapeSQLite(scope))
	}

	// Get all active daily habits.
	rows, err := queryDB(h.dbPath, fmt.Sprintf(
		`SELECT id, name, target_count FROM habits
		 WHERE (archived_at = '' OR archived_at IS NULL) AND frequency = 'daily'%s`,
		scopeFilter))
	if err != nil {
		return nil, fmt.Errorf("check alerts: %w", err)
	}

	today := time.Now().UTC().Format("2006-01-02")
	var alerts []string

	for _, row := range rows {
		habitID := jsonStr(row["id"])
		name := jsonStr(row["name"])
		targetCount := int(jsonFloat(row["target_count"]))
		if targetCount < 1 {
			targetCount = 1
		}

		// Check if already completed today.
		todayRows, err := queryDB(h.dbPath, fmt.Sprintf(
			`SELECT COALESCE(SUM(value), 0) as total
			 FROM habit_logs
			 WHERE habit_id = '%s' AND date(logged_at) = '%s'`,
			escapeSQLite(habitID), today))
		if err != nil {
			continue
		}
		todayTotal := 0.0
		if len(todayRows) > 0 {
			todayTotal = jsonFloat(todayRows[0]["total"])
		}

		if todayTotal >= float64(targetCount) {
			continue // Already completed today.
		}

		// Check if there is an active streak.
		current, _, _ := h.GetStreak(habitID, scope)
		if current > 0 {
			alerts = append(alerts, fmt.Sprintf("Habit '%s' has a %d-day streak at risk! Not yet completed today.", name, current))
		}
	}

	if alerts == nil {
		alerts = []string{}
	}
	return alerts, nil
}

// --- Tool Handlers ---

// toolHabitCreate handles the habit_create tool.
func toolHabitCreate(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalHabitsService == nil {
		return "", fmt.Errorf("habits service not initialized")
	}

	var args struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Frequency   string `json:"frequency"`
		Category    string `json:"category"`
		TargetCount int    `json:"targetCount"`
		Scope       string `json:"scope"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	id, err := globalHabitsService.CreateHabit(
		args.Name, args.Description, args.Frequency, args.Category, args.Scope, args.TargetCount)
	if err != nil {
		return "", err
	}

	out, _ := json.MarshalIndent(map[string]any{
		"status":   "created",
		"habit_id": id,
		"name":     args.Name,
	}, "", "  ")
	return string(out), nil
}

// toolHabitLog handles the habit_log tool.
func toolHabitLog(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalHabitsService == nil {
		return "", fmt.Errorf("habits service not initialized")
	}

	var args struct {
		HabitID string  `json:"habitId"`
		Note    string  `json:"note"`
		Value   float64 `json:"value"`
		Scope   string  `json:"scope"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if err := globalHabitsService.LogHabit(args.HabitID, args.Note, args.Scope, args.Value); err != nil {
		return "", err
	}

	// Return current streak after logging.
	current, longest, _ := globalHabitsService.GetStreak(args.HabitID, args.Scope)

	out, _ := json.MarshalIndent(map[string]any{
		"status":         "logged",
		"habit_id":       args.HabitID,
		"current_streak": current,
		"longest_streak": longest,
	}, "", "  ")
	return string(out), nil
}

// toolHabitStatus handles the habit_status tool.
func toolHabitStatus(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalHabitsService == nil {
		return "", fmt.Errorf("habits service not initialized")
	}

	var args struct {
		Scope string `json:"scope"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	habits, err := globalHabitsService.HabitStatus(args.Scope)
	if err != nil {
		return "", err
	}

	out, _ := json.MarshalIndent(map[string]any{
		"habits": habits,
		"count":  len(habits),
	}, "", "  ")
	return string(out), nil
}

// toolHabitReport handles the habit_report tool.
func toolHabitReport(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalHabitsService == nil {
		return "", fmt.Errorf("habits service not initialized")
	}

	var args struct {
		HabitID string `json:"habitId"`
		Period  string `json:"period"`
		Scope   string `json:"scope"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	report, err := globalHabitsService.HabitReport(args.HabitID, args.Period, args.Scope)
	if err != nil {
		return "", err
	}

	out, _ := json.MarshalIndent(report, "", "  ")
	return string(out), nil
}

// toolHealthLog handles the health_log tool.
func toolHealthLog(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalHabitsService == nil {
		return "", fmt.Errorf("habits service not initialized")
	}

	var args struct {
		Metric string  `json:"metric"`
		Value  float64 `json:"value"`
		Unit   string  `json:"unit"`
		Source string  `json:"source"`
		Scope  string  `json:"scope"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if err := globalHabitsService.LogHealth(args.Metric, args.Value, args.Unit, args.Source, args.Scope); err != nil {
		return "", err
	}

	out, _ := json.MarshalIndent(map[string]any{
		"status": "logged",
		"metric": args.Metric,
		"value":  args.Value,
		"unit":   args.Unit,
	}, "", "  ")
	return string(out), nil
}

// toolHealthSummary handles the health_summary tool.
func toolHealthSummary(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalHabitsService == nil {
		return "", fmt.Errorf("habits service not initialized")
	}

	var args struct {
		Metric string `json:"metric"`
		Period string `json:"period"`
		Scope  string `json:"scope"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	summary, err := globalHabitsService.GetHealthSummary(args.Metric, args.Period, args.Scope)
	if err != nil {
		return "", err
	}

	out, _ := json.MarshalIndent(summary, "", "  ")
	return string(out), nil
}
