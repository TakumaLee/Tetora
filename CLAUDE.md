# Tetora v2 — Project Rules

> See `~/.claude/CLAUDE.md` for universal workflow rules (plan mode, subagents, verification, etc.)

## Project Context

- Source code: project root, Runtime data: `~/.tetora/` (bin, config, db, logs, sessions)
- Go 1.25, zero external dependencies (stdlib only)
- DB via `sqlite3` CLI (`queryDB()` / `escapeSQLite()`), not cgo
- Structured logging: `logInfo`/`logWarn`/`logError`/`logDebug` + `Ctx` variants
- Config: raw JSON preserve + selective update, `$ENV_VAR` resolution

## Knowledge Management Strategy

Tetora's agents follow the same self-improvement loop as our development workflow:

### Lesson → Rule → Skill Pipeline
- **Lesson**: An observation from a single correction or session (stored in `workspace/memory/`)
- **Rule**: A validated pattern (3+ occurrences) promoted to `workspace/rules/` — auto-injected into all agent prompts
- **Skill**: A reusable workflow/procedure in `workspace/skills/` — loaded on demand by `autoInjectLearnedSkills()`

### 3x Repeat Threshold
- If Tetora agents explain, re-do, or get corrected on the same thing 3+ times → it should become a Rule or Skill
- `reflection.go` captures post-task self-assessment — patterns found here feed into Skill candidates
- `skill_learn.go` handles historical prompt matching for auto-injection

### Review Cadence (Tetora's cron system)
- Cron jobs (`cron.go`) can schedule periodic review tasks
- Review flow: scan recent session history → identify repeated patterns → suggest new Skills
- Stale Skills/Rules should be updated or removed, not left to rot

### External Knowledge Intake (Article Analysis)

When external articles/references are shared for Tetora improvement:

1. **Extract** — actionable insights only, not summaries
2. **Audit** — compare against existing Tetora capabilities (check code, not just docs)
3. **Route**:
   - Agent behavior strategy → update this file's Knowledge Management Strategy
   - Feature idea (new code) → `tasks/todo.md` as implementation task
   - Workflow improvement (how we develop Tetora) → `~/.claude/CLAUDE.md` (synced with §4)
4. **Apply** — rules update directly, features go through plan mode

Same principles as `~/.claude/CLAUDE.md` §4: prefer strengthening existing over adding new. Flag conflicts for user decision.

### Shared Knowledge Architecture
- `workspace/rules/` — governance, auto-injected into ALL roles
- `workspace/memory/` — shared observations, any role can read/write
- `workspace/knowledge/` — reference material (50KB guard, auto-injected)
- `workspace/skills/` — reusable procedures, loaded by prompt matching
- `agents/{name}/SOUL.md` — per-role personality (NOT shared)
