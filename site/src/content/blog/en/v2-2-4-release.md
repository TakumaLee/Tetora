---
title: "Tetora v2.2.4 — Discord Reliability, Worktree Safety, and Dispatch Hardening"
lang: en
date: "2026-04-13"
tag: release
readTime: "~5 min"
excerpt: "v2.2.4 hardens the runtime: Discord now retries sends and auto-recovers stale sessions, a worktree lock prevents Bash tool failures, dispatched tasks survive HTTP disconnects, and coordinator findings are no longer truncated."
description: "Tetora v2.2.4 release notes: Discord send retry, stale session recovery, worktree session lock, HTTP dispatch decoupling, provider resilience, coordinator truncation fix, and per-agent concurrency config."
---

v2.2.4 is a focused stability release. Most of the changes are invisible when things go right — but they're the reason things stay right under load, after provider switches, and across long-running agent sessions.

> **TL;DR:** Discord now retries failed sends and auto-recovers stale sessions. A session lock prevents worktree cleanup from breaking the Bash tool. Dispatched tasks survive HTTP disconnects. Provider errors are classified and retried. Findings summaries are no longer truncated. `maxTasksPerAgent` caps concurrency per agent.

---

## Discord: Reliability Under Pressure

Discord was the most-improved surface in this release. Three fixes ship together:

### Send Retry

Discord message delivery now retries on transient failures. Before this fix, a single failed API call during a long agent response would silently drop the message. Tetora now retries with backoff and logs a warning if all attempts fail.

### Persist Output Before Chunking

Agent output is now written to disk before being split into Discord message chunks. Previously, a crash mid-chunking could result in partial delivery with no recovery path. With persistence in place, a restart can resume from the last written checkpoint.

### Stale Session Auto-Recovery

If a Discord session becomes stale — which happens when you switch providers, move to a new machine, or restart the daemon — Tetora now detects the condition and heals the session automatically. The previous behavior required a manual `/reset` or config restart.

```
[discord] stale session detected for agent hisui — recovering
[discord] session recovered: channel 1234567890 → agent hisui
```

No user action needed.

---

## Agent Safety: Worktree Session Lock

When an agent is using a git worktree, Tetora now acquires a session lock on that worktree path. Cleanup jobs — including scheduled maintenance and the `tetora worktree prune` command — wait for the lock to be released before proceeding.

**Why this matters:** Previously, if a worktree was deleted while a Claude session was actively using it, the Bash tool would enter a permanent failure state for the rest of that session. The agent appeared to be running but could no longer execute shell commands. This was especially painful for long-running tasks.

The fix introduces a `SessionLockFile` constant and an advisory lock mechanism:

```go
// Tetora acquires this lock when a Claude session starts in a worktree
const SessionLockFile = ".tetora-session.lock"

// Cleanup jobs check for the lock before deleting
func pruneWorktree(path string) error {
    if isLocked(path) {
        return ErrSessionActive
    }
    return os.RemoveAll(path)
}
```

If a cleanup job finds a locked worktree, it skips it and logs a warning. The worktree is cleaned up the next time prune runs after the session ends.

---

## HTTP: Dispatch Context Decoupling

Dispatched tasks now run in their own context, independent of the HTTP request that created them.

Before this fix, a long-running agent task inherited the context of the `/api/dispatch` HTTP request. If the HTTP client disconnected — or if a proxy upstream timed out — the context was cancelled, and the task was killed mid-execution.

The fix creates a detached context for each dispatched task:

```go
// Before: task died when the HTTP request did
taskCtx := r.Context()

// After: task lives independently
taskCtx := context.WithoutCancel(r.Context())
```

The HTTP response still returns immediately once the task is enqueued. The task runs to completion regardless of the client's connection state.

---

## Provider Resilience

### Claude Error Classification

Tetora now distinguishes between transient Claude API errors (rate limits, temporary overload) and permanent failures (invalid API key, account issues). Transient errors trigger automatic retry with exponential backoff. Permanent errors surface immediately without wasting retry budget.

### Codex Quota Detection

Quota and usage-limit errors from Codex — which can appear in either stdout or stderr — are now detected and handled correctly. When a quota error is detected, Tetora schedules a retry after a backoff interval rather than marking the task as failed.

```
[provider] codex quota exceeded — retrying in 45s (attempt 2/3)
```

---

## Coordinator: No More Truncation

The coordinator's findings summary was previously capped at 500 characters before being passed to the next agent in a multi-agent chain. This caused silent data loss when agent outputs were verbose — the receiving agent would make decisions based on an incomplete picture.

The 500-character limit has been removed. Findings are now passed in full between agents.

---

## Per-Agent Concurrency Limit

A new `maxTasksPerAgent` field in `config.json` caps the number of tasks that can run concurrently on a single agent:

```json
{
  "agents": [
    {
      "name": "hisui",
      "role": "researcher",
      "maxTasksPerAgent": 2
    }
  ]
}
```

When an agent is at capacity, new tasks are queued rather than dispatched immediately. This prevents a burst of parallel requests from degrading a single agent's performance — particularly relevant for agents backed by rate-limited API keys or local hardware.

The default (if unset) is no limit, preserving backward-compatible behavior.

---

## Workflow Hardening

Two fixes improve the reliability of the workflow engine:

**Template ref validation** — Workflow templates that reference a non-existent step or agent now fail fast at dispatch time with a clear error message, rather than failing silently mid-run.

**DB write correctness** — `InitWorkflowRunsTable` and related write operations were using `db.Query` instead of `db.Exec`. While functionally equivalent for SQLite, this was semantically incorrect and caused connection pool warnings under load. All write paths now use `db.Exec`.

---

## Other Fixes

- **Workspace symlink traversal** — `tetora workspace files` now follows symlinks when listing files, consistent with how agents navigate the workspace.
- **Session compaction URL preservation** — URLs and unique identifiers (hashes, IDs) are now preserved during session compaction, preventing reference breakage in long sessions.
- **Exit-0 warning log** — The runner now emits a `WARN` log when a CLI invocation exits with code 0 but produces no output, distinguishing silent success from silent failure.
- **ctx propagation** — Context cancellation is now correctly threaded through DB calls inside goroutines, preventing context leaks on task cancellation.

---

## Upgrade

```bash
tetora upgrade
```

Single binary. No external dependencies. macOS / Linux / Windows.

[View full changelog on GitHub](https://github.com/TakumaLee/Tetora/releases/tag/v2.2.4)
