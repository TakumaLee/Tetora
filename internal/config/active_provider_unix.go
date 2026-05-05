//go:build !windows

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
)

// Load reads the active provider state from disk and caches it in memory.
// Uses a shared flock for concurrent read safety across processes.
func (s *ActiveProviderStore) Load() (*ActiveProviderState, error) {
	f, err := os.Open(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			s.mu.Lock()
			s.state = &ActiveProviderState{}
			s.mu.Unlock()
			return &ActiveProviderState{}, nil
		}
		return nil, err
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH); err != nil {
		return nil, err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:errcheck

	var alias activeProviderStateAlias
	if err := json.NewDecoder(f).Decode(&alias); err != nil {
		return nil, err
	}
	state := alias.toState()

	s.mu.Lock()
	s.state = state
	s.mu.Unlock()
	return state, nil
}

// LoadFromFile reads the active provider state fresh from disk without updating the cache.
// Use this when you need to see changes made by CLI while the daemon is running.
func (s *ActiveProviderStore) LoadFromFile() (*ActiveProviderState, error) {
	f, err := os.Open(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return &ActiveProviderState{}, nil
		}
		return nil, err
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH); err != nil {
		return nil, err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:errcheck

	var alias activeProviderStateAlias
	if err := json.NewDecoder(f).Decode(&alias); err != nil {
		return nil, err
	}
	return alias.toState(), nil
}

// Save persists the active provider state to disk atomically.
// Writes to a temp file then renames to avoid a TOCTOU race where a reader
// could open a truncated (empty) file before the write flock is acquired.
// The rename(2) call is atomic on POSIX filesystems.
func (s *ActiveProviderStore) Save(state *ActiveProviderState) error {
	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	f, err := os.CreateTemp(dir, ".active-provider-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := f.Name()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return err
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:errcheck
		f.Close()
		os.Remove(tmpPath)
		return err
	}

	if _, err := f.Write(data); err != nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:errcheck
		f.Close()
		os.Remove(tmpPath)
		return err
	}

	syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:errcheck
	f.Close()

	if err := os.Rename(tmpPath, s.filePath); err != nil {
		os.Remove(tmpPath)
		return err
	}

	s.mu.Lock()
	s.state = state
	s.mu.Unlock()
	return nil
}
