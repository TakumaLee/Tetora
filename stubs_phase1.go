package main

// stubs_phase1.go — Phase 1 CLI extraction leftovers.
//
// These functions existed in the root package before Phase 1 (refactor/kokuyou-cli-extract)
// moved CLI code into internal/cli/. The root-package callers were not updated at the
// same time, so this file provides thin bridges until each call-site is migrated.
//
// TODO(phase2): Remove each stub once its caller has been migrated to call
// internal/cli directly or the logic has been inlined at the call-site.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"tetora/internal/cli"
)

// restartLaunchd delegates to the unexported internal/cli implementation by
// forwarding through the exported KillDaemonProcess + the same launchctl logic.
// Signature mirrors internal/cli/service.go:restartLaunchd.
//
// TODO(phase2): migrate callers in cli.go to call internal/cli.RestartLaunchd
// once it is exported, or inline the call-site.
func restartLaunchd(plistPath string) error {
	// The canonical implementation lives in internal/cli but is unexported.
	// Re-expose it here until cli.go is migrated.
	return cli.RestartLaunchd(plistPath)
}

// findDaemonPIDs delegates to the exported internal/cli implementation.
//
// TODO(phase2): migrate callers in cli.go to call cli.FindDaemonPIDs directly.
func findDaemonPIDs() []string {
	return cli.FindDaemonPIDs()
}

// killDaemonProcess delegates to the exported internal/cli implementation.
//
// TODO(phase2): migrate callers in cli.go to call cli.KillDaemonProcess directly.
func killDaemonProcess() bool {
	return cli.KillDaemonProcess()
}

// updateConfigAgents adds, updates, or removes an agent entry in config.json.
// When rc is nil the agent is removed; otherwise it is marshalled and written.
// Delegates to internal/cli.UpdateConfigAgents for the actual JSON surgery.
//
// TODO(phase2): migrate callers to use cli.UpdateConfigAgents directly with a
// pre-marshalled json.RawMessage, or centralise config writes in a root helper.
func updateConfigAgents(configPath, agentName string, rc *AgentConfig) error {
	var agentJSON json.RawMessage
	if rc != nil {
		b, err := json.Marshal(rc)
		if err != nil {
			return err
		}
		agentJSON = b
	}
	return cli.UpdateConfigAgents(configPath, agentName, agentJSON)
}

// serviceInstall delegates to the unexported internal/cli implementation.
//
// TODO(phase2): export internal/cli.serviceInstall (→ ServiceInstall) and
// update the caller in cli_init.go to call it directly.
func serviceInstall() {
	cli.ServiceInstall()
}

// handleWorkflowVersionSubcommands delegates to the exported internal/cli
// implementation. Returns true if the subcommand was handled.
//
// TODO(phase2): migrate cli_workflow.go to call cli.HandleWorkflowVersionSubcommands
// directly and remove this stub.
func handleWorkflowVersionSubcommands(action string, args []string) bool {
	return cli.HandleWorkflowVersionSubcommands(action, args)
}

// discordGetWebhookChannels returns the Discord webhook notification channels
// from the root Config. Mirrors the logic in internal/cli/discord.go but
// operates on the root Config type rather than cli.CLIConfig.
//
// TODO(phase2): unify the two Config types or add a root-package adapter so
// cron.go can call cli.discordGetWebhookChannels without needing this bridge.
func discordGetWebhookChannels(cfg *Config) []NotificationChannel {
	var out []NotificationChannel
	for _, ch := range cfg.Notifications {
		if ch.Type == "discord" {
			out = append(out, ch)
		}
	}
	return out
}

// joinStrings joins a slice of strings with the given separator.
// TODO(phase2): replace callers with strings.Join and delete.
func joinStrings(parts []string, sep string) string {
	return strings.Join(parts, sep)
}

// updateAgentModel updates an agent's model in config and returns the old model.
// TODO(phase2): move to a proper config helper.
func updateAgentModel(cfg *Config, agentName, model string) (string, error) {
	ac, ok := cfg.Agents[agentName]
	if !ok {
		return "", fmt.Errorf("agent %q not found", agentName)
	}
	old := ac.Model
	ac.Model = model
	cfg.Agents[agentName] = ac
	configPath := findConfigPath()
	return old, updateConfigAgents(configPath, agentName, &ac)
}

// formatDuration formats a time.Duration as human-readable (e.g. "1m30s").
// TODO(phase2): consolidate with formatDurationMs in discord_messaging.go.
func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%.0fs", d.Seconds())
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	if s == 0 {
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%dm%ds", m, s)
}

// cmdDrain is a placeholder for the drain command.
// TODO(phase2): implement or remove from main.go.
func cmdDrain() {
	fmt.Println("drain: not yet implemented")
}

// discordValidChannelName validates a Discord notification channel name.
// TODO(phase2): export from internal/cli or inline.
func discordValidChannelName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, r := range name {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// discordUpdateNotificationsConfig adds or updates a Discord notification channel in config.
// TODO(phase2): export from internal/cli or centralize config writes.
func discordUpdateNotificationsConfig(configPath, name string, ch *NotificationChannel) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	var channels []NotificationChannel
	if notifRaw, ok := raw["notifications"]; ok {
		_ = json.Unmarshal(notifRaw, &channels)
	}

	if ch == nil {
		// Remove.
		filtered := channels[:0]
		for _, c := range channels {
			if c.Name != name {
				filtered = append(filtered, c)
			}
		}
		channels = filtered
	} else {
		found := false
		for i, c := range channels {
			if c.Name == name {
				channels[i] = *ch
				found = true
				break
			}
		}
		if !found {
			channels = append(channels, *ch)
		}
	}

	b, _ := json.Marshal(channels)
	raw["notifications"] = b
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, append(out, '\n'), 0o644)
}

// formatSize formats a byte count as a human-readable string.
// TODO(phase2): export from internal/cli/knowledge.go or inline.
var formatSize = cli.FormatSize

// parseRoleFlag extracts a --role flag from args and returns (role, remainingArgs).
// TODO(phase2): export from internal/cli/memory.go or inline.
var parseRoleFlag = cli.ParseRoleFlag

// handleConfigVersionSubcommands delegates to internal/cli.HandleConfigVersionSubcommands.
// TODO(phase2): migrate callers.
var handleConfigVersionSubcommands = cli.HandleConfigVersionSubcommands

// discordSendTestWebhook sends a test message to a Discord webhook URL.
// TODO(phase2): export from internal/cli or inline.
func discordSendTestWebhook(webhookURL, channelName string) error {
	payload := fmt.Sprintf(`{"content":"🔔 Test notification from Tetora — channel: %s"}`, channelName)
	resp, err := http.Post(webhookURL, "application/json", strings.NewReader(payload))
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("Discord returned HTTP %d", resp.StatusCode)
	}
	return nil
}
