---
title: "Running Multiple Agents Across Discord Threads"
lang: en
date: "2026-03-23"
excerpt: "Use Discord threads with /focus to run separate agents in parallel, each with independent context."
description: "Learn how to use Discord threads and the /focus command to run multiple Tetora agents concurrently with isolated sessions."
---

Tetora's Discord integration lets you run multiple agents **simultaneously** by binding each thread to a different agent. Each thread gets its own independent session — no context bleed between conversations.

## How It Works

Your main Discord channel has a single shared session. To run parallel tasks with different agents, create threads and use `/focus` to assign an agent to each one.

```
#general (main channel)                ← shared session
  └─ Thread: "Refactor auth module"    ← /focus kokuyou → independent session
  └─ Thread: "Write blog post"         ← /focus kohaku  → independent session
  └─ Thread: "Competitor analysis"     ← /focus hisui   → independent session
```

## Step by Step

**1. Create a Discord thread** — Right-click a message → Create Thread, or use Discord's thread button.

**2. Bind an agent inside the thread:**

```
/focus kokuyou
```

Once bound, all messages in that thread route to the assigned agent with its own conversation history.

**3. Repeat for other tasks** — Open as many threads as you need, each with a different (or same) agent.

**4. When done, unbind:**

```
/unfocus
```

## Configuration

Enable thread bindings in your `config.json`:

```json
{
  "discord": {
    "threadBindings": {
      "enabled": true,
      "ttlHours": 24
    }
  }
}
```

## Things to Know

- **Thread bindings expire** after 24 hours by default (configurable via `ttlHours`). After expiry, the thread falls back to the main channel's routing.
- **Sessions are fully isolated** — a thread's context never leaks into the main channel or other threads.
- **Concurrency limit** — All channels and threads share the global `maxConcurrent` limit (default 8). Messages exceeding the limit are queued.
- `/focus` only works inside threads. The main channel always uses a single session — use `!new` to reset it.
