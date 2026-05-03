// Package classify categorizes user requests by complexity.
// Used to decide session limits, model selection, and prompt depth.
package classify

import (
	"strings"
	"tetora/internal/nlp"
	"unicode/utf8"
)

// Complexity categorizes how complex a user request is.
type Complexity int

const (
	Simple   Complexity = 0
	Standard Complexity = 1
	Complex  Complexity = 2
)

// String returns the human-readable name of the complexity level.
func (c Complexity) String() string {
	switch c {
	case Simple:
		return "simple"
	case Standard:
		return "standard"
	case Complex:
		return "complex"
	default:
		return "standard"
	}
}

// MaxSessionMessages returns the maximum number of session messages
// allowed for the given complexity level.
func MaxSessionMessages(c Complexity) int {
	switch c {
	case Simple:
		return 5
	case Standard:
		return 10
	case Complex:
		return 20
	default:
		return 10
	}
}

// MaxSessionChars returns the maximum total character budget
// for session output at the given complexity level.
func MaxSessionChars(c Complexity) int {
	switch c {
	case Simple:
		return 4000
	case Standard:
		return 8000
	case Complex:
		return 16000
	default:
		return 8000
	}
}

// ChatSources contains chat-like sources that may qualify for simple classification.
var ChatSources = map[string]bool{
	"chat":     true,
	"discord":  true,
	"telegram": true,
	"slack":    true,
	"whatsapp": true,
	"line":     true,
	"matrix":   true,
	"teams":    true,
	"signal":   true,
	"gchat":    true,
	"imessage": true,
}

// ComplexSources contains sources that always indicate complex work.
// Note: cron and workflow are no longer auto-Complex — they use keyword-based
// classification like taskboard to avoid injecting heavy context for simple jobs.
var ComplexSources = map[string]bool{
	"agent-comm": true,
}

// KeywordClassifiedSources are sources that use keyword-based complexity
// instead of always being Complex. Short/simple tasks get Standard,
// only genuinely complex tasks (3+ coding keywords) get Complex.
var KeywordClassifiedSources = map[string]bool{
	"cron":     true,
	"workflow": true,
}

// complexKeywordsEN contains coding-related keywords (English).
// Matched as whole words (word-boundary aware).
var complexKeywordsEN = []string{
	"code", "implement", "build", "debug", "refactor", "deploy",
	"api", "database", "sql", "function", "algorithm",
	"compile", "test", "migration", "schema", "endpoint",
	"infrastructure", "architecture", "pipeline", "optimize",
	"benchmark", "profiling", "concurrency", "mutex",
	"authentication", "authorization", "encryption",
}

// complexKeywordsJA contains coding-related keywords (Japanese).
// Matched as substrings (no word boundaries in Japanese).
var complexKeywordsJA = []string{
	"コード", "実装", "デバッグ", "リファクタ", "デプロイ",
	"データベース", "アルゴリズム", "コンパイル", "テスト",
	"マイグレーション", "スキーマ", "エンドポイント",
	"インフラ", "アーキテクチャ", "パイプライン", "最適化",
	"ベンチマーク", "プロファイリング", "並行処理",
	"認証", "暗号化", "関数", "設計",
}

// Classify determines the complexity of a user request based on
// the prompt text and the message source (e.g. "discord", "cron").
func Classify(prompt string, source string) Complexity {
	srcLower := strings.ToLower(strings.TrimSpace(source))
	runeLen := utf8.RuneCountInString(prompt)

	// Source-based overrides: complex sources always yield complex.
	if ComplexSources[srcLower] {
		return Complex
	}

	// Very long prompts are complex regardless of content.
	if runeLen > 2000 {
		return Complex
	}

	promptLower := strings.ToLower(prompt)

	// Keyword-classified sources (cron, workflow, taskboard): use keyword counting
	// instead of blanket Complex. This avoids injecting heavy context (3 reflections,
	// writing style, all AddDirs) for simple scheduled/dispatch tasks.
	if srcLower == "taskboard" || KeywordClassifiedSources[srcLower] {
		if runeLen < 100 {
			return Simple
		}
		kwCount := countComplexKeywords(promptLower, prompt)
		if kwCount >= 3 {
			return Complex
		}
		return Standard
	}

	// Check for coding-related keywords (case-insensitive, whole-word match).
	if containsAnyComplexWord(promptLower, complexKeywordsEN) {
		return Complex
	}
	// Japanese keywords: substring match is correct since Japanese has no word boundaries.
	if containsAnySubstring(prompt, complexKeywordsJA) {
		return Complex
	}

	// Short chat messages from chat-like sources are simple.
	if runeLen < 100 && ChatSources[srcLower] {
		return Simple
	}

	return Standard
}

// containsAnyComplexWord returns true if text contains any keyword as a whole word.
func containsAnyComplexWord(text string, keywords []string) bool {
	for _, kw := range keywords {
		if nlp.ContainsWord(text, kw) {
			return true
		}
	}
	return false
}

// countComplexKeywords counts how many distinct coding keywords appear in the text.
// Checks both EN (word-boundary) and JA (substring) keywords.
func countComplexKeywords(textLower, textOriginal string) int {
	count := 0
	for _, kw := range complexKeywordsEN {
		if nlp.ContainsWord(textLower, kw) {
			count++
		}
	}
	for _, kw := range complexKeywordsJA {
		if strings.Contains(textOriginal, kw) {
			count++
		}
	}
	return count
}

// containsAnySubstring returns true if text contains any of the given substrings.
func containsAnySubstring(text string, substrings []string) bool {
	for _, sub := range substrings {
		if strings.Contains(text, sub) {
			return true
		}
	}
	return false
}
