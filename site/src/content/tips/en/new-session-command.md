---
title: "Starting a New Session with !new"
lang: en
date: "2026-03-23"
excerpt: "Type !new in Discord to reset the agent's context and start fresh without switching terminals."
description: "Use the !new command in Discord to archive the current session and start a clean conversation."
---

Over time, a Discord channel's session accumulates context. When the agent gets confused or you're switching to a completely different topic, `!new` gives you a clean slate.

## Usage

Type in any main Discord channel where Tetora is active:

```
!new
```

That's it. The current session is archived and the next message starts a fresh session.

## What Gets Reset

- **Conversation context** — The agent starts with no memory of previous messages in this channel.
- **Session history** — The old session is archived in the history database, not deleted. You can still search it later.

## What Stays

- **Agent configuration** — SOUL.md, config settings, and role assignments remain unchanged.
- **Memory files** — The agent's persistent memory is not affected.
- **Thread bindings** — Any `/focus` bindings in threads are independent and unaffected.

## When to Use

- The agent is referencing outdated context or seems confused
- You're starting a completely new task unrelated to the previous conversation
- The session has grown long and responses are slow
- After a major change (new deployment, config update) that makes old context misleading

## !new vs /focus

| | `!new` | `/focus` |
|---|---|---|
| Where | Main channel | Threads only |
| Effect | Archives session, resets context | Binds thread to a specific agent |
| Scope | Affects the entire channel | Only affects that thread |

If you need parallel conversations rather than a reset, use threads with `/focus` instead.
