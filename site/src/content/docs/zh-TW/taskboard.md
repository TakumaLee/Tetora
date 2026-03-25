---
title: "Taskboard 與自動派送指南"
lang: "zh-TW"
order: 4
description: "Track tasks, priorities, and agent assignments with the built-in taskboard."
---
# Taskboard 與自動派送指南

## 概述

Taskboard 是 Tetora 內建的看板系統，用於追蹤並自動執行任務。它結合了持久化任務儲存（以 SQLite 為後端）與自動派送引擎，後者會監看就緒任務並在無需人工介入的情況下將其交付給 agent。

典型使用情境：

- 排列一批工程任務的待辦清單，讓 agent 通宵處理
- 根據專業能力將任務路由至特定 agent（例如後端任務給 `kokuyou`，內容任務給 `kohaku`）
- 建立具有依賴關係的任務鏈，讓 agent 接續彼此的工作
- 整合 git 工作流程：自動建立分支、commit、push 並建立 PR/MR

**需求：** 在 `config.json` 中設定 `taskBoard.enabled: true`，並且 Tetora daemon 正在執行。

---

## 任務生命週期

任務依照以下順序流經各狀態：

```
idea → needs-thought → backlog → todo → doing → review → done
                                                  ↓
                                           partial-done
                                                  ↓
                                              failed
```

| 狀態 | 意義 |
|---|---|
| `idea` | 粗略概念，尚未細化 |
| `needs-thought` | 實作前需要分析或設計 |
| `backlog` | 已定義並排序優先級，但尚未排程 |
| `todo` | 準備執行——若已指派負責人，自動派送會接手 |
| `doing` | 目前執行中 |
| `review` | 執行完畢，等待品質審查 |
| `done` | 已完成並通過審查 |
| `partial-done` | 執行成功但後處理失敗（如 git merge 衝突）。可復原。 |
| `failed` | 執行失敗或輸出為空。將重試至 `maxRetries` 次。 |

自動派送只接手 `status=todo` 的任務。若任務無負責人，會自動指派給 `defaultAgent`（預設：`ruri`）。`backlog` 中的任務會由設定的 `backlogAgent`（預設：`ruri`）定期分類，並將有潛力的任務提升至 `todo`。

---

## 建立任務

### CLI

```bash
# 最簡任務（進入 backlog，無負責人）
tetora task create --title="Add rate limiting to API"

# 帶所有選項
tetora task create \
  --title="Refactor auth middleware" \
  --description="Split token validation into its own package. See ADR-14." \
  --priority=high \
  --assignee=kokuyou \
  --type=refactor

# 列出任務
tetora task list
tetora task list --status=todo
tetora task list --assignee=kokuyou
tetora task list --project=api-v2

# 顯示特定任務
tetora task show task-abc123
tetora task show task-abc123 --full   # 包含留言/討論串

# 手動移動任務
tetora task move task-abc123 --status=todo

# 指派給 agent
tetora task assign task-abc123 --assignee=kokuyou

# 新增留言（類型可為 spec、context、log 或 system）
tetora task comment task-abc123 \
  --author=takuma \
  --content="Must pass existing test suite. Do not touch auth.go." \
  --type=spec
```

任務 ID 自動以 `task-<uuid>` 格式生成。可以用完整 ID 或短前綴參照任務——CLI 會提示符合的選項。

### HTTP API

```bash
# 建立
curl -X POST http://localhost:8991/api/tasks \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Add rate limiting",
    "description": "Implement token bucket per API key",
    "priority": "high",
    "assignee": "kokuyou",
    "type": "feat"
  }'

# 列出（依狀態過濾）
curl "http://localhost:8991/api/tasks?status=todo"

# 移至新狀態
curl -X PATCH http://localhost:8991/api/tasks/task-abc123 \
  -H "Content-Type: application/json" \
  -d '{"status": "todo"}'
```

### Dashboard

在 dashboard 開啟 **Taskboard** 分頁（`http://localhost:8991/dashboard`）。任務以看板欄位顯示。拖曳卡片以變更狀態，點擊卡片以開啟含有留言和 diff 視圖的詳情面板。

---

## 自動派送

自動派送是後台迴圈，負責接手 `todo` 任務並透過 agent 執行。

### 運作方式

1. 計時器每隔 `interval`（預設：`5m`）觸發一次。
2. 掃描器檢查目前執行中的任務數。若 `activeCount >= maxConcurrentTasks`，略過本次掃描。
3. 對每個有負責人的 `todo` 任務，派送至對應 agent。未指派的任務自動指派給 `defaultAgent`。
4. 任務完成時，立即觸發重新掃描，讓下一批任務不必等待完整間隔。
5. daemon 啟動時，前次崩潰遺留的孤立 `doing` 任務，若有完成證據則還原為 `done`，若確實孤立則重設為 `todo`。

### 派送流程

```
                          ┌─────────┐
                          │  idea   │  （手動輸入概念）
                          └────┬────┘
                               ▼
                       ┌──────────────┐
                       │ needs-thought │  （需要分析）
                       └───────┬──────┘
                               ▼
  ┌──────────────────────────────────────────────────────────┐
  │                       backlog                             │
  │                                                           │
  │  分類（backlogAgent，預設：ruri）定期執行：               │
  │   • "ready"     → 指派 agent → 移至 todo                 │
  │   • "decompose" → 建立子任務 → 父任務移至 doing           │
  │   • "clarify"   → 新增問題留言 → 留在 backlog             │
  │                                                           │
  │  快速通道：已有負責人且無阻塞依賴                         │
  │   → 跳過 LLM 分類，直接提升至 todo                       │
  └──────────────────────┬───────────────────────────────────┘
                         ▼
  ┌──────────────────────────────────────────────────────────┐
  │                        todo                               │
  │                                                           │
  │  自動派送每次掃描週期接手任務：                           │
  │   • 有負責人       → 派送至該 agent                      │
  │   • 無負責人       → 指派 defaultAgent，然後執行          │
  │   • 有 workflow    → 透過 workflow pipeline 執行          │
  │   • 有 dependsOn  → 等待依賴完成                         │
  │   • 可續執行的前次 → 從 checkpoint 恢復                  │
  └──────────────────────┬───────────────────────────────────┘
                         ▼
  ┌──────────────────────────────────────────────────────────┐
  │                       doing                               │
  │                                                           │
  │  Agent 執行任務（單一 prompt 或 workflow DAG）             │
  │                                                           │
  │  防護：stuckThreshold（預設 2h）                          │
  │   • 若 workflow 仍在執行 → 更新時間戳                    │
  │   • 若真正卡住          → 重設為 todo                    │
  └────────┬──────────┬──────────┬──────────────────────────┘
           │          │          │
         成功      部分失敗    失敗
           │          │          │
           ▼          ▼          ▼
       ┌────────┐ ┌──────────┐ ┌────────┐
       │ review │ │ partial- │ │ failed │
       │        │ │   done   │ │        │
       └───┬────┘ └────┬─────┘ └───┬────┘
           │           │           │
           │     Dashboard 中      │  重試（最多 maxRetries 次）
           │     的恢復按鈕        │  或升級
           ▼                       ▼
       ┌────────┐            ┌──────────┐
       │  done  │            │ 升級給   │
       └────────┘            │ 人工處理 │
                             └──────────┘
```

### 分類詳情

分類每隔 `backlogTriageInterval`（預設：`1h`）執行一次，由 `backlogAgent`（預設：`ruri`）負責。Agent 接收每個 backlog 任務及其留言和可用 agent 清單後，做出以下決策：

| 動作 | 效果 |
|---|---|
| `ready` | 指派特定 agent 並提升至 `todo` |
| `decompose` | 建立子任務（含負責人），父任務移至 `doing` |
| `clarify` | 以留言形式新增問題，任務留在 `backlog` |

**快速通道**：已有負責人且無阻塞依賴的任務會完全跳過 LLM 分類，直接提升至 `todo`。

### 自動指派

當 `todo` 任務無負責人時，派送器會自動指派 `defaultAgent`（可設定，預設：`ruri`）。這防止任務無聲地卡住。典型流程：

1. 建立任務時無負責人 → 進入 `backlog`
2. 分類提升至 `todo`（有或無指派 agent）
3. 若分類未指派 → 派送器指派 `defaultAgent`
4. 任務正常執行

### 設定

在 `config.json` 中新增：

```json
{
  "taskBoard": {
    "enabled": true,
    "maxRetries": 3,
    "requireReview": true,
    "defaultWorkflow": "",
    "gitCommit": true,
    "gitPush": true,
    "gitPR": true,
    "gitWorktree": true,
    "autoDispatch": {
      "enabled": true,
      "interval": "5m",
      "maxConcurrentTasks": 3,
      "defaultAgent": "kokuyou",
      "backlogAgent": "ruri",
      "reviewAgent": "ruri",
      "escalateAssignee": "takuma",
      "stuckThreshold": "2h",
      "backlogTriageInterval": "1h",
      "reviewLoop": false,
      "maxBudget": 5.0,
      "defaultModel": ""
    }
  }
}
```

| 欄位 | 預設值 | 說明 |
|---|---|---|
| `enabled` | `false` | 啟用自動派送迴圈 |
| `interval` | `5m` | 掃描就緒任務的間隔 |
| `maxConcurrentTasks` | `3` | 同時執行的最大任務數 |
| `defaultAgent` | `ruri` | 自動指派給無負責人的 `todo` 任務 |
| `backlogAgent` | `ruri` | 審查並提升 backlog 任務的 agent |
| `reviewAgent` | `ruri` | 審查已完成任務輸出的 agent |
| `escalateAssignee` | `takuma` | 自動 review 要求人工判斷時的指派對象 |
| `stuckThreshold` | `2h` | 任務在 `doing` 狀態的最長時間，超過後重設 |
| `backlogTriageInterval` | `1h` | 兩次 backlog 分類之間的最小間隔 |
| `reviewLoop` | `false` | 啟用 Dev↔QA 循環（執行 → review → 修正，最多 `maxRetries` 次） |
| `maxBudget` | 不限 | 每個任務的最大費用（美元） |
| `defaultModel` | — | 覆蓋所有自動派送任務的模型 |

---

## Slot Pressure

Slot pressure 防止自動派送佔用所有並行 slot，而使互動式 session（人工聊天訊息、隨選派送）無法獲得資源。

在 `config.json` 中啟用：

```json
{
  "slotPressure": {
    "enabled": true,
    "reservedSlots": 2,
    "warnThreshold": 3,
    "nonInteractiveTimeout": "5m"
  }
}
```

| 欄位 | 預設值 | 說明 |
|---|---|---|
| `reservedSlots` | `2` | 為互動式使用保留的 slot 數。可用 slot 降至此數時，非互動式任務必須等待。 |
| `warnThreshold` | `3` | 可用 slot 降至此數時觸發警告，dashboard 和通知頻道會出現「排程接近滿載」訊息。 |
| `nonInteractiveTimeout` | `5m` | 非互動式任務等待 slot 的超時時間，逾時則取消。 |

互動式來源（人工聊天、`tetora dispatch`、`tetora route`）永遠立即獲得 slot。背景來源（taskboard、cron）在壓力高時等待。

---

## Git 整合

當 `gitCommit`、`gitPush` 和 `gitPR` 啟用時，派送器在任務成功完成後執行 git 操作。

**分支命名**由 `gitWorkflow.branchConvention` 控制：

```json
{
  "taskBoard": {
    "gitWorkflow": {
      "branchConvention": "{type}/{agent}-{description}",
      "types": ["feat", "fix", "refactor", "chore"],
      "defaultType": "feat",
      "autoMerge": true
    }
  }
}
```

預設範本 `{type}/{agent}-{description}` 會產生像 `feat/kokuyou-add-rate-limiting` 這樣的分支名稱。`{description}` 部分由任務標題衍生（小寫、空格替換為連字號、截至 40 個字元）。

任務的 `type` 欄位設定分支前綴。若任務無類型，使用 `defaultType`。

**自動 PR/MR** 同時支援 GitHub（`gh`）和 GitLab（`glab`）。自動使用 `PATH` 中可用的執行檔。

---

## Worktree 模式

當 `gitWorktree: true` 時，每個任務在獨立的 git worktree 中執行，而非共用工作目錄。這消除了同一倉庫中多個任務並行執行時的檔案衝突。

```
~/.tetora/runtime/worktrees/
  task-abc123/   ← 此任務的獨立副本
  task-def456/   ← 此任務的獨立副本
```

任務完成時：

- 若 `autoMerge: true`（預設），worktree 分支會合併回 `main`，並移除 worktree。
- 若合併失敗，任務移至 `partial-done` 狀態。Worktree 保留供手動解決。

從 `partial-done` 復原：

```bash
# 查看發生了什麼
tetora task show task-abc123 --full

# 手動合併分支
git merge feat/kokuyou-add-rate-limiting

# 標記為完成
tetora task move task-abc123 --status=done
```

---

## Workflow 整合

任務可以透過 workflow pipeline 執行，而非單一 agent prompt。當任務需要多個協調步驟時（例如：研究 → 實作 → 測試 → 撰寫文件），這非常有用。

為任務指派 workflow：

```bash
# 建立任務時設定
tetora task create \
  --title="Implement OAuth2 flow" \
  --workflow=engineering-pipeline \
  --assignee=kokuyou

# 或更新現有任務
tetora task update task-abc123 --workflow=engineering-pipeline
```

若要為特定任務停用板級預設 workflow：

```json
{ "workflow": "none" }
```

板級預設 workflow 套用於所有自動派送的任務，除非被覆蓋：

```json
{
  "taskBoard": {
    "defaultWorkflow": "engineering-pipeline"
  }
}
```

任務的 `workflowRunId` 欄位連結至特定的 workflow 執行，可在 dashboard 的 Workflows 分頁查看。

---

## Dashboard 視圖

在 `http://localhost:8991/dashboard` 開啟 dashboard，導覽至 **Taskboard** 分頁。

**看板** — 每個狀態一個欄位。卡片顯示標題、負責人、優先級標籤和費用。拖曳可變更狀態。

**任務詳情面板** — 點擊任意卡片開啟。顯示：
- 完整描述及所有留言（spec、context、log 條目）
- Session 連結（若仍在執行，跳轉至即時 worker 終端機）
- 費用、耗時、重試次數
- 若適用，顯示 workflow 執行連結

**Diff review 面板** — 當 `requireReview: true` 時，已完成的任務會出現在 review 佇列中。審查者可看到變更 diff，並可核准或要求修改。

---

## 使用技巧

**任務規模。** 將任務控制在 30～90 分鐘的範圍內。過大的任務（多天的重構）容易逾時或產生空輸出，被標記為失敗。使用 `parentId` 欄位將其拆分為子任務。

**並行派送上限。** `maxConcurrentTasks: 3` 是安全的預設值。超過 provider 允許的 API 連線數會導致競爭和逾時。從 3 開始，確認穩定運作後再提升至 5。

**partial-done 復原。** 若任務進入 `partial-done`，表示 agent 的工作已成功完成——只有 git merge 步驟失敗。手動解決衝突後，將任務移至 `done`。費用和 session 資料都會保留。

**使用 `dependsOn`。** 有未完成依賴的任務會被派送器略過，直到所有列出的任務 ID 達到 `done` 狀態。上游任務的結果會自動注入至依賴任務的 prompt，放在「Previous Task Results」區段。

**Backlog 分類。** `backlogAgent` 讀取每個 `backlog` 任務，評估可行性和優先級，並將清晰的任務提升至 `todo`。在 `backlog` 任務中撰寫詳細的描述和驗收標準——分類 agent 會以此決定是否提升或留待人工審查。

**重試與 review 循環。** 當 `reviewLoop: false`（預設）時，失敗的任務會重試最多 `maxRetries` 次，並注入先前的日誌留言。當 `reviewLoop: true` 時，每次執行都會由 `reviewAgent` 審查後才視為完成——若發現問題，agent 會收到回饋並再次嘗試。
