---
title: "Claude Code Hooks 整合"
lang: "zh-TW"
order: 3
description: "Integrate with Claude Code Hooks for real-time session observation."
---
# Claude Code Hooks 整合

## 概述

Claude Code Hooks 是內建於 Claude Code 的事件系統，在 session 的關鍵時間點觸發 shell 指令。Tetora 將自身註冊為 hook 接收器，無需輪詢、無需 tmux、也無需注入包裝腳本，即可即時觀察每個執行中的 agent session。

**Hooks 帶來的功能：**

- Dashboard 中的即時進度追蹤（工具呼叫、session 狀態、即時 worker 清單）
- 透過 statusline bridge 進行費用與 token 監控
- 工具使用稽核（哪個 session、哪個目錄執行了哪些工具）
- Session 完成偵測與自動任務狀態更新
- Plan mode 關卡：在 dashboard 中由人工核准計畫前，暫停 `ExitPlanMode`
- 互動式問題路由：`AskUserQuestion` 被重新導向至 MCP bridge，使問題出現在你的聊天平台，而非阻塞終端機

Hooks 是 Tetora v2.0 起建議的整合方式。舊版的 tmux 方法（v1.x）仍可使用，但不支援 plan gate 和問題路由等 hooks 專屬功能。

---

## 架構

```
Claude Code session
  │
  ├── PreToolUse  ──────────────────► Tetora /api/hooks/event
  │   (ExitPlanMode)                  └─► Plan gate: 長輪詢直到獲批准
  │   (AskUserQuestion)               └─► 拒絕：重新導向至 MCP bridge
  │
  ├── PostToolUse ──────────────────► Tetora /api/hooks/event
  │                                   └─► 更新 worker 狀態
  │                                   └─► 偵測計畫檔案寫入
  │
  ├── Stop        ──────────────────► Tetora /api/hooks/event
  │                                   └─► 將 worker 標記為完成
  │                                   └─► 觸發任務完成
  │
  └── Notification ─────────────────► Tetora /api/hooks/event
                                      └─► 轉發至 Discord/Telegram
```

Hook 指令是注入至 Claude Code `~/.claude/settings.json` 的小型 curl 呼叫。每個事件都會透過 `POST /api/hooks/event` 傳送給正在執行的 Tetora daemon。

---

## 設定

### 安裝 hooks

在 Tetora daemon 執行中的情況下：

```bash
tetora hooks install
```

此指令會將條目寫入 `~/.claude/settings.json`，並在 `~/.tetora/mcp/bridge.json` 生成 MCP bridge 設定。

寫入 `~/.claude/settings.json` 的範例內容：

```json
{
  "hooks": {
    "PostToolUse": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "curl -s -X POST http://localhost:8991/api/hooks/event -H 'Content-Type: application/json' -d @-"
          }
        ]
      }
    ],
    "Stop": [ { "hooks": [ { "type": "command", "command": "..." } ] } ],
    "Notification": [ { "hooks": [ { "type": "command", "command": "..." } ] } ],
    "PreToolUse": [
      {
        "matcher": "ExitPlanMode",
        "hooks": [ { "type": "command", "command": "...", "timeout": 600 } ]
      },
      {
        "matcher": "AskUserQuestion",
        "hooks": [ { "type": "command", "command": "..." } ]
      }
    ]
  }
}
```

### 檢查狀態

```bash
tetora hooks status
```

輸出會顯示已安裝的 hook、已登錄的 Tetora 規則數量，以及 daemon 啟動後收到的事件總數。

也可以在 dashboard 中查看：**Engineering Details → Hooks** 顯示相同的狀態，以及即時事件串流。

### 移除 hooks

```bash
tetora hooks remove
```

從 `~/.claude/settings.json` 移除所有 Tetora 條目。現有的非 Tetora hook 會被保留。

---

## Hook 事件

### PostToolUse

在每次工具呼叫完成後觸發。Tetora 用此事件：

- 追蹤 agent 使用的工具（`Bash`、`Write`、`Edit`、`Read` 等）
- 更新即時 worker 清單中的 `lastTool` 和 `toolCount`
- 偵測 agent 寫入計畫檔案的時機（觸發計畫快取更新）

### Stop

在 Claude Code session 結束時觸發（正常完成或取消）。Tetora 用此事件：

- 將 worker 在即時 worker 清單中標記為 `done`
- 向 dashboard 發布完成 SSE 事件
- 觸發 taskboard 任務的後續狀態更新

### Notification

在 Claude Code 發送通知時觸發（如需要權限、長時間暫停）。Tetora 會將這些通知轉發至 Discord/Telegram，並發布至 dashboard SSE 串流。

### PreToolUse: ExitPlanMode（計畫關卡）

當 agent 即將退出 plan mode 時，Tetora 以長輪詢（逾時：600 秒）攔截該事件。計畫內容會被快取，並顯示在 dashboard 的 session 詳情頁面中。

使用者可在 dashboard 核准或拒絕計畫。核准後，hook 回傳，Claude Code 繼續執行。若被拒絕（或逾時），退出操作被阻止，Claude Code 留在 plan mode。

### PreToolUse: AskUserQuestion（問題路由）

當 Claude Code 嘗試以互動方式詢問使用者時，Tetora 會攔截並拒絕預設行為。問題改透過 MCP bridge 路由，出現在你設定的聊天平台（Discord、Telegram 等），讓你無需坐在終端機前即可作答。

---

## 即時進度追蹤

安裝 hooks 後，dashboard 的 **Workers** 面板會顯示即時 session：

| 欄位 | 來源 |
|---|---|
| Session ID | hook 事件中的 `session_id` |
| 狀態 | `working` / `idle` / `done` |
| 最近工具 | 最新的 `PostToolUse` 工具名稱 |
| 工作目錄 | hook 事件中的 `cwd` |
| 工具計數 | 累積的 `PostToolUse` 次數 |
| 費用 / token | Statusline bridge（`POST /api/hooks/usage`） |
| 來源 | 若由 Tetora 派送，則連結至對應任務或 cron job |

費用與 token 資料來自 Claude Code statusline 腳本，該腳本以可設定的間隔向 `/api/hooks/usage` 傳送資料。Statusline 腳本與 hooks 分開——它讀取 Claude Code 狀態列輸出並轉發至 Tetora。

---

## 費用監控

usage 端點（`POST /api/hooks/usage`）接收：

```json
{
  "sessionId": "abc123",
  "costUsd": 0.0042,
  "inputTokens": 8200,
  "outputTokens": 340,
  "contextPct": 12,
  "model": "claude-sonnet-4-5"
}
```

這些資料會顯示在 dashboard Workers 面板中，並彙整至每日費用圖表。當 session 費用超過設定的角色或全域預算時，會觸發預算警示。

---

## 疑難排解

### Hooks 未觸發

**確認 daemon 正在執行：**
```bash
tetora status
```

**確認 hooks 已安裝：**
```bash
tetora hooks status
```

**直接檢查 settings.json：**
```bash
cat ~/.claude/settings.json | grep -A5 "hooks"
```

若 hooks 鍵不存在，重新執行 `tetora hooks install`。

**確認 daemon 可接收 hook 事件：**
```bash
curl -s -X POST http://localhost:8991/api/hooks/event \
  -H "Content-Type: application/json" \
  -d '{"hook_event_name":"Stop","session_id":"test-123"}'
# 預期結果：{"ok":true}
```

若 daemon 未在預期的連接埠監聽，請檢查 `config.json` 中的 `listenAddr`。

### settings.json 的權限錯誤

Claude Code 的 `settings.json` 位於 `~/.claude/settings.json`。若檔案由其他使用者擁有或有嚴格的權限限制：

```bash
ls -la ~/.claude/settings.json
chmod 644 ~/.claude/settings.json
```

### Dashboard workers 面板為空

1. 確認 hooks 已安裝且 daemon 正在執行。
2. 手動啟動一個 Claude Code session 並執行一個工具（如 `ls`）。
3. 檢查 dashboard Workers 面板——session 應在數秒內出現。
4. 若未出現，查看 daemon 日誌：`tetora logs -f | grep hooks`

### 計畫關卡未出現

計畫關卡只在 Claude Code 嘗試呼叫 `ExitPlanMode` 時才會啟動。這只發生在 plan mode session 中（以 `--plan` 啟動，或在角色設定中設定 `permissionMode: "plan"`）。互動式的 `acceptEdits` session 不使用 plan mode。

### 問題未路由至聊天

`AskUserQuestion` 拒絕 hook 需要設定 MCP bridge。重新執行 `tetora hooks install`——它會重新生成 bridge 設定。然後將 bridge 加入 Claude Code 的 MCP 設定：

```bash
cat ~/.tetora/mcp/bridge.json
```

在 `~/.claude/settings.json` 的 `mcpServers` 中加入該檔案作為 MCP server。

---

## 從 tmux（v1.x）遷移

在 Tetora v1.x 中，agent 在 tmux pane 內執行，Tetora 透過讀取 pane 輸出來監控它們。在 v2.0 中，agent 以純 Claude Code 程序執行，Tetora 透過 hooks 觀察它們。

**若你從 v1.x 升級：**

1. 升級後執行一次 `tetora hooks install`。
2. 從 `config.json` 移除所有 tmux session 管理設定（`tmux.*` 鍵現在會被忽略）。
3. 現有的 session 歷史保留在 `history.db` 中——無需遷移。
4. `tetora session list` 指令和 dashboard 的 Sessions 分頁繼續正常運作。

tmux 終端機 bridge（`discord_terminal.go`）仍可用於透過 Discord 進行互動式終端機存取。這與 agent 執行是分開的——它讓你能向執行中的終端機 session 傳送按鍵。Hooks 與終端機 bridge 是互補的，並不互斥。
