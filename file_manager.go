package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// --- P23.3: File & Document Processing ---

// FileManagerConfig configures the file management system.
type FileManagerConfig struct {
	Enabled    bool   `json:"enabled"`
	StorageDir string `json:"storageDir,omitempty"` // default: {baseDir}/files
	MaxSizeMB  int    `json:"maxSizeMB,omitempty"`  // default: 50
}

func (c FileManagerConfig) storageDirOrDefault(baseDir string) string {
	if c.StorageDir != "" {
		return c.StorageDir
	}
	return filepath.Join(baseDir, "files")
}

func (c FileManagerConfig) maxSizeOrDefault() int {
	if c.MaxSizeMB > 0 {
		return c.MaxSizeMB
	}
	return 50
}

// globalFileManager is exposed for tool handlers.
var globalFileManager *FileManagerService

// ManagedFile represents a stored file entry.
type ManagedFile struct {
	ID           string `json:"id"`
	UserID       string `json:"userId"`
	Filename     string `json:"filename"`
	OriginalName string `json:"originalName"`
	Category     string `json:"category"`
	MimeType     string `json:"mimeType"`
	FileSize     int64  `json:"fileSize"`
	ContentHash  string `json:"contentHash"`
	StoragePath  string `json:"storagePath"`
	Source       string `json:"source"`
	SourceID     string `json:"sourceId"`
	Metadata     string `json:"metadata"`
	CreatedAt    string `json:"createdAt"`
	UpdatedAt    string `json:"updatedAt"`
}

// FileManagerService handles file storage, dedup, and organization.
type FileManagerService struct {
	dbPath     string
	storageDir string
	maxSizeMB  int
}

// newFileManagerService creates a new FileManagerService from config.
func newFileManagerService(cfg *Config) *FileManagerService {
	dir := cfg.FileManager.storageDirOrDefault(cfg.baseDir)
	os.MkdirAll(dir, 0o755)
	return &FileManagerService{
		dbPath:     cfg.HistoryDB,
		storageDir: dir,
		maxSizeMB:  cfg.FileManager.maxSizeOrDefault(),
	}
}

// initFileManagerDB creates the managed_files table.
func initFileManagerDB(dbPath string) error {
	sql := `CREATE TABLE IF NOT EXISTS managed_files (
		id TEXT PRIMARY KEY,
		user_id TEXT DEFAULT '',
		filename TEXT NOT NULL,
		original_name TEXT DEFAULT '',
		category TEXT DEFAULT 'general',
		mime_type TEXT DEFAULT '',
		file_size INTEGER DEFAULT 0,
		content_hash TEXT DEFAULT '',
		storage_path TEXT NOT NULL,
		source TEXT DEFAULT '',
		source_id TEXT DEFAULT '',
		metadata TEXT DEFAULT '{}',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_managed_files_hash ON managed_files(content_hash);
	CREATE INDEX IF NOT EXISTS idx_managed_files_category ON managed_files(category);
	CREATE INDEX IF NOT EXISTS idx_managed_files_user ON managed_files(user_id);`
	_, err := queryDB(dbPath, sql)
	return err
}

// contentHash computes a truncated SHA-256 hash of data.
func contentHash(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:16]) // first 32 hex chars
}

// mimeFromExt returns a MIME type based on file extension.
func mimeFromExt(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	mimeMap := map[string]string{
		".pdf":  "application/pdf",
		".txt":  "text/plain",
		".md":   "text/markdown",
		".html": "text/html",
		".htm":  "text/html",
		".json": "application/json",
		".xml":  "application/xml",
		".csv":  "text/csv",
		".jpg":  "image/jpeg",
		".jpeg": "image/jpeg",
		".png":  "image/png",
		".gif":  "image/gif",
		".webp": "image/webp",
		".svg":  "image/svg+xml",
		".mp3":  "audio/mpeg",
		".wav":  "audio/wav",
		".mp4":  "video/mp4",
		".zip":  "application/zip",
		".gz":   "application/gzip",
		".tar":  "application/x-tar",
		".doc":  "application/msword",
		".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		".xls":  "application/vnd.ms-excel",
		".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		".ppt":  "application/vnd.ms-powerpoint",
		".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	}
	if mime, ok := mimeMap[ext]; ok {
		return mime
	}
	return "application/octet-stream"
}

// StoreFile stores a file with content-hash dedup.
// If a file with the same hash already exists, returns existing record and skips storage.
func (s *FileManagerService) StoreFile(userID, filename, category, source, sourceID string, data []byte) (*ManagedFile, bool, error) {
	if int64(len(data)) > int64(s.maxSizeMB)*1024*1024 {
		return nil, false, fmt.Errorf("file exceeds max size of %d MB", s.maxSizeMB)
	}
	if category == "" {
		category = "general"
	}

	hash := contentHash(data)

	// Check for existing file with same hash.
	rows, err := queryDB(s.dbPath, fmt.Sprintf(
		"SELECT id, user_id, filename, original_name, category, mime_type, file_size, content_hash, storage_path, source, source_id, metadata, created_at, updated_at FROM managed_files WHERE content_hash = '%s' LIMIT 1",
		escapeSQLite(hash),
	))
	if err == nil && len(rows) > 0 {
		existing := rowToManagedFile(rows[0])
		return existing, true, nil // duplicate
	}

	// Organize storage path: {storageDir}/{category}/{YYYY-MM}/{filename}
	now := time.Now()
	relDir := filepath.Join(category, now.Format("2006-01"))
	absDir := filepath.Join(s.storageDir, relDir)
	os.MkdirAll(absDir, 0o755)

	// Ensure unique filename.
	storedName := uniqueFilename(absDir, filename)
	absPath := filepath.Join(absDir, storedName)

	if err := os.WriteFile(absPath, data, 0o644); err != nil {
		return nil, false, fmt.Errorf("write file: %w", err)
	}

	id := umNewID()
	mime := mimeFromExt(filename)
	nowStr := now.UTC().Format(time.RFC3339)

	sql := fmt.Sprintf(
		"INSERT INTO managed_files (id, user_id, filename, original_name, category, mime_type, file_size, content_hash, storage_path, source, source_id, metadata, created_at, updated_at) VALUES ('%s','%s','%s','%s','%s','%s',%d,'%s','%s','%s','%s','{}','%s','%s')",
		escapeSQLite(id),
		escapeSQLite(userID),
		escapeSQLite(storedName),
		escapeSQLite(filename),
		escapeSQLite(category),
		escapeSQLite(mime),
		len(data),
		escapeSQLite(hash),
		escapeSQLite(absPath),
		escapeSQLite(source),
		escapeSQLite(sourceID),
		nowStr,
		nowStr,
	)
	if _, err := queryDB(s.dbPath, sql); err != nil {
		os.Remove(absPath) // cleanup on DB failure
		return nil, false, fmt.Errorf("insert record: %w", err)
	}

	mf := &ManagedFile{
		ID:           id,
		UserID:       userID,
		Filename:     storedName,
		OriginalName: filename,
		Category:     category,
		MimeType:     mime,
		FileSize:     int64(len(data)),
		ContentHash:  hash,
		StoragePath:  absPath,
		Source:       source,
		SourceID:     sourceID,
		Metadata:     "{}",
		CreatedAt:    nowStr,
		UpdatedAt:    nowStr,
	}
	return mf, false, nil
}

// uniqueFilename returns a filename that doesn't conflict in dir.
func uniqueFilename(dir, name string) string {
	base := name
	if _, err := os.Stat(filepath.Join(dir, base)); os.IsNotExist(err) {
		return base
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 1; i < 1000; i++ {
		candidate := fmt.Sprintf("%s_%d%s", stem, i, ext)
		if _, err := os.Stat(filepath.Join(dir, candidate)); os.IsNotExist(err) {
			return candidate
		}
	}
	return fmt.Sprintf("%s_%d%s", stem, time.Now().UnixNano(), ext)
}

// GetFile retrieves a file record by ID.
func (s *FileManagerService) GetFile(id string) (*ManagedFile, error) {
	rows, err := queryDB(s.dbPath, fmt.Sprintf(
		"SELECT id, user_id, filename, original_name, category, mime_type, file_size, content_hash, storage_path, source, source_id, metadata, created_at, updated_at FROM managed_files WHERE id = '%s'",
		escapeSQLite(id),
	))
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("file not found: %s", id)
	}
	return rowToManagedFile(rows[0]), nil
}

// ListFiles lists files by optional category and user.
func (s *FileManagerService) ListFiles(category, userID string, limit int) ([]*ManagedFile, error) {
	if limit <= 0 {
		limit = 50
	}
	where := "1=1"
	if category != "" {
		where += fmt.Sprintf(" AND category = '%s'", escapeSQLite(category))
	}
	if userID != "" {
		where += fmt.Sprintf(" AND user_id = '%s'", escapeSQLite(userID))
	}
	rows, err := queryDB(s.dbPath, fmt.Sprintf(
		"SELECT id, user_id, filename, original_name, category, mime_type, file_size, content_hash, storage_path, source, source_id, metadata, created_at, updated_at FROM managed_files WHERE %s ORDER BY created_at DESC LIMIT %d",
		where, limit,
	))
	if err != nil {
		return nil, err
	}
	var files []*ManagedFile
	for _, row := range rows {
		files = append(files, rowToManagedFile(row))
	}
	return files, nil
}

// DeleteFile removes a file by ID (both DB record and disk file).
func (s *FileManagerService) DeleteFile(id string) error {
	mf, err := s.GetFile(id)
	if err != nil {
		return err
	}
	// Remove from disk.
	if mf.StoragePath != "" {
		os.Remove(mf.StoragePath)
	}
	_, err = queryDB(s.dbPath, fmt.Sprintf(
		"DELETE FROM managed_files WHERE id = '%s'",
		escapeSQLite(id),
	))
	return err
}

// OrganizeFile moves a file to a new category.
func (s *FileManagerService) OrganizeFile(id, newCategory string) (*ManagedFile, error) {
	mf, err := s.GetFile(id)
	if err != nil {
		return nil, err
	}
	if newCategory == "" {
		return nil, fmt.Errorf("new category is required")
	}

	// Build new path.
	created, _ := time.Parse(time.RFC3339, mf.CreatedAt)
	if created.IsZero() {
		created = time.Now()
	}
	newDir := filepath.Join(s.storageDir, newCategory, created.Format("2006-01"))
	os.MkdirAll(newDir, 0o755)
	newPath := filepath.Join(newDir, mf.Filename)

	// Move file on disk.
	if mf.StoragePath != "" {
		data, err := os.ReadFile(mf.StoragePath)
		if err != nil {
			return nil, fmt.Errorf("read file for move: %w", err)
		}
		if err := os.WriteFile(newPath, data, 0o644); err != nil {
			return nil, fmt.Errorf("write new location: %w", err)
		}
		os.Remove(mf.StoragePath)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	_, err = queryDB(s.dbPath, fmt.Sprintf(
		"UPDATE managed_files SET category = '%s', storage_path = '%s', updated_at = '%s' WHERE id = '%s'",
		escapeSQLite(newCategory),
		escapeSQLite(newPath),
		now,
		escapeSQLite(id),
	))
	if err != nil {
		return nil, err
	}

	mf.Category = newCategory
	mf.StoragePath = newPath
	mf.UpdatedAt = now
	return mf, nil
}

// FindDuplicates returns groups of files with the same content hash.
func (s *FileManagerService) FindDuplicates() ([][]ManagedFile, error) {
	rows, err := queryDB(s.dbPath,
		"SELECT content_hash, COUNT(*) as cnt FROM managed_files GROUP BY content_hash HAVING cnt > 1 ORDER BY cnt DESC LIMIT 100",
	)
	if err != nil {
		return nil, err
	}
	var groups [][]ManagedFile
	for _, row := range rows {
		hash := jsonStr(row["content_hash"])
		if hash == "" {
			continue
		}
		fileRows, err := queryDB(s.dbPath, fmt.Sprintf(
			"SELECT id, user_id, filename, original_name, category, mime_type, file_size, content_hash, storage_path, source, source_id, metadata, created_at, updated_at FROM managed_files WHERE content_hash = '%s'",
			escapeSQLite(hash),
		))
		if err != nil {
			continue
		}
		var group []ManagedFile
		for _, fr := range fileRows {
			group = append(group, *rowToManagedFile(fr))
		}
		if len(group) > 1 {
			groups = append(groups, group)
		}
	}
	return groups, nil
}

// ExtractPDF extracts text from a PDF file using pdftotext CLI.
func (s *FileManagerService) ExtractPDF(filePath string) (string, error) {
	cmd := exec.Command("pdftotext", filePath, "-")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("pdftotext: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// rowToManagedFile converts a DB row to ManagedFile.
func rowToManagedFile(row map[string]any) *ManagedFile {
	return &ManagedFile{
		ID:           jsonStr(row["id"]),
		UserID:       jsonStr(row["user_id"]),
		Filename:     jsonStr(row["filename"]),
		OriginalName: jsonStr(row["original_name"]),
		Category:     jsonStr(row["category"]),
		MimeType:     jsonStr(row["mime_type"]),
		FileSize:     int64(jsonFloat(row["file_size"])),
		ContentHash:  jsonStr(row["content_hash"]),
		StoragePath:  jsonStr(row["storage_path"]),
		Source:       jsonStr(row["source"]),
		SourceID:     jsonStr(row["source_id"]),
		Metadata:     jsonStr(row["metadata"]),
		CreatedAt:    jsonStr(row["created_at"]),
		UpdatedAt:    jsonStr(row["updated_at"]),
	}
}

// --- Tool Handlers ---

// toolPdfRead extracts text from a PDF file (by ID or path).
func toolPdfRead(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		FileID   string `json:"file_id"`
		FilePath string `json:"file_path"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	svc := globalFileManager
	if svc == nil {
		return "", fmt.Errorf("file manager not enabled")
	}

	var pdfPath string
	if args.FileID != "" {
		mf, err := svc.GetFile(args.FileID)
		if err != nil {
			return "", err
		}
		pdfPath = mf.StoragePath
	} else if args.FilePath != "" {
		pdfPath = args.FilePath
	} else {
		return "", fmt.Errorf("file_id or file_path is required")
	}

	text, err := svc.ExtractPDF(pdfPath)
	if err != nil {
		return "", err
	}
	if len(text) > 50000 {
		text = text[:50000] + "\n... (truncated)"
	}
	return fmt.Sprintf("PDF text extracted (%d chars):\n\n%s", len(text), text), nil
}

// toolDocSummarize reads a document and returns a structured summary.
func toolDocSummarize(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		FileID   string `json:"file_id"`
		FilePath string `json:"file_path"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	svc := globalFileManager
	if svc == nil {
		return "", fmt.Errorf("file manager not enabled")
	}

	var content string
	var filename string
	var mimeType string

	if args.FileID != "" {
		mf, err := svc.GetFile(args.FileID)
		if err != nil {
			return "", err
		}
		filename = mf.OriginalName
		mimeType = mf.MimeType
		if mf.MimeType == "application/pdf" {
			text, err := svc.ExtractPDF(mf.StoragePath)
			if err != nil {
				return "", fmt.Errorf("extract pdf: %w", err)
			}
			content = text
		} else {
			data, err := os.ReadFile(mf.StoragePath)
			if err != nil {
				return "", fmt.Errorf("read file: %w", err)
			}
			content = string(data)
		}
	} else if args.FilePath != "" {
		filename = filepath.Base(args.FilePath)
		mimeType = mimeFromExt(filename)
		if mimeType == "application/pdf" {
			text, err := svc.ExtractPDF(args.FilePath)
			if err != nil {
				return "", fmt.Errorf("extract pdf: %w", err)
			}
			content = text
		} else {
			data, err := os.ReadFile(args.FilePath)
			if err != nil {
				return "", fmt.Errorf("read file: %w", err)
			}
			content = string(data)
		}
	} else {
		return "", fmt.Errorf("file_id or file_path is required")
	}

	// Truncate for summary.
	if len(content) > 100000 {
		content = content[:100000]
	}

	lines := strings.Split(content, "\n")
	wordCount := 0
	for _, line := range lines {
		wordCount += len(strings.Fields(line))
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Document: %s\n", filename))
	sb.WriteString(fmt.Sprintf("Type: %s\n", mimeType))
	sb.WriteString(fmt.Sprintf("Lines: %d\n", len(lines)))
	sb.WriteString(fmt.Sprintf("Words: ~%d\n", wordCount))
	sb.WriteString(fmt.Sprintf("Characters: %d\n\n", len(content)))

	// Extract first 20 lines as preview.
	previewLines := 20
	if len(lines) < previewLines {
		previewLines = len(lines)
	}
	sb.WriteString("Preview (first lines):\n")
	for i := 0; i < previewLines; i++ {
		sb.WriteString(lines[i])
		sb.WriteString("\n")
	}
	if len(lines) > previewLines {
		sb.WriteString(fmt.Sprintf("... (%d more lines)\n", len(lines)-previewLines))
	}

	return sb.String(), nil
}

// toolFileOrganize moves a file to a new category.
func toolFileOrganize(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		FileID   string `json:"file_id"`
		Category string `json:"category"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.FileID == "" {
		return "", fmt.Errorf("file_id is required")
	}
	if args.Category == "" {
		return "", fmt.Errorf("category is required")
	}

	svc := globalFileManager
	if svc == nil {
		return "", fmt.Errorf("file manager not enabled")
	}

	mf, err := svc.OrganizeFile(args.FileID, args.Category)
	if err != nil {
		return "", err
	}

	out, _ := json.MarshalIndent(mf, "", "  ")
	return fmt.Sprintf("File organized to category '%s':\n%s", args.Category, string(out)), nil
}

// toolFileList lists managed files.
func toolFileList(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Category string `json:"category"`
		UserID   string `json:"user_id"`
		Limit    int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	svc := globalFileManager
	if svc == nil {
		return "", fmt.Errorf("file manager not enabled")
	}

	files, err := svc.ListFiles(args.Category, args.UserID, args.Limit)
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "No files found.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Files (%d):\n\n", len(files)))
	for _, f := range files {
		sb.WriteString(fmt.Sprintf("- %s | %s | %s | %s | %d bytes | %s\n",
			f.ID[:8], f.OriginalName, f.Category, f.MimeType, f.FileSize, f.CreatedAt))
	}
	return sb.String(), nil
}

// toolFileDuplicates finds duplicate files by content hash.
func toolFileDuplicates(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	svc := globalFileManager
	if svc == nil {
		return "", fmt.Errorf("file manager not enabled")
	}

	groups, err := svc.FindDuplicates()
	if err != nil {
		return "", err
	}
	if len(groups) == 0 {
		return "No duplicate files found.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d duplicate groups:\n\n", len(groups)))
	for i, group := range groups {
		sb.WriteString(fmt.Sprintf("Group %d (hash: %s, %d files):\n", i+1, group[0].ContentHash[:16], len(group)))
		for _, f := range group {
			sb.WriteString(fmt.Sprintf("  - %s | %s | %s | %d bytes\n", f.ID[:8], f.OriginalName, f.Category, f.FileSize))
		}
		sb.WriteString("\n")
	}
	return sb.String(), nil
}

// toolFileStore stores file content (base64 or text) into the file manager.
func toolFileStore(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Filename string `json:"filename"`
		Content  string `json:"content"`
		Base64   string `json:"base64"`
		Category string `json:"category"`
		UserID   string `json:"user_id"`
		Source   string `json:"source"`
		SourceID string `json:"source_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Filename == "" {
		return "", fmt.Errorf("filename is required")
	}

	svc := globalFileManager
	if svc == nil {
		return "", fmt.Errorf("file manager not enabled")
	}

	var data []byte
	if args.Base64 != "" {
		var err error
		data, err = base64.StdEncoding.DecodeString(args.Base64)
		if err != nil {
			return "", fmt.Errorf("invalid base64: %w", err)
		}
	} else if args.Content != "" {
		data = []byte(args.Content)
	} else {
		return "", fmt.Errorf("content or base64 is required")
	}

	mf, isDup, err := svc.StoreFile(args.UserID, args.Filename, args.Category, args.Source, args.SourceID, data)
	if err != nil {
		return "", err
	}

	status := "stored"
	if isDup {
		status = "duplicate (existing file returned)"
	}
	out, _ := json.MarshalIndent(mf, "", "  ")
	return fmt.Sprintf("File %s (%s):\n%s", status, args.Filename, string(out)), nil
}
