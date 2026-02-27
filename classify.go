package main

import (
	"strings"
	"unicode/utf8"
)

// RequestComplexity categorizes how complex a user request is.
// Used to decide session limits, model selection, and prompt depth.
type RequestComplexity int

const (
	ComplexitySimple   RequestComplexity = 0
	ComplexityStandard RequestComplexity = 1
	ComplexityComplex  RequestComplexity = 2
)

// String returns the human-readable name of the complexity level.
func (c RequestComplexity) String() string {
	switch c {
	case ComplexitySimple:
		return "simple"
	case ComplexityStandard:
		return "standard"
	case ComplexityComplex:
		return "complex"
	default:
		return "standard"
	}
}

// complexityMaxSessionMessages returns the maximum number of session messages
// allowed for the given complexity level.
func complexityMaxSessionMessages(c RequestComplexity) int {
	switch c {
	case ComplexitySimple:
		return 5
	case ComplexityStandard:
		return 10
	case ComplexityComplex:
		return 20
	default:
		return 10
	}
}

// complexityMaxSessionChars returns the maximum total character budget
// for session output at the given complexity level.
func complexityMaxSessionChars(c RequestComplexity) int {
	switch c {
	case ComplexitySimple:
		return 4000
	case ComplexityStandard:
		return 8000
	case ComplexityComplex:
		return 16000
	default:
		return 8000
	}
}

// Chat-like sources that may qualify for simple classification.
var chatSources = map[string]bool{
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

// Sources that always indicate complex work.
var complexSources = map[string]bool{
	"cron":       true,
	"workflow":   true,
	"agent-comm": true,
}

// Coding-related keywords (English). Matched as whole words (word-boundary aware).
var complexKeywordsEN = []string{
	"code", "implement", "build", "debug", "refactor", "deploy",
	"api", "database", "sql", "function", "algorithm",
	"compile", "test", "migration", "schema", "endpoint",
	"infrastructure", "architecture", "pipeline", "optimize",
	"benchmark", "profiling", "concurrency", "mutex",
	"authentication", "authorization", "encryption",
}

// Coding-related keywords (Japanese). Matched as substrings (no word boundaries in Japanese).
var complexKeywordsJA = []string{
	"コード", "実装", "デバッグ", "リファクタ", "デプロイ",
	"データベース", "アルゴリズム", "コンパイル", "テスト",
	"マイグレーション", "スキーマ", "エンドポイント",
	"インフラ", "アーキテクチャ", "パイプライン", "最適化",
	"ベンチマーク", "プロファイリング", "並行処理",
	"認証", "暗号化", "関数", "設計",
}

// classifyComplexity determines the complexity of a user request based on
// the prompt text and the message source (e.g. "discord", "cron").
func classifyComplexity(prompt string, source string) RequestComplexity {
	srcLower := strings.ToLower(strings.TrimSpace(source))
	runeLen := utf8.RuneCountInString(prompt)

	// Source-based overrides: complex sources always yield complex.
	if complexSources[srcLower] {
		return ComplexityComplex
	}

	// Very long prompts are complex regardless of content.
	if runeLen > 2000 {
		return ComplexityComplex
	}

	promptLower := strings.ToLower(prompt)

	// Taskboard tasks: smarter classification to control token injection costs.
	// Simple for short tasks, Standard by default, Complex only for genuinely
	// complex tasks (3+ coding keywords) that need deep thinking.
	if srcLower == "taskboard" {
		if runeLen < 100 {
			return ComplexitySimple
		}
		kwCount := countComplexKeywords(promptLower, prompt)
		if kwCount >= 3 {
			return ComplexityComplex
		}
		return ComplexityStandard
	}

	// Check for coding-related keywords (case-insensitive, whole-word match).
	// containsWord is defined in sentiment.go with word-boundary logic.
	if containsAnyComplexWord(promptLower, complexKeywordsEN) {
		return ComplexityComplex
	}
	// Japanese keywords: substring match is correct since Japanese has no word boundaries.
	if containsAnySubstring(prompt, complexKeywordsJA) {
		return ComplexityComplex
	}

	// Short chat messages from chat-like sources are simple.
	if runeLen < 100 && chatSources[srcLower] {
		return ComplexitySimple
	}

	return ComplexityStandard
}

// containsAnyComplexWord returns true if text contains any keyword as a whole word.
// Uses containsWord from sentiment.go for word-boundary detection.
func containsAnyComplexWord(text string, keywords []string) bool {
	for _, kw := range keywords {
		if containsWord(text, kw) {
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
		if containsWord(textLower, kw) {
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
