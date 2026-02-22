package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const plistLabel = "com.tetora.daemon"

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

func serviceInstall() {
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
    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key>
        <string>/usr/local/bin:/usr/bin:/bin:%s/.local/bin:%s/.nvm/versions/node/v22.15.1/bin</string>
    </dict>
</dict>
</plist>`, plistLabel, exe,
		filepath.Join(logDir, "tetora.log"),
		filepath.Join(logDir, "tetora.err"),
		tetoraDir,
		home, home)

	if err := os.WriteFile(plistPath, []byte(plist), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing plist: %v\n", err)
		os.Exit(1)
	}

	out, err := exec.Command("launchctl", "load", plistPath).CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "launchctl load: %s\n", strings.TrimSpace(string(out)))
		os.Exit(1)
	}

	fmt.Println("Service installed and started.")
	fmt.Printf("  Plist: %s\n", plistPath)
	fmt.Printf("  Logs:  %s/tetora.{log,err}\n", logDir)
	fmt.Println("\nManage:")
	fmt.Println("  tetora service status     Check status")
	fmt.Println("  tetora service uninstall  Stop and remove")
}

func serviceUninstall() {
	home, _ := os.UserHomeDir()
	plistPath := filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist")

	if _, err := os.Stat(plistPath); os.IsNotExist(err) {
		fmt.Println("Service not installed.")
		return
	}

	exec.Command("launchctl", "unload", plistPath).CombinedOutput()
	os.Remove(plistPath)
	fmt.Println("Service stopped and removed.")
}

func serviceStatus() {
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

	// Check plist exists.
	home, _ := os.UserHomeDir()
	plistPath := filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist")
	if _, err := os.Stat(plistPath); err == nil {
		fmt.Printf("  Plist: %s\n", plistPath)
	}
}
