package main

// wire_life.go wires the life service internal packages to the root package
// by providing constructors and type aliases that keep the root API surface stable.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"os"
	"sort"
	"strconv"

	"tetora/internal/automation/insights"
	"tetora/internal/config"
	"tetora/internal/db"
	idispatch "tetora/internal/dispatch"
	"tetora/internal/history"
	"tetora/internal/lifecycle"
	"tetora/internal/log"
	"tetora/internal/nlp"
	"tetora/internal/notify"
	"tetora/internal/project"
	"tetora/internal/push"
	"tetora/internal/reflection"
	"tetora/internal/retention"
	"tetora/internal/review"
	"tetora/internal/roles"
	"tetora/internal/scheduling"
	"tetora/internal/session"
	"tetora/internal/tool"
	"tetora/internal/trust"
	"tetora/internal/usage"
	"tetora/internal/workspace"

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

// --- P24.3: Life Insights Engine ---

var globalInsightsEngine *insights.Engine

// newInsightsEngine constructs an insights.Engine from Config + globals.
func newInsightsEngine(cfg *Config) *insights.Engine {
	deps := insights.Deps{
		Query:   db.Query,
		Escape:  db.Escape,
		LogWarn: log.Warn,
		UUID:    newUUID,
	}
	if globalFinanceService != nil {
		deps.FinanceDBPath = globalFinanceService.DBPath()
	}
	if globalTaskManager != nil {
		deps.TasksDBPath = globalTaskManager.DBPath()
	}
	if globalUserProfileService != nil {
		deps.ProfileDBPath = globalUserProfileService.DBPath()
	}
	if globalContactsService != nil {
		deps.ContactsDBPath = globalContactsService.DBPath()
	}
	if globalHabitsService != nil {
		deps.HabitsDBPath = globalHabitsService.DBPath()
		deps.GetHabitStreak = globalHabitsService.GetStreak
	}
	return insights.New(cfg.HistoryDB, deps)
}

func initInsightsDB(dbPath string) error {
	return insights.InitDB(dbPath)
}

// --- Tool Handlers ---

// toolLifeReport handles the life_report tool.
func toolLifeReport(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Insights == nil {
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

	report, err := app.Insights.GenerateReport(period, targetDate)
	if err != nil {
		return "", err
	}

	out, _ := json.MarshalIndent(report, "", "  ")
	return string(out), nil
}

// toolLifeInsights handles the life_insights tool.
func toolLifeInsights(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Insights == nil {
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
		insights, err := app.Insights.DetectAnomalies(days)
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
		insights, err := app.Insights.GetInsights(20, false)
		if err != nil {
			return "", err
		}
		out, _ := json.MarshalIndent(insights, "", "  ")
		return string(out), nil

	case "acknowledge":
		if args.InsightID == "" {
			return "", fmt.Errorf("insight_id is required for acknowledge action")
		}
		if err := app.Insights.AcknowledgeInsight(args.InsightID); err != nil {
			return "", err
		}
		return fmt.Sprintf("Insight %s acknowledged.", args.InsightID), nil

	case "forecast":
		result, err := app.Insights.SpendingForecast(args.Month)
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

// insightsDBPath returns the database path for insights.
func insightsDBPath(cfg *Config) string {
	if cfg.HistoryDB != "" {
		return cfg.HistoryDB
	}
	return filepath.Join(cfg.BaseDir, "history.db")
}

// ============================================================
// Merged shims: review, push, roles, projects, workspace, notify, reflection
// ============================================================

// --- Review (from review.go) ---

func buildReviewDigest(cfg *Config, days int) string {
	return review.BuildDigest(cfg, days)
}

// --- Push (from push.go) ---

type PushSubscription = push.Subscription
type PushKeys = push.SubscriptionKeys
type PushNotification = push.Notification
type PushManager = push.Manager

func newPushManager(cfg *Config) *PushManager {
	return push.NewManager(push.Config{
		HistoryDB:       cfg.HistoryDB,
		VAPIDPrivateKey: cfg.Push.VAPIDPrivateKey,
		VAPIDEmail:      cfg.Push.VAPIDEmail,
		TTL:             cfg.Push.TTL,
	})
}

// --- Roles (from roles.go) ---

type AgentArchetype = roles.AgentArchetype

var builtinArchetypes = roles.BuiltinArchetypes

func loadAgentPrompt(cfg *Config, agentName string) (string, error) {
	return roles.LoadAgentPrompt(cfg, agentName)
}

func generateSoulContent(archetype *AgentArchetype, agentName string) string {
	return roles.GenerateSoulContent(archetype, agentName)
}

func getArchetypeByName(name string) *AgentArchetype {
	return roles.GetArchetypeByName(name)
}

func writeSoulFile(cfg *Config, agentName, content string) error {
	return roles.WriteSoulFile(cfg, agentName, content)
}

// --- Projects (from projects.go) ---

type Project = project.Project
type WorkspaceProjectEntry = project.WorkspaceProjectEntry

func initProjectsDB(dbPath string) error   { return project.InitDB(dbPath) }
func listProjects(dbPath, status string) ([]Project, error) { return project.List(dbPath, status) }
func getProject(dbPath, id string) (*Project, error) { return project.Get(dbPath, id) }
func createProject(dbPath string, p Project) error   { return project.Create(dbPath, p) }
func updateProject(dbPath string, p Project) error    { return project.Update(dbPath, p) }
func deleteProject(dbPath, id string) error           { return project.Delete(dbPath, id) }
func parseProjectsMD(path string) ([]WorkspaceProjectEntry, error) { return project.ParseProjectsMD(path) }
func generateProjectID() string { return project.GenerateID() }

// --- Workspace (from workspace.go) ---

type SessionScope = workspace.SessionScope

func resolveWorkspace(cfg *Config, agentName string) WorkspaceConfig { return workspace.ResolveWorkspace(cfg, agentName) }
func defaultWorkspace(cfg *Config) WorkspaceConfig                   { return workspace.DefaultWorkspace(cfg) }
func initDirectories(cfg *Config) error                              { return workspace.InitDirectories(cfg) }
func resolveSessionScope(cfg *Config, agentName string, sessionType string) SessionScope {
	return workspace.ResolveSessionScope(cfg, agentName, sessionType)
}
func defaultToolProfile(cfg *Config) string                  { return workspace.DefaultToolProfile(cfg) }
func minTrust(a, b string) string                            { return workspace.MinTrust(a, b) }
func resolveMCPServers(cfg *Config, agentName string) []string { return workspace.ResolveMCPServers(cfg, agentName) }
func loadSoulFile(cfg *Config, agentName string) string      { return workspace.LoadSoulFile(cfg, agentName) }
func getWorkspaceMemoryPath(cfg *Config) string              { return workspace.GetWorkspaceMemoryPath(cfg) }
func getWorkspaceSkillsPath(cfg *Config) string              { return workspace.GetWorkspaceSkillsPath(cfg) }

// --- Notify (from notify.go) ---

type Notifier = notify.Notifier
type SlackNotifier = notify.SlackNotifier
type DiscordNotifier = notify.DiscordNotifier
type MultiNotifier = notify.MultiNotifier
type WhatsAppNotifier = notify.WhatsAppNotifier
type NotifyMessage = notify.Message
type NotificationEngine = notify.Engine

const (
	PriorityCritical = notify.PriorityCritical
	PriorityHigh     = notify.PriorityHigh
	PriorityNormal   = notify.PriorityNormal
	PriorityLow      = notify.PriorityLow
)

func buildNotifiers(cfg *Config) []Notifier              { return notify.BuildNotifiers(cfg) }
func buildDiscordNotifierByName(cfg *Config, name string) *DiscordNotifier {
	return notify.BuildDiscordNotifierByName(cfg, name)
}
func NewNotificationEngine(cfg *Config, notifiers []Notifier, fallbackFn func(string)) *NotificationEngine {
	return notify.NewEngine(cfg, notifiers, fallbackFn)
}
func wrapNotifyFn(ne *NotificationEngine, defaultPriority string) func(string) {
	return notify.WrapNotifyFn(ne, defaultPriority)
}
func priorityRank(p string) int            { return notify.PriorityRank(p) }
func priorityFromRank(rank int) string     { return notify.PriorityFromRank(rank) }
func isValidPriority(p string) bool        { return notify.IsValidPriority(p) }
func newDiscordNotifier(webhookURL string, timeout time.Duration) *DiscordNotifier {
	return notify.NewDiscordNotifier(webhookURL, timeout)
}

// --- Reflection (from reflection.go) ---

type ReflectionResult = reflection.Result

func initReflectionDB(dbPath string) error { return reflection.InitDB(dbPath) }
func shouldReflect(cfg *Config, task Task, result TaskResult) bool {
	return reflection.ShouldReflect(cfg, task, result)
}
func performReflection(ctx context.Context, cfg *Config, task Task, result TaskResult, sem ...chan struct{}) (*ReflectionResult, error) {
	var taskSem chan struct{}
	if len(sem) > 0 && sem[0] != nil {
		taskSem = sem[0]
	} else {
		taskSem = make(chan struct{}, 1)
	}
	deps := reflection.Deps{
		Executor: idispatch.TaskExecutorFunc(func(ctx context.Context, t idispatch.Task, agentName string) idispatch.TaskResult {
			return runSingleTask(ctx, cfg, t, taskSem, nil, agentName)
		}),
		NewID:        newUUID,
		FillDefaults: fillDefaults,
	}
	return reflection.Perform(ctx, cfg, task, result, deps)
}
func parseReflectionOutput(output string) (*ReflectionResult, error) { return reflection.ParseOutput(output) }
func extractJSON(s string) string                                    { return reflection.ExtractJSON(s) }
func storeReflection(dbPath string, ref *ReflectionResult) error     { return reflection.Store(dbPath, ref) }
func queryReflections(dbPath, agent string, limit int) ([]ReflectionResult, error) {
	return reflection.Query(dbPath, agent, limit)
}
func buildReflectionContext(dbPath, role string, limit int) string {
	return reflection.BuildContext(dbPath, role, limit)
}
func reflectionBudgetOrDefault(cfg *Config) float64 { return reflection.BudgetOrDefault(cfg) }

// ============================================================
// Merged shims: usage, trust, retention
// ============================================================

// --- Usage (from usage.go) ---

type UsageSummary = usage.UsageSummary
type ModelUsage = usage.ModelUsage
type AgentUsage = usage.AgentUsage
type ExpensiveSession = usage.ExpensiveSession
type DayUsage = usage.DayUsage

func queryUsageSummary(dbPath, period string) (*UsageSummary, error) { return usage.QuerySummary(dbPath, period) }
func queryUsageByModel(dbPath string, days int) ([]ModelUsage, error) { return usage.QueryByModel(dbPath, days) }
func queryUsageByAgent(dbPath string, days int) ([]AgentUsage, error) { return usage.QueryByAgent(dbPath, days) }
func queryExpensiveSessions(dbPath string, limit, days int) ([]ExpensiveSession, error) {
	return usage.QueryExpensiveSessions(dbPath, limit, days)
}
func queryCostTrend(dbPath string, days int) ([]DayUsage, error) { return usage.QueryCostTrend(dbPath, days) }
func formatUsageSummary(summary *UsageSummary) string             { return usage.FormatSummary(summary) }
func formatModelBreakdown(models []ModelUsage) string             { return usage.FormatModelBreakdown(models) }
func formatAgentBreakdown(roles []AgentUsage) string              { return usage.FormatAgentBreakdown(roles) }

func formatResponseCostFooter(cfg *Config, result *ProviderResult) string {
	if cfg == nil || !cfg.Usage.ShowFooter || result == nil {
		return ""
	}
	tmpl := cfg.Usage.FooterTemplate
	if tmpl == "" {
		tmpl = "{{.tokensIn}}in/{{.tokensOut}}out ~${{.cost}}"
	}
	footer := tmpl
	footer = strings.ReplaceAll(footer, "{{.tokensIn}}", fmt.Sprintf("%d", result.TokensIn))
	footer = strings.ReplaceAll(footer, "{{.tokensOut}}", fmt.Sprintf("%d", result.TokensOut))
	footer = strings.ReplaceAll(footer, "{{.cost}}", fmt.Sprintf("%.4f", result.CostUSD))
	return footer
}

func formatResultCostFooter(cfg *Config, result *TaskResult) string {
	if cfg == nil || !cfg.Usage.ShowFooter || result == nil {
		return ""
	}
	pr := &ProviderResult{
		TokensIn:  result.TokensIn,
		TokensOut: result.TokensOut,
		CostUSD:   result.CostUSD,
	}
	return formatResponseCostFooter(cfg, pr)
}

// --- Trust (from trust.go) ---

const (
	TrustObserve = trust.Observe
	TrustSuggest = trust.Suggest
	TrustAuto    = trust.Auto
)

var validTrustLevels = trust.ValidLevels

type TrustStatus = trust.Status

func isValidTrustLevel(level string) bool                            { return trust.IsValidLevel(level) }
func trustLevelIndex(level string) int                               { return trust.LevelIndex(level) }
func nextTrustLevel(current string) string                           { return trust.NextLevel(current) }
func initTrustDB(dbPath string)                                      { trust.InitDB(dbPath) }
func resolveTrustLevel(cfg *config.Config, agentName string) string  { return trust.ResolveLevel(cfg, agentName) }
func queryConsecutiveSuccess(dbPath, role string) int                 { return trust.QueryConsecutiveSuccess(dbPath, role) }
func recordTrustEvent(dbPath, role, eventType, fromLevel, toLevel string, consecutiveSuccess int, note string) {
	trust.RecordEvent(dbPath, role, eventType, fromLevel, toLevel, consecutiveSuccess, note)
}
func queryTrustEvents(dbPath, role string, limit int) ([]map[string]any, error) {
	return trust.QueryEvents(dbPath, role, limit)
}
func getTrustStatus(cfg *Config, role string) TrustStatus         { return trust.GetStatus(cfg, role) }
func getAllTrustStatuses(cfg *Config) []TrustStatus                { return trust.GetAllStatuses(cfg) }
func applyTrustToTask(cfg *Config, task *Task, agentName string) (level string, needsConfirm bool) {
	return trust.ApplyToTask(cfg, &task.PermissionMode, agentName)
}
func checkTrustPromotion(ctx context.Context, cfg *Config, agentName string) string {
	return trust.CheckPromotion(ctx, cfg, agentName)
}
func updateAgentTrustLevel(cfg *Config, agentName, newLevel string) error {
	return trust.UpdateAgentLevel(cfg, agentName, newLevel)
}
func saveAgentTrustLevel(configPath, agentName, newLevel string) error {
	return trust.SaveAgentLevel(configPath, agentName, newLevel)
}
func updateConfigField(configPath string, mutate func(raw map[string]any)) error {
	return trust.UpdateConfigField(configPath, mutate)
}

// --- Retention (from retention.go) ---

type RetentionResult = retention.Result
type ReflectionRow = retention.ReflectionRow
type DataExport = retention.DataExport

func retentionHooks(cfg *Config) retention.Hooks {
	return retention.Hooks{
		CleanupSessions:      cleanupSessions,
		CleanupOldQueueItems: cleanupOldQueueItems,
		CleanupOutputs:       cleanupOutputs,
		ListMemory: func(workspaceDir string) ([]retention.MemoryEntry, error) {
			entries, err := listMemory(cfg, "")
			if err != nil {
				return nil, err
			}
			out := make([]retention.MemoryEntry, len(entries))
			for i, e := range entries {
				out[i] = retention.MemoryEntry{
					Key:       e.Key,
					Value:     e.Value,
					Priority:  e.Priority,
					UpdatedAt: e.UpdatedAt,
				}
			}
			return out, nil
		},
		QuerySessions: func(dbPath string, limit int) ([]session.Session, error) {
			sessions, _, err := querySessions(dbPath, SessionQuery{Limit: limit})
			return sessions, err
		},
		LoadMemoryAccessLog:    func(workspaceDir string) map[string]string { return loadMemoryAccessLog(cfg) },
		SaveMemoryAccessLog:    func(workspaceDir string, log map[string]string) { saveMemoryAccessLog(cfg, log) },
		ParseMemoryFrontmatter: parseMemoryFrontmatter,
		BuildMemoryFrontmatter: buildMemoryFrontmatter,
	}
}

func retentionDays(configured, fallback int) int       { return retention.Days(configured, fallback) }
func runRetention(cfg *Config) []RetentionResult       { return retention.Run(cfg, retentionHooks(cfg)) }
func compilePIIPatterns(patterns []string) []*regexp.Regexp { return retention.CompilePIIPatterns(patterns) }
func redactPII(text string, patterns []*regexp.Regexp) string { return retention.RedactPII(text, patterns) }
func queryRetentionStats(dbPath string) map[string]int { return retention.QueryStats(dbPath) }
func exportData(cfg *Config) ([]byte, error)           { return retention.Export(cfg, retentionHooks(cfg)) }
func queryReflectionsForExport(dbPath string) []ReflectionRow { return retention.QueryReflectionsForExport(dbPath) }
func purgeDataBefore(cfg *Config, before string) ([]RetentionResult, error) {
	return retention.PurgeBefore(cfg.HistoryDB, before)
}
func cleanupWorkflowRuns(dbPath string, days int) (int, error)   { return retention.CleanupWorkflowRuns(dbPath, days) }
func cleanupHandoffs(dbPath string, days int) (int, error)       { return retention.CleanupHandoffs(dbPath, days) }
func cleanupReflections(dbPath string, days int) (int, error)    { return retention.CleanupReflections(dbPath, days) }
func cleanupSLAChecks(dbPath string, days int) (int, error)      { return retention.CleanupSLAChecks(dbPath, days) }
func cleanupTrustEvents(dbPath string, days int) (int, error)    { return retention.CleanupTrustEvents(dbPath, days) }
func cleanupLogFiles(logDir string, days int) int                { return retention.CleanupLogFiles(logDir, days) }
func cleanupClaudeSessions(days int) int                         { return retention.CleanupClaudeSessions(days) }
func cleanupStaleMemory(cfg *Config, days int) (int, error)      { return retention.CleanupStaleMemory(cfg.WorkspaceDir, days, retentionHooks(cfg)) }

// ============================================================
// Merged from lifecycle.go
// ============================================================

// LifecycleEngine wraps the internal lifecycle engine for package main.
type LifecycleEngine struct {
	cfg    *Config
	engine *lifecycle.Engine
}

// globalLifecycleEngine is the singleton lifecycle engine.
var globalLifecycleEngine *LifecycleEngine

// newLifecycleEngine creates a new LifecycleEngine, wiring current globals.
func newLifecycleEngine(cfg *Config) *LifecycleEngine {
	le := &LifecycleEngine{cfg: cfg}
	le.rebuildEngine()
	return le
}

// rebuildEngine constructs the internal engine from current global services.
func (le *LifecycleEngine) rebuildEngine() {
	lcCfg := lifecycle.Config{
		Lifecycle: lifecycle.LifecycleConfig{
			AutoHabitSuggest:   le.cfg.Lifecycle.AutoHabitSuggest,
			AutoInsightAction:  le.cfg.Lifecycle.AutoInsightAction,
			AutoBirthdayRemind: le.cfg.Lifecycle.AutoBirthdayRemind,
		},
	}
	if le.cfg.Notes.Enabled {
		lcCfg.NotesEnabled = true
		lcCfg.VaultPath = le.cfg.Notes.VaultPathResolved(le.cfg.BaseDir)
	}
	le.engine = lifecycle.New(lcCfg, globalInsightsEngine, globalContactsService, globalGoalsService, globalReminderEngine)
}

// SuggestHabitForGoal returns habit suggestions based on goal title and category.
func (le *LifecycleEngine) SuggestHabitForGoal(title, category string) []string {
	le.rebuildEngine()
	return le.engine.SuggestHabitForGoal(title, category)
}

// RunInsightActions detects anomalies and creates reminders/notifications.
func (le *LifecycleEngine) RunInsightActions() ([]string, error) {
	le.rebuildEngine()
	return le.engine.RunInsightActions()
}

// SyncBirthdayReminders creates annual reminders for contact birthdays.
func (le *LifecycleEngine) SyncBirthdayReminders() (int, error) {
	le.rebuildEngine()
	return le.engine.SyncBirthdayReminders()
}

// OnGoalCompleted logs a celebration note when a goal is completed.
func (le *LifecycleEngine) OnGoalCompleted(goalID string) error {
	le.rebuildEngine()
	return le.engine.OnGoalCompleted(goalID)
}

// --- Lifecycle Tool Handlers ---

func toolLifecycleSync(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Lifecycle == nil {
		return "", fmt.Errorf("lifecycle engine not initialized")
	}

	var args struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Action == "" {
		args.Action = "all"
	}

	result := map[string]any{}

	switch args.Action {
	case "birthdays":
		n, err := app.Lifecycle.SyncBirthdayReminders()
		if err != nil {
			return "", err
		}
		result["birthdays_synced"] = n

	case "insights":
		actions, err := app.Lifecycle.RunInsightActions()
		if err != nil {
			return "", err
		}
		result["insight_actions"] = actions

	case "all":
		if cfg.Lifecycle.AutoBirthdayRemind {
			n, err := app.Lifecycle.SyncBirthdayReminders()
			if err != nil {
				result["birthday_error"] = err.Error()
			} else {
				result["birthdays_synced"] = n
			}
		}
		if cfg.Lifecycle.AutoInsightAction {
			actions, err := app.Lifecycle.RunInsightActions()
			if err != nil {
				result["insight_error"] = err.Error()
			} else {
				result["insight_actions"] = actions
			}
		}

	default:
		return "", fmt.Errorf("unknown action: %s (use birthdays, insights, or all)", args.Action)
	}

	out, _ := json.MarshalIndent(result, "", "  ")
	return string(out), nil
}

func toolLifecycleSuggest(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Lifecycle == nil {
		return "", fmt.Errorf("lifecycle engine not initialized")
	}

	var args struct {
		GoalTitle    string `json:"goal_title"`
		GoalCategory string `json:"goal_category"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.GoalTitle == "" {
		return "", fmt.Errorf("goal_title is required")
	}

	suggestions := app.Lifecycle.SuggestHabitForGoal(args.GoalTitle, args.GoalCategory)
	result := map[string]any{
		"goal_title":  args.GoalTitle,
		"suggestions": suggestions,
	}
	out, _ := json.MarshalIndent(result, "", "  ")
	return string(out), nil
}

// ============================================================
// Merged from scheduling.go
// ============================================================

// --- Scheduling Type aliases ---

type TimeSlot = scheduling.TimeSlot
type DaySchedule = scheduling.DaySchedule
type ScheduleEvent = scheduling.ScheduleEvent
type ScheduleSuggestion = scheduling.ScheduleSuggestion

// --- Scheduling Global ---

var globalSchedulingService *scheduling.Service

// newSchedulingService constructs a scheduling.Service wired to root globals.
func newSchedulingService(cfg *Config) *scheduling.Service {
	return scheduling.New(
		&schedulingCalendarAdapter{},
		&schedulingTaskAdapter{},
		log.Warn,
	)
}

// --- Scheduling Adapter types ---

// schedulingCalendarAdapter implements scheduling.CalendarProvider using globalCalendarService.
type schedulingCalendarAdapter struct{}

func (a *schedulingCalendarAdapter) ListEvents(ctx context.Context, timeMin, timeMax string, maxResults int) ([]scheduling.CalendarEvent, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Calendar == nil {
		return nil, nil
	}
	events, err := app.Calendar.ListEvents(ctx, timeMin, timeMax, maxResults)
	if err != nil {
		return nil, err
	}
	var result []scheduling.CalendarEvent
	for _, ev := range events {
		result = append(result, scheduling.CalendarEvent{
			Summary: ev.Summary,
			Start:   ev.Start,
			End:     ev.End,
			AllDay:  ev.AllDay,
		})
	}
	return result, nil
}

// schedulingTaskAdapter implements scheduling.TaskProvider using globalTaskManager.
type schedulingTaskAdapter struct{}

func (a *schedulingTaskAdapter) ListTasks(userID string, filter scheduling.TaskFilter) ([]scheduling.Task, error) {
	if globalTaskManager == nil {
		return nil, nil
	}
	tasks, err := globalTaskManager.ListTasks(userID, TaskFilter{
		DueDate: filter.DueDate,
		Status:  filter.Status,
		Limit:   filter.Limit,
	})
	if err != nil {
		return nil, err
	}
	var result []scheduling.Task
	for _, t := range tasks {
		result = append(result, scheduling.Task{
			Title:    t.Title,
			Priority: t.Priority,
			DueAt:    t.DueAt,
			Project:  t.Project,
		})
	}
	return result, nil
}

// --- Scheduling Tool Handlers ---

func toolScheduleView(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Scheduling == nil {
		return "", fmt.Errorf("scheduling service not initialized")
	}

	var args struct {
		Date string `json:"date"`
		Days int    `json:"days"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Days <= 0 {
		args.Days = 1
	}
	if args.Days > 30 {
		args.Days = 30
	}

	schedules, err := app.Scheduling.ViewSchedule(args.Date, args.Days)
	if err != nil {
		return "", err
	}

	out, err := json.MarshalIndent(schedules, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal result: %w", err)
	}
	return string(out), nil
}

func toolScheduleSuggest(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Scheduling == nil {
		return "", fmt.Errorf("scheduling service not initialized")
	}

	var args struct {
		DurationMinutes int  `json:"duration_minutes"`
		PreferMorning   bool `json:"prefer_morning"`
		Days            int  `json:"days"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.DurationMinutes <= 0 {
		args.DurationMinutes = 60
	}
	if args.Days <= 0 {
		args.Days = 5
	}
	if args.Days > 14 {
		args.Days = 14
	}

	suggestions, err := app.Scheduling.SuggestSlots(args.DurationMinutes, args.PreferMorning, args.Days)
	if err != nil {
		return "", err
	}

	if len(suggestions) == 0 {
		return "No available time slots found for the requested duration.", nil
	}

	out, err := json.MarshalIndent(suggestions, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal result: %w", err)
	}
	return fmt.Sprintf("Found %d suggested slots:\n%s", len(suggestions), string(out)), nil
}

func toolSchedulePlan(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Scheduling == nil {
		return "", fmt.Errorf("scheduling service not initialized")
	}

	var args struct {
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.UserID == "" {
		args.UserID = "default"
	}

	plan, err := app.Scheduling.PlanWeek(args.UserID)
	if err != nil {
		return "", err
	}

	out, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal result: %w", err)
	}
	return string(out), nil
}

// ============================================================
// Merged from daily_notes.go
// ============================================================

// generateDailyNote creates a markdown summary of the previous day's activity.
func generateDailyNote(cfg *Config, date time.Time) (string, error) {
	if cfg.HistoryDB == "" {
		return "", fmt.Errorf("historyDB not configured")
	}

	startOfDay := date.Format("2006-01-02 00:00:00")
	endOfDay := date.Add(24 * time.Hour).Format("2006-01-02 00:00:00")

	sql := fmt.Sprintf(`
		SELECT id, name, source, agent, status, duration_ms, cost_usd, tokens_in, tokens_out, started_at
		FROM history
		WHERE started_at >= '%s' AND started_at < '%s'
		ORDER BY started_at
	`, db.Escape(startOfDay), db.Escape(endOfDay))

	rows, err := db.Query(cfg.HistoryDB, sql)
	if err != nil {
		return "", fmt.Errorf("query history: %w", err)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Daily Summary — %s\n\n", date.Format("2006-01-02")))

	if len(rows) == 0 {
		sb.WriteString("No tasks executed on this day.\n")
		return sb.String(), nil
	}

	totalCost := 0.0
	totalTokensIn := 0
	totalTokensOut := 0
	successCount := 0
	errorCount := 0
	roleMap := make(map[string]int)
	sourceMap := make(map[string]int)

	for _, row := range rows {
		status := toString(row["status"])
		costUSD := toFloat(row["cost_usd"])
		tokensIn := toInt(row["tokens_in"])
		tokensOut := toInt(row["tokens_out"])
		role := toString(row["agent"])
		source := toString(row["source"])

		totalCost += costUSD
		totalTokensIn += tokensIn
		totalTokensOut += tokensOut

		if status == "success" {
			successCount++
		} else {
			errorCount++
		}

		if role != "" {
			roleMap[role]++
		}
		if source != "" {
			sourceMap[source]++
		}
	}

	sb.WriteString("## Summary\n\n")
	sb.WriteString(fmt.Sprintf("- **Total Tasks**: %d\n", len(rows)))
	sb.WriteString(fmt.Sprintf("- **Success**: %d\n", successCount))
	sb.WriteString(fmt.Sprintf("- **Errors**: %d\n", errorCount))
	sb.WriteString(fmt.Sprintf("- **Total Cost**: $%.4f\n", totalCost))
	sb.WriteString(fmt.Sprintf("- **Total Tokens**: %d in / %d out\n\n", totalTokensIn, totalTokensOut))

	if len(roleMap) > 0 {
		sb.WriteString("## Tasks by Agent\n\n")
		for role, count := range roleMap {
			if role == "" {
				role = "(none)"
			}
			sb.WriteString(fmt.Sprintf("- **%s**: %d\n", role, count))
		}
		sb.WriteString("\n")
	}

	if len(sourceMap) > 0 {
		sb.WriteString("## Tasks by Source\n\n")
		for source, count := range sourceMap {
			if source == "" {
				source = "(unknown)"
			}
			sb.WriteString(fmt.Sprintf("- **%s**: %d\n", source, count))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## Recent Tasks\n\n")
	maxShow := 10
	if len(rows) < maxShow {
		maxShow = len(rows)
	}
	for i := len(rows) - maxShow; i < len(rows); i++ {
		row := rows[i]
		name := toString(row["name"])
		status := toString(row["status"])
		costUSD := toFloat(row["cost_usd"])
		durationMs := toInt(row["duration_ms"])
		startedAt := toString(row["started_at"])
		role := toString(row["agent"])

		statusEmoji := "✅"
		if status != "success" {
			statusEmoji = "❌"
		}

		sb.WriteString(fmt.Sprintf("- %s **%s** (agent: %s)\n", statusEmoji, name, role))
		sb.WriteString(fmt.Sprintf("  - Started: %s\n", startedAt))
		sb.WriteString(fmt.Sprintf("  - Duration: %dms, Cost: $%.4f\n", durationMs, costUSD))
	}

	return sb.String(), nil
}

func writeDailyNote(cfg *Config, date time.Time, content string) error {
	if !cfg.DailyNotes.Enabled {
		return nil
	}

	notesDir := cfg.DailyNotes.DirOrDefault(cfg.BaseDir)
	if err := os.MkdirAll(notesDir, 0o755); err != nil {
		return fmt.Errorf("mkdir notes: %w", err)
	}

	filename := date.Format("2006-01-02") + ".md"
	filePath := filepath.Join(notesDir, filename)

	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write note: %w", err)
	}

	log.Info("daily note written", "date", date.Format("2006-01-02"), "path", filePath)
	return nil
}

func registerDailyNotesJob(ctx context.Context, cfg *Config, cronEngine *CronEngine) {
	if !cfg.DailyNotes.Enabled {
		return
	}

	schedule := cfg.DailyNotes.ScheduleOrDefault()

	if err := cronEngine.AddJob(CronJobConfig{
		ID:       "daily_notes",
		Name:     "Daily Notes Generator",
		Enabled:  true,
		Schedule: schedule,
	}); err != nil {
		log.Info("daily notes job register", "schedule", schedule, "note", err)
		return
	}

	log.Info("daily notes job registered", "schedule", schedule)
}

func runDailyNotesJob(ctx context.Context, cfg *Config) error {
	yesterday := time.Now().AddDate(0, 0, -1)
	content, err := generateDailyNote(cfg, yesterday)
	if err != nil {
		return fmt.Errorf("generate note: %w", err)
	}

	if err := writeDailyNote(cfg, yesterday, content); err != nil {
		return fmt.Errorf("write note: %w", err)
	}

	return nil
}

func toString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func toFloat(v any) float64 {
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
	}
	return 0
}

func toInt(v any) int {
	if v == nil {
		return 0
	}
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	}
	return 0
}

// ============================================================
// Merged from task_manager.go
// ============================================================

// globalTaskManager is the singleton task manager service.
var globalTaskManager *TaskManagerService

func toolTaskCreate(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.TaskManager == nil {
		return "", fmt.Errorf("task manager not initialized (enable taskManager in config)")
	}
	var args struct {
		Title       string   `json:"title"`
		Description string   `json:"description"`
		Project     string   `json:"project"`
		Priority    int      `json:"priority"`
		DueAt       string   `json:"dueAt"`
		Tags        []string `json:"tags"`
		UserID      string   `json:"userId"`
		Decompose   bool     `json:"decompose"`
		Subtasks    []string `json:"subtasks"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Title == "" {
		return "", fmt.Errorf("title is required")
	}
	if args.UserID == "" {
		args.UserID = "default"
	}

	task := UserTask{
		UserID:      args.UserID,
		Title:       args.Title,
		Description: args.Description,
		Project:     args.Project,
		Priority:    args.Priority,
		DueAt:       args.DueAt,
		Tags:        args.Tags,
	}

	created, err := app.TaskManager.CreateTask(task)
	if err != nil {
		return "", err
	}

	if args.Decompose && len(args.Subtasks) > 0 {
		subs, err := app.TaskManager.DecomposeTask(created.ID, args.Subtasks)
		if err != nil {
			return "", fmt.Errorf("task created but decomposition failed: %w", err)
		}
		result := map[string]any{
			"task":     created,
			"subtasks": subs,
		}
		out, _ := json.MarshalIndent(result, "", "  ")
		return string(out), nil
	}

	out, _ := json.MarshalIndent(created, "", "  ")
	return string(out), nil
}

func toolTaskList(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.TaskManager == nil {
		return "", fmt.Errorf("task manager not initialized (enable taskManager in config)")
	}
	var args struct {
		Status   string `json:"status"`
		Project  string `json:"project"`
		Priority int    `json:"priority"`
		DueDate  string `json:"dueDate"`
		Tag      string `json:"tag"`
		Limit    int    `json:"limit"`
		UserID   string `json:"userId"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.UserID == "" {
		args.UserID = "default"
	}

	filters := TaskFilter{
		Status:   args.Status,
		Project:  args.Project,
		Priority: args.Priority,
		DueDate:  args.DueDate,
		Tag:      args.Tag,
		Limit:    args.Limit,
	}

	tasksList, err := app.TaskManager.ListTasks(args.UserID, filters)
	if err != nil {
		return "", err
	}

	type taskWithSubs struct {
		UserTask
		SubtaskCount int `json:"subtaskCount"`
	}
	results := make([]taskWithSubs, 0, len(tasksList))
	for _, t := range tasksList {
		subs, _ := app.TaskManager.GetSubtasks(t.ID)
		results = append(results, taskWithSubs{UserTask: t, SubtaskCount: len(subs)})
	}

	out, _ := json.MarshalIndent(results, "", "  ")
	return string(out), nil
}

func toolTaskComplete(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.TaskManager == nil {
		return "", fmt.Errorf("task manager not initialized (enable taskManager in config)")
	}
	var args struct {
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.TaskID == "" {
		return "", fmt.Errorf("taskId is required")
	}

	if err := app.TaskManager.CompleteTask(args.TaskID); err != nil {
		return "", err
	}

	task, _ := app.TaskManager.GetTask(args.TaskID)
	if task != nil {
		out, _ := json.MarshalIndent(task, "", "  ")
		return fmt.Sprintf("Task completed.\n%s", string(out)), nil
	}
	return "Task completed.", nil
}

func toolTaskReview(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.TaskManager == nil {
		return "", fmt.Errorf("task manager not initialized (enable taskManager in config)")
	}
	var args struct {
		Period string `json:"period"`
		UserID string `json:"userId"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.UserID == "" {
		args.UserID = "default"
	}
	if args.Period == "" {
		args.Period = "daily"
	}

	reviewResult, err := app.TaskManager.GenerateReview(args.UserID, args.Period)
	if err != nil {
		return "", err
	}

	out, _ := json.MarshalIndent(reviewResult, "", "  ")
	return string(out), nil
}

// ============================================================
// Merged from template.go
// ============================================================

// expandPrompt replaces template variables in a prompt string.
func expandPrompt(prompt, jobID, dbPath, agentName, knowledgeDir string, cfg *Config) string {
	if !strings.Contains(prompt, "{{") {
		return prompt
	}

	now := time.Now()

	r := strings.NewReplacer(
		"{{date}}", now.Format("2006-01-02"),
		"{{datetime}}", now.Format(time.RFC3339),
		"{{weekday}}", now.Weekday().String(),
		"{{knowledge_dir}}", knowledgeDir,
	)
	prompt = r.Replace(prompt)

	if jobID != "" && dbPath != "" &&
		(strings.Contains(prompt, "{{last_output}}") ||
			strings.Contains(prompt, "{{last_status}}") ||
			strings.Contains(prompt, "{{last_error}}")) {

		last := history.QueryLastRun(dbPath, jobID)
		lastOutput := ""
		lastStatus := ""
		lastError := ""
		if last != nil {
			lastOutput = last.OutputSummary
			lastStatus = last.Status
			lastError = last.Error
		}

		r2 := strings.NewReplacer(
			"{{last_output}}", lastOutput,
			"{{last_status}}", lastStatus,
			"{{last_error}}", lastError,
		)
		prompt = r2.Replace(prompt)
	}

	envRe := regexp.MustCompile(`\{\{env\.([A-Za-z_][A-Za-z0-9_]*)\}\}`)
	prompt = envRe.ReplaceAllStringFunc(prompt, func(match string) string {
		parts := envRe.FindStringSubmatch(match)
		if len(parts) < 2 {
			return match
		}
		return os.Getenv(parts[1])
	})

	if agentName != "" && cfg != nil {
		memRe := regexp.MustCompile(`\{\{memory\.([A-Za-z_][A-Za-z0-9_]*)\}\}`)
		prompt = memRe.ReplaceAllStringFunc(prompt, func(match string) string {
			parts := memRe.FindStringSubmatch(match)
			if len(parts) < 2 {
				return match
			}
			val, _ := getMemory(cfg, agentName, parts[1])
			if val != "" {
				recordMemoryAccess(cfg, parts[1])
			}
			return val
		})
	}

	if cfg != nil && strings.Contains(prompt, "{{rules.") {
		rulesRe := regexp.MustCompile(`\{\{rules\.([A-Za-z_][A-Za-z0-9_\-]*)\}\}`)
		prompt = rulesRe.ReplaceAllStringFunc(prompt, func(match string) string {
			parts := rulesRe.FindStringSubmatch(match)
			if len(parts) < 2 {
				return match
			}
			path := filepath.Join(cfg.WorkspaceDir, "rules", parts[1]+".md")
			data, err := os.ReadFile(path)
			if err != nil {
				return "(rule not found: " + parts[1] + ")"
			}
			return string(data)
		})
	}

	if cfg != nil && strings.Contains(prompt, "{{skill.") {
		skillRe := regexp.MustCompile(`\{\{skill\.([A-Za-z_][A-Za-z0-9_]*)\}\}`)
		prompt = skillRe.ReplaceAllStringFunc(prompt, func(match string) string {
			parts := skillRe.FindStringSubmatch(match)
			if len(parts) < 2 {
				return match
			}
			skill := getSkill(cfg, parts[1])
			if skill == nil {
				return match
			}
			result, err := executeSkill(context.Background(), *skill, nil)
			if err != nil || result.Status != "success" {
				return "(skill error)"
			}
			return strings.TrimSpace(result.Output)
		})
	}

	if cfg != nil && strings.Contains(prompt, "{{review.digest") {
		reviewRe := regexp.MustCompile(`\{\{review\.digest(?::(\d+))?\}\}`)
		prompt = reviewRe.ReplaceAllStringFunc(prompt, func(match string) string {
			parts := reviewRe.FindStringSubmatch(match)
			days := 7
			if len(parts) >= 2 && parts[1] != "" {
				if d, err := strconv.Atoi(parts[1]); err == nil && d > 0 && d <= 90 {
					days = d
				}
			}
			return buildReviewDigest(cfg, days)
		})
	}

	return prompt
}

// PromptInfo represents a prompt template file.
type PromptInfo struct {
	Name    string `json:"name"`
	Preview string `json:"preview,omitempty"`
	Content string `json:"content,omitempty"`
}

func promptsDir(cfg *Config) string {
	dir := filepath.Join(cfg.BaseDir, "prompts")
	os.MkdirAll(dir, 0o755)
	return dir
}

func listPrompts(cfg *Config) ([]PromptInfo, error) {
	dir := promptsDir(cfg)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var prompts []PromptInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		preview := ""
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err == nil {
			preview = string(data)
			if len(preview) > 100 {
				preview = preview[:100] + "..."
			}
			preview = strings.ReplaceAll(preview, "\n", " ")
		}
		prompts = append(prompts, PromptInfo{Name: name, Preview: preview})
	}

	sort.Slice(prompts, func(i, j int) bool {
		return prompts[i].Name < prompts[j].Name
	})
	return prompts, nil
}

func readPrompt(cfg *Config, name string) (string, error) {
	path := filepath.Join(promptsDir(cfg), name+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("prompt %q not found", name)
	}
	return string(data), nil
}

func writePrompt(cfg *Config, name, content string) error {
	if name == "" {
		return fmt.Errorf("prompt name is required")
	}
	for _, r := range name {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
			return fmt.Errorf("invalid character %q in prompt name (use a-z, 0-9, -, _)", string(r))
		}
	}
	path := filepath.Join(promptsDir(cfg), name+".md")
	return os.WriteFile(path, []byte(content), 0o644)
}

func deletePrompt(cfg *Config, name string) error {
	path := filepath.Join(promptsDir(cfg), name+".md")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("prompt %q not found", name)
	}
	return os.Remove(path)
}

func resolvePromptFile(cfg *Config, promptFile string) (string, error) {
	if promptFile == "" {
		return "", nil
	}
	name := strings.TrimSuffix(promptFile, ".md")
	return readPrompt(cfg, name)
}
