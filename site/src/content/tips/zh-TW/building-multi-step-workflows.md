---
title: "建立多步驟工作流程 — 將 Agent 串成可靠的 Pipeline"
lang: zh-TW
date: "2026-04-14"
excerpt: "不只是單一 agent 任務。學習如何設計多步驟工作流程，讓每個 agent 接力傳遞，將複雜流程變成可重複執行的 pipeline。"
description: "學習如何在 Tetora 建立多步驟 agent 工作流程。串接 agent、在步驟間傳遞 context，設計可靠的 pipeline 來處理複雜的重複任務。"
---

讓單一 agent 回答問題很有用。但讓一系列 agent 自動完成研究、起草、審閱、發布——那才是真正的威力。Tetora 的工作流程系統讓你做到這一點。

## 單次 Prompt 的問題

當一個任務有多個階段，讓單一 agent 全部處理往往會以可預見的方式出錯：context 溢位、早期決策限制了後期選項、某一步失敗後也沒有乾淨的恢復機制。

更好的做法：將工作拆成階段，每個階段指定一個專注的 agent，讓每一步的輸出成為下一步的輸入。

## 定義多步驟工作流程

Tetora 的工作流程是一系列任務的序列，每個步驟都有明確的角色、輸入和輸出。以下是一個最小範例：

```json
{
  "workflow": "weekly-report",
  "steps": [
    {
      "id": "research",
      "agent": "hisui",
      "prompt": "收集過去 7 天最重要的 5 則 AI 新聞。輸出包含 title、url 和一行摘要的 JSON 陣列。",
      "output_key": "news_items"
    },
    {
      "id": "draft",
      "agent": "kohaku",
      "prompt": "使用 {{news_items}} 中的新聞項目撰寫 300 字摘要，格式為 Markdown。",
      "depends_on": ["research"],
      "output_key": "draft_md"
    },
    {
      "id": "review",
      "agent": "ruri",
      "prompt": "審閱 {{draft_md}} 中的草稿，確認準確性和語氣，回傳最終版本。",
      "depends_on": ["draft"]
    }
  ]
}
```

`depends_on` 欄位告訴 Tetora，在上游步驟成功完成前不要啟動該步驟。如果 `research` 失敗，`draft` 就不會執行——不浪費 token，不產生破碎的輸出。

## 在步驟間傳遞 Context

每個步驟的 `output_key` 值會成為下游步驟可用的模板變數。在後續任何 prompt 中使用 `{{key_name}}` 即可直接注入前一步的輸出。

這讓每個 agent 的 prompt 保持簡短專注——agent 只讀取它需要的內容，而不是所有歷史記錄。

## 執行工作流程

```bash
tetora run workflow weekly-report
```

Tetora 按照依賴順序執行步驟。沒有共同依賴的獨立步驟會自動並行執行。每個步驟的最終輸出都會記錄到任務記錄中供審閱。

## 提示：為失敗恢復而設計

為步驟取有意義的名稱，並讓每個步驟保持範圍狹窄。如果 `draft` 失敗，你可以單獨重新執行它，而不必重跑 `research`：

```bash
tetora run workflow weekly-report --from draft
```

步驟越短，失敗越快，恢復成本越低。

## 小結

多步驟工作流程是一個人操作者規模化的方式。定義好階段、串好依賴關係，讓 Tetora 處理編排。一個工作流程可靠地跑一次，之後就能每次都可靠地跑。
