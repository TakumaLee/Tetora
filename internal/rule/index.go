// Package rule parses the workspace rules INDEX and matches rules against a
// task prompt, producing a trimmed set for dynamic injection into the system
// prompt. Mirrors the pattern used by internal/skill for dynamic skill
// injection.
package rule

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// Entry represents a rule's metadata — sourced either from INDEX.md (legacy)
// or from per-file YAML frontmatter (preferred).
type Entry struct {
	Keywords []string // normalised lowercase keywords
	Path     string   // filename inside rules/, e.g. "social-media.md"
	Always   bool     // true when the rule is injected on every task
	Agents   []string // if non-empty, rule is always-injected when task.Agent is listed
}

// ParseIndex reads an INDEX.md file and returns the parsed entries.
// The expected format is a single markdown table with three columns:
//
//	| 關鍵字 | 規則檔 | 何時載入 |
//
// The parser is tolerant: lines that do not match the 3-column pipe shape are
// skipped silently. On I/O error the function returns (nil, err) so the
// caller can fall back to the legacy whole-directory injection.
func ParseIndex(indexPath string) ([]Entry, error) {
	f, err := os.Open(indexPath)
	if err != nil {
		return nil, fmt.Errorf("open index: %w", err)
	}
	defer f.Close()

	var entries []Entry
	inTable := false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "|") {
			if inTable {
				// Reached end of the first table — stop; any further tables are ignored.
				break
			}
			continue
		}
		// Separator row like "|---|---|---|" skipped.
		if isSeparatorRow(line) {
			inTable = true
			continue
		}
		cols := splitMarkdownRow(line)
		if len(cols) < 3 {
			continue
		}
		// Header row is detected by the literal column labels.
		if strings.Contains(cols[0], "關鍵字") || strings.EqualFold(cols[0], "keyword") {
			inTable = true
			continue
		}
		if !inTable {
			// Rows before the separator are treated as data too, so don't block.
			inTable = true
		}

		kwRaw := cols[0]
		pathRaw := cols[1]
		whenRaw := cols[2]

		kws := splitKeywords(kwRaw)
		if len(kws) == 0 {
			continue
		}
		path := cleanPath(pathRaw)
		if path == "" {
			continue
		}
		entries = append(entries, Entry{
			Keywords: kws,
			Path:     path,
			Always:   strings.Contains(whenRaw, "常駐"),
		})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan index: %w", err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no entries parsed from %s", indexPath)
	}
	return entries, nil
}

// isSeparatorRow returns true for markdown table separator rows like
// "|---|---|---|" or "| --- | :---: | ---: |". A row is a separator iff its
// non-pipe content consists only of dashes, colons, and whitespace.
func isSeparatorRow(line string) bool {
	s := strings.Trim(line, "|")
	if !strings.Contains(s, "-") {
		return false
	}
	for _, r := range s {
		switch r {
		case '-', ':', '|', ' ', '\t':
			// allowed
		default:
			return false
		}
	}
	return true
}

// splitMarkdownRow splits "| a | b | c |" into ["a","b","c"].
func splitMarkdownRow(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	parts := strings.Split(line, "|")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

// splitKeywords turns a comma-separated keyword column into normalised tokens.
// Keywords are lowercased and stripped of backticks/surrounding whitespace.
func splitKeywords(col string) []string {
	col = strings.ReplaceAll(col, "`", "")
	raw := strings.Split(col, ",")
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		kw := strings.TrimSpace(r)
		if kw == "" {
			continue
		}
		out = append(out, strings.ToLower(kw))
	}
	return out
}

// cleanPath extracts the filename reference from the rule column. Accepts
// "`rules/foo.md`", "rules/foo.md", "foo.md", and returns a path relative to
// the rules directory (e.g. "foo.md"). Entries pointing outside rules/ (like
// "team/TEAM-RULEBOOK.md" or "skills/INDEX.md") are returned empty so they
// are skipped — they are not in the rules dir.
func cleanPath(col string) string {
	s := strings.TrimSpace(col)
	s = strings.Trim(s, "`")
	s = strings.TrimSpace(s)
	// Only accept entries rooted under rules/ or a bare filename.
	switch {
	case strings.HasPrefix(s, "rules/"):
		return strings.TrimPrefix(s, "rules/")
	case strings.Contains(s, "/"):
		// Entries like "team/..." or "skills/..." are not in rules/.
		return ""
	default:
		return s
	}
}
