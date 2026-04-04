---
title: "Auto-Dispatch 基礎 — 把任務排進 Agent 佇列"
lang: zh-TW
date: "2026-04-02"
excerpt: "學會用 Tetora 的 dispatch 系統排隊任務，讓 Agent 自動領工，不用人工一個一個交接。"
description: "Tetora auto-dispatch 入門指南：如何排隊任務、指定 Agent、用簡單的 JSON 設定建立第一個自動化工作流程。"
---

## 問題所在

你有三件事要讓 Agent 做：研究一個主題、寫草稿、發到 Discord。沒有 dispatch，你就得把每個步驟的輸出複製貼上到下一個終端機，然後手動觸發。

那不叫「agentic」，那叫保母。

Tetora 的 dispatch 系統讓你把工作排進佇列，指定給特定 Agent，然後走人。每個 Agent 自動領取任務、完成後通知下一步。

## Dispatch 的運作方式

Dispatch 的核心就是一個任務佇列。你寫一份 task spec（誰做什麼、有什麼 context），推進佇列，指定的 Agent 就會去認領。

每個任務有三個必填欄位：

```json
{
  "agent": "kokuyou",
  "task": "寫一篇關於 MCP security 的 Twitter thread",
  "context": {
    "source": "intel/MARKETING-WEEKLY.md",
    "tone": "技術但好讀"
  }
}
```

把這個丟到 queue 目錄，Agent daemon 會在下一次 poll（預設 30 秒）自動撿起來。

## 你的第一次 Dispatch

**Step 1 — 定義 task 檔案：**

```bash
cat > tasks/queue/draft-tips-article.json << 'EOF'
{
  "agent": "kohaku",
  "task": "為 Tetora 網站寫一篇 auto-dispatch 的 tips 文章",
  "context": {
    "output_path": "site/src/content/tips/zh-TW/",
    "word_count": "300-500",
    "include_code_example": true
  }
}
EOF
```

**Step 2 — 推進佇列：**

```bash
tetora dispatch push tasks/queue/draft-tips-article.json
```

**Step 3 — 看它跑：**

```bash
tetora dispatch status
# kohaku  draft-tips-article  IN_PROGRESS  started 12s ago
```

就這樣。不用終端機交接，不用複製貼上。

## 串接任務

真正的威力在於：讓一個 Agent 的輸出自動變成下一個的輸入。用 `depends_on` 串接：

```json
{
  "agent": "spinel",
  "task": "把草稿的文章連結發到 Discord #content 頻道",
  "depends_on": "draft-tips-article"
}
```

Spinel 不會在 `draft-tips-article` 完成前啟動。如果上游任務失敗，下游任務會自動取消——不會有半成品發文。

## 核心觀念

Auto-dispatch 移除了「人當中繼」的瓶頸。不需要手動協調 Agent，只要定義好工作流程，把任務推進佇列，系統會按順序執行。從單一 Agent 佇列開始，隨著工作流程成熟再加上依賴關係。

下一步：試試 **用 Cron 排程重複性任務**，讓 dispatch 按時間自動觸發。
