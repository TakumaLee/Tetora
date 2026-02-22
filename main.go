package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
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
		case "role":
			cmdRole(os.Args[2:])
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
		case "session":
			cmdSession(os.Args[2:])
			return
		case "budget":
			cmdBudget(os.Args[2:])
			return
		case "trust":
			cmdTrust(os.Args[2:])
			return
		case "webhook":
			cmdWebhook(os.Args[2:])
			return
		case "data":
			cmdData(os.Args[2:])
			return
		case "backup":
			cmdBackup(os.Args[2:])
			return
		case "restore":
			cmdRestore(os.Args[2:])
			return
		case "dashboard":
			cmdOpenDashboard()
			return
		case "completion":
			cmdCompletion(os.Args[2:])
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

	// Initialize structured logger from config.
	// Backward compat: cfg.Log=true with no explicit level → debug.
	if cfg.Log && cfg.Logging.Level == "" {
		cfg.Logging.Level = "debug"
	}
	defaultLogger = initLogger(cfg.Logging, cfg.baseDir)

	// Shared concurrency semaphore — limits total concurrent claude sessions.
	sem := make(chan struct{}, cfg.MaxConcurrent)

	state := newDispatchState()
	state.broker = newSSEBroker()

	// Signal handling.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	if *serve {
		// --- Daemon mode ---
		logInfo("tetora v2 starting", "maxConcurrent", cfg.MaxConcurrent)

		// Init history DB.
		if cfg.HistoryDB != "" {
			if err := initHistoryDB(cfg.HistoryDB); err != nil {
				logWarn("init history db failed", "error", err)
			} else {
				logInfo("history db initialized", "path", cfg.HistoryDB)
				// Cleanup records using retention config.
				if err := cleanupHistory(cfg.HistoryDB, retentionDays(cfg.Retention.History, 90)); err != nil {
					logWarn("cleanup history failed", "error", err)
				}
			}
			// Init audit log table.
			if err := initAuditLog(cfg.HistoryDB); err != nil {
				logWarn("init audit_log failed", "error", err)
			}
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

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

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
		cron := newCronEngine(cfg, sem, notifyFn)
		if err := cron.loadJobs(); err != nil {
			logWarn("cron load error, continuing without cron", "error", err)
		} else {
			cron.start(ctx)
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
		if cfg.Budgets.Global.Daily > 0 || cfg.Budgets.Global.Weekly > 0 || cfg.Budgets.Global.Monthly > 0 || len(cfg.Budgets.Roles) > 0 {
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
			slackBot = newSlackBot(cfg, state, sem, cron)
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
			discordBot = newDiscordBot(cfg, state, sem, cron)
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

		// HTTP server.
		srv := startHTTPServer(cfg.ListenAddr, state, cfg, sem, cron, secMon, mcpHost, slackBot)

		// Start Telegram bot.
		if cfg.Telegram.Enabled && cfg.Telegram.BotToken != "" {
			bot = newBot(cfg, state, sem, cron)
			// Wire up keyboard notification for approval gate.
			cron.notifyKeyboardFn = func(text string, keyboard [][]tgInlineButton) {
				bot.replyWithKeyboard(bot.chatID, text, keyboard)
			}
			go bot.pollLoop(ctx)
		} else {
			logInfo("telegram disabled or no bot token, HTTP-only mode")
		}

		// Start Discord bot.
		if discordBot != nil {
			go discordBot.Run(ctx)
		}

		logInfo("tetora ready", "healthz", fmt.Sprintf("http://%s/healthz", cfg.ListenAddr))

		// Wait for shutdown signal.
		<-sigCh
		logInfo("shutting down")

		if discordBot != nil {
			discordBot.Stop()
		}

		// Cancel any running dispatch first.
		state.mu.Lock()
		if state.cancel != nil {
			state.cancel()
		}
		state.mu.Unlock()

		// Cancel global context (stops accepting new work).
		cancel()

		// Stop cron scheduler (waits for running jobs up to 30s).
		cron.stop()

		// Stop MCP host.
		if mcpHost != nil {
			mcpHost.Stop()
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
		srv := startHTTPServer(cfg.ListenAddr, state, cfg, sem, nil, nil, nil)

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
		result := dispatch(dispatchCtx, cfg, tasks, state, sem)

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
