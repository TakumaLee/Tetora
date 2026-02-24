package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testPodcastRSSXML = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>The Test Podcast</title>
    <description>A podcast for testing purposes.</description>
    <item>
      <title>Episode 3: The Latest</title>
      <guid>ep-003</guid>
      <pubDate>Mon, 23 Feb 2026 10:00:00 +0000</pubDate>
      <enclosure url="https://example.com/ep3.mp3" type="audio/mpeg"/>
      <duration>45:30</duration>
    </item>
    <item>
      <title>Episode 2: The Middle</title>
      <guid>ep-002</guid>
      <pubDate>Mon, 16 Feb 2026 10:00:00 +0000</pubDate>
      <enclosure url="https://example.com/ep2.mp3" type="audio/mpeg"/>
      <duration>38:15</duration>
    </item>
    <item>
      <title>Episode 1: The Beginning</title>
      <guid>ep-001</guid>
      <pubDate>Mon, 09 Feb 2026 10:00:00 +0000</pubDate>
      <enclosure url="https://example.com/ep1.mp3" type="audio/mpeg"/>
      <duration>32:00</duration>
    </item>
  </channel>
</rss>`

func TestParsePodcastRSS(t *testing.T) {
	feed, episodes, err := parsePodcastRSS([]byte(testPodcastRSSXML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if feed.Title != "The Test Podcast" {
		t.Errorf("expected title 'The Test Podcast', got %q", feed.Title)
	}
	if feed.Description != "A podcast for testing purposes." {
		t.Errorf("expected description, got %q", feed.Description)
	}

	if len(episodes) != 3 {
		t.Fatalf("expected 3 episodes, got %d", len(episodes))
	}

	ep := episodes[0]
	if ep.Title != "Episode 3: The Latest" {
		t.Errorf("expected first episode title, got %q", ep.Title)
	}
	if ep.GUID != "ep-003" {
		t.Errorf("expected GUID ep-003, got %q", ep.GUID)
	}
	if ep.AudioURL != "https://example.com/ep3.mp3" {
		t.Errorf("expected audio URL, got %q", ep.AudioURL)
	}
	if ep.Duration != "45:30" {
		t.Errorf("expected duration 45:30, got %q", ep.Duration)
	}
}

func TestParsePodcastRSSNoGUID(t *testing.T) {
	data := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>No GUID Podcast</title>
    <item>
      <title>Episode Without GUID</title>
      <enclosure url="https://example.com/no-guid.mp3" type="audio/mpeg"/>
    </item>
  </channel>
</rss>`

	_, episodes, err := parsePodcastRSS([]byte(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(episodes) != 1 {
		t.Fatalf("expected 1 episode, got %d", len(episodes))
	}
	// Should fall back to audio URL as GUID.
	if episodes[0].GUID != "https://example.com/no-guid.mp3" {
		t.Errorf("expected audio URL as GUID fallback, got %q", episodes[0].GUID)
	}
}

func TestParsePodcastRSSInvalid(t *testing.T) {
	_, _, err := parsePodcastRSS([]byte("not xml"))
	if err == nil {
		t.Fatal("expected error for invalid XML")
	}
}

func TestParsePodcastRSSEmpty(t *testing.T) {
	data := `<?xml version="1.0"?><rss version="2.0"><channel><title>Empty</title></channel></rss>`
	feed, episodes, err := parsePodcastRSS([]byte(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if feed.Title != "Empty" {
		t.Errorf("expected title 'Empty', got %q", feed.Title)
	}
	if len(episodes) != 0 {
		t.Errorf("expected 0 episodes, got %d", len(episodes))
	}
}

// createTestPodcastDB creates a temporary podcast database for testing.
func createTestPodcastDB(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "podcast_test.db")
	if err := initPodcastDB(dbPath); err != nil {
		t.Fatalf("init podcast DB: %v", err)
	}
	return dbPath
}

func TestPodcastSubscribeAndList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(testPodcastRSSXML))
	}))
	defer srv.Close()

	dbPath := createTestPodcastDB(t)
	svc := newPodcastService(dbPath)

	// Subscribe.
	err := svc.Subscribe("testuser", srv.URL)
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}

	// List feeds.
	feeds, err := svc.ListFeeds("testuser")
	if err != nil {
		t.Fatalf("list feeds failed: %v", err)
	}
	if len(feeds) != 1 {
		t.Fatalf("expected 1 feed, got %d", len(feeds))
	}
	if feeds[0].Title != "The Test Podcast" {
		t.Errorf("expected feed title, got %q", feeds[0].Title)
	}

	// List episodes.
	episodes, err := svc.ListEpisodes(srv.URL, 10)
	if err != nil {
		t.Fatalf("list episodes failed: %v", err)
	}
	if len(episodes) != 3 {
		t.Fatalf("expected 3 episodes, got %d", len(episodes))
	}
}

func TestPodcastLatestEpisodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(testPodcastRSSXML))
	}))
	defer srv.Close()

	dbPath := createTestPodcastDB(t)
	svc := newPodcastService(dbPath)

	if err := svc.Subscribe("testuser", srv.URL); err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}

	episodes, err := svc.LatestEpisodes("testuser", 2)
	if err != nil {
		t.Fatalf("latest episodes failed: %v", err)
	}
	if len(episodes) > 2 {
		t.Errorf("expected at most 2 episodes, got %d", len(episodes))
	}
}

func TestPodcastMarkPlayed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(testPodcastRSSXML))
	}))
	defer srv.Close()

	dbPath := createTestPodcastDB(t)
	svc := newPodcastService(dbPath)

	if err := svc.Subscribe("testuser", srv.URL); err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}

	// Mark first episode as played.
	if err := svc.MarkPlayed(srv.URL, "ep-003"); err != nil {
		t.Fatalf("mark played failed: %v", err)
	}

	// Verify it's marked.
	episodes, err := svc.ListEpisodes(srv.URL, 10)
	if err != nil {
		t.Fatalf("list episodes failed: %v", err)
	}

	found := false
	for _, ep := range episodes {
		if ep.GUID == "ep-003" {
			found = true
			if !ep.Played {
				t.Error("expected episode ep-003 to be marked as played")
			}
		}
	}
	if !found {
		t.Error("episode ep-003 not found")
	}
}

func TestPodcastUnsubscribe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(testPodcastRSSXML))
	}))
	defer srv.Close()

	dbPath := createTestPodcastDB(t)
	svc := newPodcastService(dbPath)

	if err := svc.Subscribe("testuser", srv.URL); err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}

	if err := svc.Unsubscribe("testuser", srv.URL); err != nil {
		t.Fatalf("unsubscribe failed: %v", err)
	}

	feeds, err := svc.ListFeeds("testuser")
	if err != nil {
		t.Fatalf("list feeds failed: %v", err)
	}
	if len(feeds) != 0 {
		t.Errorf("expected 0 feeds after unsubscribe, got %d", len(feeds))
	}
}

func TestPodcastSubscribeEmptyURL(t *testing.T) {
	dbPath := createTestPodcastDB(t)
	svc := newPodcastService(dbPath)

	err := svc.Subscribe("testuser", "")
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
	if !strings.Contains(err.Error(), "feed URL required") {
		t.Errorf("expected 'feed URL required' error, got: %v", err)
	}
}

func TestPodcastSubscribeHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	dbPath := createTestPodcastDB(t)
	svc := newPodcastService(dbPath)

	err := svc.Subscribe("testuser", srv.URL)
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected 500 in error, got: %v", err)
	}
}

func TestToolPodcastListNotInitialized(t *testing.T) {
	old := globalPodcastService
	globalPodcastService = nil
	defer func() { globalPodcastService = old }()

	input, _ := json.Marshal(map[string]any{"action": "list"})
	_, err := toolPodcastList(context.Background(), &Config{}, input)
	if err == nil {
		t.Fatal("expected error when podcast not initialized")
	}
	if !strings.Contains(err.Error(), "not initialized") {
		t.Errorf("expected 'not initialized' error, got: %v", err)
	}
}

func TestToolPodcastListAction(t *testing.T) {
	dbPath := createTestPodcastDB(t)
	svc := newPodcastService(dbPath)

	old := globalPodcastService
	globalPodcastService = svc
	defer func() { globalPodcastService = old }()

	input, _ := json.Marshal(map[string]any{"action": "list", "userId": "testuser"})
	result, err := toolPodcastList(context.Background(), &Config{}, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "No podcast subscriptions") {
		t.Errorf("expected no subscriptions message, got: %s", result)
	}
}

func TestToolPodcastInvalidAction(t *testing.T) {
	dbPath := createTestPodcastDB(t)
	svc := newPodcastService(dbPath)

	old := globalPodcastService
	globalPodcastService = svc
	defer func() { globalPodcastService = old }()

	input, _ := json.Marshal(map[string]any{"action": "invalid"})
	_, err := toolPodcastList(context.Background(), &Config{}, input)
	if err == nil {
		t.Fatal("expected error for invalid action")
	}
	if !strings.Contains(err.Error(), "unknown action") {
		t.Errorf("expected 'unknown action' error, got: %v", err)
	}
}

func TestToolPodcastSubscribeAndLatest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(testPodcastRSSXML))
	}))
	defer srv.Close()

	dbPath := createTestPodcastDB(t)
	svc := newPodcastService(dbPath)

	old := globalPodcastService
	globalPodcastService = svc
	defer func() { globalPodcastService = old }()

	// Subscribe via tool.
	input, _ := json.Marshal(map[string]any{
		"action":  "subscribe",
		"feedUrl": srv.URL,
		"userId":  "tooluser",
	})
	result, err := toolPodcastList(context.Background(), &Config{}, input)
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}
	if !strings.Contains(result, "Subscribed") {
		t.Errorf("expected subscribed message, got: %s", result)
	}

	// Latest via tool.
	input, _ = json.Marshal(map[string]any{
		"action": "latest",
		"userId": "tooluser",
		"limit":  2,
	})
	result, err = toolPodcastList(context.Background(), &Config{}, input)
	if err != nil {
		t.Fatalf("latest failed: %v", err)
	}
	if !strings.Contains(result, "Episode") {
		t.Errorf("expected episodes in result, got: %s", result)
	}
}

func TestToolPodcastPlayedMissingArgs(t *testing.T) {
	dbPath := createTestPodcastDB(t)
	svc := newPodcastService(dbPath)

	old := globalPodcastService
	globalPodcastService = svc
	defer func() { globalPodcastService = old }()

	input, _ := json.Marshal(map[string]any{"action": "played"})
	_, err := toolPodcastList(context.Background(), &Config{}, input)
	if err == nil {
		t.Fatal("expected error for missing feedUrl and guid")
	}
	if !strings.Contains(err.Error(), "required") {
		t.Errorf("expected 'required' error, got: %v", err)
	}
}

func TestInitPodcastDB(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_init.db")
	if err := initPodcastDB(dbPath); err != nil {
		t.Fatalf("init podcast DB failed: %v", err)
	}
	// Verify file was created.
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Fatal("expected database file to be created")
	}

	// Running init again should be idempotent.
	if err := initPodcastDB(dbPath); err != nil {
		t.Fatalf("second init should be idempotent: %v", err)
	}
}

func TestTruncatePodcastText(t *testing.T) {
	result := truncatePodcastText("Hello, World!", 5)
	if result != "Hello..." {
		t.Errorf("expected 'Hello...', got %q", result)
	}

	result = truncatePodcastText("Hi", 10)
	if result != "Hi" {
		t.Errorf("expected 'Hi', got %q", result)
	}
}

func TestFormatEpisodes(t *testing.T) {
	episodes := []PodcastEpisode{
		{
			Title:       "Test Episode",
			GUID:        "test-guid",
			PublishedAt: "2026-02-23",
			Duration:    "30:00",
			AudioURL:    "https://example.com/test.mp3",
			Played:      true,
		},
	}

	result := formatEpisodes(episodes)
	if !strings.Contains(result, "Test Episode") {
		t.Error("expected episode title")
	}
	if !strings.Contains(result, "[PLAYED]") {
		t.Error("expected PLAYED marker")
	}
	if !strings.Contains(result, "30:00") {
		t.Error("expected duration")
	}
	if !strings.Contains(result, "test-guid") {
		t.Error("expected GUID")
	}
}

func TestPodcastMarkPlayedMissingArgs(t *testing.T) {
	dbPath := createTestPodcastDB(t)
	svc := newPodcastService(dbPath)

	err := svc.MarkPlayed("", "guid")
	if err == nil {
		t.Fatal("expected error for empty feed URL")
	}

	err = svc.MarkPlayed("url", "")
	if err == nil {
		t.Fatal("expected error for empty GUID")
	}
}
