package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"text/tabwriter"
	"time"
)

// cmdDiscord is the main entry point for `tetora discord`.
func cmdDiscord(args []string) {
	handleDiscordCLI(args)
}

// handleDiscordCLI routes `tetora discord <subcommand> [args]`.
func handleDiscordCLI(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: tetora discord <channels> [action] [name]")
		fmt.Println()
		fmt.Println("Commands:")
		fmt.Println("  discord channels                  List all webhook channels")
		fmt.Println("  discord channels add [name]       Add a new webhook channel (wizard)")
		fmt.Println("  discord channels test [name]      Send a test message to a channel")
		fmt.Println("  discord channels remove [name]    Remove a webhook channel")
		return
	}

	switch args[0] {
	case "channels":
		handleDiscordChannelsCLI(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown discord subcommand: %s\n", args[0])
		fmt.Fprintln(os.Stderr, "Run 'tetora discord' for usage.")
		os.Exit(1)
	}
}

func handleDiscordChannelsCLI(args []string) {
	if len(args) == 0 {
		discordChannelsList()
		return
	}
	switch args[0] {
	case "add":
		name := ""
		if len(args) > 1 {
			name = args[1]
		}
		discordChannelAdd(name)
	case "test":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: tetora discord channels test <name>")
			os.Exit(1)
		}
		discordChannelTest(args[1])
	case "remove", "rm":
		name := ""
		if len(args) > 1 {
			name = args[1]
		}
		discordChannelRemove(name)
	default:
		fmt.Fprintf(os.Stderr, "Unknown channels action: %s\n", args[0])
		os.Exit(1)
	}
}

// discordChannelsList lists all configured Discord webhook channels.
func discordChannelsList() {
	cfg := loadConfig(findConfigPath())
	channels := discordGetWebhookChannels(cfg)

	if len(channels) == 0 {
		fmt.Println("No Discord webhook channels configured.")
		fmt.Println()
		fmt.Println("Add one with: tetora discord channels add")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintf(w, "NAME\tWEBHOOK (preview)\tEVENTS\n")
	for _, ch := range channels {
		preview := ch.WebhookURL
		if len(preview) > 50 {
			preview = preview[:47] + "..."
		}
		events := "all"
		if len(ch.Events) > 0 {
			events = strings.Join(ch.Events, ", ")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", ch.Name, preview, events)
	}
	w.Flush()
}

// discordChannelAdd runs the 4-step wizard to add a new Discord webhook channel.
func discordChannelAdd(nameArg string) {
	scanner := bufio.NewScanner(os.Stdin)
	prompt := func(label, defaultVal string) string {
		if defaultVal != "" {
			fmt.Printf("  %s [%s]: ", label, defaultVal)
		} else {
			fmt.Printf("  %s: ", label)
		}
		scanner.Scan()
		s := strings.TrimSpace(scanner.Text())
		if s == "" {
			return defaultVal
		}
		return s
	}

	fmt.Println()
	fmt.Println("Discord Webhook Channel Setup")
	fmt.Println("================================")
	fmt.Println()

	// --- Step 1: Channel name ---
	var channelName string
	if nameArg != "" {
		channelName = nameArg
		fmt.Printf("  Channel name: %s\n", channelName)
	} else {
		fmt.Println("Step 1/4  Channel Name")
		fmt.Println("  Use lowercase letters, numbers, hyphens, or underscores.")
		fmt.Println("  Example: stock, alerts, dev-errors")
		fmt.Println()
		for {
			channelName = prompt("Channel name", "")
			if channelName == "" {
				fmt.Println("  Name cannot be empty.")
				continue
			}
			if !discordValidChannelName(channelName) {
				fmt.Println("  Invalid name. Only letters, numbers, hyphens, and underscores allowed.")
				continue
			}
			// Check if already exists.
			cfg := loadConfig(findConfigPath())
			existing := discordGetWebhookChannels(cfg)
			dup := false
			for _, ch := range existing {
				if ch.Name == channelName {
					dup = true
					break
				}
			}
			if dup {
				fmt.Printf("  Channel '%s' already exists. Use a different name.\n", channelName)
				continue
			}
			break
		}
	}
	fmt.Println()

	// --- Step 2: Webhook URL ---
	fmt.Println("Step 2/4  Webhook URL")
	fmt.Println("  How to get a Discord webhook URL:")
	fmt.Println("  1. Open Discord and go to your server")
	fmt.Println("  2. Right-click the target channel -> Edit Channel")
	fmt.Println("  3. Go to Integrations -> Webhooks -> New Webhook")
	fmt.Println("  4. Copy the Webhook URL")
	fmt.Println()

	var webhookURL string
	for {
		webhookURL = prompt("Webhook URL", "")
		if webhookURL == "" {
			fmt.Println("  URL cannot be empty.")
			continue
		}
		if !strings.HasPrefix(webhookURL, "https://discord.com/api/webhooks/") &&
			!strings.HasPrefix(webhookURL, "https://discordapp.com/api/webhooks/") {
			fmt.Println("  Invalid URL. Must start with https://discord.com/api/webhooks/")
			continue
		}
		break
	}
	fmt.Println()

	// --- Step 3: Events filter ---
	fmt.Println("Step 3/4  Notification Events")
	fmt.Println()
	eventOptions := []string{
		"All events (tasks complete, errors, etc.)",
		"Errors only",
		"Successes only",
	}
	eventIdx := interactiveChoose(eventOptions, 0)
	if eventIdx < 0 {
		// Fallback: number input
		fmt.Println("  1. All events")
		fmt.Println("  2. Errors only")
		fmt.Println("  3. Successes only")
		s := prompt("Choose", "1")
		switch s {
		case "2":
			eventIdx = 1
		case "3":
			eventIdx = 2
		default:
			eventIdx = 0
		}
	}

	var events []string
	switch eventIdx {
	case 1:
		events = []string{"error"}
	case 2:
		events = []string{"success"}
	default:
		events = []string{"all"}
	}
	fmt.Println()

	// --- Step 4: Confirmation + optional test ---
	fmt.Println("Step 4/4  Confirm")
	fmt.Printf("  Channel name : %s\n", channelName)
	fmt.Printf("  Webhook URL  : %s\n", webhookURL[:min(60, len(webhookURL))]+"...")
	fmt.Printf("  Events       : %s\n", strings.Join(events, ", "))
	fmt.Println()
	fmt.Print("  Send a test message? [y/N]: ")
	scanner.Scan()
	sendTest := strings.ToLower(strings.TrimSpace(scanner.Text())) == "y"

	// Save to config.
	configPath := findConfigPath()
	newCh := NotificationChannel{
		Name:       channelName,
		Type:       "discord",
		WebhookURL: webhookURL,
		Events:     events,
	}
	if err := discordUpdateNotificationsConfig(configPath, channelName, &newCh); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("\n  Channel '%s' saved.\n", channelName)

	// Optional test message.
	if sendTest {
		fmt.Printf("  Sending test message to '%s'...\n", channelName)
		err := discordSendTestWebhook(webhookURL, channelName)
		if err != nil {
			fmt.Printf("  Test failed: %v\n", err)
		} else {
			fmt.Println("  Test message sent successfully.")
		}
	}
	fmt.Println()
}

// discordChannelTest sends a test message to a named channel.
func discordChannelTest(name string) {
	cfg := loadConfig(findConfigPath())
	channels := discordGetWebhookChannels(cfg)

	var found *NotificationChannel
	for i := range channels {
		if channels[i].Name == name {
			found = &channels[i]
			break
		}
	}
	if found == nil {
		fmt.Fprintf(os.Stderr, "Channel '%s' not found.\n", name)
		fmt.Fprintln(os.Stderr, "Run 'tetora discord channels' to list configured channels.")
		os.Exit(1)
	}

	fmt.Printf("Sending test message to '%s'...\n", name)
	if err := discordSendTestWebhook(found.WebhookURL, name); err != nil {
		fmt.Fprintf(os.Stderr, "Test failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Test message sent successfully.")
}

// discordChannelRemove removes a Discord webhook channel (with optional interactive selection).
func discordChannelRemove(nameArg string) {
	configPath := findConfigPath()
	cfg := loadConfig(configPath)
	channels := discordGetWebhookChannels(cfg)

	if len(channels) == 0 {
		fmt.Println("No Discord webhook channels configured.")
		return
	}

	var targetName string
	if nameArg != "" {
		targetName = nameArg
	} else {
		// Interactive selection.
		fmt.Println("Select channel to remove:")
		fmt.Println()
		names := make([]string, len(channels))
		for i, ch := range channels {
			names[i] = ch.Name
		}
		idx := interactiveChoose(names, 0)
		if idx < 0 {
			// Fallback.
			for i, n := range names {
				fmt.Printf("  %d. %s\n", i+1, n)
			}
			scanner := bufio.NewScanner(os.Stdin)
			fmt.Print("  Choose (number): ")
			scanner.Scan()
			s := strings.TrimSpace(scanner.Text())
			n := 0
			fmt.Sscanf(s, "%d", &n)
			if n < 1 || n > len(names) {
				fmt.Println("Aborted.")
				return
			}
			idx = n - 1
		}
		targetName = names[idx]
	}

	// Verify exists.
	found := false
	for _, ch := range channels {
		if ch.Name == targetName {
			found = true
			break
		}
	}
	if !found {
		fmt.Fprintf(os.Stderr, "Channel '%s' not found.\n", targetName)
		os.Exit(1)
	}

	// Confirm.
	fmt.Printf("Remove channel '%s'? [y/N]: ", targetName)
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	if strings.ToLower(strings.TrimSpace(scanner.Text())) != "y" {
		fmt.Println("Aborted.")
		return
	}

	if err := discordUpdateNotificationsConfig(configPath, targetName, nil); err != nil {
		fmt.Fprintf(os.Stderr, "Error updating config: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Channel '%s' removed.\n", targetName)
}

// --- Helpers ---

// discordValidChannelName validates the channel name format.
var discordChannelNameRe = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func discordValidChannelName(name string) bool {
	return discordChannelNameRe.MatchString(name) && len(name) <= 64
}

// discordGetWebhookChannels returns all Discord notification channels from config.
func discordGetWebhookChannels(cfg *Config) []NotificationChannel {
	var out []NotificationChannel
	for _, ch := range cfg.Notifications {
		if ch.Type == "discord" {
			out = append(out, ch)
		}
	}
	return out
}

// discordUpdateNotificationsConfig adds/updates (rc != nil) or removes (rc == nil)
// a Discord notification channel in config.json, preserving all other fields.
// It also syncs cfg.discord.webhooks so named channels are accessible by key.
func discordUpdateNotificationsConfig(configPath, name string, rc *NotificationChannel) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	// Parse existing notifications slice.
	var notifications []NotificationChannel
	if notifRaw, ok := raw["notifications"]; ok {
		json.Unmarshal(notifRaw, &notifications) //nolint:errcheck
	}

	// Parse existing discord config to update webhooks map.
	var discordCfg map[string]json.RawMessage
	if discordRaw, ok := raw["discord"]; ok {
		json.Unmarshal(discordRaw, &discordCfg) //nolint:errcheck
	}
	if discordCfg == nil {
		discordCfg = make(map[string]json.RawMessage)
	}

	// Parse discord.webhooks map.
	var webhooks map[string]string
	if whRaw, ok := discordCfg["webhooks"]; ok {
		json.Unmarshal(whRaw, &webhooks) //nolint:errcheck
	}
	if webhooks == nil {
		webhooks = make(map[string]string)
	}

	if rc == nil {
		// Remove entry with matching name + type=discord.
		filtered := notifications[:0]
		for _, ch := range notifications {
			if !(ch.Type == "discord" && ch.Name == name) {
				filtered = append(filtered, ch)
			}
		}
		notifications = filtered
		// Also remove from webhooks map.
		delete(webhooks, name)
	} else {
		// Update existing or append new.
		updated := false
		for i := range notifications {
			if notifications[i].Type == "discord" && notifications[i].Name == name {
				notifications[i] = *rc
				updated = true
				break
			}
		}
		if !updated {
			notifications = append(notifications, *rc)
		}
		// Sync into webhooks map.
		webhooks[name] = rc.WebhookURL
	}

	notifJSON, err := json.Marshal(notifications)
	if err != nil {
		return err
	}
	raw["notifications"] = notifJSON

	// Write back discord.webhooks.
	whJSON, err := json.Marshal(webhooks)
	if err != nil {
		return err
	}
	discordCfg["webhooks"] = whJSON
	discordJSON, err := json.Marshal(discordCfg)
	if err != nil {
		return err
	}
	raw["discord"] = discordJSON

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, append(out, '\n'), 0o644)
}

// discordSendTestWebhook sends a test message to a Discord webhook URL.
func discordSendTestWebhook(webhookURL, channelName string) error {
	payload := fmt.Sprintf(`{"content":"[Tetora] Test message for channel '%s' — %s"}`,
		channelName, time.Now().Format("2006-01-02 15:04:05"))

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(webhookURL, "application/json", strings.NewReader(payload))
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("Discord returned HTTP %d", resp.StatusCode)
	}
	return nil
}

