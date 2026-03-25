---
title: "Taskboard at Auto-Dispatch na Gabay"
lang: "fil"
order: 4
description: "Track tasks, priorities, and agent assignments with the built-in taskboard."
---
# Taskboard at Auto-Dispatch na Gabay

## Pangkalahatang-ideya

Ang Taskboard ay ang built-in na kanban system ng Tetora para sa pagsubaybay at awtomatikong pagpapatupad ng mga task. Pinagsasama nito ang isang persistent na task store (backed ng SQLite) at isang auto-dispatch engine na nagmamatyag sa mga handang task at ibinibigay ang mga ito sa mga agent nang walang manu-manong interbensyon.

Mga tipikal na use case:

- Mag-queue ng backlog ng mga engineering task at hayaan ang mga agent na trabahoin ang mga ito sa gabi
- Iruta ang mga task sa mga partikular na agent batay sa kaalaman (hal. `kokuyou` para sa backend, `kohaku` para sa content)
- Mag-chain ng mga task na may dependency relationship upang ang mga agent ay magpatuloy kung saan natapos ang iba
- Isama ang pagpapatupad ng task sa git: awtomatikong paglikha ng branch, commit, push, at PR/MR

**Mga kinakailangan:** `taskBoard.enabled: true` sa `config.json` at tumatakbong Tetora daemon.

---

## Lifecycle ng Task

Ang mga task ay dumadaan sa mga status sa ganitong pagkakasunud-sunod:

```
idea → needs-thought → backlog → todo → doing → review → done
                                                  ↓
                                           partial-done
                                                  ↓
                                              failed
```

| Status | Kahulugan |
|---|---|
| `idea` | Magaspang na konsepto, hindi pa pinino |
| `needs-thought` | Nangangailangan ng pagsusuri o disenyo bago maipatupad |
| `backlog` | Natukoy at inuna-unahan, ngunit hindi pa naka-iskedyul |
| `todo` | Handa nang maipatupad — kukuhanin ito ng auto-dispatch kung may nakatalagang agent |
| `doing` | Kasalukuyang tumatakbo |
| `review` | Natapos na ang pagpapatupad, naghihintay ng quality review |
| `done` | Nakumpleto at na-review |
| `partial-done` | Matagumpay ang pagpapatupad ngunit nabigo ang post-processing (hal. git merge conflict). Maaaring mabawi. |
| `failed` | Nabigo ang pagpapatupad o walang output. Susubukan ulit hanggang `maxRetries`. |

Kukuhain ng auto-dispatch ang mga task na may `status=todo`. Kung ang isang task ay walang nakatalagang agent, awtomatiko itong itatalaga sa `defaultAgent` (default: `ruri`). Ang mga task sa `backlog` ay pana-panahong tini-triage ng na-configure na `backlogAgent` (default: `ruri`) na nagpo-promote ng mga promising na task sa `todo`.

---

## Paggawa ng Mga Task

### CLI

```bash
# Minimal na task (napupunta sa backlog, walang nakatalagang agent)
tetora task create --title="Add rate limiting to API"

# Na may lahat ng opsyon
tetora task create \
  --title="Refactor auth middleware" \
  --description="Split token validation into its own package. See ADR-14." \
  --priority=high \
  --assignee=kokuyou \
  --type=refactor

# Ilista ang mga task
tetora task list
tetora task list --status=todo
tetora task list --assignee=kokuyou
tetora task list --project=api-v2

# Ipakita ang isang partikular na task
tetora task show task-abc123
tetora task show task-abc123 --full   # kasama ang mga komento/thread

# Manu-manong ilipat ang isang task
tetora task move task-abc123 --status=todo

# Italaga sa isang agent
tetora task assign task-abc123 --assignee=kokuyou

# Magdagdag ng komento (uri na spec, context, log, o system)
tetora task comment task-abc123 \
  --author=takuma \
  --content="Must pass existing test suite. Do not touch auth.go." \
  --type=spec
```

Awtomatikong nagagawa ang mga Task ID sa format na `task-<uuid>`. Maaari mong i-reference ang isang task gamit ang buong ID nito o isang maikling prefix — magmumungkahi ang CLI ng mga match.

### HTTP API

```bash
# Gumawa
curl -X POST http://localhost:8991/api/tasks \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Add rate limiting",
    "description": "Implement token bucket per API key",
    "priority": "high",
    "assignee": "kokuyou",
    "type": "feat"
  }'

# Listahan (i-filter ayon sa status)
curl "http://localhost:8991/api/tasks?status=todo"

# Ilipat sa bagong status
curl -X PATCH http://localhost:8991/api/tasks/task-abc123 \
  -H "Content-Type: application/json" \
  -d '{"status": "todo"}'
```

### Dashboard

Buksan ang **Taskboard** tab sa dashboard (`http://localhost:8991/dashboard`). Ang mga task ay ipinapakita sa mga kanban column. I-drag ang mga card sa pagitan ng column para baguhin ang status, i-click ang isang card para buksan ang detail panel na may mga komento at diff view.

---

## Auto-Dispatch

Ang auto-dispatch ay ang background loop na kumukuha ng mga `todo` na task at pinapatakbo ang mga ito sa pamamagitan ng mga agent.

### Paano ito gumagana

1. Nagpapaputok ang ticker bawat `interval` (default: `5m`).
2. Sinusuri ng scanner kung ilang task ang kasalukuyang tumatakbo. Kung `activeCount >= maxConcurrentTasks`, nilalaktawan ang scan.
3. Para sa bawat `todo` na task na may nakatalagang agent, ang task ay dini-dispatch sa agent na iyon. Ang mga task na walang nakatalagang agent ay awtomatikong itinatalaga sa `defaultAgent`.
4. Kapang natapos ang isang task, agad na nagpapaputok ang muling pag-scan upang magsimula ang susunod na batch nang hindi na kailangang hintayin ang buong interval.
5. Sa startup ng daemon, ang mga inulilang `doing` na task mula sa nakaraang crash ay alinman ay ini-restore sa `done` (kung may ebidensya ng pagkumpleto) o iri-reset sa `todo` (kung tunay na inulila).

### Dispatch Flow

```
                          ┌─────────┐
                          │  idea   │  (manu-manong pagpasok ng konsepto)
                          └────┬────┘
                               ▼
                       ┌──────────────┐
                       │ needs-thought │  (nangangailangan ng pagsusuri)
                       └───────┬──────┘
                               ▼
  ┌──────────────────────────────────────────────────────────┐
  │                       backlog                             │
  │                                                           │
  │  Triage (backlogAgent, default: ruri) ay tumatakbo nang   │
  │  pana-panahon:                                            │
  │   • "ready"     → italaga ang agent → ilipat sa todo      │
  │   • "decompose" → gumawa ng subtask → parent sa doing     │
  │   • "clarify"   → magdagdag ng tanong bilang komento →    │
  │                   manatili sa backlog                     │
  │                                                           │
  │  Fast-path: mayroon nang nakatalagang agent + walang      │
  │  blocking deps → laktawan ang LLM triage, direktang       │
  │  i-promote sa todo                                        │
  └──────────────────────┬───────────────────────────────────┘
                         ▼
  ┌──────────────────────────────────────────────────────────┐
  │                        todo                               │
  │                                                           │
  │  Kinukuha ng auto-dispatch ang mga task sa bawat scan:    │
  │   • May nakatalagang agent  → i-dispatch sa agent na iyon │
  │   • Walang nakatalagang     → italaga ang defaultAgent,   │
  │     agent                    pagkatapos patakbuhin        │
  │   • May workflow            → patakbuhin sa workflow      │
  │                               pipeline                    │
  │   • May dependsOn           → hintayin hanggang tapos     │
  │                               ang mga dep                 │
  │   • May naunang resumable   → ipagpatuloy mula sa         │
  │     na run                    checkpoint                  │
  └──────────────────────┬───────────────────────────────────┘
                         ▼
  ┌──────────────────────────────────────────────────────────┐
  │                       doing                               │
  │                                                           │
  │  Isinasagawa ng agent ang task (solong prompt o workflow  │
  │  DAG)                                                     │
  │                                                           │
  │  Guard: stuckThreshold (default 2h)                       │
  │   • Kung tumatakbo pa ang workflow → i-refresh timestamp  │
  │   • Kung tunay na naka-stuck       → i-reset sa todo      │
  └────────┬──────────┬──────────┬──────────────────────────┘
           │          │          │
     tagumpay    partial failure  pagkabigo
           │          │          │
           ▼          ▼          ▼
       ┌────────┐ ┌──────────┐ ┌────────┐
       │ review │ │ partial- │ │ failed │
       │        │ │   done   │ │        │
       └───┬────┘ └────┬─────┘ └───┬────┘
           │           │           │
           │     Resume button     │  Retry (hanggang maxRetries)
           │     sa dashboard      │  o escalate
           ▼                       ▼
       ┌────────┐            ┌──────────┐
       │  done  │            │ escalate │
       └────────┘            │ sa tao   │
                             └──────────┘
```

### Mga Detalye ng Triage

Ang triage ay tumatakbo bawat `backlogTriageInterval` (default: `1h`) at isinasagawa ng `backlogAgent` (default: `ruri`). Tinatanggap ng agent ang bawat backlog task na may mga komento nito at available na roster ng agent, pagkatapos ay nagpapasya:

| Aksyon | Epekto |
|---|---|
| `ready` | Nagtatalagang partikular na agent at nagpo-promote sa `todo` |
| `decompose` | Gumagawa ng mga subtask (na may mga nakatalagang agent), inililipat ang parent sa `doing` |
| `clarify` | Nagdadagdag ng tanong bilang komento, nananatili ang task sa `backlog` |

**Fast-path**: Ang mga task na mayroon nang nakatalagang agent at walang blocking na dependency ay lubusang nilalaktawan ang LLM triage at agad na ino-promote sa `todo`.

### Auto-Assignment

Kapang ang isang `todo` na task ay walang nakatalagang agent, awtomatiko itong itinatalaga ng dispatcher sa `defaultAgent` (nako-configure, default: `ruri`). Pinipigilan nito ang mga task mula sa tahimik na pagkastuck. Ang tipikal na daloy:

1. Nagawang task na walang nakatalagang agent → pumapasok sa `backlog`
2. Ino-promote ng triage sa `todo` (na may o walang nakatalagang agent)
3. Kung hindi nagtalaga ang triage → itinalaga ng dispatcher ang `defaultAgent`
4. Ang task ay normal na isinasagawa

### Configuration

Idagdag sa `config.json`:

```json
{
  "taskBoard": {
    "enabled": true,
    "maxRetries": 3,
    "requireReview": true,
    "defaultWorkflow": "",
    "gitCommit": true,
    "gitPush": true,
    "gitPR": true,
    "gitWorktree": true,
    "autoDispatch": {
      "enabled": true,
      "interval": "5m",
      "maxConcurrentTasks": 3,
      "defaultAgent": "kokuyou",
      "backlogAgent": "ruri",
      "reviewAgent": "ruri",
      "escalateAssignee": "takuma",
      "stuckThreshold": "2h",
      "backlogTriageInterval": "1h",
      "reviewLoop": false,
      "maxBudget": 5.0,
      "defaultModel": ""
    }
  }
}
```

| Field | Default | Paglalarawan |
|---|---|---|
| `enabled` | `false` | Paganahin ang auto-dispatch loop |
| `interval` | `5m` | Gaano kadalas mag-scan para sa mga handang task |
| `maxConcurrentTasks` | `3` | Maximum na task na tumatakbo nang sabay-sabay |
| `defaultAgent` | `ruri` | Awtomatikong itinalaga sa mga `todo` na task na walang nakatalagang agent bago mag-dispatch |
| `backlogAgent` | `ruri` | Agent na nagsusuri at nagpo-promote ng mga backlog task |
| `reviewAgent` | `ruri` | Agent na nag-re-review ng natapos na output ng task |
| `escalateAssignee` | `takuma` | Kung sino ang itinatalaga kapang humingi ng human judgment ang auto-review |
| `stuckThreshold` | `2h` | Maximum na oras na maaaring manatili ang isang task sa `doing` bago i-reset |
| `backlogTriageInterval` | `1h` | Minimum na interval sa pagitan ng mga backlog triage run |
| `reviewLoop` | `false` | Paganahin ang Dev↔QA loop (ipatupad → i-review → ayusin, hanggang `maxRetries`) |
| `maxBudget` | walang limitasyon | Maximum na gastos bawat task sa USD |
| `defaultModel` | — | I-override ang model para sa lahat ng auto-dispatched na task |

---

## Slot Pressure

Pinipigilan ng slot pressure ang auto-dispatch na makonsumo ang lahat ng concurrency slot at magutom ang mga interactive na session (mensahe ng human chat, on-demand na dispatch).

Paganahin ito sa `config.json`:

```json
{
  "slotPressure": {
    "enabled": true,
    "reservedSlots": 2,
    "warnThreshold": 3,
    "nonInteractiveTimeout": "5m"
  }
}
```

| Field | Default | Paglalarawan |
|---|---|---|
| `reservedSlots` | `2` | Mga slot na hinahawakan para sa interactive na paggamit. Ang mga non-interactive na task ay dapat maghintay kung ang available na slot ay bumaba sa antas na ito. |
| `warnThreshold` | `3` | Nagpapaputok ng babala kapag bumaba ang available na slot sa antas na ito. Ang mensaheng "排程接近滿載" ay lalabas sa dashboard at mga notification channel. |
| `nonInteractiveTimeout` | `5m` | Gaano katagal maghihintay ang isang non-interactive na task para sa isang slot bago ito kanselahin. |

Ang mga interactive na pinagmulan (human chat, `tetora dispatch`, `tetora route`) ay palaging agad na nakakakuha ng mga slot. Ang mga background na pinagmulan (taskboard, cron) ay naghihintay kung mataas ang pressure.

---

## Git Integration

Kapang pinagana ang `gitCommit`, `gitPush`, at `gitPR`, ang dispatcher ay nagpapatakbo ng mga git operation pagkatapos matagumpay na makumpleto ang isang task.

**Ang pagpapangalan ng branch** ay kinokontrol ng `gitWorkflow.branchConvention`:

```json
{
  "taskBoard": {
    "gitWorkflow": {
      "branchConvention": "{type}/{agent}-{description}",
      "types": ["feat", "fix", "refactor", "chore"],
      "defaultType": "feat",
      "autoMerge": true
    }
  }
}
```

Ang default na template na `{type}/{agent}-{description}` ay gumagawa ng mga branch tulad ng `feat/kokuyou-add-rate-limiting`. Ang bahagi ng `{description}` ay nagmumula sa pamagat ng task (pinababa ang capitalization, pinalitan ang mga espasyo ng gitling, pinigilan sa 40 karakter).

Ang `type` field ng isang task ay nagtatakda ng prefix ng branch. Kung ang isang task ay walang uri, gagamitin ang `defaultType`.

**Ang Auto PR/MR** ay sumusuporta ng parehong GitHub (`gh`) at GitLab (`glab`). Ang binary na available sa `PATH` ay awtomatikong gagamitin.

---

## Worktree Mode

Kapang `gitWorktree: true`, ang bawat task ay tumatakbo sa isang isolated na git worktree sa halip na sa shared na working directory. Inaalis nito ang mga file conflict kapag maraming task ang sabay-sabay na nagpapatakbo sa parehong repository.

```
~/.tetora/runtime/worktrees/
  task-abc123/   ← isolated na kopya para sa task na ito
  task-def456/   ← isolated na kopya para sa task na ito
```

Sa pagkumpleto ng task:

- Kung `autoMerge: true` (default), ang worktree branch ay ime-merge pabalik sa `main` at aalisin ang worktree.
- Kung mabibigo ang merge, ang task ay lilipat sa `partial-done` na status. Ang worktree ay pinapanatili para sa manu-manong resolusyon.

Para mabawi mula sa `partial-done`:

```bash
# Suriin kung ano ang nangyari
tetora task show task-abc123 --full

# Manu-manong i-merge ang branch
git merge feat/kokuyou-add-rate-limiting

# Resolbahin ang mga conflict sa iyong editor, pagkatapos ay mag-commit
git add .
git commit -m "merge: feat/kokuyou-add-rate-limiting"

# Markahan bilang done
tetora task move task-abc123 --status=done
```

---

## Workflow Integration

Maaaring tumakbo ang mga task sa pamamagitan ng isang workflow pipeline sa halip na isang solong agent prompt. Ito ay kapaki-pakinabang kapang ang isang task ay nangangailangan ng maraming koordinadong hakbang (hal. research → implement → test → document).

Mag-assign ng workflow sa isang task:

```bash
# Itakda sa paglikha ng task
tetora task create \
  --title="Implement OAuth2 flow" \
  --workflow=engineering-pipeline \
  --assignee=kokuyou

# O i-update ang isang umiiral na task
tetora task update task-abc123 --workflow=engineering-pipeline
```

Para huwag paganahin ang board-level default na workflow para sa isang partikular na task:

```json
{ "workflow": "none" }
```

Ang isang board-level default na workflow ay inilalapat sa lahat ng auto-dispatched na task maliban kung na-override:

```json
{
  "taskBoard": {
    "defaultWorkflow": "engineering-pipeline"
  }
}
```

Ang `workflowRunId` field sa task ay nag-uugnay nito sa partikular na workflow execution, na makikita sa Workflows tab ng dashboard.

---

## Mga View ng Dashboard

Buksan ang dashboard sa `http://localhost:8991/dashboard` at pumunta sa **Taskboard** tab.

**Kanban board** — mga column para sa bawat status. Ipinapakita ng mga card ang pamagat, nakatalagang agent, priority badge, at gastos. I-drag para baguhin ang status.

**Task detail panel** — i-click ang anumang card para buksan. Nagpapakita ng:
- Buong paglalarawan at lahat ng komento (mga entry na spec, context, log)
- Link ng session (tumalon sa live worker terminal kung tumatakbo pa)
- Gastos, tagal, bilang ng retry
- Link ng workflow run kung naaangkop

**Diff review panel** — kapang `requireReview: true`, ang mga natapos na task ay lumalabas sa isang review queue. Nakikita ng mga reviewer ang diff ng mga pagbabago at maaaring mag-apruba o humingi ng mga pagbabago.

---

## Mga Tip

**Sukat ng task.** Panatilihing 30–90 minuto ang saklaw ng mga task. Ang mga task na masyadong malaki (multi-day na refactor) ay karaniwang nag-ti-timeout o gumagawa ng walang laman na output at minarkahan bilang failed. Hatiin ang mga ito sa mga subtask gamit ang `parentId` field.

**Mga limitasyon ng concurrent dispatch.** Ang `maxConcurrentTasks: 3` ay ligtas na default. Ang pagpapalaki nito nang higit sa bilang ng mga koneksyon sa API na pinapayagan ng iyong provider ay nagdudulot ng contention at timeout. Magsimula sa 3, taasan sa 5 lamang pagkatapos kumpirmahin ang matatag na gawi.

**Pagbawi sa partial-done.** Kung ang isang task ay pumapasok sa `partial-done`, matagumpay na nakumpleto ng agent ang trabaho nito — nabigo lamang ang hakbang na git merge. Resolbahin ang conflict nang manu-mano, pagkatapos ilipat ang task sa `done`. Ang gastos at data ng session ay naka-preserve.

**Paggamit ng `dependsOn`.** Ang mga task na may hindi pa natutupad na dependency ay nilalaktawan ng dispatcher hanggang maabot ng lahat ng nakalista na task ID ang `done` na status. Ang mga resulta ng mga upstream na task ay awtomatikong ini-inject sa prompt ng dependent na task sa ilalim ng "Previous Task Results".

**Backlog triage.** Binabasa ng `backlogAgent` ang bawat `backlog` task, sinusuri ang feasibility at priyoridad, at pinapalabas ang mga malinaw na task sa `todo`. Magsulat ng detalyadong paglalarawan at acceptance criteria sa iyong mga `backlog` task — ginagamit ng triage agent ang mga ito para magpasya kung i-promote o iwan ang isang task para sa human review.

**Mga retry at ang review loop.** Kapang `reviewLoop: false` (default), ang isang nabagsak na task ay susubukan ulit hanggang `maxRetries` na beses na may mga nakaraang log comment na ini-inject. Kapang `reviewLoop: true`, ang bawat pagpapatupad ay nire-review ng `reviewAgent` bago ituring na tapos — ang agent ay tumatanggap ng feedback at sumusubuk ulit kung may mga natuklasang isyu.
