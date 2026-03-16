package cli

import (
	"fmt"
	"os"
)

// CmdHooks handles the `tetora hooks` subcommand.
func CmdHooks(args []string) {
	if len(args) == 0 {
		printHooksUsage()
		return
	}

	cfg := LoadCLIConfig("")

	switch args[0] {
	case "install":
		// TODO: requires root function installHooks(cfg.ListenAddr)
		// Route through daemon API instead.
		api := cfg.NewAPIClient()
		resp, err := api.Post("/api/hooks/install", "")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			fmt.Fprintln(os.Stderr, "Is the daemon running? Start with: tetora start")
			os.Exit(1)
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			fmt.Fprintf(os.Stderr, "Error: daemon returned %d\n", resp.StatusCode)
			os.Exit(1)
		}
		fmt.Println("Hooks installed.")
		homeDir, _ := os.UserHomeDir()
		fmt.Printf("MCP bridge config: %s/.tetora/mcp/bridge.json\n", homeDir)

	case "remove", "uninstall":
		// TODO: requires root function removeHooks()
		// Route through daemon API instead.
		api := cfg.NewAPIClient()
		resp, err := api.Post("/api/hooks/remove", "")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			fmt.Fprintln(os.Stderr, "Is the daemon running? Start with: tetora start")
			os.Exit(1)
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			fmt.Fprintf(os.Stderr, "Error: daemon returned %d\n", resp.StatusCode)
			os.Exit(1)
		}
		fmt.Println("Hooks removed.")

	case "status":
		// TODO: requires root function showHooksStatus()
		// Route through daemon API instead.
		api := cfg.NewAPIClient()
		resp, err := api.Get("/api/hooks/install-status")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			fmt.Fprintln(os.Stderr, "Is the daemon running? Start with: tetora start")
			os.Exit(1)
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			fmt.Fprintf(os.Stderr, "Error: daemon returned %d\n", resp.StatusCode)
			os.Exit(1)
		}
		fmt.Println("Hook status retrieved from daemon.")

	default:
		fmt.Fprintf(os.Stderr, "Unknown hooks command: %s\n", args[0])
		printHooksUsage()
		os.Exit(1)
	}
}

func printHooksUsage() {
	fmt.Println(`Usage: tetora hooks <command>

Commands:
  install    Install Tetora hooks in Claude Code settings
  remove     Remove Tetora hooks from Claude Code settings
  status     Show current hook configuration`)
}
