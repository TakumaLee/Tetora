package cli

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"tetora/internal/config"
	"tetora/internal/i18n"
	"tetora/internal/provider"
)

// InitDeps holds the root-package callbacks that CmdInit needs to invoke.
// This avoids importing root package from internal/cli.
type InitDeps struct {
	// SeedDefaultJobsJSON returns the JSON bytes for jobs.json (already marshalled).
	SeedDefaultJobsJSON func() ([]byte, error)
	// GenerateMCPBridge writes the MCP bridge config for the given parameters.
	GenerateMCPBridge func(baseDir, listenAddr, apiToken string) error
	// InstallHooks installs Claude Code hooks for the given listen address.
	InstallHooks func(listenAddr string) error
}

// --- Interactive menu helpers ---

const (
	menuKeyNone  = 0
	menuKeyUp    = 1
	menuKeyDown  = 2
	menuKeyEnter = 3
	menuKeyQuit  = 4
)


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

// CmdInit is the `tetora init` interactive setup wizard.
func CmdInit(deps InitDeps) {
	skipOnboarding := false
	for _, arg := range os.Args[2:] {
		if arg == "--skip-onboarding" {
			skipOnboarding = true
		}
	}

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
		nv, _ := strconv.Atoi(s)
		if nv < 1 || nv > len(options) {
			return defaultIdx
		}
		return nv - 1
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
	L := i18n.Translations[selectedLang]
	// fallback to English if missing
	if L.Title == "" {
		L = i18n.Translations["en"]
	}
	fmt.Println()

	home, _ := os.UserHomeDir()
	configDir := filepath.Join(home, ".tetora")
	configPath := filepath.Join(configDir, "config.json")

	isFirstTime := true
	if _, err := os.Stat(configPath); err == nil {
		isFirstTime = false
		fmt.Printf("%s %s\n", L.ConfigExists, configPath)
		fmt.Printf("  %s ", L.OverwritePrompt)
		scanner.Scan()
		if strings.ToLower(strings.TrimSpace(scanner.Text())) != "y" {
			fmt.Println(L.Aborted)
			return
		}
		fmt.Println()
	}

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

	// --- Step 2: Provider ---
	fmt.Println()
	fmt.Println(L.Step2Title)
	fmt.Println()

	// Build provider options: Claude CLI first, then presets.
	providerLabels := []string{L.ProviderOptions[0]} // "Claude CLI (local binary)"
	for _, p := range provider.Presets {
		providerLabels = append(providerLabels, p.DisplayName)
	}
	providerIdx := choose("Provider", providerLabels, 0)

	claudePath := ""
	var defaultModel string
	var selectedPreset *provider.Preset
	var presetAPIKey string

	if providerIdx == 0 {
		// Claude CLI (existing flow).
		fmt.Println()
		fmt.Printf("  \033[2m%s\n", L.ClaudeCLIHint1)
		fmt.Printf("  %s\n", L.ClaudeCLIHint2)
		fmt.Printf("  %s\n", L.ClaudeCLIHint3)
		fmt.Printf("  %s\n", L.ClaudeCLIHint4)
		fmt.Printf("  %s\033[0m\n", L.ClaudeCLIHint5)
		fmt.Println()
		detected := DetectClaude()
		claudePath = prompt(L.ClaudeCLIPathPrompt, detected)
		defaultModel = prompt(L.DefaultModelPrompt, "sonnet")
	} else {
		// Preset-based provider.
		preset := provider.Presets[providerIdx-1]
		selectedPreset = &preset
		fmt.Println()

		if preset.RequiresKey {
			presetAPIKey = prompt(fmt.Sprintf("  %s API key:", preset.DisplayName), "")
		}

		if preset.BaseURL == "" {
			// Custom preset — ask for base URL.
			selectedPreset.BaseURL = prompt("  Base URL:", "https://api.openai.com/v1")
		}

		// Model selection.
		modelDefault := ""
		models := preset.Models
		if preset.Dynamic {
			fmt.Printf("  \033[2mFetching models from %s...\033[0m\n", preset.BaseURL)
			if fetched, err := provider.FetchPresetModels(preset); err == nil && len(fetched) > 0 {
				models = fetched
			} else if err != nil {
				fmt.Printf("  \033[33mCould not fetch models: %v\033[0m\n", err)
			}
		}
		if len(models) > 0 {
			fmt.Println("  Available models:")
			modelIdx := interactiveChoose(models, 0)
			if modelIdx >= 0 && modelIdx < len(models) {
				modelDefault = models[modelIdx]
			}
		}
		defaultModel = prompt(L.DefaultModelPrompt, modelDefault)
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

	// Listen address: local-only or all interfaces (required for Tailscale / remote access).
	fmt.Println()
	fmt.Println("Network access:")
	listenHost := "127.0.0.1"
	if interactiveChoose([]string{
		"Local only (127.0.0.1) — more secure",
		"All interfaces (0.0.0.0) — required for Tailscale / remote access",
	}, 0) == 1 {
		listenHost = "0.0.0.0"
	}
	listenAddr := RandomListenPort(listenHost)

	// Default task timeout.
	fmt.Println()
	fmt.Println("Default task timeout:")
	timeoutLabels := []string{"5m — quick tasks", "15m — balanced (Recommended)", "30m — longer tasks", "1h — complex / research"}
	timeoutVals := []string{"5m", "15m", "30m", "1h"}
	timeoutIdx := interactiveChoose(timeoutLabels, 1)
	if timeoutIdx < 0 {
		timeoutIdx = 1
	}
	defaultTimeout := timeoutVals[timeoutIdx]

	// Daily cost alert.
	fmt.Println()
	dailyCostStr := prompt("Daily cost alert in USD (0 = disable):", "0")
	dailyCost, _ := strconv.ParseFloat(strings.TrimSpace(dailyCostStr), 64)

	defaultWorkdir := filepath.Join(configDir, "workspace")

	// Generate API token.
	tokenBytes := make([]byte, 32)
	rand.Read(tokenBytes)
	apiToken := hex.EncodeToString(tokenBytes)

	// Build config.
	cfg := map[string]any{
		"maxConcurrent":         3,
		"defaultModel":          defaultModel,
		"defaultTimeout":        defaultTimeout,
		"defaultBudget":         2.0,
		"defaultPermissionMode": "acceptEdits",
		"defaultWorkdir":        defaultWorkdir,
		"listenAddr":            listenAddr,
		"jobsFile":              "jobs.json",
		"apiToken":              apiToken,
		"log":                   true,
	}

	// Cost alert.
	if dailyCost > 0 {
		cfg["costAlert"] = map[string]any{
			"dailyLimit": dailyCost,
			"action":     "warn",
		}
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
	if selectedPreset != nil {
		pc := map[string]any{
			"type":    selectedPreset.Type,
			"baseUrl": selectedPreset.BaseURL,
			"model":   defaultModel,
		}
		if presetAPIKey != "" {
			pc["apiKey"] = presetAPIKey
		}
		cfg["providers"] = map[string]any{
			selectedPreset.Name: pc,
		}
		cfg["defaultProvider"] = selectedPreset.Name
	}

	// TaskBoard toggle.
	fmt.Printf("\n%s\n", "Enable Task Board? (auto-dispatch + backlog triage)")
	enableTB := interactiveChoose([]string{"Yes (Recommended)", "No"}, 0)
	if enableTB == 0 {
		cfg["taskBoard"] = map[string]any{
			"enabled":    true,
			"maxRetries": 3,
			"autoDispatch": map[string]any{
				"enabled":  true,
				"interval": "5m",
			},
		}
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
	if err := os.WriteFile(configPath, append(data, '\n'), 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Create jobs.json with default jobs if not exists.
	jobsPath := filepath.Join(configDir, "jobs.json")
	if _, err := os.Stat(jobsPath); os.IsNotExist(err) {
		if deps.SeedDefaultJobsJSON != nil {
			if jobsData, err := deps.SeedDefaultJobsJSON(); err == nil {
				os.WriteFile(jobsPath, append(jobsData, '\n'), 0o600)
			}
		}
	}

	fmt.Printf("\nConfig written: %s\n", configPath)
	fmt.Printf("%s %s\n", L.APITokenLabel, apiToken)
	fmt.Println(L.APITokenNote)

	// Connection test (warn-only on failure).
	if selectedPreset != nil {
		fmt.Printf("  Testing connection to %s...", selectedPreset.DisplayName)
		if err := provider.TestPresetConnection(*selectedPreset, presetAPIKey, defaultModel); err != nil {
			fmt.Printf(" \033[33m⚠ %v\033[0m\n", err)
		} else {
			fmt.Printf(" \033[32m✓ OK\033[0m\n")
		}
	}

	// --- Optional: Create agents ---
	var createdAgents []string
	var initDefaultAgent string

	createRole := func() string {
		fmt.Println()
		agentName := prompt(L.RoleNamePrompt, "default")

		// Archetype selection.
		fmt.Println()
		fmt.Printf("  %s\n", L.ArchetypeTitle)
		for i, a := range BuiltinArchetypes {
			fmt.Printf("    %d. %-12s %s\n", i+1, a.Name, a.Description)
		}
		fmt.Printf("    %d. %-12s %s\n", len(BuiltinArchetypes)+1, "blank", L.ArchetypeBlank)
		archChoice := prompt(fmt.Sprintf(L.ArchetypeChoosePrompt, len(BuiltinArchetypes)+1), fmt.Sprintf("%d", len(BuiltinArchetypes)+1))

		var archetype *AgentArchetype
		if nv, err := strconv.Atoi(archChoice); err == nil && nv >= 1 && nv <= len(BuiltinArchetypes) {
			archetype = &BuiltinArchetypes[nv-1]
		}

		archModel := defaultModel
		defaultPerm := "acceptEdits"
		if archetype != nil {
			// For preset-based providers, use the provider's default model instead of hardcoded Claude models.
			if selectedPreset == nil {
				archModel = archetype.Model
			}
			defaultPerm = archetype.PermissionMode
		}

		roleModel := prompt(L.RoleModelPrompt, archModel)
		roleDesc := prompt(L.RoleDescPrompt, "Default agent")
		rolePerm := prompt(L.RolePermPrompt, defaultPerm)

		// Validate permission mode.
		validPerms := []string{"plan", "acceptEdits", "auto", "bypassPermissions"}
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
				content := GenerateSoulContent(archetype, agentName)
				os.WriteFile(soulDst, []byte(content), 0o644)
				fmt.Printf("  Created soul file: %s\n", soulDst)
			}
		} else {
			customPath := prompt(L.SoulFilePrompt, "")
			if customPath != "" {
				if soulData, err := os.ReadFile(customPath); err == nil {
					os.WriteFile(soulDst, soulData, 0o644)
					fmt.Printf("  Copied soul file to: %s\n", soulDst)
				} else {
					fmt.Printf("  Cannot read %s, creating template instead\n", customPath)
					customPath = ""
				}
			}
			if customPath == "" {
				if _, err := os.Stat(soulDst); os.IsNotExist(err) {
					content := GenerateSoulContent(&AgentArchetype{SoulTemplate: `# {{.RoleName}} — Soul File

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
		rc := config.AgentConfig{
			SoulFile:       "SOUL.md",
			Model:          roleModel,
			Description:    roleDesc,
			PermissionMode: rolePerm,
		}
		rcJSON, err := json.Marshal(&rc)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  "+L.RoleError+"\n", err)
			return ""
		}
		if err := UpdateConfigAgents(configPath, agentName, rcJSON); err != nil {
			fmt.Fprintf(os.Stderr, "  "+L.RoleError+"\n", err)
			return ""
		}
		fmt.Printf("  "+L.RoleAdded+"\n", agentName)
		return agentName
	}

	{
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
			MutateConfig(configPath, func(raw map[string]any) {
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
				MutateConfig(configPath, func(raw map[string]any) {
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
		ServiceInstall()
	}

	// --- Optional: Install Claude Code hooks ---
	fmt.Println()
	fmt.Printf("  Install Claude Code hooks for real-time monitoring? (y/N) ")
	scanner.Scan()
	if strings.ToLower(strings.TrimSpace(scanner.Text())) == "y" {
		if deps.InstallHooks != nil {
			if err := deps.InstallHooks(listenAddr); err != nil {
				fmt.Printf("  Warning: %v\n", err)
			}
		}
		if deps.GenerateMCPBridge != nil {
			if err := deps.GenerateMCPBridge(configDir, listenAddr, apiToken); err != nil {
				fmt.Printf("  Warning: MCP bridge config: %v\n", err)
			} else {
				fmt.Printf("  MCP bridge config: %s/mcp/bridge.json\n", configDir)
			}
		}
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

	// Kirara onboarding: guide first-time users through creating their first agent.
	// Use claudePath if set (claude-code provider), otherwise try to detect claude binary.
	effectiveClaude := claudePath
	if effectiveClaude == "" {
		effectiveClaude = DetectClaude()
	}
	if isFirstTime && !skipOnboarding && effectiveClaude != "" {
		fmt.Println()
		RunKiraraOnboarding(scanner, configPath, configDir, effectiveClaude, selectedLang)
	}
}

// enableSmartDispatch sets smartDispatch.enabled=true in the config file.
func enableSmartDispatch(configPath string) {
	MutateConfig(configPath, func(raw map[string]any) {
		sd, _ := raw["smartDispatch"].(map[string]any)
		if sd == nil {
			sd = map[string]any{}
		}
		sd["enabled"] = true
		raw["smartDispatch"] = sd
	})
}

// updateConfigDiscordRoutes adds a channelID→role route to the discord config.
func updateConfigDiscordRoutes(configPath, channelID, role string) {
	MutateConfig(configPath, func(raw map[string]any) {
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
