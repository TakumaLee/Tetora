package db

import "testing"

func TestEscape(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"hello", "hello"},
		{"it's", "it''s"},
		{"null\x00byte", "nullbyte"},
		{"multi''quotes", "multi''''quotes"},
		{"", ""},
	}
	for _, tt := range tests {
		got := Escape(tt.input)
		if got != tt.want {
			t.Errorf("Escape(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
