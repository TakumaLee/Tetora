package warroom

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

var fixtureRaw = `{
  "schema_version": 3,
  "generated_at": "2026-01-01T00:00:00Z",
  "fronts": [
    {
      "id": "polymarket",
      "name": "Polymarket",
      "category": "finance",
      "card_type": "metrics",
      "auto": true,
      "status": "yellow",
      "summary": "test summary",
      "blocking": "",
      "next_action": "",
      "last_updated": "2026-01-01T00:00:00Z",
      "last_intel_at": null,
      "staleness_threshold_hours": null,
      "manual_override": { "active": false, "expires_at": null },
      "depends_on": ["tetora"],
      "metrics": {
        "paper_days": 0,
        "win_rate": null,
        "connection_status": "unknown",
        "active_hypo_count": 1
      },
      "extra_unknown_field": "preserve_me"
    }
  ]
}`

func TestLoadSaveRoundtrip_PreservesUnknownFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "status.json")
	if err := os.WriteFile(path, []byte(fixtureRaw), 0o644); err != nil {
		t.Fatal(err)
	}

	s1, err := LoadStatus(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if err := SaveStatus(path, s1); err != nil {
		t.Fatalf("save: %v", err)
	}

	s2, err := LoadStatus(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	if s1.SchemaVersion != s2.SchemaVersion {
		t.Errorf("schema_version mismatch: %d != %d", s1.SchemaVersion, s2.SchemaVersion)
	}
	if len(s1.Fronts) != len(s2.Fronts) {
		t.Fatalf("front count mismatch: %d != %d", len(s1.Fronts), len(s2.Fronts))
	}

	// Verify unknown field survives.
	var m map[string]json.RawMessage
	if err := json.Unmarshal(s2.Fronts[0], &m); err != nil {
		t.Fatalf("unmarshal front: %v", err)
	}
	if _, ok := m["extra_unknown_field"]; !ok {
		t.Error("extra_unknown_field was dropped on save")
	}
	var extra string
	json.Unmarshal(m["extra_unknown_field"], &extra)
	if extra != "preserve_me" {
		t.Errorf("extra_unknown_field value mangled: %q", extra)
	}
}

func TestUpdateFrontFields_Surgical(t *testing.T) {
	raw := json.RawMessage(`{
		"id": "polymarket",
		"status": "yellow",
		"summary": "old summary",
		"last_updated": "2026-01-01T00:00:00Z",
		"depends_on": ["tetora"],
		"metrics": {
			"paper_days": 10,
			"win_rate": null,
			"connection_status": "unknown",
			"active_hypo_count": 2
		},
		"card_type": "metrics",
		"auto": true
	}`)

	updated, err := UpdateFrontFields(raw, map[string]any{
		"summary":      "[auto] new summary",
		"last_updated": "2026-02-01T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("UpdateFrontFields: %v", err)
	}

	var m map[string]json.RawMessage
	if err := json.Unmarshal(updated, &m); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	// Updated fields.
	var summary string
	json.Unmarshal(m["summary"], &summary)
	if summary != "[auto] new summary" {
		t.Errorf("summary not updated: %q", summary)
	}

	// Preserved fields.
	if _, ok := m["depends_on"]; !ok {
		t.Error("depends_on was dropped")
	}
	if _, ok := m["metrics"]; !ok {
		t.Error("metrics was dropped")
	}

	// Verify metrics value is logically preserved (whitespace may differ).
	origMetrics := extractField(t, raw, "metrics")
	newMetrics := extractField(t, updated, "metrics")
	if !jsonEqual(t, origMetrics, newMetrics) {
		t.Errorf("metrics value changed:\norig: %s\nnew:  %s", origMetrics, newMetrics)
	}

	// Other fields.
	var cardType string
	json.Unmarshal(m["card_type"], &cardType)
	if cardType != "metrics" {
		t.Errorf("card_type was changed: %q", cardType)
	}
}

func extractField(t *testing.T, raw json.RawMessage, key string) json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("extractField unmarshal: %v", err)
	}
	return m[key]
}

func jsonEqual(t *testing.T, a, b json.RawMessage) bool {
	t.Helper()
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		t.Fatalf("jsonEqual unmarshal a: %v", err)
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		t.Fatalf("jsonEqual unmarshal b: %v", err)
	}
	return reflect.DeepEqual(av, bv)
}
