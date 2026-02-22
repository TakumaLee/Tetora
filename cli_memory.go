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
		fmt.Println("  list   [--role ROLE]              List memory entries")
		fmt.Println("  get    <key> --role ROLE           Get a memory value")
		fmt.Println("  set    <key> <value> --role ROLE   Set a memory value")
		fmt.Println("  delete <key> --role ROLE           Delete a memory entry")
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

// parseRoleFlag extracts --role value from args and returns remaining args.
func parseRoleFlag(args []string) (string, []string) {
	role := ""
	var remaining []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--role" && i+1 < len(args) {
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
	if cfg.HistoryDB == "" {
		fmt.Fprintln(os.Stderr, "Error: historyDB not configured")
		os.Exit(1)
	}

	if err := initMemoryDB(cfg.HistoryDB); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	entries, err := listMemory(cfg.HistoryDB, role)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(entries) == 0 {
		if role != "" {
			fmt.Printf("No memory entries for role %q.\n", role)
		} else {
			fmt.Println("No memory entries.")
		}
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ROLE\tKEY\tVALUE\tUPDATED")
	for _, e := range entries {
		val := e.Value
		if len(val) > 60 {
			val = val[:60] + "..."
		}
		val = strings.ReplaceAll(val, "\n", " ")
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", e.Role, e.Key, val, e.UpdatedAt)
	}
	w.Flush()
}

func memoryGet(args []string) {
	role, remaining := parseRoleFlag(args)
	if len(remaining) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: tetora memory get <key> --role ROLE")
		os.Exit(1)
	}
	if role == "" {
		fmt.Fprintln(os.Stderr, "Error: --role is required")
		os.Exit(1)
	}
	key := remaining[0]

	cfg := loadConfig(findConfigPath())
	if cfg.HistoryDB == "" {
		fmt.Fprintln(os.Stderr, "Error: historyDB not configured")
		os.Exit(1)
	}

	if err := initMemoryDB(cfg.HistoryDB); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	val, err := getMemory(cfg.HistoryDB, role, key)
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
		fmt.Fprintln(os.Stderr, "Usage: tetora memory set <key> <value> --role ROLE")
		os.Exit(1)
	}
	if role == "" {
		fmt.Fprintln(os.Stderr, "Error: --role is required")
		os.Exit(1)
	}
	key := remaining[0]
	value := strings.Join(remaining[1:], " ")

	cfg := loadConfig(findConfigPath())
	if cfg.HistoryDB == "" {
		fmt.Fprintln(os.Stderr, "Error: historyDB not configured")
		os.Exit(1)
	}

	if err := initMemoryDB(cfg.HistoryDB); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if err := setMemory(cfg.HistoryDB, role, key, value); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Set %s.%s\n", role, key)
}

func memoryDelete(args []string) {
	role, remaining := parseRoleFlag(args)
	if len(remaining) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: tetora memory delete <key> --role ROLE")
		os.Exit(1)
	}
	if role == "" {
		fmt.Fprintln(os.Stderr, "Error: --role is required")
		os.Exit(1)
	}
	key := remaining[0]

	cfg := loadConfig(findConfigPath())
	if cfg.HistoryDB == "" {
		fmt.Fprintln(os.Stderr, "Error: historyDB not configured")
		os.Exit(1)
	}

	if err := initMemoryDB(cfg.HistoryDB); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if err := deleteMemory(cfg.HistoryDB, role, key); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Deleted %s.%s\n", role, key)
}
