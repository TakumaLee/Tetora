# Lessons Learned — Tetora Specific

> Go and Tetora-specific patterns. For universal lessons see `~/.claude/lessons.md`.

## Go / Tetora Specific

- `queryDB()` returns `([]map[string]any, error)` — always handle both return values
- `logInfoCtx(ctx, msg, ...)` — first arg is `context.Context`, not string
- `startHTTPServer` has 18 params — always verify current signature before modifying
- Both calls to `startHTTPServer` in `main.go` must be updated together
- Binary conflicts during rebase — always `git checkout --theirs` for compiled binaries

## Context Management (CRITICAL)
- At **75% context usage**, MUST stop and save progress:
  1. Write detailed status to `tasks/todo.md` (checkboxes for each subtask)
  2. Update `memory/MEMORY.md` Quick Resume section
  3. Run `go build ./...` and record build status
  4. Tell user to start new session
- Task system (Claude Code internal tasks) alone is NOT enough — they're session-scoped
- Always write progress to persistent files on disk

## Knowledge Architecture

- Tetora's knowledge pipeline (Lesson → Rule → Skill) and Claude Code's pipeline (Lesson → Rule → Memory) are parallel but separate
- `tetora/CLAUDE.md` describes product behavior strategy; `~/.claude/CLAUDE.md` describes development workflow
- When analyzing external articles: route insights through domain routing, don't dump everything into one place

## Bugs to Fix (identified 2026-02-25)

### Fixed
- **smartDispatch config mismatch**: `defaultRole`/`coordinator` used display name "琉璃" instead of role key "ruri". Fixed in config.json.
- **Poor role description**: ruri's description was "Imported from OpenClaw (ruri)" — not useful for LLM routing. Updated to descriptive text.

### Also Fixed (code changes)
- **LLM classifier returns functional names**: Fixed `classifyByLLM()` in `route.go` — added explicit valid keys list and IMPORTANT instruction to return exact role keys only.
- **Dead `workspaces/` directories**: Fixed `cli_init.go` and `migrate_openclaw.go` — changed from `workspaces/{role}/` to `agents/{role}/` (matching v1.3.0 architecture).
- **Test/implementation mismatch**: Rewrote `workspace_test.go` — all tests now use role keys ("ruri" not "琉璃") and expect shared workspace paths.
- **`getWorkspaceMemoryPath`/`getWorkspaceSkillsPath`**: Removed unused `roleName` parameter, functions now correctly return shared workspace paths.

_New lessons added here after each correction._
