# Tetora Roadmap

This is a high-level overview of shipped and planned features. Priorities may shift based on community feedback.

```mermaid
timeline
    title Tetora Roadmap
    section v1.0
        Core
            : Multi-agent roles
            : Multi-provider support
            : 10+ chat platforms
            : Cron jobs & Knowledge base
            : MCP & Skills
    section v1.4
        Agents & Life OS
            : Agent delegation & workflows
            : Life OS (7 modules)
            : Approval gates
            : Security hardening
    section v1.5
        Intelligence & Dashboard
            : Claude Code provider
            : Token optimization
            : Dashboard (Projects, Live Task)
            : Discord & Telegram live progress
    section v1.6
        TaskBoard & Operations
            : Auto-dispatch pipeline
            : Backlog triage (LLM)
            : Slot Pressure Guard
            : Memory Write Discipline
    section v1.7–1.8
        Workers & Terminal
            : Claude Code CLI workers (tmux)
            : Discord terminal bridge
            : Heartbeat & stall detection
            : Workflow × dispatch hardening
    section v2.0
        Dashboard CEO & Office
            : 4-zone dashboard layout
            : Interactive terminal viewer
            : Pixel office customization
            : Memory leak fixes
    section Next
        Distribution & Platform
            : Homebrew tap
            : Docker image
            : Windows Service
            : macOS code signing
    section Planned
        Collaboration
            : Multi-user ACL
            : Shared knowledge bases
            : Team dashboards
            : Audit logging
    section Future
        Ideas
            : Mobile app
            : Voice mode
            : Local models
            : E2E encryption
```

> **Legend:** v1.0–v2.0 = shipped &nbsp;|&nbsp; Next / Planned / Future = not yet shipped &nbsp;|&nbsp; ✅ in text sections = completed items
>
> *Can't see the diagram? View the [PNG version](assets/roadmap.png) or [PDF version](assets/roadmap.pdf).*

---

## v1.0 — Core ✅

- [x] Multi-agent roles with soul files
- [x] Multi-provider support (Claude API, OpenAI, Gemini, OpenAI-compatible)
- [x] 10+ chat platforms (Telegram, Discord, Slack, LINE, Teams, Signal, WhatsApp, iMessage, Google Chat, Matrix)
- [x] Cron jobs with approval gates and notifications
- [x] Knowledge base for grounded responses
- [x] MCP support (Model Context Protocol servers as tool providers)
- [x] Skills and workflow pipelines
- [x] Webhooks, cost governance, data retention

## v1.4 — Advanced Agents & Life OS ✅

### Agents & Orchestration

- [x] Agent-to-agent delegation
- [x] Workflow orchestration (DAG engine, condition, parallel, handoff, retry)
- [x] Agentic loop with tool execution, token/cost accumulation
- [x] Approval gates — Telegram (inline keyboard), Discord (buttons), Slack (text-based)
- [ ] Custom tool development SDK
- [ ] Plugin marketplace

### Life OS

- [x] Contacts & social graph (cross-channel, birthday, inactivity tracking)
- [x] Habits & wellness tracking (streaks, health webhook, reports)
- [x] Goals & milestones (decomposition, weekly review)
- [x] Smart scheduling (free slot finding, overcommitment detection)
- [x] Life insights engine (behavioral analysis, anomaly detection, spending forecast)
- [x] Morning briefing & evening wrap
- [x] Quick capture (keyword classifier: expense/reminder/contact/task/idea/note)
- [x] Time tracking with report aggregation
- [x] Lifecycle engine (cross-module automation)

### Security & Hardening

- [x] Crash protection & panic recovery (HTTP middleware, tool execution)
- [x] Injection defense (fail-closed mode)
- [x] SQLite selective encryption (AES-256-GCM)
- [x] Skill install + Sentori security scanner (24 regex rules, 5 risk categories)
- [x] Tool filtering per role, output truncation, execution timeout
- [x] Budget & context mid-loop checks
- [x] Audio normalization tool (ffmpeg loudnorm)
- [x] Lightweight onboarding wizard (3-step CLI flow)
- [x] Shell completion (bash, zsh, fish)
- [x] Config validation (`tetora config validate`)

## v1.5 — Intelligence & Dashboard ✅

### Provider & Dispatch

- [x] Claude Code provider + token optimization + tiered prompts
- [x] Smart timeout estimation + dispatch subtasks
- [x] Budget soft-limit + provider fallback chain
- [x] Zombie session cleanup

### Dashboard

- [x] System Log session + Live Task view
- [x] Workspace tab with Projects management
- [x] Folder browser + batch project import

### Platform

- [x] Discord: thread-per-task notifications + live progress streaming
- [x] Telegram: live progress streaming
- [x] `tetora restart` command

### Codebase

- [x] role → agent terminology rename across codebase
- [x] CI pipeline (.github/workflows/ci.yml) + green build
- [x] Multi-language install guide (10 languages)

## v1.6 — TaskBoard & Operations ✅

### TaskBoard

- [x] Auto-dispatch pipeline (todo → doing → done/failed)
- [x] Backlog triage — LLM-powered analysis (ready/decompose/clarify)
- [x] Dashboard TaskBoard UI
- [x] TaskBoard config toggle

### Agent System

- [x] Agent World pixel sprites
- [x] Memory Write Discipline (HaluMem guard: fabrication, inaccuracy, contradiction, omission)
- [x] Tiered SKILL.md injection + metadata auto-generation
- [x] Slot Pressure Guard (protect interactive sessions from batch saturation)

### Operations

- [x] Safe upgrade (skip auto-restart when jobs running)
- [x] roles→agents config migration + backward compatibility
- [x] Sub-agent deadlock fix (separate child semaphore)
- [x] Discord session reuse fix + live streaming progress

## v1.7 — Workflow & Dispatch Hardening ✅

### CLI Workers

- [x] Claude Code CLI provider via tmux (`claude-tmux`)
- [x] CLI provider abstraction (claude-tmux + codex-tmux profiles)
- [x] Discord terminal bridge — monitor and interact with workers
- [x] tmux supervisor for worker registration and tracking
- [x] Heartbeat monitor + stall detection + auto-cancel
- [x] Orphaned tmux worker recovery on daemon restart
- [x] Persistent tmux sessions (`keepSessions` option)
- [x] Resolve tmux/brew paths for launchd minimal PATH

### Dispatch & Workflows

- [x] Workflow × dispatch session lifecycle hardening
- [x] Budget soft-limit only (no hard block)
- [x] Sub-agent deadlock fix (separate child semaphore)
- [x] Dispatch always routes to review
- [x] Safe upgrade: skip auto-restart when jobs running

## v1.8 — Dashboard Terminal & Workers ✅

### Dashboard

- [x] Workers tab with live worker grid
- [x] Terminal viewer — interactive worker control (keystrokes, text input)
- [x] Discord Gateway live interactions viewer
- [x] Dashboard kanban enhancements + board_updated SSE
- [x] Terminal mode toggle per agent

### Operations

- [x] Cron hot-reload + skill injection limits
- [x] Triage fast-path + HTTP bind race fix
- [x] SQL retry + fallback on final status update
- [x] Auto-send Enter for stuck tmux prompts

## v2.0 — Dashboard CEO Command Center ✅

### Dashboard Layout Redesign

- [x] 4-zone layout: Command Center → Operations → Insights → Engineering Details
- [x] Executive summary with ROI cards (tasks done, hours saved, cost, ROI)
- [x] Ops bar — compact 4-card grid (jobs, running, daily burn, agent status)
- [x] Agent scorecard + activity feed side-by-side
- [x] Engineering details collapsible zone with localStorage persistence
- [x] Boardroom theme shows pixel office (light background instead of hidden)

### Pixel Office

- [x] Agent World with 8-bit pixel sprites
- [x] Office background + sprite images
- [x] Mini-map navigation
- [x] Decoration editor with drag-and-drop palette
- [x] Zoom controls + customizable layout

### Performance

- [x] Memory leak fixes: live stream DOM cap, session stream text cap, chat message pruning
- [x] Pause sprite engine + terminal poll on browser tab hide
- [x] Resource optimization across all dashboard components

## Next — Distribution & Platform

- [ ] Homebrew tap (`brew install TakumaLee/tap/tetora`)
- [ ] Docker image (`docker run -v ~/.tetora:/data tetora`)
- [ ] Windows Service management
- [ ] Linux .rpm package
- [ ] macOS code signing and notarization
- [ ] ARM32 Linux support (Raspberry Pi)

## Planned — Collaboration

- [ ] Multi-user support with access control
- [ ] Shared knowledge bases
- [ ] Team dashboards
- [ ] Audit logging

## Future Ideas

- Mobile companion app (iOS/Android)
- Voice interaction mode
- Local model support (Ollama, llama.cpp)
- End-to-end encryption for conversations

---

Have a feature request? [Open an issue](https://github.com/TakumaLee/Tetora/issues).
