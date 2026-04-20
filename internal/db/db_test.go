package db

import (
	"encoding/json"
	"testing"
)

func TestEscape(t *testing.T) {
	tests := []struct {
		name        string
		input, want string
	}{
		{"normal text", "hello", "hello"},
		{"single quote", "it's", "it''s"},
		{"null byte", "null\x00byte", "nullbyte"},
		{"multiple quotes", "multi''quotes", "multi''''quotes"},
		{"empty string", "", ""},
		{"mixed: quote and null", "a'\x00b", "a''b"},
		{"only null bytes", "\x00\x00", ""},
		{"only quotes", "'''", "''''''"},
		{"backslash passes through", `a\b`, `a\b`},
		{"backslash before quote stays literal", `a\'b`, `a\''b`},
		{"double backslash stays literal", `a\\b`, `a\\b`},
		{"multi-byte UTF-8 passes through", "中文'字", "中文''字"},
		{"emoji 4-byte UTF-8 passes through", "🔥'x", "🔥''x"},
		{"CJK with null byte", "漢\x00字", "漢字"},
		{"tab and newline are literal", "a\tb\nc'd", "a\tb\nc''d"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Escape(tt.input)
			if got != tt.want {
				t.Errorf("Escape(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestStr(t *testing.T) {
	tests := []struct {
		name  string
		input any
		want  string
	}{
		{"nil", nil, ""},
		{"string", "hello", "hello"},
		{"empty string", "", ""},
		{"json.Number integer", json.Number("42"), "42"},
		{"json.Number float", json.Number("3.14"), "3.14"},
		{"int falls through to Sprintf", 99, "99"},
		{"float64 falls through to Sprintf", float64(1.5), "1.5"},
		{"bool falls through to Sprintf", true, "true"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Str(tt.input)
			if got != tt.want {
				t.Errorf("Str(%v) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestInt(t *testing.T) {
	tests := []struct {
		name  string
		input any
		want  int
	}{
		{"nil", nil, 0},
		{"float64 positive", float64(7), 7},
		{"float64 zero", float64(0), 0},
		{"float64 truncates decimal", float64(9.9), 9},
		{"float64 negative", float64(-3), -3},
		{"json.Number valid", json.Number("42"), 42},
		{"json.Number negative", json.Number("-10"), -10},
		{"json.Number invalid", json.Number("notanumber"), 0},
		{"string valid", "123", 123},
		{"string invalid", "abc", 0},
		{"string empty", "", 0},
		{"other type bool", true, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Int(tt.input)
			if got != tt.want {
				t.Errorf("Int(%v) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestFloat(t *testing.T) {
	tests := []struct {
		name  string
		input any
		want  float64
	}{
		{"nil", nil, 0},
		{"float64 positive", float64(3.14), 3.14},
		{"float64 zero", float64(0), 0},
		{"float64 negative", float64(-2.5), -2.5},
		{"json.Number valid float", json.Number("1.23"), 1.23},
		{"json.Number valid integer", json.Number("7"), 7.0},
		{"json.Number invalid", json.Number("bad"), 0},
		{"string valid", "2.718", 2.718},
		{"string valid integer", "10", 10.0},
		{"string invalid", "xyz", 0},
		{"string empty", "", 0},
		{"other type bool", false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Float(tt.input)
			if got != tt.want {
				t.Errorf("Float(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{"shorter than max", "hello", 10, "hello"},
		{"exactly max", "hello", 5, "hello"},
		{"longer than max", "hello world", 5, "hello..."},
		{"empty string", "", 5, ""},
		{"empty string zero max", "", 0, ""},
		{"non-empty zero max", "hi", 0, "..."},
		{"one over max", "abcde", 4, "abcd..."},
		{"max of 1", "abc", 1, "a..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Truncate(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("Truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
			}
		})
	}
}
