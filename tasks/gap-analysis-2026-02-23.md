# Tetora v2 深度缺漏分析報告

> 日期: 2026-02-23
> 研究目標: 從功能對標、人性需求、技術架構三個角度，識別 Tetora 的關鍵差距並提出優先排序的行動建議

## 執行摘要

Tetora v2 已是一個相當完整的 Life OS，擁有 155 個內建工具、10 個通訊頻道、完整的 agentic loop、和涵蓋生活各面向的功能模組。然而，與 OpenClaw (196K GitHub stars、3,286+ skills、50+ integrations) 相比，Tetora 的核心差距不在於功能數量，而在於三個結構性問題：(1) **缺乏生態系統效應** -- 沒有社群可以貢獻 skills；(2) **健康/身體數據整合是最大的功能缺口** -- 一個 Life OS 不能讀取用戶的睡眠、心率、步數數據；(3) **141K 行單體架構的可維護性風險正在累積** -- 20+ 全域單例、332 個檔案全在 package main。

最高優先級的三個行動：整合 Apple Health/Google Fit 數據讓 Life OS 名副其實、加入 Workflow Approval Gates 強化安全信任、以及引入 ServiceRegistry 模式遏制全域狀態耦合。

Tetora 的核心差異化優勢 -- 安全性 (injection defense, tool trust levels, AES-256-GCM)、Life Intelligence (insights, scheduling, goals, briefing)、以及零外部依賴 -- 應該被保護和強化，而非追趕 OpenClaw 的 skill 數量。

---

## 第一部分：與 OpenClaw 的功能差距分析

### OpenClaw 現狀 (2026-02)

OpenClaw (前身 Clawdbot/Moltbot) 由 Peter Steinberger 開發，目前 196,000+ GitHub stars，600+ contributors，是最大的開源個人 AI agent。

| 維度 | OpenClaw | Tetora v2 |
|------|----------|-----------|
| GitHub Stars | 196,000+ | 私人專案 |
| Contributors | 600+ | 1 (+ AI) |
| Skills/Tools | 3,286+ (ClawHub registry) + 內建工具 | 155 內建工具 |
| Messaging Channels | 15+ (WhatsApp, Telegram, Discord, Slack, Signal, iMessage, BlueBubbles, Teams, Matrix, Nostr, Tlon, Zalo, Zalo Personal, Nextcloud Talk, WebChat) | 10 (Telegram, Slack, Discord, WhatsApp, LINE, Matrix, Teams, Signal, Google Chat, iMessage) |
| AI Providers | 14+ (Claude, GPT-4/5, Gemini, Grok, DeepSeek, Mistral, GLM, Perplexity, Ollama, LM Studio, OpenRouter, HuggingFace, MiniMax, Vercel) | 3 類 (Claude CLI, Claude API, OpenAI-compatible) |
| Workflow Engine | Lobster (typed pipelines, approval gates, resume tokens) | workflow_exec.go (YAML workflows, 無 approval gates) |
| Skill Marketplace | ClawHub (公開 registry, semantic search, moderation, versioning) | skill system (create/install/search/scan + Sentori scanner) |
| Smart Home | Philips Hue, 8Sleep, Home Assistant | Home Assistant (4 tools) |
| Music/Audio | Spotify, Sonos, Shazam | Spotify, YouTube, Podcast |
| Password Manager | 1Password integration | 無 |
| Note Apps | Apple Notes, Apple Reminders, Obsidian, Bear, Notion | Obsidian/Notes, Notion sync |
| Task Management | Trello, GitHub Issues | Todoist sync, Notion sync, 內建 Task Manager |
| Voice Mode | Always-on wake word (macOS/iOS/Android), ElevenLabs | Voice STT/TTS, Realtime Voice |
| Security | 已知 CVE (CVE-2026-25253)，被批評缺乏 guardrails | 多層防禦 (injection, trust levels, AES-256-GCM, Sentori) |
| Language | Node.js | Go (zero deps, single binary) |

### 關鍵差距分析

#### 差距 1: 生態系統 vs 內建 -- 結構性差距

OpenClaw 的核心護城河是 ClawHub 的 3,286 個社群 skills，分布在 11 個類別：AI/ML (1,588)、Utility (1,520)、Development (976)、Productivity (822)、Finance、Location & Travel、Business & Enterprise 等。這是飛輪效應：更多 skills -> 更多用戶 -> 更多貢獻者 -> 更多 skills。

ClawHub 的技術特點：
- Skill 格式極簡：一個 `SKILL.md` + 可選支援檔案
- Embedding-powered semantic search
- 版本控制 + changelog
- 社群審核 (3+ unique reports 自動隱藏)
- CLI 安裝：`clawhub install <slug>`

Tetora 的 skill system 已有 create/install/search/scan + Sentori 安全掃描，但缺少公開 registry 和社群貢獻機制。

**評估**: 如果 Tetora 保持私人專案定位，這個差距可以接受。Tetora 的 155 個 **內建** 工具的品質和整合深度，很可能超過 ClawHub 上大量品質參差不齊的社群 skills。**深度勝過廣度** 是正確策略。

#### 差距 2: Workflow Engine 成熟度

OpenClaw 的 Lobster 是一個成熟的 typed workflow runtime：
- 步驟之間 JSON 類型流轉 (`$step.stdout`, `$step.json`)
- **Approval gates** -- side effects 自動暫停等待人工確認
- **Resume tokens** -- workflow 中斷後可恢復
- 可組合 pipelines (`command1 | command2 | approve --prompt 'Continue?'`)
- 逾時和輸出大小限制
- 三種狀態：`ok`, `needs_approval`, `cancelled`

Tetora 的 workflow_exec.go 有基礎 YAML workflow，但缺少 approval gates 和 resume tokens。

**建議 (P1)**: 加入 approval gate 機制。這不僅是功能差距，更是 **安全和信任的基礎** -- 高風險操作 (發送郵件、轉帳、購買、刪除數據) 前應該要求人工確認。預估 ~400 行。

#### 差距 3: AI Provider 覆蓋

Tetora 的 OpenAI-compatible 介面理論上可接大部分 provider，但缺少：
- **Provider 自動 fallback chain** -- Claude 掛了自動切 GPT (circuit breaker 已有，但是 provider 級別的)
- **Model routing** -- 簡單任務用小/便宜模型，複雜任務用大模型
- **專用本地模型支援** -- Ollama API 有些特性 (模型下載/管理) 不在 OpenAI-compat 範圍內

**建議 (P2)**: Provider fallback chain 最有價值。在 circuit breaker 基礎上加入自動 fallback 到備選 provider。預估 ~200 行。

#### 差距 4: Channels

缺少的 channels (對比 OpenClaw)：Nostr、Zalo、Nextcloud Talk、**WebChat**。

其中 WebChat 最有用 -- 讓不用任何 messaging app 的人也能透過瀏覽器用 Tetora。dashboard.html 已有基礎。

**建議 (P2)**: WebSocket-based WebChat。預估 ~300 行。

#### 差距 5: 特殊整合

1Password、Sonos、Shazam、Things 3、Bear Notes、Trello、GitHub Issues。

**建議 (P2)**: 大部分是 nice-to-have。1Password CLI 整合最實用 (安全角度)，預估 ~150 行。

### Tetora 的差異化優勢 (OpenClaw 沒有的)

這些是 Tetora 應該保護和強化的核心優勢：

| 優勢 | 說明 |
|------|------|
| **Life Intelligence Layer** | insights.go (行為分析、anomaly detection、spending forecast) -- OpenClaw 無此功能 |
| **Smart Scheduling** | 衝突偵測、自動建議空閒時段 -- OpenClaw 無 |
| **Goal Planning with Autonomy** | 目標分解、milestone tracking、weekly review -- OpenClaw 無 |
| **Morning Briefing / Evening Wrap** | 結構化每日報告，整合所有生活數據 -- OpenClaw 無 |
| **Finance & Price Watch** | 支出追蹤 + 價格監控雙向 -- OpenClaw 無內建 |
| **Family Mode** | 多用戶支援 + 共享列表 -- OpenClaw 是單用戶 |
| **Unified Memory (6 namespaces)** | 比 OpenClaw 的 Markdown-based memory 更結構化 |
| **注入防禦** | injection.go fail-closed -- OpenClaw 被批評 "security nightmare" |
| **工具信任等級** | tool_policy.go 5 levels -- OpenClaw 是全開或全關 |
| **零外部依賴** | 單一 binary 部署 -- OpenClaw 需要 Node.js + npm |
| **Agentic Loop 成熟度** | budget control, truncation, filtering, token tracking -- 比 OpenClaw 更完善 |
| **LINE 和 Google Chat** | OpenClaw 不支援這兩個亞洲重要 channel |
| **成本追蹤** | 每 task + 每日/每週 budget -- OpenClaw 無 |

---

## 第二部分：從馬斯洛需求層次分析功能缺漏

### 第一層：生理需求 (Physiological)

| 子需求 | Tetora 現狀 | 缺漏 | 優先級 |
|--------|------------|------|--------|
| 健康數據整合 | habits.go (habit_create/log, health_log/summary) | **無法讀取 Apple Health / Google Fit** (心率、步數、睡眠、血氧) | **P0** |
| 睡眠追蹤 | health_log 可手動記錄 | 沒有自動從穿戴裝置讀取睡眠階段數據 | P0 (同上) |
| 飲食/營養追蹤 | 無 | **完全缺失** -- 沒有卡路里、營養素、食物資料庫查詢 | P1 |
| 運動記錄 | habit 系統可追蹤頻率 | 沒有 workout 類型辨識、距離/配速/心率區間等運動細節數據 | P1 |
| 水分攝取 | 無專門工具 | 可透過 habit 追蹤，但沒有每日目標計算和提醒 | P2 |
| 用藥提醒 | reminder 系統可做基礎提醒 | 沒有藥物管理 (劑量、交互作用檢查、庫存追蹤、補充提醒) | P1 |

**核心問題**: 2026 年穿戴裝置市場規模已達 300 億美元，Apple Health+ 即將推出 AI 健康教練。一個 Life OS 不能讀取用戶的身體數據，就像一個財務軟體不能讀取銀行帳戶一樣 -- 功能上存在但實用性大打折扣。

**建議**:
- **P0: Apple Health / Google Fit 讀取器** -- 實作方式：macOS 上用 Apple Shortcuts 導出 JSON 或 `healthkit-to-sqlite`；Google Fit 用 REST API + OAuth。建立 `health_data.go`，提供 `health_sync`, `health_query`, `health_trends` 工具。預估 ~600 行。
- **P1: 營養追蹤** -- `nutrition_log` 工具，接 FatSecret/USDA FoodData Central API 查營養數據。支援自然語言輸入 ("午餐吃了一碗拉麵")。預估 ~400 行。
- **P1: 用藥管理** -- `medication_set`, `medication_log`, `medication_status` 工具 + cron 提醒整合。預估 ~300 行。

### 第二層：安全需求 (Safety)

| 子需求 | Tetora 現狀 | 缺漏 | 優先級 |
|--------|------------|------|--------|
| 財務安全 | finance.go (expense_add/report/budget), price_watch | 沒有銀行帳戶餘額整合、淨資產追蹤、投資組合管理 | P1 |
| 資料備份 | backup_schedule.go, export.go, GDPR compliance | 足夠完整 | - |
| 資料加密 | AES-256-GCM for OAuth tokens, P27.2 selective encryption | P27.2 已大幅改善 (session, contacts PII, finance, habits) | - |
| 密碼安全 | crypto.go | 沒有密碼管理器整合 (1Password/Bitwarden CLI) | P2 |
| 保險/證件管理 | 無 | 沒有保險到期提醒、護照/駕照/簽證更新追蹤 | P2 |
| 緊急機制 | contacts.go 有聯絡人 | 沒有 ICE (In Case of Emergency) 快速通知、緊急聯絡人機制 | P2 |

**建議**:
- **P1: 資產/淨資產追蹤** -- 擴展 finance.go 加入 `asset_add`, `net_worth`, `asset_history` 工具。手動輸入帳戶餘額 (銀行、投資、不動產)，自動計算淨資產趨勢並納入 life_insights 分析。預估 ~250 行。
- **P2: 證件到期追蹤** -- `document_track` 工具 (護照、駕照、簽證、保險到期日)，整合 cron 提醒 (到期前 30/7/1 天自動通知)。預估 ~200 行。

### 第三層：社交需求 (Love/Belonging)

| 子需求 | Tetora 現狀 | 缺漏 | 優先級 |
|--------|------------|------|--------|
| 人際關係管理 | contacts.go (5 tools) + social graph | 已有基礎 CRM | - |
| 社交提醒 | contact_upcoming (生日、久未聯繫偵測) | 足夠 | - |
| 多頻道互動 | 10 channels + group chat | 足夠 | - |
| 紀念日管理 | contact_upcoming 有生日 | 沒有自訂特殊日期 (結婚紀念日、入學日、離職日等) | P2 |
| 社交能量 | sentiment.go (情緒記憶) | 沒有 introvert/extrovert energy tracking、社交頻率建議 | P2 |
| 禮物管理 | 無 | 記錄送禮/收禮歷史、禮物點子 wishlist | P2 |

**評估**: Tetora 在社交層面相對完整。contacts.go + sentiment.go + 多 channel 支援已覆蓋核心需求。

**建議**:
- **P2: 擴展 contacts 加入自訂日期類型** -- 在現有 birthday 機制上加入 `date_type` field (anniversary, graduation, etc.)。預估 ~100 行。

### 第四層：尊重需求 (Esteem)

| 子需求 | Tetora 現狀 | 缺漏 | 優先級 |
|--------|------------|------|--------|
| 目標達成 | goals.go (4 tools, milestone tracking, weekly review) | 足夠完整 | - |
| 成就系統 | 無專門模組 | 沒有里程碑慶祝、成就徽章、連續天數獎勵 | P2 |
| **技能/學習追蹤** | 無 | **完全缺失** -- 沒有學習進度、書籍閱讀、課程完成、技能練習時數追蹤 | **P1** |
| **時間追蹤** | 無 | **完全缺失** -- 沒有工作時數、各類活動時間分配分析 | **P1** |
| 職涯發展 | goals.go 可部分覆蓋 | 沒有專門的職涯路線圖、技能樹、面試準備工具 | P2 |

**核心問題**: "你的時間花在哪裡" 和 "你在學什麼" 是 Life OS 最基礎的兩類數據。缺少這些，insights.go 的行為分析就少了最重要的輸入。

**建議**:
- **P1: 時間追蹤** -- 建立 `timetracking.go`，提供 `time_start`, `time_stop`, `time_status`, `time_report` 工具。追蹤活動花費時間 (工作、學習、運動、社交、娛樂)，生成 weekly/monthly time allocation 報告。和 insights.go 整合做時間使用趨勢分析。預估 ~400 行。
- **P1: 學習/閱讀追蹤** -- 建立 `learning.go`，提供 `book_add`, `book_status`, `learning_log`, `learning_report` 工具。追蹤書籍 (已讀/在讀/待讀/筆記/評分)、課程進度、技能練習時數。預估 ~350 行。

### 第五層：自我實現 (Self-Actualization)

| 子需求 | Tetora 現狀 | 缺漏 | 優先級 |
|--------|------------|------|--------|
| 反思 | reflection.go | 已有 | - |
| 洞察 | insights.go (life_report, life_insights) | 已有行為分析 + anomaly detection | - |
| 每日報告 | briefing.go (morning/evening) | 已有 | - |
| **結構化日記** | note_create/append 可用但非專門工具 | 沒有 gratitude journal、情緒日記、每日回顧模板 | **P1** |
| 冥想/正念 | 無 | 沒有冥想計時、正念提醒、呼吸練習引導 | P2 |
| 創作輔助 | image_generate | 沒有寫作輔助 (brainstorm, mind map, story prompts) | P2 |
| 價值觀追蹤 | 無 | 定期反思價值觀 alignment 的機制 | P2 |

**建議**:
- **P1: 結構化日記** -- 建立 `journal.go`，提供 `journal_write`, `journal_read`, `journal_prompts` 工具。支援模板 (gratitude -- 每日三件好事、mood -- 情緒 1-10 + 原因、reflection -- 今天學到什麼/明天想改進什麼)。和 unified_memory + sentiment.go 整合。briefing_evening 可以自動觸發日記提示。預估 ~300 行。
- **P2: 冥想計時** -- `meditation_start`, `meditation_log`, `meditation_stats` 工具。預估 ~200 行。

---

## 第三部分：技術架構盲點

### 3.1 代碼規模和結構分析

**現狀數據**:
- 源碼 (不含測試): **83,605 行** across **196 個** .go 檔案
- 測試代碼: **59,679 行** across **136 個** _test.go 檔案
- 總計: **143,284 行** across **332 個** .go 檔案
- 已註冊工具: **155 個** (tool.go 中)
- 測試函數: **2,285 個**
- 測試/源碼行數比: **71.4%** (良好)
- 全域單例: **20+** (globalXxxService)
- 定義的介面: **僅 8 個**
- 最大檔案: tool.go (3,535 行), telegram.go (1,631 行), apidocs.go (1,436 行), dispatch.go (1,407 行)
- 全部在 `package main`

### 3.2 全域狀態耦合 (P1 -- 風險正在累積)

main.go 中有 20+ 個 `globalXxxService` 變數：

```
globalUnifiedMemoryEnabled, globalUnifiedMemoryDB,
globalUserProfileService, globalFinanceService,
globalTaskManager, globalFileManager, globalSpotifyService,
globalPodcastService, globalFamilyService, globalContactsService,
globalInsightsEngine, globalSchedulingService, globalHabitsService,
globalGoalsService, globalBriefingService, globalReminderEngine,
globalPresence, globalHAService, globalGmailService,
globalCalendarService, globalTwitterService, ...
```

**問題**:
1. 測試時無法輕鬆替換 mock -- 全域狀態讓真正的單元測試不可能
2. 服務之間的依賴關係是隱式的 -- 不在函數簽名中，只能靠讀代碼理解
3. 初始化順序很重要但沒有明確記錄 -- main.go 800+ 行初始化邏輯
4. 任何一個服務初始化失敗都可能影響其他服務
5. 無法在不啟動整個系統的情況下測試單個模組

**建議 (P1)**: 引入 `App` struct，逐步把 global singletons 收納為 fields：

```go
type App struct {
    cfg        *Config
    registry   *ToolRegistry
    memory     *UnifiedMemoryService
    contacts   *ContactsService
    insights   *InsightsEngine
    scheduling *SchedulingService
    habits     *HabitsService
    goals      *GoalsService
    briefing   *BriefingService
    finance    *FinanceService
    // ... 其他 services
}
```

不需要一次重構全部。先建立 struct + 新增一個 `newApp(cfg) *App` 函數，然後每次修改某個模組時順便遷移該 global 到 App field。

預估: ~500 行初始建設 + 每個模組 ~20 行遷移。可分 5-10 個 session 分批完成。

### 3.3 Package 結構 (P2 -- 長期風險)

332 個 .go 檔案全在 `package main`。Go 的 package 系統是建立模組邊界的核心機制，但 Tetora 完全沒用。

**風險**:
- 任何函數都可以直接調用任何其他函數 -- 沒有強制的封裝邊界
- 新開發者 (或新 session 的 AI) 無法快速理解模組邊界
- 重構時容易意外破壞不相關的功能

**反面考量**: 全 main package 也有好處 -- 編譯簡單、沒有 import cycles、IDE 自動完成更完整。對單人維護的專案，這個 tradeoff 可能是合理的。

**建議 (P2)**: 暫時不需要拆分 package，但應該拆分 tool.go。目前 3,535 行、155 個工具註冊全在一個檔案中，找一個工具要滾很久。按功能分拆成：
- `tool_core.go` (exec, read, write, edit, search_tools)
- `tool_memory.go` (memory_*, knowledge_*, um_search)
- `tool_productivity.go` (calendar_*, email_*, reminder_*, task_*, note_*)
- `tool_media.go` (spotify_*, youtube_*, podcast_*)
- `tool_life.go` (habit_*, health_*, goal_*, contact_*, schedule_*, briefing_*, insight_*)
- `tool_cloud.go` (drive_*, dropbox_*, oauth_*)
- `tool_dev.go` (browser_*, cron_*, agent_*, exec)
- `tool_utility.go` (weather_*, currency_*, translate_*, rss_*, web_*, image_*)

純機械式搬移，不改任何邏輯。預估 ~2 小時手動搬移。

### 3.4 測試覆蓋分析 (P1)

**優點**: 2,285 個測試函數、71.4% 行數比是不錯的數字。

**盲點**:

| 缺失的測試類型 | 影響 | 建議 |
|---------------|------|------|
| **tool.go 沒有直接的 handler 測試** | 最大的檔案 (3,535 行) 零直接測試。tool handlers 的邏輯散佈在各個模組的測試中，但 tool.go 本身的 registry 邏輯、enabled() 函數、ListForProvider() 沒有測試 | P1: ~300 行 |
| **Race detection 不在 CI** | 20+ 全域 singletons + 併發 = race condition 高風險。不知道 CI 是否跑 `-race` | P1: 1 行 CI 修改 |
| **沒有 Integration tests** | 無端到端的 agentic loop 測試 (send message -> dispatch -> tool call -> response) | P1: ~500 行 |
| **沒有 Fuzz tests** | escapeSQLite(), injection detection, JSON parsing 是安全關鍵路徑，適合 fuzz testing | P2: ~200 行 |
| **沒有 Benchmark tests** | 沒有效能基準線 -- hybridSearch, embedding computation 的效能無法追蹤 | P2: ~150 行 |

**建議**:
- **P1: `go test -race` in CI** -- 在 `.github/workflows/ci.yml` 加入 `-race` flag。成本幾乎為零但能抓到並發 bug。
- **P1: tool.go 測試** -- 至少覆蓋 registry 邏輯和 top 20 最常用工具的 handler。預估 ~300-800 行。
- **P1: Integration test for agentic loop** -- 一個 mock provider + 預設 tool call sequence 的端到端測試。預估 ~500 行。

### 3.5 效能瓶頸 (P2 -- 目前不明顯但會隨數據成長)

| 瓶頸 | 描述 | 觸發時間點 |
|------|------|-----------|
| SQLite via CLI | 每次 `queryDB()` fork 一個 `sqlite3` process | 數據量大或併發高時 (>50 ops/sec) |
| TF-IDF + Vector brute force | embedding.go 的向量搜索是線性掃描 | embeddings 超過 ~50K-100K 條時 |
| 單機限制 | 整個系統跑在一台機器上 | Family mode 多人同時使用時 |
| 無 HTTP client 連線池 | 每次 OAuth/API 請求可能建立新連線 | 大量外部 API 調用時 |
| Config struct 反序列化 | 125+ fields 的 JSON unmarshal | 每次 config reload 時 (影響很小) |

**建議**:
- **P2: queryDB() LRU cache** -- 對高頻查詢 (如 memory search, habit status) 加入 TTL-based cache。預估 ~200 行。
- **P2: Embedding index** -- 當向量數量成長時，引入簡易的 bucket-based ANN search。目前 brute force 在 10K 以下足夠。預估 ~300 行 (後續需要時再做)。

### 3.6 安全性優勢 (值得保護)

Tetora 在安全方面 **顯著領先** OpenClaw：

| 安全特性 | Tetora | OpenClaw |
|----------|--------|----------|
| 注入防禦 | injection.go (fail-closed mode) | 被安全研究者稱 "security nightmare" |
| 工具信任等級 | tool_policy.go (observe/suggest/auto, 5 levels) | 全開或全關，無分級 |
| 加密 | AES-256-GCM (crypto.go), P27.2 selective encryption | 明文儲存 |
| 安全掃描 | Sentori (24 regex patterns, 5 categories, scoring) | 社群安全掃描較弱 |
| CVE 歷史 | 0 (私人專案，但也是設計優勢) | CVE-2026-25253 (authentication token leak) |
| Panic Recovery | recoveryMiddleware + safeToolExec | 未記載 |
| Request Limits | 10MB body size, per-tool 30s timeout | 未記載 |
| Audit Logging | 結構化 logging (logInfo/Warn/Error + Ctx) | 部分 |
| Body Size Limit | 10MB bodySizeMiddleware | 無 |
| Rate Limiting | global + per-group | 基礎 |

OpenClaw 被批評的核心問題包括：用戶的 AI agent 自行購買汽車、spam 聯絡人、未經授權購物。這些都是因為缺乏 guardrails。Tetora 的 tool trust levels + injection defense + approval mechanism (如果加入) 可以完全避免這些問題。

**這是 Tetora 最重要的差異化優勢，新功能開發不應犧牲安全性。**

---

## 綜合建議：優先級排序

### P0 -- 必做 (影響 Life OS 核心定位)

| # | 建議 | 預估工作量 | 理由 |
|---|------|-----------|------|
| 1 | **Apple Health / Google Fit 整合** | ~600 行 (health_data.go) | Life OS 最大的單一功能缺口。2026 年穿戴裝置普及率極高，不讀取身體數據的 Life OS 是不完整的。 |

### P1 -- 重要 (顯著提升產品價值或降低技術風險)

| # | 建議 | 預估工作量 | 理由 |
|---|------|-----------|------|
| 2 | **Workflow Approval Gates** | ~400 行 | 安全和信任的基礎。OpenClaw 因缺乏 guardrails 被嚴厲批評。這是 Tetora 差異化的延伸。 |
| 3 | **ServiceRegistry/App struct 重構** | ~500 行初始 + 漸進式遷移 | 消除 20+ global singletons 的耦合。不做會讓每次新功能開發風險累積。可分批。 |
| 4 | **時間追蹤** | ~400 行 (timetracking.go) | "你的時間花在哪" 是 Life OS 最基礎的數據，也是 insights.go 缺失的關鍵輸入。 |
| 5 | **學習/閱讀追蹤** | ~350 行 (learning.go) | 個人成長的核心指標。和 goals.go, insights.go 整合形成完整的自我提升迴圈。 |
| 6 | **結構化日記** | ~300 行 (journal.go) | 反思 + 情緒記錄 + gratitude 是心理健康基礎。和 briefing_evening 自然銜接。 |
| 7 | **營養追蹤** | ~400 行 (nutrition.go) | 搭配 Health Data 整合形成完整的健康數據面板。 |
| 8 | **用藥管理** | ~300 行 (medication.go) | 高頻剛性需求，特別是有長期用藥或維他命的用戶。 |
| 9 | **Race detection in CI** | ~1 行 | 投資回報比最高的改善。全域狀態 + 併發 = race condition 風險。 |
| 10 | **Agentic loop integration test** | ~500 行 | 目前缺少端到端測試。mock provider + tool sequence 驗證整個 dispatch pipeline。 |
| 11 | **資產/淨資產追蹤** | ~250 行 | 財務安全的基礎。expense 追蹤支出但看不到全貌。 |

### P2 -- Nice-to-have (錦上添花)

| # | 建議 | 預估工作量 | 理由 |
|---|------|-----------|------|
| 12 | tool.go 拆分 | ~2 小時搬移 | 可維護性，不改邏輯 |
| 13 | WebChat channel | ~300 行 | 降低使用門檻 |
| 14 | Provider fallback chain | ~200 行 | 提高可靠性 |
| 15 | queryDB() LRU cache | ~200 行 | 效能 |
| 16 | Fuzz tests | ~200 行 | 安全關鍵路徑 |
| 17 | Benchmark tests | ~150 行 | 效能基準線 |
| 18 | 冥想計時 | ~200 行 | 正念/心理健康 |
| 19 | 紀念日/自訂日期 | ~100 行 | 社交完整性 |
| 20 | 1Password CLI 整合 | ~150 行 | 安全便利性 |
| 21 | 證件到期追蹤 | ~200 行 | 實用 |
| 22 | Config struct 分組 | ~400 行 | 可維護性 |

---

## 建議的實作路線圖

```
P28: Health & Body Data Layer (~1,300 行)
  P28.0: Apple Health 讀取器 (health_data.go, ~600 行)
  P28.1: 營養追蹤 (nutrition.go, ~400 行)
  P28.2: 用藥管理 (medication.go, ~300 行)

P29: Life Completeness (~1,050 行)
  P29.0: 時間追蹤 (timetracking.go, ~400 行)
  P29.1: 學習/閱讀追蹤 (learning.go, ~350 行)
  P29.2: 結構化日記 (journal.go, ~300 行)

P30: Architecture Hardening II (~1,400 行 + tool.go 搬移)
  P30.0: ServiceRegistry/App struct (~500 行初始)
  P30.1: tool.go 拆分 (搬移, 0 新增行)
  P30.2: Race detection + integration tests (~500 行)
  P30.3: Workflow approval gates (~400 行)

P31: Financial & Document Completeness (~450 行)
  P31.0: 資產/淨資產追蹤 (~250 行)
  P31.1: 證件到期追蹤 (~200 行)

P32: Polish (~500 行)
  P32.0: WebChat channel (~300 行)
  P32.1: Provider fallback chain (~200 行)
```

P28-P32 預估總新增: ~4,700 行 + tool.go 搬移重構

---

## 風險與注意事項

1. **功能膨脹陷阱** -- Tetora 已經 141K 行。每個新功能都增加維護負擔。建議在 P29 之後先做 P30 (架構強化)，在更好的架構基礎上再擴展功能。

2. **OpenClaw 的教訓** -- OpenClaw 因 "缺乏 guardrails" 和 "security nightmare" 被嚴厲批評。Tetora 的安全架構是核心優勢，新功能不應該犧牲安全性。每個新工具都應該經過 trust level 評估。

3. **健康數據的隱私風險** -- Apple Health 數據是最敏感的個人資料之一。實作時必須確保：全程 AES-256-GCM 加密、本地儲存零雲端、明確的數據保留和刪除政策。

4. **不要追趕 OpenClaw 的 skill 數量** -- 3,286 skills 是社群效應的結果，其中大量是品質參差不齊的。Tetora 的策略應該是 **一個人的完美 Life OS**，追求內建工具的深度和品質，而非廣度和數量。

5. **單人維護的現實** -- 1 人 + AI 維護 141K+ 行代碼，最大的風險是知識集中。MEMORY.md + CLAUDE.md + tasks/lessons.md 的做法很好，但建議額外加入：
   - 每個新模組的檔案頂部加入 20-30 行的模組概述註解 (purpose, dependencies, key types)
   - 關鍵架構決策用 ADR 格式記錄 (為什麼用 SQLite CLI 而非 cgo？為什麼零外部依賴？)

6. **信心程度標註**:
   - 高信心: OpenClaw 對比數據 (來源充分、多方交叉驗證)
   - 高信心: 技術架構分析 (基於實際代碼檢查)
   - 中信心: 工作量預估 (基於類似模組的歷史大小推算，實際可能 +/- 30%)
   - 中信心: 健康整合需求優先級 (基於市場趨勢，但實際取決於用戶個人需求)

---

## 參考來源

- [OpenClaw Official Site](https://openclaw.ai/)
- [OpenClaw Integrations](https://openclaw.ai/integrations)
- [ClawHub Skill Directory](https://github.com/openclaw/clawhub)
- [ClawHub: 3,286 AI Agent Skills](https://clawhub.biz/)
- [OpenClaw on DigitalOcean](https://www.digitalocean.com/resources/articles/what-is-openclaw)
- [OpenClaw on Medium (2026)](https://medium.com/@gemQueenx/what-is-openclaw-open-source-ai-agent-in-2026-setup-features-8e020db20e5e)
- [OpenClaw Architecture (Medium)](https://bibek-poudel.medium.com/how-openclaw-works-understanding-ai-agents-through-a-real-architecture-5d59cc7a4764)
- [Lobster Workflow Engine Docs](https://docs.openclaw.ai/tools/lobster)
- [Lobster GitHub](https://github.com/openclaw/lobster)
- [OpenClaw Wikipedia](https://en.wikipedia.org/wiki/OpenClaw)
- [OpenClaw Weaknesses (KDnuggets)](https://www.kdnuggets.com/5-lightweight-and-secure-openclaw-alternatives-to-try-right-now)
- [OpenClaw Alternatives (emergent.sh)](https://emergent.sh/learn/best-openclaw-alternatives-and-competitors)
- [OpenClaw Security Guide (Valletta)](https://vallettasoftware.com/blog/post/openclaw-2026-guide)
- [AI Agent Frameworks 2026 (AIMultiple)](https://aimultiple.com/agentic-frameworks)
- [Multi-Agent Frameworks 2026 (multimodal.dev)](https://www.multimodal.dev/post/best-multi-agent-ai-frameworks)
- [2026 AI Operating System (TechTiff Substack)](https://techtiff.substack.com/p/the-2026-ai-operating-system)
- [Wearable Health Tech 2026](https://doccure.io/best-wearable-devices-for-health-tracking-2026-ai-integration-and-reviews/)
- [Apple Health+ AI Coach 2026](https://apple.gadgethacks.com/news/apple-health-ai-coach-launches-2026-revolutionary-features/)
- [Go Modular Monolith (Three Dots Labs)](https://threedots.tech/post/microservices-or-monolith-its-detail/)
- [AI Agent Orchestration (n8n)](https://blog.n8n.io/ai-agent-orchestration-frameworks/)
- [Top AI Agent Frameworks (Shakudo)](https://www.shakudo.io/blog/top-9-ai-agent-frameworks)
