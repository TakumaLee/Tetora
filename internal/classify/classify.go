package classify

import (
	"strings"
	"unicode/utf8"
)

// Complexity levels for requests.
type Complexity int

const (
	Simple   Complexity = iota // No tools, minimal context
	Standard                   // Basic tools, standard context
	Complex                    // Full tools, heavy context injection
)

// ComplexSources are message sources that are always considered complex.
var ComplexSources = map[string]bool{
	"main": true, // CLI/Dashboard
}

// KeywordClassifiedSources use keyword detection instead of blanket Simple/Complex.
var KeywordClassifiedSources = map[string]bool{
	"cron":     true,
	"workflow": true,
}

// ChatSources are message sources that use chat-style interaction.
var ChatSources = map[string]bool{
	"discord":  true,
	"telegram": true,
	"whatsapp": true,
	"slack":    true,
	"line":     true,
	"gchat":    true,
}

func (c Complexity) String() string {
	switch c {
	case Simple:
		return "simple"
	case Standard:
		return "standard"
	case Complex:
		return "complex"
	default:
		return "unknown"
	}
}

// Tool-intent keywords: short messages containing these need Standard (tools available).
var toolIntentKeywordsZH = []string{
	"搜尋", "搜索", "查詢", "查一下", "找一下", "找找", "查查",
	"新聞", "情報", "最新", "趨勢", "分析", "報告",
	"x.com", "twitter", "推特", "研究", "論文",
	"看一下", "追蹤", "進度", "動態",
}

var toolIntentKeywordsEN = []string{
	"search", "find", "look up", "lookup", "query", "research",
	"news", "latest", "trending", "analyze", "report", "intel",
	"update", "track", "monitor", "check", "browse",
}

// complexKeywordsEN contains coding-related keywords (English).
var complexKeywordsEN = []string{
	"refactor", "database", "sql", "api", "endpoint", "optimize",
	"deploy", "concurrency", "mutex", "implement", "debug",
}

// Classify determines the complexity of a user request.
func Classify(prompt string, source string) Complexity {
	srcLower := strings.ToLower(strings.TrimSpace(source))
	runeLen := utf8.RuneCountInString(prompt)

	// Source-based overrides: cron and workflow MUST always be Complex
	// to ensure enough context injection and session limits.
	if srcLower == "cron" || srcLower == "workflow" || ComplexSources[srcLower] {
		return Complex
	}

	promptLower := strings.ToLower(prompt)

	// Short chat messages: check for tool intent
	isChat := false
	for k := range ChatSources {
		if strings.HasPrefix(srcLower, k) {
			isChat = true
			break
		}
	}

	if runeLen < 100 && isChat {
		if containsAnySubstring(prompt, toolIntentKeywordsZH) ||
			containsAnyComplexWord(promptLower, toolIntentKeywordsEN) {
			return Standard
		}
		return Simple
	}

	if runeLen > 2000 {
		return Complex
	}

	// Keyword check
	if containsAnyComplexWord(promptLower, complexKeywordsEN) {
		return Complex
	}

	if runeLen > 500 {
		return Standard
	}

	return Simple
}

func containsAnySubstring(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func containsAnyComplexWord(s string, keywords []string) bool {
	for _, kw := range keywords {
		if strings.Contains(s, kw) {
			return true
		}
	}
	return false
}
