package main
import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	"tetora/internal/audit"
	"tetora/internal/automation/briefing"
	"tetora/internal/automation/insights"
	"tetora/internal/circuit"
	"tetora/internal/cli"
	"tetora/internal/completion"
	"tetora/internal/config"
	"tetora/internal/cost"
	"tetora/internal/db"
	"tetora/internal/export"
	"tetora/internal/history"
	"tetora/internal/hooks"
	"tetora/internal/knowledge"
	"tetora/internal/log"
	"tetora/internal/messaging/gchat"
	"tetora/internal/messaging/groupchat"
	linebot "tetora/internal/messaging/line"
	"tetora/internal/messaging/matrix"
	signalbot "tetora/internal/messaging/signal"
	slackbot "tetora/internal/messaging/slack"
	teamsbot "tetora/internal/messaging/teams"
	tgbot "tetora/internal/messaging/telegram"
	"tetora/internal/messaging/whatsapp"
	"tetora/internal/metrics"
	"tetora/internal/migrate"
	"tetora/internal/recap"
	"tetora/internal/sandbox"
	"tetora/internal/scheduling"
	"tetora/internal/sla"
	"tetora/internal/storage"
	"tetora/internal/telemetry"
	"tetora/internal/tools"
	"tetora/internal/trace"
	"tetora/internal/upload"
	"tetora/internal/version"
	imessagebot "tetora/internal/messaging/imessage"
)

// --- from main.go ---

// metricsGlobal is the global metrics registry.
var metricsGlobal *metrics.Registry

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
		case "project":
			cli.CmdProject(os.Args[2:])
			return
		case "guide":
			runGuide(os.Args[2:])
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
			cli.CmdInit(cli.InitDeps{
				SeedDefaultJobsJSON: func() ([]byte, error) {
					return json.MarshalIndent(JobsFile{Jobs: seedDefaultJobs()}, "", "  ")
				},
				GenerateMCPBridge: func(baseDir, listenAddr, apiToken string) error {
					return generateMCPBridgeConfig(&Config{BaseDir: baseDir, ListenAddr: listenAddr, APIToken: apiToken})
				},
				InstallHooks: hooks.Install,
			})
			return
		case "setup":
			cli.CmdSetup(os.Args[2:], cli.SetupWebDeps{
				SeedDefaultJobsJSON: func() ([]byte, error) {
					return json.MarshalIndent(JobsFile{Jobs: seedDefaultJobs()}, "", "  ")
				},
			})
			return
		case "workflow":
			cmdWorkflow(os.Args[2:])
			return
		case "quick":
			qcfg := loadConfig("")
			cli.CmdQuick(os.Args[2:], qcfg.ListenAddr, qcfg.APIToken)
			return
		case "pairing":
			cli.CmdPairing(os.Args[2:])
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
		case "team":
			cli.CmdTeam(os.Args[2:])
			return
		case "time-savings":
			cmdTimeSavings(os.Args[2:])
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
			Cfg:    cfg.SlotPressure,
			Sem:    sem,
			SemCap: cfg.MaxConcurrent,
		}
		spg := cfg.Runtime.SlotPressureGuard.(*SlotPressureGuard)
		log.Info("slot pressure guard enabled",
			"reserved", spg.ReservedSlots(),
			"warnThreshold", spg.WarnThreshold())
	}

	state := newDispatchState()
	state.broker = newSSEBroker()

	// Wire workflow execution into the skill package so workflow-type skills work.
	wireSkillWorkflowRunner(cfg, state, sem)

	// Multi-tenant dispatch manager: register default client with existing state/semaphores.
	dispatchMgr := newDispatchManager(cfg.MaxConcurrent, childSemConcurrentOrDefault(cfg))
	dispatchMgr.register(cfg.DefaultClientID, state, sem, childSem)

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
			if err := history.InitDB(cfg.HistoryDB); err != nil {
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
				if err := history.Cleanup(cfg.HistoryDB, retentionDays(cfg.Retention.History, 90)); err != nil {
					log.Warn("cleanup history failed", "error", err)
				}
			}
			// Init audit log table and start batched writer.
			if err := audit.Init(cfg.HistoryDB); err != nil {
				log.Warn("init audit_log failed", "error", err)
			}
			audit.StartWriter()
			audit.Cleanup(cfg.HistoryDB, retentionDays(cfg.Retention.AuditLog, 365))
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
			if err := version.InitDB(cfg.HistoryDB); err != nil {
				log.Warn("init config_versions failed", "error", err)
			}
			// Init agent communication table.
			if err := initAgentCommDB(cfg.HistoryDB); err != nil {
				log.Warn("init agent_messages failed", "error", err)
			}
			// Init handoff tables (also runs column migrations on agent_messages).
			initHandoffTables(cfg.HistoryDB)
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
			// Init human gate table (human steps).
			initHumanGateTable(cfg.HistoryDB)
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
			bsched := scheduling.NewBackupScheduler(scheduling.BackupConfig{
			DBPath:     cfg.HistoryDB,
			BackupDir:  cfg.Ops.BackupDirResolved(cfg.BaseDir),
			RetainDays: cfg.Ops.BackupRetainOrDefault(),
			EscapeSQL:  db.Escape,
			LogInfo:    log.Info,
			LogWarn:    log.Warn,
		})
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
				"batchInterval", notifyEngine.BatchInterval().String())
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
		if err := cron.LoadJobs(); err != nil {
			log.Warn("cron load error, continuing without cron", "error", err)
		} else {
			// Register daily notes job if enabled.
			registerDailyNotesJob(ctx, cfg, cron)
			cron.Start(ctx)
			cron.StartupReplay(ctx)
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
			spg.NotifyFn = notifyFn
			spg.Broker = state.broker
			if cfg.SlotPressure.MonitorEnabled {
				go spg.RunMonitor(ctx)
				log.Info("slot pressure monitor started", "interval", spg.MonitorInterval().String())
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
			cron.SetIdleChecker(heartbeatMon)
		}

		// Group chat engine.
		var groupChatEngine *groupchat.Engine
		if cfg.GroupChat.Activation != "" {
			var agentNames []string
			for name := range cfg.Agents {
				agentNames = append(agentNames, name)
			}
			groupChatEngine = groupchat.New(&groupchat.Config{
				Activation:    cfg.GroupChat.Activation,
				Keywords:      cfg.GroupChat.Keywords,
				ContextWindow: cfg.GroupChat.ContextWindow,
				RateLimit: groupchat.RateLimitConfig{
					MaxPerMin: cfg.GroupChat.RateLimit.MaxPerMin,
					PerGroup:  cfg.GroupChat.RateLimit.PerGroup,
				},
				AllowedGroups: cfg.GroupChat.AllowedGroups,
				ThreadReply:   cfg.GroupChat.ThreadReply,
				MentionNames:  cfg.GroupChat.MentionNames,
				AgentNames:    agentNames,
			})
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

		// Start zombie workflow janitor — resets stale running/resumed rows on a 30-min tick.
		go func() {
			ticker := time.NewTicker(30 * time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if count := resetZombieWorkflowRuns(cfg.HistoryDB, 4*time.Hour); count > 0 {
						notifyFn(fmt.Sprintf("zombie janitor: terminated %d stale workflow(s)", count))
					}
				}
			}
		}()

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

			// Recap watcher: mirror Claude Code transcript `away_summary` entries to
			// per-session Discord threads. Skipped when no DB path or when disabled.
			if cfg.Discord.Recap.Enabled && cfg.HistoryDB != "" {
				if err := recap.InitSchema(cfg.HistoryDB); err != nil {
					log.Warn("recap: init schema failed", "error", err)
				} else {
					recapRouter := &recap.Router{
						Cfg:    cfg.Discord.Recap,
						API:    discordBot.api,
						DBPath: cfg.HistoryDB,
					}
					recapWatcher := recap.New(cfg.Discord.Recap, recapRouter)
					go recapWatcher.Start(ctx)
				}
			}
		}

		// Wire notifyFn into config for skill install scan notifications.
		cfg.RuntimeNotifyFn = notifyFn

		// Proactive engine — initialized after Discord so notifyFn includes Discord delivery.
		var proactiveEngine *ProactiveEngine
		if cfg.Proactive.Enabled {
			proactiveEngine = newProactiveEngine(cfg, state.broker, sem, childSem, notifyFn)
			proactiveEngine.Start(ctx)
			log.Info("proactive engine started", "rules", len(cfg.Proactive.Rules))
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
		metricsGlobal = metrics.NewRegistry()
		metricsGlobal.RegisterCounter("tetora_dispatch_total", "Total dispatches", []string{"role", "status"})
		metricsGlobal.RegisterHistogram("tetora_dispatch_duration_seconds", "Dispatch latency", []string{"role"}, metrics.DefaultBuckets)
		metricsGlobal.RegisterCounter("tetora_dispatch_cost_usd", "Total cost in USD", []string{"role"})
		metricsGlobal.RegisterCounter("tetora_provider_requests_total", "Provider API calls", []string{"provider", "status"})
		metricsGlobal.RegisterHistogram("tetora_provider_latency_seconds", "Provider response time", []string{"provider"}, metrics.DefaultBuckets)
		metricsGlobal.RegisterCounter("tetora_provider_tokens_total", "Token usage", []string{"provider", "direction"})
		metricsGlobal.RegisterGauge("tetora_circuit_state", "Circuit breaker state (0=closed,1=open,2=half-open)", []string{"provider"})
		metricsGlobal.RegisterGauge("tetora_session_active", "Active session count", []string{"role"})
		metricsGlobal.RegisterGauge("tetora_queue_depth", "Offline queue depth", nil)
		metricsGlobal.RegisterCounter("tetora_cron_runs_total", "Cron job executions", []string{"status"})

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
			sm := sandbox.NewSandboxManager(cfg, pluginHost)
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
			tools.EnsureDeviceOutputDir(cfg)
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
		callbackMgr = newCallbackManager(cfg.HistoryDB)

		// Backfill global vars from App for callers that haven't migrated yet.
		app.SyncToGlobals()

		// HTTP server.
		drainCh := make(chan struct{}, 1)
		srvInstance := &Server{
			cfg: cfg, app: app, state: state, sem: sem, childSem: childSem, dispatchMgr: dispatchMgr, cron: cron, secMon: secMon, mcpHost: mcpHost,
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

		// Cleanup expired callbacks/human-gates BEFORE recovery so that recovery only
		// picks up gates that are genuinely still within their timeout window.
		cleanupExpiredCallbacks(cfg.HistoryDB)

		// Recover pending external step workflows (gates still within timeout window).
		go recoverPendingWorkflows(cfg, state, sem, childSem)

		// Cleanup zombie sessions AFTER the HTTP server starts.
		// Delayed so that if port binding fails (os.Exit in goroutine),
		// the process dies before this runs, avoiding destructive cleanup
		// during crash loops (launchd KeepAlive restart cycles).
		go func() {
			time.Sleep(2 * time.Second)
			cleanupZombieSessions(cfg.HistoryDB)
		}()

		// Periodic cleanup of stale hook workers.
		go func() {
			ticker := time.NewTicker(5 * time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					hookRecv.CleanupStaleHookWorkers()
				}
			}
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

				// Rebuild ProviderRegistry if providers config changed.
				if providersChanged(oldCfg, newCfg) {
					log.Info("providers config changed, rebuilding provider registry")
					newReg := initProviders(newCfg)
					newCfg.Runtime.ProviderRegistry = newReg
				} else {
					newCfg.Runtime.ProviderRegistry = oldCfg.Runtime.ProviderRegistry
				}

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
			cron.SetTelegramKeyboardFn(func(text string, keyboard any) {
				if kb, ok := keyboard.([][]tgInlineButton); ok {
					bot.ReplyWithKeyboard(bot.ChatID(), text, kb)
				}
			})
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

		// Self-liveness watchdog: pings /healthz and exits if unresponsive,
		// letting launchd/systemd restart the process.
		startWatchdog(ctx, cfg.Watchdog, cfg.ListenAddr)

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
		cron.Stop()

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
		srv := startHTTPServer(&Server{cfg: cfg, state: state, sem: sem, childSem: childSem, dispatchMgr: dispatchMgr})

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

// --- from app.go ---

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
	Insights    *insights.Engine
	Scheduling  *scheduling.Service
	Habits      *HabitsService
	Goals       *GoalsService
	Briefing    *briefing.Service

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
	ImageGenLimiter     *tools.ImageGenLimiter
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

// Server holds all dependencies for the HTTP server.
type Server struct {
	cfg             *Config
	app             *App // P28.1: application container
	state           *dispatchState
	sem             chan struct{}
	childSem        chan struct{} // sub-agent tasks (depth > 0)
	cron            *CronEngine
	secMon          *securityMonitor
	mcpHost         *MCPHost
	proactiveEngine *ProactiveEngine
	groupChatEngine *groupchat.Engine
	voiceEngine     *VoiceEngine
	slackBot        *slackbot.Bot
	whatsappBot     *whatsapp.Bot
	pluginHost      *PluginHost
	lineBot         *linebot.Bot
	teamsBot        *teamsbot.Bot
	signalBot       *signalbot.Bot
	gchatBot        *gchat.Bot
	imessageBot     *imessagebot.Bot
	matrixBot       *matrix.Bot
	// Multi-tenant dispatch manager.
	dispatchMgr *dispatchManager

	// internal (created at start)
	taskBoardDispatcher *TaskBoardDispatcher
	canvasEngine        *CanvasEngine
	voiceRealtimeEngine *VoiceRealtimeEngine
	heartbeatMonitor    *HeartbeatMonitor
	hookReceiver        *hookReceiver
	triggerEngine       *WorkflowTriggerEngine
	startTime           time.Time
	limiter             *loginLimiter
	apiLimiter          *apiRateLimiter

	// Config hot-reload support
	cfgMu sync.RWMutex

	// DegradedServices tracks services that failed to initialize.
	DegradedServices []string

	// drainCh is closed when a drain request is received, triggering graceful shutdown.
	drainCh chan struct{}
}

// Cfg returns the current config with read-lock protection.
func (s *Server) Cfg() *Config {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.cfg
}

// ReloadConfig atomically swaps the config pointer.
func (s *Server) ReloadConfig(newCfg *Config) {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	s.cfg = newCfg
}

// resolveClientDispatch returns the dispatch state and semaphores for a given client ID.
// If the server has a dispatchManager, it resolves per-client; otherwise falls back to default.
func (s *Server) resolveClientDispatch(clientID string) (*dispatchState, chan struct{}, chan struct{}) {
	if s.dispatchMgr != nil {
		return s.dispatchMgr.getOrCreate(clientID)
	}
	return s.state, s.sem, s.childSem
}

// resolveHistoryDB returns the history DB path for a given client ID.
// For the default client, returns cfg.HistoryDB (existing behavior).
// For other clients, returns the per-client DB path and ensures the directory exists.
func (s *Server) resolveHistoryDB(cfg *Config, clientID string) string {
	if clientID == cfg.DefaultClientID {
		return cfg.HistoryDB
	}
	dbPath := cfg.HistoryDBFor(clientID)
	// Ensure parent directory exists for new clients.
	os.MkdirAll(filepath.Dir(dbPath), 0o755)
	return dbPath
}

// --- from config.go ---

// --- Type aliases pointing to internal/config ---

type Config = config.Config

type PromptBudgetConfig = config.PromptBudgetConfig
type ApprovalGateConfig = config.ApprovalGateConfig
type WritingStyleConfig = config.WritingStyleConfig
type BrowserRelayConfig = config.BrowserRelayConfig
type NotebookLMConfig = config.NotebookLMConfig
type CitationConfig = config.CitationConfig
type ImageGenConfig = config.ImageGenConfig
type WeatherConfig = config.WeatherConfig
type CurrencyConfig = config.CurrencyConfig
type RSSConfig = config.RSSConfig
type TranslateConfig = config.TranslateConfig
type UserProfileConfig = config.UserProfileConfig
type OpsConfig = config.OpsConfig
type MessageQueueConfig = config.MessageQueueConfig
type FinanceConfig = config.FinanceConfig
type TaskManagerConfig = config.TaskManagerConfig
type TodoistConfig = config.TodoistConfig
type NotionConfig = config.NotionConfig
type WebhookConfig = config.WebhookConfig
type AgentConfig = config.AgentConfig
type ProviderConfig = config.ProviderConfig
type CostAlertConfig = config.CostAlertConfig
type DashboardAuthConfig = config.DashboardAuthConfig
type QuietHoursConfig = config.QuietHoursConfig
type DigestConfig = config.DigestConfig
type NotificationChannel = config.NotificationChannel
type RateLimitConfig = config.RateLimitConfig
type TLSConfig = config.TLSConfig
type SecurityAlertConfig = config.SecurityAlertConfig
type SmartDispatchConfig = config.SmartDispatchConfig
type RoutingRule = config.RoutingRule
type RoutingBinding = config.RoutingBinding
type EstimateConfig = config.EstimateConfig
type ToolConfig = config.ToolConfig
type WebSearchConfig = config.WebSearchConfig
type VisionConfig = config.VisionConfig
type MCPServerConfig = config.MCPServerConfig
type CircuitBreakerConfig = config.CircuitBreakerConfig
type SessionConfig = config.SessionConfig
type LoggingConfig = config.LoggingConfig
type VoiceConfig = config.VoiceConfig
type STTConfig = config.STTConfig
type TTSConfig = config.TTSConfig
type PushConfig = config.PushConfig
type AgentCommConfig = config.AgentCommConfig
type ProactiveConfig = config.ProactiveConfig
type GroupChatConfig = config.GroupChatConfig
type GroupChatRateLimitConfig = config.GroupChatRateLimitConfig

// Messaging platform type aliases (configs already defined in internal/messaging packages,
// re-exported via internal/config).
type TelegramConfig = config.TelegramConfig
type MatrixConfig = config.MatrixConfig
type WhatsAppConfig = config.WhatsAppConfig
type SignalConfig = config.SignalConfig
type GoogleChatConfig = config.GoogleChatConfig
type LINEConfig = config.LINEConfig
type TeamsConfig = config.TeamsConfig
type IMessageConfig = config.IMessageConfig
type SlackBotConfig = config.SlackBotConfig

// Integration type aliases.
type GmailConfig = config.GmailConfig
type SpotifyConfig = config.SpotifyConfig
type TwitterConfig = config.TwitterConfig
type PodcastConfig = config.PodcastConfig
type HomeAssistantConfig = config.HomeAssistantConfig
type NotesConfig = config.NotesConfig

// Other type aliases from internal/config.
type AgentToolPolicy = config.AgentToolPolicy
type CompactionConfig = config.CompactionConfig
type ToolProfile = config.ToolProfile
type WorkspaceConfig = config.WorkspaceConfig
type SandboxMode = config.SandboxMode
type DockerConfig = config.DockerConfig
type SandboxConfig = config.SandboxConfig
type PluginConfig = config.PluginConfig
type OAuthConfig = config.OAuthConfig
type OAuthServiceConfig = config.OAuthServiceConfig
type EmbeddingConfig = config.EmbeddingConfig
type MMRConfig = config.MMRConfig
type TemporalConfig = config.TemporalConfig
type InjectionDefenseConfig = config.InjectionDefenseConfig
type SecurityConfig = config.SecurityConfig
type TrustConfig = config.TrustConfig
type TaskBoardConfig = config.TaskBoardConfig
type TaskBoardDispatchConfig = config.TaskBoardDispatchConfig
type GitWorkflowConfig = config.GitWorkflowConfig
type WorkflowTriggerConfig = config.WorkflowTriggerConfig
type TriggerSpec = config.TriggerSpec
type OfflineQueueConfig = config.OfflineQueueConfig
type ReflectionConfig = config.ReflectionConfig
type NotifyIntelConfig = config.NotifyIntelConfig
type IncomingWebhookConfig = config.IncomingWebhookConfig
type RetentionConfig = config.RetentionConfig
type AccessControlConfig = config.AccessControlConfig
type SlotPressureConfig = config.SlotPressureConfig
type CanvasConfig = config.CanvasConfig
type DailyNotesConfig = config.DailyNotesConfig
type UsageConfig = config.UsageConfig
type HeartbeatConfig = config.HeartbeatConfig
type HooksConfig = config.HooksConfig
type PlanGateConfig = config.PlanGateConfig
type MCPBridgeConfig = config.MCPBridgeConfig
type StoreConfig = config.StoreConfig
type ReminderConfig = config.ReminderConfig
type DeviceConfig = config.DeviceConfig
type CalendarConfig = config.CalendarConfig
type FileManagerConfig = config.FileManagerConfig
type YouTubeConfig = config.YouTubeConfig
type FamilyConfig = config.FamilyConfig
type TimeTrackingConfig = config.TimeTrackingConfig
type LifecycleConfig = config.LifecycleConfig
type DiscordBotConfig = config.DiscordBotConfig
type DiscordRouteConfig = config.DiscordRouteConfig
type DiscordComponentsConfig = config.DiscordComponentsConfig
type DiscordThreadBindingsConfig = config.DiscordThreadBindingsConfig
type DiscordReactionsConfig = config.DiscordReactionsConfig
type DiscordForumBoardConfig = config.DiscordForumBoardConfig
type DiscordVoiceConfig = config.DiscordVoiceConfig
type DiscordVoiceAutoJoin = config.DiscordVoiceAutoJoin
type DiscordVoiceTTSConfig = config.DiscordVoiceTTSConfig
type DiscordTerminalConfig = config.DiscordTerminalConfig
type SLAConfig = config.SLAConfig
type BudgetConfig = config.BudgetConfig
type AutoDowngradeConfig = config.AutoDowngradeConfig
type ModelPricing = config.ModelPricing
type SkillConfig = config.SkillConfig
type SkillStoreConfig = config.SkillStoreConfig
type SpriteConfig = config.SpriteConfig
type QuickAction = config.QuickAction
type QuickActionParam = config.QuickActionParam
type ProactiveRule = config.ProactiveRule
type ProactiveTrigger = config.ProactiveTrigger
type ProactiveAction = config.ProactiveAction
type ProactiveDelivery = config.ProactiveDelivery
type VoiceWakeConfig = config.VoiceWakeConfig
type VoiceRealtimeConfig = config.VoiceRealtimeConfig

// deepMergeJSON merges two JSON documents. Values in override take precedence.
// Objects are merged recursively (key-level); all other types are replaced.
func deepMergeJSON(baseJSON, overrideJSON []byte) ([]byte, error) {
	var base, override map[string]any
	if err := json.Unmarshal(baseJSON, &base); err != nil {
		return nil, fmt.Errorf("unmarshal base: %w", err)
	}
	if err := json.Unmarshal(overrideJSON, &override); err != nil {
		return nil, fmt.Errorf("unmarshal override: %w", err)
	}
	deepMergeMaps(base, override)
	return json.Marshal(base)
}

// deepMergeMaps recursively merges src into dst. src values win; nested maps
// are merged at key level rather than replaced wholesale.
func deepMergeMaps(dst, src map[string]any) {
	for k, srcVal := range src {
		dstVal, exists := dst[k]
		if !exists {
			dst[k] = srcVal
			continue
		}
		srcMap, srcOK := srcVal.(map[string]any)
		dstMap, dstOK := dstVal.(map[string]any)
		if srcOK && dstOK {
			deepMergeMaps(dstMap, srcMap)
		} else {
			dst[k] = srcVal
		}
	}
}

// --- Config Loading ---

func loadConfig(path string) *Config {
	cfg, err := tryLoadConfig(path)
	if err != nil {
		log.Error("config load failed", "error", err)
		os.Exit(1)
	}
	return cfg
}

// tryLoadConfig loads and validates the config file, returning an error instead
// of calling os.Exit. Used by SIGHUP hot-reload so a bad config doesn't kill
// the daemon.
func tryLoadConfig(path string) (*Config, error) {
	if path == "" {
		// Binary at ~/.tetora/bin/tetora → config at ~/.tetora/config.json
		if exe, err := os.Executable(); err == nil {
			candidate := filepath.Join(filepath.Dir(exe), "..", "config.json")
			if abs, err := filepath.Abs(candidate); err == nil {
				candidate = abs
			}
			if _, err := os.Stat(candidate); err == nil {
				path = candidate
			}
		}
		if path == "" {
			path = "config.json"
		}
	}

	// Auto-migrate config if version is outdated.
	migrate.AutoMigrateConfig(path)

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	// Load local config override (config.local.json) and deep merge.
	localPath := strings.TrimSuffix(path, ".json") + ".local.json"
	if localData, err := os.ReadFile(localPath); err == nil {
		merged, mergeErr := deepMergeJSON(data, localData)
		if mergeErr != nil {
			return nil, fmt.Errorf("merge local config: %w", mergeErr)
		}
		cfg = Config{} // reset before re-unmarshal
		if err := json.Unmarshal(merged, &cfg); err != nil {
			return nil, fmt.Errorf("parse merged config: %w", err)
		}
		log.Info("loaded local config override", "path", localPath)
	}

	cfg.BaseDir = filepath.Dir(path)

	// Defaults.
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 8
	}
	if cfg.DefaultModel == "" {
		cfg.DefaultModel = "sonnet"
	}
	if cfg.DefaultTimeout == "" {
		cfg.DefaultTimeout = "1h"
	}
	if cfg.DefaultPermissionMode == "" {
		cfg.DefaultPermissionMode = "acceptEdits"
	}
	if cfg.Telegram.PollTimeout <= 0 {
		cfg.Telegram.PollTimeout = 30
	}
	if cfg.JobsFile == "" {
		cfg.JobsFile = "jobs.json"
	}
	if cfg.HistoryDB == "" {
		cfg.HistoryDB = "history.db"
	}
	if cfg.CostAlert.Action == "" {
		cfg.CostAlert.Action = "warn"
	}

	// Rate limit defaults.
	if cfg.RateLimit.MaxPerMin <= 0 {
		cfg.RateLimit.MaxPerMin = 60
	}
	// Security alert defaults.
	if cfg.SecurityAlert.FailThreshold <= 0 {
		cfg.SecurityAlert.FailThreshold = 10
	}
	if cfg.SecurityAlert.FailWindowMin <= 0 {
		cfg.SecurityAlert.FailWindowMin = 5
	}
	// Max prompt length default.
	if cfg.MaxPromptLen <= 0 {
		cfg.MaxPromptLen = 102400 // 100KB
	}
	// Default provider.
	if cfg.DefaultProvider == "" {
		cfg.DefaultProvider = "claude"
	}
	// Backward compat: if no providers configured, create one from ClaudePath.
	if len(cfg.Providers) == 0 {
		claudePath := cfg.ClaudePath
		if claudePath == "" {
			claudePath = "claude"
		}
		cfg.Providers = map[string]ProviderConfig{
			"claude": {Type: "claude-cli", Path: claudePath},
		}
	}

	// Smart dispatch defaults — use first agent from agents map, never hardcode.
	if cfg.SmartDispatch.Coordinator == "" && len(cfg.Agents) > 0 {
		for k := range cfg.Agents {
			cfg.SmartDispatch.Coordinator = k
			break
		}
	}
	if cfg.SmartDispatch.DefaultAgent == "" && len(cfg.Agents) > 0 {
		for k := range cfg.Agents {
			cfg.SmartDispatch.DefaultAgent = k
			break
		}
	}
	if cfg.SmartDispatch.ClassifyBudget <= 0 {
		cfg.SmartDispatch.ClassifyBudget = 0.1
	}
	if cfg.SmartDispatch.ClassifyTimeout == "" {
		cfg.SmartDispatch.ClassifyTimeout = "30s"
	}
	if cfg.SmartDispatch.ReviewBudget <= 0 {
		cfg.SmartDispatch.ReviewBudget = 0.2
	}

	// Multi-tenant defaults.
	if cfg.ClientsDir == "" {
		cfg.ClientsDir = filepath.Join(cfg.BaseDir, "clients")
	}
	if !filepath.IsAbs(cfg.ClientsDir) {
		cfg.ClientsDir = filepath.Join(cfg.BaseDir, cfg.ClientsDir)
	}
	if cfg.DefaultClientID == "" {
		cfg.DefaultClientID = "cli_default"
	}

	// Knowledge dir default.
	if cfg.KnowledgeDir == "" {
		cfg.KnowledgeDir = filepath.Join(cfg.BaseDir, "knowledge")
	}
	if !filepath.IsAbs(cfg.KnowledgeDir) {
		cfg.KnowledgeDir = filepath.Join(cfg.BaseDir, cfg.KnowledgeDir)
	}

	// Agents dir default.
	if cfg.AgentsDir == "" {
		cfg.AgentsDir = filepath.Join(cfg.BaseDir, "agents")
	}
	if !filepath.IsAbs(cfg.AgentsDir) {
		cfg.AgentsDir = filepath.Join(cfg.BaseDir, cfg.AgentsDir)
	}

	// Workspace dir default.
	if cfg.WorkspaceDir == "" {
		cfg.WorkspaceDir = filepath.Join(cfg.BaseDir, "workspace")
	}
	if !filepath.IsAbs(cfg.WorkspaceDir) {
		cfg.WorkspaceDir = filepath.Join(cfg.BaseDir, cfg.WorkspaceDir)
	}

	// Runtime dir default.
	if cfg.RuntimeDir == "" {
		cfg.RuntimeDir = filepath.Join(cfg.BaseDir, "runtime")
	}
	if !filepath.IsAbs(cfg.RuntimeDir) {
		cfg.RuntimeDir = filepath.Join(cfg.BaseDir, cfg.RuntimeDir)
	}

	// Vault dir default.
	if cfg.VaultDir == "" {
		cfg.VaultDir = filepath.Join(cfg.BaseDir, "vault")
	}
	if !filepath.IsAbs(cfg.VaultDir) {
		cfg.VaultDir = filepath.Join(cfg.BaseDir, cfg.VaultDir)
	}

	// Resolve relative paths to config dir.
	if !filepath.IsAbs(cfg.JobsFile) {
		cfg.JobsFile = filepath.Join(cfg.BaseDir, cfg.JobsFile)
	}
	if !filepath.IsAbs(cfg.HistoryDB) {
		cfg.HistoryDB = filepath.Join(cfg.BaseDir, cfg.HistoryDB)
	}
	if cfg.DefaultWorkdir != "" && !filepath.IsAbs(cfg.DefaultWorkdir) {
		cfg.DefaultWorkdir = filepath.Join(cfg.BaseDir, cfg.DefaultWorkdir)
	}

	// Resolve TLS paths relative to config dir.
	if cfg.TLS.CertFile != "" && !filepath.IsAbs(cfg.TLS.CertFile) {
		cfg.TLS.CertFile = filepath.Join(cfg.BaseDir, cfg.TLS.CertFile)
	}
	if cfg.TLS.KeyFile != "" && !filepath.IsAbs(cfg.TLS.KeyFile) {
		cfg.TLS.KeyFile = filepath.Join(cfg.BaseDir, cfg.TLS.KeyFile)
	}
	cfg.TLSEnabled = cfg.TLS.CertFile != "" && cfg.TLS.KeyFile != ""

	// Load .env file before resolving secrets so $ENV_VAR references work.
	if err := config.LoadDotEnv(filepath.Join(cfg.BaseDir, ".env")); err != nil {
		log.Warn("failed to load .env file", "error", err)
	}

	// Resolve $ENV_VAR references in secret fields.
	config.ResolveSecrets(&cfg)

	// Write MCP configs to temp files for --mcp-config flag.
	config.ResolveMCPPaths(&cfg)

	// Validate config.
	validateConfig(&cfg)

	// Initialize provider registry.
	cfg.Runtime.ProviderRegistry = initProviders(&cfg)

	// Initialize circuit breaker registry.
	cfg.Runtime.CircuitRegistry = circuit.NewRegistry(circuit.Config{
		Enabled:          cfg.CircuitBreaker.Enabled,
		FailThreshold:    cfg.CircuitBreaker.FailThreshold,
		SuccessThreshold: cfg.CircuitBreaker.SuccessThreshold,
		OpenTimeout:      cfg.CircuitBreaker.OpenTimeout,
	})

	return &cfg, nil
}

// validateConfig checks config values and logs warnings for common mistakes.
func validateConfig(cfg *Config) {
	// Check claude binary exists.
	claudePath := cfg.ClaudePath
	if claudePath == "" {
		claudePath = "claude"
	}
	if _, err := exec.LookPath(claudePath); err != nil {
		log.Warn("claude binary not found, tasks will fail", "path", claudePath)
	}

	// Validate listen address format.
	if cfg.ListenAddr != "" {
		parts := strings.SplitN(cfg.ListenAddr, ":", 2)
		if len(parts) != 2 {
			log.Warn("listenAddr should be host:port", "listenAddr", cfg.ListenAddr, "example", "127.0.0.1:7777")
		} else if _, err := strconv.Atoi(parts[1]); err != nil {
			log.Warn("listenAddr port is not a valid number", "port", parts[1])
		}
	}

	// Validate default timeout is parseable.
	if cfg.DefaultTimeout != "" {
		if _, err := time.ParseDuration(cfg.DefaultTimeout); err != nil {
			log.Warn("defaultTimeout is not a valid duration", "defaultTimeout", cfg.DefaultTimeout, "example", "15m, 1h")
		}
	}

	// Validate MaxConcurrent is reasonable.
	if cfg.MaxConcurrent > 20 {
		log.Warn("maxConcurrent is very high, claude sessions are resource-intensive", "maxConcurrent", cfg.MaxConcurrent)
	}

	// Warn if API token is empty.
	if cfg.APIToken == "" {
		log.Warn("apiToken is empty, API endpoints are unauthenticated")
	}

	// Validate default workdir exists.
	if cfg.DefaultWorkdir != "" {
		if _, err := os.Stat(cfg.DefaultWorkdir); err != nil {
			log.Warn("defaultWorkdir does not exist", "path", cfg.DefaultWorkdir)
		}
	}

	// Validate TLS cert/key files.
	if cfg.TLSEnabled {
		if _, err := os.Stat(cfg.TLS.CertFile); err != nil {
			log.Warn("tls.certFile does not exist", "path", cfg.TLS.CertFile)
		}
		if _, err := os.Stat(cfg.TLS.KeyFile); err != nil {
			log.Warn("tls.keyFile does not exist", "path", cfg.TLS.KeyFile)
		}
	}

	// Validate providers.
	for name, pc := range cfg.Providers {
		switch pc.Type {
		case "claude-cli":
			path := pc.Path
			if path == "" {
				path = cfg.ClaudePath
			}
			if path == "" {
				path = "claude"
			}
			if _, err := exec.LookPath(path); err != nil {
				log.Warn("provider binary not found", "provider", name, "path", path)
			}
		case "anthropic":
			if pc.APIKey == "" {
				log.Warn("provider has no apiKey", "provider", name)
			}
		case "openai-compatible":
			if pc.BaseURL == "" {
				log.Warn("provider has no baseUrl", "provider", name)
			}
		case "claude-api":
			if pc.APIKey == "" && os.Getenv("ANTHROPIC_API_KEY") == "" {
				log.Warn("provider has no apiKey and ANTHROPIC_API_KEY not set", "provider", name)
			}
		default:
			log.Warn("provider has unknown type", "provider", name, "type", pc.Type)
		}
	}

	// Validate allowedIPs format.
	for _, entry := range cfg.AllowedIPs {
		if !strings.Contains(entry, "/") {
			if net.ParseIP(entry) == nil {
				log.Warn("allowedIPs entry is not a valid IP address", "entry", entry)
			}
		} else {
			if _, _, err := net.ParseCIDR(entry); err != nil {
				log.Warn("allowedIPs entry is not a valid CIDR", "entry", entry, "error", err)
			}
		}
	}

	// Validate smart dispatch config.
	if cfg.SmartDispatch.Enabled {
		if _, ok := cfg.Agents[cfg.SmartDispatch.Coordinator]; !ok && cfg.SmartDispatch.Coordinator != "" {
			log.Warn("smartDispatch.coordinator agent not found in agents", "coordinator", cfg.SmartDispatch.Coordinator)
		}
		for _, rule := range cfg.SmartDispatch.Rules {
			if _, ok := cfg.Agents[rule.Agent]; !ok {
				log.Warn("smartDispatch rule references unknown agent", "agent", rule.Agent)
			}
		}
	}

	// Validate Docker sandbox config.
	if cfg.Docker.Enabled {
		if cfg.Docker.Image == "" {
			log.Warn("docker.enabled=true but docker.image is empty")
		}
		if err := sandbox.CheckDockerAvailable(); err != nil {
			log.Warn("docker sandbox enabled but unavailable", "error", err)
		}
	}
}


// configFileMu serializes all read-modify-write operations on the config file
// so concurrent HTTP handlers cannot interleave their reads and writes.
var configFileMu sync.Mutex

// updateConfigMCPs updates a single MCP config in config.json.
// If config is nil, the MCP entry is removed. Otherwise it is added/updated.
// Preserves all other config fields by reading/modifying/writing the raw JSON.
func updateConfigMCPs(configPath, mcpName string, mcpConfig json.RawMessage) error {
	configFileMu.Lock()
	defer configFileMu.Unlock()

	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	// Parse existing mcpConfigs.
	mcps := make(map[string]json.RawMessage)
	if mcpsRaw, ok := raw["mcpConfigs"]; ok {
		json.Unmarshal(mcpsRaw, &mcps)
	}

	if mcpConfig == nil {
		delete(mcps, mcpName)
	} else {
		mcps[mcpName] = mcpConfig
	}

	mcpsJSON, err := json.Marshal(mcps)
	if err != nil {
		return err
	}
	raw["mcpConfigs"] = mcpsJSON

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(configPath, append(out, '\n'), 0o600); err != nil {
		return err
	}
	// Auto-snapshot config version after MCP change.
	if cfg := config.LoadForVersioning(configPath); cfg != nil {
		version.SnapshotConfig(cfg.HistoryDB, configPath, "cli", fmt.Sprintf("mcp %s", mcpName))
	}
	return nil
}


// modelChangeResult holds the old and new model/provider for display.
type modelChangeResult struct {
	OldModel    string
	OldProvider string
	NewProvider string // empty if provider was not changed
}

// updateAgentModel updates an agent's model (and optionally provider) in config.
func updateAgentModel(cfg *Config, agentName, model, providerName string) (modelChangeResult, error) {
	ac, ok := cfg.Agents[agentName]
	if !ok {
		return modelChangeResult{}, fmt.Errorf("agent %q not found", agentName)
	}
	res := modelChangeResult{OldModel: ac.Model, OldProvider: ac.Provider}
	ac.Model = model
	if providerName != "" && providerName != ac.Provider {
		res.NewProvider = providerName
		ac.Provider = providerName
	}
	cfg.Agents[agentName] = ac
	configPath := findConfigPath()
	agentJSON, err := json.Marshal(&ac)
	if err != nil {
		return res, err
	}
	return res, cli.UpdateConfigAgents(configPath, agentName, agentJSON)
}


// --- from cli.go ---

var tetoraVersion = "dev"

func printUsage() {
	fmt.Fprintf(os.Stderr, `tetora v%s — AI Agent Orchestrator

Usage:
  tetora <command> [options]

Commands:
  serve              Start daemon (Telegram + Slack + HTTP + Cron)
  run                Dispatch tasks (CLI mode)
  dispatch           Run an ad-hoc task via the daemon
  route              Smart dispatch (auto-route to best agent)
  init               Interactive setup wizard
  doctor             Setup checks and diagnostics
  health             Runtime health (daemon, workers, taskboard, disk)
  status             Quick overview (daemon, jobs, cost)
  drain              Graceful shutdown: stop new tasks, wait for running agents to finish
  service <action>   Manage launchd service (install|uninstall|status)
  job <action>       Manage cron jobs (list|add|enable|disable|remove|trigger)
  agent <action>     Manage agents (list|add|show|set|remove)
  history <action>   View execution history (list|show|cost)
  config <action>    Manage config (show|set|validate|migrate)
  logs               View daemon logs ([-f] [-n N] [--err] [--trace ID] [--json])
  prompt <action>    Manage prompt templates (list|show|add|edit|remove)
  memory <action>    Manage agent memory (list|get|set|delete [--agent AGENT])
  mcp <action>       Manage MCP configs (list|show|add|remove|test)
  session <action>   View agent sessions (list|show)
  knowledge <action> Manage knowledge base (list|add|remove|path)
  skill <action>     Manage skills (list|run|test)
  workflow <action>  Manage workflows (list|show|validate|create|delete)
  budget <action>    Cost governance (show|pause|resume)
  webhook <action>   Manage incoming webhooks (list|show|test)
  data <action>      Data retention & privacy (status|cleanup|export|purge)
  security <action>  Security scanning (scan|baseline)
  plugin <action>    Manage external plugins (list|start|stop)
  access <action>    Manage agent directory access (list|add|remove)
  import <source>    Import data (config)
  release            Build, tag, and publish a release (atomic pipeline)
  upgrade [--force]  Upgrade to the latest release version
  backup             Create backup of tetora data
  restore            Restore from a backup file
  dashboard          Open web dashboard in browser
  completion <shell> Generate shell completion (bash|zsh|fish)
  version            Show version

Examples:
  tetora init                          Create config interactively
  tetora serve                         Start daemon
  tetora dispatch "Summarize README"    Run ad-hoc task via daemon
  tetora route "Review code security"  Auto-route to best agent
  tetora run --file tasks.json         Dispatch tasks from file
  tetora job list                      List all cron jobs
  tetora job trigger heartbeat         Manually trigger a job
  tetora agent list                    List all agents
  tetora agent add                     Create a new agent (interactive)
  tetora agent show <name>             Show agent details + soul preview
  tetora agent set <name> <field> <val> Update agent field (model, permission, description)
  tetora agent remove <name>           Remove an agent
  tetora history list                  Show recent execution history
  tetora history cost                  Show cost summary
  tetora config migrate --dry-run      Preview config migrations
  tetora session list                  List recent sessions
  tetora session list --agent <name>   List sessions for a specific agent
  tetora session show <id>            Show session conversation
  tetora backup                        Create backup
  tetora restore backup.tar.gz         Restore from backup
  tetora service install               Install as launchd service

`, tetoraVersion)
}

func cmdVersion() {
	fmt.Printf("tetora v%s (%s/%s)\n", tetoraVersion, runtime.GOOS, runtime.GOARCH)
}

// parseVersion splits a version string like "2.0.3" or "2.0.3.1" into numeric parts.
// Returns nil on parse failure.
func parseVersion(v string) []int {
	v = strings.TrimPrefix(v, "v")
	if v == "" || v == "dev" {
		return nil
	}
	parts := strings.Split(v, ".")
	nums := make([]int, len(parts))
	for i, p := range parts {
		n := 0
		for _, ch := range p {
			if ch < '0' || ch > '9' {
				return nil
			}
			n = n*10 + int(ch-'0')
		}
		nums[i] = n
	}
	return nums
}

// isDevVersion returns true if the version has a 4th segment (e.g. "2.0.3.1").
func isDevVersion(v string) bool {
	parts := parseVersion(v)
	return len(parts) > 3
}

// devBaseVersion returns the first 3 segments of a version string.
// e.g. "2.0.3.1" → "2.0.3", "2.0.3" → "2.0.3".
func devBaseVersion(v string) string {
	parts := parseVersion(v)
	if len(parts) < 3 {
		return v
	}
	return fmt.Sprintf("%d.%d.%d", parts[0], parts[1], parts[2])
}

// versionNewerThan returns true if a > b using semver comparison.
// A 3-part release (2.0.3) is considered newer than a 4-part dev (2.0.2.12).
// A release (2.0.3) is considered newer than a dev of the same base (2.0.3.1)
// because dev builds should always upgrade to matching releases.
func versionNewerThan(a, b string) bool {
	pa, pb := parseVersion(a), parseVersion(b)
	if pa == nil || pb == nil {
		return a != b // fallback: if unparseable, assume different = newer
	}
	// Compare up to the shorter length.
	maxLen := len(pa)
	if len(pb) > maxLen {
		maxLen = len(pb)
	}
	for i := 0; i < maxLen; i++ {
		va, vb := 0, 0
		if i < len(pa) {
			va = pa[i]
		}
		if i < len(pb) {
			vb = pb[i]
		}
		if va > vb {
			return true
		}
		if va < vb {
			return false
		}
	}
	return false // equal
}

func cmdUpgrade(args []string) {
	force := false
	for _, arg := range args {
		if arg == "--force" || arg == "-f" {
			force = true
		}
	}

	fmt.Printf("Current: v%s (%s/%s)\n", tetoraVersion, runtime.GOOS, runtime.GOARCH)
	if isDevVersion(tetoraVersion) {
		fmt.Println("  (dev build detected)")
	}

	// Fetch latest release tag from GitHub API.
	ghClient := &http.Client{Timeout: 15 * time.Second}
	resp, err := ghClient.Get("https://api.github.com/repos/TakumaLee/Tetora/releases/latest")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error checking latest release: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "Error checking latest release: HTTP %d\n", resp.StatusCode)
		os.Exit(1)
	}
	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing release info: %v\n", err)
		os.Exit(1)
	}
	latest := strings.TrimPrefix(release.TagName, "v")
	if latest == "" {
		fmt.Fprintf(os.Stderr, "Error: release tag_name is empty\n")
		os.Exit(1)
	}
	fmt.Printf("Latest:  v%s\n", latest)

	if !force {
		if latest == tetoraVersion {
			fmt.Println("Already up to date.")
			return
		}
		if isDevVersion(tetoraVersion) {
			// Dev build: upgrade if release matches or is newer than the base.
			// e.g. 2.0.3.1 → 2.0.3 (same base, upgrade to release) ✓
			// e.g. 2.0.2.12 → 2.0.3 (newer release) ✓
			// e.g. 2.0.4.1 → 2.0.3 (older release, skip) ✗
			base := devBaseVersion(tetoraVersion)
			if base != latest && !versionNewerThan(latest, base) {
				fmt.Printf("Dev build v%s is ahead of release v%s. Use --force to downgrade.\n", tetoraVersion, latest)
				return
			}
		} else if !versionNewerThan(latest, tetoraVersion) {
			fmt.Println("Already up to date (or newer). Use --force to override.")
			return
		}
	} else {
		fmt.Println("  (--force: skipping version check)")
	}

	// Determine binary name.
	arch := runtime.GOARCH
	binaryName := fmt.Sprintf("tetora-%s-%s", runtime.GOOS, arch)
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	dlURL := fmt.Sprintf("https://github.com/TakumaLee/Tetora/releases/download/%s/%s", release.TagName, binaryName)

	fmt.Printf("Downloading %s...\n", dlURL)
	dlClient := &http.Client{Timeout: 120 * time.Second} // binary ~15MB, allow time for slow connections
	dlResp, err := dlClient.Get(dlURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Download failed: %v\n", err)
		os.Exit(1)
	}
	defer dlResp.Body.Close()
	if dlResp.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "Download failed: HTTP %d\n", dlResp.StatusCode)
		os.Exit(1)
	}

	// Write to temp file then replace.
	selfPath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot determine binary path: %v\n", err)
		os.Exit(1)
	}
	selfPath, _ = filepath.EvalSymlinks(selfPath)

	tmpPath := selfPath + ".tmp"
	tmp, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot create temp file: %v\n", err)
		os.Exit(1)
	}
	if _, err := io.Copy(tmp, dlResp.Body); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		fmt.Fprintf(os.Stderr, "Download failed: %v\n", err)
		os.Exit(1)
	}
	tmp.Close()

	// Verify downloaded binary: size sanity check + run it to confirm version.
	info, err := os.Stat(tmpPath)
	if err != nil {
		os.Remove(tmpPath)
		fmt.Fprintf(os.Stderr, "Cannot stat downloaded binary: %v\n", err)
		os.Exit(1)
	}
	if info.Size() < 1024*1024 {
		os.Remove(tmpPath)
		fmt.Fprintf(os.Stderr, "Downloaded binary too small (%d bytes)\n", info.Size())
		os.Exit(1)
	}

	// Run the temp binary to verify its embedded version before replacing.
	verCtx, verCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer verCancel()
	verOut, err := exec.CommandContext(verCtx, tmpPath, "version").Output()
	if err != nil {
		os.Remove(tmpPath)
		fmt.Fprintf(os.Stderr, "Downloaded binary failed to execute: %v\n", err)
		os.Exit(1)
	}
	verStr := strings.TrimSpace(string(verOut))
	fmt.Printf("Downloaded binary reports: %s\n", verStr)
	if !strings.Contains(verStr, latest) {
		os.Remove(tmpPath)
		fmt.Fprintf(os.Stderr, "Version mismatch: expected v%s in output %q\n", latest, verStr)
		os.Exit(1)
	}

	// Replace old binary.
	if err := os.Rename(tmpPath, selfPath); err != nil {
		os.Remove(tmpPath)
		fmt.Fprintf(os.Stderr, "Cannot replace binary: %v\n", err)
		os.Exit(1)
	}

	// Post-replace verification: confirm the file on disk is the new version.
	postOut, err := exec.Command(selfPath, "version").Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: replaced binary but post-check failed: %v\n", err)
	} else {
		postStr := strings.TrimSpace(string(postOut))
		if !strings.Contains(postStr, latest) {
			fmt.Fprintf(os.Stderr, "WARNING: binary replaced but still reports %q (expected v%s)\n", postStr, latest)
			fmt.Fprintf(os.Stderr, "The file at %s may not have been updated. Try: tetora upgrade --force\n", selfPath)
		} else {
			fmt.Printf("Verified: %s\n", postStr)
		}
	}

	fmt.Printf("Binary replaced: v%s → v%s (%s)\n", tetoraVersion, latest, selfPath)

	// Check for running jobs before restarting — avoid killing in-flight tasks.
	if names := checkRunningJobs(); len(names) > 0 {
		fmt.Printf("\nWARNING: Running jobs detected: %s\n", strings.Join(names, ", "))
		fmt.Println("Binary on disk is updated, but daemon still runs the old version.")
		fmt.Println("Run 'tetora restart' manually when jobs finish.")
		return
	}

	// Restart daemon automatically.
	home, _ := os.UserHomeDir()
	plist := filepath.Join(home, "Library", "LaunchAgents", "com.tetora.daemon.plist")
	if _, err := os.Stat(plist); err == nil {
		fmt.Println("Restarting service (launchd)...")
		if err := cli.RestartLaunchd(plist); err != nil {
			fmt.Fprintf(os.Stderr, "Restart failed: %v\n", err)
			fmt.Println("Start manually with: tetora serve")
			return
		}
		waitForHealthy()
		return
	}

	// No launchd — find running daemon process and restart it.
	if restartDaemonProcess(selfPath) {
		waitForHealthy()
		return
	}

	fmt.Println("\nNo running daemon found. Start with:")
	fmt.Println("  tetora serve")
}

// restartDaemonProcess finds a running "tetora serve" process, kills it,
// and starts a new one in the background. Returns true if restart succeeded.
func restartDaemonProcess(binaryPath string) bool {
	// Check if there's a running daemon to restart.
	if len(cli.FindDaemonPIDs()) == 0 {
		return false
	}

	if !cli.KillDaemonProcess() {
		fmt.Fprintf(os.Stderr, "ERROR: could not stop old daemon, aborting restart\n")
		return true // old daemon exists but we couldn't kill it
	}

	// Start new daemon in background.
	fmt.Println("Starting daemon...")
	cmd := exec.Command(binaryPath, "serve")
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start daemon: %v\n", err)
		fmt.Println("Start manually with: tetora serve")
		return true // still return true since we killed the old one
	}

	// Release the child so it doesn't become a zombie.
	cmd.Process.Release()

	fmt.Printf("Daemon restarted (PID %d).\n", cmd.Process.Pid)
	return true
}

// cmdStart starts the tetora daemon if it is not already running.
func cmdStart() {
	out, _ := exec.Command("pgrep", "-f", "tetora serve").Output()
	if len(strings.TrimSpace(string(out))) > 0 {
		fmt.Println("Daemon is already running.")
		return
	}
	selfPath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot determine executable path: %v\n", err)
		os.Exit(1)
	}
	selfPath, _ = filepath.EvalSymlinks(selfPath)
	fmt.Println("Starting daemon...")
	cmd := exec.Command(selfPath, "serve")
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start daemon: %v\n", err)
		os.Exit(1)
	}
	cmd.Process.Release()
	fmt.Printf("Daemon started (PID %d).\n", cmd.Process.Pid)
	waitForHealthy()
}

// cmdRestart restarts the running tetora daemon.
func cmdRestart() {
	selfPath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot determine executable path: %v\n", err)
		os.Exit(1)
	}
	selfPath, _ = filepath.EvalSymlinks(selfPath)

	// Try launchd first.
	home, _ := os.UserHomeDir()
	plist := filepath.Join(home, "Library", "LaunchAgents", "com.tetora.daemon.plist")
	if _, err := os.Stat(plist); err == nil {
		fmt.Println("Restarting service (launchd)...")
		if err := cli.RestartLaunchd(plist); err != nil {
			fmt.Fprintf(os.Stderr, "Restart failed: %v\n", err)
			fmt.Println("Start manually with: tetora serve")
			return
		}
		waitForHealthy()
		return
	}

	// No launchd — find running daemon process and restart it.
	if restartDaemonProcess(selfPath) {
		waitForHealthy()
		return
	}

	fmt.Println("No running daemon found. Start with:")
	fmt.Println("  tetora serve")
}

// waitForHealthy polls /healthz for up to 10 seconds after a restart to confirm
// the daemon is up. Prints version on success or a warning on timeout.
func waitForHealthy() {
	cfg, _ := tryLoadConfig("")
	if cfg == nil || cfg.ListenAddr == "" {
		fmt.Println("Service restarted.")
		return
	}
	client := &http.Client{Timeout: 2 * time.Second}
	url := fmt.Sprintf("http://%s/healthz", cfg.ListenAddr)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		resp, err := client.Get(url)
		if err != nil {
			continue
		}
		var health map[string]any
		json.NewDecoder(resp.Body).Decode(&health)
		resp.Body.Close()
		if v, ok := health["version"].(string); ok {
			fmt.Printf("Service restarted. Health check: v%s OK\n", v)
			return
		}
	}
	fmt.Println("Service restarted (health check timed out — daemon may still be starting).")
}

// checkRunningJobs queries the daemon's /cron API and returns names of running jobs.
// Returns nil if daemon is unreachable or no jobs are running.
func checkRunningJobs() []string {
	cfg, _ := tryLoadConfig("")
	if cfg == nil || cfg.ListenAddr == "" {
		return nil
	}
	client := &http.Client{Timeout: 3 * time.Second}
	url := fmt.Sprintf("http://%s/cron", cfg.ListenAddr)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil
	}
	if cfg.APIToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIToken)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil
	}
	var jobs []CronJobInfo
	if err := json.NewDecoder(resp.Body).Decode(&jobs); err != nil {
		return nil
	}
	var running []string
	for _, j := range jobs {
		if j.Running {
			running = append(running, j.Name)
		}
	}
	return running
}

func cmdOpenDashboard() {
	cfg := loadConfig("")
	url := fmt.Sprintf("http://%s/dashboard", cfg.ListenAddr)
	fmt.Printf("Opening %s\n", url)
	exec.Command("open", url).Start()
}

// apiClient creates an HTTP client and base URL for daemon communication.
// Includes API token from config if set.
type apiClient struct {
	client  *http.Client
	baseURL string
	token   string
}

func newAPIClient(cfg *Config) *apiClient {
	return &apiClient{
		client:  &http.Client{Timeout: 5 * time.Second},
		baseURL: fmt.Sprintf("http://%s", cfg.ListenAddr),
		token:   cfg.APIToken,
	}
}

func (c *apiClient) do(method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	return c.client.Do(req)
}

func (c *apiClient) get(path string) (*http.Response, error) {
	return c.do("GET", path, nil)
}

func (c *apiClient) post(path string, body string) (*http.Response, error) {
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	return c.do("POST", path, r)
}

func (c *apiClient) postJSON(path string, v any) (*http.Response, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return c.do("POST", path, strings.NewReader(string(b)))
}

func findConfigPath() string {
	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "..", "config.json")
		if abs, err := filepath.Abs(candidate); err == nil {
			if _, err := os.Stat(abs); err == nil {
				return abs
			}
		}
	}
	home, _ := os.UserHomeDir()
	candidate := filepath.Join(home, ".tetora", "config.json")
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return "config.json"
}

// --- P23.7: Reliability & Operations ---

// QueuedMessage represents a message in the offline retry queue.
type QueuedMessage struct {
	ID            int    `json:"id"`
	Channel       string `json:"channel"`
	ChannelTarget string `json:"channelTarget"`
	MessageText   string `json:"messageText"`
	Priority      int    `json:"priority"`
	Status        string `json:"status"` // pending,sending,sent,failed,expired
	RetryCount    int    `json:"retryCount"`
	MaxRetries    int    `json:"maxRetries"`
	NextRetryAt   string `json:"nextRetryAt"`
	Error         string `json:"error"`
	CreatedAt     string `json:"createdAt"`
	UpdatedAt     string `json:"updatedAt"`
}

// ChannelHealthStatus tracks the health of a communication channel.
type ChannelHealthStatus struct {
	Channel      string `json:"channel"`
	Status       string `json:"status"` // healthy,degraded,offline
	LastError    string `json:"lastError"`
	LastSuccess  string `json:"lastSuccess"`
	FailureCount int    `json:"failureCount"`
	UpdatedAt    string `json:"updatedAt"`
}

// MessageQueueEngine manages message queueing and retry logic.
type MessageQueueEngine struct {
	cfg    *Config
	dbPath string
	mu     sync.Mutex
}

// initOpsDB creates the message_queue, backup_log, and channel_status tables.
func initOpsDB(dbPath string) error {
	ddl := `
CREATE TABLE IF NOT EXISTS message_queue (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    channel TEXT NOT NULL,
    channel_target TEXT NOT NULL,
    message_text TEXT NOT NULL,
    priority INTEGER DEFAULT 0,
    status TEXT DEFAULT 'pending',
    retry_count INTEGER DEFAULT 0,
    max_retries INTEGER DEFAULT 3,
    next_retry_at TEXT DEFAULT '',
    error TEXT DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_mq_status ON message_queue(status, next_retry_at);

CREATE TABLE IF NOT EXISTS backup_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    filename TEXT NOT NULL,
    size_bytes INTEGER DEFAULT 0,
    status TEXT DEFAULT 'success',
    duration_ms INTEGER DEFAULT 0,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS channel_status (
    channel TEXT PRIMARY KEY,
    status TEXT DEFAULT 'healthy',
    last_error TEXT DEFAULT '',
    last_success TEXT DEFAULT '',
    failure_count INTEGER DEFAULT 0,
    updated_at TEXT NOT NULL
);
`
	cmd := exec.Command("sqlite3", dbPath, ddl)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("init ops tables: %w: %s", err, string(out))
	}
	return nil
}

// newMessageQueueEngine creates a new message queue engine.
func newMessageQueueEngine(cfg *Config) *MessageQueueEngine {
	return &MessageQueueEngine{
		cfg:    cfg,
		dbPath: cfg.HistoryDB,
	}
}

// Enqueue adds a message to the queue for delivery.
func (mq *MessageQueueEngine) Enqueue(channel, target, text string, priority int) error {
	mq.mu.Lock()
	defer mq.mu.Unlock()

	if channel == "" || target == "" || text == "" {
		return fmt.Errorf("channel, target, and text are required")
	}

	maxRetries := mq.cfg.Ops.MessageQueue.RetryAttemptsOrDefault()
	maxSize := mq.cfg.Ops.MessageQueue.MaxQueueSizeOrDefault()

	// Check queue size limit.
	rows, err := db.Query(mq.dbPath, "SELECT COUNT(*) as cnt FROM message_queue WHERE status IN ('pending','sending')")
	if err == nil && len(rows) > 0 {
		cnt := jsonInt(rows[0]["cnt"])
		if cnt >= maxSize {
			return fmt.Errorf("queue full (%d/%d)", cnt, maxSize)
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	sql := fmt.Sprintf(
		`INSERT INTO message_queue (channel, channel_target, message_text, priority, status, max_retries, created_at, updated_at) VALUES ('%s', '%s', '%s', %d, 'pending', %d, '%s', '%s')`,
		db.Escape(channel), db.Escape(target), db.Escape(text),
		priority, maxRetries, now, now,
	)

	cmd := exec.Command("sqlite3", mq.dbPath, sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("enqueue: %w: %s", err, string(out))
	}
	return nil
}

// ProcessQueue processes pending messages with retry logic.
func (mq *MessageQueueEngine) ProcessQueue(ctx context.Context) {
	mq.mu.Lock()
	defer mq.mu.Unlock()

	now := time.Now().UTC().Format(time.RFC3339)

	// Select pending messages ready for delivery.
	sql := fmt.Sprintf(
		`SELECT id, channel, channel_target, message_text, priority, retry_count, max_retries FROM message_queue WHERE status='pending' AND (next_retry_at='' OR next_retry_at <= '%s') ORDER BY priority DESC, id ASC LIMIT 10`,
		now,
	)

	rows, err := db.Query(mq.dbPath, sql)
	if err != nil {
		log.Warn("message queue: query failed", "error", err)
		return
	}

	for _, row := range rows {
		select {
		case <-ctx.Done():
			return
		default:
		}

		id := jsonInt(row["id"])
		channel := fmt.Sprintf("%v", row["channel"])
		target := fmt.Sprintf("%v", row["channel_target"])
		text := fmt.Sprintf("%v", row["message_text"])
		retryCount := jsonInt(row["retry_count"])
		maxRetries := jsonInt(row["max_retries"])

		// Mark as sending.
		updateNow := time.Now().UTC().Format(time.RFC3339)
		updateSQL := fmt.Sprintf(
			`UPDATE message_queue SET status='sending', updated_at='%s' WHERE id=%d`,
			updateNow, id,
		)
		exec.Command("sqlite3", mq.dbPath, updateSQL).Run()

		// Attempt delivery (log for now - actual delivery integration is for later).
		deliveryErr := attemptDelivery(channel, target, text)

		updateNow = time.Now().UTC().Format(time.RFC3339)
		if deliveryErr == nil {
			// Success.
			successSQL := fmt.Sprintf(
				`UPDATE message_queue SET status='sent', updated_at='%s' WHERE id=%d`,
				updateNow, id,
			)
			exec.Command("sqlite3", mq.dbPath, successSQL).Run()

			// Update channel health.
			recordChannelHealth(mq.dbPath, channel, "healthy", "")

			log.Info("message queue: delivered", "id", id, "channel", channel, "target", target)
		} else {
			// Failure.
			retryCount++
			errMsg := db.Escape(deliveryErr.Error())

			if retryCount >= maxRetries {
				// Max retries exceeded.
				failSQL := fmt.Sprintf(
					`UPDATE message_queue SET status='failed', retry_count=%d, error='%s', updated_at='%s' WHERE id=%d`,
					retryCount, errMsg, updateNow, id,
				)
				exec.Command("sqlite3", mq.dbPath, failSQL).Run()

				log.Warn("message queue: permanently failed", "id", id, "channel", channel, "retries", retryCount)
			} else {
				// Schedule retry with exponential backoff: 30s * 2^retry_count.
				backoff := time.Duration(30*(1<<uint(retryCount))) * time.Second
				nextRetry := time.Now().UTC().Add(backoff).Format(time.RFC3339)

				retrySQL := fmt.Sprintf(
					`UPDATE message_queue SET status='pending', retry_count=%d, error='%s', next_retry_at='%s', updated_at='%s' WHERE id=%d`,
					retryCount, errMsg, nextRetry, updateNow, id,
				)
				exec.Command("sqlite3", mq.dbPath, retrySQL).Run()

				log.Info("message queue: retry scheduled", "id", id, "channel", channel, "retryCount", retryCount, "nextRetry", nextRetry)
			}

			// Update channel health.
			recordChannelHealth(mq.dbPath, channel, "degraded", deliveryErr.Error())
		}
	}
}

// attemptDelivery tries to deliver a message. For now just logs it.
// Actual channel delivery will be integrated later.
func attemptDelivery(channel, target, text string) error {
	// Placeholder: real delivery integration will come later.
	// For now, all deliveries succeed (logged only).
	log.Debug("message queue: delivery attempt", "channel", channel, "target", target, "textLen", len(text))
	return nil
}

// Start runs the message queue processor as a background goroutine.
func (mq *MessageQueueEngine) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				mq.ProcessQueue(ctx)
			}
		}
	}()
}

// QueueStats returns counts of messages by status.
func (mq *MessageQueueEngine) QueueStats() map[string]int {
	stats := map[string]int{
		"pending": 0,
		"sending": 0,
		"sent":    0,
		"failed":  0,
	}

	rows, err := db.Query(mq.dbPath, "SELECT status, COUNT(*) as cnt FROM message_queue GROUP BY status")
	if err != nil {
		return stats
	}
	for _, row := range rows {
		status := fmt.Sprintf("%v", row["status"])
		cnt := jsonInt(row["cnt"])
		stats[status] = cnt
	}
	return stats
}

// recordChannelHealth updates the health status of a channel.
func recordChannelHealth(dbPath, channel, status, lastError string) error {
	now := time.Now().UTC().Format(time.RFC3339)

	var sql string
	if status == "healthy" {
		sql = fmt.Sprintf(
			`INSERT INTO channel_status (channel, status, last_success, failure_count, updated_at) VALUES ('%s', '%s', '%s', 0, '%s') ON CONFLICT(channel) DO UPDATE SET status='%s', last_success='%s', failure_count=0, updated_at='%s'`,
			db.Escape(channel), status, now, now,
			status, now, now,
		)
	} else {
		sql = fmt.Sprintf(
			`INSERT INTO channel_status (channel, status, last_error, failure_count, updated_at) VALUES ('%s', '%s', '%s', 1, '%s') ON CONFLICT(channel) DO UPDATE SET status='%s', last_error='%s', failure_count=failure_count+1, updated_at='%s'`,
			db.Escape(channel), status, db.Escape(lastError), now,
			status, db.Escape(lastError), now,
		)
	}

	cmd := exec.Command("sqlite3", dbPath, sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("record channel health: %w: %s", err, string(out))
	}
	return nil
}

// getChannelHealth returns the health status of all channels.
func getChannelHealth(dbPath string) ([]ChannelHealthStatus, error) {
	rows, err := db.Query(dbPath, "SELECT channel, status, last_error, last_success, failure_count, updated_at FROM channel_status ORDER BY channel")
	if err != nil {
		return nil, err
	}

	var results []ChannelHealthStatus
	for _, row := range rows {
		results = append(results, ChannelHealthStatus{
			Channel:      fmt.Sprintf("%v", row["channel"]),
			Status:       fmt.Sprintf("%v", row["status"]),
			LastError:    fmt.Sprintf("%v", row["last_error"]),
			LastSuccess:  fmt.Sprintf("%v", row["last_success"]),
			FailureCount: jsonInt(row["failure_count"]),
			UpdatedAt:    fmt.Sprintf("%v", row["updated_at"]),
		})
	}
	return results, nil
}

// getSystemHealth returns an overall system health summary.
func getSystemHealth(cfg *Config) map[string]any {
	health := map[string]any{
		"status":    "healthy",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}

	// Check DB accessibility.
	dbOK := false
	if cfg.HistoryDB != "" {
		rows, err := db.Query(cfg.HistoryDB, "SELECT 1 as ok")
		if err == nil && len(rows) > 0 {
			dbOK = true
		}
	}
	health["database"] = map[string]any{
		"status": boolToHealthy(dbOK),
		"path":   cfg.HistoryDB,
	}

	// Channel health.
	channels := []ChannelHealthStatus{}
	if cfg.HistoryDB != "" {
		ch, err := getChannelHealth(cfg.HistoryDB)
		if err == nil {
			channels = ch
		}
	}
	health["channels"] = channels

	// Message queue stats.
	if cfg.Ops.MessageQueue.Enabled && cfg.HistoryDB != "" {
		mqe := newMessageQueueEngine(cfg)
		health["messageQueue"] = mqe.QueueStats()
	}

	// Active integrations.
	integrations := map[string]bool{
		"telegram":  cfg.Telegram.Enabled,
		"slack":     cfg.Slack.Enabled,
		"discord":   cfg.Discord.Enabled,
		"whatsapp":  cfg.WhatsApp.Enabled,
		"line":      cfg.LINE.Enabled,
		"matrix":    cfg.Matrix.Enabled,
		"teams":     cfg.Teams.Enabled,
		"signal":    cfg.Signal.Enabled,
		"gchat":     cfg.GoogleChat.Enabled,
		"gmail":     cfg.Gmail.Enabled,
		"calendar":  cfg.Calendar.Enabled,
		"twitter":   cfg.Twitter.Enabled,
		"imessage":  cfg.IMessage.Enabled,
		"homeassistant": cfg.HomeAssistant.Enabled,
	}
	health["integrations"] = integrations

	// Count unhealthy channels.
	unhealthyCount := 0
	for _, ch := range channels {
		if ch.Status != "healthy" {
			unhealthyCount++
		}
	}
	if !dbOK {
		health["status"] = "degraded"
	} else if unhealthyCount > 0 {
		health["status"] = "degraded"
	}

	// Config summary.
	health["config"] = map[string]any{
		"maxConcurrent":  cfg.MaxConcurrent,
		"defaultModel":   cfg.DefaultModel,
		"defaultTimeout": cfg.DefaultTimeout,
		"providers":      len(cfg.Providers),
		"agents":         len(cfg.Agents),
	}

	return health
}

// boolToHealthy returns "healthy" or "offline" based on a bool.
func boolToHealthy(ok bool) string {
	if ok {
		return "healthy"
	}
	return "offline"
}

// --- Tool Handlers for P23.7 ---

// toolBackupNow triggers an immediate backup.
func toolBackupNow(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	bs := scheduling.NewBackupScheduler(scheduling.BackupConfig{
		DBPath:     cfg.HistoryDB,
		BackupDir:  cfg.Ops.BackupDirResolved(cfg.BaseDir),
		RetainDays: cfg.Ops.BackupRetainOrDefault(),
		EscapeSQL:  db.Escape,
		LogInfo:    log.Info,
		LogWarn:    log.Warn,
	})
	result, err := bs.RunBackup()
	if err != nil {
		return "", fmt.Errorf("backup failed: %w", err)
	}
	b, _ := json.Marshal(result)
	return string(b), nil
}

// toolExportData triggers a GDPR data export.
func toolExportData(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		UserID string `json:"userId"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if !cfg.Ops.ExportEnabled {
		return "", fmt.Errorf("data export is not enabled in config (ops.exportEnabled)")
	}

	result, err := export.UserData(cfg.HistoryDB, cfg.BaseDir, args.UserID)
	if err != nil {
		return "", fmt.Errorf("export failed: %w", err)
	}
	b, _ := json.Marshal(result)
	return string(b), nil
}

// toolSystemHealth returns the system health summary.
func toolSystemHealth(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	health := getSystemHealth(cfg)
	b, _ := json.MarshalIndent(health, "", "  ")
	return string(b), nil
}

// --- Cleanup helper ---

// cleanupExpiredMessages removes old sent/failed messages from the queue.
func cleanupExpiredMessages(dbPath string, retainDays int) error {
	if retainDays <= 0 {
		retainDays = 7
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -retainDays).Format(time.RFC3339)
	sql := fmt.Sprintf(
		`DELETE FROM message_queue WHERE status IN ('sent','failed','expired') AND updated_at < '%s'`,
		cutoff,
	)

	cmd := exec.Command("sqlite3", dbPath, sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("cleanup expired messages: %w: %s", err, string(out))
	}

	// Also clean old backup logs.
	sql = fmt.Sprintf(
		`DELETE FROM backup_log WHERE created_at < '%s'`,
		time.Now().UTC().AddDate(0, 0, -90).Format(time.RFC3339),
	)
	exec.Command("sqlite3", dbPath, sql).Run()

	return nil
}

// --- Queue Status Summary ---

// queueStatusSummary returns a human-readable summary of the message queue.
func queueStatusSummary(dbPath string) string {
	rows, err := db.Query(dbPath, "SELECT status, COUNT(*) as cnt FROM message_queue GROUP BY status")
	if err != nil {
		return "message queue: unavailable"
	}
	if len(rows) == 0 {
		return "message queue: empty"
	}

	var parts []string
	for _, row := range rows {
		status := fmt.Sprintf("%v", row["status"])
		cnt := jsonInt(row["cnt"])
		parts = append(parts, fmt.Sprintf("%s=%d", status, cnt))
	}
	return "message queue: " + strings.Join(parts, ", ")
}

func cmdWorkflow(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: tetora workflow <command> [options]")
		fmt.Println()
		fmt.Println("Commands:")
		fmt.Println("  list                                       List all workflows")
		fmt.Println("  show   <name>                              Show workflow definition")
		fmt.Println("  validate <name|file>                       Validate a workflow")
		fmt.Println("  create <file>                              Import workflow from JSON file")
		fmt.Println("  export <name> [-o file]                    Export workflow as shareable JSON package")
		fmt.Println("  delete <name>                              Delete a workflow")
		fmt.Println("  run  <name> [--var key=value ...] [--dry-run|--shadow]  Execute a workflow")
		fmt.Println("  resume <run-id>                            Resume a failed/cancelled run from checkpoint")
		fmt.Println("  runs [name]                                List workflow run history")
		fmt.Println("  status <run-id>                            Show run status")
		fmt.Println("  messages <run-id>                          Show agent messages for a run")
		fmt.Println("  history  <name>                            Show version history")
		fmt.Println("  rollback <name> <version-id>               Restore to a previous version")
		fmt.Println("  diff     <version1> <version2>             Compare two versions")
		return
	}
	// Try version-related subcommands first.
	if cli.HandleWorkflowVersionSubcommands(args[0], args[1:]) {
		return
	}
	switch args[0] {
	case "list", "ls":
		workflowListCmd()
	case "show":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: tetora workflow show <name>")
			os.Exit(1)
		}
		workflowShowCmd(args[1])
	case "validate":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: tetora workflow validate <name|file>")
			os.Exit(1)
		}
		workflowValidateCmd(args[1])
	case "create":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: tetora workflow create <file>")
			os.Exit(1)
		}
		workflowCreateCmd(args[1])
	case "export":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: tetora workflow export <name> [-o file]")
			os.Exit(1)
		}
		outFile := ""
		for i := 2; i < len(args); i++ {
			if args[i] == "-o" && i+1 < len(args) {
				outFile = args[i+1]
				i++
			}
		}
		workflowExportCmd(args[1], outFile)
	case "delete", "rm":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: tetora workflow delete <name>")
			os.Exit(1)
		}
		workflowDeleteCmd(args[1])
	case "run":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: tetora workflow run <name> [--var key=value ...] [--dry-run|--shadow]")
			os.Exit(1)
		}
		workflowRunCmd(args[1], args[2:])
	case "resume":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: tetora workflow resume <run-id>")
			os.Exit(1)
		}
		workflowResumeCmd(args[1])
	case "runs":
		name := ""
		if len(args) > 1 {
			name = args[1]
		}
		workflowRunsCmd(name)
	case "status":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: tetora workflow status <run-id>")
			os.Exit(1)
		}
		workflowStatusCmd(args[1])
	case "messages", "msgs":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: tetora workflow messages <run-id>")
			os.Exit(1)
		}
		workflowMessagesCmd(args[1])
	default:
		fmt.Fprintf(os.Stderr, "Unknown workflow action: %s\n", args[0])
		os.Exit(1)
	}
}

func workflowListCmd() {
	cfg := loadConfig(findConfigPath())
	workflows, err := listWorkflows(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(workflows) == 0 {
		fmt.Println("No workflows defined.")
		fmt.Printf("Create one in: %s\n", workflowDir(cfg))
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSTEPS\tTIMEOUT\tDESCRIPTION")
	for _, wf := range workflows {
		desc := wf.Description
		if len(desc) > 50 {
			desc = desc[:50] + "..."
		}
		timeout := wf.Timeout
		if timeout == "" {
			timeout = "-"
		}
		fmt.Fprintf(w, "%s\t%d\t%s\t%s\n", wf.Name, len(wf.Steps), timeout, desc)
	}
	w.Flush()
}

func workflowShowCmd(name string) {
	cfg := loadConfig(findConfigPath())
	wf, err := loadWorkflowByName(cfg, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Print summary header.
	fmt.Printf("Workflow: %s\n", wf.Name)
	if wf.Description != "" {
		fmt.Printf("Description: %s\n", wf.Description)
	}
	if wf.Timeout != "" {
		fmt.Printf("Timeout: %s\n", wf.Timeout)
	}
	if len(wf.Variables) > 0 {
		fmt.Println("Variables:")
		for k, v := range wf.Variables {
			if v == "" {
				fmt.Printf("  %s (required)\n", k)
			} else {
				fmt.Printf("  %s = %q\n", k, v)
			}
		}
	}

	fmt.Printf("\nSteps (%d):\n", len(wf.Steps))
	for i, s := range wf.Steps {
		st := s.Type
		if st == "" {
			st = "dispatch"
		}
		prefix := "  "
		if i == len(wf.Steps)-1 {
			prefix = "  "
		}
		fmt.Printf("%s[%s] %s (type=%s", prefix, s.ID, stepSummary(&s), st)
		if s.Agent != "" {
			fmt.Printf(", agent=%s", s.Agent)
		}
		if len(s.DependsOn) > 0 {
			fmt.Printf(", after=%s", strings.Join(s.DependsOn, ","))
		}
		fmt.Println(")")
	}

	// Also output raw JSON for piping.
	fmt.Println("\n--- JSON ---")
	data, _ := json.MarshalIndent(wf, "", "  ")
	fmt.Println(string(data))
}

func workflowValidateCmd(nameOrFile string) {
	cfg := loadConfig(findConfigPath())

	var wf *Workflow
	var err error

	// Try as file first, then as name.
	if strings.HasSuffix(nameOrFile, ".json") || strings.Contains(nameOrFile, "/") {
		wf, err = loadWorkflow(nameOrFile)
	} else {
		wf, err = loadWorkflowByName(cfg, nameOrFile)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading: %v\n", err)
		os.Exit(1)
	}

	errs := validateWorkflow(wf)
	if len(errs) == 0 {
		fmt.Printf("Workflow %q is valid. (%d steps)\n", wf.Name, len(wf.Steps))

		// Show execution order.
		order := topologicalSort(wf.Steps)
		fmt.Printf("Execution order: %s\n", strings.Join(order, " -> "))
	} else {
		fmt.Fprintf(os.Stderr, "Validation errors (%d):\n", len(errs))
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "  - %s\n", e)
		}
		os.Exit(1)
	}
}

func workflowCreateCmd(file string) {
	cfg := loadConfig(findConfigPath())

	wf, err := loadWorkflow(file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading %s: %v\n", file, err)
		os.Exit(1)
	}

	// Validate before saving.
	errs := validateWorkflow(wf)
	if len(errs) > 0 {
		fmt.Fprintf(os.Stderr, "Validation errors:\n")
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "  - %s\n", e)
		}
		os.Exit(1)
	}

	if err := saveWorkflow(cfg, wf); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Workflow %q saved. (%d steps)\n", wf.Name, len(wf.Steps))
}

func workflowDeleteCmd(name string) {
	cfg := loadConfig(findConfigPath())
	if err := deleteWorkflow(cfg, name); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Workflow %q deleted.\n", name)
}

func workflowRunCmd(name string, flags []string) {
	cfg := loadConfig(findConfigPath())

	wf, err := loadWorkflowByName(cfg, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Validate before running.
	if errs := validateWorkflow(wf); len(errs) > 0 {
		fmt.Fprintf(os.Stderr, "Validation errors:\n")
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "  - %s\n", e)
		}
		os.Exit(1)
	}

	// Parse --var key=value, --dry-run, --shadow flags.
	vars := make(map[string]string)
	dryRun := false
	shadow := false
	for i := 0; i < len(flags); i++ {
		switch flags[i] {
		case "--var":
			if i+1 < len(flags) {
				kv := flags[i+1]
				parts := strings.SplitN(kv, "=", 2)
				if len(parts) == 2 {
					vars[parts[0]] = parts[1]
				}
				i++
			}
		case "--dry-run":
			dryRun = true
		case "--shadow":
			shadow = true
		}
	}

	mode := WorkflowModeLive
	if dryRun {
		mode = WorkflowModeDryRun
		fmt.Printf("Dry-run mode: no provider calls, estimating costs...\n")
	} else if shadow {
		mode = WorkflowModeShadow
		fmt.Printf("Shadow mode: executing but not recording to history...\n")
	}

	fmt.Printf("Running workflow %q (%d steps)...\n", wf.Name, len(wf.Steps))

	// Create minimal state (no SSE for CLI).
	state := newDispatchState()
	sem := make(chan struct{}, cfg.MaxConcurrent)
	if cfg.MaxConcurrent <= 0 {
		sem = make(chan struct{}, 4)
	}
	cfg.Runtime.ProviderRegistry = initProviders(cfg)

	run := executeWorkflow(context.Background(), cfg, wf, vars, state, sem, nil, mode)

	// Print results.
	fmt.Printf("\nWorkflow: %s\n", run.WorkflowName)
	fmt.Printf("Run ID:   %s\n", run.ID)
	fmt.Printf("Status:   %s\n", run.Status)
	fmt.Printf("Duration: %s\n", formatDurationMs(run.DurationMs))
	fmt.Printf("Cost:     $%.4f\n", run.TotalCost)

	if run.Error != "" {
		fmt.Printf("Error:    %s\n", run.Error)
	}

	fmt.Printf("\nStep Results:\n")
	order := topologicalSort(wf.Steps)
	for _, stepID := range order {
		sr := run.StepResults[stepID]
		if sr == nil {
			continue
		}
		icon := statusIcon(sr.Status)
		fmt.Printf("  %s [%s] %s (%s, $%.4f)\n",
			icon, sr.StepID, sr.Status, formatDurationMs(sr.DurationMs), sr.CostUSD)
		if sr.Error != "" {
			fmt.Printf("      Error: %s\n", sr.Error)
		}
		if sr.Output != "" {
			preview := sr.Output
			if len(preview) > 200 {
				preview = preview[:200] + "..."
			}
			fmt.Printf("      Output: %s\n", strings.TrimSpace(preview))
		}
	}

	if run.Status != "success" {
		os.Exit(1)
	}
}

func workflowResumeCmd(runID string) {
	cfg := loadConfig(findConfigPath())
	resolvedID := resolveWorkflowRunID(cfg, runID)

	originalRun, err := queryWorkflowRunByID(cfg.HistoryDB, resolvedID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if !isResumableStatus(originalRun.Status) {
		fmt.Fprintf(os.Stderr, "Run %s has status %q — only error/cancelled/timeout runs can be resumed.\n",
			resolvedID[:8], originalRun.Status)
		os.Exit(1)
	}

	// Count completed steps.
	completed := 0
	total := len(originalRun.StepResults)
	for _, sr := range originalRun.StepResults {
		if sr.Status == "success" || sr.Status == "skipped" {
			completed++
		}
	}
	fmt.Printf("Resuming run %s (%s) — %d/%d steps already completed\n",
		resolvedID[:8], originalRun.WorkflowName, completed, total)

	state := newDispatchState()
	sem := make(chan struct{}, 5)
	childSem := make(chan struct{}, 10)

	run, err := resumeWorkflow(context.Background(), cfg, resolvedID, state, sem, childSem, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Resume failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("New run: %s  Status: %s  Duration: %s  Cost: $%.4f\n",
		run.ID[:8], run.Status, formatDurationMs(run.DurationMs), run.TotalCost)
	if run.Status != "success" {
		os.Exit(1)
	}
}

func workflowRunsCmd(name string) {
	cfg := loadConfig(findConfigPath())
	runs, err := queryWorkflowRuns(cfg.HistoryDB, 20, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(runs) == 0 {
		fmt.Println("No workflow runs found.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tWORKFLOW\tSTATUS\tDURATION\tCOST\tSTARTED")
	for _, r := range runs {
		id := r.ID
		if len(id) > 8 {
			id = id[:8]
		}
		dur := formatDurationMs(r.DurationMs)
		cost := fmt.Sprintf("$%.4f", r.TotalCost)
		started := r.StartedAt
		if len(started) > 19 {
			started = started[:19]
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			id, r.WorkflowName, r.Status, dur, cost, started)
	}
	w.Flush()
}

func workflowStatusCmd(runID string) {
	cfg := loadConfig(findConfigPath())

	run, err := queryWorkflowRunByID(cfg.HistoryDB, runID)
	if err != nil {
		// Try prefix match.
		runs, qerr := queryWorkflowRuns(cfg.HistoryDB, 100, "")
		if qerr != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		for _, r := range runs {
			if strings.HasPrefix(r.ID, runID) {
				run = &r
				break
			}
		}
		if run == nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}

	out, _ := json.MarshalIndent(run, "", "  ")
	fmt.Println(string(out))
}

func statusIcon(status string) string {
	switch status {
	case "success":
		return "OK"
	case "error":
		return "ERR"
	case "timeout":
		return "TMO"
	case "skipped":
		return "SKP"
	case "running":
		return "RUN"
	case "cancelled":
		return "CXL"
	default:
		return "---"
	}
}

func workflowMessagesCmd(runID string) {
	cfg := loadConfig(findConfigPath())

	// Resolve prefix match for run ID.
	resolvedID := resolveWorkflowRunID(cfg, runID)

	// Fetch handoffs.
	handoffs, err := queryHandoffs(cfg.HistoryDB, resolvedID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading handoffs: %v\n", err)
	}

	// Fetch agent messages.
	msgs, err := queryAgentMessages(cfg.HistoryDB, resolvedID, "", 100)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading messages: %v\n", err)
	}

	if len(handoffs) == 0 && len(msgs) == 0 {
		fmt.Println("No agent messages or handoffs for this run.")
		return
	}

	// Print handoffs.
	if len(handoffs) > 0 {
		fmt.Printf("Handoffs (%d):\n", len(handoffs))
		for _, h := range handoffs {
			id := h.ID
			if len(id) > 8 {
				id = id[:8]
			}
			fmt.Printf("  [%s] %s → %s  status=%s\n", id, h.FromAgent, h.ToAgent, h.Status)
			if h.Instruction != "" {
				inst := h.Instruction
				if len(inst) > 80 {
					inst = inst[:80] + "..."
				}
				fmt.Printf("         instruction: %s\n", inst)
			}
		}
		fmt.Println()
	}

	// Print messages.
	if len(msgs) > 0 {
		fmt.Printf("Agent Messages (%d):\n", len(msgs))
		for _, m := range msgs {
			ts := m.CreatedAt
			if len(ts) > 19 {
				ts = ts[:19]
			}
			content := m.Content
			if len(content) > 100 {
				content = content[:100] + "..."
			}
			fmt.Printf("  %s  [%s] %s → %s: %s\n", ts, m.Type, m.FromAgent, m.ToAgent, content)
		}
	}
}

// resolveWorkflowRunID tries to resolve a short prefix to a full run ID.
func resolveWorkflowRunID(cfg *Config, runID string) string {
	// Try exact match first.
	run, err := queryWorkflowRunByID(cfg.HistoryDB, runID)
	if err == nil && run != nil {
		return run.ID
	}
	// Try prefix match.
	runs, err := queryWorkflowRuns(cfg.HistoryDB, 100, "")
	if err != nil {
		return runID
	}
	for _, r := range runs {
		if strings.HasPrefix(r.ID, runID) {
			return r.ID
		}
	}
	return runID
}

func workflowExportCmd(name, outFile string) {
	cfg := loadConfig(findConfigPath())
	wf, err := loadWorkflowByName(cfg, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Validate before export.
	if errs := validateWorkflow(wf); len(errs) > 0 {
		fmt.Fprintln(os.Stderr, "Warning: workflow has validation issues:")
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "  - %s\n", e)
		}
	}

	// Build export package.
	pkg := map[string]any{
		"tetoraExport": "workflow/v1",
		"exportedAt":   time.Now().UTC().Format(time.RFC3339),
		"workflow":     wf,
	}

	data, err := json.MarshalIndent(pkg, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error marshalling: %v\n", err)
		os.Exit(1)
	}

	if outFile != "" {
		if err := os.WriteFile(outFile, data, 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing file: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Exported '%s' to %s\n", name, outFile)
	} else {
		fmt.Println(string(data))
	}
}

// stepSummary returns a short summary of what a step does.
func stepSummary(s *WorkflowStep) string {
	st := s.Type
	if st == "" {
		st = "dispatch"
	}
	switch st {
	case "dispatch":
		p := s.Prompt
		if len(p) > 40 {
			p = p[:40] + "..."
		}
		return p
	case "skill":
		return fmt.Sprintf("skill:%s", s.Skill)
	case "condition":
		return fmt.Sprintf("if %s", s.If)
	case "parallel":
		return fmt.Sprintf("%d parallel tasks", len(s.Parallel))
	default:
		return st
	}
}

func runGuide(args []string) {
	isJSON := false
	for _, arg := range args {
		if arg == "--json" {
			isJSON = true
		}
	}

	guideData := map[string]any{
		"title": "Tetora — Quick Guide",
		"sections": []map[string]any{
			{
				"name": "Getting Started",
				"commands": []map[string]any{
					map[string]any{"cmd": "tetora init", "desc": "Set up Tetora for the first time"},
					map[string]any{"cmd": "tetora project add", "desc": "Onboard an existing project (analyze + create tasks)"},
					map[string]any{"cmd": "tetora serve", "desc": "Start the daemon + dashboard"},
				},
			},
			{
				"name": "Agents",
				"commands": []map[string]any{
					map[string]any{"cmd": "tetora agent list", "desc": "List all configured agents"},
					map[string]any{"cmd": "tetora agent chat <name>", "desc": "Start a chat session with an agent"},
				},
			},
			{
				"name": "Workflows",
				"commands": []map[string]any{
					map[string]any{"cmd": "tetora workflow list", "desc": "List available workflows"},
					map[string]any{"cmd": "tetora workflow run <name> [--var k=v]", "desc": "Run a workflow"},
				},
			},
			{
				"name": "Task Board",
				"commands": []map[string]any{
					map[string]any{"cmd": "tetora task list", "desc": "List tasks"},
					map[string]any{"cmd": "tetora task create", "desc": "Create a new task"},
					map[string]any{"cmd": "tetora task show <id>", "desc": "Show task detail"},
				},
			},
			{
				"name": "Diagnostics",
				"commands": []map[string]any{
					map[string]any{"cmd": "tetora doctor", "desc": "Check configuration health"},
					map[string]any{"cmd": "tetora status", "desc": "Show daemon status"},
					map[string]any{"cmd": "tetora guide", "desc": "Show this guide"},
				},
			},
		},
	}

	if isJSON {
		json.NewEncoder(os.Stdout).Encode(guideData)
		return
	}

	fmt.Println("\033[1mTetora — Quick Guide\033[0m")
	fmt.Println()
	for _, s := range guideData["sections"].([]map[string]any) {
		fmt.Printf("\033[36m%s\033[0m\n", s["name"])
		for _, c := range s["commands"].([]map[string]any) {
			fmt.Printf("  %-42s %s\n", c["cmd"].(string), c["desc"].(string))
		}
		fmt.Println()
	}
}
