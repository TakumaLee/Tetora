package main

// wire_life.go wires the life service internal packages to the root package
// by providing constructors and type aliases that keep the root API surface stable.

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"tetora/internal/log"
	"tetora/internal/db"
	"tetora/internal/nlp"
	"tetora/internal/tool"

	"tetora/internal/life/calendar"
	"tetora/internal/life/contacts"
	"tetora/internal/life/dailynotes"
	"tetora/internal/life/family"
	"tetora/internal/life/finance"
	"tetora/internal/life/goals"
	"tetora/internal/life/habits"
	"tetora/internal/life/lifedb"
	"tetora/internal/life/pricewatch"
	"tetora/internal/life/profile"
	"tetora/internal/life/reminder"
	"tetora/internal/life/tasks"
	"tetora/internal/life/timetracking"
)

// --- Service type aliases ---

type UserProfileService = profile.Service
type UserProfile = profile.UserProfile
type ChannelIdentity = profile.ChannelIdentity
type UserPreference = profile.UserPreference

type TaskManagerService = tasks.Service
type UserTask = tasks.UserTask
type TaskProject = tasks.TaskProject
type TaskReview = tasks.TaskReview
type TaskFilter = tasks.TaskFilter
type TodoistTask = tasks.TodoistTask

type FinanceService = finance.Service
type HabitsService = habits.Service
type GoalsService = goals.Service
type CalendarService = calendar.Service
type ContactsService = contacts.Service
type FamilyService = family.Service
type PriceWatchEngine = pricewatch.Service
type ReminderEngine = reminder.Engine
type TimeTrackingService = timetracking.Service
type DailyNotesService = dailynotes.Service

// --- Data type aliases ---

// Finance types
type Expense = finance.Expense
type Budget = finance.Budget
type ExpenseReport = finance.ExpenseReport
type ExpenseBudgetStatus = finance.ExpenseBudgetStatus
type PriceWatch = pricewatch.PriceWatch

// Goals types
type Goal = goals.Goal
type Milestone = goals.Milestone
type ReviewNote = goals.ReviewNote

// Contacts types
type Contact = contacts.Contact
type ContactInteraction = contacts.ContactInteraction

// Family types
type FamilyUser = family.FamilyUser
type SharedList = family.SharedList
type SharedListItem = family.SharedListItem

// Calendar types
type CalendarEvent = calendar.Event
type CalendarEventInput = calendar.EventInput

// TimeTracking types
type TimeEntry = timetracking.TimeEntry
type TimeReport = timetracking.TimeReport
type ActivitySummary = timetracking.ActivitySummary

// Reminder types
type Reminder = reminder.Reminder

// --- makeLifeDB ---

// makeLifeDB returns a lifedb.DB wired to the root package helpers.
func makeLifeDB() lifedb.DB {
	return lifedb.DB{
		Query:   db.Query,
		Exec:    db.Exec,
		Escape:  db.Escape,
		LogInfo: log.Info,
		LogWarn: log.Warn,
	}
}

// --- Constructors ---

func newFinanceService(cfg *Config) *FinanceService {
	encFn := func(v string) string { return encryptField(cfg, v) }
	decFn := func(v string) string { return decryptField(cfg, v) }
	return finance.New(cfg.HistoryDB, cfg.Finance.DefaultCurrencyOrTWD(), makeLifeDB(), encFn, decFn)
}

func initFinanceDB(dbPath string) error {
	return finance.InitDB(dbPath)
}

func newHabitsService(cfg *Config) *HabitsService {
	return habits.New(cfg.HistoryDB, makeLifeDB())
}

func initHabitsDB(dbPath string) error {
	return habits.InitDB(dbPath)
}

func newGoalsService(cfg *Config) *GoalsService {
	return goals.New(cfg.HistoryDB, makeLifeDB())
}

func initGoalsDB(dbPath string) error {
	return goals.InitDB(dbPath)
}

func newCalendarService(cfg *Config) *CalendarService {
	var oauth calendar.OAuthRequester
	if globalOAuthManager != nil {
		oauth = &oauthAdapter{mgr: globalOAuthManager}
	}
	return calendar.New(
		cfg.Calendar.CalendarID,
		cfg.Calendar.TimeZone,
		cfg.Calendar.MaxResults,
		oauth,
	)
}

func newContactsService(cfg *Config) *ContactsService {
	dbPath := filepath.Join(filepath.Dir(cfg.HistoryDB), "contacts.db")
	if err := contacts.InitDB(dbPath); err != nil {
		log.Error("contacts service init failed", "error", err)
		return nil
	}
	encFn := func(v string) string { return encryptField(cfg, v) }
	decFn := func(v string) string { return decryptField(cfg, v) }
	log.Info("contacts service initialized", "db", dbPath)
	return contacts.New(dbPath, makeLifeDB(), encFn, decFn)
}

func initContactsDB(dbPath string) error {
	return contacts.InitDB(dbPath)
}

func newFamilyService(cfg *Config, familyCfg FamilyConfig) (*FamilyService, error) {
	dbPath := filepath.Join(filepath.Dir(cfg.HistoryDB), "family.db")
	internalCfg := family.Config{
		MaxUsers:         familyCfg.MaxUsers,
		DefaultBudget:    familyCfg.DefaultBudget,
		DefaultRateLimit: familyCfg.DefaultRateLimit,
	}
	return family.New(dbPath, cfg.HistoryDB, internalCfg, makeLifeDB())
}

func initFamilyDB(dbPath string) error {
	return family.InitDB(dbPath)
}

func newPriceWatchEngine(cfg *Config) *PriceWatchEngine {
	return pricewatch.New(cfg.HistoryDB, tool.CurrencyBaseURL, makeLifeDB())
}

func newReminderEngine(cfg *Config, notifyFn func(string)) *ReminderEngine {
	internalCfg := reminder.Config{
		CheckInterval: cfg.Reminders.CheckIntervalOrDefault(),
		MaxPerUser:    cfg.Reminders.MaxPerUser,
	}
	return reminder.New(cfg.HistoryDB, internalCfg, makeLifeDB(), notifyFn, nextCronTime)
}

func initReminderDB(dbPath string) error {
	return reminder.InitDB(dbPath)
}

func newTimeTrackingService(cfg *Config) *TimeTrackingService {
	return timetracking.New(cfg.HistoryDB, makeLifeDB())
}

func initTimeTrackingDB(dbPath string) error {
	return timetracking.InitDB(dbPath)
}

func newDailyNotesService(cfg *Config) *DailyNotesService {
	notesDir := cfg.DailyNotes.DirOrDefault(cfg.BaseDir)
	return dailynotes.New(cfg.HistoryDB, notesDir, makeLifeDB())
}

// --- oauthAdapter wraps OAuthManager to satisfy calendar.OAuthRequester ---

type oauthAdapter struct {
	mgr *OAuthManager
}

func (a *oauthAdapter) Request(ctx context.Context, provider, method, url string, body io.Reader) (*calendar.OAuthResponse, error) {
	resp, err := a.mgr.Request(ctx, provider, method, url, body)
	if err != nil {
		return nil, err
	}
	return &calendar.OAuthResponse{
		StatusCode: resp.StatusCode,
		Body:       resp.Body,
	}, nil
}

// Ensure oauthAdapter satisfies the interface at compile time.
var _ calendar.OAuthRequester = (*oauthAdapter)(nil)

// --- Forwarding helpers used by tool handlers ---

// parseExpenseNL delegates to internal finance package.
func parseExpenseNL(text, defaultCurrency string) (amount float64, currency string, category string, description string) {
	return finance.ParseExpenseNL(text, defaultCurrency)
}

// periodToDateFilter delegates to internal finance package.
func periodToDateFilter(period string) string {
	return finance.PeriodToDateFilter(period)
}

// parseNaturalSchedule delegates to internal calendar package.
func parseNaturalSchedule(text string) (*CalendarEventInput, error) {
	return calendar.ParseNaturalSchedule(text)
}

// --- Goals helper wrappers ---

func parseMilestonesFromDescription(description string) []Milestone {
	return goals.ParseMilestonesFromDescription(description, newUUID)
}

func defaultMilestones() []Milestone {
	return goals.DefaultMilestones(newUUID)
}

func calculateMilestoneProgress(milestones []Milestone) int {
	return goals.CalculateMilestoneProgress(milestones)
}

// --- Profile ---

func newUserProfileService(cfg *Config) *UserProfileService {
	sentimentFn := func(text string) (float64, []string) {
		r := nlp.Analyze(text)
		return r.Score, r.Keywords
	}
	return profile.New(cfg.HistoryDB, profile.Config{
		Enabled:          cfg.UserProfile.Enabled,
		SentimentEnabled: cfg.UserProfile.SentimentEnabled,
	}, makeLifeDB(), newUUID, sentimentFn, nlp.Label)
}

func initUserProfileDB(dbPath string) error {
	return profile.InitDB(dbPath)
}

// --- Tasks ---

func newTaskManagerService(cfg *Config) *TaskManagerService {
	return tasks.New(cfg.HistoryDB, tasks.Config{
		DefaultProject: cfg.TaskManager.DefaultProjectOrInbox(),
	}, makeLifeDB(), newUUID)
}

func initTaskManagerDB(dbPath string) error {
	return tasks.InitDB(dbPath)
}

func newNotionSync(cfg *Config) *tasks.NotionSync {
	svc := globalTaskManager
	return tasks.NewNotionSync(svc, tasks.NotionConfig{
		APIKey:     cfg.TaskManager.Notion.APIKey,
		DatabaseID: cfg.TaskManager.Notion.DatabaseID,
	})
}

func newTodoistSync(cfg *Config) *tasks.TodoistSync {
	svc := globalTaskManager
	return tasks.NewTodoistSync(svc, tasks.TodoistConfig{
		APIKey: cfg.TaskManager.Todoist.APIKey,
	})
}

// taskFromRow delegates to tasks package.
func taskFromRow(row map[string]any) UserTask {
	return tasks.TaskFromRow(row)
}

// taskFieldToColumn delegates to tasks package.
func taskFieldToColumn(field string) string {
	return tasks.TaskFieldToColumn(field)
}

// findTaskByExternalID delegates to globalTaskManager.
func findTaskByExternalID(dbPath, source, externalID string) (*UserTask, error) {
	if globalTaskManager == nil {
		return nil, fmt.Errorf("task manager not initialized")
	}
	return globalTaskManager.FindByExternalID(source, externalID)
}
