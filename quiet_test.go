package main

import "testing"

func TestParseHHMM(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		wantH  int
		wantM  int
	}{
		{"valid 08:00", "08:00", 8, 0},
		{"valid 23:59", "23:59", 23, 59},
		{"valid 00:00", "00:00", 0, 0},
		{"valid 12:30", "12:30", 12, 30},
		{"hour out of range", "24:00", -1, -1},
		{"minute out of range", "12:60", -1, -1},
		{"no colon", "abc", -1, -1},
		{"empty string", "", -1, -1},
		{"non-numeric minute", "12:ab", -1, -1},
		{"non-numeric hour", "ab:30", -1, -1},
		{"empty hour part", ":30", 0, 30},
		{"empty minute part", "12:", 12, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotH, gotM := parseHHMM(tt.input)
			if gotH != tt.wantH || gotM != tt.wantM {
				t.Errorf("parseHHMM(%q) = (%d, %d), want (%d, %d)",
					tt.input, gotH, gotM, tt.wantH, tt.wantM)
			}
		})
	}
}

func TestIsQuietHours_Disabled(t *testing.T) {
	cfg := &Config{
		QuietHours: QuietHoursConfig{
			Enabled: false,
			Start:   "23:00",
			End:     "08:00",
		},
	}
	if isQuietHours(cfg) {
		t.Error("isQuietHours should return false when disabled")
	}
}

func TestIsQuietHours_EmptyStart(t *testing.T) {
	cfg := &Config{
		QuietHours: QuietHoursConfig{
			Enabled: true,
			Start:   "",
			End:     "08:00",
		},
	}
	if isQuietHours(cfg) {
		t.Error("isQuietHours should return false when start is empty")
	}
}

func TestIsQuietHours_EmptyEnd(t *testing.T) {
	cfg := &Config{
		QuietHours: QuietHoursConfig{
			Enabled: true,
			Start:   "23:00",
			End:     "",
		},
	}
	if isQuietHours(cfg) {
		t.Error("isQuietHours should return false when end is empty")
	}
}

func TestIsQuietHours_InvalidTimes(t *testing.T) {
	cfg := &Config{
		QuietHours: QuietHoursConfig{
			Enabled: true,
			Start:   "25:00",
			End:     "08:00",
		},
	}
	if isQuietHours(cfg) {
		t.Error("isQuietHours should return false when start time is invalid")
	}

	cfg.QuietHours.Start = "23:00"
	cfg.QuietHours.End = "abc"
	if isQuietHours(cfg) {
		t.Error("isQuietHours should return false when end time is invalid")
	}
}
