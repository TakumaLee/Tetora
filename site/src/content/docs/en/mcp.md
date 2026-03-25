---
title: "MCP (Model Context Protocol) Integration"
lang: "en"
order: 5
description: "Expose Tetora capabilities to any MCP-compatible client."
---
# MCP (Model Context Protocol) Integration

Tetora includes a built-in MCP server that allows AI agents (Claude Code, etc.) to interact with Tetora's APIs through the standard MCP protocol.

## Architecture

```
Claude Code  ──stdio──>  tetora mcp-server  ──HTTP──>  Tetora Daemon
  (client)                (bridge process)              (localhost:8991)
```

The MCP server is a **stdio JSON-RPC 2.0 bridge** — it reads requests from stdin, proxies them to Tetora's HTTP API, and writes responses to stdout. Claude Code launches it as a child process.

## Setup

### 1. Add MCP server to Claude Code settings

Add the following to `~/.claude/settings.json`:

```json
{
  "mcpServers": {
    "tetora": {
      "command": "/Users/you/.tetora/bin/tetora",
      "args": ["mcp-server"]
    }
  }
}
```

Replace the path with your actual `tetora` binary location. Find it with:

```bash
which tetora
# or
ls ~/.tetora/bin/tetora
```

### 2. Ensure the Tetora daemon is running

The MCP bridge proxies to the Tetora HTTP API, so the daemon must be running:

```bash
tetora start
```

### 3. Verify

Restart Claude Code. The MCP tools will appear as available tools prefixed with `tetora_`.

## Available Tools

### Task Management

| Tool | Description |
|------|-------------|
| `tetora_taskboard_list` | List kanban board tickets. Optional filters: `project`, `assignee`, `priority`. |
| `tetora_taskboard_update` | Update a task (status, assignee, priority, title). Requires `id`. |
| `tetora_taskboard_comment` | Add a comment to a task. Requires `id` and `comment`. |

### Memory

| Tool | Description |
|------|-------------|
| `tetora_memory_get` | Read a memory entry. Requires `agent` and `key`. |
| `tetora_memory_set` | Write a memory entry. Requires `agent`, `key`, and `value`. |
| `tetora_memory_search` | List all memory entries. Optional filter: `role`. |

### Dispatch

| Tool | Description |
|------|-------------|
| `tetora_dispatch` | Dispatch a task to another agent. Creates a new Claude Code session. Requires `prompt`. Optional: `agent`, `workdir`, `model`. |

### Knowledge

| Tool | Description |
|------|-------------|
| `tetora_knowledge_search` | Search the shared knowledge base. Requires `q`. Optional: `limit`. |

### Notifications

| Tool | Description |
|------|-------------|
| `tetora_notify` | Send a notification to the user via Discord/Telegram. Requires `message`. Optional: `level` (info/warn/error). |
| `tetora_ask_user` | Ask the user a question via Discord and wait for a response (up to 6 minutes). Requires `question`. Optional: `options` (quick-reply buttons, max 4). |

## Tool Details

### tetora_taskboard_list

```json
{
  "project": "tetora",
  "assignee": "kokuyou",
  "priority": "P0"
}
```

All parameters are optional. Returns JSON array of tasks.

### tetora_taskboard_update

```json
{
  "id": "TASK-42",
  "status": "in_progress",
  "assignee": "kokuyou",
  "priority": "P1",
  "title": "New title"
}
```

Only `id` is required. Other fields update only if provided. Status values: `todo`, `in_progress`, `review`, `done`.

### tetora_taskboard_comment

```json
{
  "id": "TASK-42",
  "comment": "Started working on this",
  "author": "kokuyou"
}
```

### tetora_dispatch

```json
{
  "prompt": "Fix the broken CSS on the dashboard sidebar",
  "agent": "kokuyou",
  "workdir": "/path/to/project",
  "model": "sonnet"
}
```

Only `prompt` is required. If `agent` is omitted, Tetora's smart dispatch routes to the best agent.

### tetora_ask_user

```json
{
  "question": "Should I proceed with the database migration?",
  "options": ["Yes", "No", "Skip for now"]
}
```

This is a **blocking call** — it waits up to 6 minutes for the user to respond via Discord. The user sees the question with optional quick-reply buttons and can also type a custom answer.

## CLI Commands

### Managing External MCP Servers

Tetora can also act as an MCP **host**, connecting to external MCP servers:

```bash
# List configured MCP servers
tetora mcp list

# Show full config for a server
tetora mcp show <name>

# Add a new MCP server
tetora mcp add <name> --command CMD [--args A1,A2] [--env K=V,K2=V2]

# Remove a server config
tetora mcp remove <name>

# Test server connection
tetora mcp test <name>
```

### Running the MCP Bridge

```bash
# Start the MCP bridge server (normally launched by Claude Code, not manually)
tetora mcp-server
```

On first run, this generates `~/.tetora/mcp/bridge.json` with the correct binary path.

## Configuration

MCP-related settings in `config.json`:

| Field | Type | Default | Description |
|------|------|---------|-------------|
| `mcpServers` | object | `{}` | Map of external MCP server configs (name → {command, args, env}). |

The bridge server reads `listenAddr` and `apiToken` from the main config to connect to the daemon.

## Authentication

If `apiToken` is set in `config.json`, the MCP bridge automatically includes `Authorization: Bearer <token>` in all HTTP requests to the daemon. No additional MCP-level auth is needed.

## Troubleshooting

**Tools not appearing in Claude Code:**
- Verify the binary path in `settings.json` is correct
- Ensure the Tetora daemon is running (`tetora start`)
- Check Claude Code logs for MCP connection errors

**"HTTP 401" errors:**
- The `apiToken` in `config.json` must match. The bridge reads it automatically.

**"connection refused" errors:**
- The daemon isn't running, or `listenAddr` doesn't match. Default: `127.0.0.1:8991`.

**`tetora_ask_user` timing out:**
- The user has 6 minutes to respond via Discord. Ensure the Discord bot is connected.
