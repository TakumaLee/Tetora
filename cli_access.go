package main

import (
	"fmt"
	"os"
	"strings"
)

func cmdAccess(args []string) {
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
	cfg := loadConfig(findConfigPath())

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
	cfg := loadConfig(findConfigPath())
	configPath := findConfigPath()

	// Normalize: trim trailing slash.
	path = strings.TrimRight(path, "/")

	// Check for duplicates.
	for _, d := range cfg.DefaultAddDirs {
		if d == path {
			fmt.Printf("Directory %q already in access list.\n", path)
			return
		}
	}

	err := updateConfigField(configPath, func(raw map[string]any) {
		existing, _ := raw["defaultAddDirs"].([]any)
		existing = append(existing, path)
		raw["defaultAddDirs"] = existing
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Added %q to agent access list.\n", path)
	fmt.Println("Takes effect on the next task (restart not required).")
}

func accessRemove(path string) {
	cfg := loadConfig(findConfigPath())
	configPath := findConfigPath()

	path = strings.TrimRight(path, "/")

	// Find and remove.
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

	err := updateConfigField(configPath, func(raw map[string]any) {
		existing, _ := raw["defaultAddDirs"].([]any)
		var filtered []any
		for _, d := range existing {
			if s, ok := d.(string); ok && s != path {
				filtered = append(filtered, d)
			}
		}
		if len(filtered) == 0 {
			delete(raw, "defaultAddDirs")
		} else {
			raw["defaultAddDirs"] = filtered
		}
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Removed %q from agent access list.\n", path)
}
