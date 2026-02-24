package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// --- RSS/Atom Feed Parser ---

// rssCache is a simple in-memory cache with TTL.
var rssCache = struct {
	mu      sync.Mutex
	entries map[string]rssCacheEntry
}{entries: make(map[string]rssCacheEntry)}

type rssCacheEntry struct {
	items   []rssFeedItem
	title   string
	fetched time.Time
}

const rssCacheTTL = 5 * time.Minute

type rssFeedItem struct {
	Title   string
	Link    string
	Summary string
	PubDate string
}

// RSS 2.0 structures.
type rssXML struct {
	XMLName xml.Name `xml:"rss"`
	Channel struct {
		Title string       `xml:"title"`
		Items []rssItemXML `xml:"item"`
	} `xml:"channel"`
}

type rssItemXML struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	PubDate     string `xml:"pubDate"`
}

// Atom structures.
type atomXML struct {
	XMLName xml.Name      `xml:"feed"`
	Title   string        `xml:"title"`
	Entries []atomEntryXML `xml:"entry"`
}

type atomEntryXML struct {
	Title   string       `xml:"title"`
	Links   []atomLinkXML `xml:"link"`
	Summary string       `xml:"summary"`
	Content string       `xml:"content"`
	Updated string       `xml:"updated"`
}

type atomLinkXML struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
}

func fetchFeed(feedURL string) (title string, items []rssFeedItem, err error) {
	// Check cache.
	rssCache.mu.Lock()
	if entry, ok := rssCache.entries[feedURL]; ok && time.Since(entry.fetched) < rssCacheTTL {
		rssCache.mu.Unlock()
		return entry.title, entry.items, nil
	}
	rssCache.mu.Unlock()

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(feedURL)
	if err != nil {
		return "", nil, fmt.Errorf("fetch feed error: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", nil, fmt.Errorf("feed returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024)) // 2MB limit
	if err != nil {
		return "", nil, fmt.Errorf("read feed error: %w", err)
	}

	title, items = parseFeedBytes(body)

	// Update cache.
	rssCache.mu.Lock()
	rssCache.entries[feedURL] = rssCacheEntry{
		items:   items,
		title:   title,
		fetched: time.Now(),
	}
	rssCache.mu.Unlock()

	return title, items, nil
}

func parseFeedBytes(body []byte) (string, []rssFeedItem) {
	// Try RSS 2.0 first.
	var rss rssXML
	if err := xml.Unmarshal(body, &rss); err == nil && len(rss.Channel.Items) > 0 {
		items := make([]rssFeedItem, len(rss.Channel.Items))
		for i, it := range rss.Channel.Items {
			items[i] = rssFeedItem{
				Title:   html.UnescapeString(it.Title),
				Link:    it.Link,
				Summary: truncateText(stripHTMLTags(html.UnescapeString(it.Description)), 200),
				PubDate: it.PubDate,
			}
		}
		return rss.Channel.Title, items
	}

	// Try Atom.
	var atom atomXML
	if err := xml.Unmarshal(body, &atom); err == nil && len(atom.Entries) > 0 {
		items := make([]rssFeedItem, len(atom.Entries))
		for i, e := range atom.Entries {
			link := ""
			for _, l := range e.Links {
				if l.Rel == "" || l.Rel == "alternate" {
					link = l.Href
					break
				}
			}
			if link == "" && len(e.Links) > 0 {
				link = e.Links[0].Href
			}
			summary := e.Summary
			if summary == "" {
				summary = e.Content
			}
			items[i] = rssFeedItem{
				Title:   html.UnescapeString(e.Title),
				Link:    link,
				Summary: truncateText(stripHTMLTags(html.UnescapeString(summary)), 200),
				PubDate: e.Updated,
			}
		}
		return atom.Title, items
	}

	return "", nil
}

// truncateText truncates a string to maxLen characters.
func truncateText(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

func toolRSSRead(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		URL   string `json:"url"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.URL == "" {
		// Use first configured feed.
		if len(cfg.RSS.Feeds) > 0 {
			args.URL = cfg.RSS.Feeds[0]
		} else {
			return "", fmt.Errorf("feed URL required (pass url parameter or configure default feeds)")
		}
	}
	if args.Limit <= 0 {
		args.Limit = 10
	}

	title, items, err := fetchFeed(args.URL)
	if err != nil {
		return "", err
	}
	if len(items) == 0 {
		return "No items found in feed.", nil
	}

	if args.Limit > len(items) {
		args.Limit = len(items)
	}

	var sb strings.Builder
	if title != "" {
		fmt.Fprintf(&sb, "Feed: %s\n", title)
	}
	fmt.Fprintf(&sb, "Showing %d of %d items:\n\n", args.Limit, len(items))
	for i := 0; i < args.Limit; i++ {
		it := items[i]
		fmt.Fprintf(&sb, "%d. %s\n", i+1, it.Title)
		if it.Link != "" {
			fmt.Fprintf(&sb, "   %s\n", it.Link)
		}
		if it.PubDate != "" {
			fmt.Fprintf(&sb, "   %s\n", it.PubDate)
		}
		if it.Summary != "" {
			fmt.Fprintf(&sb, "   %s\n", it.Summary)
		}
		sb.WriteString("\n")
	}
	return sb.String(), nil
}

func toolRSSList(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if len(cfg.RSS.Feeds) == 0 {
		return "No default RSS feeds configured. Add feeds to config.json under rss.feeds.", nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Configured RSS feeds (%d):\n", len(cfg.RSS.Feeds))
	for i, f := range cfg.RSS.Feeds {
		fmt.Fprintf(&sb, "%d. %s\n", i+1, f)
	}
	return sb.String(), nil
}
