package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupVersionTestDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	if err := initVersionDB(dbPath); err != nil {
		t.Fatalf("initVersionDB: %v", err)
	}
	return dbPath
}

func TestInitVersionDB(t *testing.T) {
	dbPath := setupVersionTestDB(t)
	// Should be idempotent.
	if err := initVersionDB(dbPath); err != nil {
		t.Fatalf("second initVersionDB: %v", err)
	}
}

func TestSnapshotEntity(t *testing.T) {
	dbPath := setupVersionTestDB(t)

	content := `{"key":"value"}`
	if err := snapshotEntity(dbPath, "config", "config.json", content, "test", "initial"); err != nil {
		t.Fatalf("snapshotEntity: %v", err)
	}

	versions, err := queryVersions(dbPath, "config", "config.json", 10)
	if err != nil {
		t.Fatalf("queryVersions: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("expected 1 version, got %d", len(versions))
	}
	if versions[0].EntityType != "config" {
		t.Errorf("entityType: got %q, want %q", versions[0].EntityType, "config")
	}
	if versions[0].EntityName != "config.json" {
		t.Errorf("entityName: got %q, want %q", versions[0].EntityName, "config.json")
	}
	if versions[0].ChangedBy != "test" {
		t.Errorf("changedBy: got %q, want %q", versions[0].ChangedBy, "test")
	}
	if versions[0].DiffSummary != "initial version" {
		t.Errorf("diffSummary: got %q, want %q", versions[0].DiffSummary, "initial version")
	}
}

func TestSnapshotEntitySkipsDuplicateContent(t *testing.T) {
	dbPath := setupVersionTestDB(t)

	content := `{"key":"value"}`
	snapshotEntity(dbPath, "config", "config.json", content, "test", "first")
	snapshotEntity(dbPath, "config", "config.json", content, "test", "second")

	versions, _ := queryVersions(dbPath, "config", "config.json", 10)
	if len(versions) != 1 {
		t.Errorf("expected 1 version (skip duplicate), got %d", len(versions))
	}
}

func TestSnapshotEntityRecordsDiff(t *testing.T) {
	dbPath := setupVersionTestDB(t)

	v1 := `{"key":"old","extra":"val"}`
	v2 := `{"key":"new","added":"field"}`

	snapshotEntity(dbPath, "config", "config.json", v1, "test", "v1")
	snapshotEntity(dbPath, "config", "config.json", v2, "test", "v2")

	versions, _ := queryVersions(dbPath, "config", "config.json", 10)
	if len(versions) != 2 {
		t.Fatalf("expected 2 versions, got %d", len(versions))
	}

	// Most recent first.
	diff := versions[0].DiffSummary
	if diff == "" || diff == "initial version" || diff == "no changes" {
		t.Errorf("expected meaningful diff, got %q", diff)
	}
	// Should mention added and removed/changed fields.
	if !strings.Contains(diff, "added") && !strings.Contains(diff, "+") {
		t.Errorf("diff should mention added field: %q", diff)
	}
}

func TestQueryLatestVersion(t *testing.T) {
	dbPath := setupVersionTestDB(t)

	snapshotEntity(dbPath, "config", "config.json", `{"v":1}`, "test", "v1")
	snapshotEntity(dbPath, "config", "config.json", `{"v":2}`, "test", "v2")

	latest, err := queryLatestVersion(dbPath, "config", "config.json")
	if err != nil {
		t.Fatalf("queryLatestVersion: %v", err)
	}
	if latest == nil {
		t.Fatal("expected non-nil version")
	}
	if latest.ContentJSON != `{"v":2}` {
		t.Errorf("content: got %q, want %q", latest.ContentJSON, `{"v":2}`)
	}
}

func TestQueryVersionByID(t *testing.T) {
	dbPath := setupVersionTestDB(t)

	snapshotEntity(dbPath, "config", "config.json", `{"x":1}`, "test", "test-reason")

	versions, _ := queryVersions(dbPath, "config", "config.json", 10)
	if len(versions) == 0 {
		t.Fatal("no versions")
	}

	ver, err := queryVersionByID(dbPath, versions[0].VersionID)
	if err != nil {
		t.Fatalf("queryVersionByID: %v", err)
	}
	if ver.ContentJSON != `{"x":1}` {
		t.Errorf("content mismatch")
	}
	if ver.Reason != "test-reason" {
		t.Errorf("reason: got %q, want %q", ver.Reason, "test-reason")
	}
}

func TestQueryVersionByIDPrefixMatch(t *testing.T) {
	dbPath := setupVersionTestDB(t)

	snapshotEntity(dbPath, "config", "config.json", `{"p":"prefix"}`, "test", "")

	versions, _ := queryVersions(dbPath, "config", "config.json", 10)
	if len(versions) == 0 {
		t.Fatal("no versions")
	}

	// Use first 3 characters as prefix.
	prefix := versions[0].VersionID[:3]
	ver, err := queryVersionByID(dbPath, prefix)
	if err != nil {
		t.Fatalf("queryVersionByID prefix: %v", err)
	}
	if ver.VersionID != versions[0].VersionID {
		t.Errorf("prefix mismatch: got %q, want %q", ver.VersionID, versions[0].VersionID)
	}
}

func TestQueryVersionByIDNotFound(t *testing.T) {
	dbPath := setupVersionTestDB(t)

	_, err := queryVersionByID(dbPath, "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent version")
	}
}

func TestSnapshotConfig(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	initVersionDB(dbPath)

	configPath := filepath.Join(dir, "config.json")
	os.WriteFile(configPath, []byte(`{"listenAddr":"127.0.0.1:7777"}`), 0o644)

	if err := snapshotConfig(dbPath, configPath, "test", "initial"); err != nil {
		t.Fatalf("snapshotConfig: %v", err)
	}

	versions, _ := queryVersions(dbPath, "config", "config.json", 10)
	if len(versions) != 1 {
		t.Errorf("expected 1 version, got %d", len(versions))
	}
}

func TestSnapshotWorkflow(t *testing.T) {
	dbPath := setupVersionTestDB(t)

	wf := `{"name":"test-wf","steps":[{"id":"s1","prompt":"hello"}]}`
	if err := snapshotWorkflow(dbPath, "test-wf", wf, "cli", "created"); err != nil {
		t.Fatalf("snapshotWorkflow: %v", err)
	}

	versions, _ := queryVersions(dbPath, "workflow", "test-wf", 10)
	if len(versions) != 1 {
		t.Errorf("expected 1 version, got %d", len(versions))
	}
	if versions[0].EntityName != "test-wf" {
		t.Errorf("entityName: got %q", versions[0].EntityName)
	}
}

func TestSnapshotPrompt(t *testing.T) {
	dbPath := setupVersionTestDB(t)

	if err := snapshotPrompt(dbPath, "greeting", "Hello {{name}}", "cli", "new prompt"); err != nil {
		t.Fatalf("snapshotPrompt: %v", err)
	}

	versions, _ := queryVersions(dbPath, "prompt", "greeting", 10)
	if len(versions) != 1 {
		t.Errorf("expected 1 version, got %d", len(versions))
	}
}

func TestRestoreConfigVersion(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	initVersionDB(dbPath)
	configPath := filepath.Join(dir, "config.json")

	// Write initial config.
	v1 := `{"listenAddr":"127.0.0.1:7777","apiToken":"abc"}`
	os.WriteFile(configPath, []byte(v1), 0o644)
	snapshotConfig(dbPath, configPath, "test", "v1")

	// Get v1 version ID.
	versions, _ := queryVersions(dbPath, "config", "config.json", 10)
	v1ID := versions[0].VersionID

	// Modify config.
	v2 := `{"listenAddr":"0.0.0.0:8888","apiToken":"xyz"}`
	os.WriteFile(configPath, []byte(v2), 0o644)
	snapshotConfig(dbPath, configPath, "test", "v2")

	// Restore to v1.
	prev, err := restoreConfigVersion(dbPath, configPath, v1ID)
	if err != nil {
		t.Fatalf("restoreConfigVersion: %v", err)
	}
	if prev != v2 {
		t.Errorf("previous content mismatch")
	}

	// Check file content was restored.
	data, _ := os.ReadFile(configPath)
	if string(data) != v1 {
		t.Errorf("restored config mismatch: got %q, want %q", string(data), v1)
	}
}

func TestRestoreConfigVersionInvalidType(t *testing.T) {
	dbPath := setupVersionTestDB(t)

	snapshotEntity(dbPath, "workflow", "my-wf", `{"name":"my-wf"}`, "test", "")
	versions, _ := queryVersions(dbPath, "workflow", "my-wf", 10)
	vid := versions[0].VersionID

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	os.WriteFile(configPath, []byte(`{}`), 0o644)

	_, err := restoreConfigVersion(dbPath, configPath, vid)
	if err == nil {
		t.Error("expected error for wrong entity type")
	}
	if !strings.Contains(err.Error(), "not a config") {
		t.Errorf("error should mention type mismatch: %v", err)
	}
}

func TestComputeJSONDiff(t *testing.T) {
	tests := []struct {
		name     string
		old      string
		new      string
		contains []string
	}{
		{
			name:     "no changes",
			old:      `{"a":1}`,
			new:      `{"a":1}`,
			contains: []string{"no changes"},
		},
		{
			name:     "added field",
			old:      `{"a":1}`,
			new:      `{"a":1,"b":2}`,
			contains: []string{"+", "b"},
		},
		{
			name:     "removed field",
			old:      `{"a":1,"b":2}`,
			new:      `{"a":1}`,
			contains: []string{"-", "b"},
		},
		{
			name:     "changed field",
			old:      `{"a":1}`,
			new:      `{"a":2}`,
			contains: []string{"~", "a"},
		},
		{
			name:     "nested change",
			old:      `{"a":{"x":1}}`,
			new:      `{"a":{"x":2}}`,
			contains: []string{"a.x"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := computeJSONDiff(tt.old, tt.new)
			for _, c := range tt.contains {
				if !strings.Contains(result, c) {
					t.Errorf("diff %q should contain %q", result, c)
				}
			}
		})
	}
}

func TestComputeTextDiff(t *testing.T) {
	old := "line1\nline2\nline3"
	new := "line1\nmodified\nline3\nline4"

	diff := computeTextDiff(old, new)
	if !strings.Contains(diff, "+") {
		t.Errorf("text diff should show additions: %q", diff)
	}
	if !strings.Contains(diff, "-") {
		t.Errorf("text diff should show removals: %q", diff)
	}
}

func TestComputeTextDiffNoChanges(t *testing.T) {
	text := "hello\nworld"
	diff := computeTextDiff(text, text)
	if diff != "no changes" {
		t.Errorf("expected 'no changes', got %q", diff)
	}
}

func TestVersionDiffDetail(t *testing.T) {
	dbPath := setupVersionTestDB(t)

	snapshotEntity(dbPath, "config", "config.json", `{"a":1,"b":"old"}`, "test", "v1")
	snapshotEntity(dbPath, "config", "config.json", `{"a":1,"b":"new","c":true}`, "test", "v2")

	versions, _ := queryVersions(dbPath, "config", "config.json", 10)
	if len(versions) < 2 {
		t.Fatal("need 2 versions")
	}

	result, err := versionDiffDetail(dbPath, versions[1].VersionID, versions[0].VersionID)
	if err != nil {
		t.Fatalf("versionDiffDetail: %v", err)
	}

	changes, ok := result["changes"].([]map[string]any)
	if !ok {
		t.Fatal("expected changes array")
	}
	if len(changes) == 0 {
		t.Error("expected some changes")
	}

	// Should have at least 'b' changed and 'c' added.
	foundChanged := false
	foundAdded := false
	for _, ch := range changes {
		if ch["field"] == "b" && ch["type"] == "changed" {
			foundChanged = true
		}
		if ch["field"] == "c" && ch["type"] == "added" {
			foundAdded = true
		}
	}
	if !foundChanged {
		t.Error("expected 'b' to be marked as changed")
	}
	if !foundAdded {
		t.Error("expected 'c' to be marked as added")
	}
}

func TestPruneVersions(t *testing.T) {
	dbPath := setupVersionTestDB(t)

	// Create 10 versions.
	for i := 0; i < 10; i++ {
		content := `{"v":` + strings.Repeat("x", i+1) + `}`
		snapshotEntity(dbPath, "config", "config.json", content, "test", "")
	}

	// Prune to keep only 3.
	pruneVersions(dbPath, "config", "config.json", 3)

	versions, _ := queryVersions(dbPath, "config", "config.json", 50)
	if len(versions) != 3 {
		t.Errorf("expected 3 versions after prune, got %d", len(versions))
	}
}

func TestQueryAllVersionedEntities(t *testing.T) {
	dbPath := setupVersionTestDB(t)

	snapshotEntity(dbPath, "config", "config.json", `{"a":1}`, "test", "")
	snapshotEntity(dbPath, "workflow", "deploy", `{"name":"deploy"}`, "test", "")
	snapshotEntity(dbPath, "prompt", "greeting", "Hello!", "test", "")

	entities, err := queryAllVersionedEntities(dbPath)
	if err != nil {
		t.Fatalf("queryAllVersionedEntities: %v", err)
	}
	if len(entities) != 3 {
		t.Errorf("expected 3 entities, got %d", len(entities))
	}

	// Verify types.
	types := make(map[string]bool)
	for _, e := range entities {
		types[e.EntityType] = true
	}
	for _, expected := range []string{"config", "workflow", "prompt"} {
		if !types[expected] {
			t.Errorf("missing entity type %q", expected)
		}
	}
}

func TestVersionGetNestedValue(t *testing.T) {
	m := map[string]any{
		"a": map[string]any{
			"b": map[string]any{
				"c": "deep",
			},
		},
		"top": "level",
	}

	if v := versionGetNestedValue(m, "top"); v != "level" {
		t.Errorf("got %v, want 'level'", v)
	}
	if v := versionGetNestedValue(m, "a.b.c"); v != "deep" {
		t.Errorf("got %v, want 'deep'", v)
	}
	if v := versionGetNestedValue(m, "missing"); v != nil {
		t.Errorf("got %v, want nil", v)
	}
	if v := versionGetNestedValue(m, "a.missing"); v != nil {
		t.Errorf("got %v, want nil", v)
	}
}

func TestNewVersionID(t *testing.T) {
	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := newVersionID()
		if !strings.HasPrefix(id, "v-") {
			t.Errorf("version ID should start with 'v-': %q", id)
		}
		if ids[id] {
			t.Errorf("duplicate version ID: %q", id)
		}
		ids[id] = true
	}
}

func TestSnapshotEntityEmptyDB(t *testing.T) {
	// Empty dbPath should be a no-op when using wrapper functions.
	if err := snapshotConfig("", "/nonexistent/config.json", "test", ""); err != nil {
		t.Errorf("expected nil error for empty dbPath, got %v", err)
	}
	if err := snapshotWorkflow("", "wf", "{}", "test", ""); err != nil {
		t.Errorf("expected nil error for empty dbPath, got %v", err)
	}
	if err := snapshotPrompt("", "prompt", "hello", "test", ""); err != nil {
		t.Errorf("expected nil error for empty dbPath, got %v", err)
	}
}

func TestFlattenDiffNestedAdd(t *testing.T) {
	old := map[string]any{"a": map[string]any{"x": 1}}
	new := map[string]any{"a": map[string]any{"x": 1, "y": 2}}

	var added, removed, changed []string
	flattenDiff("", old, new, &added, &removed, &changed)

	if len(added) != 1 || added[0] != "a.y" {
		t.Errorf("expected added=['a.y'], got %v", added)
	}
	if len(removed) != 0 {
		t.Errorf("expected no removals, got %v", removed)
	}
	if len(changed) != 0 {
		t.Errorf("expected no changes, got %v", changed)
	}
}

func TestFlattenDiffArrayChange(t *testing.T) {
	old := map[string]any{"tags": []any{"a", "b"}}
	new := map[string]any{"tags": []any{"a", "c"}}

	var added, removed, changed []string
	flattenDiff("", old, new, &added, &removed, &changed)

	if len(changed) != 1 || changed[0] != "tags" {
		t.Errorf("expected changed=['tags'], got %v", changed)
	}
}

func TestMultipleEntitiesIsolation(t *testing.T) {
	dbPath := setupVersionTestDB(t)

	snapshotEntity(dbPath, "config", "config.json", `{"a":1}`, "test", "")
	snapshotEntity(dbPath, "workflow", "deploy", `{"name":"deploy"}`, "test", "")

	configVersions, _ := queryVersions(dbPath, "config", "config.json", 10)
	workflowVersions, _ := queryVersions(dbPath, "workflow", "deploy", 10)

	if len(configVersions) != 1 {
		t.Errorf("expected 1 config version, got %d", len(configVersions))
	}
	if len(workflowVersions) != 1 {
		t.Errorf("expected 1 workflow version, got %d", len(workflowVersions))
	}
}

func TestVersionDiffDetailNotFound(t *testing.T) {
	dbPath := setupVersionTestDB(t)

	_, err := versionDiffDetail(dbPath, "v-abc", "v-def")
	if err == nil {
		t.Error("expected error for nonexistent versions")
	}
}

func TestRestoreWorkflowVersion(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	initVersionDB(dbPath)

	cfg := &Config{
		baseDir:   dir,
		HistoryDB: dbPath,
	}

	// Create workflow dir.
	os.MkdirAll(filepath.Join(dir, "workflows"), 0o755)

	// Write initial workflow.
	wf1 := &Workflow{Name: "test-wf", Steps: []WorkflowStep{{ID: "s1", Prompt: "v1"}}}
	saveWorkflow(cfg, wf1)

	// Get v1 ID.
	versions, _ := queryVersions(dbPath, "workflow", "test-wf", 10)
	if len(versions) == 0 {
		t.Fatal("no workflow versions")
	}
	v1ID := versions[0].VersionID

	// Update workflow.
	wf2 := &Workflow{Name: "test-wf", Steps: []WorkflowStep{{ID: "s1", Prompt: "v2"}, {ID: "s2", Prompt: "new"}}}
	saveWorkflow(cfg, wf2)

	// Restore to v1.
	if err := restoreWorkflowVersion(dbPath, cfg, v1ID); err != nil {
		t.Fatalf("restoreWorkflowVersion: %v", err)
	}

	// Verify restored content.
	restored, err := loadWorkflowByName(cfg, "test-wf")
	if err != nil {
		t.Fatalf("loadWorkflowByName: %v", err)
	}
	if len(restored.Steps) != 1 {
		t.Errorf("expected 1 step after restore, got %d", len(restored.Steps))
	}
	if restored.Steps[0].Prompt != "v1" {
		t.Errorf("prompt: got %q, want %q", restored.Steps[0].Prompt, "v1")
	}
}

func TestComputeJSONDiffManyFields(t *testing.T) {
	// Test with > 5 changes to trigger count mode.
	old := `{"a":1,"b":2,"c":3,"d":4,"e":5,"f":6}`
	new := `{"a":10,"b":20,"c":30,"d":40,"e":50,"f":60}`

	diff := computeJSONDiff(old, new)
	if !strings.Contains(diff, "6 fields changed") {
		t.Errorf("expected count summary for many changes: %q", diff)
	}
}

func TestComputeJSONDiffInvalidJSON(t *testing.T) {
	diff := computeJSONDiff("not json", `{"a":1}`)
	if !strings.Contains(diff, "not valid JSON") {
		t.Errorf("expected invalid JSON message: %q", diff)
	}
}

func TestHandleConfigVersionSubcommands(t *testing.T) {
	// Just test that unknown actions return false.
	if handleConfigVersionSubcommands("unknown-action", nil) {
		t.Error("unknown action should return false")
	}
}

func TestHandleWorkflowVersionSubcommands(t *testing.T) {
	if handleWorkflowVersionSubcommands("unknown-action", nil) {
		t.Error("unknown action should return false")
	}
}

func TestSnapshotConfigEmptyDB(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	os.WriteFile(configPath, []byte(`{}`), 0o644)

	// Empty DB path should be no-op.
	err := snapshotConfig("", configPath, "test", "")
	if err != nil {
		t.Errorf("expected nil for empty dbPath, got %v", err)
	}
}

func TestSnapshotWorkflowEmptyDB(t *testing.T) {
	err := snapshotWorkflow("", "wf", "{}", "test", "")
	if err != nil {
		t.Errorf("expected nil for empty dbPath, got %v", err)
	}
}

func TestVersionIDFormat(t *testing.T) {
	id := newVersionID()
	// Should be "v-" + 8 hex chars.
	if !strings.HasPrefix(id, "v-") {
		t.Errorf("ID should start with 'v-': %q", id)
	}
	if len(id) != 10 { // "v-" + 8 hex chars
		t.Errorf("ID length should be 10, got %d: %q", len(id), id)
	}
}

func TestQueryVersionsLimit(t *testing.T) {
	dbPath := setupVersionTestDB(t)

	// Create 5 versions.
	for i := 0; i < 5; i++ {
		snapshotEntity(dbPath, "config", "config.json",
			`{"v":`+json.Number(strings.Repeat("1", i+1)).String()+`}`, "test", "")
	}

	// Query with limit 2.
	versions, err := queryVersions(dbPath, "config", "config.json", 2)
	if err != nil {
		t.Fatalf("queryVersions: %v", err)
	}
	if len(versions) != 2 {
		t.Errorf("expected 2 versions with limit, got %d", len(versions))
	}
}
