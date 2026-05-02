package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// EvolveThresholds controls when a skill qualifies for evolution.
type EvolveThresholds struct {
	MinInvocations    int           // default 10
	FailRateThreshold float64       // default 0.4
	Cooldown          time.Duration // default 7 * 24h
	Days              int           // stat window, default 30
}

// DefaultEvolveThresholds returns the production defaults.
func DefaultEvolveThresholds() EvolveThresholds {
	return EvolveThresholds{
		MinInvocations:    10,
		FailRateThreshold: 0.40,
		Cooldown:          7 * 24 * time.Hour,
		Days:              30,
	}
}

// EvolveCandidate is a skill that meets evolve thresholds.
type EvolveCandidate struct {
	Name          string
	SkillDir      string
	Frontmatter   string // raw "---\n...\n---" block
	Body          string // body (no frontmatter)
	FailRate      float64
	InvokedCount  int
	FailCount     int
	Failures      string    // from LoadSkillFailures
	LastEvolvedAt time.Time // zero if never
}

// EvolveProposal is the parsed LLM result.
type EvolveProposal struct {
	Diagnosis    string   `json:"diagnosis"`
	ProposedBody string   `json:"proposed_body"`
	KeyChanges   []string `json:"key_changes"`
	Confidence   float64  `json:"confidence"`
}

// ProposalInfo identifies a proposal file.
type ProposalInfo struct {
	ID       string    // timestamp portion of filename
	FilePath string
	Status   string    // "pending" | "approved" | "rejected"
	Created  time.Time
}

// ScanEvolveCandidates returns skills meeting evolve thresholds.
// dbPath is the skill_usage DB path.
func ScanEvolveCandidates(cfg *AppConfig, dbPath string, thresholds EvolveThresholds) ([]EvolveCandidate, error) {
	stats, err := QuerySkillStats(dbPath, "", thresholds.Days)
	if err != nil {
		return nil, err
	}

	var candidates []EvolveCandidate
	for _, row := range stats {
		name := fmt.Sprintf("%v", row["skill_name"])
		invoked := toInt(row["invoked"])
		fail := toInt(row["fail"])

		if invoked < thresholds.MinInvocations {
			continue
		}
		failRate := float64(fail) / float64(invoked)
		if failRate < thresholds.FailRateThreshold {
			continue
		}

		// Find skill dir.
		skillDir := filepath.Join(SkillsDir(cfg), name)
		if _, err := os.Stat(skillDir); os.IsNotExist(err) {
			continue // not a file-based skill or already removed
		}

		// Check cooldown.
		lastEvolved := readEvolvedAt(skillDir)
		if !lastEvolved.IsZero() && time.Since(lastEvolved) < thresholds.Cooldown {
			continue
		}

		// Load skill body.
		fm, body, err := LoadSkillBody(skillDir)
		if err != nil {
			continue
		}

		failures := LoadSkillFailures(skillDir)

		candidates = append(candidates, EvolveCandidate{
			Name:          name,
			SkillDir:      skillDir,
			Frontmatter:   fm,
			Body:          body,
			FailRate:      failRate,
			InvokedCount:  invoked,
			FailCount:     fail,
			Failures:      failures,
			LastEvolvedAt: lastEvolved,
		})
	}
	return candidates, nil
}

// LoadSkillBody parses a SKILL.md file and returns (frontmatter, body, error).
// frontmatter includes the --- delimiters. body is everything after the closing ---.
func LoadSkillBody(skillDir string) (string, string, error) {
	candidates := []string{"SKILL.md", "skill.md"}
	var data []byte
	var err error
	for _, name := range candidates {
		data, err = os.ReadFile(filepath.Join(skillDir, name))
		if err == nil {
			break
		}
	}
	if err != nil {
		return "", "", fmt.Errorf("skill file not found in %s", skillDir)
	}

	return LoadSkillBodyFromString(string(data))
}

// LoadSkillBodyFromString parses frontmatter from a raw string (helper for LLM output cleanup).
func LoadSkillBodyFromString(content string) (string, string, error) {
	if !strings.HasPrefix(content, "---") {
		return "", content, nil
	}

	// Find closing ---.
	idx := strings.Index(content[3:], "\n---")
	if idx == -1 {
		return "", content, nil
	}
	closeIdx := 3 + idx + 4 // skip past initial "---" + "\n---"
	fm := content[:closeIdx]
	body := strings.Trim(content[closeIdx:], "\n")
	return fm, body, nil
}

// EvolveFrontmatterPresent reports whether frontmatter starts with "---".
func EvolveFrontmatterPresent(fm string) bool {
	return strings.HasPrefix(fm, "---")
}

const evolvedAtFile = ".evolved-at"

func readEvolvedAt(skillDir string) time.Time {
	data, err := os.ReadFile(filepath.Join(skillDir, evolvedAtFile))
	if err != nil {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(data)))
	if err != nil {
		return time.Time{}
	}
	return t
}

// MarkSkillEvolved persists the current time to skills/<name>/.evolved-at.
func MarkSkillEvolved(skillDir string) error {
	return os.WriteFile(
		filepath.Join(skillDir, evolvedAtFile),
		[]byte(time.Now().UTC().Format(time.RFC3339)),
		0o644,
	)
}

// WriteEvolveProposal writes a proposal markdown file.
// Returns the proposal file path.
func WriteEvolveProposal(cand EvolveCandidate, prop EvolveProposal) (string, error) {
	dir := filepath.Join(cand.SkillDir, "proposals")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	id := time.Now().UTC().Format("2006-01-02T15-04-05")
	fpath := filepath.Join(dir, id+".md")

	diff := computeUnifiedDiff(cand.Body, prop.ProposedBody)

	changes := ""
	for _, c := range prop.KeyChanges {
		changes += "- " + c + "\n"
	}

	content := fmt.Sprintf(`---
proposal_id: %s
skill: %s
created_at: %s
fail_rate: %.2f
invocations: %d
failures_sampled: %d
confidence: %.2f
status: pending
---

# Diagnosis

%s

# Key Changes

%s

# Original Body

%s

# Proposed Body

%s

# Diff

`+"```diff\n%s\n```"+`

# Failures Sampled

%s
`,
		id,
		cand.Name,
		time.Now().UTC().Format(time.RFC3339),
		cand.FailRate,
		cand.InvokedCount,
		cand.FailCount,
		prop.Confidence,
		prop.Diagnosis,
		changes,
		cand.Body,
		prop.ProposedBody,
		diff,
		cand.Failures,
	)

	return fpath, os.WriteFile(fpath, []byte(content), 0o644)
}

// ListProposals returns proposal files for a skill directory.
func ListProposals(skillDir string) ([]ProposalInfo, error) {
	dir := filepath.Join(skillDir, "proposals")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var proposals []ProposalInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".md")
		fpath := filepath.Join(dir, e.Name())
		status := readProposalStatus(fpath)
		created, _ := time.Parse("2006-01-02T15-04-05", id)
		proposals = append(proposals, ProposalInfo{
			ID:       id,
			FilePath: fpath,
			Status:   status,
			Created:  created,
		})
	}
	return proposals, nil
}

func readProposalStatus(fpath string) string {
	data, err := os.ReadFile(fpath)
	if err != nil {
		return "unknown"
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "status: ") {
			return strings.TrimPrefix(line, "status: ")
		}
	}
	return "pending"
}

// ApproveProposal copies the proposed body into the skill file (preserving frontmatter)
// and backs up the original to skills/<name>/history/<timestamp>.md.
func ApproveProposal(skillDir, proposalID string) error {
	proposalPath := filepath.Join(skillDir, "proposals", proposalID+".md")
	data, err := os.ReadFile(proposalPath)
	if err != nil {
		return fmt.Errorf("proposal not found: %s", proposalPath)
	}

	// Extract proposed body from proposal.
	proposed := extractSection(string(data), "# Proposed Body")
	if proposed == "" {
		return fmt.Errorf("proposal has no '# Proposed Body' section")
	}

	// Load current skill file.
	skillFile := filepath.Join(skillDir, "SKILL.md")
	if _, err := os.Stat(skillFile); os.IsNotExist(err) {
		skillFile = filepath.Join(skillDir, "skill.md")
	}
	current, err := os.ReadFile(skillFile)
	if err != nil {
		return err
	}

	// Backup original.
	histDir := filepath.Join(skillDir, "history")
	if err := os.MkdirAll(histDir, 0o755); err != nil {
		return err
	}
	backupPath := filepath.Join(histDir, proposalID+"-original.md")
	if err := os.WriteFile(backupPath, current, 0o644); err != nil {
		return err
	}

	// Reconstruct skill file: frontmatter + proposed body.
	fm, _, _ := LoadSkillBody(skillDir)
	var newContent string
	if fm != "" {
		newContent = fm + "\n" + proposed
	} else {
		newContent = proposed
	}
	if err := os.WriteFile(skillFile, []byte(newContent), 0o644); err != nil {
		return err
	}

	// Mark proposal as approved.
	updateProposalStatus(proposalPath, "approved")
	return nil
}

// RejectProposal marks proposal as rejected.
func RejectProposal(skillDir, proposalID string) error {
	proposalPath := filepath.Join(skillDir, "proposals", proposalID+".md")
	if _, err := os.Stat(proposalPath); err != nil {
		return fmt.Errorf("proposal not found: %s", proposalPath)
	}
	updateProposalStatus(proposalPath, "rejected")
	return nil
}

func updateProposalStatus(fpath, status string) {
	data, err := os.ReadFile(fpath)
	if err != nil {
		return
	}
	content := regexp.MustCompile(`(?m)^status: .+$`).
		ReplaceAllString(string(data), "status: "+status)
	_ = os.WriteFile(fpath, []byte(content), 0o644)
}

func extractSection(content, header string) string {
	idx := strings.Index(content, "\n"+header+"\n")
	if idx == -1 {
		idx = strings.Index(content, header+"\n")
		if idx == -1 {
			return ""
		}
	} else {
		idx++ // skip leading newline
	}
	rest := content[idx+len(header)+1:]
	// Find next header.
	nextHeader := strings.Index(rest, "\n# ")
	if nextHeader != -1 {
		rest = rest[:nextHeader]
	}
	return strings.TrimSpace(rest)
}

// computeUnifiedDiff returns a minimal line-by-line diff of a→b.
func computeUnifiedDiff(a, b string) string {
	aLines := strings.Split(a, "\n")
	bLines := strings.Split(b, "\n")

	var out strings.Builder
	maxA, maxB := len(aLines), len(bLines)
	i, j := 0, 0
	for i < maxA || j < maxB {
		switch {
		case i >= maxA:
			fmt.Fprintf(&out, "+ %s\n", bLines[j])
			j++
		case j >= maxB:
			fmt.Fprintf(&out, "- %s\n", aLines[i])
			i++
		case aLines[i] == bLines[j]:
			fmt.Fprintf(&out, "  %s\n", aLines[i])
			i++
			j++
		default:
			fmt.Fprintf(&out, "- %s\n", aLines[i])
			fmt.Fprintf(&out, "+ %s\n", bLines[j])
			i++
			j++
		}
	}
	return out.String()
}

// toInt converts an interface{} to int (for DB row values).
func toInt(v any) int {
	switch x := v.(type) {
	case int64:
		return int(x)
	case float64:
		return int(x)
	case int:
		return x
	}
	return 0
}
