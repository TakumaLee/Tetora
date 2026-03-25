---
title: "MCP (Model Context Protocol) Integration"
lang: "de"
order: 5
description: "Expose Tetora capabilities to any MCP-compatible client."
---
# MCP (Model Context Protocol) Integration

Tetora enthält einen integrierten MCP-Server, der es KI-Agents (Claude Code usw.) ermöglicht, über das standardisierte MCP-Protokoll mit Tetoras APIs zu interagieren.

## Architektur

```
Claude Code  ──stdio──>  tetora mcp-server  ──HTTP──>  Tetora Daemon
  (client)                (bridge process)              (localhost:8991)
```

Der MCP-Server ist eine **stdio JSON-RPC 2.0 Bridge** — er liest Anfragen von stdin, leitet sie an Tetoras HTTP-API weiter und schreibt Antworten nach stdout. Claude Code startet ihn als Kindprozess.

## Einrichtung

### 1. MCP-Server zu Claude Code-Einstellungen hinzufügen

Folgendes in `~/.claude/settings.json` eintragen:

```json
{
  "mcpServers": {
    "tetora": {
      "command": "/Users/you/.tetora/bin/tetora",
      "args": ["mcp-server"]
    }
  }
}
```

Den Pfad durch den tatsächlichen Speicherort der `tetora`-Binary ersetzen. Auffinden mit:

```bash
which tetora
# oder
ls ~/.tetora/bin/tetora
```

### 2. Sicherstellen, dass der Tetora-Daemon läuft

Die MCP-Bridge leitet Anfragen an die Tetora-HTTP-API weiter, daher muss der Daemon laufen:

```bash
tetora start
```

### 3. Überprüfen

Claude Code neu starten. Die MCP-Tools erscheinen dann als verfügbare Tools mit dem Präfix `tetora_`.

## Verfügbare Tools

### Aufgabenverwaltung

| Tool | Beschreibung |
|------|-------------|
| `tetora_taskboard_list` | Kanban-Board-Tickets auflisten. Optionale Filter: `project`, `assignee`, `priority`. |
| `tetora_taskboard_update` | Eine Aufgabe aktualisieren (Status, Zugewiesener, Priorität, Titel). Erfordert `id`. |
| `tetora_taskboard_comment` | Einen Kommentar zu einer Aufgabe hinzufügen. Erfordert `id` und `comment`. |

### Memory

| Tool | Beschreibung |
|------|-------------|
| `tetora_memory_get` | Einen Memory-Eintrag lesen. Erfordert `agent` und `key`. |
| `tetora_memory_set` | Einen Memory-Eintrag schreiben. Erfordert `agent`, `key` und `value`. |
| `tetora_memory_search` | Alle Memory-Einträge auflisten. Optionaler Filter: `role`. |

### Dispatch

| Tool | Beschreibung |
|------|-------------|
| `tetora_dispatch` | Eine Aufgabe an einen anderen Agent dispatchen. Erstellt eine neue Claude Code Session. Erfordert `prompt`. Optional: `agent`, `workdir`, `model`. |

### Knowledge

| Tool | Beschreibung |
|------|-------------|
| `tetora_knowledge_search` | Die gemeinsame Wissensbasis durchsuchen. Erfordert `q`. Optional: `limit`. |

### Benachrichtigungen

| Tool | Beschreibung |
|------|-------------|
| `tetora_notify` | Eine Benachrichtigung an den Benutzer über Discord/Telegram senden. Erfordert `message`. Optional: `level` (info/warn/error). |
| `tetora_ask_user` | Dem Benutzer eine Frage über Discord stellen und auf eine Antwort warten (bis zu 6 Minuten). Erfordert `question`. Optional: `options` (Schnellantwort-Schaltflächen, max. 4). |

## Tool-Details

### tetora_taskboard_list

```json
{
  "project": "tetora",
  "assignee": "kokuyou",
  "priority": "P0"
}
```

Alle Parameter sind optional. Gibt ein JSON-Array von Aufgaben zurück.

### tetora_taskboard_update

```json
{
  "id": "TASK-42",
  "status": "in_progress",
  "assignee": "kokuyou",
  "priority": "P1",
  "title": "New title"
}
```

Nur `id` ist erforderlich. Andere Felder werden nur aktualisiert, wenn sie angegeben sind. Statuswerte: `todo`, `in_progress`, `review`, `done`.

### tetora_taskboard_comment

```json
{
  "id": "TASK-42",
  "comment": "Started working on this",
  "author": "kokuyou"
}
```

### tetora_dispatch

```json
{
  "prompt": "Fix the broken CSS on the dashboard sidebar",
  "agent": "kokuyou",
  "workdir": "/path/to/project",
  "model": "sonnet"
}
```

Nur `prompt` ist erforderlich. Wenn `agent` weggelassen wird, leitet Tetoras Smart Dispatch zum besten Agent weiter.

### tetora_ask_user

```json
{
  "question": "Should I proceed with the database migration?",
  "options": ["Yes", "No", "Skip for now"]
}
```

Dies ist ein **blockierender Aufruf** — er wartet bis zu 6 Minuten auf eine Antwort des Benutzers über Discord. Der Benutzer sieht die Frage mit optionalen Schnellantwort-Schaltflächen und kann auch eine eigene Antwort eingeben.

## CLI-Befehle

### Externe MCP-Server verwalten

Tetora kann auch als MCP-**Host** fungieren und sich mit externen MCP-Servern verbinden:

```bash
# Konfigurierte MCP-Server auflisten
tetora mcp list

# Vollständige Konfiguration eines Servers anzeigen
tetora mcp show <name>

# Neuen MCP-Server hinzufügen
tetora mcp add <name> --command CMD [--args A1,A2] [--env K=V,K2=V2]

# Server-Konfiguration entfernen
tetora mcp remove <name>

# Server-Verbindung testen
tetora mcp test <name>
```

### MCP-Bridge starten

```bash
# Den MCP-Bridge-Server starten (wird normalerweise von Claude Code gestartet, nicht manuell)
tetora mcp-server
```

Beim ersten Aufruf wird `~/.tetora/mcp/bridge.json` mit dem korrekten Binary-Pfad generiert.

## Konfiguration

MCP-bezogene Einstellungen in `config.json`:

| Feld | Typ | Standard | Beschreibung |
|------|------|---------|-------------|
| `mcpServers` | object | `{}` | Map externer MCP-Server-Konfigurationen (Name → {command, args, env}). |

Der Bridge-Server liest `listenAddr` und `apiToken` aus der Hauptkonfiguration, um sich mit dem Daemon zu verbinden.

## Authentifizierung

Wenn `apiToken` in `config.json` gesetzt ist, fügt die MCP-Bridge automatisch `Authorization: Bearer <token>` in alle HTTP-Anfragen an den Daemon ein. Keine zusätzliche MCP-Level-Authentifizierung erforderlich.

## Fehlerbehebung

**Tools erscheinen nicht in Claude Code:**
- Den Binary-Pfad in `settings.json` auf Korrektheit prüfen
- Sicherstellen, dass der Tetora-Daemon läuft (`tetora start`)
- Claude Code-Logs auf MCP-Verbindungsfehler prüfen

**"HTTP 401"-Fehler:**
- Das `apiToken` in `config.json` muss übereinstimmen. Die Bridge liest es automatisch.

**"connection refused"-Fehler:**
- Der Daemon läuft nicht, oder `listenAddr` stimmt nicht überein. Standard: `127.0.0.1:8991`.

**`tetora_ask_user` läuft in den Timeout:**
- Der Benutzer hat 6 Minuten Zeit, über Discord zu antworten. Sicherstellen, dass der Discord-Bot verbunden ist.
