package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	appName    = "Tetora Desktop"
	appVersion = "0.1.0"
	defaultAPI = "http://localhost:4649"
)

// App holds the desktop application state.
type App struct {
	apiBase    string
	apiToken   string
	httpClient *http.Client
	configPath string
}

// DesktopConfig holds the desktop companion configuration.
type DesktopConfig struct {
	APIBase   string       `json:"apiBase"`
	APIToken  string       `json:"apiToken,omitempty"`
	Tray      TrayConfig   `json:"tray"`
	Hotkey    HotkeyConfig `json:"hotkey"`
	AutoStart bool         `json:"autoStart"`
	DeepLinks bool         `json:"deepLinks"`
	Notify    NotifyConfig `json:"notify"`
}

type TrayConfig struct {
	Enabled      bool     `json:"enabled"`
	QuickActions []string `json:"quickActions,omitempty"`
}

type HotkeyConfig struct {
	Enabled bool   `json:"enabled"`
	Binding string `json:"binding"` // e.g. "Cmd+Shift+T"
}

type NotifyConfig struct {
	Enabled bool `json:"enabled"`
	Sound   bool `json:"sound"`
}

func NewApp() *App {
	configDir, _ := os.UserConfigDir()
	return &App{
		apiBase:    defaultAPI,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		configPath: filepath.Join(configDir, "tetora-desktop", "config.json"),
	}
}

func (a *App) LoadConfig() (*DesktopConfig, error) {
	data, err := os.ReadFile(a.configPath)
	if err != nil {
		return defaultConfig(), nil
	}
	var cfg DesktopConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return defaultConfig(), nil
	}
	if cfg.APIBase != "" {
		a.apiBase = cfg.APIBase
	}
	a.apiToken = cfg.APIToken
	return &cfg, nil
}

func defaultConfig() *DesktopConfig {
	return &DesktopConfig{
		APIBase:   defaultAPI,
		Tray:      TrayConfig{Enabled: true, QuickActions: []string{"status", "quick"}},
		Hotkey:    HotkeyConfig{Enabled: true, Binding: "Cmd+Shift+T"},
		AutoStart: false,
		DeepLinks: true,
		Notify:    NotifyConfig{Enabled: true, Sound: true},
	}
}

// Dispatch sends a prompt to the Tetora daemon.
func (a *App) Dispatch(prompt string) (string, error) {
	payload := fmt.Sprintf(`{"prompt":%q}`, prompt)
	req, err := http.NewRequest("POST", a.apiBase+"/api/dispatch", strings.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if a.apiToken != "" {
		req.Header.Set("Authorization", "Bearer "+a.apiToken)
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("dispatch failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return "", fmt.Errorf("dispatch error %d: %s", resp.StatusCode, string(body))
	}
	return string(body), nil
}

// Status checks the Tetora daemon status.
func (a *App) Status() (string, error) {
	req, err := http.NewRequest("GET", a.apiBase+"/api/status", nil)
	if err != nil {
		return "", err
	}
	if a.apiToken != "" {
		req.Header.Set("Authorization", "Bearer "+a.apiToken)
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "offline", nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body), nil
}

// HandleDeepLink processes tetora:// deep links.
func (a *App) HandleDeepLink(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid deep link: %w", err)
	}
	if u.Scheme != "tetora" {
		return "", fmt.Errorf("unsupported scheme: %s", u.Scheme)
	}
	switch u.Host {
	case "dispatch":
		prompt := u.Query().Get("prompt")
		if prompt == "" {
			return "", fmt.Errorf("missing prompt parameter")
		}
		return a.Dispatch(prompt)
	case "status":
		return a.Status()
	default:
		return "", fmt.Errorf("unknown deep link action: %s", u.Host)
	}
}

func main() {
	app := NewApp()
	cfg, err := app.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("%s v%s\n", appName, appVersion)
	fmt.Printf("API: %s\n", app.apiBase)
	fmt.Printf("Tray: %v, Hotkey: %v (%s)\n", cfg.Tray.Enabled, cfg.Hotkey.Enabled, cfg.Hotkey.Binding)
	fmt.Printf("AutoStart: %v, DeepLinks: %v\n", cfg.AutoStart, cfg.DeepLinks)

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "dispatch":
			if len(os.Args) < 3 {
				fmt.Fprintln(os.Stderr, "usage: tetora-desktop dispatch <prompt>")
				os.Exit(1)
			}
			result, err := app.Dispatch(strings.Join(os.Args[2:], " "))
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
			fmt.Println(result)
		case "status":
			status, err := app.Status()
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
			fmt.Println(status)
		case "deeplink":
			if len(os.Args) < 3 {
				fmt.Fprintln(os.Stderr, "usage: tetora-desktop deeplink <url>")
				os.Exit(1)
			}
			result, err := app.HandleDeepLink(os.Args[2])
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
			fmt.Println(result)
		default:
			fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
			os.Exit(1)
		}
		return
	}

	// GUI mode — wait for signal (stub: requires Wails v3 for actual windowing)
	fmt.Println("Running in GUI mode (stub). Press Ctrl+C to exit.")
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	fmt.Println("Shutting down.")
}
