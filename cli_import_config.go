package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// cmdImportConfig imports agents, channels, and settings from another config.json.
//
// Usage: tetora import config <path> [--mode merge|replace] [--dry-run]
func cmdImportConfig(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: tetora import config <path> [--mode merge|replace] [--dry-run]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Modes:")
		fmt.Fprintln(os.Stderr, "  merge   (default) Add new agents, skip existing. Merge channelIDs.")
		fmt.Fprintln(os.Stderr, "  replace           Overwrite agents, channels, smartDispatch settings.")
		os.Exit(1)
	}

	srcPath := args[0]
	mode := "merge"
	dryRun := false

	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--mode":
			if i+1 < len(args) {
				mode = args[i+1]
				i++
			}
		case "--dry-run":
			dryRun = true
		}
	}

	if mode != "merge" && mode != "replace" {
		fmt.Fprintf(os.Stderr, "Error: unknown mode %q (must be merge or replace)\n", mode)
		os.Exit(1)
	}

	// Read source config.
	srcData, err := os.ReadFile(srcPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading source config: %v\n", err)
		os.Exit(1)
	}

	var srcCfg Config
	if err := json.Unmarshal(srcData, &srcCfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing source config: %v\n", err)
		os.Exit(1)
	}

	// Load current config.
	home, _ := os.UserHomeDir()
	configDir := filepath.Join(home, ".tetora")
	configPath := filepath.Join(configDir, "config.json")

	dstData, err := os.ReadFile(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading current config (%s): %v\n", configPath, err)
		os.Exit(1)
	}

	var dstRaw map[string]any
	if err := json.Unmarshal(dstData, &dstRaw); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing current config: %v\n", err)
		os.Exit(1)
	}

	// Parse existing agents from destination.
	dstRoles := make(map[string]AgentConfig)
	if rolesRaw, ok := dstRaw["roles"]; ok {
		b, _ := json.Marshal(rolesRaw)
		json.Unmarshal(b, &dstRoles)
	}

	// Parse source agents.
	var srcRaw map[string]any
	json.Unmarshal(srcData, &srcRaw)

	srcRoles := make(map[string]AgentConfig)
	if rolesRaw, ok := srcRaw["roles"]; ok {
		b, _ := json.Marshal(rolesRaw)
		json.Unmarshal(b, &srcRoles)
	}

	// Detect source agents directory.
	srcDir := filepath.Dir(srcPath)
	agentsSrcDir := findAgentsDir(srcDir)

	// Summary counters.
	var actions []string
	rolesAdded := 0
	rolesSkipped := 0
	rolesReplaced := 0
	soulsCopied := 0

	// Process agents.
	for name, rc := range srcRoles {
		if _, exists := dstRoles[name]; exists {
			if mode == "merge" {
				rolesSkipped++
				actions = append(actions, fmt.Sprintf("  skip agent %q (already exists)", name))
				continue
			}
			rolesReplaced++
			actions = append(actions, fmt.Sprintf("  replace agent %q", name))
		} else {
			rolesAdded++
			actions = append(actions, fmt.Sprintf("  add agent %q (model=%s, perm=%s)", name, rc.Model, rc.PermissionMode))
		}
		dstRoles[name] = rc

		// Copy SOUL.md if available.
		if agentsSrcDir != "" {
			soulSrc := filepath.Join(agentsSrcDir, name, "SOUL.md")
			if _, err := os.Stat(soulSrc); err == nil {
				soulDstDir := filepath.Join(configDir, "agents", name)
				soulDst := filepath.Join(soulDstDir, "SOUL.md")
				if !dryRun {
					os.MkdirAll(soulDstDir, 0o755)
					if data, err := os.ReadFile(soulSrc); err == nil {
						os.WriteFile(soulDst, data, 0o644)
						soulsCopied++
					}
				}
				actions = append(actions, fmt.Sprintf("  copy SOUL.md for %q", name))
			}
		}
	}

	// Merge Discord config.
	discordActions := importDiscordConfig(srcRaw, dstRaw, mode)
	actions = append(actions, discordActions...)

	// Merge SmartDispatch.
	sdActions := importSmartDispatch(srcRaw, dstRaw, mode)
	actions = append(actions, sdActions...)

	// Import defaultAgent.
	if srcCfg.DefaultAgent != "" {
		actions = append(actions, fmt.Sprintf("  set defaultAgent=%q", srcCfg.DefaultAgent))
		dstRaw["defaultAgent"] = srcCfg.DefaultAgent
	}

	// Update agents in raw config.
	rolesJSON, _ := json.Marshal(dstRoles)
	var rolesAny any
	json.Unmarshal(rolesJSON, &rolesAny)
	dstRaw["roles"] = rolesAny

	// Print summary.
	fmt.Printf("Import config: %s (mode=%s)\n", srcPath, mode)
	if len(actions) == 0 {
		fmt.Println("  No changes needed.")
		return
	}
	fmt.Println("Actions:")
	for _, a := range actions {
		fmt.Println(a)
	}
	fmt.Printf("\nSummary: %d added, %d replaced, %d skipped, %d SOUL files\n",
		rolesAdded, rolesReplaced, rolesSkipped, soulsCopied)

	if dryRun {
		fmt.Println("\n(dry-run: no changes written)")
		return
	}

	// Write updated config.
	out, err := json.MarshalIndent(dstRaw, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error marshaling config: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(configPath, append(out, '\n'), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing config: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("\nConfig updated: %s\n", configPath)
}

// findAgentsDir looks for an agents directory relative to the source config.
func findAgentsDir(srcDir string) string {
	// Check <srcDir>/agents/
	candidate := filepath.Join(srcDir, "agents")
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		return candidate
	}
	// Check <srcDir>/../agents/ (config might be in a nested dir)
	candidate = filepath.Join(srcDir, "..", "agents")
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		return candidate
	}
	return ""
}

// importDiscordConfig merges Discord settings from source into destination.
func importDiscordConfig(src, dst map[string]any, mode string) []string {
	srcDiscord, ok := src["discord"].(map[string]any)
	if !ok {
		return nil
	}
	dstDiscord, _ := dst["discord"].(map[string]any)
	if dstDiscord == nil {
		dstDiscord = map[string]any{}
	}

	var actions []string

	if mode == "replace" {
		// Overwrite channelIDs.
		if ids, ok := srcDiscord["channelIDs"]; ok {
			dstDiscord["channelIDs"] = ids
			actions = append(actions, "  replace discord.channelIDs")
		}
		if ids, ok := srcDiscord["mentionChannelIDs"]; ok {
			dstDiscord["mentionChannelIDs"] = ids
			actions = append(actions, "  replace discord.mentionChannelIDs")
		}
	} else {
		// Merge: union channelIDs.
		if srcIDs, ok := srcDiscord["channelIDs"].([]any); ok && len(srcIDs) > 0 {
			existing := toStringSet(dstDiscord["channelIDs"])
			added := 0
			var merged []any
			if dstIDs, ok := dstDiscord["channelIDs"].([]any); ok {
				merged = dstIDs
			}
			for _, id := range srcIDs {
				s := fmt.Sprint(id)
				if !existing[s] {
					merged = append(merged, id)
					existing[s] = true
					added++
				}
			}
			if added > 0 {
				dstDiscord["channelIDs"] = merged
				actions = append(actions, fmt.Sprintf("  merge discord.channelIDs (+%d)", added))
			}
		}
		if srcIDs, ok := srcDiscord["mentionChannelIDs"].([]any); ok && len(srcIDs) > 0 {
			existing := toStringSet(dstDiscord["mentionChannelIDs"])
			added := 0
			var merged []any
			if dstIDs, ok := dstDiscord["mentionChannelIDs"].([]any); ok {
				merged = dstIDs
			}
			for _, id := range srcIDs {
				s := fmt.Sprint(id)
				if !existing[s] {
					merged = append(merged, id)
					existing[s] = true
					added++
				}
			}
			if added > 0 {
				dstDiscord["mentionChannelIDs"] = merged
				actions = append(actions, fmt.Sprintf("  merge discord.mentionChannelIDs (+%d)", added))
			}
		}
	}

	dst["discord"] = dstDiscord
	return actions
}

// importSmartDispatch merges SmartDispatch settings from source into destination.
func importSmartDispatch(src, dst map[string]any, mode string) []string {
	srcSD, ok := src["smartDispatch"].(map[string]any)
	if !ok {
		return nil
	}

	var actions []string

	if mode == "replace" {
		dst["smartDispatch"] = srcSD
		actions = append(actions, "  replace smartDispatch config")
		return actions
	}

	// Merge: preserve existing settings, add new rules.
	dstSD, _ := dst["smartDispatch"].(map[string]any)
	if dstSD == nil {
		dstSD = map[string]any{}
	}

	// Copy enabled/coordinator/defaultAgent only if not set.
	if _, ok := dstSD["enabled"]; !ok {
		if v, ok := srcSD["enabled"]; ok {
			dstSD["enabled"] = v
			actions = append(actions, "  set smartDispatch.enabled")
		}
	}
	if _, ok := dstSD["coordinator"]; !ok {
		if v, ok := srcSD["coordinator"]; ok {
			dstSD["coordinator"] = v
			actions = append(actions, fmt.Sprintf("  set smartDispatch.coordinator=%v", v))
		}
	}
	if _, ok := dstSD["defaultAgent"]; !ok {
		if v, ok := srcSD["defaultAgent"]; ok {
			dstSD["defaultAgent"] = v
			actions = append(actions, fmt.Sprintf("  set smartDispatch.defaultAgent=%v", v))
		}
	}

	// Append new rules.
	if srcRules, ok := srcSD["rules"].([]any); ok && len(srcRules) > 0 {
		dstRules, _ := dstSD["rules"].([]any)
		existingRoles := make(map[string]bool)
		for _, r := range dstRules {
			if rm, ok := r.(map[string]any); ok {
				if role, ok := rm["role"].(string); ok {
					existingRoles[role] = true
				}
			}
		}
		added := 0
		for _, r := range srcRules {
			if rm, ok := r.(map[string]any); ok {
				if role, ok := rm["role"].(string); ok {
					if !existingRoles[role] {
						dstRules = append(dstRules, r)
						existingRoles[role] = true
						added++
					}
				}
			}
		}
		if added > 0 {
			dstSD["rules"] = dstRules
			actions = append(actions, fmt.Sprintf("  merge smartDispatch.rules (+%d)", added))
		}
	}

	dst["smartDispatch"] = dstSD
	return actions
}

// toStringSet converts a []any to a set of strings for deduplication.
func toStringSet(v any) map[string]bool {
	set := make(map[string]bool)
	if arr, ok := v.([]any); ok {
		for _, item := range arr {
			set[fmt.Sprint(item)] = true
		}
	}
	return set
}

