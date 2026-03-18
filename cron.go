package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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

// ============================================================
// Merged from cron_expr.go
// ============================================================

type cronExpr = cron.Expr

func parseCronExpr(s string) (cronExpr, error) {
	return cron.Parse(s)
}

func nextRunAfter(expr cronExpr, loc *time.Location, after time.Time) time.Time {
	return cron.NextRunAfter(expr, loc, after)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func seedDefaultJobs() []CronJobConfig {
	return []CronJobConfig{
		{
			ID:           "self-improve",
			Name:         "Self-Improvement",
			Enabled:      true,
			Schedule:     "0 3 */2 * *",
			TZ:           "Asia/Taipei",
			IdleMinHours: 2,
			Task: CronTaskConfig{
				Prompt: `You are a self-improvement agent for the Tetora AI orchestration system.

Analyze the activity digest below. The digest includes existing Skills, Rules, and Memory —
do NOT create anything that already exists.

## Instructions
1. Identify repeated patterns (3+ occurrences), low-score reflections, recurring failures
2. For each actionable improvement, CREATE the file directly:
   - **Rule**: Create ` + "`rules/{name}.md`" + ` — governance rules auto-injected into all agents
   - **Memory**: Create/update ` + "`memory/{key}.md`" + ` — shared observations
   - **Skill**: Create ` + "`skills/{name}/metadata.json`" + ` with ` + "`\"approved\": false`" + ` — requires human review
3. Only apply HIGH and MEDIUM priority improvements
4. Keep files concise and actionable
5. Report what you created and why

If insufficient data for improvements, say so and exit.

---

{{review.digest:7}}`,
				Model:          "sonnet",
				Timeout:        "5m",
				Budget:         1.5,
				PermissionMode: "acceptEdits",
			},
			Notify:     true,
			MaxRetries: 1,
			RetryDelay: "2m",
		},
		{
			ID:       "backlog-triage",
			Name:     "Backlog Triage",
			Enabled:  true,
			Schedule: "50 9 * * *",
			TZ:       "Asia/Taipei",
			Task:     CronTaskConfig{},
			Notify:   true,
		},
	}
}

func cronDiscordSendBotChannel(botToken, channelID, msg string) error {
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

func cronDiscordSendWebhook(webhookURL, msg string) error {
	if len(msg) > 2000 {
		msg = msg[:1997] + "..."
	}
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
