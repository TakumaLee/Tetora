package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// --- Core config types (from config.go) ---

type AgentConfig struct {
	SoulFile          string          `json:"soulFile"`
	Model             string          `json:"model"`
	Description       string          `json:"description"`
	Keywords          []string        `json:"keywords,omitempty"`
	PermissionMode    string          `json:"permissionMode,omitempty"`
	AllowedDirs       []string        `json:"allowedDirs,omitempty"`
	Provider          string          `json:"provider,omitempty"`
	Docker            *bool           `json:"docker,omitempty"`
	FallbackProviders []string        `json:"fallbackProviders,omitempty"`
	TrustLevel        string          `json:"trustLevel,omitempty"`
	ToolPolicy        AgentToolPolicy `json:"tools,omitempty"`
	ToolProfile       string          `json:"toolProfile,omitempty"`
	Workspace         WorkspaceConfig `json:"workspace,omitempty"`
	Portrait              string          `json:"portrait,omitempty"`
	VoicePreset           string          `json:"voicePreset,omitempty"`           // e.g. "alice", "carter", "maya"
	DangerousOpsWhitelist []string        `json:"dangerousOpsWhitelist,omitempty"` // patterns allowed for this agent
	CloudModel            string          `json:"cloudModel,omitempty"`            // preserved cloud model when switching to local
	LocalModel            string          `json:"localModel,omitempty"`            // preferred local model for this agent
	PinMode               string          `json:"pinMode,omitempty"`               // "cloud" | "local" | "" (follows global)
}

type ProviderConfig struct {
	Type              string `json:"type"`
	Path              string `json:"path,omitempty"`
	BaseURL           string `json:"baseUrl,omitempty"`
	APIKey            string `json:"apiKey,omitempty"`
	Model             string `json:"model,omitempty"`
	MaxTokens         int    `json:"maxTokens,omitempty"`
	FirstTokenTimeout string `json:"firstTokenTimeout,omitempty"`
}

type CostAlertConfig struct {
	DailyLimit      float64 `json:"dailyLimit"`
	WeeklyLimit     float64 `json:"weeklyLimit"`
	DailyTokenLimit int     `json:"dailyTokenLimit,omitempty"`
	Action          string  `json:"action"`
}

type DashboardAuthConfig struct {
	Enabled  bool   `json:"enabled"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Token    string `json:"token,omitempty"`
}

type QuietHoursConfig struct {
	Enabled bool   `json:"enabled"`
	Start   string `json:"start,omitempty"`
	End     string `json:"end,omitempty"`
	TZ      string `json:"tz,omitempty"`
	Digest  bool   `json:"digest,omitempty"`
}

type DigestConfig struct {
	Enabled bool   `json:"enabled"`
	Time    string `json:"time,omitempty"`
	TZ      string `json:"tz,omitempty"`
}

type NotificationChannel struct {
	Name        string   `json:"name,omitempty"`
	Type        string   `json:"type"`
	WebhookURL  string   `json:"webhookUrl"`
	Events      []string `json:"events,omitempty"`
	MinPriority string   `json:"minPriority,omitempty"`
}

type RateLimitConfig struct {
	Enabled   bool `json:"enabled"`
	MaxPerMin int  `json:"maxPerMin,omitempty"`
}

type TLSConfig struct {
	CertFile string `json:"certFile,omitempty"`
	KeyFile  string `json:"keyFile,omitempty"`
}

type SecurityAlertConfig struct {
	Enabled       bool `json:"enabled"`
	FailThreshold int  `json:"failThreshold,omitempty"`
	FailWindowMin int  `json:"failWindowMin,omitempty"`
}

type WebhookConfig struct {
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Events  []string          `json:"events,omitempty"`
}

type MCPServerConfig struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Enabled *bool             `json:"enabled,omitempty"`
}

// --- SmartDispatch ---

type SmartDispatchConfig struct {
	Enabled         bool             `json:"enabled"`
	Coordinator     string           `json:"coordinator,omitempty"`
	DefaultAgent    string           `json:"defaultAgent,omitempty"`
	ClassifyBudget  float64          `json:"classifyBudget,omitempty"`
	ClassifyTimeout string           `json:"classifyTimeout,omitempty"`
	Review          bool             `json:"review,omitempty"`
	ReviewLoop      bool             `json:"reviewLoop,omitempty"`
	MaxRetries      int              `json:"maxRetries,omitempty"`
	ReviewAgent     string           `json:"reviewAgent,omitempty"`
	ReviewBudget    float64          `json:"reviewBudget,omitempty"`
	Rules           []RoutingRule    `json:"rules,omitempty"`
	Bindings        []RoutingBinding `json:"bindings,omitempty"`
	Fallback        string           `json:"fallback,omitempty"`
}

func (c SmartDispatchConfig) MaxRetriesOrDefault() int {
	if c.MaxRetries > 0 {
		return c.MaxRetries
	}
	return 3
}

// UnmarshalJSON implements backward compat: accepts both "defaultRole" and "defaultAgent".
func (s *SmartDispatchConfig) UnmarshalJSON(data []byte) error {
	type SDAlias SmartDispatchConfig
	var alias SDAlias
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}
	if alias.DefaultAgent == "" {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err == nil {
			if drRaw, ok := raw["defaultRole"]; ok {
				var dr string
				if err := json.Unmarshal(drRaw, &dr); err == nil {
					alias.DefaultAgent = dr
				}
			}
		}
	}
	*s = SmartDispatchConfig(alias)
	return nil
}

type RoutingRule struct {
	Agent    string   `json:"agent"`
	Keywords []string `json:"keywords"`
	Patterns []string `json:"patterns,omitempty"`
}

// UnmarshalJSON implements backward compat: accepts both "role" and "agent".
func (r *RoutingRule) UnmarshalJSON(data []byte) error {
	type RRAlias RoutingRule
	var alias RRAlias
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}
	if alias.Agent == "" {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err == nil {
			if roleRaw, ok := raw["role"]; ok {
				var role string
				if err := json.Unmarshal(roleRaw, &role); err == nil {
					alias.Agent = role
				}
			}
		}
	}
	*r = RoutingRule(alias)
	return nil
}

type RoutingBinding struct {
	Channel   string `json:"channel"`
	UserID    string `json:"userId,omitempty"`
	ChannelID string `json:"channelId,omitempty"`
	GuildID   string `json:"guildId,omitempty"`
	Agent     string `json:"agent"`
}

// UnmarshalJSON implements backward compat: accepts both "role" and "agent".
func (b *RoutingBinding) UnmarshalJSON(data []byte) error {
	type RBAlias RoutingBinding
	var alias RBAlias
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}
	if alias.Agent == "" {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err == nil {
			if roleRaw, ok := raw["role"]; ok {
				var role string
				if err := json.Unmarshal(roleRaw, &role); err == nil {
					alias.Agent = role
				}
			}
		}
	}
	*b = RoutingBinding(alias)
	return nil
}

// --- Estimate ---

type EstimateConfig struct {
	ConfirmThreshold    float64 `json:"confirmThreshold,omitempty"`
	DefaultOutputTokens int     `json:"defaultOutputTokens,omitempty"`
}

func (c EstimateConfig) ConfirmThresholdOrDefault() float64 {
	if c.ConfirmThreshold > 0 {
		return c.ConfirmThreshold
	}
	return 1.0
}

func (c EstimateConfig) DefaultOutputTokensOrDefault() int {
	if c.DefaultOutputTokens > 0 {
		return c.DefaultOutputTokens
	}
	return 500
}

// --- Tools ---

type ToolConfig struct {
	MaxIterations   int                    `json:"maxIterations,omitempty"`
	Timeout         int                    `json:"timeout,omitempty"`
	Builtin         map[string]bool        `json:"builtin,omitempty"`
	Profiles        map[string]ToolProfile `json:"profiles,omitempty"`
	DefaultProfile  string                 `json:"defaultProfile,omitempty"`
	TrustOverride   map[string]string      `json:"trustOverride,omitempty"`
	ToolOutputLimit int                    `json:"toolOutputLimit,omitempty"`
	ToolTimeout     int                    `json:"toolTimeout,omitempty"`
	WebSearch       WebSearchConfig        `json:"webSearch,omitempty"`
	Vision          VisionConfig           `json:"vision,omitempty"`
}

type WebSearchConfig struct {
	Provider   string `json:"provider,omitempty"`
	APIKey     string `json:"apiKey,omitempty"`
	BaseURL    string `json:"baseURL,omitempty"`
	MaxResults int    `json:"maxResults,omitempty"`
}

type VisionConfig struct {
	Provider     string `json:"provider,omitempty"`
	APIKey       string `json:"apiKey,omitempty"`
	Model        string `json:"model,omitempty"`
	MaxImageSize int    `json:"maxImageSize,omitempty"`
	BaseURL      string `json:"baseURL,omitempty"`
}

type ToolProfile struct {
	Name  string   `json:"name"`
	Allow []string `json:"allow"`
	Deny  []string `json:"deny,omitempty"`
}

type AgentToolPolicy struct {
	Profile      string   `json:"profile,omitempty"`
	GroupProfile string   `json:"groupProfile,omitempty"` // override profile for group chat context
	DMProfile    string   `json:"dmProfile,omitempty"`    // override profile for DM context
	Allow        []string `json:"allow,omitempty"`
	Deny         []string `json:"deny,omitempty"`
	Sandbox      string   `json:"sandbox,omitempty"`
	SandboxImage string   `json:"sandboxImage,omitempty"`
}

// --- CircuitBreaker ---

type CircuitBreakerConfig struct {
	Enabled          bool   `json:"enabled,omitempty"`
	FailThreshold    int    `json:"failThreshold,omitempty"`
	SuccessThreshold int    `json:"successThreshold,omitempty"`
	OpenTimeout      string `json:"openTimeout,omitempty"`
}

// --- Session ---

type SessionConfig struct {
	ContextMessages  int              `json:"contextMessages,omitempty"`
	CompactAfter     int              `json:"compactAfter,omitempty"`
	CompactKeep      int              `json:"compactKeep,omitempty"`
	CompactTokens    int              `json:"compactTokens,omitempty"`
	MaxContextTokens int              `json:"maxContextTokens,omitempty"`
	IdleTimeout      string           `json:"idleTimeout,omitempty"`
	Compaction       CompactionConfig `json:"compaction,omitempty"`
}

func (c SessionConfig) ContextMessagesOrDefault() int {
	if c.ContextMessages > 0 {
		return c.ContextMessages
	}
	return 20
}

func (c SessionConfig) CompactAfterOrDefault() int {
	if c.CompactAfter > 0 {
		return c.CompactAfter
	}
	return 30
}

func (c SessionConfig) CompactKeepOrDefault() int {
	if c.CompactKeep > 0 {
		return c.CompactKeep
	}
	return 10
}

func (c SessionConfig) CompactTokensOrDefault() int {
	if c.CompactTokens > 0 {
		return c.CompactTokens
	}
	return 200000
}

func (c SessionConfig) MaxContextTokensOrDefault() int {
	if c.MaxContextTokens > 0 {
		return c.MaxContextTokens
	}
	return 60000
}

func (c SessionConfig) IdleTimeoutOrDefault() time.Duration {
	if c.IdleTimeout != "" {
		if d, err := time.ParseDuration(c.IdleTimeout); err == nil {
			return d
		}
	}
	return 90 * time.Minute
}

type CompactionConfig struct {
	Enabled     bool    `json:"enabled,omitempty"`
	MaxMessages int     `json:"maxMessages,omitempty"`
	CompactTo   int     `json:"compactTo,omitempty"`
	Model       string  `json:"model,omitempty"`
	MaxCost     float64 `json:"maxCost,omitempty"`
	Provider    string  `json:"provider,omitempty"`
	// Strategy controls how compaction works:
	//   "inline"        (default) — truncate session_messages in place, keep same Claude CLI session
	//   "fresh-session" — summarize → save to memory → archive session → next msg starts clean JSONL
	// "fresh-session" eliminates cache write cost accumulation at the expense of losing native --resume context.
	Strategy string `json:"strategy,omitempty"`
	// Mode controls whether compaction runs automatically or only notifies:
	//   ""      / "auto"   (default) — compact automatically when threshold is reached
	//   "notify"           — send a notification to the user instead of compacting automatically
	Mode string `json:"mode,omitempty"`
}

// --- Logging ---

type LoggingConfig struct {
	Level     string `json:"level,omitempty"`
	Format    string `json:"format,omitempty"`
	File      string `json:"file,omitempty"`
	MaxSizeMB int    `json:"maxSizeMB,omitempty"`
	MaxFiles  int    `json:"maxFiles,omitempty"`
}

func (c LoggingConfig) LevelOrDefault() string {
	if c.Level != "" {
		return c.Level
	}
	return "info"
}

func (c LoggingConfig) FormatOrDefault() string {
	if c.Format != "" {
		return c.Format
	}
	return "text"
}

func (c LoggingConfig) MaxSizeMBOrDefault() int {
	if c.MaxSizeMB > 0 {
		return c.MaxSizeMB
	}
	return 50
}

func (c LoggingConfig) MaxFilesOrDefault() int {
	if c.MaxFiles > 0 {
		return c.MaxFiles
	}
	return 5
}

// --- Voice ---

type VoiceConfig struct {
	STT      STTConfig           `json:"stt,omitempty"`
	TTS      TTSConfig           `json:"tts,omitempty"`
	Wake     VoiceWakeConfig     `json:"wake,omitempty"`
	Realtime VoiceRealtimeConfig `json:"realtime,omitempty"`
}

type STTConfig struct {
	Enabled  bool   `json:"enabled,omitempty"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
	APIKey   string `json:"apiKey,omitempty"`
	Language string `json:"language,omitempty"`
}

type TTSConfig struct {
	Enabled   bool     `json:"enabled,omitempty"`
	Provider  string   `json:"provider,omitempty"`
	Providers []string `json:"providers,omitempty"` // fallback chain
	Model     string   `json:"model,omitempty"`
	Endpoint  string   `json:"endpoint,omitempty"`
	APIKey    string   `json:"apiKey,omitempty"`
	FalAPIKey string   `json:"falApiKey,omitempty"`
	Voice     string   `json:"voice,omitempty"`
	Format    string   `json:"format,omitempty"`
	VibeVoice VibeVoiceConfig `json:"vibevoice,omitempty"`
}

type VibeVoiceConfig struct {
	Endpoint string `json:"endpoint,omitempty"`
}

type VoiceWakeConfig struct {
	Enabled   bool     `json:"enabled,omitempty"`
	WakeWords []string `json:"wakeWords,omitempty"`
	Threshold float64  `json:"threshold,omitempty"`
}

type VoiceRealtimeConfig struct {
	Enabled  bool   `json:"enabled,omitempty"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	APIKey   string `json:"apiKey,omitempty"`
	Voice    string `json:"voice,omitempty"`
}

// --- Push ---

type PushConfig struct {
	Enabled         bool   `json:"enabled,omitempty"`
	VAPIDPublicKey  string `json:"vapidPublicKey,omitempty"`
	VAPIDPrivateKey string `json:"vapidPrivateKey,omitempty"`
	VAPIDEmail      string `json:"vapidEmail,omitempty"`
	TTL             int    `json:"ttl,omitempty"`
}

// --- AgentComm ---

type AgentCommConfig struct {
	Enabled            bool `json:"enabled,omitempty"`
	MaxConcurrent      int  `json:"maxConcurrent,omitempty"`
	DefaultTimeout     int  `json:"defaultTimeout,omitempty"`
	MaxDepth           int  `json:"maxDepth,omitempty"`
	MaxChildrenPerTask int  `json:"maxChildrenPerTask,omitempty"`
	ChildSem           int  `json:"childSem,omitempty"`
}

// --- Proactive ---

type ProactiveConfig struct {
	Enabled bool            `json:"enabled,omitempty"`
	Rules   []ProactiveRule `json:"rules,omitempty"`
}

type ProactiveRule struct {
	Name     string            `json:"name"`
	Trigger  ProactiveTrigger  `json:"trigger"`
	Action   ProactiveAction   `json:"action"`
	Delivery ProactiveDelivery `json:"delivery"`
	Cooldown string            `json:"cooldown,omitempty"`
	Enabled  *bool             `json:"enabled,omitempty"`
}

func (r ProactiveRule) IsEnabled() bool {
	return r.Enabled == nil || *r.Enabled
}

type ProactiveTrigger struct {
	Type     string  `json:"type"`
	Cron     string  `json:"cron,omitempty"`
	TZ       string  `json:"tz,omitempty"`
	Event    string  `json:"event,omitempty"`
	Metric   string  `json:"metric,omitempty"`
	Op       string  `json:"op,omitempty"`
	Value    float64 `json:"value,omitempty"`
	Interval string  `json:"interval,omitempty"`
}

type ProactiveAction struct {
	Type           string         `json:"type"`
	Agent          string         `json:"agent,omitempty"`
	Prompt         string         `json:"prompt,omitempty"`
	PromptTemplate string         `json:"promptTemplate,omitempty"`
	Params         map[string]any `json:"params,omitempty"`
	Message        string         `json:"message,omitempty"`
	Autonomous     bool           `json:"autonomous,omitempty"`
}

type ProactiveDelivery struct {
	Channel string `json:"channel"`
	ChatID  int64  `json:"chatId,omitempty"`
}

// --- GroupChat ---

type GroupChatConfig struct {
	Activation    string                   `json:"activation,omitempty"`
	Keywords      []string                 `json:"keywords,omitempty"`
	ContextWindow int                      `json:"contextWindow,omitempty"`
	RateLimit     GroupChatRateLimitConfig `json:"rateLimit,omitempty"`
	AllowedGroups map[string][]string      `json:"allowedGroups,omitempty"`
	ThreadReply   bool                     `json:"threadReply,omitempty"`
	MentionNames  []string                 `json:"mentionNames,omitempty"`
}

type GroupChatRateLimitConfig struct {
	MaxPerMin int  `json:"maxPerMin,omitempty"`
	PerGroup  bool `json:"perGroup,omitempty"`
}

// --- PromptBudget ---

type PromptBudgetConfig struct {
	SoulMax          int `json:"soulMax,omitempty"`
	RulesMax         int `json:"rulesMax,omitempty"`
	KnowledgeMax     int `json:"knowledgeMax,omitempty"`
	SkillsMax        int `json:"skillsMax,omitempty"`
	MaxSkillsPerTask int `json:"maxSkillsPerTask,omitempty"`
	ContextMax       int `json:"contextMax,omitempty"`
	TotalMax         int `json:"totalMax,omitempty"`
}

func (c PromptBudgetConfig) SoulMaxOrDefault() int {
	if c.SoulMax > 0 {
		return c.SoulMax
	}
	return 8000
}
func (c PromptBudgetConfig) RulesMaxOrDefault() int {
	if c.RulesMax > 0 {
		return c.RulesMax
	}
	return 4000
}
func (c PromptBudgetConfig) KnowledgeMaxOrDefault() int {
	if c.KnowledgeMax > 0 {
		return c.KnowledgeMax
	}
	return 8000
}
func (c PromptBudgetConfig) SkillsMaxOrDefault() int {
	if c.SkillsMax > 0 {
		return c.SkillsMax
	}
	return 4000
}
func (c PromptBudgetConfig) MaxSkillsPerTaskOrDefault() int {
	if c.MaxSkillsPerTask > 0 {
		return c.MaxSkillsPerTask
	}
	return 3
}
func (c PromptBudgetConfig) ContextMaxOrDefault() int {
	if c.ContextMax > 0 {
		return c.ContextMax
	}
	return 16000
}
func (c PromptBudgetConfig) TotalMaxOrDefault() int {
	if c.TotalMax > 0 {
		return c.TotalMax
	}
	return 40000
}

// --- ApprovalGates ---

type ApprovalGateConfig struct {
	Enabled          bool     `json:"enabled,omitempty"`
	Timeout          int      `json:"timeout,omitempty"`
	Tools            []string `json:"tools,omitempty"`
	AutoApproveTools []string `json:"autoApproveTools,omitempty"`
}

// --- Writing / Citation ---

type WritingStyleConfig struct {
	Enabled    bool   `json:"enabled"`
	Guidelines string `json:"guidelines,omitempty"`
	FilePath   string `json:"filePath,omitempty"`
}

type CitationConfig struct {
	Enabled bool   `json:"enabled"`
	Format  string `json:"format,omitempty"`
}

// --- BrowserRelay ---

type BrowserRelayConfig struct {
	Enabled bool   `json:"enabled"`
	Port    int    `json:"port,omitempty"`
	Token   string `json:"token,omitempty"`
}

// --- NotebookLM ---

type NotebookLMConfig struct {
	Enabled bool `json:"enabled"`
}

// --- ImageGen ---

type ImageGenConfig struct {
	Enabled    bool    `json:"enabled"`
	Provider   string  `json:"provider,omitempty"`
	APIKey     string  `json:"apiKey,omitempty"`
	Model      string  `json:"model,omitempty"`
	DailyLimit int     `json:"dailyLimit,omitempty"`
	MaxCostDay float64 `json:"maxCostDay,omitempty"`
	Quality    string  `json:"quality,omitempty"`
}

// --- Weather / Currency / RSS / Translate ---

type WeatherConfig struct {
	Enabled  bool   `json:"enabled"`
	Location string `json:"defaultLocation,omitempty"`
}

type CurrencyConfig struct {
	Enabled bool `json:"enabled"`
}

type RSSConfig struct {
	Enabled bool     `json:"enabled"`
	Feeds   []string `json:"feeds,omitempty"`
}

type TranslateConfig struct {
	Enabled  bool   `json:"enabled"`
	Provider string `json:"provider,omitempty"`
	APIKey   string `json:"apiKey,omitempty"`
}

// --- UserProfile ---

type UserProfileConfig struct {
	Enabled          bool `json:"enabled"`
	SentimentEnabled bool `json:"sentiment,omitempty"`
	AdaptPersonality bool `json:"adaptPersonality,omitempty"`
}

// --- Ops ---

type OpsConfig struct {
	BackupSchedule string             `json:"backupSchedule,omitempty"`
	BackupRetain   int                `json:"backupRetain,omitempty"`
	BackupDir      string             `json:"backupDir,omitempty"`
	HealthNotify   bool               `json:"healthNotify,omitempty"`
	HealthCheckURL string             `json:"healthCheckUrl,omitempty"`
	ExportEnabled  bool               `json:"exportEnabled,omitempty"`
	MessageQueue   MessageQueueConfig `json:"messageQueue,omitempty"`
}

func (c OpsConfig) BackupRetainOrDefault() int {
	if c.BackupRetain > 0 {
		return c.BackupRetain
	}
	return 7
}

func (c OpsConfig) BackupDirResolved(baseDir string) string {
	if c.BackupDir != "" {
		return c.BackupDir
	}
	return baseDir + "/backups"
}

type MessageQueueConfig struct {
	Enabled       bool   `json:"enabled"`
	RetryAttempts int    `json:"retryAttempts,omitempty"`
	RetryBackoff  string `json:"retryBackoff,omitempty"`
	MaxQueueSize  int    `json:"maxQueueSize,omitempty"`
}

func (c MessageQueueConfig) RetryAttemptsOrDefault() int {
	if c.RetryAttempts > 0 {
		return c.RetryAttempts
	}
	return 3
}

func (c MessageQueueConfig) MaxQueueSizeOrDefault() int {
	if c.MaxQueueSize > 0 {
		return c.MaxQueueSize
	}
	return 1000
}

// --- Finance ---

type FinanceConfig struct {
	Enabled         bool   `json:"enabled"`
	DefaultCurrency string `json:"defaultCurrency,omitempty"`
	BudgetAlert     bool   `json:"budgetAlert,omitempty"`
}

func (c FinanceConfig) DefaultCurrencyOrTWD() string {
	if c.DefaultCurrency != "" {
		return c.DefaultCurrency
	}
	return "TWD"
}

// --- TaskManager ---

type TaskManagerConfig struct {
	Enabled        bool          `json:"enabled"`
	DefaultProject string        `json:"defaultProject,omitempty"`
	ReviewSchedule string        `json:"reviewSchedule,omitempty"`
	Todoist        TodoistConfig `json:"todoist,omitempty"`
	Notion         NotionConfig  `json:"notion,omitempty"`
}

type TodoistConfig struct {
	Enabled bool   `json:"enabled"`
	APIKey  string `json:"apiKey,omitempty"`
}

type NotionConfig struct {
	Enabled    bool   `json:"enabled"`
	APIKey     string `json:"apiKey,omitempty"`
	DatabaseID string `json:"databaseId,omitempty"`
}

func (c TaskManagerConfig) DefaultProjectOrInbox() string {
	if c.DefaultProject != "" {
		return c.DefaultProject
	}
	return "inbox"
}

// --- Types from other root files ---

// Discord configs.

type DiscordBotConfig struct {
	Enabled           bool                           `json:"enabled"`
	BotToken          string                         `json:"botToken"`
	GuildID           string                         `json:"guildID,omitempty"`
	ChannelID         string                         `json:"channelID,omitempty"`
	ChannelIDs        []string                       `json:"channelIDs,omitempty"`
	MentionChannelIDs []string                       `json:"mentionChannelIDs,omitempty"`
	Webhooks          map[string]string              `json:"webhooks,omitempty"`
	PublicKey         string                         `json:"publicKey,omitempty"`
	Components        DiscordComponentsConfig        `json:"components,omitempty"`
	ThreadBindings    DiscordThreadBindingsConfig    `json:"threadBindings,omitempty"`
	Reactions         DiscordReactionsConfig         `json:"reactions,omitempty"`
	ForumBoard        DiscordForumBoardConfig        `json:"forumBoard,omitempty"`
	Voice             DiscordVoiceConfig             `json:"voice,omitempty"`
	Terminal          DiscordTerminalConfig          `json:"terminal,omitempty"`
	NotifyChannelID   string                         `json:"notifyChannelID,omitempty"`
	ShowProgress      *bool                          `json:"showProgress,omitempty"`
	Routes            map[string]DiscordRouteConfig  `json:"routes,omitempty"`
	Recap             DiscordRecapConfig             `json:"recap,omitempty"`
	// HumanAssigneeMap maps human gate assignee names (e.g. "takuma") to Discord
	// channel IDs. When a human gate fires, the notification is routed to the
	// mapped channel. Falls back to the default notify channel if no mapping found.
	HumanAssigneeMap  map[string]string              `json:"humanAssigneeMap,omitempty"`
	// DashboardBaseURL is the public base URL of the Tetora dashboard (e.g.
	// "https://tetora.example.com"). Used to build the dashboard link in human
	// gate Discord notifications. If empty, falls back to http://localhost<listenAddr>.
	DashboardBaseURL  string                         `json:"dashboardBaseURL,omitempty"`
}

// DiscordRecapConfig forwards Claude Code "away_summary" transcripts to Discord
// threads. One thread per Claude session, auto-created under a parent channel
// chosen by cwd mapping.
type DiscordRecapConfig struct {
	Enabled              bool              `json:"enabled,omitempty"`
	DefaultParentChannel string            `json:"defaultParentChannel,omitempty"`
	ProjectChannels      map[string]string `json:"projectChannels,omitempty"`
	TranscriptRoot       string            `json:"transcriptRoot,omitempty"`
	PollIntervalMs       int               `json:"pollIntervalMs,omitempty"`
	ThreadAutoArchiveMin int               `json:"threadAutoArchiveMin,omitempty"`
}

type DiscordRouteConfig struct {
	Agent  string   `json:"agent,omitempty"`
	Mode   string   `json:"mode,omitempty"`
	Agents []string `json:"agents,omitempty"`
}

// UnmarshalJSON implements backward compat: accepts both "role" and "agent".
func (d *DiscordRouteConfig) UnmarshalJSON(data []byte) error {
	type Alias DiscordRouteConfig
	var alias Alias
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}
	if alias.Agent == "" {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err == nil {
			if roleRaw, ok := raw["role"]; ok {
				var role string
				if err := json.Unmarshal(roleRaw, &role); err == nil {
					alias.Agent = role
				}
			}
		}
	}
	*d = DiscordRouteConfig(alias)
	return nil
}

type DiscordComponentsConfig struct {
	Enabled         bool   `json:"enabled,omitempty"`
	ReusableDefault bool   `json:"reusableDefault,omitempty"`
	AccentColor     string `json:"accentColor,omitempty"`
}

type DiscordThreadBindingsConfig struct {
	Enabled               bool `json:"enabled,omitempty"`
	TTLHours              int  `json:"ttlHours,omitempty"`
	SpawnSubagentSessions bool `json:"spawnSubagentSessions,omitempty"`
}

// ThreadBindingsTTL returns the TTL for thread bindings (default 24h).
func (c DiscordThreadBindingsConfig) ThreadBindingsTTL() time.Duration {
	if c.TTLHours <= 0 {
		return 24 * time.Hour
	}
	return time.Duration(c.TTLHours) * time.Hour
}

type DiscordReactionsConfig struct {
	Enabled bool              `json:"enabled,omitempty"`
	Emojis  map[string]string `json:"emojis,omitempty"`
}

type DiscordForumBoardConfig struct {
	Enabled        bool              `json:"enabled,omitempty"`
	ForumChannelID string            `json:"forumChannelId,omitempty"`
	Tags           map[string]string `json:"tags,omitempty"`
}

type DiscordVoiceConfig struct {
	Enabled  bool                   `json:"enabled"`
	AutoJoin []DiscordVoiceAutoJoin `json:"autoJoin,omitempty"`
	TTS      DiscordVoiceTTSConfig  `json:"tts,omitempty"`
}

type DiscordVoiceAutoJoin struct {
	GuildID   string `json:"guildId"`
	ChannelID string `json:"channelId"`
}

type DiscordVoiceTTSConfig struct {
	Provider string `json:"provider,omitempty"`
	Voice    string `json:"voice,omitempty"`
}

type DiscordTerminalConfig struct {
	Enabled      bool     `json:"enabled"`
	AllowedUsers []string `json:"allowedUsers,omitempty"`
	MaxSessions  int      `json:"maxSessions,omitempty"`
	CaptureRows  int      `json:"captureRows,omitempty"`
	CaptureCols  int      `json:"captureCols,omitempty"`
	IdleTimeout  string   `json:"idleTimeout,omitempty"`
	ClaudePath   string   `json:"claudePath,omitempty"`
	CodexPath    string   `json:"codexPath,omitempty"`
	DefaultTool  string   `json:"defaultTool,omitempty"`
	Workdir      string   `json:"workdir,omitempty"`
}

// Docker / Sandbox.

type DockerConfig struct {
	Enabled  bool     `json:"enabled"`
	Image    string   `json:"image,omitempty"`
	Network  string   `json:"network,omitempty"`
	Memory   string   `json:"memory,omitempty"`
	CPUs     string   `json:"cpus,omitempty"`
	ReadOnly bool     `json:"readOnly,omitempty"`
	Volumes  []string `json:"volumes,omitempty"`
	EnvPass  []string `json:"envPass,omitempty"`
}

type SandboxConfig struct {
	Plugin       string `json:"plugin,omitempty"`
	DefaultImage string `json:"defaultImage,omitempty"`
	MemLimit     string `json:"memLimit,omitempty"`
	CPULimit     string `json:"cpuLimit,omitempty"`
	Network      string `json:"network,omitempty"`
}

func (c SandboxConfig) DefaultImageOrDefault() string {
	if c.DefaultImage != "" {
		return c.DefaultImage
	}
	return "ubuntu:22.04"
}

func (c SandboxConfig) NetworkOrDefault() string {
	if c.Network != "" {
		return c.Network
	}
	return "none"
}

// Plugin.

type PluginConfig struct {
	Type      string            `json:"type"`
	Command   string            `json:"command"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	AutoStart bool              `json:"autoStart,omitempty"`
	Tools     []string          `json:"tools,omitempty"`
}

// OAuth.

type OAuthConfig struct {
	Services      map[string]OAuthServiceConfig `json:"services,omitempty"`
	EncryptionKey string                        `json:"encryptionKey,omitempty"`
	RedirectBase  string                        `json:"redirectBase,omitempty"`
}

type OAuthServiceConfig struct {
	Name         string            `json:"name"`
	ClientID     string            `json:"clientId"`
	ClientSecret string            `json:"clientSecret"`
	AuthURL      string            `json:"authUrl"`
	TokenURL     string            `json:"tokenUrl"`
	Scopes       []string          `json:"scopes"`
	RedirectURL  string            `json:"redirectUrl,omitempty"`
	ExtraParams  map[string]string `json:"extraParams,omitempty"`
}

// Embedding.

type EmbeddingConfig struct {
	Enabled       bool           `json:"enabled,omitempty"`
	Provider      string         `json:"provider,omitempty"`
	Model         string         `json:"model,omitempty"`
	Endpoint      string         `json:"endpoint,omitempty"`
	APIKey        string         `json:"apiKey,omitempty"`
	Dimensions    int            `json:"dimensions,omitempty"`
	BatchSize     int            `json:"batchSize,omitempty"`
	MMR           MMRConfig      `json:"mmr,omitempty"`
	TemporalDecay TemporalConfig `json:"temporalDecay,omitempty"`
}

type MMRConfig struct {
	Enabled bool    `json:"enabled,omitempty"`
	Lambda  float64 `json:"lambda,omitempty"`
}

type TemporalConfig struct {
	Enabled      bool    `json:"enabled,omitempty"`
	HalfLifeDays float64 `json:"halfLifeDays,omitempty"`
	MinScore     float64 `json:"minScore,omitempty"` // 0 = disabled, e.g. 0.01
}

func (cfg EmbeddingConfig) MmrLambdaOrDefault() float64 {
	if cfg.MMR.Lambda > 0 {
		return cfg.MMR.Lambda
	}
	return 0.7
}

func (cfg EmbeddingConfig) DecayHalfLifeOrDefault() float64 {
	if cfg.TemporalDecay.HalfLifeDays > 0 {
		return cfg.TemporalDecay.HalfLifeDays
	}
	return 30.0
}

// Injection / Security.

// DangerousOpsConfig controls blocking of destructive shell/SQL/k8s commands in dispatch.
type DangerousOpsConfig struct {
	Enabled       *bool    `json:"enabled,omitempty"`       // nil = true (default enabled)
	ExtraPatterns []string `json:"extraPatterns,omitempty"` // additional regex patterns to block
}

// EnabledOrDefault returns whether dangerous ops blocking is active (default: true).
func (c *DangerousOpsConfig) EnabledOrDefault() bool {
	if c == nil || c.Enabled == nil {
		return true
	}
	return *c.Enabled
}

type InjectionDefenseConfig struct {
	Level              string  `json:"level,omitempty"`
	LLMJudgeProvider   string  `json:"llmJudgeProvider,omitempty"`
	LLMJudgeThreshold  float64 `json:"llmJudgeThreshold,omitempty"`
	BlockOnSuspicious  bool    `json:"blockOnSuspicious,omitempty"`
	CacheSize          int     `json:"cacheSize,omitempty"`
	CacheTTL           string  `json:"cacheTTL,omitempty"`
	EnableFingerprint  bool    `json:"enableFingerprint,omitempty"`
	FailOpen           bool    `json:"failOpen,omitempty"`
}

func (c InjectionDefenseConfig) LevelOrDefault() string {
	if c.Level != "" {
		return c.Level
	}
	return "basic"
}

func (c InjectionDefenseConfig) LlmJudgeProviderOrDefault() string {
	if c.LLMJudgeProvider != "" {
		return c.LLMJudgeProvider
	}
	return "claude-api"
}

func (c InjectionDefenseConfig) LlmJudgeThresholdOrDefault() float64 {
	if c.LLMJudgeThreshold > 0 {
		return c.LLMJudgeThreshold
	}
	return 0.8
}

func (c InjectionDefenseConfig) CacheSizeOrDefault() int {
	if c.CacheSize > 0 {
		return c.CacheSize
	}
	return 1000
}

func (c InjectionDefenseConfig) CacheTTLOrDefault() time.Duration {
	if c.CacheTTL != "" {
		if d, err := time.ParseDuration(c.CacheTTL); err == nil {
			return d
		}
	}
	return time.Hour
}

type SecurityConfig struct {
	InjectionDefense InjectionDefenseConfig `json:"injectionDefense,omitempty"`
	DangerousOps     DangerousOpsConfig     `json:"dangerousOps,omitempty"`
}

// Trust.

type TrustConfig struct {
	Enabled          bool `json:"enabled,omitempty"`
	PromoteThreshold int  `json:"promoteThreshold,omitempty"`
	AutoPromote      bool `json:"autoPromote,omitempty"`
}

func (c TrustConfig) PromoteThresholdOrDefault() int {
	if c.PromoteThreshold > 0 {
		return c.PromoteThreshold
	}
	return 10
}

// TaskBoard.

type TaskBoardConfig struct {
	Enabled         bool                    `json:"enabled"`
	MaxRetries      int                     `json:"maxRetries,omitempty"`
	MaxExecutions   int                     `json:"maxExecutions,omitempty"`
	RequireReview   bool                    `json:"requireReview,omitempty"`
	AutoDispatch    TaskBoardDispatchConfig `json:"autoDispatch,omitempty"`
	DefaultWorkflow string                  `json:"defaultWorkflow,omitempty"`
	GitCommit       bool                    `json:"gitCommit,omitempty"`
	GitPush         bool                    `json:"gitPush,omitempty"`
	GitPR           bool                    `json:"gitPR,omitempty"`
	GitWorktree     bool                    `json:"gitWorktree,omitempty"`
	GitWorkflow     GitWorkflowConfig       `json:"gitWorkflow,omitempty"`
	IdleAnalyze     bool                    `json:"idleAnalyze,omitempty"`
	ProblemScan     bool                    `json:"problemScan,omitempty"`
}

func (c TaskBoardConfig) MaxRetriesOrDefault() int {
	if c.MaxRetries > 0 {
		return c.MaxRetries
	}
	return 3
}

func (c TaskBoardConfig) MaxExecutionsOrDefault() int {
	if c.MaxExecutions > 0 {
		return c.MaxExecutions
	}
	return 3
}

type TaskBoardDispatchConfig struct {
	Enabled               bool                  `json:"enabled"`
	Interval              string                `json:"interval,omitempty"`
	DefaultModel          string                `json:"defaultModel,omitempty"`
	MaxBudget             float64               `json:"maxBudget,omitempty"`
	DefaultAgent          string                `json:"defaultAgent,omitempty"`
	BacklogAgent          string                `json:"backlogAgent,omitempty"`
	ReviewAgent           string                `json:"reviewAgent,omitempty"`
	EscalateAssignee      string                `json:"escalateAssignee,omitempty"`
	StuckThreshold        string                `json:"stuckThreshold,omitempty"`
	MaxConcurrentTasks    int                   `json:"maxConcurrentTasks,omitempty"`
	MaxTasksPerAgent      int                   `json:"maxTasksPerAgent,omitempty"`
	BacklogTriageInterval string                `json:"backlogTriageInterval,omitempty"`
	ReviewLoop            bool                  `json:"reviewLoop,omitempty"`
	TriageEnabled         bool                  `json:"triageEnabled,omitempty"`
	TriageBudget          float64               `json:"triageBudget,omitempty"`
	WorkflowRouting       WorkflowRoutingConfig `json:"workflowRouting,omitempty"`
	// MaxRSSMB is the RSS hard-limit for subprocess (MB). Exceeding it cancels the context → SIGKILL.
	// 0 = disabled. Default 2048 (2 GB).
	MaxRSSMB int `json:"maxRssMb,omitempty"`
}

func (c TaskBoardDispatchConfig) MaxTasksPerAgentOrDefault() int {
	if c.MaxTasksPerAgent > 0 {
		return c.MaxTasksPerAgent
	}
	return 1
}

func (c TaskBoardDispatchConfig) TriageBudgetOrDefault() float64 {
	if c.TriageBudget > 0 {
		return c.TriageBudget
	}
	return 0.05
}

func (c TaskBoardDispatchConfig) MaxRSSMBOrDefault() int {
	if c.MaxRSSMB > 0 {
		return c.MaxRSSMB
	}
	return 2048
}

type WorkflowRoutingConfig struct {
	Enabled  bool                  `json:"enabled"`
	Rules    []WorkflowRoutingRule `json:"rules,omitempty"`
	Fallback string                `json:"fallback,omitempty"`
}

type WorkflowRoutingRule struct {
	Workflow string   `json:"workflow"`
	Types    []string `json:"types,omitempty"`
	Priority []string `json:"priority,omitempty"`
	Projects []string `json:"projects,omitempty"`
	IsPublic *bool    `json:"isPublic,omitempty"`
}

type GitWorkflowConfig struct {
	BranchConvention string   `json:"branchConvention,omitempty"`
	Types            []string `json:"types,omitempty"`
	DefaultType      string   `json:"defaultType,omitempty"`
	AutoMerge        bool     `json:"autoMerge,omitempty"`
}

// Workflow triggers.

type WorkflowTriggerConfig struct {
	Name         string            `json:"name"`
	WorkflowName string            `json:"workflowName"`
	Enabled      *bool             `json:"enabled,omitempty"`
	Trigger      TriggerSpec       `json:"trigger"`
	Variables    map[string]string `json:"variables,omitempty"`
	Cooldown     string            `json:"cooldown,omitempty"`
}

func (t WorkflowTriggerConfig) IsEnabled() bool {
	return t.Enabled == nil || *t.Enabled
}

type TriggerSpec struct {
	Type    string `json:"type"`
	Cron    string `json:"cron,omitempty"`
	TZ      string `json:"tz,omitempty"`
	Event   string `json:"event,omitempty"`
	Webhook string `json:"webhook,omitempty"`
}

// Workspace.

type WorkspaceConfig struct {
	Dir        string       `json:"dir,omitempty"`
	SoulFile   string       `json:"soulFile,omitempty"`
	MCPServers []string     `json:"mcpServers,omitempty"`
	Sandbox    *SandboxMode `json:"sandbox,omitempty"`
}

type SandboxMode struct {
	Mode string `json:"mode"`
}

// Misc service configs.

type OfflineQueueConfig struct {
	Enabled  bool   `json:"enabled,omitempty"`
	TTL      string `json:"ttl,omitempty"`
	MaxItems int    `json:"maxItems,omitempty"`
}

// TtlOrDefault returns the TTL duration (default 1h).
func (c OfflineQueueConfig) TtlOrDefault() time.Duration {
	if c.TTL != "" {
		if d, err := time.ParseDuration(c.TTL); err == nil && d > 0 {
			return d
		}
	}
	return 1 * time.Hour
}

// MaxItemsOrDefault returns the max queue items (default 100).
func (c OfflineQueueConfig) MaxItemsOrDefault() int {
	if c.MaxItems > 0 {
		return c.MaxItems
	}
	return 100
}

type ReflectionConfig struct {
	Enabled       bool    `json:"enabled"`
	TriggerOnFail bool    `json:"triggerOnFail,omitempty"`
	MinCost       float64 `json:"minCost,omitempty"`
	Budget        float64 `json:"budget,omitempty"`
}

func (c ReflectionConfig) MinCostOrDefault() float64 {
	if c.MinCost > 0 {
		return c.MinCost
	}
	return 0.03
}

type NotifyIntelConfig struct {
	BatchInterval string `json:"notifyBatch,omitempty"`
}

type IncomingWebhookConfig struct {
	Agent    string `json:"agent"`
	Template string `json:"template,omitempty"`
	Secret   string `json:"secret,omitempty"`
	Filter   string `json:"filter,omitempty"`
	Workflow string `json:"workflow,omitempty"`
	Enabled  *bool  `json:"enabled,omitempty"`
}

func (c IncomingWebhookConfig) IsEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

type RetentionConfig struct {
	History        int      `json:"history,omitempty"`
	Sessions       int      `json:"sessions,omitempty"`
	AuditLog       int      `json:"auditLog,omitempty"`
	Logs           int      `json:"logs,omitempty"`
	Workflows      int      `json:"workflows,omitempty"`
	Reflections    int      `json:"reflections,omitempty"`
	SLA            int      `json:"sla,omitempty"`
	TrustEvents    int      `json:"trustEvents,omitempty"`
	Handoffs       int      `json:"handoffs,omitempty"`
	Queue          int      `json:"queue,omitempty"`
	Versions       int      `json:"versions,omitempty"`
	Outputs        int      `json:"outputs,omitempty"`
	Uploads        int      `json:"uploads,omitempty"`
	Memory         int      `json:"memory,omitempty"`
	ClaudeSessions int      `json:"claudeSessions,omitempty"`
	PIIPatterns    []string `json:"piiPatterns,omitempty"`
}

type AccessControlConfig struct {
	DMPairing      bool                `json:"dmPairing,omitempty"`
	PairingMessage string              `json:"pairingMessage,omitempty"`
	PairingExpiry  string              `json:"pairingExpiry,omitempty"`
	Allowlists     map[string][]string `json:"allowlists,omitempty"`
}

type SlotPressureConfig struct {
	Enabled               bool   `json:"enabled,omitempty"`
	ReservedSlots         int    `json:"reservedSlots,omitempty"`
	WarnThreshold         int    `json:"warnThreshold,omitempty"`
	NonInteractiveTimeout string `json:"nonInteractiveTimeout,omitempty"`
	PollInterval          string `json:"pollInterval,omitempty"`
	MonitorEnabled        bool   `json:"monitorEnabled,omitempty"`
	MonitorInterval       string `json:"monitorInterval,omitempty"`
}

type CanvasConfig struct {
	Enabled         bool   `json:"enabled,omitempty"`
	MaxIframeHeight string `json:"maxIframeHeight,omitempty"`
	AllowScripts    bool   `json:"allowScripts,omitempty"`
	CSP             string `json:"csp,omitempty"`
}

type DailyNotesConfig struct {
	Enabled  bool   `json:"enabled"`
	Dir      string `json:"dir,omitempty"`
	Schedule string `json:"schedule,omitempty"`
}

// DirOrDefault returns the configured notes directory, resolving relative paths against baseDir.
func (c DailyNotesConfig) DirOrDefault(baseDir string) string {
	if c.Dir != "" {
		dir := c.Dir
		if strings.HasPrefix(dir, "~/") {
			home, _ := os.UserHomeDir()
			dir = filepath.Join(home, dir[2:])
		}
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(baseDir, dir)
		}
		return dir
	}
	return filepath.Join(baseDir, "notes")
}

// ScheduleOrDefault returns the configured cron schedule (default "0 0 * * *" — daily at midnight).
func (c DailyNotesConfig) ScheduleOrDefault() string {
	if c.Schedule != "" {
		return c.Schedule
	}
	return "0 0 * * *"
}

type UsageConfig struct {
	ShowFooter     bool   `json:"showFooter,omitempty"`
	FooterTemplate string `json:"footerTemplate,omitempty"`
}

type WatchdogConfig struct {
	Enabled        bool   `json:"enabled,omitempty"`
	Interval       string `json:"interval,omitempty"`       // check interval, default "30s"
	FailureLimit   int    `json:"failureLimit,omitempty"`   // consecutive failures before exit, default 3
	TimeoutPerPing string `json:"timeoutPerPing,omitempty"` // per-request timeout, default "5s"
}

func (c WatchdogConfig) IntervalOrDefault() time.Duration {
	if c.Interval != "" {
		if d, err := time.ParseDuration(c.Interval); err == nil && d > 0 {
			return d
		}
	}
	return 30 * time.Second
}

func (c WatchdogConfig) FailureLimitOrDefault() int {
	if c.FailureLimit > 0 {
		return c.FailureLimit
	}
	return 3
}

func (c WatchdogConfig) TimeoutPerPingOrDefault() time.Duration {
	if c.TimeoutPerPing != "" {
		if d, err := time.ParseDuration(c.TimeoutPerPing); err == nil && d > 0 {
			return d
		}
	}
	return 5 * time.Second
}

type HeartbeatConfig struct {
	Enabled          bool    `json:"enabled,omitempty"`
	Interval         string  `json:"interval,omitempty"`
	StallThreshold   string  `json:"stallThreshold,omitempty"`
	TimeoutWarnRatio float64 `json:"timeoutWarnRatio,omitempty"`
	AutoCancel       bool    `json:"autoCancel,omitempty"`
	NotifyOnStall    bool    `json:"notifyOnStall,omitempty"`
}

func (c HeartbeatConfig) IntervalOrDefault() time.Duration {
	if c.Interval != "" {
		if d, err := time.ParseDuration(c.Interval); err == nil && d > 0 {
			return d
		}
	}
	return 30 * time.Second
}

func (c HeartbeatConfig) StallThresholdOrDefault() time.Duration {
	if c.StallThreshold != "" {
		if d, err := time.ParseDuration(c.StallThreshold); err == nil && d > 0 {
			return d
		}
	}
	return 5 * time.Minute
}

func (c HeartbeatConfig) TimeoutWarnRatioOrDefault() float64 {
	if c.TimeoutWarnRatio > 0 && c.TimeoutWarnRatio < 1 {
		return c.TimeoutWarnRatio
	}
	return 0.8
}

func (c HeartbeatConfig) NotifyOnStallOrDefault() bool {
	return c.NotifyOnStall || (!c.NotifyOnStall && !c.AutoCancel)
}

type HooksConfig struct {
	Enabled bool `json:"enabled,omitempty"`
}

type PlanGateConfig struct {
	Mode string `json:"mode,omitempty"`
}

type MCPBridgeConfig struct {
	Enabled bool `json:"enabled,omitempty"`
}

type StoreConfig struct {
	Enabled     bool   `json:"enabled,omitempty"`
	RegistryURL string `json:"registryUrl,omitempty"`
	AuthToken   string `json:"authToken,omitempty"`
}

type ReminderConfig struct {
	Enabled       bool   `json:"enabled,omitempty"`
	CheckInterval string `json:"checkInterval,omitempty"`
	MaxPerUser    int    `json:"maxPerUser,omitempty"`
}

func (rc ReminderConfig) CheckIntervalOrDefault() time.Duration {
	if rc.CheckInterval != "" {
		if d, err := time.ParseDuration(rc.CheckInterval); err == nil && d > 0 {
			return d
		}
	}
	return 30 * time.Second
}

func (rc ReminderConfig) MaxPerUserOrDefault() int {
	if rc.MaxPerUser > 0 {
		return rc.MaxPerUser
	}
	return 50
}

type DeviceConfig struct {
	Enabled          bool   `json:"enabled"`
	OutputDir        string `json:"outputDir,omitempty"`
	CameraEnabled    bool   `json:"camera,omitempty"`
	ScreenEnabled    bool   `json:"screen,omitempty"`
	ClipboardEnabled bool   `json:"clipboard,omitempty"`
	NotifyEnabled    bool   `json:"notify,omitempty"`
	LocationEnabled  bool   `json:"location,omitempty"`
}

type CalendarConfig struct {
	Enabled    bool   `json:"enabled"`
	CalendarID string `json:"calendarId,omitempty"`
	TimeZone   string `json:"timeZone,omitempty"`
	MaxResults int    `json:"maxResults,omitempty"`
}

type FileManagerConfig struct {
	Enabled    bool   `json:"enabled"`
	StorageDir string `json:"storageDir,omitempty"`
	MaxSizeMB  int    `json:"maxSizeMB,omitempty"`
}

// StorageDirOrDefault returns the storage directory (default baseDir/files).
func (c FileManagerConfig) StorageDirOrDefault(baseDir string) string {
	if c.StorageDir != "" {
		return c.StorageDir
	}
	return filepath.Join(baseDir, "files")
}

// MaxSizeOrDefault returns the max file size in MB (default 50).
func (c FileManagerConfig) MaxSizeOrDefault() int {
	if c.MaxSizeMB > 0 {
		return c.MaxSizeMB
	}
	return 50
}

type YouTubeConfig struct {
	Enabled   bool   `json:"enabled"`
	YtDlpPath string `json:"ytDlpPath,omitempty"`
}

func (c YouTubeConfig) YtDlpOrDefault() string {
	if c.YtDlpPath != "" {
		return c.YtDlpPath
	}
	return "yt-dlp"
}

type FamilyConfig struct {
	Enabled          bool    `json:"enabled"`
	MaxUsers         int     `json:"maxUsers,omitempty"`
	DefaultBudget    float64 `json:"defaultBudget,omitempty"`
	DefaultRateLimit int     `json:"defaultRateLimit,omitempty"`
}

func (c FamilyConfig) MaxUsersOrDefault() int {
	if c.MaxUsers > 0 {
		return c.MaxUsers
	}
	return 10
}

func (c FamilyConfig) DefaultRateLimitOrDefault() int {
	if c.DefaultRateLimit > 0 {
		return c.DefaultRateLimit
	}
	return 100
}

type TimeTrackingConfig struct {
	Enabled bool `json:"enabled"`
}

type LifecycleConfig struct {
	Enabled            bool `json:"enabled"`
	AutoHabitSuggest   bool `json:"autoHabitSuggest,omitempty"`
	AutoInsightAction  bool `json:"autoInsightAction,omitempty"`
	AutoBirthdayRemind bool `json:"autoBirthdayRemind,omitempty"`
}
