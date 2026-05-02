package twitter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"tetora/internal/integration/oauthif"
)

// Config holds Twitter/X integration settings.
type Config struct {
	Enabled     bool   `json:"enabled"`
	RateLimit   *bool  `json:"rateLimit,omitempty"`
	MaxTweetLen int    `json:"maxTweetLen,omitempty"`
	DefaultUser string `json:"defaultUser,omitempty"`
}

// RateLimitEnabled returns whether rate limiting is enabled (default true).
func (c Config) RateLimitEnabled() bool {
	if c.RateLimit == nil {
		return true
	}
	return *c.RateLimit
}

// MaxTweetLength returns the configured max tweet length or the default 280.
func (c Config) MaxTweetLength() int {
	if c.MaxTweetLen > 0 {
		return c.MaxTweetLen
	}
	return 280
}

// Service manages Twitter API v2 interactions.
type Service struct {
	cfg         Config
	oauth       oauthif.TokenProvider
	rateLimiter map[string]*rateLimit
	mu          sync.Mutex
}

// rateLimit tracks per-endpoint rate limit state.
type rateLimit struct {
	Remaining int
	Reset     time.Time
}

// BaseURL is the Twitter API v2 base URL.
var BaseURL = "https://api.twitter.com/2"

// Tweet represents a tweet from the Twitter API v2.
type Tweet struct {
	ID           string `json:"id"`
	Text         string `json:"text"`
	AuthorID     string `json:"authorId,omitempty"`
	AuthorName   string `json:"authorName,omitempty"`
	AuthorHandle string `json:"authorHandle,omitempty"`
	CreatedAt    string `json:"createdAt,omitempty"`
	Likes        int    `json:"likes,omitempty"`
	Retweets     int    `json:"retweets,omitempty"`
	Replies      int    `json:"replies,omitempty"`
}

// User represents a Twitter user profile.
type User struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Username    string `json:"username"`
	Description string `json:"description,omitempty"`
	Followers   int    `json:"followers,omitempty"`
	Following   int    `json:"following,omitempty"`
	TweetCount  int    `json:"tweetCount,omitempty"`
}

// New creates a new TwitterService.
func New(cfg Config, oauth oauthif.TokenProvider) *Service {
	return &Service{
		cfg:         cfg,
		oauth:       oauth,
		rateLimiter: make(map[string]*rateLimit),
	}
}

// --- Rate Limiter ---

func (s *Service) checkRateLimit(endpoint string) error {
	if !s.cfg.RateLimitEnabled() {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	rl, ok := s.rateLimiter[endpoint]
	if !ok {
		return nil
	}
	if rl.Remaining <= 0 && time.Now().Before(rl.Reset) {
		return fmt.Errorf("twitter rate limit exceeded for %s; resets at %s", endpoint, rl.Reset.Format(time.RFC3339))
	}
	return nil
}

func (s *Service) updateRateLimit(endpoint string, resp *http.Response) {
	if !s.cfg.RateLimitEnabled() {
		return
	}

	remaining := resp.Header.Get("x-rate-limit-remaining")
	resetStr := resp.Header.Get("x-rate-limit-reset")
	if remaining == "" && resetStr == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	rl := &rateLimit{}

	if remaining != "" {
		if n, err := strconv.Atoi(remaining); err == nil {
			rl.Remaining = n
		}
	}
	if resetStr != "" {
		if epoch, err := strconv.ParseInt(resetStr, 10, 64); err == nil {
			rl.Reset = time.Unix(epoch, 0)
		}
	}
	s.rateLimiter[endpoint] = rl
}

// --- API Methods ---

func (s *Service) doRequest(ctx context.Context, endpoint, method, reqURL string, body io.Reader) (*http.Response, error) {
	if s.oauth == nil {
		return nil, fmt.Errorf("oauth manager not initialized; configure OAuth for twitter")
	}

	if err := s.checkRateLimit(endpoint); err != nil {
		return nil, err
	}

	resp, err := s.oauth.Request(ctx, "twitter", method, reqURL, body)
	if err != nil {
		return nil, fmt.Errorf("twitter %s: %w", endpoint, err)
	}

	s.updateRateLimit(endpoint, resp)
	return resp, nil
}

// PostTweet posts a new tweet.
func (s *Service) PostTweet(ctx context.Context, text string, replyTo string) (*Tweet, error) {
	maxLen := s.cfg.MaxTweetLength()
	if len([]rune(text)) > maxLen {
		return nil, fmt.Errorf("tweet text exceeds maximum length of %d characters", maxLen)
	}

	reqBody := map[string]any{"text": text}
	if replyTo != "" {
		reqBody["reply"] = map[string]string{"in_reply_to_tweet_id": replyTo}
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal tweet body: %w", err)
	}

	reqURL := BaseURL + "/tweets"
	resp, err := s.doRequest(ctx, "POST /tweets", http.MethodPost, reqURL, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("twitter post tweet (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Data struct {
			ID   string `json:"id"`
			Text string `json:"text"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode tweet response: %w", err)
	}

	return &Tweet{
		ID:   result.Data.ID,
		Text: result.Data.Text,
	}, nil
}

// ReadTimeline reads the authenticated user's home timeline.
func (s *Service) ReadTimeline(ctx context.Context, maxResults int) ([]Tweet, error) {
	if maxResults <= 0 {
		maxResults = 10
	}
	if maxResults > 100 {
		maxResults = 100
	}

	params := url.Values{}
	params.Set("max_results", strconv.Itoa(maxResults))
	params.Set("tweet.fields", "created_at,public_metrics,author_id")
	params.Set("expansions", "author_id")
	params.Set("user.fields", "username,name")

	reqURL := fmt.Sprintf("%s/users/me/timelines/reverse_chronological?%s", BaseURL, params.Encode())
	resp, err := s.doRequest(ctx, "GET /timeline", http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("twitter timeline (status %d): %s", resp.StatusCode, string(respBody))
	}

	return ParseTweetsResponse(resp.Body)
}

// SearchTweets searches for recent tweets matching a query.
func (s *Service) SearchTweets(ctx context.Context, query string, maxResults int) ([]Tweet, error) {
	if maxResults <= 0 {
		maxResults = 10
	}
	if maxResults > 100 {
		maxResults = 100
	}

	params := url.Values{}
	params.Set("query", query)
	params.Set("max_results", strconv.Itoa(maxResults))
	params.Set("tweet.fields", "created_at,public_metrics,author_id")
	params.Set("expansions", "author_id")
	params.Set("user.fields", "username,name")

	reqURL := fmt.Sprintf("%s/tweets/search/recent?%s", BaseURL, params.Encode())
	resp, err := s.doRequest(ctx, "GET /search", http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("twitter search (status %d): %s", resp.StatusCode, string(respBody))
	}

	return ParseTweetsResponse(resp.Body)
}

// ReplyToTweet posts a reply to a specific tweet.
func (s *Service) ReplyToTweet(ctx context.Context, tweetID, text string) (*Tweet, error) {
	return s.PostTweet(ctx, text, tweetID)
}

// SendDM sends a direct message to a specific user by their user ID.
func (s *Service) SendDM(ctx context.Context, recipientID, text string) error {
	reqBody := map[string]string{"text": text}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal dm body: %w", err)
	}

	reqURL := fmt.Sprintf("%s/dm_conversations/with/%s/messages", BaseURL, url.PathEscape(recipientID))
	resp, err := s.doRequest(ctx, "POST /dm", http.MethodPost, reqURL, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("twitter send dm (status %d): %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// GetUserByUsername looks up a Twitter user by their @username.
func (s *Service) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	username = strings.TrimPrefix(username, "@")

	params := url.Values{}
	params.Set("user.fields", "public_metrics,description")

	reqURL := fmt.Sprintf("%s/users/by/username/%s?%s", BaseURL, url.PathEscape(username), params.Encode())
	resp, err := s.doRequest(ctx, "GET /users/by/username", http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("twitter get user (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Data struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			Username      string `json:"username"`
			Description   string `json:"description"`
			PublicMetrics struct {
				Followers  int `json:"followers_count"`
				Following  int `json:"following_count"`
				TweetCount int `json:"tweet_count"`
			} `json:"public_metrics"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode user response: %w", err)
	}

	return &User{
		ID:          result.Data.ID,
		Name:        result.Data.Name,
		Username:    result.Data.Username,
		Description: result.Data.Description,
		Followers:   result.Data.PublicMetrics.Followers,
		Following:   result.Data.PublicMetrics.Following,
		TweetCount:  result.Data.PublicMetrics.TweetCount,
	}, nil
}

// --- Response Parsing ---

// ParseTweetsResponse parses a Twitter API v2 tweets response with user expansions.
func ParseTweetsResponse(body io.Reader) ([]Tweet, error) {
	var resp struct {
		Data []struct {
			ID            string `json:"id"`
			Text          string `json:"text"`
			AuthorID      string `json:"author_id"`
			CreatedAt     string `json:"created_at"`
			PublicMetrics struct {
				Likes    int `json:"like_count"`
				Retweets int `json:"retweet_count"`
				Replies  int `json:"reply_count"`
			} `json:"public_metrics"`
		} `json:"data"`
		Includes struct {
			Users []struct {
				ID       string `json:"id"`
				Name     string `json:"name"`
				Username string `json:"username"`
			} `json:"users"`
		} `json:"includes"`
	}

	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode tweets response: %w", err)
	}

	userMap := make(map[string][2]string)
	for _, u := range resp.Includes.Users {
		userMap[u.ID] = [2]string{u.Name, u.Username}
	}

	tweets := make([]Tweet, 0, len(resp.Data))
	for _, d := range resp.Data {
		t := Tweet{
			ID:        d.ID,
			Text:      d.Text,
			AuthorID:  d.AuthorID,
			CreatedAt: d.CreatedAt,
			Likes:     d.PublicMetrics.Likes,
			Retweets:  d.PublicMetrics.Retweets,
			Replies:   d.PublicMetrics.Replies,
		}
		if info, ok := userMap[d.AuthorID]; ok {
			t.AuthorName = info[0]
			t.AuthorHandle = info[1]
		}
		tweets = append(tweets, t)
	}

	return tweets, nil
}
