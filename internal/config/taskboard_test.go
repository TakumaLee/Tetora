package config

import "testing"

func TestMaxTasksPerAgentOrDefault(t *testing.T) {
	tests := []struct {
		name string
		val  int
		want int
	}{
		{"zero returns default 1", 0, 1},
		{"negative returns default 1", -1, 1},
		{"positive returns configured value", 3, 3},
		{"one returns 1", 1, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := TaskBoardDispatchConfig{MaxTasksPerAgent: tt.val}
			if got := cfg.MaxTasksPerAgentOrDefault(); got != tt.want {
				t.Errorf("MaxTasksPerAgentOrDefault() = %d, want %d", got, tt.want)
			}
		})
	}
}
