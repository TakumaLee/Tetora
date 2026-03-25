---
title: "Troubleshooting Guide"
lang: "en"
order: 7
description: "Common issues and solutions for Tetora setup and operation."
---
# Troubleshooting Guide

This guide covers the most common issues encountered when running Tetora. For each issue, the most likely cause is listed first.

---

## tetora doctor

Always start here. Run `tetora doctor` after installation or when something stops working:

```
=== Tetora Doctor ===

  ✓ Config          /Users/you/.tetora/config.json
  ✓ Claude CLI      claude 1.2.3
  ✓ Provider        claude-cli
  ✓ Port            localhost:8991 in use (daemon running)
  ✓ Telegram        enabled (chatID=123456)
  ✓ Jobs            jobs.json (4 jobs, 3 enabled)
  ✓ History DB      12 tasks
  ✓ Workdir         /Users/you/dev
  ✓ Agent/ruri      Commander
  ✓ Binary          /Users/you/.tetora/bin/tetora
  ✓ Encryption      key configured
  ✓ ffmpeg          available
  ✓ sqlite3         available
  ✓ Agents Dir      /Users/you/.tetora/agents (3 agents)
  ✓ Workspace       /Users/you/.tetora/workspace

All checks passed.
```

Each line is one check. A red `✗` means a hard failure (the daemon will not work without fixing it). A yellow `~` means a suggestion (optional but recommended).

Common fixes for failed checks:

| Failed check | Fix |
|---|---|
| `Config: not found` | Run `tetora init` |
| `Claude CLI: not found` | Set `claudePath` in `config.json` or install Claude Code |
| `sqlite3: not found` | `brew install sqlite3` (macOS) or `apt install sqlite3` (Linux) |
| `Agent/name: soul file missing` | Create `~/.tetora/agents/{name}/SOUL.md` or run `tetora init` |
| `Workspace: not found` | Run `tetora init` to create directory structure |

---

## "session produced no output"

A task completes but the output is empty. The task is automatically marked `failed`.

**Cause 1: Context window too large.** The prompt injected into the session exceeded the model's context limit. Claude Code exits immediately when it cannot fit the context.

Fix: Enable session compaction in `config.json`:

```json
{
  "sessionCompaction": {
    "enabled": true,
    "tokenThreshold": 150000,
    "messageThreshold": 100,
    "strategy": "auto"
  }
}
```

Or reduce the amount of context injected into the task (shorter description, fewer spec comments, smaller `dependsOn` chain).

**Cause 2: Claude Code CLI startup failure.** The binary at `claudePath` crashes on startup — usually due to a bad API key, network issue, or version mismatch.

Fix: Run the Claude Code binary manually to see the error:

```bash
/usr/local/bin/claude --version
/usr/local/bin/claude -p "hello"
```

Fix the reported error, then retry the task:

```bash
tetora task move task-abc123 --status=todo
```

**Cause 3: Empty prompt.** The task has a title but no description, and the title alone is too ambiguous for the agent to act on. The session runs, produces output that does not satisfy the empty-check, and gets flagged.

Fix: Add a concrete description:

```bash
tetora task update task-abc123 \
  --description="Create src/ratelimit/bucket.go with a token bucket implementation..."
```

---

## "unauthorized" errors on the dashboard

The dashboard returns 401 or shows a blank page after reloading.

**Cause 1: Service Worker cached an old auth token.** The PWA Service Worker caches responses including auth headers. After a daemon restart with a new token, the cached version is stale.

Fix: Hard refresh the page. In Chrome/Safari:

- Mac: `Cmd + Shift + R`
- Windows/Linux: `Ctrl + Shift + R`

Or open DevTools → Application → Service Workers → click "Unregister", then reload.

**Cause 2: Referer header mismatch.** The dashboard's auth middleware validates the `Referer` header. Requests from browser extensions, proxies, or curl without a `Referer` header are rejected.

Fix: Access the dashboard directly at `http://localhost:8991/dashboard`, not through a proxy. If you need API access from external tools, use an API token instead of browser session auth.

---

## Dashboard not updating

The dashboard loads but the activity feed, worker list, or task board stays stale.

**Cause: Service Worker version mismatch.** The PWA Service Worker serves a cached version of the dashboard JS/HTML even after a `make bump` update.

Fix:

1. Hard refresh (`Cmd + Shift + R` / `Ctrl + Shift + R`)
2. If that does not work, open DevTools → Application → Service Workers → click "Update" or "Unregister"
3. Reload the page

**Cause: SSE connection dropped.** The dashboard receives live updates over Server-Sent Events. If the connection drops (network hiccup, laptop sleep), the feed stops updating.

Fix: Reload the page. The SSE connection re-establishes automatically on page load.

---

## "排程接近滿載" warning

You see this message in Discord/Telegram or the dashboard notification feed.

This is the slot pressure warning. It fires when available concurrency slots drop to or below `warnThreshold` (default: 3). It means Tetora is running near capacity.

**What to do:**

- If this is expected (many tasks running): no action needed. The warning is informational.
- If you are not running many tasks: check for stuck tasks in `doing` status:

```bash
tetora task list --status=doing
```

- If you want to raise capacity: increase `maxConcurrent` in `config.json` and adjust `slotPressure.warnThreshold` accordingly.
- If interactive sessions are being delayed: increase `slotPressure.reservedSlots` to hold more slots back for interactive use.

---

## Tasks stuck in "doing"

A task shows `status=doing` but no agent is actively working on it.

**Cause 1: Daemon restarted mid-task.** The task was running when the daemon was killed. On the next startup, Tetora checks for orphaned `doing` tasks and either restores them to `done` (if there is cost/duration evidence) or resets them to `todo`.

This is automatic — wait for the next daemon startup. If the daemon is already running and the task is still stuck, the heartbeat or stuck-task reset will catch it within `stuckThreshold` (default: 2h).

To force a reset immediately:

```bash
tetora task move task-abc123 --status=todo
```

**Cause 2: Heartbeat/stall detection.** The heartbeat monitor (`heartbeat.go`) checks running sessions. If a session produces no output for the stall threshold, it is automatically cancelled and the task is moved to `failed`.

Check task comments for `[auto-reset]` or `[stall-detected]` system comments:

```bash
tetora task show task-abc123 --full
```

**Manual cancel via API:**

```bash
curl -X POST http://localhost:8991/api/tasks/task-abc123/cancel
```

---

## Worktree merge failures

A task finishes and moves to `partial-done` with a comment like `[worktree] merge failed`.

This means the agent's changes on the task branch conflict with `main`.

**Recovery steps:**

```bash
# See the task details and which branch was created
tetora task show task-abc123 --full

# Navigate to the project repo
cd /path/to/your/repo

# Merge the branch manually
git merge feat/kokuyou-task-abc123

# Resolve conflicts in your editor, then commit
git add .
git commit -m "merge: feat/kokuyou-task-abc123"

# Mark the task done
tetora task move task-abc123 --status=done
```

The worktree directory is preserved at `~/.tetora/runtime/worktrees/task-abc123/` until you manually clean it up or move the task to `done`.

---

## High token costs

Sessions are using more tokens than expected.

**Cause 1: Context not being compacted.** Without session compaction, each turn accumulates the full conversation history. Long-running tasks (many tool calls) grow the context linearly.

Fix: Enable `sessionCompaction` (see "session produced no output" section above).

**Cause 2: Large knowledge base or rule files.** Files in `workspace/rules/` and `workspace/knowledge/` are injected into every agent prompt. If these files are large, they consume tokens on every call.

Fix:
- Audit `workspace/knowledge/` — keep individual files under 50 KB.
- Move reference material you rarely need out of the auto-inject paths.
- Run `tetora knowledge list` to see what is being injected and its size.

**Cause 3: Wrong model routing.** An expensive model (Opus) is being used for routine tasks.

Fix: Review `defaultModel` in agent config and set a cheaper default for bulk tasks:

```json
{
  "taskBoard": {
    "autoDispatch": {
      "defaultModel": "sonnet"
    }
  }
}
```

---

## Provider timeout errors

Tasks fail with timeout errors like `context deadline exceeded` or `provider request timed out`.

**Cause 1: Task timeout too short.** The default timeout may be too short for complex tasks.

Fix: Set a longer timeout in the task's agent config or per-task:

```json
{
  "roles": {
    "kokuyou": {
      "timeout": "60m"
    }
  }
}
```

Or increase the LLM timeout estimate by adding more detail to the task description (Tetora uses the description to estimate timeout via a fast model call).

**Cause 2: API rate limiting or contention.** Too many concurrent requests hitting the same provider.

Fix: Reduce `maxConcurrentTasks` or add a `maxBudget` to throttle expensive tasks:

```json
{
  "autoDispatch": {
    "maxConcurrentTasks": 2,
    "maxBudget": 3.0
  }
}
```

---

## `make bump` interrupted a workflow

You ran `make bump` while a workflow or task was executing. The daemon restarted mid-task.

The restart triggers Tetora's orphan recovery logic:

- Tasks with completion evidence (cost recorded, duration recorded) are restored to `done`.
- Tasks with no completion evidence but past the grace period (2 minutes) are reset to `todo` for re-dispatch.
- Tasks updated within the last 2 minutes are left alone until the next stuck-task scan.

**To check what happened:**

```bash
tetora task list --status=doing
tetora task list --status=failed
```

Review task comments for `[auto-restore]` or `[auto-reset]` entries.

**If you need to prevent bumps during active tasks** (not yet available as a flag), check that no tasks are running before bumping:

```bash
tetora task list --status=doing
# If empty, safe to bump
make bump
```

---

## SQLite errors

You see errors like `database is locked`, `SQLITE_BUSY`, or `index.lock` in logs.

**Cause 1: Missing WAL mode pragma.** Without WAL mode, SQLite uses exclusive file locking, which causes `database is locked` under concurrent reads/writes.

All Tetora DB calls go through `queryDB()` and `execDB()` which prepend `PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;`. If you are calling sqlite3 directly in scripts, add these pragmas:

```bash
sqlite3 ~/.tetora/history.db \
  "PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; SELECT count(*) FROM tasks;"
```

**Cause 2: Stale `index.lock` file.** Git operations leave `index.lock` if interrupted. The worktree manager checks for stale locks before starting git work, but a crash can leave one behind.

Fix:

```bash
# Find stale lock files
find ~/.tetora/runtime/worktrees -name "index.lock"

# Remove them (only if no git operation is actively running)
rm /path/to/repo/.git/index.lock
```

---

## Discord / Telegram not responding

Messages to the bot produce no reply.

**Cause 1: Wrong channel configuration.** Discord has two channel lists: `channelIDs` (direct reply to all messages) and `mentionChannelIDs` (only reply when @-mentioned). If a channel is in neither list, messages are ignored.

Fix: Check `config.json`:

```json
{
  "discord": {
    "enabled": true,
    "channelIDs": ["123456789012345678"],
    "mentionChannelIDs": []
  }
}
```

**Cause 2: Bot token expired or wrong.** Telegram bot tokens do not expire, but Discord tokens can be invalidated if the bot is kicked from the server or the token is regenerated.

Fix: Re-create the bot token in the Discord developer portal and update `config.json`.

**Cause 3: Daemon not running.** The bot gateway is only active when `tetora serve` is running.

Fix:

```bash
tetora status
tetora serve   # if not running
```

---

## glab / gh CLI errors

Git integration fails with errors from `glab` or `gh`.

**Common error: `gh: command not found`**

Fix:
```bash
brew install gh      # macOS
gh auth login        # authenticate
```

**Common error: `glab: You are not logged in`**

Fix:
```bash
brew install glab    # macOS
glab auth login      # authenticate with your GitLab instance
```

**Common error: `remote: HTTP Basic: Access denied`**

Fix: Check that your SSH key or HTTPS credential is configured for the repository host. For GitLab:

```bash
glab auth status
ssh -T git@gitlab.com   # test SSH connectivity
```

For GitHub:

```bash
gh auth status
ssh -T git@github.com
```

**PR/MR creation succeeds but points to wrong base branch**

By default, PRs target the repository's default branch (`main` or `master`). If your workflow uses a different base, set it explicitly in your post-task git configuration or ensure the repository's default branch is configured correctly in the hosting platform.
