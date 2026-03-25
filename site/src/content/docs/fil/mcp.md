---
title: "MCP (Model Context Protocol) Integration"
lang: "fil"
order: 5
description: "Expose Tetora capabilities to any MCP-compatible client."
---
# MCP (Model Context Protocol) Integration

Kasama sa Tetora ang isang built-in na MCP server na nagpapahintulot sa mga AI agent (Claude Code, atbp.) na makipag-ugnayan sa mga API ng Tetora sa pamamagitan ng karaniwang MCP protocol.

## Arkitektura

```
Claude Code  ──stdio──>  tetora mcp-server  ──HTTP──>  Tetora Daemon
  (client)                (bridge process)              (localhost:8991)
```

Ang MCP server ay isang **stdio JSON-RPC 2.0 bridge** — nagbabasa ito ng mga request mula sa stdin, pinro-proxy ang mga ito sa HTTP API ng Tetora, at nagsusulat ng mga tugon sa stdout. Inilulunsad ito ng Claude Code bilang isang child process.

## Setup

### 1. Idagdag ang MCP server sa mga setting ng Claude Code

Idagdag ang sumusunod sa `~/.claude/settings.json`:

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

Palitan ang path ng iyong aktwal na lokasyon ng `tetora` binary. Hanapin ito gamit ang:

```bash
which tetora
# o
ls ~/.tetora/bin/tetora
```

### 2. Tiyaking tumatakbo ang Tetora daemon

Ang MCP bridge ay nagpo-proxy sa Tetora HTTP API, kaya dapat tumatakbo ang daemon:

```bash
tetora start
```

### 3. I-verify

I-restart ang Claude Code. Ang mga MCP tool ay lalabas bilang available na tool na may prefix na `tetora_`.

## Mga Available na Tool

### Pamamahala ng Task

| Tool | Paglalarawan |
|------|-------------|
| `tetora_taskboard_list` | Ilista ang mga ticket ng kanban board. Mga opsyonal na filter: `project`, `assignee`, `priority`. |
| `tetora_taskboard_update` | I-update ang isang task (status, assignee, priority, title). Kailangan ang `id`. |
| `tetora_taskboard_comment` | Magdagdag ng komento sa isang task. Kailangan ang `id` at `comment`. |

### Memory

| Tool | Paglalarawan |
|------|-------------|
| `tetora_memory_get` | Basahin ang isang memory entry. Kailangan ang `agent` at `key`. |
| `tetora_memory_set` | Isulat ang isang memory entry. Kailangan ang `agent`, `key`, at `value`. |
| `tetora_memory_search` | Ilista ang lahat ng memory entry. Opsyonal na filter: `role`. |

### Dispatch

| Tool | Paglalarawan |
|------|-------------|
| `tetora_dispatch` | Mag-dispatch ng task sa ibang agent. Gumagawa ng bagong Claude Code session. Kailangan ang `prompt`. Opsyonal: `agent`, `workdir`, `model`. |

### Knowledge

| Tool | Paglalarawan |
|------|-------------|
| `tetora_knowledge_search` | Hanapin sa shared knowledge base. Kailangan ang `q`. Opsyonal: `limit`. |

### Mga Notification

| Tool | Paglalarawan |
|------|-------------|
| `tetora_notify` | Magpadala ng notification sa user sa pamamagitan ng Discord/Telegram. Kailangan ang `message`. Opsyonal: `level` (info/warn/error). |
| `tetora_ask_user` | Magtanong sa user sa pamamagitan ng Discord at hintayin ang tugon (hanggang 6 na minuto). Kailangan ang `question`. Opsyonal: `options` (mga quick-reply button, max 4). |

## Mga Detalye ng Tool

### tetora_taskboard_list

```json
{
  "project": "tetora",
  "assignee": "kokuyou",
  "priority": "P0"
}
```

Lahat ng parameter ay opsyonal. Nagbabalik ng JSON array ng mga task.

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

Ang `id` lamang ang kailangan. Ang ibang field ay ina-update lamang kung ibinibigay. Mga value ng status: `todo`, `in_progress`, `review`, `done`.

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

Ang `prompt` lamang ang kailangan. Kung aalisin ang `agent`, ang smart dispatch ng Tetora ay magru-route sa pinaka-angkop na agent.

### tetora_ask_user

```json
{
  "question": "Should I proceed with the database migration?",
  "options": ["Yes", "No", "Skip for now"]
}
```

Ito ay isang **blocking call** — naghihintay ito nang hanggang 6 na minuto para tumugon ang user sa pamamagitan ng Discord. Nakikita ng user ang tanong na may mga opsyonal na quick-reply button at maaari ring mag-type ng custom na sagot.

## Mga CLI Command

### Pamamahala ng Mga External na MCP Server

Maaari ring kumilos ang Tetora bilang MCP **host**, na kumokonekta sa mga external na MCP server:

```bash
# Ilista ang mga na-configure na MCP server
tetora mcp list

# Ipakita ang buong config para sa isang server
tetora mcp show <name>

# Magdagdag ng bagong MCP server
tetora mcp add <name> --command CMD [--args A1,A2] [--env K=V,K2=V2]

# Alisin ang config ng isang server
tetora mcp remove <name>

# Subukan ang koneksyon ng server
tetora mcp test <name>
```

### Pagpapatakbo ng MCP Bridge

```bash
# Simulan ang MCP bridge server (karaniwan ay inilulunsad ng Claude Code, hindi manu-mano)
tetora mcp-server
```

Sa unang pagpapatakbo, nagge-generate ito ng `~/.tetora/mcp/bridge.json` na may tamang path ng binary.

## Configuration

Mga MCP-related na setting sa `config.json`:

| Field | Uri | Default | Paglalarawan |
|------|------|---------|-------------|
| `mcpServers` | object | `{}` | Mapa ng mga external na MCP server config (pangalan → {command, args, env}). |

Binabasa ng bridge server ang `listenAddr` at `apiToken` mula sa pangunahing config para kumonekta sa daemon.

## Authentication

Kung nakatakda ang `apiToken` sa `config.json`, awtomatikong isinasama ng MCP bridge ang `Authorization: Bearer <token>` sa lahat ng HTTP request sa daemon. Hindi na kailangan ng karagdagang MCP-level na auth.

## Pag-troubleshoot

**Hindi lumalabas ang mga tool sa Claude Code:**
- I-verify na tama ang path ng binary sa `settings.json`
- Tiyaking tumatakbo ang Tetora daemon (`tetora start`)
- Suriin ang mga log ng Claude Code para sa mga MCP connection error

**Mga error na "HTTP 401":**
- Ang `apiToken` sa `config.json` ay dapat tumugma. Awtomatiko itong binabasa ng bridge.

**Mga error na "connection refused":**
- Hindi tumatakbo ang daemon, o hindi tumutugma ang `listenAddr`. Default: `127.0.0.1:8991`.

**Nag-ti-timeout ang `tetora_ask_user`:**
- Ang user ay may 6 na minuto para tumugon sa pamamagitan ng Discord. Tiyaking nakakonekta ang Discord bot.
