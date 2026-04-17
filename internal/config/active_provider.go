package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// ActiveProviderState tracks the runtime-active provider that overrides
// all agent-level provider configurations. This enables users to switch
// providers dynamically without modifying individual agent configs.
type ActiveProviderState struct {
	ProviderName string    `json:"providerName"`
	Model        string    `json:"model,omitempty"` // "auto" or specific model
	SetAt        time.Time `json:"setAt"`
	SetBy        string    `json:"setBy,omitempty"` // CLI, API, etc.
}

// ActiveProviderStore manages the active provider override state.
// Uses file-level locking (flock) to protect against concurrent access
// across multiple tetora processes (e.g., daemon + CLI).
type ActiveProviderStore struct {
	state    *ActiveProviderState
	filePath string
}

// NewActiveProviderStore creates a new store backed by the given file path.
func NewActiveProviderStore(filePath string) *ActiveProviderStore {
	return &ActiveProviderStore{
		filePath: filePath,
		state:    &ActiveProviderState{},
	}
}

// Load reads the active provider state from disk.
// Uses shared lock for concurrent read safety.
func (s *ActiveProviderStore) Load() error {
	f, err := os.Open(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			s.state = &ActiveProviderState{}
			return nil
		}
		return err
	}
	defer f.Close()

	// Acquire shared read lock.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	var state ActiveProviderState
	if err := json.NewDecoder(f).Decode(&state); err != nil {
		return err
	}

	s.state = &state
	return nil
}

// Save persists the active provider state to disk.
// Uses exclusive lock to prevent concurrent writes.
func (s *ActiveProviderStore) Save(state *ActiveProviderState) error {
	// Ensure directory exists.
	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	// Open with exclusive lock.
	f, err := os.OpenFile(s.filePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	// Acquire exclusive write lock.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	if _, err := f.Write(data); err != nil {
		return err
	}

	s.state = state
	return nil
}

// Get returns the current active provider state (thread-safe).
func (s *ActiveProviderStore) Get() *ActiveProviderState {
	if s.state == nil {
		return &ActiveProviderState{}
	}

	// Return a copy to prevent external modification.
	cpy := *s.state
	return &cpy
}

// Set updates the active provider state and persists to disk.
func (s *ActiveProviderStore) Set(providerName, model, setBy string) error {
	state := &ActiveProviderState{
		ProviderName: providerName,
		Model:        model,
		SetAt:        time.Now(),
		SetBy:        setBy,
	}
	return s.Save(state)
}

// Clear removes the active provider override.
func (s *ActiveProviderStore) Clear() error {
	return s.Save(&ActiveProviderState{})
}

// HasActiveOverride returns true if an active provider override is set.
func (s *ActiveProviderStore) HasActiveOverride() bool {
	state := s.Get()
	return state.ProviderName != ""
}
