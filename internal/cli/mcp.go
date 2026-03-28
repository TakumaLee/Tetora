package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
)

// MCPConfigInfo represents summary info about an MCP server config.
type MCPConfigInfo struct {
	Name    string          `json:"name"`
	Command string          `json:"command,omitempty"`
	Args    string          `json:"args,omitempty"`
	Config  json.RawMessage `json:"config"`
}

func CmdMCP(args []string) {
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
	cfg := LoadCLIConfig(FindConfigPath())
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
	cfg := LoadCLIConfig(FindConfigPath())
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
	configPath := FindConfigPath()
	cfg := LoadCLIConfig(configPath)

	var command string
	var cmdArgs []string
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
				cmdArgs = strings.Split(flags[i+1], ",")
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

	type serverEntry struct {
		Command string            `json:"command"`
		Args    []string          `json:"args,omitempty"`
		Env     map[string]string `json:"env,omitempty"`
	}
	entry := serverEntry{Command: command, Args: cmdArgs}
	if len(envMap) > 0 {
		entry.Env = envMap
	}
	wrapper := map[string]map[string]serverEntry{
		"mcpServers": {name: entry},
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

	mcpDir := filepath.Join(cfg.BaseDir, "mcp")
	os.MkdirAll(mcpDir, 0o755)
	os.WriteFile(filepath.Join(mcpDir, name+".json"), config, 0o600)

	fmt.Printf("MCP config %q added.\n", name)
}

func mcpRemoveCmd(name string) {
	configPath := FindConfigPath()
	cfg := LoadCLIConfig(configPath)

	if err := deleteMCPConfig(cfg, configPath, name); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("MCP config %q removed.\n", name)
}

func mcpTestCmd(name string) {
	cfg := LoadCLIConfig(FindConfigPath())
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

// --- MCP config operations (replicated from root mcp.go) ---

func listMCPConfigs(cfg *CLIConfig) []MCPConfigInfo {
	if len(cfg.MCPConfigs) == 0 {
		return nil
	}

	var configs []MCPConfigInfo
	for name, raw := range cfg.MCPConfigs {
		cmd, args := extractMCPSummary(raw)
		configs = append(configs, MCPConfigInfo{
			Name:    name,
			Command: cmd,
			Args:    args,
			Config:  raw,
		})
	}

	sort.Slice(configs, func(i, j int) bool {
		return configs[i].Name < configs[j].Name
	})
	return configs
}

func getMCPConfig(cfg *CLIConfig, name string) (json.RawMessage, error) {
	raw, ok := cfg.MCPConfigs[name]
	if !ok {
		return nil, fmt.Errorf("MCP config %q not found", name)
	}
	return raw, nil
}

func setMCPConfig(cfg *CLIConfig, configPath, name string, config json.RawMessage) error {
	if name == "" {
		return fmt.Errorf("MCP name is required")
	}
	for _, r := range name {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
			return fmt.Errorf("invalid character %q in MCP name (use a-z, 0-9, -, _)", string(r))
		}
	}
	if !json.Valid(config) {
		return fmt.Errorf("invalid JSON config")
	}
	return updateConfigMCPs(configPath, name, config)
}

func deleteMCPConfig(cfg *CLIConfig, configPath, name string) error {
	if _, ok := cfg.MCPConfigs[name]; !ok {
		return fmt.Errorf("MCP config %q not found", name)
	}

	if err := updateConfigMCPs(configPath, name, nil); err != nil {
		return err
	}

	filePath := filepath.Join(cfg.BaseDir, "mcp", name+".json")
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove mcp file %q: %w", filePath, err)
	}
	return nil
}

// updateConfigMCPs updates a single MCP config in config.json.
// If config is nil, the MCP entry is removed. Otherwise it is added/updated.
func updateConfigMCPs(configPath, mcpName string, config json.RawMessage) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	mcps := make(map[string]json.RawMessage)
	if mcpsRaw, ok := raw["mcpConfigs"]; ok {
		json.Unmarshal(mcpsRaw, &mcps) //nolint:errcheck
	}

	if config == nil {
		delete(mcps, mcpName)
	} else {
		mcps[mcpName] = config
	}

	mcpsJSON, err := json.Marshal(mcps)
	if err != nil {
		return err
	}
	raw["mcpConfigs"] = mcpsJSON

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, append(out, '\n'), 0o600)
}

func testMCPConfig(raw json.RawMessage) (bool, string) {
	cmd, args := extractMCPSummary(raw)
	if cmd == "" {
		return false, "could not extract command from config"
	}

	cmdPath, err := exec.LookPath(cmd)
	if err != nil {
		return false, fmt.Sprintf("command %q not found in PATH", cmd)
	}

	var cmdArgs []string
	if args != "" {
		cmdArgs = strings.Fields(args)
	}
	proc := exec.Command(cmdPath, cmdArgs...)
	proc.Env = os.Environ()

	if err := proc.Start(); err != nil {
		return false, fmt.Sprintf("failed to start: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- proc.Wait() }()

	select {
	case err := <-done:
		if err != nil {
			return false, fmt.Sprintf("process exited: %v", err)
		}
		return true, fmt.Sprintf("OK: %s (%s)", cmd, cmdPath)
	case <-time.After(2 * time.Second):
		proc.Process.Kill()
		return true, fmt.Sprintf("OK: %s started successfully (%s)", cmd, cmdPath)
	}
}

func extractMCPSummary(raw json.RawMessage) (command, args string) {
	var wrapper struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if json.Unmarshal(raw, &wrapper) == nil && len(wrapper.MCPServers) > 0 {
		for _, srv := range wrapper.MCPServers {
			return srv.Command, strings.Join(srv.Args, " ")
		}
	}

	var flat struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
	}
	if json.Unmarshal(raw, &flat) == nil && flat.Command != "" {
		return flat.Command, strings.Join(flat.Args, " ")
	}

	return "", ""
}
