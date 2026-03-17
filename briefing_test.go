package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bpkg "tetora/internal/automation/briefing"


	"tetora/internal/db"
)

// --- Test helpers ---

func setupBriefingTestDB(t *testing.T) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	// Create minimal tables that briefing queries against.
	ddl := `
CREATE TABLE IF NOT EXISTS reminders (
    id TEXT PRIMARY KEY,
    message TEXT NOT NULL,
    remind_at TEXT NOT NULL,
    status TEXT DEFAULT 'pending'
);
CREATE TABLE IF NOT EXISTS history (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    channel TEXT DEFAULT '',
    timestamp TEXT NOT NULL,
    message TEXT DEFAULT ''
);
`
	cmd := exec.Command("sqlite3", dbPath, ddl)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("init briefing test DB: %v: %s", err, string(out))
	}
	// Also init habits, user_tasks, goals, expenses tables.
	if err := initHabitsDB(dbPath); err != nil {
		t.Fatalf("initHabitsDB: %v", err)
	}
	if err := initTaskManagerDB(dbPath); err != nil {
		t.Fatalf("initTaskManagerDB: %v", err)
	}
	if err := initGoalsDB(dbPath); err != nil {
		t.Fatalf("initGoalsDB: %v", err)
	}
	if err := initFinanceDB(dbPath); err != nil {
		t.Fatalf("initFinanceDB: %v", err)
	}
	return dbPath
}

// setupBriefingService creates a briefing service with all optional globals cleared
// to nil for isolation. Callers that need a specific global must set it BEFORE calling
// this function so it gets captured into the service's deps.
func setupBriefingService(t *testing.T) (*bpkg.Service, string, func()) {
	t.Helper()
	dbPath := setupBriefingTestDB(t)
	cfg := &Config{HistoryDB: dbPath}

	// Save globals.
	oldScheduling := globalSchedulingService
	oldContacts := globalContactsService
	oldHabits := globalHabitsService
	oldGoals := globalGoalsService
	oldFinance := globalFinanceService
	oldTaskMgr := globalTaskManager
	oldInsights := globalInsightsEngine

	// Clear all globals for isolated test.
	globalSchedulingService = nil
	globalContactsService = nil
	globalHabitsService = nil
	globalGoalsService = nil
	globalFinanceService = nil
	globalTaskManager = nil
	globalInsightsEngine = nil

	svc := newBriefingService(cfg)

	cleanup := func() {
		globalSchedulingService = oldScheduling
		globalContactsService = oldContacts
		globalHabitsService = oldHabits
		globalGoalsService = oldGoals
		globalFinanceService = oldFinance
		globalTaskManager = oldTaskMgr
		globalInsightsEngine = oldInsights
	}

	return svc, dbPath, cleanup
}

// testBriefingAppCtx creates a context with an App containing the given BriefingService.
func testBriefingAppCtx(svc *bpkg.Service) context.Context {
	app := &App{Briefing: svc}
	return withApp(context.Background(), app)
}

func briefingExecSQL(t *testing.T, dbPath, sql string) {
	t.Helper()
	cmd := exec.Command("sqlite3", dbPath, sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("briefingExecSQL: %v: %s", err, string(out))
	}
}

// --- Constructor ---

func TestNewBriefingService(t *testing.T) {
	cfg := &Config{HistoryDB: "/tmp/test.db"}
	svc := newBriefingService(cfg)
	if svc == nil {
		t.Fatal("expected non-nil service")
	}
	if svc.DBPath() != cfg.HistoryDB {
		t.Errorf("expected dbPath %s, got %s", cfg.HistoryDB, svc.DBPath())
	}
}

// --- Greeting tests ---

func TestMorningGreeting(t *testing.T) {
	svc := bpkg.New("", bpkg.Deps{})

	tests := []struct {
		name string
		hour int
		want string
	}{
		{"early_bird", 4, "Early bird!"},
		{"morning", 8, "Good morning!"},
		{"afternoon", 14, "Hello!"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			date := time.Date(2026, 2, 23, tt.hour, 0, 0, 0, time.UTC)
			greeting := svc.MorningGreeting(date)
			if !strings.Contains(greeting, tt.want) {
				t.Errorf("MorningGreeting(%d:00) = %q, want to contain %q", tt.hour, greeting, tt.want)
			}
			// Should always contain the weekday and formatted date.
			if !strings.Contains(greeting, "Monday") {
				t.Errorf("greeting %q should contain weekday", greeting)
			}
			if !strings.Contains(greeting, "February 23, 2026") {
				t.Errorf("greeting %q should contain formatted date", greeting)
			}
		})
	}
}

func TestEveningGreeting(t *testing.T) {
	svc := bpkg.New("", bpkg.Deps{})
	date := time.Date(2026, 2, 23, 20, 0, 0, 0, time.UTC) // Monday
	greeting := svc.EveningGreeting(date)
	if !strings.Contains(greeting, "Good evening!") {
		t.Errorf("EveningGreeting = %q, want to contain 'Good evening!'", greeting)
	}
	if !strings.Contains(greeting, "Monday") {
		t.Errorf("EveningGreeting = %q, want to contain weekday", greeting)
	}
}

// --- Morning/Evening with no services ---

func TestGenerateMorning_NoServices(t *testing.T) {
	svc, _, cleanup := setupBriefingService(t)
	defer cleanup()

	date := time.Date(2026, 2, 23, 8, 0, 0, 0, time.UTC)
	briefing, err := svc.GenerateMorning(date)
	if err != nil {
		t.Fatalf("GenerateMorning: %v", err)
	}
	if briefing.Type != "morning" {
		t.Errorf("expected type morning, got %s", briefing.Type)
	}
	if briefing.Date != "2026-02-23" {
		t.Errorf("expected date 2026-02-23, got %s", briefing.Date)
	}
	if briefing.Greeting == "" {
		t.Error("expected non-empty greeting")
	}
	// With no services and no data, sections should be empty.
	if len(briefing.Sections) != 0 {
		t.Errorf("expected 0 sections with no services, got %d", len(briefing.Sections))
	}
	if briefing.Quote == "" {
		t.Error("expected non-empty quote")
	}
	if briefing.GeneratedAt == "" {
		t.Error("expected non-empty generated_at")
	}
}

func TestGenerateEvening_NoServices(t *testing.T) {
	svc, _, cleanup := setupBriefingService(t)
	defer cleanup()

	date := time.Date(2026, 2, 23, 20, 0, 0, 0, time.UTC)
	briefing, err := svc.GenerateEvening(date)
	if err != nil {
		t.Fatalf("GenerateEvening: %v", err)
	}
	if briefing.Type != "evening" {
		t.Errorf("expected type evening, got %s", briefing.Type)
	}
	if briefing.Date != "2026-02-23" {
		t.Errorf("expected date 2026-02-23, got %s", briefing.Date)
	}
	if briefing.Greeting == "" {
		t.Error("expected non-empty greeting")
	}
	if len(briefing.Sections) != 0 {
		t.Errorf("expected 0 sections with no services, got %d", len(briefing.Sections))
	}
	if briefing.Quote == "" {
		t.Error("expected non-empty reflection prompt")
	}
}

// --- FormatBriefing ---

func TestFormatBriefing(t *testing.T) {
	briefing := &bpkg.Briefing{
		Type:     "morning",
		Date:     "2026-02-23",
		Greeting: "Good morning! It's Monday, February 23, 2026.",
		Sections: []bpkg.BriefingSection{
			{
				Title:   "Today's Schedule",
				Icon:    "calendar",
				Items:   []string{"09:00 -- Standup", "14:00 -- Review"},
				Summary: "2 events today",
			},
			{
				Title:   "Tasks Due Today",
				Icon:    "check",
				Items:   []string{"[URGENT] Fix bug"},
				Summary: "1 tasks due",
			},
		},
		Quote:       "The secret of getting ahead is getting started. -- Mark Twain",
		GeneratedAt: "2026-02-23T08:00:00Z",
	}

	output := bpkg.FormatBriefing(briefing)

	// Check header.
	if !strings.Contains(output, "## Morning Briefing -- 2026-02-23") {
		t.Errorf("missing header in output:\n%s", output)
	}
	if !strings.Contains(output, "Good morning!") {
		t.Errorf("missing greeting in output:\n%s", output)
	}
	// Check sections.
	if !strings.Contains(output, "### calendar Today's Schedule") {
		t.Errorf("missing schedule section in output:\n%s", output)
	}
	if !strings.Contains(output, "- 09:00 -- Standup") {
		t.Errorf("missing schedule item in output:\n%s", output)
	}
	if !strings.Contains(output, "*2 events today*") {
		t.Errorf("missing summary in output:\n%s", output)
	}
	if !strings.Contains(output, "[URGENT] Fix bug") {
		t.Errorf("missing task item in output:\n%s", output)
	}
	// Check quote (morning = blockquote).
	if !strings.Contains(output, "> The secret of getting ahead") {
		t.Errorf("missing quote in output:\n%s", output)
	}
}

func TestFormatBriefing_Evening(t *testing.T) {
	briefing := &bpkg.Briefing{
		Type:     "evening",
		Date:     "2026-02-23",
		Greeting: "Good evening! Here's your Monday wrap-up.",
		Quote:    "What was the best part of your day?",
	}

	output := bpkg.FormatBriefing(briefing)

	if !strings.Contains(output, "## Evening Briefing -- 2026-02-23") {
		t.Errorf("missing header in output:\n%s", output)
	}
	// Evening uses **Reflection:** prefix.
	if !strings.Contains(output, "**Reflection:** What was the best part") {
		t.Errorf("missing reflection in output:\n%s", output)
	}
}

func TestFormatBriefing_EmptySections(t *testing.T) {
	briefing := &bpkg.Briefing{
		Type:     "morning",
		Date:     "2026-01-01",
		Greeting: "Hello!",
	}
	output := bpkg.FormatBriefing(briefing)
	if !strings.Contains(output, "## Morning Briefing") {
		t.Errorf("missing header in empty briefing output:\n%s", output)
	}
	if !strings.Contains(output, "Hello!") {
		t.Errorf("missing greeting in empty briefing output:\n%s", output)
	}
}

// --- Quote / Reflection variation ---

func TestDailyQuote_DifferentDays(t *testing.T) {
	svc := bpkg.New("", bpkg.Deps{})
	seen := make(map[string]bool)
	for day := 1; day <= 7; day++ {
		date := time.Date(2026, 1, day, 8, 0, 0, 0, time.UTC)
		q := svc.DailyQuote(date)
		if q == "" {
			t.Errorf("empty quote for day %d", day)
		}
		seen[q] = true
	}
	if len(seen) < 2 {
		t.Errorf("expected different quotes for different days, got %d unique", len(seen))
	}
}

func TestEveningReflection_DifferentDays(t *testing.T) {
	svc := bpkg.New("", bpkg.Deps{})
	seen := make(map[string]bool)
	for day := 1; day <= 7; day++ {
		date := time.Date(2026, 1, day, 20, 0, 0, 0, time.UTC)
		p := svc.EveningReflection(date)
		if p == "" {
			t.Errorf("empty reflection for day %d", day)
		}
		seen[p] = true
	}
	if len(seen) < 2 {
		t.Errorf("expected different reflections for different days, got %d unique", len(seen))
	}
}

// --- capitalizeFirst ---

func TestCapitalizeFirst(t *testing.T) {
	tests := []struct{ in, want string }{
		{"morning", "Morning"},
		{"evening", "Evening"},
		{"", ""},
		{"a", "A"},
		{"ABC", "ABC"},
	}
	for _, tt := range tests {
		got := bpkg.CapitalizeFirst(tt.in)
		if got != tt.want {
			t.Errorf("bpkg.CapitalizeFirst(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// --- Tool handler tests ---

func TestToolBriefingMorning_NotInitialized(t *testing.T) {
	ctx := withApp(context.Background(), &App{})
	_, err := toolBriefingMorning(ctx, &Config{}, json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error when service is nil")
	}
	if !strings.Contains(err.Error(), "not initialized") {
		t.Errorf("expected 'not initialized' error, got: %v", err)
	}
}

func TestToolBriefingEvening_NotInitialized(t *testing.T) {
	ctx := withApp(context.Background(), &App{})
	_, err := toolBriefingEvening(ctx, &Config{}, json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error when service is nil")
	}
	if !strings.Contains(err.Error(), "not initialized") {
		t.Errorf("expected 'not initialized' error, got: %v", err)
	}
}

func TestToolBriefingMorning(t *testing.T) {
	svc, _, cleanup := setupBriefingService(t)
	defer cleanup()

	ctx := testBriefingAppCtx(svc)
	result, err := toolBriefingMorning(ctx, &Config{}, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("toolBriefingMorning: %v", err)
	}
	if result == "" {
		t.Error("expected non-empty result")
	}
	if !strings.Contains(result, "Morning Briefing") {
		t.Errorf("result should contain 'Morning Briefing', got:\n%s", result)
	}
}

func TestToolBriefingEvening(t *testing.T) {
	svc, _, cleanup := setupBriefingService(t)
	defer cleanup()

	ctx := testBriefingAppCtx(svc)
	result, err := toolBriefingEvening(ctx, &Config{}, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("toolBriefingEvening: %v", err)
	}
	if result == "" {
		t.Error("expected non-empty result")
	}
	if !strings.Contains(result, "Evening Briefing") {
		t.Errorf("result should contain 'Evening Briefing', got:\n%s", result)
	}
	if !strings.Contains(result, "Reflection:") {
		t.Errorf("result should contain reflection prompt, got:\n%s", result)
	}
}

func TestToolBriefingMorning_WithDate(t *testing.T) {
	svc, _, cleanup := setupBriefingService(t)
	defer cleanup()

	ctx := testBriefingAppCtx(svc)
	result, err := toolBriefingMorning(ctx, &Config{}, json.RawMessage(`{"date":"2026-03-15"}`))
	if err != nil {
		t.Fatalf("toolBriefingMorning with date: %v", err)
	}
	if !strings.Contains(result, "2026-03-15") {
		t.Errorf("result should contain specified date, got:\n%s", result)
	}
}

func TestToolBriefingMorning_InvalidDate(t *testing.T) {
	svc, _, cleanup := setupBriefingService(t)
	defer cleanup()

	ctx := testBriefingAppCtx(svc)
	_, err := toolBriefingMorning(ctx, &Config{}, json.RawMessage(`{"date":"bad-date"}`))
	if err == nil {
		t.Fatal("expected error for invalid date")
	}
	if !strings.Contains(err.Error(), "invalid date format") {
		t.Errorf("expected 'invalid date format' error, got: %v", err)
	}
}

func TestToolBriefingEvening_WithDate(t *testing.T) {
	svc, _, cleanup := setupBriefingService(t)
	defer cleanup()

	ctx := testBriefingAppCtx(svc)
	result, err := toolBriefingEvening(ctx, &Config{}, json.RawMessage(`{"date":"2026-03-15"}`))
	if err != nil {
		t.Fatalf("toolBriefingEvening with date: %v", err)
	}
	if !strings.Contains(result, "2026-03-15") {
		t.Errorf("result should contain specified date, got:\n%s", result)
	}
}

func TestToolBriefingMorning_InvalidJSON(t *testing.T) {
	svc, _, cleanup := setupBriefingService(t)
	defer cleanup()

	ctx := testBriefingAppCtx(svc)
	_, err := toolBriefingMorning(ctx, &Config{}, json.RawMessage(`{invalid`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestToolBriefingEvening_InvalidJSON(t *testing.T) {
	svc, _, cleanup := setupBriefingService(t)
	defer cleanup()

	ctx := testBriefingAppCtx(svc)
	_, err := toolBriefingEvening(ctx, &Config{}, json.RawMessage(`{invalid`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// --- Section-level tests ---

func TestScheduleSection_NilService(t *testing.T) {
	svc := bpkg.New("/tmp/test.db", bpkg.Deps{
		Query:  db.Query,
		Escape: db.Escape,
		// ViewSchedule is nil by default
	})
	sec := svc.ScheduleSection("2026-02-23")
	if sec != nil {
		t.Error("expected nil when ViewSchedule is nil")
	}
}

func TestRemindersSection_NoDB(t *testing.T) {
	svc := bpkg.New("", bpkg.Deps{Query: db.Query, Escape: db.Escape})
	sec := svc.RemindersSection("2026-02-23")
	if sec != nil {
		t.Error("expected nil when dbPath is empty")
	}
}

func TestTasksSection_NilService(t *testing.T) {
	svc := bpkg.New("/tmp/test.db", bpkg.Deps{
		Query:          db.Query,
		Escape:         db.Escape,
		TasksAvailable: false,
	})
	sec := svc.TasksSection("2026-02-23")
	if sec != nil {
		t.Error("expected nil when TasksAvailable is false")
	}
}

func TestHabitsSection_NilService(t *testing.T) {
	svc := bpkg.New("/tmp/test.db", bpkg.Deps{
		Query:           db.Query,
		Escape:          db.Escape,
		HabitsAvailable: false,
	})
	sec := svc.HabitsSection("2026-02-23", time.Monday)
	if sec != nil {
		t.Error("expected nil when HabitsAvailable is false")
	}
}

func TestGoalsSection_NilService(t *testing.T) {
	svc := bpkg.New("/tmp/test.db", bpkg.Deps{
		Query:          db.Query,
		Escape:         db.Escape,
		GoalsAvailable: false,
	})
	sec := svc.GoalsSection("2026-02-23")
	if sec != nil {
		t.Error("expected nil when GoalsAvailable is false")
	}
}

func TestContactsSection_NilService(t *testing.T) {
	svc := bpkg.New("/tmp/test.db", bpkg.Deps{
		Query:  db.Query,
		Escape: db.Escape,
		// GetUpcomingEvents is nil by default
	})
	sec := svc.ContactsSection()
	if sec != nil {
		t.Error("expected nil when GetUpcomingEvents is nil")
	}
}

func TestSpendingSection_NilService(t *testing.T) {
	svc := bpkg.New("/tmp/test.db", bpkg.Deps{
		Query:            db.Query,
		Escape:           db.Escape,
		FinanceAvailable: false,
	})
	sec := svc.SpendingSection("2026-02-23")
	if sec != nil {
		t.Error("expected nil when FinanceAvailable is false")
	}
}

func TestDaySummarySection_NoDB(t *testing.T) {
	svc := bpkg.New("", bpkg.Deps{Query: db.Query, Escape: db.Escape})
	sec := svc.DaySummarySection("2026-02-23")
	if sec != nil {
		t.Error("expected nil when dbPath is empty")
	}
}

// --- Data-driven section tests ---

func TestRemindersSection_WithData(t *testing.T) {
	svc, dbPath, cleanup := setupBriefingService(t)
	defer cleanup()

	today := time.Now().UTC().Format("2006-01-02")
	remindAt := today + "T10:30:00Z"
	briefingExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO reminders (id, message, remind_at, status) VALUES ('r1', 'Buy groceries', '%s', 'pending')`,
		remindAt))
	briefingExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO reminders (id, message, remind_at, status) VALUES ('r2', 'Call dentist', '%s', 'pending')`,
		today+"T14:00:00Z"))

	sec := svc.RemindersSection(today)
	if sec == nil {
		t.Fatal("expected non-nil section")
	}
	if sec.Title != "Reminders" {
		t.Errorf("expected title 'Reminders', got %q", sec.Title)
	}
	if len(sec.Items) != 2 {
		t.Errorf("expected 2 items, got %d", len(sec.Items))
	}
	if !strings.Contains(sec.Summary, "2 reminders") {
		t.Errorf("expected summary to mention 2 reminders, got %q", sec.Summary)
	}
}

func TestTasksSection_WithData(t *testing.T) {
	dbPath := setupBriefingTestDB(t)

	// Set global BEFORE creating service so TasksAvailable=true is captured.
	oldTaskMgr := globalTaskManager
	globalTaskManager = newTaskManagerService(&Config{HistoryDB: dbPath})
	defer func() { globalTaskManager = oldTaskMgr }()

	svc := newBriefingService(&Config{HistoryDB: dbPath})

	today := time.Now().UTC().Format("2006-01-02")
	dueAt := today + "T23:59:59Z"
	now := time.Now().UTC().Format(time.RFC3339)
	briefingExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO user_tasks (id, user_id, title, status, priority, due_at, parent_id, tags, created_at, updated_at)
		 VALUES ('t1', 'default', 'Urgent task', 'todo', 1, '%s', '', '[]', '%s', '%s')`,
		dueAt, now, now))
	briefingExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO user_tasks (id, user_id, title, status, priority, due_at, parent_id, tags, created_at, updated_at)
		 VALUES ('t2', 'default', 'Normal task', 'todo', 3, '%s', '', '[]', '%s', '%s')`,
		dueAt, now, now))

	sec := svc.TasksSection(today)
	if sec == nil {
		t.Fatal("expected non-nil section")
	}
	if len(sec.Items) != 2 {
		t.Errorf("expected 2 items, got %d", len(sec.Items))
	}
	// First task is urgent.
	found := false
	for _, item := range sec.Items {
		if strings.Contains(item, "[URGENT]") && strings.Contains(item, "Urgent task") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected [URGENT] prefix for priority 1 task, items: %v", sec.Items)
	}
}

func TestHabitsSection_WithData(t *testing.T) {
	dbPath := setupBriefingTestDB(t)

	// Set global BEFORE creating service so HabitsAvailable=true is captured.
	oldHabits := globalHabitsService
	globalHabitsService = newHabitsService(&Config{HistoryDB: dbPath})
	defer func() { globalHabitsService = oldHabits }()

	svc := newBriefingService(&Config{HistoryDB: dbPath})

	now := time.Now().UTC().Format(time.RFC3339)
	today := time.Now().UTC().Format("2006-01-02")
	briefingExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO habits (id, name, frequency, target_count, archived_at, created_at, scope)
		 VALUES ('h1', 'Morning Run', 'daily', 1, '', '%s', '')`, now))
	briefingExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO habits (id, name, frequency, target_count, archived_at, created_at, scope)
		 VALUES ('h2', 'Read', 'daily', 1, '', '%s', '')`, now))
	// Log completion for h1.
	briefingExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO habit_logs (id, habit_id, logged_at, value)
		 VALUES ('l1', 'h1', '%s', 1.0)`, now))

	sec := svc.HabitsSection(today, time.Now().UTC().Weekday())
	if sec == nil {
		t.Fatal("expected non-nil section")
	}
	if len(sec.Items) != 2 {
		t.Errorf("expected 2 items, got %d: %v", len(sec.Items), sec.Items)
	}
	// h1 should be done, h2 should be todo.
	doneFound := false
	todoFound := false
	for _, item := range sec.Items {
		if strings.Contains(item, "[done]") && strings.Contains(item, "Morning Run") {
			doneFound = true
		}
		if strings.Contains(item, "[todo]") && strings.Contains(item, "Read") {
			todoFound = true
		}
	}
	if !doneFound {
		t.Errorf("expected [done] for Morning Run, items: %v", sec.Items)
	}
	if !todoFound {
		t.Errorf("expected [todo] for Read, items: %v", sec.Items)
	}
	if !strings.Contains(sec.Summary, "1 pending") {
		t.Errorf("summary should contain '1 pending', got %q", sec.Summary)
	}
}

func TestHabitsSection_WeeklyOnNonMonday(t *testing.T) {
	dbPath := setupBriefingTestDB(t)

	// Set global BEFORE creating service so HabitsAvailable=true is captured.
	oldHabits := globalHabitsService
	globalHabitsService = newHabitsService(&Config{HistoryDB: dbPath})
	defer func() { globalHabitsService = oldHabits }()

	svc := newBriefingService(&Config{HistoryDB: dbPath})

	now := time.Now().UTC().Format(time.RFC3339)
	today := time.Now().UTC().Format("2006-01-02")
	// Only a weekly habit, no daily.
	briefingExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO habits (id, name, frequency, target_count, archived_at, created_at, scope)
		 VALUES ('h1', 'Weekly Review', 'weekly', 1, '', '%s', '')`, now))

	// On a non-Monday, weekly habits should be filtered out.
	sec := svc.HabitsSection(today, time.Wednesday)
	if sec != nil {
		// Should be nil because only weekly habits, and it's not Monday.
		t.Errorf("expected nil section on non-Monday for weekly-only habits, got %v", sec.Items)
	}
}

func TestGoalsSection_WithData(t *testing.T) {
	dbPath := setupBriefingTestDB(t)

	// Set global BEFORE creating service so GoalsAvailable=true is captured.
	oldGoals := globalGoalsService
	globalGoalsService = newGoalsService(&Config{HistoryDB: dbPath})
	defer func() { globalGoalsService = oldGoals }()

	svc := newBriefingService(&Config{HistoryDB: dbPath})

	now := time.Now().UTC().Format(time.RFC3339)
	today := time.Now().UTC().Format("2006-01-02")
	// Goal with deadline in 3 days.
	deadline := time.Now().UTC().Add(3 * 24 * time.Hour).Format("2006-01-02")
	briefingExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO goals (id, user_id, title, status, target_date, milestones, review_notes, created_at, updated_at)
		 VALUES ('g1', 'default', 'Ship feature', 'active', '%s', '[]', '[]', '%s', '%s')`,
		deadline, now, now))

	sec := svc.GoalsSection(today)
	if sec == nil {
		t.Fatal("expected non-nil section")
	}
	if len(sec.Items) != 1 {
		t.Errorf("expected 1 goal item, got %d", len(sec.Items))
	}
	if !strings.Contains(sec.Items[0], "Ship feature") {
		t.Errorf("expected item to contain 'Ship feature', got %q", sec.Items[0])
	}
}

func TestSpendingSection_WithData(t *testing.T) {
	dbPath := setupBriefingTestDB(t)

	// Set global BEFORE creating service so FinanceAvailable=true is captured.
	oldFinance := globalFinanceService
	globalFinanceService = newFinanceService(&Config{HistoryDB: dbPath})
	defer func() { globalFinanceService = oldFinance }()

	svc := newBriefingService(&Config{HistoryDB: dbPath})

	today := time.Now().UTC().Format("2006-01-02")
	now := time.Now().UTC().Format(time.RFC3339)
	briefingExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO expenses (user_id, amount, currency, category, description, tags, date, created_at)
		 VALUES ('default', 350, 'TWD', 'food', 'lunch', '[]', '%s', '%s')`,
		today, now))
	briefingExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO expenses (user_id, amount, currency, category, description, tags, date, created_at)
		 VALUES ('default', 200, 'TWD', 'transport', 'taxi', '[]', '%s', '%s')`,
		today, now))

	sec := svc.SpendingSection(today)
	if sec == nil {
		t.Fatal("expected non-nil section")
	}
	if len(sec.Items) != 2 {
		t.Errorf("expected 2 categories, got %d: %v", len(sec.Items), sec.Items)
	}
	if !strings.Contains(sec.Summary, "550") {
		t.Errorf("expected total 550 in summary, got %q", sec.Summary)
	}
}

func TestTasksCompletedSection_WithData(t *testing.T) {
	dbPath := setupBriefingTestDB(t)

	// Set global BEFORE creating service so TasksAvailable=true is captured.
	oldTaskMgr := globalTaskManager
	globalTaskManager = newTaskManagerService(&Config{HistoryDB: dbPath})
	defer func() { globalTaskManager = oldTaskMgr }()

	svc := newBriefingService(&Config{HistoryDB: dbPath})

	today := time.Now().UTC().Format("2006-01-02")
	now := time.Now().UTC().Format(time.RFC3339)
	briefingExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO user_tasks (id, user_id, title, status, priority, due_at, parent_id, tags, created_at, updated_at, completed_at)
		 VALUES ('t1', 'default', 'Done task', 'done', 2, '', '', '[]', '%s', '%s', '%s')`,
		now, now, now))

	sec := svc.TasksCompletedSection(today)
	if sec == nil {
		t.Fatal("expected non-nil section")
	}
	if len(sec.Items) != 1 {
		t.Errorf("expected 1 item, got %d", len(sec.Items))
	}
	if sec.Items[0] != "Done task" {
		t.Errorf("expected 'Done task', got %q", sec.Items[0])
	}
}

func TestDaySummarySection_WithData(t *testing.T) {
	svc, dbPath, cleanup := setupBriefingService(t)
	defer cleanup()

	today := time.Now().UTC().Format("2006-01-02")
	now := time.Now().UTC().Format(time.RFC3339)
	briefingExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO history (channel, timestamp, message)
		 VALUES ('discord', '%s', 'hello')`, now))
	briefingExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO history (channel, timestamp, message)
		 VALUES ('discord', '%s', 'world')`, now))
	briefingExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO history (channel, timestamp, message)
		 VALUES ('line', '%s', 'hi')`, now))

	sec := svc.DaySummarySection(today)
	if sec == nil {
		t.Fatal("expected non-nil section")
	}
	if sec.Title != "Day Summary" {
		t.Errorf("expected title 'Day Summary', got %q", sec.Title)
	}
	if !strings.Contains(sec.Summary, "3 total interactions") {
		t.Errorf("expected 3 total interactions, got %q", sec.Summary)
	}
}

func TestTomorrowPreviewSection_NoData(t *testing.T) {
	svc, _, cleanup := setupBriefingService(t)
	defer cleanup()

	tomorrow := time.Now().UTC().Add(24 * time.Hour)
	sec := svc.TomorrowPreviewSection(tomorrow)
	// No scheduling service and no tasks -> nil.
	if sec != nil {
		t.Errorf("expected nil with no data, got %v", sec)
	}
}

// --- Full integration: morning with data ---

func TestGenerateMorning_WithReminders(t *testing.T) {
	svc, dbPath, cleanup := setupBriefingService(t)
	defer cleanup()

	today := time.Now().UTC().Format("2006-01-02")
	remindAt := today + "T09:00:00Z"
	briefingExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO reminders (id, message, remind_at, status) VALUES ('r1', 'Team meeting', '%s', 'pending')`,
		remindAt))

	briefing, err := svc.GenerateMorning(time.Now().UTC())
	if err != nil {
		t.Fatalf("GenerateMorning: %v", err)
	}

	// Should have at least the reminders section.
	found := false
	for _, sec := range briefing.Sections {
		if sec.Title == "Reminders" {
			found = true
			if len(sec.Items) != 1 {
				t.Errorf("expected 1 reminder, got %d", len(sec.Items))
			}
		}
	}
	if !found {
		t.Error("expected Reminders section in morning briefing")
	}
}

// --- Full integration: evening with data ---

func TestGenerateEvening_WithHistory(t *testing.T) {
	svc, dbPath, cleanup := setupBriefingService(t)
	defer cleanup()

	now := time.Now().UTC().Format(time.RFC3339)
	briefingExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO history (channel, timestamp, message) VALUES ('slack', '%s', 'test')`, now))

	briefing, err := svc.GenerateEvening(time.Now().UTC())
	if err != nil {
		t.Fatalf("GenerateEvening: %v", err)
	}

	found := false
	for _, sec := range briefing.Sections {
		if sec.Title == "Day Summary" {
			found = true
			if len(sec.Items) != 1 {
				t.Errorf("expected 1 channel in summary, got %d", len(sec.Items))
			}
		}
	}
	if !found {
		t.Error("expected Day Summary section in evening briefing")
	}
}

// --- Briefing serialization ---

func TestBriefingJSON(t *testing.T) {
	briefing := &bpkg.Briefing{
		Type:     "morning",
		Date:     "2026-02-23",
		Greeting: "Hello",
		Sections: []bpkg.BriefingSection{
			{Title: "Test", Icon: "star", Items: []string{"item1"}, Summary: "1 item"},
		},
		Quote:       "A quote",
		GeneratedAt: "2026-02-23T08:00:00Z",
	}

	data, err := json.Marshal(briefing)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded bpkg.Briefing
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Type != "morning" {
		t.Errorf("expected type morning, got %s", decoded.Type)
	}
	if len(decoded.Sections) != 1 {
		t.Errorf("expected 1 section, got %d", len(decoded.Sections))
	}
	if decoded.Sections[0].Items[0] != "item1" {
		t.Errorf("expected item1, got %s", decoded.Sections[0].Items[0])
	}
}
