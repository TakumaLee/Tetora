package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// --- P27.3: Channel Notifier Interface ---

// ChannelNotifier sends typing indicators and status updates to a messaging channel.
type ChannelNotifier interface {
	SendTyping(ctx context.Context) error
	SendStatus(ctx context.Context, msg string) error
}

// --- Task Types ---

type Task struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Prompt         string   `json:"prompt"`
	Workdir        string   `json:"workdir"`
	Model          string   `json:"model"`
	Provider       string   `json:"provider,omitempty"`
	Docker         *bool    `json:"docker,omitempty"` // per-task Docker sandbox override
	Timeout        string   `json:"timeout"`
	Budget         float64  `json:"budget"`
	PermissionMode string   `json:"permissionMode"`
	MCP            string   `json:"mcp"`
	AddDirs        []string `json:"addDirs"`
	SystemPrompt   string   `json:"systemPrompt"`
	SessionID      string   `json:"sessionId"`
	Agent           string   `json:"agent,omitempty"`    // role name for smart dispatch
	Source         string   `json:"source,omitempty"`  // "dispatch", "cron", "ask", "route:*"
	TraceID        string   `json:"traceId,omitempty"` // trace ID for request correlation
	Depth          int      `json:"depth,omitempty"`    // --- P13.3: Nested Sub-Agents --- nesting depth (0 = top-level)
	ParentID       string   `json:"parentId,omitempty"` // --- P13.3: Nested Sub-Agents --- parent task ID
	channelNotifier ChannelNotifier                     // P27.3: unexported, not serialized
	approvalGate    ApprovalGate                        // P28.0: unexported, not serialized
	sseBroker       *sseBroker                          // streaming: unexported, not serialized
}

type TaskResult struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Status     string  `json:"status"`
	ExitCode   int     `json:"exitCode"`
	Output     string  `json:"output"`
	Error      string  `json:"error,omitempty"`
	DurationMs int64   `json:"durationMs"`
	CostUSD    float64 `json:"costUsd"`
	Model      string  `json:"model"`
	SessionID  string  `json:"sessionId"`
	OutputFile string  `json:"outputFile,omitempty"`
	// Observability metrics.
	TokensIn   int    `json:"tokensIn,omitempty"`
	TokensOut  int    `json:"tokensOut,omitempty"`
	ProviderMs int64  `json:"providerMs,omitempty"`
	TraceID    string `json:"traceId,omitempty"`
	Provider   string `json:"provider,omitempty"`
	TrustLevel string `json:"trustLevel,omitempty"`
	Agent       string `json:"agent,omitempty"`
}

type DispatchResult struct {
	StartedAt  time.Time    `json:"startedAt"`
	FinishedAt time.Time    `json:"finishedAt"`
	DurationMs int64        `json:"durationMs"`
	TotalCost  float64      `json:"totalCostUsd"`
	Tasks      []TaskResult `json:"tasks"`
	Summary    string       `json:"summary"`
}

// --- Failed Task Storage (for retry/reroute) ---

// failedTask stores a failed task's original parameters for later retry or reroute.
type failedTask struct {
	task     Task
	failedAt time.Time
	errorMsg string
}

const failedTaskTTL = 30 * time.Minute

// --- Dispatch State ---

type dispatchState struct {
	mu          sync.Mutex
	running     map[string]*taskState
	finished    []TaskResult
	failedTasks map[string]*failedTask // task ID -> original task (for retry/reroute)
	startAt     time.Time
	active      bool
	draining    bool             // graceful shutdown: stop accepting new tasks
	cancel      context.CancelFunc
	broker      *sseBroker       // SSE event broker for streaming progress
	sandboxMgr        *SandboxManager              // --- P13.2: Sandbox Plugin ---
	discordBot        *DiscordBot                  // --- P14.1: Discord Components v2 ---
	discordActivities map[string]*discordActivity  // task ID -> active Discord task
}

// discordActivity tracks a Discord-initiated task for dashboard visibility.
type discordActivity struct {
	TaskID    string    `json:"taskId"`
	Agent      string    `json:"agent"`
	Phase     string    `json:"phase"`     // "routing", "processing", "replying"
	Author    string    `json:"author"`
	ChannelID string    `json:"channelId"`
	StartAt   time.Time `json:"startedAt"`
	Prompt    string    `json:"prompt"`
}

type taskState struct {
	task     Task
	startAt  time.Time
	cmd      *exec.Cmd
	cancelFn context.CancelFunc
}

func newDispatchState() *dispatchState {
	return &dispatchState{
		running:           make(map[string]*taskState),
		failedTasks:       make(map[string]*failedTask),
		discordActivities: make(map[string]*discordActivity),
	}
}

// setDiscordActivity registers a new Discord-initiated task for dashboard tracking.
func (s *dispatchState) setDiscordActivity(taskID string, da *discordActivity) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.discordActivities[taskID] = da
}

// updateDiscordPhase updates the phase of an active Discord task.
func (s *dispatchState) updateDiscordPhase(taskID, phase string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if da, ok := s.discordActivities[taskID]; ok {
		da.Phase = phase
	}
}

// removeDiscordActivity removes a completed Discord task from tracking.
func (s *dispatchState) removeDiscordActivity(taskID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.discordActivities, taskID)
}

// publishSSE publishes an SSE event to the task, session, and global dashboard channels.
func (s *dispatchState) publishSSE(event SSEEvent) {
	if s.broker == nil {
		return
	}
	keys := []string{SSEDashboardKey}
	if event.TaskID != "" {
		keys = append(keys, event.TaskID)
	}
	if event.SessionID != "" {
		keys = append(keys, event.SessionID)
	}
	s.broker.PublishMulti(keys, event)
}

// publishToSSEBroker publishes an SSE event directly via a broker reference.
// Used by runSingleTask which has no access to dispatchState.
func publishToSSEBroker(broker *sseBroker, event SSEEvent) {
	if broker == nil {
		return
	}
	keys := []string{SSEDashboardKey}
	if event.TaskID != "" {
		keys = append(keys, event.TaskID)
	}
	if event.SessionID != "" {
		keys = append(keys, event.SessionID)
	}
	broker.PublishMulti(keys, event)
}

func (s *dispatchState) statusJSON() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()

	type taskStatus struct {
		ID       string  `json:"id"`
		Name     string  `json:"name"`
		Status   string  `json:"status"`
		Elapsed  string  `json:"elapsed,omitempty"`
		Duration string  `json:"duration,omitempty"`
		CostUSD  float64 `json:"costUsd,omitempty"`
		Model    string  `json:"model,omitempty"`
		Timeout  string  `json:"timeout,omitempty"`
		Prompt   string  `json:"prompt,omitempty"`
		PID      int     `json:"pid,omitempty"`
		Source   string  `json:"source,omitempty"`
		Agent     string  `json:"agent,omitempty"`
		ParentID string  `json:"parentId,omitempty"`
		Depth    int     `json:"depth,omitempty"`
	}

	status := "idle"
	if s.active {
		status = "dispatching"
	} else if len(s.discordActivities) > 0 {
		status = "processing"
	} else if len(s.finished) > 0 {
		status = "done"
	}

	var tasks []taskStatus
	for _, ts := range s.running {
		prompt := ts.task.Prompt
		if len(prompt) > 100 {
			prompt = prompt[:100] + "..."
		}
		pid := 0
		if ts.cmd != nil && ts.cmd.Process != nil {
			pid = ts.cmd.Process.Pid
		}
		tasks = append(tasks, taskStatus{
			ID:       ts.task.ID,
			Name:     ts.task.Name,
			Status:   "running",
			Elapsed:  time.Since(ts.startAt).Round(time.Second).String(),
			Model:    ts.task.Model,
			Timeout:  ts.task.Timeout,
			Prompt:   prompt,
			PID:      pid,
			Source:   ts.task.Source,
			Agent:     ts.task.Agent,
			ParentID: ts.task.ParentID,
			Depth:    ts.task.Depth,
		})
	}
	for _, r := range s.finished {
		tasks = append(tasks, taskStatus{
			ID:       r.ID,
			Name:     r.Name,
			Status:   r.Status,
			Duration: (time.Duration(r.DurationMs) * time.Millisecond).Round(time.Second).String(),
			CostUSD:  r.CostUSD,
			Model:    r.Model,
			Agent:     r.Agent,
		})
	}

	// Discord activities.
	type discordActivityStatus struct {
		TaskID    string `json:"taskId"`
		Agent      string `json:"agent"`
		Phase     string `json:"phase"`
		Author    string `json:"author"`
		ChannelID string `json:"channelId"`
		Elapsed   string `json:"elapsed"`
		Prompt    string `json:"prompt"`
	}
	var discord []discordActivityStatus
	for _, da := range s.discordActivities {
		prompt := da.Prompt
		if len(prompt) > 100 {
			prompt = prompt[:100] + "..."
		}
		discord = append(discord, discordActivityStatus{
			TaskID:    da.TaskID,
			Agent:      da.Agent,
			Phase:     da.Phase,
			Author:    da.Author,
			ChannelID: da.ChannelID,
			Elapsed:   time.Since(da.StartAt).Round(time.Second).String(),
			Prompt:    prompt,
		})
	}

	out := map[string]any{
		"status":    status,
		"running":   len(s.running),
		"completed": len(s.finished),
		"tasks":     tasks,
		"discord":   discord,
	}
	if s.active {
		out["elapsed"] = time.Since(s.startAt).Round(time.Second).String()
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	return b
}

// --- UUID ---

func newUUID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// --- Task Defaults ---

// estimateTimeout infers an appropriate task timeout from the prompt content.
// This prevents long-running development tasks from being killed prematurely.
// The caller can always override by setting Task.Timeout explicitly.
func estimateTimeout(prompt string) string {
	p := strings.ToLower(prompt)

	// Heaviest tasks — large-scale refactors, migrations, multi-phase builds.
	heavyKeywords := []string{
		"refactor", "migrate", "migration", "全部", "整個", "整合", "架構",
		"rewrite", "overhaul", "all ", "entire", "全面",
	}
	for _, kw := range heavyKeywords {
		if strings.Contains(p, kw) {
			return "3h"
		}
	}

	// Medium-heavy — implementation, multi-file changes, feature builds.
	buildKeywords := []string{
		"implement", "build", "create", "add ", "新增", "建立", "實作",
		"feature", "功能", "develop", "設計", "規劃",
	}
	for _, kw := range buildKeywords {
		if strings.Contains(p, kw) {
			return "1h"
		}
	}

	// Light fixes — targeted bug fixes and updates.
	fixKeywords := []string{
		"fix", "bug", "修復", "update", "更新", "debug", "patch", "調整",
	}
	for _, kw := range fixKeywords {
		if strings.Contains(p, kw) {
			return "30m"
		}
	}

	// Read-only / query tasks.
	queryKeywords := []string{
		"check", "查", "show", "list", "search", "analyze", "分析", "查看",
	}
	for _, kw := range queryKeywords {
		if strings.Contains(p, kw) {
			return "15m"
		}
	}

	// Default: 1h is safe for most tasks.
	return "1h"
}

func fillDefaults(cfg *Config, t *Task) {
	if t.ID == "" {
		t.ID = newUUID()
	}
	if t.SessionID == "" {
		t.SessionID = newUUID()
	}
	if t.Model == "" {
		t.Model = cfg.DefaultModel
	}
	if t.Timeout == "" {
		// Use smart estimation from prompt; fall back to config default.
		if t.Prompt != "" {
			t.Timeout = estimateTimeout(t.Prompt)
		} else {
			t.Timeout = cfg.DefaultTimeout
		}
	}
	if t.Budget == 0 {
		t.Budget = cfg.DefaultBudget
	}
	if t.PermissionMode == "" {
		t.PermissionMode = cfg.DefaultPermissionMode
	}
	if t.Workdir == "" {
		t.Workdir = cfg.DefaultWorkdir
	}
	// Expand ~ in workdir.
	if strings.HasPrefix(t.Workdir, "~/") {
		home, _ := os.UserHomeDir()
		t.Workdir = filepath.Join(home, t.Workdir[2:])
	}
	if t.Name == "" {
		t.Name = fmt.Sprintf("task-%s", t.ID[:8])
	}
	// Sanitize prompt.
	if t.Prompt != "" {
		t.Prompt = sanitizePrompt(t.Prompt, cfg.MaxPromptLen)
	}
	// Resolve agent from system-wide default (not SmartDispatch — that's handled by the routing engine).
	if t.Agent == "" && cfg.DefaultAgent != "" {
		t.Agent = cfg.DefaultAgent
	}
	// Apply agent-specific overrides.
	applyAgentDefaults(cfg, t)
}

// applyAgentDefaults applies agent-specific model and permission overrides to a task,
// but only if the task still has the global defaults (i.e. not explicitly set).
func applyAgentDefaults(cfg *Config, t *Task) {
	if t.Agent == "" {
		return
	}
	rc, ok := cfg.Agents[t.Agent]
	if !ok {
		return
	}
	if rc.Model != "" && t.Model == cfg.DefaultModel {
		t.Model = rc.Model
	}
	if rc.PermissionMode != "" && t.PermissionMode == cfg.DefaultPermissionMode {
		t.PermissionMode = rc.PermissionMode
	}
}

// --- Prompt Sanitization ---

// ansiEscapeRe matches ANSI escape sequences.
var ansiEscapeRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// sanitizePrompt removes potentially dangerous content from prompt text.
// This performs structural sanitization only (null bytes, ANSI escapes, length).
// Content filtering is the LLM's responsibility.
func sanitizePrompt(input string, maxLen int) string {
	if maxLen <= 0 {
		maxLen = 102400
	}

	// Strip null bytes.
	result := strings.ReplaceAll(input, "\x00", "")

	// Strip ANSI escape sequences.
	result = ansiEscapeRe.ReplaceAllString(result, "")

	// Enforce max length.
	if len(result) > maxLen {
		result = result[:maxLen]
		logWarn("prompt truncated", "from", len(input), "to", maxLen)
	}

	if result != input && len(result) == len(input) {
		logWarn("prompt sanitized, removed control characters")
	}

	return result
}

// --- P21.2: Writing Style ---

// loadWritingStyle resolves writing style guidelines from config.
func loadWritingStyle(cfg *Config) string {
	if cfg.WritingStyle.FilePath != "" {
		data, err := os.ReadFile(cfg.WritingStyle.FilePath)
		if err == nil {
			return strings.TrimSpace(string(data))
		}
		logWarn("failed to load writing style file", "path", cfg.WritingStyle.FilePath, "error", err)
	}
	return cfg.WritingStyle.Guidelines
}

// --- Directory Validation ---

// validateDirs checks that the task's workdir and addDirs are within allowed directories.
// If allowedDirs is empty, no restriction is applied (backward compatible).
// Agent-level allowedDirs takes precedence over config-level.
func validateDirs(cfg *Config, task Task, agentName string) error {
	// Determine which allowedDirs to use.
	var allowed []string
	if agentName != "" {
		if rc, ok := cfg.Agents[agentName]; ok && len(rc.AllowedDirs) > 0 {
			allowed = rc.AllowedDirs
		}
	}
	if len(allowed) == 0 {
		allowed = cfg.AllowedDirs
	}
	if len(allowed) == 0 {
		return nil // no restriction
	}

	// Normalize allowed dirs.
	normalized := make([]string, 0, len(allowed))
	for _, d := range allowed {
		if strings.HasPrefix(d, "~/") {
			home, _ := os.UserHomeDir()
			d = filepath.Join(home, d[2:])
		}
		abs, err := filepath.Abs(d)
		if err != nil {
			continue
		}
		normalized = append(normalized, abs+string(filepath.Separator))
	}

	check := func(dir, label string) error {
		if dir == "" {
			return nil
		}
		abs, err := filepath.Abs(dir)
		if err != nil {
			return fmt.Errorf("%s: cannot resolve path %q: %w", label, dir, err)
		}
		absWithSep := abs + string(filepath.Separator)
		for _, a := range normalized {
			if strings.HasPrefix(absWithSep, a) || abs == strings.TrimSuffix(a, string(filepath.Separator)) {
				return nil
			}
		}
		return fmt.Errorf("%s %q is not within allowedDirs", label, dir)
	}

	if err := check(task.Workdir, "workdir"); err != nil {
		return err
	}
	for _, d := range task.AddDirs {
		if err := check(d, "addDir"); err != nil {
			return err
		}
	}
	return nil
}

// --- Output Storage ---

// saveTaskOutput saves the raw claude output to a file in the outputs directory.
// Returns the filename (not full path) for storage in the history DB.
func saveTaskOutput(baseDir string, jobID string, stdout []byte) string {
	if len(stdout) == 0 || baseDir == "" {
		return ""
	}
	outputDir := filepath.Join(baseDir, "outputs")
	os.MkdirAll(outputDir, 0o755)

	ts := time.Now().Format("20060102-150405")
	// Use first 8 chars of jobID for readability.
	shortID := jobID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	filename := fmt.Sprintf("%s_%s.json", shortID, ts)
	filePath := filepath.Join(outputDir, filename)

	if err := os.WriteFile(filePath, stdout, 0o644); err != nil {
		logWarn("save output failed", "error", err)
		return ""
	}
	return filename
}

// cleanupOutputs removes output files older than the given number of days.
func cleanupOutputs(baseDir string, days int) {
	outputDir := filepath.Join(baseDir, "outputs")
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -days)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			os.Remove(filepath.Join(outputDir, e.Name()))
		}
	}
}

// --- Dispatch Core ---

func dispatch(ctx context.Context, cfg *Config, tasks []Task, state *dispatchState, sem chan struct{}) *DispatchResult {
	ctx, cancel := context.WithCancel(ctx)
	state.mu.Lock()
	state.active = true
	state.startAt = time.Now()
	state.cancel = cancel
	state.finished = nil
	state.running = make(map[string]*taskState)
	state.mu.Unlock()

	defer func() {
		cancel()
		state.mu.Lock()
		state.active = false
		state.cancel = nil
		state.mu.Unlock()
	}()

	var wg sync.WaitGroup
	results := make(chan TaskResult, len(tasks))

	for _, task := range tasks {
		wg.Add(1)
		go func(t Task) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			r := runTask(ctx, cfg, t, state)
			results <- r
		}(task)
	}

	wg.Wait()
	close(results)

	dr := &DispatchResult{
		StartedAt:  state.startAt,
		FinishedAt: time.Now(),
	}
	for r := range results {
		dr.Tasks = append(dr.Tasks, r)
		dr.TotalCost += r.CostUSD
	}
	dr.DurationMs = dr.FinishedAt.Sub(dr.StartedAt).Milliseconds()
	dr.Summary = buildSummary(dr)
	return dr
}

// runSingleTask runs one task using the shared semaphore. Used by cron engine.
func runSingleTask(ctx context.Context, cfg *Config, task Task, sem chan struct{}, agentName string) TaskResult {
	// Apply trust level.
	applyTrustToTask(cfg, &task, agentName)

	// --- P16.3: Prompt Injection Defense v2 --- Apply before execution.
	if err := applyInjectionDefense(ctx, cfg, &task); err != nil {
		return TaskResult{
			ID: task.ID, Name: task.Name, Status: "error",
			Error: fmt.Sprintf("injection defense: %v", err), Model: task.Model, SessionID: task.SessionID,
		}
	}

	// Classify request complexity and build tiered system prompt.
	complexity := classifyComplexity(task.Prompt, task.Source)
	if task.Source != "route-classify" {
		buildTieredPrompt(cfg, &task, agentName, complexity)
	} else {
		// For routing classification, only set up workspace dir and baseDir.
		if agentName != "" {
			ws := resolveWorkspace(cfg, agentName)
			if ws.Dir != "" {
				task.Workdir = ws.Dir
			}
			task.AddDirs = append(task.AddDirs, cfg.baseDir)
		}
	}

	// Validate directories before running.
	if err := validateDirs(cfg, task, agentName); err != nil {
		return TaskResult{
			ID: task.ID, Name: task.Name, Status: "error",
			Error: err.Error(), Model: task.Model, SessionID: task.SessionID,
		}
	}

	sem <- struct{}{}
	defer func() { <-sem }()

	// Budget check before execution.
	if budgetResult := checkBudget(cfg, agentName, "", 0); budgetResult != nil && !budgetResult.Allowed {
		logWarnCtx(ctx, "budget check failed", "taskId", task.ID[:8], "reason", budgetResult.Message)
		return TaskResult{
			ID: task.ID, Name: task.Name, Status: "error",
			Error: "budget_exceeded: " + budgetResult.Message, Model: task.Model, SessionID: task.SessionID,
		}
	} else if budgetResult != nil && budgetResult.DowngradeModel != "" {
		logInfoCtx(ctx, "auto-downgrade model", "taskId", task.ID[:8],
			"from", task.Model, "to", budgetResult.DowngradeModel,
			"utilization", fmt.Sprintf("%.0f%%", budgetResult.Utilization*100))
		task.Model = budgetResult.DowngradeModel
	}

	providerName := resolveProviderName(cfg, task, agentName)

	logDebugCtx(ctx, "task start",
		"source", task.Source, "taskId", task.ID[:8], "name", task.Name,
		"model", task.Model, "provider", providerName,
		"agent", agentName, "workdir", task.Workdir)

	timeout, err := time.ParseDuration(task.Timeout)
	if err != nil {
		// Estimate from prompt rather than hard-coding 15m.
		estimated, _ := time.ParseDuration(estimateTimeout(task.Prompt))
		if estimated <= 0 {
			estimated = time.Hour
		}
		timeout = estimated
	}
	taskCtx, taskCancel := context.WithTimeout(ctx, timeout)
	defer taskCancel()

	// SSE streaming: publish started event and create eventCh when sseBroker is set.
	var eventCh chan SSEEvent
	if task.sseBroker != nil {
		publishToSSEBroker(task.sseBroker, SSEEvent{
			Type:      SSEStarted,
			TaskID:    task.ID,
			SessionID: task.SessionID,
			Data: map[string]any{
				"name":  task.Name,
				"role":  agentName,
				"model": task.Model,
			},
		})
		eventCh = make(chan SSEEvent, 128)
		go func() {
			for ev := range eventCh {
				publishToSSEBroker(task.sseBroker, ev)
			}
		}()
	}

	start := time.Now()
	pr := executeWithProvider(taskCtx, cfg, task, agentName, cfg.registry, eventCh)
	if eventCh != nil {
		close(eventCh)
	}
	elapsed := time.Since(start)

	result := TaskResult{
		ID:         task.ID,
		Name:       task.Name,
		Output:     pr.Output,
		CostUSD:    pr.CostUSD,
		DurationMs: elapsed.Milliseconds(),
		Model:      task.Model,
		SessionID:  pr.SessionID,
		TokensIn:   pr.TokensIn,
		TokensOut:  pr.TokensOut,
		ProviderMs: pr.ProviderMs,
		Provider:   pr.Provider,
		Agent:       agentName,
	}
	if result.SessionID == "" {
		result.SessionID = task.SessionID
	}

	if taskCtx.Err() == context.DeadlineExceeded {
		result.Status = "timeout"
		result.Error = fmt.Sprintf("timed out after %v", timeout)
	} else if ctx.Err() != nil {
		result.Status = "cancelled"
		result.Error = "cancelled"
	} else if pr.IsError {
		result.Status = "error"
		result.Error = pr.Error
	} else {
		result.Status = "success"
	}

	// Offline queue: if all providers are unavailable, enqueue for later retry.
	if result.Status == "error" && isAllProvidersUnavailable(result.Error) && cfg.OfflineQueue.Enabled {
		if !isQueueFull(cfg.HistoryDB, cfg.OfflineQueue.maxItemsOrDefault()) {
			if err := enqueueTask(cfg.HistoryDB, task, agentName, 0); err == nil {
				result.Status = "queued"
				logInfoCtx(ctx, "task queued for offline retry",
					"taskId", task.ID[:8], "name", task.Name)
			}
		}
	}

	logDebugCtx(ctx, "task done",
		"taskId", task.ID[:8], "name", task.Name,
		"elapsed", elapsed.Round(time.Millisecond),
		"cost", result.CostUSD,
		"tokensIn", result.TokensIn, "tokensOut", result.TokensOut,
		"provider", result.Provider,
		"status", result.Status)

	// Record token telemetry (async).
	go recordTokenTelemetry(cfg.HistoryDB, TokenTelemetryEntry{
		TaskID:             task.ID,
		Agent:               agentName,
		Complexity:         complexity.String(),
		Provider:           pr.Provider,
		Model:              task.Model,
		SystemPromptTokens: len(task.SystemPrompt) / 4,
		ContextTokens:      len(task.Prompt) / 4,
		ToolDefsTokens:     0,
		InputTokens:        pr.TokensIn,
		OutputTokens:       pr.TokensOut,
		CostUSD:            pr.CostUSD,
		DurationMs:         elapsed.Milliseconds(),
		Source:             task.Source,
		CreatedAt:          time.Now().Format(time.RFC3339),
	})

	// Save output to file.
	if pr.Output != "" {
		result.OutputFile = saveTaskOutput(cfg.baseDir, task.ID, []byte(pr.Output))
	}

	// SSE streaming: publish completed/error event.
	if task.sseBroker != nil && result.Status != "queued" {
		evType := SSECompleted
		if result.Status != "success" {
			evType = SSEError
		}
		publishToSSEBroker(task.sseBroker, SSEEvent{
			Type:      evType,
			TaskID:    task.ID,
			SessionID: task.SessionID,
			Data: map[string]any{
				"status":     result.Status,
				"durationMs": result.DurationMs,
				"costUsd":    result.CostUSD,
				"tokensIn":   result.TokensIn,
				"tokensOut":  result.TokensOut,
				"error":      result.Error,
			},
		})
	}

	// Note: history recording for runSingleTask is handled by the caller (cron.go).

	return result
}

func runTask(ctx context.Context, cfg *Config, task Task, state *dispatchState) TaskResult {
	// Propagate trace ID from context to task.
	if task.TraceID == "" {
		task.TraceID = traceIDFromContext(ctx)
	}

	agentName := task.Agent

	// --- P19.5: Unified Presence/Typing Indicators --- Start typing in source channel.
	if globalPresence != nil && task.Source != "" {
		globalPresence.StartTyping(ctx, task.Source)
		defer globalPresence.StopTyping(task.Source)
	}

	// --- P16.3: Prompt Injection Defense v2 --- Apply before execution.
	if err := applyInjectionDefense(ctx, cfg, &task); err != nil {
		return TaskResult{
			ID: task.ID, Name: task.Name, Status: "error",
			Error: fmt.Sprintf("injection defense: %v", err), Model: task.Model, SessionID: task.SessionID,
		}
	}

	// Classify request complexity and build tiered system prompt.
	complexity := classifyComplexity(task.Prompt, task.Source)
	buildTieredPrompt(cfg, &task, agentName, complexity)

	// Apply trust level (may override permissionMode for observe mode).
	trustLevel, _ := applyTrustToTask(cfg, &task, agentName)
	if trustLevel == TrustObserve {
		logDebugCtx(ctx, "trust: observe mode, forcing plan permission", "agent", agentName)
	}

	// Validate directories before running.
	if err := validateDirs(cfg, task, agentName); err != nil {
		return TaskResult{
			ID: task.ID, Name: task.Name, Status: "error",
			Error: err.Error(), Model: task.Model, SessionID: task.SessionID,
		}
	}

	// --- P13.2: Sandbox Plugin --- Check sandbox policy for this agent.
	useSandbox, sandboxErr := shouldUseSandbox(cfg, agentName, state.sandboxMgr)
	if sandboxErr != nil {
		return TaskResult{
			ID: task.ID, Name: task.Name, Status: "error",
			Error: sandboxErr.Error(), Model: task.Model, SessionID: task.SessionID,
		}
	}
	var sandboxID string
	if useSandbox && state.sandboxMgr != nil {
		image := sandboxImageForAgent(cfg, agentName)
		sbID, err := state.sandboxMgr.EnsureSandboxWithImage(task.SessionID, task.Workdir, image)
		if err != nil {
			logWarnCtx(ctx, "sandbox creation failed", "taskId", task.ID[:8], "error", err)
			// If policy is "required", this is fatal; if "optional", fall through.
			if sandboxPolicyForAgent(cfg, agentName) == "required" {
				return TaskResult{
					ID: task.ID, Name: task.Name, Status: "error",
					Error: fmt.Sprintf("sandbox required but creation failed: %v", err),
					Model: task.Model, SessionID: task.SessionID,
				}
			}
		} else {
			sandboxID = sbID
			logDebugCtx(ctx, "sandbox active for task", "taskId", task.ID[:8], "sandboxId", sandboxID)
		}
	}

	timeout, err := time.ParseDuration(task.Timeout)
	if err != nil {
		// Estimate from prompt rather than hard-coding 15m.
		estimated, _ := time.ParseDuration(estimateTimeout(task.Prompt))
		if estimated <= 0 {
			estimated = time.Hour
		}
		timeout = estimated
	}
	taskCtx, taskCancel := context.WithTimeout(ctx, timeout)
	defer taskCancel()

	ts := &taskState{task: task, startAt: time.Now(), cancelFn: taskCancel}
	state.mu.Lock()
	state.running[task.ID] = ts
	state.mu.Unlock()

	// Budget check before execution.
	if budgetResult := checkBudget(cfg, agentName, "", 0); budgetResult != nil && !budgetResult.Allowed {
		logWarnCtx(ctx, "budget check failed", "taskId", task.ID[:8], "reason", budgetResult.Message)
		return TaskResult{
			ID: task.ID, Name: task.Name, Status: "error",
			Error: "budget_exceeded: " + budgetResult.Message, Model: task.Model, SessionID: task.SessionID,
		}
	} else if budgetResult != nil && budgetResult.DowngradeModel != "" {
		logInfoCtx(ctx, "auto-downgrade model", "taskId", task.ID[:8],
			"from", task.Model, "to", budgetResult.DowngradeModel,
			"utilization", fmt.Sprintf("%.0f%%", budgetResult.Utilization*100))
		task.Model = budgetResult.DowngradeModel
	}

	providerName := resolveProviderName(cfg, task, agentName)

	logDebugCtx(ctx, "task start",
		"taskId", task.ID[:8], "name", task.Name,
		"model", task.Model, "provider", providerName,
		"role", agentName, "workdir", task.Workdir)

	// Discord thread-per-task notification (top-level tasks only).
	doDiscordNotify := task.Depth == 0 && state.discordBot != nil && state.discordBot.notifier != nil
	if doDiscordNotify {
		state.discordBot.notifier.NotifyStart(task)
	}

	// Publish SSE started event.
	state.publishSSE(SSEEvent{
		Type:      SSEStarted,
		TaskID:    task.ID,
		SessionID: task.SessionID,
		Data: map[string]any{
			"name":  task.Name,
			"role":  agentName,
			"model": task.Model,
		},
	})

	// Create event channel for provider streaming.
	var eventCh chan SSEEvent
	if state.broker != nil {
		hasSub := state.broker.HasSubscribers(task.ID) ||
			state.broker.HasSubscribers(task.SessionID) ||
			state.broker.HasSubscribers(SSEDashboardKey)
		if hasSub {
			eventCh = make(chan SSEEvent, 128)
			go func() {
				for ev := range eventCh {
					state.publishSSE(ev)
				}
			}()
		}
	}

	// Reuse complexity from tiered prompt builder for tool trimming.
	start := time.Now()
	var pr *ProviderResult
	if complexity == ComplexitySimple {
		// Simple requests skip the tool engine entirely.
		pr = executeWithProvider(taskCtx, cfg, task, agentName, cfg.registry, eventCh)
	} else {
		pr = executeWithProviderAndTools(taskCtx, cfg, task, agentName, cfg.registry, eventCh, state.broker)
	}
	if eventCh != nil {
		close(eventCh)
	}
	elapsed := time.Since(start)

	result := TaskResult{
		ID:         task.ID,
		Name:       task.Name,
		Output:     pr.Output,
		CostUSD:    pr.CostUSD,
		DurationMs: elapsed.Milliseconds(),
		Model:      task.Model,
		SessionID:  pr.SessionID,
		TokensIn:   pr.TokensIn,
		TokensOut:  pr.TokensOut,
		ProviderMs: pr.ProviderMs,
		Provider:   pr.Provider,
		Agent:       agentName,
	}
	if result.SessionID == "" {
		result.SessionID = task.SessionID
	}

	if taskCtx.Err() == context.DeadlineExceeded {
		result.Status = "timeout"
		result.Error = fmt.Sprintf("timed out after %v", timeout)
	} else if ctx.Err() != nil {
		result.Status = "cancelled"
		result.Error = "dispatch cancelled"
	} else if pr.IsError {
		result.Status = "error"
		result.Error = pr.Error
	} else {
		result.Status = "success"
	}

	// Offline queue: if all providers are unavailable, enqueue for later retry.
	if result.Status == "error" && isAllProvidersUnavailable(result.Error) && cfg.OfflineQueue.Enabled {
		if !isQueueFull(cfg.HistoryDB, cfg.OfflineQueue.maxItemsOrDefault()) {
			if err := enqueueTask(cfg.HistoryDB, task, agentName, 0); err == nil {
				result.Status = "queued"
				logInfoCtx(ctx, "task queued for offline retry",
					"taskId", task.ID[:8], "name", task.Name)

				// Publish SSE queued event.
				state.publishSSE(SSEEvent{
					Type:      SSEQueued,
					TaskID:    task.ID,
					SessionID: task.SessionID,
					Data: map[string]any{
						"name":  task.Name,
						"role":  agentName,
						"error": result.Error,
					},
				})
			} else {
				logWarnCtx(ctx, "failed to enqueue task", "taskId", task.ID[:8], "error", err)
			}
		} else {
			logWarnCtx(ctx, "offline queue full, task not enqueued", "taskId", task.ID[:8])
		}
	}

	state.mu.Lock()
	delete(state.running, task.ID)
	state.finished = append(state.finished, result)
	// Store failed tasks for retry/reroute.
	if result.Status != "success" && result.Status != "queued" {
		state.failedTasks[task.ID] = &failedTask{
			task:     task,
			failedAt: time.Now(),
			errorMsg: result.Error,
		}
	}
	state.mu.Unlock()

	logDebugCtx(ctx, "task done",
		"taskId", task.ID[:8], "name", task.Name,
		"elapsed", elapsed.Round(time.Millisecond),
		"cost", result.CostUSD,
		"tokensIn", result.TokensIn, "tokensOut", result.TokensOut,
		"status", result.Status)

	// Record token telemetry (async).
	go recordTokenTelemetry(cfg.HistoryDB, TokenTelemetryEntry{
		TaskID:             task.ID,
		Agent:               agentName,
		Complexity:         complexity.String(),
		Provider:           pr.Provider,
		Model:              task.Model,
		SystemPromptTokens: len(task.SystemPrompt) / 4,
		ContextTokens:      len(task.Prompt) / 4,
		ToolDefsTokens:     0,
		InputTokens:        pr.TokensIn,
		OutputTokens:       pr.TokensOut,
		CostUSD:            pr.CostUSD,
		DurationMs:         elapsed.Milliseconds(),
		Source:             task.Source,
		CreatedAt:          time.Now().Format(time.RFC3339),
	})

	// Save output to file.
	if pr.Output != "" {
		result.OutputFile = saveTaskOutput(cfg.baseDir, task.ID, []byte(pr.Output))
	}

	// Record to history DB.
	recordHistory(cfg.HistoryDB, task.ID, task.Name, task.Source, task.Agent, task, result,
		start.Format(time.RFC3339), time.Now().Format(time.RFC3339), result.OutputFile)

	// Record session activity (skip for chat source — handled by HTTP handler).
	if !strings.HasPrefix(task.Source, "chat") {
		recordSessionActivity(cfg.HistoryDB, task, result, task.Agent)
		logSystemDispatch(cfg.HistoryDB, task, result, task.Agent)
	}

	// Publish SSE completed/error/queued event.
	if result.Status != "queued" {
		evType := SSECompleted
		if result.Status != "success" {
			evType = SSEError
		}
		state.publishSSE(SSEEvent{
			Type:      evType,
			TaskID:    task.ID,
			SessionID: task.SessionID,
			Data: map[string]any{
				"status":     result.Status,
				"durationMs": result.DurationMs,
				"costUsd":    result.CostUSD,
				"tokensIn":   result.TokensIn,
				"tokensOut":  result.TokensOut,
				"error":      result.Error,
			},
		})
	}

	// Webhook notifications.
	sendWebhooks(cfg, result.Status, WebhookPayload{
		JobID:    task.ID,
		Name:     task.Name,
		Source:   task.Source,
		Status:   result.Status,
		Cost:     result.CostUSD,
		Duration: result.DurationMs,
		Model:    result.Model,
		Output:   truncate(result.Output, 500),
		Error:    truncate(result.Error, 300),
	})

	// Set trust level on result.
	result.TrustLevel = trustLevel

	// Async reflection — self-assessment after task completion.
	if shouldReflect(cfg, task, result) {
		go func() {
			ref, err := performReflection(ctx, cfg, task, result)
			if err != nil {
				logDebugCtx(ctx, "reflection failed", "taskId", task.ID[:8], "error", err)
				return
			}
			if err := storeReflection(cfg.HistoryDB, ref); err != nil {
				logDebugCtx(ctx, "reflection store failed", "taskId", task.ID[:8], "error", err)
			} else {
				logDebugCtx(ctx, "reflection stored", "taskId", task.ID[:8], "role", ref.Agent, "score", ref.Score)
			}
		}()
	}

	// --- P13.2: Sandbox Plugin --- Cleanup sandbox after task completion.
	if sandboxID != "" && state.sandboxMgr != nil {
		if err := state.sandboxMgr.DestroySandbox(sandboxID); err != nil {
			logWarnCtx(ctx, "sandbox cleanup failed", "sandboxId", sandboxID, "error", err)
		}
	}

	// Check trust promotion after successful task.
	if result.Status == "success" && agentName != "" {
		if promoMsg := checkTrustPromotion(ctx, cfg, agentName); promoMsg != "" {
			// Publish SSE event for dashboard.
			if state.broker != nil {
				state.broker.Publish("trust", SSEEvent{
					Type: "trust_promotion",
					Data: map[string]any{
						"role":    agentName,
						"message": promoMsg,
					},
				})
			}
		}
	}

	// Discord thread-per-task: post result to thread.
	if doDiscordNotify {
		state.discordBot.notifier.NotifyComplete(task.ID, result)
	}

	return result
}

// --- Retry / Reroute ---

// retryTask re-runs a previously failed task with the same parameters.
// A new task ID is generated but all other parameters are preserved.
func retryTask(ctx context.Context, cfg *Config, taskID string, state *dispatchState, sem chan struct{}) (*TaskResult, error) {
	state.mu.Lock()
	ft, ok := state.failedTasks[taskID]
	state.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("task %s not found in failed tasks", taskID)
	}

	// Clone task with new ID but same parameters.
	task := ft.task
	task.ID = newUUID()
	task.SessionID = newUUID()
	task.Source = "retry:" + task.Source
	fillDefaults(cfg, &task)

	result := runSingleTask(ctx, cfg, task, sem, task.Agent)

	// Record to history.
	start := time.Now().Add(-time.Duration(result.DurationMs) * time.Millisecond)
	recordHistory(cfg.HistoryDB, task.ID, task.Name, task.Source, task.Agent, task, result,
		start.Format(time.RFC3339), time.Now().Format(time.RFC3339), result.OutputFile)

	// Record session activity.
	recordSessionActivity(cfg.HistoryDB, task, result, task.Agent)
	logSystemDispatch(cfg.HistoryDB, task, result, task.Agent)

	// If retry succeeded, remove from failed tasks.
	if result.Status == "success" {
		state.mu.Lock()
		delete(state.failedTasks, taskID)
		state.mu.Unlock()
	} else {
		// Store the new failure (and keep old one for reference).
		state.mu.Lock()
		state.failedTasks[task.ID] = &failedTask{
			task:     task,
			failedAt: time.Now(),
			errorMsg: result.Error,
		}
		state.mu.Unlock()
	}

	auditLog(cfg.HistoryDB, "task.retry", task.Source,
		fmt.Sprintf("original=%s new=%s status=%s", taskID, task.ID, result.Status), "")

	return &result, nil
}

// rerouteTask re-dispatches a previously failed task through smart dispatch,
// allowing a different agent to handle it.
func rerouteTask(ctx context.Context, cfg *Config, taskID string, state *dispatchState, sem chan struct{}) (*SmartDispatchResult, error) {
	state.mu.Lock()
	ft, ok := state.failedTasks[taskID]
	state.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("task %s not found in failed tasks", taskID)
	}

	if !cfg.SmartDispatch.Enabled {
		return nil, fmt.Errorf("smart dispatch is not enabled")
	}

	result := smartDispatch(ctx, cfg, ft.task.Prompt, "reroute", state, sem)

	// If reroute succeeded, remove from failed tasks.
	if result.Task.Status == "success" {
		state.mu.Lock()
		delete(state.failedTasks, taskID)
		state.mu.Unlock()
	}

	auditLog(cfg.HistoryDB, "task.reroute", "reroute",
		fmt.Sprintf("original=%s role=%s status=%s", taskID, result.Route.Agent, result.Task.Status), "")

	return result, nil
}

// failedTaskInfo is a JSON-serializable summary of a failed task.
type failedTaskInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Prompt   string `json:"prompt,omitempty"`
	Agent     string `json:"agent,omitempty"`
	Source   string `json:"source,omitempty"`
	Error    string `json:"error"`
	FailedAt string `json:"failedAt"`
}

// listFailedTasks returns a list of failed tasks available for retry/reroute.
func listFailedTasks(state *dispatchState) []failedTaskInfo {
	state.mu.Lock()
	defer state.mu.Unlock()

	var tasks []failedTaskInfo
	for id, ft := range state.failedTasks {
		prompt := ft.task.Prompt
		if len(prompt) > 100 {
			prompt = prompt[:100] + "..."
		}
		tasks = append(tasks, failedTaskInfo{
			ID:       id,
			Name:     ft.task.Name,
			Prompt:   prompt,
			Agent:     ft.task.Agent,
			Source:   ft.task.Source,
			Error:    ft.errorMsg,
			FailedAt: ft.failedAt.Format(time.RFC3339),
		})
	}
	return tasks
}

// cleanupFailedTasks removes expired entries from the failed tasks map.
func cleanupFailedTasks(state *dispatchState) {
	state.mu.Lock()
	defer state.mu.Unlock()
	now := time.Now()
	for id, ft := range state.failedTasks {
		if now.Sub(ft.failedAt) > failedTaskTTL {
			delete(state.failedTasks, id)
		}
	}
}

func buildSummary(dr *DispatchResult) string {
	ok := 0
	for _, t := range dr.Tasks {
		if t.Status == "success" {
			ok++
		}
	}
	dur := time.Duration(dr.DurationMs) * time.Millisecond
	return fmt.Sprintf("%d/%d tasks succeeded ($%.2f, %s)",
		ok, len(dr.Tasks), dr.TotalCost, dur.Round(time.Second))
}

// safeToolExec wraps tool execution with panic recovery.
func safeToolExec(ctx context.Context, cfg *Config, tool *ToolDef, input json.RawMessage) (output string, err error) {
	defer func() {
		if rv := recover(); rv != nil {
			err = fmt.Errorf("tool %q panicked: %v", tool.Name, rv)
			logError("tool panic recovered", "tool", tool.Name, "panic", fmt.Sprintf("%v", rv))
		}
	}()
	return tool.Handler(ctx, cfg, input)
}

// --- Agentic Loop ---

// truncateToolOutput truncates tool output to the given limit.
// If limit <= 0, defaults to 10240 chars.
func truncateToolOutput(output string, limit int) string {
	if limit <= 0 {
		limit = 10240
	}
	if len(output) <= limit {
		return output
	}
	return output[:limit] + fmt.Sprintf("\n[truncated: first %d of %d chars]", limit, len(output))
}

// executeWithProviderAndTools runs a task with tool support via agentic loop.
// If the provider supports tools and the tool registry has tools, it will:
// 1. Call provider with tools
// 2. Check for tool_use in response
// 3. Execute tools via ToolRegistry
// 4. Inject tool results back as messages
// 5. Call provider again
// 6. Repeat until no more tool_use or max iterations
func executeWithProviderAndTools(ctx context.Context, cfg *Config, task Task, agentName string, registry *providerRegistry, eventCh chan<- SSEEvent, broker *sseBroker) *ProviderResult {
	// Check if tool engine is enabled and we have a tool registry.
	if cfg.toolRegistry == nil {
		return executeWithProvider(ctx, cfg, task, agentName, registry, eventCh)
	}

	// Resolve provider.
	providerName := resolveProviderName(cfg, task, agentName)
	p, err := registry.get(providerName)
	if err != nil {
		return &ProviderResult{IsError: true, Error: err.Error()}
	}

	// Check if provider supports tools.
	toolProvider, supportsTools := p.(ToolCapableProvider)
	if !supportsTools {
		// Fallback to regular execution.
		return executeWithProvider(ctx, cfg, task, agentName, registry, eventCh)
	}

	// Get available tools (filtered by agent policy and complexity).
	var allowed map[string]bool
	if task.Agent != "" {
		allowed = resolveAllowedTools(cfg, task.Agent)
	}
	// Apply complexity-based tool filtering.
	complexity := classifyComplexity(task.Prompt, task.Source)
	complexityProfile := ToolsForComplexity(complexity)
	if complexityProfile != "full" && complexityProfile != "none" {
		profileAllowed := ToolsForProfile(complexityProfile)
		if profileAllowed != nil {
			if allowed == nil {
				allowed = profileAllowed
			} else {
				// Intersection: only keep tools in both sets.
				for name := range allowed {
					if !profileAllowed[name] {
						delete(allowed, name)
					}
				}
			}
		}
	}
	tools := cfg.toolRegistry.ListFiltered(allowed)
	if len(tools) == 0 {
		// No tools available, use regular execution.
		return executeWithProvider(ctx, cfg, task, agentName, registry, eventCh)
	}

	// Build initial request.
	req := buildProviderRequest(cfg, task, agentName, providerName, eventCh)
	// Convert []*ToolDef to []ToolDef for the provider request.
	toolDefs := make([]ToolDef, len(tools))
	for i, t := range tools {
		toolDefs[i] = *t
	}
	req.Tools = toolDefs

	// Initialize enhanced loop detector.
	detector := NewLoopDetector()

	// Max iterations.
	maxIter := cfg.Tools.MaxIterations
	if maxIter <= 0 {
		maxIter = 10
	}

	var messages []Message
	var finalResult *ProviderResult

	// Token/cost accumulators across iterations.
	var totalTokensIn, totalTokensOut int
	var totalCostUSD float64
	var totalProviderMs int64
	var taskBudgetWarnLogged bool // soft-limit: log once and continue instead of stopping

	for i := 0; i < maxIter; i++ {
		req.Messages = messages

		// P27.3: Send typing indicator at iteration start.
		if cfg.StreamToChannels && task.channelNotifier != nil {
			go task.channelNotifier.SendTyping(ctx)
		}

		// Call provider.
		result, execErr := toolProvider.ExecuteWithTools(ctx, req)
		if execErr != nil {
			return &ProviderResult{IsError: true, Error: execErr.Error()}
		}
		if result.IsError {
			return result
		}

		// Accumulate metrics.
		totalTokensIn += result.TokensIn
		totalTokensOut += result.TokensOut
		totalCostUSD += result.CostUSD
		totalProviderMs += result.ProviderMs

		// Check stop reason.
		if result.StopReason != "tool_use" || len(result.ToolCalls) == 0 {
			// No more tool calls, we're done.
			finalResult = result
			break
		}

		// Publish SSE event for tool calls.
		if broker != nil {
			for _, tc := range result.ToolCalls {
				broker.PublishMulti([]string{task.ID, task.SessionID, SSEDashboardKey}, SSEEvent{
					Type:      "tool_call",
					TaskID:    task.ID,
					SessionID: task.SessionID,
					Data: map[string]any{
						"id":   tc.ID,
						"name": tc.Name,
					},
				})
			}
		}

		// Execute tools.
		toolResults := make([]ToolResult, 0, len(result.ToolCalls))
		for _, tc := range result.ToolCalls {
			// Check tool policy - is tool allowed for this agent?
			if task.Agent != "" && !isToolAllowed(cfg, task.Agent, tc.Name) {
				logWarnCtx(ctx, "tool call blocked by policy", "tool", tc.Name, "agent", task.Agent)
				toolResults = append(toolResults, ToolResult{
					ToolUseID: tc.ID,
					Content:   fmt.Sprintf("error: tool %q not allowed by policy for agent %q", tc.Name, task.Agent),
					IsError:   true,
				})
				continue
			}

			// Check for loop using enhanced detector.
			isLoop, loopMsg := detector.Check(tc.Name, tc.Input)
			if isLoop {
				logWarnCtx(ctx, "tool loop detected", "tool", tc.Name, "msg", loopMsg)
				toolResults = append(toolResults, ToolResult{
					ToolUseID: tc.ID,
					Content:   loopMsg,
					IsError:   true,
				})
				continue
			}

			// Check for repeating pattern.
			if i > 2 { // Only check after a few iterations.
				if hasPattern, patternMsg := detector.detectToolLoopPattern(); hasPattern {
					logWarnCtx(ctx, "tool pattern detected", "msg", patternMsg)
					toolResults = append(toolResults, ToolResult{
						ToolUseID: tc.ID,
						Content:   patternMsg,
						IsError:   true,
					})
					continue
				}
			}

			// Record tool call for loop detection.
			detector.Record(tc.Name, tc.Input)

			// Apply trust-level filtering.
			if mockResult, shouldExec := filterToolCall(cfg, task.Agent, tc); !shouldExec {
				// Tool call filtered by trust level (observe or suggest mode).
				toolResults = append(toolResults, *mockResult)
				continue
			}

			// P28.0: Pre-execution approval gate.
			if needsApproval(cfg, tc.Name) && task.approvalGate != nil && !task.approvalGate.IsAutoApproved(tc.Name) {
				approved, gateErr := requestToolApproval(ctx, cfg, task, tc)
				if gateErr != nil || !approved {
					toolResults = append(toolResults, ToolResult{
						ToolUseID: tc.ID,
						Content:   fmt.Sprintf("[REJECTED: tool %s requires approval — %s]", tc.Name, gateReason(gateErr, approved)),
						IsError:   true,
					})
					continue
				}
			}

			// Get tool handler.
			tool, ok := cfg.toolRegistry.Get(tc.Name)
			if !ok {
				toolResults = append(toolResults, ToolResult{
					ToolUseID: tc.ID,
					Content:   fmt.Sprintf("error: tool %q not found", tc.Name),
					IsError:   true,
				})
				continue
			}

			// Execute tool (with panic recovery + per-tool timeout).
			toolTimeout := time.Duration(cfg.Tools.ToolTimeout) * time.Second
			if toolTimeout <= 0 {
				toolTimeout = 30 * time.Second
			}
			toolCtx, toolCancel := context.WithTimeout(ctx, toolTimeout)
			toolStart := time.Now()
			output, err := safeToolExec(toolCtx, cfg, tool, tc.Input)
			toolCancel()
			toolDuration := time.Since(toolStart)
			if toolCtx.Err() == context.DeadlineExceeded && err == nil {
				err = fmt.Errorf("tool %q timed out after %v", tc.Name, toolTimeout)
			}

			tr := ToolResult{ToolUseID: tc.ID}
			if err != nil {
				tr.Content = fmt.Sprintf("error: %v", err)
				tr.IsError = true
			} else {
				tr.Content = truncateToolOutput(output, cfg.Tools.ToolOutputLimit)
			}
			toolResults = append(toolResults, tr)

			// P27.3: Send tool status to channel.
			if cfg.StreamToChannels && task.channelNotifier != nil {
				statusMsg := fmt.Sprintf("%s: done (%dms)", tc.Name, toolDuration.Milliseconds())
				go task.channelNotifier.SendStatus(ctx, statusMsg)
			}

			// Publish SSE event for tool result.
			if broker != nil {
				broker.PublishMulti([]string{task.ID, task.SessionID, SSEDashboardKey}, SSEEvent{
					Type:      "tool_result",
					TaskID:    task.ID,
					SessionID: task.SessionID,
					Data: map[string]any{
						"id":       tc.ID,
						"name":     tc.Name,
						"duration": toolDuration.Milliseconds(),
						"isError":  tr.IsError,
					},
				})
			}
		}

		// Build assistant message with tool uses.
		var assistantContent []ContentBlock
		if result.Output != "" {
			assistantContent = append(assistantContent, ContentBlock{
				Type: "text",
				Text: result.Output,
			})
		}
		for _, tc := range result.ToolCalls {
			assistantContent = append(assistantContent, ContentBlock{
				Type:  "tool_use",
				ID:    tc.ID,
				Name:  tc.Name,
				Input: tc.Input,
			})
		}
		assistantMsg, _ := json.Marshal(assistantContent)
		messages = append(messages, Message{
			Role:    "assistant",
			Content: assistantMsg,
		})

		// Build user message with tool results.
		var userContent []ContentBlock
		for _, tr := range toolResults {
			userContent = append(userContent, ContentBlock{
				Type:      "tool_result",
				ToolUseID: tr.ToolUseID,
				Content:   tr.Content,
				IsError:   tr.IsError,
			})
		}
		userMsg, _ := json.Marshal(userContent)
		messages = append(messages, Message{
			Role:    "user",
			Content: userMsg,
		})

		// --- Mid-loop budget + context checks ---

		// Per-task budget soft limit: log once for analysis, then continue.
		if task.Budget > 0 && totalCostUSD >= task.Budget && !taskBudgetWarnLogged {
			taskBudgetWarnLogged = true
			logWarnCtx(ctx, "task budget soft-limit exceeded (continuing)",
				"budget", task.Budget,
				"spent", totalCostUSD,
				"role", task.Agent,
				"task_id", task.ID,
				"task_prompt_preview", task.Prompt[:min(120, len(task.Prompt))],
			)
		}

		// Global budget check.
		if br := checkBudget(cfg, agentName, "", 0); br != nil && !br.Allowed {
			logWarnCtx(ctx, "global budget exceeded mid-loop", "msg", br.Message)
			finalResult = &ProviderResult{
				Output: result.Output + "\n[stopped: global budget exceeded]",
			}
			break
		}

		// Pre-send token estimation: compress old messages if nearing context window.
		ctxWindow := contextWindowForModel(req.Model)
		threshold := ctxWindow * 80 / 100
		req.Messages = messages // update for estimation
		estTokens := estimateRequestTokens(req)
		if estTokens > threshold {
			// Try compression first before stopping.
			messages = compressMessages(messages, 3)
			req.Messages = messages
			estTokens = estimateRequestTokens(req)
			if estTokens > threshold {
				logWarnCtx(ctx, "context window limit after compression", "estimatedTokens", estTokens, "threshold", threshold)
				finalResult = &ProviderResult{
					Output: result.Output + "\n[stopped: context limit reached]",
				}
				break
			}
			logInfoCtx(ctx, "compressed old messages to fit context window", "estimatedTokens", estTokens, "threshold", threshold)
		}
	}

	if finalResult == nil {
		// Max iterations reached without final answer.
		finalResult = &ProviderResult{
			IsError: true,
			Error:   fmt.Sprintf("max tool iterations (%d) reached", maxIter),
		}
	}

	// Set accumulated totals on final result.
	finalResult.TokensIn = totalTokensIn
	finalResult.TokensOut = totalTokensOut
	finalResult.CostUSD = totalCostUSD
	finalResult.ProviderMs = totalProviderMs

	return finalResult
}

// --- Workspace Content Injection ---

// injectWorkspaceContent applies the three-tier workspace injection:
// always: workspace/rules/ directory
// agent: agent-specific rules from workspace/rules/{agentName}*
// on-demand: memory only via {{memory.KEY}} template
func injectWorkspaceContent(cfg *Config, task *Task, agentName string) {
	if cfg.WorkspaceDir == "" {
		return
	}

	maxInjectionSize := 50 * 1024  // 50KB — skip entirely above this
	indexThreshold := 20 * 1024    // 20KB — inject index instead of full dir above this

	// Helper: inject a directory either as full AddDirs, as a compact index, or skip.
	injectDir := func(dir string) {
		fi, err := os.Stat(dir)
		if err != nil || !fi.IsDir() {
			return
		}
		size := estimateDirSize(dir)
		if size > maxInjectionSize {
			logWarn("workspace dir exceeds 50KB, skipping injection", "dir", dir, "size", size)
			return
		}
		if size > indexThreshold {
			// Inject compact index into system prompt instead of full dir.
			idx := buildDirIndex(dir)
			if idx != "" {
				task.SystemPrompt += "\n\n" + idx
			}
			return
		}
		// Small enough — inject full directory.
		for _, d := range task.AddDirs {
			if d == dir {
				return // already added
			}
		}
		task.AddDirs = append(task.AddDirs, dir)
	}

	injectDir(filepath.Join(cfg.WorkspaceDir, "rules"))
	injectDir(filepath.Join(cfg.WorkspaceDir, "knowledge"))

	// Agent tier: find agent-specific rules and append to system prompt.
	if agentName != "" {
		roleRules := findAgentSpecificRules(filepath.Join(cfg.WorkspaceDir, "rules"), agentName)
		if roleRules != "" {
			task.SystemPrompt += "\n\n" + roleRules
		}
	}
	// On-demand tier: memory is resolved via {{memory.KEY}} in template.go, not here.
	// When index mode is active, individual rules can be loaded via {{rules.FILENAME}}.
}

// buildDirIndex generates a compact markdown index of a directory.
// Each file is summarized by its first line (or first 100 chars).
func buildDirIndex(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	dirName := filepath.Base(dir)
	var b strings.Builder
	b.WriteString(fmt.Sprintf("## Directory Index: %s\n\nUse `{{rules.FILENAME}}` to load a specific file on demand.\n\n", dirName))
	count := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		summary := strings.TrimSpace(string(data))
		// Use first line as summary.
		if idx := strings.IndexByte(summary, '\n'); idx >= 0 {
			summary = summary[:idx]
		}
		if len(summary) > 100 {
			summary = summary[:100] + "..."
		}
		// Strip markdown heading markers for cleaner display.
		summary = strings.TrimLeft(summary, "# ")
		b.WriteString(fmt.Sprintf("- **%s**: %s\n", e.Name(), summary))
		count++
	}
	if count == 0 {
		return ""
	}
	return b.String()
}

// findAgentSpecificRules reads files in rulesDir whose name contains agentName.
func findAgentSpecificRules(rulesDir, agentName string) string {
	entries, err := os.ReadDir(rulesDir)
	if err != nil {
		return ""
	}
	var parts []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.Contains(strings.ToLower(e.Name()), strings.ToLower(agentName)) {
			data, err := os.ReadFile(filepath.Join(rulesDir, e.Name()))
			if err == nil {
				parts = append(parts, string(data))
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

// estimateDirSize returns an estimate of the total file size in a directory.
func estimateDirSize(dir string) int {
	total := 0
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if info, err := e.Info(); err == nil {
			total += int(info.Size())
		}
	}
	return total
}
