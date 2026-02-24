package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// --- Namespace Constants ---

const (
	UMNSFact       = "fact"
	UMNSPreference = "preference"
	UMNSEpisode    = "episode"
	UMNSEmotion    = "emotion"
	UMNSFile       = "file"
	UMNSReflection = "reflection"
)

// --- Status Constants ---

const (
	UMStatusActive     = "active"
	UMStatusTombstoned = "tombstoned"
	UMStatusMerged     = "merged"
)

// --- Types ---

// UnifiedMemoryEntry represents a single entry in the unified memory layer.
type UnifiedMemoryEntry struct {
	ID           string         `json:"id"`
	Namespace    string         `json:"namespace"`
	Scope        string         `json:"scope"`                  // role name or "" for global
	Key          string         `json:"key"`
	Value        string         `json:"value"`
	ContentHash  string         `json:"contentHash"`
	Version      int            `json:"version"`
	Status       string         `json:"status"`
	TombstonedAt string         `json:"tombstonedAt,omitempty"`
	TTLDays      int            `json:"ttlDays"`
	Source       string         `json:"source"`
	SourceRef    string         `json:"sourceRef,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	CreatedAt    string         `json:"createdAt"`
	UpdatedAt    string         `json:"updatedAt"`
}

// MemoryVersion represents a historical version of a memory entry.
type MemoryVersion struct {
	ID          int    `json:"id"`
	MemoryID    string `json:"memoryId"`
	Version     int    `json:"version"`
	Value       string `json:"value"`
	ContentHash string `json:"contentHash"`
	ChangedBy   string `json:"changedBy"`
	CreatedAt   string `json:"createdAt"`
}

// MemoryLink represents a cross-reference between two memory entries.
type MemoryLink struct {
	ID        int    `json:"id"`
	SourceID  string `json:"sourceId"`
	TargetID  string `json:"targetId"`
	LinkType  string `json:"linkType"` // "related","supersedes","derived_from","contradicts"
	CreatedAt string `json:"createdAt"`
}

// --- DB Init ---

// initUnifiedMemoryDB creates the unified memory tables.
func initUnifiedMemoryDB(dbPath string) error {
	schema := `
CREATE TABLE IF NOT EXISTS unified_memory (
    id TEXT PRIMARY KEY,
    namespace TEXT NOT NULL,
    scope TEXT NOT NULL DEFAULT '',
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    version INTEGER NOT NULL DEFAULT 1,
    status TEXT NOT NULL DEFAULT 'active',
    tombstoned_at TEXT,
    ttl_days INTEGER NOT NULL DEFAULT 0,
    source TEXT NOT NULL DEFAULT '',
    source_ref TEXT DEFAULT '',
    metadata TEXT DEFAULT '{}',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_um_namespace ON unified_memory(namespace);
CREATE INDEX IF NOT EXISTS idx_um_scope ON unified_memory(scope);
CREATE INDEX IF NOT EXISTS idx_um_status ON unified_memory(status);
CREATE INDEX IF NOT EXISTS idx_um_updated ON unified_memory(updated_at);
CREATE INDEX IF NOT EXISTS idx_um_hash ON unified_memory(content_hash);
CREATE UNIQUE INDEX IF NOT EXISTS idx_um_active_key
    ON unified_memory(namespace, scope, key) WHERE status = 'active';

CREATE TABLE IF NOT EXISTS memory_versions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    memory_id TEXT NOT NULL,
    version INTEGER NOT NULL,
    value TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    changed_by TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    FOREIGN KEY (memory_id) REFERENCES unified_memory(id)
);
CREATE INDEX IF NOT EXISTS idx_mv_memory ON memory_versions(memory_id);

CREATE TABLE IF NOT EXISTS memory_links (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_id TEXT NOT NULL,
    target_id TEXT NOT NULL,
    link_type TEXT NOT NULL,
    created_at TEXT NOT NULL,
    FOREIGN KEY (source_id) REFERENCES unified_memory(id),
    FOREIGN KEY (target_id) REFERENCES unified_memory(id)
);
CREATE INDEX IF NOT EXISTS idx_ml_source ON memory_links(source_id);
CREATE INDEX IF NOT EXISTS idx_ml_target ON memory_links(target_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_ml_pair ON memory_links(source_id, target_id, link_type);

CREATE TABLE IF NOT EXISTS memory_migration_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    migration TEXT NOT NULL,
    applied_at TEXT NOT NULL
);
`
	cmd := exec.Command("sqlite3", dbPath, schema)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("init unified memory db: %s: %w", string(out), err)
	}
	return nil
}

// --- Content Hash ---

// umContentHash returns the first 32 hex characters of the SHA-256 hash of value.
func umContentHash(value string) string {
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:16])
}

// --- UUID Generation ---

// umNewID generates a UUID v4 using crypto/rand.
func umNewID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// --- Helper: Parse Row ---

// umEntryFromRow converts a queryDB row map to a UnifiedMemoryEntry.
func umEntryFromRow(row map[string]any) UnifiedMemoryEntry {
	e := UnifiedMemoryEntry{
		ID:           jsonStr(row["id"]),
		Namespace:    jsonStr(row["namespace"]),
		Scope:        jsonStr(row["scope"]),
		Key:          jsonStr(row["key"]),
		Value:        jsonStr(row["value"]),
		ContentHash:  jsonStr(row["content_hash"]),
		Version:      jsonInt(row["version"]),
		Status:       jsonStr(row["status"]),
		TombstonedAt: jsonStr(row["tombstoned_at"]),
		TTLDays:      jsonInt(row["ttl_days"]),
		Source:       jsonStr(row["source"]),
		SourceRef:    jsonStr(row["source_ref"]),
		CreatedAt:    jsonStr(row["created_at"]),
		UpdatedAt:    jsonStr(row["updated_at"]),
	}
	metaStr := jsonStr(row["metadata"])
	if metaStr != "" && metaStr != "{}" {
		var meta map[string]any
		if err := json.Unmarshal([]byte(metaStr), &meta); err == nil {
			e.Metadata = meta
		}
	}
	return e
}

// umVersionFromRow converts a queryDB row map to a MemoryVersion.
func umVersionFromRow(row map[string]any) MemoryVersion {
	return MemoryVersion{
		ID:          jsonInt(row["id"]),
		MemoryID:    jsonStr(row["memory_id"]),
		Version:     jsonInt(row["version"]),
		Value:       jsonStr(row["value"]),
		ContentHash: jsonStr(row["content_hash"]),
		ChangedBy:   jsonStr(row["changed_by"]),
		CreatedAt:   jsonStr(row["created_at"]),
	}
}

// umLinkFromRow converts a queryDB row map to a MemoryLink.
func umLinkFromRow(row map[string]any) MemoryLink {
	return MemoryLink{
		ID:        jsonInt(row["id"]),
		SourceID:  jsonStr(row["source_id"]),
		TargetID:  jsonStr(row["target_id"]),
		LinkType:  jsonStr(row["link_type"]),
		CreatedAt: jsonStr(row["created_at"]),
	}
}

// --- Core CRUD ---

// umStore stores or updates a unified memory entry with dedup logic:
//  1. hash = umContentHash(value)
//  2. existing = SELECT WHERE namespace+scope+key AND status='active'
//  3. IF existing AND hash == existing.contentHash -> no-op, return existing.ID
//  4. IF existing AND hash != existing.contentHash -> backup to memory_versions, UPDATE in place, increment version
//  5. IF not exists -> INSERT new entry
//
// Returns (id, created bool, error).
func umStore(dbPath string, entry UnifiedMemoryEntry) (string, bool, error) {
	hash := umContentHash(entry.Value)
	now := time.Now().UTC().Format(time.RFC3339)

	// Check for existing active entry with same namespace+scope+key.
	existing, err := umGet(dbPath, entry.Namespace, entry.Scope, entry.Key)
	if err != nil {
		return "", false, fmt.Errorf("umStore check existing: %w", err)
	}

	if existing != nil {
		// Existing entry found.
		if existing.ContentHash == hash {
			// Content unchanged — no-op.
			return existing.ID, false, nil
		}

		// Content changed — backup old version, then update.
		backupSQL := fmt.Sprintf(
			`INSERT INTO memory_versions (memory_id, version, value, content_hash, changed_by, created_at)
			 VALUES ('%s', %d, '%s', '%s', '%s', '%s')`,
			escapeSQLite(existing.ID),
			existing.Version,
			escapeSQLite(existing.Value),
			escapeSQLite(existing.ContentHash),
			escapeSQLite(entry.Source),
			escapeSQLite(now),
		)
		cmd := exec.Command("sqlite3", dbPath, backupSQL)
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", false, fmt.Errorf("umStore backup version: %s: %w", string(out), err)
		}

		// Serialize metadata.
		metaJSON := "{}"
		if entry.Metadata != nil {
			if b, err := json.Marshal(entry.Metadata); err == nil {
				metaJSON = string(b)
			}
		}

		newVersion := existing.Version + 1
		updateSQL := fmt.Sprintf(
			`UPDATE unified_memory SET
			  value = '%s',
			  content_hash = '%s',
			  version = %d,
			  source = '%s',
			  source_ref = '%s',
			  metadata = '%s',
			  ttl_days = %d,
			  updated_at = '%s'
			 WHERE id = '%s'`,
			escapeSQLite(entry.Value),
			escapeSQLite(hash),
			newVersion,
			escapeSQLite(entry.Source),
			escapeSQLite(entry.SourceRef),
			escapeSQLite(metaJSON),
			entry.TTLDays,
			escapeSQLite(now),
			escapeSQLite(existing.ID),
		)
		cmd = exec.Command("sqlite3", dbPath, updateSQL)
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", false, fmt.Errorf("umStore update: %s: %w", string(out), err)
		}
		return existing.ID, false, nil
	}

	// No existing entry — insert new.
	id := entry.ID
	if id == "" {
		id = umNewID()
	}

	metaJSON := "{}"
	if entry.Metadata != nil {
		if b, err := json.Marshal(entry.Metadata); err == nil {
			metaJSON = string(b)
		}
	}

	status := entry.Status
	if status == "" {
		status = UMStatusActive
	}

	insertSQL := fmt.Sprintf(
		`INSERT INTO unified_memory (id, namespace, scope, key, value, content_hash, version, status, ttl_days, source, source_ref, metadata, created_at, updated_at)
		 VALUES ('%s','%s','%s','%s','%s','%s',1,'%s',%d,'%s','%s','%s','%s','%s')`,
		escapeSQLite(id),
		escapeSQLite(entry.Namespace),
		escapeSQLite(entry.Scope),
		escapeSQLite(entry.Key),
		escapeSQLite(entry.Value),
		escapeSQLite(hash),
		escapeSQLite(status),
		entry.TTLDays,
		escapeSQLite(entry.Source),
		escapeSQLite(entry.SourceRef),
		escapeSQLite(metaJSON),
		escapeSQLite(now),
		escapeSQLite(now),
	)
	cmd := exec.Command("sqlite3", dbPath, insertSQL)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", false, fmt.Errorf("umStore insert: %s: %w", string(out), err)
	}
	return id, true, nil
}

// umGet retrieves a single active entry by namespace+scope+key.
func umGet(dbPath, namespace, scope, key string) (*UnifiedMemoryEntry, error) {
	sql := fmt.Sprintf(
		`SELECT * FROM unified_memory WHERE namespace='%s' AND scope='%s' AND key='%s' AND status='%s' LIMIT 1`,
		escapeSQLite(namespace),
		escapeSQLite(scope),
		escapeSQLite(key),
		UMStatusActive,
	)
	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("umGet: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	e := umEntryFromRow(rows[0])
	return &e, nil
}

// umGetByID retrieves an entry by ID (any status).
func umGetByID(dbPath, id string) (*UnifiedMemoryEntry, error) {
	sql := fmt.Sprintf(
		`SELECT * FROM unified_memory WHERE id='%s' LIMIT 1`,
		escapeSQLite(id),
	)
	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("umGetByID: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	e := umEntryFromRow(rows[0])
	return &e, nil
}

// umDelete soft-deletes (tombstones) an entry by setting status to tombstoned.
func umDelete(dbPath, id string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	sql := fmt.Sprintf(
		`UPDATE unified_memory SET status='%s', tombstoned_at='%s', updated_at='%s' WHERE id='%s' AND status='%s'`,
		UMStatusTombstoned,
		escapeSQLite(now),
		escapeSQLite(now),
		escapeSQLite(id),
		UMStatusActive,
	)
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("umDelete: %s: %w", string(out), err)
	}
	return nil
}

// umSearch searches active entries matching a query string (LIKE %query%)
// with optional namespace and scope filters. Returns up to limit results
// ordered by updated_at DESC.
func umSearch(dbPath, query, namespace, scope string, limit int) ([]UnifiedMemoryEntry, error) {
	if limit <= 0 {
		limit = 50
	}

	conditions := []string{fmt.Sprintf("status='%s'", UMStatusActive)}

	if query != "" {
		escaped := escapeSQLite(query)
		conditions = append(conditions, fmt.Sprintf(
			"(key LIKE '%%%s%%' OR value LIKE '%%%s%%')",
			escaped, escaped,
		))
	}
	if namespace != "" {
		conditions = append(conditions, fmt.Sprintf("namespace='%s'", escapeSQLite(namespace)))
	}
	if scope != "" {
		conditions = append(conditions, fmt.Sprintf("scope='%s'", escapeSQLite(scope)))
	}

	sql := fmt.Sprintf(
		`SELECT * FROM unified_memory WHERE %s ORDER BY updated_at DESC LIMIT %d`,
		strings.Join(conditions, " AND "),
		limit,
	)
	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("umSearch: %w", err)
	}

	results := make([]UnifiedMemoryEntry, 0, len(rows))
	for _, row := range rows {
		results = append(results, umEntryFromRow(row))
	}
	return results, nil
}

// umList lists active entries with optional namespace and scope filters.
// Returns up to limit results ordered by updated_at DESC.
func umList(dbPath, namespace, scope string, limit int) ([]UnifiedMemoryEntry, error) {
	if limit <= 0 {
		limit = 50
	}

	conditions := []string{fmt.Sprintf("status='%s'", UMStatusActive)}

	if namespace != "" {
		conditions = append(conditions, fmt.Sprintf("namespace='%s'", escapeSQLite(namespace)))
	}
	if scope != "" {
		conditions = append(conditions, fmt.Sprintf("scope='%s'", escapeSQLite(scope)))
	}

	sql := fmt.Sprintf(
		`SELECT * FROM unified_memory WHERE %s ORDER BY updated_at DESC LIMIT %d`,
		strings.Join(conditions, " AND "),
		limit,
	)
	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("umList: %w", err)
	}

	results := make([]UnifiedMemoryEntry, 0, len(rows))
	for _, row := range rows {
		results = append(results, umEntryFromRow(row))
	}
	return results, nil
}

// --- Version History ---

// umHistory returns version history for a memory entry, ordered by version DESC.
func umHistory(dbPath, memoryID string, limit int) ([]MemoryVersion, error) {
	if limit <= 0 {
		limit = 20
	}

	sql := fmt.Sprintf(
		`SELECT * FROM memory_versions WHERE memory_id='%s' ORDER BY version DESC LIMIT %d`,
		escapeSQLite(memoryID),
		limit,
	)
	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("umHistory: %w", err)
	}

	results := make([]MemoryVersion, 0, len(rows))
	for _, row := range rows {
		results = append(results, umVersionFromRow(row))
	}
	return results, nil
}

// --- Links ---

// umLink creates a cross-reference between two memory entries.
// Duplicate links (same source, target, type) are silently ignored.
func umLink(dbPath, sourceID, targetID, linkType string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	sql := fmt.Sprintf(
		`INSERT OR IGNORE INTO memory_links (source_id, target_id, link_type, created_at)
		 VALUES ('%s','%s','%s','%s')`,
		escapeSQLite(sourceID),
		escapeSQLite(targetID),
		escapeSQLite(linkType),
		escapeSQLite(now),
	)
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("umLink: %s: %w", string(out), err)
	}
	return nil
}

// umGetLinks returns links for a memory entry (both directions: as source or target).
func umGetLinks(dbPath, memoryID string) ([]MemoryLink, error) {
	sql := fmt.Sprintf(
		`SELECT * FROM memory_links WHERE source_id='%s' OR target_id='%s' ORDER BY created_at DESC`,
		escapeSQLite(memoryID),
		escapeSQLite(memoryID),
	)
	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("umGetLinks: %w", err)
	}

	results := make([]MemoryLink, 0, len(rows))
	for _, row := range rows {
		results = append(results, umLinkFromRow(row))
	}
	return results, nil
}

// --- Semantic Search ---

// umSearchSemantic searches unified memory using hybrid semantic search when
// embedding is enabled, falling back to plain text search otherwise.
// It combines embedding-based vector search with TF-IDF and SQL LIKE search
// for comprehensive results.
func umSearchSemantic(ctx context.Context, cfg *Config, query, namespace, scope string, limit int) ([]UnifiedMemoryEntry, error) {
	if limit <= 0 {
		limit = 50
	}

	// If embedding not enabled, fall back to regular text search.
	if cfg == nil || !cfg.Embedding.Enabled {
		return umSearch(cfg.HistoryDB, query, namespace, scope, limit)
	}

	// Get hybrid search results (vector + TF-IDF).
	results, err := hybridSearch(ctx, cfg, query, "unified_memory", limit*2)
	if err != nil {
		logWarn("semantic search failed, falling back to text search", "error", err)
		return umSearch(cfg.HistoryDB, query, namespace, scope, limit)
	}

	if len(results) == 0 {
		// No embedding results, fall back to text search.
		return umSearch(cfg.HistoryDB, query, namespace, scope, limit)
	}

	// Convert EmbeddingSearchResult back to UnifiedMemoryEntry by looking up source_id.
	var entries []UnifiedMemoryEntry
	seen := make(map[string]bool)
	for _, r := range results {
		if r.Source != "unified_memory" {
			continue
		}
		if seen[r.SourceID] {
			continue
		}
		entry, eErr := umGetByID(cfg.HistoryDB, r.SourceID)
		if eErr != nil || entry == nil {
			continue
		}
		// Apply namespace/scope filters.
		if namespace != "" && entry.Namespace != namespace {
			continue
		}
		if scope != "" && entry.Scope != scope {
			continue
		}
		// Only return active entries.
		if entry.Status != UMStatusActive {
			continue
		}
		seen[entry.ID] = true
		entries = append(entries, *entry)
		if len(entries) >= limit {
			break
		}
	}

	// If semantic search returned too few results, supplement with text search.
	if len(entries) < limit {
		textResults, _ := umSearch(cfg.HistoryDB, query, namespace, scope, limit-len(entries))
		for _, e := range textResults {
			if !seen[e.ID] {
				entries = append(entries, e)
				seen[e.ID] = true
				if len(entries) >= limit {
					break
				}
			}
		}
	}

	return entries, nil
}

// umAutoEmbed asynchronously generates and stores an embedding for a unified memory entry.
// It is designed to be called after umStore to keep the embedding index up to date.
// The function runs in a goroutine and does not block the caller.
func umAutoEmbed(cfg *Config, entryID, key, value string) {
	if cfg == nil || !cfg.Embedding.Enabled || cfg.HistoryDB == "" {
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		text := key + ": " + value
		vec, err := getEmbedding(ctx, cfg, text)
		if err != nil {
			logDebug("umAutoEmbed: embedding failed", "entryID", entryID, "error", err)
			return
		}

		if sErr := storeEmbedding(cfg.HistoryDB, "unified_memory", entryID, text, vec, nil); sErr != nil {
			logDebug("umAutoEmbed: store failed", "entryID", entryID, "error", sErr)
		}
	}()
}
