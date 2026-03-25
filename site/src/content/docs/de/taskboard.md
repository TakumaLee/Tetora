---
title: "Taskboard & Auto-Dispatch Leitfaden"
lang: "de"
order: 4
description: "Track tasks, priorities, and agent assignments with the built-in taskboard."
---
# Taskboard & Auto-Dispatch Leitfaden

## Übersicht

Das Taskboard ist Tetoras integriertes Kanban-System zur Verfolgung und automatischen Ausführung von Aufgaben. Es kombiniert einen persistenten Aufgabenspeicher (auf SQLite-Basis) mit einer Auto-Dispatch-Engine, die auf bereite Aufgaben wartet und diese ohne manuelle Eingriffe an Agents übergibt.

Typische Anwendungsfälle:

- Einen Backlog von Entwicklungsaufgaben einreihen und Agents über Nacht daran arbeiten lassen
- Aufgaben anhand von Fachkompetenz an bestimmte Agents weiterleiten (z. B. `kokuyou` für Backend, `kohaku` für Content)
- Aufgaben mit Abhängigkeitsbeziehungen verketten, sodass Agents dort weitermachen, wo andere aufgehört haben
- Aufgabenausführung mit git integrieren: automatische Branch-Erstellung, Commit, Push und PR/MR

**Voraussetzungen:** `taskBoard.enabled: true` in `config.json` und laufender Tetora-Daemon.

---

## Aufgaben-Lebenszyklus

Aufgaben durchlaufen Statuse in dieser Reihenfolge:

```
idea → needs-thought → backlog → todo → doing → review → done
                                                  ↓
                                           partial-done
                                                  ↓
                                              failed
```

| Status | Bedeutung |
|---|---|
| `idea` | Grober Konzeptentwurf, noch nicht ausgearbeitet |
| `needs-thought` | Erfordert Analyse oder Design vor der Umsetzung |
| `backlog` | Definiert und priorisiert, aber noch nicht geplant |
| `todo` | Bereit zur Ausführung — Auto-Dispatch nimmt dies auf, wenn ein Zugewiesener gesetzt ist |
| `doing` | Wird gerade ausgeführt |
| `review` | Ausführung abgeschlossen, wartet auf Qualitätsprüfung |
| `done` | Abgeschlossen und überprüft |
| `partial-done` | Ausführung erfolgreich, aber Nachbearbeitung fehlgeschlagen (z. B. git-Merge-Konflikt). Wiederherstellbar. |
| `failed` | Ausführung fehlgeschlagen oder keine Ausgabe erzeugt. Wird bis zu `maxRetries` wiederholt. |

Auto-Dispatch nimmt Aufgaben mit `status=todo` auf. Hat eine Aufgabe keinen Zugewiesenen, wird sie automatisch `defaultAgent` zugewiesen (Standard: `ruri`). Aufgaben im `backlog` werden regelmäßig vom konfigurierten `backlogAgent` (Standard: `ruri`) triagiert, der vielversprechende Aufgaben auf `todo` befördert.

---

## Aufgaben erstellen

### CLI

```bash
# Minimale Aufgabe (landet im Backlog, ohne Zuweisung)
tetora task create --title="Add rate limiting to API"

# Mit allen Optionen
tetora task create \
  --title="Refactor auth middleware" \
  --description="Split token validation into its own package. See ADR-14." \
  --priority=high \
  --assignee=kokuyou \
  --type=refactor

# Aufgaben auflisten
tetora task list
tetora task list --status=todo
tetora task list --assignee=kokuyou
tetora task list --project=api-v2

# Eine bestimmte Aufgabe anzeigen
tetora task show task-abc123
tetora task show task-abc123 --full   # inkl. Kommentare/Thread

# Aufgabe manuell verschieben
tetora task move task-abc123 --status=todo

# Einem Agent zuweisen
tetora task assign task-abc123 --assignee=kokuyou

# Kommentar hinzufügen (Typ: spec, context, log oder system)
tetora task comment task-abc123 \
  --author=takuma \
  --content="Must pass existing test suite. Do not touch auth.go." \
  --type=spec
```

Aufgaben-IDs werden automatisch im Format `task-<uuid>` generiert. Eine Aufgabe kann über ihre vollständige ID oder ein kurzes Präfix referenziert werden — die CLI schlägt Übereinstimmungen vor.

### HTTP API

```bash
# Erstellen
curl -X POST http://localhost:8991/api/tasks \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Add rate limiting",
    "description": "Implement token bucket per API key",
    "priority": "high",
    "assignee": "kokuyou",
    "type": "feat"
  }'

# Auflisten (nach Status filtern)
curl "http://localhost:8991/api/tasks?status=todo"

# In neuen Status verschieben
curl -X PATCH http://localhost:8991/api/tasks/task-abc123 \
  -H "Content-Type: application/json" \
  -d '{"status": "todo"}'
```

### Dashboard

Den **Taskboard**-Tab im Dashboard öffnen (`http://localhost:8991/dashboard`). Aufgaben werden in Kanban-Spalten angezeigt. Karten zwischen Spalten ziehen, um den Status zu ändern; auf eine Karte klicken, um das Detailpanel mit Kommentaren und Diff-Ansicht zu öffnen.

---

## Auto-Dispatch

Auto-Dispatch ist die Hintergrundschleife, die `todo`-Aufgaben aufnimmt und durch Agents ausführt.

### Funktionsweise

1. Ein Ticker wird alle `interval` Sekunden ausgelöst (Standard: `5m`).
2. Der Scanner prüft, wie viele Aufgaben gerade laufen. Wenn `activeCount >= maxConcurrentTasks`, wird der Scan übersprungen.
3. Für jede `todo`-Aufgabe mit einem Zugewiesenen wird die Aufgabe an diesen Agent dispatcht. Nicht zugewiesene Aufgaben werden automatisch `defaultAgent` zugewiesen.
4. Wenn eine Aufgabe abgeschlossen ist, wird sofort ein erneuter Scan ausgelöst, sodass der nächste Batch ohne Warten auf das volle Intervall startet.
5. Beim Daemon-Start werden verwaiste `doing`-Aufgaben aus einem vorherigen Absturz entweder auf `done` wiederhergestellt (wenn Abschlussnachweise vorhanden sind) oder auf `todo` zurückgesetzt (wenn wirklich verwaist).

### Dispatch-Ablauf

```
                          ┌─────────┐
                          │  idea   │  (manueller Konzepteintrag)
                          └────┬────┘
                               ▼
                       ┌──────────────┐
                       │ needs-thought │  (erfordert Analyse)
                       └───────┬──────┘
                               ▼
  ┌──────────────────────────────────────────────────────────┐
  │                       backlog                             │
  │                                                           │
  │  Triage (backlogAgent, Standard: ruri) läuft regelmäßig: │
  │   • "ready"     → Agent zuweisen → zu todo befördern      │
  │   • "decompose" → Teilaufgaben erstellen → Elterntask     │
  │                   zu doing setzen                         │
  │   • "clarify"   → Frage als Kommentar hinzufügen →        │
  │                   im backlog belassen                     │
  │                                                           │
  │  Schnellpfad: hat bereits Zugewiesenen + keine            │
  │  blockierenden Abhängigkeiten → LLM-Triage überspringen,  │
  │  direkt zu todo befördern                                 │
  └──────────────────────┬───────────────────────────────────┘
                         ▼
  ┌──────────────────────────────────────────────────────────┐
  │                        todo                               │
  │                                                           │
  │  Auto-Dispatch nimmt Aufgaben bei jedem Scan-Zyklus auf:  │
  │   • Hat Zugewiesenen     → an diesen Agent dispatchen     │
  │   • Kein Zugewiesener    → defaultAgent zuweisen, dann    │
  │                            ausführen                      │
  │   • Hat Workflow         → durch Workflow-Pipeline führen │
  │   • Hat dependsOn        → warten bis Abh. erledigt sind  │
  │   • Fortsetzbare Session → ab Checkpoint fortsetzen       │
  └──────────────────────┬───────────────────────────────────┘
                         ▼
  ┌──────────────────────────────────────────────────────────┐
  │                       doing                               │
  │                                                           │
  │  Agent führt Aufgabe aus (einzelner Prompt oder           │
  │  Workflow-DAG)                                            │
  │                                                           │
  │  Guard: stuckThreshold (Standard: 2h)                     │
  │   • Wenn Workflow noch läuft → Zeitstempel aktualisieren  │
  │   • Wenn wirklich steckt    → auf todo zurücksetzen       │
  └────────┬──────────┬──────────┬──────────────────────────┘
           │          │          │
     Erfolg    Teilfehler   Fehler
           │          │          │
           ▼          ▼          ▼
       ┌────────┐ ┌──────────┐ ┌────────┐
       │ review │ │ partial- │ │ failed │
       │        │ │   done   │ │        │
       └───┬────┘ └────┬─────┘ └───┬────┘
           │           │           │
           │     Fortsetzen-       │  Wiederholen (bis maxRetries)
           │     Schaltfläche      │  oder eskalieren
           │     im Dashboard      │
           ▼                       ▼
       ┌────────┐            ┌──────────┐
       │  done  │            │  an      │
       └────────┘            │  Mensch  │
                             │  eskal.  │
                             └──────────┘
```

### Triage-Details

Triage läuft alle `backlogTriageInterval` Minuten (Standard: `1h`) und wird vom `backlogAgent` (Standard: `ruri`) durchgeführt. Der Agent erhält jede Backlog-Aufgabe mit ihren Kommentaren und dem verfügbaren Agent-Roster und entscheidet:

| Aktion | Effekt |
|---|---|
| `ready` | Weist einen bestimmten Agent zu und befördert die Aufgabe zu `todo` |
| `decompose` | Erstellt Teilaufgaben (mit Zugewiesenen), Elterntask wechselt zu `doing` |
| `clarify` | Fügt eine Frage als Kommentar hinzu, Aufgabe verbleibt im `backlog` |

**Schnellpfad**: Aufgaben, die bereits einen Zugewiesenen haben und keine blockierenden Abhängigkeiten aufweisen, überspringen die LLM-Triage vollständig und werden sofort zu `todo` befördert.

### Automatische Zuweisung

Wenn eine `todo`-Aufgabe keinen Zugewiesenen hat, weist der Dispatcher sie automatisch `defaultAgent` zu (konfigurierbar, Standard: `ruri`). Dies verhindert, dass Aufgaben still steckenbleiben. Der typische Ablauf:

1. Aufgabe ohne Zugewiesenen erstellt → in `backlog` eingereiht
2. Triage befördert zu `todo` (mit oder ohne Agentenzuweisung)
3. Wenn Triage keine Zuweisung vorgenommen hat → Dispatcher weist `defaultAgent` zu
4. Aufgabe wird normal ausgeführt

### Konfiguration

In `config.json` hinzufügen:

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

| Feld | Standard | Beschreibung |
|---|---|---|
| `enabled` | `false` | Auto-Dispatch-Schleife aktivieren |
| `interval` | `5m` | Wie oft nach bereiten Aufgaben gesucht wird |
| `maxConcurrentTasks` | `3` | Maximale gleichzeitig laufende Aufgaben |
| `defaultAgent` | `ruri` | Wird nicht zugewiesenen `todo`-Aufgaben vor dem Dispatch automatisch zugewiesen |
| `backlogAgent` | `ruri` | Agent, der Backlog-Aufgaben prüft und befördert |
| `reviewAgent` | `ruri` | Agent, der abgeschlossene Aufgabenausgaben überprüft |
| `escalateAssignee` | `takuma` | Erhält die Zuweisung, wenn eine automatische Überprüfung menschliche Einschätzung erfordert |
| `stuckThreshold` | `2h` | Maximale Zeit, die eine Aufgabe in `doing` verbleiben kann, bevor sie zurückgesetzt wird |
| `backlogTriageInterval` | `1h` | Minimales Intervall zwischen Backlog-Triage-Läufen |
| `reviewLoop` | `false` | Dev↔QA-Schleife aktivieren (ausführen → überprüfen → korrigieren, bis zu `maxRetries`) |
| `maxBudget` | kein Limit | Maximale Kosten pro Aufgabe in USD |
| `defaultModel` | — | Modell für alle automatisch dispatchten Aufgaben überschreiben |

---

## Slot Pressure

Slot Pressure verhindert, dass Auto-Dispatch alle Concurrency-Slots belegt und interaktive Sessions (menschliche Chat-Nachrichten, On-Demand-Dispatches) aushungert.

In `config.json` aktivieren:

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

| Feld | Standard | Beschreibung |
|---|---|---|
| `reservedSlots` | `2` | Für interaktive Nutzung zurückgehaltene Slots. Nicht-interaktive Aufgaben müssen warten, wenn die verfügbaren Slots auf diesen Wert fallen. |
| `warnThreshold` | `3` | Warnung wird ausgelöst, wenn die verfügbaren Slots auf diesen Wert fallen. Die Meldung "排程接近滿載" erscheint im Dashboard und in Benachrichtigungskanälen. |
| `nonInteractiveTimeout` | `5m` | Wie lange eine nicht-interaktive Aufgabe auf einen Slot wartet, bevor sie abgebrochen wird. |

Interaktive Quellen (menschlicher Chat, `tetora dispatch`, `tetora route`) belegen Slots sofort. Hintergrundquellen (Taskboard, Cron) warten, wenn der Druck hoch ist.

---

## Git-Integration

Wenn `gitCommit`, `gitPush` und `gitPR` aktiviert sind, führt der Dispatcher nach erfolgreichem Abschluss einer Aufgabe git-Operationen durch.

**Branch-Benennung** wird durch `gitWorkflow.branchConvention` gesteuert:

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

Die Standard-Vorlage `{type}/{agent}-{description}` erzeugt Branches wie `feat/kokuyou-add-rate-limiting`. Der `{description}`-Teil wird aus dem Aufgabentitel abgeleitet (Kleinbuchstaben, Leerzeichen durch Bindestriche ersetzt, auf 40 Zeichen gekürzt).

Das `type`-Feld einer Aufgabe setzt das Branch-Präfix. Hat eine Aufgabe keinen Typ, wird `defaultType` verwendet.

**Auto PR/MR** unterstützt sowohl GitHub (`gh`) als auch GitLab (`glab`). Die im `PATH` verfügbare Binary wird automatisch verwendet.

---

## Worktree-Modus

Wenn `gitWorktree: true` gesetzt ist, läuft jede Aufgabe in einem isolierten git Worktree statt im gemeinsamen Arbeitsverzeichnis. Dies eliminiert Dateikonflikte, wenn mehrere Aufgaben gleichzeitig auf demselben Repository ausgeführt werden.

```
~/.tetora/runtime/worktrees/
  task-abc123/   ← isolierte Kopie für diese Aufgabe
  task-def456/   ← isolierte Kopie für diese Aufgabe
```

Nach Aufgabenabschluss:

- Wenn `autoMerge: true` (Standard), wird der Worktree-Branch zurück in `main` gemergt und der Worktree entfernt.
- Wenn der Merge fehlschlägt, wechselt die Aufgabe in den Status `partial-done`. Der Worktree bleibt für die manuelle Auflösung erhalten.

Wiederherstellung aus `partial-done`:

```bash
# Details und den erstellten Branch prüfen
tetora task show task-abc123 --full

# Branch manuell mergen
git merge feat/kokuyou-add-rate-limiting

# Konflikte im Editor lösen, dann committen
git add .
git commit -m "merge: feat/kokuyou-add-rate-limiting"

# Aufgabe als erledigt markieren
tetora task move task-abc123 --status=done
```

---

## Workflow-Integration

Aufgaben können statt durch einen einzelnen Agent-Prompt durch eine Workflow-Pipeline ausgeführt werden. Dies ist nützlich, wenn eine Aufgabe mehrere koordinierte Schritte erfordert (z. B. Recherche → Implementierung → Test → Dokumentation).

Einen Workflow einer Aufgabe zuweisen:

```bash
# Bei der Aufgabenerstellung setzen
tetora task create \
  --title="Implement OAuth2 flow" \
  --workflow=engineering-pipeline \
  --assignee=kokuyou

# Oder eine bestehende Aufgabe aktualisieren
tetora task update task-abc123 --workflow=engineering-pipeline
```

Den Standard-Workflow für eine bestimmte Aufgabe deaktivieren:

```json
{ "workflow": "none" }
```

Ein Standard-Workflow auf Board-Ebene gilt für alle automatisch dispatchten Aufgaben, sofern nicht überschrieben:

```json
{
  "taskBoard": {
    "defaultWorkflow": "engineering-pipeline"
  }
}
```

Das Feld `workflowRunId` einer Aufgabe verknüpft sie mit der spezifischen Workflow-Ausführung, sichtbar im Workflows-Tab des Dashboards.

---

## Dashboard-Ansichten

Das Dashboard unter `http://localhost:8991/dashboard` aufrufen und zum Tab **Taskboard** navigieren.

**Kanban-Board** — Spalten für jeden Status. Karten zeigen Titel, Zugewiesenen, Prioritätsabzeichen und Kosten. Per Drag-and-Drop Status ändern.

**Aufgaben-Detailpanel** — Auf eine beliebige Karte klicken zum Öffnen. Zeigt:
- Vollständige Beschreibung und alle Kommentare (Spec, Kontext, Log-Einträge)
- Session-Link (springt zum Live-Worker-Terminal, wenn noch aktiv)
- Kosten, Dauer, Wiederholungszähler
- Workflow-Ausführungslink, falls zutreffend

**Diff-Review-Panel** — Wenn `requireReview: true`, erscheinen abgeschlossene Aufgaben in einer Review-Warteschlange. Prüfer sehen den Diff der Änderungen und können genehmigen oder Änderungen anfordern.

---

## Hinweise

**Aufgabengröße.** Aufgaben auf 30–90 Minuten beschränken. Zu große Aufgaben (mehrtägige Refactorings) neigen dazu, in den Timeout zu laufen oder keine Ausgabe zu erzeugen, und werden als fehlgeschlagen markiert. Solche Aufgaben mithilfe des `parentId`-Felds in Teilaufgaben aufteilen.

**Gleichzeitige Dispatch-Limits.** `maxConcurrentTasks: 3` ist ein sicherer Standardwert. Eine Erhöhung über die Anzahl der API-Verbindungen des Providers hinaus verursacht Konflikte und Timeouts. Mit 3 beginnen und erst auf 5 erhöhen, wenn stabiles Verhalten bestätigt ist.

**Partial-done-Wiederherstellung.** Wenn eine Aufgabe `partial-done` erreicht, hat der Agent seine Arbeit erfolgreich abgeschlossen — nur der git-Merge-Schritt ist fehlgeschlagen. Den Konflikt manuell lösen, dann die Aufgabe auf `done` setzen. Kosten- und Session-Daten bleiben erhalten.

**`dependsOn` verwenden.** Aufgaben mit nicht erfüllten Abhängigkeiten werden vom Dispatcher übersprungen, bis alle aufgelisteten Aufgaben-IDs den Status `done` erreichen. Die Ergebnisse vorgelagerter Aufgaben werden automatisch unter "Previous Task Results" in den Prompt der abhängigen Aufgabe injiziert.

**Backlog-Triage.** Der `backlogAgent` liest jede `backlog`-Aufgabe, bewertet Machbarkeit und Priorität und befördert eindeutige Aufgaben zu `todo`. Detaillierte Beschreibungen und Abnahmekriterien in `backlog`-Aufgaben schreiben — der Triage-Agent verwendet sie, um zu entscheiden, ob eine Aufgabe befördert oder zur menschlichen Überprüfung belassen wird.

**Wiederholungen und die Review-Schleife.** Mit `reviewLoop: false` (Standard) wird eine fehlgeschlagene Aufgabe bis zu `maxRetries` Mal wiederholt, wobei frühere Log-Kommentare injiziert werden. Mit `reviewLoop: true` wird jede Ausführung vom `reviewAgent` geprüft, bevor sie als erledigt gilt — der Agent erhält Feedback und versucht es erneut, wenn Probleme gefunden werden.
