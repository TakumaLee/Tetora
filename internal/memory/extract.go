package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Extract holds a single extracted memory item from an LLM response.
type Extract struct {
	Key             string   `json:"key"`
	Kind            string   `json:"kind"`            // fact|procedure|gotcha|preference
	Summary         string   `json:"summary"`
	Body            string   `json:"body"`
	Tags            []string `json:"tags"`
	Operation       string   `json:"operation"`       // ADD|UPDATE|NOOP|CONFLICT
	OperationReason string   `json:"operation_reason"`
}

// ExtractEnvelope is the top-level LLM response envelope.
type ExtractEnvelope struct {
	Extracts []Extract `json:"extracts"`
}

var validSlug = regexp.MustCompile(`^[a-z0-9-]{3,40}$`)

// ValidateExtract enforces: key prefix "extract:", slug regex, valid op enum, length limits.
func ValidateExtract(e Extract) error {
	if !strings.HasPrefix(e.Key, "extract:") {
		return fmt.Errorf("key must have prefix 'extract:', got %q", e.Key)
	}
	slug := strings.TrimPrefix(e.Key, "extract:")
	if !validSlug.MatchString(slug) {
		return fmt.Errorf("invalid slug %q: must match [a-z0-9-]{3,40}", slug)
	}
	validOps := map[string]bool{"ADD": true, "UPDATE": true, "NOOP": true, "CONFLICT": true}
	if !validOps[e.Operation] {
		return fmt.Errorf("invalid operation %q", e.Operation)
	}
	if len(e.Summary) > 150 {
		return fmt.Errorf("summary too long: %d > 150", len(e.Summary))
	}
	if len(e.Body) > 600 {
		e.Body = e.Body[:600] // truncate silently
	}
	return nil
}

// ParseExtractsJSON tolerantly parses LLM output. Returns nil on failure.
func ParseExtractsJSON(output string) []Extract {
	output = strings.TrimSpace(output)

	// Try to find a JSON object start.
	start := strings.Index(output, "{")
	if start == -1 {
		return nil
	}

	// Try as envelope first.
	var env ExtractEnvelope
	if err := json.Unmarshal([]byte(output[start:]), &env); err == nil {
		return env.Extracts
	}

	// Try direct array.
	arrStart := strings.Index(output, "[")
	if arrStart != -1 {
		var arr []Extract
		if err := json.Unmarshal([]byte(output[arrStart:]), &arr); err == nil {
			return arr
		}
	}

	return nil
}

const autoExtractsMaxEntries = 100

// AppendToAutoExtractsMD appends one summary line to workspace/memory/auto-extracts.md.
// Maintains a FIFO of at most 100 entries.
func AppendToAutoExtractsMD(memoryDir, agent string, score int, e Extract) error {
	fpath := filepath.Join(memoryDir, "auto-extracts.md")

	// Read existing entry lines.
	var lines []string
	if data, err := os.ReadFile(fpath); err == nil {
		for _, l := range strings.Split(string(data), "\n") {
			l = strings.TrimSpace(l)
			if strings.HasPrefix(l, "- [") {
				lines = append(lines, l)
			}
		}
	}

	// Build and prepend the new entry.
	ts := time.Now().UTC().Format(time.RFC3339)
	tags := ""
	if len(e.Tags) > 0 {
		tags = fmt.Sprintf(" [tags: %s]", strings.Join(e.Tags, ", "))
	}
	line := fmt.Sprintf("- [%s] (key=%s, score=%d, agent=%s, op=%s) %s%s",
		ts, e.Key, score, agent, e.Operation, e.Summary, tags)
	lines = append([]string{line}, lines...)

	// FIFO trim.
	if len(lines) > autoExtractsMaxEntries {
		lines = lines[:autoExtractsMaxEntries]
	}

	// Write back with header.
	content := "# Auto-Extracts\n\n" + strings.Join(lines, "\n") + "\n"
	return os.WriteFile(fpath, []byte(content), 0o644)
}
