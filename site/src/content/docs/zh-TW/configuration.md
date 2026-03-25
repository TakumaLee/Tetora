---
title: "設定參考手冊"
lang: "zh-TW"
order: 1
description: "Configure Tetora via ~/.tetora/config.json — models, providers, and runtime settings."
---
# 設定參考手冊

## 概述

Tetora 的設定集中於單一 JSON 檔案，位於 `~/.tetora/config.json`。

**重要行為：**

- **`$ENV_VAR` 替換** — 任何以 `$` 開頭的字串值，在啟動時會自動替換為對應的環境變數。建議用此方式處理機密資訊（API 金鑰、token）而非硬編碼。
- **熱重載** — 向 daemon 發送 `SIGHUP` 即可重新載入設定。格式錯誤的設定會被拒絕並保留原有設定，daemon 不會崩潰。
- **相對路徑** — `jobsFile`、`historyDB`、`defaultWorkdir` 及目錄欄位均相對於設定檔所在目錄（`~/.tetora/`）解析。
- **向下相容** — 舊版 `"roles"` 鍵是 `"agents"` 的別名；`smartDispatch` 內的舊版 `"defaultRole"` 鍵是 `"defaultAgent"` 的別名。

---

## 頂層欄位

### 核心設定

| 欄位 | 類型 | 預設值 | 說明 |
|---|---|---|---|
| `listenAddr` | string | `"127.0.0.1:8991"` | API 與 dashboard 的 HTTP 監聽位址，格式：`host:port`。 |
| `apiToken` | string | `""` | 所有 API 請求必須攜帶的 Bearer token。空值代表不驗證（正式環境不建議）。支援 `$ENV_VAR`。 |
| `maxConcurrent` | int | `8` | 最大並行 agent 任務數。超過 20 時會在啟動時顯示警告。 |
| `defaultModel` | string | `"sonnet"` | 預設 Claude 模型名稱。除非 agent 層級另有覆蓋，否則傳遞給 provider。 |
| `defaultTimeout` | string | `"1h"` | 預設任務逾時時間。Go duration 格式：`"15m"`、`"1h"`、`"30s"`。 |
| `defaultBudget` | float64 | `0` | 每個任務的預設費用上限（美元）。`0` 代表不限制。 |
| `defaultPermissionMode` | string | `"acceptEdits"` | 預設 Claude 權限模式。常用值：`"acceptEdits"`、`"default"`。 |
| `defaultAgent` | string | `""` | 當沒有路由規則符合時，系統層級的備援 agent 名稱。 |
| `defaultWorkdir` | string | `""` | agent 任務的預設工作目錄，必須實際存在。 |
| `claudePath` | string | `"claude"` | `claude` CLI 執行檔的路徑。預設從 `$PATH` 尋找 `claude`。 |
| `defaultProvider` | string | `"claude"` | 未指定 agent 層級覆蓋時使用的 provider 名稱。 |
| `log` | bool | `false` | 啟用檔案記錄的舊版旗標。建議改用 `logging.level`。 |
| `maxPromptLen` | int | `102400` | 最大 prompt 長度（位元組，100 KB）。超過此限制的請求會被拒絕。 |
| `configVersion` | int | `0` | 設定結構版本，用於自動遷移。請勿手動設定。 |
| `encryptionKey` | string | `""` | 用於欄位層級敏感資料加密的 AES 金鑰。支援 `$ENV_VAR`。 |
| `streamToChannels` | bool | `false` | 將即時任務狀態串流至已連線的訊息頻道（Discord、Telegram 等）。 |
| `cronNotify` | bool\|null | `null`（true） | `false` 會關閉所有 cron 任務完成通知。`null` 或 `true` 則啟用。 |
| `cronReplayHours` | int | `2` | daemon 啟動時，往回補執行錯過的 cron 任務的小時數。 |
| `diskBudgetGB` | float64 | `1.0` | 最低剩餘磁碟空間（GB）。低於此值時 cron 任務將被拒絕。 |
| `diskWarnMB` | int | `500` | 剩餘磁碟警告閾值（MB）。僅記錄 WARN，任務仍繼續執行。 |
| `diskBlockMB` | int | `200` | 剩餘磁碟封鎖閾值（MB）。任務以 `skipped_disk_full` 狀態略過。 |

### 目錄覆蓋

所有目錄預設位於 `~/.tetora/` 下。僅在需要非標準配置時才覆蓋。

| 欄位 | 類型 | 預設值 | 說明 |
|---|---|---|---|
| `knowledgeDir` | string | `~/.tetora/knowledge/` | workspace 知識庫檔案目錄。 |
| `agentsDir` | string | `~/.tetora/agents/` | 各 agent SOUL.md 檔案所在目錄。 |
| `workspaceDir` | string | `~/.tetora/workspace/` | 規則、記憶、skills、草稿等檔案目錄。 |
| `runtimeDir` | string | `~/.tetora/runtime/` | session、輸出、日誌、快取目錄。 |
| `vaultDir` | string | `~/.tetora/vault/` | 加密密鑰保管庫目錄。 |
| `historyDB` | string | `history.db` | 任務歷史 SQLite 資料庫路徑，相對於設定檔目錄。 |
| `jobsFile` | string | `jobs.json` | cron 任務定義檔路徑，相對於設定檔目錄。 |

### 全域允許目錄

```json
{
  "allowedDirs": ["/Users/me/projects", "/tmp"],
  "defaultAddDirs": ["/Users/me/shared-context"]
}
```

| 欄位 | 類型 | 說明 |
|---|---|---|
| `allowedDirs` | string[] | agent 可讀寫的目錄。全域套用，可在 agent 層級縮小範圍。 |
| `defaultAddDirs` | string[] | 每個任務都以 `--add-dir` 注入的目錄（唯讀上下文）。 |
| `allowedIPs` | string[] | 允許呼叫 API 的 IP 位址或 CIDR 範圍。空值代表允許全部。範例：`["192.168.1.0/24", "10.0.0.1"]`。 |

---

## Providers

Provider 定義 Tetora 如何執行 agent 任務。可設定多個 provider 並在 agent 層級選擇使用。

```json
{
  "defaultProvider": "claude",
  "providers": {
    "claude": {
      "type": "claude-cli",
      "path": "/usr/local/bin/claude"
    },
    "openai": {
      "type": "openai-compatible",
      "baseUrl": "https://api.openai.com/v1",
      "apiKey": "$OPENAI_API_KEY",
      "model": "gpt-4o"
    },
    "claude-api": {
      "type": "claude-api",
      "apiKey": "$ANTHROPIC_API_KEY",
      "model": "claude-sonnet-4-5",
      "maxTokens": 8192,
      "firstTokenTimeout": "60s"
    }
  }
}
```

### `providers` — `ProviderConfig`

| 欄位 | 類型 | 預設值 | 說明 |
|---|---|---|---|
| `type` | string | 必填 | Provider 類型，可選：`"claude-cli"`、`"openai-compatible"`、`"claude-api"`、`"claude-code"`。 |
| `path` | string | `""` | 執行檔路徑。`claude-cli` 和 `claude-code` 類型使用。空值時退回使用 `claudePath`。 |
| `baseUrl` | string | `""` | API 基礎 URL。`openai-compatible` 類型必填。 |
| `apiKey` | string | `""` | API 金鑰。支援 `$ENV_VAR`。`claude-api` 必填；`openai-compatible` 選填。 |
| `model` | string | `""` | 此 provider 的預設模型，會覆蓋使用此 provider 任務的 `defaultModel`。 |
| `maxTokens` | int | `8192` | 最大輸出 token 數（`claude-api` 使用）。 |
| `firstTokenTimeout` | string | `"60s"` | SSE 串流中等待第一個回應 token 的超時時間。 |

**Provider 類型：**
- `claude-cli` — 以子程序方式執行 `claude` 執行檔（預設，相容性最佳）
- `claude-api` — 透過 HTTP 直接呼叫 Anthropic API（需要 `ANTHROPIC_API_KEY`）
- `openai-compatible` — 任何相容 OpenAI 的 REST API（OpenAI、Ollama、Groq 等）
- `claude-code` — 使用 Claude Code CLI 模式

---

## Agents

Agent 定義具名角色，各自擁有獨立的模型、soul 檔案及工具存取設定。

```json
{
  "agents": {
    "coordinator": {
      "soulFile": "SOUL.md",
      "model": "sonnet",
      "description": "Handles planning, research, and coordination.",
      "keywords": ["plan", "research", "coordinate"]
    },
    "engineer": {
      "soulFile": "team/engineer/SOUL.md",
      "model": "sonnet",
      "provider": "claude",
      "description": "Handles coding, debugging, and infrastructure.",
      "keywords": ["code", "debug", "deploy"],
      "permissionMode": "acceptEdits",
      "allowedDirs": ["/Users/me/projects"],
      "trustLevel": "auto"
    }
  }
}
```

### `agents` — `AgentConfig`

| 欄位 | 類型 | 預設值 | 說明 |
|---|---|---|---|
| `soulFile` | string | 必填 | agent SOUL.md 角色檔案的路徑，相對於 `agentsDir`。 |
| `model` | string | `defaultModel` | 此 agent 使用的模型。 |
| `description` | string | `""` | 人類可讀的描述。LLM 分類器也會使用此欄位進行路由。 |
| `keywords` | string[] | `[]` | 在 smart dispatch 中觸發路由至此 agent 的關鍵字。 |
| `provider` | string | `defaultProvider` | Provider 名稱（`providers` 映射中的鍵）。 |
| `permissionMode` | string | `defaultPermissionMode` | 此 agent 的 Claude 權限模式。 |
| `allowedDirs` | string[] | `allowedDirs` | 此 agent 可存取的檔案系統路徑，會覆蓋全域設定。 |
| `docker` | bool\|null | `null` | 每個 agent 的 Docker 沙箱覆蓋設定。`null` 代表繼承全域 `docker.enabled`。 |
| `fallbackProviders` | string[] | `[]` | 主要 provider 失敗時，依序嘗試的備援 provider 名稱清單。 |
| `trustLevel` | string | `"auto"` | 信任等級：`"observe"`（唯讀）、`"suggest"`（提議但不執行）、`"auto"`（完全自主）。 |
| `tools` | AgentToolPolicy | `{}` | 工具存取政策。參見 [Tool Policy](#tool-policy)。 |
| `toolProfile` | string | `"standard"` | 命名工具設定檔：`"minimal"`、`"standard"`、`"full"`。 |
| `workspace` | WorkspaceConfig | `{}` | workspace 隔離設定。 |

---

## Smart Dispatch

Smart Dispatch 根據規則、關鍵字及 LLM 分類，自動將傳入任務路由到最適合的 agent。

```json
{
  "smartDispatch": {
    "enabled": true,
    "coordinator": "coordinator",
    "defaultAgent": "coordinator",
    "classifyBudget": 0.1,
    "classifyTimeout": "30s",
    "review": false,
    "reviewLoop": false,
    "maxRetries": 3,
    "fallback": "smart",
    "rules": [
      {
        "agent": "engineer",
        "keywords": ["bug", "error", "deploy", "docker"],
        "patterns": ["(?:fix|resolve)\\s+(?:bug|issue|error)"]
      },
      {
        "agent": "creator",
        "keywords": ["blog post", "documentation", "README"]
      }
    ],
    "bindings": [
      {
        "channel": "discord",
        "channelId": "123456789",
        "agent": "engineer"
      }
    ]
  }
}
```

### `smartDispatch` — `SmartDispatchConfig`

| 欄位 | 類型 | 預設值 | 說明 |
|---|---|---|---|
| `enabled` | bool | `false` | 啟用 smart dispatch 路由。 |
| `coordinator` | string | 第一個 agent | 用於 LLM 任務分類的 agent。 |
| `defaultAgent` | string | 第一個 agent | 無規則符合時的備援 agent。 |
| `classifyBudget` | float64 | `0.1` | 分類 LLM 呼叫的費用預算（美元）。 |
| `classifyTimeout` | string | `"30s"` | 分類呼叫的逾時時間。 |
| `review` | bool | `false` | 任務完成後對輸出執行 review agent。 |
| `reviewLoop` | bool | `false` | 啟用 Dev↔QA 重試循環：review → 意見回饋 → 重試（最多 `maxRetries` 次）。 |
| `maxRetries` | int | `3` | review 循環中最大 QA 重試次數。 |
| `reviewAgent` | string | coordinator | 負責 review 輸出的 agent。可設為嚴格的 QA agent 進行對抗性審查。 |
| `reviewBudget` | float64 | `0.2` | review LLM 呼叫的費用預算（美元）。 |
| `fallback` | string | `"smart"` | 備援策略：`"smart"`（LLM 路由）或 `"coordinator"`（永遠使用預設 agent）。 |
| `rules` | RoutingRule[] | `[]` | 在 LLM 分類前評估的關鍵字／正規表達式路由規則。 |
| `bindings` | RoutingBinding[] | `[]` | 頻道／使用者／guild 對應 agent 的綁定（最高優先級，最先評估）。 |

### `rules` — `RoutingRule`

| 欄位 | 類型 | 說明 |
|---|---|---|
| `agent` | string | 目標 agent 名稱。 |
| `keywords` | string[] | 不區分大小寫的關鍵字。任一符合即路由至此 agent。 |
| `patterns` | string[] | Go 正規表達式模式。任一符合即路由至此 agent。 |

### `bindings` — `RoutingBinding`

| 欄位 | 類型 | 說明 |
|---|---|---|
| `channel` | string | 平台：`"telegram"`、`"discord"`、`"slack"` 等。 |
| `userId` | string | 該平台的使用者 ID。 |
| `channelId` | string | 頻道或對話 ID。 |
| `guildId` | string | Guild／伺服器 ID（僅 Discord）。 |
| `agent` | string | 目標 agent 名稱。 |

---

## Session

控制多輪互動中對話上下文的維護與壓縮方式。

```json
{
  "session": {
    "contextMessages": 20,
    "compactAfter": 30,
    "compactKeep": 10,
    "compactTokens": 200000,
    "compaction": {
      "enabled": true,
      "maxMessages": 50,
      "compactTo": 10,
      "model": "haiku",
      "maxCost": 0.02,
      "provider": "claude"
    }
  }
}
```

### `session` — `SessionConfig`

| 欄位 | 類型 | 預設值 | 說明 |
|---|---|---|---|
| `contextMessages` | int | `20` | 注入新任務作為上下文的最近訊息數量上限。 |
| `compactAfter` | int | `30` | 訊息數超過此值時執行壓縮。已棄用，請改用 `compaction.maxMessages`。 |
| `compactKeep` | int | `10` | 壓縮後保留的最新訊息數量。已棄用，請改用 `compaction.compactTo`。 |
| `compactTokens` | int | `200000` | 輸入 token 總數超過此閾值時執行壓縮。 |

### `session.compaction` — `CompactionConfig`

| 欄位 | 類型 | 預設值 | 說明 |
|---|---|---|---|
| `enabled` | bool | `false` | 啟用自動 session 壓縮。 |
| `maxMessages` | int | `50` | session 訊息數超過此值時觸發壓縮。 |
| `compactTo` | int | `10` | 壓縮後保留的最新訊息數量。 |
| `model` | string | `"haiku"` | 用於生成壓縮摘要的 LLM 模型。 |
| `maxCost` | float64 | `0.02` | 每次壓縮呼叫的最大費用（美元）。 |
| `provider` | string | `defaultProvider` | 用於壓縮摘要呼叫的 provider。 |

---

## Task Board

內建的 task board 追蹤工作項目，並可自動將任務派送給 agent。

```json
{
  "taskBoard": {
    "enabled": true,
    "maxRetries": 3,
    "requireReview": false,
    "defaultWorkflow": "",
    "gitCommit": false,
    "gitPush": false,
    "gitPR": false,
    "gitWorktree": false,
    "gitWorkflow": {
      "branchConvention": "{type}/{agent}-{description}",
      "types": ["feat", "fix", "refactor", "chore"],
      "defaultType": "feat",
      "autoMerge": false
    },
    "autoDispatch": {
      "enabled": false,
      "interval": "5m",
      "maxConcurrentTasks": 3,
      "stuckThreshold": "2h",
      "reviewLoop": false
    }
  }
}
```

### `taskBoard` — `TaskBoardConfig`

| 欄位 | 類型 | 預設值 | 說明 |
|---|---|---|---|
| `enabled` | bool | `false` | 啟用 task board。 |
| `maxRetries` | int | `3` | 任務標記失敗前的最大重試次數。 |
| `requireReview` | bool | `false` | 品質關卡：任務必須通過 review 才能標記為完成。 |
| `defaultWorkflow` | string | `""` | 所有自動派送任務使用的 workflow 名稱。空值代表不使用 workflow。 |
| `gitCommit` | bool | `false` | 任務標記完成時自動 commit。 |
| `gitPush` | bool | `false` | commit 後自動 push（需 `gitCommit: true`）。 |
| `gitPR` | bool | `false` | push 後自動建立 GitHub PR（需 `gitPush: true`）。 |
| `gitWorktree` | bool | `false` | 使用 git worktree 隔離任務（消除並行任務間的檔案衝突）。 |
| `idleAnalyze` | bool | `false` | board 閒置時自動執行分析。 |
| `problemScan` | bool | `false` | 任務完成後掃描輸出中潛在的問題。 |

### `taskBoard.autoDispatch` — `TaskBoardDispatchConfig`

| 欄位 | 類型 | 預設值 | 說明 |
|---|---|---|---|
| `enabled` | bool | `false` | 啟用自動輪詢與派送佇列任務。 |
| `interval` | string | `"5m"` | 掃描就緒任務的間隔時間。 |
| `maxConcurrentTasks` | int | `3` | 每次掃描週期最多派送的任務數。 |
| `defaultModel` | string | `""` | 覆蓋自動派送任務的模型。 |
| `maxBudget` | float64 | `0` | 每個任務的最大費用（美元）。`0` 代表不限制。 |
| `defaultAgent` | string | `""` | 未指派任務的備援 agent。 |
| `backlogAgent` | string | `""` | 負責 backlog 分類的 agent。 |
| `reviewAgent` | string | `""` | 負責 review 已完成任務的 agent。 |
| `escalateAssignee` | string | `""` | review 被拒絕的任務指派給此人。 |
| `stuckThreshold` | string | `"2h"` | `doing` 狀態超過此時間的任務會被重置為 `todo`。 |
| `backlogTriageInterval` | string | `"1h"` | 執行 backlog 分類的最小間隔。 |
| `reviewLoop` | bool | `false` | 為派送任務啟用自動 Dev↔QA 循環。 |

### `taskBoard.gitWorkflow` — `GitWorkflowConfig`

| 欄位 | 類型 | 預設值 | 說明 |
|---|---|---|---|
| `branchConvention` | string | `"{type}/{agent}-{description}"` | 分支命名範本。變數：`{type}`、`{agent}`、`{description}`。 |
| `types` | string[] | `["feat","fix","refactor","chore"]` | 允許的分支類型前綴。 |
| `defaultType` | string | `"feat"` | 未指定類型時的預設值。 |
| `autoMerge` | bool | `false` | 任務完成時自動合併回 main（僅在 `gitWorktree: true` 時有效）。 |

---

## Slot Pressure

控制系統接近 `maxConcurrent` 上限時的行為。互動式（人工發起）的 session 保留專屬 slot；背景任務等待空閒。

```json
{
  "slotPressure": {
    "enabled": true,
    "reservedSlots": 2,
    "warnThreshold": 3,
    "nonInteractiveTimeout": "5m",
    "monitorEnabled": false,
    "monitorInterval": "30s"
  }
}
```

### `slotPressure` — `SlotPressureConfig`

| 欄位 | 類型 | 預設值 | 說明 |
|---|---|---|---|
| `enabled` | bool | `false` | 啟用 slot pressure 管理。 |
| `reservedSlots` | int | `2` | 保留給互動式 session 的 slot 數。背景任務不可使用這些 slot。 |
| `warnThreshold` | int | `3` | 可用 slot 少於此值時向使用者發出警告。 |
| `nonInteractiveTimeout` | string | `"5m"` | 背景任務等待 slot 的超時時間。 |
| `pollInterval` | string | `"2s"` | 背景任務檢查空閒 slot 的間隔。 |
| `monitorEnabled` | bool | `false` | 透過通知頻道啟用主動的 slot pressure 警示。 |
| `monitorInterval` | string | `"30s"` | 檢查並發送 pressure 警示的間隔。 |

---

## Workflows

Workflow 定義為目錄中的 YAML 檔案。`workflowDir` 指向該目錄；變數提供預設的範本值。

```json
{
  "workflowDir": "~/.tetora/workspace/workflows/",
  "workflowTriggers": [
    {
      "event": "task.done",
      "workflow": "notify-slack",
      "filter": {"status": "done"}
    }
  ]
}
```

| 欄位 | 類型 | 預設值 | 說明 |
|---|---|---|---|
| `workflowDir` | string | `~/.tetora/workspace/workflows/` | 存放 workflow YAML 檔案的目錄。 |
| `workflowTriggers` | WorkflowTriggerConfig[] | `[]` | 系統事件觸發的自動 workflow。 |

---

## 整合服務

### Telegram

```json
{
  "telegram": {
    "enabled": true,
    "botToken": "$TELEGRAM_BOT_TOKEN",
    "chatID": 123456789,
    "pollTimeout": 30
  }
}
```

| 欄位 | 類型 | 預設值 | 說明 |
|---|---|---|---|
| `enabled` | bool | `false` | 啟用 Telegram bot。 |
| `botToken` | string | `""` | 從 @BotFather 取得的 Telegram bot token。支援 `$ENV_VAR`。 |
| `chatID` | int64 | `0` | 傳送通知的 Telegram 聊天或群組 ID。 |
| `pollTimeout` | int | `30` | 接收訊息的長輪詢逾時時間（秒）。 |

### Discord

```json
{
  "discord": {
    "enabled": true,
    "botToken": "$DISCORD_BOT_TOKEN",
    "guildID": "123456789",
    "channelIDs": ["111111111"],
    "mentionChannelIDs": ["222222222"],
    "notifyChannelID": "333333333",
    "showProgress": true,
    "routes": {
      "111111111": {"agent": "engineer"}
    }
  }
}
```

| 欄位 | 類型 | 預設值 | 說明 |
|---|---|---|---|
| `enabled` | bool | `false` | 啟用 Discord bot。 |
| `botToken` | string | `""` | Discord bot token。支援 `$ENV_VAR`。 |
| `guildID` | string | `""` | 限定於特定 Discord 伺服器（guild）。 |
| `channelIDs` | string[] | `[]` | bot 回覆所有訊息的頻道 ID（無需 `@` 提及）。 |
| `mentionChannelIDs` | string[] | `[]` | bot 僅在被 `@` 提及時才回覆的頻道 ID。 |
| `notifyChannelID` | string | `""` | 任務完成通知的頻道（每個任務建立一個 thread）。 |
| `showProgress` | bool | `true` | 在 Discord 中顯示即時的「Working...」串流訊息。 |
| `webhooks` | map[string]string | `{}` | 僅用於對外通知的命名 webhook URL。 |
| `routes` | map[string]DiscordRouteConfig | `{}` | 頻道 ID 對應 agent 名稱的路由映射（按頻道路由）。 |

### Slack

```json
{
  "slack": {
    "enabled": true,
    "botToken": "$SLACK_BOT_TOKEN",
    "signingSecret": "$SLACK_SIGNING_SECRET",
    "appToken": "$SLACK_APP_TOKEN",
    "defaultChannel": "C0123456789"
  }
}
```

| 欄位 | 類型 | 預設值 | 說明 |
|---|---|---|---|
| `enabled` | bool | `false` | 啟用 Slack bot。 |
| `botToken` | string | `""` | Slack bot OAuth token（`xoxb-...`）。支援 `$ENV_VAR`。 |
| `signingSecret` | string | `""` | 用於請求驗證的 Slack signing secret。支援 `$ENV_VAR`。 |
| `appToken` | string | `""` | Socket Mode 用的 Slack app-level token（`xapp-...`）。選填。支援 `$ENV_VAR`。 |
| `defaultChannel` | string | `""` | 對外通知的預設頻道 ID。 |

### 對外 Webhooks

```json
{
  "webhooks": [
    {
      "url": "https://hooks.example.com/tetora",
      "headers": {"Authorization": "$WEBHOOK_TOKEN"},
      "events": ["success", "error"]
    }
  ]
}
```

| 欄位 | 類型 | 預設值 | 說明 |
|---|---|---|---|
| `url` | string | 必填 | Webhook 端點 URL。 |
| `headers` | map[string]string | `{}` | 要包含的 HTTP 標頭。值支援 `$ENV_VAR`。 |
| `events` | string[] | 全部 | 要傳送的事件：`"success"`、`"error"`、`"timeout"`、`"all"`。空值代表全部。 |

### 傳入 Webhooks

傳入 webhook 讓外部服務透過 HTTP POST 觸發 Tetora 任務。

```json
{
  "incomingWebhooks": {
    "github": {
      "secret": "$GITHUB_WEBHOOK_SECRET",
      "agent": "engineer",
      "prompt": "A GitHub event occurred: {{.Body}}"
    }
  }
}
```

### 通知頻道

用於將任務事件路由到不同 Slack／Discord 端點的命名通知頻道。

```json
{
  "notifications": [
    {
      "name": "alerts",
      "type": "slack",
      "webhookUrl": "$SLACK_ALERTS_WEBHOOK",
      "events": ["error"],
      "minPriority": "high"
    }
  ]
}
```

| 欄位 | 類型 | 預設值 | 說明 |
|---|---|---|---|
| `name` | string | `""` | 在任務 `channel` 欄位使用的命名參照（如 `"discord:alerts"`）。 |
| `type` | string | 必填 | `"slack"` 或 `"discord"`。 |
| `webhookUrl` | string | 必填 | Webhook URL。支援 `$ENV_VAR`。 |
| `events` | string[] | 全部 | 依事件類型過濾：`"all"`、`"error"`、`"success"`。 |
| `minPriority` | string | 全部 | 最低優先級：`"critical"`、`"high"`、`"normal"`、`"low"`。 |

---

## Store（範本市集）

```json
{
  "store": {
    "enabled": true,
    "registryUrl": "https://registry.tetora.dev/v1",
    "authToken": "$TETORA_STORE_TOKEN"
  }
}
```

### `store` — `StoreConfig`

| 欄位 | 類型 | 預設值 | 說明 |
|---|---|---|---|
| `enabled` | bool | `false` | 啟用範本商店。 |
| `registryUrl` | string | `"https://registry.tetora.dev/v1"` | 瀏覽與安裝範本的遠端 registry URL。 |
| `authToken` | string | `""` | registry 的驗證 token。支援 `$ENV_VAR`。 |

---

## 費用與警示

### `costAlert` — `CostAlertConfig`

```json
{
  "costAlert": {
    "dailyLimit": 10.0,
    "weeklyLimit": 50.0,
    "dailyTokenLimit": 1000000,
    "action": "warn"
  }
}
```

| 欄位 | 類型 | 預設值 | 說明 |
|---|---|---|---|
| `dailyLimit` | float64 | `0` | 每日費用上限（美元）。`0` 代表不限制。 |
| `weeklyLimit` | float64 | `0` | 每週費用上限（美元）。`0` 代表不限制。 |
| `dailyTokenLimit` | int | `0` | 每日 token 總量上限（輸入 + 輸出）。`0` 代表不限制。 |
| `action` | string | `"warn"` | 超過上限時的動作：`"warn"`（記錄並通知）或 `"pause"`（封鎖新任務）。 |

### `estimate` — `EstimateConfig`

執行任務前的費用預估。

| 欄位 | 類型 | 預設值 | 說明 |
|---|---|---|---|
| `confirmThreshold` | float64 | `1.00` | 預估費用超過此美元值時，提示確認。 |
| `defaultOutputTokens` | int | `500` | 實際用量未知時，輸出 token 的備援預估值。 |

### `budgets` — `BudgetConfig`

agent 層級與團隊層級的費用預算。

---

## 日誌記錄

```json
{
  "logging": {
    "level": "info",
    "format": "text",
    "file": "~/.tetora/runtime/logs/tetora.log",
    "maxSizeMB": 50,
    "maxFiles": 5
  }
}
```

### `logging` — `LoggingConfig`

| 欄位 | 類型 | 預設值 | 說明 |
|---|---|---|---|
| `level` | string | `"info"` | 日誌等級：`"debug"`、`"info"`、`"warn"`、`"error"`。 |
| `format` | string | `"text"` | 日誌格式：`"text"`（人類可讀）或 `"json"`（結構化）。 |
| `file` | string | `runtime/logs/tetora.log` | 日誌檔案路徑，相對於 runtime 目錄。 |
| `maxSizeMB` | int | `50` | 日誌檔案輪替前的最大大小（MB）。 |
| `maxFiles` | int | `5` | 保留的輪替日誌檔案數量。 |

---

## 安全性

### `dashboardAuth` — `DashboardAuthConfig`

```json
{
  "dashboardAuth": {
    "enabled": true,
    "username": "admin",
    "password": "$DASHBOARD_PASSWORD"
  }
}
```

| 欄位 | 類型 | 預設值 | 說明 |
|---|---|---|---|
| `enabled` | bool | `false` | 在 dashboard 上啟用 HTTP Basic Auth。 |
| `username` | string | `"admin"` | Basic auth 使用者名稱。 |
| `password` | string | `""` | Basic auth 密碼。支援 `$ENV_VAR`。 |
| `token` | string | `""` | 替代方案：以 cookie 傳遞的靜態 token。 |

### `tls` — `TLSConfig`

```json
{
  "tls": {
    "certFile": "/etc/tetora/cert.pem",
    "keyFile": "/etc/tetora/key.pem"
  }
}
```

| 欄位 | 類型 | 說明 |
|---|---|---|
| `certFile` | string | TLS 憑證 PEM 檔路徑。與 `keyFile` 同時設定時啟用 HTTPS。 |
| `keyFile` | string | TLS 私鑰 PEM 檔路徑。 |

### `rateLimit` — `RateLimitConfig`

| 欄位 | 類型 | 預設值 | 說明 |
|---|---|---|---|
| `enabled` | bool | `false` | 啟用每個 IP 的請求速率限制。 |
| `maxPerMin` | int | `60` | 每個 IP 每分鐘的最大 API 請求數。 |

### `securityAlert` — `SecurityAlertConfig`

| 欄位 | 類型 | 預設值 | 說明 |
|---|---|---|---|
| `enabled` | bool | `false` | 在驗證重複失敗時啟用安全警示。 |
| `failThreshold` | int | `10` | 時間窗口內達到此失敗次數時發出警示。 |
| `failWindowMin` | int | `5` | 滑動時間窗口（分鐘）。 |

### `approvalGates` — `ApprovalGateConfig`

在特定工具執行前要求人工核准。

```json
{
  "approvalGates": {
    "enabled": true,
    "timeout": 120,
    "tools": ["bash", "write_file"],
    "autoApproveTools": ["read_file"]
  }
}
```

| 欄位 | 類型 | 預設值 | 說明 |
|---|---|---|---|
| `enabled` | bool | `false` | 啟用核准關卡。 |
| `timeout` | int | `120` | 等待核准的秒數，逾時則取消。 |
| `tools` | string[] | `[]` | 執行前需要核准的工具名稱。 |
| `autoApproveTools` | string[] | `[]` | 啟動時預先核准的工具（永不提示）。 |

---

## 可靠性

### `circuitBreaker` — `CircuitBreakerConfig`

```json
{
  "circuitBreaker": {
    "enabled": true,
    "failThreshold": 5,
    "successThreshold": 2,
    "openTimeout": "30s"
  }
}
```

| 欄位 | 類型 | 預設值 | 說明 |
|---|---|---|---|
| `enabled` | bool | `true` | 啟用 provider 切換的 circuit breaker。 |
| `failThreshold` | int | `5` | 連續失敗幾次後開啟 circuit。 |
| `successThreshold` | int | `2` | 半開狀態下成功幾次後關閉 circuit。 |
| `openTimeout` | string | `"30s"` | 開啟狀態持續此時間後嘗試半開。 |

### `fallbackProviders`

```json
{
  "fallbackProviders": ["claude", "openai"]
}
```

預設 provider 失敗時，依序嘗試的全域備援 provider 清單。

### `heartbeat` — `HeartbeatConfig`

```json
{
  "heartbeat": {
    "enabled": true,
    "interval": "30s",
    "stallThreshold": "5m",
    "timeoutWarnRatio": 0.8,
    "autoCancel": false,
    "notifyOnStall": true
  }
}
```

| 欄位 | 類型 | 預設值 | 說明 |
|---|---|---|---|
| `enabled` | bool | `false` | 啟用 agent heartbeat 監控。 |
| `interval` | string | `"30s"` | 檢查執行中任務是否停滯的間隔。 |
| `stallThreshold` | string | `"5m"` | 此時間內無輸出即視為任務停滯。 |
| `timeoutWarnRatio` | float64 | `0.8` | 經過時間超過任務逾時此比例時發出警告。 |
| `autoCancel` | bool | `false` | 自動取消停滯超過 `2x stallThreshold` 的任務。 |
| `notifyOnStall` | bool | `true` | 偵測到任務停滯時發送通知。 |

### `retention` — `RetentionConfig`

控制舊資料的自動清理。

```json
{
  "retention": {
    "history": 90,
    "sessions": 30,
    "auditLog": 365,
    "logs": 14,
    "workflows": 90,
    "outputs": 30
  }
}
```

| 欄位 | 類型 | 預設值 | 說明 |
|---|---|---|---|
| `history` | int | `90` | 保留任務執行歷史的天數。 |
| `sessions` | int | `30` | 保留 session 資料的天數。 |
| `auditLog` | int | `365` | 保留稽核日誌條目的天數。 |
| `logs` | int | `14` | 保留日誌檔案的天數。 |
| `workflows` | int | `90` | 保留 workflow 執行記錄的天數。 |
| `reflections` | int | `60` | 保留 reflection 記錄的天數。 |
| `sla` | int | `90` | 保留 SLA 檢查記錄的天數。 |
| `trustEvents` | int | `90` | 保留信任事件記錄的天數。 |
| `handoffs` | int | `60` | 保留 agent 交接／訊息記錄的天數。 |
| `queue` | int | `7` | 保留離線佇列項目的天數。 |
| `versions` | int | `180` | 保留設定版本快照的天數。 |
| `outputs` | int | `30` | 保留 agent 輸出檔案的天數。 |
| `uploads` | int | `7` | 保留上傳檔案的天數。 |
| `memory` | int | `30` | 過時記憶條目被歸檔前的天數。 |
| `claudeSessions` | int | `3` | 保留 Claude CLI session 產物的天數。 |
| `piiPatterns` | string[] | `[]` | 用於儲存內容中 PII 遮蔽的正規表達式模式。 |

---

## 免打擾時段與摘要

```json
{
  "quietHours": {
    "enabled": true,
    "start": "23:00",
    "end": "08:00",
    "tz": "Asia/Taipei",
    "digest": true
  },
  "digest": {
    "enabled": true,
    "time": "08:00",
    "tz": "Asia/Taipei"
  }
}
```

### `quietHours` — `QuietHoursConfig`

| 欄位 | 類型 | 預設值 | 說明 |
|---|---|---|---|
| `enabled` | bool | `false` | 啟用免打擾時段。此時間窗口內的通知會被抑制。 |
| `start` | string | `""` | 免打擾開始時間（本地時間，`"HH:MM"` 格式）。 |
| `end` | string | `""` | 免打擾結束時間（本地時間）。 |
| `tz` | string | 本地 | 時區，例如 `"Asia/Taipei"`、`"UTC"`。 |
| `digest` | bool | `false` | 免打擾時段結束時，傳送被抑制通知的摘要。 |

### `digest` — `DigestConfig`

| 欄位 | 類型 | 預設值 | 說明 |
|---|---|---|---|
| `enabled` | bool | `false` | 啟用排程的每日摘要。 |
| `time` | string | `"08:00"` | 傳送摘要的時間（`"HH:MM"`）。 |
| `tz` | string | 本地 | 時區。 |

---

## 工具

```json
{
  "tools": {
    "maxIterations": 10,
    "timeout": 120,
    "toolOutputLimit": 10240,
    "toolTimeout": 30,
    "defaultProfile": "standard",
    "builtin": {
      "bash": true,
      "web_search": false
    },
    "webSearch": {
      "provider": "brave",
      "apiKey": "$BRAVE_API_KEY",
      "maxResults": 5
    },
    "vision": {
      "provider": "anthropic",
      "apiKey": "$ANTHROPIC_API_KEY",
      "model": "claude-opus-4-5"
    }
  }
}
```

### `tools` — `ToolConfig`

| 欄位 | 類型 | 預設值 | 說明 |
|---|---|---|---|
| `maxIterations` | int | `10` | 每個任務的最大工具呼叫迭代次數。 |
| `timeout` | int | `120` | 全域工具引擎逾時時間（秒）。 |
| `toolOutputLimit` | int | `10240` | 每個工具輸出的最大字元數（超過此限制將被截斷）。 |
| `toolTimeout` | int | `30` | 每個工具的執行逾時時間（秒）。 |
| `defaultProfile` | string | `"standard"` | 預設工具設定檔名稱。 |
| `builtin` | map[string]bool | `{}` | 依名稱啟用或停用個別內建工具。 |
| `profiles` | map[string]ToolProfile | `{}` | 自訂工具設定檔。 |
| `trustOverride` | map[string]string | `{}` | 依工具名稱覆蓋信任等級。 |

### `tools.webSearch` — `WebSearchConfig`

| 欄位 | 類型 | 預設值 | 說明 |
|---|---|---|---|
| `provider` | string | `""` | 搜尋 provider：`"brave"`、`"tavily"`、`"searxng"`。 |
| `apiKey` | string | `""` | provider 的 API 金鑰。支援 `$ENV_VAR`。 |
| `baseURL` | string | `""` | 自訂端點（用於自架的 searxng）。 |
| `maxResults` | int | `5` | 回傳的最大搜尋結果數。 |

### `tools.vision` — `VisionConfig`

| 欄位 | 類型 | 預設值 | 說明 |
|---|---|---|---|
| `provider` | string | `""` | 視覺 provider：`"anthropic"`、`"openai"`、`"google"`。 |
| `apiKey` | string | `""` | API 金鑰。支援 `$ENV_VAR`。 |
| `model` | string | `""` | 視覺 provider 的模型名稱。 |
| `maxImageSize` | int | `5242880` | 最大圖片大小（位元組，預設 5 MB）。 |
| `baseURL` | string | `""` | 自訂 API 端點。 |

---

## MCP（Model Context Protocol）

### `mcpConfigs`

命名的 MCP server 設定。每個鍵是 MCP 設定名稱；值為完整的 MCP JSON 設定。Tetora 會將這些設定寫入暫存檔，並透過 `--mcp-config` 傳遞給 claude 執行檔。

```json
{
  "mcpConfigs": {
    "playwright": {
      "mcpServers": {
        "playwright": {
          "command": "npx",
          "args": ["@playwright/mcp@latest"]
        }
      }
    }
  }
}
```

### `mcpServers`

由 Tetora 直接管理的簡化 MCP server 定義。

```json
{
  "mcpServers": {
    "my-server": {
      "command": "python",
      "args": ["/path/to/server.py"],
      "env": {"API_KEY": "$MY_API_KEY"},
      "enabled": true
    }
  }
}
```

| 欄位 | 類型 | 預設值 | 說明 |
|---|---|---|---|
| `command` | string | 必填 | 可執行指令。 |
| `args` | string[] | `[]` | 指令參數。 |
| `env` | map[string]string | `{}` | 程序的環境變數。值支援 `$ENV_VAR`。 |
| `enabled` | bool | `true` | 此 MCP server 是否啟用。 |

---

## Prompt 預算

控制系統 prompt 各區段的最大字元預算。當 prompt 意外被截斷時可調整此設定。

```json
{
  "promptBudget": {
    "soulMax": 8000,
    "rulesMax": 4000,
    "knowledgeMax": 8000,
    "skillsMax": 4000,
    "maxSkillsPerTask": 3,
    "contextMax": 16000,
    "totalMax": 40000
  }
}
```

### `promptBudget` — `PromptBudgetConfig`

| 欄位 | 類型 | 預設值 | 說明 |
|---|---|---|---|
| `soulMax` | int | `8000` | agent soul／個性 prompt 的最大字元數。 |
| `rulesMax` | int | `4000` | workspace 規則的最大字元數。 |
| `knowledgeMax` | int | `8000` | 知識庫內容的最大字元數。 |
| `skillsMax` | int | `4000` | 注入 skill 的最大字元數。 |
| `maxSkillsPerTask` | int | `3` | 每個任務最多注入的 skill 數量。 |
| `contextMax` | int | `16000` | session 上下文的最大字元數。 |
| `totalMax` | int | `40000` | 整個系統 prompt 大小的硬性上限（所有區段合計）。 |

---

## Agent 通訊

控制巢狀子 agent 派送（agent_dispatch 工具）。

```json
{
  "agentComm": {
    "enabled": true,
    "maxConcurrent": 3,
    "defaultTimeout": 900,
    "maxDepth": 3,
    "maxChildrenPerTask": 5
  }
}
```

### `agentComm` — `AgentCommConfig`

| 欄位 | 類型 | 預設值 | 說明 |
|---|---|---|---|
| `enabled` | bool | `false` | 啟用巢狀子 agent 呼叫的 `agent_dispatch` 工具。 |
| `maxConcurrent` | int | `3` | 全域最大並行 `agent_dispatch` 呼叫數。 |
| `defaultTimeout` | int | `900` | 子 agent 的預設逾時時間（秒）。 |
| `maxDepth` | int | `3` | 子 agent 的最大巢狀深度。 |
| `maxChildrenPerTask` | int | `5` | 每個父任務的最大並行子 agent 數。 |

---

## 範例

### 最簡設定

使用 Claude CLI provider 的最簡設定：

```json
{
  "claudePath": "/usr/local/bin/claude",
  "maxConcurrent": 3,
  "listenAddr": "127.0.0.1:8991",
  "apiToken": "$TETORA_API_TOKEN",
  "agents": {
    "coordinator": {
      "soulFile": "SOUL.md",
      "model": "sonnet",
      "description": "General-purpose agent."
    }
  }
}
```

### 多 Agent 設定（含 Smart Dispatch）

```json
{
  "claudePath": "/usr/local/bin/claude",
  "maxConcurrent": 5,
  "defaultModel": "sonnet",
  "defaultTimeout": "30m",
  "defaultBudget": 2.0,
  "defaultPermissionMode": "acceptEdits",
  "listenAddr": "127.0.0.1:8991",
  "apiToken": "$TETORA_API_TOKEN",
  "defaultWorkdir": "~/workspace",
  "agents": {
    "coordinator": {
      "soulFile": "SOUL.md",
      "model": "sonnet",
      "description": "Coordinator. Handles planning, research, and coordination.",
      "keywords": ["plan", "research", "coordinate", "summarize"]
    },
    "engineer": {
      "soulFile": "team/engineer/SOUL.md",
      "model": "sonnet",
      "description": "Engineer. Handles coding, debugging, and infrastructure.",
      "keywords": ["code", "debug", "deploy"]
    },
    "creator": {
      "soulFile": "team/creator/SOUL.md",
      "model": "sonnet",
      "description": "Creator. Handles writing, documentation, and content.",
      "keywords": ["write", "blog", "translate"]
    }
  },
  "smartDispatch": {
    "enabled": true,
    "coordinator": "coordinator",
    "defaultAgent": "coordinator",
    "classifyBudget": 0.1,
    "classifyTimeout": "30s",
    "rules": [
      {
        "agent": "engineer",
        "keywords": ["bug", "error", "deploy", "CI/CD", "docker"],
        "patterns": ["(?:fix|resolve)\\s+(?:bug|issue|error)"]
      },
      {
        "agent": "creator",
        "keywords": ["blog post", "documentation", "README", "translation"]
      }
    ]
  },
  "costAlert": {
    "dailyLimit": 10.0,
    "action": "warn"
  },
  "logging": {
    "level": "info",
    "format": "text"
  }
}
```

### 完整設定（所有主要區段）

```json
{
  "claudePath": "/usr/local/bin/claude",
  "maxConcurrent": 5,
  "defaultModel": "sonnet",
  "defaultTimeout": "30m",
  "defaultBudget": 2.0,
  "defaultPermissionMode": "acceptEdits",
  "listenAddr": "127.0.0.1:8991",
  "apiToken": "$TETORA_API_TOKEN",

  "providers": {
    "claude": {
      "type": "claude-cli",
      "path": "/usr/local/bin/claude"
    }
  },

  "agents": {
    "coordinator": {
      "soulFile": "SOUL.md",
      "model": "sonnet",
      "description": "Coordinator and general-purpose agent."
    }
  },

  "smartDispatch": {
    "enabled": true,
    "coordinator": "coordinator",
    "defaultAgent": "coordinator",
    "rules": []
  },

  "session": {
    "contextMessages": 20,
    "compaction": {
      "enabled": true,
      "maxMessages": 50,
      "compactTo": 10,
      "model": "haiku"
    }
  },

  "taskBoard": {
    "enabled": true,
    "autoDispatch": {
      "enabled": true,
      "interval": "5m",
      "maxConcurrentTasks": 3
    },
    "gitCommit": true,
    "gitPush": false
  },

  "slotPressure": {
    "enabled": true,
    "reservedSlots": 2,
    "warnThreshold": 3,
    "nonInteractiveTimeout": "5m"
  },

  "telegram": {
    "enabled": false,
    "botToken": "$TELEGRAM_BOT_TOKEN",
    "chatID": 0,
    "pollTimeout": 30
  },

  "discord": {
    "enabled": false,
    "botToken": "$DISCORD_BOT_TOKEN"
  },

  "slack": {
    "enabled": false,
    "botToken": "$SLACK_BOT_TOKEN",
    "signingSecret": "$SLACK_SIGNING_SECRET"
  },

  "store": {
    "enabled": true,
    "registryUrl": "https://registry.tetora.dev/v1"
  },

  "costAlert": {
    "dailyLimit": 10.0,
    "weeklyLimit": 50.0,
    "action": "warn"
  },

  "logging": {
    "level": "info",
    "format": "text",
    "maxSizeMB": 50,
    "maxFiles": 5
  },

  "retention": {
    "history": 90,
    "sessions": 30,
    "logs": 14
  },

  "heartbeat": {
    "enabled": true,
    "stallThreshold": "5m",
    "autoCancel": false
  },

  "dashboardAuth": {
    "enabled": false
  },

  "promptBudget": {
    "soulMax": 8000,
    "rulesMax": 4000,
    "knowledgeMax": 8000,
    "totalMax": 40000
  }
}
```
