package main

import (
	"strings"
	"testing"
)

// TestCountToolCalls_JSONMarkers verifies that the JSON-pattern matcher counts
// only genuine tool_use markers and ignores prose mentions of the phrase.
func TestCountToolCalls_JSONMarkers(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   int
	}{
		{
			name:   "empty",
			output: "",
			want:   0,
		},
		{
			name:   "no markers",
			output: "plain output text",
			want:   0,
		},
		{
			name:   "prose mentions are ignored",
			output: "The agent invoked a tool_use step and then another tool_use call.",
			want:   0,
		},
		{
			name:   "single JSON marker",
			output: `{"type":"tool_use","name":"Read"}`,
			want:   1,
		},
		{
			name:   "multiple JSON markers",
			output: `[{"type":"tool_use"},{"type":"text"},{"type":"tool_use"},{"type":"tool_use"}]`,
			want:   3,
		},
		{
			name:   "whitespace variants",
			output: `{"type" : "tool_use"} and {"type":  "tool_use"}`,
			want:   2,
		},
		{
			name:   "pretty-printed JSON",
			output: "{\n  \"type\": \"tool_use\",\n  \"name\": \"Bash\"\n}",
			want:   1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := countToolCalls(tt.output); got != tt.want {
				t.Errorf("countToolCalls() = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestShouldExtractSkill_GateConditions covers the dispatch-layer wrapper
// that converts a TaskResult into skill.TaskSignals. Focuses on the gate
// conditions the wrapper owns (status and WorkspaceDir); the threshold
// logic itself is tested in internal/skill/skill_autoextract_test.go.
func TestShouldExtractSkill_GateConditions(t *testing.T) {
	buildOutput := func(n int) string {
		return strings.Repeat(`{"type":"tool_use"}`, n)
	}

	tests := []struct {
		name   string
		cfg    *Config
		task   Task
		result TaskResult
		want   bool
	}{
		{
			name:   "non-success status skips",
			cfg:    &Config{WorkspaceDir: "/tmp/ws"},
			result: TaskResult{Status: "error", Output: buildOutput(10)},
			want:   false,
		},
		{
			name:   "empty WorkspaceDir skips",
			cfg:    &Config{WorkspaceDir: ""},
			result: TaskResult{Status: "success", Output: buildOutput(10)},
			want:   false,
		},
		{
			name:   "below threshold skips",
			cfg:    &Config{WorkspaceDir: "/tmp/ws"},
			result: TaskResult{Status: "success", Output: buildOutput(4)},
			want:   false,
		},
		{
			name:   "above threshold triggers",
			cfg:    &Config{WorkspaceDir: "/tmp/ws"},
			result: TaskResult{Status: "success", Output: buildOutput(5)},
			want:   true,
		},
		{
			name:   "prose mentions do not reach threshold",
			cfg:    &Config{WorkspaceDir: "/tmp/ws"},
			result: TaskResult{Status: "success", Output: strings.Repeat("tool_use ", 20)},
			want:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldExtractSkill(tt.cfg, tt.task, tt.result); got != tt.want {
				t.Errorf("shouldExtractSkill() = %v, want %v", got, tt.want)
			}
		})
	}
}
