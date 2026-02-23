package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestIsValidSkillName(t *testing.T) {
	tests := []struct {
		name  string
		valid bool
	}{
		{"hello", true},
		{"hello-world", true},
		{"my-skill-123", true},
		{"A", true},
		{"abc123", true},
		{"", false},
		{"-start-with-dash", false},
		{"../traversal", false},
		{"path/slash", false},
		{"path\\backslash", false},
		{"has space", false},
		{"has.dot", false},
		{"has_underscore", false},
		{"hello!", false},
		// Max 64 chars.
		{"abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijkl", true},   // 64 chars
		{"abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklm", false}, // 65 chars
	}

	for _, tt := range tests {
		got := isValidSkillName(tt.name)
		if got != tt.valid {
			t.Errorf("isValidSkillName(%q) = %v, want %v", tt.name, got, tt.valid)
		}
	}
}

func TestCreateSkill(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{
		baseDir: dir,
		SkillStore: SkillStoreConfig{
			AutoApprove: true,
			MaxSkills:   50,
		},
	}

	meta := SkillMetadata{
		Name:        "test-skill",
		Description: "A test skill",
		Command:     "./run.sh",
		Approved:    true,
	}
	script := "#!/bin/bash\necho hello"

	if err := createSkill(cfg, meta, script); err != nil {
		t.Fatalf("createSkill() error: %v", err)
	}

	// Verify files exist.
	metaPath := filepath.Join(dir, "skills", "test-skill", "metadata.json")
	if _, err := os.Stat(metaPath); err != nil {
		t.Fatalf("metadata.json not created: %v", err)
	}
	scriptPath := filepath.Join(dir, "skills", "test-skill", "run.sh")
	if _, err := os.Stat(scriptPath); err != nil {
		t.Fatalf("run.sh not created: %v", err)
	}

	// Verify script content.
	data, _ := os.ReadFile(scriptPath)
	if string(data) != script {
		t.Errorf("script content = %q, want %q", string(data), script)
	}

	// Verify metadata.
	mData, _ := os.ReadFile(metaPath)
	var loaded SkillMetadata
	json.Unmarshal(mData, &loaded)
	if loaded.Name != "test-skill" {
		t.Errorf("metadata name = %q, want %q", loaded.Name, "test-skill")
	}
	if !loaded.Approved {
		t.Error("metadata approved = false, want true")
	}
}

func TestCreateSkillDuplicate(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{
		baseDir: dir,
		SkillStore: SkillStoreConfig{
			AutoApprove: true,
		},
	}

	meta := SkillMetadata{
		Name:        "dup-skill",
		Description: "First",
		Command:     "./run.sh",
		Approved:    true,
	}
	if err := createSkill(cfg, meta, "echo first"); err != nil {
		t.Fatalf("first createSkill() error: %v", err)
	}

	// Second creation should fail.
	meta.Description = "Second"
	if err := createSkill(cfg, meta, "echo second"); err == nil {
		t.Fatal("expected error for duplicate skill, got nil")
	}
}

func TestCreateSkillInvalidName(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{
		baseDir: dir,
	}

	meta := SkillMetadata{
		Name:    "../evil",
		Command: "./run.sh",
	}
	if err := createSkill(cfg, meta, "echo evil"); err == nil {
		t.Fatal("expected error for invalid name, got nil")
	}
}

func TestMaxSkillsLimit(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{
		baseDir: dir,
		SkillStore: SkillStoreConfig{
			AutoApprove: true,
			MaxSkills:   2,
		},
	}

	// Create 2 skills (at limit).
	for i := 0; i < 2; i++ {
		meta := SkillMetadata{
			Name:     fmt.Sprintf("skill-%d", i),
			Command:  "./run.sh",
			Approved: true,
		}
		if err := createSkill(cfg, meta, "echo ok"); err != nil {
			t.Fatalf("createSkill(%d) error: %v", i, err)
		}
	}

	// Third should fail.
	meta := SkillMetadata{
		Name:     "skill-overflow",
		Command:  "./run.sh",
		Approved: true,
	}
	if err := createSkill(cfg, meta, "echo overflow"); err == nil {
		t.Fatal("expected max skills limit error, got nil")
	}
}

func TestLoadFileSkills(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{
		baseDir: dir,
		SkillStore: SkillStoreConfig{
			AutoApprove: true,
		},
	}

	// Create an approved skill.
	meta1 := SkillMetadata{
		Name:        "approved-skill",
		Description: "An approved skill",
		Command:     "./run.sh",
		Approved:    true,
	}
	createSkill(cfg, meta1, "echo approved")

	// Create an unapproved skill.
	meta2 := SkillMetadata{
		Name:        "pending-skill",
		Description: "A pending skill",
		Command:     "./run.sh",
		Approved:    false,
	}
	cfg.SkillStore.MaxSkills = 50 // ensure room
	createSkill(cfg, meta2, "echo pending")

	skills := loadFileSkills(cfg)
	if len(skills) != 1 {
		t.Fatalf("loadFileSkills() returned %d skills, want 1 (only approved)", len(skills))
	}
	if skills[0].Name != "approved-skill" {
		t.Errorf("skill name = %q, want %q", skills[0].Name, "approved-skill")
	}
}

func TestMergeSkills(t *testing.T) {
	configSkills := []SkillConfig{
		{Name: "config-skill", Description: "From config"},
		{Name: "shared-name", Description: "Config version"},
	}
	fileSkills := []SkillConfig{
		{Name: "file-skill", Description: "From file"},
		{Name: "shared-name", Description: "File version"},
	}

	merged := mergeSkills(configSkills, fileSkills)
	if len(merged) != 3 {
		t.Fatalf("mergeSkills() returned %d skills, want 3", len(merged))
	}

	// Check that config version wins for shared-name.
	for _, s := range merged {
		if s.Name == "shared-name" {
			if s.Description != "Config version" {
				t.Errorf("shared-name description = %q, want 'Config version'", s.Description)
			}
		}
	}
}

func TestApproveSkill(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{
		baseDir: dir,
		SkillStore: SkillStoreConfig{
			MaxSkills: 50,
		},
	}

	// Create an unapproved skill.
	meta := SkillMetadata{
		Name:     "pending",
		Command:  "./run.sh",
		Approved: false,
	}
	createSkill(cfg, meta, "echo pending")

	// Verify it's not in loadFileSkills.
	skills := loadFileSkills(cfg)
	if len(skills) != 0 {
		t.Fatalf("expected 0 approved skills, got %d", len(skills))
	}

	// Approve it.
	if err := approveSkill(cfg, "pending"); err != nil {
		t.Fatalf("approveSkill() error: %v", err)
	}

	// Now it should be loadable.
	skills = loadFileSkills(cfg)
	if len(skills) != 1 {
		t.Fatalf("expected 1 approved skill, got %d", len(skills))
	}

	// Double-approve should error.
	if err := approveSkill(cfg, "pending"); err == nil {
		t.Fatal("expected error for double approve, got nil")
	}
}

func TestDeleteFileSkill(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{
		baseDir: dir,
		SkillStore: SkillStoreConfig{
			AutoApprove: true,
			MaxSkills:   50,
		},
	}

	meta := SkillMetadata{
		Name:     "to-delete",
		Command:  "./run.sh",
		Approved: true,
	}
	createSkill(cfg, meta, "echo delete me")

	if err := deleteFileSkill(cfg, "to-delete"); err != nil {
		t.Fatalf("deleteFileSkill() error: %v", err)
	}

	// Directory should be gone.
	if _, err := os.Stat(filepath.Join(dir, "skills", "to-delete")); !os.IsNotExist(err) {
		t.Fatal("skill directory still exists after deletion")
	}

	// Deleting again should fail.
	if err := deleteFileSkill(cfg, "to-delete"); err == nil {
		t.Fatal("expected error for deleting nonexistent skill, got nil")
	}
}

func TestListPendingSkills(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{
		baseDir: dir,
		SkillStore: SkillStoreConfig{
			MaxSkills: 50,
		},
	}

	// Create one approved and one pending.
	createSkill(cfg, SkillMetadata{Name: "approved", Command: "./run.sh", Approved: true}, "echo ok")
	createSkill(cfg, SkillMetadata{Name: "pending", Command: "./run.sh", Approved: false}, "echo pending")

	pending := listPendingSkills(cfg)
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending skill, got %d", len(pending))
	}
	if pending[0].Name != "pending" {
		t.Errorf("pending skill name = %q, want 'pending'", pending[0].Name)
	}
}

func TestCreateSkillToolHandler(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{
		baseDir: dir,
		SkillStore: SkillStoreConfig{
			AutoApprove: true,
			MaxSkills:   50,
		},
	}

	input := `{
		"name": "greet",
		"description": "Greets the user",
		"script": "#!/bin/bash\necho Hello $1",
		"language": "bash",
		"matcher": {"keywords": ["greet", "hello"]}
	}`

	ctx := context.Background()
	result, err := createSkillToolHandler(ctx, cfg, json.RawMessage(input))
	if err != nil {
		t.Fatalf("createSkillToolHandler() error: %v", err)
	}

	var res map[string]any
	json.Unmarshal([]byte(result), &res)
	if res["name"] != "greet" {
		t.Errorf("result name = %v, want 'greet'", res["name"])
	}
	if status, ok := res["status"].(string); !ok || status != "created (auto-approved)" {
		t.Errorf("result status = %v, want 'created (auto-approved)'", res["status"])
	}

	// Verify the skill is loadable.
	skills := loadFileSkills(cfg)
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill after creation, got %d", len(skills))
	}
}

func TestCreateSkillToolHandlerPython(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{
		baseDir: dir,
		SkillStore: SkillStoreConfig{
			AutoApprove: true,
			MaxSkills:   50,
		},
	}

	input := `{
		"name": "py-skill",
		"description": "A python skill",
		"script": "print('hello')",
		"language": "python"
	}`

	ctx := context.Background()
	result, err := createSkillToolHandler(ctx, cfg, json.RawMessage(input))
	if err != nil {
		t.Fatalf("createSkillToolHandler() error: %v", err)
	}

	var res map[string]any
	json.Unmarshal([]byte(result), &res)
	if res["name"] != "py-skill" {
		t.Errorf("result name = %v, want 'py-skill'", res["name"])
	}

	// Verify python script file exists.
	scriptPath := filepath.Join(dir, "skills", "py-skill", "run.py")
	if _, err := os.Stat(scriptPath); err != nil {
		t.Fatalf("run.py not created: %v", err)
	}

	// Verify metadata command.
	metaPath := filepath.Join(dir, "skills", "py-skill", "metadata.json")
	data, _ := os.ReadFile(metaPath)
	var meta SkillMetadata
	json.Unmarshal(data, &meta)
	if meta.Command != "python3" {
		t.Errorf("command = %q, want 'python3'", meta.Command)
	}
	if len(meta.Args) != 1 || meta.Args[0] != "run.py" {
		t.Errorf("args = %v, want ['run.py']", meta.Args)
	}
}

func TestRecordSkillUsage(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{
		baseDir: dir,
		SkillStore: SkillStoreConfig{
			AutoApprove: true,
			MaxSkills:   50,
		},
	}

	// Create a skill and record usage.
	meta := SkillMetadata{
		Name:     "usage-test",
		Command:  "./run.sh",
		Approved: true,
	}
	createSkill(cfg, meta, "echo test")

	recordSkillUsage(cfg, "usage-test")

	// Read back metadata.
	metaPath := filepath.Join(dir, "skills", "usage-test", "metadata.json")
	data, _ := os.ReadFile(metaPath)
	var loaded SkillMetadata
	json.Unmarshal(data, &loaded)

	if loaded.UsageCount != 1 {
		t.Errorf("usageCount = %d, want 1", loaded.UsageCount)
	}
	if loaded.LastUsedAt == "" {
		t.Error("lastUsedAt should be set")
	}
}

// fmt is used in TestMaxSkillsLimit
func init() {
	// Ensure structured logger is initialized for tests.
	if defaultLogger == nil {
		defaultLogger = initLogger(LoggingConfig{Level: "error"}, "")
	}
}
