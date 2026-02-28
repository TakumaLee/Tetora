# Discord 多工使用指南

Tetora 在 Discord 上支援透過 **Thread + `/focus`** 實現多工並行對話，每個 thread 擁有獨立的 session 和 agent 綁定。

---

## 基本概念

### 主 Channel — 單一 Session

每個 Discord channel 只有 **一個 active session**，所有訊息共用同一個對話上下文。

- Session key 格式：`discord:{channelID}`
- 同一個 channel 裡所有人的訊息都會進入同一個 session
- 對話歷史會持續累積，直到你用 `!new` 重置

### Thread — 獨立 Session

Discord thread 可以透過 `/focus` 綁定到特定 agent，獲得完全獨立的 session。

- Session key 格式：`agent:{agentName}:discord:thread:{guildID}:{threadID}`
- 與主 channel 的 session 完全隔離，上下文互不干擾
- 每個 thread 可以綁定不同的 agent

---

## 指令

| 指令 | 位置 | 說明 |
|---|---|---|
| `/focus <agent>` | Thread 內 | 將這個 thread 綁定到指定 agent，建立獨立 session |
| `/unfocus` | Thread 內 | 解除 thread 的 agent 綁定 |
| `!new` | 主 Channel | 封存當前 session，下一則訊息會開啟全新 session |

---

## 多工操作流程

### Step 1：建立 Discord Thread

在主 channel 對某條訊息右鍵 → **Create Thread**（或使用 Discord 的建立 thread 功能）。

### Step 2：在 Thread 內綁定 Agent

```
/focus ruri
```

綁定成功後，這個 thread 內的所有對話都會：
- 使用 ruri 的 SOUL.md 角色設定
- 擁有獨立的對話歷史
- 不影響主 channel 的 session

### Step 3：依需求開啟多個 Thread

```
#general (主 channel)              ← 日常對話，1 個 session
  └─ Thread: "重構 auth 模組"      ← /focus kokuyou → 獨立 session
  └─ Thread: "寫這週部落格"        ← /focus kohaku  → 獨立 session
  └─ Thread: "競品分析報告"        ← /focus hisui   → 獨立 session
  └─ Thread: "專案規劃討論"        ← /focus ruri    → 獨立 session
```

每個 thread 都是完全隔離的對話空間，可以同時進行。

---

## 注意事項

### TTL（存活時間）

- Thread 綁定預設 **24 小時**後過期
- 過期後 thread 會回到一般模式（走主 channel 的路由邏輯）
- 可在 config 中調整 `threadBindings.ttlHours`

### 並行限制

- 全局最大並行數由 `maxConcurrent` 控制（預設 8）
- 所有 channel + thread 共用這個上限
- 超過上限的訊息會排隊等待

### 設定啟用

確認 config 中已啟用 thread bindings：

```json
{
  "discord": {
    "threadBindings": {
      "enabled": true,
      "ttlHours": 24
    }
  }
}
```

### 主 Channel 的限制

- 主 channel 無法用 `/focus` 建立第二個 session
- 如需重置對話上下文，使用 `!new`
- 同一個 channel 內同時發送多條訊息會共用 session，上下文可能互相干擾

---

## 使用情境建議

| 情境 | 建議做法 |
|---|---|
| 日常閒聊、簡單問答 | 直接在主 channel 對話 |
| 需要專注討論某個主題 | 開 thread + `/focus` |
| 不同任務指派不同 agent | 每個任務一個 thread，各自 `/focus` 對應 agent |
| 對話上下文太長想重來 | 主 channel 用 `!new`，thread 用 `/unfocus` 再 `/focus` |
| 多人協作同一個話題 | 開一個共用 thread，所有人在 thread 內對話 |
