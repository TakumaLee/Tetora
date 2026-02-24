package main

// App is the top-level application container holding all services.
// Phase 1: Created in main.go, globals set from its fields via SyncToGlobals.
// Phase 2 (future): Server.app provides access, HTTP handlers migrate.
// Phase 3 (future): Tool handlers receive App via context.
type App struct {
	Cfg *Config

	// Life services
	UserProfile *UserProfileService
	Finance     *FinanceService
	TaskManager *TaskManagerService
	FileManager *FileManagerService
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
	IMessage *IMessageBot

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
