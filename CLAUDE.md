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

### Memory Write Discipline

Agent 寫入記憶時必須遵守以下紀律，防止記憶幻覺（HaluMem）：

**CRUD 驗證（先讀再寫）**
寫入 `workspace/memory/` 之前，必須先讀取目標 key 的現有內容，然後分類處理：
- **ADD** — 目標 key 不存在或內容無關 → 直接寫入
- **UPDATE** — 新內容是對既有內容的更新 → 合併或替換，保留重要歷史
- **NOOP** — 既有內容已覆蓋新知識 → 跳過，不寫重複資訊
- **CONFLICT** — 新內容與既有內容矛盾 → 兩版都保留，新版加 `⚠️ CONFLICT` 標記，等人工裁決

**衝突處理**
- 發現矛盾時，**絕不靜默覆蓋**
- 保留兩個版本，標記衝突來源和日期
- Agent 讀取到 CONFLICT 標記的記憶時，應同時呈現兩個版本

**4 種記憶幻覺風險**
Agent 應自我檢查，避免以下寫入錯誤：
- **編造（Fabrication）** — 寫入從未發生過的事情
- **錯誤（Inaccuracy）** — 寫入不準確的資訊（如錯誤歸因）
- **衝突（Contradiction）** — 同一事實的矛盾版本並存而未標記
- **遺漏（Omission）** — 重要資訊發生在對話中但未寫入記憶

> 來源：HaluMem 研究 + 「为什么 Agent 需要记忆系统」文章分析（2026-02-27 納入）

### Shared Knowledge Architecture
- `workspace/rules/` — governance, auto-injected into ALL roles
- `workspace/memory/` — shared observations, any role can read/write
- `workspace/knowledge/` — reference material (50KB guard, auto-injected)
- `workspace/skills/` — reusable procedures, loaded by prompt matching
- `agents/{name}/SOUL.md` — per-role personality (NOT shared)
