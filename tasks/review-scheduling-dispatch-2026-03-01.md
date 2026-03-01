# Tetora 排程/派工/狀態系統 Code Review 報告

> 日期：2026-03-01（二次驗證修訂版）
> 範圍：taskboard_dispatch.go, dispatch.go, taskboard.go, route.go, cron.go, workflow_trigger.go, workflow.go, workflow_exec.go, config.go

---

## 驗證結論

初次 review（haiku agent）報告了 4 個 Critical Issues 和 7 個 Warnings。
**二次驗證（opus 逐行比對原始碼）發現：其中 C1-C4 和 W1-W4、W6 全部已在現有程式碼中修復。**

以下為驗證後的準確狀態。

---

## 一、已確認修復的項目（無需行動）

### ~~C1. Condition Step 跳過分支死鎖~~ → 已修復

**驗證：** `workflow_exec.go:336-347`
`handleConditionResult()` 現在回傳 `[]string`（被 skip 的 step IDs），呼叫端正確地做了：
```go
for _, sid := range skippedSteps {
    completed++
    for _, dep := range dependents[sid] {
        remaining[dep]--
        if remaining[dep] == 0 { readyCh <- dep }
    }
}
```
被跳過的分支的下游 dependents 會被正確解鎖。

---

### ~~C2. Webhook Trigger TOCTOU~~ → 已修復

**驗證：** `workflow_trigger.go:302-338`
`HandleWebhookTrigger()` 現在全程使用 `e.mu.Lock()`（write lock），cooldown 檢查和更新在同一個鎖範圍內完成：
```go
e.mu.Lock()
// ... find trigger, check enabled ...
// Check cooldown under write lock to prevent TOCTOU race.
expiry, ok := e.cooldowns[triggerName]
if ok && !time.Now().After(expiry) { e.mu.Unlock(); return err }
// Set cooldown immediately before releasing lock.
e.cooldowns[triggerName] = time.Now().Add(d)
e.mu.Unlock()
go e.executeTrigger(e.ctx, triggerCopy, payload)
```

---

### ~~C3. DAG Step Goroutine 無 panic recovery~~ → 已修復

**驗證：** `workflow_exec.go:275-285`
Step goroutine 已有 panic recovery，確保 doneCh 一定會收到訊息：
```go
go func(id string) {
    defer func() {
        if r := recover(); r != nil {
            logError("workflow step panic", "step", id, "recover", r)
            doneCh <- stepDoneMsg{id: id, result: &StepRunResult{Status: "error", Error: fmt.Sprintf("panic: %v", r), ...}}
        }
    }()
    // ...
    doneCh <- stepDoneMsg{id: id, result: result}
}(stepID)
```

---

### ~~C4. SQL 值未 escape~~ → 已修復

**驗證：** `taskboard_dispatch.go:138, 151`
`resetStuckDoing()` 中 `cutoff` 和 `nowISO` 現在都經過 `escapeSQLite()` 包裹：
```go
sql := fmt.Sprintf(`... updated_at < '%s'`, escapeSQLite(cutoff))
updateSQL := fmt.Sprintf(`... updated_at = '%s' WHERE id = '%s' AND ...`,
    escapeSQLite(time.Now().UTC().Format(time.RFC3339)),
    escapeSQLite(id),
)
```

---

### ~~W1. failedTasks map 無 TTL 清理~~ → 已修復

**驗證：** `dispatch.go:1395-1405` 有 `cleanupFailedTasks()` 函數，在 `http.go:546` 的定期清理 goroutine 中呼叫。

---

### ~~W2. Cooldown map 永不清理~~ → 已修復

**驗證：** `workflow_trigger.go:166-176` 有 `cleanupExpiredCooldowns()`，每 10 次 tick 呼叫一次（line 158-161）。

---

### ~~W3. Approval channel nil panic~~ → 已修復

**驗證：** `cron.go:1061, 1077`
`ApproveJob()` 和 `RejectJob()` 都有 nil guard：
```go
if !j.pendingApproval || j.approvalCh == nil {
    return fmt.Errorf("job %q is not pending approval", id)
}
```
且 `approvalCh` 是 buffered（cap 1），即使 approval 在 timeout 和 nil-set 之間到達，也不會阻塞或 panic。

---

### ~~W4. Webhook trigger 用 context.Background()~~ → 已修復

**驗證：** `workflow_trigger.go:341`
Webhook 使用 `e.ctx`（engine-scoped context），不是 `context.Background()`：
```go
go e.executeTrigger(e.ctx, triggerCopy, payload)
```
`e.ctx` 在 `Start()` 中由 `context.WithCancel(ctx)` 建立（line 85），shutdown 時可正常取消。

---

### ~~W6. Unassigned tasks 靜默跳過~~ → 已有 log

**驗證：** `taskboard_dispatch.go:196`
已有 logInfo 提示：
```go
logInfo("taskboard dispatch: skipping unassigned task", "id", t.ID, "title", t.Title)
```
可考慮升級為 `logWarn`，但非必要。

---

## 二、仍存在的問題（需修復）

### W5. Parallel sub-steps 缺少提前取消機制 (Low)

**檔案：** `workflow_exec.go:674-685`

```go
for i, sub := range step.Parallel {
    wg.Add(1)
    go func(idx int, s WorkflowStep) {
        defer wg.Done()
        sr := &StepRunResult{...}
        e.runStepOnce(ctx, &s, sr)  // ctx 有傳入
        // ...
    }(i, sub)
}
wg.Wait()  // 等待全部完成，無提前退出
```

`ctx` 有傳入 `runStepOnce`，所以 context 取消會傳播到底層任務。但 `wg.Wait()` 本身無法提前返回——即使 context 已取消，仍需等所有 goroutine 結束（底層 `runSingleTask` 可能需要時間響應取消）。

**嚴重度：** Low — context 有傳播，只是返回速度受限於最慢的取消響應。
**建議：** 可用 `errgroup.WithContext` 模式替代，或加 select 等待 ctx.Done + wg channel。非緊急。

---

### W7. Cost update 與 status update 非原子 (Low)

**檔案：** `taskboard_dispatch.go:288-305`

Task status 透過 `MoveTask()` 更新後，cost/duration 由獨立 UPDATE 寫入。如果 cost UPDATE 失敗：
- Task status 已經是 done/failed（正確）
- 但 cost_usd、duration_ms、session_id 為空值（資料不完整）

```go
// Step 1: status 已改
if result.Status == "success" { d.engine.MoveTask(taskID, "review" or "done") }
// Step 2: cost 獨立更新（可能失敗）
costSQL := fmt.Sprintf(`UPDATE tasks SET cost_usd = %.6f, duration_ms = %d, session_id = '%s' ...`)
```

**嚴重度：** Low — 狀態流正確，只是 metadata 可能缺失。
**建議：** 可將 cost 欄位寫入合併到 `MoveTask()` 的 UPDATE 中，或在 cost 失敗時 retry。

---

### W8. costSQL 的 updated_at 未 escape (Cosmetic)

**檔案：** `taskboard_dispatch.go:291-297`

```go
costSQL := fmt.Sprintf(`
    UPDATE tasks SET cost_usd = %.6f, duration_ms = %d, session_id = '%s', updated_at = '%s'
    WHERE id = '%s'
`,
    result.CostUSD,
    result.DurationMs,
    escapeSQLite(result.SessionID),
    time.Now().UTC().Format(time.RFC3339),  // ← 未 escape
    escapeSQLite(t.ID),
)
```

`session_id` 和 `id` 有 escape，但 `updated_at` 的時間值未 escape。RFC3339 不含單引號所以安全，但與 `resetStuckDoing()` 的寫法不一致。

**嚴重度：** Cosmetic — 不影響安全，但應統一風格。
**建議：** 加上 `escapeSQLite()` 保持一致。

---

### NEW-1. 巢狀 Condition skip 時，被跳過的 condition 的 then/else 分支仍會執行 (Medium)

**檔案：** `workflow_exec.go:336-347, 368-401`

場景：
```
condA → (then: condB, else: stepX)
condB → (then: stepY, else: stepZ)
stepY depends on condB
stepZ depends on condB
```

如果 condA 選 "else"（stepX）：
1. condB 被標記為 skipped ✓
2. condB 的 dependents（stepY, stepZ）被解鎖 ← 問題
3. stepY 和 stepZ 都進入 readyCh，兩個都會被執行
4. 但 condB 被 skip 了，它的 then/else 分支不應該都跑

**原因：** `handleConditionResult()` 只在 condition step 真正**執行**後才呼叫。如果 condition 是被上層 skip 的，只會走 DAG 的通用傳播路徑（line 340-346），不會觸發分支跳過邏輯。

**嚴重度：** Medium — 只影響巢狀 condition 場景。單層 condition 不受影響。
**建議：** 在 DAG 傳播 skipped step 時，如果被 skip 的 step 是 condition 類型，應遞迴 skip 其 then/else 分支。或在步驟開始執行前檢查其上游 condition 是否已被 skip。

---

## 三、描述不準確需校正的地方

### D1. SmartDispatch 分類模型是 haiku→sonnet 兩階段，不是單一 sonnet

**檔案：** `route.go:209-235`

LLM 分類先用 haiku（line 210），low confidence 才升級 sonnet（line 222-235）。描述寫 "預設用 sonnet model 做分類" 不完全對。正確描述：**先 haiku 低成本分類，低信心時自動升級 sonnet**。

### D2. SmartDispatch 不使用 dispatch DefaultModel

**檔案：** `route.go:373-388`

SmartDispatch 的 model 走 `fillDefaults()` → 全域 DefaultModel，不經過 TaskBoard 的 `dispatchCfg.DefaultModel`。Model 優先順序描述只對 TaskBoard 派工成立。

### D3. AutoRetryFailed 是 inline 呼叫，不是定期掃描

**檔案：** `taskboard_dispatch.go:335`

`AutoRetryFailed()` 只在任務失敗時 inline 呼叫，不是獨立排程。Daemon crash 前的失敗不會被重試（但 `resetStuckDoing()` 會補救卡住的任務）。

### D4. MaxConcurrentTasks=0 的行為

描述寫 "0（無限制，但實際上前一批跑完才會啟動下一批）"。

實際行為（`taskboard_dispatch.go:180-183, 199`）：
- `maxTasks=0` → 不限制每次掃描的派發數量
- **但**掃描時如果 `activeCount > 0` 會跳過整個 scan（line 180-183）
- 所以確實是「前一批跑完才啟動下一批」，但這是 activeCount guard 造成的，不是 maxTasks

正確描述：`MaxConcurrentTasks=0` 表示每次 scan 不限量派發，但 scan 本身有防護——上一批 goroutine 還在跑時會跳過整輪 scan。

---

## 四、描述遺漏的重要功能

| 功能 | 檔案位置 | 說明 |
|------|----------|------|
| haiku→sonnet 兩階段分類 | `route.go:209-235` | 先低成本試，低信心才升級 |
| Coordinator Fallback 模式 | `route.go:293-306` | `SmartDispatch.Fallback = "coordinator"` 跳過 LLM |
| ReviewBudget | `config.go:540` | SmartDispatch 有獨立 review 預算 |
| Approval 機制 | `cron.go:708-762` | Cron job 可設 require approval，阻塞等人工核准 |
| Startup Replay | `cron.go:79,646-647` | 崩潰恢復，重啟後重跑未完成的 job |
| Event Trigger wildcard | `workflow_trigger.go:262-270` | `workflow_*` 匹配 `workflow_started` 等 |
| Idle Gate | `cron.go:562-572` | `IdleMinHours > 0` 時只在系統閒置才執行 |
| Project Workdir 解析 | `taskboard_dispatch.go:268-273` | 任務可指定 Project 自動找工作目錄 |

---

## 五、修復優先級

| 優先級 | 項目 | 難度 | 涉及檔案 |
|--------|------|------|----------|
| **P1** | NEW-1: 巢狀 condition skip 傳播 | 中等 | `workflow_exec.go` |
| **P2** | W5: parallel sub-step 提前取消 | 簡單 | `workflow_exec.go` |
| **P2** | W7: cost+status 原子更新 | 中等 | `taskboard_dispatch.go` |
| **P3** | W8: costSQL updated_at escape 一致性 | 簡單 | `taskboard_dispatch.go` |

---

## 六、整體評價

**架構設計紮實**。初次 review 報告的 4 個 Critical 和大部分 Warnings 全部已在現有程式碼中修復，說明開發過程中已有意識地處理了這些邊界情況。

唯一值得注意的新發現是 **巢狀 condition skip 傳播**（NEW-1），這在使用複雜 workflow（condition 嵌套 condition）時可能導致非預期的分支執行。建議在實作複雜 workflow 前修復。

其餘問題（W5/W7/W8）均為 Low/Cosmetic 級別，不影響正常使用。
