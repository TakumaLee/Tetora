package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func cmdLogs(args []string) {
	// Parse flags.
	follow := false
	errOnly := false
	jsonOnly := false
	lines := 50
	traceFilter := ""

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-f", "--follow":
			follow = true
		case "--err":
			errOnly = true
		case "--json":
			jsonOnly = true
		case "--trace":
			if i+1 < len(args) {
				i++
				traceFilter = args[i]
			}
		case "-n":
			if i+1 < len(args) {
				i++
				if n, err := strconv.Atoi(args[i]); err == nil && n > 0 {
					lines = n
				}
			}
		case "--help", "-h":
			fmt.Println("Usage: tetora logs [flags]")
			fmt.Println()
			fmt.Println("Flags:")
			fmt.Println("  -f, --follow     Follow log output (tail -f)")
			fmt.Println("  -n <lines>       Number of lines to show (default 50)")
			fmt.Println("  --err            Show error log only")
			fmt.Println("  --trace <id>     Filter by trace ID")
			fmt.Println("  --json           Show only JSON-formatted log lines")
			return
		}
	}

	logPath := findLogPath(errOnly)
	if logPath == "" {
		fmt.Fprintln(os.Stderr, "Log file not found.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Expected locations:")
		home, _ := os.UserHomeDir()
		fmt.Fprintf(os.Stderr, "  %s\n", filepath.Join(home, ".tetora", "logs", "tetora.log"))
		fmt.Fprintf(os.Stderr, "  %s\n", filepath.Join(home, ".tetora", "logs", "tetora.err"))
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Override with TETORA_LOG environment variable.")
		os.Exit(1)
	}

	// Trace filter mode: read file and filter.
	if traceFilter != "" {
		filterLogByTrace(logPath, traceFilter, lines, jsonOnly)
		return
	}

	// JSON-only filter mode.
	if jsonOnly {
		filterLogJSON(logPath, lines)
		return
	}

	if follow {
		// tail -f (interactive, takes over stdout).
		cmd := exec.Command("tail", "-f", logPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		fmt.Fprintf(os.Stderr, "Following %s (Ctrl+C to stop)\n", logPath)
		if err := cmd.Run(); err != nil {
			// User interrupted with Ctrl+C — that's fine.
			return
		}
	} else {
		// tail -N.
		cmd := exec.Command("tail", "-"+strconv.Itoa(lines), logPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Run()
	}
}

// filterLogByTrace reads the log file and prints lines matching the trace ID.
func filterLogByTrace(logPath, traceID string, maxLines int, jsonOnly bool) {
	f, err := os.Open(logPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot open %s: %v\n", logPath, err)
		os.Exit(1)
	}
	defer f.Close()

	var matches []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 256*1024), 256*1024) // 256KB line buffer

	for scanner.Scan() {
		line := scanner.Text()
		if !lineMatchesTrace(line, traceID) {
			continue
		}
		if jsonOnly && !strings.HasPrefix(strings.TrimSpace(line), "{") {
			continue
		}
		matches = append(matches, line)
	}

	// Show last N matches.
	start := 0
	if len(matches) > maxLines {
		start = len(matches) - maxLines
	}
	for _, line := range matches[start:] {
		fmt.Println(line)
	}

	if len(matches) == 0 {
		fmt.Fprintf(os.Stderr, "No log entries found for trace ID: %s\n", traceID)
	} else {
		fmt.Fprintf(os.Stderr, "\n--- %d entries for trace %s ---\n", len(matches), traceID)
	}
}

// filterLogJSON reads the log file and prints only JSON-formatted lines.
func filterLogJSON(logPath string, maxLines int) {
	f, err := os.Open(logPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot open %s: %v\n", logPath, err)
		os.Exit(1)
	}
	defer f.Close()

	var matches []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 256*1024), 256*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(strings.TrimSpace(line), "{") {
			matches = append(matches, line)
		}
	}

	start := 0
	if len(matches) > maxLines {
		start = len(matches) - maxLines
	}
	for _, line := range matches[start:] {
		fmt.Println(line)
	}
}

// lineMatchesTrace checks if a log line contains the given trace ID.
// Supports both JSON format ({"traceId":"xxx"}) and text format ([xxx]).
func lineMatchesTrace(line, traceID string) bool {
	// Quick substring check first.
	if !strings.Contains(line, traceID) {
		return false
	}
	// JSON: check traceId field.
	if strings.HasPrefix(strings.TrimSpace(line), "{") {
		var entry map[string]any
		if json.Unmarshal([]byte(line), &entry) == nil {
			if tid, ok := entry["traceId"].(string); ok && tid == traceID {
				return true
			}
		}
	}
	// Text: check [traceID] bracket pattern.
	if strings.Contains(line, "["+traceID+"]") {
		return true
	}
	return false
}

// findLogPath resolves the daemon log file path.
// Priority: TETORA_LOG env > ~/.tetora/logs/tetora.{log,err}
func findLogPath(errOnly bool) string {
	// Environment override.
	if env := os.Getenv("TETORA_LOG"); env != "" {
		if _, err := os.Stat(env); err == nil {
			return env
		}
	}

	home, _ := os.UserHomeDir()
	logDir := filepath.Join(home, ".tetora", "logs")

	if errOnly {
		p := filepath.Join(logDir, "tetora.err")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	// Default: stdout log.
	p := filepath.Join(logDir, "tetora.log")
	if _, err := os.Stat(p); err == nil {
		return p
	}

	// Fallback to err log if stdout log doesn't exist.
	p = filepath.Join(logDir, "tetora.err")
	if _, err := os.Stat(p); err == nil {
		return p
	}

	return ""
}
