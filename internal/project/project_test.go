package project

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// tempProjectsDB creates a temp DB with the projects table initialized.
func tempProjectsDB(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not found, skipping")
	}
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_projects.db")
	if err := InitDB(dbPath); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	return dbPath
}

func TestInitProjectsDB(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not found, skipping")
	}
	dbPath := tempProjectsDB(t)
	// Idempotent: calling again should not error.
	if err := InitDB(dbPath); err != nil {
		t.Fatalf("InitDB second call: %v", err)
	}
}

func TestInitProjectsDB_EmptyPath(t *testing.T) {
	if err := InitDB(""); err == nil {
		t.Error("expected error for empty dbPath")
	}
}

func TestProjectCreateAndGet(t *testing.T) {
	dbPath := tempProjectsDB(t)

	p := Project{
		ID:          "proj-001",
		Name:        "Test Project",
		Description: "A test project",
		Status:      "active",
		Workdir:     "/tmp/test",
		Tags:        "go,test",
	}
	if err := Create(dbPath, p); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := Get(dbPath, "proj-001")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.ID != "proj-001" {
		t.Errorf("ID = %q, want %q", got.ID, "proj-001")
	}
	if got.Name != "Test Project" {
		t.Errorf("Name = %q, want %q", got.Name, "Test Project")
	}
	if got.Description != "A test project" {
		t.Errorf("Description = %q, want %q", got.Description, "A test project")
	}
	if got.Status != "active" {
		t.Errorf("Status = %q, want %q", got.Status, "active")
	}
	if got.Workdir != "/tmp/test" {
		t.Errorf("Workdir = %q, want %q", got.Workdir, "/tmp/test")
	}
	if got.Tags != "go,test" {
		t.Errorf("Tags = %q, want %q", got.Tags, "go,test")
	}
	if got.CreatedAt == "" {
		t.Error("CreatedAt should not be empty")
	}
	if got.UpdatedAt == "" {
		t.Error("UpdatedAt should not be empty")
	}
}

func TestProjectGet_NotFound(t *testing.T) {
	dbPath := tempProjectsDB(t)

	got, err := Get(dbPath, "nonexistent-id")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for nonexistent ID, got %+v", got)
	}
}

func TestProjectCreate_DefaultStatus(t *testing.T) {
	dbPath := tempProjectsDB(t)

	p := Project{
		ID:   "proj-defaults",
		Name: "Defaults Project",
	}
	if err := Create(dbPath, p); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := Get(dbPath, "proj-defaults")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.Status != "active" {
		t.Errorf("default Status = %q, want %q", got.Status, "active")
	}
}

func TestProjectList(t *testing.T) {
	dbPath := tempProjectsDB(t)

	projects := []Project{
		{ID: "p1", Name: "Alpha", Status: "active"},
		{ID: "p2", Name: "Beta", Status: "active"},
		{ID: "p3", Name: "Gamma", Status: "archived"},
	}
	for _, p := range projects {
		if err := Create(dbPath, p); err != nil {
			t.Fatalf("Create %s: %v", p.ID, err)
		}
	}

	// List all.
	all, err := List(dbPath, "")
	if err != nil {
		t.Fatalf("List all: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 projects, got %d", len(all))
	}

	// List by status.
	active, err := List(dbPath, "active")
	if err != nil {
		t.Fatalf("List active: %v", err)
	}
	if len(active) != 2 {
		t.Fatalf("expected 2 active projects, got %d", len(active))
	}
	for _, p := range active {
		if p.Status != "active" {
			t.Errorf("expected status active, got %q", p.Status)
		}
	}

	archived, err := List(dbPath, "archived")
	if err != nil {
		t.Fatalf("List archived: %v", err)
	}
	if len(archived) != 1 {
		t.Fatalf("expected 1 archived project, got %d", len(archived))
	}
}

func TestProjectList_Empty(t *testing.T) {
	dbPath := tempProjectsDB(t)

	all, err := List(dbPath, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("expected 0 projects, got %d", len(all))
	}
}

func TestProjectUpdate(t *testing.T) {
	dbPath := tempProjectsDB(t)

	p := Project{
		ID:          "proj-update",
		Name:        "Before Update",
		Description: "Original description",
		Status:      "active",
	}
	if err := Create(dbPath, p); err != nil {
		t.Fatalf("Create: %v", err)
	}

	p.Name = "After Update"
	p.Description = "Updated description"
	p.Status = "archived"
	if err := Update(dbPath, p); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := Get(dbPath, "proj-update")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil after update")
	}
	if got.Name != "After Update" {
		t.Errorf("Name = %q, want %q", got.Name, "After Update")
	}
	if got.Description != "Updated description" {
		t.Errorf("Description = %q, want %q", got.Description, "Updated description")
	}
	if got.Status != "archived" {
		t.Errorf("Status = %q, want %q", got.Status, "archived")
	}
}

func TestProjectDelete(t *testing.T) {
	dbPath := tempProjectsDB(t)

	p := Project{
		ID:   "proj-delete",
		Name: "To Delete",
	}
	if err := Create(dbPath, p); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := Delete(dbPath, "proj-delete"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	got, err := Get(dbPath, "proj-delete")
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil after delete, got %+v", got)
	}
}

func TestProjectCreate_SpecialChars(t *testing.T) {
	dbPath := tempProjectsDB(t)

	p := Project{
		ID:          "proj-special",
		Name:        "It's a project",
		Description: `She said "hello" and it's fine`,
		Status:      "active",
	}
	if err := Create(dbPath, p); err != nil {
		t.Fatalf("Create with special chars: %v", err)
	}

	got, err := Get(dbPath, "proj-special")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.Name != p.Name {
		t.Errorf("Name = %q, want %q", got.Name, p.Name)
	}
	if got.Description != p.Description {
		t.Errorf("Description = %q, want %q", got.Description, p.Description)
	}
}

func TestProjectNewFields(t *testing.T) {
	dbPath := tempProjectsDB(t)

	p := Project{
		ID:       "proj-new-fields",
		Name:     "New Fields Project",
		RepoURL:  "https://github.com/test/repo",
		Category: "AI tools",
		Priority: 10,
		Tags:     "go,ai",
		Workdir:  "/tmp/test-new",
	}
	if err := Create(dbPath, p); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := Get(dbPath, "proj-new-fields")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.RepoURL != "https://github.com/test/repo" {
		t.Errorf("RepoURL = %q, want %q", got.RepoURL, "https://github.com/test/repo")
	}
	if got.Category != "AI tools" {
		t.Errorf("Category = %q, want %q", got.Category, "AI tools")
	}
	if got.Priority != 10 {
		t.Errorf("Priority = %d, want %d", got.Priority, 10)
	}
}

func TestProjectListOrder(t *testing.T) {
	dbPath := tempProjectsDB(t)

	projects := []Project{
		{ID: "p1", Name: "Zebra", Priority: 1},
		{ID: "p2", Name: "Alpha", Priority: 5},
		{ID: "p3", Name: "Beta", Priority: 5},
		{ID: "p4", Name: "Delta", Priority: 0},
	}
	for _, p := range projects {
		if err := Create(dbPath, p); err != nil {
			t.Fatalf("Create %s: %v", p.ID, err)
		}
	}

	all, err := List(dbPath, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("expected 4 projects, got %d", len(all))
	}
	// Expected order: Alpha (5), Beta (5), Zebra (1), Delta (0)
	expected := []string{"Alpha", "Beta", "Zebra", "Delta"}
	for i, name := range expected {
		if all[i].Name != name {
			t.Errorf("position %d: got %q, want %q", i, all[i].Name, name)
		}
	}
}

func TestProjectUpdateNewFields(t *testing.T) {
	dbPath := tempProjectsDB(t)

	p := Project{
		ID:       "proj-upd-new",
		Name:     "Update New Fields",
		RepoURL:  "https://github.com/old/repo",
		Category: "Old Category",
		Priority: 1,
	}
	if err := Create(dbPath, p); err != nil {
		t.Fatalf("Create: %v", err)
	}

	p.RepoURL = "https://github.com/new/repo"
	p.Category = "New Category"
	p.Priority = 99
	if err := Update(dbPath, p); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := Get(dbPath, "proj-upd-new")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.RepoURL != "https://github.com/new/repo" {
		t.Errorf("RepoURL = %q, want %q", got.RepoURL, "https://github.com/new/repo")
	}
	if got.Category != "New Category" {
		t.Errorf("Category = %q, want %q", got.Category, "New Category")
	}
	if got.Priority != 99 {
		t.Errorf("Priority = %d, want %d", got.Priority, 99)
	}
}

func TestProjectList_NoTable(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not found, skipping")
	}
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "empty.db")
	// Don't init — List should return empty slice gracefully.
	all, err := List(dbPath, "")
	if err != nil {
		t.Fatalf("List on missing table: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("expected 0, got %d", len(all))
	}
}
