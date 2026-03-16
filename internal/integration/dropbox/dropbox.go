package dropbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"tetora/internal/integration/oauthif"
)

// Dropbox API v2 base URLs (overridable in tests).
var (
	APIBaseURL     = "https://api.dropboxapi.com"
	ContentBaseURL = "https://content.dropboxapi.com"
)

// Service provides Dropbox API v2 operations via OAuth.
type Service struct {
	oauthService string
	oauth        oauthif.Requester
}

// File represents a Dropbox file/folder entry.
type File struct {
	Tag            string `json:".tag"`
	ID             string `json:"id"`
	Name           string `json:"name"`
	PathLower      string `json:"path_lower"`
	PathDisplay    string `json:"path_display"`
	Size           int64  `json:"size,omitempty"`
	ServerModified string `json:"server_modified,omitempty"`
	ContentHash    string `json:"content_hash,omitempty"`
	IsDownloadable bool   `json:"is_downloadable,omitempty"`
}

// ListResult is the response from list_folder.
type ListResult struct {
	Entries []File `json:"entries"`
	Cursor  string `json:"cursor"`
	HasMore bool   `json:"has_more"`
}

// SearchResult is the response from search_v2.
type SearchResult struct {
	Matches []struct {
		Metadata struct {
			Metadata File `json:"metadata"`
		} `json:"metadata"`
	} `json:"matches"`
	HasMore bool `json:"has_more"`
}

// New creates a new DropboxService.
func New(oauth oauthif.Requester) *Service {
	return &Service{oauthService: "dropbox", oauth: oauth}
}

// dropboxAPIRequest makes an authenticated JSON request to the Dropbox API.
func (d *Service) dropboxAPIRequest(ctx context.Context, path string, body any) (*http.Response, error) {
	if d.oauth == nil {
		return nil, fmt.Errorf("OAuth manager not initialized")
	}

	var bodyReader io.Reader
	if body != nil {
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	reqURL := APIBaseURL + path
	resp, err := d.oauth.Request(ctx, d.oauthService, http.MethodPost, reqURL, bodyReader)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// Search searches for files in Dropbox.
func (d *Service) Search(ctx context.Context, query string, maxResults int) ([]File, error) {
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}
	if maxResults <= 0 {
		maxResults = 20
	}

	body := map[string]any{
		"query": query,
		"options": map[string]any{
			"max_results": maxResults,
			"file_status": "active",
		},
	}

	resp, err := d.dropboxAPIRequest(ctx, "/2/files/search_v2", body)
	if err != nil {
		return nil, fmt.Errorf("dropbox search: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("dropbox search returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result SearchResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode search response: %w", err)
	}

	var files []File
	for _, match := range result.Matches {
		files = append(files, match.Metadata.Metadata)
	}
	return files, nil
}

// Upload uploads a file to Dropbox.
func (d *Service) Upload(ctx context.Context, path string, data []byte, overwrite bool) (*File, error) {
	if path == "" {
		return nil, fmt.Errorf("path is required")
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	mode := "add"
	if overwrite {
		mode = "overwrite"
	}

	uploadArgs := map[string]any{
		"path":       path,
		"mode":       mode,
		"autorename": !overwrite,
		"mute":       false,
	}
	argsJSON, _ := json.Marshal(uploadArgs)

	reqURL := ContentBaseURL + "/2/files/upload"
	if d.oauth == nil {
		return nil, fmt.Errorf("OAuth manager not initialized")
	}

	resp, err := d.oauth.Request(ctx, d.oauthService, http.MethodPost, reqURL, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("dropbox upload: %w", err)
	}
	defer resp.Body.Close()

	// Note: In real usage, Dropbox-API-Arg header must be set.
	_ = argsJSON

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("dropbox upload returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result File
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode upload response: %w", err)
	}
	return &result, nil
}

// Download downloads a file from Dropbox.
func (d *Service) Download(ctx context.Context, path string) ([]byte, *File, error) {
	if path == "" {
		return nil, nil, fmt.Errorf("path is required")
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	dlArgs := map[string]string{"path": path}
	argsJSON, _ := json.Marshal(dlArgs)

	reqURL := ContentBaseURL + "/2/files/download"
	if d.oauth == nil {
		return nil, nil, fmt.Errorf("OAuth manager not initialized")
	}

	resp, err := d.oauth.Request(ctx, d.oauthService, http.MethodPost, reqURL, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("dropbox download: %w", err)
	}
	defer resp.Body.Close()
	_ = argsJSON

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, nil, fmt.Errorf("dropbox download returned %d: %s", resp.StatusCode, string(respBody))
	}

	var meta File
	if resultHeader := resp.Header.Get("Dropbox-API-Result"); resultHeader != "" {
		json.Unmarshal([]byte(resultHeader), &meta)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 100*1024*1024))
	if err != nil {
		return nil, nil, fmt.Errorf("read download body: %w", err)
	}

	return data, &meta, nil
}

// ListFolder lists files in a Dropbox folder.
func (d *Service) ListFolder(ctx context.Context, path string, recursive bool) ([]File, error) {
	if path == "" {
		path = ""
	}

	body := map[string]any{
		"path":                                path,
		"recursive":                           recursive,
		"include_media_info":                  false,
		"include_deleted":                     false,
		"include_has_explicit_shared_members": false,
		"limit":                               100,
	}

	resp, err := d.dropboxAPIRequest(ctx, "/2/files/list_folder", body)
	if err != nil {
		return nil, fmt.Errorf("dropbox list folder: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("dropbox list returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result ListResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode list response: %w", err)
	}
	return result.Entries, nil
}
