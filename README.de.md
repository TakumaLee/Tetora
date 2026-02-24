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
  sessions/          Sitzungsdateien pro Agent
  knowledge/         Dokumente der Wissensdatenbank
  logs/              Strukturierte Logdateien
  outputs/           Generierte Ausgabedateien
  uploads/           Temporärer Upload-Speicher
  bin/               Installierte Binary
```

Die Konfiguration verwendet reines JSON mit Unterstützung für `$ENV_VAR`-Referenzen, sodass Geheimnisse nie hartcodiert werden müssen. Der Einrichtungsassistent (`tetora init`) generiert interaktiv eine funktionsfähige `config.json`.

Hot-Reload wird unterstützt: Sende `SIGHUP` an den laufenden Daemon, um `config.json` ohne Ausfallzeit neu zu laden.

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
| `tetora mcp list` | MCP-Serververbindungen auflisten |
| `tetora budget show` | Budgetstatus anzeigen |
| `tetora config show` | Aktuelle Konfiguration anzeigen |
| `tetora config validate` | config.json validieren |
| `tetora backup` | Ein Backup-Archiv erstellen |
| `tetora restore <file>` | Aus einem Backup-Archiv wiederherstellen |
| `tetora dashboard` | Das Web-Dashboard im Browser öffnen |
| `tetora logs` | Daemon-Logs anzeigen (`-f` zum Verfolgen, `--json` für strukturierte Ausgabe) |
| `tetora data status` | Datenaufbewahrungsstatus anzeigen |
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
