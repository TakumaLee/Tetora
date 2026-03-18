package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Preset describes a static configuration template for a well-known LLM provider.
type Preset struct {
	Name        string   `json:"name"`        // e.g. "anthropic"
	DisplayName string   `json:"displayName"` // e.g. "Anthropic (Claude)"
	Type        string   `json:"type"`        // maps to ProviderConfig.Type
	BaseURL     string   `json:"baseUrl"`     // default base URL
	RequiresKey bool     `json:"requiresKey"` // whether an API key is required
	Models      []string `json:"models"`      // static default model list
	Dynamic     bool     `json:"dynamic"`     // if true, models can be fetched at runtime
}

// Presets is the built-in registry of supported provider presets.
var Presets = []Preset{
	{
		Name:        "anthropic",
		DisplayName: "Anthropic (Claude)",
		Type:        "openai-compatible",
		BaseURL:     "https://api.anthropic.com/v1",
		RequiresKey: true,
		Models:      []string{"claude-opus-4-6", "claude-sonnet-4-6", "claude-haiku-4-5"},
		Dynamic:     false,
	},
	{
		Name:        "openai",
		DisplayName: "OpenAI",
		Type:        "openai-compatible",
		BaseURL:     "https://api.openai.com/v1",
		RequiresKey: true,
		Models:      []string{"gpt-4o", "gpt-4o-mini", "o3-mini"},
		Dynamic:     false,
	},
	{
		Name:        "google",
		DisplayName: "Google (Gemini)",
		Type:        "openai-compatible",
		BaseURL:     "https://generativelanguage.googleapis.com/v1beta/openai",
		RequiresKey: true,
		Models:      []string{"gemini-2.5-flash", "gemini-2.5-pro"},
		Dynamic:     false,
	},
	{
		Name:        "ollama",
		DisplayName: "Ollama (local)",
		Type:        "openai-compatible",
		BaseURL:     "http://localhost:11434/v1",
		RequiresKey: false,
		Models:      []string{},
		Dynamic:     true,
	},
	{
		Name:        "lmstudio",
		DisplayName: "LM Studio (local)",
		Type:        "openai-compatible",
		BaseURL:     "http://localhost:1234/v1",
		RequiresKey: false,
		Models:      []string{},
		Dynamic:     true,
	},
	{
		Name:        "custom",
		DisplayName: "Custom",
		Type:        "openai-compatible",
		BaseURL:     "",
		RequiresKey: false,
		Models:      []string{},
		Dynamic:     false,
	},
}

// GetPreset returns the preset with the given name, or false if not found.
func GetPreset(name string) (Preset, bool) {
	for _, p := range Presets {
		if p.Name == name {
			return p, true
		}
	}
	return Preset{}, false
}

// FetchPresetModels fetches the available model list for a dynamic preset by
// calling the OpenAI-compatible GET /models endpoint on the running server.
// For non-dynamic presets it returns the static Models slice unchanged.
func FetchPresetModels(p Preset) ([]string, error) {
	if !p.Dynamic {
		return p.Models, nil
	}

	url := p.BaseURL + "/models"
	client := &http.Client{Timeout: 2 * time.Second}

	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetch models from %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("fetch models from %s: HTTP %d: %s", url, resp.StatusCode, TruncateBytes(body, 200))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read models response from %s: %w", url, err)
	}

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse models response from %s: %w", url, err)
	}

	models := make([]string, 0, len(result.Data))
	for _, entry := range result.Data {
		if entry.ID != "" {
			models = append(models, entry.ID)
		}
	}
	return models, nil
}
