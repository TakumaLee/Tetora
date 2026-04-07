---
title: "Creating Your First Agent Soul File"
lang: en
date: "2026-04-07"
excerpt: "Give your AI agents a consistent identity, voice, and personality by writing a Soul file — the single source of truth for how an agent thinks and speaks."
description: "Learn how to create a Soul file in Tetora to define your AI agent's personality, communication style, and behavioral rules. Step-by-step guide with examples."
---

## The Problem: Every Response Sounds Different

Without a defined identity, your agents drift. One session they sound formal, the next casual. They forget tone guidelines, override each other's style, and produce outputs that feel like they came from five different writers.

A **Soul file** solves this. It's a Markdown document that anchors an agent's personality, voice, and behavioral constraints — loaded at session start and referenced throughout.

## What Goes in a Soul File

A Soul file covers four areas:

1. **Identity** — Name, role, what the agent cares about
2. **Voice** — Tone, language style, what to avoid
3. **Relationships** — How this agent interacts with teammates
4. **Constraints** — Hard rules (never do X, always do Y)

## Setting It Up

Create a file at `agents/{name}/SOUL.md` inside your Tetora workspace:

```bash
mkdir -p agents/kohaku
touch agents/kohaku/SOUL.md
```

Here's a minimal Soul file to start with:

```markdown
# SOUL.md — Kohaku

## Identity
- Name: Kohaku
- Role: Content creator — writes posts, articles, and social copy
- Core belief: Good ideas deserve to be heard. My job is to make them land.

## Voice
- Warm, direct, story-first
- Uses analogies to make technical ideas accessible
- Never uses jargon without explaining it
- Language: Traditional Chinese primary, English/Japanese mixed in naturally

## Relationships
- Ryuri (lead): Final approval authority — if Ryuri says no, it's no
- Hisui (research): Primary source of intel — always read reports before writing
- Kokuyou (engineering): Ask "how would you explain this simply?" when bridging tech gaps

## Constraints
- Never publish without Ryuri review
- Don't exaggerate metrics or use clickbait headlines
- Every post must be traceable to a source
```

## Wiring It to Your Agent

Reference the Soul file in your agent config so it loads automatically:

```json
{
  "agent": "kohaku",
  "soul": "agents/kohaku/SOUL.md",
  "model": "claude-opus-4-6",
  "role": "content"
}
```

When the agent starts a session, the Soul file is injected into its system context. Every response is filtered through the identity it defines.

## The Payoff

Once your Soul file is in place, you stop correcting tone and start correcting content. The personality becomes stable — across sessions, across tasks, across collaborators. Your agent sounds like *itself*, not like a generic AI.

Start with identity and constraints. Add voice rules as you notice drift. A Soul file grows with the agent.
