package main

import (
	"context"

	imessagebot "tetora/internal/messaging/imessage"
	"tetora/internal/storage"
)

// globalIMessageBot is the package-level iMessage bot instance.
var globalIMessageBot *imessagebot.Bot

// appCtxKey is the context key for the App container.
type appCtxKey struct{}

// withApp stores the App container in the context.
func withApp(ctx context.Context, a *App) context.Context {
	return context.WithValue(ctx, appCtxKey{}, a)
}

// appFromCtx retrieves the App container from the context.
// Returns nil if no App is stored.
func appFromCtx(ctx context.Context) *App {
	if a, ok := ctx.Value(appCtxKey{}).(*App); ok {
		return a
	}
	return nil
}

// App is the top-level application container and single source of truth for all services.
// Services are initialized into App fields in main.go, then SyncToGlobals() backfills
// global vars for callers that haven't migrated yet. As callers migrate to appFromCtx(),
// globals and SyncToGlobals() will be removed.
type App struct {
	Cfg *Config

	// Life services
	UserProfile *UserProfileService
	Finance     *FinanceService
	TaskManager *TaskManagerService
	FileManager *storage.Service
	Spotify     *SpotifyService
	Podcast     *PodcastService
	Family      *FamilyService
	Contacts    *ContactsService
	Insights    *InsightsEngine
	Scheduling  *SchedulingService
	Habits      *HabitsService
	Goals       *GoalsService
	Briefing    *BriefingService

	// Integration services
	OAuth    *OAuthManager
	Gmail    *GmailService
	Calendar *CalendarService
	Twitter  *TwitterService
	HA       *HAService
	Drive    *DriveService
	Dropbox  *DropboxService
	Browser  *BrowserRelay
	IMessage *imessagebot.Bot

	// P29 services
	Lifecycle    *LifecycleEngine
	TimeTracking *TimeTrackingService

	// Infrastructure
	SpawnTracker        *spawnTracker
	JudgeCache          *judgeCache
	ImageGenLimiter     *imageGenLimiter
	Presence            *presenceManager
	Reminder            *ReminderEngine
}

// SyncToGlobals sets all global singletons from App fields.
// This maintains backwards compatibility with existing tool handlers and HTTP routes.
func (a *App) SyncToGlobals() {
	if a.UserProfile != nil {
		globalUserProfileService = a.UserProfile
	}
	if a.Finance != nil {
		globalFinanceService = a.Finance
	}
	if a.TaskManager != nil {
		globalTaskManager = a.TaskManager
	}
	if a.FileManager != nil {
		globalFileManager = a.FileManager
	}
	if a.Spotify != nil {
		globalSpotifyService = a.Spotify
	}
	if a.Podcast != nil {
		globalPodcastService = a.Podcast
	}
	if a.Family != nil {
		globalFamilyService = a.Family
	}
	if a.Contacts != nil {
		globalContactsService = a.Contacts
	}
	if a.Insights != nil {
		globalInsightsEngine = a.Insights
	}
	if a.Scheduling != nil {
		globalSchedulingService = a.Scheduling
	}
	if a.Habits != nil {
		globalHabitsService = a.Habits
	}
	if a.Goals != nil {
		globalGoalsService = a.Goals
	}
	if a.Briefing != nil {
		globalBriefingService = a.Briefing
	}
	if a.OAuth != nil {
		globalOAuthManager = a.OAuth
	}
	if a.Gmail != nil {
		globalGmailService = a.Gmail
	}
	if a.Calendar != nil {
		globalCalendarService = a.Calendar
	}
	if a.Twitter != nil {
		globalTwitterService = a.Twitter
	}
	if a.HA != nil {
		globalHAService = a.HA
	}
	if a.Drive != nil {
		globalDriveService = a.Drive
	}
	if a.Dropbox != nil {
		globalDropboxService = a.Dropbox
	}
	if a.Browser != nil {
		globalBrowserRelay = a.Browser
	}
	if a.IMessage != nil {
		globalIMessageBot = a.IMessage
	}
	if a.Lifecycle != nil {
		globalLifecycleEngine = a.Lifecycle
	}
	if a.TimeTracking != nil {
		globalTimeTracking = a.TimeTracking
	}
	if a.SpawnTracker != nil {
		globalSpawnTracker = a.SpawnTracker
	}
	if a.JudgeCache != nil {
		globalJudgeCache = a.JudgeCache
	}
	if a.ImageGenLimiter != nil {
		globalImageGenLimiter = a.ImageGenLimiter
	}
	if a.Presence != nil {
		globalPresence = a.Presence
	}
	if a.Reminder != nil {
		globalReminderEngine = a.Reminder
	}
}
