title: "Persistent Memory Across Sessions — Agents That Remember"
lang: en
date: "2026-04-30"
excerpt: "By default, agents forget everything when a session ends. Learn how Tetora's persistent memory layer keeps your agents context-aware across restarts, reboots, and long gaps."
description: "Learn how to configure persistent memory in Tetora so your AI agents retain knowledge across sessions. Covers memory file layout, write discipline, and best practices for keeping agent context fresh without bloat."
---

## The Problem: Agents That Forget

Every time a Claude session ends, its working memory resets. For a one-off task that's fine. But for agents that operate daily — tracking finances, managing tasks, writing content — a blank slate each morning is a bug, not a feature.

You want your agent to remember: what it decided yesterday, what state the project is in, and what you told it last week.

---

## How Persistent Memory Works in Tetora

Tetora separates memory into two layers:

| Layer | Path | Purpose |
|---|---|---|
| **Auto-memory** | `~/.claude/projects/{project}/memory/` | Cross-session facts, loaded automatically |
| **Workspace memory** | `memory/` inside the repo | Research, diaries, domain knowledge — loaded on demand |

Auto-memory files are written by agents and read back at the start of every session. Workspace memory is richer but requires explicit `Read` calls.

---

## Writing a Memory File

Create a `.md` file with frontmatter under the auto-memory directory:

```markdown
---
name: project-status
description: Current phase and next milestones for Project Kronos
type: project
---

Phase 2 is underway. API integration complete as of 2026-04-28.
Next: UI polish sprint, targeting 2026-05-10 release.

**Why:** Keeps agents in sync without reading the full git log.
**How to apply:** Reference before any planning or dispatch task.
```

Then add a pointer to `MEMORY.md` (the index file):

```markdown
- [project-status.md](project-status.md) — Kronos phase + next milestones
```

---

## Memory Types

Tetora uses four memory types to keep the index focused:

- `user` — who the user is, their expertise, preferences
- `feedback` — corrections and confirmed approaches from past sessions
- `project` — active work, deadlines, decisions (include absolute dates)
- `reference` — pointers to external systems (Linear, Grafana, etc.)

Avoid storing code patterns, file structure, or git history — those are always fresher in the source.

---

## Keep It Lean

Memory bloat is real. Follow these rules:

```text
✅ Save: non-obvious facts, preferences, decisions with a "Why"
✅ Save: absolute dates (not "next Thursday")
❌ Skip: things derivable from the code
❌ Skip: ephemeral task state (use todos instead)
❌ Skip: external content verbatim (summarize only)
```

The index file (`MEMORY.md`) truncates past 200 lines — keep each entry under ~150 characters.

---

## The Result

With persistent memory in place, your agents wake up knowing the project status, your preferences, and any decisions made in past sessions — without you re-briefing them every time.

**One file per topic. One line in the index. That's the whole system.**