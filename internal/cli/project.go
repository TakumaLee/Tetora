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
)

// CmdProject handles the `tetora project` subcommand.
func CmdProject(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: tetora project <add>")
		return
	}

	switch args[0] {
	case "add":
		runProjectAdd(args[1:])
	default:
		fmt.Printf("Unknown project command: %s\n", args[0])
	}
}

func runProjectAdd(args []string) {
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

	// 1. Interactive path input with default
	defaultPath, _ := filepath.Abs(".")
	if len(args) > 0 {
		defaultPath, _ = filepath.Abs(args[0])
	}

	inputPath := prompt("Project path", defaultPath)
	absPath, err := filepath.Abs(inputPath)
	if err != nil {
		fmt.Printf("Error resolving path: %v\n", err)
		return
	}

	info, err := os.Stat(absPath)
	if err != nil {
		fmt.Printf("Error: path %s does not exist\n", absPath)
		return
	}
	if !info.IsDir() {
		fmt.Printf("Error: path %s is not a directory\n", absPath)
		return
	}

	fmt.Printf("\n--- Project Onboarding: %s ---\n\n", absPath)

	// 2. Auto-Detection
	roadmaps := findRoadmapFiles(absPath)

	selectedPhase := "all"
	description := ""

	if len(roadmaps) > 0 {
		fmt.Printf("  Found roadmaps: %s\n", strings.Join(roadmaps, ", "))
		phases := []string{"all"}
		for _, r := range roadmaps {
			phases = append(phases, detectPhases(filepath.Join(absPath, r))...)
		}
		phases = uniqueStrings(phases)

		if len(phases) > 1 {
			fmt.Println("  Select the phase you want to onboard:")
			idx := interactiveChoose(phases, 0)
			if idx >= 0 {
				selectedPhase = phases[idx]
			}
		}
		// roadmaps found but no phases detected → use "all", no description prompt
	} else {
		// No roadmap files → ask for description
		description = prompt("Project description (briefly describe the goal)", "")
	}

	// 3. Confirmation
	fmt.Printf("\n  Ready to onboard project:\n")
	fmt.Printf("    Path:  %s\n", absPath)
	fmt.Printf("    Phase: %s\n", selectedPhase)
	if description != "" {
		fmt.Printf("    Desc:  %s\n", description)
	}
	fmt.Println()

	confirm := prompt("Continue? (Y/n)", "Y")
	if strings.ToLower(confirm) != "y" {
		fmt.Println("Aborted.")
		return
	}

	// 4. Check TaskBoard status
	configPath := FindConfigPath()
	if configData, err := os.ReadFile(configPath); err == nil {
		if !isTaskBoardEnabled(configData) {
			fmt.Println("\n  \033[33mWarning: Task Board might not be enabled. You can enable it with 'tetora init'.\033[0m")
		}
	}

	// 5. Execution
	fmt.Println("\n  Executing project-kickoff workflow...")
	cmdArgs := []string{"workflow", "run", "project-kickoff",
		"--var", "path=" + absPath,
		"--var", "phase=" + selectedPhase,
	}
	if description != "" {
		cmdArgs = append(cmdArgs, "--var", "description="+description)
	}

	cmd := exec.Command(os.Args[0], cmdArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("\n  Error executing workflow: %v\n", err)
		return
	}

	fmt.Println("\n  \033[32m✓ 專案接入完成，autoDispatch 正在執行\033[0m")
}

// isTaskBoardEnabled parses the config JSON and checks if taskBoard.enabled is true.
func isTaskBoardEnabled(configData []byte) bool {
	var cfg map[string]any
	if err := json.Unmarshal(configData, &cfg); err != nil {
		return false
	}
	tb, ok := cfg["taskBoard"]
	if !ok {
		return false
	}
	tbMap, ok := tb.(map[string]any)
	if !ok {
		return false
	}
	enabled, ok := tbMap["enabled"]
	if !ok {
		return false
	}
	b, ok := enabled.(bool)
	return ok && b
}

func findRoadmapFiles(dir string) []string {
	var found []string
	patterns := []string{"README.md", "ROADMAP.md", "tasks/roadmap*.md", "docs/roadmap*.md", "CLAUDE.md"}
	for _, p := range patterns {
		matches, _ := filepath.Glob(filepath.Join(dir, p))
		for _, m := range matches {
			rel, err := filepath.Rel(dir, m)
			if err == nil {
				found = append(found, rel)
			}
		}
	}
	return found
}

func detectPhases(filePath string) []string {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}

	// Only match headers with an explicit Phase/PHASE keyword or version prefix (v1.0).
	// This avoids matching generic headers like ## Overview, ## Installation, ## API.
	re := regexp.MustCompile(`(?m)^#+\s+(?:(?:Phase|PHASE)\s+([A-Za-z0-9.]+)|v([0-9][A-Za-z0-9.]*))`)
	matches := re.FindAllStringSubmatch(string(content), -1)

	var phases []string
	for _, m := range matches {
		// m[1] captures "Phase X", m[2] captures "vX.Y"
		p := m[1]
		if p == "" {
			p = "v" + m[2]
		}
		if p != "" {
			phases = append(phases, p)
		}
	}
	return phases
}

func uniqueStrings(input []string) []string {
	keys := make(map[string]bool)
	var list []string
	for _, entry := range input {
		if _, value := keys[entry]; !value {
			keys[entry] = true
			list = append(list, entry)
		}
	}
	return list
}
