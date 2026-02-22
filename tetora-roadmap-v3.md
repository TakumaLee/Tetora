# Tetora v2 — Roadmap v3: OpenClaw Parity + Beyond

> Last updated: 2026-02-23
> 對照組: OpenClaw (v2026.2.19)
> 原則: 零外部 Go 依賴不變，外部 API 可用 HTTP client 呼叫

---

## OpenClaw vs Tetora 完整功能對照

### Legend
- ✅ 已有且功能完整
- ⚠️ 部分有 / 簡化版
- ❌ 沒有
- 🔶 Tetora 獨有 (OpenClaw 沒有)
- ⊘ 不適用 / 刻意不做

### 1. Core Architecture

| Feature | OpenClaw | Tetora | Status |
|---------|----------|--------|--------|
| Daemon/Gateway | Node.js WS (port 18789) | Go HTTP server | ✅ 不同實作，同等功能 |
| Session management | Append-only event logs, branching | SQLite sessions + session_messages | ✅ |
| Multi-agent routing | Binding rules (channel/peer/guild) | Smart dispatch (keyword + LLM) | ⚠️ 機制不同，功能類似 |
| Event system | WS events (agent/chat/presence/cron) | SSE (started/progress/chunk/completed) | ✅ |
| Config format | JSON5 + env overrides | JSON + $ENV_VAR resolution | ✅ |
| Provider support | Anthropic, OpenAI, Google, Ollama | Claude CLI, OpenAI-compatible | ✅ |
| Circuit breaker | — | Per-provider state machine + failover | 🔶 |
| Cost governance | — | Budget cap + auto-downgrade + kill switch | 🔶 |

### 2. Channels

| Channel | OpenClaw | Tetora | Gap |
|---------|----------|--------|-----|
| Telegram | ✅ (grammY) | ✅ | — |
| Slack | ✅ (Bolt) | ✅ (Events API) | — |
| Discord | ✅ (discord.js) | ✅ (pure Go WS) | — |
| WebChat | ✅ (built-in) | ✅ (Dashboard Chat UI) | — |
| WhatsApp | ✅ (Baileys) | ❌ | **P12.1** |
| Signal | ✅ | ❌ | P13+ |
| iMessage | ✅ (BlueBubbles) | ❌ | ⊘ macOS only |
| Teams | ✅ | ❌ | P13+ |
| Matrix | ✅ | ❌ | P13+ |
| Google Chat | ✅ | ❌ | P13+ |
| LINE | ❌ | ❌ | P13+ (JP market) |

### 3. Tool System (★ 最大差距)

| Feature | OpenClaw | Tetora | Gap |
|---------|----------|--------|-----|
| **Tool registry** | 25+ built-in tools | Skill system (external commands) | **P10.1** |
| **Agentic loop** (tool call → execute → continue) | ✅ Agent Runtime | ❌ Single-shot dispatch | **P10.1** |
| **MCP host** | Native lifecycle management | Passthrough to Claude CLI | **P10.2** |
| **Tool profiles** | minimal/coding/messaging/full | — | **P10.3** |
| **Tool policy cascade** | Global→Provider→Agent→Group→Sandbox | — | **P10.3** |
| **Tool approval** | exec confirmation | Trust gradient (task-level) | **P10.3** |
| **Loop detection** | ✅ | — | **P10.3** |
| exec/bash | ✅ | ❌ (via Claude CLI only) | P10.1 built-in |
| read/write/edit | ✅ | ❌ | P10.1 built-in |
| web_search | ✅ (Brave API) | ❌ | **P11.4** |
| web_fetch | ✅ | ❌ | **P11.4** |
| memory_search/get | ✅ (agent-callable) | ❌ (API only) | P10.1 built-in |
| session tools | ✅ (list/send/spawn/history) | ❌ (API only) | P10.1 built-in |
| message tool | ✅ (cross-channel) | ❌ | P10.1 built-in |
| browser | ✅ (CDP) | ❌ | P13+ |
| canvas/A2UI (MCP Apps) | ✅ | ❌ | **P12.6** |
| nodes (camera/screen/location) | ✅ | ❌ | P13+ |
| image analysis | ✅ | ❌ | P12+ |

### 4. Skills

| Feature | OpenClaw | Tetora | Gap |
|---------|----------|--------|-----|
| Skill definition | SKILL.md frontmatter | name + command in config | ⚠️ |
| ClawHub registry | 5,000+ skills | — | ⊘ (not needed for personal tool) |
| Skill discovery | Auto-search + install | — | ⊘ |
| Per-agent skills | Workspace-scoped | Global | **P10.5** |
| Dynamic injection | Context-aware (only relevant) | Always inject all | **P10.1** |
| Skill env vars | Per-skill env config | — | Nice-to-have |

### 5. Memory

| Feature | OpenClaw | Tetora | Gap |
|---------|----------|--------|-----|
| Key-value memory | MEMORY.md | agent_memory table | ✅ |
| Session history | Append-only logs | session_messages table | ✅ |
| **Vector embedding** | SQLite + Voyage/OpenAI/Gemini | — | **P10.4** |
| **Hybrid search** | BM25 + vector similarity | TF-IDF only | **P10.4** |
| Daily notes | YYYY-MM-DD.md auto-generated | — | Nice-to-have |
| Memory auto-indexing | File watchers (1.5s debounce) | — | P10.4 |
| Context compaction | Auto-summary older turns | — | **P11.5** |
| Knowledge base | — | ✅ (knowledge.go + TF-IDF search) | 🔶 |
| TF-IDF with CJK | — | ✅ (bigram tokenizer) | 🔶 |

### 6. Voice & Audio

| Feature | OpenClaw | Tetora | Gap |
|---------|----------|--------|-----|
| Voice wake | ✅ (ElevenLabs) | ❌ | P13+ |
| Talk mode | ✅ | ❌ | P13+ |
| TTS | ✅ (ElevenLabs) | ❌ | P12.4 |
| STT | ✅ | ❌ | P12.4 |
| Audio transcription | ✅ | ❌ | P12.4 |

### 7. Companion Apps

| Feature | OpenClaw | Tetora | Status |
|---------|----------|--------|--------|
| macOS menu bar / Desktop | ✅ | ❌ (PWA only) | **P12.7** (Wails v3) |
| iOS app | ✅ | ❌ (PWA only) | **P12.7** (Capacitor) |
| Android app | ✅ | ❌ (PWA only) | **P12.7** (Capacitor) |
| Web Push notifications | — | ❌ | **P12.7** (VAPID) |

### 8. Security

| Feature | OpenClaw | Tetora | Gap |
|---------|----------|--------|-----|
| Docker sandbox | ✅ (per-session) | ✅ (global) | ⚠️ 需升級到 per-session |
| Tool sandboxing policy | 6-level cascade | — | **P10.3** |
| DM pairing | ✅ (approval code) | ❌ | **P12.3** |
| Allowlists | ✅ (per-channel, per-agent) | ⚠️ (basic) | **P12.3** |
| Prompt injection defense | ✅ (structured wrapping) | ✅ (sanitization) | ⚠️ |
| Security monitor | — | ✅ (security.go) | 🔶 |
| Audit log | — | ✅ (audit.go) | 🔶 |

### 9. Observability

| Feature | OpenClaw | Tetora | Gap |
|---------|----------|--------|-----|
| Dashboard | ✅ (Control UI) | ✅ (4338-line Dashboard) | ✅ |
| Structured logging | — | ✅ (logger.go, JSON/text) | 🔶 |
| Trace ID | — | ✅ (trace.go) | 🔶 |
| **Prometheus metrics** | — | ❌ | **P10.5** |
| Health check | ✅ (basic) | ✅ (deep /healthz) | 🔶 |
| SLA monitor | — | ✅ (sla.go) | 🔶 |
| API documentation | — | ✅ (OpenAPI + Swagger UI) | 🔶 |

### 10. Automation

| Feature | OpenClaw | Tetora | Gap |
|---------|----------|--------|-----|
| Cron jobs | ✅ (3 schedule types + backoff) | ✅ (30s tick + TZ + backoff) | ✅ |
| Webhooks incoming | ✅ | ✅ (HMAC + template + filter) | ✅ |
| Webhooks outgoing | ✅ (from cron) | ✅ (webhook.go) | ✅ |
| Gmail Pub/Sub | ✅ | ❌ | ⊘ |

### 11. Unique to Tetora (OpenClaw 沒有)

| Feature | File | Description |
|---------|------|-------------|
| Cost Governance | cost.go | Budget cap, auto-downgrade, kill switch, alerts |
| Trust Gradient | trust.go | observe/suggest/auto per-role, promotion |
| Agent Reflection | reflection.go | Post-task LLM self-assessment |
| Workflow Engine | workflow.go + workflow_exec.go | DAG + parallel + condition + dry-run/shadow |
| SLA Monitor | sla.go | Per-role metrics + violation alerts |
| Offline Queue | queue.go | Enqueue on provider fail, auto-drain |
| Circuit Breaker | circuit.go | Per-provider state machine + failover |
| Notification Intelligence | notify_intel.go | Priority routing, batch, dedup |
| Knowledge Base + TF-IDF | knowledge.go + knowledge_search.go | CJK bigram tokenizer |
| Config Versioning | version.go | Snapshot, restore, JSON diff, prune |
| Data Retention + GDPR | retention.go | Per-table retention, PII redaction, export/purge |
| Smart Dispatch | route.go | Keyword + LLM two-tier routing |
| Incoming Webhooks | incoming_webhook.go | HMAC verify, payload template, filter |
| Shell Completion | completion.go | bash/zsh/fish generators |

---

## Phase 10: Tool Engine & Intelligence Foundation

> 目標: 補齊與 OpenClaw 的結構性差距 — Tool System、Semantic Memory、Metrics
> 預估: ~4,800 行

### P10.1: Tool Engine + Agentic Loop (~1,500 lines)

**新檔案**: `tool.go`, `tool_test.go`
**修改**: `config.go`, `provider.go`, `provider_claude.go`, `provider_openai.go`, `dispatch.go`, `http.go`, `dashboard.html`

#### 核心概念: Agentic Loop

目前 Tetora 的 dispatch 是 **single-shot**: prompt → provider → response → done.
需要升級為 **agentic loop**: prompt → provider → [tool_use detected] → execute tool → inject result → provider continues → ... → final response.

```
User Message
    ↓
Context Assembly (system prompt + memory + skills + session history)
    ↓
Provider Call (streaming)
    ↓
┌─► Parse Output
│   ├── text → accumulate response
│   ├── tool_use → execute tool → tool_result → re-inject ─┐
│   └── stop → final response                               │
└────────────────────────────────────────────────────────────┘
```

#### Tool Registry

Config schema (`config.json`):
```json
{
  "tools": {
    "maxIterations": 10,
    "timeout": 120,
    "builtin": {
      "exec": true,
      "read": true,
      "write": true,
      "edit": true,
      "web_search": false,
      "web_fetch": true,
      "memory_search": true,
      "memory_get": true,
      "session_list": true,
      "session_send": true,
      "message": true,
      "knowledge_search": true
    }
  }
}
```

#### Built-in Tools (Phase 1)

| Tool | Description | Implementation |
|------|-------------|----------------|
| `exec` | Run shell command (returns stdout/stderr/exit code) | os/exec, timeout, allowedDirs check |
| `read` | Read file contents (with line range) | os.ReadFile, maxSize limit |
| `write` | Create/overwrite file | os.WriteFile, allowedDirs check |
| `edit` | Apply string replacement to file | Read + Replace + Write |
| `web_fetch` | Fetch URL content (HTML → plain text) | net/http GET, html strip tags |
| `memory_search` | Search agent memory by query | Existing agent_memory query |
| `memory_get` | Get specific memory value by key | Existing agent_memory get |
| `session_list` | List active sessions (filterable) | Existing sessions query |
| `session_send` | Send message to another session | Inject into session |
| `message` | Send message to channel (TG/Slack/Discord) | Existing notify infrastructure |
| `knowledge_search` | Search knowledge base | Existing TF-IDF search |
| `cron_list` | List scheduled cron jobs | Existing cron jobs query |
| `cron_create` | Create/update cron job (agent self-schedule) | Insert into jobs.json |
| `cron_delete` | Delete cron job | Remove from jobs.json |

#### Tool Definition Interface

```go
type ToolDef struct {
    Name        string          `json:"name"`
    Description string          `json:"description"`
    InputSchema json.RawMessage `json:"input_schema"` // JSON Schema
    Handler     ToolHandler     `json:"-"`
    Builtin     bool            `json:"-"`
    RequireAuth bool            `json:"requireAuth,omitempty"` // needs trust >= suggest
}

type ToolCall struct {
    ID    string          `json:"id"`
    Name  string          `json:"name"`
    Input json.RawMessage `json:"input"`
}

type ToolResult struct {
    ToolUseID string `json:"tool_use_id"`
    Content   string `json:"content"`
    IsError   bool   `json:"is_error,omitempty"`
}

type ToolHandler func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error)
```

#### Provider Changes

Both `ClaudeProvider` and `OpenAIProvider` need:
1. Serialize tool definitions into API-specific format (Claude `tools[]`, OpenAI `tools[]`)
2. Parse tool_use blocks from response
3. Support multi-turn tool loop (provider.CallWithTools)

**Claude API (direct, not CLI)**: P10.1 建議同時加入 Claude HTTP API provider，不再只依賴 CLI.
- `provider_claude_api.go` — 直接 HTTP 呼叫 Anthropic Messages API
- 支援 `tools` parameter + `tool_use` / `tool_result` content blocks
- Streaming via SSE

**OpenAI API**: 已有 HTTP provider，需增加 `tools` + `tool_calls` parsing.

#### Agentic Loop in dispatch.go

```go
func (e *Engine) dispatchAgentic(ctx context.Context, task Task) TaskResult {
    messages := assembleContext(task)
    tools := resolveTools(task.Role, task.SessionType)

    for i := 0; i < e.cfg.Tools.MaxIterations; i++ {
        result := e.provider.Call(ctx, ProviderRequest{
            Messages: messages,
            Tools:    tools,
        })

        // Accumulate text output
        // Check for tool_use blocks
        toolCalls := extractToolCalls(result)
        if len(toolCalls) == 0 {
            return finalResult(result) // No more tool calls, done
        }

        // Execute tools (with trust check)
        for _, tc := range toolCalls {
            toolResult := executeTool(ctx, tc, task)
            messages = append(messages, toolResultMessage(toolResult))
            // Emit SSE event for each tool call
            e.broker.Publish(SSEEvent{Type: "tool_call", ...})
        }

        // Continue loop with tool results injected
    }
    return errorResult("max tool iterations reached")
}
```

#### HTTP API

- `GET /api/tools` — List available tools (respects role)
- `GET /api/tools/{name}` — Tool detail + schema

#### Dashboard

- Tool Calls panel in session detail (shows tool name, input, output, latency)
- Real-time tool execution via SSE `tool_call` / `tool_result` events

#### Tests (~30)

- Tool registration, resolution, built-in handler execution
- Agentic loop: single tool call, multi-tool call, max iterations
- Tool serialization for Claude API / OpenAI API
- Each built-in tool handler: exec, read, write, edit, web_fetch, memory, session, message

---

### P10.2: MCP Host (~800 lines)

**新檔案**: `mcp_host.go`, `mcp_host_test.go`
**修改**: `config.go`, `tool.go`, `http.go`

#### 概念

Tetora 成為 MCP host，直接管理 MCP server 的生命週期，取代 Claude CLI 的 `--mcp-config` passthrough.

#### MCP Server Lifecycle

```
Config: mcpServers[name] = { command, args, env }
                ↓
Gateway Start → spawn each MCP server process
                ↓
Initialize handshake (JSON-RPC over stdio)
    → initialize request (capabilities negotiation)
    ← initialize response
    → initialized notification
                ↓
Tool Discovery
    → tools/list request
    ← tools/list response (name, description, inputSchema for each tool)
    → Register tools in ToolRegistry with "mcp:" prefix
                ↓
Runtime
    Agent requests tool "mcp:server:toolName"
    → tools/call request { name, arguments }
    ← tools/call response { content }
    → Return result to agentic loop
                ↓
Shutdown → send close notification → kill process
```

#### Config Schema

```json
{
  "mcpServers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/path/to/dir"],
      "env": {}
    },
    "github": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-github"],
      "env": { "GITHUB_TOKEN": "$GITHUB_TOKEN" }
    }
  }
}
```

#### MCP Protocol (JSON-RPC 2.0 over stdio)

```go
type MCPServer struct {
    Name    string
    Cmd     *exec.Cmd
    Stdin   io.Writer
    Stdout  *bufio.Scanner
    Tools   []ToolDef
    mu      sync.Mutex
    nextID  int
}

// JSON-RPC message format
type JSONRPCRequest struct {
    JSONRPC string      `json:"jsonrpc"`
    ID      int         `json:"id"`
    Method  string      `json:"method"`
    Params  interface{} `json:"params,omitempty"`
}
```

#### Features

- Hot-reload: config change → restart affected MCP servers
- Health monitoring: detect crashed MCP servers, auto-restart (max 3 retries)
- Tool namespace: MCP tools prefixed with server name → `github:create_issue`
- Timeout: per-call timeout (default 30s)
- Audit: all MCP tool calls logged to audit_log

#### HTTP API

- `GET /api/mcp/servers` — List MCP server status
- `POST /api/mcp/servers/{name}/restart` — Restart specific server

#### Tests (~20)

- MCP server spawn + initialize handshake (mock server)
- Tool discovery + registration
- Tool call routing + result parsing
- Server crash detection + restart
- Config hot-reload

---

### P10.3: Tool Policy + Trust Integration (~600 lines)

**新檔案**: `tool_policy.go`, `tool_policy_test.go`
**修改**: `config.go`, `tool.go`, `trust.go`

#### Tool Profiles

```json
{
  "tools": {
    "profiles": {
      "minimal": { "allow": ["memory_search", "knowledge_search"] },
      "standard": { "allow": ["read", "write", "edit", "exec", "memory_search", "memory_get", "knowledge_search", "web_fetch", "session_list"] },
      "full": { "allow": ["*"] }
    },
    "defaultProfile": "standard"
  }
}
```

#### Per-Role Tool Policy

```json
{
  "roles": [
    {
      "name": "翡翠",
      "tools": {
        "profile": "standard",
        "allow": ["web_search", "web_fetch"],
        "deny": ["exec"]
      }
    },
    {
      "name": "黒曜",
      "tools": {
        "profile": "full"
      }
    }
  ]
}
```

#### Trust Integration (tool-level)

| Trust Level | Tool Behavior |
|-------------|---------------|
| `observe` | Tool calls logged but **not executed**, result = "[OBSERVE MODE: tool call would execute {name}({input})]" |
| `suggest` | Tool call shown to user (TG/Slack/Dashboard), wait for approval → execute or reject |
| `auto` | Execute immediately |

Per-tool override:
```json
{
  "tools": {
    "trustOverride": {
      "exec": "suggest",
      "write": "suggest",
      "message": "auto"
    }
  }
}
```

#### Loop Detection

```go
type loopDetector struct {
    history []string // last N tool call signatures
    maxRep  int      // max allowed repetitions (default 3)
}

// Detect: same tool + same input called > maxRep times
func (d *loopDetector) check(name string, input string) bool
```

When detected: inject system message "Tool call loop detected. Please try a different approach." and stop tool execution.

#### Tests (~15)

- Profile resolution (role profile + allow/deny merge)
- Trust-level filtering (observe/suggest/auto per tool)
- Loop detection (same call repeated, mixed calls)
- Policy cascade: global → role → tool-specific trust

---

### P10.4: Semantic Memory (~800 lines)

**新檔案**: `embedding.go`, `embedding_test.go`
**修改**: `knowledge_search.go`, `memory.go`, `config.go`, `http.go`

#### Embedding Provider

```json
{
  "embedding": {
    "enabled": true,
    "provider": "openai",
    "model": "text-embedding-3-small",
    "endpoint": "https://api.openai.com/v1/embeddings",
    "apiKey": "$OPENAI_API_KEY",
    "dimensions": 1536,
    "batchSize": 20
  }
}
```

支援任何 OpenAI-compatible embedding endpoint (OpenAI, Ollama, local server).

#### Vector Storage (SQLite)

```sql
CREATE TABLE IF NOT EXISTS embeddings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source TEXT NOT NULL,       -- 'knowledge', 'memory', 'session'
    source_id TEXT NOT NULL,    -- knowledge doc ID, memory key, session ID
    content TEXT NOT NULL,      -- original text chunk
    embedding BLOB NOT NULL,    -- float32 array serialized
    metadata TEXT DEFAULT '{}', -- JSON metadata
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_embeddings_source ON embeddings(source);
```

#### Cosine Similarity (pure Go)

```go
func cosineSimilarity(a, b []float32) float32 {
    var dot, normA, normB float32
    for i := range a {
        dot += a[i] * b[i]
        normA += a[i] * a[i]
        normB += b[i] * b[i]
    }
    if normA == 0 || normB == 0 { return 0 }
    return dot / (sqrt(normA) * sqrt(normB))
}
```

#### Hybrid Search

```go
func hybridSearch(query string, source string, topK int) []SearchResult {
    // 1. TF-IDF results (existing)
    tfidfResults := tfidfSearch(query, source, topK*2)

    // 2. Vector results
    queryVec := getEmbedding(query)
    vectorResults := vectorSearch(queryVec, source, topK*2)

    // 3. Reciprocal Rank Fusion (RRF)
    merged := rrfMerge(tfidfResults, vectorResults, k=60)

    return merged[:topK]
}
```

#### MMR Re-ranking (Maximal Marginal Relevance)

去冗餘：從 top candidates 中依序選取，每次選與已選結果最不相似的：

```go
func mmrRerank(results []SearchResult, queryVec []float32, lambda float64, topK int) []SearchResult {
    // lambda=1.0 → pure relevance, lambda=0.0 → pure diversity
    // default lambda=0.7
    selected := []SearchResult{}
    for len(selected) < topK && len(results) > 0 {
        best := argmax(results, func(r) {
            return lambda*sim(r.Vec, queryVec) - (1-lambda)*maxSim(r.Vec, selected)
        })
        selected = append(selected, results[best])
        results = remove(results, best)
    }
    return selected
}
```

#### Temporal Decay

記憶隨時間衰減，避免舊資料淹沒新資料：

```go
func temporalDecay(score float64, createdAt time.Time, halfLifeDays float64) float64 {
    age := time.Since(createdAt).Hours() / 24
    decay := math.Pow(0.5, age/halfLifeDays) // exponential decay
    return score * decay
}
// Default halfLifeDays = 30
// MEMORY (curated) entries: no decay (halfLife = 0 means skip)
```

Config:
```json
{
  "embedding": {
    "mmr": { "enabled": true, "lambda": 0.7 },
    "temporalDecay": { "enabled": true, "halfLifeDays": 30 }
  }
}
```

#### Auto-Indexing

- Knowledge entries: index on add/update
- Agent memories: index on set
- Session messages: index on dispatch completion (configurable, off by default to save cost)
- Background goroutine for batch embedding (respect rate limits)

#### HTTP API

- `POST /api/embedding/search` — Semantic search across all sources
- `POST /api/embedding/reindex` — Force reindex all content
- `GET /api/embedding/status` — Index statistics

#### Tests (~20)

- Cosine similarity (unit vectors, orthogonal, identical)
- Embedding provider call (mock HTTP)
- Vector storage CRUD
- Hybrid search (RRF merge)
- Auto-indexing triggers

---

### P10.5: Prometheus Metrics Export (~400 lines)

**新檔案**: `prom.go`, `prom_test.go`
**修改**: `http.go`, `dispatch.go`, `provider.go`, `circuit.go`

#### Prometheus Text Exposition Format (no external dependency)

```go
type promMetric struct {
    name   string
    help   string
    typ    string // "counter", "gauge", "histogram"
    labels []string
}

func (m *promMetric) Write(w io.Writer, values ...labeledValue) {
    fmt.Fprintf(w, "# HELP %s %s\n", m.name, m.help)
    fmt.Fprintf(w, "# TYPE %s %s\n", m.name, m.typ)
    for _, v := range values {
        fmt.Fprintf(w, "%s{%s} %v\n", m.name, v.labels, v.value)
    }
}
```

#### Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `tetora_dispatch_total` | counter | role, status | Total dispatches |
| `tetora_dispatch_duration_seconds` | histogram | role | Dispatch latency |
| `tetora_dispatch_cost_usd` | counter | role | Total cost |
| `tetora_dispatch_tool_calls_total` | counter | role, tool | Tool call count |
| `tetora_provider_requests_total` | counter | provider, status | Provider API calls |
| `tetora_provider_latency_seconds` | histogram | provider | Provider response time |
| `tetora_provider_tokens_total` | counter | provider, direction | Token usage (in/out) |
| `tetora_circuit_state` | gauge | provider | 0=closed, 1=open, 2=half-open |
| `tetora_session_active` | gauge | role | Active session count |
| `tetora_queue_depth` | gauge | — | Offline queue depth |
| `tetora_cron_runs_total` | counter | status | Cron job executions |
| `tetora_mcp_server_status` | gauge | server | 0=down, 1=up |

#### Endpoint

`GET /metrics` — Skip auth middleware (like /healthz), Prometheus scrape target.

#### Tests (~10)

- Counter increment + output format
- Gauge set + output format
- Histogram observe + bucket output
- Full /metrics endpoint output validation

---

### P10.6: Agent Workspace Isolation (~500 lines)

**新檔案**: `workspace.go`, `workspace_test.go`
**修改**: `config.go`, `dispatch.go`, `roles.go`, `session.go`

#### 概念

每個 agent role 可有獨立的 workspace (工作目錄, prompt, tool 設定, memory).

#### Config

```json
{
  "roles": [
    {
      "name": "黒曜",
      "workspace": "~/.tetora/workspaces/kokuyo",
      "soulFile": "~/.tetora/workspaces/kokuyo/SOUL.md",
      "tools": { "profile": "full" },
      "mcpServers": ["filesystem", "github"],
      "sandbox": { "mode": "off" }
    },
    {
      "name": "翡翠",
      "workspace": "~/.tetora/workspaces/hisui",
      "soulFile": "~/.tetora/workspaces/hisui/SOUL.md",
      "tools": { "profile": "standard", "allow": ["web_search", "web_fetch"] },
      "sandbox": { "mode": "non-main" }
    }
  ]
}
```

#### Session Scoping

| Session Type | Trust Level | Sandbox | Tool Profile |
|--------------|-------------|---------|--------------|
| `main` (Dashboard/CLI) | From role config | From role config | From role config |
| `dm` (TG/Slack/Discord DM) | ≤ role config | On if role specifies | ≤ role profile |
| `group` (TG/Slack/Discord group) | observe (default) | Always on | minimal (default) |

#### Workspace Directory Structure

```
~/.tetora/workspaces/kokuyo/
├── SOUL.md          # Agent personality/instructions
├── TOOLS.md         # Tool usage notes
├── memory/          # Agent-specific memory files
└── skills/          # Agent-specific skills
```

#### Tests (~12)

- Workspace resolution (default + per-role)
- Session type → trust level mapping
- Per-role tool profile resolution
- MCP server scoping per role

---

## Phase 11: Personal Assistant

> 目標: Tetora 差異化 — 從被動 orchestrator 到主動 personal assistant
> 預估: ~2,700 行

### P11.1: Proactive Agent (~900 lines)

**新檔案**: `proactive.go`, `proactive_test.go`
**修改**: `config.go`, `cron.go`, `http.go`, `dashboard.html`, `completion.go`

#### 參考: OpenClaw Heartbeat Pattern

OpenClaw 每 30min 喚醒 agent，讀 `HEARTBEAT.md` 決定是否行動。無事則回 `HEARTBEAT_OK` 靜默。
Tetora 採用更彈性的 rule-based 設計，支援多種 trigger type + cooldown + delivery routing。

#### Trigger Types

| Type | Description | Example |
|------|-------------|---------|
| `schedule` | Cron expression | "0 9 * * 1-5" (weekday 9am) |
| `event` | SSE event match | dispatch completed, circuit opened |
| `threshold` | Metric condition | daily cost > $5, queue depth > 10 |
| `heartbeat` | Periodic wake (OpenClaw-style) | Every 30min, agent decides action |

#### Config

```json
{
  "proactive": {
    "enabled": true,
    "rules": [
      {
        "name": "morning-briefing",
        "trigger": { "type": "schedule", "cron": "0 9 * * *", "tz": "Asia/Tokyo" },
        "action": {
          "type": "dispatch",
          "role": "琉璃",
          "prompt": "今日のブリーフィングを作成してください。天気、カレンダー、未読通知のサマリーを含めてください。"
        },
        "delivery": { "channel": "telegram" }
      },
      {
        "name": "cost-alert",
        "trigger": { "type": "threshold", "metric": "daily_cost_usd", "op": ">", "value": 5 },
        "action": { "type": "notify", "message": "Daily cost exceeded $5: {{.Value}}" },
        "delivery": { "channel": "telegram" },
        "cooldown": "1h"
      },
      {
        "name": "weekly-digest",
        "trigger": { "type": "schedule", "cron": "0 18 * * 5" },
        "action": {
          "type": "dispatch",
          "role": "琉璃",
          "promptTemplate": "weekly-digest",
          "params": { "days": 7 }
        },
        "delivery": { "channel": "telegram" }
      }
    ]
  }
}
```

#### Digest Template Variables

| Variable | Description |
|----------|-------------|
| `{{.TasksToday}}` | Today's dispatch count |
| `{{.CostToday}}` | Today's total cost |
| `{{.TopRole}}` | Most active role |
| `{{.FailedTasks}}` | Failed task count |
| `{{.SLAViolations}}` | SLA violation count |
| `{{.QueueDepth}}` | Offline queue depth |

#### Tests (~20)

- Trigger evaluation (schedule, event, threshold)
- Action execution (dispatch, notify)
- Delivery routing (telegram, slack, discord, dashboard)
- Cooldown enforcement
- Template variable resolution

---

### P11.2: Quick Actions (~500 lines)

**新檔案**: `quickaction.go`, `quickaction_test.go`, `cli_quick.go`
**修改**: `config.go`, `http.go`, `dashboard.html`, `telegram.go`, `completion.go`

#### Config

```json
{
  "quickActions": [
    {
      "name": "summarize-chat",
      "label": "Summarize Recent Chat",
      "icon": "💬",
      "role": "琉璃",
      "promptTemplate": "Summarize the last {{.Count}} messages in session {{.SessionID}}",
      "params": { "count": { "type": "number", "default": 20 } },
      "shortcut": "s"
    },
    {
      "name": "deploy-check",
      "label": "Check Deploy Status",
      "role": "黒曜",
      "prompt": "Check the deployment status of all services and report any issues.",
      "shortcut": "d"
    }
  ]
}
```

#### Dashboard: Command Palette (Ctrl+K / Cmd+K)

- Fuzzy search across quick actions + recent dispatches
- Parameter input modal for parameterized actions
- Keyboard navigation

#### Telegram: /quick command

- `/quick` → inline keyboard with configured actions
- `/quick <name>` → execute directly

#### Tests (~10)

---

### P11.3: Group Chat Intelligence (~500 lines)

**修改**: `telegram.go`, `slack.go`, `discord.go`, `config.go`, `session.go`

#### Config

```json
{
  "groupChat": {
    "activation": "mention",
    "contextWindow": 10,
    "rateLimit": { "maxPerMin": 5, "perGroup": true },
    "allowedGroups": {
      "telegram": ["-1001234567890"],
      "discord": ["guild:channel"],
      "slack": ["C12345"]
    },
    "threadReply": true
  }
}
```

#### Activation Modes

| Mode | Description |
|------|-------------|
| `mention` | Only respond when @mentioned |
| `keyword` | Respond to configured trigger keywords |
| `all` | Respond to all messages (rate limited) |

#### Tests (~12)

---

### P11.4: Built-in Web Tools (~400 lines)

**新檔案**: `tool_web.go`, `tool_web_test.go`
**修改**: `tool.go`, `config.go`

#### web_search

```json
{
  "tools": {
    "webSearch": {
      "provider": "brave",
      "apiKey": "$BRAVE_API_KEY",
      "maxResults": 5
    }
  }
}
```

支援: Brave Search API, Tavily, SearXNG (self-hosted), 或任何 JSON API.

#### web_fetch

- HTTP GET → strip HTML tags → return plain text
- Max content length (default 50KB)
- Timeout (default 10s)
- User-Agent spoofing for better compatibility

#### Tests (~10)

---

### P11.5: Context Compaction (~400 lines)

**新檔案**: `compaction.go`, `compaction_test.go`
**修改**: `session.go`, `dispatch.go`, `config.go`

#### 概念

當 session 對話過長時，自動壓縮舊的 messages 為摘要.

```json
{
  "session": {
    "compaction": {
      "enabled": true,
      "maxMessages": 50,
      "compactTo": 10,
      "model": "haiku",
      "maxCost": 0.02
    }
  }
}
```

#### Algorithm

1. Session messages > `maxMessages`
2. Take oldest `maxMessages - compactTo` messages
3. LLM call: "Summarize this conversation segment, preserving key facts, decisions, and action items"
4. Replace old messages with single `[COMPACTED]` system message containing summary
5. Promote important facts to agent memory (optional)

#### Tests (~8)

---

## Phase 12: Ecosystem Expansion

> 目標: 更多通道 + 進階能力 + Canvas + Companion Apps
> 預估: ~4,400 行

### P12.1: WhatsApp Channel (~800 lines)

**新檔案**: `whatsapp.go`, `whatsapp_test.go`
**修改**: `config.go`, `http.go`, `notify.go`, `completion.go`

#### 實作方式: WhatsApp Cloud API (Meta Business)

不用 Baileys (需要 Node.js), 改用 WhatsApp Cloud API:
- HTTP webhook for incoming messages
- HTTP API for outgoing messages
- 需要 Meta Business account + WhatsApp Business phone number
- Zero dependency (純 HTTP)

#### Features

- Text messages (in/out)
- Image/audio/document media
- Reply to specific messages
- Message templates (required for initiating conversations)
- Webhook verification (challenge-response)

---

### P12.2: Agent-to-Agent Communication (~400 lines)

**修改**: `handoff.go`, `session.go`, `tool.go`

#### 概念

Agents 可透過 tool calls 互相溝通:

| Tool | Description |
|------|-------------|
| `agent_list` | List available agents/roles with capabilities |
| `agent_dispatch` | Dispatch sub-task to another agent, wait for result |
| `agent_message` | Send async message to another agent's session |

#### vs Existing Handoff

現有 `[DELEGATE:role]` tag-based handoff 保留, 新增 tool-based 精細控制.

---

### P12.3: DM Pairing & Access Control (~400 lines)

**新檔案**: `pairing.go`, `pairing_test.go`
**修改**: `telegram.go`, `slack.go`, `discord.go`, `config.go`

#### 概念

Unknown users must be approved before they can interact with agents:

```json
{
  "accessControl": {
    "dmPairing": true,
    "pairingMessage": "Send this code to the admin to get access: {{.Code}}",
    "allowlists": {
      "telegram": ["user1", "user2"],
      "discord": ["userid1"],
      "slack": ["U12345"]
    }
  }
}
```

- New DM → generate 6-digit pairing code
- Admin approves via CLI: `tetora pairing approve <channel> <code>`
- Or Dashboard: pairing requests panel
- Approved users stored in DB

---

### P12.4: Voice Engine (~1,000 lines)

**新檔案**: `voice.go`, `voice_stt.go`, `voice_tts.go`, `voice_test.go`
**修改**: `telegram.go`, `discord.go`, `config.go`, `http.go`, `dashboard.html`, `tool.go`

#### 設計原則: Provider Abstraction

Voice 採用與 LLM provider 相同的抽象模式 — 統一介面，可換後端。

#### STT Provider Interface

```go
type STTProvider interface {
    Transcribe(ctx context.Context, audio io.Reader, opts STTOptions) (*STTResult, error)
    Name() string
}

type STTOptions struct {
    Language string // ISO 639-1, "" = auto-detect
    Format   string // "ogg", "wav", "mp3", "webm"
}

type STTResult struct {
    Text       string  `json:"text"`
    Language   string  `json:"language"`
    Duration   float64 `json:"durationSec"`
    Confidence float64 `json:"confidence,omitempty"`
}
```

#### STT Providers (2026 Best-in-Class)

| Provider | Model | Latency | Accuracy | Pricing |
|----------|-------|---------|----------|---------|
| **OpenAI** | `gpt-4o-mini-transcribe` | ~300ms | Best (recommended by OpenAI over Whisper) | $0.003/min |
| **Deepgram** | Nova-2 | ~100ms | Excellent, lowest latency | $0.0043/min |
| **OpenAI (legacy)** | Whisper v3 Turbo | ~500ms | Good, self-hostable | $0.006/min |
| **Custom** | Any OpenAI-compatible `/v1/audio/transcriptions` | Varies | Varies | Varies |

```json
{
  "voice": {
    "stt": {
      "provider": "openai",
      "model": "gpt-4o-mini-transcribe",
      "endpoint": "https://api.openai.com/v1/audio/transcriptions",
      "apiKey": "$OPENAI_API_KEY",
      "language": "",
      "maxDuration": 300
    }
  }
}
```

#### TTS Provider Interface

```go
type TTSProvider interface {
    Synthesize(ctx context.Context, text string, opts TTSOptions) (io.Reader, error)
    SynthesizeStream(ctx context.Context, text string, opts TTSOptions) (<-chan []byte, error) // streaming chunks
    Name() string
}

type TTSOptions struct {
    Voice  string // provider-specific voice ID
    Speed  float64
    Format string // "mp3", "opus", "wav"
}
```

#### TTS Providers (2026 Best-in-Class)

| Provider | Model | TTFB | Quality (ELO) | Pricing |
|----------|-------|------|---------------|---------|
| **Cartesia** | Sonic 3 | ~40ms | Top tier | Usage-based |
| **ElevenLabs** | Flash v2.5 | ~75ms | 1,200+ ELO, 32 langs | $5-330/mo |
| **OpenAI** | TTS-1.5 Max | ~150ms | 1,115 ELO, 50+ langs | $15/1M chars |
| **Deepgram** | Aura-2 | ~90ms | Good, 7 langs | $0.030/1K chars |
| **Custom** | Any OpenAI-compatible `/v1/audio/speech` | Varies | Varies | Varies |

```json
{
  "voice": {
    "tts": {
      "enabled": true,
      "provider": "elevenlabs",
      "model": "eleven_flash_v2_5",
      "endpoint": "https://api.elevenlabs.io/v1/text-to-speech",
      "apiKey": "$ELEVENLABS_API_KEY",
      "voice": "rachel",
      "format": "opus"
    }
  }
}
```

#### Realtime Speech-to-Speech (Phase 2)

OpenAI Realtime API: gpt-realtime model, WebSocket, direct audio-to-audio.

```json
{
  "voice": {
    "realtime": {
      "enabled": false,
      "provider": "openai-realtime",
      "model": "gpt-realtime",
      "apiKey": "$OPENAI_API_KEY"
    }
  }
}
```

Dashboard: WebRTC microphone → WebSocket to Tetora → relay to OpenAI Realtime API → stream audio back.

#### Integration Points

| Channel | STT (Input) | TTS (Output) | Realtime |
|---------|-------------|--------------|----------|
| **Telegram** | Voice messages → transcribe → dispatch | Response text → TTS → voice message reply | — |
| **Discord** | Voice channel audio (future) | Voice channel playback (future) | — |
| **Dashboard** | Microphone button → WebSocket audio stream | Audio player for TTS responses | WebRTC real-time conversation |
| **API** | `POST /api/voice/transcribe` | `POST /api/voice/synthesize` | `WS /api/voice/realtime` |

#### Built-in Tools

| Tool | Description |
|------|-------------|
| `voice_speak` | Agent-initiated TTS: synthesize text and send to channel as voice |
| `voice_listen` | Agent-initiated STT: transcribe audio from session context |

#### Tests (~20)

- STT provider abstraction (mock HTTP)
- TTS provider abstraction (mock HTTP, streaming)
- Telegram voice message flow (receive → transcribe → dispatch → TTS → reply)
- Dashboard audio WebSocket
- Provider fallback chain

---

### P12.5: Claude API Direct Provider (~400 lines)

**新檔案**: `provider_claude_api.go`, `provider_claude_api_test.go`
**修改**: `provider.go`, `config.go`

#### 概念

直接呼叫 Anthropic Messages API，不經 Claude CLI:
- 支援 tools parameter → agentic loop
- 支援 streaming (SSE)
- 支援 extended thinking
- 減少 Claude CLI binary 依賴風險

```json
{
  "providers": {
    "claude-api": {
      "type": "claude-api",
      "apiKey": "$ANTHROPIC_API_KEY",
      "model": "claude-sonnet-4-5-20250929",
      "maxTokens": 8192
    }
  }
}
```

---

### P12.6: Canvas / MCP Apps (~800 lines)

**新檔案**: `canvas.go`, `canvas_test.go`
**修改**: `tool.go`, `mcp_host.go`, `http.go`, `dashboard.html`, `config.go`

#### 背景: MCP Apps (SEP-1865)

MCP Apps 是 2026 年 1 月正式上線的 MCP extension，由 Anthropic + OpenAI 共同推動。
它允許 MCP server 透過 `ui://` URI 宣告 HTML UI resources，host 在 sandboxed iframe 中渲染。

Claude, ChatGPT, VS Code, Goose 均已支援。Tetora 作為 MCP host (P10.2) 自然可以成為 MCP Apps host。

#### 設計方案: 三層能力

| Layer | Description | Complexity |
|-------|-------------|------------|
| **L1: MCP Apps Host** | Render `ui://` resources from MCP servers in Dashboard | Medium |
| **L2: Built-in Canvas** | Agent 自行生成 HTML/SVG 顯示在 Dashboard | Low |
| **L3: Interactive Canvas** | 雙向通訊: UI → agent (user input), agent → UI (state update) | High |

#### L1: MCP Apps Host (核心)

Tetora 作為 MCP host，自動發現 MCP server 宣告的 `ui://` resources:

```
MCP Server 啟動
    → tools/list response
    → 發現 tool._meta["ui/resourceUri"] = "ui://charts/bar-chart"
    → resources/read("ui://charts/bar-chart") → HTML content
    → Register as canvas-capable tool
```

Dashboard 渲染:
```html
<!-- Sandboxed iframe for MCP App -->
<iframe id="canvas-frame"
  sandbox="allow-scripts"
  srcdoc="...html from ui:// resource..."
  style="width:100%;height:400px;border:1px solid var(--border);">
</iframe>
```

通訊 (postMessage + JSON-RPC):
```javascript
// Dashboard ↔ MCP App iframe
window.addEventListener('message', function(e) {
  if (e.data && e.data.jsonrpc === '2.0') {
    // Route JSON-RPC to Tetora backend via SSE/WS
    fetch('/api/canvas/message', {
      method: 'POST',
      body: JSON.stringify({
        sessionId: currentSession,
        message: e.data
      })
    });
  }
});

// Backend → iframe
function sendToCanvas(msg) {
  document.getElementById('canvas-frame').contentWindow.postMessage(msg, '*');
}
```

Server-side routing (`canvas.go`):
```go
type CanvasMessage struct {
    SessionID string          `json:"sessionId"`
    Message   json.RawMessage `json:"message"` // JSON-RPC 2.0
}

func handleCanvasMessage(w http.ResponseWriter, r *http.Request) {
    var msg CanvasMessage
    json.NewDecoder(r.Body).Decode(&msg)

    // Route to the MCP server that owns this canvas
    server := findMCPServerForSession(msg.SessionID)
    if server == nil {
        http.Error(w, "no active canvas", http.StatusNotFound)
        return
    }

    // Forward JSON-RPC to MCP server
    resp, err := server.Call(msg.Message)
    // ...
}
```

#### L2: Built-in Canvas (Agent 自生 UI)

Agent 可透過 tool call 產生 HTML 顯示在 Dashboard:

```go
// Built-in tool: canvas_render
type CanvasRenderInput struct {
    Title   string `json:"title"`
    Content string `json:"content"` // HTML string
    Width   string `json:"width,omitempty"`   // "100%", "600px"
    Height  string `json:"height,omitempty"`  // "400px"
}
```

| Tool | Description |
|------|-------------|
| `canvas_render` | Render HTML/SVG in Dashboard canvas panel |
| `canvas_update` | Update existing canvas content |
| `canvas_close` | Close canvas panel |

安全性: HTML 在 sandboxed iframe 中渲染 (`sandbox="allow-scripts"`)，無法存取 parent window、cookie、localStorage。

用途:
- 資料視覺化 (charts, tables)
- 互動表單 (收集使用者輸入)
- Markdown 渲染 (rich text responses)
- SVG 圖表 (workflow DAG, architecture diagrams)

#### L3: Interactive Canvas (雙向通訊)

Canvas 內的 UI 可以回傳使用者輸入給 agent:

```javascript
// In canvas iframe:
parent.postMessage({
  jsonrpc: '2.0',
  method: 'user_input',
  params: {
    type: 'form_submit',
    data: { name: 'John', age: 30 }
  }
}, '*');
```

Dashboard 收到後注入 session 作為 tool_result:
```go
func handleCanvasUserInput(sessionID string, input json.RawMessage) {
    // Inject as tool_result into the agentic loop
    msg := SessionMessage{
        Role:    "tool",
        Content: string(input),
    }
    injectSessionMessage(sessionID, msg)
    // Resume agentic loop
}
```

#### Config

```json
{
  "canvas": {
    "enabled": true,
    "maxIframeHeight": "600px",
    "allowScripts": true,
    "csp": "default-src 'self' 'unsafe-inline'; img-src * data:; font-src *"
  }
}
```

#### Dashboard UI

Chat 視窗中，當 tool 返回 canvas 內容時:
```
╔═══════════════════════════════════════════════╗
║ 📊 Sales Dashboard          [Maximize] [✕]  ║
╠═══════════════════════════════════════════════╣
║                                               ║
║   [Sandboxed iframe with MCP App / HTML]     ║
║                                               ║
╚═══════════════════════════════════════════════╝
```

- Canvas panel 出現在 chat message 中 (inline) 或 side panel (可 toggle)
- Maximize: 全螢幕 canvas
- Multiple canvases: tab 切換

#### Security

| Measure | Description |
|---------|-------------|
| `sandbox="allow-scripts"` | No access to parent DOM, cookies, localStorage |
| CSP header | Configurable Content-Security-Policy for iframe content |
| Pre-declared | MCP Apps: host reviews `ui://` resources before rendering |
| Audit | All canvas messages logged to audit_log |
| Size limit | HTML content max 1MB |
| Trust gate | Canvas tools respect trust level (observe/suggest/auto) |

#### Tests (~15)

- MCP Apps `ui://` resource discovery and registration
- Canvas render handler (HTML injection, size limits)
- Iframe sandbox attributes verification
- postMessage routing (Dashboard ↔ iframe ↔ backend)
- Canvas update and close lifecycle
- Built-in canvas_render tool execution
- Security: CSP enforcement, size limit rejection
- Interactive canvas: user input injection into session

---

### P12.7: Companion Apps (~600 lines)

**新檔案**: `companion/` (separate build target), `push.go`, `push_test.go`
**修改**: `pwa.go`, `dashboard.html`, `http.go`, `config.go`

#### 設計方案: Progressive Enhancement

Tetora 已有 PWA (P9.7)。Companion Apps 基於 PWA 漸進增強，三個層級:

| Tier | Platform | Technology | Description |
|------|----------|------------|-------------|
| **T1: Enhanced PWA** | All | Web Push API | Push notifications, background sync |
| **T2: Desktop App** | macOS/Win/Linux | Wails v3 (Go + WebView) | Menu bar, system tray, native notifications |
| **T3: Mobile Shell** | iOS/Android | Capacitor | Native container wrapping Dashboard PWA |

#### T1: Enhanced PWA + Web Push (~250 lines)

Dashboard PWA 已有 offline caching，加入 Web Push:

```json
{
  "push": {
    "enabled": true,
    "vapidPublicKey": "$VAPID_PUBLIC_KEY",
    "vapidPrivateKey": "$VAPID_PRIVATE_KEY",
    "vapidEmail": "admin@example.com"
  }
}
```

**Server-side** (`push.go`):

```go
// Web Push notification (RFC 8030 + VAPID)
type PushSubscription struct {
    Endpoint string `json:"endpoint"`
    Keys     struct {
        P256dh string `json:"p256dh"`
        Auth   string `json:"auth"`
    } `json:"keys"`
}

// Send push notification using VAPID (pure Go, no external deps)
func sendWebPush(sub PushSubscription, payload []byte, vapidKeys VAPIDKeys) error {
    // 1. Generate ECDH shared secret
    // 2. Encrypt payload (AES-128-GCM, RFC 8188)
    // 3. Create VAPID JWT (ES256)
    // 4. POST to subscription endpoint
}
```

Web Push 加密需要: `crypto/ecdh`, `crypto/aes`, `crypto/ecdsa` — 全在 Go stdlib。

**Dashboard 側** (Service Worker):

```javascript
// In sw.js — push event handler
self.addEventListener('push', function(e) {
  var data = e.data ? e.data.json() : { title: 'Tetora', body: 'New notification' };
  e.waitUntil(
    self.registration.showNotification(data.title, {
      body: data.body,
      icon: '/dashboard/icon.svg',
      badge: '/dashboard/icon.svg',
      tag: data.tag || 'tetora',
      data: { url: data.url || '/dashboard' }
    })
  );
});

// Click handler — open dashboard
self.addEventListener('notificationclick', function(e) {
  e.notification.close();
  e.waitUntil(
    clients.openWindow(e.notification.data.url)
  );
});
```

**訂閱 flow**:

```
Dashboard → navigator.serviceWorker.ready
    → registration.pushManager.subscribe({
         userVisibleOnly: true,
         applicationServerKey: vapidPublicKey
       })
    → POST /api/push/subscribe { subscription JSON }
    → Server stores subscription in DB
```

**HTTP API**:

- `POST /api/push/subscribe` — Register push subscription
- `DELETE /api/push/subscribe` — Unsubscribe
- `POST /api/push/test` — Send test notification

**Integration**: Notification Engine (notify_intel.go) gains a new `WebPushNotifier` channel.

#### T2: Desktop App via Wails v3 (~200 lines)

Wails v3 allows building native desktop apps with Go backend + WebView frontend.

**Architecture**:
```
┌─────────────────────────────┐
│  Wails v3 Desktop App       │
│  ┌───────────────────────┐  │
│  │ WebView               │  │
│  │  → loads /dashboard   │  │
│  │  (connects to Tetora) │  │
│  └───────────────────────┘  │
│  ┌───────────────────────┐  │
│  │ Go Backend             │  │
│  │  → system tray         │  │
│  │  → native notifications│  │
│  │  → global hotkey       │  │
│  │  → auto-start          │  │
│  └───────────────────────┘  │
└─────────────────────────────┘
```

**Features**:

| Feature | Description |
|---------|-------------|
| System tray | Status indicator (green/yellow/red), quick actions menu |
| Menu bar | macOS: icon in menu bar with dropdown |
| Native notifications | OS-level notifications (bypass browser permission) |
| Global hotkey | Cmd+Shift+T → open quick dispatch |
| Auto-start | Launch on login (optional) |
| Deep links | `tetora://dispatch?prompt=...` URI scheme |

**Separate build target** (`companion/desktop/`):
```go
// companion/desktop/main.go
package main

import (
    "github.com/wailsapp/wails/v3"
)

func main() {
    app := wails.New(wails.Options{
        Title: "Tetora",
        Width: 1200,
        Height: 800,
        URL:   "http://localhost:18790/dashboard", // connect to running Tetora daemon
        Tray: &wails.TrayOptions{
            Icon:    trayIcon,
            Tooltip: "Tetora - AI Agent Orchestrator",
            Menu:    buildTrayMenu(),
        },
    })
    app.Run()
}
```

**Note**: Wails v3 是唯一的外部依賴，但它在 **companion app 的獨立 build target** 裡，不影響 Tetora 主 binary 的零外部依賴原則。

#### T3: Mobile Shell via Capacitor (~150 lines)

Capacitor wraps the Dashboard PWA into native iOS/Android apps.

**Architecture**:
```
┌──────────────────────────────┐
│ Capacitor Native Shell       │
│  ┌────────────────────────┐  │
│  │ WKWebView / WebView    │  │
│  │  → loads /dashboard    │  │
│  │  (connects to Tetora)  │  │
│  └────────────────────────┘  │
│  Native plugins:             │
│  • Push notifications        │
│  • Biometric auth            │
│  • Share target              │
│  • Quick actions (3D Touch)  │
│  • Background fetch          │
└──────────────────────────────┘
```

**Separate project** (`companion/mobile/`):
```
companion/mobile/
├── capacitor.config.ts
├── ios/
├── android/
├── src/
│   └── index.html  (redirect to Tetora dashboard URL)
└── package.json
```

`capacitor.config.ts`:
```typescript
const config = {
  appId: 'com.tetora.app',
  appName: 'Tetora',
  webDir: 'dist',
  server: {
    url: 'http://your-tetora-host:18790/dashboard',
    cleartext: true
  },
  plugins: {
    PushNotifications: { presentationOptions: ['badge', 'sound', 'alert'] },
    LocalNotifications: {},
    BiometricAuth: {}
  }
};
```

**Note**: Capacitor project 是純 TypeScript/native 獨立專案，與 Go 無關。提供 template + 文件即可。

#### Delivery Strategy

| Tier | 何時做 | 在 Tetora repo 中 |
|------|--------|-------------------|
| T1 (Web Push) | P12.7 核心 | `push.go` + SW changes |
| T2 (Desktop) | P12.7 stretch | `companion/desktop/` 子目錄 |
| T3 (Mobile) | P12.7 stretch | `companion/mobile/` template |

T1 是必做 (所有 PWA user 受益)。T2/T3 是延伸，提供 project template + README。

#### Tests (~12)

- VAPID key generation and JWT signing
- Web Push encryption (ECDH + AES-128-GCM)
- Push subscription CRUD (DB storage)
- sendWebPush HTTP request format validation
- Push subscription endpoint (POST/DELETE)
- Integration: notification engine → web push channel
- Service worker push event handler (unit in JS)

---

## Phase 13: Advanced (Future)

> 優先級較低，視需求啟動

| Feature | Description | Est. Lines |
|---------|-------------|------------|
| P13.1 Browser Automation | CDP protocol via Docker Chrome | ~800 |
| P13.2 LINE Channel | LINE Messaging API (JP market) | ~600 |
| P13.3 Plugin System | Extension points: channel, tool, provider, memory | ~600 |
| P13.4 Image Analysis | Vision API integration (describe images) | ~400 |
| P13.5 Matrix Channel | Matrix client-server API | ~600 |
| P13.6 Teams Channel | Microsoft Bot Framework | ~700 |

---

## Priority & Dependencies

```
P10 (Tool Engine & Intelligence) — CRITICAL, 補漏 OpenClaw 差距
  P10.1 Tool Engine + Agentic Loop     ◄── foundation for P10.2-P10.6
  P10.2 MCP Host                        ◄── depends on P10.1 (tool registry)
  P10.3 Tool Policy + Trust Integration ◄── depends on P10.1 + existing trust.go
  P10.4 Semantic Memory                 ◄── independent (enhances existing)
  P10.5 Prometheus Metrics              ◄── independent
  P10.6 Agent Workspace Isolation       ◄── depends on P10.1 + P10.3

P11 (Personal Assistant) — Tetora 差異化
  P11.1 Proactive Agent                 ◄── independent (cron + notify + SSE)
  P11.2 Quick Actions                   ◄── independent (dashboard + config)
  P11.3 Group Chat Intelligence         ◄── independent (TG/Slack/Discord)
  P11.4 Built-in Web Tools              ◄── depends on P10.1 (tool registry)
  P11.5 Context Compaction              ◄── independent (session + LLM call)

P12 (Ecosystem) — 擴展
  P12.1 WhatsApp Channel               ◄── independent
  P12.2 Agent-to-Agent Communication    ◄── depends on P10.1
  P12.3 DM Pairing & Access Control     ◄── independent
  P12.4 Voice Engine                    ◄── independent (TG voice + API)
  P12.5 Claude API Direct Provider      ◄── enhances P10.1 (better agentic loop)
  P12.6 Canvas / MCP Apps              ◄── depends on P10.2 (MCP Host) + Dashboard
  P12.7 Companion Apps                  ◄── depends on P9.7 (PWA), independent otherwise
```

### 建議執行順序

```
Phase 10 (Sequential):
  P10.1 → P10.2 → P10.3 → P10.4 → P10.5 → P10.6

Phase 11 (After P10.1):
  P11.1 → P11.3 → P11.4 → P11.5 → P11.2

Phase 12 (Parallel with P11):
  P12.5 (Claude API) → P12.1 (WhatsApp) → P12.4 (Voice) → P12.2 → P12.3
  P12.6 (Canvas) — after P10.2 (MCP Host)
  P12.7 (Companion Apps) — independent, after PWA stable
```

---

## Estimated Scope

| Phase | Items | New Files | Modified Files | Est. Lines |
|-------|-------|-----------|----------------|------------|
| P10 | 6 | 10 | ~20 | ~4,600 |
| P11 | 5 | 7 | ~15 | ~2,700 |
| P12 | 7 | 11 | ~18 | ~4,400 |
| P13 | 6 | 6 | ~10 | ~3,700 |
| **Total** | **24** | **34** | **~63** | **~15,400** |

---

## Design Philosophy

「The agent orchestrator you actually use every day.」

Tetora 不是另一個 LangGraph、CrewAI 或 Dify。它是一個**個人 AI daemon**。

- **OpenClaw parity**: Tool system, MCP host, semantic memory, canvas, voice, companion apps — table stakes
- **Tetora unique**: Cost governance, trust gradient, workflow engine, SLA, reflection — moat
- **Personal assistant**: Proactive agent, quick actions, group chat — daily value
- **Canvas & MCP Apps**: Dashboard 成為 MCP Apps host, agent 可生成互動 UI
- **Companion Apps**: Enhanced PWA (Web Push) → Desktop (Wails) → Mobile (Capacitor)
- **Zero dependency**: Tetora 主 binary 零外部 Go 依賴不變; Companion Apps 獨立 build target
