package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func main() {
	// Subcommand routing.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "init":
			cmdInit()
			return
		case "doctor":
			cmdDoctor()
			return
		case "service":
			cmdService(os.Args[2:])
			return
		case "job":
			cmdJob(os.Args[2:])
			return
		case "history":
			cmdHistory(os.Args[2:])
			return
		case "agent":
			cmdAgent(os.Args[2:])
			return
		case "status":
			cmdStatus(os.Args[2:])
			return
		case "dispatch":
			cmdDispatch(os.Args[2:])
			return
		case "route":
			cmdRouteDispatch(os.Args[2:])
			return
		case "config":
			cmdConfig(os.Args[2:])
			return
		case "logs", "log":
			cmdLogs(os.Args[2:])
			return
		case "prompt":
			cmdPrompt(os.Args[2:])
			return
		case "memory":
			cmdMemory(os.Args[2:])
			return
		case "mcp":
			cmdMCP(os.Args[2:])
			return
		case "knowledge":
			cmdKnowledge(os.Args[2:])
			return
		case "skill":
			cmdSkill(os.Args[2:])
			return
		case "workflow":
			cmdWorkflow(os.Args[2:])
			return
		case "security":
			cmdSecurity(os.Args[2:])
			return
		case "session":
			cmdSession(os.Args[2:])
			return
		case "budget":
			cmdBudget(os.Args[2:])
			return
		case "usage":
			cmdUsage(os.Args[2:])
			return
		case "trust":
			cmdTrust(os.Args[2:])
			return
		case "webhook":
			cmdWebhook(os.Args[2:])
			return
		case "proactive":
			runProactive(os.Args[2:])
			return
		case "quick":
			cmdQuick(os.Args[2:], loadConfig(""))
			return
		case "compact":
			runCompaction(os.Args[2:])
			return
		case "pairing":
			cmdPairing(os.Args[2:])
			return
		case "data":
			cmdData(os.Args[2:])
			return
		case "oauth": // --- P18.2: OAuth 2.0 Framework ---
			cmdOAuth(os.Args[2:])
			return
		case "plugin": // --- P13.1: Plugin System ---
			cmdPlugin(os.Args[2:])
			return
		case "task": // --- P14.6: Task Board ---
			cmdTask(os.Args[2:])
			return
		case "backup":
			cmdBackup(os.Args[2:])
			return
		case "restore":
			cmdRestore(os.Args[2:])
			return
		case "mirror":
			cmdMirror(os.Args[2:])
			return
		case "discord":
			handleDiscordCLI(os.Args[2:])
			return
		case "dashboard":
			cmdOpenDashboard()
			return
		case "migrate": // --- P21.8: OpenClaw Migration Tool + P27.2: Encrypt ---
			if len(os.Args) > 2 && os.Args[2] == "openclaw" {
				cmdMigrateOpenClaw(os.Args[3:])
			} else if len(os.Args) > 2 && os.Args[2] == "encrypt" {
				cmdMigrateEncrypt()
			} else {
				fmt.Fprintln(os.Stderr, "Usage: tetora migrate <openclaw|encrypt>")
				os.Exit(1)
			}
			return
		case "completion":
			cmdCompletion(os.Args[2:])
			return
		case "access":
			cmdAccess(os.Args[2:])
			return
		case "import":
			if len(os.Args) > 2 {
				switch os.Args[2] {
				case "openclaw":
					cmdImportOpenClaw()
				case "config":
					cmdImportConfig(os.Args[3:])
				default:
					fmt.Fprintln(os.Stderr, "Usage: tetora import <openclaw|config>")
					os.Exit(1)
				}
			} else {
				fmt.Fprintln(os.Stderr, "Usage: tetora import <openclaw|config>")
				os.Exit(1)
			}
			return
		case "drain":
			cmdDrain()
			return
		case "restart":
			cmdRestart()
			return
		case "release":
			cmdRelease(os.Args[2:])
			return
		case "upgrade":
			cmdUpgrade()
			return
		case "version", "--version":
			cmdVersion()
			return
		case "help", "--help":
			printUsage()
			return
		case "serve":
			// Rewrite args for flag compat.
			os.Args = append([]string{os.Args[0], "--serve"}, os.Args[2:]...)
		case "run":
			os.Args = append([]string{os.Args[0]}, os.Args[2:]...)
		}
	}

	configPath := flag.String("config", "", "config file path")
	tasksJSON := flag.String("tasks", "", "inline tasks JSON array")
	filePath := flag.String("file", "", "tasks JSON file path")
	notify := flag.Bool("notify", false, "send Telegram notification on completion")
	serve := flag.Bool("serve", false, "run as daemon (Telegram bot + HTTP + cron)")
	flag.Parse()

	cfg := loadConfig(*configPath)

	// P27.2: Set global encryption key for standalone functions.
	setGlobalEncryptionKey(resolveEncryptionKey(cfg))

	// Initialize structured logger from config.
	// Backward compat: cfg.Log=true with no explicit level → debug.
	if cfg.Log && cfg.Logging.Level == "" {
		cfg.Logging.Level = "debug"
	}
	defaultLogger = initLogger(cfg.Logging, cfg.baseDir)

	// Shared concurrency semaphore — limits total concurrent claude sessions.
	sem := make(chan struct{}, cfg.MaxConcurrent)

	// Child semaphore — separate pool for sub-agent tasks (depth > 0).
	// Prevents deadlock when parent tasks hold sem slots and spawn children that also need slots.
	childSem := make(chan struct{}, childSemConcurrentOrDefault(cfg))

	state := newDispatchState()
	state.broker = newSSEBroker()

	// Signal handling.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	if *serve {
		// --- Daemon mode ---
		logInfo("tetora v2 starting", "maxConcurrent", cfg.MaxConcurrent, "childConcurrent", childSemConcurrentOrDefault(cfg))

		// Track degraded services for health reporting.
		var degradedServices []string

		// Init history DB.
		if cfg.HistoryDB != "" {
			if err := initHistoryDB(cfg.HistoryDB); err != nil {
				logWarn("init history db failed", "error", err)
				degradedServices = append(degradedServices, "historyDB")
			} else {
				logInfo("history db initialized", "path", cfg.HistoryDB)

				// Set SQLite pragmas for reliability.
				if err := pragmaDB(cfg.HistoryDB); err != nil {
					logWarn("set db pragmas failed", "error", err)
				} else {
					logInfo("db pragmas set", "mode", "WAL")
				}

				// Init embedding DB if enabled.
				if cfg.Embedding.Enabled {
					if err := initEmbeddingDB(cfg.HistoryDB); err != nil {
						logWarn("init embedding db failed", "error", err)
						degradedServices = append(degradedServices, "embedding")
					} else {
						logInfo("embedding db initialized")
					}
				}

				// Cleanup records using retention config.
				if err := cleanupHistory(cfg.HistoryDB, retentionDays(cfg.Retention.History, 90)); err != nil {
					logWarn("cleanup history failed", "error", err)
				}
			}
			// Init audit log table and start batched writer.
			if err := initAuditLog(cfg.HistoryDB); err != nil {
				logWarn("init audit_log failed", "error", err)
			}
			startAuditWriter()
			cleanupAuditLog(cfg.HistoryDB, retentionDays(cfg.Retention.AuditLog, 365))
			// Init agent memory table.
			if err := initMemoryDB(cfg.HistoryDB); err != nil {
				logWarn("init agent_memory failed", "error", err)
			}
			// Init session tables.
			if err := initSessionDB(cfg.HistoryDB); err != nil {
				logWarn("init sessions failed", "error", err)
			}
			// Init SLA tables.
			initSLADB(cfg.HistoryDB)
			// Init offline queue table.
			if err := initQueueDB(cfg.HistoryDB); err != nil {
				logWarn("init offline_queue failed", "error", err)
			}
			// Init reflections table.
			if err := initReflectionDB(cfg.HistoryDB); err != nil {
				logWarn("init reflections failed", "error", err)
			}
			// Init trust events table.
			initTrustDB(cfg.HistoryDB)
			// Init config versioning table.
			if err := initVersionDB(cfg.HistoryDB); err != nil {
				logWarn("init config_versions failed", "error", err)
			}
			// Init agent communication table.
			if err := initAgentCommDB(cfg.HistoryDB); err != nil {
				logWarn("init agent_messages failed", "error", err)
			}
			// --- P18.4: Self-Improving Skills --- Init skill usage table.
			if err := initSkillUsageTable(cfg.HistoryDB); err != nil {
				logWarn("init skill_usage failed", "error", err)
			}
			// --- P18.2: OAuth 2.0 Framework --- Init OAuth tokens table.
			if err := initOAuthTable(cfg.HistoryDB); err != nil {
				logWarn("init oauth_tokens failed", "error", err)
			}
			// Init token telemetry table.
			if err := initTokenTelemetry(cfg.HistoryDB); err != nil {
				logWarn("init token_telemetry failed", "error", err)
			}
			// Init projects table.
			if err := initProjectsDB(cfg.HistoryDB); err != nil {
				logWarn("init projects failed", "error", err)
			}
		}

		// --- P23.1: User Profile & Emotional Memory ---
		if cfg.UserProfile.Enabled && cfg.HistoryDB != "" {
			if err := initUserProfileDB(cfg.HistoryDB); err != nil {
				logWarn("init user_profiles failed", "error", err)
			} else {
				globalUserProfileService = newUserProfileService(cfg)
				logInfo("user profile service initialized", "sentiment", cfg.UserProfile.SentimentEnabled)
			}
		}

		// --- P23.7: Reliability & Operations --- Init tables.
		if cfg.HistoryDB != "" {
			if err := initOpsDB(cfg.HistoryDB); err != nil {
				logWarn("init ops tables failed", "error", err)
			}
		}

		// --- P23.4: Financial Tracking ---
		if cfg.Finance.Enabled && cfg.HistoryDB != "" {
			if err := initFinanceDB(cfg.HistoryDB); err != nil {
				logWarn("init finance tables failed", "error", err)
			} else {
				globalFinanceService = newFinanceService(cfg)
				logInfo("finance service initialized", "defaultCurrency", cfg.Finance.defaultCurrencyOrTWD())
			}
		}

		// --- P23.2: Task Management ---
		if cfg.TaskManager.Enabled && cfg.HistoryDB != "" {
			if err := initTaskManagerDB(cfg.HistoryDB); err != nil {
				logWarn("init task_manager tables failed", "error", err)
			} else {
				globalTaskManager = newTaskManagerService(cfg)
				logInfo("task manager initialized", "defaultProject", cfg.TaskManager.defaultProjectOrInbox())
			}
		}

		// --- P23.3: File & Document Processing ---
		if cfg.FileManager.Enabled && cfg.HistoryDB != "" {
			if err := initFileManagerDB(cfg.HistoryDB); err != nil {
				logWarn("init file_manager tables failed", "error", err)
			} else {
				globalFileManager = newFileManagerService(cfg)
				logInfo("file manager initialized", "storageDir", cfg.FileManager.storageDirOrDefault(cfg.baseDir))
			}
		}

		// --- P23.5: Media Control ---
		if cfg.Spotify.Enabled {
			globalSpotifyService = newSpotifyService(cfg)
			logInfo("spotify service initialized", "market", cfg.Spotify.marketOrDefault())
		}
		if cfg.Podcast.Enabled && cfg.HistoryDB != "" {
			if err := initPodcastDB(cfg.HistoryDB); err != nil {
				logWarn("init podcast tables failed", "error", err)
			} else {
				globalPodcastService = newPodcastService(cfg.HistoryDB)
				logInfo("podcast service initialized")
			}
		}

		// --- P23.6: Multi-User / Family Mode ---
		if cfg.Family.Enabled && cfg.HistoryDB != "" {
			if err := initFamilyDB(cfg.HistoryDB); err != nil {
				logWarn("init family tables failed", "error", err)
			} else {
				svc, err := newFamilyService(cfg, cfg.Family)
				if err != nil {
					logWarn("init family service failed", "error", err)
				} else {
					globalFamilyService = svc
					logInfo("family mode initialized", "maxUsers", cfg.Family.maxUsersOrDefault())
				}
			}
		}

		// --- P24.2: Contact & Social Graph ---
		if cfg.HistoryDB != "" {
			if err := initContactsDB(cfg.HistoryDB); err != nil {
				logWarn("init contacts tables failed", "error", err)
			} else {
				globalContactsService = newContactsService(cfg)
				logInfo("contacts service initialized")
			}
		}

		// --- P24.3: Life Insights Engine ---
		if cfg.HistoryDB != "" {
			if err := initInsightsDB(cfg.HistoryDB); err != nil {
				logWarn("init insights tables failed", "error", err)
			} else {
				globalInsightsEngine = newInsightsEngine(cfg)
				logInfo("insights engine initialized")
			}
		}

		// --- P24.4: Smart Scheduling ---
		globalSchedulingService = newSchedulingService(cfg)
		logInfo("scheduling service initialized")

		// --- P24.5: Habit & Wellness Tracking ---
		if cfg.HistoryDB != "" {
			if err := initHabitsDB(cfg.HistoryDB); err != nil {
				logWarn("init habits tables failed", "error", err)
			} else {
				globalHabitsService = newHabitsService(cfg)
				logInfo("habits service initialized")
			}
		}

		// --- P24.6: Goal Planning & Autonomy ---
		if cfg.HistoryDB != "" {
			if err := initGoalsDB(cfg.HistoryDB); err != nil {
				logWarn("init goals tables failed", "error", err)
			} else {
				globalGoalsService = newGoalsService(cfg)
				logInfo("goals service initialized")
			}
		}

		// --- P29.2: Time Tracking ---
		if cfg.TimeTracking.Enabled && cfg.HistoryDB != "" {
			if err := initTimeTrackingDB(cfg.HistoryDB); err != nil {
				logWarn("init time_entries failed", "error", err)
			} else {
				globalTimeTracking = newTimeTrackingService(cfg)
				logInfo("time tracking initialized")
			}
		}

		// --- P29.0: Lifecycle Automation ---
		if cfg.Lifecycle.Enabled {
			globalLifecycleEngine = newLifecycleEngine(cfg)
			logInfo("lifecycle engine initialized",
				"autoHabitSuggest", cfg.Lifecycle.AutoHabitSuggest,
				"autoInsightAction", cfg.Lifecycle.AutoInsightAction,
				"autoBirthdayRemind", cfg.Lifecycle.AutoBirthdayRemind)
		}

		// --- P24.7: Morning Briefing & Evening Wrap ---
		if cfg.HistoryDB != "" {
			globalBriefingService = newBriefingService(cfg)
			logInfo("briefing service initialized")
		}

		// Warn about incoming webhooks without secrets.
		for name, wh := range cfg.IncomingWebhooks {
			if wh.Secret == "" {
				logWarn("incoming webhook has no secret configured", "webhook", name)
			}
		}

		// Init outputs directory + cleanup.
		os.MkdirAll(filepath.Join(cfg.baseDir, "outputs"), 0o755)
		cleanupOutputs(cfg.baseDir, retentionDays(cfg.Retention.Outputs, 30))

		// Init uploads directory + cleanup.
		uploadDir := initUploadDir(cfg.baseDir)
		cleanupUploads(uploadDir, retentionDays(cfg.Retention.Uploads, 7))
		logInfo("uploads dir initialized", "path", uploadDir)

		// Init knowledge base directory.
		initKnowledgeDir(cfg.baseDir)
		logInfo("knowledge base initialized", "path", cfg.KnowledgeDir)

		// Init tool registry.
		cfg.toolRegistry = NewToolRegistry(cfg)
		logInfo("tool registry initialized", "tools", len(cfg.toolRegistry.List()))

		// Init directories for agents, workspace, and runtime.
		if err := initDirectories(cfg); err != nil {
			logWarn("init directories failed", "error", err)
		} else {
			logInfo("directories initialized", "agents", len(cfg.Agents))
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// --- P23.7: Reliability & Operations --- Start background services.
		if cfg.Ops.MessageQueue.Enabled && cfg.HistoryDB != "" {
			mqEngine := newMessageQueueEngine(cfg)
			mqEngine.Start(ctx)
			logInfo("message queue started")
		}
		if cfg.Ops.BackupSchedule != "" && cfg.HistoryDB != "" {
			bsched := newBackupScheduler(cfg)
			bsched.Start(ctx)
			logInfo("backup scheduler started", "schedule", cfg.Ops.BackupSchedule, "retain", cfg.Ops.backupRetainOrDefault())
		}

		// Periodic cleanup (daily): uses retention config for all tables.
		go func() {
			ticker := time.NewTicker(24 * time.Hour)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					results := runRetention(cfg)
					for _, r := range results {
						if r.Error != "" {
							logWarn("retention cleanup error", "table", r.Table, "error", r.Error)
						}
					}
				}
			}
		}()

		// Notification setup.
		var bot *Bot
		extraNotifiers := buildNotifiers(cfg)

		// Build base fallback function (Telegram bot direct send).
		var telegramFn func(string)
		if cfg.Telegram.Enabled && cfg.Telegram.BotToken != "" {
			telegramFn = func(text string) {
				if bot != nil {
					bot.sendNotify(text)
				}
			}
		}

		// Create notification intelligence engine.
		notifyEngine := NewNotificationEngine(cfg, extraNotifiers, telegramFn)
		notifyEngine.Start()
		defer notifyEngine.Stop()

		// Backward-compatible notifyFn wraps through the engine.
		notifyFn := wrapNotifyFn(notifyEngine, PriorityHigh)

		if len(extraNotifiers) > 0 {
			names := make([]string, len(extraNotifiers))
			for i, n := range extraNotifiers {
				names[i] = n.Name()
			}
			logInfo("notifications configured", "notifiers", strings.Join(names, ", "),
				"batchInterval", notifyEngine.batchInterval.String())
		}

		// Security monitor.
		secMon := newSecurityMonitor(cfg, notifyFn)
		if secMon != nil {
			logInfo("security alerts enabled", "threshold", cfg.SecurityAlert.FailThreshold, "windowMin", cfg.SecurityAlert.FailWindowMin)
		}

		// Budget alert tracker.
		budgetTracker := newBudgetAlertTracker()

		// SLA monitor.
		slaCheck := newSLAChecker(cfg, notifyFn)

		// Cron engine.
		cron := newCronEngine(cfg, sem, childSem, notifyFn)
		if err := cron.loadJobs(); err != nil {
			logWarn("cron load error, continuing without cron", "error", err)
		} else {
			// Register daily notes job if enabled.
			registerDailyNotesJob(ctx, cfg, cron)
			cron.start(ctx)
		}

		// Proactive engine.
		var proactiveEngine *ProactiveEngine
		if cfg.Proactive.Enabled {
			proactiveEngine = newProactiveEngine(cfg, state.broker)
			proactiveEngine.Start(ctx)
			logInfo("proactive engine started", "rules", len(cfg.Proactive.Rules))
		}

		// Group chat engine.
		var groupChatEngine *GroupChatEngine
		if cfg.GroupChat.Activation != "" {
			groupChatEngine = newGroupChatEngine(cfg)
			logInfo("group chat engine started", "activation", cfg.GroupChat.Activation)
		}

		// Start SLA monitor + budget alert goroutine.
		if cfg.SLA.Enabled {
			go func() {
				ticker := time.NewTicker(30 * time.Second) // check eligibility every 30s
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
						slaCheck.tick(ctx)
					}
				}
			}()
			logInfo("SLA monitor enabled", "interval", cfg.SLA.checkIntervalOrDefault().String(), "window", cfg.SLA.windowOrDefault().String())
		}

		// Start budget alert goroutine.
		if cfg.Budgets.Global.Daily > 0 || cfg.Budgets.Global.Weekly > 0 || cfg.Budgets.Global.Monthly > 0 || len(cfg.Budgets.Agents) > 0 {
			go func() {
				ticker := time.NewTicker(5 * time.Minute) // check every 5m
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
						checkAndNotifyBudgetAlerts(cfg, notifyFn, budgetTracker)
					}
				}
			}()
			logInfo("budget governance enabled",
				"daily", cfg.Budgets.Global.Daily,
				"weekly", cfg.Budgets.Global.Weekly,
				"monthly", cfg.Budgets.Global.Monthly,
				"autoDowngrade", cfg.Budgets.AutoDowngrade.Enabled,
				"paused", cfg.Budgets.Paused)
		}

		// Start offline queue drainer.
		if cfg.OfflineQueue.Enabled {
			drainer := &queueDrainer{
				cfg:      cfg,
				sem:      sem,
				childSem: childSem,
				state:    state,
				notifyFn: notifyFn,
				ttl:      cfg.OfflineQueue.ttlOrDefault(),
			}
			go drainer.run(ctx)
			logInfo("offline queue enabled", "ttl", drainer.ttl.String(), "maxItems", cfg.OfflineQueue.maxItemsOrDefault())
		}

		// Initialize Slack bot (uses HTTP push, no polling needed).
		var slackBot *SlackBot
		if cfg.Slack.Enabled && cfg.Slack.BotToken != "" {
			slackBot = newSlackBot(cfg, state, sem, childSem, cron)
			logInfo("slack bot enabled", "endpoint", "/slack/events")

			// Wire Slack into notification chain.
			prevNotifyFn := notifyFn
			notifyFn = func(text string) {
				if prevNotifyFn != nil {
					prevNotifyFn(text)
				}
				slackBot.sendSlackNotify(text)
			}
		}

		// Initialize Discord bot.
		var discordBot *DiscordBot
		if cfg.Discord.Enabled && cfg.Discord.BotToken != "" {
			discordBot = newDiscordBot(cfg, state, sem, childSem, cron)
			state.discordBot = discordBot // P14.1: store for interaction handler
			logInfo("discord bot enabled")

			// Wire Discord into notification chain.
			prevNotifyFn2 := notifyFn
			notifyFn = func(text string) {
				if prevNotifyFn2 != nil {
					prevNotifyFn2(text)
				}
				discordBot.sendNotify(text)
			}
		}

		// Start MCP host.
		var mcpHost *MCPHost
		if len(cfg.MCPServers) > 0 {
			mcpHost = newMCPHost(cfg, cfg.toolRegistry)
			if err := mcpHost.Start(ctx); err != nil {
				logError("MCP host start failed: %v", err)
			} else {
				logInfo("MCP host started", "servers", len(cfg.MCPServers))
			}
		}

		// Initialize metrics registry.
		initMetrics()

		// Initialize WhatsApp bot.
		var whatsappBot *WhatsAppBot
		if cfg.WhatsApp.Enabled && cfg.WhatsApp.PhoneNumberID != "" && cfg.WhatsApp.AccessToken != "" {
			whatsappBot = newWhatsAppBot(cfg, state, sem, childSem, cron)
			logInfo("whatsapp bot enabled", "endpoint", "/api/whatsapp/webhook")
		}

		// Initialize voice engine.
		var voiceEngine *VoiceEngine
		if cfg.Voice.STT.Enabled || cfg.Voice.TTS.Enabled {
			voiceEngine = newVoiceEngine(cfg)
			logInfo("voice engine initialized")
		}

		// Initialize agent communication DB.
		initAgentCommDB(cfg.HistoryDB)

		// --- P13.1: Plugin System --- Initialize plugin host.
		var pluginHost *PluginHost
		if len(cfg.Plugins) > 0 {
			pluginHost = NewPluginHost(cfg)
			pluginHost.AutoStart()
			logInfo("plugin host initialized", "plugins", len(cfg.Plugins))
		}

		// --- P13.2: Sandbox Plugin --- Initialize sandbox manager.
		if pluginHost != nil {
			sm := NewSandboxManager(cfg, pluginHost)
			if sm.PluginName() != "" {
				state.sandboxMgr = sm
				logInfo("sandbox manager initialized", "plugin", sm.PluginName())
			}
		}

		// --- P15.1: LINE Channel --- Initialize LINE bot.
		var lineBot *LINEBot
		if cfg.LINE.Enabled && cfg.LINE.ChannelSecret != "" && cfg.LINE.ChannelAccessToken != "" {
			lineBot = newLINEBot(cfg, state, sem, childSem)
			logInfo("line bot enabled", "endpoint", cfg.LINE.webhookPathOrDefault())
		}

		// --- P15.2: Matrix Channel --- Initialize Matrix bot.
		var matrixBot *MatrixBot
		if cfg.Matrix.Enabled && cfg.Matrix.Homeserver != "" && cfg.Matrix.AccessToken != "" {
			matrixBot = newMatrixBot(cfg, state, sem, childSem)
			logInfo("matrix bot enabled", "homeserver", cfg.Matrix.Homeserver, "userId", cfg.Matrix.UserID)
		}

		// --- P15.3: Teams Channel --- Initialize Teams bot.
		var teamsBot *TeamsBot
		if cfg.Teams.Enabled && cfg.Teams.AppID != "" && cfg.Teams.AppPassword != "" {
			teamsBot = newTeamsBot(cfg, state, sem, childSem)
			logInfo("teams bot enabled", "endpoint", "/api/teams/webhook")
		}

		// --- P15.4: Signal Channel --- Initialize Signal bot.
		var signalBot *SignalBot
		if cfg.Signal.Enabled && cfg.Signal.PhoneNumber != "" {
			signalBot = newSignalBot(cfg, state, sem, childSem)
			if cfg.Signal.PollingMode {
				signalBot.Start()
				logInfo("signal bot enabled (polling mode)", "interval", cfg.Signal.pollIntervalOrDefault())
			} else {
				logInfo("signal bot enabled", "endpoint", cfg.Signal.webhookPathOrDefault())
			}
		}

		// --- P15.5: Google Chat Channel --- Initialize Google Chat bot.
		var gchatBot *GoogleChatBot
		if cfg.GoogleChat.Enabled && cfg.GoogleChat.ServiceAccountKey != "" {
			var err error
			gchatBot, err = newGoogleChatBot(cfg, state, sem, childSem)
			if err != nil {
				logError("failed to initialize google chat bot", "error", err)
			} else {
				logInfo("google chat bot enabled", "endpoint", cfg.GoogleChat.webhookPathOrDefault())
			}
		}

		// --- P18.3: Workflow Triggers --- Initialize trigger engine.
		var triggerEngine *WorkflowTriggerEngine
		if len(cfg.WorkflowTriggers) > 0 {
			triggerEngine = newWorkflowTriggerEngine(cfg, state, sem, childSem, state.broker)
			triggerEngine.Start(ctx)
		}

		// --- P19.3: Smart Reminders --- Initialize reminder engine.
		var reminderEngine *ReminderEngine
		if cfg.Reminders.Enabled && cfg.HistoryDB != "" {
			if err := initReminderDB(cfg.HistoryDB); err != nil {
				logWarn("init reminders table failed", "error", err)
			} else {
				reminderEngine = newReminderEngine(cfg, notifyFn)
				reminderEngine.Start(ctx)
				globalReminderEngine = reminderEngine
				logInfo("reminder engine started", "checkInterval", cfg.Reminders.checkIntervalOrDefault().String(), "maxPerUser", cfg.Reminders.maxPerUserOrDefault())
			}
		}

		// --- P19.4: Notes/Obsidian Integration --- Initialize notes service.
		if cfg.Notes.Enabled {
			notesSvc := newNotesService(cfg)
			setGlobalNotesService(notesSvc)
			logInfo("notes service initialized", "vault", cfg.Notes.vaultPathResolved(cfg.baseDir))
		}

		// --- P19.5: Unified Presence/Typing Indicators --- Initialize presence manager.
		// Note: Telegram bot is registered after creation below.
		globalPresence = newPresenceManager()
		if slackBot != nil {
			globalPresence.RegisterSetter("slack", slackBot)
		}
		if discordBot != nil {
			globalPresence.RegisterSetter("discord", discordBot)
		}
		if whatsappBot != nil {
			globalPresence.RegisterSetter("whatsapp", whatsappBot)
		}
		if lineBot != nil {
			globalPresence.RegisterSetter("line", lineBot)
		}
		if teamsBot != nil {
			globalPresence.RegisterSetter("teams", teamsBot)
		}
		if signalBot != nil {
			globalPresence.RegisterSetter("signal", signalBot)
		}
		if gchatBot != nil {
			globalPresence.RegisterSetter("gchat", gchatBot)
		}
		logInfo("presence manager initialized", "setters", len(globalPresence.setters))

		// --- P20.1: Home Assistant --- Initialize HA service.
		if cfg.HomeAssistant.Enabled && cfg.HomeAssistant.BaseURL != "" {
			globalHAService = newHAService(cfg.HomeAssistant)
			if cfg.HomeAssistant.WebSocket {
				go globalHAService.StartEventListener(ctx, state.broker)
			}
			logInfo("home assistant enabled", "baseUrl", cfg.HomeAssistant.BaseURL)
		}

		// --- P20.4: Device Actions --- Ensure output dir exists.
		if cfg.Device.Enabled {
			ensureDeviceOutputDir(cfg)
		}

		// --- P19.1: Gmail Integration ---
		if cfg.Gmail.Enabled {
			globalGmailService = &GmailService{cfg: cfg}
			logInfo("gmail integration enabled")
		}

		// --- P19.2: Google Calendar Integration ---
		if cfg.Calendar.Enabled {
			globalCalendarService = &CalendarService{cfg: cfg}
			logInfo("calendar integration enabled")
		}

		// --- P20.3: Twitter/X Integration ---
		if cfg.Twitter.Enabled {
			globalTwitterService = newTwitterService(cfg)
			logInfo("twitter integration enabled")
		}

		// --- P21.6: Chrome Extension Relay ---
		if cfg.BrowserRelay.Enabled {
			globalBrowserRelay = newBrowserRelay(&cfg.BrowserRelay)
			go func() {
				if err := globalBrowserRelay.Start(ctx); err != nil && err != http.ErrServerClosed {
					logWarn("browser relay stopped", "error", err)
				}
			}()
			logInfo("browser relay enabled", "port", cfg.BrowserRelay.Port)
		}

		// --- P20.2: iMessage via BlueBubbles --- Initialize iMessage bot.
		var imessageBot *IMessageBot
		if cfg.IMessage.Enabled && cfg.IMessage.ServerURL != "" {
			imessageBot = newIMessageBot(cfg, state, sem, childSem)
			globalIMessageBot = imessageBot
			logInfo("imessage bot enabled", "endpoint", cfg.IMessage.webhookPathOrDefault())
			if globalPresence != nil {
				globalPresence.RegisterSetter("imessage", imessageBot)
			}
		}

		// P28.1: Collect all services into App container.
		app := &App{
			Cfg:         cfg,
			UserProfile: globalUserProfileService,
			Finance:     globalFinanceService,
			TaskManager: globalTaskManager,
			FileManager: globalFileManager,
			Spotify:     globalSpotifyService,
			Podcast:     globalPodcastService,
			Family:      globalFamilyService,
			Contacts:    globalContactsService,
			Insights:    globalInsightsEngine,
			Scheduling:  globalSchedulingService,
			Habits:      globalHabitsService,
			Goals:       globalGoalsService,
			Briefing:    globalBriefingService,
			Lifecycle:   globalLifecycleEngine,
			TimeTracking: globalTimeTracking,
			OAuth:       globalOAuthManager,
			Gmail:       globalGmailService,
			Calendar:    globalCalendarService,
			Twitter:     globalTwitterService,
			HA:          globalHAService,
			Drive:       globalDriveService,
			Dropbox:     globalDropboxService,
			Browser:     globalBrowserRelay,
			IMessage:    globalIMessageBot,
			SpawnTracker:        globalSpawnTracker,
			JudgeCache:          globalJudgeCache,
			ImageGenLimiter:     globalImageGenLimiter,
			Presence:    globalPresence,
			Reminder:    reminderEngine,
		}

		// HTTP server.
		drainCh := make(chan struct{}, 1)
		srvInstance := &Server{
			cfg: cfg, app: app, state: state, sem: sem, childSem: childSem, cron: cron, secMon: secMon, mcpHost: mcpHost,
			proactiveEngine: proactiveEngine, groupChatEngine: groupChatEngine, voiceEngine: voiceEngine,
			slackBot: slackBot, whatsappBot: whatsappBot, pluginHost: pluginHost,
			lineBot: lineBot, teamsBot: teamsBot, signalBot: signalBot, gchatBot: gchatBot, imessageBot: imessageBot,
			DegradedServices: degradedServices,
			drainCh:          drainCh,
		}
		srv := startHTTPServer(srvInstance)

		// Report degraded services.
		if len(degradedServices) > 0 {
			logWarn("starting in degraded mode", "failedServices", strings.Join(degradedServices, ", "))
		}

		// Config hot-reload on SIGHUP.
		sighupCh := make(chan os.Signal, 1)
		signal.Notify(sighupCh, syscall.SIGHUP)
		go func() {
			for range sighupCh {
				logInfo("received SIGHUP, reloading config")
				newCfg, err := tryLoadConfig(*configPath)
				if err != nil {
					logError("config reload failed", "error", err)
					continue
				}
				// Preserve runtime-only field not set by tryLoadConfig.
				srvInstance.cfgMu.RLock()
				oldCfg := srvInstance.cfg
				srvInstance.cfgMu.RUnlock()
				newCfg.toolRegistry = oldCfg.toolRegistry

				// Log config diff.
				logConfigDiff(oldCfg, newCfg)

				// Atomic swap.
				srvInstance.ReloadConfig(newCfg)
				logInfo("config reloaded successfully")
			}
		}()

		// Start Telegram bot.
		if cfg.Telegram.Enabled && cfg.Telegram.BotToken != "" {
			bot = newBot(cfg, state, sem, childSem, cron)
			// Wire up keyboard notification for approval gate.
			cron.notifyKeyboardFn = func(text string, keyboard [][]tgInlineButton) {
				bot.replyWithKeyboard(bot.chatID, text, keyboard)
			}
			// Register Telegram bot for presence/typing indicators.
			if globalPresence != nil {
				globalPresence.RegisterSetter("telegram", bot)
			}
			go bot.pollLoop(ctx)
		} else {
			logInfo("telegram disabled or no bot token, HTTP-only mode")
		}

		// Start Discord bot.
		if discordBot != nil {
			go discordBot.Run(ctx)
		}

		// --- P15.2: Matrix Channel --- Start Matrix bot.
		if matrixBot != nil {
			go matrixBot.Run(ctx)
		}

		logInfo("tetora ready", "healthz", fmt.Sprintf("http://%s/healthz", cfg.ListenAddr))

		// Wait for shutdown signal or drain request.
		select {
		case <-sigCh:
			logInfo("shutting down")
		case <-drainCh:
			logInfo("drain requested: waiting for active agents to complete")
			// Wait for all running tasks to finish (poll with ticker).
			drainTicker := time.NewTicker(2 * time.Second)
			drainDeadline := time.Now().Add(10 * time.Minute)
		drainLoop:
			for {
				select {
				case <-sigCh:
					// Force shutdown even during drain.
					logInfo("force shutdown during drain")
					drainTicker.Stop()
					break drainLoop
				case <-drainTicker.C:
					state.mu.Lock()
					active := len(state.running)
					state.mu.Unlock()
					if active == 0 {
						logInfo("drain complete: all agents finished")
						drainTicker.Stop()
						break drainLoop
					}
					if time.Now().After(drainDeadline) {
						logWarn("drain timeout: forcing shutdown", "stillActive", active)
						drainTicker.Stop()
						break drainLoop
					}
					logInfo("draining: waiting for agents", "active", active)
				}
			}
			logInfo("shutting down after drain")
		}

		// Stop TaskBoard dispatcher (wait for in-flight tasks).
		if srvInstance.taskBoardDispatcher != nil {
			srvInstance.taskBoardDispatcher.Stop()
		}

		if signalBot != nil {
			signalBot.Stop()
		}
		if discordBot != nil {
			discordBot.Stop()
		}

		// --- P15.2: Matrix Channel --- Stop Matrix bot.
		if matrixBot != nil {
			matrixBot.Stop()
		}

		// Cancel any running dispatch first.
		state.mu.Lock()
		if state.cancel != nil {
			state.cancel()
		}
		state.mu.Unlock()

		// Cancel global context (stops accepting new work).
		cancel()

		// --- P18.3: Workflow Triggers --- Stop trigger engine.
		if triggerEngine != nil {
			triggerEngine.Stop()
		}

		// --- P19.3: Smart Reminders --- Stop reminder engine.
		if reminderEngine != nil {
			reminderEngine.Stop()
		}

		// Stop cron scheduler (waits for running jobs up to 30s).
		cron.stop()

		// Stop MCP host.
		if mcpHost != nil {
			mcpHost.Stop()
		}

		// --- P13.2: Sandbox Plugin --- Destroy all active sandboxes.
		if state.sandboxMgr != nil {
			state.sandboxMgr.DestroyAll()
		}

		// --- P13.1: Plugin System --- Stop plugin host.
		if pluginHost != nil {
			pluginHost.StopAll()
		}

		// Stop proactive engine.
		if proactiveEngine != nil {
			proactiveEngine.Stop()
		}

		// Shut down HTTP server.
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutCancel()
		srv.Shutdown(shutCtx)

		logInfo("tetora stopped")

	} else {
		// --- CLI mode ---
		tasks := readTaskInput(*tasksJSON, *filePath)
		for i := range tasks {
			fillDefaults(cfg, &tasks[i])
			tasks[i].Source = "cli"
		}

		if cfg.Log {
			logInfo("tetora dispatching", "tasks", len(tasks), "maxConcurrent", cfg.MaxConcurrent)
		}

		// Start HTTP monitor in background.
		srv := startHTTPServer(&Server{cfg: cfg, state: state, sem: sem, childSem: childSem})

		// Handle signals — cancel dispatch.
		go func() {
			<-sigCh
			logInfo("received signal, cancelling")
			state.mu.Lock()
			if state.cancel != nil {
				state.cancel()
			}
			state.mu.Unlock()
		}()

		dispatchCtx := withTraceID(context.Background(), newTraceID("cli"))
		result := dispatch(dispatchCtx, cfg, tasks, state, sem, childSem)

		// Shut down HTTP server.
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer shutCancel()
		srv.Shutdown(shutCtx)

		// Output JSON to stdout.
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(result)

		// Telegram notification.
		if *notify {
			msg := formatTelegramResult(result)
			if err := sendTelegramNotify(&cfg.Telegram, msg); err != nil {
				logError("telegram notify failed", "error", err)
			}
		}

		// Print summary to stderr.
		fmt.Fprintf(os.Stderr, "\n%s\n", result.Summary)

		// Exit non-zero if any task failed.
		for _, t := range result.Tasks {
			if t.Status != "success" {
				os.Exit(1)
			}
		}
	}
}

// logConfigDiff logs which config fields changed between old and new config.
func logConfigDiff(old, new *Config) {
	var changes []string
	if old.ListenAddr != new.ListenAddr {
		changes = append(changes, fmt.Sprintf("listenAddr: %s → %s", old.ListenAddr, new.ListenAddr))
	}
	if old.DefaultModel != new.DefaultModel {
		changes = append(changes, fmt.Sprintf("defaultModel: %s → %s", old.DefaultModel, new.DefaultModel))
	}
	if old.MaxConcurrent != new.MaxConcurrent {
		changes = append(changes, fmt.Sprintf("maxConcurrent: %d → %d", old.MaxConcurrent, new.MaxConcurrent))
	}
	if old.DefaultTimeout != new.DefaultTimeout {
		changes = append(changes, fmt.Sprintf("defaultTimeout: %s → %s", old.DefaultTimeout, new.DefaultTimeout))
	}
	if old.DefaultBudget != new.DefaultBudget {
		changes = append(changes, fmt.Sprintf("defaultBudget: %.2f → %.2f", old.DefaultBudget, new.DefaultBudget))
	}
	if len(old.Agents) != len(new.Agents) {
		changes = append(changes, fmt.Sprintf("roles: %d → %d", len(old.Agents), len(new.Agents)))
	}
	if old.Security.InjectionDefense.Level != new.Security.InjectionDefense.Level {
		changes = append(changes, fmt.Sprintf("injectionDefense.level: %s → %s", old.Security.InjectionDefense.Level, new.Security.InjectionDefense.Level))
	}
	if len(changes) > 0 {
		logInfo("config changes detected", "changes", strings.Join(changes, "; "))
	} else {
		logInfo("config reloaded, no significant changes detected")
	}
}

func readTaskInput(tasksFlag, fileFlag string) []Task {
	var data []byte

	if tasksFlag != "" {
		data = []byte(tasksFlag)
	} else if fileFlag != "" {
		var err error
		data, err = os.ReadFile(fileFlag)
		if err != nil {
			logError("read task file failed", "error", err)
			os.Exit(1)
		}
	} else {
		// Try stdin if not a TTY.
		stat, _ := os.Stdin.Stat()
		if (stat.Mode() & os.ModeCharDevice) == 0 {
			var err error
			data, err = io.ReadAll(os.Stdin)
			if err != nil {
				logError("read stdin failed", "error", err)
				os.Exit(1)
			}
		}
	}

	if len(data) == 0 {
		logError("no tasks provided, use --tasks, --file, or pipe JSON to stdin")
		os.Exit(1)
	}

	var tasks []Task
	if err := json.Unmarshal(data, &tasks); err != nil {
		logError("parse tasks JSON failed", "error", err)
		os.Exit(1)
	}
	return tasks
}
