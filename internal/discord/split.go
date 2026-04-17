package discord

import (
	"strings"
	"unicode/utf8"
)

// splitForDiscord breaks s into chunks of at most budgetBytes, preferring to
// cut at paragraph (\n\n), then line (\n), then word (space) boundaries.
// Rune boundaries are always respected so multi-byte characters stay intact.
// Callers typically pass budgetBytes < 2000 to leave room for a `(i/N) ` prefix.
func splitForDiscord(s string, budgetBytes int) []string {
	if s == "" {
		return nil
	}
	if len(s) <= budgetBytes || budgetBytes <= 0 {
		return []string{s}
	}
	var chunks []string
	for len(s) > budgetBytes {
		cut := findCut(s, budgetBytes)
		chunks = append(chunks, s[:cut])
		s = strings.TrimLeft(s[cut:], "\n ")
	}
	if s != "" {
		chunks = append(chunks, s)
	}
	return chunks
}

// findCut returns a byte index in [1, maxBytes] at which to split s. Preference
// order: "\n\n" > "\n" > " " > last rune boundary. "Preferred" breaks must
// occur at or past the midpoint so we do not create a tiny first chunk.
func findCut(s string, maxBytes int) int {
	if maxBytes >= len(s) {
		return len(s)
	}
	window := s[:maxBytes]
	mid := maxBytes / 2
	// Cut BEFORE the delimiter so the chunk does not end with trailing
	// whitespace. The next iteration TrimLefts leading whitespace, so the
	// delimiter is consumed exactly once.
	if i := strings.LastIndex(window, "\n\n"); i >= mid {
		return i
	}
	if i := strings.LastIndex(window, "\n"); i >= mid {
		return i
	}
	if i := strings.LastIndex(window, " "); i >= mid {
		return i
	}
	// No structural break — back off to the nearest rune start.
	for i := maxBytes; i > 0; i-- {
		if utf8.RuneStart(s[i]) {
			return i
		}
	}
	return maxBytes
}
