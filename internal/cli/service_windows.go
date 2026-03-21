//go:build windows

package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const windowsServiceName = "Tetora"

func windowsInstall() {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot resolve executable: %v\n", err)
		os.Exit(1)
	}
	exe, _ = filepath.Abs(exe)

	home, _ := os.UserHomeDir()
	tetoraDir := filepath.Join(home, ".tetora")
	os.MkdirAll(tetoraDir, 0o755)

	m, err := mgr.Connect()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot connect to Service Control Manager: %v\n", err)
		fmt.Fprintln(os.Stderr, "Tip: run as Administrator.")
		os.Exit(1)
	}
	defer m.Disconnect()

	// Check if already installed.
	if s, err := m.OpenService(windowsServiceName); err == nil {
		s.Close()
		fmt.Fprintf(os.Stderr, "Service '%s' already exists.\n", windowsServiceName)
		fmt.Fprintln(os.Stderr, "Use 'tetora service uninstall' to remove it first.")
		os.Exit(1)
	}

	s, err := m.CreateService(
		windowsServiceName,
		exe,
		mgr.Config{
			StartType:   mgr.StartAutomatic,
			DisplayName: "Tetora AI Assistant",
			Description: "Tetora AI Assistant Daemon — auto-starts on boot.",
		},
		"serve",
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot create service: %v\n", err)
		os.Exit(1)
	}
	defer s.Close()

	if err := s.Start("serve"); err != nil {
		fmt.Fprintf(os.Stderr, "Service installed but failed to start: %v\n", err)
		fmt.Fprintln(os.Stderr, "Start manually: sc start Tetora")
		os.Exit(1)
	}

	fmt.Printf("Service '%s' installed and started.\n", windowsServiceName)
	fmt.Println("Auto-start on boot: enabled")
	fmt.Printf("Working directory: %s\n", tetoraDir)
	fmt.Println("\nManage:")
	fmt.Println("  tetora service status     Check status")
	fmt.Println("  tetora service uninstall  Stop and remove")
	fmt.Println("  sc query Tetora           Windows SC tool")
}

func windowsUninstall() {
	m, err := mgr.Connect()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot connect to Service Control Manager: %v\n", err)
		fmt.Fprintln(os.Stderr, "Tip: run as Administrator.")
		os.Exit(1)
	}
	defer m.Disconnect()

	s, err := m.OpenService(windowsServiceName)
	if err != nil {
		fmt.Println("Service not installed.")
		return
	}
	defer s.Close()

	// Stop the service first, wait up to 15s.
	status, err := s.Control(svc.Stop)
	if err == nil {
		deadline := time.Now().Add(15 * time.Second)
		for status.State != svc.Stopped && time.Now().Before(deadline) {
			time.Sleep(300 * time.Millisecond)
			status, err = s.Query()
			if err != nil {
				break
			}
		}
	}

	if err := s.Delete(); err != nil {
		fmt.Fprintf(os.Stderr, "Cannot delete service: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Service stopped and removed.")
}

func windowsStatus() {
	m, err := mgr.Connect()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot connect to Service Control Manager: %v\n", err)
		fmt.Fprintln(os.Stderr, "Tip: run as Administrator.")
		os.Exit(1)
	}
	defer m.Disconnect()

	s, err := m.OpenService(windowsServiceName)
	if err != nil {
		fmt.Println("Service not installed.")
		fmt.Println("Install with: tetora service install")
		return
	}
	defer s.Close()

	status, err := s.Query()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot query service status: %v\n", err)
		os.Exit(1)
	}

	stateNames := map[svc.State]string{
		svc.Stopped:         "Stopped",
		svc.StartPending:    "Start Pending",
		svc.StopPending:     "Stop Pending",
		svc.Running:         "Running",
		svc.ContinuePending: "Continue Pending",
		svc.PausePending:    "Pause Pending",
		svc.Paused:          "Paused",
	}
	stateName, ok := stateNames[status.State]
	if !ok {
		stateName = fmt.Sprintf("Unknown(%d)", status.State)
	}

	cfg, _ := s.Config()
	startType := "Manual"
	if cfg.StartType == mgr.StartAutomatic {
		startType = "Automatic (auto-start on boot)"
	}

	fmt.Printf("Service: %s\n", windowsServiceName)
	fmt.Printf("  State:      %s\n", stateName)
	fmt.Printf("  Start Type: %s\n", startType)
}
