# Tetora 8-Bit Office Dashboard Roadmap

> 「Star Office UI」— 把 agent 的工作狀態變成一個像素辦公室，讓看不見的 AI 勞動變得可愛又直觀。
>
> 參考來源：
> - [Star-Office-UI](https://github.com/ringhyacinth/Star-Office-UI) — Phaser.js pixel office
> - [pixel-office](https://github.com/r266-tech/pixel-office) — zero-dep, 5 agents, boss patrol
> - [OpenClaw Mission Control](https://github.com/robsannaa/openclaw-mission-control) — full-featured dashboard
> - [Clawbot Control Center](https://github.com/BEKO2210/Control-Center) — Digital Office + theme system

---

## 設計原則

1. **Aesthetic Wrapper, Modern Interior** — 外殼是像素風（window chrome、pixel borders、retro fonts），內容維持可讀的 monospace 數據
2. **Zero Build Step** — 維持 Go-embedded 單一 HTML 的架構，不引入 npm/webpack/bundler
3. **CDN 最小化** — 只用 Google Fonts（Press Start 2P + VT323）和一個小型 sprite sheet，不引入 Phaser.js（太重，~1MB）
4. **Progressive Enhancement** — 每個 Phase 獨立可部署，不需要全做完才能用

---

## Phase 1: CSS Reskin — 8-Bit Chrome（~300 行 CSS 改動）

> 把現有 dashboard 包上像素風外殼，不改功能

### 1.1 Typography 切換
- **Headings**: `Press Start 2P`（Google Fonts CDN），限 16px+ 且 8px 倍數
- **Body/data**: `VT323`（Google Fonts CDN），18px，terminal 風格但高可讀性
- **Metrics/code**: 維持 `'SF Mono', monospace`
- `-webkit-font-smoothing: none` on pixel font elements

### 1.2 Pixel Borders（box-shadow 技法）
- `.pixel-panel` class — NES 風格 notched corner borders（純 CSS，不需圖片）
- 替換現有 `.stat`, `.project-card`, `.section` 的 `border-radius: 10px` → pixel border
- 保留 `--surface`, `--border` 等 CSS vars，只換 border 渲染方式

### 1.3 Window Chrome
- `.pixel-window` — title bar + [−][□][×] buttons（裝飾用，不需功能）
- 主要 section（Stats, Activity, Running Tasks, Projects）各包一個 window chrome
- Title bar 用 Press Start 2P，content 用 VT323

### 1.4 Color Palette 調整
```css
:root {
  /* 保留現有 dark base，加入 NES-inspired accents */
  --bg: #08080d;          /* 維持 */
  --surface: #111118;     /* 維持 */
  --border: #1e1e2e;      /* 維持 */
  --pixel-border: #e0e0e8; /* NES white for pixel borders */
  --accent: #0078f8;      /* NES Blue (替換紫色) */
  --accent2: #00b800;     /* NES Green */
  --green: #00b800;       /* NES Green */
  --red: #f83800;         /* NES Red */
  --yellow: #fce0a8;      /* NES Yellow */
}
```

### 1.5 按鈕 & Badge 樣式
- `.btn` → pixel border + Press Start 2P text
- `.badge` → 去掉 `border-radius: 20px`，改為方形 pixel badge
- hover: 顏色反轉（NES 選中效果）

### 改動檔案
| 檔案 | 改動 |
|------|------|
| `dashboard.html` | CSS vars + 新 class + Google Fonts link（~300 行 CSS） |

---

## Phase 2: Pixel Office Scene — Agent Visualization（~500 行 JS + assets）

> Dashboard tab 的上方加入一個像素辦公室場景，agents 以動畫角色呈現

### 2.1 Office Canvas
- `<canvas id="pixel-office">` — 放在 Dashboard tab 頂部，取代或疊加現有 agent pixel art
- 尺寸：100% width × 200px height（可收合）
- `image-rendering: pixelated` — 以低解析度繪製再放大

### 2.2 Tilemap 背景（靜態）
- 等角或俯瞰 2D 辦公室：4 個區域
  - **Desk Zone** — 預設位置（idle）
  - **Server Room** — 執行任務中（doing）
  - **Meeting Room** — review / 協作中
  - **Break Room** — 完成 / 閒置中
- 用 sprite sheet PNG（單一圖片，CSS sprite 技術），不需 Phaser

### 2.3 Agent Sprites
- 每個 agent（琉璃、翡翠、黒曜、琥珀）有對應的像素角色
- 4 方向 × 3 動畫 frame = 12 frames per character
- 動畫狀態：
  | Agent Status | Office Location | Animation |
  |---|---|---|
  | `idle` | Desk | 坐著打字（typing loop） |
  | `doing` | Server Room | 站立工作（standing work） |
  | `review` | Meeting Room | 討論（talking bubble） |
  | `done` | Break Room | 休息（drinking tea） |
  | `error` | Desk | 驚嘆號泡泡 |

### 2.4 Speech Bubbles
- 當 agent 有 active task → 顯示小泡泡 with task title 縮寫
- Error → 紅色驚嘆號泡泡
- Idle → 無泡泡或 zzz

### 2.5 SSE Integration
- 監聽 `agent_state` / `task_received` / `completed` / `error` 事件
- 即時更新 agent 位置和動畫狀態
- Agent 移動有 walk 動畫（lerp between positions）

### 2.6 收合控制
- 「Show Office / Hide Office」toggle button
- 預設展開，記住 localStorage preference
- 收合時回到純數據 dashboard

### 改動檔案
| 檔案 | 改動 |
|------|------|
| `dashboard.html` | Canvas element + JS pixel office engine（~500 行） |
| **新增** sprite sheet | `office_tiles.png` + `agent_sprites.png`（需設計） |
| `dashboard.go` | embed sprite assets |
| `sse.go` | 確保 `agent_state` event 有足夠資訊（agent name + status） |

### Sprite Assets 需求
- Office tilemap: 16×16 tile size, ~20 unique tiles（desk, chair, server, plant, floor, wall...）
- Agent sprites: 16×16 per frame, 4 characters × 12 frames = 48 frames
- 可用 AI 生成（Gemini/DALL-E pixel art）或手繪
- 單一 sprite sheet PNG，Go embed

---

## Phase 3: Dashboard Feature 強化（from reference repos）

> 從兩個參考 repo 偷值得偷的功能

### 3.1 Theme System（from Clawbot Control Center）
- 4 個 shell themes via CSS variables：
  | Theme | Accent | Vibe |
  |---|---|---|
  | **Tetora Classic** (default) | Purple/Blue | 現有配色 |
  | **NES Dark** | NES Blue/Green | 8-bit purist |
  | **Game Boy** | #9bbc0f green | Monochrome retro |
  | **Amber Terminal** | #ffa500 amber | DOS/CRT |
- Theme switcher in Settings tab
- `localStorage` persist
- CSS-only，不需 JS framework

### 3.2 Quick Search — Cmd+K（from OpenClaw Mission Control）
- 全局搜索 modal（keyboard shortcut: Cmd+K / Ctrl+K）
- 搜索範圍：tasks, sessions, agents, projects, memory files
- Backend: 新增 `/api/search?q=` endpoint，query across tables
- Results 顯示 type badge + title + snippet
- Arrow keys 導航 + Enter 跳轉

### 3.3 System Health Panel（from OpenClaw Mission Control）
- Dashboard tab 新增 Health section：
  - Tetora process uptime
  - DB file size
  - Active SSE connections
  - Last cron run status
  - Provider health（API reachable?）
- 用 pixel-window chrome 包裝

### 3.4 Notification Center（from Clawbot Control Center）
- Header 加 🔔 badge with unread count
- Dropdown 顯示最近 N 個事件（task complete, error, cron finish）
- 點擊跳轉到相關 tab/detail
- SSE push 新通知

### 3.5 Memory Browser 強化（from OpenClaw Mission Control）
- Workspace tab 的 Memory 區塊改為 two-panel layout：
  - 左：memory file tree（rules/, memory/, knowledge/, skills/）
  - 右：inline markdown editor with auto-save
- 顯示 file metadata（size, modified time）

### 改動檔案
| 檔案 | 改動 |
|------|------|
| `dashboard.html` | Theme CSS vars + switcher + search modal + health panel + notification center + memory browser |
| `server.go` 或 `http_*.go` | `/api/search` endpoint, `/api/health` endpoint |
| `sse.go` | notification events |

---

## Phase 4: Advanced Polish

> 加分項，非必要但加了會很好

### 4.1 Agent Interaction
- 點擊 pixel office 中的 agent → 彈出 info modal（current task, cost, uptime）
- 右鍵 → context menu（dispatch task, view sessions, view SOUL）

### 4.2 Office Customization
- 用戶可拖動辦公室物件（純裝飾）
- 自訂 agent 外觀（color palette per character）
- 季節性裝飾（聖誕節、萬聖節 sprite swap）

### 4.3 Sound Effects（opt-in）
- Task complete → 8-bit jingle
- Error → fail sound
- New task → coin collect sound
- 音效用 Web Audio API 合成，不需音檔

### 4.4 Mini-map
- Sidebar 小地圖顯示所有 agent 當前位置
- 點擊 mini-map → 主 canvas 聚焦該 agent

---

## Implementation Order & Dependencies

```
Phase 1 (CSS Reskin)          — 獨立，隨時可做
    ↓
Phase 2 (Pixel Office)       — 需要 sprite assets
    ↓
Phase 3.1 (Themes)           — 依賴 Phase 1 的 CSS var 架構
Phase 3.2 (Search)           — 獨立，需 backend endpoint
Phase 3.3 (Health)           — 獨立，需 backend endpoint
Phase 3.4 (Notifications)    — 獨立，需 SSE event type
Phase 3.5 (Memory Browser)   — 獨立
    ↓
Phase 4 (Advanced)           — 依賴 Phase 2
```

## Estimated Effort

| Phase | 行數（估） | Scope |
|---|---|---|
| Phase 1: CSS Reskin | ~300 CSS | dashboard.html only |
| Phase 2: Pixel Office | ~500 JS + assets | dashboard.html + sprites + Go embed |
| Phase 3.1: Themes | ~150 CSS/JS | dashboard.html only |
| Phase 3.2: Search | ~200 JS + ~100 Go | dashboard.html + new endpoint |
| Phase 3.3: Health | ~100 JS + ~50 Go | dashboard.html + new endpoint |
| Phase 3.4: Notifications | ~150 JS + ~30 Go | dashboard.html + SSE |
| Phase 3.5: Memory Browser | ~250 JS | dashboard.html |
| Phase 4: Advanced | ~400 JS | dashboard.html |
| **Total** | **~2,000 行** | |

## Version Mapping

> 與 `tasks/todo.md` 同步，更新於 2026-03-05。

| Version | Content |
|---|---|
| **v1.9** | Phase 1 (CSS Reskin) + Phase 2 (Pixel Office Scene) |
| **v1.10** | Phase 3 (Dashboard Features — search, themes, health, notifications, memory browser) |
| **v2.0** | Phase 4 (Advanced UI) + Agent 隔離執行環境（Terminal Bridge + CLI Provider，合併 milestone） |

---

## Asset Production Plan

Pixel art assets 是此 roadmap 的唯一外部依賴（不是 code，是美術）：

1. **Office Tilemap** (16×16 tiles, ~20 tiles) — 可用 AI 生成後手修
2. **Agent Sprites** (16×16, 4 chars × 12 frames) — 建議手繪或委託
3. **UI Icons** (8×8 or 16×16) — 可從 NES.css icon set 借用

選項：
- A: AI 生成（快，品質中）— Gemini/DALL-E pixel art prompt
- B: Pixel art community 委託（$50-100，品質高）
- C: 自己畫（時間長，最有個性）
- D: 用 open source sprite sheet（免費，但可能不完全符合需求）

建議：先用 AI 生成 placeholder，之後有空再精修或委託。
