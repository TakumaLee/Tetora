# Tetora v2 — Project Rules

> See `~/.claude/CLAUDE.md` for universal workflow rules (plan mode, subagents, verification, etc.)

## Project Context

- Source code: project root, Runtime data: `~/.tetora/` (bin, config, db, logs, sessions)
- Go 1.25, zero external dependencies (stdlib only)
- DB via `sqlite3` CLI (`queryDB()` / `escapeSQLite()`), not cgo
- Structured logging: `logInfo`/`logWarn`/`logError`/`logDebug` + `Ctx` variants
- Config: raw JSON preserve + selective update, `$ENV_VAR` resolution
