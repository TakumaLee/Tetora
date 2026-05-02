# Tetora 大瘦身 PR1：ARCHIVE life-stack 交接 Prompt

> 交給 Claude Code 執行。背景已經由 Cowork 跟主人討論完成、worktree 已開好、計畫已寫死。
> 你（Claude Code）的任務是**在這個 worktree 內把 PR1 做到 commit 完成**，過程中按 Phase 停下來等主人確認。

---

## 1. 你工作的地方

```
worktree:  /Users/vmgs.takuma/Workspace/Projects/01-Personal/tetora/.claude/worktrees/archive-life-stack-2026-05
branch:    chore/archive-life-stack-2026-05  (從 main d5a8e1e 開出)
base:      main
```

**第一件事：cd 進這個 worktree**。後續所有 git/build/edit 操作都在裡面做。不要動主 worktree。

**第二件事：在這個 worktree 內，先閱讀 `tasks/archive-plan-2026-05-02.md`**——那是 Cowork 跟主人協作產出的完整瘦身決策表，包含 KEEP / EXTRACT / FOLD / ARCHIVE / DELETE 五類分類、依賴分析、與 PR1 範圍劃定。

---

## 2. 為什麼做這件事（背景速覽）

主人覺得 Tetora 過於膨脹（679MB / 896 個 .go 檔 / 85 個 internal 套件 / root 層 24 個 .go 共 84,833 行），且自己列出的 5 個真實需求（PR review、Tasks/Workflow、Files 創作審查、戰情室、Discord 助理）跟現有 codebase 的 80 個套件大多無關。

Cowork 跟主人協作完成審核後決定：方向 a + c（砍冗餘 + 拆衛星服務）。PR1 是第一波——把 **個人生活助理 stack（life + automation + integration + 相關 tool/httpapi 包裝）** 移出 `internal/` 到 `_archive/`，但保留 `storage/`（FileManager，主人需求 #3 用）。

利用 Go 慣例「`_*` 開頭的目錄會被 `go build ./...` 自動忽略」，**不需要 build tag**。還原代價是一條 `git mv`。

---

## 3. 在動 wire.go 之前的強制步驟（CLAUDE.md §「Regression Guard」要求）

主人 CLAUDE.md 寫：

> **核心檔案改動前，必讀 `~/.tetora/workspace/tasks/fragile-points.md`。**
> 1. 改動前：讀 fragile-points.md，檢查即將改的檔案有沒有 fragile point
> 2. 改動後：如果命中 fragile point → 執行該 point 的「驗證方法」
> 3. 修 bug 後：登記到 fragile-points.md（必做）
> 4. 適用檔案：dashboard.html、tool_*.go、dispatch.go、session.go、taskboard_dispatch.go、main.go

**本 PR1 會碰到 wire.go / main.go / tool.go**，所以**第一個動作是 read `~/.tetora/workspace/tasks/fragile-points.md`**，把跟這三個檔有關的條目摘出來，列在 PR description 裡，並在 Phase 5 驗證時逐條跑「驗證方法」。

如果 fragile-points.md 沒有對應條目，繼續往下走。如果有，把它們連同此次的處理寫進 PR description。

---

## 4. PR1 範圍（最終版）

### 4.1 KEEP（不要移）

- `internal/storage/`——這是主人需求 #3「files 創作審查」的 FileManager。`storage.Service` / `globalFileManager` / `newFileManagerService` 都靠它。

### 4.2 MIGRATE（小遷移）

`internal/life/lifedb/lifedb.go` 是 25 行 type-only adapter（`QueryFn`、`ExecFn`、`EscapeFn`、`LogFn`、`DB`），目前被 `storage/` 與 11 個 life/* 子套件共用。因為 life/* 整批要 ARCHIVE，但 `storage/` 要留，所以把 lifedb 的 25 行 type 定義**搬到 `internal/storage/dbtypes.go`**，重命名：

- `lifedb.QueryFn`  → `storage.QueryFn`
- `lifedb.ExecFn`   → `storage.ExecFn`
- `lifedb.EscapeFn` → `storage.EscapeFn`
- `lifedb.LogFn`    → `storage.LogFn`
- `lifedb.DB`       → `storage.DBHelpers`（改名避免跟 internal/db 套件混淆）

然後改 `internal/storage/files.go` 用新 type，改 `wire.go` 的 `makeLifeDB()` → `makeStorageDB()`，回傳型別 `storage.DBHelpers`。

### 4.3 ARCHIVE（移到 `_archive/life-stack-2026-05/`）

**目錄整批移動（3 個）**：

```
internal/life/         → _archive/life-stack-2026-05/internal/life/
internal/automation/   → _archive/life-stack-2026-05/internal/automation/
internal/integration/  → _archive/life-stack-2026-05/internal/integration/
```

**單檔移動（12 個）**：

```
internal/tool/life_contacts.go        → _archive/life-stack-2026-05/internal/tool/life_contacts.go
internal/tool/life_finance.go         → _archive/life-stack-2026-05/internal/tool/life_finance.go
internal/tool/life_goals.go           → _archive/life-stack-2026-05/internal/tool/life_goals.go
internal/tool/life_habits.go          → _archive/life-stack-2026-05/internal/tool/life_habits.go
internal/tool/life_pricewatch.go      → _archive/life-stack-2026-05/internal/tool/life_pricewatch.go
internal/tool/life_timetracking.go    → _archive/life-stack-2026-05/internal/tool/life_timetracking.go
internal/tools/life.go                → _archive/life-stack-2026-05/internal/tools/life.go
internal/tools/integration.go         → _archive/life-stack-2026-05/internal/tools/integration.go
internal/httpapi/contacts.go          → _archive/life-stack-2026-05/internal/httpapi/contacts.go
internal/httpapi/family.go            → _archive/life-stack-2026-05/internal/httpapi/family.go
internal/httpapi/habits.go            → _archive/life-stack-2026-05/internal/httpapi/habits.go
internal/httpapi/reminders.go         → _archive/life-stack-2026-05/internal/httpapi/reminders.go
```

### 4.4 EDIT（拆 import 與 symbol references）

| 檔案 | 拆掉的 import 數 | 預估改動 |
|---|---|---|
| `wire.go` (12,149 行) | 26 個（保留 storage） | **~169 個 symbol references**（最痛）+ `makeLifeDB()` 改名 |
| `wire_test.go` (20,308 行) | 2 個（保留 storage） | 對應 test setup |
| `tool.go` (3,742 行) | 2 個 | tool init 區塊 |
| `tool_test.go` (9,349 行) | 5 個 | test setup |
| `main.go` (4,155 行) | 2 個（保留 storage） | init/shutdown 呼叫 |
| `internal/config/config.go` | 6 個 | type alias（lines 60-65: `GmailConfig` / `SpotifyConfig` / `TwitterConfig` / `PodcastConfig` / `HomeAssistantConfig` / `NotesConfig`）+ 對應 import |
| `internal/lifecycle/lifecycle.go` | 4 個 | init/shutdown hooks |
| `internal/storage/files.go` | `lifedb` 換成 `storage` 自己的 type | 同 §4.2 遷移 |

### 4.5 不在本 PR1 範圍

- `internal/oauth/`——主要被 integration 用，但 Discord 也吃。等 PR2 拆 Discord 時再處理。
- root 層 24 個 .go 檔的瘦身——獨立大工程。
- `.claude/worktrees/agent-a6b116d0/`、`*.test` 二進位、`cover.out`——獨立 PR。
- `tool/` vs `tools/` / `store/` vs `storage/` 命名重整——獨立 PR。

---

## 5. Phase 序（按順序執行，每個 Phase 做完停下來等主人確認）

### Phase 1：lifedb → storage 小遷移

1. 建立 `internal/storage/dbtypes.go`（25 行）
   - 內容是把 `internal/life/lifedb/lifedb.go` 的 type 定義搬過來，改 package name 與 type name（見 §4.2）
2. 改 `internal/storage/files.go`：
   - import 移除 `tetora/internal/life/lifedb`
   - 把 `lifedb.DB` 改為 `storage.DBHelpers`（**注意**：`files.go` 自己 `package storage`，所以不需要前綴）
   - 把 `lifedb.QueryFn` 等改為 `storage.QueryFn` 等（同樣不需要前綴）
3. 改 `wire.go`：
   - `makeLifeDB()` 函式改名為 `makeStorageDB()`
   - 回傳型別 `lifedb.DB` 改為 `storage.DBHelpers`
   - 對應的呼叫點同步改名
4. 在 worktree 內 `go build ./internal/storage/...` 驗證
5. **停下來，回報「Phase 1 完成」並等確認**

### Phase 2：建立 `_archive/` 並 git mv 整批

1. 建立目錄結構：
   ```bash
   mkdir -p _archive/life-stack-2026-05/internal/{tool,tools,httpapi}
   ```
2. 寫 `_archive/life-stack-2026-05/RESTORE.md`，內容包含：
   - 為何 ARCHIVE（一段背景）
   - 還原指令範例（`git mv _archive/life-stack-2026-05/internal/life internal/`）
   - 提醒還原時要記得把 lifedb 的 type 從 storage 搬回來
   - ARCHIVE 日期
3. 一次 `git mv` 全部 §4.3 列出的目錄與單檔
4. 確認 `_archive/` 沒被 `go build ./...` 編譯（理論上 `_*` 開頭目錄會被忽略）
5. **停下來，回報「Phase 2 完成」並等確認**

### Phase 3：拆 wire.go（最痛的一步）

1. 移除 `wire.go` 對下列 26 個套件的 import（保留 `tetora/internal/storage`）：
   - `tetora/internal/automation/insights`
   - `tetora/internal/integration/{drive,dropbox,gmail,homeassistant,notes,oauthif,podcast,spotify,twitter}`（9 個）
   - `tetora/internal/life/{calendar,contacts,dailynotes,family,finance,goals,habits,lifedb,pricewatch,profile,reminder,tasks,timetracking}`（13 個）
   - `tetora/internal/lifecycle`（如果只剩 life-stack 在用）
2. 找出 `wire.go` 內所有 `~169` 個 symbol references（用 `grep -n` 列出 line + context），分類：
   - **Init code**：`xxx.New(...)` / `xxx.InitDB(...)` 之類的初始化呼叫
   - **Field 引用**：global 或 struct field 帶 life-stack type
   - **Handler register**：tool / HTTP handler 的註冊
   - **Shutdown**：對應的清理
3. 對每一類，**整段刪除**（不是註解掉，是真刪），同時刪除：
   - 引用這些 symbol 的 helper functions
   - 引用這些 symbol 的 struct fields（例如 `globalContactsService` / `globalFamilyService` 等）
4. 跑 `go build ./...`——預期會壞，照錯誤訊息一個個修
5. 跑 `go vet ./...`
6. **停下來，回報「Phase 3 完成」並等確認**（提供 wire.go 的 line count 變化前後）

### Phase 4：拆其他 6 個 entangled 檔

按以下順序，每改一個檔跑一次 `go build ./...`：

1. **`main.go`**：
   - 移除 `tetora/internal/automation/briefing`、`tetora/internal/automation/insights` 兩個 import（保留 `tetora/internal/storage`）
   - 找出 `briefing.X` / `insights.X` 的呼叫點，刪除整段 init/shutdown
   - line 500 的 `storage.InitDB(cfg.HistoryDB)` 保留
   - line 1572 的 `FileManager *storage.Service` 保留

2. **`tool.go`**：
   - 移除 `tetora/internal/automation/briefing`、`tetora/internal/life/reminder` 兩個 import
   - 找出 `briefing.X` / `reminder.X` 的呼叫點刪除

3. **`internal/lifecycle/lifecycle.go`**：
   - 移除 4 個 integration import（gmail/homeassistant/notes/podcast/spotify/twitter，共 6 個——可能我之前漏算）
   - 移除對應的 lifecycle hooks
   - 如果整個 `lifecycle` 套件只剩骨架沒有實質內容，整個套件也 ARCHIVE

4. **`internal/config/config.go`**：
   - 移除 lines 60-65 的 6 個 type alias（`GmailConfig` / `SpotifyConfig` / `TwitterConfig` / `PodcastConfig` / `HomeAssistantConfig` / `NotesConfig`）
   - 移除對應的 6 個 import
   - 檢查 Config struct 有沒有用到這些 type，如果有就連同 field 一起刪
   - 對應的 config.json schema 變更：如果 user config 有這幾個欄位，**保留欄位但變成 ignored**（避免破壞既有 user config 檔），透過 `json.RawMessage` 接收即可

5. **`tool_test.go`**：
   - 移除 5 個 life-stack import
   - 對應的測試函式整個刪除（不是 skip，是刪）

6. **`wire_test.go`**：
   - 移除 2 個 import（保留 `tetora/internal/storage`）
   - 對應的 testFileManagerService helper 保留（line 6916）
   - 其他 life-stack 相關測試函式刪除
   - **這是 20k 行的大檔，預期會花最多時間**

驗證：`go test ./internal/...`、`go test ./...`

7. **停下來，回報「Phase 4 完成」並等確認**

### Phase 5：最終驗證 + commit

1. 跑完整驗證：
   ```bash
   go build ./...
   go vet ./...
   go test ./internal/... -count=1
   go test . -count=1     # root 層測試
   ```
2. 如果有 fragile-points.md 條目命中，跑該 point 的「驗證方法」
3. 統計成果（給主人看）：
   - `internal/` 套件數變化：85 → ?
   - `wire.go` 行數變化：12,149 → ?
   - `_archive/life-stack-2026-05/` 移入的 LOC 與檔案數
   - 其他被改動的檔案行數變化
4. **整理 commit 訊息**（建議分 3~4 個原子 commits 而非單一巨型 commit）：
   - commit 1: `refactor(storage): inline lifedb types into storage package`
   - commit 2: `chore(archive): move life/automation/integration to _archive/life-stack-2026-05`
   - commit 3: `chore(wire): remove life-stack wiring from wire.go / main.go / tool.go`
   - commit 4: `chore(test): remove life-stack tests + config aliases`
5. 每個 commit 之間都要能 `go build ./...` 通過
6. 回報「PR1 完成」並等主人推 branch / 開 PR

---

## 6. 驗收條件

- [x] `go build ./...` 通過
- [x] `go vet ./...` 通過
- [x] `go test ./internal/...` 通過
- [x] `go test .` 通過（root 層測試）
- [x] `internal/` 不再有 life/automation/integration/storage 之外的 lifestack 子套件
- [x] `_archive/life-stack-2026-05/` 包含全部 ARCHIVE 目標 + 一份 RESTORE.md
- [x] PR1 不影響主人 5 個真實需求（PR review、Workflow、Files、戰情室、Discord）——具體驗證方式：
  - PR review：`tetora review --help` 能跑，`internal/cli/review.go` 與 `internal/review/` 完整
  - Workflow：`internal/workflow/` 完整
  - Files：`storage.Service` / `globalFileManager` 工作正常
  - 戰情室：`internal/warroom/` 完整、status.json 讀寫沒壞
  - Discord：`internal/discord/` 完整（雖然下個 PR 會拆走，本 PR 不動）
- [x] 主人 config.json 內若有 gmail / spotify / homeassistant 等欄位，不會因為 type alias 移除而 panic（用 `json.RawMessage` 接住）

---

## 7. 已知風險與暗礁

1. **`wire.go` 是 fragile-points.md 標記的脆弱檔**——主人 CLAUDE.md 列出來的脆弱檔清單是 dashboard.html / tool_*.go / dispatch.go / session.go / taskboard_dispatch.go / main.go。**wire.go 沒在這份清單裡，但它是 12k 行的中央裝配檔，動的時候要極小心**。Phase 3 結束後一定要跑完整 test。

2. **`internal/lifecycle/lifecycle.go` 的命運**——它依賴 4 個 integration package，把那些拆掉後可能整個套件變空殼。如果空殼，連同 ARCHIVE。如果還有 non-integration 用途，保留並縮減。

3. **`config.go` 的 6 個 type alias 移除**會讓既有 user 的 `config.json` 內 gmail / spotify 等欄位變成 unknown field。不能讓 `json.Unmarshal` 報錯。處理方式：保留欄位定義為 `json.RawMessage`，註解寫「archived in PR1」。

4. **如果 `internal/storage/files.go` 內部用了 lifedb 的更深 method**（不是只有 type），遷移時要連同 method 搬過來。讀完整個 `files.go` 確認所有 lifedb 用法。

5. **`_archive/` 目錄內的 .go 檔不會被 `go build ./...` 編譯，但會被 `go vet ./...` 看到嗎？**測試一下，如果 vet 抓到，加一個 `_archive/.gitignore` 不夠（已 track），考慮用 `//go:build ignore` build tag 雙保險（但這違背我們「不用 build tag」的目標——權衡看狀況）。

6. **`wire_test.go` 內 `testFileManagerService` helper（line 6916）保留**——但它呼叫的 `storage.InitDB`、`storage.New` 在 lifedb→storage 遷移後可能也要跟著改 signature。注意這個。

7. **fragile-points.md 看完後請 follow 它的指示**——如果 fragile-points 寫了「動 wire.go 之後要跑某個 integration test」，就跑。

---

## 8. 你不要做的事

- ❌ 不要動 `internal/discord/`（下個 PR）
- ❌ 不要動 `internal/oauth/`（下個 PR 跟 Discord 一起處理）
- ❌ 不要動 root 層的 24 個 .go 檔（除了 wire.go / main.go / tool.go / wire_test.go / tool_test.go 這 5 個本 PR 必動的）
- ❌ 不要清理 `.claude/worktrees/agent-a6b116d0/`（獨立 PR）
- ❌ 不要刪除 `*.test` 二進位 / `cover.out`（獨立 PR）
- ❌ 不要動命名重複（tool/tools、store/storage、scheduling/cron——獨立 PR）
- ❌ 不要 push branch（讓主人手動 push）
- ❌ 每個 Phase 做完都要停下來等主人確認，不要一路衝到 Phase 5

---

## 9. 完成後

PR1 完成 commit 後，回報以下三件事給主人：

1. 變更摘要（套件數、LOC、行數變化）
2. 完整 commit log
3. 建議的下一步 PR：
   - PR2：拆 Discord + OAuth 成衛星 binary (tetora-discord)
   - PR3：DELETE 30 個 AI 時代易重做套件
   - PR4：清理 `.claude/worktrees/agent-a6b116d0/` + 測試二進位 + cover.out
   - PR5：root 層 24 個 .go 檔的瘦身

---

## 10. 重要參考檔

主人在 worktree 內已經放了 `tasks/archive-plan-2026-05-02.md`——那是完整的瘦身計畫，含 KEEP / EXTRACT / FOLD / ARCHIVE / DELETE 五類所有套件分類、依賴分析、待主人確認的疑問清單。

如果你（Claude Code）在執行過程中發現 `archive-plan-2026-05-02.md` 跟本 prompt 有任何衝突，**以本 prompt 為準**（這份是修訂後最終版，archive-plan 是中間草稿）。

如果你發現 prompt 裡某個檔案路徑不存在或 import 數對不上實際情況，先停下來回報主人，不要自己改方向。

---

主人不在現場時的判斷準則：**保守 > 激進。寧可 PR 範圍小一點留下個 PR 處理，不要一次動太多以致回不去。每個 Phase 必停。**
