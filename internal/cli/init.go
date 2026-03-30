package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"tetora/internal/config"
	"tetora/internal/i18n"
)

// InitDeps holds the root-package callbacks that CmdInit needs to invoke.
type InitDeps struct {
	SeedDefaultJobsJSON func() ([]byte, error)
	GenerateMCPBridge   func(baseDir, listenAddr, apiToken string) error
	InstallHooks        func(listenAddr string) error
}

func checkDependencies() {
	fmt.Println("  \033[36mDiagnosing environment dependencies...\033[0m")
	deps := []struct {
		name    string
		cmd     string
		args    []string
		version string
	}{
		{"sqlite3", "sqlite3", []string{"--version"}, "required for database access (no-cgo)"},
		{"go", "go", []string{"version"}, "required for building and development"},
		{"gh", "gh", []string{"--version"}, "optional for GitHub pull request integration"},
	}

	for _, d := range deps {
		out, err := exec.Command(d.cmd, d.args...).Output()
		if err != nil {
			fmt.Printf("    \033[31m✘ %s\033[0m: Not found. %s\n", d.name, d.version)
		} else {
			v := strings.Split(string(out), "\n")[0]
			fmt.Printf("    \033[32m✓ %s\033[0m: %s\n", d.name, v)
		}
	}
	fmt.Println()
}

// CmdInit is the `tetora init` interactive setup wizard.
func CmdInit(deps InitDeps) {
	fmt.Println("\033[1mTetora Initialization Wizard\033[0m")
	fmt.Println("----------------------------")
	checkDependencies()

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
		"English", "繁體中文", "日本語", "한국어",
		"Deutsch", "Español", "Français", "Bahasa Indonesia", "Filipino", "ภาษาไทย",
	}
	langCodes := []string{"en", "zh-TW", "ja", "ko", "de", "es", "fr", "id", "fil", "th"}
	langIdx := interactiveChoose(langNames, 0)
	if langIdx < 0 {
		langIdx = 0
	}
	selectedLang := langCodes[langIdx]
	i18n.SetLang(selectedLang)
	fmt.Println()

	// --- Base Directory ---
	home, _ := os.UserHomeDir()
	configDir := prompt("  Base Directory", filepath.Join(home, ".tetora"))
	if err := os.MkdirAll(configDir, 0755); err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	configPath := filepath.Join(configDir, "config.json")

	// --- API Keys & .env setup ---
	fmt.Println("\n  \033[36mSetting up API Credentials...\033[0m")
	fmt.Println("  (Stored securely in .env, referenced in config.json as $ENV_VAR)")
	
	anthropicKey := prompt("  Anthropic API Key", "")
	groqKey := prompt("  Groq API Key (Optional, for fast STT)", "")
	serpKey := prompt("  SerpAPI Key (Optional, for web search)", "")

	dotEnv := fmt.Sprintf("ANTHROPIC_API_KEY=%s\n", anthropicKey)
	if groqKey != "" {
		dotEnv += fmt.Sprintf("GROQ_API_KEY=%s\n", groqKey)
	}
	if serpKey != "" {
		dotEnv += fmt.Sprintf("SERPAPI_KEY=%s\n", serpKey)
	}
	_ = os.WriteFile(filepath.Join(configDir, ".env"), []byte(dotEnv), 0600)

	// --- Agent Presets ---
	fmt.Println("\n  \033[36mChoose your initial Agent Cabinet preset:\033[0m")
	presets := []string{
		"Minimalist (Coordinator only)",
		"Engineering Team (Coordinator, Architect, Developer, Reviewer)",
		"Strategic Research (Coordinator, Analyst, Intelligence, Risk Advisor)",
	}
	presetIdx := choose("  Preset", presets, 1)

	agentConfigs := make(map[string]config.AgentConfig)
	switch presetIdx {
	case 0: // Minimalist
		agentConfigs["coordinator"] = config.AgentConfig{
			SoulFile:       "agents/coordinator/SOUL.md",
			Model:          "sonnet",
			PermissionMode: "acceptEdits",
		}
	case 1: // Engineering
		names := []string{"coordinator", "architect", "developer", "reviewer"}
		for _, name := range names {
			pm := "acceptEdits"
			if name == "architect" {
				pm = "plan"
			}
			agentConfigs[name] = config.AgentConfig{
				SoulFile:       filepath.Join("agents", name, "SOUL.md"),
				Model:          "sonnet",
				PermissionMode: pm,
			}
		}
	case 2: // Research
		names := []string{"coordinator", "analyst", "intelligence", "risk_advisor"}
		for _, name := range names {
			pm := "plan"
			if name == "coordinator" {
				pm = "acceptEdits"
			}
			agentConfigs[name] = config.AgentConfig{
				SoulFile:       filepath.Join("agents", name, "SOUL.md"),
				Model:          "sonnet",
				PermissionMode: pm,
			}
		}
	}

	// --- Final Config ---
	cfg := config.Config{
		BaseDir:      configDir,
		AgentsDir:    filepath.Join(configDir, "agents"),
		WorkspaceDir: filepath.Join(configDir, "workspace"),
		Agents:       agentConfigs,
		DefaultAgent: "coordinator",
		Providers: map[string]config.ProviderConfig{
			"anthropic": {Type: "anthropic", APIKey: "$ANTHROPIC_API_KEY"},
		},
	}
	if groqKey != "" {
		cfg.Providers["groq"] = config.ProviderConfig{Type: "groq", APIKey: "$GROQ_API_KEY"}
	}
	if serpKey != "" {
		cfg.Tools.WebSearch = config.WebSearchConfig{
			Provider: "serp",
			APIKey:   "$SERPAPI_KEY",
		}
	}

	// Generate folders and soul files
	for name := range agentConfigs {
		agentDir := filepath.Join(cfg.AgentsDir, name)
		_ = os.MkdirAll(agentDir, 0755)
		soulPath := filepath.Join(agentDir, "SOUL.md")
		localSoulPath := filepath.Join(agentDir, "SOUL.local.md")
		
		template := fmt.Sprintf("# %s Agent\n\nIdentity: You are a professional %s in the Tetora system.", strings.Title(name), name)
		_ = os.WriteFile(soulPath, []byte(template), 0644)
		
		localTemplate := fmt.Sprintf("# %s Agent (Local Override)\n\n# Add your personal identity here to override SOUL.md without affecting git.", strings.Title(name))
		_ = os.WriteFile(localSoulPath, []byte(localTemplate), 0644)
	}

	// Write config.json
	cfgData, _ := json.MarshalIndent(cfg, "", "  ")
	_ = os.WriteFile(configPath, cfgData, 0600)
	
	fmt.Printf("\n  \033[32m✓ Configuration successfully initialized in %s\033[0m\n", configPath)
	fmt.Println("  You can now run `tetora doctor` to verify your setup.")
}
