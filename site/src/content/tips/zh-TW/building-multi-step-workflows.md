---
title: "建立多步驟工作流程 — 平行 Agent、共享上下文"
lang: zh-TW
date: "2026-04-21"
excerpt: "大多數 agent 工作流程不是線性的。學習如何平行執行步驟、在 agent 之間傳遞輸出，設計更快、更有效率的工作流程。"
description: "Tetora 多步驟 agent 工作流程實作指南：繪製 DAG、同時啟動平行步驟，並在 agent 之間自動傳遞上下文。"
---

## 問題：用線性思維面對平行世界

大多數 agent 工作流程並不是嚴格按順序執行的。以常見的「研究後發文」流程為例：某個 agent 在蒐集市場情報的同時，另一個 agent 其實可以開始起草文章大綱——只有最後的整合步驟才需要兩份輸出同時到位。

如果你把所有步驟排成一列——步驟一、等待、步驟二、等待、步驟三——你白白浪費了執行效率。但若完全不管相依性就全部平行啟動，需要上游輸出的步驟就會失敗或產出不完整的結果。

關鍵在於：清楚知道**哪些步驟彼此獨立**，哪些步驟需要**上游任務的共享上下文**。

## 先畫出工作流程，再下令執行

在 dispatch 任何任務之前，先把相依關係草圖畫出來。同一層級的步驟可以平行執行，下層步驟必須等待上層完成。

```
intel-gather（hisui）    outline-draft（kohaku）
        \                        /
         \                      /
          → final-article（kohaku） → post-to-discord（spinel）
```

最上層兩個分支平行執行。最終文章步驟等待兩者完成後才開始，發文步驟則等文章完成。

## 一次啟動平行步驟

```bash
# 同時啟動兩個獨立步驟
INTEL_ID=$(tetora dispatch --task "蒐集市場情報" --agent hisui --json | jq -r '.id')
OUTLINE_ID=$(tetora dispatch --task "起草文章大綱" --agent kohaku --json | jq -r '.id')

# 最終步驟等到兩個上游任務都完成才執行
tetora dispatch --task "撰寫完整文章" --agent kohaku \
  --depends-on "$INTEL_ID,$OUTLINE_ID" \
  --on-failure abort
```

Tetora 同時啟動 `hisui` 和 `kohaku`。文章撰寫步驟維持 `waiting` 狀態，直到**兩者都完成**為止。兩個各需 5 分鐘的步驟，總共只需 5 分鐘——而不是 10 分鐘。

## 在步驟之間傳遞上下文

每個完成的任務會將輸出寫入共享的任務儲存區。在 `inherit_outputs` 中引用上游任務 ID，系統就會在啟動下一個 agent 之前，自動將上游結果注入它的提示詞中：

```json
{
  "task": "撰寫完整文章",
  "agent": "kohaku",
  "dependsOn": ["intel-task-id", "outline-task-id"],
  "context": {
    "inherit_outputs": ["intel-task-id", "outline-task-id"]
  }
}
```

不需要手動複製貼上。Tetora 會替你把上游輸出帶入下一步。

## 使用建議

- 保持平行分支彼此獨立——如果兩個步驟同時寫入同一個檔案，會發生衝突
- 執行複雜流程之前，用 `tetora workflow visualize` 先把 DAG 渲染出來確認
- 任務命名要具體，它們會顯示在 `tetora task status` 中，出問題時方便追蹤
- 通知步驟搭配 `--on-failure continue`，確保即使上游失敗，團隊也能收到通知

設計良好的多步驟工作流程就像整潔的程式碼：一眼就能看懂結構，每個部分只負責一件事。
