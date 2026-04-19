package rule

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "INDEX.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write tmp: %v", err)
	}
	return path
}

const sampleIndex = `# Rules Index

> 收到任務時…

| 關鍵字 | 規則檔 | 何時載入 |
|--------|--------|---------|
| 發文, X, Medium | ` + "`rules/social-media.md`" + ` | 要發文時 |
| 回覆, 格式 | ` + "`rules/reply-format.md`" + ` | 每次回覆前（常駐意識） |
| 協作, 寶石團 | ` + "`team/TEAM-RULEBOOK.md`" + ` | 多角色協作時 |
| compact, summary | ` + "`rules/session-compact-summary.md`" + ` | system prompt 含 Summary 時（常駐） |

## 使用方式
`

func TestParseIndex_Basic(t *testing.T) {
	path := writeTempFile(t, sampleIndex)
	entries, err := ParseIndex(path)
	if err != nil {
		t.Fatalf("ParseIndex: %v", err)
	}
	// The `team/TEAM-RULEBOOK.md` row should be dropped (not under rules/).
	if got, want := len(entries), 3; got != want {
		t.Fatalf("entries len = %d, want %d; got=%+v", got, want, entries)
	}

	byPath := map[string]Entry{}
	for _, e := range entries {
		byPath[e.Path] = e
	}

	sm, ok := byPath["social-media.md"]
	if !ok || sm.Always || len(sm.Keywords) != 3 {
		t.Errorf("social-media entry wrong: %+v", sm)
	}
	rf, ok := byPath["reply-format.md"]
	if !ok || !rf.Always {
		t.Errorf("reply-format should be always: %+v", rf)
	}
	cs, ok := byPath["session-compact-summary.md"]
	if !ok || !cs.Always {
		t.Errorf("session-compact-summary should be always (常駐 substring): %+v", cs)
	}
}

func TestParseIndex_MissingFile(t *testing.T) {
	_, err := ParseIndex(filepath.Join(t.TempDir(), "nope.md"))
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestParseIndex_NoTable(t *testing.T) {
	path := writeTempFile(t, "# heading only\n\nno table here\n")
	_, err := ParseIndex(path)
	if err == nil {
		t.Error("expected error for file with no parseable entries")
	}
}

func TestParseIndex_KeywordsLowercased(t *testing.T) {
	path := writeTempFile(t, sampleIndex)
	entries, _ := ParseIndex(path)
	for _, e := range entries {
		for _, kw := range e.Keywords {
			if kw == "" {
				t.Errorf("empty keyword in %s", e.Path)
			}
		}
	}
	// "X" keyword must be lowercased to "x".
	for _, e := range entries {
		if e.Path == "social-media.md" {
			found := false
			for _, kw := range e.Keywords {
				if kw == "x" {
					found = true
				}
				if kw == "X" {
					t.Errorf("keyword not lowercased: %q", kw)
				}
			}
			if !found {
				t.Error("expected lowercased 'x' keyword in social-media entry")
			}
		}
	}
}
