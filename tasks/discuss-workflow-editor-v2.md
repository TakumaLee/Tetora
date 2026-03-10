# Workflow Visual Editor v2 — 討論文件

> 用途：獨立對話中討論規格與設計決策
> 規格檔：`tasks/spec-workflow-editor-v2.md`
> 日期：2026-03-10

---

## 需要決策的問題

### 1. Skill 選擇的粒度

現在 `~/.tetora/workspace/skills/` 有 48 個 skill。全列出來太長。

**方案 A**：全部列出，用搜尋框過濾
**方案 B**：按分類分組（dev / strategy / content / ops）
**方案 C**：只列最近用過的 + 搜尋

→ 你傾向哪個？Skill 有分類機制嗎？

---

### 2. Tool Call 的 Input Schema

選了 tool 之後，需要填 `toolInput`。每個 tool 的 input schema 不同。

**方案 A**：選 tool 後動態渲染 schema 欄位（最智慧，開發量大）
**方案 B**：選 tool 後顯示 schema 描述，使用者自己填 JSON
**方案 C**：先用純文字，未來再做動態表單

→ 目前 tool 的 `InputSchema` 是 `json.RawMessage`，格式統一嗎？

---

### 3. DependsOn 的 UI 模式

**方案 A**：Checkbox 多選列表（簡單直覺）
**方案 B**：在 canvas 上拖線連接（已有 port drag 機制）
**方案 C**：兩者都支援（canvas 拖線 + 屬性面板 checkbox）

→ 目前 port drag 可以建立 dependsOn 嗎？如果已經可以，屬性面板只需顯示結果？

---

### 4. Delay 輸入方式

**方案 A**：文字 input + 即時驗證（`30s`, `5m`, `1h`）
**方案 B**：數字 input + 單位下拉（更防呆但限制彈性）
**方案 C**：Slider + 預設值（5s / 30s / 1m / 5m / 15m / 30m / 1h）

→ 實際使用場景中，delay 通常設多久？常見值是什麼？

---

### 5. 全螢幕模式的行為

**方案 A**：Editor section 佔滿 viewport（overlay），ESC 退出
**方案 B**：隱藏 dashboard 其他 section，只留 editor
**方案 C**：新開 browser tab（`/dashboard/workflow-editor?name=xxx`）

→ 你在編輯 workflow 時會需要同時看 dashboard 的其他資訊（workers, tasks）嗎？

---

### 6. Run 確認 Modal 的設計

**方案 A**：簡單確認框（workflow 名稱 + variable 覆寫 + Go/Cancel）
**方案 B**：Dry-run 預覽（顯示會執行哪些步驟、預估時間/成本，再確認）
**方案 C**：直接跑，但底部出現 toast + undo（5 秒內可取消）

→ Workflow 執行有成本（API 呼叫），需要多謹慎的確認？

---

### 7. Error Handling 策略

目前很多操作失靜默失敗。統一策略：

**方案 A**：Toast 通知（右下角，3 秒自動消失）
**方案 B**：Inline error（欄位旁邊紅字）
**方案 C**：混合（操作錯誤用 toast，欄位驗證用 inline）

→ Dashboard 現在有 toast 系統嗎？

---

### 8. 現有 Workflow 案例

目前有哪些 workflow 定義？這些 workflow 的使用頻率和複雜度決定了 editor 的設計重點。

需要確認：
- `standard-dev` 打不開 → 結構有問題還是 bug？
- 最複雜的 workflow 有幾個 step？
- 有沒有用到 condition / parallel / tool_call 的實際案例？
- Variables 用的多嗎？

---

### 9. 新增 API 的安全考量

`GET /api/skills` 和 `GET /api/tools` 是否需要特別的權限控制？
- Skills 列表可能包含敏感的內部流程名稱
- Tools 列表暴露了系統能力

→ 目前 API 都有 Bearer token 認證，這樣夠嗎？

---

### 10. Mobile / 響應式

Dashboard 在手機上看嗎？Workflow editor 需要考慮小螢幕嗎？

→ 如果不需要，可以大膽用桌面優先的 UI（拖拉、右鍵選單等）

---

## 競品參考

值得看的 workflow editor UI：

| 工具 | 特點 | 可借鑑 |
|------|------|--------|
| n8n | 節點式拖拉、即時預覽、mini-map | canvas 互動、節點設計 |
| Retool Workflows | 簡潔屬性面板、step 類型圖示 | 屬性面板 layout |
| Linear | 鍵盤快捷鍵重度使用 | 快捷鍵（Cmd+S 存檔、Delete 刪節點） |
| Figma | 無限畫布、space+drag pan | pan/zoom 互動 |

---

## 實作進度追蹤

- [ ] Phase 1: BUG-1 Save fetchapi
- [ ] Phase 1: BUG-2 standard-dev 載入
- [ ] Phase 1: BUG-3 JSON 即時同步
- [ ] Phase 2: UX-2 UI 加大
- [ ] Phase 2: UX-1 Pan 修復
- [ ] Phase 2: UX-5 DependsOn 選擇
- [ ] Phase 3: UX-4 Skill 下拉
- [ ] Phase 3: UX-6 Tool 下拉
- [ ] Phase 3: UX-3 Delay 驗證
- [ ] Phase 3: UX-7 Run modal
