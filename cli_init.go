package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// randomListenAddr picks a random available port on 127.0.0.1 and returns the
// listen address (e.g. "127.0.0.1:52341"). Called once during init so the port
// is written to config and stays stable across restarts.
func randomListenAddr() string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "127.0.0.1:8991"
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

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

	// --- Language selection ---
	fmt.Println("Select language / 選擇語言 / 言語を選択 / 언어 선택:")
	langNames := []string{
		"English",
		"繁體中文",
		"日本語",
		"한국어",
		"Deutsch",
		"Español",
		"Français",
		"Bahasa Indonesia",
		"Filipino",
		"ภาษาไทย",
	}
	langCodes := []string{"en", "zh-TW", "ja", "ko", "de", "es", "fr", "id", "fil", "th"}
	langIdx := interactiveChoose(langNames, 0)
	if langIdx < 0 {
		langIdx = 0
	}
	selectedLang := langCodes[langIdx]
	L := initTranslations[selectedLang]
	// fallback to English if missing
	if L.Title == "" {
		L = initTranslations["en"]
	}
	fmt.Println()

	home, _ := os.UserHomeDir()
	configDir := filepath.Join(home, ".tetora")
	configPath := filepath.Join(configDir, "config.json")

	if _, err := os.Stat(configPath); err == nil {
		fmt.Printf("%s %s\n", L.ConfigExists, configPath)
		fmt.Printf("  %s ", L.OverwritePrompt)
		scanner.Scan()
		if strings.ToLower(strings.TrimSpace(scanner.Text())) != "y" {
			fmt.Println(L.Aborted)
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
		fmt.Printf("%s %s\n", L.OpenClawDetected, ocDir)
		fmt.Printf("  %s ", L.OpenClawImport)
		scanner.Scan()
		ans := strings.ToLower(strings.TrimSpace(scanner.Text()))
		if ans != "n" {
			// Extract only basic config fields (tokens, model).
			include := parseIncludeList("config")
			report, err := migrateOpenClaw(migCfg, ocDir, false, include, false)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  Migration error: %v\n", err)
			} else {
				ocMigrated = true
				ocReport = report
				fmt.Printf("  "+L.OpenClawImported+"\n", report.ConfigMerged)
				for _, w := range report.Warnings {
					fmt.Printf("  ! %s\n", w)
				}
			}
			fmt.Println()
			fmt.Printf("  %s\n", L.OpenClawMigrateNote1)
			fmt.Printf("  %s\n", L.OpenClawMigrateNote2)
			fmt.Println()
		}
	}
	_ = ocReport // used below if ocMigrated

	fmt.Println(L.Title)
	fmt.Println()

	// --- Step 1: Channel ---
	fmt.Println(L.Step1Title)
	fmt.Println()
	channelIdx := choose("Channel", []string{
		L.ChannelOptions[0],
		L.ChannelOptions[1],
		L.ChannelOptions[2],
		L.ChannelOptions[3],
	}, 0)

	var botToken string
	var chatID int64
	var discordToken, discordAppID, discordChannelID string
	var slackToken, slackSigningSecret string

	switch channelIdx {
	case 0: // Telegram
		fmt.Println()
		fmt.Printf("  \033[2m%s\n", L.TelegramHint1)
		fmt.Printf("    %s\n", L.TelegramHint2)
		fmt.Printf("    %s\n", L.TelegramHint3)
		fmt.Printf("    %s\033[0m\n", L.TelegramHint4)
		fmt.Println()
		botToken = prompt(L.TelegramTokenPrompt, "")
		cidStr := prompt(L.TelegramChatIDPrompt, "")
		chatID, _ = strconv.ParseInt(cidStr, 10, 64)
	case 1: // Discord
		fmt.Println()
		fmt.Printf("  \033[2m%s\n", L.DiscordHint1)
		fmt.Printf("    %s\n", L.DiscordHint2)
		fmt.Printf("    %s\n", L.DiscordHint3)
		fmt.Printf("    %s\n", L.DiscordHint4)
		fmt.Printf("    %s\n", L.DiscordHint5)
		fmt.Printf("    %s\n", L.DiscordHint6)
		fmt.Printf("    %s\033[0m\n", L.DiscordHint7)
		fmt.Println()
		discordToken = prompt(L.DiscordTokenPrompt, "")
		discordAppID = prompt(L.DiscordAppIDPrompt, "")
		discordChannelID = prompt(L.DiscordChannelPrompt, "")
	case 2: // Slack
		fmt.Println()
		fmt.Printf("  \033[2m%s\n", L.SlackHint1)
		fmt.Printf("    %s\n", L.SlackHint2)
		fmt.Printf("    %s\033[0m\n", L.SlackHint3)
		fmt.Println()
		slackToken = prompt(L.SlackTokenPrompt, "")
		slackSigningSecret = prompt(L.SlackSigningSecretPrompt, "")
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
	fmt.Println(L.Step2Title)
	fmt.Println()
	providerIdx := choose("Provider", []string{
		L.ProviderOptions[0],
		L.ProviderOptions[1],
		L.ProviderOptions[2],
	}, 0)

	claudePath := ""
	var claudeAPIKey, openaiEndpoint, openaiAPIKey, defaultModel string

	switch providerIdx {
	case 0: // Claude CLI
		fmt.Println()
		fmt.Printf("  \033[2m%s\n", L.ClaudeCLIHint1)
		fmt.Printf("  %s\n", L.ClaudeCLIHint2)
		fmt.Printf("  %s\n", L.ClaudeCLIHint3)
		fmt.Printf("  %s\n", L.ClaudeCLIHint4)
		fmt.Printf("  %s\033[0m\n", L.ClaudeCLIHint5)
		fmt.Println()
		detected := detectClaude()
		claudePath = prompt(L.ClaudeCLIPathPrompt, detected)
		defaultModel = prompt(L.DefaultModelPrompt, "sonnet")
	case 1: // Claude API
		fmt.Println()
		fmt.Printf("  \033[2m%s\n", L.ClaudeAPIHint1)
		fmt.Printf("  %s\n", L.ClaudeAPIHint2)
		fmt.Printf("  %s\033[0m\n", L.ClaudeAPIHint3)
		fmt.Println()
		claudeAPIKey = prompt(L.ClaudeAPIKeyPrompt, "")
		defaultModel = prompt(L.DefaultModelPrompt, "claude-sonnet-4-5-20250929")
	case 2: // OpenAI-compatible
		openaiEndpoint = prompt(L.OpenAIEndpointPrompt, "https://api.openai.com/v1")
		openaiAPIKey = prompt(L.OpenAIKeyPrompt, "")
		defaultModel = prompt(L.DefaultModelPrompt, "gpt-4o")
	}

	if ocMigrated {
		if defaultModel == "" && migCfg.DefaultModel != "" {
			defaultModel = migCfg.DefaultModel
			fmt.Printf("  (using model from OpenClaw: %s)\n", defaultModel)
		}
	}

	// --- Step 3: Directory Access ---
	fmt.Println()
	fmt.Println(L.Step3Title)
	fmt.Println()
	fmt.Printf("  %s\n", L.Step3Note1)
	fmt.Printf("  %s\n", L.Step3Note2)
	fmt.Println()
	accessIdx := choose("Access", []string{
		L.DirOptions[0],
		L.DirOptions[1],
		L.DirOptions[2],
	}, 0)

	var defaultAddDirs []string
	switch accessIdx {
	case 0:
		defaultAddDirs = []string{"~"}
	case 1:
		dirInput := prompt(L.DirInputPrompt, "~/Development")
		for _, d := range strings.Split(dirInput, ",") {
			d = strings.TrimSpace(d)
			if d != "" {
				defaultAddDirs = append(defaultAddDirs, d)
			}
		}
	case 2:
		// No extra dirs, only ~/.tetora/ (always included)
	}

	// --- Step 4: Generate ---
	fmt.Println()
	fmt.Println(L.Step4Title)

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
		"listenAddr":            randomListenAddr(),
		"jobsFile":              "jobs.json",
		"apiToken":              apiToken,
		"log":                   true,
	}

	// Add defaultAddDirs if configured.
	if len(defaultAddDirs) > 0 {
		cfg["defaultAddDirs"] = defaultAddDirs
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
	fmt.Printf("%s %s\n", L.APITokenLabel, apiToken)
	fmt.Println(L.APITokenNote)

	// OpenClaw workspace/agents migration is now handled by `tetora import openclaw`.
	_ = ocMigrated

	// --- Optional: Create agents ---
	var createdAgents []string
	var initDefaultAgent string

	createRole := func() string {
		fmt.Println()
		agentName := prompt(L.RoleNamePrompt, "default")

		// Archetype selection.
		fmt.Println()
		fmt.Printf("  %s\n", L.ArchetypeTitle)
		for i, a := range builtinArchetypes {
			fmt.Printf("    %d. %-12s %s\n", i+1, a.Name, a.Description)
		}
		fmt.Printf("    %d. %-12s %s\n", len(builtinArchetypes)+1, "blank", L.ArchetypeBlank)
		archChoice := prompt(fmt.Sprintf(L.ArchetypeChoosePrompt, len(builtinArchetypes)+1), fmt.Sprintf("%d", len(builtinArchetypes)+1))

		var archetype *AgentArchetype
		if n, err := strconv.Atoi(archChoice); err == nil && n >= 1 && n <= len(builtinArchetypes) {
			archetype = &builtinArchetypes[n-1]
		}

		archModel := defaultModel
		defaultPerm := "acceptEdits"
		if archetype != nil {
			archModel = archetype.Model
			defaultPerm = archetype.PermissionMode
		}

		roleModel := prompt(L.RoleModelPrompt, archModel)
		roleDesc := prompt(L.RoleDescPrompt, "Default agent")
		rolePerm := prompt(L.RolePermPrompt, defaultPerm)

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
			fmt.Printf("  "+L.RolePermInvalid+"\n", rolePerm)
			rolePerm = "acceptEdits"
		}

		// Per-agent directory: ~/.tetora/agents/{agentName}/
		agentDir := filepath.Join(configDir, "agents", agentName)
		os.MkdirAll(agentDir, 0o755)

		soulDst := filepath.Join(agentDir, "SOUL.md")
		if archetype != nil {
			if _, err := os.Stat(soulDst); os.IsNotExist(err) {
				content := generateSoulContent(archetype, agentName)
				os.WriteFile(soulDst, []byte(content), 0o644)
				fmt.Printf("  Created soul file: %s\n", soulDst)
			}
		} else {
			customPath := prompt(L.SoulFilePrompt, "")
			if customPath != "" {
				if data, err := os.ReadFile(customPath); err == nil {
					os.WriteFile(soulDst, data, 0o644)
					fmt.Printf("  Copied soul file to: %s\n", soulDst)
				} else {
					fmt.Printf("  Cannot read %s, creating template instead\n", customPath)
					customPath = ""
				}
			}
			if customPath == "" {
				if _, err := os.Stat(soulDst); os.IsNotExist(err) {
					content := generateSoulContent(&AgentArchetype{SoulTemplate: `# {{.RoleName}} — Soul File

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
`}, agentName)
					os.WriteFile(soulDst, []byte(content), 0o644)
					fmt.Printf("  Created soul file: %s\n", soulDst)
				}
			}
		}

		// Add agent to config.
		rc := AgentConfig{
			SoulFile:       "SOUL.md",
			Model:          roleModel,
			Description:    roleDesc,
			PermissionMode: rolePerm,
		}
		if err := updateConfigAgents(configPath, agentName, &rc); err != nil {
			fmt.Fprintf(os.Stderr, "  "+L.RoleError+"\n", err)
			return ""
		}
		fmt.Printf("  "+L.RoleAdded+"\n", agentName)
		return agentName
	}

	if ocMigrated {
		fmt.Println()
		fmt.Print("  " + L.CreateRoleOCPrompt + " ")
		scanner.Scan()
		if strings.ToLower(strings.TrimSpace(scanner.Text())) != "y" {
			goto afterRole
		}
	} else {
		fmt.Println()
		fmt.Print("  " + L.CreateRolePrompt + " ")
		scanner.Scan()
		if strings.ToLower(strings.TrimSpace(scanner.Text())) == "n" {
			goto afterRole
		}
	}

	// Create first agent.
	if name := createRole(); name != "" {
		createdAgents = append(createdAgents, name)

		// Ask to set as default agent.
		fmt.Println()
		fmt.Printf("  "+L.SetDefaultAgentPrompt+" ", name)
		scanner.Scan()
		if strings.ToLower(strings.TrimSpace(scanner.Text())) != "n" {
			initDefaultAgent = name
			updateConfigField(configPath, func(raw map[string]any) {
				raw["defaultAgent"] = name
			})
			fmt.Printf("  "+L.DefaultAgentSet+"\n", name)
		}

		// If Discord was chosen and a default agent is set, offer auto-routing.
		if channelIdx == 1 && initDefaultAgent != "" && discordChannelID != "" {
			fmt.Println()
			fmt.Printf("  "+L.AutoRouteDiscordPrompt+" ", initDefaultAgent)
			scanner.Scan()
			if strings.ToLower(strings.TrimSpace(scanner.Text())) != "n" {
				updateConfigDiscordRoutes(configPath, discordChannelID, initDefaultAgent)
				fmt.Printf("  "+L.AutoRouteDiscordDone+"\n", initDefaultAgent)
			}
		}

		// "Add another agent?" loop.
		for {
			fmt.Println()
			fmt.Printf("  %s ", L.AddAnotherRolePrompt)
			scanner.Scan()
			if strings.ToLower(strings.TrimSpace(scanner.Text())) != "y" {
				break
			}
			if name := createRole(); name != "" {
				createdAgents = append(createdAgents, name)
			}
		}

		// Auto-enable SmartDispatch when 2+ agents exist.
		if len(createdAgents) >= 2 {
			fmt.Println()
			fmt.Printf("  %s ", L.EnableSmartDispatch)
			scanner.Scan()
			if strings.ToLower(strings.TrimSpace(scanner.Text())) != "n" {
				coordinator := initDefaultAgent
				if coordinator == "" {
					coordinator = createdAgents[0]
				}
				updateConfigField(configPath, func(raw map[string]any) {
					sd, _ := raw["smartDispatch"].(map[string]any)
					if sd == nil {
						sd = map[string]any{}
					}
					sd["enabled"] = true
					sd["coordinator"] = coordinator
					sd["defaultAgent"] = coordinator
					raw["smartDispatch"] = sd
				})
				fmt.Printf("  %s\n", L.SmartDispatchEnabled)
			}
		}
	}
afterRole:

	// --- Optional: Install service ---
	fmt.Println()
	fmt.Printf("  %s ", L.ServiceInstallPrompt)
	scanner.Scan()
	if strings.ToLower(strings.TrimSpace(scanner.Text())) == "y" {
		serviceInstall()
	}

	// Final summary.
	fmt.Println()
	fmt.Printf("%s %s\n", L.FinalConfig, configPath)
	fmt.Printf("%s %s\n", L.FinalJobs, jobsPath)
	fmt.Println()
	fmt.Println(L.NextSteps)
	fmt.Println(L.NextDoctor)
	fmt.Println(L.NextStatus)
	fmt.Println(L.NextServe)
	fmt.Println(L.NextDashboard)
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

// updateConfigDiscordRoutes adds a channelID→role route to the discord config.
func updateConfigDiscordRoutes(configPath, channelID, role string) {
	updateConfigField(configPath, func(raw map[string]any) {
		discord, _ := raw["discord"].(map[string]any)
		if discord == nil {
			return
		}
		// Ensure channelIDs includes this channel.
		var channelIDs []any
		if existing, ok := discord["channelIDs"].([]any); ok {
			channelIDs = existing
		}
		found := false
		for _, id := range channelIDs {
			if fmt.Sprint(id) == channelID {
				found = true
				break
			}
		}
		if !found {
			channelIDs = append(channelIDs, channelID)
			discord["channelIDs"] = channelIDs
		}
		raw["discord"] = discord
	})
}

// cmdImportOpenClaw is now defined in import_openclaw.go (3-stage pipeline).

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
