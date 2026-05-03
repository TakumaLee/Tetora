# Spec：Hermes Agent Autonomy — 能力移植 + 自治架構

> Status: Draft
> PR: #111（進行中）
> Date: 2026-05-02

---

## 1. 架構原則（Why）

這份 spec 描述兩個方向的設計：hermes-agent-self-evolution 能力移植，以及 Tetora agent 自治架構。兩者共享同一組核心原則。

### 1.1 角色分工

| 層 | 職責 | 禁止 |
|----|------|------|
| **Tetora（基礎設施）** | 儲存記憶、技能、知識；提供工具和 CLI；管理 agent 目錄 | 主動使用任何記憶或知識；替 agent 組裝 context |
| **Agent（自治執行者）** | 用自己的 LLM 判斷需要什麼；自己讀取；自己執行 | 依賴 Tetora 預先注入內容 |
| **Dispatch（純路由）** | 傳遞 `{task} → {agent}` | 注入 SOUL.md；組裝 system prompt |

### 1.2 Agent Spawn 的正確模式

```
watcher 偵測到 agent 有 task
  → spawn: claude --cwd ~/.tetora/agents/<name>/ -p "[task content]"
  → agent 讀自己的 CLAUDE.md 初始化
  → 執行任務
```

**不注入 SOUL.md，不預先組裝 prompt。** Claude Code 的 `--cwd` 搭配 CLAUDE.md `@include` 語法是唯一的初始化機制。

---

## 2. Layer 1：Skill Auto-Evolution + Deep Memory Extraction（已完成）

### 2.1 Skill Auto-Evolution

**是什麼**：自動偵測表現不佳的 skill，用 LLM 生成改寫 proposal，讓人工審核後決定是否套用。

**怎麼做**：

- 實作位置：`internal/skill/evolve.go`
- 觸發條件：失敗率 >= 40%（最低 10 次 invocation，7 天冷卻期）
- 流程：掃描 → Haiku LLM 生成改寫 → 寫入 `skills/<name>/proposals/<timestamp>.md`
- 預設關閉：`cfg.SkillEvolve.Enabled: false`

**CLI 介面**：

```
tetora skill evolve list            # 列出所有待審 proposals
tetora skill evolve approve <name>  # 套用 proposal
tetora skill evolve reject <name>   # 拒絕 proposal
```

### 2.2 Deep Memory Extraction

**是什麼**：高品質任務完成後，自動萃取跨 session 可複用的知識，寫入共享記憶。

**怎麼做**：

- 實作位置：`internal/memory/extract.go`
- 觸發條件：reflection score >= 4 AND cost >= $0.10 AND 任務成功
- 萃取工具：Haiku LLM
- 寫入目標：
  - `memory/extract:<slug>.md`（CRUD 防幻覺，先讀再寫）
  - `memory/auto-extracts.md`（FIFO 100 條 index）
- 預設關閉：`cfg.DeepMemoryExtract.Enabled: false`

### 2.3 Tetora-Agent I/O Protocol

**是什麼**：定義 Tetora 與 agent 之間固定介面規則的治理文件。

- 存放位置：`~/.tetora/workspace/rules/tetora-agent-io-protocol.md`
- 已寫入 `workspace/rules/INDEX.md`
- 自動注入所有 agent prompts

---

## 3. Layer 2：Agent 自治初始化（`tetora agent configure`）

### 3.1 目標

為每個 agent 建立自我初始化所需的檔案結構。Agent 在 spawn 時能自己載入身份、能力清單、協議，不依賴 Tetora 注入。

### 3.2 生成的檔案結構

```
~/.tetora/agents/<name>/
  CLAUDE.md                        ← Claude Code 自動讀取（@include 語法）
  capabilities/
    index.md                       ← 能力清單（永遠在 context，極小 token）
    memory-subscribe.md            ← cross-session memory 使用說明
    skill-evolve.md                ← skill evolve 操作說明
    weekly-review.md               ← lesson promote + rule audit 說明
```

### 3.3 CLAUDE.md 結構

由 `configure` 指令生成，不含 Task Protocol 執行步驟：

```markdown
@SOUL.md
@capabilities/index.md
@~/.tetora/workspace/rules/tetora-agent-io-protocol.md
```

### 3.4 capabilities/index.md 結構

- 能力清單（compact，極小 token）
- 每個能力：有什麼用、何時用、detail 檔案路徑
- **不展開任何內容**，純描述性清單
- Agent 判斷需要哪個能力後，自己用 Read tool 載入 detail 檔案

範例格式：

```markdown
# Capabilities

## memory.auto-extracts
訂閱跨 session 萃取的知識。
Detail: capabilities/memory-subscribe.md

## skill-evolve
管理 skill 改寫 proposals（list / approve / reject）。
Detail: capabilities/skill-evolve.md

## weekly-review
週期性 lesson promote + rule audit（每週日 09:00）。
Detail: capabilities/weekly-review.md
```

### 3.5 動態載入原則

| 內容 | 載入時機 | 機制 |
|------|----------|------|
| `CLAUDE.md` @include 鏈 | Spawn 時自動 | Claude Code `--cwd` |
| `capabilities/index.md` | 永遠在 context | CLAUDE.md @include |
| 各能力 detail 檔案 | Agent 判斷需要時 | Agent 自己 Read |

Tetora 不預先注入任何 detail 內容。

### 3.6 configure 執行流程

```
1. 讀取現有 agent 清單（agentsDir 或 config）
2. Dispatch 任務給 agent：
   「這是 Tetora 提供的能力清單，你需要哪些？JSON 回答」
3. 解析 agent 的 JSON 回應（selected_capabilities + reason）
4. 生成：
   - CLAUDE.md
   - capabilities/index.md
   - 各個選中的 capability detail 檔案
5. 如果 agent 選了 weekly-review → 更新 jobs.json 加 cron job
```

### 3.7 能力選項清單（configure 時呈現給 agent）

```json
{
  "capabilities": [
    {
      "id": "memory.auto-extracts",
      "description": "訂閱跨 session 萃取的知識",
      "detail_file": "capabilities/memory-subscribe.md"
    },
    {
      "id": "skill-evolve",
      "description": "管理 skill 改寫 proposals",
      "detail_file": "capabilities/skill-evolve.md"
    },
    {
      "id": "weekly-review",
      "description": "週期性 lesson promote + rule audit",
      "detail_file": "capabilities/weekly-review.md",
      "cron": "0 9 * * 0"
    },
    {
      "id": "deep-memory-extract",
      "description": "高品質任務後自動萃取跨 session 知識",
      "config_flag": "deepMemoryExtract.enabled"
    }
  ]
}
```

### 3.8 CLI 介面

```
tetora agent configure <name>    # 詢問指定 agent 要哪些能力，生成檔案
tetora agent configure --all     # 對所有已註冊 agent 執行
```

- 實作位置：`internal/agent/configure.go`

---

## 4. Layer 3：Task Watcher（`tetora agent watch`）

### 4.1 目標

偵測哪個 agent 有被 assign 的 task，自動 spawn 正確的 claude 進程。Spawn 時不注入 system prompt，讓 agent 的 CLAUDE.md 自己處理初始化。

### 4.2 Spawn 命令格式

```bash
claude --cwd ~/.tetora/agents/<name>/ -p "[task content]"
```

Agent 收到的是 task 內容，不是帶著 SOUL.md 的角色扮演 prompt。

### 4.3 兩種 Watch 模式

**延遲性（watcher loop）**：

```
tetora agent watch
  → 定時掃描 taskboard（assigned 狀態的 tasks）
  → 每個有 task 的 agent → spawn claude in agent dir
  → 可整合進現有 cron 系統（job ID: agent_watcher）
```

**立即性**：

- Tetora assign task 後立刻 spawn
- 不注入 SOUL.md，走 CLAUDE.md 初始化
- 實作細節待定：現有 dispatch 加 flag `useAgentDir: true`

### 4.4 CLI 介面

```
tetora agent watch              # 啟動 watcher（前台）
tetora agent watch --daemon     # 後台運行
```

- 實作位置：`internal/agent/watcher.go`

---

## 5. 檔案結構對照

| 檔案 | 狀態 | 說明 |
|------|------|------|
| `internal/skill/evolve.go` | 已完成 | Skill auto-evolution |
| `internal/memory/extract.go` | 已完成 | Deep memory extraction |
| `~/.tetora/workspace/rules/tetora-agent-io-protocol.md` | 已完成 | Tetora-Agent 介面規則 |
| `internal/agent/configure.go` | 待實作 | agent configure 邏輯 |
| `internal/agent/watcher.go` | 待實作 | task watcher |
| `~/.tetora/agents/<name>/CLAUDE.md` | 由 configure 生成 | Agent 自我初始化進入點 |
| `~/.tetora/agents/<name>/capabilities/index.md` | 由 configure 生成 | Agent 能力清單（常駐 context）|
| `~/.tetora/agents/<name>/capabilities/*.md` | 由 configure 生成 | 各能力 detail（按需載入）|

---

## 6. PR 策略

| PR | 內容 | 狀態 |
|----|------|------|
| #111 | Layer 1（skill evolve + memory extract + I/O protocol）+ Layer 2（agent configure）+ Layer 3（watcher）| 進行中 |
| 新 PR（之後） | Task 狀態 workflow（todo → in_progress → done 完整流程）| 尚未開始 |

**暫時不在範圍內**：Task 狀態 workflow 涉及 taskboard 設計，需獨立討論後另開 PR。

---

## 7. Open Questions

| # | 問題 | 影響範圍 | 優先度 |
|---|------|----------|--------|
| 1 | Watcher 的立即性模式：在 dispatch 加 `useAgentDir: true` flag，還是另開獨立路徑？ | `internal/agent/watcher.go`、現有 dispatch 邏輯 | 高 |
| 2 | `configure` 步驟 2 的 dispatch 方式：用現有 dispatch 系統，還是直接 spawn claude 問能力？ | `internal/agent/configure.go` | 高 |
| 3 | Watcher daemon 的 process 管理：PID 檔案？整合進現有 cron 系統還是獨立進程？ | `internal/agent/watcher.go` | 中 |
| 4 | `configure --all` 的執行順序：sequential 還是 concurrent？失敗一個要不要停？ | CLI UX | 低 |
| 5 | 能力選項之後如何擴充？是否有 capability registry 機制？ | 長期可擴充性 | 低 |
