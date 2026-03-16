package main

import (
	"strings"
	"testing"

	"tetora/internal/completion"
)

func TestCompletionSubcommands(t *testing.T) {
	cmds := completion.Subcommands()

	expected := []string{
		"serve", "run", "dispatch", "route", "init", "doctor", "health",
		"status", "service", "job", "agent", "history", "config",
		"logs", "prompt", "memory", "mcp", "session", "knowledge",
		"skill", "workflow", "budget", "trust", "webhook", "data", "backup", "restore",
		"proactive", "quick", "dashboard", "compact", "plugin", "task", "version", "help", "completion",
	}

	if len(cmds) != len(expected) {
		t.Fatalf("Subcommands() returned %d items, want %d", len(cmds), len(expected))
	}

	set := make(map[string]bool)
	for _, c := range cmds {
		set[c] = true
	}

	for _, e := range expected {
		if !set[e] {
			t.Errorf("Subcommands() missing %q", e)
		}
	}
}

func TestCompletionSubActions(t *testing.T) {
	tests := []struct {
		cmd      string
		expected []string
	}{
		{"job", []string{"list", "add", "enable", "disable", "remove", "trigger", "history"}},
		{"agent", []string{"list", "add", "show", "remove"}},
		{"workflow", []string{"list", "show", "validate", "create", "delete", "run", "runs", "status", "messages", "history", "rollback", "diff"}},
		{"knowledge", []string{"list", "add", "remove", "path", "search"}},
		{"history", []string{"list", "show", "cost"}},
		{"config", []string{"show", "set", "validate", "migrate", "history", "rollback", "diff", "snapshot", "show-version", "versions"}},
		{"data", []string{"status", "cleanup", "export", "purge"}},
		{"prompt", []string{"list", "show", "add", "edit", "remove"}},
		{"memory", []string{"list", "get", "set", "delete"}},
		{"mcp", []string{"list", "show", "add", "remove", "test"}},
		{"session", []string{"list", "show", "cleanup"}},
		{"skill", []string{"list", "run", "test"}},
		{"budget", []string{"show", "pause", "resume"}},
		{"webhook", []string{"list", "show", "test"}},
		{"service", []string{"install", "uninstall", "status"}},
		{"completion", []string{"bash", "zsh", "fish"}},
	}

	for _, tt := range tests {
		actions := completion.SubActions(tt.cmd)
		if len(actions) != len(tt.expected) {
			t.Errorf("SubActions(%q) returned %d items, want %d: %v", tt.cmd, len(actions), len(tt.expected), actions)
			continue
		}
		for i, a := range actions {
			if a != tt.expected[i] {
				t.Errorf("SubActions(%q)[%d] = %q, want %q", tt.cmd, i, a, tt.expected[i])
			}
		}
	}

	// Commands without sub-actions should return nil.
	nilCmds := []string{"serve", "run", "dispatch", "init", "doctor", "dashboard", "version", "help", "nonexistent"}
	for _, cmd := range nilCmds {
		if actions := completion.SubActions(cmd); actions != nil {
			t.Errorf("SubActions(%q) = %v, want nil", cmd, actions)
		}
	}
}

func TestGenerateBashCompletion(t *testing.T) {
	output := completion.GenerateBash()

	if !strings.Contains(output, "_tetora_completions") {
		t.Error("bash completion missing _tetora_completions function")
	}
	if !strings.Contains(output, "complete -F _tetora_completions tetora") {
		t.Error("bash completion missing 'complete -F' registration")
	}

	for _, cmd := range completion.Subcommands() {
		if !strings.Contains(output, cmd) {
			t.Errorf("bash completion missing subcommand %q", cmd)
		}
	}

	for _, cmd := range []string{"job", "agent", "workflow", "config"} {
		for _, action := range completion.SubActions(cmd) {
			if !strings.Contains(output, action) {
				t.Errorf("bash completion missing sub-action %q for %q", action, cmd)
			}
		}
	}

	if !strings.Contains(output, "tetora agent list --names") {
		t.Error("bash completion missing dynamic agent completion")
	}
	if !strings.Contains(output, "tetora workflow list --names") {
		t.Error("bash completion missing dynamic workflow completion")
	}
}

func TestGenerateZshCompletion(t *testing.T) {
	output := completion.GenerateZsh()

	if !strings.Contains(output, "#compdef tetora") {
		t.Error("zsh completion missing #compdef tetora")
	}
	if !strings.Contains(output, "_tetora") {
		t.Error("zsh completion missing _tetora function")
	}
	if !strings.Contains(output, "_arguments") {
		t.Error("zsh completion missing _arguments")
	}
	if !strings.Contains(output, "_describe") {
		t.Error("zsh completion missing _describe")
	}

	descs := completion.SubcommandDescriptions()
	for _, cmd := range completion.Subcommands() {
		if !strings.Contains(output, cmd) {
			t.Errorf("zsh completion missing subcommand %q", cmd)
		}
		if desc, ok := descs[cmd]; ok {
			escaped := strings.ReplaceAll(desc, ":", "\\:")
			if !strings.Contains(output, escaped) {
				t.Errorf("zsh completion missing description for %q: %q", cmd, desc)
			}
		}
	}

	if !strings.Contains(output, "tetora agent list --names") {
		t.Error("zsh completion missing dynamic agent completion")
	}
	if !strings.Contains(output, "tetora workflow list --names") {
		t.Error("zsh completion missing dynamic workflow completion")
	}
}

func TestGenerateFishCompletion(t *testing.T) {
	output := completion.GenerateFish()

	if !strings.Contains(output, "complete -c tetora") {
		t.Error("fish completion missing 'complete -c tetora'")
	}
	if !strings.Contains(output, "__fish_use_subcommand") {
		t.Error("fish completion missing __fish_use_subcommand condition")
	}

	for _, cmd := range completion.Subcommands() {
		if !strings.Contains(output, cmd) {
			t.Errorf("fish completion missing subcommand %q", cmd)
		}
	}

	if !strings.Contains(output, "__fish_seen_subcommand_from") {
		t.Error("fish completion missing __fish_seen_subcommand_from")
	}

	descs := completion.SubcommandDescriptions()
	for _, cmd := range []string{"serve", "dispatch", "workflow", "budget"} {
		if desc, ok := descs[cmd]; ok {
			if !strings.Contains(output, desc) {
				t.Errorf("fish completion missing description for %q", cmd)
			}
		}
	}
}

func TestCompletionSubcommandDescriptions(t *testing.T) {
	descs := completion.SubcommandDescriptions()
	cmds := completion.Subcommands()

	for _, cmd := range cmds {
		if _, ok := descs[cmd]; !ok {
			t.Errorf("SubcommandDescriptions missing description for %q", cmd)
		}
	}

	for cmd, desc := range descs {
		if desc == "" {
			t.Errorf("SubcommandDescriptions has empty description for %q", cmd)
		}
	}
}

func TestCompletionSubActionDescriptions(t *testing.T) {
	for _, cmd := range completion.Subcommands() {
		actions := completion.SubActions(cmd)
		if actions == nil {
			continue
		}
		descs := completion.SubActionDescriptions(cmd)
		if descs == nil {
			t.Errorf("SubActionDescriptions(%q) returned nil, but has sub-actions", cmd)
			continue
		}
		for _, action := range actions {
			if desc, ok := descs[action]; !ok || desc == "" {
				t.Errorf("SubActionDescriptions(%q) missing or empty description for %q", cmd, action)
			}
		}
	}
}
