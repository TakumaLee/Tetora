---
title: "Task Dependencies and Chaining — Build Smarter Agent Workflows"
lang: en
date: "2026-04-04"
excerpt: "Dispatch a second agent only after the first one succeeds. Chain tasks with dependencies to build reliable multi-step pipelines."
description: "Learn how to chain Tetora agent tasks with dependencies so later steps only run when earlier ones succeed — building reliable, automated multi-step workflows."
---

## The Problem: Sequential Steps That Shouldn't Run in Parallel

Some workflows have a natural order. You want to run tests *before* deploying, scan security *after* building, or post a report *only if* an analysis completes cleanly. If you dispatch all steps at once without dependencies, they race — and you get broken deploys, false-positive alerts, or reports built on incomplete data.

Tetora lets you express these relationships explicitly through **task chaining with dependencies**.

## How Task Chaining Works

When you dispatch a task, you can pass a `dependsOn` field referencing one or more task IDs. Tetora's scheduler holds the dependent task in a `waiting` state and only enqueues it when the upstream task reaches `done`.

```json
// dispatch payload example
{
  "task": "deploy-to-staging",
  "agent": "kokuyou",
  "dependsOn": ["task-abc123"],
  "onFailure": "abort"
}
```

- `dependsOn` — array of task IDs that must complete first
- `onFailure: "abort"` — if any upstream task fails, skip this step entirely
- `onFailure: "continue"` — run regardless (useful for cleanup or notification steps)

## A Real Example: Test → Deploy → Notify

Here's a three-step pipeline where each stage waits for the previous one:

```bash
# Step 1: run tests (no dependencies)
tetora dispatch --task "run-test-suite" --agent hisui --output task-id

# Step 2: deploy only if tests pass
tetora dispatch --task "deploy-staging" --agent kokuyou \
  --depends-on $(cat task-id) \
  --on-failure abort

# Step 3: post a Discord summary regardless of deploy outcome
tetora dispatch --task "notify-team" --agent kohaku \
  --depends-on $(cat task-id) \
  --on-failure continue
```

The test agent runs first. If tests fail, the deploy is automatically aborted. The notification step always fires — your team gets an update either way.

## Why This Matters

Modern CI systems (GitHub Actions, Buildkite) have done this for years with workflow YAML. Tetora brings the same logic to **agent-native pipelines** — where the "steps" aren't shell scripts but full agents with their own memory, tools, and reasoning.

The result: you can build pipelines that are resilient to failure, self-documenting, and easy to debug because every step's status is tracked individually.

## Quick Tips

- Chain more than two steps freely — dependencies form a DAG, not just a linear list
- Use `tetora task status <id>` to inspect which step is waiting, running, or blocked
- Combine with cron triggers for fully automated nightly pipelines
- Always set `onFailure` explicitly — the default is `abort`, but it's better to be intentional

Start small: add one dependency to an existing dispatch and watch how the pipeline self-organizes around it.
