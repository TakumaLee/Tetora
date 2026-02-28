package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func cmdService(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: tetora service <install|uninstall|status>")
		return
	}
	switch args[0] {
	case "install":
		serviceInstall()
	case "uninstall":
		serviceUninstall()
	case "status":
		serviceStatus()
	default:
		fmt.Fprintf(os.Stderr, "Unknown action: %s\n", args[0])
		os.Exit(1)
	}
}

// --- macOS launchd ---

const plistLabel = "com.tetora.daemon"

func launchdInstall() {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot resolve executable: %v\n", err)
		os.Exit(1)
	}
	exe, _ = filepath.Abs(exe)

	home, _ := os.UserHomeDir()
	tetoraDir := filepath.Join(home, ".tetora")
	logDir := filepath.Join(tetoraDir, "logs")
	os.MkdirAll(logDir, 0o755)

	plistDir := filepath.Join(home, "Library", "LaunchAgents")
	os.MkdirAll(plistDir, 0o755)
	plistPath := filepath.Join(plistDir, plistLabel+".plist")

	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>serve</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>%s</string>
    <key>StandardErrorPath</key>
    <string>%s</string>
    <key>WorkingDirectory</key>
    <string>%s</string>
</dict>
</plist>`, plistLabel, exe,
		filepath.Join(logDir, "tetora.log"),
		filepath.Join(logDir, "tetora.err"),
		tetoraDir)

	if err := os.WriteFile(plistPath, []byte(plist), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing plist: %v\n", err)
		os.Exit(1)
	}

	if err := restartLaunchd(plistPath); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	fmt.Println("Service installed and started.")
	fmt.Printf("  Plist: %s\n", plistPath)
	fmt.Printf("  Logs:  %s/tetora.{log,err}\n", logDir)
	fmt.Println("\nManage:")
	fmt.Println("  tetora service status     Check status")
	fmt.Println("  tetora service uninstall  Stop and remove")
}

func launchdUninstall() {
	home, _ := os.UserHomeDir()
	plistPath := filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist")

	if _, err := os.Stat(plistPath); os.IsNotExist(err) {
		fmt.Println("Service not installed.")
		return
	}

	stopLaunchd(plistPath)
	os.Remove(plistPath)
	fmt.Println("Service stopped and removed.")
}

// killDaemonProcess finds and kills running "tetora serve" processes.
// Sends SIGTERM first, waits up to 3s, then SIGKILL stragglers.
func killDaemonProcess() {
	out, err := exec.Command("pgrep", "-f", "tetora serve").Output()
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		return
	}
	pids := strings.Fields(strings.TrimSpace(string(out)))
	fmt.Printf("Stopping daemon (PID %s)...\n", strings.Join(pids, ", "))
	for _, pid := range pids {
		exec.Command("kill", pid).Run()
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
		check, _ := exec.Command("pgrep", "-f", "tetora serve").Output()
		if len(strings.TrimSpace(string(check))) == 0 {
			return
		}
	}
	if remaining, _ := exec.Command("pgrep", "-f", "tetora serve").Output(); len(strings.TrimSpace(string(remaining))) > 0 {
		for _, pid := range strings.Fields(strings.TrimSpace(string(remaining))) {
			exec.Command("kill", "-9", pid).Run()
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// restartLaunchd kills the running daemon, then uses launchctl bootout/bootstrap
// to restart the service. This is the modern replacement for unload/load.
func restartLaunchd(plistPath string) error {
	killDaemonProcess()

	uid := fmt.Sprintf("%d", os.Getuid())
	target := "gui/" + uid

	// bootout (ignore errors — may not be bootstrapped yet)
	exec.Command("launchctl", "bootout", target+"/"+plistLabel).Run()

	// bootstrap
	out, err := exec.Command("launchctl", "bootstrap", target, plistPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl bootstrap: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// stopLaunchd kills the daemon and bootout the service (for uninstall).
func stopLaunchd(plistPath string) {
	killDaemonProcess()
	uid := fmt.Sprintf("%d", os.Getuid())
	exec.Command("launchctl", "bootout", "gui/"+uid+"/"+plistLabel).Run()
}

func launchdStatus() {
	out, err := exec.Command("launchctl", "list").CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "launchctl error: %v\n", err)
		os.Exit(1)
	}

	found := false
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "tetora") {
			if !found {
				fmt.Println("Launchd service:")
			}
			fmt.Printf("  %s\n", line)
			found = true
		}
	}

	if !found {
		fmt.Println("Service not running.")
		fmt.Println("Install with: tetora service install")
		return
	}

	home, _ := os.UserHomeDir()
	plistPath := filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist")
	if _, err := os.Stat(plistPath); err == nil {
		fmt.Printf("  Plist: %s\n", plistPath)
	}
}

// --- Linux systemd ---

const systemdUnit = "tetora.service"

func systemdInstall() {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot resolve executable: %v\n", err)
		os.Exit(1)
	}
	exe, _ = filepath.Abs(exe)

	home, _ := os.UserHomeDir()
	tetoraDir := filepath.Join(home, ".tetora")
	os.MkdirAll(tetoraDir, 0o755)

	unitDir := filepath.Join(home, ".config", "systemd", "user")
	os.MkdirAll(unitDir, 0o755)
	unitPath := filepath.Join(unitDir, systemdUnit)

	unit := fmt.Sprintf(`[Unit]
Description=Tetora AI Assistant Daemon
After=network.target

[Service]
Type=simple
ExecStart=%s serve
WorkingDirectory=%s
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`, exe, tetoraDir)

	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing unit file: %v\n", err)
		os.Exit(1)
	}

	cmds := [][]string{
		{"systemctl", "--user", "daemon-reload"},
		{"systemctl", "--user", "enable", systemdUnit},
		{"systemctl", "--user", "start", systemdUnit},
	}
	for _, c := range cmds {
		out, err := exec.Command(c[0], c[1:]...).CombinedOutput()
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %s\n", strings.Join(c, " "), strings.TrimSpace(string(out)))
			os.Exit(1)
		}
	}

	fmt.Println("Service installed and started.")
	fmt.Printf("  Unit: %s\n", unitPath)
	fmt.Println("\nManage:")
	fmt.Println("  tetora service status     Check status")
	fmt.Println("  tetora service uninstall  Stop and remove")
	fmt.Println("  journalctl --user -u tetora -f   View logs")
}

func systemdUninstall() {
	home, _ := os.UserHomeDir()
	unitPath := filepath.Join(home, ".config", "systemd", "user", systemdUnit)

	if _, err := os.Stat(unitPath); os.IsNotExist(err) {
		fmt.Println("Service not installed.")
		return
	}

	exec.Command("systemctl", "--user", "stop", systemdUnit).CombinedOutput()
	exec.Command("systemctl", "--user", "disable", systemdUnit).CombinedOutput()
	os.Remove(unitPath)
	exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput()
	fmt.Println("Service stopped and removed.")
}

func systemdStatus() {
	out, err := exec.Command("systemctl", "--user", "status", systemdUnit).CombinedOutput()
	if err != nil {
		// systemctl returns exit code 3 for inactive services.
		if len(out) > 0 {
			fmt.Println(string(out))
			return
		}
		fmt.Println("Service not running.")
		fmt.Println("Install with: tetora service install")
		return
	}
	fmt.Println(string(out))
}

// --- Dispatcher ---

func serviceInstall() {
	switch runtime.GOOS {
	case "darwin":
		launchdInstall()
	case "linux":
		systemdInstall()
	default:
		fmt.Fprintf(os.Stderr, "Service management is not supported on %s.\n", runtime.GOOS)
		fmt.Fprintln(os.Stderr, "Run 'tetora serve' manually instead.")
		os.Exit(1)
	}
}

func serviceUninstall() {
	switch runtime.GOOS {
	case "darwin":
		launchdUninstall()
	case "linux":
		systemdUninstall()
	default:
		fmt.Fprintf(os.Stderr, "Service management is not supported on %s.\n", runtime.GOOS)
		os.Exit(1)
	}
}

func serviceStatus() {
	switch runtime.GOOS {
	case "darwin":
		launchdStatus()
	case "linux":
		systemdStatus()
	default:
		fmt.Fprintf(os.Stderr, "Service management is not supported on %s.\n", runtime.GOOS)
		os.Exit(1)
	}
}

// cmdDrain sends a drain request to the running daemon via the REST API.
// The daemon will stop accepting new tasks and exit after active tasks complete.
func cmdDrain() {
	cfg := loadConfig(findConfigPath())
	api := newAPIClient(cfg)

	resp, err := api.post("/api/admin/drain", "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot reach daemon at %s: %v\n", api.baseURL, err)
		fmt.Fprintln(os.Stderr, "Is the daemon running? Start with: tetora serve")
		os.Exit(1)
	}
	defer resp.Body.Close()

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		fmt.Fprintf(os.Stderr, "Error reading response: %v\n", err)
		os.Exit(1)
	}

	if resp.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "Error: %v\n", result["error"])
		os.Exit(1)
	}

	active, _ := result["active"].(float64)
	fmt.Printf("Drain initiated.\n")
	fmt.Printf("  Active agents:  %d\n", int(active))
	fmt.Printf("  Status:         %s\n", result["status"])
	if int(active) == 0 {
		fmt.Println("  No active agents — daemon will shut down immediately.")
	} else {
		fmt.Printf("  Daemon will exit after all %d agent(s) complete.\n", int(active))
		fmt.Println("  Use 'tetora status' to monitor progress.")
	}
}
