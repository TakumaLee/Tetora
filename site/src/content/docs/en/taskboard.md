---
title: "Taskboard & Auto-Dispatch Guide"
lang: "en"
order: 4
description: "Track tasks, priorities, and agent assignments with the built-in taskboard."
---
# Taskboard & Auto-Dispatch Guide

## Overview

The Taskboard is Tetora's built-in kanban system for tracking and automatically executing tasks. It pairs a persistent task store (backed by SQLite) with an auto-dispatch engine that watches for ready tasks and hands them off to agents without manual intervention.

Typical use cases:

- Queue up a backlog of engineering tasks and let agents work through them overnight
- Route tasks to specific agents based on expertise (e.g. `kokuyou` for backend, `kohaku` for content)
- Chain tasks with dependency relationships so agents pick up where others left off
- Integrate task execution with git: automatic branch creation, commit, push, and PR/MR

**Requirements:** `taskBoard.enabled: true` in `config.json` and the Tetora daemon running.

---

## Task Lifecycle

Tasks flow through statuses in this order:

```
idea → needs-thought → backlog → todo → doing → review → done
                                                  ↓
                                           partial-done
                                                  ↓
                                              failed
```

| Status | Meaning |
|---|---|
| `idea` | Rough concept, not yet refined |
| `needs-thought` | Requires analysis or design before implementation |
| `backlog` | Defined and prioritized, but not yet scheduled |
| `todo` | Ready to execute — auto-dispatch will pick this up if an assignee is set |
| `doing` | Currently running |
| `review` | Execution finished, awaiting quality review |
| `done` | Completed and reviewed |
| `partial-done` | Execution succeeded but post-processing failed (e.g. git merge conflict). Recoverable. |
| `failed` | Execution failed or produced empty output. Will be retried up to `maxRetries`. |

Auto-dispatch picks up tasks with `status=todo`. If a task has no assignee, it is automatically assigned to `defaultAgent` (default: `ruri`). Tasks in `backlog` are triaged periodically by the configured `backlogAgent` (default: `ruri`) which promotes promising ones to `todo`.

---

## Creating Tasks

### CLI

```bash
# Minimal task (lands in backlog, unassigned)
tetora task create --title="Add rate limiting to API"

# With all options
tetora task create \
  --title="Refactor auth middleware" \
  --description="Split token validation into its own package. See ADR-14." \
  --priority=high \
  --assignee=kokuyou \
  --type=refactor

# List tasks
tetora task list
tetora task list --status=todo
tetora task list --assignee=kokuyou
tetora task list --project=api-v2

# Show a specific task
tetora task show task-abc123
tetora task show task-abc123 --full   # includes comments/thread

# Move a task manually
tetora task move task-abc123 --status=todo

# Assign to an agent
tetora task assign task-abc123 --assignee=kokuyou

# Add a comment (spec, context, log, or system type)
tetora task comment task-abc123 \
  --author=takuma \
  --content="Must pass existing test suite. Do not touch auth.go." \
  --type=spec
```

Task IDs are automatically generated in the format `task-<uuid>`. You can reference a task by its full ID or a short prefix — the CLI will suggest matches.

### HTTP API

```bash
# Create
curl -X POST http://localhost:8991/api/tasks \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Add rate limiting",
    "description": "Implement token bucket per API key",
    "priority": "high",
    "assignee": "kokuyou",
    "type": "feat"
  }'

# List (filter by status)
curl "http://localhost:8991/api/tasks?status=todo"

# Move to a new status
curl -X PATCH http://localhost:8991/api/tasks/task-abc123 \
  -H "Content-Type: application/json" \
  -d '{"status": "todo"}'
```

### Dashboard

Open the **Taskboard** tab in the dashboard (`http://localhost:8991/dashboard`). Tasks are displayed in kanban columns. Drag cards between columns to change status, click a card to open the detail panel with comments and diff view.

---

## Auto-Dispatch

Auto-dispatch is the background loop that picks up `todo` tasks and runs them through agents.

### How it works

1. A ticker fires every `interval` (default: `5m`).
2. The scanner checks how many tasks are currently running. If `activeCount >= maxConcurrentTasks`, the scan is skipped.
3. For each `todo` task with an assignee, the task is dispatched to that agent. Unassigned tasks are auto-assigned to `defaultAgent`.
4. When a task finishes, an immediate re-scan fires so the next batch starts without waiting for the full interval.
5. On daemon startup, orphaned `doing` tasks from a previous crash are either restored to `done` (if there is completion evidence) or reset to `todo` (if truly orphaned).

### Dispatch Flow

```
                          ┌─────────┐
                          │  idea   │  (manual concept entry)
                          └────┬────┘
                               ▼
                       ┌──────────────┐
                       │ needs-thought │  (requires analysis)
                       └───────┬──────┘
                               ▼
  ┌──────────────────────────────────────────────────────────┐
  │                       backlog                             │
  │                                                           │
  │  Triage (backlogAgent, default: ruri) runs periodically:  │
  │   • "ready"     → assign agent → move to todo             │
  │   • "decompose" → create subtasks → parent to doing       │
  │   • "clarify"   → add question comment → stay in backlog  │
  │                                                           │
  │  Fast-path: already has assignee + no blocking deps       │
  │   → skip LLM triage, promote directly to todo             │
  └──────────────────────┬───────────────────────────────────┘
                         ▼
  ┌──────────────────────────────────────────────────────────┐
  │                        todo                               │
  │                                                           │
  │  Auto-dispatch picks up tasks every scan cycle:           │
  │   • Has assignee       → dispatch to that agent           │
  │   • No assignee        → assign defaultAgent, then run    │
  │   • Has workflow       → run through workflow pipeline     │
  │   • Has dependsOn      → wait until deps are done         │
  │   • Resumable prev run → resume from checkpoint           │
  └──────────────────────┬───────────────────────────────────┘
                         ▼
  ┌──────────────────────────────────────────────────────────┐
  │                       doing                               │
  │                                                           │
  │  Agent executes task (single prompt or workflow DAG)       │
  │                                                           │
  │  Guard: stuckThreshold (default 2h)                       │
  │   • If workflow still running → refresh timestamp          │
  │   • If truly stuck            → reset to todo              │
  └────────┬──────────┬──────────┬──────────────────────────┘
           │          │          │
     success    partial failure  failure
           │          │          │
           ▼          ▼          ▼
       ┌────────┐ ┌──────────┐ ┌────────┐
       │ review │ │ partial- │ │ failed │
       │        │ │   done   │ │        │
       └───┬────┘ └────┬─────┘ └───┬────┘
           │           │           │
           │     Resume button     │  Retry (up to maxRetries)
           │     in dashboard      │  or escalate
           ▼                       ▼
       ┌────────┐            ┌──────────┐
       │  done  │            │ escalate │
       └────────┘            │ to human │
                             └──────────┘
```

### Triage Details

Triage runs every `backlogTriageInterval` (default: `1h`) and is performed by the `backlogAgent` (default: `ruri`). The agent receives each backlog task with its comments and available agent roster, then decides:

| Action | Effect |
|---|---|
| `ready` | Assigns a specific agent and promotes to `todo` |
| `decompose` | Creates subtasks (with assignees), parent moves to `doing` |
| `clarify` | Adds a question as a comment, task stays in `backlog` |

**Fast-path**: Tasks that already have an assignee and no blocking dependencies skip LLM triage entirely and are promoted to `todo` immediately.

### Auto-Assignment

When a `todo` task has no assignee, the dispatcher automatically assigns it to `defaultAgent` (configurable, default: `ruri`). This prevents tasks from being silently stuck. The typical flow:

1. Task created without assignee → enters `backlog`
2. Triage promotes to `todo` (with or without assigning an agent)
3. If triage didn't assign → dispatcher assigns `defaultAgent`
4. Task executes normally

### Configuration

Add to `config.json`:

```json
{
  "taskBoard": {
    "enabled": true,
    "maxRetries": 3,
    "requireReview": true,
    "defaultWorkflow": "",
    "gitCommit": true,
    "gitPush": true,
    "gitPR": true,
    "gitWorktree": true,
    "autoDispatch": {
      "enabled": true,
      "interval": "5m",
      "maxConcurrentTasks": 3,
      "defaultAgent": "kokuyou",
      "backlogAgent": "ruri",
      "reviewAgent": "ruri",
      "escalateAssignee": "takuma",
      "stuckThreshold": "2h",
      "backlogTriageInterval": "1h",
      "reviewLoop": false,
      "maxBudget": 5.0,
      "defaultModel": ""
    }
  }
}
```

| Field | Default | Description |
|---|---|---|
| `enabled` | `false` | Enable auto-dispatch loop |
| `interval` | `5m` | How often to scan for ready tasks |
| `maxConcurrentTasks` | `3` | Maximum tasks running simultaneously |
| `defaultAgent` | `ruri` | Auto-assigned to unassigned `todo` tasks before dispatch |
| `backlogAgent` | `ruri` | Agent that reviews and promotes backlog tasks |
| `reviewAgent` | `ruri` | Agent that reviews completed task output |
| `escalateAssignee` | `takuma` | Who gets assigned when auto-review requests human judgment |
| `stuckThreshold` | `2h` | Max time a task can stay in `doing` before reset |
| `backlogTriageInterval` | `1h` | Minimum interval between backlog triage runs |
| `reviewLoop` | `false` | Enable the Dev↔QA loop (execute → review → fix, up to `maxRetries`) |
| `maxBudget` | no limit | Maximum cost per task in USD |
| `defaultModel` | — | Override model for all auto-dispatched tasks |

---

## Slot Pressure

Slot pressure prevents auto-dispatch from consuming all concurrency slots and starving interactive sessions (human chat messages, on-demand dispatches).

Enable it in `config.json`:

```json
{
  "slotPressure": {
    "enabled": true,
    "reservedSlots": 2,
    "warnThreshold": 3,
    "nonInteractiveTimeout": "5m"
  }
}
```

| Field | Default | Description |
|---|---|---|
| `reservedSlots` | `2` | Slots held back for interactive use. Non-interactive tasks must wait if available slots fall to this level. |
| `warnThreshold` | `3` | Warning fires when available slots drop to this level. The message "排程接近滿載" appears in the dashboard and notification channels. |
| `nonInteractiveTimeout` | `5m` | How long a non-interactive task waits for a slot before being cancelled. |

Interactive sources (human chat, `tetora dispatch`, `tetora route`) always acquire slots immediately. Background sources (taskboard, cron) wait if pressure is high.

---

## Git Integration

When `gitCommit`, `gitPush`, and `gitPR` are enabled, the dispatcher runs git operations after a task completes successfully.

**Branch naming** is controlled by `gitWorkflow.branchConvention`:

```json
{
  "taskBoard": {
    "gitWorkflow": {
      "branchConvention": "{type}/{agent}-{description}",
      "types": ["feat", "fix", "refactor", "chore"],
      "defaultType": "feat",
      "autoMerge": true
    }
  }
}
```

The default template `{type}/{agent}-{description}` produces branches like `feat/kokuyou-add-rate-limiting`. The `{description}` portion is derived from the task title (lowercased, spaces replaced with hyphens, truncated to 40 characters).

A task's `type` field sets the branch prefix. If a task has no type, `defaultType` is used.

**Auto PR/MR** supports both GitHub (`gh`) and GitLab (`glab`). The binary that is available on `PATH` is used automatically.

---

## Worktree Mode

When `gitWorktree: true`, each task runs in an isolated git worktree instead of the shared working directory. This eliminates file conflicts when multiple tasks execute concurrently on the same repository.

```
~/.tetora/runtime/worktrees/
  task-abc123/   ← isolated copy for this task
  task-def456/   ← isolated copy for this task
```

On task completion:

- If `autoMerge: true` (default), the worktree branch is merged back into `main` and the worktree is removed.
- If the merge fails, the task moves to `partial-done` status. The worktree is preserved for manual resolution.

To recover from `partial-done`:

```bash
# Inspect what happened
tetora task show task-abc123 --full

# Manually merge the branch
git merge feat/kokuyou-add-rate-limiting

# Mark as done
tetora task move task-abc123 --status=done
```

---

## Workflow Integration

Tasks can run through a workflow pipeline instead of a single agent prompt. This is useful when a task requires multiple coordinated steps (e.g. research → implement → test → document).

Assign a workflow to a task:

```bash
# Set on task creation
tetora task create \
  --title="Implement OAuth2 flow" \
  --workflow=engineering-pipeline \
  --assignee=kokuyou

# Or update an existing task
tetora task update task-abc123 --workflow=engineering-pipeline
```

To disable the board-level default workflow for a specific task:

```json
{ "workflow": "none" }
```

A board-level default workflow applies to all auto-dispatched tasks unless overridden:

```json
{
  "taskBoard": {
    "defaultWorkflow": "engineering-pipeline"
  }
}
```

The `workflowRunId` field on the task links it to the specific workflow execution, visible in the dashboard's Workflows tab.

---

## Dashboard Views

Open the dashboard at `http://localhost:8991/dashboard` and navigate to the **Taskboard** tab.

**Kanban board** — columns for each status. Cards show title, assignee, priority badge, and cost. Drag to move status.

**Task detail panel** — click any card to open. Shows:
- Full description and all comments (spec, context, log entries)
- Session link (jumps to the live worker terminal if still running)
- Cost, duration, retry count
- Workflow run link if applicable

**Diff review panel** — when `requireReview: true`, completed tasks surface in a review queue. Reviewers see the diff of changes and can approve or request changes.

---

## Tips

**Task sizing.** Keep tasks at 30–90 minute scope. Tasks that are too large (multi-day refactors) tend to timeout or produce empty output and get marked failed. Break them into subtasks using the `parentId` field.

**Concurrent dispatch limits.** `maxConcurrentTasks: 3` is a safe default. Raising it past the number of API connections your provider allows causes contention and timeouts. Start at 3, raise to 5 only after confirming stable behavior.

**Partial-done recovery.** If a task enters `partial-done`, the agent completed its work successfully — only the git merge step failed. Resolve the conflict manually, then move the task to `done`. The cost and session data are preserved.

**Using `dependsOn`.** Tasks with unfulfilled dependencies are skipped by the dispatcher until all listed task IDs reach `done` status. The results of upstream tasks are automatically injected into the dependent task's prompt under "Previous Task Results".

**Backlog triage.** The `backlogAgent` reads each `backlog` task, assesses feasibility and priority, and promotes clear tasks to `todo`. Write detailed descriptions and acceptance criteria in your `backlog` tasks — the triage agent uses them to decide whether to promote or leave a task for human review.

**Retries and the review loop.** With `reviewLoop: false` (default), a failed task is retried up to `maxRetries` times with previous log comments injected. With `reviewLoop: true`, each execution is reviewed by the `reviewAgent` before being considered done — the agent gets feedback and tries again if issues are found.
