package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"tetora/internal/cron"
	"tetora/internal/history"
)

// cronExpr is an alias for cron.Expr for backward compatibility.
type cronExpr = cron.Expr

// parseCronExpr delegates to cron.Parse.
func parseCronExpr(s string) (cronExpr, error) {
	return cron.Parse(s)
}

// nextRunAfter delegates to cron.NextRunAfter.
func nextRunAfter(expr cronExpr, loc *time.Location, after time.Time) time.Time {
	return cron.NextRunAfter(expr, loc, after)
}

// --- Helpers ---

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// seedDefaultJobs returns the default cron jobs for new installations.
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

// cronDiscordSendBotChannel sends a message to a Discord channel via bot token.
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

// --- Startup Replay ---

// startupReplay detects cron jobs that should have run while the daemon was down
// and schedules a single catch-up run (the most recent missed slot only) after a
// 30-second delay to avoid startup conflicts.
//
// A job is considered "missed" when:
//   - Its schedule expression places at least one trigger in [now-replayHours, now]
//   - cron_execution_log has no entry for that job near the computed scheduled time
//   - job_runs also has no entry (backward-compat fallback for existing DBs)
//
// Jobs with RequireApproval, IdleMinHours>0, or the built-in special jobs
// (daily_notes, backlog-triage) are excluded from replay.
func (ce *CronEngine) startupReplay(ctx context.Context) {
	hours := ce.cfg.CronReplayHours
	if hours <= 0 {
		hours = 2
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
		// Skip jobs that shouldn't run unsupervised.
		if j.IdleMinHours > 0 || j.RequireApproval || j.Trigger == "idle" {
			continue
		}
		// Skip built-in special jobs with custom dispatch logic.
		if j.ID == "daily_notes" || j.ID == "backlog-triage" {
			continue
		}

		scheduledAt := ce.mostRecentScheduledTime(j, from, now)
		if scheduledAt.IsZero() {
			continue // no trigger in window
		}

		// Check cron_execution_log first (authoritative from this version onwards).
		if history.CronExecLogExists(ce.cfg.HistoryDB, j.ID, scheduledAt) {
			continue // job ran or was a zombie — skip
		}
		// Fallback: check job_runs for DBs upgraded before cron_execution_log existed.
		if history.JobRunExistsNear(ce.cfg.HistoryDB, j.ID, scheduledAt) {
			continue
		}

		logInfo("cron startup replay: missed run detected",
			"jobId", j.ID, "name", j.Name, "scheduledAt", scheduledAt.Format(time.RFC3339))
		missed = append(missed, j)
	}

	if len(missed) == 0 {
		return
	}

	logInfo("cron startup replay: scheduling missed jobs", "count", len(missed))

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
				logInfo("cron startup replay: job already running max instances, skipping",
					"jobId", j.ID, "running", j.runCount, "maxConcurrentRuns", maxRuns)
				continue
			}
			j.runCount++
			j.running = true
			j.replayed = true
			jobCtx, jobCancel := context.WithCancel(ctx)
			j.cancelFn = jobCancel
			ce.mu.Unlock()

			logInfo("cron startup replay: launching missed job", "jobId", j.ID, "name", j.Name)
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

// mostRecentScheduledTime returns the most recent time in [from, to] that the
// job's cron expression would have triggered. Returns zero time if none.
func (ce *CronEngine) mostRecentScheduledTime(j *cronJob, from, to time.Time) time.Time {
	var last time.Time
	// nextRunAfter starts from t+1min, so subtract 1min to include 'from' itself.
	t := from.In(j.loc).Add(-time.Minute)
	for {
		next := nextRunAfter(j.expr, j.loc, t)
		if next.IsZero() || next.After(to) {
			break
		}
		last = next
		t = next // nextRunAfter will advance by 1min internally on next call
	}
	return last
}

// cronDiscordSendWebhook sends a plain message to a Discord webhook URL.
func cronDiscordSendWebhook(webhookURL, msg string) error {
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
