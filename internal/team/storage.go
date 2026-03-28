package team

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

// slugRe matches safe team/agent name slugs: lowercase letters, digits, hyphens, underscores.
var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9\-_]*$`)

// validateName returns an error if name contains path traversal sequences or
// characters outside the safe slug character set.
func validateName(name string) error {
	if name == "" {
		return fmt.Errorf("team name cannot be empty")
	}
	if !slugRe.MatchString(name) {
		return fmt.Errorf("team name %q is invalid: use only lowercase letters, digits, hyphens, and underscores", name)
	}
	return nil
}

// Storage manages team definitions on disk under {baseDir}/teams/.
type Storage struct {
	dir string // e.g. ~/.tetora/teams
}

// NewStorage creates a Storage rooted at the given base directory.
// baseDir is the Tetora home (e.g. ~/.tetora); teams are stored under baseDir/teams/.
func NewStorage(baseDir string) *Storage {
	return &Storage{dir: filepath.Join(baseDir, "teams")}
}

// Dir returns the teams directory path.
func (s *Storage) Dir() string { return s.dir }

// ensureDir creates the teams directory if it doesn't exist.
func (s *Storage) ensureDir() error {
	return os.MkdirAll(s.dir, 0o755)
}

// teamPath returns the path to a team's JSON file.
func (s *Storage) teamPath(name string) string {
	return filepath.Join(s.dir, name, "team.json")
}

// Load reads a single team definition by name.
func (s *Storage) Load(name string) (*TeamDef, error) {
	if err := validateName(name); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(s.teamPath(name))
	if err != nil {
		return nil, fmt.Errorf("load team %q: %w", name, err)
	}
	var t TeamDef
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("parse team %q: %w", name, err)
	}
	return &t, nil
}

// List returns all team definitions sorted by name.
func (s *Storage) List() ([]TeamDef, error) {
	if err := s.ensureDir(); err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("list teams: %w", err)
	}

	var teams []TeamDef
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		t, err := s.Load(e.Name())
		if err != nil {
			continue // skip broken entries
		}
		teams = append(teams, *t)
	}

	sort.Slice(teams, func(i, j int) bool {
		return teams[i].Name < teams[j].Name
	})
	return teams, nil
}

// Save writes a team definition to disk. Creates the directory if needed.
func (s *Storage) Save(t TeamDef) error {
	if err := validateName(t.Name); err != nil {
		return err
	}
	if err := s.ensureDir(); err != nil {
		return err
	}
	dir := filepath.Join(s.dir, t.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create team dir: %w", err)
	}

	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal team: %w", err)
	}

	// Atomic write: tmp file then rename.
	tmp := s.teamPath(t.Name) + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write team tmp: %w", err)
	}
	if err := os.Rename(tmp, s.teamPath(t.Name)); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename team file: %w", err)
	}
	return nil
}

// Delete removes a team definition. Builtin teams cannot be deleted.
func (s *Storage) Delete(name string) error {
	if err := validateName(name); err != nil {
		return err
	}
	t, err := s.Load(name)
	if err != nil {
		return err
	}
	if t.Builtin {
		return fmt.Errorf("cannot delete builtin team %q", name)
	}
	return os.RemoveAll(filepath.Join(s.dir, name))
}

// MaterializeBuiltins writes all builtin templates to disk if they don't already exist.
func (s *Storage) MaterializeBuiltins() error {
	if err := s.ensureDir(); err != nil {
		return err
	}
	for _, t := range BuiltinTemplates() {
		path := s.teamPath(t.Name)
		if _, err := os.Stat(path); err == nil {
			continue // already exists
		}
		if err := s.Save(t); err != nil {
			return fmt.Errorf("materialize %q: %w", t.Name, err)
		}
	}
	return nil
}
