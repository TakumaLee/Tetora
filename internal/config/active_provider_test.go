package config

import (
	"path/filepath"
	"testing"
	"time"
)

func TestActiveProviderStore_SaveAndLoad(t *testing.T) {
	// Create temp directory.
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "active-provider.json")

	store := NewActiveProviderStore(storePath)

	// Test saving.
	err := store.Set("qwen", "qwen-plus", "test")
	if err != nil {
		t.Fatalf("failed to save: %v", err)
	}

	// Test loading.
	err = store.Load()
	if err != nil {
		t.Fatalf("failed to load: %v", err)
	}

	state := store.Get()
	if state.ProviderName != "qwen" {
		t.Errorf("expected provider 'qwen', got '%s'", state.ProviderName)
	}
	if state.Model != "qwen-plus" {
		t.Errorf("expected model 'qwen-plus', got '%s'", state.Model)
	}
	if state.SetBy != "test" {
		t.Errorf("expected setBy 'test', got '%s'", state.SetBy)
	}
	if state.SetAt.IsZero() {
		t.Error("expected non-zero SetAt")
	}
}

func TestActiveProviderStore_Clear(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "active-provider.json")
	store := NewActiveProviderStore(storePath)

	// Set a value.
	store.Set("qwen", "auto", "test")

	// Clear it.
	err := store.Clear()
	if err != nil {
		t.Fatalf("failed to clear: %v", err)
	}

	// Load and verify.
	store.Load()
	state := store.Get()
	if state.ProviderName != "" {
		t.Errorf("expected empty providerName, got '%s'", state.ProviderName)
	}
}

func TestActiveProviderStore_HasActiveOverride(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "active-provider.json")
	store := NewActiveProviderStore(storePath)

	// Initially no override.
	if store.HasActiveOverride() {
		t.Error("expected no active override initially")
	}

	// Set an override.
	store.Set("qwen", "auto", "test")
	if !store.HasActiveOverride() {
		t.Error("expected active override after Set()")
	}

	// Clear it.
	store.Clear()
	if store.HasActiveOverride() {
		t.Error("expected no active override after Clear()")
	}
}

func TestActiveProviderStore_ConcurrentAccess(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "active-provider.json")
	store := NewActiveProviderStore(storePath)

	type result struct{ err string }
	results := make(chan result, 10)

	for i := 0; i < 10; i++ {
		go func(n int) {
			p := "provider" + string(rune('0'+n))
			store.Set(p, "auto", "concurrent-test")
			state := store.Get()
			if state.ProviderName == "" {
				results <- result{"expected non-empty provider name"}
			} else {
				results <- result{}
			}
		}(i)
	}

	for i := 0; i < 10; i++ {
		if r := <-results; r.err != "" {
			t.Error(r.err)
		}
	}
}

func TestActiveProviderStore_Persistence(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "state", "active-provider.json")

	// Create store and save.
	store1 := NewActiveProviderStore(storePath)
	err := store1.Set("google", "gemini-2.5-pro", "persistence-test")
	if err != nil {
		t.Fatalf("failed to save: %v", err)
	}

	// Create a new store instance (simulates restart).
	store2 := NewActiveProviderStore(storePath)
	err = store2.Load()
	if err != nil {
		t.Fatalf("failed to load in new instance: %v", err)
	}

	state := store2.Get()
	if state.ProviderName != "google" {
		t.Errorf("expected provider 'google', got '%s'", state.ProviderName)
	}
	if state.Model != "gemini-2.5-pro" {
		t.Errorf("expected model 'gemini-2.5-pro', got '%s'", state.Model)
	}
}

func TestActiveProviderStore_NonExistentFile(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "nonexistent", "active-provider.json")

	store := NewActiveProviderStore(storePath)

	// Loading non-existent file should not error.
	err := store.Load()
	if err != nil {
		t.Fatalf("expected no error for non-existent file, got: %v", err)
	}

	// State should be empty.
	if store.HasActiveOverride() {
		t.Error("expected no active override for non-existent file")
	}
}

func TestActiveProviderStore_StateTime(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "active-provider.json")
	store := NewActiveProviderStore(storePath)

	before := time.Now()
	err := store.Set("qwen", "auto", "time-test")
	after := time.Now()

	if err != nil {
		t.Fatalf("failed to set: %v", err)
	}

	store.Load()
	state := store.Get()

	if state.SetAt.Before(before) || state.SetAt.After(after) {
		t.Errorf("SetAt time %v not within expected range [%v, %v]", 
			state.SetAt, before, after)
	}
}

func TestActiveProviderStore_GetReturnsCopy(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "active-provider.json")
	store := NewActiveProviderStore(storePath)

	store.Set("qwen", "auto", "copy-test")

	// Get state twice and modify one.
	state1 := store.Get()
	state2 := store.Get()

	// Modifying state1 should not affect state2.
	state1.ProviderName = "modified"

	if state2.ProviderName == "modified" {
		t.Error("Get() should return a copy, not the same reference")
	}
	if state2.ProviderName != "qwen" {
		t.Errorf("expected state2.ProviderName to be 'qwen', got '%s'", state2.ProviderName)
	}
}

// Benchmark tests.

func BenchmarkActiveProviderStore_Set(b *testing.B) {
	tmpDir := b.TempDir()
	storePath := filepath.Join(tmpDir, "active-provider.json")
	store := NewActiveProviderStore(storePath)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store.Set("qwen", "auto", "benchmark")
	}
}

func BenchmarkActiveProviderStore_Get(b *testing.B) {
	tmpDir := b.TempDir()
	storePath := filepath.Join(tmpDir, "active-provider.json")
	store := NewActiveProviderStore(storePath)
	store.Set("qwen", "auto", "benchmark")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store.Get()
	}
}

func BenchmarkActiveProviderStore_Load(b *testing.B) {
	tmpDir := b.TempDir()
	storePath := filepath.Join(tmpDir, "active-provider.json")
	store := NewActiveProviderStore(storePath)
	store.Set("qwen", "auto", "benchmark")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store.Load()
	}
}
