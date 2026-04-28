// Package storage implements file storage with content-hash dedup,
// category organization, and metadata tracking.
package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"tetora/internal/life/lifedb"
)

// --- Types ---

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

// UUIDFn generates a new UUID string.
type UUIDFn func() string

// --- Service ---

// Service handles file storage, dedup, and organization.
type Service struct {
	dbPath     string
	storageDir string
	maxSizeMB  int
	db         lifedb.DB
	uuidFn     UUIDFn
}

// New creates a new file management Service.
func New(dbPath, storageDir string, maxSizeMB int, db lifedb.DB, uuidFn UUIDFn) *Service {
	os.MkdirAll(storageDir, 0o755)
	return &Service{
		dbPath:     dbPath,
		storageDir: storageDir,
		maxSizeMB:  maxSizeMB,
		db:         db,
		uuidFn:     uuidFn,
	}
}

// DBPath returns the database path.
func (s *Service) DBPath() string {
	return s.dbPath
}

// --- DB Initialization ---

// InitDB creates the managed_files table.
func InitDB(dbPath string) error {
	ddl := `CREATE TABLE IF NOT EXISTS managed_files (
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
	cmd := exec.Command("sqlite3", dbPath)
	cmd.Stdin = strings.NewReader(ddl)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("init managed_files table: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// --- File Operations ---

// ContentHash computes a truncated SHA-256 hash of data.
func ContentHash(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:16])
}

// MimeFromExt returns a MIME type based on file extension.
func MimeFromExt(filename string) string {
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
func (s *Service) StoreFile(userID, filename, category, source, sourceID string, data []byte) (*ManagedFile, bool, error) {
	if int64(len(data)) > int64(s.maxSizeMB)*1024*1024 {
		return nil, false, fmt.Errorf("file exceeds max size of %d MB", s.maxSizeMB)
	}
	if category == "" {
		category = "general"
	}

	hash := ContentHash(data)

	rows, err := s.db.Query(s.dbPath, fmt.Sprintf(
		"SELECT id, user_id, filename, original_name, category, mime_type, file_size, content_hash, storage_path, source, source_id, metadata, created_at, updated_at FROM managed_files WHERE content_hash = '%s' LIMIT 1",
		s.db.Escape(hash),
	))
	if err == nil && len(rows) > 0 {
		existing := rowToManagedFile(rows[0])
		return existing, true, nil
	}

	now := time.Now()
	relDir := filepath.Join(category, now.Format("2006-01"))
	absDir := filepath.Join(s.storageDir, relDir)
	os.MkdirAll(absDir, 0o755)

	storedName := uniqueFilename(absDir, filename)
	absPath := filepath.Join(absDir, storedName)

	if err := os.WriteFile(absPath, data, 0o644); err != nil {
		return nil, false, fmt.Errorf("write file: %w", err)
	}

	id := s.uuidFn()
	mime := MimeFromExt(filename)
	nowStr := now.UTC().Format(time.RFC3339)

	sql := fmt.Sprintf(
		"INSERT INTO managed_files (id, user_id, filename, original_name, category, mime_type, file_size, content_hash, storage_path, source, source_id, metadata, created_at, updated_at) VALUES ('%s','%s','%s','%s','%s','%s',%d,'%s','%s','%s','%s','{}','%s','%s')",
		s.db.Escape(id),
		s.db.Escape(userID),
		s.db.Escape(storedName),
		s.db.Escape(filename),
		s.db.Escape(category),
		s.db.Escape(mime),
		len(data),
		s.db.Escape(hash),
		s.db.Escape(absPath),
		s.db.Escape(source),
		s.db.Escape(sourceID),
		nowStr,
		nowStr,
	)
	if _, err := s.db.Query(s.dbPath, sql); err != nil {
		os.Remove(absPath)
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
func (s *Service) GetFile(id string) (*ManagedFile, error) {
	rows, err := s.db.Query(s.dbPath, fmt.Sprintf(
		"SELECT id, user_id, filename, original_name, category, mime_type, file_size, content_hash, storage_path, source, source_id, metadata, created_at, updated_at FROM managed_files WHERE id = '%s'",
		s.db.Escape(id),
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
func (s *Service) ListFiles(category, userID string, limit int) ([]*ManagedFile, error) {
	if limit <= 0 {
		limit = 50
	}
	where := "1=1"
	if category != "" {
		where += fmt.Sprintf(" AND category = '%s'", s.db.Escape(category))
	}
	if userID != "" {
		where += fmt.Sprintf(" AND user_id = '%s'", s.db.Escape(userID))
	}
	rows, err := s.db.Query(s.dbPath, fmt.Sprintf(
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
func (s *Service) DeleteFile(id string) error {
	mf, err := s.GetFile(id)
	if err != nil {
		return err
	}
	if mf.StoragePath != "" {
		os.Remove(mf.StoragePath)
	}
	_, err = s.db.Query(s.dbPath, fmt.Sprintf(
		"DELETE FROM managed_files WHERE id = '%s'",
		s.db.Escape(id),
	))
	return err
}

// OrganizeFile moves a file to a new category.
func (s *Service) OrganizeFile(id, newCategory string) (*ManagedFile, error) {
	mf, err := s.GetFile(id)
	if err != nil {
		return nil, err
	}
	if newCategory == "" {
		return nil, fmt.Errorf("new category is required")
	}

	created, _ := time.Parse(time.RFC3339, mf.CreatedAt)
	if created.IsZero() {
		created = time.Now()
	}
	newDir := filepath.Join(s.storageDir, newCategory, created.Format("2006-01"))
	os.MkdirAll(newDir, 0o755)
	newPath := filepath.Join(newDir, mf.Filename)

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
	_, err = s.db.Query(s.dbPath, fmt.Sprintf(
		"UPDATE managed_files SET category = '%s', storage_path = '%s', updated_at = '%s' WHERE id = '%s'",
		s.db.Escape(newCategory),
		s.db.Escape(newPath),
		now,
		s.db.Escape(id),
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
func (s *Service) FindDuplicates() ([][]ManagedFile, error) {
	rows, err := s.db.Query(s.dbPath,
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
		fileRows, err := s.db.Query(s.dbPath, fmt.Sprintf(
			"SELECT id, user_id, filename, original_name, category, mime_type, file_size, content_hash, storage_path, source, source_id, metadata, created_at, updated_at FROM managed_files WHERE content_hash = '%s'",
			s.db.Escape(hash),
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
func (s *Service) ExtractPDF(filePath string) (string, error) {
	cmd := exec.Command("pdftotext", filePath, "-")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("pdftotext: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// --- Helpers ---

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

func jsonStr(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", val)
	}
}

func jsonFloat(v any) float64 {
	switch val := v.(type) {
	case float64:
		return val
	case string:
		var f float64
		fmt.Sscanf(val, "%f", &f)
		return f
	default:
		return 0
	}
}
