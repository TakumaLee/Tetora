package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDropboxSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/2/files/search_v2" {
			json.NewEncoder(w).Encode(DropboxSearchResult{
				Matches: []struct {
					Metadata struct {
						Metadata DropboxFile `json:"metadata"`
					} `json:"metadata"`
				}{
					{Metadata: struct {
						Metadata DropboxFile `json:"metadata"`
					}{Metadata: DropboxFile{
						ID: "id:abc123", Name: "report.pdf", PathDisplay: "/docs/report.pdf",
						Size: 2048, ServerModified: "2025-01-01T00:00:00Z",
					}}},
				},
				HasMore: false,
			})
			return
		}
		http.Error(w, "not found", 404)
	}))
	defer srv.Close()

	// Direct HTTP search test (bypassing OAuth).
	resp, err := http.Post(srv.URL+"/2/files/search_v2", "application/json", strings.NewReader(`{"query":"report"}`))
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	defer resp.Body.Close()

	var result DropboxSearchResult
	json.NewDecoder(resp.Body).Decode(&result)
	if len(result.Matches) != 1 {
		t.Errorf("expected 1 match, got %d", len(result.Matches))
	}
	if result.Matches[0].Metadata.Metadata.Name != "report.pdf" {
		t.Errorf("expected report.pdf, got %s", result.Matches[0].Metadata.Metadata.Name)
	}
}

func TestDropboxUpload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/2/files/upload" {
			json.NewEncoder(w).Encode(DropboxFile{
				ID:          "id:xyz789",
				Name:        "uploaded.txt",
				PathDisplay: "/uploads/uploaded.txt",
				Size:        100,
			})
			return
		}
		http.Error(w, "not found", 404)
	}))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/2/files/upload", "application/octet-stream", strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	defer resp.Body.Close()

	var result DropboxFile
	json.NewDecoder(resp.Body).Decode(&result)
	if result.Name != "uploaded.txt" {
		t.Errorf("expected uploaded.txt, got %s", result.Name)
	}
}

func TestDropboxDownload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/2/files/download" {
			w.Header().Set("Dropbox-API-Result", `{"name":"dl.txt","path_display":"/dl.txt","size":13}`)
			w.Write([]byte("file contents"))
			return
		}
		http.Error(w, "not found", 404)
	}))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/2/files/download", "application/octet-stream", nil)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	defer resp.Body.Close()

	// Check metadata header.
	var meta DropboxFile
	resultHeader := resp.Header.Get("Dropbox-API-Result")
	if resultHeader == "" {
		t.Fatal("expected Dropbox-API-Result header")
	}
	json.Unmarshal([]byte(resultHeader), &meta)
	if meta.Name != "dl.txt" {
		t.Errorf("expected dl.txt, got %s", meta.Name)
	}

	buf := make([]byte, 1024)
	n, _ := resp.Body.Read(buf)
	if !strings.Contains(string(buf[:n]), "file contents") {
		t.Errorf("expected file contents, got: %s", string(buf[:n]))
	}
}

func TestDropboxListFolder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/2/files/list_folder" {
			json.NewEncoder(w).Encode(DropboxListResult{
				Entries: []DropboxFile{
					{Tag: "file", Name: "a.txt", PathDisplay: "/a.txt", Size: 100},
					{Tag: "folder", Name: "docs", PathDisplay: "/docs"},
				},
				HasMore: false,
			})
			return
		}
		http.Error(w, "not found", 404)
	}))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/2/files/list_folder", "application/json", strings.NewReader(`{"path":""}`))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	defer resp.Body.Close()

	var result DropboxListResult
	json.NewDecoder(resp.Body).Decode(&result)
	if len(result.Entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(result.Entries))
	}
}

// --- Tool Handler Tests ---

func TestToolDropboxOpNoService(t *testing.T) {
	oldSvc := globalDropboxService
	globalDropboxService = nil
	defer func() { globalDropboxService = oldSvc }()

	cfg := &Config{}
	input, _ := json.Marshal(map[string]string{"action": "search", "query": "test"})
	_, err := toolDropboxOp(context.Background(), cfg, input)
	if err == nil {
		t.Error("expected error when Dropbox not enabled")
	}
	if !strings.Contains(err.Error(), "not enabled") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestToolDropboxOpMissingAction(t *testing.T) {
	oldSvc := globalDropboxService
	globalDropboxService = newDropboxService()
	defer func() { globalDropboxService = oldSvc }()

	cfg := &Config{}
	input, _ := json.Marshal(map[string]string{})
	_, err := toolDropboxOp(context.Background(), cfg, input)
	if err == nil {
		t.Error("expected error for missing action")
	}
}

func TestToolDropboxOpUnknownAction(t *testing.T) {
	oldSvc := globalDropboxService
	globalDropboxService = newDropboxService()
	defer func() { globalDropboxService = oldSvc }()

	cfg := &Config{}
	input, _ := json.Marshal(map[string]string{"action": "invalid"})
	_, err := toolDropboxOp(context.Background(), cfg, input)
	if err == nil {
		t.Error("expected error for unknown action")
	}
	if !strings.Contains(err.Error(), "unknown action") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestToolDropboxOpSearchMissingQuery(t *testing.T) {
	oldSvc := globalDropboxService
	globalDropboxService = newDropboxService()
	defer func() { globalDropboxService = oldSvc }()

	cfg := &Config{}
	input, _ := json.Marshal(map[string]string{"action": "search"})
	_, err := toolDropboxOp(context.Background(), cfg, input)
	if err == nil {
		t.Error("expected error for search without query")
	}
}

func TestToolDropboxOpUploadMissingPath(t *testing.T) {
	oldSvc := globalDropboxService
	globalDropboxService = newDropboxService()
	defer func() { globalDropboxService = oldSvc }()

	cfg := &Config{}
	input, _ := json.Marshal(map[string]string{"action": "upload", "content": "data"})
	_, err := toolDropboxOp(context.Background(), cfg, input)
	if err == nil {
		t.Error("expected error for upload without path")
	}
}

func TestToolDropboxOpDownloadMissingPath(t *testing.T) {
	oldSvc := globalDropboxService
	globalDropboxService = newDropboxService()
	defer func() { globalDropboxService = oldSvc }()

	cfg := &Config{}
	input, _ := json.Marshal(map[string]string{"action": "download"})
	_, err := toolDropboxOp(context.Background(), cfg, input)
	if err == nil {
		t.Error("expected error for download without path")
	}
}
