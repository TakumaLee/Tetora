package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
)

func cmdWorkflow(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: tetora workflow <command> [options]")
		fmt.Println()
		fmt.Println("Commands:")
		fmt.Println("  list                                       List all workflows")
		fmt.Println("  show   <name>                              Show workflow definition")
		fmt.Println("  validate <name|file>                       Validate a workflow")
		fmt.Println("  create <file>                              Import workflow from JSON file")
		fmt.Println("  delete <name>                              Delete a workflow")
		fmt.Println("  run  <name> [--var key=value ...] [--dry-run|--shadow]  Execute a workflow")
		fmt.Println("  runs [name]                                List workflow run history")
		fmt.Println("  status <run-id>                            Show run status")
		fmt.Println("  messages <run-id>                          Show agent messages for a run")
		fmt.Println("  history  <name>                            Show version history")
		fmt.Println("  rollback <name> <version-id>               Restore to a previous version")
		fmt.Println("  diff     <version1> <version2>             Compare two versions")
		return
	}
	// Try version-related subcommands first.
	if handleWorkflowVersionSubcommands(args[0], args[1:]) {
		return
	}
	switch args[0] {
	case "list", "ls":
		workflowListCmd()
	case "show":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: tetora workflow show <name>")
			os.Exit(1)
		}
		workflowShowCmd(args[1])
	case "validate":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: tetora workflow validate <name|file>")
			os.Exit(1)
		}
		workflowValidateCmd(args[1])
	case "create":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: tetora workflow create <file>")
			os.Exit(1)
		}
		workflowCreateCmd(args[1])
	case "delete", "rm":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: tetora workflow delete <name>")
			os.Exit(1)
		}
		workflowDeleteCmd(args[1])
	case "run":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: tetora workflow run <name> [--var key=value ...] [--dry-run|--shadow]")
			os.Exit(1)
		}
		workflowRunCmd(args[1], args[2:])
	case "runs":
		name := ""
		if len(args) > 1 {
			name = args[1]
		}
		workflowRunsCmd(name)
	case "status":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: tetora workflow status <run-id>")
			os.Exit(1)
		}
		workflowStatusCmd(args[1])
	case "messages", "msgs":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: tetora workflow messages <run-id>")
			os.Exit(1)
		}
		workflowMessagesCmd(args[1])
	default:
		fmt.Fprintf(os.Stderr, "Unknown workflow action: %s\n", args[0])
		os.Exit(1)
	}
}

func workflowListCmd() {
	cfg := loadConfig(findConfigPath())
	workflows, err := listWorkflows(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(workflows) == 0 {
		fmt.Println("No workflows defined.")
		fmt.Printf("Create one in: %s\n", workflowDir(cfg))
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSTEPS\tTIMEOUT\tDESCRIPTION")
	for _, wf := range workflows {
		desc := wf.Description
		if len(desc) > 50 {
			desc = desc[:50] + "..."
		}
		timeout := wf.Timeout
		if timeout == "" {
			timeout = "-"
		}
		fmt.Fprintf(w, "%s\t%d\t%s\t%s\n", wf.Name, len(wf.Steps), timeout, desc)
	}
	w.Flush()
}

func workflowShowCmd(name string) {
	cfg := loadConfig(findConfigPath())
	wf, err := loadWorkflowByName(cfg, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Print summary header.
	fmt.Printf("Workflow: %s\n", wf.Name)
	if wf.Description != "" {
		fmt.Printf("Description: %s\n", wf.Description)
	}
	if wf.Timeout != "" {
		fmt.Printf("Timeout: %s\n", wf.Timeout)
	}
	if len(wf.Variables) > 0 {
		fmt.Println("Variables:")
		for k, v := range wf.Variables {
			if v == "" {
				fmt.Printf("  %s (required)\n", k)
			} else {
				fmt.Printf("  %s = %q\n", k, v)
			}
		}
	}

	fmt.Printf("\nSteps (%d):\n", len(wf.Steps))
	for i, s := range wf.Steps {
		st := s.Type
		if st == "" {
			st = "dispatch"
		}
		prefix := "  "
		if i == len(wf.Steps)-1 {
			prefix = "  "
		}
		fmt.Printf("%s[%s] %s (type=%s", prefix, s.ID, stepSummary(&s), st)
		if s.Agent != "" {
			fmt.Printf(", agent=%s", s.Agent)
		}
		if len(s.DependsOn) > 0 {
			fmt.Printf(", after=%s", strings.Join(s.DependsOn, ","))
		}
		fmt.Println(")")
	}

	// Also output raw JSON for piping.
	fmt.Println("\n--- JSON ---")
	data, _ := json.MarshalIndent(wf, "", "  ")
	fmt.Println(string(data))
}

func workflowValidateCmd(nameOrFile string) {
	cfg := loadConfig(findConfigPath())

	var wf *Workflow
	var err error

	// Try as file first, then as name.
	if strings.HasSuffix(nameOrFile, ".json") || strings.Contains(nameOrFile, "/") {
		wf, err = loadWorkflow(nameOrFile)
	} else {
		wf, err = loadWorkflowByName(cfg, nameOrFile)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading: %v\n", err)
		os.Exit(1)
	}

	errs := validateWorkflow(wf)
	if len(errs) == 0 {
		fmt.Printf("Workflow %q is valid. (%d steps)\n", wf.Name, len(wf.Steps))

		// Show execution order.
		order := topologicalSort(wf.Steps)
		fmt.Printf("Execution order: %s\n", strings.Join(order, " -> "))
	} else {
		fmt.Fprintf(os.Stderr, "Validation errors (%d):\n", len(errs))
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "  - %s\n", e)
		}
		os.Exit(1)
	}
}

func workflowCreateCmd(file string) {
	cfg := loadConfig(findConfigPath())

	wf, err := loadWorkflow(file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading %s: %v\n", file, err)
		os.Exit(1)
	}

	// Validate before saving.
	errs := validateWorkflow(wf)
	if len(errs) > 0 {
		fmt.Fprintf(os.Stderr, "Validation errors:\n")
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "  - %s\n", e)
		}
		os.Exit(1)
	}

	if err := saveWorkflow(cfg, wf); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Workflow %q saved. (%d steps)\n", wf.Name, len(wf.Steps))
}

func workflowDeleteCmd(name string) {
	cfg := loadConfig(findConfigPath())
	if err := deleteWorkflow(cfg, name); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Workflow %q deleted.\n", name)
}

func workflowRunCmd(name string, flags []string) {
	cfg := loadConfig(findConfigPath())

	wf, err := loadWorkflowByName(cfg, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Validate before running.
	if errs := validateWorkflow(wf); len(errs) > 0 {
		fmt.Fprintf(os.Stderr, "Validation errors:\n")
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "  - %s\n", e)
		}
		os.Exit(1)
	}

	// Parse --var key=value, --dry-run, --shadow flags.
	vars := make(map[string]string)
	dryRun := false
	shadow := false
	for i := 0; i < len(flags); i++ {
		switch flags[i] {
		case "--var":
			if i+1 < len(flags) {
				kv := flags[i+1]
				parts := strings.SplitN(kv, "=", 2)
				if len(parts) == 2 {
					vars[parts[0]] = parts[1]
				}
				i++
			}
		case "--dry-run":
			dryRun = true
		case "--shadow":
			shadow = true
		}
	}

	mode := WorkflowModeLive
	if dryRun {
		mode = WorkflowModeDryRun
		fmt.Printf("Dry-run mode: no provider calls, estimating costs...\n")
	} else if shadow {
		mode = WorkflowModeShadow
		fmt.Printf("Shadow mode: executing but not recording to history...\n")
	}

	fmt.Printf("Running workflow %q (%d steps)...\n", wf.Name, len(wf.Steps))

	// Create minimal state (no SSE for CLI).
	state := newDispatchState()
	sem := make(chan struct{}, cfg.MaxConcurrent)
	if cfg.MaxConcurrent <= 0 {
		sem = make(chan struct{}, 4)
	}
	cfg.registry = initProviders(cfg)

	run := executeWorkflow(context.Background(), cfg, wf, vars, state, sem, mode)

	// Print results.
	fmt.Printf("\nWorkflow: %s\n", run.WorkflowName)
	fmt.Printf("Run ID:   %s\n", run.ID)
	fmt.Printf("Status:   %s\n", run.Status)
	fmt.Printf("Duration: %dms\n", run.DurationMs)
	fmt.Printf("Cost:     $%.4f\n", run.TotalCost)

	if run.Error != "" {
		fmt.Printf("Error:    %s\n", run.Error)
	}

	fmt.Printf("\nStep Results:\n")
	order := topologicalSort(wf.Steps)
	for _, stepID := range order {
		sr := run.StepResults[stepID]
		if sr == nil {
			continue
		}
		icon := statusIcon(sr.Status)
		fmt.Printf("  %s [%s] %s (%dms, $%.4f)\n",
			icon, sr.StepID, sr.Status, sr.DurationMs, sr.CostUSD)
		if sr.Error != "" {
			fmt.Printf("      Error: %s\n", sr.Error)
		}
		if sr.Output != "" {
			preview := sr.Output
			if len(preview) > 200 {
				preview = preview[:200] + "..."
			}
			fmt.Printf("      Output: %s\n", strings.TrimSpace(preview))
		}
	}

	if run.Status != "success" {
		os.Exit(1)
	}
}

func workflowRunsCmd(name string) {
	cfg := loadConfig(findConfigPath())
	runs, err := queryWorkflowRuns(cfg.HistoryDB, 20, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(runs) == 0 {
		fmt.Println("No workflow runs found.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tWORKFLOW\tSTATUS\tDURATION\tCOST\tSTARTED")
	for _, r := range runs {
		id := r.ID
		if len(id) > 8 {
			id = id[:8]
		}
		dur := fmt.Sprintf("%dms", r.DurationMs)
		cost := fmt.Sprintf("$%.4f", r.TotalCost)
		started := r.StartedAt
		if len(started) > 19 {
			started = started[:19]
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			id, r.WorkflowName, r.Status, dur, cost, started)
	}
	w.Flush()
}

func workflowStatusCmd(runID string) {
	cfg := loadConfig(findConfigPath())

	run, err := queryWorkflowRunByID(cfg.HistoryDB, runID)
	if err != nil {
		// Try prefix match.
		runs, qerr := queryWorkflowRuns(cfg.HistoryDB, 100, "")
		if qerr != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		for _, r := range runs {
			if strings.HasPrefix(r.ID, runID) {
				run = &r
				break
			}
		}
		if run == nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}

	out, _ := json.MarshalIndent(run, "", "  ")
	fmt.Println(string(out))
}

func statusIcon(status string) string {
	switch status {
	case "success":
		return "OK"
	case "error":
		return "ERR"
	case "timeout":
		return "TMO"
	case "skipped":
		return "SKP"
	case "running":
		return "RUN"
	case "cancelled":
		return "CXL"
	default:
		return "---"
	}
}

func workflowMessagesCmd(runID string) {
	cfg := loadConfig(findConfigPath())

	// Resolve prefix match for run ID.
	resolvedID := resolveWorkflowRunID(cfg, runID)

	// Fetch handoffs.
	handoffs, err := queryHandoffs(cfg.HistoryDB, resolvedID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading handoffs: %v\n", err)
	}

	// Fetch agent messages.
	msgs, err := queryAgentMessages(cfg.HistoryDB, resolvedID, "", 100)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading messages: %v\n", err)
	}

	if len(handoffs) == 0 && len(msgs) == 0 {
		fmt.Println("No agent messages or handoffs for this run.")
		return
	}

	// Print handoffs.
	if len(handoffs) > 0 {
		fmt.Printf("Handoffs (%d):\n", len(handoffs))
		for _, h := range handoffs {
			id := h.ID
			if len(id) > 8 {
				id = id[:8]
			}
			fmt.Printf("  [%s] %s → %s  status=%s\n", id, h.FromAgent, h.ToAgent, h.Status)
			if h.Instruction != "" {
				inst := h.Instruction
				if len(inst) > 80 {
					inst = inst[:80] + "..."
				}
				fmt.Printf("         instruction: %s\n", inst)
			}
		}
		fmt.Println()
	}

	// Print messages.
	if len(msgs) > 0 {
		fmt.Printf("Agent Messages (%d):\n", len(msgs))
		for _, m := range msgs {
			ts := m.CreatedAt
			if len(ts) > 19 {
				ts = ts[:19]
			}
			content := m.Content
			if len(content) > 100 {
				content = content[:100] + "..."
			}
			fmt.Printf("  %s  [%s] %s → %s: %s\n", ts, m.Type, m.FromAgent, m.ToAgent, content)
		}
	}
}

// resolveWorkflowRunID tries to resolve a short prefix to a full run ID.
func resolveWorkflowRunID(cfg *Config, runID string) string {
	// Try exact match first.
	run, err := queryWorkflowRunByID(cfg.HistoryDB, runID)
	if err == nil && run != nil {
		return run.ID
	}
	// Try prefix match.
	runs, err := queryWorkflowRuns(cfg.HistoryDB, 100, "")
	if err != nil {
		return runID
	}
	for _, r := range runs {
		if strings.HasPrefix(r.ID, runID) {
			return r.ID
		}
	}
	return runID
}

// stepSummary returns a short summary of what a step does.
func stepSummary(s *WorkflowStep) string {
	st := s.Type
	if st == "" {
		st = "dispatch"
	}
	switch st {
	case "dispatch":
		p := s.Prompt
		if len(p) > 40 {
			p = p[:40] + "..."
		}
		return p
	case "skill":
		return fmt.Sprintf("skill:%s", s.Skill)
	case "condition":
		return fmt.Sprintf("if %s", s.If)
	case "parallel":
		return fmt.Sprintf("%d parallel tasks", len(s.Parallel))
	default:
		return st
	}
}
