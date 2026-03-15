package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"tetora/internal/audit"
	"tetora/internal/db"
	"tetora/internal/classify"
	"tetora/internal/config"
	"tetora/internal/cost"
	dtypes "tetora/internal/dispatch"
	"tetora/internal/discord"
	"tetora/internal/estimate"
	"tetora/internal/history"
	"tetora/internal/hooks"
	"tetora/internal/log"
	"tetora/internal/provider"
	"tetora/internal/sandbox"
	"tetora/internal/taskboard"
	"tetora/internal/telemetry"
	"tetora/internal/trace"
	"tetora/internal/webhook"
	"tetora/internal/workspace"
	"tetora/internal/worktree"
)
// --- Type Aliases (canonical definitions in internal/dispatch) ---

type ChannelNotifier = dtypes.ChannelNotifier
type Task = dtypes.Task
type TaskResult = dtypes.TaskResult
type DispatchResult = dtypes.DispatchResult
type CompletionStatus = dtypes.CompletionStatus

const (
	StatusDone             = dtypes.StatusDone
	StatusDoneWithConcerns = dtypes.StatusDoneWithConcerns
	StatusBlocked          = dtypes.StatusBlocked
	StatusNeedsContext     = dtypes.StatusNeedsContext
)

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

// --- Multi-tenant dispatch manager ---

// dispatchManager manages per-client dispatch state, semaphores, and SSE brokers.
// Each client gets its own isolated dispatchState and concurrency limits.
type dispatchManager struct {
	mu            sync.RWMutex
	states        map[string]*dispatchState
	semaphores    map[string]chan struct{}
	childSems     map[string]chan struct{}
	maxConcurrent int
	childSemSize  int
}

func newDispatchManager(maxConcurrent, childSemSize int) *dispatchManager {
	return &dispatchManager{
		states:        make(map[string]*dispatchState),
		semaphores:    make(map[string]chan struct{}),
		childSems:     make(map[string]chan struct{}),
		maxConcurrent: maxConcurrent,
		childSemSize:  childSemSize,
	}
}

// getOrCreate returns the dispatch state, main semaphore, and child semaphore
// for the given client ID. Creates them lazily if they don't exist.
func (dm *dispatchManager) getOrCreate(clientID string) (*dispatchState, chan struct{}, chan struct{}) {
	dm.mu.RLock()
	state, ok := dm.states[clientID]
	if ok {
		sem := dm.semaphores[clientID]
		childSem := dm.childSems[clientID]
		dm.mu.RUnlock()
		return state, sem, childSem
	}
	dm.mu.RUnlock()

	dm.mu.Lock()
	defer dm.mu.Unlock()
	// Double-checked locking.
	if state, ok = dm.states[clientID]; ok {
		return state, dm.semaphores[clientID], dm.childSems[clientID]
	}
	state = newDispatchState()
	state.broker = newSSEBroker()
	sem := make(chan struct{}, dm.maxConcurrent)
	childSem := make(chan struct{}, dm.childSemSize)
	dm.states[clientID] = state
	dm.semaphores[clientID] = sem
	dm.childSems[clientID] = childSem
	return state, sem, childSem
}

// register pre-registers a client with an existing state and semaphores.
// Used to register the default client with the Server's existing state.
func (dm *dispatchManager) register(clientID string, state *dispatchState, sem, childSem chan struct{}) {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	dm.states[clientID] = state
	dm.semaphores[clientID] = sem
	dm.childSems[clientID] = childSem
}

// allStates returns a snapshot of all client states (for admin/monitoring).
func (dm *dispatchManager) allStates() map[string]*dispatchState {
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	snapshot := make(map[string]*dispatchState, len(dm.states))
	for k, v := range dm.states {
		snapshot[k] = v
	}
	return snapshot
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

	// --- RDD Engine: Check for /rdd resume ---
	applyRDDResume(cfg, &task)

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
	if budgetResult := cost.CheckBudget(cfg.Budgets, historyDBForTask(cfg, task), agentName, "", 0); budgetResult != nil && !budgetResult.Allowed {
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

	if cfg.Runtime.ProviderRegistry == nil {
		if eventCh != nil {
			close(eventCh)
		}
		return TaskResult{
			ID:     task.ID,
			Name:   task.Name,
			Status: "error",
			Output: "provider registry not initialized",
		}
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

	// Guard: errors must always have a non-empty message for diagnosability.
	if result.Status == "error" && result.Error == "" {
		result.Error = fmt.Sprintf("unknown error (provider=%s, model=%s, duration=%dms)",
			result.Provider, result.Model, result.DurationMs)
	}

	// Parse agent completion status from structured markers in output.
	if result.Status == "success" {
		result.CompletionStat, result.Concerns, result.BlockedReason = parseCompletionStatus(result.Output)
	}

	// Offline queue: if all providers are unavailable, enqueue for later retry.
	if result.Status == "error" && isAllProvidersUnavailable(result.Error) && cfg.OfflineQueue.Enabled {
		if !isQueueFull(historyDBForTask(cfg, task), cfg.OfflineQueue.MaxItemsOrDefault()) {
			if err := enqueueTask(historyDBForTask(cfg, task), task, agentName, 0); err == nil {
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
	go telemetry.Record(historyDBForTask(cfg, task), telemetry.Entry{
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

	// Save output to file (per-client dir when tenant isolation is active).
	if pr.Output != "" {
		result.OutputFile = saveTaskOutput(cfg.OutputsDirFor(task.ClientID), task.ID, []byte(pr.Output))
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
				"name":       task.Name,
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

	// --- RDD Engine: Check for /rdd resume ---
	applyRDDResume(cfg, &task)

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
	if budgetResult := cost.CheckBudget(cfg.Budgets, historyDBForTask(cfg, task), agentName, "", 0); budgetResult != nil && !budgetResult.Allowed {
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

	// --- Retry loop for direct dispatch path ---
	// Reuses SmartDispatch.MaxRetries (default 3) for consistency with taskboard.
	maxRetries := cfg.SmartDispatch.MaxRetriesOrDefault()
	var (
		pr              *ProviderResult
		result          TaskResult
		totalCost       float64
		totalTokensIn   int
		totalTokensOut  int
		attemptErrors   []string
	)
	totalStart := time.Now()

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Backoff + context check before retries.
		if attempt > 0 {
			if taskCtx.Err() != nil || ctx.Err() != nil {
				break
			}
			backoff := time.Duration(attempt) * 2 * time.Second
			select {
			case <-time.After(backoff):
			case <-taskCtx.Done():
			}
			if taskCtx.Err() != nil || ctx.Err() != nil {
				break
			}
			log.InfoCtx(ctx, "retrying task",
				"taskId", task.ID[:8], "attempt", attempt+1, "maxRetries", maxRetries)
			state.publishSSE(SSEEvent{
				Type:      "retry",
				TaskID:    task.ID,
				SessionID: task.SessionID,
				Data: map[string]any{
					"attempt":    attempt + 1,
					"maxRetries": maxRetries,
					"lastError":  result.Error,
				},
			})
		}

		// Reuse complexity from tiered prompt builder for tool trimming.
		if complexity == classify.Simple {
			pr = executeWithProvider(taskCtx, cfg, task, agentName, cfg.Runtime.ProviderRegistry.(*providerRegistry), eventCh)
		} else {
			pr = executeWithProviderAndTools(taskCtx, cfg, task, agentName, cfg.Runtime.ProviderRegistry.(*providerRegistry), eventCh, state.broker)
		}
		totalCost += pr.CostUSD
		totalTokensIn += pr.TokensIn
		totalTokensOut += pr.TokensOut

		result = TaskResult{
			ID:         task.ID,
			Name:       task.Name,
			Output:     pr.Output,
			CostUSD:    totalCost,
			DurationMs: time.Since(totalStart).Milliseconds(),
			Model:      task.Model,
			SessionID:  pr.SessionID,
			TokensIn:   totalTokensIn,
			TokensOut:  totalTokensOut,
			ProviderMs: pr.ProviderMs,
			Provider:   pr.Provider,
			Agent:      agentName,
			Attempts:   attempt + 1,
		}
		if result.SessionID == "" {
			result.SessionID = task.SessionID
		}

		if taskCtx.Err() == context.DeadlineExceeded {
			result.Status = "timeout"
			result.Error = fmt.Sprintf("timed out after %v", timeout)
			break // Don't retry timeouts.
		} else if ctx.Err() != nil {
			result.Status = "cancelled"
			result.Error = "dispatch cancelled"
			break // Don't retry cancellations.
		} else if pr.IsError {
			result.Status = "error"
			result.Error = pr.Error
			attemptErrors = append(attemptErrors, fmt.Sprintf("attempt %d: %s", attempt+1, pr.Error))
			if attempt == maxRetries {
				// Max retries exhausted — escalate.
				result.Status = "escalated"
				result.Output = formatEscalationReport(task.Name, attemptErrors, result.Error)
				log.WarnCtx(ctx, "task escalated after max retries",
					"taskId", task.ID[:8], "attempts", attempt+1)
			}
			continue
		} else {
			result.Status = "success"
			break
		}
	}

	if eventCh != nil {
		close(eventCh)
	}

	// Offline queue: if all providers are unavailable, enqueue for later retry.
	if result.Status == "error" && isAllProvidersUnavailable(result.Error) && cfg.OfflineQueue.Enabled {
		if !isQueueFull(historyDBForTask(cfg, task), cfg.OfflineQueue.MaxItemsOrDefault()) {
			if err := enqueueTask(historyDBForTask(cfg, task), task, agentName, 0); err == nil {
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
		"durationMs", result.DurationMs,
		"cost", result.CostUSD,
		"tokensIn", result.TokensIn, "tokensOut", result.TokensOut,
		"status", result.Status)

	// Record token telemetry (async).
	go telemetry.Record(historyDBForTask(cfg, task), telemetry.Entry{
		TaskID:             task.ID,
		Agent:               agentName,
		Complexity:         complexity.String(),
		Provider:           pr.Provider,
		Model:              task.Model,
		SystemPromptTokens: len(task.SystemPrompt) / 4,
		ContextTokens:      len(task.Prompt) / 4,
		ToolDefsTokens:     0,
		InputTokens:        totalTokensIn,
		OutputTokens:       totalTokensOut,
		CostUSD:            totalCost,
		DurationMs:         result.DurationMs,
		Source:             task.Source,
		CreatedAt:          time.Now().Format(time.RFC3339),
	})

	// Save output to file (per-client dir when tenant isolation is active).
	if pr.Output != "" {
		result.OutputFile = saveTaskOutput(cfg.OutputsDirFor(task.ClientID), task.ID, []byte(pr.Output))
	}

	// Record to history DB (per-tenant aware).
	taskDB := historyDBForTask(cfg, task)
	recordHistory(taskDB, task.ID, task.Name, task.Source, task.Agent, task, result,
		totalStart.Format(time.RFC3339), time.Now().Format(time.RFC3339), result.OutputFile)

	// Record session activity (skip for sources that manage their own sessions:
	// "chat" → HTTP handler, "route:" → discord/telegram executeRoute).
	if !strings.HasPrefix(task.Source, "chat") && !strings.HasPrefix(task.Source, "route:") {
		recordSessionActivity(taskDB, task, result, task.Agent)
	}
	// Log to system dispatch log (skip only for chat — already handled there).
	if !strings.HasPrefix(task.Source, "chat") {
		logSystemDispatch(taskDB, task, result, task.Agent)
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
				"name":       task.Name,
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
			// Time savings: estimate manual duration and record AI actual time.
			hdb := historyDBForTask(cfg, task)
			taskType := resolveTaskType(hdb, task.Name)
			ref.EstimatedManualDurationSec = estimateManualDuration(taskType, ref.Score)
			ref.AIDurationSec = int(result.DurationMs / 1000)
			if err := storeReflection(hdb, ref); err != nil {
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

	// Record to history (per-tenant aware).
	retryDB := historyDBForTask(cfg, task)
	start := time.Now().Add(-time.Duration(result.DurationMs) * time.Millisecond)
	recordHistory(retryDB, task.ID, task.Name, task.Source, task.Agent, task, result,
		start.Format(time.RFC3339), time.Now().Format(time.RFC3339), result.OutputFile)

	// Record session activity.
	recordSessionActivity(retryDB, task, result, task.Agent)
	logSystemDispatch(retryDB, task, result, task.Agent)

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

	audit.Log(historyDBForTask(cfg, task), "task.retry", task.Source,
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

	audit.Log(historyDBForTask(cfg, ft.task), "task.reroute", "reroute",
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

// formatEscalationReport produces a structured escalation report
// consistent with the gstack STATUS/REASON/ATTEMPTED/RECOMMENDATION format.
func formatEscalationReport(taskName string, attemptErrors []string, lastError string) string {
	var b strings.Builder
	b.WriteString("STATUS: BLOCKED\n")
	b.WriteString(fmt.Sprintf("REASON: %s\n", lastError))
	b.WriteString("ATTEMPTED:\n")
	for _, e := range attemptErrors {
		b.WriteString(fmt.Sprintf("  - %s\n", e))
	}
	b.WriteString(fmt.Sprintf("RECOMMENDATION: Task %q failed after %d attempts. Manual intervention required.\n",
		taskName, len(attemptErrors)))
	return b.String()
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

// applyRDDResume checks if the task prompt is "/rdd resume".
// If so, it attempts to load the STATE.md context to replace the prompt.
func applyRDDResume(cfg *Config, task *Task) {
	if !cfg.RDDEngine.Enabled {
		return
	}
	if strings.TrimSpace(task.Prompt) != "/rdd resume" {
		return
	}
	if task.Workdir == "" {
		task.Prompt = "Error: Cannot resume without a working directory (Workdir is empty)."
		return
	}

	stateFileName := cfg.RDDEngine.stateFileNameOrDefault()
	resumeCtx, err := BuildResumeContext(task.Workdir, stateFileName)
	if err != nil {
		task.Prompt = fmt.Sprintf("Error reading resume context: %v", err)
		return
	}
	
	task.Prompt = resumeCtx
	logInfo("RDD resume activated", "workdir", task.Workdir, "stateFile", stateFileName)
}


