# Tetora v2 — Development Execution Guide

> Created: 2026-02-23
> Purpose: 永久參照文件。所有未來的開發 session 都嚴格依照此指南執行。
> Scope: P13-P16 開發、平行任務分配、資源管理、Merge 協議

---

## 1. Resource Analysis & Constraints

### 1.1 Opus (Orchestrator) Context Budget

| Item | Tokens | Notes |
|------|--------|-------|
| System prompt + tools | ~26,000 | Fixed, non-compressible |
| Custom agents defs | ~4,000 | Fixed |
| Memory files | ~1,000 | Fixed |
| Autocompact buffer | ~33,000 | Reserved by system |
| **Available for work** | **~136,000** | |

**Per-round cost estimate:**
| Action | Tokens |
|--------|--------|
| Dispatch prompt (per agent) | ~2,000 |
| Agent result (per agent) | ~3,000-5,000 |
| Merge commands + verification | ~3,000 |
| Coordination overhead | ~2,000 |
| **Total per round (2 agents)** | **~15,000** |

**Rounds before compaction**: ~136k / 15k ≈ **9 rounds**
**After compaction**: conversation compresses → ~9 more rounds
**Conclusion**: 10 rounds 可在 1-2 次 compaction 內完成。

### 1.2 Sonnet (Worker) Budget

| Item | Value |
|------|-------|
| Context per agent | 200k tokens (independent) |
| Average usage per sub-phase | 45k-80k tokens |
| Success rate (P10-P12 data) | ~95% |
| Average execution time | 3-6 minutes |

### 1.3 Parallel Agent Limit — 結論: **2 Agents**

| Agents | Throughput | Merge Complexity | Context Pressure | Recommendation |
|--------|-----------|------------------|-----------------|----------------|
| 1 | 1x (baseline) | None | Low | Too slow |
| **2** | **2x** | **Low (1-3 conflicts/round)** | **Moderate** | **OPTIMAL** |
| 3 | 2.5x | High (8-13 conflicts on final merge) | High | Only if deadline pressure |
| 4+ | Diminishing | Nightmare | Unsustainable | Never |

**為什麼 2 比 3 好:**
- P10-P12 用 3 branches → final merge 有 13 個 conflict regions（http.go 8 個）
- 2 agents + 每 round merge → 每次 merge 只有 1 sub-phase 的差異
- Opus 有更多 context headroom 做品質控管
- Agent 失敗時損失較小
- 節省 ~15 minutes merge time，整體更穩定

### 1.4 Wall Clock Estimate

| Item | Time |
|------|------|
| Agent execution (2 parallel) | 5-6 min |
| Merge + build verification | 3-4 min |
| **Per round** | **~10 min** |
| **10 rounds** | **~100 min** |
| + retries, complex merges | ~120-150 min |
| **Total** | **2-3 sessions** |

---

## 2. Git & Worktree Strategy

### 2.1 Two Worktrees + Merge-Per-Round

```
~/.tetora/          ← master (Opus 在這裡操作 merge)
~/tetora-alpha/     ← feat/alpha worktree (Agent A)
~/tetora-beta/      ← feat/beta worktree (Agent B)
```

### 2.2 Setup (每次新 session 開始前)

```bash
# 確認 master 是最新的
cd ~/.tetora && git status

# 建立/重建 worktrees
git worktree add ~/tetora-alpha feat/alpha 2>/dev/null || \
  (cd ~/tetora-alpha && git checkout feat/alpha && git reset --hard master)
git worktree add ~/tetora-beta feat/beta 2>/dev/null || \
  (cd ~/tetora-beta && git checkout feat/beta && git reset --hard master)
```

### 2.3 Round Protocol (每一輪嚴格遵循)

```
Step 1: DISPATCH
  ├── 確認 feat/alpha 和 feat/beta 都在 master HEAD
  ├── 同時分派 Agent A → ~/tetora-alpha/
  └── 同時分派 Agent B → ~/tetora-beta/

Step 2: WAIT
  ├── 兩個 agent 都跑 background
  └── 等待兩個都完成

Step 3: VERIFY
  ├── cd ~/tetora-alpha && go build ./... && go test -run <relevant> -v
  └── cd ~/tetora-beta && go build ./... && go test -run <relevant> -v

Step 4: MERGE
  ├── cd ~/tetora-alpha && git push origin feat/alpha  (optional)
  ├── cd ~/.tetora && git merge feat/alpha --no-edit
  │   └── 解衝突 if any → commit
  ├── cd ~/.tetora && git merge feat/beta --no-edit
  │   └── 解衝突 if any → commit
  └── go build ./... (驗證 master)

Step 5: REBASE
  ├── cd ~/tetora-alpha && git rebase master
  └── cd ~/tetora-beta && git rebase master

Step 6: NEXT ROUND
```

### 2.4 Conflict Mitigation — Shared File Protocol

**高衝突檔案** (每個 feature 都會改到):

| File | Lines | Strategy |
|------|-------|----------|
| `config.go` | 863 | 每個 agent 在 Config struct 末尾加欄位，resolveSecrets 末尾加解析 |
| `http.go` | 4,152 | 每個 agent 在 endpoint 區塊末尾加路由（不改現有） |
| `main.go` | ~500 | CLI routing: 加 case 到 switch，daemon init: 加到末尾 |
| `tool.go` | 992 | registerBuiltins() 末尾加註冊 |
| `completion.go` | ~100 | 加 subcommand 到 list |

**規則:**
1. **Append-only**: 永遠在末尾新增，不修改現有行
2. **Comment markers**: 每個 sub-phase 加 `// --- P13.x: Feature Name ---` 區隔
3. **同一 round 的兩個 agent 不能修改同一個 shared file 的同一區段**

---

## 3. Phase Plan — P13-P16

### Overview

| Phase | Name | Sub-phases | Est. Lines | Rounds |
|-------|------|-----------|------------|--------|
| P13 | Plugin & Foundation | 4 | ~2,500 | R1-R2 |
| P14 | Discord Enhancement + Task Board | 6 | ~3,000 | R3-R5 |
| P15 | Channel Expansion | 5 | ~3,000 | R3-R7 |
| P16 | Advanced & Companion | 7 | ~3,200 | R8-R10 |
| **Total** | | **22** | **~11,700** | **10 rounds** |

### P13: Plugin & Foundation (~2,500 lines)

#### P13.1: Plugin System (~1,000 lines)
- **新檔案**: `plugin.go`, `plugin_test.go`
- **修改**: `config.go` (PluginConfig), `http.go` (/api/plugins), `main.go` (plugin CLI)
- **核心**: PluginHost, PluginProcess (JSON-RPC over stdin/stdout)
- **依賴**: 無。P13.2, P16.1 依賴此。

#### P13.2: Sandbox Plugin (~700 lines)
- **新檔案**: `sandbox.go`, `sandbox_test.go`, `cmd/tetora-plugin-docker-sandbox/main.go`
- **修改**: `dispatch.go` (sandbox routing), `config.go` (sandbox policy)
- **核心**: SandboxManager + Docker plugin binary stub
- **依賴**: P13.1

#### P13.3: Nested Sub-Agents (~400 lines)
- **修改**: `dispatch.go` (depth tracking), `agent_comm.go` (spawn control), `config.go` (maxDepth)
- **核心**: 可配置 spawn 深度、maxChildrenPerAgent、depth-aware tool policy
- **依賴**: 無

#### P13.4: Image Analysis (~400 lines)
- **新檔案**: `tool_vision.go`, `tool_vision_test.go`
- **修改**: `tool.go` (register), `config.go` (VisionConfig)
- **核心**: image_analyze tool (Anthropic/OpenAI/Google Vision API)
- **依賴**: 無

### P14: Discord Enhancement + Task Board (~3,000 lines)

#### P14.1: Discord Components v2 (~700 lines)
- **修改**: `discord.go` (大幅新增 component types + interaction handler)
- **新增**: Component struct types, interaction webhook handler, button/select/modal builders
- **核心**: Agent 可發送互動式 UI (buttons, selects, modals, file blocks)
- **依賴**: 無

#### P14.2: Thread-Bound Sessions (~500 lines)
- **修改**: `discord.go` (thread session routing), `dispatch.go` (session isolation)
- **核心**: 每個 thread 綁定獨立 session, /focus /unfocus 指令
- **依賴**: P14.1 (components for focus UI)

#### P14.3: Lifecycle Reactions (~300 lines)
- **修改**: `discord.go` (reaction manager)
- **核心**: queued/thinking/tool/done/error 各 phase 的 emoji 反應
- **依賴**: 無

#### P14.4: Discord Forum Task Board (~600 lines)
- **修改**: `discord.go` (forum thread creation + tag management)
- **核心**: Forum channel = Kanban board, tags = status, auto-thread creation
- **依賴**: P14.2 (thread sessions), P14.3 (reactions)

#### P14.5: Discord Voice Channel (~400 lines)
- **修改**: `discord.go` (voice state, /vc command)
- **核心**: 加入/退出語音頻道, auto-join, TTS 整合
- **依賴**: P12.4 (Voice Engine, already done)

#### P14.6: Built-in Task Board API (~500 lines)
- **新檔案**: `taskboard.go`, `taskboard_test.go`
- **修改**: `http.go` (/api/tasks), `config.go` (TaskBoardConfig)
- **核心**: Kanban API (create/move/assign/comment), DAG dependencies, webhook events
- **依賴**: 無 (獨立的 HTTP API，Discord Forum Board 是它的一個 frontend)

### P15: Channel Expansion (~3,000 lines)

每個 channel 結構相同: `{channel}.go` + `{channel}_test.go` + config/http/main/notify/completion 修改

#### P15.1: LINE (~600 lines)
#### P15.2: Matrix (~600 lines)
#### P15.3: Teams (~700 lines)
#### P15.4: Signal (~600 lines)
#### P15.5: Google Chat (~500 lines)

### P16: Advanced & Companion (~3,200 lines)

#### P16.1: Browser Automation Plugin (~800 lines)
- **外部 binary**: `cmd/tetora-plugin-browser/main.go`
- **核心**: CDP protocol, headless Chrome, 7 browser_* tools
- **依賴**: P13.1 (Plugin System)

#### P16.2: Voice Realtime (~1,000 lines)
- **新檔案**: `voice_realtime.go`, `voice_realtime_test.go`
- **核心**: Wake word + Talk Mode (OpenAI Realtime API relay)
- **依賴**: P12.4 (Voice Engine)

#### P16.3: Prompt Injection Defense v2 (~500 lines)
- **新檔案**: `injection.go`, `injection_test.go`
- **核心**: L1 static + L2 structured wrapping + L3 LLM judge
- **依賴**: 無

#### P16.4: Multi-agent Routing v2 (~500 lines)
- **修改**: `route.go`, `config.go`
- **核心**: Binding rules (channel/user → agent), priority over keyword routing
- **依賴**: 無

#### P16.5: Desktop App (~400 lines)
- **新目錄**: `companion/desktop/`
- **獨立 build target**, 不影響主 binary

#### P16.6: Mobile Shell (~300 lines)
- **新目錄**: `companion/mobile/`
- **獨立 project** (Capacitor template)

#### P16.7: QoL — Daily Notes + Skill Env + Dynamic Injection (~600 lines)

---

## 4. Round Execution Matrix

### Task Distribution Rules

1. **同一 round 的兩個 task 不能都大量修改 discord.go**
2. **Discord 相關 task 固定走 Alpha** (discord.go 的修改永遠在同一 branch，避免 cross-branch conflict)
3. **Channel/Tool task 走 Beta** (各 channel 互相獨立)
4. **Alpha 優先做 platform/discord，Beta 優先做 tool/channel**

### Matrix

| Round | Alpha (Platform/Discord) | Beta (Channel/Tool) | Conflict Risk |
|-------|--------------------------|---------------------|---------------|
| **R1** | P13.1 Plugin System | P13.4 Image Analysis | LOW — 都是新檔案為主 |
| **R2** | P13.2 Sandbox Plugin | P13.3 Nested Sub-Agents | MED — 都改 dispatch.go (不同 section) |
| **R3** | P14.1 Discord Components v2 | P15.1 LINE Channel | LOW — discord.go vs line.go |
| **R4** | P14.2 Thread-Bound Sessions | P15.2 Matrix Channel | LOW — discord.go vs matrix.go |
| **R5** | P14.3 Reactions + P14.4 Forum Board | P15.3 Teams Channel | LOW — discord.go vs teams.go |
| **R6** | P14.5 Discord Voice | P15.4 Signal Channel | LOW — discord.go vs signal.go |
| **R7** | P14.6 Task Board API | P15.5 Google Chat | LOW — taskboard.go vs gchat.go |
| **R8** | P16.1 Browser Plugin | P16.3 Injection Defense v2 | LOW — 都是新檔案 |
| **R9** | P16.2 Voice Realtime | P16.4 Routing v2 | LOW — voice_realtime.go vs route.go |
| **R10** | P16.5 Desktop + P16.6 Mobile | P16.7 QoL | NONE — companion/ 是獨立目錄 |

### Conflict Analysis

- **R1-R10 total conflicts expected**: ~10-15 regions（主要在 config.go, http.go, main.go 的 append）
- **Per-round conflicts**: 1-2 regions max（可在 2-3 分鐘內解決）
- **discord.go conflicts**: ZERO（全部走 Alpha，不會 cross-branch）
- **vs P10-P12**: 減少 ~80% 衝突（因為 merge-per-round + 固定分工）

---

## 5. Subagent Dispatch Protocol

### 5.1 Prompt Template

每個 subagent 收到的 prompt 必須包含以下結構:

```
## Task
{sub-phase ID 和名稱}

## Spec
{從 tetora-roadmap-v4.md 節錄的該 sub-phase 完整 spec}

## Working Directory
{~/tetora-alpha/ 或 ~/tetora-beta/}

## Files to Create
{新檔案清單}

## Files to Modify (APPEND-ONLY)
{修改的檔案 + 具體要加在哪個位置}

## Existing Patterns (MUST FOLLOW)
- Package: `package main` (所有 .go 檔都在 main package)
- DB: `queryDB()` returns `([]map[string]any, error)` — 不要忘記 error return
- Logging: `logInfoCtx(ctx, msg, key, val, ...)` — 第一個 arg 是 context.Context
- Config: $ENV_VAR resolution in `resolveSecrets()`
- HTTP: `mux.HandleFunc(path, handler)` pattern in `startHTTPServer()`
- Tools: register in `registerBuiltins()` with `ToolHandler` type
- Tests: `go test -run TestXxx -v` 確認通過
- Build: `go build ./...` 確認通過

## Config Additions
{該 feature 需要加到 Config struct 的欄位，連同 JSON tag}

## Commit Message
P{X}.{Y}: {Feature Name}

## Quality Checklist
1. go build ./... PASS
2. go test -run Test{Feature} -v PASS (all tests)
3. No duplicate type/func declarations
4. No unused imports
5. Comment marker: `// --- P{X}.{Y}: {Name} ---`
6. Commit with Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>
```

### 5.2 Context Size Guidelines

| Sub-phase Size | Prompt Tokens | Agent Model |
|---------------|---------------|-------------|
| Small (< 500 lines) | ~3,000 | sonnet |
| Medium (500-800 lines) | ~4,000 | sonnet |
| Large (> 800 lines) | ~5,000 | sonnet |

### 5.3 What NOT to Include in Prompt

- 整份 roadmap (太大)
- 不相關的 sub-phase spec
- 其他 agent 的工作內容
- 完整的 http.go 或 discord.go (太大, agent 自己讀)

### 5.4 What to ALWAYS Include

- 該 sub-phase 的完整 spec (從 roadmap 節錄)
- startHTTPServer 的當前 signature (因為經常改)
- Config struct 裡已有的相關欄位 (避免重複宣告)
- 相關的 existing function signatures (避免 type mismatch)

---

## 6. Merge Protocol

### 6.1 Standard Merge (每 round 執行)

```bash
# Alpha merge
cd ~/.tetora
git merge feat/alpha --no-edit
# 如果有衝突:
#   1. 檢查衝突區域
#   2. 保留 BOTH sides (通常是 append，兩邊加的不同)
#   3. go build ./... 驗證
#   4. git add -A && git commit

# Beta merge
git merge feat/beta --no-edit
# 同上

# 驗證
go build ./...

# Rebase worktrees
cd ~/tetora-alpha && git rebase master
cd ~/tetora-beta && git rebase master
```

### 6.2 Conflict Resolution Rules

| File | Resolution Strategy |
|------|-------------------|
| `config.go` (struct fields) | 保留兩邊的欄位（都是 append to struct） |
| `config.go` (resolveSecrets) | 保留兩邊的 secret resolution |
| `http.go` (routes) | 保留兩邊的 route handlers |
| `http.go` (startHTTPServer signature) | 合併參數列表 |
| `main.go` (CLI switch) | 保留兩邊的 case |
| `main.go` (daemon init) | 保留兩邊的 init calls |
| `tool.go` (registerBuiltins) | 保留兩邊的 tool registration |
| `discord.go` | 不應衝突（只有 Alpha 改）|
| `tetora` / `bin/tetora` | `git checkout --theirs` (binary) |

### 6.3 Post-Merge Verification

```bash
# 必須全部通過:
go build ./...
go test ./... -count=1 -timeout 60s 2>&1 | tail -20
```

---

## 7. Error Recovery

### 7.1 Agent Build Failure

```
1. 讀 agent output 找 error
2. 常見原因:
   a. Missing import → 加 import
   b. Duplicate type → 刪除重複
   c. Wrong function signature → 修正
3. 在 worktree 裡手動修復 → commit → 繼續 merge
4. 不要重新 dispatch（浪費 tokens）
```

### 7.2 Merge Conflict Too Complex

```
1. 如果衝突超過 5 個區域 → 停下來人工審查
2. 不要 force resolve → 可能破壞功能
3. 考慮 revert 其中一個 merge，手動整合
```

### 7.3 Agent Timeout / Crash

```
1. 檢查 agent output (background task output)
2. 如果有部分完成:
   a. 保留已完成的 code
   b. 手動完成剩餘部分
3. 如果完全失敗:
   a. Reset worktree: git reset --hard master
   b. 重新 dispatch（調整 prompt if needed）
```

### 7.4 Context Compaction

```
當 Opus context 接近 limit:
1. Session 會自動 compress
2. 繼續工作 — 所有 round 狀態在 git log 裡
3. 如果開新 session:
   a. 讀 MEMORY.md 和 dev-status.md
   b. git log --oneline 看進度
   c. 從上次的 round 繼續
```

---

## 8. Session Start Checklist

每次新 session 開始時，Opus 必須:

```
□ 讀 MEMORY.md (自動載入)
□ 讀 dev-status.md
□ 讀 dev-execution-guide.md (本文件)
□ git log --oneline -5 (確認最新進度)
□ git status (確認 working tree clean)
□ 確認 worktrees 存在且在 master HEAD
□ 確認下一個 round 是哪一個
□ 開始分派
```

---

## 9. Known Subagent Pitfalls (從 P10-P12 累積)

| # | Pitfall | Prevention |
|---|---------|------------|
| 1 | `queryDB()` 忘記 error return | Prompt 裡明確提醒 |
| 2 | `logInfoCtx` 忘記 ctx 參數 | Prompt 裡給 example |
| 3 | 重複宣告已存在的 type | Prompt 裡列出 existing types |
| 4 | `startHTTPServer` signature 不對 | Prompt 裡給當前 signature |
| 5 | Binary conflict (tetora, bin/tetora) | Always `git checkout --theirs` |
| 6 | Test helper 重複 (containsString etc.) | 提醒用 strings.Contains |
| 7 | Rate limit test 太嚴格 | 設 MaxPerMin 高一點 |
| 8 | 忘記在 completion.go 加 subcommand | Prompt 裡明確要求 |
| 9 | Import 遺漏 (尤其 "fmt", "strings") | Agent 自己 build 會抓到 |
| 10 | discord.go 的 WebSocket frame 格式 | 給 existing struct/const reference |

---

## 10. Progress Tracking

### dev-status.md 更新格式

每 round 完成後更新:

```markdown
### P13: Plugin & Foundation
| Sub-phase | Status | Commit | Lines | Round |
|-----------|--------|--------|-------|-------|
| P13.1 Plugin System | ✅ | abc1234 | ~1,000 | R1 |
| P13.2 Sandbox Plugin | ✅ | def5678 | ~700 | R2 |
```

### MEMORY.md 更新

每個 session 結束前更新 Quick Resume section.

---

## 11. Execution Summary

```
Total: 22 sub-phases across 10 rounds
Parallelism: 2 agents per round (Alpha + Beta)
Git: 2 worktrees + merge-per-round
Est. time: ~100-150 min (2-3 sessions)
Est. lines: ~11,700 new lines
Final codebase: ~75,000 lines, ~200 .go files

Strategy:
  Alpha = Platform/Discord (discord.go 獨佔)
  Beta = Channel/Tool (獨立新檔案為主)
  Merge after every round (小衝突, 快速解決)
```

---

## Appendix A: File Modification Map

| File | R1 | R2 | R3 | R4 | R5 | R6 | R7 | R8 | R9 | R10 |
|------|----|----|----|----|----|----|----|----|----|----|
| config.go | A+B | A+B | A+B | A+B | A+B | A+B | A+B | B | B | B |
| http.go | A | A | — | — | — | — | A+B | — | — | — |
| main.go | A | — | B | B | B | B | B | — | — | — |
| tool.go | — | — | — | — | — | — | — | A | — | — |
| discord.go | — | — | A | A | A | A | — | — | — | — |
| dispatch.go | — | A+B | — | A | — | — | — | — | — | — |
| completion.go | A | — | B | B | B | B | B | — | — | — |

A = Alpha modifies, B = Beta modifies, A+B = both modify (append-only, different sections)

---

## Appendix B: startHTTPServer Signature Evolution

Track the growing parameter list:

```
P12 (current): (addr, state, cfg, sem, cron, secMon, mcpHost,
    proactiveEngine, groupChatEngine, voiceEngine, slackBot, whatsappBot)

After P13: + pluginHost
After P14: + taskBoard
After P15: + lineBot, matrixBot, teamsBot, signalBot, gchatBot
After P16: (no change — browser/voice/injection are not HTTP server params)
```

**Refactor consideration**: 參數超過 15 個時，考慮改用 `ServerDeps` struct 封裝。
建議在 P14 結束後評估是否需要 refactor。

---

## Appendix C: Tool Code Mode (Cloudflare Pattern)

> 參考: Cloudflare MCP Server — 2,500 endpoints 從 117 萬 tokens 壓到 1,000 tokens

### 原理

不把所有 tool 定義塞進 context，改用 2 個 meta-tool:
- `search_tools`: 搜尋可用 tools (by keyword)
- `execute_tool`: 按名稱執行任意 tool

### Tetora 實作 (整合到 P13.1)

```
Core tools (直接暴露, ~2,400 tokens):
  exec_command, read_file, write_file, web_search, web_fetch,
  memory_search, agent_dispatch

Extended tools (Code Mode, ~800 tokens fixed):
  search_tools → 搜尋 tool registry
  execute_tool → 執行任意 registered tool

Plugin tools → 永遠走 Code Mode
```

### Token 節省

| Tools count | Traditional | Code Mode | Savings |
|-------------|-----------|-----------|---------|
| 14 (now) | 4,200 | 3,200 | 24% |
| 30 (P14) | 9,000 | 3,200 | 64% |
| 50 (P16) | 15,000 | 3,200 | 79% |
| 100+ (plugins) | 30,000+ | 3,200 | 89%+ |

---

## Appendix D: Opus-User 對話 Token 節省規則

> 這些規則適用於 Opus orchestrator 與 user 的對話 session。
> 目標: 減少 Messages token 佔比，延長 session 壽命，減少 compaction 次數。

### D.1 Output 規則 (Opus 遵守)

| 規則 | 說明 |
|------|------|
| **寫檔不貼文** | 大量內容 (roadmap, spec, code) 寫到檔案，chat 裡只說「已寫入 {path}」|
| **回覆 ≤ 15 行** | 除非 user 要求詳細解釋，否則回覆控制在 15 行以內 |
| **不回顯檔案內容** | 寫入檔案後不要在 chat 重複檔案內容 |
| **不重複 spec** | Reference `file:line` 而不是 copy-paste roadmap 內容 |
| **Agent 結果精簡** | 報告 agent 結果只需: status + commit hash + lines changed |
| **Stale notifications** | 一行回應 (「已完成，已 merge」) |

### D.2 Research 規則

| 規則 | 說明 |
|------|------|
| **Web 研究用 subagent** | WebSearch/WebFetch 放在 Task(Explore) 裡，結果寫到檔案 |
| **不在主 context 做多輪 search** | 一輪不夠就開 subagent |
| **結果摘要 ≤ 5 行** | Subagent 結果只取精華放入 chat |

### D.3 Session 管理

| 規則 | 說明 |
|------|------|
| **50% context 時 /compact** | 主動提醒 user |
| **狀態寫 dev-status.md** | 每 round 結束更新，不依賴 chat history |
| **Session 開始讀 memory** | 不需要 user 重複交代 context |
| **Background agents** | 能 background 的就 background，不佔主 context |

### D.4 Dispatch 規則

| 規則 | 說明 |
|------|------|
| **Prompt 寫到暫存檔** | 大 prompt 先 Write 到 /tmp，再用 Read 餵給 subagent |
| **Agent 結果不全貼** | 只取 commit hash + test result + error (if any) |
| **並行 dispatch** | 兩個 agent 同一個 message block 發出 |
