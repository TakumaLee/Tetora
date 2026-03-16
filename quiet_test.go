package main

import (
	"testing"

	"tetora/internal/quiet"
)

func TestIsQuietHours_Disabled(t *testing.T) {
	cfg := &Config{
		QuietHours: QuietHoursConfig{
			Enabled: false,
			Start:   "23:00",
			End:     "08:00",
		},
	}
	if quiet.IsQuietHours(toQuietCfg(cfg)) {
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
	if quiet.IsQuietHours(toQuietCfg(cfg)) {
		t.Error("isQuietHours should return false when start is empty")
	}
}
