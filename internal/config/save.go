package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

	if err := writeFileAtomic(configPath, append(out, '\n')); err != nil {
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

	if err := writeFileAtomic(configPath, append(out, '\n')); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// SaveInferenceMode atomically updates the inferenceMode field and multiple
// agent entries in the on-disk config.json in a single write.
func SaveInferenceMode(configPath, mode string, agents map[string]AgentConfig) error {
	configFileMu.Lock()
	defer configFileMu.Unlock()

	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	// Update inferenceMode.
	modeJSON, _ := json.Marshal(mode)
	raw["inferenceMode"] = modeJSON

	// Merge updated agents into existing agents map.
	var existingAgents map[string]json.RawMessage
	if agentsRaw, ok := raw["agents"]; ok {
		json.Unmarshal(agentsRaw, &existingAgents)
	}
	if existingAgents == nil {
		existingAgents = make(map[string]json.RawMessage)
	}
	for name, ac := range agents {
		acJSON, err := json.Marshal(&ac)
		if err != nil {
			return fmt.Errorf("marshal agent %s: %w", name, err)
		}
		existingAgents[name] = acJSON
	}
	agentsBytes, err := json.Marshal(existingAgents)
	if err != nil {
		return fmt.Errorf("marshal agents: %w", err)
	}
	raw["agents"] = agentsBytes

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := writeFileAtomic(configPath, append(out, '\n')); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// writeFileAtomic writes data to a .tmp file in the same directory as dst,
// then renames it to dst. os.Rename is atomic on the same filesystem (POSIX),
// so a crash mid-write leaves dst either fully updated or fully intact.
func writeFileAtomic(dst string, data []byte) error {
	tmp := filepath.Join(filepath.Dir(dst), filepath.Base(dst)+".tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp) // best-effort cleanup
		return err
	}
	return nil
}
