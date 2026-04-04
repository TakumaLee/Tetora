package provider

import "testing"

func TestInferProviderFromModel(t *testing.T) {
	tests := []struct {
		model      string
		wantPreset string
		wantOK     bool
	}{
		{"gpt-5.4", "codex", true},
		{"gpt-5.3", "codex", true},
		{"gpt-4o", "codex", true},
		{"o3-mini", "codex", true},
		{"o4-mini", "codex", true},
		{"claude-opus-4-6", "claude-code", true},
		{"claude-sonnet-4-6", "claude-code", true},
		{"sonnet", "claude-code", true},
		{"opus", "claude-code", true},
		{"haiku", "claude-code", true},
		{"gemini-2.5-flash", "google", true},
		{"gemini-2.5-pro", "google", true},
		{"llama-3.3-70b-versatile", "groq", true},
		{"mixtral-8x7b-32768", "groq", true},
		{"dolphin-mistral", "", false},
		{"my-custom-model", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			got, ok := InferProviderFromModel(tt.model)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			if got != tt.wantPreset {
				t.Errorf("got %q, want %q", got, tt.wantPreset)
			}
		})
	}
}

func TestInferProviderFromModelWithPref(t *testing.T) {
	tests := []struct {
		model      string
		pref       string
		wantPreset string
		wantOK     bool
	}{
		// Claude models respect preference
		{"claude-sonnet-4-6", "anthropic", "anthropic", true},
		{"claude-sonnet-4-6", "claude-code", "claude-code", true},
		{"claude-sonnet-4-6", "", "claude-code", true}, // default
		{"opus", "anthropic", "anthropic", true},
		{"opus", "", "claude-code", true},
		{"haiku", "anthropic", "anthropic", true},
		// Non-Claude models unaffected by preference
		{"gpt-5.4", "anthropic", "codex", true},
		{"gpt-4o", "anthropic", "codex", true},
		{"gemini-2.5-pro", "anthropic", "google", true},
		{"llama-3.3-70b-versatile", "claude-code", "groq", true},
		// Aliases must be exact match — not prefix
		{"opustest", "", "", false},
		{"haiku-custom-v2", "", "", false},
		// Unknown models
		{"dolphin-mistral", "", "", false},
		{"phi-3", "anthropic", "", false},
	}
	for _, tt := range tests {
		name := tt.model + "_" + tt.pref
		t.Run(name, func(t *testing.T) {
			got, ok := InferProviderFromModelWithPref(tt.model, tt.pref)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			if got != tt.wantPreset {
				t.Errorf("got %q, want %q", got, tt.wantPreset)
			}
		})
	}
}

func TestIsLocalProvider(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"ollama", true},
		{"lmstudio", true},
		{"anthropic", false},
		{"claude-code", false},
		{"codex", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsLocalProvider(tt.name); got != tt.want {
				t.Errorf("IsLocalProvider(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}
