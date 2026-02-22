package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
)

func cmdSkill(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: tetora skill <list|run|test> [name]")
		fmt.Println()
		fmt.Println("Commands:")
		fmt.Println("  list                                   List configured skills")
		fmt.Println("  run  <name> [--var key=value ...]      Execute a skill")
		fmt.Println("  test <name>                            Quick test (5s timeout)")
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
	default:
		fmt.Fprintf(os.Stderr, "Unknown skill action: %s\n", args[0])
		os.Exit(1)
	}
}

func skillListCmd() {
	cfg := loadConfig(findConfigPath())
	skills := listSkills(cfg)

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
	cfg := loadConfig(findConfigPath())
	skill := getSkill(cfg, name)
	if skill == nil {
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

	result, err := executeSkill(context.Background(), *skill, vars)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if skill.OutputAs == "json" {
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
	cfg := loadConfig(findConfigPath())
	skill := getSkill(cfg, name)
	if skill == nil {
		fmt.Fprintf(os.Stderr, "Error: skill %q not found\n", name)
		os.Exit(1)
	}

	fmt.Printf("Testing skill %q (%s)...\n", name, skill.Command)
	result, err := testSkill(context.Background(), *skill)
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
