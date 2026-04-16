package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tetora/internal/config"
)

// CmdProvider is the entry point for the "tetora provider" subcommand.
func CmdProvider(args []string) {
	providerCmd(args)
}

func providerCmd(args []string) {
	if len(args) < 1 {
		printProviderUsage()
		os.Exit(1)
	}

	subCmd := args[0]

	switch subCmd {
	case "set", "use":
		providerSetCmd(args[1:])
	case "status", "show":
		providerStatusCmd()
	case "clear", "unset":
		providerClearCmd()
	case "list", "ls":
		providerListCmd()
	case "help", "--help", "-h":
		printProviderUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown provider command: %s\n", subCmd)
		printProviderUsage()
		os.Exit(1)
	}
}

func printProviderUsage() {
	fmt.Println(`Provider Management Commands:

  tetora provider set <name> [model]   Set active provider (overrides all agents)
  tetora provider use <name> [model]   Alias for 'set'
  tetora provider status               Show current active provider
  tetora provider show                 Alias for 'status'
  tetora provider clear                Remove active provider override
  tetora provider unset                Alias for 'clear'
  tetora provider list                 List all configured providers
  tetora provider ls                   Alias for 'list'

Examples:
  tetora provider set qwen             Use Qwen with default model
  tetora provider set google gemini-2.5-pro
  tetora provider set claude claude-sonnet-4-20250514
  tetora provider set qwen auto        Use Qwen, auto-select model
  tetora provider status               Check current provider
  tetora provider clear                Return to agent-level config`)
}

// providerSetCmd sets the active provider override.
func providerSetCmd(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Error: provider name is required")
		fmt.Fprintln(os.Stderr, "Usage: tetora provider set <name> [model]")
		os.Exit(1)
	}

	providerName := args[0]
	model := "auto" // default to auto
	if len(args) >= 2 {
		model = args[1]
	}

	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	// Validate provider exists in config.
	if _, exists := cfg.Providers[providerName]; !exists {
		// Check if it's a known preset
		if !isKnownPreset(providerName) {
			fmt.Fprintf(os.Stderr, "Warning: provider '%s' is not configured in config.json\n", providerName)
			fmt.Fprintln(os.Stderr, "Available providers:")
			for name := range cfg.Providers {
				fmt.Fprintf(os.Stderr, "  - %s\n", name)
			}
			fmt.Fprintln(os.Stderr, "\nContinuing anyway (provider will be used if configured at runtime)...")
		}
	}

	// Initialize store if not present.
	storePath := getActiveProviderPath(cfg)
	if err := os.MkdirAll(filepath.Dir(storePath), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating provider state directory: %v\n", err)
		os.Exit(1)
	}

	store := config.NewActiveProviderStore(storePath)
	if err := store.Set(providerName, model, "CLI"); err != nil {
		fmt.Fprintf(os.Stderr, "Error setting active provider: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✓ Active provider set to: %s", providerName)
	if model != "auto" {
		fmt.Printf(" (model: %s)", model)
	}
	fmt.Println()
	fmt.Println("  This provider will be used for ALL agents until cleared.")
	fmt.Println("  Use 'tetora provider clear' to return to agent-level configuration.")
}

// providerStatusCmd shows the current active provider.
func providerStatusCmd() {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	storePath := getActiveProviderPath(cfg)
	store := config.NewActiveProviderStore(storePath)

	if err := store.Load(); err != nil {
		fmt.Fprintf(os.Stderr, "Error loading provider state: %v\n", err)
		os.Exit(1)
	}

	state := store.Get()
	if state.ProviderName == "" {
		fmt.Println("No active provider override.")
		fmt.Println("Using agent-level and global default provider configuration.")
		if cfg.DefaultProvider != "" {
			fmt.Printf("Global default provider: %s\n", cfg.DefaultProvider)
		}
		return
	}

	fmt.Println("Active Provider Override:")
	fmt.Printf("  Provider: %s\n", state.ProviderName)
	if state.Model != "" {
		fmt.Printf("  Model:    %s\n", state.Model)
	} else {
		fmt.Println("  Model:    auto (use provider default)")
	}
	fmt.Printf("  Set at:   %s\n", state.SetAt.Format("2006-01-02 15:04:05"))
	if state.SetBy != "" {
		fmt.Printf("  Set by:   %s\n", state.SetBy)
	}
	fmt.Println()
	fmt.Println("This override affects ALL agent executions.")
	fmt.Println("Use 'tetora provider clear' to remove this override.")
}

// providerClearCmd removes the active provider override.
func providerClearCmd() {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	storePath := getActiveProviderPath(cfg)
	store := config.NewActiveProviderStore(storePath)

	if err := store.Clear(); err != nil {
		fmt.Fprintf(os.Stderr, "Error clearing provider state: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("✓ Active provider override cleared.")
	fmt.Println("Returning to agent-level and global default configuration.")
}

// providerListCmd lists all configured providers.
func providerListCmd() {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Configured Providers:")
	fmt.Println()

	// Check if active override is set.
	storePath := getActiveProviderPath(cfg)
	store := config.NewActiveProviderStore(storePath)
	_ = store.Load()
	activeState := store.Get()

	for name, pCfg := range cfg.Providers {
		marker := " "
		if activeState.ProviderName == name {
			marker = "●"
		}

		model := pCfg.Model
		if model == "" {
			model = "(use default)"
		}

		fmt.Printf("  %s %-20s type: %-20s model: %s\n", marker, name, pCfg.Type, model)
	}

	fmt.Println()
	if cfg.DefaultProvider != "" {
		fmt.Printf("Global default provider: %s\n", cfg.DefaultProvider)
	}
	if activeState.ProviderName != "" {
		fmt.Printf("Active override:     %s", activeState.ProviderName)
		if activeState.Model != "" && activeState.Model != "auto" {
			fmt.Printf(" (model: %s)", activeState.Model)
		}
		fmt.Println()
	}
}

// getActiveProviderPath returns the path to the active provider state file.
func getActiveProviderPath(cfg *config.Config) string {
	if cfg.RuntimeDir != "" {
		return filepath.Join(cfg.RuntimeDir, "active-provider.json")
	}

	// Fallback to config directory.
	configPath := getConfigPath()
	configDir := filepath.Dir(configPath)
	return filepath.Join(configDir, "active-provider.json")
}

// isKnownPreset checks if the provider name is a known preset.
func isKnownPreset(name string) bool {
	knownPresets := []string{
		"qwen", "qwen-cli",
		"claude", "claude-code", "claude-cli", "anthropic",
		"google", "gemini",
		"openai", "codex", "codex-cli",
		"groq", "ollama", "lm-studio",
	}
	for _, preset := range knownPresets {
		if strings.EqualFold(name, preset) {
			return true
		}
	}
	return false
}

// loadConfig loads the full Tetora configuration.
func loadConfig() (*config.Config, error) {
	configPath := getConfigPath()

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	cfg.BaseDir = filepath.Dir(configPath)
	return &cfg, nil
}

// getConfigPath returns the path to the config file.
func getConfigPath() string {
	// Check environment variable first.
	if path := os.Getenv("TETORA_CONFIG"); path != "" {
		return path
	}

	// Default to ~/.tetora/config.json
	home, err := os.UserHomeDir()
	if err != nil {
		// Fallback to current directory.
		return "config.json"
	}

	configPath := filepath.Join(home, ".tetora", "config.json")
	if _, err := os.Stat(configPath); err == nil {
		return configPath
	}

	// Fallback to current directory.
	return "config.json"
}

// ActiveProviderStateInfo is a JSON-serializable version of ActiveProviderState.
type ActiveProviderStateInfo struct {
	ProviderName string    `json:"providerName"`
	Model        string    `json:"model,omitempty"`
	SetAt        time.Time `json:"setAt"`
	SetBy        string    `json:"setBy,omitempty"`
}
