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

_New lessons added here after each correction._
