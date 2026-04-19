package rule

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Metadata is the frontmatter surface for a rule file.
// Missing fields default to Always=true so legacy rules without frontmatter
// remain injected unconditionally.
type Metadata struct {
	Always   bool
	Keywords []string
	Agents   []string
}

// ParseFrontmatter extracts YAML-like frontmatter delimited by "---" lines at
// the top of the file. Supports two value shapes:
//
//	key: value           (scalar: bool or string)
//	key: [a, b, c]       (inline list)
//
// Returns the parsed metadata and the remaining body. When no frontmatter
// delimiter is present, returns defaultMetadata() and the original content.
func ParseFrontmatter(content string) (Metadata, string) {
	meta := defaultMetadata()
	trimmed := strings.TrimLeft(content, " \t\r\n")
	if !strings.HasPrefix(trimmed, "---") {
		return meta, content
	}
	// Strip the opening "---" line.
	rest := strings.TrimPrefix(trimmed, "---")
	rest = strings.TrimLeft(rest, " \t")
	if !strings.HasPrefix(rest, "\n") && !strings.HasPrefix(rest, "\r\n") {
		// Malformed: "---" not followed by newline.
		return meta, content
	}
	rest = strings.TrimPrefix(rest, "\r\n")
	rest = strings.TrimPrefix(rest, "\n")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		// Missing closing delimiter; treat as no frontmatter.
		return meta, content
	}
	block := rest[:end]
	body := rest[end+len("\n---"):]
	body = strings.TrimPrefix(body, "\r\n")
	body = strings.TrimPrefix(body, "\n")

	// Explicit parse — override defaults only for present keys.
	parsed := Metadata{Always: true} // assume always until a key says otherwise
	sawAlways := false
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			continue
		}
		key := strings.TrimSpace(line[:colon])
		val := strings.TrimSpace(line[colon+1:])
		switch key {
		case "always":
			sawAlways = true
			parsed.Always = parseBool(val)
		case "keywords":
			parsed.Keywords = parseList(val)
		case "agents":
			parsed.Agents = parseList(val)
		}
	}
	// If "always" wasn't specified but keywords/agents were, assume conditional (false).
	if !sawAlways && (len(parsed.Keywords) > 0 || len(parsed.Agents) > 0) {
		parsed.Always = false
	}
	return parsed, body
}

func defaultMetadata() Metadata {
	return Metadata{Always: true}
}

func parseBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "yes", "1":
		return true
	default:
		return false
	}
}

// parseList handles "[a, b, c]" and returns lowercased, trimmed tokens.
// Also tolerates an unbracketed "a, b, c" form.
func parseList(s string) []string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	if s == "" {
		return nil
	}
	raw := strings.Split(s, ",")
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		tok := strings.TrimSpace(r)
		tok = strings.Trim(tok, "\"'`")
		if tok == "" {
			continue
		}
		out = append(out, strings.ToLower(tok))
	}
	return out
}

// ScanDir scans rulesDir for top-level *.md files that carry YAML frontmatter
// and converts each into an Entry. INDEX.md is skipped (it is a human-facing
// overview, not a rule to inject). Files without frontmatter are skipped so
// they fall through to INDEX-based resolution — otherwise every legacy file
// would default to Always=true and defeat the dynamic injection budget.
//
// Returns (nil, err) on directory read failure so callers can fall back to
// ParseIndex-based behaviour. Returns an empty slice (no error) when the dir
// is readable but contains no frontmatter-annotated rules.
func ScanDir(rulesDir string) ([]Entry, error) {
	dirEntries, err := os.ReadDir(rulesDir)
	if err != nil {
		return nil, fmt.Errorf("read rules dir: %w", err)
	}
	var entries []Entry
	for _, de := range dirEntries {
		if de.IsDir() {
			continue
		}
		name := de.Name()
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		if strings.EqualFold(name, "INDEX.md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(rulesDir, name))
		if err != nil {
			continue
		}
		if !hasFrontmatter(string(data)) {
			continue
		}
		meta, _ := ParseFrontmatter(string(data))
		entries = append(entries, Entry{
			Keywords: meta.Keywords,
			Path:     name,
			Always:   meta.Always,
			Agents:   meta.Agents,
		})
	}
	return entries, nil
}

// hasFrontmatter reports whether content starts with a well-formed YAML
// frontmatter block ("---\n...\n---"). Used by ScanDir to decide whether a
// rule file opts into per-file metadata or should defer to INDEX.md.
func hasFrontmatter(content string) bool {
	trimmed := strings.TrimLeft(content, " \t\r\n")
	if !strings.HasPrefix(trimmed, "---") {
		return false
	}
	rest := strings.TrimPrefix(trimmed, "---")
	if !strings.HasPrefix(rest, "\n") && !strings.HasPrefix(rest, "\r\n") {
		return false
	}
	rest = strings.TrimPrefix(rest, "\r\n")
	rest = strings.TrimPrefix(rest, "\n")
	return strings.Contains(rest, "\n---")
}

// AgentMatch reports whether e.Agents contains agentName (case-insensitive).
// An empty Agents list means "no agent restriction" and returns false here —
// callers that want "match any agent when list is empty" should check len(e.Agents)==0.
func AgentMatch(e Entry, agentName string) bool {
	if agentName == "" || len(e.Agents) == 0 {
		return false
	}
	target := strings.ToLower(agentName)
	for _, a := range e.Agents {
		if a == target {
			return true
		}
	}
	return false
}
