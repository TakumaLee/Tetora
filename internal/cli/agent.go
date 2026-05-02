package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"tetora/internal/agent"
)

func CmdAgent(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: tetora agent <list|add|show|remove|configure|watch> [name]")
		return
	}
	switch args[0] {
	case "list", "ls":
		agentList()
	case "add":
		agentAdd()
	case "set":
		if len(args) < 4 {
			fmt.Println("Usage: tetora agent set <name> <field> <value>")
			fmt.Println("Fields: model, permission, description")
			return
		}
		agentSet(args[1], args[2], args[3])
	case "show":
		if len(args) < 2 {
			fmt.Println("Usage: tetora agent show <name>")
			return
		}
		agentShow(args[1])
	case "remove", "rm":
		if len(args) < 2 {
			fmt.Println("Usage: tetora agent remove <name>")
			return
		}
		agentRemove(args[1])
	case "configure":
		agentConfigure(args[1:])
	case "watch":
		agentWatch(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown action: %s\n", args[0])
	}
}

func agentList() {
	cfg := LoadCLIConfig(FindConfigPath())
	if len(cfg.Agents) == 0 {
		fmt.Println("No agents configured.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintf(w, "NAME\tMODEL\tPERMISSION\tSOUL FILE\tDESCRIPTION\n")
	for name, rc := range cfg.Agents {
		model := rc.Model
		if model == "" {
			model = "default"
		}
		perm := rc.PermissionMode
		if perm == "" {
			perm = "-"
		}
		soul := rc.SoulFile
		if soul == "" {
			soul = "-"
		}
		desc := rc.Description
		if desc == "" {
			desc = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", name, model, perm, soul, desc)
	}
	w.Flush()
	fmt.Printf("\n%d agents\n", len(cfg.Agents))
}

func agentAdd() {
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

	fmt.Println("=== Add Agent ===")
	fmt.Println()

	name := prompt("Agent name", "")
	if name == "" {
		fmt.Println("Name is required.")
		return
	}

	configPath := FindConfigPath()
	cfg := LoadCLIConfig(configPath)
	if _, exists := cfg.Agents[name]; exists {
		fmt.Printf("Agent %q already exists.\n", name)
		return
	}

	// Archetype selection.
	fmt.Println()
	fmt.Println("  Start from a template?")
	for i, a := range BuiltinArchetypes {
		fmt.Printf("    %d. %-12s %s\n", i+1, a.Name, a.Description)
	}
	fmt.Printf("    %d. %-12s Start from scratch\n", len(BuiltinArchetypes)+1, "blank")
	archChoice := prompt(fmt.Sprintf("Choose [1-%d]", len(BuiltinArchetypes)+1), fmt.Sprintf("%d", len(BuiltinArchetypes)+1))

	var archetype *AgentArchetype
	if n, err := strconv.Atoi(archChoice); err == nil && n >= 1 && n <= len(BuiltinArchetypes) {
		archetype = &BuiltinArchetypes[n-1]
	}

	defaultModel := "sonnet"
	defaultPerm := ""
	if archetype != nil {
		defaultModel = archetype.Model
		defaultPerm = archetype.PermissionMode
	}

	model := prompt("Model", defaultModel)
	description := prompt("Description", "")
	permMode := prompt("Permission mode (plan|acceptEdits|auto|bypassPermissions)", defaultPerm)

	var soulFile string
	if archetype != nil {
		// Auto-generate soul file in agents/{name}/ directory.
		soulFile = "SOUL.md"
		content := GenerateSoulContent(archetype, name)
		agentDir := filepath.Join(cfg.AgentsDir, name)
		soulPath := filepath.Join(agentDir, "SOUL.md")
		if err := os.MkdirAll(agentDir, 0o755); err != nil {
			fmt.Printf("Warning: could not create agent dir: %v\n", err)
		} else if _, err := os.Stat(soulPath); os.IsNotExist(err) {
			if err := os.WriteFile(soulPath, []byte(content), 0o644); err != nil {
				fmt.Printf("Warning: could not write soul file: %v\n", err)
			} else {
				fmt.Printf("  Created soul file: %s\n", soulPath)
			}
		} else {
			fmt.Printf("  Soul file already exists: %s\n", soulPath)
		}
	} else {
		soulFile = prompt("Soul file path (relative to agent dir)", "")
	}

	rc := AgentInfo{
		SoulFile:       soulFile,
		Model:          model,
		Description:    description,
		PermissionMode: permMode,
	}

	// Verify soul file exists if provided and not from archetype.
	if soulFile != "" && archetype == nil {
		path := soulFile
		if !filepath.IsAbs(path) && cfg.DefaultWorkdir != "" {
			path = filepath.Join(cfg.DefaultWorkdir, path)
		}
		if _, err := os.Stat(path); err != nil {
			fmt.Printf("Warning: soul file not found at %s\n", path)
			confirm := prompt("Continue anyway? [y/N]", "n")
			if strings.ToLower(confirm) != "y" {
				return
			}
		}
	}

	agentJSON, err := json.Marshal(rc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error marshaling agent: %v\n", err)
		os.Exit(1)
	}
	if err := UpdateConfigAgents(configPath, name, json.RawMessage(agentJSON)); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("\nAgent %q added.\n", name)
}

func agentShow(name string) {
	cfg := LoadCLIConfig(FindConfigPath())
	rc, ok := cfg.Agents[name]
	if !ok {
		fmt.Printf("Agent %q not found.\n", name)
		os.Exit(1)
	}

	model := rc.Model
	if model == "" {
		model = "default"
	}

	// Show workspace info.
	ws := ResolveWorkspace(cfg, name)
	fmt.Printf("Agent: %s\n", name)
	fmt.Printf("  Model:       %s\n", model)
	fmt.Printf("  Soul File:   %s\n", rc.SoulFile)
	fmt.Printf("  Agent Dir:   %s\n", filepath.Join(cfg.AgentsDir, name))
	fmt.Printf("  Workspace:   %s\n", ws.Dir)
	fmt.Printf("  Soul Path:   %s\n", ws.SoulFile)
	if rc.Description != "" {
		fmt.Printf("  Description: %s\n", rc.Description)
	}
	if rc.PermissionMode != "" {
		fmt.Printf("  Permission:  %s\n", rc.PermissionMode)
	}

	// Show soul file preview.
	if rc.SoulFile != "" {
		content, err := LoadAgentPrompt(cfg, name)
		if err != nil {
			fmt.Printf("\n  (soul file error: %v)\n", err)
			return
		}
		if content != "" {
			lines := strings.Split(content, "\n")
			maxLines := 30
			if len(lines) > maxLines {
				fmt.Printf("\n--- Soul Preview (first %d/%d lines) ---\n", maxLines, len(lines))
				fmt.Println(strings.Join(lines[:maxLines], "\n"))
				fmt.Println("...")
			} else {
				fmt.Printf("\n--- Soul Content (%d lines) ---\n", len(lines))
				fmt.Println(content)
			}
		}
	}
}

func agentSet(name, field, value string) {
	configPath := FindConfigPath()
	cfg := LoadCLIConfig(configPath)
	rc, ok := cfg.Agents[name]
	if !ok {
		fmt.Printf("Agent %q not found.\n", name)
		os.Exit(1)
	}

	switch field {
	case "model":
		rc.Model = value
	case "permission", "permissionMode":
		rc.PermissionMode = value
	case "description", "desc":
		rc.Description = value
	default:
		fmt.Printf("Unknown field %q. Use: model, permission, description\n", field)
		os.Exit(1)
	}

	agentJSON, err := json.Marshal(rc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if err := UpdateConfigAgents(configPath, name, json.RawMessage(agentJSON)); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Agent %q: %s -> %s\n", name, field, value)
}

func agentRemove(name string) {
	configPath := FindConfigPath()
	cfg := LoadCLIConfig(configPath)

	if _, ok := cfg.Agents[name]; !ok {
		fmt.Printf("Agent %q not found.\n", name)
		os.Exit(1)
	}

	// Check if any job uses this agent.
	jf := LoadJobsFile(cfg.JobsFile)
	var using []string
	for _, j := range jf.Jobs {
		if j.Agent == name {
			using = append(using, j.ID)
		}
	}
	if len(using) > 0 {
		fmt.Printf("Agent %q is used by jobs: %s\n", name, strings.Join(using, ", "))
		fmt.Println("Remove these job assignments first, or re-assign them.")
		os.Exit(1)
	}

	if err := UpdateConfigAgents(configPath, name, nil); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Agent %q removed.\n", name)
}

// agentConfigure runs `tetora agent configure <name>` or `--all`.
func agentConfigure(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: tetora agent configure <name>")
		fmt.Println("       tetora agent configure --all")
		return
	}

	cfg := LoadCLIConfig(FindConfigPath())
	claudePath := cfg.ClaudePath
	if claudePath == "" {
		claudePath = DetectClaude()
	}
	ioProtocolPath := filepath.Join(cfg.WorkspaceDir, "rules", "tetora-agent-io-protocol.md")

	if args[0] == "--all" {
		names := make([]string, 0, len(cfg.Agents))
		for name := range cfg.Agents {
			names = append(names, name)
		}
		sort.Strings(names)

		results, err := agent.ConfigureAll(claudePath, cfg.AgentsDir, ioProtocolPath, names)
		for _, r := range results {
			applyConfigureResult(cfg, r)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	name := args[0]
	if _, ok := cfg.Agents[name]; !ok {
		fmt.Printf("Agent %q not found. Use `tetora agent list` to see registered agents.\n", name)
		os.Exit(1)
	}

	r, err := agent.Configure(claudePath, cfg.AgentsDir, ioProtocolPath, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	applyConfigureResult(cfg, r)
	fmt.Printf("Agent %s configured.\n", name)
}

// applyConfigureResult adds cron jobs and sets config flags for the selected capabilities.
func applyConfigureResult(cfg *CLIConfig, r *agent.ConfigureResult) {
	// Apply config flags (e.g. deepMemoryExtract.enabled = true).
	if len(r.ConfigFlags) > 0 {
		if err := MutateConfig(cfg.ConfigPath, func(raw map[string]any) {
			for flagKey, val := range r.ConfigFlags {
				// flagKey format: "section.field" (e.g. "deepMemoryExtract.enabled")
				parts := strings.SplitN(flagKey, ".", 2)
				if len(parts) != 2 {
					continue
				}
				section, field := parts[0], parts[1]
				sec, ok := raw[section].(map[string]any)
				if !ok {
					sec = map[string]any{}
				}
				sec[field] = val
				raw[section] = sec
			}
		}); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not apply config flags for %s: %v\n", r.Agent, err)
		} else {
			for k := range r.ConfigFlags {
				fmt.Printf("  Config: %s = true\n", k)
			}
		}
	}

	if len(r.CronJobs) == 0 {
		return
	}
	jf := LoadJobsFile(cfg.JobsFile)
	changed := false
	for _, capID := range r.CronJobs {
		jobID := fmt.Sprintf("agent_%s_%s", r.Agent, strings.ReplaceAll(capID, ".", "_"))
		// Skip if already present.
		found := false
		for _, j := range jf.Jobs {
			if j.ID == jobID {
				found = true
				break
			}
		}
		if found {
			continue
		}
		cronExpr := capCronExpr(capID)
		if cronExpr == "" {
			continue
		}
		jf.Jobs = append(jf.Jobs, JobConfig{
			ID:      jobID,
			Name:    fmt.Sprintf("%s: %s", r.Agent, capID),
			Enabled: true,
			Schedule: cronExpr,
			Agent:   r.Agent,
			Task: TaskConfig{
				Prompt: buildCapabilityPrompt(r.Agent, capID),
			},
		})
		changed = true
		fmt.Printf("  Added cron job: %s (%s)\n", jobID, cronExpr)
	}
	if changed {
		SaveJobsFile(cfg.JobsFile, jf)
	}
}

func capCronExpr(capID string) string {
	for _, c := range agent.BuiltinCapabilities {
		if c.ID == capID {
			return c.CronExpr
		}
	}
	return ""
}

func buildCapabilityPrompt(agentName, capID string) string {
	switch capID {
	case "weekly-review":
		return fmt.Sprintf("Execute your weekly-review capability: run `tetora lesson promote` then `tetora rule audit`. Report what was promoted or flagged.")
	default:
		return fmt.Sprintf("Execute the %s capability.", capID)
	}
}

// agentWatch runs `tetora agent watch [--daemon] [--interval=<duration>]`.
func agentWatch(args []string) {
	daemon := false
	interval := 30 * time.Second

	for _, arg := range args {
		switch {
		case arg == "--daemon":
			daemon = true
		case strings.HasPrefix(arg, "--interval="):
			d, err := time.ParseDuration(strings.TrimPrefix(arg, "--interval="))
			if err != nil {
				fmt.Fprintf(os.Stderr, "Invalid interval: %v\n", err)
				os.Exit(1)
			}
			interval = d
		case arg == "--help":
			fmt.Println("Usage: tetora agent watch [--daemon] [--interval=<duration>]")
			fmt.Println("  --daemon            Run in background (logs to /tmp/tetora-watcher.log)")
			fmt.Println("  --interval=30s      Poll interval (default: 30s)")
			return
		}
	}

	cfg := LoadCLIConfig(FindConfigPath())
	claudePath := cfg.ClaudePath
	if claudePath == "" {
		claudePath = DetectClaude()
	}

	watchCfg := agent.WatchConfig{
		HistoryDB:  cfg.HistoryDB,
		AgentsDir:  cfg.AgentsDir,
		ClaudePath: claudePath,
		Interval:   interval,
	}

	if daemon {
		runWatchDaemon(watchCfg)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\nShutting down watcher...")
		cancel()
	}()

	if err := agent.Watch(ctx, watchCfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runWatchDaemon(cfg agent.WatchConfig) {
	executable, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot determine executable path: %v\n", err)
		os.Exit(1)
	}

	logPath := filepath.Join(os.TempDir(), "tetora-watcher.log")
	pidPath := filepath.Join(os.TempDir(), "tetora-watcher.pid")

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot open log file: %v\n", err)
		os.Exit(1)
	}

	cmd := buildDaemonCmd(executable, cfg.Interval)
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start daemon: %v\n", err)
		os.Exit(1)
	}
	_ = logFile.Close()

	if err := os.WriteFile(pidPath, []byte(fmt.Sprintf("%d\n", cmd.Process.Pid)), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not write PID file: %v\n", err)
	}

	fmt.Printf("Watcher daemon started (PID: %d)\n", cmd.Process.Pid)
	fmt.Printf("  Log: %s\n", logPath)
	fmt.Printf("  PID: %s\n", pidPath)
	fmt.Printf("  Stop: kill %d\n", cmd.Process.Pid)
}
