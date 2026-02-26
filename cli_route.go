package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

func cmdRouteDispatch(args []string) {
	dryRun := false
	var prompt string

	i := 0
	for i < len(args) {
		switch args[i] {
		case "--dry-run", "-n":
			dryRun = true
			i++
		case "--help":
			printRouteUsage()
			return
		default:
			if prompt == "" {
				prompt = args[i]
			}
			i++
		}
	}

	// Accept stdin.
	if prompt == "" {
		stat, _ := os.Stdin.Stat()
		if (stat.Mode() & os.ModeCharDevice) == 0 {
			data, err := io.ReadAll(os.Stdin)
			if err == nil && len(data) > 0 {
				prompt = strings.TrimSpace(string(data))
			}
		}
	}

	if prompt == "" {
		fmt.Fprintln(os.Stderr, "error: no prompt provided")
		fmt.Fprintln(os.Stderr, "usage: tetora route \"your task here\"")
		fmt.Fprintln(os.Stderr, "       echo \"task\" | tetora route")
		os.Exit(1)
	}

	cfg := loadConfig(findConfigPath())
	api := newAPIClient(cfg)
	api.client.Timeout = 0 // no timeout

	endpoint := "/route"
	if dryRun {
		endpoint = "/route/classify"
	}

	payload, _ := json.Marshal(map[string]string{"prompt": prompt})
	resp, err := api.do("POST", endpoint, strings.NewReader(string(payload)))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot reach daemon at %s: %v\n", cfg.ListenAddr, err)
		fmt.Fprintln(os.Stderr, "is the daemon running? try: tetora serve")
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "error: HTTP %d: %s\n", resp.StatusCode, body)
		os.Exit(1)
	}

	if dryRun {
		var route RouteResult
		json.NewDecoder(resp.Body).Decode(&route)
		fmt.Printf("Agent:      %s\n", route.Agent)
		fmt.Printf("Method:     %s\n", route.Method)
		fmt.Printf("Confidence: %s\n", route.Confidence)
		if route.Reason != "" {
			fmt.Printf("Reason:     %s\n", route.Reason)
		}
		return
	}

	var result SmartDispatchResult
	json.NewDecoder(resp.Body).Decode(&result)

	dur := time.Duration(result.Task.DurationMs) * time.Millisecond
	fmt.Fprintf(os.Stderr, "Route: %s (%s, %s) $%.2f %s\n",
		result.Route.Agent, result.Route.Method, result.Route.Confidence,
		result.Task.CostUSD, dur.Round(time.Second))

	if result.ReviewOK != nil {
		if *result.ReviewOK {
			fmt.Fprintln(os.Stderr, "Review: PASS")
		} else {
			fmt.Fprintln(os.Stderr, "Review: NEEDS REVIEW")
		}
		if result.Review != "" {
			fmt.Fprintf(os.Stderr, "  %s\n", result.Review)
		}
	}

	if result.Task.Output != "" {
		fmt.Println(result.Task.Output)
	}
	if result.Task.Error != "" {
		fmt.Fprintf(os.Stderr, "error: %s\n", result.Task.Error)
	}
	if result.Task.Status != "success" {
		os.Exit(1)
	}
}

func printRouteUsage() {
	fmt.Fprintf(os.Stderr, `tetora route — Smart dispatch (auto-route to best agent)

Usage:
  tetora route "your task description" [options]
  echo "task" | tetora route [options]

Options:
  --dry-run, -n     Classify only (don't execute)
  --help            Show this help

Examples:
  tetora route "Review this codebase for security issues"
  tetora route "Write a blog post about AI agents"
  tetora route --dry-run "Market research on AI tools"
  echo "Analyze competitor pricing" | tetora route
`)
}
