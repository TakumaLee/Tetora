// Package config defines the central Config struct and all configuration types
// used across Tetora. This enables internal packages to reference config types
// without depending on the root (package main) module.
package config

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"sync"

	// Type aliases for configs defined in internal packages.
	"tetora/internal/cost"
	"tetora/internal/estimate"
	"tetora/internal/integration/gmail"
	"tetora/internal/integration/homeassistant"
	"tetora/internal/integration/notes"
	"tetora/internal/integration/podcast"
	"tetora/internal/integration/spotify"
	"tetora/internal/integration/twitter"
	"tetora/internal/messaging/gchat"
	"tetora/internal/messaging/imessage"
	"tetora/internal/messaging/line"
	"tetora/internal/messaging/matrix"
	"tetora/internal/messaging/signal"
	"tetora/internal/messaging/slack"
	"tetora/internal/messaging/teams"
	tgbot "tetora/internal/messaging/telegram"
	"tetora/internal/messaging/whatsapp"
	"tetora/internal/quickaction"
	"tetora/internal/skill"
	"tetora/internal/sla"
	"tetora/internal/sprite"
)

// --- Type aliases for configs already defined in internal packages ---

type SLAConfig = sla.SLAConfig
type BudgetConfig = cost.BudgetConfig
type AutoDowngradeConfig = cost.AutoDowngradeConfig
type ModelPricing = estimate.ModelPricing
type SkillConfig = skill.SkillConfig
type SkillStoreConfig = skill.SkillStoreConfig
type SpriteConfig = sprite.Config
type QuickAction = quickaction.Action
type QuickActionParam = quickaction.Param

// Messaging platform configs.
type TelegramConfig = tgbot.Config
type MatrixConfig = matrix.Config
type WhatsAppConfig = whatsapp.Config
type SignalConfig = signal.Config
type GoogleChatConfig = gchat.Config
type LINEConfig = line.Config
type TeamsConfig = teams.Config
type IMessageConfig = imessage.Config
type SlackBotConfig = slack.Config

// Integration configs.
type GmailConfig = gmail.Config
type SpotifyConfig = spotify.Config
type TwitterConfig = twitter.Config
type PodcastConfig = podcast.Config
type HomeAssistantConfig = homeassistant.Config
type NotesConfig = notes.Config

// --- Config ---

// Config is the central configuration for the entire Tetora application.
// JSON fields are deserialized from config.json; runtime fields are set during startup.
type Config struct {
	ClaudePath            string                     `json:"claudePath"`
	MaxConcurrent         int                        `json:"maxConcurrent"`
	DefaultModel          string                     `json:"defaultModel"`
	DefaultTimeout        string                     `json:"defaultTimeout"`
	DefaultBudget         float64                    `json:"defaultBudget"`
	DefaultPermissionMode string                     `json:"defaultPermissionMode"`
	DefaultAgent          string                     `json:"defaultAgent,omitempty"`
	DefaultWorkdir        string                     `json:"defaultWorkdir"`
	ListenAddr            string                     `json:"listenAddr"`
	Telegram              TelegramConfig             `json:"telegram"`
	MCPConfigs            map[string]json.RawMessage `json:"mcpConfigs"`
	MCPServers            map[string]MCPServerConfig `json:"mcpServers,omitempty"`
	Agents                map[string]AgentConfig     `json:"agents"`
	HistoryDB             string                     `json:"historyDB"`
	JobsFile              string                     `json:"jobsFile"`
	Log                   bool                       `json:"log"`
	APIToken              string                     `json:"apiToken"`
	AllowedDirs           []string                   `json:"allowedDirs"`
	DefaultAddDirs        []string                   `json:"defaultAddDirs,omitempty"`
	CostAlert             CostAlertConfig            `json:"costAlert"`
	Webhooks              []WebhookConfig            `json:"webhooks"`
	DashboardAuth         DashboardAuthConfig        `json:"dashboardAuth"`
	QuietHours            QuietHoursConfig           `json:"quietHours"`
	Digest                DigestConfig               `json:"digest"`
	Notifications         []NotificationChannel      `json:"notifications,omitempty"`
	RateLimit             RateLimitConfig            `json:"rateLimit,omitempty"`
	TLS                   TLSConfig                  `json:"tls,omitempty"`
	SecurityAlert         SecurityAlertConfig        `json:"securityAlert,omitempty"`
	AllowedIPs            []string                   `json:"allowedIPs,omitempty"`
	MaxPromptLen          int                        `json:"maxPromptLen,omitempty"`
	Providers             map[string]ProviderConfig  `json:"providers,omitempty"`
	DefaultProvider       string                     `json:"defaultProvider,omitempty"`
	Docker                DockerConfig               `json:"docker,omitempty"`
	SmartDispatch         SmartDispatchConfig        `json:"smartDispatch,omitempty"`
	Slack                 SlackBotConfig             `json:"slack,omitempty"`
	Discord               DiscordBotConfig           `json:"discord,omitempty"`
	WhatsApp              WhatsAppConfig             `json:"whatsapp,omitempty"`
	LINE                  LINEConfig                 `json:"line,omitempty"`
	Matrix                MatrixConfig               `json:"matrix,omitempty"`
	Teams                 TeamsConfig                `json:"teams,omitempty"`
	Signal                SignalConfig               `json:"signal,omitempty"`
	GoogleChat            GoogleChatConfig           `json:"googleChat,omitempty"`
	ConfigVersion         int                        `json:"configVersion,omitempty"`
	KnowledgeDir          string                     `json:"knowledgeDir,omitempty"`
	AgentsDir             string                     `json:"agentsDir,omitempty"`
	WorkspaceDir          string                     `json:"workspaceDir,omitempty"`
	AgentOutputBase       string                     `json:"agentOutputBase,omitempty"` // Base dir for agent outputs (~/.tetora/workspace/agents)
	RuntimeDir            string                     `json:"runtimeDir,omitempty"`
	VaultDir              string                     `json:"vaultDir,omitempty"`
	Skills                []SkillConfig              `json:"skills,omitempty"`
	Session               SessionConfig              `json:"session,omitempty"`
	Pricing               map[string]ModelPricing    `json:"pricing,omitempty"`
	Estimate              EstimateConfig             `json:"estimate,omitempty"`
	Logging               LoggingConfig              `json:"logging,omitempty"`
	CircuitBreaker        CircuitBreakerConfig       `json:"circuitBreaker,omitempty"`
	FallbackProviders     []string                   `json:"fallbackProviders,omitempty"`
	InferenceMode         string                     `json:"inferenceMode,omitempty"` // "cloud" | "local" | "" (mixed)
	ClaudeProvider        string                     `json:"claudeProvider,omitempty"` // "claude-code" | "anthropic" — how to run Claude models
	SLA                   SLAConfig                  `json:"sla,omitempty"`
	OfflineQueue          OfflineQueueConfig         `json:"offlineQueue,omitempty"`
	Budgets               BudgetConfig               `json:"budgets,omitempty"`
	DiskBudgetGB          float64                    `json:"diskBudgetGB,omitempty"`
	DiskWarnMB            int                        `json:"diskWarnMB,omitempty"`
	DiskBlockMB           int                        `json:"diskBlockMB,omitempty"`
	Reflection            ReflectionConfig           `json:"reflection,omitempty"`
	NotifyIntel           NotifyIntelConfig          `json:"notifyIntel,omitempty"`
	Trust                 TrustConfig                `json:"trust,omitempty"`
	IncomingWebhooks      map[string]IncomingWebhookConfig `json:"incomingWebhooks,omitempty"`
	Retention             RetentionConfig                  `json:"retention,omitempty"`
	Tools                 ToolConfig                       `json:"tools,omitempty"`
	Embedding             EmbeddingConfig                  `json:"embedding,omitempty"`
	Proactive             ProactiveConfig                  `json:"proactive,omitempty"`
	GroupChat             GroupChatConfig                  `json:"groupChat,omitempty"`
	QuickActions          []QuickAction                    `json:"quickActions,omitempty"`
	Voice                 VoiceConfig                      `json:"voice,omitempty"`
	Push                  PushConfig                       `json:"push,omitempty"`
	AccessControl         AccessControlConfig              `json:"accessControl,omitempty"`
	AgentComm             AgentCommConfig                  `json:"agentComm,omitempty"`
	SlotPressure          SlotPressureConfig               `json:"slotPressure,omitempty"`
	Canvas                CanvasConfig                     `json:"canvas,omitempty"`
	Plugins               map[string]PluginConfig          `json:"plugins,omitempty"`
	Sandbox               SandboxConfig                    `json:"sandbox,omitempty"`
	TaskBoard             TaskBoardConfig                  `json:"taskBoard,omitempty"`
	Review                ReviewConfig                     `json:"review,omitempty"`
	Security              SecurityConfig                   `json:"security,omitempty"`
	DailyNotes            DailyNotesConfig                 `json:"dailyNotes,omitempty"`
	WarRoomAutoUpdate     WarRoomAutoUpdateConfig          `json:"warRoomAutoUpdate,omitempty"`
	SkillStore            SkillStoreConfig                 `json:"skillStore,omitempty"`
	SkillsOnDemand        SkillsOnDemandConfig             `json:"skillsOnDemand,omitempty"`
	Usage                 UsageConfig                      `json:"usage,omitempty"`
	WorkflowTriggers      []WorkflowTriggerConfig          `json:"workflowTriggers,omitempty"`
	OAuth                 OAuthConfig                      `json:"oauth,omitempty"`
	Reminders             ReminderConfig                   `json:"reminders,omitempty"`
	Notes                 NotesConfig                      `json:"notes,omitempty"`
	HomeAssistant         HomeAssistantConfig              `json:"homeAssistant,omitempty"`
	Device                DeviceConfig                     `json:"device,omitempty"`
	IMessage              IMessageConfig                   `json:"imessage,omitempty"`
	Gmail                 GmailConfig                      `json:"gmail,omitempty"`
	Calendar              CalendarConfig                   `json:"calendar,omitempty"`
	Twitter               TwitterConfig                    `json:"twitter,omitempty"`
	WritingStyle          WritingStyleConfig               `json:"writingStyle,omitempty"`
	Citation              CitationConfig                   `json:"citation,omitempty"`
	BrowserRelay          BrowserRelayConfig               `json:"browserRelay,omitempty"`
	NotebookLM            NotebookLMConfig                 `json:"notebookLM,omitempty"`
	ImageGen              ImageGenConfig                   `json:"imageGen,omitempty"`
	Weather               WeatherConfig                    `json:"weather,omitempty"`
	Currency              CurrencyConfig                   `json:"currency,omitempty"`
	RSS                   RSSConfig                        `json:"rss,omitempty"`
	Translate             TranslateConfig                  `json:"translate,omitempty"`
	UserProfile           UserProfileConfig                `json:"userProfile,omitempty"`
	Ops                   OpsConfig                        `json:"ops,omitempty"`
	Finance               FinanceConfig                    `json:"finance,omitempty"`
	TaskManager           TaskManagerConfig                `json:"taskManager,omitempty"`
	FileManager           FileManagerConfig                `json:"fileManager,omitempty"`
	Spotify               SpotifyConfig                    `json:"spotify,omitempty"`
	YouTube               YouTubeConfig                    `json:"youtube,omitempty"`
	Podcast               PodcastConfig                    `json:"podcast,omitempty"`
	Family                FamilyConfig                     `json:"family,omitempty"`
	EncryptionKey         string                           `json:"encryptionKey,omitempty"`
	StreamToChannels      bool                             `json:"streamToChannels,omitempty"`
	ApprovalGates         ApprovalGateConfig               `json:"approvalGates,omitempty"`
	TimeTracking          TimeTrackingConfig               `json:"timeTracking,omitempty"`
	Lifecycle             LifecycleConfig                  `json:"lifecycle,omitempty"`
	PromptBudget          PromptBudgetConfig               `json:"promptBudget,omitempty"`
	PromptCapture         PromptCaptureConfig              `json:"promptCapture,omitempty"`
	CronNotify            *bool                            `json:"cronNotify,omitempty"`
	CronReplayHours       int                              `json:"cronReplayHours,omitempty"`
	Heartbeat             HeartbeatConfig                  `json:"heartbeat,omitempty"`
	Watchdog              WatchdogConfig                   `json:"watchdog,omitempty"`
	Hooks                 HooksConfig                      `json:"hooks,omitempty"`
	PlanGate              PlanGateConfig                   `json:"planGate,omitempty"`
	MCPBridge             MCPBridgeConfig                  `json:"mcpBridge,omitempty"`
	Store                 StoreConfig                      `json:"store,omitempty"`

	// Multi-tenant isolation (Phase 1).
	ClientsDir      string `json:"clientsDir,omitempty"`
	DefaultClientID string `json:"defaultClientID,omitempty"`

	// Runtime fields — set after load, not serialized.
	BaseDir         string            `json:"-"`
	MCPMu           sync.RWMutex     `json:"-"`
	MCPPaths        map[string]string `json:"-"`
	TLSEnabled      bool              `json:"-"`
	RuntimeNotifyFn func(string)      `json:"-"` // set at startup for scan notifications

	// Runtime service references — typed as any because the concrete types
	// are in root (package main). Root code sets these during startup and
	// accesses them via typed helper functions.
	Runtime RuntimeState `json:"-"`
}

// RuntimeState holds runtime service references that are set after config loading.
// These use any type because the concrete types are defined in root (package main).
type RuntimeState struct {
	ProviderRegistry  any
	CircuitRegistry   any
	ToolRegistry      any
	SlotPressureGuard any
	DiscordBot        any
	HookRecv          any
}

// UnmarshalJSON implements backward compat: accepts both "roles" and "agents" keys.
func (c *Config) UnmarshalJSON(data []byte) error {
	type ConfigAlias Config
	var alias ConfigAlias
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}

	if len(alias.Agents) == 0 {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err == nil {
			if rolesRaw, ok := raw["roles"]; ok {
				var roles map[string]AgentConfig
				if err := json.Unmarshal(rolesRaw, &roles); err == nil && len(roles) > 0 {
					alias.Agents = roles
				}
			}
		}
	}

	srcVal := reflect.ValueOf(&alias).Elem()
	dstType := reflect.TypeOf((*Config)(nil)).Elem()
	reflect.ValueOf(c).Elem().Set(srcVal.Convert(dstType))
	return nil
}

// --- Multi-tenant helpers ---

// ClientDir returns the root directory for a given client.
func (c *Config) ClientDir(clientID string) string {
	return filepath.Join(c.ClientsDir, clientID)
}

// ClientDBDir returns the database directory for a given client.
func (c *Config) ClientDBDir(clientID string) string {
	return filepath.Join(c.ClientsDir, clientID, "dbs")
}

// HistoryDBFor returns the history DB path for a given client.
func (c *Config) HistoryDBFor(clientID string) string {
	return filepath.Join(c.ClientsDir, clientID, "dbs", "history.db")
}

// TaskboardDBFor returns the taskboard DB path for a given client.
func (c *Config) TaskboardDBFor(clientID string) string {
	return filepath.Join(c.ClientsDir, clientID, "dbs", "taskboard.db")
}

// OutputsDirFor returns the task output directory for a given client.
// For the default client (or when ClientsDir is unset), returns BaseDir so existing
// behavior is preserved. Non-default clients write to their own client dir.
func (c *Config) OutputsDirFor(clientID string) string {
	if clientID == "" || clientID == c.DefaultClientID || c.ClientsDir == "" {
		return c.BaseDir
	}
	return c.ClientDir(clientID)
}
