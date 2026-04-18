package cron

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"tetora/internal/audit"
	"tetora/internal/config"
	"tetora/internal/dispatch"
	"tetora/internal/health"
	"tetora/internal/history"
	"tetora/internal/log"
	"tetora/internal/quiet"
	"tetora/internal/session"
	"tetora/internal/trace"
	"tetora/internal/webhook"
)

// --- Cron Job Types ---

// JobsFile is the top-level structure of jobs.json.
type JobsFile struct {
	Jobs []JobConfig `json:"jobs"`
}

// JobConfig is the persisted configuration for a single cron job.
type JobConfig struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	Enabled         bool       `json:"enabled"`
	Schedule        string     `json:"schedule"`
	TZ              string     `json:"tz,omitempty"`
	Agent           string     `json:"agent,omitempty"`
	Task            TaskConfig `json:"task"`
	Notify          bool       `json:"notify,omitempty"`
	NotifyChannel   string     `json:"notifyChannel,omitempty"`   // Discord channel name, e.g. "stock"
	MaxRetries      int        `json:"maxRetries,omitempty"`      // 0 = no retry (default)
	RetryDelay      string     `json:"retryDelay,omitempty"`      // e.g. "1m", "5m"; default "1m"
	OnSuccess       []string   `json:"onSuccess,omitempty"`       // job IDs to trigger on success
	OnFailure       []string   `json:"onFailure,omitempty"`       // job IDs to trigger on failure
	RequireApproval bool       `json:"requireApproval,omitempty"` // true = wait for human approval before running
	ApprovalTimeout string     `json:"approvalTimeout,omitempty"` // e.g. "10m"; default "10m"
	IdleMinHours      int      `json:"idleMinHours,omitempty"`      // >0: only trigger when system idle for N hours
	MaxConcurrentRuns int      `json:"maxConcurrentRuns,omitempty"` // max simultaneous instances (default 1)
	Trigger           string   `json:"trigger,omitempty"`           // "idle" = idle-triggered mode
	IdleMinMinutes    int      `json:"idleMinMinutes,omitempty"`    // idle trigger: fire after N minutes idle (default 30)
	CooldownHours     float64  `json:"cooldownHours,omitempty"`     // idle trigger: min hours between triggers (default 20)
}

// TaskConfig holds the execution parameters for a cron task.
type TaskConfig struct {
	Prompt         string   `json:"prompt"`
	PromptFile     string   `json:"promptFile,omitempty"` // file in ~/.tetora/prompts/ (overrides prompt)
	Workdir        string   `json:"workdir,omitempty"`
	Model          string   `json:"model,omitempty"`
	Provider       string   `json:"provider,omitempty"`
	Docker         *bool    `json:"docker,omitempty"`
	Timeout        string   `json:"timeout,omitempty"`
	Budget         float64  `json:"budget,omitempty"`
	PermissionMode string   `json:"permissionMode,omitempty"`
	MCP            string   `json:"mcp,omitempty"`
	AddDirs        []string `json:"addDirs,omitempty"`
}

// --- Runtime Job State ---

type cronJob struct {
	JobConfig
	expr     Expr
	loc      *time.Location
	nextRun  time.Time
	lastRun  time.Time
	lastErr  string
	lastCost float64
	errors     int
	running    bool
	runCount   int
	runStart   time.Time
	runTimeout string
	cancelFn   context.CancelFunc
	chainDepth int

	// Approval gate.
	pendingApproval bool
	approvalCh      chan bool

	// Startup replay marker.
	replayed bool
}

const maxChainDepth = 5

func (j *cronJob) effectiveMaxConcurrentRuns() int {
	if j.MaxConcurrentRuns > 0 {
		return j.MaxConcurrentRuns
	}
	return 1
}

// --- Env: root-provided callbacks ---

// IdleChecker reports how long the system has been continuously idle.
type IdleChecker interface {
	SystemIdleDuration() time.Duration
}

// ApprovalKeyboardFn sends an approval request with inline keyboard buttons.
// Parameters: jobName, schedule string, approvalTimeout, jobID.
type ApprovalKeyboardFn func(jobName, schedule string, approvalTimeout time.Duration, jobID string)

// RegisterWorkerOriginFn registers a worker's session with the hook receiver.
// Parameters: sessionID, taskID, taskName, source, agent, jobID.
type RegisterWorkerOriginFn func(sessionID, taskID, taskName, source, agent, jobID string)

// Env holds all root-package callbacks that Engine depends on.
// Zero values are safe: nil callbacks are no-ops.
type Env struct {
	// Executor runs a task and returns the result (wraps runSingleTask).
	Executor dispatch.TaskExecutor

	// FillDefaults populates default values in a Task from config.
	FillDefaults func(cfg *config.Config, t *dispatch.Task)

	// LoadAgentPrompt loads the system prompt for a named agent.
	LoadAgentPrompt func(cfg *config.Config, agentName string) (string, error)

	// ResolvePromptFile loads prompt content from a file reference.
	ResolvePromptFile func(cfg *config.Config, promptFile string) (string, error)

	// ExpandPrompt expands template variables in a prompt string.
	ExpandPrompt func(prompt, jobID, dbPath, agentName, knowledgeDir string, cfg *config.Config) string

	// RecordHistory persists a job run to the history DB.
	RecordHistory func(dbPath, jobID, name, source, role string, task dispatch.Task, result dispatch.TaskResult, startedAt, finishedAt, outputFile string)

	// RecordSessionActivity records session-level activity for a completed task.
	RecordSessionActivity func(dbPath string, task dispatch.Task, result dispatch.TaskResult, role string)

	// TriageBacklog runs the backlog triage job.
	TriageBacklog func(ctx context.Context, cfg *config.Config, sem, childSem chan struct{})

	// RunDailyNotesJob runs the daily notes generation job.
	RunDailyNotesJob func(ctx context.Context, cfg *config.Config) error

	// SendWebhooks delivers outgoing webhook notifications.
	SendWebhooks func(cfg *config.Config, event string, payload webhook.Payload)

	// NewUUID generates a new UUID string.
	NewUUID func() string

	// RegisterWorkerOrigin registers a cron worker session with the hook receiver.
	RegisterWorkerOrigin RegisterWorkerOriginFn

	// NotifyKeyboard sends an approval request with inline keyboard buttons.
	NotifyKeyboard ApprovalKeyboardFn

	// QuietCfg extracts the quiet-hours config from the app config.
	QuietCfg func(cfg *config.Config) quiet.Config

	// QuietGlobal is the shared quiet-hours state for digest queuing.
	QuietGlobal *quiet.State
}

// --- Engine ---

// Engine is the cron scheduler: loads jobs from jobs.json, ticks every 30s,
// and runs matching jobs via the provided Executor.
type Engine struct {
	cfg      *config.Config
	sem      chan struct{}
	childSem chan struct{}
	notifyFn func(string)
	env      Env

	mu   sync.RWMutex
	jobs []*cronJob

	ctx    context.Context
	stopCh chan struct{}
	wg     sync.WaitGroup
	jobWg  sync.WaitGroup

	budgetWarned    bool
	budgetCacheTime time.Time
	budgetCacheOver bool
	budgetCacheMsg  string

	diskWarnLogged bool

	idleChecker      IdleChecker
	telegramKeyboard func(text string, keyboard any)

	lastDigestDate string

	idleCacheTime time.Time
	idleCacheLast time.Time

	jobsFileMtime time.Time
}

// NewEngine constructs an Engine. env.Executor must be non-nil for job execution.
func NewEngine(cfg *config.Config, sem, childSem chan struct{}, notifyFn func(string), env Env) *Engine {
	if env.NewUUID == nil {
		env.NewUUID = trace.NewUUID
	}
	return &Engine{
		cfg:      cfg,
		sem:      sem,
		childSem: childSem,
		notifyFn: notifyFn,
		env:      env,
		stopCh:   make(chan struct{}),
	}
}

// LastRunTime returns the most recent job completion time.
func (ce *Engine) LastRunTime() time.Time {
	ce.mu.RLock()
	defer ce.mu.RUnlock()
	var latest time.Time
	for _, j := range ce.jobs {
		if j.lastRun.After(latest) {
			latest = j.lastRun
		}
	}
	return latest
}

// SetIdleChecker wires an idle duration source for idle-trigger jobs.
func (ce *Engine) SetIdleChecker(h IdleChecker) {
	ce.idleChecker = h
}

// SetNotifyKeyboardFn replaces the approval keyboard notification handler.
func (ce *Engine) SetNotifyKeyboardFn(fn ApprovalKeyboardFn) {
	ce.env.NotifyKeyboard = fn
}

// SetTelegramKeyboardFn sets the Telegram inline keyboard callback for approval UIs.
func (ce *Engine) SetTelegramKeyboardFn(fn func(text string, keyboard any)) {
	ce.telegramKeyboard = fn
}

// TelegramKeyboard returns the Telegram keyboard callback (may be nil).
func (ce *Engine) TelegramKeyboard() func(text string, keyboard any) {
	return ce.telegramKeyboard
}

// checkJobsReload checks if jobs.json was modified and reloads if needed.
func (ce *Engine) checkJobsReload() {
	info, err := os.Stat(ce.cfg.JobsFile)
	if err != nil {
		return
	}
	mtime := info.ModTime()
	if mtime.Equal(ce.jobsFileMtime) || mtime.IsZero() {
		return
	}

	ce.mu.RLock()
	stateMap := make(map[string]*cronJob, len(ce.jobs))
	for _, j := range ce.jobs {
		stateMap[j.ID] = j
	}
	ce.mu.RUnlock()

	if err := ce.LoadJobs(); err != nil {
		log.Warn("cron hot-reload failed", "error", err)
		return
	}

	ce.mu.Lock()
	merged := make([]*cronJob, 0, len(ce.jobs))
	for _, j := range ce.jobs {
		if old, ok := stateMap[j.ID]; ok && old.runCount > 0 {
			// Job is currently running — reuse old pointer so that
			// the in-flight goroutine's defer targets the same struct.
			// Only update config fields from the new definition.
			old.JobConfig = j.JobConfig
			old.expr = j.expr
			old.loc = j.loc
			// Don't touch runtime state (runCount, running, runStart,
			// cancelFn, pendingApproval, approvalCh).
			merged = append(merged, old)
		} else if old, ok := stateMap[j.ID]; ok {
			// Job exists but not running — copy over persistent state.
			j.lastRun = old.lastRun
			j.lastErr = old.lastErr
			j.lastCost = old.lastCost
			j.errors = old.errors
			j.pendingApproval = old.pendingApproval
			j.approvalCh = old.approvalCh
			if old.nextRun.After(time.Now()) {
				j.nextRun = old.nextRun
			}
			merged = append(merged, j)
		} else {
			merged = append(merged, j)
		}
	}
	ce.jobs = merged
	ce.mu.Unlock()

	log.Info("cron hot-reloaded jobs.json", "total", len(ce.jobs), "enabled", ce.countEnabled())
}

// LoadJobs reads and parses jobs.json, replacing the in-memory job list.
func (ce *Engine) LoadJobs() error {
	data, err := os.ReadFile(ce.cfg.JobsFile)
	if err != nil {
		if os.IsNotExist(err) {
			log.Info("no jobs file, starting with 0 jobs", "path", ce.cfg.JobsFile)
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
		loc := time.Local
		if jc.TZ != "" {
			if l, err := time.LoadLocation(jc.TZ); err == nil {
				loc = l
			} else {
				log.Warn("cron job bad timezone, using local", "jobId", jc.ID, "tz", jc.TZ)
			}
		}

		// Idle-trigger jobs don't need a cron schedule expression.
		if jc.Trigger == "idle" {
			j := &cronJob{
				JobConfig: jc,
				loc:       loc,
			}
			ce.jobs = append(ce.jobs, j)
			continue
		}

		expr, err := Parse(jc.Schedule)
		if err != nil {
			log.Warn("cron skip job, bad schedule", "jobId", jc.ID, "schedule", jc.Schedule, "error", err)
			continue
		}

		j := &cronJob{
			JobConfig: jc,
			expr:      expr,
			loc:       loc,
		}
		j.nextRun = NextRunAfter(j.expr, j.loc, time.Now().In(j.loc))
		ce.jobs = append(ce.jobs, j)
	}

	if info, err := os.Stat(ce.cfg.JobsFile); err == nil {
		ce.jobsFileMtime = info.ModTime()
	}

	log.Info("cron loaded jobs", "total", len(ce.jobs), "enabled", ce.countEnabled())
	return nil
}

func (ce *Engine) countEnabled() int {
	n := 0
	for _, j := range ce.jobs {
		if j.Enabled {
			n++
		}
	}
	return n
}

// Start begins the scheduler loop. Must be called after LoadJobs.
func (ce *Engine) Start(ctx context.Context) {
	ce.ctx = ctx
	ce.wg.Add(1)
	go func() {
		defer ce.wg.Done()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		log.Info("cron scheduler started", "tick", "30s")

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

// Stop waits for the ticker to stop, then waits up to 30s for running jobs.
func (ce *Engine) Stop() {
	close(ce.stopCh)
	ce.wg.Wait()

	done := make(chan struct{})
	go func() {
		ce.jobWg.Wait()
		close(done)
	}()
	select {
	case <-done:
		log.Info("cron all jobs finished")
	case <-time.After(30 * time.Second):
		log.Warn("cron shutdown timeout, some jobs still running")
	}
}

func (ce *Engine) checkBudget() (exceeded bool, reason string) {
	if ce.cfg.CostAlert.DailyLimit <= 0 && ce.cfg.CostAlert.WeeklyLimit <= 0 {
		return false, ""
	}
	if ce.cfg.HistoryDB == "" {
		return false, ""
	}

	if time.Since(ce.budgetCacheTime) < 30*time.Second {
		return ce.budgetCacheOver, ce.budgetCacheMsg
	}

	stats, err := history.QueryCostStats(ce.cfg.HistoryDB)
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
		ce.budgetWarned = false
	}
	return false, ""
}

func (ce *Engine) diskWarnThresholdMB() int {
	if ce.cfg.DiskWarnMB > 0 {
		return ce.cfg.DiskWarnMB
	}
	if ce.cfg.DiskBudgetGB > 0 {
		return int(ce.cfg.DiskBudgetGB * 1024)
	}
	return 500
}

func (ce *Engine) diskBlockThresholdMB() int {
	if ce.cfg.DiskBlockMB > 0 {
		return ce.cfg.DiskBlockMB
	}
	return 200
}

func (ce *Engine) checkDisk() (status string, freeGB float64) {
	if ce.cfg.BaseDir == "" {
		return "ok", 0
	}
	free := health.DiskFreeBytes(ce.cfg.BaseDir)
	freeGB = float64(free) / (1024 * 1024 * 1024)
	freeMB := freeGB * 1024
	switch {
	case freeMB < float64(ce.diskBlockThresholdMB()):
		return "critical", freeGB
	case freeMB < float64(ce.diskWarnThresholdMB()):
		return "warning", freeGB
	default:
		return "ok", freeGB
	}
}

func (ce *Engine) quietCfg() quiet.Config {
	if ce.env.QuietCfg != nil {
		return ce.env.QuietCfg(ce.cfg)
	}
	return quiet.Config{
		Enabled: ce.cfg.QuietHours.Enabled,
		Start:   ce.cfg.QuietHours.Start,
		End:     ce.cfg.QuietHours.End,
		TZ:      ce.cfg.QuietHours.TZ,
		Digest:  ce.cfg.QuietHours.Digest,
	}
}

func (ce *Engine) checkDigest() {
	if !ce.cfg.Digest.Enabled || ce.notifyFn == nil || ce.cfg.HistoryDB == "" {
		return
	}

	digestTime := ce.cfg.Digest.Time
	if digestTime == "" {
		digestTime = "08:00"
	}
	dh, dm := quiet.ParseHHMM(digestTime)
	if dh < 0 {
		return
	}

	loc := time.Local
	if ce.cfg.Digest.TZ != "" {
		if l, err := time.LoadLocation(ce.cfg.Digest.TZ); err == nil {
			loc = l
		}
	}

	now := time.Now().In(loc)
	today := now.Format("2006-01-02")

	if ce.lastDigestDate == today {
		return
	}

	if now.Hour() < dh || (now.Hour() == dh && now.Minute() < dm) {
		return
	}

	ce.lastDigestDate = today

	yesterday := now.AddDate(0, 0, -1)
	from := yesterday.Format("2006-01-02") + "T00:00:00"
	to := today + "T00:00:00"

	total, success, fail, cost, failures, err := history.QueryDigestStats(ce.cfg.HistoryDB, from, to)
	if err != nil {
		log.Error("digest query error", "error", err)
		return
	}

	if total == 0 {
		return
	}

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

	log.Info("sending daily digest", "date", yesterday.Format("2006-01-02"))
	ce.notifyFn(msg)
}

func (ce *Engine) cachedLastFinished() time.Time {
	if time.Since(ce.idleCacheTime) < 30*time.Second {
		return ce.idleCacheLast
	}
	ce.idleCacheLast = history.QueryLastFinished(ce.cfg.HistoryDB)
	ce.idleCacheTime = time.Now()
	return ce.idleCacheLast
}

func (ce *Engine) hasRunningJobs(excludeID string) bool {
	for _, j := range ce.jobs {
		if j.running && j.ID != excludeID {
			return true
		}
	}
	return false
}

func (ce *Engine) tick(ctx context.Context) {
	ce.checkJobsReload()

	if ce.env.QuietGlobal != nil {
		ce.env.QuietGlobal.CheckTransition(ce.quietCfg(), ce.notifyFn)
	}

	ce.checkDigest()

	exceeded, reason := ce.checkBudget()
	if exceeded {
		if ce.cfg.CostAlert.Action == "pause" {
			log.Warn("cron budget exceeded, pausing", "reason", reason)
			return
		}
		if !ce.budgetWarned {
			ce.budgetWarned = true
			log.Warn("cron budget warning", "reason", reason)
			if ce.notifyFn != nil {
				ce.notifyFn("Budget warning: " + reason)
			}
		}
	}

	diskStatus, freeGB := ce.checkDisk()
	switch diskStatus {
	case "critical":
		if !ce.diskWarnLogged {
			ce.diskWarnLogged = true
			log.Warn("cron disk critical: new jobs will be skipped", "freeGB", fmt.Sprintf("%.2f", freeGB), "blockMB", ce.diskBlockThresholdMB())
			if ce.notifyFn != nil {
				ce.notifyFn(fmt.Sprintf("Disk critical: only %.2fGB free — new cron jobs will be skipped (block at %dMB)", freeGB, ce.diskBlockThresholdMB()))
			}
		}
	case "warning":
		if !ce.diskWarnLogged {
			ce.diskWarnLogged = true
			log.Warn("cron disk warning: low free space", "freeGB", fmt.Sprintf("%.2f", freeGB), "warnMB", ce.diskWarnThresholdMB())
			if ce.notifyFn != nil {
				ce.notifyFn(fmt.Sprintf("Disk warning: only %.2fGB free (threshold %dMB)", freeGB, ce.diskWarnThresholdMB()))
			}
		}
	default:
		ce.diskWarnLogged = false
	}

	lastFinished := ce.cachedLastFinished()

	now := time.Now()
	ce.mu.Lock()
	defer ce.mu.Unlock()

	for _, j := range ce.jobs {
		if !j.Enabled {
			continue
		}

		// Idle-trigger jobs: evaluated purely on idle duration.
		if j.Trigger == "idle" {
			if ce.idleChecker == nil {
				continue
			}
			if j.runCount >= j.effectiveMaxConcurrentRuns() {
				continue
			}
			cooldown := j.CooldownHours
			if cooldown <= 0 {
				cooldown = 20
			}
			if !j.lastRun.IsZero() && time.Since(j.lastRun) < time.Duration(cooldown*float64(time.Hour)) {
				continue
			}
			minIdle := j.IdleMinMinutes
			if minIdle <= 0 {
				minIdle = 30
			}
			idleDur := ce.idleChecker.SystemIdleDuration()
			if idleDur < time.Duration(minIdle)*time.Minute {
				continue
			}
			log.Info("cron: idle trigger firing",
				"jobId", j.ID, "name", j.Name,
				"idleMinutes", int(idleDur.Minutes()),
				"threshold", minIdle)
			j.runCount++
			j.running = true
			jobCtx, jobCancel := context.WithCancel(ctx)
			j.cancelFn = jobCancel
			ce.jobWg.Add(1)
			go func(j *cronJob) {
				defer ce.jobWg.Done()
				ce.runJob(jobCtx, j)
			}(j)
			continue
		}

		// Per-job concurrency gate.
		maxRuns := j.effectiveMaxConcurrentRuns()
		if j.runCount >= maxRuns {
			if j.runCount > 0 {
				nowLocal := now.In(j.loc)
				if j.expr.Matches(nowLocal) {
					// Suppress noise when the job just started this minute.
					// The 30s ticker can fire twice within the same minute: the first
					// fires the job, the second sees it running and would record a
					// spurious skipped_concurrent_limit. Use runStart to detect this.
					sameMinute := !j.runStart.IsZero() &&
						j.runStart.In(j.loc).Truncate(time.Minute).Equal(nowLocal.Truncate(time.Minute))
					if !sameMinute {
						log.WarnCtx(ctx, "cron job skipped: already running max instances",
							"jobId", j.ID, "name", j.Name,
							"running", j.runCount, "maxConcurrentRuns", maxRuns)
						if ce.cfg.HistoryDB != "" {
							jID, jName, running, maxR := j.ID, j.Name, j.runCount, maxRuns
							histDB := ce.cfg.HistoryDB
							go func() {
								ts := time.Now().UTC().Format(time.RFC3339)
								_ = history.InsertRun(histDB, history.JobRun{
									JobID:      jID,
									Name:       jName,
									Source:     "cron",
									StartedAt:  ts,
									FinishedAt: ts,
									Status:     history.StatusSkippedConcurrentLimit,
									Error:      fmt.Sprintf("already %d instance(s) running (max %d)", running, maxR),
								})
							}()
						}
					}
				}
			}
			continue
		}

		// Special handling for daily notes job.
		if j.ID == "daily_notes" {
			if now.After(j.nextRun) || now.Equal(j.nextRun) {
				j.nextRun = NextRunAfter(j.expr, j.loc, now.In(j.loc))
				go ce.runDailyNotesJobAsync(ctx, j)
			}
			continue
		}

		// Special handling for backlog triage job.
		if j.ID == "backlog-triage" {
			nowLocal := now.In(j.loc)
			if !j.nextRun.IsZero() && nowLocal.Before(j.nextRun) {
				continue
			}
			if !j.expr.Matches(nowLocal) {
				continue
			}
			if j.IdleMinHours > 0 {
				if ce.hasRunningJobs(j.ID) {
					continue
				}
				if !lastFinished.IsZero() && time.Since(lastFinished) < time.Duration(j.IdleMinHours)*time.Hour {
					continue
				}
				if ce.countUserSessions() > 0 {
					continue
				}
			}
			if !j.lastRun.IsZero() &&
				j.lastRun.In(j.loc).Truncate(time.Minute).Equal(nowLocal.Truncate(time.Minute)) {
				continue
			}
			j.runCount++
			j.running = true
			ce.jobWg.Add(1)
			go func(j *cronJob) {
				defer ce.jobWg.Done()
				defer func() {
					ce.mu.Lock()
					j.runCount--
					j.running = j.runCount > 0
					j.lastRun = time.Now()
					j.nextRun = NextRunAfter(j.expr, j.loc, time.Now().In(j.loc))
					ce.mu.Unlock()
				}()
				if ce.env.TriageBacklog != nil {
					ce.env.TriageBacklog(ctx, ce.cfg, ce.sem, ce.childSem)
				}
			}(j)
			continue
		}

		nowLocal := now.In(j.loc)
		if !j.nextRun.IsZero() && nowLocal.Before(j.nextRun) {
			continue
		}

		if !j.expr.Matches(nowLocal) {
			continue
		}

		if j.IdleMinHours > 0 {
			if ce.hasRunningJobs(j.ID) {
				continue
			}
			if !lastFinished.IsZero() && time.Since(lastFinished) < time.Duration(j.IdleMinHours)*time.Hour {
				continue
			}
			if ce.countUserSessions() > 0 {
				continue
			}
		}

		if !j.lastRun.IsZero() &&
			j.lastRun.In(j.loc).Truncate(time.Minute).Equal(nowLocal.Truncate(time.Minute)) {
			continue
		}

		j.runCount++
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

// countUserSessions returns the number of active user sessions.
func (ce *Engine) countUserSessions() int {
	return session.CountUserSessions(ce.cfg.HistoryDB)
}

func (ce *Engine) runDailyNotesJobAsync(ctx context.Context, j *cronJob) {
	if ce.env.RunDailyNotesJob == nil {
		return
	}
	if err := ce.env.RunDailyNotesJob(ctx, ce.cfg); err != nil {
		log.Error("daily notes job failed", "error", err)
		if ce.notifyFn != nil {
			ce.notifyFn(fmt.Sprintf("Daily notes generation failed: %v", err))
		}
	} else {
		log.Info("daily notes job completed")
	}
}

func (ce *Engine) runJob(ctx context.Context, j *cronJob) {
	// Outer panic fence: declared first → runs LAST (defers are LIFO). Catches
	// panics from both the job body and the cleanup defer below, so a crash
	// inside ce.mu.Lock() / NextRunAfter can't escape the goroutine.
	defer func() {
		if r := recover(); r != nil {
			log.ErrorCtx(ctx, "cron runJob panic recovered",
				"jobId", j.ID, "name", j.Name, "recover", fmt.Sprintf("%v", r))
		}
	}()
	// Cleanup: declared second → runs FIRST on unwind. If it panics, the outer
	// fence catches it instead of crashing the daemon.
	defer func() {
		ce.mu.Lock()
		j.runCount--
		j.running = j.runCount > 0
		j.runStart = time.Time{}
		j.runTimeout = ""
		if j.cancelFn != nil {
			j.cancelFn()
			j.cancelFn = nil
		}
		now := time.Now().In(j.loc)
		j.nextRun = NextRunAfter(j.expr, j.loc, now)
		ce.mu.Unlock()
	}()

	// Disk block check.
	if diskStatus, diskFreeGB := ce.checkDisk(); diskStatus == "critical" {
		log.ErrorCtx(ctx, "cron job skipped: disk full", "jobId", j.ID, "name", j.Name, "freeGB", fmt.Sprintf("%.2f", diskFreeGB), "blockMB", ce.diskBlockThresholdMB())
		if ce.notifyFn != nil {
			ce.notifyFn(fmt.Sprintf("Job %q skipped: disk full (%.2fGB free, block at %dMB)", j.Name, diskFreeGB, ce.diskBlockThresholdMB()))
		}
		if ce.cfg.HistoryDB != "" {
			now := time.Now().UTC().Format(time.RFC3339)
			_ = history.InsertRun(ce.cfg.HistoryDB, history.JobRun{
				JobID:      j.ID,
				Name:       j.Name,
				Source:     "cron",
				StartedAt:  now,
				FinishedAt: now,
				Status:     "skipped_disk_full",
				Error:      fmt.Sprintf("disk full: %.2fGB free (block threshold: %dMB)", diskFreeGB, ce.diskBlockThresholdMB()),
			})
		}
		return
	} else if diskStatus == "warning" {
		log.WarnCtx(ctx, "cron job disk warning", "jobId", j.ID, "name", j.Name, "freeGB", fmt.Sprintf("%.2f", diskFreeGB), "warnMB", ce.diskWarnThresholdMB())
	}

	// Build task from job config.
	jobSource := "cron"
	if j.replayed {
		jobSource = "cron-replay"
	}
	task := dispatch.Task{
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
		Source:         jobSource,
	}
	if ce.env.FillDefaults != nil {
		ce.env.FillDefaults(ce.cfg, &task)
	}
	task.Name = j.Name

	// Inject agent system prompt if specified.
	if j.Agent != "" && ce.env.LoadAgentPrompt != nil {
		prompt, err := ce.env.LoadAgentPrompt(ce.cfg, j.Agent)
		if err != nil {
			log.WarnCtx(ctx, "cron job agent load failed", "jobId", j.ID, "agent", j.Agent, "error", err)
		} else if prompt != "" {
			task.SystemPrompt = prompt
		}

		if j.Task.Model == "" {
			if rc, ok := ce.cfg.Agents[j.Agent]; ok && rc.Model != "" {
				task.Model = rc.Model
			}
		}

		if j.Task.PermissionMode == "" {
			if rc, ok := ce.cfg.Agents[j.Agent]; ok && rc.PermissionMode != "" {
				task.PermissionMode = rc.PermissionMode
			}
		}
	}

	// Resolve promptFile if specified.
	if j.Task.PromptFile != "" && ce.env.ResolvePromptFile != nil {
		if content, err := ce.env.ResolvePromptFile(ce.cfg, j.Task.PromptFile); err != nil {
			log.WarnCtx(ctx, "cron job promptFile error", "jobId", j.ID, "promptFile", j.Task.PromptFile, "error", err)
		} else if content != "" {
			task.Prompt = content
		}
	}

	// Expand template variables in prompt.
	if ce.env.ExpandPrompt != nil {
		task.Prompt = ce.env.ExpandPrompt(task.Prompt, j.ID, ce.cfg.HistoryDB, j.Agent, ce.cfg.KnowledgeDir, ce.cfg)
	}

	if strings.TrimSpace(task.Prompt) == "" {
		log.WarnCtx(ctx, "cron job skipped: empty prompt", "jobId", j.ID, "name", j.Name)
		return
	}

	// Approval gate.
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

		log.InfoCtx(ctx, "cron job requires approval", "jobId", j.ID, "timeout", approvalTimeout)
		if ce.env.NotifyKeyboard != nil {
			ce.env.NotifyKeyboard(j.Name, j.Schedule, approvalTimeout, j.ID)
		} else if ce.notifyFn != nil {
			ce.notifyFn(fmt.Sprintf("Job %q ready to run. Use /approve %s or /reject %s (timeout: %v).",
				j.Name, j.ID, j.ID, approvalTimeout))
		}

		var approved bool
		select {
		case approved = <-j.approvalCh:
		case <-time.After(approvalTimeout):
			log.WarnCtx(ctx, "cron job approval timed out", "jobId", j.ID)
		case <-ctx.Done():
			log.WarnCtx(ctx, "cron job approval cancelled", "jobId", j.ID)
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
			log.InfoCtx(ctx, "cron job skipped", "jobId", j.ID, "reason", reason)
			if ce.notifyFn != nil {
				ce.notifyFn(fmt.Sprintf("Job %q skipped (%s).", j.Name, reason))
			}
			return
		}
		log.InfoCtx(ctx, "cron job approved", "jobId", j.ID)
	}

	log.InfoCtx(ctx, "cron running job", "jobId", j.ID, "name", j.Name)
	jobStart := time.Now()

	// Record in cron_execution_log for crash-recovery / startup replay.
	if ce.cfg.HistoryDB != "" {
		ce.mu.RLock()
		scheduledAt := j.nextRun
		ce.mu.RUnlock()
		if scheduledAt.IsZero() {
			scheduledAt = jobStart
		}
		history.InsertCronExecLog(ce.cfg.HistoryDB, j.ID,
			scheduledAt.UTC().Format(time.RFC3339),
			jobStart.UTC().Format(time.RFC3339),
			j.replayed)
	}

	ce.mu.Lock()
	j.runStart = jobStart
	j.runTimeout = task.Timeout
	ce.mu.Unlock()

	// Retry loop.
	maxAttempts := 1 + j.MaxRetries
	retryDelay := time.Minute
	if j.RetryDelay != "" {
		if d, err := time.ParseDuration(j.RetryDelay); err == nil && d > 0 {
			retryDelay = d
		}
	}

	// Register worker origin for hook tracking.
	if ce.env.RegisterWorkerOrigin != nil && task.SessionID != "" {
		ce.env.RegisterWorkerOrigin(task.SessionID, task.ID, j.Name, jobSource, j.Agent, j.ID)
	}

	var result dispatch.TaskResult
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			log.InfoCtx(ctx, "cron job retry", "jobId", j.ID, "attempt", attempt, "maxRetries", j.MaxRetries, "delay", retryDelay)
			select {
			case <-ctx.Done():
				result = dispatch.TaskResult{
					ID: task.ID, Name: task.Name, Status: "cancelled",
					Error: "cancelled during retry wait", Model: task.Model,
				}
				break
			case <-time.After(retryDelay):
			}

			task.ID = ce.env.NewUUID()
			task.SessionID = ce.env.NewUUID()

			if ce.env.RegisterWorkerOrigin != nil {
				ce.env.RegisterWorkerOrigin(task.SessionID, task.ID, j.Name, jobSource, j.Agent, j.ID)
			}

			if ce.env.RecordHistory != nil {
				ce.env.RecordHistory(ce.cfg.HistoryDB, j.ID, j.Name, jobSource, j.Agent, task, result,
					jobStart.Format(time.RFC3339), time.Now().Format(time.RFC3339), result.OutputFile)
			}
		}

		// Hard timeout per attempt.
		attemptCtx := ctx
		if task.Timeout != "" {
			if hardLimit, err := time.ParseDuration(task.Timeout); err == nil {
				var hardCancel context.CancelFunc
				attemptCtx, hardCancel = context.WithTimeout(ctx, hardLimit+2*time.Minute)
				defer hardCancel()
			}
		}

		if ce.env.Executor != nil {
			result = ce.env.Executor.RunTask(attemptCtx, task, j.Agent)
		} else {
			result = dispatch.TaskResult{
				ID: task.ID, Name: task.Name, Status: "error",
				Error: "no executor configured",
			}
		}

		if result.Status == "success" {
			break
		}

		if ctx.Err() != nil {
			break
		}
	}

	// Record final result.
	if ce.env.RecordHistory != nil {
		ce.env.RecordHistory(ce.cfg.HistoryDB, j.ID, j.Name, jobSource, j.Agent, task, result,
			jobStart.Format(time.RFC3339), time.Now().Format(time.RFC3339), result.OutputFile)
	}

	if ce.env.RecordSessionActivity != nil {
		ce.env.RecordSessionActivity(ce.cfg.HistoryDB, task, result, j.Agent)
	}

	ce.mu.Lock()
	j.lastRun = time.Now()
	j.lastCost = result.CostUSD

	if result.Status == "success" || result.Status == history.StatusSkippedConcurrentLimit {
		j.errors = 0
		j.lastErr = ""
	} else {
		j.errors++
		j.lastErr = result.Error
		if j.errors >= 3 {
			j.Enabled = false
			log.Warn("cron job auto-disabled", "jobId", j.ID, "consecutiveErrors", j.errors)
			if ce.notifyFn != nil {
				ce.notifyFn(fmt.Sprintf("Cron job %q auto-disabled after %d errors.\nLast error: %s",
					j.Name, j.errors, truncate(j.lastErr, 200)))
			}
		}
	}
	ce.mu.Unlock()

	// Notifications.
	cronNotifyEnabled := ce.cfg.CronNotify == nil || *ce.cfg.CronNotify
	if cronNotifyEnabled {
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

			qcfg := ce.quietCfg()
			if quiet.IsQuietHours(qcfg) {
				if ce.cfg.QuietHours.Digest && ce.env.QuietGlobal != nil {
					ce.env.QuietGlobal.Enqueue(msg)
				}
			} else {
				ce.notifyFn(msg)
			}
		}

		if j.NotifyChannel != "" {
			channelName := j.NotifyChannel
			if strings.HasPrefix(channelName, "discord:") {
				channelName = strings.TrimPrefix(channelName, "discord:")
			}
			elapsed := time.Duration(result.DurationMs) * time.Millisecond
			statusIcon := "✅"
			if result.Status != "success" {
				statusIcon = "❌"
			}
			msg := fmt.Sprintf("%s **%s** completed\nCost: $%.4f | Duration: %s",
				statusIcon, j.Name, result.CostUSD, elapsed.Round(time.Second))
			if strings.HasPrefix(channelName, "id:") {
				channelID := strings.TrimPrefix(channelName, "id:")
				if ce.cfg.Discord.Enabled && ce.cfg.Discord.BotToken != "" {
					if err := discordSendBotChannel(ce.cfg.Discord.BotToken, channelID, msg); err != nil {
						log.WarnCtx(ctx, "cron discord notify failed", "jobId", j.ID, "channel", channelID, "error", err)
					}
				}
			} else {
				for _, ch := range ce.cfg.Notifications {
					if ch.Type == "discord" && ch.Name == channelName {
						if err := discordSendWebhook(ch.WebhookURL, msg); err != nil {
							log.WarnCtx(ctx, "cron discord notify failed", "jobId", j.ID, "channel", channelName, "error", err)
						}
						break
					}
				}
			}
		}

		if ce.env.SendWebhooks != nil {
			ce.env.SendWebhooks(ce.cfg, result.Status, webhook.Payload{
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
		}
	}

	// Chain trigger.
	var chainTargets []string
	if result.Status == "success" {
		chainTargets = j.OnSuccess
	} else {
		chainTargets = j.OnFailure
	}
	if len(chainTargets) > 0 {
		if j.chainDepth >= maxChainDepth {
			log.WarnCtx(ctx, "cron job chain depth max reached, skipping", "jobId", j.ID, "depth", j.chainDepth, "max", maxChainDepth)
		} else {
			for _, targetID := range chainTargets {
				log.InfoCtx(ctx, "cron job chain trigger", "jobId", j.ID, "target", targetID, "depth", j.chainDepth+1)
				audit.Log(ce.cfg.HistoryDB, "job.chain", "cron",
					fmt.Sprintf("%s → %s (depth=%d, trigger=%s)", j.ID, targetID, j.chainDepth+1, result.Status), "")
				if err := ce.runChainJob(ce.ctx, targetID, j.chainDepth+1); err != nil {
					log.ErrorCtx(ctx, "cron chain trigger failed", "target", targetID, "error", err)
				}
			}
		}
	}
}

// RunJobByID manually triggers a cron job.
func (ce *Engine) RunJobByID(_ context.Context, id string) error {
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
	maxRuns := target.effectiveMaxConcurrentRuns()
	if target.runCount >= maxRuns {
		ce.mu.Unlock()
		return fmt.Errorf("job %q already running %d/%d instances", id, target.runCount, maxRuns)
	}
	target.runCount++
	target.running = true
	jobCtx, jobCancel := context.WithCancel(ce.ctx)
	target.cancelFn = jobCancel
	ce.mu.Unlock()

	ce.jobWg.Add(1)
	go func() {
		defer ce.jobWg.Done()
		ce.runJob(jobCtx, target)
	}()
	return nil
}

func (ce *Engine) runChainJob(ctx context.Context, id string, depth int) error {
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
	maxRuns := target.effectiveMaxConcurrentRuns()
	if target.runCount >= maxRuns {
		ce.mu.Unlock()
		return fmt.Errorf("job %q already running %d/%d instances", id, target.runCount, maxRuns)
	}
	target.runCount++
	target.running = true
	target.chainDepth = depth
	jobCtx, jobCancel := context.WithCancel(ctx)
	target.cancelFn = jobCancel
	ce.mu.Unlock()

	ce.jobWg.Add(1)
	go func() {
		defer ce.jobWg.Done()
		ce.runJob(jobCtx, target)
		ce.mu.Lock()
		target.chainDepth = 0
		ce.mu.Unlock()
	}()
	return nil
}

// CancelJob cancels a currently running job.
func (ce *Engine) CancelJob(id string) error {
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

// ApproveJob approves a pending job.
func (ce *Engine) ApproveJob(id string) error {
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

// RejectJob rejects a pending job.
func (ce *Engine) RejectJob(id string) error {
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

// ToggleJob enables or disables a job.
func (ce *Engine) ToggleJob(id string, enabled bool) error {
	ce.mu.Lock()
	defer ce.mu.Unlock()
	for _, j := range ce.jobs {
		if j.ID == id {
			j.Enabled = enabled
			if enabled {
				j.errors = 0
				j.nextRun = NextRunAfter(j.expr, j.loc, time.Now().In(j.loc))
			}
			return nil
		}
	}
	return fmt.Errorf("job %q not found", id)
}

// JobInfo is a read-only snapshot of a cron job for display/API.
type JobInfo struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Enabled  bool      `json:"enabled"`
	Schedule string    `json:"schedule"`
	TZ       string    `json:"tz"`
	Agent    string    `json:"agent"`
	Running  bool      `json:"running"`
	RunCount int       `json:"runCount"`
	MaxConcurrentRuns int `json:"maxConcurrentRuns"`
	NextRun  time.Time `json:"nextRun"`
	LastRun  time.Time `json:"lastRun"`
	LastErr    string    `json:"lastErr,omitempty"`
	LastCost   float64   `json:"lastCost"`
	AvgCost    float64   `json:"avgCost"`
	Errors     int       `json:"errors"`
	OnSuccess    []string  `json:"onSuccess,omitempty"`
	OnFailure    []string  `json:"onFailure,omitempty"`
	IdleMinHours   int       `json:"idleMinHours,omitempty"`
	Trigger        string    `json:"trigger,omitempty"`
	IdleMinMinutes int       `json:"idleMinMinutes,omitempty"`
	CooldownHours  float64   `json:"cooldownHours,omitempty"`
	RunStart       time.Time `json:"runStart,omitempty"`
	RunElapsed string    `json:"runElapsed,omitempty"`
	RunTimeout string    `json:"runTimeout,omitempty"`
	RunModel   string    `json:"runModel,omitempty"`
	RunPrompt  string    `json:"runPrompt,omitempty"`
}

// ListJobs returns a snapshot of all cron jobs.
func (ce *Engine) ListJobs() []JobInfo {
	ce.mu.RLock()
	defer ce.mu.RUnlock()

	var infos []JobInfo
	for _, j := range ce.jobs {
		info := JobInfo{
			ID:                j.ID,
			Name:              j.Name,
			Enabled:           j.Enabled,
			Schedule:          j.Schedule,
			TZ:                j.TZ,
			Agent:             j.Agent,
			Running:           j.running,
			RunCount:          j.runCount,
			MaxConcurrentRuns: j.effectiveMaxConcurrentRuns(),
			NextRun:           j.nextRun,
			LastRun:           j.lastRun,
			LastErr:           j.lastErr,
			LastCost:          j.lastCost,
			AvgCost:           history.QueryJobAvgCost(ce.cfg.HistoryDB, j.ID),
			Errors:            j.errors,
			OnSuccess:         j.OnSuccess,
			OnFailure:         j.OnFailure,
			IdleMinHours:      j.IdleMinHours,
			Trigger:           j.Trigger,
			IdleMinMinutes:    j.IdleMinMinutes,
			CooldownHours:     j.CooldownHours,
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

// GetJobConfig returns the full JobConfig for a job by ID.
func (ce *Engine) GetJobConfig(id string) *JobConfig {
	ce.mu.RLock()
	defer ce.mu.RUnlock()
	for _, j := range ce.jobs {
		if j.ID == id {
			c := j.JobConfig
			return &c
		}
	}
	return nil
}

// --- Job CRUD ---

// AddJob adds a new cron job and saves to file.
func (ce *Engine) AddJob(jc JobConfig) error {
	expr, err := Parse(jc.Schedule)
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
		JobConfig: jc,
		expr:      expr,
		loc:       loc,
	}
	j.nextRun = NextRunAfter(j.expr, j.loc, time.Now().In(j.loc))
	ce.jobs = append(ce.jobs, j)

	if err := ce.saveToFileLocked(); err != nil {
		return fmt.Errorf("save: %w", err)
	}
	log.Info("cron added job", "jobId", jc.ID)
	return nil
}

// UpdateJob updates an existing job config and saves to file.
func (ce *Engine) UpdateJob(id string, jc JobConfig) error {
	expr, err := Parse(jc.Schedule)
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

			j.JobConfig = jc
			j.JobConfig.ID = id
			j.expr = expr
			j.loc = loc
			j.nextRun = NextRunAfter(j.expr, j.loc, time.Now().In(j.loc))
			j.errors = 0
			j.lastErr = ""

			if err := ce.saveToFileLocked(); err != nil {
				return fmt.Errorf("save: %w", err)
			}
			log.Info("cron updated job", "jobId", id)
			return nil
		}
	}
	return fmt.Errorf("job %q not found", id)
}

// RemoveJob removes a job and saves to file.
func (ce *Engine) RemoveJob(id string) error {
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
	log.Info("cron removed job", "jobId", id)
	return nil
}

func (ce *Engine) saveToFileLocked() error {
	jf := JobsFile{}
	for _, j := range ce.jobs {
		jf.Jobs = append(jf.Jobs, j.JobConfig)
	}
	data, err := json.MarshalIndent(jf, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(ce.cfg.JobsFile, append(data, '\n'), 0o600)
}

// --- Startup Replay ---

// StartupReplay detects jobs that missed their schedule while the daemon was down
// and schedules a single catch-up run (most recent missed slot) after 30s.
func (ce *Engine) StartupReplay(ctx context.Context) {
	hours := ce.cfg.CronReplayHours
	if hours <= 0 {
		hours = 4
	}
	if ce.cfg.HistoryDB == "" {
		return
	}

	now := time.Now()
	from := now.Add(-time.Duration(hours) * time.Hour)

	ce.mu.RLock()
	jobs := make([]*cronJob, len(ce.jobs))
	copy(jobs, ce.jobs)
	ce.mu.RUnlock()

	var missed []*cronJob
	for _, j := range jobs {
		if !j.Enabled {
			continue
		}
		if j.IdleMinHours > 0 || j.RequireApproval || j.Trigger == "idle" {
			continue
		}
		if j.ID == "daily_notes" || j.ID == "backlog-triage" {
			continue
		}

		scheduledAt := ce.mostRecentScheduledTime(j, from, now)
		if scheduledAt.IsZero() {
			continue
		}

		if history.CronExecLogExists(ce.cfg.HistoryDB, j.ID, scheduledAt) {
			continue
		}
		if history.JobRunExistsNear(ce.cfg.HistoryDB, j.ID, scheduledAt) {
			continue
		}

		log.Info("cron startup replay: missed run detected",
			"jobId", j.ID, "name", j.Name, "scheduledAt", scheduledAt.Format(time.RFC3339))
		missed = append(missed, j)
	}

	if len(missed) == 0 {
		return
	}

	log.Info("cron startup replay: scheduling missed jobs", "count", len(missed))

	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}

		for _, j := range missed {
			select {
			case <-ctx.Done():
				return
			default:
			}

			ce.mu.Lock()
			maxRuns := j.effectiveMaxConcurrentRuns()
			if j.runCount >= maxRuns {
				ce.mu.Unlock()
				log.Info("cron startup replay: job already running max instances, skipping",
					"jobId", j.ID, "running", j.runCount, "maxConcurrentRuns", maxRuns)
				continue
			}
			j.runCount++
			j.running = true
			j.replayed = true
			jobCtx, jobCancel := context.WithCancel(ctx)
			j.cancelFn = jobCancel
			ce.mu.Unlock()

			log.Info("cron startup replay: launching missed job", "jobId", j.ID, "name", j.Name)
			ce.jobWg.Add(1)
			go func(jj *cronJob) {
				defer ce.jobWg.Done()
				ce.runJob(jobCtx, jj)
				ce.mu.Lock()
				jj.replayed = false
				ce.mu.Unlock()
			}(j)
		}
	}()
}

func (ce *Engine) mostRecentScheduledTime(j *cronJob, from, to time.Time) time.Time {
	var last time.Time
	t := from.In(j.loc).Add(-time.Minute)
	for {
		next := NextRunAfter(j.expr, j.loc, t)
		if next.IsZero() || next.After(to) {
			break
		}
		last = next
		t = next
	}
	return last
}

// --- Discord helpers (self-contained, no root dependency) ---

func discordSendBotChannel(botToken, channelID, msg string) error {
	if len(msg) > 2000 {
		msg = msg[:1997] + "..."
	}
	payload, err := json.Marshal(map[string]string{"content": msg})
	if err != nil {
		return err
	}
	url := fmt.Sprintf("https://discord.com/api/v10/channels/%s/messages", channelID)
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("POST", url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bot "+botToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("Discord returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func discordSendWebhook(webhookURL, msg string) error {
	payload, err := json.Marshal(map[string]string{"content": msg})
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(webhookURL, "application/json", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("Discord returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// --- Helpers ---

func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen < 4 {
		return string(runes[:maxLen])
	}
	return string(runes[:maxLen-3]) + "..."
}
