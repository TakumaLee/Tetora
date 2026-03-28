// Package version provides version tracking for configuration entities (config,
// workflows, prompts) backed by a SQLite database via the system sqlite3 CLI.
package version

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"tetora/internal/db"
)

// Version represents a saved version of a configuration entity.
type Version struct {
	ID          int    `json:"id"`
	VersionID   string `json:"versionId"`   // short random ID (e.g. "v-abc12345")
	EntityType  string `json:"entityType"`  // "config", "workflow", "prompt", "routing"
	EntityName  string `json:"entityName"`  // e.g. "config.json", workflow name, prompt name
	ContentJSON string `json:"contentJson"` // full JSON snapshot
	DiffSummary string `json:"diffSummary"` // human-readable diff from previous
	ChangedBy   string `json:"changedBy"`   // "cli", "api", "telegram", "system"
	Reason      string `json:"reason"`      // optional reason for the change
	CreatedAt   string `json:"createdAt"`
}

// MaxPerEntity is the maximum number of versions retained per entity.
const MaxPerEntity = 50

// --- DB Init ---

// InitDB creates the config_versions table and indexes if they do not exist.
func InitDB(dbPath string) error {
	sql := `
CREATE TABLE IF NOT EXISTS config_versions (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  version_id TEXT NOT NULL,
  entity_type TEXT NOT NULL,
  entity_name TEXT NOT NULL,
  content_json TEXT NOT NULL,
  diff_summary TEXT DEFAULT '',
  changed_by TEXT DEFAULT 'system',
  reason TEXT DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_cv_entity ON config_versions(entity_type, entity_name);
CREATE INDEX IF NOT EXISTS idx_cv_created ON config_versions(created_at);
`
	if err := db.Exec(dbPath, sql); err != nil {
		return fmt.Errorf("init config_versions: %w", err)
	}
	return nil
}

// --- Version ID Generation ---

// NewID returns a new random version ID of the form "v-xxxxxxxx".
func NewID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return fmt.Sprintf("v-%x", b)
}

// --- Snapshot Functions ---

// SnapshotConfig takes a snapshot of the current config.json content.
// It computes a diff against the previous version and stores both.
func SnapshotConfig(dbPath, configPath, changedBy, reason string) error {
	if dbPath == "" {
		return nil
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config for snapshot: %w", err)
	}
	return SnapshotEntity(dbPath, "config", "config.json", string(data), changedBy, reason)
}

// SnapshotWorkflow takes a snapshot of a workflow definition.
func SnapshotWorkflow(dbPath, workflowName, content, changedBy, reason string) error {
	if dbPath == "" {
		return nil
	}
	return SnapshotEntity(dbPath, "workflow", workflowName, content, changedBy, reason)
}

// SnapshotPrompt takes a snapshot of a prompt template.
func SnapshotPrompt(dbPath, promptName, content, changedBy, reason string) error {
	if dbPath == "" {
		return nil
	}
	return SnapshotEntity(dbPath, "prompt", promptName, content, changedBy, reason)
}

// SnapshotEntity stores a versioned snapshot of any entity.
func SnapshotEntity(dbPath, entityType, entityName, content, changedBy, reason string) error {
	// Get previous version for diff.
	prev, _ := QueryLatest(dbPath, entityType, entityName)

	// Compute diff summary.
	diffSummary := ""
	if prev != nil {
		diffSummary = ComputeDiffSummary(prev.ContentJSON, content, entityType)
	} else {
		diffSummary = "initial version"
	}

	// Skip if content is identical to previous.
	if prev != nil && prev.ContentJSON == content {
		return nil
	}

	vid := NewID()
	now := time.Now().Format(time.RFC3339)

	sql := fmt.Sprintf(
		`INSERT INTO config_versions (version_id, entity_type, entity_name, content_json, diff_summary, changed_by, reason, created_at)
		 VALUES ('%s','%s','%s','%s','%s','%s','%s','%s')`,
		db.Escape(vid),
		db.Escape(entityType),
		db.Escape(entityName),
		db.Escape(content),
		db.Escape(diffSummary),
		db.Escape(changedBy),
		db.Escape(reason),
		db.Escape(now),
	)
	if err := db.Exec(dbPath, sql); err != nil {
		return fmt.Errorf("snapshot %s/%s: %w", entityType, entityName, err)
	}

	// Prune old versions.
	Prune(dbPath, entityType, entityName, MaxPerEntity)

	return nil
}

// --- Query Functions ---

// QueryLatest returns the most recent version of an entity.
func QueryLatest(dbPath, entityType, entityName string) (*Version, error) {
	sql := fmt.Sprintf(
		`SELECT id, version_id, entity_type, entity_name, content_json, diff_summary, changed_by, reason, created_at
		 FROM config_versions
		 WHERE entity_type='%s' AND entity_name='%s'
		 ORDER BY id DESC LIMIT 1`,
		db.Escape(entityType), db.Escape(entityName))

	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	v := versionFromRow(rows[0])
	return &v, nil
}

// QueryVersions returns version history for an entity type/name.
func QueryVersions(dbPath, entityType, entityName string, limit int) ([]Version, error) {
	if limit <= 0 {
		limit = 20
	}
	where := fmt.Sprintf("WHERE entity_type='%s'", db.Escape(entityType))
	if entityName != "" {
		where += fmt.Sprintf(" AND entity_name='%s'", db.Escape(entityName))
	}

	sql := fmt.Sprintf(
		`SELECT id, version_id, entity_type, entity_name, '' as content_json, diff_summary, changed_by, reason, created_at
		 FROM config_versions %s ORDER BY id DESC LIMIT %d`,
		where, limit)

	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil, err
	}

	var versions []Version
	for _, row := range rows {
		versions = append(versions, versionFromRow(row))
	}
	return versions, nil
}

// QueryByID returns a specific version by its version_id.
// Supports prefix matching when an exact match is not found.
func QueryByID(dbPath, versionID string) (*Version, error) {
	sql := fmt.Sprintf(
		`SELECT id, version_id, entity_type, entity_name, content_json, diff_summary, changed_by, reason, created_at
		 FROM config_versions WHERE version_id='%s'`,
		db.Escape(versionID))

	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		// Try prefix match.
		sql2 := fmt.Sprintf(
			`SELECT id, version_id, entity_type, entity_name, content_json, diff_summary, changed_by, reason, created_at
			 FROM config_versions WHERE version_id LIKE '%s%%' ORDER BY id DESC LIMIT 1`,
			db.Escape(versionID))
		rows, err = db.Query(dbPath, sql2)
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			return nil, fmt.Errorf("version %q not found", versionID)
		}
	}
	v := versionFromRow(rows[0])
	return &v, nil
}

// QueryAllEntities returns a list of unique (type, name) pairs with version counts.
func QueryAllEntities(dbPath string) ([]Version, error) {
	sql := `SELECT DISTINCT entity_type, entity_name,
	        (SELECT COUNT(*) FROM config_versions cv2
	         WHERE cv2.entity_type=cv.entity_type AND cv2.entity_name=cv.entity_name) as cnt,
	        (SELECT MAX(created_at) FROM config_versions cv3
	         WHERE cv3.entity_type=cv.entity_type AND cv3.entity_name=cv.entity_name) as latest
	        FROM config_versions cv ORDER BY entity_type, entity_name`

	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil, err
	}

	var entities []Version
	for _, row := range rows {
		entities = append(entities, Version{
			EntityType: db.Str(row["entity_type"]),
			EntityName: db.Str(row["entity_name"]),
			CreatedAt:  db.Str(row["latest"]),
			Reason:     fmt.Sprintf("%d versions", db.Int(row["cnt"])),
		})
	}
	return entities, nil
}

func versionFromRow(row map[string]any) Version {
	return Version{
		ID:          db.Int(row["id"]),
		VersionID:   db.Str(row["version_id"]),
		EntityType:  db.Str(row["entity_type"]),
		EntityName:  db.Str(row["entity_name"]),
		ContentJSON: db.Str(row["content_json"]),
		DiffSummary: db.Str(row["diff_summary"]),
		ChangedBy:   db.Str(row["changed_by"]),
		Reason:      db.Str(row["reason"]),
		CreatedAt:   db.Str(row["created_at"]),
	}
}

// --- Restore Functions ---

// RestoreConfig restores config.json to a saved version.
// Returns the previous content for undo purposes.
func RestoreConfig(dbPath, configPath, versionID string) (string, error) {
	ver, err := QueryByID(dbPath, versionID)
	if err != nil {
		return "", err
	}
	if ver.EntityType != "config" {
		return "", fmt.Errorf("version %q is a %s, not a config", versionID, ver.EntityType)
	}

	// Read current config for backup snapshot.
	current, err := os.ReadFile(configPath)
	if err != nil {
		return "", fmt.Errorf("read current config: %w", err)
	}

	// Snapshot current state before overwrite.
	SnapshotEntity(dbPath, "config", "config.json", string(current), "system", fmt.Sprintf("pre-rollback to %s", versionID))

	// Validate the restored content is valid JSON.
	var check map[string]json.RawMessage
	if err := json.Unmarshal([]byte(ver.ContentJSON), &check); err != nil {
		return "", fmt.Errorf("stored version has invalid JSON: %w", err)
	}

	// Write restored content.
	if err := os.WriteFile(configPath, []byte(ver.ContentJSON), 0o600); err != nil {
		return "", fmt.Errorf("write restored config: %w", err)
	}

	// Snapshot the restored state.
	SnapshotEntity(dbPath, "config", "config.json", ver.ContentJSON, "system", fmt.Sprintf("rollback to %s", versionID))

	return string(current), nil
}

// --- Diff Functions ---

// ComputeDiffSummary generates a human-readable diff between two content strings.
// For JSON entities, it compares keys. For text, it compares lines.
func ComputeDiffSummary(oldContent, newContent, entityType string) string {
	if entityType == "prompt" {
		return ComputeTextDiff(oldContent, newContent)
	}
	return ComputeJSONDiff(oldContent, newContent)
}

// ComputeJSONDiff compares two JSON strings and returns a summary of changes.
func ComputeJSONDiff(oldJSON, newJSON string) string {
	var oldMap, newMap map[string]any
	if err := json.Unmarshal([]byte(oldJSON), &oldMap); err != nil {
		return "changed (old content not valid JSON)"
	}
	if err := json.Unmarshal([]byte(newJSON), &newMap); err != nil {
		return "changed (new content not valid JSON)"
	}

	var added, removed, changed []string
	FlattenDiff("", oldMap, newMap, &added, &removed, &changed)

	var parts []string
	if len(added) > 0 {
		if len(added) <= 5 {
			parts = append(parts, fmt.Sprintf("+%s", strings.Join(added, ", +")))
		} else {
			parts = append(parts, fmt.Sprintf("+%d fields added", len(added)))
		}
	}
	if len(removed) > 0 {
		if len(removed) <= 5 {
			parts = append(parts, fmt.Sprintf("-%s", strings.Join(removed, ", -")))
		} else {
			parts = append(parts, fmt.Sprintf("-%d fields removed", len(removed)))
		}
	}
	if len(changed) > 0 {
		if len(changed) <= 5 {
			parts = append(parts, fmt.Sprintf("~%s", strings.Join(changed, ", ~")))
		} else {
			parts = append(parts, fmt.Sprintf("~%d fields changed", len(changed)))
		}
	}

	if len(parts) == 0 {
		return "no changes"
	}
	return strings.Join(parts, "; ")
}

// FlattenDiff recursively compares two maps and categorizes differences.
func FlattenDiff(prefix string, old, new map[string]any, added, removed, changed *[]string) {
	allKeys := make(map[string]bool)
	for k := range old {
		allKeys[k] = true
	}
	for k := range new {
		allKeys[k] = true
	}

	keys := make([]string, 0, len(allKeys))
	for k := range allKeys {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		fullKey := k
		if prefix != "" {
			fullKey = prefix + "." + k
		}

		oldVal, oldOk := old[k]
		newVal, newOk := new[k]

		if !oldOk {
			*added = append(*added, fullKey)
			continue
		}
		if !newOk {
			*removed = append(*removed, fullKey)
			continue
		}

		// Both exist — compare.
		oldSub, oldIsSub := oldVal.(map[string]any)
		newSub, newIsSub := newVal.(map[string]any)
		if oldIsSub && newIsSub {
			FlattenDiff(fullKey, oldSub, newSub, added, removed, changed)
			continue
		}

		// Compare serialized form.
		oldJSON, _ := json.Marshal(oldVal)
		newJSON, _ := json.Marshal(newVal)
		if string(oldJSON) != string(newJSON) {
			*changed = append(*changed, fullKey)
		}
	}
}

// ComputeTextDiff compares two text contents line-by-line.
func ComputeTextDiff(oldText, newText string) string {
	oldLines := strings.Split(oldText, "\n")
	newLines := strings.Split(newText, "\n")

	added := 0
	removed := 0

	// Simple diff: count lines.
	oldSet := make(map[string]int)
	for _, l := range oldLines {
		oldSet[l]++
	}
	newSet := make(map[string]int)
	for _, l := range newLines {
		newSet[l]++
	}

	for l, c := range newSet {
		if oldC, ok := oldSet[l]; ok {
			if c > oldC {
				added += c - oldC
			}
		} else {
			added += c
		}
	}
	for l, c := range oldSet {
		if newC, ok := newSet[l]; ok {
			if c > newC {
				removed += c - newC
			}
		} else {
			removed += c
		}
	}

	if added == 0 && removed == 0 {
		return "no changes"
	}

	var parts []string
	if added > 0 {
		parts = append(parts, fmt.Sprintf("+%d lines", added))
	}
	if removed > 0 {
		parts = append(parts, fmt.Sprintf("-%d lines", removed))
	}
	return strings.Join(parts, ", ")
}

// DiffDetail returns a detailed JSON diff between two versions.
func DiffDetail(dbPath, versionID1, versionID2 string) (map[string]any, error) {
	v1, err := QueryByID(dbPath, versionID1)
	if err != nil {
		return nil, fmt.Errorf("version 1: %w", err)
	}
	v2, err := QueryByID(dbPath, versionID2)
	if err != nil {
		return nil, fmt.Errorf("version 2: %w", err)
	}

	result := map[string]any{
		"from": map[string]any{
			"versionId": v1.VersionID,
			"createdAt": v1.CreatedAt,
			"changedBy": v1.ChangedBy,
		},
		"to": map[string]any{
			"versionId": v2.VersionID,
			"createdAt": v2.CreatedAt,
			"changedBy": v2.ChangedBy,
		},
	}

	if v1.EntityType == "prompt" || v2.EntityType == "prompt" {
		result["textDiff"] = ComputeTextDiff(v1.ContentJSON, v2.ContentJSON)
		return result, nil
	}

	// JSON diff.
	var oldMap, newMap map[string]any
	json.Unmarshal([]byte(v1.ContentJSON), &oldMap)
	json.Unmarshal([]byte(v2.ContentJSON), &newMap)

	if oldMap == nil {
		oldMap = map[string]any{}
	}
	if newMap == nil {
		newMap = map[string]any{}
	}

	var added, removed, changed []string
	FlattenDiff("", oldMap, newMap, &added, &removed, &changed)

	// Build detailed changes.
	changes := make([]map[string]any, 0)
	for _, k := range added {
		changes = append(changes, map[string]any{"field": k, "type": "added", "newValue": GetNestedValue(newMap, k)})
	}
	for _, k := range removed {
		changes = append(changes, map[string]any{"field": k, "type": "removed", "oldValue": GetNestedValue(oldMap, k)})
	}
	for _, k := range changed {
		changes = append(changes, map[string]any{
			"field":    k,
			"type":     "changed",
			"oldValue": GetNestedValue(oldMap, k),
			"newValue": GetNestedValue(newMap, k),
		})
	}

	result["changes"] = changes
	result["summary"] = ComputeJSONDiff(v1.ContentJSON, v2.ContentJSON)
	return result, nil
}

// GetNestedValue retrieves a value from a nested map by dot-separated path.
func GetNestedValue(m map[string]any, path string) any {
	parts := strings.Split(path, ".")
	current := any(m)
	for _, p := range parts {
		cm, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current, ok = cm[p]
		if !ok {
			return nil
		}
	}
	return current
}

// --- Prune ---

// Prune keeps only the most recent keep versions per entity.
func Prune(dbPath, entityType, entityName string, keep int) {
	if dbPath == "" || keep <= 0 {
		return
	}
	sql := fmt.Sprintf(
		`DELETE FROM config_versions
		 WHERE entity_type='%s' AND entity_name='%s'
		 AND id NOT IN (
		   SELECT id FROM config_versions
		   WHERE entity_type='%s' AND entity_name='%s'
		   ORDER BY id DESC LIMIT %d
		 )`,
		db.Escape(entityType), db.Escape(entityName),
		db.Escape(entityType), db.Escape(entityName),
		keep)

	db.Exec(dbPath, sql) // ignore errors
}

// Cleanup removes all versions older than days days.
func Cleanup(dbPath string, days int) {
	if dbPath == "" || days <= 0 {
		return
	}
	sql := fmt.Sprintf(
		`DELETE FROM config_versions WHERE datetime(created_at) < datetime('now','-%d days')`, days)
	db.Exec(dbPath, sql) // ignore errors
}
