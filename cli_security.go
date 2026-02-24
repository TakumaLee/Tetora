package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func cmdSecurity(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: tetora security <scan|baseline>")
		fmt.Println()
		fmt.Println("Commands:")
		fmt.Println("  scan       Run Sentori security scan")
		fmt.Println("  baseline   Create security baseline")
		fmt.Println()
		fmt.Println("Options:")
		fmt.Println("  --json     Output in JSON format")
		return
	}

	switch args[0] {
	case "scan":
		securityScan(args[1:])
	case "baseline":
		securityBaseline(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown security command: %s\n", args[0])
		os.Exit(1)
	}
}

func securityScan(args []string) {
	// Check if npx is available.
	npxPath, err := exec.LookPath("npx")
	if err != nil {
		fmt.Println("npx not found. Install Node.js to use Sentori security scanning.")
		fmt.Println()
		fmt.Println("Alternative: install globally with 'npm install -g @nexylore/sentori'")
		os.Exit(1)
	}

	// Determine scan target.
	scanPath := "."
	jsonOutput := false
	for _, a := range args {
		if a == "--json" {
			jsonOutput = true
		} else if a != "" {
			scanPath = a
		}
	}

	// Build command.
	cmdArgs := []string{"@nexylore/sentori", "scan", scanPath}
	if jsonOutput {
		cmdArgs = append(cmdArgs, "--json")
	}

	fmt.Printf("Running Sentori security scan on %s...\n\n", scanPath)

	cmd := exec.Command(npxPath, cmdArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Run(); err != nil {
		fmt.Printf("\nScan failed: %v\n", err)
		os.Exit(1)
	}
}

func securityBaseline(args []string) {
	npxPath, err := exec.LookPath("npx")
	if err != nil {
		fmt.Println("npx not found. Install Node.js to use Sentori security scanning.")
		os.Exit(1)
	}

	cfg := loadConfig(findConfigPath())

	reportDir := filepath.Join(cfg.RuntimeDir, "security")
	os.MkdirAll(reportDir, 0o755)
	reportPath := filepath.Join(reportDir, "baseline.json")

	cmdArgs := []string{"@nexylore/sentori", "scan", ".", "--json"}
	cmd := exec.Command(npxPath, cmdArgs...)
	out, err := cmd.Output()
	if err != nil {
		fmt.Printf("Scan failed: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(reportPath, out, 0o644); err != nil {
		fmt.Printf("Failed to write baseline: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Security baseline created: %s\n", reportPath)
}
