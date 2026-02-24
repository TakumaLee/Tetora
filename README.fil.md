<p align="center">
  <img src="assets/banner.png" alt="Tetora — AI Agent Orchestrator" width="800">
</p>

[English](README.md) | [繁體中文](README.zh-TW.md) | [日本語](README.ja.md) | [한국어](README.ko.md) | [Bahasa Indonesia](README.id.md) | [ภาษาไทย](README.th.md) | **Filipino** | [Español](README.es.md) | [Français](README.fr.md) | [Deutsch](README.de.md)

<p align="center">
  <strong>Self-hosted na AI assistant platform na may multi-agent architecture.</strong>
</p>

Ang Tetora ay tumatakbo bilang isang Go binary na walang external dependency. Kumokonekta ito sa mga AI provider na ginagamit mo na, nag-i-integrate sa mga messaging platform na ginagamit ng iyong team, at iniimbak ang lahat ng data sa sarili mong hardware.

---

## Ano ang Tetora

Ang Tetora ay isang AI agent orchestrator na nagbibigay-daan sa iyo na mag-define ng maraming agent role -- bawat isa ay may sariling personalidad, system prompt, model, at tool access -- at makipag-ugnayan sa kanila sa pamamagitan ng mga chat platform, HTTP API, o command line.

**Mga pangunahing kakayahan:**

- **Multi-agent role** -- mag-define ng magkakaibang agent na may hiwalay na personalidad, budget, at tool permission
- **Multi-provider** -- Claude API, OpenAI, Gemini, at iba pa; palitan o pagsamahin nang malaya
- **Multi-platform** -- Telegram, Discord, Slack, Google Chat, LINE, Matrix, Teams, Signal, WhatsApp, iMessage
- **Cron job** -- mag-schedule ng mga paulit-ulit na gawain na may approval gate at notification
- **Knowledge base** -- magbigay ng mga dokumento sa mga agent para sa mas tumpak na mga sagot
- **Persistent memory** -- natatandaan ng mga agent ang konteksto sa mga session; pinag-isang memory layer na may consolidation
- **Suporta sa MCP** -- ikonekta ang mga Model Context Protocol server bilang tool provider
- **Skill at workflow** -- mapagsama-samang skill pack at multi-step workflow pipeline
- **Webhook** -- mag-trigger ng agent action mula sa mga external system
- **Pamamahala ng gastos** -- per-role at global na budget na may awtomatikong model downgrade
- **Data retention** -- nako-configure na cleanup policy bawat table, na may buong export at purge
- **Plugin** -- palawakin ang functionality sa pamamagitan ng external plugin process
- **Matalinong paalala, gawi, layunin, contact, pagsubaybay sa pananalapi, briefing, at marami pa**

---

## Mabilisang Pagsisimula

### Para sa mga engineer

```bash
# I-install ang pinakabagong release
. <(curl -fsSL https://raw.githubusercontent.com/TakumaLee/Tetora/main/install.sh)

# Patakbuhin ang setup wizard
tetora init

# I-verify na tama ang lahat ng configuration
tetora doctor

# Simulan ang daemon
tetora serve
```

### Para sa mga hindi engineer

1. Pumunta sa [Releases page](https://github.com/TakumaLee/Tetora/releases/latest)
2. I-download ang binary para sa iyong platform (hal. `tetora-darwin-arm64` para sa Apple Silicon Mac)
3. Ilipat ito sa isang directory sa iyong PATH at palitan ang pangalan nito ng `tetora`, o ilagay sa `~/.tetora/bin/`
4. Magbukas ng terminal at patakbuhin ang:
   ```
   tetora init
   tetora doctor
   tetora serve
   ```

---

## Mga Agent

Ang bawat Tetora agent ay higit pa sa isang chatbot -- may sarili itong pagkakakilanlan. Ang bawat agent (tinatawag na **role**) ay tinutukoy ng isang **soul file**: isang Markdown na dokumento na nagbibigay sa agent ng personalidad, kadalubhasaan, estilo ng komunikasyon, at mga alituntunin sa pag-uugali.

### Pag-define ng role

Ang mga role ay idinedeklara sa `config.json` sa ilalim ng `roles` key:

```json
{
  "roles": {
    "default": {
      "soulFile": "SOUL.md",
      "model": "sonnet",
      "description": "General-purpose assistant",
      "permissionMode": "acceptEdits"
    },
    "researcher": {
      "soulFile": "SOUL-researcher.md",
      "model": "opus",
      "description": "Deep research and analysis",
      "permissionMode": "plan"
    }
  }
}
```

### Soul file

Ang soul file ay nagsasabi sa agent kung *sino ito*. Ilagay ito sa workspace directory (`~/.tetora/workspace/` bilang default):

```markdown
# Koto — Soul File

## Identity
You are Koto, a thoughtful assistant who lives inside the Tetora system.
You speak in a warm, concise tone and prefer actionable advice.

## Expertise
- Software architecture and code review
- Technical writing and documentation

## Behavioral Guidelines
- Think step by step before answering
- Ask clarifying questions when the request is ambiguous
- Record important decisions in memory for future reference

## Output Format
- Start with a one-line summary
- Use bullet points for details
- End with next steps if applicable
```

### Pagsisimula

Ang `tetora init` ay gagabay sa iyo sa paggawa ng iyong unang role at awtomatikong bubuo ng panimulang soul file. Maaari mo itong i-edit anumang oras -- magkakabisa ang mga pagbabago sa susunod na session.

---

## Build mula sa Source

```bash
git clone https://github.com/TakumaLee/Tetora.git
cd tetora
make install
```

Binu-build nito ang binary at ini-install sa `~/.tetora/bin/tetora`. Siguraduhing nasa iyong `PATH` ang `~/.tetora/bin`.

Para patakbuhin ang test suite:

```bash
make test
```

---

## Mga Kinakailangan

| Kinakailangan | Detalye |
|---|---|
| **sqlite3** | Dapat available sa `PATH`. Ginagamit para sa lahat ng persistent storage. |
| **API key ng AI provider** | Hindi bababa sa isa: Claude API, OpenAI, Gemini, o anumang OpenAI-compatible na endpoint. |
| **Go 1.25+** | Kailangan lang kung magbu-build mula sa source. |

---

## Mga Sinusuportahang Platform

| Platform | Architecture | Katayuan |
|---|---|---|
| macOS | amd64, arm64 | Stable |
| Linux | amd64, arm64 | Stable |
| Windows | amd64 | Beta |

---

## Architecture

Lahat ng runtime data ay nasa ilalim ng `~/.tetora/`:

```
~/.tetora/
  config.json        Pangunahing configuration (provider, role, integration)
  jobs.json          Mga cron job definition
  history.db         SQLite database (kasaysayan, memory, session, embedding, ...)
  sessions/          Mga session file bawat agent
  knowledge/         Mga dokumento ng knowledge base
  logs/              Mga structured log file
  outputs/           Mga nabuong output file
  uploads/           Pansamantalang upload storage
  bin/               Naka-install na binary
```

Ang configuration ay gumagamit ng simpleng JSON na may suporta sa `$ENV_VAR` reference, kaya hindi na kailangang i-hardcode ang mga lihim. Ang setup wizard (`tetora init`) ay bumubuo ng gumaganang `config.json` nang interactive.

Sinusuportahan ang hot-reload: magpadala ng `SIGHUP` sa tumatakbong daemon para i-reload ang `config.json` nang walang downtime.

---

## Sanggunian sa CLI

| Utos | Paglalarawan |
|---|---|
| `tetora init` | Interactive na setup wizard |
| `tetora doctor` | Pagsusuri sa kalusugan at diagnostics |
| `tetora serve` | Simulan ang daemon (chat bot + HTTP API + cron) |
| `tetora run --file tasks.json` | Magpatakbo ng gawain mula sa JSON file (CLI mode) |
| `tetora dispatch "Summarize this"` | Magpatakbo ng ad-hoc na gawain sa pamamagitan ng daemon |
| `tetora route "Review code security"` | Matalinong dispatch -- awtomatikong i-route sa pinakaangkop na role |
| `tetora status` | Mabilisang pangkalahatang-tanaw ng daemon, job, at gastos |
| `tetora job list` | Ilista ang lahat ng cron job |
| `tetora job trigger <name>` | Manual na i-trigger ang isang cron job |
| `tetora role list` | Ilista ang lahat ng naka-configure na role |
| `tetora role show <name>` | Ipakita ang detalye ng role at preview ng soul |
| `tetora history list` | Ipakita ang kamakailang kasaysayan ng pagpapatakbo |
| `tetora history cost` | Ipakita ang buod ng gastos |
| `tetora session list` | Ilista ang mga kamakailang session |
| `tetora memory list` | Ilista ang mga memory entry ng agent |
| `tetora knowledge list` | Ilista ang mga dokumento ng knowledge base |
| `tetora skill list` | Ilista ang mga available na skill |
| `tetora workflow list` | Ilista ang mga naka-configure na workflow |
| `tetora mcp list` | Ilista ang mga koneksyon sa MCP server |
| `tetora budget show` | Ipakita ang katayuan ng budget |
| `tetora config show` | Ipakita ang kasalukuyang configuration |
| `tetora config validate` | I-validate ang config.json |
| `tetora backup` | Gumawa ng backup archive |
| `tetora restore <file>` | Mag-restore mula sa backup archive |
| `tetora dashboard` | Buksan ang web dashboard sa browser |
| `tetora logs` | Tingnan ang daemon log (`-f` para sundan, `--json` para sa structured output) |
| `tetora data status` | Ipakita ang katayuan ng data retention |
| `tetora service install` | I-install bilang launchd service (macOS) |
| `tetora completion <shell>` | Bumuo ng shell completion (bash, zsh, fish) |
| `tetora version` | Ipakita ang bersyon |

Patakbuhin ang `tetora help` para sa buong sanggunian ng mga utos.

---

## Pag-ambag

Malugod na tinatanggap ang mga kontribusyon. Mangyaring mag-bukas ng issue para talakayin ang malalaking pagbabago bago magsumite ng pull request.

- **Issues**: [github.com/TakumaLee/Tetora/issues](https://github.com/TakumaLee/Tetora/issues)
- **Mga Talakayan**: [github.com/TakumaLee/Tetora/discussions](https://github.com/TakumaLee/Tetora/discussions)

Ang proyektong ito ay lisensyado sa ilalim ng AGPL-3.0, na nangangailangan na ang mga derivative work at network-accessible deployment ay open source din sa ilalim ng parehong lisensya. Mangyaring suriin ang lisensya bago mag-ambag.

---

## Lisensya

[AGPL-3.0](https://www.gnu.org/licenses/agpl-3.0.html)

Copyright (c) Mga kontribyutor ng Tetora.
