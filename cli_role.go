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

func cmdRole(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: tetora role <list|add|show|remove> [name]")
		return
	}
	switch args[0] {
	case "list", "ls":
		roleList()
	case "add":
		roleAdd()
	case "set":
		if len(args) < 4 {
			fmt.Println("Usage: tetora role set <name> <field> <value>")
			fmt.Println("Fields: model, permission, description")
			return
		}
		roleSet(args[1], args[2], args[3])
	case "show":
		if len(args) < 2 {
			fmt.Println("Usage: tetora role show <name>")
			return
		}
		roleShow(args[1])
	case "remove", "rm":
		if len(args) < 2 {
			fmt.Println("Usage: tetora role remove <name>")
			return
		}
		roleRemove(args[1])
	default:
		fmt.Fprintf(os.Stderr, "Unknown action: %s\n", args[0])
	}
}

func roleList() {
	cfg := loadConfig(findConfigPath())
	if len(cfg.Roles) == 0 {
		fmt.Println("No roles configured.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintf(w, "NAME\tMODEL\tPERMISSION\tSOUL FILE\tDESCRIPTION\n")
	for name, rc := range cfg.Roles {
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
	fmt.Printf("\n%d roles\n", len(cfg.Roles))
}

func roleAdd() {
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

	fmt.Println("=== Add Role ===")
	fmt.Println()

	name := prompt("Role name", "")
	if name == "" {
		fmt.Println("Name is required.")
		return
	}

	configPath := findConfigPath()
	cfg := loadConfig(configPath)
	if _, exists := cfg.Roles[name]; exists {
		fmt.Printf("Role %q already exists.\n", name)
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

	var archetype *RoleArchetype
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
		// Auto-generate soul file from archetype template.
		soulFile = fmt.Sprintf("SOUL-%s.md", name)
		content := generateSoulContent(archetype, name)
		soulPath := soulFile
		if !filepath.IsAbs(soulPath) && cfg.DefaultWorkdir != "" {
			soulPath = filepath.Join(cfg.DefaultWorkdir, soulPath)
		}
		if _, err := os.Stat(soulPath); os.IsNotExist(err) {
			if err := writeSoulFile(cfg, soulFile, content); err != nil {
				fmt.Printf("Warning: could not write soul file: %v\n", err)
			} else {
				fmt.Printf("  Created soul file: %s\n", soulPath)
			}
		} else {
			fmt.Printf("  Soul file already exists: %s\n", soulPath)
		}
	} else {
		soulFile = prompt("Soul file path (relative to workdir)", "")
	}

	rc := RoleConfig{
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

	if err := updateConfigRoles(configPath, name, &rc); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("\nRole %q added.\n", name)
}

func roleShow(name string) {
	cfg := loadConfig(findConfigPath())
	rc, ok := cfg.Roles[name]
	if !ok {
		fmt.Printf("Role %q not found.\n", name)
		os.Exit(1)
	}

	model := rc.Model
	if model == "" {
		model = "default"
	}

	fmt.Printf("Role: %s\n", name)
	fmt.Printf("  Model:       %s\n", model)
	fmt.Printf("  Soul File:   %s\n", rc.SoulFile)
	if rc.Description != "" {
		fmt.Printf("  Description: %s\n", rc.Description)
	}
	if rc.PermissionMode != "" {
		fmt.Printf("  Permission:  %s\n", rc.PermissionMode)
	}

	// Show soul file preview.
	if rc.SoulFile != "" {
		content, err := loadRolePrompt(cfg, name)
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

func roleSet(name, field, value string) {
	configPath := findConfigPath()
	cfg := loadConfig(configPath)
	rc, ok := cfg.Roles[name]
	if !ok {
		fmt.Printf("Role %q not found.\n", name)
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

	if err := updateConfigRoles(configPath, name, &rc); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Role %q: %s → %s\n", name, field, value)
}

// updateRoleModel updates a role's model in config. Returns the old model.
// Used by chat commands (!model, /model).
func updateRoleModel(cfg *Config, roleName, model string) (string, error) {
	rc, ok := cfg.Roles[roleName]
	if !ok {
		return "", fmt.Errorf("role %q not found", roleName)
	}
	old := rc.Model
	rc.Model = model

	configPath := findConfigPath()
	if err := updateConfigRoles(configPath, roleName, &rc); err != nil {
		return old, err
	}

	// Update in-memory config too.
	cfg.Roles[roleName] = rc
	return old, nil
}

func roleRemove(name string) {
	configPath := findConfigPath()
	cfg := loadConfig(configPath)

	if _, ok := cfg.Roles[name]; !ok {
		fmt.Printf("Role %q not found.\n", name)
		os.Exit(1)
	}

	// Check if any job uses this role.
	jf := loadJobsFile()
	var using []string
	for _, j := range jf.Jobs {
		if j.Role == name {
			using = append(using, j.ID)
		}
	}
	if len(using) > 0 {
		fmt.Printf("Role %q is used by jobs: %s\n", name, strings.Join(using, ", "))
		fmt.Println("Remove these job assignments first, or re-assign them.")
		os.Exit(1)
	}

	if err := updateConfigRoles(configPath, name, nil); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Role %q removed.\n", name)
}

// updateConfigRoles updates a single role in config.json.
// If rc is nil, the role is removed. Otherwise it is added/updated.
// This preserves all other config fields by reading/modifying/writing the raw JSON.
func updateConfigRoles(configPath, roleName string, rc *RoleConfig) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	// Parse existing roles.
	roles := make(map[string]RoleConfig)
	if rolesRaw, ok := raw["roles"]; ok {
		json.Unmarshal(rolesRaw, &roles)
	}

	if rc == nil {
		delete(roles, roleName)
	} else {
		roles[roleName] = *rc
	}

	rolesJSON, err := json.Marshal(roles)
	if err != nil {
		return err
	}
	raw["roles"] = rolesJSON

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(configPath, append(out, '\n'), 0o644); err != nil {
		return err
	}
	// Auto-snapshot config version after role change.
	// Use a heuristic to find historyDB: load config briefly.
	if cfg := tryLoadConfigForVersioning(configPath); cfg != nil {
		snapshotConfig(cfg.HistoryDB, configPath, "cli", fmt.Sprintf("role %s", roleName))
	}
	return nil
}
