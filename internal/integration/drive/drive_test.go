package drive

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDriveSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/drive/v3/files") {
			http.Error(w, "not found", 404)
			return
		}
		json.NewEncoder(w).Encode(FileList{
			Files: []File{
				{ID: "file1", Name: "doc1.pdf", MimeType: "application/pdf", Size: "1024", ModifiedTime: "2025-01-01T00:00:00Z"},
				{ID: "file2", Name: "doc2.txt", MimeType: "text/plain", Size: "512", ModifiedTime: "2025-01-02T00:00:00Z"},
			},
		})
	}))
	defer srv.Close()

	// Direct HTTP search test (bypassing OAuth).
	apiURL := srv.URL + "/drive/v3/files?q=name+contains+'doc'&pageSize=20"
	resp, err := http.Get(apiURL)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	defer resp.Body.Close()
	var result FileList
	json.NewDecoder(resp.Body).Decode(&result)
	if len(result.Files) != 2 {
		t.Errorf("expected 2 files, got %d", len(result.Files))
	}
	if result.Files[0].Name != "doc1.pdf" {
		t.Errorf("expected doc1.pdf, got %s", result.Files[0].Name)
	}
}

func TestDriveDownload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RawQuery, "alt=media") {
			w.Write([]byte("file content here"))
			return
		}
		if strings.Contains(r.URL.Path, "/drive/v3/files/") {
			json.NewEncoder(w).Encode(File{
				ID: "file1", Name: "test.txt", MimeType: "text/plain", Size: "17",
			})
			return
		}
		http.Error(w, "not found", 404)
	}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/drive/v3/files/file1?fields=id,name,mimeType,size")
	if err != nil {
		t.Fatalf("get metadata: %v", err)
	}
	defer resp.Body.Close()
	var meta File
	json.NewDecoder(resp.Body).Decode(&meta)
	if meta.Name != "test.txt" {
		t.Errorf("expected test.txt, got %s", meta.Name)
	}
}

func TestDriveUpload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/upload/drive/v3/files") {
			json.NewEncoder(w).Encode(File{ID: "new-file-id", Name: "uploaded.txt", MimeType: "text/plain"})
			return
		}
		http.Error(w, "not found", 404)
	}))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/upload/drive/v3/files?uploadType=multipart", "text/plain", strings.NewReader("test"))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	defer resp.Body.Close()
	var result File
	json.NewDecoder(resp.Body).Decode(&result)
	if result.ID != "new-file-id" {
		t.Errorf("expected new-file-id, got %s", result.ID)
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
		got := IsTextMime(tt.mime)
		if got != tt.expected {
			t.Errorf("IsTextMime(%s) = %v, want %v", tt.mime, got, tt.expected)
		}
	}
}
