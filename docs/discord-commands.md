# Discord Commands

Tetora responds to commands prefixed with `!` in any channel where the bot is active. You can also mention the bot with free text for smart dispatch routing.

---

## Quick Reference

| Command | Description |
|---------|-------------|
| `!model` | Show all agents grouped by Cloud / Local |
| `!model pick [agent]` | Interactive model picker (buttons + dropdowns) |
| `!model <model> [agent]` | Set model directly (auto-detects provider) |
| `!local [agent]` | Switch agent(s) to local models (Ollama) |
| `!cloud [agent]` | Restore agent(s) to cloud models |
| `!mode` | Show inference mode summary with toggle buttons |
| `!chat <agent>` | Lock this channel to a specific agent |
| `!end` | Unlock channel, resume smart dispatch |
| `!new` | Start a new session (clear context) |
| `!compact` | Summarize current session & carry forward |
| `!context` / `!ctx` | Show session context usage (tokens, %) |
| `!ask <prompt>` | One-off question (no routing, no session) |
| `!cancel` | Cancel all running tasks |
| `!status` | Show daemon status |
| `!jobs` | List cron jobs |
| `!cost` | Show cost summary (today / week / month) |
| `!approve [tool\|reset]` | Manage auto-approved tools |
| `!help` | Show command reference |
| `@Tetora <text>` | Smart dispatch to best agent |

---

## Model & Provider Management

### Viewing current models

```
!model
```

Displays all agents grouped by provider type:

```
Cloud (14):
  kohaku — claude-sonnet-4-6 (anthropic)
  hisui  — gpt-5.4 (codex)

Local (3):
  ruby   — gemma4:e4b (ollama)

Mode: mixed | !model pick [agent] | !local | !cloud
```

Includes interactive buttons: **Pick Model**, **Switch All Local**, **Switch All Cloud**.

### Interactive model picker

```
!model pick
!model pick kohaku
```

Walks through a 3-step flow using Discord buttons and select menus:

1. **Select agent** (skipped if agent name is provided)
2. **Select provider** (Anthropic, OpenAI, Google, Groq, Ollama, LM Studio, Codex)
3. **Select model** (populated from the chosen provider)

The provider is auto-created if it doesn't exist yet.

### Direct model switch

```
!model gpt-5.4 kohaku
```

Sets the model directly. The provider is inferred from the model name:

| Model prefix | Provider |
|-------------|----------|
| `gpt-*`, `o1-*`, `o3-*`, `o4-*` | Codex CLI |
| `claude-*`, `sonnet`, `opus`, `haiku` | Anthropic |
| `gemini-*` | Google |
| `llama-*`, `mixtral-*` | Groq |

If no agent name is given, the default agent is used.

### Session handling on provider switch

When a model switch causes the **provider to change** (e.g. `claude-code` to `codex`), Discord automatically archives the current session and starts a new one. This is necessary because session/thread IDs from one provider are invalid in another.

Same-provider model changes (e.g. `claude-sonnet-4-6` to `claude-opus-4-6`) keep the existing session.

> **Note:** The Dashboard does not automatically clear sessions when switching providers. If you change an agent's provider via the Dashboard and then use it in Discord, run `!new` first to avoid stale session errors.

---

## Remote / Local Switching

### Switch to local

```
!local           # switch ALL agents to Ollama
!local kohaku    # switch only kohaku
```

What happens:
- Saves each agent's current cloud model (restored by `!cloud`)
- Sets model to the agent's preferred `localModel`, or the first available Ollama model
- Agents with `pinMode` set are skipped (pinned)
- Requires Ollama to be running (`ollama serve`)

### Switch to cloud

```
!cloud           # restore ALL agents to cloud models
!cloud kohaku    # restore only kohaku
```

Restores each agent to its saved cloud model and infers the correct provider.

### Mode summary

```
!mode
```

Shows current mode (cloud / local / mixed) with agent counts and toggle buttons.

---

## Agent Chat

### Lock to an agent

```
!chat kokuyou
```

All messages in this channel go directly to kokuyou, bypassing smart dispatch.

### Unlock

```
!end
```

Resumes smart dispatch routing.

---

## Sessions

### New session

```
!new
```

Clears the current conversation context and starts fresh.

### Inspect context usage

```
!context
!ctx
```

Shows the current session's token usage, a visual progress bar, message count, and bound agent. Color-coded: green (<70%), yellow (70–90%), red (≥90%).

Use this to decide whether to `!compact` (summarize) or `!new` (hard reset) before the bot hits its auto-reset threshold (`session.maxContextTokens`, default `60000`).

### Compact session

```
!compact
```

Summarizes the current session and carries the summary forward into a fresh session. Unlike `!new`, prior context is preserved as a condensed summary — useful when the conversation is long but you still want continuity.

Compaction strategy is controlled by `session.compaction.strategy` in config:
- `"fresh-session"` — archives the session and starts a new one seeded with the summary
- Default — in-place summarization within the same session

### One-off question

```
!ask What is the capital of France?
```

Sends a single prompt without creating a session or routing through dispatch.

---

## Operations

### Status

```
!status
```

Shows daemon version, uptime, running tasks, and cron job count.

### Cost

```
!cost
```

Displays spending breakdown: today, this week, this month.

### Cancel

```
!cancel
```

Cancels all currently running dispatch tasks.

### Approval gates

```
!approve           # list auto-approved tools
!approve Bash      # auto-approve Bash tool
!approve reset     # clear all auto-approvals
```

---

## Configuration

### Per-agent settings

Each agent can be configured in `config.json` with:

```json
{
  "agents": {
    "kohaku": {
      "model": "claude-sonnet-4-6",
      "provider": "anthropic",
      "localModel": "gemma4:e4b",
      "fallbackProviders": ["ollama", "openai"],
      "pinMode": ""
    }
  }
}
```

| Field | Description |
|-------|-------------|
| `model` | Current active model |
| `provider` | Current provider name |
| `localModel` | Preferred model when switching to local |
| `cloudModel` | Saved cloud model (auto-set by `!local`) |
| `fallbackProviders` | Fallback chain when primary provider fails |
| `pinMode` | `"cloud"`, `"local"`, or `""` (follows global mode) |

### Global inference mode

```json
{
  "inferenceMode": "local"
}
```

When set, overrides all agents without `pinMode`. Values: `"cloud"`, `"local"`, or `""` (mixed).

---

## See Also

- [Discord Multitasking](discord-multitasking.md) -- Thread & Focus for parallel conversations
- [Configuration](configuration.md) -- Full config reference
