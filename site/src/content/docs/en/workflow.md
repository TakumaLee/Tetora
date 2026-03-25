---
title: "Workflows"
lang: "en"
order: 2
description: "Define multi-step task pipelines with JSON workflows and agent orchestration."
---
# Workflows

## Overview

Workflows are Tetora's multi-step task orchestration system. Define a sequence of steps in JSON, have different agents collaborate, and automate complex tasks.

**Use cases:**

- Tasks requiring multiple agents working sequentially or in parallel
- Processes with conditional branching and error retry logic
- Automated work triggered by cron schedules, events, or webhooks
- Formal processes that need execution history and cost tracking

## Quick Start

### 1. Write a workflow JSON

Create `my-workflow.json`:

```json
{
  "name": "research-and-summarize",
  "description": "Gather information and write a summary",
  "variables": {
    "topic": "AI agents"
  },
  "timeout": "30m",
  "steps": [
    {
      "id": "research",
      "agent": "hisui",
      "prompt": "Search and organize the latest developments in {{topic}}, listing 5 key points"
    },
    {
      "id": "summarize",
      "agent": "kohaku",
      "prompt": "Write a 300-word summary based on the following:\n{{steps.research.output}}",
      "dependsOn": ["research"]
    }
  ]
}
```

### 2. Import and validate

```bash
# Validate the JSON structure
tetora workflow validate my-workflow.json

# Import to ~/.tetora/workflows/
tetora workflow create my-workflow.json
```

### 3. Run

```bash
# Execute the workflow
tetora workflow run research-and-summarize

# Override variables
tetora workflow run research-and-summarize --var topic="LLM safety"

# Dry-run (no LLM calls, cost estimation only)
tetora workflow run research-and-summarize --dry-run
```

### 4. Check results

```bash
# List execution history
tetora workflow runs research-and-summarize

# View detailed status of a specific run
tetora workflow status <run-id>
```

## Workflow JSON Structure

### Top-Level Fields

| Field | Type | Required | Description |
|-------|------|:--------:|-------------|
| `name` | string | Yes | Workflow name. Alphanumeric, `-`, and `_` only (e.g. `my-workflow`) |
| `description` | string | | Description |
| `steps` | WorkflowStep[] | Yes | At least one step |
| `variables` | map[string]string | | Input variables with defaults (empty `""` = required) |
| `timeout` | string | | Overall timeout in Go duration format (e.g. `"30m"`, `"1h"`) |
| `onSuccess` | string | | Notification template on success (reserved — not yet implemented) |
| `onFailure` | string | | Notification template on failure (reserved — not yet implemented) |
| `gitWorktree` | bool | | Enable git worktree isolation for the entire run. Default `false` |
| `branch` | string | | Explicit branch name. Auto-generated as `wf/{name}` if omitted |
| `workdir` | string | | Repository directory. Falls back to `defaultWorkdir` from config |

### WorkflowStep Fields

| Field | Type | Description |
|-------|------|-------------|
| `id` | string | **Required** — Unique step identifier |
| `type` | string | Step type, defaults to `"dispatch"`. See types below |
| `agent` | string | Agent role to execute this step |
| `prompt` | string | Instruction for the agent (supports `{{}}` templates) |
| `skill` | string | Skill name (for type=skill) |
| `skillArgs` | string[] | Skill arguments (supports templates) |
| `dependsOn` | string[] | Prerequisite step IDs (DAG dependencies) |
| `model` | string | LLM model override |
| `provider` | string | Provider override |
| `timeout` | string | Per-step timeout |
| `budget` | number | Cost limit (USD) |
| `permissionMode` | string | Permission mode |
| `if` | string | Condition expression (type=condition) |
| `then` | string | Step ID to jump to when condition is true |
| `else` | string | Step ID to jump to when condition is false |
| `handoffFrom` | string | Source step ID (type=handoff) |
| `parallel` | WorkflowStep[] | Sub-steps to run in parallel (type=parallel) |
| `retryMax` | int | Max retry count (requires `onError: "retry"`) |
| `retryDelay` | string | Retry interval, e.g. `"10s"` |
| `onError` | string | Error handling: `"stop"` (default), `"skip"`, `"retry"` |
| `toolName` | string | Tool name (type=tool_call) |
| `toolInput` | map[string]string | Tool input parameters (supports `{{var}}` expansion) |
| `delay` | string | Wait duration (type=delay), e.g. `"30s"`, `"5m"` |
| `notifyMsg` | string | Notification message (type=notify, supports templates) |
| `notifyTo` | string | Notification channel hint (e.g. `"telegram"`) |

## Step Types

### dispatch (default)

Sends a prompt to the specified agent for execution. This is the most common step type and is used when `type` is omitted.

```json
{
  "id": "draft",
  "agent": "kohaku",
  "prompt": "Write an article about {{topic}}",
  "model": "claude-sonnet-4-20250514",
  "timeout": "10m"
}
```

**Required:** `prompt`
**Optional:** `agent`, `model`, `provider`, `timeout`, `budget`, `permissionMode`

### skill

Executes a registered skill.

```json
{
  "id": "search",
  "type": "skill",
  "skill": "web-search",
  "skillArgs": ["{{topic}}", "--depth", "3"]
}
```

**Required:** `skill`
**Optional:** `skillArgs`

### condition

Evaluates a condition expression to determine the branch. When true, takes `then`; when false, takes `else`. The unchosen branch is marked as skipped.

```json
{
  "id": "check-type",
  "type": "condition",
  "if": "{{type}} == 'technical'",
  "then": "tech-research",
  "else": "creative-draft"
}
```

**Required:** `if`, `then`
**Optional:** `else`

Supported operators:
- `==` — equals (e.g. `{{type}} == 'technical'`)
- `!=` — not equals
- Truthy check — non-empty and not `"false"`/`"0"` evaluates to true

### parallel

Runs multiple sub-steps concurrently, waiting for all to complete. Sub-step outputs are joined with `\n---\n`.

```json
{
  "id": "gather",
  "type": "parallel",
  "parallel": [
    {"id": "search-papers", "agent": "hisui", "prompt": "Search for papers"},
    {"id": "search-code", "agent": "kokuyou", "prompt": "Search open-source projects"}
  ]
}
```

**Required:** `parallel` (at least one sub-step)

Individual sub-step results can be referenced via `{{steps.search-papers.output}}`.

### handoff

Passes one step's output to another agent for further processing. The source step's full output becomes the receiving agent's context.

```json
{
  "id": "review",
  "type": "handoff",
  "agent": "ruri",
  "handoffFrom": "draft",
  "prompt": "Review and revise the article",
  "dependsOn": ["draft"]
}
```

**Required:** `handoffFrom`, `agent`
**Optional:** `prompt` (instruction for the receiving agent)

### tool_call

Invokes a registered tool from the tool registry.

```json
{
  "id": "fetch-data",
  "type": "tool_call",
  "toolName": "http-get",
  "toolInput": {
    "url": "https://api.example.com/data?q={{topic}}"
  }
}
```

**Required:** `toolName`
**Optional:** `toolInput` (supports `{{var}}` expansion)

### delay

Waits for a specified duration before continuing.

```json
{
  "id": "wait",
  "type": "delay",
  "delay": "30s"
}
```

**Required:** `delay` (Go duration format: `"30s"`, `"5m"`, `"1h"`)

### notify

Sends a notification message. The message is published as an SSE event (type=`workflow_notify`) so external consumers can trigger Telegram, Slack, etc.

```json
{
  "id": "notify-done",
  "type": "notify",
  "notifyMsg": "Task complete: {{steps.review.output}}",
  "notifyTo": "telegram"
}
```

**Required:** `notifyMsg`
**Optional:** `notifyTo` (channel hint)

## Variables and Templates

Workflows support `{{}}` template syntax, expanded before step execution.

### Input Variables

```
{{varName}}
```

Resolved from `variables` defaults or `--var key=value` overrides.

### Step Results

```
{{steps.ID.output}}    — Step output text
{{steps.ID.status}}    — Step status
{{steps.ID.error}}     — Step error message
```

Possible status values: `pending`, `running`, `success`, `error`, `skipped`, `timeout`, `cancelled`

### Environment Variables

```
{{env.KEY}}            — System environment variable
```

### Example

```json
{
  "id": "summarize",
  "agent": "kohaku",
  "prompt": "Topic: {{topic}}\nResearch results: {{steps.research.output}}\n\nPlease write a summary.",
  "dependsOn": ["research"]
}
```

## Dependencies and Flow Control

### dependsOn — DAG Dependencies

Use `dependsOn` to define execution order. The system automatically sorts steps as a DAG (Directed Acyclic Graph).

```json
{
  "id": "step-c",
  "dependsOn": ["step-a", "step-b"],
  "prompt": "..."
}
```

- `step-c` waits for both `step-a` and `step-b` to complete
- Steps without `dependsOn` start immediately (possibly in parallel)
- Circular dependencies are detected and rejected

### Conditional Branching

A `condition` step's `then`/`else` determines which downstream steps execute:

```
classify (condition)
  ├── then → tech-research
  └── else → creative-draft
```

The unchosen branch step is marked as `skipped`. Downstream steps still evaluate normally based on their `dependsOn`.

## Error Handling

### onError Strategies

Each step can set `onError`:

| Value | Behavior |
|-------|----------|
| `"stop"` | **Default** — Abort the workflow on failure; remaining steps are marked skipped |
| `"skip"` | Mark the failed step as skipped and continue |
| `"retry"` | Retry per `retryMax` + `retryDelay`; if all retries fail, treat as error |

### Retry Configuration

```json
{
  "id": "flaky-step",
  "agent": "hisui",
  "prompt": "...",
  "onError": "retry",
  "retryMax": 3,
  "retryDelay": "10s"
}
```

- `retryMax`: Maximum retry attempts (excluding the first attempt)
- `retryDelay`: Delay between retries, defaults to 5 seconds
- Only effective when `onError: "retry"`

## Triggers

Triggers enable automatic workflow execution. Configure them in `config.json` under the `workflowTriggers` array.

### WorkflowTriggerConfig Structure

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Trigger name |
| `workflowName` | string | Workflow to execute |
| `enabled` | bool | Whether enabled (default: true) |
| `trigger` | TriggerSpec | Trigger condition |
| `variables` | map[string]string | Variable overrides for the workflow |
| `cooldown` | string | Cooldown period (e.g. `"5m"`, `"1h"`) |

### TriggerSpec Structure

| Field | Type | Description |
|-------|------|-------------|
| `type` | string | `"cron"`, `"event"`, or `"webhook"` |
| `cron` | string | Cron expression (5 fields: min hour day month weekday) |
| `tz` | string | Timezone (e.g. `"Asia/Taipei"`), for cron only |
| `event` | string | SSE event type, supports `*` suffix wildcard (e.g. `"deploy_*"`) |
| `webhook` | string | Webhook path suffix |

### Cron Triggers

Checked every 30 seconds, fires at most once per minute (deduplication).

```json
{
  "name": "daily-briefing",
  "workflowName": "research-and-summarize",
  "trigger": {"type": "cron", "cron": "0 8 * * *", "tz": "Asia/Taipei"},
  "variables": {"topic": "AI industry news"},
  "cooldown": "12h"
}
```

### Event Triggers

Listens on the SSE `_triggers` channel and matches event types. Supports `*` suffix wildcard.

```json
{
  "name": "on-deploy",
  "workflowName": "content-pipeline",
  "trigger": {"type": "event", "event": "deploy_*"},
  "variables": {"type": "technical"}
}
```

Event triggers automatically inject extra variables: `event_type`, `task_id`, `session_id`, plus event data fields (prefixed with `event_`).

### Webhook Triggers

Triggered via HTTP POST:

```json
{
  "name": "external-hook",
  "workflowName": "content-pipeline",
  "trigger": {"type": "webhook", "webhook": "content-request"}
}
```

Usage:

```bash
curl -X POST http://localhost:PORT/api/triggers/webhook/external-hook \
  -H "Content-Type: application/json" \
  -d '{"topic": "new feature"}'
```

The POST body JSON key-value pairs are injected as extra workflow variables.

### Cooldown

All triggers support `cooldown` to prevent repeated firing within a short period. Triggers during cooldown are silently ignored.

### Trigger Meta-Variables

The system automatically injects these variables on each trigger:

- `_trigger_name` — Trigger name
- `_trigger_type` — Trigger type (cron/event/webhook)
- `_trigger_time` — Trigger time (RFC3339)

> **Note:** These variables are only injected when the workflow is executed via a trigger. They are not available when running directly via `tetora workflow run` or the HTTP API.

## Execution Modes

### live (default)

Full execution: calls LLMs, records history, publishes SSE events.

```bash
tetora workflow run my-workflow
```

### dry-run

No LLM calls; estimates cost for each step. Condition steps evaluate normally; dispatch/skill/handoff steps return cost estimates.

```bash
tetora workflow run my-workflow --dry-run
```

### shadow

Executes LLM calls normally but does not record to task history or session logs. Useful for testing.

```bash
tetora workflow run my-workflow --shadow
```

## Git Worktree Isolation

When `gitWorktree: true` is set, the workflow executor creates an isolated git worktree before the DAG starts. All dispatch and handoff steps run inside this worktree, preventing file conflicts with the main working tree or other concurrent workflows.

### Behavior

- **On success**: the worktree branch is merged back to main and the worktree is cleaned up.
- **On failure**: the worktree is kept for manual inspection and debugging.
- **Dry-run/shadow modes**: worktree creation is skipped (no side effects).

### Example

```json
{
  "name": "feature-pipeline",
  "gitWorktree": true,
  "branch": "feat/{{agent}}-auto-review",
  "workdir": "/path/to/repo",
  "timeout": "1h",
  "steps": [...]
}
```

If `branch` is omitted, it defaults to `wf/{workflow-name}` (e.g. `wf/feature-pipeline`). If `workdir` is omitted, it falls back to `defaultWorkdir` from `config.json`.

### When to use

| Situation | Use worktree? |
|-----------|:------------:|
| Multi-agent workflow modifying code | Yes |
| Single-agent quick task | No (faster) |
| Parallel workflows on same repo | Yes (prevents conflicts) |
| Read-only workflows (research, review) | No |

## CLI Reference

```
tetora workflow <command> [options]
```

| Command | Description |
|---------|-------------|
| `list` | List all saved workflows |
| `show <name>` | Show workflow definition (summary + JSON) |
| `validate <name\|file>` | Validate a workflow (accepts name or JSON file path) |
| `create <file>` | Import workflow from a JSON file (validates first) |
| `delete <name>` | Delete a workflow |
| `run <name> [flags]` | Execute a workflow |
| `runs [name]` | List execution history (optionally filter by name) |
| `status <run-id>` | Show detailed status of a run (JSON output) |
| `messages <run-id>` | Show agent messages and handoff records for a run |
| `history <name>` | Show workflow version history |
| `rollback <name> <version-id>` | Restore to a specific version |
| `diff <version1> <version2>` | Compare two versions |

### run Command Flags

| Flag | Description |
|------|-------------|
| `--var key=value` | Override a workflow variable (can be used multiple times) |
| `--dry-run` | Dry-run mode (no LLM calls) |
| `--shadow` | Shadow mode (no history recording) |

### Aliases

- `list` = `ls`
- `delete` = `rm`
- `messages` = `msgs`

## HTTP API Reference

### Workflow CRUD

| Method | Path | Description |
|--------|------|-------------|
| GET | `/workflows` | List all workflows |
| POST | `/workflows` | Create a workflow (body: Workflow JSON) |
| GET | `/workflows/{name}` | Get a single workflow definition |
| DELETE | `/workflows/{name}` | Delete a workflow |
| POST | `/workflows/{name}/validate` | Validate a workflow |
| POST | `/workflows/{name}/run` | Run a workflow |
| POST | `/workflows/{name}/dry-run` | Dry-run a workflow (synchronous, no LLM calls) |
| GET | `/workflows/{name}/runs` | Get run history for a workflow |
| GET | `/workflows/{name}/versions` | Get version history |
| POST | `/workflows/{name}/rollback` | Rollback to a version (body: `{"versionId":"..."}`) |

#### POST /workflows/{name}/run Body

```json
{
  "variables": {
    "topic": "AI agents"
  }
}
```

> The `run` endpoint returns `202 Accepted` immediately. The workflow executes asynchronously. Poll `/workflow-runs/{id}` for completion status.

#### POST /workflows/{name}/dry-run Body

Same as `run`. Returns the full run result synchronously — no LLM calls are made, each step shows estimated cost and execution order.

### Workflow Runs

| Method | Path | Description |
|--------|------|-------------|
| GET | `/workflow-runs` | List all run records (add `?workflow=name` to filter) |
| GET | `/workflow-runs/{id}` | Get run details (includes step results, handoffs, callbacks) |

### Triggers

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/triggers` | List all trigger configs and statuses |
| POST | `/api/triggers` | Create a new trigger (body: WorkflowTriggerConfig JSON) |
| PUT | `/api/triggers/{name}` | Update a trigger |
| DELETE | `/api/triggers/{name}` | Delete a trigger |
| POST | `/api/triggers/{name}/toggle` | Toggle trigger enabled/disabled |
| POST | `/api/triggers/{name}/fire` | Manually fire a trigger |
| GET | `/api/triggers/{name}/runs` | View trigger run history (add `?limit=N`) |
| POST | `/api/triggers/webhook/{id}` | Webhook trigger (body: JSON key-value variables) |

Trigger mutations (create/update/delete/toggle) automatically persist to `config.json` and hot-reload the trigger engine via SIGHUP.

### Templates

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/templates` | List all built-in templates (name, description, step count) |
| GET | `/api/templates/{name}` | Get full template JSON |
| POST | `/api/templates/{name}/install` | Install template as a user workflow (body: `{"newName":"..."}`) |

36 industry templates are bundled, covering HR, finance, DevOps, healthcare, logistics, and more. Browse them in the dashboard's Template Gallery or via the API.

## Version Management

Every `create` or modification automatically creates a version snapshot.

```bash
# View version history
tetora workflow history my-workflow

# Restore to a specific version
tetora workflow rollback my-workflow <version-id>

# Compare two versions
tetora workflow diff <version-id-1> <version-id-2>
```

## Validation Rules

The system validates before both `create` and `run`:

- `name` is required; only alphanumeric, `-`, and `_` allowed
- At least one step required
- Step IDs must be unique
- `dependsOn` references must point to existing step IDs
- Steps cannot depend on themselves
- Circular dependencies are rejected (DAG cycle detection)
- Required fields per step type (e.g. dispatch needs `prompt`, condition needs `if` + `then`)
- `timeout`, `retryDelay`, `delay` must be valid Go duration format
- `onError` only accepts `stop`, `skip`, `retry`
- Condition `then`/`else` must reference existing step IDs
- Handoff `handoffFrom` must reference an existing step ID
- Parallel sub-steps are validated recursively with the same rules as top-level steps (unique IDs, valid types, required fields, valid duration strings, etc.)

## Dashboard UI

The web dashboard at `http://localhost:PORT` provides a visual interface for all workflow operations.

### Workflow Editor

- **Create/Edit**: Visual editor with JSON editing, step list, and DAG preview
- **Run**: Execute workflows with variable overrides from the toolbar
- **Dry Run**: Test execution order and cost estimates without LLM calls
- **Version History**: Browse, compare, and rollback to previous versions

### Run Detail

When viewing a workflow run:

- **DAG Visualization**: Live-updating node graph showing step status (green=success, red=error, blue=running, yellow=waiting)
- **Step Results List**: Collapsible cards below the DAG showing each step's output, duration, cost, and error details
- **Cost Breakdown Bar**: Horizontal stacked bar showing per-step cost distribution
- **Live SSE Updates**: Step status changes stream in real-time via Server-Sent Events
- **External Step UX**: Waiting steps show callback URL, timeout countdown, and manual resolve controls

### Trigger Management

- **Trigger Cards**: View all triggers with type badges (cron/event/webhook), next run time, and action buttons
- **Create/Edit Modal**: Configure trigger name, type, workflow, variables, cooldown, and type-specific fields
- **Quick Actions**: Toggle enabled/disabled, Fire Now, view recent runs, copy webhook URL

### Template Gallery

- **Browse**: Grid of 36 industry workflow templates with descriptions and step counts
- **Search/Filter**: Text filter with category extraction (HR, finance, DevOps, etc.)
- **Preview**: View full template JSON before installing
- **Install**: One-click install with optional rename, immediately available in the editor

## Example: Development Workflow

Tetora includes a `standard-dev` workflow designed for agent-driven development with built-in quality gates. This is how agents like Kokuyou (黒曜) execute implementation tasks autonomously.

### Flow

```
read-spec → plan → implement → build-test → self-review → quality-review → [fix-issues] → commit
```

### How It Works

1. **read-spec** — Agent reads the task spec from `tasks/specs.md` and extracts requirements + acceptance criteria
2. **plan** — Agent creates an implementation plan: files to change, approach, risks
3. **implement** — Agent executes the plan, writing actual code changes
4. **build-test** — Runs `go build` + `go test`, auto-retries up to 2 times on failure
5. **self-review** — Agent reviews its own `git diff` against the spec, flags concerns
6. **quality-review** — A separate agent (Ruri/琉璃) performs staff-engineer-level code review with an 8-point checklist, returning one of:
   - `approve` — Production-ready, proceed to commit
   - `fix` — Specific issues listed, agent fixes them autonomously
   - `escalate` — Requires human judgment (ambiguous spec, production risk)
7. **fix-issues** — If verdict is `fix`, agent addresses all review comments and re-verifies build
8. **commit** — Creates a git commit with task ID and title

### Usage

```bash
# Run via CLI
tetora workflow run standard-dev --var taskId="T-042" --var taskTitle="Add webhook retry logic"

# Run via dashboard
# Open Workflows → standard-dev → Run → fill in taskId and taskTitle

# Dry-run first to see execution order and cost estimates
tetora workflow run standard-dev --dry-run --var taskId="T-042" --var taskTitle="test"
```

### Customization

The workflow uses `{{agent}}` as a variable (default: `kokuyou`), so any agent can run it:

```bash
tetora workflow run standard-dev --var agent=hisui --var taskId="T-043" --var taskTitle="Research API options"
```

### Key Design Decisions

- **Three-verdict review**: `approve`/`fix`/`escalate` prevents infinite fix loops — `escalate` is the escape hatch for genuinely ambiguous situations
- **Separate reviewer**: Quality review uses a different agent (Ruri) with a senior engineer persona, avoiding self-confirmation bias
- **Build-test retries**: Flaky builds get 2 auto-retries before failing the workflow
- **Budget caps**: Each step has a cost limit to prevent runaway spending
- **No auto-push**: Commit only, never push — human reviews the commit before pushing
