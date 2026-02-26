package main

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
)

func cmdMemory(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: tetora memory <list|get|set|delete> [options]")
		fmt.Println()
		fmt.Println("Commands:")
		fmt.Println("  list   [--agent AGENT]              List memory entries")
		fmt.Println("  get    <key> --agent AGENT          Get a memory value")
		fmt.Println("  set    <key> <value> --agent AGENT  Set a memory value")
		fmt.Println("  delete <key> --agent AGENT          Delete a memory entry")
		return
	}
	switch args[0] {
	case "list", "ls":
		memoryList(args[1:])
	case "get":
		memoryGet(args[1:])
	case "set":
		memorySet(args[1:])
	case "delete", "rm":
		memoryDelete(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown memory action: %s\n", args[0])
		os.Exit(1)
	}
}

// parseRoleFlag extracts --agent (or legacy --role) value from args and returns remaining args.
func parseRoleFlag(args []string) (string, []string) {
	role := ""
	var remaining []string
	for i := 0; i < len(args); i++ {
		if (args[i] == "--agent" || args[i] == "--role") && i+1 < len(args) {
			role = args[i+1]
			i++
		} else {
			remaining = append(remaining, args[i])
		}
	}
	return role, remaining
}

func memoryList(args []string) {
	role, _ := parseRoleFlag(args)

	cfg := loadConfig(findConfigPath())

	entries, err := listMemory(cfg, role)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(entries) == 0 {
		if role != "" {
			fmt.Printf("No memory entries for agent %q.\n", role)
		} else {
			fmt.Println("No memory entries.")
		}
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "KEY\tVALUE\tUPDATED")
	for _, e := range entries {
		val := e.Value
		if len(val) > 60 {
			val = val[:60] + "..."
		}
		val = strings.ReplaceAll(val, "\n", " ")
		fmt.Fprintf(w, "%s\t%s\t%s\n", e.Key, val, e.UpdatedAt)
	}
	w.Flush()
}

func memoryGet(args []string) {
	role, remaining := parseRoleFlag(args)
	if len(remaining) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: tetora memory get <key> --agent AGENT")
		os.Exit(1)
	}
	if role == "" {
		fmt.Fprintln(os.Stderr, "Error: --agent is required")
		os.Exit(1)
	}
	key := remaining[0]

	cfg := loadConfig(findConfigPath())

	val, err := getMemory(cfg, role, key)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if val == "" {
		fmt.Fprintf(os.Stderr, "No value for %s.%s\n", role, key)
		os.Exit(1)
	}
	fmt.Println(val)
}

func memorySet(args []string) {
	role, remaining := parseRoleFlag(args)
	if len(remaining) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: tetora memory set <key> <value> --agent AGENT")
		os.Exit(1)
	}
	if role == "" {
		fmt.Fprintln(os.Stderr, "Error: --agent is required")
		os.Exit(1)
	}
	key := remaining[0]
	value := strings.Join(remaining[1:], " ")

	cfg := loadConfig(findConfigPath())

	if err := setMemory(cfg, role, key, value); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Set %s.%s\n", role, key)
}

func memoryDelete(args []string) {
	role, remaining := parseRoleFlag(args)
	if len(remaining) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: tetora memory delete <key> --agent AGENT")
		os.Exit(1)
	}
	if role == "" {
		fmt.Fprintln(os.Stderr, "Error: --agent is required")
		os.Exit(1)
	}
	key := remaining[0]

	cfg := loadConfig(findConfigPath())

	if err := deleteMemory(cfg, role, key); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Deleted %s.%s\n", role, key)
}
