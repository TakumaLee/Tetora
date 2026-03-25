---
title: "MCP（Model Context Protocol）整合"
lang: "zh-TW"
order: 5
description: "Expose Tetora capabilities to any MCP-compatible client."
---
# MCP（Model Context Protocol）整合

Tetora 內建 MCP server，讓 AI agent（Claude Code 等）能透過標準 MCP 協定與 Tetora 的 API 互動。

## 架構

```
Claude Code  ──stdio──>  tetora mcp-server  ──HTTP──>  Tetora Daemon
  (client)                (bridge process)              (localhost:8991)
```

MCP server 是一個 **stdio JSON-RPC 2.0 bridge**——從 stdin 讀取請求、代理至 Tetora 的 HTTP API，並將回應寫入 stdout。Claude Code 以子程序方式啟動它。

## 設定

### 1. 將 MCP server 加入 Claude Code 設定

在 `~/.claude/settings.json` 中新增以下內容：

```json
{
  "mcpServers": {
    "tetora": {
      "command": "/Users/you/.tetora/bin/tetora",
      "args": ["mcp-server"]
    }
  }
}
```

請將路徑替換為你實際的 `tetora` 執行檔位置。可透過以下指令查詢：

```bash
which tetora
# 或
ls ~/.tetora/bin/tetora
```

### 2. 確認 Tetora daemon 正在執行

MCP bridge 代理至 Tetora HTTP API，因此 daemon 必須處於執行中：

```bash
tetora start
```

### 3. 驗證

重新啟動 Claude Code。MCP 工具會以 `tetora_` 前綴顯示為可用工具。

## 可用工具

### 任務管理

| 工具 | 說明 |
|------|-------------|
| `tetora_taskboard_list` | 列出看板任務。選用過濾條件：`project`、`assignee`、`priority`。 |
| `tetora_taskboard_update` | 更新任務（狀態、負責人、優先級、標題）。需要 `id`。 |
| `tetora_taskboard_comment` | 為任務新增留言。需要 `id` 和 `comment`。 |

### 記憶體

| 工具 | 說明 |
|------|-------------|
| `tetora_memory_get` | 讀取記憶條目。需要 `agent` 和 `key`。 |
| `tetora_memory_set` | 寫入記憶條目。需要 `agent`、`key` 和 `value`。 |
| `tetora_memory_search` | 列出所有記憶條目。選用過濾條件：`role`。 |

### 派送

| 工具 | 說明 |
|------|-------------|
| `tetora_dispatch` | 將任務派送至另一個 agent。建立新的 Claude Code session。需要 `prompt`。選用：`agent`、`workdir`、`model`。 |

### 知識庫

| 工具 | 說明 |
|------|-------------|
| `tetora_knowledge_search` | 搜尋共用知識庫。需要 `q`。選用：`limit`。 |

### 通知

| 工具 | 說明 |
|------|-------------|
| `tetora_notify` | 透過 Discord/Telegram 發送通知給使用者。需要 `message`。選用：`level`（info/warn/error）。 |
| `tetora_ask_user` | 透過 Discord 向使用者提問並等待回覆（最長 6 分鐘）。需要 `question`。選用：`options`（快速回覆按鈕，最多 4 個）。 |

## 工具詳情

### tetora_taskboard_list

```json
{
  "project": "tetora",
  "assignee": "kokuyou",
  "priority": "P0"
}
```

所有參數皆為選填。回傳任務的 JSON 陣列。

### tetora_taskboard_update

```json
{
  "id": "TASK-42",
  "status": "in_progress",
  "assignee": "kokuyou",
  "priority": "P1",
  "title": "New title"
}
```

只有 `id` 是必填。其他欄位只在提供時才更新。狀態值：`todo`、`in_progress`、`review`、`done`。

### tetora_taskboard_comment

```json
{
  "id": "TASK-42",
  "comment": "Started working on this",
  "author": "kokuyou"
}
```

### tetora_dispatch

```json
{
  "prompt": "Fix the broken CSS on the dashboard sidebar",
  "agent": "kokuyou",
  "workdir": "/path/to/project",
  "model": "sonnet"
}
```

只有 `prompt` 是必填。若省略 `agent`，Tetora 的 smart dispatch 會路由至最適合的 agent。

### tetora_ask_user

```json
{
  "question": "Should I proceed with the database migration?",
  "options": ["Yes", "No", "Skip for now"]
}
```

這是一個**阻塞式呼叫**——它等待使用者透過 Discord 回覆，最長等待 6 分鐘。使用者會看到問題及可選的快速回覆按鈕，也可以輸入自訂答案。

## CLI 指令

### 管理外部 MCP Server

Tetora 也可作為 MCP **host**，連接至外部 MCP server：

```bash
# 列出已設定的 MCP server
tetora mcp list

# 顯示特定 server 的完整設定
tetora mcp show <name>

# 新增 MCP server
tetora mcp add <name> --command CMD [--args A1,A2] [--env K=V,K2=V2]

# 移除 server 設定
tetora mcp remove <name>

# 測試 server 連線
tetora mcp test <name>
```

### 執行 MCP Bridge

```bash
# 啟動 MCP bridge server（通常由 Claude Code 啟動，不需手動執行）
tetora mcp-server
```

首次執行時，會在 `~/.tetora/mcp/bridge.json` 生成含有正確執行檔路徑的設定檔。

## 設定

`config.json` 中與 MCP 相關的設定：

| 欄位 | 類型 | 預設值 | 說明 |
|------|------|---------|-------------|
| `mcpServers` | object | `{}` | 外部 MCP server 設定的映射（名稱 → {command, args, env}）。 |

Bridge server 從主設定檔讀取 `listenAddr` 和 `apiToken` 以連接至 daemon。

## 認證

若 `config.json` 中設定了 `apiToken`，MCP bridge 會自動在所有對 daemon 的 HTTP 請求中帶入 `Authorization: Bearer <token>`。不需要額外的 MCP 層級認證。

## 疑難排解

**工具未出現在 Claude Code 中：**
- 確認 `settings.json` 中的執行檔路徑正確
- 確認 Tetora daemon 正在執行（`tetora start`）
- 查看 Claude Code 日誌中的 MCP 連線錯誤

**「HTTP 401」錯誤：**
- `config.json` 中的 `apiToken` 必須相符。Bridge 會自動讀取它。

**「connection refused」錯誤：**
- Daemon 未執行，或 `listenAddr` 不符。預設：`127.0.0.1:8991`。

**`tetora_ask_user` 逾時：**
- 使用者有 6 分鐘可透過 Discord 回覆。請確認 Discord bot 已連線。
