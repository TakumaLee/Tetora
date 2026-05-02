package insights

import (
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"strings"
	"time"
)

// QueryFn executes a SQL query and returns rows.
type QueryFn func(dbPath, sql string) ([]map[string]any, error)

// EscapeFn escapes a string for SQLite.
type EscapeFn func(s string) string

// LogFn is a log function.
type LogFn func(msg string, keyvals ...any)

// UUIDFn generates a UUID string.
type UUIDFn func() string

// Deps holds injected dependencies for the insights engine.
type Deps struct {
	Query   QueryFn
	Escape  EscapeFn
	LogWarn LogFn
	UUID    UUIDFn

	// Per-service DB paths. Empty string = service not available.
	FinanceDBPath  string
	TasksDBPath    string
	ProfileDBPath  string
	ContactsDBPath string
	HabitsDBPath   string

	// Optional: habits GetStreak function (nil = not available)
	GetHabitStreak func(habitID, userID string) (current, longest int, err error)
}

// Engine provides cross-data-source behavior analysis and proactive insights.
type Engine struct {
	dbPath string
	deps   Deps
}

// New creates a new Engine with the given DB path and dependencies.
func New(dbPath string, deps Deps) *Engine {
	return &Engine{dbPath: dbPath, deps: deps}
}

// DBPath returns the engine's database path.
func (e *Engine) DBPath() string { return e.dbPath }

// LifeInsight represents a detected behavioral insight or anomaly.
type LifeInsight struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"`
	Severity     string         `json:"severity"`
	Title        string         `json:"title"`
	Description  string         `json:"description"`
	Data         map[string]any `json:"data,omitempty"`
	Acknowledged bool           `json:"acknowledged"`
	CreatedAt    string         `json:"created_at"`
}

// LifeReport is a comprehensive cross-domain life report for a period.
type LifeReport struct {
	Period      string          `json:"period"`
	StartDate   string          `json:"start_date"`
	EndDate     string          `json:"end_date"`
	Spending    *SpendingReport `json:"spending,omitempty"`
	Tasks       *TasksReport    `json:"tasks,omitempty"`
	Mood        *MoodReport     `json:"mood,omitempty"`
	Social      *SocialReport   `json:"social,omitempty"`
	Habits      *HabitsReport   `json:"habits,omitempty"`
	Insights    []LifeInsight   `json:"insights"`
	GeneratedAt string          `json:"generated_at"`
}

// SpendingReport summarizes expense data for a period.
type SpendingReport struct {
	Total        float64            `json:"total"`
	ByCategory   map[string]float64 `json:"by_category"`
	DailyAverage float64            `json:"daily_average"`
	TopExpense   string             `json:"top_expense,omitempty"`
	VsPrevPeriod float64            `json:"vs_prev_period_pct"`
}

// TasksReport summarizes task activity for a period.
type TasksReport struct {
	Completed      int     `json:"completed"`
	Created        int     `json:"created"`
	Overdue        int     `json:"overdue"`
	CompletionRate float64 `json:"completion_rate"`
}

// MoodReport summarizes sentiment data for a period.
type MoodReport struct {
	AverageScore float64            `json:"average_score"`
	Trend        string             `json:"trend"`
	ByDay        map[string]float64 `json:"by_day,omitempty"`
}

// SocialReport summarizes contact interaction data for a period.
type SocialReport struct {
	InteractionCount int    `json:"interaction_count"`
	UniqueContacts   int    `json:"unique_contacts"`
	MostContacted    string `json:"most_contacted,omitempty"`
}

// HabitsReport summarizes habit tracking data for a period.
type HabitsReport struct {
	ActiveHabits   int     `json:"active_habits"`
	CompletionRate float64 `json:"completion_rate"`
	LongestStreak  int     `json:"longest_streak"`
	BestHabit      string  `json:"best_habit,omitempty"`
}

// InitDB creates the life_insights table.
func InitDB(dbPath string) error {
	ddl := `CREATE TABLE IF NOT EXISTS life_insights (
        id TEXT PRIMARY KEY,
        type TEXT NOT NULL,
        severity TEXT NOT NULL,
        title TEXT NOT NULL,
        description TEXT NOT NULL,
        data TEXT DEFAULT '{}',
        acknowledged INTEGER DEFAULT 0,
        created_at TEXT
    );
    CREATE INDEX IF NOT EXISTS idx_insights_type ON life_insights(type);
    CREATE INDEX IF NOT EXISTS idx_insights_created ON life_insights(created_at);`
	cmd := exec.Command("sqlite3", dbPath, ddl)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("init insights tables: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// GenerateReport produces a comprehensive cross-domain life report.
// period: "daily", "weekly", or "monthly". targetDate anchors the period.
func (e *Engine) GenerateReport(period string, targetDate time.Time) (*LifeReport, error) {
	start, end := PeriodDateRange(period, targetDate)
	prevStart, prevEnd := PrevPeriodRange(period, start)

	report := &LifeReport{
		Period:      period,
		StartDate:   start.Format("2006-01-02"),
		EndDate:     end.Format("2006-01-02"),
		Insights:    []LifeInsight{},
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}

	if e.deps.FinanceDBPath != "" {
		spending := e.buildSpendingReport(start, end, prevStart, prevEnd)
		if spending != nil {
			report.Spending = spending
		}
	}

	if e.deps.TasksDBPath != "" {
		tasks := e.buildTasksReport(start, end)
		if tasks != nil {
			report.Tasks = tasks
		}
	}

	if e.deps.ProfileDBPath != "" {
		mood := e.buildMoodReport(start, end)
		if mood != nil {
			report.Mood = mood
		}
	}

	if e.deps.ContactsDBPath != "" {
		social := e.buildSocialReport(start, end)
		if social != nil {
			report.Social = social
		}
	}

	if e.deps.HabitsDBPath != "" {
		habits := e.buildHabitsReport(start, end)
		if habits != nil {
			report.Habits = habits
		}
	}

	anomalies, err := e.DetectAnomalies(7)
	if err == nil && len(anomalies) > 0 {
		report.Insights = anomalies
	}

	return report, nil
}

func (e *Engine) buildSpendingReport(start, end, prevStart, prevEnd time.Time) *SpendingReport {
	dbPath := e.deps.FinanceDBPath
	startStr := start.Format("2006-01-02")
	endStr := end.Format("2006-01-02")

	sql := fmt.Sprintf(
		`SELECT category, SUM(amount) as total, description
		 FROM expenses
		 WHERE date >= '%s' AND date <= '%s'
		 GROUP BY category`,
		e.deps.Escape(startStr), e.deps.Escape(endStr))

	rows, err := e.deps.Query(dbPath, sql)
	if err != nil {
		e.deps.LogWarn("insights: spending query failed", "error", err)
		return nil
	}

	byCategory := make(map[string]float64)
	total := 0.0
	for _, row := range rows {
		cat := jsonStr(row["category"])
		amt := jsonFloat(row["total"])
		byCategory[cat] = math.Round(amt*100) / 100
		total += amt
	}

	topExpense := ""
	topSQL := fmt.Sprintf(
		`SELECT description, amount FROM expenses
		 WHERE date >= '%s' AND date <= '%s'
		 ORDER BY amount DESC LIMIT 1`,
		e.deps.Escape(startStr), e.deps.Escape(endStr))
	topRows, err := e.deps.Query(dbPath, topSQL)
	if err == nil && len(topRows) > 0 {
		desc := jsonStr(topRows[0]["description"])
		amt := jsonFloat(topRows[0]["amount"])
		if desc != "" {
			topExpense = fmt.Sprintf("%s (%.0f)", desc, amt)
		}
	}

	days := end.Sub(start).Hours()/24 + 1
	if days < 1 {
		days = 1
	}
	dailyAvg := math.Round(total/days*100) / 100

	prevStartStr := prevStart.Format("2006-01-02")
	prevEndStr := prevEnd.Format("2006-01-02")
	prevSQL := fmt.Sprintf(
		`SELECT SUM(amount) as total FROM expenses
		 WHERE date >= '%s' AND date <= '%s'`,
		e.deps.Escape(prevStartStr), e.deps.Escape(prevEndStr))
	prevRows, err := e.deps.Query(dbPath, prevSQL)
	prevTotal := 0.0
	if err == nil && len(prevRows) > 0 {
		prevTotal = jsonFloat(prevRows[0]["total"])
	}

	vsPrev := 0.0
	if prevTotal > 0 {
		vsPrev = math.Round((total-prevTotal)/prevTotal*10000) / 100
	}

	return &SpendingReport{
		Total:        math.Round(total*100) / 100,
		ByCategory:   byCategory,
		DailyAverage: dailyAvg,
		TopExpense:   topExpense,
		VsPrevPeriod: vsPrev,
	}
}

func (e *Engine) buildTasksReport(start, end time.Time) *TasksReport {
	dbPath := e.deps.TasksDBPath
	startStr := start.Format(time.RFC3339)
	endStr := end.Format(time.RFC3339)
	now := time.Now().UTC().Format(time.RFC3339)

	report := &TasksReport{}

	sql := fmt.Sprintf(
		`SELECT COUNT(*) as cnt FROM user_tasks
		 WHERE status = 'done' AND completed_at >= '%s' AND completed_at <= '%s'`,
		e.deps.Escape(startStr), e.deps.Escape(endStr))
	rows, err := e.deps.Query(dbPath, sql)
	if err == nil && len(rows) > 0 {
		report.Completed = jsonInt(rows[0]["cnt"])
	}

	sql = fmt.Sprintf(
		`SELECT COUNT(*) as cnt FROM user_tasks
		 WHERE created_at >= '%s' AND created_at <= '%s'`,
		e.deps.Escape(startStr), e.deps.Escape(endStr))
	rows, err = e.deps.Query(dbPath, sql)
	if err == nil && len(rows) > 0 {
		report.Created = jsonInt(rows[0]["cnt"])
	}

	sql = fmt.Sprintf(
		`SELECT COUNT(*) as cnt FROM user_tasks
		 WHERE due_at != '' AND due_at < '%s' AND status NOT IN ('done','cancelled')`,
		e.deps.Escape(now))
	rows, err = e.deps.Query(dbPath, sql)
	if err == nil && len(rows) > 0 {
		report.Overdue = jsonInt(rows[0]["cnt"])
	}

	if report.Created > 0 {
		report.CompletionRate = math.Round(float64(report.Completed)/float64(report.Created)*10000) / 100
	}

	return report
}

func (e *Engine) buildMoodReport(start, end time.Time) *MoodReport {
	dbPath := e.deps.ProfileDBPath
	startStr := start.Format(time.RFC3339)
	endStr := end.Format(time.RFC3339)

	sql := fmt.Sprintf(
		`SELECT date(created_at) as day, AVG(sentiment_score) as avg_score
		 FROM user_mood_log
		 WHERE created_at >= '%s' AND created_at <= '%s'
		 GROUP BY day ORDER BY day`,
		e.deps.Escape(startStr), e.deps.Escape(endStr))
	rows, err := e.deps.Query(dbPath, sql)
	if err != nil {
		e.deps.LogWarn("insights: mood query failed", "error", err)
		return nil
	}
	if len(rows) == 0 {
		return nil
	}

	byDay := make(map[string]float64)
	totalScore := 0.0
	for _, row := range rows {
		day := jsonStr(row["day"])
		score := jsonFloat(row["avg_score"])
		byDay[day] = math.Round(score*1000) / 1000
		totalScore += score
	}

	avgScore := math.Round(totalScore/float64(len(rows))*1000) / 1000

	trend := "stable"
	if len(rows) >= 2 {
		mid := len(rows) / 2
		firstHalf := 0.0
		for i := 0; i < mid; i++ {
			firstHalf += jsonFloat(rows[i]["avg_score"])
		}
		firstHalf /= float64(mid)

		secondHalf := 0.0
		count := 0
		for i := mid; i < len(rows); i++ {
			secondHalf += jsonFloat(rows[i]["avg_score"])
			count++
		}
		if count > 0 {
			secondHalf /= float64(count)
		}

		diff := secondHalf - firstHalf
		if diff > 0.15 {
			trend = "improving"
		} else if diff < -0.15 {
			trend = "declining"
		}
	}

	return &MoodReport{
		AverageScore: avgScore,
		Trend:        trend,
		ByDay:        byDay,
	}
}

func (e *Engine) buildSocialReport(start, end time.Time) *SocialReport {
	dbPath := e.deps.ContactsDBPath
	startStr := start.Format(time.RFC3339)
	endStr := end.Format(time.RFC3339)

	sql := fmt.Sprintf(
		`SELECT COUNT(*) as cnt, COUNT(DISTINCT contact_id) as unique_contacts
		 FROM contact_interactions
		 WHERE created_at >= '%s' AND created_at <= '%s'`,
		e.deps.Escape(startStr), e.deps.Escape(endStr))
	rows, err := e.deps.Query(dbPath, sql)
	if err != nil {
		e.deps.LogWarn("insights: social query failed", "error", err)
		return nil
	}

	report := &SocialReport{}
	if len(rows) > 0 {
		report.InteractionCount = jsonInt(rows[0]["cnt"])
		report.UniqueContacts = jsonInt(rows[0]["unique_contacts"])
	}

	topSQL := fmt.Sprintf(
		`SELECT ci.contact_id, c.name, COUNT(*) as cnt
		 FROM contact_interactions ci
		 LEFT JOIN contacts c ON ci.contact_id = c.id
		 WHERE ci.created_at >= '%s' AND ci.created_at <= '%s'
		 GROUP BY ci.contact_id
		 ORDER BY cnt DESC LIMIT 1`,
		e.deps.Escape(startStr), e.deps.Escape(endStr))
	topRows, err := e.deps.Query(dbPath, topSQL)
	if err == nil && len(topRows) > 0 {
		name := jsonStr(topRows[0]["name"])
		if name != "" {
			report.MostContacted = name
		}
	}

	return report
}

func (e *Engine) buildHabitsReport(start, end time.Time) *HabitsReport {
	dbPath := e.deps.HabitsDBPath
	startStr := start.Format(time.RFC3339)
	endStr := end.Format(time.RFC3339)

	rows, err := e.deps.Query(dbPath,
		`SELECT COUNT(*) as cnt FROM habits WHERE archived_at = ''`)
	if err != nil {
		e.deps.LogWarn("insights: habits query failed", "error", err)
		return nil
	}
	activeHabits := 0
	if len(rows) > 0 {
		activeHabits = jsonInt(rows[0]["cnt"])
	}
	if activeHabits == 0 {
		return nil
	}

	logSQL := fmt.Sprintf(
		`SELECT COUNT(DISTINCT habit_id) as logged_habits, COUNT(*) as total_logs
		 FROM habit_logs
		 WHERE logged_at >= '%s' AND logged_at <= '%s'`,
		e.deps.Escape(startStr), e.deps.Escape(endStr))
	logRows, err := e.deps.Query(dbPath, logSQL)
	if err != nil {
		e.deps.LogWarn("insights: habit logs query failed", "error", err)
		return nil
	}

	loggedHabits := 0
	if len(logRows) > 0 {
		loggedHabits = jsonInt(logRows[0]["logged_habits"])
	}

	completionRate := 0.0
	if activeHabits > 0 {
		completionRate = math.Round(float64(loggedHabits)/float64(activeHabits)*10000) / 100
	}

	bestHabit := ""
	longestStreak := 0
	bestSQL := fmt.Sprintf(
		`SELECT hl.habit_id, h.name, COUNT(*) as cnt
		 FROM habit_logs hl
		 JOIN habits h ON hl.habit_id = h.id
		 WHERE hl.logged_at >= '%s' AND hl.logged_at <= '%s'
		 GROUP BY hl.habit_id
		 ORDER BY cnt DESC LIMIT 1`,
		e.deps.Escape(startStr), e.deps.Escape(endStr))
	bestRows, err := e.deps.Query(dbPath, bestSQL)
	if err == nil && len(bestRows) > 0 {
		bestHabit = jsonStr(bestRows[0]["name"])
		habitID := jsonStr(bestRows[0]["habit_id"])
		if habitID != "" && e.deps.GetHabitStreak != nil {
			_, longest, err := e.deps.GetHabitStreak(habitID, "")
			if err == nil {
				longestStreak = longest
			}
		}
	}

	return &HabitsReport{
		ActiveHabits:   activeHabits,
		CompletionRate: completionRate,
		LongestStreak:  longestStreak,
		BestHabit:      bestHabit,
	}
}

// DetectAnomalies scans recent data for behavioral anomalies.
// days: how far back to look for anomalies.
func (e *Engine) DetectAnomalies(days int) ([]LifeInsight, error) {
	if days <= 0 {
		days = 7
	}

	var insights []LifeInsight
	today := time.Now().UTC().Format("2006-01-02")

	if e.deps.FinanceDBPath != "" {
		spendingInsights := e.detectSpendingAnomalies(days, today)
		insights = append(insights, spendingInsights...)
	}

	if e.deps.ProfileDBPath != "" {
		moodInsights := e.detectMoodAnomalies(days, today)
		insights = append(insights, moodInsights...)
	}

	if e.deps.TasksDBPath != "" {
		taskInsights := e.detectTaskAnomalies(today)
		insights = append(insights, taskInsights...)
	}

	if e.deps.ContactsDBPath != "" {
		socialInsights := e.detectSocialAnomalies(today)
		insights = append(insights, socialInsights...)
	}

	if e.deps.HabitsDBPath != "" {
		habitInsights := e.detectHabitAnomalies(today)
		insights = append(insights, habitInsights...)
	}

	for i := range insights {
		if insights[i].ID == "" {
			insights[i].ID = e.deps.UUID()
		}
		if insights[i].CreatedAt == "" {
			insights[i].CreatedAt = time.Now().UTC().Format(time.RFC3339)
		}
		e.StoreInsightDedup(&insights[i])
	}

	return insights, nil
}

func (e *Engine) detectSpendingAnomalies(days int, today string) []LifeInsight {
	dbPath := e.deps.FinanceDBPath

	thirtyDaysAgo := time.Now().UTC().AddDate(0, 0, -30).Format("2006-01-02")
	avgSQL := fmt.Sprintf(
		`SELECT AVG(daily_total) as avg_daily FROM (
			SELECT date, SUM(amount) as daily_total FROM expenses
			WHERE date >= '%s' GROUP BY date
		)`, e.deps.Escape(thirtyDaysAgo))
	avgRows, err := e.deps.Query(dbPath, avgSQL)
	if err != nil {
		return nil
	}
	dailyAvg := 0.0
	if len(avgRows) > 0 {
		dailyAvg = jsonFloat(avgRows[0]["avg_daily"])
	}
	if dailyAvg <= 0 {
		return nil
	}

	recentStart := time.Now().UTC().AddDate(0, 0, -days).Format("2006-01-02")
	spikeSQL := fmt.Sprintf(
		`SELECT date, SUM(amount) as daily_total FROM expenses
		 WHERE date >= '%s'
		 GROUP BY date
		 HAVING daily_total > %f`,
		e.deps.Escape(recentStart), dailyAvg*2)
	spikeRows, err := e.deps.Query(dbPath, spikeSQL)
	if err != nil || len(spikeRows) == 0 {
		return nil
	}

	var insights []LifeInsight
	for _, row := range spikeRows {
		dt := jsonStr(row["date"])
		amount := jsonFloat(row["daily_total"])
		insights = append(insights, LifeInsight{
			Type:        "spending_anomaly",
			Severity:    "warning",
			Title:       fmt.Sprintf("High spending on %s", dt),
			Description: fmt.Sprintf("Spent %.0f on %s, which is %.1fx your daily average of %.0f.", amount, dt, amount/dailyAvg, dailyAvg),
			Data: map[string]any{
				"date":          dt,
				"amount":        amount,
				"daily_average": dailyAvg,
				"ratio":         math.Round(amount/dailyAvg*10) / 10,
			},
		})
	}
	return insights
}

func (e *Engine) detectMoodAnomalies(days int, today string) []LifeInsight {
	dbPath := e.deps.ProfileDBPath

	recentStart := time.Now().UTC().AddDate(0, 0, -days).Format(time.RFC3339)

	sql := fmt.Sprintf(
		`SELECT date(created_at) as day, AVG(sentiment_score) as avg_score
		 FROM user_mood_log
		 WHERE created_at >= '%s'
		 GROUP BY day ORDER BY day`,
		e.deps.Escape(recentStart))
	rows, err := e.deps.Query(dbPath, sql)
	if err != nil || len(rows) == 0 {
		return nil
	}

	var insights []LifeInsight

	if len(rows) >= 3 {
		decliningDays := 0
		for i := 1; i < len(rows); i++ {
			curr := jsonFloat(rows[i]["avg_score"])
			prev := jsonFloat(rows[i-1]["avg_score"])
			if curr < prev {
				decliningDays++
			} else {
				decliningDays = 0
			}
		}
		if decliningDays >= 3 {
			insights = append(insights, LifeInsight{
				Type:        "mood_shift",
				Severity:    "warning",
				Title:       "Declining mood trend detected",
				Description: fmt.Sprintf("Your mood has been declining for %d consecutive days.", decliningDays),
				Data: map[string]any{
					"declining_days": decliningDays,
				},
			})
		}
	}

	totalScore := 0.0
	for _, row := range rows {
		totalScore += jsonFloat(row["avg_score"])
	}
	avgScore := totalScore / float64(len(rows))
	if avgScore < -0.3 {
		insights = append(insights, LifeInsight{
			Type:        "mood_shift",
			Severity:    "alert",
			Title:       "Low mood period",
			Description: fmt.Sprintf("Your average mood score over the last %d days is %.2f, indicating a difficult period.", days, avgScore),
			Data: map[string]any{
				"average_score": avgScore,
				"days":          days,
			},
		})
	}

	return insights
}

func (e *Engine) detectTaskAnomalies(today string) []LifeInsight {
	dbPath := e.deps.TasksDBPath

	sql := fmt.Sprintf(
		`SELECT COUNT(*) as cnt FROM user_tasks
		 WHERE due_at != '' AND due_at < '%s' AND status NOT IN ('done','cancelled')`,
		e.deps.Escape(today+"T23:59:59Z"))
	rows, err := e.deps.Query(dbPath, sql)
	if err != nil || len(rows) == 0 {
		return nil
	}

	overdue := jsonInt(rows[0]["cnt"])
	if overdue <= 10 {
		return nil
	}

	return []LifeInsight{{
		Type:        "task_overload",
		Severity:    "warning",
		Title:       "Task overload detected",
		Description: fmt.Sprintf("You have %d overdue tasks. Consider prioritizing or rescheduling some.", overdue),
		Data: map[string]any{
			"overdue_count": overdue,
		},
	}}
}

func (e *Engine) detectSocialAnomalies(today string) []LifeInsight {
	dbPath := e.deps.ContactsDBPath

	fourteenDaysAgo := time.Now().UTC().AddDate(0, 0, -14).Format(time.RFC3339)
	sql := fmt.Sprintf(
		`SELECT COUNT(*) as cnt FROM contact_interactions
		 WHERE created_at >= '%s'`,
		e.deps.Escape(fourteenDaysAgo))
	rows, err := e.deps.Query(dbPath, sql)
	if err != nil {
		return nil
	}

	count := 0
	if len(rows) > 0 {
		count = jsonInt(rows[0]["cnt"])
	}
	if count > 0 {
		return nil
	}

	allSQL := `SELECT COUNT(*) as cnt FROM contact_interactions`
	allRows, err := e.deps.Query(dbPath, allSQL)
	if err != nil {
		return nil
	}
	totalInteractions := 0
	if len(allRows) > 0 {
		totalInteractions = jsonInt(allRows[0]["cnt"])
	}
	if totalInteractions == 0 {
		return nil
	}

	return []LifeInsight{{
		Type:        "social_isolation",
		Severity:    "info",
		Title:       "No recent social interactions",
		Description: "No contact interactions have been logged in the last 14 days. Consider reaching out to someone.",
		Data: map[string]any{
			"days_since_last": 14,
		},
	}}
}

func (e *Engine) detectHabitAnomalies(today string) []LifeInsight {
	dbPath := e.deps.HabitsDBPath

	rows, err := e.deps.Query(dbPath,
		`SELECT id, name FROM habits WHERE archived_at = ''`)
	if err != nil || len(rows) == 0 {
		return nil
	}

	var insights []LifeInsight
	for _, row := range rows {
		habitID := jsonStr(row["id"])
		habitName := jsonStr(row["name"])
		if habitID == "" {
			continue
		}

		if e.deps.GetHabitStreak == nil {
			continue
		}
		current, longest, err := e.deps.GetHabitStreak(habitID, "")
		if err != nil {
			continue
		}

		if current == 0 && longest >= 7 {
			insights = append(insights, LifeInsight{
				Type:        "habit_streak",
				Severity:    "info",
				Title:       fmt.Sprintf("Broken streak: %s", habitName),
				Description: fmt.Sprintf("Your streak for '%s' was %d days but has been broken. Time to get back on track!", habitName, longest),
				Data: map[string]any{
					"habit_id":       habitID,
					"habit_name":     habitName,
					"longest_streak": longest,
				},
			})
		}
	}

	return insights
}

// StoreInsightDedup stores an insight, deduplicating by type+date.
func (e *Engine) StoreInsightDedup(insight *LifeInsight) {
	today := time.Now().UTC().Format("2006-01-02")

	checkSQL := fmt.Sprintf(
		`SELECT id FROM life_insights WHERE type = '%s' AND date(created_at) = '%s' LIMIT 1`,
		e.deps.Escape(insight.Type), e.deps.Escape(today))
	rows, err := e.deps.Query(e.dbPath, checkSQL)
	if err != nil {
		e.deps.LogWarn("insights: dedup check failed", "error", err)
		return
	}
	if len(rows) > 0 {
		insight.ID = jsonStr(rows[0]["id"])
		return
	}

	dataJSON, _ := json.Marshal(insight.Data)

	sql := fmt.Sprintf(
		`INSERT INTO life_insights (id, type, severity, title, description, data, acknowledged, created_at)
		 VALUES ('%s','%s','%s','%s','%s','%s',0,'%s')`,
		e.deps.Escape(insight.ID),
		e.deps.Escape(insight.Type),
		e.deps.Escape(insight.Severity),
		e.deps.Escape(insight.Title),
		e.deps.Escape(insight.Description),
		e.deps.Escape(string(dataJSON)),
		e.deps.Escape(insight.CreatedAt),
	)
	cmd := exec.Command("sqlite3", e.dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		e.deps.LogWarn("insights: store failed", "error", err, "output", string(out))
	}
}

// GetInsights returns recent insights.
func (e *Engine) GetInsights(limit int, includeAcknowledged bool) ([]LifeInsight, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	ackFilter := "AND acknowledged = 0"
	if includeAcknowledged {
		ackFilter = ""
	}

	sql := fmt.Sprintf(
		`SELECT id, type, severity, title, description, data, acknowledged, created_at
		 FROM life_insights
		 WHERE 1=1 %s
		 ORDER BY created_at DESC LIMIT %d`,
		ackFilter, limit)

	rows, err := e.deps.Query(e.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("get insights: %w", err)
	}

	insights := make([]LifeInsight, 0, len(rows))
	for _, row := range rows {
		insight := InsightFromRow(row)
		insights = append(insights, insight)
	}
	return insights, nil
}

// AcknowledgeInsight marks an insight as acknowledged.
func (e *Engine) AcknowledgeInsight(id string) error {
	if id == "" {
		return fmt.Errorf("insight ID is required")
	}

	sql := fmt.Sprintf(
		`UPDATE life_insights SET acknowledged = 1 WHERE id = '%s'`,
		e.deps.Escape(id))
	cmd := exec.Command("sqlite3", e.dbPath, sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("acknowledge insight: %w: %s", err, string(out))
	}
	return nil
}

// SpendingForecast projects month-end spending based on current daily rate.
func (e *Engine) SpendingForecast(month string) (map[string]any, error) {
	if e.deps.FinanceDBPath == "" {
		return nil, fmt.Errorf("finance service not available")
	}

	dbPath := e.deps.FinanceDBPath

	if month == "" {
		month = time.Now().UTC().Format("2006-01")
	}
	monthStart := month + "-01"
	t, err := time.Parse("2006-01-02", monthStart)
	if err != nil {
		return nil, fmt.Errorf("invalid month format (expected YYYY-MM): %w", err)
	}
	monthEnd := t.AddDate(0, 1, -1)
	today := time.Now().UTC()
	todayStr := today.Format("2006-01-02")

	sql := fmt.Sprintf(
		`SELECT SUM(amount) as total, COUNT(DISTINCT date) as days_with_spending
		 FROM expenses WHERE date >= '%s' AND date <= '%s'`,
		e.deps.Escape(monthStart), e.deps.Escape(todayStr))
	rows, err := e.deps.Query(dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("forecast query: %w", err)
	}

	currentTotal := 0.0
	daysWithSpending := 0
	if len(rows) > 0 {
		currentTotal = jsonFloat(rows[0]["total"])
		daysWithSpending = jsonInt(rows[0]["days_with_spending"])
	}

	daysElapsed := int(today.Sub(t).Hours()/24) + 1
	if daysElapsed < 1 {
		daysElapsed = 1
	}
	totalDays := int(monthEnd.Sub(t).Hours()/24) + 1
	daysRemaining := totalDays - daysElapsed
	if daysRemaining < 0 {
		daysRemaining = 0
	}

	dailyRate := currentTotal / float64(daysElapsed)
	projectedTotal := math.Round((currentTotal+dailyRate*float64(daysRemaining))*100) / 100

	result := map[string]any{
		"month":           month,
		"current_total":   math.Round(currentTotal*100) / 100,
		"daily_rate":      math.Round(dailyRate*100) / 100,
		"days_elapsed":    daysElapsed,
		"days_remaining":  daysRemaining,
		"projected_total": projectedTotal,
	}

	_ = daysWithSpending
	budgetSQL := `SELECT SUM(monthly_limit) as total_budget FROM expense_budgets`
	budgetRows, err := e.deps.Query(dbPath, budgetSQL)
	if err == nil && len(budgetRows) > 0 {
		budget := jsonFloat(budgetRows[0]["total_budget"])
		if budget > 0 {
			result["budget"] = budget
			result["on_track"] = projectedTotal <= budget
		}
	}

	return result, nil
}

// PeriodDateRange returns the start and end dates for a report period.
func PeriodDateRange(period string, anchor time.Time) (time.Time, time.Time) {
	anchor = time.Date(anchor.Year(), anchor.Month(), anchor.Day(), 0, 0, 0, 0, time.UTC)

	switch period {
	case "daily":
		return anchor, anchor.Add(23*time.Hour + 59*time.Minute + 59*time.Second)
	case "weekly":
		weekday := int(anchor.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		start := anchor.AddDate(0, 0, -(weekday - 1))
		end := start.AddDate(0, 0, 6).Add(23*time.Hour + 59*time.Minute + 59*time.Second)
		return start, end
	case "monthly":
		start := time.Date(anchor.Year(), anchor.Month(), 1, 0, 0, 0, 0, time.UTC)
		end := start.AddDate(0, 1, -1).Add(23*time.Hour + 59*time.Minute + 59*time.Second)
		return start, end
	default:
		return anchor.AddDate(0, 0, -7), anchor
	}
}

// PrevPeriodRange returns the start and end of the previous period relative to the current start.
func PrevPeriodRange(period string, currentStart time.Time) (time.Time, time.Time) {
	switch period {
	case "daily":
		prev := currentStart.AddDate(0, 0, -1)
		return prev, prev.Add(23*time.Hour + 59*time.Minute + 59*time.Second)
	case "weekly":
		prevStart := currentStart.AddDate(0, 0, -7)
		prevEnd := prevStart.AddDate(0, 0, 6).Add(23*time.Hour + 59*time.Minute + 59*time.Second)
		return prevStart, prevEnd
	case "monthly":
		prevStart := currentStart.AddDate(0, -1, 0)
		prevEnd := prevStart.AddDate(0, 1, -1).Add(23*time.Hour + 59*time.Minute + 59*time.Second)
		return prevStart, prevEnd
	default:
		return currentStart.AddDate(0, 0, -7), currentStart
	}
}

// InsightFromRow converts a DB row to a LifeInsight.
func InsightFromRow(row map[string]any) LifeInsight {
	insight := LifeInsight{
		ID:           jsonStr(row["id"]),
		Type:         jsonStr(row["type"]),
		Severity:     jsonStr(row["severity"]),
		Title:        jsonStr(row["title"]),
		Description:  jsonStr(row["description"]),
		Acknowledged: jsonInt(row["acknowledged"]) != 0,
		CreatedAt:    jsonStr(row["created_at"]),
	}

	dataStr := jsonStr(row["data"])
	if dataStr != "" && dataStr != "{}" {
		var data map[string]any
		if json.Unmarshal([]byte(dataStr), &data) == nil {
			insight.Data = data
		}
	}

	return insight
}

func jsonStr(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", val)
	}
}

func jsonInt(v any) int {
	switch val := v.(type) {
	case float64:
		return int(val)
	case int:
		return val
	case string:
		var n int
		fmt.Sscanf(val, "%d", &n)
		return n
	default:
		return 0
	}
}

func jsonFloat(v any) float64 {
	switch val := v.(type) {
	case float64:
		return val
	case string:
		var f float64
		fmt.Sscanf(val, "%f", &f)
		return f
	default:
		return 0
	}
}
