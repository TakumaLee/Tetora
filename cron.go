package main

import (
	"context"
	"time"

	"tetora/internal/cron"
	dtypes "tetora/internal/dispatch"
	"tetora/internal/quiet"
	"tetora/internal/webhook"
)

// --- Type aliases (internal/cron is canonical) ---

// CronEngine is the cron scheduler. Root package uses this alias so existing
// callers (app.go, discord.go, health.go, wire_*.go, etc.) continue to compile
// without change. All logic lives in internal/cron.Engine.
type CronEngine = cron.Engine

// CronJobConfig is the persisted configuration for a single cron job.
type CronJobConfig = cron.JobConfig

// CronTaskConfig holds the execution parameters for a cron task.
type CronTaskConfig = cron.TaskConfig

// CronJobInfo is a read-only snapshot of a cron job for display/API.
type CronJobInfo = cron.JobInfo

// JobsFile is the top-level structure of jobs.json.
type JobsFile = cron.JobsFile

// --- Quiet hours (root-only global, used by tick and Telegram) ---

var quietGlobal = quiet.NewState(func(msg string, kv ...any) {})

func toQuietCfg(cfg *Config) quiet.Config {
	return quiet.Config{
		Enabled: cfg.QuietHours.Enabled,
		Start:   cfg.QuietHours.Start,
		End:     cfg.QuietHours.End,
		TZ:      cfg.QuietHours.TZ,
		Digest:  cfg.QuietHours.Digest,
	}
}

// newCronEngine constructs a CronEngine (cron.Engine) wired with all root-
// package callbacks that the internal cron package cannot import directly.
func newCronEngine(cfg *Config, sem, childSem chan struct{}, notifyFn func(string)) *CronEngine {
	env := cron.Env{
		Executor: dtypes.TaskExecutorFunc(func(ctx context.Context, task dtypes.Task, agentName string) dtypes.TaskResult {
			return runSingleTask(ctx, cfg, task, sem, childSem, agentName)
		}),

		FillDefaults: func(c *Config, t *dtypes.Task) {
			fillDefaults(c, t)
		},

		LoadAgentPrompt: func(c *Config, agentName string) (string, error) {
			return loadAgentPrompt(c, agentName)
		},

		ResolvePromptFile: func(c *Config, promptFile string) (string, error) {
			return resolvePromptFile(c, promptFile)
		},

		ExpandPrompt: func(prompt, jobID, dbPath, agentName, knowledgeDir string, c *Config) string {
			return expandPrompt(prompt, jobID, dbPath, agentName, knowledgeDir, c)
		},

		RecordHistory: func(dbPath, jobID, name, source, role string, task dtypes.Task, result dtypes.TaskResult, startedAt, finishedAt, outputFile string) {
			recordHistory(dbPath, jobID, name, source, role, task, result, startedAt, finishedAt, outputFile)
		},

		RecordSessionActivity: func(dbPath string, task dtypes.Task, result dtypes.TaskResult, role string) {
			recordSessionActivity(dbPath, task, result, role)
		},

		TriageBacklog: func(ctx context.Context, c *Config, s, cs chan struct{}) {
			triageBacklog(ctx, c, s, cs)
		},

		RunDailyNotesJob: func(ctx context.Context, c *Config) error {
			return runDailyNotesJob(ctx, c)
		},

		SendWebhooks: func(c *Config, event string, payload webhook.Payload) {
			sendWebhooks(c, event, payload)
		},

		NewUUID: newUUID,

		RegisterWorkerOrigin: func(sessionID, taskID, taskName, source, agent, jobID string) {
			if cfg.Runtime.HookRecv == nil {
				return
			}
			cfg.Runtime.HookRecv.(*hookReceiver).RegisterOrigin(sessionID, &workerOrigin{
				TaskID:   taskID,
				TaskName: taskName,
				Source:   source,
				Agent:    agent,
				JobID:    jobID,
			})
		},

		NotifyKeyboard: func(jobName, schedule string, approvalTimeout time.Duration, jobID string) {
			// Telegram keyboard notification is wired in wire_telegram.go via
			// the notifyKeyboardFn on the telegramRuntime, not directly here.
			// For now, fall back to plain text notification.
			if notifyFn != nil {
				notifyFn("Job \"" + jobName + "\" requires approval. /approve " + jobID + " or /reject " + jobID)
			}
		},

		QuietCfg: func(c *Config) quiet.Config {
			return toQuietCfg(c)
		},

		QuietGlobal: quietGlobal,
	}

	return cron.NewEngine(cfg, sem, childSem, notifyFn, env)
}
