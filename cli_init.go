package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// --- Interactive menu helpers ---

const (
	menuKeyNone  = 0
	menuKeyUp    = 1
	menuKeyDown  = 2
	menuKeyEnter = 3
	menuKeyQuit  = 4
)

func menuSetRawMode() (string, error) {
	cmd := exec.Command("stty", "-g")
	cmd.Stdin = os.Stdin
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	saved := strings.TrimSpace(string(out))
	raw := exec.Command("stty", "raw", "-echo")
	raw.Stdin = os.Stdin
	if err := raw.Run(); err != nil {
		return "", err
	}
	return saved, nil
}

func menuRestoreMode(saved string) {
	cmd := exec.Command("stty", saved)
	cmd.Stdin = os.Stdin
	cmd.Run()
}

func menuReadKey() int {
	buf := make([]byte, 4)
	n, err := os.Stdin.Read(buf)
	if err != nil || n == 0 {
		return menuKeyQuit
	}
	if n == 1 {
		switch buf[0] {
		case 0x0d, 0x0a:
			return menuKeyEnter
		case 0x03:
			return menuKeyQuit
		case 'q':
			return menuKeyQuit
		case 'k':
			return menuKeyUp
		case 'j':
			return menuKeyDown
		}
		return menuKeyNone
	}
	if buf[0] == 0x1b && n >= 3 && buf[1] == '[' {
		switch buf[2] {
		case 'A':
			return menuKeyUp
		case 'B':
			return menuKeyDown
		}
	}
	return menuKeyNone
}

// interactiveChoose displays an arrow-key navigable menu.
// Returns the selected index, or -1 if interactive mode is unavailable.
func interactiveChoose(options []string, defaultIdx int) int {
	selected := defaultIdx
	n := len(options)

	// Hide cursor and print initial menu
	fmt.Print("\033[?25l")
	for i, o := range options {
		if i == selected {
			fmt.Printf("  \033[36m❯ %s\033[0m\n", o)
		} else {
			fmt.Printf("    %s\n", o)
		}
	}

	saved, err := menuSetRawMode()
	if err != nil {
		// Clear menu and restore cursor for fallback
		fmt.Printf("\033[%dA\033[J", n)
		fmt.Print("\033[?25h")
		return -1
	}

	for {
		key := menuReadKey()
		changed := false
		switch key {
		case menuKeyUp:
			if selected > 0 {
				selected--
				changed = true
			}
		case menuKeyDown:
			if selected < n-1 {
				selected++
				changed = true
			}
		case menuKeyEnter:
			menuRestoreMode(saved)
			fmt.Printf("\033[%dA\033[J", n)
			fmt.Printf("  \033[36m✓ %s\033[0m\n", options[selected])
			fmt.Print("\033[?25h")
			return selected
		case menuKeyQuit:
			menuRestoreMode(saved)
			fmt.Printf("\033[%dA\033[J", n)
			fmt.Print("\033[?25h")
			fmt.Println("Aborted.")
			os.Exit(0)
		}
		if !changed {
			continue
		}

		// Re-render menu
		fmt.Fprintf(os.Stdout, "\033[%dA", n)
		for i, o := range options {
			if i == selected {
				fmt.Fprintf(os.Stdout, "\r\033[2K  \033[36m❯ %s\033[0m\r\n", o)
			} else {
				fmt.Fprintf(os.Stdout, "\r\033[2K    %s\r\n", o)
			}
		}
	}
}

func cmdInit() {
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
	choose := func(label string, options []string, defaultIdx int) int {
		if idx := interactiveChoose(options, defaultIdx); idx >= 0 {
			return idx
		}
		// Fallback: number-based input
		for i, o := range options {
			marker := "  "
			if i == defaultIdx {
				marker = "* "
			}
			fmt.Printf("    %s%d. %s\n", marker, i+1, o)
		}
		s := prompt(label, fmt.Sprintf("%d", defaultIdx+1))
		n, _ := strconv.Atoi(s)
		if n < 1 || n > len(options) {
			return defaultIdx
		}
		return n - 1
	}

	home, _ := os.UserHomeDir()
	configDir := filepath.Join(home, ".tetora")
	configPath := filepath.Join(configDir, "config.json")

	if _, err := os.Stat(configPath); err == nil {
		fmt.Printf("Config already exists: %s\n", configPath)
		fmt.Print("  Overwrite? [y/N]: ")
		scanner.Scan()
		if strings.ToLower(strings.TrimSpace(scanner.Text())) != "y" {
			fmt.Println("Aborted.")
			return
		}
		fmt.Println()
	}

	// --- OpenClaw Detection ---
	migCfg := &Config{baseDir: configDir}
	var ocMigrated bool
	var ocReport *MigrationReport
	ocDir := detectOpenClaw()
	if ocDir != "" {
		fmt.Printf("OpenClaw installation detected at %s\n", ocDir)
		fmt.Print("  Import settings from OpenClaw? [Y/n]: ")
		scanner.Scan()
		ans := strings.ToLower(strings.TrimSpace(scanner.Text()))
		if ans != "n" {
			fmt.Println()
			fmt.Println("  What to import?")
			incIdx := choose("Include", []string{
				"All (config + memory + workspace + skills + cron + roles)",
				"Config only",
				"Config + roles + cron jobs",
				"Custom (comma-separated: config,memory,skills,cron,workspace,roles)",
			}, 0)

			var includeStr string
			switch incIdx {
			case 0:
				includeStr = "all"
			case 1:
				includeStr = "config"
			case 2:
				includeStr = "config,roles,cron"
			case 3:
				includeStr = prompt("Include list", "config,memory,skills,cron,workspace,roles")
			}

			include := parseIncludeList(includeStr)
			// Defer roles import until after config is written.
			wantRoles := include["roles"]
			delete(include, "roles")
			report, err := migrateOpenClaw(migCfg, ocDir, false, include, false)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  Migration error: %v\n", err)
			} else {
				ocMigrated = true
				ocReport = report
				if wantRoles {
					include["roles"] = true
				}
				fmt.Printf("  Imported: %d config fields, %d memory files, %d workspace files, %d skills, %d cron jobs\n",
					report.ConfigMerged, report.MemoryFiles, report.WorkspaceFiles, report.SkillsImported, report.CronJobs)
				for _, w := range report.Warnings {
					fmt.Printf("  \u26a0 %s\n", w)
				}
				for _, e := range report.Errors {
					fmt.Printf("  \u2717 %s\n", e)
				}
			}
			fmt.Println()
		}
	}
	_ = ocReport // used below if ocMigrated

	fmt.Println("=== Tetora Quick Setup ===")
	fmt.Println()

	// --- Step 1: Channel ---
	fmt.Println("Step 1/3: Choose a messaging channel")
	fmt.Println()
	channelIdx := choose("Channel", []string{
		"Telegram",
		"Discord",
		"Slack",
		"None (HTTP API only)",
	}, 0)

	var botToken string
	var chatID int64
	var discordToken, discordAppID, discordChannelID string
	var slackToken, slackSigningSecret string

	switch channelIdx {
	case 0: // Telegram
		fmt.Println()
		fmt.Println("  \033[2mHow to get these values:")
		fmt.Println("    1. Message @BotFather on Telegram → /newbot")
		fmt.Println("    2. Copy the bot token it gives you")
		fmt.Println("    3. Send a message to your bot, then visit:")
		fmt.Println("       https://api.telegram.org/bot<TOKEN>/getUpdates")
		fmt.Println("       to find your chat ID\033[0m")
		fmt.Println()
		botToken = prompt("Telegram bot token", "")
		cidStr := prompt("Telegram chat ID", "")
		chatID, _ = strconv.ParseInt(cidStr, 10, 64)
	case 1: // Discord
		fmt.Println()
		fmt.Println("  \033[2mHow to get these values:")
		fmt.Println("    1. Go to https://discord.com/developers/applications")
		fmt.Println("    2. Create an application (or select existing)")
		fmt.Println("    3. Application ID → General Information page (top)")
		fmt.Println("    4. Bot → Reset Token → copy (this is the bot token)")
		fmt.Println("    5. Bot → scroll down → enable MESSAGE CONTENT INTENT")
		fmt.Println("    6. Invite bot to your server:")
		fmt.Println("       (no server yet? Discord left sidebar → '+' → Create My Own)")
		fmt.Println("       OAuth2 → URL Generator → check 'bot' in SCOPES")
		fmt.Println("       → check permissions (Send Messages, Read Message History)")
		fmt.Println("       → copy Generated URL at bottom → open in browser → select server")
		fmt.Println("    7. Get channel ID:")
		fmt.Println("       Discord app → Settings (gear icon near your username)")
		fmt.Println("       → App Settings → Advanced → toggle Developer Mode ON")
		fmt.Println("       → go back, right-click the target channel → Copy Channel ID\033[0m")
		fmt.Println()
		discordToken = prompt("Discord bot token", "")
		discordAppID = prompt("Discord application ID", "")
		discordChannelID = prompt("Discord channel ID", "")
	case 2: // Slack
		fmt.Println()
		fmt.Println("  \033[2mHow to get these values:")
		fmt.Println("    1. Go to https://api.slack.com/apps → Create New App")
		fmt.Println("    2. Bot token → OAuth & Permissions → Install to Workspace")
		fmt.Println("       → copy the xoxb-... token")
		fmt.Println("    3. Signing secret → Basic Information → App Credentials\033[0m")
		fmt.Println()
		slackToken = prompt("Slack bot token (xoxb-...)", "")
		slackSigningSecret = prompt("Slack signing secret", "")
	}

	// Apply OpenClaw values as defaults.
	if ocMigrated {
		if botToken == "" && migCfg.Telegram.BotToken != "" {
			botToken = migCfg.Telegram.BotToken
			fmt.Printf("  (using Telegram token from OpenClaw: %s****)\n", botToken[:4])
		}
		if chatID == 0 && migCfg.Telegram.ChatID != 0 {
			chatID = migCfg.Telegram.ChatID
			fmt.Printf("  (using Telegram chat ID from OpenClaw: %d)\n", chatID)
		}
		if discordToken == "" && migCfg.Discord.BotToken != "" {
			discordToken = migCfg.Discord.BotToken
			fmt.Printf("  (using Discord token from OpenClaw)\n")
		}
		if slackToken == "" && migCfg.Slack.BotToken != "" {
			slackToken = migCfg.Slack.BotToken
			fmt.Printf("  (using Slack token from OpenClaw)\n")
		}
	}

	// --- Step 2: Provider ---
	fmt.Println()
	fmt.Println("Step 2/3: Choose an AI provider")
	fmt.Println()
	providerIdx := choose("Provider", []string{
		"Claude CLI (local claude binary)",
		"Claude API (direct API key)",
		"OpenAI-compatible API",
	}, 0)

	claudePath := ""
	var claudeAPIKey, openaiEndpoint, openaiAPIKey, defaultModel string

	switch providerIdx {
	case 0: // Claude CLI
		detected := detectClaude()
		claudePath = prompt("Claude CLI path", detected)
		defaultModel = prompt("Default model", "sonnet")
	case 1: // Claude API
		claudeAPIKey = prompt("Claude API key", "")
		defaultModel = prompt("Default model", "claude-sonnet-4-5-20250929")
	case 2: // OpenAI-compatible
		openaiEndpoint = prompt("API endpoint", "https://api.openai.com/v1")
		openaiAPIKey = prompt("API key", "")
		defaultModel = prompt("Default model", "gpt-4o")
	}

	if ocMigrated {
		if defaultModel == "" && migCfg.DefaultModel != "" {
			defaultModel = migCfg.DefaultModel
			fmt.Printf("  (using model from OpenClaw: %s)\n", defaultModel)
		}
	}

	// --- Step 3: Generate ---
	fmt.Println()
	fmt.Println("Step 3/3: Generating config...")

	defaultWorkdir := filepath.Join(configDir, "workspace")

	// Generate API token.
	tokenBytes := make([]byte, 32)
	rand.Read(tokenBytes)
	apiToken := hex.EncodeToString(tokenBytes)

	// Build config.
	cfg := map[string]any{
		"maxConcurrent":         3,
		"defaultModel":          defaultModel,
		"defaultTimeout":        "15m",
		"defaultBudget":         2.0,
		"defaultPermissionMode": "acceptEdits",
		"defaultWorkdir":        defaultWorkdir,
		"listenAddr":            "127.0.0.1:8991",
		"jobsFile":              "jobs.json",
		"apiToken":              apiToken,
		"log":                   true,
	}

	// Claude CLI path.
	if claudePath != "" {
		cfg["claudePath"] = claudePath
	}

	// Channel config.
	switch channelIdx {
	case 0: // Telegram
		cfg["telegram"] = map[string]any{
			"enabled":     true,
			"botToken":    botToken,
			"chatID":      chatID,
			"pollTimeout": 30,
		}
	case 1: // Discord
		cfg["discord"] = map[string]any{
			"enabled":   true,
			"botToken":  discordToken,
			"appID":     discordAppID,
			"channelID": discordChannelID,
		}
	case 2: // Slack
		cfg["slack"] = map[string]any{
			"enabled":       true,
			"botToken":      slackToken,
			"signingSecret": slackSigningSecret,
		}
	default:
		cfg["telegram"] = map[string]any{"enabled": false}
	}

	// Provider config.
	switch providerIdx {
	case 1: // Claude API
		cfg["providers"] = map[string]any{
			"claude-api": map[string]any{
				"type":   "claude",
				"apiKey": claudeAPIKey,
				"model":  defaultModel,
			},
		}
		cfg["defaultProvider"] = "claude-api"
	case 2: // OpenAI-compatible
		cfg["providers"] = map[string]any{
			"openai": map[string]any{
				"type":     "openai",
				"endpoint": openaiEndpoint,
				"apiKey":   openaiAPIKey,
				"model":    defaultModel,
			},
		}
		cfg["defaultProvider"] = "openai"
	}

	// Create directories.
	for _, d := range []string{
		configDir,
		filepath.Join(configDir, "bin"),
		filepath.Join(configDir, "logs"),
		filepath.Join(configDir, "sessions"),
		filepath.Join(configDir, "outputs"),
		defaultWorkdir,
	} {
		os.MkdirAll(d, 0o755)
	}

	// Write config.
	data, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(configPath, append(data, '\n'), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Create empty jobs.json if not exists.
	jobsPath := filepath.Join(configDir, "jobs.json")
	if _, err := os.Stat(jobsPath); os.IsNotExist(err) {
		os.WriteFile(jobsPath, []byte("{\n  \"jobs\": []\n}\n"), 0o644)
	}

	fmt.Printf("\nConfig written: %s\n", configPath)
	fmt.Printf("API token: %s\n", apiToken)
	fmt.Println("(Save this token — needed for CLI/API access)")

	// --- Deferred: OpenClaw role import (config must exist first) ---
	if ocMigrated && ocReport != nil {
		include := parseIncludeList("roles")
		roleReport := &MigrationReport{}
		migCfg.baseDir = configDir
		if err := migrateOpenClawRoles(migCfg, ocDir, false, roleReport); err != nil {
			fmt.Fprintf(os.Stderr, "  Role import error: %v\n", err)
		} else if roleReport.RolesImported > 0 {
			ocReport.RolesImported = roleReport.RolesImported
			// Enable smart dispatch when roles exist.
			enableSmartDispatch(configPath)
			fmt.Printf("  Imported %d role(s) from OpenClaw.\n", roleReport.RolesImported)
			for _, w := range roleReport.Warnings {
				fmt.Printf("  \u26a0 %s\n", w)
			}
			for _, e := range roleReport.Errors {
				fmt.Printf("  \u2717 %s\n", e)
			}
		}
		_ = include
	}

	// --- Optional: Create first role ---
	if ocMigrated && ocReport != nil && ocReport.RolesImported > 0 {
		goto afterRole
	}
	fmt.Println()
	fmt.Print("  Create a first role? [Y/n]: ")
	scanner.Scan()
	if strings.ToLower(strings.TrimSpace(scanner.Text())) != "n" {
		fmt.Println()
		roleName := prompt("Role name", "default")

		// Archetype selection.
		fmt.Println()
		fmt.Println("  Start from a template?")
		for i, a := range builtinArchetypes {
			fmt.Printf("    %d. %-12s %s\n", i+1, a.Name, a.Description)
		}
		fmt.Printf("    %d. %-12s Start from scratch\n", len(builtinArchetypes)+1, "blank")
		archChoice := prompt(fmt.Sprintf("Choose [1-%d]", len(builtinArchetypes)+1), fmt.Sprintf("%d", len(builtinArchetypes)+1))

		var archetype *RoleArchetype
		if n, err := strconv.Atoi(archChoice); err == nil && n >= 1 && n <= len(builtinArchetypes) {
			archetype = &builtinArchetypes[n-1]
		}

		archModel := defaultModel
		defaultPerm := "acceptEdits"
		if archetype != nil {
			archModel = archetype.Model
			defaultPerm = archetype.PermissionMode
		}

		roleModel := prompt("Role model", archModel)
		roleDesc := prompt("Description", "Default agent role")
		rolePerm := prompt("Permission mode (plan|acceptEdits|autoEdit|bypassPermissions)", defaultPerm)

		// Validate permission mode.
		validPerms := []string{"plan", "acceptEdits", "autoEdit", "bypassPermissions"}
		permOK := false
		for _, v := range validPerms {
			if rolePerm == v {
				permOK = true
				break
			}
		}
		if !permOK {
			fmt.Printf("  Unknown permission mode %q, using acceptEdits\n", rolePerm)
			rolePerm = "acceptEdits"
		}

		var roleSoul string
		if archetype != nil {
			roleSoul = fmt.Sprintf("SOUL-%s.md", roleName)
			soulDst := filepath.Join(defaultWorkdir, roleSoul)
			if _, err := os.Stat(soulDst); os.IsNotExist(err) {
				content := generateSoulContent(archetype, roleName)
				os.WriteFile(soulDst, []byte(content), 0o644)
				fmt.Printf("  Created soul file: %s\n", soulDst)
			}
		} else {
			roleSoul = prompt("Soul file path (relative to workdir, empty for template)", "")
			if roleSoul == "" {
				roleSoul = "SOUL.md"
				soulDst := filepath.Join(defaultWorkdir, roleSoul)
				if _, err := os.Stat(soulDst); os.IsNotExist(err) {
					content := generateSoulContent(&RoleArchetype{SoulTemplate: `# {{.RoleName}} — Soul File

## Identity
You are {{.RoleName}}, a specialized AI agent in the Tetora orchestration system.

## Core Directives
- Focus on your designated area of expertise
- Produce actionable, concise outputs
- Record decisions and reasoning in your work artifacts

## Behavioral Guidelines
- Communicate in the team's primary language
- Follow established project conventions
- Prioritize quality over speed

## Output Format
- Start with a brief summary of what was accomplished
- Include key findings or deliverables
- Note any issues or follow-up items
`}, roleName)
					os.WriteFile(soulDst, []byte(content), 0o644)
					fmt.Printf("  Created soul file: %s\n", soulDst)
				}
			}
		}

		// Add role to config.
		rc := RoleConfig{
			SoulFile:       roleSoul,
			Model:          roleModel,
			Description:    roleDesc,
			PermissionMode: rolePerm,
		}
		if err := updateConfigRoles(configPath, roleName, &rc); err != nil {
			fmt.Fprintf(os.Stderr, "  Error saving role: %v\n", err)
		} else {
			fmt.Printf("  Role %q added.\n", roleName)
		}
	}
afterRole:

	// --- Optional: Install service ---
	fmt.Println()
	fmt.Print("  Install as launchd service? [y/N]: ")
	scanner.Scan()
	if strings.ToLower(strings.TrimSpace(scanner.Text())) == "y" {
		serviceInstall()
	}

	// Final summary.
	fmt.Println()
	fmt.Printf("Config: %s\n", configPath)
	fmt.Printf("Jobs:   %s\n", jobsPath)
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println("  tetora doctor      Verify setup")
	fmt.Println("  tetora status      Quick overview")
	fmt.Println("  tetora serve       Start daemon")
	fmt.Println("  tetora dashboard   Open web UI")
}

// enableSmartDispatch sets smartDispatch.enabled=true in the config file.
func enableSmartDispatch(configPath string) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return
	}
	var raw map[string]any
	if json.Unmarshal(data, &raw) != nil {
		return
	}
	sd, _ := raw["smartDispatch"].(map[string]any)
	if sd == nil {
		sd = map[string]any{}
	}
	sd["enabled"] = true
	raw["smartDispatch"] = sd
	out, _ := json.MarshalIndent(raw, "", "  ")
	os.WriteFile(configPath, append(out, '\n'), 0o644)
}

// cmdImportOpenClaw handles "tetora import openclaw" as a standalone command.
func cmdImportOpenClaw() {
	ocDir := detectOpenClaw()
	if ocDir == "" {
		fmt.Println("No OpenClaw installation found at ~/.openclaw/")
		return
	}
	fmt.Printf("Found OpenClaw at %s\n", ocDir)

	configPath := findConfigPath()
	if configPath == "" {
		fmt.Println("No Tetora config found. Run 'tetora init' first.")
		return
	}

	cfg, err := tryLoadConfig(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		return
	}

	include := parseIncludeList("all")
	report, err := migrateOpenClaw(cfg, ocDir, false, include, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Migration error: %v\n", err)
		return
	}

	fmt.Printf("Imported: %d config fields, %d memory files, %d workspace files, %d skills, %d cron jobs, %d roles\n",
		report.ConfigMerged, report.MemoryFiles, report.WorkspaceFiles, report.SkillsImported, report.CronJobs, report.RolesImported)
	for _, w := range report.Warnings {
		fmt.Printf("  \u26a0 %s\n", w)
	}
	for _, e := range report.Errors {
		fmt.Printf("  \u2717 %s\n", e)
	}

	if report.RolesImported > 0 {
		enableSmartDispatch(configPath)
		fmt.Println("Smart dispatch enabled.")
	}

	fmt.Println("\nRestart the service to apply changes:")
	fmt.Println("  tetora service uninstall && tetora service install")
}

func detectClaude() string {
	// Prefer Homebrew path, then npm, then PATH lookup.
	home, _ := os.UserHomeDir()
	for _, p := range []string{
		"/usr/local/bin/claude",
		filepath.Join(home, ".local", "bin", "claude"),
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if path, err := exec.LookPath("claude"); err == nil {
		return path
	}
	return "/usr/local/bin/claude"
}
