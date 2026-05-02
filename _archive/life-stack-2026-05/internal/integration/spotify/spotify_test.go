package spotify

import (
	"encoding/json"
	"testing"
)

// --- Config.MarketOrDefault ---

func TestMarketOrDefault_Empty(t *testing.T) {
	cfg := Config{}
	if got := cfg.MarketOrDefault(); got != "US" {
		t.Errorf("MarketOrDefault() = %q, want %q", got, "US")
	}
}

func TestMarketOrDefault_Custom(t *testing.T) {
	cfg := Config{Market: "JP"}
	if got := cfg.MarketOrDefault(); got != "JP" {
		t.Errorf("MarketOrDefault() = %q, want %q", got, "JP")
	}
}

// --- JSONStrField ---

func TestJSONStrField_Present(t *testing.T) {
	m := map[string]any{"key": "value"}
	if got := JSONStrField(m, "key"); got != "value" {
		t.Errorf("JSONStrField() = %q, want %q", got, "value")
	}
}

func TestJSONStrField_Missing(t *testing.T) {
	m := map[string]any{"other": "value"}
	if got := JSONStrField(m, "missing"); got != "" {
		t.Errorf("JSONStrField() = %q, want empty string", got)
	}
}

func TestJSONStrField_WrongType(t *testing.T) {
	m := map[string]any{"count": 42}
	if got := JSONStrField(m, "count"); got != "" {
		t.Errorf("JSONStrField() = %q, want empty string for non-string value", got)
	}
}

// --- ParseItem ---

func TestParseItem_BasicFields(t *testing.T) {
	raw := json.RawMessage(`{
		"id":   "abc123",
		"name": "Test Track",
		"uri":  "spotify:track:abc123"
	}`)

	item, err := ParseItem(raw, "track")
	if err != nil {
		t.Fatalf("ParseItem() error: %v", err)
	}

	if item.ID != "abc123" {
		t.Errorf("ID = %q, want %q", item.ID, "abc123")
	}
	if item.Name != "Test Track" {
		t.Errorf("Name = %q, want %q", item.Name, "Test Track")
	}
	if item.URI != "spotify:track:abc123" {
		t.Errorf("URI = %q, want %q", item.URI, "spotify:track:abc123")
	}
	if item.Type != "track" {
		t.Errorf("Type = %q, want %q", item.Type, "track")
	}
}

func TestParseItem_ArtistAndAlbum(t *testing.T) {
	raw := json.RawMessage(`{
		"id":   "xyz",
		"name": "Song",
		"uri":  "spotify:track:xyz",
		"artists": [{"name": "Artist One"}],
		"album":   {"name": "Album Name"},
		"duration_ms": 210000,
		"preview_url": "https://example.com/preview.mp3"
	}`)

	item, err := ParseItem(raw, "track")
	if err != nil {
		t.Fatalf("ParseItem() error: %v", err)
	}

	if item.Artist != "Artist One" {
		t.Errorf("Artist = %q, want %q", item.Artist, "Artist One")
	}
	if item.Album != "Album Name" {
		t.Errorf("Album = %q, want %q", item.Album, "Album Name")
	}
	if item.DurMS != 210000 {
		t.Errorf("DurMS = %d, want %d", item.DurMS, 210000)
	}
	if item.Preview != "https://example.com/preview.mp3" {
		t.Errorf("Preview = %q, want %q", item.Preview, "https://example.com/preview.mp3")
	}
}

func TestParseItem_MultipleArtists(t *testing.T) {
	raw := json.RawMessage(`{
		"id":   "multi",
		"name": "Collab",
		"uri":  "spotify:track:multi",
		"artists": [
			{"name": "Artist A"},
			{"name": "Artist B"},
			{"name": "Artist C"}
		]
	}`)

	item, err := ParseItem(raw, "track")
	if err != nil {
		t.Fatalf("ParseItem() error: %v", err)
	}

	want := "Artist A, Artist B, Artist C"
	if item.Artist != want {
		t.Errorf("Artist = %q, want %q", item.Artist, want)
	}
}

func TestParseItem_InvalidJSON(t *testing.T) {
	_, err := ParseItem(json.RawMessage(`not json`), "track")
	if err == nil {
		t.Error("ParseItem() expected error for invalid JSON, got nil")
	}
}

// --- ParseSearchResults ---

func TestParseSearchResults_Track(t *testing.T) {
	data := []byte(`{
		"tracks": {
			"items": [
				{
					"id":   "t1",
					"name": "Track One",
					"uri":  "spotify:track:t1",
					"artists": [{"name": "Singer"}],
					"album": {"name": "Album One"},
					"duration_ms": 180000
				},
				{
					"id":   "t2",
					"name": "Track Two",
					"uri":  "spotify:track:t2",
					"artists": [{"name": "Band"}],
					"duration_ms": 240000
				}
			]
		}
	}`)

	items, err := ParseSearchResults(data, "track")
	if err != nil {
		t.Fatalf("ParseSearchResults() error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(items))
	}

	if items[0].ID != "t1" {
		t.Errorf("items[0].ID = %q, want %q", items[0].ID, "t1")
	}
	if items[0].Artist != "Singer" {
		t.Errorf("items[0].Artist = %q, want %q", items[0].Artist, "Singer")
	}
	if items[1].ID != "t2" {
		t.Errorf("items[1].ID = %q, want %q", items[1].ID, "t2")
	}
}

func TestParseSearchResults_MissingSection(t *testing.T) {
	data := []byte(`{"albums": {"items": []}}`)
	_, err := ParseSearchResults(data, "track")
	if err == nil {
		t.Error("ParseSearchResults() expected error for missing section, got nil")
	}
}

func TestParseSearchResults_InvalidJSON(t *testing.T) {
	_, err := ParseSearchResults([]byte(`bad`), "track")
	if err == nil {
		t.Error("ParseSearchResults() expected error for invalid JSON, got nil")
	}
}
