---
title: "Discord Multitasking-Leitfaden"
lang: "de"
order: 6
description: "Run multiple agents concurrently via Discord threads."
---
# Discord Multitasking-Leitfaden

Tetora unterstützt auf Discord parallele Mehrfachkonversationen über **Threads + `/focus`**. Jeder Thread hat eine eigene Session und eine eigene Agent-Bindung.

---

## Grundkonzepte

### Hauptkanal — Einzelne Session

Jeder Discord-Kanal hat nur **eine aktive Session**, alle Nachrichten teilen sich denselben Konversationskontext.

- Session-Key-Format: `discord:{channelID}`
- Alle Nachrichten aller Nutzer in demselben Kanal fließen in dieselbe Session
- Der Konversationsverlauf akkumuliert sich kontinuierlich, bis er mit `!new` zurückgesetzt wird

### Thread — Unabhängige Session

Discord-Threads können über `/focus` an einen bestimmten Agent gebunden werden und erhalten eine vollständig unabhängige Session.

- Session-Key-Format: `agent:{agentName}:discord:thread:{guildID}:{threadID}`
- Vollständig vom Hauptkanal isoliert — Kontexte beeinflussen sich gegenseitig nicht
- Jeder Thread kann an einen anderen Agent gebunden werden

---

## Befehle

| Befehl | Ort | Beschreibung |
|---|---|---|
| `/focus <agent>` | Im Thread | Diesen Thread an den angegebenen Agent binden und eine unabhängige Session erstellen |
| `/unfocus` | Im Thread | Die Agent-Bindung des Threads aufheben |
| `!new` | Hauptkanal | Aktuelle Session archivieren, die nächste Nachricht öffnet eine völlig neue Session |

---

## Multitasking-Ablauf

### Schritt 1: Discord-Thread erstellen

Im Hauptkanal auf eine Nachricht rechtsklicken → **Thread erstellen** (oder die Thread-Erstellungsfunktion von Discord verwenden).

### Schritt 2: Agent im Thread binden

```
/focus ruri
```

Nach erfolgreicher Bindung laufen alle Konversationen in diesem Thread:
- Mit den SOUL.md-Rolleneinstellungen von ruri
- Mit eigenem Konversationsverlauf
- Ohne den Hauptkanal-Session zu beeinflussen

### Schritt 3: Nach Bedarf mehrere Threads öffnen

```
#general (Hauptkanal)                    ← Alltägliche Gespräche, 1 Session
  └─ Thread: "Auth-Modul refaktorieren"  ← /focus kokuyou → eigene Session
  └─ Thread: "Blog-Post diese Woche"     ← /focus kohaku  → eigene Session
  └─ Thread: "Wettbewerber-Analyse"      ← /focus hisui   → eigene Session
  └─ Thread: "Projektplanungsdiskussion" ← /focus ruri    → eigene Session
```

Jeder Thread ist ein vollständig isolierter Konversationsraum, der gleichzeitig betrieben werden kann.

---

## Hinweise

### TTL (Time To Live)

- Thread-Bindungen laufen standardmäßig nach **24 Stunden** ab
- Nach dem Ablauf kehrt der Thread in den normalen Modus zurück (folgt der Routing-Logik des Hauptkanals)
- Über `threadBindings.ttlHours` in der Konfiguration anpassbar

### Gleichzeitigkeitslimits

- Die globale maximale Parallelität wird durch `maxConcurrent` gesteuert (Standard: 8)
- Alle Kanäle und Threads teilen sich dieses Limit
- Nachrichten, die das Limit überschreiten, werden in einer Warteschlange eingereiht

### Konfiguration aktivieren

Sicherstellen, dass Thread-Bindungen in der Konfiguration aktiviert sind:

```json
{
  "discord": {
    "threadBindings": {
      "enabled": true,
      "ttlHours": 24
    }
  }
}
```

### Einschränkungen des Hauptkanals

- Im Hauptkanal kann keine zweite Session mit `/focus` erstellt werden
- Zum Zurücksetzen des Konversationskontexts `!new` verwenden
- Das gleichzeitige Senden mehrerer Nachrichten in demselben Kanal teilt eine Session — der Kontext kann sich gegenseitig beeinflussen

---

## Empfehlungen nach Anwendungsfall

| Situation | Empfohlenes Vorgehen |
|---|---|
| Alltägliche Gespräche, einfache Fragen | Direkt im Hauptkanal chatten |
| Fokussierte Diskussion zu einem Thema | Thread öffnen + `/focus` |
| Verschiedene Aufgaben verschiedenen Agents zuweisen | Pro Aufgabe ein Thread, jeweils `/focus` zum passenden Agent |
| Konversationskontext ist zu lang, neu anfangen | Im Hauptkanal `!new`, im Thread `/unfocus` dann `/focus` |
| Mehrere Personen arbeiten gemeinsam an einem Thema | Einen gemeinsamen Thread öffnen, alle Personen chatten im Thread |
