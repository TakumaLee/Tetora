---
title: "Auto-Dispatch Basics — Queue Tasks for Your Agents"
lang: en
date: "2026-04-02"
excerpt: "Learn how to queue tasks for Tetora agents using the dispatch system, so your agents pick up work automatically without manual handoff."
description: "A beginner's guide to auto-dispatch in Tetora: queue tasks, assign agents, and build your first automated workflow with simple JSON config."
---

## The Problem

You have three things you want your agents to do: research a topic, write a draft, and post it to Discord. Without dispatch, you're copying output from one terminal to another and manually kicking off each step. That's not agentic — that's babysitting.

Tetora's dispatch system lets you queue work for specific agents and walk away. Each agent picks up its task, completes it, and signals the next step.

## How Dispatch Works

At its core, dispatch is a task queue. You write a task spec (who does what, with what context), push it to the queue, and the assigned agent claims it.

Every task has three required fields:

```json
{
  "agent": "kokuyou",
  "task": "Write a Twitter thread about MCP security",
  "context": {
    "source": "intel/MARKETING-WEEKLY.md",
    "tone": "technical but accessible"
  }
}
```

Drop that into your queue directory and the agent daemon picks it up automatically on its next poll cycle (default: 30 seconds).

## Your First Dispatch

**Step 1 — Define the task file:**

```bash
cat > tasks/queue/draft-tips-article.json << 'EOF'
{
  "agent": "kohaku",
  "task": "Write a tips article about auto-dispatch for the Tetora site",
  "context": {
    "output_path": "site/src/content/tips/en/",
    "word_count": "300-500",
    "include_code_example": true
  }
}
EOF
```

**Step 2 — Push it:**

```bash
tetora dispatch push tasks/queue/draft-tips-article.json
```

**Step 3 — Watch it run:**

```bash
tetora dispatch status
# kohaku  draft-tips-article  IN_PROGRESS  started 12s ago
```

That's it. No terminal handoff, no copy-paste.

## Chaining Tasks

The real power kicks in when one agent's output feeds the next. Use `depends_on` to chain:

```json
{
  "agent": "spinel",
  "task": "Post the drafted article link to Discord #content channel",
  "depends_on": "draft-tips-article"
}
```

Spinel won't start until `draft-tips-article` completes. If the upstream task fails, the dependent task is automatically cancelled — no half-baked posts.

## Key Takeaway

Auto-dispatch removes the human-as-relay bottleneck. Instead of orchestrating agents manually, you define the workflow once, push tasks to the queue, and the system executes in order. Start with a single-agent queue, add dependencies as your workflow grows.

Next step: try **Scheduling Recurring Tasks with Cron** to dispatch automatically on a time trigger.
