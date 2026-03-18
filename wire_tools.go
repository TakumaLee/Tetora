package main

// wire_tools.go constructs tool dependency structs from root globals
// and registers tools via internal/tools.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tetora/internal/classify"
	dtypes "tetora/internal/dispatch"
	iproactive "tetora/internal/proactive"
	"tetora/internal/prompt"
	"tetora/internal/tool"
	"tetora/internal/tools"
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

		RegisterDeviceTools: registerDeviceTools,

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
