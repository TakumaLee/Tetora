---
title: "Building Multi-Step Workflows — Parallel Agents, Shared Context"
lang: en
date: "2026-04-21"
excerpt: "Most agent workflows aren't linear. Learn how to run steps in parallel, pass outputs between agents, and design workflows that finish faster with less wasted time."
description: "A practical guide to building multi-step agent workflows in Tetora: map your DAG, launch parallel steps simultaneously, and pass context between agents automatically."
---

## The Problem: Linear Thinking in a Parallel World

Most agent workflows aren't strictly sequential. A typical research-and-publish pipeline has steps that can run simultaneously: while one agent gathers market data, another can draft an outline. Only the final assembly step needs both outputs.

If you dispatch everything linearly — step 1, wait, step 2, wait, step 3 — you leave performance on the table. But parallelize blindly and steps that depend on upstream output will fail or produce incomplete results.

The key: know **which steps are independent** and which require **shared context from upstream tasks**.

## Map Your Workflow Before You Dispatch

Sketch the dependency graph first. Steps at the same level can run in parallel; steps below must wait.

```
intel-gather (hisui)    outline-draft (kohaku)
        \                      /
         \                    /
          → final-article (kohaku) → post-to-discord (spinel)
```

Two branches at the top run in parallel. The final article waits for both. The post step waits for the article.

## Dispatch Parallel Steps in One Block

```bash
# Launch two independent steps simultaneously
INTEL_ID=$(tetora dispatch --task "gather market intel" --agent hisui --json | jq -r '.id')
OUTLINE_ID=$(tetora dispatch --task "draft article outline" --agent kohaku --json | jq -r '.id')

# Final step waits for both upstream tasks to complete
tetora dispatch --task "write full article" --agent kohaku \
  --depends-on "$INTEL_ID,$OUTLINE_ID" \
  --on-failure abort
```

Tetora fires `hisui` and `kohaku` at the same time. The article step stays in `waiting` state until **both** complete. Two 5-minute steps finish in 5 minutes total — not 10.

## Passing Context Between Steps

Each completed task writes its output to a shared task store. Reference upstream task IDs in `inherit_outputs` to feed their results into the next agent's prompt automatically:

```json
{
  "task": "write full article",
  "agent": "kohaku",
  "dependsOn": ["intel-task-id", "outline-task-id"],
  "context": {
    "inherit_outputs": ["intel-task-id", "outline-task-id"]
  }
}
```

No manual copy-paste. Tetora injects the upstream results as context before the agent starts.

## Quick Tips

- Keep parallel branches independent — if two steps write to the same file, they will conflict
- Use `tetora workflow visualize` to render your DAG before running a complex pipeline
- Name tasks descriptively; they appear in `tetora task status` and make debugging fast
- Combine with `--on-failure continue` on notification steps so your team always gets an update, even when upstream steps fail

Well-designed multi-step workflows are like clean code: readable at a glance, each part with one clear job.
