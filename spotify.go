package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// --- P23.5: Spotify Media Control ---

// SpotifyConfig holds Spotify integration settings.
type SpotifyConfig struct {
	Enabled      bool   `json:"enabled"`
	ClientID     string `json:"clientId,omitempty"`
	ClientSecret string `json:"clientSecret,omitempty"`
	Market       string `json:"market,omitempty"` // default "US"
}

func (c SpotifyConfig) marketOrDefault() string {
	if c.Market != "" {
		return c.Market
	}
	return "US"
}

// SpotifyItem represents a Spotify track, album, artist, or playlist.
type SpotifyItem struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	URI     string `json:"uri"`
	Type    string `json:"type"` // track, album, artist, playlist
	Artist  string `json:"artist,omitempty"`
	Album   string `json:"album,omitempty"`
	DurMS   int    `json:"durationMs,omitempty"`
	Preview string `json:"previewUrl,omitempty"`
}

// SpotifyDevice represents a Spotify Connect device.
type SpotifyDevice struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	IsActive bool   `json:"isActive"`
	Volume   int    `json:"volumePercent"`
}

// SpotifyService manages Spotify API interactions.
type SpotifyService struct {
	cfg     *Config
	baseURL string // overridable for tests
}

// globalSpotifyService is the singleton Spotify service instance.
var globalSpotifyService *SpotifyService

// spotifyDefaultBaseURL is the default Spotify API base URL.
const spotifyDefaultBaseURL = "https://api.spotify.com/v1"

func newSpotifyService(cfg *Config) *SpotifyService {
	return &SpotifyService{
		cfg:     cfg,
		baseURL: spotifyDefaultBaseURL,
	}
}

// getAccessToken retrieves a valid access token from the OAuth manager.
func (s *SpotifyService) getAccessToken() (string, error) {
	if globalOAuthManager == nil {
		return "", fmt.Errorf("oauth manager not initialized")
	}
	// Use the OAuth manager's token refresh mechanism.
	// We rely on globalOAuthManager.Request() for authenticated calls,
	// but expose this for cases where raw token is needed.
	tok, err := globalOAuthManager.refreshTokenIfNeeded("spotify")
	if err != nil {
		return "", fmt.Errorf("spotify auth: %w", err)
	}
	if tok == nil || tok.AccessToken == "" {
		return "", fmt.Errorf("spotify not connected — authorize via /api/oauth/spotify/authorize")
	}
	return tok.AccessToken, nil
}

// apiRequest makes an authenticated request to the Spotify API.
func (s *SpotifyService) apiRequest(method, endpoint string, body io.Reader) ([]byte, error) {
	if globalOAuthManager == nil {
		return nil, fmt.Errorf("oauth manager not initialized")
	}

	reqURL := s.baseURL + endpoint
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	resp, err := globalOAuthManager.Request(ctx, "spotify", method, reqURL, body)
	if err != nil {
		return nil, fmt.Errorf("spotify API request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read spotify response: %w", err)
	}

	if resp.StatusCode == 204 {
		return nil, nil // No content (success for playback control)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("spotify API returned %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// apiRequestDirect makes an authenticated request without the OAuth manager,
// using a raw token. Used internally when globalOAuthManager is bypassed (e.g. tests).
func (s *SpotifyService) apiRequestDirect(method, fullURL, token string, body io.Reader) ([]byte, error) {
	req, err := http.NewRequest(method, fullURL, body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("spotify request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode == 204 {
		return nil, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("spotify API returned %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// Search searches Spotify for items matching the query.
func (s *SpotifyService) Search(query, searchType string, limit int) ([]SpotifyItem, error) {
	if query == "" {
		return nil, fmt.Errorf("search query required")
	}
	if searchType == "" {
		searchType = "track"
	}
	if limit <= 0 || limit > 50 {
		limit = 10
	}

	market := "US"
	if s.cfg != nil {
		// Try to get market from config via SpotifyConfig if available
		market = spotifyMarketFromConfig(s.cfg)
	}

	params := url.Values{}
	params.Set("q", query)
	params.Set("type", searchType)
	params.Set("limit", fmt.Sprintf("%d", limit))
	params.Set("market", market)

	data, err := s.apiRequest(http.MethodGet, "/search?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}

	return parseSearchResults(data, searchType)
}

// spotifyMarketFromConfig extracts market setting. Uses "US" as default.
func spotifyMarketFromConfig(cfg *Config) string {
	// Since we cannot reference cfg.Spotify directly (field added later),
	// we return a sensible default.
	return "US"
}

// parseSearchResults parses the Spotify search API response.
func parseSearchResults(data []byte, searchType string) ([]SpotifyItem, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse search results: %w", err)
	}

	// Spotify returns results under "{type}s" key (e.g. "tracks", "artists")
	key := searchType + "s"
	section, ok := raw[key]
	if !ok {
		return nil, fmt.Errorf("no %s in search results", key)
	}

	var container struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(section, &container); err != nil {
		return nil, fmt.Errorf("parse %s container: %w", key, err)
	}

	items := make([]SpotifyItem, 0, len(container.Items))
	for _, raw := range container.Items {
		item, err := parseSpotifyItem(raw, searchType)
		if err != nil {
			continue // skip unparseable items
		}
		items = append(items, item)
	}

	return items, nil
}

// parseSpotifyItem parses a single Spotify item from JSON.
func parseSpotifyItem(data json.RawMessage, itemType string) (SpotifyItem, error) {
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return SpotifyItem{}, err
	}

	item := SpotifyItem{
		ID:   jsonStrField(obj, "id"),
		Name: jsonStrField(obj, "name"),
		URI:  jsonStrField(obj, "uri"),
		Type: itemType,
	}

	// Extract artist(s)
	if artists, ok := obj["artists"].([]any); ok && len(artists) > 0 {
		names := make([]string, 0, len(artists))
		for _, a := range artists {
			if am, ok := a.(map[string]any); ok {
				if n, ok := am["name"].(string); ok {
					names = append(names, n)
				}
			}
		}
		item.Artist = strings.Join(names, ", ")
	}

	// Extract album
	if album, ok := obj["album"].(map[string]any); ok {
		if n, ok := album["name"].(string); ok {
			item.Album = n
		}
	}

	// Duration
	if dur, ok := obj["duration_ms"].(float64); ok {
		item.DurMS = int(dur)
	}

	// Preview URL
	if preview, ok := obj["preview_url"].(string); ok {
		item.Preview = preview
	}

	return item, nil
}

// jsonStrField safely extracts a string field from a map.
func jsonStrField(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// Play starts or resumes playback.
func (s *SpotifyService) Play(uri string, deviceID string) error {
	endpoint := "/me/player/play"
	if deviceID != "" {
		endpoint += "?device_id=" + url.QueryEscape(deviceID)
	}

	var body io.Reader
	if uri != "" {
		var payload map[string]any
		if strings.HasPrefix(uri, "spotify:track:") {
			payload = map[string]any{"uris": []string{uri}}
		} else {
			// album, artist, playlist context
			payload = map[string]any{"context_uri": uri}
		}
		data, _ := json.Marshal(payload)
		body = strings.NewReader(string(data))
	}

	_, err := s.apiRequest(http.MethodPut, endpoint, body)
	return err
}

// Pause pauses playback.
func (s *SpotifyService) Pause() error {
	_, err := s.apiRequest(http.MethodPut, "/me/player/pause", nil)
	return err
}

// Next skips to the next track.
func (s *SpotifyService) Next() error {
	_, err := s.apiRequest(http.MethodPost, "/me/player/next", nil)
	return err
}

// Previous skips to the previous track.
func (s *SpotifyService) Previous() error {
	_, err := s.apiRequest(http.MethodPost, "/me/player/previous", nil)
	return err
}

// CurrentlyPlaying returns the currently playing item.
func (s *SpotifyService) CurrentlyPlaying() (*SpotifyItem, error) {
	data, err := s.apiRequest(http.MethodGet, "/me/player/currently-playing", nil)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, nil // nothing playing
	}

	var resp struct {
		IsPlaying bool            `json:"is_playing"`
		Item      json.RawMessage `json:"item"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parse currently playing: %w", err)
	}
	if resp.Item == nil {
		return nil, nil
	}

	item, err := parseSpotifyItem(resp.Item, "track")
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// GetDevices returns available Spotify Connect devices.
func (s *SpotifyService) GetDevices() ([]SpotifyDevice, error) {
	data, err := s.apiRequest(http.MethodGet, "/me/player/devices", nil)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Devices []struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			Type          string `json:"type"`
			IsActive      bool   `json:"is_active"`
			VolumePercent int    `json:"volume_percent"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parse devices: %w", err)
	}

	devices := make([]SpotifyDevice, len(resp.Devices))
	for i, d := range resp.Devices {
		devices[i] = SpotifyDevice{
			ID:       d.ID,
			Name:     d.Name,
			Type:     d.Type,
			IsActive: d.IsActive,
			Volume:   d.VolumePercent,
		}
	}
	return devices, nil
}

// SetVolume sets the playback volume percentage (0-100).
func (s *SpotifyService) SetVolume(pct int) error {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	endpoint := fmt.Sprintf("/me/player/volume?volume_percent=%d", pct)
	_, err := s.apiRequest(http.MethodPut, endpoint, nil)
	return err
}

// GetRecommendations gets track recommendations based on seed tracks.
func (s *SpotifyService) GetRecommendations(seedTracks []string, limit int) ([]SpotifyItem, error) {
	if len(seedTracks) == 0 {
		return nil, fmt.Errorf("at least one seed track required")
	}
	if limit <= 0 || limit > 100 {
		limit = 10
	}

	// Spotify allows max 5 seed tracks
	if len(seedTracks) > 5 {
		seedTracks = seedTracks[:5]
	}

	params := url.Values{}
	params.Set("seed_tracks", strings.Join(seedTracks, ","))
	params.Set("limit", fmt.Sprintf("%d", limit))

	data, err := s.apiRequest(http.MethodGet, "/recommendations?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Tracks []json.RawMessage `json:"tracks"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parse recommendations: %w", err)
	}

	items := make([]SpotifyItem, 0, len(resp.Tracks))
	for _, raw := range resp.Tracks {
		item, err := parseSpotifyItem(raw, "track")
		if err != nil {
			continue
		}
		items = append(items, item)
	}
	return items, nil
}

// --- Tool Handlers ---

// toolSpotifyPlay handles playback control actions.
func toolSpotifyPlay(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalSpotifyService == nil {
		return "", fmt.Errorf("spotify not initialized")
	}

	var args struct {
		Action   string `json:"action"`   // play, pause, next, prev, volume
		Query    string `json:"query"`    // search query for play
		URI      string `json:"uri"`      // direct URI for play
		DeviceID string `json:"deviceId"` // target device
		Volume   int    `json:"volume"`   // volume percentage (0-100)
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	svc := globalSpotifyService

	switch args.Action {
	case "play":
		uri := args.URI
		if uri == "" && args.Query != "" {
			// Search first, then play the first result
			results, err := svc.Search(args.Query, "track", 1)
			if err != nil {
				return "", fmt.Errorf("search failed: %w", err)
			}
			if len(results) == 0 {
				return "No tracks found for query: " + args.Query, nil
			}
			uri = results[0].URI
			logInfo("spotify play search result", "query", args.Query, "track", results[0].Name, "artist", results[0].Artist)
		}
		if err := svc.Play(uri, args.DeviceID); err != nil {
			return "", fmt.Errorf("play failed: %w", err)
		}
		if uri != "" {
			return fmt.Sprintf("Now playing: %s", uri), nil
		}
		return "Playback resumed.", nil

	case "pause":
		if err := svc.Pause(); err != nil {
			return "", fmt.Errorf("pause failed: %w", err)
		}
		return "Playback paused.", nil

	case "next":
		if err := svc.Next(); err != nil {
			return "", fmt.Errorf("next failed: %w", err)
		}
		return "Skipped to next track.", nil

	case "prev", "previous":
		if err := svc.Previous(); err != nil {
			return "", fmt.Errorf("previous failed: %w", err)
		}
		return "Returned to previous track.", nil

	case "volume":
		if err := svc.SetVolume(args.Volume); err != nil {
			return "", fmt.Errorf("volume failed: %w", err)
		}
		return fmt.Sprintf("Volume set to %d%%.", args.Volume), nil

	default:
		return "", fmt.Errorf("unknown action %q — use play, pause, next, prev, or volume", args.Action)
	}
}

// toolSpotifySearch searches Spotify for items.
func toolSpotifySearch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalSpotifyService == nil {
		return "", fmt.Errorf("spotify not initialized")
	}

	var args struct {
		Query string `json:"query"`
		Type  string `json:"type"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Query == "" {
		return "", fmt.Errorf("query required")
	}
	if args.Type == "" {
		args.Type = "track"
	}
	if args.Limit <= 0 {
		args.Limit = 5
	}

	results, err := globalSpotifyService.Search(args.Query, args.Type, args.Limit)
	if err != nil {
		return "", err
	}
	if len(results) == 0 {
		return "No results found for: " + args.Query, nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Spotify search results for %q (%s):\n\n", args.Query, args.Type)
	for i, item := range results {
		fmt.Fprintf(&sb, "%d. %s", i+1, item.Name)
		if item.Artist != "" {
			fmt.Fprintf(&sb, " — %s", item.Artist)
		}
		if item.Album != "" {
			fmt.Fprintf(&sb, " [%s]", item.Album)
		}
		sb.WriteString("\n")
		fmt.Fprintf(&sb, "   URI: %s\n", item.URI)
		if item.DurMS > 0 {
			dur := time.Duration(item.DurMS) * time.Millisecond
			min := int(dur.Minutes())
			sec := int(dur.Seconds()) % 60
			fmt.Fprintf(&sb, "   Duration: %d:%02d\n", min, sec)
		}
		sb.WriteString("\n")
	}
	return sb.String(), nil
}

// toolSpotifyNowPlaying returns the currently playing track.
func toolSpotifyNowPlaying(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalSpotifyService == nil {
		return "", fmt.Errorf("spotify not initialized")
	}

	item, err := globalSpotifyService.CurrentlyPlaying()
	if err != nil {
		return "", err
	}
	if item == nil {
		return "Nothing is currently playing.", nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Now playing: %s", item.Name)
	if item.Artist != "" {
		fmt.Fprintf(&sb, " — %s", item.Artist)
	}
	if item.Album != "" {
		fmt.Fprintf(&sb, " [%s]", item.Album)
	}
	sb.WriteString("\n")
	fmt.Fprintf(&sb, "URI: %s\n", item.URI)
	if item.DurMS > 0 {
		dur := time.Duration(item.DurMS) * time.Millisecond
		min := int(dur.Minutes())
		sec := int(dur.Seconds()) % 60
		fmt.Fprintf(&sb, "Duration: %d:%02d\n", min, sec)
	}
	return sb.String(), nil
}

// toolSpotifyDevices lists available Spotify Connect devices.
func toolSpotifyDevices(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalSpotifyService == nil {
		return "", fmt.Errorf("spotify not initialized")
	}

	devices, err := globalSpotifyService.GetDevices()
	if err != nil {
		return "", err
	}
	if len(devices) == 0 {
		return "No active Spotify devices found.", nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Spotify devices (%d):\n\n", len(devices))
	for i, d := range devices {
		active := ""
		if d.IsActive {
			active = " [ACTIVE]"
		}
		fmt.Fprintf(&sb, "%d. %s (%s)%s — Volume: %d%%\n", i+1, d.Name, d.Type, active, d.Volume)
		fmt.Fprintf(&sb, "   ID: %s\n", d.ID)
	}
	return sb.String(), nil
}

// toolSpotifyRecommend gets recommendations based on current or specified tracks.
func toolSpotifyRecommend(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalSpotifyService == nil {
		return "", fmt.Errorf("spotify not initialized")
	}

	var args struct {
		TrackIDs []string `json:"trackIds"`
		Limit    int      `json:"limit"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	// If no seed tracks provided, try to use currently playing track
	if len(args.TrackIDs) == 0 {
		current, err := globalSpotifyService.CurrentlyPlaying()
		if err != nil {
			return "", fmt.Errorf("no seed tracks provided and cannot get current track: %w", err)
		}
		if current == nil {
			return "", fmt.Errorf("no seed tracks provided and nothing is playing")
		}
		args.TrackIDs = []string{current.ID}
	}
	if args.Limit <= 0 {
		args.Limit = 5
	}

	results, err := globalSpotifyService.GetRecommendations(args.TrackIDs, args.Limit)
	if err != nil {
		return "", err
	}
	if len(results) == 0 {
		return "No recommendations found.", nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Spotify recommendations (%d tracks):\n\n", len(results))
	for i, item := range results {
		fmt.Fprintf(&sb, "%d. %s", i+1, item.Name)
		if item.Artist != "" {
			fmt.Fprintf(&sb, " — %s", item.Artist)
		}
		if item.Album != "" {
			fmt.Fprintf(&sb, " [%s]", item.Album)
		}
		sb.WriteString("\n")
		fmt.Fprintf(&sb, "   URI: %s\n\n", item.URI)
	}
	return sb.String(), nil
}
