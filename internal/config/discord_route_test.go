package config

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestDiscordRouteConfig_UnmarshalJSON_FixedAgent(t *testing.T) {
	data := `{"agent":"kokuyou"}`
	var r DiscordRouteConfig
	if err := json.Unmarshal([]byte(data), &r); err != nil {
		t.Fatal(err)
	}
	if r.Agent != "kokuyou" {
		t.Errorf("expected kokuyou, got %s", r.Agent)
	}
	if r.Mode != "" {
		t.Errorf("expected empty mode, got %s", r.Mode)
	}
}

func TestDiscordRouteConfig_UnmarshalJSON_BackwardCompatRole(t *testing.T) {
	data := `{"role":"hisui"}`
	var r DiscordRouteConfig
	if err := json.Unmarshal([]byte(data), &r); err != nil {
		t.Fatal(err)
	}
	if r.Agent != "hisui" {
		t.Errorf("expected hisui from role field, got %s", r.Agent)
	}
}

func TestDiscordRouteConfig_UnmarshalJSON_SmartMode(t *testing.T) {
	data := `{"mode":"smart","agents":["hisui","spinel"]}`
	var r DiscordRouteConfig
	if err := json.Unmarshal([]byte(data), &r); err != nil {
		t.Fatal(err)
	}
	if r.Mode != "smart" {
		t.Errorf("expected smart mode, got %s", r.Mode)
	}
	want := []string{"hisui", "spinel"}
	if !reflect.DeepEqual(r.Agents, want) {
		t.Errorf("expected agents %v, got %v", want, r.Agents)
	}
	if r.Agent != "" {
		t.Errorf("expected empty single agent, got %s", r.Agent)
	}
}

func TestDiscordRouteConfig_BackwardCompat_AgentOnly(t *testing.T) {
	// Old configs with only "agent" field should keep working.
	data := `{"agent":"kokuyou"}`
	var r DiscordRouteConfig
	if err := json.Unmarshal([]byte(data), &r); err != nil {
		t.Fatal(err)
	}
	// Smart mode should NOT be triggered.
	if r.Mode == "smart" || len(r.Agents) > 0 {
		t.Error("old single-agent config should not trigger smart mode")
	}
}
