---
title: "Konfigurationsreferenz"
lang: "de"
order: 1
description: "Configure Tetora via ~/.tetora/config.json — models, providers, and runtime settings."
---
# Konfigurationsreferenz

## Übersicht

Tetora wird durch eine einzelne JSON-Datei unter `~/.tetora/config.json` konfiguriert.

**Wichtige Verhaltensweisen:**

- **`$ENV_VAR`-Substitution** — Jeder Zeichenkettenwert, der mit `$` beginnt, wird beim Start durch die entsprechende Umgebungsvariable ersetzt. Verwende dies für Secrets (API-Schlüssel, Tokens) statt sie fest einzukodieren.
- **Hot-Reload** — Das Senden von `SIGHUP` an den Daemon lädt die Konfiguration neu. Eine fehlerhafte Konfiguration wird abgelehnt und die laufende Konfiguration beibehalten; der Daemon stürzt nicht ab.
- **Relative Pfade** — `jobsFile`, `historyDB`, `defaultWorkdir` und Verzeichnisfelder werden relativ zum Verzeichnis der Konfigurationsdatei (`~/.tetora/`) aufgelöst.
- **Abwärtskompatibilität** — Der alte Schlüssel `"roles"` ist ein Alias für `"agents"`. Der alte Schlüssel `"defaultRole"` innerhalb von `smartDispatch` ist ein Alias für `"defaultAgent"`.

---

## Felder der obersten Ebene

### Grundeinstellungen

| Feld | Typ | Standard | Beschreibung |
|---|---|---|---|
| `listenAddr` | string | `"127.0.0.1:8991"` | HTTP-Adresse für API und Dashboard. Format: `host:port`. |
| `apiToken` | string | `""` | Bearer-Token, der für alle API-Anfragen erforderlich ist. Leer bedeutet keine Authentifizierung (nicht für den Produktionsbetrieb empfohlen). Unterstützt `$ENV_VAR`. |
| `maxConcurrent` | int | `8` | Maximale Anzahl gleichzeitiger Agent-Aufgaben. Werte über 20 erzeugen eine Startwarnung. |
| `defaultModel` | string | `"sonnet"` | Standard-Claude-Modellname. Wird an den Provider übergeben, sofern nicht pro Agent überschrieben. |
| `defaultTimeout` | string | `"1h"` | Standard-Aufgaben-Timeout. Go-Dauerformat: `"15m"`, `"1h"`, `"30s"`. |
| `defaultBudget` | float64 | `0` | Standard-Kostenbudget pro Aufgabe in USD. `0` bedeutet kein Limit. |
| `defaultPermissionMode` | string | `"acceptEdits"` | Standard-Claude-Berechtigungsmodus. Häufige Werte: `"acceptEdits"`, `"default"`. |
| `defaultAgent` | string | `""` | Systemweiter Fallback-Agent, wenn keine Routing-Regel zutrifft. |
| `defaultWorkdir` | string | `""` | Standard-Arbeitsverzeichnis für Agent-Aufgaben. Muss auf dem Datenträger vorhanden sein. |
| `claudePath` | string | `"claude"` | Pfad zur `claude`-CLI-Binary. Standardmäßig wird `claude` über `$PATH` gesucht. |
| `defaultProvider` | string | `"claude"` | Name des Providers, wenn keine Überschreibung auf Agent-Ebene gesetzt ist. |
| `log` | bool | `false` | Legacy-Flag zur Aktivierung der Dateiprotokollierung. Bevorzuge stattdessen `logging.level`. |
| `maxPromptLen` | int | `102400` | Maximale Promptlänge in Bytes (100 KB). Anfragen, die diesen Wert überschreiten, werden abgelehnt. |
| `configVersion` | int | `0` | Konfigurationsschema-Version. Wird für die automatische Migration verwendet. Nicht manuell setzen. |
| `encryptionKey` | string | `""` | AES-Schlüssel für die Feldverschlüsselung sensibler Daten. Unterstützt `$ENV_VAR`. |
| `streamToChannels` | bool | `false` | Leitet den Live-Aufgabenstatus an verbundene Messaging-Kanäle (Discord, Telegram usw.) weiter. |
| `cronNotify` | bool\|null | `null` (true) | `false` unterdrückt alle Cron-Job-Abschlussbenachrichtigungen. `null` oder `true` aktiviert sie. |
| `cronReplayHours` | int | `2` | Wie viele Stunden beim Daemon-Start nach verpassten Cron-Jobs zurückgeschaut wird. |
| `diskBudgetGB` | float64 | `1.0` | Mindestens verfügbarer Festplattenspeicher in GB. Cron-Jobs werden unterhalb dieses Werts abgelehnt. |
| `diskWarnMB` | int | `500` | Warnschwelle für freien Speicher in MB. Protokolliert eine WARN-Meldung, aber Jobs werden weiter ausgeführt. |
| `diskBlockMB` | int | `200` | Blockierungsschwelle für freien Speicher in MB. Jobs werden mit Status `skipped_disk_full` übersprungen. |

### Verzeichnis-Überschreibungen

Standardmäßig befinden sich alle Verzeichnisse unter `~/.tetora/`. Überschreibe sie nur, wenn ein nicht-standardmäßiges Layout erforderlich ist.

| Feld | Typ | Standard | Beschreibung |
|---|---|---|---|
| `knowledgeDir` | string | `~/.tetora/knowledge/` | Verzeichnis für Workspace-Wissensdateien. |
| `agentsDir` | string | `~/.tetora/agents/` | Verzeichnis mit den SOUL.md-Dateien der einzelnen Agents. |
| `workspaceDir` | string | `~/.tetora/workspace/` | Verzeichnis für Regeln, Memory, Skills, Entwürfe usw. |
| `runtimeDir` | string | `~/.tetora/runtime/` | Verzeichnis für Sessions, Ausgaben, Logs, Cache. |
| `vaultDir` | string | `~/.tetora/vault/` | Verzeichnis für den verschlüsselten Secrets-Vault. |
| `historyDB` | string | `history.db` | SQLite-Datenbankpfad für den Job-Verlauf. Relativ zum Konfigurationsverzeichnis. |
| `jobsFile` | string | `jobs.json` | Pfad zur Cron-Jobs-Definitionsdatei. Relativ zum Konfigurationsverzeichnis. |

### Global erlaubte Verzeichnisse

```json
{
  "allowedDirs": ["/Users/me/projects", "/tmp"],
  "defaultAddDirs": ["/Users/me/shared-context"]
}
```

| Feld | Typ | Beschreibung |
|---|---|---|
| `allowedDirs` | string[] | Verzeichnisse, auf die der Agent lesen und schreiben darf. Global angewendet; kann pro Agent eingeschränkt werden. |
| `defaultAddDirs` | string[] | Verzeichnisse, die als `--add-dir` für jede Aufgabe injiziert werden (nur lesender Kontext). |
| `allowedIPs` | string[] | IP-Adressen oder CIDR-Bereiche, die die API aufrufen dürfen. Leer = alle erlaubt. Beispiel: `["192.168.1.0/24", "10.0.0.1"]`. |

---

## Provider

Provider definieren, wie Tetora Agent-Aufgaben ausführt. Mehrere Provider können konfiguriert und pro Agent ausgewählt werden.

```json
{
  "defaultProvider": "claude",
  "providers": {
    "claude": {
      "type": "claude-cli",
      "path": "/usr/local/bin/claude"
    },
    "openai": {
      "type": "openai-compatible",
      "baseUrl": "https://api.openai.com/v1",
      "apiKey": "$OPENAI_API_KEY",
      "model": "gpt-4o"
    },
    "claude-api": {
      "type": "claude-api",
      "apiKey": "$ANTHROPIC_API_KEY",
      "model": "claude-sonnet-4-5",
      "maxTokens": 8192,
      "firstTokenTimeout": "60s"
    }
  }
}
```

### `providers` — `ProviderConfig`

| Feld | Typ | Standard | Beschreibung |
|---|---|---|---|
| `type` | string | erforderlich | Provider-Typ. Einer von: `"claude-cli"`, `"openai-compatible"`, `"claude-api"`, `"claude-code"`. |
| `path` | string | `""` | Binary-Pfad. Wird von den Typen `claude-cli` und `claude-code` verwendet. Fällt auf `claudePath` zurück, wenn leer. |
| `baseUrl` | string | `""` | API-Basis-URL. Erforderlich für `openai-compatible`. |
| `apiKey` | string | `""` | API-Schlüssel. Unterstützt `$ENV_VAR`. Erforderlich für `claude-api`; optional für `openai-compatible`. |
| `model` | string | `""` | Standardmodell für diesen Provider. Überschreibt `defaultModel` für Aufgaben, die diesen Provider verwenden. |
| `maxTokens` | int | `8192` | Maximale Ausgabe-Tokens (wird von `claude-api` verwendet). |
| `firstTokenTimeout` | string | `"60s"` | Wartezeit auf das erste Antwort-Token, bevor ein Timeout ausgelöst wird (SSE-Stream). |

**Provider-Typen:**
- `claude-cli` — führt die `claude`-Binary als Subprocess aus (Standard, breiteste Kompatibilität)
- `claude-api` — ruft die Anthropic-API direkt über HTTP auf (erfordert `ANTHROPIC_API_KEY`)
- `openai-compatible` — jede OpenAI-kompatible REST-API (OpenAI, Ollama, Groq usw.)
- `claude-code` — verwendet den Claude Code CLI-Modus

---

## Agents

Agents definieren benannte Personas mit eigenem Modell, Soul-Datei und Tool-Zugriff.

```json
{
  "agents": {
    "coordinator": {
      "soulFile": "SOUL.md",
      "model": "sonnet",
      "description": "Handles planning, research, and coordination.",
      "keywords": ["plan", "research", "coordinate"]
    },
    "engineer": {
      "soulFile": "team/engineer/SOUL.md",
      "model": "sonnet",
      "provider": "claude",
      "description": "Handles coding, debugging, and infrastructure.",
      "keywords": ["code", "debug", "deploy"],
      "permissionMode": "acceptEdits",
      "allowedDirs": ["/Users/me/projects"],
      "trustLevel": "auto"
    }
  }
}
```

### `agents` — `AgentConfig`

| Feld | Typ | Standard | Beschreibung |
|---|---|---|---|
| `soulFile` | string | erforderlich | Pfad zur SOUL.md-Persönlichkeitsdatei des Agents, relativ zu `agentsDir`. |
| `model` | string | `defaultModel` | Zu verwendendes Modell für diesen Agent. |
| `description` | string | `""` | Menschenlesbare Beschreibung. Wird auch vom LLM-Klassifikator für das Routing verwendet. |
| `keywords` | string[] | `[]` | Schlüsselwörter, die das Routing zu diesem Agent im Smart Dispatch auslösen. |
| `provider` | string | `defaultProvider` | Provider-Name (Schlüssel in der `providers`-Map). |
| `permissionMode` | string | `defaultPermissionMode` | Claude-Berechtigungsmodus für diesen Agent. |
| `allowedDirs` | string[] | `allowedDirs` | Dateisystempfade, auf die dieser Agent zugreifen kann. Überschreibt die globale Einstellung. |
| `docker` | bool\|null | `null` | Agentspezifische Docker-Sandbox-Überschreibung. `null` = globales `docker.enabled` erben. |
| `fallbackProviders` | string[] | `[]` | Geordnete Liste von Fallback-Provider-Namen, wenn der primäre ausfällt. |
| `trustLevel` | string | `"auto"` | Vertrauensebene: `"observe"` (nur lesen), `"suggest"` (vorschlagen, aber nicht anwenden), `"auto"` (volle Autonomie). |
| `tools` | AgentToolPolicy | `{}` | Tool-Zugriffsrichtlinie. Siehe [Tool-Richtlinie](#tool-policy). |
| `toolProfile` | string | `"standard"` | Benanntes Tool-Profil: `"minimal"`, `"standard"`, `"full"`. |
| `workspace` | WorkspaceConfig | `{}` | Workspace-Isolierungseinstellungen. |

---

## Smart Dispatch

Smart Dispatch leitet eingehende Aufgaben automatisch anhand von Regeln, Schlüsselwörtern und LLM-Klassifizierung an den am besten geeigneten Agent weiter.

```json
{
  "smartDispatch": {
    "enabled": true,
    "coordinator": "coordinator",
    "defaultAgent": "coordinator",
    "classifyBudget": 0.1,
    "classifyTimeout": "30s",
    "review": false,
    "reviewLoop": false,
    "maxRetries": 3,
    "fallback": "smart",
    "rules": [
      {
        "agent": "engineer",
        "keywords": ["bug", "error", "deploy", "docker"],
        "patterns": ["(?:fix|resolve)\\s+(?:bug|issue|error)"]
      },
      {
        "agent": "creator",
        "keywords": ["blog post", "documentation", "README"]
      }
    ],
    "bindings": [
      {
        "channel": "discord",
        "channelId": "123456789",
        "agent": "engineer"
      }
    ]
  }
}
```

### `smartDispatch` — `SmartDispatchConfig`

| Feld | Typ | Standard | Beschreibung |
|---|---|---|---|
| `enabled` | bool | `false` | Smart Dispatch Routing aktivieren. |
| `coordinator` | string | erster Agent | Agent, der für die LLM-basierte Aufgabenklassifizierung verwendet wird. |
| `defaultAgent` | string | erster Agent | Fallback-Agent, wenn keine Regel zutrifft. |
| `classifyBudget` | float64 | `0.1` | Kostenbudget (USD) für den Klassifizierungs-LLM-Aufruf. |
| `classifyTimeout` | string | `"30s"` | Timeout für den Klassifizierungsaufruf. |
| `review` | bool | `false` | Nach Aufgabenabschluss einen Review-Agent auf die Ausgabe ausführen. |
| `reviewLoop` | bool | `false` | Dev↔QA-Wiederholungsschleife aktivieren: Review → Feedback → Wiederholung (bis zu `maxRetries`). |
| `maxRetries` | int | `3` | Maximale QA-Wiederholungsversuche in der Review-Schleife. |
| `reviewAgent` | string | coordinator | Agent, der für die Überprüfung der Ausgabe zuständig ist. Für adversariales Review auf einen strengen QA-Agent setzen. |
| `reviewBudget` | float64 | `0.2` | Kostenbudget (USD) für den Review-LLM-Aufruf. |
| `fallback` | string | `"smart"` | Fallback-Strategie: `"smart"` (LLM-Routing) oder `"coordinator"` (immer Standard-Agent). |
| `rules` | RoutingRule[] | `[]` | Schlüsselwort-/Regex-Routing-Regeln, die vor der LLM-Klassifizierung ausgewertet werden. |
| `bindings` | RoutingBinding[] | `[]` | Kanal/Benutzer/Gilde → Agent-Bindungen (höchste Priorität, zuerst ausgewertet). |

### `rules` — `RoutingRule`

| Feld | Typ | Beschreibung |
|---|---|---|
| `agent` | string | Name des Ziel-Agents. |
| `keywords` | string[] | Schlüsselwörter (Groß-/Kleinschreibung ignoriert). Jede Übereinstimmung leitet zu diesem Agent weiter. |
| `patterns` | string[] | Go-Regex-Muster. Jede Übereinstimmung leitet zu diesem Agent weiter. |

### `bindings` — `RoutingBinding`

| Feld | Typ | Beschreibung |
|---|---|---|
| `channel` | string | Plattform: `"telegram"`, `"discord"`, `"slack"` usw. |
| `userId` | string | Benutzer-ID auf dieser Plattform. |
| `channelId` | string | Kanal- oder Chat-ID. |
| `guildId` | string | Gild-/Server-ID (nur Discord). |
| `agent` | string | Name des Ziel-Agents. |

---

## Session

Steuert, wie der Konversationskontext über mehrere Interaktionen hinweg gepflegt und komprimiert wird.

```json
{
  "session": {
    "contextMessages": 20,
    "compactAfter": 30,
    "compactKeep": 10,
    "compactTokens": 200000,
    "compaction": {
      "enabled": true,
      "maxMessages": 50,
      "compactTo": 10,
      "model": "haiku",
      "maxCost": 0.02,
      "provider": "claude"
    }
  }
}
```

### `session` — `SessionConfig`

| Feld | Typ | Standard | Beschreibung |
|---|---|---|---|
| `contextMessages` | int | `20` | Maximale Anzahl der zuletzt empfangenen Nachrichten, die als Kontext in eine neue Aufgabe injiziert werden. |
| `compactAfter` | int | `30` | Komprimierung, wenn die Nachrichtenanzahl diesen Wert übersteigt. Veraltet: verwende `compaction.maxMessages`. |
| `compactKeep` | int | `10` | Letzte N Nachrichten nach der Komprimierung behalten. Veraltet: verwende `compaction.compactTo`. |
| `compactTokens` | int | `200000` | Komprimierung, wenn die Gesamt-Eingabe-Tokens diesen Schwellenwert überschreiten. |

### `session.compaction` — `CompactionConfig`

| Feld | Typ | Standard | Beschreibung |
|---|---|---|---|
| `enabled` | bool | `false` | Automatische Session-Komprimierung aktivieren. |
| `maxMessages` | int | `50` | Komprimierung auslösen, wenn die Session diese Nachrichtenanzahl überschreitet. |
| `compactTo` | int | `10` | Anzahl der zuletzt empfangenen Nachrichten, die nach der Komprimierung behalten werden. |
| `model` | string | `"haiku"` | LLM-Modell zur Erstellung der Komprimierungszusammenfassung. |
| `maxCost` | float64 | `0.02` | Maximale Kosten pro Komprimierungsaufruf (USD). |
| `provider` | string | `defaultProvider` | Provider für den Komprimierungszusammenfassungsaufruf. |

---

## Task Board

Das integrierte Task Board verfolgt Arbeitsaufgaben und kann sie automatisch an Agents verteilen.

```json
{
  "taskBoard": {
    "enabled": true,
    "maxRetries": 3,
    "requireReview": false,
    "defaultWorkflow": "",
    "gitCommit": false,
    "gitPush": false,
    "gitPR": false,
    "gitWorktree": false,
    "gitWorkflow": {
      "branchConvention": "{type}/{agent}-{description}",
      "types": ["feat", "fix", "refactor", "chore"],
      "defaultType": "feat",
      "autoMerge": false
    },
    "autoDispatch": {
      "enabled": false,
      "interval": "5m",
      "maxConcurrentTasks": 3,
      "stuckThreshold": "2h",
      "reviewLoop": false
    }
  }
}
```

### `taskBoard` — `TaskBoardConfig`

| Feld | Typ | Standard | Beschreibung |
|---|---|---|---|
| `enabled` | bool | `false` | Task Board aktivieren. |
| `maxRetries` | int | `3` | Maximale Wiederholungsversuche pro Aufgabe, bevor sie als fehlgeschlagen markiert wird. |
| `requireReview` | bool | `false` | Qualitätskontrolle: Aufgabe muss einen Review bestehen, bevor sie als erledigt gilt. |
| `defaultWorkflow` | string | `""` | Workflow-Name für alle automatisch verteilten Aufgaben. Leer = kein Workflow. |
| `gitCommit` | bool | `false` | Auto-Commit, wenn eine Aufgabe als erledigt markiert wird. |
| `gitPush` | bool | `false` | Auto-Push nach dem Commit (erfordert `gitCommit: true`). |
| `gitPR` | bool | `false` | Automatisch einen GitHub-PR nach dem Push erstellen (erfordert `gitPush: true`). |
| `gitWorktree` | bool | `false` | Git Worktrees zur Aufgabenisolierung verwenden (vermeidet Dateikonflikte zwischen gleichzeitigen Aufgaben). |
| `idleAnalyze` | bool | `false` | Automatische Analyse ausführen, wenn das Board im Leerlauf ist. |
| `problemScan` | bool | `false` | Aufgabenausgabe nach Abschluss auf latente Probleme scannen. |

### `taskBoard.autoDispatch` — `TaskBoardDispatchConfig`

| Feld | Typ | Standard | Beschreibung |
|---|---|---|---|
| `enabled` | bool | `false` | Automatisches Polling und Dispatching von Aufgaben in der Warteschlange aktivieren. |
| `interval` | string | `"5m"` | Wie oft nach bereiten Aufgaben gesucht wird. |
| `maxConcurrentTasks` | int | `3` | Maximale Aufgaben, die pro Scan-Zyklus verteilt werden. |
| `defaultModel` | string | `""` | Modell für automatisch verteilte Aufgaben überschreiben. |
| `maxBudget` | float64 | `0` | Maximale Kosten pro Aufgabe (USD). `0` = kein Limit. |
| `defaultAgent` | string | `""` | Fallback-Agent für nicht zugewiesene Aufgaben. |
| `backlogAgent` | string | `""` | Agent für die Backlog-Triage. |
| `reviewAgent` | string | `""` | Agent zur Überprüfung abgeschlossener Aufgaben. |
| `escalateAssignee` | string | `""` | Vom Review abgelehnte Aufgaben diesem Benutzer zuweisen. |
| `stuckThreshold` | string | `"2h"` | Aufgaben, die länger als diese Zeitspanne im Status "doing" verbleiben, werden auf "todo" zurückgesetzt. |
| `backlogTriageInterval` | string | `"1h"` | Wie oft die Backlog-Triage ausgeführt wird. |
| `reviewLoop` | bool | `false` | Automatische Dev↔QA-Schleife für verteilte Aufgaben aktivieren. |

### `taskBoard.gitWorkflow` — `GitWorkflowConfig`

| Feld | Typ | Standard | Beschreibung |
|---|---|---|---|
| `branchConvention` | string | `"{type}/{agent}-{description}"` | Branch-Benennungsvorlage. Variablen: `{type}`, `{agent}`, `{description}`. |
| `types` | string[] | `["feat","fix","refactor","chore"]` | Erlaubte Branch-Typ-Präfixe. |
| `defaultType` | string | `"feat"` | Fallback-Typ, wenn keiner angegeben ist. |
| `autoMerge` | bool | `false` | Automatisch zurück nach main mergen, wenn die Aufgabe erledigt ist (nur wenn `gitWorktree: true`). |

---

## Slot Pressure

Steuert das Systemverhalten, wenn die `maxConcurrent`-Slot-Grenze erreicht wird. Interaktive (von Menschen initiierte) Sessions erhalten reservierte Slots; Hintergrundaufgaben warten.

```json
{
  "slotPressure": {
    "enabled": true,
    "reservedSlots": 2,
    "warnThreshold": 3,
    "nonInteractiveTimeout": "5m",
    "monitorEnabled": false,
    "monitorInterval": "30s"
  }
}
```

### `slotPressure` — `SlotPressureConfig`

| Feld | Typ | Standard | Beschreibung |
|---|---|---|---|
| `enabled` | bool | `false` | Slot-Pressure-Management aktivieren. |
| `reservedSlots` | int | `2` | Für interaktive Sessions reservierte Slots. Hintergrundaufgaben können diese nicht nutzen. |
| `warnThreshold` | int | `3` | Warnung ausgeben, wenn weniger als diese Anzahl Slots verfügbar sind. |
| `nonInteractiveTimeout` | string | `"5m"` | Wie lange eine Hintergrundaufgabe auf einen Slot wartet, bevor ein Timeout ausgelöst wird. |
| `pollInterval` | string | `"2s"` | Wie oft Hintergrundaufgaben nach einem freien Slot suchen. |
| `monitorEnabled` | bool | `false` | Proaktive Slot-Pressure-Warnungen über Benachrichtigungskanäle aktivieren. |
| `monitorInterval` | string | `"30s"` | Wie oft Pressure-Warnungen geprüft und ausgegeben werden. |

---

## Workflows

Workflows werden als YAML-Dateien in einem Verzeichnis definiert. `workflowDir` zeigt auf dieses Verzeichnis; Variablen liefern Standard-Template-Werte.

```json
{
  "workflowDir": "~/.tetora/workspace/workflows/",
  "workflowTriggers": [
    {
      "event": "task.done",
      "workflow": "notify-slack",
      "filter": {"status": "done"}
    }
  ]
}
```

| Feld | Typ | Standard | Beschreibung |
|---|---|---|---|
| `workflowDir` | string | `~/.tetora/workspace/workflows/` | Verzeichnis, in dem Workflow-YAML-Dateien gespeichert sind. |
| `workflowTriggers` | WorkflowTriggerConfig[] | `[]` | Automatische Workflow-Auslöser bei Systemereignissen. |

---

## Integrationen

### Telegram

```json
{
  "telegram": {
    "enabled": true,
    "botToken": "$TELEGRAM_BOT_TOKEN",
    "chatID": 123456789,
    "pollTimeout": 30
  }
}
```

| Feld | Typ | Standard | Beschreibung |
|---|---|---|---|
| `enabled` | bool | `false` | Telegram-Bot aktivieren. |
| `botToken` | string | `""` | Telegram-Bot-Token von @BotFather. Unterstützt `$ENV_VAR`. |
| `chatID` | int64 | `0` | Telegram-Chat- oder Gruppen-ID für Benachrichtigungen. |
| `pollTimeout` | int | `30` | Long-Poll-Timeout in Sekunden für eingehende Nachrichten. |

### Discord

```json
{
  "discord": {
    "enabled": true,
    "botToken": "$DISCORD_BOT_TOKEN",
    "guildID": "123456789",
    "channelIDs": ["111111111"],
    "mentionChannelIDs": ["222222222"],
    "notifyChannelID": "333333333",
    "showProgress": true,
    "routes": {
      "111111111": {"agent": "engineer"}
    }
  }
}
```

| Feld | Typ | Standard | Beschreibung |
|---|---|---|---|
| `enabled` | bool | `false` | Discord-Bot aktivieren. |
| `botToken` | string | `""` | Discord-Bot-Token. Unterstützt `$ENV_VAR`. |
| `guildID` | string | `""` | Auf einen bestimmten Discord-Server (Guild) einschränken. |
| `channelIDs` | string[] | `[]` | Kanal-IDs, in denen der Bot auf alle Nachrichten antwortet (kein `@`-Mention erforderlich). |
| `mentionChannelIDs` | string[] | `[]` | Kanal-IDs, in denen der Bot nur antwortet, wenn er `@`-erwähnt wird. |
| `notifyChannelID` | string | `""` | Kanal für Aufgabenabschluss-Benachrichtigungen (erstellt einen Thread pro Aufgabe). |
| `showProgress` | bool | `true` | Live-"Arbeitet..."-Streaming-Nachrichten in Discord anzeigen. |
| `webhooks` | map[string]string | `{}` | Benannte Webhook-URLs für ausgehende Benachrichtigungen. |
| `routes` | map[string]DiscordRouteConfig | `{}` | Map von Kanal-ID zu Agent-Name für kanalspezifisches Routing. |

### Slack

```json
{
  "slack": {
    "enabled": true,
    "botToken": "$SLACK_BOT_TOKEN",
    "signingSecret": "$SLACK_SIGNING_SECRET",
    "appToken": "$SLACK_APP_TOKEN",
    "defaultChannel": "C0123456789"
  }
}
```

| Feld | Typ | Standard | Beschreibung |
|---|---|---|---|
| `enabled` | bool | `false` | Slack-Bot aktivieren. |
| `botToken` | string | `""` | Slack-Bot-OAuth-Token (`xoxb-...`). Unterstützt `$ENV_VAR`. |
| `signingSecret` | string | `""` | Slack-Signing-Secret zur Anfrageverifizierung. Unterstützt `$ENV_VAR`. |
| `appToken` | string | `""` | Slack-App-Level-Token für den Socket-Modus (`xapp-...`). Optional. Unterstützt `$ENV_VAR`. |
| `defaultChannel` | string | `""` | Standard-Kanal-ID für ausgehende Benachrichtigungen. |

### Ausgehende Webhooks

```json
{
  "webhooks": [
    {
      "url": "https://hooks.example.com/tetora",
      "headers": {"Authorization": "$WEBHOOK_TOKEN"},
      "events": ["success", "error"]
    }
  ]
}
```

| Feld | Typ | Standard | Beschreibung |
|---|---|---|---|
| `url` | string | erforderlich | Webhook-Endpunkt-URL. |
| `headers` | map[string]string | `{}` | Einzuschließende HTTP-Header. Werte unterstützen `$ENV_VAR`. |
| `events` | string[] | alle | Zu sendende Ereignisse: `"success"`, `"error"`, `"timeout"`, `"all"`. Leer = alle. |

### Eingehende Webhooks

Eingehende Webhooks ermöglichen es externen Diensten, Tetora-Aufgaben per HTTP POST auszulösen.

```json
{
  "incomingWebhooks": {
    "github": {
      "secret": "$GITHUB_WEBHOOK_SECRET",
      "agent": "engineer",
      "prompt": "A GitHub event occurred: {{.Body}}"
    }
  }
}
```

### Benachrichtigungskanäle

Benannte Benachrichtigungskanäle zur Weiterleitung von Aufgabenereignissen an verschiedene Slack-/Discord-Endpunkte.

```json
{
  "notifications": [
    {
      "name": "alerts",
      "type": "slack",
      "webhookUrl": "$SLACK_ALERTS_WEBHOOK",
      "events": ["error"],
      "minPriority": "high"
    }
  ]
}
```

| Feld | Typ | Standard | Beschreibung |
|---|---|---|---|
| `name` | string | `""` | Benannte Referenz, die im `channel`-Feld eines Jobs verwendet wird (z. B. `"discord:alerts"`). |
| `type` | string | erforderlich | `"slack"` oder `"discord"`. |
| `webhookUrl` | string | erforderlich | Webhook-URL. Unterstützt `$ENV_VAR`. |
| `events` | string[] | alle | Nach Ereignistyp filtern: `"all"`, `"error"`, `"success"`. |
| `minPriority` | string | alle | Mindestpriorität: `"critical"`, `"high"`, `"normal"`, `"low"`. |

---

## Store (Template-Marktplatz)

```json
{
  "store": {
    "enabled": true,
    "registryUrl": "https://registry.tetora.dev/v1",
    "authToken": "$TETORA_STORE_TOKEN"
  }
}
```

### `store` — `StoreConfig`

| Feld | Typ | Standard | Beschreibung |
|---|---|---|---|
| `enabled` | bool | `false` | Template-Store aktivieren. |
| `registryUrl` | string | `"https://registry.tetora.dev/v1"` | Remote-Registry-URL zum Durchsuchen und Installieren von Templates. |
| `authToken` | string | `""` | Authentifizierungstoken für die Registry. Unterstützt `$ENV_VAR`. |

---

## Kosten und Warnungen

### `costAlert` — `CostAlertConfig`

```json
{
  "costAlert": {
    "dailyLimit": 10.0,
    "weeklyLimit": 50.0,
    "dailyTokenLimit": 1000000,
    "action": "warn"
  }
}
```

| Feld | Typ | Standard | Beschreibung |
|---|---|---|---|
| `dailyLimit` | float64 | `0` | Tägliches Ausgabenlimit in USD. `0` = kein Limit. |
| `weeklyLimit` | float64 | `0` | Wöchentliches Ausgabenlimit in USD. `0` = kein Limit. |
| `dailyTokenLimit` | int | `0` | Tägliches Gesamt-Token-Limit (Eingabe + Ausgabe). `0` = kein Limit. |
| `action` | string | `"warn"` | Aktion bei Limitüberschreitung: `"warn"` (protokollieren und benachrichtigen) oder `"pause"` (neue Aufgaben blockieren). |

### `estimate` — `EstimateConfig`

Kostenschätzung vor der Ausführung einer Aufgabe.

| Feld | Typ | Standard | Beschreibung |
|---|---|---|---|
| `confirmThreshold` | float64 | `1.00` | Bestätigung anfordern, wenn die geschätzten Kosten diesen USD-Wert überschreiten. |
| `defaultOutputTokens` | int | `500` | Fallback-Ausgabe-Token-Schätzung, wenn die tatsächliche Nutzung unbekannt ist. |

### `budgets` — `BudgetConfig`

Kostenbudgets auf Agent- und Team-Ebene.

---

## Protokollierung

```json
{
  "logging": {
    "level": "info",
    "format": "text",
    "file": "~/.tetora/runtime/logs/tetora.log",
    "maxSizeMB": 50,
    "maxFiles": 5
  }
}
```

### `logging` — `LoggingConfig`

| Feld | Typ | Standard | Beschreibung |
|---|---|---|---|
| `level` | string | `"info"` | Log-Level: `"debug"`, `"info"`, `"warn"`, `"error"`. |
| `format` | string | `"text"` | Log-Format: `"text"` (menschenlesbar) oder `"json"` (strukturiert). |
| `file` | string | `runtime/logs/tetora.log` | Log-Dateipfad. Relativ zum Runtime-Verzeichnis. |
| `maxSizeMB` | int | `50` | Maximale Log-Dateigröße in MB vor der Rotation. |
| `maxFiles` | int | `5` | Anzahl der rotierten Log-Dateien, die aufbewahrt werden. |

---

## Sicherheit

### `dashboardAuth` — `DashboardAuthConfig`

```json
{
  "dashboardAuth": {
    "enabled": true,
    "username": "admin",
    "password": "$DASHBOARD_PASSWORD"
  }
}
```

| Feld | Typ | Standard | Beschreibung |
|---|---|---|---|
| `enabled` | bool | `false` | HTTP Basic Auth für das Dashboard aktivieren. |
| `username` | string | `"admin"` | Basic-Auth-Benutzername. |
| `password` | string | `""` | Basic-Auth-Passwort. Unterstützt `$ENV_VAR`. |
| `token` | string | `""` | Alternative: statischer Token, der als Cookie übergeben wird. |

### `tls` — `TLSConfig`

```json
{
  "tls": {
    "certFile": "/etc/tetora/cert.pem",
    "keyFile": "/etc/tetora/key.pem"
  }
}
```

| Feld | Typ | Beschreibung |
|---|---|---|
| `certFile` | string | Pfad zur TLS-Zertifikat-PEM-Datei. Aktiviert HTTPS, wenn gesetzt (zusammen mit `keyFile`). |
| `keyFile` | string | Pfad zur TLS-Privatschlüssel-PEM-Datei. |

### `rateLimit` — `RateLimitConfig`

| Feld | Typ | Standard | Beschreibung |
|---|---|---|---|
| `enabled` | bool | `false` | IP-basierte Anfrage-Ratenbegrenzung aktivieren. |
| `maxPerMin` | int | `60` | Maximale API-Anfragen pro Minute und IP. |

### `securityAlert` — `SecurityAlertConfig`

| Feld | Typ | Standard | Beschreibung |
|---|---|---|---|
| `enabled` | bool | `false` | Sicherheitswarnungen bei wiederholten Authentifizierungsfehlern aktivieren. |
| `failThreshold` | int | `10` | Anzahl der Fehler im Zeitfenster, bevor eine Warnung ausgegeben wird. |
| `failWindowMin` | int | `5` | Gleitendes Zeitfenster in Minuten. |

### `approvalGates` — `ApprovalGateConfig`

Menschliche Genehmigung erforderlich, bevor bestimmte Tools ausgeführt werden.

```json
{
  "approvalGates": {
    "enabled": true,
    "timeout": 120,
    "tools": ["bash", "write_file"],
    "autoApproveTools": ["read_file"]
  }
}
```

| Feld | Typ | Standard | Beschreibung |
|---|---|---|---|
| `enabled` | bool | `false` | Approval Gates aktivieren. |
| `timeout` | int | `120` | Sekunden, die auf Genehmigung gewartet wird, bevor abgebrochen wird. |
| `tools` | string[] | `[]` | Tool-Namen, die vor der Ausführung eine Genehmigung erfordern. |
| `autoApproveTools` | string[] | `[]` | Tools, die beim Start vorab genehmigt werden (keine Abfrage). |

---

## Zuverlässigkeit

### `circuitBreaker` — `CircuitBreakerConfig`

```json
{
  "circuitBreaker": {
    "enabled": true,
    "failThreshold": 5,
    "successThreshold": 2,
    "openTimeout": "30s"
  }
}
```

| Feld | Typ | Standard | Beschreibung |
|---|---|---|---|
| `enabled` | bool | `true` | Circuit Breaker für Provider-Failover aktivieren. |
| `failThreshold` | int | `5` | Aufeinanderfolgende Fehler, bevor der Circuit geöffnet wird. |
| `successThreshold` | int | `2` | Erfolge im halb-offenen Zustand, bevor der Circuit geschlossen wird. |
| `openTimeout` | string | `"30s"` | Dauer im offenen Zustand, bevor ein erneuter Versuch unternommen wird (halb-offen). |

### `fallbackProviders`

```json
{
  "fallbackProviders": ["claude", "openai"]
}
```

Globale geordnete Liste von Fallback-Providern, wenn der Standard-Provider ausfällt.

### `heartbeat` — `HeartbeatConfig`

```json
{
  "heartbeat": {
    "enabled": true,
    "interval": "30s",
    "stallThreshold": "5m",
    "timeoutWarnRatio": 0.8,
    "autoCancel": false,
    "notifyOnStall": true
  }
}
```

| Feld | Typ | Standard | Beschreibung |
|---|---|---|---|
| `enabled` | bool | `false` | Agent-Heartbeat-Überwachung aktivieren. |
| `interval` | string | `"30s"` | Wie oft laufende Aufgaben auf Blockierungen geprüft werden. |
| `stallThreshold` | string | `"5m"` | Keine Ausgabe für diese Dauer = Aufgabe ist blockiert. |
| `timeoutWarnRatio` | float64 | `0.8` | Warnung, wenn die verstrichene Zeit diesen Anteil des Aufgaben-Timeouts überschreitet. |
| `autoCancel` | bool | `false` | Aufgaben automatisch abbrechen, die länger als `2x stallThreshold` blockiert sind. |
| `notifyOnStall` | bool | `true` | Benachrichtigung senden, wenn eine blockierte Aufgabe erkannt wird. |

### `retention` — `RetentionConfig`

Steuert die automatische Bereinigung alter Daten.

```json
{
  "retention": {
    "history": 90,
    "sessions": 30,
    "auditLog": 365,
    "logs": 14,
    "workflows": 90,
    "outputs": 30
  }
}
```

| Feld | Typ | Standard | Beschreibung |
|---|---|---|---|
| `history` | int | `90` | Tage, für die der Job-Ausführungsverlauf aufbewahrt wird. |
| `sessions` | int | `30` | Tage, für die Session-Daten aufbewahrt werden. |
| `auditLog` | int | `365` | Tage, für die Audit-Log-Einträge aufbewahrt werden. |
| `logs` | int | `14` | Tage, für die Log-Dateien aufbewahrt werden. |
| `workflows` | int | `90` | Tage, für die Workflow-Ausführungsprotokolle aufbewahrt werden. |
| `reflections` | int | `60` | Tage, für die Reflection-Protokolle aufbewahrt werden. |
| `sla` | int | `90` | Tage, für die SLA-Prüfprotokolle aufbewahrt werden. |
| `trustEvents` | int | `90` | Tage, für die Trust-Ereignisprotokolle aufbewahrt werden. |
| `handoffs` | int | `60` | Tage, für die Agent-Handoff-/Nachrichtenprotokolle aufbewahrt werden. |
| `queue` | int | `7` | Tage, für die Offline-Warteschlangenelemente aufbewahrt werden. |
| `versions` | int | `180` | Tage, für die Konfigurations-Versions-Snapshots aufbewahrt werden. |
| `outputs` | int | `30` | Tage, für die Agent-Ausgabedateien aufbewahrt werden. |
| `uploads` | int | `7` | Tage, für die hochgeladene Dateien aufbewahrt werden. |
| `memory` | int | `30` | Tage, bevor veraltete Memory-Einträge archiviert werden. |
| `claudeSessions` | int | `3` | Tage, für die Claude-CLI-Session-Artefakte aufbewahrt werden. |
| `piiPatterns` | string[] | `[]` | Regex-Muster zur PII-Schwärzung in gespeicherten Inhalten. |

---

## Ruhezeiten und Digest

```json
{
  "quietHours": {
    "enabled": true,
    "start": "23:00",
    "end": "08:00",
    "tz": "Asia/Taipei",
    "digest": true
  },
  "digest": {
    "enabled": true,
    "time": "08:00",
    "tz": "Asia/Taipei"
  }
}
```

### `quietHours` — `QuietHoursConfig`

| Feld | Typ | Standard | Beschreibung |
|---|---|---|---|
| `enabled` | bool | `false` | Ruhezeiten aktivieren. Benachrichtigungen werden in diesem Zeitraum unterdrückt. |
| `start` | string | `""` | Beginn der Ruhezeit (Lokalzeit, Format `"HH:MM"`). |
| `end` | string | `""` | Ende der Ruhezeit (Lokalzeit). |
| `tz` | string | lokal | Zeitzone, z. B. `"Asia/Taipei"`, `"UTC"`. |
| `digest` | bool | `false` | Zusammenfassung der unterdrückten Benachrichtigungen senden, wenn die Ruhezeit endet. |

### `digest` — `DigestConfig`

| Feld | Typ | Standard | Beschreibung |
|---|---|---|---|
| `enabled` | bool | `false` | Geplanten täglichen Digest aktivieren. |
| `time` | string | `"08:00"` | Uhrzeit für den Digest (`"HH:MM"`). |
| `tz` | string | lokal | Zeitzone. |

---

## Tools

```json
{
  "tools": {
    "maxIterations": 10,
    "timeout": 120,
    "toolOutputLimit": 10240,
    "toolTimeout": 30,
    "defaultProfile": "standard",
    "builtin": {
      "bash": true,
      "web_search": false
    },
    "webSearch": {
      "provider": "brave",
      "apiKey": "$BRAVE_API_KEY",
      "maxResults": 5
    },
    "vision": {
      "provider": "anthropic",
      "apiKey": "$ANTHROPIC_API_KEY",
      "model": "claude-opus-4-5"
    }
  }
}
```

### `tools` — `ToolConfig`

| Feld | Typ | Standard | Beschreibung |
|---|---|---|---|
| `maxIterations` | int | `10` | Maximale Tool-Aufruf-Iterationen pro Aufgabe. |
| `timeout` | int | `120` | Globaler Tool-Engine-Timeout in Sekunden. |
| `toolOutputLimit` | int | `10240` | Maximale Zeichen pro Tool-Ausgabe (wird darüber hinaus abgeschnitten). |
| `toolTimeout` | int | `30` | Ausführungs-Timeout pro Tool in Sekunden. |
| `defaultProfile` | string | `"standard"` | Standard-Tool-Profilname. |
| `builtin` | map[string]bool | `{}` | Eingebaute Tools nach Name aktivieren/deaktivieren. |
| `profiles` | map[string]ToolProfile | `{}` | Benutzerdefinierte Tool-Profile. |
| `trustOverride` | map[string]string | `{}` | Vertrauensebene pro Tool-Name überschreiben. |

### `tools.webSearch` — `WebSearchConfig`

| Feld | Typ | Standard | Beschreibung |
|---|---|---|---|
| `provider` | string | `""` | Suchanbieter: `"brave"`, `"tavily"`, `"searxng"`. |
| `apiKey` | string | `""` | API-Schlüssel für den Anbieter. Unterstützt `$ENV_VAR`. |
| `baseURL` | string | `""` | Benutzerdefinierter Endpunkt (für selbst gehostetes searxng). |
| `maxResults` | int | `5` | Maximale Anzahl der zurückgegebenen Suchergebnisse. |

### `tools.vision` — `VisionConfig`

| Feld | Typ | Standard | Beschreibung |
|---|---|---|---|
| `provider` | string | `""` | Vision-Anbieter: `"anthropic"`, `"openai"`, `"google"`. |
| `apiKey` | string | `""` | API-Schlüssel. Unterstützt `$ENV_VAR`. |
| `model` | string | `""` | Modellname für den Vision-Anbieter. |
| `maxImageSize` | int | `5242880` | Maximale Bildgröße in Bytes (Standard: 5 MB). |
| `baseURL` | string | `""` | Benutzerdefinierter API-Endpunkt. |

---

## MCP (Model Context Protocol)

### `mcpConfigs`

Benannte MCP-Server-Konfigurationen. Jeder Schlüssel ist ein MCP-Konfigurationsname; der Wert ist die vollständige MCP-JSON-Konfiguration. Tetora schreibt diese in temporäre Dateien und übergibt sie der claude-Binary über `--mcp-config`.

```json
{
  "mcpConfigs": {
    "playwright": {
      "mcpServers": {
        "playwright": {
          "command": "npx",
          "args": ["@playwright/mcp@latest"]
        }
      }
    }
  }
}
```

### `mcpServers`

Vereinfachte MCP-Server-Definitionen, die direkt von Tetora verwaltet werden.

```json
{
  "mcpServers": {
    "my-server": {
      "command": "python",
      "args": ["/path/to/server.py"],
      "env": {"API_KEY": "$MY_API_KEY"},
      "enabled": true
    }
  }
}
```

| Feld | Typ | Standard | Beschreibung |
|---|---|---|---|
| `command` | string | erforderlich | Ausführbarer Befehl. |
| `args` | string[] | `[]` | Befehlsargumente. |
| `env` | map[string]string | `{}` | Umgebungsvariablen für den Prozess. Werte unterstützen `$ENV_VAR`. |
| `enabled` | bool | `true` | Ob dieser MCP-Server aktiv ist. |

---

## Prompt-Budget

Steuert die maximalen Zeichenbudgets für jeden Abschnitt des System-Prompts. Anpassen, wenn Prompts unerwartet abgeschnitten werden.

```json
{
  "promptBudget": {
    "soulMax": 8000,
    "rulesMax": 4000,
    "knowledgeMax": 8000,
    "skillsMax": 4000,
    "maxSkillsPerTask": 3,
    "contextMax": 16000,
    "totalMax": 40000
  }
}
```

### `promptBudget` — `PromptBudgetConfig`

| Feld | Typ | Standard | Beschreibung |
|---|---|---|---|
| `soulMax` | int | `8000` | Maximale Zeichen für den Agent-Soul-/Persönlichkeits-Prompt. |
| `rulesMax` | int | `4000` | Maximale Zeichen für Workspace-Regeln. |
| `knowledgeMax` | int | `8000` | Maximale Zeichen für Wissensbasisinhalte. |
| `skillsMax` | int | `4000` | Maximale Zeichen für injizierte Skills. |
| `maxSkillsPerTask` | int | `3` | Maximale Anzahl der pro Aufgabe injizierten Skills. |
| `contextMax` | int | `16000` | Maximale Zeichen für den Session-Kontext. |
| `totalMax` | int | `40000` | Harte Obergrenze für die Gesamtgröße des System-Prompts (alle Abschnitte zusammen). |

---

## Agent-Kommunikation

Steuert den verschachtelten Sub-Agent-Dispatch (agent_dispatch Tool).

```json
{
  "agentComm": {
    "enabled": true,
    "maxConcurrent": 3,
    "defaultTimeout": 900,
    "maxDepth": 3,
    "maxChildrenPerTask": 5
  }
}
```

### `agentComm` — `AgentCommConfig`

| Feld | Typ | Standard | Beschreibung |
|---|---|---|---|
| `enabled` | bool | `false` | Das `agent_dispatch`-Tool für verschachtelte Sub-Agent-Aufrufe aktivieren. |
| `maxConcurrent` | int | `3` | Maximale gleichzeitige `agent_dispatch`-Aufrufe global. |
| `defaultTimeout` | int | `900` | Standard-Sub-Agent-Timeout in Sekunden. |
| `maxDepth` | int | `3` | Maximale Verschachtelungstiefe für Sub-Agents. |
| `maxChildrenPerTask` | int | `5` | Maximale gleichzeitige Kind-Agents pro Elternaufgabe. |

---

## Beispiele

### Minimale Konfiguration

Eine minimale Konfiguration für den Einstieg mit dem Claude CLI-Provider:

```json
{
  "claudePath": "/usr/local/bin/claude",
  "maxConcurrent": 3,
  "listenAddr": "127.0.0.1:8991",
  "apiToken": "$TETORA_API_TOKEN",
  "agents": {
    "coordinator": {
      "soulFile": "SOUL.md",
      "model": "sonnet",
      "description": "General-purpose agent."
    }
  }
}
```

### Multi-Agent-Konfiguration mit Smart Dispatch

```json
{
  "claudePath": "/usr/local/bin/claude",
  "maxConcurrent": 5,
  "defaultModel": "sonnet",
  "defaultTimeout": "30m",
  "defaultBudget": 2.0,
  "defaultPermissionMode": "acceptEdits",
  "listenAddr": "127.0.0.1:8991",
  "apiToken": "$TETORA_API_TOKEN",
  "defaultWorkdir": "~/workspace",
  "agents": {
    "coordinator": {
      "soulFile": "SOUL.md",
      "model": "sonnet",
      "description": "Coordinator. Handles planning, research, and coordination.",
      "keywords": ["plan", "research", "coordinate", "summarize"]
    },
    "engineer": {
      "soulFile": "team/engineer/SOUL.md",
      "model": "sonnet",
      "description": "Engineer. Handles coding, debugging, and infrastructure.",
      "keywords": ["code", "debug", "deploy"]
    },
    "creator": {
      "soulFile": "team/creator/SOUL.md",
      "model": "sonnet",
      "description": "Creator. Handles writing, documentation, and content.",
      "keywords": ["write", "blog", "translate"]
    }
  },
  "smartDispatch": {
    "enabled": true,
    "coordinator": "coordinator",
    "defaultAgent": "coordinator",
    "classifyBudget": 0.1,
    "classifyTimeout": "30s",
    "rules": [
      {
        "agent": "engineer",
        "keywords": ["bug", "error", "deploy", "CI/CD", "docker"],
        "patterns": ["(?:fix|resolve)\\s+(?:bug|issue|error)"]
      },
      {
        "agent": "creator",
        "keywords": ["blog post", "documentation", "README", "translation"]
      }
    ]
  },
  "costAlert": {
    "dailyLimit": 10.0,
    "action": "warn"
  },
  "logging": {
    "level": "info",
    "format": "text"
  }
}
```

### Vollständige Konfiguration (alle wichtigen Abschnitte)

```json
{
  "claudePath": "/usr/local/bin/claude",
  "maxConcurrent": 5,
  "defaultModel": "sonnet",
  "defaultTimeout": "30m",
  "defaultBudget": 2.0,
  "defaultPermissionMode": "acceptEdits",
  "listenAddr": "127.0.0.1:8991",
  "apiToken": "$TETORA_API_TOKEN",

  "providers": {
    "claude": {
      "type": "claude-cli",
      "path": "/usr/local/bin/claude"
    }
  },

  "agents": {
    "coordinator": {
      "soulFile": "SOUL.md",
      "model": "sonnet",
      "description": "Coordinator and general-purpose agent."
    }
  },

  "smartDispatch": {
    "enabled": true,
    "coordinator": "coordinator",
    "defaultAgent": "coordinator",
    "rules": []
  },

  "session": {
    "contextMessages": 20,
    "compaction": {
      "enabled": true,
      "maxMessages": 50,
      "compactTo": 10,
      "model": "haiku"
    }
  },

  "taskBoard": {
    "enabled": true,
    "autoDispatch": {
      "enabled": true,
      "interval": "5m",
      "maxConcurrentTasks": 3
    },
    "gitCommit": true,
    "gitPush": false
  },

  "slotPressure": {
    "enabled": true,
    "reservedSlots": 2,
    "warnThreshold": 3,
    "nonInteractiveTimeout": "5m"
  },

  "telegram": {
    "enabled": false,
    "botToken": "$TELEGRAM_BOT_TOKEN",
    "chatID": 0,
    "pollTimeout": 30
  },

  "discord": {
    "enabled": false,
    "botToken": "$DISCORD_BOT_TOKEN"
  },

  "slack": {
    "enabled": false,
    "botToken": "$SLACK_BOT_TOKEN",
    "signingSecret": "$SLACK_SIGNING_SECRET"
  },

  "store": {
    "enabled": true,
    "registryUrl": "https://registry.tetora.dev/v1"
  },

  "costAlert": {
    "dailyLimit": 10.0,
    "weeklyLimit": 50.0,
    "action": "warn"
  },

  "logging": {
    "level": "info",
    "format": "text",
    "maxSizeMB": 50,
    "maxFiles": 5
  },

  "retention": {
    "history": 90,
    "sessions": 30,
    "logs": 14
  },

  "heartbeat": {
    "enabled": true,
    "stallThreshold": "5m",
    "autoCancel": false
  },

  "dashboardAuth": {
    "enabled": false
  },

  "promptBudget": {
    "soulMax": 8000,
    "rulesMax": 4000,
    "knowledgeMax": 8000,
    "totalMax": 40000
  }
}
```
