---
title: "Tetora v2.2 — Safety by Default, Multi-Tenant Dispatch"
lang: en
date: "2026-03-30"
tag: release
readTime: "~6 min"
excerpt: "DangerousOpsConfig blocks destructive commands before agents run them. Multi-tenant --client flag, worktree failure preservation, History CLI for failure analysis — v2.2 brings agent dispatch to production-grade."
description: "Tetora v2.2 adds DangerousOpsConfig for command blocking, multi-tenant dispatch isolation, worktree failure preservation, self-liveness watchdog, and History CLI diagnostics across three patch releases."
---

Tetora v2.2 raises the bar on safety, reliability, and agent observability. Spanning three patch releases (v2.2.0 → v2.2.2), over 30 improvements make multi-agent dispatch more resilient in production and set the foundation for enterprise-grade deployments.

> **TL;DR:** DangerousOpsConfig blocks destructive commands before agents execute them. Worktree isolation now covers all tasks. New History CLI for failure analysis. `--client` flag enables multi-tenant workspace isolation. Pipeline overhaul eliminates zombie processes. Self-liveness watchdog auto-restarts unresponsive daemons.

## Safety First: DangerousOpsConfig

The most important change in v2.2 isn't a feature — it's a guardrail.

**DangerousOpsConfig** is a new pattern-based command blocking engine. Before any agent runs a shell command, Tetora checks it against a configurable blocklist. If matched, the command is rejected before execution — no side effects, no data loss.

Default-blocked patterns:
- `rm -rf` (and variants)
- `DROP TABLE`, `DROP DATABASE`
- `git push --force`
- `find ~/` (broad `$HOME` scanning)

Configure your own allowlist in `config.json`:

```json
{
  "dangerousOps": {
    "enabled": true,
    "extraPatterns": ["truncate", "kubectl delete"],
    "allowlist": ["rm -rf ./dist"]
  }
}
```

Combined with the companion fix that blocks `$HOME` from agent `AddDirs`, agents can no longer accidentally access your entire home directory even when instructed to do so. Defense in depth, not just at the prompt level.

## Reliability: Pipeline Overhaul

v2.2 rewrites the pipeline execution layer for production resilience:

- **Async `scanReviews` with semaphore** — concurrent review scanning is capped at 3 parallel processes, preventing CPU spikes during heavy review workloads
- **Pipeline health check monitor** — a background monitor runs every 30 minutes, auto-resetting tasks stuck in `doing` state (zombie detection via `ResetStuckDoing`)
- **Process group kill on timeout** — when a pipeline step times out, the entire process group is terminated, not just the parent. No orphaned child processes
- **Escalated review auto-approve** — reviews escalated for 4+ hours are automatically approved to prevent indefinite blockage

The workspace Git layer received the same treatment: `index.lock` retry with exponential backoff, serialization via `wsGitMu`, and the stale lock threshold reduced from 1 hour to 30 seconds.

## Self-Liveness Watchdog

Production deployments now get automatic crash recovery. The new self-liveness watchdog monitors the Tetora daemon heartbeat; if the process becomes unresponsive, it triggers a supervisor-managed restart.

No more manually SSHing to restart a daemon that silently hung at 3 AM.

## Multi-Tenant Dispatch: `--client` Flag

Multi-tenant support is now built in. The new `--client` flag isolates dispatch output per client:

```bash
tetora dispatch --client acme "Run the weekly report workflow"
tetora dispatch --client initech "Code review for PR #42"
```

Each client gets its own output path, preventing task outputs from different clients from interleaving. Pairs with the Team Builder CLI for managing multi-client agent configurations from a single Tetora instance.

## Worktree Failure Preservation

Previously, if a task failed mid-execution, worktree cleanup discarded any uncommitted changes. v2.2 changes this behavior: failed or cancelled tasks that have commits or local changes are preserved as `partial-done` instead of being discarded.

This means:
1. Work-in-progress is never silently lost
2. You can inspect exactly how far an agent got before failing
3. Manual recovery is straightforward — the branch is intact

## History CLI: Failure Analysis

Three new `tetora history` subcommands for diagnosing failed agent runs:

```bash
tetora history fails              # List recently failed tasks with error summaries
tetora history streak             # Show current success/failure streak per agent
tetora history trace <task-id>    # Full execution trace for a specific task
```

When an agent fails repeatedly, `history fails` and `trace` give you the data to diagnose root cause without digging through raw logs.

## Cancel Buttons (v2.2.1)

A quality-of-life improvement that ships in v2.2.1: cancel running tasks directly from the Dashboard.

- **Task Detail modal** — yellow "Cancel" button visible while task status is `doing`
- **Workflow progress panel** — "Cancel Run" button next to "View Full Run"

Both buttons auto-hide once the task completes or the status changes away from `doing`.

## Provider Preset UI

The Dashboard Settings now includes a Provider Preset UI with:

- **Custom `baseUrl`** input for self-hosted or proxied endpoints
- **Anthropic native provider type** — uses `x-api-key` auth with the correct header format
- **Connection test endpoint** — verify your provider config before dispatching tasks

## Memory Temporal Decay

Agent memory entries now have time-based relevance decay. Facts learned months ago gradually decrease in priority, preventing stale information from overriding recent context in long-running Tetora deployments.

The decay rate is tunable per project — useful for teams where context changes rapidly and old assumptions should fade quickly.

## Site: Astro Migration

The Tetora site has been migrated from legacy HTML to [Astro](https://astro.build/), with notable performance and authoring improvements:

- **pnpm** for faster, reproducible installs
- **WebP logo** replacing a 909KB PNG (3KB — a 99.7% reduction)
- **GA4 deferred loading** to reduce Total Blocking Time
- **Dynamic sidebar** for documentation navigation
- **i18n docs** — 54 translation files across 9 languages for 6 core docs

## Security Fixes

v2.2 closes two security issues found during internal audit:

- **SSRF fix** — `/api/provider-test` endpoint was hardened against server-side request forgery. User-supplied URLs are now validated before any outbound request is made
- **XSS fix** — Provider preset UI inputs are sanitized to prevent cross-site scripting in dashboard views

## Upgrade to v2.2.2

```bash
tetora upgrade
```

Single binary. Zero dependencies. macOS / Linux / Windows.

[View full changelog on GitHub](https://github.com/TakumaLee/Tetora/releases/tag/v2.2.2)
