package dispatch

import (
	"strings"
	"unicode/utf8"
)

// ChatSources maps source names that indicate interactive chat context.
// Inlined from archived internal/classify package.
var ChatSources = map[string]bool{
	"chat": true, "discord": true, "telegram": true, "slack": true,
	"whatsapp": true, "line": true, "matrix": true, "teams": true,
	"signal": true, "gchat": true, "imessage": true,
}

// Complexity categorizes how complex a user request is.
// Inlined from archived internal/classify package.
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

// Classify determines the complexity of a user request based on
// the prompt text and the message source (e.g. "discord", "cron").
func Classify(prompt string, source string) Complexity {
	srcLower := strings.ToLower(strings.TrimSpace(source))
	runeLen := utf8.RuneCountInString(prompt)

	if complexSources[srcLower] {
		return Complex
	}

	if runeLen > 2000 {
		return Complex
	}

	promptLower := strings.ToLower(prompt)

	if srcLower == "taskboard" || keywordClassifiedSources[srcLower] {
		if runeLen < 100 {
			return Simple
		}
		if countComplexKeywords(promptLower, prompt) >= 3 {
			return Complex
		}
		return Standard
	}

	if containsAnyComplexWord(promptLower, complexKeywordsEN) {
		return Complex
	}
	if containsAnySubstring(prompt, complexKeywordsJA) {
		return Complex
	}

	if runeLen < 100 && chatSources[srcLower] {
		return Simple
	}

	return Standard
}

var chatSources = map[string]bool{
	"chat": true, "discord": true, "telegram": true, "slack": true,
	"whatsapp": true, "line": true, "matrix": true, "teams": true,
	"signal": true, "gchat": true, "imessage": true,
}

var complexSources = map[string]bool{"agent-comm": true}

var keywordClassifiedSources = map[string]bool{"cron": true, "workflow": true}

var complexKeywordsEN = []string{
	"code", "implement", "build", "debug", "refactor", "deploy",
	"api", "database", "sql", "function", "algorithm",
	"compile", "test", "migration", "schema", "endpoint",
	"infrastructure", "architecture", "pipeline", "optimize",
	"benchmark", "profiling", "concurrency", "mutex",
	"authentication", "authorization", "encryption",
}

var complexKeywordsJA = []string{
	"コード", "実装", "デバッグ", "リファクタ", "デプロイ",
	"データベース", "アルゴリズム", "コンパイル", "テスト",
	"マイグレーション", "スキーマ", "エンドポイント",
	"インフラ", "アーキテクチャ", "パイプライン", "最適化",
	"ベンチマーク", "プロファイリング", "並行処理",
	"認証", "暗号化", "関数", "設計",
}

func containsAnyComplexWord(text string, keywords []string) bool {
	for _, kw := range keywords {
		if containsWord(text, kw) {
			return true
		}
	}
	return false
}

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

func containsAnySubstring(text string, substrings []string) bool {
	for _, sub := range substrings {
		if strings.Contains(text, sub) {
			return true
		}
	}
	return false
}

// containsWord checks if text contains word with basic word-boundary logic.
func containsWord(text, word string) bool {
	idx := 0
	for {
		pos := strings.Index(text[idx:], word)
		if pos < 0 {
			return false
		}
		absPos := idx + pos
		endPos := absPos + len(word)
		beforeOK := absPos == 0 || !isLetterByte(text[absPos-1])
		afterOK := endPos >= len(text) || !isLetterByte(text[endPos])
		if beforeOK && afterOK {
			return true
		}
		idx = absPos + utf8.RuneLen(rune(text[absPos]))
		if idx >= len(text) {
			return false
		}
	}
}

func isLetterByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// MaxSessionMessages returns the maximum number of session messages for the given complexity.
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

// MaxSessionChars returns the maximum total character budget for session output.
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
