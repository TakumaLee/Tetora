package config

import (
	"sync"
	"time"
)

// ActiveProviderState tracks the runtime-active provider that overrides
// all agent-level provider configurations. This enables users to switch
// providers dynamically without modifying individual agent configs.
//
// Both camelCase and snake_case JSON keys are accepted for backward
// compatibility with files written by older versions of Tetora.
type ActiveProviderState struct {
	ProviderName string    `json:"providerName"`
	Model        string    `json:"model,omitempty"`
	SetAt        time.Time `json:"setAt"`
	SetBy        string    `json:"setBy,omitempty"`
}

// activeProviderStateAlias is used during JSON decoding to handle legacy
// snake_case field names written by older Tetora versions.
type activeProviderStateAlias struct {
	ProviderName     string    `json:"providerName"`
	ProviderNameSnake string   `json:"provider_name"`
	Model            string    `json:"model"`
	SetAt            time.Time `json:"setAt"`
	SetAtSnake       time.Time `json:"set_at"`
	SetBy            string    `json:"setBy"`
	SetBySnake       string    `json:"set_by"`
}

func (a *activeProviderStateAlias) toState() *ActiveProviderState {
	s := &ActiveProviderState{
		ProviderName: a.ProviderName,
		Model:        a.Model,
		SetAt:        a.SetAt,
		SetBy:        a.SetBy,
	}
	if s.ProviderName == "" {
		s.ProviderName = a.ProviderNameSnake
	}
	if s.SetAt.IsZero() {
		s.SetAt = a.SetAtSnake
	}
	if s.SetBy == "" {
		s.SetBy = a.SetBySnake
	}
	return s
}

// ActiveProviderStore manages the active provider override state.
// Uses file-level locking for cross-process safety and a sync.RWMutex
// for in-process goroutine safety (e.g., daemon goroutines + HTTP handlers).
// Platform-specific Load/LoadFromFile/Save methods are in active_provider_unix.go
// and active_provider_windows.go.
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

// Get returns a copy of the current in-memory active provider state (thread-safe).
func (s *ActiveProviderStore) Get() *ActiveProviderState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.state == nil {
		return &ActiveProviderState{}
	}
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
	return s.Get().ProviderName != ""
}
