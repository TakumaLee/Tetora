// Package podcast provides a podcast subscription and episode tracking service.
// It uses RSS 2.0 parsing over HTTP and persists data via injected SQLite helpers,
// following the same dependency-injection pattern as internal/life/lifedb.
package podcast

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Config holds podcast integration settings.
type Config struct {
	Enabled bool `json:"enabled"`
}

// Feed represents a subscribed podcast feed.
type Feed struct {
	ID          int    `json:"id"`
	UserID      string `json:"userId"`
	FeedURL     string `json:"feedUrl"`
	Title       string `json:"title"`
	Description string `json:"description"`
	LastChecked string `json:"lastChecked"`
	CreatedAt   string `json:"createdAt"`
}

// Episode represents a single podcast episode.
type Episode struct {
	ID          int    `json:"id"`
	FeedURL     string `json:"feedUrl"`
	GUID        string `json:"guid"`
	Title       string `json:"title"`
	PublishedAt string `json:"publishedAt"`
	Duration    string `json:"duration"`
	AudioURL    string `json:"audioUrl"`
	Played      bool   `json:"played"`
	UserID      string `json:"userId"`
	CreatedAt   string `json:"createdAt"`
}

// DB bundles the database helpers needed by the podcast service.
type DB struct {
	Query   func(dbPath, sql string) ([]map[string]any, error)
	Exec    func(dbPath, sql string) error
	Escape  func(s string) string
	LogInfo func(msg string, keyvals ...any)
	LogWarn func(msg string, keyvals ...any)
}

// Service manages podcast subscriptions and episode tracking.
type Service struct {
	dbPath string
	db     DB
}

// New creates a new podcast Service.
func New(dbPath string, db DB) *Service {
	return &Service{dbPath: dbPath, db: db}
}

// InitDB creates the podcast database schema. It is a standalone function
// because it is called before the Service is constructed.
func InitDB(dbPath string, exec func(dbPath, sql string) error) error {
	schema := `
CREATE TABLE IF NOT EXISTS podcast_feeds (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id TEXT DEFAULT '',
    feed_url TEXT NOT NULL,
    title TEXT DEFAULT '',
    description TEXT DEFAULT '',
    last_checked TEXT DEFAULT '',
    created_at TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_podcast_feed ON podcast_feeds(user_id, feed_url);

CREATE TABLE IF NOT EXISTS podcast_episodes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    feed_url TEXT NOT NULL,
    episode_guid TEXT NOT NULL,
    title TEXT NOT NULL,
    published_at TEXT DEFAULT '',
    duration TEXT DEFAULT '',
    audio_url TEXT DEFAULT '',
    played INTEGER DEFAULT 0,
    user_id TEXT DEFAULT '',
    created_at TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_podcast_ep ON podcast_episodes(feed_url, episode_guid);
`
	return exec(dbPath, schema)
}

// --- RSS/XML parsing types (unexported) ---

type rssXML struct {
	XMLName xml.Name `xml:"rss"`
	Channel struct {
		Title       string       `xml:"title"`
		Description string       `xml:"description"`
		Items       []rssItemXML `xml:"item"`
	} `xml:"channel"`
}

type rssItemXML struct {
	Title     string       `xml:"title"`
	GUID      string       `xml:"guid"`
	PubDate   string       `xml:"pubDate"`
	Enclosure enclosureXML `xml:"enclosure"`
	Duration  string       `xml:"duration"`
}

type enclosureXML struct {
	URL  string `xml:"url,attr"`
	Type string `xml:"type,attr"`
}

// --- Exported parsing/formatting helpers ---

// ParseRSS parses RSS 2.0 XML data into a Feed and its Episodes.
func ParseRSS(data []byte) (*Feed, []Episode, error) {
	var rss rssXML
	if err := xml.Unmarshal(data, &rss); err != nil {
		return nil, nil, fmt.Errorf("parse podcast RSS: %w", err)
	}

	feed := &Feed{
		Title:       rss.Channel.Title,
		Description: TruncateText(rss.Channel.Description, 500),
	}

	episodes := make([]Episode, 0, len(rss.Channel.Items))
	for _, item := range rss.Channel.Items {
		guid := item.GUID
		if guid == "" {
			guid = item.Enclosure.URL // fallback to audio URL as GUID
		}
		if guid == "" {
			guid = item.Title // last-resort fallback
		}

		episodes = append(episodes, Episode{
			GUID:        guid,
			Title:       item.Title,
			PublishedAt: item.PubDate,
			Duration:    item.Duration,
			AudioURL:    item.Enclosure.URL,
		})
	}

	return feed, episodes, nil
}

// TruncateText truncates s to at most maxLen runes, appending "..." when trimmed.
func TruncateText(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

// FormatEpisodes formats a slice of episodes for human-readable display.
func FormatEpisodes(episodes []Episode) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Episodes (%d):\n\n", len(episodes))
	for i, ep := range episodes {
		played := ""
		if ep.Played {
			played = " [PLAYED]"
		}
		fmt.Fprintf(&sb, "%d. %s%s\n", i+1, ep.Title, played)
		if ep.PublishedAt != "" {
			fmt.Fprintf(&sb, "   Published: %s\n", ep.PublishedAt)
		}
		if ep.Duration != "" {
			fmt.Fprintf(&sb, "   Duration: %s\n", ep.Duration)
		}
		if ep.AudioURL != "" {
			fmt.Fprintf(&sb, "   Audio: %s\n", ep.AudioURL)
		}
		fmt.Fprintf(&sb, "   GUID: %s\n\n", ep.GUID)
	}
	return sb.String()
}

// --- Service methods ---

// Subscribe fetches the RSS feed at feedURL, stores the feed record, and
// bulk-inserts all episodes (ignoring duplicates).
func (s *Service) Subscribe(userID, feedURL string) error {
	if feedURL == "" {
		return fmt.Errorf("feed URL required")
	}
	if userID == "" {
		userID = "default"
	}

	feed, episodes, err := s.fetchAndParse(feedURL)
	if err != nil {
		return err
	}

	now := time.Now().UTC().Format(time.RFC3339)

	sql := fmt.Sprintf(
		`INSERT INTO podcast_feeds (user_id, feed_url, title, description, last_checked, created_at)
		 VALUES ('%s','%s','%s','%s','%s','%s')
		 ON CONFLICT(user_id, feed_url) DO UPDATE SET
		   title = excluded.title,
		   description = excluded.description,
		   last_checked = excluded.last_checked`,
		s.db.Escape(userID),
		s.db.Escape(feedURL),
		s.db.Escape(feed.Title),
		s.db.Escape(feed.Description),
		now, now,
	)
	if err := s.db.Exec(s.dbPath, sql); err != nil {
		return fmt.Errorf("insert podcast feed: %w", err)
	}

	for _, ep := range episodes {
		epSQL := fmt.Sprintf(
			`INSERT OR IGNORE INTO podcast_episodes (feed_url, episode_guid, title, published_at, duration, audio_url, user_id, created_at)
			 VALUES ('%s','%s','%s','%s','%s','%s','%s','%s')`,
			s.db.Escape(feedURL),
			s.db.Escape(ep.GUID),
			s.db.Escape(ep.Title),
			s.db.Escape(ep.PublishedAt),
			s.db.Escape(ep.Duration),
			s.db.Escape(ep.AudioURL),
			s.db.Escape(userID),
			now,
		)
		if err := s.db.Exec(s.dbPath, epSQL); err != nil {
			s.db.LogWarn("insert podcast episode failed", "title", ep.Title, "error", err.Error())
		}
	}

	s.db.LogInfo("podcast subscribed", "feed", feed.Title, "episodes", len(episodes), "user", userID)
	return nil
}

// Unsubscribe removes a feed and all its episodes for userID.
func (s *Service) Unsubscribe(userID, feedURL string) error {
	if feedURL == "" {
		return fmt.Errorf("feed URL required")
	}
	if userID == "" {
		userID = "default"
	}

	sql := fmt.Sprintf(
		`DELETE FROM podcast_feeds WHERE user_id = '%s' AND feed_url = '%s';
		 DELETE FROM podcast_episodes WHERE user_id = '%s' AND feed_url = '%s';`,
		s.db.Escape(userID), s.db.Escape(feedURL),
		s.db.Escape(userID), s.db.Escape(feedURL),
	)
	if err := s.db.Exec(s.dbPath, sql); err != nil {
		return fmt.Errorf("unsubscribe podcast: %w", err)
	}

	s.db.LogInfo("podcast unsubscribed", "feedUrl", feedURL, "user", userID)
	return nil
}

// ListFeeds returns all subscribed feeds for userID, ordered by title.
func (s *Service) ListFeeds(userID string) ([]Feed, error) {
	if userID == "" {
		userID = "default"
	}

	sql := fmt.Sprintf(
		`SELECT id, user_id, feed_url, title, description, last_checked, created_at
		 FROM podcast_feeds WHERE user_id = '%s' ORDER BY title`,
		s.db.Escape(userID),
	)
	rows, err := s.db.Query(s.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("list podcast feeds: %w", err)
	}

	feeds := make([]Feed, 0, len(rows))
	for _, row := range rows {
		feeds = append(feeds, Feed{
			ID:          jsonInt(row["id"]),
			UserID:      jsonStr(row["user_id"]),
			FeedURL:     jsonStr(row["feed_url"]),
			Title:       jsonStr(row["title"]),
			Description: jsonStr(row["description"]),
			LastChecked: jsonStr(row["last_checked"]),
			CreatedAt:   jsonStr(row["created_at"]),
		})
	}
	return feeds, nil
}

// ListEpisodes returns up to limit episodes for feedURL, newest first.
func (s *Service) ListEpisodes(feedURL string, limit int) ([]Episode, error) {
	if feedURL == "" {
		return nil, fmt.Errorf("feed URL required")
	}
	if limit <= 0 {
		limit = 20
	}

	sql := fmt.Sprintf(
		`SELECT id, feed_url, episode_guid, title, published_at, duration, audio_url, played, user_id, created_at
		 FROM podcast_episodes WHERE feed_url = '%s'
		 ORDER BY published_at DESC LIMIT %d`,
		s.db.Escape(feedURL), limit,
	)
	rows, err := s.db.Query(s.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("list podcast episodes: %w", err)
	}

	return rowsToEpisodes(rows), nil
}

// LatestEpisodes returns the newest limit episodes across all feeds subscribed by userID.
func (s *Service) LatestEpisodes(userID string, limit int) ([]Episode, error) {
	if userID == "" {
		userID = "default"
	}
	if limit <= 0 {
		limit = 10
	}

	sql := fmt.Sprintf(
		`SELECT e.id, e.feed_url, e.episode_guid, e.title, e.published_at, e.duration, e.audio_url, e.played, e.user_id, e.created_at
		 FROM podcast_episodes e
		 JOIN podcast_feeds f ON e.feed_url = f.feed_url AND e.user_id = f.user_id
		 WHERE e.user_id = '%s'
		 ORDER BY e.published_at DESC LIMIT %d`,
		s.db.Escape(userID), limit,
	)
	rows, err := s.db.Query(s.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("latest podcast episodes: %w", err)
	}

	return rowsToEpisodes(rows), nil
}

// MarkPlayed marks the episode identified by feedURL+guid as played.
func (s *Service) MarkPlayed(feedURL, guid string) error {
	if feedURL == "" || guid == "" {
		return fmt.Errorf("feed URL and episode GUID required")
	}

	sql := fmt.Sprintf(
		`UPDATE podcast_episodes SET played = 1 WHERE feed_url = '%s' AND episode_guid = '%s'`,
		s.db.Escape(feedURL), s.db.Escape(guid),
	)
	if err := s.db.Exec(s.dbPath, sql); err != nil {
		return fmt.Errorf("mark played: %w", err)
	}
	return nil
}

// --- Internal helpers ---

// fetchAndParse downloads feedURL (5 MB limit, 15 s timeout) and parses it.
func (s *Service) fetchAndParse(feedURL string) (*Feed, []Episode, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(feedURL)
	if err != nil {
		return nil, nil, fmt.Errorf("fetch podcast feed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("podcast feed returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return nil, nil, fmt.Errorf("read podcast feed: %w", err)
	}

	return ParseRSS(body)
}

// rowsToEpisodes converts query result rows to an Episode slice.
func rowsToEpisodes(rows []map[string]any) []Episode {
	episodes := make([]Episode, 0, len(rows))
	for _, row := range rows {
		episodes = append(episodes, Episode{
			ID:          jsonInt(row["id"]),
			FeedURL:     jsonStr(row["feed_url"]),
			GUID:        jsonStr(row["episode_guid"]),
			Title:       jsonStr(row["title"]),
			PublishedAt: jsonStr(row["published_at"]),
			Duration:    jsonStr(row["duration"]),
			AudioURL:    jsonStr(row["audio_url"]),
			Played:      jsonInt(row["played"]) != 0,
			UserID:      jsonStr(row["user_id"]),
			CreatedAt:   jsonStr(row["created_at"]),
		})
	}
	return episodes
}

// jsonStr extracts a string from a query result value; returns "" on mismatch.
func jsonStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// jsonInt extracts an int from a query result value (handles float64 from JSON
// decode and int directly); returns 0 on mismatch.
func jsonInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}
