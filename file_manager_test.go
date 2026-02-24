package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func testFileManagerService(t *testing.T) (*FileManagerService, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	storageDir := filepath.Join(dir, "files")
	os.MkdirAll(storageDir, 0o755)

	if err := initFileManagerDB(dbPath); err != nil {
		t.Fatalf("initFileManagerDB: %v", err)
	}

	svc := &FileManagerService{
		dbPath:     dbPath,
		storageDir: storageDir,
		maxSizeMB:  10,
	}
	return svc, dir
}

func TestInitFileManagerDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	if err := initFileManagerDB(dbPath); err != nil {
		t.Fatalf("first init: %v", err)
	}
	// Idempotent.
	if err := initFileManagerDB(dbPath); err != nil {
		t.Fatalf("second init: %v", err)
	}

	// Verify table exists.
	rows, err := queryDB(dbPath, "SELECT name FROM sqlite_master WHERE type='table' AND name='managed_files'")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("managed_files table not created")
	}
}

func TestStoreFile(t *testing.T) {
	svc, _ := testFileManagerService(t)

	data := []byte("hello world")
	mf, isDup, err := svc.StoreFile("user1", "hello.txt", "docs", "test", "", data)
	if err != nil {
		t.Fatalf("StoreFile: %v", err)
	}
	if isDup {
		t.Error("expected isDup=false for first store")
	}
	if mf.Filename != "hello.txt" {
		t.Errorf("expected filename=hello.txt, got %s", mf.Filename)
	}
	if mf.MimeType != "text/plain" {
		t.Errorf("expected mime=text/plain, got %s", mf.MimeType)
	}
	if mf.Category != "docs" {
		t.Errorf("expected category=docs, got %s", mf.Category)
	}
	if mf.FileSize != int64(len(data)) {
		t.Errorf("expected size=%d, got %d", len(data), mf.FileSize)
	}
	if mf.ContentHash == "" {
		t.Error("expected non-empty hash")
	}
	// Verify file exists on disk.
	if _, err := os.Stat(mf.StoragePath); os.IsNotExist(err) {
		t.Errorf("file not found on disk: %s", mf.StoragePath)
	}
}

func TestStoreFileDuplicate(t *testing.T) {
	svc, _ := testFileManagerService(t)

	data := []byte("duplicate content")
	mf1, _, err := svc.StoreFile("user1", "file1.txt", "docs", "", "", data)
	if err != nil {
		t.Fatalf("StoreFile 1: %v", err)
	}

	mf2, isDup, err := svc.StoreFile("user1", "file2.txt", "docs", "", "", data)
	if err != nil {
		t.Fatalf("StoreFile 2: %v", err)
	}
	if !isDup {
		t.Error("expected isDup=true for duplicate content")
	}
	if mf2.ID != mf1.ID {
		t.Error("expected same ID for duplicate")
	}
}

func TestStoreFileMaxSize(t *testing.T) {
	svc, _ := testFileManagerService(t)
	svc.maxSizeMB = 1 // 1 MB limit

	bigData := make([]byte, 2*1024*1024) // 2 MB
	_, _, err := svc.StoreFile("user1", "big.bin", "", "", "", bigData)
	if err == nil {
		t.Error("expected error for oversized file")
	}
	if !strings.Contains(err.Error(), "exceeds max size") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGetFile(t *testing.T) {
	svc, _ := testFileManagerService(t)

	data := []byte("get me")
	mf, _, err := svc.StoreFile("user1", "getme.txt", "general", "", "", data)
	if err != nil {
		t.Fatalf("StoreFile: %v", err)
	}

	got, err := svc.GetFile(mf.ID)
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if got.OriginalName != "getme.txt" {
		t.Errorf("expected originalName=getme.txt, got %s", got.OriginalName)
	}
}

func TestGetFileNotFound(t *testing.T) {
	svc, _ := testFileManagerService(t)

	_, err := svc.GetFile("nonexistent-id")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestListFiles(t *testing.T) {
	svc, _ := testFileManagerService(t)

	svc.StoreFile("user1", "a.txt", "docs", "", "", []byte("aaa"))
	svc.StoreFile("user1", "b.pdf", "reports", "", "", []byte("bbb"))
	svc.StoreFile("user2", "c.txt", "docs", "", "", []byte("ccc"))

	// List all.
	all, err := svc.ListFiles("", "", 50)
	if err != nil {
		t.Fatalf("ListFiles all: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("expected 3 files, got %d", len(all))
	}

	// List by category.
	docs, err := svc.ListFiles("docs", "", 50)
	if err != nil {
		t.Fatalf("ListFiles docs: %v", err)
	}
	if len(docs) != 2 {
		t.Errorf("expected 2 docs, got %d", len(docs))
	}

	// List by user.
	user2, err := svc.ListFiles("", "user2", 50)
	if err != nil {
		t.Fatalf("ListFiles user2: %v", err)
	}
	if len(user2) != 1 {
		t.Errorf("expected 1 user2 file, got %d", len(user2))
	}
}

func TestDeleteFile(t *testing.T) {
	svc, _ := testFileManagerService(t)

	data := []byte("delete me")
	mf, _, err := svc.StoreFile("user1", "deleteme.txt", "general", "", "", data)
	if err != nil {
		t.Fatalf("StoreFile: %v", err)
	}

	if err := svc.DeleteFile(mf.ID); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}

	// Verify deleted from DB.
	_, err = svc.GetFile(mf.ID)
	if err == nil {
		t.Error("expected error after delete")
	}

	// Verify deleted from disk.
	if _, err := os.Stat(mf.StoragePath); !os.IsNotExist(err) {
		t.Error("expected file removed from disk")
	}
}

func TestOrganizeFile(t *testing.T) {
	svc, _ := testFileManagerService(t)

	data := []byte("organize me")
	mf, _, err := svc.StoreFile("user1", "org.txt", "general", "", "", data)
	if err != nil {
		t.Fatalf("StoreFile: %v", err)
	}

	organized, err := svc.OrganizeFile(mf.ID, "important")
	if err != nil {
		t.Fatalf("OrganizeFile: %v", err)
	}
	if organized.Category != "important" {
		t.Errorf("expected category=important, got %s", organized.Category)
	}
	if !strings.Contains(organized.StoragePath, "important") {
		t.Errorf("expected path to contain 'important', got %s", organized.StoragePath)
	}

	// Verify new file exists on disk.
	if _, err := os.Stat(organized.StoragePath); os.IsNotExist(err) {
		t.Error("organized file not found on disk")
	}

	// Verify old file removed.
	if _, err := os.Stat(mf.StoragePath); !os.IsNotExist(err) {
		t.Error("old file should be removed after organize")
	}
}

func TestFindDuplicates(t *testing.T) {
	svc, _ := testFileManagerService(t)

	// Store same content with different filenames (need to bypass dedup for test).
	data1 := []byte("unique content alpha")
	data2 := []byte("unique content beta")

	svc.StoreFile("user1", "dup1.txt", "docs", "", "", data1)

	// Insert a second record with same hash manually for testing.
	mf, _, _ := svc.StoreFile("user1", "dup1.txt", "docs", "", "", data1)
	// Since dedup returns existing, manually insert a second record.
	hash := mf.ContentHash
	id2 := umNewID()
	queryDB(svc.dbPath, "INSERT INTO managed_files (id, user_id, filename, original_name, category, mime_type, file_size, content_hash, storage_path, source, source_id, metadata, created_at, updated_at) VALUES ('"+id2+"','user1','dup2.txt','dup2.txt','docs','text/plain',20,'"+hash+"','/tmp/fake','','','{}','2025-01-01T00:00:00Z','2025-01-01T00:00:00Z')")

	svc.StoreFile("user1", "unique.txt", "docs", "", "", data2)

	groups, err := svc.FindDuplicates()
	if err != nil {
		t.Fatalf("FindDuplicates: %v", err)
	}
	if len(groups) != 1 {
		t.Errorf("expected 1 duplicate group, got %d", len(groups))
	}
	if len(groups) > 0 && len(groups[0]) != 2 {
		t.Errorf("expected 2 files in group, got %d", len(groups[0]))
	}
}

func TestExtractPDF(t *testing.T) {
	// Skip if pdftotext not available.
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext not found, skipping PDF extraction test")
	}

	svc, dir := testFileManagerService(t)

	// Create a minimal PDF for testing.
	pdfContent := `%PDF-1.0
1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj
2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj
3 0 obj<</Type/Page/MediaBox[0 0 612 792]/Parent 2 0 R/Resources<</Font<</F1 4 0 R>>>>/Contents 5 0 R>>endobj
4 0 obj<</Type/Font/Subtype/Type1/BaseFont/Helvetica>>endobj
5 0 obj<</Length 44>>
stream
BT /F1 12 Tf 100 700 Td (Hello PDF) Tj ET
endstream
endobj
xref
0 6
0000000000 65535 f
0000000009 00000 n
0000000058 00000 n
0000000115 00000 n
0000000266 00000 n
0000000340 00000 n
trailer<</Size 6/Root 1 0 R>>
startxref
434
%%EOF`

	pdfPath := filepath.Join(dir, "test.pdf")
	os.WriteFile(pdfPath, []byte(pdfContent), 0o644)

	text, err := svc.ExtractPDF(pdfPath)
	if err != nil {
		t.Fatalf("ExtractPDF: %v", err)
	}
	if !strings.Contains(text, "Hello PDF") {
		t.Errorf("expected 'Hello PDF' in output, got: %s", text)
	}
}

func TestMimeFromExt(t *testing.T) {
	tests := []struct {
		filename string
		expected string
	}{
		{"doc.pdf", "application/pdf"},
		{"image.jpg", "image/jpeg"},
		{"image.PNG", "image/png"},
		{"data.json", "application/json"},
		{"page.html", "text/html"},
		{"unknown.xyz", "application/octet-stream"},
	}
	for _, tt := range tests {
		got := mimeFromExt(tt.filename)
		if got != tt.expected {
			t.Errorf("mimeFromExt(%s) = %s, want %s", tt.filename, got, tt.expected)
		}
	}
}

func TestContentHash(t *testing.T) {
	data := []byte("test data")
	h := contentHash(data)
	if len(h) != 32 {
		t.Errorf("expected hash length 32, got %d", len(h))
	}
	// Deterministic.
	h2 := contentHash(data)
	if h != h2 {
		t.Error("expected same hash for same data")
	}
	// Different data, different hash.
	h3 := contentHash([]byte("other data"))
	if h == h3 {
		t.Error("expected different hash for different data")
	}
}

// --- Tool Handler Tests ---

func TestToolFileStore(t *testing.T) {
	svc, _ := testFileManagerService(t)
	oldGlobal := globalFileManager
	globalFileManager = svc
	defer func() { globalFileManager = oldGlobal }()

	cfg := &Config{}

	// Store text content.
	input, _ := json.Marshal(map[string]string{
		"filename": "test.txt",
		"content":  "hello world",
		"category": "docs",
	})
	result, err := toolFileStore(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("toolFileStore: %v", err)
	}
	if !strings.Contains(result, "test.txt") {
		t.Errorf("expected filename in result, got: %s", result)
	}
	if !strings.Contains(result, "stored") {
		t.Errorf("expected 'stored' in result, got: %s", result)
	}
}

func TestToolFileStoreBase64(t *testing.T) {
	svc, _ := testFileManagerService(t)
	oldGlobal := globalFileManager
	globalFileManager = svc
	defer func() { globalFileManager = oldGlobal }()

	cfg := &Config{}
	encoded := base64.StdEncoding.EncodeToString([]byte("binary data"))
	input, _ := json.Marshal(map[string]string{
		"filename": "data.bin",
		"base64":   encoded,
	})
	result, err := toolFileStore(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("toolFileStore base64: %v", err)
	}
	if !strings.Contains(result, "data.bin") {
		t.Errorf("expected filename in result, got: %s", result)
	}
}

func TestToolFileList(t *testing.T) {
	svc, _ := testFileManagerService(t)
	oldGlobal := globalFileManager
	globalFileManager = svc
	defer func() { globalFileManager = oldGlobal }()

	cfg := &Config{}

	// Store some files first.
	svc.StoreFile("user1", "a.txt", "docs", "", "", []byte("aaa"))
	svc.StoreFile("user1", "b.txt", "docs", "", "", []byte("bbb"))

	input, _ := json.Marshal(map[string]string{"category": "docs"})
	result, err := toolFileList(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("toolFileList: %v", err)
	}
	if !strings.Contains(result, "a.txt") {
		t.Errorf("expected a.txt in result, got: %s", result)
	}
}

func TestToolFileDuplicates(t *testing.T) {
	svc, _ := testFileManagerService(t)
	oldGlobal := globalFileManager
	globalFileManager = svc
	defer func() { globalFileManager = oldGlobal }()

	cfg := &Config{}
	input := json.RawMessage(`{}`)
	result, err := toolFileDuplicates(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("toolFileDuplicates: %v", err)
	}
	if !strings.Contains(result, "No duplicate") {
		t.Errorf("expected no duplicates message, got: %s", result)
	}
}

func TestToolFileOrganize(t *testing.T) {
	svc, _ := testFileManagerService(t)
	oldGlobal := globalFileManager
	globalFileManager = svc
	defer func() { globalFileManager = oldGlobal }()

	cfg := &Config{}

	mf, _, _ := svc.StoreFile("user1", "move.txt", "general", "", "", []byte("move me"))

	input, _ := json.Marshal(map[string]string{
		"file_id":  mf.ID,
		"category": "archive",
	})
	result, err := toolFileOrganize(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("toolFileOrganize: %v", err)
	}
	if !strings.Contains(result, "archive") {
		t.Errorf("expected 'archive' in result, got: %s", result)
	}
}

func TestToolDocSummarize(t *testing.T) {
	svc, _ := testFileManagerService(t)
	oldGlobal := globalFileManager
	globalFileManager = svc
	defer func() { globalFileManager = oldGlobal }()

	cfg := &Config{}

	content := "Line one\nLine two\nLine three\nThe end."
	mf, _, _ := svc.StoreFile("user1", "readme.md", "docs", "", "", []byte(content))

	input, _ := json.Marshal(map[string]string{"file_id": mf.ID})
	result, err := toolDocSummarize(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("toolDocSummarize: %v", err)
	}
	if !strings.Contains(result, "readme.md") {
		t.Errorf("expected filename in result, got: %s", result)
	}
	if !strings.Contains(result, "Lines: 4") {
		t.Errorf("expected line count, got: %s", result)
	}
}

func TestToolPdfRead(t *testing.T) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext not found, skipping")
	}

	svc, dir := testFileManagerService(t)
	oldGlobal := globalFileManager
	globalFileManager = svc
	defer func() { globalFileManager = oldGlobal }()

	cfg := &Config{}

	pdfContent := `%PDF-1.0
1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj
2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj
3 0 obj<</Type/Page/MediaBox[0 0 612 792]/Parent 2 0 R/Resources<</Font<</F1 4 0 R>>>>/Contents 5 0 R>>endobj
4 0 obj<</Type/Font/Subtype/Type1/BaseFont/Helvetica>>endobj
5 0 obj<</Length 44>>
stream
BT /F1 12 Tf 100 700 Td (Hello PDF) Tj ET
endstream
endobj
xref
0 6
0000000000 65535 f
0000000009 00000 n
0000000058 00000 n
0000000115 00000 n
0000000266 00000 n
0000000340 00000 n
trailer<</Size 6/Root 1 0 R>>
startxref
434
%%EOF`
	pdfPath := filepath.Join(dir, "test.pdf")
	os.WriteFile(pdfPath, []byte(pdfContent), 0o644)

	input, _ := json.Marshal(map[string]string{"file_path": pdfPath})
	result, err := toolPdfRead(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("toolPdfRead: %v", err)
	}
	if !strings.Contains(result, "Hello PDF") {
		t.Errorf("expected 'Hello PDF' in result, got: %s", result)
	}
}
