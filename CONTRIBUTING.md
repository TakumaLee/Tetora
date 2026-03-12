# Contributing to Tetora

Welcome, and thank you for your interest in contributing. Tetora is a young open-source project and every contribution matters — bug reports, fixes, features, and translations are all appreciated.

---

## Development Setup

### Prerequisites

- **Go 1.25+** — the project uses no external Go dependencies (stdlib only)
- **sqlite3** — must be available on `$PATH` (Tetora shells out to the CLI, not cgo)
- **Claude Code CLI** — optional, only needed if you're testing CLI-worker functionality

### Clone and build

```bash
git clone https://github.com/TakumaLee/Tetora.git
cd Tetora
make build
```

`make build` first compiles the dashboard (`dashboard/*.js` + `dashboard/*.css` → `dashboard.html`), then builds the binary.

### Configure

```bash
cp examples/config.example.json ~/.tetora/config.json
# Edit ~/.tetora/config.json — add your API keys and preferred agents
tetora doctor   # validates the config
```

### Run tests

```bash
make test
```

---

## Architecture Overview

A few things that are non-obvious and worth knowing before you touch the code:

**Single binary, single package.** Everything lives in `package main` at the project root. There are no internal sub-packages.

**Zero external dependencies.** The Go module has no third-party imports. Keep it that way. If you need a library, discuss it in an issue first.

**Database access via sqlite3 CLI.** Tetora does not use cgo or a Go SQLite driver. All queries go through a shell call to `sqlite3`. Every query must include:
```
PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;
```
Use the existing `queryDB()` and `escapeSQLite()` helpers — do not roll your own.

**Dashboard is compiled.** The file `dashboard.html` is a build artifact. It is assembled from the source files in `dashboard/` by `make dashboard` (which `make build` calls automatically). If you edit `dashboard.html` directly, your changes will be overwritten on the next build.

**Structured logging.** Use `logInfo`, `logWarn`, `logError`, and `logDebug` (and their `Ctx`-suffixed variants for request-scoped logging). Do not use `fmt.Println` or `log.Printf` for anything that will run in production.

**Config with `$ENV_VAR` resolution.** Config values can reference environment variables using `$VAR_NAME` syntax. The config loader preserves the raw JSON structure on writes to avoid clobbering keys it doesn't know about.

---

## Making Changes

### Workflow

1. Fork the repository
2. Create a branch from `main` using the naming convention below
3. Make your changes
4. Run `make build` and `make test`
5. Open a pull request against `main`

### Branch naming

| Prefix | Use for |
|--------|---------|
| `feat/` | New functionality |
| `fix/` | Bug fixes |
| `refactor/` | Refactoring with no behavior change |
| `chore/` | Config, dependencies, docs, CI |

Examples: `feat/matrix-provider`, `fix/cron-timezone`, `chore/update-install`

### Dashboard changes

Edit the source files in `dashboard/`, never `dashboard.html` directly.

```bash
make dashboard   # rebuild dashboard.html from sources
make build       # rebuild dashboard + binary
```

### Local testing with live reload

```bash
make bump        # increments dev version, rebuilds, and hot-reloads the running daemon
```

`make bump` is safe to run on any branch. It checks for running workflows before restarting; use `make bump-force` only if you need to override that check.

---

## Code Style

**Errors are not optional.** Every function with a side effect must return an error. Callers must handle it — no `_` discards on writes, DB calls, or I/O.

**Fail fast.** Validate inputs and check preconditions before starting expensive operations.

**No over-engineering.** Tetora favors readable, direct code over abstractions. If you find yourself adding an interface for something that has one implementation, reconsider.

**No external dependencies.** Solve problems with the standard library. Exceptions require explicit discussion and maintainer sign-off.

---

## Commit Messages

Follow the conventional commits format:

```
type(scope): short description (under 72 chars)
```

**Types:** `feat`, `fix`, `refactor`, `chore`, `docs`, `test`

**Examples:**
```
feat(workflow): add retry backoff for failed steps
fix(discord): prevent duplicate message sends on reconnect
chore(makefile): add arm64 linux to release targets
```

Avoid vague messages like "fix bug" or "update code". The title should tell a reviewer what changed and why at a glance.

---

## Reporting Issues

Please use [GitHub Issues](https://github.com/TakumaLee/Tetora/issues).

Include in your report:

- **Tetora version** — run `tetora version`
- **OS and architecture** — e.g., macOS arm64, Ubuntu x86_64
- **Go version** — run `go version`
- **Steps to reproduce** — minimal and specific
- **Relevant log output** — from `~/.tetora/logs/tetora.log`

For crashes or panics, the log file is the most useful artifact. Please include the lines around the error, not just the error line itself.

---

## Translations

Tetora's core docs are available in 10+ languages. Translation files live alongside the originals using a language suffix:

```
README.md          # English (source of truth)
README.zh-TW.md    # Traditional Chinese
README.ja.md       # Japanese
# etc.
```

If you are contributing a translation:

- Keep the same structure and headings as the English source
- Submit translations through a PR like any other change
- If the English source has been updated since a translation was written, note the gaps in your PR description so reviewers can assess accuracy

---

## Questions

If you are unsure about anything before starting work, open an issue and ask. It is better to discuss a design upfront than to submit a large PR that needs a full rewrite.
