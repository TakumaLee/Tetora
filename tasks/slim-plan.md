# Tetora 瘦身計畫（2026-05-03 更新）

> **目標**：砍冗餘套件 + 拆衛星服務，減少 root binary 體積與 internal/ 套件數。
> **原始動機**：679MB / 896 .go 檔 / 85 internal 套件 / root 24 檔 84,833 行
> **Branch**: `chore/delete-leaf-packages-2026-05`

---

## 完成進度

### PR1 ✅ — Life Stack Archive (2026-05-02)
移至 `_archive/life-stack-2026-05/internal/`：life/*, automation/*, integration/*（個人生活管理，與核心需求無關）

### PR2 ✅ — Delete Batch (2026-05-03)
移至 `_archive/delete-2026-05/internal/`：benchmark, canvas, classify, handoff, nlp
- handoff 邏輯 inline 至 `internal/dispatch/handoff_inline.go`
- classify 的 ChatSources/MaxSession* inline 至 `internal/dispatch/complexity.go`
- 所有 caller 修正，`go test ./...` ✅

### PR3 ✅ — Life-Stack Batch (2026-05-03)
移至 `_archive/life-stack-2026-05/internal/`：recap, backup, health
- health 邏輯 inline 至 `health_inline.go` + `health_disk_*.go`（root） + `internal/cron/disk_*.go`
- backup HTTP endpoint 換成 501 stub；cli/backup.go 換成 "not available"
- recap Discord watcher 從 main.go 移除
- usage, notify：**退守 internal/**（耦合太深，見下方決策）
- `go test ./...` ✅

---

## 下一步

### PR4 — FOLD 薄套件（小工程，可繼續）

目標：把真正薄的套件 fold 進 caller，不建 standalone 套件。

| 套件 | LOC | 動作 | Caller |
|------|-----|------|--------|
| `httputil` | ~50 | → inline httpapi 或 root | httpapi/* 幾個 helper |
| `text` | ~172 | → inline caller | usage.go + 其他 |
| `trace` | ~thin | → 用 log 直接取代 | 少量 caller |
| `crypto` | ~thin | → inline root | 少量 caller |

做法：先 grep caller，確認 < 3 個檔，再 inline + 移除套件。

### PR5 — EXTRACT Discord（大工程，獨立計畫）
- `internal/discord/` → `cmd/tetora-discord/`
- 需要：定義 IPC/webhook 協議、重新思考 notify 依賴
- 預估：1–2 天，需要獨立規劃

### PR6 — EXTRACT Warroom + Dashboard（大工程，獨立計畫）
- `internal/warroom/` + dashboard → `cmd/tetora-warroom/`
- 預估：1–2 天，需要獨立規劃

---

## 套件分類決策（基於 dependency 分析更新）

### 原計畫 ARCHIVE → 改為 KEEP

以下套件原計畫 archive，但 dependency 分析顯示耦合太深，**改分類為 KEEP**：

| 套件 | 原因 | Caller 數 |
|------|------|-----------|
| `audit` | cross-cutting logging，18 個 caller（含所有 httpapi/*） | 18 |
| `workspace` | dispatch 核心路徑（ResolveWorkspace, LoadSoulFile 等） | 2 (critical) |
| `sla` | http.go + wire.go + httpapi/stats.go 整合 | 5 |
| `scheduling` | wire.go + main.go + tests，多個 type | 4 |
| `retention` | wire.go 複雜清理邏輯 | 2 |
| `usage` | wire.go stats + wire_test.go 測試 + httpapi stats | 多個 |
| `notify` | main.go 啟動流程核心（notification engine） | 1 (critical) |

**Why**：archive 這些會製造大量 boilerplate inline，讓 root package 更臃腫。瘦身目標是減少無意義的套件數，不是為了 archive 而 archive。

### KEEP（核心）
cli, httpapi, taskboard, workflow, tools, skill, provider, tool, config, dispatch, cron, reflection, knowledge, session, history, worktree, prompt, version, rule, cost, review, log, db, metrics, dedupguard, circuit, roles, migrate, audit, workspace, sla, scheduling, retention, usage, notify

### 仍在 ARCHIVE 候選（需評估）
- `storage` vs `store`：重複關係待釐清
- `telemetry` vs `usage`：重疊嫌疑
- `pairing`：個人單機用途

---

## 技術備忘

- Go 忽略 `_archive/` 開頭目錄（build tag 機制）
- 搬移 + 修 caller 是標準流程，不需要 build tag trick
- 每次 PR 後必跑 `go build ./...` + `go test ./...`
- dispatch 套件在 root 層要用 `dtypes "tetora/internal/dispatch"` 別名（因為 `dispatch` 是 root 的函數名）
