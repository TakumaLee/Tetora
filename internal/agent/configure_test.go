package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseCapabilityResponse_ValidJSON(t *testing.T) {
	raw := `{"selected_capabilities": ["memory.auto-extracts", "skill-evolve"], "reason": "I need cross-session memory"}`
	resp, err := parseCapabilityResponse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.SelectedCapabilities) != 2 {
		t.Fatalf("expected 2 capabilities, got %d", len(resp.SelectedCapabilities))
	}
	if resp.SelectedCapabilities[0] != "memory.auto-extracts" {
		t.Errorf("expected memory.auto-extracts, got %s", resp.SelectedCapabilities[0])
	}
}

func TestParseCapabilityResponse_JSONEmbeddedInText(t *testing.T) {
	// Claude often wraps JSON in prose.
	raw := `Sure! Here is my selection:
{"selected_capabilities": ["weekly-review"], "reason": "weekly maintenance"}
Hope that helps.`
	resp, err := parseCapabilityResponse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.SelectedCapabilities) != 1 || resp.SelectedCapabilities[0] != "weekly-review" {
		t.Errorf("unexpected selection: %v", resp.SelectedCapabilities)
	}
}

func TestParseCapabilityResponse_InvalidJSON(t *testing.T) {
	_, err := parseCapabilityResponse("not json at all")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestParseCapabilityResponse_Empty(t *testing.T) {
	resp, err := parseCapabilityResponse(`{"selected_capabilities": [], "reason": "none needed"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.SelectedCapabilities) != 0 {
		t.Errorf("expected empty selection, got %v", resp.SelectedCapabilities)
	}
}

func TestBuildConfigurePrompt_IncludesSoul(t *testing.T) {
	prompt := buildConfigurePrompt("hisui", "I am a market analyst.")
	if !contains(prompt, "I am a market analyst.") {
		t.Error("expected SOUL content in prompt")
	}
	if !contains(prompt, "hisui") {
		t.Error("expected agent name in prompt")
	}
	if !contains(prompt, "selected_capabilities") {
		t.Error("expected JSON schema hint in prompt")
	}
}

func TestBuildConfigurePrompt_NoSoul(t *testing.T) {
	prompt := buildConfigurePrompt("amber", "")
	if !contains(prompt, "amber") {
		t.Error("expected agent name in prompt")
	}
}

func TestGenerateCapabilityFiles_CreatesFiles(t *testing.T) {
	dir := t.TempDir()

	resp := &capabilityResponse{
		SelectedCapabilities: []string{"memory.auto-extracts", "skill-evolve"},
		Reason:               "test",
	}

	if err := generateCapabilityFiles(dir, "/tetora/workspace/rules/tetora-agent-io-protocol.md", resp); err != nil {
		t.Fatalf("generateCapabilityFiles: %v", err)
	}

	// CLAUDE.md must exist and contain @SOUL.md
	claudeMD, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("CLAUDE.md not created: %v", err)
	}
	if !contains(string(claudeMD), "@SOUL.md") {
		t.Error("CLAUDE.md missing @SOUL.md")
	}
	if !contains(string(claudeMD), "@capabilities/index.md") {
		t.Error("CLAUDE.md missing @capabilities/index.md")
	}

	// capabilities/index.md must list selected capabilities.
	index, err := os.ReadFile(filepath.Join(dir, "capabilities", "index.md"))
	if err != nil {
		t.Fatalf("capabilities/index.md not created: %v", err)
	}
	if !contains(string(index), "memory.auto-extracts") {
		t.Error("index.md missing memory.auto-extracts")
	}
	if !contains(string(index), "skill-evolve") {
		t.Error("index.md missing skill-evolve")
	}
	// weekly-review was not selected
	if contains(string(index), "weekly-review") {
		t.Error("index.md should not contain weekly-review (not selected)")
	}

	// Detail files for selected capabilities.
	if _, err := os.Stat(filepath.Join(dir, "capabilities", "memory-subscribe.md")); err != nil {
		t.Error("memory-subscribe.md not created")
	}
	if _, err := os.Stat(filepath.Join(dir, "capabilities", "skill-evolve.md")); err != nil {
		t.Error("skill-evolve.md not created")
	}
	// weekly-review detail must NOT be created.
	if _, err := os.Stat(filepath.Join(dir, "capabilities", "weekly-review.md")); err == nil {
		t.Error("weekly-review.md created but should not be (not selected)")
	}
}

func TestGenerateCapabilityFiles_AllCapabilities(t *testing.T) {
	dir := t.TempDir()
	resp := &capabilityResponse{
		SelectedCapabilities: []string{"memory.auto-extracts", "skill-evolve", "weekly-review", "deep-memory-extract"},
	}
	if err := generateCapabilityFiles(dir, "/proto.md", resp); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, cap := range BuiltinCapabilities {
		if cap.DetailFile == "" {
			continue
		}
		path := filepath.Join(dir, "capabilities", cap.DetailFile)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("detail file not created: %s", path)
		}
	}
}

func TestConfigureResult_ConfigFlags(t *testing.T) {
	// deep-memory-extract must produce a ConfigFlag entry.
	var deepCap *Capability
	for i := range BuiltinCapabilities {
		if BuiltinCapabilities[i].ID == "deep-memory-extract" {
			deepCap = &BuiltinCapabilities[i]
			break
		}
	}
	if deepCap == nil {
		t.Fatal("deep-memory-extract not in BuiltinCapabilities")
	}
	if deepCap.ConfigFlag != "deepMemoryExtract.enabled" {
		t.Errorf("unexpected ConfigFlag: %q", deepCap.ConfigFlag)
	}
	if deepCap.DetailFile == "" {
		t.Error("deep-memory-extract should have a detail file")
	}
}

func TestConfigureResult_WeeklyReviewCronJob(t *testing.T) {
	var wrCap *Capability
	for i := range BuiltinCapabilities {
		if BuiltinCapabilities[i].ID == "weekly-review" {
			wrCap = &BuiltinCapabilities[i]
			break
		}
	}
	if wrCap == nil {
		t.Fatal("weekly-review not in BuiltinCapabilities")
	}
	if wrCap.CronExpr != "0 9 * * 0" {
		t.Errorf("unexpected CronExpr: %q", wrCap.CronExpr)
	}
}

func TestWriteCLAUDEMD(t *testing.T) {
	dir := t.TempDir()
	if err := writeCLAUDEMD(dir, "/workspace/rules/proto.md"); err != nil {
		t.Fatalf("writeCLAUDEMD: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	content := string(data)
	if !contains(content, "@SOUL.md") {
		t.Error("missing @SOUL.md")
	}
	if !contains(content, "@capabilities/index.md") {
		t.Error("missing @capabilities/index.md")
	}
	if !contains(content, "/workspace/rules/proto.md") {
		t.Error("missing io-protocol path")
	}
}

func TestLoadSoul_Missing(t *testing.T) {
	dir := t.TempDir()
	if got := loadSoul(dir); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestLoadSoul_Found(t *testing.T) {
	dir := t.TempDir()
	content := "I am a test agent."
	os.WriteFile(filepath.Join(dir, "SOUL.md"), []byte(content), 0o644)
	if got := loadSoul(dir); got != content {
		t.Errorf("expected %q, got %q", content, got)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}
