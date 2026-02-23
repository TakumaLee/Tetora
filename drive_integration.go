package main

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
	"time"
)

// --- P23.3: Google Drive Integration ---

// driveBaseURL is the Google Drive v3 API base (overridable in tests).
var driveBaseURL = "https://www.googleapis.com"

// DriveService provides Google Drive v3 operations via OAuth.
type DriveService struct {
	oauthService string // OAuth service name, default "google"
}

// DriveFile represents a Google Drive file.
type DriveFile struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	MimeType     string `json:"mimeType"`
	Size         string `json:"size,omitempty"`
	CreatedTime  string `json:"createdTime,omitempty"`
	ModifiedTime string `json:"modifiedTime,omitempty"`
	WebViewLink  string `json:"webViewLink,omitempty"`
	Parents      []string `json:"parents,omitempty"`
}

// DriveFileList is a paginated list of Drive files.
type DriveFileList struct {
	Files         []DriveFile `json:"files"`
	NextPageToken string      `json:"nextPageToken,omitempty"`
}

// globalDriveService is exposed for tool handlers.
var globalDriveService *DriveService

// newDriveService creates a new DriveService.
func newDriveService() *DriveService {
	return &DriveService{oauthService: "google"}
}

// driveRequest makes an authenticated request to the Drive API.
func (d *DriveService) driveRequest(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	if globalOAuthManager == nil {
		return nil, fmt.Errorf("OAuth manager not initialized")
	}
	reqURL := driveBaseURL + path
	return globalOAuthManager.Request(ctx, d.oauthService, method, reqURL, body)
}

// Search searches for files in Google Drive.
func (d *DriveService) Search(ctx context.Context, query string, maxResults int) ([]DriveFile, error) {
	if maxResults <= 0 {
		maxResults = 20
	}
	if maxResults > 100 {
		maxResults = 100
	}

	// Build search query.
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

	var result DriveFileList
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode drive response: %w", err)
	}
	return result.Files, nil
}

// Upload uploads a file to Google Drive.
func (d *DriveService) Upload(ctx context.Context, name, mimeType, parentID string, data []byte) (*DriveFile, error) {
	if name == "" {
		return nil, fmt.Errorf("file name is required")
	}

	// Use multipart upload for simplicity.
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	// Metadata part.
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

	// File part.
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

	if globalOAuthManager == nil {
		return nil, fmt.Errorf("OAuth manager not initialized")
	}

	reqURL := driveBaseURL + apiPath
	resp, err := globalOAuthManager.Request(ctx, d.oauthService, http.MethodPost, reqURL, &buf)
	if err != nil {
		return nil, fmt.Errorf("drive upload: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("drive upload returned %d: %s", resp.StatusCode, string(body))
	}

	var result DriveFile
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode upload response: %w", err)
	}
	return &result, nil
}

// Download downloads a file from Google Drive by ID.
func (d *DriveService) Download(ctx context.Context, fileID string) ([]byte, *DriveFile, error) {
	if fileID == "" {
		return nil, nil, fmt.Errorf("file ID is required")
	}

	// First get file metadata.
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

	var fileMeta DriveFile
	if err := json.NewDecoder(metaResp.Body).Decode(&fileMeta); err != nil {
		return nil, nil, fmt.Errorf("decode metadata: %w", err)
	}

	// Download content.
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

	data, err := io.ReadAll(io.LimitReader(dlResp.Body, 100*1024*1024)) // 100 MB limit
	if err != nil {
		return nil, nil, fmt.Errorf("read download body: %w", err)
	}

	return data, &fileMeta, nil
}

// ListFolder lists files in a specific Drive folder.
func (d *DriveService) ListFolder(ctx context.Context, folderID string, maxResults int) ([]DriveFile, error) {
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

	var result DriveFileList
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode list response: %w", err)
	}
	return result.Files, nil
}

// --- Tool Handlers ---

// toolDriveSearch searches for files in Google Drive.
func toolDriveSearch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
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

	svc := globalDriveService
	if svc == nil {
		return "", fmt.Errorf("Google Drive integration not enabled")
	}

	files, err := svc.Search(ctx, args.Query, args.MaxResults)
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "No files found matching query.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Drive search results (%d files):\n\n", len(files)))
	for _, f := range files {
		size := f.Size
		if size == "" {
			size = "-"
		}
		sb.WriteString(fmt.Sprintf("- %s | %s | %s | %s bytes | %s\n",
			f.ID, f.Name, f.MimeType, size, f.ModifiedTime))
	}
	return sb.String(), nil
}

// toolDriveUpload uploads a file to Google Drive.
func toolDriveUpload(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Name     string `json:"name"`
		Content  string `json:"content"`
		MimeType string `json:"mime_type"`
		ParentID string `json:"parent_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Name == "" {
		return "", fmt.Errorf("name is required")
	}
	if args.Content == "" {
		return "", fmt.Errorf("content is required")
	}

	svc := globalDriveService
	if svc == nil {
		return "", fmt.Errorf("Google Drive integration not enabled")
	}

	if args.MimeType == "" {
		args.MimeType = mimeFromExt(args.Name)
	}

	result, err := svc.Upload(ctx, args.Name, args.MimeType, args.ParentID, []byte(args.Content))
	if err != nil {
		return "", err
	}

	out, _ := json.MarshalIndent(result, "", "  ")
	return fmt.Sprintf("File uploaded to Drive:\n%s", string(out)), nil
}

// toolDriveDownload downloads a file from Google Drive.
func toolDriveDownload(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		FileID string `json:"file_id"`
		SaveAs string `json:"save_as"` // optional: save to local file manager
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.FileID == "" {
		return "", fmt.Errorf("file_id is required")
	}

	svc := globalDriveService
	if svc == nil {
		return "", fmt.Errorf("Google Drive integration not enabled")
	}

	data, fileMeta, err := svc.Download(ctx, args.FileID)
	if err != nil {
		return "", err
	}

	// Optionally store in local file manager.
	if args.SaveAs != "" && globalFileManager != nil {
		name := args.SaveAs
		if name == "auto" {
			name = fileMeta.Name
		}
		mf, isDup, err := globalFileManager.StoreFile("", name, "drive", "google_drive", fileMeta.ID, data)
		if err != nil {
			return "", fmt.Errorf("save to file manager: %w", err)
		}
		status := "saved"
		if isDup {
			status = "duplicate (existing)"
		}
		return fmt.Sprintf("Downloaded '%s' (%d bytes) from Drive and %s locally (ID: %s)",
			fileMeta.Name, len(data), status, mf.ID), nil
	}

	// Return preview for text, or size info for binary.
	if isTextMime(fileMeta.MimeType) && len(data) < 50000 {
		return fmt.Sprintf("Downloaded '%s' (%d bytes):\n\n%s", fileMeta.Name, len(data), string(data)), nil
	}
	return fmt.Sprintf("Downloaded '%s' (%d bytes, %s). Use save_as to store locally.",
		fileMeta.Name, len(data), fileMeta.MimeType), nil
}

// isTextMime returns true if the MIME type is text-based.
func isTextMime(mime string) bool {
	return strings.HasPrefix(mime, "text/") ||
		mime == "application/json" ||
		mime == "application/xml" ||
		mime == "application/javascript"
}

// Ensure time import is used.
var _ = time.Now
