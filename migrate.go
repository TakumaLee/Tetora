package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// currentConfigVersion is the latest config schema version.
// Bump this when adding new migrations.
const currentConfigVersion = 3

// Migration describes a single config schema migration.
type Migration struct {
	Version     int
	Description string
	Migrate     func(raw map[string]json.RawMessage) error
}

// migrations is the ordered list of all config migrations.
// Each migration upgrades from Version-1 to Version.
var migrations = []Migration{
	{
		Version:     2,
		Description: "Add configVersion, smartDispatch defaults, knowledgeDir",
		Migrate: func(raw map[string]json.RawMessage) error {
			// Set configVersion to 2.
			v, _ := json.Marshal(2)
			raw["configVersion"] = v

			// Add smartDispatch with defaults if missing.
			if _, ok := raw["smartDispatch"]; !ok {
				sd := SmartDispatchConfig{
					Enabled:         false,
					Coordinator:     "琉璃",
					DefaultAgent:     "琉璃",
					ClassifyBudget:  0.1,
					ClassifyTimeout: "30s",
				}
				b, err := json.Marshal(sd)
				if err != nil {
					return fmt.Errorf("marshal smartDispatch: %w", err)
				}
				raw["smartDispatch"] = b
			}

			// Add knowledgeDir default if missing.
			if _, ok := raw["knowledgeDir"]; !ok {
				b, _ := json.Marshal("knowledge")
				raw["knowledgeDir"] = b
			}

			return nil
		},
	},
	{
		Version:     3,
		Description: "Rename roles->agents, defaultRole->defaultAgent, rule.role->rule.agent",
		Migrate: func(raw map[string]json.RawMessage) error {
			// Rename top-level "roles" → "agents".
			if _, ok := raw["agents"]; !ok {
				if rolesRaw, ok := raw["roles"]; ok {
					raw["agents"] = rolesRaw
					delete(raw, "roles")
				}
			}

			// Rename inside smartDispatch: "defaultRole" → "defaultAgent", rules[].role → rules[].agent.
			if sdRaw, ok := raw["smartDispatch"]; ok {
				var sd map[string]json.RawMessage
				if err := json.Unmarshal(sdRaw, &sd); err == nil {
					// defaultRole → defaultAgent
					if _, ok := sd["defaultAgent"]; !ok {
						if drRaw, ok := sd["defaultRole"]; ok {
							sd["defaultAgent"] = drRaw
							delete(sd, "defaultRole")
						}
					}

					// rules[].role → rules[].agent
					if rulesRaw, ok := sd["rules"]; ok {
						var rules []map[string]json.RawMessage
						if err := json.Unmarshal(rulesRaw, &rules); err == nil {
							for i, rule := range rules {
								if _, ok := rule["agent"]; !ok {
									if roleRaw, ok := rule["role"]; ok {
										rule["agent"] = roleRaw
										delete(rule, "role")
										rules[i] = rule
									}
								}
							}
							if b, err := json.Marshal(rules); err == nil {
								sd["rules"] = b
							}
						}
					}

					if b, err := json.Marshal(sd); err == nil {
						raw["smartDispatch"] = b
					}
				}
			}

			// Rename inside discord.routes: {id}.role → {id}.agent.
			if discordRaw, ok := raw["discord"]; ok {
				var discord map[string]json.RawMessage
				if err := json.Unmarshal(discordRaw, &discord); err == nil {
					if routesRaw, ok := discord["routes"]; ok {
						var routes map[string]map[string]json.RawMessage
						if err := json.Unmarshal(routesRaw, &routes); err == nil {
							for id, route := range routes {
								if _, ok := route["agent"]; !ok {
									if roleRaw, ok := route["role"]; ok {
										route["agent"] = roleRaw
										delete(route, "role")
										routes[id] = route
									}
								}
							}
							if b, err := json.Marshal(routes); err == nil {
								discord["routes"] = b
							}
						}
					}
					if b, err := json.Marshal(discord); err == nil {
						raw["discord"] = b
					}
				}
			}

			// Set configVersion.
			v, _ := json.Marshal(3)
			raw["configVersion"] = v

			return nil
		},
	},
}

// getConfigVersion parses the configVersion field from raw JSON config.
// Returns 1 if the field is missing or invalid (pre-versioning configs).
func getConfigVersion(raw map[string]json.RawMessage) int {
	vRaw, ok := raw["configVersion"]
	if !ok {
		return 1
	}
	var v int
	if err := json.Unmarshal(vRaw, &v); err != nil {
		return 1
	}
	if v <= 0 {
		return 1
	}
	return v
}

// migrateConfig reads a config file, detects its version, and applies
// all pending migrations in order. If dryRun is true, the file is not
// modified. Returns the list of applied migration descriptions.
//
// Before writing, a backup is created at configPath.backup.TIMESTAMP.
func migrateConfig(configPath string, dryRun bool) ([]string, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	currentVer := getConfigVersion(raw)
	if currentVer >= currentConfigVersion {
		return nil, nil // already up to date
	}

	var applied []string
	for _, m := range migrations {
		if m.Version <= currentVer {
			continue
		}
		if err := m.Migrate(raw); err != nil {
			return applied, fmt.Errorf("migration v%d (%s): %w", m.Version, m.Description, err)
		}
		applied = append(applied, fmt.Sprintf("v%d: %s", m.Version, m.Description))
	}

	// Update configVersion in raw JSON.
	vBytes, _ := json.Marshal(currentConfigVersion)
	raw["configVersion"] = vBytes

	if dryRun {
		return applied, nil
	}

	// Create backup before writing.
	backupPath := configPath + ".backup." + time.Now().Format("20060102-150405")
	if err := os.WriteFile(backupPath, data, 0o644); err != nil {
		return applied, fmt.Errorf("create backup: %w", err)
	}

	// Write migrated config.
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return applied, fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(configPath, append(out, '\n'), 0o644); err != nil {
		return applied, fmt.Errorf("write config: %w", err)
	}

	return applied, nil
}

// autoMigrateConfig checks the config version and applies migrations
// if needed. Called during loadConfig(). Returns the config path used
// for re-reading if migration was performed.
func autoMigrateConfig(configPath string) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return
	}

	ver := getConfigVersion(raw)
	if ver >= currentConfigVersion {
		return
	}

	logInfo("config auto-migration starting", "currentVersion", ver, "targetVersion", currentConfigVersion)
	applied, err := migrateConfig(configPath, false)
	if err != nil {
		logWarn("config migration failed", "error", err)
		return
	}
	for _, desc := range applied {
		logInfo("config migration applied", "migration", desc)
	}
	logInfo("config migration completed", "version", currentConfigVersion)
}
