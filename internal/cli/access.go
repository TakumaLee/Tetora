package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// CmdAccess implements `tetora access <list|add|remove> [path]`.
func CmdAccess(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: tetora access <list|add|remove> [path]")
		fmt.Println()
		fmt.Println("Manage directories that agents can access (defaultAddDirs).")
		fmt.Println("The tetora data directory (~/.tetora/) is always included.")
		fmt.Println()
		fmt.Println("Commands:")
		fmt.Println("  list              Show accessible directories")
		fmt.Println("  add <path>        Grant agent access to a directory")
		fmt.Println("  remove <path>     Revoke agent access to a directory")
		fmt.Println()
		fmt.Println("Examples:")
		fmt.Println("  tetora access list")
		fmt.Println("  tetora access add ~                   Grant access to home directory")
		fmt.Println("  tetora access add ~/Development       Grant access to Development folder")
		fmt.Println("  tetora access remove ~/Development    Revoke access")
		return
	}

	switch args[0] {
	case "list", "ls":
		accessList()
	case "add":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: tetora access add <path>")
			os.Exit(1)
		}
		accessAdd(args[1])
	case "remove", "rm":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: tetora access remove <path>")
			os.Exit(1)
		}
		accessRemove(args[1])
	default:
		fmt.Fprintf(os.Stderr, "Unknown access action: %s\n", args[0])
		os.Exit(1)
	}
}

func accessList() {
	cfg := LoadCLIConfig(FindConfigPath())

	fmt.Println("Agent accessible directories:")
	fmt.Println()
	fmt.Printf("  ~/.tetora/  (always included)\n")
	if len(cfg.DefaultAddDirs) == 0 {
		fmt.Println()
		fmt.Println("No additional directories configured.")
		fmt.Println("Add with: tetora access add <path>")
		return
	}
	for _, d := range cfg.DefaultAddDirs {
		fmt.Printf("  %s\n", d)
	}
	fmt.Printf("\n%d additional directories configured.\n", len(cfg.DefaultAddDirs))
}

func accessAdd(path string) {
	cfg := LoadCLIConfig(FindConfigPath())
	configPath := FindConfigPath()

	path = strings.TrimRight(path, "/")

	for _, d := range cfg.DefaultAddDirs {
		if d == path {
			fmt.Printf("Directory %q already in access list.\n", path)
			return
		}
	}

	err := UpdateConfigField(configPath, "defaultAddDirs", appendDirJSON(cfg.DefaultAddDirs, path))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Added %q to agent access list.\n", path)
	fmt.Println("Takes effect on the next task (restart not required).")
}

func accessRemove(path string) {
	cfg := LoadCLIConfig(FindConfigPath())
	configPath := FindConfigPath()

	path = strings.TrimRight(path, "/")

	found := false
	for _, d := range cfg.DefaultAddDirs {
		if d == path {
			found = true
			break
		}
	}
	if !found {
		fmt.Fprintf(os.Stderr, "Directory %q not in access list.\n", path)
		os.Exit(1)
	}

	var filtered []string
	for _, d := range cfg.DefaultAddDirs {
		if d != path {
			filtered = append(filtered, d)
		}
	}

	var fieldValue json.RawMessage
	if len(filtered) == 0 {
		fieldValue = json.RawMessage("null")
	} else {
		b, _ := json.Marshal(filtered)
		fieldValue = b
	}

	if len(filtered) == 0 {
		// Remove the key entirely by writing null, then use the mutate-based path.
		if err := removeConfigKey(configPath, "defaultAddDirs"); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	} else {
		if err := UpdateConfigField(configPath, "defaultAddDirs", fieldValue); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}
	fmt.Printf("Removed %q from agent access list.\n", path)
}

// appendDirJSON builds a json.RawMessage for a string slice with an appended value.
func appendDirJSON(existing []string, add string) json.RawMessage {
	updated := append(existing, add)
	b, _ := json.Marshal(updated)
	return b
}

// removeConfigKey deletes a top-level key from config.json.
func removeConfigKey(configPath, key string) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}
	delete(raw, key)
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, append(out, '\n'), 0o600)
}
