<p align="center">
  <img src="assets/banner.png" alt="Tetora — AI Agent Orchestrator" width="800">
</p>

<p align="center">
  <strong>Self-hosted AI assistant platform with multi-agent architecture.</strong>
</p>

<p align="center">
  <strong>English</strong> | <a href="docs/i18n/README.zh-TW.md">繁體中文</a> | <a href="docs/i18n/README.ja.md">日本語</a> | <a href="docs/i18n/README.ko.md">한국어</a> | <a href="docs/i18n/README.id.md">Bahasa Indonesia</a> | <a href="docs/i18n/README.th.md">ภาษาไทย</a> | <a href="docs/i18n/README.fil.md">Filipino</a> | <a href="docs/i18n/README.es.md">Español</a> | <a href="docs/i18n/README.fr.md">Français</a> | <a href="docs/i18n/README.de.md">Deutsch</a>
</p>

Tetora runs as a single Go binary with zero external dependencies. It connects to the AI providers you already use, integrates with the messaging platforms your team lives on, and keeps all data on your own hardware.

---

## What is Tetora

Tetora is an AI agent orchestrator that lets you define multiple agent roles -- each with its own personality, system prompt, model, and tool access -- and interact with them through chat platforms, HTTP APIs, or the command line.

**Core capabilities:**

- **Multi-agent roles** -- define distinct agents with separate personalities, budgets, and tool permissions
- **Multi-provider** -- Claude API, OpenAI, Gemini, and more; swap or combine freely
- **Multi-platform** -- Telegram, Discord, Slack, Google Chat, LINE, Matrix, Teams, Signal, WhatsApp, iMessage
- **Web dashboard** -- CEO command center with ROI metrics, pixel office, and live activity feed
- **Cron jobs** -- schedule recurring tasks with approval gates and notifications
- **Knowledge base** -- feed documents to agents for grounded responses
- **Persistent memory** -- agents remember context across sessions; unified memory layer with consolidation
- **MCP support** -- connect Model Context Protocol servers as tool providers
- **Skills and workflows** -- composable skill packs and multi-step workflow pipelines
- **Workflow Engine** -- DAG-based pipeline execution with condition branches, parallel steps, retry logic, and dynamic model routing (Sonnet for routine tasks, Opus for complex ones)
- **Template Marketplace** -- Store tab for browsing, importing, and exporting workflow templates
- **Taskboard Auto-Dispatch** -- Kanban board with automatic task assignment, configurable concurrent slots, and slot pressure system that reserves capacity for interactive sessions
- **GitLab MR + GitHub PR** -- automatic PR/MR creation after workflow completion; auto-detects the remote host
- **Session Compaction** -- token-based and message-count-based automatic context compression to keep sessions within model limits
- **Service Worker PWA** -- offline-capable dashboard with smart caching
- **Partial-done Status** -- tasks that complete but fail post-processing (git merge, review) enter a recoverable intermediate state instead of being lost
- **Webhooks** -- trigger agent actions from external systems
- **Cost governance** -- per-role and global budgets with automatic model downgrade
- **Data retention** -- configurable cleanup policies per table, with full export and purge
- **Plugins** -- extend functionality via external plugin processes
- **Smart reminders, habits, goals, contacts, finance tracking, briefings, and more**

---

## Quick Start

### For engineers

```bash
# Install the latest release
. <(curl -fsSL https://raw.githubusercontent.com/TakumaLee/Tetora/main/install.sh)

# Run the setup wizard
tetora init

# Verify everything is configured correctly
tetora doctor

# Start the daemon
tetora serve
```

### For non-engineers

1. Go to the [Releases page](https://github.com/TakumaLee/Tetora/releases/latest)
2. Download the binary for your platform (e.g. `tetora-darwin-arm64` for Apple Silicon Mac)
3. Move it to a directory in your PATH and rename it to `tetora`, or place it in `~/.tetora/bin/`
4. Open a terminal and run:
   ```
   tetora init
   tetora doctor
   tetora serve
   ```

---

## Agents

Every Tetora agent is more than a chatbot — it has an identity. Each agent (called a **role**) is defined by a **soul file**: a Markdown document that gives the agent its personality, expertise, communication style, and behavioral guidelines.

### Defining a role

Roles are declared in `config.json` under the `roles` key:

```json
{
  "roles": {
    "default": {
      "soulFile": "SOUL.md",
      "model": "sonnet",
      "description": "General-purpose assistant",
      "permissionMode": "acceptEdits"
    },
    "researcher": {
      "soulFile": "SOUL-researcher.md",
      "model": "opus",
      "description": "Deep research and analysis",
      "permissionMode": "plan"
    }
  }
}
```

### Soul files

A soul file tells the agent *who it is*. Place it in the workspace directory (`~/.tetora/workspace/` by default):

```markdown
# Koto — Soul File

## Identity
You are Koto, a thoughtful assistant who lives inside the Tetora system.
You speak in a warm, concise tone and prefer actionable advice.

## Expertise
- Software architecture and code review
- Technical writing and documentation

## Behavioral Guidelines
- Think step by step before answering
- Ask clarifying questions when the request is ambiguous
- Record important decisions in memory for future reference

## Output Format
- Start with a one-line summary
- Use bullet points for details
- End with next steps if applicable
```

### Getting started

`tetora init` walks you through creating your first role and generates a starter soul file automatically. You can edit it at any time — changes take effect on the next session.

---

## Dashboard

Tetora ships with a built-in web dashboard at `http://localhost:8991/dashboard`. It is organized into four zones:

| Zone | Contents |
|------|----------|
| **Command Center** | Executive summary (ROI cards), pixel team sprites, expandable Agent World office |
| **Operations** | Compact ops bar, agent scorecard + live activity feed (side-by-side), running tasks |
| **Insights** | 7-day trend chart, historical task throughput and cost charts |
| **Engineering Details** | Cost dashboard, cron jobs, sessions, provider health, trust, SLA, version history, routing, memory, and more (collapsible) |

The agent editor includes a **provider-aware model picker** with one-click switching between cloud and local models (Ollama). A global **inference mode toggle** lets you switch all agents between cloud and local with a single button. Each agent card shows a Cloud/Local badge and a quick-switch dropdown.

Multiple themes are available (Glass, Clean, Material, Boardroom, Retro). The Agent World pixel office can be customized with decorations and zoom controls.

```bash
# Open the dashboard in your default browser
tetora dashboard
```

---

## Discord Commands

Tetora responds to `!` prefix commands in Discord:

| Command | Description |
|---------|-------------|
| `!model` | Show all agents grouped by Cloud / Local |
| `!model pick [agent]` | Interactive model picker (buttons + dropdowns) |
| `!model <model> [agent]` | Set model directly (auto-detects provider) |
| `!local [agent]` | Switch to local models (Ollama) |
| `!cloud [agent]` | Restore cloud models |
| `!mode` | Inference mode summary with toggle buttons |
| `!chat <agent>` | Lock channel to a specific agent |
| `!end` | Unlock channel, resume smart dispatch |
| `!new` | Start new session |
| `!compact` | Summarize & carry forward current session |
| `!context` / `!ctx` | Show session token usage (bar + %) |
| `!ask <prompt>` | One-off question |
| `!cancel` | Cancel all running tasks |
| `!approve [tool\|reset]` | Manage auto-approved tools |
| `!status` / `!cost` / `!jobs` | Operations overview |
| `!help` | Show command reference |
| `@Tetora <text>` | Smart dispatch to best agent |

**[Full Discord Commands Reference](docs/discord-commands.md)** -- model switching, remote/local toggle, provider config, and more.

---

## Build from Source

```bash
git clone https://github.com/TakumaLee/Tetora.git
cd tetora
make install
```

This builds the binary and installs it to `~/.tetora/bin/tetora`. Make sure `~/.tetora/bin` is in your `PATH`.

To run the test suite:

```bash
make test
```

---

## Requirements

| Requirement | Details |
|---|---|
| **sqlite3** | Must be available on `PATH`. Used for all persistent storage. |
| **AI provider API key** | At least one: Claude API, OpenAI, Gemini, or any OpenAI-compatible endpoint. |
| **Go 1.25+** | Only required if building from source. |

---

## Supported Platforms

| Platform | Architectures | Status |
|---|---|---|
| macOS | amd64, arm64 | Stable |
| Linux | amd64, arm64 | Stable |
| Windows | amd64 | Beta |

---

## Architecture

All runtime data lives under `~/.tetora/`:

```
~/.tetora/
  config.json        Main configuration (providers, roles, integrations)
  jobs.json          Cron job definitions
  history.db         SQLite database (history, memory, sessions, embeddings, ...)
  bin/               Installed binary
  agents/            Per-agent soul files (agents/{name}/SOUL.md)
  workspace/
    rules/           Governance rules, auto-injected into all agent prompts
    memory/          Shared observations readable/writable by any agent
    knowledge/       Reference documents (auto-injected up to 50 KB)
    skills/          Reusable procedures, loaded by prompt matching
    tasks/           Task files and todo lists
  runtime/
    sessions/        Per-agent session files
    outputs/         Generated output files
    logs/            Structured log files
    cache/           Temporary cache
```

Configuration uses plain JSON with support for `$ENV_VAR` references, so secrets never need to be hardcoded. The setup wizard (`tetora init`) generates a working `config.json` interactively.

Hot-reload is supported: send `SIGHUP` to the running daemon to reload `config.json` without downtime.

---

## Session Compaction

Tetora automatically compresses session context when it grows too large. This directly affects **API cost** — large sessions cause expensive cache writes on every request.

### How it works

Every agent session accumulates conversation history. As the history grows, each API call pays more for prompt cache writes. Compaction kicks in automatically once a session crosses the configured threshold.

Two strategies are available:

| Strategy | Behaviour | Best for |
|---|---|---|
| `inline` (default) | Truncates old messages in Tetora's DB; Claude CLI session file stays intact | Maximising conversation continuity |
| `fresh-session` | Summarises the full history → saves to memory → archives the session; next message starts from a clean JSONL | Minimising long-term cache write cost |

**Why `fresh-session` matters for cost:** With `inline`, the Claude CLI `.jsonl` file keeps growing even after compaction, so cache write costs accumulate in proportion to total context size. With `fresh-session`, the CLI session resets to zero — every compaction cycle caps the cache write exposure.

### Configuration

```json
{
  "session": {
    "compactAfter": 30,
    "compactTokens": 50000,
    "compaction": {
      "strategy": "fresh-session"
    }
  }
}
```

| Field | Default | Description |
|---|---|---|
| `compactAfter` | `30` | Trigger compaction after this many messages |
| `compactTokens` | `200000` | Trigger compaction after this many input tokens |
| `compaction.strategy` | `"inline"` | `"inline"` or `"fresh-session"` |
| `compaction.model` | coordinator model | Model used to generate the summary |

Compaction runs asynchronously in the background and has exponential backoff on failure. The summary generated by `fresh-session` is automatically injected into the next session's system prompt, so the agent retains key context.

### Tradeoff

`fresh-session` eliminates cache write accumulation at the cost of native `--resume` continuity. For long-running personal assistant use cases (large context, frequent idle periods), `fresh-session` with `compactTokens: 50000` is the recommended setting.

---

## Workflows

Tetora includes a built-in workflow engine for orchestrating multi-step, multi-agent tasks. Define your pipeline in JSON, and let agents collaborate automatically.

**[Full Workflow Documentation](docs/workflow.md)** — step types, variables, triggers, CLI & API reference.

Quick example:

```bash
# Validate and import a workflow
tetora workflow create examples/workflow-basic.json

# Run it
tetora workflow run research-and-summarize --var topic="LLM safety"

# Check results
tetora workflow status <run-id>
```

See [`examples/`](examples/) for ready-to-use workflow JSON files.

---

## CLI Reference

| Command | Description |
|---|---|
| `tetora init` | Interactive setup wizard |
| `tetora doctor` | Health checks and diagnostics |
| `tetora serve` | Start daemon (chat bots + HTTP API + cron) |
| `tetora run --file tasks.json` | Dispatch tasks from a JSON file (CLI mode) |
| `tetora dispatch "Summarize this"` | Run an ad-hoc task via the daemon |
| `tetora route "Review code security"` | Smart dispatch -- auto-route to the best role |
| `tetora status` | Quick overview of daemon, jobs, and cost |
| `tetora job list` | List all cron jobs |
| `tetora job trigger <name>` | Manually trigger a cron job |
| `tetora role list` | List all configured roles |
| `tetora role show <name>` | Show role details and soul preview |
| `tetora history list` | Show recent execution history |
| `tetora history cost` | Show cost summary |
| `tetora session list` | List recent sessions |
| `tetora memory list` | List agent memory entries |
| `tetora knowledge list` | List knowledge base documents |
| `tetora skill list` | List available skills |
| `tetora workflow list` | List configured workflows |
| `tetora workflow run <name>` | Run a workflow (pass `--var key=value` for variables) |
| `tetora workflow status <run-id>` | Show the status of a workflow run |
| `tetora workflow export <name>` | Export a workflow to a shareable JSON file |
| `tetora workflow create <file>` | Validate and import a workflow from a JSON file |
| `tetora mcp list` | List MCP server connections |
| `tetora budget show` | Show budget status |
| `tetora config show` | Show current configuration |
| `tetora config validate` | Validate config.json |
| `tetora backup` | Create a backup archive |
| `tetora restore <file>` | Restore from a backup archive |
| `tetora dashboard` | Open the web dashboard in a browser |
| `tetora logs` | View daemon logs (`-f` to follow, `--json` for structured output) |
| `tetora health` | Runtime health (daemon, workers, taskboard, disk) |
| `tetora drain` | Graceful shutdown: stop new tasks, wait for running agents |
| `tetora data status` | Show data retention status |
| `tetora security scan` | Security scanning and baseline |
| `tetora prompt list` | Manage prompt templates |
| `tetora project add` | Add a project to the workspace |
| `tetora guide` | Interactive onboarding guide |
| `tetora upgrade` | Upgrade to latest version |
| `tetora service install` | Install as a launchd service (macOS) |
| `tetora completion <shell>` | Generate shell completions (bash, zsh, fish) |
| `tetora version` | Show version |

Run `tetora help` for the full command reference.

---

## Contributing

Contributions are welcome. Please open an issue to discuss larger changes before submitting a pull request.

- **Issues**: [github.com/TakumaLee/Tetora/issues](https://github.com/TakumaLee/Tetora/issues)
- **Discussions**: [github.com/TakumaLee/Tetora/discussions](https://github.com/TakumaLee/Tetora/discussions)

This project is licensed under the AGPL-3.0, which requires that derivative works and network-accessible deployments also be open source under the same license. Please review the license before contributing.

---

## License

[AGPL-3.0](https://www.gnu.org/licenses/agpl-3.0.html)

Copyright (c) Tetora contributors.
