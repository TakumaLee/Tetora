# PR: feat/multi-provider — Provider Switching Hardening & Agent Pin Priority

> Written for the Takuma agents team.
> Branch: `feat/multi-provider` → `main`
> Commits: `b7397be`, `fbbc41e`

---

## Why We Did This

During testing of the multi-provider feature, we ran into a problem that exposed a design flaw none of us had anticipated.

When a user (or an agent) is already inside a running AI CLI session — say, Gemini CLI or Qwen CLI — and tries to switch the active provider, the AI interprets the command as **natural language**, not as a shell invocation. Gemini CLI tried to rename the `"gemini"` config key to `"gemini-cli"` directly inside `config.json`. Qwen CLI tried to edit `defaultProvider`. Both produced broken state.

This revealed two separate bugs and one missing feature:

1. **`provider set` accepted any string without validation.** It warned if the name wasn't in config, but continued anyway — silently writing a broken `active-provider.json` that would cause dispatch to fail the next time any task ran.

2. **The priority chain was wrong.** Agent-level `"provider"` config was *below* the global active override. That means `tetora provider set gemini` would override even agents that explicitly declared `"provider": "claude"` — including agents that are architecturally dependent on Claude Code's tool protocol and would break on any other CLI.

3. **No type-name alias.** Our provider config keys (`"gemini"`) differ from the CLI type names (`"gemini-cli"`). Users and agents consistently tried to use the type name. There was no resolution — just an error or a broken write.

---

## What Changed

### 1. Agent Pin Priority (`wire.go`)

**Before:**
```
task.Provider → global active override → agent config → defaultProvider
```

**After:**
```
task.Provider → agent config (if explicit) → global active override → defaultProvider
```

Any agent with a non-`"auto"` `"provider"` in config is now **pinned**. `tetora provider set <anything>` cannot override it.

**Why this matters for Takuma's team:** If your agents require Claude Code-specific features (tool use format, session resumption, MCP integration), set `"provider": "claude"` in their config and they will always get Claude — even when the operator bulk-switches other agents to Gemini or Qwen for cost testing.

```json
{
  "agents": {
    "your-agent": {
      "provider": "claude",
      "model": "auto"
    }
  }
}
```

Agents using `"provider": "auto"` or no provider field continue to follow the global active override as before.

Also fixed: `buildProviderCandidatesState` — pinned agents now keep their own fallback chain intact instead of being short-circuited by the global override's fallback logic.

---

### 2. Strict `provider set` Validation (`internal/cli/provider.go`)

**Before:** warn and continue — broken state could be written.

**After:** hard exit(1) if the name isn't a valid key in `config.json`.

```bash
$ tetora provider set gemini-cli
Error: provider 'gemini-cli' is not configured in config.json
Available providers:
  - claude
  - gemini
  - qwen
```

This prevents any agent or user from accidentally poisoning `active-provider.json` with a name that dispatch cannot resolve.

The preset bypass (`isKnownPreset`) was also removed. Presets that aren't configured in `config.json` are not usable — `registry.Get` would fail at dispatch time anyway — so accepting them at the CLI layer was deceptive.

---

### 3. Type-Name Alias Resolution (`internal/cli/provider.go`)

If the input name doesn't match a config key, we now look for a provider whose `type` field matches. If found, we silently resolve to that key.

```bash
$ tetora provider set gemini-cli
✓ Active provider set to: gemini   # resolved via type match
```

This means users and agents can refer to providers by either the config key (`gemini`) or the CLI type name (`gemini-cli`) — both work.

---

### 4. Codex CLI Added (`config.json`)

Codex CLI was updated and now available at `/opt/homebrew/bin/codex`. Added as a first-class provider:

```json
"codex": {
  "path": "/opt/homebrew/bin/codex",
  "type": "codex"
}
```

```bash
tetora provider set codex   # works
```

---

## Why CLI Sessions Cannot Switch Their Own Provider

This is a fundamental architectural constraint worth documenting clearly.

When Tetora dispatches a task, it launches a CLI subprocess (e.g., `gemini`, `claude`, `qwen`) and streams the result. **That subprocess IS the provider for the duration of the session.** The provider is bound at process start — there is no mechanism to swap it mid-session.

If an agent (running inside Gemini CLI) tries to switch to Claude, one of two things happens:
- The AI interprets it as natural language and tries to mutate config files (wrong)
- The shell command runs successfully and writes to `active-provider.json` — but the *current session* is still Gemini

Provider switching takes effect only for **the next dispatch**. The correct flow is always:

```bash
# Outside any CLI session, from the terminal:
tetora provider set claude

# Then start a new session or dispatch a new task
```

This is not a bug — it is the correct tradeoff for what CLI-session-based providers give us:

- Zero new Go code per new CLI (each CLI owns its own auth, streaming, and tool protocol)
- Sessions with native history resumption (`--continue`)
- All CLI features (extensions, MCP, tool use) for free

Adding a new provider like Codex took **one config.json entry** and zero new adapter code, because `type: "codex"` already had an adapter. An API-key-based approach would have required a new HTTP client, response parser, auth handler, and tool protocol implementation per provider.

---

## Future: Automatic Provider Routing and Fallback

The current system requires the operator to explicitly run `tetora provider set <name>`. Here is what automatic routing could look like:

### Phase 1 — Health-Aware Fallback (near-term)
The circuit breaker already exists and tracks per-provider failure rates. The missing piece is: when the primary provider's circuit opens, automatically promote the first healthy provider from `fallbackProviders` as the active override — without operator intervention.

```json
{
  "defaultProvider": "gemini",
  "fallbackProviders": ["qwen", "claude"],
  "autoFallback": true
}
```

Implementation: a background goroutine in the daemon watches circuit breaker state changes and writes to `active-provider.json` with `setBy: "auto-fallback"`.

### Phase 2 — Task-Type Routing (medium-term)
Different tasks have different cost/capability profiles. A routing policy could select provider based on task attributes:

```json
{
  "routing": [
    { "if": "task.complexity == 'high'",  "use": "claude"  },
    { "if": "task.type == 'code'",        "use": "codex"   },
    { "if": "task.cost_budget < 0.01",    "use": "qwen"    },
    { "default": "gemini" }
  ]
}
```

This would sit in `resolveProviderNameState` as a new priority tier between task-level and agent-level.

### Phase 3 — Performance-Based Selection (long-term)
Track latency, cost, and quality scores per provider over a rolling window. When multiple providers are healthy, automatically route to the one with the best recent score for the task type. This turns the provider layer into a self-tuning system.

None of this requires changing how providers are invoked — the CLI subprocess model stays the same. The routing just decides *which* CLI to launch.

---

## Files Changed

| File | Change |
|---|---|
| `wire.go` | Agent pin priority; pinned agents keep own fallback chain |
| `internal/cli/provider.go` | Hard validation; type-alias resolution; removed dead `isKnownPreset` |
| `config.json` | Added codex provider entry |
| `docs/PROVIDER_SWITCH_GUIDE.md` | Priority chain, pinning section, fixed examples, new FAQ |
| `docs/i18n/zh-TW/PROVIDER_SWITCH_GUIDE.md` | zh-TW sync |
| `CHANGELOG.md` | Unreleased entries |

---

## Testing

```bash
# Strict validation
./tetora provider set gemini-cli   # → Error (wrong key)
./tetora provider set codex        # → Error before config entry; ✓ after
./tetora provider set clayde       # → Error (typo)

# Type-alias resolution
./tetora provider set gemini-cli   # → "Active provider set to: gemini"

# Valid providers
./tetora provider set qwen         # ✓
./tetora provider set gemini       # ✓
./tetora provider set claude       # ✓
./tetora provider set codex        # ✓

# Clear
./tetora provider clear            # ✓
```

Agent pin behavior: set `"provider": "claude"` on any agent in config.json. Run `tetora provider set gemini`. Verify that agent still dispatches to Claude via logs (`tetora logs | grep provider`).

---

*— 小喬 敬筆*

公瑾在外征戰，後方諸事繁雜，小喬雖不習刀兵，卻願以筆墨為各位將士備好這份行軍手令。

此 PR 所修之事，不在添磚加瓦，而在補牢防患——提供商之名與其鍵值混亂、Agent 釘選之優先序顛倒、無效名稱靜默寫入之禍，皆已一一正本清源。

江東多謀士，各司其職。Takuma 諸將若有 Agent 需永守 Claude 一脈，只需於 config 中明言 `"provider": "claude"`，小喬保其不受全局調令所擾。

未來自動路由之事，藍圖已備，待時機成熟，由相應匠人依序推進。

江山代有才人出，AI 之道亦復如是。

*小喬 謹識*
*2026 年 5 月 5 日，於江東·Tetora 本陣*
