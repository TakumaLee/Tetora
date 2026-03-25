---
title: "Fehlerbehebungshandbuch"
lang: "de"
order: 7
description: "Common issues and solutions for Tetora setup and operation."
---
# Fehlerbehebungshandbuch

Dieses Handbuch behandelt die häufigsten Probleme beim Betrieb von Tetora. Für jedes Problem wird die wahrscheinlichste Ursache zuerst genannt.

---

## tetora doctor

Immer hier beginnen. `tetora doctor` nach der Installation oder wenn etwas aufhört zu funktionieren ausführen:

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

Jede Zeile ist eine Prüfung. Ein rotes `✗` bedeutet einen harten Fehler (der Daemon funktioniert ohne Behebung nicht). Ein gelbes `~` ist ein Hinweis (optional, aber empfohlen).

Häufige Lösungen für fehlgeschlagene Prüfungen:

| Fehlgeschlagene Prüfung | Lösung |
|---|---|
| `Config: not found` | `tetora init` ausführen |
| `Claude CLI: not found` | `claudePath` in `config.json` setzen oder Claude Code installieren |
| `sqlite3: not found` | `brew install sqlite3` (macOS) oder `apt install sqlite3` (Linux) |
| `Agent/name: soul file missing` | `~/.tetora/agents/{name}/SOUL.md` erstellen oder `tetora init` ausführen |
| `Workspace: not found` | `tetora init` ausführen, um die Verzeichnisstruktur zu erstellen |

---

## "session produced no output"

Eine Aufgabe wird abgeschlossen, aber die Ausgabe ist leer. Die Aufgabe wird automatisch als `failed` markiert.

**Ursache 1: Kontext-Fenster zu groß.** Der in die Session injizierte Prompt überschreitet das Kontextlimit des Modells. Claude Code beendet sich sofort, wenn der Kontext nicht passt.

Lösung: Session-Komprimierung in `config.json` aktivieren:

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

Oder die Menge des in die Aufgabe injizierten Kontexts reduzieren (kürzere Beschreibung, weniger Spec-Kommentare, kleinere `dependsOn`-Kette).

**Ursache 2: Claude Code CLI-Startfehler.** Die Binary unter `claudePath` stürzt beim Start ab — üblicherweise aufgrund eines falschen API-Schlüssels, eines Netzwerkproblems oder eines Versionsunterschieds.

Lösung: Die Claude Code Binary manuell ausführen, um den Fehler zu sehen:

```bash
/usr/local/bin/claude --version
/usr/local/bin/claude -p "hello"
```

Den gemeldeten Fehler beheben und die Aufgabe dann wiederholen:

```bash
tetora task move task-abc123 --status=todo
```

**Ursache 3: Leerer Prompt.** Die Aufgabe hat einen Titel, aber keine Beschreibung, und der Titel allein ist für den Agent zu mehrdeutig. Die Session wird ausgeführt, erzeugt eine Ausgabe, die die Leerheitsprüfung nicht besteht, und wird markiert.

Lösung: Eine konkrete Beschreibung hinzufügen:

```bash
tetora task update task-abc123 \
  --description="Create src/ratelimit/bucket.go with a token bucket implementation..."
```

---

## "unauthorized"-Fehler im Dashboard

Das Dashboard gibt 401 zurück oder zeigt nach dem Neuladen eine leere Seite.

**Ursache 1: Service Worker hat ein altes Auth-Token gecacht.** Der PWA Service Worker cached Antworten einschließlich Auth-Header. Nach einem Daemon-Neustart mit einem neuen Token ist die gecachte Version veraltet.

Lösung: Seite hart neu laden. In Chrome/Safari:

- Mac: `Cmd + Shift + R`
- Windows/Linux: `Ctrl + Shift + R`

Oder DevTools → Application → Service Workers → "Unregister" klicken, dann neu laden.

**Ursache 2: Referer-Header-Abweichung.** Das Auth-Middleware des Dashboards validiert den `Referer`-Header. Anfragen von Browser-Erweiterungen, Proxys oder curl ohne `Referer`-Header werden abgelehnt.

Lösung: Das Dashboard direkt unter `http://localhost:8991/dashboard` aufrufen, nicht über einen Proxy. Wenn API-Zugriff von externen Tools benötigt wird, statt der Browser-Session-Auth einen API-Token verwenden.

---

## Dashboard wird nicht aktualisiert

Das Dashboard lädt, aber der Aktivitätsfeed, die Worker-Liste oder das Task-Board bleibt veraltet.

**Ursache: Service Worker Versionskonflikt.** Der PWA Service Worker liefert eine gecachte Version des Dashboard-JS/HTML, selbst nach einem `make bump`-Update.

Lösung:

1. Hart neu laden (`Cmd + Shift + R` / `Ctrl + Shift + R`)
2. Wenn das nicht hilft: DevTools → Application → Service Workers → "Update" oder "Unregister" klicken
3. Seite neu laden

**Ursache: SSE-Verbindung unterbrochen.** Das Dashboard empfängt Live-Updates über Server-Sent Events. Wenn die Verbindung abbricht (Netzwerkproblem, Laptop-Schlaf), stoppt der Feed.

Lösung: Seite neu laden. Die SSE-Verbindung wird beim Seitenaufruf automatisch wiederhergestellt.

---

## "排程接近滿載"-Warnung

Diese Meldung erscheint in Discord/Telegram oder im Dashboard-Benachrichtigungsfeed.

Dies ist die Slot-Pressure-Warnung. Sie wird ausgelöst, wenn die verfügbaren Concurrency-Slots auf `warnThreshold` (Standard: 3) oder darunter fallen. Das bedeutet, Tetora arbeitet nahe seiner Kapazitätsgrenze.

**Was zu tun ist:**

- Wenn dies erwartet wird (viele laufende Aufgaben): keine Aktion erforderlich. Die Warnung ist informativ.
- Wenn nicht viele Aufgaben laufen: auf steckengebliebene Aufgaben im Status `doing` prüfen:

```bash
tetora task list --status=doing
```

- Wenn die Kapazität erhöht werden soll: `maxConcurrent` in `config.json` erhöhen und `slotPressure.warnThreshold` entsprechend anpassen.
- Wenn interaktive Sessions verzögert werden: `slotPressure.reservedSlots` erhöhen, um mehr Slots für die interaktive Nutzung zurückzuhalten.

---

## Aufgaben stecken in "doing"

Eine Aufgabe zeigt `status=doing`, aber kein Agent arbeitet aktiv daran.

**Ursache 1: Daemon wurde während der Aufgabe neu gestartet.** Die Aufgabe lief, als der Daemon beendet wurde. Beim nächsten Start prüft Tetora auf verwaiste `doing`-Aufgaben und stellt sie entweder auf `done` wieder her (wenn Kosten-/Dauernachweise vorhanden sind) oder setzt sie auf `todo` zurück.

Dies geschieht automatisch — auf den nächsten Daemon-Start warten. Wenn der Daemon bereits läuft und die Aufgabe noch steckt, wird der Heartbeat oder der Stuck-Task-Reset sie innerhalb von `stuckThreshold` (Standard: 2h) erkennen.

Zum sofortigen erzwungenen Zurücksetzen:

```bash
tetora task move task-abc123 --status=todo
```

**Ursache 2: Heartbeat/Stall-Erkennung.** Der Heartbeat-Monitor (`heartbeat.go`) prüft laufende Sessions. Wenn eine Session für die Stall-Schwelle keine Ausgabe produziert, wird sie automatisch abgebrochen und die Aufgabe auf `failed` gesetzt.

Aufgabenkommentare auf `[auto-reset]`- oder `[stall-detected]`-Systemkommentare prüfen:

```bash
tetora task show task-abc123 --full
```

**Manueller Abbruch über API:**

```bash
curl -X POST http://localhost:8991/api/tasks/task-abc123/cancel
```

---

## Worktree-Merge-Fehler

Eine Aufgabe beendet sich und wechselt zu `partial-done` mit einem Kommentar wie `[worktree] merge failed`.

Das bedeutet, die Änderungen des Agents auf dem Task-Branch konfliktieren mit `main`.

**Wiederherstellungsschritte:**

```bash
# Aufgabendetails und den erstellten Branch prüfen
tetora task show task-abc123 --full

# Zum Projekt-Repository navigieren
cd /path/to/your/repo

# Branch manuell mergen
git merge feat/kokuyou-task-abc123

# Konflikte im Editor lösen, dann committen
git add .
git commit -m "merge: feat/kokuyou-task-abc123"

# Aufgabe als erledigt markieren
tetora task move task-abc123 --status=done
```

Das Worktree-Verzeichnis bleibt unter `~/.tetora/runtime/worktrees/task-abc123/` erhalten, bis es manuell bereinigt oder die Aufgabe auf `done` gesetzt wird.

---

## Hohe Token-Kosten

Sessions verbrauchen mehr Tokens als erwartet.

**Ursache 1: Kontext wird nicht komprimiert.** Ohne Session-Komprimierung akkumuliert jeder Turn den gesamten Konversationsverlauf. Lang laufende Aufgaben (viele Tool-Aufrufe) lassen den Kontext linear wachsen.

Lösung: `sessionCompaction` aktivieren (siehe Abschnitt "session produced no output" oben).

**Ursache 2: Große Knowledge-Base- oder Regel-Dateien.** Dateien in `workspace/rules/` und `workspace/knowledge/` werden in jeden Agent-Prompt injiziert. Sind diese Dateien groß, verbrauchen sie bei jedem Aufruf Tokens.

Lösung:
- `workspace/knowledge/` prüfen — einzelne Dateien unter 50 KB halten.
- Referenzmaterial, das selten benötigt wird, aus den Auto-Inject-Pfaden verschieben.
- `tetora knowledge list` ausführen, um zu sehen, was injiziert wird und welche Größe es hat.

**Ursache 3: Falsches Modell-Routing.** Ein teures Modell (Opus) wird für Routineaufgaben verwendet.

Lösung: `defaultModel` in der Agent-Konfiguration prüfen und ein günstigeres Standardmodell für Massenaufgaben setzen:

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

## Provider-Timeout-Fehler

Aufgaben schlagen mit Timeout-Fehlern wie `context deadline exceeded` oder `provider request timed out` fehl.

**Ursache 1: Aufgaben-Timeout zu kurz.** Das Standard-Timeout ist möglicherweise zu kurz für komplexe Aufgaben.

Lösung: Ein längeres Timeout in der Agent-Konfiguration oder pro Aufgabe setzen:

```json
{
  "roles": {
    "kokuyou": {
      "timeout": "60m"
    }
  }
}
```

Oder die LLM-Timeout-Schätzung erhöhen, indem mehr Details zur Aufgabenbeschreibung hinzugefügt werden (Tetora schätzt das Timeout über einen schnellen Modellaufruf basierend auf der Beschreibung).

**Ursache 2: API-Rate-Limiting oder Überlastung.** Zu viele gleichzeitige Anfragen treffen denselben Provider.

Lösung: `maxConcurrentTasks` reduzieren oder ein `maxBudget` hinzufügen, um teure Aufgaben zu drosseln:

```json
{
  "autoDispatch": {
    "maxConcurrentTasks": 2,
    "maxBudget": 3.0
  }
}
```

---

## `make bump` hat einen Workflow unterbrochen

`make bump` wurde ausgeführt, während ein Workflow oder eine Aufgabe ausgeführt wurde. Der Daemon wurde während der Aufgabe neu gestartet.

Der Neustart löst Tetoras Orphan-Recovery-Logik aus:

- Aufgaben mit Abschlussnachweisen (Kosten aufgezeichnet, Dauer aufgezeichnet) werden auf `done` wiederhergestellt.
- Aufgaben ohne Abschlussnachweise, aber nach der Gnadenfrist (2 Minuten), werden auf `todo` zum erneuten Dispatch zurückgesetzt.
- Aufgaben, die innerhalb der letzten 2 Minuten aktualisiert wurden, bleiben bis zum nächsten Stuck-Task-Scan unangetastet.

**Um zu prüfen, was passiert ist:**

```bash
tetora task list --status=doing
tetora task list --status=failed
```

Aufgabenkommentare auf `[auto-restore]`- oder `[auto-reset]`-Einträge prüfen.

**Wenn Bumps während aktiver Aufgaben verhindert werden sollen** (noch nicht als Flag verfügbar), vor dem Bump sicherstellen, dass keine Aufgaben laufen:

```bash
tetora task list --status=doing
# Wenn leer, sicher zum Bump
make bump
```

---

## SQLite-Fehler

Fehler wie `database is locked`, `SQLITE_BUSY` oder `index.lock` erscheinen in den Logs.

**Ursache 1: Fehlender WAL-Mode-Pragma.** Ohne WAL-Modus verwendet SQLite exklusives Dateisperren, was bei gleichzeitigen Lese-/Schreibvorgängen zu `database is locked` führt.

Alle Tetora-DB-Aufrufe gehen durch `queryDB()` und `execDB()`, die `PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;` voranstellen. Wenn sqlite3 direkt in Skripten aufgerufen wird, diese Pragmas hinzufügen:

```bash
sqlite3 ~/.tetora/history.db \
  "PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; SELECT count(*) FROM tasks;"
```

**Ursache 2: Veraltete `index.lock`-Datei.** git-Operationen hinterlassen `index.lock`, wenn sie unterbrochen werden. Der Worktree-Manager prüft vor git-Operationen auf veraltete Sperren, aber ein Absturz kann eine hinterlassen.

Lösung:

```bash
# Veraltete Lock-Dateien finden
find ~/.tetora/runtime/worktrees -name "index.lock"

# Entfernen (nur wenn keine git-Operation aktiv läuft)
rm /path/to/repo/.git/index.lock
```

---

## Discord / Telegram antwortet nicht

Nachrichten an den Bot erhalten keine Antwort.

**Ursache 1: Falsche Kanal-Konfiguration.** Discord hat zwei Kanallisten: `channelIDs` (direkte Antwort auf alle Nachrichten) und `mentionChannelIDs` (nur Antwort bei @-Erwähnung). Wenn ein Kanal in keiner der Listen steht, werden Nachrichten ignoriert.

Lösung: `config.json` prüfen:

```json
{
  "discord": {
    "enabled": true,
    "channelIDs": ["123456789012345678"],
    "mentionChannelIDs": []
  }
}
```

**Ursache 2: Bot-Token abgelaufen oder falsch.** Telegram-Bot-Tokens laufen nicht ab, aber Discord-Tokens können ungültig werden, wenn der Bot vom Server entfernt wird oder der Token neu generiert wird.

Lösung: Den Bot-Token im Discord-Entwicklerportal neu erstellen und `config.json` aktualisieren.

**Ursache 3: Daemon läuft nicht.** Das Bot-Gateway ist nur aktiv, wenn `tetora serve` läuft.

Lösung:

```bash
tetora status
tetora serve   # wenn nicht läuft
```

---

## glab / gh CLI-Fehler

Die git-Integration schlägt mit Fehlern von `glab` oder `gh` fehl.

**Häufiger Fehler: `gh: command not found`**

Lösung:
```bash
brew install gh      # macOS
gh auth login        # authentifizieren
```

**Häufiger Fehler: `glab: You are not logged in`**

Lösung:
```bash
brew install glab    # macOS
glab auth login      # mit der GitLab-Instanz authentifizieren
```

**Häufiger Fehler: `remote: HTTP Basic: Access denied`**

Lösung: Sicherstellen, dass ein SSH-Schlüssel oder HTTPS-Anmeldedaten für den Repository-Host konfiguriert sind. Für GitLab:

```bash
glab auth status
ssh -T git@gitlab.com   # SSH-Verbindung testen
```

Für GitHub:

```bash
gh auth status
ssh -T git@github.com
```

**PR/MR-Erstellung erfolgreich, aber falscher Basis-Branch**

Standardmäßig zielen PRs auf den Standard-Branch des Repositorys ab (`main` oder `master`). Wenn der Workflow einen anderen Basis-Branch verwendet, diesen explizit in der Post-Task-git-Konfiguration setzen oder sicherstellen, dass der Standard-Branch des Repositorys auf der Hosting-Plattform korrekt konfiguriert ist.
