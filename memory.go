package main

import (
	"encoding/json"
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
	Priority  string `json:"priority,omitempty"` // P0=permanent, P1=active(default), P2=stale
	UpdatedAt string `json:"updatedAt"`
}

// parseMemoryFrontmatter extracts priority from YAML-like frontmatter.
// Returns the priority string and the body without frontmatter.
// If no frontmatter is present, returns "P1" (default) and the full data.
func parseMemoryFrontmatter(data []byte) (priority string, body string) {
	s := string(data)
	if !strings.HasPrefix(s, "---\n") {
		return "P1", s
	}
	end := strings.Index(s[4:], "\n---\n")
	if end < 0 {
		return "P1", s
	}
	front := s[4 : 4+end]
	body = s[4+end+5:] // skip past closing "---\n"

	// Parse simple key: value pairs from frontmatter.
	priority = "P1"
	for _, line := range strings.Split(front, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "priority:") {
			val := strings.TrimSpace(strings.TrimPrefix(line, "priority:"))
			if val == "P0" || val == "P1" || val == "P2" {
				priority = val
			}
		}
	}
	return priority, body
}

// buildMemoryFrontmatter creates frontmatter + body content.
func buildMemoryFrontmatter(priority, body string) string {
	if priority == "" || priority == "P1" {
		// P1 is default — omit frontmatter for backward compatibility.
		return body
	}
	return "---\npriority: " + priority + "\n---\n" + body
}

// --- Get ---

// getMemory reads workspace/memory/{key}.md, stripping any frontmatter.
func getMemory(cfg *Config, role, key string) (string, error) {
	path := filepath.Join(cfg.WorkspaceDir, "memory", sanitizeKey(key)+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil // missing = empty, not error
	}
	_, body := parseMemoryFrontmatter(data)
	return body, nil
}

// --- Set (Write) ---

// setMemory writes workspace/memory/{key}.md, preserving existing priority if not specified.
// priority is optional — pass "" to preserve existing, or "P0"/"P1"/"P2" to set.
func setMemory(cfg *Config, role, key, value string, priority ...string) error {
	dir := filepath.Join(cfg.WorkspaceDir, "memory")
	os.MkdirAll(dir, 0o755)

	path := filepath.Join(dir, sanitizeKey(key)+".md")

	// Determine priority: explicit arg > existing frontmatter > default P1.
	pri := ""
	if len(priority) > 0 && priority[0] != "" {
		pri = priority[0]
	} else {
		// Preserve existing priority if file exists.
		if existing, err := os.ReadFile(path); err == nil {
			pri, _ = parseMemoryFrontmatter(existing)
		}
	}

	content := buildMemoryFrontmatter(pri, value)
	return os.WriteFile(path, []byte(content), 0o644)
}

// --- List ---

// listMemory lists all memory files, parsing priority from frontmatter.
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
		priority, body := parseMemoryFrontmatter(data)
		info, _ := e.Info()
		updatedAt := ""
		if info != nil {
			updatedAt = info.ModTime().Format(time.RFC3339)
		}
		result = append(result, MemoryEntry{
			Key:       key,
			Value:     body,
			Priority:  priority,
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

// --- Access Tracking ---

// recordMemoryAccess updates the last-access timestamp for a memory key.
func recordMemoryAccess(cfg *Config, key string) {
	if cfg == nil || cfg.WorkspaceDir == "" {
		return
	}
	accessLog := loadMemoryAccessLog(cfg)
	accessLog[sanitizeKey(key)] = time.Now().UTC().Format(time.RFC3339)
	saveMemoryAccessLog(cfg, accessLog)
}

// loadMemoryAccessLog reads workspace/memory/.access.json.
func loadMemoryAccessLog(cfg *Config) map[string]string {
	result := make(map[string]string)
	if cfg == nil || cfg.WorkspaceDir == "" {
		return result
	}
	path := filepath.Join(cfg.WorkspaceDir, "memory", ".access.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return result
	}
	json.Unmarshal(data, &result)
	return result
}

// saveMemoryAccessLog writes workspace/memory/.access.json.
func saveMemoryAccessLog(cfg *Config, accessLog map[string]string) {
	if cfg == nil || cfg.WorkspaceDir == "" {
		return
	}
	dir := filepath.Join(cfg.WorkspaceDir, "memory")
	os.MkdirAll(dir, 0o755)
	data, err := json.MarshalIndent(accessLog, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(filepath.Join(dir, ".access.json"), data, 0o644)
}

// initMemoryDB is a no-op kept for backward compatibility.
func initMemoryDB(dbPath string) error {
	return nil
}
