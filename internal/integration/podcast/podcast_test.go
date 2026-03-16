package podcast

import (
	"strings"
	"testing"
)

// --- ParseRSS ---

func TestParseRSS(t *testing.T) {
	data := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>My Podcast</title>
    <description>A test podcast feed</description>
    <item>
      <title>Episode 1</title>
      <guid>ep-001</guid>
      <pubDate>Mon, 01 Jan 2024 00:00:00 +0000</pubDate>
      <enclosure url="https://example.com/ep1.mp3" type="audio/mpeg"/>
      <duration>30:00</duration>
    </item>
    <item>
      <title>Episode 2</title>
      <guid>ep-002</guid>
      <pubDate>Tue, 02 Jan 2024 00:00:00 +0000</pubDate>
      <enclosure url="https://example.com/ep2.mp3" type="audio/mpeg"/>
      <duration>45:00</duration>
    </item>
  </channel>
</rss>`)

	feed, episodes, err := ParseRSS(data)
	if err != nil {
		t.Fatalf("ParseRSS() error: %v", err)
	}

	if feed.Title != "My Podcast" {
		t.Errorf("feed.Title = %q, want %q", feed.Title, "My Podcast")
	}
	if feed.Description != "A test podcast feed" {
		t.Errorf("feed.Description = %q, want %q", feed.Description, "A test podcast feed")
	}

	if len(episodes) != 2 {
		t.Fatalf("len(episodes) = %d, want 2", len(episodes))
	}

	ep := episodes[0]
	if ep.GUID != "ep-001" {
		t.Errorf("episodes[0].GUID = %q, want %q", ep.GUID, "ep-001")
	}
	if ep.Title != "Episode 1" {
		t.Errorf("episodes[0].Title = %q, want %q", ep.Title, "Episode 1")
	}
	if ep.AudioURL != "https://example.com/ep1.mp3" {
		t.Errorf("episodes[0].AudioURL = %q, want %q", ep.AudioURL, "https://example.com/ep1.mp3")
	}
	if ep.Duration != "30:00" {
		t.Errorf("episodes[0].Duration = %q, want %q", ep.Duration, "30:00")
	}
	if ep.PublishedAt != "Mon, 01 Jan 2024 00:00:00 +0000" {
		t.Errorf("episodes[0].PublishedAt = %q", ep.PublishedAt)
	}
}

// TestParseRSSNoGUID verifies the fallback chain: missing guid → audio URL → title.
func TestParseRSSNoGUID(t *testing.T) {
	data := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>No GUID Feed</title>
    <description></description>
    <item>
      <title>Episode Without GUID</title>
      <enclosure url="https://example.com/audio.mp3" type="audio/mpeg"/>
      <duration>10:00</duration>
    </item>
    <item>
      <title>Title Only Fallback</title>
    </item>
  </channel>
</rss>`)

	_, episodes, err := ParseRSS(data)
	if err != nil {
		t.Fatalf("ParseRSS() error: %v", err)
	}
	if len(episodes) != 2 {
		t.Fatalf("len(episodes) = %d, want 2", len(episodes))
	}

	// First item: guid falls back to enclosure URL.
	if episodes[0].GUID != "https://example.com/audio.mp3" {
		t.Errorf("episodes[0].GUID = %q, want audio URL fallback", episodes[0].GUID)
	}

	// Second item: no guid, no enclosure → falls back to title.
	if episodes[1].GUID != "Title Only Fallback" {
		t.Errorf("episodes[1].GUID = %q, want title fallback", episodes[1].GUID)
	}
}

// TestParseRSSInvalid verifies that malformed XML returns an error.
func TestParseRSSInvalid(t *testing.T) {
	_, _, err := ParseRSS([]byte(`this is not xml`))
	if err == nil {
		t.Error("ParseRSS() expected error for invalid XML, got nil")
	}
}

// TestParseRSSEmpty verifies that a valid but empty channel parses without error.
func TestParseRSSEmpty(t *testing.T) {
	data := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title></title>
    <description></description>
  </channel>
</rss>`)

	feed, episodes, err := ParseRSS(data)
	if err != nil {
		t.Fatalf("ParseRSS() error: %v", err)
	}
	if feed == nil {
		t.Fatal("ParseRSS() returned nil feed")
	}
	if len(episodes) != 0 {
		t.Errorf("len(episodes) = %d, want 0", len(episodes))
	}
}

// --- TruncateText ---

func TestTruncateText(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{
			name:   "shorter than limit",
			input:  "hello",
			maxLen: 10,
			want:   "hello",
		},
		{
			name:   "exactly at limit",
			input:  "hello",
			maxLen: 5,
			want:   "hello",
		},
		{
			name:   "one over limit",
			input:  "hello!",
			maxLen: 5,
			want:   "hello...",
		},
		{
			name:   "empty string",
			input:  "",
			maxLen: 10,
			want:   "",
		},
		{
			name:   "multibyte runes respected",
			input:  "日本語テスト", // 6 runes
			maxLen: 4,
			want:   "日本語テ...",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := TruncateText(tc.input, tc.maxLen)
			if got != tc.want {
				t.Errorf("TruncateText(%q, %d) = %q, want %q", tc.input, tc.maxLen, got, tc.want)
			}
		})
	}
}

// --- FormatEpisodes ---

func TestFormatEpisodes(t *testing.T) {
	episodes := []Episode{
		{
			Title:       "Episode One",
			GUID:        "ep-1",
			PublishedAt: "2024-01-01",
			Duration:    "30:00",
			AudioURL:    "https://example.com/ep1.mp3",
			Played:      false,
		},
		{
			Title:       "Episode Two",
			GUID:        "ep-2",
			PublishedAt: "2024-01-02",
			Duration:    "45:00",
			AudioURL:    "https://example.com/ep2.mp3",
			Played:      true,
		},
	}

	out := FormatEpisodes(episodes)

	if !strings.Contains(out, "Episodes (2):") {
		t.Errorf("FormatEpisodes() missing header, got:\n%s", out)
	}
	if !strings.Contains(out, "Episode One") {
		t.Errorf("FormatEpisodes() missing Episode One")
	}
	if !strings.Contains(out, "Episode Two") {
		t.Errorf("FormatEpisodes() missing Episode Two")
	}
	if strings.Contains(out, "[PLAYED]") && !strings.Contains(out, "Episode Two [PLAYED]") {
		t.Errorf("FormatEpisodes() PLAYED marker on wrong episode")
	}
	if !strings.Contains(out, "[PLAYED]") {
		t.Errorf("FormatEpisodes() missing [PLAYED] marker for played episode")
	}
	// Unplayed episode must NOT carry the marker.
	if strings.Contains(out, "Episode One [PLAYED]") {
		t.Errorf("FormatEpisodes() incorrectly marked Episode One as played")
	}
	if !strings.Contains(out, "Published: 2024-01-01") {
		t.Errorf("FormatEpisodes() missing published date")
	}
	if !strings.Contains(out, "Duration: 30:00") {
		t.Errorf("FormatEpisodes() missing duration")
	}
	if !strings.Contains(out, "Audio: https://example.com/ep1.mp3") {
		t.Errorf("FormatEpisodes() missing audio URL")
	}
	if !strings.Contains(out, "GUID: ep-1") {
		t.Errorf("FormatEpisodes() missing GUID")
	}
}

func TestFormatEpisodesEmpty(t *testing.T) {
	out := FormatEpisodes([]Episode{})
	if !strings.Contains(out, "Episodes (0):") {
		t.Errorf("FormatEpisodes(empty) = %q, want header with count 0", out)
	}
}
