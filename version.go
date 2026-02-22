package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// --- Version Types ---

// ConfigVersion represents a saved version of a configuration entity.
type ConfigVersion struct {
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

const maxVersionsPerEntity = 50

// --- DB Init ---

func initVersionDB(dbPath string) error {
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
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("init config_versions: %s: %w", string(out), err)
	}
	return nil
}

// --- Version ID Generation ---

func newVersionID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return fmt.Sprintf("v-%x", b)
}

// --- Snapshot Functions ---

// snapshotConfig takes a snapshot of the current config.json content.
// It computes a diff against the previous version and stores both.
func snapshotConfig(dbPath, configPath, changedBy, reason string) error {
	if dbPath == "" {
		return nil
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config for snapshot: %w", err)
	}
	return snapshotEntity(dbPath, "config", "config.json", string(data), changedBy, reason)
}

// snapshotWorkflow takes a snapshot of a workflow definition.
func snapshotWorkflow(dbPath, workflowName, content, changedBy, reason string) error {
	if dbPath == "" {
		return nil
	}
	return snapshotEntity(dbPath, "workflow", workflowName, content, changedBy, reason)
}

// snapshotPrompt takes a snapshot of a prompt template.
func snapshotPrompt(dbPath, promptName, content, changedBy, reason string) error {
	if dbPath == "" {
		return nil
	}
	return snapshotEntity(dbPath, "prompt", promptName, content, changedBy, reason)
}

// snapshotEntity stores a versioned snapshot of any entity.
func snapshotEntity(dbPath, entityType, entityName, content, changedBy, reason string) error {
	// Get previous version for diff.
	prev, _ := queryLatestVersion(dbPath, entityType, entityName)

	// Compute diff summary.
	diffSummary := ""
	if prev != nil {
		diffSummary = computeDiffSummary(prev.ContentJSON, content, entityType)
	} else {
		diffSummary = "initial version"
	}

	// Skip if content is identical to previous.
	if prev != nil && prev.ContentJSON == content {
		return nil
	}

	vid := newVersionID()
	now := time.Now().Format(time.RFC3339)

	sql := fmt.Sprintf(
		`INSERT INTO config_versions (version_id, entity_type, entity_name, content_json, diff_summary, changed_by, reason, created_at)
		 VALUES ('%s','%s','%s','%s','%s','%s','%s','%s')`,
		escapeSQLite(vid),
		escapeSQLite(entityType),
		escapeSQLite(entityName),
		escapeSQLite(content),
		escapeSQLite(diffSummary),
		escapeSQLite(changedBy),
		escapeSQLite(reason),
		escapeSQLite(now),
	)
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("snapshot %s/%s: %s: %w", entityType, entityName, string(out), err)
	}

	// Prune old versions.
	pruneVersions(dbPath, entityType, entityName, maxVersionsPerEntity)

	return nil
}

// --- Query Functions ---

// queryLatestVersion returns the most recent version of an entity.
func queryLatestVersion(dbPath, entityType, entityName string) (*ConfigVersion, error) {
	sql := fmt.Sprintf(
		`SELECT id, version_id, entity_type, entity_name, content_json, diff_summary, changed_by, reason, created_at
		 FROM config_versions
		 WHERE entity_type='%s' AND entity_name='%s'
		 ORDER BY id DESC LIMIT 1`,
		escapeSQLite(entityType), escapeSQLite(entityName))

	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	v := versionFromRow(rows[0])
	return &v, nil
}

// queryVersions returns version history for an entity type/name.
func queryVersions(dbPath, entityType, entityName string, limit int) ([]ConfigVersion, error) {
	if limit <= 0 {
		limit = 20
	}
	where := fmt.Sprintf("WHERE entity_type='%s'", escapeSQLite(entityType))
	if entityName != "" {
		where += fmt.Sprintf(" AND entity_name='%s'", escapeSQLite(entityName))
	}

	sql := fmt.Sprintf(
		`SELECT id, version_id, entity_type, entity_name, '' as content_json, diff_summary, changed_by, reason, created_at
		 FROM config_versions %s ORDER BY id DESC LIMIT %d`,
		where, limit)

	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return nil, err
	}

	var versions []ConfigVersion
	for _, row := range rows {
		versions = append(versions, versionFromRow(row))
	}
	return versions, nil
}

// queryVersionByID returns a specific version by its version_id.
func queryVersionByID(dbPath, versionID string) (*ConfigVersion, error) {
	sql := fmt.Sprintf(
		`SELECT id, version_id, entity_type, entity_name, content_json, diff_summary, changed_by, reason, created_at
		 FROM config_versions WHERE version_id='%s'`,
		escapeSQLite(versionID))

	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		// Try prefix match.
		sql2 := fmt.Sprintf(
			`SELECT id, version_id, entity_type, entity_name, content_json, diff_summary, changed_by, reason, created_at
			 FROM config_versions WHERE version_id LIKE '%s%%' ORDER BY id DESC LIMIT 1`,
			escapeSQLite(versionID))
		rows, err = queryDB(dbPath, sql2)
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

// queryAllVersionedEntities returns a list of unique (type, name) pairs.
func queryAllVersionedEntities(dbPath string) ([]ConfigVersion, error) {
	sql := `SELECT DISTINCT entity_type, entity_name,
	        (SELECT COUNT(*) FROM config_versions cv2
	         WHERE cv2.entity_type=cv.entity_type AND cv2.entity_name=cv.entity_name) as cnt,
	        (SELECT MAX(created_at) FROM config_versions cv3
	         WHERE cv3.entity_type=cv.entity_type AND cv3.entity_name=cv.entity_name) as latest
	        FROM config_versions cv ORDER BY entity_type, entity_name`

	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return nil, err
	}

	var entities []ConfigVersion
	for _, row := range rows {
		entities = append(entities, ConfigVersion{
			EntityType: jsonStr(row["entity_type"]),
			EntityName: jsonStr(row["entity_name"]),
			CreatedAt:  jsonStr(row["latest"]),
			Reason:     fmt.Sprintf("%d versions", jsonInt(row["cnt"])),
		})
	}
	return entities, nil
}

func versionFromRow(row map[string]any) ConfigVersion {
	return ConfigVersion{
		ID:          jsonInt(row["id"]),
		VersionID:   jsonStr(row["version_id"]),
		EntityType:  jsonStr(row["entity_type"]),
		EntityName:  jsonStr(row["entity_name"]),
		ContentJSON: jsonStr(row["content_json"]),
		DiffSummary: jsonStr(row["diff_summary"]),
		ChangedBy:   jsonStr(row["changed_by"]),
		Reason:      jsonStr(row["reason"]),
		CreatedAt:   jsonStr(row["created_at"]),
	}
}

// --- Restore Functions ---

// restoreConfigVersion restores config.json to a saved version.
// Returns the previous content for undo purposes.
func restoreConfigVersion(dbPath, configPath, versionID string) (string, error) {
	ver, err := queryVersionByID(dbPath, versionID)
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
	snapshotEntity(dbPath, "config", "config.json", string(current), "system", fmt.Sprintf("pre-rollback to %s", versionID))

	// Validate the restored content is valid JSON.
	var check map[string]json.RawMessage
	if err := json.Unmarshal([]byte(ver.ContentJSON), &check); err != nil {
		return "", fmt.Errorf("stored version has invalid JSON: %w", err)
	}

	// Write restored content.
	if err := os.WriteFile(configPath, []byte(ver.ContentJSON), 0o644); err != nil {
		return "", fmt.Errorf("write restored config: %w", err)
	}

	// Snapshot the restored state.
	snapshotEntity(dbPath, "config", "config.json", ver.ContentJSON, "system", fmt.Sprintf("rollback to %s", versionID))

	return string(current), nil
}

// restoreWorkflowVersion restores a workflow to a saved version.
func restoreWorkflowVersion(dbPath string, cfg *Config, versionID string) error {
	ver, err := queryVersionByID(dbPath, versionID)
	if err != nil {
		return err
	}
	if ver.EntityType != "workflow" {
		return fmt.Errorf("version %q is a %s, not a workflow", versionID, ver.EntityType)
	}

	// Validate the stored content.
	var wf Workflow
	if err := json.Unmarshal([]byte(ver.ContentJSON), &wf); err != nil {
		return fmt.Errorf("stored version has invalid workflow JSON: %w", err)
	}

	// Read current workflow for backup snapshot (if it exists).
	existing, err := loadWorkflowByName(cfg, ver.EntityName)
	if err == nil && existing != nil {
		data, _ := json.MarshalIndent(existing, "", "  ")
		snapshotEntity(dbPath, "workflow", ver.EntityName, string(data), "system", fmt.Sprintf("pre-rollback to %s", versionID))
	}

	// Save the restored workflow.
	if err := saveWorkflow(cfg, &wf); err != nil {
		return fmt.Errorf("write restored workflow: %w", err)
	}

	// Snapshot the restored state.
	snapshotEntity(dbPath, "workflow", ver.EntityName, ver.ContentJSON, "system", fmt.Sprintf("rollback to %s", versionID))

	return nil
}

// --- Diff Functions ---

// computeDiffSummary generates a human-readable diff between two content strings.
// For JSON entities, it compares keys. For text, it compares lines.
func computeDiffSummary(oldContent, newContent, entityType string) string {
	if entityType == "prompt" {
		return computeTextDiff(oldContent, newContent)
	}
	return computeJSONDiff(oldContent, newContent)
}

// computeJSONDiff compares two JSON strings and returns a summary of changes.
func computeJSONDiff(oldJSON, newJSON string) string {
	var oldMap, newMap map[string]any
	if err := json.Unmarshal([]byte(oldJSON), &oldMap); err != nil {
		return "changed (old content not valid JSON)"
	}
	if err := json.Unmarshal([]byte(newJSON), &newMap); err != nil {
		return "changed (new content not valid JSON)"
	}

	var added, removed, changed []string
	flattenDiff("", oldMap, newMap, &added, &removed, &changed)

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

// flattenDiff recursively compares two maps and categorizes differences.
func flattenDiff(prefix string, old, new map[string]any, added, removed, changed *[]string) {
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
			flattenDiff(fullKey, oldSub, newSub, added, removed, changed)
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

// computeTextDiff compares two text contents line-by-line.
func computeTextDiff(oldText, newText string) string {
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

// versionDiffDetail returns a detailed JSON diff between two versions.
func versionDiffDetail(dbPath, versionID1, versionID2 string) (map[string]any, error) {
	v1, err := queryVersionByID(dbPath, versionID1)
	if err != nil {
		return nil, fmt.Errorf("version 1: %w", err)
	}
	v2, err := queryVersionByID(dbPath, versionID2)
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
		result["textDiff"] = computeTextDiff(v1.ContentJSON, v2.ContentJSON)
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
	flattenDiff("", oldMap, newMap, &added, &removed, &changed)

	// Build detailed changes.
	changes := make([]map[string]any, 0)
	for _, k := range added {
		changes = append(changes, map[string]any{"field": k, "type": "added", "newValue": versionGetNestedValue(newMap, k)})
	}
	for _, k := range removed {
		changes = append(changes, map[string]any{"field": k, "type": "removed", "oldValue": versionGetNestedValue(oldMap, k)})
	}
	for _, k := range changed {
		changes = append(changes, map[string]any{
			"field":    k,
			"type":     "changed",
			"oldValue": versionGetNestedValue(oldMap, k),
			"newValue": versionGetNestedValue(newMap, k),
		})
	}

	result["changes"] = changes
	result["summary"] = computeJSONDiff(v1.ContentJSON, v2.ContentJSON)
	return result, nil
}

// versionGetNestedValue retrieves a value from a nested map by dot-separated path.
func versionGetNestedValue(m map[string]any, path string) any {
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

// pruneVersions keeps only the most recent N versions per entity.
func pruneVersions(dbPath, entityType, entityName string, keep int) {
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
		escapeSQLite(entityType), escapeSQLite(entityName),
		escapeSQLite(entityType), escapeSQLite(entityName),
		keep)

	cmd := exec.Command("sqlite3", dbPath, sql)
	cmd.CombinedOutput() // ignore errors
}

// cleanupVersions removes all versions older than N days.
func cleanupVersions(dbPath string, days int) {
	if dbPath == "" || days <= 0 {
		return
	}
	sql := fmt.Sprintf(
		`DELETE FROM config_versions WHERE datetime(created_at) < datetime('now','-%d days')`, days)
	cmd := exec.Command("sqlite3", dbPath, sql)
	cmd.CombinedOutput()
}
