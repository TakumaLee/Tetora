package main

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	"tetora/internal/provider"
)

// --- Type Aliases (backward compatibility) ---
// These allow existing root-level code to continue using old type names
// without adding provider package imports.

type ProviderRequest = provider.Request
type ProviderResult = provider.Result
type Provider = provider.Provider
type ToolCapableProvider = provider.ToolCapableProvider
type Message = provider.Message
type ContentBlock = provider.ContentBlock

// providerRegistry is an alias for the provider.Registry type.
type providerRegistry = provider.Registry

// --- Function Aliases ---

var (
	errResult                = provider.ErrResult
	providerHasNativeSession = provider.HasNativeSession
	isTransientError         = provider.IsTransientError
	buildClaudeArgs          = provider.BuildClaudeArgs
	buildCodexArgs           = provider.BuildCodexArgs
	claudeSessionFileExists  = provider.ClaudeSessionFileExists
)

// Exported function aliases for test files.
var (
	ParseClaudeOutput       = provider.ParseClaudeOutput
	ParseCodexOutput        = provider.ParseCodexOutput
	ParseOpenAIResponse     = provider.ParseOpenAIResponse
	ConvertToOpenAIMessages = provider.ConvertToOpenAIMessages
	MapOpenAIFinishReason   = provider.MapOpenAIFinishReason
	EstimateOpenAICost      = provider.EstimateOpenAICost
)

// Type aliases for provider implementations.
type ClaudeProvider = provider.ClaudeProvider
type CodexProvider = provider.CodexProvider
type OpenAIProvider = provider.OpenAIProvider
type TerminalProvider = provider.TerminalProvider
type CodexQuota = provider.CodexQuota

// Function aliases for codex quota.
var (
	fetchCodexQuota        = provider.FetchCodexQuota
	parseCodexStatusOutput = provider.ParseCodexStatusOutput
)

// truncateBytes is an alias for provider.TruncateBytes.
var truncateBytes = func(b []byte, maxLen int) string { return provider.TruncateBytes(b, maxLen) }

// parseClaudeOutput wraps provider.ParseClaudeOutput for backward compatibility with tests.
// Tests expect a TaskResult with Status/Error fields rather than *provider.Result.
func parseClaudeOutput(stdout, stderr []byte, exitCode int) TaskResult {
	pr := provider.ParseClaudeOutput(stdout, stderr, exitCode)
	r := TaskResult{
		Output:     pr.Output,
		CostUSD:    pr.CostUSD,
		SessionID:  pr.SessionID,
		ProviderMs: pr.ProviderMs,
		TokensIn:   pr.TokensIn,
		TokensOut:  pr.TokensOut,
	}
	if pr.IsError {
		r.Status = "error"
		r.Error = pr.Error
	} else {
		r.Status = "success"
	}
	return r
}

// --- Provider Registry Helpers ---

func newProviderRegistry() *provider.Registry {
	return provider.NewRegistry()
}

// --- initProviders creates provider instances from config ---
// Stays in root because it depends on Config and root-level Docker/Tmux adapters.

func initProviders(cfg *Config) *provider.Registry {
	reg := provider.NewRegistry()

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
			reg.Register(name, &provider.ClaudeProvider{
				BinaryPath:    path,
				DockerEnabled: cfg.Docker.Enabled,
				Docker:        newDockerRunner(cfg.Docker),
			})

		case "openai-compatible":
			reg.Register(name, &provider.OpenAIProvider{
				Name_:        name,
				BaseURL:      pc.BaseURL,
				APIKey:       pc.APIKey,
				DefaultModel: pc.Model,
			})

		case "claude-api":
			logWarn("provider type 'claude-api' is deprecated in v3, use 'claude-code' instead", "name", name)
			path := pc.Path
			if path == "" {
				path = "/usr/local/bin/claude"
			}
			reg.Register(name, &provider.ClaudeProvider{
				BinaryPath:    path,
				DockerEnabled: cfg.Docker.Enabled,
				Docker:        newDockerRunner(cfg.Docker),
			})

		case "claude-code", "claude-tmux":
			if pc.Type == "claude-tmux" {
				logWarn("provider type 'claude-tmux' is deprecated in v3, use 'claude-code' instead", "name", name)
			}
			path := pc.Path
			if path == "" {
				path = "/usr/local/bin/claude"
			}
			reg.Register(name, &provider.ClaudeProvider{
				BinaryPath:    path,
				DockerEnabled: cfg.Docker.Enabled,
				Docker:        newDockerRunner(cfg.Docker),
			})

		case "terminal-claude":
			path := pc.Path
			if path == "" {
				path = cfg.ClaudePath
			}
			if path == "" {
				path = "/usr/local/bin/claude"
			}
			reg.Register(name, &provider.TerminalProvider{
				BinaryPath:     path,
				DefaultWorkdir: cfg.DefaultWorkdir,
				Profile:        newProfileAdapter(&claudeTmuxProfile{}),
				Tmux:           tmuxOpsAdapter{},
				Workers:        newWorkerTrackerAdapter(newTmuxSupervisor()),
			})

		case "terminal-codex":
			path := pc.Path
			if path == "" {
				path = "codex"
			}
			reg.Register(name, &provider.TerminalProvider{
				BinaryPath:     path,
				DefaultWorkdir: cfg.DefaultWorkdir,
				Profile:        newProfileAdapter(&codexTmuxProfile{}),
				Tmux:           tmuxOpsAdapter{},
				Workers:        newWorkerTrackerAdapter(newTmuxSupervisor()),
			})

		case "codex-cli":
			path := pc.Path
			if path == "" {
				path = "codex"
			}
			reg.Register(name, &provider.CodexProvider{BinaryPath: path})
		}
	}

	// Ensure "claude" provider always exists (backward compat).
	if _, err := reg.Get("claude"); err != nil {
		path := cfg.ClaudePath
		if path == "" {
			path = "claude"
		}
		reg.Register("claude", &provider.ClaudeProvider{
			BinaryPath:    path,
			DockerEnabled: cfg.Docker.Enabled,
			Docker:        newDockerRunner(cfg.Docker),
		})
	}

	// Ensure "claude-code" provider always exists (headless default).
	if _, err := reg.Get("claude-code"); err != nil {
		path := cfg.ClaudePath
		if path == "" {
			path = "/usr/local/bin/claude"
		}
		reg.Register("claude-code", &provider.ClaudeProvider{
			BinaryPath:    path,
			DockerEnabled: cfg.Docker.Enabled,
			Docker:        newDockerRunner(cfg.Docker),
		})
	}

	// Auto-register "codex" if binary found on PATH.
	if _, err := reg.Get("codex"); err != nil {
		if path, lookErr := exec.LookPath("codex"); lookErr == nil {
			reg.Register("codex", &provider.CodexProvider{BinaryPath: path})
		}
	}

	return reg
}

// --- buildProviderRequest ---
// Stays in root because it depends on Config, Task, and SSEEvent.

func resolveProviderName(cfg *Config, task Task, agentName string) string {
	if task.Provider != "" {
		return task.Provider
	}
	if agentName != "" {
		if rc, ok := cfg.Agents[agentName]; ok && rc.Provider != "" {
			return rc.Provider
		}
	}
	if cfg.DefaultProvider != "" {
		return cfg.DefaultProvider
	}
	return "claude"
}

func buildProviderCandidates(cfg *Config, task Task, agentName string) []string {
	primary := resolveProviderName(cfg, task, agentName)
	seen := map[string]bool{primary: true}
	candidates := []string{primary}

	if agentName != "" {
		if rc, ok := cfg.Agents[agentName]; ok {
			for _, fb := range rc.FallbackProviders {
				if !seen[fb] {
					seen[fb] = true
					candidates = append(candidates, fb)
				}
			}
		}
	}

	for _, fb := range cfg.FallbackProviders {
		if !seen[fb] {
			seen[fb] = true
			candidates = append(candidates, fb)
		}
	}

	return candidates
}

// buildProviderRequest constructs a provider.Request from task, config, and provider name.
// The eventCh is bridged into the provider.Request.OnEvent callback.
func buildProviderRequest(cfg *Config, task Task, agentName, providerName string, eventCh chan<- SSEEvent) provider.Request {
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
	} else if agentName != "" {
		if rc, ok := cfg.Agents[agentName]; ok && rc.Docker != nil {
			docker = rc.Docker
		}
	}

	// Build OnEvent callback that bridges provider.Event → SSEEvent.
	var onEvent func(provider.Event)
	if eventCh != nil {
		onEvent = func(ev provider.Event) {
			select {
			case eventCh <- SSEEvent{
				Type:      ev.Type,
				TaskID:    ev.TaskID,
				SessionID: ev.SessionID,
				Data:      ev.Data,
				Timestamp: ev.Timestamp,
			}:
			default:
			}
		}
	}

	req := provider.Request{
		Prompt:         task.Prompt,
		SystemPrompt:   task.SystemPrompt,
		Model:          model,
		Workdir:        task.Workdir,
		Timeout:        timeout,
		Budget:         task.Budget,
		PermissionMode: task.PermissionMode,
		MCP:            task.MCP,
		AddDirs:        task.AddDirs,
		SessionID:      task.SessionID,
		Resume:         task.Resume,
		PersistSession: task.PersistSession,
		Docker:         docker,
		OnEvent:        onEvent,
		AgentName:      agentName,
	}

	if task.MCP != "" {
		if mcpPath, ok := cfg.mcpPaths[task.MCP]; ok {
			req.MCPPath = mcpPath
		}
	}

	return req
}

// --- executeWithProvider ---
// Stays in root because it depends on Config.circuits (circuit breaker).

func executeWithProvider(ctx context.Context, cfg *Config, task Task, agentName string, registry *provider.Registry, eventCh chan<- SSEEvent) *provider.Result {
	candidates := buildProviderCandidates(cfg, task, agentName)

	var lastErr string
	for i, providerName := range candidates {
		if cfg.circuits != nil {
			cb := cfg.circuits.Get(providerName)
			if !cb.Allow() {
				logDebugCtx(ctx, "circuit open, skipping provider", "provider", providerName)
				if i == 0 && len(candidates) > 1 {
					publishFailoverEvent(eventCh, task.ID, providerName, candidates[i+1], "circuit open")
				}
				continue
			}
		}

		p, err := registry.Get(providerName)
		if err != nil {
			logDebugCtx(ctx, "provider not registered", "provider", providerName)
			continue
		}

		req := buildProviderRequest(cfg, task, agentName, providerName, eventCh)
		result, execErr := p.Execute(ctx, req)

		errMsg := ""
		if execErr != nil {
			errMsg = execErr.Error()
		} else if result != nil && result.IsError {
			errMsg = result.Error
		}

		if errMsg != "" {
			if provider.IsTransientError(errMsg) {
				if cfg.circuits != nil {
					cfg.circuits.Get(providerName).RecordFailure()
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
				logWarnCtx(ctx, "provider non-transient error", "provider", providerName, "error", errMsg)
				if result == nil {
					result = &provider.Result{IsError: true, Error: fmt.Sprintf("provider %s: %s", providerName, errMsg)}
				}
				result.Provider = providerName
				return result
			}
		}

		if errMsg == "" {
			if cfg.circuits != nil {
				cfg.circuits.Get(providerName).RecordSuccess()
			}
			if result == nil {
				result = &provider.Result{}
			}
			result.Provider = providerName
			return result
		}
	}

	errMsg := "all providers unavailable"
	if lastErr != "" {
		errMsg = lastErr
	}
	return &provider.Result{
		IsError: true,
		Error:   errMsg,
	}
}

// publishFailoverEvent sends a provider_failover SSE event if eventCh is available.
// The send is non-blocking to avoid blocking executeWithProvider on a full channel.
func publishFailoverEvent(eventCh chan<- SSEEvent, taskID, from, to, reason string) {
	if eventCh == nil {
		return
	}
	select {
	case eventCh <- SSEEvent{
		Type:   "provider_failover",
		TaskID: taskID,
		Data: map[string]any{
			"from":   from,
			"to":     to,
			"reason": reason,
		},
	}:
	default:
	}
}

// --- Docker Runner Adapter ---

// dockerRunnerAdapter wraps root-level Docker functions to implement provider.DockerRunner.
type dockerRunnerAdapter struct {
	cfg DockerConfig
}

// newDockerRunner returns a provider.DockerRunner backed by root-level Docker helpers,
// or nil if Docker is not enabled.
func newDockerRunner(cfg DockerConfig) provider.DockerRunner {
	if !cfg.Enabled {
		return nil
	}
	return &dockerRunnerAdapter{cfg: cfg}
}

func (d *dockerRunnerAdapter) BuildCmd(ctx context.Context, binaryPath, workdir string, args, addDirs []string, mcpPath string) *exec.Cmd {
	dockerArgs := rewriteDockerArgs(args, addDirs, mcpPath)
	envVars := dockerEnvFilter(d.cfg)
	return buildDockerCmd(ctx, d.cfg, workdir, binaryPath, dockerArgs, addDirs, mcpPath, envVars)
}

// --- Tmux Ops Adapter ---

// tmuxOpsAdapter wraps root-level tmux functions to implement provider.TmuxOps.
type tmuxOpsAdapter struct{}

func (t tmuxOpsAdapter) Create(session string, cols, rows int, command, workdir string) error {
	return tmuxCreate(session, cols, rows, command, workdir)
}

// Kill calls tmuxKill and silently discards the error, satisfying provider.TmuxOps
// which declares Kill with no return value.
func (t tmuxOpsAdapter) Kill(session string) {
	_ = tmuxKill(session)
}

func (t tmuxOpsAdapter) Capture(session string) (string, error) {
	return tmuxCapture(session)
}

func (t tmuxOpsAdapter) HasSession(session string) bool {
	return tmuxHasSession(session)
}

func (t tmuxOpsAdapter) LoadAndPaste(session, text string) error {
	return tmuxLoadAndPaste(session, text)
}

func (t tmuxOpsAdapter) SendText(session, text string) error {
	return tmuxSendText(session, text)
}

func (t tmuxOpsAdapter) SendKeys(session string, keys ...string) error {
	return tmuxSendKeys(session, keys...)
}

func (t tmuxOpsAdapter) CaptureHistory(session string) (string, error) {
	return tmuxCaptureHistory(session)
}

// --- Worker Tracker Adapter ---

// workerTrackerAdapter wraps *tmuxSupervisor to implement provider.WorkerTracker.
type workerTrackerAdapter struct {
	sup *tmuxSupervisor
}

func newWorkerTrackerAdapter(sup *tmuxSupervisor) provider.WorkerTracker {
	return &workerTrackerAdapter{sup: sup}
}

func (w *workerTrackerAdapter) Register(sessionName string, info provider.WorkerInfo) {
	worker := &tmuxWorker{
		TmuxName:    info.TmuxName,
		TaskID:      info.TaskID,
		Agent:       info.Agent,
		Prompt:      info.Prompt,
		Workdir:     info.Workdir,
		State:       tmuxScreenState(info.State),
		CreatedAt:   info.CreatedAt,
		LastChanged: info.LastChanged,
	}
	w.sup.register(sessionName, worker)
}

func (w *workerTrackerAdapter) Unregister(sessionName string) {
	w.sup.unregister(sessionName)
}

func (w *workerTrackerAdapter) UpdateWorker(sessionName string, state provider.ScreenState, capture string, changed bool) {
	worker := w.sup.getWorker(sessionName)
	if worker == nil {
		return
	}
	w.sup.mu.Lock()
	worker.State = tmuxScreenState(state)
	worker.LastCapture = capture
	if changed {
		worker.LastChanged = time.Now()
	}
	w.sup.mu.Unlock()
}

// --- Tmux Profile Adapter ---

// profileAdapter wraps a root-level tmuxCLIProfile to implement provider.TmuxProfile.
// This bridges the type mismatch between tmuxScreenState and provider.ScreenState.
type profileAdapter struct {
	inner tmuxCLIProfile
}

func newProfileAdapter(p tmuxCLIProfile) provider.TmuxProfile {
	return &profileAdapter{inner: p}
}

func (a *profileAdapter) Name() string { return a.inner.Name() }

func (a *profileAdapter) BuildCommand(binaryPath string, req provider.Request) string {
	return a.inner.BuildCommand(binaryPath, req)
}

func (a *profileAdapter) DetectState(capture string) provider.ScreenState {
	return provider.ScreenState(a.inner.DetectState(capture))
}

func (a *profileAdapter) ApproveKeys() []string { return a.inner.ApproveKeys() }
func (a *profileAdapter) RejectKeys() []string  { return a.inner.RejectKeys() }
