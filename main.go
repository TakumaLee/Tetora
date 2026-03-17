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

	"tetora/internal/cli"
	"tetora/internal/log"
	"tetora/internal/completion"
	"tetora/internal/db"
	"tetora/internal/sla"
	"tetora/internal/knowledge"
	"tetora/internal/storage"
	"tetora/internal/upload"
	"strings"
	"syscall"
	"time"

	"tetora/internal/cost"
	"tetora/internal/messaging/gchat"
	imessagebot "tetora/internal/messaging/imessage"
	linebot "tetora/internal/messaging/line"
	"tetora/internal/messaging/matrix"
	signalbot "tetora/internal/messaging/signal"
	slackbot "tetora/internal/messaging/slack"
	teamsbot "tetora/internal/messaging/teams"
	tgbot "tetora/internal/messaging/telegram"
	"tetora/internal/messaging/whatsapp"
	"tetora/internal/telemetry"
	"tetora/internal/trace"
)

// initLogger creates the global logger from config.
func initLogger(cfg LoggingConfig, baseDir string) {
	l := log.Init(log.Config{
		Level:     cfg.LevelOrDefault(),
		Format:    cfg.FormatOrDefault(),
		File:      cfg.File,
		MaxSizeMB: cfg.MaxSizeMBOrDefault(),
		MaxFiles:  cfg.MaxFilesOrDefault(),
	}, baseDir)
	l.SetTraceExtractor(trace.IDFromContext)
	log.SetDefault(l)
}

func main() {
	// Set CLI version before routing.
	cli.TetoraVersion = tetoraVersion

	// Subcommand routing.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		// --- Commands routed to internal/cli ---
		case "doctor":
			cli.CmdDoctor()
			return
		case "health":
			cli.CmdHealth(os.Args[2:])
			return
		case "service":
			cli.CmdService(os.Args[2:])
			return
		case "job":
			cli.CmdJob(os.Args[2:])
			return
		case "history":
			cli.CmdHistory(os.Args[2:])
			return
		case "agent":
			cli.CmdAgent(os.Args[2:])
			return
		case "status":
			cli.CmdStatus(os.Args[2:])
			return
		case "dispatch":
			cli.CmdDispatch(os.Args[2:])
			return
		case "route":
			cli.CmdRouteDispatch(os.Args[2:])
			return
		case "config":
			cli.CmdConfig(os.Args[2:])
			return
		case "logs", "log":
			cli.CmdLogs(os.Args[2:])
			return
		case "prompt":
			cli.CmdPrompt(os.Args[2:])
			return
		case "memory":
			cli.CmdMemory(os.Args[2:])
			return
		case "mcp":
			cli.CmdMCP(os.Args[2:])
			return
		case "hooks":
			cli.CmdHooks(os.Args[2:])
			return
		case "knowledge":
			cli.CmdKnowledge(os.Args[2:])
			return
		case "skill":
			cli.CmdSkill(os.Args[2:])
			return
		case "security":
			cli.CmdSecurity(os.Args[2:])
			return
		case "session", "sessions":
			cli.CmdSession(os.Args[2:])
			return
		case "budget":
			cli.CmdBudget(os.Args[2:])
			return
		case "usage":
			cli.CmdUsage(os.Args[2:])
			return
		case "trust":
			cli.CmdTrust(os.Args[2:])
			return
		case "webhook":
			cli.CmdWebhook(os.Args[2:])
			return
		case "data":
			cli.CmdData(os.Args[2:])
			return
		case "oauth":
			cli.CmdOAuth(os.Args[2:])
			return
		case "backup":
			cli.CmdBackup(os.Args[2:])
			return
		case "restore":
			cli.CmdRestore(os.Args[2:])
			return
		case "mirror":
			cli.CmdMirror(os.Args[2:])
			return
		case "discord":
			cli.CmdDiscord(os.Args[2:])
			return
		case "access":
			cli.CmdAccess(os.Args[2:])
			return
		case "release":
			cli.CmdRelease(os.Args[2:])
			return
		case "import":
			if len(os.Args) > 2 {
				switch os.Args[2] {
				case "config":
					cli.CmdImportConfig(os.Args[3:])
				default:
					fmt.Fprintln(os.Stderr, "Usage: tetora import <config>")
					os.Exit(1)
				}
			} else {
				fmt.Fprintln(os.Stderr, "Usage: tetora import <config>")
				os.Exit(1)
			}
			return

		// --- Commands staying in root (deeply coupled) ---
		case "init":
			cmdInit()
			return
		case "workflow":
			cmdWorkflow(os.Args[2:])
			return
		case "quick":
			cmdQuick(os.Args[2:], loadConfig(""))
			return
		case "pairing":
			cmdPairing(os.Args[2:])
			return
		case "mcp-server":
			cmdMCPServer()
			return
		case "proactive":
			runProactive(os.Args[2:])
			return
		case "compact":
			runCompaction(os.Args[2:])
			return
		case "plugin":
			cmdPlugin(os.Args[2:])
			return
		case "task":
			cmdTask(os.Args[2:])
			return
		case "dashboard":
			cmdOpenDashboard()
			return
		case "migrate":
			if len(os.Args) > 2 && os.Args[2] == "encrypt" {
				cmdMigrateEncrypt()
			} else {
				fmt.Fprintln(os.Stderr, "Usage: tetora migrate <encrypt>")
				os.Exit(1)
			}
			return
		case "completion":
			completion.Run(os.Args[2:])
			return
		case "stop":
			cli.KillDaemonProcess()
			return
		case "start":
			cmdStart()
			return
		case "drain":
			fmt.Println("drain: not yet implemented")
			return
		case "restart":
			cmdRestart()
			return
		case "upgrade":
			cmdUpgrade(os.Args[2:])
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
	initLogger(cfg.Logging, cfg.BaseDir)

	// Shared concurrency semaphore — limits total concurrent claude sessions.
	sem := make(chan struct{}, cfg.MaxConcurrent)

	// Child semaphore — separate pool for sub-agent tasks (depth > 0).
	// Prevents deadlock when parent tasks hold sem slots and spawn children that also need slots.
	childSem := make(chan struct{}, childSemConcurrentOrDefault(cfg))

	// Initialize slot pressure guard if enabled.
	if cfg.SlotPressure.Enabled {
		cfg.Runtime.SlotPressureGuard = &SlotPressureGuard{
			cfg:    cfg.SlotPressure,
			sem:    sem,
			semCap: cfg.MaxConcurrent,
		}
		spg := cfg.Runtime.SlotPressureGuard.(*SlotPressureGuard)
		log.Info("slot pressure guard enabled",
			"reserved", spg.reservedSlots(),
			"warnThreshold", spg.warnThreshold())
	}

	state := newDispatchState()
	state.broker = newSSEBroker()

	// App is the single source of truth for all services.
	// Services are initialized into app fields below, then SyncToGlobals()
	// backfills global vars for callers that haven't migrated yet.
	app := &App{Cfg: cfg}

	// Initialize hooks event receiver.
	hookRecv := newHookReceiver(state.broker, cfg)
	cfg.Runtime.HookRecv = hookRecv

	// Signal handling.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	if *serve {
		// --- Daemon mode ---
		log.Info("tetora v2 starting", "maxConcurrent", cfg.MaxConcurrent, "childConcurrent", childSemConcurrentOrDefault(cfg))

		// Track degraded services for health reporting.
		var degradedServices []string

		// Init history DB.
		if cfg.HistoryDB != "" {
			if err := initHistoryDB(cfg.HistoryDB); err != nil {
				log.Warn("init history db failed", "error", err)
				degradedServices = append(degradedServices, "historyDB")
			} else {
				log.Info("history db initialized", "path", cfg.HistoryDB)

				// Init plan reviews table.
				if err := initPlanReviewDB(cfg.HistoryDB); err != nil {
					log.Warn("init plan_reviews table failed", "error", err)
				}

				// Set SQLite pragmas for reliability.
				if err := db.Pragma(cfg.HistoryDB); err != nil {
					log.Warn("set db pragmas failed", "error", err)
				} else {
					log.Info("db pragmas set", "mode", "WAL")
				}

				// Init embedding DB if enabled.
				if cfg.Embedding.Enabled {
					if err := initEmbeddingDB(cfg.HistoryDB); err != nil {
						log.Warn("init embedding db failed", "error", err)
						degradedServices = append(degradedServices, "embedding")
					} else {
						log.Info("embedding db initialized")
					}
				}

				// Cleanup records using retention config.
				if err := cleanupHistory(cfg.HistoryDB, retentionDays(cfg.Retention.History, 90)); err != nil {
					log.Warn("cleanup history failed", "error", err)
				}
			}
			// Init audit log table and start batched writer.
			if err := initAuditLog(cfg.HistoryDB); err != nil {
				log.Warn("init audit_log failed", "error", err)
			}
			startAuditWriter()
			cleanupAuditLog(cfg.HistoryDB, retentionDays(cfg.Retention.AuditLog, 365))
			// Init agent memory table.
			if err := initMemoryDB(cfg.HistoryDB); err != nil {
				log.Warn("init agent_memory failed", "error", err)
			}
			// Init session tables.
			if err := initSessionDB(cfg.HistoryDB); err != nil {
				log.Warn("init sessions failed", "error", err)
			}
			// Init SLA tables.
			sla.InitSLADB(cfg.HistoryDB)
			// Init offline queue table.
			if err := initQueueDB(cfg.HistoryDB); err != nil {
				log.Warn("init offline_queue failed", "error", err)
			}
			// Init reflections table.
			if err := initReflectionDB(cfg.HistoryDB); err != nil {
				log.Warn("init reflections failed", "error", err)
			}
			// Init trust events table.
			initTrustDB(cfg.HistoryDB)
			// Init config versioning table.
			if err := initVersionDB(cfg.HistoryDB); err != nil {
				log.Warn("init config_versions failed", "error", err)
			}
			// Init agent communication table.
			if err := initAgentCommDB(cfg.HistoryDB); err != nil {
				log.Warn("init agent_messages failed", "error", err)
			}
			// --- P18.4: Self-Improving Skills --- Init skill usage table.
			if err := initSkillUsageTable(cfg.HistoryDB); err != nil {
				log.Warn("init skill_usage failed", "error", err)
			}
			// --- P18.2: OAuth 2.0 Framework --- Init OAuth tokens table.
			if err := initOAuthTable(cfg.HistoryDB); err != nil {
				log.Warn("init oauth_tokens failed", "error", err)
			}
			// Init token telemetry table.
			if err := telemetry.Init(cfg.HistoryDB); err != nil {
				log.Warn("init token_telemetry failed", "error", err)
			}
			// Init projects table.
			if err := initProjectsDB(cfg.HistoryDB); err != nil {
				log.Warn("init projects failed", "error", err)
			}
			// Init workflow callbacks table (external steps).
			initCallbackTable(cfg.HistoryDB)
		}

		// --- P23.1: User Profile & Emotional Memory ---
		if cfg.UserProfile.Enabled && cfg.HistoryDB != "" {
			if err := initUserProfileDB(cfg.HistoryDB); err != nil {
				log.Warn("init user_profiles failed", "error", err)
			} else {
				app.UserProfile = newUserProfileService(cfg)
				log.Info("user profile service initialized", "sentiment", cfg.UserProfile.SentimentEnabled)
			}
		}

		// --- P23.7: Reliability & Operations --- Init tables.
		if cfg.HistoryDB != "" {
			if err := initOpsDB(cfg.HistoryDB); err != nil {
				log.Warn("init ops tables failed", "error", err)
			}
		}

		// --- P23.4: Financial Tracking ---
		if cfg.Finance.Enabled && cfg.HistoryDB != "" {
			if err := initFinanceDB(cfg.HistoryDB); err != nil {
				log.Warn("init finance tables failed", "error", err)
			} else {
				app.Finance = newFinanceService(cfg)
				log.Info("finance service initialized", "defaultCurrency", cfg.Finance.DefaultCurrencyOrTWD())
			}
		}

		// --- P23.2: Task Management ---
		if cfg.TaskManager.Enabled && cfg.HistoryDB != "" {
			if err := initTaskManagerDB(cfg.HistoryDB); err != nil {
				log.Warn("init task_manager tables failed", "error", err)
			} else {
				app.TaskManager = newTaskManagerService(cfg)
				log.Info("task manager initialized", "defaultProject", cfg.TaskManager.DefaultProjectOrInbox())
			}
		}

		// --- P23.3: File & Document Processing ---
		if cfg.FileManager.Enabled && cfg.HistoryDB != "" {
			if err := storage.InitDB(cfg.HistoryDB); err != nil {
				log.Warn("init file_manager tables failed", "error", err)
			} else {
				app.FileManager = newFileManagerService(cfg)
				log.Info("file manager initialized", "storageDir", cfg.FileManager.StorageDirOrDefault(cfg.BaseDir))
			}
		}

		// --- P23.5: Media Control ---
		if cfg.Spotify.Enabled {
			app.Spotify = newSpotifyService(cfg)
			log.Info("spotify service initialized", "market", cfg.Spotify.MarketOrDefault())
		}
		if cfg.Podcast.Enabled && cfg.HistoryDB != "" {
			if err := initPodcastDB(cfg.HistoryDB); err != nil {
				log.Warn("init podcast tables failed", "error", err)
			} else {
				app.Podcast = newPodcastService(cfg.HistoryDB)
				log.Info("podcast service initialized")
			}
		}

		// --- P23.6: Multi-User / Family Mode ---
		if cfg.Family.Enabled && cfg.HistoryDB != "" {
			if err := initFamilyDB(cfg.HistoryDB); err != nil {
				log.Warn("init family tables failed", "error", err)
			} else {
				svc, err := newFamilyService(cfg, cfg.Family)
				if err != nil {
					log.Warn("init family service failed", "error", err)
				} else {
					app.Family = svc
					log.Info("family mode initialized", "maxUsers", cfg.Family.MaxUsersOrDefault())
				}
			}
		}

		// --- P24.2: Contact & Social Graph ---
		if cfg.HistoryDB != "" {
			if err := initContactsDB(cfg.HistoryDB); err != nil {
				log.Warn("init contacts tables failed", "error", err)
			} else {
				app.Contacts = newContactsService(cfg)
				log.Info("contacts service initialized")
			}
		}

		// --- P24.3: Life Insights Engine ---
		if cfg.HistoryDB != "" {
			if err := initInsightsDB(cfg.HistoryDB); err != nil {
				log.Warn("init insights tables failed", "error", err)
			} else {
				app.Insights = newInsightsEngine(cfg)
				log.Info("insights engine initialized")
			}
		}

		// --- P24.4: Smart Scheduling ---
		app.Scheduling = newSchedulingService(cfg)
		log.Info("scheduling service initialized")

		// --- P24.5: Habit & Wellness Tracking ---
		if cfg.HistoryDB != "" {
			if err := initHabitsDB(cfg.HistoryDB); err != nil {
				log.Warn("init habits tables failed", "error", err)
			} else {
				app.Habits = newHabitsService(cfg)
				log.Info("habits service initialized")
			}
		}

		// --- P24.6: Goal Planning & Autonomy ---
		if cfg.HistoryDB != "" {
			if err := initGoalsDB(cfg.HistoryDB); err != nil {
				log.Warn("init goals tables failed", "error", err)
			} else {
				app.Goals = newGoalsService(cfg)
				log.Info("goals service initialized")
			}
		}

		// --- P29.2: Time Tracking ---
		if cfg.TimeTracking.Enabled && cfg.HistoryDB != "" {
			if err := initTimeTrackingDB(cfg.HistoryDB); err != nil {
				log.Warn("init time_entries failed", "error", err)
			} else {
				app.TimeTracking = newTimeTrackingService(cfg)
				log.Info("time tracking initialized")
			}
		}

		// --- P29.0: Lifecycle Automation ---
		if cfg.Lifecycle.Enabled {
			app.Lifecycle = newLifecycleEngine(cfg)
			log.Info("lifecycle engine initialized",
				"autoHabitSuggest", cfg.Lifecycle.AutoHabitSuggest,
				"autoInsightAction", cfg.Lifecycle.AutoInsightAction,
				"autoBirthdayRemind", cfg.Lifecycle.AutoBirthdayRemind)
		}

		// --- P24.7: Morning Briefing & Evening Wrap ---
		if cfg.HistoryDB != "" {
			app.Briefing = newBriefingService(cfg)
			log.Info("briefing service initialized")
		}

		// Warn about incoming webhooks without secrets.
		for name, wh := range cfg.IncomingWebhooks {
			if wh.Secret == "" {
				log.Warn("incoming webhook has no secret configured", "webhook", name)
			}
		}

		// Init outputs directory + cleanup.
		os.MkdirAll(filepath.Join(cfg.BaseDir, "outputs"), 0o755)
		cleanupOutputs(cfg.BaseDir, retentionDays(cfg.Retention.Outputs, 30))

		// Init uploads directory + cleanup.
		uploadDir := upload.InitDir(cfg.BaseDir)
		upload.Cleanup(uploadDir, retentionDays(cfg.Retention.Uploads, 7))
		log.Info("uploads dir initialized", "path", uploadDir)

		// Init knowledge base directory.
		knowledge.InitDir(cfg.BaseDir)
		log.Info("knowledge base initialized", "path", cfg.KnowledgeDir)

		// Init tool registry.
		cfg.Runtime.ToolRegistry = NewToolRegistry(cfg)
		log.Info("tool registry initialized", "tools", len(cfg.Runtime.ToolRegistry.(*ToolRegistry).List()))

		// Init directories for agents, workspace, and runtime.
		if err := initDirectories(cfg); err != nil {
			log.Warn("init directories failed", "error", err)
		} else {
			log.Info("directories initialized", "agents", len(cfg.Agents))
		}

		ctx, cancel := context.WithCancel(context.Background())
		ctx = withApp(ctx, app)
		defer cancel()

		// --- P23.7: Reliability & Operations --- Start background services.
		if cfg.Ops.MessageQueue.Enabled && cfg.HistoryDB != "" {
			mqEngine := newMessageQueueEngine(cfg)
			mqEngine.Start(ctx)
			log.Info("message queue started")
		}
		if cfg.Ops.BackupSchedule != "" && cfg.HistoryDB != "" {
			bsched := newBackupScheduler(cfg)
			bsched.Start(ctx)
			log.Info("backup scheduler started", "schedule", cfg.Ops.BackupSchedule, "retain", cfg.Ops.BackupRetainOrDefault())
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
							log.Warn("retention cleanup error", "table", r.Table, "error", r.Error)
						}
					}
					cleanupExpiredCallbacks(cfg.HistoryDB)
				}
			}
		}()

		// Notification setup.
		var bot *tgbot.Bot
		extraNotifiers := buildNotifiers(cfg)

		// Build base fallback function (Telegram bot direct send).
		var telegramFn func(string)
		if cfg.Telegram.Enabled && cfg.Telegram.BotToken != "" {
			telegramFn = func(text string) {
				if bot != nil {
					bot.SendNotify(text)
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
			log.Info("notifications configured", "notifiers", strings.Join(names, ", "),
				"batchInterval", notifyEngine.batchInterval.String())
		}

		// Security monitor.
		secMon := newSecurityMonitor(cfg, notifyFn)
		if secMon != nil {
			log.Info("security alerts enabled", "threshold", cfg.SecurityAlert.FailThreshold, "windowMin", cfg.SecurityAlert.FailWindowMin)
		}

		// Budget alert tracker.
		budgetTracker := cost.NewBudgetAlertTracker()

		// SLA monitor.
		slaCheck := newSLAChecker(cfg, notifyFn)

		// Cron engine.
		cron := newCronEngine(cfg, sem, childSem, notifyFn)
		if err := cron.loadJobs(); err != nil {
			log.Warn("cron load error, continuing without cron", "error", err)
		} else {
			// Register daily notes job if enabled.
			registerDailyNotesJob(ctx, cfg, cron)
			cron.start(ctx)
		}

		// Startup disk check.
		if cfg.BaseDir != "" {
			free := diskFreeBytes(cfg.BaseDir)
			freeGB := float64(free) / (1024 * 1024 * 1024)
			budgetGB := cfg.DiskBudgetGB
			if budgetGB <= 0 {
				budgetGB = 1.0
			}
			switch {
			case freeGB < 0.5:
				log.Warn("startup disk critical: very low free space", "freeGB", fmt.Sprintf("%.2f", freeGB))
			case freeGB < budgetGB:
				log.Warn("startup disk warning: low free space", "freeGB", fmt.Sprintf("%.2f", freeGB), "thresholdGB", budgetGB)
			}
		}

		// Wire slot pressure guard to notification chain and SSE broker.
		if cfg.Runtime.SlotPressureGuard != nil {
			spg := cfg.Runtime.SlotPressureGuard.(*SlotPressureGuard)
			spg.notifyFn = notifyFn
			spg.broker = state.broker
			if cfg.SlotPressure.MonitorEnabled {
				go spg.RunMonitor(ctx)
				log.Info("slot pressure monitor started", "interval", spg.monitorInterval().String())
			}
		}

		// Agent heartbeat monitor.
		var heartbeatMon *HeartbeatMonitor
		if cfg.Heartbeat.Enabled {
			heartbeatMon = newHeartbeatMonitor(cfg.Heartbeat, state, notifyFn)

			// Wire idle detection: check dispatched tasks + hook workers + user sessions.
			heartbeatMon.SetIdleCheckFn(func() bool {
				state.mu.Lock()
				hasRunning := len(state.running) > 0
				state.mu.Unlock()
				if hasRunning {
					return false
				}
				if hookRecv.HasActiveWorkers() {
					return false
				}
				if countUserSessions(cfg.HistoryDB) > 0 {
					return false
				}
				return true
			})

			go heartbeatMon.Start(ctx)

			// Wire heartbeat into cron engine for idle-trigger jobs.
			cron.SetHeartbeatMonitor(heartbeatMon)
		}

		// Proactive engine.
		var proactiveEngine *ProactiveEngine
		if cfg.Proactive.Enabled {
			proactiveEngine = newProactiveEngine(cfg, state.broker, sem, childSem)
			proactiveEngine.Start(ctx)
			log.Info("proactive engine started", "rules", len(cfg.Proactive.Rules))
		}

		// Group chat engine.
		var groupChatEngine *GroupChatEngine
		if cfg.GroupChat.Activation != "" {
			groupChatEngine = newGroupChatEngine(cfg)
			log.Info("group chat engine started", "activation", cfg.GroupChat.Activation)
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
			log.Info("SLA monitor enabled", "interval", cfg.SLA.CheckIntervalOrDefault().String(), "window", cfg.SLA.WindowOrDefault().String())
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
						cost.CheckAndNotifyBudgetAlerts(cfg.Budgets, cfg.HistoryDB, notifyFn, budgetTracker)
					}
				}
			}()
			log.Info("budget governance enabled",
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
				ttl:      cfg.OfflineQueue.TtlOrDefault(),
			}
			go drainer.run(ctx)
			log.Info("offline queue enabled", "ttl", drainer.ttl.String(), "maxItems", cfg.OfflineQueue.MaxItemsOrDefault())
		}

		// Initialize Slack bot (uses HTTP push, no polling needed).
		var slackBot *slackbot.Bot
		if cfg.Slack.Enabled && cfg.Slack.BotToken != "" {
			rt := newMessagingRuntime(cfg, state, sem, childSem)
			rt.cron = cron
			slackBot = slackbot.NewBot(cfg.Slack, rt)
			log.Info("slack bot enabled", "endpoint", "/slack/events")

			// Wire Slack into notification chain.
			prevNotifyFn := notifyFn
			notifyFn = func(text string) {
				if prevNotifyFn != nil {
					prevNotifyFn(text)
				}
				slackBot.SendNotify(text)
			}
		}

		// Initialize Discord bot.
		var discordBot *DiscordBot
		if cfg.Discord.Enabled && cfg.Discord.BotToken != "" {
			discordBot = newDiscordBot(cfg, state, sem, childSem, cron)
			state.discordBot = discordBot       // P14.1: store for interaction handler
			cfg.Runtime.DiscordBot = discordBot // provider approval routing
			log.Info("discord bot enabled")

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
			mcpHost = newMCPHost(cfg, cfg.Runtime.ToolRegistry.(*ToolRegistry))
			if err := mcpHost.Start(ctx); err != nil {
				log.Error("MCP host start failed: %v", err)
			} else {
				log.Info("MCP host started", "servers", len(cfg.MCPServers))
			}
		}

		// Initialize metrics registry.
		initMetrics()

		// Initialize WhatsApp bot.
		var whatsappBot *whatsapp.Bot
		if cfg.WhatsApp.Enabled && cfg.WhatsApp.PhoneNumberID != "" && cfg.WhatsApp.AccessToken != "" {
			rt := newMessagingRuntime(cfg, state, sem, childSem)
			whatsappBot = whatsapp.NewBot(cfg.WhatsApp, rt)
			log.Info("whatsapp bot enabled", "endpoint", "/api/whatsapp/webhook")
		}

		// Initialize voice engine.
		var voiceEngine *VoiceEngine
		if cfg.Voice.STT.Enabled || cfg.Voice.TTS.Enabled {
			voiceEngine = newVoiceEngine(cfg)
			log.Info("voice engine initialized")
		}

		// Initialize agent communication DB.
		initAgentCommDB(cfg.HistoryDB)

		// --- P13.1: Plugin System --- Initialize plugin host.
		var pluginHost *PluginHost
		if len(cfg.Plugins) > 0 {
			pluginHost = NewPluginHost(cfg)
			pluginHost.AutoStart()
			log.Info("plugin host initialized", "plugins", len(cfg.Plugins))
		}

		// --- P13.2: Sandbox Plugin --- Initialize sandbox manager.
		if pluginHost != nil {
			sm := NewSandboxManager(cfg, pluginHost)
			if sm.PluginName() != "" {
				state.sandboxMgr = sm
				log.Info("sandbox manager initialized", "plugin", sm.PluginName())
			}
		}

		// --- P15.1: LINE Channel --- Initialize LINE bot.
		var lineBot *linebot.Bot
		if cfg.LINE.Enabled && cfg.LINE.ChannelSecret != "" && cfg.LINE.ChannelAccessToken != "" {
			rt := newMessagingRuntime(cfg, state, sem, childSem)
			lineBot = linebot.NewBot(cfg.LINE, rt)
			log.Info("line bot enabled", "endpoint", cfg.LINE.WebhookPathOrDefault())
		}

		// --- P15.2: Matrix Channel --- Initialize Matrix bot.
		var matrixBot *matrix.Bot
		if cfg.Matrix.Enabled && cfg.Matrix.Homeserver != "" && cfg.Matrix.AccessToken != "" {
			rt := newMessagingRuntime(cfg, state, sem, childSem)
			matrixBot = matrix.NewBot(cfg.Matrix, rt)
			log.Info("matrix bot enabled", "homeserver", cfg.Matrix.Homeserver, "userId", cfg.Matrix.UserID)
		}

		// --- P15.3: Teams Channel --- Initialize Teams bot.
		var teamsBot *teamsbot.Bot
		if cfg.Teams.Enabled && cfg.Teams.AppID != "" && cfg.Teams.AppPassword != "" {
			rt := newMessagingRuntime(cfg, state, sem, childSem)
			teamsBot = teamsbot.NewBot(cfg.Teams, rt)
			log.Info("teams bot enabled", "endpoint", "/api/teams/webhook")
		}

		// --- P15.4: Signal Channel --- Initialize Signal bot.
		var signalBot *signalbot.Bot
		if cfg.Signal.Enabled && cfg.Signal.PhoneNumber != "" {
			rt := newMessagingRuntime(cfg, state, sem, childSem)
			signalBot = signalbot.NewBot(cfg.Signal, rt)
			if cfg.Signal.PollingMode {
				signalBot.Start()
				log.Info("signal bot enabled (polling mode)", "interval", cfg.Signal.PollIntervalOrDefault())
			} else {
				log.Info("signal bot enabled", "endpoint", cfg.Signal.WebhookPathOrDefault())
			}
		}

		// --- P15.5: Google Chat Channel --- Initialize Google Chat bot.
		var gchatBot *gchat.Bot
		if cfg.GoogleChat.Enabled && cfg.GoogleChat.ServiceAccountKey != "" {
			rt := newMessagingRuntime(cfg, state, sem, childSem)
			var err error
			gchatBot, err = gchat.NewBot(cfg.GoogleChat, rt)
			if err != nil {
				log.Error("failed to initialize google chat bot", "error", err)
			} else {
				log.Info("google chat bot enabled", "endpoint", cfg.GoogleChat.WebhookPathOrDefault())
			}
		}

		// --- P18.3: Workflow Triggers --- Initialize trigger engine.
		// Always create engine (even if no triggers) so HTTP handlers can use it.
		triggerEngine := newWorkflowTriggerEngine(cfg, state, sem, childSem, state.broker)
		if len(cfg.WorkflowTriggers) > 0 {
			triggerEngine.Start(ctx)
		}

		// --- P19.3: Smart Reminders --- Initialize reminder engine.
		if cfg.Reminders.Enabled && cfg.HistoryDB != "" {
			if err := initReminderDB(cfg.HistoryDB); err != nil {
				log.Warn("init reminders table failed", "error", err)
			} else {
				app.Reminder = newReminderEngine(cfg, notifyFn)
				app.Reminder.Start()
				log.Info("reminder engine started", "checkInterval", cfg.Reminders.CheckIntervalOrDefault().String(), "maxPerUser", cfg.Reminders.MaxPerUserOrDefault())
			}
		}

		// --- P19.4: Notes/Obsidian Integration --- Initialize notes service.
		if cfg.Notes.Enabled {
			notesSvc := newNotesService(cfg)
			setGlobalNotesService(notesSvc)
			log.Info("notes service initialized", "vault", cfg.Notes.VaultPathResolved(cfg.BaseDir))
		}

		// --- P19.5: Unified Presence/Typing Indicators --- Initialize presence manager.
		// Note: Telegram bot is registered after creation below.
		app.Presence = newPresenceManager()
		if slackBot != nil {
			app.Presence.RegisterSetter("slack", slackBot)
		}
		if discordBot != nil {
			app.Presence.RegisterSetter("discord", discordBot)
		}
		if whatsappBot != nil {
			app.Presence.RegisterSetter("whatsapp", whatsappBot)
		}
		if lineBot != nil {
			app.Presence.RegisterSetter("line", lineBot)
		}
		if teamsBot != nil {
			app.Presence.RegisterSetter("teams", teamsBot)
		}
		if signalBot != nil {
			app.Presence.RegisterSetter("signal", signalBot)
		}
		if gchatBot != nil {
			app.Presence.RegisterSetter("gchat", gchatBot)
		}
		log.Info("presence manager initialized", "setters", len(app.Presence.setters))

		// --- P20.1: Home Assistant --- Initialize HA service.
		if cfg.HomeAssistant.Enabled && cfg.HomeAssistant.BaseURL != "" {
			app.HA = newHAService(cfg.HomeAssistant)
			if cfg.HomeAssistant.WebSocket {
				go app.HA.StartEventListener(ctx, &haEventPublisherAdapter{broker: state.broker})
			}
			log.Info("home assistant enabled", "baseUrl", cfg.HomeAssistant.BaseURL)
		}

		// --- P20.4: Device Actions --- Ensure output dir exists.
		if cfg.Device.Enabled {
			ensureDeviceOutputDir(cfg)
		}

		// --- P19.1: Gmail Integration ---
		if cfg.Gmail.Enabled {
			app.Gmail = newGmailService(cfg)
			log.Info("gmail integration enabled")
		}

		// --- P19.2: Google Calendar Integration ---
		if cfg.Calendar.Enabled {
			app.Calendar = newCalendarService(cfg)
			log.Info("calendar integration enabled")
		}

		// --- P20.3: Twitter/X Integration ---
		if cfg.Twitter.Enabled {
			app.Twitter = newTwitterService(cfg)
			log.Info("twitter integration enabled")
		}

		// --- P21.6: Chrome Extension Relay ---
		if cfg.BrowserRelay.Enabled {
			app.Browser = newBrowserRelay(&cfg.BrowserRelay)
			go func() {
				if err := app.Browser.Start(ctx); err != nil && err != http.ErrServerClosed {
					log.Warn("browser relay stopped", "error", err)
				}
			}()
			log.Info("browser relay enabled", "port", cfg.BrowserRelay.Port)
		}

		// --- P20.2: iMessage via BlueBubbles --- Initialize iMessage bot.
		var imessageBot *imessagebot.Bot
		if cfg.IMessage.Enabled && cfg.IMessage.ServerURL != "" {
			rt := newMessagingRuntime(cfg, state, sem, childSem)
			imessageBot = imessagebot.NewBot(cfg.IMessage, rt)
			app.IMessage = imessageBot
			log.Info("imessage bot enabled", "endpoint", cfg.IMessage.WebhookPathOrDefault())
			if app.Presence != nil {
				app.Presence.RegisterSetter("imessage", imessageBot)
			}
		}

		// Initialize callback manager for external workflow steps.
		callbackMgr = NewCallbackManager(cfg.HistoryDB)

		// Backfill global vars from App for callers that haven't migrated yet.
		app.SyncToGlobals()

		// HTTP server.
		drainCh := make(chan struct{}, 1)
		srvInstance := &Server{
			cfg: cfg, app: app, state: state, sem: sem, childSem: childSem, cron: cron, secMon: secMon, mcpHost: mcpHost,
			proactiveEngine: proactiveEngine, groupChatEngine: groupChatEngine, voiceEngine: voiceEngine,
			slackBot: slackBot, whatsappBot: whatsappBot, pluginHost: pluginHost,
			lineBot: lineBot, teamsBot: teamsBot, signalBot: signalBot, gchatBot: gchatBot, imessageBot: imessageBot, matrixBot: matrixBot,
			heartbeatMonitor: heartbeatMon,
			hookReceiver:     hookRecv,
			triggerEngine:    triggerEngine,
			DegradedServices: degradedServices,
			drainCh:          drainCh,
		}
		srv := startHTTPServer(srvInstance)

		// Recover pending external step workflows.
		go recoverPendingWorkflows(cfg, state, sem, childSem)

		// Cleanup expired callbacks (timeout marking + old streaming records).
		cleanupExpiredCallbacks(cfg.HistoryDB)

		// Cleanup zombie sessions AFTER the HTTP server starts.
		// Delayed so that if port binding fails (os.Exit in goroutine),
		// the process dies before this runs, avoiding destructive cleanup
		// during crash loops (launchd KeepAlive restart cycles).
		go func() {
			time.Sleep(2 * time.Second)
			cleanupZombieSessions(cfg.HistoryDB)
		}()

		// Report degraded services.
		if len(degradedServices) > 0 {
			log.Warn("starting in degraded mode", "failedServices", strings.Join(degradedServices, ", "))
		}

		// Config hot-reload on SIGHUP.
		sighupCh := make(chan os.Signal, 1)
		signal.Notify(sighupCh, syscall.SIGHUP)
		go func() {
			for range sighupCh {
				log.Info("received SIGHUP, reloading config")
				newCfg, err := tryLoadConfig(*configPath)
				if err != nil {
					log.Error("config reload failed", "error", err)
					continue
				}
				// Preserve runtime-only field not set by tryLoadConfig.
				srvInstance.cfgMu.RLock()
				oldCfg := srvInstance.cfg
				srvInstance.cfgMu.RUnlock()
				newCfg.Runtime.ToolRegistry = oldCfg.Runtime.ToolRegistry

				// Log config diff.
				logConfigDiff(oldCfg, newCfg)

				// Atomic swap.
				srvInstance.ReloadConfig(newCfg)

				// Reload workflow triggers.
				if srvInstance.triggerEngine != nil {
					srvInstance.triggerEngine.ReloadTriggers(newCfg.WorkflowTriggers)
				}

				log.Info("config reloaded successfully")
			}
		}()

		// Start Telegram bot.
		if cfg.Telegram.Enabled && cfg.Telegram.BotToken != "" {
			tgrt := newTelegramRuntime(cfg, state, sem, childSem, cron)
			bot = tgbot.NewBot(cfg.Telegram, tgrt)
			// Wire up keyboard notification for approval gate.
			cron.notifyKeyboardFn = func(text string, keyboard [][]tgInlineButton) {
				bot.ReplyWithKeyboard(bot.ChatID(), text, keyboard)
			}
			// Register Telegram bot for presence/typing indicators.
			if app.Presence != nil {
				app.Presence.RegisterSetter("telegram", bot)
			}
			go bot.PollLoop(ctx)
		} else {
			log.Info("telegram disabled or no bot token, HTTP-only mode")
		}

		// Start Discord bot.
		if discordBot != nil {
			go discordBot.Run(ctx)
		}

		// --- P15.2: Matrix Channel --- Start Matrix bot.
		if matrixBot != nil {
			go matrixBot.Run(ctx)
		}

		log.Info("tetora ready", "healthz", fmt.Sprintf("http://%s/healthz", cfg.ListenAddr))

		// Wait for shutdown signal or drain request.
		select {
		case <-sigCh:
			log.Info("shutting down")
		case <-drainCh:
			log.Info("drain requested: waiting for active agents to complete")
			// Wait for all running tasks to finish (poll with ticker).
			drainTicker := time.NewTicker(2 * time.Second)
			drainDeadline := time.Now().Add(10 * time.Minute)
		drainLoop:
			for {
				select {
				case <-sigCh:
					// Force shutdown even during drain.
					log.Info("force shutdown during drain")
					drainTicker.Stop()
					break drainLoop
				case <-drainTicker.C:
					state.mu.Lock()
					active := len(state.running)
					state.mu.Unlock()
					if active == 0 {
						log.Info("drain complete: all agents finished")
						drainTicker.Stop()
						break drainLoop
					}
					if time.Now().After(drainDeadline) {
						log.Warn("drain timeout: forcing shutdown", "stillActive", active)
						drainTicker.Stop()
						break drainLoop
					}
					log.Info("draining: waiting for agents", "active", active)
				}
			}
			log.Info("shutting down after drain")
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
		if srvInstance.triggerEngine != nil {
			srvInstance.triggerEngine.Stop()
		}

		// --- P19.3: Smart Reminders --- Stop reminder engine.
		if app.Reminder != nil {
			app.Reminder.Stop()
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

		// Stop terminal bridge sessions.
		if discordBot != nil && discordBot.terminal != nil {
			discordBot.terminal.stopAllSessions()
		}

		// Shut down HTTP server first (5s drain for in-flight callbacks).
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutCancel()
		srv.Shutdown(shutCtx)

		// Cancel all running workflows AFTER HTTP drain to let final callbacks deliver.
		runCancellers.Range(func(key, value any) bool {
			if cancel, ok := value.(context.CancelFunc); ok {
				cancel()
			}
			runCancellers.Delete(key)
			return true
		})

		log.Info("tetora stopped")

	} else {
		// --- CLI mode ---
		tasks := readTaskInput(*tasksJSON, *filePath)
		for i := range tasks {
			fillDefaults(cfg, &tasks[i])
			tasks[i].Source = "cli"
		}

		if cfg.Log {
			log.Info("tetora dispatching", "tasks", len(tasks), "maxConcurrent", cfg.MaxConcurrent)
		}

		// Start HTTP monitor in background.
		srv := startHTTPServer(&Server{cfg: cfg, state: state, sem: sem, childSem: childSem})

		// Handle signals — cancel dispatch.
		go func() {
			<-sigCh
			log.Info("received signal, cancelling")
			state.mu.Lock()
			if state.cancel != nil {
				state.cancel()
			}
			state.mu.Unlock()
		}()

		dispatchCtx := trace.WithID(context.Background(), trace.NewID("cli"))
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
				log.Error("telegram notify failed", "error", err)
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
		log.Info("config changes detected", "changes", strings.Join(changes, "; "))
	} else {
		log.Info("config reloaded, no significant changes detected")
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
			log.Error("read task file failed", "error", err)
			os.Exit(1)
		}
	} else {
		// Try stdin if not a TTY.
		stat, _ := os.Stdin.Stat()
		if (stat.Mode() & os.ModeCharDevice) == 0 {
			var err error
			data, err = io.ReadAll(os.Stdin)
			if err != nil {
				log.Error("read stdin failed", "error", err)
				os.Exit(1)
			}
		}
	}

	if len(data) == 0 {
		log.Error("no tasks provided, use --tasks, --file, or pipe JSON to stdin")
		os.Exit(1)
	}

	var tasks []Task
	if err := json.Unmarshal(data, &tasks); err != nil {
		log.Error("parse tasks JSON failed", "error", err)
		os.Exit(1)
	}
	return tasks
}
