package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

func cmdDispatch(args []string) {
	// Parse flags.
	model := ""
	timeout := ""
	budget := 0.0
	workdir := ""
	permission := ""
	role := ""
	notify := false
	estimate := false
	var prompt string

	i := 0
	for i < len(args) {
		switch args[i] {
		case "--model", "-m":
			if i+1 < len(args) {
				model = args[i+1]
				i += 2
			} else {
				i++
			}
		case "--timeout", "-t":
			if i+1 < len(args) {
				timeout = args[i+1]
				i += 2
			} else {
				i++
			}
		case "--budget", "-b":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%f", &budget)
				i += 2
			} else {
				i++
			}
		case "--workdir", "-w":
			if i+1 < len(args) {
				workdir = args[i+1]
				i += 2
			} else {
				i++
			}
		case "--permission":
			if i+1 < len(args) {
				permission = args[i+1]
				i += 2
			} else {
				i++
			}
		case "--role", "-r":
			if i+1 < len(args) {
				role = args[i+1]
				i += 2
			} else {
				i++
			}
		case "--notify":
			notify = true
			i++
		case "--estimate", "-e":
			estimate = true
			i++
		case "--help":
			printDispatchUsage()
			return
		default:
			// First non-flag argument is the prompt.
			if prompt == "" {
				prompt = args[i]
			}
			i++
		}
	}

	// If no prompt from args, try stdin.
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
		fmt.Fprintln(os.Stderr, "usage: tetora dispatch \"your prompt here\"")
		fmt.Fprintln(os.Stderr, "       echo \"prompt\" | tetora dispatch")
		os.Exit(1)
	}

	cfg := loadConfig(findConfigPath())
	api := newAPIClient(cfg)
	api.client.Timeout = 0 // no timeout — dispatch can be long

	// Build task payload.
	task := map[string]any{
		"prompt": prompt,
	}
	if model != "" {
		task["model"] = model
	}
	if timeout != "" {
		task["timeout"] = timeout
	}
	if budget > 0 {
		task["budget"] = budget
	}
	if workdir != "" {
		task["workdir"] = workdir
	}
	if permission != "" {
		task["permissionMode"] = permission
	}

	// If role specified, fetch soul content and inject.
	if role != "" {
		// Always pass the role name to the daemon so it can apply role config
		// (model, permissionMode, workspace, etc.) server-side.
		task["role"] = role

		resp, err := api.get("/roles/" + role)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: cannot fetch role %q from daemon: %v\n", role, err)
			os.Exit(1)
		}
		defer resp.Body.Close()
		if resp.StatusCode == 404 {
			fmt.Fprintf(os.Stderr, "error: role %q not found — check `tetora role list` for available roles\n", role)
			os.Exit(1)
		}
		if resp.StatusCode != 200 {
			fmt.Fprintf(os.Stderr, "error: fetching role %q failed (HTTP %d)\n", role, resp.StatusCode)
			os.Exit(1)
		}
		var roleData map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&roleData); err != nil {
			fmt.Fprintf(os.Stderr, "error: parse role %q response: %v\n", role, err)
			os.Exit(1)
		}
		// Inject soul content as system prompt prefix (daemon will also load it
		// server-side via task.Role, but sending it here enables estimate mode).
		if sc, ok := roleData["soulContent"].(string); ok && sc != "" {
			task["systemPrompt"] = sc
		}
		// Use role's model if not overridden by --model flag.
		if model == "" {
			if rm, ok := roleData["model"].(string); ok && rm != "" {
				task["model"] = rm
			}
		}
	}

	payload, _ := json.Marshal([]any{task})

	// Estimate-only mode: show cost estimate and exit.
	if estimate {
		resp, err := api.do("POST", "/dispatch/estimate", strings.NewReader(string(payload)))
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: cannot reach daemon at %s: %v\n", cfg.ListenAddr, err)
			fmt.Fprintln(os.Stderr, "is the daemon running? try: tetora serve")
			os.Exit(1)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(resp.Body)
			fmt.Fprintf(os.Stderr, "error: estimate failed (HTTP %d): %s\n", resp.StatusCode, string(body))
			os.Exit(1)
		}

		var estResult EstimateResult
		if err := json.NewDecoder(resp.Body).Decode(&estResult); err != nil {
			fmt.Fprintf(os.Stderr, "error: parse estimate: %v\n", err)
			os.Exit(1)
		}

		fmt.Fprintln(os.Stderr, "Cost Estimate (dry-run)")
		fmt.Fprintln(os.Stderr, "")
		for _, t := range estResult.Tasks {
			fmt.Fprintf(os.Stderr, "  %s (%s, %s): ~$%.4f\n", t.Name, t.Provider, t.Model, t.EstimatedCostUSD)
			fmt.Fprintf(os.Stderr, "    %s\n", t.Breakdown)
		}
		if estResult.ClassifyCost > 0 {
			fmt.Fprintf(os.Stderr, "  Classification: ~$%.4f\n", estResult.ClassifyCost)
		}
		fmt.Fprintf(os.Stderr, "\n  Total estimated: $%.4f\n", estResult.TotalEstimatedCost)
		return
	}

	fmt.Fprintf(os.Stderr, "dispatching to %s...\n", cfg.ListenAddr)
	start := time.Now()

	resp, err := api.do("POST", "/dispatch", strings.NewReader(string(payload)))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot reach daemon at %s: %v\n", cfg.ListenAddr, err)
		fmt.Fprintln(os.Stderr, "is the daemon running? try: tetora serve")
		os.Exit(1)
	}
	defer resp.Body.Close()

	elapsed := time.Since(start)

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "error: dispatch failed (HTTP %d): %s\n", resp.StatusCode, string(body))
		os.Exit(1)
	}

	var result DispatchResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		fmt.Fprintf(os.Stderr, "error: parse response: %v\n", err)
		os.Exit(1)
	}

	// Display result.
	for _, t := range result.Tasks {
		icon := "OK"
		if t.Status != "success" {
			icon = t.Status
		}
		fmt.Fprintf(os.Stderr, "\n[%s] %s ($%.2f, %s)\n", icon, t.Name, t.CostUSD,
			elapsed.Round(time.Second))

		if t.Output != "" {
			fmt.Println(t.Output)
		}
		if t.Error != "" {
			fmt.Fprintf(os.Stderr, "error: %s\n", t.Error)
		}
	}

	// Telegram notification.
	if notify {
		msg := formatTelegramResult(&result)
		if err := sendTelegramNotify(&cfg.Telegram, msg); err != nil {
			fmt.Fprintf(os.Stderr, "warning: telegram notify: %v\n", err)
		}
	}

	// Exit non-zero if task failed.
	for _, t := range result.Tasks {
		if t.Status != "success" {
			os.Exit(1)
		}
	}
}

func printDispatchUsage() {
	fmt.Fprintf(os.Stderr, `tetora dispatch — Run an ad-hoc task via the daemon

Usage:
  tetora dispatch "your prompt here" [options]
  echo "prompt" | tetora dispatch [options]

Options:
  --model, -m       Model name (default: from config)
  --timeout, -t     Task timeout (default: from config)
  --budget, -b      Max budget in USD (default: from config)
  --workdir, -w     Working directory
  --permission      Permission mode (acceptEdits, bypassPermissions, plan)
  --role, -r        Role name (injects soul prompt + role model)
  --notify          Send Telegram notification on completion
  --estimate, -e    Show cost estimate without executing (dry-run)

Examples:
  tetora dispatch "Summarize the README.md"
  tetora dispatch -m opus -t 10m "Review this codebase for security issues"
  tetora dispatch -r 琉璃 -w ~/projects/myapp "Fix the failing tests"
  echo "Generate a changelog" | tetora dispatch --workdir ~/projects/myapp
`)
}
