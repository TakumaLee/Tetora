package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testRSS20XML = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Test Blog</title>
    <item>
      <title>First Post</title>
      <link>https://example.com/post1</link>
      <description>This is the first post.</description>
      <pubDate>Mon, 23 Feb 2026 10:00:00 +0000</pubDate>
    </item>
    <item>
      <title>Second Post</title>
      <link>https://example.com/post2</link>
      <description>&lt;p&gt;HTML content here&lt;/p&gt;</description>
      <pubDate>Tue, 24 Feb 2026 12:00:00 +0000</pubDate>
    </item>
    <item>
      <title>Third Post</title>
      <link>https://example.com/post3</link>
      <description>Third post content.</description>
      <pubDate>Wed, 25 Feb 2026 14:00:00 +0000</pubDate>
    </item>
  </channel>
</rss>`

const testAtomXML = `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Test Atom Feed</title>
  <entry>
    <title>Atom Entry One</title>
    <link href="https://example.com/atom1" rel="alternate"/>
    <summary>Summary of entry one.</summary>
    <updated>2026-02-23T10:00:00Z</updated>
  </entry>
  <entry>
    <title>Atom Entry Two</title>
    <link href="https://example.com/atom2"/>
    <content>Full content of entry two.</content>
    <updated>2026-02-24T12:00:00Z</updated>
  </entry>
</feed>`

func TestToolRSSReadRSS20(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(testRSS20XML))
	}))
	defer srv.Close()

	// Clear cache.
	rssCache.mu.Lock()
	rssCache.entries = make(map[string]rssCacheEntry)
	rssCache.mu.Unlock()

	cfg := &Config{}
	input, _ := json.Marshal(map[string]any{"url": srv.URL, "limit": 2})
	result, err := toolRSSRead(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Test Blog") {
		t.Errorf("expected feed title, got: %s", result)
	}
	if !strings.Contains(result, "First Post") {
		t.Errorf("expected first item, got: %s", result)
	}
	if !strings.Contains(result, "Second Post") {
		t.Errorf("expected second item, got: %s", result)
	}
	if !strings.Contains(result, "Showing 2 of 3") {
		t.Errorf("expected 2 of 3 items shown, got: %s", result)
	}
	// HTML should be stripped from description.
	if strings.Contains(result, "<p>") {
		t.Errorf("expected HTML stripped, got: %s", result)
	}
}

func TestToolRSSReadAtom(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/atom+xml")
		w.Write([]byte(testAtomXML))
	}))
	defer srv.Close()

	rssCache.mu.Lock()
	rssCache.entries = make(map[string]rssCacheEntry)
	rssCache.mu.Unlock()

	cfg := &Config{}
	input, _ := json.Marshal(map[string]any{"url": srv.URL})
	result, err := toolRSSRead(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Test Atom Feed") {
		t.Errorf("expected atom feed title, got: %s", result)
	}
	if !strings.Contains(result, "Atom Entry One") {
		t.Errorf("expected first entry, got: %s", result)
	}
	if !strings.Contains(result, "https://example.com/atom1") {
		t.Errorf("expected link, got: %s", result)
	}
	if !strings.Contains(result, "Full content of entry two") {
		t.Errorf("expected content fallback, got: %s", result)
	}
}

func TestToolRSSReadMissingURL(t *testing.T) {
	cfg := &Config{}
	input, _ := json.Marshal(map[string]any{})
	_, err := toolRSSRead(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error for missing URL")
	}
	if !strings.Contains(err.Error(), "URL required") {
		t.Errorf("expected URL required error, got: %v", err)
	}
}

func TestToolRSSReadDefaultFeed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(testRSS20XML))
	}))
	defer srv.Close()

	rssCache.mu.Lock()
	rssCache.entries = make(map[string]rssCacheEntry)
	rssCache.mu.Unlock()

	cfg := &Config{RSS: RSSConfig{Feeds: []string{srv.URL}}}
	input, _ := json.Marshal(map[string]any{})
	result, err := toolRSSRead(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Test Blog") {
		t.Errorf("expected default feed content, got: %s", result)
	}
}

func TestToolRSSList(t *testing.T) {
	cfg := &Config{RSS: RSSConfig{
		Feeds: []string{"https://example.com/feed1", "https://example.com/feed2"},
	}}
	input, _ := json.Marshal(map[string]any{})
	result, err := toolRSSList(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "2") {
		t.Errorf("expected 2 feeds, got: %s", result)
	}
	if !strings.Contains(result, "feed1") {
		t.Errorf("expected feed1, got: %s", result)
	}
}

func TestToolRSSListEmpty(t *testing.T) {
	cfg := &Config{}
	input, _ := json.Marshal(map[string]any{})
	result, err := toolRSSList(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "No default") {
		t.Errorf("expected no feeds message, got: %s", result)
	}
}

func TestToolRSSCache(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Write([]byte(testRSS20XML))
	}))
	defer srv.Close()

	rssCache.mu.Lock()
	rssCache.entries = make(map[string]rssCacheEntry)
	rssCache.mu.Unlock()

	cfg := &Config{}
	input, _ := json.Marshal(map[string]any{"url": srv.URL})

	// First call.
	_, err := toolRSSRead(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Second call should use cache.
	_, err = toolRSSRead(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if callCount != 1 {
		t.Errorf("expected 1 HTTP call (cached), got %d", callCount)
	}
}

func TestToolRSSHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	rssCache.mu.Lock()
	rssCache.entries = make(map[string]rssCacheEntry)
	rssCache.mu.Unlock()

	cfg := &Config{}
	input, _ := json.Marshal(map[string]any{"url": srv.URL})
	_, err := toolRSSRead(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error for HTTP failure")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected 500 error, got: %v", err)
	}
}

func TestParseFeedBytesEmpty(t *testing.T) {
	title, items := parseFeedBytes([]byte("not xml"))
	if title != "" || items != nil {
		t.Errorf("expected empty result for invalid XML, got title=%q items=%v", title, items)
	}
}

func TestTruncateText(t *testing.T) {
	result := truncateText("Hello, World!", 5)
	if result != "Hello..." {
		t.Errorf("expected 'Hello...', got %q", result)
	}
	result = truncateText("Hi", 10)
	if result != "Hi" {
		t.Errorf("expected 'Hi', got %q", result)
	}
}
