package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const setupWebPort = "7474"
const setupWebAddr = "127.0.0.1:" + setupWebPort

// cmdSetup handles: tetora setup --web
func cmdSetup(args []string) {
	webMode := false
	for _, a := range args {
		if a == "--web" || a == "-web" {
			webMode = true
		}
	}
	if !webMode {
		fmt.Fprintln(os.Stderr, "Usage: tetora setup --web")
		fmt.Fprintln(os.Stderr, "  --web   Launch browser-based setup wizard on localhost:7474")
		os.Exit(1)
	}
	runSetupWebServer()
}

// runSetupWebServer starts a temporary HTTP server for the setup wizard,
// opens the browser, serves requests until setup completes, then shuts down.
func runSetupWebServer() {
	// Check if port is already in use.
	if ln, err := net.Listen("tcp", setupWebAddr); err != nil {
		fmt.Fprintf(os.Stderr, "Error: port %s is already in use. Is Tetora already running a setup wizard?\n", setupWebPort)
		os.Exit(1)
	} else {
		ln.Close()
	}

	doneCh := make(chan struct{})

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write([]byte(setupWizardHTML))
	})
	mux.HandleFunc("/api/setup/detect", handleSetupDetect)
	mux.HandleFunc("/api/setup/save", func(w http.ResponseWriter, r *http.Request) {
		handleSetupSave(w, r, doneCh)
	})

	srv := &http.Server{
		Addr:    setupWebAddr,
		Handler: mux,
	}

	fmt.Printf("Starting setup wizard at http://localhost:%s\n", setupWebPort)
	fmt.Println("Opening browser...")

	go func() {
		time.Sleep(300 * time.Millisecond)
		openBrowser("http://localhost:" + setupWebPort)
	}()

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "Setup server error: %v\n", err)
		}
	}()

	// Wait for setup completion or Ctrl+C.
	<-doneCh

	fmt.Println("\nSetup complete. Shutting down wizard...")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	srv.Shutdown(ctx)

	fmt.Println("")
	fmt.Println("Configuration saved. Next steps:")
	fmt.Println("  tetora doctor   — verify your setup")
	fmt.Println("  tetora serve    — start the daemon")
	fmt.Println("  tetora dashboard — open the web dashboard")
}

// handleSetupDetect returns auto-detected values for the wizard.
func handleSetupDetect(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	claudePath := detectClaude()
	json.NewEncoder(w).Encode(map[string]string{
		"claudePath": claudePath,
	})
}

// handleSetupSave writes config.json from the wizard form submission.
func handleSetupSave(w http.ResponseWriter, r *http.Request, doneCh chan struct{}) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var req setupSaveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	if err := writeSetupConfig(req); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

	// Signal shutdown after response is written.
	go func() {
		time.Sleep(200 * time.Millisecond)
		select {
		case doneCh <- struct{}{}:
		default:
		}
	}()
}

type setupSaveRequest struct {
	Language string `json:"language"`
	Channel  string `json:"channel"` // telegram|discord|slack|none

	// Telegram
	TelegramBotToken string `json:"telegramBotToken"`
	TelegramChatID   int64  `json:"telegramChatID"`

	// Discord
	DiscordBotToken  string `json:"discordBotToken"`
	DiscordAppID     string `json:"discordAppID"`
	DiscordChannelID string `json:"discordChannelID"`

	// Slack
	SlackBotToken      string `json:"slackBotToken"`
	SlackSigningSecret string `json:"slackSigningSecret"`

	// Provider
	Provider       string `json:"provider"` // claude-cli|claude-api|openai
	ClaudePath     string `json:"claudePath"`
	ClaudeAPIKey   string `json:"claudeAPIKey"`
	OpenAIEndpoint string `json:"openaiEndpoint"`
	OpenAIAPIKey   string `json:"openaiAPIKey"`
	DefaultModel   string `json:"defaultModel"`
}

func writeSetupConfig(req setupSaveRequest) error {
	home, _ := os.UserHomeDir()
	configDir := filepath.Join(home, ".tetora")
	configPath := filepath.Join(configDir, "config.json")

	// Default model by provider.
	model := req.DefaultModel
	if model == "" {
		switch req.Provider {
		case "claude-api":
			model = "claude-sonnet-4-5-20250929"
		case "openai":
			model = "gpt-4o"
		default:
			model = "sonnet"
		}
	}

	// Generate API token.
	tokenBytes := make([]byte, 32)
	rand.Read(tokenBytes)
	apiToken := hex.EncodeToString(tokenBytes)

	defaultWorkdir := filepath.Join(configDir, "workspace")

	cfg := map[string]any{
		"maxConcurrent":         3,
		"defaultModel":          model,
		"defaultTimeout":        "15m",
		"defaultBudget":         2.0,
		"defaultPermissionMode": "acceptEdits",
		"defaultWorkdir":        defaultWorkdir,
		"listenAddr":            "127.0.0.1:8991",
		"jobsFile":              "jobs.json",
		"apiToken":              apiToken,
		"log":                   true,
		"defaultAddDirs":        []string{"~"},
	}

	// Claude CLI path.
	if req.Provider == "claude-cli" && req.ClaudePath != "" {
		cfg["claudePath"] = req.ClaudePath
	}

	// Channel config.
	switch req.Channel {
	case "telegram":
		cfg["telegram"] = map[string]any{
			"enabled":     true,
			"botToken":    req.TelegramBotToken,
			"chatID":      req.TelegramChatID,
			"pollTimeout": 30,
		}
	case "discord":
		cfg["discord"] = map[string]any{
			"enabled":   true,
			"botToken":  req.DiscordBotToken,
			"appID":     req.DiscordAppID,
			"channelID": req.DiscordChannelID,
		}
	case "slack":
		cfg["slack"] = map[string]any{
			"enabled":       true,
			"botToken":      req.SlackBotToken,
			"signingSecret": req.SlackSigningSecret,
		}
	default:
		cfg["telegram"] = map[string]any{"enabled": false}
	}

	// Provider config.
	switch req.Provider {
	case "claude-api":
		cfg["providers"] = map[string]any{
			"claude-api": map[string]any{
				"type":   "claude",
				"apiKey": req.ClaudeAPIKey,
				"model":  model,
			},
		}
		cfg["defaultProvider"] = "claude-api"
	case "openai":
		endpoint := req.OpenAIEndpoint
		if endpoint == "" {
			endpoint = "https://api.openai.com/v1"
		}
		cfg["providers"] = map[string]any{
			"openai": map[string]any{
				"type":     "openai",
				"endpoint": endpoint,
				"apiKey":   req.OpenAIAPIKey,
				"model":    model,
			},
		}
		cfg["defaultProvider"] = "openai"
	}

	// Create directories.
	for _, d := range []string{
		configDir,
		filepath.Join(configDir, "bin"),
		filepath.Join(configDir, "logs"),
		filepath.Join(configDir, "sessions"),
		filepath.Join(configDir, "outputs"),
		defaultWorkdir,
	} {
		os.MkdirAll(d, 0o755)
	}

	// Write config.
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(configPath, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	// Create empty jobs.json if not exists.
	jobsPath := filepath.Join(configDir, "jobs.json")
	if _, err := os.Stat(jobsPath); os.IsNotExist(err) {
		os.WriteFile(jobsPath, []byte("{\n  \"jobs\": []\n}\n"), 0o644)
	}

	return nil
}

// openBrowser opens the URL in the default system browser.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default:
		// Linux — try xdg-open, fallback to sensible-browser.
		if _, err := exec.LookPath("xdg-open"); err == nil {
			cmd = exec.Command("xdg-open", url)
		} else {
			cmd = exec.Command("sensible-browser", url)
		}
	}
	if cmd != nil {
		cmd.Start()
	}
}

// setupWizardHTML is the inline HTML for the 4-step setup wizard.
// It matches the dark theme from dashboard.html.
var setupWizardHTML = strings.TrimSpace(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Tetora Setup</title>
<style>
:root {
  --bg: #08080d;
  --surface: #111118;
  --surface2: #16161f;
  --border: #1e1e2e;
  --text: #e0e0e8;
  --muted: #6b6b80;
  --accent: #a78bfa;
  --accent2: #60a5fa;
  --green: #34d399;
  --red: #f87171;
  --yellow: #fbbf24;
}
* { margin: 0; padding: 0; box-sizing: border-box; }
body {
  font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', system-ui, sans-serif;
  background: var(--bg);
  color: var(--text);
  line-height: 1.5;
  min-height: 100vh;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: flex-start;
  padding: 40px 20px 60px;
}
.wizard {
  width: 100%;
  max-width: 600px;
}
.logo {
  font-size: 28px;
  font-weight: 700;
  background: linear-gradient(135deg, var(--accent), var(--accent2));
  -webkit-background-clip: text;
  -webkit-text-fill-color: transparent;
  margin-bottom: 8px;
  text-align: center;
}
.subtitle {
  text-align: center;
  color: var(--muted);
  font-size: 14px;
  margin-bottom: 36px;
}

/* Step indicator */
.steps {
  display: flex;
  align-items: center;
  justify-content: center;
  margin-bottom: 36px;
  gap: 0;
}
.step-item {
  display: flex;
  flex-direction: column;
  align-items: center;
  position: relative;
}
.step-circle {
  width: 32px;
  height: 32px;
  border-radius: 50%;
  border: 2px solid var(--border);
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 13px;
  font-weight: 600;
  color: var(--muted);
  background: var(--surface);
  transition: all 0.2s;
}
.step-circle.active {
  border-color: var(--accent);
  color: var(--accent);
  background: rgba(167,139,250,0.1);
}
.step-circle.done {
  border-color: var(--green);
  color: var(--green);
  background: rgba(52,211,153,0.1);
}
.step-label {
  font-size: 11px;
  color: var(--muted);
  margin-top: 6px;
  white-space: nowrap;
}
.step-label.active { color: var(--accent); }
.step-connector {
  width: 60px;
  height: 2px;
  background: var(--border);
  margin: 0 4px;
  margin-bottom: 20px;
  transition: background 0.2s;
}
.step-connector.done { background: var(--green); }

/* Card */
.card {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 14px;
  padding: 32px;
}
.card-title {
  font-size: 18px;
  font-weight: 700;
  margin-bottom: 6px;
}
.card-desc {
  color: var(--muted);
  font-size: 13px;
  margin-bottom: 28px;
  line-height: 1.6;
}

/* Option cards (radio-style) */
.option-grid {
  display: grid;
  gap: 10px;
  margin-bottom: 24px;
}
.option-card {
  border: 2px solid var(--border);
  border-radius: 10px;
  padding: 14px 18px;
  cursor: pointer;
  transition: border-color 0.15s, background 0.15s;
  display: flex;
  align-items: flex-start;
  gap: 14px;
}
.option-card:hover { border-color: var(--accent); background: rgba(167,139,250,0.04); }
.option-card.selected { border-color: var(--accent); background: rgba(167,139,250,0.08); }
.option-icon {
  font-size: 20px;
  line-height: 1;
  margin-top: 2px;
  flex-shrink: 0;
}
.option-text { flex: 1; }
.option-title { font-weight: 600; font-size: 15px; }
.option-desc { font-size: 12px; color: var(--muted); margin-top: 2px; }
.option-badge {
  font-size: 10px;
  font-weight: 700;
  padding: 2px 8px;
  border-radius: 10px;
  background: rgba(167,139,250,0.15);
  color: var(--accent);
  letter-spacing: 0.5px;
  flex-shrink: 0;
  align-self: center;
}

/* Form fields */
.field { margin-bottom: 16px; }
.field label {
  display: block;
  font-size: 12px;
  font-weight: 600;
  color: var(--muted);
  text-transform: uppercase;
  letter-spacing: 0.5px;
  margin-bottom: 6px;
}
.field input {
  width: 100%;
  background: var(--surface2);
  border: 1px solid var(--border);
  border-radius: 8px;
  color: var(--text);
  font-size: 14px;
  padding: 10px 14px;
  outline: none;
  transition: border-color 0.15s;
  font-family: inherit;
}
.field input:focus { border-color: var(--accent); }
.field input::placeholder { color: var(--muted); }
.hint {
  font-size: 11px;
  color: var(--muted);
  margin-top: 5px;
  line-height: 1.5;
}
.hint a { color: var(--accent2); text-decoration: none; }
.hint a:hover { text-decoration: underline; }

/* Buttons */
.btn-row {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  margin-top: 28px;
}
.btn {
  padding: 10px 24px;
  border-radius: 8px;
  font-size: 14px;
  font-weight: 600;
  cursor: pointer;
  border: none;
  transition: opacity 0.15s;
  font-family: inherit;
}
.btn:hover { opacity: 0.85; }
.btn-primary {
  background: var(--accent);
  color: #0d0d18;
}
.btn-secondary {
  background: var(--surface2);
  color: var(--text);
  border: 1px solid var(--border);
}

/* Done screen */
.done-icon {
  font-size: 56px;
  text-align: center;
  margin-bottom: 16px;
}
.done-title {
  font-size: 24px;
  font-weight: 700;
  text-align: center;
  margin-bottom: 8px;
}
.done-sub {
  text-align: center;
  color: var(--muted);
  margin-bottom: 28px;
}
.code-block {
  background: var(--surface2);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 14px 18px;
  font-family: 'SF Mono', 'Fira Code', 'Cascadia Code', monospace;
  font-size: 13px;
  color: var(--accent2);
  margin-bottom: 10px;
}

/* Error */
.error-msg {
  color: var(--red);
  font-size: 13px;
  margin-top: 10px;
  display: none;
}

/* Spinner */
.spinner {
  display: inline-block;
  width: 16px; height: 16px;
  border: 2px solid rgba(167,139,250,0.3);
  border-top-color: var(--accent);
  border-radius: 50%;
  animation: spin 0.7s linear infinite;
  vertical-align: middle;
  margin-right: 6px;
}
@keyframes spin { to { transform: rotate(360deg); } }

/* Sub-fields toggle */
.sub-fields { display: none; margin-top: 16px; padding-top: 16px; border-top: 1px solid var(--border); }
.sub-fields.visible { display: block; }
</style>
</head>
<body>
<div class="wizard">
  <div class="logo">Tetora</div>
  <div class="subtitle">Setup Wizard — get started in 4 steps</div>

  <!-- Step indicator -->
  <div class="steps" id="stepIndicator">
    <div class="step-item">
      <div class="step-circle active" id="sc1">1</div>
      <div class="step-label active" id="sl1">Language</div>
    </div>
    <div class="step-connector" id="conn1"></div>
    <div class="step-item">
      <div class="step-circle" id="sc2">2</div>
      <div class="step-label" id="sl2">Channel</div>
    </div>
    <div class="step-connector" id="conn2"></div>
    <div class="step-item">
      <div class="step-circle" id="sc3">3</div>
      <div class="step-label" id="sl3">AI Provider</div>
    </div>
    <div class="step-connector" id="conn3"></div>
    <div class="step-item">
      <div class="step-circle" id="sc4">4</div>
      <div class="step-label" id="sl4">Done</div>
    </div>
  </div>

  <!-- Step 1: Language -->
  <div class="card" id="step1">
    <div class="card-title">Choose your language</div>
    <div class="card-desc">Select the language for the setup wizard. Tetora itself is multi-lingual.</div>
    <div class="option-grid">
      <div class="option-card selected" onclick="selectLang(this,'en')">
        <div class="option-icon">🇺🇸</div>
        <div class="option-text"><div class="option-title">English</div></div>
      </div>
      <div class="option-card" onclick="selectLang(this,'zh-TW')">
        <div class="option-icon">🇹🇼</div>
        <div class="option-text"><div class="option-title">繁體中文</div></div>
      </div>
      <div class="option-card" onclick="selectLang(this,'zh-CN')">
        <div class="option-icon">🇨🇳</div>
        <div class="option-text"><div class="option-title">简体中文</div></div>
      </div>
      <div class="option-card" onclick="selectLang(this,'ja')">
        <div class="option-icon">🇯🇵</div>
        <div class="option-text"><div class="option-title">日本語</div></div>
      </div>
      <div class="option-card" onclick="selectLang(this,'ko')">
        <div class="option-icon">🇰🇷</div>
        <div class="option-text"><div class="option-title">한국어</div></div>
      </div>
      <div class="option-card" onclick="selectLang(this,'es')">
        <div class="option-icon">🇪🇸</div>
        <div class="option-text"><div class="option-title">Español</div></div>
      </div>
      <div class="option-card" onclick="selectLang(this,'fr')">
        <div class="option-icon">🇫🇷</div>
        <div class="option-text"><div class="option-title">Français</div></div>
      </div>
      <div class="option-card" onclick="selectLang(this,'de')">
        <div class="option-icon">🇩🇪</div>
        <div class="option-text"><div class="option-title">Deutsch</div></div>
      </div>
      <div class="option-card" onclick="selectLang(this,'pt')">
        <div class="option-icon">🇧🇷</div>
        <div class="option-text"><div class="option-title">Português</div></div>
      </div>
      <div class="option-card" onclick="selectLang(this,'ru')">
        <div class="option-icon">🇷🇺</div>
        <div class="option-text"><div class="option-title">Русский</div></div>
      </div>
    </div>
    <div class="btn-row">
      <button class="btn btn-primary" onclick="goStep(2)">Continue</button>
    </div>
  </div>

  <!-- Step 2: Channel -->
  <div class="card" id="step2" style="display:none">
    <div class="card-title" id="s2title">Set up your messaging channel</div>
    <div class="card-desc" id="s2desc">Choose how you want to interact with your agents. You can add more channels later.</div>
    <div class="option-grid">
      <div class="option-card" onclick="selectChannel(this,'telegram')">
        <div class="option-icon">✈️</div>
        <div class="option-text">
          <div class="option-title">Telegram</div>
          <div class="option-desc">Chat with your agents via Telegram bot</div>
        </div>
      </div>
      <div class="option-card" onclick="selectChannel(this,'discord')">
        <div class="option-icon">🎮</div>
        <div class="option-text">
          <div class="option-title">Discord</div>
          <div class="option-desc">Integrate with your Discord server</div>
        </div>
      </div>
      <div class="option-card" onclick="selectChannel(this,'slack')">
        <div class="option-icon">💼</div>
        <div class="option-text">
          <div class="option-title">Slack</div>
          <div class="option-desc">Connect to your Slack workspace</div>
        </div>
      </div>
      <div class="option-card selected" onclick="selectChannel(this,'none')">
        <div class="option-icon">🌐</div>
        <div class="option-text">
          <div class="option-title">None (HTTP API only)</div>
          <div class="option-desc">Use the REST API and web dashboard only</div>
        </div>
      </div>
    </div>

    <!-- Telegram fields -->
    <div class="sub-fields" id="sf-telegram">
      <div class="field">
        <label>Bot Token</label>
        <input type="text" id="telegramToken" placeholder="1234567890:AABBcc..." />
        <div class="hint">Get from <a href="https://t.me/BotFather" target="_blank">@BotFather</a> → /newbot</div>
      </div>
      <div class="field">
        <label>Chat ID</label>
        <input type="number" id="telegramChatID" placeholder="123456789" />
        <div class="hint">Send a message to your bot, then visit: api.telegram.org/bot&lt;TOKEN&gt;/getUpdates</div>
      </div>
    </div>

    <!-- Discord fields -->
    <div class="sub-fields" id="sf-discord">
      <div class="field">
        <label>Bot Token</label>
        <input type="text" id="discordToken" placeholder="MTk4NjIy..." />
        <div class="hint"><a href="https://discord.com/developers/applications" target="_blank">discord.com/developers</a> → Bot → Reset Token</div>
      </div>
      <div class="field">
        <label>Application ID</label>
        <input type="text" id="discordAppID" placeholder="123456789012345678" />
      </div>
      <div class="field">
        <label>Channel ID</label>
        <input type="text" id="discordChannelID" placeholder="123456789012345678" />
        <div class="hint">Right-click a channel → Copy Channel ID (Developer Mode must be on)</div>
      </div>
    </div>

    <!-- Slack fields -->
    <div class="sub-fields" id="sf-slack">
      <div class="field">
        <label>Bot Token</label>
        <input type="text" id="slackToken" placeholder="xoxb-..." />
        <div class="hint"><a href="https://api.slack.com/apps" target="_blank">api.slack.com/apps</a> → OAuth & Permissions → Bot Token</div>
      </div>
      <div class="field">
        <label>Signing Secret</label>
        <input type="text" id="slackSigningSecret" placeholder="a1b2c3..." />
        <div class="hint">App Settings → Basic Information → Signing Secret</div>
      </div>
    </div>

    <div class="btn-row">
      <button class="btn btn-secondary" onclick="goStep(1)">Back</button>
      <button class="btn btn-primary" onclick="goStep(3)">Continue</button>
    </div>
  </div>

  <!-- Step 3: AI Provider -->
  <div class="card" id="step3" style="display:none">
    <div class="card-title" id="s3title">Choose your AI provider</div>
    <div class="card-desc" id="s3desc">Select how Tetora connects to an AI model. You can change this later in config.json.</div>
    <div class="option-grid">
      <div class="option-card selected" onclick="selectProvider(this,'claude-cli')">
        <div class="option-icon">🤖</div>
        <div class="option-text">
          <div class="option-title">Claude Pro (Claude Code CLI)</div>
          <div class="option-desc">Requires Claude Pro subscription ($20/mo). No API key needed.</div>
        </div>
        <div class="option-badge">RECOMMENDED</div>
      </div>
      <div class="option-card" onclick="selectProvider(this,'claude-api')">
        <div class="option-icon">🔑</div>
        <div class="option-text">
          <div class="option-title">Claude API Key</div>
          <div class="option-desc">Pay-per-use API. Get a key at console.anthropic.com.</div>
        </div>
      </div>
      <div class="option-card" onclick="selectProvider(this,'openai')">
        <div class="option-icon">⚡</div>
        <div class="option-text">
          <div class="option-title">OpenAI / Compatible Endpoint</div>
          <div class="option-desc">OpenAI API, Ollama, LM Studio, Azure, or any OpenAI-compatible API.</div>
        </div>
      </div>
    </div>

    <!-- Claude CLI fields -->
    <div class="sub-fields visible" id="sf-claude-cli">
      <div class="field">
        <label>Claude CLI Path</label>
        <input type="text" id="claudePath" placeholder="Detecting..." />
        <div class="hint">Auto-detected. Change only if <code>claude</code> is installed in a non-standard location.</div>
      </div>
      <div class="field">
        <label>Default Model</label>
        <input type="text" id="claudeCLIModel" placeholder="sonnet" value="sonnet" />
        <div class="hint">Options: sonnet, opus, haiku</div>
      </div>
    </div>

    <!-- Claude API fields -->
    <div class="sub-fields" id="sf-claude-api">
      <div class="field">
        <label>API Key</label>
        <input type="password" id="claudeAPIKey" placeholder="sk-ant-api03-..." />
        <div class="hint"><a href="https://console.anthropic.com" target="_blank">console.anthropic.com</a> → API Keys → Create Key</div>
      </div>
      <div class="field">
        <label>Default Model</label>
        <input type="text" id="claudeAPIModel" placeholder="claude-sonnet-4-5-20250929" value="claude-sonnet-4-5-20250929" />
      </div>
    </div>

    <!-- OpenAI fields -->
    <div class="sub-fields" id="sf-openai">
      <div class="field">
        <label>API Endpoint</label>
        <input type="text" id="openaiEndpoint" placeholder="https://api.openai.com/v1" value="https://api.openai.com/v1" />
        <div class="hint">For Ollama: http://localhost:11434/v1 &nbsp;|&nbsp; For Azure: your Azure endpoint</div>
      </div>
      <div class="field">
        <label>API Key</label>
        <input type="password" id="openaiAPIKey" placeholder="sk-... (leave empty for Ollama)" />
      </div>
      <div class="field">
        <label>Default Model</label>
        <input type="text" id="openaiModel" placeholder="gpt-4o" value="gpt-4o" />
      </div>
    </div>

    <div class="error-msg" id="providerError">Please select a provider and fill in the required fields.</div>
    <div class="btn-row">
      <button class="btn btn-secondary" onclick="goStep(2)">Back</button>
      <button class="btn btn-primary" onclick="submitSetup()" id="submitBtn">Save & Finish</button>
    </div>
  </div>

  <!-- Step 4: Done -->
  <div class="card" id="step4" style="display:none">
    <div class="done-icon">✅</div>
    <div class="done-title">Setup complete!</div>
    <div class="done-sub">Your configuration has been saved to <code>~/.tetora/config.json</code></div>
    <div class="card-desc" style="margin-bottom:16px;text-align:center">Run these commands to start using Tetora:</div>
    <div class="code-block">tetora doctor</div>
    <div class="code-block">tetora serve</div>
    <div class="hint" style="text-align:center;margin-top:12px">
      Once the daemon is running, open the dashboard with: <strong>tetora dashboard</strong>
    </div>
  </div>
</div>

<script>
// --- State ---
var state = {
  lang: 'en',
  channel: 'none',
  provider: 'claude-cli',
  currentStep: 1
};

// --- Language selection ---
function selectLang(el, code) {
  document.querySelectorAll('#step1 .option-card').forEach(c => c.classList.remove('selected'));
  el.classList.add('selected');
  state.lang = code;
}

// --- Channel selection ---
function selectChannel(el, ch) {
  document.querySelectorAll('#step2 .option-card').forEach(c => c.classList.remove('selected'));
  el.classList.add('selected');
  state.channel = ch;
  // Show/hide sub-fields
  ['telegram','discord','slack'].forEach(function(c) {
    var sf = document.getElementById('sf-' + c);
    if (sf) sf.className = 'sub-fields' + (c === ch ? ' visible' : '');
  });
}

// --- Provider selection ---
function selectProvider(el, prov) {
  document.querySelectorAll('#step3 .option-card').forEach(c => c.classList.remove('selected'));
  el.classList.add('selected');
  state.provider = prov;
  ['claude-cli','claude-api','openai'].forEach(function(p) {
    var sf = document.getElementById('sf-' + p);
    if (sf) sf.className = 'sub-fields' + (p === prov ? ' visible' : '');
  });
}

// --- Step navigation ---
function goStep(n) {
  document.getElementById('step' + state.currentStep).style.display = 'none';
  document.getElementById('step' + n).style.display = 'block';
  // Update step indicators
  for (var i = 1; i <= 4; i++) {
    var sc = document.getElementById('sc' + i);
    var sl = document.getElementById('sl' + i);
    if (!sc) continue;
    if (i < n) {
      sc.className = 'step-circle done';
      sc.textContent = '\u2713';
      sl.className = 'step-label';
    } else if (i === n) {
      sc.className = 'step-circle active';
      sc.textContent = i;
      sl.className = 'step-label active';
    } else {
      sc.className = 'step-circle';
      sc.textContent = i;
      sl.className = 'step-label';
    }
    // Connector
    if (i < 4) {
      var conn = document.getElementById('conn' + i);
      if (conn) conn.className = 'step-connector' + (i < n ? ' done' : '');
    }
  }
  state.currentStep = n;
}

// --- Detect claude path ---
fetch('/api/setup/detect').then(function(r) { return r.json(); }).then(function(d) {
  if (d.claudePath) {
    document.getElementById('claudePath').value = d.claudePath;
    document.getElementById('claudePath').placeholder = d.claudePath;
  }
});

// --- Submit ---
function submitSetup() {
  var btn = document.getElementById('submitBtn');
  var errEl = document.getElementById('providerError');
  errEl.style.display = 'none';

  // Gather data
  var payload = {
    language: state.lang,
    channel: state.channel,
    provider: state.provider,
    telegramBotToken: document.getElementById('telegramToken').value.trim(),
    telegramChatID: parseInt(document.getElementById('telegramChatID').value) || 0,
    discordBotToken: document.getElementById('discordToken').value.trim(),
    discordAppID: document.getElementById('discordAppID').value.trim(),
    discordChannelID: document.getElementById('discordChannelID').value.trim(),
    slackBotToken: document.getElementById('slackToken').value.trim(),
    slackSigningSecret: document.getElementById('slackSigningSecret').value.trim(),
    claudePath: document.getElementById('claudePath').value.trim(),
    claudeAPIKey: document.getElementById('claudeAPIKey').value.trim(),
    openaiEndpoint: document.getElementById('openaiEndpoint').value.trim(),
    openaiAPIKey: document.getElementById('openaiAPIKey').value.trim(),
    defaultModel: ''
  };

  // Set model by provider
  switch (state.provider) {
    case 'claude-cli': payload.defaultModel = document.getElementById('claudeCLIModel').value.trim() || 'sonnet'; break;
    case 'claude-api': payload.defaultModel = document.getElementById('claudeAPIModel').value.trim() || 'claude-sonnet-4-5-20250929'; break;
    case 'openai':     payload.defaultModel = document.getElementById('openaiModel').value.trim() || 'gpt-4o'; break;
  }

  // Validate required fields
  if (state.provider === 'claude-api' && !payload.claudeAPIKey) {
    errEl.textContent = 'Claude API key is required.';
    errEl.style.display = 'block';
    return;
  }

  btn.disabled = true;
  btn.innerHTML = '<span class="spinner"></span>Saving...';

  fetch('/api/setup/save', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload)
  })
  .then(function(r) { return r.json(); })
  .then(function(d) {
    if (d.error) {
      errEl.textContent = 'Error: ' + d.error;
      errEl.style.display = 'block';
      btn.disabled = false;
      btn.textContent = 'Save & Finish';
      return;
    }
    goStep(4);
  })
  .catch(function(e) {
    errEl.textContent = 'Network error: ' + e.message;
    errEl.style.display = 'block';
    btn.disabled = false;
    btn.textContent = 'Save & Finish';
  });
}
</script>
</body>
</html>`)
