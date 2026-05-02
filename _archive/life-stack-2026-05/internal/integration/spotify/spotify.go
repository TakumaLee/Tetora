package spotify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"tetora/internal/integration/oauthif"
)

// Config holds Spotify integration settings.
type Config struct {
	Enabled      bool   `json:"enabled"`
	ClientID     string `json:"clientId,omitempty"`
	ClientSecret string `json:"clientSecret,omitempty"`
	Market       string `json:"market,omitempty"`
}

func (c Config) MarketOrDefault() string {
	if c.Market != "" {
		return c.Market
	}
	return "US"
}

// Item represents a Spotify track, album, artist, or playlist.
type Item struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	URI     string `json:"uri"`
	Type    string `json:"type"`
	Artist  string `json:"artist,omitempty"`
	Album   string `json:"album,omitempty"`
	DurMS   int    `json:"durationMs,omitempty"`
	Preview string `json:"previewUrl,omitempty"`
}

// Device represents a Spotify Connect device.
type Device struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	IsActive bool   `json:"isActive"`
	Volume   int    `json:"volumePercent"`
}

// Service manages Spotify API interactions.
type Service struct {
	cfg     Config
	oauth   oauthif.TokenProvider
	BaseURL string // overridable for tests
}

// DefaultBaseURL is the default Spotify API base URL.
const DefaultBaseURL = "https://api.spotify.com/v1"

// New creates a new Spotify Service.
func New(cfg Config, oauth oauthif.TokenProvider) *Service {
	return &Service{
		cfg:     cfg,
		oauth:   oauth,
		BaseURL: DefaultBaseURL,
	}
}

// GetAccessToken retrieves a valid access token from the OAuth manager.
func (s *Service) GetAccessToken() (string, error) {
	if s.oauth == nil {
		return "", fmt.Errorf("oauth manager not initialized")
	}
	return s.oauth.RefreshTokenIfNeeded("spotify")
}

// apiRequest makes an authenticated request to the Spotify API.
func (s *Service) apiRequest(method, endpoint string, body io.Reader) ([]byte, error) {
	if s.oauth == nil {
		return nil, fmt.Errorf("oauth manager not initialized")
	}

	reqURL := s.BaseURL + endpoint
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	resp, err := s.oauth.Request(ctx, "spotify", method, reqURL, body)
	if err != nil {
		return nil, fmt.Errorf("spotify API request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read spotify response: %w", err)
	}

	if resp.StatusCode == 204 {
		return nil, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("spotify API returned %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// APIRequestDirect makes an authenticated request without the OAuth manager,
// using a raw token. Used internally when oauth is bypassed (e.g. tests).
func (s *Service) APIRequestDirect(method, fullURL, token string, body io.Reader) ([]byte, error) {
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
func (s *Service) Search(query, searchType string, limit int) ([]Item, error) {
	if query == "" {
		return nil, fmt.Errorf("search query required")
	}
	if searchType == "" {
		searchType = "track"
	}
	if limit <= 0 || limit > 50 {
		limit = 10
	}

	market := s.cfg.MarketOrDefault()

	params := url.Values{}
	params.Set("q", query)
	params.Set("type", searchType)
	params.Set("limit", fmt.Sprintf("%d", limit))
	params.Set("market", market)

	data, err := s.apiRequest(http.MethodGet, "/search?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}

	return ParseSearchResults(data, searchType)
}

// ParseSearchResults parses the Spotify search API response.
func ParseSearchResults(data []byte, searchType string) ([]Item, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse search results: %w", err)
	}

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

	items := make([]Item, 0, len(container.Items))
	for _, raw := range container.Items {
		item, err := ParseItem(raw, searchType)
		if err != nil {
			continue
		}
		items = append(items, item)
	}

	return items, nil
}

// ParseItem parses a single Spotify item from JSON.
func ParseItem(data json.RawMessage, itemType string) (Item, error) {
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return Item{}, err
	}

	item := Item{
		ID:   JSONStrField(obj, "id"),
		Name: JSONStrField(obj, "name"),
		URI:  JSONStrField(obj, "uri"),
		Type: itemType,
	}

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

	if album, ok := obj["album"].(map[string]any); ok {
		if n, ok := album["name"].(string); ok {
			item.Album = n
		}
	}

	if dur, ok := obj["duration_ms"].(float64); ok {
		item.DurMS = int(dur)
	}

	if preview, ok := obj["preview_url"].(string); ok {
		item.Preview = preview
	}

	return item, nil
}

// JSONStrField safely extracts a string field from a map.
func JSONStrField(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// Play starts or resumes playback.
func (s *Service) Play(uri string, deviceID string) error {
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
			payload = map[string]any{"context_uri": uri}
		}
		data, _ := json.Marshal(payload)
		body = strings.NewReader(string(data))
	}

	_, err := s.apiRequest(http.MethodPut, endpoint, body)
	return err
}

// Pause pauses playback.
func (s *Service) Pause() error {
	_, err := s.apiRequest(http.MethodPut, "/me/player/pause", nil)
	return err
}

// Next skips to the next track.
func (s *Service) Next() error {
	_, err := s.apiRequest(http.MethodPost, "/me/player/next", nil)
	return err
}

// Previous skips to the previous track.
func (s *Service) Previous() error {
	_, err := s.apiRequest(http.MethodPost, "/me/player/previous", nil)
	return err
}

// CurrentlyPlaying returns the currently playing item.
func (s *Service) CurrentlyPlaying() (*Item, error) {
	data, err := s.apiRequest(http.MethodGet, "/me/player/currently-playing", nil)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, nil
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

	item, err := ParseItem(resp.Item, "track")
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// GetDevices returns available Spotify Connect devices.
func (s *Service) GetDevices() ([]Device, error) {
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

	devices := make([]Device, len(resp.Devices))
	for i, d := range resp.Devices {
		devices[i] = Device{
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
func (s *Service) SetVolume(pct int) error {
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
func (s *Service) GetRecommendations(seedTracks []string, limit int) ([]Item, error) {
	if len(seedTracks) == 0 {
		return nil, fmt.Errorf("at least one seed track required")
	}
	if limit <= 0 || limit > 100 {
		limit = 10
	}

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

	items := make([]Item, 0, len(resp.Tracks))
	for _, raw := range resp.Tracks {
		item, err := ParseItem(raw, "track")
		if err != nil {
			continue
		}
		items = append(items, item)
	}
	return items, nil
}
