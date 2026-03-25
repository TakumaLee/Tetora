# Ops Runbook: Job Fail Investigation

This runbook covers how to investigate cron job failures, identify root causes, and resolve recurring failure patterns.

---

## Quick orientation

When a job fails, you have three entry points:

| Command | What it shows |
|---|---|
| `tetora history fails` | All non-success runs in the last 3 days |
| `tetora history trace <job-id>` | Per-job run history with consecutive fail streak marker |
| `tetora health` | System-wide overview including "Fail Streaks" alert |

---

## Step 1: Check for streaks at a glance

```bash
tetora health
```

The `Fail Streaks` line tells you immediately if any job has failed 3+ consecutive times:

```
  ⚠️  Fail Streaks     2 job(s) failing 3+ consecutive: daily-digest (5x), weekly-retro (3x)
  ✅  Fail Streaks     no jobs failing 3+ consecutive
```

For machine-readable output (e.g. in scripts or monitoring):

```bash
tetora health --json | jq '.consecutive_fails'
```

---

## Step 2: List recent failures

```bash
# All failures in the last 3 days
tetora history fails

# Failures for a specific job
tetora history fails --job <job-id>

# Extend window (e.g. 7 days) and cap output
tetora history fails --days 7 --limit 50
```

Output columns: `ID | NAME | STATUS | TIME | ERROR`

Statuses to watch:

| Status | Meaning |
|---|---|
| `error` | Agent exited non-zero or threw an exception |
| `timeout` | Hit the job's `timeout` setting (default 30 min) |
| `skipped_concurrent_limit` | Another instance was already running; this trigger was dropped |

---

## Step 3: Trace a specific job

Once you know which job is problematic, trace its full run history:

```bash
tetora history trace <job-id>
```

Example output:

```
Job: daily-digest (cron-digest-001)
  #1042    error    14:05:02  API timeout connecting to Supabase  <- streak: 5
  #1038    error    13:05:01  API timeout connecting to Supabase
  #1034    error    12:05:03  API timeout connecting to Supabase
  #1030    error    11:05:00  API timeout connecting to Supabase
  #1027    error    10:05:02  API timeout connecting to Supabase
  #1024    success  09:05:01
```

The `<- streak: N` marker pinpoints the start of the streak. Runs above the marker are the consecutive failures; the first `success` below is the last known good run.

Options:

```bash
# Show last 20 runs instead of 10
tetora history trace <job-id> --limit 20

# Target a specific client DB
tetora history trace <job-id> --client <client-id>
```

---

## Step 4: Inspect a specific run

Use the run ID from `fails` or `trace` output to get full detail:

```bash
tetora history show <run-id>
```

This shows the full `error` field and `output_summary` without truncation.

---

## Step 5: Identify root cause by status type

### `error` — agent threw / non-zero exit

1. Read the `error` field from `history show <run-id>`.
2. Check the session output file if recorded (`output_file` field).
3. Common causes:
   - External API down (check error message for HTTP 5xx / connection refused)
   - Prompt too large — enable session compaction in `config.json`
   - Bug in agent task description causing the agent to crash

### `timeout`

1. Check the job's `timeout` setting in `jobs.json`.
2. Check how long previous successful runs took (`history trace` shows timestamps).
3. Common causes:
   - Upstream API latency increased
   - Task scope grew (e.g. more data to process)
   - Agent stuck in a loop — increase timeout OR reduce task scope

### `skipped_concurrent_limit`

This means the previous run was still active when the next cron trigger fired.

1. Run `tetora history trace <job-id>` and compare `started_at` to `finished_at` for recent runs.
2. If duration consistently exceeds the cron interval, the job is underprovisioned for its frequency.
3. Fix options:
   - Reduce frequency in `jobs.json` (e.g. `0 */2 * * *` instead of `0 * * * *`)
   - Increase the job's `timeout` if it just needs more time
   - Split the job into smaller independent jobs

---

## Step 6: Remediate

### Retry a failed task

```bash
# Move task back to todo (re-queues for next available worker)
tetora task move <task-id> --status=todo
```

### Re-trigger a cron job manually

```bash
tetora job run <job-id>
```

### Disable a broken job temporarily

Edit `jobs.json` and set `"enabled": false`, then reload:

```bash
tetora reload
```

---

## Monitoring integration

The `tetora health` command and `tetora health --json` both include consecutive fail streak data. Use these in your monitoring scripts:

```bash
# Alert if any job has 3+ consecutive fails
streaks=$(tetora health --json | jq '.consecutive_fails | length')
if [ "$streaks" -gt 0 ]; then
  echo "ALERT: $streaks job(s) in consecutive fail streak"
  tetora health --json | jq -r '.consecutive_fails[] | "\(.name): \(.streak)x"'
fi
```

---

## Reference

- History DB location: `historyDB` field in `~/.tetora/config.json`
- Job definitions: `jobsFile` field in `~/.tetora/config.json` (default `~/.tetora/jobs.json`)
- Log files: `~/.tetora/logs/`
- `tetora history` subcommands: `list`, `show`, `cost`, `fails`, `trace`
