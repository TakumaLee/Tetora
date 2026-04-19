package rule

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tetora/internal/config"
)

// setupRulesDir creates a minimal rules/ dir with an INDEX and a few rule
// files. Returns the dir path.
func setupRulesDir(t *testing.T, index string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "INDEX.md"), []byte(index), 0o644); err != nil {
		t.Fatalf("write INDEX: %v", err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

func TestBuildPrompt_AlwaysAndMatched(t *testing.T) {
	index := `| 關鍵字 | 規則檔 | 何時載入 |
|---|---|---|
| medium, 發文 | ` + "`rules/social-media.md`" + ` | 要發文時 |
| 回覆 | ` + "`rules/reply-format.md`" + ` | 常駐意識 |
| hisui | ` + "`rules/hisui.md`" + ` | 市場掃描時 |
`
	files := map[string]string{
		"social-media.md": "# Social Media\nRule content.",
		"reply-format.md": "# Reply Format\nBe concise.",
		"hisui.md":        "# Hisui Market\nNot relevant here.",
	}
	dir := setupRulesDir(t, index, files)

	cfg := &config.Config{WorkspaceDir: filepath.Dir(dir), PromptBudget: config.PromptBudgetConfig{}}
	out := BuildPrompt(cfg, dir, "幫我發一篇 Medium 文")

	if out == "" {
		t.Fatal("expected non-empty output")
	}
	if !strings.Contains(out, "[always] reply-format.md") {
		t.Errorf("missing always-on rule: %s", out)
	}
	if !strings.Contains(out, "social-media.md") || !strings.Contains(out, "matched") {
		t.Errorf("missing matched rule: %s", out)
	}
	if strings.Contains(out, "hisui.md") {
		t.Errorf("unrelated rule should not be injected: %s", out)
	}
	if !strings.Contains(out, "{{rules.FILENAME}}") {
		t.Errorf("missing footer hint: %s", out)
	}
}

func TestBuildPrompt_FallbackOnMissingIndex(t *testing.T) {
	dir := t.TempDir() // no INDEX.md
	cfg := &config.Config{WorkspaceDir: filepath.Dir(dir)}
	if out := BuildPrompt(cfg, dir, "anything"); out != "" {
		t.Errorf("expected empty for missing INDEX, got %q", out)
	}
}

func TestBuildPrompt_BudgetEnforcement(t *testing.T) {
	index := `| 關鍵字 | 規則檔 | 何時載入 |
|---|---|---|
| a | ` + "`rules/a.md`" + ` | 常駐 |
| b | ` + "`rules/b.md`" + ` | 常駐 |
| c | ` + "`rules/c.md`" + ` | 常駐 |
`
	big := strings.Repeat("X", 5000) // each rule is 5000 bytes
	files := map[string]string{
		"a.md": big,
		"b.md": big,
		"c.md": big,
	}
	dir := setupRulesDir(t, index, files)

	cfg := &config.Config{
		WorkspaceDir: filepath.Dir(dir),
		PromptBudget: config.PromptBudgetConfig{RulesMax: 6000},
	}
	out := BuildPrompt(cfg, dir, "")
	// First rule fits (5000 + header ≈ 5020 < 6000). Second pushes over budget.
	if !strings.Contains(out, "a.md") {
		t.Errorf("first rule should fit: %s", out[:min(200, len(out))])
	}
	if strings.Contains(out, "### [always] b.md") || strings.Contains(out, "### [always] c.md") {
		t.Errorf("budget not enforced, got content of b/c: len=%d", len(out))
	}
	if !strings.Contains(out, "Skipped for budget") {
		t.Errorf("expected skip notice: %s", out)
	}
}

func TestBuildPrompt_MaxRulesPerTaskCap(t *testing.T) {
	index := `| 關鍵字 | 規則檔 | 何時載入 |
|---|---|---|
| x | ` + "`rules/x1.md`" + ` | 匹配時 |
| x | ` + "`rules/x2.md`" + ` | 匹配時 |
| x | ` + "`rules/x3.md`" + ` | 匹配時 |
| x | ` + "`rules/x4.md`" + ` | 匹配時 |
`
	files := map[string]string{
		"x1.md": "one",
		"x2.md": "two",
		"x3.md": "three",
		"x4.md": "four",
	}
	dir := setupRulesDir(t, index, files)

	cfg := &config.Config{
		WorkspaceDir: filepath.Dir(dir),
		PromptBudget: config.PromptBudgetConfig{MaxRulesPerTask: 2},
	}
	out := BuildPrompt(cfg, dir, "x matches all")
	// x1 and x2 injected, x3/x4 dropped by per-task cap (not budget).
	if !strings.Contains(out, "x1.md") || !strings.Contains(out, "x2.md") {
		t.Errorf("first two should inject: %s", out)
	}
	if strings.Contains(out, "x3.md") || strings.Contains(out, "x4.md") {
		t.Errorf("MaxRulesPerTask not enforced: %s", out)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
