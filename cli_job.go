package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
)

func cmdJob(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: tetora job <list|add|enable|disable|remove|trigger> [id]")
		return
	}
	switch args[0] {
	case "list", "ls":
		jobList()
	case "add":
		jobAdd()
	case "enable":
		if len(args) < 2 {
			fmt.Println("Usage: tetora job enable <id>")
			return
		}
		jobToggle(args[1], true)
	case "disable":
		if len(args) < 2 {
			fmt.Println("Usage: tetora job disable <id>")
			return
		}
		jobToggle(args[1], false)
	case "remove", "rm":
		if len(args) < 2 {
			fmt.Println("Usage: tetora job remove <id>")
			return
		}
		jobRemove(args[1])
	case "trigger", "run":
		if len(args) < 2 {
			fmt.Println("Usage: tetora job trigger <id>")
			return
		}
		jobTrigger(args[1])
	default:
		fmt.Fprintf(os.Stderr, "Unknown action: %s\n", args[0])
	}
}

func jobList() {
	jf := loadJobsFile()
	if len(jf.Jobs) == 0 {
		fmt.Println("No jobs configured.")
		return
	}

	// Try to get avg costs from history DB.
	cfg := loadConfig(findConfigPath())
	avgCosts := make(map[string]float64)
	if cfg.HistoryDB != "" {
		for _, j := range jf.Jobs {
			if avg := queryJobAvgCost(cfg.HistoryDB, j.ID); avg > 0 {
				avgCosts[j.ID] = avg
			}
		}
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintf(w, "STATUS\tID\tNAME\tSCHEDULE\tROLE\tMODEL\tAVG COST\n")
	for _, j := range jf.Jobs {
		status := "off"
		if j.Enabled {
			status = "on"
		}
		role := j.Agent
		if role == "" {
			role = "-"
		}
		model := j.Task.Model
		if model == "" {
			model = "default"
		}
		avgStr := "-"
		if avg, ok := avgCosts[j.ID]; ok {
			avgStr = fmt.Sprintf("$%.2f", avg)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			status, j.ID, j.Name, j.Schedule, role, model, avgStr)
	}
	w.Flush()
	fmt.Printf("\n%d jobs total\n", len(jf.Jobs))
}

func jobAdd() {
	scanner := bufio.NewScanner(os.Stdin)
	prompt := func(label, defaultVal string) string {
		if defaultVal != "" {
			fmt.Printf("  %s [%s]: ", label, defaultVal)
		} else {
			fmt.Printf("  %s: ", label)
		}
		scanner.Scan()
		s := strings.TrimSpace(scanner.Text())
		if s == "" {
			return defaultVal
		}
		return s
	}

	fmt.Println("=== Add Cron Job ===")
	fmt.Println()

	id := prompt("Job ID (unique)", "")
	if id == "" {
		fmt.Println("ID is required.")
		return
	}
	name := prompt("Display name", id)
	schedule := prompt("Cron schedule (m h dom mon dow)", "")
	if schedule == "" {
		fmt.Println("Schedule is required.")
		return
	}

	// Validate schedule.
	if _, err := parseCronExpr(schedule); err != nil {
		fmt.Printf("Invalid schedule: %v\n", err)
		return
	}

	tz := prompt("Timezone", "Asia/Taipei")
	promptText := prompt("Prompt", "")
	if promptText == "" {
		fmt.Println("Prompt is required.")
		return
	}
	model := prompt("Model", "sonnet")
	timeout := prompt("Timeout", "5m")
	budgetStr := prompt("Budget (USD)", "2.00")
	budget, _ := strconv.ParseFloat(budgetStr, 64)
	if budget <= 0 {
		budget = 2.0
	}
	role := prompt("Agent (optional)", "")

	fmt.Println()
	fmt.Println("  Permission modes:")
	fmt.Println("    plan               Read-only, planning mode")
	fmt.Println("    acceptEdits        Accept file edits (default)")
	fmt.Println("    autoEdit           Auto-accept edits")
	fmt.Println("    bypassPermissions  Skip all confirmations")
	permMode := prompt("Permission mode", "")
	if permMode != "" {
		valid := false
		for _, v := range []string{"plan", "acceptEdits", "autoEdit", "bypassPermissions"} {
			if permMode == v {
				valid = true
				break
			}
		}
		if !valid {
			fmt.Printf("Invalid permission mode: %q\n", permMode)
			return
		}
	}

	notify := strings.ToLower(prompt("Notify on complete? [y/N]", "n")) == "y"

	job := CronJobConfig{
		ID:       id,
		Name:     name,
		Enabled:  true,
		Schedule: schedule,
		TZ:       tz,
		Agent:     role,
		Task: CronTaskConfig{
			Prompt:         promptText,
			Model:          model,
			Timeout:        timeout,
			Budget:         budget,
			PermissionMode: permMode,
		},
		Notify: notify,
	}

	// Load, append, save.
	jf := loadJobsFile()

	// Check duplicate ID.
	for _, j := range jf.Jobs {
		if j.ID == id {
			fmt.Printf("Job %q already exists.\n", id)
			return
		}
	}

	jf.Jobs = append(jf.Jobs, job)
	saveJobsFile(jf)
	fmt.Printf("\nJob %q added. Restart daemon to apply.\n", id)
}

func jobToggle(id string, enabled bool) {
	jf := loadJobsFile()
	found := false
	for i := range jf.Jobs {
		if jf.Jobs[i].ID == id {
			jf.Jobs[i].Enabled = enabled
			found = true
			break
		}
	}
	if !found {
		fmt.Printf("Job %q not found.\n", id)
		return
	}
	saveJobsFile(jf)

	action := "enabled"
	if !enabled {
		action = "disabled"
	}
	fmt.Printf("Job %q %s.\n", id, action)

	// Try to notify running daemon.
	cfg := loadConfig(findConfigPath())
	api := newAPIClient(cfg)
	resp, err := api.post(fmt.Sprintf("/cron/%s/toggle", id), fmt.Sprintf(`{"enabled":%v}`, enabled))
	if err == nil {
		resp.Body.Close()
		fmt.Println("(daemon updated)")
	}
}

func jobRemove(id string) {
	jf := loadJobsFile()
	idx := -1
	for i, j := range jf.Jobs {
		if j.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		fmt.Printf("Job %q not found.\n", id)
		return
	}
	jf.Jobs = append(jf.Jobs[:idx], jf.Jobs[idx+1:]...)
	saveJobsFile(jf)
	fmt.Printf("Job %q removed. Restart daemon to apply.\n", id)
}

func jobTrigger(id string) {
	cfg := loadConfig(findConfigPath())
	api := newAPIClient(cfg)
	resp, err := api.post(fmt.Sprintf("/cron/%s/run", id), "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot reach daemon at %s\n", cfg.ListenAddr)
		fmt.Fprintln(os.Stderr, "Is the daemon running? Start with: tetora serve")
		os.Exit(1)
	}
	defer resp.Body.Close()

	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)

	if resp.StatusCode == 200 {
		fmt.Printf("Job %q triggered.\n", id)
	} else {
		fmt.Printf("Error: %v\n", result["error"])
	}
}

// --- Jobs file helpers ---

func loadJobsFile() JobsFile {
	cfg := loadConfig(findConfigPath())
	data, err := os.ReadFile(cfg.JobsFile)
	if err != nil {
		if os.IsNotExist(err) {
			return JobsFile{}
		}
		fmt.Fprintf(os.Stderr, "Error reading jobs: %v\n", err)
		os.Exit(1)
	}
	var jf JobsFile
	if err := json.Unmarshal(data, &jf); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing jobs: %v\n", err)
		os.Exit(1)
	}
	return jf
}

func saveJobsFile(jf JobsFile) {
	cfg := loadConfig(findConfigPath())
	data, _ := json.MarshalIndent(jf, "", "  ")
	if err := os.WriteFile(cfg.JobsFile, append(data, '\n'), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing jobs: %v\n", err)
		os.Exit(1)
	}
}
