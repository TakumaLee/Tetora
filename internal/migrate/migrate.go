// Package migrate handles config schema versioning and upgrades.
package migrate

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	tlog "tetora/internal/log"
)

// CurrentConfigVersion is the latest config schema version.
const CurrentConfigVersion = 3

// Migration describes a single config schema migration.
type Migration struct {
	Version     int
	Description string
	Migrate     func(raw map[string]json.RawMessage) error
}

// Migrations is the ordered list of all config migrations.
var Migrations = []Migration{
	{
		Version:     2,
		Description: "Add configVersion, smartDispatch defaults, knowledgeDir",
		Migrate: func(raw map[string]json.RawMessage) error {
			v, _ := json.Marshal(2)
			raw["configVersion"] = v

			if _, ok := raw["smartDispatch"]; !ok {
				sd := map[string]interface{}{
					"enabled":         false,
					"coordinator":     "琉璃",
					"defaultAgent":    "琉璃",
					"classifyBudget":  0.1,
					"classifyTimeout": "30s",
				}
				b, err := json.Marshal(sd)
				if err != nil {
					return fmt.Errorf("marshal smartDispatch: %w", err)
				}
				raw["smartDispatch"] = b
			}

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
			if _, ok := raw["agents"]; !ok {
				if rolesRaw, ok := raw["roles"]; ok {
					raw["agents"] = rolesRaw
					delete(raw, "roles")
				}
			}

			if sdRaw, ok := raw["smartDispatch"]; ok {
				var sd map[string]json.RawMessage
				if err := json.Unmarshal(sdRaw, &sd); err == nil {
					if _, ok := sd["defaultAgent"]; !ok {
						if drRaw, ok := sd["defaultRole"]; ok {
							sd["defaultAgent"] = drRaw
							delete(sd, "defaultRole")
						}
					}

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

			v, _ := json.Marshal(3)
			raw["configVersion"] = v

			return nil
		},
	},
}

// GetConfigVersion parses the configVersion field from raw JSON config.
func GetConfigVersion(raw map[string]json.RawMessage) int {
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

// MigrateConfig reads a config file, detects its version, and applies
// all pending migrations in order. If dryRun is true, the file is not modified.
func MigrateConfig(configPath string, dryRun bool) ([]string, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	currentVer := GetConfigVersion(raw)
	if currentVer >= CurrentConfigVersion {
		return nil, nil
	}

	var applied []string
	for _, m := range Migrations {
		if m.Version <= currentVer {
			continue
		}
		if err := m.Migrate(raw); err != nil {
			return applied, fmt.Errorf("migration v%d (%s): %w", m.Version, m.Description, err)
		}
		applied = append(applied, fmt.Sprintf("v%d: %s", m.Version, m.Description))
	}

	vBytes, _ := json.Marshal(CurrentConfigVersion)
	raw["configVersion"] = vBytes

	if dryRun {
		return applied, nil
	}

	backupPath := configPath + ".backup." + time.Now().Format("20060102-150405")
	if err := os.WriteFile(backupPath, data, 0o600); err != nil {
		return applied, fmt.Errorf("create backup: %w", err)
	}

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return applied, fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(configPath, append(out, '\n'), 0o600); err != nil {
		return applied, fmt.Errorf("write config: %w", err)
	}

	return applied, nil
}

// AutoMigrateConfig checks the config version and applies migrations if needed.
func AutoMigrateConfig(configPath string) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return
	}

	ver := GetConfigVersion(raw)
	if ver >= CurrentConfigVersion {
		return
	}

	tlog.Info("config auto-migration starting", "currentVersion", ver, "targetVersion", CurrentConfigVersion)
	applied, err := MigrateConfig(configPath, false)
	if err != nil {
		tlog.Warn("config migration failed", "error", err)
		return
	}
	for _, desc := range applied {
		tlog.Info("config migration applied", "migration", desc)
	}
	tlog.Info("config migration completed", "version", CurrentConfigVersion)
}
