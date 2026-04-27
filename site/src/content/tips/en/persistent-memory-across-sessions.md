---
title: "Persistent Memory Across Sessions — Keep Agents Informed Between Runs"
lang: en
date: "2026-04-25"
excerpt: "Every new session shouldn't feel like amnesia. Learn how Tetora's two-layer memory system lets your agents retain facts, decisions, and context across conversations."
description: "Learn how to configure persistent memory in Tetora so agents remember project context, past decisions, and user preferences across sessions — no more re-explaining from scratch."
---

## The Problem: Agents That Forget Everything

By default, each agent session starts cold. The agent doesn't know what you discussed yesterday, what decisions were made last week, or what the current project status is. You end up spending the first few minutes of every session re-explaining context that hasn't changed.

This isn't just annoying — it's a compounding tax. The longer a project runs, the more catch-up context an agent needs.

---

## Tetora's Two-Layer Memory System

Tetora separates memory into two layers with different access patterns:

| Layer | Location | Loaded | Best For |
|---|---|---|---|
| **Auto-memory** | `~/.claude/.../memory/` | Automatically on every session | Cross-session facts, preferences, learned rules |
| **Workspace memory** | `memory/` (project root) | On-demand via explicit `Read` | Research archives, agent diaries, domain data |

This split matters. Auto-memory is small and always present — it shapes how agents think. Workspace memory is large and intentional — agents pull it when they need it.

---

## Setting Up Auto-Memory

Auto-memory files are plain Markdown loaded at session start. Add facts you want every agent to know:

```markdown
<!-- memory/auto/project-context.md -->
# Project: Tetora
Status: active development, beta users onboarded
Stack: Go backend, Astro frontend, Claude API
Decision log: using Haiku for log parsing (2026-04-11)
Owner preference: no emoji in system outputs
```

Place the file under `~/.claude/memory/` for global facts, or `<project>/.claude/memory/` for project-scoped facts.

---

## Writing to Memory Mid-Session

When an agent discovers something worth preserving — a user correction, a new constraint, a key decision — it should write it to the appropriate memory file:

```bash
# Append a new lesson to the project memory
echo "- Polymarket API rate limit: 10 req/s (confirmed 2026-04-25)" \
  >> .claude/memory/domain-facts.md
```

In practice, agents do this automatically when they detect a correction or encounter a pattern worth recording. You can also trigger it manually:

```
/remember The staging DB uses port 5433, not 5432
```

---

## Workspace Memory: Pull When Needed

For larger, structured context — past research, agent diaries, weekly reports — use workspace memory. Agents load it explicitly:

```markdown
<!-- In agent Soul file -->
## Session Start
- Read memory/domain/polymarket-notes.md if task involves Polymarket
- Read memory/agents/hisui/diary.md if planning a new research task
```

This keeps session context lean by default, deep when needed.

---

## The Payoff

Agents with well-maintained memory stop feeling like stateless tools and start behaving like long-term collaborators. The context compounds: each session builds on the last, decisions persist, and preferences stick.

**Rule of thumb:** If you'd say it to an agent twice, it belongs in memory.
