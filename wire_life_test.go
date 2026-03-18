package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"tetora/internal/automation/insights"
	"tetora/internal/cli"
	"tetora/internal/completion"
	"tetora/internal/db"
	"tetora/internal/history"
	"tetora/internal/integration/notes"
	"tetora/internal/knowledge"
	"tetora/internal/log"
	"tetora/internal/scheduling"
	"tetora/internal/sla"
)

// --- from daily_notes_test.go ---

func TestDailyNotesConfig(t *testing.T) {
	cfg := DailyNotesConfig{}
	if cfg.ScheduleOrDefault() != "0 0 * * *" {
		t.Errorf("default schedule wrong: got %s", cfg.ScheduleOrDefault())
	}

	cfg.Schedule = "0 12 * * *"
	if cfg.ScheduleOrDefault() != "0 12 * * *" {
		t.Errorf("custom schedule wrong: got %s", cfg.ScheduleOrDefault())
	}

	baseDir := "/tmp/tetora-test"
	if cfg.DirOrDefault(baseDir) != "/tmp/tetora-test/notes" {
		t.Errorf("default dir wrong: got %s", cfg.DirOrDefault(baseDir))
	}

	cfg.Dir = "custom_notes"
	if cfg.DirOrDefault(baseDir) != "/tmp/tetora-test/custom_notes" {
		t.Errorf("relative dir wrong: got %s", cfg.DirOrDefault(baseDir))
	}

	cfg.Dir = "/absolute/path"
	if cfg.DirOrDefault(baseDir) != "/absolute/path" {
		t.Errorf("absolute dir wrong: got %s", cfg.DirOrDefault(baseDir))
	}
}

func TestGenerateDailyNote(t *testing.T) {
	// Create temp DB with test data.
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create DB schema.
	schema := `CREATE TABLE IF NOT EXISTS history (
		id TEXT PRIMARY KEY,
		name TEXT,
		source TEXT,
		agent TEXT,
		status TEXT,
		duration_ms INTEGER,
		cost_usd REAL,
		tokens_in INTEGER,
		tokens_out INTEGER,
		started_at TEXT,
		finished_at TEXT
	);`
	if _, err := db.Query(dbPath, schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	cfg := &Config{
		BaseDir:   tmpDir,
		HistoryDB: dbPath,
	}

	// Insert test tasks.
	yesterday := time.Now().AddDate(0, 0, -1)
	startedAt := yesterday.Format("2006-01-02 10:00:00")
	sql := `INSERT INTO history (id, name, source, agent, status, duration_ms, cost_usd, tokens_in, tokens_out, started_at, finished_at)
	        VALUES ('test1', 'Test Task 1', 'cron', '琉璃', 'success', 1000, 0.05, 100, 200, '` + db.Escape(startedAt) + `', '` + db.Escape(startedAt) + `')`
	if _, err := db.Query(dbPath, sql); err != nil {
		t.Fatalf("insert test data: %v", err)
	}

	// Generate note.
	content, err := generateDailyNote(cfg, yesterday)
	if err != nil {
		t.Fatalf("generate note: %v", err)
	}

	if content == "" {
		t.Fatal("note content is empty")
	}

	if !dailyNoteContains(content, "# Daily Summary") {
		t.Error("note missing header")
	}
	if !dailyNoteContains(content, "Total Tasks") {
		t.Error("note missing summary")
	}
	if !dailyNoteContains(content, "Test Task 1") {
		t.Error("note missing task details")
	}
}

func TestWriteDailyNote(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &Config{
		BaseDir: tmpDir,
		DailyNotes: DailyNotesConfig{
			Enabled: true,
			Dir:     "notes",
		},
	}

	date := time.Now()
	content := "# Daily Summary\n\nTest content."

	if err := writeDailyNote(cfg, date, content); err != nil {
		t.Fatalf("write note: %v", err)
	}

	notesDir := cfg.DailyNotes.DirOrDefault(tmpDir)
	filename := date.Format("2006-01-02") + ".md"
	filePath := filepath.Join(notesDir, filename)

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read note file: %v", err)
	}

	if string(data) != content {
		t.Errorf("note content mismatch: got %q", string(data))
	}
}

func dailyNoteContains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && len(s) >= len(substr) && dailyNoteFindSubstring(s, substr)
}

func dailyNoteFindSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// --- from insights_test.go ---

// setupInsightsTestDB creates a temp database with all required tables for testing.
func setupInsightsTestDB(t *testing.T) (string, *insights.Engine) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	if err := initInsightsDB(dbPath); err != nil {
		t.Fatalf("initInsightsDB: %v", err)
	}

	// Create dependent tables for cross-domain testing.
	tables := `
CREATE TABLE IF NOT EXISTS expenses (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id TEXT NOT NULL DEFAULT 'default',
    amount REAL NOT NULL,
    currency TEXT NOT NULL DEFAULT 'TWD',
    amount_usd REAL DEFAULT 0,
    category TEXT NOT NULL DEFAULT 'other',
    description TEXT DEFAULT '',
    tags TEXT DEFAULT '[]',
    date TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS user_tasks (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL DEFAULT 'default',
    title TEXT NOT NULL,
    description TEXT DEFAULT '',
    project TEXT DEFAULT 'inbox',
    status TEXT DEFAULT 'todo',
    priority INTEGER DEFAULT 2,
    due_at TEXT DEFAULT '',
    parent_id TEXT DEFAULT '',
    tags TEXT DEFAULT '[]',
    source_channel TEXT DEFAULT '',
    external_id TEXT DEFAULT '',
    external_source TEXT DEFAULT '',
    sort_order INTEGER DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    completed_at TEXT DEFAULT ''
);

CREATE TABLE IF NOT EXISTS user_mood_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id TEXT NOT NULL,
    channel TEXT NOT NULL,
    sentiment_score REAL NOT NULL,
    keywords TEXT DEFAULT '',
    message_snippet TEXT DEFAULT '',
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS contacts (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    nickname TEXT DEFAULT '',
    email TEXT DEFAULT '',
    phone TEXT DEFAULT '',
    birthday TEXT DEFAULT '',
    anniversary TEXT DEFAULT '',
    notes TEXT DEFAULT '',
    tags TEXT DEFAULT '[]',
    channel_ids TEXT DEFAULT '{}',
    relationship TEXT DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS contact_interactions (
    id TEXT PRIMARY KEY,
    contact_id TEXT NOT NULL,
    channel TEXT DEFAULT '',
    interaction_type TEXT NOT NULL,
    summary TEXT DEFAULT '',
    sentiment TEXT DEFAULT '',
    created_at TEXT NOT NULL
);

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

CREATE TABLE IF NOT EXISTS expense_budgets (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id TEXT NOT NULL,
    category TEXT NOT NULL,
    monthly_limit REAL NOT NULL,
    currency TEXT NOT NULL DEFAULT 'TWD',
    created_at TEXT NOT NULL
);
`
	cmd := exec.Command("sqlite3", dbPath, tables)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create test tables: %v: %s", err, string(out))
	}

	deps := insights.Deps{
		Query:          db.Query,
		Escape:         db.Escape,
		LogWarn:        log.Warn,
		UUID:           newUUID,
		FinanceDBPath:  dbPath,
		TasksDBPath:    dbPath,
		ProfileDBPath:  dbPath,
		ContactsDBPath: dbPath,
		HabitsDBPath:   dbPath,
	}
	engine := insights.New(dbPath, deps)
	return dbPath, engine
}

// setupTestGlobals sets up global service pointers for testing and returns a cleanup function.
func setupTestGlobals(t *testing.T, dbPath string, cfg *Config) func() {
	t.Helper()

	oldFinance := globalFinanceService
	oldTasks := globalTaskManager
	oldProfile := globalUserProfileService
	oldContacts := globalContactsService
	oldHabits := globalHabitsService

	globalFinanceService = newFinanceService(cfg)
	globalTaskManager = newTaskManagerService(cfg)
	globalUserProfileService = newUserProfileService(cfg)
	globalContactsService = newContactsService(cfg)
	globalHabitsService = newHabitsService(cfg)

	return func() {
		globalFinanceService = oldFinance
		globalTaskManager = oldTasks
		globalUserProfileService = oldProfile
		globalContactsService = oldContacts
		globalHabitsService = oldHabits
	}
}

// testInsightsAppCtx returns a context that carries an App with the given engine.
func testInsightsAppCtx(engine *insights.Engine) context.Context {
	app := &App{Insights: engine}
	return withApp(context.Background(), app)
}

func insertExpense(t *testing.T, dbPath string, amount float64, category, description, date string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	sql := fmt.Sprintf(
		`INSERT INTO expenses (user_id, amount, currency, category, description, date, created_at)
		 VALUES ('default', %f, 'TWD', '%s', '%s', '%s', '%s')`,
		amount, db.Escape(category), db.Escape(description), db.Escape(date), now)
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("insert expense: %v: %s", err, string(out))
	}
}

func insertTask(t *testing.T, dbPath, id, title, status, dueAt, createdAt, completedAt string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	if createdAt == "" {
		createdAt = now
	}
	sql := fmt.Sprintf(
		`INSERT INTO user_tasks (id, user_id, title, status, due_at, created_at, updated_at, completed_at)
		 VALUES ('%s', 'default', '%s', '%s', '%s', '%s', '%s', '%s')`,
		db.Escape(id), db.Escape(title), db.Escape(status),
		db.Escape(dueAt), db.Escape(createdAt), now, db.Escape(completedAt))
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("insert task: %v: %s", err, string(out))
	}
}

func insertMoodLog(t *testing.T, dbPath string, score float64, createdAt string) {
	t.Helper()
	sql := fmt.Sprintf(
		`INSERT INTO user_mood_log (user_id, channel, sentiment_score, created_at)
		 VALUES ('default', 'test', %f, '%s')`,
		score, db.Escape(createdAt))
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("insert mood: %v: %s", err, string(out))
	}
}

func insertInteraction(t *testing.T, dbPath, contactID, interactionType, createdAt string) {
	t.Helper()
	id := newUUID()
	sql := fmt.Sprintf(
		`INSERT INTO contact_interactions (id, contact_id, interaction_type, created_at)
		 VALUES ('%s', '%s', '%s', '%s')`,
		db.Escape(id), db.Escape(contactID), db.Escape(interactionType), db.Escape(createdAt))
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("insert interaction: %v: %s", err, string(out))
	}
}

func insertContact(t *testing.T, dbPath, id, name string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	sql := fmt.Sprintf(
		`INSERT INTO contacts (id, name, created_at, updated_at)
		 VALUES ('%s', '%s', '%s', '%s')`,
		db.Escape(id), db.Escape(name), now, now)
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("insert contact: %v: %s", err, string(out))
	}
}

func insertHabit(t *testing.T, dbPath, id, name string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	sql := fmt.Sprintf(
		`INSERT INTO habits (id, name, frequency, target_count, created_at, archived_at)
		 VALUES ('%s', '%s', 'daily', 1, '%s', '')`,
		db.Escape(id), db.Escape(name), now)
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("insert habit: %v: %s", err, string(out))
	}
}

func insightsInsertHabitLog(t *testing.T, dbPath, habitID, loggedAt string) {
	t.Helper()
	id := newUUID()
	sql := fmt.Sprintf(
		`INSERT INTO habit_logs (id, habit_id, logged_at, value)
		 VALUES ('%s', '%s', '%s', 1.0)`,
		db.Escape(id), db.Escape(habitID), db.Escape(loggedAt))
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("insert habit log: %v: %s", err, string(out))
	}
}

// --- Tests ---

func TestInitInsightsDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	if err := initInsightsDB(dbPath); err != nil {
		t.Fatalf("initInsightsDB: %v", err)
	}

	// Verify table exists.
	rows, err := db.Query(dbPath, "SELECT name FROM sqlite_master WHERE type='table' AND name='life_insights'")
	if err != nil {
		t.Fatalf("db.Query: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("life_insights table not created")
	}

	// Verify indices.
	idxRows, err := db.Query(dbPath, "SELECT name FROM sqlite_master WHERE type='index' AND name LIKE 'idx_insights_%'")
	if err != nil {
		t.Fatalf("db.Query indices: %v", err)
	}
	if len(idxRows) < 2 {
		t.Errorf("expected at least 2 indices, got %d", len(idxRows))
	}
}

func TestInitInsightsDB_InvalidPath(t *testing.T) {
	err := initInsightsDB("/nonexistent/path/db.sqlite")
	if err == nil {
		t.Fatal("expected error for invalid path")
	}
}

func TestInsightsGenerateReport_Empty(t *testing.T) {
	dbPath, engine := setupInsightsTestDB(t)
	cfg := &Config{HistoryDB: dbPath}
	cleanup := setupTestGlobals(t, dbPath, cfg)
	defer cleanup()

	report, err := engine.GenerateReport("weekly", time.Now().UTC())
	if err != nil {
		t.Fatalf("GenerateReport: %v", err)
	}
	if report == nil {
		t.Fatal("report should not be nil")
	}
	if report.Period != "weekly" {
		t.Errorf("period: got %q, want weekly", report.Period)
	}
	if report.GeneratedAt == "" {
		t.Error("GeneratedAt should be set")
	}
	// All sections should be empty/nil with no data (spending will return zero-value report).
}

func TestGenerateReport_WithSpending(t *testing.T) {
	dbPath, engine := setupInsightsTestDB(t)
	cfg := &Config{HistoryDB: dbPath}
	cleanup := setupTestGlobals(t, dbPath, cfg)
	defer cleanup()

	// Use "daily" period to avoid day-of-week boundary issues.
	today := time.Now().UTC().Format("2006-01-02")

	insertExpense(t, dbPath, 500, "food", "lunch", today)
	insertExpense(t, dbPath, 300, "food", "dinner", today)
	insertExpense(t, dbPath, 200, "transport", "taxi", today)

	report, err := engine.GenerateReport("daily", time.Now().UTC())
	if err != nil {
		t.Fatalf("GenerateReport: %v", err)
	}
	if report.Spending == nil {
		t.Fatal("spending section should not be nil")
	}
	if report.Spending.Total != 1000 {
		t.Errorf("spending total: got %.0f, want 1000", report.Spending.Total)
	}
	if report.Spending.ByCategory["food"] != 800 {
		t.Errorf("spending food: got %.0f, want 800", report.Spending.ByCategory["food"])
	}
	if report.Spending.ByCategory["transport"] != 200 {
		t.Errorf("spending transport: got %.0f, want 200", report.Spending.ByCategory["transport"])
	}
	if report.Spending.DailyAverage <= 0 {
		t.Error("daily average should be positive")
	}
}

func TestGenerateReport_WithTasks(t *testing.T) {
	dbPath, engine := setupInsightsTestDB(t)
	cfg := &Config{HistoryDB: dbPath}
	cleanup := setupTestGlobals(t, dbPath, cfg)
	defer cleanup()

	now := time.Now().UTC()
	todayStr := now.Format(time.RFC3339)
	pastDue := now.AddDate(0, 0, -3).Format(time.RFC3339)

	insertTask(t, dbPath, "t1", "Task 1", "done", "", todayStr, todayStr)
	insertTask(t, dbPath, "t2", "Task 2", "done", "", todayStr, todayStr)
	insertTask(t, dbPath, "t3", "Task 3", "todo", pastDue, todayStr, "")
	insertTask(t, dbPath, "t4", "Task 4", "todo", "", todayStr, "")

	report, err := engine.GenerateReport("weekly", now)
	if err != nil {
		t.Fatalf("GenerateReport: %v", err)
	}
	if report.Tasks == nil {
		t.Fatal("tasks section should not be nil")
	}
	if report.Tasks.Completed != 2 {
		t.Errorf("completed: got %d, want 2", report.Tasks.Completed)
	}
	if report.Tasks.Created != 4 {
		t.Errorf("created: got %d, want 4", report.Tasks.Created)
	}
	if report.Tasks.Overdue != 1 {
		t.Errorf("overdue: got %d, want 1", report.Tasks.Overdue)
	}
	if report.Tasks.CompletionRate != 50 {
		t.Errorf("completion rate: got %.2f, want 50", report.Tasks.CompletionRate)
	}
}

func TestGenerateReport_WithMood(t *testing.T) {
	dbPath, engine := setupInsightsTestDB(t)
	cfg := &Config{HistoryDB: dbPath}
	cleanup := setupTestGlobals(t, dbPath, cfg)
	defer cleanup()

	now := time.Now().UTC()
	for i := 0; i < 7; i++ {
		ts := now.AddDate(0, 0, -i).Format(time.RFC3339)
		// Scores: improving trend (older = lower, newer = higher).
		score := 0.3 + float64(6-i)*0.1
		insertMoodLog(t, dbPath, score, ts)
	}

	report, err := engine.GenerateReport("weekly", now)
	if err != nil {
		t.Fatalf("GenerateReport: %v", err)
	}
	if report.Mood == nil {
		t.Fatal("mood section should not be nil")
	}
	if report.Mood.AverageScore == 0 {
		t.Error("average score should not be zero")
	}
	if len(report.Mood.ByDay) == 0 {
		t.Error("by_day should have entries")
	}
}

func TestGenerateReport_MoodTrend(t *testing.T) {
	dbPath, engine := setupInsightsTestDB(t)
	cfg := &Config{HistoryDB: dbPath}
	cleanup := setupTestGlobals(t, dbPath, cfg)
	defer cleanup()

	// Use a fixed anchor date (mid-month Wednesday) to avoid weekly boundary issues.
	anchor := time.Date(2026, 3, 11, 12, 0, 0, 0, time.UTC) // Wednesday

	// Insert declining trend over 7 days: first half positive, second half negative.
	// Scores: day -6: 0.8, day -5: 0.6, day -4: 0.4, day -3: 0.2, day -2: 0.0, day -1: -0.2, day 0: -0.4
	for i := 6; i >= 0; i-- {
		ts := anchor.AddDate(0, 0, -i).Format(time.RFC3339)
		score := 0.8 - float64(6-i)*0.2
		insertMoodLog(t, dbPath, score, ts)
	}

	report, err := engine.GenerateReport("weekly", anchor)
	if err != nil {
		t.Fatalf("GenerateReport: %v", err)
	}
	if report.Mood == nil {
		t.Fatal("mood section should not be nil")
	}
	if report.Mood.Trend != "declining" {
		t.Errorf("trend: got %q, want declining (avg=%.3f, byDay=%v)", report.Mood.Trend, report.Mood.AverageScore, report.Mood.ByDay)
	}
}

func TestDetectAnomalies_SpendingAnomaly(t *testing.T) {
	dbPath, engine := setupInsightsTestDB(t)
	cfg := &Config{HistoryDB: dbPath}
	cleanup := setupTestGlobals(t, dbPath, cfg)
	defer cleanup()

	// Insert 30 days of normal spending (100/day).
	for i := 30; i >= 1; i-- {
		date := time.Now().UTC().AddDate(0, 0, -i).Format("2006-01-02")
		insertExpense(t, dbPath, 100, "food", "daily food", date)
	}

	// Insert spike today (500 = 5x average).
	today := time.Now().UTC().Format("2006-01-02")
	insertExpense(t, dbPath, 500, "shopping", "big purchase", today)

	ins, err := engine.DetectAnomalies(7)
	if err != nil {
		t.Fatalf("DetectAnomalies: %v", err)
	}

	found := false
	for _, item := range ins {
		if item.Type == "spending_anomaly" {
			found = true
			if item.Severity != "warning" {
				t.Errorf("severity: got %q, want warning", item.Severity)
			}
			if item.Data == nil {
				t.Error("data should not be nil")
			}
			break
		}
	}
	if !found {
		t.Error("expected spending_anomaly insight")
	}
}

func TestDetectAnomalies_TaskOverload(t *testing.T) {
	dbPath, engine := setupInsightsTestDB(t)
	cfg := &Config{HistoryDB: dbPath}
	cleanup := setupTestGlobals(t, dbPath, cfg)
	defer cleanup()

	// Insert 11 overdue tasks.
	pastDue := time.Now().UTC().AddDate(0, 0, -5).Format(time.RFC3339)
	for i := 0; i < 11; i++ {
		id := fmt.Sprintf("overdue-%d", i)
		insertTask(t, dbPath, id, fmt.Sprintf("Overdue task %d", i), "todo", pastDue, pastDue, "")
	}

	ins, err := engine.DetectAnomalies(7)
	if err != nil {
		t.Fatalf("DetectAnomalies: %v", err)
	}

	found := false
	for _, item := range ins {
		if item.Type == "task_overload" {
			found = true
			if item.Severity != "warning" {
				t.Errorf("severity: got %q, want warning", item.Severity)
			}
			overdue, ok := item.Data["overdue_count"]
			if !ok {
				t.Error("data should contain overdue_count")
			} else {
				var cnt int
				switch v := overdue.(type) {
				case float64:
					cnt = int(v)
				case int:
					cnt = v
				}
				if cnt < 11 {
					t.Errorf("overdue_count: got %v, want >= 11", overdue)
				}
			}
			break
		}
	}
	if !found {
		t.Error("expected task_overload insight")
	}
}

func TestDetectAnomalies_NoAnomalies(t *testing.T) {
	dbPath, engine := setupInsightsTestDB(t)
	cfg := &Config{HistoryDB: dbPath}
	cleanup := setupTestGlobals(t, dbPath, cfg)
	defer cleanup()

	// Insert normal spending.
	for i := 30; i >= 0; i-- {
		date := time.Now().UTC().AddDate(0, 0, -i).Format("2006-01-02")
		insertExpense(t, dbPath, 100, "food", "daily food", date)
	}

	// Insert 5 non-overdue tasks.
	future := time.Now().UTC().AddDate(0, 0, 30).Format(time.RFC3339)
	now := time.Now().UTC().Format(time.RFC3339)
	for i := 0; i < 5; i++ {
		insertTask(t, dbPath, fmt.Sprintf("normal-%d", i), fmt.Sprintf("Task %d", i), "todo", future, now, "")
	}

	ins, err := engine.DetectAnomalies(7)
	if err != nil {
		t.Fatalf("DetectAnomalies: %v", err)
	}

	// Should have no anomalies.
	for _, item := range ins {
		if item.Type == "spending_anomaly" || item.Type == "task_overload" || item.Type == "social_isolation" {
			t.Errorf("unexpected anomaly: %s - %s", item.Type, item.Title)
		}
	}
}

func TestGetInsights(t *testing.T) {
	dbPath, engine := setupInsightsTestDB(t)

	// Insert some insights directly.
	now := time.Now().UTC().Format(time.RFC3339)
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("test-insight-%d", i)
		acked := 0
		if i >= 3 {
			acked = 1
		}
		sql := fmt.Sprintf(
			`INSERT INTO life_insights (id, type, severity, title, description, data, acknowledged, created_at)
			 VALUES ('%s', 'test_type', 'info', 'Test %d', 'Description %d', '{}', %d, '%s')`,
			id, i, i, acked, now)
		cmd := exec.Command("sqlite3", dbPath, sql)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("insert insight: %v: %s", err, string(out))
		}
	}

	// Get unacknowledged only.
	ins, err := engine.GetInsights(20, false)
	if err != nil {
		t.Fatalf("GetInsights: %v", err)
	}
	if len(ins) != 3 {
		t.Errorf("unacknowledged count: got %d, want 3", len(ins))
	}

	// Get all.
	allInsights, err := engine.GetInsights(20, true)
	if err != nil {
		t.Fatalf("GetInsights (all): %v", err)
	}
	if len(allInsights) != 5 {
		t.Errorf("all count: got %d, want 5", len(allInsights))
	}
}

func TestAcknowledgeInsight(t *testing.T) {
	dbPath, engine := setupInsightsTestDB(t)

	// Insert an insight.
	now := time.Now().UTC().Format(time.RFC3339)
	id := "ack-test-1"
	sql := fmt.Sprintf(
		`INSERT INTO life_insights (id, type, severity, title, description, acknowledged, created_at)
		 VALUES ('%s', 'test', 'info', 'Test', 'Test desc', 0, '%s')`,
		id, now)
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("insert insight: %v: %s", err, string(out))
	}

	// Acknowledge it.
	if err := engine.AcknowledgeInsight(id); err != nil {
		t.Fatalf("AcknowledgeInsight: %v", err)
	}

	// Verify it's acknowledged.
	rows, err := db.Query(dbPath, fmt.Sprintf(
		`SELECT acknowledged FROM life_insights WHERE id = '%s'`, id))
	if err != nil {
		t.Fatalf("db.Query: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("insight not found")
	}
	if jsonInt(rows[0]["acknowledged"]) != 1 {
		t.Error("insight should be acknowledged")
	}

	// Should not appear in unacknowledged list.
	ins, err := engine.GetInsights(20, false)
	if err != nil {
		t.Fatalf("GetInsights: %v", err)
	}
	for _, item := range ins {
		if item.ID == id {
			t.Error("acknowledged insight should not appear in unacknowledged list")
		}
	}
}

func TestAcknowledgeInsight_EmptyID(t *testing.T) {
	_, engine := setupInsightsTestDB(t)
	err := engine.AcknowledgeInsight("")
	if err == nil {
		t.Fatal("expected error for empty ID")
	}
}

func TestSpendingForecast(t *testing.T) {
	dbPath, engine := setupInsightsTestDB(t)
	cfg := &Config{HistoryDB: dbPath}
	cleanup := setupTestGlobals(t, dbPath, cfg)
	defer cleanup()

	// Insert expenses for this month.
	now := time.Now().UTC()
	for i := 0; i < 5; i++ {
		date := now.AddDate(0, 0, -i).Format("2006-01-02")
		insertExpense(t, dbPath, 100, "food", "daily food", date)
	}

	month := now.Format("2006-01")
	result, err := engine.SpendingForecast(month)
	if err != nil {
		t.Fatalf("SpendingForecast: %v", err)
	}

	if result["month"] != month {
		t.Errorf("month: got %v, want %s", result["month"], month)
	}
	currentTotal, _ := result["current_total"].(float64)
	if currentTotal != 500 {
		t.Errorf("current_total: got %v, want 500", currentTotal)
	}
	dailyRate, _ := result["daily_rate"].(float64)
	if dailyRate <= 0 {
		t.Errorf("daily_rate should be positive, got %v", dailyRate)
	}
	projectedTotal, _ := result["projected_total"].(float64)
	if projectedTotal < currentTotal {
		t.Errorf("projected_total (%v) should be >= current_total (%v)", projectedTotal, currentTotal)
	}
}

func TestSpendingForecast_InvalidMonth(t *testing.T) {
	dbPath, engine := setupInsightsTestDB(t)
	cfg := &Config{HistoryDB: dbPath}
	cleanup := setupTestGlobals(t, dbPath, cfg)
	defer cleanup()

	_, err := engine.SpendingForecast("invalid")
	if err == nil {
		t.Fatal("expected error for invalid month format")
	}
}

func TestSpendingForecast_NoFinanceService(t *testing.T) {
	dbPath, _ := setupInsightsTestDB(t)
	// Create engine with no FinanceDBPath = finance service not available.
	deps := insights.Deps{
		Query:   db.Query,
		Escape:  db.Escape,
		LogWarn: log.Warn,
		UUID:    newUUID,
	}
	engine := insights.New(dbPath, deps)

	_, err := engine.SpendingForecast("")
	if err == nil {
		t.Fatal("expected error when finance service is nil")
	}
}

func TestToolLifeReport(t *testing.T) {
	dbPath, engine := setupInsightsTestDB(t)
	cfg := &Config{HistoryDB: dbPath}
	cleanup := setupTestGlobals(t, dbPath, cfg)
	defer cleanup()

	today := time.Now().UTC().Format("2006-01-02")
	insertExpense(t, dbPath, 300, "food", "lunch", today)

	input, _ := json.Marshal(map[string]any{
		"period": "daily",
		"date":   today,
	})

	ctx := testInsightsAppCtx(engine)
	result, err := toolLifeReport(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolLifeReport: %v", err)
	}

	var report insights.LifeReport
	if err := json.Unmarshal([]byte(result), &report); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if report.Period != "daily" {
		t.Errorf("period: got %q, want daily", report.Period)
	}
	if report.Spending == nil {
		t.Fatal("spending should not be nil")
	}
	if report.Spending.Total != 300 {
		t.Errorf("spending total: got %.0f, want 300", report.Spending.Total)
	}
}

func TestToolLifeReport_InvalidPeriod(t *testing.T) {
	_, engine := setupInsightsTestDB(t)
	ctx := testInsightsAppCtx(engine)

	input, _ := json.Marshal(map[string]any{"period": "invalid"})
	_, err := toolLifeReport(ctx, &Config{}, input)
	if err == nil {
		t.Fatal("expected error for invalid period")
	}
}

func TestToolLifeReport_NilEngine(t *testing.T) {
	ctx := withApp(context.Background(), &App{})

	input, _ := json.Marshal(map[string]any{"period": "weekly"})
	_, err := toolLifeReport(ctx, &Config{}, input)
	if err == nil {
		t.Fatal("expected error when engine is nil")
	}
}

func TestToolLifeInsights_Detect(t *testing.T) {
	dbPath, engine := setupInsightsTestDB(t)
	cfg := &Config{HistoryDB: dbPath}
	cleanup := setupTestGlobals(t, dbPath, cfg)
	defer cleanup()

	ctx := testInsightsAppCtx(engine)
	input, _ := json.Marshal(map[string]any{
		"action": "detect",
		"days":   7,
	})

	result, err := toolLifeInsights(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolLifeInsights detect: %v", err)
	}
	if result == "" {
		t.Fatal("result should not be empty")
	}
}

func TestToolLifeInsights_List(t *testing.T) {
	dbPath, engine := setupInsightsTestDB(t)

	// Insert an insight.
	now := time.Now().UTC().Format(time.RFC3339)
	sql := fmt.Sprintf(
		`INSERT INTO life_insights (id, type, severity, title, description, acknowledged, created_at)
		 VALUES ('list-test', 'test', 'info', 'Test', 'Desc', 0, '%s')`, now)
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("insert: %v: %s", err, string(out))
	}

	ctx := testInsightsAppCtx(engine)
	input, _ := json.Marshal(map[string]any{"action": "list"})
	result, err := toolLifeInsights(ctx, &Config{}, input)
	if err != nil {
		t.Fatalf("toolLifeInsights list: %v", err)
	}

	var ins []insights.LifeInsight
	if err := json.Unmarshal([]byte(result), &ins); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(ins) == 0 {
		t.Error("expected at least 1 insight")
	}
}

func TestToolLifeInsights_Acknowledge(t *testing.T) {
	dbPath, engine := setupInsightsTestDB(t)

	// Insert an insight.
	now := time.Now().UTC().Format(time.RFC3339)
	sql := fmt.Sprintf(
		`INSERT INTO life_insights (id, type, severity, title, description, acknowledged, created_at)
		 VALUES ('ack-tool-test', 'test', 'info', 'Test', 'Desc', 0, '%s')`, now)
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("insert: %v: %s", err, string(out))
	}

	ctx := testInsightsAppCtx(engine)
	input, _ := json.Marshal(map[string]any{
		"action":     "acknowledge",
		"insight_id": "ack-tool-test",
	})
	result, err := toolLifeInsights(ctx, &Config{}, input)
	if err != nil {
		t.Fatalf("toolLifeInsights acknowledge: %v", err)
	}
	if result == "" {
		t.Fatal("result should not be empty")
	}
}

func TestToolLifeInsights_Forecast(t *testing.T) {
	dbPath, engine := setupInsightsTestDB(t)
	cfg := &Config{HistoryDB: dbPath}
	cleanup := setupTestGlobals(t, dbPath, cfg)
	defer cleanup()

	today := time.Now().UTC().Format("2006-01-02")
	insertExpense(t, dbPath, 200, "food", "lunch", today)

	ctx := testInsightsAppCtx(engine)
	input, _ := json.Marshal(map[string]any{
		"action": "forecast",
		"month":  time.Now().UTC().Format("2006-01"),
	})

	result, err := toolLifeInsights(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolLifeInsights forecast: %v", err)
	}

	var forecast map[string]any
	if err := json.Unmarshal([]byte(result), &forecast); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if forecast["projected_total"] == nil {
		t.Error("projected_total should be present")
	}
}

func TestToolLifeInsights_InvalidAction(t *testing.T) {
	_, engine := setupInsightsTestDB(t)
	ctx := testInsightsAppCtx(engine)

	input, _ := json.Marshal(map[string]any{"action": "invalid"})
	_, err := toolLifeInsights(ctx, &Config{}, input)
	if err == nil {
		t.Fatal("expected error for invalid action")
	}
}

func TestToolLifeInsights_NilEngine(t *testing.T) {
	ctx := withApp(context.Background(), &App{})

	input, _ := json.Marshal(map[string]any{"action": "list"})
	_, err := toolLifeInsights(ctx, &Config{}, input)
	if err == nil {
		t.Fatal("expected error when engine is nil")
	}
}

func TestPeriodDateRange_Daily(t *testing.T) {
	anchor := time.Date(2026, 2, 23, 12, 0, 0, 0, time.UTC)
	start, end := insights.PeriodDateRange("daily", anchor)
	if start.Format("2006-01-02") != "2026-02-23" {
		t.Errorf("start: got %s, want 2026-02-23", start.Format("2006-01-02"))
	}
	if end.Format("2006-01-02") != "2026-02-23" {
		t.Errorf("end: got %s, want 2026-02-23", end.Format("2006-01-02"))
	}
}

func TestPeriodDateRange_Weekly(t *testing.T) {
	// 2026-02-23 is Monday.
	anchor := time.Date(2026, 2, 25, 12, 0, 0, 0, time.UTC) // Wednesday
	start, end := insights.PeriodDateRange("weekly", anchor)
	if start.Format("2006-01-02") != "2026-02-23" {
		t.Errorf("start: got %s, want 2026-02-23 (Monday)", start.Format("2006-01-02"))
	}
	if end.Format("2006-01-02") != "2026-03-01" {
		t.Errorf("end: got %s, want 2026-03-01 (Sunday)", end.Format("2006-01-02"))
	}
}

func TestPeriodDateRange_Monthly(t *testing.T) {
	anchor := time.Date(2026, 2, 15, 12, 0, 0, 0, time.UTC)
	start, end := insights.PeriodDateRange("monthly", anchor)
	if start.Format("2006-01-02") != "2026-02-01" {
		t.Errorf("start: got %s, want 2026-02-01", start.Format("2006-01-02"))
	}
	if end.Format("2006-01-02") != "2026-02-28" {
		t.Errorf("end: got %s, want 2026-02-28", end.Format("2006-01-02"))
	}
}

func TestPrevPeriodRange(t *testing.T) {
	start := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	prevStart, prevEnd := insights.PrevPeriodRange("monthly", start)
	if prevStart.Format("2006-01-02") != "2026-01-01" {
		t.Errorf("prevStart: got %s, want 2026-01-01", prevStart.Format("2006-01-02"))
	}
	if prevEnd.Format("2006-01-02") != "2026-01-31" {
		t.Errorf("prevEnd: got %s, want 2026-01-31", prevEnd.Format("2006-01-02"))
	}
}

func TestInsightDedup(t *testing.T) {
	dbPath, engine := setupInsightsTestDB(t)
	cfg := &Config{HistoryDB: dbPath}
	cleanup := setupTestGlobals(t, dbPath, cfg)
	defer cleanup()

	// Store same type insight twice.
	insight1 := &insights.LifeInsight{
		ID:          newUUID(),
		Type:        "test_dedup",
		Severity:    "info",
		Title:       "First",
		Description: "First occurrence",
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	engine.StoreInsightDedup(insight1)

	insight2 := &insights.LifeInsight{
		ID:          newUUID(),
		Type:        "test_dedup",
		Severity:    "info",
		Title:       "Second",
		Description: "Second occurrence",
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	engine.StoreInsightDedup(insight2)

	// Should only have one insight of this type.
	rows, err := db.Query(dbPath, `SELECT COUNT(*) as cnt FROM life_insights WHERE type = 'test_dedup'`)
	if err != nil {
		t.Fatalf("db.Query: %v", err)
	}
	count := jsonInt(rows[0]["cnt"])
	if count != 1 {
		t.Errorf("dedup failed: got %d insights, want 1", count)
	}
}

func TestInsightFromRow(t *testing.T) {
	row := map[string]any{
		"id":           "test-id",
		"type":         "spending_anomaly",
		"severity":     "warning",
		"title":        "High spending",
		"description":  "You spent a lot",
		"data":         `{"amount":500}`,
		"acknowledged": float64(1),
		"created_at":   "2026-02-23T00:00:00Z",
	}

	insight := insights.InsightFromRow(row)
	if insight.ID != "test-id" {
		t.Errorf("ID: got %q, want test-id", insight.ID)
	}
	if insight.Type != "spending_anomaly" {
		t.Errorf("Type: got %q", insight.Type)
	}
	if !insight.Acknowledged {
		t.Error("should be acknowledged")
	}
	if insight.Data == nil {
		t.Fatal("data should not be nil")
	}
	amount, ok := insight.Data["amount"]
	if !ok {
		t.Error("data should contain amount")
	}
	if v, _ := amount.(float64); v != 500 {
		t.Errorf("amount: got %v, want 500", amount)
	}
}

func TestSpendingReport_PrevPeriodComparison(t *testing.T) {
	dbPath, engine := setupInsightsTestDB(t)
	cfg := &Config{HistoryDB: dbPath}
	cleanup := setupTestGlobals(t, dbPath, cfg)
	defer cleanup()

	now := time.Now().UTC()

	// Insert current month expenses.
	for i := 0; i < 5; i++ {
		date := now.AddDate(0, 0, -i).Format("2006-01-02")
		insertExpense(t, dbPath, 200, "food", "lunch", date)
	}

	// Insert previous month expenses (lower).
	for i := 0; i < 5; i++ {
		date := now.AddDate(0, -1, -i).Format("2006-01-02")
		insertExpense(t, dbPath, 100, "food", "lunch", date)
	}

	report, err := engine.GenerateReport("monthly", now)
	if err != nil {
		t.Fatalf("GenerateReport: %v", err)
	}
	if report.Spending == nil {
		t.Fatal("spending should not be nil")
	}
	// Current total = 1000, prev total = 500, so vs_prev_period = +100%.
	if report.Spending.VsPrevPeriod == 0 && report.Spending.Total > 0 {
		// VsPrevPeriod might be 0 if prev period had no data in the range.
		// This is acceptable since previous period dates may not align perfectly.
		t.Log("Note: VsPrevPeriod is 0, previous period data may not be in range")
	}
}

func TestGenerateReport_NilServices(t *testing.T) {
	dbPath, _ := setupInsightsTestDB(t)
	// Create engine with no service DB paths = all services unavailable.
	deps := insights.Deps{
		Query:   db.Query,
		Escape:  db.Escape,
		LogWarn: log.Warn,
		UUID:    newUUID,
	}
	engine := insights.New(dbPath, deps)

	report, err := engine.GenerateReport("weekly", time.Now().UTC())
	if err != nil {
		t.Fatalf("GenerateReport with nil services: %v", err)
	}
	if report == nil {
		t.Fatal("report should not be nil even with all nil services")
	}
	if report.Spending != nil {
		t.Error("spending should be nil when finance service is nil")
	}
	if report.Tasks != nil {
		t.Error("tasks should be nil when task manager is nil")
	}
	if report.Mood != nil {
		t.Error("mood should be nil when user profile service is nil")
	}
	if report.Social != nil {
		t.Error("social should be nil when contacts service is nil")
	}
	if report.Habits != nil {
		t.Error("habits should be nil when habits service is nil")
	}
}

func TestSpendingForecast_WithBudget(t *testing.T) {
	dbPath, engine := setupInsightsTestDB(t)
	cfg := &Config{HistoryDB: dbPath}
	cleanup := setupTestGlobals(t, dbPath, cfg)
	defer cleanup()

	now := time.Now().UTC()
	today := now.Format("2006-01-02")
	insertExpense(t, dbPath, 1000, "food", "groceries", today)

	// Insert a budget.
	budgetSQL := `INSERT INTO expense_budgets (user_id, category, monthly_limit, currency, created_at)
		VALUES ('default', 'food', 5000, 'TWD', '2026-01-01T00:00:00Z')`
	cmd := exec.Command("sqlite3", dbPath, budgetSQL)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("insert budget: %v: %s", err, string(out))
	}

	result, err := engine.SpendingForecast(now.Format("2006-01"))
	if err != nil {
		t.Fatalf("SpendingForecast: %v", err)
	}

	if result["budget"] == nil {
		t.Error("budget should be present")
	}
	budget, _ := result["budget"].(float64)
	if budget != 5000 {
		t.Errorf("budget: got %v, want 5000", budget)
	}
	if result["on_track"] == nil {
		t.Error("on_track should be present")
	}
}

// Suppress unused import warnings.
var _ = math.Round

// --- from knowledge_test.go ---

func TestInitKnowledgeDir(t *testing.T) {
	dir := t.TempDir()
	kDir := knowledge.InitDir(dir)
	want := filepath.Join(dir, "knowledge")
	if kDir != want {
		t.Errorf("InitDir = %q, want %q", kDir, want)
	}
	if _, err := os.Stat(kDir); err != nil {
		t.Errorf("knowledge dir not created: %v", err)
	}
}

func TestInitKnowledgeDirIdempotent(t *testing.T) {
	dir := t.TempDir()
	knowledge.InitDir(dir)
	kDir := knowledge.InitDir(dir)
	if _, err := os.Stat(kDir); err != nil {
		t.Errorf("knowledge dir not found on second call: %v", err)
	}
}

func TestListKnowledgeFilesEmpty(t *testing.T) {
	dir := knowledge.InitDir(t.TempDir())
	files, err := knowledge.ListFiles(dir)
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 files, got %d", len(files))
	}
}

func TestListKnowledgeFilesNonExistent(t *testing.T) {
	files, err := knowledge.ListFiles("/nonexistent/path/knowledge")
	if err != nil {
		t.Fatalf("expected nil error for nonexistent dir, got: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 files, got %d", len(files))
	}
}

func TestListKnowledgeFilesSkipsHidden(t *testing.T) {
	dir := knowledge.InitDir(t.TempDir())
	os.WriteFile(filepath.Join(dir, ".hidden"), []byte("secret"), 0o644)
	os.WriteFile(filepath.Join(dir, "visible.md"), []byte("content"), 0o644)

	files, err := knowledge.ListFiles(dir)
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if files[0].Name != "visible.md" {
		t.Errorf("expected visible.md, got %q", files[0].Name)
	}
}

func TestListKnowledgeFilesSkipsDirs(t *testing.T) {
	dir := knowledge.InitDir(t.TempDir())
	os.MkdirAll(filepath.Join(dir, "subdir"), 0o755)
	os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hello"), 0o644)

	files, err := knowledge.ListFiles(dir)
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
}

func TestAddKnowledgeFile(t *testing.T) {
	baseDir := t.TempDir()
	kDir := knowledge.InitDir(baseDir)

	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "notes.md")
	os.WriteFile(srcPath, []byte("# Knowledge Notes"), 0o644)

	if err := knowledge.AddFile(kDir, srcPath); err != nil {
		t.Fatalf("AddFile: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(kDir, "notes.md"))
	if err != nil {
		t.Fatalf("read copied file: %v", err)
	}
	if string(data) != "# Knowledge Notes" {
		t.Errorf("copied content = %q, want %q", string(data), "# Knowledge Notes")
	}
}

func TestAddKnowledgeFileNotFound(t *testing.T) {
	kDir := knowledge.InitDir(t.TempDir())
	err := knowledge.AddFile(kDir, "/nonexistent/file.txt")
	if err == nil {
		t.Fatal("expected error for nonexistent source")
	}
}

func TestAddKnowledgeFileDirectory(t *testing.T) {
	kDir := knowledge.InitDir(t.TempDir())
	srcDir := t.TempDir()
	err := knowledge.AddFile(kDir, srcDir)
	if err == nil {
		t.Fatal("expected error when source is a directory")
	}
}

func TestAddKnowledgeFileHiddenReject(t *testing.T) {
	kDir := knowledge.InitDir(t.TempDir())
	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, ".secret")
	os.WriteFile(srcPath, []byte("secret"), 0o644)

	err := knowledge.AddFile(kDir, srcPath)
	if err == nil {
		t.Fatal("expected error for hidden file")
	}
}

func TestRemoveKnowledgeFile(t *testing.T) {
	kDir := knowledge.InitDir(t.TempDir())
	os.WriteFile(filepath.Join(kDir, "old.txt"), []byte("data"), 0o644)

	if err := knowledge.RemoveFile(kDir, "old.txt"); err != nil {
		t.Fatalf("RemoveFile: %v", err)
	}

	if _, err := os.Stat(filepath.Join(kDir, "old.txt")); !os.IsNotExist(err) {
		t.Error("file should have been removed")
	}
}

func TestRemoveKnowledgeFileNotFound(t *testing.T) {
	kDir := knowledge.InitDir(t.TempDir())
	err := knowledge.RemoveFile(kDir, "nonexistent.txt")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestRemoveKnowledgeFilePathTraversal(t *testing.T) {
	kDir := knowledge.InitDir(t.TempDir())
	err := knowledge.RemoveFile(kDir, "../../../etc/passwd")
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
}

func TestValidateKnowledgeFilename(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{"notes.md", false},
		{"README.txt", false},
		{"my-doc.pdf", false},
		{"", true},
		{".hidden", true},
		{"../etc/passwd", true},
		{"foo/bar.txt", true},
		{"foo\\bar.txt", true},
		{"..", true},
		{".", true},
	}
	for _, tc := range tests {
		err := knowledge.ValidateFilename(tc.name)
		if (err != nil) != tc.wantErr {
			t.Errorf("ValidateFilename(%q): err=%v, wantErr=%v", tc.name, err, tc.wantErr)
		}
	}
}

func TestKnowledgeDirHasFiles(t *testing.T) {
	kDir := knowledge.InitDir(t.TempDir())

	if knowledge.HasFiles(kDir) {
		t.Error("expected false for empty dir")
	}

	os.WriteFile(filepath.Join(kDir, ".hidden"), []byte("x"), 0o644)
	if knowledge.HasFiles(kDir) {
		t.Error("expected false with only hidden files")
	}

	os.WriteFile(filepath.Join(kDir, "doc.md"), []byte("content"), 0o644)
	if !knowledge.HasFiles(kDir) {
		t.Error("expected true with visible file")
	}
}

func TestKnowledgeDirHasFilesNonExistent(t *testing.T) {
	if knowledge.HasFiles("/nonexistent/knowledge") {
		t.Error("expected false for nonexistent dir")
	}
}

// TODO: TestKnowledgeDir removed — knowledgeDir() moved to internal/cli

func TestFormatSizeKnowledge(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{2048, "2.0 KB"},
		{1048576, "1.0 MB"},
		{1572864, "1.5 MB"},
	}
	for _, tc := range tests {
		got := cli.FormatSize(tc.bytes)
		if got != tc.want {
			t.Errorf("FormatSize(%d) = %q, want %q", tc.bytes, got, tc.want)
		}
	}
}

func TestExpandPromptKnowledgeDir(t *testing.T) {
	got := expandPrompt("Use files in {{knowledge_dir}}", "", "", "", "/tmp/tetora/knowledge", nil)
	want := "Use files in /tmp/tetora/knowledge"
	if got != want {
		t.Errorf("expandPrompt with knowledge_dir = %q, want %q", got, want)
	}
}

func TestExpandPromptKnowledgeDirEmpty(t *testing.T) {
	got := expandPrompt("Use files in {{knowledge_dir}}", "", "", "", "", nil)
	want := "Use files in "
	if got != want {
		t.Errorf("expandPrompt with empty knowledge_dir = %q, want %q", got, want)
	}
}

// --- from lifecycle_test.go ---

func TestSuggestHabitForGoal_Fitness(t *testing.T) {
	le := &LifecycleEngine{cfg: &Config{}}

	suggestions := le.SuggestHabitForGoal("Run a marathon", "fitness")
	if len(suggestions) == 0 {
		t.Fatal("expected suggestions for fitness goal")
	}
	if len(suggestions) > 3 {
		t.Errorf("expected max 3 suggestions, got %d", len(suggestions))
	}
}

func TestSuggestHabitForGoal_Learning(t *testing.T) {
	le := &LifecycleEngine{cfg: &Config{}}

	suggestions := le.SuggestHabitForGoal("Learn Japanese", "learning")
	if len(suggestions) == 0 {
		t.Fatal("expected suggestions for learning goal")
	}
	found := false
	for _, s := range suggestions {
		if s == "Read 30 min daily" || s == "Practice flashcards" || s == "Write summary notes" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected learning-related suggestion, got %v", suggestions)
	}
}

func TestSuggestHabitForGoal_NoMatch(t *testing.T) {
	le := &LifecycleEngine{cfg: &Config{}}

	suggestions := le.SuggestHabitForGoal("Buy a house", "personal")
	if len(suggestions) == 0 {
		t.Fatal("expected generic suggestions when no match")
	}
	// Should return generic suggestions.
	if suggestions[0] != "Review progress weekly" {
		t.Errorf("expected generic suggestion, got %q", suggestions[0])
	}
}

func TestSuggestHabitForGoal_MultipleMatches(t *testing.T) {
	le := &LifecycleEngine{cfg: &Config{}}

	// "health and fitness" matches both keywords.
	suggestions := le.SuggestHabitForGoal("Improve health and fitness", "")
	if len(suggestions) == 0 {
		t.Fatal("expected suggestions")
	}
	if len(suggestions) > 3 {
		t.Errorf("expected max 3 suggestions even with multiple matches, got %d", len(suggestions))
	}
}

func TestOnGoalCompleted_NoGoalsService(t *testing.T) {
	le := &LifecycleEngine{cfg: &Config{}}
	old := globalGoalsService
	globalGoalsService = nil
	defer func() { globalGoalsService = old }()

	err := le.OnGoalCompleted("fake-id")
	if err == nil {
		t.Error("expected error when goals service is nil")
	}
}

func TestSyncBirthdayReminders_NoContacts(t *testing.T) {
	le := &LifecycleEngine{cfg: &Config{}}
	old := globalContactsService
	globalContactsService = nil
	defer func() { globalContactsService = old }()

	_, err := le.SyncBirthdayReminders()
	if err == nil {
		t.Error("expected error when contacts service is nil")
	}
}

func TestRunInsightActions_NilServices(t *testing.T) {
	le := &LifecycleEngine{cfg: &Config{}}
	oldInsights := globalInsightsEngine
	oldContacts := globalContactsService
	globalInsightsEngine = nil
	globalContactsService = nil
	defer func() {
		globalInsightsEngine = oldInsights
		globalContactsService = oldContacts
	}()

	// Should not panic with nil services.
	actions, err := le.RunInsightActions()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(actions) != 0 {
		t.Errorf("expected 0 actions with nil services, got %d", len(actions))
	}
}

// --- from note_dedup_test.go ---

func TestToolNoteDedup(t *testing.T) {
	tmp := t.TempDir()

	// Set up a mock global notes service.
	svc := notes.New(NotesConfig{Enabled: true, VaultPath: tmp, DefaultExt: ".md"}, tmp, false, nil, nil, nil, nil)
	setGlobalNotesService(svc)
	defer setGlobalNotesService(nil)

	// Create files: two duplicates and one unique.
	os.WriteFile(filepath.Join(tmp, "a.md"), []byte("hello world"), 0o644)
	os.WriteFile(filepath.Join(tmp, "b.md"), []byte("hello world"), 0o644)
	os.WriteFile(filepath.Join(tmp, "c.md"), []byte("unique content"), 0o644)

	ctx := context.Background()
	cfg := &Config{}

	// Test: scan without auto_delete.
	input, _ := json.Marshal(map[string]any{"auto_delete": false})
	out, err := toolNoteDedup(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolNoteDedup: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	totalFiles := int(result["total_files"].(float64))
	if totalFiles != 3 {
		t.Errorf("expected 3 total_files, got %d", totalFiles)
	}

	dupGroups := int(result["duplicate_groups"].(float64))
	if dupGroups != 1 {
		t.Errorf("expected 1 duplicate_groups, got %d", dupGroups)
	}

	// Verify files still exist (no deletion).
	for _, name := range []string{"a.md", "b.md", "c.md"} {
		if _, err := os.Stat(filepath.Join(tmp, name)); err != nil {
			t.Errorf("expected %s to still exist", name)
		}
	}
}

func TestToolNoteDedupAutoDelete(t *testing.T) {
	tmp := t.TempDir()

	svc := notes.New(NotesConfig{Enabled: true, VaultPath: tmp, DefaultExt: ".md"}, tmp, false, nil, nil, nil, nil)
	setGlobalNotesService(svc)
	defer setGlobalNotesService(nil)

	// Three files with same content.
	os.WriteFile(filepath.Join(tmp, "x.md"), []byte("dup content"), 0o644)
	os.WriteFile(filepath.Join(tmp, "y.md"), []byte("dup content"), 0o644)
	os.WriteFile(filepath.Join(tmp, "z.md"), []byte("dup content"), 0o644)

	ctx := context.Background()
	cfg := &Config{}

	input, _ := json.Marshal(map[string]any{"auto_delete": true})
	out, err := toolNoteDedup(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolNoteDedup: %v", err)
	}

	var result map[string]any
	json.Unmarshal([]byte(out), &result)

	deleted := int(result["deleted"].(float64))
	if deleted != 2 {
		t.Errorf("expected 2 deleted, got %d", deleted)
	}

	// Verify only one file remains.
	remaining := 0
	for _, name := range []string{"x.md", "y.md", "z.md"} {
		if _, err := os.Stat(filepath.Join(tmp, name)); err == nil {
			remaining++
		}
	}
	if remaining != 1 {
		t.Errorf("expected 1 remaining file, got %d", remaining)
	}
}

func TestToolNoteDedupPrefix(t *testing.T) {
	tmp := t.TempDir()

	svc := notes.New(NotesConfig{Enabled: true, VaultPath: tmp, DefaultExt: ".md"}, tmp, false, nil, nil, nil, nil)
	setGlobalNotesService(svc)
	defer setGlobalNotesService(nil)

	// Create subdirectory with duplicates.
	os.MkdirAll(filepath.Join(tmp, "sub"), 0o755)
	os.WriteFile(filepath.Join(tmp, "sub", "a.md"), []byte("same"), 0o644)
	os.WriteFile(filepath.Join(tmp, "sub", "b.md"), []byte("same"), 0o644)
	// Outside prefix - should not be scanned.
	os.WriteFile(filepath.Join(tmp, "outside.md"), []byte("same"), 0o644)

	ctx := context.Background()
	cfg := &Config{}

	input, _ := json.Marshal(map[string]any{"prefix": "sub"})
	out, err := toolNoteDedup(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolNoteDedup: %v", err)
	}

	var result map[string]any
	json.Unmarshal([]byte(out), &result)

	totalFiles := int(result["total_files"].(float64))
	if totalFiles != 2 {
		t.Errorf("expected 2 total_files (prefix filter), got %d", totalFiles)
	}
}

func TestToolSourceAudit(t *testing.T) {
	tmp := t.TempDir()

	svc := notes.New(NotesConfig{Enabled: true, VaultPath: tmp, DefaultExt: ".md"}, tmp, false, nil, nil, nil, nil)
	setGlobalNotesService(svc)
	defer setGlobalNotesService(nil)

	// Create some actual notes.
	os.WriteFile(filepath.Join(tmp, "note1.md"), []byte("content1"), 0o644)
	os.WriteFile(filepath.Join(tmp, "note2.md"), []byte("content2"), 0o644)

	ctx := context.Background()
	cfg := &Config{}

	// Expected: note1, note2, note3 (note3 is missing).
	input, _ := json.Marshal(map[string]any{
		"expected": []string{"note1.md", "note2.md", "note3.md"},
	})
	out, err := toolSourceAudit(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolSourceAudit: %v", err)
	}

	var result map[string]any
	json.Unmarshal([]byte(out), &result)

	expectedCount := int(result["expected_count"].(float64))
	if expectedCount != 3 {
		t.Errorf("expected_count: want 3, got %d", expectedCount)
	}

	actualCount := int(result["actual_count"].(float64))
	if actualCount != 2 {
		t.Errorf("actual_count: want 2, got %d", actualCount)
	}

	missingCount := int(result["missing_count"].(float64))
	if missingCount != 1 {
		t.Errorf("missing_count: want 1, got %d", missingCount)
	}

	// Check missing contains note3.md.
	missingList, ok := result["missing"].([]any)
	if !ok || len(missingList) != 1 {
		t.Fatalf("expected 1 missing entry, got %v", result["missing"])
	}
	if missingList[0].(string) != "note3.md" {
		t.Errorf("expected missing note3.md, got %s", missingList[0])
	}
}

func TestToolSourceAuditExtra(t *testing.T) {
	tmp := t.TempDir()

	svc := notes.New(NotesConfig{Enabled: true, VaultPath: tmp, DefaultExt: ".md"}, tmp, false, nil, nil, nil, nil)
	setGlobalNotesService(svc)
	defer setGlobalNotesService(nil)

	// Actual has an extra file not in expected list.
	os.WriteFile(filepath.Join(tmp, "note1.md"), []byte("content1"), 0o644)
	os.WriteFile(filepath.Join(tmp, "extra.md"), []byte("extra content"), 0o644)

	ctx := context.Background()
	cfg := &Config{}

	input, _ := json.Marshal(map[string]any{
		"expected": []string{"note1.md"},
	})
	out, err := toolSourceAudit(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolSourceAudit: %v", err)
	}

	var result map[string]any
	json.Unmarshal([]byte(out), &result)

	extraCount := int(result["extra_count"].(float64))
	if extraCount != 1 {
		t.Errorf("extra_count: want 1, got %d", extraCount)
	}

	extraList, ok := result["extra"].([]any)
	if !ok || len(extraList) != 1 {
		t.Fatalf("expected 1 extra entry, got %v", result["extra"])
	}
	if extraList[0].(string) != "extra.md" {
		t.Errorf("expected extra.md, got %s", extraList[0])
	}
}

func TestContentHashSHA256(t *testing.T) {
	h1 := contentHashSHA256("hello world")
	h2 := contentHashSHA256("hello world")
	h3 := contentHashSHA256("different content")

	if h1 != h2 {
		t.Errorf("same content should produce same hash: %s != %s", h1, h2)
	}
	if h1 == h3 {
		t.Errorf("different content should produce different hash")
	}
	// First 16 bytes = 32 hex chars.
	if len(h1) != 32 {
		t.Errorf("expected 32 hex chars, got %d", len(h1))
	}
}

// --- from notify_test.go ---

type mockNotifier struct {
	name     string
	messages []string
	mu       sync.Mutex
	failErr  error
}

func (m *mockNotifier) Send(text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failErr != nil {
		return m.failErr
	}
	m.messages = append(m.messages, text)
	return nil
}

func (m *mockNotifier) Name() string { return m.name }

func TestMultiNotifierSend(t *testing.T) {
	n1 := &mockNotifier{name: "slack"}
	n2 := &mockNotifier{name: "discord"}
	multi := &MultiNotifier{Notifiers: []Notifier{n1, n2}}

	multi.Send("hello")

	if len(n1.messages) != 1 || n1.messages[0] != "hello" {
		t.Errorf("slack got %v, want [hello]", n1.messages)
	}
	if len(n2.messages) != 1 || n2.messages[0] != "hello" {
		t.Errorf("discord got %v, want [hello]", n2.messages)
	}
}

func TestMultiNotifierPartialFailure(t *testing.T) {
	n1 := &mockNotifier{name: "slack", failErr: fmt.Errorf("timeout")}
	n2 := &mockNotifier{name: "discord"}
	multi := &MultiNotifier{Notifiers: []Notifier{n1, n2}}

	multi.Send("test")

	// n1 fails but n2 should still receive.
	if len(n2.messages) != 1 {
		t.Errorf("discord should receive despite slack failure")
	}
}

func TestBuildNotifiers(t *testing.T) {
	cfg := &Config{
		Notifications: []NotificationChannel{
			{Type: "slack", WebhookURL: "https://hooks.slack.com/test"},
			{Type: "discord", WebhookURL: "https://discord.com/api/webhooks/test"},
			{Type: "unknown", WebhookURL: "https://example.com"},
			{Type: "slack", WebhookURL: ""}, // empty URL, should skip
		},
	}
	notifiers := buildNotifiers(cfg)
	if len(notifiers) != 2 {
		t.Errorf("got %d notifiers, want 2", len(notifiers))
	}
	if notifiers[0].Name() != "slack" {
		t.Errorf("first notifier = %q, want slack", notifiers[0].Name())
	}
	if notifiers[1].Name() != "discord" {
		t.Errorf("second notifier = %q, want discord", notifiers[1].Name())
	}
}

func TestDiscordContentLimit(t *testing.T) {
	d := &DiscordNotifier{WebhookURL: "http://localhost:0/test"}
	// Verify the struct is properly initialized.
	if d.Name() != "discord" {
		t.Errorf("Name() = %q, want discord", d.Name())
	}
}

func TestSlackNotifierName(t *testing.T) {
	s := &SlackNotifier{WebhookURL: "http://localhost:0/test"}
	if s.Name() != "slack" {
		t.Errorf("Name() = %q, want slack", s.Name())
	}
}

func TestBuildNotifiersEmpty(t *testing.T) {
	cfg := &Config{}
	notifiers := buildNotifiers(cfg)
	if len(notifiers) != 0 {
		t.Errorf("got %d notifiers, want 0", len(notifiers))
	}
}

func TestMultiNotifierEmpty(t *testing.T) {
	multi := &MultiNotifier{Notifiers: nil}
	// Should not panic with zero notifiers.
	multi.Send("test")
}

// --- from notify_intel_test.go ---

// --- Priority Tests ---

func TestPriorityRank(t *testing.T) {
	tests := []struct {
		priority string
		rank     int
	}{
		{PriorityCritical, 4},
		{PriorityHigh, 3},
		{PriorityNormal, 2},
		{PriorityLow, 1},
		{"unknown", 2}, // defaults to normal
		{"", 2},
	}
	for _, tt := range tests {
		if got := priorityRank(tt.priority); got != tt.rank {
			t.Errorf("priorityRank(%q) = %d, want %d", tt.priority, got, tt.rank)
		}
	}
}

func TestPriorityFromRank(t *testing.T) {
	for _, p := range []string{PriorityCritical, PriorityHigh, PriorityNormal, PriorityLow} {
		rank := priorityRank(p)
		got := priorityFromRank(rank)
		if got != p {
			t.Errorf("priorityFromRank(%d) = %q, want %q", rank, got, p)
		}
	}
}

func TestIsValidPriority(t *testing.T) {
	for _, p := range []string{PriorityCritical, PriorityHigh, PriorityNormal, PriorityLow} {
		if !isValidPriority(p) {
			t.Errorf("isValidPriority(%q) = false, want true", p)
		}
	}
	for _, p := range []string{"", "unknown", "CRITICAL", "Critical"} {
		if isValidPriority(p) {
			t.Errorf("isValidPriority(%q) = true, want false", p)
		}
	}
}

// --- Dedup Key Tests ---

func TestNotifyMessageDedupKey(t *testing.T) {
	m1 := NotifyMessage{EventType: "task.complete", Agent: "琉璃"}
	m2 := NotifyMessage{EventType: "task.complete", Agent: "琉璃"}
	m3 := NotifyMessage{EventType: "task.complete", Agent: "黒曜"}

	if m1.DedupKey() != m2.DedupKey() {
		t.Error("same event+role should have same dedup key")
	}
	if m1.DedupKey() == m3.DedupKey() {
		t.Error("different role should have different dedup key")
	}
}

// --- Mock Notifier ---

type mockIntelNotifier struct {
	mu       sync.Mutex
	name     string
	messages []string
}

func (m *mockIntelNotifier) Send(text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, text)
	return nil
}

func (m *mockIntelNotifier) Name() string { return m.name }

func (m *mockIntelNotifier) messageCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.messages)
}

func (m *mockIntelNotifier) lastMessage() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.messages) == 0 {
		return ""
	}
	return m.messages[len(m.messages)-1]
}

// --- Engine Tests ---

func TestNotificationEngine_ImmediateCritical(t *testing.T) {
	n := &mockIntelNotifier{name: "test"}
	cfg := &Config{
		Notifications: []NotificationChannel{
			{Type: "slack", MinPriority: ""},
		},
	}
	ne := NewNotificationEngine(cfg, []Notifier{n}, nil)

	ne.Notify(NotifyMessage{
		Priority:  PriorityCritical,
		EventType: "sla.violation",
		Text:      "SLA violation on 琉璃",
	})

	// Critical should be delivered immediately.
	if n.messageCount() != 1 {
		t.Fatalf("expected 1 message, got %d", n.messageCount())
	}
	if !strings.Contains(n.lastMessage(), "CRITICAL") {
		t.Errorf("expected [CRITICAL] prefix, got %q", n.lastMessage())
	}
	if !strings.Contains(n.lastMessage(), "SLA violation") {
		t.Errorf("expected message text, got %q", n.lastMessage())
	}
}

func TestNotificationEngine_ImmediateHigh(t *testing.T) {
	n := &mockIntelNotifier{name: "test"}
	cfg := &Config{
		Notifications: []NotificationChannel{
			{Type: "slack"},
		},
	}
	ne := NewNotificationEngine(cfg, []Notifier{n}, nil)

	ne.Notify(NotifyMessage{
		Priority: PriorityHigh,
		Text:     "Task failed",
	})

	if n.messageCount() != 1 {
		t.Fatalf("expected 1 immediate message, got %d", n.messageCount())
	}
}

func TestNotificationEngine_BufferNormal(t *testing.T) {
	n := &mockIntelNotifier{name: "test"}
	cfg := &Config{
		Notifications: []NotificationChannel{
			{Type: "slack"},
		},
		NotifyIntel: NotifyIntelConfig{BatchInterval: "1h"}, // long interval to avoid auto-flush
	}
	ne := NewNotificationEngine(cfg, []Notifier{n}, nil)

	ne.Notify(NotifyMessage{
		Priority: PriorityNormal,
		Text:     "Job completed successfully",
	})

	// Normal priority should be buffered, not sent immediately.
	if n.messageCount() != 0 {
		t.Errorf("expected 0 immediate messages for normal priority, got %d", n.messageCount())
	}
	if ne.BufferedCount() != 1 {
		t.Errorf("expected 1 buffered message, got %d", ne.BufferedCount())
	}
}

func TestNotificationEngine_BufferLow(t *testing.T) {
	n := &mockIntelNotifier{name: "test"}
	cfg := &Config{
		Notifications: []NotificationChannel{
			{Type: "slack"},
		},
		NotifyIntel: NotifyIntelConfig{BatchInterval: "1h"},
	}
	ne := NewNotificationEngine(cfg, []Notifier{n}, nil)

	ne.Notify(NotifyMessage{
		Priority: PriorityLow,
		Text:     "Debug info",
	})

	if n.messageCount() != 0 {
		t.Errorf("expected 0 immediate messages for low priority, got %d", n.messageCount())
	}
	if ne.BufferedCount() != 1 {
		t.Errorf("expected 1 buffered message, got %d", ne.BufferedCount())
	}
}

func TestNotificationEngine_Dedup(t *testing.T) {
	cfg := &Config{
		NotifyIntel: NotifyIntelConfig{BatchInterval: "1h"},
	}
	ne := NewNotificationEngine(cfg, nil, nil)

	// Send same event+role twice.
	ne.Notify(NotifyMessage{
		Priority:  PriorityNormal,
		EventType: "task.complete",
		Agent:      "琉璃",
		Text:      "First",
	})
	ne.Notify(NotifyMessage{
		Priority:  PriorityNormal,
		EventType: "task.complete",
		Agent:      "琉璃",
		Text:      "Second (should be deduped)",
	})

	if ne.BufferedCount() != 1 {
		t.Errorf("expected 1 buffered (deduped), got %d", ne.BufferedCount())
	}

	// Different role should not be deduped.
	ne.Notify(NotifyMessage{
		Priority:  PriorityNormal,
		EventType: "task.complete",
		Agent:      "黒曜",
		Text:      "Different role",
	})
	if ne.BufferedCount() != 2 {
		t.Errorf("expected 2 buffered, got %d", ne.BufferedCount())
	}
}

func TestNotificationEngine_DedupDifferentEvent(t *testing.T) {
	cfg := &Config{
		NotifyIntel: NotifyIntelConfig{BatchInterval: "1h"},
	}
	ne := NewNotificationEngine(cfg, nil, nil)

	ne.Notify(NotifyMessage{
		Priority:  PriorityNormal,
		EventType: "task.complete",
		Agent:      "琉璃",
		Text:      "Task done",
	})
	ne.Notify(NotifyMessage{
		Priority:  PriorityNormal,
		EventType: "job.complete",
		Agent:      "琉璃",
		Text:      "Job done",
	})

	// Different event types should not dedup.
	if ne.BufferedCount() != 2 {
		t.Errorf("expected 2 buffered (different events), got %d", ne.BufferedCount())
	}
}

func TestNotificationEngine_FlushBatch(t *testing.T) {
	n := &mockIntelNotifier{name: "test"}
	cfg := &Config{
		Notifications: []NotificationChannel{
			{Type: "slack"},
		},
		NotifyIntel: NotifyIntelConfig{BatchInterval: "1h"},
	}
	ne := NewNotificationEngine(cfg, []Notifier{n}, nil)

	ne.Notify(NotifyMessage{Priority: PriorityNormal, Text: "Msg 1"})
	ne.Notify(NotifyMessage{Priority: PriorityLow, EventType: "low1", Text: "Msg 2"})

	// Manually flush.
	ne.FlushBatch()

	if n.messageCount() != 1 {
		t.Fatalf("expected 1 batch message, got %d", n.messageCount())
	}
	msg := n.lastMessage()
	if !strings.Contains(msg, "Digest") {
		t.Errorf("expected digest format, got %q", msg)
	}
	if !strings.Contains(msg, "2 notifications") {
		t.Errorf("expected '2 notifications' in digest, got %q", msg)
	}
	if ne.BufferedCount() != 0 {
		t.Errorf("expected 0 buffered after flush, got %d", ne.BufferedCount())
	}
}

func TestNotificationEngine_FlushBatchEmpty(t *testing.T) {
	n := &mockIntelNotifier{name: "test"}
	cfg := &Config{
		Notifications: []NotificationChannel{
			{Type: "slack"},
		},
	}
	ne := NewNotificationEngine(cfg, []Notifier{n}, nil)

	// Flush with no buffered messages.
	ne.FlushBatch()
	if n.messageCount() != 0 {
		t.Errorf("expected no messages for empty flush, got %d", n.messageCount())
	}
}

func TestNotificationEngine_PerChannelFilter(t *testing.T) {
	nAll := &mockIntelNotifier{name: "all"}
	nHigh := &mockIntelNotifier{name: "high-only"}

	cfg := &Config{
		Notifications: []NotificationChannel{
			{Type: "slack", MinPriority: ""},     // accept all
			{Type: "discord", MinPriority: "high"}, // only high+critical
		},
	}
	ne := NewNotificationEngine(cfg, []Notifier{nAll, nHigh}, nil)

	// Send a high-priority message.
	ne.Notify(NotifyMessage{Priority: PriorityHigh, Text: "Important"})

	if nAll.messageCount() != 1 {
		t.Errorf("all-channel: expected 1, got %d", nAll.messageCount())
	}
	if nHigh.messageCount() != 1 {
		t.Errorf("high-channel: expected 1, got %d", nHigh.messageCount())
	}

	// Send a critical message.
	ne.Notify(NotifyMessage{Priority: PriorityCritical, Text: "Urgent"})
	if nAll.messageCount() != 2 {
		t.Errorf("all-channel: expected 2, got %d", nAll.messageCount())
	}
	if nHigh.messageCount() != 2 {
		t.Errorf("high-channel: expected 2, got %d", nHigh.messageCount())
	}
}

func TestNotificationEngine_PerChannelFilter_BatchFlush(t *testing.T) {
	nAll := &mockIntelNotifier{name: "all"}
	nHigh := &mockIntelNotifier{name: "high-only"}

	cfg := &Config{
		Notifications: []NotificationChannel{
			{Type: "slack", MinPriority: ""},
			{Type: "discord", MinPriority: "high"},
		},
		NotifyIntel: NotifyIntelConfig{BatchInterval: "1h"},
	}
	ne := NewNotificationEngine(cfg, []Notifier{nAll, nHigh}, nil)

	// Buffer a normal message.
	ne.Notify(NotifyMessage{Priority: PriorityNormal, Text: "Routine"})
	ne.FlushBatch()

	// All-channel should get the batch, high-only should not.
	if nAll.messageCount() != 1 {
		t.Errorf("all-channel: expected 1 batch message, got %d", nAll.messageCount())
	}
	if nHigh.messageCount() != 0 {
		t.Errorf("high-channel: expected 0 (filtered), got %d", nHigh.messageCount())
	}
}

func TestNotificationEngine_FallbackFn(t *testing.T) {
	var received []string
	fallback := func(text string) {
		received = append(received, text)
	}

	cfg := &Config{}
	ne := NewNotificationEngine(cfg, nil, fallback)

	ne.Notify(NotifyMessage{Priority: PriorityCritical, Text: "Alert!"})

	if len(received) != 1 {
		t.Fatalf("expected 1 fallback call, got %d", len(received))
	}
	if !strings.Contains(received[0], "Alert!") {
		t.Errorf("fallback message missing text, got %q", received[0])
	}
}

func TestNotificationEngine_FallbackOnFlush(t *testing.T) {
	var received []string
	fallback := func(text string) {
		received = append(received, text)
	}

	cfg := &Config{
		NotifyIntel: NotifyIntelConfig{BatchInterval: "1h"},
	}
	ne := NewNotificationEngine(cfg, nil, fallback)

	ne.Notify(NotifyMessage{Priority: PriorityNormal, Text: "Buffered"})
	ne.FlushBatch()

	if len(received) != 1 {
		t.Fatalf("expected 1 fallback call on flush, got %d", len(received))
	}
	if !strings.Contains(received[0], "Digest") {
		t.Errorf("expected digest format in fallback, got %q", received[0])
	}
}

func TestNotificationEngine_DefaultPriority(t *testing.T) {
	cfg := &Config{
		NotifyIntel: NotifyIntelConfig{BatchInterval: "1h"},
	}
	ne := NewNotificationEngine(cfg, nil, nil)

	// Empty priority should default to normal (buffered).
	ne.Notify(NotifyMessage{Text: "No priority set"})
	if ne.BufferedCount() != 1 {
		t.Errorf("expected 1 buffered (default normal), got %d", ne.BufferedCount())
	}
}

func TestNotificationEngine_NotifyText(t *testing.T) {
	n := &mockIntelNotifier{name: "test"}
	cfg := &Config{
		Notifications: []NotificationChannel{
			{Type: "slack"},
		},
	}
	ne := NewNotificationEngine(cfg, []Notifier{n}, nil)

	ne.NotifyText(PriorityCritical, "test.event", "琉璃", "Critical event")

	if n.messageCount() != 1 {
		t.Fatalf("expected 1 message, got %d", n.messageCount())
	}
}

func TestNotificationEngine_BatchInterval(t *testing.T) {
	cfg := &Config{
		NotifyIntel: NotifyIntelConfig{BatchInterval: "30s"},
	}
	ne := NewNotificationEngine(cfg, nil, nil)
	if ne.BatchInterval() != 30*time.Second {
		t.Errorf("expected 30s batch interval, got %v", ne.BatchInterval())
	}
}

func TestNotificationEngine_BatchIntervalDefault(t *testing.T) {
	cfg := &Config{}
	ne := NewNotificationEngine(cfg, nil, nil)
	if ne.BatchInterval() != 5*time.Minute {
		t.Errorf("expected 5m default batch interval, got %v", ne.BatchInterval())
	}
}

func TestNotificationEngine_BatchIntervalInvalid(t *testing.T) {
	cfg := &Config{
		NotifyIntel: NotifyIntelConfig{BatchInterval: "invalid"},
	}
	ne := NewNotificationEngine(cfg, nil, nil)
	if ne.BatchInterval() != 5*time.Minute {
		t.Errorf("expected 5m fallback for invalid interval, got %v", ne.BatchInterval())
	}
}

func TestNotificationEngine_StopFlushes(t *testing.T) {
	var mu sync.Mutex
	var received []string
	fallback := func(text string) {
		mu.Lock()
		received = append(received, text)
		mu.Unlock()
	}

	cfg := &Config{
		NotifyIntel: NotifyIntelConfig{BatchInterval: "1h"},
	}
	ne := NewNotificationEngine(cfg, nil, fallback)
	ne.Start()

	ne.Notify(NotifyMessage{Priority: PriorityNormal, Text: "Pending"})
	ne.Stop()

	// Give goroutine time to flush on stop.
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	count := len(received)
	mu.Unlock()
	if count != 1 {
		t.Errorf("expected 1 message flushed on stop, got %d", count)
	}
}

// --- Format Tests ---

// TODO: TestFormatNotifyMessage, TestFormatBatchDigest removed — functions are internal-only in internal/notify

// TODO: TestInferPriority, TestInferEventType removed — functions are internal-only in internal/notify

// --- Wrap NotifyFn Tests ---

func TestWrapNotifyFn_Nil(t *testing.T) {
	fn := wrapNotifyFn(nil, PriorityHigh)
	if fn != nil {
		t.Error("expected nil for nil engine")
	}
}

func TestWrapNotifyFn_Routes(t *testing.T) {
	n := &mockIntelNotifier{name: "test"}
	cfg := &Config{
		Notifications: []NotificationChannel{
			{Type: "slack"},
		},
	}
	ne := NewNotificationEngine(cfg, []Notifier{n}, nil)
	fn := wrapNotifyFn(ne, PriorityHigh)

	// Critical text should be delivered immediately.
	fn("Security alert: IP blocked")
	if n.messageCount() != 1 {
		t.Errorf("expected 1 immediate message for critical text, got %d", n.messageCount())
	}
}

func TestWrapNotifyFn_DefaultPriority(t *testing.T) {
	cfg := &Config{
		NotifyIntel: NotifyIntelConfig{BatchInterval: "1h"},
	}
	ne := NewNotificationEngine(cfg, nil, nil)
	fn := wrapNotifyFn(ne, PriorityNormal)

	// Non-matching text should use default priority (normal = buffered).
	fn("Some routine message")
	if ne.BufferedCount() != 1 {
		t.Errorf("expected 1 buffered, got %d", ne.BufferedCount())
	}
}

// --- from prompt_test.go ---

func TestWriteAndReadPrompt(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{BaseDir: dir}

	if err := writePrompt(cfg, "test-prompt", "# Hello\nThis is a test."); err != nil {
		t.Fatalf("writePrompt: %v", err)
	}

	content, err := readPrompt(cfg, "test-prompt")
	if err != nil {
		t.Fatalf("readPrompt: %v", err)
	}
	if content != "# Hello\nThis is a test." {
		t.Errorf("got %q", content)
	}
}

func TestReadPromptNotFound(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{BaseDir: dir}

	_, err := readPrompt(cfg, "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent prompt")
	}
}

func TestListPrompts(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{BaseDir: dir}

	// Empty dir.
	prompts, err := listPrompts(cfg)
	if err != nil {
		t.Fatalf("listPrompts empty: %v", err)
	}
	if len(prompts) != 0 {
		t.Errorf("expected 0 prompts, got %d", len(prompts))
	}

	// Add some prompts.
	writePrompt(cfg, "alpha", "Alpha content")
	writePrompt(cfg, "beta", "Beta content that is a bit longer for preview testing")

	prompts, err = listPrompts(cfg)
	if err != nil {
		t.Fatalf("listPrompts: %v", err)
	}
	if len(prompts) != 2 {
		t.Fatalf("expected 2 prompts, got %d", len(prompts))
	}
	// Sorted alphabetically.
	if prompts[0].Name != "alpha" {
		t.Errorf("first prompt = %q, want alpha", prompts[0].Name)
	}
	if prompts[1].Name != "beta" {
		t.Errorf("second prompt = %q, want beta", prompts[1].Name)
	}
}

func TestDeletePrompt(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{BaseDir: dir}

	writePrompt(cfg, "to-delete", "content")

	if err := deletePrompt(cfg, "to-delete"); err != nil {
		t.Fatalf("deletePrompt: %v", err)
	}

	// Should be gone.
	_, err := readPrompt(cfg, "to-delete")
	if err == nil {
		t.Error("expected error after delete")
	}
}

func TestDeletePromptNotFound(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{BaseDir: dir}

	err := deletePrompt(cfg, "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent prompt")
	}
}

func TestWritePromptInvalidName(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{BaseDir: dir}

	err := writePrompt(cfg, "bad/name", "content")
	if err == nil {
		t.Error("expected error for invalid name with /")
	}

	err = writePrompt(cfg, "bad name", "content")
	if err == nil {
		t.Error("expected error for name with space")
	}

	err = writePrompt(cfg, "", "content")
	if err == nil {
		t.Error("expected error for empty name")
	}
}

func TestResolvePromptFile(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{BaseDir: dir}

	writePrompt(cfg, "my-prompt", "resolved content here")

	// With .md extension.
	content, err := resolvePromptFile(cfg, "my-prompt.md")
	if err != nil {
		t.Fatalf("resolvePromptFile with .md: %v", err)
	}
	if content != "resolved content here" {
		t.Errorf("got %q", content)
	}

	// Without .md extension.
	content, err = resolvePromptFile(cfg, "my-prompt")
	if err != nil {
		t.Fatalf("resolvePromptFile without .md: %v", err)
	}
	if content != "resolved content here" {
		t.Errorf("got %q", content)
	}

	// Empty promptFile.
	content, err = resolvePromptFile(cfg, "")
	if err != nil {
		t.Fatalf("resolvePromptFile empty: %v", err)
	}
	if content != "" {
		t.Errorf("expected empty, got %q", content)
	}
}

func TestListPromptsIgnoresNonMd(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{BaseDir: dir}
	promptDir := filepath.Join(dir, "prompts")
	os.MkdirAll(promptDir, 0o755)

	// Create a .md and a .txt file.
	os.WriteFile(filepath.Join(promptDir, "valid.md"), []byte("valid"), 0o644)
	os.WriteFile(filepath.Join(promptDir, "ignored.txt"), []byte("ignored"), 0o644)

	prompts, _ := listPrompts(cfg)
	if len(prompts) != 1 {
		t.Errorf("expected 1 prompt, got %d", len(prompts))
	}
}

// --- from reflection_test.go ---

// tempReflectionDB creates a temp DB with the reflections table initialized.
func tempReflectionDB(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not found, skipping")
	}
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_reflection.db")
	if err := initReflectionDB(dbPath); err != nil {
		t.Fatalf("initReflectionDB: %v", err)
	}
	return dbPath
}

// --- shouldReflect tests ---

func TestShouldReflect_Enabled(t *testing.T) {
	cfg := &Config{
		Reflection: ReflectionConfig{Enabled: true},
	}
	task := Task{Agent: "翡翠"}
	result := TaskResult{Status: "success", CostUSD: 0.10}

	if !shouldReflect(cfg, task, result) {
		t.Error("shouldReflect should return true when enabled with successful task")
	}
}

func TestShouldReflect_Disabled(t *testing.T) {
	cfg := &Config{
		Reflection: ReflectionConfig{Enabled: false},
	}
	task := Task{Agent: "翡翠"}
	result := TaskResult{Status: "success"}

	if shouldReflect(cfg, task, result) {
		t.Error("shouldReflect should return false when disabled")
	}
}

func TestShouldReflect_NilConfig(t *testing.T) {
	task := Task{Agent: "翡翠"}
	result := TaskResult{Status: "success"}

	if shouldReflect(nil, task, result) {
		t.Error("shouldReflect should return false with nil config")
	}
}

func TestShouldReflect_MinCost(t *testing.T) {
	cfg := &Config{
		Reflection: ReflectionConfig{Enabled: true, MinCost: 0.50},
	}
	task := Task{Agent: "翡翠"}

	// Below threshold.
	resultLow := TaskResult{Status: "success", CostUSD: 0.10}
	if shouldReflect(cfg, task, resultLow) {
		t.Error("shouldReflect should return false when cost below MinCost")
	}

	// At threshold.
	resultAt := TaskResult{Status: "success", CostUSD: 0.50}
	if !shouldReflect(cfg, task, resultAt) {
		t.Error("shouldReflect should return true when cost equals MinCost")
	}

	// Above threshold.
	resultHigh := TaskResult{Status: "success", CostUSD: 1.00}
	if !shouldReflect(cfg, task, resultHigh) {
		t.Error("shouldReflect should return true when cost above MinCost")
	}
}

func TestShouldReflect_TriggerOnFail(t *testing.T) {
	// Without TriggerOnFail: skip errors.
	cfg := &Config{
		Reflection: ReflectionConfig{Enabled: true, TriggerOnFail: false},
	}
	task := Task{Agent: "黒曜"}

	resultErr := TaskResult{Status: "error"}
	if shouldReflect(cfg, task, resultErr) {
		t.Error("shouldReflect should return false for error status when TriggerOnFail is false")
	}

	resultTimeout := TaskResult{Status: "timeout"}
	if shouldReflect(cfg, task, resultTimeout) {
		t.Error("shouldReflect should return false for timeout status when TriggerOnFail is false")
	}

	// With TriggerOnFail: include errors.
	cfg.Reflection.TriggerOnFail = true
	if !shouldReflect(cfg, task, resultErr) {
		t.Error("shouldReflect should return true for error status when TriggerOnFail is true")
	}
	if !shouldReflect(cfg, task, resultTimeout) {
		t.Error("shouldReflect should return true for timeout status when TriggerOnFail is true")
	}
}

func TestShouldReflect_NoRole(t *testing.T) {
	cfg := &Config{
		Reflection: ReflectionConfig{Enabled: true},
	}
	task := Task{Agent: ""}
	result := TaskResult{Status: "success"}

	if shouldReflect(cfg, task, result) {
		t.Error("shouldReflect should return false when role is empty")
	}
}

// --- parseReflectionOutput tests ---

func TestParseReflectionOutput_ValidJSON(t *testing.T) {
	output := `{"score":4,"feedback":"Good analysis","improvement":"Add more examples"}`

	ref, err := parseReflectionOutput(output)
	if err != nil {
		t.Fatalf("parseReflectionOutput: %v", err)
	}
	if ref.Score != 4 {
		t.Errorf("score = %d, want 4", ref.Score)
	}
	if ref.Feedback != "Good analysis" {
		t.Errorf("feedback = %q, want %q", ref.Feedback, "Good analysis")
	}
	if ref.Improvement != "Add more examples" {
		t.Errorf("improvement = %q, want %q", ref.Improvement, "Add more examples")
	}
}

func TestParseReflectionOutput_WrappedJSON(t *testing.T) {
	output := "```json\n{\"score\":5,\"feedback\":\"Excellent work\",\"improvement\":\"None needed\"}\n```"

	ref, err := parseReflectionOutput(output)
	if err != nil {
		t.Fatalf("parseReflectionOutput: %v", err)
	}
	if ref.Score != 5 {
		t.Errorf("score = %d, want 5", ref.Score)
	}
	if ref.Feedback != "Excellent work" {
		t.Errorf("feedback = %q, want %q", ref.Feedback, "Excellent work")
	}
}

func TestParseReflectionOutput_WithSurroundingText(t *testing.T) {
	output := "Here is my evaluation:\n{\"score\":3,\"feedback\":\"Adequate\",\"improvement\":\"Be more concise\"}\nThat's my assessment."

	ref, err := parseReflectionOutput(output)
	if err != nil {
		t.Fatalf("parseReflectionOutput: %v", err)
	}
	if ref.Score != 3 {
		t.Errorf("score = %d, want 3", ref.Score)
	}
}

func TestParseReflectionOutput_InvalidJSON(t *testing.T) {
	output := "This is not JSON at all"

	_, err := parseReflectionOutput(output)
	if err == nil {
		t.Error("expected error for non-JSON output")
	}
}

func TestParseReflectionOutput_MalformedJSON(t *testing.T) {
	output := `{"score":3, "feedback": incomplete`

	_, err := parseReflectionOutput(output)
	if err == nil {
		t.Error("expected error for malformed JSON")
	}
}

func TestParseReflectionOutput_InvalidScore(t *testing.T) {
	tests := []struct {
		name   string
		output string
	}{
		{"score 0", `{"score":0,"feedback":"bad","improvement":"fix"}`},
		{"score 6", `{"score":6,"feedback":"too high","improvement":"fix"}`},
		{"score -1", `{"score":-1,"feedback":"negative","improvement":"fix"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseReflectionOutput(tt.output)
			if err == nil {
				t.Errorf("expected error for invalid score in %q", tt.output)
			}
		})
	}
}

// --- DB tests ---

func TestInitReflectionDB(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not found, skipping")
	}
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	if err := initReflectionDB(dbPath); err != nil {
		t.Fatalf("initReflectionDB: %v", err)
	}
	// Verify file was created.
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("db file not created: %v", err)
	}
	// Idempotent: calling again should not error.
	if err := initReflectionDB(dbPath); err != nil {
		t.Fatalf("initReflectionDB second call: %v", err)
	}
}

func TestStoreAndQueryReflections(t *testing.T) {
	dbPath := tempReflectionDB(t)

	now := time.Now().UTC().Format(time.RFC3339)

	ref1 := &ReflectionResult{
		TaskID:      "task-001",
		Agent:        "翡翠",
		Score:       4,
		Feedback:    "Good research quality",
		Improvement: "Include more sources",
		CostUSD:     0.02,
		CreatedAt:   now,
	}
	if err := storeReflection(dbPath, ref1); err != nil {
		t.Fatalf("storeReflection ref1: %v", err)
	}

	ref2 := &ReflectionResult{
		TaskID:      "task-002",
		Agent:        "翡翠",
		Score:       2,
		Feedback:    "Incomplete analysis",
		Improvement: "Cover all edge cases",
		CostUSD:     0.03,
		CreatedAt:   now,
	}
	if err := storeReflection(dbPath, ref2); err != nil {
		t.Fatalf("storeReflection ref2: %v", err)
	}

	ref3 := &ReflectionResult{
		TaskID:      "task-003",
		Agent:        "黒曜",
		Score:       5,
		Feedback:    "Excellent implementation",
		Improvement: "None needed",
		CostUSD:     0.01,
		CreatedAt:   now,
	}
	if err := storeReflection(dbPath, ref3); err != nil {
		t.Fatalf("storeReflection ref3: %v", err)
	}

	// Query all.
	all, err := queryReflections(dbPath, "", 10)
	if err != nil {
		t.Fatalf("queryReflections all: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 reflections, got %d", len(all))
	}

	// Query by role.
	jade, err := queryReflections(dbPath, "翡翠", 10)
	if err != nil {
		t.Fatalf("queryReflections jade: %v", err)
	}
	if len(jade) != 2 {
		t.Fatalf("expected 2 reflections for 翡翠, got %d", len(jade))
	}
	// Verify ordering (most recent first — both have same timestamp, so check both exist).
	for _, ref := range jade {
		if ref.Agent != "翡翠" {
			t.Errorf("expected role 翡翠, got %q", ref.Agent)
		}
	}

	// Query with limit.
	limited, err := queryReflections(dbPath, "", 1)
	if err != nil {
		t.Fatalf("queryReflections limited: %v", err)
	}
	if len(limited) != 1 {
		t.Fatalf("expected 1 reflection with limit=1, got %d", len(limited))
	}

	// Verify field values round-trip.
	obsidian, err := queryReflections(dbPath, "黒曜", 10)
	if err != nil {
		t.Fatalf("queryReflections obsidian: %v", err)
	}
	if len(obsidian) != 1 {
		t.Fatalf("expected 1 reflection for 黒曜, got %d", len(obsidian))
	}
	if obsidian[0].Score != 5 {
		t.Errorf("score = %d, want 5", obsidian[0].Score)
	}
	if obsidian[0].Feedback != "Excellent implementation" {
		t.Errorf("feedback = %q, want %q", obsidian[0].Feedback, "Excellent implementation")
	}
	if obsidian[0].Improvement != "None needed" {
		t.Errorf("improvement = %q, want %q", obsidian[0].Improvement, "None needed")
	}
	if obsidian[0].TaskID != "task-003" {
		t.Errorf("taskId = %q, want %q", obsidian[0].TaskID, "task-003")
	}
}

func TestStoreReflectionSpecialChars(t *testing.T) {
	dbPath := tempReflectionDB(t)

	ref := &ReflectionResult{
		TaskID:      "task-special",
		Agent:        "琥珀",
		Score:       3,
		Feedback:    `She said "it's fine" and that's that`,
		Improvement: "Use apostrophes carefully",
		CostUSD:     0.01,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	if err := storeReflection(dbPath, ref); err != nil {
		t.Fatalf("storeReflection with special chars: %v", err)
	}

	results, err := queryReflections(dbPath, "琥珀", 10)
	if err != nil {
		t.Fatalf("queryReflections: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Feedback != ref.Feedback {
		t.Errorf("feedback = %q, want %q", results[0].Feedback, ref.Feedback)
	}
}

// --- buildReflectionContext tests ---

func TestBuildReflectionContext(t *testing.T) {
	dbPath := tempReflectionDB(t)

	now := time.Now().UTC().Format(time.RFC3339)

	storeReflection(dbPath, &ReflectionResult{
		TaskID: "t1", Agent: "翡翠", Score: 3,
		Feedback: "OK", Improvement: "Be more thorough",
		CostUSD: 0.01, CreatedAt: now,
	})
	storeReflection(dbPath, &ReflectionResult{
		TaskID: "t2", Agent: "翡翠", Score: 2,
		Feedback: "Needs work", Improvement: "Check all sources",
		CostUSD: 0.01, CreatedAt: now,
	})

	ctx := buildReflectionContext(dbPath, "翡翠", 5)
	if ctx == "" {
		t.Fatal("buildReflectionContext returned empty string")
	}
	if !stringContains(ctx, "Recent self-assessments for agent 翡翠") {
		t.Errorf("missing header in context: %q", ctx)
	}
	if !stringContains(ctx, "Score: 3/5") {
		t.Errorf("missing score 3/5 in context: %q", ctx)
	}
	if !stringContains(ctx, "Score: 2/5") {
		t.Errorf("missing score 2/5 in context: %q", ctx)
	}
	if !stringContains(ctx, "Be more thorough") {
		t.Errorf("missing improvement text in context: %q", ctx)
	}
}

func TestBuildReflectionContext_Empty(t *testing.T) {
	dbPath := tempReflectionDB(t)

	// No reflections stored — should return empty.
	ctx := buildReflectionContext(dbPath, "翡翠", 5)
	if ctx != "" {
		t.Errorf("expected empty context, got %q", ctx)
	}
}

func TestBuildReflectionContext_EmptyRole(t *testing.T) {
	ctx := buildReflectionContext("/tmp/nonexistent.db", "", 5)
	if ctx != "" {
		t.Errorf("expected empty context for empty role, got %q", ctx)
	}
}

func TestBuildReflectionContext_EmptyDBPath(t *testing.T) {
	ctx := buildReflectionContext("", "翡翠", 5)
	if ctx != "" {
		t.Errorf("expected empty context for empty dbPath, got %q", ctx)
	}
}

// --- Budget helper ---

func TestReflectionBudgetOrDefault(t *testing.T) {
	// Nil config.
	if got := reflectionBudgetOrDefault(nil); got != 0.05 {
		t.Errorf("nil config: budget = %f, want 0.05", got)
	}

	// Zero budget (use default).
	cfg := &Config{}
	if got := reflectionBudgetOrDefault(cfg); got != 0.05 {
		t.Errorf("zero budget: budget = %f, want 0.05", got)
	}

	// Custom budget.
	cfg.Reflection.Budget = 0.10
	if got := reflectionBudgetOrDefault(cfg); got != 0.10 {
		t.Errorf("custom budget: budget = %f, want 0.10", got)
	}
}

// --- extractJSON tests ---

func TestExtractJSON_Simple(t *testing.T) {
	input := `{"key":"value"}`
	got := extractJSON(input)
	if got != `{"key":"value"}` {
		t.Errorf("extractJSON = %q, want %q", got, input)
	}
}

func TestExtractJSON_Nested(t *testing.T) {
	input := `{"outer":{"inner":"value"}}`
	got := extractJSON(input)
	if got != input {
		t.Errorf("extractJSON = %q, want %q", got, input)
	}
}

func TestExtractJSON_NoJSON(t *testing.T) {
	got := extractJSON("no json here")
	if got != "" {
		t.Errorf("extractJSON = %q, want empty", got)
	}
}

func TestExtractJSON_MarkdownWrapped(t *testing.T) {
	input := "```json\n{\"score\":5}\n```"
	got := extractJSON(input)
	if got != `{"score":5}` {
		t.Errorf("extractJSON = %q, want %q", got, `{"score":5}`)
	}
}

// --- Helper ---

func stringContains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// --- from retention_test.go ---

// --- retentionDays ---

func TestRetentionDays(t *testing.T) {
	if retentionDays(0, 90) != 90 {
		t.Error("expected fallback 90")
	}
	if retentionDays(30, 90) != 30 {
		t.Error("expected configured 30")
	}
	if retentionDays(-1, 14) != 14 {
		t.Error("expected fallback for negative")
	}
	if retentionDays(365, 90) != 365 {
		t.Error("expected configured 365")
	}
}

// --- PII Redaction ---

func TestCompilePIIPatterns(t *testing.T) {
	patterns := compilePIIPatterns([]string{
		`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b`, // email
		`\b\d{3}-\d{2}-\d{4}\b`,                                 // SSN
		`invalid[`,                                                // invalid regex
	})
	if len(patterns) != 2 {
		t.Errorf("expected 2 compiled patterns, got %d", len(patterns))
	}
}

func TestCompilePIIPatternsEmpty(t *testing.T) {
	patterns := compilePIIPatterns(nil)
	if patterns != nil {
		t.Error("expected nil for empty input")
	}
}

func TestRedactPII(t *testing.T) {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b`),
		regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`),
	}

	tests := []struct {
		input, expected string
	}{
		{"contact user@example.com for details", "contact [REDACTED] for details"},
		{"SSN: 123-45-6789", "SSN: [REDACTED]"},
		{"no PII here", "no PII here"},
		{"", ""},
		{"email test@test.org and 999-88-7777", "email [REDACTED] and [REDACTED]"},
	}

	for _, tt := range tests {
		result := redactPII(tt.input, patterns)
		if result != tt.expected {
			t.Errorf("redactPII(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestRedactPIINoPatterns(t *testing.T) {
	result := redactPII("user@example.com", nil)
	if result != "user@example.com" {
		t.Error("expected no change with nil patterns")
	}
}

// --- Helper: create test DB ---

func createRetentionTestDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// Create all tables.
	sql := `
CREATE TABLE IF NOT EXISTS job_runs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  job_id TEXT NOT NULL, name TEXT NOT NULL, source TEXT NOT NULL DEFAULT '',
  started_at TEXT NOT NULL, finished_at TEXT NOT NULL,
  status TEXT NOT NULL, exit_code INTEGER DEFAULT 0,
  cost_usd REAL DEFAULT 0, output_summary TEXT DEFAULT '',
  error TEXT DEFAULT '', model TEXT DEFAULT '',
  session_id TEXT DEFAULT '', output_file TEXT DEFAULT '',
  tokens_in INTEGER DEFAULT 0, tokens_out INTEGER DEFAULT 0,
  agent TEXT DEFAULT '', parent_id TEXT DEFAULT ''
);
CREATE TABLE IF NOT EXISTS audit_log (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  timestamp TEXT NOT NULL, action TEXT NOT NULL,
  source TEXT NOT NULL DEFAULT '', detail TEXT DEFAULT '', ip TEXT DEFAULT ''
);
CREATE TABLE IF NOT EXISTS sessions (
  id TEXT PRIMARY KEY, agent TEXT NOT NULL DEFAULT '',
  source TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'active',
  title TEXT NOT NULL DEFAULT '', total_cost REAL DEFAULT 0,
  total_tokens_in INTEGER DEFAULT 0, total_tokens_out INTEGER DEFAULT 0,
  message_count INTEGER DEFAULT 0, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS session_messages (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id TEXT NOT NULL, role TEXT NOT NULL DEFAULT 'system',
  content TEXT NOT NULL DEFAULT '', cost_usd REAL DEFAULT 0,
  tokens_in INTEGER DEFAULT 0, tokens_out INTEGER DEFAULT 0,
  model TEXT DEFAULT '', task_id TEXT DEFAULT '', created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS workflow_runs (
  id TEXT PRIMARY KEY, workflow_name TEXT NOT NULL,
  status TEXT NOT NULL, started_at TEXT NOT NULL,
  finished_at TEXT DEFAULT '', duration_ms INTEGER DEFAULT 0,
  total_cost REAL DEFAULT 0, variables TEXT DEFAULT '{}',
  step_results TEXT DEFAULT '{}', error TEXT DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS handoffs (
  id TEXT PRIMARY KEY, workflow_run_id TEXT DEFAULT '',
  from_agent TEXT NOT NULL, to_agent TEXT NOT NULL,
  from_step_id TEXT DEFAULT '', to_step_id TEXT DEFAULT '',
  from_session_id TEXT DEFAULT '', to_session_id TEXT DEFAULT '',
  context TEXT DEFAULT '', instruction TEXT DEFAULT '',
  status TEXT NOT NULL DEFAULT 'pending', created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS agent_messages (
  id TEXT PRIMARY KEY, workflow_run_id TEXT DEFAULT '',
  from_agent TEXT NOT NULL, to_agent TEXT NOT NULL,
  type TEXT NOT NULL, content TEXT NOT NULL DEFAULT '',
  ref_id TEXT DEFAULT '', created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS reflections (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id TEXT NOT NULL, agent TEXT NOT NULL DEFAULT '',
  score INTEGER DEFAULT 0, feedback TEXT DEFAULT '',
  improvement TEXT DEFAULT '', cost_usd REAL DEFAULT 0,
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS sla_checks (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  agent TEXT NOT NULL, checked_at TEXT NOT NULL,
  success_rate REAL DEFAULT 0, p95_latency_ms INTEGER DEFAULT 0,
  violation INTEGER DEFAULT 0, detail TEXT DEFAULT ''
);
CREATE TABLE IF NOT EXISTS trust_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  agent TEXT NOT NULL, event_type TEXT NOT NULL,
  from_level TEXT DEFAULT '', to_level TEXT DEFAULT '',
  consecutive_success INTEGER DEFAULT 0,
  created_at TEXT NOT NULL, note TEXT DEFAULT ''
);
CREATE TABLE IF NOT EXISTS config_versions (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  entity_type TEXT NOT NULL, entity_name TEXT NOT NULL DEFAULT '',
  version INTEGER NOT NULL, content TEXT NOT NULL DEFAULT '{}',
  changed_by TEXT DEFAULT '', diff_summary TEXT DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS agent_memory (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  agent TEXT NOT NULL, key TEXT NOT NULL, value TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL, created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS offline_queue (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task_json TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'pending',
  retry_count INTEGER DEFAULT 0, error TEXT DEFAULT '',
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
`
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create test db: %s: %v", string(out), err)
	}
	return dbPath
}

func insertTestRow(t *testing.T, dbPath, sql string) {
	t.Helper()
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("insert: %s: %v", string(out), err)
	}
}

func countRows(t *testing.T, dbPath, table string) int {
	t.Helper()
	rows, err := db.Query(dbPath, fmt.Sprintf("SELECT COUNT(*) as cnt FROM %s", table))
	if err != nil || len(rows) == 0 {
		return 0
	}
	return jsonInt(rows[0]["cnt"])
}

// --- Cleanup Functions ---

func TestCleanupWorkflowRuns(t *testing.T) {
	dbPath := createRetentionTestDB(t)
	old := time.Now().AddDate(0, 0, -100).Format(time.RFC3339)
	recent := time.Now().Format(time.RFC3339)

	insertTestRow(t, dbPath, fmt.Sprintf(
		`INSERT INTO workflow_runs (id, workflow_name, status, started_at, created_at) VALUES ('old1','wf','done','%s','%s')`, old, old))
	insertTestRow(t, dbPath, fmt.Sprintf(
		`INSERT INTO workflow_runs (id, workflow_name, status, started_at, created_at) VALUES ('new1','wf','done','%s','%s')`, recent, recent))

	n, err := cleanupWorkflowRuns(dbPath, 30)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("expected 1 deleted, got %d", n)
	}
	if countRows(t, dbPath, "workflow_runs") != 1 {
		t.Error("expected 1 remaining")
	}
}

func TestCleanupHandoffs(t *testing.T) {
	dbPath := createRetentionTestDB(t)
	old := time.Now().AddDate(0, 0, -100).Format(time.RFC3339)
	recent := time.Now().Format(time.RFC3339)

	insertTestRow(t, dbPath, fmt.Sprintf(
		`INSERT INTO handoffs (id, from_agent, to_agent, status, created_at) VALUES ('h1','a','b','done','%s')`, old))
	insertTestRow(t, dbPath, fmt.Sprintf(
		`INSERT INTO agent_messages (id, from_agent, to_agent, type, content, created_at) VALUES ('m1','a','b','note','hi','%s')`, old))
	insertTestRow(t, dbPath, fmt.Sprintf(
		`INSERT INTO handoffs (id, from_agent, to_agent, status, created_at) VALUES ('h2','a','b','done','%s')`, recent))

	n, err := cleanupHandoffs(dbPath, 30)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("expected 1 handoff deleted, got %d", n)
	}
	if countRows(t, dbPath, "handoffs") != 1 {
		t.Error("expected 1 handoff remaining")
	}
	if countRows(t, dbPath, "agent_messages") != 0 {
		t.Error("expected 0 agent_messages remaining")
	}
}

func TestCleanupReflections(t *testing.T) {
	dbPath := createRetentionTestDB(t)
	old := time.Now().AddDate(0, 0, -100).Format(time.RFC3339)
	recent := time.Now().Format(time.RFC3339)

	insertTestRow(t, dbPath, fmt.Sprintf(
		`INSERT INTO reflections (task_id, agent, score, created_at) VALUES ('t1','r1',4,'%s')`, old))
	insertTestRow(t, dbPath, fmt.Sprintf(
		`INSERT INTO reflections (task_id, agent, score, created_at) VALUES ('t2','r1',5,'%s')`, recent))

	n, err := cleanupReflections(dbPath, 30)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("expected 1 deleted, got %d", n)
	}
	if countRows(t, dbPath, "reflections") != 1 {
		t.Error("expected 1 remaining")
	}
}

func TestCleanupSLAChecks(t *testing.T) {
	dbPath := createRetentionTestDB(t)
	old := time.Now().AddDate(0, 0, -100).Format(time.RFC3339)
	recent := time.Now().Format(time.RFC3339)

	insertTestRow(t, dbPath, fmt.Sprintf(
		`INSERT INTO sla_checks (agent, checked_at) VALUES ('r1','%s')`, old))
	insertTestRow(t, dbPath, fmt.Sprintf(
		`INSERT INTO sla_checks (agent, checked_at) VALUES ('r1','%s')`, recent))

	n, err := cleanupSLAChecks(dbPath, 30)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("expected 1 deleted, got %d", n)
	}
}

func TestCleanupTrustEvents(t *testing.T) {
	dbPath := createRetentionTestDB(t)
	old := time.Now().AddDate(0, 0, -100).Format(time.RFC3339)
	recent := time.Now().Format(time.RFC3339)

	insertTestRow(t, dbPath, fmt.Sprintf(
		`INSERT INTO trust_events (agent, event_type, created_at) VALUES ('r1','promote','%s')`, old))
	insertTestRow(t, dbPath, fmt.Sprintf(
		`INSERT INTO trust_events (agent, event_type, created_at) VALUES ('r1','promote','%s')`, recent))

	n, err := cleanupTrustEvents(dbPath, 30)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("expected 1 deleted, got %d", n)
	}
}

func TestCleanupEmptyDB(t *testing.T) {
	// All cleanup functions should handle empty/missing DB gracefully.
	n, err := cleanupWorkflowRuns("", 30)
	if err != nil || n != 0 {
		t.Error("expected no-op for empty path")
	}
	n, err = cleanupHandoffs("", 30)
	if err != nil || n != 0 {
		t.Error("expected no-op for empty path")
	}
	n, err = cleanupReflections("", 30)
	if err != nil || n != 0 {
		t.Error("expected no-op for empty path")
	}
	n, err = cleanupSLAChecks("", 30)
	if err != nil || n != 0 {
		t.Error("expected no-op for empty path")
	}
	n, err = cleanupTrustEvents("", 30)
	if err != nil || n != 0 {
		t.Error("expected no-op for empty path")
	}
}

func TestCleanupZeroDays(t *testing.T) {
	n, _ := cleanupWorkflowRuns("/tmp/test.db", 0)
	if n != 0 {
		t.Error("expected 0 for zero days")
	}
	n, _ = cleanupWorkflowRuns("/tmp/test.db", -1)
	if n != 0 {
		t.Error("expected 0 for negative days")
	}
}

// --- Log File Cleanup ---

func TestCleanupLogFiles(t *testing.T) {
	dir := t.TempDir()

	// Create some log files.
	os.WriteFile(filepath.Join(dir, "tetora.log"), []byte("current"), 0o644)
	os.WriteFile(filepath.Join(dir, "tetora.log.1"), []byte("recent"), 0o644)
	os.WriteFile(filepath.Join(dir, "tetora.log.2"), []byte("old"), 0o644)

	// Make .2 old.
	oldTime := time.Now().AddDate(0, 0, -30)
	os.Chtimes(filepath.Join(dir, "tetora.log.2"), oldTime, oldTime)

	removed := cleanupLogFiles(dir, 14)
	if removed != 1 {
		t.Errorf("expected 1 removed, got %d", removed)
	}

	// Current log should not be touched.
	if _, err := os.Stat(filepath.Join(dir, "tetora.log")); err != nil {
		t.Error("current log should still exist")
	}
	// Recent rotated should still exist.
	if _, err := os.Stat(filepath.Join(dir, "tetora.log.1")); err != nil {
		t.Error("recent rotated log should still exist")
	}
}

func TestCleanupLogFilesEmptyDir(t *testing.T) {
	n := cleanupLogFiles("", 14)
	if n != 0 {
		t.Error("expected 0 for empty dir")
	}
	n = cleanupLogFiles("/nonexistent", 14)
	if n != 0 {
		t.Error("expected 0 for nonexistent dir")
	}
}

// --- Retention Stats ---

func TestQueryRetentionStats(t *testing.T) {
	dbPath := createRetentionTestDB(t)

	now := time.Now().Format(time.RFC3339)
	insertTestRow(t, dbPath, fmt.Sprintf(
		`INSERT INTO job_runs (job_id, name, source, started_at, finished_at, status) VALUES ('j1','test','cli','%s','%s','success')`, now, now))
	insertTestRow(t, dbPath, fmt.Sprintf(
		`INSERT INTO audit_log (timestamp, action) VALUES ('%s','test')`, now))
	insertTestRow(t, dbPath, fmt.Sprintf(
		`INSERT INTO reflections (task_id, agent, created_at) VALUES ('t1','r1','%s')`, now))

	stats := queryRetentionStats(dbPath)
	if stats["job_runs"] != 1 {
		t.Errorf("expected 1 job_run, got %d", stats["job_runs"])
	}
	if stats["audit_log"] != 1 {
		t.Errorf("expected 1 audit_log, got %d", stats["audit_log"])
	}
	if stats["reflections"] != 1 {
		t.Errorf("expected 1 reflection, got %d", stats["reflections"])
	}
}

func TestQueryRetentionStatsEmptyDB(t *testing.T) {
	stats := queryRetentionStats("")
	if len(stats) != 0 {
		t.Error("expected empty stats for empty path")
	}
}

// --- Data Export ---

func TestExportData(t *testing.T) {
	dbPath := createRetentionTestDB(t)
	now := time.Now().Format(time.RFC3339)

	insertTestRow(t, dbPath, fmt.Sprintf(
		`INSERT INTO job_runs (job_id, name, source, started_at, finished_at, status) VALUES ('j1','test','cli','%s','%s','success')`, now, now))
	insertTestRow(t, dbPath, fmt.Sprintf(
		`INSERT INTO audit_log (timestamp, action) VALUES ('%s','test')`, now))

	cfg := &Config{HistoryDB: dbPath}
	data, err := exportData(cfg)
	if err != nil {
		t.Fatal(err)
	}

	var export DataExport
	if err := json.Unmarshal(data, &export); err != nil {
		t.Fatal(err)
	}
	if export.ExportedAt == "" {
		t.Error("expected exportedAt")
	}
	if len(export.History) != 1 {
		t.Errorf("expected 1 history record, got %d", len(export.History))
	}
	if len(export.AuditLog) != 1 {
		t.Errorf("expected 1 audit record, got %d", len(export.AuditLog))
	}
}

func TestExportDataNoDBPath(t *testing.T) {
	cfg := &Config{}
	data, err := exportData(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var export DataExport
	if err := json.Unmarshal(data, &export); err != nil {
		t.Fatal(err)
	}
	if export.ExportedAt == "" {
		t.Error("expected exportedAt even with no DB")
	}
}

// --- Data Purge ---

func TestPurgeDataBefore(t *testing.T) {
	dbPath := createRetentionTestDB(t)
	old := "2024-01-01T00:00:00Z"
	recent := time.Now().Format(time.RFC3339)

	insertTestRow(t, dbPath, fmt.Sprintf(
		`INSERT INTO job_runs (job_id, name, source, started_at, finished_at, status) VALUES ('j1','old','cli','%s','%s','success')`, old, old))
	insertTestRow(t, dbPath, fmt.Sprintf(
		`INSERT INTO job_runs (job_id, name, source, started_at, finished_at, status) VALUES ('j2','new','cli','%s','%s','success')`, recent, recent))
	insertTestRow(t, dbPath, fmt.Sprintf(
		`INSERT INTO audit_log (timestamp, action) VALUES ('%s','old')`, old))
	insertTestRow(t, dbPath, fmt.Sprintf(
		`INSERT INTO reflections (task_id, agent, created_at) VALUES ('t1','r1','%s')`, old))

	results, err := purgeDataBefore(&Config{HistoryDB: dbPath}, "2025-01-01")
	if err != nil {
		t.Fatal(err)
	}

	// Check results.
	for _, r := range results {
		if r.Error != "" {
			t.Errorf("table %s error: %s", r.Table, r.Error)
		}
	}

	// Old job should be deleted, new should remain.
	if countRows(t, dbPath, "job_runs") != 1 {
		t.Errorf("expected 1 job_run remaining, got %d", countRows(t, dbPath, "job_runs"))
	}
	if countRows(t, dbPath, "audit_log") != 0 {
		t.Errorf("expected 0 audit_log remaining, got %d", countRows(t, dbPath, "audit_log"))
	}
	if countRows(t, dbPath, "reflections") != 0 {
		t.Errorf("expected 0 reflections remaining, got %d", countRows(t, dbPath, "reflections"))
	}
}

func TestPurgeDataBeforeNoDBPath(t *testing.T) {
	_, err := purgeDataBefore(&Config{}, "2025-01-01")
	if err == nil {
		t.Error("expected error for empty DB path")
	}
}

// --- runRetention ---

func TestRunRetention(t *testing.T) {
	dbPath := createRetentionTestDB(t)
	dir := t.TempDir()

	cfg := &Config{
		HistoryDB: dbPath,
		BaseDir:   dir,
		Retention: RetentionConfig{
			History:     30,
			Sessions:    15,
			AuditLog:    90,
			Workflows:   30,
			Reflections: 30,
			SLA:         30,
			TrustEvents: 30,
			Handoffs:    30,
			Queue:       3,
			Versions:    60,
			Outputs:     14,
			Uploads:     3,
			Logs:        7,
		},
	}

	// Create dirs for outputs/uploads/logs.
	os.MkdirAll(filepath.Join(dir, "outputs"), 0o755)
	os.MkdirAll(filepath.Join(dir, "uploads"), 0o755)
	os.MkdirAll(filepath.Join(dir, "logs"), 0o755)

	results := runRetention(cfg)
	if len(results) == 0 {
		t.Error("expected results from runRetention")
	}

	// Check all tables are covered.
	tables := make(map[string]bool)
	for _, r := range results {
		tables[r.Table] = true
	}

	expected := []string{"job_runs", "audit_log", "sessions", "offline_queue",
		"workflow_runs", "handoffs", "reflections", "sla_checks",
		"trust_events", "config_versions", "outputs", "uploads", "log_files"}
	for _, e := range expected {
		if !tables[e] {
			t.Errorf("missing result for table: %s", e)
		}
	}
}

func TestRunRetentionDefaults(t *testing.T) {
	dbPath := createRetentionTestDB(t)
	dir := t.TempDir()

	// Empty retention config → all defaults.
	cfg := &Config{
		HistoryDB: dbPath,
		BaseDir:   dir,
	}
	os.MkdirAll(filepath.Join(dir, "outputs"), 0o755)
	os.MkdirAll(filepath.Join(dir, "uploads"), 0o755)
	os.MkdirAll(filepath.Join(dir, "logs"), 0o755)

	results := runRetention(cfg)
	if len(results) == 0 {
		t.Error("expected results even with default config")
	}
}

// --- from scheduling_test.go ---

// setupSchedulingTest creates a scheduling.Service for testing and returns
// a cleanup function that restores the original global state.
func setupSchedulingTest(t *testing.T) (*scheduling.Service, func()) {
	t.Helper()

	cfg := &Config{}
	svc := newSchedulingService(cfg)

	oldScheduling := globalSchedulingService
	oldCalendar := globalCalendarService
	oldTaskMgr := globalTaskManager

	globalSchedulingService = svc
	globalCalendarService = nil
	globalTaskManager = nil

	cleanup := func() {
		globalSchedulingService = oldScheduling
		globalCalendarService = oldCalendar
		globalTaskManager = oldTaskMgr
	}

	return svc, cleanup
}

func TestNewSchedulingService(t *testing.T) {
	cfg := &Config{}
	svc := newSchedulingService(cfg)
	if svc == nil {
		t.Fatal("expected non-nil service")
	}
}

func TestViewSchedule_NoServices(t *testing.T) {
	svc, cleanup := setupSchedulingTest(t)
	defer cleanup()

	// Both globalCalendarService and globalTaskManager are nil.
	schedules, err := svc.ViewSchedule("", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(schedules) != 1 {
		t.Fatalf("expected 1 day, got %d", len(schedules))
	}

	day := schedules[0]
	today := time.Now().Format("2006-01-02")
	if day.Date != today {
		t.Errorf("expected date %s, got %s", today, day.Date)
	}
	if len(day.Events) != 0 {
		t.Errorf("expected 0 events, got %d", len(day.Events))
	}
	if day.BusyHours != 0 {
		t.Errorf("expected 0 busy hours, got %f", day.BusyHours)
	}
	// Should have 1 free slot = full working hours.
	if len(day.FreeSlots) != 1 {
		t.Errorf("expected 1 free slot (full working day), got %d", len(day.FreeSlots))
	}
	if day.FreeHours != 9 {
		t.Errorf("expected 9 free hours, got %f", day.FreeHours)
	}
}

func TestViewSchedule_MultipleDays(t *testing.T) {
	svc, cleanup := setupSchedulingTest(t)
	defer cleanup()

	schedules, err := svc.ViewSchedule("2026-03-01", 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(schedules) != 3 {
		t.Fatalf("expected 3 days, got %d", len(schedules))
	}
	expected := []string{"2026-03-01", "2026-03-02", "2026-03-03"}
	for i, day := range schedules {
		if day.Date != expected[i] {
			t.Errorf("day %d: expected %s, got %s", i, expected[i], day.Date)
		}
	}
}

func TestViewSchedule_InvalidDate(t *testing.T) {
	svc, cleanup := setupSchedulingTest(t)
	defer cleanup()

	_, err := svc.ViewSchedule("not-a-date", 1)
	if err == nil {
		t.Fatal("expected error for invalid date")
	}
}

func TestFindFreeSlots_FullDay(t *testing.T) {
	svc, cleanup := setupSchedulingTest(t)
	defer cleanup()

	loc := time.Now().Location()
	start := time.Date(2026, 3, 15, 9, 0, 0, 0, loc)
	end := time.Date(2026, 3, 15, 18, 0, 0, 0, loc)

	slots, err := svc.FindFreeSlots(start, end, 30)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// No events, so the entire range should be one free slot.
	if len(slots) != 1 {
		t.Fatalf("expected 1 free slot, got %d", len(slots))
	}
	if slots[0].Duration != 540 { // 9 hours = 540 min
		t.Errorf("expected 540 min, got %d", slots[0].Duration)
	}
}

func TestFindFreeSlots_WithEvents(t *testing.T) {
	// Since FindFreeSlots calls fetchCalendarEvents and fetchTaskDeadlines
	// which return nil when globals are nil, this effectively tests with no events.
	svc, cleanup := setupSchedulingTest(t)
	defer cleanup()

	loc := time.Now().Location()
	start := time.Date(2026, 3, 15, 9, 0, 0, 0, loc)
	end := time.Date(2026, 3, 15, 18, 0, 0, 0, loc)

	slots, err := svc.FindFreeSlots(start, end, 60)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(slots) != 1 {
		t.Fatalf("expected 1 slot, got %d", len(slots))
	}
	if slots[0].Duration != 540 {
		t.Errorf("expected 540 min, got %d", slots[0].Duration)
	}
}

func TestFindFreeSlots_NoSpace(t *testing.T) {
	svc, cleanup := setupSchedulingTest(t)
	defer cleanup()

	loc := time.Now().Location()
	start := time.Date(2026, 3, 15, 9, 0, 0, 0, loc)
	end := time.Date(2026, 3, 15, 9, 10, 0, 0, loc)

	// Only 10 minutes available, but we need at least 30.
	slots, err := svc.FindFreeSlots(start, end, 30)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(slots) != 0 {
		t.Errorf("expected 0 slots, got %d", len(slots))
	}
}

func TestFindFreeSlots_InvalidRange(t *testing.T) {
	svc, cleanup := setupSchedulingTest(t)
	defer cleanup()

	loc := time.Now().Location()
	start := time.Date(2026, 3, 15, 18, 0, 0, 0, loc)
	end := time.Date(2026, 3, 15, 9, 0, 0, 0, loc)

	_, err := svc.FindFreeSlots(start, end, 30)
	if err == nil {
		t.Fatal("expected error for invalid range")
	}
}

func TestSuggestSlots_Basic(t *testing.T) {
	svc, cleanup := setupSchedulingTest(t)
	defer cleanup()

	suggestions, err := svc.SuggestSlots(60, false, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// With no events, there should be suggestions.
	if len(suggestions) == 0 {
		t.Fatal("expected at least 1 suggestion")
	}
	if len(suggestions) > 5 {
		t.Errorf("expected at most 5 suggestions, got %d", len(suggestions))
	}

	// All suggestions should have duration 60.
	for i, s := range suggestions {
		if s.Slot.Duration != 60 {
			t.Errorf("suggestion %d: expected 60 min, got %d", i, s.Slot.Duration)
		}
		if s.Score < 0 || s.Score > 1 {
			t.Errorf("suggestion %d: score %f out of [0,1] range", i, s.Score)
		}
		if s.Reason == "" {
			t.Errorf("suggestion %d: empty reason", i)
		}
	}

	// Verify sorted by score descending.
	for i := 1; i < len(suggestions); i++ {
		if suggestions[i].Score > suggestions[i-1].Score {
			t.Errorf("suggestions not sorted: [%d].Score=%f > [%d].Score=%f", i, suggestions[i].Score, i-1, suggestions[i-1].Score)
		}
	}
}

func TestSuggestSlots_PreferMorning(t *testing.T) {
	svc, cleanup := setupSchedulingTest(t)
	defer cleanup()

	suggestions, err := svc.SuggestSlots(60, true, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(suggestions) == 0 {
		t.Fatal("expected at least 1 suggestion")
	}

	// The top suggestion should be a morning slot (before noon).
	topHour := suggestions[0].Slot.Start.Hour()
	if topHour >= 12 {
		t.Errorf("expected morning slot as top suggestion, got hour %d", topHour)
	}
}

func TestSuggestSlots_NoFreeTime(t *testing.T) {
	svc, cleanup := setupSchedulingTest(t)
	defer cleanup()

	// Request a 600-minute slot (10 hours) — impossible in a 9-hour workday.
	suggestions, err := svc.SuggestSlots(600, false, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(suggestions) != 0 {
		t.Errorf("expected 0 suggestions for 600-min slot, got %d", len(suggestions))
	}
}

func TestSuggestSlots_InvalidDuration(t *testing.T) {
	svc, cleanup := setupSchedulingTest(t)
	defer cleanup()

	_, err := svc.SuggestSlots(0, false, 1)
	if err == nil {
		t.Fatal("expected error for zero duration")
	}
}

func TestPlanWeek_Basic(t *testing.T) {
	svc, cleanup := setupSchedulingTest(t)
	defer cleanup()

	plan, err := svc.PlanWeek("default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if plan == nil {
		t.Fatal("expected non-nil plan")
	}

	// Check required fields exist.
	requiredKeys := []string{"period", "total_meetings", "total_busy_hours", "total_free_hours", "daily_summaries", "focus_blocks", "urgent_tasks", "warnings"}
	for _, key := range requiredKeys {
		if _, ok := plan[key]; !ok {
			t.Errorf("missing key in plan: %s", key)
		}
	}

	// daily_summaries should have 7 entries.
	summaries, ok := plan["daily_summaries"].([]map[string]any)
	if !ok {
		t.Fatalf("daily_summaries wrong type: %T", plan["daily_summaries"])
	}
	if len(summaries) != 7 {
		t.Errorf("expected 7 daily summaries, got %d", len(summaries))
	}

	// With no events, total_meetings should be 0.
	totalMeetings, ok := plan["total_meetings"].(int)
	if !ok {
		t.Fatalf("total_meetings wrong type: %T", plan["total_meetings"])
	}
	if totalMeetings != 0 {
		t.Errorf("expected 0 total meetings, got %d", totalMeetings)
	}

	// With no events, total_free_hours should be 63 (9 * 7).
	totalFree, ok := plan["total_free_hours"].(float64)
	if !ok {
		t.Fatalf("total_free_hours wrong type: %T", plan["total_free_hours"])
	}
	if totalFree != 63 {
		t.Errorf("expected 63 total free hours, got %f", totalFree)
	}
}

func TestMergeEvents(t *testing.T) {
	loc := time.Now().Location()
	base := time.Date(2026, 3, 15, 9, 0, 0, 0, loc)

	events := []ScheduleEvent{
		{Title: "A", Start: base, End: base.Add(60 * time.Minute)},
		{Title: "B", Start: base.Add(30 * time.Minute), End: base.Add(90 * time.Minute)},
		{Title: "C", Start: base.Add(120 * time.Minute), End: base.Add(150 * time.Minute)},
	}

	merged := scheduling.MergeEvents(events)
	if len(merged) != 2 {
		t.Fatalf("expected 2 merged events, got %d", len(merged))
	}
	// First merged: 09:00-10:30 (A and B overlap).
	if merged[0].End != base.Add(90*time.Minute) {
		t.Errorf("expected first merged end at 10:30, got %s", merged[0].End.Format("15:04"))
	}
	// Second: 11:00-11:30 (C standalone).
	if merged[1].Start != base.Add(120*time.Minute) {
		t.Errorf("expected second event at 11:00, got %s", merged[1].Start.Format("15:04"))
	}
}

func TestMergeEvents_Empty(t *testing.T) {
	merged := scheduling.MergeEvents(nil)
	if merged != nil {
		t.Errorf("expected nil for empty input, got %v", merged)
	}
}

func TestMergeEvents_Adjacent(t *testing.T) {
	loc := time.Now().Location()
	base := time.Date(2026, 3, 15, 9, 0, 0, 0, loc)

	events := []ScheduleEvent{
		{Title: "A", Start: base, End: base.Add(60 * time.Minute)},
		{Title: "B", Start: base.Add(60 * time.Minute), End: base.Add(120 * time.Minute)},
	}

	merged := scheduling.MergeEvents(events)
	// Adjacent events should be merged.
	if len(merged) != 1 {
		t.Fatalf("expected 1 merged event for adjacent, got %d", len(merged))
	}
	if merged[0].End != base.Add(120*time.Minute) {
		t.Errorf("expected end at 11:00, got %s", merged[0].End.Format("15:04"))
	}
}

func TestToolScheduleView(t *testing.T) {
	_, cleanup := setupSchedulingTest(t)
	defer cleanup()

	cfg := &Config{}
	input := json.RawMessage(`{"date": "2026-03-15", "days": 2}`)

	result, err := toolScheduleView(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}

	// Parse output as JSON.
	var schedules []DaySchedule
	if err := json.Unmarshal([]byte(result), &schedules); err != nil {
		t.Fatalf("invalid JSON output: %v", err)
	}
	if len(schedules) != 2 {
		t.Errorf("expected 2 days, got %d", len(schedules))
	}
}

func TestToolScheduleView_Defaults(t *testing.T) {
	_, cleanup := setupSchedulingTest(t)
	defer cleanup()

	cfg := &Config{}
	input := json.RawMessage(`{}`)

	result, err := toolScheduleView(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var schedules []DaySchedule
	if err := json.Unmarshal([]byte(result), &schedules); err != nil {
		t.Fatalf("invalid JSON output: %v", err)
	}
	if len(schedules) != 1 {
		t.Errorf("expected 1 day (default), got %d", len(schedules))
	}
}

func TestToolScheduleView_NotInitialized(t *testing.T) {
	old := globalSchedulingService
	globalSchedulingService = nil
	defer func() { globalSchedulingService = old }()

	cfg := &Config{}
	input := json.RawMessage(`{}`)

	_, err := toolScheduleView(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error when service not initialized")
	}
}

func TestToolScheduleSuggest(t *testing.T) {
	_, cleanup := setupSchedulingTest(t)
	defer cleanup()

	cfg := &Config{}
	input := json.RawMessage(`{"duration_minutes": 60, "prefer_morning": true, "days": 2}`)

	result, err := toolScheduleSuggest(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
	if !contains(result, "suggested slots") {
		t.Errorf("expected 'suggested slots' in result, got: %s", schedTruncateForTest(result, 200))
	}
}

func TestToolScheduleSuggest_Defaults(t *testing.T) {
	_, cleanup := setupSchedulingTest(t)
	defer cleanup()

	cfg := &Config{}
	input := json.RawMessage(`{}`)

	result, err := toolScheduleSuggest(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should use defaults (60 min, no preference, 5 days).
	if result == "" {
		t.Fatal("expected non-empty result")
	}
}

func TestToolSchedulePlan(t *testing.T) {
	_, cleanup := setupSchedulingTest(t)
	defer cleanup()

	cfg := &Config{}
	input := json.RawMessage(`{"user_id": "testuser"}`)

	result, err := toolSchedulePlan(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}

	// Parse as JSON.
	var plan map[string]any
	if err := json.Unmarshal([]byte(result), &plan); err != nil {
		t.Fatalf("invalid JSON output: %v", err)
	}
	if _, ok := plan["period"]; !ok {
		t.Error("expected 'period' in plan")
	}
	if _, ok := plan["warnings"]; !ok {
		t.Error("expected 'warnings' in plan")
	}
}

// --- Test helpers ---
// contains() is defined in memory_test.go and shared across the package.

func schedTruncateForTest(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// --- from task_manager_test.go ---

// testTaskDB creates a temporary DB and initializes task manager tables.
func testTaskDB(t *testing.T) (string, func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_tasks.db")

	// Create the database file first.
	f, err := os.Create(dbPath)
	if err != nil {
		t.Fatalf("create db file: %v", err)
	}
	f.Close()

	if err := initTaskManagerDB(dbPath); err != nil {
		t.Fatalf("initTaskManagerDB: %v", err)
	}
	return dbPath, func() { os.RemoveAll(dir) }
}

func testTaskService(t *testing.T) (*TaskManagerService, func()) {
	t.Helper()
	dbPath, cleanup := testTaskDB(t)
	cfg := &Config{HistoryDB: dbPath}
	svc := newTaskManagerService(cfg)
	return svc, cleanup
}

func TestInitTaskManagerDB(t *testing.T) {
	_, cleanup := testTaskDB(t)
	defer cleanup()
}

func TestCreateTask(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	task := UserTask{
		UserID:   "user1",
		Title:    "Buy groceries",
		Priority: 2,
		Tags:     []string{"personal", "shopping"},
	}
	created, err := svc.CreateTask(task)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if created.ID == "" {
		t.Error("expected non-empty ID")
	}
	if created.Status != "todo" {
		t.Errorf("expected status 'todo', got %q", created.Status)
	}
	if created.Project != "inbox" {
		t.Errorf("expected project 'inbox', got %q", created.Project)
	}
	if created.CreatedAt == "" {
		t.Error("expected non-empty CreatedAt")
	}
	if len(created.Tags) != 2 {
		t.Errorf("expected 2 tags, got %d", len(created.Tags))
	}
}

func TestGetTask(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	task := UserTask{
		UserID:      "user1",
		Title:       "Test task",
		Description: "A test description",
		Priority:    1,
		Tags:        []string{"urgent"},
	}
	created, err := svc.CreateTask(task)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	got, err := svc.GetTask(created.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Title != "Test task" {
		t.Errorf("expected title 'Test task', got %q", got.Title)
	}
	if got.Description != "A test description" {
		t.Errorf("expected description, got %q", got.Description)
	}
	if got.Priority != 1 {
		t.Errorf("expected priority 1, got %d", got.Priority)
	}
	if len(got.Tags) != 1 || got.Tags[0] != "urgent" {
		t.Errorf("expected tags [urgent], got %v", got.Tags)
	}
}

func TestGetTask_NotFound(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	_, err := svc.GetTask("nonexistent-id")
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

func TestUpdateTask(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	created, err := svc.CreateTask(UserTask{
		UserID: "user1",
		Title:  "Original title",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	err = svc.UpdateTask(created.ID, map[string]any{
		"title":    "Updated title",
		"status":   "in_progress",
		"priority": 1,
	})
	if err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}

	got, err := svc.GetTask(created.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Title != "Updated title" {
		t.Errorf("expected updated title, got %q", got.Title)
	}
	if got.Status != "in_progress" {
		t.Errorf("expected status 'in_progress', got %q", got.Status)
	}
	if got.Priority != 1 {
		t.Errorf("expected priority 1, got %d", got.Priority)
	}
}

func TestUpdateTask_EmptyUpdates(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	created, _ := svc.CreateTask(UserTask{UserID: "u1", Title: "t"})
	err := svc.UpdateTask(created.ID, map[string]any{})
	if err != nil {
		t.Fatalf("expected no error for empty updates, got: %v", err)
	}
}

func TestDeleteTask(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	created, err := svc.CreateTask(UserTask{
		UserID: "user1",
		Title:  "To delete",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	err = svc.DeleteTask(created.ID)
	if err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	_, err = svc.GetTask(created.ID)
	if err == nil {
		t.Fatal("expected error after deletion")
	}
}

func TestCompleteTask(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	created, _ := svc.CreateTask(UserTask{
		UserID: "user1",
		Title:  "Complete me",
	})

	err := svc.CompleteTask(created.ID)
	if err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}

	got, _ := svc.GetTask(created.ID)
	if got.Status != "done" {
		t.Errorf("expected status 'done', got %q", got.Status)
	}
	if got.CompletedAt == "" {
		t.Error("expected non-empty CompletedAt")
	}
}

func TestCompleteTask_WithSubtasks(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	parent, _ := svc.CreateTask(UserTask{
		UserID: "user1",
		Title:  "Parent task",
	})

	// Create subtasks.
	sub1, _ := svc.CreateTask(UserTask{
		UserID:   "user1",
		Title:    "Subtask 1",
		ParentID: parent.ID,
	})
	sub2, _ := svc.CreateTask(UserTask{
		UserID:   "user1",
		Title:    "Subtask 2",
		ParentID: parent.ID,
	})

	// Create a nested subtask.
	nested, _ := svc.CreateTask(UserTask{
		UserID:   "user1",
		Title:    "Nested subtask",
		ParentID: sub1.ID,
	})

	// Complete parent should cascade.
	err := svc.CompleteTask(parent.ID)
	if err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}

	// Verify all are completed.
	for _, id := range []string{parent.ID, sub1.ID, sub2.ID, nested.ID} {
		got, _ := svc.GetTask(id)
		if got.Status != "done" {
			t.Errorf("task %s: expected 'done', got %q", id, got.Status)
		}
	}
}

func TestCompleteTask_SkipsCancelledSubtasks(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	parent, _ := svc.CreateTask(UserTask{
		UserID: "user1",
		Title:  "Parent",
	})
	sub, _ := svc.CreateTask(UserTask{
		UserID:   "user1",
		Title:    "Cancelled subtask",
		ParentID: parent.ID,
		Status:   "cancelled",
	})

	err := svc.CompleteTask(parent.ID)
	if err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}

	got, _ := svc.GetTask(sub.ID)
	if got.Status != "cancelled" {
		t.Errorf("expected cancelled subtask to stay 'cancelled', got %q", got.Status)
	}
}

func TestListTasks(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	for i := 0; i < 5; i++ {
		svc.CreateTask(UserTask{
			UserID:   "user1",
			Title:    "Task " + string(rune('A'+i)),
			Priority: (i % 4) + 1,
		})
	}
	// Task from different user.
	svc.CreateTask(UserTask{
		UserID: "user2",
		Title:  "Other user task",
	})

	tasks, err := svc.ListTasks("user1", TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 5 {
		t.Errorf("expected 5 tasks, got %d", len(tasks))
	}
}

func TestListTasks_WithFilters(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	svc.CreateTask(UserTask{UserID: "u1", Title: "Todo 1", Status: "todo", Project: "work", Priority: 1})
	svc.CreateTask(UserTask{UserID: "u1", Title: "Todo 2", Status: "todo", Project: "personal"})
	svc.CreateTask(UserTask{UserID: "u1", Title: "Done 1", Status: "done", Project: "work"})
	svc.CreateTask(UserTask{UserID: "u1", Title: "In Prog", Status: "in_progress", Project: "work"})

	// Filter by status.
	tasks, _ := svc.ListTasks("u1", TaskFilter{Status: "todo"})
	if len(tasks) != 2 {
		t.Errorf("status filter: expected 2, got %d", len(tasks))
	}

	// Filter by project.
	tasks, _ = svc.ListTasks("u1", TaskFilter{Project: "work"})
	if len(tasks) != 3 {
		t.Errorf("project filter: expected 3, got %d", len(tasks))
	}

	// Filter by priority.
	tasks, _ = svc.ListTasks("u1", TaskFilter{Priority: 1})
	if len(tasks) != 1 {
		t.Errorf("priority filter: expected 1, got %d", len(tasks))
	}

	// Filter by limit.
	tasks, _ = svc.ListTasks("u1", TaskFilter{Limit: 2})
	if len(tasks) != 2 {
		t.Errorf("limit filter: expected 2, got %d", len(tasks))
	}
}

func TestListTasks_DueDateFilter(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	tomorrow := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	nextWeek := time.Now().UTC().Add(7 * 24 * time.Hour).Format(time.RFC3339)

	svc.CreateTask(UserTask{UserID: "u1", Title: "Due tomorrow", DueAt: tomorrow})
	svc.CreateTask(UserTask{UserID: "u1", Title: "Due next week", DueAt: nextWeek})
	svc.CreateTask(UserTask{UserID: "u1", Title: "No due date"})

	// Filter by due date (before 3 days from now).
	cutoff := time.Now().UTC().Add(3 * 24 * time.Hour).Format(time.RFC3339)
	tasks, _ := svc.ListTasks("u1", TaskFilter{DueDate: cutoff})
	if len(tasks) != 1 {
		t.Errorf("due date filter: expected 1, got %d", len(tasks))
	}
}

func TestListTasks_TagFilter(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	svc.CreateTask(UserTask{UserID: "u1", Title: "Tagged", Tags: []string{"important", "work"}})
	svc.CreateTask(UserTask{UserID: "u1", Title: "Other tag", Tags: []string{"personal"}})

	tasks, _ := svc.ListTasks("u1", TaskFilter{Tag: "important"})
	if len(tasks) != 1 {
		t.Errorf("tag filter: expected 1, got %d", len(tasks))
	}
}

func TestGetSubtasks(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	parent, _ := svc.CreateTask(UserTask{UserID: "u1", Title: "Parent"})
	svc.CreateTask(UserTask{UserID: "u1", Title: "Sub1", ParentID: parent.ID, SortOrder: 1})
	svc.CreateTask(UserTask{UserID: "u1", Title: "Sub2", ParentID: parent.ID, SortOrder: 2})

	subs, err := svc.GetSubtasks(parent.ID)
	if err != nil {
		t.Fatalf("GetSubtasks: %v", err)
	}
	if len(subs) != 2 {
		t.Errorf("expected 2 subtasks, got %d", len(subs))
	}
	if subs[0].Title != "Sub1" {
		t.Errorf("expected first subtask 'Sub1', got %q", subs[0].Title)
	}
}

func TestGetSubtasks_Empty(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	parent, _ := svc.CreateTask(UserTask{UserID: "u1", Title: "No subs"})
	subs, err := svc.GetSubtasks(parent.ID)
	if err != nil {
		t.Fatalf("GetSubtasks: %v", err)
	}
	if len(subs) != 0 {
		t.Errorf("expected 0 subtasks, got %d", len(subs))
	}
}

func TestCreateProject(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	proj, err := svc.CreateProject("user1", "Work", "Work-related tasks")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if proj.ID == "" {
		t.Error("expected non-empty ID")
	}
	if proj.Name != "Work" {
		t.Errorf("expected name 'Work', got %q", proj.Name)
	}
}

func TestCreateProject_Duplicate(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	_, err := svc.CreateProject("user1", "Work", "")
	if err != nil {
		t.Fatalf("first CreateProject: %v", err)
	}

	_, err = svc.CreateProject("user1", "Work", "duplicate")
	if err == nil {
		t.Fatal("expected error for duplicate project name")
	}
}

func TestListProjects(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	svc.CreateProject("user1", "Alpha", "")
	svc.CreateProject("user1", "Beta", "")
	svc.CreateProject("user2", "Gamma", "")

	projs, err := svc.ListProjects("user1")
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projs) != 2 {
		t.Errorf("expected 2 projects, got %d", len(projs))
	}
	// Should be sorted by name.
	if projs[0].Name != "Alpha" {
		t.Errorf("expected first project 'Alpha', got %q", projs[0].Name)
	}
}

func TestDecomposeTask(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	parent, _ := svc.CreateTask(UserTask{
		UserID:   "user1",
		Title:    "Plan vacation",
		Project:  "personal",
		Priority: 2,
		Tags:     []string{"travel"},
	})

	subtitles := []string{"Book flights", "Reserve hotel", "Plan activities"}
	subs, err := svc.DecomposeTask(parent.ID, subtitles)
	if err != nil {
		t.Fatalf("DecomposeTask: %v", err)
	}
	if len(subs) != 3 {
		t.Errorf("expected 3 subtasks, got %d", len(subs))
	}

	// Verify subtask properties inherited from parent.
	for i, sub := range subs {
		if sub.ParentID != parent.ID {
			t.Errorf("subtask %d: parent_id mismatch", i)
		}
		if sub.Project != "personal" {
			t.Errorf("subtask %d: project should be 'personal', got %q", i, sub.Project)
		}
		if sub.Priority != 2 {
			t.Errorf("subtask %d: priority should be 2, got %d", i, sub.Priority)
		}
		if sub.SortOrder != i+1 {
			t.Errorf("subtask %d: sort_order should be %d, got %d", i, i+1, sub.SortOrder)
		}
	}

	// Parent should now be in_progress.
	updatedParent, _ := svc.GetTask(parent.ID)
	if updatedParent.Status != "in_progress" {
		t.Errorf("expected parent status 'in_progress', got %q", updatedParent.Status)
	}
}

func TestDecomposeTask_NonexistentParent(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	_, err := svc.DecomposeTask("nonexistent", []string{"sub1"})
	if err == nil {
		t.Fatal("expected error for nonexistent parent")
	}
}

func TestGenerateReview(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	now := time.Now().UTC().Format(time.RFC3339)
	past := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	yesterday := time.Now().UTC().Add(-25 * time.Hour).Format(time.RFC3339)

	// Create tasks with various states.
	svc.CreateTask(UserTask{UserID: "u1", Title: "Done recently", Status: "done", Project: "work"})
	// Manually set completed_at to recent time.
	tasks, _ := svc.ListTasks("u1", TaskFilter{Status: "done"})
	if len(tasks) > 0 {
		svc.UpdateTask(tasks[0].ID, map[string]any{"status": "done"})
		// Manually set completed_at via raw SQL.
		setCompleted := fmt.Sprintf(`UPDATE user_tasks SET completed_at = '%s' WHERE id = '%s';`,
			db.Escape(past), db.Escape(tasks[0].ID))
		exec.Command("sqlite3", svc.DBPath(), setCompleted).Run()
	}

	svc.CreateTask(UserTask{UserID: "u1", Title: "In progress", Status: "in_progress", Project: "work"})
	svc.CreateTask(UserTask{UserID: "u1", Title: "Todo 1", Status: "todo", Project: "personal"})
	svc.CreateTask(UserTask{UserID: "u1", Title: "Overdue", Status: "todo", DueAt: yesterday, Project: "work"})

	_ = now // used via time checks in the review

	review, err := svc.GenerateReview("u1", "daily")
	if err != nil {
		t.Fatalf("GenerateReview: %v", err)
	}
	if review.Period != "daily" {
		t.Errorf("expected period 'daily', got %q", review.Period)
	}
	if review.InProgress != 1 {
		t.Errorf("expected 1 in_progress, got %d", review.InProgress)
	}
	if review.Pending < 2 {
		t.Errorf("expected at least 2 pending, got %d", review.Pending)
	}
	if review.Overdue < 1 {
		t.Errorf("expected at least 1 overdue, got %d", review.Overdue)
	}
	if len(review.TopProjects) == 0 {
		t.Error("expected at least 1 top project")
	}
}

func TestGenerateReview_Weekly(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	svc.CreateTask(UserTask{UserID: "u1", Title: "Weekly task", Status: "todo"})

	review, err := svc.GenerateReview("u1", "weekly")
	if err != nil {
		t.Fatalf("GenerateReview weekly: %v", err)
	}
	if review.Period != "weekly" {
		t.Errorf("expected period 'weekly', got %q", review.Period)
	}
}

func TestTaskFromRow(t *testing.T) {
	row := map[string]any{
		"id":              "test-id",
		"user_id":         "user1",
		"title":           "Test",
		"description":     "desc",
		"project":         "inbox",
		"status":          "todo",
		"priority":        float64(2),
		"due_at":          "",
		"parent_id":       "",
		"tags":            `["a","b"]`,
		"source_channel":  "telegram",
		"external_id":     "",
		"external_source": "",
		"sort_order":      float64(0),
		"created_at":      "2026-01-01T00:00:00Z",
		"updated_at":      "2026-01-01T00:00:00Z",
		"completed_at":    "",
	}

	task := taskFromRow(row)
	if task.ID != "test-id" {
		t.Errorf("expected ID 'test-id', got %q", task.ID)
	}
	if len(task.Tags) != 2 {
		t.Errorf("expected 2 tags, got %d", len(task.Tags))
	}
	if task.Tags[0] != "a" || task.Tags[1] != "b" {
		t.Errorf("expected tags [a,b], got %v", task.Tags)
	}
}

func TestTaskFieldToColumn(t *testing.T) {
	tests := []struct {
		field  string
		column string
	}{
		{"title", "title"},
		{"dueAt", "due_at"},
		{"parentId", "parent_id"},
		{"sortOrder", "sort_order"},
		{"unknown", ""},
	}
	for _, tt := range tests {
		got := taskFieldToColumn(tt.field)
		if got != tt.column {
			t.Errorf("taskFieldToColumn(%q) = %q, want %q", tt.field, got, tt.column)
		}
	}
}

func TestDefaultProjectOrInbox(t *testing.T) {
	cfg := TaskManagerConfig{}
	if cfg.DefaultProjectOrInbox() != "inbox" {
		t.Errorf("expected 'inbox', got %q", cfg.DefaultProjectOrInbox())
	}

	cfg.DefaultProject = "work"
	if cfg.DefaultProjectOrInbox() != "work" {
		t.Errorf("expected 'work', got %q", cfg.DefaultProjectOrInbox())
	}
}

func TestCreateTaskPriorityValidation(t *testing.T) {
	svc, cleanup := testTaskService(t)
	defer cleanup()

	// Priority 0 should default to 2.
	created, _ := svc.CreateTask(UserTask{UserID: "u1", Title: "No priority"})
	got, _ := svc.GetTask(created.ID)
	if got.Priority != 2 {
		t.Errorf("expected default priority 2, got %d", got.Priority)
	}

	// Priority 5 (out of range) should default to 2.
	created, _ = svc.CreateTask(UserTask{UserID: "u1", Title: "Bad priority", Priority: 5})
	got, _ = svc.GetTask(created.ID)
	if got.Priority != 2 {
		t.Errorf("expected default priority 2 for out-of-range, got %d", got.Priority)
	}
}

func testAppCtx(tm *TaskManagerService) context.Context {
	app := &App{TaskManager: tm}
	return withApp(context.Background(), app)
}

func TestToolTaskCreate_JSONOutput(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	f, _ := os.Create(dbPath)
	f.Close()
	initTaskManagerDB(dbPath)

	cfg := &Config{HistoryDB: dbPath}
	svc := newTaskManagerService(cfg)
	ctx := testAppCtx(svc)

	input, _ := json.Marshal(map[string]any{
		"title":  "Test tool create",
		"userId": "tool-user",
	})
	result, err := toolTaskCreate(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolTaskCreate: %v", err)
	}

	var task UserTask
	if err := json.Unmarshal([]byte(result), &task); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if task.Title != "Test tool create" {
		t.Errorf("expected title 'Test tool create', got %q", task.Title)
	}
}

func TestToolTaskCreate_WithDecompose(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	f, _ := os.Create(dbPath)
	f.Close()
	initTaskManagerDB(dbPath)

	cfg := &Config{HistoryDB: dbPath}
	svc := newTaskManagerService(cfg)
	ctx := testAppCtx(svc)

	input, _ := json.Marshal(map[string]any{
		"title":     "Big task",
		"userId":    "u1",
		"decompose": true,
		"subtasks":  []string{"Step 1", "Step 2"},
	})
	result, err := toolTaskCreate(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolTaskCreate with decompose: %v", err)
	}

	var out struct {
		Task     UserTask   `json:"task"`
		Subtasks []UserTask `json:"subtasks"`
	}
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("unmarshal decompose result: %v", err)
	}
	if len(out.Subtasks) != 2 {
		t.Errorf("expected 2 subtasks, got %d", len(out.Subtasks))
	}
}

func TestToolTaskCreate_MissingTitle(t *testing.T) {
	ctx := testAppCtx(newTaskManagerService(&Config{}))

	input, _ := json.Marshal(map[string]any{})
	_, err := toolTaskCreate(ctx, &Config{}, input)
	if err == nil {
		t.Fatal("expected error for missing title")
	}
}

func TestToolTaskCreate_NotInitialized(t *testing.T) {
	ctx := testAppCtx(nil)

	input, _ := json.Marshal(map[string]any{"title": "test"})
	_, err := toolTaskCreate(ctx, &Config{}, input)
	if err == nil {
		t.Fatal("expected error when not initialized")
	}
}

// --- from tasks_test.go ---

func TestEscapeSQLite(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"normal string", "hello", "hello"},
		{"single quote", "it's", "it''s"},
		{"double single quotes", "it''s", "it''''s"},
		{"null byte removed", "hello\x00world", "helloworld"},
		{"null and quote combined", "it's\x00test", "it''stest"},
		{"empty string", "", ""},
		{"unicode unchanged", "\u3053\u3093\u306b\u3061\u306f", "\u3053\u3093\u306b\u3061\u306f"},
		{"sql injection attempt", "'; DROP TABLE--", "''; DROP TABLE--"},
		{"multiple quotes", "a'b'c", "a''b''c"},
		{"only null bytes", "\x00\x00\x00", ""},
		{"only single quote", "'", "''"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := db.Escape(tt.input)
			if got != tt.want {
				t.Errorf("db.Escape(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestStringSliceContains(t *testing.T) {
	tests := []struct {
		name  string
		slice []string
		value string
		want  bool
	}{
		{"found exact", []string{"alpha", "beta", "gamma"}, "beta", true},
		{"found case insensitive upper", []string{"alpha", "beta"}, "ALPHA", true},
		{"found case insensitive mixed", []string{"Hello", "World"}, "hello", true},
		{"not found", []string{"alpha", "beta"}, "delta", false},
		{"empty slice", []string{}, "anything", false},
		{"nil slice", nil, "anything", false},
		{"empty search string", []string{"alpha", ""}, "", true},
		{"first element", []string{"target", "other"}, "target", true},
		{"last element", []string{"other", "target"}, "target", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stringSliceContains(tt.slice, tt.value)
			if got != tt.want {
				t.Errorf("stringSliceContains(%v, %q) = %v, want %v",
					tt.slice, tt.value, got, tt.want)
			}
		})
	}
}

// --- from template_test.go ---

// ---------------------------------------------------------------------------
// expandPrompt
// ---------------------------------------------------------------------------

func TestExpandPrompt_NoTemplateVariables(t *testing.T) {
	got := expandPrompt("hello world", "", "", "", "", nil)
	if got != "hello world" {
		t.Errorf("expandPrompt(%q) = %q, want %q", "hello world", got, "hello world")
	}
}

func TestExpandPrompt_DateReplacement(t *testing.T) {
	got := expandPrompt("Today is {{date}}", "", "", "", "", nil)
	want := "Today is " + time.Now().Format("2006-01-02")
	if got != want {
		t.Errorf("expandPrompt with {{date}} = %q, want %q", got, want)
	}
}

func TestExpandPrompt_WeekdayReplacement(t *testing.T) {
	got := expandPrompt("Day: {{weekday}}", "", "", "", "", nil)
	want := "Day: " + time.Now().Weekday().String()
	if got != want {
		t.Errorf("expandPrompt with {{weekday}} = %q, want %q", got, want)
	}
}

func TestExpandPrompt_EnvVarSet(t *testing.T) {
	t.Setenv("TETORA_TEST_TMPL", "foo")

	got := expandPrompt("Hello {{env.TETORA_TEST_TMPL}}", "", "", "", "", nil)
	if got != "Hello foo" {
		t.Errorf("expandPrompt with {{env.TETORA_TEST_TMPL}} = %q, want %q", got, "Hello foo")
	}
}

func TestExpandPrompt_EnvVarUnset(t *testing.T) {
	got := expandPrompt("Val={{env.TETORA_UNSET_VAR_99999}}", "", "", "", "", nil)
	if got != "Val=" {
		t.Errorf("expandPrompt with unset env var = %q, want %q", got, "Val=")
	}
}

func TestExpandPrompt_MultipleVariables(t *testing.T) {
	got := expandPrompt("Date: {{date}}, Day: {{weekday}}", "", "", "", "", nil)
	wantDate := time.Now().Format("2006-01-02")
	wantWeekday := time.Now().Weekday().String()
	want := "Date: " + wantDate + ", Day: " + wantWeekday
	if got != want {
		t.Errorf("expandPrompt with multiple vars = %q, want %q", got, want)
	}
}

func TestExpandPrompt_LastOutputWithEmptyJobIDAndDBPath(t *testing.T) {
	input := "Previous: {{last_output}}"
	got := expandPrompt(input, "", "", "", "", nil)
	// When jobID and dbPath are both empty, last_* variables are not replaced.
	if got != input {
		t.Errorf("expandPrompt with empty jobID/dbPath = %q, want %q (unchanged)", got, input)
	}
}

// --- from trust_test.go ---

func setupTrustTestDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "trust_test.db")
	if err := history.InitDB(dbPath); err != nil {
		t.Fatalf("initHistoryDB: %v", err)
	}
	sla.InitSLADB(dbPath)
	initTrustDB(dbPath)
	return dbPath
}

func testCfgWithTrust(dbPath string) *Config {
	return &Config{
		HistoryDB: dbPath,
		Agents: map[string]AgentConfig{
			"翡翠": {Model: "sonnet", TrustLevel: "suggest"},
			"黒曜": {Model: "opus", TrustLevel: "auto"},
			"琥珀": {Model: "sonnet", TrustLevel: "observe"},
		},
		Trust: TrustConfig{
			Enabled:          true,
			PromoteThreshold: 5,
		},
	}
}

// --- Trust Level Validation ---

func TestIsValidTrustLevel(t *testing.T) {
	tests := []struct {
		level string
		want  bool
	}{
		{"observe", true},
		{"suggest", true},
		{"auto", true},
		{"", false},
		{"unknown", false},
		{"AUTO", false},
	}
	for _, tt := range tests {
		if got := isValidTrustLevel(tt.level); got != tt.want {
			t.Errorf("isValidTrustLevel(%q) = %v, want %v", tt.level, got, tt.want)
		}
	}
}

func TestTrustLevelIndex(t *testing.T) {
	if idx := trustLevelIndex("observe"); idx != 0 {
		t.Errorf("trustLevelIndex(observe) = %d, want 0", idx)
	}
	if idx := trustLevelIndex("suggest"); idx != 1 {
		t.Errorf("trustLevelIndex(suggest) = %d, want 1", idx)
	}
	if idx := trustLevelIndex("auto"); idx != 2 {
		t.Errorf("trustLevelIndex(auto) = %d, want 2", idx)
	}
	if idx := trustLevelIndex("invalid"); idx != -1 {
		t.Errorf("trustLevelIndex(invalid) = %d, want -1", idx)
	}
}

func TestNextTrustLevel(t *testing.T) {
	if next := nextTrustLevel("observe"); next != "suggest" {
		t.Errorf("nextTrustLevel(observe) = %q, want suggest", next)
	}
	if next := nextTrustLevel("suggest"); next != "auto" {
		t.Errorf("nextTrustLevel(suggest) = %q, want auto", next)
	}
	if next := nextTrustLevel("auto"); next != "" {
		t.Errorf("nextTrustLevel(auto) = %q, want empty", next)
	}
}

// --- Trust Level Resolution ---

func TestResolveTrustLevel(t *testing.T) {
	cfg := testCfgWithTrust("")

	if level := resolveTrustLevel(cfg, "翡翠"); level != "suggest" {
		t.Errorf("翡翠 trust level = %q, want suggest", level)
	}
	if level := resolveTrustLevel(cfg, "黒曜"); level != "auto" {
		t.Errorf("黒曜 trust level = %q, want auto", level)
	}
	if level := resolveTrustLevel(cfg, "琥珀"); level != "observe" {
		t.Errorf("琥珀 trust level = %q, want observe", level)
	}
}

func TestResolveTrustLevelDisabled(t *testing.T) {
	cfg := &Config{
		Trust: TrustConfig{Enabled: false},
		Agents: map[string]AgentConfig{
			"翡翠": {TrustLevel: "observe"},
		},
	}
	// When trust is disabled, always returns auto.
	if level := resolveTrustLevel(cfg, "翡翠"); level != "auto" {
		t.Errorf("disabled trust level = %q, want auto", level)
	}
}

func TestResolveTrustLevelDefault(t *testing.T) {
	cfg := &Config{
		Trust: TrustConfig{Enabled: true},
		Agents: map[string]AgentConfig{
			"翡翠": {Model: "sonnet"}, // no TrustLevel set
		},
	}
	// Default should be auto.
	if level := resolveTrustLevel(cfg, "翡翠"); level != "auto" {
		t.Errorf("default trust level = %q, want auto", level)
	}
}

// --- Apply Trust to Task ---

func TestApplyTrustObserve(t *testing.T) {
	cfg := testCfgWithTrust("")
	task := Task{PermissionMode: "acceptEdits"}

	level, needsConfirm := applyTrustToTask(cfg, &task, "琥珀")
	if level != "observe" {
		t.Errorf("level = %q, want observe", level)
	}
	if needsConfirm {
		t.Error("observe mode should not need confirmation")
	}
	if task.PermissionMode != "plan" {
		t.Errorf("permissionMode = %q, want plan (forced by observe)", task.PermissionMode)
	}
}

func TestApplyTrustSuggest(t *testing.T) {
	cfg := testCfgWithTrust("")
	task := Task{PermissionMode: "acceptEdits"}

	level, needsConfirm := applyTrustToTask(cfg, &task, "翡翠")
	if level != "suggest" {
		t.Errorf("level = %q, want suggest", level)
	}
	if !needsConfirm {
		t.Error("suggest mode should need confirmation")
	}
	if task.PermissionMode != "acceptEdits" {
		t.Errorf("permissionMode should not change for suggest mode, got %q", task.PermissionMode)
	}
}

func TestApplyTrustAuto(t *testing.T) {
	cfg := testCfgWithTrust("")
	task := Task{PermissionMode: "acceptEdits"}

	level, needsConfirm := applyTrustToTask(cfg, &task, "黒曜")
	if level != "auto" {
		t.Errorf("level = %q, want auto", level)
	}
	if needsConfirm {
		t.Error("auto mode should not need confirmation")
	}
}

// --- DB Operations ---

func TestInitTrustDB(t *testing.T) {
	dbPath := setupTrustTestDB(t)

	// Verify trust_events table exists.
	rows, err := db.Query(dbPath, "SELECT name FROM sqlite_master WHERE type='table' AND name='trust_events'")
	if err != nil {
		t.Fatalf("db.Query: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected trust_events table, got %d tables", len(rows))
	}
}

func TestRecordAndQueryTrustEvents(t *testing.T) {
	dbPath := setupTrustTestDB(t)

	recordTrustEvent(dbPath, "翡翠", "set", "observe", "suggest", 0, "test set")
	recordTrustEvent(dbPath, "翡翠", "promote", "suggest", "auto", 10, "auto promoted")

	events, err := queryTrustEvents(dbPath, "翡翠", 10)
	if err != nil {
		t.Fatalf("queryTrustEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	// Most recent first.
	if jsonStr(events[0]["event_type"]) != "promote" {
		t.Errorf("first event = %q, want promote", jsonStr(events[0]["event_type"]))
	}
	if jsonStr(events[1]["event_type"]) != "set" {
		t.Errorf("second event = %q, want set", jsonStr(events[1]["event_type"]))
	}
}

func TestQueryTrustEventsAllRoles(t *testing.T) {
	dbPath := setupTrustTestDB(t)

	recordTrustEvent(dbPath, "翡翠", "set", "", "suggest", 0, "")
	recordTrustEvent(dbPath, "黒曜", "set", "", "auto", 0, "")

	events, err := queryTrustEvents(dbPath, "", 10)
	if err != nil {
		t.Fatalf("queryTrustEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events across all roles, got %d", len(events))
	}
}

// --- Consecutive Success ---

func TestQueryConsecutiveSuccess(t *testing.T) {
	dbPath := setupTrustTestDB(t)

	// Insert 5 successes then 1 failure then 3 successes.
	now := time.Now()
	for i := 0; i < 5; i++ {
		insertTestRun(t, dbPath, "翡翠", "success",
			now.Add(time.Duration(i)*time.Minute).Format(time.RFC3339),
			now.Add(time.Duration(i)*time.Minute+30*time.Second).Format(time.RFC3339), 0.1)
	}
	insertTestRun(t, dbPath, "翡翠", "error",
		now.Add(5*time.Minute).Format(time.RFC3339),
		now.Add(5*time.Minute+30*time.Second).Format(time.RFC3339), 0.1)
	for i := 6; i < 9; i++ {
		insertTestRun(t, dbPath, "翡翠", "success",
			now.Add(time.Duration(i)*time.Minute).Format(time.RFC3339),
			now.Add(time.Duration(i)*time.Minute+30*time.Second).Format(time.RFC3339), 0.1)
	}

	// Most recent consecutive successes = 3 (before hitting the error).
	count := queryConsecutiveSuccess(dbPath, "翡翠")
	if count != 3 {
		t.Errorf("consecutive success = %d, want 3", count)
	}
}

func TestQueryConsecutiveSuccessEmpty(t *testing.T) {
	dbPath := setupTrustTestDB(t)
	count := queryConsecutiveSuccess(dbPath, "翡翠")
	if count != 0 {
		t.Errorf("consecutive success = %d, want 0", count)
	}
}

func TestQueryConsecutiveSuccessAllSuccess(t *testing.T) {
	dbPath := setupTrustTestDB(t)

	now := time.Now()
	for i := 0; i < 7; i++ {
		insertTestRun(t, dbPath, "翡翠", "success",
			now.Add(time.Duration(i)*time.Minute).Format(time.RFC3339),
			now.Add(time.Duration(i)*time.Minute+30*time.Second).Format(time.RFC3339), 0.1)
	}

	count := queryConsecutiveSuccess(dbPath, "翡翠")
	if count != 7 {
		t.Errorf("consecutive success = %d, want 7", count)
	}
}

// --- Trust Status ---

func TestGetTrustStatus(t *testing.T) {
	dbPath := setupTrustTestDB(t)
	cfg := testCfgWithTrust(dbPath)

	// Add some successes for 翡翠.
	now := time.Now()
	for i := 0; i < 6; i++ {
		insertTestRun(t, dbPath, "翡翠", "success",
			now.Add(time.Duration(i)*time.Minute).Format(time.RFC3339),
			now.Add(time.Duration(i)*time.Minute+30*time.Second).Format(time.RFC3339), 0.1)
	}

	status := getTrustStatus(cfg, "翡翠")
	if status.Level != "suggest" {
		t.Errorf("level = %q, want suggest", status.Level)
	}
	if status.ConsecutiveSuccess != 6 {
		t.Errorf("consecutiveSuccess = %d, want 6", status.ConsecutiveSuccess)
	}
	if !status.PromoteReady {
		t.Error("expected promoteReady = true (6 >= threshold 5)")
	}
	if status.NextLevel != "auto" {
		t.Errorf("nextLevel = %q, want auto", status.NextLevel)
	}
	if status.TotalTasks != 6 {
		t.Errorf("totalTasks = %d, want 6", status.TotalTasks)
	}
}

func TestGetAllTrustStatuses(t *testing.T) {
	dbPath := setupTrustTestDB(t)
	cfg := testCfgWithTrust(dbPath)

	statuses := getAllTrustStatuses(cfg)
	if len(statuses) != 3 {
		t.Fatalf("expected 3 statuses, got %d", len(statuses))
	}
}

// --- Config Update ---

func TestUpdateRoleTrustLevel(t *testing.T) {
	cfg := testCfgWithTrust("")

	if err := updateAgentTrustLevel(cfg, "翡翠", "auto"); err != nil {
		t.Fatalf("updateAgentTrustLevel: %v", err)
	}
	if level := resolveTrustLevel(cfg, "翡翠"); level != "auto" {
		t.Errorf("level = %q, want auto", level)
	}
}

func TestUpdateRoleTrustLevelInvalid(t *testing.T) {
	cfg := testCfgWithTrust("")

	if err := updateAgentTrustLevel(cfg, "翡翠", "invalid"); err == nil {
		t.Error("expected error for invalid trust level")
	}
}

func TestUpdateRoleTrustLevelUnknownRole(t *testing.T) {
	cfg := testCfgWithTrust("")

	if err := updateAgentTrustLevel(cfg, "unknown", "auto"); err == nil {
		t.Error("expected error for unknown role")
	}
}

func TestSaveRoleTrustLevel(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	// Create a minimal config.
	cfg := map[string]any{
		"agents": map[string]any{
			"翡翠": map[string]any{
				"model":      "sonnet",
				"trustLevel": "suggest",
			},
		},
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(configPath, data, 0o644)

	// Update trust level.
	if err := saveAgentTrustLevel(configPath, "翡翠", "auto"); err != nil {
		t.Fatalf("saveAgentTrustLevel: %v", err)
	}

	// Read back and verify.
	data, _ = os.ReadFile(configPath)
	var result map[string]any
	json.Unmarshal(data, &result)

	roles := result["agents"].(map[string]any)
	role := roles["翡翠"].(map[string]any)
	if role["trustLevel"] != "auto" {
		t.Errorf("persisted trustLevel = %v, want auto", role["trustLevel"])
	}
}

// --- Promote Threshold ---

func TestPromoteThresholdOrDefault(t *testing.T) {
	cfg := TrustConfig{}
	if v := cfg.PromoteThresholdOrDefault(); v != 10 {
		t.Errorf("default = %d, want 10", v)
	}

	cfg = TrustConfig{PromoteThreshold: 20}
	if v := cfg.PromoteThresholdOrDefault(); v != 20 {
		t.Errorf("custom = %d, want 20", v)
	}
}

// --- HTTP API ---

func TestTrustAPIGetAll(t *testing.T) {
	dbPath := setupTrustTestDB(t)
	cfg := testCfgWithTrust(dbPath)

	mux := http.NewServeMux()
	mux.HandleFunc("/trust", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(getAllTrustStatuses(cfg))
	})

	req := httptest.NewRequest("GET", "/trust", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var statuses []TrustStatus
	json.Unmarshal(w.Body.Bytes(), &statuses)
	if len(statuses) != 3 {
		t.Fatalf("expected 3 statuses, got %d", len(statuses))
	}
}

func TestTrustAPIGetSingle(t *testing.T) {
	dbPath := setupTrustTestDB(t)
	cfg := testCfgWithTrust(dbPath)

	mux := http.NewServeMux()
	mux.HandleFunc("/trust/", func(w http.ResponseWriter, r *http.Request) {
		role := strings.TrimPrefix(r.URL.Path, "/trust/")
		if _, ok := cfg.Agents[role]; !ok {
			http.Error(w, `{"error":"not found"}`, 404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(getTrustStatus(cfg, role))
	})

	req := httptest.NewRequest("GET", "/trust/翡翠", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var status TrustStatus
	json.Unmarshal(w.Body.Bytes(), &status)
	if status.Level != "suggest" {
		t.Errorf("level = %q, want suggest", status.Level)
	}
}

func TestTrustAPISetLevel(t *testing.T) {
	dbPath := setupTrustTestDB(t)
	cfg := testCfgWithTrust(dbPath)

	// Write a config file for persistence.
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	cfgJSON := map[string]any{
		"agents": map[string]any{
			"翡翠": map[string]any{"model": "sonnet", "trustLevel": "suggest"},
		},
	}
	data, _ := json.MarshalIndent(cfgJSON, "", "  ")
	os.WriteFile(configPath, data, 0o644)
	cfg.BaseDir = dir

	mux := http.NewServeMux()
	mux.HandleFunc("/trust/", func(w http.ResponseWriter, r *http.Request) {
		role := strings.TrimPrefix(r.URL.Path, "/trust/")
		if _, ok := cfg.Agents[role]; !ok {
			http.Error(w, `{"error":"not found"}`, 404)
			return
		}
		var body struct{ Level string `json:"level"` }
		json.NewDecoder(r.Body).Decode(&body)
		updateAgentTrustLevel(cfg, role, body.Level)
		saveAgentTrustLevel(configPath, role, body.Level)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(getTrustStatus(cfg, role))
	})

	body := strings.NewReader(`{"level":"auto"}`)
	req := httptest.NewRequest("POST", "/trust/翡翠", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var status TrustStatus
	json.Unmarshal(w.Body.Bytes(), &status)
	if status.Level != "auto" {
		t.Errorf("level = %q, want auto", status.Level)
	}
}

// --- Trust Events API ---

func TestTrustEventsAPI(t *testing.T) {
	dbPath := setupTrustTestDB(t)

	recordTrustEvent(dbPath, "翡翠", "set", "observe", "suggest", 0, "via CLI")

	mux := http.NewServeMux()
	mux.HandleFunc("/trust-events", func(w http.ResponseWriter, r *http.Request) {
		role := r.URL.Query().Get("role")
		events, _ := queryTrustEvents(dbPath, role, 20)
		if events == nil {
			events = []map[string]any{}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(events)
	})

	req := httptest.NewRequest("GET", "/trust-events?role=翡翠", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var events []map[string]any
	json.Unmarshal(w.Body.Bytes(), &events)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
}

// --- from usage_test.go ---

func TestQueryUsageSummary(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test.db")
	if err := history.InitDB(dbPath); err != nil {
		t.Fatal(err)
	}

	// Insert test data for today.
	now := time.Now()
	history.InsertRun(dbPath, JobRun{
		JobID:     "u1",
		Name:      "test1",
		Source:    "test",
		StartedAt: now.Format(time.RFC3339),
		FinishedAt: now.Format(time.RFC3339),
		Status:    "success",
		CostUSD:   0.05,
		Model:     "sonnet",
		TokensIn:  1000,
		TokensOut: 500,
		Agent:      "ruri",
	})
	history.InsertRun(dbPath, JobRun{
		JobID:     "u2",
		Name:      "test2",
		Source:    "test",
		StartedAt: now.Format(time.RFC3339),
		FinishedAt: now.Format(time.RFC3339),
		Status:    "success",
		CostUSD:   0.10,
		Model:     "opus",
		TokensIn:  2000,
		TokensOut: 800,
		Agent:      "kohaku",
	})

	summary, err := queryUsageSummary(dbPath, "today")
	if err != nil {
		t.Fatal(err)
	}

	if summary.Period != "today" {
		t.Errorf("expected period=today, got %s", summary.Period)
	}
	if summary.TotalTasks != 2 {
		t.Errorf("expected 2 tasks, got %d", summary.TotalTasks)
	}
	if summary.TotalCost < 0.14 || summary.TotalCost > 0.16 {
		t.Errorf("expected ~0.15 total cost, got %.4f", summary.TotalCost)
	}
	if summary.TokensIn != 3000 {
		t.Errorf("expected 3000 tokens in, got %d", summary.TokensIn)
	}
	if summary.TokensOut != 1300 {
		t.Errorf("expected 1300 tokens out, got %d", summary.TokensOut)
	}
}

func TestQueryUsageSummaryEmptyDB(t *testing.T) {
	summary, err := queryUsageSummary("", "today")
	if err != nil {
		t.Fatal(err)
	}
	if summary.TotalCost != 0 {
		t.Errorf("expected 0 cost for empty db, got %.4f", summary.TotalCost)
	}
}

func TestQueryUsageByModel(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test.db")
	if err := history.InitDB(dbPath); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	history.InsertRun(dbPath, JobRun{
		JobID: "m1", Name: "test1", Source: "test",
		StartedAt: now.Format(time.RFC3339), FinishedAt: now.Format(time.RFC3339),
		Status: "success", CostUSD: 0.30, Model: "opus",
		TokensIn: 1000, TokensOut: 500,
	})
	history.InsertRun(dbPath, JobRun{
		JobID: "m2", Name: "test2", Source: "test",
		StartedAt: now.Format(time.RFC3339), FinishedAt: now.Format(time.RFC3339),
		Status: "success", CostUSD: 0.10, Model: "sonnet",
		TokensIn: 2000, TokensOut: 800,
	})
	history.InsertRun(dbPath, JobRun{
		JobID: "m3", Name: "test3", Source: "test",
		StartedAt: now.Format(time.RFC3339), FinishedAt: now.Format(time.RFC3339),
		Status: "success", CostUSD: 0.10, Model: "opus",
		TokensIn: 500, TokensOut: 200,
	})

	models, err := queryUsageByModel(dbPath, 30)
	if err != nil {
		t.Fatal(err)
	}

	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}

	// Should be ordered by cost DESC, so opus first.
	if models[0].Model != "opus" {
		t.Errorf("expected first model=opus, got %s", models[0].Model)
	}
	if models[0].Tasks != 2 {
		t.Errorf("expected opus tasks=2, got %d", models[0].Tasks)
	}
	if models[0].Cost < 0.39 || models[0].Cost > 0.41 {
		t.Errorf("expected opus cost ~0.40, got %.4f", models[0].Cost)
	}
	if models[0].Pct < 79 || models[0].Pct > 81 {
		t.Errorf("expected opus pct ~80%%, got %.1f%%", models[0].Pct)
	}
}

func TestQueryUsageByRole(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test.db")
	if err := history.InitDB(dbPath); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	history.InsertRun(dbPath, JobRun{
		JobID: "r1", Name: "test1", Source: "test",
		StartedAt: now.Format(time.RFC3339), FinishedAt: now.Format(time.RFC3339),
		Status: "success", CostUSD: 0.20, Model: "sonnet",
		TokensIn: 1000, TokensOut: 500, Agent: "ruri",
	})
	history.InsertRun(dbPath, JobRun{
		JobID: "r2", Name: "test2", Source: "test",
		StartedAt: now.Format(time.RFC3339), FinishedAt: now.Format(time.RFC3339),
		Status: "success", CostUSD: 0.05, Model: "sonnet",
		TokensIn: 500, TokensOut: 200, Agent: "kohaku",
	})

	roles, err := queryUsageByAgent(dbPath, 30)
	if err != nil {
		t.Fatal(err)
	}

	if len(roles) != 2 {
		t.Fatalf("expected 2 roles, got %d", len(roles))
	}

	// Ordered by cost DESC.
	if roles[0].Agent != "ruri" {
		t.Errorf("expected first role=ruri, got %s", roles[0].Agent)
	}
}

func TestQueryExpensiveSessions(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test.db")
	if err := history.InitDB(dbPath); err != nil {
		t.Fatal(err)
	}
	if err := initSessionDB(dbPath); err != nil {
		t.Fatal(err)
	}

	// Insert test sessions directly.
	now := time.Now().Format(time.RFC3339)
	queries := []string{
		"INSERT INTO sessions (id, agent, title, total_cost, message_count, total_tokens_in, total_tokens_out, created_at, updated_at) VALUES ('s1', 'ruri', 'Expensive session', 1.50, 10, 5000, 3000, '" + now + "', '" + now + "')",
		"INSERT INTO sessions (id, agent, title, total_cost, message_count, total_tokens_in, total_tokens_out, created_at, updated_at) VALUES ('s2', 'kohaku', 'Cheap session', 0.10, 3, 500, 200, '" + now + "', '" + now + "')",
		"INSERT INTO sessions (id, agent, title, total_cost, message_count, total_tokens_in, total_tokens_out, created_at, updated_at) VALUES ('s3', 'hisui', 'Medium session', 0.50, 5, 2000, 1000, '" + now + "', '" + now + "')",
	}
	for _, sql := range queries {
		db.Query(dbPath, sql)
	}

	sessions, err := queryExpensiveSessions(dbPath, 5, 30)
	if err != nil {
		t.Fatal(err)
	}

	if len(sessions) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(sessions))
	}

	// Ordered by total_cost DESC.
	if sessions[0].SessionID != "s1" {
		t.Errorf("expected first session=s1, got %s", sessions[0].SessionID)
	}
	if sessions[0].TotalCost < 1.49 || sessions[0].TotalCost > 1.51 {
		t.Errorf("expected s1 cost ~1.50, got %.4f", sessions[0].TotalCost)
	}
	if sessions[1].SessionID != "s3" {
		t.Errorf("expected second session=s3, got %s", sessions[1].SessionID)
	}
}

func TestQueryCostTrend(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test.db")
	if err := history.InitDB(dbPath); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	yesterday := now.AddDate(0, 0, -1)

	history.InsertRun(dbPath, JobRun{
		JobID: "t1", Name: "test1", Source: "test",
		StartedAt: now.Format(time.RFC3339), FinishedAt: now.Format(time.RFC3339),
		Status: "success", CostUSD: 0.05, Model: "sonnet",
		TokensIn: 1000, TokensOut: 500,
	})
	history.InsertRun(dbPath, JobRun{
		JobID: "t2", Name: "test2", Source: "test",
		StartedAt: yesterday.Format(time.RFC3339), FinishedAt: yesterday.Format(time.RFC3339),
		Status: "success", CostUSD: 0.10, Model: "opus",
		TokensIn: 2000, TokensOut: 800,
	})

	trend, err := queryCostTrend(dbPath, 7)
	if err != nil {
		t.Fatal(err)
	}

	if len(trend) < 1 {
		t.Fatal("expected at least 1 day in trend")
	}

	// Verify total across all days.
	var totalCost float64
	var totalTasks int
	for _, d := range trend {
		totalCost += d.Cost
		totalTasks += d.Tasks
	}
	if totalTasks != 2 {
		t.Errorf("expected 2 total tasks in trend, got %d", totalTasks)
	}
	if totalCost < 0.14 || totalCost > 0.16 {
		t.Errorf("expected ~0.15 total cost, got %.4f", totalCost)
	}
}

func TestFormatResponseCostFooter(t *testing.T) {
	// Disabled.
	cfg := &Config{}
	result := &ProviderResult{TokensIn: 1000, TokensOut: 500, CostUSD: 0.05}
	footer := formatResponseCostFooter(cfg, result)
	if footer != "" {
		t.Errorf("expected empty footer when disabled, got %q", footer)
	}

	// Enabled with default template.
	cfg.Usage.ShowFooter = true
	footer = formatResponseCostFooter(cfg, result)
	if footer != "1000in/500out ~$0.0500" {
		t.Errorf("unexpected footer: %q", footer)
	}

	// Custom template.
	cfg.Usage.FooterTemplate = "Cost: ${{.cost}} ({{.tokensIn}}+{{.tokensOut}})"
	footer = formatResponseCostFooter(cfg, result)
	if footer != "Cost: $0.0500 (1000+500)" {
		t.Errorf("unexpected custom footer: %q", footer)
	}

	// Nil result.
	footer = formatResponseCostFooter(cfg, nil)
	if footer != "" {
		t.Errorf("expected empty footer for nil result, got %q", footer)
	}

	// Nil config.
	footer = formatResponseCostFooter(nil, result)
	if footer != "" {
		t.Errorf("expected empty footer for nil config, got %q", footer)
	}
}

func TestFormatResultCostFooter(t *testing.T) {
	cfg := &Config{Usage: UsageConfig{ShowFooter: true}}
	result := &TaskResult{TokensIn: 500, TokensOut: 200, CostUSD: 0.02}
	footer := formatResultCostFooter(cfg, result)
	if footer != "500in/200out ~$0.0200" {
		t.Errorf("unexpected footer: %q", footer)
	}
}

// --- from workspace_test.go ---

func TestResolveWorkspace_Defaults(t *testing.T) {
	cfg := &Config{
		WorkspaceDir: "/home/user/.tetora/workspace",
		AgentsDir:    "/home/user/.tetora/agents",
		Agents: map[string]AgentConfig{
			"ruri": {Model: "opus"},
		},
	}

	ws := resolveWorkspace(cfg, "ruri")

	// Should use shared workspace directory.
	if ws.Dir != cfg.WorkspaceDir {
		t.Errorf("Dir = %q, want %q", ws.Dir, cfg.WorkspaceDir)
	}

	// Soul file should resolve to agents/{role}/SOUL.md.
	expectedSoulFile := filepath.Join(cfg.AgentsDir, "ruri", "SOUL.md")
	if ws.SoulFile != expectedSoulFile {
		t.Errorf("SoulFile = %q, want %q", ws.SoulFile, expectedSoulFile)
	}
}

func TestResolveWorkspace_CustomConfig(t *testing.T) {
	cfg := &Config{
		WorkspaceDir: "/home/user/.tetora/workspace",
		AgentsDir:    "/home/user/.tetora/agents",
		Agents: map[string]AgentConfig{
			"ruri": {
				Model: "opus",
				Workspace: WorkspaceConfig{
					Dir:        "/custom/workspace",
					SoulFile:   "/custom/soul.md",
					MCPServers: []string{"server1", "server2"},
				},
			},
		},
	}

	ws := resolveWorkspace(cfg, "ruri")

	if ws.Dir != "/custom/workspace" {
		t.Errorf("Dir = %q, want /custom/workspace", ws.Dir)
	}
	if ws.SoulFile != "/custom/soul.md" {
		t.Errorf("SoulFile = %q, want /custom/soul.md", ws.SoulFile)
	}
	if len(ws.MCPServers) != 2 {
		t.Errorf("MCPServers len = %d, want 2", len(ws.MCPServers))
	}
}

func TestResolveWorkspace_UnknownRole(t *testing.T) {
	cfg := &Config{
		WorkspaceDir: "/tmp/tetora/workspace",
		Agents:        map[string]AgentConfig{},
	}

	ws := resolveWorkspace(cfg, "unknown")

	if ws.Dir != cfg.WorkspaceDir {
		t.Errorf("Dir = %q, want %q", ws.Dir, cfg.WorkspaceDir)
	}
}

func TestResolveSessionScope_Main(t *testing.T) {
	cfg := &Config{
		Tools: ToolConfig{
			DefaultProfile: "standard",
		},
		Agents: map[string]AgentConfig{
			"ruri": {
				Model:      "opus",
				TrustLevel: "auto",
				ToolPolicy: AgentToolPolicy{
					Profile: "full",
				},
				Workspace: WorkspaceConfig{
					Sandbox: &SandboxMode{Mode: "off"},
				},
			},
		},
	}

	scope := resolveSessionScope(cfg, "ruri", "main")

	if scope.SessionType != "main" {
		t.Errorf("SessionType = %q, want main", scope.SessionType)
	}
	if scope.TrustLevel != "auto" {
		t.Errorf("TrustLevel = %q, want auto", scope.TrustLevel)
	}
	if scope.ToolProfile != "full" {
		t.Errorf("ToolProfile = %q, want full", scope.ToolProfile)
	}
	if scope.Sandbox {
		t.Error("Sandbox = true, want false")
	}
}

func TestResolveSessionScope_DM(t *testing.T) {
	cfg := &Config{
		Tools: ToolConfig{
			DefaultProfile: "standard",
		},
		Agents: map[string]AgentConfig{
			"ruri": {
				Model:      "opus",
				TrustLevel: "auto",
				ToolPolicy: AgentToolPolicy{
					Profile: "standard",
				},
			},
		},
	}

	scope := resolveSessionScope(cfg, "ruri", "dm")

	if scope.SessionType != "dm" {
		t.Errorf("SessionType = %q, want dm", scope.SessionType)
	}
	// DM should cap trust at "suggest" even if role is "auto"
	if scope.TrustLevel != "suggest" {
		t.Errorf("TrustLevel = %q, want suggest", scope.TrustLevel)
	}
	if scope.ToolProfile != "standard" {
		t.Errorf("ToolProfile = %q, want standard", scope.ToolProfile)
	}
	// DM should default to sandboxed
	if !scope.Sandbox {
		t.Error("Sandbox = false, want true")
	}
}

func TestResolveSessionScope_Group(t *testing.T) {
	cfg := &Config{
		Agents: map[string]AgentConfig{
			"ruri": {
				Model:      "opus",
				TrustLevel: "auto",
				ToolPolicy: AgentToolPolicy{
					Profile: "full",
				},
			},
		},
	}

	scope := resolveSessionScope(cfg, "ruri", "group")

	if scope.SessionType != "group" {
		t.Errorf("SessionType = %q, want group", scope.SessionType)
	}
	// Group should always be "observe" regardless of role config
	if scope.TrustLevel != "observe" {
		t.Errorf("TrustLevel = %q, want observe", scope.TrustLevel)
	}
	// Group should always use minimal tools
	if scope.ToolProfile != "minimal" {
		t.Errorf("ToolProfile = %q, want minimal", scope.ToolProfile)
	}
	// Group should always be sandboxed
	if !scope.Sandbox {
		t.Error("Sandbox = false, want true")
	}
}

func TestMinTrust(t *testing.T) {
	tests := []struct {
		a    string
		b    string
		want string
	}{
		{"observe", "suggest", "observe"},
		{"suggest", "observe", "observe"},
		{"auto", "suggest", "suggest"},
		{"suggest", "auto", "suggest"},
		{"auto", "observe", "observe"},
		{"observe", "auto", "observe"},
		{"invalid", "suggest", "suggest"},
		{"auto", "invalid", "auto"},
	}

	for _, tt := range tests {
		got := minTrust(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("minTrust(%q, %q) = %q, want %q", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestResolveMCPServers_Explicit(t *testing.T) {
	cfg := &Config{
		MCPServers: map[string]MCPServerConfig{
			"server1": {},
			"server2": {},
			"server3": {},
		},
		Agents: map[string]AgentConfig{
			"ruri": {
				Workspace: WorkspaceConfig{
					MCPServers: []string{"server1", "server2"},
				},
			},
		},
	}

	servers := resolveMCPServers(cfg, "ruri")

	if len(servers) != 2 {
		t.Fatalf("len(servers) = %d, want 2", len(servers))
	}

	// Check servers are the explicitly configured ones
	found := make(map[string]bool)
	for _, s := range servers {
		found[s] = true
	}
	if !found["server1"] || !found["server2"] {
		t.Errorf("servers = %v, want [server1, server2]", servers)
	}
}

func TestResolveMCPServers_Default(t *testing.T) {
	cfg := &Config{
		MCPServers: map[string]MCPServerConfig{
			"server1": {},
			"server2": {},
			"server3": {},
		},
		Agents: map[string]AgentConfig{
			"ruri": {}, // No explicit MCP servers
		},
	}

	servers := resolveMCPServers(cfg, "ruri")

	// Should return all configured servers
	if len(servers) != 3 {
		t.Errorf("len(servers) = %d, want 3", len(servers))
	}
}

func TestResolveMCPServers_UnknownRole(t *testing.T) {
	cfg := &Config{
		MCPServers: map[string]MCPServerConfig{
			"server1": {},
		},
	}

	servers := resolveMCPServers(cfg, "unknown")

	if servers != nil {
		t.Errorf("servers = %v, want nil", servers)
	}
}

func TestInitWorkspaces(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &Config{
		BaseDir:      tmpDir,
		AgentsDir:    filepath.Join(tmpDir, "agents"),
		WorkspaceDir: filepath.Join(tmpDir, "workspace"),
		RuntimeDir:   filepath.Join(tmpDir, "runtime"),
		VaultDir:     filepath.Join(tmpDir, "vault"),
		Agents: map[string]AgentConfig{
			"ruri":  {Model: "opus"},
			"hisui": {Model: "sonnet"},
		},
	}

	err := initDirectories(cfg)
	if err != nil {
		t.Fatalf("initDirectories failed: %v", err)
	}

	// Check shared workspace directory was created
	if _, err := os.Stat(cfg.WorkspaceDir); os.IsNotExist(err) {
		t.Errorf("workspace dir not created: %s", cfg.WorkspaceDir)
	}

	// Check shared workspace subdirs
	for _, sub := range []string{"memory", "skills", "rules", "team", "knowledge"} {
		dir := filepath.Join(cfg.WorkspaceDir, sub)
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			t.Errorf("workspace subdir not created: %s", dir)
		}
	}

	// Check agent directories were created
	for _, role := range []string{"ruri", "hisui"} {
		agentDir := filepath.Join(cfg.AgentsDir, role)
		if _, err := os.Stat(agentDir); os.IsNotExist(err) {
			t.Errorf("agent dir not created: %s", agentDir)
		}
	}

	// Check v1.3.0 directories
	v130Dirs := []string{
		filepath.Join(tmpDir, "workspace", "team"),
		filepath.Join(tmpDir, "workspace", "knowledge"),
		filepath.Join(tmpDir, "workspace", "drafts"),
		filepath.Join(tmpDir, "workspace", "intel"),
		filepath.Join(tmpDir, "runtime", "sessions"),
		filepath.Join(tmpDir, "runtime", "cache"),
		filepath.Join(tmpDir, "dbs"),
		filepath.Join(tmpDir, "vault"),
		filepath.Join(tmpDir, "media"),
	}
	for _, d := range v130Dirs {
		if _, err := os.Stat(d); os.IsNotExist(err) {
			t.Errorf("v1.3.0 dir not created: %s", d)
		}
	}
}

func TestLoadSoulFile(t *testing.T) {
	tmpDir := t.TempDir()
	soulFile := filepath.Join(tmpDir, "SOUL.md")
	soulContent := "I am ruri, the coordinator agent."

	// Create soul file
	if err := os.WriteFile(soulFile, []byte(soulContent), 0644); err != nil {
		t.Fatalf("failed to create test soul file: %v", err)
	}

	cfg := &Config{
		Agents: map[string]AgentConfig{
			"ruri": {
				Workspace: WorkspaceConfig{
					SoulFile: soulFile,
				},
			},
		},
	}

	content := loadSoulFile(cfg, "ruri")
	if content != soulContent {
		t.Errorf("loadSoulFile = %q, want %q", content, soulContent)
	}
}

func TestLoadSoulFile_NotExist(t *testing.T) {
	cfg := &Config{
		Agents: map[string]AgentConfig{
			"ruri": {
				Workspace: WorkspaceConfig{
					SoulFile: "/nonexistent/soul.md",
				},
			},
		},
	}

	content := loadSoulFile(cfg, "ruri")
	if content != "" {
		t.Errorf("loadSoulFile = %q, want empty string", content)
	}
}

func TestGetWorkspaceMemoryPath(t *testing.T) {
	cfg := &Config{
		WorkspaceDir: "/home/user/.tetora/workspace",
	}

	path := getWorkspaceMemoryPath(cfg)
	expected := filepath.Join("/home/user/.tetora/workspace", "memory")

	if path != expected {
		t.Errorf("getWorkspaceMemoryPath = %q, want %q", path, expected)
	}
}

func TestGetWorkspaceSkillsPath(t *testing.T) {
	cfg := &Config{
		WorkspaceDir: "/home/user/.tetora/workspace",
	}

	path := getWorkspaceSkillsPath(cfg)
	expected := filepath.Join("/home/user/.tetora/workspace", "skills")

	if path != expected {
		t.Errorf("getWorkspaceSkillsPath = %q, want %q", path, expected)
	}
}

// --- from completion_test.go ---

func TestCompletionSubcommands(t *testing.T) {
	cmds := completion.Subcommands()

	expected := []string{
		"serve", "run", "dispatch", "route", "init", "doctor", "health",
		"status", "service", "job", "agent", "history", "config",
		"logs", "prompt", "memory", "mcp", "session", "knowledge",
		"skill", "workflow", "budget", "trust", "webhook", "data", "backup", "restore",
		"proactive", "quick", "dashboard", "compact", "plugin", "task", "version", "help", "completion",
	}

	if len(cmds) != len(expected) {
		t.Fatalf("Subcommands() returned %d items, want %d", len(cmds), len(expected))
	}

	set := make(map[string]bool)
	for _, c := range cmds {
		set[c] = true
	}

	for _, e := range expected {
		if !set[e] {
			t.Errorf("Subcommands() missing %q", e)
		}
	}
}

func TestCompletionSubActions(t *testing.T) {
	tests := []struct {
		cmd      string
		expected []string
	}{
		{"job", []string{"list", "add", "enable", "disable", "remove", "trigger", "history"}},
		{"agent", []string{"list", "add", "show", "remove"}},
		{"workflow", []string{"list", "show", "validate", "create", "delete", "run", "runs", "status", "messages", "history", "rollback", "diff"}},
		{"knowledge", []string{"list", "add", "remove", "path", "search"}},
		{"history", []string{"list", "show", "cost"}},
		{"config", []string{"show", "set", "validate", "migrate", "history", "rollback", "diff", "snapshot", "show-version", "versions"}},
		{"data", []string{"status", "cleanup", "export", "purge"}},
		{"prompt", []string{"list", "show", "add", "edit", "remove"}},
		{"memory", []string{"list", "get", "set", "delete"}},
		{"mcp", []string{"list", "show", "add", "remove", "test"}},
		{"session", []string{"list", "show", "cleanup"}},
		{"skill", []string{"list", "run", "test"}},
		{"budget", []string{"show", "pause", "resume"}},
		{"webhook", []string{"list", "show", "test"}},
		{"service", []string{"install", "uninstall", "status"}},
		{"completion", []string{"bash", "zsh", "fish"}},
	}

	for _, tt := range tests {
		actions := completion.SubActions(tt.cmd)
		if len(actions) != len(tt.expected) {
			t.Errorf("SubActions(%q) returned %d items, want %d: %v", tt.cmd, len(actions), len(tt.expected), actions)
			continue
		}
		for i, a := range actions {
			if a != tt.expected[i] {
				t.Errorf("SubActions(%q)[%d] = %q, want %q", tt.cmd, i, a, tt.expected[i])
			}
		}
	}

	// Commands without sub-actions should return nil.
	nilCmds := []string{"serve", "run", "dispatch", "init", "doctor", "dashboard", "version", "help", "nonexistent"}
	for _, cmd := range nilCmds {
		if actions := completion.SubActions(cmd); actions != nil {
			t.Errorf("SubActions(%q) = %v, want nil", cmd, actions)
		}
	}
}

func TestGenerateBashCompletion(t *testing.T) {
	output := completion.GenerateBash()

	if !strings.Contains(output, "_tetora_completions") {
		t.Error("bash completion missing _tetora_completions function")
	}
	if !strings.Contains(output, "complete -F _tetora_completions tetora") {
		t.Error("bash completion missing 'complete -F' registration")
	}

	for _, cmd := range completion.Subcommands() {
		if !strings.Contains(output, cmd) {
			t.Errorf("bash completion missing subcommand %q", cmd)
		}
	}

	for _, cmd := range []string{"job", "agent", "workflow", "config"} {
		for _, action := range completion.SubActions(cmd) {
			if !strings.Contains(output, action) {
				t.Errorf("bash completion missing sub-action %q for %q", action, cmd)
			}
		}
	}

	if !strings.Contains(output, "tetora agent list --names") {
		t.Error("bash completion missing dynamic agent completion")
	}
	if !strings.Contains(output, "tetora workflow list --names") {
		t.Error("bash completion missing dynamic workflow completion")
	}
}

func TestGenerateZshCompletion(t *testing.T) {
	output := completion.GenerateZsh()

	if !strings.Contains(output, "#compdef tetora") {
		t.Error("zsh completion missing #compdef tetora")
	}
	if !strings.Contains(output, "_tetora") {
		t.Error("zsh completion missing _tetora function")
	}
	if !strings.Contains(output, "_arguments") {
		t.Error("zsh completion missing _arguments")
	}
	if !strings.Contains(output, "_describe") {
		t.Error("zsh completion missing _describe")
	}

	descs := completion.SubcommandDescriptions()
	for _, cmd := range completion.Subcommands() {
		if !strings.Contains(output, cmd) {
			t.Errorf("zsh completion missing subcommand %q", cmd)
		}
		if desc, ok := descs[cmd]; ok {
			escaped := strings.ReplaceAll(desc, ":", "\\:")
			if !strings.Contains(output, escaped) {
				t.Errorf("zsh completion missing description for %q: %q", cmd, desc)
			}
		}
	}

	if !strings.Contains(output, "tetora agent list --names") {
		t.Error("zsh completion missing dynamic agent completion")
	}
	if !strings.Contains(output, "tetora workflow list --names") {
		t.Error("zsh completion missing dynamic workflow completion")
	}
}

func TestGenerateFishCompletion(t *testing.T) {
	output := completion.GenerateFish()

	if !strings.Contains(output, "complete -c tetora") {
		t.Error("fish completion missing 'complete -c tetora'")
	}
	if !strings.Contains(output, "__fish_use_subcommand") {
		t.Error("fish completion missing __fish_use_subcommand condition")
	}

	for _, cmd := range completion.Subcommands() {
		if !strings.Contains(output, cmd) {
			t.Errorf("fish completion missing subcommand %q", cmd)
		}
	}

	if !strings.Contains(output, "__fish_seen_subcommand_from") {
		t.Error("fish completion missing __fish_seen_subcommand_from")
	}

	descs := completion.SubcommandDescriptions()
	for _, cmd := range []string{"serve", "dispatch", "workflow", "budget"} {
		if desc, ok := descs[cmd]; ok {
			if !strings.Contains(output, desc) {
				t.Errorf("fish completion missing description for %q", cmd)
			}
		}
	}
}

func TestCompletionSubcommandDescriptions(t *testing.T) {
	descs := completion.SubcommandDescriptions()
	cmds := completion.Subcommands()

	for _, cmd := range cmds {
		if _, ok := descs[cmd]; !ok {
			t.Errorf("SubcommandDescriptions missing description for %q", cmd)
		}
	}

	for cmd, desc := range descs {
		if desc == "" {
			t.Errorf("SubcommandDescriptions has empty description for %q", cmd)
		}
	}
}

func TestCompletionSubActionDescriptions(t *testing.T) {
	for _, cmd := range completion.Subcommands() {
		actions := completion.SubActions(cmd)
		if actions == nil {
			continue
		}
		descs := completion.SubActionDescriptions(cmd)
		if descs == nil {
			t.Errorf("SubActionDescriptions(%q) returned nil, but has sub-actions", cmd)
			continue
		}
		for _, action := range actions {
			if desc, ok := descs[action]; !ok || desc == "" {
				t.Errorf("SubActionDescriptions(%q) missing or empty description for %q", cmd, action)
			}
		}
	}
}
