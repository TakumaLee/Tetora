package messaging

// TruncateStr truncates s to at most maxLen runes.
// If truncated, the last 3 characters are replaced with "...".
// If maxLen < 4, returns the first maxLen runes without a suffix.
func TruncateStr(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen < 4 {
		return string(runes[:maxLen])
	}
	return string(runes[:maxLen-3]) + "..."
}
