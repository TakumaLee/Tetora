---
title: "跨 Session 的持久記憶——讓 Agent 記住你說過的事"
lang: zh-TW
date: "2026-04-25"
excerpt: "每次新 session 都不應該像失憶症發作。了解 Tetora 的雙層記憶系統，讓 agent 在對話之間保留事實、決策與背景脈絡。"
description: "了解如何在 Tetora 中設定持久記憶，讓 agent 跨 session 記住專案背景、過去的決策與使用者偏好——不再每次都從頭解釋。"
---

## 問題：每次都從零開始的 Agent

預設情況下，每個 agent session 都是冷啟動。Agent 不知道你昨天討論了什麼、上週做了哪些決定、目前的專案狀態是什麼。每次 session 的開頭都要花時間重新說明那些根本沒變的背景。

這不只是麻煩——它是一種複利式的消耗。專案越長，agent 需要補的背景就越多。

---

## Tetora 的雙層記憶系統

Tetora 將記憶分成兩層，各有不同的存取模式：

| 層級 | 位置 | 載入方式 | 最適合 |
|---|---|---|---|
| **Auto-memory** | `~/.claude/.../memory/` | 每次 session 自動載入 | 跨 session 事實、偏好、學到的規則 |
| **Workspace memory** | `memory/`（專案根目錄） | 由 agent 主動 `Read` 載入 | 研究資料、agent 日記、domain 資料 |

這個分層很重要。Auto-memory 小而常駐——它塑造 agent 的思考方式。Workspace memory 大而刻意——agent 有需要時才拉取。

---

## 設定 Auto-Memory

Auto-memory 檔案是純 Markdown，在 session 啟動時載入。加入你希望每個 agent 都知道的事實：

```markdown
<!-- memory/auto/project-context.md -->
# 專案：Tetora
狀態：開發中，已上線 beta 用戶
技術棧：Go 後端、Astro 前端、Claude API
決策記錄：log 解析使用 Haiku（2026-04-11）
Owner 偏好：系統輸出不用 emoji
```

全域事實放在 `~/.claude/memory/`，專案範圍的事實放在 `<project>/.claude/memory/`。

---

## 在 Session 中途寫入記憶

當 agent 發現值得保留的事——使用者的糾正、新的限制條件、關鍵決策——應該寫入對應的記憶檔案：

```bash
# 將新的 lesson 加進專案記憶
echo "- Polymarket API 速率限制：10 req/s（確認於 2026-04-25）" \
  >> .claude/memory/domain-facts.md
```

實務上，agent 偵測到糾正或值得記錄的模式時會自動執行這個動作。你也可以手動觸發：

```
/remember staging DB 用的是 port 5433，不是 5432
```

---

## Workspace Memory：需要時再拉取

對於較大的結構化背景——過去的研究、agent 日記、週報——使用 workspace memory。Agent 明確載入它：

```markdown
<!-- 在 agent Soul file 中 -->
## Session 開始
- 若任務涉及 Polymarket，讀取 memory/domain/polymarket-notes.md
- 若要規劃新的研究任務，讀取 memory/agents/hisui/diary.md
```

這讓 session 背景預設保持精簡，需要時才加深。

---

## 成果

維護良好記憶的 agent，會從無狀態工具逐漸變成長期協作者。背景不斷累積：每次 session 建立在上一次的基礎上，決策得以保留，偏好也會延續。

**原則：如果你會對 agent 說同一件事兩次，它就該進記憶檔。**
