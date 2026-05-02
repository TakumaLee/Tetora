package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tetora/internal/db"
)

// ---- helpers ----

func mkSkillMD(t *testing.T, skillDir, content string) {
	t.Helper()
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile SKILL.md: %v", err)
	}
}

// ---- LoadSkillBody ----

func TestLoadSkillBody_WithFrontmatter(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "my-skill")
	content := "---\nname: my-skill\ndescription: A test skill\n---\n\nThis is the body.\n"
	writeSkillMD(t, skillDir, content)

	fm, body, err := LoadSkillBody(skillDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(fm, "---") {
		t.Errorf("frontmatter should start with ---; got %q", fm)
	}
	if !strings.Contains(fm, "name: my-skill") {
		t.Errorf("frontmatter should contain name field; got %q", fm)
	}
	if body != "This is the body." {
		t.Errorf("unexpected body: %q", body)
	}
}

func TestLoadSkillBody_WithoutFrontmatter(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "plain-skill")
	content := "Just a plain body.\nNo frontmatter.\n"
	writeSkillMD(t, skillDir, content)

	fm, body, err := LoadSkillBody(skillDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fm != "" {
		t.Errorf("expected empty frontmatter, got %q", fm)
	}
	if body != content {
		t.Errorf("body should match original content; got %q", body)
	}
}

func TestLoadSkillBody_NotFound(t *testing.T) {
	dir := t.TempDir()
	_, _, err := LoadSkillBody(filepath.Join(dir, "nonexistent"))
	if err == nil {
		t.Error("expected error for missing skill dir")
	}
}

// ---- LoadSkillBodyFromString ----

func TestLoadSkillBodyFromString_WithFrontmatter(t *testing.T) {
	content := "---\nname: test\n---\n\nBody here."
	fm, body, err := LoadSkillBodyFromString(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(fm, "---") {
		t.Errorf("expected frontmatter; got %q", fm)
	}
	if body != "Body here." {
		t.Errorf("unexpected body: %q", body)
	}
}

func TestLoadSkillBodyFromString_NoFrontmatter(t *testing.T) {
	content := "Just a plain body."
	fm, body, err := LoadSkillBodyFromString(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fm != "" {
		t.Errorf("expected empty frontmatter, got %q", fm)
	}
	if body != content {
		t.Errorf("body mismatch: got %q", body)
	}
}

// ---- computeUnifiedDiff ----

func TestComputeUnifiedDiff_Identical(t *testing.T) {
	a := "line1\nline2\nline3"
	diff := computeUnifiedDiff(a, a)
	if strings.Contains(diff, "+") || strings.Contains(diff, "-") {
		// Should only have context lines prefixed with "  "
		for _, line := range strings.Split(diff, "\n") {
			if line == "" {
				continue
			}
			if !strings.HasPrefix(line, "  ") {
				t.Errorf("unexpected diff line for identical content: %q", line)
			}
		}
	}
}

func TestComputeUnifiedDiff_Additions(t *testing.T) {
	a := "line1\nline2"
	b := "line1\nline2\nline3"
	diff := computeUnifiedDiff(a, b)
	if !strings.Contains(diff, "+ line3") {
		t.Errorf("expected addition in diff; got:\n%s", diff)
	}
}

func TestComputeUnifiedDiff_Deletions(t *testing.T) {
	a := "line1\nline2\nline3"
	b := "line1\nline3"
	diff := computeUnifiedDiff(a, b)
	if !strings.Contains(diff, "- line2") {
		t.Errorf("expected deletion in diff; got:\n%s", diff)
	}
}

func TestComputeUnifiedDiff_Empty(t *testing.T) {
	diff := computeUnifiedDiff("", "")
	// Empty-to-empty: one empty element each from Split; should produce a "  " line.
	if strings.Contains(diff, "+") || strings.Contains(diff, "-") {
		t.Errorf("unexpected diff for empty-to-empty: %q", diff)
	}
}

// ---- WriteEvolveProposal ----

func TestWriteEvolveProposal(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "test-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	cand := EvolveCandidate{
		Name:         "test-skill",
		SkillDir:     skillDir,
		Frontmatter:  "---\nname: test-skill\n---",
		Body:         "Original body text.",
		FailRate:     0.5,
		InvokedCount: 20,
		FailCount:    10,
		Failures:     "## 2026-01-01 — task (agent: ruri)\nfailed because of X",
	}
	prop := EvolveProposal{
		Diagnosis:    "Root cause: X is broken.",
		ProposedBody: "Fixed body text.",
		KeyChanges:   []string{"fixed X", "added Y"},
		Confidence:   0.8,
	}

	fpath, err := WriteEvolveProposal(cand, prop)
	if err != nil {
		t.Fatalf("WriteEvolveProposal error: %v", err)
	}

	data, err := os.ReadFile(fpath)
	if err != nil {
		t.Fatalf("ReadFile proposal: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "# Proposed Body") {
		t.Error("proposal missing '# Proposed Body' section")
	}
	if !strings.Contains(content, "# Diagnosis") {
		t.Error("proposal missing '# Diagnosis' section")
	}
	if !strings.Contains(content, "Fixed body text.") {
		t.Error("proposal missing proposed body content")
	}
	if !strings.Contains(content, "Root cause: X is broken.") {
		t.Error("proposal missing diagnosis content")
	}
	if !strings.Contains(content, "status: pending") {
		t.Error("proposal should start with status: pending")
	}
	if !strings.Contains(content, "# Key Changes") {
		t.Error("proposal missing '# Key Changes' section")
	}
	// Proposals dir should exist.
	if _, err := os.Stat(filepath.Join(skillDir, "proposals")); err != nil {
		t.Error("proposals directory should exist")
	}
}

// ---- ApproveProposal ----

func TestApproveProposal(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "approve-skill")

	// Write the original SKILL.md.
	originalBody := "Original body."
	skillContent := "---\nname: approve-skill\n---\n\n" + originalBody
	writeSkillMD(t, skillDir, skillContent)

	// Create a proposal.
	cand := EvolveCandidate{
		Name:         "approve-skill",
		SkillDir:     skillDir,
		Frontmatter:  "---\nname: approve-skill\n---",
		Body:         originalBody,
		FailRate:     0.5,
		InvokedCount: 20,
		FailCount:    10,
	}
	prop := EvolveProposal{
		Diagnosis:    "Needs improvement.",
		ProposedBody: "Improved body.",
		KeyChanges:   []string{"improved"},
		Confidence:   0.9,
	}
	fpath, err := WriteEvolveProposal(cand, prop)
	if err != nil {
		t.Fatalf("WriteEvolveProposal: %v", err)
	}

	// Extract proposal ID from filename.
	proposalID := strings.TrimSuffix(filepath.Base(fpath), ".md")

	// Approve the proposal.
	if err := ApproveProposal(skillDir, proposalID); err != nil {
		t.Fatalf("ApproveProposal: %v", err)
	}

	// Check skill file was updated.
	data, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	if err != nil {
		t.Fatalf("ReadFile SKILL.md after approve: %v", err)
	}
	if !strings.Contains(string(data), "Improved body.") {
		t.Errorf("SKILL.md should contain proposed body; got: %s", string(data))
	}

	// Check backup exists in history/.
	histDir := filepath.Join(skillDir, "history")
	entries, err := os.ReadDir(histDir)
	if err != nil {
		t.Fatalf("history dir should exist: %v", err)
	}
	if len(entries) == 0 {
		t.Error("history dir should contain backup file")
	}
	backupData, err := os.ReadFile(filepath.Join(histDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile backup: %v", err)
	}
	if !strings.Contains(string(backupData), "Original body.") {
		t.Error("backup should contain original body")
	}

	// Check proposal status updated to approved.
	proposalData, err := os.ReadFile(fpath)
	if err != nil {
		t.Fatalf("ReadFile proposal: %v", err)
	}
	if !strings.Contains(string(proposalData), "status: approved") {
		t.Error("proposal status should be 'approved'")
	}
}

// ---- ScanEvolveCandidates ----

func setupEvolveDB(t *testing.T, dbPath string) {
	t.Helper()
	if err := InitSkillUsageTable(dbPath); err != nil {
		t.Fatalf("InitSkillUsageTable: %v", err)
	}
}

func insertSkillUsageRow(t *testing.T, dbPath, skillName, eventType, status string) {
	t.Helper()
	sql := "INSERT INTO skill_usage (skill_name, event_type, task_prompt, role, created_at, status, duration_ms, source, session_id, error_msg) " +
		"VALUES ('" + db.Escape(skillName) + "', '" + db.Escape(eventType) + "', '', '', datetime('now'), '" + db.Escape(status) + "', 0, 'test', '', '')"
	if _, err := db.Query(dbPath, sql); err != nil {
		t.Fatalf("INSERT skill_usage: %v", err)
	}
}

func TestScanEvolveCandidates_HighFailRate(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "skill.db")
	setupEvolveDB(t, dbPath)

	// Insert 10 invoked rows: 5 fail, 5 success (50% fail rate >= 40% threshold).
	for i := 0; i < 5; i++ {
		insertSkillUsageRow(t, dbPath, "bad-skill", "invoked", "fail")
		insertSkillUsageRow(t, dbPath, "bad-skill", "invoked", "success")
	}

	// Create a skill directory for bad-skill.
	skillsDir := filepath.Join(dir, "skills")
	skillDir := filepath.Join(skillsDir, "bad-skill")
	writeSkillMD(t, skillDir, "---\nname: bad-skill\n---\n\nBad skill body.")

	cfg := &AppConfig{WorkspaceDir: dir}
	thresholds := DefaultEvolveThresholds()
	thresholds.MinInvocations = 10
	thresholds.FailRateThreshold = 0.40

	candidates, err := ScanEvolveCandidates(cfg, dbPath, thresholds)
	if err != nil {
		t.Fatalf("ScanEvolveCandidates: %v", err)
	}
	if len(candidates) == 0 {
		t.Fatal("expected at least one candidate for bad-skill")
	}
	found := false
	for _, c := range candidates {
		if c.Name == "bad-skill" {
			found = true
			if c.FailRate < 0.40 {
				t.Errorf("expected failRate >= 0.40, got %.2f", c.FailRate)
			}
			if c.InvokedCount < 10 {
				t.Errorf("expected invocations >= 10, got %d", c.InvokedCount)
			}
		}
	}
	if !found {
		t.Error("bad-skill not found in candidates")
	}
}

func TestScanEvolveCandidates_LowFailRate(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "skill.db")
	setupEvolveDB(t, dbPath)

	// 2 fail, 10 success (< 40% fail rate).
	for i := 0; i < 2; i++ {
		insertSkillUsageRow(t, dbPath, "good-skill", "invoked", "fail")
	}
	for i := 0; i < 10; i++ {
		insertSkillUsageRow(t, dbPath, "good-skill", "invoked", "success")
	}

	skillsDir := filepath.Join(dir, "skills")
	skillDir := filepath.Join(skillsDir, "good-skill")
	writeSkillMD(t, skillDir, "---\nname: good-skill\n---\n\nGood skill body.")

	cfg := &AppConfig{WorkspaceDir: dir}
	thresholds := DefaultEvolveThresholds()

	candidates, err := ScanEvolveCandidates(cfg, dbPath, thresholds)
	if err != nil {
		t.Fatalf("ScanEvolveCandidates: %v", err)
	}
	for _, c := range candidates {
		if c.Name == "good-skill" {
			t.Error("good-skill should not appear in candidates (low fail rate)")
		}
	}
}

func TestScanEvolveCandidates_InsufficientInvocations(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "skill.db")
	setupEvolveDB(t, dbPath)

	// Only 5 invocations (below MinInvocations=10) with 100% fail rate.
	for i := 0; i < 5; i++ {
		insertSkillUsageRow(t, dbPath, "rare-skill", "invoked", "fail")
	}

	skillsDir := filepath.Join(dir, "skills")
	skillDir := filepath.Join(skillsDir, "rare-skill")
	writeSkillMD(t, skillDir, "---\nname: rare-skill\n---\n\nRare skill body.")

	cfg := &AppConfig{WorkspaceDir: dir}
	thresholds := DefaultEvolveThresholds()
	thresholds.MinInvocations = 10

	candidates, err := ScanEvolveCandidates(cfg, dbPath, thresholds)
	if err != nil {
		t.Fatalf("ScanEvolveCandidates: %v", err)
	}
	for _, c := range candidates {
		if c.Name == "rare-skill" {
			t.Error("rare-skill should not appear: insufficient invocations")
		}
	}
}

func TestScanEvolveCandidates_CooldownRespected(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "skill.db")
	setupEvolveDB(t, dbPath)

	// High fail rate.
	for i := 0; i < 5; i++ {
		insertSkillUsageRow(t, dbPath, "cooling-skill", "invoked", "fail")
		insertSkillUsageRow(t, dbPath, "cooling-skill", "invoked", "success")
	}

	skillsDir := filepath.Join(dir, "skills")
	skillDir := filepath.Join(skillsDir, "cooling-skill")
	writeSkillMD(t, skillDir, "---\nname: cooling-skill\n---\n\nCooling skill body.")

	// Write a recent .evolved-at (within cooldown period).
	if err := MarkSkillEvolved(skillDir); err != nil {
		t.Fatalf("MarkSkillEvolved: %v", err)
	}

	cfg := &AppConfig{WorkspaceDir: dir}
	thresholds := DefaultEvolveThresholds()
	thresholds.MinInvocations = 10
	thresholds.Cooldown = 7 * 24 * 60 * 60 * 1000000000 // 7 days

	candidates, err := ScanEvolveCandidates(cfg, dbPath, thresholds)
	if err != nil {
		t.Fatalf("ScanEvolveCandidates: %v", err)
	}
	for _, c := range candidates {
		if c.Name == "cooling-skill" {
			t.Error("cooling-skill should not appear: still in cooldown")
		}
	}
}

// ---- RejectProposal ----

func TestRejectProposal(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "reject-skill")

	writeSkillMD(t, skillDir, "---\nname: reject-skill\n---\n\nBody.")

	cand := EvolveCandidate{
		Name:         "reject-skill",
		SkillDir:     skillDir,
		Body:         "Body.",
		FailRate:     0.5,
		InvokedCount: 20,
		FailCount:    10,
	}
	prop := EvolveProposal{
		Diagnosis:    "Some issue.",
		ProposedBody: "Better body.",
		KeyChanges:   []string{"something"},
		Confidence:   0.7,
	}
	fpath, err := WriteEvolveProposal(cand, prop)
	if err != nil {
		t.Fatalf("WriteEvolveProposal: %v", err)
	}

	proposalID := strings.TrimSuffix(filepath.Base(fpath), ".md")

	if err := RejectProposal(skillDir, proposalID); err != nil {
		t.Fatalf("RejectProposal: %v", err)
	}

	data, err := os.ReadFile(fpath)
	if err != nil {
		t.Fatalf("ReadFile proposal: %v", err)
	}
	if !strings.Contains(string(data), "status: rejected") {
		t.Error("proposal status should be 'rejected'")
	}
}

// ---- MarkSkillEvolved / readEvolvedAt ----

func TestMarkSkillEvolved(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "mark-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	before := readEvolvedAt(skillDir)
	if !before.IsZero() {
		t.Error("expected zero time before MarkSkillEvolved")
	}

	if err := MarkSkillEvolved(skillDir); err != nil {
		t.Fatalf("MarkSkillEvolved: %v", err)
	}

	after := readEvolvedAt(skillDir)
	if after.IsZero() {
		t.Error("expected non-zero time after MarkSkillEvolved")
	}
}
