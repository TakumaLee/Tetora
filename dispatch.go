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
	"tetora/internal/history"
	"tetora/internal/log"
	"tetora/internal/sandbox"
	"tetora/internal/telemetry"
	"tetora/internal/trace"
	"tetora/internal/webhook"
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

