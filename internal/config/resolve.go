package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tetora/internal/log"
)

// ResolveEnvRef resolves a value starting with $ to the environment variable.
// Returns the original value if it doesn't start with $, or the env var value.
// Logs a warning if the env var is not set.
func ResolveEnvRef(value, fieldName string) string {
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

// ResolveSecrets resolves $ENV_VAR references in secret config fields.
func ResolveSecrets(cfg *Config) {
	cfg.APIToken = ResolveEnvRef(cfg.APIToken, "apiToken")
	cfg.Telegram.BotToken = ResolveEnvRef(cfg.Telegram.BotToken, "telegram.botToken")
	if cfg.DashboardAuth.Password != "" {
		cfg.DashboardAuth.Password = ResolveEnvRef(cfg.DashboardAuth.Password, "dashboardAuth.password")
	}
	if cfg.DashboardAuth.Token != "" {
		cfg.DashboardAuth.Token = ResolveEnvRef(cfg.DashboardAuth.Token, "dashboardAuth.token")
	}
	for i, wh := range cfg.Webhooks {
		for k, v := range wh.Headers {
			cfg.Webhooks[i].Headers[k] = ResolveEnvRef(v, fmt.Sprintf("webhooks[%d].headers.%s", i, k))
		}
	}
	for i := range cfg.Notifications {
		cfg.Notifications[i].WebhookURL = ResolveEnvRef(cfg.Notifications[i].WebhookURL, fmt.Sprintf("notifications[%d].webhookUrl", i))
	}
	if cfg.TLS.CertFile != "" {
		cfg.TLS.CertFile = ResolveEnvRef(cfg.TLS.CertFile, "tls.certFile")
	}
	if cfg.TLS.KeyFile != "" {
		cfg.TLS.KeyFile = ResolveEnvRef(cfg.TLS.KeyFile, "tls.keyFile")
	}
	for name, pc := range cfg.Providers {
		if pc.APIKey != "" {
			pc.APIKey = ResolveEnvRef(pc.APIKey, fmt.Sprintf("providers.%s.apiKey", name))
			cfg.Providers[name] = pc
		}
	}
	for name, wh := range cfg.IncomingWebhooks {
		if wh.Secret != "" {
			wh.Secret = ResolveEnvRef(wh.Secret, fmt.Sprintf("incomingWebhooks.%s.secret", name))
			cfg.IncomingWebhooks[name] = wh
		}
	}
	if cfg.Slack.BotToken != "" {
		cfg.Slack.BotToken = ResolveEnvRef(cfg.Slack.BotToken, "slack.botToken")
	}
	if cfg.Slack.SigningSecret != "" {
		cfg.Slack.SigningSecret = ResolveEnvRef(cfg.Slack.SigningSecret, "slack.signingSecret")
	}
	if cfg.Slack.AppToken != "" {
		cfg.Slack.AppToken = ResolveEnvRef(cfg.Slack.AppToken, "slack.appToken")
	}
	if cfg.Discord.BotToken != "" {
		cfg.Discord.BotToken = ResolveEnvRef(cfg.Discord.BotToken, "discord.botToken")
	}
	if cfg.Embedding.APIKey != "" {
		cfg.Embedding.APIKey = ResolveEnvRef(cfg.Embedding.APIKey, "embedding.apiKey")
	}
	if cfg.Tools.WebSearch.APIKey != "" {
		cfg.Tools.WebSearch.APIKey = ResolveEnvRef(cfg.Tools.WebSearch.APIKey, "tools.webSearch.apiKey")
	}
	if cfg.Voice.STT.APIKey != "" {
		cfg.Voice.STT.APIKey = ResolveEnvRef(cfg.Voice.STT.APIKey, "voice.stt.apiKey")
	}
	if cfg.Voice.TTS.APIKey != "" {
		cfg.Voice.TTS.APIKey = ResolveEnvRef(cfg.Voice.TTS.APIKey, "voice.tts.apiKey")
	}
	if cfg.WhatsApp.AccessToken != "" {
		cfg.WhatsApp.AccessToken = ResolveEnvRef(cfg.WhatsApp.AccessToken, "whatsapp.accessToken")
	}
	if cfg.WhatsApp.AppSecret != "" {
		cfg.WhatsApp.AppSecret = ResolveEnvRef(cfg.WhatsApp.AppSecret, "whatsapp.appSecret")
	}
	if cfg.Push.VAPIDPublicKey != "" {
		cfg.Push.VAPIDPublicKey = ResolveEnvRef(cfg.Push.VAPIDPublicKey, "push.vapidPublicKey")
	}
	if cfg.Push.VAPIDPrivateKey != "" {
		cfg.Push.VAPIDPrivateKey = ResolveEnvRef(cfg.Push.VAPIDPrivateKey, "push.vapidPrivateKey")
	}
	for name, pcfg := range cfg.Plugins {
		if len(pcfg.Env) > 0 {
			for k, v := range pcfg.Env {
				pcfg.Env[k] = ResolveEnvRef(v, fmt.Sprintf("plugins.%s.env.%s", name, k))
			}
			cfg.Plugins[name] = pcfg
		}
	}
	if cfg.Tools.Vision.APIKey != "" {
		cfg.Tools.Vision.APIKey = ResolveEnvRef(cfg.Tools.Vision.APIKey, "tools.vision.apiKey")
	}
	if cfg.LINE.ChannelSecret != "" {
		cfg.LINE.ChannelSecret = ResolveEnvRef(cfg.LINE.ChannelSecret, "line.channelSecret")
	}
	if cfg.LINE.ChannelAccessToken != "" {
		cfg.LINE.ChannelAccessToken = ResolveEnvRef(cfg.LINE.ChannelAccessToken, "line.channelAccessToken")
	}
	if cfg.Matrix.AccessToken != "" {
		cfg.Matrix.AccessToken = ResolveEnvRef(cfg.Matrix.AccessToken, "matrix.accessToken")
	}
	if cfg.Teams.AppID != "" {
		cfg.Teams.AppID = ResolveEnvRef(cfg.Teams.AppID, "teams.appId")
	}
	if cfg.Teams.AppPassword != "" {
		cfg.Teams.AppPassword = ResolveEnvRef(cfg.Teams.AppPassword, "teams.appPassword")
	}
	if cfg.Teams.TenantID != "" {
		cfg.Teams.TenantID = ResolveEnvRef(cfg.Teams.TenantID, "teams.tenantId")
	}
	if cfg.Signal.PhoneNumber != "" {
		cfg.Signal.PhoneNumber = ResolveEnvRef(cfg.Signal.PhoneNumber, "signal.phoneNumber")
	}
	if cfg.GoogleChat.ServiceAccountKey != "" {
		cfg.GoogleChat.ServiceAccountKey = ResolveEnvRef(cfg.GoogleChat.ServiceAccountKey, "googleChat.serviceAccountKey")
	}
	cfg.HomeAssistant.Token = ResolveEnvRef(cfg.HomeAssistant.Token, "homeAssistant.token")
	cfg.IMessage.Password = ResolveEnvRef(cfg.IMessage.Password, "imessage.password")
	cfg.OAuth.EncryptionKey = ResolveEnvRef(cfg.OAuth.EncryptionKey, "oauth.encryptionKey")
	for name, svc := range cfg.OAuth.Services {
		svc.ClientID = ResolveEnvRef(svc.ClientID, fmt.Sprintf("oauth.services.%s.clientId", name))
		svc.ClientSecret = ResolveEnvRef(svc.ClientSecret, fmt.Sprintf("oauth.services.%s.clientSecret", name))
		cfg.OAuth.Services[name] = svc
	}
	if cfg.TaskManager.Todoist.APIKey != "" {
		cfg.TaskManager.Todoist.APIKey = ResolveEnvRef(cfg.TaskManager.Todoist.APIKey, "taskManager.todoist.apiKey")
	}
	if cfg.TaskManager.Notion.APIKey != "" {
		cfg.TaskManager.Notion.APIKey = ResolveEnvRef(cfg.TaskManager.Notion.APIKey, "taskManager.notion.apiKey")
	}
}

// ResolveMCPPaths writes MCP configs to temp files for --mcp-config flag.
func ResolveMCPPaths(cfg *Config) {
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

// LoadForVersioning is a lightweight config loader for versioning hooks.
// It only resolves historyDB path. Returns nil if loading fails.
func LoadForVersioning(configPath string) *Config {
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
