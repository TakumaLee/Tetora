package team

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

// ApplyOptions controls how a team is applied to config.
type ApplyOptions struct {
	Force bool // overwrite existing agents with same key
}

// Apply writes a team's agents into the Tetora config.json and creates SOUL.md files.
// configPath is the absolute path to config.json.
// agentsDir is the path to the agents/ directory (e.g. ~/.tetora/agents).
// signalReload is called after successful apply to trigger SIGHUP.
func Apply(teamName string, store *Storage, configPath, agentsDir string, opts ApplyOptions, signalReload func()) error {
	team, err := store.Load(teamName)
	if err != nil {
		return fmt.Errorf("load team: %w", err)
	}

	// Step 1: Backup config.json.
	configData, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	backupPath := configPath + ".bak"
	if err := os.WriteFile(backupPath, configData, 0o600); err != nil {
		return fmt.Errorf("backup config: %w", err)
	}

	// Step 2: Parse config as raw map to preserve unknown fields.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(configData, &raw); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	// Step 3: Inject agents.
	agents := make(map[string]json.RawMessage)
	if agentsRaw, ok := raw["agents"]; ok {
		if err := json.Unmarshal(agentsRaw, &agents); err != nil {
			log.Printf("[team/apply] warning: failed to parse existing agents field: %v", err)
		}
	} else if rolesRaw, ok := raw["roles"]; ok {
		if err := json.Unmarshal(rolesRaw, &agents); err != nil {
			log.Printf("[team/apply] warning: failed to parse existing roles field: %v", err)
		}
		delete(raw, "roles")
	}

	for _, a := range team.Agents {
		if _, exists := agents[a.Key]; exists && !opts.Force {
			return fmt.Errorf("agent %q already exists (use force to overwrite)", a.Key)
		}

		ac := agentConfigJSON{
			SoulFile:       fmt.Sprintf("agents/%s/SOUL.md", a.Key),
			Model:          a.Model,
			Description:    a.Description,
			Keywords:       a.Keywords,
			PermissionMode: a.PermissionMode,
		}
		b, _ := json.Marshal(ac)
		agents[a.Key] = b
	}

	agentsBytes, err := json.Marshal(agents)
	if err != nil {
		return fmt.Errorf("marshal agents: %w", err)
	}
	raw["agents"] = agentsBytes

	// Step 4: Inject smartDispatch routing rules (append).
	var sd map[string]json.RawMessage
	if sdRaw, ok := raw["smartDispatch"]; ok {
		if err := json.Unmarshal(sdRaw, &sd); err != nil {
			log.Printf("[team/apply] warning: failed to parse existing smartDispatch field: %v", err)
		}
	}
	if sd == nil {
		sd = make(map[string]json.RawMessage)
	}

	var existingRules []routingRuleJSON
	if rulesRaw, ok := sd["rules"]; ok {
		if err := json.Unmarshal(rulesRaw, &existingRules); err != nil {
			log.Printf("[team/apply] warning: failed to parse existing smartDispatch.rules: %v", err)
		}
	}

	// Build set of existing agent keys in rules for dedup.
	ruleAgents := make(map[string]bool)
	for _, r := range existingRules {
		ruleAgents[r.Agent] = true
	}

	for _, a := range team.Agents {
		if ruleAgents[a.Key] && !opts.Force {
			continue // skip existing rules
		}
		if ruleAgents[a.Key] && opts.Force {
			// Remove existing rule for this agent.
			filtered := existingRules[:0]
			for _, r := range existingRules {
				if r.Agent != a.Key {
					filtered = append(filtered, r)
				}
			}
			existingRules = filtered
		}
		existingRules = append(existingRules, routingRuleJSON{
			Agent:    a.Key,
			Keywords: a.Keywords,
			Patterns: a.Patterns,
		})
	}

	rulesBytes, _ := json.Marshal(existingRules)
	sd["rules"] = rulesBytes

	// Ensure smartDispatch.enabled is true.
	sd["enabled"] = json.RawMessage(`true`)

	sdBytes, _ := json.Marshal(sd)
	raw["smartDispatch"] = sdBytes

	// Step 5: Atomic write config.json.
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	tmp := configPath + ".tmp"
	if err := os.WriteFile(tmp, append(out, '\n'), 0o600); err != nil {
		return fmt.Errorf("write config tmp: %w", err)
	}
	if err := os.Rename(tmp, configPath); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename config: %w", err)
	}

	// Step 6: Create SOUL.md files.
	for _, a := range team.Agents {
		dir := filepath.Join(agentsDir, a.Key)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create agent dir %q: %w", a.Key, err)
		}
		soulPath := filepath.Join(dir, "SOUL.md")
		if err := os.WriteFile(soulPath, []byte(a.Soul), 0o644); err != nil {
			return fmt.Errorf("write SOUL.md for %q: %w", a.Key, err)
		}
	}

	// Step 7: Signal reload.
	if signalReload != nil {
		signalReload()
	}

	return nil
}

// --- JSON helper structs (match config.json shape) ---

type agentConfigJSON struct {
	SoulFile       string   `json:"soulFile"`
	Model          string   `json:"model"`
	Description    string   `json:"description"`
	Keywords       []string `json:"keywords,omitempty"`
	PermissionMode string   `json:"permissionMode,omitempty"`
}

type routingRuleJSON struct {
	Agent    string   `json:"agent"`
	Keywords []string `json:"keywords"`
	Patterns []string `json:"patterns,omitempty"`
}
