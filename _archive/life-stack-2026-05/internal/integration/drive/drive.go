package drive

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"

	"tetora/internal/integration/oauthif"
)

// BaseURL is the Google Drive v3 API base (overridable in tests).
var BaseURL = "https://www.googleapis.com"

// Service provides Google Drive v3 operations via OAuth.
type Service struct {
	oauthService string
	oauth        oauthif.Requester
}

// File represents a Google Drive file.
type File struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	MimeType     string   `json:"mimeType"`
	Size         string   `json:"size,omitempty"`
	CreatedTime  string   `json:"createdTime,omitempty"`
	ModifiedTime string   `json:"modifiedTime,omitempty"`
	WebViewLink  string   `json:"webViewLink,omitempty"`
	Parents      []string `json:"parents,omitempty"`
}

// FileList is a paginated list of Drive files.
type FileList struct {
	Files         []File `json:"files"`
	NextPageToken string `json:"nextPageToken,omitempty"`
}

// New creates a new DriveService.
func New(oauth oauthif.Requester) *Service {
	return &Service{oauthService: "google", oauth: oauth}
}

// driveRequest makes an authenticated request to the Drive API.
func (d *Service) driveRequest(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	if d.oauth == nil {
		return nil, fmt.Errorf("OAuth manager not initialized")
	}
	reqURL := BaseURL + path
	return d.oauth.Request(ctx, d.oauthService, method, reqURL, body)
}

// Search searches for files in Google Drive.
func (d *Service) Search(ctx context.Context, query string, maxResults int) ([]File, error) {
	if maxResults <= 0 {
		maxResults = 20
	}
	if maxResults > 100 {
		maxResults = 100
	}

	q := url.QueryEscape(query)
	fields := "files(id,name,mimeType,size,createdTime,modifiedTime,webViewLink,parents)"
	apiPath := fmt.Sprintf("/drive/v3/files?q=name+contains+'%s'&fields=%s&pageSize=%d&orderBy=modifiedTime+desc",
		q, url.QueryEscape(fields), maxResults)

	resp, err := d.driveRequest(ctx, http.MethodGet, apiPath, nil)
	if err != nil {
		return nil, fmt.Errorf("drive search: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("drive search returned %d: %s", resp.StatusCode, string(body))
	}

	var result FileList
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode drive response: %w", err)
	}
	return result.Files, nil
}

// Upload uploads a file to Google Drive.
func (d *Service) Upload(ctx context.Context, name, mimeType, parentID string, data []byte) (*File, error) {
	if name == "" {
		return nil, fmt.Errorf("file name is required")
	}

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	metaHeader := make(textproto.MIMEHeader)
	metaHeader.Set("Content-Type", "application/json; charset=UTF-8")

	metaPart, err := writer.CreatePart(metaHeader)
	if err != nil {
		return nil, fmt.Errorf("create meta part: %w", err)
	}
	meta := map[string]any{"name": name}
	if mimeType != "" {
		meta["mimeType"] = mimeType
	}
	if parentID != "" {
		meta["parents"] = []string{parentID}
	}
	json.NewEncoder(metaPart).Encode(meta)

	fileHeader := make(textproto.MIMEHeader)
	if mimeType != "" {
		fileHeader.Set("Content-Type", mimeType)
	} else {
		fileHeader.Set("Content-Type", "application/octet-stream")
	}
	filePart, err := writer.CreatePart(fileHeader)
	if err != nil {
		return nil, fmt.Errorf("create file part: %w", err)
	}
	filePart.Write(data)
	writer.Close()

	apiPath := "/upload/drive/v3/files?uploadType=multipart&fields=id,name,mimeType,size,createdTime,modifiedTime,webViewLink"

	reqURL := BaseURL + apiPath
	resp, err := d.oauth.Request(ctx, d.oauthService, http.MethodPost, reqURL, &buf)
	if err != nil {
		return nil, fmt.Errorf("drive upload: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("drive upload returned %d: %s", resp.StatusCode, string(body))
	}

	var result File
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode upload response: %w", err)
	}
	return &result, nil
}

// Download downloads a file from Google Drive by ID.
func (d *Service) Download(ctx context.Context, fileID string) ([]byte, *File, error) {
	if fileID == "" {
		return nil, nil, fmt.Errorf("file ID is required")
	}

	metaPath := fmt.Sprintf("/drive/v3/files/%s?fields=id,name,mimeType,size", url.PathEscape(fileID))
	metaResp, err := d.driveRequest(ctx, http.MethodGet, metaPath, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("drive get metadata: %w", err)
	}
	defer metaResp.Body.Close()

	if metaResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(metaResp.Body)
		return nil, nil, fmt.Errorf("drive metadata returned %d: %s", metaResp.StatusCode, string(body))
	}

	var fileMeta File
	if err := json.NewDecoder(metaResp.Body).Decode(&fileMeta); err != nil {
		return nil, nil, fmt.Errorf("decode metadata: %w", err)
	}

	dlPath := fmt.Sprintf("/drive/v3/files/%s?alt=media", url.PathEscape(fileID))
	dlResp, err := d.driveRequest(ctx, http.MethodGet, dlPath, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("drive download: %w", err)
	}
	defer dlResp.Body.Close()

	if dlResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(dlResp.Body)
		return nil, nil, fmt.Errorf("drive download returned %d: %s", dlResp.StatusCode, string(body))
	}

	data, err := io.ReadAll(io.LimitReader(dlResp.Body, 100*1024*1024))
	if err != nil {
		return nil, nil, fmt.Errorf("read download body: %w", err)
	}

	return data, &fileMeta, nil
}

// ListFolder lists files in a specific Drive folder.
func (d *Service) ListFolder(ctx context.Context, folderID string, maxResults int) ([]File, error) {
	if folderID == "" {
		folderID = "root"
	}
	if maxResults <= 0 {
		maxResults = 50
	}

	q := url.QueryEscape(fmt.Sprintf("'%s' in parents and trashed = false", folderID))
	fields := "files(id,name,mimeType,size,createdTime,modifiedTime,webViewLink,parents)"
	apiPath := fmt.Sprintf("/drive/v3/files?q=%s&fields=%s&pageSize=%d&orderBy=name",
		q, url.QueryEscape(fields), maxResults)

	resp, err := d.driveRequest(ctx, http.MethodGet, apiPath, nil)
	if err != nil {
		return nil, fmt.Errorf("drive list folder: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("drive list returned %d: %s", resp.StatusCode, string(body))
	}

	var result FileList
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode list response: %w", err)
	}
	return result.Files, nil
}

// IsTextMime returns true if the MIME type is text-based.
func IsTextMime(mime string) bool {
	return strings.HasPrefix(mime, "text/") ||
		mime == "application/json" ||
		mime == "application/xml" ||
		mime == "application/javascript"
}
