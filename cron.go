package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// --- Cron Job Types ---

type JobsFile struct {
	Jobs []CronJobConfig `json:"jobs"`
}

type CronJobConfig struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	Enabled         bool           `json:"enabled"`
	Schedule        string         `json:"schedule"`
	TZ              string         `json:"tz"`
	Role            string         `json:"role"`
	Task            CronTaskConfig `json:"task"`
	Notify          bool           `json:"notify"`
	MaxRetries      int            `json:"maxRetries,omitempty"`      // 0 = no retry (default)
	RetryDelay      string         `json:"retryDelay,omitempty"`      // e.g. "1m", "5m"; default "1m"
	OnSuccess       []string       `json:"onSuccess,omitempty"`       // job IDs to trigger on success
	OnFailure       []string       `json:"onFailure,omitempty"`       // job IDs to trigger on failure
	RequireApproval bool           `json:"requireApproval,omitempty"` // true = wait for human approval before running
	ApprovalTimeout string         `json:"approvalTimeout,omitempty"` // e.g. "10m"; default "10m"
}

type CronTaskConfig struct {
	Prompt         string   `json:"prompt"`
	PromptFile     string   `json:"promptFile,omitempty"` // file in ~/.tetora/prompts/ (overrides prompt)
	Workdir        string   `json:"workdir"`
	Model          string   `json:"model"`
	Provider       string   `json:"provider,omitempty"` // override provider (e.g. "openai", "ollama")
	Docker         *bool    `json:"docker,omitempty"`   // per-job Docker sandbox override
	Timeout        string   `json:"timeout"`
	Budget         float64  `json:"budget"`
	PermissionMode string   `json:"permissionMode"`
	MCP            string   `json:"mcp"`
	AddDirs        []string `json:"addDirs"`
}

// --- Runtime Job State ---

type cronJob struct {
	CronJobConfig
	expr     cronExpr
	loc      *time.Location
	nextRun  time.Time
	lastRun  time.Time
	lastErr  string
	lastCost float64
	errors     int  // consecutive errors
	running    bool
	runStart   time.Time
	runTimeout string
	cancelFn   context.CancelFunc // cancel this specific job
	chainDepth int                // current chain depth (0 = top-level)

	// Approval gate.
	pendingApproval bool
	approvalCh      chan bool // true = approved, false = rejected
}

const maxChainDepth = 5

// --- Cron Engine ---

type CronEngine struct {
	cfg      *Config
	sem      chan struct{}
	notifyFn         func(string)                              // send Telegram message
	notifyKeyboardFn func(string, [][]tgInlineButton)          // send with inline keyboard

	mu   sync.RWMutex
	jobs []*cronJob

	ctx    context.Context // global context for spawning chain jobs
	stopCh chan struct{}
	wg     sync.WaitGroup  // tracks the ticker goroutine
	jobWg  sync.WaitGroup  // tracks all running job goroutines

	budgetWarned    bool
	budgetCacheTime time.Time
	budgetCacheOver bool
	budgetCacheMsg  string

	lastDigestDate string // "2006-01-02" — prevents firing more than once per day
}

func newCronEngine(cfg *Config, sem chan struct{}, notifyFn func(string)) *CronEngine {
	return &CronEngine{
		cfg:      cfg,
		sem:      sem,
		notifyFn: notifyFn,
		stopCh:   make(chan struct{}),
	}
}

func (ce *CronEngine) loadJobs() error {
	data, err := os.ReadFile(ce.cfg.JobsFile)
	if err != nil {
		if os.IsNotExist(err) {
			logInfo("no jobs file, starting with 0 jobs", "path", ce.cfg.JobsFile)
			return nil
		}
		return fmt.Errorf("read jobs: %w", err)
	}

	var jf JobsFile
	if err := json.Unmarshal(data, &jf); err != nil {
		return fmt.Errorf("parse jobs: %w", err)
	}

	ce.mu.Lock()
	defer ce.mu.Unlock()

	ce.jobs = nil
	for _, jc := range jf.Jobs {
		expr, err := parseCronExpr(jc.Schedule)
		if err != nil {
			logWarn("cron skip job, bad schedule", "jobId", jc.ID, "schedule", jc.Schedule, "error", err)
			continue
		}

		loc := time.Local
		if jc.TZ != "" {
			if l, err := time.LoadLocation(jc.TZ); err == nil {
				loc = l
			} else {
				logWarn("cron job bad timezone, using local", "jobId", jc.ID, "tz", jc.TZ)
			}
		}

		j := &cronJob{
			CronJobConfig: jc,
			expr:          expr,
			loc:           loc,
		}
		j.nextRun = nextRunAfter(j.expr, j.loc, time.Now().In(j.loc))
		ce.jobs = append(ce.jobs, j)
	}

	logInfo("cron loaded jobs", "total", len(ce.jobs), "enabled", ce.countEnabled())
	return nil
}

func (ce *CronEngine) countEnabled() int {
	n := 0
	for _, j := range ce.jobs {
		if j.Enabled {
			n++
		}
	}
	return n
}

func (ce *CronEngine) start(ctx context.Context) {
	ce.ctx = ctx
	ce.wg.Add(1)
	go func() {
		defer ce.wg.Done()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		logInfo("cron scheduler started", "tick", "30s")

		for {
			select {
			case <-ctx.Done():
				return
			case <-ce.stopCh:
				return
			case <-ticker.C:
				ce.tick(ctx)
			}
		}
	}()
}

func (ce *CronEngine) stop() {
	close(ce.stopCh)
	ce.wg.Wait() // wait for ticker goroutine

	// Wait for all running jobs to finish (with timeout).
	done := make(chan struct{})
	go func() {
		ce.jobWg.Wait()
		close(done)
	}()
	select {
	case <-done:
		logInfo("cron all jobs finished")
	case <-time.After(30 * time.Second):
		logWarn("cron shutdown timeout, some jobs still running")
	}
}

func (ce *CronEngine) checkBudget() (exceeded bool, reason string) {
	if ce.cfg.CostAlert.DailyLimit <= 0 && ce.cfg.CostAlert.WeeklyLimit <= 0 {
		return false, ""
	}
	if ce.cfg.HistoryDB == "" {
		return false, ""
	}

	// Cache for 30 seconds to avoid spawning sqlite3 every tick.
	if time.Since(ce.budgetCacheTime) < 30*time.Second {
		return ce.budgetCacheOver, ce.budgetCacheMsg
	}

	stats, err := queryCostStats(ce.cfg.HistoryDB)
	if err != nil {
		return false, ""
	}

	ce.budgetCacheTime = time.Now()

	if ce.cfg.CostAlert.DailyLimit > 0 && stats.Today >= ce.cfg.CostAlert.DailyLimit {
		ce.budgetCacheOver = true
		ce.budgetCacheMsg = fmt.Sprintf("daily limit $%.2f reached (spent $%.2f today)", ce.cfg.CostAlert.DailyLimit, stats.Today)
		return true, ce.budgetCacheMsg
	}
	if ce.cfg.CostAlert.WeeklyLimit > 0 && stats.Week >= ce.cfg.CostAlert.WeeklyLimit {
		ce.budgetCacheOver = true
		ce.budgetCacheMsg = fmt.Sprintf("weekly limit $%.2f reached (spent $%.2f this week)", ce.cfg.CostAlert.WeeklyLimit, stats.Week)
		return true, ce.budgetCacheMsg
	}

	ce.budgetCacheOver = false
	ce.budgetCacheMsg = ""
	if ce.budgetWarned {
		ce.budgetWarned = false // reset when under budget again
	}
	return false, ""
}

// checkDigest sends a daily digest notification if configured and it's time.
func (ce *CronEngine) checkDigest() {
	if !ce.cfg.Digest.Enabled || ce.notifyFn == nil || ce.cfg.HistoryDB == "" {
		return
	}

	// Parse digest time (default 08:00).
	digestTime := ce.cfg.Digest.Time
	if digestTime == "" {
		digestTime = "08:00"
	}
	dh, dm := parseHHMM(digestTime)
	if dh < 0 {
		return
	}

	// Resolve timezone.
	loc := time.Local
	if ce.cfg.Digest.TZ != "" {
		if l, err := time.LoadLocation(ce.cfg.Digest.TZ); err == nil {
			loc = l
		}
	}

	now := time.Now().In(loc)
	today := now.Format("2006-01-02")

	// Already sent today?
	if ce.lastDigestDate == today {
		return
	}

	// Check if current time is past the digest time.
	if now.Hour() < dh || (now.Hour() == dh && now.Minute() < dm) {
		return
	}

	// Mark as sent for today (even if query fails, avoid retrying every tick).
	ce.lastDigestDate = today

	// Query yesterday's stats.
	yesterday := now.AddDate(0, 0, -1)
	from := yesterday.Format("2006-01-02") + "T00:00:00"
	to := today + "T00:00:00"

	total, success, fail, cost, failures, err := queryDigestStats(ce.cfg.HistoryDB, from, to)
	if err != nil {
		logError("digest query error", "error", err)
		return
	}

	if total == 0 {
		// Nothing happened yesterday — skip digest.
		return
	}

	// Format message.
	msg := fmt.Sprintf("Tetora Daily (%s)\n%d jobs: %d OK, %d FAIL\nCost: $%.2f",
		yesterday.Format("2006-01-02"), total, success, fail, cost)

	if len(failures) > 0 {
		msg += "\n"
		for _, f := range failures {
			errMsg := f.Error
			if len(errMsg) > 100 {
				errMsg = errMsg[:100] + "..."
			}
			msg += fmt.Sprintf("\n[FAIL] %s: %s", f.Name, errMsg)
		}
	}

	logInfo("sending daily digest", "date", yesterday.Format("2006-01-02"))
	ce.notifyFn(msg)
}

func (ce *CronEngine) tick(ctx context.Context) {
	// Check quiet hours transition (flush digest if just left quiet period).
	quiet.checkQuietTransition(ce.cfg, ce.notifyFn)

	// Check daily digest trigger.
	ce.checkDigest()

	exceeded, reason := ce.checkBudget()
	if exceeded {
		if ce.cfg.CostAlert.Action == "pause" {
			logWarn("cron budget exceeded, pausing", "reason", reason)
			return
		}
		if !ce.budgetWarned {
			ce.budgetWarned = true
			logWarn("cron budget warning", "reason", reason)
			if ce.notifyFn != nil {
				ce.notifyFn("Budget warning: " + reason)
			}
		}
	}

	now := time.Now()
	ce.mu.Lock()
	defer ce.mu.Unlock()

	for _, j := range ce.jobs {
		if !j.Enabled || j.running {
			continue
		}

		nowLocal := now.In(j.loc)
		if !j.nextRun.IsZero() && nowLocal.Before(j.nextRun) {
			continue
		}

		// Check cron expression matches current minute.
		if !j.expr.matches(nowLocal) {
			continue
		}

		// Avoid double-firing in the same minute.
		if !j.lastRun.IsZero() &&
			j.lastRun.In(j.loc).Truncate(time.Minute).Equal(nowLocal.Truncate(time.Minute)) {
			continue
		}

		j.running = true
		jobCtx, jobCancel := context.WithCancel(ctx)
		j.cancelFn = jobCancel
		ce.jobWg.Add(1)
		go func(j *cronJob) {
			defer ce.jobWg.Done()
			ce.runJob(jobCtx, j)
		}(j)
	}
}

func (ce *CronEngine) runJob(ctx context.Context, j *cronJob) {
	defer func() {
		ce.mu.Lock()
		j.running = false
		j.runStart = time.Time{}
		j.runTimeout = ""
		if j.cancelFn != nil {
			j.cancelFn()
			j.cancelFn = nil
		}
		now := time.Now().In(j.loc)
		j.nextRun = nextRunAfter(j.expr, j.loc, now)
		ce.mu.Unlock()
	}()

	// Build task from cron job config.
	task := Task{
		Prompt:         j.Task.Prompt,
		Workdir:        j.Task.Workdir,
		Model:          j.Task.Model,
		Provider:       j.Task.Provider,
		Docker:         j.Task.Docker,
		Timeout:        j.Task.Timeout,
		Budget:         j.Task.Budget,
		PermissionMode: j.Task.PermissionMode,
		MCP:            j.Task.MCP,
		AddDirs:        j.Task.AddDirs,
		Source:         "cron",
	}
	fillDefaults(ce.cfg, &task)
	task.Name = j.Name

	// Inject role system prompt if specified.
	if j.Role != "" {
		prompt, err := loadRolePrompt(ce.cfg, j.Role)
		if err != nil {
			logWarnCtx(ctx, "cron job role load failed", "jobId", j.ID, "role", j.Role, "error", err)
		} else if prompt != "" {
			task.SystemPrompt = prompt
		}

		// Use role's model if task doesn't override.
		if j.Task.Model == "" {
			if rc, ok := ce.cfg.Roles[j.Role]; ok && rc.Model != "" {
				task.Model = rc.Model
			}
		}

		// Use role's permission mode if job didn't set one.
		if j.Task.PermissionMode == "" {
			if rc, ok := ce.cfg.Roles[j.Role]; ok && rc.PermissionMode != "" {
				task.PermissionMode = rc.PermissionMode
			}
		}
	}

	// Resolve promptFile if specified (overrides inline prompt).
	if j.Task.PromptFile != "" {
		if content, err := resolvePromptFile(ce.cfg, j.Task.PromptFile); err != nil {
			logWarnCtx(ctx, "cron job promptFile error", "jobId", j.ID, "promptFile", j.Task.PromptFile, "error", err)
		} else if content != "" {
			task.Prompt = content
		}
	}

	// Expand template variables in prompt.
	task.Prompt = expandPrompt(task.Prompt, j.ID, ce.cfg.HistoryDB, j.Role, ce.cfg.KnowledgeDir, ce.cfg)

	// Approval gate: wait for human approval if required.
	if j.RequireApproval && j.chainDepth == 0 {
		approvalTimeout := 10 * time.Minute
		if j.ApprovalTimeout != "" {
			if d, err := time.ParseDuration(j.ApprovalTimeout); err == nil && d > 0 {
				approvalTimeout = d
			}
		}

		j.approvalCh = make(chan bool, 1)
		ce.mu.Lock()
		j.pendingApproval = true
		ce.mu.Unlock()

		logInfoCtx(ctx, "cron job requires approval", "jobId", j.ID, "timeout", approvalTimeout)
		if ce.notifyKeyboardFn != nil {
			keyboard := [][]tgInlineButton{
				{
					{Text: "Approve", CallbackData: "approve:" + j.ID},
					{Text: "Reject", CallbackData: "reject:" + j.ID},
				},
			}
			ce.notifyKeyboardFn(fmt.Sprintf("Job %q ready to run.\nSchedule: %s\nApprove or reject within %v.",
				j.Name, j.Schedule, approvalTimeout), keyboard)
		} else if ce.notifyFn != nil {
			ce.notifyFn(fmt.Sprintf("Job %q ready to run. Use /approve %s or /reject %s (timeout: %v).",
				j.Name, j.ID, j.ID, approvalTimeout))
		}

		// Wait for approval, rejection, timeout, or context cancellation.
		var approved bool
		select {
		case approved = <-j.approvalCh:
		case <-time.After(approvalTimeout):
			logWarnCtx(ctx, "cron job approval timed out", "jobId", j.ID)
		case <-ctx.Done():
			logWarnCtx(ctx, "cron job approval cancelled", "jobId", j.ID)
		}

		ce.mu.Lock()
		j.pendingApproval = false
		j.approvalCh = nil
		ce.mu.Unlock()

		if !approved {
			reason := "rejected"
			if ctx.Err() != nil {
				reason = "cancelled"
			}
			logInfoCtx(ctx, "cron job skipped", "jobId", j.ID, "reason", reason)
			if ce.notifyFn != nil {
				ce.notifyFn(fmt.Sprintf("Job %q skipped (%s).", j.Name, reason))
			}
			return
		}
		logInfoCtx(ctx, "cron job approved", "jobId", j.ID)
	}

	logInfoCtx(ctx, "cron running job", "jobId", j.ID, "name", j.Name)
	jobStart := time.Now()

	ce.mu.Lock()
	j.runStart = jobStart
	j.runTimeout = task.Timeout
	ce.mu.Unlock()

	// Retry loop.
	maxAttempts := 1 + j.MaxRetries // first attempt + retries
	retryDelay := time.Minute       // default 1m
	if j.RetryDelay != "" {
		if d, err := time.ParseDuration(j.RetryDelay); err == nil && d > 0 {
			retryDelay = d
		}
	}

	var result TaskResult
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			// Wait before retry.
			logInfoCtx(ctx, "cron job retry", "jobId", j.ID, "attempt", attempt, "maxRetries", j.MaxRetries, "delay", retryDelay)
			select {
			case <-ctx.Done():
				result = TaskResult{
					ID: task.ID, Name: task.Name, Status: "cancelled",
					Error: "cancelled during retry wait", Model: task.Model,
				}
				break
			case <-time.After(retryDelay):
			}

			// Generate a new task ID + session for the retry.
			task.ID = newUUID()
			task.SessionID = newUUID()

			// Record the retry attempt in history.
			recordHistory(ce.cfg.HistoryDB, j.ID, j.Name, "cron", j.Role, task, result,
				jobStart.Format(time.RFC3339), time.Now().Format(time.RFC3339), result.OutputFile)
		}

		attemptStart := time.Now()
		result = runSingleTask(ctx, ce.cfg, task, ce.sem, j.Role)

		if result.Status == "success" {
			break
		}

		// If context cancelled, don't retry.
		if ctx.Err() != nil {
			break
		}

		_ = attemptStart // used for potential future per-attempt timing
	}

	// Record final result to history DB.
	recordHistory(ce.cfg.HistoryDB, j.ID, j.Name, "cron", j.Role, task, result,
		jobStart.Format(time.RFC3339), time.Now().Format(time.RFC3339), result.OutputFile)

	// Record session activity.
	recordSessionActivity(ce.cfg.HistoryDB, task, result, j.Role)

	ce.mu.Lock()
	j.lastRun = time.Now()
	j.lastCost = result.CostUSD

	if result.Status == "success" {
		j.errors = 0
		j.lastErr = ""
	} else {
		j.errors++
		j.lastErr = result.Error
		if j.errors >= 3 {
			j.Enabled = false
			logWarn("cron job auto-disabled", "jobId", j.ID, "consecutiveErrors", j.errors)
			if ce.notifyFn != nil {
				ce.notifyFn(fmt.Sprintf("Cron job %q auto-disabled after %d errors.\nLast error: %s",
					j.Name, j.errors, truncate(j.lastErr, 200)))
			}
		}
	}
	ce.mu.Unlock()

	// Telegram notification (respects quiet hours).
	if j.Notify && ce.notifyFn != nil {
		dur := time.Duration(result.DurationMs) * time.Millisecond
		var msg string
		if result.Status == "success" {
			output := truncate(result.Output, 500)
			msg = fmt.Sprintf("Cron %s\n%s (%s, $%.2f)\n\n%s",
				j.Name, result.Status, dur.Round(time.Second), result.CostUSD, output)
		} else {
			msg = fmt.Sprintf("Cron %s\n%s (%s)\nError: %s",
				j.Name, result.Status, dur.Round(time.Second), truncate(result.Error, 300))
		}

		if isQuietHours(ce.cfg) {
			if ce.cfg.QuietHours.Digest {
				quiet.enqueue(msg)
			}
			// else: discard silently
		} else {
			ce.notifyFn(msg)
		}
	}

	// Webhook notifications.
	sendWebhooks(ce.cfg, result.Status, WebhookPayload{
		JobID:    j.ID,
		Name:     j.Name,
		Source:   "cron",
		Status:   result.Status,
		Cost:     result.CostUSD,
		Duration: result.DurationMs,
		Model:    result.Model,
		Output:   truncate(result.Output, 500),
		Error:    truncate(result.Error, 300),
	})

	// Chain trigger: onSuccess / onFailure.
	var chainTargets []string
	if result.Status == "success" {
		chainTargets = j.OnSuccess
	} else {
		chainTargets = j.OnFailure
	}
	if len(chainTargets) > 0 {
		if j.chainDepth >= maxChainDepth {
			logWarnCtx(ctx, "cron job chain depth max reached, skipping", "jobId", j.ID, "depth", j.chainDepth, "max", maxChainDepth)
		} else {
			for _, targetID := range chainTargets {
				logInfoCtx(ctx, "cron job chain trigger", "jobId", j.ID, "target", targetID, "depth", j.chainDepth+1)
				auditLog(ce.cfg.HistoryDB, "job.chain", "cron",
					fmt.Sprintf("%s → %s (depth=%d, trigger=%s)", j.ID, targetID, j.chainDepth+1, result.Status), "")
				if err := ce.runChainJob(ce.ctx, targetID, j.chainDepth+1); err != nil {
					logErrorCtx(ctx, "cron chain trigger failed", "target", targetID, "error", err)
				}
			}
		}
	}
}

// RunJobByID manually triggers a cron job. Returns error if not found.
func (ce *CronEngine) RunJobByID(ctx context.Context, id string) error {
	ce.mu.Lock()
	var target *cronJob
	for _, j := range ce.jobs {
		if j.ID == id {
			target = j
			break
		}
	}
	if target == nil {
		ce.mu.Unlock()
		return fmt.Errorf("job %q not found", id)
	}
	if target.running {
		ce.mu.Unlock()
		return fmt.Errorf("job %q already running", id)
	}
	target.running = true
	jobCtx, jobCancel := context.WithCancel(ctx)
	target.cancelFn = jobCancel
	ce.mu.Unlock()

	ce.jobWg.Add(1)
	go func() {
		defer ce.jobWg.Done()
		ce.runJob(jobCtx, target)
	}()
	return nil
}

// runChainJob triggers a job as part of a chain with the given depth.
func (ce *CronEngine) runChainJob(ctx context.Context, id string, depth int) error {
	ce.mu.Lock()
	var target *cronJob
	for _, j := range ce.jobs {
		if j.ID == id {
			target = j
			break
		}
	}
	if target == nil {
		ce.mu.Unlock()
		return fmt.Errorf("job %q not found", id)
	}
	if target.running {
		ce.mu.Unlock()
		return fmt.Errorf("job %q already running", id)
	}
	target.running = true
	target.chainDepth = depth
	jobCtx, jobCancel := context.WithCancel(ctx)
	target.cancelFn = jobCancel
	ce.mu.Unlock()

	ce.jobWg.Add(1)
	go func() {
		defer ce.jobWg.Done()
		ce.runJob(jobCtx, target)
		// Reset chain depth after completion.
		ce.mu.Lock()
		target.chainDepth = 0
		ce.mu.Unlock()
	}()
	return nil
}

// CancelJob cancels a currently running job by ID.
func (ce *CronEngine) CancelJob(id string) error {
	ce.mu.Lock()
	defer ce.mu.Unlock()
	for _, j := range ce.jobs {
		if j.ID == id {
			if !j.running {
				return fmt.Errorf("job %q is not running", id)
			}
			if j.cancelFn != nil {
				j.cancelFn()
				return nil
			}
			return fmt.Errorf("job %q has no cancel function", id)
		}
	}
	return fmt.Errorf("job %q not found", id)
}

// ApproveJob approves a pending job. Returns error if not pending.
func (ce *CronEngine) ApproveJob(id string) error {
	ce.mu.Lock()
	defer ce.mu.Unlock()
	for _, j := range ce.jobs {
		if j.ID == id {
			if !j.pendingApproval || j.approvalCh == nil {
				return fmt.Errorf("job %q is not pending approval", id)
			}
			j.approvalCh <- true
			return nil
		}
	}
	return fmt.Errorf("job %q not found", id)
}

// RejectJob rejects a pending job. Returns error if not pending.
func (ce *CronEngine) RejectJob(id string) error {
	ce.mu.Lock()
	defer ce.mu.Unlock()
	for _, j := range ce.jobs {
		if j.ID == id {
			if !j.pendingApproval || j.approvalCh == nil {
				return fmt.Errorf("job %q is not pending approval", id)
			}
			j.approvalCh <- false
			return nil
		}
	}
	return fmt.Errorf("job %q not found", id)
}

// ToggleJob enables or disables a cron job.
func (ce *CronEngine) ToggleJob(id string, enabled bool) error {
	ce.mu.Lock()
	defer ce.mu.Unlock()
	for _, j := range ce.jobs {
		if j.ID == id {
			j.Enabled = enabled
			if enabled {
				j.errors = 0
				j.nextRun = nextRunAfter(j.expr, j.loc, time.Now().In(j.loc))
			}
			return nil
		}
	}
	return fmt.Errorf("job %q not found", id)
}

// ListJobs returns a snapshot of all cron jobs for display.
func (ce *CronEngine) ListJobs() []CronJobInfo {
	ce.mu.RLock()
	defer ce.mu.RUnlock()

	var infos []CronJobInfo
	for _, j := range ce.jobs {
		info := CronJobInfo{
			ID:        j.ID,
			Name:      j.Name,
			Enabled:   j.Enabled,
			Schedule:  j.Schedule,
			TZ:        j.TZ,
			Role:      j.Role,
			Running:   j.running,
			NextRun:   j.nextRun,
			LastRun:   j.lastRun,
			LastErr:   j.lastErr,
			LastCost:  j.lastCost,
			AvgCost:   queryJobAvgCost(ce.cfg.HistoryDB, j.ID),
			Errors:    j.errors,
			OnSuccess: j.OnSuccess,
			OnFailure: j.OnFailure,
		}
		if j.running && !j.runStart.IsZero() {
			info.RunStart = j.runStart
			info.RunElapsed = time.Since(j.runStart).Round(time.Second).String()
			info.RunTimeout = j.runTimeout
			info.RunModel = j.Task.Model
			prompt := j.Task.Prompt
			if len(prompt) > 100 {
				prompt = prompt[:100] + "..."
			}
			info.RunPrompt = prompt
		}
		infos = append(infos, info)
	}
	return infos
}

type CronJobInfo struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Enabled  bool      `json:"enabled"`
	Schedule string    `json:"schedule"`
	TZ       string    `json:"tz"`
	Role     string    `json:"role"`
	Running  bool      `json:"running"`
	NextRun  time.Time `json:"nextRun"`
	LastRun  time.Time `json:"lastRun"`
	LastErr    string    `json:"lastErr,omitempty"`
	LastCost   float64   `json:"lastCost"`
	AvgCost    float64   `json:"avgCost"`
	Errors     int       `json:"errors"`
	OnSuccess  []string  `json:"onSuccess,omitempty"`
	OnFailure  []string  `json:"onFailure,omitempty"`
	RunStart   time.Time `json:"runStart,omitempty"`
	RunElapsed string    `json:"runElapsed,omitempty"`
	RunTimeout string    `json:"runTimeout,omitempty"`
	RunModel   string    `json:"runModel,omitempty"`
	RunPrompt  string    `json:"runPrompt,omitempty"`
}

// GetJobConfig returns the full CronJobConfig for a job by ID.
func (ce *CronEngine) GetJobConfig(id string) *CronJobConfig {
	ce.mu.RLock()
	defer ce.mu.RUnlock()
	for _, j := range ce.jobs {
		if j.ID == id {
			c := j.CronJobConfig
			return &c
		}
	}
	return nil
}

// --- Job CRUD ---

// AddJob adds a new cron job, saves to file, and activates it in memory.
func (ce *CronEngine) AddJob(jc CronJobConfig) error {
	expr, err := parseCronExpr(jc.Schedule)
	if err != nil {
		return fmt.Errorf("bad schedule: %w", err)
	}

	ce.mu.Lock()
	defer ce.mu.Unlock()

	for _, j := range ce.jobs {
		if j.ID == jc.ID {
			return fmt.Errorf("job %q already exists", jc.ID)
		}
	}

	loc := time.Local
	if jc.TZ != "" {
		if l, err := time.LoadLocation(jc.TZ); err == nil {
			loc = l
		}
	}

	j := &cronJob{
		CronJobConfig: jc,
		expr:          expr,
		loc:           loc,
	}
	j.nextRun = nextRunAfter(j.expr, j.loc, time.Now().In(j.loc))
	ce.jobs = append(ce.jobs, j)

	if err := ce.saveToFileLocked(); err != nil {
		return fmt.Errorf("save: %w", err)
	}
	logInfo("cron added job", "jobId", jc.ID)
	return nil
}

// UpdateJob updates an existing cron job's config and saves to file.
func (ce *CronEngine) UpdateJob(id string, jc CronJobConfig) error {
	expr, err := parseCronExpr(jc.Schedule)
	if err != nil {
		return fmt.Errorf("bad schedule: %w", err)
	}

	ce.mu.Lock()
	defer ce.mu.Unlock()

	for _, j := range ce.jobs {
		if j.ID == id {
			if j.running {
				return fmt.Errorf("job %q is running, cannot update", id)
			}

			loc := time.Local
			if jc.TZ != "" {
				if l, err := time.LoadLocation(jc.TZ); err == nil {
					loc = l
				}
			}

			j.CronJobConfig = jc
			j.CronJobConfig.ID = id // preserve original ID
			j.expr = expr
			j.loc = loc
			j.nextRun = nextRunAfter(j.expr, j.loc, time.Now().In(j.loc))
			j.errors = 0
			j.lastErr = ""

			if err := ce.saveToFileLocked(); err != nil {
				return fmt.Errorf("save: %w", err)
			}
			logInfo("cron updated job", "jobId", id)
			return nil
		}
	}
	return fmt.Errorf("job %q not found", id)
}

// RemoveJob removes a cron job and saves to file.
func (ce *CronEngine) RemoveJob(id string) error {
	ce.mu.Lock()
	defer ce.mu.Unlock()

	idx := -1
	for i, j := range ce.jobs {
		if j.ID == id {
			if j.running {
				return fmt.Errorf("job %q is running, cannot remove", id)
			}
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("job %q not found", id)
	}

	ce.jobs = append(ce.jobs[:idx], ce.jobs[idx+1:]...)

	if err := ce.saveToFileLocked(); err != nil {
		return fmt.Errorf("save: %w", err)
	}
	logInfo("cron removed job", "jobId", id)
	return nil
}

// saveToFileLocked writes the current jobs to the jobs file. Must be called with mu held.
func (ce *CronEngine) saveToFileLocked() error {
	jf := JobsFile{}
	for _, j := range ce.jobs {
		jf.Jobs = append(jf.Jobs, j.CronJobConfig)
	}
	data, err := json.MarshalIndent(jf, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(ce.cfg.JobsFile, append(data, '\n'), 0o644)
}

// --- Cron Expression Parser ---

// cronExpr represents a parsed 5-field cron expression.
// Fields: minute(0-59) hour(0-23) dom(1-31) month(1-12) dow(0-6, 0=Sunday)
type cronExpr struct {
	minutes []bool // [60]
	hours   []bool // [24]
	doms    []bool // [32] (index 0 unused)
	months  []bool // [13] (index 0 unused)
	dows    []bool // [7]
}

func (e cronExpr) matches(t time.Time) bool {
	return e.minutes[t.Minute()] &&
		e.hours[t.Hour()] &&
		e.doms[t.Day()] &&
		e.months[int(t.Month())] &&
		e.dows[int(t.Weekday())]
}

func parseCronExpr(s string) (cronExpr, error) {
	fields := strings.Fields(s)
	if len(fields) != 5 {
		return cronExpr{}, fmt.Errorf("expected 5 fields, got %d", len(fields))
	}

	minutes, err := parseField(fields[0], 0, 59)
	if err != nil {
		return cronExpr{}, fmt.Errorf("minute: %w", err)
	}
	hours, err := parseField(fields[1], 0, 23)
	if err != nil {
		return cronExpr{}, fmt.Errorf("hour: %w", err)
	}
	doms, err := parseField(fields[2], 1, 31)
	if err != nil {
		return cronExpr{}, fmt.Errorf("dom: %w", err)
	}
	months, err := parseField(fields[3], 1, 12)
	if err != nil {
		return cronExpr{}, fmt.Errorf("month: %w", err)
	}
	dows, err := parseField(fields[4], 0, 6)
	if err != nil {
		return cronExpr{}, fmt.Errorf("dow: %w", err)
	}

	e := cronExpr{
		minutes: make([]bool, 60),
		hours:   make([]bool, 24),
		doms:    make([]bool, 32),
		months:  make([]bool, 13),
		dows:    make([]bool, 7),
	}
	for _, v := range minutes {
		e.minutes[v] = true
	}
	for _, v := range hours {
		e.hours[v] = true
	}
	for _, v := range doms {
		e.doms[v] = true
	}
	for _, v := range months {
		e.months[v] = true
	}
	for _, v := range dows {
		e.dows[v] = true
	}
	return e, nil
}

// parseField parses a single cron field. Supports: *, N, N-M, */N, N-M/S, N,M,O
func parseField(field string, min, max int) ([]int, error) {
	var result []int

	for _, part := range strings.Split(field, ",") {
		vals, err := parsePart(part, min, max)
		if err != nil {
			return nil, err
		}
		result = append(result, vals...)
	}

	return result, nil
}

func parsePart(part string, min, max int) ([]int, error) {
	// Handle step: */N or N-M/S
	step := 1
	if idx := strings.Index(part, "/"); idx != -1 {
		s, err := strconv.Atoi(part[idx+1:])
		if err != nil || s <= 0 {
			return nil, fmt.Errorf("bad step in %q", part)
		}
		step = s
		part = part[:idx]
	}

	var lo, hi int

	switch {
	case part == "*":
		lo, hi = min, max

	case strings.Contains(part, "-"):
		parts := strings.SplitN(part, "-", 2)
		var err error
		lo, err = strconv.Atoi(parts[0])
		if err != nil {
			return nil, fmt.Errorf("bad range start in %q", part)
		}
		hi, err = strconv.Atoi(parts[1])
		if err != nil {
			return nil, fmt.Errorf("bad range end in %q", part)
		}

	default:
		v, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("bad value %q", part)
		}
		if v < min || v > max {
			return nil, fmt.Errorf("value %d out of bounds [%d,%d]", v, min, max)
		}
		if step == 1 {
			return []int{v}, nil
		}
		lo, hi = v, max
	}

	if lo < min || hi > max || lo > hi {
		return nil, fmt.Errorf("range %d-%d out of bounds [%d,%d]", lo, hi, min, max)
	}

	var vals []int
	for v := lo; v <= hi; v += step {
		vals = append(vals, v)
	}
	return vals, nil
}

// nextRunAfter finds the next time after `after` that matches the cron expression.
func nextRunAfter(expr cronExpr, loc *time.Location, after time.Time) time.Time {
	// Start from the next minute.
	t := after.Truncate(time.Minute).Add(time.Minute)

	// Search up to 366 days ahead.
	limit := t.Add(366 * 24 * time.Hour)
	for t.Before(limit) {
		if expr.matches(t) {
			return t
		}
		// Skip ahead intelligently.
		if !expr.months[int(t.Month())] {
			// Skip to next month.
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, loc)
			continue
		}
		if !expr.doms[t.Day()] || !expr.dows[int(t.Weekday())] {
			// Skip to next day.
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, loc)
			continue
		}
		if !expr.hours[t.Hour()] {
			// Skip to next hour.
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, loc)
			continue
		}
		// Skip to next minute.
		t = t.Add(time.Minute)
	}
	return time.Time{} // no match found
}

// --- Helpers ---

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
