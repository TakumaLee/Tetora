package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// CmdTeam handles the "tetora team" CLI command.
func CmdTeam(args []string) {
	if len(args) == 0 {
		printTeamUsage()
		return
	}

	switch args[0] {
	case "list":
		cmdTeamList(args[1:])
	case "show":
		cmdTeamShow(args[1:])
	case "generate":
		cmdTeamGenerate(args[1:])
	case "apply":
		cmdTeamApply(args[1:])
	case "delete":
		cmdTeamDelete(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown team subcommand: %s\n", args[0])
		printTeamUsage()
		os.Exit(1)
	}
}

func printTeamUsage() {
	fmt.Fprintln(os.Stderr, `Usage: tetora team <command>

Commands:
  list                   List all teams
  show <name>            Show team details
  generate <description> Generate a new team via AI
  apply <name>           Apply team to config
  delete <name>          Delete a user-created team

Flags (generate):
  --size N               Number of agents (default: AI decides)
  --template NAME        Base template name
  --preview              Show generated team without saving
  --json                 Output as JSON`)
}

func cmdTeamList(args []string) {
	cfg := LoadCLIConfig(FindConfigPath())
	api := cfg.NewAPIClient()

	resp, err := api.Get("/api/teams")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "Error: %s\n", body)
		os.Exit(1)
	}

	// Check --json flag.
	for _, a := range args {
		if a == "--json" {
			fmt.Println(string(body))
			return
		}
	}

	var teams []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Builtin     bool   `json:"builtin"`
		AgentCount  int    `json:"agentCount"`
	}
	json.Unmarshal(body, &teams)

	if len(teams) == 0 {
		fmt.Println("No teams found. Use 'tetora team generate' to create one.")
		return
	}

	fmt.Printf("%-20s %-8s %-6s %s\n", "NAME", "BUILTIN", "AGENTS", "DESCRIPTION")
	for _, t := range teams {
		builtin := "no"
		if t.Builtin {
			builtin = "yes"
		}
		fmt.Printf("%-20s %-8s %-6d %s\n", t.Name, builtin, t.AgentCount, t.Description)
	}
}

func cmdTeamShow(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: tetora team show <name>")
		os.Exit(1)
	}
	name := args[0]

	cfg := LoadCLIConfig(FindConfigPath())
	api := cfg.NewAPIClient()

	resp, err := api.Get("/api/teams/" + name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "Error: %s\n", body)
		os.Exit(1)
	}

	// Check --json flag.
	for _, a := range args[1:] {
		if a == "--json" {
			fmt.Println(string(body))
			return
		}
	}

	var team struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Builtin     bool   `json:"builtin"`
		Agents      []struct {
			Key         string   `json:"key"`
			DisplayName string   `json:"displayName"`
			Description string   `json:"description"`
			Model       string   `json:"model"`
			Keywords    []string `json:"keywords"`
		} `json:"agents"`
	}
	json.Unmarshal(body, &team)

	builtin := ""
	if team.Builtin {
		builtin = " (builtin)"
	}
	fmt.Printf("Team: %s%s\n", team.Name, builtin)
	fmt.Printf("Description: %s\n", team.Description)
	fmt.Printf("Agents: %d\n\n", len(team.Agents))

	for _, a := range team.Agents {
		fmt.Printf("  [%s] %s (%s)\n", a.Key, a.DisplayName, a.Model)
		fmt.Printf("    %s\n", a.Description)
		if len(a.Keywords) > 0 {
			kw := a.Keywords
			if len(kw) > 8 {
				kw = kw[:8]
			}
			fmt.Printf("    Keywords: %s ...\n", JoinStrings(kw, ", "))
		}
		fmt.Println()
	}
}

func cmdTeamGenerate(args []string) {
	var size int
	var template string
	var preview bool
	var jsonOut bool
	var descParts []string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--size":
			if i+1 < len(args) {
				i++
				fmt.Sscanf(args[i], "%d", &size)
			}
		case "--template":
			if i+1 < len(args) {
				i++
				template = args[i]
			}
		case "--preview":
			preview = true
		case "--json":
			jsonOut = true
		default:
			descParts = append(descParts, args[i])
		}
	}

	description := strings.Join(descParts, " ")
	if description == "" {
		// Try stdin.
		stat, _ := os.Stdin.Stat()
		if (stat.Mode() & os.ModeCharDevice) == 0 {
			data, _ := io.ReadAll(os.Stdin)
			description = strings.TrimSpace(string(data))
		}
	}
	if description == "" {
		fmt.Fprintln(os.Stderr, "Error: description required")
		fmt.Fprintln(os.Stderr, "Usage: tetora team generate <description> [--size N] [--template NAME] [--preview]")
		os.Exit(1)
	}

	cfg := LoadCLIConfig(FindConfigPath())
	api := cfg.NewAPIClient()
	api.Client.Timeout = 180e9 // 3 minutes for generation

	payload := map[string]any{
		"description": description,
	}
	if size > 0 {
		payload["size"] = size
	}
	if template != "" {
		payload["template"] = template
	}

	fmt.Fprintln(os.Stderr, "Generating team... (this may take a minute)")

	resp, err := api.PostJSON("/api/teams/generate", payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "Error: %s\n", body)
		os.Exit(1)
	}

	if jsonOut || preview {
		// Pretty print.
		var pretty json.RawMessage
		json.Unmarshal(body, &pretty)
		out, _ := json.MarshalIndent(pretty, "", "  ")
		fmt.Println(string(out))
		if preview {
			fmt.Fprintln(os.Stderr, "\n(preview mode — not saved)")
		}
		return
	}

	// Save the generated team.
	resp2, err := api.Do("POST", "/api/teams", strings.NewReader(string(body)))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error saving team: %v\n", err)
		os.Exit(1)
	}
	defer resp2.Body.Close()

	body2, _ := io.ReadAll(resp2.Body)
	if resp2.StatusCode != 200 && resp2.StatusCode != 201 {
		fmt.Fprintf(os.Stderr, "Error saving: %s\n", body2)
		os.Exit(1)
	}

	var saved struct {
		Name       string `json:"name"`
		AgentCount int    `json:"agentCount"`
	}
	json.Unmarshal(body2, &saved)
	fmt.Printf("Team %q created with %d agents.\n", saved.Name, saved.AgentCount)
	fmt.Printf("Run 'tetora team apply %s' to activate.\n", saved.Name)
}

func cmdTeamApply(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: tetora team apply <name> [--force]")
		os.Exit(1)
	}
	name := args[0]

	force := false
	for _, a := range args[1:] {
		if a == "--force" {
			force = true
		}
	}

	cfg := LoadCLIConfig(FindConfigPath())
	api := cfg.NewAPIClient()

	payload := map[string]any{"force": force}
	resp, err := api.PostJSON("/api/teams/"+name+"/apply", payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "Error: %s\n", body)
		os.Exit(1)
	}

	fmt.Printf("Team %q applied. Config reloaded.\n", name)
}

func cmdTeamDelete(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: tetora team delete <name>")
		os.Exit(1)
	}
	name := args[0]

	cfg := LoadCLIConfig(FindConfigPath())
	api := cfg.NewAPIClient()

	resp, err := api.Do("DELETE", "/api/teams/"+name, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "Error: %s\n", body)
		os.Exit(1)
	}

	fmt.Printf("Team %q deleted.\n", name)
}
