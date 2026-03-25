---
title: "Configuration Reference"
lang: "en"
order: 1
description: "Configure Tetora via ~/.tetora/config.json — models, providers, and runtime settings."
---
# Configuration Reference

## Overview

Tetora is configured by a single JSON file located at `~/.tetora/config.json`.

**Key behaviors:**

- **`$ENV_VAR` substitution** — any string value starting with `$` is replaced with the corresponding environment variable at startup. Use this for secrets (API keys, tokens) instead of hardcoding them.
- **Hot-reload** — sending `SIGHUP` to the daemon reloads the config. A bad config will be rejected and the running config kept; the daemon will not crash.
- **Relative paths** — `jobsFile`, `historyDB`, `defaultWorkdir`, and directory fields are resolved relative to the config file's directory (`~/.tetora/`).
- **Backward compatibility** — the old `"roles"` key is an alias for `"agents"`. The old `"defaultRole"` key inside `smartDispatch` is an alias for `"defaultAgent"`.

---

## Top-Level Fields

### Core Settings

| Field | Type | Default | Description |
|---|---|---|---|
| `listenAddr` | string | `"127.0.0.1:8991"` | HTTP listen address for the API and dashboard. Format: `host:port`. |
| `apiToken` | string | `""` | Bearer token required for all API requests. Empty means no authentication (not recommended for production). Supports `$ENV_VAR`. |
| `maxConcurrent` | int | `8` | Maximum number of concurrent agent tasks. Values above 20 produce a startup warning. |
| `defaultModel` | string | `"sonnet"` | Default Claude model name. Passed to the provider unless overridden per-agent. |
| `defaultTimeout` | string | `"1h"` | Default task timeout. Go duration format: `"15m"`, `"1h"`, `"30s"`. |
| `defaultBudget` | float64 | `0` | Default cost budget per task in USD. `0` means no limit. |
| `defaultPermissionMode` | string | `"acceptEdits"` | Default Claude permission mode. Common values: `"acceptEdits"`, `"default"`. |
| `defaultAgent` | string | `""` | System-wide fallback agent name when no routing rule matches. |
| `defaultWorkdir` | string | `""` | Default working directory for agent tasks. Must exist on disk. |
| `claudePath` | string | `"claude"` | Path to the `claude` CLI binary. Defaults to looking up `claude` on `$PATH`. |
| `defaultProvider` | string | `"claude"` | Name of the provider to use when no agent-level override is set. |
| `log` | bool | `false` | Legacy flag to enable file logging. Prefer `logging.level` instead. |
| `maxPromptLen` | int | `102400` | Maximum prompt length in bytes (100 KB). Requests exceeding this are rejected. |
| `configVersion` | int | `0` | Config schema version. Used for auto-migration. Do not set manually. |
| `encryptionKey` | string | `""` | AES key for field-level encryption of sensitive data. Supports `$ENV_VAR`. |
| `streamToChannels` | bool | `false` | Stream live task status to connected messaging channels (Discord, Telegram, etc.). |
| `cronNotify` | bool\|null | `null` (true) | `false` suppresses all cron job completion notifications. `null` or `true` enables them. |
| `cronReplayHours` | int | `2` | How many hours to look back for missed cron jobs on daemon startup. |
| `diskBudgetGB` | float64 | `1.0` | Minimum free disk space in GB. Cron jobs are refused below this level. |
| `diskWarnMB` | int | `500` | Free disk warn threshold in MB. Logs a WARN but jobs continue. |
| `diskBlockMB` | int | `200` | Free disk block threshold in MB. Jobs are skipped with `skipped_disk_full` status. |

### Directory Overrides

By default all directories live under `~/.tetora/`. Override only if you need a non-standard layout.

| Field | Type | Default | Description |
|---|---|---|---|
| `knowledgeDir` | string | `~/.tetora/knowledge/` | Directory for workspace knowledge files. |
| `agentsDir` | string | `~/.tetora/agents/` | Directory containing per-agent SOUL.md files. |
| `workspaceDir` | string | `~/.tetora/workspace/` | Directory for rules, memory, skills, drafts, etc. |
| `runtimeDir` | string | `~/.tetora/runtime/` | Directory for sessions, outputs, logs, cache. |
| `vaultDir` | string | `~/.tetora/vault/` | Directory for encrypted secrets vault. |
| `historyDB` | string | `history.db` | SQLite database path for job history. Relative to config dir. |
| `jobsFile` | string | `jobs.json` | Path to the cron jobs definition file. Relative to config dir. |

### Global Allowed Directories

```json
{
  "allowedDirs": ["/Users/me/projects", "/tmp"],
  "defaultAddDirs": ["/Users/me/shared-context"]
}
```

| Field | Type | Description |
|---|---|---|
| `allowedDirs` | string[] | Directories the agent is allowed to read and write. Applied globally; can be narrowed per-agent. |
| `defaultAddDirs` | string[] | Directories injected as `--add-dir` for every task (read-only context). |
| `allowedIPs` | string[] | IP addresses or CIDR ranges allowed to call the API. Empty = allow all. Example: `["192.168.1.0/24", "10.0.0.1"]`. |

---

## Providers

Providers define how Tetora executes agent tasks. Multiple providers can be configured and selected per-agent.

```json
{
  "defaultProvider": "claude",
  "providers": {
    "claude": {
      "type": "claude-cli",
      "path": "/usr/local/bin/claude"
    },
    "openai": {
      "type": "openai-compatible",
      "baseUrl": "https://api.openai.com/v1",
      "apiKey": "$OPENAI_API_KEY",
      "model": "gpt-4o"
    },
    "claude-api": {
      "type": "claude-api",
      "apiKey": "$ANTHROPIC_API_KEY",
      "model": "claude-sonnet-4-5",
      "maxTokens": 8192,
      "firstTokenTimeout": "60s"
    }
  }
}
```

### `providers` — `ProviderConfig`

| Field | Type | Default | Description |
|---|---|---|---|
| `type` | string | required | Provider type. One of: `"claude-cli"`, `"openai-compatible"`, `"claude-api"`, `"claude-code"`. |
| `path` | string | `""` | Binary path. Used by `claude-cli` and `claude-code` types. Falls back to `claudePath` if empty. |
| `baseUrl` | string | `""` | API base URL. Required for `openai-compatible`. |
| `apiKey` | string | `""` | API key. Supports `$ENV_VAR`. Required for `claude-api`; optional for `openai-compatible`. |
| `model` | string | `""` | Default model for this provider. Overrides `defaultModel` for tasks using this provider. |
| `maxTokens` | int | `8192` | Maximum output tokens (used by `claude-api`). |
| `firstTokenTimeout` | string | `"60s"` | How long to wait for the first response token before timing out (SSE stream). |

**Provider types:**
- `claude-cli` — runs the `claude` binary as a subprocess (default, most compatible)
- `claude-api` — calls the Anthropic API directly using HTTP (requires `ANTHROPIC_API_KEY`)
- `openai-compatible` — any OpenAI-compatible REST API (OpenAI, Ollama, Groq, etc.)
- `claude-code` — uses Claude Code CLI mode

---

## Agents

Agents define named personas with their own model, soul file, and tool access.

```json
{
  "agents": {
    "coordinator": {
      "soulFile": "SOUL.md",
      "model": "sonnet",
      "description": "Handles planning, research, and coordination.",
      "keywords": ["plan", "research", "coordinate"]
    },
    "engineer": {
      "soulFile": "team/engineer/SOUL.md",
      "model": "sonnet",
      "provider": "claude",
      "description": "Handles coding, debugging, and infrastructure.",
      "keywords": ["code", "debug", "deploy"],
      "permissionMode": "acceptEdits",
      "allowedDirs": ["/Users/me/projects"],
      "trustLevel": "auto"
    }
  }
}
```

### `agents` — `AgentConfig`

| Field | Type | Default | Description |
|---|---|---|---|
| `soulFile` | string | required | Path to the agent's SOUL.md personality file, relative to `agentsDir`. |
| `model` | string | `defaultModel` | Model to use for this agent. |
| `description` | string | `""` | Human-readable description. Also used by the LLM classifier for routing. |
| `keywords` | string[] | `[]` | Keywords that trigger routing to this agent in smart dispatch. |
| `provider` | string | `defaultProvider` | Provider name (key in `providers` map). |
| `permissionMode` | string | `defaultPermissionMode` | Claude permission mode for this agent. |
| `allowedDirs` | string[] | `allowedDirs` | Filesystem paths this agent can access. Overrides the global setting. |
| `docker` | bool\|null | `null` | Per-agent Docker sandbox override. `null` = inherit global `docker.enabled`. |
| `fallbackProviders` | string[] | `[]` | Ordered list of fallback provider names if the primary fails. |
| `trustLevel` | string | `"auto"` | Trust level: `"observe"` (read-only), `"suggest"` (propose but not apply), `"auto"` (full autonomy). |
| `tools` | AgentToolPolicy | `{}` | Tool access policy. See [Tool Policy](#tool-policy). |
| `toolProfile` | string | `"standard"` | Named tool profile: `"minimal"`, `"standard"`, `"full"`. |
| `workspace` | WorkspaceConfig | `{}` | Workspace isolation settings. |

---

## Smart Dispatch

Smart Dispatch automatically routes incoming tasks to the most appropriate agent based on rules, keywords, and LLM classification.

```json
{
  "smartDispatch": {
    "enabled": true,
    "coordinator": "coordinator",
    "defaultAgent": "coordinator",
    "classifyBudget": 0.1,
    "classifyTimeout": "30s",
    "review": false,
    "reviewLoop": false,
    "maxRetries": 3,
    "fallback": "smart",
    "rules": [
      {
        "agent": "engineer",
        "keywords": ["bug", "error", "deploy", "docker"],
        "patterns": ["(?:fix|resolve)\\s+(?:bug|issue|error)"]
      },
      {
        "agent": "creator",
        "keywords": ["blog post", "documentation", "README"]
      }
    ],
    "bindings": [
      {
        "channel": "discord",
        "channelId": "123456789",
        "agent": "engineer"
      }
    ]
  }
}
```

### `smartDispatch` — `SmartDispatchConfig`

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Enable smart dispatch routing. |
| `coordinator` | string | first agent | Agent used for LLM-based task classification. |
| `defaultAgent` | string | first agent | Fallback agent when no rule matches. |
| `classifyBudget` | float64 | `0.1` | Cost budget (USD) for the classification LLM call. |
| `classifyTimeout` | string | `"30s"` | Timeout for the classification call. |
| `review` | bool | `false` | Run a review agent on the output after task completion. |
| `reviewLoop` | bool | `false` | Enable Dev↔QA retry loop: review → feedback → retry (up to `maxRetries`). |
| `maxRetries` | int | `3` | Maximum QA retry attempts in the review loop. |
| `reviewAgent` | string | coordinator | Agent responsible for reviewing output. Set to a strict QA agent for adversarial review. |
| `reviewBudget` | float64 | `0.2` | Cost budget (USD) for the review LLM call. |
| `fallback` | string | `"smart"` | Fallback strategy: `"smart"` (LLM routing) or `"coordinator"` (always default agent). |
| `rules` | RoutingRule[] | `[]` | Keyword/regex routing rules evaluated before LLM classification. |
| `bindings` | RoutingBinding[] | `[]` | Channel/user/guild → agent bindings (highest priority, evaluated first). |

### `rules` — `RoutingRule`

| Field | Type | Description |
|---|---|---|
| `agent` | string | Target agent name. |
| `keywords` | string[] | Case-insensitive keywords. Any match routes to this agent. |
| `patterns` | string[] | Go regex patterns. Any match routes to this agent. |

### `bindings` — `RoutingBinding`

| Field | Type | Description |
|---|---|---|
| `channel` | string | Platform: `"telegram"`, `"discord"`, `"slack"`, etc. |
| `userId` | string | User ID on that platform. |
| `channelId` | string | Channel or chat ID. |
| `guildId` | string | Guild/server ID (Discord only). |
| `agent` | string | Target agent name. |

---

## Session

Controls how conversation context is maintained and compacted across multi-turn interactions.

```json
{
  "session": {
    "contextMessages": 20,
    "compactAfter": 30,
    "compactKeep": 10,
    "compactTokens": 200000,
    "compaction": {
      "enabled": true,
      "maxMessages": 50,
      "compactTo": 10,
      "model": "haiku",
      "maxCost": 0.02,
      "provider": "claude"
    }
  }
}
```

### `session` — `SessionConfig`

| Field | Type | Default | Description |
|---|---|---|---|
| `contextMessages` | int | `20` | Maximum number of recent messages to inject as context into a new task. |
| `compactAfter` | int | `30` | Compact when message count exceeds this. Deprecated: use `compaction.maxMessages`. |
| `compactKeep` | int | `10` | Keep last N messages after compaction. Deprecated: use `compaction.compactTo`. |
| `compactTokens` | int | `200000` | Compact when total input tokens exceed this threshold. |

### `session.compaction` — `CompactionConfig`

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Enable automatic session compaction. |
| `maxMessages` | int | `50` | Trigger compaction when session exceeds this many messages. |
| `compactTo` | int | `10` | Number of recent messages to keep after compaction. |
| `model` | string | `"haiku"` | LLM model to use for generating the compaction summary. |
| `maxCost` | float64 | `0.02` | Maximum cost per compaction call (USD). |
| `provider` | string | `defaultProvider` | Provider to use for the compaction summary call. |

---

## Task Board

The built-in task board tracks work items and can automatically dispatch them to agents.

```json
{
  "taskBoard": {
    "enabled": true,
    "maxRetries": 3,
    "requireReview": false,
    "defaultWorkflow": "",
    "gitCommit": false,
    "gitPush": false,
    "gitPR": false,
    "gitWorktree": false,
    "gitWorkflow": {
      "branchConvention": "{type}/{agent}-{description}",
      "types": ["feat", "fix", "refactor", "chore"],
      "defaultType": "feat",
      "autoMerge": false
    },
    "autoDispatch": {
      "enabled": false,
      "interval": "5m",
      "maxConcurrentTasks": 3,
      "stuckThreshold": "2h",
      "reviewLoop": false
    }
  }
}
```

### `taskBoard` — `TaskBoardConfig`

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Enable the task board. |
| `maxRetries` | int | `3` | Maximum retry attempts per task before marking as failed. |
| `requireReview` | bool | `false` | Quality gate: task must pass review before being marked done. |
| `defaultWorkflow` | string | `""` | Workflow name to run for all auto-dispatched tasks. Empty = no workflow. |
| `gitCommit` | bool | `false` | Auto-commit when a task is marked done. |
| `gitPush` | bool | `false` | Auto-push after commit (requires `gitCommit: true`). |
| `gitPR` | bool | `false` | Auto-create a GitHub PR after push (requires `gitPush: true`). |
| `gitWorktree` | bool | `false` | Use git worktrees for task isolation (eliminates file conflicts between concurrent tasks). |
| `idleAnalyze` | bool | `false` | Auto-run analysis when the board is idle. |
| `problemScan` | bool | `false` | Scan task output for latent issues after completion. |

### `taskBoard.autoDispatch` — `TaskBoardDispatchConfig`

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Enable automatic polling and dispatch of queued tasks. |
| `interval` | string | `"5m"` | How often to scan for ready tasks. |
| `maxConcurrentTasks` | int | `3` | Maximum tasks dispatched per scan cycle. |
| `defaultModel` | string | `""` | Override model for auto-dispatched tasks. |
| `maxBudget` | float64 | `0` | Maximum cost per task (USD). `0` = no limit. |
| `defaultAgent` | string | `""` | Fallback agent for unassigned tasks. |
| `backlogAgent` | string | `""` | Agent for backlog triage. |
| `reviewAgent` | string | `""` | Agent for reviewing completed tasks. |
| `escalateAssignee` | string | `""` | Assign review-rejected tasks to this user. |
| `stuckThreshold` | string | `"2h"` | Tasks in "doing" longer than this are reset to "todo". |
| `backlogTriageInterval` | string | `"1h"` | How often to run backlog triage. |
| `reviewLoop` | bool | `false` | Enable automated Dev↔QA loop for dispatched tasks. |

### `taskBoard.gitWorkflow` — `GitWorkflowConfig`

| Field | Type | Default | Description |
|---|---|---|---|
| `branchConvention` | string | `"{type}/{agent}-{description}"` | Branch naming template. Variables: `{type}`, `{agent}`, `{description}`. |
| `types` | string[] | `["feat","fix","refactor","chore"]` | Allowed branch type prefixes. |
| `defaultType` | string | `"feat"` | Fallback type when none is specified. |
| `autoMerge` | bool | `false` | Automatically merge back to main when task is done (only when `gitWorktree: true`). |

---

## Slot Pressure

Controls how the system behaves when approaching the `maxConcurrent` slot limit. Interactive (human-initiated) sessions get reserved slots; background tasks wait.

```json
{
  "slotPressure": {
    "enabled": true,
    "reservedSlots": 2,
    "warnThreshold": 3,
    "nonInteractiveTimeout": "5m",
    "monitorEnabled": false,
    "monitorInterval": "30s"
  }
}
```

### `slotPressure` — `SlotPressureConfig`

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Enable slot pressure management. |
| `reservedSlots` | int | `2` | Slots reserved for interactive sessions. Background tasks cannot use these. |
| `warnThreshold` | int | `3` | Warn the user when fewer than this many slots are available. |
| `nonInteractiveTimeout` | string | `"5m"` | How long a background task waits for a slot before timing out. |
| `pollInterval` | string | `"2s"` | How often background tasks check for a free slot. |
| `monitorEnabled` | bool | `false` | Enable proactive slot pressure alerts via notification channels. |
| `monitorInterval` | string | `"30s"` | How often to check and emit pressure alerts. |

---

## Workflows

Workflows are defined as YAML files in a directory. The `workflowDir` points to that directory; variables provide default template values.

```json
{
  "workflowDir": "~/.tetora/workspace/workflows/",
  "workflowTriggers": [
    {
      "event": "task.done",
      "workflow": "notify-slack",
      "filter": {"status": "done"}
    }
  ]
}
```

| Field | Type | Default | Description |
|---|---|---|---|
| `workflowDir` | string | `~/.tetora/workspace/workflows/` | Directory where workflow YAML files are stored. |
| `workflowTriggers` | WorkflowTriggerConfig[] | `[]` | Automatic workflow triggers on system events. |

---

## Integrations

### Telegram

```json
{
  "telegram": {
    "enabled": true,
    "botToken": "$TELEGRAM_BOT_TOKEN",
    "chatID": 123456789,
    "pollTimeout": 30
  }
}
```

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Enable Telegram bot. |
| `botToken` | string | `""` | Telegram bot token from @BotFather. Supports `$ENV_VAR`. |
| `chatID` | int64 | `0` | Telegram chat or group ID to send notifications to. |
| `pollTimeout` | int | `30` | Long-poll timeout in seconds for receiving messages. |

### Discord

```json
{
  "discord": {
    "enabled": true,
    "botToken": "$DISCORD_BOT_TOKEN",
    "guildID": "123456789",
    "channelIDs": ["111111111"],
    "mentionChannelIDs": ["222222222"],
    "notifyChannelID": "333333333",
    "showProgress": true,
    "routes": {
      "111111111": {"agent": "engineer"}
    }
  }
}
```

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Enable Discord bot. |
| `botToken` | string | `""` | Discord bot token. Supports `$ENV_VAR`. |
| `guildID` | string | `""` | Restrict to a specific Discord server (guild). |
| `channelIDs` | string[] | `[]` | Channel IDs where the bot replies to all messages (no `@` mention needed). |
| `mentionChannelIDs` | string[] | `[]` | Channel IDs where the bot only replies when `@`-mentioned. |
| `notifyChannelID` | string | `""` | Channel for task completion notifications (creates a thread per task). |
| `showProgress` | bool | `true` | Show live "Working..." streaming messages in Discord. |
| `webhooks` | map[string]string | `{}` | Named webhook URLs for outbound-only notifications. |
| `routes` | map[string]DiscordRouteConfig | `{}` | Map of channel ID to agent name for per-channel routing. |

### Slack

```json
{
  "slack": {
    "enabled": true,
    "botToken": "$SLACK_BOT_TOKEN",
    "signingSecret": "$SLACK_SIGNING_SECRET",
    "appToken": "$SLACK_APP_TOKEN",
    "defaultChannel": "C0123456789"
  }
}
```

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Enable Slack bot. |
| `botToken` | string | `""` | Slack bot OAuth token (`xoxb-...`). Supports `$ENV_VAR`. |
| `signingSecret` | string | `""` | Slack signing secret for request verification. Supports `$ENV_VAR`. |
| `appToken` | string | `""` | Slack app-level token for Socket Mode (`xapp-...`). Optional. Supports `$ENV_VAR`. |
| `defaultChannel` | string | `""` | Default channel ID for outbound notifications. |

### Outbound Webhooks

```json
{
  "webhooks": [
    {
      "url": "https://hooks.example.com/tetora",
      "headers": {"Authorization": "$WEBHOOK_TOKEN"},
      "events": ["success", "error"]
    }
  ]
}
```

| Field | Type | Default | Description |
|---|---|---|---|
| `url` | string | required | Webhook endpoint URL. |
| `headers` | map[string]string | `{}` | HTTP headers to include. Values support `$ENV_VAR`. |
| `events` | string[] | all | Events to send: `"success"`, `"error"`, `"timeout"`, `"all"`. Empty = all. |

### Incoming Webhooks

Incoming webhooks let external services trigger Tetora tasks via HTTP POST.

```json
{
  "incomingWebhooks": {
    "github": {
      "secret": "$GITHUB_WEBHOOK_SECRET",
      "agent": "engineer",
      "prompt": "A GitHub event occurred: {{.Body}}"
    }
  }
}
```

### Notification Channels

Named notification channels for routing task events to different Slack/Discord endpoints.

```json
{
  "notifications": [
    {
      "name": "alerts",
      "type": "slack",
      "webhookUrl": "$SLACK_ALERTS_WEBHOOK",
      "events": ["error"],
      "minPriority": "high"
    }
  ]
}
```

| Field | Type | Default | Description |
|---|---|---|---|
| `name` | string | `""` | Named reference used in job `channel` field (e.g., `"discord:alerts"`). |
| `type` | string | required | `"slack"` or `"discord"`. |
| `webhookUrl` | string | required | Webhook URL. Supports `$ENV_VAR`. |
| `events` | string[] | all | Filter by event type: `"all"`, `"error"`, `"success"`. |
| `minPriority` | string | all | Minimum priority: `"critical"`, `"high"`, `"normal"`, `"low"`. |

---

## Store (Template Marketplace)

```json
{
  "store": {
    "enabled": true,
    "registryUrl": "https://registry.tetora.dev/v1",
    "authToken": "$TETORA_STORE_TOKEN"
  }
}
```

### `store` — `StoreConfig`

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Enable the template store. |
| `registryUrl` | string | `"https://registry.tetora.dev/v1"` | Remote registry URL for browsing and installing templates. |
| `authToken` | string | `""` | Authentication token for the registry. Supports `$ENV_VAR`. |

---

## Cost and Alerts

### `costAlert` — `CostAlertConfig`

```json
{
  "costAlert": {
    "dailyLimit": 10.0,
    "weeklyLimit": 50.0,
    "dailyTokenLimit": 1000000,
    "action": "warn"
  }
}
```

| Field | Type | Default | Description |
|---|---|---|---|
| `dailyLimit` | float64 | `0` | Daily spend limit in USD. `0` = no limit. |
| `weeklyLimit` | float64 | `0` | Weekly spend limit in USD. `0` = no limit. |
| `dailyTokenLimit` | int | `0` | Total daily token cap (input + output). `0` = no cap. |
| `action` | string | `"warn"` | Action on limit breach: `"warn"` (log and notify) or `"pause"` (block new tasks). |

### `estimate` — `EstimateConfig`

Pre-execution cost estimation before running a task.

| Field | Type | Default | Description |
|---|---|---|---|
| `confirmThreshold` | float64 | `1.00` | Prompt for confirmation when estimated cost exceeds this USD value. |
| `defaultOutputTokens` | int | `500` | Fallback output token estimate when actual usage is unknown. |

### `budgets` — `BudgetConfig`

Agent-level and team-level cost budgets.

---

## Logging

```json
{
  "logging": {
    "level": "info",
    "format": "text",
    "file": "~/.tetora/runtime/logs/tetora.log",
    "maxSizeMB": 50,
    "maxFiles": 5
  }
}
```

### `logging` — `LoggingConfig`

| Field | Type | Default | Description |
|---|---|---|---|
| `level` | string | `"info"` | Log level: `"debug"`, `"info"`, `"warn"`, `"error"`. |
| `format` | string | `"text"` | Log format: `"text"` (human-readable) or `"json"` (structured). |
| `file` | string | `runtime/logs/tetora.log` | Log file path. Relative to runtime dir. |
| `maxSizeMB` | int | `50` | Maximum log file size in MB before rotation. |
| `maxFiles` | int | `5` | Number of rotated log files to retain. |

---

## Security

### `dashboardAuth` — `DashboardAuthConfig`

```json
{
  "dashboardAuth": {
    "enabled": true,
    "username": "admin",
    "password": "$DASHBOARD_PASSWORD"
  }
}
```

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Enable HTTP Basic Auth on the dashboard. |
| `username` | string | `"admin"` | Basic auth username. |
| `password` | string | `""` | Basic auth password. Supports `$ENV_VAR`. |
| `token` | string | `""` | Alternative: static token passed as a cookie. |

### `tls` — `TLSConfig`

```json
{
  "tls": {
    "certFile": "/etc/tetora/cert.pem",
    "keyFile": "/etc/tetora/key.pem"
  }
}
```

| Field | Type | Description |
|---|---|---|
| `certFile` | string | Path to TLS certificate PEM file. Enables HTTPS when set (together with `keyFile`). |
| `keyFile` | string | Path to TLS private key PEM file. |

### `rateLimit` — `RateLimitConfig`

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Enable per-IP request rate limiting. |
| `maxPerMin` | int | `60` | Maximum API requests per minute per IP. |

### `securityAlert` — `SecurityAlertConfig`

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Enable security alerts on repeated auth failures. |
| `failThreshold` | int | `10` | Number of failures in the window before alerting. |
| `failWindowMin` | int | `5` | Sliding window in minutes. |

### `approvalGates` — `ApprovalGateConfig`

Require human approval before certain tools execute.

```json
{
  "approvalGates": {
    "enabled": true,
    "timeout": 120,
    "tools": ["bash", "write_file"],
    "autoApproveTools": ["read_file"]
  }
}
```

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Enable approval gates. |
| `timeout` | int | `120` | Seconds to wait for approval before cancelling. |
| `tools` | string[] | `[]` | Tool names that require approval before execution. |
| `autoApproveTools` | string[] | `[]` | Tools pre-approved at startup (never prompt). |

---

## Reliability

### `circuitBreaker` — `CircuitBreakerConfig`

```json
{
  "circuitBreaker": {
    "enabled": true,
    "failThreshold": 5,
    "successThreshold": 2,
    "openTimeout": "30s"
  }
}
```

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `true` | Enable circuit breaker for provider failover. |
| `failThreshold` | int | `5` | Consecutive failures before opening the circuit. |
| `successThreshold` | int | `2` | Successes in half-open state before closing. |
| `openTimeout` | string | `"30s"` | Duration in open state before trying again (half-open). |

### `fallbackProviders`

```json
{
  "fallbackProviders": ["claude", "openai"]
}
```

Global ordered list of fallback providers if the default provider fails.

### `heartbeat` — `HeartbeatConfig`

```json
{
  "heartbeat": {
    "enabled": true,
    "interval": "30s",
    "stallThreshold": "5m",
    "timeoutWarnRatio": 0.8,
    "autoCancel": false,
    "notifyOnStall": true
  }
}
```

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Enable agent heartbeat monitoring. |
| `interval` | string | `"30s"` | How often to check running tasks for stalls. |
| `stallThreshold` | string | `"5m"` | No output for this duration = task is stalled. |
| `timeoutWarnRatio` | float64 | `0.8` | Warn when elapsed time exceeds this ratio of the task timeout. |
| `autoCancel` | bool | `false` | Automatically cancel tasks stalled longer than `2x stallThreshold`. |
| `notifyOnStall` | bool | `true` | Send a notification when a task is detected as stalled. |

### `retention` — `RetentionConfig`

Controls automatic cleanup of old data.

```json
{
  "retention": {
    "history": 90,
    "sessions": 30,
    "auditLog": 365,
    "logs": 14,
    "workflows": 90,
    "outputs": 30
  }
}
```

| Field | Type | Default | Description |
|---|---|---|---|
| `history` | int | `90` | Days to retain job run history. |
| `sessions` | int | `30` | Days to retain session data. |
| `auditLog` | int | `365` | Days to retain audit log entries. |
| `logs` | int | `14` | Days to retain log files. |
| `workflows` | int | `90` | Days to retain workflow run records. |
| `reflections` | int | `60` | Days to retain reflection records. |
| `sla` | int | `90` | Days to retain SLA check records. |
| `trustEvents` | int | `90` | Days to retain trust event records. |
| `handoffs` | int | `60` | Days to retain agent handoff/message records. |
| `queue` | int | `7` | Days to retain offline queue items. |
| `versions` | int | `180` | Days to retain config version snapshots. |
| `outputs` | int | `30` | Days to retain agent output files. |
| `uploads` | int | `7` | Days to retain uploaded files. |
| `memory` | int | `30` | Days before stale memory entries are archived. |
| `claudeSessions` | int | `3` | Days to retain Claude CLI session artifacts. |
| `piiPatterns` | string[] | `[]` | Regex patterns for PII redaction in stored content. |

---

## Quiet Hours and Digest

```json
{
  "quietHours": {
    "enabled": true,
    "start": "23:00",
    "end": "08:00",
    "tz": "Asia/Taipei",
    "digest": true
  },
  "digest": {
    "enabled": true,
    "time": "08:00",
    "tz": "Asia/Taipei"
  }
}
```

### `quietHours` — `QuietHoursConfig`

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Enable quiet hours. Notifications are suppressed during this window. |
| `start` | string | `""` | Start of quiet period (local time, `"HH:MM"` format). |
| `end` | string | `""` | End of quiet period (local time). |
| `tz` | string | local | Timezone, e.g. `"Asia/Taipei"`, `"UTC"`. |
| `digest` | bool | `false` | Send a digest of suppressed notifications when quiet hours end. |

### `digest` — `DigestConfig`

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Enable scheduled daily digest. |
| `time` | string | `"08:00"` | Time to send the digest (`"HH:MM"`). |
| `tz` | string | local | Timezone. |

---

## Tools

```json
{
  "tools": {
    "maxIterations": 10,
    "timeout": 120,
    "toolOutputLimit": 10240,
    "toolTimeout": 30,
    "defaultProfile": "standard",
    "builtin": {
      "bash": true,
      "web_search": false
    },
    "webSearch": {
      "provider": "brave",
      "apiKey": "$BRAVE_API_KEY",
      "maxResults": 5
    },
    "vision": {
      "provider": "anthropic",
      "apiKey": "$ANTHROPIC_API_KEY",
      "model": "claude-opus-4-5"
    }
  }
}
```

### `tools` — `ToolConfig`

| Field | Type | Default | Description |
|---|---|---|---|
| `maxIterations` | int | `10` | Maximum tool call iterations per task. |
| `timeout` | int | `120` | Global tool engine timeout in seconds. |
| `toolOutputLimit` | int | `10240` | Maximum characters per tool output (truncated beyond this). |
| `toolTimeout` | int | `30` | Per-tool execution timeout in seconds. |
| `defaultProfile` | string | `"standard"` | Default tool profile name. |
| `builtin` | map[string]bool | `{}` | Enable/disable individual built-in tools by name. |
| `profiles` | map[string]ToolProfile | `{}` | Custom tool profiles. |
| `trustOverride` | map[string]string | `{}` | Override trust level per tool name. |

### `tools.webSearch` — `WebSearchConfig`

| Field | Type | Default | Description |
|---|---|---|---|
| `provider` | string | `""` | Search provider: `"brave"`, `"tavily"`, `"searxng"`. |
| `apiKey` | string | `""` | API key for the provider. Supports `$ENV_VAR`. |
| `baseURL` | string | `""` | Custom endpoint (for self-hosted searxng). |
| `maxResults` | int | `5` | Maximum search results to return. |

### `tools.vision` — `VisionConfig`

| Field | Type | Default | Description |
|---|---|---|---|
| `provider` | string | `""` | Vision provider: `"anthropic"`, `"openai"`, `"google"`. |
| `apiKey` | string | `""` | API key. Supports `$ENV_VAR`. |
| `model` | string | `""` | Model name for the vision provider. |
| `maxImageSize` | int | `5242880` | Maximum image size in bytes (default 5 MB). |
| `baseURL` | string | `""` | Custom API endpoint. |

---

## MCP (Model Context Protocol)

### `mcpConfigs`

Named MCP server configurations. Each key is an MCP config name; the value is the full MCP JSON config. Tetora writes these to temp files and passes them to the claude binary via `--mcp-config`.

```json
{
  "mcpConfigs": {
    "playwright": {
      "mcpServers": {
        "playwright": {
          "command": "npx",
          "args": ["@playwright/mcp@latest"]
        }
      }
    }
  }
}
```

### `mcpServers`

Simplified MCP server definitions managed directly by Tetora.

```json
{
  "mcpServers": {
    "my-server": {
      "command": "python",
      "args": ["/path/to/server.py"],
      "env": {"API_KEY": "$MY_API_KEY"},
      "enabled": true
    }
  }
}
```

| Field | Type | Default | Description |
|---|---|---|---|
| `command` | string | required | Executable command. |
| `args` | string[] | `[]` | Command arguments. |
| `env` | map[string]string | `{}` | Environment variables for the process. Values support `$ENV_VAR`. |
| `enabled` | bool | `true` | Whether this MCP server is active. |

---

## Prompt Budget

Controls the maximum character budgets for each section of the system prompt. Adjust when prompts are being truncated unexpectedly.

```json
{
  "promptBudget": {
    "soulMax": 8000,
    "rulesMax": 4000,
    "knowledgeMax": 8000,
    "skillsMax": 4000,
    "maxSkillsPerTask": 3,
    "contextMax": 16000,
    "totalMax": 40000
  }
}
```

### `promptBudget` — `PromptBudgetConfig`

| Field | Type | Default | Description |
|---|---|---|---|
| `soulMax` | int | `8000` | Max characters for the agent soul/personality prompt. |
| `rulesMax` | int | `4000` | Max characters for workspace rules. |
| `knowledgeMax` | int | `8000` | Max characters for knowledge base content. |
| `skillsMax` | int | `4000` | Max characters for injected skills. |
| `maxSkillsPerTask` | int | `3` | Maximum number of skills injected per task. |
| `contextMax` | int | `16000` | Max characters for session context. |
| `totalMax` | int | `40000` | Hard cap on total system prompt size (all sections combined). |

---

## Agent Communication

Controls nested sub-agent dispatch (agent_dispatch tool).

```json
{
  "agentComm": {
    "enabled": true,
    "maxConcurrent": 3,
    "defaultTimeout": 900,
    "maxDepth": 3,
    "maxChildrenPerTask": 5
  }
}
```

### `agentComm` — `AgentCommConfig`

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Enable the `agent_dispatch` tool for nested sub-agent calls. |
| `maxConcurrent` | int | `3` | Max concurrent `agent_dispatch` calls globally. |
| `defaultTimeout` | int | `900` | Default sub-agent timeout in seconds. |
| `maxDepth` | int | `3` | Maximum nesting depth for sub-agents. |
| `maxChildrenPerTask` | int | `5` | Maximum concurrent child agents per parent task. |

---

## Examples

### Minimal Config

A minimal config to get started with the Claude CLI provider:

```json
{
  "claudePath": "/usr/local/bin/claude",
  "maxConcurrent": 3,
  "listenAddr": "127.0.0.1:8991",
  "apiToken": "$TETORA_API_TOKEN",
  "agents": {
    "coordinator": {
      "soulFile": "SOUL.md",
      "model": "sonnet",
      "description": "General-purpose agent."
    }
  }
}
```

### Multi-Agent Config with Smart Dispatch

```json
{
  "claudePath": "/usr/local/bin/claude",
  "maxConcurrent": 5,
  "defaultModel": "sonnet",
  "defaultTimeout": "30m",
  "defaultBudget": 2.0,
  "defaultPermissionMode": "acceptEdits",
  "listenAddr": "127.0.0.1:8991",
  "apiToken": "$TETORA_API_TOKEN",
  "defaultWorkdir": "~/workspace",
  "agents": {
    "coordinator": {
      "soulFile": "SOUL.md",
      "model": "sonnet",
      "description": "Coordinator. Handles planning, research, and coordination.",
      "keywords": ["plan", "research", "coordinate", "summarize"]
    },
    "engineer": {
      "soulFile": "team/engineer/SOUL.md",
      "model": "sonnet",
      "description": "Engineer. Handles coding, debugging, and infrastructure.",
      "keywords": ["code", "debug", "deploy"]
    },
    "creator": {
      "soulFile": "team/creator/SOUL.md",
      "model": "sonnet",
      "description": "Creator. Handles writing, documentation, and content.",
      "keywords": ["write", "blog", "translate"]
    }
  },
  "smartDispatch": {
    "enabled": true,
    "coordinator": "coordinator",
    "defaultAgent": "coordinator",
    "classifyBudget": 0.1,
    "classifyTimeout": "30s",
    "rules": [
      {
        "agent": "engineer",
        "keywords": ["bug", "error", "deploy", "CI/CD", "docker"],
        "patterns": ["(?:fix|resolve)\\s+(?:bug|issue|error)"]
      },
      {
        "agent": "creator",
        "keywords": ["blog post", "documentation", "README", "translation"]
      }
    ]
  },
  "costAlert": {
    "dailyLimit": 10.0,
    "action": "warn"
  },
  "logging": {
    "level": "info",
    "format": "text"
  }
}
```

### Full Config (All Major Sections)

```json
{
  "claudePath": "/usr/local/bin/claude",
  "maxConcurrent": 5,
  "defaultModel": "sonnet",
  "defaultTimeout": "30m",
  "defaultBudget": 2.0,
  "defaultPermissionMode": "acceptEdits",
  "listenAddr": "127.0.0.1:8991",
  "apiToken": "$TETORA_API_TOKEN",

  "providers": {
    "claude": {
      "type": "claude-cli",
      "path": "/usr/local/bin/claude"
    }
  },

  "agents": {
    "coordinator": {
      "soulFile": "SOUL.md",
      "model": "sonnet",
      "description": "Coordinator and general-purpose agent."
    }
  },

  "smartDispatch": {
    "enabled": true,
    "coordinator": "coordinator",
    "defaultAgent": "coordinator",
    "rules": []
  },

  "session": {
    "contextMessages": 20,
    "compaction": {
      "enabled": true,
      "maxMessages": 50,
      "compactTo": 10,
      "model": "haiku"
    }
  },

  "taskBoard": {
    "enabled": true,
    "autoDispatch": {
      "enabled": true,
      "interval": "5m",
      "maxConcurrentTasks": 3
    },
    "gitCommit": true,
    "gitPush": false
  },

  "slotPressure": {
    "enabled": true,
    "reservedSlots": 2,
    "warnThreshold": 3,
    "nonInteractiveTimeout": "5m"
  },

  "telegram": {
    "enabled": false,
    "botToken": "$TELEGRAM_BOT_TOKEN",
    "chatID": 0,
    "pollTimeout": 30
  },

  "discord": {
    "enabled": false,
    "botToken": "$DISCORD_BOT_TOKEN"
  },

  "slack": {
    "enabled": false,
    "botToken": "$SLACK_BOT_TOKEN",
    "signingSecret": "$SLACK_SIGNING_SECRET"
  },

  "store": {
    "enabled": true,
    "registryUrl": "https://registry.tetora.dev/v1"
  },

  "costAlert": {
    "dailyLimit": 10.0,
    "weeklyLimit": 50.0,
    "action": "warn"
  },

  "logging": {
    "level": "info",
    "format": "text",
    "maxSizeMB": 50,
    "maxFiles": 5
  },

  "retention": {
    "history": 90,
    "sessions": 30,
    "logs": 14
  },

  "heartbeat": {
    "enabled": true,
    "stallThreshold": "5m",
    "autoCancel": false
  },

  "dashboardAuth": {
    "enabled": false
  },

  "promptBudget": {
    "soulMax": 8000,
    "rulesMax": 4000,
    "knowledgeMax": 8000,
    "totalMax": 40000
  }
}
```
