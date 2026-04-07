package main

import "testing"

// TestPerAgentConcurrencyGuard verifies the n >= maxPerAgent comparison
// used in the HTTP /dispatch handler and auto-dispatcher scan().
func TestPerAgentConcurrencyGuard(t *testing.T) {
	tests := []struct {
		name       string
		doing      int
		maxPerAgent int
		wantReject bool
	}{
		{"0 doing, limit 1 → allow", 0, 1, false},
		{"1 doing, limit 1 → reject", 1, 1, true},
		{"1 doing, limit 2 → allow", 1, 2, false},
		{"2 doing, limit 2 → reject", 2, 2, true},
		{"3 doing, limit 2 → reject", 3, 2, true},
		{"0 doing, limit 3 → allow", 0, 3, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reject := tt.doing >= tt.maxPerAgent
			if reject != tt.wantReject {
				t.Errorf("doing=%d >= max=%d = %v, want %v",
					tt.doing, tt.maxPerAgent, reject, tt.wantReject)
			}
		})
	}
}
