<p align="center">
  <img src="assets/banner.png" alt="Tetora — KI-Agenten-Orchestrator" width="800">
</p>

[English](README.md) | [繁體中文](README.zh-TW.md) | [日本語](README.ja.md) | [한국어](README.ko.md) | [Bahasa Indonesia](README.id.md) | [ภาษาไทย](README.th.md) | [Filipino](README.fil.md) | [Español](README.es.md) | [Français](README.fr.md) | **Deutsch**

<p align="center">
  <strong>Selbstgehostete KI-Assistenzplattform mit Multi-Agenten-Architektur.</strong>
</p>

Tetora läuft als einzelne Go-Binary ohne externe Abhängigkeiten. Es verbindet sich mit den KI-Anbietern, die du bereits nutzt, integriert sich in die Messaging-Plattformen deines Teams und speichert alle Daten auf deiner eigenen Hardware.

---

## Was ist Tetora

Tetora ist ein KI-Agenten-Orchestrator, mit dem du mehrere Agentenrollen definieren kannst -- jede mit eigener Persönlichkeit, System-Prompt, Modell und Werkzeugzugang -- und über Chat-Plattformen, HTTP-APIs oder die Kommandozeile mit ihnen interagieren kannst.

**Kernfunktionen:**

- **Multi-Agenten-Rollen** -- definiere unterschiedliche Agenten mit separaten Persönlichkeiten, Budgets und Werkzeugberechtigungen
- **Multi-Anbieter** -- Claude API, OpenAI, Gemini und mehr; tausche oder kombiniere frei
- **Multi-Plattform** -- Telegram, Discord, Slack, Google Chat, LINE, Matrix, Teams, Signal, WhatsApp, iMessage
- **Cron Jobs** -- plane wiederkehrende Aufgaben mit Genehmigungsstufen und Benachrichtigungen
- **Wissensdatenbank** -- stelle Agenten Dokumente für fundierte Antworten bereit
- **Persistenter Speicher** -- Agenten merken sich den Kontext über Sitzungen hinweg; einheitliche Speicherschicht mit Konsolidierung
- **MCP-Unterstützung** -- verbinde Model Context Protocol Server als Werkzeuganbieter
- **Skills und Workflows** -- zusammensetzbare Skill-Pakete und mehrstufige Workflow-Pipelines
- **Web-Dashboard** -- CEO-Kommandozentrale mit ROI-Metriken, Pixel-Büro und Live-Aktivitätsfeed
- **Workflow-Engine** -- DAG-basierte Pipeline-Ausführung mit Bedingungsverzweigungen, parallelen Schritten, Wiederholungslogik und dynamischem Modell-Routing (Sonnet für Routineaufgaben, Opus für komplexe)
- **Template-Marktplatz** -- Store-Tab zum Durchsuchen, Importieren und Exportieren von Workflow-Vorlagen
- **Taskboard Auto-Dispatch** -- Kanban-Board mit automatischer Aufgabenzuweisung, konfigurierbaren gleichzeitigen Slots und Slot-Drucksystem, das Kapazität für interaktive Sitzungen reserviert
- **GitLab MR + GitHub PR** -- automatische PR/MR-Erstellung nach Workflow-Abschluss; automatische Erkennung des Remote-Hosts
- **Sitzungskomprimierung** -- automatische Kontextkompression basierend auf Token- und Nachrichtenanzahl, um Sitzungen innerhalb der Modellgrenzen zu halten
- **Service Worker PWA** -- Offline-fähiges Dashboard mit intelligentem Caching
- **Teilweise-Erledigt-Status** -- Aufgaben, die abgeschlossen werden, aber beim Nachbearbeiten (Git-Merge, Review) scheitern, gehen in einen wiederherstellbaren Zwischenzustand über, statt verloren zu gehen
- **Webhooks** -- löse Agentenaktionen von externen Systemen aus
- **Kostensteuerung** -- Budgets pro Rolle und global mit automatischem Modell-Downgrade
- **Datenaufbewahrung** -- konfigurierbare Bereinigungsrichtlinien pro Tabelle, mit vollständigem Export und Löschung
- **Plugins** -- erweitere die Funktionalität über externe Plugin-Prozesse
- **Intelligente Erinnerungen, Gewohnheiten, Ziele, Kontakte, Finanzverfolgung, Briefings und mehr**

---

## Schnellstart

### Für Entwickler

```bash
# Die neueste Version installieren
. <(curl -fsSL https://raw.githubusercontent.com/TakumaLee/Tetora/main/install.sh)

# Den Einrichtungsassistenten starten
tetora init

# Prüfen, ob alles korrekt konfiguriert ist
tetora doctor

# Den Daemon starten
tetora serve
```

### Für Nicht-Entwickler

1. Gehe zur [Releases-Seite](https://github.com/TakumaLee/Tetora/releases/latest)
2. Lade die Binary für deine Plattform herunter (z.B. `tetora-darwin-arm64` für Apple Silicon Mac)
3. Verschiebe sie in ein Verzeichnis in deinem PATH und benenne sie in `tetora` um, oder lege sie in `~/.tetora/bin/` ab
4. Öffne ein Terminal und führe aus:
   ```
   tetora init
   tetora doctor
   tetora serve
   ```

---

## Agenten

Jeder Tetora-Agent ist mehr als ein Chatbot -- er hat eine Identität. Jeder Agent (genannt **Rolle**) wird durch eine **Soul-Datei** definiert: ein Markdown-Dokument, das dem Agenten seine Persönlichkeit, Expertise, Kommunikationsstil und Verhaltensrichtlinien verleiht.

### Eine Rolle definieren

Rollen werden in `config.json` unter dem Schlüssel `roles` deklariert:

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

### Soul-Dateien

Eine Soul-Datei teilt dem Agenten mit, *wer er ist*. Lege sie im Workspace-Verzeichnis ab (`~/.tetora/workspace/` standardmäßig):

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

### Erste Schritte

`tetora init` führt dich durch die Erstellung deiner ersten Rolle und generiert automatisch eine Starter-Soul-Datei. Du kannst sie jederzeit bearbeiten -- Änderungen werden in der nächsten Sitzung wirksam.

---

## Dashboard

Tetora enthält ein integriertes Web-Dashboard unter `http://localhost:8991/dashboard`. Es ist in vier Zonen unterteilt:

| Zone | Inhalt |
|------|----------|
| **Kommandozentrale** | Executive Summary (ROI-Karten), Pixel-Team-Sprites, erweiterbares Agent World-Büro |
| **Betrieb** | Kompakte Ops-Leiste, Agenten-Scorecard + Live-Aktivitätsfeed (nebeneinander), laufende Aufgaben |
| **Einblicke** | 7-Tage-Trenddiagramm, historische Aufgabendurchsatz- und Kostendiagramme |
| **Engineering-Details** | Kosten-Dashboard, Cron Jobs, Sitzungen, Anbieter-Gesundheit, Vertrauen, SLA, Versionshistorie, Routing, Speicher und mehr (einklappbar) |

Der Agenten-Editor enthält einen **anbieterbewussten Modellwähler** mit Ein-Klick-Wechsel zwischen Cloud- und lokalen Modellen (Ollama). Ein globaler **Inferenzmodus-Umschalter** ermöglicht es, alle Agenten mit einem einzigen Button zwischen Cloud und lokal umzuschalten. Jede Agentenkarte zeigt ein Cloud/Local-Badge und ein Schnellwechsel-Dropdown.

Mehrere Themes sind verfügbar (Glass, Clean, Material, Boardroom, Retro). Das Agent World Pixel-Büro kann mit Dekorationen und Zoom-Steuerung angepasst werden.

```bash
# Das Dashboard im Standardbrowser öffnen
tetora dashboard
```

---

## Discord-Befehle

Tetora reagiert auf `!`-Präfix-Befehle in Discord:

| Befehl | Beschreibung |
|---------|-------------|
| `!model` | Alle Agenten nach Cloud / Local gruppiert anzeigen |
| `!model pick [agent]` | Interaktiver Modellwähler (Buttons + Dropdowns) |
| `!model <model> [agent]` | Modell direkt setzen (automatische Anbietererkennung) |
| `!local [agent]` | Zu lokalen Modellen (Ollama) wechseln |
| `!cloud [agent]` | Cloud-Modelle wiederherstellen |
| `!mode` | Inferenzmodus-Zusammenfassung mit Umschaltbuttons |
| `!chat <agent>` | Kanal auf einen bestimmten Agenten sperren |
| `!end` | Kanal entsperren, intelligente Verteilung fortsetzen |
| `!new` | Neue Sitzung starten |
| `!ask <prompt>` | Einmalige Frage |
| `!cancel` | Alle laufenden Aufgaben abbrechen |
| `!approve [tool\|reset]` | Auto-genehmigte Werkzeuge verwalten |
| `!status` / `!cost` / `!jobs` | Betriebsübersicht |
| `!help` | Befehlsreferenz anzeigen |
| `@Tetora <text>` | Intelligente Verteilung an den besten Agenten |

**[Vollständige Discord-Befehlsreferenz](docs/discord-commands.md)** -- Modellwechsel, Remote/Lokal-Umschaltung, Anbieterkonfiguration und mehr.

---

## Aus dem Quellcode kompilieren

```bash
git clone https://github.com/TakumaLee/Tetora.git
cd tetora
make install
```

Dies kompiliert die Binary und installiert sie nach `~/.tetora/bin/tetora`. Stelle sicher, dass `~/.tetora/bin` in deinem `PATH` enthalten ist.

Um die Testsuite auszuführen:

```bash
make test
```

---

## Voraussetzungen

| Voraussetzung | Details |
|---|---|
| **sqlite3** | Muss im `PATH` verfügbar sein. Wird für die gesamte persistente Datenspeicherung verwendet. |
| **KI-Anbieter-API-Schlüssel** | Mindestens einer: Claude API, OpenAI, Gemini oder ein beliebiger OpenAI-kompatibler Endpunkt. |
| **Go 1.25+** | Nur erforderlich beim Kompilieren aus dem Quellcode. |

---

## Unterstützte Plattformen

| Plattform | Architekturen | Status |
|---|---|---|
| macOS | amd64, arm64 | Stabil |
| Linux | amd64, arm64 | Stabil |
| Windows | amd64 | Beta |

---

## Architektur

Alle Laufzeitdaten befinden sich unter `~/.tetora/`:

```
~/.tetora/
  config.json        Hauptkonfiguration (Anbieter, Rollen, Integrationen)
  jobs.json          Cron-Job-Definitionen
  history.db         SQLite-Datenbank (Verlauf, Speicher, Sitzungen, Embeddings, ...)
  bin/               Installierte Binary
  agents/            Soul-Dateien pro Agent (agents/{name}/SOUL.md)
  workspace/
    rules/           Governance-Regeln, automatisch in alle Agenten-Prompts injiziert
    memory/          Geteilte Beobachtungen, von jedem Agenten les-/schreibbar
    knowledge/       Referenzdokumente (automatisch injiziert bis 50 KB)
    skills/          Wiederverwendbare Prozeduren, geladen durch Prompt-Matching
    tasks/           Aufgabendateien und TODO-Listen
  runtime/
    sessions/        Sitzungsdateien pro Agent
    outputs/         Generierte Ausgabedateien
    logs/            Strukturierte Logdateien
    cache/           Temporärer Cache
```

Die Konfiguration verwendet reines JSON mit Unterstützung für `$ENV_VAR`-Referenzen, sodass Geheimnisse nie hartcodiert werden müssen. Der Einrichtungsassistent (`tetora init`) generiert interaktiv eine funktionsfähige `config.json`.

Hot-Reload wird unterstützt: Sende `SIGHUP` an den laufenden Daemon, um `config.json` ohne Ausfallzeit neu zu laden.

---

## Workflows

Tetora enthält eine integrierte Workflow-Engine zur Orchestrierung von mehrstufigen Aufgaben mit mehreren Agenten. Definiere deine Pipeline in JSON und lass die Agenten automatisch zusammenarbeiten.

**[Vollständige Workflow-Dokumentation](docs/workflow.de.md)** — Schritttypen, Variablen, Trigger, CLI- und API-Referenz.

Schnellbeispiel:

```bash
# Einen Workflow validieren und importieren
tetora workflow create examples/workflow-basic.json

# Ausführen
tetora workflow run research-and-summarize --var topic="LLM safety"

# Ergebnisse prüfen
tetora workflow status <run-id>
```

Unter [`examples/`](examples/) findest du gebrauchsfertige Workflow-JSON-Dateien.

---

## CLI-Referenz

| Befehl | Beschreibung |
|---|---|
| `tetora init` | Interaktiver Einrichtungsassistent |
| `tetora doctor` | Gesundheitsprüfungen und Diagnosen |
| `tetora serve` | Daemon starten (Chat-Bots + HTTP API + Cron) |
| `tetora run --file tasks.json` | Aufgaben aus einer JSON-Datei verteilen (CLI-Modus) |
| `tetora dispatch "Summarize this"` | Eine Ad-hoc-Aufgabe über den Daemon ausführen |
| `tetora route "Review code security"` | Intelligente Verteilung -- automatisches Routing zur besten Rolle |
| `tetora status` | Schnellübersicht über Daemon, Jobs und Kosten |
| `tetora job list` | Alle Cron Jobs auflisten |
| `tetora job trigger <name>` | Einen Cron Job manuell auslösen |
| `tetora role list` | Alle konfigurierten Rollen auflisten |
| `tetora role show <name>` | Rollendetails und Soul-Vorschau anzeigen |
| `tetora history list` | Aktuellen Ausführungsverlauf anzeigen |
| `tetora history cost` | Kostenübersicht anzeigen |
| `tetora session list` | Aktuelle Sitzungen auflisten |
| `tetora memory list` | Speichereinträge des Agenten auflisten |
| `tetora knowledge list` | Dokumente der Wissensdatenbank auflisten |
| `tetora skill list` | Verfügbare Skills auflisten |
| `tetora workflow list` | Konfigurierte Workflows auflisten |
| `tetora workflow run <name>` | Einen Workflow ausführen (`--var key=value` für Variablen) |
| `tetora workflow status <run-id>` | Status einer Workflow-Ausführung anzeigen |
| `tetora workflow export <name>` | Workflow als teilbare JSON-Datei exportieren |
| `tetora workflow create <file>` | Workflow aus einer JSON-Datei validieren und importieren |
| `tetora mcp list` | MCP-Serververbindungen auflisten |
| `tetora budget show` | Budgetstatus anzeigen |
| `tetora config show` | Aktuelle Konfiguration anzeigen |
| `tetora config validate` | config.json validieren |
| `tetora backup` | Ein Backup-Archiv erstellen |
| `tetora restore <file>` | Aus einem Backup-Archiv wiederherstellen |
| `tetora dashboard` | Das Web-Dashboard im Browser öffnen |
| `tetora logs` | Daemon-Logs anzeigen (`-f` zum Verfolgen, `--json` für strukturierte Ausgabe) |
| `tetora health` | Laufzeit-Gesundheitsprüfung (Daemon, Worker, Taskboard, Festplatte) |
| `tetora drain` | Sanftes Herunterfahren: neue Aufgaben stoppen, laufende Agenten abwarten |
| `tetora data status` | Datenaufbewahrungsstatus anzeigen |
| `tetora security scan` | Sicherheitsscan und Baseline |
| `tetora prompt list` | Prompt-Vorlagen verwalten |
| `tetora project add` | Ein Projekt zum Workspace hinzufügen |
| `tetora guide` | Interaktiver Onboarding-Leitfaden |
| `tetora upgrade` | Auf die neueste Version aktualisieren |
| `tetora service install` | Als launchd-Dienst installieren (macOS) |
| `tetora completion <shell>` | Shell-Vervollständigungen generieren (bash, zsh, fish) |
| `tetora version` | Version anzeigen |

Führe `tetora help` für die vollständige Befehlsreferenz aus.

---

## Mitwirken

Beiträge sind willkommen. Bitte eröffne ein Issue, um größere Änderungen zu besprechen, bevor du einen Pull Request einreichst.

- **Issues**: [github.com/TakumaLee/Tetora/issues](https://github.com/TakumaLee/Tetora/issues)
- **Diskussionen**: [github.com/TakumaLee/Tetora/discussions](https://github.com/TakumaLee/Tetora/discussions)

Dieses Projekt ist unter der AGPL-3.0 lizenziert, die verlangt, dass abgeleitete Werke und netzwerkzugängliche Deployments ebenfalls unter derselben Lizenz als Open Source veröffentlicht werden. Bitte lies die Lizenz vor dem Mitwirken.

---

## Lizenz

[AGPL-3.0](https://www.gnu.org/licenses/agpl-3.0.html)

Copyright (c) Tetora contributors.
