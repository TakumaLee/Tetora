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

// TestStripPostTaskSections verifies the last-line defense that prevents
// server-side internal prompt sections (Post-Task Skill Extraction, 反省,
// Self-Evaluation) from leaking into client-visible output when an agent
// echoes the markdown headers back despite instructions to stay silent.
func TestStripPostTaskSections(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   string
	}{
		{
			name:  "empty input",
			input: "",
			want:  "",
		},
		{
			name:  "normal output untouched",
			input: "Implementation complete. All tests pass.",
			want:  "Implementation complete. All tests pass.",
		},
		{
			name:  "strips Post-Task Skill Extraction section",
			input: "task summary here\n\n## Post-Task Skill Extraction\n完成後要提取 skill...",
			want:  "task summary here",
		},
		{
			name:  "strips 反省 section",
			input: "done.\n\n## 反省\nreflection content\nmore content",
			want:  "done.",
		},
		{
			name:  "strips Self-Evaluation section",
			input: "Report.\n\n## Self-Evaluation\nscore: 0.9",
			want:  "Report.",
		},
		{
			name:  "marker at start returns empty",
			input: "## Post-Task Skill Extraction\nserver-side only",
			want:  "",
		},
		{
			name:  "multiple markers earliest wins",
			input: "body text\n\n## Self-Evaluation\nfirst\n\n## Post-Task Skill Extraction\nsecond",
			want:  "body text",
		},
		{
			name:  "markers reversed order earliest still wins",
			input: "body\n\n## Post-Task Skill Extraction\nA\n## 反省\nB",
			want:  "body",
		},
		{
			name:  "marker mid-line not stripped",
			input: "mention of ## Post-Task Skill Extraction inline is allowed",
			want:  "mention of ## Post-Task Skill Extraction inline is allowed",
		},
		{
			name:  "trailing whitespace trimmed before marker",
			input: "content   \n## Post-Task Skill Extraction\ntail",
			want:  "content",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripPostTaskSections(tt.input); got != tt.want {
				t.Errorf("stripPostTaskSections() = %q, want %q", got, tt.want)
			}
		})
	}
}
