# Tetora 套件審核與瘦身計畫（2026-05-02）

> 起因：user 反映 Tetora 過度膨脹（679MB / 896 個 .go 檔 / 85 個 internal 套件 / root 層 24 個 .go 檔共 84,833 行）
> 方向：a + c — 砍冗餘 + 拆衛星服務
> 真實核心需求：PR review、Tasks/Workflow（修穩定性）、Files 創作審查、戰情室、Discord 助理

## 圖例

- **KEEP** — 核心，留在 tetora-core
- **EXTRACT** — 拆獨立 binary（衛星服務）
- **FOLD** — 太薄，合併進其他套件
- **ARCHIVE** — 暫存到 `archive/` 或獨立 repo（未來可能撿回）
- **DELETE** — AI 時代易重做（重寫成本 < 1 天），直接砍

每個套件都有 `LOC` 與 `為何這樣分類` 與 `重做難度（如砍）`。

---

## 第一層 internal/* 分類

### KEEP（28 個 — 核心保留）

| 套件 | LOC | 角色 |
|---|---|---|
| `cli` | 15923 | CLI 主體（含 `cli/review.go` PR review 命令） |
| `httpapi` | 7429 | HTTP API（戰情室與 dashboard 都吃這個） |
| `taskboard` | 5259 | 任務面板核心 |
| `workflow` | 4629 | 工作流引擎（user 真實需求 #2） |
| `tools` | 4550 | 工具集（待釐清與 tool/ 重複關係） |
| `skill` | 3547 | 技能系統（CLAUDE.md 知識管理核心） |
| `provider` | 2512 | LLM provider 抽象 |
| `tool` | 2509 | 工具註冊（待釐清與 tools/ 重複關係） |
| `config` | 2350 | 配置管理 |
| `dispatch` | 2032 | 核心 dispatch（user 真實需求 #1，且穩定性是頂梁柱） |
| `cron` | 2021 | 排程（review cadence 用） |
| `reflection` | 1203 | 反思系統（CLAUDE.md 提到的 lesson→rule→skill 管線） |
| `knowledge` | 1229 | 知識庫 |
| `session` | 1117 | session 管理 |
| `history` | 989 | 歷史記錄（PR review digest 用） |
| `worktree` | 725 | git worktree（dispatch 時用） |
| `prompt` | 655 | prompt 管理 |
| `version` | 567 | 版本 |
| `rule` | 514 | 規則系統（CLAUDE.md 提到的 rules） |
| `cost` | 494 | 成本追蹤（修 token budget 必須） |
| `review` | 405 | review digest（PR review 流程） |
| `log` | 406 | 日誌（核心基礎設施） |
| `db` | 377 | DB 抽象 |
| `metrics` | 284 | dispatch metrics（修穩定性需要） |
| `dedupguard` | 252 | 去重（retry 風暴防護） |
| `circuit` | 243 | circuit breaker（修穩定性需要） |
| `roles` | 234 | 角色（黑曜在這裡） |
| `migrate` | 229 | DB 遷移 |

**KEEP 小計**：~58,800 LOC

---

### EXTRACT（2 個 — 拆衛星服務）

| 套件 | LOC | 拆出後叫什麼 | 為何拆 |
|---|---|---|---|
| `discord` | 2373 | `tetora-discord` | user (5) — 需要獨立啟停 + token budget guard，避免 Discord 失控拖垮 core |
| `warroom` | 122 | 併入 `tetora-warroom` 衛星 dashboard | user (4) — warroom 套件本體只 122 行（純 status.json 讀寫），真正的 UI 在 dashboard.html，拆走後 dashboard 也跟著拆 |

**註**：`oauth` (782) 如果只 Discord 用，跟 Discord 一起拆走。

---

### FOLD（4 個 — 合併不獨立）

| 套件 | LOC | 合併到 | 原因 |
|---|---|---|---|
| `httputil` | 20 | `httpapi` | 太薄，獨立 package 沒意義 |
| `text` | 15 | utility/inline | 15 行不該是 package |
| `crypto` | 88 | utility | 太薄 |
| `trace` | 63 | `log` | 與 logging 同領域 |
| `store` vs `storage` | 220 + 428 | 合併成一個 | 命名重複，要釐清 |

---

### KEEP（追加 — 主人確認 2026-05-03）

下列原本列在 ARCHIVE，主人確認後改為 KEEP：

| 套件 | LOC | 原因 |
|---|---|---|
| `voice` | 1492 | 主人確認保留 |
| `mcp` | 1146 | 主人確認保留 |
| `proactive` | 1228 | 主人確認保留 |
| `team` | 1220 | 主人確認保留 |
| `i18n` | 1216 | 主人確認保留 |
| `hooks` | 1189 | 主人確認保留 |

---

### ARCHIVE（11 個 — 暫存，未來可能撿回）

放到 `_archive/life-stack-2026-05/`。

| 套件 | LOC | 用途 | 為何不殺 |
|---|---|---|---|
| `scheduling` | 950 | 排程（與 cron 不同？） | 與 cron 重複嫌疑，要釐清 |
| `audit` | 274 | 審計 | 與 history 重疊，但 audit 是另一層次 |
| `recap` | 546 | recap 摘要 | 與 review digest 重疊嫌疑 |
| `usage` | 359 | 用量追蹤 | 與 cost 重疊嫌疑 |
| `notify` | 601 | 通知 | Discord 拆走後可能不需要 |
| `backup` | 314 | 備份 | 個人專案不需要，但移除前確認 SQLite 資料無風險 |
| `benchmark` | 311 | 基準測試 | 不在核心路徑，但測試用 |
| `health` | 254 | health check | 修 dispatch 穩定性可能需要 |
| `workspace` | 429 | workspace 管理 | 待釐清與 ~/.tetora/workspace/ 關係 |
| `retention` | 783 | 資料保留策略 | 個人專案需求低 |
| `session` 已 KEEP | — | — | — |

**ARCHIVE 小計**：~12,800 LOC

---

### DELETE（30+ 個 — AI 時代易重做）

每一個都列了「重做難度」與「重做方式」。

#### UI/視覺玩具（純前端裝飾，重做 < 30 分鐘）

| 套件 | LOC | 用途 | 重做方式 |
|---|---|---|---|
| `pwa` | 127 | PWA manifest + service worker | LLM 一次寫完 |
| `sprite` | 178 | agent 表情/動畫 spritesheet | 直接寫死或刪掉視覺特效 |

#### 簡易演算法（LLM 直接判斷比較好）

| 套件 | LOC | 用途 | 重做方式 |
|---|---|---|---|
| `nlp` | 198 | 字典式情感分析 | LLM 一句 prompt 取代 |
| `classify` | 200 | 複雜度分類 | LLM 直接判斷 |
| `bm25` | 508 | 關鍵字搜尋 | embedding + cosine 比較好（或直接拋給 LLM） |

#### 太薄的工具套件（重做 < 2 小時）

| 套件 | LOC | 用途 | 重做方式 |
|---|---|---|---|
| `quickaction` | 128 | 快速動作 | inline 即可 |
| `quiet` | 169 | 靜音時段 | inline cron 判斷 |
| `estimate` | 138 | 任務估算 | LLM 估或固定值 |
| `messaging` | 172 | 訊息抽象 | 跟 Discord 一起拆走或 inline |
| `upload` | 171 | 上傳 | 直接用 http.HandleFunc |
| `webhook` | 96 | webhook 接收 | 直接 http.HandleFunc |
| `lifecycle` | 248 | 不明 | 看實際用法決定 |

#### 重複/已有更好替代

| 套件 | LOC | 用途 | 為何砍 |
|---|---|---|---|
| `circuit` (已改 KEEP) | — | — | — |
| `dedupguard` (已改 KEEP) | — | — | — |
| `sandbox` | 507 | Docker sandbox | Container Use / dagger 有更成熟方案 |
| `tmux` | 693 | tmux 集成 | symphony / warp 都支援 multi-pane，且大多人不用 |
| `plugin` | 475 | 插件系統 | MCP 已是 de facto 標準 |
| `pairing` | 243 | 配對授權 | 個人單機不需要 |
| `sla` | 417 | SLA 監控 | 個人專案 overkill |
| `trust` | 421 | 信任系統 | 個人單機不需要 |
| `oauth` | 782 | OAuth | 跟著 Discord 走或拆獨立 |

#### 中型功能（重做 < 1 天）

| 套件 | LOC | 用途 | 重做方式 |
|---|---|---|---|
| `completion` | 488 | 不明（命令補全？） | bash completion 直接寫 |
| `push` | 416 | web push | PWA 一起砍 |
| `handoff` | 433 | agent 交棒 | dispatch 裡 inline |
| `canvas` | 452 | MCP canvas 工具 | 重做 |
| `export` | 206 | 匯出 | 重做 |
| `telemetry` | 240 | 遙測 | 個人專案不需要 |

**DELETE 小計**：~7,500 LOC（不算重做後新增）

---

## 第二層子套件分類

### life/* 個人生活管理（13 個套件，~7,300 LOC）→ ARCHIVE 整批

| 套件 | LOC | 描述 |
|---|---|---|
| `life/calendar` | 601 | 行事曆 |
| `life/contacts` | 510 | 聯絡人 |
| `life/dailynotes` | 239 | 每日筆記 |
| `life/family` | 643 | 家庭 |
| `life/finance` | 585 | 財務 |
| `life/goals` | 598 | 目標 |
| `life/habits` | 808 | 習慣追蹤 |
| `life/lifedb` | 25 | DB stub |
| `life/pricewatch` | 289 | 比價 |
| `life/profile` | 605 | profile |
| `life/reminder` | 721 | 提醒 |
| `life/tasks` | 1305 | 個人任務（與 taskboard 重疊嫌疑） |
| `life/timetracking` | 376 | 時間追蹤 |

**判斷**：個人生活管理不是 task agent 該做的事，scope 嚴重失焦。獨立成 `tetora-life` 或直接 ARCHIVE，等 user 確認再撿回。**0 個 test**——成熟度低。

### automation/* 自動化（2 個套件，~1,700 LOC）→ ARCHIVE

| 套件 | LOC | 描述 |
|---|---|---|
| `automation/briefing` | 648 | 早報 |
| `automation/insights` | 1031 | 洞察 |

### integration/* 第三方集成（9 個套件，~3,500 LOC）→ ARCHIVE

| 套件 | LOC | 描述 |
|---|---|---|
| `integration/drive` | 231 | Google Drive |
| `integration/dropbox` | 246 | Dropbox |
| `integration/gmail` | 484 | Gmail |
| `integration/homeassistant` | 614 | Home Assistant |
| `integration/notes` | 643 | Notes |
| `integration/oauthif` | 18 | OAuth interface stub |
| `integration/podcast` | 429 | Podcast |
| `integration/spotify` | 408 | Spotify |
| `integration/twitter` | 394 | Twitter |

**判斷**：這 9 個是個人助理連 IFTTT 級別的集成。MCP 時代這些都該變成獨立 MCP server，不該編在 tetora 主 binary 裡。

---

## 體積估算

| 動作 | 砍掉 | 剩下 |
|---|---|---|
| 起點 | — | 679 MB / 896 .go |
| 移除 `.claude/worktrees/` | ~340 MB | ~339 MB |
| 移除 `*.test` 二進位 + `cover.out` | ~30 MB | ~309 MB |
| ARCHIVE life/automation/integration | ~12,500 LOC | — |
| ARCHIVE 17 個 internal 子套件 | ~12,800 LOC | — |
| DELETE 30+ 個 internal 子套件 | ~7,500 LOC | — |
| EXTRACT discord + warroom | ~2,500 LOC（搬走） | — |
| FOLD 4 個薄套件 | — | — |
| **預估最終** | — | **<100 MB / ~58,800 LOC core** |

從 85 個 internal 套件 → **約 28 個核心套件 + 2 個衛星 binary**。

---

## 後續行動

1. **這份清單需要 user 確認**——特別是 ARCHIVE 那 17 個（voice、mcp、proactive、team、hooks 等），有些可能還是真實需求。
2. **執行前先 git tag**：`git tag pre-trim-2026-05-02` 留退路。
3. **分批 PR**：
   - PR1：移除 `.claude/worktrees/`、`*.test` 二進位、`cover.out`
   - PR2：DELETE 那 30 個 AI 時代易重做的套件
   - PR3：ARCHIVE life/automation/integration 整批到 `archive/`
   - PR4：ARCHIVE 那 17 個 internal 套件
   - PR5：EXTRACT discord 成獨立 binary
   - PR6：EXTRACT warroom + dashboard 成衛星服務
4. **每一步跑 `go build ./...` + 主要測試**。
5. **Root 層 24 個 .go 檔（84,833 行）的瘦身**是另一個獨立的大工程，不在本計畫內。

---

## 待 user 確認的疑問

- [x] ARCHIVE 名單裡的 voice/mcp/proactive/team/hooks/i18n 是否確實不再用？
  **→ 全部 KEEP（主人確認 2026-05-03）**
- [x] life/automation/integration 整批可以 ARCHIVE 嗎？還是有哪幾個是真需求？
  **→ storage/ 有用到要留（已在 KEEP），其餘大部分可 ARCHIVE。有用到的要從 life/ 移出來 KEEP，其他 ARCHIVE。PR1 handoff 已按此處理（storage 留、其他移 _archive）。**
- [ ] tool/ vs tools/ 差別？哪個保留？⚠️ 主人表示已回答過，但 session 記錄遺失，待重新確認（不影響 PR1）
- [ ] store/ vs storage/ 哪個保留？⚠️ 主人表示已回答過，但 session 記錄遺失，待重新確認（不影響 PR1）
- [ ] scheduling/ 與 cron/ 差別？⚠️ 主人表示已回答過，但 session 記錄遺失，待重新確認（不影響 PR1）
- [ ] DELETE 名單裡有沒有「等等這個我還在用」的？（待回答）
