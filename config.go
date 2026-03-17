package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"tetora/internal/cli"
	"tetora/internal/config"
	"tetora/internal/log"
	"tetora/internal/migrate"
)

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

	// Resolve $ENV_VAR references in secret fields.
	resolveSecrets(&cfg)

	// Write MCP configs to temp files for --mcp-config flag.
	resolveMCPPaths(&cfg)

	// Validate config.
	validateConfig(&cfg)

	// Initialize provider registry.
	cfg.Runtime.ProviderRegistry = initProviders(&cfg)

	// Initialize circuit breaker registry.
	cfg.Runtime.CircuitRegistry = newCircuitRegistry(cfg.CircuitBreaker)

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
		if err := checkDockerAvailable(); err != nil {
			log.Warn("docker sandbox enabled but unavailable", "error", err)
		}
	}
}

// resolveEnvRef resolves a value starting with $ to the environment variable.
// Returns the original value if it doesn't start with $, or the env var value.
// Logs a warning if the env var is not set.
func resolveEnvRef(value, fieldName string) string {
	if !strings.HasPrefix(value, "$") {
		return value
	}
	envKey := value[1:]
	if envKey == "" {
		return value
	}
	envVal := os.Getenv(envKey)
	if envVal == "" {
		log.Warn("env var reference not set", "field", fieldName, "envVar", envKey)
		return ""
	}
	return envVal
}

// resolveSecrets resolves $ENV_VAR references in secret config fields.
func resolveSecrets(cfg *Config) {
	cfg.APIToken = resolveEnvRef(cfg.APIToken, "apiToken")
	cfg.Telegram.BotToken = resolveEnvRef(cfg.Telegram.BotToken, "telegram.botToken")
	if cfg.DashboardAuth.Password != "" {
		cfg.DashboardAuth.Password = resolveEnvRef(cfg.DashboardAuth.Password, "dashboardAuth.password")
	}
	if cfg.DashboardAuth.Token != "" {
		cfg.DashboardAuth.Token = resolveEnvRef(cfg.DashboardAuth.Token, "dashboardAuth.token")
	}
	for i, wh := range cfg.Webhooks {
		for k, v := range wh.Headers {
			cfg.Webhooks[i].Headers[k] = resolveEnvRef(v, fmt.Sprintf("webhooks[%d].headers.%s", i, k))
		}
	}
	for i := range cfg.Notifications {
		cfg.Notifications[i].WebhookURL = resolveEnvRef(cfg.Notifications[i].WebhookURL, fmt.Sprintf("notifications[%d].webhookUrl", i))
	}
	// Resolve TLS paths (support $ENV_VAR).
	if cfg.TLS.CertFile != "" {
		cfg.TLS.CertFile = resolveEnvRef(cfg.TLS.CertFile, "tls.certFile")
	}
	if cfg.TLS.KeyFile != "" {
		cfg.TLS.KeyFile = resolveEnvRef(cfg.TLS.KeyFile, "tls.keyFile")
	}
	// Resolve provider API keys.
	for name, pc := range cfg.Providers {
		if pc.APIKey != "" {
			pc.APIKey = resolveEnvRef(pc.APIKey, fmt.Sprintf("providers.%s.apiKey", name))
			cfg.Providers[name] = pc
		}
	}
	// Resolve incoming webhook secrets.
	for name, wh := range cfg.IncomingWebhooks {
		if wh.Secret != "" {
			wh.Secret = resolveEnvRef(wh.Secret, fmt.Sprintf("incomingWebhooks.%s.secret", name))
			cfg.IncomingWebhooks[name] = wh
		}
	}
	// Resolve Slack tokens.
	if cfg.Slack.BotToken != "" {
		cfg.Slack.BotToken = resolveEnvRef(cfg.Slack.BotToken, "slack.botToken")
	}
	if cfg.Slack.SigningSecret != "" {
		cfg.Slack.SigningSecret = resolveEnvRef(cfg.Slack.SigningSecret, "slack.signingSecret")
	}
	if cfg.Slack.AppToken != "" {
		cfg.Slack.AppToken = resolveEnvRef(cfg.Slack.AppToken, "slack.appToken")
	}
	// Resolve Discord token.
	if cfg.Discord.BotToken != "" {
		cfg.Discord.BotToken = resolveEnvRef(cfg.Discord.BotToken, "discord.botToken")
	}
	// Resolve Embedding API key.
	if cfg.Embedding.APIKey != "" {
		cfg.Embedding.APIKey = resolveEnvRef(cfg.Embedding.APIKey, "embedding.apiKey")
	}
	// Resolve WebSearch API key.
	if cfg.Tools.WebSearch.APIKey != "" {
		cfg.Tools.WebSearch.APIKey = resolveEnvRef(cfg.Tools.WebSearch.APIKey, "tools.webSearch.apiKey")
	}
	// Resolve Voice API keys.
	if cfg.Voice.STT.APIKey != "" {
		cfg.Voice.STT.APIKey = resolveEnvRef(cfg.Voice.STT.APIKey, "voice.stt.apiKey")
	}
	if cfg.Voice.TTS.APIKey != "" {
		cfg.Voice.TTS.APIKey = resolveEnvRef(cfg.Voice.TTS.APIKey, "voice.tts.apiKey")
	}
	// Resolve WhatsApp credentials.
	if cfg.WhatsApp.AccessToken != "" {
		cfg.WhatsApp.AccessToken = resolveEnvRef(cfg.WhatsApp.AccessToken, "whatsapp.accessToken")
	}
	if cfg.WhatsApp.AppSecret != "" {
		cfg.WhatsApp.AppSecret = resolveEnvRef(cfg.WhatsApp.AppSecret, "whatsapp.appSecret")
	}
	// Resolve Push VAPID keys.
	if cfg.Push.VAPIDPublicKey != "" {
		cfg.Push.VAPIDPublicKey = resolveEnvRef(cfg.Push.VAPIDPublicKey, "push.vapidPublicKey")
	}
	if cfg.Push.VAPIDPrivateKey != "" {
		cfg.Push.VAPIDPrivateKey = resolveEnvRef(cfg.Push.VAPIDPrivateKey, "push.vapidPrivateKey")
	}
	// Resolve plugin env vars.
	for name, pcfg := range cfg.Plugins {
		if len(pcfg.Env) > 0 {
			for k, v := range pcfg.Env {
				pcfg.Env[k] = resolveEnvRef(v, fmt.Sprintf("plugins.%s.env.%s", name, k))
			}
			cfg.Plugins[name] = pcfg
		}
	}
	// Resolve Vision API key.
	if cfg.Tools.Vision.APIKey != "" {
		cfg.Tools.Vision.APIKey = resolveEnvRef(cfg.Tools.Vision.APIKey, "tools.vision.apiKey")
	}
	// Resolve LINE credentials.
	if cfg.LINE.ChannelSecret != "" {
		cfg.LINE.ChannelSecret = resolveEnvRef(cfg.LINE.ChannelSecret, "line.channelSecret")
	}
	if cfg.LINE.ChannelAccessToken != "" {
		cfg.LINE.ChannelAccessToken = resolveEnvRef(cfg.LINE.ChannelAccessToken, "line.channelAccessToken")
	}
	// Resolve Matrix access token.
	if cfg.Matrix.AccessToken != "" {
		cfg.Matrix.AccessToken = resolveEnvRef(cfg.Matrix.AccessToken, "matrix.accessToken")
	}
	// Resolve Teams credentials.
	if cfg.Teams.AppID != "" {
		cfg.Teams.AppID = resolveEnvRef(cfg.Teams.AppID, "teams.appId")
	}
	if cfg.Teams.AppPassword != "" {
		cfg.Teams.AppPassword = resolveEnvRef(cfg.Teams.AppPassword, "teams.appPassword")
	}
	if cfg.Teams.TenantID != "" {
		cfg.Teams.TenantID = resolveEnvRef(cfg.Teams.TenantID, "teams.tenantId")
	}
	// Resolve Signal credentials.
	if cfg.Signal.PhoneNumber != "" {
		cfg.Signal.PhoneNumber = resolveEnvRef(cfg.Signal.PhoneNumber, "signal.phoneNumber")
	}
	// Resolve Google Chat credentials.
	if cfg.GoogleChat.ServiceAccountKey != "" {
		cfg.GoogleChat.ServiceAccountKey = resolveEnvRef(cfg.GoogleChat.ServiceAccountKey, "googleChat.serviceAccountKey")
	}
	// Resolve Home Assistant token.
	cfg.HomeAssistant.Token = resolveEnvRef(cfg.HomeAssistant.Token, "homeAssistant.token")
	// Resolve iMessage via BlueBubbles password.
	cfg.IMessage.Password = resolveEnvRef(cfg.IMessage.Password, "imessage.password")
	// Resolve OAuth secrets.
	cfg.OAuth.EncryptionKey = resolveEnvRef(cfg.OAuth.EncryptionKey, "oauth.encryptionKey")
	for name, svc := range cfg.OAuth.Services {
		svc.ClientID = resolveEnvRef(svc.ClientID, fmt.Sprintf("oauth.services.%s.clientId", name))
		svc.ClientSecret = resolveEnvRef(svc.ClientSecret, fmt.Sprintf("oauth.services.%s.clientSecret", name))
		cfg.OAuth.Services[name] = svc
	}
	// Resolve Todoist/Notion API keys.
	if cfg.TaskManager.Todoist.APIKey != "" {
		cfg.TaskManager.Todoist.APIKey = resolveEnvRef(cfg.TaskManager.Todoist.APIKey, "taskManager.todoist.apiKey")
	}
	if cfg.TaskManager.Notion.APIKey != "" {
		cfg.TaskManager.Notion.APIKey = resolveEnvRef(cfg.TaskManager.Notion.APIKey, "taskManager.notion.apiKey")
	}
}

func resolveMCPPaths(cfg *Config) {
	if len(cfg.MCPConfigs) == 0 {
		return
	}
	dir := filepath.Join(cfg.BaseDir, "mcp")
	os.MkdirAll(dir, 0o755)
	cfg.MCPPaths = make(map[string]string)
	for name, raw := range cfg.MCPConfigs {
		path := filepath.Join(dir, name+".json")
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			log.Warn("write mcp config failed", "name", name, "error", err)
			continue
		}
		cfg.MCPPaths[name] = path
	}
}

// configFileMu serializes all read-modify-write operations on the config file
// so concurrent HTTP handlers cannot interleave their reads and writes.
var configFileMu sync.Mutex

// updateConfigMCPs updates a single MCP config in config.json.
// If config is nil, the MCP entry is removed. Otherwise it is added/updated.
// Preserves all other config fields by reading/modifying/writing the raw JSON.
func updateConfigMCPs(configPath, mcpName string, config json.RawMessage) error {
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

	if config == nil {
		delete(mcps, mcpName)
	} else {
		mcps[mcpName] = config
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
	if err := os.WriteFile(configPath, append(out, '\n'), 0o644); err != nil {
		return err
	}
	// Auto-snapshot config version after MCP change.
	if cfg := tryLoadConfigForVersioning(configPath); cfg != nil {
		snapshotConfig(cfg.HistoryDB, configPath, "cli", fmt.Sprintf("mcp %s", mcpName))
	}
	return nil
}

// tryLoadConfigForVersioning is a lightweight config loader for versioning hooks.
// It only resolves historyDB path. Returns nil if loading fails.
func tryLoadConfigForVersioning(configPath string) *Config {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil
	}
	var cfg Config
	if json.Unmarshal(data, &cfg) != nil {
		return nil
	}
	cfg.BaseDir = filepath.Dir(configPath)
	if cfg.HistoryDB == "" {
		cfg.HistoryDB = "history.db"
	}
	if !filepath.IsAbs(cfg.HistoryDB) {
		cfg.HistoryDB = filepath.Join(cfg.BaseDir, cfg.HistoryDB)
	}
	return &cfg
}

// updateAgentModel updates an agent's model in config and returns the old model.
func updateAgentModel(cfg *Config, agentName, model string) (string, error) {
	ac, ok := cfg.Agents[agentName]
	if !ok {
		return "", fmt.Errorf("agent %q not found", agentName)
	}
	old := ac.Model
	ac.Model = model
	cfg.Agents[agentName] = ac
	configPath := findConfigPath()
	agentJSON, err := json.Marshal(&ac)
	if err != nil {
		return "", err
	}
	return old, cli.UpdateConfigAgents(configPath, agentName, agentJSON)
}
