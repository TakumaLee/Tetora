# Lessons Learned

> Patterns from corrections and mistakes. Reviewed at session start to prevent repeats.

## Go / Tetora Specific

- `queryDB()` returns `([]map[string]any, error)` — always handle both return values
- `logInfoCtx(ctx, msg, ...)` — first arg is `context.Context`, not string
- `startHTTPServer` has 18 params — always verify current signature before modifying
- Both calls to `startHTTPServer` in `main.go` must be updated together
- Binary conflicts during rebase — always `git checkout --theirs` for compiled binaries

## Workflow

- Bash-type subagent cannot write files — use `ruri-dev-team` or write manually
- Duplicate type declarations can appear after rebase — check before committing

## General

_New lessons added here after each correction._
