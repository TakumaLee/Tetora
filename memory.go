package main

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// --- Agent Memory Types ---

// MemoryEntry represents a key-value memory entry.
type MemoryEntry struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	UpdatedAt string `json:"updatedAt"`
}

// --- Get ---

// getMemory reads workspace/memory/{key}.md
func getMemory(cfg *Config, role, key string) (string, error) {
	path := filepath.Join(cfg.WorkspaceDir, "memory", sanitizeKey(key)+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil // missing = empty, not error
	}
	return string(data), nil
}

// --- Set (Write) ---

// setMemory writes workspace/memory/{key}.md
func setMemory(cfg *Config, role, key, value string) error {
	dir := filepath.Join(cfg.WorkspaceDir, "memory")
	os.MkdirAll(dir, 0o755)
	return os.WriteFile(filepath.Join(dir, sanitizeKey(key)+".md"), []byte(value), 0o644)
}

// --- List ---

// listMemory lists all memory files.
func listMemory(cfg *Config, role string) ([]MemoryEntry, error) {
	dir := filepath.Join(cfg.WorkspaceDir, "memory")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var result []MemoryEntry
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		key := strings.TrimSuffix(e.Name(), ".md")
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		info, _ := e.Info()
		updatedAt := ""
		if info != nil {
			updatedAt = info.ModTime().Format(time.RFC3339)
		}
		result = append(result, MemoryEntry{
			Key:       key,
			Value:     string(data),
			UpdatedAt: updatedAt,
		})
	}
	return result, nil
}

// --- Delete ---

// deleteMemory removes workspace/memory/{key}.md
func deleteMemory(cfg *Config, role, key string) error {
	path := filepath.Join(cfg.WorkspaceDir, "memory", sanitizeKey(key)+".md")
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// --- Search ---

// searchMemory searches memory files by content.
func searchMemoryFS(cfg *Config, role, query string) ([]MemoryEntry, error) {
	all, err := listMemory(cfg, role)
	if err != nil {
		return nil, err
	}
	query = strings.ToLower(query)
	var results []MemoryEntry
	for _, e := range all {
		if strings.Contains(strings.ToLower(e.Key), query) ||
			strings.Contains(strings.ToLower(e.Value), query) {
			results = append(results, e)
		}
	}
	return results, nil
}

// sanitizeKey sanitizes a memory key for use as a filename.
func sanitizeKey(key string) string {
	// Replace path separators and other unsafe chars.
	r := strings.NewReplacer("/", "_", "\\", "_", "..", "_", "\x00", "")
	return r.Replace(key)
}

// initMemoryDB is a no-op kept for backward compatibility.
func initMemoryDB(dbPath string) error {
	return nil
}
