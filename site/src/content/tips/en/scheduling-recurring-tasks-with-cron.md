---
title: "Scheduling Recurring Tasks with Cron — Automate Agent Workflows on a Timer"
lang: en
date: "2026-04-09"
excerpt: "Stop manually triggering agents every morning. Use Tetora's cron scheduler to run recurring tasks on a time-based schedule, automatically."
description: "Learn how to schedule recurring agent tasks in Tetora using cron syntax. Set up daily reports, weekly summaries, and time-triggered dispatches without any manual work."
---

## The Problem

Some tasks should just *happen* — daily market scans, weekly content summaries, hourly health checks. Manually dispatching these every day is friction you don't need. Worse, you'll forget.

Tetora's cron scheduler lets you attach a time trigger to any dispatch task. The daemon wakes it up on schedule, no babysitting required.

## How Cron Scheduling Works

Under the hood, Tetora uses standard cron expressions to define when a task runs. You pair a cron string with a task spec, and the scheduler handles the rest.

```yaml
# .tetora/crons/daily-market-scan.yaml
schedule: "0 8 * * 1-5"   # 08:00, Mon–Fri
agent: midori
task: "Run the daily market scan and post results to Discord #money-lab"
context:
  output_channel: "discord:money-lab"
  markets: ["tw.main", "polymarket"]
enabled: true
```

Drop that file in `.tetora/crons/` and the scheduler picks it up on its next reload cycle (or immediately after a `SIGHUP`).

## Setting Up Your First Cron Task

**Step 1 — Write the cron spec:**

```bash
mkdir -p .tetora/crons

cat > .tetora/crons/weekly-content-summary.yaml << 'EOF'
schedule: "0 9 * * 1"    # Every Monday at 09:00
agent: kohaku
task: "Summarize last week's published content and suggest 3 topics for this week"
context:
  source_dir: "site/src/content/tips"
  output: "drafts/weekly-summary.md"
enabled: true
EOF
```

**Step 2 — Reload the scheduler:**

```bash
tetora cron reload
# Loaded 1 new cron: weekly-content-summary (next run: Mon 2026-04-13 09:00)
```

**Step 3 — Verify:**

```bash
tetora cron list
# NAME                     SCHEDULE       AGENT    NEXT RUN
# weekly-content-summary   0 9 * * 1      kohaku   2026-04-13 09:00
# daily-market-scan        0 8 * * 1-5    midori   2026-04-10 08:00
```

## Cron Expression Quick Reference

| Expression | Meaning |
|---|---|
| `0 8 * * 1-5` | 08:00, weekdays only |
| `0 9 * * 1` | Monday 09:00 |
| `*/30 * * * *` | Every 30 minutes |
| `0 0 * * *` | Midnight every day |
| `0 12 1 * *` | Noon on the 1st of every month |

Not sure about your expression? Use [crontab.guru](https://crontab.guru) to validate before committing.

## Disabling Without Deleting

Set `enabled: false` in the spec to pause a cron without removing the file:

```yaml
enabled: false   # paused — easy to re-enable later
```

Run `tetora cron reload` after any change.

## Key Takeaway

Cron scheduling turns one-off dispatches into reliable, automated workflows. Start with one daily task — a market scan, a content check, or a report — and watch it run itself. Once it's stable, layer in more. The goal is an agent team that shows up for work before you do.

Previously: **Auto-Dispatch Basics** — queue tasks manually. Now with cron, the queue fills itself.
