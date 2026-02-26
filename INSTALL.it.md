# Installazione di Tetora

<p align="center">
  <a href="INSTALL.md">English</a> | <a href="INSTALL.zh-TW.md">繁體中文</a> | <a href="INSTALL.ja.md">日本語</a> | <a href="INSTALL.ko.md">한국어</a> | <a href="INSTALL.es.md">Español</a> | <a href="INSTALL.fr.md">Français</a> | <a href="INSTALL.de.md">Deutsch</a> | <a href="INSTALL.pt.md">Português</a> | <strong>Italiano</strong> | <a href="INSTALL.ru.md">Русский</a>
</p>

---

## Requisiti

| Requisito | Dettagli |
|---|---|
| **Sistema operativo** | macOS, Linux o Windows (WSL) |
| **Terminale** | Qualsiasi emulatore di terminale |
| **sqlite3** | Deve essere disponibile nel `PATH` |
| **Provider AI** | Vedi Percorso 1 o Percorso 2 in basso |

### Installare sqlite3

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

**Windows (WSL):** Installa all'interno della tua distribuzione WSL usando le istruzioni Linux sopra.

---

## Scarica Tetora

Vai alla [pagina Releases](https://github.com/TakumaLee/Tetora/releases/latest) e scarica il binario per la tua piattaforma:

| Piattaforma | File |
|---|---|
| macOS (Apple Silicon) | `tetora-darwin-arm64` |
| macOS (Intel) | `tetora-darwin-amd64` |
| Linux (x86_64) | `tetora-linux-amd64` |
| Linux (ARM64) | `tetora-linux-arm64` |
| Windows (WSL) | Usa il binario Linux all'interno di WSL |

**Installa il binario:**
```bash
# Sostituisci il nome del file con quello scaricato
chmod +x tetora-darwin-arm64
mv tetora-darwin-arm64 ~/.tetora/bin/tetora

# Assicurati che ~/.tetora/bin sia nel tuo PATH
echo 'export PATH="$HOME/.tetora/bin:$PATH"' >> ~/.zshrc  # o ~/.bashrc
source ~/.zshrc
```

**Oppure usa l'installatore in una riga (macOS / Linux):**
```bash
. <(curl -fsSL https://raw.githubusercontent.com/TakumaLee/Tetora/main/install.sh)
```

---

## Percorso 1: Claude Pro ($20/mese) — Consigliato per i principianti

Questo percorso usa **Claude Code CLI** come backend AI. È necessario un abbonamento Claude Pro attivo ($20/mese su [claude.ai](https://claude.ai)).

> **Perché questo percorso?** Nessuna chiave API da gestire, nessuna sorpresa di fatturazione. Il tuo abbonamento Pro copre tutto l'utilizzo di Tetora tramite Claude Code.

> [!IMPORTANT]
> **Prerequisiti:** Questo percorso richiede un abbonamento Claude Pro attivo ($20/mese). Se non sei ancora abbonato, visita prima [claude.ai/upgrade](https://claude.ai/upgrade).

### Passo 1: Installa Claude Code CLI

```bash
npm install -g @anthropic-ai/claude-code
```

Se non hai Node.js, installalo prima:
- **macOS:** `brew install node`
- **Linux:** `sudo apt install nodejs npm` (Ubuntu/Debian)
- **Windows (WSL):** Segui le istruzioni Linux sopra

Verifica l'installazione:
```bash
claude --version
```

Accedi con il tuo account Claude Pro:
```bash
claude
# Segui il flusso di accesso nel browser
```

### Passo 2: Esegui tetora init

```bash
tetora init
```

Il wizard di configurazione ti guiderà attraverso:
1. **Scegli la lingua** — seleziona la tua lingua preferita
2. **Scegli il canale di messaggistica** — Telegram, Discord, Slack o Nessuno
3. **Scegli il provider AI** — seleziona **"Claude Code CLI"**
   - Il wizard rileva automaticamente la posizione del tuo binario `claude`
   - Premi Invio per accettare il percorso rilevato
4. **Scegli l'accesso alle directory** — quali cartelle Tetora può leggere/scrivere
5. **Crea il tuo primo ruolo agente** — assegna un nome e una personalità

### Passo 3: Verifica e avvia

```bash
# Verifica che tutto sia configurato correttamente
tetora doctor

# Avvia il daemon
tetora serve
```

Apri la dashboard web:
```bash
tetora dashboard
```

---

## Percorso 2: Chiave API

Questo percorso usa una chiave API diretta. Provider supportati:

- **Claude API** (Anthropic) — [console.anthropic.com](https://console.anthropic.com)
- **OpenAI API** — [platform.openai.com](https://platform.openai.com)
- **Qualsiasi endpoint compatibile con OpenAI** — Ollama, LM Studio, Azure OpenAI, ecc.

> **Nota sui costi:** L'utilizzo delle API viene fatturato per token. Controlla i prezzi del tuo provider prima di abilitare modelli costosi o flussi di lavoro ad alta frequenza.

### Passo 1: Ottieni la tua chiave API

**Claude API:**
1. Vai su [console.anthropic.com](https://console.anthropic.com)
2. Crea un account o accedi
3. Naviga in **API Keys** → **Create Key**
4. Copia la chiave (inizia con `sk-ant-...`)

**OpenAI:**
1. Vai su [platform.openai.com/api-keys](https://platform.openai.com/api-keys)
2. Clicca **Create new secret key**
3. Copia la chiave (inizia con `sk-...`)

**Endpoint compatibile con OpenAI (es. Ollama):**
```bash
# Avvia un server Ollama locale
ollama serve
# Endpoint predefinito: http://localhost:11434/v1
# Nessuna chiave API richiesta per i modelli locali
```

### Passo 2: Esegui tetora init

```bash
tetora init
```

Il wizard di configurazione ti guiderà attraverso:
1. **Scegli la lingua**
2. **Scegli il canale di messaggistica**
3. **Scegli il provider AI:**
   - Seleziona **"Claude API Key"** per Anthropic Claude
   - Seleziona **"Endpoint compatibile OpenAI"** per OpenAI o modelli locali
4. **Inserisci la tua chiave API** (o URL endpoint per modelli locali)
5. **Scegli l'accesso alle directory**
6. **Crea il tuo primo ruolo agente**

### Passo 3: Verifica e avvia

```bash
tetora doctor
tetora serve
```

---

## Wizard di configurazione Web (per non sviluppatori)

Se preferisci un'interfaccia grafica, usa il wizard web:

```bash
tetora setup --web
```

Questo apre una finestra del browser su `http://localhost:7474` con un wizard di configurazione in 4 passi. Nessuna configurazione da terminale richiesta.

---

## Dopo l'installazione

| Comando | Descrizione |
|---|---|
| `tetora doctor` | Controlli di salute — esegui questo se qualcosa non va |
| `tetora serve` | Avvia il daemon (bot + HTTP API + job pianificati) |
| `tetora dashboard` | Apri la dashboard web |
| `tetora status` | Panoramica rapida dello stato |
| `tetora init` | Riesegui il wizard di configurazione |

### File di configurazione

Tutte le impostazioni sono memorizzate in `~/.tetora/config.json`. Puoi modificare questo file direttamente — esegui di nuovo `tetora serve` per applicare le modifiche, o invia `SIGHUP` per ricaricare senza riavviare:

```bash
kill -HUP $(pgrep tetora)
```

---

## Risoluzione dei problemi

### `tetora: command not found`

Assicurati che `~/.tetora/bin` sia nel tuo `PATH`:
```bash
echo 'export PATH="$HOME/.tetora/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc
```

### `sqlite3: command not found`

Installa sqlite3 per la tua piattaforma (vedi Requisiti sopra).

### `tetora doctor` segnala errori del provider

- **Percorso Claude Code CLI:** Esegui `which claude` e aggiorna `claudePath` in `~/.tetora/config.json`
- **Chiave API non valida:** Verifica la tua chiave nella console del provider
- **Modello non trovato:** Verifica che il nome del modello corrisponda al tuo livello di abbonamento

### Problemi di accesso a Claude Code

```bash
# Ri-autenticati
claude logout
claude
```

### Permesso negato sul binario

```bash
chmod +x ~/.tetora/bin/tetora
```

### Porta 8991 già in uso

Modifica `~/.tetora/config.json` e cambia `listenAddr` con una porta libera:
```json
"listenAddr": "127.0.0.1:9000"
```

---

## Compilazione dal codice sorgente

Richiede Go 1.25+:

```bash
git clone https://github.com/TakumaLee/Tetora.git
cd tetora
make install
```

Questo compila e installa in `~/.tetora/bin/tetora`.

---

## Passi successivi

- Leggi il [README](README.md) per la documentazione completa delle funzionalità
- Unisciti alla community: [github.com/TakumaLee/Tetora/discussions](https://github.com/TakumaLee/Tetora/discussions)
- Segnala problemi: [github.com/TakumaLee/Tetora/issues](https://github.com/TakumaLee/Tetora/issues)
