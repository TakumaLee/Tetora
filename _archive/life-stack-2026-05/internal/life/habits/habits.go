// Package habits implements habit tracking and health data logging.
package habits

import (
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"time"

	"tetora/internal/life/lifedb"
)

// Service manages habit tracking and health data.
type Service struct {
	db     lifedb.DB
	dbPath string
}

// New creates a new habits Service.
func New(dbPath string, db lifedb.DB) *Service {
	return &Service{
		db:     db,
		dbPath: dbPath,
	}
}

// DBPath returns the database file path.
func (h *Service) DBPath() string { return h.dbPath }

// InitDB creates the habits, habit_logs, and health_data tables.
func InitDB(dbPath string) error {
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

// CreateHabit creates a new habit and returns its ID.
func (h *Service) CreateHabit(id, name, description, frequency, category, scope string, targetCount int) error {
	if name == "" {
		return fmt.Errorf("habit name is required")
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

	now := time.Now().UTC().Format(time.RFC3339)

	sql := fmt.Sprintf(
		`INSERT INTO habits (id, name, description, frequency, target_count, category, created_at, scope)
		 VALUES ('%s', '%s', '%s', '%s', %d, '%s', '%s', '%s')`,
		h.db.Escape(id),
		h.db.Escape(name),
		h.db.Escape(description),
		h.db.Escape(frequency),
		targetCount,
		h.db.Escape(category),
		h.db.Escape(now),
		h.db.Escape(scope),
	)

	return h.db.Exec(h.dbPath, sql)
}

// LogHabit records a habit completion. The note is expected to be pre-encrypted if needed.
func (h *Service) LogHabit(logID, habitID, note, scope string, value float64) error {
	if habitID == "" {
		return fmt.Errorf("habit_id is required")
	}

	rows, err := h.db.Query(h.dbPath, fmt.Sprintf(
		`SELECT id, archived_at FROM habits WHERE id = '%s'`, h.db.Escape(habitID)))
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

	now := time.Now().UTC().Format(time.RFC3339)

	sql := fmt.Sprintf(
		`INSERT INTO habit_logs (id, habit_id, logged_at, value, note, scope)
		 VALUES ('%s', '%s', '%s', %f, '%s', '%s')`,
		h.db.Escape(logID),
		h.db.Escape(habitID),
		h.db.Escape(now),
		value,
		h.db.Escape(note),
		h.db.Escape(scope),
	)

	return h.db.Exec(h.dbPath, sql)
}

// GetStreak calculates the current and longest streak for a habit.
func (h *Service) GetStreak(habitID, scope string) (current int, longest int, err error) {
	rows, err := h.db.Query(h.dbPath, fmt.Sprintf(
		`SELECT frequency, target_count FROM habits WHERE id = '%s'`, h.db.Escape(habitID)))
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

	scopeFilter := ""
	if scope != "" {
		scopeFilter = fmt.Sprintf(" AND scope = '%s'", h.db.Escape(scope))
	}

	if frequency == "weekly" {
		return h.getWeeklyStreak(habitID, targetCount, scopeFilter)
	}
	return h.getDailyStreak(habitID, targetCount, scopeFilter)
}

func (h *Service) getDailyStreak(habitID string, targetCount int, scopeFilter string) (current int, longest int, err error) {
	sql := fmt.Sprintf(
		`SELECT date(logged_at) as log_date, SUM(value) as total
		 FROM habit_logs
		 WHERE habit_id = '%s'%s
		 GROUP BY log_date
		 HAVING total >= %d
		 ORDER BY log_date DESC`,
		h.db.Escape(habitID), scopeFilter, targetCount)

	rows, err := h.db.Query(h.dbPath, sql)
	if err != nil {
		return 0, 0, fmt.Errorf("query streak: %w", err)
	}

	if len(rows) == 0 {
		return 0, 0, nil
	}

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

func (h *Service) getWeeklyStreak(habitID string, targetCount int, scopeFilter string) (current int, longest int, err error) {
	sql := fmt.Sprintf(
		`SELECT strftime('%%Y-W%%W', logged_at) as log_week, SUM(value) as total
		 FROM habit_logs
		 WHERE habit_id = '%s'%s
		 GROUP BY log_week
		 HAVING total >= %d
		 ORDER BY log_week DESC`,
		h.db.Escape(habitID), scopeFilter, targetCount)

	rows, err := h.db.Query(h.dbPath, sql)
	if err != nil {
		return 0, 0, fmt.Errorf("query weekly streak: %w", err)
	}

	if len(rows) == 0 {
		return 0, 0, nil
	}

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

func isoWeekNumber(t time.Time) int {
	_, week := t.ISOWeek()
	return week
}

func consecutiveWeeks(earlier, later string) bool {
	var y1, w1, y2, w2 int
	fmt.Sscanf(earlier, "%d-W%d", &y1, &w1)
	fmt.Sscanf(later, "%d-W%d", &y2, &w2)

	if y1 == y2 {
		return w2-w1 == 1
	}
	if y2 == y1+1 && w2 == 1 {
		return w1 >= 52
	}
	return false
}

// HabitStatus returns all active habits with current streak and today's completion.
// logWarn is a callback for non-fatal errors during per-habit queries.
func (h *Service) HabitStatus(scope string, logWarn func(msg string, args ...any)) ([]map[string]any, error) {
	if logWarn == nil {
		logWarn = func(string, ...any) {}
	}

	scopeFilter := ""
	if scope != "" {
		scopeFilter = fmt.Sprintf(" AND scope = '%s'", h.db.Escape(scope))
	}

	rows, err := h.db.Query(h.dbPath, fmt.Sprintf(
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

		todayRows, err := h.db.Query(h.dbPath, fmt.Sprintf(
			`SELECT COALESCE(SUM(value), 0) as today_total
			 FROM habit_logs
			 WHERE habit_id = '%s' AND date(logged_at) = '%s'`,
			h.db.Escape(habitID), today))
		if err != nil {
			logWarn("habits: status query failed", "habit_id", habitID, "error", err)
			continue
		}
		todayTotal := 0.0
		if len(todayRows) > 0 {
			todayTotal = jsonFloat(todayRows[0]["today_total"])
		}

		rateRows, err := h.db.Query(h.dbPath, fmt.Sprintf(
			`SELECT COUNT(DISTINCT date(logged_at)) as completed_days
			 FROM habit_logs
			 WHERE habit_id = '%s' AND date(logged_at) >= '%s'`,
			h.db.Escape(habitID), thirtyDaysAgo))
		if err != nil {
			logWarn("habits: rate query failed", "habit_id", habitID, "error", err)
			continue
		}
		completedDays := 0.0
		if len(rateRows) > 0 {
			completedDays = jsonFloat(rateRows[0]["completed_days"])
		}
		completionRate := completedDays / 30.0

		currentStreak, longestStreak, _ := h.GetStreak(habitID, scope)

		status := map[string]any{
			"id":              habitID,
			"name":            jsonStr(row["name"]),
			"description":     jsonStr(row["description"]),
			"frequency":       jsonStr(row["frequency"]),
			"target_count":    targetCount,
			"category":        jsonStr(row["category"]),
			"color":           jsonStr(row["color"]),
			"today_count":     todayTotal,
			"today_complete":  todayTotal >= float64(targetCount),
			"current_streak":  currentStreak,
			"longest_streak":  longestStreak,
			"completion_rate": math.Round(completionRate*1000) / 10,
		}
		result = append(result, status)
	}

	if result == nil {
		result = []map[string]any{}
	}
	return result, nil
}

// HabitReport generates a detailed report for a habit or all habits.
func (h *Service) HabitReport(habitID, period, scope string) (map[string]any, error) {
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

	endDate := now.Format("2006-01-02")

	scopeFilter := ""
	if scope != "" {
		scopeFilter = fmt.Sprintf(" AND scope = '%s'", h.db.Escape(scope))
	}

	habitFilter := ""
	if habitID != "" {
		habitFilter = fmt.Sprintf(" AND habit_id = '%s'", h.db.Escape(habitID))
	}

	sql := fmt.Sprintf(
		`SELECT COUNT(*) as total_logs, COALESCE(SUM(value), 0) as total_value
		 FROM habit_logs
		 WHERE date(logged_at) >= '%s' AND date(logged_at) <= '%s'%s%s`,
		startDate, endDate, habitFilter, scopeFilter)
	rows, err := h.db.Query(h.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("report query: %w", err)
	}

	totalLogs := 0.0
	totalValue := 0.0
	if len(rows) > 0 {
		totalLogs = jsonFloat(rows[0]["total_logs"])
		totalValue = jsonFloat(rows[0]["total_value"])
	}

	sql = fmt.Sprintf(
		`SELECT date(logged_at) as log_date, SUM(value) as day_total
		 FROM habit_logs
		 WHERE date(logged_at) >= '%s' AND date(logged_at) <= '%s'%s%s
		 GROUP BY log_date
		 ORDER BY log_date ASC`,
		startDate, endDate, habitFilter, scopeFilter)
	dayRows, err := h.db.Query(h.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("day breakdown query: %w", err)
	}

	start, _ := time.Parse("2006-01-02", startDate)
	end, _ := time.Parse("2006-01-02", endDate)
	totalDays := int(end.Sub(start).Hours()/24) + 1
	completedDays := len(dayRows)
	completionRate := 0.0
	if totalDays > 0 {
		completionRate = float64(completedDays) / float64(totalDays) * 100
	}

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
	}
	_ = worstAvg

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
func (h *Service) LogHealth(id, metric string, value float64, unit, source, scope string) error {
	if metric == "" {
		return fmt.Errorf("metric is required")
	}
	if source == "" {
		source = "manual"
	}

	now := time.Now().UTC().Format(time.RFC3339)

	sql := fmt.Sprintf(
		`INSERT INTO health_data (id, metric, value, unit, recorded_at, source, scope)
		 VALUES ('%s', '%s', %f, '%s', '%s', '%s', '%s')`,
		h.db.Escape(id),
		h.db.Escape(metric),
		value,
		h.db.Escape(unit),
		h.db.Escape(now),
		h.db.Escape(source),
		h.db.Escape(scope),
	)

	return h.db.Exec(h.dbPath, sql)
}

// GetHealthSummary returns summary statistics for a health metric.
func (h *Service) GetHealthSummary(metric, period, scope string) (map[string]any, error) {
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
		scopeFilter = fmt.Sprintf(" AND scope = '%s'", h.db.Escape(scope))
	}

	sql := fmt.Sprintf(
		`SELECT COUNT(*) as cnt, AVG(value) as avg_val, MIN(value) as min_val, MAX(value) as max_val,
		        COALESCE(SUM(value), 0) as total
		 FROM health_data
		 WHERE metric = '%s' AND date(recorded_at) >= '%s'%s`,
		h.db.Escape(metric), startDate, scopeFilter)

	rows, err := h.db.Query(h.dbPath, sql)
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

	sql = fmt.Sprintf(
		`SELECT value, recorded_at FROM health_data
		 WHERE metric = '%s' AND date(recorded_at) >= '%s'%s
		 ORDER BY recorded_at ASC`,
		h.db.Escape(metric), startDate, scopeFilter)

	dataRows, err := h.db.Query(h.dbPath, sql)
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

	unitRows, err := h.db.Query(h.dbPath, fmt.Sprintf(
		`SELECT unit FROM health_data WHERE metric = '%s' ORDER BY recorded_at DESC LIMIT 1`,
		h.db.Escape(metric)))
	if err == nil && len(unitRows) > 0 {
		result["unit"] = jsonStr(unitRows[0]["unit"])
	}

	return result, nil
}

// CheckStreakAlerts checks for habits about to break their streak.
func (h *Service) CheckStreakAlerts(scope string) ([]string, error) {
	scopeFilter := ""
	if scope != "" {
		scopeFilter = fmt.Sprintf(" AND scope = '%s'", h.db.Escape(scope))
	}

	rows, err := h.db.Query(h.dbPath, fmt.Sprintf(
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

		todayRows, err := h.db.Query(h.dbPath, fmt.Sprintf(
			`SELECT COALESCE(SUM(value), 0) as total
			 FROM habit_logs
			 WHERE habit_id = '%s' AND date(logged_at) = '%s'`,
			h.db.Escape(habitID), today))
		if err != nil {
			continue
		}
		todayTotal := 0.0
		if len(todayRows) > 0 {
			todayTotal = jsonFloat(todayRows[0]["total"])
		}

		if todayTotal >= float64(targetCount) {
			continue
		}

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

// --- JSON helpers ---

func jsonStr(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func jsonFloat(v any) float64 {
	if v == nil {
		return 0
	}
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case string:
		var f float64
		fmt.Sscanf(x, "%f", &f)
		return f
	}
	return 0
}

// UnmarshalTags parses a JSON tags string into a string slice.
func UnmarshalTags(s string) []string {
	if s == "" {
		return nil
	}
	var tags []string
	json.Unmarshal([]byte(s), &tags)
	return tags
}
