package config

import "testing"

func TestWarRoomAutoUpdateConfig_ScheduleOrDefault(t *testing.T) {
	c := WarRoomAutoUpdateConfig{}
	if got := c.ScheduleOrDefault(); got != "*/15 * * * *" {
		t.Errorf("default schedule: got %q, want %q", got, "*/15 * * * *")
	}

	c = WarRoomAutoUpdateConfig{Schedule: "0 * * * *"}
	if got := c.ScheduleOrDefault(); got != "0 * * * *" {
		t.Errorf("custom schedule: got %q, want %q", got, "0 * * * *")
	}
}
