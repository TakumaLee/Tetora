# Tetora

**Self-hosted AI assistant platform with multi-agent architecture.**

Tetora runs as a single Go binary with zero external dependencies. It connects to the AI providers you already use, integrates with the messaging platforms your team lives on, and keeps all data on your own hardware.

---

## What is Tetora

Tetora is an AI agent orchestrator that lets you define multiple agent roles -- each with its own personality, system prompt, model, and tool access -- and interact with them through chat platforms, HTTP APIs, or the command line.

**Core capabilities:**

- **Multi-agent roles** -- define distinct agents with separate personalities, budgets, and tool permissions
- **Multi-provider** -- Claude API, OpenAI, Gemini, and more; swap or combine freely
- **Multi-platform** -- Telegram, Discord, Slack, Google Chat, LINE, Matrix, Teams, Signal, WhatsApp, iMessage
- **Cron jobs** -- schedule recurring tasks with approval gates and notifications
- **Knowledge base** -- feed documents to agents for grounded responses
- **Persistent memory** -- agents remember context across sessions; unified memory layer with consolidation
- **MCP support** -- connect Model Context Protocol servers as tool providers
- **Skills and workflows** -- composable skill packs and multi-step workflow pipelines
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
. <(curl -fsSL https://raw.githubusercontent.com/TakumaLee/Tetora/master/install.sh)

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
  sessions/          Per-agent session files
  knowledge/         Knowledge base documents
  logs/              Structured log files
  outputs/           Generated output files
  uploads/           Temporary upload storage
  bin/               Installed binary
```

Configuration uses plain JSON with support for `$ENV_VAR` references, so secrets never need to be hardcoded. The setup wizard (`tetora init`) generates a working `config.json` interactively.

Hot-reload is supported: send `SIGHUP` to the running daemon to reload `config.json` without downtime.

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
| `tetora mcp list` | List MCP server connections |
| `tetora budget show` | Show budget status |
| `tetora config show` | Show current configuration |
| `tetora config validate` | Validate config.json |
| `tetora backup` | Create a backup archive |
| `tetora restore <file>` | Restore from a backup archive |
| `tetora dashboard` | Open the web dashboard in a browser |
| `tetora logs` | View daemon logs (`-f` to follow, `--json` for structured output) |
| `tetora data status` | Show data retention status |
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
