package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// CLIConfig is a lightweight config struct with only CLI-relevant fields.
// It's JSON-compatible with the root Config but doesn't include runtime-only
// fields like circuit breakers, tool registries, provider instances, etc.
type CLIConfig struct {
	ClaudePath            string                     `json:"claudePath"`
	MaxConcurrent         int                        `json:"maxConcurrent"`
	DefaultModel          string                     `json:"defaultModel"`
	DefaultTimeout        string                     `json:"defaultTimeout"`
	DefaultBudget         float64                    `json:"defaultBudget"`
	DefaultPermissionMode string                     `json:"defaultPermissionMode"`
	DefaultAgent          string                     `json:"defaultAgent,omitempty"`
	DefaultWorkdir        string                     `json:"defaultWorkdir"`
	ListenAddr            string                     `json:"listenAddr"`
	Agents                map[string]AgentInfo       `json:"agents"`
	HistoryDB             string                     `json:"historyDB"`
	JobsFile              string                     `json:"jobsFile"`
	APIToken              string                     `json:"apiToken"`
	AllowedDirs           []string                   `json:"allowedDirs"`
	DefaultAddDirs        []string                   `json:"defaultAddDirs,omitempty"`
	QuietHours            QuietHoursInfo             `json:"quietHours"`
	AgentsDir             string                     `json:"agentsDir,omitempty"`
	WorkspaceDir          string                     `json:"workspaceDir,omitempty"`
	RuntimeDir            string                     `json:"runtimeDir,omitempty"`
	KnowledgeDir          string                     `json:"knowledgeDir,omitempty"`
	VaultDir              string                     `json:"vaultDir,omitempty"`
	Providers             map[string]json.RawMessage `json:"providers,omitempty"`
	DefaultProvider       string                     `json:"defaultProvider,omitempty"`
	DiskBudgetGB          float64                    `json:"diskBudgetGB,omitempty"`
	ConfigVersion         int                        `json:"configVersion,omitempty"`
	MCPServers            map[string]json.RawMessage `json:"mcpServers,omitempty"`
	MCPConfigs            map[string]json.RawMessage `json:"mcpConfigs,omitempty"`
	Budgets               BudgetInfo                 `json:"budgets,omitempty"`
	DashboardAuth         json.RawMessage            `json:"dashboardAuth,omitempty"`
	Logging               LoggingInfo                `json:"logging,omitempty"`
	Skills                json.RawMessage            `json:"skills,omitempty"`
	Webhooks              json.RawMessage            `json:"webhooks,omitempty"`
	IncomingWebhooks      map[string]json.RawMessage `json:"incomingWebhooks,omitempty"`
	Trust                 json.RawMessage            `json:"trust,omitempty"`
	Retention             json.RawMessage            `json:"retention,omitempty"`
	Security              json.RawMessage            `json:"security,omitempty"`
	Telegram              ChannelInfo                `json:"telegram,omitempty"`
	Discord               ChannelInfo                `json:"discord,omitempty"`
	Slack                 ChannelInfo                `json:"slack,omitempty"`
	WhatsApp              ChannelInfo                `json:"whatsapp,omitempty"`
	LINE                  ChannelInfo                `json:"line,omitempty"`
	Matrix                ChannelInfo                `json:"matrix,omitempty"`
	Teams                 ChannelInfo                `json:"teams,omitempty"`
	Signal                ChannelInfo                `json:"signal,omitempty"`
	GoogleChat            ChannelInfo                `json:"googleChat,omitempty"`
	IMessage              ChannelInfo                `json:"imessage,omitempty"`
	Heartbeat             HeartbeatInfo              `json:"heartbeat,omitempty"`
	Session               json.RawMessage            `json:"session,omitempty"`
	SmartDispatch         json.RawMessage            `json:"smartDispatch,omitempty"`
	Usage                 json.RawMessage            `json:"usage,omitempty"`
	OAuth                 json.RawMessage            `json:"oauth,omitempty"`
	Notifications         []NotificationChannel      `json:"notifications,omitempty"`
	EncryptionKey         string                     `json:"encryptionKey,omitempty"`
	ClientsDir            string                     `json:"clientsDir,omitempty"`
	DefaultClientID       string                     `json:"defaultClientID,omitempty"`

	// Resolved paths (not from JSON).
	BaseDir    string `json:"-"`
	ConfigPath string `json:"-"`
}

// AgentInfo is a CLI-local version of AgentConfig.
// All JSON fields match root's AgentConfig for round-trip fidelity.
type AgentInfo struct {
	SoulFile          string        `json:"soulFile"`
	Model             string        `json:"model"`
	Description       string        `json:"description"`
	Keywords          []string      `json:"keywords,omitempty"`
	PermissionMode    string        `json:"permissionMode,omitempty"`
	AllowedDirs       []string      `json:"allowedDirs,omitempty"`
	Provider          string        `json:"provider,omitempty"`
	Docker            *bool         `json:"docker,omitempty"`
	FallbackProviders []string      `json:"fallbackProviders,omitempty"`
	TrustLevel        string        `json:"trustLevel,omitempty"`
	ToolPolicy        json.RawMessage `json:"tools,omitempty"`
	ToolProfile       string        `json:"toolProfile,omitempty"`
	Workspace         WorkspaceInfo `json:"workspace,omitempty"`
}

// WorkspaceInfo mirrors WorkspaceConfig for CLI display.
type WorkspaceInfo struct {
	Dir        string          `json:"dir,omitempty"`
	SoulFile   string          `json:"soulFile,omitempty"`
	MCPServers []string        `json:"mcpServers,omitempty"`
	Sandbox    json.RawMessage `json:"sandbox,omitempty"`
}

// QuietHoursInfo mirrors QuietHoursConfig.
type QuietHoursInfo struct {
	Enabled bool   `json:"enabled"`
	Start   string `json:"start,omitempty"`
	End     string `json:"end,omitempty"`
	TZ      string `json:"tz,omitempty"`
	Digest  bool   `json:"digest,omitempty"`
}

// BudgetInfo mirrors BudgetConfig for CLI budget display.
type BudgetInfo struct {
	Global        BudgetLimits            `json:"global,omitempty"`
	Agents        map[string]BudgetLimits `json:"agents,omitempty"`
	AutoDowngrade json.RawMessage         `json:"autoDowngrade,omitempty"`
	Paused        bool                    `json:"paused,omitempty"`
}

type BudgetLimits struct {
	Daily   float64 `json:"daily,omitempty"`
	Weekly  float64 `json:"weekly,omitempty"`
	Monthly float64 `json:"monthly,omitempty"`
}

// NotificationChannel represents a notification channel (Discord webhook, Slack, etc.).
type NotificationChannel struct {
	Name        string   `json:"name,omitempty"`
	Type        string   `json:"type"`
	WebhookURL  string   `json:"webhookUrl"`
	Events      []string `json:"events,omitempty"`
	MinPriority string   `json:"minPriority,omitempty"`
}

// ChannelInfo is a minimal struct for checking if a channel is configured.
type ChannelInfo struct {
	Enabled  bool   `json:"enabled"`
	BotToken string `json:"botToken,omitempty"`
	ChatID   int64  `json:"chatID,omitempty"`
}

// HeartbeatInfo mirrors HeartbeatConfig.
type HeartbeatInfo struct {
	Enabled bool `json:"enabled"`
}

// LoggingInfo mirrors LoggingConfig.
type LoggingInfo struct {
	Level string `json:"level,omitempty"`
	File  string `json:"file,omitempty"`
}

// LoadCLIConfig loads config from path with CLI-essential defaults and path resolution.
// Unlike root's tryLoadConfig, this does NOT initialize providers, circuit breakers,
// tool registries, MCP paths, or secret resolution.
func LoadCLIConfig(path string) *CLIConfig {
	cfg, err := TryLoadCLIConfig(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}
	return cfg
}

// TryLoadCLIConfig loads config without exiting on error.
func TryLoadCLIConfig(path string) (*CLIConfig, error) {
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

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg CLIConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	cfg.BaseDir = filepath.Dir(path)
	cfg.ConfigPath = path

	// Essential defaults.
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = "127.0.0.1:7777"
	}
	if cfg.HistoryDB == "" {
		cfg.HistoryDB = "history.db"
	}
	if cfg.JobsFile == "" {
		cfg.JobsFile = "jobs.json"
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
	if cfg.DefaultProvider == "" {
		cfg.DefaultProvider = "claude"
	}

	// Resolve relative paths.
	cfg.HistoryDB = absPath(cfg.BaseDir, cfg.HistoryDB)
	cfg.JobsFile = absPath(cfg.BaseDir, cfg.JobsFile)
	if cfg.DefaultWorkdir != "" {
		cfg.DefaultWorkdir = absPath(cfg.BaseDir, cfg.DefaultWorkdir)
	}

	// Multi-tenant defaults.
	cfg.ClientsDir = resolveDir(cfg.BaseDir, cfg.ClientsDir, "clients")
	if cfg.DefaultClientID == "" {
		cfg.DefaultClientID = "cli_default"
	}

	// Directory defaults.
	cfg.AgentsDir = resolveDir(cfg.BaseDir, cfg.AgentsDir, "agents")
	cfg.WorkspaceDir = resolveDir(cfg.BaseDir, cfg.WorkspaceDir, "workspace")
	cfg.RuntimeDir = resolveDir(cfg.BaseDir, cfg.RuntimeDir, "runtime")
	cfg.KnowledgeDir = resolveDir(cfg.BaseDir, cfg.KnowledgeDir, "knowledge")
	cfg.VaultDir = resolveDir(cfg.BaseDir, cfg.VaultDir, "vault")

	return &cfg, nil
}

// absPath resolves a path relative to baseDir if not already absolute.
func absPath(baseDir, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(baseDir, p)
}

// resolveDir resolves a directory path with a default name relative to baseDir.
func resolveDir(baseDir, dir, defaultName string) string {
	if dir == "" {
		return filepath.Join(baseDir, defaultName)
	}
	if filepath.IsAbs(dir) {
		return dir
	}
	return filepath.Join(baseDir, dir)
}

// HistoryDBFor returns the history DB path for a given client.
func (cfg *CLIConfig) HistoryDBFor(clientID string) string {
	return filepath.Join(cfg.ClientsDir, clientID, "dbs", "history.db")
}

// NewAPIClientFromConfig creates an API client from CLIConfig.
func (cfg *CLIConfig) NewAPIClient() *APIClient {
	return NewAPIClient(cfg.ListenAddr, cfg.APIToken)
}
