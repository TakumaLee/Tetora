package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var tetoraVersion = "dev"

func printUsage() {
	fmt.Fprintf(os.Stderr, `tetora v%s — AI Agent Orchestrator

Usage:
  tetora <command> [options]

Commands:
  serve              Start daemon (Telegram + Slack + HTTP + Cron)
  run                Dispatch tasks (CLI mode)
  dispatch           Run an ad-hoc task via the daemon
  route              Smart dispatch (auto-route to best agent)
  init               Interactive setup wizard
  doctor             Health checks and diagnostics
  status             Quick overview (daemon, jobs, cost)
  drain              Graceful shutdown: stop new tasks, wait for running agents to finish
  service <action>   Manage launchd service (install|uninstall|status)
  job <action>       Manage cron jobs (list|add|enable|disable|remove|trigger)
  agent <action>     Manage agents (list|add|show|set|remove)
  history <action>   View execution history (list|show|cost)
  config <action>    Manage config (show|set|validate|migrate)
  logs               View daemon logs ([-f] [-n N] [--err] [--trace ID] [--json])
  prompt <action>    Manage prompt templates (list|show|add|edit|remove)
  memory <action>    Manage agent memory (list|get|set|delete [--agent AGENT])
  mcp <action>       Manage MCP configs (list|show|add|remove|test)
  session <action>   View agent sessions (list|show)
  knowledge <action> Manage knowledge base (list|add|remove|path)
  skill <action>     Manage skills (list|run|test)
  workflow <action>  Manage workflows (list|show|validate|create|delete)
  budget <action>    Cost governance (show|pause|resume)
  webhook <action>   Manage incoming webhooks (list|show|test)
  data <action>      Data retention & privacy (status|cleanup|export|purge)
  security <action>  Security scanning (scan|baseline)
  plugin <action>    Manage external plugins (list|start|stop)
  access <action>    Manage agent directory access (list|add|remove)
  import <source>    Import data (openclaw|config)
  release            Build, tag, and publish a release (atomic pipeline)
  upgrade            Upgrade to the latest version
  backup             Create backup of tetora data
  restore            Restore from a backup file
  dashboard          Open web dashboard in browser
  completion <shell> Generate shell completion (bash|zsh|fish)
  version            Show version

Examples:
  tetora init                          Create config interactively
  tetora serve                         Start daemon
  tetora dispatch "Summarize README"    Run ad-hoc task via daemon
  tetora route "Review code security"  Auto-route to best agent
  tetora run --file tasks.json         Dispatch tasks from file
  tetora job list                      List all cron jobs
  tetora job trigger heartbeat         Manually trigger a job
  tetora agent list                    List all agents
  tetora agent add                     Create a new agent (interactive)
  tetora agent show <name>             Show agent details + soul preview
  tetora agent set <name> <field> <val> Update agent field (model, permission, description)
  tetora agent remove <name>           Remove an agent
  tetora history list                  Show recent execution history
  tetora history cost                  Show cost summary
  tetora config migrate --dry-run      Preview config migrations
  tetora session list                  List recent sessions
  tetora session list --agent <name>   List sessions for a specific agent
  tetora session show <id>            Show session conversation
  tetora backup                        Create backup
  tetora restore backup.tar.gz         Restore from backup
  tetora service install               Install as launchd service

`, tetoraVersion)
}

func cmdVersion() {
	fmt.Printf("tetora v%s (%s/%s)\n", tetoraVersion, runtime.GOOS, runtime.GOARCH)
}

func cmdUpgrade() {
	fmt.Printf("Current: v%s (%s/%s)\n", tetoraVersion, runtime.GOOS, runtime.GOARCH)

	// Fetch latest release tag from GitHub API.
	ghClient := &http.Client{Timeout: 15 * time.Second}
	resp, err := ghClient.Get("https://api.github.com/repos/TakumaLee/Tetora/releases/latest")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error checking latest release: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing release info: %v\n", err)
		os.Exit(1)
	}
	latest := strings.TrimPrefix(release.TagName, "v")
	if latest == tetoraVersion {
		fmt.Println("Already up to date.")
		return
	}
	fmt.Printf("Latest:  v%s\n", latest)

	// Determine binary name.
	arch := runtime.GOARCH
	binaryName := fmt.Sprintf("tetora-%s-%s", runtime.GOOS, arch)
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	dlURL := fmt.Sprintf("https://github.com/TakumaLee/Tetora/releases/download/%s/%s", release.TagName, binaryName)

	fmt.Printf("Downloading %s...\n", dlURL)
	dlClient := &http.Client{Timeout: 120 * time.Second} // binary ~15MB, allow time for slow connections
	dlResp, err := dlClient.Get(dlURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Download failed: %v\n", err)
		os.Exit(1)
	}
	defer dlResp.Body.Close()
	if dlResp.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "Download failed: HTTP %d\n", dlResp.StatusCode)
		os.Exit(1)
	}

	// Write to temp file then replace.
	selfPath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot determine binary path: %v\n", err)
		os.Exit(1)
	}
	selfPath, _ = filepath.EvalSymlinks(selfPath)

	tmpPath := selfPath + ".tmp"
	tmp, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot create temp file: %v\n", err)
		os.Exit(1)
	}
	if _, err := io.Copy(tmp, dlResp.Body); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		fmt.Fprintf(os.Stderr, "Download failed: %v\n", err)
		os.Exit(1)
	}
	tmp.Close()

	// Replace old binary.
	if err := os.Rename(tmpPath, selfPath); err != nil {
		os.Remove(tmpPath)
		fmt.Fprintf(os.Stderr, "Cannot replace binary: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Upgraded to v%s\n", latest)

	// Restart daemon automatically.
	home, _ := os.UserHomeDir()
	plist := filepath.Join(home, "Library", "LaunchAgents", "com.tetora.daemon.plist")
	if _, err := os.Stat(plist); err == nil {
		fmt.Println("Restarting service (launchd)...")
		exec.Command("launchctl", "unload", plist).Run()
		exec.Command("launchctl", "load", plist).Run()
		fmt.Println("Service restarted.")
		return
	}

	// No launchd — find running daemon process and restart it.
	if restartDaemonProcess(selfPath) {
		return
	}

	fmt.Println("\nNo running daemon found. Start with:")
	fmt.Println("  tetora serve")
}

// restartDaemonProcess finds a running "tetora serve" process, kills it,
// and starts a new one in the background. Returns true if restart succeeded.
func restartDaemonProcess(binaryPath string) bool {
	// Find daemon PID via pgrep.
	out, err := exec.Command("pgrep", "-f", "tetora serve").Output()
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		return false
	}

	// May have multiple PIDs (one per line), kill each with SIGTERM.
	pids := strings.Fields(strings.TrimSpace(string(out)))
	fmt.Printf("Stopping daemon (PID %s)...\n", strings.Join(pids, ", "))
	for _, pid := range pids {
		exec.Command("kill", pid).Run()
	}

	// Poll until all processes are dead (up to 3s), then SIGKILL stragglers.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
		check, _ := exec.Command("pgrep", "-f", "tetora serve").Output()
		if len(strings.TrimSpace(string(check))) == 0 {
			break
		}
	}
	// Force-kill any remaining processes.
	if remaining, _ := exec.Command("pgrep", "-f", "tetora serve").Output(); len(strings.TrimSpace(string(remaining))) > 0 {
		stragglers := strings.Fields(strings.TrimSpace(string(remaining)))
		fmt.Printf("Force-killing stragglers (PID %s)...\n", strings.Join(stragglers, ", "))
		for _, pid := range stragglers {
			exec.Command("kill", "-9", pid).Run()
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Start new daemon in background.
	fmt.Println("Starting daemon...")
	cmd := exec.Command(binaryPath, "serve")
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start daemon: %v\n", err)
		fmt.Println("Start manually with: tetora serve")
		return true // still return true since we killed the old one
	}

	// Release the child so it doesn't become a zombie.
	cmd.Process.Release()

	fmt.Printf("Daemon restarted (PID %d).\n", cmd.Process.Pid)
	return true
}

// cmdRestart restarts the running tetora daemon.
func cmdRestart() {
	selfPath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot determine executable path: %v\n", err)
		os.Exit(1)
	}
	selfPath, _ = filepath.EvalSymlinks(selfPath)

	// Try launchd first.
	home, _ := os.UserHomeDir()
	plist := filepath.Join(home, "Library", "LaunchAgents", "com.tetora.daemon.plist")
	if _, err := os.Stat(plist); err == nil {
		fmt.Println("Restarting service (launchd)...")
		exec.Command("launchctl", "unload", plist).Run()
		exec.Command("launchctl", "load", plist).Run()
		fmt.Println("Service restarted.")
		return
	}

	// No launchd — find running daemon process and restart it.
	if restartDaemonProcess(selfPath) {
		return
	}

	fmt.Println("No running daemon found. Start with:")
	fmt.Println("  tetora serve")
}

func cmdOpenDashboard() {
	cfg := loadConfig("")
	url := fmt.Sprintf("http://%s/dashboard", cfg.ListenAddr)
	fmt.Printf("Opening %s\n", url)
	exec.Command("open", url).Start()
}

// apiClient creates an HTTP client and base URL for daemon communication.
// Includes API token from config if set.
type apiClient struct {
	client  *http.Client
	baseURL string
	token   string
}

func newAPIClient(cfg *Config) *apiClient {
	return &apiClient{
		client:  &http.Client{Timeout: 5 * time.Second},
		baseURL: fmt.Sprintf("http://%s", cfg.ListenAddr),
		token:   cfg.APIToken,
	}
}

func (c *apiClient) do(method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	return c.client.Do(req)
}

func (c *apiClient) get(path string) (*http.Response, error) {
	return c.do("GET", path, nil)
}

func (c *apiClient) post(path string, body string) (*http.Response, error) {
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	return c.do("POST", path, r)
}

func (c *apiClient) postJSON(path string, v any) (*http.Response, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return c.do("POST", path, strings.NewReader(string(b)))
}

func findConfigPath() string {
	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "..", "config.json")
		if abs, err := filepath.Abs(candidate); err == nil {
			if _, err := os.Stat(abs); err == nil {
				return abs
			}
		}
	}
	home, _ := os.UserHomeDir()
	candidate := filepath.Join(home, ".tetora", "config.json")
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return "config.json"
}
