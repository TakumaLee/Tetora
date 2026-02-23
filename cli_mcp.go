package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
)

func cmdMCP(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: tetora mcp <list|show|add|remove|test> [name]")
		fmt.Println()
		fmt.Println("Commands:")
		fmt.Println("  list                                   List MCP server configs")
		fmt.Println("  show   <name>                          Show full config JSON")
		fmt.Println("  add    <name> --command CMD [--args ..]  Add MCP server")
		fmt.Println("  remove <name>                          Remove MCP server config")
		fmt.Println("  test   <name>                          Test MCP server connection")
		return
	}
	switch args[0] {
	case "list", "ls":
		mcpListCmd()
	case "show":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: tetora mcp show <name>")
			os.Exit(1)
		}
		mcpShowCmd(args[1])
	case "add":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: tetora mcp add <name> --command CMD [--args A1,A2] [--env K=V,K2=V2]")
			os.Exit(1)
		}
		mcpAddCmd(args[1], args[2:])
	case "remove", "rm":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: tetora mcp remove <name>")
			os.Exit(1)
		}
		mcpRemoveCmd(args[1])
	case "test":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: tetora mcp test <name>")
			os.Exit(1)
		}
		mcpTestCmd(args[1])
	default:
		fmt.Fprintf(os.Stderr, "Unknown mcp action: %s\n", args[0])
		os.Exit(1)
	}
}

func mcpListCmd() {
	cfg := loadConfig(findConfigPath())
	configs := listMCPConfigs(cfg)

	if len(configs) == 0 {
		fmt.Println("No MCP server configs.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tCOMMAND\tARGS")
	for _, c := range configs {
		args := c.Args
		if len(args) > 60 {
			args = args[:60] + "..."
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", c.Name, c.Command, args)
	}
	w.Flush()
}

func mcpShowCmd(name string) {
	cfg := loadConfig(findConfigPath())
	raw, err := getMCPConfig(cfg, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	var pretty json.RawMessage
	if json.Unmarshal(raw, &pretty) == nil {
		out, _ := json.MarshalIndent(pretty, "", "  ")
		fmt.Println(string(out))
	} else {
		fmt.Println(string(raw))
	}
}

func mcpAddCmd(name string, flags []string) {
	cfg := loadConfig(findConfigPath())
	configPath := findConfigPath()

	// Parse flags.
	var command string
	var args []string
	envMap := make(map[string]string)

	for i := 0; i < len(flags); i++ {
		switch flags[i] {
		case "--command":
			if i+1 < len(flags) {
				command = flags[i+1]
				i++
			}
		case "--args":
			if i+1 < len(flags) {
				args = strings.Split(flags[i+1], ",")
				i++
			}
		case "--env":
			if i+1 < len(flags) {
				for _, kv := range strings.Split(flags[i+1], ",") {
					parts := strings.SplitN(kv, "=", 2)
					if len(parts) == 2 {
						envMap[parts[0]] = parts[1]
					}
				}
				i++
			}
		}
	}

	if command == "" {
		fmt.Fprintln(os.Stderr, "Error: --command is required")
		os.Exit(1)
	}

	// Build mcpServers wrapper.
	type serverEntry struct {
		Command string            `json:"command"`
		Args    []string          `json:"args,omitempty"`
		Env     map[string]string `json:"env,omitempty"`
	}
	wrapper := map[string]map[string]serverEntry{
		"mcpServers": {
			name: {Command: command, Args: args, Env: envMap},
		},
	}

	// Clean up empty env.
	if len(envMap) == 0 {
		wrapper["mcpServers"][name] = serverEntry{Command: command, Args: args}
	}

	config, err := json.MarshalIndent(wrapper, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if err := setMCPConfig(cfg, configPath, name, config); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Ensure mcp dir exists and write file.
	mcpDir := filepath.Join(cfg.baseDir, "mcp")
	os.MkdirAll(mcpDir, 0o755)
	os.WriteFile(filepath.Join(mcpDir, name+".json"), config, 0o644)

	fmt.Printf("MCP config %q added.\n", name)
}

func mcpRemoveCmd(name string) {
	cfg := loadConfig(findConfigPath())
	configPath := findConfigPath()

	if err := deleteMCPConfig(cfg, configPath, name); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("MCP config %q removed.\n", name)
}

func mcpTestCmd(name string) {
	cfg := loadConfig(findConfigPath())
	raw, err := getMCPConfig(cfg, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	ok, output := testMCPConfig(raw)
	if ok {
		fmt.Println(output)
	} else {
		fmt.Fprintf(os.Stderr, "FAIL: %s\n", output)
		os.Exit(1)
	}
}
