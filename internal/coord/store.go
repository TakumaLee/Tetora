package coord

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// WriteClaim writes a claim record atomically to claims/{task_id}__{agent}.json.
func WriteClaim(dir string, c Claim) error {
	name := fmt.Sprintf("%s__%s.json", c.TaskID, c.Agent)
	return atomicWrite(filepath.Join(dir, "claims", name), c)
}

// ReadActiveClaims scans claims/ and returns all claims with status "active"
// that have not expired.
func ReadActiveClaims(dir string) ([]Claim, error) {
	pattern := filepath.Join(dir, "claims", "*.json")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	var active []Claim
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var c Claim
		if err := json.Unmarshal(data, &c); err != nil {
			continue
		}
		if c.Status == "active" && c.ExpiresAt.After(now) {
			active = append(active, c)
		}
	}
	return active, nil
}

// ReleaseClaim updates an existing claim's status to "released".
func ReleaseClaim(dir, taskID, agent string) error {
	name := fmt.Sprintf("%s__%s.json", taskID, agent)
	path := filepath.Join(dir, "claims", name)
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var c Claim
	if err := json.Unmarshal(data, &c); err != nil {
		return err
	}
	c.Status = "released"
	return atomicWrite(path, c)
}

// WriteFinding writes a finding record atomically.
func WriteFinding(dir string, f Finding) error {
	ts := f.RecordedAt.Format("20060102T150405")
	name := fmt.Sprintf("%s__%s__%s.json", f.TaskID, f.Agent, ts)
	return atomicWrite(filepath.Join(dir, "findings", name), f)
}

// WriteBlocker writes a blocker record atomically.
func WriteBlocker(dir string, b Blocker) error {
	ts := b.BlockedAt.Format("20060102T150405")
	name := fmt.Sprintf("%s__%s__%s.json", b.TaskID, b.Agent, ts)
	return atomicWrite(filepath.Join(dir, "blockers", name), b)
}

// HasPendingBlocker returns true if there is already an unresolved blocker for
// the given taskID (i.e. Resolution is empty). Used to avoid writing duplicate
// blocker files when the same region conflict is detected on repeated scans.
func HasPendingBlocker(dir, taskID string) bool {
	pattern := filepath.Join(dir, "blockers", taskID+"__*.json")
	files, _ := filepath.Glob(pattern)
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var b Blocker
		if err := json.Unmarshal(data, &b); err != nil {
			continue
		}
		if b.Resolution == "" {
			return true
		}
	}
	return false
}

// ResolveBlockersFor resolves all unresolved blockers whose depends_on_task
// matches the given taskID.
func ResolveBlockersFor(dir, taskID, agent, resolution string) error {
	pattern := filepath.Join(dir, "blockers", "*.json")
	files, _ := filepath.Glob(pattern)
	now := time.Now().UTC()
	var writeErr error
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var b Blocker
		if err := json.Unmarshal(data, &b); err != nil {
			continue
		}
		if b.DependsOnTask == taskID && b.Resolution == "" {
			b.Resolution = resolution
			b.ResolvedAt = &now
			b.ResolvedBy = agent
			if err := atomicWrite(f, b); err != nil && writeErr == nil {
				writeErr = err
			}
		}
	}
	return writeErr
}

// CheckConflict returns the first active claim by a *different* agent whose
// regions overlap with the given regions. Same-agent claims are always skipped
// because parallel execution in isolated worktrees is safe within one agent.
// Returns nil if no conflict.
func CheckConflict(activeClaims []Claim, agent string, regions []string) *Claim {
	for i := range activeClaims {
		if activeClaims[i].Agent == agent {
			continue // same agent: worktree isolation prevents real conflicts
		}
		if regionsOverlap(activeClaims[i].Regions, regions) {
			return &activeClaims[i]
		}
	}
	return nil
}

// regionsOverlap returns true if any region in a is a prefix of (or equal to)
// any region in b, or vice versa. This handles directory containment.
func regionsOverlap(a, b []string) bool {
	for _, ra := range a {
		ra = filepath.Clean(ra)
		for _, rb := range b {
			rb = filepath.Clean(rb)
			if ra == rb {
				return true
			}
			if strings.HasPrefix(ra, rb+string(filepath.Separator)) ||
				strings.HasPrefix(rb, ra+string(filepath.Separator)) {
				return true
			}
		}
	}
	return false
}

// atomicWrite marshals v to JSON and writes it atomically via tmp+rename.
func atomicWrite(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
