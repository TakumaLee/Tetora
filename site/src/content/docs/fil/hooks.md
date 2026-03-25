---
title: "Claude Code Hooks Integration"
lang: "fil"
order: 3
description: "Integrate with Claude Code Hooks for real-time session observation."
---
# Claude Code Hooks Integration

## Pangkalahatang-ideya

Ang Claude Code Hooks ay isang event system na built-in sa Claude Code na nagpapaputok ng mga shell command sa mga mahahalagang sandali sa isang session. Nire-rehistro ng Tetora ang sarili nito bilang hook receiver upang maobserbahan ang bawat tumatakbong agent session sa real time — nang walang polling, walang tmux, at nang hindi nagdi-inject ng mga wrapper script.

**Ano ang pinapagana ng mga hook:**

- Real-time na pagsubaybay ng progreso sa dashboard (mga tool call, estado ng session, live na listahan ng worker)
- Pagmamatyag ng gastos at token sa pamamagitan ng statusline bridge
- Pag-audit ng paggamit ng tool (kung aling mga tool ang tumakbo, sa aling session, sa aling directory)
- Pagtuklas ng pagkumpleto ng session at awtomatikong pag-update ng status ng task
- Plan mode gate: hinahawakan ang `ExitPlanMode` hanggang maprubahan ng tao ang plano sa dashboard
- Interactive question routing: ang `AskUserQuestion` ay nire-redirect sa MCP bridge upang ang mga tanong ay lumabas sa iyong chat platform sa halip na i-block ang terminal

Ang mga hook ay ang inirerekomendang integration path mula sa Tetora v2.0. Ang lumang tmux-based na diskarte (v1.x) ay gumagana pa rin ngunit hindi sumusuporta ng mga hooks-only na feature tulad ng plan gate at question routing.

---

## Arkitektura

```
Claude Code session
  │
  ├── PreToolUse  ──────────────────► Tetora /api/hooks/event
  │   (ExitPlanMode)                  └─► Plan gate: long-poll hanggang maprubahan
  │   (AskUserQuestion)               └─► Deny: redirect sa MCP bridge
  │
  ├── PostToolUse ──────────────────► Tetora /api/hooks/event
  │                                   └─► I-update ang estado ng worker
  │                                   └─► Tuklasin ang mga pagsusulat sa plan file
  │
  ├── Stop        ──────────────────► Tetora /api/hooks/event
  │                                   └─► Markahan ang worker bilang done
  │                                   └─► I-trigger ang pagkumpleto ng task
  │
  └── Notification ─────────────────► Tetora /api/hooks/event
                                      └─► I-forward sa Discord/Telegram
```

Ang hook command ay isang maliit na curl call na ini-inject sa `~/.claude/settings.json` ng Claude Code. Ang bawat event ay pino-post sa `POST /api/hooks/event` sa tumatakbong Tetora daemon.

---

## Setup

### I-install ang mga hook

Habang tumatakbo ang Tetora daemon:

```bash
tetora hooks install
```

Nagsusulat ito ng mga entry sa `~/.claude/settings.json` at nagge-generate ng MCP bridge config sa `~/.tetora/mcp/bridge.json`.

Halimbawa ng isinusulat sa `~/.claude/settings.json`:

```json
{
  "hooks": {
    "PostToolUse": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "curl -s -X POST http://localhost:8991/api/hooks/event -H 'Content-Type: application/json' -d @-"
          }
        ]
      }
    ],
    "Stop": [ { "hooks": [ { "type": "command", "command": "..." } ] } ],
    "Notification": [ { "hooks": [ { "type": "command", "command": "..." } ] } ],
    "PreToolUse": [
      {
        "matcher": "ExitPlanMode",
        "hooks": [ { "type": "command", "command": "...", "timeout": 600 } ]
      },
      {
        "matcher": "AskUserQuestion",
        "hooks": [ { "type": "command", "command": "..." } ]
      }
    ]
  }
}
```

### Suriin ang status

```bash
tetora hooks status
```

Ipinapakita ng output kung aling mga hook ang naka-install, ilang Tetora rule ang nakarehistrong, at ang kabuuang bilang ng event na natanggap mula nang magsimula ang daemon.

Maaari mo ring suriin mula sa dashboard: ang **Engineering Details → Hooks** ay nagpapakita ng parehong status kasama ang isang live na event feed.

### Alisin ang mga hook

```bash
tetora hooks remove
```

Inaalis ang lahat ng Tetora entry mula sa `~/.claude/settings.json`. Ang mga umiiral na hook na hindi galing sa Tetora ay pinapanatili.

---

## Mga Hook Event

### PostToolUse

Nagpapaputok pagkatapos makumpleto ang bawat tool call. Ginagamit ito ng Tetora para sa:

- Pagsubaybay kung aling mga tool ang ginagamit ng isang agent (`Bash`, `Write`, `Edit`, `Read`, atbp.)
- Pag-update ng `lastTool` at `toolCount` ng worker sa live na listahan ng worker
- Pagtuklas kapag nagsulat ang isang agent sa isang plan file (nagti-trigger ng pag-update ng plan cache)

### Stop

Nagpapaputok kapag natapos ang isang Claude Code session (natural na pagkumpleto o pagkansela). Ginagamit ito ng Tetora para sa:

- Pagmamarka ng worker bilang `done` sa live na listahan ng worker
- Pag-publish ng completion SSE event sa dashboard
- Pag-trigger ng mga downstream na pag-update ng task status para sa mga taskboard task

### Notification

Nagpapaputok kapag nagpadala ng notification ang Claude Code (hal. kailangan ng pahintulot, matagal na pause). Ipinapadala ito ng Tetora sa Discord/Telegram at ini-publish sa dashboard SSE stream.

### PreToolUse: ExitPlanMode (plan gate)

Kapang malapit nang lumabas ang isang agent sa plan mode, iniintercept ito ng Tetora gamit ang long-poll (timeout: 600 segundo). Kine-cache ang nilalaman ng plano at inilalabas sa dashboard sa ilalim ng detail view ng session.

Maaaring aprubahan o tanggihan ng isang tao ang plano mula sa dashboard. Kung aprubahan, babalik ang hook at magpapatuloy ang Claude Code. Kung tanggihan (o kapag nag-expire ang timeout), naha-hold ang paglabas at nananatili sa plan mode ang Claude Code.

### PreToolUse: AskUserQuestion (question routing)

Kapang sinubukan ng Claude Code na magtanong sa user nang interactive, iniintercept ito ng Tetora at tina-tanggihan ang default na gawi. Ang tanong ay iruruta sa halip sa pamamagitan ng MCP bridge, na lalabas sa iyong na-configure na chat platform (Discord, Telegram, atbp.) upang masagot mo ito nang hindi nakaupo sa terminal.

---

## Real-Time na Pagsubaybay ng Progreso

Sa sandaling naka-install ang mga hook, ipinapakita ng **Workers** panel ng dashboard ang mga live na session:

| Field | Pinagmulan |
|---|---|
| Session ID | `session_id` sa hook event |
| Estado | `working` / `idle` / `done` |
| Huling tool | Pinakabagong pangalan ng tool mula sa `PostToolUse` |
| Working directory | `cwd` mula sa hook event |
| Bilang ng tool | Cumulative na bilang ng `PostToolUse` |
| Gastos / token | Statusline bridge (`POST /api/hooks/usage`) |
| Pinagmulan | Naka-link na task o cron job kung ini-dispatch ng Tetora |

Ang data ng gastos at token ay nagmumula sa Claude Code statusline script, na nagpo-post sa `/api/hooks/usage` sa nako-configure na interval. Ang statusline script ay hiwalay sa mga hook — binabasa nito ang Claude Code status bar output at ipinapadala ito sa Tetora.

---

## Pagmamatyag ng Gastos

Tinatanggap ng usage endpoint (`POST /api/hooks/usage`) ang:

```json
{
  "sessionId": "abc123",
  "costUsd": 0.0042,
  "inputTokens": 8200,
  "outputTokens": 340,
  "contextPct": 12,
  "model": "claude-sonnet-4-5"
}
```

Ang data na ito ay makikita sa Workers panel ng dashboard at pinagsama-sama sa mga araw-araw na cost chart. Ang mga budget alert ay nagpapaputok kapang lumampas ang gastos ng isang session sa na-configure na per-role o global na budget.

---

## Pag-troubleshoot

### Ang mga hook ay hindi nagpapaputok

**Suriin kung tumatakbo ang daemon:**
```bash
tetora status
```

**Suriin kung naka-install ang mga hook:**
```bash
tetora hooks status
```

**Direktang suriin ang settings.json:**
```bash
cat ~/.claude/settings.json | grep -A5 "hooks"
```

Kung nawawala ang hooks key, muling patakbuhin ang `tetora hooks install`.

**Suriin kung makatanggap ng hook event ang daemon:**
```bash
curl -s -X POST http://localhost:8991/api/hooks/event \
  -H "Content-Type: application/json" \
  -d '{"hook_event_name":"Stop","session_id":"test-123"}'
# Inaasahan: {"ok":true}
```

Kung hindi nakikinig ang daemon sa inaasahang port, suriin ang `listenAddr` sa `config.json`.

### Mga error sa pahintulot sa settings.json

Ang `settings.json` ng Claude Code ay nasa `~/.claude/settings.json`. Kung ang file ay pagmamay-ari ng ibang user o may mahigpit na mga pahintulot:

```bash
ls -la ~/.claude/settings.json
chmod 644 ~/.claude/settings.json
```

### Walang laman ang Workers panel ng dashboard

1. Kumpirmahin na naka-install ang mga hook at tumatakbo ang daemon.
2. Manu-manong magsimula ng Claude Code session at magpatakbo ng isang tool (hal. `ls`).
3. Suriin ang Workers panel ng dashboard — ang session ay dapat lumabas sa loob ng ilang segundo.
4. Kung hindi, suriin ang mga log ng daemon: `tetora logs -f | grep hooks`

### Hindi lumalabas ang plan gate

Ang plan gate ay ina-activate lamang kapang sinubukan ng Claude Code na tawagan ang `ExitPlanMode`. Nangyayari ito lamang sa mga plan mode session (sinimulan gamit ang `--plan` o itinakda sa pamamagitan ng `permissionMode: "plan"` sa role config). Ang mga interactive na `acceptEdits` session ay hindi gumagamit ng plan mode.

### Ang mga tanong ay hindi niruruta sa chat

Ang `AskUserQuestion` deny hook ay nangangailangan ng MCP bridge na maka-configure. Muling patakbuhin ang `tetora hooks install` — nagre-regenerate ito ng bridge config. Pagkatapos idagdag ang bridge sa iyong mga MCP setting ng Claude Code:

```bash
cat ~/.tetora/mcp/bridge.json
```

Idagdag ang file na iyon bilang MCP server sa `~/.claude/settings.json` sa ilalim ng `mcpServers`.

---

## Migration mula sa tmux (v1.x)

Sa Tetora v1.x, ang mga agent ay tumatakbo sa loob ng mga tmux pane at sinusubaybayan ng Tetora ang mga ito sa pamamagitan ng pagbabasa ng pane output. Sa v2.0, ang mga agent ay tumatakbo bilang mga bare Claude Code process at sinusubaybayan ng Tetora ang mga ito sa pamamagitan ng mga hook.

**Kung nag-upgrade ka mula sa v1.x:**

1. Patakbuhin ang `tetora hooks install` nang isang beses pagkatapos mag-upgrade.
2. Alisin ang anumang tmux session management configuration mula sa `config.json` (ang mga `tmux.*` key ay hindi na ginagamit).
3. Ang kasaysayan ng umiiral na session ay naka-preserve sa `history.db` — walang kailangang migration.
4. Ang `tetora session list` command at ang Sessions tab sa dashboard ay patuloy na gumagana gaya ng dati.

Ang tmux terminal bridge (`discord_terminal.go`) ay available pa rin para sa interactive na terminal access sa pamamagitan ng Discord. Ito ay hiwalay sa pagpapatakbo ng agent — pinapayagan ka nitong magpadala ng keystroke sa isang tumatakbong terminal session. Ang mga hook at ang terminal bridge ay nagpupunan sa isa't isa, hindi mutual na eksklusibo.
