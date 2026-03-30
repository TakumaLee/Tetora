# Tetora Getting Started Guide

This guide provides a comprehensive overview of how to set up and operate Tetora, an AI agent orchestrator designed for multi-agent collaboration and autonomous task execution.

## 1. Core Concepts

### Agents and Soul Files
Agents in Tetora are defined by "Soul Files" (Markdown). A high-quality Soul File should include:
- **Identity**: The specific role and persona of the agent.
- **Expertise**: Technical skills or domains the agent excels in.
- **Behavioral Guidelines**: Instructions on reasoning depth and communication style.
- **Output Format**: Preferred data structures (Markdown, JSON, etc.).

### Workflows
Workflows are JSON-defined pipelines that orchestrate multiple agents.
- **DAG Structure**: Steps must form a Directed Acyclic Graph.
- **Variable Injection**: Use `{{steps.ID.output}}` to pass data between steps.
- **Isolation**: Use `gitWorktree: true` for concurrent development tasks.

### Taskboard
The Taskboard is a Kanban-style system for managing autonomous execution.
- **Statuses**: `backlog` -> `todo` -> `doing` -> `review` -> `done`.
- **Auto-Dispatch**: Automatically assigns `todo` tasks to available agents based on configuration.

## 2. Best Practices

1. **Task Atomicity**: Break down complex requirements into tasks that can be completed within 30-90 minutes.
2. **Rule Promotion**: If an agent repeats an error, document the fix in `workspace/rules/`. Patterns appearing 3+ times should become permanent rules.
3. **Cost Management**: Assign lower-cost models (e.g., Claude Haiku) for routine tasks and premium models (e.g., Claude Opus) for critical architectural decisions.
4. **Environment Safety**: Never hardcode API keys. Use environment variables like `$ANTHROPIC_API_KEY`.

## 3. Initialization

Run the provided `tetora-init.sh` script to scaffold your environment:
```bash
bash tetora-init.sh
```
This script will create the necessary directory structure (`agents/`, `workspace/`, `workflows/`) and guide you through creating your first agent.

## 4. Troubleshooting

- Run `tetora doctor` to verify system dependencies (e.g., `sqlite3`, API keys).
- Check `history.db` for execution logs and agent memory persistence.
