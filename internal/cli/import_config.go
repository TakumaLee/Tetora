package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// CmdImportConfig imports agents, channels, and settings from another config.json.
//
// Usage: tetora import config <path> [--mode merge|replace] [--dry-run]
func CmdImportConfig(args []string) {
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

	var srcRaw map[string]any
	if err := json.Unmarshal(srcData, &srcRaw); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing source config: %v\n", err)
		os.Exit(1)
	}

	// Load current config.
	configPath := FindConfigPath()
	configDir := filepath.Dir(configPath)

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

	// Parse existing agents from destination (try "agents" first, fall back to "roles").
	dstAgents := make(map[string]map[string]any)
	if agentsRaw, ok := dstRaw["agents"]; ok {
		b, _ := json.Marshal(agentsRaw)
		json.Unmarshal(b, &dstAgents) //nolint:errcheck
	} else if rolesRaw, ok := dstRaw["roles"]; ok {
		b, _ := json.Marshal(rolesRaw)
		json.Unmarshal(b, &dstAgents) //nolint:errcheck
	}

	// Parse source agents (try "agents" first, fall back to "roles").
	srcAgents := make(map[string]map[string]any)
	if agentsRaw, ok := srcRaw["agents"]; ok {
		b, _ := json.Marshal(agentsRaw)
		json.Unmarshal(b, &srcAgents) //nolint:errcheck
	} else if rolesRaw, ok := srcRaw["roles"]; ok {
		b, _ := json.Marshal(rolesRaw)
		json.Unmarshal(b, &srcAgents) //nolint:errcheck
	}

	// Detect source agents directory.
	srcDir := filepath.Dir(srcPath)
	agentsSrcDir := importFindAgentsDir(srcDir)

	// Summary counters.
	var actions []string
	agentsAdded := 0
	agentsSkipped := 0
	agentsReplaced := 0
	soulsCopied := 0

	// Process agents.
	for name, rc := range srcAgents {
		if _, exists := dstAgents[name]; exists {
			if mode == "merge" {
				agentsSkipped++
				actions = append(actions, fmt.Sprintf("  skip agent %q (already exists)", name))
				continue
			}
			agentsReplaced++
			actions = append(actions, fmt.Sprintf("  replace agent %q", name))
		} else {
			model, _ := rc["model"].(string)
			perm, _ := rc["permissionMode"].(string)
			agentsAdded++
			actions = append(actions, fmt.Sprintf("  add agent %q (model=%s, perm=%s)", name, model, perm))
		}
		dstAgents[name] = rc

		// Copy SOUL.md if available.
		if agentsSrcDir != "" {
			soulSrc := filepath.Join(agentsSrcDir, name, "SOUL.md")
			if _, err := os.Stat(soulSrc); err == nil {
				soulDstDir := filepath.Join(configDir, "agents", name)
				soulDst := filepath.Join(soulDstDir, "SOUL.md")
				if !dryRun {
					os.MkdirAll(soulDstDir, 0o755)
					if data, err := os.ReadFile(soulSrc); err == nil {
						os.WriteFile(soulDst, data, 0o644) //nolint:errcheck
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
	if defaultAgent, _ := srcRaw["defaultAgent"].(string); defaultAgent != "" {
		actions = append(actions, fmt.Sprintf("  set defaultAgent=%q", defaultAgent))
		dstRaw["defaultAgent"] = defaultAgent
	}

	// Update agents in raw config.
	agentsJSON, _ := json.Marshal(dstAgents)
	var agentsAny any
	json.Unmarshal(agentsJSON, &agentsAny) //nolint:errcheck
	dstRaw["agents"] = agentsAny
	delete(dstRaw, "roles") // clean up old key

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
		agentsAdded, agentsReplaced, agentsSkipped, soulsCopied)

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
	if err := os.WriteFile(configPath, append(out, '\n'), 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing config: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("\nConfig updated: %s\n", configPath)
}

// importFindAgentsDir looks for an agents directory relative to the source config.
func importFindAgentsDir(srcDir string) string {
	candidate := filepath.Join(srcDir, "agents")
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		return candidate
	}
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
		if ids, ok := srcDiscord["channelIDs"]; ok {
			dstDiscord["channelIDs"] = ids
			actions = append(actions, "  replace discord.channelIDs")
		}
		if ids, ok := srcDiscord["mentionChannelIDs"]; ok {
			dstDiscord["mentionChannelIDs"] = ids
			actions = append(actions, "  replace discord.mentionChannelIDs")
		}
	} else {
		if srcIDs, ok := srcDiscord["channelIDs"].([]any); ok && len(srcIDs) > 0 {
			existing := importToStringSet(dstDiscord["channelIDs"])
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
			existing := importToStringSet(dstDiscord["mentionChannelIDs"])
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

	dstSD, _ := dst["smartDispatch"].(map[string]any)
	if dstSD == nil {
		dstSD = map[string]any{}
	}

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

	if srcRules, ok := srcSD["rules"].([]any); ok && len(srcRules) > 0 {
		dstRules, _ := dstSD["rules"].([]any)
		existingAgents := make(map[string]bool)
		for _, r := range dstRules {
			if rm, ok := r.(map[string]any); ok {
				if agent, ok := rm["agent"].(string); ok {
					existingAgents[agent] = true
				} else if role, ok := rm["role"].(string); ok {
					existingAgents[role] = true
				}
			}
		}
		added := 0
		for _, r := range srcRules {
			if rm, ok := r.(map[string]any); ok {
				agent, _ := rm["agent"].(string)
				if agent == "" {
					agent, _ = rm["role"].(string)
				}
				if agent != "" && !existingAgents[agent] {
					dstRules = append(dstRules, r)
					existingAgents[agent] = true
					added++
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

// importToStringSet converts a []any to a set of strings for deduplication.
func importToStringSet(v any) map[string]bool {
	set := make(map[string]bool)
	if arr, ok := v.([]any); ok {
		for _, item := range arr {
			set[fmt.Sprint(item)] = true
		}
	}
	return set
}
