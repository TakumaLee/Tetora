---
title: "Tetora v2.2 — 預設安全、多租戶 Dispatch"
lang: zh-TW
date: "2026-03-30"
tag: release
readTime: "~6 min"
excerpt: "DangerousOpsConfig 在 agent 執行前阻擋危險指令。多租戶 --client flag、Worktree 故障保護、History CLI 失敗分析——v2.2 讓 agent dispatch 進入生產就緒時代。"
description: "Tetora v2.2 新增 DangerousOpsConfig 指令攔截、多租戶 dispatch 隔離、Worktree 故障保護、Self-liveness watchdog 與 History CLI 診斷工具，跨三個 patch 版本共 30 項以上改進。"
---

Tetora v2.2 從三個面向提升標準：**安全防護、執行可靠性、多租戶隔離**。跨越 v2.2.0 到 v2.2.2 三個版本，30 多項改進讓多 agent 並行 dispatch 更加穩健，為企業級部署奠定基礎。

> **TL;DR：** DangerousOpsConfig 在 agent 執行前阻擋破壞性指令。Worktree 隔離全面覆蓋所有任務。全新 History CLI 提供失敗分析工具。`--client` flag 實現多租戶工作空間隔離。Pipeline 重整消滅 zombie 進程。Self-liveness watchdog 讓 daemon 自動重啟。

## 安全第一：DangerousOpsConfig

v2.2 最重要的改變不是新功能——而是防護欄。

**DangerousOpsConfig** 是一個基於規則的指令攔截引擎。在 agent 執行任何 shell 指令之前，Tetora 會先比對可設定的黑名單。一旦匹配，指令在執行前即遭封鎖，不產生任何副作用，也不造成資料損失。

預設封鎖的指令模式：
- `rm -rf`（及各種變體）
- `DROP TABLE`、`DROP DATABASE`
- `git push --force`
- `find ~/`（大範圍 `$HOME` 掃描）

可在 `config.json` 設定自訂 allowlist：

```json
{
  "dangerousOps": {
    "enabled": true,
    "extraPatterns": ["truncate", "kubectl delete"],
    "allowlist": ["rm -rf ./dist"]
  }
}
```

搭配同步修正的 `$HOME` 阻擋機制，agent 再也無法誤存取你的整個主目錄，即便收到指示也一樣。這是縱深防禦，不只是 prompt 層面的管控。

## 可靠性：Pipeline 全面重整

v2.2 大幅重寫 pipeline 執行層，提升生產環境穩定性：

- **非同步 `scanReviews` + semaphore** — 並行 review 掃描上限 3 個，防止大量 review 任務時 CPU 爆衝
- **Pipeline 健康監控** — 背景每 30 分鐘執行一次，透過 `ResetStuckDoing` 自動重置卡在 `doing` 狀態的 zombie 任務
- **Timeout 殺整個 process group** — 步驟逾時時，連子進程一起清除，徹底消滅孤兒進程
- **升級 review 自動核准** — 升級後超過 4 小時未處理的 review 自動通過，避免無限阻塞

Workspace Git 層也同步加固：`index.lock` 重試加指數退避、`wsGitMu` 序列化鎖、stale lock 閾值從 1 小時縮短至 30 秒。

## Self-Liveness Watchdog

生產環境現在有自動故障恢復。全新的 self-liveness watchdog 監控 Tetora daemon 心跳；進程無回應時，觸發 supervisor 管理的自動重啟。

再也不用在凌晨三點 SSH 去重啟一個悄悄掛掉的 daemon。

## 多租戶 Dispatch：`--client` Flag

多租戶支援正式到位。新的 `--client` flag 讓每個客戶的 dispatch 輸出完全隔離：

```bash
tetora dispatch --client acme "執行每週報告 workflow"
tetora dispatch --client initech "PR #42 程式碼審查"
```

每個客戶擁有獨立的輸出路徑，不同客戶的任務輸出不會混雜。搭配 Team Builder CLI，可從單一 Tetora 實例管理多客戶 agent 設定。

## Worktree 故障保護

過去任務中途失敗時，worktree 清理會直接丟棄未提交的變更。v2.2 改變這個行為：有 commit 或本地變更的失敗/取消任務，會保存為 `partial-done` 狀態而非直接刪除。

這意味著：
1. 進行中的工作不再被靜默丟失
2. 可以精確檢視 agent 在哪個步驟失敗
3. 手動補救簡單直接——branch 完整保留

## History CLI：失敗分析工具

三個新的 `tetora history` 子指令，協助診斷 agent 執行失敗：

```bash
tetora history fails              # 列出最近失敗的任務及錯誤摘要
tetora history streak             # 顯示各 agent 的連勝/連敗紀錄
tetora history trace <task-id>    # 特定任務的完整執行追蹤
```

agent 重複失敗時，`history fails` 和 `trace` 讓你不用翻原始 log 就能找到根本原因。

## 取消按鈕（v2.2.1）

Dashboard 新增直接取消執行中任務的功能：

- **Task Detail modal** — 任務狀態為 `doing` 時顯示黃色「Cancel」按鈕
- **Workflow 進度面板** — 「View Full Run」旁新增「Cancel Run」按鈕

任務完成或狀態離開 `doing` 後，按鈕自動消失。

## Provider Preset UI

Dashboard Settings 新增 Provider Preset UI：

- **Custom `baseUrl`** 輸入框（支援自建或代理端點）
- **Anthropic 原生 provider 類型** — 使用正確格式的 `x-api-key` 認證
- **連線測試端點** — 在派發任務前先驗證 provider 設定

## 記憶時間衰減

agent 的記憶條目現在支援時間衰減機制。幾個月前學到的事實會逐漸降低優先度，避免過時資訊在長期運行的 Tetora 部署中壓過新的 context。

衰減速率可按專案設定——適合 context 快速變動、舊假設應該自然淡出的團隊。

## 網站：Astro 遷移

Tetora 官網從舊版 HTML 遷移至 [Astro](https://astro.build/)，效能與開發體驗同步提升：

- **pnpm** 加速安裝，結果可重現
- **WebP logo** 取代 909KB PNG（縮減至 3KB，減少 99.7%）
- **GA4 延遲載入** 降低 Total Blocking Time
- **動態側欄** 改善文件導覽
- **i18n 文件** — 54 個翻譯檔案，涵蓋 6 份核心文件的 9 種語言

## 安全修復

v2.2 修正了內部審計發現的兩個安全問題：

- **SSRF 修復** — `/api/provider-test` 端點加強防護，對使用者提供的 URL 在發出對外請求前進行驗證
- **XSS 修復** — Provider preset UI 輸入欄位加入清理，防止 dashboard 視圖中的跨站腳本攻擊

## 升級至 v2.2.2

```bash
tetora upgrade
```

單一執行檔，零外部依賴。支援 macOS / Linux / Windows。

[在 GitHub 上查看完整 Changelog](https://github.com/TakumaLee/Tetora/releases/tag/v2.2.2)
