---
title: "任務依賴與串接 — 打造更聰明的 Agent 工作流程"
lang: zh-TW
date: "2026-04-04"
excerpt: "等第一個 agent 成功後才派發第二個。用依賴關係串接任務，建立可靠的多步驟 pipeline。"
description: "學習如何在 Tetora 中為 agent 任務設定依賴關係，讓後續步驟只在前置步驟成功後才執行，打造可靠的自動化多步驟工作流程。"
---

## 問題：不該同時執行的步驟

有些工作流程有天然的順序。你想在部署之前跑測試、在 build 之後掃安全漏洞、或只在分析完成後才發報告。如果一次 dispatch 所有步驟卻沒有設定依賴，它們會搶著執行——結果是壞掉的部署、假陽性警報、或是建立在不完整資料上的報告。

Tetora 讓你透過**任務串接與依賴關係**明確表達這些前後關係。

## 任務串接的運作方式

Dispatch 任務時，你可以在 `dependsOn` 欄位填入一個或多個任務 ID。Tetora 的排程器會把有依賴的任務保持在 `waiting` 狀態，等上游任務到達 `done` 才將它加入佇列。

```json
// dispatch payload 範例
{
  "task": "deploy-to-staging",
  "agent": "kokuyou",
  "dependsOn": ["task-abc123"],
  "onFailure": "abort"
}
```

- `dependsOn` — 必須先完成的任務 ID 陣列
- `onFailure: "abort"` — 若任何上游任務失敗，完全跳過此步驟
- `onFailure: "continue"` — 無論如何都執行（適用於清理或通知步驟）

## 實際範例：測試 → 部署 → 通知

這是一個三步驟 pipeline，每個階段都等待前一個完成：

```bash
# 步驟 1：跑測試（無依賴）
tetora dispatch --task "run-test-suite" --agent hisui --output task-id

# 步驟 2：測試通過才部署
tetora dispatch --task "deploy-staging" --agent kokuyou \
  --depends-on $(cat task-id) \
  --on-failure abort

# 步驟 3：無論部署結果如何，都發 Discord 通知
tetora dispatch --task "notify-team" --agent kohaku \
  --depends-on $(cat task-id) \
  --on-failure continue
```

測試 agent 先執行。測試若失敗，部署自動中止。通知步驟永遠觸發——你的團隊無論如何都會收到更新。

## 為什麼這很重要

現代 CI 系統（GitHub Actions、Buildkite）多年來一直用 workflow YAML 做這件事。Tetora 把同樣的邏輯帶進 **agent 原生 pipeline**——這裡的「步驟」不是 shell script，而是有自己記憶體、工具和推理能力的完整 agent。

結果：你可以打造對失敗有韌性、自我記錄、且容易 debug 的 pipeline，因為每個步驟的狀態都被獨立追蹤。

## 小技巧

- 步驟數量不限——依賴關係形成 DAG，不只是線性清單
- 用 `tetora task status <id>` 查看哪個步驟在等待、執行中，或被封鎖
- 結合 cron 觸發，打造全自動的夜間 pipeline
- 永遠明確設定 `onFailure`——預設是 `abort`，但明確設定更保險

從小地方開始：在現有的 dispatch 裡加一個依賴關係，看看 pipeline 如何自動圍繞它組織起來。
