# Workflow Visual Editor v2 — 修正與強化規格

> 來源：使用者回報 10 項問題，2026-03-10
> 狀態：待實作

---

## 問題清單與修正方案

### BUG-1: Save 按鈕報錯 `fetchapi is not defined`
**優先級：P0（完全無法使用）**
- `saveWorkflowEditorData()` 裡用了不存在的 `fetchapi` 函數
- **修正**：改用標準 `fetch()` 或現有的 `fetchAPI()` wrapper

### BUG-2: 特定 workflow（如 `standard-dev`）無法打開編輯
**優先級：P0**
- 調查原因：可能是 workflow 名稱含特殊字元、載入失敗沒有錯誤提示
- **修正**：加上 error handling + toast 提示載入失敗原因

### BUG-3: Raw JSON 面板沒有即時同步
**優先級：P1**
- 目前 JSON textarea 只在開啟時填入一次，canvas 上的修改不會反映
- **修正**：每次 canvas 修改後，若 JSON panel 可見，自動更新 textarea 內容

---

### UX-1: 畫布無法拖拉移動（Pan）
**優先級：P1**
- 現有代碼有 space+drag pan 邏輯，但使用者不知道
- 可能 event handler 沒正確綁定，或 space keydown 在 input focus 時被吃掉
- **修正**：
  1. 修復 pan 功能（確認 event 正確綁定）
  2. 加上滑鼠中鍵拖拉支援（無需按 space）
  3. 畫布底部提示改為更明顯：「Space+拖拉 移動畫布 · 滾輪 縮放 · 拖拉節點 移動」

### UX-2: 編輯 UI 太小
**優先級：P1**
- 畫布高度 520px、屬性面板 260px → 擁擠
- **修正**：
  - 畫布高度改為 `calc(100vh - 200px)`，最小 600px
  - 屬性面板寬度改為 320px
  - 新增全螢幕按鈕（⛶），點擊後 editor section 佔滿 viewport
  - 節點尺寸從 148×56 加大到 180×64

### UX-3: Delay 秒數可以亂輸入
**優先級：P2**
- delay 欄位是純文字 input，無驗證
- **修正**：
  - 輸入後即時驗證格式（必須是 `Ns`, `Nm`, `Nh` 格式，如 `30s`, `5m`, `1h`）
  - 無效輸入時：紅框 + 提示「格式：30s / 5m / 1h」
  - 或改為數字 input + 單位下拉（s/m/h）更直覺

### UX-4: Skill 欄位不知道有哪些 skill
**優先級：P1**
- 目前 skill 是純文字輸入
- **修正**：
  - 改為 `<select>` 下拉，從 API 動態載入可用 skills
  - API 端新增 `GET /api/skills` 回傳 `[{name, description}]`（讀取 `~/.tetora/workspace/skills/` 目錄）
  - 下拉選項顯示：`skill-name — 簡短描述`

### UX-5: DependsOn 不知道有哪些 step ID
**優先級：P1**
- 目前是逗號分隔的文字輸入
- **修正**：
  - 改為多選 checkbox 列表，列出目前 workflow 內所有其他 step（排除自己）
  - 顯示格式：`☑ step-id (type: dispatch, agent: kokuyou)`
  - 或用 tag-style UI：點擊 + 按鈕展開可選 ID 列表

### UX-6: Tool Call 不知道有哪些 tool
**優先級：P1**
- 目前 toolName 是純文字輸入
- **修正**：
  - 改為 `<select>` 下拉，從 API 動態載入
  - API 端新增 `GET /api/tools` 回傳 `[{name, description}]`（從 ToolRegistry）
  - 選擇 tool 後，自動顯示該 tool 的 inputSchema 欄位，讓使用者填入

### UX-7: Run 按鈕功能不明確
**優先級：P2**
- 使用者不知道 Run 按鈕做什麼
- **修正**：
  - 按鈕改為「▶ Run」帶 tooltip：「執行此 workflow」
  - 點擊後彈出確認 modal：
    - 顯示 workflow 名稱
    - 列出 variables 可覆寫
    - 確認 / 取消
  - 執行後自動切換到 Workflow Runs 區域，顯示即時狀態

---

## 實作順序

### Phase 1: 修 Bug（必須先做）
1. BUG-1: Save `fetchapi` → `fetch`
2. BUG-2: `standard-dev` 載入失敗 debug + error handling
3. BUG-3: JSON 即時同步

### Phase 2: 核心 UX 改善
4. UX-2: UI 加大 + 全螢幕
5. UX-1: Pan 修復 + 中鍵支援
6. UX-5: DependsOn 智慧選擇

### Phase 3: 欄位智慧化
7. UX-4: Skill 下拉（需新 API）
8. UX-6: Tool 下拉（需新 API）
9. UX-3: Delay 格式驗證
10. UX-7: Run 確認 modal

---

## 需要新增的 API

### `GET /api/skills`
```json
[
  {"name": "go-backend", "description": "Go REST API patterns"},
  {"name": "content-strategy", "description": "Multi-platform content ROI"},
  ...
]
```
來源：讀取 `cfg.SkillsDir` 下的目錄名 + 每個 skill 的第一行描述

### `GET /api/tools`
```json
[
  {"name": "memory_get", "description": "Retrieve a memory by key"},
  {"name": "web_search", "description": "Search the web"},
  ...
]
```
來源：`ToolRegistry.tools` map

---

## 影響檔案
- `dashboard/workflow-editor.js` — 主要修改
- `dashboard/style.css` — UI 尺寸調整
- `dashboard/body.html` — HTML 結構微調
- `http_workflow.go` — 新增 `/api/skills`, `/api/tools` 端點
- `tool.go` — 暴露 tool list 方法
