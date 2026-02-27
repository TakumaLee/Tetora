package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
)

func cmdAgent(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: tetora agent <list|add|show|remove> [name]")
		return
	}
	switch args[0] {
	case "list", "ls":
		agentList()
	case "add":
		agentAdd()
	case "set":
		if len(args) < 4 {
			fmt.Println("Usage: tetora agent set <name> <field> <value>")
			fmt.Println("Fields: model, permission, description")
			return
		}
		agentSet(args[1], args[2], args[3])
	case "show":
		if len(args) < 2 {
			fmt.Println("Usage: tetora agent show <name>")
			return
		}
		agentShow(args[1])
	case "remove", "rm":
		if len(args) < 2 {
			fmt.Println("Usage: tetora agent remove <name>")
			return
		}
		agentRemove(args[1])
	default:
		fmt.Fprintf(os.Stderr, "Unknown action: %s\n", args[0])
	}
}

func agentList() {
	cfg := loadConfig(findConfigPath())
	if len(cfg.Agents) == 0 {
		fmt.Println("No agents configured.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintf(w, "NAME\tMODEL\tPERMISSION\tSOUL FILE\tDESCRIPTION\n")
	for name, rc := range cfg.Agents {
		model := rc.Model
		if model == "" {
			model = "default"
		}
		perm := rc.PermissionMode
		if perm == "" {
			perm = "-"
		}
		soul := rc.SoulFile
		if soul == "" {
			soul = "-"
		}
		desc := rc.Description
		if desc == "" {
			desc = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", name, model, perm, soul, desc)
	}
	w.Flush()
	fmt.Printf("\n%d agents\n", len(cfg.Agents))
}

func agentAdd() {
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

	fmt.Println("=== Add Agent ===")
	fmt.Println()

	name := prompt("Agent name", "")
	if name == "" {
		fmt.Println("Name is required.")
		return
	}

	configPath := findConfigPath()
	cfg := loadConfig(configPath)
	if _, exists := cfg.Agents[name]; exists {
		fmt.Printf("Agent %q already exists.\n", name)
		return
	}

	// Archetype selection.
	fmt.Println()
	fmt.Println("  Start from a template?")
	for i, a := range builtinArchetypes {
		fmt.Printf("    %d. %-12s %s\n", i+1, a.Name, a.Description)
	}
	fmt.Printf("    %d. %-12s Start from scratch\n", len(builtinArchetypes)+1, "blank")
	archChoice := prompt(fmt.Sprintf("Choose [1-%d]", len(builtinArchetypes)+1), fmt.Sprintf("%d", len(builtinArchetypes)+1))

	var archetype *AgentArchetype
	if n, err := strconv.Atoi(archChoice); err == nil && n >= 1 && n <= len(builtinArchetypes) {
		archetype = &builtinArchetypes[n-1]
	}

	defaultModel := "sonnet"
	defaultPerm := ""
	if archetype != nil {
		defaultModel = archetype.Model
		defaultPerm = archetype.PermissionMode
	}

	model := prompt("Model", defaultModel)
	description := prompt("Description", "")
	permMode := prompt("Permission mode (plan|acceptEdits|autoEdit|bypassPermissions)", defaultPerm)

	var soulFile string
	if archetype != nil {
		// Auto-generate soul file in agents/{name}/ directory.
		soulFile = "SOUL.md"
		content := generateSoulContent(archetype, name)
		agentDir := filepath.Join(cfg.AgentsDir, name)
		soulPath := filepath.Join(agentDir, "SOUL.md")
		if err := os.MkdirAll(agentDir, 0o755); err != nil {
			fmt.Printf("Warning: could not create agent dir: %v\n", err)
		} else if _, err := os.Stat(soulPath); os.IsNotExist(err) {
			if err := os.WriteFile(soulPath, []byte(content), 0o644); err != nil {
				fmt.Printf("Warning: could not write soul file: %v\n", err)
			} else {
				fmt.Printf("  Created soul file: %s\n", soulPath)
			}
		} else {
			fmt.Printf("  Soul file already exists: %s\n", soulPath)
		}
	} else {
		soulFile = prompt("Soul file path (relative to agent dir)", "")
	}

	rc := AgentConfig{
		SoulFile:       soulFile,
		Model:          model,
		Description:    description,
		PermissionMode: permMode,
	}

	// Verify soul file exists if provided and not from archetype.
	if soulFile != "" && archetype == nil {
		path := soulFile
		if !filepath.IsAbs(path) && cfg.DefaultWorkdir != "" {
			path = filepath.Join(cfg.DefaultWorkdir, path)
		}
		if _, err := os.Stat(path); err != nil {
			fmt.Printf("Warning: soul file not found at %s\n", path)
			confirm := prompt("Continue anyway? [y/N]", "n")
			if strings.ToLower(confirm) != "y" {
				return
			}
		}
	}

	if err := updateConfigAgents(configPath, name, &rc); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("\nAgent %q added.\n", name)
}

func agentShow(name string) {
	cfg := loadConfig(findConfigPath())
	rc, ok := cfg.Agents[name]
	if !ok {
		fmt.Printf("Agent %q not found.\n", name)
		os.Exit(1)
	}

	model := rc.Model
	if model == "" {
		model = "default"
	}

	// Show workspace info.
	ws := resolveWorkspace(cfg, name)
	fmt.Printf("Agent: %s\n", name)
	fmt.Printf("  Model:       %s\n", model)
	fmt.Printf("  Soul File:   %s\n", rc.SoulFile)
	fmt.Printf("  Agent Dir:   %s\n", filepath.Join(cfg.AgentsDir, name))
	fmt.Printf("  Workspace:   %s\n", ws.Dir)
	fmt.Printf("  Soul Path:   %s\n", ws.SoulFile)
	if rc.Description != "" {
		fmt.Printf("  Description: %s\n", rc.Description)
	}
	if rc.PermissionMode != "" {
		fmt.Printf("  Permission:  %s\n", rc.PermissionMode)
	}

	// Show soul file preview.
	if rc.SoulFile != "" {
		content, err := loadAgentPrompt(cfg, name)
		if err != nil {
			fmt.Printf("\n  (soul file error: %v)\n", err)
			return
		}
		if content != "" {
			lines := strings.Split(content, "\n")
			maxLines := 30
			if len(lines) > maxLines {
				fmt.Printf("\n--- Soul Preview (first %d/%d lines) ---\n", maxLines, len(lines))
				fmt.Println(strings.Join(lines[:maxLines], "\n"))
				fmt.Println("...")
			} else {
				fmt.Printf("\n--- Soul Content (%d lines) ---\n", len(lines))
				fmt.Println(content)
			}
		}
	}
}

func agentSet(name, field, value string) {
	configPath := findConfigPath()
	cfg := loadConfig(configPath)
	rc, ok := cfg.Agents[name]
	if !ok {
		fmt.Printf("Agent %q not found.\n", name)
		os.Exit(1)
	}

	switch field {
	case "model":
		rc.Model = value
	case "permission", "permissionMode":
		rc.PermissionMode = value
	case "description", "desc":
		rc.Description = value
	default:
		fmt.Printf("Unknown field %q. Use: model, permission, description\n", field)
		os.Exit(1)
	}

	if err := updateConfigAgents(configPath, name, &rc); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Agent %q: %s -> %s\n", name, field, value)
}

// updateAgentModel updates an agent's model in config. Returns the old model.
// Used by chat commands (!model, /model).
func updateAgentModel(cfg *Config, agentName, model string) (string, error) {
	rc, ok := cfg.Agents[agentName]
	if !ok {
		return "", fmt.Errorf("agent %q not found", agentName)
	}
	old := rc.Model
	rc.Model = model

	configPath := findConfigPath()
	if err := updateConfigAgents(configPath, agentName, &rc); err != nil {
		return old, err
	}

	// Update in-memory config too.
	cfg.Agents[agentName] = rc
	return old, nil
}

func agentRemove(name string) {
	configPath := findConfigPath()
	cfg := loadConfig(configPath)

	if _, ok := cfg.Agents[name]; !ok {
		fmt.Printf("Agent %q not found.\n", name)
		os.Exit(1)
	}

	// Check if any job uses this agent.
	jf := loadJobsFile()
	var using []string
	for _, j := range jf.Jobs {
		if j.Agent == name {
			using = append(using, j.ID)
		}
	}
	if len(using) > 0 {
		fmt.Printf("Agent %q is used by jobs: %s\n", name, strings.Join(using, ", "))
		fmt.Println("Remove these job assignments first, or re-assign them.")
		os.Exit(1)
	}

	if err := updateConfigAgents(configPath, name, nil); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Agent %q removed.\n", name)
}

// updateConfigAgents updates a single agent in config.json.
// If rc is nil, the agent is removed. Otherwise it is added/updated.
// This preserves all other config fields by reading/modifying/writing the raw JSON.
func updateConfigAgents(configPath, agentName string, rc *AgentConfig) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	// Parse existing agents (try "agents" first, fall back to "roles").
	agents := make(map[string]AgentConfig)
	if agentsRaw, ok := raw["agents"]; ok {
		json.Unmarshal(agentsRaw, &agents)
	} else if rolesRaw, ok := raw["roles"]; ok {
		json.Unmarshal(rolesRaw, &agents)
		delete(raw, "roles") // migrate to new key
	}

	if rc == nil {
		delete(agents, agentName)
	} else {
		agents[agentName] = *rc
	}

	agentsJSON, err := json.Marshal(agents)
	if err != nil {
		return err
	}
	raw["agents"] = agentsJSON

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(configPath, append(out, '\n'), 0o644); err != nil {
		return err
	}
	// Auto-snapshot config version after agent change.
	// Use a heuristic to find historyDB: load config briefly.
	if cfg := tryLoadConfigForVersioning(configPath); cfg != nil {
		snapshotConfig(cfg.HistoryDB, configPath, "cli", fmt.Sprintf("agent %s", agentName))
	}
	return nil
}

// updateConfigSmartDispatchDefault sets smartDispatch.defaultAgent in the config file.
func updateConfigSmartDispatchDefault(configPath, agentName string) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	sd := make(map[string]any)
	if sdRaw, ok := raw["smartDispatch"]; ok {
		json.Unmarshal(sdRaw, &sd)
	}

	sd["defaultAgent"] = agentName

	sdJSON, err := json.Marshal(sd)
	if err != nil {
		return err
	}
	raw["smartDispatch"] = sdJSON

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, append(out, '\n'), 0o644)
}
