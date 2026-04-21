---
title: "Tetora v2.4 — 會自我學習的 Agent、動態規則注入與 Slot 感知重試"
lang: zh-TW
date: "2026-04-20"
tag: release
readTime: "~6 分鐘"
excerpt: "v2.3–v2.4 系列完成了 agent 學習閉環：Lesson 升格流程讓 agent 主動提案新規則、動態關鍵字匹配取代了 58KB 靜態規則注入、Slot 感知重試終於讓 taskboard 在高負載下可靠運行。"
description: "Tetora v2.4 release notes：lesson 升格流水線、agent 反思歷史查詢、動態規則注入、per-task 重試策略、session 摘要持久化、War Room v2 儀表板，以及 Discord 失憶修復。"
---

v2.3 到 v2.4 這一系列的發布，加在一起是一次完整的升級：agent 累積經驗、只載入需要的規則，並且在發生錯誤時無需人工介入就能恢復。

> **TL;DR：** Lesson 升格流水線完成了學習閉環——agent 現在能主動將反覆出現的模式提案為規則。規則注入改為關鍵字匹配，消除了 50KB 的硬截斷問題。Slot 感知重試支援 per-task 策略並加入 stall 偵測。Session compact summary 現在持久化，防止 Discord 失憶。War Room 全面翻新並新增 /wr Discord 指令。

---

## War Room v2（v2.3.0）

War Room 儀表板從頭重建。新的 v2 網格佈局同時顯示所有 fronts，包含時效性指標、手動覆蓋標記與依賴鏈——無需在不同畫面間切換。

### /wr Discord 指令

所有儀表板操作現在都有對應的 Discord slash 指令：

```
/wr list                      — 列出所有 fronts 和當前狀態
/wr status <front> <status>   — 從對話切換狀態
/wr intel <front> <note>      — 在 front 的 intel 側欄追加備注
/wr export                    — 複製所有 fronts 的 markdown 摘要
```

在 session 中無法開啟 Web UI 時，`/wr` 提供完整的操作對等性。

### 自動更新器

`card_type: auto` 的 fronts 現在有背景 cron 按設定排程自動更新。儀表板頂端的「⚡ 立即執行」按鈕可以觸發臨時更新，無需等待下一個排程。儀表板標題列顯示上次與下次更新時間。

### UI CRUD

現在可以直接從 Web 介面建立、封存和刪除 fronts——不需要手動編輯 `status.json`。Modal 中的「Depends On」欄位渲染為依賴鏈 chip，點擊可跳轉到被依賴的 front。

---

## Session 連續性：修復 Discord 失憶（v2.4.1）

Session compaction 之後，agent 常常會遺忘剛才的對話——要求用戶重複資訊、遺失任務狀態，表現得好像 session 剛剛開始一樣。

根本原因是三個問題疊加：

1. **摘要太短。** Compaction 目標只有 300–500 字，具體細節被大量刪除。
2. **注入後即刪除。** 摘要在第一次注入後立刻從記憶體中刪除，若寫入失敗就永久遺失。
3. **Per-message cap 太嚴。** 800 字的上限讓 summarizer 輸入品質下降。

v2.4.1 三者一次修復：

- 摘要目標提升至 1500–2000 字，明確要求保留逐字識別符、具體數字與未完成事項。
- 摘要改為持久化儲存，不再注入後刪除。下次 compaction 會覆蓋同一個 key——不累積過期記錄，也不因失敗清空歷史。
- Per-message cap 提升至 1600 字。Summarizer timeout 從 90s 提升至 180s。

Compact summary 注入時現在附帶明確標頭，指示 agent 在要求用戶重複資訊前先查閱摘要。

---

## Slot 感知重試（v2.4.1，P0）

Taskboard 重試迴圈累積了三個 bug，在實際工作負載下導致不可靠：

- 所有 agent slot 全滿時，任務仍可進入重試佇列，無法執行。
- 缺少 stall 偵測——若 agent 在任務執行中途崩潰，任務可能永遠停在「running」。
- 設定了 `require_human_confirm` 時，系統每分鐘追加一條新 comment 直到人工回應，造成 comment 風暴。

v2.4.1 修復以上三個問題，並引入 per-task 重試策略：

```json
{
  "max": 1,
  "require_human_confirm": true
}
```

對任何不應靜默自動重試的任務套用此設定：

```bash
tetora task create \
  --title "高風險 migration" \
  --retry-policy '{"max":1,"require_human_confirm":true}'
```

達到 `max` 上限後，任務停止並等待人工明確確認後才繼續。Slot guard 確保重試只在有可用 slot 時觸發。Stall 偵測在指派的 agent 停止回報進度後將任務標記為失敗。

---

## 動態規則注入（v2.4.2）

規則以前是靜態注入到每個任務——整個 `rules/` 目錄，無論是否相關。在成熟的 workspace 中，這是 58KB 的內容，讓 system prompt 膨脹，稀釋了 agent 真正需要的信號。

更糟的是：超過 50KB 時，規則區塊會被靜默跳過。新增一條規則反而可能導致所有規則消失。

v2.4.2 將靜態注入替換為關鍵字匹配注入：

```yaml
# rules/INDEX.md（新增一次即可）
- file: dispatch-workflow.md
  keywords: [dispatch, agent, task, assign]
  always: false

- file: git-safety.md
  keywords: [git, commit, push, branch, merge]
  always: false

- file: language-compliance.md
  keywords: []
  always: true   # 每個任務都注入
```

注入器組建「Active Rules」區塊，上限為 `MaxRulesPerTask`（預設 3 條）和 `RulesMax`（8000 字元）。只有關鍵字出現在任務描述或標題中的規則才會被包含；`always: true` 的規則無條件注入。

50KB 硬截斷消除。新的 200KB 軟性警告取而代之——系統優雅降級，不再靜默丟棄內容。

Agent 反思自動萃取的 lesson 現在寫入 `memory/auto-lessons.md`（升格等待佇列），而非 `rules/`（已驗證的治理規則）。這防止未審核的 lesson 佔用規則注入預算。

---

## Lesson 升格流水線（v2.4.2）

Agent 每次任務後都會萃取 lesson——但這些 lesson 停留在 markdown 檔案中，沒有路徑可以成為可執行的規則。累積 44 條 lesson 卻沒有審查機制，這個模式明顯是壞的。

v2.4.2 用 CLI 驅動、人工把關的升格流水線完成閉環：

```bash
# 查看哪些 lesson 在足夠多的不同任務中反覆出現，值得升格
tetora lessons scan --threshold 3

# 將候選項生成到 rules/auto-promoted-YYYYMMDD.md（預設 dry-run）
tetora lessons promote --dry-run

# 審查超過 90 天未更新的規則
tetora lessons audit --age 90
```

流水線在新的 `lesson_events` DB table 中追蹤每次 `ExtractAutoLesson` 觸發，獨立於 markdown key——即使檔案被編輯，出現次數統計仍然準確。

### Agent 反思歷史查詢

Agent 現在可以在對話中查詢自己的歷史，無需執行 CLI：

```
reflection_search     — 依關鍵字、agent、任務、分數篩選反思記錄
reflection_get        — 用 task ID 取得單筆反思
lesson_history        — 追蹤某個 lesson key 的每次出現歷史
lesson_candidates     — 預覽升格等待佇列
```

在此之前，重複的錯誤對 agent 來說是不可見的，因為它們無法問「這種情況以前發生過嗎？」現在可以——並且能在上下文中給出答案。

---

## 其他變更

- **Discord session ref 更新** — 每次 `runSingleTask` 完成後，Tetora 現在會重新取得 Discord session reference。此前若 session 在執行中被封存，後續輸出會發送到已封存的 session 並靜默丟失。
- **Taskboard `next_retry_at` 迴圈修復** — 重試排程器中三個邊緣情況導致任務無限循環或排程到過去時間。全部在 v2.4.2 修復（#90）。
- **Docs viewer** — War Room 儀表板現在內建文件閱讀器，可在不離開儀表板的情況下閱讀 workspace 文件。

---

## 升級方式

```bash
tetora upgrade
```

單一 binary，無外部依賴。支援 macOS / Linux / Windows。

[在 GitHub 查看完整 changelog](https://github.com/TakumaLee/Tetora/releases/tag/v2.4.2)
