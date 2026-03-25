---
title: "Gabay sa Pag-troubleshoot"
lang: "fil"
order: 7
description: "Common issues and solutions for Tetora setup and operation."
---
# Gabay sa Pag-troubleshoot

Sinasaklaw ng gabay na ito ang mga pinakakaraniwang isyu na naranasan kapag nagpapatakbo ng Tetora. Para sa bawat isyu, ang pinaka-malamang na sanhi ay nakalista muna.

---

## tetora doctor

Laging magsimula dito. Patakbuhin ang `tetora doctor` pagkatapos ng pag-install o kapang huminto ang isang bagay sa paggana:

```
=== Tetora Doctor ===

  ✓ Config          /Users/you/.tetora/config.json
  ✓ Claude CLI      claude 1.2.3
  ✓ Provider        claude-cli
  ✓ Port            localhost:8991 in use (daemon running)
  ✓ Telegram        enabled (chatID=123456)
  ✓ Jobs            jobs.json (4 jobs, 3 enabled)
  ✓ History DB      12 tasks
  ✓ Workdir         /Users/you/dev
  ✓ Agent/ruri      Commander
  ✓ Binary          /Users/you/.tetora/bin/tetora
  ✓ Encryption      key configured
  ✓ ffmpeg          available
  ✓ sqlite3         available
  ✓ Agents Dir      /Users/you/.tetora/agents (3 agents)
  ✓ Workspace       /Users/you/.tetora/workspace

All checks passed.
```

Ang bawat linya ay isang tseke. Ang pulang `✗` ay nangangahulugang kritikal na pagkabigo (hindi gagana ang daemon nang hindi ito naayos). Ang dilaw na `~` ay nangangahulugang isang mungkahi (opsyonal ngunit inirerekomenda).

Mga karaniwang paraan ng pag-aayos para sa mga nabagsak na tseke:

| Nabagsak na tseke | Paraan ng pag-aayos |
|---|---|
| `Config: not found` | Patakbuhin ang `tetora init` |
| `Claude CLI: not found` | Itakda ang `claudePath` sa `config.json` o i-install ang Claude Code |
| `sqlite3: not found` | `brew install sqlite3` (macOS) o `apt install sqlite3` (Linux) |
| `Agent/name: soul file missing` | Gumawa ng `~/.tetora/agents/{name}/SOUL.md` o patakbuhin ang `tetora init` |
| `Workspace: not found` | Patakbuhin ang `tetora init` para gumawa ng istruktura ng directory |

---

## "session produced no output"

Natapos ang isang task ngunit walang laman ang output. Ang task ay awtomatikong minarkahang `failed`.

**Sanhi 1: Masyadong malaki ang context window.** Ang prompt na ini-inject sa session ay lumampas sa limitasyon ng context ng model. Agad na lumalabas ang Claude Code kapang hindi nito maisama ang context.

Paraan ng pag-aayos: Paganahin ang session compaction sa `config.json`:

```json
{
  "sessionCompaction": {
    "enabled": true,
    "tokenThreshold": 150000,
    "messageThreshold": 100,
    "strategy": "auto"
  }
}
```

O bawasan ang dami ng context na ini-inject sa task (mas maikling paglalarawan, mas kaunting spec comment, mas maliit na `dependsOn` chain).

**Sanhi 2: Pagkabigo sa startup ng Claude Code CLI.** Ang binary sa `claudePath` ay nag-crash sa startup — kadalasan ay dahil sa masamang API key, isyu sa network, o hindi tugmang bersyon.

Paraan ng pag-aayos: Manu-manong patakbuhin ang Claude Code binary para makita ang error:

```bash
/usr/local/bin/claude --version
/usr/local/bin/claude -p "hello"
```

Ayusin ang iniulat na error, pagkatapos ay subukang ulit ang task:

```bash
tetora task move task-abc123 --status=todo
```

**Sanhi 3: Walang laman na prompt.** Ang task ay may pamagat ngunit walang paglalarawan, at ang pamagat lamang ay masyadong malabo para kumilos ang agent. Ang session ay tumatakbo, gumagawa ng output na hindi nakakatugon sa empty-check, at minabibilangan.

Paraan ng pag-aayos: Magdagdag ng kongkretong paglalarawan:

```bash
tetora task update task-abc123 \
  --description="Create src/ratelimit/bucket.go with a token bucket implementation..."
```

---

## Mga error na "unauthorized" sa dashboard

Ang dashboard ay nagbabalik ng 401 o nagpapakita ng blangkong pahina pagkatapos i-reload.

**Sanhi 1: Na-cache ng Service Worker ang lumang auth token.** Kine-cache ng PWA Service Worker ang mga tugon kasama ang mga auth header. Pagkatapos ng pag-restart ng daemon na may bagong token, ang naka-cache na bersyon ay luma na.

Paraan ng pag-aayos: I-hard refresh ang pahina. Sa Chrome/Safari:

- Mac: `Cmd + Shift + R`
- Windows/Linux: `Ctrl + Shift + R`

O buksan ang DevTools → Application → Service Workers → i-click ang "Unregister", pagkatapos i-reload.

**Sanhi 2: Hindi tugma ang Referer header.** Ang auth middleware ng dashboard ay nagva-validate ng `Referer` header. Ang mga request mula sa mga browser extension, proxy, o curl nang walang `Referer` header ay tinatanggihan.

Paraan ng pag-aayos: I-access ang dashboard nang direkta sa `http://localhost:8991/dashboard`, hindi sa pamamagitan ng proxy. Kung kailangan mo ng API access mula sa mga external na tool, gumamit ng API token sa halip na browser session auth.

---

## Hindi nag-a-update ang dashboard

Nag-lo-load ang dashboard ngunit nananatiling luma ang activity feed, listahan ng worker, o task board.

**Sanhi: Hindi tugma ang bersyon ng Service Worker.** Nagse-serve ang PWA Service Worker ng naka-cache na bersyon ng dashboard JS/HTML kahit pagkatapos ng `make bump` na pag-update.

Paraan ng pag-aayos:

1. I-hard refresh (`Cmd + Shift + R` / `Ctrl + Shift + R`)
2. Kung hindi gumagana iyon, buksan ang DevTools → Application → Service Workers → i-click ang "Update" o "Unregister"
3. I-reload ang pahina

**Sanhi: Naputol ang SSE connection.** Nakakatanggap ang dashboard ng mga live na update sa pamamagitan ng Server-Sent Events. Kung naputol ang koneksyon (network hiccup, natulog ang laptop), titigil ang pag-update ng feed.

Paraan ng pag-aayos: I-reload ang pahina. Awtomatikong muling naitatag ang SSE connection sa pag-load ng pahina.

---

## Babala na "排程接近滿載"

Nakikita mo ang mensaheng ito sa Discord/Telegram o sa notification feed ng dashboard.

Ito ang babala ng slot pressure. Nagpapaputok ito kapang bumaba ang mga available na concurrency slot sa o mas mababa sa `warnThreshold` (default: 3). Nangangahulugang halos puno ang kapasidad ng Tetora.

**Ano ang dapat gawin:**

- Kung ito ay inaasahan (maraming task na tumatakbo): hindi na kailangang kumilos. Ang babala ay pang-impormasyon.
- Kung hindi ka nagpapatakbo ng maraming task: suriin ang mga naka-stuck na task sa status na `doing`:

```bash
tetora task list --status=doing
```

- Kung gusto mong taasan ang kapasidad: taasan ang `maxConcurrent` sa `config.json` at i-adjust ang `slotPressure.warnThreshold` nang naaangkop.
- Kung naaantala ang mga interactive na session: taasan ang `slotPressure.reservedSlots` para mas maraming slot ang maitagal para sa interactive na paggamit.

---

## Mga task na naka-stuck sa "doing"

Ipinapakita ng isang task ang `status=doing` ngunit walang aktibong nagtatrabahong agent dito.

**Sanhi 1: Na-restart ang daemon habang tumatakbo ang task.** Ang task ay tumatakbo nang patayin ang daemon. Sa susunod na startup, sinusuri ng Tetora ang mga inulilang `doing` na task at alinman ay inire-restore ang mga ito sa `done` (kung may ebidensya ng gastos/tagal) o iri-reset sa `todo`.

Awtomatiko ito — hintayin ang susunod na startup ng daemon. Kung tumatakbo na ang daemon at naka-stuck pa rin ang task, ang heartbeat o stuck-task reset ay makukuha ito sa loob ng `stuckThreshold` (default: 2h).

Para pumilit ng reset kaagad:

```bash
tetora task move task-abc123 --status=todo
```

**Sanhi 2: Heartbeat/stall detection.** Sinusuri ng heartbeat monitor (`heartbeat.go`) ang mga tumatakbong session. Kung ang isang session ay walang output sa loob ng stall threshold, awtomatiko itong kinakansela at inililipat ang task sa `failed`.

Suriin ang mga komento ng task para sa mga `[auto-reset]` o `[stall-detected]` na system comment:

```bash
tetora task show task-abc123 --full
```

**Manu-manong kanselahin sa pamamagitan ng API:**

```bash
curl -X POST http://localhost:8991/api/tasks/task-abc123/cancel
```

---

## Mga pagkabigo sa worktree merge

Natapos ang isang task at lumipat sa `partial-done` na may komentong `[worktree] merge failed`.

Nangangahulugan ito na ang mga pagbabago ng agent sa task branch ay nagko-conflict sa `main`.

**Mga hakbang sa pagbawi:**

```bash
# Tingnan ang mga detalye ng task at kung aling branch ang ginawa
tetora task show task-abc123 --full

# Pumunta sa project repo
cd /path/to/your/repo

# Manu-manong i-merge ang branch
git merge feat/kokuyou-task-abc123

# Resolbahin ang mga conflict sa iyong editor, pagkatapos ay mag-commit
git add .
git commit -m "merge: feat/kokuyou-task-abc123"

# Markahan ang task bilang done
tetora task move task-abc123 --status=done
```

Ang worktree directory ay naka-preserve sa `~/.tetora/runtime/worktrees/task-abc123/` hanggang manu-mano mo itong linisin o ilipat ang task sa `done`.

---

## Mataas na gastos sa token

Gumagamit ng mas maraming token kaysa inaasahan ang mga session.

**Sanhi 1: Hindi niko-compact ang context.** Nang walang session compaction, ang bawat turn ay nag-aapon ng buong kasaysayan ng pag-uusap. Ang mga matagalang task (maraming tool call) ay linear na lumalaki ang context.

Paraan ng pag-aayos: Paganahin ang `sessionCompaction` (tingnan ang seksyon na "session produced no output" sa itaas).

**Sanhi 2: Malalaking knowledge base o rule file.** Ang mga file sa `workspace/rules/` at `workspace/knowledge/` ay ini-inject sa bawat agent prompt. Kung malaki ang mga file na ito, nagko-konsumo sila ng token sa bawat tawag.

Paraan ng pag-aayos:
- I-audit ang `workspace/knowledge/` — panatilihing mas mababa sa 50 KB ang mga indibidwal na file.
- Ilipat ang reference material na bihira mong kailangan labas ng mga auto-inject path.
- Patakbuhin ang `tetora knowledge list` para makita kung ano ang ini-inject at ang laki nito.

**Sanhi 3: Mali ang routing ng model.** Ang isang mahal na model (Opus) ay ginagamit para sa mga karaniwang task.

Paraan ng pag-aayos: Suriin ang `defaultModel` sa config ng agent at magtakda ng mas murang default para sa mga bulk na task:

```json
{
  "taskBoard": {
    "autoDispatch": {
      "defaultModel": "sonnet"
    }
  }
}
```

---

## Mga error sa provider timeout

Nabibigo ang mga task na may mga timeout error tulad ng `context deadline exceeded` o `provider request timed out`.

**Sanhi 1: Masyadong maikli ang task timeout.** Ang default na timeout ay maaaring masyadong maikli para sa mga kumplikadong task.

Paraan ng pag-aayos: Magtakda ng mas mahabang timeout sa agent config ng task o bawat-task:

```json
{
  "roles": {
    "kokuyou": {
      "timeout": "60m"
    }
  }
}
```

O taasan ang pagtatantya ng LLM timeout sa pamamagitan ng pagdaragdag ng mas maraming detalye sa paglalarawan ng task (ginagamit ng Tetora ang paglalarawan para tantyahin ang timeout sa pamamagitan ng mabilis na model call).

**Sanhi 2: API rate limiting o contention.** Masyadong maraming concurrent na request na tumutama sa parehong provider.

Paraan ng pag-aayos: Bawasan ang `maxConcurrentTasks` o magdagdag ng `maxBudget` para mapigilan ang mga mahal na task:

```json
{
  "autoDispatch": {
    "maxConcurrentTasks": 2,
    "maxBudget": 3.0
  }
}
```

---

## `make bump` na nagnterminate ng workflow

Nagpatakbo ka ng `make bump` habang nagpapatakbo ng workflow o task. Na-restart ang daemon habang nasa kalagitnaan ng task.

Ang pag-restart ay nagti-trigger ng orphan recovery logic ng Tetora:

- Ang mga task na may ebidensya ng pagkumpleto (naitala ang gastos, naitala ang tagal) ay ini-restore sa `done`.
- Ang mga task na walang ebidensya ng pagkumpleto ngunit nakalipas na sa grace period (2 minuto) ay ini-reset sa `todo` para sa muling pag-dispatch.
- Ang mga task na na-update sa loob ng huling 2 minuto ay hindi ginagalaw hanggang sa susunod na stuck-task scan.

**Para suriin kung ano ang nangyari:**

```bash
tetora task list --status=doing
tetora task list --status=failed
```

Suriin ang mga komento ng task para sa mga `[auto-restore]` o `[auto-reset]` na entry.

**Kung kailangan mong pigilan ang mga bump habang aktibo ang mga task** (hindi pa available bilang flag), suriin na walang tumatakbong task bago mag-bump:

```bash
tetora task list --status=doing
# Kung walang laman, ligtas na mag-bump
make bump
```

---

## Mga error sa SQLite

Nakikita mo ang mga error tulad ng `database is locked`, `SQLITE_BUSY`, o `index.lock` sa mga log.

**Sanhi 1: Nawawalang WAL mode pragma.** Nang walang WAL mode, gumagamit ang SQLite ng eksklusibong file locking, na nagdudulot ng `database is locked` sa ilalim ng mga concurrent na read/write.

Lahat ng Tetora DB call ay dumadaan sa `queryDB()` at `execDB()` na nagdadagdag ng `PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;`. Kung direkta mong tinatawagan ang sqlite3 sa mga script, idagdag ang mga pragma na ito:

```bash
sqlite3 ~/.tetora/history.db \
  "PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; SELECT count(*) FROM tasks;"
```

**Sanhi 2: Lumang `index.lock` file.** Iniiiwan ng mga git operation ang `index.lock` kapag nainterrupt. Sinusuri ng worktree manager ang mga lumang lock bago simulan ang git work, ngunit maaaring mag-iwan ng isa ang isang crash.

Paraan ng pag-aayos:

```bash
# Hanapin ang mga lumang lock file
find ~/.tetora/runtime/worktrees -name "index.lock"

# Alisin ang mga ito (lamang kung walang aktibong tumatakbong git operation)
rm /path/to/repo/.git/index.lock
```

---

## Hindi sumasagot ang Discord / Telegram

Ang mga mensahe sa bot ay walang tugon.

**Sanhi 1: Mali ang configuration ng channel.** Ang Discord ay may dalawang listahan ng channel: `channelIDs` (direktang sumasagot sa lahat ng mensahe) at `mentionChannelIDs` (sumasagot lamang kapang @-binanggit). Kung ang isang channel ay wala sa alinmang listahan, ang mga mensahe ay hindi papansinin.

Paraan ng pag-aayos: Suriin ang `config.json`:

```json
{
  "discord": {
    "enabled": true,
    "channelIDs": ["123456789012345678"],
    "mentionChannelIDs": []
  }
}
```

**Sanhi 2: Nag-expire o mali ang bot token.** Ang mga Telegram bot token ay hindi nag-e-expire, ngunit ang mga Discord token ay maaaring ma-invalidate kung ang bot ay inalis sa server o muling nagawa ang token.

Paraan ng pag-aayos: Muling gumawa ng bot token sa Discord developer portal at i-update ang `config.json`.

**Sanhi 3: Hindi tumatakbo ang daemon.** Ang bot gateway ay aktibo lamang kapang tumatakbo ang `tetora serve`.

Paraan ng pag-aayos:

```bash
tetora status
tetora serve   # kung hindi tumatakbo
```

---

## Mga error sa glab / gh CLI

Nabibigo ang git integration na may mga error mula sa `glab` o `gh`.

**Karaniwang error: `gh: command not found`**

Paraan ng pag-aayos:
```bash
brew install gh      # macOS
gh auth login        # mag-authenticate
```

**Karaniwang error: `glab: You are not logged in`**

Paraan ng pag-aayos:
```bash
brew install glab    # macOS
glab auth login      # mag-authenticate sa iyong GitLab instance
```

**Karaniwang error: `remote: HTTP Basic: Access denied`**

Paraan ng pag-aayos: Suriin na ang iyong SSH key o HTTPS credential ay na-configure para sa repository host. Para sa GitLab:

```bash
glab auth status
ssh -T git@gitlab.com   # subukan ang SSH connectivity
```

Para sa GitHub:

```bash
gh auth status
ssh -T git@github.com
```

**Matagumpay ang paglikha ng PR/MR ngunit nagtuturo sa maling base branch**

Sa default, ang mga PR ay nagtutukoy sa default branch ng repository (`main` o `master`). Kung ang iyong workflow ay gumagamit ng ibang base, itakda ito nang malinaw sa iyong post-task git configuration o tiyaking ang default branch ng repository ay na-configure nang tama sa hosting platform.
