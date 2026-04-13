---
title: "Tetora v2.2.4 — Discord 可靠性、Worktree 安全鎖與 Dispatch 強化"
lang: zh-TW
date: "2026-04-13"
tag: release
readTime: "~5 分鐘"
excerpt: "v2.2.4 加固執行時穩定性：Discord 現在能重試失敗的訊息發送並自動修復過期 Session，Worktree 鎖防止 Bash 工具進入永久失效狀態，分派的任務在 HTTP 斷線後繼續執行，Coordinator 的 findings 不再被截斷。"
description: "Tetora v2.2.4 Release Notes：Discord 發送重試、Stale Session 自動修復、Worktree Session 鎖、HTTP Dispatch 解耦、Provider 容錯、Coordinator 截斷修復、每個 Agent 的併發上限設定。"
---

v2.2.4 是一個聚焦穩定性的版本。大多數改動在順利運行時是無感的——但它們正是系統在高負載、Provider 切換和長時間 Agent Session 下保持穩定的原因。

> **TL;DR：** Discord 現在能重試失敗的發送並自動修復過期 Session。Session 鎖防止 Worktree 清理破壞 Bash 工具。分派的任務在 HTTP 斷線後繼續執行。Provider 錯誤被分類並自動重試。Findings 摘要不再被截斷。`maxTasksPerAgent` 可為每個 Agent 設定併發上限。

---

## Discord：高壓下的可靠性

Discord 是這次版本改動最多的介面。三個修復同時上線：

### 發送重試

Discord 訊息發送在遇到暫時性失敗時現在會自動重試。在此修復之前，長篇 Agent 回應中途的一次 API 失敗會導致訊息無聲消失。Tetora 現在採用退避重試，若所有嘗試均失敗則輸出警告日誌。

### 輸出先持久化再分塊

Agent 輸出現在會先寫入磁碟，再分割成 Discord 訊息區塊送出。過去，分塊中途崩潰可能導致訊息部分送達且無法復原。有了持久化機制，重啟後可以從上次寫入的檢查點繼續。

### Stale Session 自動修復

當 Discord Session 因切換 Provider、更換機器或重啟 Daemon 而過期時，Tetora 現在能自動偵測並修復。過去的行為需要手動執行 `/reset` 或重啟設定。

```
[discord] stale session detected for agent hisui — recovering
[discord] session recovered: channel 1234567890 → agent hisui
```

使用者不需要任何操作。

---

## Agent 安全：Worktree Session 鎖

當 Agent 使用 git worktree 時，Tetora 現在會對該 worktree 路徑取得 Session 鎖。清理作業——包括排程維護和 `tetora worktree prune` 指令——會等待鎖釋放後再繼續。

**為什麼這很重要：** 過去，如果 Worktree 在 Claude Session 正在使用時被刪除，Bash 工具會進入永久失效狀態，直到 Session 結束。Agent 看起來在運行，但已無法執行任何 Shell 指令。這對長時間任務來說尤其麻煩。

此修復引入了 `SessionLockFile` 常數和一個 Advisory Lock 機制：

```go
// Tetora 在 Worktree 中啟動 Claude Session 時取得此鎖
const SessionLockFile = ".tetora-session.lock"

// 清理作業在刪除前檢查鎖
func pruneWorktree(path string) error {
    if isLocked(path) {
        return ErrSessionActive
    }
    return os.RemoveAll(path)
}
```

如果清理作業發現鎖定的 Worktree，會跳過並記錄警告。下次 prune 執行時（Session 已結束），才會清理。

---

## HTTP：Dispatch Context 解耦

分派的任務現在在獨立的 Context 中執行，與建立它們的 HTTP Request 無關。

修復前，長時間執行的 Agent 任務會繼承 `/api/dispatch` HTTP Request 的 Context。如果 HTTP 客戶端斷線——或上游 Proxy 逾時——Context 就會被取消，任務在執行中途被強制終止。

修復方式是為每個分派的任務建立獨立的 Context：

```go
// 修復前：HTTP Request 斷線時任務也死亡
taskCtx := r.Context()

// 修復後：任務獨立執行
taskCtx := context.WithoutCancel(r.Context())
```

HTTP Response 在任務加入佇列後立即返回。無論客戶端連線狀態如何，任務都會執行到完成。

---

## Provider 容錯

### Claude 錯誤分類

Tetora 現在能區分暫時性的 Claude API 錯誤（速率限制、暫時過載）和永久性失敗（無效 API Key、帳號問題）。暫時性錯誤會觸發指數退避的自動重試；永久性錯誤則立即呈現，不浪費重試次數。

### Codex 配額偵測

Codex 的配額和用量上限錯誤——可能出現在 stdout 或 stderr——現在能被正確偵測和處理。偵測到配額錯誤時，Tetora 會在退避等待後重試，而不是直接標記任務失敗。

```
[provider] codex quota exceeded — retrying in 45s (attempt 2/3)
```

---

## Coordinator：不再截斷 Findings

Coordinator 的 findings 摘要過去在傳遞給多 Agent 鏈中的下一個 Agent 時，會被截斷到 500 個字元。這導致 Agent 輸出內容豐富時，接收方只能根據不完整的資訊做決策。

500 字元的上限已移除。Findings 現在完整傳遞於 Agent 之間。

---

## 每個 Agent 的併發上限

`config.json` 中新增 `maxTasksPerAgent` 欄位，可限制單一 Agent 同時執行的任務數：

```json
{
  "agents": [
    {
      "name": "hisui",
      "role": "researcher",
      "maxTasksPerAgent": 2
    }
  ]
}
```

當 Agent 達到上限時，新任務會進入佇列等待，而非立即分派。這能防止突發的並行請求降低單一 Agent 的效能——對於受速率限制的 API Key 或本機硬體執行的 Agent 尤其重要。

預設值（若未設定）為無限制，保持向後相容。

---

## Workflow 強化

兩個修復提升了 Workflow 引擎的可靠性：

**Template ref 驗證** — 參照不存在步驟或 Agent 的 Workflow Template 現在會在分派時立即報錯，提供清晰的錯誤訊息，而不是在執行中途無聲失敗。

**DB 寫入正確性** — `InitWorkflowRunsTable` 等寫入操作過去使用 `db.Query` 而非 `db.Exec`。雖然對 SQLite 功能上等效，但語意不正確，且在高負載時會產生連線池警告。所有寫入路徑現在都使用 `db.Exec`。

---

## 其他修復

- **Workspace 符號連結追蹤** — `tetora workspace files` 現在在列出檔案時會追蹤符號連結，與 Agent 瀏覽 Workspace 的行為一致。
- **Session 壓縮 URL 保留** — URL 和唯一識別碼（雜湊值、ID）現在在 Session 壓縮過程中被保留，防止長 Session 中的參照斷裂。
- **Exit-0 警告日誌** — Runner 現在在 CLI 指令以代碼 0 結束但無任何輸出時輸出 `WARN` 日誌，區分靜默成功與靜默失敗。
- **ctx 傳播** — Context 取消現在正確貫穿 goroutine 內部的 DB 呼叫，防止任務取消時的 Context 洩漏。

---

## 升級方式

```bash
tetora upgrade
```

單一二進位檔。無外部依賴。支援 macOS / Linux / Windows。

[在 GitHub 查看完整 Changelog](https://github.com/TakumaLee/Tetora/releases/tag/v2.2.4)
