package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// TetoraVersion is set by main.go before calling any CLI function.
var TetoraVersion = "dev"

// FindConfigPath discovers the config.json file path.
// Resolution order:
//  1. <executable>/../config.json (Homebrew layout)
//  2. ~/.tetora/config.json
//  3. config.json (current directory)
func FindConfigPath() string {
	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "..", "config.json")
		if abs, err := filepath.Abs(candidate); err == nil {
			if _, err := os.Stat(abs); err == nil {
				return abs
			}
		}
	}
	home, _ := os.UserHomeDir()
	candidate := filepath.Join(home, ".tetora", "config.json")
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return "config.json"
}

// --- API Client ---

// APIClient creates an HTTP client for daemon communication.
type APIClient struct {
	Client   *http.Client
	BaseURL  string
	Token    string
	ClientID string // X-Client-ID header value; empty means omit header (server uses default)
	SubAgent bool   // if true, adds X-Tetora-Source: agent_dispatch header
}

// NewAPIClient creates an API client from config values.
func NewAPIClient(listenAddr, token string) *APIClient {
	return &APIClient{
		Client:  &http.Client{Timeout: 5 * time.Second},
		BaseURL: fmt.Sprintf("http://%s", listenAddr),
		Token:   token,
	}
}

func (c *APIClient) Do(method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, c.BaseURL+path, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if c.ClientID != "" {
		req.Header.Set("X-Client-ID", c.ClientID)
	}
	if c.SubAgent {
		req.Header.Set("X-Tetora-Source", "agent_dispatch")
	}
	return c.Client.Do(req)
}

func (c *APIClient) Get(path string) (*http.Response, error) {
	return c.Do("GET", path, nil)
}

func (c *APIClient) Post(path string, body string) (*http.Response, error) {
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	return c.Do("POST", path, r)
}

func (c *APIClient) PostJSON(path string, v any) (*http.Response, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return c.Do("POST", path, strings.NewReader(string(b)))
}

// --- Format Helpers ---

func JSONFloatSafe(v any) float64 {
	if f, ok := v.(float64); ok {
		return f
	}
	return 0
}

func JSONStrSafe(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func FormatTimeAgo(rfc3339 string) string {
	t, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return rfc3339
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func FormatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh%dm", h, m)
}

func JoinStrings(ss []string, sep string) string {
	if len(ss) == 0 {
		return ""
	}
	result := ss[0]
	for _, s := range ss[1:] {
		result += sep + s
	}
	return result
}
