package main

import (
	"fmt"
	"os"
	"strings"
)

func cmdCompletion(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: tetora completion <bash|zsh|fish>")
		fmt.Println()
		fmt.Println("Generate shell completion scripts.")
		fmt.Println()
		fmt.Println("Examples:")
		fmt.Println("  eval \"$(tetora completion bash)\"")
		fmt.Println("  tetora completion zsh > ~/.zsh/completions/_tetora")
		fmt.Println("  tetora completion fish > ~/.config/fish/completions/tetora.fish")
		return
	}

	switch args[0] {
	case "bash":
		fmt.Print(generateBashCompletion())
	case "zsh":
		fmt.Print(generateZshCompletion())
	case "fish":
		fmt.Print(generateFishCompletion())
	default:
		fmt.Fprintf(os.Stderr, "Unknown shell: %s (use bash, zsh, or fish)\n", args[0])
		os.Exit(1)
	}
}

// completionSubcommands returns all top-level tetora subcommands.
func completionSubcommands() []string {
	return []string{
		"serve", "run", "dispatch", "route", "init", "doctor",
		"status", "service", "job", "role", "history", "config",
		"logs", "prompt", "memory", "mcp", "session", "knowledge",
		"skill", "workflow", "budget", "trust", "webhook", "data", "backup", "restore",
		"proactive", "quick", "dashboard", "compact", "version", "help", "completion",
	}
}

// completionSubActions returns sub-actions for a given subcommand.
func completionSubActions(cmd string) []string {
	switch cmd {
	case "job":
		return []string{"list", "add", "enable", "disable", "remove", "trigger", "history"}
	case "role":
		return []string{"list", "add", "show", "remove"}
	case "history":
		return []string{"list", "show", "cost"}
	case "config":
		return []string{"show", "set", "validate", "migrate", "history", "rollback", "diff", "snapshot", "show-version", "versions"}
	case "prompt":
		return []string{"list", "show", "add", "edit", "remove"}
	case "memory":
		return []string{"list", "get", "set", "delete"}
	case "mcp":
		return []string{"list", "show", "add", "remove", "test"}
	case "session":
		return []string{"list", "show"}
	case "knowledge":
		return []string{"list", "add", "remove", "path", "search"}
	case "skill":
		return []string{"list", "run", "test"}
	case "workflow":
		return []string{"list", "show", "validate", "create", "delete", "run", "runs", "status", "messages", "history", "rollback", "diff"}
	case "budget":
		return []string{"show", "pause", "resume"}
	case "trust":
		return []string{"show", "set", "events"}
	case "webhook":
		return []string{"list", "show", "test"}
	case "proactive":
		return []string{"list", "trigger", "status"}
	case "quick":
		return []string{"list", "run", "search"}
	case "service":
		return []string{"install", "uninstall", "status"}
	case "data":
		return []string{"status", "cleanup", "export", "purge"}
	case "completion":
		return []string{"bash", "zsh", "fish"}
	}
	return nil
}

// completionSubcommandDescriptions returns subcommand to description mapping.
func completionSubcommandDescriptions() map[string]string {
	return map[string]string{
		"serve":      "Start daemon (Telegram + Slack + HTTP + Cron)",
		"run":        "Dispatch tasks (CLI mode)",
		"dispatch":   "Run an ad-hoc task via the daemon",
		"route":      "Smart dispatch (auto-route to best role)",
		"init":       "Interactive setup wizard",
		"doctor":     "Health checks and diagnostics",
		"status":     "Quick overview (daemon, jobs, cost)",
		"service":    "Manage launchd service",
		"job":        "Manage cron jobs",
		"role":       "Manage roles",
		"history":    "View execution history",
		"config":     "Manage config",
		"logs":       "View daemon logs",
		"prompt":     "Manage prompt templates",
		"memory":     "Manage agent memory",
		"mcp":        "Manage MCP configs",
		"session":    "View agent sessions",
		"knowledge":  "Manage knowledge base",
		"skill":      "Manage skills",
		"workflow":   "Manage workflows",
		"budget":     "Cost governance",
		"trust":      "Manage trust gradient per agent",
		"webhook":    "Manage incoming webhooks",
		"proactive":  "Manage proactive agent rules",
		"compact":    "Compact session messages",
		"data":       "Data retention & privacy management",
		"backup":     "Create backup of tetora data",
		"restore":    "Restore from a backup file",
		"dashboard":  "Open web dashboard in browser",
		"version":    "Show version",
		"help":       "Show help",
		"completion": "Generate shell completion scripts",
	}
}

// completionSubActionDescriptions returns sub-action descriptions for a given subcommand.
func completionSubActionDescriptions(cmd string) map[string]string {
	switch cmd {
	case "job":
		return map[string]string{
			"list": "List all cron jobs", "add": "Add a new cron job",
			"enable": "Enable a cron job", "disable": "Disable a cron job",
			"remove": "Remove a cron job", "trigger": "Manually trigger a cron job",
			"history": "Show job execution history",
		}
	case "role":
		return map[string]string{
			"list": "List all roles", "add": "Add a new role",
			"show": "Show role details", "remove": "Remove a role",
		}
	case "history":
		return map[string]string{
			"list": "Show recent execution history", "show": "Show execution details",
			"cost": "Show cost summary",
		}
	case "config":
		return map[string]string{
			"show": "Show current config", "set": "Set a config value",
			"validate": "Validate config file", "migrate": "Run config migration",
			"history": "Show config version history", "rollback": "Restore to a previous version",
			"diff": "Compare two versions", "snapshot": "Create a manual snapshot",
			"show-version": "Show full content of a version", "versions": "List all versioned entities",
		}
	case "prompt":
		return map[string]string{
			"list": "List prompt templates", "show": "Show a prompt template",
			"add": "Add a prompt template", "edit": "Edit a prompt template",
			"remove": "Remove a prompt template",
		}
	case "memory":
		return map[string]string{
			"list": "List agent memory entries", "get": "Get a memory entry",
			"set": "Set a memory entry", "delete": "Delete a memory entry",
		}
	case "mcp":
		return map[string]string{
			"list": "List MCP configs", "show": "Show MCP config details",
			"add": "Add an MCP config", "remove": "Remove an MCP config",
			"test": "Test an MCP connection",
		}
	case "session":
		return map[string]string{
			"list": "List recent sessions", "show": "Show session conversation",
		}
	case "knowledge":
		return map[string]string{
			"list": "List knowledge base files", "add": "Add file to knowledge base",
			"remove": "Remove file from knowledge base", "path": "Show knowledge base path",
			"search": "Search knowledge base",
		}
	case "skill":
		return map[string]string{
			"list": "List available skills", "run": "Run a skill",
			"test": "Test a skill",
		}
	case "workflow":
		return map[string]string{
			"list": "List all workflows", "show": "Show workflow definition",
			"validate": "Validate a workflow", "create": "Import workflow from JSON",
			"delete": "Delete a workflow", "run": "Execute a workflow",
			"runs": "List workflow run history", "status": "Show run status",
			"messages": "Show agent messages for a run",
			"history": "Show workflow version history", "rollback": "Restore to a previous version",
			"diff": "Compare two versions",
		}
	case "budget":
		return map[string]string{
			"show": "Show budget status", "pause": "Pause all spending",
			"resume": "Resume spending",
		}
	case "trust":
		return map[string]string{
			"show":   "Show trust levels for all agents",
			"set":    "Set trust level for a role",
			"events": "Show trust event history",
		}
	case "webhook":
		return map[string]string{
			"list": "List incoming webhooks",
			"show": "Show webhook details",
			"test": "Send a test event to a webhook",
		}
	case "data":
		return map[string]string{
			"status": "Show retention config and row counts", "cleanup": "Run retention cleanup",
			"export": "Export all user data (GDPR)", "purge": "Delete data before a date",
		}
	case "service":
		return map[string]string{
			"install": "Install as launchd service", "uninstall": "Uninstall launchd service",
			"status": "Show service status",
		}
	case "completion":
		return map[string]string{
			"bash": "Generate bash completion", "zsh": "Generate zsh completion",
			"fish": "Generate fish completion",
		}
	}
	return nil
}

func generateBashCompletion() string {
	var b strings.Builder

	b.WriteString(`#!/bin/bash
# tetora bash completion — generated by tetora completion bash

_tetora_completions() {
    local cur prev words cword
    _init_completion || return

    local commands="`)
	b.WriteString(strings.Join(completionSubcommands(), " "))
	b.WriteString(`"

    # Complete top-level subcommands.
    if [[ ${cword} -eq 1 ]]; then
        COMPREPLY=($(compgen -W "${commands}" -- "${cur}"))
        return
    fi

    # Complete sub-actions for each subcommand.
    case "${words[1]}" in
`)

	// Emit a case branch for each subcommand that has sub-actions.
	for _, cmd := range completionSubcommands() {
		actions := completionSubActions(cmd)
		if len(actions) == 0 {
			continue
		}
		b.WriteString("        ")
		b.WriteString(cmd)
		b.WriteString(")\n")
		b.WriteString("            if [[ ${cword} -eq 2 ]]; then\n")
		b.WriteString("                COMPREPLY=($(compgen -W \"")
		b.WriteString(strings.Join(actions, " "))
		b.WriteString("\" -- \"${cur}\"))\n")
		b.WriteString("            fi\n")

		// Add dynamic completions for specific commands at position 3.
		switch cmd {
		case "role":
			b.WriteString("            if [[ ${cword} -eq 3 && (\"${words[2]}\" == \"show\" || \"${words[2]}\" == \"remove\") ]]; then\n")
			b.WriteString("                local roles\n")
			b.WriteString("                roles=$(tetora role list --names 2>/dev/null)\n")
			b.WriteString("                COMPREPLY=($(compgen -W \"${roles}\" -- \"${cur}\"))\n")
			b.WriteString("            fi\n")
		case "workflow":
			b.WriteString("            if [[ ${cword} -eq 3 && (\"${words[2]}\" == \"show\" || \"${words[2]}\" == \"run\" || \"${words[2]}\" == \"validate\" || \"${words[2]}\" == \"delete\") ]]; then\n")
			b.WriteString("                local workflows\n")
			b.WriteString("                workflows=$(tetora workflow list --names 2>/dev/null)\n")
			b.WriteString("                COMPREPLY=($(compgen -W \"${workflows}\" -- \"${cur}\"))\n")
			b.WriteString("            fi\n")
		case "job":
			b.WriteString("            if [[ ${cword} -eq 3 && (\"${words[2]}\" == \"enable\" || \"${words[2]}\" == \"disable\" || \"${words[2]}\" == \"remove\" || \"${words[2]}\" == \"trigger\") ]]; then\n")
			b.WriteString("                local jobs\n")
			b.WriteString("                jobs=$(tetora job list --names 2>/dev/null)\n")
			b.WriteString("                COMPREPLY=($(compgen -W \"${jobs}\" -- \"${cur}\"))\n")
			b.WriteString("            fi\n")
		case "skill":
			b.WriteString("            if [[ ${cword} -eq 3 && (\"${words[2]}\" == \"run\" || \"${words[2]}\" == \"test\") ]]; then\n")
			b.WriteString("                local skills\n")
			b.WriteString("                skills=$(tetora skill list --names 2>/dev/null)\n")
			b.WriteString("                COMPREPLY=($(compgen -W \"${skills}\" -- \"${cur}\"))\n")
			b.WriteString("            fi\n")
		case "prompt":
			b.WriteString("            if [[ ${cword} -eq 3 && (\"${words[2]}\" == \"show\" || \"${words[2]}\" == \"edit\" || \"${words[2]}\" == \"remove\") ]]; then\n")
			b.WriteString("                local prompts\n")
			b.WriteString("                prompts=$(tetora prompt list --names 2>/dev/null)\n")
			b.WriteString("                COMPREPLY=($(compgen -W \"${prompts}\" -- \"${cur}\"))\n")
			b.WriteString("            fi\n")
		case "mcp":
			b.WriteString("            if [[ ${cword} -eq 3 && (\"${words[2]}\" == \"show\" || \"${words[2]}\" == \"remove\" || \"${words[2]}\" == \"test\") ]]; then\n")
			b.WriteString("                local mcps\n")
			b.WriteString("                mcps=$(tetora mcp list --names 2>/dev/null)\n")
			b.WriteString("                COMPREPLY=($(compgen -W \"${mcps}\" -- \"${cur}\"))\n")
			b.WriteString("            fi\n")
		}

		b.WriteString("            ;;\n")
	}

	b.WriteString(`    esac
}

complete -F _tetora_completions tetora
`)

	return b.String()
}

func generateZshCompletion() string {
	var b strings.Builder

	descs := completionSubcommandDescriptions()

	b.WriteString(`#compdef tetora
# tetora zsh completion — generated by tetora completion zsh

_tetora() {
    local -a commands
    commands=(
`)

	for _, cmd := range completionSubcommands() {
		desc := descs[cmd]
		// Escape colons in descriptions for zsh _describe format.
		desc = strings.ReplaceAll(desc, ":", "\\:")
		b.WriteString(fmt.Sprintf("        '%s:%s'\n", cmd, desc))
	}

	b.WriteString(`    )

    _arguments -C \
        '1:command:->command' \
        '*::arg:->args'

    case $state in
    command)
        _describe -t commands 'tetora command' commands
        ;;
    args)
        case ${words[1]} in
`)

	for _, cmd := range completionSubcommands() {
		actions := completionSubActions(cmd)
		if len(actions) == 0 {
			continue
		}
		actionDescs := completionSubActionDescriptions(cmd)

		b.WriteString("        ")
		b.WriteString(cmd)
		b.WriteString(")\n")
		b.WriteString("            local -a subactions\n")
		b.WriteString("            subactions=(\n")
		for _, action := range actions {
			desc := actionDescs[action]
			desc = strings.ReplaceAll(desc, ":", "\\:")
			b.WriteString(fmt.Sprintf("                '%s:%s'\n", action, desc))
		}
		b.WriteString("            )\n")

		// Add dynamic completions for specific subcommands.
		switch cmd {
		case "role":
			b.WriteString("            if (( CURRENT == 3 )) && [[ ${words[2]} == (show|remove) ]]; then\n")
			b.WriteString("                local -a roles\n")
			b.WriteString("                roles=(${(f)\"$(tetora role list --names 2>/dev/null)\"})\n")
			b.WriteString("                _describe -t roles 'role name' roles && return\n")
			b.WriteString("            fi\n")
		case "workflow":
			b.WriteString("            if (( CURRENT == 3 )) && [[ ${words[2]} == (show|run|validate|delete) ]]; then\n")
			b.WriteString("                local -a workflows\n")
			b.WriteString("                workflows=(${(f)\"$(tetora workflow list --names 2>/dev/null)\"})\n")
			b.WriteString("                _describe -t workflows 'workflow name' workflows && return\n")
			b.WriteString("            fi\n")
		case "job":
			b.WriteString("            if (( CURRENT == 3 )) && [[ ${words[2]} == (enable|disable|remove|trigger) ]]; then\n")
			b.WriteString("                local -a jobs\n")
			b.WriteString("                jobs=(${(f)\"$(tetora job list --names 2>/dev/null)\"})\n")
			b.WriteString("                _describe -t jobs 'job name' jobs && return\n")
			b.WriteString("            fi\n")
		}

		b.WriteString("            _describe -t subactions '")
		b.WriteString(cmd)
		b.WriteString(" action' subactions\n")
		b.WriteString("            ;;\n")
	}

	b.WriteString(`        esac
        ;;
    esac
}

_tetora "$@"
`)

	return b.String()
}

func generateFishCompletion() string {
	var b strings.Builder

	descs := completionSubcommandDescriptions()

	b.WriteString("# tetora fish completion — generated by tetora completion fish\n\n")

	// Disable file completions by default.
	b.WriteString("complete -c tetora -f\n\n")

	// Top-level subcommands: only complete when no subcommand given yet.
	b.WriteString("# Top-level subcommands\n")
	for _, cmd := range completionSubcommands() {
		desc := descs[cmd]
		b.WriteString(fmt.Sprintf("complete -c tetora -n '__fish_use_subcommand' -a %s -d '%s'\n", cmd, desc))
	}

	b.WriteString("\n# Sub-actions\n")

	// Sub-actions for each subcommand.
	for _, cmd := range completionSubcommands() {
		actions := completionSubActions(cmd)
		if len(actions) == 0 {
			continue
		}
		actionDescs := completionSubActionDescriptions(cmd)
		for _, action := range actions {
			desc := actionDescs[action]
			b.WriteString(fmt.Sprintf("complete -c tetora -n '__fish_seen_subcommand_from %s' -a %s -d '%s'\n",
				cmd, action, desc))
		}
	}

	// Dynamic completions for specific commands.
	b.WriteString("\n# Dynamic completions\n")
	b.WriteString("complete -c tetora -n '__fish_seen_subcommand_from role; and __fish_seen_subcommand_from show remove' -a '(tetora role list --names 2>/dev/null)' -d 'Role name'\n")
	b.WriteString("complete -c tetora -n '__fish_seen_subcommand_from workflow; and __fish_seen_subcommand_from show run validate delete' -a '(tetora workflow list --names 2>/dev/null)' -d 'Workflow name'\n")
	b.WriteString("complete -c tetora -n '__fish_seen_subcommand_from job; and __fish_seen_subcommand_from enable disable remove trigger' -a '(tetora job list --names 2>/dev/null)' -d 'Job name'\n")

	return b.String()
}
