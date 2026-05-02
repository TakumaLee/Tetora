# Life Stack Archive (2026-05-02)

Archived: 2026-05-03
Reason: Tetora 瘦身計畫 PR1 — life/automation/integration stack 與 Tetora 核心需求（PR review/Workflow/Files/戰情室/Discord）無關，暫存以便未來撿回。

## 還原方式
git mv _archive/life-stack-2026-05/internal/life internal/
git mv _archive/life-stack-2026-05/internal/automation internal/
git mv _archive/life-stack-2026-05/internal/integration internal/
# 逐個還原 tool/ tools/ httpapi/ 單檔
# 記得把 storage/dbtypes.go 的 type 移回 life/lifedb/lifedb.go（或讓 life 套件直接用 storage 型別）
# 還原 wire.go 的 import 與 symbol references
