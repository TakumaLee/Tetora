package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"
)

// MemoryEntry represents a key-value memory entry.
type MemoryEntry struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	Priority  string `json:"priority,omitempty"`
	UpdatedAt string `json:"updatedAt"`
}

func CmdMemory(args []string) {
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
func ParseRoleFlag(args []string) (string, []string) {
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
	role, _ := ParseRoleFlag(args)
	cfg := LoadCLIConfig(FindConfigPath())

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
	role, remaining := ParseRoleFlag(args)
	if len(remaining) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: tetora memory get <key> --agent AGENT")
		os.Exit(1)
	}
	if role == "" {
		fmt.Fprintln(os.Stderr, "Error: --agent is required")
		os.Exit(1)
	}
	key := remaining[0]
	cfg := LoadCLIConfig(FindConfigPath())

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
	role, remaining := ParseRoleFlag(args)
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
	cfg := LoadCLIConfig(FindConfigPath())

	if err := setMemory(cfg, role, key, value); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Set %s.%s\n", role, key)
}

func memoryDelete(args []string) {
	role, remaining := ParseRoleFlag(args)
	if len(remaining) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: tetora memory delete <key> --agent AGENT")
		os.Exit(1)
	}
	if role == "" {
		fmt.Fprintln(os.Stderr, "Error: --agent is required")
		os.Exit(1)
	}
	key := remaining[0]
	cfg := LoadCLIConfig(FindConfigPath())

	if err := deleteMemory(cfg, role, key); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Deleted %s.%s\n", role, key)
}

// --- Memory FS operations (replicated from root memory.go) ---

func sanitizeMemoryKey(key string) string {
	r := strings.NewReplacer("/", "_", "\\", "_", "..", "_", "\x00", "")
	return r.Replace(key)
}

func parseMemoryFrontmatter(data []byte) (priority string, body string) {
	s := string(data)
	if !strings.HasPrefix(s, "---\n") {
		return "P1", s
	}
	end := strings.Index(s[4:], "\n---\n")
	if end < 0 {
		return "P1", s
	}
	front := s[4 : 4+end]
	body = s[4+end+5:]
	priority = "P1"
	for _, line := range strings.Split(front, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "priority:") {
			val := strings.TrimSpace(strings.TrimPrefix(line, "priority:"))
			if val == "P0" || val == "P1" || val == "P2" {
				priority = val
			}
		}
	}
	return priority, body
}

func buildMemoryFrontmatter(priority, body string) string {
	if priority == "" || priority == "P1" {
		return body
	}
	return "---\npriority: " + priority + "\n---\n" + body
}

func getMemory(cfg *CLIConfig, role, key string) (string, error) {
	path := filepath.Join(cfg.WorkspaceDir, "memory", sanitizeMemoryKey(key)+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil
	}
	_, body := parseMemoryFrontmatter(data)
	return body, nil
}

func setMemory(cfg *CLIConfig, role, key, value string) error {
	dir := filepath.Join(cfg.WorkspaceDir, "memory")
	os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, sanitizeMemoryKey(key)+".md")

	pri := ""
	if existing, err := os.ReadFile(path); err == nil {
		pri, _ = parseMemoryFrontmatter(existing)
	}

	content := buildMemoryFrontmatter(pri, value)
	return os.WriteFile(path, []byte(content), 0o644)
}

func listMemory(cfg *CLIConfig, role string) ([]MemoryEntry, error) {
	dir := filepath.Join(cfg.WorkspaceDir, "memory")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var result []MemoryEntry
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		key := strings.TrimSuffix(e.Name(), ".md")
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		priority, body := parseMemoryFrontmatter(data)
		info, _ := e.Info()
		updatedAt := ""
		if info != nil {
			updatedAt = info.ModTime().Format(time.RFC3339)
		}
		result = append(result, MemoryEntry{
			Key:       key,
			Value:     body,
			Priority:  priority,
			UpdatedAt: updatedAt,
		})
	}
	return result, nil
}

func deleteMemory(cfg *CLIConfig, role, key string) error {
	path := filepath.Join(cfg.WorkspaceDir, "memory", sanitizeMemoryKey(key)+".md")
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
