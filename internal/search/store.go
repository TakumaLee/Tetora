package search

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// IntelStore handles tiered storage for search results.
type IntelStore struct {
	dbPath  string
	baseDir string
}

func NewIntelStore(baseDir string) (*IntelStore, error) {
	dbPath := filepath.Join(baseDir, "dbs", "intel.db")
	_ = os.MkdirAll(filepath.Dir(dbPath), 0755)

	s := &IntelStore{
		dbPath:  dbPath,
		baseDir: baseDir,
	}

	if err := s.initDB(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *IntelStore) initDB() error {
	schema := `
	PRAGMA journal_mode=WAL;
	PRAGMA busy_timeout=5000;
	CREATE TABLE IF NOT EXISTS intel_metadata (
		query_hash TEXT,
		result_id TEXT PRIMARY KEY,
		url TEXT,
		file_path TEXT,
		expire_at DATETIME,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_expire ON intel_metadata(expire_at);
	CREATE INDEX IF NOT EXISTS idx_query ON intel_metadata(query_hash);
	`
	cmd := exec.Command("sqlite3", s.dbPath, schema)
	return cmd.Run()
}

// SaveResult persists metadata to SQLite and raw JSON to the filesystem.
func (s *IntelStore) SaveResult(query string, results []SearchResult, ttl time.Duration) error {
	queryHash := hashString(query)
	today := time.Now().Format("2006-01-02")
	dir := filepath.Join(s.baseDir, "workspace", "intel", today)
	_ = os.MkdirAll(dir, 0755)

	expireAt := time.Now().Add(ttl)

	for _, r := range results {
		// 1. Write to Filesystem
		fileName := fmt.Sprintf("%s.json", r.ID)
		filePath := filepath.Join("workspace", "intel", today, fileName)
		absPath := filepath.Join(s.baseDir, filePath)
		
		data, _ := json.MarshalIndent(r, "", "  ")
		if err := os.WriteFile(absPath, data, 0644); err != nil {
			return err
		}

		// 2. Save Metadata to SQLite
		sql := fmt.Sprintf("INSERT OR REPLACE INTO intel_metadata (query_hash, result_id, url, file_path, expire_at) VALUES ('%s', '%s', '%s', '%s', '%s');",
			queryHash, r.ID, escapeSQL(r.URL), filePath, expireAt.Format(time.RFC3339))
		
		_ = exec.Command("sqlite3", s.dbPath, sql).Run()
	}
	return nil
}

func (s *IntelStore) Prune() error {
	now := time.Now().Format(time.RFC3339)
	// Get files to delete before removing from DB
	cmd := exec.Command("sqlite3", s.dbPath, fmt.Sprintf("SELECT file_path FROM intel_metadata WHERE expire_at < '%s';", now))
	out, _ := cmd.Output()
	
	for _, line := range strings.Split(string(out), "\n") {
		path := strings.TrimSpace(line)
		if path != "" {
			_ = os.Remove(filepath.Join(s.baseDir, path))
		}
	}

	return exec.Command("sqlite3", s.dbPath, fmt.Sprintf("DELETE FROM intel_metadata WHERE expire_at < '%s';", now)).Run()
}

func hashString(s string) string {
	h := sha256.New()
	h.Write([]byte(strings.ToLower(strings.TrimSpace(s))))
	return hex.EncodeToString(h.Sum(nil))
}

func escapeSQL(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}
