---
title: "Model Selection Per Role — Assign Sonnet to Drafts, Opus to Strategy"
lang: en
date: "2026-04-11"
excerpt: "Not every task needs your smartest model. Learn how to assign Sonnet for fast drafts and Opus for high-stakes decisions — and cut costs without sacrificing quality."
description: "Learn how to configure model selection per agent role in Tetora. Assign Claude Sonnet for fast content drafts and Claude Opus for strategic reasoning — balance speed, quality, and cost across your multi-agent workflow."
---

## The Problem: One Model Fits None

Running every agent on Opus feels safe. But it's expensive, and for tasks like first-pass drafts or log parsing, Opus is overkill — you're paying reasoning-tier prices for formatting-tier work.

Conversely, routing strategy calls through Sonnet can produce shallow output when you needed depth.

The fix: **assign models by role, not by default.**

---

## How Model Roles Work in Tetora

Each agent in Tetora has a `role` field in its Soul file. You can map roles to models in `tetora.config.json`:

```json
{
  "models": {
    "draft": "claude-sonnet-4-6",
    "review": "claude-opus-4-6",
    "strategy": "claude-opus-4-6",
    "parse": "claude-haiku-4-5-20251001"
  },
  "agents": {
    "kohaku": { "role": "draft" },
    "ryuri":  { "role": "review" },
    "hisui":  { "role": "strategy" },
    "jade":   { "role": "parse" }
  }
}
```

When a task is dispatched to an agent, Tetora automatically uses the model mapped to that agent's role — no per-call configuration required.

---

## A Practical Mapping

| Agent Role | Suggested Model | Why |
|---|---|---|
| Initial draft | Sonnet | Fast, cost-efficient for generative work |
| Code review | Opus | Needs deep reasoning over diffs |
| Strategy / planning | Opus | Multi-step inference, fewer blind spots |
| Log parsing / tagging | Haiku | Structured extraction; speed matters most |
| Final editorial review | Opus | Catches what Sonnet misses |

---

## Per-Task Override

Sometimes you need to override the default. Use the `--model` flag when dispatching:

```bash
tetora dispatch --role kohaku --model claude-opus-4-6 "Rewrite the landing page copy from scratch"
```

This is useful for one-off high-stakes tasks without changing the global config.

---

## The Result

On a typical content day — 5 drafts, 2 reviews, 20 log parses — this routing pattern cuts token spend by roughly 40–60% compared to running everything on Opus, with no measurable quality drop in the final output.

**Rule of thumb:** Opus for decisions, Sonnet for execution, Haiku for extraction.
