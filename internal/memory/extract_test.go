package memory

import (
	"os"
	"strings"
	"testing"
)

// --- ValidateExtract ---

func TestValidateExtract_Valid(t *testing.T) {
	e := Extract{
		Key:       "extract:my-slug",
		Kind:      "fact",
		Summary:   "short summary",
		Body:      "some body text",
		Tags:      []string{"go", "test"},
		Operation: "ADD",
	}
	if err := ValidateExtract(e); err != nil {
		t.Errorf("expected valid, got error: %v", err)
	}
}

func TestValidateExtract_MissingPrefix(t *testing.T) {
	e := Extract{
		Key:       "no-prefix-slug",
		Operation: "ADD",
	}
	if err := ValidateExtract(e); err == nil {
		t.Error("expected error for missing 'extract:' prefix, got nil")
	}
}

func TestValidateExtract_InvalidSlug(t *testing.T) {
	cases := []string{
		"extract:AB",               // uppercase
		"extract:a",                // too short (< 3)
		"extract:has space",        // space
		"extract:" + strings.Repeat("a", 41), // too long (> 40)
	}
	for _, key := range cases {
		e := Extract{Key: key, Operation: "ADD"}
		if err := ValidateExtract(e); err == nil {
			t.Errorf("expected error for key %q, got nil", key)
		}
	}
}

func TestValidateExtract_InvalidOp(t *testing.T) {
	e := Extract{
		Key:       "extract:valid-slug",
		Operation: "DELETE",
	}
	if err := ValidateExtract(e); err == nil {
		t.Error("expected error for invalid operation, got nil")
	}
}

func TestValidateExtract_SummaryTooLong(t *testing.T) {
	e := Extract{
		Key:       "extract:valid-slug",
		Summary:   strings.Repeat("x", 151),
		Operation: "ADD",
	}
	if err := ValidateExtract(e); err == nil {
		t.Error("expected error for summary > 150 chars, got nil")
	}
}

func TestValidateExtract_AllOpsValid(t *testing.T) {
	for _, op := range []string{"ADD", "UPDATE", "NOOP", "CONFLICT"} {
		e := Extract{Key: "extract:my-slug", Operation: op}
		if err := ValidateExtract(e); err != nil {
			t.Errorf("expected op %q to be valid, got: %v", op, err)
		}
	}
}

// --- ParseExtractsJSON ---

func TestParseExtractsJSON_ValidEnvelope(t *testing.T) {
	input := `{"extracts": [{"key": "extract:foo", "kind": "fact", "summary": "s", "body": "b", "operation": "ADD", "operation_reason": "new"}]}`
	extracts := ParseExtractsJSON(input)
	if len(extracts) != 1 {
		t.Fatalf("expected 1 extract, got %d", len(extracts))
	}
	if extracts[0].Key != "extract:foo" {
		t.Errorf("unexpected key: %s", extracts[0].Key)
	}
}

func TestParseExtractsJSON_DirectArray(t *testing.T) {
	input := `[{"key": "extract:bar", "kind": "procedure", "summary": "s2", "body": "b2", "operation": "UPDATE", "operation_reason": "r"}]`
	extracts := ParseExtractsJSON(input)
	if len(extracts) != 1 {
		t.Fatalf("expected 1 extract, got %d", len(extracts))
	}
	if extracts[0].Key != "extract:bar" {
		t.Errorf("unexpected key: %s", extracts[0].Key)
	}
}

func TestParseExtractsJSON_InvalidInput(t *testing.T) {
	cases := []string{
		"",
		"not json at all",
		"just some text without braces",
	}
	for _, c := range cases {
		result := ParseExtractsJSON(c)
		if result != nil {
			t.Errorf("expected nil for input %q, got %v", c, result)
		}
	}
}

func TestParseExtractsJSON_EmptyExtracts(t *testing.T) {
	input := `{"extracts": []}`
	extracts := ParseExtractsJSON(input)
	// Empty slice is fine — not nil.
	if extracts == nil {
		t.Error("expected empty slice, got nil")
	}
	if len(extracts) != 0 {
		t.Errorf("expected 0 extracts, got %d", len(extracts))
	}
}

// --- AppendToAutoExtractsMD FIFO ---

func TestAppendToAutoExtractsMD_FIFO100(t *testing.T) {
	dir := t.TempDir()

	e := Extract{
		Key:       "extract:test-key",
		Summary:   "test summary",
		Operation: "ADD",
	}

	// Write 101 entries.
	for i := 0; i < 101; i++ {
		if err := AppendToAutoExtractsMD(dir, "test-agent", 5, e); err != nil {
			t.Fatalf("AppendToAutoExtractsMD failed at iteration %d: %v", i, err)
		}
	}

	// Read back and count lines.
	data, err := os.ReadFile(dir + "/auto-extracts.md")
	if err != nil {
		t.Fatalf("failed to read auto-extracts.md: %v", err)
	}

	count := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "- [") {
			count++
		}
	}

	if count != 100 {
		t.Errorf("expected exactly 100 entries after FIFO trim, got %d", count)
	}
}

func TestAppendToAutoExtractsMD_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	e := Extract{
		Key:       "extract:new-key",
		Summary:   "new entry",
		Operation: "ADD",
		Tags:      []string{"go"},
	}
	if err := AppendToAutoExtractsMD(dir, "ruri", 4, e); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err := os.ReadFile(dir + "/auto-extracts.md")
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "extract:new-key") {
		t.Errorf("expected key in content, got: %s", content)
	}
	if !strings.Contains(content, "tags: go") {
		t.Errorf("expected tags in content, got: %s", content)
	}
}
