package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
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
// Thread-safe file-based storage.
type ActiveProviderStore struct {
	mu       sync.RWMutex
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
func (s *ActiveProviderStore) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			s.state = &ActiveProviderState{}
			return nil
		}
		return err
	}

	var state ActiveProviderState
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}

	s.state = &state
	return nil
}

// Save persists the active provider state to disk.
func (s *ActiveProviderStore) Save(state *ActiveProviderState) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Ensure directory exists.
	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(s.filePath, data, 0644); err != nil {
		return err
	}

	s.state = state
	return nil
}

// Get returns the current active provider state (thread-safe).
func (s *ActiveProviderStore) Get() *ActiveProviderState {
	s.mu.RLock()
	defer s.mu.RUnlock()

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
