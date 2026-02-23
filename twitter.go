package main

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
)

// --- P20.3: Twitter/X Integration ---

// TwitterConfig holds Twitter/X integration settings.
type TwitterConfig struct {
	Enabled     bool   `json:"enabled"`
	RateLimit   *bool  `json:"rateLimit,omitempty"`   // respect x-rate-limit headers (default true)
	MaxTweetLen int    `json:"maxTweetLen,omitempty"`  // default 280
	DefaultUser string `json:"defaultUser,omitempty"`  // @handle for context
}

// rateLimitEnabled returns whether rate limiting is enabled (default true).
func (c TwitterConfig) rateLimitEnabled() bool {
	if c.RateLimit == nil {
		return true
	}
	return *c.RateLimit
}

// maxTweetLen returns the configured max tweet length or the default 280.
func (c TwitterConfig) maxTweetLen() int {
	if c.MaxTweetLen > 0 {
		return c.MaxTweetLen
	}
	return 280
}

// TwitterService manages Twitter API v2 interactions.
type TwitterService struct {
	cfg         *Config
	rateLimiter map[string]*twitterRateLimit
	mu          sync.Mutex
}

// twitterRateLimit tracks per-endpoint rate limit state.
type twitterRateLimit struct {
	Remaining int
	Reset     time.Time
}

// globalTwitterService is the singleton Twitter service instance.
var globalTwitterService *TwitterService

// twitterBaseURL is the Twitter API v2 base URL.
const twitterBaseURL = "https://api.twitter.com/2"

// newTwitterService creates a new TwitterService.
func newTwitterService(cfg *Config) *TwitterService {
	return &TwitterService{
		cfg:         cfg,
		rateLimiter: make(map[string]*twitterRateLimit),
	}
}

// --- Types ---

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

// TwitterUser represents a Twitter user profile.
type TwitterUser struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Username    string `json:"username"`
	Description string `json:"description,omitempty"`
	Followers   int    `json:"followers,omitempty"`
	Following   int    `json:"following,omitempty"`
	TweetCount  int    `json:"tweetCount,omitempty"`
}

// --- Rate Limiter ---

// checkRateLimit checks if a request to the given endpoint is allowed.
// Returns an error if rate limit is depleted and the reset time is in the future.
func (s *TwitterService) checkRateLimit(endpoint string) error {
	if !s.cfg.Twitter.rateLimitEnabled() {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	rl, ok := s.rateLimiter[endpoint]
	if !ok {
		return nil // no rate limit info yet
	}
	if rl.Remaining <= 0 && time.Now().Before(rl.Reset) {
		return fmt.Errorf("twitter rate limit exceeded for %s; resets at %s", endpoint, rl.Reset.Format(time.RFC3339))
	}
	return nil
}

// updateRateLimit parses rate limit headers from a Twitter API response.
func (s *TwitterService) updateRateLimit(endpoint string, resp *http.Response) {
	if !s.cfg.Twitter.rateLimitEnabled() {
		return
	}

	remaining := resp.Header.Get("x-rate-limit-remaining")
	resetStr := resp.Header.Get("x-rate-limit-reset")
	if remaining == "" && resetStr == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	rl := &twitterRateLimit{}

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

// doRequest performs a Twitter API request with rate limit handling.
func (s *TwitterService) doRequest(ctx context.Context, endpoint, method, reqURL string, body io.Reader) (*http.Response, error) {
	if globalOAuthManager == nil {
		return nil, fmt.Errorf("oauth manager not initialized; configure OAuth for twitter")
	}

	if err := s.checkRateLimit(endpoint); err != nil {
		return nil, err
	}

	resp, err := globalOAuthManager.Request(ctx, "twitter", method, reqURL, body)
	if err != nil {
		return nil, fmt.Errorf("twitter %s: %w", endpoint, err)
	}

	s.updateRateLimit(endpoint, resp)
	return resp, nil
}

// PostTweet posts a new tweet. If replyTo is non-empty, the tweet is posted as a reply.
func (s *TwitterService) PostTweet(ctx context.Context, text string, replyTo string) (*Tweet, error) {
	maxLen := s.cfg.Twitter.maxTweetLen()
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

	reqURL := twitterBaseURL + "/tweets"
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

// ReadTimeline reads the authenticated user's home timeline (reverse chronological).
func (s *TwitterService) ReadTimeline(ctx context.Context, maxResults int) ([]Tweet, error) {
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

	reqURL := fmt.Sprintf("%s/users/me/timelines/reverse_chronological?%s", twitterBaseURL, params.Encode())
	resp, err := s.doRequest(ctx, "GET /timeline", http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("twitter timeline (status %d): %s", resp.StatusCode, string(respBody))
	}

	return parseTweetsResponse(resp.Body)
}

// SearchTweets searches for recent tweets matching a query.
func (s *TwitterService) SearchTweets(ctx context.Context, query string, maxResults int) ([]Tweet, error) {
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

	reqURL := fmt.Sprintf("%s/tweets/search/recent?%s", twitterBaseURL, params.Encode())
	resp, err := s.doRequest(ctx, "GET /search", http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("twitter search (status %d): %s", resp.StatusCode, string(respBody))
	}

	return parseTweetsResponse(resp.Body)
}

// ReplyToTweet posts a reply to a specific tweet.
func (s *TwitterService) ReplyToTweet(ctx context.Context, tweetID, text string) (*Tweet, error) {
	return s.PostTweet(ctx, text, tweetID)
}

// SendDM sends a direct message to a specific user by their user ID.
func (s *TwitterService) SendDM(ctx context.Context, recipientID, text string) error {
	reqBody := map[string]string{"text": text}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal dm body: %w", err)
	}

	reqURL := fmt.Sprintf("%s/dm_conversations/with/%s/messages", twitterBaseURL, url.PathEscape(recipientID))
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
func (s *TwitterService) GetUserByUsername(ctx context.Context, username string) (*TwitterUser, error) {
	// Strip leading '@' if present.
	username = strings.TrimPrefix(username, "@")

	params := url.Values{}
	params.Set("user.fields", "public_metrics,description")

	reqURL := fmt.Sprintf("%s/users/by/username/%s?%s", twitterBaseURL, url.PathEscape(username), params.Encode())
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

	return &TwitterUser{
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

// parseTweetsResponse parses a Twitter API v2 tweets response with user expansions.
func parseTweetsResponse(body io.Reader) ([]Tweet, error) {
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

	// Build user lookup map from expansions.
	userMap := make(map[string][2]string) // id -> [name, username]
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

// --- Tool Handlers ---

// toolTweetPost posts a new tweet.
func toolTweetPost(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Text    string `json:"text"`
		ReplyTo string `json:"reply_to"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Text == "" {
		return "", fmt.Errorf("text is required")
	}

	if globalTwitterService == nil {
		return "", fmt.Errorf("twitter not configured; enable twitter in config and connect via OAuth")
	}

	tweet, err := globalTwitterService.PostTweet(ctx, args.Text, args.ReplyTo)
	if err != nil {
		return "", err
	}

	b, _ := json.Marshal(map[string]any{
		"status": "posted",
		"tweet":  tweet,
	})
	return string(b), nil
}

// toolTweetTimeline reads the home timeline.
func toolTweetTimeline(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		MaxResults int `json:"max_results"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if globalTwitterService == nil {
		return "", fmt.Errorf("twitter not configured; enable twitter in config and connect via OAuth")
	}

	tweets, err := globalTwitterService.ReadTimeline(ctx, args.MaxResults)
	if err != nil {
		return "", err
	}

	b, _ := json.Marshal(map[string]any{
		"count":  len(tweets),
		"tweets": tweets,
	})
	return string(b), nil
}

// toolTweetSearch searches recent tweets.
func toolTweetSearch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Query      string `json:"query"`
		MaxResults int    `json:"max_results"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Query == "" {
		return "", fmt.Errorf("query is required")
	}

	if globalTwitterService == nil {
		return "", fmt.Errorf("twitter not configured; enable twitter in config and connect via OAuth")
	}

	tweets, err := globalTwitterService.SearchTweets(ctx, args.Query, args.MaxResults)
	if err != nil {
		return "", err
	}

	b, _ := json.Marshal(map[string]any{
		"count":  len(tweets),
		"tweets": tweets,
	})
	return string(b), nil
}

// toolTweetReply replies to a specific tweet.
func toolTweetReply(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		TweetID string `json:"tweet_id"`
		Text    string `json:"text"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.TweetID == "" {
		return "", fmt.Errorf("tweet_id is required")
	}
	if args.Text == "" {
		return "", fmt.Errorf("text is required")
	}

	if globalTwitterService == nil {
		return "", fmt.Errorf("twitter not configured; enable twitter in config and connect via OAuth")
	}

	tweet, err := globalTwitterService.ReplyToTweet(ctx, args.TweetID, args.Text)
	if err != nil {
		return "", err
	}

	b, _ := json.Marshal(map[string]any{
		"status": "replied",
		"tweet":  tweet,
	})
	return string(b), nil
}

// toolTweetDM sends a direct message to a user.
func toolTweetDM(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		RecipientID string `json:"recipient_id"`
		Text        string `json:"text"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.RecipientID == "" {
		return "", fmt.Errorf("recipient_id is required")
	}
	if args.Text == "" {
		return "", fmt.Errorf("text is required")
	}

	if globalTwitterService == nil {
		return "", fmt.Errorf("twitter not configured; enable twitter in config and connect via OAuth")
	}

	if err := globalTwitterService.SendDM(ctx, args.RecipientID, args.Text); err != nil {
		return "", err
	}

	return `{"status":"dm_sent"}`, nil
}
