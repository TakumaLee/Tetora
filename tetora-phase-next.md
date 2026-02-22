# Tetora v2 — Phase Next Roadmap

> Last updated: 2026-02-22
> Status: P0-P8(old) 幾乎全部完成，以下為 New P5-P9 計畫

---

## Completed Phases (Archive)

### P0-P4.4 (2026-02-21)
全部完成。Security, Multi-LLM, Docker Sandbox, Smart Dispatch。

### Old P5-P8 (2026-02-22)
幾乎全部完成。以下為完成狀態：

| Phase | Item | Status |
|-------|------|--------|
| P5.1 | Config routing rules | ✅ |
| P5.2 | Dashboard routing history | ✅ |
| P5.3 | Telegram polish | ✅ |
| P5.4 | Route API async | ✅ |
| P6.1 | Conversational session | ⚠️ Partial (SessionID exists, no multi-turn --resume) |
| P6.2 | Progress & streaming | ❌ Not done |
| P6.3 | Error recovery UX | ✅ (retry + reroute API) |
| P7.1 | Slack bot | ✅ |
| P7.2 | Web Chat UI | ❌ Not done |
| P7.3 | File & image | ✅ |
| P7.4 | Knowledge base | ✅ |
| P8.1 | Observability & metrics | ✅ |
| P8.2 | Config migration | ✅ |
| P8.3 | Backup & export | ✅ |
| P8.4 | Skill system | ✅ |

**額外完成（未在舊 roadmap 中）：**
- Slack Events API bot (slack.go)
- Upload handling (upload.go)
- Knowledge CLI (cli_knowledge.go)
- Skill CLI (cli_skill.go)
- Backup/Restore CLI + API (backup.go, cli_backup.go)
- Config migration engine (migrate.go)
- Routing stats API (/stats/routing, /stats/metrics)
- Failed dispatch retry/reroute API

---

## New P5: Conversational & Streaming（對話與串流）

> 核心目標：**分派各 agent 且可觀察各自 session 的詳細狀況**
> 完成舊 P6.1/P6.2/P7.2 殘留項目 + 增強

### P5.1: Session Manager + Agent Observatory
**目標**: 完整的 multi-turn session 支援 + **per-agent session 觀測**
**新檔案**: `session.go`

**Session 核心:**
- Session table in history.db: `(id, source, source_id, session_id, role, status, created_at, last_active, message_count, total_cost, total_tokens_in, total_tokens_out)`
- Source mapping: telegram:chat_id / slack:thread_ts / http:client → session
- Claude CLI `--resume` flag 支援（使用 session_id 續接對話）
- 自動 session 過期（configurable timeout, default 30min）
- Session status: `active` → `idle` → `expired` → `closed`
- Telegram: `/new` 開新 session, 其餘自動延續
- Slack: thread-based session (thread_ts → session_id)
- HTTP API: `sessionId` 參數支援（POST /dispatch body）

**Session Message Log:**
- session_messages table: `(id, session_id, role, direction, content, cost, tokens_in, tokens_out, created_at)`
- direction: `user` (inbound) / `agent` (outbound) / `system` (internal)
- 每次 dispatch 完成後自動記錄 user prompt + agent response
- 完整對話歷史可回溯

**Per-Agent Session Observatory (核心需求):**
- `GET /sessions` — 列出所有 active sessions, filterable by role/source/status
- `GET /sessions?role=黒曜` — 該 agent 的所有 session
- `GET /sessions/{id}` — Session 詳情 (status, cost, token 用量, message count)
- `GET /sessions/{id}/messages` — 完整對話歷史 (含每條的 cost/tokens)
- `DELETE /sessions/{id}` — 關閉 session
- `POST /sessions/{id}/inject` — 注入 system message 到 session (管理者介入)
- CLI: `tetora session list [--role NAME] [--status active]`
- CLI: `tetora session show <id>` — 顯示 session 詳情 + 最後幾條訊息
- CLI: `tetora session messages <id>` — 完整對話歷史
- CLI: `tetora session close <id>`

**Dashboard: Agent Mission Control (★ 核心 UI):**
- Dashboard 新 section: **"Sessions"** (或稱 "Mission Control")
- **Agent Overview Panel**: 四個 agent card (琉璃/翡翠/黒曜/琥珀)
  - 每個 card 顯示: active sessions 數, 今日 task count, 今日 cost, 狀態燈號
  - 點擊 → 展開該 agent 的所有 sessions
- **Session List**: 可按 agent/status/source 篩選
  - 每行: session ID, role, source (TG/Slack/HTTP), status, message count, cost, last active
- **Session Detail Panel**: 點擊 session 展開完整對話
  - 對話氣泡 (user ↔ agent), 類似 chat UI
  - 每條訊息旁顯示 cost + tokens
  - Session 統計: 總 cost, 總 tokens, 持續時間, 訊息數
  - 操作: Close session, Inject message, Export conversation

### P5.2: SSE Progress Stream
**目標**: 即時任務進度推送
- SSE endpoint: `GET /dispatch/{id}/stream`
- Event types: `started`, `progress`, `output_chunk`, `completed`, `error`
- Provider 層: pipe stdout, 每 N bytes 發送 chunk event
- ClaudeProvider: `--stream-json` or incremental stdout read
- OpenAIProvider: `stream: true` SSE response parsing
- `dispatchState` 加 progress channel per-task
- 自動 heartbeat (每 15s 防止 timeout)
- **Session SSE**: `GET /sessions/{id}/stream` — 訂閱 session 內所有活動

### P5.3: Web Chat UI
**目標**: Dashboard 內嵌對話介面
- Dashboard 新 tab: "Chat"
- 即時對話 (使用 P5.2 SSE streaming)
- Session 管理 (localStorage session_id)
- Role 選擇器 dropdown + auto-route option
- 歷史訊息載入 (query session history)
- Markdown 渲染 (自製 lightweight renderer, 不引入外部 lib)
- Typing indicator (SSE progress events)
- 檔案拖放上傳 (使用現有 /upload endpoint)

### P5.4: Channel Session Sync
**目標**: 跨通道 session 一致性
- Telegram: 發送 "typing" action + 週期性進度更新 (每 5s)
- Slack: 回覆在 thread 中, 顯示 "thinking..." 佔位訊息再更新
- **Context compaction**: 長對話自動摘要 (session messages > N 條時, coordinator 生成摘要取代舊 messages)
  - Config: `"session": {"compactAfter": 20, "compactBudget": 0.02}`
  - 摘要存入 session_messages (direction: "system", content: "Context summary: ...")

### P5.5: Pre-execution Cost Estimate
**目標**: Dispatch 前顯示預估費用，減輕用戶成本焦慮
- `POST /dispatch/estimate` — 根據 prompt length + model + history 預估費用
- Estimate 邏輯: input_tokens * model_rate + estimated_output * model_rate
  - estimated_output 基於: 該 role 過去類似 prompt 的平均 output length
- Telegram: 大額任務 (estimated > $0.50) 自動提示 "預估費用 $X.XX，確認執行？"
- Dashboard Chat: 送出前顯示 estimated cost badge
- CLI: `tetora dispatch --estimate` dry-run mode

---

## New P6: Workflow Engine（工作流引擎）

> 多步驟 agent 協作，DAG 執行

### P6.1: Workflow Definition
**目標**: 定義多步驟工作流格式
**新檔案**: `workflow.go`
- JSON workflow schema:
  ```json
  {
    "name": "code-review-pipeline",
    "steps": [
      { "id": "analyze", "role": "黒曜", "prompt": "分析 {{input}} 的程式碼品質" },
      { "id": "security", "role": "黒曜", "prompt": "安全審查: {{steps.analyze.output}}", "dependsOn": ["analyze"] },
      { "id": "report", "role": "琥珀", "prompt": "撰寫報告: {{steps.analyze.output}}\n{{steps.security.output}}", "dependsOn": ["analyze", "security"] }
    ],
    "variables": { "input": "" },
    "timeout": "30m"
  }
  ```
- Step types: `dispatch` (LLM call), `skill` (external command), `condition` (branch), `parallel` (fan-out)
- Variable system: `{{input}}`, `{{steps.ID.output}}`, `{{steps.ID.status}}`, `{{env.KEY}}`
- Condition step: `{"type": "condition", "if": "{{steps.X.status}} == 'success'", "then": "stepA", "else": "stepB"}`
- 儲存: `~/.tetora/workflows/` 目錄, JSON files

### P6.2: DAG Executor
**目標**: 解析依賴、平行執行、狀態追蹤
**新檔案**: `workflow_exec.go`
- Dependency graph builder: 解析 `dependsOn` → DAG
- Cycle detection (startup validation)
- Parallel executor: 無依賴的 steps 同時執行, 受 maxConcurrent 限制
- Step status: `pending` → `running` → `success` / `error` / `skipped`
- WorkflowRun: `(id, workflow_name, status, started_at, finished_at, step_results[])`
- 中途失敗策略: `onStepFailure: "abort" | "continue" | "retry"`
- 超時控制: per-step timeout + workflow-level timeout
- History: workflow_runs table in history.db
- **每個 step 自動建立 session**, 可在 Mission Control 觀察

### P6.3: Agent Handoff & Auto-delegation
**目標**: Agent 間的輸出傳遞與 context 共享 + **自主轉派**
- Output injection: step output 自動注入下游 step prompt (`{{steps.ID.output}}`)
- Context accumulation: optional `accumulateContext: true` 把所有前置 step output 串接
- Handoff metadata: 每次 handoff 記錄 (from_role, to_role, output_summary, confidence)
- Memory bridge: 在 workflow 執行期間, 各 step 可寫入 workflow-scoped memory
- Review gate: optional step type, coordinator 檢查中間結果再決定是否繼續
- **Auto-delegation (新增)**:
  - Agent 在 output 中標記 `[DELEGATE:翡翠] 這需要市場分析` → 自動轉派
  - 偵測 delegate tag → 建立新 session → 轉派到目標 role → 結果回傳原 session
  - Config: `"delegation": {"enabled": true, "maxDepth": 3}` (防止無限遞迴)
  - Dashboard: delegation chain 可視化 (A → B → C)
- **Agent-to-Agent messaging (新增)**:
  - `POST /agent/{role}/send` — 以 agent 身分發送訊息到另一 agent 的 session
  - Template: `{{agent.send("黒曜", "請 review 這段 code")}}` in prompt
  - 結果自動注入 calling agent 的 context

### P6.4: Workflow API & CLI
**新檔案**: `cli_workflow.go`
- CLI:
  - `tetora workflow list` — 列出 workflows
  - `tetora workflow show <name>` — 顯示 workflow 定義
  - `tetora workflow run <name> [--var key=value]` — 執行
  - `tetora workflow status <run-id>` — 查看執行狀態 (含每個 step 的 session ID)
  - `tetora workflow create` — 互動式建立 (or from file)
  - `tetora workflow delete <name>` — 刪除
- HTTP API:
  - `GET /workflows` — list
  - `POST /workflows` — create/update
  - `GET /workflows/{name}` — show
  - `DELETE /workflows/{name}` — delete
  - `POST /workflows/{name}/run` — execute (body: variables)
  - `GET /workflows/runs/{id}` — run status (含 step sessions)
  - `GET /workflows/runs/{id}/stream` — SSE 即時追蹤 workflow 進度
- Telegram: `/workflow run <name>` trigger
- CronJob: `workflow` field in CronJobConfig → 定時觸發 workflow
- Dashboard: Workflows tab (list, detail, run, status, **step session links**)

---

## New P7: Reliability & Observability（穩定性與可觀測性）

### P7.1: Structured Logging
**目標**: 結構化日誌，取代 ad-hoc log.Printf
**新檔案**: `logger.go`
- Logger struct: level (debug/info/warn/error), component tag, JSON output
- Log levels: configurable per-component (e.g., `"logLevels": {"cron": "debug", "http": "info"}`)
- JSON format: `{"ts":"...","level":"info","component":"cron","msg":"job started","job":"backup"}`
- 向後相容: 預設 text format, `"logFormat": "json"` 啟用 JSON
- Log rotation: 內建 size-based rotation (max 50MB, keep 5 files)
- 取代現有所有 `log.Printf` calls (漸進式)
- `tetora logs` CLI 自動 parse JSON format (filter by level/component)

### P7.2: Circuit Breaker + Model Failover
**目標**: Provider 故障自動隔離、恢復、**自動切換備用 provider**
**新檔案**: `circuit.go`
- CircuitBreaker struct: per-provider, 3 states (closed/open/half-open)
- Config: `"circuitBreaker": {"failThreshold": 5, "resetTimeout": "60s", "halfOpenMax": 2}`
- Closed → Open: 連續 N 次失敗 (default 5)
- Open → Half-Open: resetTimeout 後自動嘗試
- Half-Open → Closed: 連續成功 halfOpenMax 次
- Provider.Execute() wrapper: check circuit before execution
- Dashboard: provider health status indicator (green/yellow/red)
- 通知: circuit open/close events → notify
- **Model Failover (新增)**:
  - Config: `"failover": {"claude": ["openai", "local"], "openai": ["claude"]}`
  - Circuit open → 自動嘗試 failover chain 中的下一個 provider
  - 降級通知: "Claude unavailable, using OpenAI as fallback"
  - Failover 記錄: audit_log (action: "provider.failover", from: "claude", to: "openai")

### P7.3: Enhanced Health Check
**目標**: 深度健康檢查
- `GET /healthz` 擴展為 deep check:
  ```json
  {
    "status": "healthy",
    "uptime": "3d2h15m",
    "checks": {
      "db": { "status": "ok", "latency_ms": 2 },
      "providers": {
        "claude": { "status": "ok", "circuit": "closed" },
        "openai": { "status": "degraded", "circuit": "half-open" }
      },
      "cron": { "status": "ok", "activeJobs": 4, "nextRun": "2026-02-22T10:00:00Z" },
      "disk": { "status": "ok", "usedMB": 45, "freeMB": 1024 },
      "sessions": { "active": 3, "idle": 7, "total": 10 },
      "memory": { "status": "ok", "goroutines": 12 }
    }
  }
  ```
- Shallow check: `GET /healthz?shallow=true` (just "ok", for LB probes)
- Periodic self-check: 每 5 分鐘 run health check, 異常時 notify
- Dashboard: Health section (real-time indicators)

### P7.4: Agent SLA Monitor
**目標**: Per-role 品質追蹤與告警
**新檔案**: `sla.go`
- SLA metrics per role: success_rate, avg_latency, p95_latency, avg_cost
- SLA Config:
  ```json
  "sla": {
    "enabled": true,
    "roles": {
      "黒曜": { "minSuccessRate": 0.95, "maxP95LatencyMs": 60000 },
      "琉璃": { "minSuccessRate": 0.90, "maxP95LatencyMs": 120000 }
    },
    "checkInterval": "1h",
    "window": "24h"
  }
  ```
- Violation detection: 每 checkInterval 計算 sliding window metrics
- Alert: SLA violation → notify (with degradation details)
- Dashboard: per-role SLA status card (✅ / ⚠️ / ❌)
- API: `GET /stats/sla` — 返回各 role 的 SLA 狀態
- History: sla_checks table (role, timestamp, success_rate, p95_latency, violation)

### P7.5: Offline Queue (新增)
**目標**: API 不可用時的任務排隊 + 恢復後自動重送
- Offline detection: provider.Execute() 連續失敗 + circuit open + 所有 failover 都 open
- Task queue: offline_queue table (id, task_json, queued_at, retry_count, status)
- 恢復偵測: health check 發現 provider 恢復 → flush queue (FIFO)
- Queue 上限: max 100 tasks, 超過 → reject + 通知
- Dashboard: Offline Queue indicator (queue depth badge)
- 通知: "API offline, N tasks queued" / "API recovered, flushing N queued tasks"

---

## New P8: Intelligence & DX（智能與開發體驗）

### P8.1: Knowledge Search
**目標**: Knowledge base 全文搜尋與自動 context 注入
- Full-text search: 掃描 `~/.tetora/knowledge/` 下所有檔案
- 搜尋 API: `GET /knowledge/search?q=keyword` → 返回相關片段 + 檔名 + relevance score
- TF-IDF-like scoring: 簡易 term frequency * inverse document frequency (純 Go, 不用外部)
- Auto-context injection: dispatch 時自動搜尋 knowledge, 將相關片段注入 system prompt
  - Config: `"knowledge": {"autoInject": true, "maxChunks": 3, "maxTokens": 2000}`
- CLI: `tetora knowledge search <query>`
- Dashboard Knowledge tab: 加搜尋框
- Template: `{{knowledge.search("query")}}` 在 prompt 中手動觸發搜尋

### P8.2: Agent Reflection
**目標**: Agent 執行後自動反思與品質評分
- Post-execution review: 每次 dispatch 完成後, coordinator 自動評估輸出品質
  - Config: `"reflection": {"enabled": true, "coordinator": "琉璃", "budget": 0.05}`
- Quality score: 1-10 scale, 存入 history (quality_score column)
- Reflection prompt: "Review this output. Score 1-10 for accuracy, completeness, relevance. Brief feedback."
- Auto-learning: 低分 (<5) 結果自動記入 agent memory 作為改進參考
  - `memory set --role 黒曜 --key reflection_feedback --value "..."`
- Dashboard: quality score trend per role
- API: `GET /stats/quality?role=黒曜&days=7`
- 可選: 達到閾值才觸發反思 (e.g., cost > $0.50 的任務才 review)

### P8.3: CLI Autocomplete
**目標**: Shell 自動補全
- 支援 bash / zsh / fish
- `tetora completion bash > /etc/bash_completion.d/tetora`
- `tetora completion zsh > ~/.zfunc/_tetora`
- `tetora completion fish > ~/.config/fish/completions/tetora.fish`
- 補全: subcommands, flags, job names, role names, workflow names, prompt names
- 動態補全: job/role/prompt 名稱從 config/filesystem 讀取

### P8.4: API Documentation
**目標**: 自動生成 API 文件
- 內建 OpenAPI 3.0 spec generator
- `GET /api/spec` — 返回 OpenAPI JSON
- `GET /api/docs` — 內嵌 Swagger UI-like 文件頁面 (lightweight, embedded HTML)
- 從 http.go HandleFunc 註冊自動提取 endpoint 資訊
- 包含: path, method, request body schema, response schema, auth requirements
- CLI: `tetora api-docs [--output openapi.json]`

---

## New P9: Human Trust & Ecosystem（人性信任與生態）

> 解決 OpenClaw 對比 gap + 人性化需求

### P9.1: Trust Gradient (漸進式信任)
**目標**: Per-role 自主權分級，不只是 permissionMode
- Autonomy levels:
  - `observe` — Agent 只能看，不能做 (dry-run mode, 只回覆建議)
  - `suggest` — Agent 提出建議 + 等人工確認 (類似現有 approval gate, 但更細緻)
  - `auto` — Agent 自主執行 (現有預設)
  - `auto+notify` — 自主執行但每次通知
- Config per-role:
  ```json
  "roles": {
    "黒曜": { "autonomy": "auto", ... },
    "翡翠": { "autonomy": "suggest", ... }
  }
  ```
- Task-level override: `"autonomy": "observe"` 覆蓋 role 預設
- Dashboard: 每個 agent card 顯示 autonomy level badge
- Telegram: suggest mode 時回覆 "建議: ...\n/approve or /reject"
- Audit: 所有 autonomy 相關決策記錄

### P9.2: Incoming Webhooks (外部事件觸發)
**目標**: 外部系統 → 觸發 Tetora dispatch
**新檔案**: `webhook_in.go`
- `POST /hooks/{name}` — 接收外部 webhook, 觸發 dispatch
- Webhook 定義:
  ```json
  "incomingWebhooks": {
    "github-pr": {
      "secret": "$GITHUB_WEBHOOK_SECRET",
      "role": "黒曜",
      "promptTemplate": "Review this PR: {{payload.pull_request.html_url}}\nTitle: {{payload.pull_request.title}}\nDiff: {{payload.pull_request.diff_url}}",
      "events": ["pull_request.opened", "pull_request.synchronize"]
    },
    "sentry-alert": {
      "secret": "$SENTRY_SECRET",
      "role": "黒曜",
      "promptTemplate": "Investigate Sentry alert: {{payload.event.title}}\nURL: {{payload.url}}"
    }
  }
  ```
- HMAC signature verification (GitHub style X-Hub-Signature-256)
- Event filter: 只處理指定 events
- Payload template: `{{payload.xxx}}` 展開 JSON body
- Dashboard: incoming webhook 列表 + 觸發歷史
- CLI: `tetora webhook list/add/remove/test`

### P9.3: Notification Intelligence (智慧通知)
**目標**: 減少通知疲勞, 智慧聚合 + 優先級
- Priority levels: `critical` / `high` / `normal` / `low`
  - critical: 立即通知 (SLA violation, security alert, budget exceeded)
  - high: 正常通知 (task complete, approval needed)
  - normal: 可聚合 (job success, routine report)
  - low: 只進 digest (info, debug)
- Smart batching: normal/low 訊息每 N 分鐘聚合成一條 (config: `"notifyBatch": "5m"`)
- Dedup: 相同 event type + role 在 batch window 內只通知一次
- Per-channel priority filter: Telegram 只收 critical+high, Slack 收 all
  ```json
  "notifications": [
    { "type": "telegram", "minPriority": "high" },
    { "type": "slack", "minPriority": "normal", "channel": "#tetora-ops" }
  ]
  ```
- Dashboard: notification history + priority distribution chart

### P9.4: Discord Bot (第三通道)
**目標**: 支援 Discord 作為互動通道
**新檔案**: `discord.go`
- Discord Bot (WebSocket gateway, 不用外部 lib, 純 Go)
- Message event → smart dispatch (same as Telegram/Slack)
- Thread-based session (similar to Slack)
- Slash commands: `/tetora dispatch`, `/tetora route`, `/tetora status`
- Embed response formatting (rich messages)
- Config:
  ```json
  "discord": {
    "enabled": true,
    "botToken": "$DISCORD_BOT_TOKEN",
    "guildID": "...",
    "channelID": "..."
  }
  ```
- Group chat mode: mention-only activation (respond only when @mentioned)

---

## New P10: Personal Assistant（個人助理）

> 核心目標：**讓 Tetora 從「被動的 orchestrator」變成「主動的個人助理」**
> Personal assistant 功能是 orchestration 引擎的「體感介面」，不是附帶品。

### P10.1: Quick Actions（快捷操作）
**目標**: Dashboard 上的快速操作介面 + 鍵盤快捷鍵
**新檔案**: `quickaction.go`

**Command Palette (Cmd+K / Ctrl+K):**
- Dashboard 新增 Command Palette modal (類似 VS Code 的 Ctrl+P)
- Fuzzy search: 搜尋所有可用操作
  - 子命令: dispatch, route, job enable/disable/trigger, workflow run, session close
  - 最近使用的 prompt (從 history 查詢)
  - 已儲存的 Quick Action
  - Agent 名稱 → 直接對話
- 鍵盤導航: ↑↓ 選擇, Enter 執行, Esc 關閉
- 顯示每個操作的 keyboard shortcut (如果有)

**Quick Action 定義:**
```json
"quickActions": [
  {
    "name": "morning-briefing",
    "label": "Morning Briefing",
    "icon": "📋",
    "role": "琉璃",
    "prompt": "今天的排程、待辦事項和重要提醒",
    "shortcut": "g b",
    "category": "daily"
  },
  {
    "name": "code-review",
    "label": "Code Review",
    "icon": "🔍",
    "role": "黒曜",
    "promptTemplate": "Review the code at {{path}}",
    "params": [{"name": "path", "label": "File/PR path", "required": true}],
    "shortcut": "g r",
    "category": "dev"
  }
]
```

**Quick Action 特性:**
- `prompt` — 固定 prompt, 一鍵執行
- `promptTemplate` + `params` — 帶參數的模板, 彈出輸入框填寫
- `shortcut` — 全域鍵盤快捷鍵 (vim-style sequential keys)
- `category` — 分類 (daily, dev, ops, creative)
- `workflow` — 可觸發 workflow 而非單次 dispatch
- `confirm` — 是否需要確認 (default false)

**Dashboard UI:**
- Header 新增 `⌘K` 按鈕
- Quick Action 面板: grid of action cards (icon + label + shortcut)
- 拖放排序 + pin to top
- Telegram: `/quick` 列出所有 quick actions, `/quick <name>` 直接執行

**API:**
- `GET /quick-actions` — 列出所有
- `POST /quick-actions/{name}` — 執行 (body: params)
- CRUD via config (quickActions array in config.json)

**CLI:**
- `tetora quick list` — 列出
- `tetora quick run <name> [--param key=value]` — 執行

### P10.2: Proactive Agent（主動式 Agent）
**目標**: Agent 能主動發起通知、提醒、建議
**新檔案**: `proactive.go`

**觸發機制:**
- **Schedule triggers**: Cron 表達式觸發 (e.g., 每天 09:00 morning briefing)
- **Event triggers**: 事件驅動 (e.g., cost 超過閾值、連續失敗、SLA violation)
- **Context triggers**: 基於對話上下文 (e.g., user 提到 "明天"、"deadline")

**Proactive Rule 定義:**
```json
"proactiveRules": [
  {
    "name": "morning-briefing",
    "enabled": true,
    "trigger": {"type": "schedule", "cron": "0 9 * * MON-FRI", "tz": "Asia/Taipei"},
    "action": {
      "role": "琉璃",
      "prompt": "準備今天的 briefing: 1) 排程中的 cron jobs 2) 昨天的 cost 摘要 3) 任何異常或待處理事項",
      "notify": true,
      "channel": "telegram"
    }
  },
  {
    "name": "cost-alert",
    "enabled": true,
    "trigger": {"type": "event", "event": "budget.warning"},
    "action": {
      "role": "琉璃",
      "prompt": "Budget 即將超限，分析近期 cost 趨勢並建議節省方案",
      "notify": true,
      "priority": "high"
    }
  },
  {
    "name": "daily-digest",
    "enabled": true,
    "trigger": {"type": "schedule", "cron": "0 18 * * MON-FRI", "tz": "Asia/Taipei"},
    "action": {
      "type": "digest",
      "template": "今日摘要:\n- 執行 {{stats.taskCount}} 個任務\n- 總花費 ${{stats.totalCost}}\n- 成功率 {{stats.successRate}}%",
      "notify": true,
      "channel": "telegram"
    }
  },
  {
    "name": "weekly-review",
    "enabled": true,
    "trigger": {"type": "schedule", "cron": "0 10 * * MON", "tz": "Asia/Taipei"},
    "action": {
      "role": "琉璃",
      "prompt": "回顧上週: 分析各 agent 的任務量、cost、品質趨勢，提出改善建議",
      "notify": true
    }
  }
]
```

**Action Types:**
- `dispatch` — 呼叫 agent 並通知結果 (default)
- `digest` — 模板化摘要 (不需要 LLM, 直接查 DB 填充)
- `workflow` — 觸發 workflow
- `notify` — 純通知 (不需要 agent 處理)

**Digest 模板變數:**
- `{{stats.taskCount}}`, `{{stats.totalCost}}`, `{{stats.successRate}}`
- `{{stats.topRole}}`, `{{stats.avgLatency}}`, `{{stats.errorCount}}`
- `{{jobs.pending}}`, `{{jobs.failed}}`, `{{jobs.nextRun}}`
- 查詢區間: 上次 digest 到現在

**Event Bus 整合:**
- 訂閱 SSE bus 的特定 event types
- `budget.warning`, `budget.exceeded`, `sla.violation`, `circuit.open`, `task.error`
- 事件觸發 → 查找匹配 rules → 執行 action

**CLI:**
- `tetora proactive list` — 列出 rules
- `tetora proactive trigger <name>` — 手動觸發
- `tetora proactive history` — 最近觸發紀錄

**Dashboard:**
- Proactive Rules section: 列表 + enable/disable toggle
- 觸發歷史: 最近 N 筆 (time, rule, result)

### P10.3: Email Channel（Email 通道）
**目標**: Email 作為 Tetora 的輸入/輸出通道
**新檔案**: `email.go`

**Outgoing Email (SMTP):**
- 使用 Go stdlib `net/smtp` (零依賴)
- Config:
  ```json
  "email": {
    "enabled": true,
    "smtp": {
      "host": "smtp.gmail.com",
      "port": 587,
      "username": "$EMAIL_USER",
      "password": "$EMAIL_PASSWORD",
      "from": "tetora@example.com"
    },
    "recipients": ["user@example.com"]
  }
  ```
- 作為 notification channel: `{"type": "email", "minPriority": "high"}`
- 支援 HTML 格式 (task result 轉 HTML)
- Digest/proactive 結果可透過 email 發送

**Incoming Email (Webhook):**
- 透過 email service webhook (SendGrid Inbound Parse, Mailgun Routes, etc.)
- `POST /hooks/email` — 接收 email webhook payload
- 解析: From, Subject, Body (text/plain)
- Subject 作為 routing hint (e.g., "[黒曜] Review this code")
- Body 作為 prompt
- 自動建立 session, 回覆透過 email 發送

**Email Template:**
- Subject: `[Tetora] {{role}}: {{task_summary}}`
- Body: task result + metadata (cost, duration, model)
- 支援 text/plain + text/html multipart

**CLI:**
- `tetora email test` — 發送測試郵件
- `tetora email send --to <addr> --subject <subj> --body <body>` — 手動發送

### P10.4: Webhook Response Channel（Webhook 雙向通道）
**目標**: 讓 incoming webhook 變成完整的雙向通道
**修改檔案**: `incoming_webhook.go`, `http.go`

**現狀**: Incoming webhook 觸發 dispatch 但不回傳結果給 source。

**擴展:**
- Webhook config 新增 `responseURL` 和 `responseTemplate`:
  ```json
  "incomingWebhooks": {
    "github-pr": {
      "role": "黒曜",
      "template": "Review PR: {{payload.pull_request.title}}",
      "secret": "$GITHUB_WEBHOOK_SECRET",
      "responseURL": "{{payload.pull_request.comments_url}}",
      "responseAuth": "Bearer $GITHUB_TOKEN",
      "responseTemplate": "{\"body\": \"## Code Review by Tetora\\n\\n{{result}}\"}"
    }
  }
  ```
- Dispatch 完成後, POST result 到 `responseURL`
- `responseAuth` — 回傳時的 auth header
- `responseTemplate` — 回傳 payload 模板 (`{{result}}`, `{{status}}`, `{{cost}}`)
- `responseMethod` — HTTP method (default POST)

**Use Cases:**
- GitHub PR review → 自動留 comment
- Sentry alert → 回報分析結果
- CI/CD webhook → 回報 deploy review
- Slack incoming webhook → 回傳到 Slack

**同步模式 (可選):**
- `"sync": true` — webhook handler 等待 dispatch 完成再回覆
- 適用於需要即時回應的場景
- 超時: `"syncTimeout": "30s"`

### P10.5: Group Chat Intelligence（群組對話智慧）
**目標**: 在群組環境 (Telegram group, Slack channel, Discord server) 中智慧回應
**修改檔案**: `telegram.go`, `slack.go`, `discord.go`

**Activation Mode:**
- `mention` — 只在被 @mention 時回應 (default for groups)
- `keyword` — 偵測特定 keyword 時回應
- `all` — 回應所有訊息 (不建議用於大群)
- Config:
  ```json
  "groupChat": {
    "activationMode": "mention",
    "keywords": ["tetora", "幫我", "help"],
    "cooldown": "30s",
    "maxContextMessages": 10,
    "allowedGroups": ["group_id_1", "group_id_2"]
  }
  ```

**Context Window:**
- 群組中回應時, 自動抓取最近 N 條訊息作為 context
- 只包含 activation 前的對話 (不包含 Tetora 自己的回覆)
- 格式: `[Name]: message` 逐行傳入 prompt
- Config: `maxContextMessages` (default 10)

**Rate Limiting:**
- Per-group cooldown (防止 spam 觸發)
- Per-user rate limit
- Daily cost cap per group

**Permission:**
- `allowedGroups` — 白名單
- Group admin 可 `/enable` 和 `/disable`
- 不在白名單的群組: 回覆 "請聯繫管理員啟用"

**Thread Support:**
- Telegram: 在 reply thread 中回覆 (避免污染主聊天)
- Slack: 在 thread 中回覆
- Discord: 在 thread 中回覆

---

## P10 Priority & Dependencies

```
P10 (Personal Assistant)
  P10.1 Quick Actions     ◄── independent (dashboard + config)
  P10.2 Proactive Agent   ◄── independent (cron + notify + SSE events)
  P10.3 Email Channel     ◄── independent (net/smtp + incoming webhooks)
  P10.4 Webhook Response   ◄── depends on P9.2 (incoming webhooks)
  P10.5 Group Chat Intel   ◄── independent (telegram/slack/discord modifications)
```

建議順序: P10.1 → P10.2 → P10.5 → P10.4 → P10.3
- P10.1 最直覺、最常用
- P10.2 核心 "proactive" 體驗
- P10.5 群組是常見使用場景
- P10.4 擴展現有 webhook
- P10.3 email 最低優先 (大多數用戶已有 TG/Slack)

## P10 Estimated Scope

| Phase | New Files | Modified Files | Est. Lines |
|-------|-----------|----------------|------------|
| P10.1 | quickaction.go, cli_quick.go, quickaction_test.go | config.go, http.go, dashboard.html, telegram.go, completion.go | ~800 |
| P10.2 | proactive.go, proactive_test.go | config.go, http.go, main.go, dashboard.html, completion.go | ~900 |
| P10.3 | email.go, email_test.go | config.go, http.go, notify.go, completion.go | ~600 |
| P10.4 | — | incoming_webhook.go, config.go, http.go | ~300 |
| P10.5 | — | telegram.go, slack.go, discord.go, config.go | ~500 |
| **Total** | **5** | **~15** | **~3,100** |

---

## Priority & Dependencies

```
New P5 (Conversational)  ──► P5.1 先行 (foundation), 其餘依賴 P5.1
  P5.1 session manager + observatory  ◄── foundation for all (★ 核心)
  P5.2 SSE streaming                  ◄── depends on P5.1
  P5.3 web chat UI                    ◄── depends on P5.1 + P5.2
  P5.4 channel session sync           ◄── depends on P5.1
  P5.5 cost estimate                  ◄── independent

New P6 (Workflow)        ──► P6.1 → P6.2 → P6.3 → P6.4 順序執行
  P6.1 workflow definition             ◄── foundation
  P6.2 DAG executor                    ◄── depends on P6.1
  P6.3 agent handoff + auto-delegate   ◄── depends on P6.2 + P5.1 (sessions)
  P6.4 workflow API & CLI              ◄── depends on P6.1-P6.3

New P7 (Reliability)     ──► 大部分獨立
  P7.1 structured logging              ◄── independent
  P7.2 circuit breaker + failover      ◄── independent
  P7.3 enhanced health                 ◄── benefits from P7.2
  P7.4 agent SLA monitor               ◄── benefits from P7.1, P5.1 (sessions)
  P7.5 offline queue                   ◄── depends on P7.2 (circuit)

New P8 (Intelligence)    ──► 全部獨立
  P8.1 knowledge search                ◄── independent
  P8.2 agent reflection                ◄── independent (benefits from P5.1 sessions)
  P8.3 CLI autocomplete                ◄── independent
  P8.4 API documentation               ◄── independent

New P9 (Trust & Eco)     ──► 全部獨立
  P9.1 trust gradient                  ◄── independent
  P9.2 incoming webhooks               ◄── independent
  P9.3 notification intelligence       ◄── independent
  P9.4 discord bot                     ◄── independent (mirrors telegram/slack pattern)
```

---

## User's Core Need — Feature Mapping

> **「分派各個 agent 且可以觀察各自 session 的詳細狀況」**

| Need | Feature | Phase |
|------|---------|-------|
| 分派到指定 agent | Smart dispatch (已有) + Trust gradient | ✅ + P9.1 |
| 觀察各 agent session 列表 | GET /sessions?role=X + Dashboard Agent Overview | P5.1 |
| 查看 session 對話歷史 | session_messages table + GET /sessions/{id}/messages | P5.1 |
| 即時觀察執行中 session | SSE /sessions/{id}/stream + Dashboard real-time | P5.2 |
| Session cost/token 統計 | Per-session metrics in session table | P5.1 |
| 管理者介入 session | POST /sessions/{id}/inject + Dashboard | P5.1 |
| 跨 agent 協作觀察 | Workflow step sessions + delegation chain | P6.3 |
| Agent 健康/SLA 一覽 | Agent SLA cards + Health dashboard | P7.3, P7.4 |
| Web 上直接對話 | Web Chat UI | P5.3 |
| 對話前預估成本 | Cost estimate | P5.5 |

---

## Estimated Scope

| Phase | Items | New Files | Modified Files | Est. Lines |
|-------|-------|-----------|----------------|------------|
| New P5 | 5 | 1 (session.go) | 8 (dispatch, http, telegram, slack, provider_claude, dashboard.html, cli, main) | ~2,800 |
| New P6 | 4 | 3 (workflow.go, workflow_exec.go, cli_workflow.go) | 5 (http, dashboard.html, cron, telegram, main) | ~2,500 |
| New P7 | 5 | 3 (logger.go, circuit.go, sla.go) | 7 (http, provider, dispatch, config, dashboard.html, main, cron) | ~2,200 |
| New P8 | 4 | 1 (reflection.go) | 6 (knowledge, history, dispatch, http, dashboard.html, cli) | ~1,500 |
| New P9 | 4 | 2 (webhook_in.go, discord.go) | 5 (http, config, notify, dashboard.html, main) | ~2,000 |
| **Total** | **22** | **10** | **~20** | **~11,000** |
