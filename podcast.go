package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// --- P23.5: Podcast RSS Parser & Episode Tracker ---

// PodcastConfig holds podcast integration settings.
type PodcastConfig struct {
	Enabled bool `json:"enabled"`
}

// PodcastFeed represents a subscribed podcast feed.
type PodcastFeed struct {
	ID          int    `json:"id"`
	UserID      string `json:"userId"`
	FeedURL     string `json:"feedUrl"`
	Title       string `json:"title"`
	Description string `json:"description"`
	LastChecked string `json:"lastChecked"`
	CreatedAt   string `json:"createdAt"`
}

// PodcastEpisode represents a single podcast episode.
type PodcastEpisode struct {
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

// PodcastService manages podcast subscriptions and episode tracking.
type PodcastService struct {
	dbPath string
}

// globalPodcastService is the singleton podcast service.
var globalPodcastService *PodcastService

func newPodcastService(dbPath string) *PodcastService {
	return &PodcastService{dbPath: dbPath}
}

// initPodcastDB creates the podcast database tables.
func initPodcastDB(dbPath string) error {
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
	cmd := exec.Command("sqlite3", dbPath, schema)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("init podcast schema: %s: %w", string(out), err)
	}
	return nil
}

// --- RSS/XML Parsing ---

// podcastRSSXML represents an RSS 2.0 podcast feed.
type podcastRSSXML struct {
	XMLName xml.Name `xml:"rss"`
	Channel struct {
		Title       string              `xml:"title"`
		Description string              `xml:"description"`
		Items       []podcastRSSItemXML `xml:"item"`
	} `xml:"channel"`
}

type podcastRSSItemXML struct {
	Title       string              `xml:"title"`
	GUID        string              `xml:"guid"`
	PubDate     string              `xml:"pubDate"`
	Description string              `xml:"description"`
	Enclosure   podcastEnclosureXML `xml:"enclosure"`
	Duration    string              `xml:"duration"`
}

type podcastEnclosureXML struct {
	URL  string `xml:"url,attr"`
	Type string `xml:"type,attr"`
}

// parsePodcastRSS parses RSS XML data into feed and episode structs.
func parsePodcastRSS(data []byte) (*PodcastFeed, []PodcastEpisode, error) {
	var rss podcastRSSXML
	if err := xml.Unmarshal(data, &rss); err != nil {
		return nil, nil, fmt.Errorf("parse podcast RSS: %w", err)
	}

	feed := &PodcastFeed{
		Title:       rss.Channel.Title,
		Description: truncatePodcastText(rss.Channel.Description, 500),
	}

	episodes := make([]PodcastEpisode, 0, len(rss.Channel.Items))
	for _, item := range rss.Channel.Items {
		guid := item.GUID
		if guid == "" {
			guid = item.Enclosure.URL // fallback to audio URL as GUID
		}
		if guid == "" {
			guid = item.Title // last resort fallback
		}

		ep := PodcastEpisode{
			GUID:        guid,
			Title:       item.Title,
			PublishedAt: item.PubDate,
			Duration:    item.Duration,
			AudioURL:    item.Enclosure.URL,
		}
		episodes = append(episodes, ep)
	}

	return feed, episodes, nil
}

// truncatePodcastText truncates text to maxLen runes.
func truncatePodcastText(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

// --- Service Methods ---

// Subscribe adds a podcast feed and stores its episodes.
func (p *PodcastService) Subscribe(userID, feedURL string) error {
	if feedURL == "" {
		return fmt.Errorf("feed URL required")
	}
	if userID == "" {
		userID = "default"
	}

	// Fetch and parse the RSS feed.
	feed, episodes, err := p.fetchAndParse(feedURL)
	if err != nil {
		return err
	}

	now := time.Now().UTC().Format(time.RFC3339)

	// Insert or update the feed.
	sql := fmt.Sprintf(
		`INSERT INTO podcast_feeds (user_id, feed_url, title, description, last_checked, created_at)
		 VALUES ('%s','%s','%s','%s','%s','%s')
		 ON CONFLICT(user_id, feed_url) DO UPDATE SET
		   title = excluded.title,
		   description = excluded.description,
		   last_checked = excluded.last_checked`,
		escapeSQLite(userID),
		escapeSQLite(feedURL),
		escapeSQLite(feed.Title),
		escapeSQLite(feed.Description),
		now, now,
	)
	cmd := exec.Command("sqlite3", p.dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("insert podcast feed: %s: %w", string(out), err)
	}

	// Insert episodes (ignore conflicts).
	for _, ep := range episodes {
		epSQL := fmt.Sprintf(
			`INSERT OR IGNORE INTO podcast_episodes (feed_url, episode_guid, title, published_at, duration, audio_url, user_id, created_at)
			 VALUES ('%s','%s','%s','%s','%s','%s','%s','%s')`,
			escapeSQLite(feedURL),
			escapeSQLite(ep.GUID),
			escapeSQLite(ep.Title),
			escapeSQLite(ep.PublishedAt),
			escapeSQLite(ep.Duration),
			escapeSQLite(ep.AudioURL),
			escapeSQLite(userID),
			now,
		)
		cmd := exec.Command("sqlite3", p.dbPath, epSQL)
		if out, err := cmd.CombinedOutput(); err != nil {
			logWarn("insert podcast episode failed", "title", ep.Title, "error", fmt.Sprintf("%s: %s", err, string(out)))
		}
	}

	logInfo("podcast subscribed", "feed", feed.Title, "episodes", len(episodes), "user", userID)
	return nil
}

// fetchAndParse fetches an RSS feed URL and parses it.
func (p *PodcastService) fetchAndParse(feedURL string) (*PodcastFeed, []PodcastEpisode, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(feedURL)
	if err != nil {
		return nil, nil, fmt.Errorf("fetch podcast feed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, nil, fmt.Errorf("podcast feed returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024)) // 5MB limit
	if err != nil {
		return nil, nil, fmt.Errorf("read podcast feed: %w", err)
	}

	return parsePodcastRSS(body)
}

// Unsubscribe removes a podcast feed and its episodes.
func (p *PodcastService) Unsubscribe(userID, feedURL string) error {
	if feedURL == "" {
		return fmt.Errorf("feed URL required")
	}
	if userID == "" {
		userID = "default"
	}

	sql := fmt.Sprintf(
		`DELETE FROM podcast_feeds WHERE user_id = '%s' AND feed_url = '%s';
		 DELETE FROM podcast_episodes WHERE user_id = '%s' AND feed_url = '%s';`,
		escapeSQLite(userID), escapeSQLite(feedURL),
		escapeSQLite(userID), escapeSQLite(feedURL),
	)
	cmd := exec.Command("sqlite3", p.dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("unsubscribe podcast: %s: %w", string(out), err)
	}

	logInfo("podcast unsubscribed", "feedUrl", feedURL, "user", userID)
	return nil
}

// ListFeeds returns all subscribed feeds for a user.
func (p *PodcastService) ListFeeds(userID string) ([]PodcastFeed, error) {
	if userID == "" {
		userID = "default"
	}

	sql := fmt.Sprintf(
		`SELECT id, user_id, feed_url, title, description, last_checked, created_at
		 FROM podcast_feeds WHERE user_id = '%s' ORDER BY title`,
		escapeSQLite(userID),
	)
	rows, err := queryDB(p.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("list podcast feeds: %w", err)
	}

	feeds := make([]PodcastFeed, 0, len(rows))
	for _, row := range rows {
		feeds = append(feeds, PodcastFeed{
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

// ListEpisodes returns episodes for a specific feed.
func (p *PodcastService) ListEpisodes(feedURL string, limit int) ([]PodcastEpisode, error) {
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
		escapeSQLite(feedURL), limit,
	)
	rows, err := queryDB(p.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("list podcast episodes: %w", err)
	}

	return rowsToEpisodes(rows), nil
}

// LatestEpisodes returns the latest episodes across all subscribed feeds for a user.
func (p *PodcastService) LatestEpisodes(userID string, limit int) ([]PodcastEpisode, error) {
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
		escapeSQLite(userID), limit,
	)
	rows, err := queryDB(p.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("latest podcast episodes: %w", err)
	}

	return rowsToEpisodes(rows), nil
}

// MarkPlayed marks an episode as played.
func (p *PodcastService) MarkPlayed(feedURL, guid string) error {
	if feedURL == "" || guid == "" {
		return fmt.Errorf("feed URL and episode GUID required")
	}

	sql := fmt.Sprintf(
		`UPDATE podcast_episodes SET played = 1 WHERE feed_url = '%s' AND episode_guid = '%s'`,
		escapeSQLite(feedURL), escapeSQLite(guid),
	)
	cmd := exec.Command("sqlite3", p.dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("mark played: %s: %w", string(out), err)
	}
	return nil
}

// rowsToEpisodes converts query result rows to PodcastEpisode slices.
func rowsToEpisodes(rows []map[string]any) []PodcastEpisode {
	episodes := make([]PodcastEpisode, 0, len(rows))
	for _, row := range rows {
		played := jsonInt(row["played"]) != 0
		episodes = append(episodes, PodcastEpisode{
			ID:          jsonInt(row["id"]),
			FeedURL:     jsonStr(row["feed_url"]),
			GUID:        jsonStr(row["episode_guid"]),
			Title:       jsonStr(row["title"]),
			PublishedAt: jsonStr(row["published_at"]),
			Duration:    jsonStr(row["duration"]),
			AudioURL:    jsonStr(row["audio_url"]),
			Played:      played,
			UserID:      jsonStr(row["user_id"]),
			CreatedAt:   jsonStr(row["created_at"]),
		})
	}
	return episodes
}

// --- Tool Handler ---

// toolPodcastList handles podcast management actions.
func toolPodcastList(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalPodcastService == nil {
		return "", fmt.Errorf("podcast service not initialized")
	}

	var args struct {
		Action  string `json:"action"`  // subscribe, unsubscribe, list, latest, played
		FeedURL string `json:"feedUrl"` // for subscribe/unsubscribe/episodes
		GUID    string `json:"guid"`    // for mark played
		UserID  string `json:"userId"`
		Limit   int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.UserID == "" {
		args.UserID = "default"
	}
	if args.Limit <= 0 {
		args.Limit = 10
	}

	svc := globalPodcastService

	switch args.Action {
	case "subscribe":
		if err := svc.Subscribe(args.UserID, args.FeedURL); err != nil {
			return "", err
		}
		return fmt.Sprintf("Subscribed to %s", args.FeedURL), nil

	case "unsubscribe":
		if err := svc.Unsubscribe(args.UserID, args.FeedURL); err != nil {
			return "", err
		}
		return fmt.Sprintf("Unsubscribed from %s", args.FeedURL), nil

	case "list":
		feeds, err := svc.ListFeeds(args.UserID)
		if err != nil {
			return "", err
		}
		if len(feeds) == 0 {
			return "No podcast subscriptions.", nil
		}

		var sb strings.Builder
		fmt.Fprintf(&sb, "Podcast subscriptions (%d):\n\n", len(feeds))
		for i, f := range feeds {
			fmt.Fprintf(&sb, "%d. %s\n", i+1, f.Title)
			fmt.Fprintf(&sb, "   %s\n", f.FeedURL)
			if f.Description != "" {
				desc := f.Description
				if len(desc) > 100 {
					desc = desc[:100] + "..."
				}
				fmt.Fprintf(&sb, "   %s\n", desc)
			}
			sb.WriteString("\n")
		}
		return sb.String(), nil

	case "episodes":
		if args.FeedURL == "" {
			return "", fmt.Errorf("feedUrl required for episodes action")
		}
		episodes, err := svc.ListEpisodes(args.FeedURL, args.Limit)
		if err != nil {
			return "", err
		}
		if len(episodes) == 0 {
			return "No episodes found.", nil
		}
		return formatEpisodes(episodes), nil

	case "latest":
		episodes, err := svc.LatestEpisodes(args.UserID, args.Limit)
		if err != nil {
			return "", err
		}
		if len(episodes) == 0 {
			return "No new episodes.", nil
		}
		return formatEpisodes(episodes), nil

	case "played":
		if args.FeedURL == "" || args.GUID == "" {
			return "", fmt.Errorf("feedUrl and guid required for played action")
		}
		if err := svc.MarkPlayed(args.FeedURL, args.GUID); err != nil {
			return "", err
		}
		return fmt.Sprintf("Marked episode %s as played.", args.GUID), nil

	default:
		return "", fmt.Errorf("unknown action %q — use subscribe, unsubscribe, list, episodes, latest, or played", args.Action)
	}
}

// formatEpisodes formats a list of episodes for display.
func formatEpisodes(episodes []PodcastEpisode) string {
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
