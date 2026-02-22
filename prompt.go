package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// PromptInfo represents a prompt template file.
type PromptInfo struct {
	Name    string `json:"name"`
	Preview string `json:"preview,omitempty"` // first ~100 chars
	Content string `json:"content,omitempty"` // full content (only when requested)
}

// promptsDir returns the prompts directory path, creating it if needed.
func promptsDir(cfg *Config) string {
	dir := filepath.Join(cfg.baseDir, "prompts")
	os.MkdirAll(dir, 0o755)
	return dir
}

// listPrompts returns all .md files in the prompts directory.
func listPrompts(cfg *Config) ([]PromptInfo, error) {
	dir := promptsDir(cfg)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var prompts []PromptInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		preview := ""
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err == nil {
			preview = string(data)
			if len(preview) > 100 {
				preview = preview[:100] + "..."
			}
			// Collapse newlines for preview.
			preview = strings.ReplaceAll(preview, "\n", " ")
		}
		prompts = append(prompts, PromptInfo{Name: name, Preview: preview})
	}

	sort.Slice(prompts, func(i, j int) bool {
		return prompts[i].Name < prompts[j].Name
	})
	return prompts, nil
}

// readPrompt reads a prompt file by name.
func readPrompt(cfg *Config, name string) (string, error) {
	path := filepath.Join(promptsDir(cfg), name+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("prompt %q not found", name)
	}
	return string(data), nil
}

// writePrompt creates or updates a prompt file.
func writePrompt(cfg *Config, name, content string) error {
	if name == "" {
		return fmt.Errorf("prompt name is required")
	}
	// Sanitize name: only allow alphanumeric, hyphens, underscores.
	for _, r := range name {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
			return fmt.Errorf("invalid character %q in prompt name (use a-z, 0-9, -, _)", string(r))
		}
	}
	path := filepath.Join(promptsDir(cfg), name+".md")
	return os.WriteFile(path, []byte(content), 0o644)
}

// deletePrompt removes a prompt file.
func deletePrompt(cfg *Config, name string) error {
	path := filepath.Join(promptsDir(cfg), name+".md")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("prompt %q not found", name)
	}
	return os.Remove(path)
}

// resolvePromptFile reads a prompt from the prompts directory.
// Used by cron to resolve CronTaskConfig.PromptFile.
func resolvePromptFile(cfg *Config, promptFile string) (string, error) {
	if promptFile == "" {
		return "", nil
	}
	// Allow with or without .md extension.
	name := strings.TrimSuffix(promptFile, ".md")
	return readPrompt(cfg, name)
}
