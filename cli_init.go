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

	fmt.Println("=== Tetora Setup ===")
	fmt.Println()

	// 1. Claude CLI
	detected := detectClaude()
	claudePath := prompt("Claude CLI path", detected)

	// 2. Max concurrent
	maxStr := prompt("Max concurrent tasks", "3")
	maxConcurrent, _ := strconv.Atoi(maxStr)
	if maxConcurrent <= 0 {
		maxConcurrent = 3
	}

	// 3. Model
	model := prompt("Default model", "sonnet")

	// 4. Listen address
	listenAddr := prompt("HTTP listen address", "127.0.0.1:8991")

	// 5. Workdir
	defaultWorkdir := prompt("Default workdir", filepath.Join(configDir, "workspace"))

	// 6. Budget
	budgetStr := prompt("Default budget (USD)", "2.00")
	budget, _ := strconv.ParseFloat(budgetStr, 64)
	if budget <= 0 {
		budget = 2.0
	}

	// 7. Telegram
	fmt.Println()
	fmt.Print("  Enable Telegram? [y/N]: ")
	scanner.Scan()
	tgEnabled := strings.ToLower(strings.TrimSpace(scanner.Text())) == "y"
	var botToken string
	var chatID int64
	if tgEnabled {
		botToken = prompt("Bot token", "")
		cidStr := prompt("Chat ID", "")
		chatID, _ = strconv.ParseInt(cidStr, 10, 64)
	}

	// 8. API Token
	fmt.Println()
	var apiToken string
	tokenBytes := make([]byte, 32)
	rand.Read(tokenBytes)
	apiToken = hex.EncodeToString(tokenBytes)
	fmt.Printf("  API token: %s\n", apiToken)
	fmt.Println("  (Save this token — needed for CLI/API access)")

	// Build config.
	cfg := map[string]any{
		"claudePath":            claudePath,
		"maxConcurrent":         maxConcurrent,
		"defaultModel":          model,
		"defaultTimeout":        "15m",
		"defaultBudget":         budget,
		"defaultPermissionMode": "acceptEdits",
		"defaultWorkdir":        defaultWorkdir,
		"listenAddr":            listenAddr,
		"jobsFile":              "jobs.json",
		"apiToken":              apiToken,
		"log":                   true,
		"telegram": map[string]any{
			"enabled":     tgEnabled,
			"botToken":    botToken,
			"chatID":      chatID,
			"pollTimeout": 30,
		},
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

	// --- Step A: Create first role ---
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

		defaultModel := model
		defaultPerm := "acceptEdits"
		if archetype != nil {
			defaultModel = archetype.Model
			defaultPerm = archetype.PermissionMode
		}

		roleModel := prompt("Role model", defaultModel)
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

	// --- Step B: Create sample job ---
	fmt.Println()
	fmt.Print("  Create a sample cron job? [Y/n]: ")
	scanner.Scan()
	if strings.ToLower(strings.TrimSpace(scanner.Text())) != "n" {
		fmt.Println()
		jobID := prompt("Job ID", "heartbeat")
		jobName := prompt("Job name", "Heartbeat")
		jobSchedule := prompt("Cron schedule (m h dom mon dow)", "0 */2 * * *")
		jobPrompt := prompt("Prompt", "Quick health check. Respond HEARTBEAT_OK.")
		jobModel := prompt("Model", "haiku")

		sampleJob := map[string]any{
			"id":       jobID,
			"name":     jobName,
			"enabled":  true,
			"schedule": jobSchedule,
			"tz":       "Asia/Taipei",
			"task": map[string]any{
				"prompt":  jobPrompt,
				"model":   jobModel,
				"timeout": "1m",
				"budget":  0.1,
			},
			"notify": false,
		}

		jfData := map[string]any{"jobs": []any{sampleJob}}

		// Merge with existing jobs.json if it has content.
		if raw, err := os.ReadFile(jobsPath); err == nil {
			var existing map[string]any
			if json.Unmarshal(raw, &existing) == nil {
				if existingJobs, ok := existing["jobs"].([]any); ok && len(existingJobs) > 0 {
					jfData["jobs"] = append(existingJobs, sampleJob)
				}
			}
		}

		jobData, _ := json.MarshalIndent(jfData, "", "  ")
		os.WriteFile(jobsPath, append(jobData, '\n'), 0o644)
		fmt.Printf("  Job %q added.\n", jobID)
	}

	// --- Step C: Quick test ---
	fmt.Println()
	fmt.Print("  Run a quick test of claude CLI? [y/N]: ")
	scanner.Scan()
	if strings.ToLower(strings.TrimSpace(scanner.Text())) == "y" {
		fmt.Println("  Testing claude CLI...")
		cmd := exec.Command(claudePath, "--print", "--output-format", "json",
			"--model", "haiku", "--max-budget-usd", "0.05",
			"--permission-mode", "plan",
			"-p", "respond with OK")
		out, err := cmd.CombinedOutput()
		if err != nil {
			fmt.Printf("  Test failed: %v\n", err)
			if len(out) > 0 {
				s := string(out)
				if len(s) > 200 {
					s = s[:200] + "..."
				}
				fmt.Printf("  Output: %s\n", s)
			}
		} else {
			var result map[string]any
			if json.Unmarshal(out, &result) == nil {
				r, _ := result["result"].(string)
				c, _ := result["cost_usd"].(float64)
				if len(r) > 100 {
					r = r[:100] + "..."
				}
				fmt.Printf("  Test OK: %s (cost: $%.4f)\n", r, c)
			} else {
				s := string(out)
				if len(s) > 200 {
					s = s[:200] + "..."
				}
				fmt.Printf("  Test completed: %s\n", s)
			}
		}
	}

	// --- Step D: Install service ---
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

func detectClaude() string {
	if path, err := exec.LookPath("claude"); err == nil {
		return path
	}
	home, _ := os.UserHomeDir()
	for _, p := range []string{
		filepath.Join(home, ".local", "bin", "claude"),
		"/usr/local/bin/claude",
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "/usr/local/bin/claude"
}
