## [Unreleased]

---

## [v2.2.5] - 2026-04-17

### Added
- **Fresh-session compaction**: New `fresh-session` strategy — summarises session via LLM, writes summary to workspace memory, archives the old session, and injects the summary into the next session's system prompt. Eliminates JSONL cache bloat while preserving full context continuity
- **Notify-only compaction mode**: `compaction.mode = "notify"` alerts user via Discord when context threshold is approached instead of auto-compacting; backoff prevents notification spam
- **`/compact` Discord command**: Manual compaction trigger respecting configured strategy (`fresh-session` or inline); typing indicator and 3-min timeout
- **BM25 tool search**: Replace substring search with BM25-ranked two-stage tool reranking; pluggable reranker interface, deferred tool loading, usage frequency tracking
- **Per-task todo file**: Agents can maintain per-task todo lists at `agents/{agent}/todos/{taskId}.md`; task ID shown on dashboard cards with URL hash routing
- **Per-context tool profile**: Agents can declare a tool profile override in their config, applied per-context without affecting other agents
- **Session auto-resume**: Discord sessions auto-continue context from archived sessions on channel reconnect
- **`allowDangerous` taskboard flag**: Bypass dangerous-ops check for trusted automation flows

### Fixed
- **Discord output cap**: Raise max output from 5700 → 16000 chars with chunked delivery
- **CLI error surfacing**: Non-JSON CLI output now captured (10 lines × 300 chars) and surfaced as structured error instead of silent failure
- **Image error auto-recovery**: Detect image-related CLI crash → archive broken session → notify user with friendly message; no more wedged channels
- **Agent session dispatch bypass**: Agent sub-sessions auto-bypass the concurrent dispatch guard to prevent deadlock
- **Claude binary auto-detect**: Detect `claude` binary via login shell before falling back to PATH, fixing installs that use `fnm`/`nvm` managed Node
- **Service restart**: Use `tetora restart` in bump flow; fix KeepAlive respawn loop with proper `bootout` before kill
- **Session token counter reset**: Inline compaction now resets `total_tokens_in` to 0 so the compaction threshold re-arms correctly after compact
- **Memory abort on failure**: `compactSessionFresh` aborts archive if summary memory write fails, preventing silent context loss
- **Summary one-shot injection**: Replace `MessageCount <= 3` guard with delete-after-inject, eliminating async race in summary injection

---

## [v2.2.4] - 2026-04-12

### Added
- **Regression guard tests**: Context cancellation timeout tests for DB operations, unit test for dispatchTask prompt construction with task ID validation
- **Context propagation improvements**: ExecContext/QueryContext for DB calls, QuerySessionByIDCtx method to ensure timeout handling in all goroutines
- **Site SEO enhancements**: BreadcrumbList structured data, theme-color extraction, OG locale optimization
- **CI improvements**: Auto-update site version on release, CODEOWNERS for auto-assignment, dashboard drift guard (rebuild + `git diff --exit-code` on every PR)
- **Tips**: Model Selection Per Role (en/ja/zh-TW) — Sonnet for drafts, Opus for strategy
- **Observability**: Warning logs to distinguish CLI silent success vs truly no output in provider streaming

### Fixed
- **i18n URL dedup**: Fix duplicate locale prefix in zh-TW blog/tips URLs (e.g. `/zh-TW/blog/zh-tw/slug/`)
- **Build system**: Extract platform-specific process group handling, syscall usage for Windows cross-compile support
- **Site versioning**: Update i18n download buttons to v2.2.3, fix release CI workflow
- **Refactoring**: Extract OG_LOCALE_MAP constant from Base.astro to consts.ts for reusability, preserve task description language (繁體中文) in agent commit messages
- **Skills cache**: `LoadFileSkills` now caches per-directory with `sync.RWMutex`; invalidated on create/approve/delete
- **SessionLockFile**: Extracted `.tetora-active` into a shared constant to eliminate duplicate hardcoded strings

---

## [v2.2.3] - 2026-04-04

### Added
- **Model switching UX**: Discord `!model pick` interactive picker (Agent → Provider → Model), `!local`/`!cloud` bulk toggle, `!mode` summary with buttons
- **Dashboard model picker**: Provider bar + model dropdown in agent editor, quick-switch on agent cards, Cloud/Local badges, global inference mode toggle
- **Inference mode API**: `POST/GET /api/inference-mode` for cloud/local/mixed switching with atomic batch config write
- **Claude provider preference**: `claudeProvider` config field — choose between `claude-code` (CLI) and `anthropic` (API) per installation
- **Per-model history tracking**: `provider` column in history DB, `byModel` stats grouped by model + provider
- **Human gate reject recovery**: Retry API + dashboard UI for rejected human gates
- **Human gate cancel**: Cancel API + dashboard button
- **Human gate Discord notifications**: Timeout/waiting/assignee notifications with dashboard links
- **Human gate schema unification**: `action` (canonical) + `decision` (legacy compat) API fields
- **VibeVoice TTS**: Local VibeVoice + fal.ai cloud TTS providers with fallback chain
- **Skill AllowedTools**: Per-skill tool restrictions via `allowedTools` field in SkillConfig
- **Learned skill extraction**: Auto-extract skills from session history with pending review display
- **Discord commands docs**: Full reference at `docs/discord-commands.md` + README section
- **Blog**: v2.2 release notes (en/zh-TW/ja), Auto-Dispatch tips
- **CLI**: `tetora project add` and `tetora guide` commands
- **Config**: `config.local.json` deep-merge override support
- **Privacy policy**: tetora.dev/privacy page

### Fixed
- **Codex CLI v0.118+**: Support new JSONL format (`item.completed` events), close stdin to prevent hang
- **Claude CLI permissions**: Add `--dangerously-skip-permissions` when `bypassPermissions` is set (enables Bash commands like `gh`)
- **Local model cost**: Ollama/LM Studio endpoints automatically report $0 cost
- **Provider inference**: Dynamic model matching for Ollama (e.g. `dolphin-mistral` matches `dolphin-mistral:latest`), exact match for Claude aliases
- **Auto-new-session**: Automatically archive Discord session when provider changes
- **workspace package**: Add missing `internal/workspace/` (was imported but never committed)
- **resumeWorkflow**: Fix missing `extraVars` parameter at 3 call sites
- **Data race**: Hold mutex during `checkpointRun` JSON marshal in workflow DAG
- **TestSpendingForecast**: Avoid month boundary crossing when day-of-month < 5
- **Session resume**: Retry with new session when resume fails with `error_during_execution`
- **launchd**: Fix plist PATH and make reload race condition
- **Codex quota**: Detect quota/usage-limit errors from stdout and stderr
- **OAuth**: Fix redirect URL construction
- **Security**: Harden config/data file permissions (0644 → 0600)
- **Dispatch**: Dedup guard + execution guard to prevent infinite retry loops
- **Worktree**: Auto-resolve `.tetora-branch` conflict on merge

### Changed
- **IsLocalProvider / IsLocalEndpoint helpers**: Replace magic string checks across codebase
- **TruncateStr consolidation**: Unified into `internal/text` package with rune-aware truncation
- **History SQL**: Migrate write operations from `db.Query` to `db.Exec` for write mutex consistency
- **Human gate**: Replace `http.Error` with `writeJSONError` for consistent JSON responses
- **Provider presets**: Add `gpt-5.4`, `gpt-5.3` to OpenAI/Codex presets
- **OpenAIProvider**: Cache `IsLocal` at construction instead of per-request URL check

---

## [v2.2.2] - 2026-03-27

### Added
- **Coord region granularity**: Directory-level coordination (was workspace-level), with dispatch serialization on region conflict
- **Project workdir required**: DB trigger + app layer enforcement for non-empty workdir on project creation/update
- **Provider preset UI**: Custom baseUrl input, Anthropic native provider type with `x-api-key` auth, connection test endpoint
- **DangerousOpsConfig**: Pattern-based blocking engine for destructive commands (rm -rf, DROP TABLE, force-push, etc.) — configurable allowlist per agent
- **Pipeline overhaul**: Async scanReviews with semaphore (max 3), pipeline health check monitor (30 min), process group kill on timeout, zombie detection (ResetStuckDoing)
- **Skill auto-load**: SKILL.md-only skills + always-on catalog, frontmatter compatibility aliases
- **Site**: Astro migration, pnpm, GA4 defer, WebP logo, dynamic sidebar, i18n docs
- **Multi-tenant dispatch**: `--client` flag for per-client output path isolation; team builder CLI
- **Memory temporal decay**: Knowledge entries now have time-based relevance decay
- **Self-liveness watchdog**: Supervisor-managed automatic restart when process becomes unresponsive
- **Dashboard File Browser**: Full directory browsing with lazy-load expansion, Markdown rendering with View/Edit toggle
- **Dashboard Team Builder redesign**: Completely rebuilt UI for team configuration page
- **Discord `!chat`/`!end` agent locking**: Per-channel agent lock with cancellable task context
- **History CLI diagnostics**: `tetora history fails`, `streak`, `trace` subcommands for failure analysis and job tracing
- **Store**: Skill-workflow items in browse results
- **Worktree isolation**: Now applies to default-project tasks; gate coverage for all 4 conditions
- **Worktree failure preservation**: Failed/cancelled tasks with commits or changes preserved as `partial-done` instead of discarded

### Fixed
- **Reflection**: NOT NULL constraint on role column fix; duplicate `role` column in INSERT fix; wiring to taskboard + cron executors
- **Blank error messages**: Provider + runSingleTask final guards, never empty error message
- **Workspace git**: index.lock retry with backoff, serialization mutex (`wsGitMu`), stale lock threshold 1h→30s
- **Escalated review auto-approve**: After 4h stale, auto-approve escalated reviews
- **SSRF fix**: `/api/provider-test` endpoint hardened
- **XSS fix**: Provider preset UI input sanitization
- **Coord JSON truncation**: Rune-aware string slicing
- **Concurrent limit noise**: Fix skipped_concurrent_limit noise from 30s tick
- **Stale hook worker cleanup**: Periodic garbage collection of zombie hook worker processes
- **Discord proactive delivery**: Channel delivery for proactive notifications; heartbeat and cooldown timer fixes
- **MCP mock server type mismatch**: Corrected InputSchema type in test fixtures
- **Budget pause + nil-registry guard**: `cost.SetBudgetPaused` API fix; dispatch no longer panics on nil registry
- **Task ID normalization**: Normalize task ID in all mutating Engine methods
- **Skill completion tracking**: Fallback to role+time window matching
- **Worktree data-loss**: Conditional cleanup with stale index.lock detection
- **Dispatch workdir fallback**: Warn when project workdir is empty and fallback is used
- **Agent AddDirs scope**: Block bare `$HOME` from agent AddDirs; add `find ~/` to dangerous ops patterns

### Changed
- **Anthropic version constant**: Extracted shared `anthropic-version` into provider package constant
- **Site**: Replaced legacy PNG logo with WebP (909KB → 3KB)
- **CI**: pages.yml self-trigger path filter

---

## [v2.2.1] - 2026-03-19

### Added
- **Task Detail cancel button**: Cancel running tasks directly from the Task Detail modal (yellow "Cancel" button, visible only when status is "doing")
- **Workflow progress cancel button**: Cancel running workflow runs from the step progress panel ("Cancel Run" button next to "View Full Run")
- Cancel buttons auto-hide when workflow completes or status changes away from "doing"

---

## [v2.1.0] - 2026-03-18

### Highlights
- **Massive codebase consolidation**: 28 root source files → 9 domain files. 111 test files → 22. Cleaner, easier to navigate.
- **Workflow Engine & Marketplace**: DAG pipeline execution, dynamic model routing, Store tab, Import/Export
- **Dashboard improvements**: Workflow progress tracking, Capabilities tab, DAG visualization

### Added
- **Git Workflow Pipeline — configurable branch convention**: Branch naming for agent dispatch is now template-based (`{type}/{agent}-{description}`), configurable via `taskBoard.gitWorkflow` in config
- **TaskBoard `type` field**: Tasks now have a type (feat/fix/refactor/chore) for branch naming; available in Dashboard create/detail UI, CLI `--type`, and agent tools
- **Worktree `.tetora-branch` metadata**: Worktrees record their branch name in a metadata file, enabling dynamic branch names instead of hardcoded `task/{taskID}`
- **Decompose subtask type inheritance**: Subtasks inherit parent task type unless explicitly overridden

### Fixed
- **`slugify()` regexp recompilation**: Hoisted to package-level `var` to avoid compiling on every call

---

## [v2.0.4] - 2026-03-16

### Added
- **Skill observability CLI**: `tetora skill log`, `skill stats`, `skill diagnostics` commands for tracking skill usage
- **Session-based skill completion**: `recordSkillCompletion` uses `session_id` for accurate attribution
- **Dashboard MCP toggle**: Claude Code MCP server enable/disable in Settings > Integrations
- **`local-install` Makefile target**: Build, stop daemon, copy binary, codesign, restart in one command
- **Upgrade `--force` flag**: Force re-download even when version matches

### Fixed
- **`tetora upgrade` semver comparison**: Proper numeric version comparison instead of string equality; dev builds (2.0.3.1) correctly upgrade to releases (2.0.3)
- **`tetora upgrade` nil pointer dereference**: `os.Stat` failure no longer crashes on `info.Size()`
- **`tetora upgrade` binary verification**: Downloaded binary is executed to confirm version before and after replacement
- **Classify cron/workflow as keyword-based**: No longer auto-Complex; uses keyword counting to save tokens on simple scheduled tasks
- **MCP concurrent test race condition**: Channel-based synchronization replaces `time.Sleep` in `TestServerContextCancellationDuringRequest`

### Changed
- **Backup includes `dbs/`**: Database files now included in `tetora backup` output
- **Permission mode cleanup**: `autoEdit` replaced with `acceptEdits` in examples and dashboard

---

## [v2.0.3] - 2026-03-16

### Added
- **Workflow Engine**: DAG-based pipeline execution with condition branches, parallel steps, and retry logic
- **Dynamic Model Routing**: auto-detect task complexity and route accordingly (low complexity → Sonnet, high complexity → Opus)
- **Template Marketplace**: Store tab in the dashboard for browsing and importing workflow templates by category
- **Capabilities tab**: grid view of all installed tools, skills, workflows, and templates
- **Import/Export**: `tetora workflow export <name>` and `tetora workflow create <file>` CLI commands for sharing workflow definitions
- **GitLab MR support**: `standard-dev` workflow auto-detects GitHub vs GitLab remote and creates a PR or MR accordingly
- **Slot Pressure System**: reserved slots for interactive sessions; non-interactive batch tasks queue automatically when slots are scarce
- **Token-based session compaction**: sessions auto-compress when token count exceeds the 200K threshold, preventing unbounded context growth
- **Partial-done task status**: recoverable intermediate state for tasks that complete core work but fail post-processing steps (git merge, review)
- **Worktree data-loss prevention**: conditional worktree cleanup with stale `index.lock` detection to avoid destroying uncommitted work
- **Lessons.md injection**: agent lessons from `workspace/memory/lessons.md` are automatically injected into dispatch prompts
- **Workflow step progress tracking**: dashboard shows per-step status and progress for running workflows
- **Dashboard DAG visualization**: workflow DAG rendered with theme-adaptive colors in the workflow editor
- **Docs i18n**: 54 translation files across 9 languages for 6 core docs; language selector auto-detects browser locale
- **MCP concurrent safety**: `mcpMu` + `configFileMu` mutexes; concurrent CRUD races eliminated; test coverage raised to 81.8%
- **Skill attribution via session_id**: `recordSkillCompletion` uses exact `session_id` match instead of time-window heuristic
- **Dashboard "Show Done" toggle**: kanban board filter bar checkbox to include done/failed tasks via `includeDone=true`

### Fixed
- **Dispatch skips non-agent assignees**: tasks assigned to human users (e.g. "takuma") are no longer auto-dispatched by the daemon
- **Deprecated `autoEdit` permission mode**: replaced with `acceptEdits` / `auto` for Claude Code compatibility
- **Discord gateway dedup**: 128-entry ring buffer skips replayed `MESSAGE_CREATE` events on gateway Resume
- **Dashboard theme colors**: chat bubbles, cost badge, SVG grid lines now use CSS variables — all 8 themes render correctly
- **Service Worker intercepting API requests**: Service Worker now excludes API routes, fixing 401 errors
- **Dashboard refresh buttons not working**: re-fetch logic repaired across all dashboard tabs
- **Review execution error**: review step returns `"escalate"` on errors instead of incorrect `"approve"`
- **Session context growing unbounded**: compaction threshold and trigger logic corrected
- **Bump safety check**: `make bump` warns and aborts if workflows are currently running

### Changed
- **`standard-dev` workflow**: now creates a PR or MR instead of merging directly to main
- **`direct-dev` workflow**: new workflow for private projects where direct merge is acceptable

### Improved
- **Package extraction (Phase 0–1a)**: 14 packages extracted to `internal/` — `db`, `log`, `cron`, `nlp`, `circuit`, `backup`, `pwa`, `sprite`, `quickaction`, `webhook`, `i18n`, `classify`, `audit`, `version`; dead wrapper code removed
- **MCP reviewer fixes**: notifications, protocol version, nextID, error handling, duplicate loadConfig
- **Unit tests**: new tests for db, audit, webhook, sprite packages

---

## [v2.0.0] - 2026-03-08

### Added
- **Claude Code Hooks integration (v3 architecture)**: Worker tracking now runs entirely via Claude Code PostToolUse/Stop/Notification hooks — no more tmux polling
- **MCP server bridge**: Claude Code agents can now access Tetora tools natively via MCP protocol
- **Remote plan mode via Discord**: Rich plan review embeds with Approve/Reject buttons; plan history stored in DB
- **Dashboard plan review panel**: Real-time pending plans with approve/reject UI + notification sound
- **Dashboard hooks events feed**: Live stream of Claude Code hook events (tool calls, notifications) in Activity Feed
- **One-click hooks installer in Dashboard**: Settings tab UI for installing/removing Claude Code hooks without CLI knowledge
- **`tetora hooks` CLI commands**: `install` / `status` / `remove` for managing Claude Code settings.json integration
- **Terminal Bridge + Codex CLI support**: New `TerminalProvider` running Claude Code or Codex in isolated tmux sessions (parallel to hooks mode)
- **Taskboard parent/child subtasks**: Hierarchical task decomposition for autonomous dispatch
- **Worker monitor goroutine**: Detects stalled/recovered workers + fires SSE Activity Feed events
- **Dashboard mini-map + office customization**: CEO command center visual redesign (v2.0 stretch goals)
- **Smart orphan recovery**: LLM timeout estimation + default pixel agent fallback (v1.7.8)
- **Dependency tree view**: Dashboard shows inter-task dependency graph (v1.7.6)

### Fixed
- **P1: SQL retry + fallback on final status update** — tasks no longer get stuck in doing on DB write failure
- **Cross-channel session leakage**: Dispatch no longer routes tasks to wrong Discord channel sessions
- **Cron hot-reload**: Config changes now take effect without daemon restart
- **Skill injection limits**: Prevents prompt overflow when injecting large skill sets
- **Triage fast-path**: Reduced latency on high-priority task routing
- **HTTP bind race**: Eliminated race condition on daemon startup port binding
- **Orphan recovery**: tmux sessions properly cleaned up on daemon restart
- **Claude Code v2+ startup detection**: Fixed prompt detection for new Claude Code session format
- **Workflow × dispatch session lifecycle**: Template resolution and output merge fixed for complex workflow chains
- **Session resumption flag**: Corrected `--resume` vs `--continue` for Claude Code CLI

### Changed (Refactor)
- **Complete tmux removal (v3)**: Deleted `tmux_supervisor.go`, `provider_tmux.go`, `tmux_profile.go`, `discord_terminal.go` (−4,249 LOC); hooks are now the single source of truth for worker state
- **Removed `claude-api` direct provider** (−1,380 LOC): Replaced by `claude-code` provider; old configs auto-migrate
- **Hooks-only worker tracking**: tmux polling relaxed to backup-only (15s interval) when hooks are active
- Dashboard Workers tab merged into main dashboard layout

---

## [v2.0.0] - 2026-03-08

### 新增
- **Claude Code Hooks 整合（v3 架構）**：Worker 狀態追蹤完全改由 Claude Code PostToolUse/Stop/Notification hooks 驅動，不再依賴 tmux 輪詢
- **MCP server bridge**：Claude Code agent 可透過 MCP protocol 原生使用 Tetora 工具
- **Discord 遠端 plan review**：Rich embed 顯示完整計劃文字，含 Approve/Reject 按鈕，歷史記錄存入 DB
- **Dashboard plan review 面板**：即時顯示待審計劃 + 通知音效
- **Dashboard hooks 事件流**：Activity Feed 即時顯示 Claude Code hook 事件（tool calls、通知）
- **一鍵 hooks 安裝 UI**：Dashboard Settings 頁，非工程師也能安裝/移除 Claude Code hooks
- **`tetora hooks` CLI 指令**：`install` / `status` / `remove` 管理 Claude Code settings.json 整合
- **Terminal Bridge + Codex CLI 支援**：新增 `TerminalProvider`，可在隔離 tmux session 中執行 Claude Code 或 Codex
- **Taskboard 父子任務**：分層任務拆解，支援自主 dispatch 工作流
- **Worker monitor goroutine**：偵測停滯/恢復的 worker，透過 SSE 發射 Activity Feed 事件
- **Dashboard mini-map + 辦公室自訂**：CEO 指揮中心視覺全面翻新
- **智慧孤兒恢復**：LLM timeout 估算 + 預設 pixel agent 備援（v1.7.8）
- **依賴樹視圖**：Dashboard 顯示任務間相依圖（v1.7.6）

### 修復
- **P1：SQL retry + 最終狀態更新 fallback** — DB 寫入失敗不再導致任務卡在 doing
- **跨頻道 session 洩漏**：dispatch 不再把任務路由到錯誤的 Discord 頻道
- **Cron 熱重載**：設定變更不需重啟 daemon 即可生效
- **Skill 注入上限**：防止大量 skill 注入導致 prompt 溢出
- **Triage 快速路徑**：高優先任務路由延遲降低
- **HTTP 啟動 race condition**：消除 daemon 啟動時的 port 綁定競爭
- **孤兒 session 清理**：daemon 重啟時正確清理 tmux session
- **Claude Code v2+ 啟動偵測**：修正新版 Claude Code session 格式的 prompt 偵測
- **Workflow × dispatch session 生命週期**：修正複雜工作流的模板解析與輸出合併
- **Session 恢復旗標**：修正 `--resume` vs `--continue` 用法

### 變更（重構）
- **完整移除 tmux 依賴（v3）**：刪除 `tmux_supervisor.go`、`provider_tmux.go`、`tmux_profile.go`、`discord_terminal.go`（−4,249 LOC）
- **移除 `claude-api` 直接 provider**（−1,380 LOC）：改由 `claude-code` provider 取代，舊設定自動遷移
- **Hooks 為唯一 worker 狀態來源**：tmux 輪詢降為備援（15s interval）
