package twitter

import (
	"strings"
	"testing"
)

// --- Config.RateLimitEnabled ---

func TestRateLimitEnabled_Default(t *testing.T) {
	cfg := Config{}
	if !cfg.RateLimitEnabled() {
		t.Error("RateLimitEnabled() = false, want true when RateLimit is nil")
	}
}

func TestRateLimitEnabled_ExplicitTrue(t *testing.T) {
	v := true
	cfg := Config{RateLimit: &v}
	if !cfg.RateLimitEnabled() {
		t.Error("RateLimitEnabled() = false, want true")
	}
}

func TestRateLimitEnabled_ExplicitFalse(t *testing.T) {
	v := false
	cfg := Config{RateLimit: &v}
	if cfg.RateLimitEnabled() {
		t.Error("RateLimitEnabled() = true, want false")
	}
}

// --- Config.MaxTweetLength ---

func TestMaxTweetLength_Default(t *testing.T) {
	cfg := Config{}
	if got := cfg.MaxTweetLength(); got != 280 {
		t.Errorf("MaxTweetLength() = %d, want 280", got)
	}
}

func TestMaxTweetLength_Custom(t *testing.T) {
	cfg := Config{MaxTweetLen: 140}
	if got := cfg.MaxTweetLength(); got != 140 {
		t.Errorf("MaxTweetLength() = %d, want 140", got)
	}
}

func TestMaxTweetLength_ZeroFallsToDefault(t *testing.T) {
	cfg := Config{MaxTweetLen: 0}
	if got := cfg.MaxTweetLength(); got != 280 {
		t.Errorf("MaxTweetLength() = %d, want 280 for zero value", got)
	}
}

// --- ParseTweetsResponse ---

func TestParseTweetsResponse_WithUserExpansions(t *testing.T) {
	body := strings.NewReader(`{
		"data": [
			{
				"id": "1001",
				"text": "Hello world",
				"author_id": "u1",
				"created_at": "2024-01-01T00:00:00Z",
				"public_metrics": {
					"like_count": 10,
					"retweet_count": 3,
					"reply_count": 1
				}
			},
			{
				"id": "1002",
				"text": "Another tweet",
				"author_id": "u2",
				"created_at": "2024-01-02T00:00:00Z",
				"public_metrics": {
					"like_count": 5,
					"retweet_count": 0,
					"reply_count": 2
				}
			}
		],
		"includes": {
			"users": [
				{"id": "u1", "name": "Alice", "username": "alice"},
				{"id": "u2", "name": "Bob",   "username": "bob"}
			]
		}
	}`)

	tweets, err := ParseTweetsResponse(body)
	if err != nil {
		t.Fatalf("ParseTweetsResponse() error: %v", err)
	}
	if len(tweets) != 2 {
		t.Fatalf("len(tweets) = %d, want 2", len(tweets))
	}

	t0 := tweets[0]
	if t0.ID != "1001" {
		t.Errorf("tweets[0].ID = %q, want %q", t0.ID, "1001")
	}
	if t0.Text != "Hello world" {
		t.Errorf("tweets[0].Text = %q, want %q", t0.Text, "Hello world")
	}
	if t0.AuthorID != "u1" {
		t.Errorf("tweets[0].AuthorID = %q, want %q", t0.AuthorID, "u1")
	}
	if t0.AuthorName != "Alice" {
		t.Errorf("tweets[0].AuthorName = %q, want %q", t0.AuthorName, "Alice")
	}
	if t0.AuthorHandle != "alice" {
		t.Errorf("tweets[0].AuthorHandle = %q, want %q", t0.AuthorHandle, "alice")
	}
	if t0.Likes != 10 {
		t.Errorf("tweets[0].Likes = %d, want 10", t0.Likes)
	}
	if t0.Retweets != 3 {
		t.Errorf("tweets[0].Retweets = %d, want 3", t0.Retweets)
	}
	if t0.Replies != 1 {
		t.Errorf("tweets[0].Replies = %d, want 1", t0.Replies)
	}

	t1 := tweets[1]
	if t1.AuthorName != "Bob" {
		t.Errorf("tweets[1].AuthorName = %q, want %q", t1.AuthorName, "Bob")
	}
	if t1.AuthorHandle != "bob" {
		t.Errorf("tweets[1].AuthorHandle = %q, want %q", t1.AuthorHandle, "bob")
	}
}

func TestParseTweetsResponse_AuthorNotInExpansions(t *testing.T) {
	body := strings.NewReader(`{
		"data": [
			{
				"id":        "2001",
				"text":      "Orphan tweet",
				"author_id": "unknown",
				"public_metrics": {"like_count": 0, "retweet_count": 0, "reply_count": 0}
			}
		],
		"includes": {"users": []}
	}`)

	tweets, err := ParseTweetsResponse(body)
	if err != nil {
		t.Fatalf("ParseTweetsResponse() error: %v", err)
	}
	if len(tweets) != 1 {
		t.Fatalf("len(tweets) = %d, want 1", len(tweets))
	}

	if tweets[0].AuthorName != "" {
		t.Errorf("AuthorName = %q, want empty when user not in expansions", tweets[0].AuthorName)
	}
	if tweets[0].AuthorHandle != "" {
		t.Errorf("AuthorHandle = %q, want empty when user not in expansions", tweets[0].AuthorHandle)
	}
}

func TestParseTweetsResponse_EmptyData(t *testing.T) {
	body := strings.NewReader(`{"data": [], "includes": {"users": []}}`)

	tweets, err := ParseTweetsResponse(body)
	if err != nil {
		t.Fatalf("ParseTweetsResponse() error: %v", err)
	}
	if len(tweets) != 0 {
		t.Errorf("len(tweets) = %d, want 0", len(tweets))
	}
}

func TestParseTweetsResponse_NoIncludesField(t *testing.T) {
	body := strings.NewReader(`{
		"data": [
			{
				"id": "3001",
				"text": "No includes",
				"author_id": "u9",
				"public_metrics": {"like_count": 1, "retweet_count": 0, "reply_count": 0}
			}
		]
	}`)

	tweets, err := ParseTweetsResponse(body)
	if err != nil {
		t.Fatalf("ParseTweetsResponse() error: %v", err)
	}
	if len(tweets) != 1 {
		t.Fatalf("len(tweets) = %d, want 1", len(tweets))
	}
	if tweets[0].ID != "3001" {
		t.Errorf("ID = %q, want %q", tweets[0].ID, "3001")
	}
}

func TestParseTweetsResponse_InvalidJSON(t *testing.T) {
	_, err := ParseTweetsResponse(strings.NewReader(`not json`))
	if err == nil {
		t.Error("ParseTweetsResponse() expected error for invalid JSON, got nil")
	}
}
