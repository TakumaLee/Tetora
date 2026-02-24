package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

const testVTTContent = `WEBVTT
Kind: captions
Language: en

00:00:00.000 --> 00:00:03.000
Hello and welcome to the show.

00:00:03.000 --> 00:00:06.000
Hello and welcome to the show.

00:00:06.000 --> 00:00:10.000
Today we will discuss something interesting.

00:00:10.000 --> 00:00:14.000
<c>Let's</c> get <c>started</c> right away.

00:00:14.000 --> 00:00:18.000
1

00:00:18.000 --> 00:00:22.000
This is the first main topic.

00:00:22.000 --> 00:00:26.000
And here we continue with more details.
`

func TestParseVTT(t *testing.T) {
	result := parseVTT(testVTTContent)

	// Should not contain WEBVTT header.
	if strings.Contains(result, "WEBVTT") {
		t.Error("expected WEBVTT header to be stripped")
	}

	// Should not contain timestamps.
	if strings.Contains(result, "-->") {
		t.Error("expected timestamps to be stripped")
	}

	// Should not contain Kind: or Language: lines.
	if strings.Contains(result, "Kind:") {
		t.Error("expected Kind: line to be stripped")
	}
	if strings.Contains(result, "Language:") {
		t.Error("expected Language: line to be stripped")
	}

	// Should contain the actual text.
	if !strings.Contains(result, "Hello and welcome to the show.") {
		t.Error("expected subtitle text to be present")
	}
	if !strings.Contains(result, "Today we will discuss something interesting.") {
		t.Error("expected subtitle text to be present")
	}

	// Duplicate lines should be removed.
	count := strings.Count(result, "Hello and welcome to the show.")
	if count != 1 {
		t.Errorf("expected 1 occurrence of duplicate line, got %d", count)
	}

	// VTT tags should be stripped.
	if strings.Contains(result, "<c>") {
		t.Error("expected VTT tags to be stripped")
	}
	if !strings.Contains(result, "Let's get started right away.") {
		t.Error("expected cleaned text without tags")
	}

	// Should contain other lines.
	if !strings.Contains(result, "This is the first main topic.") {
		t.Error("expected first main topic text")
	}
	if !strings.Contains(result, "And here we continue with more details.") {
		t.Error("expected continuation text")
	}
}

func TestParseVTTEmpty(t *testing.T) {
	result := parseVTT("")
	if result != "" {
		t.Errorf("expected empty result for empty input, got %q", result)
	}
}

func TestParseVTTOnlyHeader(t *testing.T) {
	result := parseVTT("WEBVTT\n\n")
	if result != "" {
		t.Errorf("expected empty result for header-only VTT, got %q", result)
	}
}

const testYouTubeJSON = `{
	"id": "dQw4w9WgXcQ",
	"title": "Rick Astley - Never Gonna Give You Up",
	"channel": "Rick Astley",
	"duration": 212,
	"description": "The official video for Never Gonna Give You Up by Rick Astley.",
	"upload_date": "20091025",
	"view_count": 1500000000
}`

func TestParseYouTubeVideoJSON(t *testing.T) {
	info, err := parseYouTubeVideoJSON([]byte(testYouTubeJSON))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if info.ID != "dQw4w9WgXcQ" {
		t.Errorf("expected ID dQw4w9WgXcQ, got %q", info.ID)
	}
	if info.Title != "Rick Astley - Never Gonna Give You Up" {
		t.Errorf("expected title, got %q", info.Title)
	}
	if info.Channel != "Rick Astley" {
		t.Errorf("expected channel Rick Astley, got %q", info.Channel)
	}
	if info.Duration != 212 {
		t.Errorf("expected duration 212, got %d", info.Duration)
	}
	if info.ViewCount != 1500000000 {
		t.Errorf("expected view count 1500000000, got %d", info.ViewCount)
	}
	if info.UploadDate != "20091025" {
		t.Errorf("expected upload date 20091025, got %q", info.UploadDate)
	}
}

func TestParseYouTubeVideoJSONUploader(t *testing.T) {
	data := `{"id":"test","title":"Test","uploader":"Some Uploader","duration":100}`
	info, err := parseYouTubeVideoJSON([]byte(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Channel != "Some Uploader" {
		t.Errorf("expected uploader fallback, got %q", info.Channel)
	}
}

func TestParseYouTubeVideoJSONInvalid(t *testing.T) {
	_, err := parseYouTubeVideoJSON([]byte("not json"))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestSummarizeYouTubeVideo(t *testing.T) {
	text := strings.Repeat("word ", 1000)
	text = strings.TrimSpace(text)

	result := summarizeYouTubeVideo(text, 100)
	words := strings.Fields(result)
	// Should have 100 words + "..." suffix
	if len(words) != 101 { // 100 words + "word..."
		// The last element is "word..." due to truncation
		lastWord := words[len(words)-1]
		if !strings.HasSuffix(lastWord, "...") && len(words) > 101 {
			t.Errorf("expected ~100 words with ..., got %d words", len(words))
		}
	}

	// Short text should not be truncated.
	short := "this is short"
	result = summarizeYouTubeVideo(short, 100)
	if result != short {
		t.Errorf("expected unchanged short text, got %q", result)
	}
}

func TestSummarizeYouTubeVideoDefaultWords(t *testing.T) {
	text := strings.Repeat("word ", 600)
	result := summarizeYouTubeVideo(text, 0) // 0 should default to 500
	words := strings.Fields(result)
	// Should be truncated since 600 > 500
	if len(words) > 502 { // 500 words + trailing "..."
		t.Errorf("expected ~500 words, got %d", len(words))
	}
}

func TestFormatYTDuration(t *testing.T) {
	tests := []struct {
		seconds  int
		expected string
	}{
		{0, "0:00"},
		{-5, "0:00"},
		{65, "1:05"},
		{3661, "1:01:01"},
		{212, "3:32"},
		{7200, "2:00:00"},
	}
	for _, tc := range tests {
		result := formatYTDuration(tc.seconds)
		if result != tc.expected {
			t.Errorf("formatYTDuration(%d) = %q, want %q", tc.seconds, result, tc.expected)
		}
	}
}

func TestFormatViewCount(t *testing.T) {
	tests := []struct {
		count    int
		expected string
	}{
		{0, "0"},
		{-1, "0"},
		{999, "999"},
		{1000, "1,000"},
		{1234567, "1,234,567"},
		{1500000000, "1,500,000,000"},
	}
	for _, tc := range tests {
		result := formatViewCount(tc.count)
		if result != tc.expected {
			t.Errorf("formatViewCount(%d) = %q, want %q", tc.count, result, tc.expected)
		}
	}
}

func TestIsNumericLine(t *testing.T) {
	if !isNumericLine("123") {
		t.Error("expected '123' to be numeric")
	}
	if isNumericLine("12a") {
		t.Error("expected '12a' to not be numeric")
	}
	if isNumericLine("") {
		t.Error("expected empty string to not be numeric")
	}
	if isNumericLine("12.5") {
		t.Error("expected '12.5' to not be numeric")
	}
}

func TestToolYouTubeSummaryMissingURL(t *testing.T) {
	input, _ := json.Marshal(map[string]any{})
	_, err := toolYouTubeSummary(context.Background(), &Config{}, input)
	if err == nil {
		t.Fatal("expected error for missing URL")
	}
	if !strings.Contains(err.Error(), "url required") {
		t.Errorf("expected 'url required' error, got: %v", err)
	}
}

func TestToolYouTubeSummaryInvalidInput(t *testing.T) {
	_, err := toolYouTubeSummary(context.Background(), &Config{}, json.RawMessage(`{invalid`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// Integration test: only runs if yt-dlp is available.
func TestYouTubeIntegration(t *testing.T) {
	if _, err := exec.LookPath("yt-dlp"); err != nil {
		t.Skip("yt-dlp not available, skipping integration test")
	}
	// Skipping actual download tests in CI — they require network access.
	t.Skip("skipping integration test (requires network)")
}

func TestYouTubeConfigYtDlpOrDefault(t *testing.T) {
	c := YouTubeConfig{}
	if c.ytDlpOrDefault() != "yt-dlp" {
		t.Errorf("expected yt-dlp default, got %q", c.ytDlpOrDefault())
	}

	c.YtDlpPath = "/usr/local/bin/yt-dlp"
	if c.ytDlpOrDefault() != "/usr/local/bin/yt-dlp" {
		t.Errorf("expected custom path, got %q", c.ytDlpOrDefault())
	}
}

func TestWriteVideoHeader(t *testing.T) {
	info := &YouTubeVideoInfo{
		Title:      "Test Video",
		Channel:    "Test Channel",
		Duration:   185,
		ViewCount:  1234567,
		UploadDate: "20260101",
	}

	var sb strings.Builder
	writeVideoHeader(&sb, info)
	result := sb.String()

	if !strings.Contains(result, "Title: Test Video") {
		t.Error("expected title in header")
	}
	if !strings.Contains(result, "Channel: Test Channel") {
		t.Error("expected channel in header")
	}
	if !strings.Contains(result, "Duration: 3:05") {
		t.Error("expected duration in header")
	}
	if !strings.Contains(result, "Views: 1,234,567") {
		t.Error("expected view count in header")
	}
	if !strings.Contains(result, "Uploaded: 20260101") {
		t.Error("expected upload date in header")
	}
}
