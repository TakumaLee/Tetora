package cli

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

func CmdService(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: tetora service <install|uninstall|status>")
		return
	}
	switch args[0] {
	case "install":
		ServiceInstall()
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

// PlistLabel is the macOS LaunchAgent identifier for the Tetora daemon.
const PlistLabel = "com.tetora.daemon"

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
	plistPath := filepath.Join(plistDir, PlistLabel+".plist")

	// Build PATH that includes common tool locations so spawned processes
	// (e.g. claude CLI via homebrew) are reachable from the launchd environment.
	daemonPath := "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"

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
    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key>
        <string>%s</string>
        <key>HOME</key>
        <string>%s</string>
        <key>USER</key>
        <string>%s</string>
    </dict>
</dict>
</plist>`, PlistLabel, exe,
		filepath.Join(logDir, "tetora.log"),
		filepath.Join(logDir, "tetora.err"),
		tetoraDir,
		daemonPath,
		home,
		os.Getenv("USER"))

	if err := os.WriteFile(plistPath, []byte(plist), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing plist: %v\n", err)
		os.Exit(1)
	}

	if err := RestartLaunchd(plistPath); err != nil {
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
	plistPath := filepath.Join(home, "Library", "LaunchAgents", PlistLabel+".plist")

	if _, err := os.Stat(plistPath); os.IsNotExist(err) {
		fmt.Println("Service not installed.")
		return
	}

	stopLaunchd(plistPath)
	os.Remove(plistPath)
	fmt.Println("Service stopped and removed.")
}

// FindDaemonPIDs returns PIDs of running tetora serve processes.
// Uses pgrep first, falls back to lsof on the HTTP port.
func FindDaemonPIDs() []string {
	// Try pgrep first.
	if out, err := exec.Command("pgrep", "-f", "tetora serve").Output(); err == nil {
		if pids := strings.Fields(strings.TrimSpace(string(out))); len(pids) > 0 {
			return pids
		}
	}
	// Fallback: find process holding port 8991.
	if out, err := exec.Command("lsof", "-ti", ":8991").Output(); err == nil {
		if pids := strings.Fields(strings.TrimSpace(string(out))); len(pids) > 0 {
			return pids
		}
	}
	return nil
}

// KillDaemonProcess finds and kills running "tetora serve" processes.
// Sends SIGTERM first, waits up to 15s for in-flight tasks to finish,
// then SIGKILL stragglers.
// Returns true if no daemon process remains.
func KillDaemonProcess() bool {
	pids := FindDaemonPIDs()
	if len(pids) == 0 {
		return true
	}
	fmt.Printf("Stopping daemon (PID %s)...\n", strings.Join(pids, ", "))
	for _, pid := range pids {
		exec.Command("kill", pid).Run() // SIGTERM
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
		if len(FindDaemonPIDs()) == 0 {
			fmt.Println("Daemon stopped.")
			return true
		}
	}
	// Force kill remaining.
	remaining := FindDaemonPIDs()
	if len(remaining) == 0 {
		fmt.Println("Daemon stopped.")
		return true
	}
	fmt.Printf("Force killing (PID %s)...\n", strings.Join(remaining, ", "))
	for _, pid := range remaining {
		exec.Command("kill", "-9", pid).Run()
	}
	time.Sleep(500 * time.Millisecond)
	if leftovers := FindDaemonPIDs(); len(leftovers) > 0 {
		fmt.Fprintf(os.Stderr, "ERROR: failed to kill daemon (PID %s)\n", strings.Join(leftovers, ", "))
		return false
	}
	fmt.Println("Daemon stopped (forced).")
	return true
}

// RestartLaunchd kills the running daemon, then uses launchctl bootout/bootstrap
// to restart the service. This is the modern replacement for unload/load.
func RestartLaunchd(plistPath string) error {
	KillDaemonProcess()

	uid := fmt.Sprintf("%d", os.Getuid())
	target := "gui/" + uid

	// bootout (ignore errors — may not be bootstrapped yet)
	exec.Command("launchctl", "bootout", target+"/"+PlistLabel).Run()

	// bootstrap
	out, err := exec.Command("launchctl", "bootstrap", target, plistPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl bootstrap: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// stopLaunchd kills the daemon and bootout the service (for uninstall).
func stopLaunchd(plistPath string) {
	KillDaemonProcess()
	uid := fmt.Sprintf("%d", os.Getuid())
	exec.Command("launchctl", "bootout", "gui/"+uid+"/"+PlistLabel).Run()
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
	plistPath := filepath.Join(home, "Library", "LaunchAgents", PlistLabel+".plist")
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

func ServiceInstall() {
	switch runtime.GOOS {
	case "darwin":
		launchdInstall()
	case "linux":
		systemdInstall()
	case "windows":
		windowsInstall()
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
	case "windows":
		windowsUninstall()
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
	case "windows":
		windowsStatus()
	default:
		fmt.Fprintf(os.Stderr, "Service management is not supported on %s.\n", runtime.GOOS)
		os.Exit(1)
	}
}

// CmdDrain sends a drain request to the running daemon via the REST API.
// The daemon will stop accepting new tasks and exit after active tasks complete.
func CmdDrain() {
	cfg := LoadCLIConfig(FindConfigPath())
	api := cfg.NewAPIClient()

	resp, err := api.Post("/api/admin/drain", "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot reach daemon at %s: %v\n", api.BaseURL, err)
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
