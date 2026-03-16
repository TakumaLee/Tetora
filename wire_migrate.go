package main

// wire_migrate.go — thin wrappers over internal/migrate.

import (
	"encoding/json"

	"tetora/internal/migrate"
)

const currentConfigVersion = migrate.CurrentConfigVersion

func getConfigVersion(raw map[string]json.RawMessage) int {
	return migrate.GetConfigVersion(raw)
}

func migrateConfig(configPath string, dryRun bool) ([]string, error) {
	return migrate.MigrateConfig(configPath, dryRun)
}

func autoMigrateConfig(configPath string) {
	migrate.AutoMigrateConfig(configPath)
}
