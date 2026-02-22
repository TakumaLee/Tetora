package main

import "testing"

func TestEscapeSQLite(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"normal string", "hello", "hello"},
		{"single quote", "it's", "it''s"},
		{"double single quotes", "it''s", "it''''s"},
		{"null byte removed", "hello\x00world", "helloworld"},
		{"null and quote combined", "it's\x00test", "it''stest"},
		{"empty string", "", ""},
		{"unicode unchanged", "\u3053\u3093\u306b\u3061\u306f", "\u3053\u3093\u306b\u3061\u306f"},
		{"sql injection attempt", "'; DROP TABLE--", "''; DROP TABLE--"},
		{"multiple quotes", "a'b'c", "a''b''c"},
		{"only null bytes", "\x00\x00\x00", ""},
		{"only single quote", "'", "''"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := escapeSQLite(tt.input)
			if got != tt.want {
				t.Errorf("escapeSQLite(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestStringSliceContains(t *testing.T) {
	tests := []struct {
		name  string
		slice []string
		value string
		want  bool
	}{
		{"found exact", []string{"alpha", "beta", "gamma"}, "beta", true},
		{"found case insensitive upper", []string{"alpha", "beta"}, "ALPHA", true},
		{"found case insensitive mixed", []string{"Hello", "World"}, "hello", true},
		{"not found", []string{"alpha", "beta"}, "delta", false},
		{"empty slice", []string{}, "anything", false},
		{"nil slice", nil, "anything", false},
		{"empty search string", []string{"alpha", ""}, "", true},
		{"first element", []string{"target", "other"}, "target", true},
		{"last element", []string{"other", "target"}, "target", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stringSliceContains(tt.slice, tt.value)
			if got != tt.want {
				t.Errorf("stringSliceContains(%v, %q) = %v, want %v",
					tt.slice, tt.value, got, tt.want)
			}
		})
	}
}
