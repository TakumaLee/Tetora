package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"path/filepath"
	"time"
)

// --- P24.3: Life Insights Engine ---

// --- Types ---

// InsightsEngine provides cross-data-source behavior analysis and proactive insights.
type InsightsEngine struct {
	dbPath string
	cfg    *Config
}

var globalInsightsEngine *InsightsEngine

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

// --- DB Init ---

// initInsightsDB creates the life_insights table.
func initInsightsDB(dbPath string) error {
	ddl := `
CREATE TABLE IF NOT EXISTS life_insights (
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
CREATE INDEX IF NOT EXISTS idx_insights_created ON life_insights(created_at);
`
	cmd := exec.Command("sqlite3", dbPath, ddl)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("init insights tables: %w: %s", err, string(out))
	}
	return nil
}

// --- Constructor ---

func newInsightsEngine(cfg *Config) *InsightsEngine {
	return &InsightsEngine{
		dbPath: cfg.HistoryDB,
		cfg:    cfg,
	}
}

// --- Report Generation ---

// GenerateReport produces a comprehensive cross-domain life report.
// period: "daily", "weekly", or "monthly". targetDate anchors the period.
func (e *InsightsEngine) GenerateReport(period string, targetDate time.Time) (*LifeReport, error) {
	start, end := periodDateRange(period, targetDate)
	prevStart, prevEnd := prevPeriodRange(period, start)

	report := &LifeReport{
		Period:      period,
		StartDate:   start.Format("2006-01-02"),
		EndDate:     end.Format("2006-01-02"),
		Insights:    []LifeInsight{},
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}

	// Spending section.
	if globalFinanceService != nil {
		spending := e.buildSpendingReport(start, end, prevStart, prevEnd)
		if spending != nil {
			report.Spending = spending
		}
	}

	// Tasks section.
	if globalTaskManager != nil {
		tasks := e.buildTasksReport(start, end)
		if tasks != nil {
			report.Tasks = tasks
		}
	}

	// Mood section.
	if globalUserProfileService != nil {
		mood := e.buildMoodReport(start, end)
		if mood != nil {
			report.Mood = mood
		}
	}

	// Social section.
	if globalContactsService != nil {
		social := e.buildSocialReport(start, end)
		if social != nil {
			report.Social = social
		}
	}

	// Habits section.
	if globalHabitsService != nil {
		habits := e.buildHabitsReport(start, end)
		if habits != nil {
			report.Habits = habits
		}
	}

	// Detect anomalies and attach relevant ones.
	anomalies, err := e.DetectAnomalies(7)
	if err == nil && len(anomalies) > 0 {
		report.Insights = anomalies
	}

	return report, nil
}

// --- Report Section Builders ---

func (e *InsightsEngine) buildSpendingReport(start, end, prevStart, prevEnd time.Time) *SpendingReport {
	dbPath := globalFinanceService.dbPath
	startStr := start.Format("2006-01-02")
	endStr := end.Format("2006-01-02")

	// Current period spending by category.
	sql := fmt.Sprintf(
		`SELECT category, SUM(amount) as total, description
		 FROM expenses
		 WHERE date >= '%s' AND date <= '%s'
		 GROUP BY category`,
		escapeSQLite(startStr), escapeSQLite(endStr))

	rows, err := queryDB(dbPath, sql)
	if err != nil {
		logWarn("insights: spending query failed", "error", err)
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

	// Find top expense.
	topExpense := ""
	topSQL := fmt.Sprintf(
		`SELECT description, amount FROM expenses
		 WHERE date >= '%s' AND date <= '%s'
		 ORDER BY amount DESC LIMIT 1`,
		escapeSQLite(startStr), escapeSQLite(endStr))
	topRows, err := queryDB(dbPath, topSQL)
	if err == nil && len(topRows) > 0 {
		desc := jsonStr(topRows[0]["description"])
		amt := jsonFloat(topRows[0]["amount"])
		if desc != "" {
			topExpense = fmt.Sprintf("%s (%.0f)", desc, amt)
		}
	}

	// Days in period.
	days := end.Sub(start).Hours()/24 + 1
	if days < 1 {
		days = 1
	}
	dailyAvg := math.Round(total/days*100) / 100

	// Previous period comparison.
	prevStartStr := prevStart.Format("2006-01-02")
	prevEndStr := prevEnd.Format("2006-01-02")
	prevSQL := fmt.Sprintf(
		`SELECT SUM(amount) as total FROM expenses
		 WHERE date >= '%s' AND date <= '%s'`,
		escapeSQLite(prevStartStr), escapeSQLite(prevEndStr))
	prevRows, err := queryDB(dbPath, prevSQL)
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

func (e *InsightsEngine) buildTasksReport(start, end time.Time) *TasksReport {
	dbPath := globalTaskManager.dbPath
	startStr := start.Format(time.RFC3339)
	endStr := end.Format(time.RFC3339)
	now := time.Now().UTC().Format(time.RFC3339)

	report := &TasksReport{}

	// Completed in period.
	sql := fmt.Sprintf(
		`SELECT COUNT(*) as cnt FROM user_tasks
		 WHERE status = 'done' AND completed_at >= '%s' AND completed_at <= '%s'`,
		escapeSQLite(startStr), escapeSQLite(endStr))
	rows, err := queryDB(dbPath, sql)
	if err == nil && len(rows) > 0 {
		report.Completed = jsonInt(rows[0]["cnt"])
	}

	// Created in period.
	sql = fmt.Sprintf(
		`SELECT COUNT(*) as cnt FROM user_tasks
		 WHERE created_at >= '%s' AND created_at <= '%s'`,
		escapeSQLite(startStr), escapeSQLite(endStr))
	rows, err = queryDB(dbPath, sql)
	if err == nil && len(rows) > 0 {
		report.Created = jsonInt(rows[0]["cnt"])
	}

	// Overdue (due before now, not done/cancelled).
	sql = fmt.Sprintf(
		`SELECT COUNT(*) as cnt FROM user_tasks
		 WHERE due_at != '' AND due_at < '%s' AND status NOT IN ('done','cancelled')`,
		escapeSQLite(now))
	rows, err = queryDB(dbPath, sql)
	if err == nil && len(rows) > 0 {
		report.Overdue = jsonInt(rows[0]["cnt"])
	}

	// Completion rate: completed / (completed + still pending from period).
	if report.Created > 0 {
		report.CompletionRate = math.Round(float64(report.Completed)/float64(report.Created)*10000) / 100
	}

	return report
}

func (e *InsightsEngine) buildMoodReport(start, end time.Time) *MoodReport {
	dbPath := globalUserProfileService.dbPath
	startStr := start.Format(time.RFC3339)
	endStr := end.Format(time.RFC3339)

	// Get mood entries in the period.
	sql := fmt.Sprintf(
		`SELECT date(created_at) as day, AVG(sentiment_score) as avg_score
		 FROM user_mood_log
		 WHERE created_at >= '%s' AND created_at <= '%s'
		 GROUP BY day ORDER BY day`,
		escapeSQLite(startStr), escapeSQLite(endStr))
	rows, err := queryDB(dbPath, sql)
	if err != nil {
		logWarn("insights: mood query failed", "error", err)
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

	// Trend: compare first half vs second half.
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

func (e *InsightsEngine) buildSocialReport(start, end time.Time) *SocialReport {
	dbPath := globalContactsService.dbPath
	startStr := start.Format(time.RFC3339)
	endStr := end.Format(time.RFC3339)

	// Total interactions in period.
	sql := fmt.Sprintf(
		`SELECT COUNT(*) as cnt, COUNT(DISTINCT contact_id) as unique_contacts
		 FROM contact_interactions
		 WHERE created_at >= '%s' AND created_at <= '%s'`,
		escapeSQLite(startStr), escapeSQLite(endStr))
	rows, err := queryDB(dbPath, sql)
	if err != nil {
		logWarn("insights: social query failed", "error", err)
		return nil
	}

	report := &SocialReport{}
	if len(rows) > 0 {
		report.InteractionCount = jsonInt(rows[0]["cnt"])
		report.UniqueContacts = jsonInt(rows[0]["unique_contacts"])
	}

	// Most contacted person.
	topSQL := fmt.Sprintf(
		`SELECT ci.contact_id, c.name, COUNT(*) as cnt
		 FROM contact_interactions ci
		 LEFT JOIN contacts c ON ci.contact_id = c.id
		 WHERE ci.created_at >= '%s' AND ci.created_at <= '%s'
		 GROUP BY ci.contact_id
		 ORDER BY cnt DESC LIMIT 1`,
		escapeSQLite(startStr), escapeSQLite(endStr))
	topRows, err := queryDB(dbPath, topSQL)
	if err == nil && len(topRows) > 0 {
		name := jsonStr(topRows[0]["name"])
		if name != "" {
			report.MostContacted = name
		}
	}

	return report
}

func (e *InsightsEngine) buildHabitsReport(start, end time.Time) *HabitsReport {
	dbPath := globalHabitsService.dbPath
	startStr := start.Format(time.RFC3339)
	endStr := end.Format(time.RFC3339)

	// Count active (non-archived) habits.
	rows, err := queryDB(dbPath,
		`SELECT COUNT(*) as cnt FROM habits WHERE archived_at = ''`)
	if err != nil {
		logWarn("insights: habits query failed", "error", err)
		return nil
	}
	activeHabits := 0
	if len(rows) > 0 {
		activeHabits = jsonInt(rows[0]["cnt"])
	}
	if activeHabits == 0 {
		return nil
	}

	// Count habit logs in period.
	logSQL := fmt.Sprintf(
		`SELECT COUNT(DISTINCT habit_id) as logged_habits, COUNT(*) as total_logs
		 FROM habit_logs
		 WHERE logged_at >= '%s' AND logged_at <= '%s'`,
		escapeSQLite(startStr), escapeSQLite(endStr))
	logRows, err := queryDB(dbPath, logSQL)
	if err != nil {
		logWarn("insights: habit logs query failed", "error", err)
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

	// Find best habit (most logs in period).
	bestHabit := ""
	longestStreak := 0
	bestSQL := fmt.Sprintf(
		`SELECT hl.habit_id, h.name, COUNT(*) as cnt
		 FROM habit_logs hl
		 JOIN habits h ON hl.habit_id = h.id
		 WHERE hl.logged_at >= '%s' AND hl.logged_at <= '%s'
		 GROUP BY hl.habit_id
		 ORDER BY cnt DESC LIMIT 1`,
		escapeSQLite(startStr), escapeSQLite(endStr))
	bestRows, err := queryDB(dbPath, bestSQL)
	if err == nil && len(bestRows) > 0 {
		bestHabit = jsonStr(bestRows[0]["name"])
		// Get streak for best habit.
		habitID := jsonStr(bestRows[0]["habit_id"])
		if habitID != "" {
			_, longest, err := globalHabitsService.GetStreak(habitID, "")
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

// --- Anomaly Detection ---

// DetectAnomalies scans recent data for behavioral anomalies.
// days: how far back to look for anomalies.
func (e *InsightsEngine) DetectAnomalies(days int) ([]LifeInsight, error) {
	if days <= 0 {
		days = 7
	}

	var insights []LifeInsight
	today := time.Now().UTC().Format("2006-01-02")

	// Spending anomaly: daily spend > 2x daily average of last 30 days.
	if globalFinanceService != nil {
		spendingInsights := e.detectSpendingAnomalies(days, today)
		insights = append(insights, spendingInsights...)
	}

	// Mood shift: 3+ days of declining sentiment or average below 0.3.
	if globalUserProfileService != nil {
		moodInsights := e.detectMoodAnomalies(days, today)
		insights = append(insights, moodInsights...)
	}

	// Task overload: more than 10 overdue tasks.
	if globalTaskManager != nil {
		taskInsights := e.detectTaskAnomalies(today)
		insights = append(insights, taskInsights...)
	}

	// Social isolation: no contact interactions in 14+ days.
	if globalContactsService != nil {
		socialInsights := e.detectSocialAnomalies(today)
		insights = append(insights, socialInsights...)
	}

	// Habit warning: broken streak that had 7+ day streak.
	if globalHabitsService != nil {
		habitInsights := e.detectHabitAnomalies(today)
		insights = append(insights, habitInsights...)
	}

	// Store insights with dedup.
	for i := range insights {
		if insights[i].ID == "" {
			insights[i].ID = newUUID()
		}
		if insights[i].CreatedAt == "" {
			insights[i].CreatedAt = time.Now().UTC().Format(time.RFC3339)
		}
		e.storeInsightDedup(&insights[i])
	}

	return insights, nil
}

func (e *InsightsEngine) detectSpendingAnomalies(days int, today string) []LifeInsight {
	dbPath := globalFinanceService.dbPath

	// Get 30-day daily average.
	thirtyDaysAgo := time.Now().UTC().AddDate(0, 0, -30).Format("2006-01-02")
	avgSQL := fmt.Sprintf(
		`SELECT AVG(daily_total) as avg_daily FROM (
			SELECT date, SUM(amount) as daily_total FROM expenses
			WHERE date >= '%s' GROUP BY date
		)`, escapeSQLite(thirtyDaysAgo))
	avgRows, err := queryDB(dbPath, avgSQL)
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

	// Check recent days for spikes.
	recentStart := time.Now().UTC().AddDate(0, 0, -days).Format("2006-01-02")
	spikeSQL := fmt.Sprintf(
		`SELECT date, SUM(amount) as daily_total FROM expenses
		 WHERE date >= '%s'
		 GROUP BY date
		 HAVING daily_total > %f`,
		escapeSQLite(recentStart), dailyAvg*2)
	spikeRows, err := queryDB(dbPath, spikeSQL)
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

func (e *InsightsEngine) detectMoodAnomalies(days int, today string) []LifeInsight {
	dbPath := globalUserProfileService.dbPath

	recentStart := time.Now().UTC().AddDate(0, 0, -days).Format(time.RFC3339)

	// Get daily mood scores.
	sql := fmt.Sprintf(
		`SELECT date(created_at) as day, AVG(sentiment_score) as avg_score
		 FROM user_mood_log
		 WHERE created_at >= '%s'
		 GROUP BY day ORDER BY day`,
		escapeSQLite(recentStart))
	rows, err := queryDB(dbPath, sql)
	if err != nil || len(rows) == 0 {
		return nil
	}

	var insights []LifeInsight

	// Check for 3+ days of declining scores.
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

	// Check for overall low mood (average below 0.3 means net negative — scale is -1 to 1).
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

func (e *InsightsEngine) detectTaskAnomalies(today string) []LifeInsight {
	dbPath := globalTaskManager.dbPath

	// Count overdue tasks.
	sql := fmt.Sprintf(
		`SELECT COUNT(*) as cnt FROM user_tasks
		 WHERE due_at != '' AND due_at < '%s' AND status NOT IN ('done','cancelled')`,
		escapeSQLite(today+"T23:59:59Z"))
	rows, err := queryDB(dbPath, sql)
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

func (e *InsightsEngine) detectSocialAnomalies(today string) []LifeInsight {
	dbPath := globalContactsService.dbPath

	// Check for recent interactions.
	fourteenDaysAgo := time.Now().UTC().AddDate(0, 0, -14).Format(time.RFC3339)
	sql := fmt.Sprintf(
		`SELECT COUNT(*) as cnt FROM contact_interactions
		 WHERE created_at >= '%s'`,
		escapeSQLite(fourteenDaysAgo))
	rows, err := queryDB(dbPath, sql)
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

	// Verify that the user actually uses contacts (has any interactions at all).
	allSQL := `SELECT COUNT(*) as cnt FROM contact_interactions`
	allRows, err := queryDB(dbPath, allSQL)
	if err != nil {
		return nil
	}
	totalInteractions := 0
	if len(allRows) > 0 {
		totalInteractions = jsonInt(allRows[0]["cnt"])
	}
	if totalInteractions == 0 {
		// User never logged interactions, don't flag.
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

func (e *InsightsEngine) detectHabitAnomalies(today string) []LifeInsight {
	dbPath := globalHabitsService.dbPath

	// Get all active habits.
	rows, err := queryDB(dbPath,
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

		current, longest, err := globalHabitsService.GetStreak(habitID, "")
		if err != nil {
			continue
		}

		// Broken streak that was 7+ days.
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

// --- Insight Storage ---

// storeInsightDedup stores an insight, deduplicating by type+date.
func (e *InsightsEngine) storeInsightDedup(insight *LifeInsight) {
	today := time.Now().UTC().Format("2006-01-02")

	// Check for existing insight of same type today.
	checkSQL := fmt.Sprintf(
		`SELECT id FROM life_insights WHERE type = '%s' AND date(created_at) = '%s' LIMIT 1`,
		escapeSQLite(insight.Type), escapeSQLite(today))
	rows, err := queryDB(e.dbPath, checkSQL)
	if err != nil {
		logWarn("insights: dedup check failed", "error", err)
		return
	}
	if len(rows) > 0 {
		// Already have insight of this type today, skip.
		insight.ID = jsonStr(rows[0]["id"])
		return
	}

	dataJSON, _ := json.Marshal(insight.Data)

	sql := fmt.Sprintf(
		`INSERT INTO life_insights (id, type, severity, title, description, data, acknowledged, created_at)
		 VALUES ('%s','%s','%s','%s','%s','%s',0,'%s')`,
		escapeSQLite(insight.ID),
		escapeSQLite(insight.Type),
		escapeSQLite(insight.Severity),
		escapeSQLite(insight.Title),
		escapeSQLite(insight.Description),
		escapeSQLite(string(dataJSON)),
		escapeSQLite(insight.CreatedAt),
	)
	cmd := exec.Command("sqlite3", e.dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		logWarn("insights: store failed", "error", err, "output", string(out))
	}
}

// --- Insight Query ---

// GetInsights returns recent insights.
func (e *InsightsEngine) GetInsights(limit int, includeAcknowledged bool) ([]LifeInsight, error) {
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

	rows, err := queryDB(e.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("get insights: %w", err)
	}

	insights := make([]LifeInsight, 0, len(rows))
	for _, row := range rows {
		insight := insightFromRow(row)
		insights = append(insights, insight)
	}
	return insights, nil
}

// AcknowledgeInsight marks an insight as acknowledged.
func (e *InsightsEngine) AcknowledgeInsight(id string) error {
	if id == "" {
		return fmt.Errorf("insight ID is required")
	}

	sql := fmt.Sprintf(
		`UPDATE life_insights SET acknowledged = 1 WHERE id = '%s'`,
		escapeSQLite(id))
	cmd := exec.Command("sqlite3", e.dbPath, sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("acknowledge insight: %w: %s", err, string(out))
	}
	return nil
}

// --- Spending Forecast ---

// SpendingForecast projects month-end spending based on current daily rate.
func (e *InsightsEngine) SpendingForecast(month string) (map[string]any, error) {
	if globalFinanceService == nil {
		return nil, fmt.Errorf("finance service not available")
	}

	dbPath := globalFinanceService.dbPath

	// Parse month (YYYY-MM format).
	if month == "" {
		month = time.Now().UTC().Format("2006-01")
	}
	monthStart := month + "-01"
	t, err := time.Parse("2006-01-02", monthStart)
	if err != nil {
		return nil, fmt.Errorf("invalid month format (expected YYYY-MM): %w", err)
	}
	monthEnd := t.AddDate(0, 1, -1) // Last day of month.
	today := time.Now().UTC()
	todayStr := today.Format("2006-01-02")

	// Get current month spending.
	sql := fmt.Sprintf(
		`SELECT SUM(amount) as total, COUNT(DISTINCT date) as days_with_spending
		 FROM expenses WHERE date >= '%s' AND date <= '%s'`,
		escapeSQLite(monthStart), escapeSQLite(todayStr))
	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("forecast query: %w", err)
	}

	currentTotal := 0.0
	daysWithSpending := 0
	if len(rows) > 0 {
		currentTotal = jsonFloat(rows[0]["total"])
		daysWithSpending = jsonInt(rows[0]["days_with_spending"])
	}

	// Calculate days elapsed and remaining.
	daysElapsed := int(today.Sub(t).Hours()/24) + 1
	if daysElapsed < 1 {
		daysElapsed = 1
	}
	totalDays := int(monthEnd.Sub(t).Hours()/24) + 1
	daysRemaining := totalDays - daysElapsed
	if daysRemaining < 0 {
		daysRemaining = 0
	}

	// Daily rate based on days elapsed (not just days with spending).
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

	// Check budget if available.
	_ = daysWithSpending // used for calculation context
	budgetSQL := fmt.Sprintf(
		`SELECT SUM(monthly_limit) as total_budget FROM expense_budgets`)
	budgetRows, err := queryDB(dbPath, budgetSQL)
	if err == nil && len(budgetRows) > 0 {
		budget := jsonFloat(budgetRows[0]["total_budget"])
		if budget > 0 {
			result["budget"] = budget
			result["on_track"] = projectedTotal <= budget
		}
	}

	return result, nil
}

// --- Tool Handlers ---

// toolLifeReport handles the life_report tool.
func toolLifeReport(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalInsightsEngine == nil {
		return "", fmt.Errorf("insights engine not initialized")
	}

	var args struct {
		Period string `json:"period"`
		Date   string `json:"date"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	period := args.Period
	if period == "" {
		period = "weekly"
	}
	if period != "daily" && period != "weekly" && period != "monthly" {
		return "", fmt.Errorf("invalid period %q (use: daily, weekly, monthly)", period)
	}

	targetDate := time.Now().UTC()
	if args.Date != "" {
		parsed, err := time.Parse("2006-01-02", args.Date)
		if err != nil {
			return "", fmt.Errorf("invalid date format (expected YYYY-MM-DD): %w", err)
		}
		targetDate = parsed
	}

	report, err := globalInsightsEngine.GenerateReport(period, targetDate)
	if err != nil {
		return "", err
	}

	out, _ := json.MarshalIndent(report, "", "  ")
	return string(out), nil
}

// toolLifeInsights handles the life_insights tool.
func toolLifeInsights(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalInsightsEngine == nil {
		return "", fmt.Errorf("insights engine not initialized")
	}

	var args struct {
		Action    string `json:"action"`
		Days      int    `json:"days"`
		InsightID string `json:"insight_id"`
		Month     string `json:"month"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	switch args.Action {
	case "detect":
		days := args.Days
		if days <= 0 {
			days = 7
		}
		insights, err := globalInsightsEngine.DetectAnomalies(days)
		if err != nil {
			return "", err
		}
		if len(insights) == 0 {
			return `{"message":"No anomalies detected","insights":[]}`, nil
		}
		out, _ := json.MarshalIndent(map[string]any{
			"insights": insights,
			"count":    len(insights),
		}, "", "  ")
		return string(out), nil

	case "list":
		insights, err := globalInsightsEngine.GetInsights(20, false)
		if err != nil {
			return "", err
		}
		out, _ := json.MarshalIndent(insights, "", "  ")
		return string(out), nil

	case "acknowledge":
		if args.InsightID == "" {
			return "", fmt.Errorf("insight_id is required for acknowledge action")
		}
		if err := globalInsightsEngine.AcknowledgeInsight(args.InsightID); err != nil {
			return "", err
		}
		return fmt.Sprintf("Insight %s acknowledged.", args.InsightID), nil

	case "forecast":
		result, err := globalInsightsEngine.SpendingForecast(args.Month)
		if err != nil {
			return "", err
		}
		out, _ := json.MarshalIndent(result, "", "  ")
		return string(out), nil

	default:
		return "", fmt.Errorf("unknown action %q (use: detect, list, acknowledge, forecast)", args.Action)
	}
}

// --- Helpers ---

// periodDateRange returns the start and end dates for a report period.
func periodDateRange(period string, anchor time.Time) (time.Time, time.Time) {
	anchor = time.Date(anchor.Year(), anchor.Month(), anchor.Day(), 0, 0, 0, 0, time.UTC)

	switch period {
	case "daily":
		return anchor, anchor.Add(23*time.Hour + 59*time.Minute + 59*time.Second)
	case "weekly":
		// Go back to Monday.
		weekday := int(anchor.Weekday())
		if weekday == 0 {
			weekday = 7 // Sunday
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

// prevPeriodRange returns the start and end of the previous period relative to the current start.
func prevPeriodRange(period string, currentStart time.Time) (time.Time, time.Time) {
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

// insightFromRow converts a DB row to a LifeInsight.
func insightFromRow(row map[string]any) LifeInsight {
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

// insightsDBPath returns the database path, using HistoryDB for standalone or the engine's own path.
func insightsDBPath(cfg *Config) string {
	if cfg.HistoryDB != "" {
		return cfg.HistoryDB
	}
	return filepath.Join(cfg.baseDir, "history.db")
}
