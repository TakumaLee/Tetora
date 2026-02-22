package main

import (
	"strings"
	"testing"
)

func TestCompletionSubcommands(t *testing.T) {
	cmds := completionSubcommands()

	expected := []string{
		"serve", "run", "dispatch", "route", "init", "doctor",
		"status", "service", "job", "role", "history", "config",
		"logs", "prompt", "memory", "mcp", "session", "knowledge",
		"skill", "workflow", "budget", "trust", "webhook", "data", "backup", "restore",
		"dashboard", "version", "help", "completion",
	}

	if len(cmds) != len(expected) {
		t.Fatalf("completionSubcommands() returned %d items, want %d", len(cmds), len(expected))
	}

	set := make(map[string]bool)
	for _, c := range cmds {
		set[c] = true
	}

	for _, e := range expected {
		if !set[e] {
			t.Errorf("completionSubcommands() missing %q", e)
		}
	}
}

func TestCompletionSubActions(t *testing.T) {
	tests := []struct {
		cmd      string
		expected []string
	}{
		{"job", []string{"list", "add", "enable", "disable", "remove", "trigger", "history"}},
		{"role", []string{"list", "add", "show", "remove"}},
		{"workflow", []string{"list", "show", "validate", "create", "delete", "run", "runs", "status", "messages", "history", "rollback", "diff"}},
		{"knowledge", []string{"list", "add", "remove", "path", "search"}},
		{"history", []string{"list", "show", "cost"}},
		{"config", []string{"show", "set", "validate", "migrate", "history", "rollback", "diff", "snapshot", "show-version", "versions"}},
		{"data", []string{"status", "cleanup", "export", "purge"}},
		{"prompt", []string{"list", "show", "add", "edit", "remove"}},
		{"memory", []string{"list", "get", "set", "delete"}},
		{"mcp", []string{"list", "show", "add", "remove", "test"}},
		{"session", []string{"list", "show"}},
		{"skill", []string{"list", "run", "test"}},
		{"budget", []string{"show", "pause", "resume"}},
		{"webhook", []string{"list", "show", "test"}},
		{"service", []string{"install", "uninstall", "status"}},
		{"completion", []string{"bash", "zsh", "fish"}},
	}

	for _, tt := range tests {
		actions := completionSubActions(tt.cmd)
		if len(actions) != len(tt.expected) {
			t.Errorf("completionSubActions(%q) returned %d items, want %d: %v", tt.cmd, len(actions), len(tt.expected), actions)
			continue
		}
		for i, a := range actions {
			if a != tt.expected[i] {
				t.Errorf("completionSubActions(%q)[%d] = %q, want %q", tt.cmd, i, a, tt.expected[i])
			}
		}
	}

	// Commands without sub-actions should return nil.
	nilCmds := []string{"serve", "run", "dispatch", "init", "doctor", "dashboard", "version", "help", "nonexistent"}
	for _, cmd := range nilCmds {
		if actions := completionSubActions(cmd); actions != nil {
			t.Errorf("completionSubActions(%q) = %v, want nil", cmd, actions)
		}
	}
}

func TestGenerateBashCompletion(t *testing.T) {
	output := generateBashCompletion()

	// Must contain the function name.
	if !strings.Contains(output, "_tetora_completions") {
		t.Error("bash completion missing _tetora_completions function")
	}

	// Must register the completion function.
	if !strings.Contains(output, "complete -F _tetora_completions tetora") {
		t.Error("bash completion missing 'complete -F' registration")
	}

	// Must contain all top-level subcommands.
	for _, cmd := range completionSubcommands() {
		if !strings.Contains(output, cmd) {
			t.Errorf("bash completion missing subcommand %q", cmd)
		}
	}

	// Must contain sub-action words for key commands.
	for _, cmd := range []string{"job", "role", "workflow", "config"} {
		for _, action := range completionSubActions(cmd) {
			if !strings.Contains(output, action) {
				t.Errorf("bash completion missing sub-action %q for %q", action, cmd)
			}
		}
	}

	// Must contain dynamic completion hints.
	if !strings.Contains(output, "tetora role list --names") {
		t.Error("bash completion missing dynamic role completion")
	}
	if !strings.Contains(output, "tetora workflow list --names") {
		t.Error("bash completion missing dynamic workflow completion")
	}
}

func TestGenerateZshCompletion(t *testing.T) {
	output := generateZshCompletion()

	// Must contain the compdef directive.
	if !strings.Contains(output, "#compdef tetora") {
		t.Error("zsh completion missing #compdef tetora")
	}

	// Must contain the function name.
	if !strings.Contains(output, "_tetora") {
		t.Error("zsh completion missing _tetora function")
	}

	// Must contain _arguments for argument handling.
	if !strings.Contains(output, "_arguments") {
		t.Error("zsh completion missing _arguments")
	}

	// Must contain _describe for sub-action descriptions.
	if !strings.Contains(output, "_describe") {
		t.Error("zsh completion missing _describe")
	}

	// Must contain all subcommands with descriptions.
	descs := completionSubcommandDescriptions()
	for _, cmd := range completionSubcommands() {
		if !strings.Contains(output, cmd) {
			t.Errorf("zsh completion missing subcommand %q", cmd)
		}
		if desc, ok := descs[cmd]; ok {
			// Descriptions may have escaped colons.
			escaped := strings.ReplaceAll(desc, ":", "\\:")
			if !strings.Contains(output, escaped) {
				t.Errorf("zsh completion missing description for %q: %q", cmd, desc)
			}
		}
	}

	// Must contain dynamic completions.
	if !strings.Contains(output, "tetora role list --names") {
		t.Error("zsh completion missing dynamic role completion")
	}
	if !strings.Contains(output, "tetora workflow list --names") {
		t.Error("zsh completion missing dynamic workflow completion")
	}
}

func TestGenerateFishCompletion(t *testing.T) {
	output := generateFishCompletion()

	// Must use fish complete command.
	if !strings.Contains(output, "complete -c tetora") {
		t.Error("fish completion missing 'complete -c tetora'")
	}

	// Must use __fish_use_subcommand for top-level commands.
	if !strings.Contains(output, "__fish_use_subcommand") {
		t.Error("fish completion missing __fish_use_subcommand condition")
	}

	// Must contain all subcommands.
	for _, cmd := range completionSubcommands() {
		if !strings.Contains(output, cmd) {
			t.Errorf("fish completion missing subcommand %q", cmd)
		}
	}

	// Must contain __fish_seen_subcommand_from for sub-actions.
	if !strings.Contains(output, "__fish_seen_subcommand_from") {
		t.Error("fish completion missing __fish_seen_subcommand_from")
	}

	// Must contain descriptions.
	descs := completionSubcommandDescriptions()
	for _, cmd := range []string{"serve", "dispatch", "workflow", "budget"} {
		if desc, ok := descs[cmd]; ok {
			if !strings.Contains(output, desc) {
				t.Errorf("fish completion missing description for %q", cmd)
			}
		}
	}
}

func TestCompletionSubcommandDescriptions(t *testing.T) {
	descs := completionSubcommandDescriptions()
	cmds := completionSubcommands()

	// Every subcommand must have a description.
	for _, cmd := range cmds {
		if _, ok := descs[cmd]; !ok {
			t.Errorf("completionSubcommandDescriptions missing description for %q", cmd)
		}
	}

	// No description should be empty.
	for cmd, desc := range descs {
		if desc == "" {
			t.Errorf("completionSubcommandDescriptions has empty description for %q", cmd)
		}
	}
}

func TestCompletionSubActionDescriptions(t *testing.T) {
	// Every command with sub-actions should have descriptions for each action.
	for _, cmd := range completionSubcommands() {
		actions := completionSubActions(cmd)
		if actions == nil {
			continue
		}
		descs := completionSubActionDescriptions(cmd)
		if descs == nil {
			t.Errorf("completionSubActionDescriptions(%q) returned nil, but has sub-actions", cmd)
			continue
		}
		for _, action := range actions {
			if desc, ok := descs[action]; !ok || desc == "" {
				t.Errorf("completionSubActionDescriptions(%q) missing or empty description for %q", cmd, action)
			}
		}
	}
}
