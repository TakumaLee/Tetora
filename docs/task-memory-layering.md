# Tetora 三層任務記憶派發架構

> **適用範圍**：Tetora v2 + Claude Code（或其他 CLI agent）+ 使用者自訂的 Tetora agents
> **配套規則檔**：`~/.tetora/workspace/rules/task-layering.md`（agent auto-inject）
>
> 本文描述 Tetora 如何把「任務」拆成三層儲存——session-bound todo、跨 session 結構化 ticket、永久長文件——以及它們如何與 agent 記憶系統（rules / skills / memory）互動。
>
> 文中提到的 agent 名稱（`hisui`、`ruri`、`hekigyoku` 等）是示範用的角色名，實際部署可依 `~/.tetora/agents/` 設定自訂。使用者本人以「使用者」指稱。

---

## 1. 為什麼要分層

任務資訊有 **三種差異極大的生命週期**：

| 差異 | 短命 | 中長 | 永久 |
|---|---|---|---|
| 存活時間 | 對話結束 | 直到 task 完成 | 永遠 |
| 結構化程度 | 非結構（自然語言 step） | 高（id/status/assignee）| 低（長文） |
| 查詢需求 | 無 | 高（assignee / status） | 中（grep / git log） |
| 派發自動化 | 無 | 有（auto-dispatcher） | 無 |
| 修改頻率 | 高 | 中 | 低（寫完基本固定） |

用一種格式承載全部 = 一定有一類被糟蹋：
- 全塞 Markdown → 無 status 無 assignee 無法 query，agent 無法自動派送
- 全塞 DB → DB 不是 filesystem，長文件 diff / grep / git blame 都痛苦
- 全塞 session → 任何跨 session 工作都會消失

所以：**一個工具解一個問題**。這是 Unix 哲學在任務管理上的應用。

---

## 2. 三層總覽

```
┌─────────────────────────────────────────────────────────────────┐
│                                                                 │
│  L1 ── Session Tasks ─── Claude Code TaskCreate                 │
│  ↑     當前對話內的步驟追蹤                                        │
│  ↑     生命週期：session-bound（對話結束就沒）                       │
│  │                                                              │
│  L2 ── Tetora Taskboard ── ~/.tetora/history.db tasks 表         │
│  ↑     跨 session 的結構化 ticket                                 │
│  ↑     生命週期：永久（直到手動 archive）                            │
│  ↑     CLI：tetora task {list|create|show|update|move|...}       │
│  │                                                              │
│  L3 ── Markdown 長文件 ─── tasks/*.md                            │
│        設計 / 計畫 / post-mortem / roadmap                        │
│        生命週期：永久（git 或 workspace 持久化）                      │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

**判斷流程**：

```
需要紀錄某事
    ↓
只在當前對話內追蹤？──── Yes ──→ L1（TaskCreate）
    ↓ No
跨 session / 有 owner / 有 due date？── Yes ──→ L2（tetora task create）
    ↓ No
需要 > 300 字的設計 / 分析？ ──── Yes ──→ L3（*.md）
    ↓ No
→ 一次性小事，口頭解決，不記錄
```

若同時符合 L2 + L3（例如：寫完 spec 要排 implementation ticket）：
- **主體放 L3**（spec.md 長文件）
- **L2 ticket 的 description 指向 L3 路徑**，不複製內容
- Ticket 完成時在 L3 尾部追加 outcome 段

---

## 3. L1 · Session Tasks

### 機制
- Claude Code 內建 `TaskCreate` / `TaskUpdate` / `TaskGet` / `TaskList` 工具
- 儲存在 Claude Code session state（`~/.claude/projects/*/..jsonl`）
- 每個 session 有獨立的 task list，跨 session 不共享

### 適用情境
```
「我先 grep 找出所有 foo，再 edit 檔案 A 和 B，最後 run test」
```
三步以上、當前對話內完成、不需要別人接手。

### 不適用
- 「下週二 review」→ L2
- 「請 hisui 跟進」→ L2
- 「寫一份 12-factor compliance 分析」→ L3

### 優點
- 幾乎零 overhead（tool call 即可）
- spinner 顯示當前 active task，UX 好
- 不污染長期記憶

### 限制
- **session 結束即可能消失**（至少對其他 session / agent 不可見）
- 沒有 assignee、priority、depends_on 等結構化欄位
- 無 query API（只能看當前 session task list）

### 常見陷阱
用 L1 開「下週請 agent X review」這種跨 session 任務——session 結束後 agent X 根本讀不到，時間到了沒人觸發。凡是「需要未來某天、由別人接手」的任務，一律走 L2。

---

## 4. L2 · Tetora Taskboard

### 儲存
- `~/.tetora/history.db` 的 `tasks` 表
- Schema：`id TEXT PK, project, title, description, status, assignee, priority, depends_on TEXT (JSON), type, parent_id, workflow, workdirs TEXT (JSON), cost_usd, duration_ms, session_id, model, created_at, updated_at, completed_at, retry_count`
- 上述欄位清單為撰寫當下的快照，日後若 schema 異動而本文未同步，**以 `tetora task show <id>` 的實際輸出為準**

### Status 狀態機
```
idea ─→ backlog ─→ todo ─→ running ─→ review ─→ done
  │        │                          │           (終態)
  │        │                          └─→ failed
  │        │                                │
  │        └──────────→ archived            └─→ todo（重試）
  │                     (終態)
  └──────→ archived（不可行）
```

詳細狀態轉移見 `~/.tetora/workspace/rules/task-lifecycle.md`。

### CLI
```bash
tetora task list                        # 所有
tetora task list --assignee=hisui       # 按 agent 過濾
tetora task list --status=todo          # 按狀態過濾
tetora task list --project=stock-trading
tetora task create --title="..." --assignee=hisui \
                   --description="..." --priority=high --type=chore
tetora task show <id>
tetora task show <id> --full            # 含 comment thread
tetora task update <id> --title="..." --description="..."
tetora task move <id> --status=todo     # backlog → todo 觸發 auto-dispatch
tetora task assign <id> --assignee=ruri
tetora task comment <id> --author=hisui --content="..."
tetora task thread <id>                 # 看整串 comments
```

### Auto-dispatcher（關鍵機制）
- 實作位置：`internal/taskboard/dispatcher.go`
- 輪詢 interval：預設 5 分鐘（`config.taskBoard.autoDispatch.interval`）
- 條件：`status='todo'` AND `assignee != ''` AND **無 blocking deps**
- 動作：對 assignee agent 發起 dispatch，session 開啟後把 task 改成 `running`
- 並行上限：`maxConcurrentTasks: 6`、`maxTasksPerAgent: 5`
- 完成後：agent 寫 `status=done/failed/review`，dispatcher 從池中移除

**想要 1 週後才跑**：建 ticket 時設 `status=backlog`（不會自動派），1 週後人工或 cron 執行 `tetora task move ID --status=todo` 觸發。

### 優點
- 結構化 → 可 `SELECT * FROM tasks WHERE assignee='hisui' AND status='todo'`
- Auto-dispatch → 使用者不用手動派送
- Discord thread integration（`discord_thread_id` 欄位）
- Comment thread（`task_comments` 表）保留討論歷史

### 限制
- description 長度理論無限制，但實務 > 500 字就該改放 L3
- `depends_on` 是 JSON 陣列，可表達先後順序但無 cross-project dep

---

## 5. L3 · Markdown 長文件

### 兩個慣例位置

| 路徑 | 用途 | 版本控制 |
|---|---|---|
| `tetora/tasks/*.md`（source 倉庫） | 專案級長文件：spec / plan / roadmap / architecture | git-tracked |
| `~/.tetora/workspace/tasks/*.md` | runtime 長文件：triage / dispatch report / lessons | 不進 git（workspace 是本機 state） |

### 命名慣例

| 前綴 | 用途 | 範例 |
|---|---|---|
| `spec-` | 功能規格 | `spec-*.md` |
| `plan-` | 實作計畫 | `plan-idle-trigger.md` |
| `roadmap-` | 長期路線圖 | `roadmap-workflow-ux.md` |
| `review-` | 事後回顧 | `review-scheduling-dispatch-2026-03-01.md` |
| `gap-analysis-` | 差距分析 | `gap-analysis-2026-02-23.md` |
| `autopsy-` | 失敗剖析 | `autopsy-correlation-arb-0W22L.md`（假想） |
| 日期後綴 | 時間敏感文件 | `workflow-changes-2026-04-18.md` |

### Memory 子系統（L3 的變形）
`~/.tetora/workspace/memory/` 是 agent 的記憶層，也是 markdown：

| 子路徑 | 用途 |
|---|---|
| `memory/daily/YYYY-MM-DD-*.md` | agent 每日產出（health report, scan summary） |
| `memory/observations/*.md` | agent 跨 session 觀察（HaluMem CRUD 紀律）|
| `memory/archive/` | 舊的 memory，半年以上移入 |

這些技術上也是 L3，但生命週期比 `tasks/*.md` 更動態（agent 會頻繁 read/write）。

### 適用情境
- 設計一個新功能 → `spec-*.md`
- 策略回顧 / 策略 autopsy → `autopsy-*.md` + L2 ticket 指向它
- 跨專案 roadmap → `roadmap-*.md`
- 週 / 月 retrospective → `review-YYYY-MM-DD.md`

### 不適用
- 3 行 todo 列表 → L2 create task
- session 內臨時筆記 → L1 TaskCreate 就夠了

---

## 6. 記憶系統整合

Tetora 的「任務記憶派發」不是只有 L1/L2/L3——它跟其他記憶層有雙向互動：

```
                     ┌─────────────────────┐
                     │   L1 Session Tasks  │
                     │   (Claude Code)     │
                     └─────────────────────┘
                              │ 完成後 summary 寫入
                              ↓
          ┌───────────────────────────────────┐
          │         memory/observations/       │  ←─ Agent 寫
          │         跨 session 共享            │
          └───────────────────────────────────┘
                     │             ↑
         提交入 L2    │             │ 提煉 3 次 → 升格
                     ↓             │
┌─────────────────────────────────────────────────┐
│   L2 Tetora Taskboard (history.db tasks)        │
│                                                 │
│   auto-dispatcher ──→ agent session ──→ output  │
│   (5m poll)                │                    │
│                            ├─→ 短報告 → L2 comment │
│                            └─→ 長報告 → L3 *.md    │
└─────────────────────────────────────────────────┘
                     ↑                    ↑
                     │                    │
        ┌────────────┴──┐       ┌─────────┴────────┐
        │  workspace/   │       │   workspace/     │
        │  rules/       │       │   skills/        │
        │  (規範 · 自動 │       │   (可重用程序 ·  │
        │   注入所有     │       │    prompt 匹配  │
        │   agent 提示) │       │    後注入)       │
        └───────────────┘       └──────────────────┘
                     ↑
                     │ occurrences ≥ 3 升格
          ┌──────────┴──────────┐
          │ memory/auto-lessons  │
          │   (lesson 原始紀錄)  │
          └──────────────────────┘
```

### 各層職責
| 層 | 性質 | 讀寫頻率 | 誰讀 | 誰寫 |
|---|---|---|---|---|
| **L1 session tasks** | ephemeral todo | 高 | 當前 Claude Code | Claude Code |
| **L2 taskboard** | 結構化 ticket | 中 | 所有 agent + dashboard | agent / 使用者 / dispatcher |
| **L3 tasks/\*.md** | 長文件（spec/plan） | 低 | agent / 使用者 | agent / 使用者 |
| **memory/observations** | 跨 agent 觀察 | 高 | agent | agent（有 CRUD 紀律）|
| **memory/daily** | agent 日報 | 日 | 使用者 + agent | agent |
| **rules/** | 規範 | 低 | 所有 agent（auto-inject） | 使用者 / agent 升格 |
| **skills/** | 可重用流程 | 中 | 相關 agent（prompt match 注入） | agent 產生 + 使用者核准 |
| **knowledge/** | 參考資料 | 低 | agent（50KB guard） | 使用者手動整理 |

### 升格管道（L1 → rules / skills）

```
L1 或 session 中出現新模式
    ↓
寫 lesson 到 memory/lessons-*.md（單次觀察）
    ↓
同模式重複 ≥ 3 次
    ↓
升格：
  - 治理性規範 → workspace/rules/XXX.md（auto-inject）
  - 可重用 procedure → workspace/skills/XXX.md（prompt match inject）
```

這是 `~/.claude/CLAUDE.md` §4 的 3x repeat threshold 原則。

---

## 7. 派發流程（Dispatch Flow）

### 7.1 人類手動派發
```
使用者 tetora dispatch "請 hisui 分析 binary_arb 訊號"
    ↓
daemon 直接 spawn agent session（不經 L2）
    ↓
agent 執行 → 寫 L3 report + memory observation
    ↓
完成後 session 記在 sessions table（history 用）
```
**特性**：立即執行、不排 queue、不需先建 L2 ticket。適合一次性指令。

### 7.2 經 L2 自動派發
```
使用者或 agent: tetora task create --title=X --assignee=hisui
    ↓
L2 tasks 表插入一筆，status=backlog（預設）
    ↓
使用者或 cron: tetora task move <id> --status=todo
    ↓
auto-dispatcher 下一輪 poll（5m）偵測到
    ↓
check: 無 blocking deps? 並行未滿? agent 未達上限?
    ↓ Yes
spawn agent session（注入 task description 當 prompt）
    ↓
狀態轉 running
    ↓
agent 執行 → 寫 L3 + comment 回 L2
    ↓
agent 結束：tetora task move <id> --status=done/failed/review
    ↓
（若 review → 使用者手動確認 → done）
```

**特性**：
- 可追蹤（tasks 表 query）
- 可排程（status=backlog 等觸發）
- 可鏈式（depends_on）
- 可 comment（討論歷史保留）

### 7.3 Agent 產出回饋
Agent 執行一個 L2 task 後，**通常** 產出：
1. **短結論** → `tetora task comment <id> --content="summary..."`
2. **長報告**（若有）→ 寫到 `~/.tetora/workspace/memory/daily/` 或 `tasks/*.md`，在 comment 中指向路徑
3. **觀察到的模式** → `memory/lessons-*.md`（供未來升格 rule / skill）

---

## 8. 典型情境

### 情境 A：跨週的策略觀察 review
```
[使用者] 請 Claude Code 擴大某策略，1 週後由 hisui review

Claude Code:
  1. edit 該策略 source（L1 TaskCreate 追蹤「改完再 build」）
  2. commit
  3. 重啟 daemon 吃新設定
  4. tetora task create --title="[<strategy>] 1 週觀察 review" \
                        --assignee=hisui --description="指定日期執行：..."
     ← L2 backlog，等到指定日期由使用者或 cron move 到 todo
  5. session 結束，L1 自動清

[一週後]
  使用者或 cron: tetora task move task-XXX --status=todo
  auto-dispatcher: 派送給 hisui
  hisui session:
    - grep 對應 log
    - 分析訊號頻率
    - 寫 ~/.tetora/workspace/memory/daily/<strategy>-review-YYYY-MM-DD.md（L3）
    - tetora task comment task-XXX --author=hisui \
        --content="訊號 X 筆，建議調整參數，詳見 <L3 路徑>"
    - tetora task move task-XXX --status=review
  使用者看 dashboard → 確認 → tetora task move --status=done
```

這個模式的關鍵是：L1 處理當下對話的步驟、L2 把未來的觸發點封裝成 ticket、L3 是 agent 跑完後才產出的長文件。三層各司其職、不重複存同一件事。

### 情境 B：寫架構文件（例如本文件）
```
[使用者] 請寫「tetora 三層任務記憶派發」的結構說明文件

Claude Code 判斷：
  - > 300 字說明 → L3
  - 需要 git-tracked（Tetora 架構文件永久參考） → tetora/docs/
  - 命名直觀（例如 task-memory-layering.md）
  ↓
Write docs/task-memory-layering.md
  ↓
commit 到 tetora 倉庫
```
**沒有 L2 ticket**，因為：這是當下對話產出，不需 agent 接手，也不需狀態追蹤。

### 情境 C：跨 agent 協作（scheduling dispatch）
```
ruri 發現新 feature 想法
    ↓
ruri: tetora task create --title="..." --assignee=hekigyoku --type=feat \
                          --description="spec: tetora/tasks/spec-XXX.md"
    ↓
（假設 spec 還沒寫）
ruri: 寫 tetora/tasks/spec-XXX.md（L3）
    ↓
ruri: tetora task move <id> --status=todo
    ↓
auto-dispatcher: 派給 hekigyoku
    ↓
hekigyoku 實作：
  - L1 TaskCreate 追蹤「先寫 test，再 impl，再 build」
  - 產出 code + commit
  - tetora task comment <id> --author=hekigyoku --content="實作完成，commit abc123"
  - tetora task move <id> --status=review
    ↓
使用者 / ruri review → done
```

---

## 9. 反模式（絕不做）

### ❌ 用 L1 做跨 session 追蹤
```
# 錯誤
Claude Code TaskCreate { subject: "下週請 hisui review" }
# 結果：session 結束，hisui 看不到，時間到了沒人想起

# 正確
tetora task create --title="..." --assignee=hisui
```

### ❌ 用 L3 做 ticket
```
# 錯誤
tasks/todo.md:
  - [ ] review <策略> (assignee: hisui, due 下週)
  - [ ] fix <module> bug
  - [ ] ... 50 條 todo 積了半年，沒人管 ...
# 結果：無 status、無 query、stale 後沒人敢刪

# 正確
tetora task create --title="review <策略>" --assignee=hisui
tetora task create --title="fix <module> bug" --priority=high
```

### ❌ 用 L2 塞長文件
```
# 錯誤
tetora task create --title="spec" --description="<3000 字 spec>"
# 結果：DB 可存，但 diff / grep / git blame 都痛苦

# 正確
Write tetora/docs/<feature>.md  （L3 長文件 + git）
tetora task create --title="implement spec" \
                    --description="see docs/<feature>.md"
```

### ❌ DM / Discord 口頭傳指令
```
# 錯誤
使用者 → hisui 私訊：「下週幫我看一下 <某事>」
# 結果：沒有任何層記錄，dispatch 無法追蹤，忘了就忘了

# 正確
tetora task create --title="..." --assignee=hisui （+ Discord notification）
```

### ❌ 同一事跨層重複
```
# 錯誤
Claude Code TaskCreate: "<task>"    ← L1
tetora task create: "<task>"        ← L2
tasks/todo.md 加一條: "<task>"      ← L3
# 結果：三層各一份，哪份是真的？

# 正確
只在一層。主體放 L2（結構化 ticket），若需長文分析時 L3 引用 L2 id。
```

---

## 10. 維護

### 自動維護
- **L1**：session 結束自動清，無需人為維護
- **L2**：
  - Archived tasks 保留永久（history.db 不自動清）
  - 失敗超過 `maxRetries` 的 task → `escalateAssignee: ruri` 接手
  - Stuck running > 15 min → 重置 todo
- **L3 (memory)**：
  - `memory/archive/` 子目錄供手動歸檔
  - 無自動過期機制

### 人工維護
季度檢查一次：
```bash
# L2 stale backlog（3 月未動）
tetora task list --status=backlog  # 人工審視
tetora task move <stale-id> --status=archived

# L3 stale md（半年未改）
find tetora/tasks -name "*.md" -mtime +180
# → 搬到 archive/ 或刪

# memory/ stale observations
# 由 agent 自己按 HaluMem 紀律處理
```

---

## 11. 開放問題（尚未解決）

1. **due_date 欄位**：目前 tasks 表沒有 `due_date` 欄位，1 週後觸發只能用 `status=backlog` + 人工 / cron 改 todo。若要精確：加 schema 欄位 + dispatcher 偵測 due_date ≤ now 自動 move。
2. **跨 client task visibility**：不同 client（`cli_default` / `cli_sentori` 等）的 tasks 各在獨立 DB，目前無 aggregate view。Dashboard 只顯示 default client。
3. **L3 auto-index**：`tasks/*.md` 越長越多，目前沒 agent 自動掃 + 建索引。未來可能需要 `tetora task index` 掃 md 前 matter 產 index。
4. **Memory vacuum**：`memory/observations/*.md` 累積會變巨大。HaluMem CRUD 紀律是規範，但沒自動化。未來考慮 compaction 工具。

---

## 12. 參考

- 規範（auto-inject）：`~/.tetora/workspace/rules/task-layering.md`
- 狀態機：`~/.tetora/workspace/rules/task-lifecycle.md`
- CLI 源碼：`dispatch.go` 的 `cmdTask`
- Engine 源碼：`internal/taskboard/engine.go`
- Auto-dispatcher：`internal/taskboard/dispatcher.go`
- Schema：`~/.tetora/history.db` tasks + task_comments 表

---

## Appendix A · 快速參考卡

```
L1 - Claude Code TaskCreate    對話內 3+ 步驟         session-bound
L2 - tetora task create        跨 session ticket      永久 queryable
L3 - tasks/*.md                長文件 (spec/plan)     永久 git-tracked

判斷：
  [ ] session 結束還需追蹤？ → Yes: L2/L3, No: L1
  [ ] > 300 字？              → Yes: L3, No: L2
  [ ] 有特定 agent 負責？    → Yes: L2 --assignee=X

CLI:
  tetora task list [--assignee=X] [--status=Y]
  tetora task create --title=X --assignee=Y --description=Z
  tetora task move <id> --status=todo    # 觸發 auto-dispatch
  tetora task comment <id> --author=X --content=Y
```
