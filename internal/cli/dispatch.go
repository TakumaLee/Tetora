package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"tetora/internal/estimate"
)

// dispatchResult is a CLI-local copy of DispatchResult for decoding dispatch responses.
type dispatchResult struct {
	StartedAt  time.Time      `json:"startedAt"`
	FinishedAt time.Time      `json:"finishedAt"`
	DurationMs int64          `json:"durationMs"`
	TotalCost  float64        `json:"totalCostUsd"`
	Tasks      []dispatchTask `json:"tasks"`
	Summary    string         `json:"summary"`
}

// dispatchTask is a CLI-local copy of TaskResult for decoding dispatch task responses.
type dispatchTask struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Status     string  `json:"status"`
	ExitCode   int     `json:"exitCode"`
	Output     string  `json:"output"`
	Error      string  `json:"error,omitempty"`
	DurationMs int64   `json:"durationMs"`
	CostUSD    float64 `json:"costUsd"`
	Model      string  `json:"model"`
	QAApproved *bool   `json:"qaApproved,omitempty"`
	QAComment  string  `json:"qaComment,omitempty"`
	Attempts   int     `json:"attempts,omitempty"`
}

func CmdDispatch(args []string) {
	// Subcommand routing.
	if len(args) > 0 {
		switch args[0] {
		case "list":
			cfg := LoadCLIConfig(FindConfigPath())
			CmdDispatchList(cfg, cfg.NewAPIClient())
			return
		case "subtasks":
			if len(args) < 2 {
				fmt.Fprintln(os.Stderr, "usage: tetora dispatch subtasks <parentID>")
				os.Exit(1)
			}
			cfg := LoadCLIConfig(FindConfigPath())
			CmdDispatchSubtasks(cfg, cfg.NewAPIClient(), args[1])
			return
		}
	}

	// Parse flags.
	model := ""
	timeout := ""
	budget := 0.0
	workdir := ""
	permission := ""
	role := ""
	clientID := ""
	notify := false
	estimate_ := false
	decompose := false
	review := false
	allowDangerous := false
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
		case "--client":
			if i+1 < len(args) {
				clientID = args[i+1]
				i += 2
			} else {
				i++
			}
		case "--notify":
			notify = true
			i++
		case "--estimate", "-e":
			estimate_ = true
			i++
		case "--decompose":
			decompose = true
			i++
		case "--review":
			review = true
			i++
		case "--allow-dangerous":
			allowDangerous = true
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

	// Silence the unused variable warning — notify is intentionally a no-op in CLI context.
	_ = notify

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

	if decompose {
		fmt.Fprintln(os.Stderr, "warning: --decompose is not yet implemented; dispatching as single task")
	}

	if prompt == "" {
		fmt.Fprintln(os.Stderr, "error: no prompt provided")
		fmt.Fprintln(os.Stderr, "usage: tetora dispatch \"your prompt here\"")
		fmt.Fprintln(os.Stderr, "       echo \"prompt\" | tetora dispatch")
		os.Exit(1)
	}

	cfg := LoadCLIConfig(FindConfigPath())
	api := cfg.NewAPIClient()
	api.Client.Timeout = 0 // no timeout — dispatch can be long
	if clientID != "" {
		api.ClientID = clientID
	}
	if os.Getenv("TETORA_SOURCE") == "agent_dispatch" {
		api.SubAgent = true
	}

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
	if review {
		task["reviewLoop"] = true
	}
	if allowDangerous {
		task["allowDangerous"] = true
	}

	// If agent specified, fetch soul content and inject.
	if role != "" {
		// Always pass the agent name to the daemon so it can apply agent config
		// (model, permissionMode, workspace, etc.) server-side.
		task["agent"] = role

		resp, err := api.Get("/roles/" + role)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: cannot fetch agent %q from daemon: %v\n", role, err)
			os.Exit(1)
		}
		defer resp.Body.Close()
		if resp.StatusCode == 404 {
			fmt.Fprintf(os.Stderr, "error: agent %q not found — check `tetora agent list` for available agents\n", role)
			os.Exit(1)
		}
		if resp.StatusCode != 200 {
			fmt.Fprintf(os.Stderr, "error: fetching agent %q failed (HTTP %d)\n", role, resp.StatusCode)
			os.Exit(1)
		}
		var roleData map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&roleData); err != nil {
			fmt.Fprintf(os.Stderr, "error: parse agent %q response: %v\n", role, err)
			os.Exit(1)
		}
		// Inject soul content as system prompt prefix (daemon will also load it
		// server-side via task.Agent, but sending it here enables estimate mode).
		if sc, ok := roleData["soulContent"].(string); ok && sc != "" {
			task["systemPrompt"] = sc
		}
		// Use agent's model if not overridden by --model flag.
		if model == "" {
			if rm, ok := roleData["model"].(string); ok && rm != "" {
				task["model"] = rm
			}
		}
	}

	payload, _ := json.Marshal([]any{task})

	// Estimate-only mode: show cost estimate and exit.
	if estimate_ {
		resp, err := api.Do("POST", "/dispatch/estimate", strings.NewReader(string(payload)))
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

		var estResult estimate.EstimateResult
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

	resp, err := api.Do("POST", "/dispatch", strings.NewReader(string(payload)))
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

	var result dispatchResult
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
		suffix := ""
		if t.Attempts > 0 {
			qaStatus := "pending"
			if t.QAApproved != nil {
				if *t.QAApproved {
					qaStatus = "passed"
				} else {
					qaStatus = "failed"
				}
			}
			suffix = fmt.Sprintf(" [QA:%s, attempts:%d]", qaStatus, t.Attempts)
		}
		fmt.Fprintf(os.Stderr, "\n[%s] %s ($%.2f, %s)%s\n", icon, t.Name, t.CostUSD,
			elapsed.Round(time.Second), suffix)

		if t.Output != "" {
			fmt.Println(t.Output)
		}
		if t.Error != "" {
			fmt.Fprintf(os.Stderr, "error: %s\n", t.Error)
		}
		if t.QAComment != "" {
			fmt.Fprintf(os.Stderr, "qa: %s\n", t.QAComment)
		}
	}

	// Telegram notification is not available in CLI context (goes through daemon API).

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
  --role, -r        Agent name (injects soul prompt + agent model)
  --client          Client ID for tenant isolation (format: cli_<name>, e.g. cli_myapp)
  --notify          Send Telegram notification on completion
  --estimate, -e    Show cost estimate without executing (dry-run)
  --review          Enable Dev↔QA review loop (max 3 retries, auto-escalate)

Examples:
  tetora dispatch "Summarize the README.md"
  tetora dispatch -m opus -t 10m "Review this codebase for security issues"
  tetora dispatch -r 琉璃 -w ~/projects/myapp "Fix the failing tests"
  echo "Generate a changelog" | tetora dispatch --workdir ~/projects/myapp
`)
}
