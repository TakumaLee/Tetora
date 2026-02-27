# Workflows

## Übersicht

Workflows sind das Mehrstufige-Aufgaben-Orchestrierungssystem von Tetora. Definieren Sie eine Abfolge von Schritten in JSON, lassen Sie verschiedene Agents zusammenarbeiten und automatisieren Sie komplexe Aufgaben.

**Anwendungsfälle:**

- Aufgaben, die mehrere Agents sequenziell oder parallel erfordern
- Prozesse mit bedingter Verzweigung und Fehler-Retry-Logik
- Automatisierte Arbeit, ausgelöst durch cron-Zeitpläne, Events oder webhooks
- Formale Prozesse, die einen Ausführungsverlauf und Kostenüberwachung benötigen

## Schnellstart

### 1. Workflow-JSON erstellen

Erstellen Sie `my-workflow.json`:

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

### 2. Importieren und validieren

```bash
# Validate the JSON structure
tetora workflow validate my-workflow.json

# Import to ~/.tetora/workflows/
tetora workflow create my-workflow.json
```

### 3. Ausführen

```bash
# Execute the workflow
tetora workflow run research-and-summarize

# Override variables
tetora workflow run research-and-summarize --var topic="LLM safety"

# Dry-run (no LLM calls, cost estimation only)
tetora workflow run research-and-summarize --dry-run
```

### 4. Ergebnisse prüfen

```bash
# List execution history
tetora workflow runs research-and-summarize

# View detailed status of a specific run
tetora workflow status <run-id>
```

## Workflow-JSON-Struktur

### Felder auf oberster Ebene

| Feld | Typ | Pflicht | Beschreibung |
|------|-----|:-------:|--------------|
| `name` | string | Ja | Workflow-Name. Nur alphanumerisch, `-` und `_` (z. B. `my-workflow`) |
| `description` | string | | Beschreibung |
| `steps` | WorkflowStep[] | Ja | Mindestens ein Schritt erforderlich |
| `variables` | map[string]string | | Eingabevariablen mit Standardwerten (leeres `""` = Pflichtfeld) |
| `timeout` | string | | Gesamttimeout im Go-Dauerformat (z. B. `"30m"`, `"1h"`) |
| `onSuccess` | string | | Benachrichtigungsvorlage bei Erfolg |
| `onFailure` | string | | Benachrichtigungsvorlage bei Fehler |

### WorkflowStep-Felder

| Feld | Typ | Beschreibung |
|------|-----|--------------|
| `id` | string | **Pflichtfeld** — Eindeutiger Schritt-Bezeichner |
| `type` | string | Schritttyp, Standard ist `"dispatch"`. Siehe Typen unten |
| `agent` | string | Agent-Rolle, die diesen Schritt ausführt |
| `prompt` | string | Anweisung für den Agent (unterstützt `{{}}` Templates) |
| `skill` | string | Skill-Name (für type=skill) |
| `skillArgs` | string[] | Skill-Argumente (unterstützt Templates) |
| `dependsOn` | string[] | Voraussetzende Schritt-IDs (DAG-Abhängigkeiten) |
| `model` | string | LLM-Modell-Override |
| `provider` | string | Provider-Override |
| `timeout` | string | Timeout pro Schritt |
| `budget` | number | Kostenlimit (USD) |
| `permissionMode` | string | Berechtigungsmodus |
| `if` | string | Bedingungsausdruck (type=condition) |
| `then` | string | Schritt-ID, zu der bei wahrem Ausdruck gesprungen wird |
| `else` | string | Schritt-ID, zu der bei falschem Ausdruck gesprungen wird |
| `handoffFrom` | string | Quell-Schritt-ID (type=handoff) |
| `parallel` | WorkflowStep[] | Teilschritte für parallele Ausführung (type=parallel) |
| `retryMax` | int | Maximale Anzahl von Wiederholungsversuchen (erfordert `onError: "retry"`) |
| `retryDelay` | string | Wiederholungsintervall, z. B. `"10s"` |
| `onError` | string | Fehlerbehandlung: `"stop"` (Standard), `"skip"`, `"retry"` |
| `toolName` | string | Tool-Name (type=tool_call) |
| `toolInput` | map[string]string | Tool-Eingabeparameter (unterstützt `{{var}}`-Expansion) |
| `delay` | string | Wartezeit (type=delay), z. B. `"30s"`, `"5m"` |
| `notifyMsg` | string | Benachrichtigungsnachricht (type=notify, unterstützt Templates) |
| `notifyTo` | string | Hinweis auf Benachrichtigungskanal (z. B. `"telegram"`) |

## Schritttypen

### dispatch (Standard)

Sendet einen Prompt an den angegebenen Agent zur Ausführung. Dies ist der häufigste Schritttyp und wird verwendet, wenn `type` weggelassen wird.

```json
{
  "id": "draft",
  "agent": "kohaku",
  "prompt": "Write an article about {{topic}}",
  "model": "claude-sonnet-4-20250514",
  "timeout": "10m"
}
```

**Pflichtfeld:** `prompt`
**Optional:** `agent`, `model`, `provider`, `timeout`, `budget`, `permissionMode`

### skill

Führt einen registrierten Skill aus.

```json
{
  "id": "search",
  "type": "skill",
  "skill": "web-search",
  "skillArgs": ["{{topic}}", "--depth", "3"]
}
```

**Pflichtfeld:** `skill`
**Optional:** `skillArgs`

### condition

Wertet einen Bedingungsausdruck aus, um den Branch zu bestimmen. Bei wahrem Ausdruck wird `then` gewählt, bei falschem `else`. Der nicht gewählte Branch wird als übersprungen markiert.

```json
{
  "id": "check-type",
  "type": "condition",
  "if": "{{type}} == 'technical'",
  "then": "tech-research",
  "else": "creative-draft"
}
```

**Pflichtfelder:** `if`, `then`
**Optional:** `else`

Unterstützte Operatoren:
- `==` — gleich (z. B. `{{type}} == 'technical'`)
- `!=` — ungleich
- Wahrheitswertprüfung — nicht leer und nicht `"false"`/`"0"` wird als wahr ausgewertet

### parallel

Führt mehrere Teilschritte gleichzeitig aus und wartet auf den Abschluss aller. Ausgaben der Teilschritte werden mit `\n---\n` verbunden.

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

**Pflichtfeld:** `parallel` (mindestens ein Teilschritt)

Einzelne Teilschritt-Ergebnisse können über `{{steps.search-papers.output}}` referenziert werden.

### handoff

Übergibt die Ausgabe eines Schritts an einen anderen Agent zur weiteren Verarbeitung. Die vollständige Ausgabe des Quellschritts wird zum Kontext des empfangenden Agents.

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

**Pflichtfelder:** `handoffFrom`, `agent`
**Optional:** `prompt` (Anweisung für den empfangenden Agent)

### tool_call

Ruft ein registriertes Tool aus der Tool-Registry auf.

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

**Pflichtfeld:** `toolName`
**Optional:** `toolInput` (unterstützt `{{var}}`-Expansion)

### delay

Wartet eine bestimmte Zeitspanne, bevor die Ausführung fortgesetzt wird.

```json
{
  "id": "wait",
  "type": "delay",
  "delay": "30s"
}
```

**Pflichtfeld:** `delay` (Go-Dauerformat: `"30s"`, `"5m"`, `"1h"`)

### notify

Sendet eine Benachrichtigungsnachricht. Die Nachricht wird als SSE-Event (type=`workflow_notify`) veröffentlicht, sodass externe Konsumenten Telegram, Slack usw. auslösen können.

```json
{
  "id": "notify-done",
  "type": "notify",
  "notifyMsg": "Task complete: {{steps.review.output}}",
  "notifyTo": "telegram"
}
```

**Pflichtfeld:** `notifyMsg`
**Optional:** `notifyTo` (Kanalhinweis)

## Variablen und Templates

Workflows unterstützen die `{{}}`-Template-Syntax, die vor der Schrittausführung aufgelöst wird.

### Eingabevariablen

```
{{varName}}
```

Werden aus den `variables`-Standardwerten oder `--var key=value`-Overrides aufgelöst.

### Schritt-Ergebnisse

```
{{steps.ID.output}}    — Ausgabetext des Schritts
{{steps.ID.status}}    — Status des Schritts (success/error/skipped/timeout)
{{steps.ID.error}}     — Fehlermeldung des Schritts
```

### Umgebungsvariablen

```
{{env.KEY}}            — Systemumgebungsvariable
```

### Beispiel

```json
{
  "id": "summarize",
  "agent": "kohaku",
  "prompt": "Topic: {{topic}}\nResearch results: {{steps.research.output}}\n\nPlease write a summary.",
  "dependsOn": ["research"]
}
```

## Abhängigkeiten und Ablaufsteuerung

### dependsOn — DAG-Abhängigkeiten

Verwenden Sie `dependsOn`, um die Ausführungsreihenfolge festzulegen. Das System sortiert Schritte automatisch als DAG (gerichteter azyklischer Graph).

```json
{
  "id": "step-c",
  "dependsOn": ["step-a", "step-b"],
  "prompt": "..."
}
```

- `step-c` wartet auf den Abschluss sowohl von `step-a` als auch von `step-b`
- Schritte ohne `dependsOn` starten sofort (möglicherweise parallel)
- Zirkuläre Abhängigkeiten werden erkannt und abgelehnt

### Bedingte Verzweigung

Das `then`/`else` eines `condition`-Schritts bestimmt, welche nachgelagerten Schritte ausgeführt werden:

```
classify (condition)
  ├── then → tech-research
  └── else → creative-draft
```

Der nicht gewählte Branch-Schritt wird als `skipped` markiert. Nachgelagerte Schritte werden weiterhin normal anhand ihrer `dependsOn`-Abhängigkeiten ausgewertet.

## Fehlerbehandlung

### onError-Strategien

Jeder Schritt kann `onError` festlegen:

| Wert | Verhalten |
|------|-----------|
| `"stop"` | **Standard** — Workflow bei Fehler abbrechen; verbleibende Schritte werden als übersprungen markiert |
| `"skip"` | Fehlgeschlagenen Schritt als übersprungen markieren und fortfahren |
| `"retry"` | Gemäß `retryMax` + `retryDelay` wiederholen; bei erschöpften Versuchen als Fehler behandeln |

### Retry-Konfiguration

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

- `retryMax`: Maximale Anzahl von Wiederholungsversuchen (ohne den ersten Versuch)
- `retryDelay`: Wartezeit zwischen Wiederholungen, Standard ist 5 Sekunden
- Nur wirksam bei `onError: "retry"`

## Trigger

Trigger ermöglichen die automatische Workflow-Ausführung. Konfigurieren Sie sie in `config.json` unter dem `workflowTriggers`-Array.

### WorkflowTriggerConfig-Struktur

| Feld | Typ | Beschreibung |
|------|-----|--------------|
| `name` | string | Trigger-Name |
| `workflowName` | string | Auszuführender Workflow |
| `enabled` | bool | Ob aktiviert (Standard: true) |
| `trigger` | TriggerSpec | Trigger-Bedingung |
| `variables` | map[string]string | Variablen-Overrides für den Workflow |
| `cooldown` | string | Cooldown-Periode (z. B. `"5m"`, `"1h"`) |

### TriggerSpec-Struktur

| Feld | Typ | Beschreibung |
|------|-----|--------------|
| `type` | string | `"cron"`, `"event"` oder `"webhook"` |
| `cron` | string | Cron-Ausdruck (5 Felder: min hour day month weekday) |
| `tz` | string | Zeitzone (z. B. `"Asia/Taipei"`), nur für cron |
| `event` | string | SSE-Event-Typ, unterstützt `*`-Suffix-Wildcard (z. B. `"deploy_*"`) |
| `webhook` | string | Webhook-Pfad-Suffix |

### Cron-Trigger

Wird alle 30 Sekunden geprüft, löst höchstens einmal pro Minute aus (Deduplizierung).

```json
{
  "name": "daily-briefing",
  "workflowName": "research-and-summarize",
  "trigger": {"type": "cron", "cron": "0 8 * * *", "tz": "Asia/Taipei"},
  "variables": {"topic": "AI industry news"},
  "cooldown": "12h"
}
```

### Event-Trigger

Lauscht auf dem SSE-`_triggers`-Kanal und vergleicht Event-Typen. Unterstützt `*`-Suffix-Wildcard.

```json
{
  "name": "on-deploy",
  "workflowName": "content-pipeline",
  "trigger": {"type": "event", "event": "deploy_*"},
  "variables": {"type": "technical"}
}
```

Event-Trigger injizieren automatisch zusätzliche Variablen: `event_type`, `task_id`, `session_id` sowie Event-Datenfelder (mit dem Präfix `event_`).

### Webhook-Trigger

Wird per HTTP POST ausgelöst:

```json
{
  "name": "external-hook",
  "workflowName": "content-pipeline",
  "trigger": {"type": "webhook", "webhook": "content-request"}
}
```

Verwendung:

```bash
curl -X POST http://localhost:PORT/api/triggers/webhook/external-hook \
  -H "Content-Type: application/json" \
  -d '{"topic": "new feature"}'
```

Die JSON-Schlüssel-Wert-Paare des POST-Body werden als zusätzliche Workflow-Variablen injiziert.

### Cooldown

Alle Trigger unterstützen `cooldown`, um wiederholtes Auslösen innerhalb kurzer Zeit zu verhindern. Trigger während des Cooldowns werden stillschweigend ignoriert.

### Trigger-Meta-Variablen

Das System injiziert bei jedem Trigger automatisch folgende Variablen:

- `_trigger_name` — Trigger-Name
- `_trigger_type` — Trigger-Typ (cron/event/webhook)
- `_trigger_time` — Auslösezeitpunkt (RFC3339)

## Ausführungsmodi

### live (Standard)

Vollständige Ausführung: ruft LLMs auf, zeichnet den Verlauf auf, veröffentlicht SSE-Events.

```bash
tetora workflow run my-workflow
```

### dry-run

Keine LLM-Aufrufe; schätzt die Kosten für jeden Schritt. Condition-Schritte werden normal ausgewertet; dispatch/skill/handoff-Schritte geben Kostenschätzungen zurück.

```bash
tetora workflow run my-workflow --dry-run
```

### shadow

Führt LLM-Aufrufe normal durch, zeichnet aber nichts im Aufgabenverlauf oder in den Sitzungsprotokollen auf. Nützlich zum Testen.

```bash
tetora workflow run my-workflow --shadow
```

## CLI-Referenz

```
tetora workflow <command> [options]
```

| Befehl | Beschreibung |
|--------|--------------|
| `list` | Alle gespeicherten Workflows auflisten |
| `show <name>` | Workflow-Definition anzeigen (Zusammenfassung + JSON) |
| `validate <name\|file>` | Einen Workflow validieren (akzeptiert Name oder JSON-Dateipfad) |
| `create <file>` | Workflow aus einer JSON-Datei importieren (validiert zuerst) |
| `delete <name>` | Einen Workflow löschen |
| `run <name> [flags]` | Einen Workflow ausführen |
| `runs [name]` | Ausführungsverlauf auflisten (optional nach Name filtern) |
| `status <run-id>` | Detaillierten Status einer Ausführung anzeigen (JSON-Ausgabe) |
| `messages <run-id>` | Agent-Nachrichten und Handoff-Einträge einer Ausführung anzeigen |
| `history <name>` | Versions-Verlauf eines Workflows anzeigen |
| `rollback <name> <version-id>` | Auf eine bestimmte Version zurücksetzen |
| `diff <version1> <version2>` | Zwei Versionen vergleichen |

### Flags des run-Befehls

| Flag | Beschreibung |
|------|--------------|
| `--var key=value` | Workflow-Variable überschreiben (kann mehrfach verwendet werden) |
| `--dry-run` | Dry-run-Modus (keine LLM-Aufrufe) |
| `--shadow` | Shadow-Modus (kein Verlaufs-Recording) |

### Aliase

- `list` = `ls`
- `delete` = `rm`
- `messages` = `msgs`

## HTTP-API-Referenz

### Workflow CRUD

| Methode | Pfad | Beschreibung |
|---------|------|--------------|
| GET | `/workflows` | Alle Workflows auflisten |
| POST | `/workflows` | Einen Workflow erstellen (Body: Workflow JSON) |
| GET | `/workflows/{name}` | Eine einzelne Workflow-Definition abrufen |
| DELETE | `/workflows/{name}` | Einen Workflow löschen |
| POST | `/workflows/{name}/validate` | Einen Workflow validieren |
| POST | `/workflows/{name}/run` | Einen Workflow ausführen (asynchron, gibt `202 Accepted` zurück) |
| GET | `/workflows/{name}/runs` | Ausführungsverlauf eines Workflows abrufen |

#### POST /workflows/{name}/run Body

```json
{
  "variables": {
    "topic": "AI agents"
  }
}
```

### Workflow-Ausführungen

| Methode | Pfad | Beschreibung |
|---------|------|--------------|
| GET | `/workflow-runs` | Alle Ausführungseinträge auflisten (mit `?workflow=name` filtern) |
| GET | `/workflow-runs/{id}` | Ausführungsdetails abrufen (enthält Handoffs + Agent-Nachrichten) |

### Trigger

| Methode | Pfad | Beschreibung |
|---------|------|--------------|
| GET | `/api/triggers` | Alle Trigger-Status auflisten |
| POST | `/api/triggers/{name}/fire` | Einen Trigger manuell auslösen |
| GET | `/api/triggers/{name}/runs` | Trigger-Ausführungsverlauf anzeigen (mit `?limit=N`) |
| POST | `/api/triggers/webhook/{id}` | Webhook-Trigger (Body: JSON-Schlüssel-Wert-Variablen) |

## Versionsverwaltung

Jedes `create` oder jede Änderung erstellt automatisch einen Versions-Snapshot.

```bash
# View version history
tetora workflow history my-workflow

# Restore to a specific version
tetora workflow rollback my-workflow <version-id>

# Compare two versions
tetora workflow diff <version-id-1> <version-id-2>
```

## Validierungsregeln

Das System validiert vor sowohl `create` als auch `run`:

- `name` ist Pflichtfeld; nur alphanumerische Zeichen, `-` und `_` erlaubt
- Mindestens ein Schritt erforderlich
- Schritt-IDs müssen eindeutig sein
- `dependsOn`-Referenzen müssen auf vorhandene Schritt-IDs zeigen
- Schritte dürfen nicht von sich selbst abhängen
- Zirkuläre Abhängigkeiten werden abgelehnt (DAG-Zykluserkennung)
- Pflichtfelder je nach Schritttyp (z. B. dispatch benötigt `prompt`, condition benötigt `if` + `then`)
- `timeout`, `retryDelay`, `delay` müssen im gültigen Go-Dauerformat angegeben werden
- `onError` akzeptiert nur `stop`, `skip`, `retry`
- `then`/`else` einer Condition müssen auf vorhandene Schritt-IDs verweisen
- `handoffFrom` eines Handoffs muss auf eine vorhandene Schritt-ID verweisen
