package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"tetora/internal/config"
)

// chatTurn represents a single turn in the onboarding conversation.
type chatTurn struct {
	Role    string // "user" or "assistant"
	Content string
}

// soulBlockRe extracts a SOUL.md code block from kirara's response.
// Matches ```markdown or ``` blocks whose first content line starts with "# ".
var soulBlockRe = regexp.MustCompile("(?s)```(?:markdown)?\\s*\\n(# .+?)\\n```")

// RunKiraraOnboarding guides first-time users through creating their first agent
// via a multi-turn conversation with kirara (the onboarding agent).
func RunKiraraOnboarding(scanner *bufio.Scanner, configPath, configDir, claudePath, lang string) {
	// Locate kirara's SOUL.md.
	agentsDir := filepath.Join(configDir, "workspace", "agents")
	soulPath := filepath.Join(agentsDir, "kirara", "SOUL.md")
	soulBytes, err := os.ReadFile(soulPath)
	if err != nil {
		// kirara not available — skip silently.
		return
	}
	soul := string(soulBytes)

	// Ask user if they want the guided onboarding.
	fmt.Println("─────────────────────────────────────────")
	fmt.Println()
	switch lang {
	case "zh-TW":
		fmt.Println("  キララ（雲母）可以引導你建立第一個 AI agent。")
		fmt.Print("  要讓雲母幫你嗎？ [Y/n] ")
	case "ja":
		fmt.Println("  キララが最初の AI agent の作成をガイドします。")
		fmt.Print("  キララに手伝ってもらいますか？ [Y/n] ")
	default:
		fmt.Println("  Kirara can guide you through creating your first AI agent.")
		fmt.Print("  Would you like her help? [Y/n] ")
	}
	scanner.Scan()
	ans := strings.ToLower(strings.TrimSpace(scanner.Text()))
	if ans == "n" || ans == "no" {
		return
	}

	fmt.Println()
	fmt.Println("─────────────────────────────────────────")
	fmt.Println()

	// Multi-turn conversation loop.
	var history []chatTurn

	// Send initial context as the first "user" message so kirara knows the situation.
	initMsg := buildOnboardingInit(lang)

	for turn := 0; turn < 20; turn++ {
		var userMsg string
		if turn == 0 {
			userMsg = initMsg
		} else {
			fmt.Print("\033[36m你: \033[0m")
			scanner.Scan()
			userMsg = strings.TrimSpace(scanner.Text())
			if userMsg == "" {
				continue
			}
			if userMsg == "/quit" || userMsg == "/exit" {
				fmt.Println()
				fmt.Println("  Onboarding ended.")
				return
			}
		}

		response, err := kiraraCall(claudePath, soul, history, userMsg)
		if err != nil {
			fmt.Printf("  Error calling claude: %v\n", err)
			return
		}

		history = append(history,
			chatTurn{Role: "user", Content: userMsg},
			chatTurn{Role: "assistant", Content: response},
		)

		// Display kirara's response.
		fmt.Println()
		fmt.Println(response)
		fmt.Println()

		// Check if kirara produced a SOUL.md in the response.
		soulContent := extractSoulMD(response)
		if soulContent != "" {
			if writeOnboardingAgent(scanner, configPath, configDir, soulContent, history, claudePath, soul) {
				return
			}
			// User declined writing — continue conversation.
		}
	}

	fmt.Println("  (conversation limit reached)")
}

// buildOnboardingInit creates the first user message for kirara.
func buildOnboardingInit(lang string) string {
	switch lang {
	case "zh-TW":
		return "我剛完成 tetora init，這是我第一次使用。請引導我建立第一個 agent。"
	case "ja":
		return "tetora init が完了しました。初めて使います。最初の agent の作成を手伝ってください。"
	default:
		return "I just completed tetora init for the first time. Please guide me through creating my first agent."
	}
}

// kiraraCall invokes `claude --print` with kirara's SOUL.md as system prompt
// and the assembled conversation history + new user message as the prompt.
func kiraraCall(claudePath, soul string, history []chatTurn, userMsg string) (string, error) {
	// Build conversation text for the prompt.
	var sb strings.Builder
	if len(history) > 0 {
		sb.WriteString("[Previous conversation]\n")
		for _, t := range history {
			role := "User"
			if t.Role == "assistant" {
				role = "Kirara"
			}
			sb.WriteString(role)
			sb.WriteString(": ")
			sb.WriteString(t.Content)
			sb.WriteString("\n\n")
		}
		sb.WriteString("[New message]\n")
	}
	sb.WriteString("User: ")
	sb.WriteString(userMsg)

	prompt := sb.String()

	cmd := exec.Command(claudePath,
		"--print",
		"--system-prompt", soul,
		"--model", "sonnet",
		"--permission-mode", "plan",
		"--no-session-persistence",
	)
	cmd.Stdin = strings.NewReader(prompt)

	// Filter Claude Code session env vars (same as provider_claude.go).
	rawEnv := os.Environ()
	filteredEnv := make([]string, 0, len(rawEnv))
	for _, e := range rawEnv {
		if !strings.HasPrefix(e, "CLAUDECODE=") &&
			!strings.HasPrefix(e, "CLAUDE_CODE_ENTRYPOINT=") &&
			!strings.HasPrefix(e, "CLAUDE_CODE_TEAM_MODE=") {
			filteredEnv = append(filteredEnv, e)
		}
	}
	cmd.Env = filteredEnv

	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("claude exited %d: %s", exitErr.ExitCode(), string(exitErr.Stderr))
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// extractSoulMD looks for a SOUL.md code block in kirara's response.
// Returns the content if found, empty string otherwise.
func extractSoulMD(response string) string {
	matches := soulBlockRe.FindStringSubmatch(response)
	if len(matches) < 2 {
		return ""
	}
	return strings.TrimSpace(matches[1])
}

// writeOnboardingAgent handles the SOUL.md write flow after kirara produces one.
// Returns true if the agent was written (or user explicitly ended), false to continue.
func writeOnboardingAgent(scanner *bufio.Scanner, configPath, configDir, soulContent string, history []chatTurn, claudePath, soul string) bool {
	fmt.Println("─────────────────────────────────────────")
	fmt.Println()
	fmt.Println("  SOUL.md detected in kirara's response.")
	fmt.Print("  Save this agent? [Y/n] ")
	scanner.Scan()
	ans := strings.ToLower(strings.TrimSpace(scanner.Text()))
	if ans == "n" || ans == "no" {
		fmt.Println("  Skipped. You can continue the conversation or type /quit to exit.")
		return false
	}

	// Ask for the agent's role key.
	fmt.Print("  Agent name (used as config key, e.g. 'assistant'): ")
	scanner.Scan()
	roleKey := strings.TrimSpace(scanner.Text())
	if roleKey == "" {
		roleKey = "default"
	}

	// Ask for model.
	fmt.Print("  Model [sonnet]: ")
	scanner.Scan()
	model := strings.TrimSpace(scanner.Text())
	if model == "" {
		model = "sonnet"
	}

	// Ask for description.
	fmt.Print("  Description: ")
	scanner.Scan()
	desc := strings.TrimSpace(scanner.Text())

	// Ask for permission mode.
	fmt.Print("  Permission mode (plan|acceptEdits|auto|bypassPermissions) [acceptEdits]: ")
	scanner.Scan()
	perm := strings.TrimSpace(scanner.Text())
	if perm == "" {
		perm = "acceptEdits"
	}

	// Write files.
	agentsDir := filepath.Join(configDir, "workspace", "agents")
	agentDir := filepath.Join(agentsDir, roleKey)
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		fmt.Printf("  Error creating agent dir: %v\n", err)
		return true
	}

	soulDst := filepath.Join(agentDir, "SOUL.md")
	if err := os.WriteFile(soulDst, []byte(soulContent+"\n"), 0o644); err != nil {
		fmt.Printf("  Error writing SOUL.md: %v\n", err)
		return true
	}
	fmt.Printf("  Written: %s\n", soulDst)

	// Initialize lessons.md.
	lessonsPath := filepath.Join(agentDir, "lessons.md")
	if _, err := os.Stat(lessonsPath); os.IsNotExist(err) {
		os.WriteFile(lessonsPath, []byte(""), 0o644)
	}

	// Also write to configDir/agents/{roleKey}/ for dispatch compatibility.
	dispatchDir := filepath.Join(configDir, "agents", roleKey)
	os.MkdirAll(dispatchDir, 0o755)
	dispatchSoul := filepath.Join(dispatchDir, "SOUL.md")
	if _, err := os.Stat(dispatchSoul); os.IsNotExist(err) {
		os.WriteFile(dispatchSoul, []byte(soulContent+"\n"), 0o644)
	}

	// Detect provider from main config.
	provider := "claude-code"
	if rawCfgData, err := os.ReadFile(configPath); err == nil {
		var rawCfg map[string]any
		if json.Unmarshal(rawCfgData, &rawCfg) == nil {
			if _, hasPath := rawCfg["claudePath"]; !hasPath {
				provider = "claude-api"
			}
		}
	}

	// Update config.json.
	rc := config.AgentConfig{
		SoulFile:       "SOUL.md",
		Model:          model,
		Description:    desc,
		PermissionMode: perm,
		Provider:       provider,
		ToolProfile:    "standard",
	}
	rcJSON, err := json.Marshal(&rc)
	if err != nil {
		fmt.Printf("  Error updating config: %v\n", err)
		return true
	}
	if err := UpdateConfigAgents(configPath, roleKey, rcJSON); err != nil {
		fmt.Printf("  Error updating config: %v\n", err)
		return true
	}

	fmt.Printf("  Agent %q registered in config.\n", roleKey)
	fmt.Println()
	fmt.Println("  You can now use:")
	fmt.Printf("    tetora dispatch --role %s \"your task\"\n", roleKey)
	fmt.Printf("    tetora agent show %s\n", roleKey)
	fmt.Println()
	fmt.Println("  Welcome to Tetora!")
	return true
}
