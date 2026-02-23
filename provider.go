package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// --- Provider Interface ---

// Provider abstracts LLM execution backends.
type Provider interface {
	// Name returns the provider name (e.g. "claude", "openai").
	Name() string
	// Execute runs a prompt and returns the result.
	Execute(ctx context.Context, req ProviderRequest) (*ProviderResult, error)
}

// ToolCapableProvider extends Provider with tool execution support.
type ToolCapableProvider interface {
	Provider
	ExecuteWithTools(ctx context.Context, req ProviderRequest) (*ProviderResult, error)
}

// ProviderRequest contains all information needed to execute a task.
type ProviderRequest struct {
	Prompt       string
	SystemPrompt string
	Model        string
	Workdir      string
	Timeout      time.Duration
	Budget       float64

	// Claude-specific (ignored by API providers).
	PermissionMode string
	MCP            string
	MCPPath        string
	AddDirs        []string
	SessionID      string

	// Docker sandbox override (nil=use config default).
	Docker *bool

	// Tools for agentic loop (passed to provider).
	Tools []ToolDef

	// Optional event channel for SSE streaming.
	// When set, provider publishes output_chunk events as output is generated.
	EventCh chan<- SSEEvent `json:"-"`

	// Messages for multi-turn tool loop.
	Messages []Message `json:"messages,omitempty"`
}

// ProviderResult is the normalized output from any provider.
type ProviderResult struct {
	Output     string
	CostUSD    float64
	DurationMs int64
	SessionID  string
	IsError    bool
	Error      string
	Provider   string // name of the provider that actually handled the request
	// Observability metrics.
	TokensIn   int   `json:"tokensIn,omitempty"`   // input tokens consumed
	TokensOut  int   `json:"tokensOut,omitempty"`  // output tokens generated
	ProviderMs int64 `json:"providerMs,omitempty"` // provider-reported latency (vs wall-clock DurationMs)
	// Tool support.
	ToolCalls  []ToolCall `json:"toolCalls,omitempty"`
	StopReason string     `json:"stopReason,omitempty"` // "end_turn", "tool_use"
}

// Message represents a chat message for multi-turn conversations.
type Message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"` // string or []ContentBlock
}

// ContentBlock represents a piece of content (text or tool use/result).
type ContentBlock struct {
	Type      string          `json:"type"` // "text", "tool_use", "tool_result"
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

// --- Provider Registry ---

// providerRegistry holds initialized provider instances.
type providerRegistry struct {
	providers map[string]Provider
}

func newProviderRegistry() *providerRegistry {
	return &providerRegistry{
		providers: make(map[string]Provider),
	}
}

func (r *providerRegistry) register(name string, p Provider) {
	r.providers[name] = p
}

func (r *providerRegistry) get(name string) (Provider, error) {
	p, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("provider %q not configured", name)
	}
	return p, nil
}

// --- Provider Resolution ---

// resolveProviderName determines which provider to use for a task.
// Chain: task.Provider → role provider → config.DefaultProvider → "claude"
func resolveProviderName(cfg *Config, task Task, roleName string) string {
	if task.Provider != "" {
		return task.Provider
	}
	if roleName != "" {
		if rc, ok := cfg.Roles[roleName]; ok && rc.Provider != "" {
			return rc.Provider
		}
	}
	if cfg.DefaultProvider != "" {
		return cfg.DefaultProvider
	}
	return "claude"
}

// --- Provider Initialization ---

// initProviders creates provider instances from config.
func initProviders(cfg *Config) *providerRegistry {
	reg := newProviderRegistry()

	for name, pc := range cfg.Providers {
		switch pc.Type {
		case "claude-cli":
			path := pc.Path
			if path == "" {
				path = cfg.ClaudePath
			}
			if path == "" {
				path = "claude"
			}
			reg.register(name, &ClaudeProvider{binaryPath: path, cfg: cfg})

		case "openai-compatible":
			reg.register(name, &OpenAIProvider{
				name:         name,
				baseURL:      pc.BaseURL,
				apiKey:       pc.APIKey,
				defaultModel: pc.Model,
			})

		case "claude-api":
			apiKey := pc.APIKey
			if apiKey == "" {
				apiKey = os.Getenv("ANTHROPIC_API_KEY")
			}
			model := pc.Model
			if model == "" {
				model = "claude-sonnet-4-5-20250929"
			}
			maxTokens := pc.MaxTokens
			if maxTokens <= 0 {
				maxTokens = 8192
			}
			baseURL := pc.BaseURL
			if baseURL == "" {
				baseURL = "https://api.anthropic.com/v1"
			}
			reg.register(name, &ClaudeAPIProvider{
				name:      name,
				apiKey:    apiKey,
				model:     model,
				maxTokens: maxTokens,
				baseURL:   baseURL,
				cfg:       cfg,
			})
		}
	}

	// Ensure "claude" provider always exists (backward compat).
	if _, err := reg.get("claude"); err != nil {
		path := cfg.ClaudePath
		if path == "" {
			path = "claude"
		}
		reg.register("claude", &ClaudeProvider{binaryPath: path, cfg: cfg})
	}

	return reg
}

// --- Execute Helper ---

// buildProviderCandidates returns an ordered list of provider names to try.
// Order: primary → role fallbacks → config fallbacks (deduplicated).
func buildProviderCandidates(cfg *Config, task Task, roleName string) []string {
	primary := resolveProviderName(cfg, task, roleName)
	seen := map[string]bool{primary: true}
	candidates := []string{primary}

	// Role-level fallbacks.
	if roleName != "" {
		if rc, ok := cfg.Roles[roleName]; ok {
			for _, fb := range rc.FallbackProviders {
				if !seen[fb] {
					seen[fb] = true
					candidates = append(candidates, fb)
				}
			}
		}
	}

	// Config-level fallbacks.
	for _, fb := range cfg.FallbackProviders {
		if !seen[fb] {
			seen[fb] = true
			candidates = append(candidates, fb)
		}
	}

	return candidates
}

// isTransientError checks whether an error message indicates a transient failure
// that should count towards the circuit breaker threshold and trigger failover.
func isTransientError(errMsg string) bool {
	lower := strings.ToLower(errMsg)
	transient := []string{
		"timeout", "timed out", "deadline exceeded",
		"connection refused", "connection reset",
		"eof", "broken pipe",
		"http 5", "status 5", // 5xx server errors
		"temporarily unavailable", "service unavailable",
		"too many requests", "rate limit",
	}
	for _, t := range transient {
		if strings.Contains(lower, t) {
			return true
		}
	}
	return false
}

// buildProviderRequest constructs a ProviderRequest from task, config, and provider name.
func buildProviderRequest(cfg *Config, task Task, roleName, providerName string, eventCh chan<- SSEEvent) ProviderRequest {
	model := task.Model
	if model == "" {
		if pc, ok := cfg.Providers[providerName]; ok && pc.Model != "" {
			model = pc.Model
		}
	}

	timeout, parseErr := time.ParseDuration(task.Timeout)
	if parseErr != nil {
		timeout = 15 * time.Minute
	}

	var docker *bool
	if task.Docker != nil {
		docker = task.Docker
	} else if roleName != "" {
		if rc, ok := cfg.Roles[roleName]; ok && rc.Docker != nil {
			docker = rc.Docker
		}
	}

	// Build system prompt with dynamic skill injection.
	systemPrompt := task.SystemPrompt
	if len(cfg.Skills) > 0 {
		skillsPrompt := buildSkillsPrompt(cfg, task)
		if skillsPrompt != "" {
			systemPrompt = systemPrompt + skillsPrompt
		}
	}

	req := ProviderRequest{
		Prompt:         task.Prompt,
		SystemPrompt:   systemPrompt,
		Model:          model,
		Workdir:        task.Workdir,
		Timeout:        timeout,
		Budget:         task.Budget,
		PermissionMode: task.PermissionMode,
		MCP:            task.MCP,
		AddDirs:        task.AddDirs,
		SessionID:      task.SessionID,
		Docker:         docker,
		EventCh:        eventCh,
	}

	if task.MCP != "" {
		if mcpPath, ok := cfg.mcpPaths[task.MCP]; ok {
			req.MCPPath = mcpPath
		}
	}

	return req
}

// executeWithProvider runs a task through the resolved provider with circuit breaker
// and failover support. It tries providers in order: primary → role fallbacks → config fallbacks.
// eventCh is optional — when non-nil, the provider will stream output chunks.
func executeWithProvider(ctx context.Context, cfg *Config, task Task, roleName string, registry *providerRegistry, eventCh chan<- SSEEvent) *ProviderResult {
	candidates := buildProviderCandidates(cfg, task, roleName)

	var lastErr string
	for i, providerName := range candidates {
		// Circuit breaker check.
		if cfg.circuits != nil {
			cb := cfg.circuits.get(providerName)
			if !cb.Allow() {
				logDebugCtx(ctx, "circuit open, skipping provider", "provider", providerName)
				if i == 0 && len(candidates) > 1 {
					// Publish failover event for primary provider.
					publishFailoverEvent(eventCh, task.ID, providerName, candidates[i+1], "circuit open")
				}
				continue
			}
		}

		p, err := registry.get(providerName)
		if err != nil {
			logDebugCtx(ctx, "provider not registered", "provider", providerName)
			continue
		}

		req := buildProviderRequest(cfg, task, roleName, providerName, eventCh)
		result, execErr := p.Execute(ctx, req)

		// Determine if this is a failure.
		errMsg := ""
		if execErr != nil {
			errMsg = execErr.Error()
		} else if result != nil && result.IsError {
			errMsg = result.Error
		}

		if errMsg != "" {
			if isTransientError(errMsg) {
				// Transient error: record failure, try next provider.
				if cfg.circuits != nil {
					cfg.circuits.get(providerName).RecordFailure()
				}
				logWarnCtx(ctx, "provider transient error", "provider", providerName, "error", errMsg)
				lastErr = fmt.Sprintf("provider %s: %s", providerName, errMsg)

				if i < len(candidates)-1 {
					next := candidates[i+1]
					publishFailoverEvent(eventCh, task.ID, providerName, next, errMsg)
					logInfoCtx(ctx, "failing over to next provider", "from", providerName, "to", next)
					continue
				}
			} else {
				// Non-transient error: don't count against circuit, return immediately.
				logWarnCtx(ctx, "provider non-transient error", "provider", providerName, "error", errMsg)
				if result == nil {
					result = &ProviderResult{IsError: true, Error: fmt.Sprintf("provider %s: %s", providerName, errMsg)}
				}
				result.Provider = providerName
				return result
			}
		}

		if errMsg == "" {
			// Success.
			if cfg.circuits != nil {
				cfg.circuits.get(providerName).RecordSuccess()
			}
			if result == nil {
				result = &ProviderResult{}
			}
			result.Provider = providerName
			return result
		}
	}

	// All candidates failed or circuits are open.
	errMsg := "all providers unavailable"
	if lastErr != "" {
		errMsg = lastErr
	}
	return &ProviderResult{
		IsError: true,
		Error:   errMsg,
	}
}

// publishFailoverEvent sends a provider_failover SSE event if eventCh is available.
func publishFailoverEvent(eventCh chan<- SSEEvent, taskID, from, to, reason string) {
	if eventCh == nil {
		return
	}
	eventCh <- SSEEvent{
		Type:   "provider_failover",
		TaskID: taskID,
		Data: map[string]any{
			"from":   from,
			"to":     to,
			"reason": reason,
		},
	}
}
