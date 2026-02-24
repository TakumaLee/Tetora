# Task Tracking

> Active task plans and progress. Updated each session.

## Current

### P29 + P31 Partial: Cross-Module Intelligence + Architecture Hardening II ✅ (2026-02-24)

**P29.1: Quick Capture** ✅
- [x] `capture.go` (~120 lines) — keyword classifier (expense/reminder/contact/task/idea/note)
- [x] `capture_test.go` (~50 lines) — 20 table-driven tests
- [x] `quick_capture` tool registered

**P29.2: Time Tracking** ✅
- [x] `timetracking.go` (~270 lines) — DB schema, CRUD, report aggregation
- [x] `timetracking_test.go` (~130 lines) — 6 tests
- [x] `TimeTrackingConfig` in config.go
- [x] 4 tools: `time_start`, `time_stop`, `time_log`, `time_report`
- [x] Service init in main.go

**P29.0: Closed-Loop Automation** ✅
- [x] `lifecycle.go` (~270 lines) — LifecycleEngine connecting modules
- [x] `lifecycle_test.go` (~90 lines) — 7 tests
- [x] `LifecycleConfig` in config.go
- [x] 2 tools: `lifecycle_sync`, `lifecycle_suggest`
- [x] Hooks in goals.go (habit suggest on goal create, celebration on goal complete)
- [x] Service init in main.go

**P31.0: App Struct Phase 2** ✅
- [x] Added 7 fields to App struct (Lifecycle, TimeTracking, SpawnTracker, JudgeCache, ImageGenLimiter, UnifiedMemoryEnabled, UnifiedMemoryDB)
- [x] Updated SyncToGlobals() + main.go App construction
- [x] `TestAppSyncToGlobals_Phase2Fields` — passes

**P31.1: tool.go Split** ✅
- [x] Split 3,535-line tool.go → 7 files (133 + 790 + 455 + 803 + 965 + 461 + 123 = 3,730 lines)
- [x] tool.go (infrastructure), tool_core.go, tool_memory.go, tool_life.go, tool_integration.go, tool_daily.go, tool_admin.go
- [x] Each file exports `registerXxxTools()`, called from `registerBuiltins()`

**P31.2: Integration Tests** ✅
- [x] `integration_test.go` (~310 lines) — mock ToolCapableProvider
- [x] 8 tests: BasicToolCall, MultipleIterations, NoToolCalls, ToolNotFound, BudgetExceeded, RoleFiltering, ConcurrentRace, ConfigReloadRace
- [x] All pass with `-race` flag

**Verification** ✅
- [x] `go build ./...` — PASS
- [x] All P29+P31 tests (25 total) — PASS
- [x] Race detector — PASS
- [x] `go vet` — only pre-existing prom.go warning
- [x] New files: 12 (6 source + 4 test + 6 tool split)
- [x] New lines: ~1,530

---

### Project Restructure + OpenClaw Migration Upgrade ✅ (2026-02-24)

**Part A: Project Directory Restructure** ✅
- [x] Source code moved to `~/projects/tetora/`
- [x] Runtime data stays at `~/.tetora/` (bin, config.json, jobs.json, history.db, logs, sessions, workspace, mcp)
- [x] `.gitignore` updated for new location
- [x] `go build ./...` passes from new location
- [x] `make install` deploys to `~/.tetora/bin/tetora`
- [x] `~/.tetora/bin/tetora` runs correctly
- [x] CLAUDE.md + MEMORY.md updated

**Part B: OpenClaw Migration Upgrade** ✅
- [x] B1: Nested JSON parsing — `getNestedString/Int/Bool/Map/Slice`, `traverseNested`, `maskSecret`, `stripModelPrefix`
- [x] B1: Config mapping — 10+ nested paths (channels.telegram.botToken, agents.defaults.model.primary, gateway.port, etc.)
- [x] B2: Fixed memory path to `workspace/memory/*.md` (101 files detected in real install)
- [x] B2: Workspace migration — SOUL.md, AGENTS.md, USER.md, IDENTITY.md, MEMORY.md, HEARTBEAT.md warning
- [x] B3: Cron jobs migration — handles wrapped format `{"version":1,"jobs":[...]}`, slugify, merge/skip existing
- [x] B4: Folder-based skills — recursive `copyDir()` for SKILL.md directories
- [x] B5: `detectOpenClaw()` integrated into `tetora init` — interactive import with 4 presets
- [x] B5: OpenClaw values pre-fill wizard steps (channel tokens, model)
- [x] B6: 29 tests — helpers, nested config, no-overwrite, memory, workspace, skills folders, cron, cron skip existing, dry-run, subset, invalid JSON

**Verification** ✅
- [x] `go build ./...` — PASS
- [x] `go test ./...` — ALL PASS (64s)
- [x] `go vet` — only pre-existing prom.go warning
- [x] `tetora migrate openclaw --dry-run` — correctly detects 2 config fields, 101 memory files, 5 workspace files, 22 cron jobs
- [x] `make install` + installed binary works

---

### P28: Approval Gates + App Struct ✅ (2026-02-23)

**P28.0: Approval Gates** ✅
- [x] `ApprovalGate` interface + `ApprovalRequest` type in tool_policy.go
- [x] `needsApproval()`, `requestToolApproval()`, `summarizeToolCall()`, `gateReason()` helpers
- [x] `ApprovalGateConfig` struct + field in config.go
- [x] Approval gate check in dispatch.go agentic loop (after filterToolCall)
- [x] `approvalGate` field on Task struct
- [x] Telegram: `tgApprovalGate` + inline keyboard approve/reject + callback wiring
- [x] Discord: `discordApprovalGate` + button components + handleBuiltinComponent wiring
- [x] Slack: `slackApprovalGate` + text-based approve/reject in event handler
- [x] Tests: `TestNeedsApproval`, `TestSummarizeToolCall`, `TestApprovalGateTimeout`, `TestGateReason`

**P28.1: App Struct Phase 1** ✅
- [x] `app.go` — App struct with 25 service fields + `SyncToGlobals()`
- [x] `app` field added to Server struct in server.go
- [x] App created in main.go daemon mode, populated from globals, passed to Server
- [x] Tests: `TestAppSyncToGlobals`, `TestAppNilSafe`

**Verification**
- [x] `go build ./...` passes
- [x] `go test -run "TestNeedsApproval|TestSummarize|TestApprovalGate|TestGateReason|TestApp"` — all pass
- [x] Full test suite: all pass (one pre-existing flaky test: TestNewTraceID_Uniqueness)
- [x] `go vet` — one pre-existing warning in prom.go, no new warnings

---

### P27: Audio Tool + Skill Ecosystem + Hardening ✅ (2026-02-23)

**P27.0: Audio Normalize** ✅
- [x] `toolAudioNormalize` handler in tool.go — ffmpeg loudnorm, 5min timeout, in-place support
- [x] Registration in `registerBuiltins()` guarded by `enabled("audio_normalize")`

**P27.1: Skill Install + Sentori Scanner** ✅
- [x] `skill_install.go` (424 lines) — SentoriReport types, 24 pre-compiled regexps, 5 categories
- [x] `sentoriScan()` — score calc (critical +25, high +15, medium +8, low +3, cap 100)
- [x] `toolSentoriScan` — scan by name or raw content
- [x] `toolSkillInstall` — download, parse, scan, refuse/install, store report
- [x] `toolSkillSearch` — search skill-registry.json
- [x] `skill_install_test.go` (184 lines) — 5 tests (safe, exec, paths, exfil, scoring)
- [x] CLI: `tetora skill install/search/scan` commands in cli_skill.go
- [x] 3 new tool registrations in tool.go

**P27.2: SQLite Selective Encryption** ✅
- [x] `crypto.go` (~120 lines) — generalized AES-256-GCM encrypt/decrypt
- [x] `encryptField`/`decryptField` helpers with graceful fallback
- [x] `globalEncryptionKey()` for standalone functions (session.go)
- [x] Refactored oauth.go to delegate to crypto.go
- [x] `EncryptionKey` config field (supports $ENV_VAR)
- [x] Encryption at callsites: session content, contacts PII, finance descriptions, habit notes
- [x] Decryption at read sites: sessionMessageFromRow, rowToContact, expense listing
- [x] `tetora migrate encrypt` CLI command
- [x] `crypto_test.go` (180 lines) — 11 tests (round-trip, nonce, empty, fallback, long)

**P27.3: Streaming to Channels** ✅
- [x] `ChannelNotifier` interface in dispatch.go (SendTyping, SendStatus)
- [x] `channelNotifier` field on Task struct (unexported)
- [x] `StreamToChannels` config toggle
- [x] Typing at iteration start + status after tool calls in agentic loop
- [x] `tgChannelNotifier` in telegram.go — wired before dispatch
- [x] `discordChannelNotifier` in discord.go
- [x] `slackChannelNotifier` in slack.go (no-op — Slack needs RTM for typing)

**P27.4: Lightweight Onboarding** ✅
- [x] Restructured cli_init.go into 3-step flow (Channel → Provider → Generate)
- [x] Supports Telegram/Discord/Slack/None channel selection
- [x] Supports Claude CLI/Claude API/OpenAI-compatible provider selection
- [x] Enhanced cli_doctor.go with suggestions (no provider, no channel, no encryption key, no ffmpeg, sqlite3 check)

## Completed

### P26: Agentic Loop Hardening ✅ (2026-02-23)

- [x] 26.0: Tool filtering per role — `ListFiltered()` in tool.go, wired in dispatch.go
- [x] 26.1: Token/cost accumulation — accumulators across iterations, set on finalResult
- [x] 26.2: Tool output truncation — `truncateToolOutput()`, config `toolOutputLimit` (default 10240)
- [x] 26.3: Per-tool execution timeout — `context.WithTimeout`, config `toolTimeout` (default 30s)
- [x] 26.4: Mid-loop budget + context check — per-task budget, global budget, context window estimate
- [x] Tests: 8 new tests (ListFiltered x3, TruncateToolOutput x4, TokenAccumulation x1) — all pass

### P25: Hardening & Polish ✅ (2026-02-23)

**Phase 1: Crash Protection** ✅
- [x] 1A: HTTP panic recovery middleware (`recoveryMiddleware` in http.go)
- [x] 1B: Tool execution panic recovery (`safeToolExec` in dispatch.go)
- [x] 1C: Request body size limit (10MB `bodySizeMiddleware` in http.go)

**Phase 2: Security Hardening** ✅
- [x] 2A: Injection defense fail-closed (`FailOpen` field in injection.go)
- [x] 2B: RequireAuth tools default to `suggest` trust level (tool_policy.go)
- [x] 2C: Webhook secret warning at startup (main.go)

**Phase 3: Database Resilience** ✅
- [x] 3A: SQLite WAL mode + pragmas (`pragmaDB` in tasks.go, called in main.go)
- [x] 3B: Backup verification (`verifyDBBackup` in backup_schedule.go)
- [x] 3C: Fix silent error swallowing in embedding.go migrations

**Phase 4: Operational Robustness** ✅
- [x] 4A: Fix tomorrow 3pm bug (EN/JA/ZH paths in reminder.go)
- [x] 4B: Degraded state awareness (`DegradedServices` in server.go, healthz reporting)
- [x] 4C: Config reload diff logging (`logConfigDiff` in main.go)

**Phase 5: Code Quality** ✅
- [x] 5A: HTTP middleware tests (recovery, body size in http_test.go)
- [x] 5B: CI pipeline (.github/workflows/ci.yml)

### P24: Agentic Life OS ✅ (2026-02-23)

**P24.0: Agentic Loop Activation** ✅
- [x] ExecuteWithTools() in ClaudeAPIProvider and OpenAIProvider
- [x] StopReason propagation in dispatch.go

**P24.1: Semantic Memory Integration** ✅
- [x] hybridSearch() with TF-IDF + vector fusion
- [x] reindexAll(), wire umSearchSemantic() into umSearch()

**P24.2: Contact & Social Graph** ✅
- [x] contacts.go (748 lines) — cross-channel contacts, birthday, inactivity
- [x] contacts_test.go (772 lines)
- [x] http_contacts.go (213 lines)
- [x] 5 tools registered: contact_add, contact_search, contact_list, contact_upcoming, contact_log
- [x] Service init in main.go
- [x] HTTP routes wired

**P24.3: Life Insights Engine** ✅
- [x] insights.go (1,148 lines) — behavioral analysis, anomaly detection, spending forecast
- [x] insights_test.go (1,145 lines)
- [x] 2 tools registered: life_report, life_insights
- [x] Service init in main.go

**P24.4: Smart Scheduling** ✅
- [x] scheduling.go (796 lines) — free slot finding, overcommitment detection
- [x] scheduling_test.go (803 lines)
- [x] 3 tools registered: schedule_view, schedule_suggest, schedule_plan
- [x] Service init in main.go

**P24.5: Habit & Wellness Tracking** ✅
- [x] habits.go (982 lines) — streak tracking, health webhook, reports
- [x] habits_test.go (584 lines)
- [x] http_habits.go (207 lines)
- [x] 6 tools registered: habit_create, habit_log, habit_status, habit_report, health_log, health_summary
- [x] Service init in main.go
- [x] HTTP routes wired

**P24.6: Goal Planning & Autonomy** ✅
- [x] goals.go (789 lines) — goal decomposition, milestone, weekly review
- [x] goals_test.go (923 lines)
- [x] 4 tools registered: goal_create, goal_list, goal_update, goal_review
- [x] Service init in main.go

**P24.7: Morning Briefing & Evening Wrap** ✅
- [x] briefing.go (619 lines) — morning/evening summaries, section aggregation
- [x] briefing_test.go (928 lines) — 39 tests
- [x] 2 tools registered: briefing_morning, briefing_evening
- [x] Service init in main.go

**P24 Stats:**
- New files: 14 (7 source + 7 test)
- P24-specific lines: 10,657
- Total registered tools added: 22
- Build: ✅ PASS
- Tests: ✅ ALL PASS (except pre-existing TestParseNaturalTime_English/tomorrow_3pm)

### P23: Unified Memory + Life Intelligence Layer ✅
### P22: Architecture Hardening + Dashboard + Daily Tools ✅
### P0-P21: All complete ✅

## Build Status
- Last build: ✅ PASS (2026-02-24)
- Last test: pre-existing TestNewTraceID_Uniqueness flaky + prom.go vet warning only
- Total Go lines: ~143,330 + ~1,530 new = ~144,860

## Review Notes

### P22.1 Notes
- `go vet` reports one pre-existing warning in prom.go (WriteTo signature) — not introduced by this change
- `TestParseNaturalTime_English/tomorrow_3pm` fails due to time-of-day sensitivity — pre-existing, unrelated
- Created `tryLoadConfig()` in config.go for safe reload (returns error instead of os.Exit)
- toolRegistry is the only runtime field that needs preservation across config reload

## Future Roadmap (Tentative)

> 記錄於 2026-02-24。非當前優先事項，供未來規劃參考。

### P29: Cross-Module Intelligence ✅ (completed 2026-02-24)

### P30: Life Completeness (~1,250 行)
- P30.0: 結構化日記 (journal.go, ~300 行) — gratitude/mood/reflection 模板
- P30.1: 學習/閱讀追蹤 (learning.go, ~350 行) — 書籍、課程、技能練習
- P30.2: Apple Health / Google Fit (health_data.go, ~600 行) — 穿戴裝置健康數據

### P31: Architecture Hardening II ✅ (partially completed 2026-02-24)
- P31.0-P31.2 done. Remaining: further global migration (future)

### P32: Financial & Nutrition (~650 行)
- P32.0: 資產/淨資產追蹤 (~250 行)
- P32.1: 營養追蹤 (nutrition.go, ~400 行) — FatSecret/USDA API

### P33: Polish (~700 行)
- P33.0: WebChat channel (~300 行) — WebSocket-based browser chat
- P33.1: Provider fallback chain (~200 行) — 自動切換備選 provider
- P33.2: 離線 CLI mode (~200 行) — 無網路時直接 query SQLite

### 其他 Nice-to-have
- 地點/情境感知 (geofencing triggers)
- 生活模板系統 (搬家、旅行、新工作 checklists)
- 用藥管理 (medication.go, ~300 行)
- 證件到期追蹤 (~200 行)
- 冥想計時 (~200 行)
- 1Password CLI 整合 (~150 行)
- Fuzz tests / Benchmark tests (~350 行)
- queryDB() LRU cache (~200 行)
