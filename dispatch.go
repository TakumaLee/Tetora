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
	"unsafe"
)

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
	Role           string   `json:"role,omitempty"`    // role name for smart dispatch
	Source         string   `json:"source,omitempty"`  // "dispatch", "cron", "ask", "route:*"
	TraceID        string   `json:"traceId,omitempty"` // trace ID for request correlation
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
	cancel      context.CancelFunc
	broker      *sseBroker // SSE event broker for streaming progress
}

type taskState struct {
	task     Task
	startAt  time.Time
	cmd      *exec.Cmd
	cancelFn context.CancelFunc
}

func newDispatchState() *dispatchState {
	return &dispatchState{
		running:     make(map[string]*taskState),
		failedTasks: make(map[string]*failedTask),
	}
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
	}

	status := "idle"
	if s.active {
		status = "dispatching"
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
			ID:      ts.task.ID,
			Name:    ts.task.Name,
			Status:  "running",
			Elapsed: time.Since(ts.startAt).Round(time.Second).String(),
			Model:   ts.task.Model,
			Timeout: ts.task.Timeout,
			Prompt:  prompt,
			PID:     pid,
			Source:  ts.task.Source,
		})
	}
	for _, r := range s.finished {
		tasks = append(tasks, taskStatus{
			ID: r.ID, Name: r.Name, Status: r.Status,
			Duration: (time.Duration(r.DurationMs) * time.Millisecond).Round(time.Second).String(),
			CostUSD:  r.CostUSD,
			Model:    r.Model,
		})
	}

	out := map[string]any{
		"status":    status,
		"running":   len(s.running),
		"completed": len(s.finished),
		"tasks":     tasks,
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
		t.Timeout = cfg.DefaultTimeout
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

// --- Directory Validation ---

// validateDirs checks that the task's workdir and addDirs are within allowed directories.
// If allowedDirs is empty, no restriction is applied (backward compatible).
// Role-level allowedDirs takes precedence over config-level.
func validateDirs(cfg *Config, task Task, roleName string) error {
	// Determine which allowedDirs to use.
	var allowed []string
	if roleName != "" {
		if rc, ok := cfg.Roles[roleName]; ok && len(rc.AllowedDirs) > 0 {
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
func runSingleTask(ctx context.Context, cfg *Config, task Task, sem chan struct{}, roleName string) TaskResult {
	// Apply trust level.
	applyTrustToTask(cfg, &task, roleName)

	// Validate directories before running.
	if err := validateDirs(cfg, task, roleName); err != nil {
		return TaskResult{
			ID: task.ID, Name: task.Name, Status: "error",
			Error: err.Error(), Model: task.Model, SessionID: task.SessionID,
		}
	}

	sem <- struct{}{}
	defer func() { <-sem }()

	// Auto-inject knowledge dir if it has files.
	if cfg.KnowledgeDir != "" && knowledgeDirHasFiles(cfg.KnowledgeDir) {
		task.AddDirs = append(task.AddDirs, cfg.KnowledgeDir)
	}

	// Budget check before execution.
	if budgetResult := checkBudget(cfg, roleName, "", 0); budgetResult != nil && !budgetResult.Allowed {
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

	providerName := resolveProviderName(cfg, task, roleName)

	logDebugCtx(ctx, "task start",
		"taskId", task.ID[:8], "name", task.Name,
		"model", task.Model, "provider", providerName,
		"workdir", task.Workdir, "source", task.Source)

	timeout, err := time.ParseDuration(task.Timeout)
	if err != nil {
		timeout = 15 * time.Minute
	}
	taskCtx, taskCancel := context.WithTimeout(ctx, timeout)
	defer taskCancel()

	start := time.Now()
	pr := executeWithProvider(taskCtx, cfg, task, roleName, cfg.registry, nil)
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
			if err := enqueueTask(cfg.HistoryDB, task, roleName, 0); err == nil {
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

	// Save output to file.
	if pr.Output != "" {
		result.OutputFile = saveTaskOutput(cfg.baseDir, task.ID, []byte(pr.Output))
	}

	// Note: history recording for runSingleTask is handled by the caller (cron.go).

	return result
}

func runTask(ctx context.Context, cfg *Config, task Task, state *dispatchState) TaskResult {
	// Propagate trace ID from context to task.
	if task.TraceID == "" {
		task.TraceID = traceIDFromContext(ctx)
	}

	roleName := task.Role

	// Apply workspace configuration if role is set.
	if roleName != "" {
		// Use workspace soul file if available, otherwise fallback to legacy loadRolePrompt.
		soulPrompt := loadSoulFile(cfg, roleName)
		if soulPrompt == "" {
			if sp, err := loadRolePrompt(cfg, roleName); err == nil {
				soulPrompt = sp
			}
		}
		if soulPrompt != "" {
			task.SystemPrompt = soulPrompt
		}

		// Apply workspace directory as workdir if task doesn't specify one.
		ws := resolveWorkspace(cfg, roleName)
		if task.Workdir == cfg.DefaultWorkdir && ws.Dir != "" {
			task.Workdir = ws.Dir
		}

		// Apply role config overrides.
		if rc, ok := cfg.Roles[roleName]; ok {
			if task.Model == cfg.DefaultModel && rc.Model != "" {
				task.Model = rc.Model
			}
			if task.PermissionMode == cfg.DefaultPermissionMode && rc.PermissionMode != "" {
				task.PermissionMode = rc.PermissionMode
			}
		}
	}

	// Auto-inject knowledge dir if it has files.
	if cfg.KnowledgeDir != "" && knowledgeDirHasFiles(cfg.KnowledgeDir) {
		task.AddDirs = append(task.AddDirs, cfg.KnowledgeDir)
	}

	// Apply trust level (may override permissionMode for observe mode).
	trustLevel, _ := applyTrustToTask(cfg, &task, roleName)
	if trustLevel == TrustObserve {
		logDebugCtx(ctx, "trust: observe mode, forcing plan permission", "role", roleName)
	}

	// Inject reflection context from past self-assessments.
	if cfg.Reflection.Enabled && roleName != "" && cfg.HistoryDB != "" {
		if refCtx := buildReflectionContext(cfg.HistoryDB, roleName, 3); refCtx != "" {
			task.SystemPrompt = task.SystemPrompt + "\n\n" + refCtx
		}
	}

	// Validate directories before running.
	if err := validateDirs(cfg, task, roleName); err != nil {
		return TaskResult{
			ID: task.ID, Name: task.Name, Status: "error",
			Error: err.Error(), Model: task.Model, SessionID: task.SessionID,
		}
	}

	timeout, err := time.ParseDuration(task.Timeout)
	if err != nil {
		timeout = 15 * time.Minute
	}
	taskCtx, taskCancel := context.WithTimeout(ctx, timeout)
	defer taskCancel()

	ts := &taskState{task: task, startAt: time.Now(), cancelFn: taskCancel}
	state.mu.Lock()
	state.running[task.ID] = ts
	state.mu.Unlock()

	// Budget check before execution.
	if budgetResult := checkBudget(cfg, roleName, "", 0); budgetResult != nil && !budgetResult.Allowed {
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

	providerName := resolveProviderName(cfg, task, roleName)

	logDebugCtx(ctx, "task start",
		"taskId", task.ID[:8], "name", task.Name,
		"model", task.Model, "provider", providerName,
		"role", roleName, "workdir", task.Workdir)

	// Publish SSE started event.
	if state.broker != nil {
		state.broker.PublishMulti([]string{task.ID, task.SessionID}, SSEEvent{
			Type:      SSEStarted,
			TaskID:    task.ID,
			SessionID: task.SessionID,
			Data: map[string]any{
				"name":  task.Name,
				"role":  roleName,
				"model": task.Model,
			},
		})
	}

	// Create event channel for provider streaming.
	var eventCh chan SSEEvent
	if state.broker != nil && (state.broker.HasSubscribers(task.ID) || state.broker.HasSubscribers(task.SessionID)) {
		eventCh = make(chan SSEEvent, 128)
		go func() {
			for ev := range eventCh {
				state.broker.PublishMulti([]string{task.ID, task.SessionID}, ev)
			}
		}()
	}

	start := time.Now()
	pr := executeWithProviderAndTools(taskCtx, cfg, task, roleName, cfg.registry, eventCh, state.broker)
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
			if err := enqueueTask(cfg.HistoryDB, task, roleName, 0); err == nil {
				result.Status = "queued"
				logInfoCtx(ctx, "task queued for offline retry",
					"taskId", task.ID[:8], "name", task.Name)

				// Publish SSE queued event.
				if state.broker != nil {
					state.broker.PublishMulti([]string{task.ID, task.SessionID}, SSEEvent{
						Type:      SSEQueued,
						TaskID:    task.ID,
						SessionID: task.SessionID,
						Data: map[string]any{
							"name":  task.Name,
							"role":  roleName,
							"error": result.Error,
						},
					})
				}
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

	// Save output to file.
	if pr.Output != "" {
		result.OutputFile = saveTaskOutput(cfg.baseDir, task.ID, []byte(pr.Output))
	}

	// Record to history DB.
	recordHistory(cfg.HistoryDB, task.ID, task.Name, task.Source, task.Role, task, result,
		start.Format(time.RFC3339), time.Now().Format(time.RFC3339), result.OutputFile)

	// Record session activity (skip for chat source — handled by HTTP handler).
	if !strings.HasPrefix(task.Source, "chat") {
		recordSessionActivity(cfg.HistoryDB, task, result, task.Role)
	}

	// Publish SSE completed/error/queued event.
	if state.broker != nil {
		evType := SSECompleted
		if result.Status == "queued" {
			// Already published SSEQueued above.
		} else if result.Status != "success" {
			evType = SSEError
		}
		if result.Status != "queued" {
			state.broker.PublishMulti([]string{task.ID, task.SessionID}, SSEEvent{
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
				logDebugCtx(ctx, "reflection stored", "taskId", task.ID[:8], "role", ref.Role, "score", ref.Score)
			}
		}()
	}

	// Check trust promotion after successful task.
	if result.Status == "success" && roleName != "" {
		if promoMsg := checkTrustPromotion(ctx, cfg, roleName); promoMsg != "" {
			// Publish SSE event for dashboard.
			if state.broker != nil {
				state.broker.Publish("trust", SSEEvent{
					Type: "trust_promotion",
					Data: map[string]any{
						"role":    roleName,
						"message": promoMsg,
					},
				})
			}
		}
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

	result := runSingleTask(ctx, cfg, task, sem, task.Role)

	// Record to history.
	start := time.Now().Add(-time.Duration(result.DurationMs) * time.Millisecond)
	recordHistory(cfg.HistoryDB, task.ID, task.Name, task.Source, task.Role, task, result,
		start.Format(time.RFC3339), time.Now().Format(time.RFC3339), result.OutputFile)

	// Record session activity.
	recordSessionActivity(cfg.HistoryDB, task, result, task.Role)

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
// allowing a different role to handle it.
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
		fmt.Sprintf("original=%s role=%s status=%s", taskID, result.Route.Role, result.Task.Status), "")

	return result, nil
}

// failedTaskInfo is a JSON-serializable summary of a failed task.
type failedTaskInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Prompt   string `json:"prompt,omitempty"`
	Role     string `json:"role,omitempty"`
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
			Role:     ft.task.Role,
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

// --- Agentic Loop ---

// executeWithProviderAndTools runs a task with tool support via agentic loop.
// If the provider supports tools and the tool registry has tools, it will:
// 1. Call provider with tools
// 2. Check for tool_use in response
// 3. Execute tools via ToolRegistry
// 4. Inject tool results back as messages
// 5. Call provider again
// 6. Repeat until no more tool_use or max iterations
func executeWithProviderAndTools(ctx context.Context, cfg *Config, task Task, roleName string, registry *providerRegistry, eventCh chan<- SSEEvent, broker *sseBroker) *ProviderResult {
	// Check if tool engine is enabled and we have a tool registry.
	if cfg.toolRegistry == nil {
		return executeWithProvider(ctx, cfg, task, roleName, registry, eventCh)
	}

	// Resolve provider.
	providerName := resolveProviderName(cfg, task, roleName)
	p, err := registry.get(providerName)
	if err != nil {
		return &ProviderResult{IsError: true, Error: err.Error()}
	}

	// Check if provider supports tools.
	toolProvider, supportsTools := p.(ToolCapableProvider)
	if !supportsTools {
		// Fallback to regular execution.
		return executeWithProvider(ctx, cfg, task, roleName, registry, eventCh)
	}

	// Get available tools.
	tools := cfg.toolRegistry.List()
	if len(tools) == 0 {
		// No tools available, use regular execution.
		return executeWithProvider(ctx, cfg, task, roleName, registry, eventCh)
	}

	// Build initial request.
	req := buildProviderRequest(cfg, task, roleName, providerName, eventCh)
	req.Tools = *(*[]ToolDef)(unsafe.Pointer(&tools)) // Convert []*ToolDef to []ToolDef

	// Initialize enhanced loop detector.
	detector := NewLoopDetector()

	// Max iterations.
	maxIter := cfg.Tools.MaxIterations
	if maxIter <= 0 {
		maxIter = 10
	}

	var messages []Message
	var finalResult *ProviderResult

	for i := 0; i < maxIter; i++ {
		req.Messages = messages

		// Call provider.
		result, execErr := toolProvider.ExecuteWithTools(ctx, req)
		if execErr != nil {
			return &ProviderResult{IsError: true, Error: execErr.Error()}
		}
		if result.IsError {
			return result
		}

		// Check stop reason.
		if result.StopReason != "tool_use" || len(result.ToolCalls) == 0 {
			// No more tool calls, we're done.
			finalResult = result
			break
		}

		// Publish SSE event for tool calls.
		if broker != nil {
			for _, tc := range result.ToolCalls {
				broker.PublishMulti([]string{task.ID, task.SessionID}, SSEEvent{
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
			// Check tool policy - is tool allowed for this role?
			if task.Role != "" && !isToolAllowed(cfg, task.Role, tc.Name) {
				logWarnCtx(ctx, "tool call blocked by policy", "tool", tc.Name, "role", task.Role)
				toolResults = append(toolResults, ToolResult{
					ToolUseID: tc.ID,
					Content:   fmt.Sprintf("error: tool %q not allowed by policy for role %q", tc.Name, task.Role),
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
			if mockResult, shouldExec := filterToolCall(cfg, task.Role, tc); !shouldExec {
				// Tool call filtered by trust level (observe or suggest mode).
				toolResults = append(toolResults, *mockResult)
				continue
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

			// Execute tool.
			toolStart := time.Now()
			output, err := tool.Handler(ctx, cfg, tc.Input)
			toolDuration := time.Since(toolStart)

			tr := ToolResult{ToolUseID: tc.ID}
			if err != nil {
				tr.Content = fmt.Sprintf("error: %v", err)
				tr.IsError = true
			} else {
				tr.Content = output
			}
			toolResults = append(toolResults, tr)

			// Publish SSE event for tool result.
			if broker != nil {
				broker.PublishMulti([]string{task.ID, task.SessionID}, SSEEvent{
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
	}

	if finalResult == nil {
		// Max iterations reached without final answer.
		finalResult = &ProviderResult{
			IsError: true,
			Error:   fmt.Sprintf("max tool iterations (%d) reached", maxIter),
		}
	}

	return finalResult
}
