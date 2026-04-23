---
name: kokuyou-review
description: Dispatch a direct PR/MR review to kokuyou via `tetora review`, bypassing ruri triage for cost savings. Use when the user asks to "review this PR", "kokuyou review", or types `/kokuyou-review`.
---

# kokuyou-review

Run `tetora review <url>` to send a GitHub PR or GitLab MR directly to kokuyou for review, skipping ruri's triage layer.

## When to use

- User pastes a PR/MR URL and asks for a review.
- User says "review this PR", "kokuyou review", or runs `/kokuyou-review`.
- You want a fast, cheap review without coordination overhead.

## What to do

1. Extract the PR/MR URL from the user's message (or from the current branch if obvious).
2. Call the Bash tool with:
   ```bash
   tetora review <url>
   ```
3. Return the review output to the user verbatim.

### Shorthand

- `owner/repo#NUM` expands to `https://github.com/owner/repo/pull/NUM`.
- Use `--queue <name>` to review every URL in `config.review.queues.<name>`.

## Notes

- Diff is fetched via `gh pr diff` or `glab mr diff` and truncated to `config.review.maxDiffLines` (default 3000 lines).
- Default agent is `config.review.defaultAgent` (fallback `kokuyou`); default model is `haiku`.
- If the daemon returns HTTP 409 (`dispatch already running`), wait for the in-flight task and retry.

## Installation

Copy this directory to `.claude/skills/kokuyou-review/` (project-local) or `~/.claude/skills/kokuyou-review/` (user-global).
