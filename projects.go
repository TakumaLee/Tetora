package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// --- Project Types ---

type Project struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Workdir     string `json:"workdir"`
	Tags        string `json:"tags"`
	RepoURL     string `json:"repoUrl"`
	Category    string `json:"category"`
	Priority    int    `json:"priority"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}

// --- DB Init ---

const projectsTableSQL = `
CREATE TABLE IF NOT EXISTS projects (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  description TEXT DEFAULT '',
  status TEXT DEFAULT 'active',
  workdir TEXT DEFAULT '',
  tags TEXT DEFAULT '',
  repo_url TEXT DEFAULT '',
  category TEXT DEFAULT '',
  priority INTEGER DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_projects_status ON projects(status);
`

func initProjectsDB(dbPath string) error {
	if dbPath == "" {
		return fmt.Errorf("dbPath is empty")
	}
	if err := execDB(dbPath, projectsTableSQL); err != nil {
		return fmt.Errorf("init projects db: %w", err)
	}
	migrateProjectsDB(dbPath)
	return nil
}

func migrateProjectsDB(dbPath string) {
	// Each column is added separately; ignore errors (column may already exist).
	cols := []string{
		"ALTER TABLE projects ADD COLUMN repo_url TEXT DEFAULT ''",
		"ALTER TABLE projects ADD COLUMN category TEXT DEFAULT ''",
		"ALTER TABLE projects ADD COLUMN priority INTEGER DEFAULT 0",
	}
	for _, ddl := range cols {
		execDB(dbPath, ddl)
	}
}

// --- CRUD ---

func listProjects(dbPath, status string) ([]Project, error) {
	where := ""
	if status != "" {
		where = fmt.Sprintf("WHERE status = '%s'", escapeSQLite(status))
	}
	sql := fmt.Sprintf(
		`SELECT id, name, description, status, workdir, tags, repo_url, category, priority, created_at, updated_at
		 FROM projects %s ORDER BY priority DESC, name ASC`,
		where,
	)
	rows, err := queryDB(dbPath, sql)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return []Project{}, nil
		}
		return nil, err
	}
	projects := make([]Project, 0, len(rows))
	for _, row := range rows {
		projects = append(projects, rowToProject(row))
	}
	return projects, nil
}

func getProject(dbPath, id string) (*Project, error) {
	sql := fmt.Sprintf(
		`SELECT id, name, description, status, workdir, tags, repo_url, category, priority, created_at, updated_at
		 FROM projects WHERE id = '%s' LIMIT 1`,
		escapeSQLite(id),
	)
	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	p := rowToProject(rows[0])
	return &p, nil
}

func createProject(dbPath string, p Project) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if p.CreatedAt == "" {
		p.CreatedAt = now
	}
	if p.UpdatedAt == "" {
		p.UpdatedAt = now
	}
	if p.Status == "" {
		p.Status = "active"
	}
	sql := fmt.Sprintf(
		`INSERT INTO projects (id, name, description, status, workdir, tags, repo_url, category, priority, created_at, updated_at)
		 VALUES ('%s','%s','%s','%s','%s','%s','%s','%s',%d,'%s','%s')`,
		escapeSQLite(p.ID),
		escapeSQLite(p.Name),
		escapeSQLite(p.Description),
		escapeSQLite(p.Status),
		escapeSQLite(p.Workdir),
		escapeSQLite(p.Tags),
		escapeSQLite(p.RepoURL),
		escapeSQLite(p.Category),
		p.Priority,
		escapeSQLite(p.CreatedAt),
		escapeSQLite(p.UpdatedAt),
	)
	if _, err := queryDB(dbPath, sql); err != nil {
		return fmt.Errorf("create project: %w", err)
	}
	return nil
}

func updateProject(dbPath string, p Project) error {
	p.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	sql := fmt.Sprintf(
		`UPDATE projects SET name='%s', description='%s', status='%s', workdir='%s', tags='%s', repo_url='%s', category='%s', priority=%d, updated_at='%s'
		 WHERE id='%s'`,
		escapeSQLite(p.Name),
		escapeSQLite(p.Description),
		escapeSQLite(p.Status),
		escapeSQLite(p.Workdir),
		escapeSQLite(p.Tags),
		escapeSQLite(p.RepoURL),
		escapeSQLite(p.Category),
		p.Priority,
		escapeSQLite(p.UpdatedAt),
		escapeSQLite(p.ID),
	)
	if _, err := queryDB(dbPath, sql); err != nil {
		return fmt.Errorf("update project: %w", err)
	}
	return nil
}

func deleteProject(dbPath, id string) error {
	sql := fmt.Sprintf(
		`DELETE FROM projects WHERE id = '%s'`,
		escapeSQLite(id),
	)
	if _, err := queryDB(dbPath, sql); err != nil {
		return fmt.Errorf("delete project: %w", err)
	}
	return nil
}

// WorkspaceProjectEntry represents a project parsed from PROJECTS.md.
type WorkspaceProjectEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Workdir     string `json:"workdir"`
}

// parseProjectsMD reads a PROJECTS.md file and extracts project entries.
// Format expected: ## Category headers, then "- **Name**: description" lines.
// Workdir detected from "path" patterns in the description.
func parseProjectsMD(path string) ([]WorkspaceProjectEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")
	var entries []WorkspaceProjectEntry
	category := ""
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Detect category headers: ## emoji Category
		if strings.HasPrefix(line, "## ") {
			cat := strings.TrimPrefix(line, "## ")
			// Strip leading emoji (any non-ASCII prefix).
			for i, r := range cat {
				if r < 128 && r != ' ' {
					cat = strings.TrimSpace(cat[i:])
					break
				}
			}
			category = cat
			continue
		}
		// Detect project lines: - **Name**: description
		if strings.HasPrefix(line, "- **") {
			end := strings.Index(line[4:], "**")
			if end < 0 {
				continue
			}
			name := line[4 : 4+end]
			rest := ""
			if len(line) > 4+end+2 {
				rest = strings.TrimSpace(line[4+end+2:])
				rest = strings.TrimPrefix(rest, "\uff1a")
				rest = strings.TrimPrefix(rest, ":")
				rest = strings.TrimSpace(rest)
			}
			entry := WorkspaceProjectEntry{
				Name:        name,
				Description: rest,
				Category:    category,
			}
			// Extract workdir from "path" pattern.
			if idx := strings.Index(rest, "\u8def\u5f91\uff1a"); idx >= 0 {
				wp := strings.TrimSpace(rest[idx+len("\u8def\u5f91\uff1a"):])
				entry.Workdir = wp
				entry.Description = strings.TrimSpace(rest[:idx])
			} else if idx := strings.Index(rest, "\u8def\u5f91:"); idx >= 0 {
				wp := strings.TrimSpace(rest[idx+len("\u8def\u5f91:"):])
				entry.Workdir = wp
				entry.Description = strings.TrimSpace(rest[:idx])
			}
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

// rowToProject converts a DB row to a Project struct.
func rowToProject(row map[string]any) Project {
	pri := 0
	if v, ok := row["priority"]; ok {
		switch vv := v.(type) {
		case float64:
			pri = int(vv)
		case int:
			pri = vv
		}
	}
	return Project{
		ID:          jsonStr(row["id"]),
		Name:        jsonStr(row["name"]),
		Description: jsonStr(row["description"]),
		Status:      jsonStr(row["status"]),
		Workdir:     jsonStr(row["workdir"]),
		Tags:        jsonStr(row["tags"]),
		RepoURL:     jsonStr(row["repo_url"]),
		Category:    jsonStr(row["category"]),
		Priority:    pri,
		CreatedAt:   jsonStr(row["created_at"]),
		UpdatedAt:   jsonStr(row["updated_at"]),
	}
}
