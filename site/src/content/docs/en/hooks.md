---
title: "Claude Code Hooks Integration"
lang: "en"
order: 3
description: "Integrate with Claude Code Hooks for real-time session observation."
---
# Claude Code Hooks Integration

## Overview

Claude Code Hooks are an event system built into Claude Code that fires shell commands at key points during a session. Tetora registers itself as a hook receiver so it can observe every running agent session in real time — without polling, without tmux, and without injecting wrapper scripts.

**What hooks enable:**

- Real-time progress tracking in the dashboard (tool calls, session state, live workers list)
- Cost and token monitoring via statusline bridge
- Tool use auditing (which tools ran, in which session, in which directory)
- Session completion detection and automatic task status updates
- Plan mode gate: holds `ExitPlanMode` until a human approves the plan in the dashboard
- Interactive question routing: `AskUserQuestion` is redirected to the MCP bridge so questions surface in your chat platform instead of blocking the terminal

Hooks are the recommended integration path as of Tetora v2.0. The older tmux-based approach (v1.x) still works but does not support hooks-only features like the plan gate and question routing.

---

## Architecture

```
Claude Code session
  │
  ├── PreToolUse  ──────────────────► Tetora /api/hooks/event
  │   (ExitPlanMode)                  └─► Plan gate: long-poll until approved
  │   (AskUserQuestion)               └─► Deny: redirect to MCP bridge
  │
  ├── PostToolUse ──────────────────► Tetora /api/hooks/event
  │                                   └─► Update worker state
  │                                   └─► Detect plan file writes
  │
  ├── Stop        ──────────────────► Tetora /api/hooks/event
  │                                   └─► Mark worker done
  │                                   └─► Trigger task completion
  │
  └── Notification ─────────────────► Tetora /api/hooks/event
                                      └─► Forward to Discord/Telegram
```

The hook command is a small curl call injected into Claude Code's `~/.claude/settings.json`. Every event is posted to `POST /api/hooks/event` on the running Tetora daemon.

---

## Setup

### Install hooks

With the Tetora daemon running:

```bash
tetora hooks install
```

This writes entries into `~/.claude/settings.json` and generates the MCP bridge config at `~/.tetora/mcp/bridge.json`.

Example of what gets written to `~/.claude/settings.json`:

```json
{
  "hooks": {
    "PostToolUse": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "curl -s -X POST http://localhost:8991/api/hooks/event -H 'Content-Type: application/json' -d @-"
          }
        ]
      }
    ],
    "Stop": [ { "hooks": [ { "type": "command", "command": "..." } ] } ],
    "Notification": [ { "hooks": [ { "type": "command", "command": "..." } ] } ],
    "PreToolUse": [
      {
        "matcher": "ExitPlanMode",
        "hooks": [ { "type": "command", "command": "...", "timeout": 600 } ]
      },
      {
        "matcher": "AskUserQuestion",
        "hooks": [ { "type": "command", "command": "..." } ]
      }
    ]
  }
}
```

### Check status

```bash
tetora hooks status
```

Output shows which hooks are installed, how many Tetora rules are registered, and the total event count received since the daemon started.

You can also check from the dashboard: **Engineering Details → Hooks** shows the same status plus a live event feed.

### Remove hooks

```bash
tetora hooks remove
```

Removes all Tetora entries from `~/.claude/settings.json`. Existing non-Tetora hooks are preserved.

---

## Hook Events

### PostToolUse

Fires after every tool call completes. Tetora uses this to:

- Track which tools an agent is using (`Bash`, `Write`, `Edit`, `Read`, etc.)
- Update the worker's `lastTool` and `toolCount` in the live workers list
- Detect when an agent writes to a plan file (triggers plan cache update)

### Stop

Fires when a Claude Code session ends (natural completion or cancellation). Tetora uses this to:

- Mark the worker as `done` in the live workers list
- Publish a completion SSE event to the dashboard
- Trigger downstream task status updates for taskboard tasks

### Notification

Fires when Claude Code sends a notification (e.g. permission required, long pause). Tetora forwards these to Discord/Telegram and publishes them to the dashboard SSE stream.

### PreToolUse: ExitPlanMode (plan gate)

When an agent is about to exit plan mode, Tetora intercepts the event with a long-poll (timeout: 600 seconds). The plan content is cached and surfaced in the dashboard under the session's detail view.

A human can approve or reject the plan from the dashboard. If approved, the hook returns and Claude Code proceeds. If rejected (or if the timeout expires), the exit is blocked and Claude Code stays in plan mode.

### PreToolUse: AskUserQuestion (question routing)

When Claude Code tries to ask the user a question interactively, Tetora intercepts it and denies the default behavior. The question is routed instead through the MCP bridge, surfacing in your configured chat platform (Discord, Telegram, etc.) so you can answer without sitting at a terminal.

---

## Real-Time Progress Tracking

Once hooks are installed, the dashboard **Workers** panel shows live sessions:

| Field | Source |
|---|---|
| Session ID | `session_id` in hook event |
| State | `working` / `idle` / `done` |
| Last tool | Most recent `PostToolUse` tool name |
| Working directory | `cwd` from hook event |
| Tool count | Cumulative `PostToolUse` count |
| Cost / tokens | Statusline bridge (`POST /api/hooks/usage`) |
| Origin | Linked task or cron job if dispatched by Tetora |

Cost and token data come from the Claude Code statusline script, which posts to `/api/hooks/usage` at a configurable interval. The statusline script is separate from the hooks — it reads the Claude Code status bar output and forwards it to Tetora.

---

## Cost Monitoring

The usage endpoint (`POST /api/hooks/usage`) receives:

```json
{
  "sessionId": "abc123",
  "costUsd": 0.0042,
  "inputTokens": 8200,
  "outputTokens": 340,
  "contextPct": 12,
  "model": "claude-sonnet-4-5"
}
```

This data is visible in the dashboard Workers panel and is aggregated into the daily cost charts. Budget alerts fire when a session's cost exceeds the configured per-role or global budget.

---

## Troubleshooting

### Hooks not firing

**Check daemon is running:**
```bash
tetora status
```

**Check hooks are installed:**
```bash
tetora hooks status
```

**Check settings.json directly:**
```bash
cat ~/.claude/settings.json | grep -A5 "hooks"
```

If the hooks key is missing, re-run `tetora hooks install`.

**Check daemon can receive hook events:**
```bash
curl -s -X POST http://localhost:8991/api/hooks/event \
  -H "Content-Type: application/json" \
  -d '{"hook_event_name":"Stop","session_id":"test-123"}'
# Expected: {"ok":true}
```

If the daemon is not listening on the expected port, check `listenAddr` in `config.json`.

### Permission errors on settings.json

Claude Code's `settings.json` is at `~/.claude/settings.json`. If the file is owned by another user or has restrictive permissions:

```bash
ls -la ~/.claude/settings.json
chmod 644 ~/.claude/settings.json
```

### Dashboard workers panel is empty

1. Confirm hooks are installed and the daemon is running.
2. Start a Claude Code session manually and run one tool (e.g. `ls`).
3. Check the dashboard Workers panel — the session should appear within seconds.
4. If not, check daemon logs: `tetora logs -f | grep hooks`

### Plan gate not appearing

The plan gate only activates when Claude Code tries to call `ExitPlanMode`. This only happens in plan mode sessions (started with `--plan` or set via `permissionMode: "plan"` in the role config). Interactive `acceptEdits` sessions do not use plan mode.

### Questions not routing to chat

The `AskUserQuestion` deny hook requires the MCP bridge to be configured. Run `tetora hooks install` again — it regenerates the bridge config. Then add the bridge to your Claude Code MCP settings:

```bash
cat ~/.tetora/mcp/bridge.json
```

Add that file as an MCP server in `~/.claude/settings.json` under `mcpServers`.

---

## Migration from tmux (v1.x)

In Tetora v1.x, agents ran inside tmux panes and Tetora monitored them by reading pane output. In v2.0, agents run as bare Claude Code processes and Tetora observes them through hooks.

**If you are upgrading from v1.x:**

1. Run `tetora hooks install` once after upgrading.
2. Remove any tmux session management configuration from `config.json` (`tmux.*` keys are now ignored).
3. Existing session history is preserved in `history.db` — no migration needed.
4. The `tetora session list` command and the Sessions tab in the dashboard continue to work as before.

The tmux terminal bridge (`discord_terminal.go`) is still available for interactive terminal access via Discord. This is separate from agent execution — it lets you send keystrokes to a running terminal session. Hooks and the terminal bridge are complementary, not mutually exclusive.
