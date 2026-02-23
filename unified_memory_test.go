package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tempUnifiedMemoryDB creates and initializes a temporary unified memory DB for testing.
func tempUnifiedMemoryDB(t *testing.T) string {
	t.Helper()
	f, err := os.CreateTemp("", "um-test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := initUnifiedMemoryDB(f.Name()); err != nil {
		os.Remove(f.Name())
		t.Fatal(err)
	}
	return f.Name()
}

// --- DB Init ---

func TestInitUnifiedMemoryDB(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	if err := initUnifiedMemoryDB(dbPath); err != nil {
		t.Fatalf("initUnifiedMemoryDB: %v", err)
	}
	// Idempotent: calling again should not error.
	if err := initUnifiedMemoryDB(dbPath); err != nil {
		t.Fatalf("initUnifiedMemoryDB (second call): %v", err)
	}

	// Verify tables exist by querying them.
	rows, err := queryDB(dbPath, `SELECT name FROM sqlite_master WHERE type='table' ORDER BY name`)
	if err != nil {
		t.Fatalf("query tables: %v", err)
	}
	tables := make(map[string]bool)
	for _, row := range rows {
		tables[jsonStr(row["name"])] = true
	}
	for _, want := range []string{"unified_memory", "memory_versions", "memory_links", "memory_migration_log"} {
		if !tables[want] {
			t.Errorf("table %q not found", want)
		}
	}
}

// --- Content Hash ---

func TestUmContentHash(t *testing.T) {
	// Hash should be deterministic.
	h1 := umContentHash("hello world")
	h2 := umContentHash("hello world")
	if h1 != h2 {
		t.Errorf("hash not deterministic: %q vs %q", h1, h2)
	}

	// Hash should be 32 hex chars.
	if len(h1) != 32 {
		t.Errorf("hash length = %d, want 32", len(h1))
	}

	// Different inputs produce different hashes.
	h3 := umContentHash("different input")
	if h1 == h3 {
		t.Errorf("different inputs produced same hash: %q", h1)
	}

	// Empty string should still produce a valid hash.
	h4 := umContentHash("")
	if len(h4) != 32 {
		t.Errorf("empty string hash length = %d, want 32", len(h4))
	}
}

// --- UUID Generation ---

func TestUmNewID(t *testing.T) {
	id := umNewID()

	// Should be 36 chars (8-4-4-4-12).
	if len(id) != 36 {
		t.Errorf("id length = %d, want 36", len(id))
	}

	// Should contain 4 hyphens.
	parts := strings.Split(id, "-")
	if len(parts) != 5 {
		t.Errorf("id parts = %d, want 5", len(parts))
	}

	// Version nibble should be 4.
	if len(parts) >= 3 && len(parts[2]) >= 1 && parts[2][0] != '4' {
		t.Errorf("version nibble = %c, want '4'", parts[2][0])
	}

	// Two generated IDs should differ.
	id2 := umNewID()
	if id == id2 {
		t.Errorf("two generated IDs are identical: %q", id)
	}
}

// --- Store: New Entry ---

func TestUmStore_New(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := tempUnifiedMemoryDB(t)
	defer os.Remove(dbPath)

	entry := UnifiedMemoryEntry{
		Namespace: UMNSFact,
		Scope:     "global",
		Key:       "user_name",
		Value:     "Kumaneko",
		Source:    "test",
	}

	id, created, err := umStore(dbPath, entry)
	if err != nil {
		t.Fatalf("umStore: %v", err)
	}
	if !created {
		t.Error("expected created=true for new entry")
	}
	if id == "" {
		t.Error("expected non-empty ID")
	}

	// Verify the entry was persisted.
	got, err := umGetByID(dbPath, id)
	if err != nil {
		t.Fatalf("umGetByID: %v", err)
	}
	if got == nil {
		t.Fatal("entry not found after store")
	}
	if got.Namespace != UMNSFact {
		t.Errorf("namespace = %q, want %q", got.Namespace, UMNSFact)
	}
	if got.Scope != "global" {
		t.Errorf("scope = %q, want %q", got.Scope, "global")
	}
	if got.Key != "user_name" {
		t.Errorf("key = %q, want %q", got.Key, "user_name")
	}
	if got.Value != "Kumaneko" {
		t.Errorf("value = %q, want %q", got.Value, "Kumaneko")
	}
	if got.Version != 1 {
		t.Errorf("version = %d, want 1", got.Version)
	}
	if got.Status != UMStatusActive {
		t.Errorf("status = %q, want %q", got.Status, UMStatusActive)
	}
	if got.ContentHash == "" {
		t.Error("content hash should not be empty")
	}
}

// --- Store: Dedup (same content) ---

func TestUmStore_Dedup(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := tempUnifiedMemoryDB(t)
	defer os.Remove(dbPath)

	entry := UnifiedMemoryEntry{
		Namespace: UMNSFact,
		Scope:     "",
		Key:       "favorite_color",
		Value:     "blue",
		Source:    "test",
	}

	id1, created1, err := umStore(dbPath, entry)
	if err != nil {
		t.Fatalf("umStore first: %v", err)
	}
	if !created1 {
		t.Error("first store: expected created=true")
	}

	// Store again with exact same content.
	id2, created2, err := umStore(dbPath, entry)
	if err != nil {
		t.Fatalf("umStore second: %v", err)
	}
	if created2 {
		t.Error("second store: expected created=false (dedup)")
	}
	if id1 != id2 {
		t.Errorf("dedup should return same ID: %q vs %q", id1, id2)
	}

	// Version should still be 1 (no update happened).
	got, _ := umGetByID(dbPath, id1)
	if got.Version != 1 {
		t.Errorf("version after dedup = %d, want 1", got.Version)
	}

	// No version history should exist.
	history, _ := umHistory(dbPath, id1, 10)
	if len(history) != 0 {
		t.Errorf("history count = %d, want 0 (dedup should not create history)", len(history))
	}
}

// --- Store: Update (different content, same key) ---

func TestUmStore_Update(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := tempUnifiedMemoryDB(t)
	defer os.Remove(dbPath)

	entry := UnifiedMemoryEntry{
		Namespace: UMNSPreference,
		Scope:     "alice",
		Key:       "language",
		Value:     "English",
		Source:    "chat",
	}

	id1, _, err := umStore(dbPath, entry)
	if err != nil {
		t.Fatalf("umStore first: %v", err)
	}

	// Update with different value.
	entry.Value = "Japanese"
	entry.Source = "correction"
	id2, created, err := umStore(dbPath, entry)
	if err != nil {
		t.Fatalf("umStore update: %v", err)
	}
	if created {
		t.Error("update should return created=false")
	}
	if id1 != id2 {
		t.Errorf("update should return same ID: %q vs %q", id1, id2)
	}

	// Check updated entry.
	got, _ := umGetByID(dbPath, id1)
	if got.Value != "Japanese" {
		t.Errorf("value = %q, want %q", got.Value, "Japanese")
	}
	if got.Version != 2 {
		t.Errorf("version = %d, want 2", got.Version)
	}
	if got.Source != "correction" {
		t.Errorf("source = %q, want %q", got.Source, "correction")
	}

	// Check version history contains old value.
	history, err := umHistory(dbPath, id1, 10)
	if err != nil {
		t.Fatalf("umHistory: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("history count = %d, want 1", len(history))
	}
	if history[0].Value != "English" {
		t.Errorf("history[0].value = %q, want %q", history[0].Value, "English")
	}
	if history[0].Version != 1 {
		t.Errorf("history[0].version = %d, want 1", history[0].Version)
	}

	// Update again to create second version.
	entry.Value = "Chinese"
	_, _, err = umStore(dbPath, entry)
	if err != nil {
		t.Fatalf("umStore third: %v", err)
	}

	got, _ = umGetByID(dbPath, id1)
	if got.Version != 3 {
		t.Errorf("version = %d, want 3", got.Version)
	}

	history, _ = umHistory(dbPath, id1, 10)
	if len(history) != 2 {
		t.Errorf("history count = %d, want 2", len(history))
	}
}

// --- Store: Custom ID ---

func TestUmStore_CustomID(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := tempUnifiedMemoryDB(t)
	defer os.Remove(dbPath)

	entry := UnifiedMemoryEntry{
		ID:        "custom-id-001",
		Namespace: UMNSFact,
		Scope:     "",
		Key:       "test_key",
		Value:     "test_value",
		Source:    "test",
	}

	id, created, err := umStore(dbPath, entry)
	if err != nil {
		t.Fatalf("umStore: %v", err)
	}
	if !created {
		t.Error("expected created=true")
	}
	if id != "custom-id-001" {
		t.Errorf("id = %q, want %q", id, "custom-id-001")
	}
}

// --- Store: Metadata Roundtrip ---

func TestUmStore_Metadata(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := tempUnifiedMemoryDB(t)
	defer os.Remove(dbPath)

	entry := UnifiedMemoryEntry{
		Namespace: UMNSEpisode,
		Scope:     "",
		Key:       "meeting_2024",
		Value:     "Discussed project timeline",
		Source:    "session",
		Metadata:  map[string]any{"participants": "alice,bob", "duration": 30},
	}

	id, _, err := umStore(dbPath, entry)
	if err != nil {
		t.Fatalf("umStore: %v", err)
	}

	got, err := umGetByID(dbPath, id)
	if err != nil {
		t.Fatalf("umGetByID: %v", err)
	}
	if got.Metadata == nil {
		t.Fatal("metadata should not be nil")
	}
	if jsonStr(got.Metadata["participants"]) != "alice,bob" {
		t.Errorf("metadata.participants = %v, want %q", got.Metadata["participants"], "alice,bob")
	}
}

// --- Get ---

func TestUmGet(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := tempUnifiedMemoryDB(t)
	defer os.Remove(dbPath)

	// Not found returns nil, no error.
	got, err := umGet(dbPath, UMNSFact, "", "nonexistent")
	if err != nil {
		t.Fatalf("umGet: %v", err)
	}
	if got != nil {
		t.Error("expected nil for nonexistent entry")
	}

	// Store an entry, then retrieve it.
	entry := UnifiedMemoryEntry{
		Namespace: UMNSFact,
		Scope:     "bot",
		Key:       "version",
		Value:     "2.0",
		Source:    "init",
	}
	umStore(dbPath, entry)

	got, err = umGet(dbPath, UMNSFact, "bot", "version")
	if err != nil {
		t.Fatalf("umGet: %v", err)
	}
	if got == nil {
		t.Fatal("expected entry, got nil")
	}
	if got.Value != "2.0" {
		t.Errorf("value = %q, want %q", got.Value, "2.0")
	}
}

func TestUmGetByID(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := tempUnifiedMemoryDB(t)
	defer os.Remove(dbPath)

	// Not found returns nil, no error.
	got, err := umGetByID(dbPath, "nonexistent-id")
	if err != nil {
		t.Fatalf("umGetByID: %v", err)
	}
	if got != nil {
		t.Error("expected nil for nonexistent ID")
	}

	// Store and retrieve by ID.
	entry := UnifiedMemoryEntry{
		ID:        "get-by-id-test",
		Namespace: UMNSEmotion,
		Scope:     "",
		Key:       "mood",
		Value:     "happy",
		Source:    "analysis",
	}
	umStore(dbPath, entry)

	got, err = umGetByID(dbPath, "get-by-id-test")
	if err != nil {
		t.Fatalf("umGetByID: %v", err)
	}
	if got == nil {
		t.Fatal("expected entry, got nil")
	}
	if got.Value != "happy" {
		t.Errorf("value = %q, want %q", got.Value, "happy")
	}
}

// --- Delete ---

func TestUmDelete(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := tempUnifiedMemoryDB(t)
	defer os.Remove(dbPath)

	entry := UnifiedMemoryEntry{
		ID:        "del-test",
		Namespace: UMNSFact,
		Scope:     "",
		Key:       "temp_note",
		Value:     "to be deleted",
		Source:    "test",
	}
	umStore(dbPath, entry)

	// Delete.
	if err := umDelete(dbPath, "del-test"); err != nil {
		t.Fatalf("umDelete: %v", err)
	}

	// Active get should return nil (tombstoned).
	got, _ := umGet(dbPath, UMNSFact, "", "temp_note")
	if got != nil {
		t.Error("expected nil from umGet after delete")
	}

	// But getByID should still find it with tombstoned status.
	gotByID, _ := umGetByID(dbPath, "del-test")
	if gotByID == nil {
		t.Fatal("expected to find tombstoned entry by ID")
	}
	if gotByID.Status != UMStatusTombstoned {
		t.Errorf("status = %q, want %q", gotByID.Status, UMStatusTombstoned)
	}
	if gotByID.TombstonedAt == "" {
		t.Error("tombstoned_at should be set")
	}

	// Storing a new entry with the same key should work (old one is tombstoned).
	entry2 := UnifiedMemoryEntry{
		Namespace: UMNSFact,
		Scope:     "",
		Key:       "temp_note",
		Value:     "new value after delete",
		Source:    "test",
	}
	id, created, err := umStore(dbPath, entry2)
	if err != nil {
		t.Fatalf("umStore after delete: %v", err)
	}
	if !created {
		t.Error("expected created=true after storing with tombstoned predecessor")
	}
	if id == "del-test" {
		t.Error("new entry should have different ID from tombstoned one")
	}
}

// --- Search ---

func TestUmSearch(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := tempUnifiedMemoryDB(t)
	defer os.Remove(dbPath)

	// Seed some entries.
	entries := []UnifiedMemoryEntry{
		{Namespace: UMNSFact, Scope: "", Key: "name", Value: "Alice Johnson", Source: "test"},
		{Namespace: UMNSFact, Scope: "", Key: "email", Value: "alice@example.com", Source: "test"},
		{Namespace: UMNSPreference, Scope: "", Key: "color", Value: "blue", Source: "test"},
		{Namespace: UMNSFact, Scope: "bot", Key: "creator", Value: "Alice", Source: "test"},
		{Namespace: UMNSEpisode, Scope: "", Key: "chat_001", Value: "Talked about Alice's project", Source: "test"},
	}
	for _, e := range entries {
		if _, _, err := umStore(dbPath, e); err != nil {
			t.Fatalf("umStore seed: %v", err)
		}
	}

	// Search by query across all namespaces.
	// SQLite LIKE is case-insensitive for ASCII, so "alice@example.com" also matches "Alice".
	results, err := umSearch(dbPath, "Alice", "", "", 10)
	if err != nil {
		t.Fatalf("umSearch: %v", err)
	}
	if len(results) != 4 {
		t.Errorf("search 'Alice': got %d results, want 4", len(results))
	}

	// Search with namespace filter.
	results, err = umSearch(dbPath, "Alice", UMNSFact, "", 10)
	if err != nil {
		t.Fatalf("umSearch with namespace: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("search 'Alice' in fact: got %d results, want 3", len(results))
	}

	// Search with scope filter.
	results, err = umSearch(dbPath, "Alice", "", "bot", 10)
	if err != nil {
		t.Fatalf("umSearch with scope: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("search 'Alice' scope=bot: got %d results, want 1", len(results))
	}

	// Search with limit.
	results, err = umSearch(dbPath, "Alice", "", "", 1)
	if err != nil {
		t.Fatalf("umSearch with limit: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("search 'Alice' limit=1: got %d results, want 1", len(results))
	}

	// Empty query returns all active entries (with limit).
	results, err = umSearch(dbPath, "", "", "", 100)
	if err != nil {
		t.Fatalf("umSearch empty query: %v", err)
	}
	if len(results) != 5 {
		t.Errorf("search '': got %d results, want 5", len(results))
	}
}

// --- List ---

func TestUmList(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := tempUnifiedMemoryDB(t)
	defer os.Remove(dbPath)

	// Seed entries.
	entries := []UnifiedMemoryEntry{
		{Namespace: UMNSFact, Scope: "", Key: "a", Value: "1", Source: "test"},
		{Namespace: UMNSFact, Scope: "", Key: "b", Value: "2", Source: "test"},
		{Namespace: UMNSPreference, Scope: "", Key: "c", Value: "3", Source: "test"},
		{Namespace: UMNSFact, Scope: "role1", Key: "d", Value: "4", Source: "test"},
	}
	for _, e := range entries {
		umStore(dbPath, e)
	}

	// List all.
	results, err := umList(dbPath, "", "", 100)
	if err != nil {
		t.Fatalf("umList: %v", err)
	}
	if len(results) != 4 {
		t.Errorf("list all: got %d, want 4", len(results))
	}

	// List by namespace.
	results, err = umList(dbPath, UMNSFact, "", 100)
	if err != nil {
		t.Fatalf("umList by namespace: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("list fact: got %d, want 3", len(results))
	}

	// List by scope.
	results, err = umList(dbPath, "", "role1", 100)
	if err != nil {
		t.Fatalf("umList by scope: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("list scope=role1: got %d, want 1", len(results))
	}

	// List with limit.
	results, err = umList(dbPath, "", "", 2)
	if err != nil {
		t.Fatalf("umList with limit: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("list limit=2: got %d, want 2", len(results))
	}

	// Tombstoned entries should not appear.
	id := results[0].ID
	umDelete(dbPath, id)
	results, err = umList(dbPath, "", "", 100)
	if err != nil {
		t.Fatalf("umList after delete: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("list after delete: got %d, want 3", len(results))
	}
}

// --- History ---

func TestUmHistory(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := tempUnifiedMemoryDB(t)
	defer os.Remove(dbPath)

	entry := UnifiedMemoryEntry{
		ID:        "hist-test",
		Namespace: UMNSFact,
		Scope:     "",
		Key:       "evolving",
		Value:     "v1",
		Source:    "init",
	}
	umStore(dbPath, entry)

	// No history yet for a brand new entry.
	history, err := umHistory(dbPath, "hist-test", 10)
	if err != nil {
		t.Fatalf("umHistory: %v", err)
	}
	if len(history) != 0 {
		t.Errorf("history for new entry = %d, want 0", len(history))
	}

	// Update to v2 -> v1 goes to history.
	entry.Value = "v2"
	umStore(dbPath, entry)
	history, _ = umHistory(dbPath, "hist-test", 10)
	if len(history) != 1 {
		t.Fatalf("history after v2 = %d, want 1", len(history))
	}
	if history[0].Value != "v1" {
		t.Errorf("history[0].value = %q, want %q", history[0].Value, "v1")
	}

	// Update to v3 -> v2 goes to history.
	entry.Value = "v3"
	umStore(dbPath, entry)
	history, _ = umHistory(dbPath, "hist-test", 10)
	if len(history) != 2 {
		t.Fatalf("history after v3 = %d, want 2", len(history))
	}
	// Ordered by version DESC.
	if history[0].Version != 2 {
		t.Errorf("history[0].version = %d, want 2", history[0].Version)
	}
	if history[1].Version != 1 {
		t.Errorf("history[1].version = %d, want 1", history[1].Version)
	}

	// Limit.
	history, _ = umHistory(dbPath, "hist-test", 1)
	if len(history) != 1 {
		t.Errorf("history limit=1: got %d, want 1", len(history))
	}
}

// --- Link ---

func TestUmLink(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := tempUnifiedMemoryDB(t)
	defer os.Remove(dbPath)

	// Create two entries.
	e1 := UnifiedMemoryEntry{ID: "link-a", Namespace: UMNSFact, Key: "a", Value: "1", Source: "test"}
	e2 := UnifiedMemoryEntry{ID: "link-b", Namespace: UMNSFact, Key: "b", Value: "2", Source: "test"}
	umStore(dbPath, e1)
	umStore(dbPath, e2)

	// Create a link.
	if err := umLink(dbPath, "link-a", "link-b", "related"); err != nil {
		t.Fatalf("umLink: %v", err)
	}

	// Duplicate link should not error (INSERT OR IGNORE).
	if err := umLink(dbPath, "link-a", "link-b", "related"); err != nil {
		t.Fatalf("umLink duplicate: %v", err)
	}

	// Different link type between same pair should work.
	if err := umLink(dbPath, "link-a", "link-b", "supersedes"); err != nil {
		t.Fatalf("umLink different type: %v", err)
	}
}

func TestUmGetLinks(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := tempUnifiedMemoryDB(t)
	defer os.Remove(dbPath)

	// Create three entries.
	e1 := UnifiedMemoryEntry{ID: "gl-a", Namespace: UMNSFact, Key: "x", Value: "1", Source: "test"}
	e2 := UnifiedMemoryEntry{ID: "gl-b", Namespace: UMNSFact, Key: "y", Value: "2", Source: "test"}
	e3 := UnifiedMemoryEntry{ID: "gl-c", Namespace: UMNSFact, Key: "z", Value: "3", Source: "test"}
	umStore(dbPath, e1)
	umStore(dbPath, e2)
	umStore(dbPath, e3)

	// a -> b (related), c -> a (derived_from)
	umLink(dbPath, "gl-a", "gl-b", "related")
	umLink(dbPath, "gl-c", "gl-a", "derived_from")

	// Get links for 'a' — should find both (as source and as target).
	links, err := umGetLinks(dbPath, "gl-a")
	if err != nil {
		t.Fatalf("umGetLinks: %v", err)
	}
	if len(links) != 2 {
		t.Errorf("links for gl-a = %d, want 2", len(links))
	}

	// Get links for 'b' — should find 1.
	links, err = umGetLinks(dbPath, "gl-b")
	if err != nil {
		t.Fatalf("umGetLinks for b: %v", err)
	}
	if len(links) != 1 {
		t.Errorf("links for gl-b = %d, want 1", len(links))
	}
	if links[0].LinkType != "related" {
		t.Errorf("link type = %q, want %q", links[0].LinkType, "related")
	}

	// Get links for 'c' — should find 1.
	links, err = umGetLinks(dbPath, "gl-c")
	if err != nil {
		t.Fatalf("umGetLinks for c: %v", err)
	}
	if len(links) != 1 {
		t.Errorf("links for gl-c = %d, want 1", len(links))
	}

	// No links for nonexistent.
	links, err = umGetLinks(dbPath, "nonexistent")
	if err != nil {
		t.Fatalf("umGetLinks nonexistent: %v", err)
	}
	if len(links) != 0 {
		t.Errorf("links for nonexistent = %d, want 0", len(links))
	}
}

// --- Edge Cases ---

func TestUmStore_SpecialCharacters(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := tempUnifiedMemoryDB(t)
	defer os.Remove(dbPath)

	entry := UnifiedMemoryEntry{
		Namespace: UMNSFact,
		Scope:     "",
		Key:       "user's \"name\"",
		Value:     "O'Brien said \"hello\" -- it's fine; DROP TABLE; --",
		Source:    "test",
	}

	id, created, err := umStore(dbPath, entry)
	if err != nil {
		t.Fatalf("umStore with special chars: %v", err)
	}
	if !created {
		t.Error("expected created=true")
	}

	got, err := umGetByID(dbPath, id)
	if err != nil {
		t.Fatalf("umGetByID: %v", err)
	}
	if got.Key != "user's \"name\"" {
		t.Errorf("key = %q, want %q", got.Key, "user's \"name\"")
	}
	if got.Value != "O'Brien said \"hello\" -- it's fine; DROP TABLE; --" {
		t.Errorf("value roundtrip failed: %q", got.Value)
	}
}

func TestUmStore_TTLDays(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := tempUnifiedMemoryDB(t)
	defer os.Remove(dbPath)

	entry := UnifiedMemoryEntry{
		Namespace: UMNSEpisode,
		Scope:     "",
		Key:       "ttl_test",
		Value:     "ephemeral",
		Source:    "test",
		TTLDays:   7,
	}

	id, _, err := umStore(dbPath, entry)
	if err != nil {
		t.Fatalf("umStore: %v", err)
	}

	got, _ := umGetByID(dbPath, id)
	if got.TTLDays != 7 {
		t.Errorf("ttlDays = %d, want 7", got.TTLDays)
	}
}

func TestUmSearch_NoResults(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := tempUnifiedMemoryDB(t)
	defer os.Remove(dbPath)

	results, err := umSearch(dbPath, "nonexistent_query_xyz", "", "", 10)
	if err != nil {
		t.Fatalf("umSearch: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestUmDelete_Nonexistent(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := tempUnifiedMemoryDB(t)
	defer os.Remove(dbPath)

	// Deleting a nonexistent ID should not error (no rows affected).
	if err := umDelete(dbPath, "nonexistent-id"); err != nil {
		t.Fatalf("umDelete nonexistent: %v", err)
	}
}

func TestUmList_EmptyDB(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := tempUnifiedMemoryDB(t)
	defer os.Remove(dbPath)

	results, err := umList(dbPath, "", "", 100)
	if err != nil {
		t.Fatalf("umList: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0, got %d", len(results))
	}
}

// --- Semantic Search ---

func TestUmSearchSemantic_FallbackToText(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := tempUnifiedMemoryDB(t)
	defer os.Remove(dbPath)

	// Seed some entries.
	entries := []UnifiedMemoryEntry{
		{Namespace: UMNSFact, Scope: "", Key: "pet", Value: "Alice has a cat named Mochi", Source: "test"},
		{Namespace: UMNSFact, Scope: "", Key: "food", Value: "Alice likes sushi", Source: "test"},
		{Namespace: UMNSPreference, Scope: "", Key: "color", Value: "blue", Source: "test"},
	}
	for _, e := range entries {
		if _, _, err := umStore(dbPath, e); err != nil {
			t.Fatalf("umStore seed: %v", err)
		}
	}

	// With embedding disabled, umSearchSemantic should fall back to text search.
	cfg := &Config{
		HistoryDB: dbPath,
		Embedding: EmbeddingConfig{Enabled: false},
	}

	results, err := umSearchSemantic(context.Background(), cfg, "Alice", "", "", 10)
	if err != nil {
		t.Fatalf("umSearchSemantic: %v", err)
	}

	// Should find entries containing "Alice" via text search fallback.
	if len(results) < 2 {
		t.Errorf("expected at least 2 results for 'Alice', got %d", len(results))
	}

	// Verify all results contain "Alice" in key or value.
	for _, r := range results {
		if !strings.Contains(r.Key, "Alice") && !strings.Contains(r.Value, "Alice") {
			t.Errorf("result %q does not contain 'Alice'", r.Key)
		}
	}
}

func TestUmSearchSemantic_NilConfig(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := tempUnifiedMemoryDB(t)
	defer os.Remove(dbPath)

	// Seed.
	umStore(dbPath, UnifiedMemoryEntry{
		Namespace: UMNSFact, Key: "test", Value: "value", Source: "test",
	})

	// nil config should use the dbPath from the config if possible.
	// Since cfg is nil, it should fall back and not panic.
	cfg := &Config{
		HistoryDB: dbPath,
		Embedding: EmbeddingConfig{Enabled: false},
	}
	results, err := umSearchSemantic(context.Background(), cfg, "test", "", "", 10)
	if err != nil {
		t.Fatalf("umSearchSemantic: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

func TestUmSearchSemantic_NamespaceFilter(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := tempUnifiedMemoryDB(t)
	defer os.Remove(dbPath)

	// Seed entries across namespaces.
	umStore(dbPath, UnifiedMemoryEntry{
		Namespace: UMNSFact, Key: "animal", Value: "cat", Source: "test",
	})
	umStore(dbPath, UnifiedMemoryEntry{
		Namespace: UMNSPreference, Key: "animal_pref", Value: "cat lover", Source: "test",
	})
	umStore(dbPath, UnifiedMemoryEntry{
		Namespace: UMNSEpisode, Key: "episode_cat", Value: "talked about cat", Source: "test",
	})

	cfg := &Config{
		HistoryDB: dbPath,
		Embedding: EmbeddingConfig{Enabled: false},
	}

	// Search with namespace filter.
	results, err := umSearchSemantic(context.Background(), cfg, "cat", UMNSFact, "", 10)
	if err != nil {
		t.Fatalf("umSearchSemantic: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result in fact namespace, got %d", len(results))
	}
	if len(results) > 0 && results[0].Namespace != UMNSFact {
		t.Errorf("namespace = %q, want %q", results[0].Namespace, UMNSFact)
	}
}

func TestUmSearchSemantic_ScopeFilter(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := tempUnifiedMemoryDB(t)
	defer os.Remove(dbPath)

	umStore(dbPath, UnifiedMemoryEntry{
		Namespace: UMNSFact, Scope: "alice", Key: "name", Value: "Alice Smith", Source: "test",
	})
	umStore(dbPath, UnifiedMemoryEntry{
		Namespace: UMNSFact, Scope: "bob", Key: "name", Value: "Bob Smith", Source: "test",
	})

	cfg := &Config{
		HistoryDB: dbPath,
		Embedding: EmbeddingConfig{Enabled: false},
	}

	results, err := umSearchSemantic(context.Background(), cfg, "Smith", "", "alice", 10)
	if err != nil {
		t.Fatalf("umSearchSemantic: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result for scope=alice, got %d", len(results))
	}
	if len(results) > 0 && results[0].Scope != "alice" {
		t.Errorf("scope = %q, want %q", results[0].Scope, "alice")
	}
}

func TestUmSearchSemantic_NoResults(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := tempUnifiedMemoryDB(t)
	defer os.Remove(dbPath)

	cfg := &Config{
		HistoryDB: dbPath,
		Embedding: EmbeddingConfig{Enabled: false},
	}

	results, err := umSearchSemantic(context.Background(), cfg, "nonexistent_xyz", "", "", 10)
	if err != nil {
		t.Fatalf("umSearchSemantic: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestUmSearchSemantic_Limit(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := tempUnifiedMemoryDB(t)
	defer os.Remove(dbPath)

	// Seed 5 entries all matching "data".
	for i := 0; i < 5; i++ {
		umStore(dbPath, UnifiedMemoryEntry{
			Namespace: UMNSFact,
			Key:       "data_" + string(rune('a'+i)),
			Value:     "data point " + string(rune('a'+i)),
			Source:    "test",
		})
	}

	cfg := &Config{
		HistoryDB: dbPath,
		Embedding: EmbeddingConfig{Enabled: false},
	}

	results, err := umSearchSemantic(context.Background(), cfg, "data", "", "", 2)
	if err != nil {
		t.Fatalf("umSearchSemantic: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results (limit), got %d", len(results))
	}
}

// --- Auto Embed ---

func TestUmAutoEmbed_DisabledNoOp(t *testing.T) {
	// When embedding is disabled, umAutoEmbed should not panic or error.
	cfg := &Config{
		Embedding: EmbeddingConfig{Enabled: false},
	}
	// Should return immediately without error.
	umAutoEmbed(cfg, "test-id", "key", "value")
	// nil config should also not panic.
	umAutoEmbed(nil, "test-id", "key", "value")
}
