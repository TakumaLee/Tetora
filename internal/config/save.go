package config

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// configFileMu protects concurrent writes to the config file on disk.
var configFileMu sync.Mutex

// SaveProviders merges the given provider into the on-disk config.json and
// writes the result back atomically. configPath must be an absolute path.
func SaveProviders(configPath, name string, pc ProviderConfig) error {
	configFileMu.Lock()
	defer configFileMu.Unlock()

	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	// Ensure providers map exists.
	providers, _ := raw["providers"].(map[string]any)
	if providers == nil {
		providers = make(map[string]any)
	}

	// Convert ProviderConfig to map for JSON merge.
	pcBytes, err := json.Marshal(pc)
	if err != nil {
		return fmt.Errorf("marshal provider config: %w", err)
	}
	var pcMap map[string]any
	if err := json.Unmarshal(pcBytes, &pcMap); err != nil {
		return fmt.Errorf("convert provider config: %w", err)
	}

	providers[name] = pcMap
	raw["providers"] = providers

	// Validate the result can round-trip before writing.
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(configPath, append(out, '\n'), 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// DeleteProvider removes a provider from the on-disk config.json.
func DeleteProvider(configPath, name string) error {
	configFileMu.Lock()
	defer configFileMu.Unlock()

	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	providers, _ := raw["providers"].(map[string]any)
	if providers != nil {
		delete(providers, name)
		raw["providers"] = providers
	}

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(configPath, append(out, '\n'), 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}
