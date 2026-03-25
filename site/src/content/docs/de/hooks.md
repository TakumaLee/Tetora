---
title: "Claude Code Hooks Integration"
lang: "de"
order: 3
description: "Integrate with Claude Code Hooks for real-time session observation."
---
# Claude Code Hooks Integration

## Übersicht

Claude Code Hooks sind ein in Claude Code integriertes Ereignissystem, das Shell-Befehle an wichtigen Punkten einer Session ausführt. Tetora registriert sich als Hook-Empfänger, um jede laufende Agent-Session in Echtzeit zu beobachten — ohne Polling, ohne tmux und ohne das Einschleusen von Wrapper-Skripten.

**Was Hooks ermöglichen:**

- Echtzeit-Fortschrittsverfolgung im Dashboard (Tool-Aufrufe, Session-Status, Live-Worker-Liste)
- Kosten- und Token-Überwachung über die Statusline-Bridge
- Tool-Nutzungs-Auditing (welche Tools in welcher Session in welchem Verzeichnis ausgeführt wurden)
- Erkennung des Session-Abschlusses und automatische Aktualisierung des Aufgabenstatus
- Plan-Mode-Gate: hält `ExitPlanMode` an, bis ein Mensch den Plan im Dashboard genehmigt
- Interaktives Fragen-Routing: `AskUserQuestion` wird zur MCP-Bridge umgeleitet, sodass Fragen in der konfigurierten Chat-Plattform erscheinen, statt das Terminal zu blockieren

Hooks sind der empfohlene Integrationspfad seit Tetora v2.0. Der ältere tmux-basierte Ansatz (v1.x) funktioniert noch, unterstützt aber keine hooks-exklusiven Funktionen wie das Plan-Gate und das Fragen-Routing.

---

## Architektur

```
Claude Code Session
  │
  ├── PreToolUse  ──────────────────► Tetora /api/hooks/event
  │   (ExitPlanMode)                  └─► Plan Gate: Long-Poll bis zur Genehmigung
  │   (AskUserQuestion)               └─► Ablehnen: Umleitung zur MCP-Bridge
  │
  ├── PostToolUse ──────────────────► Tetora /api/hooks/event
  │                                   └─► Worker-Status aktualisieren
  │                                   └─► Schreibvorgänge in Plan-Dateien erkennen
  │
  ├── Stop        ──────────────────► Tetora /api/hooks/event
  │                                   └─► Worker als erledigt markieren
  │                                   └─► Aufgabenabschluss auslösen
  │
  └── Notification ─────────────────► Tetora /api/hooks/event
                                      └─► Weiterleitung an Discord/Telegram
```

Der Hook-Befehl ist ein kleiner curl-Aufruf, der in die `~/.claude/settings.json` von Claude Code injiziert wird. Jedes Ereignis wird per `POST /api/hooks/event` an den laufenden Tetora-Daemon gesendet.

---

## Einrichtung

### Hooks installieren

Bei laufendem Tetora-Daemon:

```bash
tetora hooks install
```

Dieser Befehl schreibt Einträge in `~/.claude/settings.json` und generiert die MCP-Bridge-Konfiguration unter `~/.tetora/mcp/bridge.json`.

Beispiel, was in `~/.claude/settings.json` geschrieben wird:

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

### Status prüfen

```bash
tetora hooks status
```

Die Ausgabe zeigt, welche Hooks installiert sind, wie viele Tetora-Regeln registriert sind und die Gesamtzahl der empfangenen Ereignisse seit dem Daemon-Start.

Der Status ist auch im Dashboard abrufbar: **Engineering Details → Hooks** zeigt den gleichen Status plus einen Live-Ereignis-Feed.

### Hooks entfernen

```bash
tetora hooks remove
```

Entfernt alle Tetora-Einträge aus `~/.claude/settings.json`. Bestehende Hooks anderer Anwendungen bleiben erhalten.

---

## Hook-Ereignisse

### PostToolUse

Wird nach jedem abgeschlossenen Tool-Aufruf ausgelöst. Tetora verwendet dies, um:

- Zu verfolgen, welche Tools ein Agent verwendet (`Bash`, `Write`, `Edit`, `Read` usw.)
- `lastTool` und `toolCount` des Workers in der Live-Worker-Liste zu aktualisieren
- Zu erkennen, wenn ein Agent in eine Plan-Datei schreibt (löst Plan-Cache-Aktualisierung aus)

### Stop

Wird ausgelöst, wenn eine Claude Code Session endet (natürlicher Abschluss oder Abbruch). Tetora verwendet dies, um:

- Den Worker in der Live-Worker-Liste als `done` zu markieren
- Ein Abschluss-SSE-Ereignis an das Dashboard zu senden
- Nachgelagerte Aufgabenstatusaktualisierungen für Taskboard-Aufgaben auszulösen

### Notification

Wird ausgelöst, wenn Claude Code eine Benachrichtigung sendet (z. B. Berechtigung erforderlich, lange Pause). Tetora leitet diese an Discord/Telegram weiter und veröffentlicht sie im Dashboard-SSE-Stream.

### PreToolUse: ExitPlanMode (Plan-Gate)

Wenn ein Agent den Plan-Modus verlassen möchte, fängt Tetora das Ereignis mit einem Long-Poll (Timeout: 600 Sekunden) ab. Der Plan-Inhalt wird zwischengespeichert und im Dashboard unter der Detail-Ansicht der Session angezeigt.

Ein Mensch kann den Plan im Dashboard genehmigen oder ablehnen. Bei Genehmigung kehrt der Hook zurück und Claude Code fährt fort. Bei Ablehnung (oder wenn der Timeout abläuft) wird der Austritt blockiert und Claude Code verbleibt im Plan-Modus.

### PreToolUse: AskUserQuestion (Fragen-Routing)

Wenn Claude Code versucht, dem Benutzer interaktiv eine Frage zu stellen, fängt Tetora dies ab und verweigert das Standardverhalten. Die Frage wird stattdessen über die MCP-Bridge geleitet und erscheint in der konfigurierten Chat-Plattform (Discord, Telegram usw.), sodass geantwortet werden kann, ohne am Terminal zu sitzen.

---

## Echtzeit-Fortschrittsverfolgung

Sobald Hooks installiert sind, zeigt das **Workers**-Panel im Dashboard Live-Sessions:

| Feld | Quelle |
|---|---|
| Session-ID | `session_id` im Hook-Ereignis |
| Status | `working` / `idle` / `done` |
| Letztes Tool | Letzter `PostToolUse`-Tool-Name |
| Arbeitsverzeichnis | `cwd` aus Hook-Ereignis |
| Tool-Anzahl | Kumulativer `PostToolUse`-Zähler |
| Kosten / Tokens | Statusline-Bridge (`POST /api/hooks/usage`) |
| Herkunft | Verknüpfte Aufgabe oder Cron-Job, wenn durch Tetora dispatcht |

Kosten- und Token-Daten kommen vom Claude Code Statusline-Skript, das in einem konfigurierbaren Intervall an `/api/hooks/usage` sendet. Das Statusline-Skript ist von den Hooks getrennt — es liest die Claude Code Statusleisten-Ausgabe und leitet sie an Tetora weiter.

---

## Kostenüberwachung

Der Usage-Endpunkt (`POST /api/hooks/usage`) empfängt:

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

Diese Daten sind im Dashboard-Workers-Panel sichtbar und werden in den täglichen Kostendiagrammen aggregiert. Budget-Warnungen werden ausgelöst, wenn die Kosten einer Session das konfigurierte Pro-Rolle- oder globale Budget überschreiten.

---

## Fehlerbehebung

### Hooks werden nicht ausgelöst

**Prüfen, ob der Daemon läuft:**
```bash
tetora status
```

**Prüfen, ob Hooks installiert sind:**
```bash
tetora hooks status
```

**settings.json direkt prüfen:**
```bash
cat ~/.claude/settings.json | grep -A5 "hooks"
```

Fehlt der hooks-Schlüssel, `tetora hooks install` erneut ausführen.

**Prüfen, ob der Daemon Hook-Ereignisse empfangen kann:**
```bash
curl -s -X POST http://localhost:8991/api/hooks/event \
  -H "Content-Type: application/json" \
  -d '{"hook_event_name":"Stop","session_id":"test-123"}'
# Erwartet: {"ok":true}
```

Wenn der Daemon nicht auf dem erwarteten Port lauscht, `listenAddr` in `config.json` prüfen.

### Berechtigungsfehler bei settings.json

Die `settings.json` von Claude Code befindet sich unter `~/.claude/settings.json`. Wenn die Datei einem anderen Benutzer gehört oder eingeschränkte Berechtigungen hat:

```bash
ls -la ~/.claude/settings.json
chmod 644 ~/.claude/settings.json
```

### Dashboard-Workers-Panel ist leer

1. Bestätigen, dass Hooks installiert sind und der Daemon läuft.
2. Eine Claude Code Session manuell starten und ein Tool ausführen (z. B. `ls`).
3. Das Dashboard-Workers-Panel prüfen — die Session sollte innerhalb von Sekunden erscheinen.
4. Andernfalls Daemon-Logs prüfen: `tetora logs -f | grep hooks`

### Plan-Gate erscheint nicht

Das Plan-Gate wird nur aktiviert, wenn Claude Code versucht, `ExitPlanMode` aufzurufen. Dies geschieht nur in Plan-Mode-Sessions (gestartet mit `--plan` oder über `permissionMode: "plan"` in der Rollenkonfiguration). Interaktive `acceptEdits`-Sessions verwenden keinen Plan-Modus.

### Fragen werden nicht zur Chat-Plattform weitergeleitet

Der `AskUserQuestion`-Deny-Hook erfordert eine konfigurierte MCP-Bridge. `tetora hooks install` erneut ausführen — dies generiert die Bridge-Konfiguration neu. Anschließend die Bridge zu den Claude Code MCP-Einstellungen hinzufügen:

```bash
cat ~/.tetora/mcp/bridge.json
```

Diese Datei als MCP-Server in `~/.claude/settings.json` unter `mcpServers` eintragen.

---

## Migration von tmux (v1.x)

In Tetora v1.x liefen Agents in tmux-Panes und Tetora überwachte sie durch Lesen der Pane-Ausgabe. In v2.0 laufen Agents als einfache Claude Code Prozesse und Tetora beobachtet sie über Hooks.

**Bei einem Upgrade von v1.x:**

1. Nach dem Upgrade einmalig `tetora hooks install` ausführen.
2. Jegliche tmux-Session-Management-Konfiguration aus `config.json` entfernen (`tmux.*`-Schlüssel werden nun ignoriert).
3. Der bestehende Session-Verlauf bleibt in `history.db` erhalten — keine Migration erforderlich.
4. Der Befehl `tetora session list` und der Sessions-Tab im Dashboard funktionieren weiterhin wie gewohnt.

Die tmux-Terminal-Bridge (`discord_terminal.go`) ist weiterhin für interaktiven Terminal-Zugriff über Discord verfügbar. Dies ist von der Agent-Ausführung getrennt — sie ermöglicht das Senden von Tastendrücken an eine laufende Terminal-Session. Hooks und die Terminal-Bridge ergänzen sich und schließen sich nicht gegenseitig aus.
