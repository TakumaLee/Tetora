package main

import "testing"

func TestToolsForComplexity(t *testing.T) {
	tests := []struct {
		name       string
		complexity RequestComplexity
		want       string
	}{
		{"simple returns none", ComplexitySimple, "none"},
		{"standard returns standard", ComplexityStandard, "standard"},
		{"complex returns full", ComplexityComplex, "full"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ToolsForComplexity(tt.complexity)
			if got != tt.want {
				t.Errorf("ToolsForComplexity(%v) = %q, want %q", tt.complexity, got, tt.want)
			}
		})
	}
}

func TestToolsForComplexityProfileIntegration(t *testing.T) {
	// Verify that the profile returned by ToolsForComplexity is handled
	// correctly by ToolsForProfile.

	// "none" profile should return nil from ToolsForProfile (unknown profile).
	profile := ToolsForComplexity(ComplexitySimple)
	if profile != "none" {
		t.Fatalf("expected 'none' for simple, got %q", profile)
	}
	allowed := ToolsForProfile(profile)
	if allowed != nil {
		t.Error("ToolsForProfile('none') should return nil (unknown profile)")
	}

	// "standard" should return a non-nil set with known tools.
	profile = ToolsForComplexity(ComplexityStandard)
	if profile != "standard" {
		t.Fatalf("expected 'standard', got %q", profile)
	}
	allowed = ToolsForProfile(profile)
	if allowed == nil {
		t.Fatal("ToolsForProfile('standard') should return non-nil tool set")
	}
	if !allowed["memory_get"] {
		t.Error("standard profile should include memory_get")
	}
	if !allowed["web_search"] {
		t.Error("standard profile should include web_search")
	}

	// "full" should return nil (all tools).
	profile = ToolsForComplexity(ComplexityComplex)
	if profile != "full" {
		t.Fatalf("expected 'full', got %q", profile)
	}
	allowed = ToolsForProfile(profile)
	if allowed != nil {
		t.Error("ToolsForProfile('full') should return nil (all tools)")
	}
}
