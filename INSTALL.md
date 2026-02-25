# Installing Tetora

<p align="center">
  <strong>English</strong> | <a href="INSTALL.zh-TW.md">繁體中文</a> | <a href="INSTALL.zh-CN.md">简体中文</a> | <a href="INSTALL.ja.md">日本語</a> | <a href="INSTALL.ko.md">한국어</a> | <a href="INSTALL.fr.md">Français</a> | <a href="INSTALL.de.md">Deutsch</a> | <a href="INSTALL.es.md">Español</a> | <a href="INSTALL.pt.md">Português</a> | <a href="INSTALL.id.md">Bahasa Indonesia</a>
</p>

---

## Requirements

| Requirement | Details |
|---|---|
| **Operating system** | macOS, Linux, or Windows (WSL) |
| **Terminal** | Any terminal emulator |
| **sqlite3** | Must be available on `PATH` |
| **AI provider** | See Path 1 or Path 2 below |

### Install sqlite3

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

**Windows (WSL):** Install inside your WSL distribution using the Linux instructions above.

---

## Download Tetora

Go to the [Releases page](https://github.com/TakumaLee/Tetora/releases/latest) and download the binary for your platform:

| Platform | File |
|---|---|
| macOS (Apple Silicon) | `tetora-darwin-arm64` |
| macOS (Intel) | `tetora-darwin-amd64` |
| Linux (x86_64) | `tetora-linux-amd64` |
| Linux (ARM64) | `tetora-linux-arm64` |
| Windows (WSL) | Use the Linux binary inside WSL |

**Install the binary:**
```bash
# Replace the filename with what you downloaded
chmod +x tetora-darwin-arm64
mv tetora-darwin-arm64 ~/.tetora/bin/tetora

# Make sure ~/.tetora/bin is in your PATH
echo 'export PATH="$HOME/.tetora/bin:$PATH"' >> ~/.zshrc  # or ~/.bashrc
source ~/.zshrc
```

**Or use the one-line installer (macOS / Linux):**
```bash
. <(curl -fsSL https://raw.githubusercontent.com/TakumaLee/Tetora/main/install.sh)
```

---

## Path 1: Claude Pro ($20/month) — Recommended for beginners

This path uses **Claude Code CLI** as the AI backend. You need an active Claude Pro subscription ($20/month at [claude.ai](https://claude.ai)).

> **Why this path?** No API keys to manage, no usage billing surprises. Your Pro subscription covers all Tetora usage through Claude Code.

> [!IMPORTANT]
> **Prerequisites:** This path requires an active Claude Pro subscription ($20/month). If you haven't subscribed yet, visit [claude.ai/upgrade](https://claude.ai/upgrade) first.

### Step 1: Install Claude Code CLI

```bash
npm install -g @anthropic-ai/claude-code
```

If you don't have Node.js, install it first:
- **macOS:** `brew install node`
- **Linux:** `sudo apt install nodejs npm` (Ubuntu/Debian)
- **Windows (WSL):** Follow the Linux instructions above

Verify the installation:
```bash
claude --version
```

Sign in with your Claude Pro account:
```bash
claude
# Follow the browser-based login flow
```

### Step 2: Run tetora init

```bash
tetora init
```

The setup wizard will ask you to:
1. **Choose a language** — select your preferred language
2. **Choose a messaging channel** — Telegram, Discord, Slack, or None
3. **Choose an AI provider** — select **"Claude Code CLI"**
   - The wizard auto-detects your `claude` binary location
   - Press Enter to accept the detected path
4. **Choose directory access** — which folders Tetora can read/write
5. **Create your first agent role** — give it a name and personality

### Step 3: Verify and start

```bash
# Check everything is configured correctly
tetora doctor

# Start the daemon
tetora serve
```

Open the web dashboard:
```bash
tetora dashboard
```

---

## Path 2: API Key

This path uses a direct API key. Supported providers:

- **Claude API** (Anthropic) — [console.anthropic.com](https://console.anthropic.com)
- **OpenAI API** — [platform.openai.com](https://platform.openai.com)
- **Any OpenAI-compatible endpoint** — Ollama, LM Studio, Azure OpenAI, etc.

> **Note on costs:** API usage is billed per token. Check your provider's pricing before enabling expensive models or high-frequency workflows.

### Step 1: Get your API key

**Claude API:**
1. Go to [console.anthropic.com](https://console.anthropic.com)
2. Create an account or sign in
3. Navigate to **API Keys** → **Create Key**
4. Copy the key (starts with `sk-ant-...`)

**OpenAI:**
1. Go to [platform.openai.com/api-keys](https://platform.openai.com/api-keys)
2. Click **Create new secret key**
3. Copy the key (starts with `sk-...`)

**OpenAI-compatible (e.g., Ollama):**
```bash
# Start a local Ollama server
ollama serve
# Default endpoint: http://localhost:11434/v1
# No API key needed for local models
```

### Step 2: Run tetora init

```bash
tetora init
```

The setup wizard will ask you to:
1. **Choose a language** — select your preferred language
2. **Choose a messaging channel** — Telegram, Discord, Slack, or None
3. **Choose an AI provider:**
   - Select **"Claude API Key"** for Anthropic Claude
   - Select **"OpenAI-compatible endpoint"** for OpenAI or local models
4. **Enter your API key** (or endpoint URL for local models)
5. **Choose directory access** — which folders Tetora can read/write
6. **Create your first agent role**

### Step 3: Verify and start

```bash
tetora doctor
tetora serve
```

---

## Web Setup Wizard (non-engineers)

If you prefer a graphical setup experience, use the web wizard:

```bash
tetora setup --web
```

This opens a browser window at `http://localhost:7474` with a 4-step setup wizard. No terminal configuration required.

---

## After Installation

| Command | Description |
|---|---|
| `tetora doctor` | Health checks — run this if something seems wrong |
| `tetora serve` | Start the daemon (bots + HTTP API + scheduled jobs) |
| `tetora dashboard` | Open the web dashboard |
| `tetora status` | Quick status overview |
| `tetora init` | Re-run setup wizard to change configuration |

### Configuration file

All settings are stored in `~/.tetora/config.json`. You can edit this file directly — run `tetora serve` again to apply changes, or send `SIGHUP` to reload without restarting:

```bash
kill -HUP $(pgrep tetora)
```

---

## Troubleshooting

### `tetora: command not found`

Make sure `~/.tetora/bin` is in your `PATH`:
```bash
echo 'export PATH="$HOME/.tetora/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc
```

### `sqlite3: command not found`

Install sqlite3 for your platform (see Requirements above).

### `tetora doctor` reports provider errors

- **Claude Code CLI path:** Run `which claude` and update `claudePath` in `~/.tetora/config.json`
- **API key invalid:** Double-check your key at your provider's console
- **Model not found:** Verify the model name matches your subscription tier

### Claude Code login issues

```bash
# Re-authenticate
claude logout
claude
```

### Permission denied on binary

```bash
chmod +x ~/.tetora/bin/tetora
```

### Port 8991 already in use

Edit `~/.tetora/config.json` and change `listenAddr` to a free port:
```json
"listenAddr": "127.0.0.1:9000"
```

---

## Build from Source

Requires Go 1.25+:

```bash
git clone https://github.com/TakumaLee/Tetora.git
cd tetora
make install
```

This builds and installs to `~/.tetora/bin/tetora`.

---

## Next Steps

- Read the [README](README.md) for full feature documentation
- Join the community: [github.com/TakumaLee/Tetora/discussions](https://github.com/TakumaLee/Tetora/discussions)
- Report issues: [github.com/TakumaLee/Tetora/issues](https://github.com/TakumaLee/Tetora/issues)
