---
title: "Configuration Reference"
lang: "fil"
order: 1
description: "Configure Tetora via ~/.tetora/config.json — models, providers, and runtime settings."
---
# Configuration Reference

## Pangkalahatang-ideya

Ang Tetora ay kino-configure gamit ang isang JSON file na nasa `~/.tetora/config.json`.

**Mga pangunahing gawi:**

- **`$ENV_VAR` substitution** — anumang string value na nagsisimula sa `$` ay papalitan ng katumbas na environment variable sa startup. Gamitin ito para sa mga sikreto (API keys, tokens) sa halip na i-hardcode ang mga ito.
- **Hot-reload** — ang pagpapadala ng `SIGHUP` sa daemon ay magre-reload ng config. Ang isang masamang config ay ire-reject at mananatiling gumagamit ang daemon ng naunang config; hindi ito mag-crash.
- **Relative paths** — ang `jobsFile`, `historyDB`, `defaultWorkdir`, at mga directory field ay reresolbahin kaugnay ng directory ng config file (`~/.tetora/`).
- **Backward compatibility** — ang lumang `"roles"` key ay alias para sa `"agents"`. Ang lumang `"defaultRole"` key sa loob ng `smartDispatch` ay alias para sa `"defaultAgent"`.

---

## Mga Top-Level Field

### Mga Core Setting

| Field | Uri | Default | Paglalarawan |
|---|---|---|---|
| `listenAddr` | string | `"127.0.0.1:8991"` | HTTP listen address para sa API at dashboard. Format: `host:port`. |
| `apiToken` | string | `""` | Bearer token na kailangan para sa lahat ng API request. Ang walang laman ay nangangahulugang walang authentication (hindi inirerekomenda para sa production). Sumusuporta ng `$ENV_VAR`. |
| `maxConcurrent` | int | `8` | Maximum na bilang ng mga concurrent agent task. Ang mga value na higit sa 20 ay nagbibigay ng babala sa startup. |
| `defaultModel` | string | `"sonnet"` | Default na pangalan ng Claude model. Ipinasa sa provider maliban kung naka-override bawat-agent. |
| `defaultTimeout` | string | `"1h"` | Default na timeout ng task. Go duration format: `"15m"`, `"1h"`, `"30s"`. |
| `defaultBudget` | float64 | `0` | Default na gastos na budget bawat task sa USD. Ang `0` ay walang limitasyon. |
| `defaultPermissionMode` | string | `"acceptEdits"` | Default na Claude permission mode. Mga karaniwang value: `"acceptEdits"`, `"default"`. |
| `defaultAgent` | string | `""` | System-wide fallback agent name kapag walang routing rule na nag-match. |
| `defaultWorkdir` | string | `""` | Default na working directory para sa mga agent task. Dapat mayroon sa disk. |
| `claudePath` | string | `"claude"` | Path sa `claude` CLI binary. Default ay naghahanap ng `claude` sa `$PATH`. |
| `defaultProvider` | string | `"claude"` | Pangalan ng provider na gagamitin kapag walang agent-level override. |
| `log` | bool | `false` | Legacy flag para paganahin ang file logging. Mas gustong gamitin ang `logging.level`. |
| `maxPromptLen` | int | `102400` | Maximum na haba ng prompt sa bytes (100 KB). Ang mga request na lumagpas dito ay ire-reject. |
| `configVersion` | int | `0` | Bersyon ng config schema. Ginagamit para sa auto-migration. Huwag itakda nang manu-mano. |
| `encryptionKey` | string | `""` | AES key para sa field-level encryption ng sensitibong data. Sumusuporta ng `$ENV_VAR`. |
| `streamToChannels` | bool | `false` | I-stream ang live task status sa mga konektadong messaging channel (Discord, Telegram, atbp.). |
| `cronNotify` | bool\|null | `null` (true) | Ang `false` ay nagpi-pigil ng lahat ng cron job completion notification. Ang `null` o `true` ay nagpapagana ng mga ito. |
| `cronReplayHours` | int | `2` | Ilang oras ang titingnan para sa mga napalampas na cron job sa daemon startup. |
| `diskBudgetGB` | float64 | `1.0` | Minimum na libreng espasyo sa disk sa GB. Ang mga cron job ay tatanggihan kapag mas mababa dito. |
| `diskWarnMB` | int | `500` | Threshold ng babala sa libreng disk sa MB. Nagla-log ng WARN ngunit nagpapatuloy ang mga job. |
| `diskBlockMB` | int | `200` | Threshold ng pag-block sa libreng disk sa MB. Ang mga job ay lalaktawan na may `skipped_disk_full` na status. |

### Mga Directory Override

Sa default, lahat ng directory ay nasa ilalim ng `~/.tetora/`. I-override lamang kung kailangan mo ng hindi karaniwang layout.

| Field | Uri | Default | Paglalarawan |
|---|---|---|---|
| `knowledgeDir` | string | `~/.tetora/knowledge/` | Directory para sa mga workspace knowledge file. |
| `agentsDir` | string | `~/.tetora/agents/` | Directory na naglalaman ng bawat-agent na SOUL.md file. |
| `workspaceDir` | string | `~/.tetora/workspace/` | Directory para sa mga rules, memory, skills, drafts, atbp. |
| `runtimeDir` | string | `~/.tetora/runtime/` | Directory para sa mga session, output, log, cache. |
| `vaultDir` | string | `~/.tetora/vault/` | Directory para sa encrypted secrets vault. |
| `historyDB` | string | `history.db` | Path ng SQLite database para sa job history. Kaugnay ng config dir. |
| `jobsFile` | string | `jobs.json` | Path sa cron jobs definition file. Kaugnay ng config dir. |

### Mga Global Allowed Directory

```json
{
  "allowedDirs": ["/Users/me/projects", "/tmp"],
  "defaultAddDirs": ["/Users/me/shared-context"]
}
```

| Field | Uri | Paglalarawan |
|---|---|---|
| `allowedDirs` | string[] | Mga directory na pinapayagan ang agent na basahin at isulat. Inilalapat nang global; maaaring paliitin bawat-agent. |
| `defaultAddDirs` | string[] | Mga directory na ini-inject bilang `--add-dir` para sa bawat task (read-only context). |
| `allowedIPs` | string[] | Mga IP address o CIDR range na pinapayagan na tawagan ang API. Walang laman = payagan lahat. Halimbawa: `["192.168.1.0/24", "10.0.0.1"]`. |

---

## Mga Provider

Tinutukoy ng mga provider kung paano pinapatakbo ng Tetora ang mga agent task. Maaaring i-configure ang maraming provider at piliin bawat-agent.

```json
{
  "defaultProvider": "claude",
  "providers": {
    "claude": {
      "type": "claude-cli",
      "path": "/usr/local/bin/claude"
    },
    "openai": {
      "type": "openai-compatible",
      "baseUrl": "https://api.openai.com/v1",
      "apiKey": "$OPENAI_API_KEY",
      "model": "gpt-4o"
    },
    "claude-api": {
      "type": "claude-api",
      "apiKey": "$ANTHROPIC_API_KEY",
      "model": "claude-sonnet-4-5",
      "maxTokens": 8192,
      "firstTokenTimeout": "60s"
    }
  }
}
```

### `providers` — `ProviderConfig`

| Field | Uri | Default | Paglalarawan |
|---|---|---|---|
| `type` | string | kailangan | Uri ng provider. Isa sa: `"claude-cli"`, `"openai-compatible"`, `"claude-api"`, `"claude-code"`. |
| `path` | string | `""` | Path ng binary. Ginagamit ng `claude-cli` at `claude-code` na uri. Babalik sa `claudePath` kung walang laman. |
| `baseUrl` | string | `""` | Base URL ng API. Kailangan para sa `openai-compatible`. |
| `apiKey` | string | `""` | API key. Sumusuporta ng `$ENV_VAR`. Kailangan para sa `claude-api`; opsyonal para sa `openai-compatible`. |
| `model` | string | `""` | Default na model para sa provider na ito. Ino-override ang `defaultModel` para sa mga task na gumagamit ng provider na ito. |
| `maxTokens` | int | `8192` | Maximum na output token (ginagamit ng `claude-api`). |
| `firstTokenTimeout` | string | `"60s"` | Gaano katagal hihintayin ang unang response token bago mag-timeout (SSE stream). |

**Mga uri ng provider:**
- `claude-cli` — nagpapatakbo ng `claude` binary bilang subprocess (default, pinaka-compatible)
- `claude-api` — direktang tumatawag sa Anthropic API gamit ang HTTP (nangangailangan ng `ANTHROPIC_API_KEY`)
- `openai-compatible` — anumang OpenAI-compatible na REST API (OpenAI, Ollama, Groq, atbp.)
- `claude-code` — gumagamit ng Claude Code CLI mode

---

## Mga Agent

Tinutukoy ng mga agent ang mga pinangalanang persona na may sariling model, soul file, at tool access.

```json
{
  "agents": {
    "coordinator": {
      "soulFile": "SOUL.md",
      "model": "sonnet",
      "description": "Handles planning, research, and coordination.",
      "keywords": ["plan", "research", "coordinate"]
    },
    "engineer": {
      "soulFile": "team/engineer/SOUL.md",
      "model": "sonnet",
      "provider": "claude",
      "description": "Handles coding, debugging, and infrastructure.",
      "keywords": ["code", "debug", "deploy"],
      "permissionMode": "acceptEdits",
      "allowedDirs": ["/Users/me/projects"],
      "trustLevel": "auto"
    }
  }
}
```

### `agents` — `AgentConfig`

| Field | Uri | Default | Paglalarawan |
|---|---|---|---|
| `soulFile` | string | kailangan | Path sa SOUL.md personality file ng agent, kaugnay ng `agentsDir`. |
| `model` | string | `defaultModel` | Model na gagamitin para sa agent na ito. |
| `description` | string | `""` | Paglalarawang nababasa ng tao. Ginagamit din ng LLM classifier para sa routing. |
| `keywords` | string[] | `[]` | Mga keyword na nag-trigger ng routing sa agent na ito sa smart dispatch. |
| `provider` | string | `defaultProvider` | Pangalan ng provider (key sa `providers` map). |
| `permissionMode` | string | `defaultPermissionMode` | Claude permission mode para sa agent na ito. |
| `allowedDirs` | string[] | `allowedDirs` | Mga filesystem path na maaaring ma-access ng agent na ito. Ino-override ang global na setting. |
| `docker` | bool\|null | `null` | Per-agent Docker sandbox override. Ang `null` = inheritin ang global `docker.enabled`. |
| `fallbackProviders` | string[] | `[]` | Nakaayos na listahan ng fallback provider name kung mabibigo ang pangunahin. |
| `trustLevel` | string | `"auto"` | Antas ng tiwala: `"observe"` (read-only), `"suggest"` (imungkahi ngunit hindi ilapat), `"auto"` (buong awtonomiya). |
| `tools` | AgentToolPolicy | `{}` | Patakaran ng tool access. Tingnan ang [Tool Policy](#tool-policy). |
| `toolProfile` | string | `"standard"` | Pinangalanang tool profile: `"minimal"`, `"standard"`, `"full"`. |
| `workspace` | WorkspaceConfig | `{}` | Mga setting ng workspace isolation. |

---

## Smart Dispatch

Awtomatikong iniruruta ng Smart Dispatch ang mga papasok na task sa pinaka-angkop na agent batay sa mga rule, keyword, at LLM classification.

```json
{
  "smartDispatch": {
    "enabled": true,
    "coordinator": "coordinator",
    "defaultAgent": "coordinator",
    "classifyBudget": 0.1,
    "classifyTimeout": "30s",
    "review": false,
    "reviewLoop": false,
    "maxRetries": 3,
    "fallback": "smart",
    "rules": [
      {
        "agent": "engineer",
        "keywords": ["bug", "error", "deploy", "docker"],
        "patterns": ["(?:fix|resolve)\\s+(?:bug|issue|error)"]
      },
      {
        "agent": "creator",
        "keywords": ["blog post", "documentation", "README"]
      }
    ],
    "bindings": [
      {
        "channel": "discord",
        "channelId": "123456789",
        "agent": "engineer"
      }
    ]
  }
}
```

### `smartDispatch` — `SmartDispatchConfig`

| Field | Uri | Default | Paglalarawan |
|---|---|---|---|
| `enabled` | bool | `false` | Paganahin ang smart dispatch routing. |
| `coordinator` | string | unang agent | Agent na ginagamit para sa LLM-based na pag-classify ng task. |
| `defaultAgent` | string | unang agent | Fallback agent kapag walang rule na nag-match. |
| `classifyBudget` | float64 | `0.1` | Gastos na budget (USD) para sa classification LLM call. |
| `classifyTimeout` | string | `"30s"` | Timeout para sa classification call. |
| `review` | bool | `false` | Magpatakbo ng review agent sa output pagkatapos makumpleto ang task. |
| `reviewLoop` | bool | `false` | Paganahin ang Dev↔QA retry loop: review → feedback → retry (hanggang `maxRetries`). |
| `maxRetries` | int | `3` | Maximum na bilang ng QA retry sa review loop. |
| `reviewAgent` | string | coordinator | Agent na responsable sa pag-review ng output. Itakda sa isang mahigpit na QA agent para sa adversarial review. |
| `reviewBudget` | float64 | `0.2` | Gastos na budget (USD) para sa review LLM call. |
| `fallback` | string | `"smart"` | Fallback na estratehiya: `"smart"` (LLM routing) o `"coordinator"` (palaging default agent). |
| `rules` | RoutingRule[] | `[]` | Mga keyword/regex routing rule na sinusuri bago ang LLM classification. |
| `bindings` | RoutingBinding[] | `[]` | Channel/user/guild → agent binding (pinakamataas na priyoridad, sinusuri muna). |

### `rules` — `RoutingRule`

| Field | Uri | Paglalarawan |
|---|---|---|
| `agent` | string | Pangalan ng target agent. |
| `keywords` | string[] | Mga keyword na hindi case-sensitive. Ang anumang match ay magru-route sa agent na ito. |
| `patterns` | string[] | Mga Go regex pattern. Ang anumang match ay magru-route sa agent na ito. |

### `bindings` — `RoutingBinding`

| Field | Uri | Paglalarawan |
|---|---|---|
| `channel` | string | Platform: `"telegram"`, `"discord"`, `"slack"`, atbp. |
| `userId` | string | User ID sa platform na iyon. |
| `channelId` | string | Channel o chat ID. |
| `guildId` | string | Guild/server ID (Discord lamang). |
| `agent` | string | Pangalan ng target agent. |

---

## Session

Kinokontrol kung paano pinapanatili at pinipiga ang conversation context sa mga multi-turn na interaksyon.

```json
{
  "session": {
    "contextMessages": 20,
    "compactAfter": 30,
    "compactKeep": 10,
    "compactTokens": 200000,
    "compaction": {
      "enabled": true,
      "maxMessages": 50,
      "compactTo": 10,
      "model": "haiku",
      "maxCost": 0.02,
      "provider": "claude"
    }
  }
}
```

### `session` — `SessionConfig`

| Field | Uri | Default | Paglalarawan |
|---|---|---|---|
| `contextMessages` | int | `20` | Maximum na bilang ng mga kamakailang mensahe na ii-inject bilang context sa isang bagong task. |
| `compactAfter` | int | `30` | I-compact kapag lumampas ang bilang ng mensahe dito. Deprecated: gamitin ang `compaction.maxMessages`. |
| `compactKeep` | int | `10` | Panatilihin ang huling N mensahe pagkatapos ng compaction. Deprecated: gamitin ang `compaction.compactTo`. |
| `compactTokens` | int | `200000` | I-compact kapang lumampas ang kabuuang input token sa threshold na ito. |

### `session.compaction` — `CompactionConfig`

| Field | Uri | Default | Paglalarawan |
|---|---|---|---|
| `enabled` | bool | `false` | Paganahin ang awtomatikong session compaction. |
| `maxMessages` | int | `50` | I-trigger ang compaction kapag lumampas ang session sa ganitong karaming mensahe. |
| `compactTo` | int | `10` | Bilang ng mga kamakailang mensahe na itatago pagkatapos ng compaction. |
| `model` | string | `"haiku"` | LLM model na gagamitin para sa pagbuo ng compaction summary. |
| `maxCost` | float64 | `0.02` | Maximum na gastos bawat compaction call (USD). |
| `provider` | string | `defaultProvider` | Provider na gagamitin para sa compaction summary call. |

---

## Task Board

Sinusubaybayan ng built-in na task board ang mga work item at maaaring awtomatikong ipamigay ang mga ito sa mga agent.

```json
{
  "taskBoard": {
    "enabled": true,
    "maxRetries": 3,
    "requireReview": false,
    "defaultWorkflow": "",
    "gitCommit": false,
    "gitPush": false,
    "gitPR": false,
    "gitWorktree": false,
    "gitWorkflow": {
      "branchConvention": "{type}/{agent}-{description}",
      "types": ["feat", "fix", "refactor", "chore"],
      "defaultType": "feat",
      "autoMerge": false
    },
    "autoDispatch": {
      "enabled": false,
      "interval": "5m",
      "maxConcurrentTasks": 3,
      "stuckThreshold": "2h",
      "reviewLoop": false
    }
  }
}
```

### `taskBoard` — `TaskBoardConfig`

| Field | Uri | Default | Paglalarawan |
|---|---|---|---|
| `enabled` | bool | `false` | Paganahin ang task board. |
| `maxRetries` | int | `3` | Maximum na bilang ng retry bawat task bago markahan bilang failed. |
| `requireReview` | bool | `false` | Quality gate: ang task ay dapat makapasa sa review bago markahang tapos. |
| `defaultWorkflow` | string | `""` | Pangalan ng workflow na patatakbuhin para sa lahat ng auto-dispatched na task. Walang laman = walang workflow. |
| `gitCommit` | bool | `false` | Auto-commit kapag natapos ang task. |
| `gitPush` | bool | `false` | Auto-push pagkatapos ng commit (kailangan ng `gitCommit: true`). |
| `gitPR` | bool | `false` | Auto-gumawa ng GitHub PR pagkatapos ng push (kailangan ng `gitPush: true`). |
| `gitWorktree` | bool | `false` | Gumamit ng git worktree para sa task isolation (inaalis ang file conflict sa pagitan ng mga concurrent na task). |
| `idleAnalyze` | bool | `false` | Auto-run analysis kapag idle ang board. |
| `problemScan` | bool | `false` | I-scan ang task output para sa mga nakatagong isyu pagkatapos makumpleto. |

### `taskBoard.autoDispatch` — `TaskBoardDispatchConfig`

| Field | Uri | Default | Paglalarawan |
|---|---|---|---|
| `enabled` | bool | `false` | Paganahin ang awtomatikong polling at dispatch ng mga queued na task. |
| `interval` | string | `"5m"` | Gaano kadalas mag-scan para sa mga handang task. |
| `maxConcurrentTasks` | int | `3` | Maximum na task na ini-dispatch bawat scan cycle. |
| `defaultModel` | string | `""` | I-override ang model para sa mga auto-dispatched na task. |
| `maxBudget` | float64 | `0` | Maximum na gastos bawat task (USD). Ang `0` = walang limitasyon. |
| `defaultAgent` | string | `""` | Fallback agent para sa mga hindi itinalagang task. |
| `backlogAgent` | string | `""` | Agent para sa backlog triage. |
| `reviewAgent` | string | `""` | Agent para sa pag-review ng mga natapos na task. |
| `escalateAssignee` | string | `""` | Italaga ang mga task na tinanggihan ng review sa user na ito. |
| `stuckThreshold` | string | `"2h"` | Ang mga task sa "doing" na mas matagal kaysa dito ay ire-reset sa "todo". |
| `backlogTriageInterval` | string | `"1h"` | Gaano kadalas magpatakbo ng backlog triage. |
| `reviewLoop` | bool | `false` | Paganahin ang automated na Dev↔QA loop para sa mga dispatched na task. |

### `taskBoard.gitWorkflow` — `GitWorkflowConfig`

| Field | Uri | Default | Paglalarawan |
|---|---|---|---|
| `branchConvention` | string | `"{type}/{agent}-{description}"` | Template para sa pagpapangalan ng branch. Mga variable: `{type}`, `{agent}`, `{description}`. |
| `types` | string[] | `["feat","fix","refactor","chore"]` | Mga pinapayagang prefix ng uri ng branch. |
| `defaultType` | string | `"feat"` | Fallback na uri kapag walang tinukoy. |
| `autoMerge` | bool | `false` | Awtomatikong i-merge pabalik sa main kapag tapos ang task (lamang kapag `gitWorktree: true`). |

---

## Slot Pressure

Kinokontrol kung paano kumilos ang sistema kapag malapit na sa limitasyon ng `maxConcurrent` slot. Ang mga interactive (human-initiated) na session ay nakakakuha ng mga reserved slot; ang mga background na task ay naghihintay.

```json
{
  "slotPressure": {
    "enabled": true,
    "reservedSlots": 2,
    "warnThreshold": 3,
    "nonInteractiveTimeout": "5m",
    "monitorEnabled": false,
    "monitorInterval": "30s"
  }
}
```

### `slotPressure` — `SlotPressureConfig`

| Field | Uri | Default | Paglalarawan |
|---|---|---|---|
| `enabled` | bool | `false` | Paganahin ang slot pressure management. |
| `reservedSlots` | int | `2` | Mga slot na nakareserbang para sa mga interactive na session. Hindi maaaring gamitin ng mga background na task ang mga ito. |
| `warnThreshold` | int | `3` | Balaan ang user kapag mas kaunti kaysa ganitong karaming slot ang available. |
| `nonInteractiveTimeout` | string | `"5m"` | Gaano katagal maghihintay ang background na task para sa isang slot bago mag-timeout. |
| `pollInterval` | string | `"2s"` | Gaano kadalas suriin ng mga background na task ang isang libreng slot. |
| `monitorEnabled` | bool | `false` | Paganahin ang proactive na slot pressure alert sa pamamagitan ng notification channel. |
| `monitorInterval` | string | `"30s"` | Gaano kadalas suriin at mag-emit ng pressure alert. |

---

## Mga Workflow

Ang mga workflow ay tinukoy bilang mga YAML file sa isang directory. Ang `workflowDir` ay nagtuturo sa directory na iyon; ang mga variable ay nagbibigay ng default na template value.

```json
{
  "workflowDir": "~/.tetora/workspace/workflows/",
  "workflowTriggers": [
    {
      "event": "task.done",
      "workflow": "notify-slack",
      "filter": {"status": "done"}
    }
  ]
}
```

| Field | Uri | Default | Paglalarawan |
|---|---|---|---|
| `workflowDir` | string | `~/.tetora/workspace/workflows/` | Directory kung saan naka-imbak ang mga workflow YAML file. |
| `workflowTriggers` | WorkflowTriggerConfig[] | `[]` | Mga awtomatikong workflow trigger sa mga system event. |

---

## Mga Integration

### Telegram

```json
{
  "telegram": {
    "enabled": true,
    "botToken": "$TELEGRAM_BOT_TOKEN",
    "chatID": 123456789,
    "pollTimeout": 30
  }
}
```

| Field | Uri | Default | Paglalarawan |
|---|---|---|---|
| `enabled` | bool | `false` | Paganahin ang Telegram bot. |
| `botToken` | string | `""` | Telegram bot token mula sa @BotFather. Sumusuporta ng `$ENV_VAR`. |
| `chatID` | int64 | `0` | Telegram chat o group ID kung saan ipapadala ang mga notification. |
| `pollTimeout` | int | `30` | Long-poll timeout sa segundo para sa pagtanggap ng mensahe. |

### Discord

```json
{
  "discord": {
    "enabled": true,
    "botToken": "$DISCORD_BOT_TOKEN",
    "guildID": "123456789",
    "channelIDs": ["111111111"],
    "mentionChannelIDs": ["222222222"],
    "notifyChannelID": "333333333",
    "showProgress": true,
    "routes": {
      "111111111": {"agent": "engineer"}
    }
  }
}
```

| Field | Uri | Default | Paglalarawan |
|---|---|---|---|
| `enabled` | bool | `false` | Paganahin ang Discord bot. |
| `botToken` | string | `""` | Discord bot token. Sumusuporta ng `$ENV_VAR`. |
| `guildID` | string | `""` | Limitahan sa isang partikular na Discord server (guild). |
| `channelIDs` | string[] | `[]` | Mga channel ID kung saan sumasagot ang bot sa lahat ng mensahe (hindi na kailangan ng `@` mention). |
| `mentionChannelIDs` | string[] | `[]` | Mga channel ID kung saan sumasagot ang bot lamang kapag `@`-binanggit. |
| `notifyChannelID` | string | `""` | Channel para sa mga task completion notification (gumagawa ng thread bawat task). |
| `showProgress` | bool | `true` | Ipakita ang live na "Working..." streaming message sa Discord. |
| `webhooks` | map[string]string | `{}` | Mga pinangalanang webhook URL para sa outbound-only na notification. |
| `routes` | map[string]DiscordRouteConfig | `{}` | Mapa ng channel ID sa pangalan ng agent para sa per-channel routing. |

### Slack

```json
{
  "slack": {
    "enabled": true,
    "botToken": "$SLACK_BOT_TOKEN",
    "signingSecret": "$SLACK_SIGNING_SECRET",
    "appToken": "$SLACK_APP_TOKEN",
    "defaultChannel": "C0123456789"
  }
}
```

| Field | Uri | Default | Paglalarawan |
|---|---|---|---|
| `enabled` | bool | `false` | Paganahin ang Slack bot. |
| `botToken` | string | `""` | Slack bot OAuth token (`xoxb-...`). Sumusuporta ng `$ENV_VAR`. |
| `signingSecret` | string | `""` | Slack signing secret para sa request verification. Sumusuporta ng `$ENV_VAR`. |
| `appToken` | string | `""` | Slack app-level token para sa Socket Mode (`xapp-...`). Opsyonal. Sumusuporta ng `$ENV_VAR`. |
| `defaultChannel` | string | `""` | Default na channel ID para sa outbound na notification. |

### Mga Outbound Webhook

```json
{
  "webhooks": [
    {
      "url": "https://hooks.example.com/tetora",
      "headers": {"Authorization": "$WEBHOOK_TOKEN"},
      "events": ["success", "error"]
    }
  ]
}
```

| Field | Uri | Default | Paglalarawan |
|---|---|---|---|
| `url` | string | kailangan | URL ng webhook endpoint. |
| `headers` | map[string]string | `{}` | Mga HTTP header na isasama. Ang mga value ay sumusuporta ng `$ENV_VAR`. |
| `events` | string[] | lahat | Mga event na ipapadala: `"success"`, `"error"`, `"timeout"`, `"all"`. Walang laman = lahat. |

### Mga Incoming Webhook

Ang mga incoming webhook ay nagpapahintulot sa mga external na serbisyo na mag-trigger ng Tetora task sa pamamagitan ng HTTP POST.

```json
{
  "incomingWebhooks": {
    "github": {
      "secret": "$GITHUB_WEBHOOK_SECRET",
      "agent": "engineer",
      "prompt": "A GitHub event occurred: {{.Body}}"
    }
  }
}
```

### Mga Notification Channel

Mga pinangalanang notification channel para sa pag-route ng mga task event sa iba't ibang Slack/Discord endpoint.

```json
{
  "notifications": [
    {
      "name": "alerts",
      "type": "slack",
      "webhookUrl": "$SLACK_ALERTS_WEBHOOK",
      "events": ["error"],
      "minPriority": "high"
    }
  ]
}
```

| Field | Uri | Default | Paglalarawan |
|---|---|---|---|
| `name` | string | `""` | Pinangalanang reference na ginagamit sa `channel` field ng job (hal., `"discord:alerts"`). |
| `type` | string | kailangan | `"slack"` o `"discord"`. |
| `webhookUrl` | string | kailangan | URL ng webhook. Sumusuporta ng `$ENV_VAR`. |
| `events` | string[] | lahat | I-filter ayon sa uri ng event: `"all"`, `"error"`, `"success"`. |
| `minPriority` | string | lahat | Minimum na priyoridad: `"critical"`, `"high"`, `"normal"`, `"low"`. |

---

## Store (Template Marketplace)

```json
{
  "store": {
    "enabled": true,
    "registryUrl": "https://registry.tetora.dev/v1",
    "authToken": "$TETORA_STORE_TOKEN"
  }
}
```

### `store` — `StoreConfig`

| Field | Uri | Default | Paglalarawan |
|---|---|---|---|
| `enabled` | bool | `false` | Paganahin ang template store. |
| `registryUrl` | string | `"https://registry.tetora.dev/v1"` | Remote registry URL para sa pag-browse at pag-install ng template. |
| `authToken` | string | `""` | Authentication token para sa registry. Sumusuporta ng `$ENV_VAR`. |

---

## Gastos at Mga Alerto

### `costAlert` — `CostAlertConfig`

```json
{
  "costAlert": {
    "dailyLimit": 10.0,
    "weeklyLimit": 50.0,
    "dailyTokenLimit": 1000000,
    "action": "warn"
  }
}
```

| Field | Uri | Default | Paglalarawan |
|---|---|---|---|
| `dailyLimit` | float64 | `0` | Araw-araw na limitasyon sa gastos sa USD. Ang `0` = walang limitasyon. |
| `weeklyLimit` | float64 | `0` | Lingguhang limitasyon sa gastos sa USD. Ang `0` = walang limitasyon. |
| `dailyTokenLimit` | int | `0` | Kabuuang araw-araw na token cap (input + output). Ang `0` = walang cap. |
| `action` | string | `"warn"` | Aksyon kapag nalabag ang limitasyon: `"warn"` (mag-log at magbigay ng notification) o `"pause"` (i-block ang mga bagong task). |

### `estimate` — `EstimateConfig`

Pre-execution na pagtatantya ng gastos bago patakbuhin ang isang task.

| Field | Uri | Default | Paglalarawan |
|---|---|---|---|
| `confirmThreshold` | float64 | `1.00` | Mag-prompt para sa kumpirmasyon kapag ang tinantiyang gastos ay lumampas sa USD value na ito. |
| `defaultOutputTokens` | int | `500` | Fallback na tantya ng output token kapag hindi alam ang aktwal na paggamit. |

### `budgets` — `BudgetConfig`

Mga gastos na budget sa antas ng agent at team.

---

## Logging

```json
{
  "logging": {
    "level": "info",
    "format": "text",
    "file": "~/.tetora/runtime/logs/tetora.log",
    "maxSizeMB": 50,
    "maxFiles": 5
  }
}
```

### `logging` — `LoggingConfig`

| Field | Uri | Default | Paglalarawan |
|---|---|---|---|
| `level` | string | `"info"` | Antas ng log: `"debug"`, `"info"`, `"warn"`, `"error"`. |
| `format` | string | `"text"` | Format ng log: `"text"` (nababasa ng tao) o `"json"` (structured). |
| `file` | string | `runtime/logs/tetora.log` | Path ng log file. Kaugnay ng runtime dir. |
| `maxSizeMB` | int | `50` | Maximum na laki ng log file sa MB bago mag-rotate. |
| `maxFiles` | int | `5` | Bilang ng mga rotated na log file na itatago. |

---

## Seguridad

### `dashboardAuth` — `DashboardAuthConfig`

```json
{
  "dashboardAuth": {
    "enabled": true,
    "username": "admin",
    "password": "$DASHBOARD_PASSWORD"
  }
}
```

| Field | Uri | Default | Paglalarawan |
|---|---|---|---|
| `enabled` | bool | `false` | Paganahin ang HTTP Basic Auth sa dashboard. |
| `username` | string | `"admin"` | Basic auth username. |
| `password` | string | `""` | Basic auth password. Sumusuporta ng `$ENV_VAR`. |
| `token` | string | `""` | Alternatibo: static token na ipinasa bilang cookie. |

### `tls` — `TLSConfig`

```json
{
  "tls": {
    "certFile": "/etc/tetora/cert.pem",
    "keyFile": "/etc/tetora/key.pem"
  }
}
```

| Field | Uri | Paglalarawan |
|---|---|---|
| `certFile` | string | Path sa TLS certificate PEM file. Pinapagana ang HTTPS kapag itinakda (kasabay ng `keyFile`). |
| `keyFile` | string | Path sa TLS private key PEM file. |

### `rateLimit` — `RateLimitConfig`

| Field | Uri | Default | Paglalarawan |
|---|---|---|---|
| `enabled` | bool | `false` | Paganahin ang per-IP request rate limiting. |
| `maxPerMin` | int | `60` | Maximum na API request bawat minuto bawat IP. |

### `securityAlert` — `SecurityAlertConfig`

| Field | Uri | Default | Paglalarawan |
|---|---|---|---|
| `enabled` | bool | `false` | Paganahin ang mga security alert sa paulit-ulit na auth failure. |
| `failThreshold` | int | `10` | Bilang ng mga pagkabigo sa window bago mag-alert. |
| `failWindowMin` | int | `5` | Sliding window sa minuto. |

### `approvalGates` — `ApprovalGateConfig`

Kailangan ng aprubasyong mula sa tao bago ang ilang tool ay maipatupad.

```json
{
  "approvalGates": {
    "enabled": true,
    "timeout": 120,
    "tools": ["bash", "write_file"],
    "autoApproveTools": ["read_file"]
  }
}
```

| Field | Uri | Default | Paglalarawan |
|---|---|---|---|
| `enabled` | bool | `false` | Paganahin ang mga approval gate. |
| `timeout` | int | `120` | Mga segundo na hihintayin para sa pag-apruba bago mag-cancel. |
| `tools` | string[] | `[]` | Mga pangalan ng tool na nangangailangan ng pag-apruba bago maipatupad. |
| `autoApproveTools` | string[] | `[]` | Mga tool na pre-approved sa startup (hindi na magpo-prompt). |

---

## Pagiging Maaasahan

### `circuitBreaker` — `CircuitBreakerConfig`

```json
{
  "circuitBreaker": {
    "enabled": true,
    "failThreshold": 5,
    "successThreshold": 2,
    "openTimeout": "30s"
  }
}
```

| Field | Uri | Default | Paglalarawan |
|---|---|---|---|
| `enabled` | bool | `true` | Paganahin ang circuit breaker para sa provider failover. |
| `failThreshold` | int | `5` | Mga sunud-sunod na pagkabigo bago buksan ang circuit. |
| `successThreshold` | int | `2` | Mga tagumpay sa half-open na estado bago isara. |
| `openTimeout` | string | `"30s"` | Tagal sa open na estado bago subukan muli (half-open). |

### `fallbackProviders`

```json
{
  "fallbackProviders": ["claude", "openai"]
}
```

Global na nakaayos na listahan ng mga fallback provider kung mabibigo ang default na provider.

### `heartbeat` — `HeartbeatConfig`

```json
{
  "heartbeat": {
    "enabled": true,
    "interval": "30s",
    "stallThreshold": "5m",
    "timeoutWarnRatio": 0.8,
    "autoCancel": false,
    "notifyOnStall": true
  }
}
```

| Field | Uri | Default | Paglalarawan |
|---|---|---|---|
| `enabled` | bool | `false` | Paganahin ang agent heartbeat monitoring. |
| `interval` | string | `"30s"` | Gaano kadalas suriin ang mga tumatakbong task para sa mga stall. |
| `stallThreshold` | string | `"5m"` | Walang output sa tagal na ito = naka-stall ang task. |
| `timeoutWarnRatio` | float64 | `0.8` | Magbabala kapang lumampas ang elapsed time sa ratio na ito ng task timeout. |
| `autoCancel` | bool | `false` | Awtomatikong kanselahin ang mga task na naka-stall nang mas matagal kaysa sa `2x stallThreshold`. |
| `notifyOnStall` | bool | `true` | Magpadala ng notification kapang natukoy na naka-stall ang isang task. |

### `retention` — `RetentionConfig`

Kinokontrol ang awtomatikong paglilinis ng lumang data.

```json
{
  "retention": {
    "history": 90,
    "sessions": 30,
    "auditLog": 365,
    "logs": 14,
    "workflows": 90,
    "outputs": 30
  }
}
```

| Field | Uri | Default | Paglalarawan |
|---|---|---|---|
| `history` | int | `90` | Mga araw na itatago ang kasaysayan ng job run. |
| `sessions` | int | `30` | Mga araw na itatago ang data ng session. |
| `auditLog` | int | `365` | Mga araw na itatago ang mga entry ng audit log. |
| `logs` | int | `14` | Mga araw na itatago ang mga log file. |
| `workflows` | int | `90` | Mga araw na itatago ang mga rekord ng workflow run. |
| `reflections` | int | `60` | Mga araw na itatago ang mga rekord ng reflection. |
| `sla` | int | `90` | Mga araw na itatago ang mga rekord ng SLA check. |
| `trustEvents` | int | `90` | Mga araw na itatago ang mga rekord ng trust event. |
| `handoffs` | int | `60` | Mga araw na itatago ang mga rekord ng agent handoff/mensahe. |
| `queue` | int | `7` | Mga araw na itatago ang mga offline queue item. |
| `versions` | int | `180` | Mga araw na itatago ang mga config version snapshot. |
| `outputs` | int | `30` | Mga araw na itatago ang mga agent output file. |
| `uploads` | int | `7` | Mga araw na itatago ang mga na-upload na file. |
| `memory` | int | `30` | Mga araw bago ang mga lumang memory entry ay ma-archive. |
| `claudeSessions` | int | `3` | Mga araw na itatago ang mga Claude CLI session artifact. |
| `piiPatterns` | string[] | `[]` | Mga regex pattern para sa PII redaction sa nakaimbak na nilalaman. |

---

## Quiet Hours at Digest

```json
{
  "quietHours": {
    "enabled": true,
    "start": "23:00",
    "end": "08:00",
    "tz": "Asia/Taipei",
    "digest": true
  },
  "digest": {
    "enabled": true,
    "time": "08:00",
    "tz": "Asia/Taipei"
  }
}
```

### `quietHours` — `QuietHoursConfig`

| Field | Uri | Default | Paglalarawan |
|---|---|---|---|
| `enabled` | bool | `false` | Paganahin ang quiet hours. Pinipigilan ang mga notification sa panahon ng window na ito. |
| `start` | string | `""` | Simula ng quiet period (lokal na oras, format na `"HH:MM"`). |
| `end` | string | `""` | Katapusan ng quiet period (lokal na oras). |
| `tz` | string | lokal | Timezone, hal. `"Asia/Taipei"`, `"UTC"`. |
| `digest` | bool | `false` | Magpadala ng digest ng mga pinigilan na notification kapang natapos na ang quiet hours. |

### `digest` — `DigestConfig`

| Field | Uri | Default | Paglalarawan |
|---|---|---|---|
| `enabled` | bool | `false` | Paganahin ang naka-iskedyul na araw-araw na digest. |
| `time` | string | `"08:00"` | Oras para ipadala ang digest (`"HH:MM"`). |
| `tz` | string | lokal | Timezone. |

---

## Mga Tool

```json
{
  "tools": {
    "maxIterations": 10,
    "timeout": 120,
    "toolOutputLimit": 10240,
    "toolTimeout": 30,
    "defaultProfile": "standard",
    "builtin": {
      "bash": true,
      "web_search": false
    },
    "webSearch": {
      "provider": "brave",
      "apiKey": "$BRAVE_API_KEY",
      "maxResults": 5
    },
    "vision": {
      "provider": "anthropic",
      "apiKey": "$ANTHROPIC_API_KEY",
      "model": "claude-opus-4-5"
    }
  }
}
```

### `tools` — `ToolConfig`

| Field | Uri | Default | Paglalarawan |
|---|---|---|---|
| `maxIterations` | int | `10` | Maximum na bilang ng tool call iteration bawat task. |
| `timeout` | int | `120` | Global na tool engine timeout sa segundo. |
| `toolOutputLimit` | int | `10240` | Maximum na karakter bawat tool output (niti-truncate lampas dito). |
| `toolTimeout` | int | `30` | Per-tool na execution timeout sa segundo. |
| `defaultProfile` | string | `"standard"` | Default na pangalan ng tool profile. |
| `builtin` | map[string]bool | `{}` | I-enable/disable ang mga indibidwal na built-in na tool ayon sa pangalan. |
| `profiles` | map[string]ToolProfile | `{}` | Mga custom na tool profile. |
| `trustOverride` | map[string]string | `{}` | I-override ang antas ng tiwala bawat pangalan ng tool. |

### `tools.webSearch` — `WebSearchConfig`

| Field | Uri | Default | Paglalarawan |
|---|---|---|---|
| `provider` | string | `""` | Search provider: `"brave"`, `"tavily"`, `"searxng"`. |
| `apiKey` | string | `""` | API key para sa provider. Sumusuporta ng `$ENV_VAR`. |
| `baseURL` | string | `""` | Custom na endpoint (para sa self-hosted na searxng). |
| `maxResults` | int | `5` | Maximum na bilang ng search result na ibabalik. |

### `tools.vision` — `VisionConfig`

| Field | Uri | Default | Paglalarawan |
|---|---|---|---|
| `provider` | string | `""` | Vision provider: `"anthropic"`, `"openai"`, `"google"`. |
| `apiKey` | string | `""` | API key. Sumusuporta ng `$ENV_VAR`. |
| `model` | string | `""` | Pangalan ng model para sa vision provider. |
| `maxImageSize` | int | `5242880` | Maximum na laki ng larawan sa bytes (default 5 MB). |
| `baseURL` | string | `""` | Custom na API endpoint. |

---

## MCP (Model Context Protocol)

### `mcpConfigs`

Mga pinangalanang MCP server configuration. Ang bawat key ay isang MCP config name; ang value ay ang buong MCP JSON config. Isusulat ng Tetora ang mga ito sa mga temp file at ipapasa ang mga ito sa claude binary sa pamamagitan ng `--mcp-config`.

```json
{
  "mcpConfigs": {
    "playwright": {
      "mcpServers": {
        "playwright": {
          "command": "npx",
          "args": ["@playwright/mcp@latest"]
        }
      }
    }
  }
}
```

### `mcpServers`

Mga pinasimpleng MCP server definition na direktang pinamamahalaan ng Tetora.

```json
{
  "mcpServers": {
    "my-server": {
      "command": "python",
      "args": ["/path/to/server.py"],
      "env": {"API_KEY": "$MY_API_KEY"},
      "enabled": true
    }
  }
}
```

| Field | Uri | Default | Paglalarawan |
|---|---|---|---|
| `command` | string | kailangan | Executable command. |
| `args` | string[] | `[]` | Mga argumento ng command. |
| `env` | map[string]string | `{}` | Mga environment variable para sa proseso. Ang mga value ay sumusuporta ng `$ENV_VAR`. |
| `enabled` | bool | `true` | Kung aktibo ang MCP server na ito. |

---

## Prompt Budget

Kinokontrol ang maximum na character budget para sa bawat seksyon ng system prompt. I-adjust kapang ang mga prompt ay hindi inaasahang niti-truncate.

```json
{
  "promptBudget": {
    "soulMax": 8000,
    "rulesMax": 4000,
    "knowledgeMax": 8000,
    "skillsMax": 4000,
    "maxSkillsPerTask": 3,
    "contextMax": 16000,
    "totalMax": 40000
  }
}
```

### `promptBudget` — `PromptBudgetConfig`

| Field | Uri | Default | Paglalarawan |
|---|---|---|---|
| `soulMax` | int | `8000` | Max na karakter para sa agent soul/personality prompt. |
| `rulesMax` | int | `4000` | Max na karakter para sa mga workspace rule. |
| `knowledgeMax` | int | `8000` | Max na karakter para sa nilalaman ng knowledge base. |
| `skillsMax` | int | `4000` | Max na karakter para sa mga ini-inject na skill. |
| `maxSkillsPerTask` | int | `3` | Maximum na bilang ng skill na ini-inject bawat task. |
| `contextMax` | int | `16000` | Max na karakter para sa session context. |
| `totalMax` | int | `40000` | Hard cap sa kabuuang laki ng system prompt (lahat ng seksyon pinagsama). |

---

## Agent Communication

Kinokontrol ang nested sub-agent dispatch (agent_dispatch tool).

```json
{
  "agentComm": {
    "enabled": true,
    "maxConcurrent": 3,
    "defaultTimeout": 900,
    "maxDepth": 3,
    "maxChildrenPerTask": 5
  }
}
```

### `agentComm` — `AgentCommConfig`

| Field | Uri | Default | Paglalarawan |
|---|---|---|---|
| `enabled` | bool | `false` | Paganahin ang `agent_dispatch` tool para sa mga nested sub-agent call. |
| `maxConcurrent` | int | `3` | Max na concurrent na `agent_dispatch` call nang global. |
| `defaultTimeout` | int | `900` | Default na sub-agent timeout sa segundo. |
| `maxDepth` | int | `3` | Maximum na antas ng pag-nest para sa mga sub-agent. |
| `maxChildrenPerTask` | int | `5` | Maximum na concurrent na child agent bawat parent task. |

---

## Mga Halimbawa

### Minimal Config

Isang minimal na config para makapagsimula gamit ang Claude CLI provider:

```json
{
  "claudePath": "/usr/local/bin/claude",
  "maxConcurrent": 3,
  "listenAddr": "127.0.0.1:8991",
  "apiToken": "$TETORA_API_TOKEN",
  "agents": {
    "coordinator": {
      "soulFile": "SOUL.md",
      "model": "sonnet",
      "description": "General-purpose agent."
    }
  }
}
```

### Multi-Agent Config na may Smart Dispatch

```json
{
  "claudePath": "/usr/local/bin/claude",
  "maxConcurrent": 5,
  "defaultModel": "sonnet",
  "defaultTimeout": "30m",
  "defaultBudget": 2.0,
  "defaultPermissionMode": "acceptEdits",
  "listenAddr": "127.0.0.1:8991",
  "apiToken": "$TETORA_API_TOKEN",
  "defaultWorkdir": "~/workspace",
  "agents": {
    "coordinator": {
      "soulFile": "SOUL.md",
      "model": "sonnet",
      "description": "Coordinator. Handles planning, research, and coordination.",
      "keywords": ["plan", "research", "coordinate", "summarize"]
    },
    "engineer": {
      "soulFile": "team/engineer/SOUL.md",
      "model": "sonnet",
      "description": "Engineer. Handles coding, debugging, and infrastructure.",
      "keywords": ["code", "debug", "deploy"]
    },
    "creator": {
      "soulFile": "team/creator/SOUL.md",
      "model": "sonnet",
      "description": "Creator. Handles writing, documentation, and content.",
      "keywords": ["write", "blog", "translate"]
    }
  },
  "smartDispatch": {
    "enabled": true,
    "coordinator": "coordinator",
    "defaultAgent": "coordinator",
    "classifyBudget": 0.1,
    "classifyTimeout": "30s",
    "rules": [
      {
        "agent": "engineer",
        "keywords": ["bug", "error", "deploy", "CI/CD", "docker"],
        "patterns": ["(?:fix|resolve)\\s+(?:bug|issue|error)"]
      },
      {
        "agent": "creator",
        "keywords": ["blog post", "documentation", "README", "translation"]
      }
    ]
  },
  "costAlert": {
    "dailyLimit": 10.0,
    "action": "warn"
  },
  "logging": {
    "level": "info",
    "format": "text"
  }
}
```

### Buong Config (Lahat ng Pangunahing Seksyon)

```json
{
  "claudePath": "/usr/local/bin/claude",
  "maxConcurrent": 5,
  "defaultModel": "sonnet",
  "defaultTimeout": "30m",
  "defaultBudget": 2.0,
  "defaultPermissionMode": "acceptEdits",
  "listenAddr": "127.0.0.1:8991",
  "apiToken": "$TETORA_API_TOKEN",

  "providers": {
    "claude": {
      "type": "claude-cli",
      "path": "/usr/local/bin/claude"
    }
  },

  "agents": {
    "coordinator": {
      "soulFile": "SOUL.md",
      "model": "sonnet",
      "description": "Coordinator and general-purpose agent."
    }
  },

  "smartDispatch": {
    "enabled": true,
    "coordinator": "coordinator",
    "defaultAgent": "coordinator",
    "rules": []
  },

  "session": {
    "contextMessages": 20,
    "compaction": {
      "enabled": true,
      "maxMessages": 50,
      "compactTo": 10,
      "model": "haiku"
    }
  },

  "taskBoard": {
    "enabled": true,
    "autoDispatch": {
      "enabled": true,
      "interval": "5m",
      "maxConcurrentTasks": 3
    },
    "gitCommit": true,
    "gitPush": false
  },

  "slotPressure": {
    "enabled": true,
    "reservedSlots": 2,
    "warnThreshold": 3,
    "nonInteractiveTimeout": "5m"
  },

  "telegram": {
    "enabled": false,
    "botToken": "$TELEGRAM_BOT_TOKEN",
    "chatID": 0,
    "pollTimeout": 30
  },

  "discord": {
    "enabled": false,
    "botToken": "$DISCORD_BOT_TOKEN"
  },

  "slack": {
    "enabled": false,
    "botToken": "$SLACK_BOT_TOKEN",
    "signingSecret": "$SLACK_SIGNING_SECRET"
  },

  "store": {
    "enabled": true,
    "registryUrl": "https://registry.tetora.dev/v1"
  },

  "costAlert": {
    "dailyLimit": 10.0,
    "weeklyLimit": 50.0,
    "action": "warn"
  },

  "logging": {
    "level": "info",
    "format": "text",
    "maxSizeMB": 50,
    "maxFiles": 5
  },

  "retention": {
    "history": 90,
    "sessions": 30,
    "logs": 14
  },

  "heartbeat": {
    "enabled": true,
    "stallThreshold": "5m",
    "autoCancel": false
  },

  "dashboardAuth": {
    "enabled": false
  },

  "promptBudget": {
    "soulMax": 8000,
    "rulesMax": 4000,
    "knowledgeMax": 8000,
    "totalMax": 40000
  }
}
```
