---
title: "Workflow 工作流程"
lang: "zh-TW"
order: 2
description: "Define multi-step task pipelines with JSON workflows and agent orchestration."
---
# Workflow 工作流程

## 概述

Workflow 是 Tetora 的多步驟任務編排系統。透過 JSON 定義一連串步驟，由不同 agent 協作完成複雜任務。

**適用場景：**

- 需要多個 agent 依序或平行協作的任務
- 包含條件分支、錯誤重試的流程
- 定時（cron）、事件觸發、webhook 觸發的自動化工作
- 需要追蹤執行歷史與成本的正式流程

## 快速開始

### 1. 撰寫 workflow JSON

建立 `my-workflow.json`：

```json
{
  "name": "research-and-summarize",
  "description": "收集資料後撰寫摘要",
  "variables": {
    "topic": "AI agents"
  },
  "timeout": "30m",
  "steps": [
    {
      "id": "research",
      "agent": "hisui",
      "prompt": "搜尋並整理 {{topic}} 的最新發展，列出 5 個重點"
    },
    {
      "id": "summarize",
      "agent": "kohaku",
      "prompt": "根據以下資料撰寫 300 字摘要：\n{{steps.research.output}}",
      "dependsOn": ["research"]
    }
  ]
}
```

### 2. 匯入並驗證

```bash
# 驗證 JSON 結構
tetora workflow validate my-workflow.json

# 匯入到 ~/.tetora/workflows/
tetora workflow create my-workflow.json
```

### 3. 執行

```bash
# 執行 workflow
tetora workflow run research-and-summarize

# 覆蓋變數
tetora workflow run research-and-summarize --var topic="LLM safety"

# Dry-run（不呼叫 LLM，僅估算成本）
tetora workflow run research-and-summarize --dry-run
```

### 4. 查看結果

```bash
# 列出執行紀錄
tetora workflow runs research-and-summarize

# 查看某次執行的詳細狀態
tetora workflow status <run-id>
```

## Workflow JSON 結構

### 頂層欄位

| 欄位 | 型別 | 必填 | 說明 |
|------|------|:----:|------|
| `name` | string | Yes | Workflow 名稱，僅允許英數、`-`、`_`，如 `my-workflow` |
| `description` | string | | 描述說明 |
| `steps` | WorkflowStep[] | Yes | 至少一個步驟 |
| `variables` | map[string]string | | 輸入變數及預設值（空字串 `""` 表示必填） |
| `timeout` | string | | 整體逾時，Go duration 格式（如 `"30m"`、`"1h"`） |
| `onSuccess` | string | | 成功時的通知模板 |
| `onFailure` | string | | 失敗時的通知模板 |
| `gitWorktree` | bool | | 啟用 git worktree 隔離。預設 `false` |
| `branch` | string | | 指定分支名稱。省略時自動產生 `wf/{name}` |
| `workdir` | string | | 目標 repo 路徑。省略時使用 config 的 `defaultWorkdir` |

### WorkflowStep 欄位

| 欄位 | 型別 | 說明 |
|------|------|------|
| `id` | string | **必填** — 步驟唯一識別碼 |
| `type` | string | 步驟類型，預設 `"dispatch"`。可選值見下方 |
| `agent` | string | 執行此步驟的 agent 角色 |
| `prompt` | string | 給 agent 的指令（支援 `{{}}` 模板） |
| `skill` | string | Skill 名稱（type=skill 時） |
| `skillArgs` | string[] | Skill 參數（支援模板） |
| `dependsOn` | string[] | 前置步驟 ID 列表（DAG 依賴） |
| `model` | string | 指定 LLM model |
| `provider` | string | 指定 provider |
| `timeout` | string | 單步驟逾時 |
| `budget` | number | 成本上限（USD） |
| `permissionMode` | string | 權限模式 |
| `if` | string | 條件表達式（type=condition） |
| `then` | string | 條件為真時跳轉的步驟 ID |
| `else` | string | 條件為假時跳轉的步驟 ID |
| `handoffFrom` | string | 來源步驟 ID（type=handoff） |
| `parallel` | WorkflowStep[] | 平行子步驟（type=parallel） |
| `retryMax` | int | 最大重試次數（需搭配 `onError: "retry"`） |
| `retryDelay` | string | 重試間隔，如 `"10s"` |
| `onError` | string | 錯誤處理策略：`"stop"`（預設）、`"skip"`、`"retry"` |
| `toolName` | string | 工具名稱（type=tool_call） |
| `toolInput` | map[string]string | 工具輸入參數（支援 `{{var}}` 展開） |
| `delay` | string | 等待時間（type=delay），如 `"30s"`、`"5m"` |
| `notifyMsg` | string | 通知訊息（type=notify，支援模板） |
| `notifyTo` | string | 通知頻道提示（如 `"telegram"`） |

## 步驟類型詳解

### dispatch（預設）

將 prompt 發送給指定 agent 執行。這是最常用的步驟類型，省略 `type` 時即為 dispatch。

```json
{
  "id": "draft",
  "agent": "kohaku",
  "prompt": "撰寫一篇關於 {{topic}} 的文章",
  "model": "claude-sonnet-4-20250514",
  "timeout": "10m"
}
```

**必填：** `prompt`
**可選：** `agent`、`model`、`provider`、`timeout`、`budget`、`permissionMode`

### skill

執行已註冊的 skill。

```json
{
  "id": "search",
  "type": "skill",
  "skill": "web-search",
  "skillArgs": ["{{topic}}", "--depth", "3"]
}
```

**必填：** `skill`
**可選：** `skillArgs`

### condition

根據條件表達式決定分支走向。條件為真時走 `then`，為假走 `else`。未選中的分支會被標記為 skipped。

```json
{
  "id": "check-type",
  "type": "condition",
  "if": "{{type}} == 'technical'",
  "then": "tech-research",
  "else": "creative-draft"
}
```

**必填：** `if`、`then`
**可選：** `else`

條件支援的運算子：
- `==` — 等於（如 `{{type}} == 'technical'`）
- `!=` — 不等於
- 單值 truthy 檢查 — 非空且非 `"false"`/`"0"` 即為真

### parallel

平行執行多個子步驟，全部完成後才繼續。子步驟的輸出以 `\n---\n` 串接。

```json
{
  "id": "gather",
  "type": "parallel",
  "parallel": [
    {"id": "search-papers", "agent": "hisui", "prompt": "搜尋論文"},
    {"id": "search-code", "agent": "kokuyou", "prompt": "搜尋開源專案"}
  ]
}
```

**必填：** `parallel`（至少一個子步驟）

子步驟的結果可個別以 `{{steps.search-papers.output}}` 引用。

### handoff

將一個步驟的輸出交接給另一個 agent 處理。source step 的完整輸出會作為接收 agent 的上下文。

```json
{
  "id": "review",
  "type": "handoff",
  "agent": "ruri",
  "handoffFrom": "draft",
  "prompt": "審閱並修改文章",
  "dependsOn": ["draft"]
}
```

**必填：** `handoffFrom`、`agent`
**可選：** `prompt`（給接收 agent 的指令）

### tool_call

呼叫已註冊的工具（tool registry）。

```json
{
  "id": "fetch-data",
  "type": "tool_call",
  "toolName": "http-get",
  "toolInput": {
    "url": "https://api.example.com/data?q={{topic}}"
  }
}
```

**必填：** `toolName`
**可選：** `toolInput`（支援 `{{var}}` 展開）

### delay

等待指定時間後繼續。

```json
{
  "id": "wait",
  "type": "delay",
  "delay": "30s"
}
```

**必填：** `delay`（Go duration 格式：`"30s"`、`"5m"`、`"1h"`）

### notify

發送通知訊息。訊息透過 SSE event 發布（type=`workflow_notify`），外部消費者可據此觸發 Telegram、Slack 等通知。

```json
{
  "id": "notify-done",
  "type": "notify",
  "notifyMsg": "任務完成：{{steps.review.output}}",
  "notifyTo": "telegram"
}
```

**必填：** `notifyMsg`
**可選：** `notifyTo`（頻道提示）

## 變數與模板

Workflow 支援 `{{}}` 模板語法，在步驟執行前展開。

### 輸入變數

```
{{varName}}
```

從 `variables` 預設值或 `--var key=value` 覆蓋值取得。

### 步驟結果

```
{{steps.ID.output}}    — 步驟的輸出文字
{{steps.ID.status}}    — 步驟狀態（success/error/skipped/timeout）
{{steps.ID.error}}     — 步驟的錯誤訊息
```

### 環境變數

```
{{env.KEY}}            — 系統環境變數
```

### 範例

```json
{
  "id": "summarize",
  "agent": "kohaku",
  "prompt": "主題：{{topic}}\n研究結果：{{steps.research.output}}\n\n請撰寫摘要。",
  "dependsOn": ["research"]
}
```

## 依賴與流程控制

### dependsOn — DAG 依賴

透過 `dependsOn` 定義步驟間的先後關係，系統自動以 DAG（有向無環圖）排序執行。

```json
{
  "id": "step-c",
  "dependsOn": ["step-a", "step-b"],
  "prompt": "..."
}
```

- `step-c` 會等 `step-a` 和 `step-b` 都完成才開始
- 沒有 `dependsOn` 的步驟會立即開始（可能平行）
- 系統會檢測循環依賴並拒絕執行

### 條件分支

`condition` 步驟的 `then`/`else` 決定後續哪些步驟執行：

```
classify (condition)
  ├── then → tech-research
  └── else → creative-draft
```

未選中的分支步驟會被標記為 `skipped`，其下游步驟仍會正常依 `dependsOn` 評估。

## 錯誤處理

### onError 策略

每個步驟可設定 `onError`：

| 值 | 行為 |
|---|---|
| `"stop"` | **預設** — 步驟失敗時中止整個 workflow，後續步驟標記為 skipped |
| `"skip"` | 步驟失敗後標記為 skipped，繼續執行後續步驟 |
| `"retry"` | 依 `retryMax` + `retryDelay` 重試，全部失敗後視為 error |

### 重試設定

```json
{
  "id": "flaky-step",
  "agent": "hisui",
  "prompt": "...",
  "onError": "retry",
  "retryMax": 3,
  "retryDelay": "10s"
}
```

- `retryMax`：最大重試次數（不含首次執行）
- `retryDelay`：重試間隔，預設 5 秒
- 僅在 `onError: "retry"` 時生效

## 觸發器（Triggers）

觸發器讓 workflow 自動執行，設定在 `config.json` 的 `workflowTriggers` 陣列中。

### WorkflowTriggerConfig 結構

| 欄位 | 型別 | 說明 |
|------|------|------|
| `name` | string | 觸發器名稱 |
| `workflowName` | string | 要執行的 workflow 名稱 |
| `enabled` | bool | 是否啟用（預設 true） |
| `trigger` | TriggerSpec | 觸發條件 |
| `variables` | map[string]string | 覆蓋 workflow 變數 |
| `cooldown` | string | 冷卻時間（如 `"5m"`、`"1h"`） |

### TriggerSpec 結構

| 欄位 | 型別 | 說明 |
|------|------|------|
| `type` | string | `"cron"`、`"event"`、`"webhook"` |
| `cron` | string | Cron 表達式（5 欄位：分 時 日 月 週） |
| `tz` | string | 時區（如 `"Asia/Taipei"`），cron 專用 |
| `event` | string | SSE event type，支援 `*` 後綴萬用（如 `"deploy_*"`） |
| `webhook` | string | Webhook 路徑後綴 |

### Cron 觸發

每 30 秒檢查一次，每分鐘最多觸發一次（防止重複）。

```json
{
  "name": "daily-briefing",
  "workflowName": "research-and-summarize",
  "trigger": {"type": "cron", "cron": "0 8 * * *", "tz": "Asia/Taipei"},
  "variables": {"topic": "AI industry news"},
  "cooldown": "12h"
}
```

### Event 觸發

監聽 SSE `_triggers` 頻道，比對 event type。支援 `*` 後綴萬用字元。

```json
{
  "name": "on-deploy",
  "workflowName": "content-pipeline",
  "trigger": {"type": "event", "event": "deploy_*"},
  "variables": {"type": "technical"}
}
```

Event 觸發時會自動注入額外變數：`event_type`、`task_id`、`session_id`，以及 event data 中的各欄位（前綴 `event_`）。

### Webhook 觸發

透過 HTTP POST 觸發：

```json
{
  "name": "external-hook",
  "workflowName": "content-pipeline",
  "trigger": {"type": "webhook", "webhook": "content-request"}
}
```

呼叫方式：

```bash
curl -X POST http://localhost:PORT/api/triggers/webhook/external-hook \
  -H "Content-Type: application/json" \
  -d '{"topic": "new feature"}'
```

POST body 的 JSON 鍵值會作為額外變數注入 workflow。

### Cooldown 設定

所有觸發器都支援 `cooldown`，防止短時間內重複觸發。冷卻期間內的觸發會被靜默忽略。

### 觸發器元變數

每次觸發時，系統自動注入以下變數：

- `_trigger_name` — 觸發器名稱
- `_trigger_type` — 觸發類型（cron/event/webhook）
- `_trigger_time` — 觸發時間（RFC3339）

## 執行模式

### live（預設）

完整執行：呼叫 LLM、記錄歷史、發布 SSE 事件。

```bash
tetora workflow run my-workflow
```

### dry-run

不呼叫 LLM，僅估算各步驟成本。condition 步驟會正常評估，dispatch/skill/handoff 步驟回傳成本估算。

```bash
tetora workflow run my-workflow --dry-run
```

### shadow

正常執行 LLM 呼叫，但不記錄到任務歷史和 session 紀錄。適合測試用途。

```bash
tetora workflow run my-workflow --shadow
```

## CLI 指令參考

```
tetora workflow <command> [options]
```

| 指令 | 說明 |
|------|------|
| `list` | 列出所有已儲存的 workflow |
| `show <name>` | 顯示 workflow 定義（摘要 + JSON） |
| `validate <name\|file>` | 驗證 workflow（接受名稱或 JSON 檔案路徑） |
| `create <file>` | 從 JSON 檔案匯入 workflow（會先驗證） |
| `delete <name>` | 刪除 workflow |
| `run <name> [flags]` | 執行 workflow |
| `runs [name]` | 列出執行歷史（可按名稱篩選） |
| `status <run-id>` | 查看某次執行的詳細狀態（JSON 輸出） |
| `messages <run-id>` | 查看執行過程中的 agent 訊息和 handoff 記錄 |
| `history <name>` | 查看 workflow 版本歷史 |
| `rollback <name> <version-id>` | 還原到指定版本 |
| `diff <version1> <version2>` | 比較兩個版本差異 |

### run 指令 flags

| Flag | 說明 |
|------|------|
| `--var key=value` | 覆蓋 workflow 變數（可多次使用） |
| `--dry-run` | Dry-run 模式（不呼叫 LLM） |
| `--shadow` | Shadow 模式（不記錄歷史） |

### 別名

- `list` = `ls`
- `delete` = `rm`
- `messages` = `msgs`

## HTTP API 參考

### Workflow CRUD

| Method | 路徑 | 說明 |
|--------|------|------|
| GET | `/workflows` | 列出所有 workflow |
| POST | `/workflows` | 建立 workflow（body: Workflow JSON） |
| GET | `/workflows/{name}` | 取得單一 workflow 定義 |
| DELETE | `/workflows/{name}` | 刪除 workflow |
| POST | `/workflows/{name}/validate` | 驗證 workflow |
| POST | `/workflows/{name}/run` | 執行 workflow（非同步，立即回傳 `202 Accepted`） |
| POST | `/workflows/{name}/dry-run` | Dry-run（同步，不呼叫 LLM，回傳成本估算） |
| GET | `/workflows/{name}/runs` | 取得該 workflow 的執行紀錄 |
| GET | `/workflows/{name}/versions` | 取得版本歷史 |
| POST | `/workflows/{name}/rollback` | 還原版本（body: `{"versionId":"..."}`) |

#### POST /workflows/{name}/run body

```json
{
  "variables": {
    "topic": "AI agents"
  }
}
```

#### POST /workflows/{name}/dry-run body

同 `run`。同步回傳完整執行結果 — 不呼叫 LLM，每個步驟顯示預估成本和執行順序。

### Workflow Runs

| Method | 路徑 | 說明 |
|--------|------|------|
| GET | `/workflow-runs` | 列出所有執行紀錄（可加 `?workflow=name` 篩選） |
| GET | `/workflow-runs/{id}` | 取得單次執行詳情（含 step results、handoffs、callbacks） |

### Triggers

| Method | 路徑 | 說明 |
|--------|------|------|
| GET | `/api/triggers` | 列出所有觸發器設定和狀態 |
| POST | `/api/triggers` | 建立觸發器（body: WorkflowTriggerConfig JSON） |
| PUT | `/api/triggers/{name}` | 更新觸發器 |
| DELETE | `/api/triggers/{name}` | 刪除觸發器 |
| POST | `/api/triggers/{name}/toggle` | 啟用/停用觸發器 |
| POST | `/api/triggers/{name}/fire` | 手動觸發 |
| GET | `/api/triggers/{name}/runs` | 查看觸發執行紀錄（可加 `?limit=N`） |
| POST | `/api/triggers/webhook/{id}` | Webhook 觸發（body 為 JSON 鍵值變數） |

觸發器的異動（建立/更新/刪除/切換）會自動寫入 `config.json` 並透過 SIGHUP 熱載入觸發引擎。

### Templates 範本

| Method | 路徑 | 說明 |
|--------|------|------|
| GET | `/api/templates` | 列出所有內建範本（名稱、描述、步驟數） |
| GET | `/api/templates/{name}` | 取得完整範本 JSON |
| POST | `/api/templates/{name}/install` | 安裝範本為使用者 workflow（body: `{"newName":"..."}`) |

內建 36 個產業範本，涵蓋 HR、財務、DevOps、醫療、物流等。可在 Dashboard 的範本庫或 API 瀏覽。

## 版本管理

每次 `create` 或修改 workflow 時，系統會自動建立版本快照。

```bash
# 查看版本歷史
tetora workflow history my-workflow

# 還原到指定版本
tetora workflow rollback my-workflow <version-id>

# 比較兩個版本
tetora workflow diff <version-id-1> <version-id-2>
```

## 驗證規則

系統在 `create` 和 `run` 之前都會執行驗證，檢查項目包括：

- `name` 必填，僅允許英數、`-`、`_`
- 至少一個步驟
- 步驟 ID 必須唯一
- `dependsOn` 引用的步驟 ID 必須存在
- 步驟不能依賴自己
- 不允許循環依賴（DAG cycle detection）
- 各步驟類型的必填欄位檢查（如 dispatch 需要 prompt、condition 需要 if + then）
- `timeout`、`retryDelay`、`delay` 必須是合法的 Go duration 格式
- `onError` 僅接受 `stop`、`skip`、`retry`
- condition 的 `then`/`else` 引用的步驟 ID 必須存在
- handoff 的 `handoffFrom` 引用的步驟 ID 必須存在
- parallel 子步驟會遞迴驗證（唯一 ID、合法類型、必填欄位、合法 duration 格式等）

## Dashboard UI

Dashboard（`http://localhost:PORT`）提供 workflow 所有操作的視覺化介面。

### Workflow 編輯器

- **建立/編輯**：JSON 編輯器、步驟列表、DAG 預覽
- **執行**：從工具列直接執行，可覆蓋變數
- **Dry Run**：測試執行順序和成本估算，不呼叫 LLM
- **版本歷史**：瀏覽、比較、還原到歷史版本

### 執行詳情

- **DAG 圖**：即時更新節點狀態（綠=成功、紅=錯誤、藍=執行中、黃=等待中）
- **步驟結果列表**：DAG 下方可展開的卡片，顯示每個步驟的 output、耗時、成本、錯誤
- **成本分佈條**：水平堆疊圖，顯示各步驟成本佔比
- **SSE 即時更新**：步驟狀態變更透過 Server-Sent Events 即時推送
- **External 步驟 UX**：等待中步驟顯示 callback URL、倒數計時、手動回覆控制

### 觸發器管理

- **觸發器卡片**：顯示類型標籤（cron/event/webhook）、下次執行時間、操作按鈕
- **建立/編輯 Modal**：設定名稱、類型、workflow、變數、冷卻時間、類型專屬欄位
- **快速操作**：啟用/停用、手動觸發、查看歷史紀錄、複製 webhook URL

### 範本庫

- **瀏覽**：36 個產業範本的網格，含描述和步驟數
- **搜尋/篩選**：文字搜尋，自動分類（HR、財務、DevOps 等）
- **預覽**：安裝前查看完整 JSON
- **安裝**：一鍵安裝並可重新命名，立即可在編輯器中使用

## 範例：開發用 Workflow

Tetora 內建 `standard-dev` workflow，為 agent 驅動開發設計，包含自動品質關卡。黒曜（Kokuyou）等 agent 用這個流程自主執行實作任務。

### 流程

```
read-spec → plan → implement → build-test → self-review → quality-review → [fix-issues] → commit
```

### 運作方式

1. **read-spec** — Agent 從 `tasks/specs.md` 讀取任務 spec，整理需求和驗收條件
2. **plan** — 擬定實作計畫：要改的檔案、方法、風險
3. **implement** — 按計畫實作，寫出實際程式碼
4. **build-test** — 執行 `go build` + `go test`，失敗自動重試 2 次
5. **self-review** — Agent 自我 review `git diff`，對照 spec 找出疑慮
6. **quality-review** — 獨立 agent（琉璃/Ruri）進行 staff engineer 等級的 code review（8 項檢查清單），回傳：
   - `approve` — 可上線，進入 commit
   - `fix` — 列出具體問題，agent 自主修復
   - `escalate` — 需要人工判斷（spec 不明確、生產風險無法確認）
7. **fix-issues** — verdict 為 `fix` 時，agent 修復所有 review 意見並重新驗證 build
8. **commit** — 建立 git commit（包含 taskId 和標題）

### 使用方式

```bash
# CLI 執行
tetora workflow run standard-dev --var taskId="T-042" --var taskTitle="Add webhook retry logic"

# Dashboard 執行
# 開啟 Workflows → standard-dev → Run → 填入 taskId 和 taskTitle

# 先 dry-run 確認執行順序和成本
tetora workflow run standard-dev --dry-run --var taskId="T-042" --var taskTitle="test"
```

### 自訂

workflow 使用 `{{agent}}` 變數（預設：kokuyou），任何 agent 都能執行：

```bash
tetora workflow run standard-dev --var agent=hisui --var taskId="T-043" --var taskTitle="Research API options"
```

### 設計決策

- **三元裁決**：`approve`/`fix`/`escalate` 避免無限修復迴圈 — `escalate` 是真正模糊情況的逃生口
- **獨立 reviewer**：品質 review 使用不同 agent（琉璃），避免自我確認偏誤
- **Build 重試**：不穩定的 build 自動重試 2 次再判定失敗
- **成本上限**：每個步驟設有 budget 防止失控
- **不自動 push**：只 commit，不 push — 人工確認後再推送
