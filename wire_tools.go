package main

// wire_tools.go constructs tool dependency structs from root globals
// and registers tools via internal/tools.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"tetora/internal/circuit"
	"tetora/internal/classify"
	"tetora/internal/cron"
	"tetora/internal/handoff"
	"tetora/internal/cost"
	"tetora/internal/db"
	dtypes "tetora/internal/dispatch"
	"tetora/internal/estimate"
	"tetora/internal/health"
	"tetora/internal/log"
	iplugin "tetora/internal/plugin"
	iproactive "tetora/internal/proactive"
	"tetora/internal/prompt"
	"tetora/internal/quiet"
	"tetora/internal/sla"
	"tetora/internal/tool"
	"tetora/internal/tools"
	"tetora/internal/webhook"
)

// buildMemoryDeps constructs MemoryDeps from root memory functions.
func buildMemoryDeps() tools.MemoryDeps {
	return tools.MemoryDeps{
		GetMemory: getMemory,
		SetMemory: func(cfg *Config, role, key, value string) error {
			return setMemory(cfg, role, key, value) // drop variadic priority
		},
		DeleteMemory: deleteMemory,
		SearchMemory: func(cfg *Config, role, query string) ([]tools.MemoryEntry, error) {
			entries, err := searchMemoryFS(cfg, role, query)
			if err != nil {
				return nil, err
			}
			result := make([]tools.MemoryEntry, len(entries))
			for i, e := range entries {
				result[i] = tools.MemoryEntry{Key: e.Key, Value: e.Value}
			}
			return result, nil
		},
	}
}

// buildImageGenDeps constructs ImageGenDeps from the global limiter.
func buildImageGenDeps() tools.ImageGenDeps {
	return tools.ImageGenDeps{
		GetLimiter: func(ctx context.Context) *tools.ImageGenLimiter {
			app := appFromCtx(ctx)
			if app == nil {
				return nil
			}
			return app.ImageGenLimiter
		},
	}
}

// buildTaskboardDeps constructs TaskboardDeps by wrapping root handler factories.
func buildTaskboardDeps(cfg *Config) tools.TaskboardDeps {
	return tools.TaskboardDeps{
		ListHandler:      toolTaskboardList(cfg),
		GetHandler:       toolTaskboardGet(cfg),
		CreateHandler:    toolTaskboardCreate(cfg),
		MoveHandler:      toolTaskboardMove(cfg),
		CommentHandler:   toolTaskboardComment(cfg),
		DecomposeHandler: toolTaskboardDecompose(cfg),
	}
}

// buildDailyDeps constructs DailyDeps from root handler functions.
func buildDailyDeps(cfg *Config) tools.DailyDeps {
	return tools.DailyDeps{
		WeatherCurrent: func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
			return tool.WeatherCurrent(ctx, cfg.Weather.Location, input)
		},
		WeatherForecast: func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
			return tool.WeatherForecast(ctx, cfg.Weather.Location, input)
		},
		CurrencyConvert: func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
			return tool.CurrencyConvert(ctx, input)
		},
		CurrencyRates: func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
			return tool.CurrencyRates(ctx, input)
		},
		RSSRead: func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
			return tool.RSSRead(ctx, cfg.RSS.Feeds, input)
		},
		RSSList: func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
			return tool.RSSList(ctx, cfg.RSS.Feeds, input)
		},
		Translate: func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
			return tool.Translate(ctx, cfg.Translate.Provider, cfg.Translate.APIKey, input)
		},
		DetectLanguage: func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
			return tool.DetectLanguage(ctx, cfg.Translate.Provider, cfg.Translate.APIKey, input)
		},
		NoteCreate:     toolNoteCreate,
		NoteRead:       toolNoteRead,
		NoteAppend:     toolNoteAppend,
		NoteList:       toolNoteList,
		NoteSearch:     toolNoteSearch,
		StoreLesson:    toolStoreLesson,
		NoteDedup:      toolNoteDedup,
		SourceAudit:    toolSourceAudit,
		WebCrawl:       toolWebCrawl,
		SourceAuditURL: toolSourceAuditURL,
		AudioNormalize: func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
			return tool.AudioNormalize(ctx, input)
		},
	}
}

// buildCoreDeps constructs CoreDeps from root handler functions.
func buildCoreDeps() tools.CoreDeps {
	return tools.CoreDeps{
		ExecHandler:    toolExec,
		ReadHandler:    toolRead,
		WriteHandler:   toolWrite,
		EditHandler:    toolEdit,
		WebSearchHandler: func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
			return tool.WebSearch(ctx, tool.WebSearchConfig{
				Provider:   cfg.Tools.WebSearch.Provider,
				APIKey:     cfg.Tools.WebSearch.APIKey,
				BaseURL:    cfg.Tools.WebSearch.BaseURL,
				MaxResults: cfg.Tools.WebSearch.MaxResults,
			}, input)
		},
		WebFetchHandler:      toolWebFetch,
		SessionListHandler:   toolSessionList,
		MessageHandler:       toolMessage,
		CronListHandler:      toolCronList,
		CronCreateHandler:    toolCronCreate,
		CronDeleteHandler:    toolCronDelete,
		AgentListHandler:     toolAgentList,
		AgentDispatchHandler: toolAgentDispatch,
		AgentMessageHandler:  toolAgentMessage,
		SearchToolsHandler:   toolSearchTools,
		ExecuteToolHandler:   toolExecuteTool,
		ImageAnalyzeHandler: func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
			return tool.ImageAnalyze(ctx, tool.VisionConfig{
				Provider:     cfg.Tools.Vision.Provider,
				APIKey:       cfg.Tools.Vision.APIKey,
				Model:        cfg.Tools.Vision.Model,
				MaxImageSize: cfg.Tools.Vision.MaxImageSize,
				BaseURL:      cfg.Tools.Vision.BaseURL,
			}, input)
		},
	}
}

// buildLifeDeps constructs LifeDeps from root handler functions.
func buildLifeDeps() tools.LifeDeps {
	return tools.LifeDeps{
		TaskCreate:       toolTaskCreate,
		TaskList:         toolTaskList,
		TaskComplete:     toolTaskComplete,
		TaskReview:       toolTaskReview,
		TodoistSync:      toolTodoistSync,
		NotionSync:       toolNotionSync,
		ExpenseAdd:       toolExpenseAdd,
		ExpenseReport:    toolExpenseReport,
		ExpenseBudget:    toolExpenseBudget,
		PriceWatch:       toolPriceWatch,
		ContactAdd:       toolContactAdd,
		ContactSearch:    toolContactSearch,
		ContactList:      toolContactList,
		ContactUpcoming:  toolContactUpcoming,
		ContactLog:       toolContactLog,
		LifeReport:       toolLifeReport,
		LifeInsights:     toolLifeInsights,
		ScheduleView:     toolScheduleView,
		ScheduleSuggest:  toolScheduleSuggest,
		SchedulePlan:     toolSchedulePlan,
		HabitCreate:      toolHabitCreate,
		HabitLog:         toolHabitLog,
		HabitStatus:      toolHabitStatus,
		HabitReport:      toolHabitReport,
		HealthLog:        toolHealthLog,
		HealthSummary:    toolHealthSummary,
		GoalCreate:       toolGoalCreate,
		GoalList:         toolGoalList,
		GoalUpdate:       toolGoalUpdate,
		GoalReview:       toolGoalReview,
		BriefingMorning:  toolBriefingMorning,
		BriefingEvening:  toolBriefingEvening,
		TimeStart:        toolTimeStart,
		TimeStop:         toolTimeStop,
		TimeLog:          toolTimeLog,
		TimeReport:       toolTimeReport,
		QuickCapture:     toolQuickCapture,
		LifecycleSync:    toolLifecycleSync,
		LifecycleSuggest: toolLifecycleSuggest,
		UserProfileGet:   toolUserProfileGet,
		UserProfileSet:   toolUserProfileSet,
		MoodCheck:        toolMoodCheck,
		FamilyListAdd:    toolFamilyListAdd,
		FamilyListView:   toolFamilyListView,
		UserSwitch:       toolUserSwitch,
		FamilyManage:     toolFamilyManage,
	}
}

// buildIntegrationDeps constructs IntegrationDeps from root handler functions.
func buildIntegrationDeps(cfg *Config) tools.IntegrationDeps {
	return tools.IntegrationDeps{
		EmailList:   toolEmailList,
		EmailRead:   toolEmailRead,
		EmailSend:   toolEmailSend,
		EmailDraft:  toolEmailDraft,
		EmailSearch: toolEmailSearch,
		EmailLabel:  toolEmailLabel,

		CalendarList:   toolCalendarList,
		CalendarCreate: toolCalendarCreate,
		CalendarUpdate: toolCalendarUpdate,
		CalendarDelete: toolCalendarDelete,
		CalendarSearch: toolCalendarSearch,

		TweetPost:         toolTweetPost,
		TweetReadTimeline: toolTweetTimeline,
		TweetSearch:       toolTweetSearch,
		TweetReply:        toolTweetReply,
		TweetDM:           toolTweetDM,

		BrowserNavigate:   toolBrowserRelay("navigate"),
		BrowserContent:    toolBrowserRelay("content"),
		BrowserClick:      toolBrowserRelay("click"),
		BrowserType:       toolBrowserRelay("type"),
		BrowserScreenshot: toolBrowserRelay("screenshot"),
		BrowserEval:       toolBrowserRelay("eval"),

		NotebookLMImport:       toolNotebookLMImport,
		NotebookLMListSources:  toolNotebookLMListSources,
		NotebookLMQuery:        toolNotebookLMQuery,
		NotebookLMDeleteSource: toolNotebookLMDeleteSource,

		HAListEntities: toolHAListEntities,
		HAGetState:     toolHAGetState,
		HACallService:  toolHACallService,
		HASetState:     toolHASetState,

		IMessageSend: func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
			var args struct {
				ChatGUID string `json:"chat_guid"`
				Text     string `json:"text"`
			}
			if err := json.Unmarshal(input, &args); err != nil {
				return "", fmt.Errorf("invalid input: %w", err)
			}
			if args.ChatGUID == "" || args.Text == "" {
				return "", fmt.Errorf("chat_guid and text are required")
			}
			app := appFromCtx(ctx)
			if app == nil || app.IMessage == nil {
				return "", fmt.Errorf("iMessage bot not initialized")
			}
			if err := app.IMessage.SendMessage(args.ChatGUID, args.Text); err != nil {
				return "", err
			}
			return fmt.Sprintf("message sent to %s", args.ChatGUID), nil
		},
		IMessageSearch: func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
			var args struct {
				Query string `json:"query"`
				Limit int    `json:"limit"`
			}
			if err := json.Unmarshal(input, &args); err != nil {
				return "", fmt.Errorf("invalid input: %w", err)
			}
			if args.Query == "" {
				return "", fmt.Errorf("query is required")
			}
			if args.Limit <= 0 {
				args.Limit = 10
			}
			app := appFromCtx(ctx)
			if app == nil || app.IMessage == nil {
				return "", fmt.Errorf("iMessage bot not initialized")
			}
			messages, err := app.IMessage.SearchMessages(args.Query, args.Limit)
			if err != nil {
				return "", err
			}
			b, _ := json.Marshal(messages)
			return string(b), nil
		},
		IMessageRead: func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
			var args struct {
				ChatGUID string `json:"chat_guid"`
				Limit    int    `json:"limit"`
			}
			if err := json.Unmarshal(input, &args); err != nil {
				return "", fmt.Errorf("invalid input: %w", err)
			}
			if args.ChatGUID == "" {
				return "", fmt.Errorf("chat_guid is required")
			}
			if args.Limit <= 0 {
				args.Limit = 20
			}
			app := appFromCtx(ctx)
			if app == nil || app.IMessage == nil {
				return "", fmt.Errorf("iMessage bot not initialized")
			}
			messages, err := app.IMessage.ReadRecentMessages(args.ChatGUID, args.Limit)
			if err != nil {
				return "", err
			}
			b, _ := json.Marshal(messages)
			return string(b), nil
		},

		RegisterDeviceTools: tools.RegisterDeviceTools,

		SpotifyPlay:       toolSpotifyPlay,
		SpotifySearch:     toolSpotifySearch,
		SpotifyNowPlaying: toolSpotifyNowPlaying,
		SpotifyDevices:    toolSpotifyDevices,
		SpotifyRecommend:  toolSpotifyRecommend,
		YouTubeSummary:    toolYouTubeSummary,
		PodcastList:       toolPodcastList,

		PdfRead:        toolPdfRead,
		DocSummarize:   toolDocSummarize,
		FileStore:      toolFileStore,
		FileList:       toolFileList,
		FileDuplicates: toolFileDuplicates,
		FileOrganize:   toolFileOrganize,
		DriveSearch:    toolDriveSearch,
		DriveUpload:    toolDriveUpload,
		DriveDownload:  toolDriveDownload,
		DropboxOp:      toolDropboxOp,

		OAuthStatus:    toolOAuthStatus,
		OAuthRequest:   toolOAuthRequest,
		OAuthAuthorize: toolOAuthAuthorize,

		ReminderSet:    toolReminderSet,
		ReminderList:   toolReminderList,
		ReminderCancel: toolReminderCancel,
	}
}

// --- Agent Memory Types ---

// MemoryEntry represents a key-value memory entry.
type MemoryEntry struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	Priority  string `json:"priority,omitempty"` // P0=permanent, P1=active(default), P2=stale
	UpdatedAt string `json:"updatedAt"`
}

// parseMemoryFrontmatter extracts priority from YAML-like frontmatter.
// Returns the priority string and the body without frontmatter.
// If no frontmatter is present, returns "P1" (default) and the full data.
func parseMemoryFrontmatter(data []byte) (priority string, body string) {
	s := string(data)
	if !strings.HasPrefix(s, "---\n") {
		return "P1", s
	}
	end := strings.Index(s[4:], "\n---\n")
	if end < 0 {
		return "P1", s
	}
	front := s[4 : 4+end]
	body = s[4+end+5:] // skip past closing "---\n"

	// Parse simple key: value pairs from frontmatter.
	priority = "P1"
	for _, line := range strings.Split(front, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "priority:") {
			val := strings.TrimSpace(strings.TrimPrefix(line, "priority:"))
			if val == "P0" || val == "P1" || val == "P2" {
				priority = val
			}
		}
	}
	return priority, body
}

// buildMemoryFrontmatter creates frontmatter + body content.
func buildMemoryFrontmatter(priority, body string) string {
	if priority == "" || priority == "P1" {
		// P1 is default — omit frontmatter for backward compatibility.
		return body
	}
	return "---\npriority: " + priority + "\n---\n" + body
}

// --- Get ---

// getMemory reads workspace/memory/{key}.md, stripping any frontmatter.
func getMemory(cfg *Config, role, key string) (string, error) {
	path := filepath.Join(cfg.WorkspaceDir, "memory", sanitizeKey(key)+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil // missing = empty, not error
	}
	_, body := parseMemoryFrontmatter(data)
	return body, nil
}

// --- Set (Write) ---

// setMemory writes workspace/memory/{key}.md, preserving existing priority if not specified.
// priority is optional — pass "" to preserve existing, or "P0"/"P1"/"P2" to set.
func setMemory(cfg *Config, role, key, value string, priority ...string) error {
	dir := filepath.Join(cfg.WorkspaceDir, "memory")
	os.MkdirAll(dir, 0o755)

	path := filepath.Join(dir, sanitizeKey(key)+".md")

	// Determine priority: explicit arg > existing frontmatter > default P1.
	pri := ""
	if len(priority) > 0 && priority[0] != "" {
		pri = priority[0]
	} else {
		// Preserve existing priority if file exists.
		if existing, err := os.ReadFile(path); err == nil {
			pri, _ = parseMemoryFrontmatter(existing)
		}
	}

	content := buildMemoryFrontmatter(pri, value)
	return os.WriteFile(path, []byte(content), 0o644)
}

// --- List ---

// listMemory lists all memory files, parsing priority from frontmatter.
func listMemory(cfg *Config, role string) ([]MemoryEntry, error) {
	dir := filepath.Join(cfg.WorkspaceDir, "memory")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var result []MemoryEntry
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		key := strings.TrimSuffix(e.Name(), ".md")
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		priority, body := parseMemoryFrontmatter(data)
		info, _ := e.Info()
		updatedAt := ""
		if info != nil {
			updatedAt = info.ModTime().Format(time.RFC3339)
		}
		result = append(result, MemoryEntry{
			Key:       key,
			Value:     body,
			Priority:  priority,
			UpdatedAt: updatedAt,
		})
	}
	return result, nil
}

// --- Delete ---

// deleteMemory removes workspace/memory/{key}.md
func deleteMemory(cfg *Config, role, key string) error {
	path := filepath.Join(cfg.WorkspaceDir, "memory", sanitizeKey(key)+".md")
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// --- Search ---

// searchMemory searches memory files by content.
func searchMemoryFS(cfg *Config, role, query string) ([]MemoryEntry, error) {
	all, err := listMemory(cfg, role)
	if err != nil {
		return nil, err
	}
	query = strings.ToLower(query)
	var results []MemoryEntry
	for _, e := range all {
		if strings.Contains(strings.ToLower(e.Key), query) ||
			strings.Contains(strings.ToLower(e.Value), query) {
			results = append(results, e)
		}
	}
	return results, nil
}

// sanitizeKey sanitizes a memory key for use as a filename.
func sanitizeKey(key string) string {
	// Replace path separators and other unsafe chars.
	r := strings.NewReplacer("/", "_", "\\", "_", "..", "_", "\x00", "")
	return r.Replace(key)
}

// --- Access Tracking ---

// recordMemoryAccess updates the last-access timestamp for a memory key.
func recordMemoryAccess(cfg *Config, key string) {
	if cfg == nil || cfg.WorkspaceDir == "" {
		return
	}
	accessLog := loadMemoryAccessLog(cfg)
	accessLog[sanitizeKey(key)] = time.Now().UTC().Format(time.RFC3339)
	saveMemoryAccessLog(cfg, accessLog)
}

// loadMemoryAccessLog reads workspace/memory/.access.json.
func loadMemoryAccessLog(cfg *Config) map[string]string {
	result := make(map[string]string)
	if cfg == nil || cfg.WorkspaceDir == "" {
		return result
	}
	path := filepath.Join(cfg.WorkspaceDir, "memory", ".access.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return result
	}
	json.Unmarshal(data, &result)
	return result
}

// saveMemoryAccessLog writes workspace/memory/.access.json.
func saveMemoryAccessLog(cfg *Config, accessLog map[string]string) {
	if cfg == nil || cfg.WorkspaceDir == "" {
		return
	}
	dir := filepath.Join(cfg.WorkspaceDir, "memory")
	os.MkdirAll(dir, 0o755)
	data, err := json.MarshalIndent(accessLog, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(filepath.Join(dir, ".access.json"), data, 0o644)
}

// initMemoryDB is a no-op kept for backward compatibility.
func initMemoryDB(dbPath string) error {
	return nil
}

// ============================================================
// Merged shims: slot_pressure, prompt_tier
// ============================================================

// --- Slot Pressure (from slot_pressure.go) ---

type SlotPressureGuard = dtypes.SlotPressureGuard
type AcquireResult = dtypes.AcquireResult

func isInteractiveSource(source string) bool { return dtypes.IsInteractiveSource(source) }

// --- Prompt Tier (from prompt_tier.go) ---

func buildTieredPrompt(cfg *Config, task *Task, agentName string, complexity classify.Complexity) {
	prompt.BuildTieredPrompt(cfg, task, agentName, complexity, prompt.Deps{
		ResolveProviderName:    resolveProviderName,
		LoadSoulFile:           loadSoulFile,
		LoadAgentPrompt:        loadAgentPrompt,
		ResolveWorkspace:       resolveWorkspace,
		BuildReflectionContext: buildReflectionContext,
		LoadWritingStyle:       loadWritingStyle,
		BuildSkillsPrompt:      buildSkillsPrompt,
		InjectWorkspaceContent: injectWorkspaceContent,
		EstimateDirSize:        estimateDirSize,
	})
}

func truncateToChars(s string, maxChars int) string {
	return prompt.TruncateToChars(s, maxChars)
}

func truncateLessonsToRecent(content string, n int) string {
	return prompt.TruncateLessonsToRecent(content, n)
}

// ============================================================
// Merged shim: proactive
// ============================================================

type ProactiveEngine = iproactive.Engine
type ProactiveRuleInfo = iproactive.RuleInfo

func newProactiveEngine(cfg *Config, broker *sseBroker, sem, childSem chan struct{}) *ProactiveEngine {
	deps := iproactive.Deps{
		RunTask: func(ctx context.Context, task Task, sem, childSem chan struct{}, agentName string) TaskResult {
			return runSingleTask(ctx, cfg, task, sem, childSem, agentName)
		},
		RecordHistory: func(dbPath string, task Task, result TaskResult, agentName, startedAt, finishedAt, outputFile string) {
			recordHistory(dbPath, task.ID, task.Name, task.Source, agentName, task, result, startedAt, finishedAt, outputFile)
		},
		FillDefaults: func(c *Config, t *Task) {
			fillDefaults(c, t)
		},
	}
	return iproactive.New(cfg, broker, sem, childSem, deps)
}

func runProactive(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: tetora proactive <subcommand>")
		fmt.Println()
		fmt.Println("Subcommands:")
		fmt.Println("  list          List all proactive rules")
		fmt.Println("  trigger <name> Manually trigger a rule")
		fmt.Println("  status        Show engine status")
		return
	}

	cfg := loadConfig("")

	switch args[0] {
	case "list":
		iproactive.CmdList(cfg)
	case "trigger":
		if len(args) < 2 {
			fmt.Println("Usage: tetora proactive trigger <rule-name>")
			return
		}
		iproactive.CmdTrigger(cfg, args[1])
	case "status":
		iproactive.CmdStatus(cfg)
	default:
		fmt.Printf("Unknown subcommand: %s\n", args[0])
	}
}

func cmdProactiveTrigger(cfg *Config, ruleName string) {
	apiURL := fmt.Sprintf("http://%s/api/proactive/rules/%s/trigger", cfg.ListenAddr, ruleName)

	req, err := http.NewRequest("POST", apiURL, nil)
	if err != nil {
		fmt.Printf("Error creating request: %v\n", err)
		return
	}

	if cfg.APIToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIToken)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		fmt.Printf("Rule %q triggered successfully.\n", ruleName)
		return
	}
	fmt.Printf("Error: HTTP %d\n", resp.StatusCode)
}

// ============================================================
// Merged from sse.go
// ============================================================

// --- SSE Event Types (aliases to internal/dispatch) ---

type SSEEvent = dtypes.SSEEvent

const (
	SSEStarted           = dtypes.SSEStarted
	SSEProgress          = dtypes.SSEProgress
	SSEOutputChunk       = dtypes.SSEOutputChunk
	SSECompleted         = dtypes.SSECompleted
	SSEError             = dtypes.SSEError
	SSEHeartbeat         = dtypes.SSEHeartbeat
	SSEQueued            = dtypes.SSEQueued
	SSETaskReceived      = dtypes.SSETaskReceived
	SSETaskRouting       = dtypes.SSETaskRouting
	SSEDiscordProcessing = dtypes.SSEDiscordProcessing
	SSEDiscordReplying   = dtypes.SSEDiscordReplying
	SSEDashboardKey      = dtypes.SSEDashboardKey
	SSEToolCall          = dtypes.SSEToolCall
	SSEToolResult        = dtypes.SSEToolResult
	SSESessionMessage    = dtypes.SSESessionMessage
	SSEAgentState        = dtypes.SSEAgentState
	SSEHeartbeatAlert    = dtypes.SSEHeartbeatAlert
	SSETaskStalled       = dtypes.SSETaskStalled
	SSETaskRecovered     = dtypes.SSETaskRecovered
	SSEWorkerUpdate      = dtypes.SSEWorkerUpdate
	SSEHookEvent         = dtypes.SSEHookEvent
	SSEPlanReview        = dtypes.SSEPlanReview
)

type sseBroker = dtypes.Broker

func newSSEBroker() *sseBroker {
	return dtypes.NewBroker()
}

func serveSSE(w http.ResponseWriter, r *http.Request, broker *sseBroker, key string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":"streaming not supported"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch, unsub := broker.Subscribe(key)
	defer unsub()

	var eventID atomic.Int64

	fmt.Fprintf(w, ": connected to %s\n\n", key)
	flusher.Flush()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return

		case event, ok := <-ch:
			if !ok {
				return
			}
			id := eventID.Add(1)
			writeSSEEvent(w, id, event)
			flusher.Flush()

			if event.Type == SSECompleted || event.Type == SSEError {
				return
			}

		case <-heartbeat.C:
			fmt.Fprintf(w, ": heartbeat %s\n\n", time.Now().Format(time.RFC3339))
			flusher.Flush()
		}
	}
}

func serveDashboardSSE(w http.ResponseWriter, r *http.Request, broker *sseBroker) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":"streaming not supported"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch, unsub := broker.Subscribe(SSEDashboardKey)
	defer unsub()

	var eventID atomic.Int64

	fmt.Fprintf(w, ": connected to dashboard\n\n")
	flusher.Flush()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return

		case event, ok := <-ch:
			if !ok {
				return
			}
			id := eventID.Add(1)
			writeSSEEvent(w, id, event)
			flusher.Flush()

		case <-heartbeat.C:
			fmt.Fprintf(w, ": heartbeat %s\n\n", time.Now().Format(time.RFC3339))
			flusher.Flush()
		}
	}
}

func serveSSEPersistent(w http.ResponseWriter, r *http.Request, broker *sseBroker, key string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":"streaming not supported"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch, unsub := broker.Subscribe(key)
	defer unsub()

	var eventID atomic.Int64

	fmt.Fprintf(w, ": connected to %s\n\n", key)
	flusher.Flush()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-ch:
			if !ok {
				return
			}
			id := eventID.Add(1)
			writeSSEEvent(w, id, event)
			flusher.Flush()
		case <-heartbeat.C:
			fmt.Fprintf(w, ": heartbeat %s\n\n", time.Now().Format(time.RFC3339))
			flusher.Flush()
		}
	}
}

func writeSSEEvent(w http.ResponseWriter, id int64, event SSEEvent) {
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", id, event.Type, string(data))
}

// ============================================================
// Merged from agent_comm.go
// ============================================================

type spawnTracker = dtypes.SpawnTracker

var globalSpawnTracker = dtypes.NewSpawnTracker()

func newSpawnTracker() *spawnTracker { return dtypes.NewSpawnTracker() }

func childSemConcurrentOrDefault(cfg *Config) int {
	return dtypes.ChildSemConcurrentOrDefault(cfg)
}

func maxDepthOrDefault(cfg *Config) int {
	return dtypes.MaxDepthOrDefault(cfg)
}

func maxChildrenPerTaskOrDefault(cfg *Config) int {
	return dtypes.MaxChildrenPerTaskOrDefault(cfg)
}

func toolAgentList(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	return dtypes.ToolAgentList(ctx, cfg, input)
}

func toolAgentMessage(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	return dtypes.ToolAgentMessage(ctx, cfg, input)
}

func generateMessageID() string {
	return dtypes.GenerateMessageID()
}

func initAgentCommDB(dbPath string) error {
	return dtypes.InitAgentCommDB(dbPath)
}

func getAgentMessages(dbPath, role string, markAsRead bool) ([]map[string]any, error) {
	return dtypes.GetAgentMessages(dbPath, role, markAsRead)
}

func toolAgentDispatch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Agent    string  `json:"agent"`
		Role     string  `json:"role"`
		Prompt   string  `json:"prompt"`
		Timeout  float64 `json:"timeout"`
		Depth    int     `json:"depth"`
		ParentID string  `json:"parentId"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Agent == "" {
		args.Agent = args.Role
	}
	if args.Agent == "" {
		return "", fmt.Errorf("agent is required")
	}
	if args.Prompt == "" {
		return "", fmt.Errorf("prompt is required")
	}
	if args.Timeout <= 0 {
		if cfg.AgentComm.DefaultTimeout > 0 {
			args.Timeout = float64(cfg.AgentComm.DefaultTimeout)
		} else {
			estimated, err := time.ParseDuration(estimateTimeout(args.Prompt))
			if err != nil {
				estimated = time.Hour
			}
			args.Timeout = estimated.Seconds()
		}
	}

	childDepth := args.Depth + 1
	maxDepth := maxDepthOrDefault(cfg)
	if args.Depth >= maxDepth {
		return "", fmt.Errorf("max nesting depth exceeded: current depth %d >= maxDepth %d", args.Depth, maxDepth)
	}

	app := appFromCtx(ctx)
	maxChildren := maxChildrenPerTaskOrDefault(cfg)
	if args.ParentID != "" {
		tracker := globalSpawnTracker
		if app != nil && app.SpawnTracker != nil {
			tracker = app.SpawnTracker
		}
		if !tracker.TrySpawn(args.ParentID, maxChildren) {
			return "", fmt.Errorf("max children per task exceeded: parent %s already has %d active children (limit %d)",
				args.ParentID, tracker.Count(args.ParentID), maxChildren)
		}
		defer tracker.Release(args.ParentID)
	}

	if _, ok := cfg.Agents[args.Agent]; !ok {
		return "", fmt.Errorf("agent %q not found", args.Agent)
	}

	task := Task{
		Prompt:   args.Prompt,
		Agent:    args.Agent,
		Timeout:  fmt.Sprintf("%.0fs", args.Timeout),
		Source:   "agent_dispatch",
		Depth:    childDepth,
		ParentID: args.ParentID,
	}
	fillDefaults(cfg, &task)

	log.Debug("agent_dispatch", "agent", args.Agent, "depth", childDepth, "parentId", args.ParentID)

	requestBody, _ := json.Marshal([]Task{task})

	addr := cfg.ListenAddr
	if addr == "" {
		addr = "127.0.0.1:7777"
	}

	apiURL := fmt.Sprintf("http://%s/dispatch", addr)
	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(requestBody))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tetora-Source", "agent_dispatch")

	if cfg.APIToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIToken)
	}

	client := &http.Client{
		Timeout: time.Duration(args.Timeout+10) * time.Second,
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("dispatch request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("dispatch failed: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var dispatchResult DispatchResult
	if err := json.Unmarshal(body, &dispatchResult); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	if len(dispatchResult.Tasks) == 0 {
		return "", fmt.Errorf("no task result returned")
	}

	taskResult := dispatchResult.Tasks[0]

	result := map[string]any{
		"role":       args.Agent,
		"status":     taskResult.Status,
		"output":     taskResult.Output,
		"durationMs": taskResult.DurationMs,
		"costUsd":    taskResult.CostUSD,
	}
	if taskResult.Error != "" {
		result["error"] = taskResult.Error
	}

	b, _ := json.Marshal(result)
	return string(b), nil
}

// ============================================================
// Merged from plugin.go
// ============================================================

type PluginHost = iplugin.Host

func NewPluginHost(cfg *Config) *PluginHost {
	return iplugin.NewHost(cfg, &pluginToolRegistrar{cfg: cfg})
}

type pluginToolRegistrar struct {
	cfg *Config
}

func (r *pluginToolRegistrar) RegisterPluginTool(toolName, pluginName string, call func(method string, params any) (json.RawMessage, error)) {
	if r.cfg.Runtime.ToolRegistry == nil {
		return
	}
	r.cfg.Runtime.ToolRegistry.(*ToolRegistry).Register(&ToolDef{
		Name:        toolName,
		Description: fmt.Sprintf("Plugin tool (%s) provided by plugin %q", toolName, pluginName),
		InputSchema: json.RawMessage(`{"type": "object", "properties": {"input": {"type": "object", "description": "Tool input"}}, "required": []}`),
		Handler: func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
			result, err := call("tool/execute", map[string]any{
				"name":  toolName,
				"input": json.RawMessage(input),
			})
			if err != nil {
				return "", err
			}
			return string(result), nil
		},
		Builtin: false,
	})
}

var codeModeCoreTools = map[string]bool{
	"exec":           true,
	"read":           true,
	"write":          true,
	"web_search":     true,
	"web_fetch":      true,
	"memory_search":  true,
	"agent_dispatch": true,
	"search_tools":   true,
	"execute_tool":   true,
}

const codeModeTotalThreshold = 10

func shouldUseCodeMode(registry *ToolRegistry) bool {
	if registry == nil {
		return false
	}
	return len(registry.List()) > codeModeTotalThreshold
}

func toolSearchTools(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Query == "" {
		return "", fmt.Errorf("query is required")
	}
	if args.Limit <= 0 {
		args.Limit = 10
	}

	if cfg.Runtime.ToolRegistry == nil {
		return "[]", nil
	}

	query := strings.ToLower(args.Query)
	var results []map[string]string

	for _, t := range cfg.Runtime.ToolRegistry.(*ToolRegistry).List() {
		nameMatch := strings.Contains(strings.ToLower(t.Name), query)
		descMatch := strings.Contains(strings.ToLower(t.Description), query)
		if nameMatch || descMatch {
			results = append(results, map[string]string{
				"name":        t.Name,
				"description": t.Description,
			})
			if len(results) >= args.Limit {
				break
			}
		}
	}

	b, _ := json.Marshal(results)
	return string(b), nil
}

func toolExecuteTool(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Name == "" {
		return "", fmt.Errorf("name is required")
	}

	if cfg.Runtime.ToolRegistry == nil {
		return "", fmt.Errorf("tool registry not initialized")
	}

	t, ok := cfg.Runtime.ToolRegistry.(*ToolRegistry).Get(args.Name)
	if !ok {
		return "", fmt.Errorf("tool %q not found", args.Name)
	}

	if t.Handler == nil {
		return "", fmt.Errorf("tool %q has no handler", args.Name)
	}

	return t.Handler(ctx, cfg, args.Input)
}

func cmdPlugin(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: tetora plugin <list|start|stop> [name]")
		fmt.Println()
		fmt.Println("Manage external plugins.")
		fmt.Println()
		fmt.Println("Commands:")
		fmt.Println("  list          List configured plugins and their status")
		fmt.Println("  start <name>  Start a plugin")
		fmt.Println("  stop <name>   Stop a running plugin")
		return
	}

	cfg := loadConfig("")

	switch args[0] {
	case "list":
		if len(cfg.Plugins) == 0 {
			fmt.Println("No plugins configured.")
			return
		}
		fmt.Printf("%-20s %-10s %-10s %-30s %s\n", "NAME", "TYPE", "AUTOSTART", "COMMAND", "TOOLS")
		for name, pcfg := range cfg.Plugins {
			toolsList := "-"
			if len(pcfg.Tools) > 0 {
				toolsList = strings.Join(pcfg.Tools, ", ")
			}
			autoStart := "no"
			if pcfg.AutoStart {
				autoStart = "yes"
			}
			fmt.Printf("%-20s %-10s %-10s %-30s %s\n", name, pcfg.Type, autoStart, pcfg.Command, toolsList)
		}

	case "start":
		if len(args) < 2 {
			fmt.Println("Usage: tetora plugin start <name>")
			return
		}
		name := args[1]
		pcfg, ok := cfg.Plugins[name]
		if !ok {
			fmt.Printf("Plugin %q not found in config.\n", name)
			return
		}
		fmt.Printf("Starting plugin %q (type=%s, command=%s)...\n", name, pcfg.Type, pcfg.Command)
		fmt.Println("Note: plugins are managed by the daemon. Use the HTTP API to start plugins at runtime.")

	case "stop":
		if len(args) < 2 {
			fmt.Println("Usage: tetora plugin stop <name>")
			return
		}
		name := args[1]
		if _, ok := cfg.Plugins[name]; !ok {
			fmt.Printf("Plugin %q not found in config.\n", name)
			return
		}
		fmt.Printf("Note: plugins are managed by the daemon. Use the HTTP API to stop plugins at runtime.\n")

	default:
		fmt.Printf("Unknown plugin command: %s\n", args[0])
		fmt.Println("Use: tetora plugin list|start|stop")
	}
}

// ============================================================
// Merged from health.go
// ============================================================

type slaChecker struct {
	cfg     *Config
	inner   *sla.Checker
	lastRun time.Time
}

func newSLAChecker(cfg *Config, notifyFn func(string)) *slaChecker {
	return &slaChecker{
		cfg:   cfg,
		inner: sla.NewChecker(cfg.HistoryDB, cfg.SLA, notifyFn),
	}
}

func (s *slaChecker) tick(ctx context.Context) {
	if !s.cfg.SLA.Enabled {
		return
	}
	s.inner.Tick(ctx)
	s.lastRun = s.inner.LastRun()
}

func deepHealthCheck(cfg *Config, state *dispatchState, cron *CronEngine, startTime time.Time) map[string]any {
	input := health.CheckInput{
		Version:      tetoraVersion,
		StartTime:    startTime,
		BaseDir:      cfg.BaseDir,
		DiskBlockMB:  cfg.DiskBlockMB,
		DiskWarnMB:   cfg.DiskWarnMB,
		DiskBudgetGB: cfg.DiskBudgetGB,
	}

	if cfg.HistoryDB != "" {
		input.DBCheck = func() (int, error) {
			rows, err := db.Query(cfg.HistoryDB, "SELECT count(*) as cnt FROM job_runs;")
			if err != nil {
				return 0, err
			}
			count := 0
			if len(rows) > 0 {
				if v, ok := rows[0]["cnt"]; ok {
					fmt.Sscanf(fmt.Sprint(v), "%d", &count)
				}
			}
			return count, nil
		}
		input.DBPath = cfg.HistoryDB
	}

	providers := map[string]health.ProviderInfo{}
	if cfg.Runtime.ProviderRegistry != nil {
		for name := range cfg.Providers {
			pi := health.ProviderInfo{
				Type:   cfg.Providers[name].Type,
				Status: "ok",
			}
			if cfg.Runtime.CircuitRegistry != nil {
				cb := cfg.Runtime.CircuitRegistry.(*circuit.Registry).Get(name)
				st := cb.State()
				pi.Circuit = st.String()
				if st == circuit.Open {
					pi.Status = "open"
				} else if st == circuit.HalfOpen {
					pi.Status = "recovering"
				}
			}
			providers[name] = pi
		}
		if _, exists := providers["claude"]; !exists {
			pi := health.ProviderInfo{Type: "claude-cli", Status: "ok"}
			if cfg.Runtime.CircuitRegistry != nil {
				cb := cfg.Runtime.CircuitRegistry.(*circuit.Registry).Get("claude")
				st := cb.State()
				pi.Circuit = st.String()
				if st == circuit.Open {
					pi.Status = "open"
				}
			}
			providers["claude"] = pi
		}
	}
	input.Providers = providers

	input.DispatchJSON = state.statusJSON()

	if cron != nil {
		jobs := cron.ListJobs()
		running := 0
		enabled := 0
		for _, j := range jobs {
			if j.Running {
				running++
			}
			if j.Enabled {
				enabled++
			}
		}
		input.Cron = &health.CronSummary{Total: len(jobs), Enabled: enabled, Running: running}
	}

	if cfg.Runtime.CircuitRegistry != nil {
		input.CircuitStatus = cfg.Runtime.CircuitRegistry.(*circuit.Registry).Status()
	}

	if cfg.OfflineQueue.Enabled && cfg.HistoryDB != "" {
		input.Queue = &health.QueueInfo{
			Pending: countPendingQueue(cfg.HistoryDB),
			Max:     cfg.OfflineQueue.MaxItemsOrDefault(),
		}
	}

	return health.DeepCheck(input)
}

func degradeStatus(current, proposed string) string {
	return health.DegradeStatus(current, proposed)
}

func diskInfo(path string) map[string]any {
	return health.DiskInfo(path)
}

func diskFreeBytes(path string) uint64 {
	return health.DiskFreeBytes(path)
}

// ============================================================
// Merged from cost.go
// ============================================================

type GlobalBudget = cost.GlobalBudget
type AgentBudget = cost.AgentBudget
type WorkflowBudget = cost.WorkflowBudget
type DowngradeThreshold = cost.DowngradeThreshold
type BudgetCheckResult = cost.BudgetCheckResult
type BudgetStatus = cost.BudgetStatus
type BudgetMeter = cost.BudgetMeter
type AgentBudgetMeter = cost.AgentBudgetMeter
type budgetAlertTracker = cost.BudgetAlertTracker

func newBudgetAlertTracker() *budgetAlertTracker { return cost.NewBudgetAlertTracker() }

func querySpend(dbPath, role string) (daily, weekly, monthly float64) {
	return cost.QuerySpend(dbPath, role)
}

func queryWorkflowRunSpend(dbPath string, runID int) float64 {
	return cost.QueryWorkflowRunSpend(dbPath, runID)
}

func checkBudget(cfg *Config, agentName, workflowName string, workflowRunID int) *BudgetCheckResult {
	return cost.CheckBudget(cfg.Budgets, cfg.HistoryDB, agentName, workflowName, workflowRunID)
}

func resolveDowngradeModel(ad AutoDowngradeConfig, utilization float64) string {
	return cost.ResolveDowngradeModel(ad, utilization)
}

func queryBudgetStatus(cfg *Config) *BudgetStatus {
	return cost.QueryBudgetStatus(cfg.Budgets, cfg.HistoryDB)
}

func checkAndNotifyBudgetAlerts(cfg *Config, notifyFn func(string), tracker *budgetAlertTracker) {
	cost.CheckAndNotifyBudgetAlerts(cfg.Budgets, cfg.HistoryDB, notifyFn, tracker)
}

func checkPeriodAlert(notifyFn func(string), tracker *budgetAlertTracker, scope, period string, spend, limit float64) {
	cost.CheckPeriodAlert(notifyFn, tracker, scope, period, spend, limit)
}

func formatBudgetSummary(cfg *Config) string {
	return cost.FormatBudgetSummary(queryBudgetStatus(cfg))
}

func setBudgetPaused(configPath string, paused bool) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	var budgets map[string]json.RawMessage
	if budgetsRaw, ok := raw["budgets"]; ok {
		json.Unmarshal(budgetsRaw, &budgets)
	}
	if budgets == nil {
		budgets = make(map[string]json.RawMessage)
	}

	pausedJSON, _ := json.Marshal(paused)
	budgets["paused"] = pausedJSON

	budgetsJSON, err := json.Marshal(budgets)
	if err != nil {
		return err
	}
	raw["budgets"] = budgetsJSON

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, append(out, '\n'), 0o644)
}

type CostEstimate = estimate.CostEstimate
type EstimateResult = estimate.EstimateResult

func estimateRequestTokens(req ProviderRequest) int {
	total := len(req.Prompt)/4 + len(req.SystemPrompt)/4
	for _, m := range req.Messages {
		total += len(m.Content) / 4
	}
	for _, t := range req.Tools {
		total += (len(t.Name) + len(t.Description) + len(string(t.InputSchema))) / 4
	}
	if total < 10 {
		total = 10
	}
	return total
}

func compressMessages(messages []Message, keepRecent int) []Message {
	keepMsgs := keepRecent * 2
	if len(messages) <= keepMsgs {
		return messages
	}

	result := make([]Message, len(messages))
	compressEnd := len(messages) - keepMsgs

	for i, msg := range messages {
		if i < compressEnd && len(msg.Content) > 256 {
			summary := fmt.Sprintf(`[{"type":"text","text":"[prior tool exchange, %d bytes compressed]"}]`, len(msg.Content))
			result[i] = Message{Role: msg.Role, Content: json.RawMessage(summary)}
		} else {
			result[i] = msg
		}
	}
	return result
}

func estimateTaskCost(cfg *Config, task Task, agentName string) CostEstimate {
	providerName := resolveProviderName(cfg, task, agentName)

	model := task.Model
	if model == "" {
		if pc, ok := cfg.Providers[providerName]; ok && pc.Model != "" {
			model = pc.Model
		}
	}
	if model == "" {
		model = cfg.DefaultModel
	}

	if agentName != "" {
		if rc, ok := cfg.Agents[agentName]; ok && rc.Model != "" {
			if task.Model == "" || task.Model == cfg.DefaultModel {
				model = rc.Model
			}
		}
	}

	tokensIn := estimate.InputTokens(task.Prompt, task.SystemPrompt)

	tokensOut := estimate.QueryModelAvgOutput(cfg.HistoryDB, model)
	if tokensOut == 0 {
		tokensOut = cfg.Estimate.DefaultOutputTokensOrDefault()
	}

	pricing := estimate.ResolvePricing(cfg.Pricing, model)

	costUSD := float64(tokensIn)*pricing.InputPer1M/1_000_000 +
		float64(tokensOut)*pricing.OutputPer1M/1_000_000

	return CostEstimate{
		Name:               task.Name,
		Provider:           providerName,
		Model:              model,
		EstimatedCostUSD:   costUSD,
		EstimatedTokensIn:  tokensIn,
		EstimatedTokensOut: tokensOut,
		Breakdown: fmt.Sprintf("~%d in + ~%d out @ $%.2f/$%.2f per 1M",
			tokensIn, tokensOut, pricing.InputPer1M, pricing.OutputPer1M),
	}
}

func estimateTasks(cfg *Config, tasks []Task) *EstimateResult {
	result := &EstimateResult{}

	for _, task := range tasks {
		fillDefaults(cfg, &task)
		agentName := task.Agent

		if agentName == "" && cfg.SmartDispatch.Enabled {
			classifyModel := cfg.DefaultModel
			if rc, ok := cfg.Agents[cfg.SmartDispatch.Coordinator]; ok && rc.Model != "" {
				classifyModel = rc.Model
			}
			classifyPricing := estimate.ResolvePricing(cfg.Pricing, classifyModel)
			classifyCost := float64(500)*classifyPricing.InputPer1M/1_000_000 +
				float64(50)*classifyPricing.OutputPer1M/1_000_000
			result.ClassifyCost += classifyCost

			if kr := classifyByKeywords(cfg, task.Prompt); kr != nil {
				agentName = kr.Agent
			} else {
				agentName = cfg.SmartDispatch.DefaultAgent
			}
		}

		est := estimateTaskCost(cfg, task, agentName)
		result.Tasks = append(result.Tasks, est)
		result.TotalEstimatedCost += est.EstimatedCostUSD
	}

	result.TotalEstimatedCost += result.ClassifyCost
	return result
}

// ============================================================
// Merged from queue.go
// ============================================================

type QueueItem = dtypes.QueueItem

const maxQueueRetries = dtypes.MaxQueueRetries

func initQueueDB(dbPath string) error {
	return dtypes.InitQueueDB(dbPath)
}

func enqueueTask(dbPath string, task Task, agentName string, priority int) error {
	taskBytes, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("marshal task: %w", err)
	}
	return dtypes.EnqueueTask(dbPath, string(taskBytes), task.Source, agentName, priority)
}

func dequeueNext(dbPath string) *QueueItem {
	return dtypes.DequeueNext(dbPath)
}

func queryQueue(dbPath, status string) []QueueItem {
	return dtypes.QueryQueue(dbPath, status)
}

func queryQueueItem(dbPath string, id int) *QueueItem {
	return dtypes.QueryQueueItem(dbPath, id)
}

func updateQueueStatus(dbPath string, id int, status, errMsg string) {
	dtypes.UpdateQueueStatus(dbPath, id, status, errMsg)
}

func incrementQueueRetry(dbPath string, id int, status, errMsg string) {
	dtypes.IncrementQueueRetry(dbPath, id, status, errMsg)
}

func deleteQueueItem(dbPath string, id int) error {
	return dtypes.DeleteQueueItem(dbPath, id)
}

func cleanupExpiredQueue(dbPath string, ttl time.Duration) int {
	return dtypes.CleanupExpiredQueue(dbPath, ttl)
}

func cleanupOldQueueItems(dbPath string, days int) {
	dtypes.CleanupOldQueueItems(dbPath, days)
}

func countPendingQueue(dbPath string) int {
	return dtypes.CountPendingQueue(dbPath)
}

func isQueueFull(dbPath string, maxItems int) bool {
	return dtypes.IsQueueFull(dbPath, maxItems)
}

func isAllProvidersUnavailable(errMsg string) bool {
	return dtypes.IsAllProvidersUnavailable(errMsg)
}

// queueDrainer processes offline queue items when providers recover.
type queueDrainer struct {
	cfg      *Config
	sem      chan struct{}
	childSem chan struct{}
	state    *dispatchState
	notifyFn func(string)
	ttl      time.Duration
}

func (d *queueDrainer) anyProviderAvailable() bool {
	if d.cfg.Runtime.CircuitRegistry == nil {
		return true
	}
	for name := range d.cfg.Providers {
		cb := d.cfg.Runtime.CircuitRegistry.(*circuit.Registry).Get(name)
		if cb.State() != circuit.Open {
			return true
		}
	}
	return false
}

func (d *queueDrainer) run(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	log.Info("queue drainer started", "ttl", d.ttl.String())

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.tick(ctx)
		}
	}
}

func (d *queueDrainer) tick(ctx context.Context) {
	dbPath := d.cfg.HistoryDB
	if dbPath == "" {
		return
	}

	expired := cleanupExpiredQueue(dbPath, d.ttl)
	if expired > 0 {
		log.Warn("queue items expired", "count", expired)
		if d.notifyFn != nil {
			d.notifyFn(fmt.Sprintf("Offline queue: %d item(s) expired (TTL %s)", expired, d.ttl.String()))
		}
	}

	if !d.anyProviderAvailable() {
		return
	}

	for {
		if ctx.Err() != nil {
			return
		}

		item := dequeueNext(dbPath)
		if item == nil {
			return
		}

		d.processItem(ctx, item)
	}
}

func (d *queueDrainer) processItem(ctx context.Context, item *QueueItem) {
	var task Task
	if err := json.Unmarshal([]byte(item.TaskJSON), &task); err != nil {
		log.Error("queue: bad task JSON", "id", item.ID, "error", err)
		updateQueueStatus(d.cfg.HistoryDB, item.ID, "failed", "invalid task JSON: "+err.Error())
		return
	}

	task.ID = newUUID()
	task.SessionID = newUUID()
	task.Source = "queue:" + task.Source

	log.InfoCtx(ctx, "queue: retrying task", "queueId", item.ID, "taskId", task.ID[:8], "name", task.Name, "retry", item.RetryCount+1)

	result := runSingleTask(ctx, d.cfg, task, d.sem, d.childSem, item.AgentName)

	if result.Status == "success" {
		updateQueueStatus(d.cfg.HistoryDB, item.ID, "completed", "")
		log.InfoCtx(ctx, "queue: task succeeded", "queueId", item.ID, "taskId", task.ID[:8])

		start := time.Now().Add(-time.Duration(result.DurationMs) * time.Millisecond)
		recordHistory(d.cfg.HistoryDB, task.ID, task.Name, task.Source, item.AgentName, task, result,
			start.Format(time.RFC3339), time.Now().Format(time.RFC3339), result.OutputFile)
		recordSessionActivity(d.cfg.HistoryDB, task, result, item.AgentName)

		if d.notifyFn != nil {
			d.notifyFn(fmt.Sprintf("Offline queue: task %q completed successfully (retry #%d)", task.Name, item.RetryCount+1))
		}
	} else if isAllProvidersUnavailable(result.Error) {
		if item.RetryCount+1 >= maxQueueRetries {
			incrementQueueRetry(d.cfg.HistoryDB, item.ID, "failed", result.Error)
			log.WarnCtx(ctx, "queue: task failed after max retries", "queueId", item.ID, "retries", maxQueueRetries)
			if d.notifyFn != nil {
				d.notifyFn(fmt.Sprintf("Offline queue: task %q failed after %d retries: %s",
					task.Name, maxQueueRetries, truncate(result.Error, 200)))
			}
		} else {
			incrementQueueRetry(d.cfg.HistoryDB, item.ID, "pending", result.Error)
			log.InfoCtx(ctx, "queue: task still unavailable, re-queued", "queueId", item.ID, "retry", item.RetryCount+1)
		}
	} else {
		incrementQueueRetry(d.cfg.HistoryDB, item.ID, "failed", result.Error)
		log.WarnCtx(ctx, "queue: task failed with non-provider error", "queueId", item.ID, "error", result.Error)

		start := time.Now().Add(-time.Duration(result.DurationMs) * time.Millisecond)
		recordHistory(d.cfg.HistoryDB, task.ID, task.Name, task.Source, item.AgentName, task, result,
			start.Format(time.RFC3339), time.Now().Format(time.RFC3339), result.OutputFile)
	}
}

// ============================================================
// Merged from handoff.go
// ============================================================

// --- Type aliases ---

type Handoff = handoff.Handoff
type AgentMessage = handoff.AgentMessage
type AutoDelegation = handoff.AutoDelegation

const maxAutoDelegations = handoff.MaxAutoDelegations

// --- Delegating functions ---

func initHandoffTables(dbPath string)                       { handoff.InitTables(dbPath) }
func recordHandoff(dbPath string, h Handoff) error          { return handoff.RecordHandoff(dbPath, h) }
func updateHandoffStatus(dbPath, id, status string) error   { return handoff.UpdateStatus(dbPath, id, status) }
func queryHandoffs(dbPath, wfID string) ([]Handoff, error)  { return handoff.QueryHandoffs(dbPath, wfID) }
func sendAgentMessage(dbPath string, msg AgentMessage) error {
	return handoff.SendAgentMessage(dbPath, msg, newUUID)
}
func queryAgentMessages(dbPath, wfID, role string, limit int) ([]AgentMessage, error) {
	return handoff.QueryAgentMessages(dbPath, wfID, role, limit)
}
func parseAutoDelegate(output string) []AutoDelegation { return handoff.ParseAutoDelegate(output) }
func findMatchingBrace(s string) int                   { return handoff.FindMatchingBrace(s) }
func buildHandoffPrompt(ctx, instr string) string      { return handoff.BuildHandoffPrompt(ctx, instr) }

// --- Execution (root-only: uses runSingleTask, dispatchState, sseBroker, etc.) ---

func executeHandoff(ctx context.Context, cfg *Config, h *Handoff,
	state *dispatchState, sem, childSem chan struct{}) TaskResult {

	prompt := buildHandoffPrompt(h.Context, h.Instruction)

	task := Task{
		ID:        newUUID(),
		Name:      fmt.Sprintf("handoff:%s→%s", h.FromAgent, h.ToAgent),
		Prompt:    prompt,
		Agent:     h.ToAgent,
		Source:    "handoff:" + h.FromAgent,
		SessionID: h.ToSessionID,
	}
	fillDefaults(cfg, &task)

	if task.Agent != "" {
		if soulPrompt, err := loadAgentPrompt(cfg, task.Agent); err == nil && soulPrompt != "" {
			task.SystemPrompt = soulPrompt
		}
	}

	now := time.Now().Format(time.RFC3339)
	createSession(cfg.HistoryDB, Session{
		ID:        task.SessionID,
		Agent:     h.ToAgent,
		Source:    "handoff:" + h.FromAgent,
		Status:    "active",
		Title:     fmt.Sprintf("Handoff from %s", h.FromAgent),
		CreatedAt: now,
		UpdatedAt: now,
	})

	h.Status = "active"
	updateHandoffStatus(cfg.HistoryDB, h.ID, "active")

	result := runSingleTask(ctx, cfg, task, sem, childSem, h.ToAgent)
	recordSessionActivity(cfg.HistoryDB, task, result, h.ToAgent)

	if result.Status == "success" {
		updateHandoffStatus(cfg.HistoryDB, h.ID, "completed")
	} else {
		updateHandoffStatus(cfg.HistoryDB, h.ID, "error")
	}

	if cfg.Log {
		log.Info("handoff completed", "from", h.FromAgent, "to", h.ToAgent, "handoff", h.ID[:8], "status", result.Status)
	}

	return result
}

func processAutoDelegations(ctx context.Context, cfg *Config, delegations []AutoDelegation,
	originalOutput, workflowRunID, fromAgent, fromStepID string,
	state *dispatchState, sem, childSem chan struct{}, broker *sseBroker) string {

	if len(delegations) == 0 {
		return originalOutput
	}

	combinedOutput := originalOutput

	for _, d := range delegations {
		if _, ok := cfg.Agents[d.Agent]; !ok {
			log.Warn("auto-delegate agent not found, skipping", "agent", d.Agent)
			continue
		}

		now := time.Now().Format(time.RFC3339)
		handoffID := newUUID()
		toSessionID := newUUID()

		h := Handoff{
			ID:            handoffID,
			WorkflowRunID: workflowRunID,
			FromAgent:     fromAgent,
			ToAgent:       d.Agent,
			FromStepID:    fromStepID,
			Context:       truncateStr(originalOutput, cfg.PromptBudget.ContextMaxOrDefault()),
			Instruction:   d.Task,
			Status:        "pending",
			ToSessionID:   toSessionID,
			CreatedAt:     now,
		}
		recordHandoff(cfg.HistoryDB, h)

		sendAgentMessage(cfg.HistoryDB, AgentMessage{
			WorkflowRunID: workflowRunID,
			FromAgent:     fromAgent,
			ToAgent:       d.Agent,
			Type:          "handoff",
			Content:       fmt.Sprintf("Auto-delegated: %s (reason: %s)", d.Task, d.Reason),
			RefID:         handoffID,
			CreatedAt:     now,
		})

		if broker != nil {
			broker.PublishMulti([]string{
				"workflow:" + workflowRunID,
			}, SSEEvent{
				Type: "auto_delegation",
				Data: map[string]any{
					"handoffId": handoffID,
					"fromAgent": fromAgent,
					"toAgent":   d.Agent,
					"task":      d.Task,
					"reason":    d.Reason,
				},
			})
		}

		if cfg.Log {
			log.Info("auto-delegate executing", "from", fromAgent, "to", d.Agent, "task", truncate(d.Task, 60))
		}

		result := executeHandoff(ctx, cfg, &h, state, sem, childSem)

		if result.Output != "" {
			combinedOutput += fmt.Sprintf("\n---\n[Delegated to %s]\n%s", d.Agent, result.Output)
		}

		sendAgentMessage(cfg.HistoryDB, AgentMessage{
			WorkflowRunID: workflowRunID,
			FromAgent:     d.Agent,
			ToAgent:       fromAgent,
			Type:          "response",
			Content:       truncateStr(result.Output, 2000),
			RefID:         handoffID,
			CreatedAt:     time.Now().Format(time.RFC3339),
		})
	}

	return combinedOutput
}

// ============================================================
// Merged from cron.go
// ============================================================

// --- Type aliases (internal/cron is canonical) ---

// CronEngine is the cron scheduler. Root package uses this alias so existing
// callers (app.go, discord.go, health.go, wire_*.go, etc.) continue to compile
// without change. All logic lives in internal/cron.Engine.
type CronEngine = cron.Engine

// CronJobConfig is the persisted configuration for a single cron job.
type CronJobConfig = cron.JobConfig

// CronTaskConfig holds the execution parameters for a cron task.
type CronTaskConfig = cron.TaskConfig

// CronJobInfo is a read-only snapshot of a cron job for display/API.
type CronJobInfo = cron.JobInfo

// JobsFile is the top-level structure of jobs.json.
type JobsFile = cron.JobsFile

// --- Quiet hours (root-only global, used by tick and Telegram) ---

var quietGlobal = quiet.NewState(func(msg string, kv ...any) {})

func toQuietCfg(cfg *Config) quiet.Config {
	return quiet.Config{
		Enabled: cfg.QuietHours.Enabled,
		Start:   cfg.QuietHours.Start,
		End:     cfg.QuietHours.End,
		TZ:      cfg.QuietHours.TZ,
		Digest:  cfg.QuietHours.Digest,
	}
}

// newCronEngine constructs a CronEngine (cron.Engine) wired with all root-
// package callbacks that the internal cron package cannot import directly.
func newCronEngine(cfg *Config, sem, childSem chan struct{}, notifyFn func(string)) *CronEngine {
	env := cron.Env{
		Executor: dtypes.TaskExecutorFunc(func(ctx context.Context, task dtypes.Task, agentName string) dtypes.TaskResult {
			return runSingleTask(ctx, cfg, task, sem, childSem, agentName)
		}),

		FillDefaults: func(c *Config, t *dtypes.Task) {
			fillDefaults(c, t)
		},

		LoadAgentPrompt: func(c *Config, agentName string) (string, error) {
			return loadAgentPrompt(c, agentName)
		},

		ResolvePromptFile: func(c *Config, promptFile string) (string, error) {
			return resolvePromptFile(c, promptFile)
		},

		ExpandPrompt: func(prompt, jobID, dbPath, agentName, knowledgeDir string, c *Config) string {
			return expandPrompt(prompt, jobID, dbPath, agentName, knowledgeDir, c)
		},

		RecordHistory: func(dbPath, jobID, name, source, role string, task dtypes.Task, result dtypes.TaskResult, startedAt, finishedAt, outputFile string) {
			recordHistory(dbPath, jobID, name, source, role, task, result, startedAt, finishedAt, outputFile)
		},

		RecordSessionActivity: func(dbPath string, task dtypes.Task, result dtypes.TaskResult, role string) {
			recordSessionActivity(dbPath, task, result, role)
		},

		TriageBacklog: func(ctx context.Context, c *Config, s, cs chan struct{}) {
			triageBacklog(ctx, c, s, cs)
		},

		RunDailyNotesJob: func(ctx context.Context, c *Config) error {
			return runDailyNotesJob(ctx, c)
		},

		SendWebhooks: func(c *Config, event string, payload webhook.Payload) {
			sendWebhooks(c, event, payload)
		},

		NewUUID: newUUID,

		RegisterWorkerOrigin: func(sessionID, taskID, taskName, source, agent, jobID string) {
			if cfg.Runtime.HookRecv == nil {
				return
			}
			cfg.Runtime.HookRecv.(*hookReceiver).RegisterOrigin(sessionID, &workerOrigin{
				TaskID:   taskID,
				TaskName: taskName,
				Source:   source,
				Agent:    agent,
				JobID:    jobID,
			})
		},

		NotifyKeyboard: func(jobName, schedule string, approvalTimeout time.Duration, jobID string) {
			// Telegram keyboard notification is wired in wire_telegram.go via
			// the notifyKeyboardFn on the telegramRuntime, not directly here.
			// For now, fall back to plain text notification.
			if notifyFn != nil {
				notifyFn("Job \"" + jobName + "\" requires approval. /approve " + jobID + " or /reject " + jobID)
			}
		},

		QuietCfg: func(c *Config) quiet.Config {
			return toQuietCfg(c)
		},

		QuietGlobal: quietGlobal,
	}

	return cron.NewEngine(cfg, sem, childSem, notifyFn, env)
}

// ============================================================
// Merged from cron_expr.go
// ============================================================

type cronExpr = cron.Expr

func parseCronExpr(s string) (cronExpr, error) {
	return cron.Parse(s)
}

func nextRunAfter(expr cronExpr, loc *time.Location, after time.Time) time.Time {
	return cron.NextRunAfter(expr, loc, after)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func seedDefaultJobs() []CronJobConfig {
	return []CronJobConfig{
		{
			ID:           "self-improve",
			Name:         "Self-Improvement",
			Enabled:      true,
			Schedule:     "0 3 */2 * *",
			TZ:           "Asia/Taipei",
			IdleMinHours: 2,
			Task: CronTaskConfig{
				Prompt: `You are a self-improvement agent for the Tetora AI orchestration system.

Analyze the activity digest below. The digest includes existing Skills, Rules, and Memory —
do NOT create anything that already exists.

## Instructions
1. Identify repeated patterns (3+ occurrences), low-score reflections, recurring failures
2. For each actionable improvement, CREATE the file directly:
   - **Rule**: Create ` + "`rules/{name}.md`" + ` — governance rules auto-injected into all agents
   - **Memory**: Create/update ` + "`memory/{key}.md`" + ` — shared observations
   - **Skill**: Create ` + "`skills/{name}/metadata.json`" + ` with ` + "`\"approved\": false`" + ` — requires human review
3. Only apply HIGH and MEDIUM priority improvements
4. Keep files concise and actionable
5. Report what you created and why

If insufficient data for improvements, say so and exit.

---

{{review.digest:7}}`,
				Model:          "sonnet",
				Timeout:        "5m",
				Budget:         1.5,
				PermissionMode: "acceptEdits",
			},
			Notify:     true,
			MaxRetries: 1,
			RetryDelay: "2m",
		},
		{
			ID:       "backlog-triage",
			Name:     "Backlog Triage",
			Enabled:  true,
			Schedule: "50 9 * * *",
			TZ:       "Asia/Taipei",
			Task:     CronTaskConfig{},
			Notify:   true,
		},
	}
}

func cronDiscordSendBotChannel(botToken, channelID, msg string) error {
	if len(msg) > 2000 {
		msg = msg[:1997] + "..."
	}
	payload, err := json.Marshal(map[string]string{"content": msg})
	if err != nil {
		return err
	}
	url := fmt.Sprintf("https://discord.com/api/v10/channels/%s/messages", channelID)
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("POST", url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bot "+botToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("Discord returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func cronDiscordSendWebhook(webhookURL, msg string) error {
	if len(msg) > 2000 {
		msg = msg[:1997] + "..."
	}
	payload, err := json.Marshal(map[string]string{"content": msg})
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(webhookURL, "application/json", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("Discord returned HTTP %d", resp.StatusCode)
	}
	return nil
}
