package skill

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

// varRefRE matches {{varname}} placeholders. Names are [A-Za-z0-9_].
var varRefRE = regexp.MustCompile(`\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}\}`)

// ExtractVars returns the unique {{var}} names referenced anywhere in the
// skill's Command, Args, Env values, or Workdir — sorted for deterministic
// tool-schema generation.
func ExtractVars(s SkillConfig) []string {
	seen := map[string]struct{}{}
	collect := func(text string) {
		for _, m := range varRefRE.FindAllStringSubmatch(text, -1) {
			seen[m[1]] = struct{}{}
		}
	}
	collect(s.Command)
	for _, a := range s.Args {
		collect(a)
	}
	for _, v := range s.Env {
		collect(v)
	}
	collect(s.Workdir)

	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// BuildToolSchema produces a JSON Schema describing the skill's inputs.
// Every extracted {{var}} becomes an optional string property; numeric and
// bool inputs get coerced to strings in the handler.
func BuildToolSchema(s SkillConfig) json.RawMessage {
	vars := ExtractVars(s)
	props := map[string]any{}
	for _, v := range vars {
		props[v] = map[string]any{"type": "string"}
	}
	schema := map[string]any{
		"type":       "object",
		"properties": props,
	}
	buf, _ := json.Marshal(schema)
	return buf
}

// ToolNameFor returns the sanitized tool name for a skill. Hyphens and
// whitespace become underscores; the rest is preserved. This yields a
// stable, LLM-friendly identifier (e.g. "competitive-intel" → "competitive_intel").
func ToolNameFor(s SkillConfig) string {
	name := strings.ToLower(strings.TrimSpace(s.Name))
	r := strings.NewReplacer("-", "_", " ", "_", ".", "_", "/", "_")
	return r.Replace(name)
}
