# Tetora installieren

<p align="center">
  <a href="INSTALL.md">English</a> | <a href="INSTALL.zh-TW.md">繁體中文</a> | <a href="INSTALL.ja.md">日本語</a> | <a href="INSTALL.ko.md">한국어</a> | <a href="INSTALL.es.md">Español</a> | <a href="INSTALL.fr.md">Français</a> | <strong>Deutsch</strong> | <a href="INSTALL.pt.md">Português</a> | <a href="INSTALL.it.md">Italiano</a> | <a href="INSTALL.ru.md">Русский</a>
</p>

---

## Voraussetzungen

| Voraussetzung | Details |
|---|---|
| **Betriebssystem** | macOS, Linux oder Windows (WSL) |
| **Terminal** | Beliebiger Terminal-Emulator |
| **sqlite3** | Muss im `PATH` verfügbar sein |
| **KI-Anbieter** | Siehe Pfad 1 oder Pfad 2 unten |

### sqlite3 installieren

**macOS:**
```bash
brew install sqlite3
```

**Ubuntu / Debian:**
```bash
sudo apt install sqlite3
```

**Fedora / RHEL:**
```bash
sudo dnf install sqlite
```

**Windows (WSL):** Installieren Sie innerhalb Ihrer WSL-Distribution mit den Linux-Befehlen oben.

---

## Tetora herunterladen

Besuchen Sie die [Releases-Seite](https://github.com/TakumaLee/Tetora/releases/latest) und laden Sie das Binärprogramm für Ihre Plattform herunter:

| Plattform | Datei |
|---|---|
| macOS (Apple Silicon) | `tetora-darwin-arm64` |
| macOS (Intel) | `tetora-darwin-amd64` |
| Linux (x86_64) | `tetora-linux-amd64` |
| Linux (ARM64) | `tetora-linux-arm64` |
| Windows (WSL) | Linux-Binärprogramm in WSL verwenden |

**Binärprogramm installieren:**
```bash
# Ersetzen Sie den Dateinamen durch die heruntergeladene Datei
chmod +x tetora-darwin-arm64
mv tetora-darwin-arm64 ~/.tetora/bin/tetora

# Stellen Sie sicher, dass ~/.tetora/bin im PATH ist
echo 'export PATH="$HOME/.tetora/bin:$PATH"' >> ~/.zshrc  # oder ~/.bashrc
source ~/.zshrc
```

**Oder verwenden Sie den Einzeilen-Installer (macOS / Linux):**
```bash
. <(curl -fsSL https://raw.githubusercontent.com/TakumaLee/Tetora/main/install.sh)
```

---

## Pfad 1: Claude Pro (20 $/Monat) — Empfohlen für Einsteiger

Dieser Pfad verwendet **Claude Code CLI** als KI-Backend. Ein aktives Claude Pro Abonnement (20 $/Monat auf [claude.ai](https://claude.ai)) ist erforderlich.

> **Warum dieser Pfad?** Keine API-Schlüssel zu verwalten, keine Überraschungen bei der Abrechnung. Ihr Pro-Abonnement deckt die gesamte Tetora-Nutzung über Claude Code ab.

> [!IMPORTANT]
> **Voraussetzung:** Dieser Pfad erfordert ein aktives Claude Pro Abonnement (20 $/Monat). Falls Sie noch kein Abonnement haben, besuchen Sie zuerst [claude.ai/upgrade](https://claude.ai/upgrade).

### Schritt 1: Claude Code CLI installieren

```bash
npm install -g @anthropic-ai/claude-code
```

Falls Sie Node.js noch nicht installiert haben:
- **macOS:** `brew install node`
- **Linux:** `sudo apt install nodejs npm` (Ubuntu/Debian)

Installation überprüfen:
```bash
claude --version
```

Mit Ihrem Claude Pro Konto anmelden:
```bash
claude
# Folgen Sie dem Browser-basierten Anmeldevorgang
```

### Schritt 2: tetora init ausführen

```bash
tetora init
```

Der Einrichtungsassistent führt Sie durch:
1. **Sprache wählen** — wählen Sie Ihre bevorzugte Sprache
2. **Messaging-Kanal wählen** — Telegram, Discord, Slack oder Keiner
3. **KI-Anbieter wählen** — wählen Sie **„Claude Code CLI"**
   - Der Assistent erkennt automatisch den Speicherort Ihres `claude`-Binärprogramms
   - Drücken Sie Enter, um den erkannten Pfad zu akzeptieren
4. **Verzeichniszugriff wählen** — welche Ordner Tetora lesen/schreiben darf
5. **Erste Agentenrolle erstellen** — geben Sie ihr einen Namen und eine Persönlichkeit

### Schritt 3: Überprüfen und starten

```bash
# Überprüfen, ob alles korrekt konfiguriert ist
tetora doctor

# Daemon starten
tetora serve
```

Web-Dashboard öffnen:
```bash
tetora dashboard
```

---

## Pfad 2: API-Schlüssel

Dieser Pfad verwendet einen direkten API-Schlüssel. Unterstützte Anbieter:

- **Claude API** (Anthropic) — [console.anthropic.com](https://console.anthropic.com)
- **OpenAI API** — [platform.openai.com](https://platform.openai.com)
- **Beliebiger OpenAI-kompatibler Endpunkt** — Ollama, LM Studio, Azure OpenAI usw.

> **Hinweis zu Kosten:** Die API-Nutzung wird pro Token abgerechnet. Überprüfen Sie die Preisgestaltung Ihres Anbieters vor der Aktivierung.

### Schritt 1: API-Schlüssel erhalten

**Claude API:**
1. Gehen Sie zu [console.anthropic.com](https://console.anthropic.com)
2. Erstellen Sie ein Konto oder melden Sie sich an
3. Navigieren Sie zu **API Keys** → **Create Key**
4. Kopieren Sie den Schlüssel (beginnt mit `sk-ant-...`)

**OpenAI:**
1. Gehen Sie zu [platform.openai.com/api-keys](https://platform.openai.com/api-keys)
2. Klicken Sie auf **Create new secret key**
3. Kopieren Sie den Schlüssel (beginnt mit `sk-...`)

**OpenAI-kompatibler Endpunkt (z.B. Ollama):**
```bash
# Lokalen Ollama-Server starten
ollama serve
# Standard-Endpunkt: http://localhost:11434/v1
# Kein API-Schlüssel für lokale Modelle erforderlich
```

### Schritt 2: tetora init ausführen

```bash
tetora init
```

Der Assistent führt Sie durch:
1. **Sprache wählen**
2. **Messaging-Kanal wählen**
3. **KI-Anbieter wählen:**
   - Wählen Sie **„Claude API Key"** für Anthropic Claude
   - Wählen Sie **„OpenAI-kompatibler Endpunkt"** für OpenAI oder lokale Modelle
4. **API-Schlüssel eingeben** (oder Endpunkt-URL für lokale Modelle)
5. **Verzeichniszugriff wählen**
6. **Erste Agentenrolle erstellen**

### Schritt 3: Überprüfen und starten

```bash
tetora doctor
tetora serve
```

---

## Web-Einrichtungsassistent (für Nicht-Entwickler)

Wenn Sie eine grafische Einrichtungsoberfläche bevorzugen, verwenden Sie den Web-Assistenten:

```bash
tetora setup --web
```

Dies öffnet ein Browserfenster unter `http://localhost:7474` mit einem 4-Schritte-Einrichtungsassistenten.

---

## Nützliche Befehle nach der Installation

| Befehl | Beschreibung |
|---|---|
| `tetora doctor` | Gesundheitschecks — bei Problemen als erstes ausführen |
| `tetora serve` | Daemon starten (Bots + HTTP API + geplante Jobs) |
| `tetora dashboard` | Web-Dashboard öffnen |
| `tetora status` | Schnelle Statusübersicht |
| `tetora init` | Einrichtungsassistenten erneut ausführen |

---

## Fehlerbehebung

### `tetora: command not found`

Stellen Sie sicher, dass `~/.tetora/bin` im PATH ist:
```bash
echo 'export PATH="$HOME/.tetora/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc
```

### `sqlite3: command not found`

Installieren Sie sqlite3 für Ihre Plattform (siehe Voraussetzungen oben).

### `tetora doctor` meldet Anbieterfehler

- **Claude Code CLI-Pfad:** Führen Sie `which claude` aus und aktualisieren Sie `claudePath` in `~/.tetora/config.json`
- **Ungültiger API-Schlüssel:** Überprüfen Sie Ihren Schlüssel in der Konsole Ihres Anbieters
- **Modell nicht gefunden:** Überprüfen Sie, ob der Modellname zu Ihrem Abonnementniveau passt

### Claude Code Anmeldeprobleme

```bash
claude logout
claude
```

---

## Aus dem Quellcode kompilieren

Erfordert Go 1.25+:

```bash
git clone https://github.com/TakumaLee/Tetora.git
cd tetora
make install
```

---

## Nächste Schritte

- Lesen Sie die [README](README.de.md) für vollständige Funktionsdokumentation
- Community: [github.com/TakumaLee/Tetora/discussions](https://github.com/TakumaLee/Tetora/discussions)
- Probleme melden: [github.com/TakumaLee/Tetora/issues](https://github.com/TakumaLee/Tetora/issues)
