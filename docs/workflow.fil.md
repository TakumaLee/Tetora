# Mga Workflow

## Pangkalahatang-ideya

Ang mga workflow ay ang multi-step na sistema ng task orchestration ng Tetora. Tukuyin ang isang pagkakasunod-sunod ng mga hakbang sa JSON, hayaan ang iba't ibang agent na magtulungan, at i-automate ang mga kumplikadong gawain.

**Mga kaso ng paggamit:**

- Mga gawain na nangangailangan ng maraming agent na nagtatrabaho nang sunud-sunod o sabay-sabay
- Mga proseso na may conditional branching at lohika ng pag-retry sa error
- Automated na trabaho na tina-trigger ng mga cron schedule, event, o webhook
- Mga pormal na proseso na nangangailangan ng kasaysayan ng pagpapatupad at pagsubaybay ng gastos

## Mabilis na Pagsisimula

### 1. Sumulat ng workflow JSON

Gumawa ng `my-workflow.json`:

```json
{
  "name": "research-and-summarize",
  "description": "Gather information and write a summary",
  "variables": {
    "topic": "AI agents"
  },
  "timeout": "30m",
  "steps": [
    {
      "id": "research",
      "agent": "hisui",
      "prompt": "Search and organize the latest developments in {{topic}}, listing 5 key points"
    },
    {
      "id": "summarize",
      "agent": "kohaku",
      "prompt": "Write a 300-word summary based on the following:\n{{steps.research.output}}",
      "dependsOn": ["research"]
    }
  ]
}
```

### 2. Mag-import at mag-validate

```bash
# I-validate ang istraktura ng JSON
tetora workflow validate my-workflow.json

# Mag-import sa ~/.tetora/workflows/
tetora workflow create my-workflow.json
```

### 3. Patakbuhin

```bash
# Patakbuhin ang workflow
tetora workflow run research-and-summarize

# I-override ang mga variable
tetora workflow run research-and-summarize --var topic="LLM safety"

# Dry-run (walang tawag sa LLM, pagtatantya ng gastos lamang)
tetora workflow run research-and-summarize --dry-run
```

### 4. Suriin ang mga resulta

```bash
# Ilista ang kasaysayan ng pagpapatupad
tetora workflow runs research-and-summarize

# Tingnan ang detalyadong status ng isang partikular na run
tetora workflow status <run-id>
```

## Istraktura ng Workflow JSON

### Mga Field sa Pinakamataas na Antas

| Field | Uri | Kinakailangan | Paglalarawan |
|-------|------|:--------:|-------------|
| `name` | string | Oo | Pangalan ng workflow. Alphanumeric, `-`, at `_` lamang (hal. `my-workflow`) |
| `description` | string | | Paglalarawan |
| `steps` | WorkflowStep[] | Oo | Kahit isang hakbang man lang |
| `variables` | map[string]string | | Mga input variable na may mga default (walang laman `""` = kinakailangan) |
| `timeout` | string | | Kabuuang timeout sa format na Go duration (hal. `"30m"`, `"1h"`) |
| `onSuccess` | string | | Template ng notipikasyon sa tagumpay |
| `onFailure` | string | | Template ng notipikasyon sa kabiguan |

### Mga Field ng WorkflowStep

| Field | Uri | Paglalarawan |
|-------|------|-------------|
| `id` | string | **Kinakailangan** — Natatanging identifier ng hakbang |
| `type` | string | Uri ng hakbang, default ay `"dispatch"`. Tingnan ang mga uri sa ibaba |
| `agent` | string | Papel ng agent na magpapatakbo ng hakbang na ito |
| `prompt` | string | Tagubilin para sa agent (sumusuporta sa mga template na `{{}}`) |
| `skill` | string | Pangalan ng skill (para sa type=skill) |
| `skillArgs` | string[] | Mga argumento ng skill (sumusuporta sa mga template) |
| `dependsOn` | string[] | Mga ID ng hakbang na kailangang matapos muna (mga dependency ng DAG) |
| `model` | string | Override ng LLM model |
| `provider` | string | Override ng provider |
| `timeout` | string | Timeout bawat hakbang |
| `budget` | number | Limitasyon sa gastos (USD) |
| `permissionMode` | string | Mode ng pahintulot |
| `if` | string | Expression ng kondisyon (type=condition) |
| `then` | string | ID ng hakbang na tutukuyin kapag totoo ang kondisyon |
| `else` | string | ID ng hakbang na tutukuyin kapag mali ang kondisyon |
| `handoffFrom` | string | ID ng pinagmulang hakbang (type=handoff) |
| `parallel` | WorkflowStep[] | Mga sub-step na tatakbo nang sabay-sabay (type=parallel) |
| `retryMax` | int | Pinakamataas na bilang ng pag-retry (nangangailangan ng `onError: "retry"`) |
| `retryDelay` | string | Agwat ng pag-retry, hal. `"10s"` |
| `onError` | string | Paghawak sa error: `"stop"` (default), `"skip"`, `"retry"` |
| `toolName` | string | Pangalan ng tool (type=tool_call) |
| `toolInput` | map[string]string | Mga input parameter ng tool (sumusuporta sa pagpapalawak ng `{{var}}`) |
| `delay` | string | Tagal ng paghihintay (type=delay), hal. `"30s"`, `"5m"` |
| `notifyMsg` | string | Mensahe ng notipikasyon (type=notify, sumusuporta sa mga template) |
| `notifyTo` | string | Pahiwatig ng channel ng notipikasyon (hal. `"telegram"`) |

## Mga Uri ng Hakbang

### dispatch (default)

Nagpapadala ng prompt sa tinukoy na agent para sa pagpapatupad. Ito ang pinakakaraniwang uri ng hakbang at ginagamit kapag inalis ang `type`.

```json
{
  "id": "draft",
  "agent": "kohaku",
  "prompt": "Write an article about {{topic}}",
  "model": "claude-sonnet-4-20250514",
  "timeout": "10m"
}
```

**Kinakailangan:** `prompt`
**Opsyonal:** `agent`, `model`, `provider`, `timeout`, `budget`, `permissionMode`

### skill

Nagpapatakbo ng isang nakarehistrong skill.

```json
{
  "id": "search",
  "type": "skill",
  "skill": "web-search",
  "skillArgs": ["{{topic}}", "--depth", "3"]
}
```

**Kinakailangan:** `skill`
**Opsyonal:** `skillArgs`

### condition

Sinusuri ang isang expression ng kondisyon upang matukoy ang landas. Kapag totoo, tinatahak ang `then`; kapag mali, tinatahak ang `else`. Ang landas na hindi pinili ay minarkahan bilang skipped.

```json
{
  "id": "check-type",
  "type": "condition",
  "if": "{{type}} == 'technical'",
  "then": "tech-research",
  "else": "creative-draft"
}
```

**Kinakailangan:** `if`, `then`
**Opsyonal:** `else`

Mga sinusuportahang operator:
- `==` — katumbas (hal. `{{type}} == 'technical'`)
- `!=` — hindi katumbas
- Truthy na tseke — ang hindi walang laman at hindi `"false"`/`"0"` ay itinuturing na totoo

### parallel

Nagpapatakbo ng maraming sub-step nang sabay-sabay, naghihintay na matapos ang lahat. Ang mga output ng sub-step ay pinagsama gamit ang `\n---\n`.

```json
{
  "id": "gather",
  "type": "parallel",
  "parallel": [
    {"id": "search-papers", "agent": "hisui", "prompt": "Search for papers"},
    {"id": "search-code", "agent": "kokuyou", "prompt": "Search open-source projects"}
  ]
}
```

**Kinakailangan:** `parallel` (kahit isang sub-step man lang)

Ang mga resulta ng indibidwal na sub-step ay maaaring i-reference sa pamamagitan ng `{{steps.search-papers.output}}`.

### handoff

Ipinapasa ang output ng isang hakbang sa ibang agent para sa karagdagang pagpoproseso. Ang buong output ng pinagmulang hakbang ay nagiging konteksto ng tumatanggap na agent.

```json
{
  "id": "review",
  "type": "handoff",
  "agent": "ruri",
  "handoffFrom": "draft",
  "prompt": "Review and revise the article",
  "dependsOn": ["draft"]
}
```

**Kinakailangan:** `handoffFrom`, `agent`
**Opsyonal:** `prompt` (tagubilin para sa tumatanggap na agent)

### tool_call

Nag-i-invoke ng isang nakarehistrong tool mula sa tool registry.

```json
{
  "id": "fetch-data",
  "type": "tool_call",
  "toolName": "http-get",
  "toolInput": {
    "url": "https://api.example.com/data?q={{topic}}"
  }
}
```

**Kinakailangan:** `toolName`
**Opsyonal:** `toolInput` (sumusuporta sa pagpapalawak ng `{{var}}`)

### delay

Naghihintay ng tinukoy na tagal bago magpatuloy.

```json
{
  "id": "wait",
  "type": "delay",
  "delay": "30s"
}
```

**Kinakailangan:** `delay` (format ng Go duration: `"30s"`, `"5m"`, `"1h"`)

### notify

Nagpapadala ng mensahe ng notipikasyon. Ang mensahe ay nipo-publish bilang SSE event (type=`workflow_notify`) upang ang mga panlabas na consumer ay makapag-trigger ng Telegram, Slack, atbp.

```json
{
  "id": "notify-done",
  "type": "notify",
  "notifyMsg": "Task complete: {{steps.review.output}}",
  "notifyTo": "telegram"
}
```

**Kinakailangan:** `notifyMsg`
**Opsyonal:** `notifyTo` (pahiwatig ng channel)

## Mga Variable at Template

Sumusuporta ang mga workflow sa syntax ng template na `{{}}`, na pinapalawak bago ang pagpapatupad ng hakbang.

### Mga Input Variable

```
{{varName}}
```

Nalulutas mula sa mga default ng `variables` o sa mga override na `--var key=value`.

### Mga Resulta ng Hakbang

```
{{steps.ID.output}}    — Teksto ng output ng hakbang
{{steps.ID.status}}    — Status ng hakbang (success/error/skipped/timeout)
{{steps.ID.error}}     — Mensahe ng error ng hakbang
```

### Mga Environment Variable

```
{{env.KEY}}            — Variable ng environment ng sistema
```

### Halimbawa

```json
{
  "id": "summarize",
  "agent": "kohaku",
  "prompt": "Topic: {{topic}}\nResearch results: {{steps.research.output}}\n\nPlease write a summary.",
  "dependsOn": ["research"]
}
```

## Mga Dependency at Kontrol ng Daloy

### dependsOn — Mga Dependency ng DAG

Gamitin ang `dependsOn` upang tukuyin ang pagkakasunod-sunod ng pagpapatupad. Awtomatikong inuuri ng sistema ang mga hakbang bilang isang DAG (Directed Acyclic Graph).

```json
{
  "id": "step-c",
  "dependsOn": ["step-a", "step-b"],
  "prompt": "..."
}
```

- Ang `step-c` ay naghihintay na matapos ang parehong `step-a` at `step-b`
- Ang mga hakbang na walang `dependsOn` ay nagsisimula agad (posibleng sabay-sabay)
- Ang mga circular na dependency ay natatagpuan at tinatanggihan

### Conditional Branching

Ang `then`/`else` ng hakbang na `condition` ay nagtatakda kung aling mga kasunod na hakbang ang isasagawa:

```
classify (condition)
  ├── then → tech-research
  └── else → creative-draft
```

Ang hakbang sa landas na hindi pinili ay minarkahan bilang `skipped`. Ang mga kasunod na hakbang ay normal pa rin na sinusuri batay sa kanilang `dependsOn`.

## Paghawak sa Error

### Mga Estratehiya ng onError

Maaaring itakda ng bawat hakbang ang `onError`:

| Halaga | Gawi |
|-------|----------|
| `"stop"` | **Default** — Itigil ang workflow sa kabiguan; ang mga natitirang hakbang ay minarkahan bilang skipped |
| `"skip"` | Markahan ang nabigong hakbang bilang skipped at magpatuloy |
| `"retry"` | Mag-retry ayon sa `retryMax` + `retryDelay`; kung mabigo ang lahat ng retry, ituring bilang error |

### Konfigurasyong ng Pag-retry

```json
{
  "id": "flaky-step",
  "agent": "hisui",
  "prompt": "...",
  "onError": "retry",
  "retryMax": 3,
  "retryDelay": "10s"
}
```

- `retryMax`: Pinakamataas na bilang ng pagtatangka ng retry (hindi kasama ang unang pagtatangka)
- `retryDelay`: Agwat sa pagitan ng mga retry, default ay 5 segundo
- Epektibo lamang kapag `onError: "retry"`

## Mga Trigger

Pinapagana ng mga trigger ang awtomatikong pagpapatupad ng workflow. I-configure ang mga ito sa `config.json` sa ilalim ng array na `workflowTriggers`.

### Istraktura ng WorkflowTriggerConfig

| Field | Uri | Paglalarawan |
|-------|------|-------------|
| `name` | string | Pangalan ng trigger |
| `workflowName` | string | Workflow na ipapagana |
| `enabled` | bool | Kung pinagana (default: true) |
| `trigger` | TriggerSpec | Kondisyon ng trigger |
| `variables` | map[string]string | Mga override ng variable para sa workflow |
| `cooldown` | string | Panahon ng cooldown (hal. `"5m"`, `"1h"`) |

### Istraktura ng TriggerSpec

| Field | Uri | Paglalarawan |
|-------|------|-------------|
| `type` | string | `"cron"`, `"event"`, o `"webhook"` |
| `cron` | string | Expression ng cron (5 field: min hour day month weekday) |
| `tz` | string | Timezone (hal. `"Asia/Taipei"`), para sa cron lamang |
| `event` | string | Uri ng SSE event, sumusuporta sa wildcard na `*` sa suffix (hal. `"deploy_*"`) |
| `webhook` | string | Suffix ng path ng webhook |

### Mga Cron Trigger

Sinusuri tuwing 30 segundo, nagpapaputok nang isang beses lamang bawat minuto (deduplication).

```json
{
  "name": "daily-briefing",
  "workflowName": "research-and-summarize",
  "trigger": {"type": "cron", "cron": "0 8 * * *", "tz": "Asia/Taipei"},
  "variables": {"topic": "AI industry news"},
  "cooldown": "12h"
}
```

### Mga Event Trigger

Nakikinig sa channel ng SSE na `_triggers` at tumutugma sa mga uri ng event. Sumusuporta sa wildcard na `*` sa suffix.

```json
{
  "name": "on-deploy",
  "workflowName": "content-pipeline",
  "trigger": {"type": "event", "event": "deploy_*"},
  "variables": {"type": "technical"}
}
```

Ang mga event trigger ay awtomatikong naglalagay ng karagdagang mga variable: `event_type`, `task_id`, `session_id`, kasama ang mga field ng data ng event (may prefix na `event_`).

### Mga Webhook Trigger

Tina-trigger sa pamamagitan ng HTTP POST:

```json
{
  "name": "external-hook",
  "workflowName": "content-pipeline",
  "trigger": {"type": "webhook", "webhook": "content-request"}
}
```

Paggamit:

```bash
curl -X POST http://localhost:PORT/api/triggers/webhook/external-hook \
  -H "Content-Type: application/json" \
  -d '{"topic": "new feature"}'
```

Ang mga pares ng key-value sa JSON ng POST body ay nila-lagay bilang karagdagang mga variable ng workflow.

### Cooldown

Sumusuporta ang lahat ng trigger sa `cooldown` upang maiwasan ang paulit-ulit na pagpapaputok sa loob ng maikling panahon. Ang mga trigger sa panahon ng cooldown ay tahimik na binabalewala.

### Mga Meta-Variable ng Trigger

Awtomatikong nila-lagay ng sistema ang mga variable na ito sa bawat trigger:

- `_trigger_name` — Pangalan ng trigger
- `_trigger_type` — Uri ng trigger (cron/event/webhook)
- `_trigger_time` — Oras ng trigger (RFC3339)

## Mga Mode ng Pagpapatupad

### live (default)

Buong pagpapatupad: tumatawag sa mga LLM, nagtatala ng kasaysayan, nag-publish ng mga SSE event.

```bash
tetora workflow run my-workflow
```

### dry-run

Walang tawag sa LLM; tinatantya ang gastos para sa bawat hakbang. Normal na sinusuri ang mga hakbang na condition; ang mga hakbang na dispatch/skill/handoff ay nagbabalik ng mga tantya ng gastos.

```bash
tetora workflow run my-workflow --dry-run
```

### shadow

Normal na nagpapatupad ng mga tawag sa LLM ngunit hindi nagtatala sa kasaysayan ng gawain o mga log ng session. Kapaki-pakinabang para sa pagsubok.

```bash
tetora workflow run my-workflow --shadow
```

## Sanggunian ng CLI

```
tetora workflow <command> [options]
```

| Utos | Paglalarawan |
|---------|-------------|
| `list` | Ilista ang lahat ng nakatipong workflow |
| `show <name>` | Ipakita ang depinisyon ng workflow (buod + JSON) |
| `validate <name\|file>` | I-validate ang isang workflow (tumatanggap ng pangalan o path ng JSON file) |
| `create <file>` | Mag-import ng workflow mula sa JSON file (nagva-validate muna) |
| `delete <name>` | Magtanggal ng workflow |
| `run <name> [flags]` | Magpatakbo ng workflow |
| `runs [name]` | Ilista ang kasaysayan ng pagpapatupad (opsyonal na i-filter ayon sa pangalan) |
| `status <run-id>` | Ipakita ang detalyadong status ng isang run (output na JSON) |
| `messages <run-id>` | Ipakita ang mga mensahe ng agent at mga talaan ng handoff para sa isang run |
| `history <name>` | Ipakita ang kasaysayan ng bersyon ng workflow |
| `rollback <name> <version-id>` | Ibalik sa isang partikular na bersyon |
| `diff <version1> <version2>` | Ihambing ang dalawang bersyon |

### Mga Flag ng Utos na run

| Flag | Paglalarawan |
|------|-------------|
| `--var key=value` | I-override ang isang variable ng workflow (maaaring gamitin nang maraming beses) |
| `--dry-run` | Mode na dry-run (walang tawag sa LLM) |
| `--shadow` | Mode na shadow (walang pagtatala ng kasaysayan) |

### Mga Alias

- `list` = `ls`
- `delete` = `rm`
- `messages` = `msgs`

## Sanggunian ng HTTP API

### Workflow CRUD

| Pamamaraan | Path | Paglalarawan |
|--------|------|-------------|
| GET | `/workflows` | Ilista ang lahat ng workflow |
| POST | `/workflows` | Gumawa ng workflow (body: Workflow JSON) |
| GET | `/workflows/{name}` | Kunin ang isang depinisyon ng workflow |
| DELETE | `/workflows/{name}` | Magtanggal ng workflow |
| POST | `/workflows/{name}/validate` | I-validate ang isang workflow |
| POST | `/workflows/{name}/run` | Magpatakbo ng workflow (async, nagbabalik ng `202 Accepted`) |
| GET | `/workflows/{name}/runs` | Kunin ang kasaysayan ng run para sa isang workflow |

#### POST /workflows/{name}/run Body

```json
{
  "variables": {
    "topic": "AI agents"
  }
}
```

### Mga Workflow Run

| Pamamaraan | Path | Paglalarawan |
|--------|------|-------------|
| GET | `/workflow-runs` | Ilista ang lahat ng talaan ng run (magdagdag ng `?workflow=name` para i-filter) |
| GET | `/workflow-runs/{id}` | Kunin ang mga detalye ng run (kasama ang mga handoff + mensahe ng agent) |

### Mga Trigger

| Pamamaraan | Path | Paglalarawan |
|--------|------|-------------|
| GET | `/api/triggers` | Ilista ang lahat ng status ng trigger |
| POST | `/api/triggers/{name}/fire` | Manu-manong magpaputok ng trigger |
| GET | `/api/triggers/{name}/runs` | Tingnan ang kasaysayan ng run ng trigger (magdagdag ng `?limit=N`) |
| POST | `/api/triggers/webhook/{id}` | Webhook trigger (body: JSON key-value variables) |

## Pamamahala ng Bersyon

Ang bawat `create` o pagbabago ay awtomatikong gumagawa ng snapshot ng bersyon.

```bash
# Tingnan ang kasaysayan ng bersyon
tetora workflow history my-workflow

# Ibalik sa isang partikular na bersyon
tetora workflow rollback my-workflow <version-id>

# Ihambing ang dalawang bersyon
tetora workflow diff <version-id-1> <version-id-2>
```

## Mga Patakaran sa Validation

Vina-validate ng sistema bago ang parehong `create` at `run`:

- Kinakailangan ang `name`; alphanumeric, `-`, at `_` lamang ang pinapayagan
- Kailangan ng kahit isang hakbang
- Ang mga ID ng hakbang ay dapat natatangi
- Ang mga reference sa `dependsOn` ay dapat tumuro sa mga umiiral na ID ng hakbang
- Hindi maaaring umasa ang mga hakbang sa kanilang sarili
- Ang mga circular na dependency ay tinatanggihan (pagtuklas ng cycle sa DAG)
- Mga kinakailangang field bawat uri ng hakbang (hal. ang dispatch ay nangangailangan ng `prompt`, ang condition ay nangangailangan ng `if` + `then`)
- Ang `timeout`, `retryDelay`, `delay` ay dapat nasa wastong format ng Go duration
- Ang `onError` ay tumatanggap lamang ng `stop`, `skip`, `retry`
- Ang `then`/`else` ng condition ay dapat mag-reference ng mga umiiral na ID ng hakbang
- Ang `handoffFrom` ng handoff ay dapat mag-reference ng isang umiiral na ID ng hakbang
