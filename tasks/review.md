# Tetora — Living Project Review

> Auto-refreshed on `git pull` (post-merge hook calls `scripts/update-review.sh`).
> Static analysis sections are updated manually after significant changes.
> Last updated: 2026-03-17

---

## Project Overview

Tetora is a Go-based AI agent orchestration platform (400+ `.go` files, zero external deps).
Core function: dispatch tasks to Claude Code CLI agents from Discord/Slack/web, with full
workflow, cron, skill, memory, and knowledge management.

Runtime data lives in `~/.tetora/`; source in project root. DB access via `sqlite3` CLI (no cgo).

---

## Recent Commits

<!-- AUTO-UPDATED by scripts/update-review.sh -->
```
e268f29 feat: implement RDD Infrastructure Phase 1 (STATE.md & GSD Engine)
dfacf94 release: v2.0.2 — dispatch reliability, auto-assign, docs viewer
34d1237 fix: extract syscall.Kill to platform files for Windows cross-compile
c957e0e release: v2.0.1
c2365ce feat: token-based session compaction, docs viewer, DAG theme fix, bump safety, GitLab MR support
b4a5aa8 docs: comprehensive documentation update for v2.1 release
8cd6003 feat: workflow enhancements + retention policies + admin API improvements
27f9779 feat: dashboard partial-done status support + SSE improvements
7437cef feat: multi-ticket concurrent dispatch with slot pressure system
e286b6c fix: service worker only caches app shell, stops intercepting API requests
```
<!-- END AUTO-UPDATED -->

---

## Current Unstaged Changes

<!-- AUTO-UPDATED by scripts/update-review.sh -->
```
 classify.go        | 12 ++++++++++--
 prompt_tier.go     |  1 +
 provider_claude.go | 19 +++++--------------
 rdd_engine.go      |  1 +
 4 files changed, 17 insertions(+), 16 deletions(-)
```
<!-- END AUTO-UPDATED -->

---

## Architecture Notes

### Complexity Classification (`classify.go`)
Three tiers: `Simple` / `Standard` / `Complex`. Determined by keyword matching (EN word-boundary,
JA/ZH substring) + message source (cron → standard, discord DM → standard baseline).
Complex tasks get full workspace injection + RDD engine.

### Prompt Tier Pipeline (`prompt_tier.go`)
12-step system prompt builder:
1. Base role + personality (SOUL.md)
2. Knowledge injection
3. Rules injection
4. Skills injection
5. Citation rules
6. Workspace content (skip for Simple)
7. **RDD engine** (Complex + workdir only) — injects STATE.md + GSD workflow
8. AddDirs filtering (Simple strips to baseDir only)
9. Budget cap enforcement

### RDD Engine (`rdd_engine.go`)
Requirement-Driven Development: agents carry a `STATE.md` with Objective / Constraints /
Decisions / Current Status / Next Steps. `/rdd resume` reconstructs session context from it.
Phase 1 shipped in e268f29. Still debugging activation (workdir often empty for channel tasks).

### Provider: Claude Code CLI (`provider_claude.go`)
- Prompt delivered via stdin (not positional arg) to avoid ARG_MAX limits
- System prompt prepended inline: `SYSTEM PROMPT:\n...\nUSER PROMPT:\n...`
- Session modes: `--resume ID` (resume specific session) only; new sessions get no ID injected
- `--add-dir` removed — workspace dirs no longer forwarded to Claude CLI

### Dispatch (`dispatch.go`)
Central task router. Handles `/rdd resume` interceptor. Multi-ticket concurrent dispatch with
slot pressure. Auto-assign based on agent availability.

---

## Open Risks / Observations

| # | File | Risk | Severity |
|---|------|------|----------|
| 1 | `provider_claude.go` | Session persistence removed — new channel sessions won't persist ID for future `--resume` | High |
| 2 | `provider_claude.go` | System prompt injected as stdin text, not true system prompt — affects Claude's weighting | Medium |
| 3 | `provider_claude.go` | `--add-dir` removed but `AddDirs` still populated in Task — dirs silently dropped | Medium |
| 4 | `prompt_tier.go` | RDD gate requires `task.Workdir != ""` — channel tasks without workdir never trigger RDD | Medium |
| 5 | `classify.go` | Chinese ZH keywords: `"遷移"` appears twice in the list | Low |

---

## Key Files Reference

| File | Purpose |
|------|---------|
| `classify.go` | Request complexity (Simple/Standard/Complex) |
| `prompt_tier.go` | 12-step system prompt builder |
| `rdd_engine.go` | STATE.md management, `/rdd resume` logic |
| `dispatch.go` | Central task router + command interceptors |
| `provider_claude.go` | Claude Code CLI execution wrapper |
| `config.go` | Config parsing + RDDEngine struct |
| `taskboard_dispatch.go` | Multi-ticket concurrent dispatch |
| `workflow_exec.go` | Workflow step execution engine |
| `session.go` | Session lifecycle management |
