package provider

import "tetora/internal/config"

// ProviderProfile contains optimized default parameters for each provider.
// This allows users to switch providers without manually tuning parameters.
//
// NOTE: DefaultModel values are hardcoded and may become stale as providers
// release new versions. These profiles should be treated as reference defaults
// and may need periodic manual updates. Users can override these values in
// their config.json to use newer models without waiting for code updates.
// The primary value-add of this system is the auto model resolution, which
// falls back to these profiles only when no explicit model is configured.
type ProviderProfile struct {
	Name              string
	Type              string
	BaseURL           string
	DefaultModel      string
	MaxTokens         int
	Temperature       float64
	TopP              float64
	FirstTokenTimeout string
	ContextWindow     int // Maximum context window in tokens
	
	// Capabilities
	SupportsTools     bool
	SupportsStreaming bool
	SupportsVision    bool
	
	// Characteristics
	Strengths         []string // What this provider is good at
	BestFor           []string // Recommended use cases
}

// ProviderProfiles returns a map of all known provider profiles.
func ProviderProfiles() map[string]ProviderProfile {
	return map[string]ProviderProfile{
		// --- Anthropic Claude ---
		"claude-code": {
			Name:              "Claude Code",
			Type:              "claude-code",
			DefaultModel:      "claude-sonnet-4-20250514",
			MaxTokens:         8192,
			Temperature:       0.7,
			TopP:              0.9,
			FirstTokenTimeout: "30s",
			ContextWindow:     200000,
			SupportsTools:     true,
			SupportsStreaming: true,
			SupportsVision:    true,
			Strengths:         []string{"code-understanding", "complex-reasoning", "long-context"},
			BestFor:           []string{"code-review", "architecture", "refactoring"},
		},
		"anthropic": {
			Name:              "Anthropic API",
			Type:              "anthropic",
			DefaultModel:      "claude-sonnet-4-20250514",
			MaxTokens:         8192,
			Temperature:       0.7,
			TopP:              0.9,
			FirstTokenTimeout: "30s",
			ContextWindow:     200000,
			SupportsTools:     true,
			SupportsStreaming: true,
			SupportsVision:    true,
			Strengths:         []string{"code-understanding", "complex-reasoning", "long-context"},
			BestFor:           []string{"code-review", "architecture", "refactoring"},
		},
		
		// --- Alibaba Qwen ---
		"qwen": {
			Name:              "Qwen (通义千问)",
			Type:              "openai-compatible",
			BaseURL:           "https://dashscope.aliyuncs.com/compatible-mode/v1",
			DefaultModel:      "qwen3.6-plus",
			MaxTokens:         8192,
			Temperature:       0.7,
			TopP:              0.8,
			FirstTokenTimeout: "30s",
			ContextWindow:     131072,
			SupportsTools:     true,
			SupportsStreaming: true,
			SupportsVision:    true,
			Strengths:         []string{"chinese-language", "code-generation", "cost-effective"},
			BestFor:           []string{"coding", "translation", "summarization"},
		},
		"qwen-cli": {
			Name:              "Qwen CLI (Terminal)",
			Type:              "terminal-qwen",
			DefaultModel:      "qwen3.6-plus",
			MaxTokens:         8192,
			Temperature:       0.7,
			TopP:              0.8,
			FirstTokenTimeout: "30s",
			ContextWindow:     131072,
			SupportsTools:     true,
			SupportsStreaming: true,
			SupportsVision:    false,
			Strengths:         []string{"chinese-language", "code-generation", "cost-effective"},
			BestFor:           []string{"coding", "translation", "summarization"},
		},
		
		// --- Google Gemini ---
		"google": {
			Name:              "Google Gemini",
			Type:              "openai-compatible",
			BaseURL:           "https://generativelanguage.googleapis.com/v1beta/openai",
			DefaultModel:      "gemini-2.5-pro",
			MaxTokens:         65536,
			Temperature:       0.6,
			TopP:              0.95,
			FirstTokenTimeout: "45s",
			ContextWindow:     1000000,
			SupportsTools:     true,
			SupportsStreaming: true,
			SupportsVision:    true,
			Strengths:         []string{"massive-context", "multimodal", "reasoning"},
			BestFor:           []string{"large-codebase-analysis", "documentation", "research"},
		},
		"gemini": {
			Name:              "Google Gemini",
			Type:              "openai-compatible",
			BaseURL:           "https://generativelanguage.googleapis.com/v1beta/openai",
			DefaultModel:      "gemini-2.5-pro",
			MaxTokens:         65536,
			Temperature:       0.6,
			TopP:              0.95,
			FirstTokenTimeout: "45s",
			ContextWindow:     1000000,
			SupportsTools:     true,
			SupportsStreaming: true,
			SupportsVision:    true,
			Strengths:         []string{"massive-context", "multimodal", "reasoning"},
			BestFor:           []string{"large-codebase-analysis", "documentation", "research"},
		},
		
		// --- OpenAI ---
		"openai": {
			Name:              "OpenAI",
			Type:              "openai-compatible",
			BaseURL:           "https://api.openai.com/v1",
			DefaultModel:      "gpt-4.1",
			MaxTokens:         8192,
			Temperature:       0.7,
			TopP:              0.9,
			FirstTokenTimeout: "30s",
			ContextWindow:     1047576,
			SupportsTools:     true,
			SupportsStreaming: true,
			SupportsVision:    true,
			Strengths:         []string{"general-purpose", "tool-use", "ecosystem"},
			BestFor:           []string{"general-tasks", "writing", "analysis"},
		},
		
		// --- OpenAI Codex ---
		"codex-cli": {
			Name:              "OpenAI Codex CLI",
			Type:              "codex-cli",
			DefaultModel:      "o3",
			MaxTokens:         100000,
			Temperature:       0.2,
			TopP:              0.9,
			FirstTokenTimeout: "60s",
			ContextWindow:     200000,
			SupportsTools:     true,
			SupportsStreaming: true,
			SupportsVision:    false,
			Strengths:         []string{"code-editing", "autonomous-coding"},
			BestFor:           []string{"code-generation", "batch-editing", "refactoring"},
		},
		
		// --- Groq ---
		"groq": {
			Name:              "Groq",
			Type:              "openai-compatible",
			BaseURL:           "https://api.groq.com/openai/v1",
			DefaultModel:      "llama-3.3-70b-versatile",
			MaxTokens:         8192,
			Temperature:       0.7,
			TopP:              0.9,
			FirstTokenTimeout: "10s", // Groq is very fast
			ContextWindow:     131072,
			SupportsTools:     true,
			SupportsStreaming: true,
			SupportsVision:    false,
			Strengths:         []string{"speed", "low-latency", "cost-effective"},
			BestFor:           []string{"quick-tasks", "classification", "extraction"},
		},
		
		// --- Ollama (Local) ---
		"ollama": {
			Name:              "Ollama",
			Type:              "openai-compatible",
			BaseURL:           "http://localhost:11434/v1",
			DefaultModel:      "qwen2.5-coder:32b",
			MaxTokens:         4096,
			Temperature:       0.7,
			TopP:              0.9,
			FirstTokenTimeout: "60s",
			ContextWindow:     32768,
			SupportsTools:     true,
			SupportsStreaming: true,
			SupportsVision:    false,
			Strengths:         []string{"local", "privacy", "offline", "free"},
			BestFor:           []string{"local-development", "privacy-sensitive", "testing"},
		},
	}
}

// GetProviderProfile returns the profile for a given provider name.
// Returns nil if not found.
func GetProviderProfile(name string) *ProviderProfile {
	profiles := ProviderProfiles()
	if profile, ok := profiles[name]; ok {
		return &profile
	}
	return nil
}

// ApplyProfileToConfig applies a provider profile's optimized settings to a ProviderConfig.
// Only overrides fields that are non-zero in the profile.
func ApplyProfileToConfig(profile *ProviderProfile, cfg *config.ProviderConfig) {
	if profile == nil || cfg == nil {
		return
	}
	
	if profile.Type != "" {
		cfg.Type = profile.Type
	}
	if profile.BaseURL != "" {
		cfg.BaseURL = profile.BaseURL
	}
	if profile.DefaultModel != "" && cfg.Model == "" {
		cfg.Model = profile.DefaultModel
	}
	if profile.MaxTokens > 0 && cfg.MaxTokens == 0 {
		cfg.MaxTokens = profile.MaxTokens
	}
	if profile.FirstTokenTimeout != "" && cfg.FirstTokenTimeout == "" {
		cfg.FirstTokenTimeout = profile.FirstTokenTimeout
	}
}

// GetOptimizedSettings returns a map of all optimized settings for display.
func (p *ProviderProfile) GetOptimizedSettings() map[string]interface{} {
	return map[string]interface{}{
		"model":             p.DefaultModel,
		"maxTokens":         p.MaxTokens,
		"temperature":       p.Temperature,
		"topP":              p.TopP,
		"firstTokenTimeout": p.FirstTokenTimeout,
		"contextWindow":     p.ContextWindow,
	}
}
