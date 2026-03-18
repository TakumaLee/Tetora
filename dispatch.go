package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"tetora/internal/audit"
	"tetora/internal/classify"
	"tetora/internal/cost"
	dtypes "tetora/internal/dispatch"
	"tetora/internal/estimate"
	"tetora/internal/history"
	"tetora/internal/log"
	"tetora/internal/provider"
	"tetora/internal/sandbox"
	"tetora/internal/telemetry"
	"tetora/internal/trace"
	"tetora/internal/webhook"
	"tetora/internal/workspace"
)

// --- Type Aliases (canonical definitions in internal/dispatch) ---

type ChannelNotifier = dtypes.ChannelNotifier
type Task = dtypes.Task
type TaskResult = dtypes.TaskResult
type DispatchResult = dtypes.DispatchResult

// --- Webhook Helpers ---

// sendWebhooks converts cfg.Webhooks to []webhook.Config and posts the event payload
// to all matching endpoints.
func sendWebhooks(cfg *Config, event string, payload webhook.Payload) {
	whs := make([]webhook.Config, len(cfg.Webhooks))
	for i, w := range cfg.Webhooks {
		whs[i] = webhook.Config{URL: w.URL, Events: w.Events, Headers: w.Headers}
	}
	webhook.Send(whs, event, payload)
}

// webhookMatchesEvent checks whether a WebhookConfig should fire for the given event.
func webhookMatchesEvent(wh WebhookConfig, event string) bool {
	return webhook.MatchesEvent(webhook.Config{URL: wh.URL, Events: wh.Events, Headers: wh.Headers}, event)
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
	sandboxMgr        *sandbox.SandboxManager       // --- P13.2: Sandbox Plugin ---
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
	task         Task
	startAt      time.Time
	lastActivity time.Time // last time this task produced output or progress
	cmd          *exec.Cmd
	cancelFn     context.CancelFunc
	stalled      bool // true when heartbeat monitor has flagged this task
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
// It also updates the lastActivity timestamp on the corresponding taskState for heartbeat monitoring.
func (s *dispatchState) publishSSE(event SSEEvent) {
	if s.broker == nil {
		return
	}

	// Update lastActivity for heartbeat monitoring on output/progress events.
	if event.TaskID != "" {
		switch event.Type {
		case SSEOutputChunk, SSEProgress, SSEToolCall, SSEToolResult:
			s.mu.Lock()
			if ts, ok := s.running[event.TaskID]; ok {
				ts.lastActivity = time.Now()
			}
			s.mu.Unlock()
		}
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

// emitAgentState publishes an agent_state SSE event to the dashboard broker.
// state is one of: "idle", "thinking", "working", "waiting", "done".
func emitAgentState(broker *sseBroker, agent, state string) {
	if broker == nil || agent == "" {
		return
	}
	broker.Publish(SSEDashboardKey, SSEEvent{
		Type: SSEAgentState,
		Data: map[string]string{"agent": agent, "state": state},
	})
}

// publishToSSEBroker publishes an SSE event directly via a broker reference.
// Used by runSingleTask which has no access to dispatchState.
func publishToSSEBroker(broker dtypes.SSEBrokerPublisher, event SSEEvent) {
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
	// Forward to workflow SSE channel when set (so dashboard workflow view sees streaming output).
	if event.WorkflowRunID != "" {
		keys = append(keys, "workflow:"+event.WorkflowRunID)
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

	// Build per-agent sprite states.
	sprites := make(map[string]string)
	for _, ts := range s.running {
		if ts.task.Agent != "" {
			sprites[ts.task.Agent] = resolveAgentSprite("running", status, ts.task.Source)
		}
	}
	for _, r := range s.finished {
		if r.Agent != "" {
			if _, busy := sprites[r.Agent]; !busy {
				sprites[r.Agent] = resolveAgentSprite(r.Status, status, "")
			}
		}
	}
	for _, da := range s.discordActivities {
		if da.Agent != "" {
			if _, busy := sprites[da.Agent]; !busy {
				sprites[da.Agent] = resolveAgentSprite("running", status, "discord")
			}
		}
	}

	out := map[string]any{
		"status":    status,
		"running":   len(s.running),
		"completed": len(s.finished),
		"tasks":     tasks,
		"discord":   discord,
		"sprites":   sprites,
	}
	if s.active {
		out["elapsed"] = time.Since(s.startAt).Round(time.Second).String()
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	return b
}

// --- Dispatch Core ---

// selectSem returns childSem for sub-agent tasks (depth > 0), otherwise the parent sem.
// This prevents deadlock when parent holds a sem slot and spawns child tasks that also need slots.
func selectSem(sem, childSem chan struct{}, depth int) chan struct{} {
	if depth > 0 && childSem != nil {
		return childSem
	}
	return sem
}

func dispatch(ctx context.Context, cfg *Config, tasks []Task, state *dispatchState, sem, childSem chan struct{}) *DispatchResult {
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
			s := selectSem(sem, childSem, t.Depth)
			if t.Depth == 0 && cfg.Runtime.SlotPressureGuard != nil {
				ar, err := cfg.Runtime.SlotPressureGuard.(*dtypes.SlotPressureGuard).AcquireSlot(ctx, s, t.Source)
				if err != nil {
					results <- TaskResult{
						ID: t.ID, Name: t.Name, Status: "cancelled",
						Error: "slot acquisition cancelled: " + err.Error(), Model: t.Model, SessionID: t.SessionID,
					}
					return
				}
				defer cfg.Runtime.SlotPressureGuard.(*dtypes.SlotPressureGuard).ReleaseSlot()
				defer func() { <-s }()
				var r TaskResult
				if t.ReviewLoop {
					r = dispatchDevQALoop(ctx, cfg, t, state, sem, childSem)
				} else {
					r = runTask(ctx, cfg, t, state)
				}
				r.SlotWarning = ar.Warning
				results <- r
			} else {
				s <- struct{}{}
				defer func() { <-s }()
				var r TaskResult
				if t.ReviewLoop {
					r = dispatchDevQALoop(ctx, cfg, t, state, sem, childSem)
				} else {
					r = runTask(ctx, cfg, t, state)
				}
				results <- r
			}
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
func runSingleTask(ctx context.Context, cfg *Config, task Task, sem, childSem chan struct{}, agentName string) TaskResult {
	// Register worker origin (if not already registered by cron layer).
	if cfg.Runtime.HookRecv != nil && task.SessionID != "" {
		cfg.Runtime.HookRecv.(*hookReceiver).RegisterOriginIfAbsent(task.SessionID, &workerOrigin{
			TaskID:   task.ID,
			TaskName: task.Name,
			Source:   task.Source,
			Agent:    agentName,
		})
	}

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
	complexity := classify.Classify(task.Prompt, task.Source)
	if task.Source != "route-classify" {
		buildTieredPrompt(cfg, &task, agentName, complexity)
	} else {
		// For routing classification, only set up workspace dir and baseDir.
		if agentName != "" {
			ws := resolveWorkspace(cfg, agentName)
			if ws.Dir != "" {
				task.Workdir = ws.Dir
			}
			task.AddDirs = append(task.AddDirs, cfg.BaseDir)
		}
	}

	// Validate directories before running.
	if err := validateDirs(cfg, task, agentName); err != nil {
		return TaskResult{
			ID: task.ID, Name: task.Name, Status: "error",
			Error: err.Error(), Model: task.Model, SessionID: task.SessionID,
		}
	}

	s := selectSem(sem, childSem, task.Depth)
	var slotWarning string
	if task.Depth == 0 && cfg.Runtime.SlotPressureGuard != nil {
		ar, err := cfg.Runtime.SlotPressureGuard.(*dtypes.SlotPressureGuard).AcquireSlot(ctx, s, task.Source)
		if err != nil {
			return TaskResult{
				ID: task.ID, Name: task.Name, Status: "cancelled",
				Error: "slot acquisition cancelled: " + err.Error(), Model: task.Model, SessionID: task.SessionID,
			}
		}
		defer cfg.Runtime.SlotPressureGuard.(*dtypes.SlotPressureGuard).ReleaseSlot()
		defer func() { <-s }()
		slotWarning = ar.Warning
	} else {
		s <- struct{}{}
		defer func() { <-s }()
	}

	// Signal that this task has acquired a slot and is about to execute.
	if task.OnStart != nil {
		task.OnStart()
	}

	// Budget check before execution.
	if budgetResult := cost.CheckBudget(cfg.Budgets, cfg.HistoryDB, agentName, "", 0); budgetResult != nil && !budgetResult.Allowed {
		log.WarnCtx(ctx, "budget check failed", "taskId", task.ID[:8], "reason", budgetResult.Message)
		return TaskResult{
			ID: task.ID, Name: task.Name, Status: "error",
			Error: "budget_exceeded: " + budgetResult.Message, Model: task.Model, SessionID: task.SessionID,
		}
	} else if budgetResult != nil && budgetResult.DowngradeModel != "" {
		log.InfoCtx(ctx, "auto-downgrade model", "taskId", task.ID[:8],
			"from", task.Model, "to", budgetResult.DowngradeModel,
			"utilization", fmt.Sprintf("%.0f%%", budgetResult.Utilization*100))
		task.Model = budgetResult.DowngradeModel
	}

	providerName := resolveProviderName(cfg, task, agentName)

	log.DebugCtx(ctx, "task start",
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
	if task.SSEBroker != nil {
		publishToSSEBroker(task.SSEBroker, SSEEvent{
			Type:           SSEStarted,
			TaskID:         task.ID,
			SessionID:      task.SessionID,
			WorkflowRunID:  task.WorkflowRunID,
			Data: map[string]any{
				"name":  task.Name,
				"role":  agentName,
				"model": task.Model,
			},
		})
		eventCh = make(chan SSEEvent, 128)
		go func() {
			for ev := range eventCh {
				// Stamp workflow run ID so events route to the workflow SSE channel.
				if task.WorkflowRunID != "" {
					ev.WorkflowRunID = task.WorkflowRunID
				}
				log.Debug("sse forward", "type", ev.Type, "taskID", ev.TaskID, "sessionID", ev.SessionID)
				publishToSSEBroker(task.SSEBroker, ev)
			}
		}()
	}

	start := time.Now()
	pr := executeWithProvider(taskCtx, cfg, task, agentName, cfg.Runtime.ProviderRegistry.(*providerRegistry), eventCh)
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

	// If the provider reported success but produced no output, treat it as an
	// error — the session likely exited before producing any messages (e.g.
	// CLI startup failure, auth error, or silent crash).
	if result.Status == "success" && strings.TrimSpace(result.Output) == "" {
		result.Status = "error"
		result.Error = "session produced no output"
	}

	// Offline queue: if all providers are unavailable, enqueue for later retry.
	if result.Status == "error" && isAllProvidersUnavailable(result.Error) && cfg.OfflineQueue.Enabled {
		if !isQueueFull(cfg.HistoryDB, cfg.OfflineQueue.MaxItemsOrDefault()) {
			if err := enqueueTask(cfg.HistoryDB, task, agentName, 0); err == nil {
				result.Status = "queued"
				log.InfoCtx(ctx, "task queued for offline retry",
					"taskId", task.ID[:8], "name", task.Name)
			}
		}
	}

	log.DebugCtx(ctx, "task done",
		"taskId", task.ID[:8], "name", task.Name,
		"elapsed", elapsed.Round(time.Millisecond),
		"cost", result.CostUSD,
		"tokensIn", result.TokensIn, "tokensOut", result.TokensOut,
		"provider", result.Provider,
		"status", result.Status)

	// Record token telemetry (async).
	go telemetry.Record(cfg.HistoryDB, telemetry.Entry{
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
		result.OutputFile = saveTaskOutput(cfg.BaseDir, task.ID, []byte(pr.Output))
	}

	// SSE streaming: publish completed/error event.
	if task.SSEBroker != nil && result.Status != "queued" {
		evType := SSECompleted
		if result.Status != "success" {
			evType = SSEError
		}
		publishToSSEBroker(task.SSEBroker, SSEEvent{
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

	result.SlotWarning = slotWarning
	return result
}

func runTask(ctx context.Context, cfg *Config, task Task, state *dispatchState) TaskResult {
	// Propagate trace ID from context to task.
	if task.TraceID == "" {
		task.TraceID = trace.IDFromContext(ctx)
	}

	agentName := task.Agent

	// --- P19.5: Unified Presence/Typing Indicators --- Start typing in source channel.
	presence := globalPresence
	if appCtx := appFromCtx(ctx); appCtx != nil && appCtx.Presence != nil {
		presence = appCtx.Presence
	}
	if presence != nil && task.Source != "" {
		presence.StartTyping(ctx, task.Source)
		defer presence.StopTyping(task.Source)
	}

	// --- P16.3: Prompt Injection Defense v2 --- Apply before execution.
	if err := applyInjectionDefense(ctx, cfg, &task); err != nil {
		return TaskResult{
			ID: task.ID, Name: task.Name, Status: "error",
			Error: fmt.Sprintf("injection defense: %v", err), Model: task.Model, SessionID: task.SessionID,
		}
	}

	// Classify request complexity and build tiered system prompt.
	complexity := classify.Classify(task.Prompt, task.Source)
	buildTieredPrompt(cfg, &task, agentName, complexity)

	// Apply trust level (may override permissionMode for observe mode).
	trustLevel, _ := applyTrustToTask(cfg, &task, agentName)
	if trustLevel == TrustObserve {
		log.DebugCtx(ctx, "trust: observe mode, forcing plan permission", "agent", agentName)
	}

	// Validate directories before running.
	if err := validateDirs(cfg, task, agentName); err != nil {
		return TaskResult{
			ID: task.ID, Name: task.Name, Status: "error",
			Error: err.Error(), Model: task.Model, SessionID: task.SessionID,
		}
	}

	// --- P13.2: Sandbox Plugin --- Check sandbox policy for this agent.
	useSandbox, sandboxErr := sandbox.ShouldUseSandbox(cfg, agentName, state.sandboxMgr)
	if sandboxErr != nil {
		return TaskResult{
			ID: task.ID, Name: task.Name, Status: "error",
			Error: sandboxErr.Error(), Model: task.Model, SessionID: task.SessionID,
		}
	}
	var sandboxID string
	if useSandbox && state.sandboxMgr != nil {
		image := sandbox.ImageForAgent(cfg, agentName)
		sbID, err := state.sandboxMgr.EnsureSandboxWithImage(task.SessionID, task.Workdir, image)
		if err != nil {
			log.WarnCtx(ctx, "sandbox creation failed", "taskId", task.ID[:8], "error", err)
			// If policy is "required", this is fatal; if "optional", fall through.
			if sandbox.PolicyForAgent(cfg, agentName) == "required" {
				return TaskResult{
					ID: task.ID, Name: task.Name, Status: "error",
					Error: fmt.Sprintf("sandbox required but creation failed: %v", err),
					Model: task.Model, SessionID: task.SessionID,
				}
			}
		} else {
			sandboxID = sbID
			log.DebugCtx(ctx, "sandbox active for task", "taskId", task.ID[:8], "sandboxId", sandboxID)
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

	now := time.Now()
	ts := &taskState{task: task, startAt: now, lastActivity: now, cancelFn: taskCancel}
	state.mu.Lock()
	state.running[task.ID] = ts
	state.mu.Unlock()

	// Budget check before execution.
	if budgetResult := cost.CheckBudget(cfg.Budgets, cfg.HistoryDB, agentName, "", 0); budgetResult != nil && !budgetResult.Allowed {
		log.WarnCtx(ctx, "budget check failed", "taskId", task.ID[:8], "reason", budgetResult.Message)
		return TaskResult{
			ID: task.ID, Name: task.Name, Status: "error",
			Error: "budget_exceeded: " + budgetResult.Message, Model: task.Model, SessionID: task.SessionID,
		}
	} else if budgetResult != nil && budgetResult.DowngradeModel != "" {
		log.InfoCtx(ctx, "auto-downgrade model", "taskId", task.ID[:8],
			"from", task.Model, "to", budgetResult.DowngradeModel,
			"utilization", fmt.Sprintf("%.0f%%", budgetResult.Utilization*100))
		task.Model = budgetResult.DowngradeModel
	}

	providerName := resolveProviderName(cfg, task, agentName)

	log.DebugCtx(ctx, "task start",
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
	emitAgentState(state.broker, agentName, "working")

	// Create event channel for provider streaming.
	// Always create when broker exists — subscribers may join after task starts
	// (e.g. Discord progress updater subscribes in a goroutine).
	var eventCh chan SSEEvent
	if state.broker != nil {
		eventCh = make(chan SSEEvent, 128)
		go func() {
			for ev := range eventCh {
				state.publishSSE(ev)
			}
		}()
	}

	// Reuse complexity from tiered prompt builder for tool trimming.
	start := time.Now()
	var pr *ProviderResult
	if complexity == classify.Simple {
		// Simple requests skip the tool engine entirely.
		pr = executeWithProvider(taskCtx, cfg, task, agentName, cfg.Runtime.ProviderRegistry.(*providerRegistry), eventCh)
	} else {
		pr = executeWithProviderAndTools(taskCtx, cfg, task, agentName, cfg.Runtime.ProviderRegistry.(*providerRegistry), eventCh, state.broker)
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
		if !isQueueFull(cfg.HistoryDB, cfg.OfflineQueue.MaxItemsOrDefault()) {
			if err := enqueueTask(cfg.HistoryDB, task, agentName, 0); err == nil {
				result.Status = "queued"
				log.InfoCtx(ctx, "task queued for offline retry",
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
				emitAgentState(state.broker, agentName, "waiting")
			} else {
				log.WarnCtx(ctx, "failed to enqueue task", "taskId", task.ID[:8], "error", err)
			}
		} else {
			log.WarnCtx(ctx, "offline queue full, task not enqueued", "taskId", task.ID[:8])
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

	log.DebugCtx(ctx, "task done",
		"taskId", task.ID[:8], "name", task.Name,
		"elapsed", elapsed.Round(time.Millisecond),
		"cost", result.CostUSD,
		"tokensIn", result.TokensIn, "tokensOut", result.TokensOut,
		"status", result.Status)

	// Record token telemetry (async).
	go telemetry.Record(cfg.HistoryDB, telemetry.Entry{
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
		result.OutputFile = saveTaskOutput(cfg.BaseDir, task.ID, []byte(pr.Output))
	}

	// Record to history DB.
	recordHistory(cfg.HistoryDB, task.ID, task.Name, task.Source, task.Agent, task, result,
		start.Format(time.RFC3339), time.Now().Format(time.RFC3339), result.OutputFile)

	// Record session activity (skip for sources that manage their own sessions:
	// "chat" → HTTP handler, "route:" → discord/telegram executeRoute).
	if !strings.HasPrefix(task.Source, "chat") && !strings.HasPrefix(task.Source, "route:") {
		recordSessionActivity(cfg.HistoryDB, task, result, task.Agent)
	}
	// Log to system dispatch log (skip only for chat — already handled there).
	if !strings.HasPrefix(task.Source, "chat") {
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
		if result.Status == "success" {
			emitAgentState(state.broker, agentName, "done")
		} else {
			emitAgentState(state.broker, agentName, "idle")
		}
	}

	// Webhook notifications.
	sendWebhooks(cfg, result.Status, webhook.Payload{
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
	// Use a detached context so the reflection goroutine is not cancelled
	// when the parent dispatch context (derived from r.Context()) is done.
	if shouldReflect(cfg, task, result) {
		go func() {
			reflCtx, reflCancel := context.WithTimeout(
				trace.WithID(context.Background(), trace.IDFromContext(ctx)),
				2*time.Minute,
			)
			defer reflCancel()
			ref, err := performReflection(reflCtx, cfg, task, result)
			if err != nil {
				log.Debug("reflection failed", "taskId", task.ID[:8], "error", err)
				return
			}
			if err := storeReflection(cfg.HistoryDB, ref); err != nil {
				log.Debug("reflection store failed", "taskId", task.ID[:8], "error", err)
			} else {
				log.Debug("reflection stored", "taskId", task.ID[:8], "role", ref.Agent, "score", ref.Score)
			}
		}()
	}

	// --- P13.2: Sandbox Plugin --- Cleanup sandbox after task completion.
	if sandboxID != "" && state.sandboxMgr != nil {
		if err := state.sandboxMgr.DestroySandbox(sandboxID); err != nil {
			log.WarnCtx(ctx, "sandbox cleanup failed", "sandboxId", sandboxID, "error", err)
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

// --- Dispatch Dev↔QA Loop ---

// dispatchDevQALoop runs the Dev↔QA retry loop for the main dispatch path.
// On each attempt: execute task → QA review → (pass → done) | (fail → record failure → inject feedback → retry).
// After maxRetries QA failures, the task is escalated (returned with QAApproved=false).
//
// Uses SmartDispatch config for reviewer agent and max retries.
// Skill failure injection is integrated: QA rejections are recorded and loaded on retry.
func dispatchDevQALoop(ctx context.Context, cfg *Config, task Task, state *dispatchState, sem, childSem chan struct{}) TaskResult {
	maxRetries := cfg.SmartDispatch.MaxRetriesOrDefault() // default 3
	originalPrompt := task.Prompt

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Step 1: Dev execution.
		result := runTask(ctx, cfg, task, state)

		// If execution itself failed (crash/timeout/empty output), exit loop immediately.
		if result.Status != "success" {
			result.Attempts = attempt + 1
			return result
		}
		if strings.TrimSpace(result.Output) == "" {
			result.Attempts = attempt + 1
			return result
		}

		// Step 2: QA review.
		reviewOK, reviewComment := reviewOutput(ctx, cfg, originalPrompt, result.Output, task.Agent, sem, childSem)
		if reviewOK {
			approved := true
			result.QAApproved = &approved
			result.QAComment = reviewComment
			result.Attempts = attempt + 1
			log.InfoCtx(ctx, "dispatchDevQA: review passed", "agent", task.Agent, "attempt", attempt+1)
			return result
		}

		// QA failed.
		log.InfoCtx(ctx, "dispatchDevQA: review failed, injecting feedback",
			"agent", task.Agent, "attempt", attempt+1, "maxAttempts", maxRetries+1,
			"comment", truncate(reviewComment, 200))

		// Record QA rejection as skill failure for future context injection.
		qaFailMsg := fmt.Sprintf("[QA rejection attempt %d] %s", attempt+1, reviewComment)
		skills := selectSkills(cfg, task)
		for _, s := range skills {
			appendSkillFailure(cfg, s.Name, task.Name, task.Agent, qaFailMsg)
		}

		if attempt == maxRetries {
			// All retries exhausted — escalate.
			log.WarnCtx(ctx, "dispatchDevQA: max retries exhausted, escalating",
				"agent", task.Agent, "attempts", maxRetries+1)
			rejected := false
			result.QAApproved = &rejected
			result.QAComment = fmt.Sprintf("Dev↔QA loop exhausted (%d attempts): %s", maxRetries+1, reviewComment)
			result.Attempts = attempt + 1
			return result
		}

		// Step 3: Rebuild prompt with failure context + QA feedback for retry.
		task.Prompt = originalPrompt

		// Inject accumulated skill failures.
		for _, s := range skills {
			failures := loadSkillFailuresByName(cfg, s.Name)
			if failures != "" {
				task.Prompt += fmt.Sprintf("\n\n<skill-failures name=\"%s\">\n%s\n</skill-failures>", s.Name, failures)
			}
		}

		// Inject QA reviewer's specific feedback.
		task.Prompt += fmt.Sprintf("\n\n## QA Review Feedback (Attempt %d)\n", attempt+1)
		task.Prompt += "The QA reviewer rejected the output. Issues found:\n"
		task.Prompt += reviewComment
		task.Prompt += fmt.Sprintf("\n\nAddress ALL issues above. This is retry %d of %d.\n", attempt+2, maxRetries+1)

		// Fresh IDs for retry (no session bleed between attempts).
		task.ID = newUUID()
		task.SessionID = newUUID()
	}

	// Unreachable, but satisfy the compiler.
	return TaskResult{}
}

// --- Retry / Reroute ---

// retryTask re-runs a previously failed task with the same parameters.
// A new task ID is generated but all other parameters are preserved.
func retryTask(ctx context.Context, cfg *Config, taskID string, state *dispatchState, sem, childSem chan struct{}) (*TaskResult, error) {
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

	result := runSingleTask(ctx, cfg, task, sem, childSem, task.Agent)

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

	audit.Log(cfg.HistoryDB, "task.retry", task.Source,
		fmt.Sprintf("original=%s new=%s status=%s", taskID, task.ID, result.Status), "")

	return &result, nil
}

// rerouteTask re-dispatches a previously failed task through smart dispatch,
// allowing a different agent to handle it.
func rerouteTask(ctx context.Context, cfg *Config, taskID string, state *dispatchState, sem, childSem chan struct{}) (*SmartDispatchResult, error) {
	state.mu.Lock()
	ft, ok := state.failedTasks[taskID]
	state.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("task %s not found in failed tasks", taskID)
	}

	if !cfg.SmartDispatch.Enabled {
		return nil, fmt.Errorf("smart dispatch is not enabled")
	}

	result := smartDispatch(ctx, cfg, ft.task.Prompt, "reroute", state, sem, childSem)

	// If reroute succeeded, remove from failed tasks.
	if result.Task.Status == "success" {
		state.mu.Lock()
		delete(state.failedTasks, taskID)
		state.mu.Unlock()
	}

	audit.Log(cfg.HistoryDB, "task.reroute", "reroute",
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

// --- Forwarding functions (canonical implementations in internal/dispatch + internal/trace) ---

// ansiEscapeRe matches ANSI escape sequences (used by discord_progress.go, discord_terminal.go).
var ansiEscapeRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func newUUID() string                        { return trace.NewUUID() }
func fillDefaults(cfg *Config, t *Task)      { dtypes.FillDefaults(cfg, t) }
func estimateTimeout(prompt string) string   { return dtypes.EstimateTimeout(prompt) }
func sanitizePrompt(input string, maxLen int) string { return dtypes.SanitizePrompt(input, maxLen) }

// --- P21.2: Writing Style ---

// loadWritingStyle resolves writing style guidelines from config.
func loadWritingStyle(cfg *Config) string {
	if cfg.WritingStyle.FilePath != "" {
		data, err := os.ReadFile(cfg.WritingStyle.FilePath)
		if err == nil {
			return strings.TrimSpace(string(data))
		}
		log.Warn("failed to load writing style file", "path", cfg.WritingStyle.FilePath, "error", err)
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
		log.Warn("save output failed", "error", err)
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

// --- Type aliases (history types used across root package) ---

type JobRun = history.JobRun
type CostStats = history.CostStats
type HistoryQuery = history.HistoryQuery
type DayStat = history.DayStat
type MetricsResult = history.MetricsResult
type DailyMetrics = history.DailyMetrics
type ProviderMetrics = history.ProviderMetrics
type SubtaskCount = history.SubtaskCount

// --- JSON helpers ---

func jsonStr(v any) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case json.Number:
		return val.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}

func jsonFloat(v any) float64 {
	if v == nil {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return val
	case json.Number:
		f, _ := val.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(val, 64)
		return f
	default:
		return 0
	}
}

func jsonInt(v any) int {
	if v == nil {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return int(val)
	case json.Number:
		i, _ := val.Int64()
		return int(i)
	case string:
		i, _ := strconv.Atoi(val)
		return i
	default:
		return 0
	}
}

// --- Record History Helper ---
// Used by both cron.go and dispatch.go to record task execution.

func recordHistory(dbPath string, jobID, name, source, role string, task Task, result TaskResult, startedAt, finishedAt, outputFile string) {
	if dbPath == "" {
		return
	}
	run := JobRun{
		JobID:         jobID,
		Name:          name,
		Source:        source,
		StartedAt:     startedAt,
		FinishedAt:    finishedAt,
		Status:        result.Status,
		ExitCode:      result.ExitCode,
		CostUSD:       result.CostUSD,
		OutputSummary: truncateStr(result.Output, 1000),
		Error:         result.Error,
		Model:         result.Model,
		SessionID:     result.SessionID,
		OutputFile:    outputFile,
		TokensIn:      result.TokensIn,
		TokensOut:     result.TokensOut,
		Agent:         role,
		ParentID:      task.ParentID,
	}
	if err := history.InsertRun(dbPath, run); err != nil {
		// Log but don't fail the task.
		log.Warn("record history failed", "error", err)
	}

	// Record skill completion events for all skills that were injected for this task.
	recordSkillCompletion(dbPath, task, result, role, startedAt, finishedAt)
}

// --- Generic helpers ---

// truncateStr is like truncate() but avoids name collision if truncate is in another file.
func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// stringSliceContains checks if a string slice contains a value.
func stringSliceContains(ss []string, s string) bool {
	for _, v := range ss {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}

// ============================================================
// Merged from heartbeat.go
// ============================================================

// --- Agent Heartbeat / Self-healing ---

// HeartbeatMonitor periodically checks running tasks for signs of being stuck.
type HeartbeatMonitor struct {
	cfg      HeartbeatConfig
	state    *dispatchState
	notifyFn func(string)

	mu    sync.Mutex
	stats HeartbeatStats

	// Idle tracking.
	systemIdleCheckFn func() bool // injected idle check function
	idleMu            sync.RWMutex
	systemIdleSince   time.Time // when system became idle (zero = not idle)
}

// HeartbeatStats tracks heartbeat monitor activity.
type HeartbeatStats struct {
	CheckCount      int        `json:"checkCount"`                // total scan cycles performed
	StallsDetected  int        `json:"stallsDetected"`            // total stall events
	StallsRecovered int        `json:"stallsRecovered"`           // stalls that resolved (task produced output again)
	AutoCancelled   int        `json:"autoCancelled"`             // tasks force-cancelled by heartbeat
	TimeoutWarnings int        `json:"timeoutWarnings"`           // timeout proximity warnings emitted
	LastCheck       time.Time  `json:"lastCheck"`                 // timestamp of last scan cycle
	SystemIdleSince *time.Time `json:"systemIdleSince,omitempty"` // when system entered idle state
}

func newHeartbeatMonitor(cfg HeartbeatConfig, state *dispatchState, notifyFn func(string)) *HeartbeatMonitor {
	return &HeartbeatMonitor{
		cfg:      cfg,
		state:    state,
		notifyFn: notifyFn,
	}
}

// Start begins the heartbeat monitor loop. Blocks until ctx is cancelled.
func (h *HeartbeatMonitor) Start(ctx context.Context) {
	interval := h.cfg.IntervalOrDefault()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	log.Info("heartbeat monitor started",
		"interval", interval.String(),
		"stallThreshold", h.cfg.StallThresholdOrDefault().String(),
		"timeoutWarnRatio", fmt.Sprintf("%.0f%%", h.cfg.TimeoutWarnRatioOrDefault()*100),
		"autoCancel", h.cfg.AutoCancel)

	for {
		select {
		case <-ctx.Done():
			log.Info("heartbeat monitor stopped")
			return
		case <-ticker.C:
			h.check()
		}
	}
}

// SetIdleCheckFn sets the function used to check system idle state.
func (h *HeartbeatMonitor) SetIdleCheckFn(fn func() bool) {
	h.systemIdleCheckFn = fn
}

// SystemIdleDuration returns how long the system has been continuously idle.
// Returns 0 if the system is not idle or idle tracking is not configured.
func (h *HeartbeatMonitor) SystemIdleDuration() time.Duration {
	h.idleMu.RLock()
	defer h.idleMu.RUnlock()
	if h.systemIdleSince.IsZero() {
		return 0
	}
	return time.Since(h.systemIdleSince)
}

// Stats returns a snapshot of heartbeat statistics.
func (h *HeartbeatMonitor) Stats() HeartbeatStats {
	h.mu.Lock()
	s := h.stats
	h.mu.Unlock()

	h.idleMu.RLock()
	if !h.systemIdleSince.IsZero() {
		t := h.systemIdleSince
		s.SystemIdleSince = &t
	}
	h.idleMu.RUnlock()
	return s
}

// check performs a single heartbeat scan of all running tasks.
func (h *HeartbeatMonitor) check() {
	h.mu.Lock()
	h.stats.CheckCount++
	h.stats.LastCheck = time.Now()
	h.mu.Unlock()

	stallThreshold := h.cfg.StallThresholdOrDefault()
	warnRatio := h.cfg.TimeoutWarnRatioOrDefault()
	now := time.Now()

	h.state.mu.Lock()
	// Snapshot running tasks under lock.
	type taskSnapshot struct {
		id           string
		name         string
		agent        string
		startAt      time.Time
		lastActivity time.Time
		timeout      string
		stalled      bool
		cancelFn     context.CancelFunc
	}
	tasks := make([]taskSnapshot, 0, len(h.state.running))
	for _, ts := range h.state.running {
		tasks = append(tasks, taskSnapshot{
			id:           ts.task.ID,
			name:         ts.task.Name,
			agent:        ts.task.Agent,
			startAt:      ts.startAt,
			lastActivity: ts.lastActivity,
			timeout:      ts.task.Timeout,
			stalled:      ts.stalled,
			cancelFn:     ts.cancelFn,
		})
	}
	h.state.mu.Unlock()

	if len(tasks) == 0 {
		return
	}

	for _, t := range tasks {
		silent := now.Sub(t.lastActivity)
		elapsed := now.Sub(t.startAt)
		shortID := t.id
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}

		// --- Stall detection ---
		if silent > stallThreshold {
			if !t.stalled {
				// Newly stalled.
				h.mu.Lock()
				h.stats.StallsDetected++
				h.mu.Unlock()

				h.state.mu.Lock()
				if ts, ok := h.state.running[t.id]; ok {
					ts.stalled = true
				}
				h.state.mu.Unlock()

				log.Warn("heartbeat: task stalled",
					"taskId", shortID,
					"name", t.name,
					"agent", t.agent,
					"silent", silent.Round(time.Second).String(),
					"threshold", stallThreshold.String())

				// Publish stall SSE event.
				h.state.publishSSE(SSEEvent{
					Type:   SSETaskStalled,
					TaskID: t.id,
					Data: map[string]any{
						"name":      t.name,
						"agent":     t.agent,
						"silent":    silent.Round(time.Second).String(),
						"elapsed":   elapsed.Round(time.Second).String(),
						"threshold": stallThreshold.String(),
					},
				})

				// Notify.
				if h.notifyFn != nil && h.cfg.NotifyOnStallOrDefault() {
					h.notifyFn(fmt.Sprintf("Agent heartbeat alert: task %s (%s) has stalled — no output for %s",
						shortID, t.name, silent.Round(time.Second)))
				}
			}

			// Auto-cancel if stalled for 2x threshold.
			if h.cfg.AutoCancel && silent > 2*stallThreshold {
				log.Warn("heartbeat: auto-cancelling stalled task",
					"taskId", shortID,
					"name", t.name,
					"silent", silent.Round(time.Second).String())

				if t.cancelFn != nil {
					t.cancelFn()
				}

				h.mu.Lock()
				h.stats.AutoCancelled++
				h.mu.Unlock()

				h.state.publishSSE(SSEEvent{
					Type:   SSEHeartbeatAlert,
					TaskID: t.id,
					Data: map[string]any{
						"action":  "auto_cancel",
						"name":    t.name,
						"agent":   t.agent,
						"silent":  silent.Round(time.Second).String(),
						"elapsed": elapsed.Round(time.Second).String(),
					},
				})

				if h.notifyFn != nil {
					h.notifyFn(fmt.Sprintf("Agent heartbeat: auto-cancelled stalled task %s (%s) after %s of silence",
						shortID, t.name, silent.Round(time.Second)))
				}
			}
		} else if t.stalled {
			// Task was stalled but is now producing output again — recovered.
			h.state.mu.Lock()
			if ts, ok := h.state.running[t.id]; ok {
				ts.stalled = false
			}
			h.state.mu.Unlock()

			h.mu.Lock()
			h.stats.StallsRecovered++
			h.mu.Unlock()

			log.Info("heartbeat: task recovered",
				"taskId", shortID,
				"name", t.name,
				"agent", t.agent)

			h.state.publishSSE(SSEEvent{
				Type:   SSETaskRecovered,
				TaskID: t.id,
				Data: map[string]any{
					"name":  t.name,
					"agent": t.agent,
				},
			})
		}

		// --- Timeout proximity warning ---
		if t.timeout != "" {
			if timeout, err := time.ParseDuration(t.timeout); err == nil && timeout > 0 {
				if elapsed > time.Duration(float64(timeout)*warnRatio) && !t.stalled {
					// Only warn once per task by checking if we're close to the boundary.
					// We emit this warning when elapsed first crosses warnRatio * timeout.
					// Since check() runs periodically, we allow a window of 2 intervals.
					boundary := time.Duration(float64(timeout) * warnRatio)
					if elapsed-boundary < 2*h.cfg.IntervalOrDefault() {
						h.mu.Lock()
						h.stats.TimeoutWarnings++
						h.mu.Unlock()

						remaining := timeout - elapsed
						log.Warn("heartbeat: task approaching timeout",
							"taskId", shortID,
							"name", t.name,
							"elapsed", elapsed.Round(time.Second).String(),
							"timeout", timeout.String(),
							"remaining", remaining.Round(time.Second).String())

						h.state.publishSSE(SSEEvent{
							Type:   SSEHeartbeatAlert,
							TaskID: t.id,
							Data: map[string]any{
								"action":    "timeout_warning",
								"name":      t.name,
								"agent":     t.agent,
								"elapsed":   elapsed.Round(time.Second).String(),
								"timeout":   timeout.String(),
								"remaining": remaining.Round(time.Second).String(),
							},
						})
					}
				}
			}
		}
	}

	// --- Idle state tracking ---
	if h.systemIdleCheckFn != nil {
		idle := h.systemIdleCheckFn()
		h.idleMu.Lock()
		if idle {
			if h.systemIdleSince.IsZero() {
				h.systemIdleSince = time.Now()
				log.Debug("heartbeat: system entered idle state")
			}
		} else {
			if !h.systemIdleSince.IsZero() {
				log.Debug("heartbeat: system left idle state",
					"idleDuration", time.Since(h.systemIdleSince).Round(time.Second).String())
			}
			h.systemIdleSince = time.Time{}
		}
		h.idleMu.Unlock()
	}
}

// ============================================================
// Merged from route.go
// ============================================================

// devQALoopResult holds the outcome of a Dev↔QA retry loop.
type devQALoopResult struct {
	Result     TaskResult
	QAApproved bool
	Attempts   int
	TotalCost  float64
}

// --- Smart Dispatch Types (aliases to internal/dispatch) ---

type RouteRequest = dtypes.RouteRequest
type RouteResult = dtypes.RouteResult
type SmartDispatchResult = dtypes.SmartDispatchResult

// --- Binding Classification (Highest Priority) ---

// checkBindings delegates to internal/dispatch.CheckBindings.
func checkBindings(cfg *Config, req RouteRequest) *RouteResult {
	return dtypes.CheckBindings(cfg, req)
}

// --- Keyword Classification (Fast Path) ---

// classifyByKeywords delegates to internal/dispatch.ClassifyByKeywords.
func classifyByKeywords(cfg *Config, prompt string) *RouteResult {
	return dtypes.ClassifyByKeywords(cfg, prompt)
}

// --- LLM Classification (Slow Path) ---

// routeSemGlobal is a dedicated semaphore for routing LLM calls.
// Routing should never compete with task execution for slots.
var routeSemGlobal = make(chan struct{}, 5)

// classifyByLLM delegates to internal/dispatch.ClassifyByLLM, wiring
// runSingleTask+routeSemGlobal as the TaskExecutor.
func classifyByLLM(ctx context.Context, cfg *Config, prompt string) (*RouteResult, error) {
	return dtypes.ClassifyByLLM(ctx, cfg, prompt, routeExecutor(cfg))
}

// parseLLMRouteResult delegates to internal/dispatch.ParseLLMRouteResult.
func parseLLMRouteResult(output, defaultAgent string) (*RouteResult, error) {
	return dtypes.ParseLLMRouteResult(output, defaultAgent)
}

// --- Multi-Tier Route ---

// routeTask delegates to internal/dispatch.RouteTask, wiring runSingleTask as the executor.
func routeTask(ctx context.Context, cfg *Config, req RouteRequest) *RouteResult {
	return dtypes.RouteTask(ctx, cfg, req, routeExecutor(cfg))
}

// routeExecutor returns a TaskExecutor backed by runSingleTask + routeSemGlobal.
func routeExecutor(cfg *Config) dtypes.TaskExecutor {
	return dtypes.TaskExecutorFunc(func(ctx context.Context, task dtypes.Task, agentName string) dtypes.TaskResult {
		return runSingleTask(ctx, cfg, task, routeSemGlobal, nil, agentName)
	})
}

// --- Full Smart Dispatch Pipeline ---

// smartDispatch is the full pipeline: route → dispatch → memory → review → audit.
func smartDispatch(ctx context.Context, cfg *Config, prompt string, source string,
	state *dispatchState, sem, childSem chan struct{}) *SmartDispatchResult {

	// Publish task_received to dashboard.
	if state != nil && state.broker != nil {
		state.broker.Publish(SSEDashboardKey, SSEEvent{
			Type: SSETaskReceived,
			Data: map[string]any{
				"source": source,
				"prompt": truncate(prompt, 200),
			},
		})
	}

	// Step 1: Route.
	route := routeTask(ctx, cfg, RouteRequest{Prompt: prompt, Source: source})

	log.InfoCtx(ctx, "route decision",
		"prompt", truncate(prompt, 60), "role", route.Agent,
		"method", route.Method, "confidence", route.Confidence)

	// Publish task_routing to dashboard.
	if state != nil && state.broker != nil {
		state.broker.Publish(SSEDashboardKey, SSEEvent{
			Type: SSETaskRouting,
			Data: map[string]any{
				"source":     source,
				"role":       route.Agent,
				"method":     route.Method,
				"confidence": route.Confidence,
			},
		})
	}

	// Step 2: Build and run task with the selected agent.
	task := Task{
		Prompt: prompt,
		Agent:  route.Agent,
		Source: "route:" + source,
	}
	fillDefaults(cfg, &task)

	// Inject agent soul prompt + model + permission mode.
	if route.Agent != "" {
		if soulPrompt, err := loadAgentPrompt(cfg, route.Agent); err == nil && soulPrompt != "" {
			task.SystemPrompt = soulPrompt
		}
		if rc, ok := cfg.Agents[route.Agent]; ok {
			if rc.Model != "" {
				task.Model = rc.Model
			}
			if rc.PermissionMode != "" {
				task.PermissionMode = rc.PermissionMode
			}
		}
	}

	// Expand template variables.
	task.Prompt = expandPrompt(task.Prompt, "", cfg.HistoryDB, route.Agent, cfg.KnowledgeDir, cfg)

	// Step 3: Execute with optional Dev↔QA retry loop.
	taskStart := time.Now()
	var result TaskResult
	var totalCost float64
	var qaApproved bool
	var attempts int

	if cfg.SmartDispatch.ReviewLoop {
		// Dev↔QA retry loop: execute → review → retry with feedback (max N retries).
		loopResult := routeDevQALoop(ctx, cfg, task, prompt, route.Agent, sem, childSem)
		result = loopResult.Result
		totalCost = loopResult.TotalCost
		qaApproved = loopResult.QAApproved
		attempts = loopResult.Attempts
	} else {
		result = runSingleTask(ctx, cfg, task, sem, childSem, route.Agent)
		totalCost = result.CostUSD
		attempts = 1
	}

	// Record to history.
	recordHistory(cfg.HistoryDB, task.ID, task.Name, task.Source, route.Agent, task, result,
		taskStart.Format(time.RFC3339), time.Now().Format(time.RFC3339), result.OutputFile)

	// Record session activity.
	recordSessionActivity(cfg.HistoryDB, task, result, route.Agent)

	// Step 4: Store output summary in agent memory.
	if result.Status == "success" {
		setMemory(cfg, route.Agent, "last_route_output", truncate(result.Output, 500))
		setMemory(cfg, route.Agent, "last_route_prompt", truncate(prompt, 200))
		setMemory(cfg, route.Agent, "last_route_time", time.Now().Format(time.RFC3339))
	}

	sdr := &SmartDispatchResult{
		Route:    *route,
		Task:     result,
		Attempts: attempts,
	}

	// Use accumulated cost from all attempts.
	if totalCost > result.CostUSD {
		sdr.Task.CostUSD = totalCost
	}

	// Step 5: Review gate.
	if cfg.SmartDispatch.ReviewLoop {
		// Dev↔QA loop already handled review — propagate the result.
		sdr.ReviewOK = &qaApproved
		if !qaApproved && attempts > 1 {
			sdr.Review = fmt.Sprintf("Dev↔QA loop exhausted (%d attempts)", attempts)
		}
	} else if shouldReview(cfg, route, result.CostUSD) && result.Status == "success" {
		// Single-pass review (original behavior).
		reviewOK, reviewComment := reviewOutput(ctx, cfg, prompt, result.Output, route.Agent, sem, childSem)
		sdr.ReviewOK = &reviewOK
		sdr.Review = reviewComment
	}

	// Step 6: Audit log.
	audit.Log(cfg.HistoryDB, "route.dispatch", source,
		fmt.Sprintf("role=%s method=%s confidence=%s attempts=%d prompt=%s",
			route.Agent, route.Method, route.Confidence, attempts, truncate(prompt, 100)), "")

	// Webhook notifications.
	sendWebhooks(cfg, result.Status, webhook.Payload{
		JobID:    task.ID,
		Name:     task.Name,
		Source:   task.Source,
		Status:   result.Status,
		Cost:     totalCost,
		Duration: result.DurationMs,
		Model:    result.Model,
		Output:   truncate(result.Output, 500),
		Error:    truncate(result.Error, 300),
	})

	return sdr
}

// --- Route Dev↔QA Loop ---

// routeDevQALoop runs the Dev↔QA retry loop for smart dispatch.
// Unlike the taskboard version, this operates without a TaskBoard record.
//
// Flow: Dev execute → QA review → (pass → done) | (fail → record failure → inject feedback → retry)
func routeDevQALoop(ctx context.Context, cfg *Config, task Task, originalPrompt, agentName string, sem, childSem chan struct{}) devQALoopResult {
	maxRetries := cfg.SmartDispatch.MaxRetriesOrDefault() // default 3

	var accumulated float64

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Step 1: Dev execution.
		result := runSingleTask(ctx, cfg, task, sem, childSem, agentName)
		accumulated += result.CostUSD

		// If execution itself failed, exit loop immediately.
		if result.Status != "success" || strings.TrimSpace(result.Output) == "" {
			return devQALoopResult{Result: result, Attempts: attempt + 1, TotalCost: accumulated}
		}

		// Step 2: QA review.
		reviewOK, reviewComment := reviewOutput(ctx, cfg, originalPrompt, result.Output, agentName, sem, childSem)
		if reviewOK {
			log.InfoCtx(ctx, "routeDevQA: review passed", "agent", agentName, "attempt", attempt+1)
			return devQALoopResult{Result: result, QAApproved: true, Attempts: attempt + 1, TotalCost: accumulated}
		}

		// QA failed.
		log.InfoCtx(ctx, "routeDevQA: review failed, injecting feedback",
			"agent", agentName, "attempt", attempt+1, "maxAttempts", maxRetries+1,
			"comment", truncate(reviewComment, 200))

		// Record QA rejection as skill failure for future context injection.
		qaFailMsg := fmt.Sprintf("[QA rejection attempt %d] %s", attempt+1, reviewComment)
		skills := selectSkills(cfg, task)
		for _, s := range skills {
			appendSkillFailure(cfg, s.Name, task.Name, agentName, qaFailMsg)
		}

		if attempt == maxRetries {
			log.WarnCtx(ctx, "routeDevQA: max retries exhausted, escalating",
				"agent", agentName, "attempts", maxRetries+1)
			return devQALoopResult{Result: result, Attempts: attempt + 1, TotalCost: accumulated}
		}

		// Step 3: Rebuild prompt with failure context + QA feedback for retry.
		task.Prompt = originalPrompt

		// Inject accumulated skill failures.
		for _, s := range skills {
			failures := loadSkillFailuresByName(cfg, s.Name)
			if failures != "" {
				task.Prompt += fmt.Sprintf("\n\n<skill-failures name=\"%s\">\n%s\n</skill-failures>", s.Name, failures)
			}
		}

		// Inject QA reviewer's specific feedback.
		task.Prompt += fmt.Sprintf("\n\n## QA Review Feedback (Attempt %d)\n", attempt+1)
		task.Prompt += "The QA reviewer rejected the output. Issues found:\n"
		task.Prompt += reviewComment
		task.Prompt += fmt.Sprintf("\n\nAddress ALL issues above. This is retry %d of %d.\n", attempt+2, maxRetries+1)

		// Fresh session for retry.
		task.ID = newUUID()
		task.SessionID = newUUID()
	}

	return devQALoopResult{}
}

// --- Coordinator Review ---

// reviewOutput asks the review agent (or coordinator) to review the agent's output.
func reviewOutput(ctx context.Context, cfg *Config, originalPrompt, output, agentRole string, sem, childSem chan struct{}) (bool, string) {
	// Use dedicated review agent if configured, otherwise fall back to coordinator.
	reviewer := cfg.SmartDispatch.Coordinator
	if cfg.SmartDispatch.ReviewAgent != "" {
		reviewer = cfg.SmartDispatch.ReviewAgent
	}

	reviewPrompt := fmt.Sprintf(
		`Review this agent output for quality and correctness.

Original request: %s

Agent (%s) output:
%s

Reply with ONLY a JSON object:
{"ok":true,"comment":"brief comment"} or {"ok":false,"comment":"what's wrong and what evidence is missing"}`,
		truncate(originalPrompt, 300),
		agentRole,
		truncate(output, 2000),
	)

	task := Task{
		Prompt:  reviewPrompt,
		Timeout: cfg.SmartDispatch.ClassifyTimeout,
		Budget:  cfg.SmartDispatch.ReviewBudget,
		Source:  "route-review",
	}
	fillDefaults(cfg, &task)

	// Inject review agent's SOUL prompt and model.
	if soulPrompt, err := loadAgentPrompt(cfg, reviewer); err == nil && soulPrompt != "" {
		task.SystemPrompt = soulPrompt
	}
	if rc, ok := cfg.Agents[reviewer]; ok {
		if rc.Model != "" {
			task.Model = rc.Model
		}
		if rc.PermissionMode != "" {
			task.PermissionMode = rc.PermissionMode
		}
	}

	result := runSingleTask(ctx, cfg, task, sem, childSem, reviewer)
	if result.Status != "success" {
		return true, "review skipped (error)"
	}

	// Parse review JSON.
	start := strings.Index(result.Output, "{")
	end := strings.LastIndex(result.Output, "}")
	if start >= 0 && end > start {
		var review struct {
			OK      bool   `json:"ok"`
			Comment string `json:"comment"`
		}
		if json.Unmarshal([]byte(result.Output[start:end+1]), &review) == nil {
			return review.OK, review.Comment
		}
	}

	return true, "review parse error"
}

// --- Conditional Review Trigger ---

// shouldReview delegates to internal/dispatch.ShouldReview.
func shouldReview(cfg *Config, routeResult *RouteResult, taskCost float64) bool {
	return dtypes.ShouldReview(cfg, routeResult, taskCost)
}

// ============================================================
// Merged from dispatch_tools.go
// ============================================================

// safeToolExec wraps tool execution with panic recovery.
func safeToolExec(ctx context.Context, cfg *Config, tool *ToolDef, input json.RawMessage) (output string, err error) {
	defer func() {
		if rv := recover(); rv != nil {
			err = fmt.Errorf("tool %q panicked: %v", tool.Name, rv)
			log.Error("tool panic recovered", "tool", tool.Name, "panic", fmt.Sprintf("%v", rv))
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
	if cfg.Runtime.ToolRegistry == nil {
		return executeWithProvider(ctx, cfg, task, agentName, registry, eventCh)
	}

	// Resolve provider.
	providerName := resolveProviderName(cfg, task, agentName)
	p, err := registry.Get(providerName)
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
	complexity := classify.Classify(task.Prompt, task.Source)
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
	tools := cfg.Runtime.ToolRegistry.(*ToolRegistry).ListFiltered(allowed)
	if len(tools) == 0 {
		// No tools available, use regular execution.
		return executeWithProvider(ctx, cfg, task, agentName, registry, eventCh)
	}

	// Build initial request.
	req := buildProviderRequest(cfg, task, agentName, providerName, eventCh)
	// Convert []*ToolDef to []provider.ToolDef for the provider request.
	providerTools := make([]provider.ToolDef, len(tools))
	for i, t := range tools {
		providerTools[i] = provider.ToolDef{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		}
	}
	req.Tools = providerTools

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
		// Check context deadline before each iteration.
		if ctx.Err() != nil {
			finalResult = &ProviderResult{
				Output: "[stopped: task deadline exceeded]",
			}
			break
		}

		req.Messages = messages

		// P27.3: Send typing indicator at iteration start.
		if cfg.StreamToChannels && task.ChannelNotifier != nil {
			go task.ChannelNotifier.SendTyping(ctx)
		}

		// Call provider.
		result, execErr := toolProvider.ExecuteWithTools(ctx, req)
		if execErr != nil {
			// If context was cancelled, treat as deadline rather than hard error.
			if ctx.Err() != nil {
				finalResult = &ProviderResult{
					Output: "[stopped: task deadline exceeded]",
				}
				break
			}
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
				// Extract a one-line preview from the tool input.
				var preview string
				if len(tc.Input) > 0 {
					var inputMap map[string]any
					if err := json.Unmarshal(tc.Input, &inputMap); err == nil {
						if desc, ok := inputMap["description"].(string); ok && desc != "" {
							preview = desc
						} else if cmd, ok := inputMap["command"].(string); ok && cmd != "" {
							if idx := strings.Index(cmd, "\n"); idx != -1 {
								preview = cmd[:idx]
							} else {
								preview = cmd
							}
						}
					}
				}
				broker.PublishMulti([]string{task.ID, task.SessionID, SSEDashboardKey}, SSEEvent{
					Type:      "tool_call",
					TaskID:    task.ID,
					SessionID: task.SessionID,
					Data: map[string]any{
						"id":      tc.ID,
						"name":    tc.Name,
						"preview": preview,
					},
				})
			}
		}

		// Execute tools.
		toolResults := make([]ToolResult, 0, len(result.ToolCalls))
		for _, tc := range result.ToolCalls {
			// Check tool policy - is tool allowed for this agent?
			if task.Agent != "" && !isToolAllowed(cfg, task.Agent, tc.Name) {
				log.WarnCtx(ctx, "tool call blocked by policy", "tool", tc.Name, "agent", task.Agent)
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
				log.WarnCtx(ctx, "tool loop detected", "tool", tc.Name, "msg", loopMsg)
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
					log.WarnCtx(ctx, "tool pattern detected", "msg", patternMsg)
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
			rootTC := ToolCall{ID: tc.ID, Name: tc.Name, Input: tc.Input}
			if mockResult, shouldExec := filterToolCall(cfg, task.Agent, rootTC); !shouldExec {
				// Tool call filtered by trust level (observe or suggest mode).
				toolResults = append(toolResults, *mockResult)
				continue
			}

			// P28.0: Pre-execution approval gate.
			if needsApproval(cfg, tc.Name) && task.ApprovalGate != nil && !task.ApprovalGate.IsAutoApproved(tc.Name) {
				approved, gateErr := requestToolApproval(ctx, cfg, task, rootTC)
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
			tool, ok := cfg.Runtime.ToolRegistry.(*ToolRegistry).Get(tc.Name)
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
			if cfg.StreamToChannels && task.ChannelNotifier != nil {
				statusMsg := fmt.Sprintf("%s: done (%dms)", tc.Name, toolDuration.Milliseconds())
				go task.ChannelNotifier.SendStatus(ctx, statusMsg)
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

		// --- Mid-loop budget + context + deadline checks ---

		// Context deadline check: stop if task timeout has expired.
		if ctx.Err() != nil {
			finalResult = &ProviderResult{
				Output: result.Output + "\n[stopped: task deadline exceeded]",
			}
			break
		}

		// Per-task budget soft limit: log once for analysis, then continue.
		if task.Budget > 0 && totalCostUSD >= task.Budget && !taskBudgetWarnLogged {
			taskBudgetWarnLogged = true
			log.WarnCtx(ctx, "task budget soft-limit exceeded (continuing)",
				"budget", task.Budget,
				"spent", totalCostUSD,
				"role", task.Agent,
				"task_id", task.ID,
				"task_prompt_preview", task.Prompt[:min(120, len(task.Prompt))],
			)
		}

		// Global budget check.
		if br := cost.CheckBudget(cfg.Budgets, cfg.HistoryDB, agentName, "", 0); br != nil && !br.Allowed {
			log.WarnCtx(ctx, "global budget exceeded mid-loop", "msg", br.Message)
			finalResult = &ProviderResult{
				Output:  result.Output + "\n[stopped: global budget exceeded]",
				IsError: true,
				Error:   "global budget exceeded",
			}
			break
		}

		// Pre-send token estimation: compress old messages if nearing context window.
		ctxWindow := estimate.ContextWindow(req.Model)
		threshold := ctxWindow * 80 / 100
		req.Messages = messages // update for estimation
		estTokens := estimateRequestTokens(req)
		if estTokens > threshold {
			// Try compression first before stopping.
			messages = compressMessages(messages, 3)
			req.Messages = messages
			estTokens = estimateRequestTokens(req)
			if estTokens > threshold {
				log.WarnCtx(ctx, "context window limit after compression", "estimatedTokens", estTokens, "threshold", threshold)
				finalResult = &ProviderResult{
					Output:  result.Output + "\n[stopped: context limit reached]",
					IsError: true,
					Error:   "context window limit reached",
				}
				break
			}
			log.InfoCtx(ctx, "compressed old messages to fit context window", "estimatedTokens", estTokens, "threshold", threshold)
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
	workspace.InjectContent(cfg, &task.SystemPrompt, &task.AddDirs, agentName)
}

// estimateDirSize returns the total size of all files (non-recursive) in a directory.
func estimateDirSize(dir string) int {
	return workspace.DirSize(dir)
}

