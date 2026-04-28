package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Load reads the active provider state from disk.
// Uses file locking via os.OpenFile with O_EXCL on Windows.
func (s *ActiveProviderStore) Load() (*ActiveProviderState, error) {
	f, err := os.Open(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return &ActiveProviderState{}, nil
		}
		return nil, err
	}
	defer f.Close()

	var state ActiveProviderState
	if err := json.NewDecoder(f).Decode(&state); err != nil {
		return nil, err
	}

	return &state, nil
}

// Save persists the active provider state to disk.
// Uses file locking via os.OpenFile with O_EXCL on Windows.
func (s *ActiveProviderStore) Save(state *ActiveProviderState) error {
	// Ensure directory exists.
	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	// Create temp file first, then rename to avoid race conditions.
	tempPath := s.filePath + ".tmp"
	f, err := os.OpenFile(tempPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	if _, err := f.Write(data); err != nil {
		return err
	}

	// Rename temp file to final location.
	if err := os.Rename(tempPath, s.filePath); err != nil {
		return err
	}

	return nil
}
