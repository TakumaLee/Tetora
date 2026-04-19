package warroom

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Mu serialises concurrent writes to status.json across all writers
// (cron autoupdater, Discord bot, HTTP API). Every writer acquires this
// before calling LoadStatus/SaveStatus so the multi-writer design stays
// race-free. AppendIntel (HTTP + Discord /wr intel) also holds it while
// reading/writing the md file and status.json in a single critical section.
var Mu sync.Mutex

// StatusPath returns the canonical path to war-room status.json given a
// tetora base directory (typically cfg.BaseDir, i.e. ~/.tetora).
func StatusPath(baseDir string) string {
	return filepath.Join(baseDir, "workspace", "memory", "war-room", "status.json")
}

// Status is the top-level structure of war-room/status.json.
type Status struct {
	SchemaVersion int               `json:"schema_version"`
	GeneratedAt   string            `json:"generated_at"`
	Fronts        []json.RawMessage `json:"fronts"`
}

// LoadStatus reads and parses status.json. Each front is preserved as
// json.RawMessage so unknown fields are never dropped.
func LoadStatus(path string) (*Status, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read status.json: %w", err)
	}
	var s Status
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse status.json: %w", err)
	}
	return &s, nil
}

// SaveStatus marshals s and writes it atomically via a temp file + rename.
// GeneratedAt is updated to the current UTC time before marshalling.
func SaveStatus(path string, s *Status) error {
	s.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal status.json: %w", err)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "status-*.json.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}

// FrontID extracts the "id" field from a raw front JSON message.
func FrontID(raw json.RawMessage) (string, error) {
	var m struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", fmt.Errorf("peek id: %w", err)
	}
	return m.ID, nil
}

// FrontField extracts a single field from a raw front JSON message.
func FrontField(raw json.RawMessage, key string, out any) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("parse front: %w", err)
	}
	v, ok := m[key]
	if !ok {
		return fmt.Errorf("field %q not found", key)
	}
	if err := json.Unmarshal(v, out); err != nil {
		return fmt.Errorf("unmarshal field %q: %w", key, err)
	}
	return nil
}

// UpdateFrontFields merges updates into raw without dropping unknown fields.
// Existing fields not present in updates are preserved byte-for-byte.
func UpdateFrontFields(raw json.RawMessage, updates map[string]any) (json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse front: %w", err)
	}
	for k, v := range updates {
		encoded, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("marshal field %q: %w", k, err)
		}
		m[k] = json.RawMessage(encoded)
	}
	result, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("re-marshal front: %w", err)
	}
	return result, nil
}
