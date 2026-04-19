package rule

import "strings"

// Match partitions parsed entries into always-on and prompt-matched groups.
// Matching is case-insensitive substring on the task prompt (same semantics as
// skill.ShouldInjectSkill keyword loop). An entry appearing in `always` is
// excluded from `matched` to avoid double injection.
//
// Matched entries are returned in the order they appear in `entries`, so the
// caller can apply a stable per-task cap by truncating the slice.
func Match(entries []Entry, prompt string) (always, matched []Entry) {
	return MatchForAgent(entries, prompt, "")
}

// MatchForAgent is Match extended with an agent-aware always-on rule: when
// entry.Agents lists agentName, the entry is treated as always for this task.
func MatchForAgent(entries []Entry, prompt, agentName string) (always, matched []Entry) {
	promptLower := strings.ToLower(prompt)
	for _, e := range entries {
		if e.Always || AgentMatch(e, agentName) {
			always = append(always, e)
			continue
		}
		for _, kw := range e.Keywords {
			if kw == "" {
				continue
			}
			if strings.Contains(promptLower, kw) {
				matched = append(matched, e)
				break
			}
		}
	}
	return always, matched
}
