package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"tetora/internal/skill"
)

// toSkillAppConfig builds a skill.AppConfig from CLIConfig.
func toSkillAppConfig(cfg *CLIConfig) *skill.AppConfig {
	return &skill.AppConfig{
		WorkspaceDir: cfg.WorkspaceDir,
		HistoryDB:    cfg.HistoryDB,
		BaseDir:      cfg.BaseDir,
	}
}

func CmdSkill(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: tetora skill <list|run|test|store|approve|reject|install|search|scan|init|log|stats|diagnostics> [name]")
		fmt.Println()
		fmt.Println("Commands:")
		fmt.Println("  list                                   List all skills (config + file-based)")
		fmt.Println("  run  <name> [--var key=value ...]      Execute a skill")
		fmt.Println("  test <name>                            Quick test (5s timeout)")
		fmt.Println("  store                                  List file-based skills (store)")
		fmt.Println("  approve <name>                         Approve a pending skill")
		fmt.Println("  reject  <name>                         Reject (delete) a pending skill")
		fmt.Println("  install <url>                          Install a skill from URL")
		fmt.Println("  search  <query>                        Search skill registry")
		fmt.Println("  scan    <name>                         Security scan a skill")
		fmt.Println("  init   [name]                          AI interview to generate SKILL.md")
		fmt.Println("  log    <name> [flags]                  Record a skill execution event")
		fmt.Println("  stats  [name] [--days N]               Show skill usage statistics")
		fmt.Println("  diagnostics                            Run skill trigger diagnostics")
		return
	}
	switch args[0] {
	case "list", "ls":
		skillListCmd()
	case "run":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: tetora skill run <name> [--var key=value ...]")
			os.Exit(1)
		}
		skillRunCmd(args[1], args[2:])
	case "test":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: tetora skill test <name>")
			os.Exit(1)
		}
		skillTestCmd(args[1])
	case "store":
		skillStoreCmd()
	case "approve":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: tetora skill approve <name>")
			os.Exit(1)
		}
		skillApproveCmd(args[1])
	case "reject":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: tetora skill reject <name>")
			os.Exit(1)
		}
		skillRejectCmd(args[1])
	case "install":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: tetora skill install <url>")
			os.Exit(1)
		}
		skillInstallCmd(args[1])
	case "search":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: tetora skill search <query>")
			os.Exit(1)
		}
		skillSearchCmd(strings.Join(args[1:], " "))
	case "scan":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: tetora skill scan <name>")
			os.Exit(1)
		}
		skillScanCmd(args[1])
	case "init":
		nameArg := ""
		if len(args) >= 2 {
			nameArg = args[1]
		}
		CmdSkillInit(nameArg)
	case "log":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: tetora skill log <name> --event=<type> [--source=<src>] [--status=<s>] [--duration=<ms>] [--error=\"...\"]")
			os.Exit(1)
		}
		skillLogCmd(args[1], args[2:])
	case "stats":
		nameArg := ""
		daysArg := 30
		for i := 1; i < len(args); i++ {
			if strings.HasPrefix(args[i], "--days=") {
				fmt.Sscanf(args[i], "--days=%d", &daysArg)
			} else if args[i] == "--days" && i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%d", &daysArg)
				i++
			} else if !strings.HasPrefix(args[i], "-") {
				nameArg = args[i]
			}
		}
		skillStatsCmd(nameArg, daysArg)
	case "diagnostics", "diag":
		skillDiagnosticsCmd(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown skill action: %s\n", args[0])
		os.Exit(1)
	}
}

// --- P27.1: CLI wrappers ---

func skillInstallCmd(url string) {
	cfg := LoadCLIConfig(FindConfigPath())
	input, _ := json.Marshal(map[string]any{"url": url, "auto_approve": false})
	result, err := skill.ToolSkillInstall(context.Background(), toSkillAppConfig(cfg), input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(result)
}

func skillSearchCmd(query string) {
	cfg := LoadCLIConfig(FindConfigPath())
	input, _ := json.Marshal(map[string]any{"query": query})
	result, err := skill.ToolSkillSearch(context.Background(), toSkillAppConfig(cfg), input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Pretty print results.
	var entries []map[string]any
	if json.Unmarshal([]byte(result), &entries) == nil && len(entries) > 0 {
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tDESCRIPTION\tURL")
		for _, e := range entries {
			name, _ := e["name"].(string)
			desc, _ := e["description"].(string)
			u, _ := e["url"].(string)
			if len(desc) > 50 {
				desc = desc[:50] + "..."
			}
			fmt.Fprintf(w, "%s\t%s\t%s\n", name, desc, u)
		}
		w.Flush()
	} else {
		fmt.Println(result)
	}
}

func skillScanCmd(name string) {
	cfg := LoadCLIConfig(FindConfigPath())
	input, _ := json.Marshal(map[string]any{"name": name})
	result, err := skill.ToolSentoriScan(context.Background(), toSkillAppConfig(cfg), input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Pretty print report.
	var report skill.SentoriReport
	if json.Unmarshal([]byte(result), &report) == nil {
		fmt.Printf("Skill: %s\n", report.SkillName)
		fmt.Printf("Risk:  %s (score: %d)\n", report.OverallRisk, report.Score)
		if len(report.Findings) > 0 {
			fmt.Println("Findings:")
			for _, f := range report.Findings {
				line := ""
				if f.Line > 0 {
					line = fmt.Sprintf(" (line %d)", f.Line)
				}
				fmt.Printf("  [%s] %s: %s%s\n", f.Severity, f.Category, f.Description, line)
			}
		} else {
			fmt.Println("No findings.")
		}
	} else {
		fmt.Println(result)
	}
}

func skillListCmd() {
	cfg := LoadCLIConfig(FindConfigPath())
	skills := skill.ListSkills(toSkillAppConfig(cfg))

	if len(skills) == 0 {
		fmt.Println("No skills configured.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tCOMMAND\tDESCRIPTION")
	for _, s := range skills {
		desc := s.Description
		if len(desc) > 60 {
			desc = desc[:60] + "..."
		}
		cmdStr := s.Command
		if len(s.Args) > 0 {
			cmdStr += " " + strings.Join(s.Args, " ")
		}
		if len(cmdStr) > 40 {
			cmdStr = cmdStr[:40] + "..."
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", s.Name, cmdStr, desc)
	}
	w.Flush()
}

func skillRunCmd(name string, flags []string) {
	cfg := LoadCLIConfig(FindConfigPath())
	s := skill.GetSkill(toSkillAppConfig(cfg), name)
	if s == nil {
		fmt.Fprintf(os.Stderr, "Error: skill %q not found\n", name)
		os.Exit(1)
	}

	// Parse --var key=value flags.
	vars := make(map[string]string)
	for i := 0; i < len(flags); i++ {
		if flags[i] == "--var" && i+1 < len(flags) {
			kv := flags[i+1]
			parts := strings.SplitN(kv, "=", 2)
			if len(parts) == 2 {
				vars[parts[0]] = parts[1]
			}
			i++
		}
	}

	result, err := skill.ExecuteSkill(context.Background(), *s, vars)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if s.OutputAs == "json" {
		out, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(out))
	} else {
		if result.Status != "success" {
			fmt.Fprintf(os.Stderr, "[%s] %s\n", result.Status, result.Error)
		}
		fmt.Print(result.Output)
		if result.Output != "" && !strings.HasSuffix(result.Output, "\n") {
			fmt.Println()
		}
		fmt.Fprintf(os.Stderr, "(%dms)\n", result.Duration)
	}

	if result.Status != "success" {
		os.Exit(1)
	}
}

func skillTestCmd(name string) {
	cfg := LoadCLIConfig(FindConfigPath())
	s := skill.GetSkill(toSkillAppConfig(cfg), name)
	if s == nil {
		fmt.Fprintf(os.Stderr, "Error: skill %q not found\n", name)
		os.Exit(1)
	}

	fmt.Printf("Testing skill %q (%s)...\n", name, s.Command)
	result, err := skill.TestSkill(context.Background(), *s)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if result.Status == "success" {
		fmt.Printf("OK (%dms)\n", result.Duration)
		if result.Output != "" {
			preview := result.Output
			if len(preview) > 200 {
				preview = preview[:200] + "..."
			}
			fmt.Printf("Output: %s\n", strings.TrimSpace(preview))
		}
	} else {
		fmt.Fprintf(os.Stderr, "FAIL: [%s] %s\n", result.Status, result.Error)
		if result.Output != "" {
			fmt.Fprintf(os.Stderr, "Output: %s\n", strings.TrimSpace(result.Output))
		}
		os.Exit(1)
	}
}

// --- P18.4: Self-Improving Skills CLI ---

func skillStoreCmd() {
	cfg := LoadCLIConfig(FindConfigPath())
	metas := skill.LoadAllFileSkillMetas(toSkillAppConfig(cfg))

	if len(metas) == 0 {
		fmt.Println("No file-based skills in store.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSTATUS\tUSAGE\tCREATED BY\tCREATED AT")
	for _, m := range metas {
		status := "pending"
		if m.Approved {
			status = "approved"
		}
		createdAt := m.CreatedAt
		if len(createdAt) > 10 {
			createdAt = createdAt[:10]
		}
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n",
			m.Name, status, m.UsageCount, m.CreatedBy, createdAt)
	}
	w.Flush()
}

func skillApproveCmd(name string) {
	cfg := LoadCLIConfig(FindConfigPath())
	if err := skill.ApproveSkill(toSkillAppConfig(cfg), name); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Skill %q approved.\n", name)

	// Record approval event.
	if cfg.HistoryDB != "" {
		skill.RecordSkillEvent(cfg.HistoryDB, name, "approved", "", "cli")
	}
}

func skillRejectCmd(name string) {
	cfg := LoadCLIConfig(FindConfigPath())
	if err := skill.RejectSkill(toSkillAppConfig(cfg), name); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Skill %q rejected and deleted.\n", name)

	// Record rejection event.
	if cfg.HistoryDB != "" {
		skill.RecordSkillEvent(cfg.HistoryDB, name, "rejected", "", "cli")
	}
}

// --- P18.5: Skill Observability CLI ---

// skillLogCmd records a skill execution event from the command line.
// Usage: tetora skill log <name> --event=invoked --source=claude-code --status=success
func skillLogCmd(name string, flags []string) {
	cfg := LoadCLIConfig(FindConfigPath())
	if cfg.HistoryDB == "" {
		fmt.Fprintln(os.Stderr, "Error: no history database configured")
		os.Exit(1)
	}

	var eventType, source, status, errorMsg, role string
	var durationMs int

	eventType = "invoked" // default
	for i := 0; i < len(flags); i++ {
		switch {
		case strings.HasPrefix(flags[i], "--event="):
			eventType = strings.TrimPrefix(flags[i], "--event=")
		case strings.HasPrefix(flags[i], "--source="):
			source = strings.TrimPrefix(flags[i], "--source=")
		case strings.HasPrefix(flags[i], "--status="):
			status = strings.TrimPrefix(flags[i], "--status=")
		case strings.HasPrefix(flags[i], "--duration="):
			fmt.Sscanf(strings.TrimPrefix(flags[i], "--duration="), "%d", &durationMs)
		case strings.HasPrefix(flags[i], "--error="):
			errorMsg = strings.TrimPrefix(flags[i], "--error=")
		case strings.HasPrefix(flags[i], "--role="):
			role = strings.TrimPrefix(flags[i], "--role=")
		}
	}

	skill.RecordSkillEventEx(cfg.HistoryDB, name, eventType, "", role, skill.SkillEventOpts{
		Status:     status,
		DurationMs: durationMs,
		Source:     source,
		ErrorMsg:   errorMsg,
	})
	fmt.Printf("Logged: %s %s (status=%s, source=%s)\n", name, eventType, status, source)
}

// skillStatsCmd shows per-skill usage statistics.
func skillStatsCmd(name string, days int) {
	cfg := LoadCLIConfig(FindConfigPath())
	if cfg.HistoryDB == "" {
		fmt.Fprintln(os.Stderr, "Error: no history database configured")
		os.Exit(1)
	}

	if name != "" {
		// Detailed view for a single skill.
		rows, err := skill.QuerySkillStats(cfg.HistoryDB, name, days)
		if err != nil || len(rows) == 0 {
			fmt.Printf("No data for skill %q in last %d days.\n", name, days)
			return
		}
		row := rows[0]
		fmt.Printf("Skill: %s (last %d days)\n", name, days)
		fmt.Printf("  Injected:  %v\n", row["injected"])
		fmt.Printf("  Invoked:   %v\n", row["invoked"])
		fmt.Printf("  Success:   %v\n", row["success"])
		fmt.Printf("  Fail:      %v\n", row["fail"])
		fmt.Printf("  Last used: %v\n", row["last_used"])

		// Recent history.
		history, err := skill.QuerySkillHistory(cfg.HistoryDB, name, 10)
		if err == nil && len(history) > 0 {
			fmt.Println("\nRecent events:")
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "  EVENT\tSTATUS\tSOURCE\tDURATION\tERROR\tTIME")
			for _, h := range history {
				dur := ""
				if d, ok := h["duration_ms"]; ok && fmt.Sprintf("%v", d) != "0" {
					dur = fmt.Sprintf("%vms", d)
				}
				errStr := fmt.Sprintf("%v", h["error_msg"])
				if len(errStr) > 40 {
					errStr = errStr[:40] + "..."
				}
				ts := fmt.Sprintf("%v", h["created_at"])
				if len(ts) > 16 {
					ts = ts[:16]
				}
				fmt.Fprintf(w, "  %v\t%v\t%v\t%s\t%s\t%s\n",
					h["event_type"], h["status"], h["source"], dur, errStr, ts)
			}
			w.Flush()
		}
		return
	}

	// Summary view for all skills.
	rows, err := skill.QuerySkillStats(cfg.HistoryDB, "", days)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if len(rows) == 0 {
		fmt.Printf("No skill usage data in last %d days.\n", days)
		return
	}

	fmt.Printf("Skill usage (last %d days):\n\n", days)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SKILL\tINJECTED\tINVOKED\tSUCCESS\tFAIL\tLAST USED")
	for _, row := range rows {
		lastUsed := fmt.Sprintf("%v", row["last_used"])
		if len(lastUsed) > 10 {
			lastUsed = lastUsed[:10]
		}
		fmt.Fprintf(w, "%v\t%v\t%v\t%v\t%v\t%s\n",
			row["skill_name"], row["injected"], row["invoked"],
			row["success"], row["fail"], lastUsed)
	}
	w.Flush()
}

// skillDiagnosticsCmd delegates to the internal skill package.
func skillDiagnosticsCmd(args []string) {
	cfg := LoadCLIConfig(FindConfigPath())
	skill.SkillDiagnosticsCmd(args, cfg.HistoryDB)
}
