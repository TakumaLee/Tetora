# ChildSem 並行系統改善方案

> 狀態：提案 / 未實作
> 日期：2026-02-28
> 來源：agent_comm.go, dispatch.go, config.go 現有架構分析

## 現況問題

Tetora 的 agent 並行控制用兩個 `chan struct{}` 做 semaphore：
- `sem`（top-level，default 8 slots）
- `childSem`（sub-agent，default maxConcurrent×2 = 16 slots）

配合 `spawnTracker` 限制每個 parent 的 child 數量（default 5）。

**四個瓶頸：**

1. **Parent 同步 block** — `toolAgentDispatch()` 用 HTTP 同步呼叫 /dispatch，parent 被 block 到 child 完成（15min+），佔著 parent sem slot
2. **Child pool 固定** — 啟動後不能調整容量，SIGHUP reload 無法改 channel size
3. **沒有優先級** — Go channel FIFO，所有任務平等競爭 slot
4. **Timeout 不分深度** — DefaultTimeout=900s 對所有深度一致

## 改善方案

### Phase 1: Per-Depth Timeout（最低風險）

**概念**：深層 child 用更短 timeout，防止資源長期佔用。

**設計**：
- `config.go` AgentCommConfig 加 `DepthTimeoutDecay float64`（default 0.6）和 `MinChildTimeout int`（default 60s）
- `agent_comm.go` 新增 `depthAdjustedTimeout(cfg, baseTimeout, depth)`
- 公式：`effective = base × decay^depth`，有 floor
- 效果：depth 0=900s → 1=540s → 2=324s → 3=194s

**影響範圍**：只改 agent_comm.go + config.go，零介面變更。

---

### Phase 2: Async Child Dispatch

**概念**：Parent 能 fire-and-forget 多個 child，不 block，然後 poll 結果。讓 `maxChildrenPerTask=5` 真正發揮。

**設計**：
- `agent_dispatch` tool args 加 `async bool`
- async=true 時：開 goroutine 做 HTTP call，立即回傳 taskId
- 新增 `agent_poll` tool：接受 taskIds[]，回傳各 ID 狀態（pending/completed/error）
- `asyncResultStore`（sync.Mutex + map）存結果，30min TTL 清理
- spawnTracker release 改在 goroutine 內 defer（而非 toolAgentDispatch 本體）

**影響範圍**：agent_comm.go, tool_core.go, tool.go, plugin.go, main.go（cleanup goroutine）

**注意**：
- goroutine 的 spawnTracker release 時機要區分 sync/async
- 需要背景 cleanup 防止 memory leak

---

### Phase 3: PrioritySem（優先級 Semaphore）

**概念**：用 `container/heap` 實作優先級 semaphore，取代 `chan struct{}`。

**設計**：
- 新檔 `semaphore.go`
- `Semaphore` struct：mutex + cond + counter + `priorityWaitHeap`
- Methods：`Acquire(priority)`, `Release()`, `Resize(newCap)`, `TryAcquire()`, `Stats()`
- `priorityFromTask(t Task) int`：depth 越淺 priority 越高，user-interactive +20，sub-agent -10
- 替換 16 個子系統的 `chan struct{}` → `*Semaphore`（機械式 type swap）
- SIGHUP handler 加 `sem.Resize()` — channel 做不到，Semaphore 可以

**影響範圍**：最大。觸及所有持有 sem/childSem 的 struct（Server, 各 Bot, CronEngine, queueDrainer, workflow*, TaskBoardDispatcher）。
但改動是機械式的——struct field type 和 constructor parameter type 換掉就好。

**Priority 計算**：
```
base = 100 - depth*10
user-interactive (discord/telegram/slack): +20
cron: +0
agent_dispatch: -10
queue retry: -20
```

---

### Phase 4: Adaptive Pool Sizing

**概念**：背景 goroutine 根據 metrics 動態調整 semaphore capacity。

**依賴**：Phase 3 的 `Semaphore.Resize()` 和 `Stats()`。

**設計**：
- `config.go` 加 `AdaptivePoolConfig`（enabled, minCap, maxCap, scaleUpWaitMs, scaleDownUtil, checkInterval, cooldown）
- 新檔 `adaptive_pool.go`：`AdaptivePoolMonitor`
- 每 30s tick 一次：
  - avgWaitMs > 5000 && waiting > 0 → 擴容 25%（不超 maxCap）
  - utilization < 0.3 && waiting == 0 → 縮容 25%（不低於 minCap）
- 預設 disabled，需 `agentComm.adaptivePool.enabled: true`

**影響範圍**：config.go, 新檔 adaptive_pool.go, main.go（啟動 goroutine）

---

## 向後相容

| 新設定 | Default | 不設定時行為 |
|--------|---------|-------------|
| `depthTimeoutDecay` | 0.6 | depth=0 不受影響 |
| `minChildTimeout` | 60 | 只在 decay 推低到 60s 以下才生效 |
| `async` (tool arg) | false | 原有同步行為 |
| `adaptivePool.enabled` | false | pool 固定不動 |
| `*Semaphore` + priority=0 | — | 等同原本 FIFO channel |

## 實作順序建議

Phase 1 → 2 → 3 → 4，每個獨立可部署。
Phase 1-2 互相獨立。Phase 4 依賴 Phase 3。

## 關鍵檔案

| 檔案 | 角色 |
|------|------|
| `agent_comm.go` | Phase 1 (timeout), Phase 2 (async dispatch/poll) |
| `dispatch.go` | Phase 3 (selectSem, priorityFromTask, Acquire/Release) |
| `config.go` | All phases (AgentCommConfig 擴充) |
| `main.go` | Phase 3 (sem 建立), Phase 4 (monitor 啟動) |
| `semaphore.go` (新) | Phase 3 核心 |
| `adaptive_pool.go` (新) | Phase 4 核心 |
| 16 個 bot/subsystem files | Phase 3 (type swap) |
