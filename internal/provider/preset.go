package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// AnthropicVersion is the Anthropic API version header value shared across the package.
const AnthropicVersion = "2023-06-01"

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
		Name:        "groq",
		DisplayName: "Groq",
		Type:        "openai-compatible",
		BaseURL:     "https://api.groq.com/openai/v1",
		RequiresKey: true,
		Models:      []string{"llama-3.3-70b-versatile", "llama-3.1-8b-instant", "mixtral-8x7b-32768"},
		Dynamic:     false,
	},
	{
		Name:        "anthropic",
		DisplayName: "Anthropic (Claude)",
		Type:        "anthropic",
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
		Models:      []string{"gpt-5.4", "gpt-5.3", "gpt-4o", "gpt-4o-mini", "o3-mini"},
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
		Name:        "codex",
		DisplayName: "OpenAI Codex CLI",
		Type:        "codex-cli",
		BaseURL:     "",
		RequiresKey: false,
		Models:      []string{"gpt-5.4", "gpt-5.3", "o4-mini", "o3-mini", "gpt-4o"},
		Dynamic:     false,
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

// modelPrefixToPreset maps model name prefixes to preset names for auto-inference.
// OpenAI models map to "codex" (Codex CLI, subscription-based) rather than
// "openai" (API key required) so users can use their Codex subscription.
var modelPrefixToPreset = []struct {
	prefix string
	preset string
}{
	// OpenAI → Codex CLI (subscription login, no API key needed)
	{"gpt-", "codex"},
	{"o1-", "codex"},
	{"o3-", "codex"},
	{"o4-", "codex"},
	{"codex-", "codex"},
	// Anthropic → resolved via claudeProvider preference
	{"claude-", "claude"},
	// Google
	{"gemini-", "google"},
	// Groq
	{"llama-", "groq"},
	{"mixtral-", "groq"},
}

// InferProviderFromModel returns the preset name that likely serves the given
// model, based on well-known model name prefixes. Returns ("", false) if the
// model doesn't match any known pattern (e.g. custom or local models).
//
// For Claude models, the result depends on the configured preference:
// if claudeProvider is set (e.g. "claude-code" or "anthropic"), it is used;
// otherwise defaults to "claude-code" (CLI).
func InferProviderFromModel(model string) (presetName string, ok bool) {
	return InferProviderFromModelWithPref(model, "")
}

// InferProviderFromModelWithPref is like InferProviderFromModel but accepts
// a claudeProvider preference to resolve Claude model ambiguity.
func InferProviderFromModelWithPref(model, claudeProvider string) (presetName string, ok bool) {
	if claudeProvider == "" {
		claudeProvider = "claude-code" // default to CLI
	}
	lower := strings.ToLower(model)
	for _, entry := range modelPrefixToPreset {
		if strings.HasPrefix(lower, entry.prefix) {
			// Resolve the "claude" placeholder to actual preference.
			if entry.preset == "claude" {
				return claudeProvider, true
			}
			return entry.preset, true
		}
	}
	// Also check Anthropic short aliases: "sonnet", "opus", "haiku"
	for _, alias := range []string{"sonnet", "opus", "haiku"} {
		if lower == alias {
			return claudeProvider, true
		}
	}
	return "", false
}

// IsLocalProvider returns true if the named provider runs locally (e.g. Ollama, LM Studio).
func IsLocalProvider(name string) bool {
	return name == "ollama" || name == "lmstudio"
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

// ValidateCustomURL checks that a user-supplied URL is safe to use as a provider
// endpoint. It rejects non-http/https schemes and RFC1918/loopback/link-local/
// cloud-metadata addresses to prevent SSRF.
func ValidateCustomURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("URL scheme must be http or https, got %q", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("URL must include a host")
	}
	// Resolve to IP for range checks.
	ip := net.ParseIP(host)
	if ip == nil {
		addrs, err := net.LookupHost(host)
		if err != nil || len(addrs) == 0 {
			return fmt.Errorf("cannot resolve host %q", host)
		}
		ip = net.ParseIP(addrs[0])
	}
	if ip != nil && isPrivateIP(ip) {
		return fmt.Errorf("host %q resolves to a private or restricted address", host)
	}
	return nil
}

// privateRanges lists IP blocks forbidden in custom provider URLs.
var privateRanges = func() []*net.IPNet {
	cidrs := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"127.0.0.0/8",
		"169.254.0.0/16", // link-local / AWS metadata
		"::1/128",
		"fc00::/7",
		"fe80::/10",
	}
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, block, _ := net.ParseCIDR(cidr)
		if block != nil {
			nets = append(nets, block)
		}
	}
	return nets
}()

func isPrivateIP(ip net.IP) bool {
	for _, block := range privateRanges {
		if block.Contains(ip) {
			return true
		}
	}
	return false
}

// TestPresetConnection sends a minimal chat completion request to verify
// that the provider endpoint is reachable and the API key is accepted.
// Returns nil on success; the caller should treat errors as warnings.
func TestPresetConnection(p Preset, apiKey, model string) error {
	client := &http.Client{Timeout: 10 * time.Second}

	// Anthropic native endpoint uses /messages with x-api-key auth.
	// The OpenAI-compat /chat/completions endpoint expects Authorization: Bearer,
	// so hitting it with x-api-key would return 401 despite a valid key.
	var testURL string
	if p.Name == "anthropic" {
		testURL = p.BaseURL + "/messages"
	} else {
		testURL = p.BaseURL + "/chat/completions"
	}
	body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"ping"}],"max_tokens":1}`, model)

	req, err := http.NewRequest("POST", testURL, strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("connect %s: %w", p.BaseURL, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		if p.Name == "anthropic" {
			req.Header.Set("x-api-key", apiKey)
			req.Header.Set("anthropic-version", AnthropicVersion)
		} else {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("connect %s: %w", p.BaseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, TruncateBytes(respBody, 200))
	}
	return nil
}
