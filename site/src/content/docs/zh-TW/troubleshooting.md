---
title: "疑難排解指南"
lang: "zh-TW"
order: 7
description: "Common issues and solutions for Tetora setup and operation."
---
# 疑難排解指南

本指南涵蓋執行 Tetora 時最常見的問題。每個問題列出的第一個原因通常是最可能的情況。

---

## tetora doctor

永遠從這裡開始。安裝後或發生問題時，執行 `tetora doctor`：

```
=== Tetora Doctor ===

  ✓ Config          /Users/you/.tetora/config.json
  ✓ Claude CLI      claude 1.2.3
  ✓ Provider        claude-cli
  ✓ Port            localhost:8991 in use (daemon running)
  ✓ Telegram        enabled (chatID=123456)
  ✓ Jobs            jobs.json (4 jobs, 3 enabled)
  ✓ History DB      12 tasks
  ✓ Workdir         /Users/you/dev
  ✓ Agent/ruri      Commander
  ✓ Binary          /Users/you/.tetora/bin/tetora
  ✓ Encryption      key configured
  ✓ ffmpeg          available
  ✓ sqlite3         available
  ✓ Agents Dir      /Users/you/.tetora/agents (3 agents)
  ✓ Workspace       /Users/you/.tetora/workspace

All checks passed.
```

每一行代表一項檢查。紅色 `✗` 表示硬性失敗（不修正就無法運作）。黃色 `~` 表示建議（選填但推薦）。

常見失敗檢查的修正方式：

| 失敗的檢查 | 修正方式 |
|---|---|
| `Config: not found` | 執行 `tetora init` |
| `Claude CLI: not found` | 在 `config.json` 中設定 `claudePath`，或安裝 Claude Code |
| `sqlite3: not found` | `brew install sqlite3`（macOS）或 `apt install sqlite3`（Linux） |
| `Agent/name: soul file missing` | 建立 `~/.tetora/agents/{name}/SOUL.md`，或執行 `tetora init` |
| `Workspace: not found` | 執行 `tetora init` 建立目錄結構 |

---

## 「session produced no output」

任務完成但輸出為空。任務自動標記為 `failed`。

**原因一：Context window 過大。** 注入至 session 的 prompt 超過模型的 context 上限。Claude Code 無法容納 context 時會立即退出。

修正：在 `config.json` 中啟用 session 壓縮：

```json
{
  "sessionCompaction": {
    "enabled": true,
    "tokenThreshold": 150000,
    "messageThreshold": 100,
    "strategy": "auto"
  }
}
```

或減少注入任務的 context 量（縮短描述、減少 spec 留言、縮短 `dependsOn` 鏈）。

**原因二：Claude Code CLI 啟動失敗。** `claudePath` 指向的執行檔在啟動時崩潰——通常是 API 金鑰錯誤、網路問題或版本不符。

修正：手動執行 Claude Code 執行檔查看錯誤：

```bash
/usr/local/bin/claude --version
/usr/local/bin/claude -p "hello"
```

修正回報的錯誤後，重試任務：

```bash
tetora task move task-abc123 --status=todo
```

**原因三：Prompt 為空。** 任務有標題但無描述，而標題本身對 agent 而言過於模糊，無法採取行動。Session 執行後，產生的輸出不符合空值檢查，被標記為問題。

修正：新增具體描述：

```bash
tetora task update task-abc123 \
  --description="Create src/ratelimit/bucket.go with a token bucket implementation..."
```

---

## Dashboard 顯示「unauthorized」錯誤

Dashboard 回傳 401，或重新載入後顯示空白頁面。

**原因一：Service Worker 快取了舊的 auth token。** PWA Service Worker 快取回應，包含 auth 標頭。使用新 token 重啟 daemon 後，快取版本已過期。

修正：強制重新整理頁面。Chrome/Safari：

- Mac：`Cmd + Shift + R`
- Windows/Linux：`Ctrl + Shift + R`

或開啟開發者工具 → Application → Service Workers → 點選「Unregister」後重新載入。

**原因二：Referer 標頭不符。** Dashboard 的 auth middleware 會驗證 `Referer` 標頭。來自瀏覽器擴充功能、代理或未帶 `Referer` 標頭的 curl 請求會被拒絕。

修正：直接存取 `http://localhost:8991/dashboard`，不要透過代理。若需要從外部工具存取 API，請使用 API token 而非瀏覽器 session 認證。

---

## Dashboard 未更新

Dashboard 載入了，但活動動態、worker 清單或 task board 停滯不動。

**原因：Service Worker 版本不符。** 執行 `make bump` 更新後，PWA Service Worker 仍提供快取版本的 dashboard JS/HTML。

修正：

1. 強制重新整理（`Cmd + Shift + R` / `Ctrl + Shift + R`）
2. 若無效，開啟開發者工具 → Application → Service Workers → 點選「Update」或「Unregister」
3. 重新載入頁面

**原因：SSE 連線中斷。** Dashboard 透過 Server-Sent Events 接收即時更新。若連線中斷（網路閃斷、筆電休眠），動態更新就會停止。

修正：重新載入頁面。SSE 連線在載入時自動重新建立。

---

## 「排程接近滿載」警告

你在 Discord/Telegram 或 dashboard 通知動態中看到此訊息。

這是 slot pressure 警告。當可用的並行 slot 降至 `warnThreshold`（預設：3）或以下時觸發，表示 Tetora 正在接近容量上限。

**處理方式：**

- 若這是預期中的情況（許多任務正在執行）：不需任何動作，此警告僅供參考。
- 若你並未執行許多任務：檢查是否有任務卡在 `doing` 狀態：

```bash
tetora task list --status=doing
```

- 若你想提高容量：在 `config.json` 中增大 `maxConcurrent`，並相應調整 `slotPressure.warnThreshold`。
- 若互動式 session 被延遲：增大 `slotPressure.reservedSlots`，為互動式使用保留更多 slot。

---

## 任務卡在「doing」

任務顯示 `status=doing`，但沒有任何 agent 在處理它。

**原因一：Daemon 在任務執行中途重啟。** 任務執行時 daemon 被終止。下次啟動時，Tetora 會檢查孤立的 `doing` 任務，若有費用/耗時記錄則還原為 `done`，否則重設為 `todo`。

這是自動處理的——等待下次 daemon 啟動即可。若 daemon 已在執行而任務仍然卡住，heartbeat 或卡住任務重設機制會在 `stuckThreshold`（預設：2h）內處理。

立即強制重設：

```bash
tetora task move task-abc123 --status=todo
```

**原因二：Heartbeat／停滯偵測。** Heartbeat 監控器（`heartbeat.go`）檢查執行中的 session。若 session 在停滯閾值期間內無輸出，會自動取消並將任務移至 `failed`。

檢查任務留言中是否有 `[auto-reset]` 或 `[stall-detected]` 系統留言：

```bash
tetora task show task-abc123 --full
```

**透過 API 手動取消：**

```bash
curl -X POST http://localhost:8991/api/tasks/task-abc123/cancel
```

---

## Worktree 合併失敗

任務完成後移至 `partial-done`，並有類似 `[worktree] merge failed` 的留言。

這表示 agent 在任務分支上的變更與 `main` 產生衝突。

**復原步驟：**

```bash
# 查看任務詳情及建立的分支
tetora task show task-abc123 --full

# 切至專案倉庫
cd /path/to/your/repo

# 手動合併分支
git merge feat/kokuyou-task-abc123

# 在編輯器中解決衝突後 commit
git add .
git commit -m "merge: feat/kokuyou-task-abc123"

# 將任務標記為完成
tetora task move task-abc123 --status=done
```

Worktree 目錄保存在 `~/.tetora/runtime/worktrees/task-abc123/`，直到你手動清除或將任務移至 `done` 為止。

---

## Token 費用過高

Session 使用的 token 超出預期。

**原因一：Context 未被壓縮。** 沒有 session 壓縮，每一輪都會累積完整的對話歷史。長時間執行的任務（許多工具呼叫）會線性增長 context。

修正：啟用 `sessionCompaction`（參見上方「session produced no output」章節）。

**原因二：知識庫或規則檔案過大。** `workspace/rules/` 和 `workspace/knowledge/` 中的檔案會注入至每個 agent prompt。若這些檔案很大，每次呼叫都會消耗大量 token。

修正：
- 稽核 `workspace/knowledge/`——保持個別檔案在 50 KB 以下。
- 將你不常需要的參考資料移出自動注入路徑。
- 執行 `tetora knowledge list` 查看正在注入的內容及其大小。

**原因三：模型路由錯誤。** 昂貴的模型（Opus）被用於例行性任務。

修正：檢查 agent 設定中的 `defaultModel`，為大量任務設定較便宜的預設值：

```json
{
  "taskBoard": {
    "autoDispatch": {
      "defaultModel": "sonnet"
    }
  }
}
```

---

## Provider 逾時錯誤

任務以 `context deadline exceeded` 或 `provider request timed out` 等逾時錯誤失敗。

**原因一：任務逾時設定過短。** 預設逾時對複雜任務可能不足。

修正：在 agent 設定或個別任務中設定更長的逾時：

```json
{
  "roles": {
    "kokuyou": {
      "timeout": "60m"
    }
  }
}
```

或在任務描述中加入更多細節，提高逾時預估值（Tetora 會透過快速模型呼叫，依描述估算逾時）。

**原因二：API 速率限制或競爭。** 太多並行請求打到同一個 provider。

修正：降低 `maxConcurrentTasks`，或新增 `maxBudget` 來限制高費用任務：

```json
{
  "autoDispatch": {
    "maxConcurrentTasks": 2,
    "maxBudget": 3.0
  }
}
```

---

## `make bump` 中斷了 workflow

你在 workflow 或任務執行中途執行了 `make bump`，daemon 在任務進行時重啟。

重啟會觸發 Tetora 的孤立任務復原邏輯：

- 有完成證據（已記錄費用、耗時）的任務還原為 `done`。
- 沒有完成證據且超過寬限期（2 分鐘）的任務重設為 `todo` 以重新派送。
- 在最後 2 分鐘內更新的任務保持不動，等待下次卡住任務掃描。

**確認發生了什麼：**

```bash
tetora task list --status=doing
tetora task list --status=failed
```

查看任務留言中是否有 `[auto-restore]` 或 `[auto-reset]` 條目。

**若需要在有活躍任務時防止 bump**（目前尚無旗標），請在 bump 前確認沒有任務執行中：

```bash
tetora task list --status=doing
# 若為空，可安全 bump
make bump
```

---

## SQLite 錯誤

你在日誌中看到 `database is locked`、`SQLITE_BUSY` 或 `index.lock` 等錯誤。

**原因一：缺少 WAL mode pragma。** 沒有 WAL mode，SQLite 使用排他性檔案鎖，在並行讀寫時導致 `database is locked`。

所有 Tetora DB 呼叫都透過 `queryDB()` 和 `execDB()` 執行，並在前面附加 `PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;`。若你在腳本中直接呼叫 sqlite3，請加入這些 pragma：

```bash
sqlite3 ~/.tetora/history.db \
  "PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; SELECT count(*) FROM tasks;"
```

**原因二：殘留的 `index.lock` 檔案。** Git 操作被中斷時會留下 `index.lock`。Worktree 管理員在開始 git 工作前會檢查殘留的鎖，但崩潰仍可能留下鎖檔。

修正：

```bash
# 尋找殘留的鎖檔
find ~/.tetora/runtime/worktrees -name "index.lock"

# 移除它們（只在沒有 git 操作正在執行時）
rm /path/to/repo/.git/index.lock
```

---

## Discord / Telegram 不回覆

發送給 bot 的訊息沒有得到回覆。

**原因一：頻道設定錯誤。** Discord 有兩個頻道清單：`channelIDs`（直接回覆所有訊息）和 `mentionChannelIDs`（只在被 @ 提及時回覆）。若某個頻道不在任何清單中，訊息會被忽略。

修正：檢查 `config.json`：

```json
{
  "discord": {
    "enabled": true,
    "channelIDs": ["123456789012345678"],
    "mentionChannelIDs": []
  }
}
```

**原因二：Bot token 過期或錯誤。** Telegram bot token 不會過期，但若 bot 被踢出伺服器或 token 被重新生成，Discord token 可能失效。

修正：在 Discord 開發者入口網站重新建立 bot token，並更新 `config.json`。

**原因三：Daemon 未執行。** Bot gateway 只在 `tetora serve` 執行中才有效。

修正：

```bash
tetora status
tetora serve   # 若未執行
```

---

## glab / gh CLI 錯誤

Git 整合因 `glab` 或 `gh` 的錯誤而失敗。

**常見錯誤：`gh: command not found`**

修正：
```bash
brew install gh      # macOS
gh auth login        # 認證
```

**常見錯誤：`glab: You are not logged in`**

修正：
```bash
brew install glab    # macOS
glab auth login      # 以你的 GitLab 帳號認證
```

**常見錯誤：`remote: HTTP Basic: Access denied`**

修正：確認你的 SSH 金鑰或 HTTPS 憑證已針對倉庫主機設定。GitLab：

```bash
glab auth status
ssh -T git@gitlab.com   # 測試 SSH 連線
```

GitHub：

```bash
gh auth status
ssh -T git@github.com
```

**PR/MR 建立成功但指向錯誤的基礎分支**

預設情況下，PR 以倉庫的預設分支（`main` 或 `master`）為目標。若你的 workflow 使用不同的基礎分支，請在任務後 git 設定中明確指定，或確認倉庫在代管平台上的預設分支設定正確。
