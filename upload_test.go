package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- sanitizeFilename tests ---

func TestSanitizeFilename_Normal(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"hello.txt", "hello.txt"},
		{"photo.jpg", "photo.jpg"},
		{"my-file_v2.pdf", "my-file_v2.pdf"},
	}
	for _, tc := range cases {
		got := sanitizeFilename(tc.input)
		if got != tc.expected {
			t.Errorf("sanitizeFilename(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestSanitizeFilename_PathTraversal(t *testing.T) {
	cases := []struct {
		input string
	}{
		{"../../etc/passwd"},
		{"/etc/shadow"},
		{"../../../secret.txt"},
	}
	for _, tc := range cases {
		got := sanitizeFilename(tc.input)
		if strings.Contains(got, "/") || strings.Contains(got, "..") {
			t.Errorf("sanitizeFilename(%q) = %q, should not contain path separators", tc.input, got)
		}
	}
}

func TestSanitizeFilename_LeadingDots(t *testing.T) {
	got := sanitizeFilename(".hidden")
	if strings.HasPrefix(got, ".") {
		t.Errorf("sanitizeFilename(%q) = %q, should not start with dot", ".hidden", got)
	}
}

func TestSanitizeFilename_UnsafeChars(t *testing.T) {
	got := sanitizeFilename("file name (1).txt")
	// Spaces and parens should be stripped.
	if strings.ContainsAny(got, " ()") {
		t.Errorf("sanitizeFilename returned unsafe chars: %q", got)
	}
	// Should still contain the safe parts.
	if !strings.Contains(got, "filename1.txt") {
		t.Errorf("sanitizeFilename(%q) = %q, expected safe characters preserved", "file name (1).txt", got)
	}
}

func TestSanitizeFilename_Empty(t *testing.T) {
	got := sanitizeFilename("...")
	if got != "" {
		t.Errorf("sanitizeFilename(%q) = %q, want empty", "...", got)
	}
}

// --- detectMimeType tests ---

func TestDetectMimeType(t *testing.T) {
	cases := []struct {
		name     string
		expected string
	}{
		{"photo.jpg", "image/jpeg"},
		{"photo.jpeg", "image/jpeg"},
		{"image.png", "image/png"},
		{"animation.gif", "image/gif"},
		{"document.pdf", "application/pdf"},
		{"readme.md", "text/markdown"},
		{"data.json", "application/json"},
		{"data.csv", "text/csv"},
		{"code.go", "text/x-go"},
		{"script.py", "text/x-python"},
		{"unknown.xyz", "application/octet-stream"},
		{"noext", "application/octet-stream"},
	}
	for _, tc := range cases {
		got := detectMimeType(tc.name)
		if got != tc.expected {
			t.Errorf("detectMimeType(%q) = %q, want %q", tc.name, got, tc.expected)
		}
	}
}

// --- initUploadDir tests ---

func TestInitUploadDir(t *testing.T) {
	tmpDir := t.TempDir()
	dir := initUploadDir(tmpDir)

	expected := filepath.Join(tmpDir, "uploads")
	if dir != expected {
		t.Errorf("initUploadDir(%q) = %q, want %q", tmpDir, dir, expected)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("upload dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("upload dir is not a directory")
	}
}

// --- saveUpload tests ---

func TestSaveUpload_Success(t *testing.T) {
	tmpDir := t.TempDir()
	uploadDir := initUploadDir(tmpDir)

	content := "hello world"
	reader := strings.NewReader(content)

	file, err := saveUpload(uploadDir, "test.txt", reader, int64(len(content)), "test")
	if err != nil {
		t.Fatalf("saveUpload failed: %v", err)
	}

	if file.Name != "test.txt" {
		t.Errorf("file.Name = %q, want %q", file.Name, "test.txt")
	}
	if file.Size != int64(len(content)) {
		t.Errorf("file.Size = %d, want %d", file.Size, len(content))
	}
	if file.MimeType != "text/plain" {
		t.Errorf("file.MimeType = %q, want %q", file.MimeType, "text/plain")
	}
	if file.Source != "test" {
		t.Errorf("file.Source = %q, want %q", file.Source, "test")
	}
	if file.UploadedAt == "" {
		t.Error("file.UploadedAt should not be empty")
	}

	// Verify file exists on disk.
	data, err := os.ReadFile(file.Path)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	if string(data) != content {
		t.Errorf("file content = %q, want %q", string(data), content)
	}
}

func TestSaveUpload_EmptyName(t *testing.T) {
	tmpDir := t.TempDir()
	uploadDir := initUploadDir(tmpDir)

	content := "data"
	reader := strings.NewReader(content)

	file, err := saveUpload(uploadDir, "", reader, int64(len(content)), "test")
	if err != nil {
		t.Fatalf("saveUpload failed: %v", err)
	}

	if file.Name != "upload" {
		t.Errorf("file.Name = %q, want %q for empty original name", file.Name, "upload")
	}
}

func TestSaveUpload_PathTraversal(t *testing.T) {
	tmpDir := t.TempDir()
	uploadDir := initUploadDir(tmpDir)

	content := "malicious"
	reader := strings.NewReader(content)

	file, err := saveUpload(uploadDir, "../../etc/passwd", reader, int64(len(content)), "test")
	if err != nil {
		t.Fatalf("saveUpload failed: %v", err)
	}

	// File should be saved within the upload dir, not outside.
	if !strings.HasPrefix(file.Path, uploadDir) {
		t.Errorf("file saved outside upload dir: %q", file.Path)
	}
}

func TestSaveUpload_TimestampPrefix(t *testing.T) {
	tmpDir := t.TempDir()
	uploadDir := initUploadDir(tmpDir)

	reader := strings.NewReader("x")
	file, err := saveUpload(uploadDir, "doc.pdf", reader, 1, "test")
	if err != nil {
		t.Fatalf("saveUpload failed: %v", err)
	}

	basename := filepath.Base(file.Path)
	// Should have format: YYYYMMDD-HHMMSS_doc.pdf
	if !strings.Contains(basename, "_doc.pdf") {
		t.Errorf("filename %q should contain timestamp prefix and original name", basename)
	}
}

// --- buildFilePromptPrefix tests ---

func TestBuildFilePromptPrefix_Empty(t *testing.T) {
	got := buildFilePromptPrefix(nil)
	if got != "" {
		t.Errorf("buildFilePromptPrefix(nil) = %q, want empty", got)
	}
}

func TestBuildFilePromptPrefix_SingleFile(t *testing.T) {
	files := []*UploadedFile{
		{
			Name:     "report.pdf",
			Path:     "/tmp/uploads/20260222-120000_report.pdf",
			Size:     1024,
			MimeType: "application/pdf",
		},
	}
	got := buildFilePromptPrefix(files)
	if !strings.Contains(got, "The user has attached the following files:") {
		t.Error("prefix should contain header")
	}
	if !strings.Contains(got, "report.pdf") {
		t.Error("prefix should contain filename")
	}
	if !strings.Contains(got, "application/pdf") {
		t.Error("prefix should contain MIME type")
	}
	if !strings.Contains(got, "1024 bytes") {
		t.Error("prefix should contain file size")
	}
}

func TestBuildFilePromptPrefix_MultipleFiles(t *testing.T) {
	files := []*UploadedFile{
		{Name: "a.txt", Path: "/tmp/a.txt", Size: 10, MimeType: "text/plain"},
		{Name: "b.png", Path: "/tmp/b.png", Size: 2048, MimeType: "image/png"},
	}
	got := buildFilePromptPrefix(files)
	if !strings.Contains(got, "a.txt") || !strings.Contains(got, "b.png") {
		t.Error("prefix should contain both filenames")
	}
	// Should have two "- " lines for the files.
	lines := strings.Split(got, "\n")
	fileLines := 0
	for _, l := range lines {
		if strings.HasPrefix(l, "- ") {
			fileLines++
		}
	}
	if fileLines != 2 {
		t.Errorf("expected 2 file lines, got %d", fileLines)
	}
}

// --- cleanupUploads tests ---

func TestCleanupUploads(t *testing.T) {
	tmpDir := t.TempDir()
	uploadDir := initUploadDir(tmpDir)

	// Create an "old" file.
	oldFile := filepath.Join(uploadDir, "old.txt")
	os.WriteFile(oldFile, []byte("old"), 0o644)
	// Set its modification time to 10 days ago.
	oldTime := time.Now().AddDate(0, 0, -10)
	os.Chtimes(oldFile, oldTime, oldTime)

	// Create a "new" file.
	newFile := filepath.Join(uploadDir, "new.txt")
	os.WriteFile(newFile, []byte("new"), 0o644)

	// Cleanup files older than 7 days.
	cleanupUploads(uploadDir, 7)

	// Old file should be removed.
	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Error("old file should have been removed")
	}

	// New file should still exist.
	if _, err := os.Stat(newFile); err != nil {
		t.Error("new file should still exist")
	}
}

func TestCleanupUploads_NonExistentDir(t *testing.T) {
	// Should not panic on non-existent directory.
	cleanupUploads("/nonexistent/dir/that/does/not/exist", 7)
}

// --- coalesce tests ---

func TestCoalesce(t *testing.T) {
	cases := []struct {
		input    []string
		expected string
	}{
		{[]string{"a", "b"}, "a"},
		{[]string{"", "b", "c"}, "b"},
		{[]string{"", "", "c"}, "c"},
		{[]string{"", "", ""}, ""},
		{[]string{}, ""},
	}
	for _, tc := range cases {
		got := coalesce(tc.input...)
		if got != tc.expected {
			t.Errorf("coalesce(%v) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}
