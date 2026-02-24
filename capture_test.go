package main

import "testing"

func TestClassifyCapture(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"spent $20 on lunch", "expense"},
		{"paid 500元 for groceries", "expense"},
		{"bought new shoes cost $80", "expense"},
		{"300円のコーヒー", "expense"},
		{"remind me to call doctor", "reminder"},
		{"deadline for project is friday", "reminder"},
		{"don't forget to buy milk", "reminder"},
		{"phone number is 555-1234", "contact"},
		{"email john@example.com", "contact"},
		{"birthday party for alice", "contact"},
		{"todo: review PRs", "task"},
		{"need to fix the login bug", "task"},
		{"should update the docs", "task"},
		{"idea: build a CLI tool", "idea"},
		{"what if we use websockets", "idea"},
		{"the sky is blue today", "note"},
		{"random thought about architecture", "note"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := classifyCapture(tt.input)
			if got != tt.expected {
				t.Errorf("classifyCapture(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestClassifyCapture_DefaultNote(t *testing.T) {
	inputs := []string{
		"hello world",
		"meeting notes from today",
		"考えたこと",
	}
	for _, input := range inputs {
		got := classifyCapture(input)
		if got != "note" {
			t.Errorf("classifyCapture(%q) = %q, want 'note'", input, got)
		}
	}
}
