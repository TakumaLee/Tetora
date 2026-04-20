---
title: "Tetora v2.4 — Self-Improving Agents, Dynamic Rules, and Slot-Aware Retry"
lang: en
date: "2026-04-20"
tag: release
readTime: "~6 min"
excerpt: "The v2.3–v2.4 release series closes the agent learning loop: a lesson promotion pipeline lets agents surface their own rule candidates, dynamic keyword-matched rule injection replaces the 58KB static payload, and slot-aware retry finally makes the taskboard reliable under load."
description: "Tetora v2.4 release notes: lesson promotion pipeline, agent-facing reflection query, dynamic rule injection, slot-aware retry with per-task policy, session compact summary persistence, War Room v2 dashboard, and Discord amnesia fix."
---

The v2.3 and v2.4 release series add up to one coherent upgrade: agents that accumulate experience, load only what they need, and recover from failures without manual intervention.

> **TL;DR:** A lesson promotion pipeline closes the learning loop — agents now surface repeated patterns for rule promotion. Rule injection is now keyword-matched rather than static, eliminating the 50KB cliff. Slot-aware retry enforces per-task policies with stall detection. Session compact summaries are now persisted to prevent Discord amnesia. War Room gets a full UI overhaul with /wr Discord commands.

---

## War Room v2 (v2.3.0)

The War Room dashboard was rebuilt from scratch. The new v2 grid layout shows all fronts simultaneously with staleness indicators, manual override badges, and dependency chains — without navigating between views.

### /wr Discord Commands

Every dashboard action is now mirrored as a Discord slash command:

```
/wr list                      — list all fronts and current status
/wr status <front> <status>   — toggle status from chat
/wr intel <front> <note>      — append intel to a front's sidebar
/wr export                    — copy all fronts as a markdown summary
```

If you're in a session and can't reach the web UI, `/wr` gives you full command parity.

### Auto-Updater

Fronts with `card_type: auto` now have a background cron that pulls updates on a configurable schedule. An "⚡ Run Now" button in the dashboard triggers an ad-hoc refresh without waiting for the next cycle. The dashboard header shows the last and next scheduled update timestamps.

### UI CRUD

You can now create, archive, and delete fronts directly from the web interface — no more editing `status.json` by hand. The "Depends On" field renders as a dependency chip in each modal, clickable to jump to the dependency.

---

## Session Continuity: No More Discord Amnesia (v2.4.1)

After a session compaction, agents would frequently forget context they'd had moments before — asking users to repeat information, losing task state, and behaving as if the session had just started.

The root cause was a combination of three issues:

1. **Summaries were too short.** The compaction target was 300–500 words, which aggressively dropped specifics.
2. **Delete-after-inject.** The summary was deleted from memory after the first injection. A write failure left nothing for subsequent sessions.
3. **Per-message cap too tight.** The 800-character cap on summarizer input degraded quality on long exchanges.

v2.4.1 fixes all three:

- Summary target raised to 1500–2000 words, with explicit instructions to preserve verbatim identifiers, specific numbers, and open action items.
- Summary is now persisted in memory rather than deleted after injection. The next compaction overwrites the same key — no stale accumulation.
- Per-message cap raised to 1600 characters. Summarizer timeout raised from 90s to 180s.

The compact summary block also now carries an explicit instruction header telling the agent to consult the summary before asking the user to repeat context.

---

## Slot-Aware Retry (v2.4.1, P0)

The taskboard retry loop had accumulated three bugs that made it unreliable under real workloads:

- Tasks could spin in a retry loop even when all agent slots were full, queuing work with no path to execution.
- Stall detection was missing — a task could be marked "running" indefinitely if the agent died mid-task.
- When `require_human_confirm` was set, the system appended a new comment every minute until a human responded, producing a comment storm.

v2.4.1 fixes all three and introduces a proper per-task retry policy:

```json
{
  "max": 1,
  "require_human_confirm": true
}
```

Set this on any task that shouldn't silently auto-retry:

```bash
tetora task create \
  --title "Risky migration" \
  --retry-policy '{"max":1,"require_human_confirm":true}'
```

When `max` is reached, the task stops and waits for explicit human sign-off before continuing. Slot guards ensure retries only fire when a slot is actually available. Stall detection marks tasks as failed when the assigned agent stops reporting progress.

---

## Dynamic Rule Injection (v2.4.2)

Rules were previously injected as a static block for every task — the entire `rules/` directory, regardless of relevance. On a mature workspace this was 58KB of content that ballooned system prompts and diluted the signal agents actually needed.

Worse: above 50KB, the rules block was silently skipped entirely. Adding a new rule could inadvertently remove all rules.

v2.4.2 replaces static injection with keyword-matched injection:

```yaml
# rules/INDEX.md (new file — define once)
- file: dispatch-workflow.md
  keywords: [dispatch, agent, task, assign]
  always: false

- file: git-safety.md
  keywords: [git, commit, push, branch, merge]
  always: false

- file: language-compliance.md
  keywords: []
  always: true   # injected for every task
```

The injector builds an "Active Rules" block capped at `MaxRulesPerTask` (default 3) and `RulesMax` (8000 characters). Only rules whose keywords appear in the task description or title are included. Always-on rules are injected unconditionally.

The 50KB cliff is gone. A new 200KB soft warning replaces it — the system degrades gracefully rather than silently dropping content.

Auto-lessons from agent reflections now write to `memory/auto-lessons.md` (a promotion queue) instead of `rules/` (governance). This prevents unreviewed lessons from consuming the rules budget.

---

## Lesson Promotion Pipeline (v2.4.2)

Agents extract lessons after each task — but those lessons sat in a markdown file with no path to becoming actionable rules. After 44 lessons accumulated with no review mechanism, the pattern was clearly broken.

v2.4.2 closes the loop with a CLI-driven, human-gated promotion pipeline:

```bash
# See which lessons recur across enough distinct tasks to warrant promotion
tetora lessons scan --threshold 3

# Materialise candidates to rules/auto-promoted-YYYYMMDD.md (dry-run by default)
tetora lessons promote --dry-run

# Audit rules older than 90 days
tetora lessons audit --age 90
```

The pipeline tracks every `ExtractAutoLesson` trigger in a new `lesson_events` DB table, independent of the markdown key — so occurrence counts are accurate even if the file is edited.

### Agent-Facing Reflection Query

Agents can now query their own history without shelling out to the CLI:

```
reflection_search     — find reflections by keyword, agent, task, score
reflection_get        — fetch a single reflection by task ID
lesson_history        — trace every occurrence of a repeated lesson key
lesson_candidates     — peek at the promotion queue
```

Before this, repeated mistakes were invisible across sessions because agents had no way to ask "has this happened before?" Now they can — and surface the answer in-context.

---

## Other Changes

- **Discord session ref** — Tetora now refreshes the Discord session reference after each `runSingleTask` call. Previously, if a session was archived mid-run, subsequent output would be sent to the archived session and silently dropped.
- **Taskboard `next_retry_at` loop** — Three edge cases in the retry scheduler caused tasks to spin without advancing or to schedule retries in the past. All three are patched in v2.4.2 (#90).
- **Docs viewer** — The War Room dashboard now includes an inline docs viewer for workspace documentation. No more leaving the dashboard to read architecture notes.

---

## Upgrade

```bash
tetora upgrade
```

Single binary. No external dependencies. macOS / Linux / Windows.

[View full changelog on GitHub](https://github.com/TakumaLee/Tetora/releases/tag/v2.4.2)
