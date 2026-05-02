package skill

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSkillMD(t *testing.T, dir, content string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", dir, err)
	}
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	return path
}

func TestDiagParseSkillMD_TopLevelVersionMaintainer(t *testing.T) {
	dir := t.TempDir()
	path := writeSkillMD(t, dir, `---
name: my-skill
description: A test skill
version: "1.2"
maintainer: kokuyou
---
Body here.
`)
	s, err := diagParseSkillMD(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Version != "1.2" {
		t.Errorf("Version = %q, want %q", s.Version, "1.2")
	}
	if s.Maintainer != "kokuyou" {
		t.Errorf("Maintainer = %q, want %q", s.Maintainer, "kokuyou")
	}
}

func TestDiagParseSkillMD_MetadataSubKey(t *testing.T) {
	dir := t.TempDir()
	path := writeSkillMD(t, dir, `---
name: my-skill
description: A test skill
metadata:
  version: "2.0"
  maintainer: hisui
---
Body here.
`)
	s, err := diagParseSkillMD(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Version != "2.0" {
		t.Errorf("Version = %q, want %q", s.Version, "2.0")
	}
	if s.Maintainer != "hisui" {
		t.Errorf("Maintainer = %q, want %q", s.Maintainer, "hisui")
	}
}

func TestDiagParseSkillMD_RequiresInline(t *testing.T) {
	dir := t.TempDir()
	path := writeSkillMD(t, dir, `---
name: my-skill
description: A test skill
requires: [Python 3.11+, PinchTab, requests]
---
Body.
`)
	s, err := diagParseSkillMD(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(s.Requires) != 3 {
		t.Errorf("Requires len = %d, want 3: %v", len(s.Requires), s.Requires)
	}
}

func TestDiagParseSkillMD_CompatibilityAlias(t *testing.T) {
	dir := t.TempDir()
	path := writeSkillMD(t, dir, `---
name: my-skill
description: A test skill
compatibility: [Python 3.11+, PinchTab]
---
Body.
`)
	s, err := diagParseSkillMD(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(s.Requires) != 2 {
		t.Errorf("Requires len = %d, want 2: %v", len(s.Requires), s.Requires)
	}
}

func TestDiagParseSkillMD_RequiresAndCompatibilityMerge(t *testing.T) {
	dir := t.TempDir()
	path := writeSkillMD(t, dir, `---
name: my-skill
description: A test skill
requires: [Python 3.11+, PinchTab]
compatibility: [Python 3.11+, requests]
---
Body.
`)
	s, err := diagParseSkillMD(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Python 3.11+ appears in both — should be deduplicated
	if len(s.Requires) != 3 {
		t.Errorf("Requires len = %d, want 3 (deduplicated): %v", len(s.Requires), s.Requires)
	}
}

func TestDiagParseSkillMD_RequiresMultiLine(t *testing.T) {
	dir := t.TempDir()
	path := writeSkillMD(t, dir, `---
name: my-skill
description: A test skill
requires:
  - Python 3.11+
  - PinchTab
  - requests
---
Body.
`)
	s, err := diagParseSkillMD(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(s.Requires) != 3 {
		t.Errorf("Requires len = %d, want 3: %v", len(s.Requires), s.Requires)
	}
}

func TestDiagParseSkillMD_MetadataSubKeyAfterOtherKey(t *testing.T) {
	dir := t.TempDir()
	// metadata: block followed by another top-level key
	path := writeSkillMD(t, dir, `---
name: my-skill
description: A test skill
metadata:
  version: "3.0"
  maintainer: ruri
triggers: [foo, bar]
---
Body.
`)
	s, err := diagParseSkillMD(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Version != "3.0" {
		t.Errorf("Version = %q, want %q", s.Version, "3.0")
	}
	if s.Maintainer != "ruri" {
		t.Errorf("Maintainer = %q, want %q", s.Maintainer, "ruri")
	}
	if len(s.Triggers) != 2 {
		t.Errorf("Triggers len = %d, want 2: %v", len(s.Triggers), s.Triggers)
	}
}

func TestDiagAppendUnique(t *testing.T) {
	s := diagAppendUnique(nil, "a")
	s = diagAppendUnique(s, "b")
	s = diagAppendUnique(s, "a") // duplicate
	if len(s) != 2 {
		t.Errorf("len = %d, want 2: %v", len(s), s)
	}
}
