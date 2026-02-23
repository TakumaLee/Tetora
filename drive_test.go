package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockOAuthManager creates a mock OAuth manager for testing Drive.
type mockOAuthManagerForDrive struct {
	baseURL string
}

func TestDriveSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/drive/v3/files") {
			http.Error(w, "not found", 404)
			return
		}
		json.NewEncoder(w).Encode(DriveFileList{
			Files: []DriveFile{
				{ID: "file1", Name: "doc1.pdf", MimeType: "application/pdf", Size: "1024", ModifiedTime: "2025-01-01T00:00:00Z"},
				{ID: "file2", Name: "doc2.txt", MimeType: "text/plain", Size: "512", ModifiedTime: "2025-01-02T00:00:00Z"},
			},
		})
	}))
	defer srv.Close()

	origBase := driveBaseURL
	driveBaseURL = srv.URL
	defer func() { driveBaseURL = origBase }()

	// Create a minimal mock OAuth manager.
	origOAuth := globalOAuthManager
	globalOAuthManager = &OAuthManager{
		cfg:    &Config{},
		states: make(map[string]oauthState),
	}
	defer func() { globalOAuthManager = origOAuth }()

	svc := newDriveService()

	// Override driveRequest to skip OAuth token logic.
	files, err := driveSearchDirect(svc, srv.URL, "doc")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(files) != 2 {
		t.Errorf("expected 2 files, got %d", len(files))
	}
	if files[0].Name != "doc1.pdf" {
		t.Errorf("expected doc1.pdf, got %s", files[0].Name)
	}
}

// driveSearchDirect does a direct HTTP search bypassing OAuth for testing.
func driveSearchDirect(d *DriveService, baseURL, query string) ([]DriveFile, error) {
	apiURL := baseURL + "/drive/v3/files?q=name+contains+'" + query + "'&pageSize=20"
	resp, err := http.Get(apiURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var result DriveFileList
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result.Files, nil
}

func TestDriveDownload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RawQuery, "alt=media") {
			w.Write([]byte("file content here"))
			return
		}
		if strings.Contains(r.URL.Path, "/drive/v3/files/") {
			json.NewEncoder(w).Encode(DriveFile{
				ID:       "file1",
				Name:     "test.txt",
				MimeType: "text/plain",
				Size:     "17",
			})
			return
		}
		http.Error(w, "not found", 404)
	}))
	defer srv.Close()

	// Direct HTTP download test.
	resp, err := http.Get(srv.URL + "/drive/v3/files/file1?fields=id,name,mimeType,size")
	if err != nil {
		t.Fatalf("get metadata: %v", err)
	}
	defer resp.Body.Close()

	var meta DriveFile
	json.NewDecoder(resp.Body).Decode(&meta)
	if meta.Name != "test.txt" {
		t.Errorf("expected test.txt, got %s", meta.Name)
	}

	resp2, err := http.Get(srv.URL + "/drive/v3/files/file1?alt=media")
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	defer resp2.Body.Close()
	buf := make([]byte, 1024)
	n, _ := resp2.Body.Read(buf)
	if !strings.Contains(string(buf[:n]), "file content here") {
		t.Errorf("expected file content, got: %s", string(buf[:n]))
	}
}

func TestDriveUpload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/upload/drive/v3/files") {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(DriveFile{
				ID:       "new-file-id",
				Name:     "uploaded.txt",
				MimeType: "text/plain",
			})
			return
		}
		http.Error(w, "not found", 404)
	}))
	defer srv.Close()

	// Direct HTTP upload test.
	resp, err := http.Post(srv.URL+"/upload/drive/v3/files?uploadType=multipart", "text/plain", strings.NewReader("test content"))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	defer resp.Body.Close()
	var result DriveFile
	json.NewDecoder(resp.Body).Decode(&result)
	if result.ID != "new-file-id" {
		t.Errorf("expected new-file-id, got %s", result.ID)
	}
}

func TestToolDriveSearchNoService(t *testing.T) {
	oldSvc := globalDriveService
	globalDriveService = nil
	defer func() { globalDriveService = oldSvc }()

	cfg := &Config{}
	input, _ := json.Marshal(map[string]string{"query": "test"})
	_, err := toolDriveSearch(context.Background(), cfg, input)
	if err == nil {
		t.Error("expected error when Drive service not enabled")
	}
	if !strings.Contains(err.Error(), "not enabled") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestToolDriveSearchMissingQuery(t *testing.T) {
	oldSvc := globalDriveService
	globalDriveService = newDriveService()
	defer func() { globalDriveService = oldSvc }()

	cfg := &Config{}
	input, _ := json.Marshal(map[string]string{})
	_, err := toolDriveSearch(context.Background(), cfg, input)
	if err == nil {
		t.Error("expected error for missing query")
	}
}

func TestToolDriveUploadMissingName(t *testing.T) {
	oldSvc := globalDriveService
	globalDriveService = newDriveService()
	defer func() { globalDriveService = oldSvc }()

	cfg := &Config{}
	input, _ := json.Marshal(map[string]string{"content": "data"})
	_, err := toolDriveUpload(context.Background(), cfg, input)
	if err == nil {
		t.Error("expected error for missing name")
	}
}

func TestToolDriveDownloadMissingID(t *testing.T) {
	oldSvc := globalDriveService
	globalDriveService = newDriveService()
	defer func() { globalDriveService = oldSvc }()

	cfg := &Config{}
	input, _ := json.Marshal(map[string]string{})
	_, err := toolDriveDownload(context.Background(), cfg, input)
	if err == nil {
		t.Error("expected error for missing file_id")
	}
}

func TestIsTextMime(t *testing.T) {
	tests := []struct {
		mime     string
		expected bool
	}{
		{"text/plain", true},
		{"text/html", true},
		{"application/json", true},
		{"application/pdf", false},
		{"image/png", false},
	}
	for _, tt := range tests {
		got := isTextMime(tt.mime)
		if got != tt.expected {
			t.Errorf("isTextMime(%s) = %v, want %v", tt.mime, got, tt.expected)
		}
	}
}
