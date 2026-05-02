title: "跨 Session 的持久記憶 — 讓 Agent 記住昨天"
lang: zh-TW
date: "2026-04-30"
excerpt: "Session 結束後，Agent 預設忘掉一切。學習如何用 Tetora 的持久記憶層，讓 Agent 跨重啟、跨間隔保持上下文感知。"
description: "學習如何在 Tetora 設定持久記憶，讓 AI Agent 跨 session 保留知識。涵蓋記憶檔案結構、寫入紀律，以及讓 Agent 上下文保持新鮮而不臃腫的最佳實踐。"
---

## 問題：每次都從頭開始的 Agent

每次 Claude session 結束，工作記憶就會重置。偶發任務還好，但對每天運作的 Agent——追蹤財務、管理任務、撰寫內容——每天早上一片空白是 bug，不是 feature。

你希望 Agent 記得：昨天做了什麼決策、專案目前的狀態、上週你說過的事。

---

## Tetora 的持久記憶運作方式

Tetora 將記憶分成兩層：

| 層級 | 路徑 | 用途 |
|---|---|---|
| **Auto-memory** | `~/.claude/projects/{project}/memory/` | 跨 session 事實，自動載入 |
| **Workspace memory** | repo 內的 `memory/` | 研究資料、日誌、領域知識——按需載入 |

Auto-memory 由 Agent 寫入，每次 session 開始時自動讀取。Workspace memory 內容更豐富，但需要明確的 `Read` 呼叫。

---

## 寫入記憶檔案

在 auto-memory 目錄下建立帶有 frontmatter 的 `.md` 檔案：

```markdown
---
name: project-status
description: Kronos 專案目前階段與下一個里程碑
type: project
---

Phase 2 進行中。API 整合於 2026-04-28 完成。
下一步：UI 打磨衝刺，目標 2026-05-10 上線。

**Why:** 讓各 Agent 同步狀態，不需讀取完整 git log。
**How to apply:** 任何規劃或 dispatch 任務前先參照。
```

接著在 `MEMORY.md`（索引檔）加一行指標：

```markdown
- [project-status.md](project-status.md) — Kronos 階段 + 下一個里程碑
```

---

## 記憶類型

Tetora 使用四種記憶類型，讓索引保持聚焦：

- `user` — 使用者是誰、專業背景、偏好
- `feedback` — 過去 session 的修正與確認方法
- `project` — 進行中的工作、截止日、決策（請用絕對日期）
- `reference` — 外部系統指標（Linear、Grafana 等）

不要存程式碼模式、檔案結構或 git 歷史——這些從原始碼讀總是更新。

---

## 保持精簡

記憶臃腫是真實問題。遵守這些規則：

```text
✅ 存：非顯而易見的事實、偏好、含 Why 的決策
✅ 存：絕對日期（不要寫「下週四」）
❌ 不存：可從程式碼推導的東西
❌ 不存：短暫的任務狀態（用 todos 代替）
❌ 不存：外部內容原文（只摘要）
```

索引檔 `MEMORY.md` 超過 200 行會截斷——每行保持在約 150 字以內。

---

## 結果

持久記憶建好之後，Agent 醒來就知道專案狀態、你的偏好、以及過去 session 的決策——不需要你每次重新簡報。

**一個主題一個檔案，索引一行。這就是整套系統。**