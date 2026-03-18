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
