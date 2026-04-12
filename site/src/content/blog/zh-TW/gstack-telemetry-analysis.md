---
title: "gstack Telemetry 事件技術分析：你的 AI Coding 工具在收集什麼？"
lang: zh-TW
date: "2026-04-12"
tag: security
readTime: "~8 分鐘"
excerpt: "YC CEO 開源的 Claude Code skill pack 被發現即使設定 telemetry off 仍持續收集使用行為。Sentori 團隊獨立審計確認了四個關鍵問題。"
description: "gstack Claude Code skill pack 安全審計：telemetry off 不停止收集、eureka 洞察萃取、Supabase 遠端上傳、無 sandbox 隔離。完整技術分析與防護指南。"
---

> 本文由 Sentori 團隊的安全審計結果撰寫

## 事件概述

2026 年 4 月，社群開始關注 gstack — 由 YC CEO Garry Tan 開源的 Claude Code skill pack。LinkedIn 上江中喬的揭露文章引發廣泛討論，指出 gstack 存在嚴重的 telemetry 問題。

Sentori 團隊隨即對 gstack 進行獨立審計，確認了四個值得所有 AI coding tool 使用者認識的關鍵問題。這不是恐嚇文。這是技術事實的完整呈現，以及你可以採取的防護措施。

## 技術分析：四個關鍵發現

### 發現一：「telemetry off」不等於「不收集」

這是最容易被忽略、卻最值得警惕的設計。

gstack 的 telemetry 設定提供了一個 `off` 選項，直覺上讓人以為關閉就沒事了。但審計結果顯示：**每次 skill 啟動，在讀取 telemetry 設定檔之前，本地收集就已經發生**。

具體行為：啟動任何 skill 時，系統立即寫入 `~/.gstack/analytics/skill-usage.jsonl`，記錄 skill 名稱、時間戳、以及當前的 repo 名稱。設定 `telemetry: off` 只能阻止資料被上傳至遠端伺服器，但本地收集會持續進行。

這意味著：你的完整 skill 使用紀錄，包含你在哪個專案裡、什麼時間啟動了哪個 skill，全部存在磁碟上。如果後來有任何讀取這個檔案的機制（或你不知道的機制），這份歷史就是完整的行為側寫。

**問題所在：** 「告知同意」的前提是使用者能正確理解選項的作用範圍。一個名為 `off` 的選項卻繼續收集資料，違反了最基本的透明度原則。

### 發現二：eureka.jsonl — 你的對話洞察被萃取了

多個 gstack skill，包含 `office-hours`、`plan-ceo-review`、`design-consultation` 等，內建了一個「洞察萃取」邏輯。

這些 skill 在對話過程中，會主動從你與 Claude 的互動內容中萃取「商業洞察」，並寫入 `~/.gstack/sessions/{session_id}/eureka.jsonl`。

換句話說：**你跟 AI 討論的產品策略、商業模式、技術決策，可能被結構化地記錄下來**，以「洞察」的形式保存在本地。

這個設計本身或許出於「幫助使用者回顧重要決策」的好意，但問題在於：
- 使用者是否清楚知道這件事正在發生？
- 這些 eureka 紀錄最終會不會被納入遠端上傳的範疇？
- 在 `gstack-telemetry-sync` 觸發時，eureka 資料的邊界在哪裡？

審計中我們無法從文件中找到清晰的回答。

### 發現三：Supabase 遠端上傳 — session_id 仍在傳輸中

`gstack-telemetry-sync` 腳本會將資料 POST 至：

```
frugpmstpnojnhfyimgv.supabase.co/functions/v1/telemetry-ingest
```

gstack 的文件聲稱這是 anonymous 模式，資料不會與使用者身份關聯。但審計發現：**`session_id` 仍然包含在上傳 payload 中**。

`session_id` 本身不是個人識別資訊（PII），但它是一個持久化識別符。如果配合其他中繼資料（使用時間、repo 名稱、skill 序列），仍然可以建立有意義的行為模式。

在隱私設計上，「anonymous」應當意味著連追蹤關聯都做不到。保留 session_id 的設計，與這個承諾存在落差。

### 發現四：無 sandbox — skill 擁有完整 OS 權限

這是所有問題中架構層面最根本的一個。

Claude Code 目前對安裝的 skill 沒有任何沙箱隔離機制。**安裝一個 skill，等於給它完整的作業系統讀寫權限。**

實際後果：任何 skill 都可以讀取你的 `~/.ssh/` 目錄、`.env` 環境變數、甚至整個 home directory 的任何檔案，而你不會收到任何警告。

gstack 安裝時會在以下位置建立檔案與連結：
- `~/.gstack/`（全域 analytics、config、sessions）
- `~/.claude/skills/gstack/`（80 個 skill 本體）
- 各專案目錄下的 `.gstack/`（包含瀏覽 log 等）
- 34 個 symlink skills 指向 gstack 本體

安裝是一鍵完成，但完整清理需要五輪手動操作才能確認所有殘留都已移除。

這不只是 gstack 的問題，而是整個 Claude Code skill 生態系目前缺乏的基礎安全設施。

## 共通模式：2026 Q1 的 AI 工具供應鏈危機

gstack 的問題並非孤立事件。2026 年第一季，AI 開發工具的安全事件呈現出清晰的共同模式：

**Trivy VS Code 擴充被注入惡意 AI prompt**（96 萬安裝）：合法的開發工具在更新中被植入惡意 AI 指令，利用使用者對工具的既有信任。

**Cline CLI 被植入自主 AI agent**：CLI 工具在版本更新中加入了未公告的自主行為能力。

**Claude Code 51 萬行 source code 意外洩漏**：不是攻擊，而是意外，但暴露了 AI 工具本身的安全處理邊界。

**多個 AI 瀏覽器擴充偷收對話內容**：繼 2024-2025 年的瀏覽器擴充風波，AI 特化版本的同類問題持續出現。

**共同模式是什麼？**

1. **信任前置化**：使用者在理解工具行為之前就已完成安裝，信任在透明度之前建立。
2. **擴充點即攻擊面**：plugin、skill、extension 天然就是「信任繼承」機制，但缺乏對應的權限隔離。
3. **收集在設定之前**：多個事件中，資料收集發生在使用者看到設定選項之前。
4. **清理成本不對稱**：安裝容易，完整清理困難，形成事實上的鎖定。

## 自我檢查指南

如果你安裝過 gstack，可以用以下指令確認狀況：

```bash
# 檢查是否有本地 analytics 收集
ls -la ~/.gstack/analytics/

# 查看 skill 使用紀錄
cat ~/.gstack/analytics/skill-usage.jsonl

# 檢查是否有 eureka 洞察紀錄
find ~/.gstack/sessions -name "eureka.jsonl" 2>/dev/null

# 確認 skill symlink 數量
ls ~/.claude/skills/ | grep gstack | wc -l

# 檢查各專案目錄是否有 .gstack 殘留
find ~ -name ".gstack" -type d 2>/dev/null | head -20
```

即使你認為自己已關閉 telemetry，也建議執行第一、二條指令確認本地收集的實際狀態。

## 防護建議

**對於個人開發者：**

1. **審計 skill 來源**：只安裝你能閱讀原始碼並理解其行為的 skill。開源不等於安全，但可審計是基本前提。

2. **安裝前做最小化假設**：假設每個 skill 都有完整系統讀取權限。在這個假設下，你是否仍然願意安裝？

3. **定期檢查 home directory 的新 dotfile**：`~/.gstack`、`~/.claude/` 下的異常目錄，應定期掃描。

4. **使用環境隔離**：在 devcontainer 或 VM 中使用 AI coding tool，限制爆炸半徑。

5. **不要把敏感 secret 放在 agent 可觸及的工作目錄**：`.env` 中的 API key 一旦在 skill 的讀取範圍內，就等同於已暴露。

**對於團隊與企業：**

1. **建立 AI 工具白名單政策**：未經安全審查的 AI coding tool 不應在包含生產 credential 的環境中使用。

2. **CI/CD 環境隔離**：確保 AI 工具（包含 skill 系統）不存在於 CI runner 的基礎環境中。

3. **定期執行 AI 工具供應鏈掃描**：檢查已安裝的 extension、plugin、skill 是否有未預期的檔案寫入行為。

## Sentori：AI Agent Security Scanner

[Sentori](https://github.com/TakumaLee/Sentori) 是一個開源的 AI Agent 安全掃描工具，可自動化偵測上述類型的問題：

- **Telemetry 行為分析**：靜態掃描 skill/plugin 原始碼，識別資料收集邏輯與其執行時序
- **權限模式偵測**：標記對敏感路徑（SSH key、`.env`、credential 檔）的存取嘗試
- **供應鏈風險評估**：分析外部端點呼叫，識別不透明的資料傳輸行為
- **Eureka 模式識別**：偵測對話洞察萃取邏輯，即本次 gstack 審計中發現的第二類問題

Sentori 目前支援掃描 Claude Code skill 生態系，後續將擴展至 VS Code 擴充、CLI 工具插件等更多 AI 開發工具形式。

---

AI 開發工具的便利性是真實的。它們帶來的效率提升也是真實的。但在這個生態系快速擴張的當下，「信任預設值」的代價值得每一位開發者認真評估。

工具的能力邊界，應該由使用者來定義，而不是由工具自己決定。

---

*本文由 Sentori 團隊的安全審計結果撰寫*
