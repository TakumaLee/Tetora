---
title: "Building Multi-Step Workflows — Chain Agents Into a Reliable Pipeline"
lang: en
date: "2026-04-14"
excerpt: "Go beyond single-agent tasks. Learn how to design multi-step workflows where each agent hands off to the next, turning complex processes into repeatable pipelines."
description: "Learn how to build multi-step agent workflows in Tetora. Chain agents, pass context between steps, and design reliable pipelines for complex recurring tasks."
---

Running a single agent to answer a question is useful. Running a sequence of agents that research, draft, review, and publish — automatically — is transformative. Tetora's workflow system lets you do exactly that.

## The Problem With One-Shot Prompts

When a task has multiple stages, one agent handling everything tends to go wrong in predictable ways: context overflows, early decisions constrain later ones, and there is no clean recovery if one step fails.

The better approach: break the work into stages, assign each stage to a focused agent, and let each step feed the next.

## Defining a Multi-Step Workflow

A Tetora workflow is a sequence of tasks where each step has a defined role, input, and output. Here is a minimal example:

```json
{
  "workflow": "weekly-report",
  "steps": [
    {
      "id": "research",
      "agent": "hisui",
      "prompt": "Gather the top 5 AI news items from the past 7 days. Output a JSON array with title, url, and one-line summary for each.",
      "output_key": "news_items"
    },
    {
      "id": "draft",
      "agent": "kohaku",
      "prompt": "Write a 300-word digest using the news items in {{news_items}}. Format as Markdown.",
      "depends_on": ["research"],
      "output_key": "draft_md"
    },
    {
      "id": "review",
      "agent": "ruri",
      "prompt": "Review the draft in {{draft_md}} for accuracy and tone. Return the final version.",
      "depends_on": ["draft"]
    }
  ]
}
```

The `depends_on` field tells Tetora not to start a step until its upstream steps have completed successfully. If `research` fails, `draft` never runs — no wasted tokens, no broken output.

## Passing Context Between Steps

Each step's `output_key` value becomes a template variable available to downstream steps. Use `{{key_name}}` in any later prompt to inject the previous output inline.

This keeps each agent's prompt short and focused — the agent reads only what it needs, not everything that happened before it.

## Running the Workflow

```bash
tetora run workflow weekly-report
```

Tetora executes steps in dependency order. Independent steps with no shared dependencies run in parallel automatically. The final output of each step is logged to the task record for review.

## Tip: Design for Failure Recovery

Name your steps descriptively and keep each one narrow. If `draft` fails, you can re-run it alone without re-running `research`:

```bash
tetora run workflow weekly-report --from draft
```

Short steps fail fast and recover cheap.

## Wrapping Up

Multi-step workflows are how solo operators scale. Define the stages, wire the dependencies, and let Tetora handle the orchestration. Once a workflow runs reliably once, it runs reliably every time.
