---
title: "依角色選擇模型 — 草稿用 Sonnet，策略用 Opus"
lang: zh-TW
date: "2026-04-11"
excerpt: "不是每個任務都需要你最強的模型。學會如何讓 Sonnet 處理快速草稿、Opus 負責高風險決策，在不犧牲品質的前提下大幅降低成本。"
description: "學習如何在 Tetora 中依 agent 角色設定模型選擇。將 Claude Sonnet 指定給內容草稿、Claude Opus 用於策略推理——在多 agent 工作流程中平衡速度、品質與成本。"
---

## 問題：用同一個模型做所有事

把所有 agent 都跑在 Opus 上感覺很安全。但這很貴，而且對於初稿撰寫或日誌解析這類任務，Opus 根本是大材小用——你在用推理等級的價格做格式化等級的工作。

反過來，把策略規劃交給 Sonnet，有時會得到缺乏深度的輸出。

解法：**依角色分配模型，而不是依賴預設值。**

---

## Tetora 的模型角色運作方式

Tetora 中每個 agent 的 Soul 檔案裡都有 `role` 欄位。你可以在 `tetora.config.json` 中將角色對應到模型：

```json
{
  "models": {
    "draft": "claude-sonnet-4-6",
    "review": "claude-opus-4-6",
    "strategy": "claude-opus-4-6",
    "parse": "claude-haiku-4-5-20251001"
  },
  "agents": {
    "kohaku": { "role": "draft" },
    "ryuri":  { "role": "review" },
    "hisui":  { "role": "strategy" },
    "jade":   { "role": "parse" }
  }
}
```

當任務被 dispatch 給某個 agent 時，Tetora 會自動使用該角色對應的模型——無需逐次設定。

---

## 實用對應表

| Agent 角色 | 建議模型 | 原因 |
|---|---|---|
| 初稿撰寫 | Sonnet | 快速、適合生成類工作，成本低 |
| 程式碼審查 | Opus | 需要對 diff 進行深度推理 |
| 策略 / 規劃 | Opus | 多步驟推理，盲點更少 |
| 日誌解析 / 標記 | Haiku | 結構化提取，速度最重要 |
| 最終編輯審查 | Opus | 能抓到 Sonnet 遺漏的問題 |

---

## 單次任務覆蓋

有時你需要覆蓋預設值。Dispatch 時使用 `--model` 旗標：

```bash
tetora dispatch --role kohaku --model claude-opus-4-6 "從頭重寫首頁文案"
```

這適合不想更改全域設定的一次性高風險任務。

---

## 成果

以典型的內容工作日為例——5 篇草稿、2 次審查、20 次日誌解析——這套路由模式與全部跑 Opus 相比，token 花費大約減少 40–60%，最終輸出品質沒有明顯差異。

**口訣：Opus 負責決策，Sonnet 負責執行，Haiku 負責提取。**
