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
	"time"
)

// --- Config Types ---

type Config struct {
	ClaudePath            string                     `json:"claudePath"`
	MaxConcurrent         int                        `json:"maxConcurrent"`
	DefaultModel          string                     `json:"defaultModel"`
	DefaultTimeout        string                     `json:"defaultTimeout"`
	DefaultBudget         float64                    `json:"defaultBudget"`
	DefaultPermissionMode string                     `json:"defaultPermissionMode"`
	DefaultWorkdir        string                     `json:"defaultWorkdir"`
	ListenAddr            string                     `json:"listenAddr"`
	Telegram              TelegramConfig             `json:"telegram"`
	MCPConfigs            map[string]json.RawMessage `json:"mcpConfigs"`
	MCPServers            map[string]MCPServerConfig `json:"mcpServers,omitempty"`
	Roles                 map[string]RoleConfig      `json:"roles"`
	DashboardDB           string                     `json:"dashboardDB"`
	HistoryDB             string                     `json:"historyDB"`
	JobsFile              string                     `json:"jobsFile"`
	Log                   bool                       `json:"log"`
	APIToken              string                     `json:"apiToken"`
	AllowedDirs           []string                   `json:"allowedDirs"`
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
	ConfigVersion         int                        `json:"configVersion,omitempty"`
	KnowledgeDir          string                     `json:"knowledgeDir,omitempty"` // default: baseDir/knowledge/
	Skills                []SkillConfig              `json:"skills,omitempty"`
	Session               SessionConfig              `json:"session,omitempty"`
	Pricing               map[string]ModelPricing    `json:"pricing,omitempty"`
	Estimate              EstimateConfig             `json:"estimate,omitempty"`
	Logging               LoggingConfig              `json:"logging,omitempty"`
	CircuitBreaker        CircuitBreakerConfig       `json:"circuitBreaker,omitempty"`
	FallbackProviders     []string                   `json:"fallbackProviders,omitempty"`
	SLA                   SLAConfig                  `json:"sla,omitempty"`
	OfflineQueue          OfflineQueueConfig         `json:"offlineQueue,omitempty"`
	Budgets               BudgetConfig               `json:"budgets,omitempty"`
	Reflection            ReflectionConfig           `json:"reflection,omitempty"`
	NotifyIntel           NotifyIntelConfig          `json:"notifyIntel,omitempty"`
	Trust                 TrustConfig                `json:"trust,omitempty"`
	IncomingWebhooks      map[string]IncomingWebhookConfig `json:"incomingWebhooks,omitempty"`
	Retention             RetentionConfig                  `json:"retention,omitempty"`
	Tools                 ToolConfig                       `json:"tools,omitempty"`
	Embedding             EmbeddingConfig                  `json:"embedding,omitempty"`
	Proactive             ProactiveConfig                  `json:"proactive,omitempty"`

	// Resolved at runtime (not serialized).
	baseDir      string
	mcpPaths     map[string]string
	tlsEnabled   bool
	registry     *providerRegistry
	circuits     *circuitRegistry
	toolRegistry *ToolRegistry
}

type WebhookConfig struct {
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Events  []string          `json:"events,omitempty"` // "success", "error", "timeout", "all"; empty = all
}

type TelegramConfig struct {
	Enabled     bool   `json:"enabled"`
	BotToken    string `json:"botToken"`
	ChatID      int64  `json:"chatID"`
	PollTimeout int    `json:"pollTimeout"`
}

type RoleConfig struct {
	SoulFile          string          `json:"soulFile"`
	Model             string          `json:"model"`
	Description       string          `json:"description"`
	Keywords          []string        `json:"keywords,omitempty"`          // routing keywords for smart dispatch
	PermissionMode    string          `json:"permissionMode,omitempty"`
	AllowedDirs       []string        `json:"allowedDirs,omitempty"`
	Provider          string          `json:"provider,omitempty"`
	Docker            *bool           `json:"docker,omitempty"`            // per-role Docker sandbox override
	FallbackProviders []string        `json:"fallbackProviders,omitempty"` // failover chain
	TrustLevel        string          `json:"trustLevel,omitempty"`        // "observe", "suggest", "auto" (default "auto")
	ToolPolicy        RoleToolPolicy  `json:"tools,omitempty"`             // tool access policy
	Workspace         WorkspaceConfig `json:"workspace,omitempty"`         // workspace isolation config
}

type ProviderConfig struct {
	Type    string `json:"type"`              // "claude-cli" | "openai-compatible"
	Path    string `json:"path,omitempty"`    // binary path for CLI providers
	BaseURL string `json:"baseUrl,omitempty"` // API endpoint for API providers
	APIKey  string `json:"apiKey,omitempty"`  // $ENV_VAR supported
	Model   string `json:"model,omitempty"`   // default model for this provider
}

type CostAlertConfig struct {
	DailyLimit  float64 `json:"dailyLimit"`
	WeeklyLimit float64 `json:"weeklyLimit"`
	Action      string  `json:"action"` // "warn" or "pause"
}

type DashboardAuthConfig struct {
	Enabled  bool   `json:"enabled"`           // false = no auth (default)
	Username string `json:"username,omitempty"` // basic auth user (default "admin")
	Password string `json:"password,omitempty"` // password or $ENV_VAR
	Token    string `json:"token,omitempty"`    // alternative: static token cookie
}

type QuietHoursConfig struct {
	Enabled bool   `json:"enabled"`          // false = disabled (default)
	Start   string `json:"start,omitempty"`  // "23:00" (local time)
	End     string `json:"end,omitempty"`    // "08:00" (local time)
	TZ      string `json:"tz,omitempty"`     // timezone, default local
	Digest  bool   `json:"digest,omitempty"` // true = send digest when quiet ends; false = discard
}

type DigestConfig struct {
	Enabled bool   `json:"enabled"`           // false = disabled (default)
	Time    string `json:"time,omitempty"`    // "08:00" default
	TZ      string `json:"tz,omitempty"`      // timezone, default local
}

type NotificationChannel struct {
	Type        string   `json:"type"`                 // "slack", "discord"
	WebhookURL  string   `json:"webhookUrl"`           // webhook endpoint
	Events      []string `json:"events,omitempty"`     // "all", "error", "success"; empty = all
	MinPriority string   `json:"minPriority,omitempty"` // "critical", "high", "normal", "low"; empty = all
}

type RateLimitConfig struct {
	Enabled   bool `json:"enabled"`
	MaxPerMin int  `json:"maxPerMin,omitempty"` // default 60
}

type TLSConfig struct {
	CertFile string `json:"certFile,omitempty"` // path to PEM cert file
	KeyFile  string `json:"keyFile,omitempty"`  // path to PEM key file
}

type SecurityAlertConfig struct {
	Enabled       bool `json:"enabled"`
	FailThreshold int  `json:"failThreshold,omitempty"` // N failures in window → alert (default 10)
	FailWindowMin int  `json:"failWindowMin,omitempty"` // window in minutes (default 5)
}

// SmartDispatchConfig configures the smart dispatch routing engine.
type SmartDispatchConfig struct {
	Enabled         bool          `json:"enabled"`
	Coordinator     string        `json:"coordinator,omitempty"`     // role name for LLM classification (default "琉璃")
	DefaultRole     string        `json:"defaultRole,omitempty"`     // fallback if no match (default "琉璃")
	ClassifyBudget  float64       `json:"classifyBudget,omitempty"`  // budget for classification LLM call (default 0.1)
	ClassifyTimeout string        `json:"classifyTimeout,omitempty"` // timeout for classification (default "30s")
	Review          bool          `json:"review,omitempty"`          // if true, coordinator reviews output
	ReviewBudget    float64       `json:"reviewBudget,omitempty"`    // budget for review LLM call (default 0.2)
	Rules           []RoutingRule `json:"rules,omitempty"`           // explicit keyword rules (fast path)
}

// RoutingRule is a keyword-based routing rule for fast-path matching.
type RoutingRule struct {
	Role     string   `json:"role"`                // target role name
	Keywords []string `json:"keywords"`            // case-insensitive keyword match (any = match)
	Patterns []string `json:"patterns,omitempty"`  // regex patterns (any = match)
}

// EstimateConfig configures pre-execution cost estimation.
type EstimateConfig struct {
	ConfirmThreshold    float64 `json:"confirmThreshold,omitempty"`    // cost threshold for TG confirmation (default $1.00)
	DefaultOutputTokens int     `json:"defaultOutputTokens,omitempty"` // fallback output token estimate (default 500)
}

// ToolConfig configures the tool engine.
type ToolConfig struct {
	MaxIterations  int                       `json:"maxIterations,omitempty"`  // default 10
	Timeout        int                       `json:"timeout,omitempty"`        // seconds, default 120
	Builtin        map[string]bool           `json:"builtin,omitempty"`        // tool name -> enabled
	Profiles       map[string]ToolProfile    `json:"profiles,omitempty"`       // custom profiles
	DefaultProfile string                    `json:"defaultProfile,omitempty"` // default "standard"
	TrustOverride  map[string]string         `json:"trustOverride,omitempty"`  // tool → trust level
}

// MCPServerConfig defines an MCP server managed by Tetora.
type MCPServerConfig struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Enabled *bool             `json:"enabled,omitempty"` // default true
}

// confirmThresholdOrDefault returns the configured confirm threshold (default $1.00).
func (c EstimateConfig) confirmThresholdOrDefault() float64 {
	if c.ConfirmThreshold > 0 {
		return c.ConfirmThreshold
	}
	return 1.0
}

// CircuitBreakerConfig configures the circuit breaker for provider failover.
type CircuitBreakerConfig struct {
	Enabled          bool   `json:"enabled,omitempty"`          // default true (enabled when any config present)
	FailThreshold    int    `json:"failThreshold,omitempty"`    // failures before open (default 5)
	SuccessThreshold int    `json:"successThreshold,omitempty"` // successes in half-open before close (default 2)
	OpenTimeout      string `json:"openTimeout,omitempty"`      // duration before half-open (default "30s")
}

// defaultOutputTokensOrDefault returns the configured default output tokens (default 500).
func (c EstimateConfig) defaultOutputTokensOrDefault() int {
	if c.DefaultOutputTokens > 0 {
		return c.DefaultOutputTokens
	}
	return 500
}

// SessionConfig configures channel session sync and context compaction.
type SessionConfig struct {
	ContextMessages int `json:"contextMessages,omitempty"` // max messages to inject as context (default 20)
	CompactAfter    int `json:"compactAfter,omitempty"`    // compact when message_count > N (default 30)
	CompactKeep     int `json:"compactKeep,omitempty"`     // keep last N messages after compact (default 10)
}

type LoggingConfig struct {
	Level     string `json:"level,omitempty"`     // "debug", "info", "warn", "error" (default "info")
	Format    string `json:"format,omitempty"`    // "text", "json" (default "text")
	File      string `json:"file,omitempty"`      // log file path (default baseDir/logs/tetora.log)
	MaxSizeMB int    `json:"maxSizeMB,omitempty"` // max file size before rotation in MB (default 50)
	MaxFiles  int    `json:"maxFiles,omitempty"`  // rotated files to keep (default 5)
}

func (c LoggingConfig) levelOrDefault() string {
	if c.Level != "" {
		return c.Level
	}
	return "info"
}
func (c LoggingConfig) formatOrDefault() string {
	if c.Format != "" {
		return c.Format
	}
	return "text"
}
func (c LoggingConfig) maxSizeMBOrDefault() int {
	if c.MaxSizeMB > 0 {
		return c.MaxSizeMB
	}
	return 50
}
func (c LoggingConfig) maxFilesOrDefault() int {
	if c.MaxFiles > 0 {
		return c.MaxFiles
	}
	return 5
}

// ProactiveConfig configures the proactive agent engine.
type ProactiveConfig struct {
	Enabled bool             `json:"enabled,omitempty"`
	Rules   []ProactiveRule  `json:"rules,omitempty"`
}

// sessionContextMessages returns the configured max context messages (default 20).
func (c SessionConfig) contextMessagesOrDefault() int {
	if c.ContextMessages > 0 {
		return c.ContextMessages
	}
	return 20
}

// compactAfterOrDefault returns the configured compact threshold (default 30).
func (c SessionConfig) compactAfterOrDefault() int {
	if c.CompactAfter > 0 {
		return c.CompactAfter
	}
	return 30
}

// compactKeepOrDefault returns the number of messages to keep after compaction (default 10).
func (c SessionConfig) compactKeepOrDefault() int {
	if c.CompactKeep > 0 {
		return c.CompactKeep
	}
	return 10
}

// --- Config Loading ---

func loadConfig(path string) *Config {
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
	autoMigrateConfig(path)

	data, err := os.ReadFile(path)
	if err != nil {
		logError("read config failed", "path", path, "error", err)
		os.Exit(1)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		logError("parse config failed", "path", path, "error", err)
		os.Exit(1)
	}

	cfg.baseDir = filepath.Dir(path)

	// Defaults.
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 3
	}
	if cfg.DefaultModel == "" {
		cfg.DefaultModel = "sonnet"
	}
	if cfg.DefaultTimeout == "" {
		cfg.DefaultTimeout = "15m"
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

	// Smart dispatch defaults.
	if cfg.SmartDispatch.Coordinator == "" {
		cfg.SmartDispatch.Coordinator = "琉璃"
	}
	if cfg.SmartDispatch.DefaultRole == "" {
		cfg.SmartDispatch.DefaultRole = "琉璃"
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
		cfg.KnowledgeDir = filepath.Join(cfg.baseDir, "knowledge")
	}
	if !filepath.IsAbs(cfg.KnowledgeDir) {
		cfg.KnowledgeDir = filepath.Join(cfg.baseDir, cfg.KnowledgeDir)
	}

	// Resolve relative paths to config dir.
	if !filepath.IsAbs(cfg.JobsFile) {
		cfg.JobsFile = filepath.Join(cfg.baseDir, cfg.JobsFile)
	}
	if !filepath.IsAbs(cfg.HistoryDB) {
		cfg.HistoryDB = filepath.Join(cfg.baseDir, cfg.HistoryDB)
	}
	if cfg.DefaultWorkdir != "" && !filepath.IsAbs(cfg.DefaultWorkdir) {
		cfg.DefaultWorkdir = filepath.Join(cfg.baseDir, cfg.DefaultWorkdir)
	}

	// Resolve TLS paths relative to config dir.
	if cfg.TLS.CertFile != "" && !filepath.IsAbs(cfg.TLS.CertFile) {
		cfg.TLS.CertFile = filepath.Join(cfg.baseDir, cfg.TLS.CertFile)
	}
	if cfg.TLS.KeyFile != "" && !filepath.IsAbs(cfg.TLS.KeyFile) {
		cfg.TLS.KeyFile = filepath.Join(cfg.baseDir, cfg.TLS.KeyFile)
	}
	cfg.tlsEnabled = cfg.TLS.CertFile != "" && cfg.TLS.KeyFile != ""

	// Resolve Telegram token from OpenClaw config if empty.
	if cfg.Telegram.BotToken == "" {
		cfg.Telegram.BotToken = readOpenClawTelegramToken()
	}

	// Resolve $ENV_VAR references in secret fields.
	cfg.resolveSecrets()

	// Write MCP configs to temp files for --mcp-config flag.
	cfg.resolveMCPPaths()

	// Validate config.
	cfg.validate()

	// Initialize provider registry.
	cfg.registry = initProviders(&cfg)

	// Initialize circuit breaker registry.
	cfg.circuits = newCircuitRegistry(cfg.CircuitBreaker)

	return &cfg
}

// validate checks config values and logs warnings for common mistakes.
func (cfg *Config) validate() {
	// Check claude binary exists.
	claudePath := cfg.ClaudePath
	if claudePath == "" {
		claudePath = "claude"
	}
	if _, err := exec.LookPath(claudePath); err != nil {
		logWarn("claude binary not found, tasks will fail", "path", claudePath)
	}

	// Validate listen address format.
	if cfg.ListenAddr != "" {
		parts := strings.SplitN(cfg.ListenAddr, ":", 2)
		if len(parts) != 2 {
			logWarn("listenAddr should be host:port", "listenAddr", cfg.ListenAddr, "example", "127.0.0.1:7777")
		} else if _, err := strconv.Atoi(parts[1]); err != nil {
			logWarn("listenAddr port is not a valid number", "port", parts[1])
		}
	}

	// Validate default timeout is parseable.
	if cfg.DefaultTimeout != "" {
		if _, err := time.ParseDuration(cfg.DefaultTimeout); err != nil {
			logWarn("defaultTimeout is not a valid duration", "defaultTimeout", cfg.DefaultTimeout, "example", "15m, 1h")
		}
	}

	// Validate MaxConcurrent is reasonable.
	if cfg.MaxConcurrent > 20 {
		logWarn("maxConcurrent is very high, claude sessions are resource-intensive", "maxConcurrent", cfg.MaxConcurrent)
	}

	// Warn if API token is empty.
	if cfg.APIToken == "" {
		logWarn("apiToken is empty, API endpoints are unauthenticated")
	}

	// Validate default workdir exists.
	if cfg.DefaultWorkdir != "" {
		if _, err := os.Stat(cfg.DefaultWorkdir); err != nil {
			logWarn("defaultWorkdir does not exist", "path", cfg.DefaultWorkdir)
		}
	}

	// Validate TLS cert/key files.
	if cfg.tlsEnabled {
		if _, err := os.Stat(cfg.TLS.CertFile); err != nil {
			logWarn("tls.certFile does not exist", "path", cfg.TLS.CertFile)
		}
		if _, err := os.Stat(cfg.TLS.KeyFile); err != nil {
			logWarn("tls.keyFile does not exist", "path", cfg.TLS.KeyFile)
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
				logWarn("provider binary not found", "provider", name, "path", path)
			}
		case "openai-compatible":
			if pc.BaseURL == "" {
				logWarn("provider has no baseUrl", "provider", name)
			}
		default:
			logWarn("provider has unknown type", "provider", name, "type", pc.Type)
		}
	}

	// Validate allowedIPs format.
	for _, entry := range cfg.AllowedIPs {
		if !strings.Contains(entry, "/") {
			if net.ParseIP(entry) == nil {
				logWarn("allowedIPs entry is not a valid IP address", "entry", entry)
			}
		} else {
			if _, _, err := net.ParseCIDR(entry); err != nil {
				logWarn("allowedIPs entry is not a valid CIDR", "entry", entry, "error", err)
			}
		}
	}

	// Validate smart dispatch config.
	if cfg.SmartDispatch.Enabled {
		if _, ok := cfg.Roles[cfg.SmartDispatch.Coordinator]; !ok && cfg.SmartDispatch.Coordinator != "" {
			logWarn("smartDispatch.coordinator role not found in roles", "coordinator", cfg.SmartDispatch.Coordinator)
		}
		for _, rule := range cfg.SmartDispatch.Rules {
			if _, ok := cfg.Roles[rule.Role]; !ok {
				logWarn("smartDispatch rule references unknown role", "role", rule.Role)
			}
		}
	}

	// Validate Docker sandbox config.
	if cfg.Docker.Enabled {
		if cfg.Docker.Image == "" {
			logWarn("docker.enabled=true but docker.image is empty")
		}
		if err := checkDockerAvailable(); err != nil {
			logWarn("docker sandbox enabled but unavailable", "error", err)
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
		logWarn("env var reference not set", "field", fieldName, "envVar", envKey)
		return ""
	}
	return envVal
}

// resolveSecrets resolves $ENV_VAR references in secret config fields.
func (cfg *Config) resolveSecrets() {
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
}

func (cfg *Config) resolveMCPPaths() {
	if len(cfg.MCPConfigs) == 0 {
		return
	}
	dir := filepath.Join(cfg.baseDir, "mcp")
	os.MkdirAll(dir, 0o755)
	cfg.mcpPaths = make(map[string]string)
	for name, raw := range cfg.MCPConfigs {
		path := filepath.Join(dir, name+".json")
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			logWarn("write mcp config failed", "name", name, "error", err)
			continue
		}
		cfg.mcpPaths[name] = path
	}
}

// updateConfigMCPs updates a single MCP config in config.json.
// If config is nil, the MCP entry is removed. Otherwise it is added/updated.
// Preserves all other config fields by reading/modifying/writing the raw JSON.
func updateConfigMCPs(configPath, mcpName string, config json.RawMessage) error {
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
	cfg.baseDir = filepath.Dir(configPath)
	if cfg.HistoryDB == "" {
		cfg.HistoryDB = "history.db"
	}
	if !filepath.IsAbs(cfg.HistoryDB) {
		cfg.HistoryDB = filepath.Join(cfg.baseDir, cfg.HistoryDB)
	}
	return &cfg
}

func readOpenClawTelegramToken() string {
	home, _ := os.UserHomeDir()
	path := filepath.Join(home, ".openclaw", "openclaw.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var oc struct {
		Channels struct {
			Telegram struct {
				BotToken string `json:"botToken"`
			} `json:"telegram"`
		} `json:"channels"`
	}
	if json.Unmarshal(data, &oc) == nil {
		return oc.Channels.Telegram.BotToken
	}
	return ""
}
