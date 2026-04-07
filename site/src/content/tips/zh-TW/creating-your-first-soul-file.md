---
title: "建立你的第一個 Agent Soul 檔案"
lang: zh-TW
date: "2026-04-07"
excerpt: "用 Soul 檔案為你的 AI agent 定義一致的身分、語氣與個性——讓它永遠知道自己是誰、該怎麼說話。"
description: "學習如何在 Tetora 中建立 Soul 檔案，定義 AI agent 的個性、溝通風格與行為規則。含完整步驟與範例。"
---

## 問題：每次回應都像不同人在寫

沒有固定身分，agent 就會飄移。這個 session 正式，下個 session 隨興。語氣準則被遺忘，不同 agent 的風格互相蓋過，產出的內容像五個不同人寫的。

**Soul 檔案**解決這個問題。它是一份 Markdown 文件，錨定 agent 的個性、聲音與行為限制——在 session 開始時載入，整個過程持續參照。

## Soul 檔案裡寫什麼

Soul 檔案涵蓋四個面向：

1. **身分** — 名字、角色、agent 在意什麼
2. **語氣** — 表達風格、語言習慣、要避免的事
3. **關係** — 與團隊成員的互動方式
4. **限制** — 硬規則（絕不做 X，一定做 Y）

## 建立步驟

在 Tetora workspace 中建立 `agents/{name}/SOUL.md`：

```bash
mkdir -p agents/kohaku
touch agents/kohaku/SOUL.md
```

這是一份最小可用的 Soul 檔案：

```markdown
# SOUL.md — 琥珀

## 身分
- 名字：琥珀
- 角色：創作擔當——負責貼文、文章、社群文案
- 核心信念：好想法值得被聽見。我的工作是讓它們被看見。

## 語氣
- 溫暖、直接、故事優先
- 用比喻讓技術概念變得好懂
- 不用行話，用了就解釋
- 主語言：繁體中文，自然混入英文/日文

## 關係
- 琉璃（主管）：最終審核權威——琉璃說不行，就是不行
- 翡翠（情報）：主要情報來源——寫之前先讀報告
- 黒曜（工程）：遇到技術橋接難題就問「用白話怎麼說？」

## 限制
- 未經琉璃審核不得發布
- 不誇大數據，不用標題黨
- 每篇貼文都要可追溯來源
```

## 接入 Agent 設定

在 agent 設定中引用 Soul 檔案，讓它自動載入：

```json
{
  "agent": "kohaku",
  "soul": "agents/kohaku/SOUL.md",
  "model": "claude-opus-4-6",
  "role": "content"
}
```

Agent 開啟 session 時，Soul 檔案會注入 system context。每次回應都會通過它定義的身分過濾。

## 效果

Soul 檔案到位之後，你不再需要修正語氣，只需要修正內容。個性變得穩定——跨 session、跨任務、跨協作者。你的 agent 聽起來像*它自己*，而不是通用 AI。

從身分和限制開始寫，觀察到語氣飄移再補語氣規則。Soul 檔案會跟著 agent 一起成長。
