package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Extract holds a single extracted memory item from an LLM response.
type Extract struct {
	Key             string   `json:"key"`
	Kind            string   `json:"kind"` // fact|procedure|gotcha|preference
	Summary         string   `json:"summary"`
	Body            string   `json:"body"`
	Tags            []string `json:"tags"`
	Operation       string   `json:"operation"` // ADD|UPDATE|NOOP|CONFLICT
	OperationReason string   `json:"operation_reason"`
}

// ExtractEnvelope is the top-level LLM response envelope.
type ExtractEnvelope struct {
	Extracts []Extract `json:"extracts"`
}

var validSlug = regexp.MustCompile(`^[a-z0-9-]{3,40}$`)

// ValidateExtract enforces: key prefix "extract:", slug regex, valid op enum,
// length limits. Body is truncated in place to 600 chars when oversize, so the
// caller observes the capped value.
func ValidateExtract(e *Extract) error {
	if e == nil {
		return fmt.Errorf("nil extract")
	}
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
		e.Body = e.Body[:600]
	}
	return nil
}

// ParseExtractsJSON tolerantly parses LLM output. Returns nil on failure.
// Tries the array form first when the output starts with `[`, otherwise the
// `{...}` envelope form, falling back across both.
func ParseExtractsJSON(output string) []Extract {
	output = strings.TrimSpace(output)

	objStart := strings.Index(output, "{")
	arrStart := strings.Index(output, "[")
	if objStart == -1 && arrStart == -1 {
		return nil
	}

	tryArray := func() []Extract {
		if arrStart == -1 {
			return nil
		}
		var arr []Extract
		if err := json.Unmarshal([]byte(output[arrStart:]), &arr); err == nil {
			return arr
		}
		return nil
	}
	tryEnvelope := func() []Extract {
		if objStart == -1 {
			return nil
		}
		var env ExtractEnvelope
		if err := json.Unmarshal([]byte(output[objStart:]), &env); err == nil {
			return env.Extracts
		}
		return nil
	}

	// Prefer the form whose marker appears first in the output.
	if arrStart != -1 && (objStart == -1 || arrStart < objStart) {
		if r := tryArray(); r != nil {
			return r
		}
		return tryEnvelope()
	}
	if r := tryEnvelope(); r != nil {
		return r
	}
	return tryArray()
}

const autoExtractsMaxEntries = 100

// autoExtractsMu serialises the read-modify-write of auto-extracts.md so
// concurrent goroutines (runDeepMemoryExtract is launched as `go func()`)
// cannot interleave their FIFO updates and corrupt the file.
var autoExtractsMu sync.Mutex

// AppendToAutoExtractsMD appends one summary line to workspace/memory/auto-extracts.md.
// Maintains a FIFO of at most 100 entries.
func AppendToAutoExtractsMD(memoryDir, agent string, score int, e Extract) error {
	autoExtractsMu.Lock()
	defer autoExtractsMu.Unlock()

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

// dailyBudget tracks cumulative deep-memory-extract spend per UTC day.
var (
	dailyBudgetMu    sync.Mutex
	dailyBudgetDate  string
	dailyBudgetSpent float64
)

// ReserveDailyBudget reports whether spending budgetUSD more today would stay
// within limitUSD. When limitUSD <= 0, no cap is enforced. Increments the
// running total only when the reservation is granted.
func ReserveDailyBudget(limitUSD, budgetUSD float64) bool {
	dailyBudgetMu.Lock()
	defer dailyBudgetMu.Unlock()
	today := time.Now().UTC().Format("2006-01-02")
	if dailyBudgetDate != today {
		dailyBudgetDate = today
		dailyBudgetSpent = 0
	}
	if limitUSD > 0 && dailyBudgetSpent+budgetUSD > limitUSD {
		return false
	}
	dailyBudgetSpent += budgetUSD
	return true
}

// AdjustDailyBudget corrects the running spend after the actual cost is known.
// Pass deltaUSD = actual - reserved (can be negative).
func AdjustDailyBudget(deltaUSD float64) {
	dailyBudgetMu.Lock()
	defer dailyBudgetMu.Unlock()
	dailyBudgetSpent += deltaUSD
	if dailyBudgetSpent < 0 {
		dailyBudgetSpent = 0
	}
}
